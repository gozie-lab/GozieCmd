//go:build linux

package main

// Fájleszközök (Linux-felület): tartalmi összehasonlítás, ellenőrzőösszeg,
// ismétlődő fájlok, darabolás/összefűzés, jogok és dátum.

import (
	"fmt"
	"image/color"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ── általános táblázatsor: oszlopok, két háttérfél, kijelölés ──

// toolRow oszlopai: w > 0 rögzített szélesség, w < 0 arány (a maradékból).
type toolRow struct {
	widget.BaseWidget
	widths   []float32
	cells    []*canvas.Text
	bgL, bgR *canvas.Rectangle
	split    int // ettől az oszloptól a jobb háttér
	sel      *canvas.Rectangle
	onTap    func()
	onDouble func()
}

func newToolRow(widths []float32, split int, mono bool) *toolRow {
	r := &toolRow{widths: widths, split: split,
		bgL: canvas.NewRectangle(color.Transparent), bgR: canvas.NewRectangle(color.Transparent)}
	r.sel = canvas.NewRectangle(color.Transparent)
	r.sel.StrokeWidth = 1.5
	for range widths {
		t := canvas.NewText("", theme.Color(theme.ColorNameForeground))
		t.TextSize = theme.TextSize()
		t.TextStyle.Monospace = mono
		r.cells = append(r.cells, t)
	}
	r.ExtendBaseWidget(r)
	return r
}

func (r *toolRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}

func (r *toolRow) DoubleTapped(*fyne.PointEvent) {
	if r.onDouble != nil {
		r.onDouble()
	}
}

func (r *toolRow) CreateRenderer() fyne.WidgetRenderer { return &toolRowRenderer{r} }

type toolRowRenderer struct{ r *toolRow }

func (s *toolRowRenderer) Destroy() {}
func (s *toolRowRenderer) Objects() []fyne.CanvasObject {
	o := []fyne.CanvasObject{s.r.bgL, s.r.bgR}
	for _, c := range s.r.cells {
		o = append(o, c)
	}
	return append(o, s.r.sel)
}
func (s *toolRowRenderer) MinSize() fyne.Size {
	return fyne.NewSize(200, theme.TextSize()*1.25+6)
}
func (s *toolRowRenderer) Layout(sz fyne.Size) {
	r := s.r
	var fixed, weight float32
	for _, w := range r.widths {
		if w > 0 {
			fixed += w
		} else {
			weight -= w
		}
	}
	rest := sz.Width - fixed
	if rest < 0 {
		rest = 0
	}
	x := float32(0)
	splitX := sz.Width
	for i, c := range r.cells {
		w := r.widths[i]
		if w < 0 {
			w = rest * -w / weight
		}
		if i == r.split {
			splitX = x
		}
		h := fyne.MeasureText("Ág", c.TextSize, c.TextStyle).Height
		c.Move(fyne.NewPos(x+5, (sz.Height-h)/2))
		c.Resize(fyne.NewSize(w-10, h))
		x += w
	}
	r.bgL.Move(fyne.NewPos(0, 0))
	r.bgL.Resize(fyne.NewSize(splitX, sz.Height))
	r.bgR.Move(fyne.NewPos(splitX, 0))
	r.bgR.Resize(fyne.NewSize(sz.Width-splitX, sz.Height))
	r.sel.Resize(sz)
}
func (s *toolRowRenderer) Refresh() {
	s.Layout(s.r.Size())
	canvas.Refresh(s.r)
}

// clipText: canvas.Text nem vág, ezért a túl hosszú szöveget mi rövidítjük.
func clipText(t *canvas.Text, s string, w float32) {
	s = strings.ReplaceAll(s, "\t", "    ")
	if w <= 0 || fyne.MeasureText(s, t.TextSize, t.TextStyle).Width <= w {
		t.Text = s
		return
	}
	r := []rune(s)
	lo, hi := 0, len(r)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fyne.MeasureText(fmt.Sprintf(T("%s…"), string(r[:mid])), t.TextSize, t.TextStyle).Width <= w {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	t.Text = fmt.Sprintf(T("%s…"), string(r[:lo]))
}

func (r *toolRow) setTexts(vals ...string) {
	var fixed, weight float32
	for _, w := range r.widths {
		if w > 0 {
			fixed += w
		} else {
			weight -= w
		}
	}
	width := r.Size().Width
	for i, c := range r.cells {
		w := r.widths[i]
		if w < 0 && weight > 0 {
			w = (width - fixed) * -w / weight
		}
		v := ""
		if i < len(vals) {
			v = vals[i]
		}
		clipText(c, v, w-10)
		c.Color = theme.Color(theme.ColorNameForeground)
	}
}

func (r *toolRow) setSelected(on bool) {
	if on {
		r.sel.StrokeColor = theme.Color(theme.ColorNamePrimary)
	} else {
		r.sel.StrokeColor = color.Transparent
	}
}

func (a *App) toolWindow(title, key string, w0, h0 int) fyne.Window {
	w := a.fa.NewWindow(title)
	w.SetIcon(appIconRes)
	a.restoreWinSize(w, key, w0, h0)
	w.SetOnClosed(func() { a.saveWinSize(w, key) })
	return w
}

func winInfo(w fyne.Window, msg string) {
	dialog.ShowInformation(appName, msg, w)
}

func (a *App) needLocal(p *Panel) bool {
	if p.vfs != nil {
		a.warn(T("Ez a művelet csak helyi fájlokkal működik (archívumon és FTP-n belül nem)."))
		return false
	}
	return true
}

func shiftDown() bool {
	if d, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok {
		return d.CurrentKeyModifiers()&fyne.KeyModifierShift != 0
	}
	return false
}

// ── tartalmi összehasonlítás ──

func (a *App) compareContent() {
	p, o := a.active, a.other()
	if !a.needLocal(p) || !a.needLocal(o) {
		return
	}
	pa, pb, msg := pickPair(p.selectedEntries(), p.marked, p.currentEntry(), o.path, o.currentEntry())
	if msg != "" {
		a.warn(msg)
		return
	}
	if p == a.right && filepath.Dir(pb) == a.left.path {
		pa, pb = pb, pa
	}
	a.diffWindow(pa, pb)
}

func diffBg(kind byte, dark bool) color.Color {
	if dark {
		switch kind {
		case 'c':
			return color.NRGBA{80, 68, 20, 255}
		case '-':
			return color.NRGBA{90, 35, 35, 255}
		case '+':
			return color.NRGBA{30, 75, 40, 255}
		}
	} else {
		switch kind {
		case 'c':
			return color.NRGBA{255, 243, 190, 255}
		case '-':
			return color.NRGBA{255, 215, 215, 255}
		case '+':
			return color.NRGBA{210, 245, 210, 255}
		}
	}
	return color.Transparent
}

func (a *App) diffWindow(pa, pb string) {
	w := a.toolWindow(T("Fájlok összehasonlítása"), "diff", 1100, 700)
	var res *diffResult
	var show []int
	cur := -1
	spaceC := widget.NewCheck(T("Szóközök figyelmen kívül"), nil)
	caseC := widget.NewCheck(T("Kis- és nagybetű egyezik"), nil)
	onlyC := widget.NewCheck(T("Csak az eltérések"), nil)
	info := widget.NewLabel("")
	info.Wrapping = fyne.TextWrapWord
	ea, eb := widget.NewEntry(), widget.NewEntry()
	var list *widget.List
	list = widget.NewList(func() int { return len(show) },
		func() fyne.CanvasObject { return newToolRow([]float32{56, -1, 56, -1}, 2, true) },
		func(id widget.ListItemID, o fyne.CanvasObject) {
			row := o.(*toolRow)
			r := res.Rows[show[id]]
			num := func(n int) string {
				if n == 0 || res.Binary {
					return ""
				}
				return strconv.Itoa(n)
			}
			row.setTexts(num(r.A), r.TA, num(r.B), r.TB)
			dark := a.isDark()
			l, rt := diffBg(r.Kind, dark), diffBg(r.Kind, dark)
			if r.Kind == '+' {
				l = color.Transparent
			}
			if r.Kind == '-' {
				rt = color.Transparent
			}
			row.bgL.FillColor, row.bgR.FillColor = l, rt
			row.setSelected(id == cur)
			row.onTap = func() {
				cur = id
				list.Refresh()
			}
			row.Refresh()
		})
	kinds := func() []byte {
		k := make([]byte, len(show))
		for i, x := range show {
			k[i] = res.Rows[x].Kind
		}
		return k
	}
	jumpTo := func(dir int) {
		if res == nil {
			return
		}
		if i := diffJump(kinds(), dir, cur); i >= 0 {
			cur = i
			list.ScrollTo(i)
			list.Refresh()
		}
	}
	rebuild := func() {
		show = show[:0]
		for i, r := range res.Rows {
			if onlyC.Checked && r.Kind == '=' {
				continue
			}
			show = append(show, i)
		}
		cur = -1
		list.Refresh()
		jumpTo(1)
	}
	load := func() {
		ea.SetText(pa)
		eb.SetText(pb)
		r, err := compareFiles(pa, pb, spaceC.Checked, caseC.Checked)
		if err != nil {
			winInfo(w, errText(err))
			return
		}
		res = r
		switch {
		case r.Same && r.Binary, r.Same:
			info.SetText(T("A két fájl tartalma azonos."))
		case r.Binary:
			info.SetText(fmt.Sprintf(T("Bináris fájlok. %s"), r.Note))
		default:
			info.SetText(fmt.Sprintf(Tn(r.Changes, "%d eltérő szakasz.  Sárga: módosult sor · piros: csak a bal oldalon · zöld: csak a jobb oldalon."), r.Changes))
		}
		rebuild()
	}
	spaceC.OnChanged = func(bool) { load() }
	caseC.OnChanged = func(bool) { load() }
	onlyC.OnChanged = func(bool) {
		if res != nil {
			rebuild()
		}
	}
	prevB := widget.NewButton(T("▲ Előző eltérés"), func() { jumpTo(-1) })
	nextB := widget.NewButton(T("▼ Következő eltérés (F7)"), func() { jumpTo(1) })
	swapB := widget.NewButton(T("Csere"), func() {
		pa, pb = pb, pa
		load()
	})
	top := container.NewVBox(
		container.NewGridWithColumns(2, ea, eb),
		container.NewHBox(spaceC, caseC, onlyC, layout.NewSpacer(), prevB, nextB),
	)
	bottom := container.NewBorder(nil, nil, nil, container.NewHBox(swapB, widget.NewButton(T("Bezárás"), w.Close)), info)
	w.SetContent(container.NewBorder(top, bottom, nil, nil, list))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		switch ev.Name {
		case fyne.KeyEscape:
			w.Close()
		case fyne.KeyF7:
			if shiftDown() {
				jumpTo(-1)
			} else {
				jumpTo(1)
			}
		}
	})
	w.Show()
	load()
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
	w := a.toolWindow(T("Ellenőrzőösszeg készítése"), "checksum", 820, 460)
	algoS := widget.NewSelect(hashAlgos, nil)
	out := widget.NewMultiLineEntry()
	out.TextStyle.Monospace = true
	out.Wrapping = fyne.TextWrapOff
	nameE := widget.NewEntry()
	sepC := widget.NewCheck(T("Fájlonként külön"), func(on bool) {
		if on {
			nameE.Disable()
		} else {
			nameE.Enable()
		}
	})
	var lines []sumLine
	var saveB, copyB *widget.Button
	compute := func() {
		algo := algoS.Selected
		var res []sumLine
		a.runJobIn(w, T("Ellenőrzőösszeg számítása"), func(j *job) error {
			var err error
			res, err = hashFiles(p.path, paths, algo, j)
			return err
		}, func(err error) {
			if err != nil {
				return
			}
			lines = res
			var sb strings.Builder
			for _, l := range res {
				sb.WriteString(l.Hash + "  " + l.Name + "\n")
			}
			out.SetText(sb.String())
			if len(res) > 0 {
				saveB.Enable()
				copyB.Enable()
			}
		})
	}
	saveB = widget.NewButton(T("Mentés a mappába"), func() {
		algo := algoS.Selected
		write := func() {
			var err error
			if sepC.Checked {
				for _, l := range lines {
					one := []sumLine{{Hash: l.Hash, Name: filepath.Base(l.Name)}}
					if err = os.WriteFile(filepath.Join(p.path, l.Name+hashExt(algo)), []byte(formatSums(one)), 0o644); err != nil {
						break
					}
				}
			} else {
				err = os.WriteFile(filepath.Join(p.path, strings.TrimSpace(nameE.Text)), []byte(formatSums(lines)), 0o644)
			}
			if err != nil {
				winInfo(w, fmt.Sprintf(T("A mentés nem sikerült:\n%s"), errText(err)))
				return
			}
			p.refresh()
			w.Close()
		}
		name := strings.TrimSpace(nameE.Text)
		if !sepC.Checked {
			if name == "" {
				return
			}
			if _, err := os.Stat(filepath.Join(p.path, name)); err == nil {
				dialog.ShowConfirm(appName, fmt.Sprintf(T("%s már létezik. Felülírod?"), name), func(ok bool) {
					if ok {
						write()
					}
				}, w)
				return
			}
		}
		write()
	})
	saveB.Importance = widget.HighImportance
	copyB = widget.NewButton(T("Vágólapra"), func() { w.Clipboard().SetContent(out.Text) })
	saveB.Disable()
	copyB.Disable()
	ai := 2
	if v, ok := a.settings.Get("checksumAlgo"); ok {
		for i, x := range hashAlgos {
			if x == v {
				ai = i
			}
		}
	}
	algoS.SetSelectedIndex(ai)
	nameE.SetText(defName(hashAlgos[ai]))
	algoS.OnChanged = func(s string) {
		nameE.SetText(defName(s))
		a.settings.Put("checksumAlgo", s)
		a.settings.Save()
		compute()
	}
	top := container.NewHBox(widget.NewLabel(T("Algoritmus:")), sized(algoS, 130), layout.NewSpacer(), widget.NewLabel(describe(items)))
	bottom := container.NewBorder(nil, nil, widget.NewLabel(T("Fájlnév:")),
		container.NewHBox(sepC, saveB, copyB, widget.NewButton(T("Bezárás"), w.Close)), nameE)
	w.SetContent(container.NewBorder(top, bottom, nil, nil, out))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyEscape {
			w.Close()
		}
	})
	w.Show()
	compute()
}

func (a *App) verifyChecksums() {
	p := a.active
	if !a.needLocal(p) {
		return
	}
	e := p.currentEntry()
	if e == nil || e.IsDir || !isSumFile(e.Name) {
		a.warn(T("Állj a kurzorral egy ellenőrzőösszeg-fájlra (.md5, .sha1, .sha256, .sha512)."))
		return
	}
	var res []sumCheck
	var algo string
	a.runJob(T("Ellenőrzés"), func(j *job) error {
		var err error
		res, algo, err = verifySums(e.Path, j)
		return err
	}, func(err error) {
		if err != nil {
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
		w := a.toolWindow(fmt.Sprintf(T("Ellenőrzés – %s"), e.Name), "verify", 700, 440)
		list := widget.NewList(func() int { return len(res) },
			func() fyne.CanvasObject { return newToolRow([]float32{-2, -1}, 99, false) },
			func(id widget.ListItemID, o fyne.CanvasObject) {
				row := o.(*toolRow)
				r := res[id]
				s := sumStatusText(r.Status)
				if r.Detail != "" {
					s += fmt.Sprintf(T(" – %s"), r.Detail)
				}
				row.setTexts(r.Name, s)
				if r.Status == 0 {
					row.cells[1].Color = color.NRGBA{40, 160, 70, 255}
				} else {
					row.cells[1].Color = color.NRGBA{220, 60, 60, 255}
				}
				row.Refresh()
			})
		w.SetContent(container.NewBorder(widget.NewLabel(sum+"."),
			container.NewHBox(layout.NewSpacer(), widget.NewButton(T("Bezárás"), w.Close)), nil, nil, list))
		w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
			if ev.Name == fyne.KeyEscape || ev.Name == fyne.KeyReturn {
				w.Close()
			}
		})
		w.Show()
	})
}

// ── ismétlődő fájlok ──

var dupeMinSizes = []struct {
	Label string
	Size  int64
}{{T("bármekkora"), 0}, {T("legalább 1 KB"), 1 << 10}, {T("legalább 100 KB"), 100 << 10}, {T("legalább 1 MB"), 1 << 20}, {T("legalább 10 MB"), 10 << 20}, {T("legalább 100 MB"), 100 << 20}}

func (a *App) findDuplicates() {
	p := a.active
	if !a.needLocal(p) {
		return
	}
	w := a.toolWindow(T("Ismétlődő fájlok keresése"), "dupes", 1000, 640)
	type drow struct {
		group int
		f     dupeFile
		size  int64
	}
	var groups []dupeGroup
	var rows []drow
	marked := map[int]bool{}
	rootE := widget.NewEntry()
	rootE.SetText(p.path)
	contC := widget.NewCheck(T("Tartalom szerint (biztos)"), nil)
	contC.SetChecked(true)
	nameC := widget.NewCheck(T("A névnek is egyeznie kell"), nil)
	labels := make([]string, len(dupeMinSizes))
	for i, m := range dupeMinSizes {
		labels[i] = m.Label
	}
	minS := widget.NewSelect(labels, nil)
	minS.SetSelectedIndex(1)
	info := widget.NewLabel(T("Add meg a mappát, és nyomd meg a Keresés gombot. Kattintás: kijelölés, dupla kattintás: ugrás a fájlhoz."))
	info.Wrapping = fyne.TextWrapWord
	var list *widget.List
	fill := func() {
		rows = rows[:0]
		marked = map[int]bool{}
		for gi, g := range groups {
			for _, f := range g.Files {
				rows = append(rows, drow{group: gi, f: f, size: g.Size})
			}
		}
		list.Refresh()
		n, b := dupeWaste(groups)
		if len(groups) == 0 {
			info.SetText(T("Nincs ismétlődő fájl."))
		} else {
			info.SetText(fmt.Sprintf(T("%s; a fölösleges példányok: %s, %s. Kattintás: kijelölés, dupla kattintás: ugrás a fájlhoz."), Nf(len(groups), "%d csoport"), Nf(n, "%d fájl"), fmtHuman(uint64(b))))
		}
	}
	list = widget.NewList(func() int { return len(rows) },
		func() fyne.CanvasObject {
			return newToolRow([]float32{34, 54, -2, -3, 110, 140}, 99, false)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			row := o.(*toolRow)
			r := rows[id]
			mark := "[ ]"
			if marked[id] {
				mark = "[x]"
			}
			row.setTexts(mark, strconv.Itoa(r.group+1), filepath.Base(r.f.Path), filepath.Dir(r.f.Path), fmtSize(r.size), r.f.Mod.Format("2006.01.02 15:04"))
			row.bgL.FillColor = color.Transparent
			if r.group%2 == 1 {
				if a.isDark() {
					row.bgL.FillColor = color.NRGBA{55, 55, 62, 255}
				} else {
					row.bgL.FillColor = color.NRGBA{238, 242, 250, 255}
				}
			}
			if marked[id] {
				for _, c := range row.cells {
					c.Color = color.NRGBA{220, 60, 60, 255}
				}
			}
			row.onTap = func() {
				marked[id] = !marked[id]
				list.RefreshItem(id)
			}
			row.onDouble = func() {
				w.Close()
				p.navigate(filepath.Dir(r.f.Path), filepath.Base(r.f.Path), true)
			}
			row.Refresh()
		})
	search := func() {
		var roots []string
		for _, r := range strings.Split(rootE.Text, ";") {
			if r = strings.TrimSpace(r); r != "" {
				roots = append(roots, r)
			}
		}
		for _, r := range roots {
			if st, err := os.Stat(r); err != nil || !st.IsDir() {
				winInfo(w, fmt.Sprintf(T("Nem létező mappa:\n%s"), r))
				return
			}
		}
		o := dupeOpts{MinSize: dupeMinSizes[minS.SelectedIndex()].Size, Content: contC.Checked, SameName: nameC.Checked}
		var res []dupeGroup
		a.runJobIn(w, T("Ismétlődő fájlok keresése"), func(j *job) error {
			var err error
			res, err = findDupes(roots, o, j)
			return err
		}, func(err error) {
			if err == nil {
				groups = res
				fill()
			}
		})
	}
	allButOne := widget.NewButton(T("Csoportonként egy kivételével"), func() {
		marked = map[int]bool{}
		for i := range rows {
			if i > 0 && rows[i-1].group == rows[i].group {
				marked[i] = true
			}
		}
		list.Refresh()
	})
	trashB := widget.NewButton(T("Kukába a kijelölteket"), func() {
		var sel []int
		for i := range rows {
			if marked[i] {
				sel = append(sel, i)
			}
		}
		if len(sel) == 0 {
			return
		}
		left := map[int]int{}
		for _, r := range rows {
			left[r.group]++
		}
		for _, i := range sel {
			left[rows[i].group]--
		}
		for _, n := range left {
			if n == 0 {
				winInfo(w, T("Van olyan csoport, amelyből minden példány ki van jelölve.\nLegalább egynek meg kell maradnia."))
				return
			}
		}
		dialog.ShowConfirm(T("Törlés"), fmt.Sprintf(Tn(len(sel), "%d fájl a Kukába kerül. Folytatod?"), len(sel)), func(ok bool) {
			if !ok {
				return
			}
			gone := map[string]bool{}
			a.runJobIn(w, T("Törlés"), func(j *job) error {
				j.SetTotal(int64(len(sel)))
				for _, i := range sel {
					f := rows[i].f.Path
					j.Status(f)
					if err := trashPath(f); err != nil {
						j.Err(f, err)
					} else {
						gone[f] = true
					}
					j.Add(1)
				}
				return nil
			}, func(error) {
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
				a.left.refresh()
				a.right.refresh()
			})
		}, w)
	})
	addOther := widget.NewButton(T("+ másik panel"), func() {
		if o := a.other(); o.vfs == nil {
			rootE.SetText(strings.TrimSpace(rootE.Text) + ";" + o.path)
		}
	})
	searchB := widget.NewButton(T("Keresés"), search)
	searchB.Importance = widget.HighImportance
	head := newToolRow([]float32{34, 54, -2, -3, 110, 140}, 99, false)
	head.setTexts("", T("Csop."), T("Név"), T("Mappa"), T("Méret"), T("Dátum"))
	for _, c := range head.cells {
		c.TextStyle.Bold = true
	}
	top := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel(T("Hol (több mappa ;-vel):")), addOther, rootE),
		container.NewHBox(contC, nameC, widget.NewLabel(T("Méret:")), sized(minS, 170), layout.NewSpacer(), searchB),
		head,
	)
	bottom := container.NewBorder(nil, nil, nil,
		container.NewHBox(allButOne, trashB, widget.NewButton(T("Bezárás"), w.Close)), info)
	w.SetContent(container.NewBorder(top, bottom, nil, nil, list))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyEscape {
			w.Close()
		}
	})
	w.Show()
}

// ── darabolás és összefűzés ──

func (a *App) splitCurrent() {
	p := a.active
	if !a.needLocal(p) {
		return
	}
	e := p.currentEntry()
	if e == nil || e.IsDir || e.IsParent {
		a.warn(T("Állj a kurzorral a darabolandó fájlra."))
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
	sizeE := widget.NewSelectEntry(labels)
	def := "100 MB"
	if v, ok := a.settings.Get("splitSize"); ok && v != "" {
		def = v
	}
	sizeE.SetText(def)
	destE := widget.NewEntry()
	destE.SetText(target)
	hint := widget.NewLabel(T("Saját méret is megadható, pl. 250 MB, 1,5 GB, 800k. A darabok mellé .crc fájl is készül, az összefűzés ezzel ellenőrzi az eredményt."))
	hint.Wrapping = fyne.TextWrapWord
	form := container.NewVBox(
		widget.NewLabel(fmt.Sprintf(T("%s  (%s bájt)"), e.Name, fmtSize(e.Size))),
		widget.NewForm(widget.NewFormItem(T("Darabméret"), sizeE), widget.NewFormItem(T("Cél mappa"), destE)),
		hint,
	)
	d := dialog.NewCustomConfirm(T("Fájl darabolása"), T("Darabolás"), T("Mégse"), form, func(ok bool) {
		if !ok {
			return
		}
		txt := strings.TrimSpace(sizeE.Text)
		var part int64
		for _, s := range splitPresets {
			if s.Label == txt {
				part = s.Size
			}
		}
		if part == 0 {
			v, err := parseSize(txt)
			if err != nil {
				a.warn(errText(err))
				return
			}
			part = v
		}
		a.settings.Put("splitSize", txt)
		a.settings.Save()
		dest := strings.TrimSpace(destE.Text)
		if err := os.MkdirAll(dest, 0o755); err != nil {
			a.warn(errText(err))
			return
		}
		var n int
		a.runJob(T("Darabolás"), func(j *job) error {
			var err error
			n, err = splitFile(e.Path, dest, part, j)
			return err
		}, func(err error) {
			a.left.refresh()
			a.right.refresh()
			if err == nil && n > 0 {
				a.warn(fmt.Sprintf(Tn(n, "Kész: %d darab és egy .crc fájl a(z) %s mappában."), n, dest))
			}
		})
	}, a.win)
	d.Resize(fyne.NewSize(560, 0))
	d.Show()
}

func (a *App) joinCurrent() {
	p := a.active
	if !a.needLocal(p) {
		return
	}
	e := p.currentEntry()
	if e == nil || e.IsDir || !isPartName(e.Name) {
		a.warn(T("Állj a kurzorral a darabolt fájl egyik darabjára (pl. valami.zip.001)."))
		return
	}
	target := p.path
	if o := a.other(); o.vfs == nil {
		target = o.path
	}
	a.inputBox(T("Fájlok összefűzése"), fmt.Sprintf(T("A(z) „%s” darabjainak összefűzése ide:"), strings.TrimSuffix(e.Name, filepath.Ext(e.Name))), target, func(dest string) {
		dest = strings.TrimSpace(dest)
		if err := os.MkdirAll(dest, 0o755); err != nil {
			a.warn(errText(err))
			return
		}
		var out, note string
		a.runJob(T("Összefűzés"), func(j *job) error {
			var err error
			out, note, err = joinFiles(e.Path, dest, j)
			return err
		}, func(err error) {
			a.left.refresh()
			a.right.refresh()
			if err == nil && out != "" {
				a.warn(note)
			}
		})
	})
}

// ── jogok és dátum ──

// lxSetPerm a jogosultságokat módosítja. Mappánál az olvasási joggal együtt a
// belépési (x) jog is megmarad, különben a mappa használhatatlanná válna.
func lxSetPerm(p string, isDir bool, c bitChange) error {
	st, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if st.Mode()&fs.ModeSymlink != 0 {
		return nil
	}
	if isDir {
		x := (c.Set & 0o444) >> 2
		c.Set |= x
		c.Clear &^= x
	}
	nv := c.apply(uint32(st.Mode().Perm())) & 0o777
	return os.Chmod(p, fs.FileMode(nv)|st.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky))
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
	var mode uint32 = 0o644
	mixed := false
	for i, e := range items {
		st, err := os.Stat(e.Path)
		if err != nil {
			continue
		}
		m := uint32(st.Mode().Perm())
		if i == 0 {
			mode = m
		} else if m != mode {
			mixed = true
		}
	}
	permC := widget.NewCheck(T("Jogok beállítása"), nil)
	octal := widget.NewLabel("")
	var checks [9]*widget.Check
	grid := container.NewGridWithColumns(4, widget.NewLabel(""), widget.NewLabel(T("Olvasás")), widget.NewLabel(T("Írás")), widget.NewLabel(T("Futtatás / belépés")))
	upd := func() {
		var v uint32
		for i, c := range checks {
			if c.Checked {
				v |= 1 << (8 - i)
			}
		}
		octal.SetText(fmt.Sprintf(T("Számmal: %03o"), v))
	}
	who := []string{T("Tulajdonos"), T("Csoport"), T("Mindenki más")}
	for r := 0; r < 3; r++ {
		grid.Add(widget.NewLabel(who[r]))
		for c := 0; c < 3; c++ {
			i := r*3 + c
			ch := widget.NewCheck("", func(bool) {
				permC.SetChecked(true)
				upd()
			})
			ch.Checked = mode&(1<<(8-i)) != 0
			checks[i] = ch
			grid.Add(ch)
		}
	}
	upd()
	note := T("Jelöld be a „Jogok beállítása” pipát, ha módosítani akarod őket.")
	if mixed {
		note = T("A kijelölt elemek jogai eltérnek; a táblázat az első elemét mutatja.")
	}
	timeC := widget.NewCheck(T("Módosítás dátuma:"), nil)
	timeE := widget.NewEntry()
	timeE.SetText(items[0].ModTime.Format("2006.01.02 15:04:05"))
	timeE.OnChanged = func(string) { timeC.SetChecked(true) }
	nowB := widget.NewButton(T("Most"), func() {
		timeE.SetText(time.Now().Format("2006.01.02 15:04:05"))
		timeC.SetChecked(true)
	})
	recC := widget.NewCheck(T("Mappák tartalmára is (almappákkal együtt)"), nil)
	if !hasDir {
		recC.Hide()
	}
	content := container.NewVBox(
		widget.NewLabel(describe(items)),
		permC, grid, octal, widget.NewLabel(note),
		widget.NewSeparator(),
		container.NewBorder(nil, nil, timeC, nowB, timeE),
		recC,
	)
	d := dialog.NewCustomConfirm(T("Jogok és dátum módosítása"), "OK", T("Mégse"), content, func(ok bool) {
		if !ok {
			return
		}
		req := attrRequest{Recurse: hasDir && recC.Checked}
		if permC.Checked {
			for i, c := range checks {
				bit := uint32(1) << (8 - i)
				if c.Checked {
					req.Bits.Set |= bit
				} else {
					req.Bits.Clear |= bit
				}
			}
		}
		if timeC.Checked {
			t, err := parseDateTime(timeE.Text)
			if err != nil {
				a.warn(errText(err))
				return
			}
			req.SetTime, req.Time = true, t
		}
		if req.Bits.empty() && !req.SetTime {
			return
		}
		paths := entryPaths(items)
		a.runJob(T("Jogok és dátum módosítása"), func(j *job) error {
			_, err := applyAttrs(paths, req, lxSetPerm, j)
			return err
		}, func(error) {
			a.left.refresh()
			a.right.refresh()
		})
	}, a.win)
	d.Resize(fyne.NewSize(560, 0))
	d.Show()
}
