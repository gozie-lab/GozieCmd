package main

// Háttérben futó műveletek sora (másolás, mozgatás): egymás után futnak, közben
// tovább lehet dolgozni. A felület (Windows / Linux) a postUI-n át kap értesítést.

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type qState int

const (
	qWaiting qState = iota
	qRunning
	qDone
	qFailed
	qCancelled
)

func (s qState) String() string {
	return [...]string{T("várakozik"), T("fut"), T("kész"), T("hibával zárult"), T("megszakítva")}[s]
}

type qItem struct {
	ID             int
	Title, Detail  string
	State          qState
	Errs           []string
	Done, Total    int64
	Speed          float64 // bájt/s
	Status         string
	Started, Ended time.Time

	run    func(Progress) error
	after  func(error)
	cancel atomic.Bool
	owAll  int
}

type transferQueue struct {
	mu     sync.Mutex
	items  []*qItem
	nextID int
	wake   chan struct{}
	once   sync.Once

	postUI     func(func())                           // a felület szálán futtat
	onChange   func()                                 // a lista megváltozott (a felület szálán hívódik)
	ask        func(name, detail string) (int, error) // felülírás: 1 igen, 2 mindet, 3 nem, 4 egyiket sem
	threadInit func() func()                          // munkaszál előkészítése (Windows: COM)
	lastNotify time.Time
}

func newTransferQueue() *transferQueue {
	return &transferQueue{wake: make(chan struct{}, 1)}
}

func (q *transferQueue) Add(title, detail string, run func(Progress) error, after func(error)) *qItem {
	q.mu.Lock()
	q.nextID++
	it := &qItem{ID: q.nextID, Title: title, Detail: detail, run: run, after: after}
	q.items = append(q.items, it)
	q.mu.Unlock()
	q.once.Do(func() { go q.loop() })
	select {
	case q.wake <- struct{}{}:
	default:
	}
	q.notify(true)
	return it
}

// Adopt: egy már futó (előtérben indított, majd háttérbe küldött) művelet felvétele
// a sorba. A sor nem futtatja: az állapotát a SetProgress és a Finish frissíti.
func (q *transferQueue) Adopt(title, detail string) *qItem {
	q.mu.Lock()
	q.nextID++
	it := &qItem{ID: q.nextID, Title: title, Detail: detail, State: qRunning, Started: time.Now()}
	q.items = append(q.items, it)
	q.mu.Unlock()
	q.notify(true)
	return it
}

// SetProgress: egy átvett művelet állását frissíti.
func (q *transferQueue) SetProgress(it *qItem, done, total int64, speed float64, status string) {
	q.mu.Lock()
	it.Done, it.Total, it.Speed, it.Status = done, total, speed, status
	q.mu.Unlock()
	q.notify(false)
}

// Finish: egy átvett művelet lezárása.
func (q *transferQueue) Finish(it *qItem, err error, errs []string) {
	q.mu.Lock()
	it.Ended = time.Now()
	it.Errs = append(it.Errs, errs...)
	switch {
	case it.cancel.Load() || err == errCancel:
		it.State = qCancelled
	case err != nil:
		it.State = qFailed
		it.Errs = append([]string{errText(err)}, it.Errs...)
	case len(it.Errs) > 0:
		it.State = qFailed
	default:
		it.State = qDone
	}
	q.mu.Unlock()
	q.notify(true)
}

func (q *transferQueue) Cancel(id int) {
	q.mu.Lock()
	for _, it := range q.items {
		if it.ID == id {
			it.cancel.Store(true)
			if it.State == qWaiting {
				it.State = qCancelled
				it.Ended = time.Now()
			}
		}
	}
	q.mu.Unlock()
	q.notify(true)
}

func (q *transferQueue) ClearFinished() {
	q.mu.Lock()
	var keep []*qItem
	for _, it := range q.items {
		if it.State == qWaiting || it.State == qRunning {
			keep = append(keep, it)
		}
	}
	q.items = keep
	q.mu.Unlock()
	q.notify(true)
}

// qView: egy sorelem állapota megjelenítéshez (másolható pillanatkép).
type qView struct {
	ID             int
	Title, Detail  string
	State          qState
	Errs           []string
	Done, Total    int64
	Speed          float64
	Status         string
	Started, Ended time.Time
}

// Snapshot: a sor másolata megjelenítéshez.
func (q *transferQueue) Snapshot() []qView {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]qView, len(q.items))
	for i, it := range q.items {
		out[i] = qView{ID: it.ID, Title: it.Title, Detail: it.Detail, State: it.State, Errs: append([]string(nil), it.Errs...),
			Done: it.Done, Total: it.Total, Speed: it.Speed, Status: it.Status, Started: it.Started, Ended: it.Ended}
	}
	return out
}

// Active: hány művelet vár vagy fut.
func (q *transferQueue) Active() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, it := range q.items {
		if it.State == qWaiting || it.State == qRunning {
			n++
		}
	}
	return n
}

func (q *transferQueue) notify(force bool) {
	q.mu.Lock()
	if !force && time.Since(q.lastNotify) < 250*time.Millisecond {
		q.mu.Unlock()
		return
	}
	q.lastNotify = time.Now()
	q.mu.Unlock()
	if q.postUI != nil && q.onChange != nil {
		q.postUI(q.onChange)
	}
}

func (q *transferQueue) next() *qItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, it := range q.items {
		if it.State == qWaiting {
			it.State = qRunning
			it.Started = time.Now()
			return it
		}
	}
	return nil
}

func (q *transferQueue) loop() {
	runtime.LockOSThread()
	if q.threadInit != nil {
		if done := q.threadInit(); done != nil {
			defer done()
		}
	}
	for {
		it := q.next()
		if it == nil {
			<-q.wake
			continue
		}
		q.notify(true)
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf(T("programhiba: %v"), r)
				}
			}()
			err = it.run(&qProgress{q: q, it: it, last: time.Now()})
		}()
		q.mu.Lock()
		it.Ended = time.Now()
		switch {
		case it.cancel.Load() || err == errCancel:
			it.State = qCancelled
		case err != nil:
			it.State = qFailed
			it.Errs = append([]string{errText(err)}, it.Errs...)
		case len(it.Errs) > 0:
			it.State = qFailed
		default:
			it.State = qDone
		}
		after := it.after
		q.mu.Unlock()
		q.notify(true)
		if after != nil && q.postUI != nil {
			q.postUI(func() { after(err) })
		}
	}
}

// qProgress a sor egy elemének Progress-megvalósítása (sebességméréssel).
type qProgress struct {
	q        *transferQueue
	it       *qItem
	last     time.Time
	lastDone int64
}

func (p *qProgress) Cancelled() bool { return p.it.cancel.Load() }

func (p *qProgress) SetTotal(n int64) {
	p.q.mu.Lock()
	p.it.Total, p.it.Done = n, 0
	p.q.mu.Unlock()
	p.lastDone = 0
}

func (p *qProgress) Status(t string) {
	p.q.mu.Lock()
	p.it.Status = t
	p.q.mu.Unlock()
	p.q.notify(false)
}

func (p *qProgress) Add(n int64) {
	p.q.mu.Lock()
	p.it.Done += n
	if dt := time.Since(p.last).Seconds(); dt >= 0.5 {
		inst := float64(p.it.Done-p.lastDone) / dt
		if p.it.Speed == 0 {
			p.it.Speed = inst
		} else {
			p.it.Speed = 0.7*p.it.Speed + 0.3*inst
		}
		p.last, p.lastDone = time.Now(), p.it.Done
	}
	p.q.mu.Unlock()
	p.q.notify(false)
}

func (p *qProgress) Err(name string, err error) {
	if err == nil || err == errCancel {
		return
	}
	p.q.mu.Lock()
	p.it.Errs = append(p.it.Errs, name+": "+errText(err))
	p.q.mu.Unlock()
}

func (p *qProgress) AskOverwrite(name, detail string) (bool, error) {
	switch p.it.owAll {
	case 1:
		return true, nil
	case -1:
		return false, nil
	}
	if p.q.ask == nil {
		return false, nil
	}
	r, err := p.q.ask(name, detail)
	if err != nil {
		p.it.cancel.Store(true)
		return false, errCancel
	}
	switch r {
	case 2:
		p.it.owAll = 1
		return true, nil
	case 4:
		p.it.owAll = -1
		return false, nil
	}
	return r == 1, nil
}

// ── kijelzés ──

func fmtSpeed(bps float64) string {
	if bps <= 0 {
		return ""
	}
	return fmtHuman(uint64(bps)) + "/s"
}

func fmtETA(done, total int64, bps float64) string {
	if bps <= 0 || total <= done {
		return ""
	}
	s := int(float64(total-done) / bps)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// qLine: egy sorelem egysoros leírása.
func qLine(it qView) string {
	var b strings.Builder
	b.WriteString(it.Title)
	switch it.State {
	case qRunning:
		if it.Total > 0 {
			fmt.Fprintf(&b, T(" – %d%%"), int(100*it.Done/it.Total))
		}
		if s := fmtSpeed(it.Speed); s != "" {
			b.WriteString(", " + s)
		}
		if e := fmtETA(it.Done, it.Total, it.Speed); e != "" {
			b.WriteString(fmt.Sprintf(T(", hátra %s"), e))
		}
	default:
		b.WriteString(fmt.Sprintf(T(" – %s"), it.State.String()))
		if it.State == qFailed && len(it.Errs) > 0 {
			fmt.Fprintf(&b, Tn(len(it.Errs), " (%d hiba)"), len(it.Errs))
		}
	}
	return b.String()
}
