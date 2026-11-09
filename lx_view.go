//go:build linux

package main

// Nézőke (F3): szöveg (kódolásválasztással), hexa és formázott Markdown.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/text/encoding/charmap"
)

const viewLimit = 4 << 20

var encodings = []struct {
	label string
	cm    *charmap.Charmap
}{
	{"UTF-8", nil},
	{T("Windows-1250 (közép-európai)"), charmap.Windows1250},
	{"ISO-8859-2 (Latin-2)", charmap.ISO8859_2},
	{"CP852 (DOS)", charmap.CodePage852},
}

func decodeText(b []byte, enc int) string {
	if enc > 0 && enc < len(encodings) {
		if s, err := encodings[enc].cm.NewDecoder().Bytes(b); err == nil {
			return string(s)
		}
	}
	return strings.ToValidUTF8(string(b), "�")
}

func guessEncoding(b []byte) int {
	if utf8.Valid(b) {
		return 0
	}
	return 1
}

func hexDump(b []byte) string {
	var sb strings.Builder
	for off := 0; off < len(b); off += 16 {
		end := off + 16
		if end > len(b) {
			end = len(b)
		}
		fmt.Fprintf(&sb, "%08X  ", off)
		for i := off; i < off+16; i++ {
			if i < end {
				fmt.Fprintf(&sb, "%02X ", b[i])
			} else {
				sb.WriteString("   ")
			}
			if i == off+7 {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(" ")
		for i := off; i < end; i++ {
			c := b[i]
			if c < 32 || c > 126 {
				c = '.'
			}
			sb.WriteByte(c)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func isBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(b[:n], 0) >= 0
}

func isMarkdownName(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	}
	return false
}

func (a *App) showViewer(path string) {
	f, err := os.Open(path)
	if err != nil {
		a.warn(fmt.Sprintf(T("A fájl nem nyitható meg:\n%s"), errText(err)))
		return
	}
	st, _ := f.Stat()
	buf := make([]byte, viewLimit)
	n, _ := f.Read(buf)
	f.Close()
	data := buf[:n]
	truncated := st != nil && st.Size() > int64(n)

	w := a.fa.NewWindow(fmt.Sprintf(T("%s – Nézőke"), filepath.Base(path)))
	w.SetIcon(appIconRes)
	modes := []string{T("Szöveg"), T("Hexa")}
	md := isMarkdownName(path)
	if md {
		modes = append([]string{T("Markdown")}, modes...)
	}
	enc := guessEncoding(data)
	mode := modes[0]
	if isBinary(data) {
		mode = T("Hexa")
	}
	wrap := false

	holder := container.NewStack()
	info := widget.NewLabel("")
	var encSel *widget.Select
	render := func() {
		var obj fyne.CanvasObject
		switch mode {
		case "Markdown":
			rt := widget.NewRichTextFromMarkdown(decodeText(data, enc))
			rt.Wrapping = fyne.TextWrapWord
			obj = container.NewScroll(container.NewPadded(rt))
		case T("Hexa"):
			hb := data
			if len(hb) > 1<<20 {
				hb = hb[:1<<20]
			}
			tg := widget.NewTextGridFromString(hexDump(hb))
			tg.Scroll = fyne.ScrollBoth
			obj = tg
		default:
			text := strings.ReplaceAll(decodeText(data, enc), "\r\n", "\n")
			text = strings.ReplaceAll(text, "\t", "    ")
			if wrap {
				l := widget.NewLabel(text)
				l.Wrapping = fyne.TextWrapWord
				l.TextStyle.Monospace = true
				obj = container.NewVScroll(l)
			} else {
				tg := widget.NewTextGridFromString(text)
				tg.Scroll = fyne.ScrollBoth
				obj = tg
			}
		}
		holder.Objects = []fyne.CanvasObject{obj}
		holder.Refresh()
		if encSel != nil {
			if mode == T("Hexa") {
				encSel.Disable()
			} else {
				encSel.Enable()
			}
		}
	}
	modeSel := widget.NewSelect(modes, func(s string) { mode = s; render() })
	modeSel.SetSelected(mode)
	var names []string
	for _, e := range encodings {
		names = append(names, e.label)
	}
	encSel = widget.NewSelect(names, func(s string) {
		for i, e := range encodings {
			if e.label == s {
				enc = i
			}
		}
		render()
	})
	encSel.SetSelected(names[enc])
	wrapChk := widget.NewCheck(T("Sortörés"), func(b bool) { wrap = b; render() })
	sz := fmt.Sprintf(T("%s bájt"), fmtSize(int64(n)))
	if st != nil {
		sz = fmt.Sprintf(T("%s bájt"), fmtSize(st.Size()))
	}
	if truncated {
		sz += fmt.Sprintf(T("  (az első %s látszik)"), fmtHuman(uint64(n)))
	}
	info.SetText(sz)
	closeB := widget.NewButton(T("Bezárás"), w.Close)
	top := container.NewBorder(nil, nil, container.NewHBox(modeSel, encSel, wrapChk, info), closeB)
	w.SetContent(container.NewBorder(top, nil, nil, nil, holder))
	render()
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		switch ev.Name {
		case fyne.KeyEscape, fyne.KeyF3, fyne.KeyF10:
			w.Close()
		}
	})
	w.Canvas().SetOnTypedRune(func(r rune) {
		if r == 'q' {
			w.Close()
		}
	})
	a.restoreWinSize(w, "viewer", 980, 680)
	w.SetOnClosed(func() { a.saveWinSize(w, "viewer") })
	w.Show()
}

func (a *App) restoreWinSize(w fyne.Window, key string, dw, dh int) {
	ww, hh := a.settingInt("lx.win."+key+".w", dw), a.settingInt("lx.win."+key+".h", dh)
	w.Resize(fyne.NewSize(float32(ww), float32(hh)))
}

func (a *App) saveWinSize(w fyne.Window, key string) {
	s := w.Canvas().Size()
	if s.Width > 100 && s.Height > 100 {
		a.settings.Put("lx.win."+key+".w", fmt.Sprint(int(s.Width)))
		a.settings.Put("lx.win."+key+".h", fmt.Sprint(int(s.Height)))
		a.settings.Save()
	}
}
