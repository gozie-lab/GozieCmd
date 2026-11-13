//go:build linux

package main

// A fájllista: egy billentyűzetet kezelő nézet (fileView) és benne a sorok
// (fileRow). A sorokat a Fyne virtuális listája (widget.List) rajzolja, így
// több tízezer fájlnál is gyors.

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ── oszlopok ──

const (
	colName = iota
	colExt
	colSize
	colDate
	colAttr
	numCols
)

var colTitles = [numCols]string{T("Név"), T("Kiterj."), T("Méret"), T("Dátum"), "Attr."}

// colWidths az oszlopok szélessége (az ikon a név oszlopán belül van).
func (p *Panel) colWidths(total float32) [numCols]float32 {
	f := p.app.listTextSize() / 14
	w := [numCols]float32{0, 64 * f, 105 * f, 128 * f, 86 * f}
	rest := total - w[colExt] - w[colSize] - w[colDate] - w[colAttr]
	if rest < 120*f { // keskeny panel: az attribútum, majd a kiterjesztés oszlop eltűnik
		rest += w[colAttr]
		w[colAttr] = 0
	}
	if rest < 120*f {
		rest += w[colExt]
		w[colExt] = 0
	}
	w[colName] = rest
	return w
}

// ── egy sor ──

type fileRow struct {
	widget.BaseWidget
	p     *Panel
	idx   int
	bg    *canvas.Rectangle
	prog  *canvas.Rectangle // jobb gombos nyomva tartás: a helyi menüig hátralévő idő
	icon  *canvas.Image
	cells [numCols]*canvas.Text
	full  [numCols]string

	nameFull string
}

func newFileRow(p *Panel) *fileRow {
	r := &fileRow{p: p, idx: -1, bg: canvas.NewRectangle(color.Transparent), prog: canvas.NewRectangle(color.Transparent)}
	r.prog.Hide()
	r.icon = canvas.NewImageFromResource(theme.FileIcon())
	r.icon.FillMode = canvas.ImageFillContain
	for i := range r.cells {
		t := canvas.NewText("", color.Black)
		if i == colSize {
			t.Alignment = fyne.TextAlignTrailing
		}
		r.cells[i] = t
	}
	r.ExtendBaseWidget(r)
	return r
}

func (r *fileRow) CreateRenderer() fyne.WidgetRenderer {
	objs := []fyne.CanvasObject{r.bg, r.icon}
	for _, c := range r.cells {
		objs = append(objs, c)
	}
	objs = append(objs, r.prog)
	return &rowRenderer{r: r, objs: objs}
}

type rowRenderer struct {
	r    *fileRow
	objs []fyne.CanvasObject
}

func (rr *rowRenderer) Destroy()                     {}
func (rr *rowRenderer) Objects() []fyne.CanvasObject { return rr.objs }
func (rr *rowRenderer) Refresh() {
	rr.Layout(rr.r.Size())
	canvas.Refresh(rr.r)
}

func (rr *rowRenderer) MinSize() fyne.Size {
	return rr.r.p.cellSize()
}

const cellPad = 5

func (rr *rowRenderer) Layout(s fyne.Size) {
	r := rr.r
	r.bg.Resize(s)
	r.bg.Move(fyne.NewPos(0, 0))
	r.layoutHold(s)
	switch r.p.viewMode {
	case viewBrief:
		rr.layoutBrief(s)
		return
	case viewThumbs:
		rr.layoutThumb(s)
		return
	}
	w := r.p.colWidths(s.Width)
	ts := r.cells[0].TextSize
	isz := ts + 4
	r.icon.Resize(fyne.NewSize(isz, isz))
	r.icon.Move(fyne.NewPos(cellPad, (s.Height-isz)/2))
	x := float32(0)
	for i, c := range r.cells {
		cw := w[i]
		left := x + cellPad
		avail := cw - 2*cellPad
		if i == colName {
			left += isz + 4
			avail -= isz + 4
		}
		if cw <= 0 || avail <= 0 {
			c.Hide()
			x += cw
			continue
		}
		c.Show()
		c.Text = fitText(r.full[i], avail, c.TextSize, c.TextStyle)
		h := fyne.MeasureText("Ág", c.TextSize, c.TextStyle).Height
		c.Resize(fyne.NewSize(avail, h))
		c.Move(fyne.NewPos(left, (s.Height-h)/2))
		x += cw
	}
}

// fitText a szöveget „…”-tal rövidíti, hogy elférjen.
// layoutHold: a jobb gomb nyomva tartásakor a sor alján egy sáv telik meg, amíg a
// helyi menü elő nem jön – így látszik, hogy a várakozásnak célja van.
func (r *fileRow) layoutHold(s fyne.Size) {
	f := r.p.rsProg
	if r.idx != r.p.rsRow || f <= 0 {
		r.prog.Hide()
		return
	}
	h := float32(3)
	r.prog.FillColor = r.p.app.look.CursorFg // a nyomva tartott sor a kurzorsor: a betűszínével látszik rajta
	r.prog.Resize(fyne.NewSize(s.Width*f, h))
	r.prog.Move(fyne.NewPos(0, s.Height-h))
	r.prog.Show()
}

func fitText(s string, w, size float32, st fyne.TextStyle) string {
	if s == "" || fyne.MeasureText(s, size, st).Width <= w {
		return s
	}
	r := []rune(s)
	lo, hi := 0, len(r)
	for lo < hi {
		m := (lo + hi + 1) / 2
		if fyne.MeasureText(fmt.Sprintf(T("%s…"), string(r[:m])), size, st).Width <= w {
			lo = m
		} else {
			hi = m - 1
		}
	}
	return fmt.Sprintf(T("%s…"), string(r[:lo]))
}

// egér: a sorok kezelik a kattintást (a lista saját kijelölését nem használjuk)

func (r *fileRow) Tapped(*fyne.PointEvent) {}

func (r *fileRow) DoubleTapped(*fyne.PointEvent) {
	if r.idx >= 0 {
		r.p.setCursor(r.idx)
		r.p.openCurrent()
	}
}

func (r *fileRow) MouseDown(ev *desktop.MouseEvent) {
	r.p.rowMouseDown(r.idx, ev)
}

func (r *fileRow) MouseUp(ev *desktop.MouseEvent) {
	r.p.rowMouseUp(r.idx, ev)
}

func (r *fileRow) MouseIn(ev *desktop.MouseEvent)    { r.p.rowHover(r.idx, ev) }
func (r *fileRow) MouseMoved(ev *desktop.MouseEvent) { r.p.rowHover(r.idx, ev) }
func (r *fileRow) MouseOut()                         {}

func (r *fileRow) TappedSecondary(ev *fyne.PointEvent) {
	if !r.p.app.rightSelect() {
		r.p.showContextMenu(ev.AbsolutePosition)
	}
}

func (r *fileRow) Dragged(ev *fyne.DragEvent) { r.p.dragMove(r.idx, ev) }
func (r *fileRow) DragEnd()                   { r.p.dragEnd() }

// ── a lista üres része ──

// fileList és fileGrid: a Fyne lista és rács, kiegészítve azzal, hogy a sorok alatti
// üres részre kattintva (bal vagy jobb gombbal) a panel aktív lesz. A sorok a saját
// kattintásukat maguk kezelik, ide csak az üres terület kattintása jut el.
type fileList struct {
	widget.List
	p *Panel
}

func (l *fileList) Tapped(*fyne.PointEvent)          { l.p.activateFromEmpty() }
func (l *fileList) TappedSecondary(*fyne.PointEvent) { l.p.activateFromEmpty() }

type fileGrid struct {
	widget.GridWrap
	p *Panel
}

func (g *fileGrid) Tapped(*fyne.PointEvent)          { g.p.activateFromEmpty() }
func (g *fileGrid) TappedSecondary(*fyne.PointEvent) { g.p.activateFromEmpty() }

func (p *Panel) activateFromEmpty() {
	p.activate()
	p.focus()
}

// ── a lista nézete: fókusz és billentyűzet ──

type fileView struct {
	widget.BaseWidget
	p       *Panel
	bg      *canvas.Rectangle
	flist   *fileList
	list    *widget.List
	grid    *widget.GridWrap
	holder  *fyne.Container
	focused bool
}

func newFileView(p *Panel) *fileView {
	v := &fileView{p: p, bg: canvas.NewRectangle(color.White)}
	l := &fileList{p: p}
	l.Length = func() int { return len(p.items) }
	l.CreateItem = func() fyne.CanvasObject { return newFileRow(p) }
	l.UpdateItem = func(id widget.ListItemID, o fyne.CanvasObject) { p.updateRow(id, o.(*fileRow)) }
	l.HideSeparators = true
	l.ExtendBaseWidget(l)
	v.list, v.flist = &l.List, l
	v.holder = container.NewStack()
	v.ExtendBaseWidget(v)
	return v
}

func (v *fileView) CreateRenderer() fyne.WidgetRenderer {
	v.applyMode()
	return widget.NewSimpleRenderer(container.NewStack(v.bg, v.holder))
}

// applyMode: részletes nézetben a lista, rövid és bélyegkép nézetben a rács látszik.
// A rács cellamérete a létrehozáskor rögzül, ezért módváltáskor újat készítünk.
func (v *fileView) applyMode() {
	p := v.p
	if p.viewMode == viewDetail {
		v.grid = nil
		v.holder.Objects = []fyne.CanvasObject{container.NewThemeOverride(v.listObj(), tightTheme{p.app.theme})}
	} else {
		g := &fileGrid{p: p}
		g.Length = func() int { return len(p.items) }
		g.CreateItem = func() fyne.CanvasObject { return newFileRow(p) }
		g.UpdateItem = func(id widget.GridWrapItemID, o fyne.CanvasObject) { p.updateRow(id, o.(*fileRow)) }
		g.ExtendBaseWidget(g)
		v.grid = &g.GridWrap
		v.holder.Objects = []fyne.CanvasObject{container.NewThemeOverride(g, tightTheme{p.app.theme})}
	}
	v.holder.Refresh()
}

// listObj: a részletes nézet listája a saját (kattintható) típusával.
func (v *fileView) listObj() fyne.CanvasObject { return v.flist }

func (v *fileView) refreshAll() {
	if v.grid != nil {
		v.grid.Refresh()
	} else {
		v.list.Refresh()
	}
}

func (v *fileView) refreshItem(i int) {
	if v.grid != nil {
		v.grid.RefreshItem(i)
	} else {
		v.list.RefreshItem(i)
	}
}

// columns: hány elem van egy sorban (részletes nézetben 1).
func (v *fileView) columns() int {
	if v.grid == nil {
		return 1
	}
	if c := v.grid.ColumnCount(); c > 0 {
		return c
	}
	return 1
}

func (v *fileView) scrollOffset() float32 {
	if v.grid != nil {
		return v.grid.GetScrollOffset()
	}
	return v.list.GetScrollOffset()
}

func (v *fileView) FocusGained() {
	v.focused = true
	v.p.app.setActive(v.p)
	v.p.refreshCursorRows()
}

func (v *fileView) FocusLost() {
	v.p.endQuickSearch()
	v.focused = false
	v.p.refreshCursorRows()
}

func (v *fileView) AcceptsTab() bool { return true }

func (v *fileView) TypedRune(r rune) { v.p.typedRune(r) }

func (v *fileView) TypedKey(ev *fyne.KeyEvent) { v.p.typedKey(ev) }

func (v *fileView) KeyDown(ev *fyne.KeyEvent) {
	// a numerikus billentyűzet +, -, * jele (X11 billentyűkódok): kijelölés mintával
	switch ev.Physical.ScanCode {
	case 86:
		v.p.kpRune = '+'
	case 82:
		v.p.kpRune = '-'
	case 63:
		v.p.kpRune = '*'
	default:
		v.p.kpRune = 0
	}
}

func (v *fileView) KeyUp(*fyne.KeyEvent) {}

func (v *fileView) TypedShortcut(s fyne.Shortcut) { v.p.typedShortcut(s) }

// visibleRows: hány sor fér el egyszerre.
func (v *fileView) visibleRows() int {
	if v.grid != nil {
		h := v.p.cellSize().Height
		n := int(v.grid.Size().Height / h)
		if n < 1 {
			n = 1
		}
		return n
	}
	h := v.p.rowHeight()
	if h <= 0 {
		return 10
	}
	n := int(v.list.Size().Height / h)
	if n < 1 {
		n = 1
	}
	return n
}

// ensureVisible a sort a látható területre görgeti.
func (v *fileView) ensureVisible(i int) {
	if v.grid != nil {
		v.grid.ScrollTo(i)
		return
	}
	h := v.p.rowHeight()
	off := v.list.GetScrollOffset()
	vh := v.list.Size().Height
	top := float32(i) * h
	switch {
	case top < off:
		v.list.ScrollToOffset(top)
	case top+h > off+vh:
		v.list.ScrollToOffset(top + h - vh)
	}
}

// ── oszlopfejléc ──

type headerCell struct {
	widget.BaseWidget
	p   *Panel
	col int
	txt *canvas.Text
	bg  *canvas.Rectangle
}

func newHeaderCell(p *Panel, col int) *headerCell {
	h := &headerCell{p: p, col: col, txt: canvas.NewText(colTitles[col], color.Black), bg: canvas.NewRectangle(color.Transparent)}
	if col == colSize {
		h.txt.Alignment = fyne.TextAlignTrailing
	}
	h.ExtendBaseWidget(h)
	return h
}

func (h *headerCell) CreateRenderer() fyne.WidgetRenderer {
	return &headerCellRenderer{h: h}
}

func (h *headerCell) Tapped(*fyne.PointEvent) { h.p.sortBy(h.col) }

func (h *headerCell) MouseIn(*desktop.MouseEvent) {
	h.bg.FillColor = theme.Color(theme.ColorNameHover)
	h.bg.Refresh()
}
func (h *headerCell) MouseMoved(*desktop.MouseEvent) {}
func (h *headerCell) MouseOut() {
	h.bg.FillColor = color.Transparent
	h.bg.Refresh()
}

type headerCellRenderer struct{ h *headerCell }

func (r *headerCellRenderer) Destroy() {}
func (r *headerCellRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.h.bg, r.h.txt}
}
func (r *headerCellRenderer) MinSize() fyne.Size {
	return fyne.NewSize(10, r.h.p.app.textSize()+10)
}
func (r *headerCellRenderer) Layout(s fyne.Size) {
	r.h.bg.Resize(s)
	t := r.h.txt
	t.TextSize = r.h.p.app.textSize()
	t.Color = theme.Color(theme.ColorNameForeground)
	label := colTitles[r.h.col]
	if r.h.p.sortCol == r.h.col {
		if r.h.p.sortDesc {
			label += " ▼"
		} else {
			label += " ▲"
		}
	}
	t.Text = fitText(label, s.Width-2*cellPad, t.TextSize, t.TextStyle)
	hh := fyne.MeasureText("Ág", t.TextSize, t.TextStyle).Height
	t.Resize(fyne.NewSize(s.Width-2*cellPad, hh))
	t.Move(fyne.NewPos(cellPad, (s.Height-hh)/2))
}
func (r *headerCellRenderer) Refresh() {
	r.Layout(r.h.Size())
	canvas.Refresh(r.h)
}

// headerLayout a fejléc celláit a sorokkal azonos szélességre osztja.
type headerLayout struct{ p *Panel }

func (l *headerLayout) Layout(objs []fyne.CanvasObject, s fyne.Size) {
	w := l.p.colWidths(s.Width)
	x := float32(0)
	for i, o := range objs {
		if i >= numCols {
			break
		}
		o.Move(fyne.NewPos(x, 0))
		o.Resize(fyne.NewSize(w[i], s.Height))
		if w[i] <= 0 {
			o.Hide()
		} else {
			o.Show()
		}
		x += w[i]
	}
}

func (l *headerLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	if len(objs) == 0 {
		return fyne.NewSize(0, 0)
	}
	return fyne.NewSize(100, objs[0].MinSize().Height)
}

// ── kattintható útvonalsáv ──

type tapBar struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onTap   func()
}

func newTapBar(content fyne.CanvasObject, onTap func()) *tapBar {
	t := &tapBar{content: content, onTap: onTap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tapBar) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(t.content) }
func (t *tapBar) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

// ── segédek ──

func iconFor(e *Entry) fyne.Resource {
	switch {
	case e.IsParent:
		return theme.NewColoredResource(theme.MoveUpIcon(), theme.ColorNameForeground)
	case e.IsDir:
		return folderRes
	}
	switch strings.ToLower(e.Ext) {
	case "jpg", "jpeg", "png", "gif", "bmp", "webp", "svg", "tif", "tiff", "ico", "heic", "avif", "xcf", "kra":
		return theme.FileImageIcon()
	case "mp4", "mkv", "avi", "mov", "webm", "wmv", "flv", "m4v", "mpg", "mpeg":
		return theme.FileVideoIcon()
	case "mp3", "flac", "ogg", "wav", "m4a", "opus", "aac", "wma":
		return theme.FileAudioIcon()
	case "txt", "md", "markdown", "log", "ini", "conf", "cfg", "json", "xml", "yaml", "yml", "csv",
		"go", "c", "h", "cpp", "py", "sh", "js", "ts", "html", "css", "rs", "java", "toml", "desktop":
		return theme.FileTextIcon()
	case "zip", "7z", "rar", "tar", "gz", "tgz", "xz", "txz", "bz2", "tbz", "tbz2", "zst", "cab", "arj", "lzh",
		"deb", "rpm", "appimage", "exe", "msi", "iso", "img", "flatpakref", "run", "bin", "jar":
		return theme.FileApplicationIcon()
	}
	if e.Attr&0o111 != 0 {
		return theme.FileApplicationIcon()
	}
	return theme.FileIcon()
}

var folderRes = theme.NewColoredResource(theme.FolderIcon(), colFolder)
