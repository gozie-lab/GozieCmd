package main

import "strings"

// weightFromName: a Windows a betűcsalád nevébe írja a vastagságot
// (pl. „Segoe UI Semibold”); a betűválasztó csak a pontos vastagsággal találja meg.
func weightFromName(family string) int {
	f := strings.ToLower(family)
	for _, w := range []struct {
		suffix string
		weight int
	}{
		{" semilight", 350}, {" extralight", 200}, {" ultralight", 200}, {" light", 300}, {" thin", 100},
		{" semibold", 600}, {" demibold", 600}, {" extrabold", 800}, {" ultrabold", 800},
		{" black", 900}, {" heavy", 900}, {" medium", 500},
	} {
		if strings.HasSuffix(f, w.suffix) {
			return w.weight
		}
	}
	return 0
}
