//go:build windows

package main

// Nézetek (részletes, rövid, bélyegkép) és a gyorsnézet panel (Windows-felület).
// A rövid nézet a lista saját „lista” módja; a bélyegkép nézetben a lista ikon
// módban fut, az elemeket pedig mi rajzoljuk (kép vagy nagy ikon, alatta a név).

import (
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/text/encoding/charmap"
)

const (
	viewDetail = iota
	viewBrief
	viewThumbs
)

var (
	procImageListCreate  = comctl32.NewProc("ImageList_Create")
	procImageListGetIcon = comctl32.NewProc("ImageList_GetIcon")
	procSHGetImageList   = shell32.NewProc("SHGetImageList")
	procSHGetFileInfoW2  = shell32.NewProc("SHGetFileInfoW")
	procDrawIconEx       = user32.NewProc("DrawIconEx")
	gdi32v               = syscall.NewLazyDLL("gdi32.dll")
	procCreateDIBSection = gdi32v.NewProc("CreateDIBSection")
	procCreateCompatDC   = gdi32v.NewProc("CreateCompatibleDC")
	procDeleteDC         = gdi32v.NewProc("DeleteDC")
	procSelectObjectV    = gdi32v.NewProc("SelectObject")
	procAlphaBlend       = syscall.NewLazyDLL("msimg32.dll").NewProc("AlphaBlend")
	procSetBkModeV       = gdi32v.NewProc("SetBkMode")
	procSetTextColorV    = gdi32v.NewProc("SetTextColor")
)

func (p *Panel) thumbPx() int32 {
	dpi := int32(win.GetDpiForWindow(p.lv))
	if dpi == 0 {
		dpi = 96
	}
	return 112 * dpi / 96
}

func (p *Panel) loadViewMode() {
	v, _ := p.app.settings.Get(p.side + ".view")
	if n, err := strconv.Atoi(v); err == nil && n >= viewDetail && n <= viewThumbs {
		p.setViewMode(n)
	}
}

func (p *Panel) setViewMode(m int) {
	if p.lv == 0 {
		return
	}
	p.viewMode = m
	p.app.settings.Put(p.side+".view", strconv.Itoa(m))
	const lvmSetView, lvmSetColumnWidth, lvmSetImageList, lvmSetIconSpacing = 0x108E, 0x101E, 0x1003, 0x1035
	switch m {
	case viewDetail:
		win.SendMessage(p.lv, lvmSetView, 1, 0) // LV_VIEW_DETAILS
		p.fitColumns()
	case viewBrief:
		win.SendMessage(p.lv, lvmSetView, 3, 0) // LV_VIEW_LIST
		p.fitColumns()
	case viewThumbs:
		t := p.thumbPx()
		if p.thumbIml == 0 || p.thumbImlSize != t {
			h, _, _ := procImageListCreate.Call(uintptr(t), uintptr(t), 0x20, 1, 1) // ILC_COLOR32
			p.thumbIml, p.thumbImlSize = h, t
		}
		win.SendMessage(p.lv, lvmSetImageList, 0, p.thumbIml) // LVSIL_NORMAL: ettől lesz nagy a cella
		win.SendMessage(p.lv, lvmSetView, 0, 0)               // LV_VIEW_ICON
		win.SendMessage(p.lv, lvmSetIconSpacing, 0, uintptr(win.MAKELONG(uint16(t+28), uint16(t+48))))
	}
	if r := p.cursorRow(); r >= 0 {
		win.SendMessage(p.lv, 0x1013, uintptr(r), 0) // LVM_ENSUREVISIBLE
	}
	p.repaint()
}

// briefWidth: a rövid nézet oszlopszélessége.
func (p *Panel) briefWidth() int {
	dpi := int(win.GetDpiForWindow(p.lv))
	if dpi == 0 {
		dpi = 96
	}
	return 230 * dpi / 96
}

// ── bélyegképek ──

type thumbBmp struct {
	hbm  uintptr
	w, h int32
}

type winThumbs struct {
	mu      sync.Mutex
	bmps    map[string]*thumbBmp
	loading map[string]bool
	sem     chan struct{}
	icons   map[int32]uintptr // rendszer-ikonindex → nagy ikon
	jumbo   uintptr
	xlarge  uintptr
}

var wthumbs = &winThumbs{bmps: map[string]*thumbBmp{}, loading: map[string]bool{}, sem: make(chan struct{}, 3), icons: map[int32]uintptr{}}

// thumbFor a kész bélyegképet adja, vagy elindítja a betöltését (utána a redraw fut).
func (p *Panel) thumbFor(e *Entry, size int) *thumbBmp {
	key := e.Path + "|" + e.ModTime.String() + "|" + strconv.Itoa(size)
	c := wthumbs
	c.mu.Lock()
	if b, ok := c.bmps[key]; ok {
		c.mu.Unlock()
		return b
	}
	if c.loading[key] {
		c.mu.Unlock()
		return nil
	}
	c.loading[key] = true
	c.mu.Unlock()
	path, name := e.Path, e.Name
	go func() {
		c.sem <- struct{}{}
		img := makeThumb(path, size)
		<-c.sem
		p.app.mw.Synchronize(func() {
			c.mu.Lock()
			delete(c.loading, key)
			if img != nil {
				if b := dibFromImage(img); b != nil {
					c.bmps[key] = b
				}
			}
			c.mu.Unlock()
			for i, it := range p.model.items {
				if it.Name == name {
					p.redrawRow(i)
					break
				}
			}
		})
	}()
	return nil
}

// dibFromImage: előszorzott alfájú 32 bites DIB (az AlphaBlend ezt várja).
func dibFromImage(img image.Image) *thumbBmp {
	b := img.Bounds()
	w, h := int32(b.Dx()), int32(b.Dy())
	type bmiHeader struct {
		Size                          uint32
		Width, Height                 int32
		Planes, BitCount              uint16
		Compression, SizeImage        uint32
		XPels, YPels, ClrUsed, ClrImp int32
	}
	hdr := bmiHeader{Size: 40, Width: w, Height: -h, Planes: 1, BitCount: 32}
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&hdr)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		return nil
	}
	px := unsafe.Slice((*byte)(bits), int(w*h*4))
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA() // már előszorzott
			px[i], px[i+1], px[i+2], px[i+3] = byte(bl>>8), byte(g>>8), byte(r>>8), byte(a>>8)
			i += 4
		}
	}
	return &thumbBmp{hbm: hbm, w: w, h: h}
}

// sysIcon: a fájltípus nagy (256 képpontos) rendszer-ikonja.
func sysIcon(path string, isDir, virtual bool) uintptr {
	type shfi struct {
		HIcon       uintptr
		IIcon       int32
		Attr        uint32
		DisplayName [260]uint16
		TypeName    [80]uint16
	}
	var fi shfi
	flags := uintptr(0x4000) // SHGFI_SYSICONINDEX
	attr := uintptr(0x80)
	if isDir {
		attr = 0x10
	}
	if virtual {
		flags |= 0x10 // SHGFI_USEFILEATTRIBUTES: nem létező (archívumbeli) fájl
	}
	pp, _ := syscall.UTF16PtrFromString(path)
	procSHGetFileInfoW2.Call(uintptr(unsafe.Pointer(pp)), attr, uintptr(unsafe.Pointer(&fi)), unsafe.Sizeof(fi), flags)
	c := wthumbs
	if h, ok := c.icons[fi.IIcon]; ok {
		return h
	}
	if c.jumbo == 0 {
		iid := guid{0x46EB5926, 0x582E, 0x4017, [8]byte{0x9F, 0xDF, 0xE8, 0x99, 0x8D, 0xAA, 0x09, 0x50}} // IID_IImageList
		procSHGetImageList.Call(4, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&c.jumbo)))     // SHIL_JUMBO
		if c.jumbo == 0 {
			procSHGetImageList.Call(2, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&c.jumbo))) // SHIL_EXTRALARGE
		}
	}
	if c.jumbo == 0 {
		return 0
	}
	h, _, _ := procImageListGetIcon.Call(c.jumbo, uintptr(fi.IIcon), 1) // ILD_TRANSPARENT
	if h != 0 && iconIsPadded(h) {
		// Sok program (pl. tömörítők) csak kis ikont ad: a 256-os listában
		// ez a bal felső sarokba kerül. Ilyenkor a 48-as listából vesszük.
		if c.xlarge == 0 {
			iid := guid{0x46EB5926, 0x582E, 0x4017, [8]byte{0x9F, 0xDF, 0xE8, 0x99, 0x8D, 0xAA, 0x09, 0x50}}
			procSHGetImageList.Call(2, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&c.xlarge))) // SHIL_EXTRALARGE
		}
		if c.xlarge != 0 {
			if h2, _, _ := procImageListGetIcon.Call(c.xlarge, uintptr(fi.IIcon), 1); h2 != 0 {
				win.DestroyIcon(win.HICON(h))
				h = h2
			}
		}
	}
	c.icons[fi.IIcon] = h
	return h
}

// iconIsPadded igaz, ha az ikon látható része csak a bal felső negyedet tölti ki.
func iconIsPadded(h uintptr) bool {
	var ii win.ICONINFO
	if !win.GetIconInfo(win.HICON(h), &ii) {
		return false
	}
	defer func() {
		if ii.HbmColor != 0 {
			win.DeleteObject(win.HGDIOBJ(ii.HbmColor))
		}
		if ii.HbmMask != 0 {
			win.DeleteObject(win.HGDIOBJ(ii.HbmMask))
		}
	}()
	if ii.HbmColor == 0 {
		return false
	}
	var bm win.BITMAP
	if win.GetObject(win.HGDIOBJ(ii.HbmColor), unsafe.Sizeof(bm), unsafe.Pointer(&bm)) == 0 {
		return false
	}
	w, ht := int(bm.BmWidth), int(bm.BmHeight)
	if w < 128 || ht < 128 {
		return false
	}
	var bi win.BITMAPINFO
	bi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bi.BmiHeader))
	bi.BmiHeader.BiWidth = int32(w)
	bi.BmiHeader.BiHeight = -int32(ht)
	bi.BmiHeader.BiPlanes = 1
	bi.BmiHeader.BiBitCount = 32
	buf := make([]byte, w*ht*4)
	dc := win.GetDC(0)
	n := win.GetDIBits(dc, ii.HbmColor, 0, uint32(ht), &buf[0], &bi, 0)
	win.ReleaseDC(0, dc)
	if n == 0 {
		return false
	}
	maxX, maxY, any := 0, 0, false
	for y := 0; y < ht; y++ {
		row := buf[y*w*4:]
		for x := 0; x < w; x++ {
			if row[x*4+3] != 0 {
				any = true
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	return any && maxX < w/2 && maxY < ht/2
}

// drawThumbItem egy elemet rajzol bélyegkép nézetben (a lista helyett).
func (p *Panel) drawThumbItem(hdc uintptr, row int) {
	if row < 0 || row >= len(p.model.items) {
		return
	}
	e := p.model.items[row]
	var rc win.RECT // LVIR_BOUNDS
	if win.SendMessage(p.lv, 0x100E, uintptr(row), uintptr(unsafe.Pointer(&rc))) == 0 {
		return
	}
	marked := !e.IsParent && p.model.marked[e.Name]
	bg, fg := cur.ListBg, cur.ListFont.Color
	if c, ok := p.app.typeColorOf(e); ok {
		fg = c
	}
	if marked {
		bg, fg = cur.MarkBg, cur.MarkFont.Color
	}
	switch {
	case row == p.dropRow:
		bg = dropColor
	case row == p.cursorRow():
		if p.tv.Focused() {
			bg, fg = cur.CursorBg, cur.CursorFg
		} else {
			bg, fg = cur.CursorInBg, cur.CurInFg
		}
		if marked {
			fg = cur.MarkFont.Color
		}
	}
	fillRect(hdc, &rc, bg)
	t := p.thumbPx()
	w := rc.Right - rc.Left
	top := rc.Top + 4
	drawn := false
	if !e.IsDir && p.vfs == nil && isImageName(e.Name) {
		if b := p.thumbFor(e, int(t)); b != nil {
			mdc, _, _ := procCreateCompatDC.Call(hdc)
			old, _, _ := procSelectObjectV.Call(mdc, b.hbm)
			x := rc.Left + (w-b.w)/2
			y := top + (t-b.h)/2
			blend := uintptr(0x00 | 0x00<<8 | 0xFF<<16 | 0x01<<24) // AC_SRC_OVER, 255, AC_SRC_ALPHA
			procAlphaBlend.Call(hdc, uintptr(x), uintptr(y), uintptr(b.w), uintptr(b.h), mdc, 0, 0, uintptr(b.w), uintptr(b.h), blend)
			procSelectObjectV.Call(mdc, old)
			procDeleteDC.Call(mdc)
			drawn = true
		}
	}
	if !drawn {
		path := e.Path
		if e.IsParent {
			path = p.path
		}
		if ic := sysIcon(path, e.IsDir, p.vfs != nil); ic != 0 {
			sz := t * 6 / 10
			procDrawIconEx.Call(hdc, uintptr(rc.Left+(w-sz)/2), uintptr(top+(t-sz)/2), ic, uintptr(sz), uintptr(sz), 0, 0, 3)
		}
	}
	name := e.Name
	if e.IsParent {
		name = ".."
	}
	font := win.SendMessage(p.lv, win.WM_GETFONT, 0, 0)
	oldF, _, _ := procSelectObjectV.Call(hdc, font)
	procSetBkModeV.Call(hdc, 1) // TRANSPARENT
	procSetTextColorV.Call(hdc, uintptr(fg))
	tr := win.RECT{Left: rc.Left + 3, Top: top + t + 4, Right: rc.Right - 3, Bottom: rc.Bottom - 2}
	drawText(hdc, name, &tr, dtCenter|dtEndEllips|0x10|0x2000) // DT_WORDBREAK | DT_EDITCONTROL
	procSelectObjectV.Call(hdc, oldF)
}

// ── gyorsnézet (Ctrl+Q) ──

func (a *App) toggleQuickView() {
	a.qvOn = !a.qvOn
	a.placeQuickView()
}

func (a *App) placeQuickView() {
	a.mw.SetSuspended(true)
	for _, p := range []*Panel{a.left, a.right} {
		show := a.qvOn && p != a.active
		p.tv.SetVisible(!show)
		p.qvBox.SetVisible(show)
	}
	a.mw.SetSuspended(false)
	if a.qvOn {
		a.updateQuickView()
	}
}

func (a *App) scheduleQuickView() {
	if !a.qvOn {
		return
	}
	if a.qvTimer != nil {
		a.qvTimer.Stop()
	}
	a.qvTimer = time.AfterFunc(150*time.Millisecond, func() { a.mw.Synchronize(a.updateQuickView) })
}

func (a *App) updateQuickView() {
	if !a.qvOn {
		return
	}
	src := a.active
	dst := a.other()
	if dst == src {
		return
	}
	e := src.currentEntry()
	showText := func(title, text string) {
		dst.qvTitle.SetText(title)
		dst.qvImage.SetVisible(false)
		dst.qvText.SetVisible(true)
		dst.qvText.SetText(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n"))
	}
	switch {
	case e == nil || e.IsParent:
		showText("", T("Állj egy fájlra vagy mappára a másik panelen."))
	case src.vfs != nil:
		showText(e.Name, fmt.Sprintf(T("Archívumban és FTP-n a gyorsnézet nem mutat tartalmat.\nMéret: %s"), fmtSize(e.Size)))
	case e.IsDir:
		n, sub := 0, 0
		if des, err := os.ReadDir(e.Path); err == nil {
			for _, d := range des {
				if d.IsDir() {
					sub++
				} else {
					n++
				}
			}
		}
		txt := fmt.Sprintf(T("Mappa\n\n%s, %s\nMódosítva: %s"), Nf(n, "%d fájl"), Nf(sub, "%d almappa"), e.ModTime.Format("2006.01.02 15:04"))
		if s, ok := src.model.dirSizes[e.Name]; ok && s >= 0 {
			txt += fmt.Sprintf(T("\nMéret: %s"), fmtHuman(uint64(s)))
		} else {
			txt += T("\n(a méretet a Szóköz kiszámolja)")
		}
		showText(e.Name+`\`, txt)
	case isImageName(e.Name):
		dst.qvTitle.SetText(fmt.Sprintf(T("%s   %s bájt"), e.Name, fmtSize(e.Size)))
		dst.qvText.SetVisible(false)
		dst.qvImage.SetVisible(true)
		dst.qvImage.SetImage(nil)
		path, gen := e.Path, a.qvGen+1
		a.qvGen = gen
		go func() {
			img := makeThumb(path, 1600)
			a.mw.Synchronize(func() {
				if a.qvGen != gen || img == nil {
					return
				}
				if bmp, err := walk.NewBitmapFromImageForDPI(img, 96); err == nil {
					if old := dst.qvImage.Image(); old != nil {
						defer old.Dispose()
					}
					dst.qvImage.SetImage(bmp)
				}
			})
		}()
	default:
		f, err := os.Open(e.Path)
		if err != nil {
			showText(e.Name, errText(err))
			return
		}
		buf := make([]byte, 64<<10)
		n, _ := f.Read(buf)
		f.Close()
		data := buf[:n]
		var text string
		if isBinaryData(data) {
			if len(data) > 4096 {
				data = data[:4096]
			}
			text = hexDump(data)
		} else {
			text = decodeQuick(data)
		}
		showText(fmt.Sprintf(T("%s   %s bájt   %s"), e.Name, fmtSize(e.Size), e.ModTime.Format("2006.01.02 15:04")), text)
	}
}

func isBinaryData(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

// decodeQuick: UTF-8, különben Windows-1250 (közép-európai) szöveg.
func decodeQuick(b []byte) string {
	if utf8.Valid(b) {
		return strings.TrimPrefix(string(b), "\uFEFF")
	}
	if s, err := charmap.Windows1250.NewDecoder().Bytes(b); err == nil {
		return string(s)
	}
	return strings.ToValidUTF8(string(b), "?")
}
