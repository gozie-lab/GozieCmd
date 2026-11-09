package main

import (
	"compress/gzip"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchCore(t *testing.T) {
	if !matchName("Számla.PDF", []string{"*.pdf"}, false) || matchName("Számla.PDF", []string{"*.pdf"}, true) {
		t.Error("matchName kis/nagybetű")
	}
	if !matchName("a.txt", []string{"*.doc", "a.*"}, false) {
		t.Error("több minta")
	}
	dir := t.TempDir()
	// a keresett szó két 1 MiB-os olvasási darab határára esik
	big := strings.Repeat("x", 1<<20-3) + "HATÁR" + strings.Repeat("y", 100)
	p := filepath.Join(dir, "nagy.txt")
	must(t, os.WriteFile(p, []byte(big), 0o644))
	var stop atomic.Bool
	if !fileContains(p, [][]byte{[]byte("HATÁR")}, true, &stop) {
		t.Error("darabhatáron átnyúló találat elveszett")
	}
	if !fileContains(p, [][]byte{[]byte("határ")}, false, &stop) {
		t.Error("kis/nagybetű-független keresés (ékezetes)")
	}
	if fileContains(p, [][]byte{[]byte("nincs benne")}, false, &stop) {
		t.Error("téves találat")
	}
	u := filepath.Join(dir, "utf16.txt")
	must(t, os.WriteFile(u, append([]byte{0xff, 0xfe}, utf16le("Helló világ")...), 0o644))
	if !fileContains(u, [][]byte{[]byte("világ"), utf16le("világ")}, true, &stop) {
		t.Error("UTF-16 találat")
	}
}

func TestQuickSearchAndTabs(t *testing.T) {
	names := []string{"..", "Alma.txt", "számla 2024.pdf", "Szamarak", "zene"}
	if i := quickFind(names, "szamla", 0, 1); i != 2 {
		t.Errorf("ékezet nélküli keresés: %d", i)
	}
	if i := quickFind(names, "*2024", 0, 1); i != 2 {
		t.Errorf("csillagos keresés: %d", i)
	}
	if i := quickFind(names, "sza", 3, 1); i != 3 {
		t.Errorf("továbblépés: %d", i)
	}
	if i := quickFind(names, "sza", 3, -1); i != 3 || quickFind(names, "sza", 2, -1) != 2 {
		t.Errorf("visszafelé: %d", i)
	}
	if quickFind(names, "qqq", 0, 1) != -1 || quickFind(nil, "a", 0, 1) != -1 {
		t.Error("nincs találat")
	}
	tabs := []tabState{{Path: "/home", Sel: "x"}, {Path: ""}, {Path: "/tmp"}}
	if d := decodeTabs(encodeTabs(tabs)); len(d) != 2 || d[0].Sel != "x" {
		t.Errorf("fülek: %+v", d)
	}
	if decodeTabs("rossz json") != nil || decodeHotlist("") != nil {
		t.Error("hibás bemenet")
	}
	if l := tabLabel("/nagyon/hosszú/mappanév/ami/tényleg/hosszabb/mint/huszonkét/karakter_ez_bizony"); len([]rune(l)) > 22 {
		t.Errorf("fülcím: %q", l)
	}
	if tabLabel("/") != "/" {
		t.Error("gyökér fülcíme")
	}
	h := historyMenu([]string{"/a", "/b", "/a", "/c", "/cur"}, "/cur", 10)
	if strings.Join(h, ",") != "/c,/a,/b" {
		t.Errorf("előzmények: %v", h)
	}
}

func TestTransferQueue(t *testing.T) {
	q := newTransferQueue()
	var mu sync.Mutex
	q.postUI = func(f func()) { mu.Lock(); defer mu.Unlock(); f() }
	q.onChange = func() {}
	var order []int
	var wg sync.WaitGroup
	release := make(chan struct{})
	wg.Add(4)
	q.Add("1", "", func(p Progress) error { <-release; order = append(order, 1); return nil }, func(error) { wg.Done() })
	q.Add("2", "", func(p Progress) error { order = append(order, 2); return errors.New("hiba") }, func(error) { wg.Done() })
	it3 := q.Add("3", "", func(p Progress) error { order = append(order, 3); return nil }, func(error) { wg.Done() })
	q.Add("4", "", func(p Progress) error {
		p.SetTotal(10)
		p.Add(10)
		p.Err("x", errors.New("rossz"))
		order = append(order, 4)
		return nil
	}, func(error) { wg.Done() })
	if q.Active() != 4 {
		t.Errorf("aktív: %d", q.Active())
	}
	q.Cancel(it3.ID) // még vár: el sem indul
	wg.Add(-1)       // a megszakított várakozó után nincs „after”
	close(release)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a sor elakadt")
	}
	if fmtInts(order) != "1,2,4" {
		t.Errorf("sorrend: %v", order)
	}
	s := q.Snapshot()
	want := []qState{qDone, qFailed, qCancelled, qFailed}
	for i, v := range s {
		if v.State != want[i] {
			t.Errorf("%d. állapot: %v, várt: %v", i+1, v.State, want[i])
		}
	}
	q.ClearFinished()
	if len(q.Snapshot()) != 0 || q.Active() != 0 {
		t.Error("ürítés")
	}
}

func fmtInts(a []int) string {
	var s []string
	for _, x := range a {
		s = append(s, string(rune('0'+x)))
	}
	return strings.Join(s, ",")
}

func TestSingleFileCompressed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "napló.txt.gz")
	f, _ := os.Create(p)
	zw := gzip.NewWriter(f)
	zw.Write([]byte("tömörített tartalom"))
	zw.Close()
	f.Close()
	a, err := openArchive(p)
	must(t, err)
	es := listOf(t, a, a.Root())
	if len(es) != 1 || es[0].Name != "napló.txt" {
		t.Fatalf("lista: %v", names(es))
	}
	out := t.TempDir()
	must(t, a.Extract(es, out, &testProg{}))
	if b, _ := os.ReadFile(filepath.Join(out, "napló.txt")); string(b) != "tömörített tartalom" {
		t.Errorf("tartalom: %q", b)
	}
}

// 7Z olvasás a sevenzip könyvtár mintafájljaival (különféle tömörítők, üres, titkosított).
func Test7zRead(t *testing.T) {
	mc, _ := exec.Command("go", "env", "GOMODCACHE").Output()
	td := filepath.Join(strings.TrimSpace(string(mc)), "github.com/bodgit/sevenzip@v1.5.2/testdata")
	if _, err := os.Stat(td); err != nil {
		t.Skip("nincs mintaadat")
	}
	for _, n := range []string{"lzma.7z", "lzma2.7z", "bzip2.7z", "deflate.7z", "copy.7z", "bcj2.7z", "delta.7z", "empty.7z", "file_and_empty.7z", "aes7z.7z"} {
		a, err := openArchive(filepath.Join(td, n))
		if err != nil {
			if n == "aes7z.7z" {
				t.Logf("%s: %v", n, err)
				continue
			}
			t.Errorf("%s: megnyitás: %v", n, err)
			continue
		}
		root := listOf(t, a, a.Root())
		out := t.TempDir()
		pr := &testProg{}
		err = a.Extract(root, out, pr)
		if n == "aes7z.7z" {
			if err == nil && len(pr.errs) == 0 {
				t.Errorf("%s: a jelszavas archívumnak hibát kellett volna adnia", n)
			} else {
				t.Logf("%s: %v %v", n, err, pr.errs)
			}
			continue
		}
		if err != nil || len(pr.errs) > 0 {
			t.Errorf("%s: kibontás: %v %v", n, err, pr.errs)
		}
		if a.Writable() && sevenZipExe() == "" {
			t.Errorf("%s: 7-Zip nélkül nem lehet írható", n)
		}
	}
}
