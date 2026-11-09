//go:build windows

package main

// Beállítások: színek, szerkesztő, terminál; ablakméretek mentése; programikon.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

var procMonitorFromRect = user32.NewProc("MonitorFromRect")

// ── külső programok keresése ──

type appChoice struct {
	label string
	path  string
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if !strings.ContainsAny(p, `\/`) {
			if lp, err := exec.LookPath(p); err == nil {
				return lp
			}
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func envPath(env, rel string) string {
	if v := os.Getenv(env); v != "" {
		return filepath.Join(v, rel)
	}
	return ""
}

// detectTerminals a telepített parancssori programokat sorolja fel.
func detectTerminals() []appChoice {
	var out []appChoice
	if p := firstExisting("wt.exe", envPath("LOCALAPPDATA", `Microsoft\WindowsApps\wt.exe`)); p != "" {
		out = append(out, appChoice{"Windows Terminal", p})
	}
	if p := firstExisting("pwsh.exe", envPath("ProgramFiles", `PowerShell\7\pwsh.exe`)); p != "" {
		out = append(out, appChoice{"PowerShell 7", p})
	}
	if p := firstExisting(envPath("SystemRoot", `System32\WindowsPowerShell\v1.0\powershell.exe`), "powershell.exe"); p != "" {
		out = append(out, appChoice{"Windows PowerShell", p})
	}
	out = append(out, appChoice{T("Parancssor (cmd)"), firstExisting(envPath("SystemRoot", `System32\cmd.exe`), "cmd.exe")})
	return out
}

// detectEditors a gyakori szövegszerkesztőket keresi.
func detectEditors() []appChoice {
	out := []appChoice{{T("Jegyzettömb"), firstExisting(envPath("SystemRoot", `System32\notepad.exe`), "notepad.exe")}}
	if p := firstExisting(envPath("ProgramFiles", `Notepad++\notepad++.exe`), envPath("ProgramFiles(x86)", `Notepad++\notepad++.exe`)); p != "" {
		out = append(out, appChoice{"Notepad++", p})
	}
	if p := firstExisting(envPath("LOCALAPPDATA", `Programs\Microsoft VS Code\Code.exe`), envPath("ProgramFiles", `Microsoft VS Code\Code.exe`)); p != "" {
		out = append(out, appChoice{"Visual Studio Code", p})
	}
	if p := firstExisting(envPath("ProgramFiles", `Sublime Text\sublime_text.exe`), envPath("ProgramFiles", `Sublime Text 3\sublime_text.exe`)); p != "" {
		out = append(out, appChoice{"Sublime Text", p})
	}
	return out
}

func (a *App) editorPath() string {
	if v, _ := a.settings.Get("editor"); v != "" {
		return v
	}
	if v := os.Getenv("GOZIE_EDITOR"); v != "" {
		return v
	}
	return "notepad.exe"
}

func (a *App) terminalPath() string {
	if v, _ := a.settings.Get("terminal"); v != "" {
		return v
	}
	return "cmd.exe"
}

func termKind(path string) string {
	switch strings.ToLower(filepath.Base(path)) {
	case "wt.exe":
		return "wt"
	case "pwsh.exe", "powershell.exe":
		return "ps"
	case "cmd.exe":
		return "cmd"
	}
	return "other"
}

func startProgram(exe, cmdline, dir string, flags uint32) error {
	c := exec.Command(exe)
	c.Dir = dir
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdline, CreationFlags: flags}
	if err := c.Start(); err != nil {
		return err
	}
	go c.Wait()
	return nil
}

func q(s string) string { return `"` + s + `"` }

// shellRun programot indít a Windows indítójával. (Az os/exec a gyermek bemenetét
// a NUL eszközre köti, amitől a parancssori ablak azonnal bezárulna.)
func shellRun(owner win.HWND, exe, params, dir string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, _ := syscall.UTF16PtrFromString(exe)
	var args *uint16
	if params != "" {
		args, _ = syscall.UTF16PtrFromString(params)
	}
	cwd, _ := syscall.UTF16PtrFromString(dir)
	if !win.ShellExecute(owner, verb, file, args, cwd, win.SW_SHOWNORMAL) {
		return fmt.Errorf(T("nem indítható: %s"), exe)
	}
	return nil
}

// qdir idézőjelbe tett mappa; a záró \ elé pontot tesz, hogy ne „escape-elje” az idézőjelet.
func qdir(dir string) string {
	if strings.HasSuffix(dir, `\`) {
		dir += "."
	}
	return q(dir)
}

// openTerminalAt a beállított terminált nyitja a mappában; command != "" esetén
// a parancsot futtatja benne, és az ablak nyitva marad.
func (a *App) openTerminalAt(dir, command string) error {
	exe := a.terminalPath()
	owner := a.mw.Handle()
	var params string
	switch termKind(exe) {
	case "wt":
		params = "-d " + qdir(dir)
		if command != "" {
			params += " cmd.exe /k " + command
		}
	case "ps":
		params = "-NoExit"
		if command != "" {
			params += " -Command " + command
		}
	case "cmd":
		if command != "" {
			params = "/k " + command
		}
	}
	dlog("terminál: %s %s (mappa: %s)", exe, params, dir)
	return shellRun(owner, exe, params, dir)
}

func (a *App) openInEditor(path, dir string) error {
	return shellRun(a.mw.Handle(), a.editorPath(), q(path), dir)
}

// ── ablakméret és -pozíció ──

func savePlacement(a *App, h win.HWND, key string) {
	var wp win.WINDOWPLACEMENT
	wp.Length = uint32(unsafe.Sizeof(wp))
	if !win.GetWindowPlacement(h, &wp) {
		return
	}
	r := wp.RcNormalPosition
	maxed := 0
	if wp.ShowCmd == win.SW_SHOWMAXIMIZED || win.IsZoomed(h) {
		maxed = 1
	}
	v := fmt.Sprintf("%d,%d,%d,%d,%d", r.Left, r.Top, r.Right, r.Bottom, maxed)
	a.settings.Put("win_"+key, v)
	dlog("ablakhely mentése (%s): %s", key, v)
}

// restorePlacement true, ha volt mentett, látható helyre eső méret.
func restorePlacement(a *App, h win.HWND, key string) bool {
	ok, _ := placeWindow(a, h, key, false)
	return ok
}

// placeWindow visszaállítja a mentett helyet. hidden esetén az ablak rejtve marad
// (csak a helye és a mérete áll be); a második érték, hogy teljes méretű volt-e.
func placeWindow(a *App, h win.HWND, key string, hidden bool) (bool, bool) {
	v, _ := a.settings.Get("win_" + key)
	parts := strings.Split(v, ",")
	if len(parts) != 5 {
		return false, false
	}
	var n [5]int32
	for i, s := range parts {
		x, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return false, false
		}
		n[i] = int32(x)
	}
	r := win.RECT{Left: n[0], Top: n[1], Right: n[2], Bottom: n[3]}
	if r.Right-r.Left < 200 || r.Bottom-r.Top < 150 {
		return false, false
	}
	if m, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), 0); m == 0 {
		return false, false // a mentett hely már nincs egyik képernyőn sem
	}
	dlog("ablakhely visszaállítása (%s): %s", key, v)
	maxed := n[4] == 1
	wp := win.WINDOWPLACEMENT{RcNormalPosition: r, ShowCmd: win.SW_SHOWNORMAL}
	switch {
	case hidden:
		wp.ShowCmd = win.SW_HIDE
	case maxed:
		wp.ShowCmd = win.SW_SHOWMAXIMIZED
	}
	wp.Length = uint32(unsafe.Sizeof(wp))
	return win.SetWindowPlacement(h, &wp), maxed
}

// sizeableDialog átméretezhetővé, teljes méretre nagyíthatóvá teszi a dialógust,
// megnyitáskor visszaállítja a mentett méretét (vagy az alapméretet a szülő közepén),
// bezáráskor pedig elmenti.
func (a *App) sizeableDialog(dlg *walk.Dialog, owner walk.Form, key string, w96, h96 int) {
	dlg.Starting().Attach(func() {
		h := dlg.Handle()
		style := win.GetWindowLong(h, win.GWL_STYLE)
		win.SetWindowLong(h, win.GWL_STYLE, style|win.WS_THICKFRAME|win.WS_MAXIMIZEBOX)
		win.SetWindowPos(h, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
		if a.icon != nil {
			dlg.SetIcon(a.icon)
		}
		if restorePlacement(a, h, key) {
			return
		}
		dpi := int32(dlg.DPI())
		w, hh := int32(w96)*dpi/96, int32(h96)*dpi/96
		var orc win.RECT
		if owner != nil {
			win.GetWindowRect(owner.Handle(), &orc)
		}
		x := orc.Left + (orc.Right-orc.Left-w)/2
		y := orc.Top + (orc.Bottom-orc.Top-hh)/2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		win.SetWindowPos(h, 0, x, y, w, hh, win.SWP_NOZORDER)
	})
	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		savePlacement(a, dlg.Handle(), key)
	})
}

// ── programikon ──

func loadAppIcon() *walk.Icon {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	ic, err := walk.NewIconExtractedFromFileWithSize(exe, 0, 32)
	if err != nil {
		dlog("ikon betöltése sikertelen: %v", err)
		return nil
	}
	return ic
}

// ── 7-Zip ──

var detected7z *string

func detect7z() string {
	if detected7z == nil {
		p := firstExisting(envPath("ProgramFiles", `7-Zip\7z.exe`), envPath("ProgramFiles(x86)", `7-Zip\7z.exe`),
			envPath("ProgramW6432", `7-Zip\7z.exe`), "7z.exe", "7za.exe")
		detected7z = &p
	}
	return *detected7z
}

func init() {
	sevenZipExe = func() string {
		if theApp != nil && theApp.settings != nil {
			if v, _ := theApp.settings.Get("sevenZip"); v != "" {
				if st, err := os.Stat(v); err == nil && !st.IsDir() {
					return v
				}
			}
		}
		return detect7z()
	}
	run7z = func(exe, dir string, args ...string) (string, error) {
		c := exec.Command(exe, args...)
		c.Dir = dir
		c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		out, err := c.CombinedOutput()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == 1 { // 1 = figyelmeztetés, az eredmény jó
				return string(out), nil
			}
			msg := strings.TrimSpace(string(out))
			if lines := strings.Split(msg, "\n"); len(lines) > 6 {
				msg = strings.Join(lines[len(lines)-6:], "\n")
			}
			return string(out), fmt.Errorf(T("7-Zip hiba: %v\n%s"), err, msg)
		}
		return string(out), nil
	}
}
