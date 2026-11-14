//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Mozgatás meglévő mappába (összefésülés): a felül nem írt fájl forrása nem veszhet el.
func TestMoveMergeKeepsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"forras/A/egy.txt":   "új egy",
		"forras/A/ketto.txt": "új kettő",
		"cel/A/egy.txt":      "régi egy",
	})
	pr := &testProg{overwrite: false}
	must(t, copyItems([]string{filepath.Join(dir, "forras/A")}, filepath.Join(dir, "cel"), "", true, pr))
	if b, _ := os.ReadFile(filepath.Join(dir, "cel/A/egy.txt")); string(b) != "régi egy" {
		t.Errorf("felülírta, pedig nemet mondtunk: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "cel/A/ketto.txt")); string(b) != "új kettő" {
		t.Errorf("a nem ütköző fájl nem ment át: %q", b)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "forras/A/egy.txt")); err != nil || string(b) != "új egy" {
		t.Errorf("ADATVESZTÉS: a ki nem hagyott forrás eltűnt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "forras/A/ketto.txt")); err == nil {
		t.Error("az áthelyezett fájl a forrásban maradt")
	}
}

// Másolás: alapesetek, ékezetes nevek, felülírás, önmagába másolás tiltása.
func TestCopyItems(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"f/árvíz.txt": "a", "f/al/b.txt": "b", "g/x": "x"})
	pr := &testProg{}
	must(t, copyItems([]string{filepath.Join(dir, "f")}, filepath.Join(dir, "g"), "", false, pr))
	if readTree(t, filepath.Join(dir, "g"))["f/al/b.txt"] != "b" || len(pr.errs) > 0 {
		t.Errorf("másolás: %v %v", readTree(t, filepath.Join(dir, "g")), pr.errs)
	}
	pr = &testProg{}
	must(t, copyItems([]string{filepath.Join(dir, "f")}, filepath.Join(dir, "f", "al"), "", false, pr))
	if len(pr.errs) != 1 {
		t.Errorf("önmagába másolás: %v", pr.errs)
	}
	// átnevezve másolás
	must(t, copyItems([]string{filepath.Join(dir, "g/x")}, filepath.Join(dir, "g"), "y", false, &testProg{}))
	if b, _ := os.ReadFile(filepath.Join(dir, "g/y")); string(b) != "x" {
		t.Error("új néven másolás")
	}
	// egyszerű mozgatás
	must(t, copyItems([]string{filepath.Join(dir, "g/y")}, filepath.Join(dir, "f"), "", true, &testProg{}))
	if _, err := os.Stat(filepath.Join(dir, "g/y")); err == nil {
		t.Error("mozgatás után a forrás megmaradt")
	}
}

func TestUdisksOutput(t *testing.T) {
	for _, s := range []string{"Mounted /dev/sdb1 at /run/media/zs/MY STICK", "Mounted /dev/sdb1 at /run/media/zs/MY STICK."} {
		if m := mountedAt.FindStringSubmatch(s); m == nil || m[1] != "/run/media/zs/MY STICK" {
			t.Errorf("%q → %v", s, m)
		}
	}
	if deviceStamp() == "" {
		t.Error("üres bélyeg")
	}
}

func TestAppIcon(t *testing.T) {
	d := t.TempDir()
	t.Setenv("XDG_DATA_DIRS", d)
	t.Setenv("HOME", t.TempDir())
	writeTree(t, d, map[string]string{
		"applications/org.gimp.GIMP.desktop":   "[Desktop Entry]\nName=GIMP\nExec=env GDK=1 /usr/bin/gimp-2.10 %U\nIcon=gimp\n[Desktop Action new]\nIcon=rossz\n",
		"icons/hicolor/48x48/apps/gimp.png":    "PNG",
		"icons/hicolor/scalable/apps/kate.svg": "<svg/>",
		"applications/kate.desktop":            "[Desktop Entry]\nExec=kate -b %U\nIcon=kate\n",
	})
	if r := appIconFor("gimp-2.10", ""); r == nil || r.Name() != "gimp.png" {
		t.Errorf("gimp: %v", r)
	}
	if r := appIconFor("/usr/bin/kate", ""); r == nil || r.Name() != "kate.svg" {
		t.Errorf("kate: %v", r)
	}
	if r := appIconFor("ismeretlen", ""); r != nil {
		t.Error("ismeretlen programhoz ikon")
	}
	if r := appIconFor("ismeretlen2", filepath.Join(d, "icons/hicolor/48x48/apps/gimp.png")); r == nil {
		t.Error("megadott képfájl")
	}
}

func TestRemovableMount(t *testing.T) {
	m := "/dev/nvme0n1p2 / btrfs rw 0 0\n" +
		"/dev/sdb1 /run/media/gozie/PEN\\040DRIVE vfat rw 0 0\n" +
		"/dev/sdc1 /media/usb ext4 rw 0 0\n" +
		"tmpfs /run/media/gozie/x tmpfs rw 0 0\n"
	for _, c := range []struct{ dir, dev, mnt string }{
		{"/run/media/gozie/PEN DRIVE/Fotók", "/dev/sdb1", "/run/media/gozie/PEN DRIVE"},
		{"/run/media/gozie/PEN DRIVE", "/dev/sdb1", "/run/media/gozie/PEN DRIVE"},
		{"/media/usb/a", "/dev/sdc1", "/media/usb"},
		{"/home/gozie", "", ""},
		{"/run/media/gozie/x", "", ""},
		{"/run/media/gozie/PEN DRIVEX", "", ""},
	} {
		dev, mnt, ok := removableMountIn(m, c.dir)
		if dev != c.dev || mnt != c.mnt || ok != (c.dev != "") {
			t.Errorf("%s: %q %q %v", c.dir, dev, mnt, ok)
		}
	}
}
