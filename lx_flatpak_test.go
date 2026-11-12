//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFlatpakHost(t *testing.T) {
	oldIn, oldRoot := inFlatpak, hostRoot
	defer func() { inFlatpak, hostRoot = oldIn, oldRoot }()
	inFlatpak, hostRoot = true, t.TempDir()
	must(t, os.MkdirAll(filepath.Join(hostRoot, "usr/bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(hostRoot, "usr/bin/kate"), []byte("#!/bin/sh\n"), 0o755))
	must(t, os.WriteFile(filepath.Join(hostRoot, "usr/bin/nemfut"), []byte("x"), 0o644))

	if p, err := hostLookPath("kate"); err != nil || p != "/usr/bin/kate" {
		t.Fatalf("kate: %q %v", p, err)
	}
	if _, err := hostLookPath("nemfut"); err == nil {
		t.Fatal("nem futtatható fájlt is talált")
	}
	if p, err := hostLookPath("/usr/bin/kate"); err != nil || p != "/usr/bin/kate" {
		t.Fatalf("abszolút: %q %v", p, err)
	}
	if findExe("nincsilyen", "kate") != "/usr/bin/kate" {
		t.Fatal("findExe")
	}

	c := exec.Command("kate", "a b.txt")
	c.Dir = "/home/x"
	h := hostify(c)
	want := []string{"flatpak-spawn", "--host", "--directory=/home/x", "kate", "a b.txt"}
	if !reflect.DeepEqual(h.Args, want) {
		t.Fatalf("%q", h.Args)
	}
	home, _ := os.UserHomeDir()
	if hostPath(filepath.Join(home, "a")) != filepath.Join(home, "a") || hostPath("/usr/share/x") != hostRoot+"/usr/share/x" {
		t.Fatal("hostPath")
	}

	inFlatpak = false
	if hostify(c) != c {
		t.Fatal("Flatpakon kívül nem változhat")
	}
}

func TestFlatpakHasVersion(t *testing.T) {
	list := "org.mozilla.firefox\t140.0\nhu.gozie.commander\t0.52\nhu.gozie.commander\t0.53\n"
	if !flatpakHasVersion(list, "hu.gozie.commander", "0.53") || flatpakHasVersion(list, "hu.gozie.commander", "0.54") ||
		flatpakHasVersion(list, "hu.gozie", "0.52") {
		t.Fatal("rossz egyezés")
	}
}
