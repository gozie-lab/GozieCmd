package main

// Mappák szinkronizálása: két mappafa összevetése és a különbségek átmásolása
// (közös a Windows- és a Linux-felületben).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type syncStatus int

const (
	sSame   syncStatus = iota // azonos
	sOnlyA                    // csak bal oldalon
	sOnlyB                    // csak jobb oldalon
	sNewerA                   // a bal újabb
	sNewerB                   // a jobb újabb
	sDiffer                   // eltér, de nem dönthető el, melyik az újabb
)

type syncAct int

const (
	actNone syncAct = iota
	actToB          // bal → jobb
	actToA          // bal ← jobb
)

type syncSide struct {
	Exists bool
	IsDir  bool
	Size   int64
	Mod    time.Time
}

type syncItem struct {
	Rel    string // a mappán belüli út, '/' elválasztóval
	A, B   syncSide
	Status syncStatus
	Act    syncAct
}

type syncOpts struct {
	Subdirs bool
	Content bool // azonos méretnél a tartalom is számít (lassabb)
	Mode    int  // 0: mindkét irány, 1: csak bal → jobb, 2: csak jobb → bal
}

func (s syncStatus) symbol() string {
	return [...]string{"=", "→", "←", "→", "←", "≠"}[s]
}

func (a syncAct) symbol() string {
	return [...]string{"", "→", "←"}[a]
}

// syncTimeEqual: 2 mp tűrés (FAT, hálózati meghajtók).
func syncTimeEqual(a, b time.Time) bool {
	d := a.Sub(b)
	return d < 2*time.Second && d > -2*time.Second
}

func scanTree(root string, subdirs bool, pr Progress) (map[string]syncSide, error) {
	out := map[string]syncSide{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if pr != nil && pr.Cancelled() {
			return errCancel
		}
		if err != nil {
			if info != nil && info.IsDir() && p != root {
				return filepath.SkipDir
			}
			if p == root {
				return err
			}
			return nil
		}
		if p == root {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			out[rel] = syncSide{Exists: true, IsDir: true, Mod: info.ModTime()}
			if !subdirs {
				return filepath.SkipDir
			}
			if pr != nil {
				pr.Status(p)
			}
			return nil
		}
		if info.Mode().IsRegular() {
			out[rel] = syncSide{Exists: true, Size: info.Size(), Mod: info.ModTime()}
		}
		return nil
	})
	return out, err
}

func sameContent(a, b string, pr Progress) bool {
	fa, err := os.Open(a)
	if err != nil {
		return false
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false
	}
	defer fb.Close()
	ba, bb := make([]byte, 256<<10), make([]byte, 256<<10)
	for {
		if pr != nil && pr.Cancelled() {
			return false
		}
		na, ea := io.ReadFull(fa, ba)
		nb, eb := io.ReadFull(fb, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false
		}
		if pr != nil {
			pr.Add(int64(na))
		}
		if ea == io.EOF || ea == io.ErrUnexpectedEOF {
			return eb == io.EOF || eb == io.ErrUnexpectedEOF
		}
		if ea != nil || eb != nil {
			return false
		}
	}
}

// syncCompare két mappát vet össze; az eredmény útvonal szerint rendezett.
func syncCompare(rootA, rootB string, opt syncOpts, pr Progress) ([]syncItem, error) {
	if pr != nil {
		pr.Status(T("Bal oldal beolvasása…"))
	}
	ma, err := scanTree(rootA, opt.Subdirs, pr)
	if err != nil {
		return nil, err
	}
	if pr != nil {
		pr.Status(T("Jobb oldal beolvasása…"))
	}
	mb, err := scanTree(rootB, opt.Subdirs, pr)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for k := range ma {
		keys[k] = true
	}
	for k := range mb {
		keys[k] = true
	}
	var items []syncItem
	for k := range keys {
		a, b := ma[k], mb[k]
		// csak a „hiányzó” mappák érdekesek; a meglévő mappák tartalma külön sorokban jelenik meg
		if a.IsDir || b.IsDir {
			if a.Exists && b.Exists {
				continue
			}
			if (a.Exists && a.IsDir && hasPrefixKey(ma, k)) || (b.Exists && b.IsDir && hasPrefixKey(mb, k)) {
				continue // nem üres mappa: a fájljai úgyis átkerülnek
			}
		}
		it := syncItem{Rel: k, A: a, B: b}
		switch {
		case a.Exists && !b.Exists:
			it.Status = sOnlyA
		case !a.Exists && b.Exists:
			it.Status = sOnlyB
		case a.IsDir != b.IsDir:
			it.Status = sDiffer
		case a.Size == b.Size && syncTimeEqual(a.Mod, b.Mod):
			it.Status = sSame
			if opt.Content {
				if pr != nil {
					pr.Status(fmt.Sprintf(T("Tartalom összevetése: %s"), k))
				}
				if !sameContent(filepath.Join(rootA, filepath.FromSlash(k)), filepath.Join(rootB, filepath.FromSlash(k)), pr) {
					it.Status = sDiffer
				}
			}
		case opt.Content && a.Size == b.Size && sameContent(filepath.Join(rootA, filepath.FromSlash(k)), filepath.Join(rootB, filepath.FromSlash(k)), pr):
			it.Status = sSame // csak az idő tér el
		case syncTimeEqual(a.Mod, b.Mod):
			it.Status = sDiffer
		case a.Mod.After(b.Mod):
			it.Status = sNewerA
		default:
			it.Status = sNewerB
		}
		it.Act = defaultAct(it.Status, opt.Mode)
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Rel) < strings.ToLower(items[j].Rel)
	})
	return items, nil
}

func hasPrefixKey(m map[string]syncSide, dir string) bool {
	pre := dir + "/"
	for k := range m {
		if strings.HasPrefix(k, pre) {
			return true
		}
	}
	return false
}

func defaultAct(s syncStatus, mode int) syncAct {
	var act syncAct
	switch s {
	case sOnlyA, sNewerA:
		act = actToB
	case sOnlyB, sNewerB:
		act = actToA
	}
	if (mode == 1 && act == actToA) || (mode == 2 && act == actToB) {
		return actNone
	}
	return act
}

// cycleAct: kattintásra a következő irány (→, ←, semmi), csak ahol értelmes.
func (it *syncItem) cycleAct() {
	order := []syncAct{actToB, actToA, actNone}
	for i, a := range order {
		if a == it.Act {
			for k := 1; k <= 3; k++ {
				n := order[(i+k)%3]
				if (n == actToB && !it.A.Exists) || (n == actToA && !it.B.Exists) {
					continue
				}
				it.Act = n
				return
			}
		}
	}
}

// syncSummary: hány elem és mennyi adat megy jobbra, balra; hány eltérés vár döntésre.
func syncSummary(items []syncItem) (toB, toA, conflicts, same int, bytesB, bytesA int64) {
	for _, it := range items {
		switch {
		case it.Act == actToB:
			toB++
			bytesB += it.A.Size
		case it.Act == actToA:
			toA++
			bytesA += it.B.Size
		case it.Status == sSame:
			same++
		default:
			conflicts++
		}
	}
	return
}

// syncExecute a kijelölt irányokban másol; a dátumot is átviszi.
func syncExecute(items []syncItem, rootA, rootB string, pr Progress) error {
	var total int64
	for _, it := range items {
		switch it.Act {
		case actToB:
			total += it.A.Size
		case actToA:
			total += it.B.Size
		}
	}
	pr.SetTotal(total)
	for _, it := range items {
		if pr.Cancelled() {
			return errCancel
		}
		var src, dst string
		var side syncSide
		switch it.Act {
		case actToB:
			src, dst, side = rootA, rootB, it.A
		case actToA:
			src, dst, side = rootB, rootA, it.B
		default:
			continue
		}
		rel := filepath.FromSlash(it.Rel)
		from, to := filepath.Join(src, rel), filepath.Join(dst, rel)
		pr.Status(from)
		if side.IsDir {
			if err := os.MkdirAll(to, 0o755); err != nil {
				pr.Err(it.Rel, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			pr.Err(it.Rel, err)
			continue
		}
		tmp := to + ".gozie-sync"
		if err := copyFileProgress(from, tmp, pr); err != nil {
			os.Remove(tmp)
			if errors.Is(err, errCancel) || pr.Cancelled() {
				return errCancel
			}
			pr.Err(it.Rel, err)
			continue
		}
		if st, err := os.Stat(from); err == nil {
			os.Chmod(tmp, st.Mode().Perm())
		}
		os.Chtimes(tmp, side.Mod, side.Mod)
		if _, err := os.Stat(to); err == nil {
			os.Chmod(to, 0o666) // csak olvasható cél felülírásához
		}
		if err := os.Rename(tmp, to); err != nil {
			os.Remove(tmp)
			pr.Err(it.Rel, err)
		}
	}
	return nil
}
