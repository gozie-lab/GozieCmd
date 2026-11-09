package main

// Fájleszközök közös magja: tartalmi összehasonlítás, ellenőrzőösszeg,
// ismétlődő fájlok, darabolás/összefűzés, dátum- és attribútummódosítás.

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// ── tartalmi összehasonlítás ──

// diffRow: egy sor az egymás mellé igazított nézetben.
// Kind: '=' egyezik, 'c' eltér, '-' csak bal oldalon, '+' csak jobb oldalon.
// A és B 1-től számozott sorszám (0: nincs ilyen sor az adott oldalon).
type diffRow struct {
	Kind   byte
	A, B   int
	TA, TB string
}

const diffMaxText = 32 << 20 // e fölött csak bájtonként hasonlítunk

// isBinaryData: NUL bájt az elején → bináris.
func isBinaryBytes(b []byte) bool {
	if len(b) > 8192 {
		b = b[:8192]
	}
	if bytes.HasPrefix(b, []byte{0xFF, 0xFE}) || bytes.HasPrefix(b, []byte{0xFE, 0xFF}) {
		return false // UTF-16 szöveg
	}
	return bytes.IndexByte(b, 0) >= 0
}

// textLines szöveggé alakítja a fájl tartalmát (UTF-8, UTF-16, különben Windows-1250).
func textLines(b []byte) []string {
	var s string
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		s = string(b[3:])
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		be := b[0] == 0xFE
		b = b[2:]
		r := make([]rune, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			v := rune(b[i]) | rune(b[i+1])<<8
			if be {
				v = rune(b[i])<<8 | rune(b[i+1])
			}
			r = append(r, v)
		}
		s = string(r)
	case utf8.Valid(b):
		s = string(b)
	default:
		d, err := charmap.Windows1250.NewDecoder().Bytes(b)
		if err != nil {
			d = b
		}
		s = string(d)
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// diffResult: a két fájl összehasonlításának eredménye.
type diffResult struct {
	Binary  bool
	Rows    []diffRow // szöveges eredmény
	Same    bool
	Changes int // eltérő blokkok száma
	SizeA   int64
	SizeB   int64
	Note    string
}

// compareFiles két fájlt hasonlít össze tartalom szerint.
func compareFiles(pa, pb string, ignoreSpace, ignoreCase bool) (*diffResult, error) {
	sa, err := os.Stat(pa)
	if err != nil {
		return nil, err
	}
	sb, err := os.Stat(pb)
	if err != nil {
		return nil, err
	}
	if sa.IsDir() || sb.IsDir() {
		return nil, errors.New(T("mappát nem lehet tartalom szerint összehasonlítani"))
	}
	res := &diffResult{SizeA: sa.Size(), SizeB: sb.Size()}
	if sa.Size() > diffMaxText || sb.Size() > diffMaxText {
		return binaryDiff(pa, pb, res)
	}
	a, err := os.ReadFile(pa)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(pb)
	if err != nil {
		return nil, err
	}
	if isBinaryBytes(a) || isBinaryBytes(b) {
		return binaryDiff(pa, pb, res)
	}
	la, lb := textLines(a), textLines(b)
	norm := func(s string) string {
		if ignoreSpace {
			s = strings.Join(strings.Fields(s), " ")
		}
		if ignoreCase {
			s = strings.ToLower(s)
		}
		return s
	}
	res.Rows = diffLines(la, lb, norm)
	res.Same = true
	prev := byte('=')
	for _, r := range res.Rows {
		if r.Kind != '=' {
			res.Same = false
			if prev == '=' {
				res.Changes++
			}
		}
		prev = r.Kind
	}
	return res, nil
}

// pickPair: a két összehasonlítandó fájl. Ha az aktív panelen pontosan két
// fájl van kijelölve, azok; különben a kurzor alatti fájl és a másik panel
// azonos nevű (vagy kurzor alatti) fájlja. Hiba esetén a harmadik érték az üzenet.
func pickPair(sel []*Entry, marked map[string]bool, cur *Entry, othPath string, othCur *Entry) (string, string, string) {
	var m []*Entry
	for _, e := range sel {
		if !e.IsDir && !e.IsParent && marked[e.Name] {
			m = append(m, e)
		}
	}
	if len(m) == 2 {
		return m[0].Path, m[1].Path, ""
	}
	if cur == nil || cur.IsDir || cur.IsParent {
		return "", "", T("Állj a kurzorral egy fájlra (a másik panelen az azonos nevű vagy a kurzor alatti fájllal hasonlítom össze), vagy jelölj ki pontosan két fájlt.")
	}
	other := filepath.Join(othPath, cur.Name)
	if st, err := os.Stat(other); err == nil && !st.IsDir() {
		return cur.Path, other, ""
	}
	if othCur != nil && !othCur.IsDir && !othCur.IsParent {
		return cur.Path, othCur.Path, ""
	}
	return "", "", fmt.Sprintf(T("A másik panelen nincs „%s” nevű fájl, és a kurzor sem fájlon áll."), cur.Name)
}

// binaryDiff bájtonként hasonlít; legfeljebb 1000 eltérő bájtot listáz.
func binaryDiff(pa, pb string, res *diffResult) (*diffResult, error) {
	res.Binary = true
	fa, err := os.Open(pa)
	if err != nil {
		return nil, err
	}
	defer fa.Close()
	fb, err := os.Open(pb)
	if err != nil {
		return nil, err
	}
	defer fb.Close()
	ra, rb := bufio.NewReaderSize(fa, 1<<20), bufio.NewReaderSize(fb, 1<<20)
	var off int64
	const maxRows = 1000
	n := 0
	for {
		x, ea := ra.ReadByte()
		y, eb := rb.ReadByte()
		if ea != nil || eb != nil {
			break
		}
		if x != y {
			if n < maxRows {
				res.Rows = append(res.Rows, diffRow{Kind: 'c', A: int(off), B: int(off),
					TA: fmt.Sprintf("%08X:  %02X  %s", off, x, printable(x)),
					TB: fmt.Sprintf("%08X:  %02X  %s", off, y, printable(y))})
			}
			n++
		}
		off++
	}
	res.Changes = n
	res.Same = n == 0 && res.SizeA == res.SizeB
	switch {
	case n > maxRows:
		res.Note = fmt.Sprintf(T("%s; az első %d látható."), Nf(n, "%d eltérő bájt"), maxRows)
	case n > 0:
		res.Note = fmt.Sprintf(Tn(n, "%d eltérő bájt."), n)
	}
	if res.SizeA != res.SizeB {
		if res.Note != "" {
			res.Note += " "
		}
		res.Note += fmt.Sprintf(T("A méret eltér: %s ↔ %s bájt."), fmtSize(res.SizeA), fmtSize(res.SizeB))
	}
	return res, nil
}

func printable(b byte) string {
	if b >= 32 && b < 127 {
		return "'" + string(rune(b)) + "'"
	}
	return ""
}

// diffLines Myers-algoritmussal igazítja egymás mellé a két szöveg sorait.
func diffLines(a, b []string, norm func(string) string) []diffRow {
	na, nb := make([]string, len(a)), make([]string, len(b))
	for i, s := range a {
		na[i] = norm(s)
	}
	for i, s := range b {
		nb[i] = norm(s)
	}
	// közös eleje és vége
	pre := 0
	for pre < len(na) && pre < len(nb) && na[pre] == nb[pre] {
		pre++
	}
	suf := 0
	for suf < len(na)-pre && suf < len(nb)-pre && na[len(na)-1-suf] == nb[len(nb)-1-suf] {
		suf++
	}
	ops := myers(na[pre:len(na)-suf], nb[pre:len(nb)-suf])
	// műveletsor: '=' / '-' / '+'
	all := make([]byte, 0, pre+len(ops)+suf)
	for i := 0; i < pre; i++ {
		all = append(all, '=')
	}
	all = append(all, ops...)
	for i := 0; i < suf; i++ {
		all = append(all, '=')
	}
	var rows []diffRow
	ia, ib := 0, 0
	for k := 0; k < len(all); {
		if all[k] == '=' {
			rows = append(rows, diffRow{Kind: '=', A: ia + 1, B: ib + 1, TA: a[ia], TB: b[ib]})
			ia++
			ib++
			k++
			continue
		}
		// egy eltérő blokk: törlések és beszúrások párba állítva
		var dels, ins []int
		for k < len(all) && all[k] != '=' {
			if all[k] == '-' {
				dels = append(dels, ia)
				ia++
			} else {
				ins = append(ins, ib)
				ib++
			}
			k++
		}
		for i := 0; i < len(dels) || i < len(ins); i++ {
			r := diffRow{Kind: 'c'}
			if i < len(dels) {
				r.A, r.TA = dels[i]+1, a[dels[i]]
			} else {
				r.Kind = '+'
			}
			if i < len(ins) {
				r.B, r.TB = ins[i]+1, b[ins[i]]
			} else {
				r.Kind = '-'
			}
			rows = append(rows, r)
		}
	}
	return rows
}

// diffJump: a következő (dir>0) vagy előző eltérő szakasz első sora; -1, ha nincs.
func diffJump(kinds []byte, dir, from int) int {
	start := func(i int) bool { return kinds[i] != '=' && (i == 0 || kinds[i-1] == '=') }
	if dir > 0 {
		for i := from + 1; i < len(kinds); i++ {
			if i >= 0 && start(i) {
				return i
			}
		}
		return -1
	}
	cur := from
	if cur >= len(kinds) {
		cur = len(kinds)
	}
	// ha egy szakaszon belül állunk, annak elejétől visszafelé keresünk
	for cur > 0 && cur < len(kinds) && kinds[cur] != '=' && !start(cur) {
		cur--
	}
	for i := cur - 1; i >= 0; i-- {
		if start(i) {
			return i
		}
	}
	return -1
}

// myers: legrövidebb szerkesztési sor ('=', '-', '+'). Ha túl sok az eltérés,
// a középső részt egyetlen cserének veszi.
func myers(a, b []string) []byte {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		out := make([]byte, 0, n+m)
		for i := 0; i < n; i++ {
			out = append(out, '-')
		}
		for i := 0; i < m; i++ {
			out = append(out, '+')
		}
		return out
	}
	const maxD = 3000
	max := n + m
	if max > maxD {
		max = maxD
	}
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	found := -1
	for d := 0; d <= max && found < 0; d++ {
		snap := make([]int, 2*d+3)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = d
			}
		}
		for k := -d - 1; k <= d+1; k++ {
			snap[k+d+1] = v[off+k]
		}
		trace = append(trace, snap)
	}
	if found < 0 {
		out := make([]byte, 0, n+m)
		for i := 0; i < n; i++ {
			out = append(out, '-')
		}
		for i := 0; i < m; i++ {
			out = append(out, '+')
		}
		return out
	}
	get := func(d, k int) int { return trace[d][k+d+1] }
	var rev []byte
	x, y := n, m
	for d := found; d > 0; d-- {
		k := x - y
		var pk int
		if k == -d || (k != d && get(d-1, k-1) < get(d-1, k+1)) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := get(d-1, pk)
		py := px - pk
		for x > px && y > py {
			rev = append(rev, '=')
			x--
			y--
		}
		if x == px {
			rev = append(rev, '+')
		} else {
			rev = append(rev, '-')
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		rev = append(rev, '=')
		x--
		y--
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// ── ellenőrzőösszeg ──

var hashAlgos = []string{"MD5", "SHA-1", "SHA-256", "SHA-512"}

func newHash(algo string) hash.Hash {
	switch algo {
	case "MD5":
		return md5.New()
	case "SHA-1":
		return sha1.New()
	case "SHA-512":
		return sha512.New()
	}
	return sha256.New()
}

func hashExt(algo string) string {
	return "." + strings.ToLower(strings.ReplaceAll(algo, "-", ""))
}

// algoForLen a hexa hossz alapján találja ki az algoritmust.
func algoForLen(n int) string {
	switch n {
	case 32:
		return "MD5"
	case 40:
		return "SHA-1"
	case 64:
		return "SHA-256"
	case 128:
		return "SHA-512"
	}
	return ""
}

// hashReader: io.Copy megszakíthatóan, folyamatjelzéssel.
func hashReader(r io.Reader, h hash.Hash, pr Progress) error {
	buf := make([]byte, 1<<20)
	for {
		if pr != nil && pr.Cancelled() {
			return errCancel
		}
		n, err := r.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			if pr != nil {
				pr.Add(int64(n))
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func hashFile(path, algo string, pr Progress) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := newHash(algo)
	if err := hashReader(f, h, pr); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sumLine: egy sor az ellenőrzőösszeg-fájlban.
type sumLine struct{ Hash, Name string }

// collectFiles a megadott útvonalak fájljait gyűjti (mappáknál rekurzívan),
// a base-hez viszonyított névvel.
func collectFiles(base string, paths []string) (files []string, rels []string, total int64) {
	for _, p := range paths {
		filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if info, e := d.Info(); e == nil {
				total += info.Size()
			}
			rel, e := filepath.Rel(base, q)
			if e != nil {
				rel = filepath.Base(q)
			}
			files = append(files, q)
			rels = append(rels, rel)
			return nil
		})
	}
	return
}

// hashFiles minden fájl összegét kiszámolja.
func hashFiles(base string, paths []string, algo string, pr Progress) ([]sumLine, error) {
	files, rels, total := collectFiles(base, paths)
	pr.SetTotal(total)
	var out []sumLine
	for i, f := range files {
		pr.Status(rels[i])
		h, err := hashFile(f, algo, pr)
		if err == errCancel {
			return out, err
		}
		if err != nil {
			pr.Err(rels[i], err)
			continue
		}
		out = append(out, sumLine{Hash: h, Name: rels[i]})
	}
	return out, nil
}

// formatSums a megszokott „hash *név” formában ír (md5sum/sha256sum binary mód).
func formatSums(lines []sumLine) string {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l.Hash + " *" + filepath.ToSlash(l.Name) + "\r\n")
	}
	return sb.String()
}

// parseSums: „hash  név”, „hash *név” és BSD „SHA256 (név) = hash” sorok.
func parseSums(data string) (algo string, lines []sumLine) {
	for _, ln := range strings.Split(strings.ReplaceAll(data, "\r", ""), "\n") {
		ln = strings.TrimSpace(strings.TrimPrefix(ln, "\uFEFF"))
		if ln == "" || ln[0] == ';' || ln[0] == '#' {
			continue
		}
		if i := strings.Index(ln, " ("); i > 0 && strings.Contains(ln, ") = ") {
			j := strings.LastIndex(ln, ") = ")
			name, h := ln[i+2:j], strings.TrimSpace(ln[j+4:])
			if algo == "" {
				algo = algoForLen(len(h))
			}
			lines = append(lines, sumLine{Hash: strings.ToLower(h), Name: name})
			continue
		}
		sp := strings.IndexAny(ln, " \t")
		if sp <= 0 {
			continue
		}
		h := ln[:sp]
		if _, err := hex.DecodeString(h); err != nil {
			continue
		}
		name := strings.TrimLeft(ln[sp:], " \t")
		name = strings.TrimPrefix(name, "*")
		if algo == "" {
			algo = algoForLen(len(h))
		}
		lines = append(lines, sumLine{Hash: strings.ToLower(h), Name: name})
	}
	return
}

// sumCheck: egy fájl ellenőrzésének eredménye.
type sumCheck struct {
	Name   string
	Status int // 0 rendben, 1 eltér, 2 hiányzik, 3 hiba
	Detail string
}

func sumStatusText(s int) string {
	return [...]string{"rendben", T("HIBÁS"), T("hiányzik"), "hiba"}[s]
}

// verifySums az ellenőrzőösszeg-fájl sorait ellenőrzi.
func verifySums(sumFile string, pr Progress) ([]sumCheck, string, error) {
	data, err := os.ReadFile(sumFile)
	if err != nil {
		return nil, "", err
	}
	algo, lines := parseSums(string(data))
	if a := extAlgo(sumFile); a != "" {
		algo = a
	}
	if algo == "" || len(lines) == 0 {
		return nil, "", errors.New(T("ez nem ellenőrzőösszeg-fájl (MD5, SHA-1, SHA-256, SHA-512)"))
	}
	dir := filepath.Dir(sumFile)
	var total int64
	for _, l := range lines {
		if st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(l.Name))); err == nil {
			total += st.Size()
		}
	}
	pr.SetTotal(total)
	var out []sumCheck
	for _, l := range lines {
		pr.Status(l.Name)
		p := filepath.Join(dir, filepath.FromSlash(l.Name))
		if _, err := os.Stat(p); err != nil {
			out = append(out, sumCheck{Name: l.Name, Status: 2})
			continue
		}
		h, err := hashFile(p, algo, pr)
		if err == errCancel {
			return out, algo, err
		}
		switch {
		case err != nil:
			out = append(out, sumCheck{Name: l.Name, Status: 3, Detail: errText(err)})
		case h != l.Hash:
			out = append(out, sumCheck{Name: l.Name, Status: 1, Detail: h})
		default:
			out = append(out, sumCheck{Name: l.Name})
		}
	}
	return out, algo, nil
}

func extAlgo(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md5":
		return "MD5"
	case ".sha1":
		return "SHA-1"
	case ".sha256":
		return "SHA-256"
	case ".sha512":
		return "SHA-512"
	}
	return ""
}

func isSumFile(name string) bool { return extAlgo(name) != "" }

// ── ismétlődő fájlok ──

type dupeOpts struct {
	MinSize  int64
	SameName bool // a névnek is egyeznie kell
	Content  bool // tartalom szerint (különben név + méret)
}

type dupeFile struct {
	Path string
	Mod  time.Time
}

type dupeGroup struct {
	Size  int64
	Files []dupeFile
}

func findDupes(roots []string, o dupeOpts, pr Progress) ([]dupeGroup, error) {
	if !o.Content {
		o.SameName = true
	}
	type key struct {
		size int64
		name string
	}
	groups := map[key][]dupeFile{}
	seen := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if pr.Cancelled() {
				return errCancel
			}
			if err != nil {
				return nil
			}
			if d.IsDir() {
				pr.Status(fmt.Sprintf(T("Keresés: %s"), p))
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if seen[p] {
				return nil
			}
			seen[p] = true
			info, e := d.Info()
			if e != nil || info.Size() < o.MinSize || info.Size() == 0 && o.Content {
				return nil
			}
			k := key{size: info.Size()}
			if o.SameName {
				k.name = strings.ToLower(d.Name())
			}
			groups[k] = append(groups[k], dupeFile{Path: p, Mod: info.ModTime()})
			return nil
		})
		if err == errCancel {
			return nil, err
		}
	}
	var out []dupeGroup
	if !o.Content {
		for k, fs := range groups {
			if len(fs) > 1 {
				out = append(out, dupeGroup{Size: k.size, Files: fs})
			}
		}
	} else {
		var total int64
		for k, fs := range groups {
			if len(fs) > 1 {
				total += k.size * int64(len(fs))
			}
		}
		pr.SetTotal(total)
		for k, fs := range groups {
			if len(fs) < 2 {
				continue
			}
			// először az első 64 KB, utána a teljes tartalom
			byHead := map[string][]dupeFile{}
			for _, f := range fs {
				pr.Status(fmt.Sprintf(T("Összehasonlítás: %s"), f.Path))
				h, err := headHash(f.Path, 64<<10)
				if err != nil {
					pr.Err(f.Path, err)
					continue
				}
				byHead[h] = append(byHead[h], f)
			}
			for _, hs := range byHead {
				if len(hs) < 2 {
					pr.Add(k.size)
					continue
				}
				byFull := map[string][]dupeFile{}
				for _, f := range hs {
					if k.size <= 64<<10 {
						byFull["x"] = append(byFull["x"], f)
						pr.Add(k.size)
						continue
					}
					pr.Status(fmt.Sprintf(T("Összehasonlítás: %s"), f.Path))
					h, err := hashFile(f.Path, "SHA-256", pr)
					if err == errCancel {
						return nil, err
					}
					if err != nil {
						pr.Err(f.Path, err)
						continue
					}
					byFull[h] = append(byFull[h], f)
				}
				for _, g := range byFull {
					if len(g) > 1 {
						out = append(out, dupeGroup{Size: k.size, Files: g})
					}
				}
			}
		}
	}
	for _, g := range out {
		sort.Slice(g.Files, func(i, j int) bool { return g.Files[i].Path < g.Files[j].Path })
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Files[0].Path < out[j].Files[0].Path
	})
	return out, nil
}

func headHash(p string, n int64) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyN(h, f, n); err != nil && err != io.EOF {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// dupeWaste: ennyi hely szabadulna fel, ha csoportonként egy példány maradna.
func dupeWaste(gs []dupeGroup) (files int, bytes int64) {
	for _, g := range gs {
		files += len(g.Files) - 1
		bytes += g.Size * int64(len(g.Files)-1)
	}
	return
}

// ── darabolás és összefűzés ──

var splitPresets = []struct {
	Label string
	Size  int64
}{
	{T("1,44 MB (hajlékonylemez)"), 1457664},
	{"10 MB", 10 << 20},
	{T("25 MB (e-mail melléklet)"), 25 << 20},
	{"100 MB", 100 << 20},
	{"700 MB (CD)", 700 << 20},
	{"4 GB (FAT32)", 4<<30 - 1},
	{"4,7 GB (DVD)", 4700000000},
}

// parseSize: „100”, „100 MB”, „1,5G”, „700k” → bájt (szám egymagában: MB).
func parseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(strings.ReplaceAll(s, ",", ".")))
	s = strings.TrimSuffix(strings.TrimSuffix(s, "B"), "I")
	mult := float64(1 << 20)
	switch {
	case strings.HasSuffix(s, "K"):
		mult, s = 1<<10, s[:len(s)-1]
	case strings.HasSuffix(s, "M"):
		mult, s = 1<<20, s[:len(s)-1]
	case strings.HasSuffix(s, "G"):
		mult, s = 1<<30, s[:len(s)-1]
	case strings.HasSuffix(s, "BYTE"), strings.HasSuffix(s, "BÁJT"):
		mult = 1
		s = strings.TrimSuffix(strings.TrimSuffix(s, "BYTE"), "BÁJT")
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v <= 0 {
		return 0, errors.New(fmt.Sprintf(T("érvénytelen méret: %s"), s))
	}
	n := int64(v * mult)
	if n < 1024 {
		return 0, errors.New(T("a darabméret legalább 1 KB legyen"))
	}
	return n, nil
}

func partName(base string, i int) string { return fmt.Sprintf("%s.%03d", base, i) }

// splitFile a fájlt part méretű darabokra vágja a destDir mappába, és a
// szokásos (filename/size/crc32) .crc leírót is megírja.
func splitFile(src, destDir string, part int64, pr Progress) (int, error) {
	st, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	if st.Size() <= part {
		return 0, errors.New(T("a fájl nem nagyobb a darabméretnél, nincs mit darabolni"))
	}
	count := int((st.Size() + part - 1) / part)
	if count > 999 {
		return 0, fmt.Errorf(T("túl sok darab lenne (%d); legfeljebb 999 lehet"), count)
	}
	base := filepath.Join(destDir, filepath.Base(src))
	for i := 1; i <= count; i++ {
		if _, err := os.Stat(partName(base, i)); err == nil {
			ok, err := pr.AskOverwrite(filepath.Base(partName(base, i)), T("A darabok már léteznek."))
			if err != nil {
				return 0, err
			}
			if !ok {
				return 0, errCancel
			}
			break
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	pr.SetTotal(st.Size())
	crc := crc32.NewIEEE()
	for i := 1; i <= count; i++ {
		name := partName(base, i)
		pr.Status(filepath.Base(name))
		out, err := os.Create(name)
		if err != nil {
			return i - 1, err
		}
		w := io.MultiWriter(out, crc)
		_, err = copyN(w, in, part, pr)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(name)
			return i - 1, err
		}
	}
	desc := fmt.Sprintf("filename=%s\r\nsize=%d\r\ncrc32=%08X\r\n", filepath.Base(src), st.Size(), crc.Sum32())
	if err := os.WriteFile(base+".crc", []byte(desc), 0o644); err != nil {
		return count, err
	}
	return count, nil
}

// copyN: io.CopyN megszakíthatóan, folyamatjelzéssel.
func copyN(w io.Writer, r io.Reader, n int64, pr Progress) (int64, error) {
	buf := make([]byte, 1<<20)
	var done int64
	for done < n {
		if pr.Cancelled() {
			return done, errCancel
		}
		want := int64(len(buf))
		if n-done < want {
			want = n - done
		}
		k, err := r.Read(buf[:want])
		if k > 0 {
			if _, werr := w.Write(buf[:k]); werr != nil {
				return done, werr
			}
			done += int64(k)
			pr.Add(int64(k))
		}
		if err == io.EOF {
			return done, nil
		}
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

// isPartName: „valami.001” alakú név.
func isPartName(name string) bool {
	ext := filepath.Ext(name)
	if len(ext) != 4 {
		return false
	}
	_, err := strconv.Atoi(ext[1:])
	return err == nil
}

// splitParts a darabok listája az egyik darab neve alapján (001-től folyamatosan).
func splitParts(any string) (base string, parts []string, err error) {
	if !isPartName(any) {
		return "", nil, errors.New(T("ez nem darabolt fájl (.001, .002 …)"))
	}
	base = strings.TrimSuffix(any, filepath.Ext(any))
	for i := 1; i <= 999; i++ {
		p := partName(base, i)
		if _, err := os.Stat(p); err != nil {
			break
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "", nil, errors.New(fmt.Sprintf(T("nem található az első darab: %s"), filepath.Base(partName(base, 1))))
	}
	return base, parts, nil
}

// crcInfo a .crc leíró tartalma.
type crcInfo struct {
	Name  string
	Size  int64
	CRC   uint32
	Valid bool
}

func readCRCFile(base string) crcInfo {
	data, err := os.ReadFile(base + ".crc")
	if err != nil {
		return crcInfo{}
	}
	var c crcInfo
	for _, ln := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "filename":
			c.Name = strings.TrimSpace(v)
		case "size":
			c.Size, _ = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		case "crc32":
			x, err := strconv.ParseUint(strings.TrimSpace(v), 16, 32)
			if err == nil {
				c.CRC = uint32(x)
				c.Valid = true
			}
		}
	}
	return c
}

// joinFiles a darabokat egy fájllá fűzi a destDir mappába; ha van .crc, ellenőriz.
func joinFiles(anyPart, destDir string, pr Progress) (string, string, error) {
	base, parts, err := splitParts(anyPart)
	if err != nil {
		return "", "", err
	}
	info := readCRCFile(base)
	name := filepath.Base(base)
	if info.Name != "" && !strings.ContainsAny(info.Name, `/\`) {
		name = info.Name
	}
	dest := filepath.Join(destDir, name)
	if _, err := os.Stat(dest); err == nil {
		ok, err := pr.AskOverwrite(name, T("A célfájl már létezik."))
		if err != nil {
			return "", "", err
		}
		if !ok {
			return "", "", errCancel
		}
	}
	var total int64
	for _, p := range parts {
		if st, err := os.Stat(p); err == nil {
			total += st.Size()
		}
	}
	pr.SetTotal(total)
	tmp := dest + ".gzpart"
	out, err := os.Create(tmp)
	if err != nil {
		return "", "", err
	}
	crc := crc32.NewIEEE()
	w := io.MultiWriter(out, crc)
	for _, p := range parts {
		pr.Status(filepath.Base(p))
		in, err := os.Open(p)
		if err != nil {
			out.Close()
			os.Remove(tmp)
			return "", "", err
		}
		_, err = copyN(w, in, 1<<62, pr)
		in.Close()
		if err != nil {
			out.Close()
			os.Remove(tmp)
			return "", "", err
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", "", err
	}
	note := fmt.Sprintf(Tn(len(parts), "%d darab összefűzve: %s (%s bájt)."), len(parts), name, fmtSize(total))
	if info.Valid {
		if info.Size != 0 && info.Size != total {
			os.Remove(tmp)
			return "", "", fmt.Errorf(T("a méret nem egyezik a .crc fájlban lévővel (%s ↔ %s bájt) – hiányzik egy darab?"), fmtSize(total), fmtSize(info.Size))
		}
		if crc.Sum32() != info.CRC {
			os.Remove(tmp)
			return "", "", errors.New(T("a CRC-ellenőrzés hibát talált: valamelyik darab sérült"))
		}
		note += T("\nA CRC-ellenőrzés rendben.")
	} else {
		note += T("\nNem volt .crc fájl, ezért ellenőrzés nem történt.")
	}
	os.Remove(dest)
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", "", err
	}
	return dest, note, nil
}

// ── attribútumok és dátum ──

// bitChange: bekapcsolandó és kikapcsolandó bitek.
type bitChange struct{ Set, Clear uint32 }

func (c bitChange) apply(v uint32) uint32 { return v&^c.Clear | c.Set }
func (c bitChange) empty() bool           { return c.Set == 0 && c.Clear == 0 }

// attrRequest: mit kell módosítani.
type attrRequest struct {
	Bits    bitChange
	SetTime bool
	Time    time.Time
	Recurse bool
}

// applyAttrs bejárja az elemeket (kérésre rekurzívan) és módosít.
// setBits az OS-specifikus attribútum/jog beállító.
func applyAttrs(paths []string, req attrRequest, setBits func(p string, isDir bool, c bitChange) error, pr Progress) (int, error) {
	n := 0
	do := func(p string, isDir bool) {
		if !req.Bits.empty() {
			if err := setBits(p, isDir, req.Bits); err != nil {
				pr.Err(p, err)
				return
			}
		}
		if req.SetTime {
			if err := os.Chtimes(p, req.Time, req.Time); err != nil {
				pr.Err(p, err)
				return
			}
		}
		n++
	}
	for _, root := range paths {
		if pr.Cancelled() {
			return n, errCancel
		}
		st, err := os.Lstat(root)
		if err != nil {
			pr.Err(root, err)
			continue
		}
		if !st.IsDir() || !req.Recurse {
			pr.Status(root)
			do(root, st.IsDir())
			continue
		}
		var dirs []string
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if pr.Cancelled() {
				return errCancel
			}
			if err != nil {
				pr.Err(p, err)
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				dirs = append(dirs, p) // a mappák dátuma a végén, különben a tartalom módosítása felülírná
				return nil
			}
			pr.Status(p)
			do(p, false)
			return nil
		})
		for i := len(dirs) - 1; i >= 0; i-- {
			do(dirs[i], true)
		}
	}
	if pr.Cancelled() {
		return n, errCancel
	}
	return n, nil
}

// parseDateTime: „2026.11.07 21:30[:15]”, „2026-11-07 21:30”, csak dátum is.
func parseDateTime(s string) (time.Time, error) {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "-", "."), "/", "."))
	s = strings.TrimSuffix(s, ".")
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, ". ", " ")), " ")
	for _, f := range []string{"2006.01.02 15:04:05", "2006.01.02 15:04", "2006.01.02", "2006.1.2 15:04:05", "2006.1.2 15:04", "2006.1.2"} {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New(T("érvénytelen dátum (minta: 2026.11.07 21:30:00)"))
}
