//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// listStyler: a ListBox ettől lesz saját rajzolású (LBS_OWNERDRAWVARIABLE).
// A rajzolást viszont nem a walk végzi, hanem a szülőablak alosztálya
// (lbParentProc), így a kijelölt sor világosban és sötétben is olvasható.
type listStyler struct{ lb **walk.ListBox }

func newListStyler(lb **walk.ListBox) *listStyler { return &listStyler{lb: lb} }

func (s *listStyler) ItemHeightDependsOnWidth() bool { return false }
func (s *listStyler) DefaultItemHeight() int {
	d := 96
	if s.lb != nil && *s.lb != nil {
		if x := (*s.lb).DPI(); x > 0 {
			d = x
		}
	}
	return 20 * d / 96
}
func (s *listStyler) ItemHeight(int, int) int          { return s.DefaultItemHeight() }
func (s *listStyler) StyleItem(st *walk.ListItemStyle) {}

var styledLists = map[win.HWND]*walk.ListBox{}

func listText(lb *walk.ListBox, i int) string {
	switch m := lb.Model().(type) {
	case []string:
		if i >= 0 && i < len(m) {
			return m[i]
		}
	case walk.ListModel:
		if i >= 0 && i < m.ItemCount() {
			return fmt.Sprint(m.Value(i))
		}
	}
	return ""
}

// hookStyledLists a párbeszédablak saját rajzolású listáit köti be.
func hookStyledLists(w walk.Window) {
	if lb, ok := w.(*walk.ListBox); ok {
		if _, ok := lb.ItemStyler().(*listStyler); ok {
			h := lb.Handle()
			styledLists[h] = lb
			subclassOnce(win.GetParent(h), lbParentProc, 0xC020)
			subclassOnce(h, lbSelfProc, 0xC021)
			lb.Disposing().Attach(func() { delete(styledLists, h) })
			hgt := lb.ItemStyler().DefaultItemHeight()
			n := int(int32(win.SendMessage(h, win.LB_GETCOUNT, 0, 0)))
			for i := 0; i < n; i++ {
				win.SendMessage(h, win.LB_SETITEMHEIGHT, uintptr(i), uintptr(hgt))
			}
			win.InvalidateRect(h, nil, true)
		}
	}
	if tv, ok := w.(*walk.TableView); ok && !isPanelTable(tv) {
		// a Windows-téma ne rajzolja a kijelölést a mi színeink fölé
		subclassOnce(tv.Handle(), dlgTableProc, 0xC022)
	}
	if c, ok := w.(walk.Container); ok && c.Children() != nil {
		for i := 0; i < c.Children().Len(); i++ {
			hookStyledLists(c.Children().At(i))
		}
	}
}

var lbParentProc = syscall.NewCallback(func(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	switch msg {
	case 0x002C: // WM_MEASUREITEM
		mis := (*win.MEASUREITEMSTRUCT)(unsafe.Pointer(lp))
		if mis.CtlType == 2 { // ODT_LISTBOX
			for _, lb := range styledLists {
				if win.GetParent(lb.Handle()) == win.HWND(hwnd) {
					mis.ItemHeight = uint32(lb.ItemStyler().DefaultItemHeight())
					return 1
				}
			}
		}
	case 0x002B: // WM_DRAWITEM
		dis := (*win.DRAWITEMSTRUCT)(unsafe.Pointer(lp))
		if lb, ok := styledLists[dis.HwndItem]; ok {
			drawListItem(lb, dis)
			return 1
		}
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
	return r
})

func drawListItem(lb *walk.ListBox, dis *win.DRAWITEMSTRUCT) {
	hdc := uintptr(dis.HDC)
	rc := dis.RcItem
	focused := win.GetFocus() == dis.HwndItem
	sel := dis.ItemState&1 != 0 // ODS_SELECTED
	var bg, fg walk.Color
	switch {
	case darkActive && sel && focused:
		bg, fg = walk.RGB(0, 95, 184), walk.RGB(255, 255, 255)
	case darkActive && sel:
		bg, fg = dk.hot, dk.fg
	case darkActive:
		bg, fg = dk.edit, dk.fg
	case sel && focused:
		bg, fg = walk.Color(win.GetSysColor(win.COLOR_HIGHLIGHT)), walk.Color(win.GetSysColor(win.COLOR_HIGHLIGHTTEXT))
	case sel:
		bg, fg = walk.Color(win.GetSysColor(win.COLOR_BTNFACE)), walk.Color(win.GetSysColor(win.COLOR_WINDOWTEXT))
	default:
		bg, fg = walk.Color(win.GetSysColor(win.COLOR_WINDOW)), walk.Color(win.GetSysColor(win.COLOR_WINDOWTEXT))
	}
	fillRect(hdc, &rc, bg)
	if int32(dis.ItemID) < 0 {
		return
	}
	font := win.SendMessage(dis.HwndItem, win.WM_GETFONT, 0, 0)
	oldF, _, _ := procSelectObjectV.Call(hdc, font)
	procSetBkModeV.Call(hdc, 1)
	procSetTextColorV.Call(hdc, uintptr(fg))
	pad := dpiScale(dis.HwndItem, 4)
	tr := win.RECT{Left: rc.Left + pad, Top: rc.Top, Right: rc.Right - pad, Bottom: rc.Bottom}
	drawText(hdc, listText(lb, int(dis.ItemID)), &tr, 0x20|0x4|0x8000|0x800) // SINGLELINE|VCENTER|END_ELLIPSIS|NOPREFIX
	procSelectObjectV.Call(hdc, oldF)
}

// lbSelfProc: maga a lista is teljesen saját maga rajzol (WM_PAINT), így nem
// számít, hová jut el a WM_DRAWITEM. Kijelölés- és fókuszváltáskor újrarajzol.
var lbSelfProc = syscall.NewCallback(func(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	h := win.HWND(hwnd)
	switch msg {
	case win.WM_ERASEBKGND:
		return 1
	case win.WM_PAINT:
		lb, ok := styledLists[h]
		if !ok {
			break
		}
		var ps win.PAINTSTRUCT
		hdc := win.BeginPaint(h, &ps)
		paintList(lb, h, uintptr(hdc))
		win.EndPaint(h, &ps)
		return 0
	case win.WM_KEYDOWN, win.WM_LBUTTONDOWN, win.WM_LBUTTONUP, win.WM_SETFOCUS, win.WM_KILLFOCUS,
		win.WM_MOUSEWHEEL, win.WM_VSCROLL, win.WM_SIZE, 0x0186 /* LB_SETCURSEL */, 0x0185 /* LB_SETSEL */ :
		r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
		win.InvalidateRect(h, nil, false)
		return r
	case win.WM_MOUSEMOVE:
		r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
		if wp&1 != 0 { // MK_LBUTTON: húzás közben változik a kijelölés
			win.InvalidateRect(h, nil, false)
		}
		return r
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
	return r
})

func paintList(lb *walk.ListBox, h win.HWND, hdc uintptr) {
	var cr win.RECT
	win.GetClientRect(h, &cr)
	bg := walk.Color(win.GetSysColor(win.COLOR_WINDOW))
	if darkActive {
		bg = dk.edit
	}
	fillRect(hdc, &cr, bg)
	n := int(int32(win.SendMessage(h, win.LB_GETCOUNT, 0, 0)))
	cursel := int(int32(win.SendMessage(h, win.LB_GETCURSEL, 0, 0)))
	for i := int(win.SendMessage(h, win.LB_GETTOPINDEX, 0, 0)); i < n; i++ {
		var rc win.RECT
		if int32(win.SendMessage(h, win.LB_GETITEMRECT, uintptr(i), uintptr(unsafe.Pointer(&rc)))) < 0 {
			break
		}
		if rc.Top >= cr.Bottom {
			break
		}
		dis := win.DRAWITEMSTRUCT{HwndItem: h, HDC: win.HDC(hdc), RcItem: rc, ItemID: int32(i)}
		if i == cursel {
			dis.ItemState = 1
		}
		drawListItem(lb, &dis)
	}
}

func isPanelTable(tv *walk.TableView) bool {
	return theApp != nil && ((theApp.left != nil && theApp.left.tv == tv) || (theApp.right != nil && theApp.right.tv == tv))
}

var dlgTableProc = syscall.NewCallback(func(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	if msg == 0x004E { // WM_NOTIFY
		if (*nmCustomDraw)(unsafe.Pointer(lp)).Hdr.Code == nmCustomDrawCode {
			stripSelectionState(lp)
		}
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
	return r
})

// selColors: a kijelölt sor színei párbeszédablakok tábláiban.
func selColors(focused bool) (walk.Color, walk.Color) {
	if darkActive {
		if focused {
			return walk.RGB(0, 95, 184), walk.RGB(255, 255, 255)
		}
		return walk.RGB(70, 70, 76), dk.fg
	}
	if focused {
		return walk.Color(win.GetSysColor(win.COLOR_HIGHLIGHT)), walk.Color(win.GetSysColor(win.COLOR_HIGHLIGHTTEXT))
	}
	return walk.RGB(215, 215, 215), walk.Color(win.GetSysColor(win.COLOR_WINDOWTEXT))
}
