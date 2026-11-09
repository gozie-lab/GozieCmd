//go:build windows

package main

// Világos / sötét / automatikus (a Windows beállítását követő) téma.
//
// A Win32-ben nincs egyetlen sötétmód-kapcsoló, ezért (mint más klasszikus Win32-programok
// vagy a Notepad++) a vezérlők egy részét mi magunk rajzoljuk:
//   - menüsor: a nem dokumentált UAH üzenetekkel (WM_UAHDRAWMENU…)
//   - felugró menük, gördítősávok, gombok: a Windows saját sötét témái
//   - jelölőnégyzet, választógomb, csoportkeret, fülsor: saját rajzolás
//   - beviteli mezők, listák: sötét háttér és világos szöveg
//   - üzenetablakok: horog (WH_CBT) színezi át őket megnyitáskor

import (
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows/registry"
)

var (
	uxtheme                   = syscall.NewLazyDLL("uxtheme.dll")
	procSetWindowTheme        = uxtheme.NewProc("SetWindowTheme")
	dkOpenThemeData           = uxtheme.NewProc("OpenThemeData")
	dkCloseThemeData          = uxtheme.NewProc("CloseThemeData")
	dkDrawThemeBackground     = uxtheme.NewProc("DrawThemeBackground")
	dkGetThemePartSize        = uxtheme.NewProc("GetThemePartSize")
	dwmapi                    = syscall.NewLazyDLL("dwmapi.dll")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procGetProcAddress        = kernel32.NewProc("GetProcAddress")
	procLoadLibraryW          = kernel32.NewProc("LoadLibraryW")
	gdi32dk                   = syscall.NewLazyDLL("gdi32.dll")
	procSetTextColorGDI       = gdi32dk.NewProc("SetTextColor")
	procSetBkColorGDI         = gdi32dk.NewProc("SetBkColor")
	procImageListSetBkColor   = syscall.NewLazyDLL("comctl32.dll").NewProc("ImageList_SetBkColor")
	procCreateSolidBrush      = gdi32dk.NewProc("CreateSolidBrush")
	procGetClassNameW         = user32.NewProc("GetClassNameW")
	dkFillRect                = user32.NewProc("FillRect")
	dkFrameRect               = user32.NewProc("FrameRect")
	dkDrawTextW               = user32.NewProc("DrawTextW")
	dkGetWindowDC             = user32.NewProc("GetWindowDC")
	dkGetMenuBarInfo          = user32.NewProc("GetMenuBarInfo")
	dkGetMenuItemInfoW        = user32.NewProc("GetMenuItemInfoW")
	dkGetWindowTextW          = user32.NewProc("GetWindowTextW")
	dkMapWindowPoints         = user32.NewProc("MapWindowPoints")
	dkSetWindowsHookExW       = user32.NewProc("SetWindowsHookExW")
	dkCallNextHookEx          = user32.NewProc("CallNextHookEx")
	dkGetDlgCtrlID            = user32.NewProc("GetDlgCtrlID")
)

func className(h uintptr) string {
	buf := make([]uint16, 64)
	n, _, _ := procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 64)
	return syscall.UTF16ToString(buf[:n])
}

func windowText(h win.HWND) string {
	buf := make([]uint16, 512)
	n, _, _ := dkGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), 512)
	return syscall.UTF16ToString(buf[:n])
}

// darkActive: a sötét téma él-e most.
var darkActive bool

func (a *App) themeMode() string {
	v, _ := a.settings.Get("theme")
	switch v {
	case "dark", "light", "auto":
		return v
	}
	return "light"
}

// systemDark a Windows „alkalmazások módja” beállítása.
func systemDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 0
}

func (a *App) wantDark() bool {
	switch a.themeMode() {
	case "dark":
		return true
	case "auto":
		return systemDark()
	}
	return false
}

// ── a sötét téma színei ──

var dk = struct {
	bg, fg, edit, edge, hot, tab, dis walk.Color
}{
	bg:   walk.RGB(32, 32, 32),    // ablakháttér
	fg:   walk.RGB(230, 230, 230), // szöveg
	edit: walk.RGB(45, 45, 45),    // beviteli mezők, listák
	edge: walk.RGB(85, 85, 85),    // keretek
	hot:  walk.RGB(65, 65, 65),    // kijelölt menü, gomb alatti egér
	tab:  walk.RGB(48, 48, 48),    // aktív fül
	dis:  walk.RGB(128, 128, 128), // tiltott szöveg
}

var gdiBrushes = map[walk.Color]uintptr{}

func gdiBrush(c walk.Color) uintptr {
	if b, ok := gdiBrushes[c]; ok {
		return b
	}
	b, _, _ := procCreateSolidBrush.Call(uintptr(c))
	gdiBrushes[c] = b
	return b
}

func walkBrush(c walk.Color) *walk.SolidColorBrush {
	// Mindig új ecset: a walk eldobja (Dispose) az ecsetet, amikor az utolsó ablak is
	// leveszi magáról (pl. sötétről világosra váltáskor SetBackground(nil)). Egy
	// gyorsítótárazott ecset így a következő sötét váltáskor már érvénytelen volt, és
	// a háttér nem rajzolódott ki. A nem használt ecseteket a walk maga szabadítja fel.
	b, _ := walk.NewSolidColorBrush(c)
	return b
}

func fillRect(hdc uintptr, rc *win.RECT, c walk.Color) {
	dkFillRect.Call(hdc, uintptr(unsafe.Pointer(rc)), gdiBrush(c))
}

func frameRect(hdc uintptr, rc *win.RECT, c walk.Color) {
	dkFrameRect.Call(hdc, uintptr(unsafe.Pointer(rc)), gdiBrush(c))
}

func drawText(hdc uintptr, s string, rc *win.RECT, flags uint32) {
	if s == "" {
		return
	}
	p, _ := syscall.UTF16FromString(s)
	dkDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)-1), uintptr(unsafe.Pointer(rc)), uintptr(flags))
}

const (
	dtCenter     = 0x1
	dtVCenter    = 0x4
	dtSingleLine = 0x20
	dtCalcRect   = 0x400
	dtEndEllips  = 0x8000
	dtHidePrefix = 0x100000
)

// ── alacsony szintű Windows-hívások ──

func setTheme(h win.HWND, app string, list *string) {
	if h == 0 {
		return
	}
	var pa, pl uintptr
	if app != "\x00" { // "\x00": NULL – az alapértelmezett téma visszaállítása
		p, _ := syscall.UTF16PtrFromString(app)
		pa = uintptr(unsafe.Pointer(p))
	}
	if list != nil {
		p, _ := syscall.UTF16PtrFromString(*list)
		pl = uintptr(unsafe.Pointer(p))
	}
	procSetWindowTheme.Call(uintptr(h), pa, pl)
}

// themeOr a sötét vagy (világosban) az alapértelmezett témát állítja.
func themeOr(h win.HWND, dark bool, darkName string) {
	if dark {
		setTheme(h, darkName, nil)
	} else {
		setTheme(h, "\x00", nil)
	}
}

// darkTitle sötét (vagy világos) címsort kér a Windowstól.
func darkTitle(h win.HWND, dark bool) {
	v := int32(0)
	if dark {
		v = 1
	}
	if r, _, _ := procDwmSetWindowAttribute.Call(uintptr(h), 20, uintptr(unsafe.Pointer(&v)), 4); r != 0 {
		procDwmSetWindowAttribute.Call(uintptr(h), 19, uintptr(unsafe.Pointer(&v)), 4) // régebbi Windows 10
	}
}

// uxtheme sorszámmal elérhető (nem dokumentált) függvényei.
var uxCache = map[uintptr]uintptr{}

func uxOrdinal(n uintptr) uintptr {
	if p, ok := uxCache[n]; ok {
		return p
	}
	name, _ := syscall.UTF16PtrFromString("uxtheme.dll")
	h, _, _ := procLoadLibraryW.Call(uintptr(unsafe.Pointer(name)))
	var p uintptr
	if h != 0 {
		p, _, _ = procGetProcAddress.Call(h, n)
	}
	uxCache[n] = p
	return p
}

func setAppDark(dark bool) {
	mode := uintptr(3) // ForceLight
	if dark {
		mode = 2 // ForceDark
	}
	if p := uxOrdinal(135); p != 0 { // SetPreferredAppMode
		syscall.SyscallN(p, mode)
	}
	if p := uxOrdinal(104); p != 0 { // RefreshImmersiveColorPolicyState
		syscall.SyscallN(p)
	}
	if p := uxOrdinal(136); p != 0 { // FlushMenuThemes
		syscall.SyscallN(p)
	}
}

func allowDarkWindow(h win.HWND, dark bool) {
	if p := uxOrdinal(133); p != 0 && h != 0 { // AllowDarkModeForWindow
		syscall.SyscallN(p, uintptr(h), uintptr(boolInt(dark)))
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func dpiScale(h win.HWND, v int32) int32 {
	dpi := int32(win.GetDpiForWindow(h))
	if dpi == 0 {
		dpi = 96
	}
	return v * dpi / 96
}

var dkGetWindowSubclass = comctl32.NewProc("GetWindowSubclass")

// subclassOnce: egy ablakra egy eljárást csak egyszer akasztunk
// (a Windows kérdezzük, mert a bezárt ablakok azonosítói újra kioszthatók).
func subclassOnce(h win.HWND, proc uintptr, id uintptr) {
	if h == 0 || hasSubclass(h, proc, id) {
		return
	}
	procSetWindowSubclass.Call(uintptr(h), proc, id, 0)
}

func hasSubclass(h win.HWND, proc, id uintptr) bool {
	var ref uintptr
	r, _, _ := dkGetWindowSubclass.Call(uintptr(h), proc, id, uintptr(unsafe.Pointer(&ref)))
	return r != 0
}

// ── szülőablakok: listamező- és legördülőszínek, menüsor, eszköztár ──

var colorProc = syscall.NewCallback(func(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	if (msg == 0x031A || msg == 0x0015) && !reapplying { // WM_THEMECHANGED, WM_SYSCOLORCHANGE
		if _, ok := listBg[win.HWND(hwnd)]; ok {
			// a walk ilyenkor a rendszer (világos) színeire állítja a sorokat: utána újra a mieink
			r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
			scheduleReapply()
			return r
		}
	}
	if msg == 0x001A && lp != 0 && theApp != nil && theApp.mw != nil && win.HWND(hwnd) == theApp.mw.Handle() { // WM_SETTINGCHANGE
		if windows.UTF16PtrToString((*uint16)(unsafe.Pointer(lp))) == "ImmersiveColorSet" {
			// a Windows világos/sötét módja változott: azonnal (nem a következő lekérdezéskor)
			if themeChecksPending.CompareAndSwap(false, true) {
				time.AfterFunc(time.Second, func() { themeChecksPending.Store(false) })
				dlog("téma: a Windows jelezte a változást")
				// a rendszerleíróban a beállítás néha csak kicsit később frissül: többször nézzük meg
				for _, d := range []time.Duration{0, 100, 250, 500, 900} {
					time.AfterFunc(d*time.Millisecond, func() {
						theApp.mw.Synchronize(func() {
							if themeCheckFn != nil {
								themeCheckFn()
							}
						})
					})
				}
			}
		}
	}
	if msg == 0x1001 { // LVM_SETBKCOLOR: a walk témaváltáskor (WM_THEMECHANGED) a rendszer színére állítaná vissza
		if bg, ok := listBg[win.HWND(hwnd)]; ok {
			lp = uintptr(bg)
		}
		r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
		clearImageListBk(win.HWND(hwnd))
		return r
	}
	if darkActive {
		switch msg {
		case 0x0134: // WM_CTLCOLORLISTBOX
			procSetTextColorGDI.Call(wp, uintptr(dk.fg))
			procSetBkColorGDI.Call(wp, uintptr(dk.edit))
			return gdiBrush(dk.edit)
		case 0x0133, 0x0138: // WM_CTLCOLOREDIT, WM_CTLCOLORSTATIC – a legördülők szerkesztőmezője
			if className(lp) == "Edit" && className(uintptr(win.GetParent(win.HWND(lp)))) == "ComboBox" {
				procSetTextColorGDI.Call(wp, uintptr(dk.fg))
				procSetBkColorGDI.Call(wp, uintptr(dk.edit))
				return gdiBrush(dk.edit)
			}
		case 0x0091: // WM_UAHDRAWMENU: a menüsor háttere
			drawMenuBar(win.HWND(hwnd), lp)
			return 1
		case 0x0092: // WM_UAHDRAWMENUITEM
			drawMenuBarItem(lp)
			return 1
		case 0x0085, 0x0086: // WM_NCPAINT, WM_NCACTIVATE: a menüsor alatti világos vonal
			r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
			paintMenuLine(win.HWND(hwnd))
			return r
		case 0x004E: // WM_NOTIFY
			if r, ok := darkNotify(lp); ok {
				return r
			}
		}
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
	return r
})

func hookColors(h win.HWND) { subclassOnce(h, colorProc, 0xC010) }

// darkNotify: a fejlécek szövegszíne és az eszköztárgombok háttere.
func darkNotify(lp uintptr) (uintptr, bool) {
	from := *(*uintptr)(unsafe.Pointer(lp))
	code := *(*int32)(unsafe.Pointer(lp + 16))
	if code != -12 { // NM_CUSTOMDRAW
		return 0, false
	}
	stage := *(*uint32)(unsafe.Pointer(lp + 24))
	hdc := *(*uintptr)(unsafe.Pointer(lp + 32))
	rc := (*win.RECT)(unsafe.Pointer(lp + 40))
	state := *(*uint32)(unsafe.Pointer(lp + 64))
	switch className(from) {
	case "SysHeader32":
		switch stage {
		case 1: // CDDS_PREPAINT
			return 0x20, true // CDRF_NOTIFYITEMDRAW
		case 0x10001: // CDDS_ITEMPREPAINT: a téma a szövegszínt figyelmen kívül hagyná, ezért mi rajzolunk
			item := *(*uintptr)(unsafe.Pointer(lp + 56))
			paintHeaderItem(win.HWND(from), hdc, *rc, int(item), state&0x40 != 0)
			return 0x4, true // CDRF_SKIPDEFAULT
		}
	case "ToolbarWindow32":
		switch stage {
		case 1:
			fillRect(hdc, rc, dk.bg)
			return 0x20, true
		case 0x10001:
			if state&(0x40|0x1|0x8) != 0 { // forró, lenyomott, bejelölt
				fillRect(hdc, rc, dk.hot)
			}
			*(*uint32)(unsafe.Pointer(lp + 104)) = uint32(dk.fg) // clrText
			return 0x00400000 | 0x00010000, true                 // TBCDRF_NOBACKGROUND | TBCDRF_NOEDGES
		}
	}
	return 0, false
}

// ── menüsor (Notepad++ / win32-darkmode módszer) ──

type menuBarInfo struct {
	CbSize   uint32
	RcBar    win.RECT
	_        uint32
	HMenu    uintptr
	HwndMenu uintptr
	Flags    int32
	_        int32
}

func menuBarRect(h win.HWND) (win.RECT, bool) {
	mbi := menuBarInfo{CbSize: uint32(unsafe.Sizeof(menuBarInfo{}))}
	if r, _, _ := dkGetMenuBarInfo.Call(uintptr(h), uintptr(0xFFFFFFFD), 0, uintptr(unsafe.Pointer(&mbi))); r == 0 {
		return win.RECT{}, false
	}
	var wr win.RECT
	win.GetWindowRect(h, &wr)
	rc := mbi.RcBar
	rc.Left -= wr.Left
	rc.Right -= wr.Left
	rc.Top -= wr.Top
	rc.Bottom -= wr.Top
	return rc, true
}

func drawMenuBar(h win.HWND, lp uintptr) {
	hdc := *(*uintptr)(unsafe.Pointer(lp + 8))
	if rc, ok := menuBarRect(h); ok {
		fillRect(hdc, &rc, dk.bg)
	}
}

type menuItemInfo struct {
	CbSize        uint32
	FMask         uint32
	FType         uint32
	FState        uint32
	WID           uint32
	HSubMenu      uintptr
	HbmpChecked   uintptr
	HbmpUnchecked uintptr
	DwItemData    uintptr
	DwTypeData    *uint16
	Cch           uint32
	HbmpItem      uintptr
}

func drawMenuBarItem(lp uintptr) {
	itemState := *(*uint32)(unsafe.Pointer(lp + 16))
	hdc := *(*uintptr)(unsafe.Pointer(lp + 32))
	rc := *(*win.RECT)(unsafe.Pointer(lp + 40))
	hmenu := *(*uintptr)(unsafe.Pointer(lp + 64))
	pos := *(*int32)(unsafe.Pointer(lp + 88))
	buf := make([]uint16, 256)
	mii := menuItemInfo{FMask: 0x40, DwTypeData: &buf[0], Cch: 255} // MIIM_STRING
	mii.CbSize = uint32(unsafe.Sizeof(mii))
	dkGetMenuItemInfoW.Call(hmenu, uintptr(pos), 1, uintptr(unsafe.Pointer(&mii)))
	text := syscall.UTF16ToString(buf)
	bg, fg := dk.bg, dk.fg
	if itemState&(0x40|0x1) != 0 { // ODS_HOTLIGHT, ODS_SELECTED
		bg = dk.hot
	}
	if itemState&(0x4|0x2) != 0 { // ODS_DISABLED, ODS_GRAYED
		fg = dk.dis
	} else if itemState&0x80 != 0 { // ODS_INACTIVE: az ablak nem aktív
		fg = walk.RGB(175, 175, 175)
	}
	fillRect(hdc, &rc, bg)
	win.SetBkMode(win.HDC(hdc), win.TRANSPARENT)
	procSetTextColorGDI.Call(hdc, uintptr(fg))
	flags := uint32(dtCenter | dtVCenter | dtSingleLine)
	if itemState&0x100 != 0 { // ODS_NOACCEL
		flags |= dtHidePrefix
	}
	drawText(hdc, text, &rc, flags)
}

func paintMenuLine(h win.HWND) {
	if _, ok := menuBarRect(h); !ok {
		return
	}
	var rc, wr win.RECT
	win.GetClientRect(h, &rc)
	dkMapWindowPoints.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&rc)), 2)
	win.GetWindowRect(h, &wr)
	line := win.RECT{Left: rc.Left - wr.Left, Right: rc.Right - wr.Left, Top: rc.Top - wr.Top - 1, Bottom: rc.Top - wr.Top}
	hdc, _, _ := dkGetWindowDC.Call(uintptr(h))
	if hdc != 0 {
		fillRect(hdc, &line, dk.bg)
		win.ReleaseDC(h, win.HDC(hdc))
	}
}

// ── jelölőnégyzet, választógomb, csoportkeret: saját rajzolás sötétben ──

var buttonProc = syscall.NewCallback(func(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	if darkActive {
		h := win.HWND(hwnd)
		switch msg {
		case 0x000F: // WM_PAINT
			var ps win.PAINTSTRUCT
			hdc := win.BeginPaint(h, &ps)
			paintButton(h, uintptr(hdc))
			win.EndPaint(h, &ps)
			return 0
		case 0x0318: // WM_PRINTCLIENT
			paintButton(h, wp)
			return 0
		case 0x0014: // WM_ERASEBKGND
			return 1
		case 0x00F1, 0x00F3, 0x000A, 0x000C, 0x0128, 0x0007, 0x0008: // BM_SETCHECK, BM_SETSTATE, WM_ENABLE, WM_SETTEXT, WM_UPDATEUISTATE, fókusz
			r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
			win.InvalidateRect(h, nil, false)
			return r
		}
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
	return r
})

func paintButton(h win.HWND, hdc uintptr) {
	var rc win.RECT
	win.GetClientRect(h, &rc)
	style := uint32(win.GetWindowLong(h, win.GWL_STYLE))
	typ := style & 0xF
	font := win.SendMessage(h, win.WM_GETFONT, 0, 0)
	old := win.SelectObject(win.HDC(hdc), win.HGDIOBJ(font))
	defer win.SelectObject(win.HDC(hdc), old)
	win.SetBkMode(win.HDC(hdc), win.TRANSPARENT)
	text := windowText(h)
	fg := dk.fg
	if !win.IsWindowEnabled(h) {
		fg = dk.dis
	}
	procSetTextColorGDI.Call(hdc, uintptr(fg))

	if typ == 7 { // BS_GROUPBOX: csak keret és cím (a belsejét a benne lévő vezérlők rajzolják)
		var trc win.RECT
		drawText(hdc, "Ág", &trc, dtSingleLine|dtCalcRect)
		th := trc.Bottom - trc.Top
		frame := win.RECT{Left: rc.Left, Top: rc.Top + th/2, Right: rc.Right, Bottom: rc.Bottom}
		frameRect(hdc, &frame, dk.edge)
		if text != "" {
			trc = win.RECT{}
			drawText(hdc, text, &trc, dtSingleLine|dtCalcRect)
			x := rc.Left + dpiScale(h, 8)
			back := win.RECT{Left: x - dpiScale(h, 3), Top: rc.Top, Right: x + (trc.Right - trc.Left) + dpiScale(h, 3), Bottom: rc.Top + th}
			fillRect(hdc, &back, dk.bg)
			t := win.RECT{Left: x, Top: rc.Top, Right: back.Right, Bottom: rc.Top + th}
			drawText(hdc, text, &t, dtSingleLine)
		}
		return
	}

	fillRect(hdc, &rc, dk.bg)
	radio := typ == 4 || typ == 9
	part := uintptr(3) // BP_CHECKBOX
	if radio {
		part = 2 // BP_RADIOBUTTON
	}
	chk := win.SendMessage(h, 0x00F0, 0, 0) // BM_GETCHECK
	bst := win.SendMessage(h, 0x00F2, 0, 0) // BM_GETSTATE
	state := uintptr(1)                     // nincs bejelölve
	switch chk {
	case 1:
		state = 5
	case 2:
		if !radio {
			state = 9
		}
	}
	switch {
	case !win.IsWindowEnabled(h):
		state += 3
	case bst&0x4 != 0: // BST_PUSHED
		state += 2
	case bst&0x200 != 0: // BST_HOT
		state++
	}
	cls, _ := syscall.UTF16PtrFromString("Button")
	box := int32(dpiScale(h, 13))
	theme, _, _ := dkOpenThemeData.Call(uintptr(h), uintptr(unsafe.Pointer(cls)))
	if theme != 0 {
		var sz struct{ CX, CY int32 }
		dkGetThemePartSize.Call(theme, hdc, part, state, 0, 2, uintptr(unsafe.Pointer(&sz))) // TS_DRAW
		if sz.CX > 0 {
			box = sz.CX
		}
	}
	br := win.RECT{Left: rc.Left, Top: rc.Top + (rc.Bottom-rc.Top-box)/2, Right: rc.Left + box}
	br.Bottom = br.Top + box
	if theme != 0 {
		dkDrawThemeBackground.Call(theme, hdc, part, state, uintptr(unsafe.Pointer(&br)), 0)
		dkCloseThemeData.Call(theme)
	} else {
		frameRect(hdc, &br, dk.fg)
	}
	t := win.RECT{Left: br.Right + dpiScale(h, 5), Top: rc.Top, Right: rc.Right, Bottom: rc.Bottom}
	flags := uint32(dtSingleLine | dtVCenter | dtEndEllips)
	if win.SendMessage(h, 0x0129, 0, 0)&0x4 != 0 { // WM_QUERYUISTATE: UISF_HIDEACCEL
		flags |= dtHidePrefix
	}
	drawText(hdc, text, &t, flags)
	if win.GetFocus() == h && win.SendMessage(h, 0x0129, 0, 0)&0x1 == 0 { // UISF_HIDEFOCUS
		c := t
		drawText(hdc, text, &c, flags|dtCalcRect)
		c.Top = t.Top + (t.Bottom-t.Top-(c.Bottom-c.Top))/2
		c.Bottom = c.Top + (c.Bottom - c.Top)
		c.Left -= 1
		c.Right += 1
		win.DrawFocusRect(win.HDC(hdc), &c)
	}
}

// ── fülsor: saját rajzolás sötétben ──

type tcItem struct {
	Mask      uint32
	State     uint32
	StateMask uint32
	_         uint32
	Text      *uint16
	TextMax   int32
	Image     int32
	Param     uintptr
}

var tabProc = syscall.NewCallback(func(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	if darkActive {
		h := win.HWND(hwnd)
		switch msg {
		case 0x000F:
			var ps win.PAINTSTRUCT
			hdc := win.BeginPaint(h, &ps)
			paintTabs(h, uintptr(hdc))
			win.EndPaint(h, &ps)
			return 0
		case 0x0014:
			return 1
		}
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
	return r
})

func paintTabs(h win.HWND, hdc uintptr) {
	var rc win.RECT
	win.GetClientRect(h, &rc)
	font := win.SendMessage(h, win.WM_GETFONT, 0, 0)
	old := win.SelectObject(win.HDC(hdc), win.HGDIOBJ(font))
	defer win.SelectObject(win.HDC(hdc), old)
	win.SetBkMode(win.HDC(hdc), win.TRANSPARENT)
	n := int(win.SendMessage(h, 0x1304, 0, 0))          // TCM_GETITEMCOUNT
	cur := int(int32(win.SendMessage(h, 0x130B, 0, 0))) // TCM_GETCURSEL
	var bottom int32
	for i := 0; i < n; i++ {
		var r win.RECT
		win.SendMessage(h, 0x130A, uintptr(i), uintptr(unsafe.Pointer(&r))) // TCM_GETITEMRECT
		if r.Bottom > bottom {
			bottom = r.Bottom
		}
	}
	// csak a fülsort töltjük ki: a lapokat a rajtuk lévő vezérlők rajzolják
	strip := win.RECT{Left: rc.Left, Top: rc.Top, Right: rc.Right, Bottom: bottom}
	fillRect(hdc, &strip, dk.bg)
	page := win.RECT{Left: rc.Left, Top: bottom, Right: rc.Right, Bottom: rc.Bottom}
	frameRect(hdc, &page, dk.edge)
	for i := 0; i < n; i++ {
		var r win.RECT
		win.SendMessage(h, 0x130A, uintptr(i), uintptr(unsafe.Pointer(&r)))
		buf := make([]uint16, 128)
		it := tcItem{Mask: 1, Text: &buf[0], TextMax: 127}                   // TCIF_TEXT
		win.SendMessage(h, 0x133C, uintptr(i), uintptr(unsafe.Pointer(&it))) // TCM_GETITEMW
		fg := dk.dis
		if i == cur {
			r.Top -= dpiScale(h, 2)
			r.Left -= dpiScale(h, 2)
			r.Right += dpiScale(h, 2)
			fillRect(hdc, &r, dk.tab)
			frameRect(hdc, &r, dk.edge)
			gap := win.RECT{Left: r.Left + 1, Top: r.Bottom - 1, Right: r.Right - 1, Bottom: r.Bottom + 1}
			fillRect(hdc, &gap, dk.tab)
			fg = dk.fg
		} else {
			frameRect(hdc, &r, dk.edge)
		}
		procSetTextColorGDI.Call(hdc, uintptr(fg))
		drawText(hdc, syscall.UTF16ToString(buf), &r, dtCenter|dtVCenter|dtSingleLine|dtHidePrefix)
	}
}

// ── vezérlőfa ──

type comboBoxInfo struct {
	CbSize      uint32
	RcItem      win.RECT
	RcButton    win.RECT
	StateButton uint32
	HwndCombo   uintptr
	HwndItem    uintptr
	HwndList    uintptr
}

// themeTree egy ablak összes vezérlőjére alkalmazza a témát.
func themeTree(w walk.Window, dark bool) {
	h := w.Handle()
	allowDarkWindow(h, dark)
	fg := walk.Color(0)
	var editBg walk.Brush
	if dark {
		fg = dk.fg
		editBg = walkBrush(dk.edit)
	}
	if pw := win.GetParent(h); pw != 0 {
		hookColors(pw)
	}
	switch x := w.(type) {
	case *walk.Label:
		x.SetTextColor(fg)
	case *walk.LineEdit:
		x.SetTextColor(fg)
		x.SetBackground(editBg)
		themeOr(h, dark, "DarkMode_CFD")
	case *walk.TextEdit:
		x.SetTextColor(fg)
		x.SetBackground(editBg)
		themeOr(h, dark, "DarkMode_Explorer")
	case *walk.ComboBox:
		themeOr(h, dark, "DarkMode_CFD")
		hookColors(h)
		cbi := comboBoxInfo{}
		cbi.CbSize = uint32(unsafe.Sizeof(cbi))
		if win.SendMessage(h, 0x0164, 0, uintptr(unsafe.Pointer(&cbi))) != 0 && cbi.HwndList != 0 { // CB_GETCOMBOBOXINFO
			themeOr(win.HWND(cbi.HwndList), dark, "DarkMode_Explorer")
		}
	case *walk.PushButton:
		themeOr(h, dark, "DarkMode_Explorer")
	case *walk.CheckBox, *walk.RadioButton:
		themeOr(h, dark, "DarkMode_Explorer")
		subclassOnce(h, buttonProc, 0xC011)
		win.InvalidateRect(h, nil, true)
	case *walk.ListBox:
		themeOr(h, dark, "DarkMode_Explorer")
		x.Invalidate()
	case *walk.TableView:
		themeTable(x, dark)
	case *walk.GroupBox:
		if gb := listViewField(x, "hWndGroupBox"); gb != 0 {
			subclassOnce(gb, buttonProc, 0xC011)
			win.InvalidateRect(gb, nil, true)
		}
	case *walk.TabWidget:
		if tab := listViewField(x, "hWndTab"); tab != 0 {
			allowDarkWindow(tab, dark)
			subclassOnce(tab, tabProc, 0xC013)
			win.InvalidateRect(tab, nil, true)
		}
		themePage := func(pg *walk.TabPage, dark bool) {
			pageDark[pg] = dark
			if dark {
				pg.SetBackground(walkBrush(dk.bg))
			} else {
				pg.SetBackground(nil)
			}
			themeTree(pg, dark)
		}
		if !tabHooked[x] { // a háttérben lévő lapok átszínezése, amikor előjönnek
			tabHooked[x] = true
			tw := x
			tw.CurrentIndexChanged().Attach(func() {
				if pg := tw.Pages().At(maxInt(tw.CurrentIndex(), 0)); pg != nil {
					if d, ok := pageDark[pg]; !ok || d != darkActive {
						themePage(pg, darkActive)
						win.RedrawWindow(pg.Handle(), nil, 0, win.RDW_INVALIDATE|win.RDW_ERASE|win.RDW_ALLCHILDREN)
					}
				}
			})
		}
		for i := 0; i < x.Pages().Len(); i++ {
			pg := x.Pages().At(i)
			// témaváltáskor (nyitott ablakban) csak a látható lapot: a többit előjövetelkor
			if lazyTabs && i != x.CurrentIndex() {
				continue
			}
			themePage(pg, dark)
		}
	case *walk.ToolBar:
		themeOr(h, dark, "DarkMode_Explorer")
	}
	if c, ok := w.(walk.Container); ok && c.Children() != nil {
		hookColors(h)
		for i := 0; i < c.Children().Len(); i++ {
			themeTree(c.Children().At(i), dark)
		}
	}
}

// themeListView: lista, fejléc és gördítősáv témája (a fejléc szövegét a szülő színezi).
// setListColors: a lista háttér- és betűszíne. Az LVM_SETBKCOLOR a lista (nem
// megosztott) ikonlistájának háttérszínét is átállítja, és onnantól minden új ikon
// erre a színre „sül rá”: témaváltás után a régi háttér (pl. fehér) látszott az
// ikonokban. Ezért az ikonlisták háttere mindig átlátszó (CLR_NONE) marad.
func setListColors(lv win.HWND, bg, fg walk.Color) {
	listBg[lv] = bg
	hookColors(lv)
	win.SendMessage(lv, 0x1001, 0, uintptr(bg)) // LVM_SETBKCOLOR
	win.SendMessage(lv, 0x1026, 0, uintptr(bg)) // LVM_SETTEXTBKCOLOR
	win.SendMessage(lv, 0x1024, 0, uintptr(fg)) // LVM_SETTEXTCOLOR
	clearImageListBk(lv)
}

// listBg: a listák kívánt háttérszíne (a walk felülírási kísérleteihez).
var listBg = map[win.HWND]walk.Color{}

func clearImageListBk(lv win.HWND) {
	for _, which := range []uintptr{0, 1, 2} { // LVSIL_NORMAL, LVSIL_SMALL, LVSIL_STATE
		if iml := win.SendMessage(lv, 0x1002, which, 0); iml != 0 { // LVM_GETIMAGELIST
			procImageListSetBkColor.Call(iml, 0xFFFFFFFF) // CLR_NONE
		}
	}
}

func themeListView(lv win.HWND, dark bool) {
	if lv == 0 {
		return
	}
	allowDarkWindow(lv, dark)
	if dark {
		setTheme(lv, "DarkMode_Explorer", nil)
	} else {
		setTheme(lv, "Explorer", nil)
	}
	hookColors(lv)                                                    // a fejléc NM_CUSTOMDRAW üzenete a listához érkezik
	if hdr := win.HWND(win.SendMessage(lv, 0x101F, 0, 0)); hdr != 0 { // LVM_GETHEADER
		allowDarkWindow(hdr, dark)
		themeOr(hdr, dark, "DarkMode_ItemsView")
		subclassOnce(hdr, headerProc, 0xC015) // sötétben a fejlécet teljesen mi rajzoljuk
		win.InvalidateRect(hdr, nil, true)
	}
}

var headerHot = map[win.HWND]int{}

var headerProc = syscall.NewCallback(func(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	if msg == 0x120C && panelHeaders[win.HWND(hwnd)] && lp != 0 { // HDM_SETITEMW: a fájlpanelen a címben van a ▲▼ jel,
		mask := (*uint32)(unsafe.Pointer(lp)) // a walk rendezési nyila (világos témában) duplán jelenne meg
		if *mask&0x4 != 0 {                   // HDI_FORMAT
			f := (*int32)(unsafe.Pointer(lp + 28)) // HDITEMW.fmt (64 bites elrendezés)
			*f &^= 0x0400 | 0x0200                 // HDF_SORTUP | HDF_SORTDOWN
		}
	}
	if darkActive {
		h := win.HWND(hwnd)
		switch msg {
		case 0x000F: // WM_PAINT
			var ps win.PAINTSTRUCT
			hdc := win.BeginPaint(h, &ps)
			paintHeader(h, uintptr(hdc))
			win.EndPaint(h, &ps)
			return 0
		case 0x0318: // WM_PRINTCLIENT
			paintHeader(h, wp)
			return 0
		case 0x0014: // WM_ERASEBKGND
			return 1
		case 0x0200: // WM_MOUSEMOVE: a forró oszlop kiemelése
			r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
			ht := struct {
				X, Y  int32
				Flags uint32
				Item  int32
			}{X: int32(int16(lp)), Y: int32(int16(lp >> 16))}
			win.SendMessage(h, 0x1206, 0, uintptr(unsafe.Pointer(&ht))) // HDM_HITTEST
			if old, ok := headerHot[h]; !ok || old != int(ht.Item) {
				headerHot[h] = int(ht.Item)
				win.InvalidateRect(h, nil, false)
			}
			return r
		case 0x02A3: // WM_MOUSELEAVE
			delete(headerHot, h)
			win.InvalidateRect(h, nil, false)
		}
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
	return r
})

func paintHeader(h win.HWND, hdc uintptr) {
	var rc win.RECT
	win.GetClientRect(h, &rc)
	fillRect(hdc, &rc, dk.bg)
	line := win.RECT{Left: rc.Left, Top: rc.Bottom - 1, Right: rc.Right, Bottom: rc.Bottom}
	fillRect(hdc, &line, dk.edge)
	hot, hasHot := headerHot[h]
	n := int(win.SendMessage(h, 0x1200, 0, 0)) // HDM_GETITEMCOUNT
	for i := 0; i < n; i++ {
		var r win.RECT
		if win.SendMessage(h, 0x1207, uintptr(i), uintptr(unsafe.Pointer(&r))) == 0 { // HDM_GETITEMRECT
			continue
		}
		if r.Right <= r.Left {
			continue
		}
		paintHeaderItem(h, hdc, r, i, hasHot && hot == i)
	}
}

// themeTable egy (nem fájlpanel) táblázat sötét/világos színei.
func themeTable(tv *walk.TableView, dark bool) {
	bg, fg := walk.Color(win.GetSysColor(win.COLOR_WINDOW)), walk.Color(win.GetSysColor(win.COLOR_WINDOWTEXT))
	if dark {
		bg, fg = dk.edit, dk.fg
	}
	lvs := []win.HWND{listViewField(tv, "hwndNormalLV"), listViewField(tv, "hwndFrozenLV")}
	// előbb a téma: a SetWindowTheme után a walk (WM_THEMECHANGED) a rendszer színeire
	// állítja vissza a sorok színeit, ezért a saját színeket csak utána adjuk meg
	for _, lv := range lvs {
		if lv != 0 {
			themeListView(lv, dark)
		}
	}
	setWalkColor(tv, "themeNormalBGColor", bg)
	setWalkColor(tv, "themeNormalTextColor", fg)
	if !isPanelTable(tv) {
		sb, sf := selColors(true)
		nb, _ := selColors(false)
		setWalkColor(tv, "themeSelectedBGColor", sb)
		setWalkColor(tv, "themeSelectedTextColor", sf)
		setWalkColor(tv, "themeSelectedNotFocusedBGColor", nb)
	}
	for _, lv := range lvs {
		if lv != 0 {
			setListColors(lv, bg, fg)
		}
	}
	tv.Invalidate()
}

// themeDialog a dialógusablakot a mostani témához igazítja.
func themeDialog(dlg *walk.Dialog) {
	if dlg == nil {
		return
	}
	if theApp != nil && theApp.icon != nil {
		dlg.SetIcon(theApp.icon)
	}
	hookStyledLists(dlg)
	trackForm(dlg)
	formDark[dlg] = darkActive
	if !darkActive {
		return
	}
	allowDarkWindow(dlg.Handle(), true)
	darkTitle(dlg.Handle(), true)
	dlg.SetBackground(walkBrush(dk.bg))
	themeTree(dlg, true)
}

// ── a Windows üzenetablakai (MessageBox) sötétben ──

var msgBoxProc = syscall.NewCallback(func(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	if darkActive {
		switch msg {
		case 0x0136, 0x0138, 0x0135: // WM_CTLCOLORDLG, WM_CTLCOLORSTATIC, WM_CTLCOLORBTN
			procSetTextColorGDI.Call(wp, uintptr(dk.fg))
			procSetBkColorGDI.Call(wp, uintptr(dk.bg))
			return gdiBrush(dk.bg)
		case 0x0014: // WM_ERASEBKGND
			var rc win.RECT
			win.GetClientRect(win.HWND(hwnd), &rc)
			fillRect(wp, &rc, dk.bg)
			return 1
		case 0x000F: // WM_PAINT: a világos alsó sáv helyett egyszínű háttér
			var ps win.PAINTSTRUCT
			hdc := win.BeginPaint(win.HWND(hwnd), &ps)
			var rc win.RECT
			win.GetClientRect(win.HWND(hwnd), &rc)
			fillRect(uintptr(hdc), &rc, dk.bg)
			win.EndPaint(win.HWND(hwnd), &ps)
			return 0
		}
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
	return r
})

// isMessageBox: csak gombokból és szövegekből álló #32770 párbeszédablak
// (a fájl-, szín- és betűválasztó ablakokhoz nem nyúlunk).
func isMessageBox(h win.HWND) bool {
	if className(uintptr(h)) != "#32770" {
		return false
	}
	hasText := false
	for c := win.GetWindow(h, win.GW_CHILD); c != 0; c = win.GetWindow(c, win.GW_HWNDNEXT) {
		switch className(uintptr(c)) {
		case "Button":
		case "Static":
			if id, _, _ := dkGetDlgCtrlID.Call(uintptr(c)); id == 0xFFFF {
				hasText = true
			}
		default:
			return false
		}
	}
	return hasText
}

var cbtProc = syscall.NewCallback(func(code, wp, lp uintptr) uintptr {
	if int32(code) == 5 && darkActive { // HCBT_ACTIVATE
		h := win.HWND(wp)
		if isMessageBox(h) && !hasSubclass(h, msgBoxProc, 0xC014) {
			darkTitle(h, true)
			allowDarkWindow(h, true)
			subclassOnce(h, msgBoxProc, 0xC014)
			for c := win.GetWindow(h, win.GW_CHILD); c != 0; c = win.GetWindow(c, win.GW_HWNDNEXT) {
				if className(uintptr(c)) == "Button" {
					allowDarkWindow(c, true)
					setTheme(c, "DarkMode_Explorer", nil)
				}
			}
			win.InvalidateRect(h, nil, true)
		}
	}
	r, _, _ := dkCallNextHookEx.Call(0, code, wp, lp)
	return r
})

var cbtHook uintptr

func installMsgBoxHook() {
	if cbtHook == 0 {
		cbtHook, _, _ = dkSetWindowsHookExW.Call(5, cbtProc, 0, uintptr(win.GetCurrentThreadId())) // WH_CBT
	}
}

// ── a főablak ──

var reapplying, reapplyPending bool

// scheduleReapply: a téma (újra)alkalmazása a felület szálán, egyszer.
func scheduleReapply() {
	if reapplyPending || theApp == nil || theApp.mw == nil {
		return
	}
	reapplyPending = true
	theApp.mw.Synchronize(func() {
		reapplyPending = false
		reapplying = true
		defer func() { reapplying = false }()
		if recolorFn != nil {
			recolorFn() // csak a walk által visszaállított színeket adjuk vissza (gyors)
		}
	})
}

// recolorAll: a táblázatok (panelek, párbeszédablakok listái) saját színei újra.
func recolorAll() {
	a := theApp
	for _, p := range []*Panel{a.left, a.right} {
		if p != nil {
			p.applyLook()
		}
	}
	var walkTables func(w walk.Window)
	walkTables = func(w walk.Window) {
		if tv, ok := w.(*walk.TableView); ok && !isPanelTable(tv) {
			bg, fg := walk.Color(win.GetSysColor(win.COLOR_WINDOW)), walk.Color(win.GetSysColor(win.COLOR_WINDOWTEXT))
			if darkActive {
				bg, fg = dk.edit, dk.fg
			}
			setWalkColor(tv, "themeNormalBGColor", bg)
			setWalkColor(tv, "themeNormalTextColor", fg)
			sb, sf := selColors(true)
			nb, _ := selColors(false)
			setWalkColor(tv, "themeSelectedBGColor", sb)
			setWalkColor(tv, "themeSelectedTextColor", sf)
			setWalkColor(tv, "themeSelectedNotFocusedBGColor", nb)
			tv.Invalidate()
		}
		if tw, ok := w.(*walk.TabWidget); ok {
			for i := 0; i < tw.Pages().Len(); i++ {
				walkTables(tw.Pages().At(i))
			}
		}
		if c, ok := w.(walk.Container); ok && c.Children() != nil {
			for i := 0; i < c.Children().Len(); i++ {
				walkTables(c.Children().At(i))
			}
		}
	}
	for f := range openForms {
		walkTables(f)
	}
}

// themeCheckFn: azonnali témaellenőrzés a rendszer jelzésére (közvetve, hogy ne
// legyen körkörös inicializálás).
var themeCheckFn func()

var recolorFn func()

// pageDark, tabHooked, lazyTabs: a lapok késleltetett átszínezése témaváltáskor.
var (
	pageDark  = map[*walk.TabPage]bool{}
	tabHooked = map[*walk.TabWidget]bool{}
	lazyTabs  bool
)

// themeChecksPending: a Windows egy váltásnál sok jelzést küld; egyszerre csak egy ellenőrzési sor fut.
var themeChecksPending atomic.Bool

func init() {
	recolorFn = recolorAll
	themeCheckFn = func() {
		if theApp.themeMode() == "auto" && systemDark() != darkActive {
			theApp.applyTheme()
		}
	}
}

// panelHeaders: a fájlpanelek oszlopfejlécei.
var panelHeaders = map[win.HWND]bool{}

// openForms: a nyitott párbeszéd- és egyéb ablakok, hogy témaváltáskor (pl. a
// rendszer automatikus világos/sötét váltásakor) azok is átszíneződjenek.
var openForms = map[walk.Form]bool{}

// formDark: melyik témára színeztük utoljára az adott ablakot.
var formDark = map[walk.Form]bool{}

func trackForm(f walk.Form) {
	if openForms[f] {
		return
	}
	openForms[f] = true
	f.Disposing().Attach(func() { delete(openForms, f); delete(formDark, f) })
}

// healForms: ha egy nyitott ablak valamiért kimaradt a témaváltásból, most pótoljuk.
func healForms() {
	for f := range openForms {
		if d, ok := formDark[f]; !ok || d != darkActive {
			themeForm(f, darkActive)
		}
	}
}

// themeForm egy (nyitott) ablakot a mostani témához igazít – világosra vissza is.
func themeForm(f walk.Form, dark bool) {
	formDark[f] = dark
	dlog("téma: nyitott ablak átszínezése (%q) → sötét: %v", f.Title(), dark)
	h := f.Handle()
	allowDarkWindow(h, dark)
	darkTitle(h, dark)
	if dark {
		f.SetBackground(walkBrush(dk.bg))
	} else {
		f.SetBackground(nil)
	}
	themeTree(f, dark)
	if !reapplying { // az applyTheme a végén egyszerre rajzol újra
		win.RedrawWindow(h, nil, 0, win.RDW_INVALIDATE|win.RDW_ERASE|win.RDW_ALLCHILDREN|win.RDW_FRAME)
	}
}

// applyTheme a főablakra (és a menükre) alkalmazza a választott témát.
func (a *App) applyTheme() {
	if !reapplying { // a közben keletkező WM_THEMECHANGED üzenetek ne indítsák újra
		reapplying = true
		defer func() { reapplying = false }()
	}
	dark := a.wantDark()
	start := time.Now()
	// rajzolás felfüggesztése, hogy ne apránként váltson át (és gyorsabb is)
	frozen := []win.HWND{a.mw.Handle()}
	for f := range openForms {
		frozen = append(frozen, f.Handle())
	}
	for _, h := range frozen {
		win.SendMessage(h, win.WM_SETREDRAW, 0, 0)
	}
	defer func() {
		for _, h := range frozen {
			win.SendMessage(h, win.WM_SETREDRAW, 1, 0)
			win.RedrawWindow(h, nil, 0, win.RDW_INVALIDATE|win.RDW_ERASE|win.RDW_ALLCHILDREN|win.RDW_FRAME|win.RDW_UPDATENOW)
		}
		dlog("téma: átváltás %v alatt", time.Since(start).Round(time.Millisecond))
	}()
	darkActive = dark
	setAppDark(dark)
	installMsgBoxHook()
	h := a.mw.Handle()
	allowDarkWindow(h, dark)
	darkTitle(h, dark)
	hookColors(h) // menüsor, eszköztár
	if dark {
		a.mw.SetBackground(walkBrush(dk.bg))
	} else {
		a.mw.SetBackground(nil)
	}
	themeTree(a.mw, dark)
	dlog("téma:   főablak %v", time.Since(start).Round(time.Millisecond))
	for _, p := range []*Panel{a.left, a.right} {
		for _, lv := range []win.HWND{p.lv, p.frozenLV} {
			if lv == 0 {
				continue
			}
			if hdr := win.HWND(win.SendMessage(lv, 0x101F, 0, 0)); hdr != 0 { // LVM_GETHEADER
				panelHeaders[hdr] = true
			}
			if lv == p.frozenLV { // a látható listát a themeTree már átállította
				themeListView(lv, dark)
			}
		}
	}
	dlog("téma: %v, nyitott ablakok: %d", dark, len(openForms))
	lazyTabs = true
	for f := range openForms {
		if f != walk.Form(a.mw) {
			themeForm(f, dark)
		}
	}
	lazyTabs = false
	dlog("téma:   ablakok %v", time.Since(start).Round(time.Millisecond))
	if a.toolbar != nil {
		allowDarkWindow(a.toolbar.Handle(), dark)
		themeOr(a.toolbar.Handle(), dark, "DarkMode_Explorer")
	}
	a.loadLook()
	a.applyLook()
	dlog("téma:   színek %v", time.Since(start).Round(time.Millisecond))
	if a.toolbar != nil && a.toolbar.Actions().Len() > 0 {
		// a gombok ikonjai nem függnek a témától: elég az eszköztár témája
		if dark {
			setTheme(a.toolbar.Handle(), "DarkMode_Explorer", nil)
		} else {
			setTheme(a.toolbar.Handle(), "\x00", nil)
		}
	} else {
		a.buildToolbar()
	}
	win.DrawMenuBar(h)
	win.RedrawWindow(h, nil, 0, win.RDW_INVALIDATE|win.RDW_ERASE|win.RDW_ALLCHILDREN|win.RDW_FRAME)
}

// paintHeaderItem egy oszlopfejlécet rajzol sötétben (szöveg, igazítás, rendezési jel).
func paintHeaderItem(h win.HWND, hdc uintptr, rc win.RECT, item int, hot bool) {
	buf := make([]uint16, 128)
	var hd struct {
		Mask    uint32
		Cxy     int32
		Text    *uint16
		Hbm     uintptr
		TextMax int32
		Fmt     int32
		Param   uintptr
		Image   int32
		Order   int32
		Type    uint32
		_       uint32
		Filter  uintptr
		State   uint32
		_       uint32
	}
	hd.Mask = 0x2 | 0x4 // HDI_TEXT | HDI_FORMAT
	hd.Text = &buf[0]
	hd.TextMax = 127
	win.SendMessage(h, 0x120B, uintptr(item), uintptr(unsafe.Pointer(&hd))) // HDM_GETITEMW
	bg := dk.bg
	if hot {
		bg = dk.hot
	}
	fillRect(hdc, &rc, bg)
	sep := win.RECT{Left: rc.Right - 1, Top: rc.Top + dpiScale(h, 3), Right: rc.Right, Bottom: rc.Bottom - dpiScale(h, 3)}
	fillRect(hdc, &sep, dk.edge)
	line := win.RECT{Left: rc.Left, Top: rc.Bottom - 1, Right: rc.Right, Bottom: rc.Bottom}
	fillRect(hdc, &line, dk.edge)
	font := win.SendMessage(h, win.WM_GETFONT, 0, 0)
	old := win.SelectObject(win.HDC(hdc), win.HGDIOBJ(font))
	defer win.SelectObject(win.HDC(hdc), old)
	win.SetBkMode(win.HDC(hdc), win.TRANSPARENT)
	procSetTextColorGDI.Call(hdc, uintptr(dk.fg))
	pad := dpiScale(h, 6)
	t := win.RECT{Left: rc.Left + pad, Top: rc.Top, Right: rc.Right - pad, Bottom: rc.Bottom}
	text := syscall.UTF16ToString(buf) // az irányjel már a címben van
	flags := uint32(dtSingleLine | dtVCenter | dtEndEllips | dtHidePrefix)
	switch hd.Fmt & 3 {
	case 1:
		flags |= 0x2 // DT_RIGHT
	case 2:
		flags |= dtCenter
	}
	drawText(hdc, text, &t, flags)
}

// cloakWindow eltakarja (vagy újra megmutatja) az ablakot a képernyőn úgy, hogy közben
// a Windows szerint látható marad: elrendezhető és kirajzolható, de a felhasználó nem
// látja a félkész állapotot (DWMWA_CLOAK).
func cloakWindow(h win.HWND, on bool) {
	var v int32
	if on {
		v = 1
	}
	procDwmSetWindowAttribute.Call(uintptr(h), 13, uintptr(unsafe.Pointer(&v)), 4)
}
