//go:build windows

package main

import (
	"fmt"
	"path/filepath"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// archivePasswordBox: jelszó bekérése egy archívumhoz (a felület szálán, modálisan).
func archivePasswordBox(a *App, name string, wrong bool) (string, bool) {
	var dlg *walk.Dialog
	var le *walk.LineEdit
	var ok, cancel *walk.PushButton
	var result string
	msg := fmt.Sprintf(T("A(z) „%s” archívum jelszóval védett.\nJelszó:"), name)
	if wrong {
		msg = fmt.Sprintf(T("Hibás jelszó a(z) „%s” archívumhoz. Próbáld újra:"), name)
	}
	err := Dialog{
		AssignTo: &dlg, Title: T("Jelszóval védett archívum"), DefaultButton: &ok, CancelButton: &cancel,
		MinSize: Size{Width: 440, Height: 150}, Layout: VBox{},
		Children: []Widget{
			Label{Text: msg},
			LineEdit{AssignTo: &le, PasswordMode: true},
			Label{Text: T("A jelszót a program nem menti, csak amíg fut, addig emlékszik rá.")},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &ok, Text: T("OK"), OnClicked: func() { result = le.Text(); dlg.Accept() }},
				PushButton{AssignTo: &cancel, Text: T("Mégse"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		return "", false
	}
	themeDialog(dlg)
	dlg.Starting().Attach(func() { le.SetFocus() })
	res := dlg.Run()
	return result, res == walk.DlgCmdOK
}

// initPasswordPrompt: a munkaszálakról hívható jelszókérő.
func (a *App) initPasswordPrompt() {
	passwordPrompt = func(name string, wrong bool) (string, bool) {
		type res struct {
			pw string
			ok bool
		}
		ch := make(chan res, 1)
		a.mw.Synchronize(func() {
			pw, ok := archivePasswordBox(a, name, wrong)
			ch <- res{pw, ok}
		})
		r := <-ch
		return r.pw, r.ok
	}
}

// openArchiveUI: megnyitás a felület szálán, szükség esetén jelszókéréssel.
func (a *App) openArchiveUI(path string) (Archive, error) {
	for {
		z, err := openArchive(path)
		npe, ok := asNeedPassword(err)
		if !ok {
			return z, err
		}
		pw, ok := archivePasswordBox(a, filepath.Base(path), npe.Wrong)
		if !ok {
			return nil, errCancel
		}
		setArchivePassword(path, pw)
	}
}
