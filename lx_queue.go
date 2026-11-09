//go:build linux

package main

// Háttérműveletek sora (Linux-felület): jelző a főablakban és a sor ablaka.

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func (a *App) initQueue() {
	q := newTransferQueue()
	q.postUI = fyne.Do
	q.onChange = a.updateQueueUI
	q.ask = func(name, detail string) (int, error) {
		ch := make(chan int, 1)
		fyne.Do(func() {
			msg := widget.NewLabel(fmt.Sprintf(T("Háttérművelet – már létezik:\n%s\n\n%s"), name, detail))
			msg.Wrapping = fyne.TextWrapWord
			var d dialog.Dialog
			btn := func(t string, v int) *widget.Button {
				return widget.NewButton(t, func() { d.Hide(); ch <- v })
			}
			yes := btn(T("Felülírás"), 1)
			yes.Importance = widget.HighImportance
			d = dialog.NewCustomWithoutButtons(T("Felülírás?"), container.NewVBox(msg,
				container.NewGridWithColumns(3, yes, btn(T("Mindet felülír"), 2), btn(T("Kihagyás"), 3),
					btn(T("Egyiket sem"), 4), btn(T("Mégse"), 5))), a.win)
			d.Resize(fyne.NewSize(520, 0))
			d.Show()
		})
		r := <-ch
		if r == 5 {
			return 0, errCancel
		}
		return r, nil
	}
	a.queue = q
	a.queueBtn = widget.NewButton("", a.showQueue)
	a.queueBtn.Importance = widget.LowImportance
	a.queueBtn.Hide()
}

// updateQueueUI: a főablak jelzője és (ha nyitva van) a sor ablaka.
func (a *App) updateQueueUI() {
	items := a.queue.Snapshot()
	var run *qView
	waiting := 0
	for i := range items {
		switch items[i].State {
		case qRunning:
			run = &items[i]
		case qWaiting:
			waiting++
		}
	}
	switch {
	case run != nil:
		t := "⏵ " + qLine(*run)
		if waiting > 0 {
			t += fmt.Sprintf(Tn(waiting, "  (+%d vár)"), waiting)
		}
		a.queueBtn.SetText(elideMiddle(t, 70))
		a.queueBtn.Show()
	case waiting > 0:
		a.queueBtn.SetText(fmt.Sprintf(Tn(waiting, "⏵ %d háttérművelet vár"), waiting))
		a.queueBtn.Show()
	default:
		failed := 0
		for _, it := range items {
			if it.State == qFailed {
				failed++
			}
		}
		if failed > 0 {
			a.queueBtn.SetText(fmt.Sprintf(Tn(failed, "⚠ %d háttérművelet hibával zárult"), failed))
			a.queueBtn.Show()
		} else {
			a.queueBtn.Hide()
		}
	}
	if a.queueRefresh != nil {
		a.queueRefresh()
	}
}

func (a *App) showQueueHint() { a.updateQueueUI() }

func (a *App) showQueue() {
	if a.queueWin != nil {
		a.queueWin.RequestFocus()
		return
	}
	w := a.fa.NewWindow(T("Háttérműveletek"))
	w.SetIcon(appIconRes)
	a.queueWin = w
	var items []qView
	sel := -1
	list := widget.NewList(func() int { return len(items) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(l, widget.NewProgressBar())
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			c := o.(*fyne.Container).Objects
			it := items[i]
			c[0].(*widget.Label).SetText(qLine(it))
			bar := c[1].(*widget.ProgressBar)
			switch {
			case it.State == qRunning && it.Total > 0:
				bar.SetValue(float64(it.Done) / float64(it.Total))
				bar.Show()
			case it.State == qDone:
				bar.SetValue(1)
				bar.Show()
			default:
				bar.Hide()
			}
		})
	list.OnSelected = func(i widget.ListItemID) { sel = i }
	detail := widget.NewLabel("")
	detail.Wrapping = fyne.TextWrapWord
	refresh := func() {
		items = a.queue.Snapshot()
		list.Refresh()
		if sel >= 0 && sel < len(items) {
			it := items[sel]
			txt := it.Status
			if len(it.Errs) > 0 {
				shown := it.Errs
				if len(shown) > 8 {
					shown = append(shown[:8:8], fmt.Sprintf(T("… és még %d"), len(it.Errs)-8))
				}
				txt = fmt.Sprintf(T("Hibák:\n%s"), strings.Join(shown, "\n"))
			}
			detail.SetText(txt)
		} else {
			detail.SetText("")
		}
	}
	a.queueRefresh = refresh
	refresh()
	buttons := container.NewHBox(
		widget.NewButton(T("Megszakítás"), func() {
			if sel >= 0 && sel < len(items) {
				a.queue.Cancel(items[sel].ID)
			}
		}),
		widget.NewButton(T("Befejezettek törlése a listából"), func() { a.queue.ClearFinished(); sel = -1; list.UnselectAll() }),
		widget.NewButton(T("Bezárás"), w.Close),
	)
	w.SetContent(container.NewBorder(nil, container.NewVBox(detail, buttons), nil, nil, list))
	w.Resize(fyne.NewSize(720, 420))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyEscape {
			w.Close()
		}
	})
	w.SetOnClosed(func() { a.queueWin = nil; a.queueRefresh = nil })
	w.Show()
}
