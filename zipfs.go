package main

// ZIP-archívum mint virtuális mappa: böngészés, kicsomagolás, hozzáadás, törlés.

import (
	"errors"

	"archive/zip"
	"compress/flate"
	"fmt"
	yzip "github.com/yeka/zip"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// decodeOEM a nem UTF-8 jelzésű régi ZIP-nevekhez (Windowson a 852-es kódlap).
var decodeOEM = func(b []byte) string { return string(b) }

type zipFS struct {
	archive  string
	children map[string][]*Entry
	dirs     map[string]*Entry
	files    map[string]*Entry
}

func openZipFS(archive string) (*zipFS, error) {
	z := &zipFS{archive: archive}
	return z, z.load()
}

func zipName(f *zip.File) string {
	n := f.Name
	if f.NonUTF8 {
		n = decodeOEM([]byte(n))
	}
	n = strings.ReplaceAll(n, `\`, "/")
	n = path.Clean("/" + n) // ".." nem vezethet ki a gyökérből
	return strings.TrimPrefix(n, "/")
}

func zipIsDir(f *zip.File) bool {
	return strings.HasSuffix(f.Name, "/") || strings.HasSuffix(f.Name, `\`) || f.Mode().IsDir()
}

func pdir(n string) string {
	if i := strings.LastIndex(n, "/"); i >= 0 {
		return n[:i]
	}
	return ""
}

func (z *zipFS) load() error {
	z.children = map[string][]*Entry{}
	z.dirs = map[string]*Entry{}
	z.files = map[string]*Entry{}
	r, err := zip.OpenReader(z.archive)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // új, még üres archívum
		}
		return err
	}
	defer r.Close()
	var ensureDir func(d string) *Entry
	ensureDir = func(d string) *Entry {
		if d == "" {
			return nil
		}
		if e, ok := z.dirs[d]; ok {
			return e
		}
		ensureDir(pdir(d))
		e := newEntry(path.Base(d), d, true, 0, time.Time{})
		z.dirs[d] = e
		z.children[pdir(d)] = append(z.children[pdir(d)], e)
		return e
	}
	for _, f := range r.File {
		name := zipName(f)
		if name == "" {
			continue
		}
		if zipIsDir(f) {
			if e := ensureDir(name); e != nil && e.ModTime.IsZero() {
				e.ModTime = f.Modified
			}
			continue
		}
		if _, dup := z.files[name]; dup || z.dirs[name] != nil {
			continue
		}
		ensureDir(pdir(name))
		e := newEntry(path.Base(name), name, false, int64(f.UncompressedSize64), f.Modified)
		z.files[name] = e
		z.children[pdir(name)] = append(z.children[pdir(name)], e)
	}
	return nil
}

func (z *zipFS) Kind() string        { return "arc" }
func (z *zipFS) Root() string        { return "" }
func (z *zipFS) Info() string        { return T("ZIP-archívum") }
func (z *zipFS) Close()              {}
func (z *zipFS) ArchivePath() string { return z.archive }
func (z *zipFS) Format() string      { return "zip" }
func (z *zipFS) Writable() bool      { return true }

// zipLevel az új ZIP-bejegyzések tömörítési szintje (0: tárolás tömörítés nélkül, 1–9).
var zipLevel = flate.DefaultCompression

func (z *zipFS) List(dir string) ([]*Entry, error) {
	src := z.children[dir]
	out := make([]*Entry, len(src))
	copy(out, src)
	return out, nil
}

func (z *zipFS) Parent(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	return pdir(dir), true
}

func (z *zipFS) Join(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func (z *zipFS) Display(dir string) string {
	if dir == "" {
		return z.archive
	}
	return z.archive + string(os.PathSeparator) + filepath.FromSlash(dir)
}

func inside(name, item string) bool {
	return name == item || strings.HasPrefix(name, item+"/")
}

// Extract a megadott elemeket (mappákat rekurzívan) a dstDir helyi mappába bontja ki.
func (z *zipFS) Extract(items []*Entry, dstDir string, pr Progress) error {
	r, err := zip.OpenReader(z.archive)
	if err != nil {
		return err
	}
	defer r.Close()
	type task struct {
		f   *zip.File
		rel string
	}
	var tasks []task
	var dirs []string
	var total int64
	for _, it := range items {
		strip := len(pdir(it.Path))
		if strip > 0 {
			strip++
		}
		if it.IsDir {
			dirs = append(dirs, it.Path[strip:])
		}
		for _, f := range r.File {
			n := zipName(f)
			if n == "" || !inside(n, it.Path) {
				continue
			}
			if zipIsDir(f) {
				dirs = append(dirs, n[strip:])
			} else {
				tasks = append(tasks, task{f, n[strip:]})
				total += int64(f.UncompressedSize64)
			}
		}
	}
	// titkosított bejegyzések: a jelszó bekérése és ellenőrzése a kibontás előtt
	var enc *yzip.ReadCloser
	for _, t := range tasks {
		if t.f.Flags&0x1 == 0 {
			continue
		}
		err := withPassword(z.archive, func() error {
			if enc != nil {
				enc.Close()
				enc = nil
			}
			pw, has := archivePassword(z.archive)
			if !has {
				return &needPasswordError{Path: z.archive}
			}
			yr, err := yzip.OpenReader(z.archive)
			if err != nil {
				return err
			}
			enc = yr
			yf := findYFile(yr, t.f.Name)
			if yf == nil {
				return os.ErrNotExist
			}
			yf.SetPassword(pw)
			rc, err := yf.Open()
			if err == nil {
				_, err = io.Copy(io.Discard, rc) // a CRC a végén ellenőrződik
				rc.Close()
			}
			if err != nil {
				return &needPasswordError{Path: z.archive, Wrong: true}
			}
			return nil
		})
		if err != nil {
			if enc != nil {
				enc.Close()
			}
			return err
		}
		break
	}
	if enc != nil {
		defer enc.Close()
	}
	pr.SetTotal(total)
	for _, d := range dirs {
		if p, ok := safeJoin(dstDir, d); ok {
			if err := os.MkdirAll(p, 0o777); err != nil {
				pr.Err(p, err)
			}
		}
	}
	for _, t := range tasks {
		if pr.Cancelled() {
			return errCancel
		}
		size := int64(t.f.UncompressedSize64)
		target, ok := safeJoin(dstDir, t.rel)
		if !ok {
			pr.Err(t.rel, os.ErrPermission)
			continue
		}
		if st, err := os.Stat(target); err == nil {
			if st.IsDir() {
				pr.Err(target, os.ErrExist)
				pr.Add(size)
				continue
			}
			ow, err := pr.AskOverwrite(target, fmt.Sprintf(T("Meglévő:  %s\nAz archívumban:  %s"), sizeAndTime(st.Size(), st.ModTime()), sizeAndTime(size, t.f.Modified)))
			if err != nil {
				return err
			}
			if !ow {
				pr.Add(size)
				continue
			}
			os.Chmod(target, 0o666)
		}
		pr.Status(fmt.Sprintf(T("Kicsomagolás: %s"), t.rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o777); err != nil {
			pr.Err(target, err)
			continue
		}
		var err error
		if t.f.Flags&0x1 != 0 && enc != nil {
			err = extractEncrypted(enc, t.f.Name, z.archive, target, pr)
		} else {
			err = extractOne(t.f, target, pr)
		}
		if err != nil {
			os.Remove(target)
			if err == errCancel {
				return err
			}
			pr.Err(target, err)
			continue
		}
		if !t.f.Modified.IsZero() {
			os.Chtimes(target, t.f.Modified, t.f.Modified)
		}
	}
	return nil
}

func findYFile(r *yzip.ReadCloser, name string) *yzip.File {
	for _, f := range r.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// extractEncrypted: titkosított (ZipCrypto vagy AES) bejegyzés kibontása.
func extractEncrypted(r *yzip.ReadCloser, name, archive, target string, pr Progress) error {
	f := findYFile(r, name)
	if f == nil {
		return os.ErrNotExist
	}
	pw, _ := archivePassword(archive)
	f.SetPassword(pw)
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	_, err = io.Copy(progressWriter{out, pr}, rc)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil && (errors.Is(err, yzip.ErrPassword) || errors.Is(err, yzip.ErrAuthentication) || errors.Is(err, yzip.ErrDecryption)) {
		err = &needPasswordError{Path: archive, Wrong: true}
	}
	return err
}

func extractOne(f *zip.File, target string, pr Progress) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	_, err = io.Copy(progressWriter{out, pr}, rc)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// rewrite új archívumot ír: a régi bejegyzéseket (a skip kivételével) tömörítés
// nélkül átmásolja, majd az add függvény hozzáírhat. Hiba esetén az eredeti marad.
func (z *zipFS) rewrite(skip func(name string) bool, add func(w *zip.Writer) error) error {
	tmp := z.archive + ".duotmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := zip.NewWriter(out)
	w.RegisterCompressor(zip.Deflate, func(o io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(o, zipLevel)
	})
	fail := func(e error) error {
		w.Close()
		out.Close()
		os.Remove(tmp)
		return e
	}
	r, err := zip.OpenReader(z.archive)
	switch {
	case err == nil:
		w.SetComment(r.Comment)
		for _, f := range r.File {
			if skip != nil && skip(zipName(f)) {
				continue
			}
			if err := w.Copy(f); err != nil {
				r.Close()
				return fail(err)
			}
		}
		r.Close()
	case !os.IsNotExist(err):
		return fail(err)
	}
	if add != nil {
		if err := add(w); err != nil {
			return fail(err)
		}
	}
	if err := w.Close(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, z.archive); err != nil {
		os.Remove(tmp)
		return err
	}
	return z.load()
}

// AddLocal helyi fájlokat/mappákat ír az archívum intoDir mappájába.
func (z *zipFS) AddLocal(srcs []string, intoDir string, pr Progress) error {
	type add struct {
		local string
		name  string
		info  os.FileInfo
	}
	var adds []add
	var total int64
	for _, src := range srcs {
		parent := filepath.Dir(src)
		filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				pr.Err(p, err)
				return nil
			}
			rel, rerr := filepath.Rel(parent, p)
			if rerr != nil {
				return nil
			}
			adds = append(adds, add{p, z.Join(intoDir, filepath.ToSlash(rel)), info})
			if !info.IsDir() {
				total += info.Size()
			}
			return nil
		})
	}
	replace := map[string]bool{}
	skipAdd := map[string]bool{}
	for _, a := range adds {
		if a.info.IsDir() {
			continue
		}
		if z.dirs[a.name] != nil {
			pr.Err(a.name, os.ErrExist)
			skipAdd[a.name] = true
			continue
		}
		if old, ok := z.files[a.name]; ok {
			ow, err := pr.AskOverwrite(z.Display(a.name), fmt.Sprintf(T("Az archívumban:  %s\nÚj:  %s"), sizeAndTime(old.Size, old.ModTime), sizeAndTime(a.info.Size(), a.info.ModTime())))
			if err != nil {
				return err
			}
			if ow {
				replace[a.name] = true
			} else {
				skipAdd[a.name] = true
				total -= a.info.Size()
			}
		}
	}
	pr.SetTotal(total)
	return z.rewrite(func(n string) bool { return replace[n] }, func(w *zip.Writer) error {
		for _, a := range adds {
			if pr.Cancelled() {
				return errCancel
			}
			if skipAdd[a.name] {
				continue
			}
			if a.info.IsDir() {
				if z.dirs[a.name] != nil {
					continue
				}
				h := &zip.FileHeader{Name: a.name + "/", Modified: a.info.ModTime()}
				h.SetMode(os.ModeDir | 0o755)
				if _, err := w.CreateHeader(h); err != nil {
					return err
				}
				continue
			}
			h, err := zip.FileInfoHeader(a.info)
			if err != nil {
				return err
			}
			h.Name = a.name
			h.Method = zip.Deflate
			if zipLevel == 0 {
				h.Method = zip.Store
			}
			h.Modified = a.info.ModTime()
			pr.Status(fmt.Sprintf(T("Tömörítés: %s"), a.name))
			wr, err := w.CreateHeader(h)
			if err != nil {
				return err
			}
			f, err := os.Open(a.local)
			if err != nil {
				return err
			}
			_, err = io.Copy(progressWriter{wr, pr}, f)
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// Delete elemeket (mappákat tartalmukkal) töröl az archívumból.
func (z *zipFS) Delete(items []*Entry, pr Progress) error {
	pr.Status(T("Törlés az archívumból…"))
	return z.rewrite(func(n string) bool {
		for _, it := range items {
			if inside(n, it.Path) {
				return true
			}
		}
		return false
	}, nil)
}

func (z *zipFS) Mkdir(dir, name string) error {
	full := z.Join(dir, strings.Trim(strings.ReplaceAll(name, `\`, "/"), "/"))
	return z.rewrite(nil, func(w *zip.Writer) error {
		h := &zip.FileHeader{Name: full + "/", Modified: time.Now()}
		h.SetMode(os.ModeDir | 0o755)
		_, err := w.CreateHeader(h)
		return err
	})
}
