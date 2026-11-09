//go:build linux

package main

// Nézetek (részletes, rövid, bélyegkép) és a gyorsnézet panel (Linux-felület).

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	viewDetail = iota
	viewBrief
	viewThumbs
)

var viewNames = []string{T("részletes"), T("rövid"), T("bélyegkép")}

func (p *Panel) thumbSize() float32 { return 112 * p.app.uiScale }

// cellSize: egy sor (részletes), illetve egy rácscella mérete.
func (p *Panel) cellSize() fyne.Size {
	switch p.viewMode {
	case viewBrief:
		return fyne.NewSize(230*p.app.listTextSize()/14, p.rowHeight())
	case viewThumbs:
		t := p.thumbSize()
		return fyne.NewSize(t+20, t+p.rowHeight()+12)
	}
	return fyne.NewSize(100, p.rowHeight())
}

func (rr *rowRenderer) layoutBrief(s fyne.Size) {
	r := rr.r
	for i, c := range r.cells {
		if i != colName {
			c.Hide()
		}
	}
	c := r.cells[colName]
	isz := c.TextSize + 4
	r.icon.Resize(fyne.NewSize(isz, isz))
	r.icon.Move(fyne.NewPos(cellPad, (s.Height-isz)/2))
	avail := s.Width - isz - 3*cellPad
	c.Show()
	c.Alignment = fyne.TextAlignLeading
	c.Text = fitText(r.nameFull, avail, c.TextSize, c.TextStyle)
	h := fyne.MeasureText("Ág", c.TextSize, c.TextStyle).Height
	c.Resize(fyne.NewSize(avail, h))
	c.Move(fyne.NewPos(isz+2*cellPad, (s.Height-h)/2))
}

func (rr *rowRenderer) layoutThumb(s fyne.Size) {
	r := rr.r
	for i, c := range r.cells {
		if i != colName {
			c.Hide()
		}
	}
	t := r.p.thumbSize()
	isz := t
	if r.icon.Image == nil { // ikon: kisebb, hogy ne legyen elmosódott óriás
		isz = t * 0.6
	}
	r.icon.Resize(fyne.NewSize(isz, isz))
	r.icon.Move(fyne.NewPos((s.Width-isz)/2, 6+(t-isz)/2))
	c := r.cells[colName]
	c.Show()
	c.Alignment = fyne.TextAlignCenter
	c.Text = fitText(r.nameFull, s.Width-8, c.TextSize, c.TextStyle)
	h := fyne.MeasureText("Ág", c.TextSize, c.TextStyle).Height
	c.Resize(fyne.NewSize(s.Width-8, h))
	c.Move(fyne.NewPos(4, t+8))
}

func (p *Panel) setViewMode(m int) {
	if m == p.viewMode {
		return
	}
	p.viewMode = m
	p.app.settings.Put(p.side+".view", itoa(m))
	p.view.applyMode()
	p.colHead.Hidden = m != viewDetail || p.qvBox.Visible()
	p.relayout()
	p.view.refreshAll()
	p.view.ensureVisible(p.cursor)
}

// indexAt: melyik elem van az abszolút képernyőpont alatt (-1: egyik sem).
func (p *Panel) indexAt(pos fyne.Position) int {
	if !p.contains(pos) {
		return -1
	}
	vp := fyne.CurrentApp().Driver().AbsolutePositionForObject(p.view)
	x, y := pos.X-vp.X, pos.Y-vp.Y+p.view.scrollOffset()
	cs := p.cellSize()
	if p.view.grid == nil {
		return int(y / cs.Height)
	}
	col := int(x / cs.Width)
	if cols := p.view.columns(); col >= cols {
		return -1
	}
	return int(y/cs.Height)*p.view.columns() + col
}

// ── bélyegképek ──

type thumbCache struct {
	mu      sync.Mutex
	imgs    map[string]image.Image
	loading map[string]bool
	sem     chan struct{}
}

var thumbs = &thumbCache{imgs: map[string]image.Image{}, loading: map[string]bool{}, sem: make(chan struct{}, 3)}

// get a kész bélyegképet adja; ha még nincs, a háttérben elkészíti, majd meghívja a ready-t.
func (c *thumbCache) get(path string, mod time.Time, size int, ready func()) image.Image {
	key := path + "|" + mod.String() + "|" + itoa(size)
	c.mu.Lock()
	defer c.mu.Unlock()
	if img, ok := c.imgs[key]; ok {
		return img
	}
	if c.loading[key] {
		return nil
	}
	c.loading[key] = true
	go func() {
		c.sem <- struct{}{}
		img := makeThumb(path, size)
		<-c.sem
		c.mu.Lock()
		delete(c.loading, key)
		if img != nil {
			if len(c.imgs) > 2000 { // egyszerű korlát a memóriára
				c.imgs = map[string]image.Image{}
			}
			c.imgs[key] = img
		}
		c.mu.Unlock()
		if img != nil {
			fyne.Do(ready)
		}
	}()
	return nil
}

// applyThumb bélyegkép nézetben a képfájlok ikonja helyére a kicsinyített képet teszi.
func (p *Panel) applyThumb(r *fileRow, e *Entry) {
	r.icon.Image = nil
	if p.viewMode != viewThumbs || e.IsDir || p.vfs != nil || !isImageName(e.Name) {
		return
	}
	size := int(p.thumbSize() * 2) // éles marad nagy felbontású kijelzőn is
	name := e.Name
	img := thumbs.get(e.Path, e.ModTime, size, func() {
		for i, it := range p.items {
			if it.Name == name {
				p.refreshRow(i)
				return
			}
		}
	})
	if img != nil {
		r.icon.Image = img
		r.icon.Resource = nil
	}
}

// ── gyorsnézet (Ctrl+Q) ──

// toggleQuickView: a másik panel helyén a kurzor alatti fájl tartalma látszik.
func (a *App) toggleQuickView() {
	a.qvOn = !a.qvOn
	a.placeQuickView()
}

func (a *App) placeQuickView() {
	for _, p := range []*Panel{a.left, a.right} {
		show := a.qvOn && p != a.active
		if p.qvBox == nil {
			continue
		}
		if show {
			p.view.Hide()
			p.colHead.Hide() // a gyorsnézetnél nincs értelme az oszlopfejlécnek
			p.qvBox.Show()
		} else {
			p.qvBox.Hide()
			p.view.Show()
			if p.viewMode == viewDetail {
				p.colHead.Show()
			}
		}
		p.relayout()
	}
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
	a.qvTimer = time.AfterFunc(120*time.Millisecond, func() { fyne.Do(a.updateQuickView) })
}

func (a *App) updateQuickView() {
	if !a.qvOn {
		return
	}
	src := a.active
	dst := a.other(src)
	if dst.qvBox == nil {
		return
	}
	e := src.currentEntry()
	var content fyne.CanvasObject
	head := ""
	switch {
	case e == nil || e.IsParent:
		content = widget.NewLabel(T("Állj egy fájlra vagy mappára a másik panelen."))
	case src.vfs != nil:
		head = e.Name
		content = widget.NewLabel(fmt.Sprintf(T("Archívumban és FTP-n a gyorsnézet nem mutat tartalmat.\nMéret: %s"), src.sizeText(e)))
	case e.IsDir:
		head = e.Name + "/"
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
		txt := fmt.Sprintf(T("Mappa\n\n%s, %s\nMódosítva: %s"), Nf(n, "%d fájl"), Nf(sub, "%d almappa"), dateText(e))
		if s, ok := src.dirSizes[e.Name]; ok && s >= 0 {
			txt += fmt.Sprintf(T("\nMéret: %s"), fmtHuman(uint64(s)))
		} else {
			txt += T("\n(a méretet a Szóköz kiszámolja)")
		}
		content = widget.NewLabel(txt)
	case isImageName(e.Name):
		head = fmt.Sprintf(T("%s   %s bájt"), e.Name, fmtSize(e.Size))
		img := canvas.NewImageFromResource(theme.FileImageIcon())
		img.FillMode = canvas.ImageFillContain
		path, mod := e.Path, e.ModTime
		if pic := thumbs.get(path, mod, 1600, func() { a.updateQuickView() }); pic != nil {
			img = canvas.NewImageFromImage(pic)
			img.FillMode = canvas.ImageFillContain
		}
		content = img
	default:
		head = fmt.Sprintf(T("%s   %s bájt   %s"), e.Name, fmtSize(e.Size), dateText(e))
		content = quickText(e.Path)
	}
	title := widget.NewLabelWithStyle(head, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	title.Truncation = fyne.TextTruncateEllipsis
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	dst.qvBox.Objects = []fyne.CanvasObject{container.NewBorder(title, nil, nil, nil, container.NewStack(bg, content))}
	dst.qvBox.Refresh()
}

// quickText a fájl elejét mutatja (szövegként, vagy ha bináris, hexában).
func quickText(path string) fyne.CanvasObject {
	f, err := os.Open(path)
	if err != nil {
		return widget.NewLabel(errText(err))
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	n, _ := f.Read(buf)
	data := buf[:n]
	var text string
	if isBinary(data) {
		if len(data) > 4096 {
			data = data[:4096]
		}
		text = hexDump(data)
	} else {
		text = strings.ReplaceAll(strings.ReplaceAll(decodeText(data, guessEncoding(data)), "\r\n", "\n"), "\t", "    ")
	}
	tg := widget.NewTextGridFromString(text)
	tg.Scroll = fyne.ScrollBoth
	return tg
}

var _ = color.Transparent
