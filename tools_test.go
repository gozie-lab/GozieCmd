package main

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiff(t *testing.T) {
	a := strings.Split("a b c d e f g", " ")
	b := strings.Split("a x c d f g h", " ")
	rows := diffLines(a, b, func(s string) string { return s })
	var sb strings.Builder
	for _, r := range rows {
		sb.WriteString(string(r.Kind) + r.TA + "|" + r.TB + " ")
	}
	got := sb.String()
	want := "=a|a cb|x =c|c =d|d -e| =f|f =g|g +|h "
	if got != want {
		t.Fatalf("diff:\n%s\n%s", got, want)
	}
	// véletlen próbák: a sorok helyreállíthatók
	r := rand.New(rand.NewSource(1))
	for it := 0; it < 300; it++ {
		x, y := randLines(r), randLines(r)
		rows := diffLines(x, y, func(s string) string { return s })
		var ra, rb []string
		for _, w := range rows {
			if w.A > 0 {
				ra = append(ra, w.TA)
			}
			if w.B > 0 {
				rb = append(rb, w.TB)
			}
			if w.Kind == '=' && w.TA != w.TB {
				t.Fatal("egyezőnek jelölt eltérő sor")
			}
		}
		if strings.Join(ra, ",") != strings.Join(x, ",") || strings.Join(rb, ",") != strings.Join(y, ",") {
			t.Fatalf("hibás igazítás %v %v", x, y)
		}
	}
}

func randLines(r *rand.Rand) []string {
	n := r.Intn(30)
	out := make([]string, n)
	for i := range out {
		out[i] = string(rune('a' + r.Intn(5)))
	}
	return out
}

func TestTools(t *testing.T) {
	d := t.TempDir()
	data := make([]byte, 3_500_000)
	rand.New(rand.NewSource(2)).Read(data)
	src := filepath.Join(d, "nagy.bin")
	os.WriteFile(src, data, 0o644)
	out := filepath.Join(d, "darab")
	os.Mkdir(out, 0o755)
	n, err := splitFile(src, out, 1<<20, nopProgress{})
	if err != nil || n != 4 {
		t.Fatal(n, err)
	}
	j := filepath.Join(d, "vissza")
	os.Mkdir(j, 0o755)
	dest, note, err := joinFiles(filepath.Join(out, "nagy.bin.003"), j, nopProgress{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) || !strings.Contains(note, "CRC") {
		t.Fatal("összefűzés hibás", note)
	}
	// sérült darab
	os.WriteFile(filepath.Join(out, "nagy.bin.002"), make([]byte, 1<<20), 0o644)
	if _, _, err := joinFiles(filepath.Join(out, "nagy.bin.001"), j, nopProgress{}); err == nil {
		t.Fatal("a CRC-hibát nem vette észre")
	}

	// ellenőrzőösszeg
	os.WriteFile(filepath.Join(d, "a.txt"), []byte("alma"), 0o644)
	os.WriteFile(filepath.Join(d, "b.txt"), []byte("körte"), 0o644)
	lines, err := hashFiles(d, []string{filepath.Join(d, "a.txt"), filepath.Join(d, "b.txt")}, "SHA-256", nopProgress{})
	if err != nil || len(lines) != 2 {
		t.Fatal(err)
	}
	sf := filepath.Join(d, "x.sha256")
	os.WriteFile(sf, []byte(formatSums(lines)), 0o644)
	os.WriteFile(filepath.Join(d, "b.txt"), []byte("barack"), 0o644)
	res, algo, err := verifySums(sf, nopProgress{})
	if err != nil || algo != "SHA-256" || res[0].Status != 0 || res[1].Status != 1 {
		t.Fatal(res, algo, err)
	}
	if a, l := parseSums("MD5 (x y.txt) = d41d8cd98f00b204e9800998ecf8427e\n"); a != "MD5" || l[0].Name != "x y.txt" {
		t.Fatal(a, l)
	}

	// ismétlődők
	dd := filepath.Join(d, "dup")
	os.MkdirAll(filepath.Join(dd, "al"), 0o755)
	os.WriteFile(filepath.Join(dd, "1.txt"), []byte("ugyanaz"), 0o644)
	os.WriteFile(filepath.Join(dd, "al", "2.txt"), []byte("ugyanaz"), 0o644)
	os.WriteFile(filepath.Join(dd, "3.txt"), []byte("másvalami"), 0o644)
	os.WriteFile(filepath.Join(dd, "4.txt"), []byte("ugyanaX"), 0o644)
	gs, err := findDupes([]string{dd}, dupeOpts{Content: true}, nopProgress{})
	if err != nil || len(gs) != 1 || len(gs[0].Files) != 2 {
		t.Fatal(gs, err)
	}

	// dátum
	tm, err := parseDateTime("2024.03.05 10:20")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyAttrs([]string{dd}, attrRequest{SetTime: true, Time: tm, Recurse: true}, nil, nopProgress{}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Join(dd, "al", "2.txt"))
	st2, _ := os.Stat(dd)
	if !st.ModTime().Equal(tm) || !st2.ModTime().Equal(tm) {
		t.Fatal(st.ModTime(), st2.ModTime())
	}
	if _, err := parseSize("1,5G"); err != nil {
		t.Fatal(err)
	}
	if v, _ := parseSize("700"); v != 700<<20 {
		t.Fatal(v)
	}
	_ = time.Now
}

func TestCompareFiles(t *testing.T) {
	d := t.TempDir()
	a, b := filepath.Join(d, "a"), filepath.Join(d, "b")
	os.WriteFile(a, []byte("első\r\nmásodik\r\nharmadik\r\n"), 0o644)
	os.WriteFile(b, []byte("első\nMÁSODIK\nharmadik\nnegyedik\n"), 0o644)
	r, err := compareFiles(a, b, false, false)
	if err != nil || r.Same || r.Changes != 2 {
		t.Fatal(r, err)
	}
	r, _ = compareFiles(a, b, false, true)
	if r.Changes != 1 {
		t.Fatal(r.Changes)
	}
	os.WriteFile(a, []byte{0, 1, 2, 3}, 0o644)
	os.WriteFile(b, []byte{0, 1, 9, 3, 4}, 0o644)
	r, _ = compareFiles(a, b, false, false)
	if !r.Binary || r.Changes != 1 || r.Rows[0].A != 2 {
		t.Fatal(r)
	}
}

func TestDiffJump(t *testing.T) {
	k := []byte("==cc==-+=c")
	cases := [][3]int{{1, -1, 2}, {1, 2, 6}, {1, 3, 6}, {1, 6, 9}, {1, 9, -1}, {-1, 9, 6}, {-1, 7, 2}, {-1, 3, -1}, {-1, 5, 2}}
	for _, c := range cases {
		if got := diffJump(k, c[0], c[1]); got != c[2] {
			t.Fatalf("%v → %d", c, got)
		}
	}
}
