//go:build linux

package main

// Többfájl-átnevező (Ctrl+M): élő előnézet, ütközésvédelem, visszavonás.
// A számítás (maszkok, csere, számláló) közös a Windows-változattal.

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// maskEntry megjegyzi, hogy a felhasználó utoljára melyik maszkmezőben volt.
type maskEntry struct {
	widget.Entry
	onFocus func()
	onEsc   func()
}

func (e *maskEntry) TypedKey(ev *fyne.KeyEvent) {
	if ev.Name == fyne.KeyEscape && e.onEsc != nil {
		e.onEsc()
		return
	}
	e.Entry.TypedKey(ev)
}

func newMaskEntry(text string, onFocus func()) *maskEntry {
	e := &maskEntry{onFocus: onFocus}
	e.ExtendBaseWidget(e)
	e.SetText(text)
	return e
}

func (e *maskEntry) FocusGained() {
	e.Entry.FocusGained()
	if e.onFocus != nil {
		e.onFocus()
	}
}

func (a *App) multiRename() {
	p := a.active
	if p.vfs != nil {
		a.warn(T("A többfájl-átnevezés csak helyi mappában működik."))
		return
	}
	ents := p.selectedEntries()
	if len(ents) == 0 {
		return
	}
	var items []renameItem
	for _, e := range ents {
		items = append(items, renameItem{Name: e.Name, Dir: filepath.Dir(e.Path), Mod: e.ModTime, IsDir: e.IsDir})
	}
	var names, probs []string
	var undo []renamePair

	w := a.fa.NewWindow(T("Többfájl-átnevezés"))
	w.SetIcon(appIconRes)
	var lastMask *maskEntry
	var nameE, extE *maskEntry
	searchE, replE := widget.NewEntry(), widget.NewEntry()
	startE, stepE, digE := widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	startE.SetText("1")
	stepE.SetText("1")
	digE.SetText("3")
	regexC := widget.NewCheck(T("Reguláris kifejezés"), nil)
	caseC := widget.NewCheck(T("Kis/nagybetű különbözik"), nil)
	caseS := widget.NewSelect([]string{T("változatlan"), T("kisbetű"), T("NAGYBETŰ"), T("Első betű nagy"), T("Minden Szó Nagy")}, nil)
	caseS.SetSelectedIndex(0)
	status := widget.NewLabel("")
	var goB, undoB *widget.Button

	num := func(e *widget.Entry, def int) int {
		v, err := strconv.Atoi(strings.TrimSpace(e.Text))
		if err != nil {
			return def
		}
		return v
	}
	var list *widget.List
	update := func() {
		if nameE == nil || extE == nil || list == nil {
			return
		}
		o := renameOpts{NameMask: nameE.Text, ExtMask: extE.Text, Search: searchE.Text, Replace: replE.Text,
			Regex: regexC.Checked, MatchCase: caseC.Checked, Case: caseS.SelectedIndex(),
			CStart: num(startE, 1), CStep: num(stepE, 1), CDigits: num(digE, 1)}
		n, pr, err := computeNames(items, o)
		if err != nil {
			status.SetText(err.Error())
			goB.Disable()
			return
		}
		names, probs = n, pr
		bad, changed := 0, 0
		for i := range names {
			if probs[i] != "" {
				bad++
			} else if names[i] != items[i].Name {
				changed++
			}
		}
		list.Refresh()
		if bad > 0 {
			status.SetText(fmt.Sprintf(Tn(bad, "%d név hibás – javítsd, mielőtt átnevezel."), bad))
		} else {
			status.SetText(fmt.Sprintf(Tn(len(names), "%d / %d elem kap új nevet."), changed, len(names)))
		}
		if bad == 0 && changed > 0 {
			goB.Enable()
		} else {
			goB.Disable()
		}
	}
	nameE = newMaskEntry("[N]", func() { lastMask = nameE })
	extE = newMaskEntry("[E]", func() { lastMask = extE })
	nameE.onEsc, extE.onEsc = w.Close, w.Close
	for _, e := range []*widget.Entry{&nameE.Entry, &extE.Entry, searchE, replE, startE, stepE, digE} {
		e.OnChanged = func(string) { update() }
	}
	regexC.OnChanged = func(bool) { update() }
	caseC.OnChanged = func(bool) { update() }
	caseS.OnChanged = func(string) { update() }

	ins := func(tok string) func() {
		return func() {
			e := lastMask
			if e == nil {
				e = nameE
			}
			r := []rune(e.Text)
			at := e.CursorColumn
			if at < 0 || at > len(r) || e.CursorRow != 0 {
				at = len(r)
			}
			e.SetText(string(r[:at]) + tok + string(r[at:]))
			e.CursorColumn = at + len([]rune(tok))
			e.Refresh()
			w.Canvas().Focus(e)
		}
	}

	list = widget.NewList(func() int { return len(items) },
		func() fyne.CanvasObject {
			t := func() *canvas.Text { return canvas.NewText("", theme.Color(theme.ColorNameForeground)) }
			return container.New(layout.NewGridLayout(3), t(), t(), t())
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			c := o.(*fyne.Container).Objects
			old, nw, pr := c[0].(*canvas.Text), c[1].(*canvas.Text), c[2].(*canvas.Text)
			fg := theme.Color(theme.ColorNameForeground)
			old.Text, old.Color = items[i].Name, fg
			nw.Text, nw.Color, pr.Text = "", fg, ""
			pr.Color = color.NRGBA{220, 50, 40, 255}
			if i < len(names) {
				nw.Text = names[i]
				if names[i] != items[i].Name {
					nw.Color = color.NRGBA{40, 160, 60, 255}
				}
			}
			if i < len(probs) && probs[i] != "" {
				pr.Text = probs[i]
				nw.Color = pr.Color
			}
			for _, t := range []*canvas.Text{old, nw, pr} {
				t.Refresh()
			}
		})

	goB = widget.NewButton(T("Átnevezés"), func() {
		n, pr, err := computeNames(items, renameOpts{NameMask: nameE.Text, ExtMask: extE.Text, Search: searchE.Text,
			Replace: replE.Text, Regex: regexC.Checked, MatchCase: caseC.Checked, Case: caseS.SelectedIndex(),
			CStart: num(startE, 1), CStep: num(stepE, 1), CDigits: num(digE, 1)})
		if err != nil {
			return
		}
		inSet := map[string]bool{}
		for _, it := range items {
			inSet[filepath.Join(it.Dir, it.Name)] = true
		}
		var pairs []renamePair
		for i, it := range items {
			if pr[i] != "" {
				return
			}
			from, to := filepath.Join(it.Dir, it.Name), filepath.Join(it.Dir, n[i])
			if from == to {
				continue
			}
			if _, err := os.Lstat(to); err == nil && !inSet[to] {
				a.warn(fmt.Sprintf(T("Már létezik ilyen nevű fájl:\n%s"), to))
				return
			}
			pairs = append(pairs, renamePair{from, to})
		}
		if err := doRenames(pairs); err != nil {
			a.warn(fmt.Sprintf(T("Az átnevezés nem sikerült, semmi nem változott:\n%s"), err.Error()))
			return
		}
		undo = nil
		for _, pr := range pairs {
			undo = append(undo, renamePair{pr.to, pr.from})
		}
		for i := range items {
			items[i].Name = n[i]
		}
		undoB.Enable()
		nameE.SetText("[N]")
		extE.SetText("[E]")
		searchE.SetText("")
		update()
		status.SetText(fmt.Sprintf(Tn(len(pairs), "%d elem átnevezve."), len(pairs)))
		p.refresh()
	})
	goB.Importance = widget.HighImportance
	undoB = widget.NewButton(T("Visszavonás"), func() {
		if len(undo) == 0 {
			return
		}
		if err := doRenames(undo); err != nil {
			a.warn(fmt.Sprintf(T("A visszavonás nem sikerült:\n%s"), err.Error()))
			return
		}
		back := map[string]string{}
		for _, u := range undo {
			back[u.from] = filepath.Base(u.to)
		}
		for i, it := range items {
			if n, ok := back[filepath.Join(it.Dir, it.Name)]; ok {
				items[i].Name = n
			}
		}
		undo = nil
		undoB.Disable()
		update()
		status.SetText(T("Visszavonva."))
		p.refresh()
	})
	undoB.Disable()

	masks := container.New(layout.NewFormLayout(),
		widget.NewLabel(T("Névmaszk:")), nameE,
		widget.NewLabel(T("Kiterjesztés:")), extE,
	)
	insRow := container.NewHBox(widget.NewLabel(T("Beszúrás:")),
		widget.NewButton(T("[N] név"), ins("[N]")), widget.NewButton(T("[N1-3] részlet"), ins("[N1-3]")),
		widget.NewButton(T("[C] számláló"), ins("[C]")), widget.NewButton(T("[YMD] dátum"), ins("[YMD]")),
		widget.NewButton(T("[hms] idő"), ins("[hms]")), widget.NewButton(T("[P] mappa"), ins("[P]")))
	opts := container.New(layout.NewGridLayout(4),
		widget.NewLabel(T("Keresés:")), searchE, widget.NewLabel(T("Csere:")), replE,
		regexC, caseC, widget.NewLabel(T("Betűméret:")), caseS,
		widget.NewLabel(T("Számláló kezdete:")), startE, widget.NewLabel(T("Lépés / jegyek:")), container.NewGridWithColumns(2, stepE, digE),
	)
	head := container.New(layout.NewGridLayout(3), widget.NewLabelWithStyle(T("Régi név"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle(T("Új név"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle(T("Hiba"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	help := widget.NewLabel(T("[N] név, [E] kiterjesztés, [N2-5] / [N3] / [N-4] / [N2,3] részlet, [C] számláló, [P] szülőmappa,\n[Y] [M] [D] [h] [m] [s] dátum és idő részei, [YMD] év-hó-nap, [hms] óra.perc.mp, [[ és ]] szögletes zárójel."))
	bottom := container.NewVBox(help, container.NewBorder(nil, nil, nil,
		container.NewHBox(goB, undoB, widget.NewButton(T("Bezárás"), w.Close)), status))
	w.SetContent(container.NewBorder(container.NewVBox(masks, insRow, opts, head), bottom, nil, nil, list))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyEscape {
			w.Close()
		}
	})
	a.restoreWinSize(w, "rename", 900, 620)
	w.SetOnClosed(func() { a.saveWinSize(w, "rename"); p.refresh() })
	update()
	w.Show()
	w.Canvas().Focus(nameE)
}
