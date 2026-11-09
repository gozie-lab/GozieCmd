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
