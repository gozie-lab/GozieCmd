//go:build linux

package main

// Flatpak-támogatás. A Flatpak a programot egy zárt környezetben (sandbox) futtatja:
// a fájlrendszert látja (--filesystem=host), de a rendszer programjait nem. Ezért a
// külső programokat (szerkesztő, terminál, eszköztár, fájlmegnyitás, udisksctl, gio)
// a gazdarendszeren indítjuk, a flatpak-spawn --host segítségével. A 7-Zip a
// csomagban van, az a sandboxon belül fut.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// inFlatpak: Flatpak-csomagból fut-e a program.
var inFlatpak = os.Getenv("FLATPAK_ID") != ""

// hostRoot: a gazdarendszer /usr-je itt látszik (--filesystem=host-os).
var hostRoot = "/run/host"

// flatpakSetup: indításkor, minden más előtt. Az ideiglenes fájlok olyan helyre
// kerülnek, amelyet a gazdarendszer programjai is látnak (a sandbox /tmp-je saját),
// így például egy archívumból szerkesztésre kibontott fájl is megnyitható.
func flatpakSetup() {
	if !inFlatpak {
		return
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home, _ := os.UserHomeDir()
		cache = filepath.Join(home, ".var", "app", os.Getenv("FLATPAK_ID"), "cache")
	}
	tmp := filepath.Join(cache, "tmp")
	if os.MkdirAll(tmp, 0o700) == nil {
		os.Setenv("TMPDIR", tmp)
	}
}

// hostify: a parancsot a gazdarendszeren futtatja (Flatpakban); egyébként változatlan.
func hostify(c *exec.Cmd) *exec.Cmd {
	if !inFlatpak {
		return c
	}
	args := []string{"--host"}
	if c.Dir != "" {
		args = append(args, "--directory="+c.Dir)
	}
	args = append(args, c.Args...)
	h := exec.Command("flatpak-spawn", args...)
	h.Dir = c.Dir
	h.Stdin, h.Stdout, h.Stderr = c.Stdin, c.Stdout, c.Stderr
	return h
}

// hostPath: a gazdarendszer egy útvonala úgy, ahogy a programból olvasható.
func hostPath(p string) string {
	if !inFlatpak || !filepath.IsAbs(p) {
		return p
	}
	home, _ := os.UserHomeDir()
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return p // a saját mappa ugyanott látszik
	}
	return hostRoot + p
}

// hostLookPath: a gazdarendszeren elérhető program teljes útvonala (a gazdarendszer
// szerinti alakban), mint az exec.LookPath.
func hostLookPath(name string) (string, error) {
	if !inFlatpak {
		return exec.LookPath(name)
	}
	isExe := func(p string) bool {
		st, err := os.Stat(hostPath(p))
		return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
	}
	if strings.Contains(name, "/") {
		if isExe(name) {
			return name, nil
		}
		return "", exec.ErrNotFound
	}
	home, _ := os.UserHomeDir()
	for _, d := range []string{filepath.Join(home, ".local/bin"), "/usr/local/bin", "/usr/bin", "/bin", "/usr/local/sbin", "/usr/sbin", "/sbin"} {
		if p := filepath.Join(d, name); isExe(p) {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// hostDataDirs: a gazdarendszer XDG-adatmappái (alkalmazásikonok, .desktop fájlok).
func hostDataDirs() []string {
	home, _ := os.UserHomeDir()
	data := os.Getenv("HOST_XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local/share")
	}
	dirs := []string{data, filepath.Join(home, ".local/share/flatpak/exports/share"), "/var/lib/flatpak/exports/share"}
	if inFlatpak {
		return append(dirs, hostRoot+"/usr/local/share", hostRoot+"/usr/share")
	}
	sys := os.Getenv("XDG_DATA_DIRS")
	if sys == "" {
		sys = "/usr/local/share:/usr/share"
	}
	return append(dirs, filepath.SplitList(sys)...)
}
