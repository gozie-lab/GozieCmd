package main

import (
	"strings"
	"testing"
)

func TestKeysAndColors(t *testing.T) {
	for _, s := range []string{"Ctrl+Shift+F7", "Alt+F7", "F2", "Ctrl+1", "Ctrl+PageUp", "Shift+Delete"} {
		k, ok := parseCombo(s)
		if !ok || k.String() != s {
			t.Fatalf("%s → %v %v", s, k, ok)
		}
	}
	if k, _ := parseCombo("shift+ctrl+f7"); k.String() != "Ctrl+Shift+F7" {
		t.Fatal(k.String())
	}
	if _, ok := parseCombo("Ctrl+Valami"); ok {
		t.Fatal("hibás billentyűt elfogadott")
	}
	for s, want := range map[string]bool{"A": false, "Shift+A": false, "Alt+A": false, "Ctrl+A": true, "F5": true, "Shift+F5": true, "Insert": true, "Enter": false, "Ctrl+Enter": true} {
		k, _ := parseCombo(s)
		if u, _ := k.usable(); u != want {
			t.Fatalf("usable(%s) = %v", s, u)
		}
	}
	b := decodeKeys(encodeKeys([]keyBinding{{"Ctrl+F7", "search"}, {"F2", "-"}}))
	if len(b) != 2 || b[1].Cmd != "-" {
		t.Fatal(b)
	}
	tc := newTypeColors(true, defaultTypeRules)
	if c, ok := tc.colorFor("Kép.JPG", false, false); !ok || c != 0x1F64C8 {
		t.Fatal(c, ok)
	}
	if _, ok := tc.colorFor("kepek.jpg", true, false); ok {
		t.Fatal("mappát is színezett")
	}
	if _, ok := tc.colorFor("level.xyz", false, false); ok {
		t.Fatal("ismeretlen típus színes")
	}
	if c, _ := tc.colorFor("a.zip", false, true); c == 0xA0522D {
		t.Fatal("sötétben nem világosított")
	}
	if defaultKeyFor("search") != "Alt+F7" {
		t.Fatal(defaultKeyFor("search"))
	}
	r := decodeTypeRules(encodeTypeRules(defaultTypeRules[:1]))
	if len(r) != 3 || r[0].Name != "Képek" {
		t.Fatal(r)
	}
}

func TestTypeMigration(t *testing.T) {
	old := encodeTypeRules(defaultTypeRules[:4])
	r := decodeTypeRules(old)
	if len(r) != 6 {
		t.Fatal(len(r))
	}
	tc := newTypeColors(true, r)
	if c, ok := tc.colorFor("Számla.DOCX", false, false); !ok || c != 0x00838F {
		t.Fatal(c, ok)
	}
	if c, ok := tc.colorFor("jegyzet.md", false, false); !ok || c != 0xB07A00 {
		t.Fatal(c, ok)
	}
	// ha a felhasználó már felvette a saját szabályában, nem duplázza
	own := append(defaultTypeRules[:4:4], typeRule{Name: "Saját", Masks: "*.txt;*.doc", Color: 1})
	if n := len(decodeTypeRules(encodeTypeRules(own))); n != 5 {
		t.Fatal(n)
	}
}

func TestTypeMasksUpgrade(t *testing.T) {
	old := `[{"name":"Zene és videó","masks":"*.mp3;*.flac;*.ogg;*.opus;*.wav;*.m4a;*.aac;*.mp4;*.mkv;*.avi;*.mov;*.webm;*.wmv","color":1},` +
		`{"name":"Saját","masks":"*.mp3","color":2}]`
	r := decodeTypeRules(old)
	tc := newTypeColors(true, r)
	if c, ok := tc.colorFor("film.MPG", false, false); !ok || c != 1 {
		t.Fatal("az .mpg nem került be a frissített listába", c, ok)
	}
	if r[1].Masks != "*.mp3" {
		t.Fatal("a saját szabályt is átírta")
	}
	// minden kiterjesztés csak egy alapértelmezett szabályban szerepel
	seen := map[string]string{}
	for _, d := range defaultTypeRules {
		for _, m := range strings.Split(d.Masks, ";") {
			if prev, ok := seen[m]; ok {
				t.Errorf("%s kétszer: %s, %s", m, prev, d.Name)
			}
			seen[m] = d.Name
		}
	}
}
