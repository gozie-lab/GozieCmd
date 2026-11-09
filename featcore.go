package main

// Közös segédek a lapfülekhez, a gyorskereséshez, a kedvenc mappákhoz és az
// előzményekhez (a Windows- és a Linux-felület is ezeket használja).

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// ── lapfülek ──

type tabState struct {
	Path string `json:"path"`
	Sel  string `json:"sel,omitempty"` // a kurzor alatti elem neve
}

func encodeTabs(tabs []tabState) string {
	b, _ := json.Marshal(tabs)
	return string(b)
}

func decodeTabs(s string) []tabState {
	var t []tabState
	if json.Unmarshal([]byte(s), &t) != nil {
		return nil
	}
	var out []tabState
	for _, x := range t {
		if x.Path != "" {
			out = append(out, x)
		}
	}
	return out
}

// tabLabel: a fül felirata – a mappa neve, gyökérnél maga az útvonal.
func tabLabel(path string) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || name == "" || strings.HasSuffix(path, `:\`) {
		name = path
	}
	if r := []rune(name); len(r) > 22 {
		name = fmt.Sprintf(T("%s…"), string(r[:21]))
	}
	return name
}

// ── gyorskeresés ──

// foldName kis betűssé és ékezet nélkülivé alakít (a „szamla” megtalálja a „Számla.pdf”-et).
func foldName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if base, ok := foldMap[r]; ok {
			r = base
		}
		b.WriteRune(r)
	}
	return b.String()
}

var foldMap = map[rune]rune{
	'á': 'a', 'é': 'e', 'í': 'i', 'ó': 'o', 'ö': 'o', 'ő': 'o', 'ú': 'u', 'ü': 'u', 'ű': 'u',
	'à': 'a', 'â': 'a', 'ä': 'a', 'è': 'e', 'ê': 'e', 'ë': 'e', 'î': 'i', 'ï': 'i', 'ô': 'o', 'ù': 'u', 'û': 'u',
	'ç': 'c', 'ñ': 'n', 'š': 's', 'ž': 'z', 'č': 'c', 'ř': 'r', 'ý': 'y', 'ą': 'a', 'ę': 'e', 'ł': 'l', 'ś': 's', 'ź': 'z', 'ż': 'z',
}

// quickMatch: a név a beírt szöveggel kezdődik; „*” után bárhol lehet benne.
func quickMatch(name, typed string) bool {
	if typed == "" {
		return true
	}
	n, t := foldName(name), foldName(typed)
	if strings.HasPrefix(t, "*") {
		return strings.Contains(n, strings.TrimLeft(t, "*"))
	}
	if i := strings.Index(t, "*"); i > 0 {
		return strings.HasPrefix(n, t[:i]) && strings.Contains(n[i:], strings.Trim(t[i:], "*"))
	}
	return strings.HasPrefix(n, t)
}

// quickFind: a következő (dir: +1) vagy előző (-1) egyező sor from-tól kezdve, körbe; -1 ha nincs.
func quickFind(names []string, typed string, from, dir int) int {
	n := len(names)
	if n == 0 {
		return -1
	}
	if from < 0 {
		from = 0
	}
	for k := 0; k < n; k++ {
		i := ((from+dir*k)%n + n) % n
		if quickMatch(names[i], typed) {
			return i
		}
	}
	return -1
}

// ── kedvenc mappák ──

type hotEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func decodeHotlist(s string) []hotEntry {
	var h []hotEntry
	if s == "" || json.Unmarshal([]byte(s), &h) != nil {
		return nil
	}
	return h
}

func encodeHotlist(h []hotEntry) string {
	b, _ := json.Marshal(h)
	return string(b)
}

// ── előzmények ──

// historyMenu: a vissza-lista, legfrissebb elöl, ismétlődés nélkül (legfeljebb n elem).
func historyMenu(back []string, current string, n int) []string {
	seen := map[string]bool{current: true}
	var out []string
	for i := len(back) - 1; i >= 0 && len(out) < n; i-- {
		if p := back[i]; !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
