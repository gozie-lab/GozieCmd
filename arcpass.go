package main

// Jelszóval védett archívumok (ZIP, 7Z, RAR). A jelszót csak a program futása
// alatt, a memóriában tartjuk (archívumonként), soha nem mentjük.
//
// Megnyitáskor (felület szála) a hiba *needPasswordError: a felület bekéri a
// jelszót, és újra megnyitja. Kibontás közben (munkaszál) a passwordPrompt
// függvényt hívjuk, ami a felületen kérdez és megvárja a választ.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	yzip "github.com/yeka/zip"
)

var (
	pwMu         sync.Mutex
	arcPasswords = map[string]string{}

	// passwordPrompt: a felület adja meg; munkaszálról hívható, blokkol. ok=false: Mégse.
	passwordPrompt func(archive string, wrong bool) (pw string, ok bool)
)

type needPasswordError struct {
	Path  string
	Wrong bool
}

func (e *needPasswordError) Error() string {
	if e.Wrong {
		return T("hibás jelszó")
	}
	return T("jelszóval védett archívum – meg kell adni a jelszót")
}

func archivePassword(p string) (string, bool) {
	pwMu.Lock()
	defer pwMu.Unlock()
	pw, ok := arcPasswords[filepath.Clean(p)]
	return pw, ok
}

func setArchivePassword(p, pw string) {
	pwMu.Lock()
	arcPasswords[filepath.Clean(p)] = pw
	pwMu.Unlock()
}

func forgetArchivePassword(p string) {
	pwMu.Lock()
	delete(arcPasswords, filepath.Clean(p))
	pwMu.Unlock()
}

func asNeedPassword(err error) (*needPasswordError, bool) {
	var npe *needPasswordError
	ok := errors.As(err, &npe)
	return npe, ok
}

// withPassword munkaszálon futtatja az fn-t; ha jelszó kell (vagy rossz volt),
// bekéri és újrapróbálja. Mégse esetén errCancel.
func withPassword(path string, fn func() error) error {
	for tries := 0; ; tries++ {
		err := fn()
		npe, ok := asNeedPassword(err)
		if !ok {
			return err
		}
		if passwordPrompt == nil || tries >= 5 {
			return err
		}
		forgetArchivePassword(path)
		pw, ok := passwordPrompt(filepath.Base(path), npe.Wrong)
		if !ok {
			return errCancel
		}
		setArchivePassword(path, pw)
	}
}

// sevenHeaderEncrypted: a 7Z fejléce (a fájlnevek) is titkosítva van-e. A titkosított
// fejlécben szerepel az AES kódoló azonosítója (06 F1 07 01) a fájl végén lévő
// kódolt fejléc leírásában; a „következő fejléc” helyét a kezdőfejléc adja meg.
func sevenHeaderEncrypted(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var sig [32]byte
	if _, err := io.ReadFull(f, sig[:]); err != nil || !bytes.Equal(sig[:6], []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}) {
		return false
	}
	le := func(b []byte) int64 {
		var v int64
		for i := 7; i >= 0; i-- {
			v = v<<8 | int64(b[i])
		}
		return v
	}
	off, size := le(sig[12:20]), le(sig[20:28])
	if size <= 0 || size > 1<<20 {
		return false
	}
	buf := make([]byte, size)
	if _, err := f.ReadAt(buf, 32+off); err != nil {
		return false
	}
	return len(buf) > 0 && buf[0] == 0x17 && bytes.Contains(buf, []byte{0x06, 0xF1, 0x07, 0x01})
}

// openArchiveWorker: munkaszálon nyit meg archívumot; titkosított fejlécnél bekéri a jelszót.
func openArchiveWorker(p string) (Archive, error) {
	var z Archive
	err := withPassword(p, func() error {
		var err error
		z, err = openArchive(p)
		return err
	})
	return z, err
}

// Jelszavas becsomagolás: a becsomagolás idejére beállított értékek (a 7-Zip ezeket kapja).
var (
	packPassword     string
	packEncryptNames bool
)

// packZipEncrypted új, AES-256-tal titkosított ZIP-et készít (a fájlnevek a ZIP-ben
// nem titkosíthatók). Meglévő archívumba nem ír.
func packZipEncrypted(srcs []string, dest, pw string, pr Progress) (err error) {
	if _, e := os.Stat(dest); e == nil {
		return errors.New(T("jelszóval csak új ZIP-archívum hozható létre – ez a fájl már létezik"))
	}
	var total int64
	for _, s := range srcs {
		filepath.Walk(s, func(_ string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode().IsRegular() {
				total += fi.Size()
			}
			return nil
		})
	}
	pr.SetTotal(total)
	tmp := dest + ".gozie-tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	zw := yzip.NewWriter(out)
	for _, src := range srcs {
		base := filepath.Dir(src)
		err = filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				pr.Err(p, err)
				return nil
			}
			if pr.Cancelled() {
				return errCancel
			}
			rel, _ := filepath.Rel(base, p)
			name := filepath.ToSlash(rel)
			h, herr := yzip.FileInfoHeader(fi)
			if herr != nil {
				return herr
			}
			h.Name = name
			if strings.ContainsFunc(name, func(r rune) bool { return r > 127 }) {
				h.Flags |= 0x800 // UTF-8 név
			}
			if fi.IsDir() {
				h.Name += "/"
				_, err := zw.CreateHeader(h)
				return err
			}
			if !fi.Mode().IsRegular() {
				return nil // szimbolikus link stb.
			}
			h.Method = yzip.Deflate
			h.SetPassword(pw)
			h.SetEncryptionMethod(yzip.AES256Encryption)
			w, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			f, err := os.Open(p)
			if err != nil {
				pr.Err(p, err)
				return nil
			}
			defer f.Close()
			pr.Status(name)
			_, err = io.Copy(w, progressReader{f, pr})
			return err
		})
		if err != nil {
			zw.Close()
			out.Close()
			return err
		}
	}
	if err = zw.Close(); err != nil {
		out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
