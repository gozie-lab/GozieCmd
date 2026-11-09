//go:build linux

package main

// Külső programok Linuxon: megnyitás a társított programmal (xdg-open),
// szövegszerkesztő, terminál, 7-Zip; hibaszövegek magyarul.

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type progChoice struct {
	label, path string
}

func findExe(names ...string) string {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

var editorCandidates = []struct{ name, label string }{
	{"kate", T("Kate")}, {"kwrite", "KWrite"}, {"gnome-text-editor", T("GNOME Szövegszerkesztő")}, {"gedit", "gedit"},
	{"xed", T("Xed")}, {"pluma", T("Pluma")}, {"mousepad", T("Mousepad")}, {"featherpad", "FeatherPad"},
	{"code", "Visual Studio Code"}, {"codium", "VSCodium"}, {"zed", T("Zed")}, {"geany", T("Geany")},
}

func detectEditors() []progChoice {
	var out []progChoice
	for _, c := range editorCandidates {
		if p := findExe(c.name); p != "" {
			out = append(out, progChoice{c.label, p})
		}
	}
	return out
}

var terminalCandidates = []struct{ name, label string }{
	{"konsole", T("Konsole")}, {"ptyxis", T("Ptyxis")}, {"gnome-terminal", T("GNOME Terminál")}, {"kgx", T("GNOME Konzol")},
	{"xfce4-terminal", T("Xfce Terminál")}, {"mate-terminal", T("MATE Terminál")}, {"tilix", T("Tilix")},
	{"alacritty", T("Alacritty")}, {"kitty", "kitty"}, {"wezterm", "WezTerm"}, {"foot", "foot"},
	{"x-terminal-emulator", T("Alapértelmezett terminál")}, {"xterm", "XTerm"},
}

func detectTerminals() []progChoice {
	var out []progChoice
	for _, c := range terminalCandidates {
		if p := findExe(c.name); p != "" {
			out = append(out, progChoice{c.label, p})
		}
	}
	return out
}

func (a *App) editorPath() string {
	if v := a.setting("lx.editor", ""); v != "" {
		return v
	}
	if es := detectEditors(); len(es) > 0 {
		return es[0].path
	}
	return ""
}

func (a *App) terminalPath() string {
	if v := a.setting("lx.terminal", ""); v != "" {
		return v
	}
	// a futó asztali környezethez illő terminál előnyben
	de := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
	prefer := map[string][]string{
		"kde": {"konsole"}, "gnome": {"ptyxis", "kgx", "gnome-terminal"}, "x-cinnamon": {"gnome-terminal", "xfce4-terminal"},
		"cinnamon": {"gnome-terminal"}, "xfce": {"xfce4-terminal"}, "mate": {"mate-terminal"},
	}
	for k, names := range prefer {
		if strings.Contains(de, k) {
			if p := findExe(names...); p != "" {
				return p
			}
		}
	}
	if ts := detectTerminals(); len(ts) > 0 {
		return ts[0].path
	}
	return ""
}

// startDetached külön folyamatcsoportban indít, hogy a programunk bezárása ne vigye magával.
func startDetached(c *exec.Cmd) error {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Stdin, c.Stdout, c.Stderr = nil, nil, nil
	if err := c.Start(); err != nil {
		return err
	}
	go c.Wait()
	return nil
}

func (a *App) openFile(path string) {
	dir := filepath.Dir(path)
	if st, err := os.Stat(path); err == nil {
		if st.IsDir() {
			dir = path
		} else if st.Mode()&0o111 != 0 && !isArchive(path) && !hasDocExt(path) {
			// futtatható program: elindítjuk
			c := exec.Command(path)
			c.Dir = dir
			if err := startDetached(c); err == nil {
				return
			}
		}
	}
	opener := findExe("xdg-open", "gio", "kde-open", "gnome-open")
	if opener == "" {
		a.warn(T("Nem található program a fájl megnyitásához (xdg-open)."))
		return
	}
	args := []string{path}
	if filepath.Base(opener) == "gio" {
		args = []string{"open", path}
	}
	c := exec.Command(opener, args...)
	if st, err := os.Stat(dir); err == nil && st.IsDir() { // webcímnél nincs munkamappa
		c.Dir = dir
	}
	if err := startDetached(c); err != nil {
		a.warn(fmt.Sprintf(T("A fájl nem nyitható meg:\n%s"), errText(err)))
	}
}

func hasDocExt(p string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(p), ".")) {
	case "txt", "md", "pdf", "jpg", "jpeg", "png", "gif", "mp3", "mp4", "mkv", "doc", "docx", "odt", "xls", "xlsx", "ods", "html", "htm", "svg", "webp":
		return true
	}
	return false
}

func (a *App) openInEditor(path string) {
	ed := a.editorPath()
	if ed == "" {
		a.openFile(path)
		return
	}
	args := strings.Fields(ed)
	if _, err := exec.LookPath(args[0]); err != nil && !filepath.IsAbs(args[0]) {
		a.warn(fmt.Sprintf(T("A beállított szerkesztő nem található:\n%s"), ed))
		return
	}
	// terminálos szerkesztő (nano, vim, micro…): terminálban indul
	switch filepath.Base(args[0]) {
	case "nano", "vim", "vi", "nvim", "micro", "emacs", "mcedit", "helix", "hx", "joe", "ne":
		a.runInTerminal(shellQuote(args...)+" "+shellQuote(path), filepath.Dir(path))
		return
	}
	c := exec.Command(args[0], append(args[1:], path)...)
	c.Dir = filepath.Dir(path)
	if err := startDetached(c); err != nil {
		a.warn(fmt.Sprintf(T("A szerkesztő nem indítható:\n%s"), errText(err)))
	}
}

func shellQuote(args ...string) string {
	var out []string
	for _, s := range args {
		if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?&;|<>(){}[]#~") {
			out = append(out, s)
			continue
		}
		out = append(out, "'"+strings.ReplaceAll(s, "'", `'\''`)+"'")
	}
	return strings.Join(out, " ")
}

func (a *App) editFile() {
	p := a.active
	e := p.currentEntry()
	if e == nil || e.IsParent || e.IsDir {
		return
	}
	if p.vfs != nil {
		a.openVirtual(p, e, false)
		return
	}
	a.openInEditor(e.Path)
}

func (a *App) viewFile() {
	p := a.active
	e := p.currentEntry()
	if e == nil || e.IsParent {
		return
	}
	if e.IsDir {
		if p.vfs == nil {
			p.computeDirSize(e)
		}
		return
	}
	if p.vfs != nil {
		a.openVirtual(p, e, true)
		return
	}
	if isMediaFile(e.Name) {
		a.openFile(e.Path)
		return
	}
	a.showViewer(e.Path)
}

func isMediaFile(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "jpg", "jpeg", "png", "gif", "bmp", "webp", "tif", "tiff", "ico", "pdf", "mp4", "mp3", "heic", "avif",
		"mkv", "avi", "mov", "webm", "flac", "ogg", "wav", "m4a", "opus", "svg", "odt", "ods", "odp", "docx", "xlsx", "pptx":
		return true
	}
	return false
}

// openVirtual: archívumbeli / FTP-s fájl ideiglenes helyre, majd megnyitás vagy nézőke.
func (a *App) openVirtual(p *Panel, e *Entry, view bool) {
	dir, err := tempViewDir()
	if err != nil {
		a.warn(errText(err))
		return
	}
	title := T("Kicsomagolás")
	if isRemoteKind(p.kind()) {
		title = T("Letöltés")
	}
	a.runJob(title, func(j *job) error { return exportItems(p.vfs, []*Entry{e}, dir, j) }, func(err error) {
		local := filepath.Join(dir, e.Name)
		if _, serr := os.Stat(local); err != nil || serr != nil {
			return
		}
		if view && !isMediaFile(e.Name) {
			a.showViewer(local)
			return
		}
		a.openFile(local)
	})
}

// ── terminál ──

func (a *App) terminal() {
	dir := a.active.path
	if a.active.vfs != nil {
		dir, _ = os.UserHomeDir()
	}
	a.openTerminalAt(dir)
}

func termArgs(term, dir string, cmd string) []string {
	base := filepath.Base(term)
	if cmd == "" {
		switch base {
		case "konsole":
			return []string{"--workdir", dir}
		case "gnome-terminal", "kgx", "xfce4-terminal", "mate-terminal", "ptyxis":
			return []string{"--working-directory=" + dir}
		case "tilix":
			return []string{"-w", dir}
		case "alacritty":
			return []string{"--working-directory", dir}
		case "kitty":
			return []string{"--directory", dir}
		case "wezterm":
			return []string{"start", "--cwd", dir}
		case "foot":
			return []string{"-D", dir}
		}
		return nil
	}
	script := fmt.Sprintf(T("%s; printf '\\n[Kész – Enter a bezáráshoz] '; read _"), cmd)
	switch base {
	case "gnome-terminal", "ptyxis", "kgx":
		return append(termArgs(term, dir, ""), "--", "sh", "-c", script)
	case "wezterm":
		return []string{"start", "--cwd", dir, "--", "sh", "-c", script}
	case "kitty", "foot":
		return append(termArgs(term, dir, ""), "sh", "-c", script)
	case "xfce4-terminal", "mate-terminal", "tilix":
		return append(termArgs(term, dir, ""), "-x", "sh", "-c", script)
	}
	return append(termArgs(term, dir, ""), "-e", "sh", "-c", script)
}

func (a *App) openTerminalAt(dir string) {
	t := a.terminalPath()
	if t == "" {
		a.warn(T("Nem található terminálprogram. A Beállítások → Programok lapon megadhatod."))
		return
	}
	c := exec.Command(t, termArgs(t, dir, "")...)
	c.Dir = dir
	if err := startDetached(c); err != nil {
		a.warn(fmt.Sprintf(T("A terminál nem indítható:\n%s"), errText(err)))
	}
}

func (a *App) runInTerminal(cmdline, dir string) {
	t := a.terminalPath()
	if t == "" {
		a.warn(T("Nem található terminálprogram. A Beállítások → Programok lapon megadhatod."))
		return
	}
	c := exec.Command(t, termArgs(t, dir, cmdline)...)
	c.Dir = dir
	if err := startDetached(c); err != nil {
		a.warn(fmt.Sprintf(T("A terminál nem indítható:\n%s"), errText(err)))
	}
}

// ── egyéb ──

func fileOwner(st os.FileInfo) string {
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	name := strconv.Itoa(int(s.Uid))
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	grp := strconv.Itoa(int(s.Gid))
	if g, err := user.LookupGroupId(grp); err == nil {
		grp = g.Name
	}
	return name + " : " + grp
}

func errnoText(e syscall.Errno) string {
	switch e {
	case syscall.EACCES:
		return T("Hozzáférés megtagadva (nincs jogosultság)")
	case syscall.EPERM:
		return T("A művelet nem engedélyezett")
	case syscall.ENOENT:
		return T("A fájl vagy mappa nem létezik")
	case syscall.EEXIST:
		return T("Már létezik")
	case syscall.ENOSPC:
		return T("Nincs elég hely a lemezen")
	case syscall.EROFS:
		return T("Csak olvasható fájlrendszer")
	case syscall.ENOTEMPTY:
		return T("A mappa nem üres")
	case syscall.EBUSY:
		return T("Az erőforrás foglalt")
	case syscall.EISDIR:
		return T("Ez egy mappa")
	case syscall.ENOTDIR:
		return T("Nem mappa")
	case syscall.ENAMETOOLONG:
		return T("Túl hosszú név")
	case syscall.EXDEV:
		return T("Más fájlrendszerre nem nevezhető át")
	case syscall.EINVAL:
		return T("Érvénytelen név vagy paraméter")
	case syscall.EIO:
		return T("Bemeneti/kimeneti hiba")
	}
	return ""
}

func init() {
	sevenZipExe = func() string {
		if v := theAppSetting("sevenZip"); v != "" {
			return v
		}
		return findExe("7zz", "7z", "7za")
	}
	run7z = func(exe, dir string, args ...string) (string, error) {
		c := exec.Command(exe, args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			err = nil // figyelmeztetés: nem végzetes
		}
		if err != nil {
			return string(out), fmt.Errorf("%v\n%s", err, strings.TrimSpace(string(out)))
		}
		return string(out), nil
	}
}

func theAppSetting(k string) string {
	if theApp == nil || theApp.settings == nil {
		return ""
	}
	v, _ := theApp.settings.Get("lx." + k)
	return v
}
