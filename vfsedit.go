package main

// Archívumból vagy távoli helyről (FTP, SFTP…) szerkesztésre megnyitott fájl
// visszaírása: a fájl egy ideiglenes mappába kerül, ott nyílik meg a szerkesztőben,
// és amikor a szerkesztő elmenti, a program visszaírja az eredeti helyére.

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// editSession egy szerkesztésre megnyitott virtuális fájl.
type editSession struct {
	v     VFS
	dir   string // a fájl mappája a virtuális helyen (ide íródik vissza)
	where string // a hely neve az üzenetekhez (pl. „FTP: szerver”)
	local string // az ideiglenes példány

	mu      sync.Mutex
	mod     time.Time // az utoljára visszaírt (vagy kibontott) állapot
	size    int64
	pending time.Time // az észlelt, még nem visszaírt állapot
	psize   int64
}

func newEditSession(v VFS, dir, where, local string) *editSession {
	s := &editSession{v: v, dir: dir, where: where, local: local}
	if st, err := os.Stat(local); err == nil {
		s.mod, s.size = st.ModTime(), st.Size()
	}
	return s
}

func (s *editSession) name() string { return filepath.Base(s.local) }

// poll: történt-e azóta mentés, ami már be is fejeződött. Egy változást csak akkor
// jelez, ha két egymást követő ellenőrzéskor ugyanaz az állapot (a szerkesztő már
// nem ír a fájlba). Ha a fájl eltűnt (a program kilépéskor törli), gone igaz.
func (s *editSession) poll() (changed, gone bool) {
	st, err := os.Stat(s.local)
	if err != nil {
		return false, os.IsNotExist(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, n := st.ModTime(), st.Size()
	if m.Equal(s.mod) && n == s.size {
		s.pending = time.Time{}
		return false, false
	}
	if m.Equal(s.pending) && n == s.psize {
		return true, false
	}
	s.pending, s.psize = m, n
	return false, false
}

// writeBack visszaírja a fájlt; meglévő fájlt kérdés nélkül felülír (a szerkesztés
// éppen ennek a fájlnak a módosítása volt).
func (s *editSession) writeBack(pr Progress) error {
	st, err := os.Stat(s.local)
	if err != nil {
		return err
	}
	if err := importPaths(s.v, []string{s.local}, s.dir, overwriteAll{pr}); err != nil {
		return err
	}
	s.mu.Lock()
	s.mod, s.size, s.pending = st.ModTime(), st.Size(), time.Time{}
	s.mu.Unlock()
	return nil
}

// skip: ezt a változatot nem kell visszaírni (a felhasználó elutasította).
func (s *editSession) skip() {
	if st, err := os.Stat(s.local); err == nil {
		s.mu.Lock()
		s.mod, s.size, s.pending = st.ModTime(), st.Size(), time.Time{}
		s.mu.Unlock()
	}
}

// watch figyeli a fájlt, amíg az létezik (legfeljebb egy napig), és minden kész
// mentés után meghívja az onChange-et. Az onChange a felület szálán kezelje a
// visszaírást; a figyelés közben folytatódik.
func (s *editSession) watch(onChange func(*editSession)) {
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		end := time.Now().Add(24 * time.Hour)
		busy := false
		var bmu sync.Mutex
		for range t.C {
			if time.Now().After(end) {
				return
			}
			bmu.Lock()
			b := busy
			bmu.Unlock()
			if b {
				continue
			}
			changed, gone := s.poll()
			if gone {
				return
			}
			if changed {
				bmu.Lock()
				busy = true
				bmu.Unlock()
				onChange(s)
				bmu.Lock()
				busy = false
				bmu.Unlock()
			}
		}
	}()
}

// overwriteAll minden felülírási kérdésre igennel felel.
type overwriteAll struct{ Progress }

func (overwriteAll) AskOverwrite(string, string) (bool, error) { return true, nil }

// quietProgress: háttérbeli visszaíráshoz; az első hibát megőrzi.
type quietProgress struct {
	mu  sync.Mutex
	err error
}

func (*quietProgress) Cancelled() bool                           { return false }
func (*quietProgress) SetTotal(int64)                            {}
func (*quietProgress) Status(string)                             {}
func (*quietProgress) Add(int64)                                 {}
func (*quietProgress) AskOverwrite(string, string) (bool, error) { return true, nil }
func (q *quietProgress) Err(name string, err error) {
	q.mu.Lock()
	if q.err == nil {
		q.err = err
	}
	q.mu.Unlock()
}

// writeBackQuiet: visszaírás a háttérben, a hibát (a fájlonkéntit is) visszaadja.
func (s *editSession) writeBackQuiet() error {
	q := &quietProgress{}
	if err := s.writeBack(q); err != nil {
		return err
	}
	return q.err
}
