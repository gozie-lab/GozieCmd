package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Minden fordításban ugyanazok a %-jelek kellenek, ugyanabban a sorrendben.
func TestLangFile(t *testing.T) {
	data, err := langFS.ReadFile("lang/en.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	verb := regexp.MustCompile(`%[-+ #0-9.]*[a-zA-Z%]`)
	for k, v := range m {
		if strings.HasPrefix(k, "_") {
			continue
		}
		for _, form := range strings.Split(v, "|") { // többes számú alakok
			if strings.Join(verb.FindAllString(k, -1), " ") != strings.Join(verb.FindAllString(form, -1), " ") {
				t.Errorf("eltérő %%-jelek: %q → %q", k, form)
			}
			if strings.HasSuffix(k, "\n") != strings.HasSuffix(form, "\n") {
				t.Errorf("eltérő sorvég: %q → %q", k, form)
			}
		}
	}
	if len(m) < 900 {
		t.Fatal("túl kevés fordítás:", len(m))
	}
	tpl := map[string]string{}
	if err := json.Unmarshal(langTemplate(), &tpl); err != nil || tpl["_code"] != "xx" {
		t.Fatal("sablon:", err)
	}
	if helpT("A\n\nB") != "A\n\nB" {
		t.Fatal("helpT")
	}
}

func TestPlural(t *testing.T) {
	cases := map[string][]int{ // szabály → alakindex 0, 1, 2, 5, 11, 21, 22, 25 esetén
		"one":    {1, 0, 1, 1, 1, 1, 1, 1},
		"fr":     {0, 0, 1, 1, 1, 1, 1, 1},
		"slavic": {2, 0, 1, 2, 2, 0, 1, 2},
		"pl":     {2, 0, 1, 2, 2, 2, 1, 2},
		"cs":     {2, 0, 1, 2, 2, 2, 2, 2},
		"none":   {0, 0, 0, 0, 0, 0, 0, 0},
	}
	ns := []int{0, 1, 2, 5, 11, 21, 22, 25}
	for rule, want := range cases {
		for i, n := range ns {
			if got := pluralIndex(rule, n); got != want[i] {
				t.Errorf("%s(%d) = %d, várt %d", rule, n, got, want[i])
			}
		}
	}
	// magyarul (nincs tábla) a kulcs változatlan
	if Nf(3, "%d fájl") != "3 fájl" {
		t.Fatal(Nf(3, "%d fájl"))
	}
}

func TestEnglishForms(t *testing.T) {
	data, _ := langFS.ReadFile("lang/en.json")
	en, _, _ := parseLang(data)
	oldTr, oldEn, oldRule := trTable, enTable, pluralRule
	trTable, enTable, pluralRule = nil, en, "one"
	defer func() { trTable, enTable, pluralRule = oldTr, oldEn, oldRule }()
	checks := map[string]string{
		Nf(1, "%d fájl"): "1 file",
		Nf(4, "%d fájl"): "4 files",
		Nf(1, "%d elem"): "1 item",
		fmt.Sprintf(T("%s másolása ide:"), "“a.txt”"): "Copy “a.txt” to:",
		fmt.Sprintf(T("%s / %s kijelölve,  %s,  %s"), "0 B", "2 KB",
			fmt.Sprintf(Tn(1, "%d / %d fájl"), 0, 1), fmt.Sprintf(Tn(3, "%d / %d mappa"), 1, 3)): "0 B / 2 KB selected,  0 / 1 file,  1 / 3 folders",
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("%q, várt %q", got, want)
		}
	}
}
