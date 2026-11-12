package main

// Többnyelvűség. A forrásban a szövegek magyarul állnak; a T() a kiválasztott
// nyelv táblájából adja a fordítást, ha van. A táblák JSON-fájlok:
//   {"_code": "en", "_name": "English", "Magyar szöveg": "English text", …}
// Az angol a programba van építve; további nyelvek a beállítások mappájában
// (vagy a program mellett) lévő lang/<kód>.json fájlokból töltődnek.
// Hiányzó fordításnál az angolra, végül a magyar eredetire esik vissza.
// A nyelv a program indulásakor dől el (a beállítás újraindítás után érvényes).

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

//go:embed lang/en.json
var langFS embed.FS

type langInfo struct {
	Code string
	Name string
	Path string // külső fájl esetén
}

// A táblák a csomag változóinak inicializálása előtt töltődnek be (a T erre
// hivatkozik), így a csomagszintű szövegtáblák is már fordítva készülnek.
var curLang, trTable, enTable = loadLanguage()

// T: a szöveg a kiválasztott nyelven.
func T(s string) string {
	if trTable != nil {
		if v, ok := trTable[s]; ok && v != "" {
			return v
		}
	}
	if enTable != nil {
		if v, ok := enTable[s]; ok && v != "" {
			return v
		}
	}
	return s
}

// userConfigDir: a felhasználó beállításmappája. Flatpakban is a gazdarendszer
// ~/.config-ja (nem a ~/.var/app/… alatti saját mappa), így a Flatpak és a
// kicsomagolt változat ugyanazokat a beállításokat használja.
func userConfigDir() (string, error) {
	if os.Getenv("FLATPAK_ID") != "" {
		if d := os.Getenv("HOST_XDG_CONFIG_HOME"); d != "" {
			return d, nil
		}
		home, err := os.UserHomeDir()
		return filepath.Join(home, ".config"), err
	}
	return os.UserConfigDir()
}

// earlyConfigDirs: ahol a beállítások lehetnek (hordozható mód: a program mellett).
func earlyConfigDirs() (portable string, normal string) {
	if exe, err := os.Executable(); err == nil {
		portable = filepath.Dir(exe)
	}
	if runtime.GOOS == "windows" {
		normal = filepath.Join(os.Getenv("APPDATA"), appDir, appDir)
	} else if d, err := userConfigDir(); err == nil {
		normal = filepath.Join(d, appDir)
	}
	return
}

// earlySetting egy kulcsot olvas a beállításfájlból, még a felület indulása előtt.
func earlySetting(key string) string {
	port, norm := earlyConfigDirs()
	var files []string
	if port != "" {
		files = append(files, filepath.Join(port, "GozieCmd.ini"))
	}
	if norm != "" {
		files = append(files, filepath.Join(norm, "settings.ini"))
	}
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		val := ""
		for sc.Scan() {
			k, v, ok := strings.Cut(strings.TrimRight(sc.Text(), "\r"), "=")
			if ok && strings.TrimSpace(k) == key {
				val = strings.TrimSpace(v)
			}
		}
		fh.Close()
		return val
	}
	return ""
}

func parseLang(data []byte) (map[string]string, string, string) {
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		return nil, "", ""
	}
	return m, m["_code"], m["_name"]
}

// languages: a választható nyelvek (magyar, beépített angol, külső fájlok).
func languages() []langInfo {
	out := []langInfo{{Code: "hu", Name: "Magyar"}, {Code: "en", Name: "English"}}
	seen := map[string]bool{"hu": true, "en": true}
	port, norm := earlyConfigDirs()
	for _, dir := range []string{norm, port} {
		if dir == "" {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(dir, "lang", "*.json"))
		sort.Strings(files)
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			_, code, name := parseLang(data)
			if code == "" {
				code = strings.TrimSuffix(filepath.Base(f), ".json")
			}
			if name == "" {
				name = code
			}
			if seen[code] {
				// külső fájl felülírhatja a beépített angolt is
				for i := range out {
					if out[i].Code == code {
						out[i].Path = f
					}
				}
				continue
			}
			seen[code] = true
			out = append(out, langInfo{Code: code, Name: name, Path: f})
		}
	}
	return out
}

func loadLanguage() (string, map[string]string, map[string]string) {
	code := earlySetting("lang")
	if code == "" || code == "hu" {
		return "hu", nil, nil
	}
	var en map[string]string
	if data, err := langFS.ReadFile("lang/en.json"); err == nil {
		en, _, _ = parseLang(data)
	}
	for _, l := range languages() {
		if l.Code == code && l.Path != "" {
			if data, err := os.ReadFile(l.Path); err == nil {
				if m, _, _ := parseLang(data); m != nil {
					return code, m, en
				}
			}
		}
	}
	if code == "en" {
		return code, nil, en
	}
	return "hu", nil, nil
}

// langTemplate: sablon új fordításhoz – minden magyar szöveg, mellette az angol.
func langTemplate() []byte {
	var en map[string]string
	if data, err := langFS.ReadFile("lang/en.json"); err == nil {
		en, _, _ = parseLang(data)
	}
	out := map[string]string{"_code": "xx", "_name": "Nyelv neve / Language name"}
	for k, v := range en {
		if !strings.HasPrefix(k, "_") {
			out[k] = v
		}
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return b
}

// langDir: ide kerülnek a külső nyelvi fájlok.
func langDir() string {
	_, norm := earlyConfigDirs()
	return filepath.Join(norm, "lang")
}

// helpT: a súgó bekezdésenként fordítódik (egy-egy bekezdés egy kulcs).
func helpT(text string) string {
	parts := strings.Split(text, "\n\n")
	for i, p := range parts {
		parts[i] = T(p)
	}
	return strings.Join(parts, "\n\n")
}

// langNames: a választható nyelvek neve és kódja a beállítások listájához.
func langNames() (names []string, codes []string, cur int) {
	for i, l := range languages() {
		names = append(names, l.Name)
		codes = append(codes, l.Code)
		if l.Code == curLang {
			cur = i
		}
	}
	return
}

// saveLangTemplate: sablon új fordításhoz a nyelvi mappába; a fájl útját adja vissza.
func saveLangTemplate() (string, error) {
	dir := langDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "template.json")
	return p, os.WriteFile(p, langTemplate(), 0o644)
}

// langTemplateHelp: rövid útmutató (mindkét nyelven, hogy bárki elolvashassa).
func langTemplateHelp(p string) string {
	return "Sablon mentve / Template saved:\n" + p + "\n\n" +
		"Új nyelv: másold le pl. de.json néven, írd át a „_code” és „_name” mezőt és a jobb oldali szövegeket, " +
		"majd indítsd újra a programot.\n\n" +
		"New language: copy it e.g. as de.json, set “_code” and “_name”, translate the values on the right, " +
		"then restart the program. Untranslated entries fall back to English."
}

// ── többes szám ──
// A fordításban a szám szerinti alakok „|” jellel választhatók el, pl.
//   "%d fájl": "%d file|%d files"
// Az alakok sorrendjét a nyelv "_plural" szabálya adja (ha nincs, a kód alapján):
//   one    – 1 | minden más                     (angol, német, holland, svéd, olasz, spanyol …)
//   fr     – 0 vagy 1 | minden más              (francia, portugál-brazil)
//   slavic – 1, 21, 31… | 2–4, 22–24… | többi   (orosz, ukrán, szerb, horvát)
//   pl     – 1 | 2–4, 22–24… | többi             (lengyel)
//   cs     – 1 | 2–4 | többi                     (cseh, szlovák)
//   none   – egyetlen alak                       (magyar, török, japán, kínai …)

var pluralRule = pluralRuleFor()

func pluralRuleFor() string {
	if trTable != nil && trTable["_plural"] != "" {
		return trTable["_plural"]
	}
	switch curLang {
	case "hu", "tr", "ja", "zh", "ko", "vi", "id":
		return "none"
	case "fr", "pt":
		return "fr"
	case "ru", "uk", "sr", "hr", "bs", "be":
		return "slavic"
	case "pl":
		return "pl"
	case "cs", "sk":
		return "cs"
	}
	return "one"
}

func pluralIndex(rule string, n int) int {
	if n < 0 {
		n = -n
	}
	switch rule {
	case "none":
		return 0
	case "fr":
		if n <= 1 {
			return 0
		}
		return 1
	case "slavic":
		switch {
		case n%10 == 1 && n%100 != 11:
			return 0
		case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
			return 1
		}
		return 2
	case "pl":
		switch {
		case n == 1:
			return 0
		case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
			return 1
		}
		return 2
	case "cs":
		switch {
		case n == 1:
			return 0
		case n >= 2 && n <= 4:
			return 1
		}
		return 2
	}
	if n == 1 {
		return 0
	}
	return 1
}

// Tn: a szöveg a darabszámnak megfelelő alakban (a %d-t a hívó tölti ki).
func Tn(n int, s string) string {
	forms := strings.Split(T(s), "|")
	i := pluralIndex(pluralRule, n)
	if i >= len(forms) {
		i = len(forms) - 1
	}
	return forms[i]
}

// Nf: „%d fájl” jellegű kifejezés kész szövegként (a szám beírva).
func Nf(n int, s string) string { return fmt.Sprintf(Tn(n, s), n) }
