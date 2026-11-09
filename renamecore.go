package main

// A többfájl-átnevező szabályai (szögletes zárójeles maszkok). Felület nélkül, tesztelhető.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type renameOpts struct {
	NameMask, ExtMask string
	Search, Replace   string
	Regex, MatchCase  bool
	Case              int // 0 változatlan, 1 kisbetű, 2 NAGYBETŰ, 3 Első nagy, 4 Minden Szó Nagy
	CStart, CStep     int
	CDigits           int
}

type renameItem struct {
	Name  string
	Dir   string
	Mod   time.Time
	IsDir bool
}

var maskToken = regexp.MustCompile(`\[(N|E|C|P|YMD|hms|Y|M|D|h|m|s)([-0-9,]*)\]`)

// sliceRunes kivágás: "2-5", "3", "2-", "-3" (utolsó 3), "2,4" (2-től 4 db).
func sliceRunes(s, spec string) string {
	r := []rune(s)
	n := len(r)
	if spec == "" {
		return s
	}
	clamp := func(i int) int {
		if i < 0 {
			return 0
		}
		if i > n {
			return n
		}
		return i
	}
	pos := func(v int) int { // 1-alapú, negatív: a végétől
		if v < 0 {
			return n + v
		}
		return v - 1
	}
	if a, b, ok := strings.Cut(spec, ","); ok {
		x, err1 := strconv.Atoi(a)
		l, err2 := strconv.Atoi(b)
		if err1 != nil || err2 != nil {
			return s
		}
		st := clamp(pos(x))
		return string(r[st:clamp(st+l)])
	}
	if strings.HasPrefix(spec, "-") && !strings.Contains(spec[1:], "-") {
		k, err := strconv.Atoi(spec[1:])
		if err != nil {
			return s
		}
		return string(r[clamp(n-k):])
	}
	if a, b, ok := strings.Cut(spec, "-"); ok {
		x, err := strconv.Atoi(a)
		if err != nil {
			return s
		}
		end := n
		if b != "" {
			y, err := strconv.Atoi(b)
			if err != nil {
				return s
			}
			end = clamp(pos(y) + 1)
		}
		st := clamp(pos(x))
		if st > end {
			return ""
		}
		return string(r[st:end])
	}
	x, err := strconv.Atoi(spec)
	if err != nil {
		return s
	}
	st := clamp(pos(x))
	return string(r[st:clamp(st+1)])
}

func splitNameExt(it renameItem) (string, string) {
	if it.IsDir {
		return it.Name, ""
	}
	ext := filepath.Ext(it.Name)
	if len(ext) <= 1 || len(ext) == len(it.Name) {
		return it.Name, ""
	}
	return it.Name[:len(it.Name)-len(ext)], ext[1:]
}

func lastPathElem(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func applyMask(mask string, it renameItem, counter string) string {
	base, ext := splitNameExt(it)
	value := func(m []string) string {
		switch m[1] {
		case "N":
			return sliceRunes(base, m[2])
		case "E":
			return sliceRunes(ext, m[2])
		case "C":
			return counter
		case "P":
			return lastPathElem(it.Dir)
		case "YMD":
			return it.Mod.Format("2006-01-02")
		case "hms":
			return it.Mod.Format("15.04.05")
		case "Y":
			return it.Mod.Format("2006")
		case "M":
			return it.Mod.Format("01")
		case "D":
			return it.Mod.Format("02")
		case "h":
			return it.Mod.Format("15")
		case "m":
			return it.Mod.Format("04")
		case "s":
			return it.Mod.Format("05")
		}
		return m[0]
	}
	// balról jobbra: „[[” és „]]” szó szerinti zárójel, „[X]” helyettesítés
	var b strings.Builder
	for i := 0; i < len(mask); {
		switch {
		case strings.HasPrefix(mask[i:], "[["):
			b.WriteByte('[')
			i += 2
		case strings.HasPrefix(mask[i:], "]]"):
			b.WriteByte(']')
			i += 2
		case mask[i] == '[':
			if loc := maskToken.FindStringSubmatchIndex(mask[i:]); loc != nil && loc[0] == 0 {
				m := []string{mask[i : i+loc[1]], mask[i+loc[2] : i+loc[3]], mask[i+loc[4] : i+loc[5]]}
				b.WriteString(value(m))
				i += loc[1]
				continue
			}
			b.WriteByte('[')
			i++
		default:
			b.WriteByte(mask[i])
			i++
		}
	}
	return b.String()
}

func changeCase(s string, mode int) string {
	switch mode {
	case 1:
		return strings.ToLower(s)
	case 2:
		return strings.ToUpper(s)
	case 3:
		r := []rune(strings.ToLower(s))
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
		}
		return string(r)
	case 4:
		r := []rune(strings.ToLower(s))
		up := true
		for i, c := range r {
			if up && unicode.IsLetter(c) {
				r[i] = unicode.ToUpper(c)
				up = false
			} else if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '\'' {
				up = true
			}
		}
		return string(r)
	}
	return s
}

// computeNames az új neveket és soronként a hibát adja ("" ha rendben).
func computeNames(items []renameItem, o renameOpts) ([]string, []string, error) {
	var re *regexp.Regexp
	if o.Search != "" {
		pat := o.Search
		if !o.Regex {
			pat = regexp.QuoteMeta(pat)
		}
		if !o.MatchCase {
			pat = "(?i)" + pat
		}
		var err error
		if re, err = regexp.Compile(pat); err != nil {
			return nil, nil, fmt.Errorf(T("hibás keresőkifejezés: %v"), err)
		}
	}
	if o.CStep == 0 {
		o.CStep = 1
	}
	names := make([]string, len(items))
	problems := make([]string, len(items))
	for i, it := range items {
		c := fmt.Sprintf("%0*d", o.CDigits, o.CStart+i*o.CStep)
		nb := applyMask(o.NameMask, it, c)
		ne := applyMask(o.ExtMask, it, c)
		if it.IsDir && o.ExtMask == "[E]" {
			ne = ""
		}
		full := nb
		if ne != "" {
			full += "." + ne
		}
		if re != nil {
			repl := o.Replace
			if !o.Regex {
				repl = strings.ReplaceAll(repl, "$", "$$")
			}
			full = re.ReplaceAllString(full, repl)
		}
		if o.Case != 0 {
			b, e := splitNameExt(renameItem{Name: full, IsDir: it.IsDir})
			full = changeCase(b, o.Case)
			if e != "" {
				extMode := o.Case
				if extMode >= 3 { // „Első nagy” és „Minden Szó” esetén a kiterjesztés kisbetűs
					extMode = 1
				}
				full += "." + changeCase(e, extMode)
			}
		}
		names[i] = full
		switch {
		case strings.TrimSpace(full) == "" || full == "." || full == "..":
			problems[i] = T("üres név")
		case strings.ContainsAny(full, `\/:*?"<>|`):
			problems[i] = T("tiltott karakter (\\ / : * ? \" < > |)")
		case strings.HasSuffix(full, " ") || strings.HasSuffix(full, "."):
			problems[i] = T("szóközre vagy pontra nem végződhet")
		}
	}
	seen := map[string]int{}
	for i, n := range names {
		k := strings.ToLower(filepath.Join(items[i].Dir, n))
		if j, ok := seen[k]; ok && problems[i] == "" {
			problems[i] = fmt.Sprintf(T("ütközik ezzel: %s"), items[j].Name)
		}
		seen[k] = i
	}
	return names, problems, nil
}

type renamePair struct{ from, to string }

// doRenames két lépésben nevez át (előbb ideiglenes névre), így a nevek cseréje
// (A→B, B→A) is működik; hiba esetén visszaállít.
func doRenames(pairs []renamePair) error {
	tmp := make([]string, len(pairs))
	for i, p := range pairs {
		tmp[i] = p.from + ".duoren" + strconv.Itoa(i)
		if err := os.Rename(p.from, tmp[i]); err != nil {
			for k := i - 1; k >= 0; k-- {
				os.Rename(tmp[k], pairs[k].from)
			}
			return fmt.Errorf("%s: %v", filepath.Base(p.from), errText(err))
		}
	}
	for i, p := range pairs {
		if err := os.Rename(tmp[i], p.to); err != nil {
			for k := i - 1; k >= 0; k-- {
				os.Rename(pairs[k].to, pairs[k].from)
			}
			for k := i; k < len(pairs); k++ {
				os.Rename(tmp[k], pairs[k].from)
			}
			return fmt.Errorf("%s: %v", filepath.Base(p.to), errText(err))
		}
	}
	return nil
}
