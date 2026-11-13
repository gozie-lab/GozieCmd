package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Szerkesztés távoli helyről: a mentést a figyelő észleli (csak ha már nem változik),
// és a visszaírás a meglévő fájlt kérdés nélkül felülírja.
func TestEditSessionWriteBack(t *testing.T) {
	root := t.TempDir()
	host, port := startSFTP(t, root)
	f, err := dialRemote(remoteConf{Proto: "sftp", Host: host, Port: port, User: "teszt", Pass: "titok",
		KnownHosts: filepath.Join(t.TempDir(), "kh"), ConfirmHost: func(string, string, bool) bool { return true }})
	must(t, err)
	defer f.Close()
	must(t, os.WriteFile(filepath.Join(root, "jegyzet.txt"), []byte("régi"), 0o644))

	tmp := t.TempDir()
	must(t, exportItems(f, []*Entry{{Name: "jegyzet.txt", Path: root + "/jegyzet.txt", Size: 5}}, tmp, nopProgress{}))
	local := filepath.Join(tmp, "jegyzet.txt")
	s := newEditSession(f, root, f.Info(), local)

	if c, g := s.poll(); c || g {
		t.Fatal("változatlan fájl változásnak látszik")
	}
	time.Sleep(20 * time.Millisecond)
	must(t, os.WriteFile(local, []byte("új tartalom"), 0o644))
	if c, _ := s.poll(); c {
		t.Fatal("az első észlelés még nem számít késznek")
	}
	if c, _ := s.poll(); !c {
		t.Fatal("a kész mentést nem észlelte")
	}
	must(t, s.writeBackQuiet())
	if b, _ := os.ReadFile(filepath.Join(root, "jegyzet.txt")); string(b) != "új tartalom" {
		t.Fatalf("visszaírt tartalom: %q", b)
	}
	if c, _ := s.poll(); c {
		t.Fatal("visszaírás után újra változást jelez")
	}
	must(t, os.Remove(local))
	if _, g := s.poll(); !g {
		t.Fatal("a törölt fájlt nem vette észre")
	}
}
