package main

// Változásfigyelés – közös rész. A rendszer (Windows: ReadDirectoryChangesW,
// Linux: inotify) jelzi a mappa változását; a jelzéseket összevárjuk, hogy egy
// sok fájlt érintő művelet ne okozzon százszoros frissítést.

import (
	"sync"
	"time"
)

const (
	watchQuiet   = 150 * time.Millisecond // ennyi csend után frissít
	watchMaxWait = time.Second            // folyamatos változásnál legalább ilyen gyakran
)

// watchSink összegyűjti a jelzéseket, és egyszer hívja a fire-t (háttérszálon:
// a hívó gondoskodik róla, hogy a frissítés a felület szálán fusson).
type watchSink struct {
	mu      sync.Mutex
	pending bool
	first   time.Time
	timer   *time.Timer
	fire    func()
	stopped bool
}

func newWatchSink(fire func()) *watchSink { return &watchSink{fire: fire} }

// poke: változás történt (bármely szálról hívható).
func (s *watchSink) poke() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	now := time.Now()
	if !s.pending {
		s.pending, s.first = true, now
		s.timer = time.AfterFunc(watchQuiet, s.flush)
		return
	}
	if now.Sub(s.first) < watchMaxWait {
		s.timer.Reset(watchQuiet)
	}
}

func (s *watchSink) flush() {
	s.mu.Lock()
	s.pending = false
	stopped := s.stopped
	s.mu.Unlock()
	if !stopped {
		s.fire()
	}
}

func (s *watchSink) stop() {
	s.mu.Lock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
}

// Ha a figyelés működik, a régi lekérdezés csak ritkán fut (biztonsági háló);
// hálózati helyen marad a gyakori lekérdezés is.
const watchPollEvery = 7 // × 1,5 s ≈ 10 s
