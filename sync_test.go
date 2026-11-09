package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type nopProgress struct{}

func (nopProgress) Cancelled() bool                           { return false }
func (nopProgress) SetTotal(int64)                            {}
func (nopProgress) Status(string)                             {}
func (nopProgress) Add(int64)                                 {}
func (nopProgress) Err(n string, e error)                     { panic(n + ": " + e.Error()) }
func (nopProgress) AskOverwrite(string, string) (bool, error) { return true, nil }

func TestSync(t *testing.T) {
	d := t.TempDir()
	a, b := filepath.Join(d, "a"), filepath.Join(d, "b")
	w := func(p, s string, age time.Duration) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
		tm := time.Now().Add(-age)
		os.Chtimes(p, tm, tm)
	}
	w(filepath.Join(a, "same.txt"), "x", time.Hour)
	w(filepath.Join(b, "same.txt"), "x", time.Hour)
	w(filepath.Join(a, "onlya", "f.txt"), "aa", time.Hour)
	w(filepath.Join(b, "onlyb.txt"), "bbb", time.Hour)
	w(filepath.Join(a, "newer.txt"), "new", time.Minute)
	w(filepath.Join(b, "newer.txt"), "old", time.Hour)
	os.MkdirAll(filepath.Join(b, "emptydir"), 0o755)
	items, err := syncCompare(a, b, syncOpts{Subdirs: true}, nopProgress{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]syncAct{}
	for _, it := range items {
		got[it.Rel] = it.Act
	}
	want := map[string]syncAct{"same.txt": actNone, "onlya/f.txt": actToB, "onlyb.txt": actToA, "newer.txt": actToB, "emptydir": actToA}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s: %v (van: %v), várt %v; %v", k, g, ok, v, got)
		}
	}
	if _, ok := got["onlya"]; ok {
		t.Error("a nem üres mappa ne legyen külön sor")
	}
	if err := syncExecute(items, a, b, nopProgress{}); err != nil {
		t.Fatal(err)
	}
	again, _ := syncCompare(a, b, syncOpts{Subdirs: true}, nopProgress{})
	for _, it := range again {
		if it.Status != sSame && !(it.A.IsDir || it.B.IsDir) {
			t.Errorf("szinkron után eltérés: %s %v", it.Rel, it.Status)
		}
	}
	if s, _ := os.ReadFile(filepath.Join(b, "newer.txt")); string(s) != "new" {
		t.Error("newer.txt nem frissült")
	}
	// csak bal → jobb mód
	items, _ = syncCompare(a, b, syncOpts{Subdirs: true, Mode: 1}, nopProgress{})
	for _, it := range items {
		if it.Act == actToA {
			t.Error("bal → jobb módban nem mehet balra:", it.Rel)
		}
	}
}

func TestQueue(t *testing.T) {
	q := newTransferQueue()
	done := make(chan string, 3)
	q.postUI = func(f func()) { f() }
	q.Add("1", "", func(p Progress) error { p.SetTotal(10); p.Add(10); return nil }, func(error) { done <- "1" })
	it := q.Add("2", "", func(p Progress) error { time.Sleep(50 * time.Millisecond); return nil }, func(error) { done <- "2" })
	q.Add("3", "", func(p Progress) error { return nil }, func(error) { done <- "3" })
	_ = it
	for i := 0; i < 3; i++ {
		select {
		case s := <-done:
			if s != string(rune('1'+i)) {
				t.Fatal("sorrend:", s)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("időtúllépés")
		}
	}
	for _, s := range q.Snapshot() {
		if s.State != qDone {
			t.Fatal(s.Title, s.State)
		}
	}
}
