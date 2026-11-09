//go:build windows

package main

// Fájleszközök (Windows-felület): tartalmi összehasonlítás, ellenőrzőösszeg,
// ismétlődő fájlok, darabolás/összefűzés, attribútumok.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

func (a *App) needLocal(p *Panel) bool {
	if p.vfs != nil {
		walk.MsgBox(a.mw, appName, T("Ez a művelet csak helyi fájlokkal működik (archívumon és FTP-n belül nem)."), walk.MsgBoxIconInformation)
		return false
	}
	return true
}

func (a *App) info(msg string) { walk.MsgBox(a.mw, appName, msg, walk.MsgBoxIconInformation) }

// ── tartalmi összehasonlítás ──

func comparePair(act, oth *Panel) (string, string, string) {
	return pickPair(act.selectedEntries(), act.model.marked, act.currentEntry(), oth.path, oth.currentEntry())
}

func (a *App) compareContent() {
	p, o := a.active, a.other()
	if !a.needLocal(p) || !a.needLocal(o) {
		return
	}
	pa, pb, msg := comparePair(p, o)
	if msg != "" {
		a.info(msg)
		return
	}
	if p == a.right && pb != "" && filepath.Dir(pb) == a.left.path {
		pa, pb = pb, pa // a bal panel fájlja legyen bal oldalon
	}
	a.diffDialog(pa, pb)
}

type diffModel struct {
	walk.TableModelBase
	res  *diffResult
	show []int
}

func (m *diffModel) RowCount() int { return len(m.show) }
func (m *diffModel) Value(row, col int) interface{} {
	if row >= len(m.show) {
		return ""
	}
	r := m.res.Rows[m.show[row]]
	if m.res.Binary {
		switch col {
		case 1:
			return r.TA
		case 3:
			return r.TB
		}
		return ""
	}
	num := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	tabs := func(s string) string { return strings.ReplaceAll(s, "\t", "    ") }
	switch col {
	case 0:
		return num(r.A)
	case 1:
		return tabs(r.TA)
	case 2:
		return num(r.B)
	case 3:
		return tabs(r.TB)
	}
	return ""
}

func diffColors(kind byte) (walk.Color, bool) {
	if darkActive {
		switch kind {
		case 'c':
			return walk.RGB(80, 68, 20), true
		case '-':
			return walk.RGB(90, 35, 35), true
		case '+':
			return walk.RGB(30, 75, 40), true
		}
		return 0, false
	}
	switch kind {
	case 'c':
		return walk.RGB(255, 243, 190), true
	case '-':
		return walk.RGB(255, 215, 215), true
	case '+':
		return walk.RGB(210, 245, 210), true
	}
	return 0, false
}

func (a *App) diffDialog(pa, pb string) {
	model := &diffModel{}
	var dlg *walk.Dialog
	var tv *walk.TableView
	var info *walk.Label
	var spaceCB, caseCB, onlyCB *walk.CheckBox
	var closeBtn *walk.PushButton
	rebuild := func() {
		model.show = model.show[:0]
		for i, r := range model.res.Rows {
			if onlyCB.Checked() && r.Kind == '=' {
				continue
			}
			model.show = append(model.show, i)
		}
		model.PublishRowsReset()
	}
	load := func() {
		res, err := compareFiles(pa, pb, spaceCB.Checked(), caseCB.Checked())
		if err != nil {
			walk.MsgBox(dlg, appName, errText(err), walk.MsgBoxIconWarning)
			return
		}
		model.res = res
		var t string
		switch {
		case res.Same && res.Binary:
			t = T("A két fájl tartalma azonos.")
		case res.Same:
			t = fmt.Sprintf(T("A két fájl tartalma azonos%s"), map[bool]string{true: T(" (a beállított eltérések figyelmen kívül hagyásával)."), false: "."}[spaceCB.Checked() || caseCB.Checked()])
		case res.Binary:
			t = fmt.Sprintf(T("Bináris fájlok. %s"), res.Note)
		default:
			t = fmt.Sprintf(Tn(res.Changes, "%d eltérő szakasz.  Sárga: módosult sor · piros: csak a bal oldalon · zöld: csak a jobb oldalon."), res.Changes)
		}
		info.SetText(t)
		rebuild()
		jump(tv, model, 1, -1)
	}
	err := Dialog{
		AssignTo: &dlg, Title: T("Fájlok összehasonlítása"), CancelButton: &closeBtn,
		MinSize: Size{Width: 900, Height: 560}, Layout: VBox{},
		Children: []Widget{
			Composite{Layout: Grid{Columns: 2, MarginsZero: true}, Children: []Widget{
				LineEdit{Text: pa, ReadOnly: true},
				LineEdit{Text: pb, ReadOnly: true},
			}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				CheckBox{AssignTo: &spaceCB, Text: T("Szóközök figyelmen kívül"), OnCheckedChanged: func() {
					if model.res != nil {
						load()
					}
				}},
				CheckBox{AssignTo: &caseCB, Text: T("Kis- és nagybetű egyezik"), OnCheckedChanged: func() {
					if model.res != nil {
						load()
					}
				}},
				CheckBox{AssignTo: &onlyCB, Text: T("Csak az eltérések"), OnCheckedChanged: func() {
					if model.res != nil {
						rebuild()
					}
				}},
				HSpacer{},
				PushButton{Text: T("▲ Előző eltérés"), OnClicked: func() { jump(tv, model, -1, tv.CurrentIndex()) }},
				PushButton{Text: T("▼ Következő eltérés"), OnClicked: func() { jump(tv, model, 1, tv.CurrentIndex()) }},
			}},
			TableView{AssignTo: &tv, Model: model, LastColumnStretched: true,
				Columns: []TableViewColumn{
					{Title: "#", Width: 50, Alignment: AlignFar},
					{Title: filepath.Base(pa), Width: 380},
					{Title: "#", Width: 50, Alignment: AlignFar},
					{Title: filepath.Base(pb), Width: 380},
				},
				StyleCell: func(st *walk.CellStyle) {
					if st.Row() >= len(model.show) {
						return
					}
					r := model.res.Rows[model.show[st.Row()]]
					kind := r.Kind
					if (kind == '-' && st.Col() >= 2) || (kind == '+' && st.Col() < 2) {
						kind = 'x' // az üres oldal halványabb
					}
					if c, ok := diffColors(kind); ok {
						st.BackgroundColor = c
						st.TextColor = dk.fg
						if !darkActive {
							st.TextColor = walk.Color(win.GetSysColor(win.COLOR_WINDOWTEXT))
						}
					}
					// a kurzorsor: a sorszám-oszlopok jelölik, a szöveg színe megmarad
					if st.Row() == tv.CurrentIndex() && (st.Col() == 0 || st.Col() == 2 || kind == '=' || kind == 'x') {
						st.BackgroundColor, st.TextColor = selColors(tv.Focused())
					}
				},
				OnKeyDown: func(k walk.Key) {
					switch {
					case k == walk.KeyF7 && walk.ModifiersDown()&walk.ModShift != 0:
						jump(tv, model, -1, tv.CurrentIndex())
					case k == walk.KeyF7:
						jump(tv, model, 1, tv.CurrentIndex())
					}
				}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				Label{AssignTo: &info, Text: ""},
				HSpacer{},
				PushButton{Text: T("Csere"), OnClicked: func() {
					pa, pb = pb, pa
					tv.Columns().At(1).SetTitle(filepath.Base(pa))
					tv.Columns().At(3).SetTitle(filepath.Base(pb))
					load()
				}},
				PushButton{AssignTo: &closeBtn, Text: T("Bezárás"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, appName, err.Error(), walk.MsgBoxIconError)
		return
	}
	if f, err := walk.NewFont("Consolas", 10, 0); err == nil {
		tv.SetFont(f)
	}
	themeDialog(dlg)
	a.sizeableDialog(dlg, a.mw, "diff", 1100, 700)
	dlg.Starting().Attach(func() {
		dlg.Synchronize(func() {
			w := (tv.ClientBoundsPixels().Width - tv.Columns().At(0).Width()*2 - 30) / 2
			if w > 150 {
				tv.Columns().At(1).SetWidth(w)
				tv.Columns().At(3).SetWidth(w)
			}
			load()
		})
	})
	dlg.Run()
}

// jump a következő/előző eltérő szakasz elejére ugrik.
func jump(tv *walk.TableView, m *diffModel, dir, from int) {
	if m.res == nil {
		return
	}
	kinds := make([]byte, len(m.show))
	for i, k := range m.show {
		kinds[i] = m.res.Rows[k].Kind
	}
	if i := diffJump(kinds, dir, from); i >= 0 {
		tv.SetCurrentIndex(i)
		tv.EnsureItemVisible(i)
	}
}

// ── ellenőrzőösszeg ──

func (a *App) makeChecksums() {
	p := a.active
	if !a.needLocal(p) {
		return
	}
	items := p.selectedEntries()
	var paths []string
	for _, e := range items {
		if !e.IsParent {
			paths = append(paths, e.Path)
		}
	}
	if len(paths) == 0 {
		return
	}
	defName := func(algo string) string {
		if len(paths) == 1 {
			return filepath.Base(paths[0]) + hashExt(algo)
		}
		return filepath.Base(p.path) + hashExt(algo)
	}
	var dlg *walk.Dialog
	var algoCB *walk.ComboBox
	var sepCB *walk.CheckBox
	var nameLE *walk.LineEdit
	var out *walk.TextEdit
	var saveBtn, copyBtn, closeBtn *walk.PushButton
	var lines []sumLine
	algo, _ := a.settings.Get("checksumAlgo")
	ai := 2
	for i, x := range hashAlgos {
		if x == algo {
			ai = i
		}
	}
	compute := func() {
		algo := hashAlgos[algoCB.CurrentIndex()]
		var res []sumLine
		if err := runJob(dlg, T("Ellenőrzőösszeg számítása"), func(j *Job) error {
			var err error
			res, err = hashFiles(p.path, paths, algo, j)
			return err
		}); err != nil {
			walk.MsgBox(dlg, appName, errText(err), walk.MsgBoxIconWarning)
			return
		}
		lines = res
		var sb strings.Builder
		for _, l := range res {
			sb.WriteString(l.Hash + "  " + l.Name + "\r\n")
		}
		out.SetText(sb.String())
		saveBtn.SetEnabled(len(res) > 0)
		copyBtn.SetEnabled(len(res) > 0)
	}
	err := Dialog{
		AssignTo: &dlg, Title: T("Ellenőrzőösszeg készítése"), CancelButton: &closeBtn,
		MinSize: Size{Width: 760, Height: 420}, Layout: VBox{},
		Children: []Widget{
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				Label{Text: T("Algoritmus:")},
				ComboBox{AssignTo: &algoCB, Model: hashAlgos, CurrentIndex: ai, OnCurrentIndexChanged: func() {
					if nameLE != nil {
						nameLE.SetText(defName(hashAlgos[algoCB.CurrentIndex()]))
						a.settings.Put("checksumAlgo", hashAlgos[algoCB.CurrentIndex()])
						if lines != nil {
							compute()
						}
					}
				}},
				HSpacer{},
				Label{Text: describe(items)},
			}},
			TextEdit{AssignTo: &out, ReadOnly: true, VScroll: true, HScroll: true},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				Label{Text: T("Fájlnév:")},
				LineEdit{AssignTo: &nameLE, Text: defName(hashAlgos[ai])},
				CheckBox{AssignTo: &sepCB, Text: T("Fájlonként külön"), OnCheckedChanged: func() { nameLE.SetEnabled(!sepCB.Checked()) }},
				PushButton{AssignTo: &saveBtn, Text: T("Mentés a mappába"), Enabled: false, OnClicked: func() {
					algo := hashAlgos[algoCB.CurrentIndex()]
					var err error
					n := 0
					if sepCB.Checked() {
						for _, l := range lines {
							target := filepath.Join(p.path, l.Name+hashExt(algo))
							one := []sumLine{{Hash: l.Hash, Name: filepath.Base(l.Name)}}
							if err = os.WriteFile(target, []byte(formatSums(one)), 0o644); err != nil {
								break
							}
							n++
						}
					} else {
						name := strings.TrimSpace(nameLE.Text())
						if name == "" {
							return
						}
						target := filepath.Join(p.path, name)
						if _, e := os.Stat(target); e == nil &&
							walk.MsgBox(dlg, appName, fmt.Sprintf(T("%s már létezik. Felülírod?"), name), walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != win.IDYES {
							return
						}
						err = os.WriteFile(target, []byte(formatSums(lines)), 0o644)
						n = 1
					}
					if err != nil {
						walk.MsgBox(dlg, appName, fmt.Sprintf(T("A mentés nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
						return
					}
					p.refresh()
					dlg.Accept()
				}},
				PushButton{AssignTo: &copyBtn, Text: T("Vágólapra"), Enabled: false, OnClicked: func() {
					walk.Clipboard().SetText(out.Text())
				}},
				PushButton{AssignTo: &closeBtn, Text: T("Bezárás"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, appName, err.Error(), walk.MsgBoxIconError)
		return
	}
	if f, err := walk.NewFont("Consolas", 10, 0); err == nil {
		out.SetFont(f)
	}
	themeDialog(dlg)
	a.sizeableDialog(dlg, a.mw, "checksum", 820, 460)
	dlg.Starting().Attach(func() { dlg.Synchronize(compute) })
	dlg.Run()
}

type sumModel struct {
	walk.TableModelBase
	rows []sumCheck
}

func (m *sumModel) RowCount() int { return len(m.rows) }
func (m *sumModel) Value(row, col int) interface{} {
	r := m.rows[row]
	if col == 0 {
		return r.Name
	}
	s := sumStatusText(r.Status)
	if r.Detail != "" {
		s += fmt.Sprintf(T(" – %s"), r.Detail)
	}
	return s
}

func (a *App) verifyChecksums() {
	p := a.active
	if !a.needLocal(p) {
		return
	}
	e := p.currentEntry()
	if e == nil || e.IsDir || !isSumFile(e.Name) {
		a.info(T("Állj a kurzorral egy ellenőrzőösszeg-fájlra (.md5, .sha1, .sha256, .sha512)."))
		return
	}
	var res []sumCheck
	var algo string
	if err := runJob(a.mw, T("Ellenőrzés"), func(j *Job) error {
		var err error
		res, algo, err = verifySums(e.Path, j)
		return err
	}); err != nil {
		walk.MsgBox(a.mw, appName, errText(err), walk.MsgBoxIconWarning)
		return
	}
	cnt := [4]int{}
	for _, r := range res {
		cnt[r.Status]++
	}
	sum := fmt.Sprintf(Tn(cnt[0], "%s: %d fájl rendben"), algo, cnt[0])
	if cnt[1] > 0 {
		sum += fmt.Sprintf(Tn(cnt[1], ", %d HIBÁS"), cnt[1])
	}
	if cnt[2] > 0 {
		sum += fmt.Sprintf(Tn(cnt[2], ", %d hiányzik"), cnt[2])
	}
	if cnt[3] > 0 {
		sum += fmt.Sprintf(Tn(cnt[3], ", %d nem olvasható"), cnt[3])
	}
	model := &sumModel{rows: res}
	var dlg *walk.Dialog
	var closeBtn *walk.PushButton
	err := Dialog{
		AssignTo: &dlg, Title: fmt.Sprintf(T("Ellenőrzés – %s"), e.Name), CancelButton: &closeBtn, DefaultButton: &closeBtn,
		MinSize: Size{Width: 640, Height: 400}, Layout: VBox{},
		Children: []Widget{
			Label{Text: sum + "."},
			TableView{LastColumnStretched: true, Model: model, Columns: []TableViewColumn{{Title: T("Fájl"), Width: 380}, {Title: T("Eredmény"), Width: 200}},
				StyleCell: func(st *walk.CellStyle) {
					if st.Col() == 1 && st.Row() < len(res) {
						switch res[st.Row()].Status {
						case 0:
							st.TextColor = walk.RGB(30, 150, 60)
						default:
							st.TextColor = walk.RGB(220, 50, 50)
						}
					}
				}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &closeBtn, Text: T("Bezárás"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		return
	}
	themeDialog(dlg)
	a.sizeableDialog(dlg, a.mw, "verify", 700, 440)
	dlg.Run()
}

// ── ismétlődő fájlok ──

type dupeRow struct {
	group int
	f     dupeFile
	size  int64
}

type dupeModel struct {
	walk.TableModelBase
	rows []dupeRow
}

func (m *dupeModel) RowCount() int { return len(m.rows) }
func (m *dupeModel) Value(row, col int) interface{} {
	r := m.rows[row]
	switch col {
	case 0:
		return strconv.Itoa(r.group + 1)
	case 1:
		return filepath.Base(r.f.Path)
	case 2:
		return filepath.Dir(r.f.Path)
	case 3:
		return fmtSize(r.size)
	case 4:
		return r.f.Mod.Format("2006.01.02 15:04")
	}
	return ""
}

var dupeMinSizes = []struct {
	Label string
	Size  int64
}{{T("bármekkora"), 0}, {T("legalább 1 KB"), 1 << 10}, {T("legalább 100 KB"), 100 << 10}, {T("legalább 1 MB"), 1 << 20}, {T("legalább 10 MB"), 10 << 20}, {T("legalább 100 MB"), 100 << 20}}

func (a *App) findDuplicates() {
	p := a.active
	if !a.needLocal(p) {
		return
	}
	model := &dupeModel{}
	var groups []dupeGroup
	var dlg *walk.Dialog
	var rootLE *walk.LineEdit
	var contCB, nameCB *walk.CheckBox
	var minCB *walk.ComboBox
	var tv *walk.TableView
	var info *walk.Label
	var closeBtn *walk.PushButton
	minLabels := make([]string, len(dupeMinSizes))
	for i, m := range dupeMinSizes {
		minLabels[i] = m.Label
	}
	fill := func() {
		model.rows = model.rows[:0]
		for gi, g := range groups {
			for _, f := range g.Files {
				model.rows = append(model.rows, dupeRow{group: gi, f: f, size: g.Size})
			}
		}
		model.PublishRowsReset()
		n, b := dupeWaste(groups)
		if len(groups) == 0 {
			info.SetText(T("Nincs ismétlődő fájl."))
		} else {
			info.SetText(fmt.Sprintf(T("%s; a fölösleges példányok: %s, %s."), Nf(len(groups), "%d csoport"), Nf(n, "%d fájl"), fmtHuman(uint64(b))))
		}
	}
	search := func() {
		var roots []string
		for _, r := range strings.Split(rootLE.Text(), ";") {
			if r = strings.TrimSpace(r); r != "" {
				roots = append(roots, r)
			}
		}
		for _, r := range roots {
			if st, err := os.Stat(r); err != nil || !st.IsDir() {
				walk.MsgBox(dlg, appName, fmt.Sprintf(T("Nem létező mappa:\n%s"), r), walk.MsgBoxIconWarning)
				return
			}
		}
		o := dupeOpts{MinSize: dupeMinSizes[minCB.CurrentIndex()].Size, Content: contCB.Checked(), SameName: nameCB.Checked()}
		var res []dupeGroup
		if err := runJob(dlg, T("Ismétlődő fájlok keresése"), func(j *Job) error {
			var err error
			res, err = findDupes(roots, o, j)
			return err
		}); err != nil {
			walk.MsgBox(dlg, appName, errText(err), walk.MsgBoxIconWarning)
			return
		}
		groups = res
		fill()
	}
	selected := func() []int {
		return tv.SelectedIndexes()
	}
	roots := p.path
	err := Dialog{
		AssignTo: &dlg, Title: T("Ismétlődő fájlok keresése"), CancelButton: &closeBtn,
		MinSize: Size{Width: 900, Height: 560}, Layout: VBox{},
		Children: []Widget{
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				Label{Text: T("Hol (több mappa ;-vel):")},
				LineEdit{AssignTo: &rootLE, Text: roots},
				PushButton{Text: T("+ másik panel"), OnClicked: func() {
					if o := a.other(); o.vfs == nil {
						rootLE.SetText(strings.TrimSpace(rootLE.Text()) + ";" + o.path)
					}
				}},
			}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				CheckBox{AssignTo: &contCB, Text: T("Tartalom szerint (biztos)"), Checked: true},
				CheckBox{AssignTo: &nameCB, Text: T("A névnek is egyeznie kell")},
				Label{Text: T("Méret:")},
				ComboBox{AssignTo: &minCB, Model: minLabels, CurrentIndex: 1},
				HSpacer{},
				PushButton{Text: T("Keresés"), OnClicked: search},
			}},
			TableView{LastColumnStretched: true, AssignTo: &tv, Model: model, MultiSelection: true,
				Columns: []TableViewColumn{
					{Title: T("Csop."), Width: 50, Alignment: AlignFar}, {Title: T("Név"), Width: 220}, {Title: T("Mappa"), Width: 360},
					{Title: T("Méret"), Width: 100, Alignment: AlignFar}, {Title: T("Dátum"), Width: 120},
				},
				StyleCell: func(st *walk.CellStyle) {
					if st.Row() < len(model.rows) && model.rows[st.Row()].group%2 == 1 {
						if darkActive {
							st.BackgroundColor = walk.RGB(55, 55, 62)
							st.TextColor = dk.fg
						} else {
							st.BackgroundColor = walk.RGB(238, 242, 250)
						}
					}
				},
				OnItemActivated: func() {
					i := tv.CurrentIndex()
					if i < 0 || i >= len(model.rows) {
						return
					}
					f := model.rows[i].f.Path
					dlg.Cancel()
					p.navigate(filepath.Dir(f), filepath.Base(f), true, false)
				}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				Label{AssignTo: &info, Text: T("Add meg a mappát, és nyomd meg a Keresés gombot.")},
				HSpacer{},
				PushButton{Text: T("Csoportonként egy kivételével"), OnClicked: func() {
					var sel []int
					for i := range model.rows {
						if i > 0 && model.rows[i-1].group == model.rows[i].group {
							sel = append(sel, i)
						}
					}
					tv.SetSelectedIndexes(sel)
					tv.SetFocus()
				}},
				PushButton{Text: T("Lomtárba a kijelölteket"), OnClicked: func() {
					sel := selected()
					if len(sel) == 0 {
						return
					}
					// biztonsági ellenőrzés: egyetlen csoportból se törlődjön minden példány
					left := map[int]int{}
					for _, r := range model.rows {
						left[r.group]++
					}
					for _, i := range sel {
						left[model.rows[i].group]--
					}
					for _, n := range left {
						if n == 0 {
							walk.MsgBox(dlg, appName, T("Van olyan csoport, amelyből minden példány ki van jelölve.\nLegalább egynek meg kell maradnia."), walk.MsgBoxIconWarning)
							return
						}
					}
					if walk.MsgBox(dlg, T("Törlés"), fmt.Sprintf(Tn(len(sel), "%d fájl áthelyezése a Lomtárba?"), len(sel)), walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != win.IDYES {
						return
					}
					paths := make([]string, len(sel))
					for k, i := range sel {
						paths[k] = model.rows[i].f.Path
					}
					a.localFileOp(foDelete, paths, "", uint32(fofNoConfirmation|fofxShowElevationPrompt|fofAllowUndo|fofWantNukeWarning|fofxRecycleOnDelete|fofxAddUndoRecord))
					// a törölteket kivesszük a listából
					gone := map[string]bool{}
					for _, pth := range paths {
						if _, err := os.Stat(pth); err != nil {
							gone[pth] = true
						}
					}
					var ng []dupeGroup
					for _, g := range groups {
						var fs []dupeFile
						for _, f := range g.Files {
							if !gone[f.Path] {
								fs = append(fs, f)
							}
						}
						if len(fs) > 1 {
							ng = append(ng, dupeGroup{Size: g.Size, Files: fs})
						}
					}
					groups = ng
					fill()
					a.refreshBoth()
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
	a.sizeableDialog(dlg, a.mw, "dupes", 1000, 640)
	dlg.Run()
}

// ── darabolás és összefűzés ──

func (a *App) splitCurrent() {
	p := a.active
	if !a.needLocal(p) {
		return
	}
	e := p.currentEntry()
	if e == nil || e.IsDir || e.IsParent {
		a.info(T("Állj a kurzorral a darabolandó fájlra."))
		return
	}
	target := p.path
	if o := a.other(); o.vfs == nil {
		target = o.path
	}
	labels := make([]string, len(splitPresets))
	for i, s := range splitPresets {
		labels[i] = s.Label
	}
	var dlg *walk.Dialog
	var sizeCB *walk.ComboBox
	var destLE *walk.LineEdit
	var okBtn, cancelBtn *walk.PushButton
	var part int64
	err := Dialog{
		AssignTo: &dlg, Title: T("Fájl darabolása"), DefaultButton: &okBtn, CancelButton: &cancelBtn,
		MinSize: Size{Width: 520}, Layout: Grid{Columns: 2},
		Children: []Widget{
			Label{Text: T("Fájl:")},
			Label{Text: fmt.Sprintf(T("%s  (%s bájt)"), e.Name, fmtSize(e.Size)), EllipsisMode: EllipsisEnd},
			Label{Text: T("Darabméret:")},
			ComboBox{AssignTo: &sizeCB, Editable: true, Model: labels, Value: splitDefault(a)},
			Label{Text: T("Cél mappa:")},
			LineEdit{AssignTo: &destLE, Text: target},
			Label{ColumnSpan: 2, Text: T("Saját méret is megadható, pl. 250 MB, 1,5 GB, 800k. A darabok mellé .crc fájl is készül,\naz összefűzés ezzel ellenőrzi az eredményt.")},
			Composite{ColumnSpan: 2, Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &okBtn, Text: T("Darabolás"), OnClicked: func() {
					txt := sizeCB.Text()
					for _, s := range splitPresets {
						if s.Label == txt {
							part = s.Size
						}
					}
					if part == 0 {
						v, err := parseSize(txt)
						if err != nil {
							walk.MsgBox(dlg, appName, errText(err), walk.MsgBoxIconWarning)
							return
						}
						part = v
					}
					a.settings.Put("splitSize", txt)
					dlg.Accept()
				}},
				PushButton{AssignTo: &cancelBtn, Text: T("Mégse"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		return
	}
	themeDialog(dlg)
	if dlg.Run() != walk.DlgCmdOK {
		return
	}
	dest := strings.TrimSpace(destLE.Text())
	if err := os.MkdirAll(dest, 0o755); err != nil {
		walk.MsgBox(a.mw, appName, errText(err), walk.MsgBoxIconWarning)
		return
	}
	var n int
	err = runJob(a.mw, T("Darabolás"), func(j *Job) error {
		var err error
		n, err = splitFile(e.Path, dest, part, j)
		return err
	})
	a.refreshBoth()
	if err != nil {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A darabolás nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return
	}
	if n > 0 {
		a.info(fmt.Sprintf(Tn(n, "Kész: %d darab és egy .crc fájl a(z) %s mappában."), n, dest))
	}
}

func (a *App) joinCurrent() {
	p := a.active
	if !a.needLocal(p) {
		return
	}
	e := p.currentEntry()
	if e == nil || e.IsDir || !isPartName(e.Name) {
		a.info(T("Állj a kurzorral a darabolt fájl egyik darabjára (pl. valami.zip.001)."))
		return
	}
	target := p.path
	if o := a.other(); o.vfs == nil {
		target = o.path
	}
	dest, ok, _ := inputBoxEx(a.mw, T("Fájlok összefűzése"), fmt.Sprintf(T("A(z) „%s” darabjainak összefűzése ide:"), strings.TrimSuffix(e.Name, filepath.Ext(e.Name))), target, false, false)
	if !ok {
		return
	}
	dest = strings.TrimSpace(dest)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		walk.MsgBox(a.mw, appName, errText(err), walk.MsgBoxIconWarning)
		return
	}
	var out, note string
	err := runJob(a.mw, T("Összefűzés"), func(j *Job) error {
		var err error
		out, note, err = joinFiles(e.Path, dest, j)
		return err
	})
	a.refreshBoth()
	if err != nil {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("Az összefűzés nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return
	}
	if out != "" {
		a.info(note)
	}
}

// ── attribútumok és dátum ──

const (
	attrReadOnly = 0x1
	attrHidden   = 0x2
	attrSystem   = 0x4
	attrArchive  = 0x20
)

func winSetAttrs(p string, isDir bool, c bitChange) error {
	ptr, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return err
	}
	old, err := syscall.GetFileAttributes(ptr)
	if err != nil {
		return err
	}
	nv := c.apply(old) & (attrReadOnly | attrHidden | attrSystem | attrArchive)
	return syscall.SetFileAttributes(ptr, nv)
}

func (a *App) changeAttrs() {
	p := a.active
	if !a.needLocal(p) {
		return
	}
	var items []*Entry
	for _, e := range p.selectedEntries() {
		if !e.IsParent {
			items = append(items, e)
		}
	}
	if len(items) == 0 {
		return
	}
	hasDir := false
	for _, e := range items {
		hasDir = hasDir || e.IsDir
	}
	// kiinduló állapot: ha minden elemnél azonos, az; különben határozatlan
	state := func(bit uint32) walk.CheckState {
		on, off := 0, 0
		for _, e := range items {
			ptr, _ := syscall.UTF16PtrFromString(e.Path)
			v, err := syscall.GetFileAttributes(ptr)
			if err != nil {
				continue
			}
			if v&bit != 0 {
				on++
			} else {
				off++
			}
		}
		switch {
		case on > 0 && off == 0:
			return walk.CheckChecked
		case off > 0 && on == 0:
			return walk.CheckUnchecked
		}
		return walk.CheckIndeterminate
	}
	type ab struct {
		bit  uint32
		text string
		cb   *walk.CheckBox
	}
	attrs := []*ab{{bit: attrReadOnly, text: T("Csak olvasható")}, {bit: attrHidden, text: T("Rejtett")}, {bit: attrSystem, text: T("Rendszer")}, {bit: attrArchive, text: T("Archiválandó")}}
	var dlg *walk.Dialog
	var timeCB, recCB *walk.CheckBox
	var timeLE *walk.LineEdit
	var okBtn, cancelBtn *walk.PushButton
	var children []Widget
	for _, x := range attrs {
		x := x
		children = append(children, CheckBox{AssignTo: &x.cb, Text: x.text, Tristate: true, CheckState: state(x.bit)})
	}
	box := []Widget{
		GroupBox{Title: T("Attribútumok (a szürke pipa: nem változik)"), Layout: VBox{}, Children: children},
		GroupBox{Title: T("Módosítás dátuma"), Layout: HBox{}, Children: []Widget{
			CheckBox{AssignTo: &timeCB, Text: T("Beállítás erre:"), OnCheckedChanged: func() { timeLE.SetEnabled(timeCB.Checked()) }},
			LineEdit{AssignTo: &timeLE, Text: items[0].ModTime.Format("2006.01.02 15:04:05"), Enabled: false},
			PushButton{Text: T("Most"), OnClicked: func() {
				timeCB.SetChecked(true)
				timeLE.SetText(time.Now().Format("2006.01.02 15:04:05"))
			}},
		}},
		CheckBox{AssignTo: &recCB, Text: T("Mappák tartalmára is (almappákkal együtt)"), Visible: hasDir},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			Label{Text: describe(items)},
			HSpacer{},
			PushButton{AssignTo: &okBtn, Text: "OK", OnClicked: func() { dlg.Accept() }},
			PushButton{AssignTo: &cancelBtn, Text: T("Mégse"), OnClicked: func() { dlg.Cancel() }},
		}},
	}
	err := Dialog{
		AssignTo: &dlg, Title: T("Attribútumok módosítása"), DefaultButton: &okBtn, CancelButton: &cancelBtn,
		MinSize: Size{Width: 440}, Layout: VBox{}, Children: box,
	}.Create(a.mw)
	if err != nil {
		return
	}
	themeDialog(dlg)
	if dlg.Run() != walk.DlgCmdOK {
		return
	}
	req := attrRequest{Recurse: hasDir && recCB.Checked()}
	for _, x := range attrs {
		st := x.cb.CheckState()
		switch st {
		case walk.CheckChecked:
			req.Bits.Set |= x.bit
		case walk.CheckUnchecked:
			req.Bits.Clear |= x.bit
		}
	}
	if timeCB.Checked() {
		t, err := parseDateTime(timeLE.Text())
		if err != nil {
			walk.MsgBox(a.mw, appName, errText(err), walk.MsgBoxIconWarning)
			return
		}
		req.SetTime, req.Time = true, t
	}
	if req.Bits.empty() && !req.SetTime {
		return
	}
	paths := entryPaths(items)
	runJob(a.mw, T("Attribútumok módosítása"), func(j *Job) error {
		_, err := applyAttrs(paths, req, winSetAttrs, j)
		return err
	})
	a.refreshBoth()
}

func splitDefault(a *App) string {
	if v, ok := a.settings.Get("splitSize"); ok && v != "" {
		return v
	}
	return "100 MB"
}
