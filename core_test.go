package main

// Átfogó tesztek a közös magra: archívumok, átnevezés, keresés, segédfüggvények.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type testProg struct {
	mu        sync.Mutex
	errs      []string
	overwrite bool
	asked     int
	done      int64
}

func (p *testProg) Cancelled() bool { return false }
func (p *testProg) SetTotal(int64)  {}
func (p *testProg) Status(string)   {}
func (p *testProg) Add(n int64)     { p.mu.Lock(); p.done += n; p.mu.Unlock() }
func (p *testProg) Err(n string, e error) {
	p.mu.Lock()
	p.errs = append(p.errs, n+": "+e.Error())
	p.mu.Unlock()
}
func (p *testProg) AskOverwrite(string, string) (bool, error) { p.asked++; return p.overwrite, nil }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for n, c := range files {
		p := filepath.Join(root, filepath.FromSlash(n))
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte(c), 0o644))
	}
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(root, p)
			out[filepath.ToSlash(rel)] = string(b)
		}
		return nil
	})
	return out
}

func names(es []*Entry) []string {
	var s []string
	for _, e := range es {
		n := e.Name
		if e.IsDir {
			n += "/"
		}
		s = append(s, n)
	}
	sort.Strings(s)
	return s
}

func findEntry(t *testing.T, a Archive, dir, name string) *Entry {
	t.Helper()
	es, err := a.List(dir)
	must(t, err)
	for _, e := range es {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("%q nincs a(z) %q mappában: %v", name, dir, names(es))
	return nil
}

// Írható formátumok teljes köre: létrehozás, listázás, felülírás, mappa, törlés, kibontás.
func TestArchiveRoundTrip(t *testing.T) {
	for _, ext := range []string{".zip", ".tar", ".tar.gz", ".tgz", ".tar.xz"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "forrás")
			files := map[string]string{
				"forrás/a.txt":            "alma",
				"forrás/ékezetes név.txt": "árvíztűrő tükörfúrógép",
				"forrás/al/b.bin":         strings.Repeat("x", 100000),
				"forrás/al/mély/c.txt":    "c",
				"forrás/üres mappa/.keep": "",
			}
			writeTree(t, dir, files)
			arcPath := filepath.Join(dir, "teszt"+ext)
			a, err := openArchive(arcPath)
			must(t, err)
			if !a.Writable() {
				t.Fatal("írhatónak kellene lennie")
			}
			pr := &testProg{}
			must(t, a.AddLocal([]string{src}, a.Root(), pr))
			if len(pr.errs) > 0 {
				t.Fatal(pr.errs)
			}
			// újranyitva is ugyanaz
			a, err = openArchive(arcPath)
			must(t, err)
			root, _ := a.List(a.Root())
			if got := names(root); len(got) != 1 || got[0] != "forrás/" {
				t.Fatalf("gyökér: %v", got)
			}
			fd := findEntry(t, a, a.Root(), "forrás")
			got := names(listOf(t, a, fd.Path))
			want := []string{"a.txt", "al/", "ékezetes név.txt", "üres mappa/"}
			sort.Strings(want)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("lista: %v, várt: %v", got, want)
			}
			if e := findEntry(t, a, a.Join(fd.Path, "al"), "b.bin"); e.Size != 100000 {
				t.Errorf("méret: %d", e.Size)
			}
			// mappa létrehozása
			must(t, a.Mkdir(fd.Path, "új mappa"))
			findEntry(t, a, fd.Path, "új mappa")
			// felülírás: nem → marad, igen → csere
			must(t, os.WriteFile(filepath.Join(src, "a.txt"), []byte("körte"), 0o644))
			pr = &testProg{overwrite: false}
			must(t, a.AddLocal([]string{filepath.Join(src, "a.txt")}, fd.Path, pr))
			if pr.asked != 1 {
				t.Errorf("rákérdezés: %d", pr.asked)
			}
			pr = &testProg{overwrite: true}
			must(t, a.AddLocal([]string{filepath.Join(src, "a.txt")}, fd.Path, pr))
			// törlés
			must(t, a.Delete([]*Entry{findEntry(t, a, fd.Path, "ékezetes név.txt")}, &testProg{}))
			if g := names(listOf(t, a, fd.Path)); strings.Contains(strings.Join(g, "|"), "ékezetes") {
				t.Errorf("törlés után: %v", g)
			}
			// kibontás
			out := filepath.Join(dir, "ki")
			must(t, os.Mkdir(out, 0o755))
			pr = &testProg{}
			must(t, a.Extract([]*Entry{fd}, out, pr))
			if len(pr.errs) > 0 {
				t.Fatal(pr.errs)
			}
			tree := readTree(t, out)
			if tree["forrás/a.txt"] != "körte" || tree["forrás/al/mély/c.txt"] != "c" || len(tree["forrás/al/b.bin"]) != 100000 {
				t.Errorf("kibontott tartalom: %v", keysOf(tree))
			}
			if _, ok := tree["forrás/ékezetes név.txt"]; ok {
				t.Error("a törölt fájl is kibontódott")
			}
			if st, err := os.Stat(filepath.Join(out, "forrás", "új mappa")); err != nil || !st.IsDir() {
				t.Error("az üres új mappa nem bontódott ki")
			}
			// egyetlen almappa kibontása: a szülő nélkül kerül a célba
			out2 := filepath.Join(dir, "ki2")
			must(t, os.Mkdir(out2, 0o755))
			must(t, a.Extract([]*Entry{findEntry(t, a, fd.Path, "al")}, out2, &testProg{}))
			if readTree(t, out2)["al/mély/c.txt"] != "c" {
				t.Errorf("almappa kibontása: %v", keysOf(readTree(t, out2)))
			}
			// meglévő fájl kibontáskor: kérdez, nem írja felül
			pr = &testProg{overwrite: false}
			must(t, a.Extract([]*Entry{findEntry(t, a, fd.Path, "a.txt")}, filepath.Join(out, "forrás"), pr))
			if pr.asked != 1 {
				t.Errorf("kibontáskori rákérdezés: %d", pr.asked)
			}
		})
	}
}

func must2[T any](t *testing.T, v T, err error) T {
	t.Helper()
	must(t, err)
	return v
}

func keysOf(m map[string]string) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// Kártékony archívum: a „../” és abszolút útvonalú elemek nem kerülhetnek a célon kívülre.
func TestArchivePathTraversal(t *testing.T) {
	dir := t.TempDir()
	evil := []string{"../kint.txt", "../../kint2.txt", "/abs.txt", "ok/../../kint3.txt", "jo.txt"}
	// ZIP
	zp := filepath.Join(dir, "rossz.zip")
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for _, n := range evil {
		w, _ := zw.Create(n)
		w.Write([]byte("x"))
	}
	zw.Close()
	must(t, os.WriteFile(zp, zb.Bytes(), 0o644))
	// TAR
	tp := filepath.Join(dir, "rossz.tar")
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	for _, n := range evil {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
		tw.Write([]byte("x"))
	}
	tw.WriteHeader(&tar.Header{Name: "link", Linkname: "/etc", Typeflag: tar.TypeSymlink})
	tw.Close()
	must(t, os.WriteFile(tp, tb.Bytes(), 0o644))

	for _, p := range []string{zp, tp} {
		a, err := openArchive(p)
		if err != nil {
			continue // a megnyitás elutasítása is elfogadható
		}
		out := filepath.Join(dir, "cel", filepath.Base(p))
		must(t, os.MkdirAll(out, 0o755))
		root, _ := a.List(a.Root())
		a.Extract(root, out, &testProg{})
		filepath.Walk(dir, func(q string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && !strings.HasPrefix(q, filepath.Join(dir, "cel")+string(os.PathSeparator)) && q != zp && q != tp {
				t.Errorf("%s: a célon kívülre írt: %s", filepath.Base(p), q)
			}
			return nil
		})
		if _, err := os.Lstat(filepath.Join(out, "link")); err == nil {
			if l, _ := os.Readlink(filepath.Join(out, "link")); l != "" {
				t.Errorf("szimbolikus link jött létre: %s", l)
			}
		}
	}
}

func TestReadOnlyFormats(t *testing.T) {
	dir := t.TempDir()
	// .gz egyetlen fájl
	gzp := filepath.Join(dir, "level.txt.gz")
	a, err := openArchive(gzp)
	if err == nil && a.Writable() {
		t.Log("a .gz írható (ha nincs még meg)")
	}
	if archiveFormat("x.ACE") != "nope" || archiveFormat("x.docx") != "" || archiveFormat("a.TAR.GZ") != "tgz" {
		t.Error("formátumfelismerés")
	}
	if _, err := openArchive(filepath.Join(dir, "x.ace")); err == nil {
		t.Error("ACE-t el kell utasítani")
	}
}

func TestRenameMasks(t *testing.T) {
	mod := time.Date(2024, 3, 5, 7, 8, 9, 0, time.Local)
	items := []renameItem{
		{Name: "Kép 001.JPG", Dir: "/a/Nyaralás", Mod: mod},
		{Name: "kep002.jpg", Dir: "/a/Nyaralás", Mod: mod},
		{Name: "mappa", Dir: "/a/Nyaralás", Mod: mod, IsDir: true},
	}
	cases := []struct {
		o    renameOpts
		want []string
	}{
		{renameOpts{NameMask: "[N]", ExtMask: "[E]"}, []string{"Kép 001.JPG", "kep002.jpg", "mappa"}},
		{renameOpts{NameMask: "[P]_[C]", ExtMask: "[E]", CStart: 1, CStep: 1, CDigits: 3}, []string{"Nyaralás_001.JPG", "Nyaralás_002.jpg", "Nyaralás_003"}},
		{renameOpts{NameMask: "[YMD]_[N1-3]", ExtMask: "[E]"}, []string{"2024-03-05_Kép.JPG", "2024-03-05_kep.jpg", "2024-03-05_map"}},
		{renameOpts{NameMask: "[N]", ExtMask: "[E]", Case: 1}, []string{"kép 001.jpg", "kep002.jpg", "mappa"}},
		{renameOpts{NameMask: "[N]", ExtMask: "[E]", Case: 4}, []string{"Kép 001.jpg", "Kep002.jpg", "Mappa"}},
		{renameOpts{NameMask: "[N]", ExtMask: "[E]", Search: "kép", Replace: "Foto", MatchCase: false}, []string{"Foto 001.JPG", "kep002.jpg", "mappa"}},
		{renameOpts{NameMask: "[N]", ExtMask: "[E]", Search: `(\d+)`, Replace: "#$1", Regex: true}, []string{"Kép #001.JPG", "kep#002.jpg", "mappa"}},
		{renameOpts{NameMask: "[[[N]]]", ExtMask: "[E]"}, []string{"[Kép 001].JPG", "[kep002].jpg", "[mappa]"}},
		{renameOpts{NameMask: "[N-3]", ExtMask: "txt"}, []string{"001.txt", "002.txt", "ppa.txt"}},
		{renameOpts{NameMask: "[N]", ExtMask: "[E]", Search: "$", Replace: "$x"}, []string{"Kép 001.JPG", "kep002.jpg", "mappa"}},
	}
	for i, c := range cases {
		got, probs, err := computeNames(items, c.o)
		must(t, err)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%d: %v, várt: %v (%v)", i, got, c.want, probs)
		}
	}
	// ütközés és tiltott nevek
	_, probs, _ := computeNames(items[:2], renameOpts{NameMask: "azonos", ExtMask: "jpg"})
	if probs[1] == "" {
		t.Error("ütközést kellett volna jelezni")
	}
	_, probs, _ = computeNames(items[:1], renameOpts{NameMask: "a/b", ExtMask: ""})
	if probs[0] == "" {
		t.Error("tiltott karaktert kellett volna jelezni")
	}
	_, probs, _ = computeNames(items[:1], renameOpts{NameMask: "", ExtMask: ""})
	if probs[0] == "" {
		t.Error("üres nevet kellett volna jelezni")
	}
	if _, _, err := computeNames(items, renameOpts{NameMask: "[N]", Search: "(", Regex: true}); err == nil {
		t.Error("hibás regexet kellett volna jelezni")
	}
}

func TestDoRenamesSwap(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.WriteFile(a, []byte("A"), 0o644)
	os.WriteFile(b, []byte("B"), 0o644)
	must(t, doRenames([]renamePair{{a, b}, {b, a}}))
	if x, _ := os.ReadFile(a); string(x) != "B" {
		t.Errorf("csere: %s", x)
	}
	// hiba esetén visszaáll
	err := doRenames([]renamePair{{a, filepath.Join(dir, "c")}, {filepath.Join(dir, "nincs"), filepath.Join(dir, "d")}})
	if err == nil {
		t.Fatal("hibát vártam")
	}
	if _, err := os.Stat(a); err != nil {
		t.Error("a visszaállítás nem történt meg")
	}
}

func listOf(t *testing.T, a Archive, dir string) []*Entry {
	t.Helper()
	es, err := a.List(dir)
	must(t, err)
	return es
}
