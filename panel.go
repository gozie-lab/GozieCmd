//go:build windows

package main

// Egy fájlpanel: meghajtóválasztó, útvonalsáv, lista és állapotsor.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

type Panel struct {
	lastPos map[string]string // mappánként a kurzor alatti elem, amikor elhagytuk (visszalépéskor oda áll)
	app  *App
	side string

	drives   *walk.ComboBox
	free     *walk.Label
	btnRoot  *walk.PushButton
	btnUp    *walk.PushButton
	btnDisc  *walk.PushButton // távoli kapcsolat bontása
	header   *walk.Composite
	pathLbl  *walk.Label
	tv       *walk.TableView
	status   *walk.Label
	model    *FileModel
	driveLst []string

	path        string
	back        []string
	lastMod     time.Time
	watch       *dirWatch // változásfigyelés
	watchPath   string
	watchFailed bool
	watchOff    bool // eszköz eltávolítása miatt felfüggesztve
	pollTick    int
	updating    bool
	sortKeep    *Entry
	sortReady   bool

	// lapfülek, gyorskeresés
	tabs    []tabState
	tabIdx  int
	tabBar  *walk.Composite
	tabBtns []*walk.PushButton

	// nézet, gyorsnézet
	viewMode     int
	thumbIml     uintptr
	thumbImlSize int32
	qvBox        *walk.Composite
	qvTitle      *walk.Label
	qvText       *walk.TextEdit
	qvImage      *walk.ImageView
	qs           string
	qsOn         bool
	// jobb gombos kijelölés állapota
	rsActive, rsMark, rsMoved bool
	rsStart, rsLast           int
	rsT0                      time.Time // a jobb gomb lenyomásának ideje (a jelzősávhoz)
	rsBar                     win.RECT  // a kirajzolt jelzősáv (törléshez)

	fwd      []string
	lv       win.HWND // a lista tényleges Windows-vezérlője (húzd és ejtsd)
	frozenLV win.HWND
	dropRow  int // húzás közben kiemelt célmappa sora
	dropTgt  *dropTarget

	vfs   VFS    // nem nil: ZIP-archívum vagy FTP-kapcsolat látszik a panelen
	vpath string // belső útvonal a vfs-en belül
}

// vfsExit a „..” bejegyzés útvonala a virtuális mappa gyökerében: kilépés.
const vfsExit = "\x00kilepes"

func (p *Panel) kind() string {
	if p.vfs == nil {
		return "local"
	}
	return p.vfs.Kind()
}

func newPanel(app *App, side string) *Panel {
	p := &Panel{app: app, side: side, model: NewFileModel()}
	p.model.beforeSort = func() { p.sortKeep = p.currentEntry() }
	p.model.afterReset = func() {
		if p.tv != nil {
			p.repaint()
		}
	}
	p.model.afterSort = func() {
		if p.tv != nil {
			p.repaint()
		}
		p.updateSortTitles()
		if p.sortReady { // a rendezés megjegyzése (a Linux-változattal azonos kulcsokon)
			p.app.settings.Put(p.side+".sort", strconv.Itoa(p.model.sortCol))
			p.app.settings.Put(p.side+".sortDesc", boolStr(p.model.sortOrder == walk.SortDescending))
		}
		if p.sortKeep != nil {
			for i, e := range p.model.items {
				if e == p.sortKeep {
					p.setCursor(i)
					break
				}
			}
		}
	}
	return p
}

func (p *Panel) widget() Widget {
	return Composite{
		Layout: VBox{MarginsZero: true, Spacing: 3},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 4},
				Children: []Widget{
					ComboBox{AssignTo: &p.drives, MaxSize: Size{Width: 70}, MinSize: Size{Width: 70},
						OnCurrentIndexChanged: p.driveChosen},
					Label{AssignTo: &p.free},
					HSpacer{},
					PushButton{AssignTo: &p.btnDisc, Text: T("Kapcsolat bontása"), Visible: false,
						ToolTipText: T("A távoli kapcsolat bontása, vissza a helyi mappába (Ctrl+Shift+F)"),
						OnClicked: func() {
							p.activate()
							p.exitVFS()
							p.tv.SetFocus()
						}},
					PushButton{AssignTo: &p.btnRoot, Text: `\`, MaxSize: Size{Width: 28}, ToolTipText: T("Gyökérmappa"),
						OnClicked: func() { p.activate(); p.goRoot() }},
					PushButton{AssignTo: &p.btnUp, Text: "..", MaxSize: Size{Width: 28}, ToolTipText: T("Szülőmappa (Backspace)"),
						OnClicked: func() { p.activate(); p.goUp() }},
				},
			},
			Composite{AssignTo: &p.tabBar, Layout: HBox{MarginsZero: true, Spacing: 2}, Visible: false},
			Composite{
				AssignTo:    &p.header,
				Layout:      HBox{Margins: Margins{Left: 6, Top: 3, Right: 6, Bottom: 3}},
				Background:  SolidColorBrush{Color: inactiveBg},
				OnMouseDown: func(x, y int, b walk.MouseButton) { p.activate(); p.app.gotoPathDialog() },
				Children: []Widget{
					Label{AssignTo: &p.pathLbl, Font: Font{Family: "Segoe UI", PointSize: 9, Bold: true},
						EllipsisMode: EllipsisPath},
				},
			},
			TableView{
				AssignTo:         &p.tv,
				Model:            p.model,
				MultiSelection:   false,
				ColumnsOrderable: false,
				Columns: []TableViewColumn{
					{Title: T("Név"), Width: 260},
					{Title: T("Kiterj."), Width: 55},
					{Title: T("Méret"), Width: 95, Alignment: AlignFar},
					{Title: T("Dátum"), Width: 115},
					{Title: T("Attr."), Width: 45},
				},
				StyleCell:       p.styleCell,
				OnItemActivated: p.openCurrent,
				OnKeyDown:       p.onKey,
				OnMouseDown:     p.onMouse,
				OnSizeChanged:   p.fitColumns,
				ContextMenuItems: []MenuItem{
					Action{Text: T("Megnyitás\tEnter"), OnTriggered: p.openCurrent},
					Action{Text: T("Nézőke\tF3"), OnTriggered: func() { p.app.viewFile() }},
					Action{Text: T("Szerkesztés\tF4"), OnTriggered: func() { p.app.editFile() }},
					Separator{},
					Action{Text: T("Másolás\tF5"), OnTriggered: func() { p.app.transfer(false) }},
					Action{Text: T("Mozgatás\tF6"), OnTriggered: func() { p.app.transfer(true) }},
					Action{Text: T("Átnevezés\tShift+F6"), OnTriggered: func() { p.app.rename() }},
					Action{Text: T("Törlés\tF8"), OnTriggered: func() { p.app.deleteSelected(false) }},
					Separator{},
					Action{Text: T("Új mappa\tF7"), OnTriggered: func() { p.app.mkdir() }},
					Action{Text: T("Új fájl\tShift+F4"), OnTriggered: func() { p.app.newFile() }},
					Action{Text: T("Útvonal másolása\tCtrl+Shift+C"), OnTriggered: func() { p.app.copyPaths() }},
					Action{Text: T("Tulajdonságok"), OnTriggered: p.properties},
				},
			},
			Composite{
				AssignTo: &p.qvBox, Visible: false, Layout: VBox{MarginsZero: true, Spacing: 2},
				Children: []Widget{
					Label{AssignTo: &p.qvTitle, Font: Font{Family: "Segoe UI", PointSize: 9, Bold: true}, EllipsisMode: EllipsisEnd},
					TextEdit{AssignTo: &p.qvText, ReadOnly: true, HScroll: true, VScroll: true, Font: Font{Family: "Consolas", PointSize: 10}},
					ImageView{AssignTo: &p.qvImage, Visible: false, Mode: ImageViewModeShrink},
				},
			},
			Label{AssignTo: &p.status},
		},
	}
}

// afterCreate a felület létrejötte után fut.
func (p *Panel) afterCreate(start string) {
	for _, w := range []walk.Window{p.drives, p.btnRoot, p.btnUp} {
		removeTabStop(w.Handle())
	}
	p.tv.CurrentIndexChanged().Attach(func() {
		if p.app.active == p {
			p.app.scheduleQuickView()
		}
	})
	p.tv.FocusedChanged().Attach(func() {
		if p.tv.Focused() {
			p.app.setActive(p)
		} else {
			p.endQuickSearch()
		}
	})
	p.populateDrives()
	if !p.navigate(start, p.tabSel(), false, true) {
		home, _ := os.UserHomeDir()
		if !p.navigate(home, "", false, true) {
			p.navigate(`C:\`, "", false, false)
		}
	}
	p.restoreSort()
	p.app.mw.Synchronize(p.loadViewMode) // a lista ablakai ekkorra már élnek
}

// restoreSort a legutóbbi rendezést állítja vissza (a walk induláskor a saját
// alapértelmezését alkalmazza, ezért a mentett értéket utána, késleltetve is).
func (p *Panel) restoreSort() {
	col, desc := 0, false
	if v, ok := p.app.settings.Get(p.side + ".sort"); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 3 {
			col = n
		}
	}
	if v, ok := p.app.settings.Get(p.side + ".sortDesc"); ok {
		desc = v == "1"
	}
	order := walk.SortAscending
	if desc {
		order = walk.SortDescending
	}
	apply := func() {
		// mindig újra rendezünk: a walk csak így rajzolja ki a fejlécben az irányjelet
		p.model.Sort(col, order)
		p.sortReady = true
	}
	apply()
	p.app.mw.Synchronize(apply)
}

func (p *Panel) activate() { p.app.setActive(p) }

// ── navigáció ──

func (p *Panel) navigate(path, selectName string, addHistory, quiet bool) bool {
	if len(path) == 2 && path[1] == ':' {
		path += `\`
	}
	if p.netNavigate(path) { // `\\` vagy `\\gép`: hálózati nézet
		return true
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	path = abs
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		selectName = filepath.Base(path)
		path = filepath.Dir(path)
	}
	entries, err := readEntries(path, p.app.showHidden)
	if err != nil && !quiet && p.tryConnect(path, err) {
		entries, err = readEntries(path, p.app.showHidden)
	}
	if err == nil {
		if server, _, ok := uncParts(path); ok {
			p.app.rememberServer(server)
		}
	}
	if err != nil {
		if !quiet {
			walk.MsgBox(p.app.mw, appName, fmt.Sprintf(T("A mappa nem nyitható meg:\n%s\n\n%s"), path, errText(err)),
				walk.MsgBoxIconWarning)
		}
		return false
	}
	if p.vfs != nil { // kilépés az archívumból / FTP-ről
		p.vfs.Close()
		p.vfs, p.vpath = nil, ""
		p.model.virtual = false
		p.model.marked = map[string]bool{}
		p.model.dirSizes = map[string]int64{}
	}
	if p.path != "" && !strings.EqualFold(p.path, path) {
		p.rememberPos()
		if selectName == "" {
			selectName = p.lastPos[strings.ToLower(path)] // ahol legutóbb álltunk benne
		}
		if addHistory {
			p.back = append(p.back, p.path)
			p.fwd = nil
			if len(p.back) > 100 {
				p.back = p.back[1:]
			}
		}
		p.model.marked = map[string]bool{}
		p.model.dirSizes = map[string]int64{}
	}
	if !strings.EqualFold(p.path, path) {
		p.watchOff = false
	}
	p.path = path
	p.lastMod = dirModTime(path)
	p.syncWatch()
	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	}
	// újrarajzolás csak a végén, egyszer (sötét módban kevesebb villanás)
	p.tv.SetSuspended(true)
	p.model.setItems(entries, parent)
	if !p.selectName(selectName) {
		p.setCursor(0)
	}
	p.tv.SetSuspended(false)
	p.updateHeader()
	return true
}

func (p *Panel) refresh() {
	if p.vfs != nil {
		p.refreshVFS()
		return
	}
	if p.path == "" {
		return
	}
	dir := p.path
	for {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			break
		}
		up := filepath.Dir(dir)
		if up == dir {
			break
		}
		dir = up
	}
	if dir != p.path {
		p.navigate(dir, "", false, true)
		return
	}
	cur := p.currentEntry()
	row := p.cursorRow()
	entries, err := readEntries(dir, p.app.showHidden)
	if err != nil {
		return
	}
	p.lastMod = dirModTime(dir)
	parent := filepath.Dir(dir)
	if parent == dir {
		parent = ""
	}
	p.model.setItems(entries, parent)
	if cur == nil || cur.IsParent || !p.selectName(cur.Name) {
		p.setCursor(row)
	}
	p.updateHeader()
}

// checkChanged: ha a mappa kívülről megváltozott, frissít.
func (p *Panel) checkChanged() {
	p.syncWatch()
	if p.path == "" || p.vfs != nil {
		return
	}
	if p.watch != nil && !p.watch.remote() {
		if p.pollTick++; p.pollTick%watchPollEvery != 0 {
			return
		}
	}
	if m := dirModTime(p.path); m.IsZero() || !m.Equal(p.lastMod) {
		p.refresh()
	}
}

func dirModTime(path string) time.Time {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

func (p *Panel) goUp() {
	if p.vfs != nil {
		if parent, ok := p.vfs.Parent(p.vpath); ok {
			sel := p.baseName(p.vpath)
			if p.kind() == "net" {
				sel = strings.TrimPrefix(p.vpath, `\\`)
			}
			p.navigateVFS(parent, sel)
		} else {
			p.exitVFS()
		}
		return
	}
	parent := filepath.Dir(p.path)
	if parent != p.path {
		p.navigate(parent, filepath.Base(p.path), true, false)
	} else if isUNCShareRoot(p.path) { // megosztás gyökeréből a gép megosztásaihoz
		server, share, _ := uncParts(p.path)
		p.openNet(`\\`+server, share)
	}
}

func (p *Panel) goRoot() {
	if p.vfs != nil {
		p.navigateVFS(p.vfs.Root(), "")
		return
	}
	p.navigate(filepath.VolumeName(p.path)+`\`, "", true, false)
}

func (p *Panel) goBack() {
	for len(p.back) > 0 {
		t := p.back[len(p.back)-1]
		p.back = p.back[:len(p.back)-1]
		cur := p.path
		if p.navigate(t, "", false, true) {
			p.fwd = append(p.fwd, cur)
			return
		}
	}
}

func (p *Panel) goForward() {
	for len(p.fwd) > 0 {
		t := p.fwd[len(p.fwd)-1]
		p.fwd = p.fwd[:len(p.fwd)-1]
		cur := p.path
		if p.navigate(t, "", false, true) {
			p.back = append(p.back, cur)
			return
		}
	}
}

func (p *Panel) openCurrent() {
	e := p.currentEntry()
	if e == nil {
		return
	}
	switch {
	case e.IsParent:
		p.goUp()
	case p.kind() == "net" && e.IsDir:
		if _, share, _ := uncParts(e.Path); share != "" {
			p.navigate(e.Path, "", true, false) // megosztás: valódi mappaként
		} else {
			p.navigateVFS(e.Path, "")
		}
	case p.vfs != nil && e.IsDir:
		p.navigateVFS(e.Path, "")
	case p.vfs != nil:
		p.app.openVirtual(p, e, false)
	case e.IsDir:
		p.navigate(e.Path, "", true, false)
	case isArchive(e.Name):
		p.openArchiveAt(e.Path)
	default:
		if !shellOpen(p.app.mw.Handle(), e.Path, p.path) {
			walk.MsgBox(p.app.mw, appName, fmt.Sprintf(T("Nem sikerült megnyitni:\n%s"), e.Path), walk.MsgBoxIconWarning)
		}
	}
}

func (p *Panel) properties() {
	if p.vfs != nil {
		return
	}
	e := p.currentEntry()
	target := p.path
	if e != nil && !e.IsParent {
		target = e.Path
	}
	shellVerbEx(p.app.mw.Handle(), "properties", target)
}

// ── kurzor és jelölés ──

func (p *Panel) currentEntry() *Entry {
	i := p.cursorRow()
	if i < 0 || i >= len(p.model.items) {
		return nil
	}
	return p.model.items[i]
}

func (p *Panel) selectedEntries() []*Entry {
	var out []*Entry
	if len(p.model.marked) > 0 {
		for _, e := range p.model.items {
			if !e.IsParent && p.model.marked[e.Name] {
				out = append(out, e)
			}
		}
		return out
	}
	if e := p.currentEntry(); e != nil && !e.IsParent {
		out = append(out, e)
	}
	return out
}

func (p *Panel) setCursor(i int) {
	n := len(p.model.items)
	if n == 0 {
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}
	p.tv.SetCurrentIndex(i)
	p.tv.EnsureItemVisible(i)
}

func (p *Panel) selectName(name string) bool {
	if name == "" {
		return false
	}
	for i, e := range p.model.items {
		if !e.IsParent && strings.EqualFold(e.Name, name) {
			p.setCursor(i)
			return true
		}
	}
	return false
}

func (p *Panel) toggleMark(row int) {
	if row < 0 || row >= len(p.model.items) {
		return
	}
	e := p.model.items[row]
	if e.IsParent {
		return
	}
	if p.model.marked[e.Name] {
		delete(p.model.marked, e.Name)
	} else {
		p.model.marked[e.Name] = true
	}
	p.redrawRow(row)
	p.updateStatus()
}

func (p *Panel) setMarks(names map[string]bool) {
	p.model.marked = names
	if p.lv != 0 {
		win.InvalidateRect(p.lv, nil, false) // háttértörlés nélkül: nem villog
	} else {
		p.repaint()
	}
	p.updateStatus()
}

func (p *Panel) markAll() {
	m := map[string]bool{}
	for _, e := range p.model.items {
		if !e.IsParent {
			m[e.Name] = true
		}
	}
	p.setMarks(m)
}

func (p *Panel) invertMarks() {
	m := map[string]bool{}
	for _, e := range p.model.items {
		if e.IsParent {
			continue
		}
		if e.IsDir {
			if p.model.marked[e.Name] {
				m[e.Name] = true
			}
		} else if !p.model.marked[e.Name] {
			m[e.Name] = true
		}
	}
	p.setMarks(m)
}

func (p *Panel) markPattern(patterns []string, add bool) {
	m := map[string]bool{}
	for k := range p.model.marked {
		m[k] = true
	}
	for _, e := range p.model.items {
		if e.IsParent {
			continue
		}
		for _, pat := range patterns {
			wantDir := strings.HasSuffix(pat, `\`) || strings.HasSuffix(pat, "/")
			pat = strings.ToLower(strings.TrimRight(pat, `\/`))
			if pat == "*.*" || pat == "" {
				pat = "*"
			}
			if ok, _ := filepath.Match(pat, strings.ToLower(e.Name)); ok && wantDir == e.IsDir {
				if add {
					m[e.Name] = true
				} else {
					delete(m, e.Name)
				}
				break
			}
		}
	}
	p.setMarks(m)
}

func (p *Panel) computeDirSize(e *Entry) {
	if p.vfs != nil {
		return
	}
	p.model.dirSizes[e.Name] = -1
	p.repaint()
	base, name, path := p.path, e.Name, e.Path
	go func() {
		var total int64
		filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if info, err := d.Info(); err == nil {
					total += info.Size()
				}
			}
			return nil
		})
		p.app.mw.Synchronize(func() {
			if p.path == base {
				p.model.dirSizes[name] = total
				p.repaint()
				p.updateStatus()
			}
		})
	}()
}

// ── megjelenítés ──

func (p *Panel) fitColumns() {
	if p.viewMode == viewBrief && p.lv != 0 {
		win.SendMessage(p.lv, 0x101E, 0, uintptr(p.briefWidth())) // LVM_SETCOLUMNWIDTH (lista mód)
		return
	}
	if p.viewMode != viewDetail {
		return
	}
	cols := p.tv.Columns()
	if cols.Len() < 5 {
		return
	}
	other := 0
	for i := 1; i < cols.Len(); i++ {
		other += cols.At(i).Width()
	}
	w := p.tv.ClientBounds().Width - other - 24 // függőleges görgetősáv
	if w < 120 {
		w = 120
	}
	cols.At(0).SetWidth(w)
}

func (p *Panel) setActive(on bool) {
	bg, fg := inactiveBg, inactiveFg
	if on {
		bg, fg = activeBg, activeFg
	}
	if brush, err := walk.NewSolidColorBrush(bg); err == nil {
		p.header.SetBackground(brush)
	}
	p.pathLbl.SetTextColor(fg)
	p.header.Invalidate()
}

func (p *Panel) updateHeader() {
	if p.btnDisc != nil {
		if remote := p.vfs != nil && isRemoteKind(p.vfs.Kind()); remote != p.btnDisc.Visible() {
			p.btnDisc.SetVisible(remote)
		}
	}
	if p.vfs != nil {
		p.pathLbl.SetText(p.vfs.Display(p.vpath))
		p.free.SetText(p.vfs.Info())
		p.updateStatus()
		if p.app.active == p {
			p.app.updateCmdLabel()
		}
		p.renderTabs()
		return
	}
	p.renderTabs()
	p.pathLbl.SetText(p.path)
	p.selectDrive()
	root := filepath.VolumeName(p.path) + `\`
	text := ""
	if name := volumeName(root); name != "" {
		text = "[" + name + "]  "
	}
	if free, total, ok := diskFree(p.path); ok {
		text += fmt.Sprintf(T("%s szabad / %s"), fmtHuman(free), fmtHuman(total))
	}
	p.free.SetText(text)
	p.updateStatus()
	if p.app.active == p {
		p.app.updateCmdLabel()
	}
}

func (p *Panel) updateStatus() {
	var files, dirs, mf, md int
	var sel, total int64
	for _, e := range p.model.items {
		if e.IsParent {
			continue
		}
		m := p.model.marked[e.Name]
		if e.IsDir {
			dirs++
			if m {
				md++
				if s := p.model.dirSizes[e.Name]; s > 0 {
					sel += s
				}
			}
		} else {
			files++
			total += e.Size
			if m {
				mf++
				sel += e.Size
			}
		}
	}
	quickText(p.status, fmt.Sprintf(T("%s / %s kijelölve,  %s,  %s"),
		fmtHuman(uint64(sel)), fmtHuman(uint64(total)),
		fmt.Sprintf(Tn(files, "%d / %d fájl"), mf, files), fmt.Sprintf(Tn(dirs, "%d / %d mappa"), md, dirs)))
}

// ── meghajtók ──

func (p *Panel) populateDrives() {
	drives, err := walk.DriveNames()
	if err != nil {
		return
	}
	p.driveLst = append(drives, netRootMarker)
	labels := make([]string, len(p.driveLst))
	for i, d := range drives {
		labels[i] = strings.TrimSuffix(d, `\`)
	}
	labels[len(labels)-1] = T("Hálózat")
	p.updating = true
	p.drives.SetModel(labels)
	p.updating = false
	p.selectDrive()
}

func (p *Panel) selectDrive() {
	vol := strings.ToUpper(filepath.VolumeName(p.path))
	p.updating = true
	defer func() { p.updating = false }()
	for i, d := range p.driveLst {
		if strings.EqualFold(strings.TrimSuffix(d, `\`), vol) {
			if p.drives.CurrentIndex() != i {
				p.drives.SetCurrentIndex(i)
			}
			return
		}
	}
	p.drives.SetCurrentIndex(-1)
}

func (p *Panel) driveChosen() {
	if p.updating || p.drives == nil {
		return
	}
	i := p.drives.CurrentIndex()
	if i < 0 || i >= len(p.driveLst) {
		return
	}
	p.activate()
	if p.driveLst[i] == netRootMarker {
		p.openNet("", "")
		p.tv.SetFocus()
		return
	}
	if !p.navigate(p.driveLst[i], "", true, false) {
		p.selectDrive()
	}
	p.tv.SetFocus()
}

// ── bevitel ──

func (p *Panel) onKey(key walk.Key) {
	mods := walk.ModifiersDown()
	ctrl := mods&walk.ModControl != 0
	shift := mods&walk.ModShift != 0
	alt := mods&walk.ModAlt != 0
	row := p.cursorRow()
	if alt {
		return
	}
	if p.quickKey(key) {
		return
	}
	switch {
	case key == walk.KeyTab && ctrl:
		if shift {
			p.cycleTab(-1)
		} else {
			p.cycleTab(1)
		}
	case key == walk.KeyTab && !ctrl:
		p.app.tabKey()
	case key == walk.KeyBack && !ctrl:
		p.goUp()
	case key == walk.KeyPrior && ctrl:
		p.goUp()
	case key == walk.KeyInsert && !ctrl && !shift:
		p.toggleMark(row)
		p.setCursor(row + 1)
	case key == walk.KeySpace && !ctrl && p.qsOn:
		// a szóközt a gyorskeresés kapja (WM_CHAR)
	case key == walk.KeySpace && !ctrl:
		if e := p.currentEntry(); e != nil && !e.IsParent {
			p.toggleMark(row)
			if e.IsDir && p.model.marked[e.Name] {
				p.computeDirSize(e)
			}
		}
	case shift && !ctrl && (key == walk.KeyDown || key == walk.KeyUp):
		p.toggleMark(row) // a lista maga lép tovább
	case key == walk.KeyAdd && !ctrl:
		p.app.selectPattern(true)
	case key == walk.KeySubtract && !ctrl:
		p.app.selectPattern(false)
	case key == walk.KeyMultiply:
		p.invertMarks()
	case key == walk.KeyA && ctrl:
		p.markAll()
	case key == walk.KeyDelete && !ctrl:
		p.app.deleteSelected(shift)
	case ctrl && key == walk.KeyLeft:
		p.app.openInSide(p.app.left)
	case ctrl && key == walk.KeyRight:
		p.app.openInSide(p.app.right)
	case !ctrl && key != walk.KeySpace && key != walk.KeyReturn && key != walk.KeyTab:
		// gépelés: a parancssorba kerül
		if ch := keyChar(uint32(key)); ch != "" {
			if r := []rune(ch)[0]; unicode.IsPrint(r) {
				if !p.quickTyping() { // gyorskeresésnél a karaktert a WM_CHAR hozza
					p.app.cmdInsert(ch)
				}
			}
		}
	}
}

func (p *Panel) onMouse(x, y int, button walk.MouseButton) {
	p.activate()
	if button != walk.LeftButton {
		return
	}
	mods := walk.ModifiersDown()
	row := p.tv.IndexAt(x, y)
	if row < 0 {
		// üres területre kattintva a lista elengedné a kurzort; tartsuk meg
		cur := p.cursorRow()
		p.app.mw.Synchronize(func() {
			if p.cursorRow() < 0 {
				p.setCursor(cur)
			}
		})
		return
	}
	switch {
	case mods&walk.ModControl != 0:
		p.toggleMark(row)
		p.app.mw.Synchronize(func() { p.setCursor(row) })
	case mods&walk.ModShift != 0:
		cur := p.cursorRow()
		if cur < 0 {
			cur = 0
		}
		lo, hi := cur, row
		if lo > hi {
			lo, hi = hi, lo
		}
		m := map[string]bool{}
		for k := range p.model.marked {
			m[k] = true
		}
		for i := lo; i <= hi && i < len(p.model.items); i++ {
			if e := p.model.items[i]; !e.IsParent {
				m[e.Name] = true
			}
		}
		p.setMarks(m)
		p.app.mw.Synchronize(func() { p.setCursor(row) })
	}
}

// ── ZIP / FTP ──

func (p *Panel) baseName(internal string) string {
	if i := strings.LastIndex(internal, "/"); i >= 0 {
		return internal[i+1:]
	}
	return internal
}

func (p *Panel) openArchiveAt(archive string) {
	if archiveFormat(archive) == "ext" && sevenZipExe() == "" {
		// 7-Zip nélkül a Windows nyissa meg (a CAB-ot például az Intéző is kezeli)
		shellOpen(p.app.mw.Handle(), archive, p.path)
		return
	}
	z, err := p.app.openArchiveUI(archive)
	if err == errCancel {
		return
	}
	if err != nil {
		walk.MsgBox(p.app.mw, appName, fmt.Sprintf(T("Az archívum nem nyitható meg:\n%s\n\n%s"), archive, errText(err)), walk.MsgBoxIconWarning)
		return
	}
	p.openVFS(z, z.Root())
}

// openVFS a panelen megnyit egy virtuális mappát; p.path a helyi mappa marad.
func (p *Panel) openVFS(v VFS, dir string) bool {
	defer p.syncWatch() // archívumban / távol nincs mit figyelni
	old, oldPath := p.vfs, p.vpath
	p.vfs, p.vpath = v, ""
	p.model.virtual = true
	if !p.navigateVFS(dir, "") {
		p.vfs, p.vpath = old, oldPath
		p.model.virtual = old != nil
		v.Close()
		return false
	}
	if old != nil {
		old.Close()
	}
	return true
}

func (p *Panel) listVFS(dir string) ([]*Entry, error) {
	entries, err := p.vfs.List(dir)
	if err != nil {
		return nil, err
	}
	if !p.app.showHidden && isRemoteKind(p.vfs.Kind()) {
		out := entries[:0]
		for _, e := range entries {
			if !strings.HasPrefix(e.Name, ".") {
				out = append(out, e)
			}
		}
		entries = out
	}
	return entries, nil
}

func (p *Panel) vfsParent(dir string) string {
	if parent, ok := p.vfs.Parent(dir); ok {
		if parent == "" {
			return "/" // ZIP gyökere: csak jelölő, a „..” a vpath alapján lép
		}
		return parent
	}
	return vfsExit
}

func (p *Panel) navigateVFS(dir, selectName string) bool {
	entries, err := p.listVFS(dir)
	if err != nil {
		walk.MsgBox(p.app.mw, appName, fmt.Sprintf(T("A mappa nem olvasható:\n%s\n\n%s"), p.vfs.Display(dir), errText(err)),
			walk.MsgBoxIconWarning)
		return false
	}
	if dir != p.vpath {
		p.model.marked = map[string]bool{}
		p.model.dirSizes = map[string]int64{}
	}
	p.vpath = dir
	p.model.setItems(entries, p.vfsParent(dir))
	if !p.selectName(selectName) {
		p.setCursor(0)
	}
	p.updateHeader()
	return true
}

func (p *Panel) refreshVFS() {
	cur := p.currentEntry()
	row := p.cursorRow()
	entries, err := p.listVFS(p.vpath)
	if err != nil {
		walk.MsgBox(p.app.mw, appName, fmt.Sprintf(T("A frissítés nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return
	}
	p.model.setItems(entries, p.vfsParent(p.vpath))
	if cur == nil || cur.IsParent || !p.selectName(cur.Name) {
		p.setCursor(row)
	}
	p.updateHeader()
}

// exitVFS bezárja az archívumot / kapcsolatot és visszatér a helyi mappába.
func (p *Panel) exitVFS() {
	defer p.syncWatch() // archívumban / távol nincs mit figyelni
	if p.vfs == nil {
		return
	}
	sel := ""
	if z, ok := p.vfs.(Archive); ok {
		sel = filepath.Base(z.ArchivePath())
	}
	if !p.navigate(p.path, sel, false, true) {
		home, _ := os.UserHomeDir()
		p.navigate(home, "", false, false)
	}
}

// redrawRow egyetlen sort rajzol újra (a TableView.UpdateItem itt az egész listát újrarendezné).
func (p *Panel) redrawRow(row int) {
	if p.lv == 0 {
		p.repaint()
		return
	}
	rc := win.RECT{Left: 0}                                                             // LVIR_BOUNDS
	if win.SendMessage(p.lv, 0x100E, uintptr(row), uintptr(unsafe.Pointer(&rc))) != 0 { // LVM_GETITEMRECT
		win.InvalidateRect(p.lv, &rc, false) // háttértörlés nélkül
		return
	}
	win.SendMessage(p.lv, 0x1015, uintptr(row), uintptr(row)) // LVM_REDRAWITEMS
}

// cursorRow a lista valódi kurzorsora, közvetlenül a Windows-vezérlőtől.
// (A TableView.CurrentIndex() billentyűzetes, különösen Shift+nyilas léptetéskor
// nem frissül, ezért jelölt néha rossz fájlt.)
func (p *Panel) cursorRow() int {
	if p.lv == 0 {
		return p.tv.CurrentIndex()
	}
	r := int(int32(win.SendMessage(p.lv, 0x100C, ^uintptr(0), 0x0001))) // LVM_GETNEXTITEM, LVNI_FOCUSED
	if r < 0 || r >= len(p.model.items) {
		return p.tv.CurrentIndex()
	}
	return r
}

// quickText a címke szövegét az ablak újrarendezése nélkül cseréli. A walk minden
// szövegváltáskor az egész ablakot újra elrendezi, ami kijelöléskor villogást
// okozott; a címke a panel teljes szélességét kapja, így erre nincs szükség.
var quickMax = map[*walk.Label]int{}

func quickText(l *walk.Label, s string) {
	hs := listViewField(l, "hwndStatic")
	if hs == 0 || quickMax[l] == 0 {
		quickMax[l] = 1 // az első szöveg még elrendezi; a címke ezután a panel teljes szélességét kapja
		l.SetText(s)
		return
	}
	if l.Text() == s {
		return
	}
	p, _ := syscall.UTF16PtrFromString(s)
	procSetWindowTextW.Call(uintptr(l.Handle()), uintptr(unsafe.Pointer(p)))
	procSetWindowTextW.Call(uintptr(hs), uintptr(unsafe.Pointer(p)))
	// a címkét a szülő jobb széléig nyújtjuk, hogy a hosszabb szöveg is kiférjen
	h := l.Handle()
	var rc, prc win.RECT
	win.GetWindowRect(h, &rc)
	parent := win.GetParent(h)
	dkMapWindowPoints.Call(0, uintptr(parent), uintptr(unsafe.Pointer(&rc)), 2)
	win.GetClientRect(parent, &prc)
	if w := prc.Right - rc.Left; w > rc.Right-rc.Left {
		const flags = win.SWP_NOMOVE | win.SWP_NOZORDER | win.SWP_NOACTIVATE
		win.SetWindowPos(h, 0, 0, 0, w, rc.Bottom-rc.Top, flags)
		win.SetWindowPos(hs, 0, 0, 0, w, rc.Bottom-rc.Top, flags)
	}
	win.InvalidateRect(hs, nil, true)
}

var procSetWindowTextW = user32.NewProc("SetWindowTextW")

// repaint a listát háttértörlés nélkül rajzolja újra (a walk Invalidate törölne, ami villog).
func (p *Panel) repaint() {
	if p.lv == 0 {
		p.tv.Invalidate()
		return
	}
	win.InvalidateRect(p.lv, nil, false)
	if p.frozenLV != 0 {
		win.InvalidateRect(p.frozenLV, nil, false)
	}
}

var colBaseTitles = []string{T("Név"), T("Kiterj."), T("Méret"), T("Dátum"), "Attr."}

// updateSortTitles: a rendezett oszlop címében jól látható irányjel (▲ növekvő, ▼ csökkenő).
func (p *Panel) updateSortTitles() {
	if p.tv == nil {
		return
	}
	cols := p.tv.Columns()
	for i := 0; i < cols.Len() && i < len(colBaseTitles); i++ {
		t := colBaseTitles[i]
		if i == p.model.sortCol {
			if p.model.sortOrder == walk.SortDescending {
				t += " ▼"
			} else {
				t += " ▲"
			}
		}
		if cols.At(i).Title() != t {
			cols.At(i).SetTitle(t)
		}
	}
}

// rememberPos: a mappa elhagyásakor megjegyzi, melyik elemen állt a kurzor.
func (p *Panel) rememberPos() {
	if p.path == "" || p.vfs != nil {
		return
	}
	if e := p.currentEntry(); e != nil && !e.IsParent {
		if p.lastPos == nil {
			p.lastPos = map[string]string{}
		}
		p.lastPos[strings.ToLower(p.path)] = e.Name
	}
}
