//go:build windows

package main

// Lapfülek, gyorskeresés, kedvenc mappák és előzmények (Windows-felület).

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// ── lapfülek ──

func (p *Panel) initTabs(start string) string {
	v, _ := p.app.settings.Get(p.side + ".tabs")
	p.tabs = decodeTabs(v)
	idx, _ := p.app.settings.Get(p.side + ".tab")
	p.tabIdx, _ = strconv.Atoi(idx)
	if len(p.tabs) == 0 {
		p.tabs = []tabState{{Path: start}}
		p.tabIdx = 0
	}
	if p.tabIdx < 0 || p.tabIdx >= len(p.tabs) {
		p.tabIdx = 0
	}
	if p.app.startArgs {
		p.tabs[p.tabIdx].Path = start
	}
	return p.tabs[p.tabIdx].Path
}

func (p *Panel) tabSel() string {
	if p.app.startArgs || p.tabIdx < 0 || p.tabIdx >= len(p.tabs) {
		return ""
	}
	return p.tabs[p.tabIdx].Sel
}

func (p *Panel) syncTab() {
	if p.tabIdx < 0 || p.tabIdx >= len(p.tabs) {
		return
	}
	t := &p.tabs[p.tabIdx]
	t.Path = p.path
	if e := p.currentEntry(); e != nil && !e.IsParent && p.vfs == nil {
		t.Sel = e.Name
	}
}

func (p *Panel) saveTabs() {
	p.syncTab()
	p.app.settings.Put(p.side+".tabs", encodeTabs(p.tabs))
	p.app.settings.Put(p.side+".tab", strconv.Itoa(p.tabIdx))
}

func (p *Panel) newTab() {
	p.syncTab()
	t := tabState{Path: p.path}
	if e := p.currentEntry(); e != nil && e.IsDir && !e.IsParent && p.vfs == nil {
		t = tabState{Path: e.Path} // a kurzor alatti mappa új fülön
	}
	at := p.tabIdx + 1
	p.tabs = append(p.tabs[:at], append([]tabState{t}, p.tabs[at:]...)...)
	p.switchTab(at)
}

func (p *Panel) closeTab() {
	if len(p.tabs) <= 1 {
		return
	}
	i := p.tabIdx
	p.tabs = append(p.tabs[:i], p.tabs[i+1:]...)
	if i >= len(p.tabs) {
		i = len(p.tabs) - 1
	}
	p.tabIdx = -1
	p.switchTab(i)
}

func (p *Panel) switchTab(i int) {
	if i < 0 || i >= len(p.tabs) {
		return
	}
	if p.tabIdx >= 0 && p.tabIdx != i {
		p.syncTab()
	}
	p.endQuickSearch()
	if p.vfs != nil {
		v := p.vfs
		p.vfs, p.vpath = nil, ""
		v.Close()
	}
	p.tabIdx = i
	t := p.tabs[i]
	if !p.navigate(t.Path, t.Sel, true, true) {
		home, _ := os.UserHomeDir()
		p.navigate(home, "", false, false)
	}
	p.renderTabs()
	p.tv.SetFocus()
}

func (p *Panel) cycleTab(d int) {
	if n := len(p.tabs); n > 1 {
		p.switchTab(((p.tabIdx+d)%n + n) % n)
	}
}

// renderTabs: a fülsor csak egynél több fülnél látszik .
// Villogás ellen: ha a fülek száma nem változott, csak a feliratok frissülnek;
// újraépítéskor az ablak rajzolása szünetel.
func (p *Panel) renderTabs() {
	if p.tabBar == nil {
		return
	}
	if p.tabIdx >= 0 && p.tabIdx < len(p.tabs) {
		p.tabs[p.tabIdx].Path = p.path
	}
	labels := make([]string, len(p.tabs))
	for i, t := range p.tabs {
		l := tabLabel(t.Path)
		if i == p.tabIdx && p.vfs != nil {
			l = tabLabel(p.vfs.Display(p.vpath))
		}
		if i == p.tabIdx {
			l = "● " + l
		}
		labels[i] = l
	}
	if len(p.tabs) <= 1 {
		if p.tabBar.Visible() || len(p.tabBtns) > 0 {
			p.app.mw.SetSuspended(true)
			p.clearTabButtons()
			p.tabBar.SetVisible(false)
			p.app.mw.SetSuspended(false)
		}
		return
	}
	if len(p.tabBtns) == len(p.tabs) && p.tabBar.Visible() {
		for i, b := range p.tabBtns {
			if b.Text() != labels[i] {
				b.SetText(labels[i])
			}
			b.SetToolTipText(p.tabs[i].Path)
			want := p.app.mw.Font()
			if i == p.tabIdx && p.app.boldFont != nil {
				want = p.app.boldFont
			}
			if b.Font() != want {
				b.SetFont(want)
			}
		}
		return
	}
	p.app.mw.SetSuspended(true)
	defer func() {
		p.app.mw.SetSuspended(false)
		p.tabBar.Invalidate()
	}()
	p.clearTabButtons()
	for i, t := range p.tabs {
		i := i
		b, err := walk.NewPushButton(p.tabBar)
		if err != nil {
			continue
		}
		b.SetText(labels[i])
		b.SetToolTipText(t.Path)
		if i == p.tabIdx && p.app.boldFont != nil {
			b.SetFont(p.app.boldFont)
		}
		removeTabStop(b.Handle())
		b.Clicked().Attach(func() {
			p.app.mw.Synchronize(func() { p.activate(); p.switchTab(i) })
		})
		b.MouseUp().Attach(func(x, y int, mb walk.MouseButton) {
			if mb == walk.MiddleButton { // középső gomb: a fül bezárása
				p.app.mw.Synchronize(func() {
					p.activate()
					if i != p.tabIdx {
						p.switchTab(i)
					}
					p.closeTab()
				})
			}
		})
		themeTree(b, darkActive)
		p.tabBtns = append(p.tabBtns, b)
	}
	if b, err := walk.NewPushButton(p.tabBar); err == nil {
		b.SetText("×")
		b.SetToolTipText(T("Fül bezárása (Ctrl+W)"))
		removeTabStop(b.Handle())
		b.Clicked().Attach(func() { p.app.mw.Synchronize(func() { p.activate(); p.closeTab() }) })
		themeTree(b, darkActive)
	}
	walk.NewHSpacer(p.tabBar)
	p.tabBar.SetVisible(true)
}

func (p *Panel) clearTabButtons() {
	ch := p.tabBar.Children()
	var old []walk.Widget
	for i := 0; i < ch.Len(); i++ {
		old = append(old, ch.At(i))
	}
	ch.Clear()
	for _, w := range old {
		w.Dispose()
	}
	p.tabBtns = nil
}

// ── gyorskeresés ──

func (p *Panel) quickNames() []string {
	names := make([]string, len(p.model.items))
	for i, e := range p.model.items {
		if !e.IsParent {
			names[i] = e.Name
		}
	}
	return names
}

func (p *Panel) startQuickSearch(first string) {
	p.qsOn, p.qs = true, ""
	if first != "" {
		p.quickAdd(first)
	}
	p.showQuickSearch()
}

func (p *Panel) quickAdd(s string) bool {
	cand := p.qs + s
	if i := quickFind(p.quickNames(), cand, p.cursorRow(), 1); i >= 0 {
		p.qs = cand
		p.setCursor(i)
		p.showQuickSearch()
		return true
	}
	return false
}

func (p *Panel) showQuickSearch() {
	quickText(p.status, fmt.Sprintf(T("Gyorskeresés: %s▏   (↑/↓ következő, Esc vége)"), p.qs))
}

func (p *Panel) endQuickSearch() {
	if !p.qsOn {
		return
	}
	p.qsOn, p.qs = false, ""
	p.updateStatus()
}

// quickKey: igaz, ha a gyorskeresés elnyelte a billentyűt.
func (p *Panel) quickKey(key walk.Key) bool {
	if !p.qsOn {
		return false
	}
	switch key {
	case walk.KeyBack:
		if r := []rune(p.qs); len(r) > 0 {
			p.qs = string(r[:len(r)-1])
			p.showQuickSearch()
		} else {
			p.endQuickSearch()
		}
		return true
	case walk.KeyEscape:
		p.endQuickSearch()
		return true
	case walk.KeyDown, walk.KeyUp:
		d := 1
		if key == walk.KeyUp {
			d = -1
		}
		cur := p.cursorRow()
		if i := quickFind(p.quickNames(), p.qs, cur+d, d); i >= 0 {
			p.app.mw.Synchronize(func() { p.setCursor(i) }) // a lista saját lépése után
		} else {
			p.app.mw.Synchronize(func() { p.setCursor(cur) })
		}
		return true
	case walk.KeyReturn:
		p.endQuickSearch() // a megnyitást a lista maga intézi
		return true
	case walk.KeyLeft, walk.KeyRight, walk.KeyPrior, walk.KeyNext, walk.KeyHome, walk.KeyEnd,
		walk.KeyTab, walk.KeyInsert, walk.KeyDelete, walk.KeyF1, walk.KeyF2, walk.KeyF3, walk.KeyF4,
		walk.KeyF5, walk.KeyF6, walk.KeyF7, walk.KeyF8, walk.KeyF9, walk.KeyF10, walk.KeyF11, walk.KeyF12:
		p.endQuickSearch()
		return false
	case walk.KeyShift, walk.KeyControl, walk.KeyMenu, walk.KeyLShift, walk.KeyRShift, walk.KeyLControl, walk.KeyRControl:
		return true
	}
	return false // karakter: a szokásos ág kezeli (gyorskereséshez adja)
}

// quickTyping: a gépelt karakter most a gyorskeresésé-e.
func (p *Panel) quickTyping() bool {
	return p.qsOn || (p.app.typingSearches() && p.app.cmd.Text() == "")
}

// quickChar: a lista WM_CHAR üzenete (a gépelt karakter, a billentyűzetkiosztás szerint).
// Igaz, ha felhasználtuk; a lista saját „gépelésre ugrás”-át így elnyomjuk.
func (p *Panel) quickChar(r rune) bool {
	if r < 32 || r == 127 {
		return false
	}
	if p.quickTyping() {
		if r == ' ' && !p.qsOn {
			return false // szóköz: jelölés (az onKey intézi)
		}
		if p.qsOn {
			p.quickAdd(string(r))
		} else {
			p.startQuickSearch(string(r))
		}
		return true
	}
	return true // a karakter a parancssorba megy (onKey), a lista ne ugráljon tőle
}

func (a *App) typingSearches() bool {
	v, _ := a.settings.Get("quickSearchTyping")
	return v == "1"
}

// ── kedvenc mappák (Ctrl+D) ──

func (a *App) hotlist() []hotEntry {
	v, _ := a.settings.Get("hotlist")
	return decodeHotlist(v)
}

func (a *App) saveHotlist(h []hotEntry) {
	a.settings.Put("hotlist", encodeHotlist(h))
	a.settings.Save()
}

// cursorScreenPos: a kurzor alatti sor bal alsó sarka képernyő-koordinátában.
func (p *Panel) cursorScreenPos() (int32, int32) {
	var rc win.RECT
	rc.Left = 0
	if p.lv != 0 && win.SendMessage(p.lv, 0x100E, uintptr(p.cursorRow()), uintptr(unsafe.Pointer(&rc))) != 0 {
		pt := win.POINT{X: rc.Left + 40, Y: rc.Bottom}
		win.ClientToScreen(p.lv, &pt)
		return pt.X, pt.Y
	}
	var pt win.POINT
	win.GetCursorPos(&pt)
	return pt.X, pt.Y
}

func (a *App) popupAtCursor(items []string) int {
	x, y := a.active.cursorScreenPos()
	return popupChoiceAt(a.mw.Handle(), items, x, y)
}

func (a *App) hotlistMenu() {
	p := a.active
	h := a.hotlist()
	var items []string
	for _, e := range h {
		items = append(items, e.Name+"\t"+e.Path)
	}
	if len(items) > 0 {
		items = append(items, "-")
	}
	items = append(items, T("Aktuális mappa hozzáadása…"), T("Kedvencek szerkesztése…"))
	r := a.popupAtCursor(items) - 1
	switch {
	case r < 0:
	case r < len(h):
		p.exitVFSTo(h[r].Path)
	case r == len(items)-2:
		a.addHotlist()
	case r == len(items)-1:
		a.editHotlist()
	}
	p.tv.SetFocus()
}

// exitVFSTo: archívumból / FTP-ről kilépve a megadott mappára ugrik.
func (p *Panel) exitVFSTo(path string) {
	if p.vfs != nil {
		v := p.vfs
		p.vfs, p.vpath = nil, ""
		v.Close()
	}
	p.navigate(path, "", true, false)
}

func (a *App) addHotlist() {
	p := a.active
	if p.vfs != nil {
		walk.MsgBox(a.mw, appName, T("Archívum vagy FTP nem tehető a kedvencek közé."), walk.MsgBoxIconInformation)
		return
	}
	path := p.path
	name, ok := inputBox(a.mw, T("Kedvenc mappa"), fmt.Sprintf(T("Név a menüben (%s):"), path), tabLabel(path), false)
	if !ok {
		return
	}
	if name == "" {
		name = tabLabel(path)
	}
	h := a.hotlist()
	for i := range h {
		if h[i].Path == path {
			h[i].Name = name
			a.saveHotlist(h)
			return
		}
	}
	a.saveHotlist(append(h, hotEntry{Name: name, Path: path}))
}

func (a *App) editHotlist() {
	h := a.hotlist()
	var dlg *walk.Dialog
	var lb *walk.ListBox
	labels := func() []string {
		var out []string
		for _, e := range h {
			out = append(out, fmt.Sprintf(T("%s   —   %s"), e.Name, e.Path))
		}
		return out
	}
	refresh := func(sel int) {
		lb.SetModel(labels())
		if sel >= 0 && sel < len(h) {
			lb.SetCurrentIndex(sel)
		}
		a.saveHotlist(h)
	}
	move := func(d int) {
		i := lb.CurrentIndex()
		if i < 0 || i+d < 0 || i+d >= len(h) {
			return
		}
		h[i], h[i+d] = h[i+d], h[i]
		refresh(i + d)
	}
	err := Dialog{
		AssignTo: &dlg, Title: T("Kedvenc mappák"), MinSize: Size{Width: 620, Height: 380}, Layout: HBox{},
		Children: []Widget{
			ListBox{AssignTo: &lb, ItemStyler: newListStyler(&lb), Model: labels()},
			Composite{Layout: VBox{MarginsZero: true}, Children: []Widget{
				PushButton{Text: T("Fel"), OnClicked: func() { move(-1) }},
				PushButton{Text: T("Le"), OnClicked: func() { move(1) }},
				PushButton{Text: T("Átnevezés…"), OnClicked: func() {
					i := lb.CurrentIndex()
					if i < 0 || i >= len(h) {
						return
					}
					if n, ok := inputBox(dlg, T("Átnevezés"), T("Név a menüben:"), h[i].Name, false); ok && n != "" {
						h[i].Name = n
						refresh(i)
					}
				}},
				PushButton{Text: T("Eltávolítás"), OnClicked: func() {
					i := lb.CurrentIndex()
					if i < 0 || i >= len(h) {
						return
					}
					h = append(h[:i], h[i+1:]...)
					refresh(i)
				}},
				VSpacer{},
				PushButton{Text: T("Bezárás"), OnClicked: func() { dlg.Accept() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		return
	}
	themeDialog(dlg)
	dlg.Run()
}

// ── előzmények (Alt+↓) ──

func (a *App) historyPopup() {
	p := a.active
	hist := historyMenu(p.back, p.path, 25)
	if len(hist) == 0 {
		return
	}
	r := a.popupAtCursor(hist) - 1
	if r >= 0 && r < len(hist) {
		p.exitVFSTo(hist[r])
	}
	p.tv.SetFocus()
}

// popupChoiceAt: felugró menü megadott képernyőpontnál; a választott elem sorszáma (1-től), 0 ha semmi.
func popupChoiceAt(owner win.HWND, items []string, x, y int32) int {
	h := win.CreatePopupMenu()
	if h == 0 {
		return 0
	}
	defer win.DestroyMenu(h)
	for i, t := range items {
		if t == "-" {
			procAppendMenuW.Call(uintptr(h), 0x800, 0, 0)
			continue
		}
		s, _ := syscall.UTF16PtrFromString(t)
		procAppendMenuW.Call(uintptr(h), 0, uintptr(i+1), uintptr(unsafe.Pointer(s)))
	}
	win.SetForegroundWindow(owner)
	r, _, _ := procTrackPopupMenuEx.Call(uintptr(h), uintptr(win.TPM_RETURNCMD|win.TPM_RIGHTBUTTON),
		uintptr(x), uintptr(y), uintptr(owner), 0)
	return int(r)
}
