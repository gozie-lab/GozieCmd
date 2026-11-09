//go:build linux

package main

// Mappák szinkronizálása (Linux-felület).

import (
	"fmt"
	"image/color"
	"os"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// syncRow: egy sor; kattintásra az irány vált.
type syncRow struct {
	widget.BaseWidget
	left, act, right *canvas.Text
	bg               *canvas.Rectangle
	onTap            func()
}

func newSyncRow() *syncRow {
	r := &syncRow{bg: canvas.NewRectangle(color.Transparent)}
	mk := func() *canvas.Text {
		t := canvas.NewText("", theme.Color(theme.ColorNameForeground))
		t.TextSize = theme.TextSize()
		return t
	}
	r.left, r.act, r.right = mk(), mk(), mk()
	r.act.Alignment = fyne.TextAlignCenter
	r.act.TextStyle.Bold = true
	r.act.TextSize = theme.TextSize() * 1.4
	r.ExtendBaseWidget(r)
	return r
}

func (r *syncRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}

func (r *syncRow) CreateRenderer() fyne.WidgetRenderer {
	return &syncRowRenderer{r: r}
}

type syncRowRenderer struct{ r *syncRow }

func (s *syncRowRenderer) Destroy() {}
func (s *syncRowRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{s.r.bg, s.r.left, s.r.act, s.r.right}
}
func (s *syncRowRenderer) MinSize() fyne.Size {
	return fyne.NewSize(300, theme.TextSize()*1.4+10)
}
func (s *syncRowRenderer) Layout(sz fyne.Size) {
	r := s.r
	r.bg.Resize(sz)
	mid := float32(60)
	half := (sz.Width - mid) / 2
	h := fyne.MeasureText("Ág", r.left.TextSize, r.left.TextStyle).Height
	y := (sz.Height - h) / 2
	r.left.Move(fyne.NewPos(6, y))
	r.left.Resize(fyne.NewSize(half-12, h))
	ah := fyne.MeasureText("→", r.act.TextSize, r.act.TextStyle).Height
	r.act.Move(fyne.NewPos(half, (sz.Height-ah)/2))
	r.act.Resize(fyne.NewSize(mid, ah))
	r.right.Move(fyne.NewPos(half+mid+6, y))
	r.right.Resize(fyne.NewSize(half-12, h))
}
func (s *syncRowRenderer) Refresh() {
	s.Layout(s.r.Size())
	canvas.Refresh(s.r)
}

func sideText(rel string, s syncSide) string {
	if !s.Exists {
		return ""
	}
	if s.IsDir {
		return rel + "/  " + T("<mappa>")
	}
	return rel + "   " + fmtSize(s.Size) + "  " + s.Mod.Format("2006.01.02 15:04")
}

func (a *App) syncDirs() {
	l, r := a.left, a.right
	if l.vfs != nil || r.vfs != nil {
		a.warn(T("A szinkronizálás két helyi mappa között működik."))
		return
	}
	w := a.fa.NewWindow(T("Mappák szinkronizálása"))
	w.SetIcon(appIconRes)
	leftE, rightE := widget.NewEntry(), widget.NewEntry()
	leftE.SetText(l.path)
	rightE.SetText(r.path)
	subC := widget.NewCheck(T("Almappákkal"), nil)
	subC.SetChecked(true)
	contC := widget.NewCheck(T("Tartalom szerint is (lassabb)"), nil)
	sameC := widget.NewCheck(T("Egyezők mutatása"), nil)
	modes := []string{T("Mindkét irányba"), T("Csak balról jobbra →"), T("Csak jobbról balra ←")}
	modeS := widget.NewSelect(modes, nil)
	modeS.SetSelectedIndex(0)
	summary := widget.NewLabel(T("Nyomd meg az Összehasonlítás gombot."))
	summary.Wrapping = fyne.TextWrapWord

	var all, shown []int // all: az elemek; shown: a látható sorok indexei
	var items []syncItem
	var list *widget.List
	var goB *widget.Button
	rebuild := func() {
		shown = shown[:0]
		for i, it := range items {
			if it.Status == sSame && !sameC.Checked {
				continue
			}
			shown = append(shown, i)
		}
		_ = all
		toB, toA, conf, same, bB, bA := syncSummary(items)
		summary.SetText(fmt.Sprintf(T("→ jobbra: %s (%s)    ← balra: %s (%s)    döntésre vár: %d    egyezik: %d")+"\n"+T("Kattints egy sorra az irány váltásához (→, ←, kihagyás)."),
			Nf(toB, "%d elem"), fmtHuman(uint64(bB)), Nf(toA, "%d elem"), fmtHuman(uint64(bA)), conf, same))
		if toA+toB > 0 {
			goB.Enable()
		} else {
			goB.Disable()
		}
		list.Refresh()
	}
	list = widget.NewList(func() int { return len(shown) },
		func() fyne.CanvasObject { return newSyncRow() },
		func(id widget.ListItemID, o fyne.CanvasObject) {
			row := o.(*syncRow)
			i := shown[id]
			it := &items[i]
			fg := theme.Color(theme.ColorNameForeground)
			row.left.Text, row.right.Text = sideText(it.Rel, it.A), sideText(it.Rel, it.B)
			row.left.Color, row.right.Color = fg, fg
			switch it.Act {
			case actToB:
				row.act.Text, row.act.Color = "→", color.NRGBA{40, 160, 60, 255}
			case actToA:
				row.act.Text, row.act.Color = "←", color.NRGBA{40, 120, 220, 255}
			default:
				if it.Status == sSame {
					row.act.Text, row.act.Color = "=", fg
				} else {
					row.act.Text, row.act.Color = "≠", color.NRGBA{220, 120, 30, 255}
				}
			}
			row.onTap = func() {
				if it.Status == sSame {
					return
				}
				it.cycleAct()
				rebuild()
			}
			row.Refresh()
		})
	sameC.OnChanged = func(bool) { rebuild() }

	compare := func() {
		la, ra := strings.TrimSpace(leftE.Text), strings.TrimSpace(rightE.Text)
		for _, d := range []string{la, ra} {
			if st, err := os.Stat(d); err != nil || !st.IsDir() {
				a.warn(fmt.Sprintf(T("Nem létező mappa:\n%s"), d))
				return
			}
		}
		opt := syncOpts{Subdirs: subC.Checked, Content: contC.Checked, Mode: modeS.SelectedIndex()}
		var res []syncItem
		a.runJobIn(w, T("Összehasonlítás"), func(j *job) error {
			var err error
			res, err = syncCompare(la, ra, opt, j)
			return err
		}, func(err error) {
			if err != nil {
				return
			}
			items = res
			rebuild()
		})
	}
	goB = widget.NewButton(T("Szinkronizálás"), func() {
		la, ra := strings.TrimSpace(leftE.Text), strings.TrimSpace(rightE.Text)
		todo := append([]syncItem(nil), items...)
		a.runJobIn(w, T("Szinkronizálás"), func(j *job) error { return syncExecute(todo, la, ra, j) }, func(error) {
			a.left.refresh()
			a.right.refresh()
			compare()
		})
	})
	goB.Importance = widget.HighImportance
	modeS.OnChanged = func(string) {
		if items != nil {
			compare()
		}
	}
	goB.Disable()
	cmpB := widget.NewButton(T("Összehasonlítás"), compare)
	top := container.NewVBox(
		container.New(layout.NewGridLayout(2), leftE, rightE),
		container.NewHBox(subC, contC, sameC, sized(modeS, 230), layout.NewSpacer(), cmpB, goB),
	)
	bottom := container.NewBorder(nil, nil, nil, container.NewVBox(layout.NewSpacer(), widget.NewButton(T("Bezárás"), w.Close)), summary)
	w.SetContent(container.NewBorder(top, bottom, nil, nil, list))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyEscape {
			w.Close()
		}
	})
	a.restoreWinSize(w, "sync", 1000, 640)
	w.SetOnClosed(func() { a.saveWinSize(w, "sync") })
	w.Show()
	compare()
}
