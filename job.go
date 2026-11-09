//go:build windows

package main

// Háttérben futó műveletek (ZIP, FTP) folyamatjelzővel és felülírás-kérdéssel.

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

type Job struct {
	dlg      *walk.Dialog
	cancel   atomic.Bool
	mu       sync.Mutex
	done     int64
	total    int64
	text     string
	lastUI   time.Time
	owAll    int // 0: kérdez, 1: mindet felülír, -1: mindet kihagy
	errs     []string
	skipped  int
	finished bool
	lbl      *walk.Label
	bar      *walk.ProgressBar
}

func (j *Job) Cancelled() bool { return j.cancel.Load() }

// Clean: nem volt sem hiba, sem kihagyott elem (mozgatásnál ekkor törölhető a forrás).
func (j *Job) Clean() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.errs) == 0 && j.skipped == 0 && !j.Cancelled()
}

func (j *Job) SetTotal(n int64) {
	j.mu.Lock()
	j.total = n
	j.mu.Unlock()
}

func (j *Job) Status(text string) {
	j.mu.Lock()
	j.text = text
	j.mu.Unlock()
	j.push(true)
}

func (j *Job) Add(n int64) {
	j.mu.Lock()
	j.done += n
	j.mu.Unlock()
	j.push(false)
}

func (j *Job) Err(name string, err error) {
	j.mu.Lock()
	j.errs = append(j.errs, name+": "+errText(err))
	j.mu.Unlock()
}

func (j *Job) push(force bool) {
	j.mu.Lock()
	if !force && time.Since(j.lastUI) < 100*time.Millisecond {
		j.mu.Unlock()
		return
	}
	j.lastUI = time.Now()
	j.mu.Unlock()
	j.dlg.Synchronize(func() { j.applyUI() })
}

func (j *Job) applyUI() {
	if j.finished {
		return
	}
	j.mu.Lock()
	text, done, total := j.text, j.done, j.total
	j.mu.Unlock()
	j.lbl.SetText(text)
	if total > 0 {
		j.bar.SetValue(int(done * 1000 / total))
	}
}

// AskOverwrite a háttérszálból kérdez a felhasználótól; true = felülírható.
func (j *Job) AskOverwrite(name, detail string) (bool, error) {
	if j.owAll == 1 {
		return true, nil
	}
	if j.owAll == -1 {
		j.skipped++
		return false, nil
	}
	ch := make(chan int, 1)
	j.dlg.Synchronize(func() { ch <- overwriteDialog(j.dlg, name, detail) })
	switch <-ch {
	case 1:
		return true, nil
	case 2:
		j.owAll = 1
		return true, nil
	case 3:
		j.owAll = -1
		j.skipped++
		return false, nil
	case 0:
		j.skipped++
		return false, nil
	}
	j.cancel.Store(true)
	return false, errCancel
}

// runJob modális folyamatablakot mutat, amíg a work függvény fut.
func runJob(owner walk.Form, title string, work func(j *Job) error) error {
	j := &Job{}
	var cancelBtn *walk.PushButton
	err := Dialog{
		AssignTo:     &j.dlg,
		Title:        title,
		CancelButton: &cancelBtn,
		MinSize:      Size{Width: 480, Height: 140},
		Layout:       VBox{},
		Children: []Widget{
			Label{AssignTo: &j.lbl, Text: T("Előkészítés…"), EllipsisMode: EllipsisPath},
			ProgressBar{AssignTo: &j.bar, MinValue: 0, MaxValue: 1000},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &cancelBtn, Text: T("Mégse"), OnClicked: func() {
						j.cancel.Store(true)
						j.lbl.SetText(T("Megszakítás…"))
					}},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		return err
	}
	themeDialog(j.dlg)
	j.dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !j.finished {
			*canceled = true
			j.cancel.Store(true)
		}
	})
	var result error
	j.dlg.Starting().Attach(func() {
		go func() {
			err := work(j)
			j.dlg.Synchronize(func() {
				result = err
				j.finished = true
				j.dlg.Close(0)
			})
		}()
	})
	j.dlg.Run()
	if result == nil && len(j.errs) > 0 {
		msg := fmt.Sprintf(Tn(len(j.errs), "A művelet %d hibával fejeződött be:\n\n"), len(j.errs))
		for i, e := range j.errs {
			if i == 15 {
				msg += fmt.Sprintf(T("… és még %d\n"), len(j.errs)-15)
				break
			}
			msg += e + "\n"
		}
		walk.MsgBox(owner, appName, msg, walk.MsgBoxIconWarning)
	}
	if errors.Is(result, errCancel) {
		return nil
	}
	return result
}

// overwriteDialog: 1 felülír, 2 mindet felülír, 0 kihagy, 3 mindet kihagy, -1 mégse.
func overwriteDialog(owner walk.Form, name, detail string) int {
	var dlg *walk.Dialog
	var yes, cancel *walk.PushButton
	res := -1
	btn := func(text string, v int, assign **walk.PushButton) PushButton {
		return PushButton{AssignTo: assign, Text: text, OnClicked: func() { res = v; dlg.Accept() }}
	}
	var b1, b2, b3 *walk.PushButton
	err := Dialog{
		AssignTo:      &dlg,
		Title:         T("Felülírás"),
		DefaultButton: &yes,
		CancelButton:  &cancel,
		MinSize:       Size{Width: 520, Height: 160},
		Layout:        VBox{},
		Children: []Widget{
			Label{Text: T("A cél már létezik:")},
			Label{Text: name, Font: Font{Family: "Segoe UI", PointSize: 9, Bold: true}, EllipsisMode: EllipsisPath},
			Label{Text: detail},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					btn(T("Felülírás"), 1, &yes),
					btn(T("Mindet felülír"), 2, &b1),
					btn(T("Kihagyás"), 0, &b2),
					btn(T("Mindet kihagy"), 3, &b3),
					HSpacer{},
					PushButton{AssignTo: &cancel, Text: T("Mégse"), OnClicked: func() { res = -1; dlg.Cancel() }},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		return -1
	}
	themeDialog(dlg)
	dlg.Run()
	return res
}
