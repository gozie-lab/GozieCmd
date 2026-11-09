package main

// Feltöltés közben folyamatos listázás: minden fájlnak bájtra egyeznie kell.

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSFTPUploadIntegrity(t *testing.T) {
	root := t.TempDir()
	host, port := startSFTP(t, root)
	conf := remoteConf{Proto: "sftp", Host: host, Port: port, User: "teszt", Pass: "titok",
		KnownHosts: filepath.Join(t.TempDir(), "kh"), ConfirmHost: func(string, string, bool) bool { return true }}
	f, err := dialRemote(conf)
	must(t, err)
	defer f.Close()
	local := t.TempDir()
	src := filepath.Join(local, "kepek")
	os.MkdirAll(src, 0o755)
	want := map[string][]byte{}
	for i := 0; i < 20; i++ {
		b := make([]byte, 100_000+i*150_000)
		rand.Read(b)
		n := fmt.Sprintf("f%02d.bin", i)
		os.WriteFile(filepath.Join(src, n), b, 0o644)
		want[n] = b
	}
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				f.List(root)
				f.List(filepath.Join(root, "kepek"))
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()
	err = f.Upload([]string{src}, root, nopProgress{})
	close(stop)
	must(t, err)
	for n, b := range want {
		got, err := os.ReadFile(filepath.Join(root, "kepek", n))
		if err != nil || !bytes.Equal(got, b) {
			t.Errorf("%s: %d / %d bájt (%v)", n, len(got), len(b), err)
		}
	}
}

// FTPS (TLS) feltöltés: külső tesztszerverrel (GZ_FTPS_ROOT, 127.0.0.1:2122). Sok kis és
// nagyobb fájl egymás után; mindnek bájtra egyeznie kell.
func TestFTPSUploadIntegrity(t *testing.T) {
	root := os.Getenv("GZ_FTPS_ROOT")
	if root == "" {
		t.Skip("nincs FTPS-tesztszerver")
	}
	f, err := dialRemote(remoteConf{Proto: "ftp", Host: "127.0.0.1", Port: 2122, User: "teszt", Pass: "jelszo", TLS: true, Insecure: true})
	must(t, err)
	defer f.Close()
	local := t.TempDir()
	src := filepath.Join(local, fmt.Sprintf("ftps%d", time.Now().UnixNano()))
	os.MkdirAll(src, 0o755)
	want := map[string][]byte{}
	for i := 0; i < 12; i++ {
		b := make([]byte, 3000+i*37_000)
		rand.Read(b)
		n := fmt.Sprintf("f%02d.woff", i)
		os.WriteFile(filepath.Join(src, n), b, 0o644)
		want[n] = b
	}
	done := make(chan error, 1)
	pr := &errCount{}
	go func() { done <- f.Upload([]string{src}, f.StartDir(), pr) }()
	select {
	case err := <-done:
		must(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("a feltöltés elakadt")
	}
	for n, b := range want {
		got, err := os.ReadFile(filepath.Join(root, filepath.Base(src), n))
		if err != nil || !bytes.Equal(got, b) {
			t.Errorf("%s: %d / %d bájt (%v)", n, len(got), len(b), err)
		}
	}
	if pr.n != 0 {
		t.Errorf("%d hiba", pr.n)
	}
	exercise(t, f, f.StartDir()) // listázás, letöltés, törlés TLS-sel
}
