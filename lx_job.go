//go:build linux

package main

// Hosszú műveletek (másolás, törlés, tömörítés, FTP) a háttérben, folyamatjelzővel,
// megszakítással és felülírás-kérdéssel. A Progress felületet valósítja meg, így az
// archívum- és FTP-kód változatlanul használható.

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type job struct {
	a         *App
	title     string
	cancelled atomic.Bool
	total     atomic.Int64
	done      atomic.Int64
	mu        sync.Mutex
	status    string
	errs      []string
	owAll     int // 0: kérdez, 1: mindet felülír, -1: egyiket sem
	skipped   atomic.Int64
	askCh     chan int    // a nyitott felülírás-kérdés válaszcsatornája (felület szála)
	detached  atomic.Bool // háttérbe küldve: a sor ablakában látszik
	qit       atomic.Pointer[qItem]
	askD      dialog.Dialog
	lbl       *widget.Label
	bar       *widget.ProgressBar
	d         dialog.Dialog
	shown     bool
	parent    fyne.Window
	speed     float64
	lastDone  int64
	lastT     time.Time
}

func (j *job) Cancelled() bool {
	return j.cancelled.Load() || (j.qit.Load() != nil && j.qit.Load().cancel.Load()) // a sor ablakából is megszakítható
}

// Clean: nem volt sem hiba, sem kihagyott elem – mozgatásnál csak ekkor törölhető a forrás.
func (j *job) Clean() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.errs) == 0 && j.skipped.Load() == 0 && !j.Cancelled()
}
func (j *job) SetTotal(n int64) { j.total.Store(n); j.done.Store(0) }
func (j *job) Add(n int64)      { j.done.Add(n) }
func (j *job) Status(t string) {
	j.mu.Lock()
	j.status = t
	j.mu.Unlock()
}

func (j *job) Err(name string, err error) {
	if err == nil || err == errCancel {
		return
	}
	j.mu.Lock()
	j.errs = append(j.errs, name+": "+errText(err))
	j.mu.Unlock()
}

// AskOverwrite: felülírja-e a létező célt (a felület szálán kérdez, a háttérszál vár).
func (j *job) AskOverwrite(name, detail string) (bool, error) {
	switch j.owAll {
	case 1:
		return true, nil
	case -1:
		j.skipped.Add(1)
		return false, nil
	}
	ch := make(chan int, 1)
	fyne.Do(func() {
		if j.Cancelled() {
			ch <- 5
			return
		}
		// előbb a folyamatablak, hogy a kérdés biztosan fölötte legyen (különben a
		// késleltetve megjelenő folyamatablak eltakarná)
		if !j.shown && j.d != nil && !j.detached.Load() {
			j.shown = true
			j.d.Show()
		}
		msg := widget.NewLabel(fmt.Sprintf(T("Már létezik:\n%s\n\n%s"), name, detail))
		msg.Wrapping = fyne.TextWrapWord
		var d dialog.Dialog
		btn := func(t string, v int) *widget.Button {
			return widget.NewButton(t, func() {
				d.Hide()
				j.askCh, j.askD = nil, nil
				ch <- v
			})
		}
		yes := btn(T("Felülírás"), 1)
		yes.Importance = widget.HighImportance
		buttons := container.NewGridWithColumns(3, yes, btn(T("Mindet felülír"), 2), btn(T("Kihagyás"), 3),
			btn(T("Egyiket sem"), 4), btn(T("Mégse"), 5))
		d = dialog.NewCustomWithoutButtons(T("Felülírás?"), container.NewVBox(msg, buttons), j.parent)
		d.Resize(fyne.NewSize(520, 0))
		j.askCh, j.askD = ch, d
		d.Show()
	})
	switch <-ch {
	case 1:
		return true, nil
	case 2:
		j.owAll = 1
		return true, nil
	case 3:
		j.skipped.Add(1)
		return false, nil
	case 4:
		j.owAll = -1
		j.skipped.Add(1)
		return false, nil
	}
	j.cancelled.Store(true)
	return false, errCancel
}

// promptsOpen: munkaszálról nyitott kérdések (pl. jelszó) száma; amíg van ilyen, a
// késleltetve megjelenő folyamatablak nem takarhatja el.
var promptsOpen atomic.Int32

// runJob a háttérben futtatja fn-t; a végén (a felület szálán) meghívja a done-t.
func (a *App) runJob(title string, fn func(j *job) error, done func(err error)) {
	a.runJobIn(a.win, title, fn, done)
}

// runJobIn: mint a runJob, de a folyamatjelző a megadott ablakban jelenik meg.
func (a *App) runJobIn(parent fyne.Window, title string, fn func(j *job) error, done func(err error)) {
	a.runJobOpt(parent, title, fn, done, "")
}

// runJobBg: mint a runJob, de a folyamatablak „Háttérbe” gombjával a művelet a
// háttérműveletek közé tehető (what: a sor ablakában megjelenő leírás).
func (a *App) runJobBg(title, what string, fn func(j *job) error, done func(err error)) {
	a.runJobOpt(a.win, title, fn, done, what)
}

func (a *App) runJobOpt(parent fyne.Window, title string, fn func(j *job) error, done func(err error), bgWhat string) {
	j := &job{a: a, title: title, parent: parent, lastT: time.Now()}
	j.lbl = widget.NewLabel(T("…"))
	j.lbl.Truncation = fyne.TextTruncateEllipsis
	j.bar = widget.NewProgressBar()
	cancel := widget.NewButton(T("Mégse"), func() {
		j.cancelled.Store(true)
		if j.askCh != nil { // éppen a felülírásra vár: a kérdést is lezárjuk
			j.askD.Hide()
			j.askCh <- 5
			j.askCh, j.askD = nil, nil
		}
	})
	btns := container.NewHBox(cancel)
	if bgWhat != "" {
		bg := widget.NewButton(T("Háttérbe"), func() {
			if j.detached.Swap(true) {
				return
			}
			j.qit.Store(a.queue.Adopt(title+": "+bgWhat, ""))
			a.fgJobs.Add(-1) // innentől a sor számolja
			j.d.Hide()
			a.showQueueHint()
		})
		btns = container.NewHBox(bg, cancel)
	}
	box := container.NewVBox(j.lbl, j.bar, container.NewCenter(btns))
	j.d = dialog.NewCustomWithoutButtons(title, box, parent)
	j.d.Resize(fyne.NewSize(560, 0))

	finished := make(chan error, 1)
	a.fgJobs.Add(1)
	go func() {
		defer func() {
			if !j.detached.Load() {
				a.fgJobs.Add(-1)
			}
		}()
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf(T("programhiba: %v"), r)
					dlog("PROGRAMHIBA (%s): %v", title, r)
				}
			}()
			err = fn(j)
		}()
		finished <- err
	}()

	// rövid műveletnél nincs villanás: az ablak csak 250 ms után jelenik meg
	tick := time.NewTicker(120 * time.Millisecond)
	start := time.Now()
	go func() {
		defer tick.Stop()
		for {
			select {
			case err := <-finished:
				fyne.Do(func() {
					if j.detached.Load() { // háttérben ért véget: az eredmény a sorban látszik
						j.mu.Lock()
						errs := append([]string(nil), j.errs...)
						j.mu.Unlock()
						a.queue.Finish(j.qit.Load(), err, errs)
						if done != nil {
							done(err)
						}
						return
					}
					if j.shown {
						j.d.Hide()
					}
					j.finish(err, done)
				})
				return
			case <-tick.C:
				fyne.Do(func() {
					if !j.shown && !j.detached.Load() && promptsOpen.Load() == 0 && time.Since(start) > 250*time.Millisecond {
						j.shown = true
						j.d.Show()
					}
					j.mu.Lock()
					st := j.status
					j.mu.Unlock()
					t, dn := j.total.Load(), j.done.Load()
					if dt := time.Since(j.lastT).Seconds(); dt >= 0.5 {
						inst := float64(dn-j.lastDone) / dt
						if j.speed == 0 {
							j.speed = inst
						} else {
							j.speed = 0.7*j.speed + 0.3*inst
						}
						j.lastDone, j.lastT = dn, time.Now()
					}
					line := st
					if sp := fmtSpeed(j.speed); sp != "" && t > 0 {
						line += "\n" + sp
						if eta := fmtETA(dn, t, j.speed); eta != "" {
							line += fmt.Sprintf(T(", hátra %s"), eta)
						}
					}
					j.lbl.SetText(line)
					if t > 0 {
						j.bar.SetValue(float64(dn) / float64(t))
					}
					if j.detached.Load() {
						a.queue.SetProgress(j.qit.Load(), dn, t, j.speed, st)
					}
				})
			}
		}
	}()
}

func (j *job) finish(err error, done func(error)) {
	a := j.a
	j.mu.Lock()
	errs := j.errs
	j.mu.Unlock()
	if err != nil && err != errCancel {
		errs = append([]string{errText(err)}, errs...)
	}
	if len(errs) > 0 {
		shown := errs
		if len(shown) > 15 {
			shown = append(shown[:15:15], fmt.Sprintf(Tn(len(errs)-15, "… és még %d hiba"), len(errs)-15))
		}
		a.warn(fmt.Sprintf(T("%s – hiba történt:\n\n%s"), j.title, strings.Join(shown, "\n")))
	}
	if done != nil {
		done(err)
	}
}
