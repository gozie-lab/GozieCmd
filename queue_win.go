//go:build windows

package main

// Háttérműveletek sora és mappaszinkronizálás (Windows-felület).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

var (
	procCoInitializeEx = ole32.NewProc("CoInitializeEx")
	procCoUninitialize = ole32.NewProc("CoUninitialize")
)

func (a *App) initQueue() {
	q := newTransferQueue()
	q.postUI = func(f func()) { a.mw.Synchronize(f) }
	q.onChange = a.updateQueueUI
	q.threadInit = func() func() {
		procCoInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED: a Windows másolóablakához
		return func() { procCoUninitialize.Call() }
	}
	q.ask = func(name, detail string) (int, error) {
		ch := make(chan int, 1)
		a.mw.Synchronize(func() { ch <- overwriteDialog(a.mw, name, detail) })
		switch <-ch { // overwriteDialog: 1 felülír, 2 mindet, 0 kihagy, 3 mindet kihagy, -1 mégse
		case 1:
			return 1, nil
		case 2:
			return 2, nil
		case 0:
			return 3, nil
		case 3:
			return 4, nil
		}
		return 0, errCancel
	}
	a.queue = q
}

// queueTransfer: helyi másolás / mozgatás a háttérben. A Windows saját másolóablakát
// mutatja (sebességgel, hátralévő idővel), és a sorban egymás után fut.
func (a *App) queueTransfer(p *Panel, srcs []string, target string, move bool) {
	op, title := uint32(foCopy), T("Másolás")
	if move {
		op, title = foMove, T("Mozgatás")
	}
	what := filepath.Base(srcs[0])
	if len(srcs) > 1 {
		what = Nf(len(srcs), "%d elem")
	}
	p.model.marked = map[string]bool{}
	p.repaint()
	p.updateStatus()
	a.queue.Add(title+": "+what+" → "+target, "", func(pr Progress) error {
		pr.Status(T("a Windows másolóablakában"))
		hr, aborted, err := fileOp(0, op, srcs, target, fofAllowUndo|fofNoConfirmMkdir|fofxShowElevationPrompt|fofxAddUndoRecord)
		switch {
		case err != nil && hr == 0:
			return err
		case aborted || isCancelHR(hr):
			return errCancel
		case hr != 0:
			return fmt.Errorf("%s", hresultText(hr))
		}
		return nil
	}, func(error) { a.refreshBoth() })
	a.updateQueueUI()
}

func (a *App) updateQueueUI() {
	if a.queueBtn == nil {
		return
	}
	items := a.queue.Snapshot()
	var run *qView
	waiting, failed := 0, 0
	for i := range items {
		switch items[i].State {
		case qRunning:
			run = &items[i]
		case qWaiting:
			waiting++
		case qFailed:
			failed++
		}
	}
	text := ""
	switch {
	case run != nil:
		text = "⏵ " + qLine(*run)
		if waiting > 0 {
			text += fmt.Sprintf(Tn(waiting, "  (+%d vár)"), waiting)
		}
	case waiting > 0:
		text = fmt.Sprintf(Tn(waiting, "⏵ %d háttérművelet vár"), waiting)
	case failed > 0:
		text = fmt.Sprintf(Tn(failed, "⚠ %d háttérművelet hibával zárult"), failed)
	}
	if r := []rune(text); len(r) > 60 {
		text = fmt.Sprintf(T("%s…"), string(r[:59]))
	}
	a.queueBtn.SetText(text)
	a.queueBtn.SetVisible(text != "")
	if a.queueRefresh != nil {
		a.queueRefresh()
	}
}

func (a *App) showQueue() {
	if a.queueDlg != nil {
		a.queueDlg.Show()
		return
	}
	var dlg *walk.Dialog
	var lb *walk.ListBox
	var detail *walk.Label
	var items []qView
	refresh := func() {
		items = a.queue.Snapshot()
		var lines []string
		for _, it := range items {
			lines = append(lines, qLine(it))
		}
		cur := lb.CurrentIndex()
		lb.SetModel(lines)
		if cur >= 0 && cur < len(lines) {
			lb.SetCurrentIndex(cur)
		}
		txt := ""
		if cur >= 0 && cur < len(items) {
			it := items[cur]
			txt = it.Status
			if len(it.Errs) > 0 {
				shown := it.Errs
				if len(shown) > 8 {
					shown = append(shown[:8:8], fmt.Sprintf(T("… és még %d"), len(it.Errs)-8))
				}
				txt = fmt.Sprintf(T("Hibák:\n%s"), strings.Join(shown, "\n"))
			}
		}
		detail.SetText(txt)
	}
	err := Dialog{
		AssignTo: &dlg, Title: T("Háttérműveletek"), MinSize: Size{Width: 700, Height: 380}, Layout: VBox{},
		Children: []Widget{
			ListBox{AssignTo: &lb, ItemStyler: newListStyler(&lb), OnCurrentIndexChanged: func() {
				if detail != nil {
					refresh()
				}
			}},
			Label{AssignTo: &detail},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{Text: T("Megszakítás"), OnClicked: func() {
					if i := lb.CurrentIndex(); i >= 0 && i < len(items) {
						a.queue.Cancel(items[i].ID)
					}
				}},
				PushButton{Text: T("Befejezettek törlése a listából"), OnClicked: func() { a.queue.ClearFinished() }},
				HSpacer{},
				PushButton{Text: T("Bezárás"), OnClicked: func() { dlg.Close(0) }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		return
	}
	themeDialog(dlg)
	a.queueDlg = dlg
	a.queueRefresh = refresh
	dlg.Disposing().Attach(func() { a.queueDlg = nil; a.queueRefresh = nil })
	refresh()
	dlg.Show() // nem modális: közben tovább lehet dolgozni
}

// ── mappák szinkronizálása ──

type syncModel struct {
	walk.TableModelBase
	items []syncItem
	show  []int
}

func (m *syncModel) RowCount() int { return len(m.show) }

func (m *syncModel) Value(row, col int) interface{} {
	if row >= len(m.show) {
		return ""
	}
	it := m.items[m.show[row]]
	side := func(s syncSide) string {
		if !s.Exists {
			return ""
		}
		if s.IsDir {
			return T("<mappa>")
		}
		return fmtSize(s.Size) + "   " + s.Mod.Format("2006.01.02 15:04")
	}
	switch col {
	case 0:
		return filepath.FromSlash(it.Rel)
	case 1:
		return side(it.A)
	case 2:
		switch {
		case it.Act != actNone:
			return it.Act.symbol()
		case it.Status == sSame:
			return "="
		}
		return "≠"
	case 3:
		return side(it.B)
	}
	return ""
}

func (a *App) syncDirs() {
	l, r := a.left, a.right
	if l.vfs != nil || r.vfs != nil {
		walk.MsgBox(a.mw, appName, T("A szinkronizálás két helyi mappa között működik."), walk.MsgBoxIconInformation)
		return
	}
	model := &syncModel{}
	var dlg *walk.Dialog
	var leftLE, rightLE *walk.LineEdit
	var subCB, contCB, sameCB *walk.CheckBox
	var modeCB *walk.ComboBox
	var tv *walk.TableView
	var summary *walk.Label
	var goBtn, closeBtn *walk.PushButton
	rebuild := func() {
		model.show = model.show[:0]
		for i, it := range model.items {
			if it.Status == sSame && !sameCB.Checked() {
				continue
			}
			model.show = append(model.show, i)
		}
		toB, toA, conf, same, bB, bA := syncSummary(model.items)
		summary.SetText(fmt.Sprintf(T("→ jobbra: %s (%s)    ← balra: %s (%s)    döntésre vár: %d    egyezik: %d")+"\n"+T("Kattints egy sor irányjelére (vagy Szóköz / Enter) az irány váltásához: →, ←, kihagyás."),
			Nf(toB, "%d elem"), fmtHuman(uint64(bB)), Nf(toA, "%d elem"), fmtHuman(uint64(bA)), conf, same))
		goBtn.SetEnabled(toA+toB > 0)
		model.PublishRowsReset()
		tv.Invalidate()
	}
	cycle := func(row int) {
		if row < 0 || row >= len(model.show) {
			return
		}
		it := &model.items[model.show[row]]
		if it.Status == sSame {
			return
		}
		it.cycleAct()
		rebuild()
		tv.SetCurrentIndex(row)
	}
	var compare func()
	compare = func() {
		la, ra := strings.TrimSpace(leftLE.Text()), strings.TrimSpace(rightLE.Text())
		for _, d := range []string{la, ra} {
			if st, err := os.Stat(d); err != nil || !st.IsDir() {
				walk.MsgBox(dlg, appName, fmt.Sprintf(T("Nem létező mappa:\n%s"), d), walk.MsgBoxIconWarning)
				return
			}
		}
		opt := syncOpts{Subdirs: subCB.Checked(), Content: contCB.Checked(), Mode: modeCB.CurrentIndex()}
		var res []syncItem
		if err := runJob(dlg, T("Összehasonlítás"), func(j *Job) error {
			var err error
			res, err = syncCompare(la, ra, opt, j)
			return err
		}); err != nil {
			return
		}
		model.items = res
		rebuild()
	}
	err := Dialog{
		AssignTo: &dlg, Title: T("Mappák szinkronizálása"), CancelButton: &closeBtn,
		MinSize: Size{Width: 980, Height: 620}, Layout: VBox{},
		Children: []Widget{
			Composite{Layout: Grid{Columns: 2}, Children: []Widget{
				LineEdit{AssignTo: &leftLE, Text: l.path},
				LineEdit{AssignTo: &rightLE, Text: r.path},
			}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				CheckBox{AssignTo: &subCB, Text: T("Almappákkal"), Checked: true},
				CheckBox{AssignTo: &contCB, Text: T("Tartalom szerint is (lassabb)")},
				CheckBox{AssignTo: &sameCB, Text: T("Egyezők mutatása"), OnCheckedChanged: func() {
					if summary != nil {
						rebuild()
					}
				}},
				ComboBox{AssignTo: &modeCB, Model: []string{T("Mindkét irányba"), T("Csak balról jobbra →"), T("Csak jobbról balra ←")},
					CurrentIndex: 0, OnCurrentIndexChanged: func() {
						if model.items != nil {
							compare()
						}
					}},
				HSpacer{},
				PushButton{Text: T("Összehasonlítás"), OnClicked: func() { compare() }},
				PushButton{AssignTo: &goBtn, Text: T("Szinkronizálás"), Enabled: false, OnClicked: func() {
					la, ra := strings.TrimSpace(leftLE.Text()), strings.TrimSpace(rightLE.Text())
					todo := append([]syncItem(nil), model.items...)
					runJob(dlg, T("Szinkronizálás"), func(j *Job) error { return syncExecute(todo, la, ra, j) })
					a.refreshBoth()
					compare()
				}},
			}},
			TableView{LastColumnStretched: true, AssignTo: &tv, Model: model, Columns: []TableViewColumn{
				{Title: T("Fájl"), Width: 340}, {Title: T("Bal oldal"), Width: 200},
				{Title: T("Irány"), Width: 60, Alignment: AlignCenter}, {Title: T("Jobb oldal"), Width: 200},
			}, StyleCell: func(st *walk.CellStyle) {
				if st.Col() != 2 || st.Row() >= len(model.show) {
					return
				}
				it := model.items[model.show[st.Row()]]
				switch {
				case it.Act == actToB:
					st.TextColor = walk.RGB(30, 140, 50)
				case it.Act == actToA:
					st.TextColor = walk.RGB(30, 100, 210)
				case it.Status != sSame:
					st.TextColor = walk.RGB(210, 110, 20)
				}
			}, OnItemActivated: func() { cycle(tv.CurrentIndex()) },
				OnMouseDown: func(x, y int, b walk.MouseButton) {
					if b == walk.LeftButton {
						if row := tv.IndexAt(x, y); row >= 0 {
							w := tv.Columns().At(0).Width() + tv.Columns().At(1).Width()
							if x >= w && x < w+tv.Columns().At(2).Width() {
								cycle(row)
							}
						}
					}
				},
				OnKeyDown: func(k walk.Key) {
					if k == walk.KeySpace {
						cycle(tv.CurrentIndex())
					}
				}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				Label{AssignTo: &summary, Text: T("Összehasonlítás…")},
				HSpacer{},
				PushButton{AssignTo: &closeBtn, Text: T("Bezárás"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, appName, err.Error(), walk.MsgBoxIconError)
		return
	}
	themeDialog(dlg)
	a.sizeableDialog(dlg, a.mw, "sync", 1000, 640)
	dlg.Starting().Attach(func() { dlg.Synchronize(compare) })
	dlg.Run()
}
