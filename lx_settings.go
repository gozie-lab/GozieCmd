//go:build linux

package main

// Beállítások Linuxon: ugyanaz a kulcs=érték formátum, mint a Windows-változat
// settings.ini fájlja, így a két rendszer között átvihető (Beállítások → Kezelés).
// Helye: ~/.config/GozieCommander/settings.ini, hordozható módban a program
// melletti GozieCmd.ini.

import (
	"bufio"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const portableFile = "GozieCmd.ini"

type iniSettings struct {
	mu   sync.Mutex
	path string
	m    map[string]string
}

func newIniSettings(path string) *iniSettings {
	return &iniSettings{path: path, m: map[string]string{}}
}

func (s *iniSettings) FilePath() string { return s.path }

func (s *iniSettings) Get(k string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	return v, ok
}

func (s *iniSettings) Put(k, v string) {
	s.mu.Lock()
	s.m[k] = v
	s.mu.Unlock()
}

func (s *iniSettings) Load() error {
	return s.loadFrom(s.path)
}

func (s *iniSettings) loadFrom(p string) error {
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		i := strings.Index(line, "=")
		if i < 0 {
			if strings.TrimSpace(line) == "" {
				continue
			}
			return fmt.Errorf(T("hibás sor: %q"), line)
		}
		k := strings.TrimSpace(line[:i])
		if j := strings.Index(k, "|"); j >= 0 { // a Windows-változat időbélyege
			k = k[:j]
		}
		m[k] = strings.TrimSpace(line[i+1:])
	}
	if err := sc.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.m = m
	s.mu.Unlock()
	return nil
}

func (s *iniSettings) Save() error {
	return s.saveTo(s.path)
}

func (s *iniSettings) saveTo(p string) error {
	s.mu.Lock()
	keys := make([]string, 0, len(s.m))
	for k := range s.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + s.m[k] + "\r\n")
	}
	s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func configDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, appDir)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", appDir)
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe)
}

// openSettings: a program melletti GozieCmd.ini (hordozható mód), különben a ~/.config-beli.
func openSettings() (*iniSettings, bool) {
	pp := filepath.Join(exeDir(), portableFile)
	if _, err := os.Stat(pp); err == nil {
		return newIniSettings(pp), true
	}
	return newIniSettings(filepath.Join(configDir(), "settings.ini")), false
}

func (a *App) setting(k, def string) string {
	if v, ok := a.settings.Get(k); ok && v != "" {
		return v
	}
	return def
}

func (a *App) settingBool(k string, def bool) bool {
	v, ok := a.settings.Get(k)
	if !ok || v == "" {
		return def
	}
	return v == "1"
}

func (a *App) settingInt(k string, def int) int {
	if n, err := strconv.Atoi(a.setting(k, "")); err == nil {
		return n
	}
	return def
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// ── színek és betűk (a Windows-változattal azonos kulcsokon) ──

type fontSpec struct {
	Family       string
	Size         int
	Bold, Italic bool
	Color        color.NRGBA
	Weight       int // a Windows-betűválasztó pontos vastagsága; megőrizzük
}

func (f fontSpec) String() string {
	bold := boolStr(f.Bold)
	if f.Weight > 0 && f.Weight != 400 && f.Weight != 700 && f.Bold == (f.Weight >= 600) {
		bold = strconv.Itoa(f.Weight)
	}
	return f.Family + "|" + strconv.Itoa(f.Size) + "|" + bold + "|" + boolStr(f.Italic) + "|" + colorToHex(f.Color)
}

func parseFont(s string, def fontSpec) fontSpec {
	p := strings.Split(s, "|")
	if len(p) != 5 || p[0] == "" {
		return def
	}
	size, err := strconv.Atoi(p[1])
	if err != nil || size < 5 || size > 48 {
		return def
	}
	bold, weight := p[2] == "1", 0
	if n, err := strconv.Atoi(p[2]); err == nil && n >= 100 { // a Windows-betűválasztó pontos vastagsága (pl. 600)
		bold, weight = n >= 600, n
	} else if w := weightFromName(p[0]); w > 0 { // korábbi mentés: a vastagság a névből ("Segoe UI Semibold")
		bold, weight = w >= 600, w
	}
	return fontSpec{p[0], size, bold, p[3] == "1", hexToColor(p[4], def.Color), weight}
}

func rgb(r, g, b uint8) color.NRGBA { return color.NRGBA{r, g, b, 255} }

func colorToHex(c color.NRGBA) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

func hexToColor(s string, def color.NRGBA) color.NRGBA {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return def
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return def
	}
	return rgb(byte(v>>16), byte(v>>8), byte(v))
}

type look struct {
	ListFont, MarkFont  fontSpec
	ListBg, MarkBg      color.NRGBA
	CursorBg, CursorFg  color.NRGBA
	CursorInBg, CurInFg color.NRGBA
	HeadBg, HeadFg      color.NRGBA
	HeadInBg, HeadInFg  color.NRGBA
	Drop                color.NRGBA
}

// A Windowson mentett betűméret pontban van (9 pt ≈ 12 képpont).
var defaultLook = look{
	ListFont:   fontSpec{"Segoe UI", 9, false, false, rgb(0, 0, 0), 0},
	MarkFont:   fontSpec{"Segoe UI", 9, true, false, rgb(210, 0, 0), 0},
	ListBg:     rgb(255, 255, 255),
	MarkBg:     rgb(255, 255, 255),
	CursorBg:   rgb(0, 120, 215),
	CursorFg:   rgb(255, 255, 255),
	CursorInBg: rgb(217, 217, 217),
	CurInFg:    rgb(0, 0, 0),
	HeadBg:     rgb(0, 102, 204),
	HeadFg:     rgb(255, 255, 255),
	Drop:       rgb(255, 230, 160),
	HeadInBg:   rgb(218, 218, 218),
	HeadInFg:   rgb(40, 40, 40),
}

var defaultDarkLook = look{
	ListFont:   fontSpec{"Segoe UI", 9, false, false, rgb(220, 220, 220), 0},
	MarkFont:   fontSpec{"Segoe UI", 9, true, false, rgb(255, 110, 110), 0},
	ListBg:     rgb(25, 25, 25),
	MarkBg:     rgb(25, 25, 25),
	CursorBg:   rgb(0, 95, 170),
	CursorFg:   rgb(255, 255, 255),
	CursorInBg: rgb(60, 60, 60),
	CurInFg:    rgb(230, 230, 230),
	HeadBg:     rgb(0, 95, 184),
	HeadFg:     rgb(255, 255, 255),
	Drop:       rgb(110, 90, 30),
	HeadInBg:   rgb(50, 50, 50),
	HeadInFg:   rgb(200, 200, 200),
}

type colorKey struct {
	key   string
	label string
	ptr   func(l *look) *color.NRGBA
}

var colorKeys = []colorKey{
	{"colorListBg", T("Fájllista háttere"), func(l *look) *color.NRGBA { return &l.ListBg }},
	{"colorMarkBg", T("Jelölt fájlok háttere"), func(l *look) *color.NRGBA { return &l.MarkBg }},
	{"colorCursorBg", T("Kurzor háttere – aktív panel"), func(l *look) *color.NRGBA { return &l.CursorBg }},
	{"colorCursorFg", T("Kurzor szövege – aktív panel"), func(l *look) *color.NRGBA { return &l.CursorFg }},
	{"colorCursorInBg", T("Kurzor háttere – inaktív panel"), func(l *look) *color.NRGBA { return &l.CursorInBg }},
	{"colorCursorInFg", T("Kurzor szövege – inaktív panel"), func(l *look) *color.NRGBA { return &l.CurInFg }},
	{"colorActiveBg", T("Aktív panel fejléce"), func(l *look) *color.NRGBA { return &l.HeadBg }},
	{"colorActiveFg", T("Aktív fejléc szövege"), func(l *look) *color.NRGBA { return &l.HeadFg }},
	{"colorInactiveBg", T("Inaktív panel fejléce"), func(l *look) *color.NRGBA { return &l.HeadInBg }},
	{"colorInactiveFg", T("Inaktív fejléc szövege"), func(l *look) *color.NRGBA { return &l.HeadInFg }},
	{"colorDrop2", T("Célmappa húzáskor"), func(l *look) *color.NRGBA { return &l.Drop }},
}

func lookKey(k string, dark bool) string {
	if dark {
		return "dark_" + k
	}
	return k
}

func (a *App) lookFor(dark bool) look {
	l := defaultLook
	if dark {
		l = defaultDarkLook
	}
	get := func(k string) string { v, _ := a.settings.Get(lookKey(k, dark)); return v }
	l.ListFont = parseFont(get("fontList"), l.ListFont)
	l.MarkFont = parseFont(get("fontMark"), l.MarkFont)
	for _, ck := range colorKeys {
		p := ck.ptr(&l)
		*p = hexToColor(get(ck.key), *p)
	}
	return l
}

func (a *App) saveLook(l look, dark bool) {
	a.settings.Put(lookKey("fontList", dark), l.ListFont.String())
	a.settings.Put(lookKey("fontMark", dark), l.MarkFont.String())
	for _, ck := range colorKeys {
		a.settings.Put(lookKey(ck.key, dark), colorToHex(*ck.ptr(&l)))
	}
}

// ptToPx: a Windows-pont értéket Fyne-méretre váltja (96 DPI: 1 pt = 4/3 képpont).
func ptToPx(pt int) float32 { return float32(pt) * 4 / 3 }
