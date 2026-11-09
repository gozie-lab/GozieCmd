//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestInotifyWatch(t *testing.T) {
	dir := t.TempDir()
	var fires atomic.Int32
	w, err := startWatch(dir, func() { fires.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	// egy fájl: gyorsan jelez
	start := time.Now()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	for fires.Load() == 0 && time.Since(start) < 2*time.Second {
		time.Sleep(10 * time.Millisecond)
	}
	if fires.Load() != 1 {
		t.Fatal("nem jelzett")
	}
	t.Logf("első jelzés: %v", time.Since(start).Round(time.Millisecond))
	// 300 fájl gyors egymásutánban: összevárva, kevés frissítés
	fires.Store(0)
	for i := 0; i < 300; i++ {
		os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), []byte("x"), 0o644)
	}
	time.Sleep(600 * time.Millisecond)
	if n := fires.Load(); n < 1 || n > 3 {
		t.Fatalf("300 változásra %d frissítés", n)
	}
	// méretváltozás (a mappa ideje ilyenkor nem változik)
	fires.Store(0)
	f, _ := os.OpenFile(filepath.Join(dir, "a.txt"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("hosszabb")
	f.Close()
	time.Sleep(400 * time.Millisecond)
	if fires.Load() == 0 {
		t.Fatal("a fájl növekedését nem jelezte")
	}
	// leállítás után nem jelez
	w.Stop()
	fires.Store(0)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x"), 0o644)
	time.Sleep(400 * time.Millisecond)
	if fires.Load() != 0 {
		t.Fatal("leállítás után is jelzett")
	}
}

func TestWatchSinkMaxWait(t *testing.T) {
	var fires atomic.Int32
	s := newWatchSink(func() { fires.Add(1) })
	// 2,5 másodpercig folyamatos változás: legalább 2 frissítés kell (max. 1 s várakozás)
	end := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(end) {
		s.poke()
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if n := fires.Load(); n < 2 || n > 4 {
		t.Fatalf("%d frissítés", n)
	}
}
