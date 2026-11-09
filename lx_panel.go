//go:build linux

package main

// Egy fájlpanel Linuxon: meghajtó-/csatolásválasztó, útvonalsáv, oszlopfejléc,
// fájllista és állapotsor; navigáció, kijelölés, rendezés, archívum/FTP nézet.

import (
	"bufio"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type Panel struct {
	app  *App
	side string

	path        string
	back, fwd   []string
	items       []*Entry
	marked      map[string]bool
	dirSizes    map[string]int64
	cursor      int
	sortCol     int
	sortDesc    bool
	dirStamp    time.Time
	watch       *dirWatch // változásfigyelés
	watchPath   string
	watchFailed bool
	pollTick    int
	vfs         VFS
	vpath       string
	kpRune      rune
	dropRow     int
	dragging    bool
	dragStart   int

	// jobb gombos kijelölés
	rsTimer *time.Timer
	rsRow   int
	rsDown  bool // jobb gomb lenyomva (jelölő húzás)
	rsMark  bool // a húzás ezt az állapotot adja a soroknak

	// lapfülek, gyorskeresés
	tabs   []tabState
	tabIdx int
	tabBar *fyne.Container
	qs     string
	qsOn   bool

	// nézet, gyorsnézet
	viewMode int
	btnDisc  *widget.Button // távoli kapcsolat bontása
	colHead  *fyne.Container
	qvBox    *fyne.Container

	// felület
	root     fyne.CanvasObject
	drives   *widget.Select
	free     *widget.Label
	headBg   *canvas.Rectangle
	pathText *canvas.Text
	view     *fileView
	header   []*headerCell
	status   *widget.Label
	driveMap map[string]string
	quietSel bool
}

func newPanel(a *App, side string) *Panel {
	p := &Panel{app: a, side: side, marked: map[string]bool{}, dirSizes: map[string]int64{}, dropRow: -1}
	p.sortCol = a.settingInt(side+".sort", colName)
	p.sortDesc = a.settingBool(side+".sortDesc", false)
	if m := a.settingInt(side+".view", viewDetail); m >= viewDetail && m <= viewThumbs {
		p.viewMode = m
	}
	p.build()
	return p
}

func (p *Panel) build() {
	p.free = widget.NewLabel("")
	p.free.Truncation = fyne.TextTruncateEllipsis
	p.drives = widget.NewSelect(nil, func(s string) {
		if p.quietSel {
			return
		}
		if path, ok := p.driveMap[s]; ok {
			if dev, isDev := strings.CutPrefix(path, "dev:"); isDev { // nem csatolt eszköz
				p.mountAndOpen(dev)
				return
			}
			p.exitVFS()
			p.navigate(path, "", true)
			p.focus()
		}
	})
	p.populateDrives()
	btnRoot := widget.NewButton("/", func() { p.goRoot(); p.focus() })
	btnUp := widget.NewButton("..", func() { p.goUp(); p.focus() })
	p.btnDisc = widget.NewButtonWithIcon(T("Kapcsolat bontása"), theme.LogoutIcon(), func() {
		p.app.setActive(p)
		p.exitVFS()
		p.focus()
	})
	p.btnDisc.Hide()
	top := container.NewBorder(nil, nil, p.drives, container.NewHBox(p.btnDisc, btnRoot, btnUp), p.free)

	p.headBg = canvas.NewRectangle(color.Gray{200})
	p.pathText = canvas.NewText("", color.Black)
	p.pathText.TextStyle.Bold = true
	pathBox := container.NewStack(p.headBg, container.NewPadded(p.pathText))
	pathBar := newTapBar(pathBox, func() { p.activate(); p.app.gotoPath() })

	var cells []fyne.CanvasObject
	for i := 0; i < numCols; i++ {
		h := newHeaderCell(p, i)
		p.header = append(p.header, h)
		cells = append(cells, h)
	}
	colHead := container.New(&headerLayout{p: p}, cells...)
	p.colHead = colHead
	colHead.Hidden = p.viewMode != viewDetail

	p.view = newFileView(p)
	p.status = widget.NewLabel("")
	p.status.Truncation = fyne.TextTruncateEllipsis

	p.tabBar = container.NewStack()
	p.tabBar.Hide()
	p.qvBox = container.NewStack()
	p.qvBox.Hide()
	p.root = container.NewBorder(container.NewVBox(top, p.tabBar, pathBar, colHead), p.status, nil, nil, container.NewStack(p.view, p.qvBox))
}

func (p *Panel) focus() {
	if c := p.app.win.Canvas(); c != nil && c.Focused() != p.view {
		c.Focus(p.view)
	}
}

func (p *Panel) activate() { p.app.setActive(p) }

func (p *Panel) rowHeight() float32 {
	return fyne.MeasureText("Ág", p.app.listTextSize(), fyne.TextStyle{Bold: true}).Height + 6
}

// ── meghajtók (csatolási pontok) ──

type mountInfo struct{ label, path string }

func listMounts() []mountInfo {
	home, _ := os.UserHomeDir()
	out := []mountInfo{{T("/  (rendszer)"), "/"}}
	if home != "" {
		out = append(out, mountInfo{T("~  (saját mappa)"), home})
	}
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return out
	}
	defer f.Close()
	seen := map[string]bool{"/": true, home: true}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 3 {
			continue
		}
		dev, mp, fs := fl[0], unescapeMount(fl[1]), fl[2]
		if seen[mp] {
			continue
		}
		user := strings.HasPrefix(mp, "/media/") || strings.HasPrefix(mp, "/run/media/") || strings.HasPrefix(mp, "/mnt/")
		gvfs := strings.Contains(mp, "/gvfs/") || strings.HasSuffix(mp, "/gvfs")
		real := strings.HasPrefix(dev, "/dev/") && !strings.HasPrefix(dev, "/dev/loop")
		switch fs {
		case "nfs", "nfs4", "cifs", "smb3", "sshfs", "fuse.sshfs", "fuse.rclone", "davfs":
			real = true
		}
		if !(user || (real && !systemMount(mp)) || gvfs) {
			continue
		}
		seen[mp] = true
		label := mp
		if user {
			label = filepath.Base(mp) + "  (" + mp + ")"
		}
		out = append(out, mountInfo{label, mp})
	}
	return out
}

func systemMount(mp string) bool {
	if mp == "/" {
		return true
	}
	for _, pre := range []string{"/boot", "/efi", "/var", "/usr", "/etc", "/sysroot", "/opt", "/snap", "/proc", "/sys", "/dev", "/run", "/tmp", "/srv", "/root", "/nix", "/gnu", "/home", "/recovery"} {
		if mp == pre || strings.HasPrefix(mp, pre+"/") {
			return true
		}
	}
	return false
}

func unescapeMount(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

func (p *Panel) populateDrives() {
	ms := listMounts()
	p.driveMap = map[string]string{}
	var opts []string
	for _, m := range ms {
		p.driveMap[m.label] = m.path
		opts = append(opts, m.label)
	}
	for _, d := range unmountedRemovable() {
		l := d.display()
		p.driveMap[l] = "dev:" + d.dev
		opts = append(opts, l)
	}
	p.drives.Options = opts
	p.drives.Refresh()
	p.selectDrive()
}

// mountAndOpen csatolja a cserélhető eszközt, majd megnyitja.
func (p *Panel) mountAndOpen(dev string) {
	go func() {
		mp, err := mountDevice(dev)
		fyne.Do(func() {
			p.app.refreshDrives()
			if err != nil {
				p.selectDrive()
				p.app.warn(fmt.Sprintf(T("Az eszköz csatolása nem sikerült:\n%s"), errText(err)))
				return
			}
			p.exitVFS()
			p.navigate(mp, "", true)
			p.focus()
		})
	}()
}

// selectDrive a választóban azt a csatolást mutatja, amelyiken a mappa van.
func (p *Panel) selectDrive() {
	best, bestLen := "", -1
	for label, mp := range p.driveMap {
		if (p.path == mp || strings.HasPrefix(p.path, strings.TrimSuffix(mp, "/")+"/")) && len(mp) > bestLen {
			best, bestLen = label, len(mp)
		}
	}
	p.quietSel = true
	p.drives.SetSelected(best)
	p.quietSel = false
}

// ── navigáció ──

func (p *Panel) navigate(path, selectName string, addHistory bool) bool {
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		path = home + path[1:]
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		p.app.warn(fmt.Sprintf(T("Hibás útvonal:\n%s"), path))
		return false
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		if err == nil && !st.IsDir() { // fájlra mutat: a mappájába lépünk, és rááll
			return p.navigate(filepath.Dir(abs), filepath.Base(abs), addHistory)
		}
		p.app.warn(fmt.Sprintf(T("A mappa nem nyitható meg:\n%s\n\n%s"), abs, errText(err)))
		return false
	}
	items, err := readLocal(abs, p.app.showHidden)
	if err != nil {
		p.app.warn(fmt.Sprintf(T("A mappa nem olvasható:\n%s\n\n%s"), abs, errText(err)))
		return false
	}
	if addHistory && p.path != "" && p.path != abs {
		p.back = append(p.back, p.path)
		p.fwd = nil
	}
	changed := p.path != abs
	p.path = abs
	p.dirStamp = st.ModTime()
	p.syncWatch()
	if changed {
		p.marked = map[string]bool{}
		p.dirSizes = map[string]int64{}
	}
	p.setItems(items, selectName)
	p.updateHeader()
	return true
}

func (p *Panel) setItems(items []*Entry, selectName string) {
	var keep string
	oldIdx := p.cursor
	if selectName == "" && p.cursor >= 0 && p.cursor < len(p.items) {
		keep = p.items[p.cursor].Name
	}
	p.sortItems(items)
	p.items = items
	// a már nem létező elemek jelölését elengedjük
	if len(p.marked) > 0 {
		have := map[string]bool{}
		for _, e := range items {
			have[e.Name] = true
		}
		for k := range p.marked {
			if !have[k] {
				delete(p.marked, k)
			}
		}
	}
	idx := 0
	want := selectName
	if want == "" {
		want = keep
	}
	if want != "" {
		found := false
		for i, e := range items {
			if e.Name == want {
				idx, found = i, true
				break
			}
		}
		if !found && selectName == "" && keep != "" {
			idx = oldIdx // a kurzor alatti elem eltűnt (törlés): a helyén marad
		}
	}
	if idx >= len(items) {
		idx = len(items) - 1
	}
	if idx < 0 {
		idx = 0
	}
	p.cursor = idx
	p.view.refreshAll()
	p.view.ensureVisible(idx)
	if selectName != "" || keep == "" {
		p.scrollCursorIntoView()
	}
	p.updateStatus()
	if p.app.active == p {
		p.app.scheduleQuickView()
	}
}

func (p *Panel) scrollCursorIntoView() {
	p.view.ensureVisible(p.cursor)
}

func readLocal(dir string, showHidden bool) ([]*Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*Entry
	if dir != "/" {
		out = append(out, &Entry{Name: "..", Path: filepath.Dir(dir), IsDir: true, IsParent: true})
	}
	for _, d := range des {
		name := d.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		info, err := d.Info()
		if err != nil {
			continue
		}
		isDir := info.IsDir()
		mode := info.Mode()
		size := info.Size()
		if mode&os.ModeSymlink != 0 {
			if st, err := os.Stat(full); err == nil {
				isDir = st.IsDir()
				if !isDir {
					size = st.Size()
				}
			}
		}
		e := newEntry(name, full, isDir, size, info.ModTime())
		e.Attr = uint32(mode.Perm())
		if mode&os.ModeSymlink != 0 {
			e.Attr |= 1 << 31
		}
		out = append(out, e)
	}
	return out, nil
}

func (p *Panel) refresh() {
	if p.vfs != nil {
		p.refreshVFS()
		return
	}
	if p.path == "" {
		return
	}
	if _, err := os.Stat(p.path); err != nil { // a mappa eltűnt: a legközelebbi létező szülő
		d := p.path
		for {
			d = filepath.Dir(d)
			if _, err := os.Stat(d); err == nil || d == "/" {
				break
			}
		}
		p.navigate(d, "", false)
		return
	}
	items, err := readLocal(p.path, p.app.showHidden)
	if err != nil {
		return
	}
	if st, err := os.Stat(p.path); err == nil {
		p.dirStamp = st.ModTime()
	}
	p.setItems(items, "")
	p.updateHeader()
}

// checkChanged: ha a mappa módosult (más program írt bele), frissít.
func (p *Panel) checkChanged() {
	p.syncWatch()
	if p.vfs != nil || p.path == "" {
		return
	}
	if p.watch != nil && !p.watch.remote() {
		if p.pollTick++; p.pollTick%watchPollEvery != 0 {
			return
		}
	}
	st, err := os.Stat(p.path)
	if err != nil || !st.ModTime().Equal(p.dirStamp) {
		p.refresh()
	}
}

func (p *Panel) goUp() {
	if p.vfs != nil {
		if parent, ok := p.vfs.Parent(p.vpath); ok {
			cur := p.vpath
			p.navigateVFS(parent, p.baseName(cur))
		} else {
			p.exitVFS()
		}
		return
	}
	if p.path == "/" {
		return
	}
	p.navigate(filepath.Dir(p.path), filepath.Base(p.path), true)
}

func (p *Panel) goRoot() {
	if p.vfs != nil {
		p.navigateVFS(p.vfs.Root(), "")
		return
	}
	p.navigate("/", "", true)
}

func (p *Panel) goBack() {
	if len(p.back) == 0 {
		return
	}
	d := p.back[len(p.back)-1]
	p.back = p.back[:len(p.back)-1]
	cur := p.path
	if p.navigate(d, "", false) {
		p.fwd = append(p.fwd, cur)
	}
}

func (p *Panel) goForward() {
	if len(p.fwd) == 0 {
		return
	}
	d := p.fwd[len(p.fwd)-1]
	p.fwd = p.fwd[:len(p.fwd)-1]
	cur := p.path
	if p.navigate(d, "", false) {
		p.back = append(p.back, cur)
	}
}

func (p *Panel) currentEntry() *Entry {
	if p.cursor >= 0 && p.cursor < len(p.items) {
		return p.items[p.cursor]
	}
	return nil
}

// selectedEntries: a jelölt elemek, vagy ha nincs jelölés, a kurzor alatti.
func (p *Panel) selectedEntries() []*Entry {
	var out []*Entry
	if len(p.marked) > 0 {
		for _, e := range p.items {
			if !e.IsParent && p.marked[e.Name] {
				out = append(out, e)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if e := p.currentEntry(); e != nil && !e.IsParent {
		return []*Entry{e}
	}
	return nil
}

func (p *Panel) openCurrent() {
	e := p.currentEntry()
	if e == nil {
		return
	}
	if e.IsParent {
		p.goUp()
		return
	}
	if p.vfs != nil {
		if e.IsDir {
			p.navigateVFS(e.Path, "")
			return
		}
		p.app.openVirtual(p, e, false)
		return
	}
	if e.IsDir {
		p.navigate(e.Path, "", true)
		return
	}
	if isArchive(e.Name) {
		p.openArchiveAt(e.Path)
		return
	}
	p.app.openFile(e.Path)
}

// ── sorok rajzolása ──

func (p *Panel) updateRow(id int, r *fileRow) {
	r.idx = id
	if id < 0 || id >= len(p.items) {
		return
	}
	e := p.items[id]
	l := &p.app.look
	marked := !e.IsParent && p.marked[e.Name]
	fg := l.ListFont.Color
	st := fyne.TextStyle{Bold: l.ListFont.Bold, Italic: l.ListFont.Italic}
	var bg color.Color = color.Transparent
	if !marked {
		if c, ok := p.app.typeColorOf(e); ok {
			fg = color.NRGBAModel.Convert(c).(color.NRGBA)
		}
	}
	if marked {
		fg = l.MarkFont.Color
		st = fyne.TextStyle{Bold: l.MarkFont.Bold, Italic: l.MarkFont.Italic}
		if l.MarkBg != l.ListBg {
			bg = l.MarkBg
		}
	}
	switch {
	case id == p.dropRow:
		bg = l.Drop
	case id == p.cursor:
		if p.app.active == p {
			bg, fg = l.CursorBg, l.CursorFg
		} else {
			bg, fg = l.CursorInBg, l.CurInFg
		}
		if marked {
			fg = l.MarkFont.Color
		}
	}
	r.bg.FillColor = bg
	size := p.app.listTextSize()
	name, ext := e.Name, e.Ext
	if e.IsDir {
		ext = ""
	} else if ext != "" {
		name = e.Stem()
	}
	r.full = [numCols]string{name, ext, p.sizeText(e), dateText(e), attrText(e)}
	r.nameFull = e.Name
	if e.IsParent {
		r.full = [numCols]string{"..", "", T("<FEL>"), "", ""}
		r.nameFull = ".."
	}
	for _, c := range r.cells {
		c.Color = fg
		c.TextSize = size
		c.TextStyle = st
	}
	r.icon.Resource = iconFor(e)
	p.applyThumb(r, e)
	r.icon.Refresh()
	r.Refresh()
}

func (p *Panel) sizeText(e *Entry) string {
	if e.IsDir {
		if s, ok := p.dirSizes[e.Name]; ok {
			if s < 0 {
				return T("…")
			}
			return fmtSize(s)
		}
		return "<DIR>"
	}
	return fmtSize(e.Size)
}

func dateText(e *Entry) string {
	if e.ModTime.IsZero() {
		return ""
	}
	return e.ModTime.Format("2006.01.02 15:04")
}

func attrText(e *Entry) string {
	if e.Attr == 0 {
		return "" // archívumban, FTP-n nincs jogosultság-információ
	}
	s := os.FileMode(e.Attr & 0o777).String()[1:]
	if e.Attr&(1<<31) != 0 {
		s = "l" + s[1:]
	}
	return s
}

func (p *Panel) refreshRow(i int) {
	if i >= 0 && i < len(p.items) {
		p.view.refreshItem(i)
	}
}

func (p *Panel) refreshCursorRows() {
	p.refreshRow(p.cursor)
}

// ── kurzor és kijelölés ──

func (p *Panel) setCursor(i int) {
	n := len(p.items)
	if n == 0 {
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}
	old := p.cursor
	p.cursor = i
	p.refreshRow(old)
	p.refreshRow(i)
	p.view.ensureVisible(i)
	if p.app.active == p {
		p.app.scheduleQuickView()
	}
}

func (p *Panel) toggleMark(i int) {
	if i < 0 || i >= len(p.items) || p.items[i].IsParent {
		return
	}
	n := p.items[i].Name
	if p.marked[n] {
		delete(p.marked, n)
	} else {
		p.marked[n] = true
	}
	p.refreshRow(i)
	p.updateStatus()
}

func (p *Panel) setMark(i int, on bool) {
	if i < 0 || i >= len(p.items) || p.items[i].IsParent || p.marked[p.items[i].Name] == on {
		return
	}
	p.toggleMark(i)
}

func (p *Panel) setMarks(m map[string]bool) {
	p.marked = m
	p.view.refreshAll()
	p.updateStatus()
}

func (p *Panel) markAll() {
	m := map[string]bool{}
	for _, e := range p.items {
		if !e.IsParent {
			m[e.Name] = true
		}
	}
	p.setMarks(m)
}

func (p *Panel) invertMarks() {
	m := map[string]bool{}
	for _, e := range p.items {
		if !e.IsParent && !e.IsDir && !p.marked[e.Name] {
			m[e.Name] = true
		} else if e.IsDir && p.marked[e.Name] {
			m[e.Name] = true
		}
	}
	p.setMarks(m)
}

func (p *Panel) markPattern(patterns []string, add bool) {
	m := map[string]bool{}
	for k := range p.marked {
		m[k] = true
	}
	for _, e := range p.items {
		if e.IsParent {
			continue
		}
		for _, pat := range patterns {
			if ok, _ := filepath.Match(strings.ToLower(pat), strings.ToLower(e.Name)); ok {
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

func (p *Panel) selectName(name string) {
	for i, e := range p.items {
		if e.Name == name {
			p.setCursor(i)
			return
		}
	}
}

func (p *Panel) updateStatus() {
	var files, dirs, mf, md int
	var sel, total int64
	for _, e := range p.items {
		if e.IsParent {
			continue
		}
		m := p.marked[e.Name]
		if e.IsDir {
			dirs++
			if m {
				md++
				if s := p.dirSizes[e.Name]; s > 0 {
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
	p.status.SetText(fmt.Sprintf(T("%s / %s kijelölve,  %s,  %s"),
		fmtHuman(uint64(sel)), fmtHuman(uint64(total)),
		fmt.Sprintf(Tn(files, "%d / %d fájl"), mf, files), fmt.Sprintf(Tn(dirs, "%d / %d mappa"), md, dirs)))
}

func (p *Panel) updateHeader() {
	if p.btnDisc != nil {
		if p.vfs != nil && isRemoteKind(p.vfs.Kind()) {
			p.btnDisc.Show()
		} else {
			p.btnDisc.Hide()
		}
	}
	if p.vfs != nil {
		p.pathText.Text = p.vfs.Display(p.vpath)
		p.free.SetText(p.vfs.Info())
	} else {
		p.pathText.Text = p.path
		var st syscall.Statfs_t
		if err := syscall.Statfs(p.path, &st); err == nil {
			free := uint64(st.Bavail) * uint64(st.Bsize)
			tot := uint64(st.Blocks) * uint64(st.Bsize)
			p.free.SetText(fmt.Sprintf(T("%s szabad / %s"), fmtHuman(free), fmtHuman(tot)))
		} else {
			p.free.SetText("")
		}
		p.selectDrive()
	}
	p.pathText.Refresh()
	if p.app.active == p {
		p.app.updateCmdLabel()
	}
	p.renderTabs()
}

// setActive az útvonalsáv színét állítja (aktív / inaktív panel).
func (p *Panel) setActive(on bool) {
	l := &p.app.look
	if on {
		p.headBg.FillColor, p.pathText.Color = l.HeadBg, l.HeadFg
	} else {
		p.headBg.FillColor, p.pathText.Color = l.HeadInBg, l.HeadInFg
	}
	p.pathText.TextSize = p.app.textSize()
	p.headBg.Refresh()
	p.pathText.Refresh()
	p.refreshCursorRows()
}

func (p *Panel) applyLook() {
	p.view.bg.FillColor = p.app.look.ListBg
	p.view.bg.Refresh()
	for _, h := range p.header {
		h.Refresh()
	}
	p.view.refreshAll()
	p.setActive(p.app.active == p)
}

// ── rendezés ──

func (p *Panel) sortBy(col int) {
	if p.sortCol == col {
		p.sortDesc = !p.sortDesc
	} else {
		p.sortCol, p.sortDesc = col, false
	}
	if col == colAttr {
		p.sortCol = colName
	}
	p.app.settings.Put(p.side+".sort", fmt.Sprint(p.sortCol))
	p.app.settings.Put(p.side+".sortDesc", boolStr(p.sortDesc))
	for _, h := range p.header {
		h.Refresh()
	}
	p.setItems(p.items, "")
}

func (p *Panel) sortItems(items []*Entry) {
	col, desc := p.sortCol, p.sortDesc
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.IsParent != b.IsParent {
			return a.IsParent
		}
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		less := func(x, y *Entry) bool {
			switch col {
			case colExt:
				if c := strings.Compare(strings.ToLower(x.Ext), strings.ToLower(y.Ext)); c != 0 {
					return c < 0
				}
			case colSize:
				if x.Size != y.Size {
					return x.Size < y.Size
				}
			case colDate:
				if !x.ModTime.Equal(y.ModTime) {
					return x.ModTime.Before(y.ModTime)
				}
			}
			return naturalLess(x.Name, y.Name)
		}
		if desc {
			return less(b, a)
		}
		return less(a, b)
	})
}

// naturalLess: kis- és nagybetű nélkül, a számokat számként hasonlítva (fájl2 < fájl10).
func naturalLess(a, b string) bool {
	ra, rb := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		ca, cb := ra[i], rb[j]
		if unicode.IsDigit(ca) && unicode.IsDigit(cb) {
			si := i
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			sj := j
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na := strings.TrimLeft(string(ra[si:i]), "0")
			nb := strings.TrimLeft(string(rb[sj:j]), "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ca != cb {
			return collate(ca) < collate(cb)
		}
		i++
		j++
	}
	if len(ra)-i != len(rb)-j {
		return len(ra)-i < len(rb)-j
	}
	return a < b
}

// collate: az ékezetes betűk az ékezet nélküli párjuk mellé kerülnek (á az a után).
func collate(r rune) float64 {
	if base, ok := accentBase[r]; ok {
		return float64(base) + 0.5
	}
	return float64(r)
}

var accentBase = map[rune]rune{'á': 'a', 'é': 'e', 'í': 'i', 'ó': 'o', 'ö': 'o', 'ő': 'o', 'ú': 'u', 'ü': 'u', 'ű': 'u'}

// ── egér ──

func (p *Panel) rowMouseDown(idx int, ev *desktop.MouseEvent) {
	p.activate()
	p.focus()
	if idx < 0 || idx >= len(p.items) {
		return
	}
	switch ev.Button {
	case desktop.MouseButtonPrimary:
		p.dragStart = idx
		switch {
		case ev.Modifier&fyne.KeyModifierControl != 0:
			p.toggleMark(idx)
			p.setCursor(idx)
		case ev.Modifier&fyne.KeyModifierShift != 0:
			lo, hi := p.cursor, idx
			if lo > hi {
				lo, hi = hi, lo
			}
			for i := lo; i <= hi; i++ {
				p.setMark(i, true)
			}
			p.setCursor(idx)
		default:
			p.setCursor(idx)
		}
	case desktop.MouseButtonSecondary:
		if p.app.rightSelect() {
			p.toggleMark(idx)
			p.setCursor(idx)
			p.rsRow = idx
			p.rsDown = true
			p.rsMark = p.marked[p.items[idx].Name]
			pos := ev.AbsolutePosition
			if p.rsTimer != nil {
				p.rsTimer.Stop()
			}
			p.rsTimer = time.AfterFunc(time.Second, func() {
				fyne.Do(func() {
					p.rsTimer = nil
					p.setMark(p.rsRow, true)
					p.showContextMenu(pos)
				})
			})
		} else {
			p.setCursor(idx)
		}
	case desktop.MouseButtonTertiary:
		// középső gomb: nincs külön szerepe
	}
}

func (p *Panel) rowMouseUp(idx int, ev *desktop.MouseEvent) {
	if ev.Button == desktop.MouseButtonSecondary {
		p.rsDown = false
		if p.rsTimer != nil {
			p.rsTimer.Stop()
			p.rsTimer = nil
		}
	}
}

// rowHover: jobb gombot nyomva tartva a sorokon áthúzva azok is megjelölődnek
// (vagy a jelölés lekerül róluk, ha az első soron levettük), mint Windowson.
func (p *Panel) rowHover(idx int, ev *desktop.MouseEvent) {
	if !p.rsDown || ev.Button != desktop.MouseButtonSecondary || idx < 0 || idx >= len(p.items) || idx == p.cursor {
		if p.rsDown && ev.Button != desktop.MouseButtonSecondary {
			p.rsDown = false // a gombot a listán kívül engedték fel
		}
		return
	}
	if p.rsTimer != nil { // húzás: nem kell helyi menü
		p.rsTimer.Stop()
		p.rsTimer = nil
	}
	lo, hi := p.cursor, idx // gyors mozgásnál a kihagyott sorok is
	if lo > hi {
		lo, hi = hi, lo
	}
	for i := lo; i <= hi; i++ {
		if !p.items[i].IsParent {
			p.setMark(i, p.rsMark)
		}
	}
	p.setCursor(idx)
}

func (p *Panel) showContextMenu(pos fyne.Position) {
	a := p.app
	m := fyne.NewMenu("",
		fyne.NewMenuItem(T("Megnyitás"), p.openCurrent),
		fyne.NewMenuItem(T("Nézőke (F3)"), a.viewFile),
		fyne.NewMenuItem(T("Szerkesztés (F4)"), a.editFile),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(T("Másolás (F5)"), func() { a.transfer(false) }),
		fyne.NewMenuItem(T("Mozgatás (F6)"), func() { a.transfer(true) }),
		fyne.NewMenuItem(T("Átnevezés (Shift+F6)"), a.rename),
		fyne.NewMenuItem(T("Törlés a Kukába (F8)"), func() { a.deleteSelected(false) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(T("Új mappa (F7)"), a.mkdir),
		fyne.NewMenuItem(T("Új fájl (Shift+F4)"), a.newFile),
		fyne.NewMenuItem(T("Útvonal másolása"), a.copyPaths),
		fyne.NewMenuItem(T("Megnyitás a fájlkezelőben"), func() { a.openFile(p.path) }),
		fyne.NewMenuItem(T("Tulajdonságok"), a.properties),
	)
	widget.ShowPopUpMenuAtPosition(m, a.win.Canvas(), pos)
}

// ── húzás a másik panelre ──

func (p *Panel) dragMove(idx int, ev *fyne.DragEvent) {
	if !p.dragging {
		p.dragging = true
		if p.dragStart >= 0 && p.dragStart < len(p.items) && p.items[p.dragStart].IsParent {
			p.dragging = false
			return
		}
	}
	o := p.app.other(p)
	o.setDropRowAt(ev.AbsolutePosition)
	p.app.lastDragPos = ev.AbsolutePosition
}

func (p *Panel) dragEnd() {
	if !p.dragging {
		return
	}
	p.dragging = false
	o := p.app.other(p)
	row := o.dropRow
	o.clearDropRow()
	pos := p.app.lastDragPos
	if !o.contains(pos) {
		return
	}
	if p.dragStart >= 0 && p.dragStart < len(p.items) && !p.marked[p.items[p.dragStart].Name] {
		p.setCursor(p.dragStart)
		if len(p.marked) > 0 { // nem jelölt elemet húzott: csak azt
			p.marked = map[string]bool{}
			p.view.refreshAll()
		}
	}
	target := ""
	if row >= 0 && row < len(o.items) && o.items[row].IsDir && o.vfs == nil {
		target = o.items[row].Path
	}
	move := p.app.fa.Driver().(desktop.Driver).CurrentKeyModifiers()&fyne.KeyModifierShift != 0
	p.app.transferTo(p, o, target, move, true)
}

// contains: az abszolút pont a panel listáján belül van-e.
func (p *Panel) contains(pos fyne.Position) bool {
	vp := fyne.CurrentApp().Driver().AbsolutePositionForObject(p.view)
	s := p.view.Size()
	return pos.X >= vp.X && pos.Y >= vp.Y && pos.X < vp.X+s.Width && pos.Y < vp.Y+s.Height
}

func (p *Panel) setDropRowAt(pos fyne.Position) {
	row := -1
	if p.contains(pos) {
		r := p.indexAt(pos)
		if r >= 0 && r < len(p.items) && p.items[r].IsDir && !p.items[r].IsParent {
			row = r
		}
	}
	if row != p.dropRow {
		old := p.dropRow
		p.dropRow = row
		p.refreshRow(old)
		p.refreshRow(row)
	}
}

func (p *Panel) clearDropRow() {
	old := p.dropRow
	p.dropRow = -1
	p.refreshRow(old)
}

// ── billentyűzet ──

func (p *Panel) mods() fyne.KeyModifier {
	if d, ok := p.app.fa.Driver().(desktop.Driver); ok {
		return d.CurrentKeyModifiers()
	}
	return 0
}

func (p *Panel) typedKey(ev *fyne.KeyEvent) {
	if p.quickKey(ev) {
		return
	}
	if p.app.userKey(comboOf(ev.Name, p.mods())) {
		return
	}
	a := p.app
	shift := p.mods()&fyne.KeyModifierShift != 0
	cols := p.view.columns()
	page := p.view.visibleRows() - 1
	if page < 1 {
		page = 1
	}
	page *= cols
	switch ev.Name {
	case fyne.KeyUp:
		if shift {
			p.toggleMark(p.cursor)
		}
		p.setCursor(p.cursor - cols)
	case fyne.KeyDown:
		if shift {
			p.toggleMark(p.cursor)
		}
		p.setCursor(p.cursor + cols)
	case fyne.KeyLeft:
		if cols > 1 {
			p.setCursor(p.cursor - 1)
		}
	case fyne.KeyRight:
		if cols > 1 {
			p.setCursor(p.cursor + 1)
		}
	case fyne.KeyPageUp:
		p.setCursor(p.cursor - page)
	case fyne.KeyPageDown:
		p.setCursor(p.cursor + page)
	case fyne.KeyHome:
		p.setCursor(0)
	case fyne.KeyEnd:
		p.setCursor(len(p.items) - 1)
	case fyne.KeyReturn, fyne.KeyEnter:
		if strings.TrimSpace(a.cmd.Text) != "" {
			a.runCommandLine(shift)
		} else if shift {
			a.terminal()
		} else {
			p.openCurrent()
		}
	case fyne.KeyBackspace:
		if a.cmd.Text != "" {
			r := []rune(a.cmd.Text)
			a.cmd.SetText(string(r[:len(r)-1]))
		} else {
			p.goUp()
		}
	case fyne.KeyEscape:
		a.cmd.SetText("")
	case fyne.KeyTab:
		a.other(p).focus()
	case fyne.KeyInsert:
		p.toggleMark(p.cursor)
		p.setCursor(p.cursor + 1)
	case fyne.KeyDelete:
		a.deleteSelected(shift)
	case fyne.KeyF1:
		a.help()
	case fyne.KeyF2:
		if shift {
			a.compareDirs()
		} else {
			a.refreshAll()
		}
	case fyne.KeyF3:
		a.viewFile()
	case fyne.KeyF4:
		if shift {
			a.newFile()
		} else {
			a.editFile()
		}
	case fyne.KeyF5:
		a.transfer(false)
	case fyne.KeyF6:
		if shift {
			a.rename()
		} else {
			a.transfer(true)
		}
	case fyne.KeyF7:
		a.mkdir()
	case fyne.KeyF8:
		a.deleteSelected(shift)
	case fyne.KeyF9:
		a.terminal()
	case fyne.KeyF10:
		if shift {
			p.showContextMenu(p.cursorScreenPos())
		} else {
			a.quit()
		}
	case desktop.KeyMenu:
		p.showContextMenu(p.cursorScreenPos())
	}
}

func (p *Panel) cursorScreenPos() fyne.Position {
	vp := fyne.CurrentApp().Driver().AbsolutePositionForObject(p.view)
	y := float32(p.cursor+1)*p.rowHeight() - p.view.scrollOffset()
	return fyne.NewPos(vp.X+40, vp.Y+y)
}

func (p *Panel) typedRune(r rune) {
	a := p.app
	if p.kpRune != 0 && r == p.kpRune {
		p.kpRune = 0
		switch r {
		case '+':
			a.selectPattern(true)
		case '-':
			a.selectPattern(false)
		case '*':
			p.invertMarks()
		}
		return
	}
	if p.qsOn {
		p.quickAdd(string(r))
		return
	}
	if a.typingSearches() && a.cmd.Text == "" && r != ' ' && unicode.IsPrint(r) {
		p.startQuickSearch(string(r))
		return
	}
	if r == ' ' && a.cmd.Text == "" {
		if e := p.currentEntry(); e != nil && !e.IsParent {
			p.toggleMark(p.cursor)
			if e.IsDir && p.marked[e.Name] {
				p.computeDirSize(e)
			}
		}
		return
	}
	if unicode.IsPrint(r) {
		a.cmd.SetText(a.cmd.Text + string(r))
		a.cmd.CursorColumn = len([]rune(a.cmd.Text))
		a.cmd.Refresh()
	}
}

func (p *Panel) typedShortcut(s fyne.Shortcut) {
	a := p.app
	if k, ok := shortcutCombo(s); ok && a.userKey(k) {
		return
	}
	switch sc := s.(type) {
	case *fyne.ShortcutSelectAll:
		p.markAll()
	case *fyne.ShortcutCut: // Shift+Delete
		if p.mods()&fyne.KeyModifierShift != 0 {
			a.deleteSelected(true)
		}
	case *fyne.ShortcutPaste:
		if a.cmd.Text == "" {
			a.cmd.TypedShortcut(s)
		}
	case *fyne.ShortcutCopy:
		a.copyPaths()
	case *desktop.CustomShortcut:
		ctrl := sc.Modifier&fyne.KeyModifierControl != 0
		alt := sc.Modifier&fyne.KeyModifierAlt != 0
		shift := sc.Modifier&fyne.KeyModifierShift != 0
		switch {
		case ctrl && (sc.KeyName == fyne.KeyPageUp || isRootKey(sc.KeyName)):
			if sc.KeyName != fyne.KeyPageUp {
				p.goRoot()
			} else {
				p.goUp()
			}
		case ctrl && sc.KeyName == fyne.KeyPageDown:
			p.openCurrent()
		case ctrl && sc.KeyName == fyne.KeyLeft:
			a.openInPanel(a.left)
		case ctrl && sc.KeyName == fyne.KeyRight:
			a.openInPanel(a.right)
		case alt && sc.KeyName == fyne.KeyLeft:
			p.goBack()
		case alt && sc.KeyName == fyne.KeyDown:
			a.historyPopup()
		case ctrl && sc.KeyName == fyne.KeyS:
			p.startQuickSearch("")
		case ctrl && sc.KeyName == fyne.KeyTab:
			if shift {
				p.cycleTab(-1)
			} else {
				p.cycleTab(1)
			}
		case alt && sc.KeyName == fyne.KeyRight:
			p.goForward()
		case alt && sc.KeyName == fyne.KeyF7:
			a.findFiles()
		case alt && sc.KeyName == fyne.KeyF5:
			a.pack()
		case alt && sc.KeyName == fyne.KeyF9:
			a.unpack()
		case alt && sc.KeyName == fyne.KeyF1:
			p.drives.Tapped(nil)
		case ctrl && sc.KeyName == fyne.Key1:
			p.setViewMode(viewDetail)
		case ctrl && sc.KeyName == fyne.Key2:
			p.setViewMode(viewBrief)
		case ctrl && sc.KeyName == fyne.Key3:
			p.setViewMode(viewThumbs)
		case ctrl && sc.KeyName == fyne.Key4: // KDE alatt a Ctrl+F3/F4 asztalt vált
			p.sortBy(colName)
		case ctrl && sc.KeyName == fyne.Key5:
			p.sortBy(colExt)
		case ctrl && sc.KeyName == fyne.Key6:
			p.sortBy(colDate)
		case ctrl && sc.KeyName == fyne.Key7:
			p.sortBy(colSize)
		case ctrl && shift && sc.KeyName == fyne.KeyF1:
			p.setViewMode(viewThumbs)
		case ctrl && sc.KeyName == fyne.KeyF1:
			p.setViewMode(viewBrief)
		case ctrl && sc.KeyName == fyne.KeyF2:
			p.setViewMode(viewDetail)
		case ctrl && sc.KeyName == fyne.KeyF3:
			p.sortBy(colName)
		case ctrl && sc.KeyName == fyne.KeyF4:
			p.sortBy(colExt)
		case ctrl && sc.KeyName == fyne.KeyF5:
			p.sortBy(colDate)
		case ctrl && sc.KeyName == fyne.KeyF6:
			p.sortBy(colSize)
		case ctrl && shift && sc.KeyName == fyne.KeyC:
			a.copyPaths()
		case ctrl && (sc.KeyName == fyne.KeyUp || sc.KeyName == fyne.KeyDown):
			// nincs teendő
		}
	}
}

// isRootKey: Ctrl+\ (amerikai), Ctrl+Ű (magyar billentyűzeten ugyanott van), Ctrl+Home.
func isRootKey(k fyne.KeyName) bool {
	switch k {
	case fyne.KeyBackslash, fyne.KeyHome, "Ű", "ű":
		return true
	}
	return false
}

// computeDirSize a mappa méretét a háttérben számolja (Szóköz).
func (p *Panel) computeDirSize(e *Entry) {
	if p.vfs != nil {
		return
	}
	p.dirSizes[e.Name] = -1
	p.refreshRow(p.cursor)
	base, name, path := p.path, e.Name, e.Path
	go func() {
		var total int64
		filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				total += info.Size()
			}
			return nil
		})
		fyne.Do(func() {
			if p.path == base && p.vfs == nil {
				p.dirSizes[name] = total
				p.view.refreshAll()
				p.updateStatus()
			}
		})
	}()
}

// ── archívum és FTP nézet ──

func (p *Panel) baseName(internal string) string {
	if i := strings.LastIndex(strings.TrimSuffix(internal, "/"), "/"); i >= 0 {
		return strings.TrimSuffix(internal, "/")[i+1:]
	}
	return internal
}

func (p *Panel) openArchiveAt(archive string) {
	p.app.openArchiveUI(archive, func(z Archive, err error) {
		if err == errCancel {
			return
		}
		if err != nil {
			p.app.warn(fmt.Sprintf(T("Az archívum nem nyitható meg:\n%s\n\n%s"), archive, errText(err)))
			return
		}
		p.openVFS(z, "")
	})
}

func (p *Panel) openVFS(v VFS, dir string) bool {
	defer p.syncWatch() // archívumban / távol nincs mit figyelni
	items, err := v.List(dir)
	if err != nil {
		p.app.warn(fmt.Sprintf(T("Nem olvasható:\n%s"), errText(err)))
		return false
	}
	if p.vfs != nil && p.vfs != v {
		p.vfs.Close()
	}
	p.vfs = v
	p.vpath = dir
	p.marked = map[string]bool{}
	p.setItems(p.withParent(items), "")
	p.updateHeader()
	return true
}

func (p *Panel) withParent(items []*Entry) []*Entry {
	return append([]*Entry{{Name: "..", IsDir: true, IsParent: true}}, items...)
}

func (p *Panel) navigateVFS(dir, selectName string) {
	items, err := p.vfs.List(dir)
	if err != nil {
		p.app.warn(fmt.Sprintf(T("Nem olvasható:\n%s"), errText(err)))
		return
	}
	p.vpath = dir
	p.marked = map[string]bool{}
	p.setItems(p.withParent(items), selectName)
	p.updateHeader()
}

func (p *Panel) refreshVFS() {
	if r, ok := p.vfs.(interface{ load() error }); ok {
		r.load()
	}
	items, err := p.vfs.List(p.vpath)
	if err != nil {
		p.exitVFS()
		return
	}
	p.setItems(p.withParent(items), "")
	p.updateHeader()
}

func (p *Panel) exitVFS() {
	defer p.syncWatch() // archívumban / távol nincs mit figyelni
	if p.vfs == nil {
		return
	}
	v := p.vfs
	p.vfs = nil
	p.vpath = ""
	v.Close()
	sel := ""
	if x, ok := v.(Archive); ok {
		sel = filepath.Base(x.ArchivePath())
	}
	if !p.navigate(p.path, sel, false) {
		home, _ := os.UserHomeDir()
		p.navigate(home, "", false)
	}
}

func (p *Panel) kind() string {
	if p.vfs == nil {
		return "local"
	}
	return p.vfs.Kind()
}
