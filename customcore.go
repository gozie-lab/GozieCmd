package main

// Testreszabás közös magja: billentyű-hozzárendelések, fájltípus-színezés.

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ── billentyűk ──
// Egy billentyűkombináció szöveges alakja: „Ctrl+Shift+F7”, „Alt+X”, „F2”.

var keyNames = func() []string {
	var k []string
	for c := 'A'; c <= 'Z'; c++ {
		k = append(k, string(c))
	}
	for c := '0'; c <= '9'; c++ {
		k = append(k, string(c))
	}
	for i := 1; i <= 12; i++ {
		k = append(k, "F"+strconv.Itoa(i))
	}
	return append(k, "Insert", "Delete", "Home", "End", "PageUp", "PageDown", "Up", "Down", "Left", "Right",
		"Space", "Enter", "Backspace", "Tab")
}()

type keyCombo struct {
	Ctrl, Alt, Shift bool
	Key              string
}

func (k keyCombo) String() string {
	s := ""
	if k.Ctrl {
		s += "Ctrl+"
	}
	if k.Alt {
		s += "Alt+"
	}
	if k.Shift {
		s += "Shift+"
	}
	return s + k.Key
}

func parseCombo(s string) (keyCombo, bool) {
	var k keyCombo
	parts := strings.Split(strings.TrimSpace(s), "+")
	if len(parts) == 0 {
		return k, false
	}
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "ctrl":
			k.Ctrl = true
		case "alt":
			k.Alt = true
		case "shift":
			k.Shift = true
		default:
			return k, false
		}
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	for _, n := range keyNames {
		if strings.EqualFold(n, last) {
			k.Key = n
			return k, true
		}
	}
	return k, false
}

// usable: a sima betű-, szám- és szerkesztőbillentyűk módosító nélkül nem
// foglalhatók le (a gépelést és a lista kezelését nem szabad elvenni).
func (k keyCombo) usable() (bool, string) {
	if k.Key == "" {
		return false, T("Nyomj le egy billentyűt.")
	}
	if k.Ctrl || k.Alt {
		if k.Alt && !k.Ctrl && len(k.Key) == 1 && k.Key[0] >= 'A' && k.Key[0] <= 'Z' {
			return false, T("Az Alt+betű a menük megnyitására foglalt.")
		}
		return true, ""
	}
	if strings.HasPrefix(k.Key, "F") && len(k.Key) > 1 {
		return true, ""
	}
	switch k.Key {
	case "Insert", "Delete":
		return true, ""
	}
	return false, T("Ehhez a billentyűhöz Ctrl vagy Alt is kell (különben a gépelést vagy a lista kezelését venné el).")
}

// keyBinding: egy saját hozzárendelés. Cmd "-": a billentyű ki van kapcsolva.
type keyBinding struct {
	Key string
	Cmd string
}

func decodeKeys(s string) []keyBinding {
	var out []keyBinding
	for _, part := range strings.Split(s, ";") {
		k, c, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if kc, ok := parseCombo(k); ok && c != "" {
			out = append(out, keyBinding{Key: kc.String(), Cmd: c})
		}
	}
	return out
}

func encodeKeys(b []keyBinding) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = x.Key + "=" + x.Cmd
	}
	return strings.Join(parts, ";")
}

// defaultKeys: a beépített billentyűk (mindkét rendszeren azonosak), a súgóhoz
// és az ütközések jelzéséhez.
var defaultKeys = map[string]string{
	"F3": "view", "F4": "edit", "F5": "copy", "F6": "move", "F7": "mkdir", "F8": "delete", "F9": "terminal",
	"Shift+F4": "newfile", "Shift+F6": "rename", "Shift+F8": "permdelete", "Ctrl+M": "multirename",
	"Alt+F7": "search", "Alt+F5": "pack", "Alt+F9": "unpack", "Ctrl+F": "ftp", "Ctrl+Shift+F": "disconnect",
	"Ctrl+Q": "quickview", "Ctrl+J": "queue", "Ctrl+D": "hotlist", "Ctrl+T": "newtab", "Ctrl+W": "closetab",
	"Ctrl+S": "quicksearch", "Ctrl+H": "hidden", "Ctrl+U": "swap", "Ctrl+A": "selectall", "Ctrl+L": "gotopath", "Ctrl+E": "samedir",
	"Ctrl+K": "cmdline", "Ctrl+Shift+C": "copypaths", "Alt+Down": "history",
}

func defaultKeyFor(cmd string) string {
	var ks []string
	for k, c := range defaultKeys {
		if c == cmd {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

// ── fájltípusonkénti színezés ──

type typeRule struct {
	Name  string `json:"name"`
	Masks string `json:"masks"`
	Color uint32 `json:"color"` // 0xRRGGBB (világos témához; sötétben a program világosít rajta)
}

var defaultTypeRules = []typeRule{
	{T("Képek"), imageMasks, 0x1F64C8},
	{T("Archívumok"), archiveMasks, 0xA0522D},
	{T("Programok, szkriptek"), programMasks, 0x1E8C3C},
	{T("Zene és videó"), mediaMasks, 0xA0308C},
	{T("Dokumentumok"), docMasks, 0x00838F},
	{T("Jegyzetek, szövegek"), noteMasks, 0xB07A00},
}

const (
	imageMasks = "*.jpg;*.jpeg;*.jpe;*.jfif;*.png;*.apng;*.gif;*.bmp;*.dib;*.webp;*.avif;*.heic;*.heif;*.jxl;" +
		"*.tif;*.tiff;*.svg;*.svgz;*.ico;*.icns;*.cur;*.psd;*.xcf;*.kra;*.ora;*.tga;*.pcx;*.ppm;*.pgm;*.pbm;*.exr;*.hdr;" +
		"*.raw;*.dng;*.cr2;*.cr3;*.crw;*.nef;*.nrw;*.arw;*.srf;*.sr2;*.orf;*.rw2;*.raf;*.pef;*.srw;*.3fr;*.erf;*.kdc;*.mrw;*.x3f;" +
		"*.eps;*.ai;*.cdr;*.emf;*.wmf"
	archiveMasks = "*.zip;*.zipx;*.7z;*.rar;*.tar;*.gz;*.tgz;*.xz;*.txz;*.bz2;*.tbz;*.tbz2;*.zst;*.tzst;*.lz;*.lzma;*.lz4;*.lzo;" +
		"*.z;*.taz;*.cab;*.arj;*.lzh;*.lha;*.ace;*.uc2;*.cpio;*.rpm;*.deb;*.iso;*.img;*.dmg;*.vhd;*.vhdx;*.wim;*.squashfs;" +
		"*.001;*.r00;*.jar;*.war;*.apk;*.xpi;*.crx"
	programMasks = "*.exe;*.com;*.msi;*.msix;*.msixbundle;*.appx;*.appxbundle;*.bat;*.cmd;*.ps1;*.psm1;*.vbs;*.vbe;*.wsf;*.reg;*.scr;" +
		"*.sh;*.bash;*.zsh;*.fish;*.csh;*.run;*.appimage;*.flatpak;*.flatpakref;*.snap;*.desktop;*.py;*.pyw;*.pl;*.rb;*.lua;*.php;*.tcl"
	mediaMasks = "*.mp3;*.flac;*.ogg;*.oga;*.opus;*.wav;*.wave;*.aif;*.aiff;*.aifc;*.m4a;*.m4b;*.aac;*.ac3;*.dts;*.wma;*.ape;*.wv;" +
		"*.mka;*.mid;*.midi;*.amr;*.au;*.ra;*.mpc;*.dsf;*.dff;*.cue;*.m3u;*.m3u8;*.pls;" +
		"*.mp4;*.m4v;*.mkv;*.webm;*.avi;*.mov;*.qt;*.wmv;*.asf;*.flv;*.f4v;*.mpg;*.mpeg;*.mpe;*.m2v;*.m1v;*.vob;*.ts;*.mts;*.m2ts;" +
		"*.3gp;*.3g2;*.ogv;*.ogm;*.rm;*.rmvb;*.divx;*.xvid;*.dv;*.mxf;*.y4m;*.srt;*.ass;*.ssa;*.sub;*.vtt"
	docMasks = "*.doc;*.docx;*.docm;*.dot;*.dotx;*.dotm;*.odt;*.ott;*.fodt;*.rtf;*.wpd;*.wps;*.pages;" +
		"*.xls;*.xlsx;*.xlsm;*.xlsb;*.xlt;*.xltx;*.ods;*.ots;*.fods;*.csv;*.tsv;*.numbers;" +
		"*.ppt;*.pptx;*.pptm;*.pps;*.ppsx;*.pot;*.potx;*.odp;*.otp;*.key;" +
		"*.odg;*.odf;*.vsd;*.vsdx;*.pub;*.mpp;*.accdb;*.mdb;*.odb;" +
		"*.pdf;*.xps;*.oxps;*.djvu;*.djv;*.epub;*.mobi;*.azw;*.azw3;*.fb2;*.cbz;*.cbr;*.chm"
	noteMasks = "*.txt;*.text;*.md;*.markdown;*.mdown;*.rst;*.adoc;*.asciidoc;*.org;*.tex;*.bib;*.log;*.nfo;*.diz;*.1st;" +
		"*.note;*.notes;*.one;*.enex;*.ini;*.cfg;*.conf;*.json;*.jsonc;*.yaml;*.yml;*.toml;*.xml;*.html;*.htm;*.css"
)

// régebbi alapértelmezett mintalisták → az újak (a módosítatlan szabályok frissítéséhez)
var oldDefaultMasks = map[string]string{
	"*.jpg;*.jpeg;*.png;*.gif;*.bmp;*.webp;*.tif;*.tiff;*.heic;*.svg;*.ico;*.raw;*.cr2;*.nef":                                       imageMasks,
	"*.zip;*.7z;*.rar;*.tar;*.gz;*.tgz;*.xz;*.bz2;*.zst;*.cab;*.iso;*.001":                                                          archiveMasks,
	"*.exe;*.msi;*.bat;*.cmd;*.ps1;*.com;*.sh;*.appimage;*.run;*.flatpakref;*.deb;*.rpm":                                            programMasks,
	"*.mp3;*.flac;*.ogg;*.opus;*.wav;*.m4a;*.aac;*.mp4;*.mkv;*.avi;*.mov;*.webm;*.wmv":                                              mediaMasks,
	"*.doc;*.docx;*.docm;*.odt;*.rtf;*.pdf;*.xls;*.xlsx;*.xlsm;*.ods;*.csv;*.ppt;*.pptx;*.odp;*.pages;*.numbers;*.key;*.epub;*.xps": docMasks,
	"*.txt;*.md;*.markdown;*.log;*.nfo;*.org;*.rst;*.tex;*.note;*.one;*.enex":                                                       noteMasks,
}

// migrateTypeRules: a korábban elmentett szabálylistába felveszi az azóta bekerült
// alapértelmezett típusokat (dokumentumok, jegyzetek), ha még nincsenek benne.
func migrateTypeRules(r []typeRule) []typeRule {
	for i := range r {
		if nm, ok := oldDefaultMasks[r[i].Masks]; ok {
			r[i].Masks = nm
		}
	}
	has := func(mask string) bool {
		for _, x := range r {
			for _, m := range strings.Split(strings.ToLower(x.Masks), ";") {
				if strings.TrimSpace(m) == mask {
					return true
				}
			}
		}
		return false
	}
	for _, d := range defaultTypeRules {
		first := strings.Split(d.Masks, ";")[0]
		if (d.Masks == docMasks || d.Masks == noteMasks) && !has(first) {
			r = append(r, d)
		}
	}
	return r
}

func decodeTypeRules(s string) []typeRule {
	if s == "" {
		return append([]typeRule(nil), defaultTypeRules...)
	}
	var r []typeRule
	if json.Unmarshal([]byte(s), &r) != nil {
		return append([]typeRule(nil), defaultTypeRules...)
	}
	return migrateTypeRules(r)
}

func encodeTypeRules(r []typeRule) string {
	b, _ := json.Marshal(r)
	return string(b)
}

// typeColors: a szabályok előkészítve a gyors kereséshez.
type typeColors struct {
	on    bool
	rules []typeRule
	masks [][]string
	cache map[string]int // kisbetűs név → szabály indexe (-1: nincs)
}

func newTypeColors(on bool, rules []typeRule) *typeColors {
	tc := &typeColors{on: on, rules: rules, cache: map[string]int{}}
	for _, r := range rules {
		var ms []string
		for _, m := range strings.FieldsFunc(r.Masks, func(c rune) bool { return c == ';' || c == ',' || c == ' ' }) {
			ms = append(ms, strings.ToLower(m))
		}
		tc.masks = append(tc.masks, ms)
	}
	return tc
}

// colorFor: a fájl színe (0xRRGGBB), ha valamelyik szabály illik rá. Mappák nem színeződnek.
func (tc *typeColors) colorFor(name string, isDir bool, dark bool) (uint32, bool) {
	if tc == nil || !tc.on || isDir {
		return 0, false
	}
	l := strings.ToLower(name)
	i, ok := tc.cache[l]
	if !ok {
		i = -1
	outer:
		for ri, ms := range tc.masks {
			for _, m := range ms {
				if hit, _ := filepath.Match(m, l); hit {
					i = ri
					break outer
				}
			}
		}
		if len(tc.cache) > 20000 {
			tc.cache = map[string]int{}
		}
		tc.cache[l] = i
	}
	if i < 0 {
		return 0, false
	}
	c := tc.rules[i].Color
	if dark {
		c = lightenForDark(c)
	}
	return c, true
}

// lightenForDark: sötét háttéren a sötét színeket világosabbra keveri.
func lightenForDark(c uint32) uint32 {
	r, g, b := float64(c>>16&0xFF), float64(c>>8&0xFF), float64(c&0xFF)
	lum := (0.299*r + 0.587*g + 0.114*b) / 255
	if lum >= 0.55 {
		return c
	}
	t := 0.55 - lum + 0.15
	if t > 0.7 {
		t = 0.7
	}
	mix := func(v float64) uint32 { return uint32(v + (255-v)*t) }
	return mix(r)<<16 | mix(g)<<8 | mix(b)
}
