//go:build linux

package main

// Gozie Commander – Linux-felület (Fyne). A fájlkezelés magja (archívumok,
// FTP, átnevezés) közös a Windows-változattal.

import (
	"fmt"
	fynelang "fyne.io/fyne/v2/lang"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type App struct {
	fa       fyne.App
	fgJobs   atomic.Int32 // futó (előtérbeli) műveletek száma a kilépési kérdéshez
	win      fyne.Window
	settings *iniSettings
	portable bool
	theme    *gzTheme
	look     look
	uiScale  float32

	left, right, active *Panel
	cmd                 *cmdEntry
	cmdLabel            *widget.Label
	toolbarHolder       *fyne.Container
	split               *container.Split
	showHidden          bool
	lastPattern         string
	lastDragPos         fyne.Position
	startArgs           bool
	idleNotice          bool
	keyMap              map[string]string // saját billentyűk: kombináció → parancs
	typeCol             *typeColors
	queue               *transferQueue
	queueBtn            *widget.Button
	queueWin            fyne.Window
	queueRefresh        func()
	qvOn                bool
	qvTimer             *time.Timer
	hiddenItem          *fyne.MenuItem
	toolbarItem         *fyne.MenuItem
}

var theApp *App

func main() {
	flatpakSetup() // még a napló előtt: az ideiglenes mappa helye
	dlogReset()
	dlog("%s %s indul (Linux)", appName, appVersion)
	if f, err := os.OpenFile(dlogPath(), os.O_APPEND|os.O_WRONLY, 0o666); err == nil {
		debug.SetCrashOutput(f, debug.CrashOptions{})
		f.Close()
	}
	defer cleanupTemp()
	fynelang.SetLanguage(curLang) // a Fyne saját feliratai (OK, Mégse, fájlablak) is a program nyelvén

	a := &App{lastPattern: "*.*", uiScale: 1}
	theApp = a
	a.settings, a.portable = openSettings()
	if err := a.settings.Load(); err != nil {
		dlog("beállítások: %v", err)
	}
	dlog("beállítások: %s", a.settings.FilePath())
	a.showHidden = a.settingBool("showHidden", false)
	if s := a.settingInt("lx.uiScale", 100); s >= 70 && s <= 200 {
		a.uiScale = float32(s) / 100
	}

	a.fa = app.NewWithID("hu.gozie.commander")
	a.theme = &gzTheme{a: a}
	a.fa.Settings().SetTheme(a.theme)
	a.fa.SetIcon(appIconRes)
	a.win = a.fa.NewWindow(appName + " " + appVersion)
	a.win.SetIcon(appIconRes)
	a.look = a.lookFor(a.isDark())

	home, _ := os.UserHomeDir()
	startL, startR := a.setting("left", home), a.setting("right", home)
	if len(os.Args) > 1 {
		startL = os.Args[1]
	}
	if len(os.Args) > 2 {
		startR = os.Args[2]
	}
	a.startArgs = len(os.Args) > 1
	a.left = newPanel(a, "left")
	a.right = newPanel(a, "right")
	startL = a.left.initTabs(startL)
	startR = a.right.initTabs(startR)
	a.active = a.left

	a.initQueue()
	a.cmdLabel = widget.NewLabel("")
	a.cmd = newCmdEntry(a)
	cmdRow := container.NewBorder(nil, nil, a.cmdLabel, a.queueBtn, a.cmd)

	fk := func(label string, f func()) fyne.CanvasObject {
		b := widget.NewButton(label, func() { f(); a.active.focus() })
		return b
	}
	fkeys := container.NewGridWithColumns(9,
		fk(T("F3 Nézőke"), a.viewFile),
		fk(T("F4 Szerkeszt"), a.editFile),
		fk(T("F5 Másol"), func() { a.transfer(false) }),
		fk(T("F6 Mozgat"), func() { a.transfer(true) }),
		fk(T("F7 Új mappa"), a.mkdir),
		fk(T("F8 Töröl"), func() { a.deleteSelected(false) }),
		fk(T("F9 Terminál"), a.terminal),
		fk(T("Beállítások"), a.settingsDialog),
		fk(T("F10 Kilépés"), a.quit),
	)

	a.split = container.NewHSplit(a.left.root, a.right.root)
	a.split.Offset = 0.5
	a.toolbarHolder = container.NewStack()
	a.win.SetContent(container.NewBorder(a.toolbarHolder, container.NewVBox(cmdRow, fkeys), nil, nil, a.split))
	a.loadTypeColors()
	a.keyMap = map[string]string{}
	for _, b := range a.userKeys() {
		a.keyMap[b.Key] = b.Cmd
	}
	a.win.SetMainMenu(a.menu())
	a.buildToolbar()

	if !a.left.navigate(startL, a.left.tabSel(), false) {
		a.left.navigate(home, "", false)
	}
	if !a.right.navigate(startR, a.right.tabSel(), false) {
		a.right.navigate(home, "", false)
	}
	first := a.left
	if a.setting("active", "left") == "right" {
		first = a.right
	}
	a.active = nil
	a.setActive(first)
	a.applyLook()

	w, h := float32(a.settingInt("lx.win.w", 1200)), float32(a.settingInt("lx.win.h", 760))
	a.win.Resize(fyne.NewSize(w, h))
	if pw, ok := a.win.(placeableWindow); ok {
		if x, y := a.settingInt("lx.win.x", -1<<20), a.settingInt("lx.win.y", -1<<20); x > -1<<20 && y > -1<<20 && onSomeScreen(x, y) {
			pw.SetPosition(x, y)
		}
		if a.settingInt("lx.win.max", 0) == 1 {
			pw.Maximize()
		}
	}
	a.win.SetOnDropped(a.dropFiles)
	a.win.SetCloseIntercept(a.quit)

	// változásfigyelés: más program írt a mappába; a rendszer témaváltása
	lastDark := a.isDark()
	lastDev := deviceStamp()
	go func() {
		for range time.Tick(1500 * time.Millisecond) {
			fyne.Do(func() {
				a.left.checkChanged()
				a.right.checkChanged()
				a.checkIdle()
				if st := deviceStamp(); st != lastDev { // pendrive be/ki, csatolás
					lastDev = st
					a.refreshDrives()
				}
				if d := a.isDark(); d != lastDark {
					lastDark = d
					a.applyTheme()
				}
			})
		}
	}()
	a.fa.Lifecycle().SetOnStarted(func() {
		first.focus()
		a.startUpdateCheck()
		a.initPasswordPrompt()
		go func() { // az ablak tényleges megjelenése után is (egyes ablakkezelőknél később kap fókuszt)
			time.Sleep(400 * time.Millisecond)
			fyne.Do(func() {
				if a.win.Canvas().Focused() == nil {
					a.active.focus()
				}
			})
		}()
	})
	a.win.ShowAndRun()
	if restartAfterExit {
		startSelf()
	}
}

// placeableWindow: a (módosított) Fyne ablakhely-kezelése X11 / XWayland alatt.
type placeableWindow interface {
	Position() (int, int)
	SetPosition(x, y int)
	Maximized() bool
	Maximize()
}

// onSomeScreen: a mentett hely ésszerű-e (pl. lecsatolt második monitor után ne
// kerüljön az ablak láthatatlan helyre). A pontos monitorlista itt nem érhető el,
// ezért csak a nyilvánvalóan rossz értékeket szűrjük.
func onSomeScreen(x, y int) bool { return x > -200 && y > -50 && x < 16000 && y < 9000 }

func (a *App) saveState() {
	s := a.settings
	pw, ok := a.win.(placeableWindow)
	if ok && pw.Maximized() {
		s.Put("lx.win.max", "1") // nagyítva: a korábbi (normál) méret és hely marad
	} else {
		sz := a.win.Canvas().Size()
		s.Put("lx.win.w", fmt.Sprint(int(sz.Width)))
		s.Put("lx.win.h", fmt.Sprint(int(sz.Height)))
		s.Put("lx.win.max", "0")
		if ok {
			x, y := pw.Position()
			s.Put("lx.win.x", fmt.Sprint(x))
			s.Put("lx.win.y", fmt.Sprint(y))
		}
	}
	a.left.saveTabs()
	a.right.saveTabs()
	s.Put("left", a.left.path)
	s.Put("right", a.right.path)
	s.Put("active", a.active.side)
	s.Put("showHidden", boolStr(a.showHidden))
	if err := s.Save(); err != nil {
		dlog("mentés: %v", err)
	}
}

func (a *App) quit() {
	if n := a.queue.Active() + int(a.fgJobs.Load()); n > 0 {
		a.confirm(T("Kilépés"), fmt.Sprintf(Tn(n, "%d művelet (másolás, mozgatás stb.) még fut vagy vár. Kilépve megszakadnak.\nKilépsz?"), n), T("Kilépés"), func() {
			a.saveState()
			a.fa.Quit()
		})
		return
	}
	a.saveState()
	a.fa.Quit()
}

func (a *App) other(p ...*Panel) *Panel {
	x := a.active
	if len(p) > 0 {
		x = p[0]
	}
	if x == a.left {
		return a.right
	}
	return a.left
}

func (a *App) setActive(p *Panel) {
	if a.active == p {
		return
	}
	old := a.active
	a.active = p
	if old != nil {
		old.setActive(false)
	}
	p.setActive(true)
	a.updateCmdLabel()
	if a.qvOn {
		a.placeQuickView()
	}
}

func (a *App) updateCmdLabel() {
	if a.cmdLabel == nil || a.active == nil {
		return
	}
	path := a.active.path
	if a.active.vfs != nil {
		path = a.active.vfs.Display(a.active.vpath)
	}
	if r := []rune(path); len(r) > 50 {
		path = fmt.Sprintf(T("…%s"), string(r[len(r)-49:]))
	}
	a.cmdLabel.SetText(path + ">")
}

func (a *App) textSize() float32 {
	return a.theme.Size(theme.SizeNameText)
}

// listTextSize: a lista betűmérete (a Windowson megadott 9 pont itt 14 képpont).
func (a *App) listTextSize() float32 {
	return float32(a.look.ListFont.Size) * 14 / 9 * a.uiScale
}

func (a *App) rightSelect() bool { return a.settingBool("rightSelect", false) }

func (a *App) applyLook() {
	a.look = a.lookFor(a.isDark())
	for _, p := range []*Panel{a.left, a.right} {
		p.applyLook()
	}
}

func (a *App) applyTheme() {
	a.fa.Settings().SetTheme(&gzTheme{a: a}) // új példány: a Fyne így mindent újrarajzol
	a.theme = a.fa.Settings().Theme().(*gzTheme)
	a.applyLook()
	a.buildToolbar()
}

func (a *App) refreshAll() {
	a.left.populateDrives()
	a.right.populateDrives()
	a.left.refresh()
	a.right.refresh()
}

// refreshDrives: a meghajtólisták újraépítése (eszköz be/ki, csatolás).
func (a *App) refreshDrives() {
	a.left.populateDrives()
	a.right.populateDrives()
}

func (a *App) warn(msg string) {
	a.choice(appName, msg, []string{"OK"}, 0, func(int) {})
}

func (a *App) toggleHidden() {
	a.showHidden = !a.showHidden
	if a.hiddenItem != nil {
		a.hiddenItem.Checked = a.showHidden
		a.win.MainMenu().Refresh()
	}
	a.left.refresh()
	a.right.refresh()
}

func (a *App) swapPanels() {
	l, r := a.left, a.right
	lp, rp := l.path, r.path
	if l.vfs != nil || r.vfs != nil {
		a.warn(T("Archívum vagy FTP nézetben a panelek nem cserélhetők."))
		return
	}
	l.navigate(rp, "", true)
	r.navigate(lp, "", true)
}

func (a *App) openInPanel(target *Panel) {
	src := a.active
	e := src.currentEntry()
	if e == nil || src.vfs != nil {
		return
	}
	dir := src.path
	if e.IsDir && !e.IsParent {
		dir = e.Path
	} else if e.IsParent {
		dir = filepath.Dir(src.path)
	}
	target.exitVFS()
	target.navigate(dir, "", true)
}

func (a *App) sameDirOther() {
	if a.active.vfs != nil {
		return
	}
	o := a.other()
	o.exitVFS()
	o.navigate(a.active.path, "", true)
}

func (a *App) gotoPath() {
	p := a.active
	a.inputBox(T("Ugrás mappára"), T("Mappa (vagy fájl) útvonala:"), p.path, func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		p.exitVFS()
		if !filepath.IsAbs(s) && !strings.HasPrefix(s, "~") {
			s = filepath.Join(p.path, s)
		}
		p.navigate(s, "", true)
		p.focus()
	})
}

func (a *App) selectPattern(add bool) {
	title := T("Kijelölés mintával")
	if !add {
		title = T("Jelölés megszüntetése mintával")
	}
	a.inputBox(title, T("Minta (pl. *.jpg; *.png):"), a.lastPattern, func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		a.lastPattern = s
		var pats []string
		for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ' ' || r == ',' }) {
			if x == "*.*" {
				x = "*"
			}
			pats = append(pats, x)
		}
		a.active.markPattern(pats, add)
		a.active.focus()
	})
}

func (a *App) compareDirs() {
	l, r := a.left, a.right
	if l.vfs != nil || r.vfs != nil {
		a.warn(T("Az összehasonlítás csak helyi mappák között működik."))
		return
	}
	idx := func(p *Panel) map[string]*Entry {
		m := map[string]*Entry{}
		for _, e := range p.items {
			if !e.IsParent && !e.IsDir {
				m[e.Name] = e
			}
		}
		return m
	}
	li, ri := idx(l), idx(r)
	diff := func(a, b map[string]*Entry) map[string]bool {
		out := map[string]bool{}
		for n, e := range a {
			o, ok := b[n]
			if !ok || o.Size != e.Size || e.ModTime.Truncate(2*time.Second).After(o.ModTime.Truncate(2*time.Second)) {
				out[n] = true
			}
		}
		return out
	}
	l.setMarks(diff(li, ri))
	r.setMarks(diff(ri, li))
}

func (a *App) copyPaths() {
	p := a.active
	if p.vfs != nil {
		return
	}
	var out []string
	for _, e := range p.selectedEntries() {
		out = append(out, e.Path)
	}
	if len(out) == 0 {
		out = []string{p.path}
	}
	a.win.Clipboard().SetContent(strings.Join(out, "\n"))
}

func (a *App) properties() {
	p := a.active
	e := p.currentEntry()
	if e == nil || p.vfs != nil {
		return
	}
	path := e.Path
	if e.IsParent {
		path = p.path
	}
	st, err := os.Lstat(path)
	if err != nil {
		a.warn(errText(err))
		return
	}
	txt := fmt.Sprintf(T("Név: %s\nHely: %s\nMéret: %s bájt\nMódosítva: %s\nJogok: %s"),
		filepath.Base(path), filepath.Dir(path), fmtSize(st.Size()), st.ModTime().Format("2006.01.02 15:04:05"), st.Mode().String())
	if st.Mode()&os.ModeSymlink != 0 {
		if t, err := os.Readlink(path); err == nil {
			txt += fmt.Sprintf(T("\nHivatkozás ide: %s"), t)
		}
	}
	if owner := fileOwner(st); owner != "" {
		txt += fmt.Sprintf(T("\nTulajdonos: %s"), owner)
	}
	dialog.ShowInformation(T("Tulajdonságok"), txt, a.win)
}

// dropFiles: a fájlkezelőből (Dolphin, Nautilus…) behúzott fájlok.
func (a *App) dropFiles(pos fyne.Position, uris []fyne.URI) {
	var files []string
	for _, u := range uris {
		if u.Scheme() == "file" {
			files = append(files, u.Path())
		}
	}
	if len(files) == 0 {
		return
	}
	p := a.left
	if a.right.contains(pos) {
		p = a.right
	} else if !a.left.contains(pos) {
		p = a.active
	}
	target := p.path
	if p.vfs == nil {
		r := p.indexAt(pos)
		if r >= 0 && r < len(p.items) && p.items[r].IsDir && !p.items[r].IsParent {
			target = p.items[r].Path
		}
	}
	what := describe([]*Entry{{Name: filepath.Base(files[0])}})
	if len(files) > 1 {
		what = Nf(len(files), "%d elem")
	}
	where := target
	if p.vfs != nil {
		where = p.vfs.Display(p.vpath)
	}
	d := dialog.NewCustom(T("Behúzott fájlok"), T("Mégse"),
		widget.NewLabel(fmt.Sprintf(T("%s ide:\n%s"), what, where)), a.win)
	copyB := widget.NewButton(T("Másolás ide"), func() { d.Hide(); a.copyIn(files, p, target, false) })
	moveB := widget.NewButton(T("Mozgatás ide"), func() { d.Hide(); a.copyIn(files, p, target, true) })
	copyB.Importance = widget.HighImportance
	d.SetButtons([]fyne.CanvasObject{copyB, moveB, widget.NewButton(T("Mégse"), d.Hide)})
	d.Show()
}

// ── parancssor ──

type cmdEntry struct {
	widget.Entry
	a *App
}

func newCmdEntry(a *App) *cmdEntry {
	e := &cmdEntry{a: a}
	e.ExtendBaseWidget(e)
	e.SetPlaceHolder(T("parancs (Enter: futtatás, Shift+Enter: terminálban)"))
	return e
}

func (e *cmdEntry) TypedKey(ev *fyne.KeyEvent) {
	switch ev.Name {
	case fyne.KeyReturn, fyne.KeyEnter:
		shift := e.a.active.mods()&fyne.KeyModifierShift != 0
		e.a.runCommandLine(shift)
		return
	case fyne.KeyEscape:
		e.SetText("")
		e.a.active.focus()
		return
	case fyne.KeyUp, fyne.KeyDown, fyne.KeyPageUp, fyne.KeyPageDown:
		e.a.active.focus()
		e.a.active.typedKey(ev)
		return
	case fyne.KeyF1, fyne.KeyF2, fyne.KeyF3, fyne.KeyF4, fyne.KeyF5, fyne.KeyF6, fyne.KeyF7, fyne.KeyF8, fyne.KeyF9, fyne.KeyF10:
		e.a.active.typedKey(ev)
		return
	}
	e.Entry.TypedKey(ev)
}

func (e *cmdEntry) AcceptsTab() bool { return true }

func (e *cmdEntry) TypedRune(r rune) {
	if r == '\t' {
		e.a.active.focus()
		return
	}
	e.Entry.TypedRune(r)
}

func (a *App) focusCmd() { a.win.Canvas().Focus(a.cmd) }

// runCommandLine: cd, illetve program futtatása a panel mappájában.
func (a *App) runCommandLine(inTerminal bool) {
	line := strings.TrimSpace(a.cmd.Text)
	if line == "" {
		return
	}
	p := a.active
	dir := p.path
	if line == "cd" || strings.HasPrefix(line, "cd ") {
		t := strings.TrimSpace(strings.TrimPrefix(line, "cd"))
		t = strings.Trim(t, `"'`)
		if t == "" || t == "~" {
			t, _ = os.UserHomeDir()
		}
		if t == "-" && len(p.back) > 0 {
			t = p.back[len(p.back)-1]
		}
		if !filepath.IsAbs(t) && !strings.HasPrefix(t, "~") {
			t = filepath.Join(dir, t)
		}
		p.exitVFS()
		if p.navigate(t, "", true) {
			a.cmd.SetText("")
		}
		p.focus()
		return
	}
	if p.vfs != nil {
		home, _ := os.UserHomeDir()
		dir = home
	}
	a.cmd.SetText("")
	p.focus()
	if inTerminal {
		a.runInTerminal(line, dir)
		return
	}
	c := exec.Command("sh", "-c", line)
	c.Dir = dir
	if err := startDetached(hostify(c)); err != nil {
		a.warn(fmt.Sprintf(T("A parancs nem indítható:\n%s"), errText(err)))
	}
}

// ── menü ──

func (a *App) menu() *fyne.MainMenu {
	sc := func(k fyne.KeyName, m fyne.KeyModifier) fyne.Shortcut {
		return &desktop.CustomShortcut{KeyName: k, Modifier: m}
	}
	item := func(label string, f func(), s fyne.Shortcut) *fyne.MenuItem {
		it := fyne.NewMenuItem(label, func() { f(); a.active.focus() })
		if s != nil { // saját billentyű felülírja: a menü ne kapja el
			if k, ok := shortcutCombo(s); ok && a.keyMap[k.String()] != "" {
				s = nil
			}
		}
		it.Shortcut = s
		return it
	}
	ctrl, shift := fyne.KeyModifierControl, fyne.KeyModifierShift

	a.hiddenItem = item(T("Rejtett fájlok mutatása"), a.toggleHidden, sc(fyne.KeyH, ctrl))
	a.hiddenItem.Checked = a.showHidden
	a.toolbarItem = item(T("Eszköztár"), func() {
		a.settings.Put("toolbar.show", boolStr(!a.toolbarVisible()))
		a.buildToolbar()
	}, nil)
	a.toolbarItem.Checked = a.toolbarVisible()

	file := fyne.NewMenu(T("Fájl"),
		item(T("Megnyitás"), func() { a.active.openCurrent() }, nil),
		item(T("Nézőke   F3"), a.viewFile, nil),
		item(T("Szerkesztés   F4"), a.editFile, nil),
		item(T("Új fájl   Shift+F4"), a.newFile, nil),
		fyne.NewMenuItemSeparator(),
		item(T("Másolás   F5"), func() { a.transfer(false) }, nil),
		item(T("Mozgatás   F6"), func() { a.transfer(true) }, nil),
		item(T("Átnevezés   Shift+F6"), a.rename, nil),
		item(T("Többfájl-átnevezés"), a.multiRename, sc(fyne.KeyM, ctrl)),
		item(T("Új mappa   F7"), a.mkdir, nil),
		item(T("Törlés a Kukába   F8"), func() { a.deleteSelected(false) }, nil),
		item(T("Végleges törlés   Shift+F8"), func() { a.deleteSelected(true) }, nil),
		fyne.NewMenuItemSeparator(),
		item(T("Tulajdonságok"), a.properties, nil),
		item(T("Útvonal másolása"), a.copyPaths, sc(fyne.KeyC, ctrl|shift)),
		fyne.NewMenuItemSeparator(),
		item(T("Kilépés   F10"), a.quit, nil),
	)
	file.Items[len(file.Items)-1].IsQuit = true
	sel := fyne.NewMenu(T("Kijelölés"),
		item(T("Kijelölés mintával…   Num +"), func() { a.selectPattern(true) }, nil),
		item(T("Jelölés megszüntetése mintával…   Num −"), func() { a.selectPattern(false) }, nil),
		item(T("Mindet kijelöl   Ctrl+A"), func() { a.active.markAll() }, nil),
		item(T("Jelölés megszüntetése"), func() { a.active.setMarks(map[string]bool{}) }, nil),
		item(T("Jelölés megfordítása   Num *"), func() { a.active.invertMarks() }, nil),
		item(T("Mappák összehasonlítása   Shift+F2"), a.compareDirs, nil),
	)
	arc := fyne.NewMenu(T("Archívum"),
		item(T("Becsomagolás…   Alt+F5"), a.pack, sc(fyne.KeyP, ctrl)),
		item(T("Kicsomagolás…   Alt+F9"), a.unpack, sc(fyne.KeyP, ctrl|shift)),
	)
	ftpM := fyne.NewMenu(T("Távoli"),
		item(T("Kapcsolatok…"), a.ftpConnect, sc(fyne.KeyF, ctrl)),
		item(T("Gyors kapcsolódás…"), a.quickConnect, nil),
		item(T("Kapcsolat bontása"), a.ftpDisconnect, sc(fyne.KeyF, ctrl|shift)),
	)
	cmds := fyne.NewMenu(T("Parancsok"),
		item(T("Fájlkeresés…   Alt+F7"), a.findFiles, nil),
		item(T("Mappák szinkronizálása…"), a.syncDirs, nil),
		item(T("Háttérműveletek…"), a.showQueue, sc(fyne.KeyJ, ctrl)),
		item(T("Terminál   F9"), a.terminal, nil),
		item(T("Ugrás mappára…"), a.gotoPath, sc(fyne.KeyL, ctrl)),
		item(T("Vissza   Alt+←"), func() { a.active.goBack() }, nil),
		item(T("Előre   Alt+→"), func() { a.active.goForward() }, nil),
		item(T("Ugyanaz a mappa a másik panelen"), a.sameDirOther, sc(fyne.KeyE, ctrl)),
		item(T("Panelek cseréje"), a.swapPanels, sc(fyne.KeyU, ctrl)),
		item(T("Parancssor"), a.focusCmd, sc(fyne.KeyK, ctrl)),
		fyne.NewMenuItemSeparator(),
		item(T("Kedvenc mappák…"), a.hotlistMenu, sc(fyne.KeyD, ctrl)),
		item(T("Előzmények…   Alt+↓"), a.historyPopup, nil),
		item(T("Gyorskeresés a listában"), func() { a.active.startQuickSearch("") }, sc(fyne.KeyS, ctrl)),
		item(T("Mappa megnyitása a rendszer fájlkezelőjében"), func() { a.openFile(a.active.path) }, nil),
	)
	view := fyne.NewMenu(T("Nézet"),
		a.hiddenItem,
		fyne.NewMenuItemSeparator(),
		item(T("Részletes nézet   Ctrl+1"), func() { a.active.setViewMode(viewDetail) }, nil),
		item(T("Rövid nézet   Ctrl+2"), func() { a.active.setViewMode(viewBrief) }, nil),
		item(T("Bélyegkép nézet   Ctrl+3"), func() { a.active.setViewMode(viewThumbs) }, nil),
		item(T("Gyorsnézet a másik panelen"), a.toggleQuickView, sc(fyne.KeyQ, ctrl)),
		fyne.NewMenuItemSeparator(),
		item(T("Rendezés név szerint   Ctrl+4"), func() { a.active.sortBy(colName) }, nil),
		item(T("Rendezés kiterjesztés szerint   Ctrl+5"), func() { a.active.sortBy(colExt) }, nil),
		item(T("Rendezés dátum szerint   Ctrl+6"), func() { a.active.sortBy(colDate) }, nil),
		item(T("Rendezés méret szerint   Ctrl+7"), func() { a.active.sortBy(colSize) }, nil),
		fyne.NewMenuItemSeparator(),
		item(T("Frissítés"), a.refreshAll, sc(fyne.KeyR, ctrl)),
		fyne.NewMenuItemSeparator(),
		a.toolbarItem,
		item(T("Eszköztár szerkesztése…"), a.editToolbar, nil),
		item(T("Fájltípusok színezése…"), a.editTypeColors, nil),
		item(T("Billentyűparancsok…"), a.editKeys, nil),
		item(T("Beállítások…"), a.settingsDialog, nil),
	)
	help := fyne.NewMenu(T("Súgó"),
		item(T("Billentyűparancsok   F1"), a.help, nil),
		item(T("Diagnosztikai napló"), func() { a.showViewer(dlogPath()) }, nil),
		item(T("Frissítés keresése…"), func() { a.checkUpdate(true) }, nil),
		item(T("Névjegy"), func() {
			dialog.ShowInformation(T("Névjegy"), aboutText(), a.win)
		}, nil),
	)
	tools := fyne.NewMenu(T("Eszközök"),
		item(T("Fájlok összehasonlítása tartalom szerint…"), a.compareContent, nil),
		fyne.NewMenuItemSeparator(),
		item(T("Ellenőrzőösszeg készítése (MD5, SHA)…"), a.makeChecksums, nil),
		item(T("Ellenőrzőösszeg ellenőrzése"), a.verifyChecksums, nil),
		fyne.NewMenuItemSeparator(),
		item(T("Ismétlődő fájlok keresése…"), a.findDuplicates, nil),
		fyne.NewMenuItemSeparator(),
		item(T("Fájl darabolása…"), a.splitCurrent, nil),
		item(T("Fájlok összefűzése…"), a.joinCurrent, nil),
		fyne.NewMenuItemSeparator(),
		item(T("Jogok és dátum módosítása…"), a.changeAttrs, nil),
	)
	tabsM := fyne.NewMenu(T("Fülek"),
		item(T("Új fül"), func() { a.active.newTab() }, sc(fyne.KeyT, ctrl)),
		item(T("Fül bezárása"), func() { a.active.closeTab() }, sc(fyne.KeyW, ctrl)),
		item(T("Következő fül"), func() { a.active.cycleTab(1) }, sc(fyne.KeyTab, ctrl)),
		item(T("Előző fül"), func() { a.active.cycleTab(-1) }, sc(fyne.KeyTab, ctrl|shift)),
	)
	return fyne.NewMainMenu(file, sel, arc, ftpM, cmds, tools, tabsM, view, fyne.NewMenu(T("Saját"), a.userMenuItems()...), help)
}

func itoa(n int) string { return fmt.Sprint(n) }

func homeDir() string {
	h, _ := os.UserHomeDir()
	if h == "" {
		h = "/"
	}
	return h
}

// widgetDialog: egyszerű OK/Mégse ablak egy beviteli elemmel.
func widgetDialog(parent fyne.Window, title string, content fyne.CanvasObject, ok func()) dialog.Dialog {
	d := dialog.NewCustomConfirm(title, "OK", T("Mégse"), content, func(b bool) {
		if b {
			ok()
		}
	}, parent)
	d.Resize(fyne.NewSize(420, 0))
	return d
}
