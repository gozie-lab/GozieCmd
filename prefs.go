//go:build windows

package main

// Kezelési beállítások: eszköztár (megjelenés, ikonméret), jobb gombos kijelölés,
// beállításfájl (hordozható mód, mentés és betöltés).

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/lxn/walk"
)

const portableFile = "GozieCmd.ini"

var iconSizes = []int{16, 20, 24, 32, 40, 48}

func iconSizeNames() []string {
	out := make([]string, len(iconSizes))
	for i, s := range iconSizes {
		out[i] = fmt.Sprintf(T("%s képpont"), strconv.Itoa(s))
	}
	return out
}

func iconSizeIndex(size int) int {
	for i, s := range iconSizes {
		if s == size {
			return i
		}
	}
	return 0
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *App) toolbarVisible() bool {
	v, ok := a.settings.Get("toolbar.show")
	return !ok || v != "0"
}

func (a *App) toolbarIconSize() int {
	v, _ := a.settings.Get("toolbar.iconSize")
	if n, err := strconv.Atoi(v); err == nil && iconSizeIndex(n) >= 0 && n >= 16 && n <= 48 {
		return n
	}
	return 24
}

func (a *App) rightSelect() bool {
	v, _ := a.settings.Get("rightSelect")
	return v == "1"
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// openSettings a hordozható (program melletti) fájlt használja, ha van; különben az AppData-t.
func openSettings() (*walk.IniFileSettings, bool) {
	pp := filepath.Join(exeDir(), portableFile)
	if _, err := os.Stat(pp); err == nil {
		s := walk.NewIniFileSettings(pp)
		s.SetPortable(true)
		return s, true
	}
	return walk.NewIniFileSettings("settings.ini"), false
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Dir(dst), 0o777)
	return os.WriteFile(dst, b, 0o666)
}

func (a *App) switchSettings(s *walk.IniFileSettings, portable bool) {
	s.Load()
	a.settings = s
	a.portable = portable
	walk.App().SetSettings(s)
}

// setPortable a beállításokat a program mellé (vagy vissza az AppData-ba) költözteti.
func (a *App) setPortable(on bool) {
	a.settings.Save()
	from := a.settings.FilePath()
	if on {
		to := filepath.Join(exeDir(), portableFile)
		if err := copyFile(from, to); err != nil {
			walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A beállításfájl nem írható a program mappájába:\n%s\n\n%s\n\nVédett helyen (pl. Program Files) a hordozható mód nem használható."), to, errText(err)), walk.MsgBoxIconWarning)
			return
		}
		s := walk.NewIniFileSettings(to)
		s.SetPortable(true)
		a.switchSettings(s, true)
		dlog("hordozható mód: %s", to)
		return
	}
	s := walk.NewIniFileSettings("settings.ini")
	if err := copyFile(from, s.FilePath()); err != nil {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A beállítások nem másolhatók vissza:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return
	}
	os.Remove(from)
	a.switchSettings(s, false)
	dlog("hordozható mód kikapcsolva: %s", s.FilePath())
}

func (a *App) exportSettings(owner walk.Form) {
	savePlacement(a, a.mw.Handle(), "main")
	a.settings.Save()
	fd := walk.FileDialog{Title: T("Beállítások mentése"), Filter: T("Beállításfájl (*.ini)|*.ini"), FilePath: portableFile}
	ok, _ := fd.ShowSave(owner)
	if !ok {
		return
	}
	dst := fd.FilePath
	if filepath.Ext(dst) == "" {
		dst += ".ini"
	}
	if err := copyFile(a.settings.FilePath(), dst); err != nil {
		walk.MsgBox(owner, appName, fmt.Sprintf(T("A mentés nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return
	}
	walk.MsgBox(owner, appName, fmt.Sprintf(T("A beállítások elmentve:\n%s\n\nMásik gépen a Beállítások → Kezelés → „Betöltés fájlból…” gombbal töltheted be, vagy tedd GozieCmd.ini néven a program mellé."), dst), walk.MsgBoxIconInformation)
}

func (a *App) importSettings(owner walk.Form) bool {
	fd := walk.FileDialog{Title: T("Beállítások betöltése"), Filter: T("Beállításfájl (*.ini)|*.ini")}
	ok, _ := fd.ShowOpen(owner)
	if !ok {
		return false
	}
	probe := walk.NewIniFileSettings(fd.FilePath)
	probe.SetPortable(true)
	if err := probe.Load(); err != nil {
		walk.MsgBox(owner, appName, fmt.Sprintf(T("Ez nem érvényes beállításfájl:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return false
	}
	target := a.settings.FilePath()
	if err := copyFile(fd.FilePath, target); err != nil {
		walk.MsgBox(owner, appName, fmt.Sprintf(T("A betöltés nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return false
	}
	s := walk.NewIniFileSettings(target)
	if a.portable {
		s.SetPortable(true)
	} else {
		s = walk.NewIniFileSettings("settings.ini")
	}
	a.switchSettings(s, a.portable)
	a.applyTheme()
	walk.MsgBox(a.mw, appName, T("A beállítások betöltve. Az ablak mérete és a panelek mappái a következő indításkor érvényesülnek."), walk.MsgBoxIconInformation)
	return true
}
