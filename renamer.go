//go:build windows

package main

// Többfájl-átnevező ablak (Ctrl+M): élő előnézet, ütközésvédelem, visszavonás.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

type renameModel struct {
	walk.TableModelBase
	items    []renameItem
	names    []string
	problems []string
}

func (m *renameModel) RowCount() int { return len(m.items) }

func (m *renameModel) Value(row, col int) interface{} {
	if row >= len(m.items) {
		return ""
	}
	switch col {
	case 0:
		return m.items[row].Name
	case 1:
		if row < len(m.names) {
			return m.names[row]
		}
	case 2:
		if row < len(m.problems) {
			return m.problems[row]
		}
	}
	return ""
}

func (a *App) multiRename() {
	if a.notInVFS() {
		return
	}
	p := a.active
	ents := p.selectedEntries()
	if len(ents) == 0 {
		return
	}
	model := &renameModel{}
	for _, e := range ents {
		model.items = append(model.items, renameItem{Name: e.Name, Dir: filepath.Dir(e.Path), Mod: e.ModTime, IsDir: e.IsDir})
	}
	var undo []renamePair

	var dlg *walk.Dialog
	var nameLE, extLE, searchLE, replLE, startLE, stepLE, digLE *walk.LineEdit
	var regexCB, caseCB *walk.CheckBox
	var caseSel *walk.ComboBox
	var tv *walk.TableView
	var status *walk.Label
	var goBtn, undoBtn, closeBtn *walk.PushButton
	ready := false

	opts := func() renameOpts {
		n := func(le *walk.LineEdit, def int) int {
			v, err := strconv.Atoi(strings.TrimSpace(le.Text()))
			if err != nil {
				return def
			}
			return v
		}
		return renameOpts{
			NameMask: nameLE.Text(), ExtMask: extLE.Text(),
			Search: searchLE.Text(), Replace: replLE.Text(),
			Regex: regexCB.Checked(), MatchCase: caseCB.Checked(),
			Case:   caseSel.CurrentIndex(),
			CStart: n(startLE, 1), CStep: n(stepLE, 1), CDigits: n(digLE, 1),
		}
	}
	update := func() {
		if !ready {
			return
		}
		names, probs, err := computeNames(model.items, opts())
		if err != nil {
			status.SetText(err.Error())
			goBtn.SetEnabled(false)
			return
		}
		model.names, model.problems = names, probs
		bad, changed := 0, 0
		for i := range names {
			if probs[i] != "" {
				bad++
			} else if names[i] != model.items[i].Name {
				changed++
			}
		}
		model.PublishRowsReset()
		tv.Invalidate() // azonos sorszámnál a lista magától nem rajzolja újra a sorokat
		switch {
		case bad > 0:
			status.SetText(fmt.Sprintf(Tn(bad, "%d név hibás – javítsd, mielőtt átnevezel."), bad))
		default:
			status.SetText(fmt.Sprintf(Tn(len(names), "%d / %d elem kap új nevet."), changed, len(names)))
		}
		goBtn.SetEnabled(bad == 0 && changed > 0)
	}
	var lastMask *walk.LineEdit // az utoljára használt maszkmező (név vagy kiterjesztés)
	ins := func(tok string) func() {
		return func() {
			le := lastMask
			if le == nil {
				le = nameLE
			}
			// a kurzor helyére (a kijelölés helyére) szúr be; a pozíciók UTF-16 egységek
			u := utf16.Encode([]rune(le.Text()))
			st, en := le.TextSelection()
			if st < 0 || st > len(u) {
				st = len(u)
			}
			if en < st || en > len(u) {
				en = st
			}
			t := utf16.Encode([]rune(tok))
			nu := append(append(append([]uint16{}, u[:st]...), t...), u[en:]...)
			le.SetText(string(utf16.Decode(nu)))
			le.SetFocus()
			le.SetTextSelection(st+len(t), st+len(t))
		}
	}

	err := Dialog{
		AssignTo: &dlg, Title: T("Többfájl-átnevezés"), DefaultButton: &goBtn, CancelButton: &closeBtn,
		MinSize: Size{Width: 860, Height: 600}, Layout: VBox{},
		Children: []Widget{
			Composite{Layout: Grid{Columns: 4}, Children: []Widget{
				Label{Text: T("Névmaszk:")},
				LineEdit{AssignTo: &nameLE, Text: "[N]", OnTextChanged: update},
				Label{Text: T("Kiterjesztés:")},
				LineEdit{AssignTo: &extLE, Text: "[E]", OnTextChanged: update, MaxSize: Size{Width: 140}},
			}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				Label{Text: T("Beszúrás:")},
				PushButton{Text: T("[N] név"), OnClicked: ins("[N]")},
				PushButton{Text: T("[N1-3] részlet"), OnClicked: ins("[N1-3]")},
				PushButton{Text: T("[C] számláló"), OnClicked: ins("[C]")},
				PushButton{Text: T("[YMD] dátum"), OnClicked: ins("[YMD]")},
				PushButton{Text: T("[hms] idő"), OnClicked: ins("[hms]")},
				PushButton{Text: T("[P] mappa"), OnClicked: ins("[P]")},
				HSpacer{},
			}},
			Composite{Layout: Grid{Columns: 6}, Children: []Widget{
				Label{Text: T("Keresés:")},
				LineEdit{AssignTo: &searchLE, OnTextChanged: update},
				Label{Text: T("Csere:")},
				LineEdit{AssignTo: &replLE, OnTextChanged: update},
				CheckBox{AssignTo: &regexCB, Text: T("Reguláris kifejezés"), OnCheckedChanged: update},
				CheckBox{AssignTo: &caseCB, Text: T("Kis/nagybetű különbözik"), OnCheckedChanged: update},
				Label{Text: T("Betűméret:")},
				ComboBox{AssignTo: &caseSel, Model: []string{T("változatlan"), T("kisbetű"), T("NAGYBETŰ"), T("Első betű nagy"), T("Minden Szó Nagy")},
					CurrentIndex: 0, OnCurrentIndexChanged: update},
				Label{Text: T("Számláló kezdete:")},
				LineEdit{AssignTo: &startLE, Text: "1", OnTextChanged: update, MaxSize: Size{Width: 70}},
				Label{Text: T("Lépés / jegyek:")},
				Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
					LineEdit{AssignTo: &stepLE, Text: "1", OnTextChanged: update, MaxSize: Size{Width: 50}},
					LineEdit{AssignTo: &digLE, Text: "3", OnTextChanged: update, MaxSize: Size{Width: 50}},
				}},
			}},
			TableView{LastColumnStretched: true, AssignTo: &tv, Model: model, Columns: []TableViewColumn{
				{Title: T("Régi név"), Width: 300}, {Title: T("Új név"), Width: 300}, {Title: T("Hiba"), Width: 200},
			}, StyleCell: func(st *walk.CellStyle) {
				if r := st.Row(); r < len(model.problems) && model.problems[r] != "" {
					st.TextColor = walk.RGB(200, 0, 0)
				} else if r < len(model.names) && st.Col() == 1 && model.names[r] != model.items[r].Name {
					st.TextColor = walk.RGB(0, 110, 0)
				}
			}},
			Label{Text: T("[N] név, [E] kiterjesztés, [N2-5] / [N3] / [N-4] / [N2,3] részlet, [C] számláló, [P] szülőmappa,\n[Y] [M] [D] [h] [m] [s] dátum és idő részei, [YMD] év-hó-nap, [hms] óra.perc.mp, [[ és ]] szögletes zárójel.")},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				Label{AssignTo: &status},
				HSpacer{},
				PushButton{AssignTo: &goBtn, Text: T("Átnevezés"), OnClicked: func() {
					names, probs, err := computeNames(model.items, opts())
					if err != nil {
						return
					}
					var pairs []renamePair
					inSet := map[string]bool{}
					for _, it := range model.items {
						inSet[strings.ToLower(filepath.Join(it.Dir, it.Name))] = true
					}
					for i, it := range model.items {
						if probs[i] != "" {
							return
						}
						from, to := filepath.Join(it.Dir, it.Name), filepath.Join(it.Dir, names[i])
						if from == to {
							continue
						}
						if _, err := os.Lstat(to); err == nil && !inSet[strings.ToLower(to)] {
							walk.MsgBox(dlg, appName, fmt.Sprintf(T("Már létezik ilyen nevű fájl:\n%s"), to), walk.MsgBoxIconWarning)
							return
						}
						pairs = append(pairs, renamePair{from, to})
					}
					if err := doRenames(pairs); err != nil {
						walk.MsgBox(dlg, appName, fmt.Sprintf(T("Az átnevezés nem sikerült, semmi nem változott:\n%s"), err.Error()), walk.MsgBoxIconWarning)
						return
					}
					undo = nil
					for _, pr := range pairs {
						undo = append(undo, renamePair{pr.to, pr.from})
					}
					for i := range model.items {
						model.items[i].Name = names[i]
					}
					undoBtn.SetEnabled(len(undo) > 0)
					nameLE.SetText("[N]")
					extLE.SetText("[E]")
					searchLE.SetText("")
					update()
					status.SetText(fmt.Sprintf(Tn(len(pairs), "%d elem átnevezve."), len(pairs)))
					p.refresh()
				}},
				PushButton{AssignTo: &undoBtn, Text: T("Visszavonás"), Enabled: false, OnClicked: func() {
					if len(undo) == 0 {
						return
					}
					if err := doRenames(undo); err != nil {
						walk.MsgBox(dlg, appName, fmt.Sprintf(T("A visszavonás nem sikerült:\n%s"), err.Error()), walk.MsgBoxIconWarning)
						return
					}
					back := map[string]string{}
					for _, u := range undo {
						back[strings.ToLower(u.from)] = filepath.Base(u.to)
					}
					for i, it := range model.items {
						if n, ok := back[strings.ToLower(filepath.Join(it.Dir, it.Name))]; ok {
							model.items[i].Name = n
						}
					}
					undo = nil
					undoBtn.SetEnabled(false)
					update()
					status.SetText(T("Visszavonva."))
					p.refresh()
				}},
				PushButton{AssignTo: &closeBtn, Text: T("Bezárás"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, appName, err.Error(), walk.MsgBoxIconError)
		return
	}
	themeDialog(dlg)
	if a.icon != nil {
		dlg.SetIcon(a.icon)
	}
	ready = true
	for _, le := range []*walk.LineEdit{nameLE, extLE} {
		le := le
		le.FocusedChanged().Attach(func() {
			if le.Focused() {
				lastMask = le
			}
		})
	}
	update()
	dlg.Starting().Attach(func() { nameLE.SetFocus() })
	dlg.Run()
	p.refresh()
}
