//go:build windows

package main

// Húzd és ejtsd a Windows OLE rendszerén keresztül:
//   – húzás a panelek között, mappára ejtve abba a mappába
//   – húzás az Intézőbe / asztalra / más programba, és onnan vissza
//   – Shift: mozgatás, jobb gombos húzás: menü (Másolás / Mozgatás / Mégse)
// Ezen kívül az egér oldalsó (vissza/előre) gombjai is működnek.

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
)

var (
	ole32    = syscall.NewLazyDLL("ole32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")

	procOleInitialize      = ole32.NewProc("OleInitialize")
	procDoDragDrop         = ole32.NewProc("DoDragDrop")
	procRegisterDragDrop   = ole32.NewProc("RegisterDragDrop")
	procReleaseStgMedium   = ole32.NewProc("ReleaseStgMedium")
	procCoCreateInstance   = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree      = ole32.NewProc("CoTaskMemFree")
	procSHParseDisplayName = shell32.NewProc("SHParseDisplayName")
	procSHCreateDataObject = shell32.NewProc("SHCreateDataObject")
	procDragQueryFileW     = shell32.NewProc("DragQueryFileW")
	procSetWindowSubclass  = comctl32.NewProc("SetWindowSubclass")
	procDefSubclassProc    = comctl32.NewProc("DefSubclassProc")
	procFindWindowExW      = user32.NewProc("FindWindowExW")
	procAppendMenuW        = user32.NewProc("AppendMenuW")
	procTrackPopupMenuEx   = user32.NewProc("TrackPopupMenuEx")
	procGetAsyncKeyState   = user32.NewProc("GetAsyncKeyState")
)

const (
	dropNone = 0
	dropCopy = 1
	dropMove = 2
	dropLink = 4

	mkLButton = 0x01
	mkRButton = 0x02
	mkShift   = 0x04
	mkControl = 0x08

	sOK                = 0
	eNoInterface       = 0x80004002
	dragDropSDrop      = 0x00040100
	dragDropSCancel    = 0x00040101
	dragDropSDefCursor = 0x00040102

	wmNotify     = 0x004E
	wmXButtonUp  = 0x020C
	lvnBeginDrag = 0xFFFFFF93 // LVN_FIRST-9
	lvnBeginRDrg = 0xFFFFFF91 // LVN_FIRST-11
	lvmScroll    = 0x1014
	lvmHitTest   = 0x1012
	cfHDrop      = 15
)

type guid struct {
	D1     uint32
	D2, D3 uint16
	D4     [8]byte
}

var (
	iidUnknown          = guid{0x00000000, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidDropSource       = guid{0x00000121, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidDropTarget       = guid{0x00000122, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidDataObject       = guid{0x0000010E, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	clsidDragDropHelper = guid{0x4657278A, 0x411B, 0x11D2, [8]byte{0x83, 0x9A, 0x00, 0xC0, 0x4F, 0xD9, 0x18, 0xD0}}
	iidDragSourceHelper = guid{0xDE5BF786, 0x477A, 0x11D2, [8]byte{0x83, 0x9D, 0x00, 0xC0, 0x4F, 0xD9, 0x18, 0xD0}}
	iidDropTargetHelper = guid{0x4657278B, 0x411B, 0x11D2, [8]byte{0x83, 0x9A, 0x00, 0xC0, 0x4F, 0xD9, 0x18, 0xD0}}
)

type formatEtc struct {
	CfFormat uint16
	Ptd      uintptr
	Aspect   uint32
	Lindex   int32
	Tymed    uint32
}

type stgMedium struct {
	Tymed         uint32
	Data          uintptr
	UnkForRelease uintptr
}

// nmHdr a C-beli NMHDR: 64 biten a végén 4 bájt kitöltéssel 24 bájt,
// ezért külön struktúra kell, különben az utána jövő mező rossz helyről olvasódik.
type nmHdr struct {
	HwndFrom uintptr
	IDFrom   uintptr
	Code     uint32
}

type nmListView struct {
	Hdr   nmHdr
	IItem int32
}

func oleInit() { procOleInitialize.Call(0) }

// comCall egy COM-metódust hív a vtábla indexe alapján.
func comCall(obj uintptr, idx int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func comRelease(obj uintptr) {
	if obj != 0 {
		comCall(obj, 2)
	}
}

func guidOf(p uintptr) guid { return *(*guid)(unsafe.Pointer(p)) }

func setPtr(pp uintptr, v uintptr) { *(*uintptr)(unsafe.Pointer(pp)) = v }

func pointFromPOINTL(pt uintptr) win.POINT {
	return win.POINT{X: int32(uint32(pt)), Y: int32(uint32(pt >> 32))}
}

// ── IDropSource ──

type dropSource struct {
	vtbl  *[5]uintptr
	right bool
}

var dropSourceVtbl = [5]uintptr{
	syscall.NewCallback(func(this, riid, ppv uintptr) uintptr {
		if g := guidOf(riid); g == iidUnknown || g == iidDropSource {
			setPtr(ppv, this)
			return sOK
		}
		setPtr(ppv, 0)
		return eNoInterface
	}),
	syscall.NewCallback(func(this uintptr) uintptr { return 1 }),
	syscall.NewCallback(func(this uintptr) uintptr { return 1 }),
	syscall.NewCallback(func(this, escape, keys uintptr) uintptr { // QueryContinueDrag
		ds := (*dropSource)(unsafe.Pointer(this))
		btn := uintptr(mkLButton)
		if ds.right {
			btn = mkRButton
		}
		if escape != 0 {
			return dragDropSCancel
		}
		if keys&btn == 0 {
			return dragDropSDrop
		}
		return sOK
	}),
	syscall.NewCallback(func(this, effect uintptr) uintptr { return dragDropSDefCursor }),
}

// shellDataObject a Windows Intéző saját adatobjektumát kéri le a fájlokhoz,
// így bármely program (Intéző, asztal, levelező) ugyanúgy fogadja, mintha onnan húznánk.
func shellDataObject(dir string, paths []string) (uintptr, func(), bool) {
	var pidls []uintptr
	free := func() {
		for _, p := range pidls {
			procCoTaskMemFree.Call(p)
		}
	}
	parse := func(p string) uintptr {
		s, err := syscall.UTF16PtrFromString(p)
		if err != nil {
			return 0
		}
		var pidl uintptr
		if r, _, _ := procSHParseDisplayName.Call(uintptr(unsafe.Pointer(s)), 0, uintptr(unsafe.Pointer(&pidl)), 0, 0); r != 0 {
			return 0
		}
		pidls = append(pidls, pidl)
		return pidl
	}
	parent := parse(dir)
	if parent == 0 {
		dlog("SHParseDisplayName sikertelen: %s", dir)
		free()
		return 0, nil, false
	}
	var rel []uintptr
	for _, p := range paths {
		if abs := parse(p); abs != 0 {
			rel = append(rel, lastItemID(abs))
		}
	}
	if len(rel) == 0 {
		free()
		return 0, nil, false
	}
	var pdo uintptr
	r, _, _ := procSHCreateDataObject.Call(parent, uintptr(len(rel)), uintptr(unsafe.Pointer(&rel[0])), 0,
		uintptr(unsafe.Pointer(&iidDataObject)), uintptr(unsafe.Pointer(&pdo)))
	runtime.KeepAlive(rel)
	if r != 0 || pdo == 0 {
		dlog("SHCreateDataObject hiba: 0x%X (elemek: %d)", r, len(rel))
		free()
		return 0, nil, false
	}
	return pdo, func() { comRelease(pdo); free() }, true
}

// lastItemID egy abszolút PIDL utolsó elemére mutat (az ILFindLastID megfelelője,
// amely nem minden Windowson érhető el név szerint).
func lastItemID(pidl uintptr) uintptr {
	last := pidl
	for p := pidl; ; {
		cb := *(*uint16)(unsafe.Pointer(p))
		if cb == 0 {
			return last
		}
		last = p
		p += uintptr(cb)
	}
}

func filesFromDataObject(pdo uintptr) []string {
	fe := formatEtc{CfFormat: cfHDrop, Aspect: 1, Lindex: -1, Tymed: 1}
	var stg stgMedium
	if comCall(pdo, 3, uintptr(unsafe.Pointer(&fe)), uintptr(unsafe.Pointer(&stg))) != sOK {
		return nil
	}
	defer procReleaseStgMedium.Call(uintptr(unsafe.Pointer(&stg)))
	n, _, _ := procDragQueryFileW.Call(stg.Data, 0xFFFFFFFF, 0, 0)
	var out []string
	for i := uintptr(0); i < n; i++ {
		l, _, _ := procDragQueryFileW.Call(stg.Data, i, 0, 0)
		buf := make([]uint16, l+1)
		procDragQueryFileW.Call(stg.Data, i, uintptr(unsafe.Pointer(&buf[0])), l+1)
		out = append(out, syscall.UTF16ToString(buf))
	}
	return out
}

func createHelper(iid *guid) uintptr {
	var obj uintptr
	r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidDragDropHelper)), 0, 1,
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&obj)))
	if r != 0 {
		return 0
	}
	return obj
}

// startDrag a lista kezdeményezésére (LVN_BEGINDRAG) indítja a húzást.
func (p *Panel) startDrag(row int, right bool) {
	if row < 0 || row >= len(p.model.items) || p.kind() == "net" {
		return
	}
	e := p.model.items[row]
	if e.IsParent {
		return
	}
	ents := []*Entry{e}
	if p.model.marked[e.Name] {
		ents = p.selectedEntries()
	}
	paths := entryPaths(ents)
	base := p.path
	if p.vfs != nil {
		// Archívumból / távoli helyről: az elemek előbb egy ideiglenes mappába kerülnek,
		// mert az Intéző és a többi program csak valódi fájlokat fogad. A másik
		// panelre ejtve a program az eredeti helyről másol (handleDrop).
		var ok bool
		if base, paths, ok = p.stageDrag(ents, right); !ok {
			return
		}
	}
	dlog("húzás indul: %d elem, jobb gomb: %v, első: %s", len(paths), right, paths[0])
	pdo, cleanup, ok := shellDataObject(base, paths)
	if !ok {
		return
	}
	defer cleanup()
	if helper := createHelper(&iidDragSourceHelper); helper != 0 {
		var pt win.POINT
		win.GetCursorPos(&pt)
		win.ScreenToClient(p.lv, &pt)
		comCall(helper, 4, uintptr(p.lv), uintptr(unsafe.Pointer(&pt)), pdo) // InitializeFromWindow
		comRelease(helper)
	}
	ds := &dropSource{vtbl: &dropSourceVtbl, right: right}
	p.app.dragSrc, p.app.dragFiles, p.app.dragEntries = p, paths, nil
	if p.vfs != nil {
		p.app.dragEntries = ents
	}
	var effect uint32
	hr, _, _ := procDoDragDrop.Call(pdo, uintptr(unsafe.Pointer(ds)), dropCopy|dropMove|dropLink, uintptr(unsafe.Pointer(&effect)))
	dlog("DoDragDrop vége: 0x%X, hatás: %d", hr, effect)
	runtime.KeepAlive(ds)
	p.app.mw.Synchronize(p.app.refreshBoth) // ha az Intéző mozgatta, lássuk
}

// ── IDropTarget ──

type dropTarget struct {
	vtbl    *[7]uintptr
	panel   *Panel
	files   []string
	right   bool
	effect  uint32
	target  string
	helper  uintptr
	allowed uint32
}

var dropTargetVtbl = [7]uintptr{
	syscall.NewCallback(func(this, riid, ppv uintptr) uintptr {
		if g := guidOf(riid); g == iidUnknown || g == iidDropTarget {
			setPtr(ppv, this)
			return sOK
		}
		setPtr(ppv, 0)
		return eNoInterface
	}),
	syscall.NewCallback(func(this uintptr) uintptr { return 1 }),
	syscall.NewCallback(func(this uintptr) uintptr { return 1 }),
	syscall.NewCallback(func(this, pdo, keys, pt, peff uintptr) uintptr { // DragEnter
		dt := (*dropTarget)(unsafe.Pointer(this))
		dt.files = filesFromDataObject(pdo)
		dt.right = keys&mkRButton != 0
		dlog("DragEnter (%s panel): %d fájl", dt.panel.side, len(dt.files))
		dt.allowed = *(*uint32)(unsafe.Pointer(peff))
		dt.over(keys, pt, peff)
		if dt.helper != 0 {
			p := pointFromPOINTL(pt)
			comCall(dt.helper, 3, uintptr(dt.panel.tv.Handle()), pdo, uintptr(unsafe.Pointer(&p)), uintptr(dt.effect))
		}
		return sOK
	}),
	syscall.NewCallback(func(this, keys, pt, peff uintptr) uintptr { // DragOver
		dt := (*dropTarget)(unsafe.Pointer(this))
		dt.allowed = *(*uint32)(unsafe.Pointer(peff))
		dt.over(keys, pt, peff)
		if dt.helper != 0 {
			p := pointFromPOINTL(pt)
			comCall(dt.helper, 5, uintptr(unsafe.Pointer(&p)), uintptr(dt.effect))
		}
		return sOK
	}),
	syscall.NewCallback(func(this uintptr) uintptr { // DragLeave
		dt := (*dropTarget)(unsafe.Pointer(this))
		dt.panel.setDropRow(-1)
		if dt.helper != 0 {
			comCall(dt.helper, 4)
		}
		dt.files = nil
		return sOK
	}),
	syscall.NewCallback(func(this, pdo, keys, pt, peff uintptr) uintptr { // Drop
		dt := (*dropTarget)(unsafe.Pointer(this))
		dt.allowed = *(*uint32)(unsafe.Pointer(peff))
		dt.over(keys|mkLButton, pt, peff)
		eff, target, files, right := dt.effect, dt.target, dt.files, dt.right
		dlog("Drop (%s panel): hatás %d, cél %s, %d fájl", dt.panel.side, eff, target, len(files))
		dt.panel.setDropRow(-1)
		if dt.helper != 0 {
			p := pointFromPOINTL(pt)
			comCall(dt.helper, 6, pdo, uintptr(unsafe.Pointer(&p)), uintptr(eff))
		}
		// A forrásnak mindig „másolást” jelzünk: a mozgatást mi végezzük el,
		// így az Intéző nem törli magától a forrásfájlokat.
		if eff == dropNone || dt.allowed&dropCopy == 0 {
			*(*uint32)(unsafe.Pointer(peff)) = dropNone
		} else {
			*(*uint32)(unsafe.Pointer(peff)) = dropCopy
		}
		dt.files = nil
		if eff != dropNone && len(files) > 0 {
			p := dt.panel
			p.app.mw.Synchronize(func() { p.app.handleDrop(p, files, target, eff == dropMove, right) })
		}
		return sOK
	}),
}

// over kiszámolja a célmappát és a hatást (másolás / mozgatás) az egér helye alapján.
func (dt *dropTarget) over(keys, pt, peff uintptr) {
	p := dt.panel
	dt.effect, dt.target = dropNone, ""
	row := -1
	if len(dt.files) > 0 {
		var target string
		target, row = p.dropTargetAt(pointFromPOINTL(pt), dt.files)
		if target != "" {
			dt.target = target
			dt.effect = dropCopy
			if keys&mkShift != 0 && keys&mkControl == 0 && !dt.right {
				dt.effect = dropMove
			}
			if dt.effect&dt.allowed == 0 {
				dt.effect = dt.allowed & (dropCopy | dropMove)
				if dt.effect == dropCopy|dropMove {
					dt.effect = dropCopy
				}
			}
		}
	}
	if dt.effect == dropNone {
		row = -1
	}
	p.setDropRow(row)
	p.autoScroll(pointFromPOINTL(pt))
	*(*uint32)(unsafe.Pointer(peff)) = dt.effect
}

// dropTargetAt: a célmappa (üres: nem lehet ide ejteni) és a kiemelendő sor.
func (p *Panel) dropTargetAt(screen win.POINT, files []string) (string, int) {
	if p.kind() == "net" {
		return "", -1 // a gépek / megosztások listájára nem lehet ejteni
	}
	if p.vfs != nil {
		if p.app.dragSrc == p {
			return "", -1 // archívumból / távoli helyről önmagára nem
		}
		return p.vfs.Display(p.vpath), -1
	}
	pt := screen
	win.ScreenToClient(p.lv, &pt)
	hti := win.LVHITTESTINFO{Pt: pt}
	row := int(int32(win.SendMessage(p.lv, lvmHitTest, 0, uintptr(unsafe.Pointer(&hti)))))
	target := p.path
	if row >= 0 && row < len(p.model.items) {
		e := p.model.items[row]
		switch {
		case e.IsParent:
			target = e.Path
		case e.IsDir && !containsFold(files, e.Path):
			target = e.Path
		default:
			row = -1
		}
	} else {
		row = -1
	}
	// ugyanabba a mappába ejtés nem csinálna semmit
	same := true
	for _, f := range files {
		if !strings.EqualFold(parentDir(f), target) {
			same = false
			break
		}
	}
	if same {
		return "", -1
	}
	return target, row
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func parentDir(p string) string {
	p = strings.TrimRight(p, `\`)
	if i := strings.LastIndex(p, `\`); i >= 0 {
		d := p[:i]
		if len(d) == 2 && d[1] == ':' {
			d += `\`
		}
		return d
	}
	return p
}

func (p *Panel) setDropRow(row int) {
	if p.dropRow == row {
		return
	}
	old := p.dropRow
	p.dropRow = row
	if old >= 0 {
		p.redrawRow(old)
	}
	if row >= 0 {
		p.redrawRow(row)
	}
}

// autoScroll húzás közben görget, ha az egér a lista tetején / alján jár.
func (p *Panel) autoScroll(screen win.POINT) {
	pt := screen
	win.ScreenToClient(p.lv, &pt)
	var rc win.RECT
	win.GetClientRect(p.lv, &rc)
	edge := int32(20)
	switch {
	case pt.Y >= 0 && pt.Y < edge+24: // a fejléc alatt
		win.SendMessage(p.lv, lvmScroll, 0, ^uintptr(15)) // -16
	case pt.Y > rc.Bottom-edge && pt.Y <= rc.Bottom:
		win.SendMessage(p.lv, lvmScroll, 0, 16)
	}
}

// ── ablak-alosztályozás: húzás indítása és az egér oldalsó gombjai ──

var subclassPanels = map[uintptr]*Panel{}

var subclassProc = syscall.NewCallback(func(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	if p := subclassPanels[id]; p != nil {
		switch {
		case msg == wmNotify && id < 10:
			nm := (*nmListView)(unsafe.Pointer(lp))
			if nm.Hdr.Code == nmCustomDrawCode && win.HWND(nm.Hdr.HwndFrom) != 0 &&
				(win.HWND(nm.Hdr.HwndFrom) == p.lv || win.HWND(nm.Hdr.HwndFrom) == p.frozenLV) {
				if p.viewMode == viewThumbs && win.HWND(nm.Hdr.HwndFrom) == p.lv {
					cd := (*nmCustomDraw)(unsafe.Pointer(lp))
					switch cd.DrawStage {
					case 0x1: // CDDS_PREPAINT
						return 0x20 // CDRF_NOTIFYITEMDRAW
					case cddsItemPrepaint:
						p.drawThumbItem(cd.Hdc, int(cd.ItemSpec))
						return 0x4 // CDRF_SKIPDEFAULT: az elemet mi rajzoltuk
					}
				}
				stripSelectionState(lp) // a kurzort mi színezzük, ne a Windows-téma
			}
			if nm.Hdr.Code == lvnBeginDrag || nm.Hdr.Code == lvnBeginRDrg {
				dlog("LVN_BEGINDRAG (%s panel) sor %d, lista %X (normál %X)", p.side, nm.IItem, nm.Hdr.HwndFrom, p.lv)
				p.startDrag(int(nm.IItem), nm.Hdr.Code == lvnBeginRDrg)
				return 0
			}
		case msg == 0x0087 && id >= 10 && id < 20: // WM_GETDLGCODE: a lista kéri a Tab-ot
			r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
			return r | 0x0002 // DLGC_WANTTAB
		case msg == 0x0087 && id == 20: // parancssor: Esc és Tab is kell
			r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
			if wp == 0x1B || wp == 0x09 {
				return r | 0x0004 // DLGC_WANTALLKEYS
			}
			return r
		case msg == 0x0102 && id >= 10 && id < 20: // WM_CHAR a listában: gyorskeresés / parancssor
			if p.quickChar(rune(wp)) {
				return 0
			}
		case msg == 0x0104 && id >= 10: // WM_SYSKEYDOWN: Alt+F billentyűk
			switch wp {
			case 0x76: // Alt+F7
				p.app.mw.Synchronize(p.app.findFiles)
				return 0
			case 0x74: // Alt+F5
				p.app.mw.Synchronize(p.app.pack)
				return 0
			case 0x78: // Alt+F9
				p.app.mw.Synchronize(p.app.unpack)
				return 0
			case 0x28: // Alt+↓: előzmények
				p.app.mw.Synchronize(p.app.historyPopup)
				return 0
			}
		case id >= 10 && id < 20 && (msg == 0x0204 || msg == 0x0206 || msg == 0x0205 || msg == 0x0200 || msg == 0x0113 || msg == 0x001F) &&
			(p.rsActive || (msg == 0x0204 || msg == 0x0206) && p.app.rightSelect()):
			if p.rightSelectMsg(win.HWND(hwnd), uint32(msg), wp, lp) {
				return 0
			}
		case msg == wmXButtonUp && id >= 10 && id < 20:
			if (wp>>16)&0xFFFF == 1 {
				p.goBack()
			} else {
				p.goForward()
			}
			return 1
		}
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
	return r
})

func findListView(parent win.HWND) win.HWND {
	cls, _ := syscall.UTF16PtrFromString("SysListView32")
	var best win.HWND
	var bestW int32 = -1
	var child uintptr
	for {
		child, _, _ = procFindWindowExW.Call(uintptr(parent), child, uintptr(unsafe.Pointer(cls)), 0)
		if child == 0 {
			break
		}
		var rc win.RECT
		win.GetClientRect(win.HWND(child), &rc)
		if w := rc.Right - rc.Left; w >= bestW { // egyenlőnél a később létrehozott (normál) lista
			best, bestW = win.HWND(child), w
		}
	}
	return best
}

// normalListView a TableView látható listavezérlője. A TableView-ban két lista van
// (a rejtett a „befagyasztott” oszlopoké), és induláskor még mindkettő 0 széles,
// ezért a walk belső mezőjéből olvassuk ki, és csak végszükségben keresünk.
func normalListView(tv interface{}, parent win.HWND) win.HWND {
	if h := listViewField(tv, "hwndNormalLV"); h != 0 {
		return h
	}
	return findListView(parent)
}

func listViewField(tv interface{}, name string) win.HWND {
	v := reflect.ValueOf(tv)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if f := v.FieldByName(name); f.IsValid() {
		switch f.Kind() {
		case reflect.Uintptr, reflect.Uint, reflect.Uint64, reflect.Uint32:
			return win.HWND(f.Uint())
		}
	}
	return 0
}

// setupMouse a panel létrehozása után köti be a húzd és ejtsd-et.
func (p *Panel) setupMouse(id uintptr) {
	hrOle, _, _ := procOleInitialize.Call(0) // már a GUI szálán
	p.dropRow = -1
	p.lv = normalListView(p.tv, p.tv.Handle())
	frozen := listViewField(p.tv, "hwndFrozenLV")
	p.frozenLV = frozen
	dlog("%s panel: OleInitialize 0x%X, tábla %X, lista %X, rejtett lista %X", p.side, hrOle, p.tv.Handle(), p.lv, frozen)
	if p.lv == 0 {
		return
	}
	subclassPanels[id] = p
	subclassPanels[id+10] = p
	r1, _, _ := procSetWindowSubclass.Call(uintptr(p.tv.Handle()), subclassProc, id, 0)
	r2, _, _ := procSetWindowSubclass.Call(uintptr(p.lv), subclassProc, id+10, 0)
	dt := &dropTarget{vtbl: &dropTargetVtbl, panel: p, helper: createHelper(&iidDropTargetHelper)}
	p.dropTgt = dt
	for _, h := range []win.HWND{p.tv.Handle(), p.lv, frozen} {
		if h != 0 {
			hr, _, _ := procRegisterDragDrop.Call(uintptr(h), uintptr(unsafe.Pointer(dt)))
			dlog("  RegisterDragDrop(%X) = 0x%X", h, hr)
		}
	}
	dlog("  SetWindowSubclass: tábla %d, lista %d; ejtés-segéd: %v", r1, r2, dt.helper != 0)
}

// popupChoice egyszerű előugró menü a kurzornál; a választott azonosítót adja (0: mégse).
func popupChoice(owner win.HWND, items []string) int {
	h := win.CreatePopupMenu()
	if h == 0 {
		return 0
	}
	defer win.DestroyMenu(h)
	for i, t := range items {
		if t == "-" {
			procAppendMenuW.Call(uintptr(h), 0x800, 0, 0)
			continue
		}
		s, _ := syscall.UTF16PtrFromString(t)
		procAppendMenuW.Call(uintptr(h), 0, uintptr(i+1), uintptr(unsafe.Pointer(s)))
	}
	var pt win.POINT
	win.GetCursorPos(&pt)
	win.SetForegroundWindow(owner)
	r, _, _ := procTrackPopupMenuEx.Call(uintptr(h), uintptr(win.TPM_RETURNCMD|win.TPM_RIGHTBUTTON),
		uintptr(pt.X), uintptr(pt.Y), uintptr(owner), 0)
	return int(r)
}

// ── kijelölés jobb egérgombbal (klasszikus kétpaneles mód): nyomásra a sor kijelölése vált,
// lenyomva tartva húzással több sor; 1 mp mozdulatlan nyomvatartás után helyi menü ──

const (
	rsTimer    = 0xD1
	rsBarTimer = 0xD2 // a nyomva tartás jelzősávjának frissítése
)

// drawHoldBar: jobb gombos kijelölésnél a nyomva tartott sor alján egy sáv telik meg
// a helyi menü megjelenéséig (0,2 másodperc után indul, hogy egy rövid kattintásnál
// ne villanjon fel). Közvetlenül a listára rajzol; ha a sor újrarajzolódik, a
// következő lépés pótolja.
func (p *Panel) drawHoldBar(lv win.HWND) {
	if !p.rsActive || p.rsMoved || p.rsStart < 0 {
		p.stopHoldBar(lv)
		return
	}
	f := (float64(time.Since(p.rsT0)) - float64(200*time.Millisecond)) / float64(800*time.Millisecond)
	if f <= 0 {
		return
	}
	if f > 1 {
		f = 1
	}
	rc := win.RECT{Left: 0}                                                                 // LVIR_BOUNDS
	if win.SendMessage(lv, 0x100E, uintptr(p.rsStart), uintptr(unsafe.Pointer(&rc))) == 0 { // LVM_GETITEMRECT
		return
	}
	var cl win.RECT
	win.GetClientRect(lv, &cl)
	if rc.Right > cl.Right {
		rc.Right = cl.Right
	}
	h := int32(3 * win.GetDpiForWindow(lv) / 96)
	if h < 2 {
		h = 2
	}
	bar := win.RECT{Left: rc.Left, Top: rc.Bottom - h, Right: rc.Left + int32(float64(rc.Right-rc.Left)*f), Bottom: rc.Bottom}
	hdc := win.GetDC(lv)
	fillRect(uintptr(hdc), &bar, cur.CursorFg)
	win.ReleaseDC(lv, hdc)
	p.rsBar = bar
}

func (p *Panel) stopHoldBar(lv win.HWND) {
	win.KillTimer(lv, rsBarTimer)
	if p.rsBar.Right > p.rsBar.Left {
		win.InvalidateRect(lv, &p.rsBar, true)
		p.rsBar = win.RECT{}
	}
}

func (p *Panel) rowAtY(lv win.HWND, y int32) int {
	var ht struct {
		X, Y                 int32
		Flags                uint32
		Item, SubItem, Group int32
	}
	ht.X, ht.Y = 4, y
	r := int32(win.SendMessage(lv, 0x1039, 0, uintptr(unsafe.Pointer(&ht)))) // LVM_SUBITEMHITTEST
	if r < 0 {
		return -1
	}
	return int(r)
}

func (p *Panel) setRowMark(row int, on bool) {
	if row < 0 || row >= len(p.model.items) {
		return
	}
	e := p.model.items[row]
	if e.IsParent || p.model.marked[e.Name] == on {
		return
	}
	p.toggleMark(row)
}

func (p *Panel) rightSelectMsg(lv win.HWND, msg uint32, wp, lp uintptr) bool {
	y := int32(int16(lp >> 16))
	switch msg {
	case 0x0204, 0x0206: // WM_RBUTTONDOWN, WM_RBUTTONDBLCLK
		p.activate()
		win.SetFocus(lv)
		row := p.rowAtY(lv, y)
		p.rsActive, p.rsMoved, p.rsStart, p.rsLast = true, false, row, row
		if row >= 0 {
			p.setCursor(row)
			e := p.model.items[row]
			p.rsMark = !p.model.marked[e.Name]
			p.setRowMark(row, p.rsMark)
		}
		win.SetCapture(lv)
		win.SetTimer(lv, rsTimer, 1000, 0)
		p.rsT0, p.rsBar = time.Now(), win.RECT{}
		win.SetTimer(lv, rsBarTimer, 30, 0)
		return true
	case 0x0200: // WM_MOUSEMOVE
		if wp&0x0002 == 0 { // a gombot közben elengedték
			p.endRightSelect(lv)
			return true
		}
		var rc win.RECT
		win.GetClientRect(lv, &rc)
		if y < 0 || y >= rc.Bottom { // a lista szélén görget
			dy := int32(16)
			if y < 0 {
				dy = -16
			}
			win.SendMessage(lv, 0x1014, 0, uintptr(dy)) // LVM_SCROLL
			if y < 0 {
				y = 1
			} else {
				y = rc.Bottom - 2
			}
		}
		row := p.rowAtY(lv, y)
		if row < 0 || row == p.rsLast {
			return true
		}
		if !p.rsMoved {
			p.rsMoved = true
			win.KillTimer(lv, rsTimer)
			p.stopHoldBar(lv)
		}
		if p.rsStart < 0 {
			p.rsStart = row
			p.rsMark = !p.model.marked[p.model.items[row].Name]
		}
		lo, hi := p.rsLast, row
		if lo < 0 {
			lo = row
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		for i := lo; i <= hi; i++ {
			p.setRowMark(i, p.rsMark)
		}
		p.rsLast = row
		p.setCursor(row)
		return true
	case 0x0205: // WM_RBUTTONUP
		p.endRightSelect(lv)
		return true
	case 0x001F: // WM_CANCELMODE
		p.endRightSelect(lv)
		return false
	case 0x0113: // WM_TIMER
		if wp == rsBarTimer {
			p.drawHoldBar(lv)
			return true
		}
		if wp != rsTimer {
			return false
		}
		win.KillTimer(lv, rsTimer)
		if !p.rsActive || p.rsMoved {
			return true
		}
		row := p.rsStart
		p.endRightSelect(lv)
		if row >= 0 {
			p.setRowMark(row, true) // a menü a kijelölt elem(ek)re vonatkozik
		}
		var pt win.POINT
		win.GetCursorPos(&pt)
		win.SendMessage(p.tv.Handle(), 0x007B, uintptr(p.tv.Handle()), uintptr(win.MAKELONG(uint16(int16(pt.X)), uint16(int16(pt.Y))))) // WM_CONTEXTMENU
		return true
	}
	return false
}

func (p *Panel) endRightSelect(lv win.HWND) {
	if !p.rsActive {
		return
	}
	p.rsActive = false
	win.KillTimer(lv, rsTimer)
	p.stopHoldBar(lv)
	if getCapture() == lv {
		win.ReleaseCapture()
	}
}

var procGetCapture = user32.NewProc("GetCapture")

func getCapture() win.HWND {
	h, _, _ := procGetCapture.Call()
	return win.HWND(h)
}

// stageDrag: a húzott archívumbeli / távoli elemek kibontása egy ideiglenes mappába.
// Kis archívumnál csendben, egyébként folyamatablakkal. Ha közben az egérgombot
// felengedték, a húzás elmarad (különben a fájlok azonnal oda kerülnének, ahol az
// egér éppen áll).
func (p *Panel) stageDrag(ents []*Entry, right bool) (string, []string, bool) {
	dir, err := tempViewDir()
	if err != nil {
		return "", nil, false
	}
	var total int64
	for _, e := range ents {
		total += e.Size
		if e.IsDir {
			total += 64 << 20 // a mappa tartalmát nem ismerjük: inkább folyamatablak
		}
	}
	if _, isArc := p.vfs.(Archive); isArc && total < 32<<20 {
		q := &quietProgress{}
		if err := exportItems(p.vfs, ents, dir, q); err != nil || q.err != nil {
			return "", nil, false
		}
	} else {
		title := T("Kicsomagolás")
		if isRemoteKind(p.kind()) {
			title = T("Letöltés")
		}
		if runJob(p.app.mw, title, func(j *Job) error { return exportItems(p.vfs, ents, dir, j) }) != nil {
			return "", nil, false
		}
	}
	btn := int32(win.VK_LBUTTON)
	if right {
		btn = win.VK_RBUTTON
	}
	if st, _, _ := procGetAsyncKeyState.Call(uintptr(btn)); st&0x8000 == 0 {
		return "", nil, false
	}
	var paths []string
	for _, e := range ents {
		lp := filepath.Join(dir, e.Name)
		if _, err := os.Lstat(lp); err == nil {
			paths = append(paths, lp)
		}
	}
	return dir, paths, len(paths) > 0
}
