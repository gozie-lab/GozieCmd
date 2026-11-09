//go:build linux

package main

// Külső programok ikonja az eszköztárhoz: a program .desktop fájljából (Icon=), a
// rendszer ikontémáiból, vagy a felhasználó által megadott képfájlból (PNG, SVG).

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
)

var (
	appIconMu    sync.Mutex
	appIconCache = map[string]fyne.Resource{}
)

func xdgDataDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{filepath.Join(home, ".local/share"), filepath.Join(home, ".local/share/flatpak/exports/share"), "/var/lib/flatpak/exports/share"}
	sys := os.Getenv("XDG_DATA_DIRS")
	if sys == "" {
		sys = "/usr/local/share:/usr/share"
	}
	dirs = append(dirs, filepath.SplitList(sys)...)
	return dirs
}

// desktopIconName: a programhoz tartozó .desktop fájl Icon= értéke.
func desktopIconName(exe string) string {
	base := filepath.Base(strings.Fields(exe + " ")[0])
	var byExec string
	for _, d := range xdgDataDirs() {
		appDir := filepath.Join(d, "applications")
		// gyors út: <program>.desktop
		if n := readDesktopIcon(filepath.Join(appDir, base+".desktop"), ""); n != "" {
			return n
		}
		if byExec != "" {
			continue
		}
		es, _ := os.ReadDir(appDir)
		for _, e := range es {
			if !strings.HasSuffix(e.Name(), ".desktop") {
				continue
			}
			if n := readDesktopIcon(filepath.Join(appDir, e.Name()), base); n != "" {
				byExec = n
				break
			}
		}
	}
	return byExec
}

// readDesktopIcon: a fájl Icon= sora; ha execBase nem üres, csak akkor, ha az Exec= erre a programra mutat.
func readDesktopIcon(path, execBase string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	icon, execOK := "", execBase == ""
	inEntry := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "[") {
			if inEntry {
				break
			}
			inEntry = l == "[Desktop Entry]"
			continue
		}
		if !inEntry {
			continue
		}
		switch {
		case strings.HasPrefix(l, "Icon="):
			icon = strings.TrimSpace(l[5:])
		case strings.HasPrefix(l, "Exec=") && execBase != "":
			for _, tok := range strings.Fields(l[5:]) {
				if strings.Contains(tok, "=") && !strings.Contains(tok, "/") { // env VAR=x
					continue
				}
				if tok == "env" {
					continue
				}
				execOK = filepath.Base(strings.Trim(tok, `"`)) == execBase
				break
			}
		}
	}
	if execOK {
		return icon
	}
	return ""
}

// findThemeIcon: ikonnév → fájl a szokásos ikontémákban (a nagyobb és a vektoros előnyben).
func findThemeIcon(name string) string {
	if name == "" {
		return ""
	}
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err == nil {
			return name
		}
		return ""
	}
	themes := []string{"hicolor", "breeze", "breeze-dark", "Adwaita", "Papirus", "Yaru", "elementary"}
	sizes := []string{"scalable", "256x256", "128x128", "96x96", "64x64", "48x48", "32x32", "24x24"}
	for _, d := range xdgDataDirs() {
		for _, th := range themes {
			for _, sz := range sizes {
				for _, cat := range []string{"apps", "categories", "places", "devices"} {
					for _, ext := range []string{".svg", ".png"} {
						p := filepath.Join(d, "icons", th, sz, cat, name+ext)
						if _, err := os.Stat(p); err == nil {
							return p
						}
					}
				}
			}
			// breeze: apps/48/név.svg felépítés
			for _, sz := range []string{"64", "48", "32"} {
				p := filepath.Join(d, "icons", th, "apps", sz, name+".svg")
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
		for _, ext := range []string{".svg", ".png"} {
			p := filepath.Join(d, "pixmaps", name+ext)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// appIconFor: a gomb ikonja (megadott kép vagy ikonnév, különben a program .desktop fájljából); nil, ha nincs.
func appIconFor(exe, icon string) fyne.Resource {
	key := exe + "\x00" + icon
	appIconMu.Lock()
	if r, ok := appIconCache[key]; ok {
		appIconMu.Unlock()
		return r
	}
	appIconMu.Unlock()
	var file string
	if icon != "" {
		file = findThemeIcon(icon)
	}
	if file == "" && exe != "" {
		file = findThemeIcon(desktopIconName(exe))
	}
	var res fyne.Resource
	if file != "" {
		if b, err := os.ReadFile(file); err == nil {
			res = fyne.NewStaticResource(filepath.Base(file), b)
		}
	}
	appIconMu.Lock()
	appIconCache[key] = res
	appIconMu.Unlock()
	return res
}
