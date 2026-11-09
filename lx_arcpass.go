//go:build linux

package main

import (
	"fmt"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// archivePasswordDialog: jelszó bekérése egy archívumhoz (a felület szálán).
func (a *App) archivePasswordDialog(name string, wrong bool, cb func(pw string, ok bool)) {
	msg := fmt.Sprintf(T("A(z) „%s” archívum jelszóval védett.\nJelszó:"), name)
	if wrong {
		msg = fmt.Sprintf(T("Hibás jelszó a(z) „%s” archívumhoz. Próbáld újra:"), name)
	}
	pw := widget.NewPasswordEntry()
	note := widget.NewLabel(T("A jelszót a program nem menti, csak amíg fut, addig emlékszik rá."))
	note.Wrapping = fyne.TextWrapWord
	done := false
	finish := func(ok bool) {
		if done {
			return
		}
		done = true
		a.active.focus()
		cb(pw.Text, ok)
	}
	d := dialog.NewCustomConfirm(T("Jelszóval védett archívum"), T("OK"), T("Mégse"),
		container.NewVBox(widget.NewLabel(msg), pw, note), finish, a.win)
	pw.OnSubmitted = func(string) { d.Hide(); finish(true) }
	d.Resize(fyne.NewSize(460, 0))
	d.Show()
	a.win.Canvas().Focus(pw)
}

func (a *App) initPasswordPrompt() {
	passwordPrompt = func(name string, wrong bool) (string, bool) {
		type res struct {
			pw string
			ok bool
		}
		ch := make(chan res, 1)
		promptsOpen.Add(1) // amíg nyitva van, a folyamatablak nem jelenik meg fölötte
		fyne.Do(func() {
			a.archivePasswordDialog(name, wrong, func(pw string, ok bool) { ch <- res{pw, ok} })
		})
		defer promptsOpen.Add(-1)
		r := <-ch
		return r.pw, r.ok
	}
}

// openArchiveUI megnyitja az archívumot; ha jelszó kell, bekéri és újrapróbálja.
func (a *App) openArchiveUI(path string, then func(Archive, error)) {
	z, err := openArchive(path)
	npe, ok := asNeedPassword(err)
	if !ok {
		then(z, err)
		return
	}
	a.archivePasswordDialog(filepath.Base(path), npe.Wrong, func(pw string, ok bool) {
		if !ok {
			then(nil, errCancel)
			return
		}
		setArchivePassword(path, pw)
		a.openArchiveUI(path, then)
	})
}
