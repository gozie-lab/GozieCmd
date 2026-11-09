//go:build linux

package main

// Egyszerű párbeszédablakok magyar gombokkal és billentyűzetkezeléssel
// (Enter: jóváhagyás, Esc: mégse).

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// keyCatcher láthatatlan, fókuszálható elem: a párbeszédablak billentyűit fogadja.
type keyCatcher struct {
	widget.BaseWidget
	onKey  func(*fyne.KeyEvent)
	onRune func(rune)
}

func newKeyCatcher(onKey func(*fyne.KeyEvent)) *keyCatcher {
	k := &keyCatcher{onKey: onKey}
	k.ExtendBaseWidget(k)
	return k
}

func (k *keyCatcher) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewWithoutLayout())
}
func (k *keyCatcher) MinSize() fyne.Size { return fyne.NewSize(0, 0) }
func (k *keyCatcher) FocusGained()       {}
func (k *keyCatcher) FocusLost()         {}
func (k *keyCatcher) TypedRune(r rune) {
	if k.onRune != nil {
		k.onRune(r)
	}
}
func (k *keyCatcher) TypedKey(ev *fyne.KeyEvent) {
	if k.onKey != nil {
		k.onKey(ev)
	}
}

// dlgEntry: beviteli mező, amelyben az Esc bezárja az ablakot.
type dlgEntry struct {
	widget.Entry
	onEsc func()
	onF2  func()
}

func newDlgEntry() *dlgEntry {
	e := &dlgEntry{}
	e.ExtendBaseWidget(e)
	return e
}

func (e *dlgEntry) TypedKey(ev *fyne.KeyEvent) {
	if ev.Name == fyne.KeyEscape && e.onEsc != nil {
		e.onEsc()
		return
	}
	if ev.Name == fyne.KeyF2 && e.onF2 != nil {
		e.onF2()
		return
	}
	e.Entry.TypedKey(ev)
}

// confirm: Igen/Nem kérdés; Enter = igen, Esc = nem.
func (a *App) confirm(title, msg, yesLabel string, onYes func()) {
	a.choice(title, msg, []string{yesLabel, T("Mégse")}, 0, func(i int) {
		if i == 0 {
			onYes()
		}
	})
}

// choice: több gomb; Enter = def, Esc = utolsó (mégse). A visszahívás a gomb sorszámát kapja.
func (a *App) choice(title, msg string, buttons []string, def int, cb func(int)) {
	lbl := widget.NewLabel(msg)
	lbl.Wrapping = fyne.TextWrapWord
	var d dialog.Dialog
	done := false
	finish := func(i int) {
		if done {
			return
		}
		done = true
		d.Hide()
		a.active.focus()
		cb(i)
	}
	var objs []fyne.CanvasObject
	for i, t := range buttons {
		i := i
		b := widget.NewButton(t, func() { finish(i) })
		if i == def {
			b.Importance = widget.HighImportance
		}
		objs = append(objs, b)
	}
	kc := newKeyCatcher(func(ev *fyne.KeyEvent) {
		switch ev.Name {
		case fyne.KeyReturn, fyne.KeyEnter:
			finish(def)
		case fyne.KeyEscape:
			finish(len(buttons) - 1)
		}
	})
	content := container.NewVBox(lbl, kc, container.NewCenter(container.NewHBox(objs...)))
	d = dialog.NewCustomWithoutButtons(title, content, a.win)
	d.Resize(fyne.NewSize(480, 0))
	d.Show()
	a.win.Canvas().Focus(kc)
}

// inputBox egysoros bevitel; Enter = OK, Esc = mégse. A cursorAt >= 0 a kurzor helye.
func (a *App) inputBox(title, prompt, def string, cb func(string)) {
	a.inputBoxAt(title, prompt, def, -1, cb)
}

func (a *App) inputBoxAt(title, prompt, def string, cursorAt int, cb func(string)) {
	e := newDlgEntry()
	e.SetText(def)
	if cursorAt >= 0 {
		e.CursorColumn = cursorAt
	} else {
		e.CursorColumn = len([]rune(def))
	}
	var d dialog.Dialog
	done := false
	finish := func(ok bool) {
		if done {
			return
		}
		done = true
		d.Hide()
		a.active.focus()
		if ok {
			cb(e.Text)
		}
	}
	e.OnSubmitted = func(string) { finish(true) }
	e.onEsc = func() { finish(false) }
	okB := widget.NewButton("OK", func() { finish(true) })
	okB.Importance = widget.HighImportance
	content := container.NewVBox(widget.NewLabel(prompt), e,
		container.NewCenter(container.NewHBox(okB, widget.NewButton(T("Mégse"), func() { finish(false) }))))
	d = dialog.NewCustomWithoutButtons(title, content, a.win)
	w := a.win.Canvas().Size().Width * 0.6
	if w < 480 {
		w = 480
	}
	d.Resize(fyne.NewSize(w, 0))
	d.Show()
	a.win.Canvas().Focus(e)
}

// showText egy hosszabb szöveget mutat görgethető ablakban.
func (a *App) showText(title, text string) {
	lbl := widget.NewLabel(text)
	lbl.Wrapping = fyne.TextWrapWord
	sc := container.NewVScroll(lbl)
	sc.SetMinSize(fyne.NewSize(560, 320))
	var d dialog.Dialog
	kc := newKeyCatcher(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyEscape || ev.Name == fyne.KeyReturn || ev.Name == fyne.KeyEnter {
			d.Hide()
			a.active.focus()
		}
	})
	d = dialog.NewCustom(title, T("Bezárás"), container.NewBorder(nil, kc, nil, nil, sc), a.win)
	d.SetOnClosed(func() { a.active.focus() })
	d.Show()
	a.win.Canvas().Focus(kc)
}

// inputBoxBg: mint az inputBox, de „Háttérben” gombbal is (F2).
func (a *App) inputBoxBg(title, prompt, def string, cb func(string, bool)) {
	e := newDlgEntry()
	e.SetText(def)
	e.CursorColumn = len([]rune(def))
	var d dialog.Dialog
	done := false
	finish := func(ok, bg bool) {
		if done {
			return
		}
		done = true
		d.Hide()
		a.active.focus()
		if ok {
			cb(e.Text, bg)
		}
	}
	e.OnSubmitted = func(string) { finish(true, false) }
	e.onEsc = func() { finish(false, false) }
	e.onF2 = func() { finish(true, true) }
	okB := widget.NewButton("OK", func() { finish(true, false) })
	okB.Importance = widget.HighImportance
	bgB := widget.NewButton(T("Háttérben (F2)"), func() { finish(true, true) })
	content := container.NewVBox(widget.NewLabel(prompt), e,
		container.NewCenter(container.NewHBox(okB, bgB, widget.NewButton(T("Mégse"), func() { finish(false, false) }))))
	d = dialog.NewCustomWithoutButtons(title, content, a.win)
	w := a.win.Canvas().Size().Width * 0.6
	if w < 520 {
		w = 520
	}
	d.Resize(fyne.NewSize(w, 0))
	d.Show()
	a.win.Canvas().Focus(e)
}
