//go:build linux

package main

// Lapfülek, gyorskeresés, kedvenc mappák és előzmények (Linux-felület).

import (
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ── lapfülek ──

// initTabs a mentett füleket tölti be; start üres: a mentett aktív fül mappája.
func (p *Panel) initTabs(start string) string {
	p.tabs = decodeTabs(p.app.setting(p.side+".tabs", ""))
	p.tabIdx = p.app.settingInt(p.side+".tab", 0)
	if len(p.tabs) == 0 {
		p.tabs = []tabState{{Path: start}}
		p.tabIdx = 0
	}
	if p.tabIdx < 0 || p.tabIdx >= len(p.tabs) {
		p.tabIdx = 0
	}
	if start != "" && p.app.startArgs {
		p.tabs[p.tabIdx].Path = start
	}
	return p.tabs[p.tabIdx].Path
}

func (p *Panel) syncTab() {
	if p.tabIdx < 0 || p.tabIdx >= len(p.tabs) {
		return
	}
	t := &p.tabs[p.tabIdx]
	if p.vfs == nil {
		t.Path = p.path
	} else {
		t.Path = p.path // archívum / FTP: a fül a helyi mappát jegyzi meg
	}
	if e := p.currentEntry(); e != nil && !e.IsParent && p.vfs == nil {
		t.Sel = e.Name
	}
}

func (p *Panel) saveTabs() {
	p.syncTab()
	p.app.settings.Put(p.side+".tabs", encodeTabs(p.tabs))
	p.app.settings.Put(p.side+".tab", itoa(p.tabIdx))
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
	p.tabIdx = -1 // a bezárt fület már nem kell menteni
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
	p.exitVFSQuiet()
	p.tabIdx = i
	t := p.tabs[i]
	if !p.navigate(t.Path, t.Sel, true) {
		home := homeDir()
		p.navigate(home, "", false)
	}
	p.renderTabs()
	p.focus()
}

func (p *Panel) cycleTab(d int) {
	if len(p.tabs) > 1 {
		p.switchTab(((p.tabIdx+d)%len(p.tabs) + len(p.tabs)) % len(p.tabs))
	}
}

// renderTabs: a fülsor csak akkor látszik, ha egynél több fül van .
func (p *Panel) renderTabs() {
	if p.tabBar == nil {
		return
	}
	if p.tabIdx >= 0 && p.tabIdx < len(p.tabs) {
		p.tabs[p.tabIdx].Path = p.path
	}
	if len(p.tabs) <= 1 {
		if p.tabBar.Visible() {
			p.tabBar.Objects = nil
			p.tabBar.Hide()
			p.relayout()
		}
		return
	}
	var objs []fyne.CanvasObject
	for i, t := range p.tabs {
		i := i
		label := tabLabel(t.Path)
		if i == p.tabIdx && p.vfs != nil {
			label = tabLabel(p.vfs.Display(p.vpath))
		}
		b := widget.NewButton(label, func() { p.activate(); p.switchTab(i) })
		if i == p.tabIdx {
			b.Importance = widget.HighImportance
		} else {
			b.Importance = widget.LowImportance
		}
		objs = append(objs, b)
	}
	closeB := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { p.activate(); p.closeTab() })
	closeB.Importance = widget.LowImportance
	objs = append(objs, closeB)
	p.tabBar.Objects = []fyne.CanvasObject{container.NewHScroll(container.NewHBox(objs...))}
	p.tabBar.Show()
	p.relayout()
}

// relayout: a fülsor megjelenése / eltűnése után a panel elrendezése újra.
func (p *Panel) relayout() {
	if c, ok := p.root.(*fyne.Container); ok {
		c.Refresh()
	}
}

// exitVFSQuiet: archívum / FTP elhagyása navigáció nélkül (fülváltáskor).
func (p *Panel) exitVFSQuiet() {
	if p.vfs == nil {
		return
	}
	v := p.vfs
	p.vfs, p.vpath = nil, ""
	v.Close()
}

// ── gyorskeresés ──

func (p *Panel) quickNames() []string {
	names := make([]string, len(p.items))
	for i, e := range p.items {
		if !e.IsParent {
			names[i] = e.Name
		}
	}
	return names
}

func (p *Panel) startQuickSearch(first string) {
	dlog("gyorskeresés indul (%s)", p.side)
	p.qsOn, p.qs = true, ""
	if first != "" {
		p.quickAdd(first)
	}
	p.showQuickSearch()
}

func (p *Panel) quickAdd(s string) bool {
	cand := p.qs + s
	if i := quickFind(p.quickNames(), cand, p.cursor, 1); i >= 0 {
		p.qs = cand
		p.setCursor(i)
		p.showQuickSearch()
		return true
	}
	return false // nincs ilyen név: a betűt nem fogadjuk el
}

func (p *Panel) showQuickSearch() {
	p.status.SetText(fmt.Sprintf(T("Gyorskeresés: %s▏   (↑/↓ következő, Esc vége)"), p.qs))
}

func (p *Panel) endQuickSearch() {
	if p.qsOn {
		dlog("gyorskeresés vége (%s)", p.side)
	}
	if !p.qsOn {
		return
	}
	p.qsOn, p.qs = false, ""
	p.updateStatus()
}

// quickKey: igaz, ha a gyorskeresés elnyelte a billentyűt.
func (p *Panel) quickKey(ev *fyne.KeyEvent) bool {
	if !p.qsOn {
		return false
	}
	switch ev.Name {
	case fyne.KeyBackspace:
		if r := []rune(p.qs); len(r) > 0 {
			p.qs = string(r[:len(r)-1])
			p.showQuickSearch()
		} else {
			p.endQuickSearch()
		}
		return true
	case fyne.KeyEscape:
		p.endQuickSearch()
		return true
	case fyne.KeyDown, fyne.KeyUp:
		d := 1
		if ev.Name == fyne.KeyUp {
			d = -1
		}
		if i := quickFind(p.quickNames(), p.qs, p.cursor+d, d); i >= 0 {
			p.setCursor(i)
		}
		return true
	case fyne.KeyReturn, fyne.KeyEnter:
		p.endQuickSearch()
		p.openCurrent()
		return true
	}
	switch ev.Name {
	case fyne.KeyLeft, fyne.KeyRight, fyne.KeyPageUp, fyne.KeyPageDown, fyne.KeyHome, fyne.KeyEnd,
		fyne.KeyTab, fyne.KeyInsert, fyne.KeyDelete, fyne.KeyF1, fyne.KeyF2, fyne.KeyF3, fyne.KeyF4,
		fyne.KeyF5, fyne.KeyF6, fyne.KeyF7, fyne.KeyF8, fyne.KeyF9, fyne.KeyF10, fyne.KeyF11, fyne.KeyF12:
		p.endQuickSearch() // vége, és a billentyű a szokásos módon hat
		return false
	}
	return true // betű, szám: a karaktert a TypedRune hozza
}

func (a *App) typingSearches() bool { return a.settingBool("quickSearchTyping", false) }

// ── kedvenc mappák (Ctrl+D) ──

func (a *App) hotlist() []hotEntry { return decodeHotlist(a.setting("hotlist", "")) }

func (a *App) saveHotlist(h []hotEntry) {
	a.settings.Put("hotlist", encodeHotlist(h))
	a.settings.Save()
}

func (a *App) hotlistMenu() {
	p := a.active
	var items []*fyne.MenuItem
	for _, h := range a.hotlist() {
		h := h
		items = append(items, fyne.NewMenuItem(fmt.Sprintf(T("%s   —   %s"), h.Name, h.Path), func() {
			p.exitVFS()
			p.navigate(h.Path, "", true)
			p.focus()
		}))
	}
	if len(items) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
	}
	items = append(items,
		fyne.NewMenuItem(T("Aktuális mappa hozzáadása…"), a.addHotlist),
		fyne.NewMenuItem(T("Kedvencek szerkesztése…"), a.editHotlist))
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), a.win.Canvas(), p.cursorScreenPos())
}

func (a *App) addHotlist() {
	p := a.active
	if p.vfs != nil {
		a.warn(T("Archívum vagy FTP nem tehető a kedvencek közé."))
		return
	}
	path := p.path
	a.inputBox(T("Kedvenc mappa"), fmt.Sprintf(T("Név a menüben (%s):"), path), tabLabel(path), func(name string) {
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
	})
}

func (a *App) editHotlist() {
	h := a.hotlist()
	w := a.fa.NewWindow(T("Kedvenc mappák"))
	w.SetIcon(appIconRes)
	sel := -1
	list := widget.NewList(func() int { return len(h) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(fmt.Sprintf(T("%s   —   %s"), h[i].Name, h[i].Path))
		})
	list.OnSelected = func(i widget.ListItemID) { sel = i }
	save := func() { a.saveHotlist(h); list.Refresh() }
	move := func(d int) {
		if sel < 0 || sel+d < 0 || sel+d >= len(h) {
			return
		}
		h[sel], h[sel+d] = h[sel+d], h[sel]
		sel += d
		list.Select(sel)
		save()
	}
	side := container.NewVBox(
		widget.NewButton(T("Fel"), func() { move(-1) }),
		widget.NewButton(T("Le"), func() { move(1) }),
		widget.NewButton(T("Átnevezés…"), func() {
			if sel < 0 || sel >= len(h) {
				return
			}
			i := sel
			e := widget.NewEntry()
			e.SetText(h[i].Name)
			d := widgetDialog(w, T("Átnevezés"), e, func() { h[i].Name = e.Text; save() })
			d.Show()
			w.Canvas().Focus(e)
		}),
		widget.NewButton(T("Eltávolítás"), func() {
			if sel >= 0 && sel < len(h) {
				h = append(h[:sel], h[sel+1:]...)
				sel = -1
				list.UnselectAll()
				save()
			}
		}),
		widget.NewSeparator(),
		widget.NewButton(T("Bezárás"), w.Close),
	)
	w.SetContent(container.NewBorder(nil, widget.NewLabel(T("Új kedvenc: Ctrl+D → „Aktuális mappa hozzáadása”.")), nil, side, list))
	w.Resize(fyne.NewSize(640, 420))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyEscape {
			w.Close()
		}
	})
	w.Show()
}

// ── előzmények (Alt+↓) ──

func (a *App) historyPopup() {
	p := a.active
	hist := historyMenu(p.back, p.path, 25)
	if len(hist) == 0 {
		return
	}
	var items []*fyne.MenuItem
	for _, h := range hist {
		h := h
		items = append(items, fyne.NewMenuItem(h, func() {
			p.exitVFS()
			p.navigate(h, "", true)
			p.focus()
		}))
	}
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), a.win.Canvas(), p.cursorScreenPos())
}

// tabSel: induláskor a mentett fül kurzorpozíciója (parancssori mappánál nincs).
func (p *Panel) tabSel() string {
	if p.app.startArgs || p.tabIdx < 0 || p.tabIdx >= len(p.tabs) {
		return ""
	}
	return p.tabs[p.tabIdx].Sel
}
