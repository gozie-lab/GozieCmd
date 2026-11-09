//go:build linux

package main

// Fájlkeresés (Alt+F7): név, tartalom, méret és dátum szerint; a talált fájlra ugrik.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

type hit struct {
	path  string
	size  int64
	mod   time.Time
	isDir bool
}

func elideMiddle(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	h := (n - 1) / 2
	return fmt.Sprintf(T("%s…%s"), string(r[:h]), string(r[len(r)-(n-1-h):]))
}

func (a *App) findFiles() {
	start := a.active.path
	if a.active.vfs != nil {
		start, _ = os.UserHomeDir()
	}
	w := a.fa.NewWindow(T("Fájlkeresés"))
	w.SetIcon(appIconRes)

	var hits []hit
	var mu sync.Mutex
	var pending []hit
	var stop, running, closed atomic.Bool
	var gen atomic.Int64
	sel := -1

	whereE := widget.NewEntry()
	whereE.SetText(start)
	nameE := widget.NewEntry()
	nameE.SetText(a.setting("findPattern", "*"))
	nameE.CursorColumn = len([]rune(nameE.Text))
	nameE.SetPlaceHolder(T("pl. *.jpg;*.png  vagy egy névrészlet"))
	textE := widget.NewEntry()
	textE.SetPlaceHolder(T("üresen: csak név szerint keres"))
	subC := widget.NewCheck(T("Almappákban is"), nil)
	subC.SetChecked(true)
	caseC := widget.NewCheck(T("Kis/nagybetű különbözik"), nil)
	dirC := widget.NewCheck(T("Mappák is"), nil)
	hiddenC := widget.NewCheck(T("Rejtett mappákban is"), nil)
	minE, maxE, daysE := widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	minE.SetPlaceHolder(T("legalább"))
	maxE.SetPlaceHolder(T("legfeljebb"))
	daysE.SetPlaceHolder(T("nap"))
	status := widget.NewLabel(T("Add meg a feltételeket, majd Keresés indítása (Enter)."))
	status.Truncation = fyne.TextTruncateEllipsis

	list := widget.NewList(func() int { return len(hits) },
		func() fyne.CanvasObject {
			n := widget.NewLabel("")
			n.Truncation = fyne.TextTruncateEllipsis
			d := widget.NewLabel("")
			d.Truncation = fyne.TextTruncateEllipsis
			s := widget.NewLabel("")
			s.Alignment = fyne.TextAlignTrailing
			return container.New(&hitLayout{}, n, d, s, widget.NewLabel(""))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			c := o.(*fyne.Container).Objects
			h := hits[i]
			c[0].(*widget.Label).SetText(filepath.Base(h.path))
			c[1].(*widget.Label).SetText(filepath.Dir(h.path))
			if h.isDir {
				c[2].(*widget.Label).SetText(T("<DIR>"))
			} else {
				c[2].(*widget.Label).SetText(fmtSize(h.size))
			}
			c[3].(*widget.Label).SetText(h.mod.Format("2006.01.02 15:04"))
		})
	list.OnSelected = func(i widget.ListItemID) { sel = i }

	var startB, stopB *widget.Button
	gotoHit := func() {
		if sel < 0 || sel >= len(hits) {
			return
		}
		h := hits[sel]
		stop.Store(true)
		closed.Store(true)
		w.Close()
		p := a.active
		p.exitVFS()
		p.navigate(filepath.Dir(h.path), filepath.Base(h.path), true)
		a.win.RequestFocus()
		p.focus()
	}

	begin := func() {
		if running.Load() {
			return
		}
		root := strings.TrimSpace(whereE.Text)
		if strings.HasPrefix(root, "~") {
			home, _ := os.UserHomeDir()
			root = home + root[1:]
		}
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			status.SetText(T("A keresés helye nem létező mappa."))
			return
		}
		var pats []string
		for _, p := range strings.FieldsFunc(nameE.Text, func(r rune) bool { return r == ';' || r == ',' }) {
			if p = strings.TrimSpace(p); p != "" {
				if p == "*.*" {
					p = "*"
				}
				if !strings.ContainsAny(p, "*?") {
					p = "*" + p + "*"
				}
				pats = append(pats, p)
			}
		}
		if len(pats) == 0 {
			pats = []string{"*"}
		}
		a.settings.Put("findPattern", nameE.Text)
		spec := searchSpec{root: root, patterns: pats, text: textE.Text, matchCase: caseC.Checked,
			subdirs: subC.Checked, dirsToo: dirC.Checked, minSize: -1, maxSize: -1}
		pf := func(s string) (float64, error) {
			return strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
		}
		if v, err := pf(minE.Text); err == nil {
			spec.minSize = int64(v * 1024)
		}
		if v, err := pf(maxE.Text); err == nil {
			spec.maxSize = int64(v * 1024)
		}
		if v, err := strconv.Atoi(strings.TrimSpace(daysE.Text)); err == nil && v > 0 {
			spec.after = time.Now().AddDate(0, 0, -v)
		}
		var needles [][]byte
		if spec.text != "" {
			t := spec.text
			if !spec.matchCase {
				t = strings.ToLower(t)
			}
			needles = [][]byte{[]byte(t), utf16le(t)}
		}
		skipHidden := !hiddenC.Checked
		hits = nil
		sel = -1
		list.UnselectAll()
		list.Refresh()
		stop.Store(false)
		running.Store(true)
		g := gen.Add(1)
		startB.Disable()
		stopB.Enable()
		status.SetText(T("Keresés…"))
		var scanned atomic.Int64
		var curDir atomic.Value
		curDir.Store(root)

		ticker := time.NewTicker(150 * time.Millisecond)
		go func() {
			for range ticker.C {
				if closed.Load() {
					ticker.Stop()
					return
				}
				done := !running.Load()
				fyne.Do(func() {
					if closed.Load() || gen.Load() != g {
						return
					}
					mu.Lock()
					if len(pending) > 0 {
						hits = append(hits, pending...)
						pending = nil
						list.Refresh()
					}
					mu.Unlock()
					if done {
						word := T("Kész")
						if stop.Load() {
							word = T("Leállítva")
						}
						status.SetText(fmt.Sprintf(T("%s: %s, %s."), word, Nf(len(hits), "%d találat"), Nf(int(scanned.Load()), "%d elem átnézve")))
						startB.Enable()
						stopB.Disable()
					} else {
						status.SetText(fmt.Sprintf(T("Keresés… %s, %s – %s"),
							Nf(len(hits), "%d találat"), Nf(int(scanned.Load()), "%d elem átnézve"), elideMiddle(curDir.Load().(string), 70)))
					}
				})
				if done {
					ticker.Stop()
					return
				}
			}
		}()

		go func() {
			defer running.Store(false)
			filepath.WalkDir(spec.root, func(p string, d fs.DirEntry, err error) error {
				if stop.Load() {
					return filepath.SkipAll
				}
				if err != nil {
					if d != nil && d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if p == spec.root {
					return nil
				}
				if d.IsDir() {
					if skipHidden && strings.HasPrefix(d.Name(), ".") {
						return filepath.SkipDir
					}
					if strings.HasPrefix(p, "/proc/") || strings.HasPrefix(p, "/sys/") || p == "/proc" || p == "/sys" {
						return filepath.SkipDir
					}
					curDir.Store(p)
					if !spec.dirsToo || spec.text != "" {
						if !spec.subdirs {
							return filepath.SkipDir
						}
						return nil
					}
				}
				scanned.Add(1)
				if matchName(d.Name(), spec.patterns, spec.matchCase) {
					info, ierr := d.Info()
					if ierr == nil && (d.IsDir() || info.Mode().IsRegular()) &&
						(d.IsDir() || ((spec.minSize < 0 || info.Size() >= spec.minSize) &&
							(spec.maxSize < 0 || info.Size() <= spec.maxSize))) &&
						(spec.after.IsZero() || info.ModTime().After(spec.after)) &&
						(len(needles) == 0 || (!d.IsDir() && fileContains(p, needles, spec.matchCase, &stop))) {
						mu.Lock()
						pending = append(pending, hit{p, info.Size(), info.ModTime(), d.IsDir()})
						mu.Unlock()
					}
				}
				if d.IsDir() && !spec.subdirs {
					return filepath.SkipDir
				}
				return nil
			})
		}()
	}

	startB = widget.NewButton(T("Keresés indítása"), begin)
	startB.Importance = widget.HighImportance
	stopB = widget.NewButton(T("Leállítás"), func() { stop.Store(true) })
	stopB.Disable()
	for _, e := range []*widget.Entry{whereE, nameE, textE, minE, maxE, daysE} {
		e.OnSubmitted = func(string) { begin() }
	}
	browse := widget.NewButton(T("Tallózás…"), func() {
		d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
			if u != nil {
				whereE.SetText(u.Path())
			}
		}, w)
		d.Resize(fyne.NewSize(760, 520))
		d.Show()
	})
	form := container.New(layout.NewFormLayout(),
		widget.NewLabel(T("Keresés helye:")), container.NewBorder(nil, nil, nil, browse, whereE),
		widget.NewLabel(T("Fájlnév:")), nameE,
		widget.NewLabel(T("Tartalmazza:")), textE,
	)
	opts := container.NewHBox(subC, caseC, dirC, hiddenC, layout.NewSpacer(),
		widget.NewLabel(T("Méret (KB):")), sized(minE, 90), sized(maxE, 90),
		widget.NewLabel(T("Az utolsó")), sized(daysE, 60), widget.NewLabel(T("napban")))
	btns := container.NewHBox(startB, stopB, widget.NewButton(T("Ugrás a fájlra"), gotoHit))
	head := container.New(&hitLayout{},
		widget.NewLabelWithStyle(T("Név"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle(T("Mappa"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle(T("Méret"), fyne.TextAlignTrailing, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle(T("Dátum"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	bottom := container.NewBorder(nil, nil, nil, widget.NewButton(T("Bezárás"), w.Close), status)
	w.SetContent(container.NewBorder(container.NewVBox(form, opts, btns, head), bottom, nil, nil, list))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		switch ev.Name {
		case fyne.KeyEscape:
			w.Close()
		case fyne.KeyReturn, fyne.KeyEnter:
			if sel >= 0 {
				gotoHit()
			} else {
				begin()
			}
		}
	})
	a.restoreWinSize(w, "search", 900, 620)
	w.SetOnClosed(func() {
		stop.Store(true)
		closed.Store(true)
		a.saveWinSize(w, "search")
	})
	w.Show()
	w.Canvas().Focus(nameE)
	list.OnSelected = func(i widget.ListItemID) { sel = i }
	_ = func() { gotoHit() }
}

// sized egy elemet adott minimális szélességűre tesz (a HBox egyébként összenyomná).
func sized(o fyne.CanvasObject, w float32) fyne.CanvasObject {
	return container.New(&minWidthLayout{w}, o)
}

type minWidthLayout struct{ w float32 }

func (l *minWidthLayout) Layout(objs []fyne.CanvasObject, s fyne.Size) {
	for _, o := range objs {
		o.Resize(s)
		o.Move(fyne.NewPos(0, 0))
	}
}

func (l *minWidthLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objs {
		if m := o.MinSize(); m.Height > h {
			h = m.Height
		}
	}
	return fyne.NewSize(l.w, h)
}

// hitLayout: név 30%, mappa a maradék, méret és dátum rögzített szélességgel.
type hitLayout struct{}

func (hitLayout) Layout(objs []fyne.CanvasObject, s fyne.Size) {
	sizeW, dateW := float32(110), float32(140)
	nameW := (s.Width - sizeW - dateW) * 0.35
	dirW := s.Width - sizeW - dateW - nameW
	xs := []float32{0, nameW, nameW + dirW, nameW + dirW + sizeW}
	ws := []float32{nameW, dirW, sizeW, dateW}
	for i, o := range objs {
		if i >= 4 {
			break
		}
		o.Move(fyne.NewPos(xs[i], 0))
		o.Resize(fyne.NewSize(ws[i], s.Height))
	}
}

func (hitLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objs {
		if m := o.MinSize(); m.Height > h {
			h = m.Height
		}
	}
	return fyne.NewSize(400, h)
}
