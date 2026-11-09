// i18ntool: a fordítás karbantartója.
//
//	go run . list  <forrásmappa>             – a még be nem csomagolt magyar szövegek
//	go run . wrap  <forrásmappa>             – becsomagolja őket T(...)-be
//	go run . keys  <forrásmappa>             – minden fordítandó kulcs (JSON-szöveg soronként)
//	go run . check <forrásmappa> <nyelv.json> – a nyelvi fájlból hiányzó kulcsok
//
// A darabokból összerakott üzeneteket (T("…") + x + T("…")) a tools/i18nfix alakítja
// egész mondattá: fmt.Sprintf(T("…%s…"), x).
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

var uiFields = map[string]bool{"Text": true, "Title": true, "Label": true, "ToolTipText": true, "CueBanner": true, "Tip": true, "Name": false}
var uiFuncs = map[string]bool{"MsgBox": true, "NewLabel": true, "NewButton": true, "NewButtonWithIcon": true, "NewMenuItem": true, "NewMenu": true,
	"NewCheck": true, "NewFormItem": true, "SetText": true, "SetTitle": true, "warn": true, "info": true, "winInfo": true,
	"ShowInformation": true, "ShowConfirm": true, "NewCustomConfirm": true, "NewCustom": true, "NewWindow": true, "Status": true,
	"item": true, "inputBox": true, "inputBoxEx": true, "runJob": true, "runJobIn": true, "choice": true, "confirm": true,
	"SetPlaceHolder": true, "SetCueBanner": true, "SetToolTipText": true, "New": true, "Errorf": true, "toolWindow": true, "c": true, "NewCard": true, "NewRadioGroup": true}
var skipFuncs = map[string]bool{"dlog": true, "Get": true, "Put": true, "Getenv": true, "Printf": true, "Println": true, "panic": true,
	"HasPrefix": true, "HasSuffix": true, "Contains": true, "TrimPrefix": true, "TrimSuffix": true, "Index": true, "Split": true, "Cut": true,
	"EqualFold": true, "Fields": true, "Join": true, "ReplaceAll": true, "Replace": true, "NewProc": true, "Command": true, "LoadDLL": true,
	"NewLazyDLL": true, "NewLazySystemDLL": true, "Parse": true, "ParseInLocation": true, "Format": true, "get": true, "setting": true,
	"settingBool": true, "settingInt": true, "Match": true, "LookPath": true, "Setenv": true, "Sscanf": true}

func hungarian(s string) bool {
	for _, r := range s {
		if r > 127 && (unicode.IsLetter(r) || strings.ContainsRune("„”…–—«»", r)) {
			return true
		}
	}
	return false
}

func hasWord(s string) bool {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
			if n >= 2 {
				return true
			}
		} else {
			n = 0
		}
	}
	return false
}

type hit struct {
	file       string
	start, end int
	val        string
	note       string
}

func main() {
	mode := os.Args[1] // list | wrap
	dir := os.Args[2]
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	var all []hit
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, "i18n.go") || strings.HasSuffix(f, "version.go") {
			continue
		}
		fs := token.NewFileSet()
		af, err := parser.ParseFile(fs, f, nil, parser.ParseComments)
		if err != nil {
			panic(err)
		}
		skip := map[*ast.BasicLit]string{}
		ui := map[*ast.BasicLit]bool{}
		ast.Inspect(af, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.ImportSpec:
				skip[x.Path] = "import"
			case *ast.Field:
				if x.Tag != nil {
					skip[x.Tag] = "tag"
				}
			case *ast.GenDecl:
				if x.Tok == token.CONST {
					ast.Inspect(x, func(m ast.Node) bool {
						if b, ok := m.(*ast.BasicLit); ok {
							skip[b] = "const"
						}
						return true
					})
				}
			case *ast.CaseClause:
				for _, e := range x.List {
					if b, ok := e.(*ast.BasicLit); ok {
						skip[b] = "case"
					}
				}
			case *ast.BinaryExpr:
				if x.Op == token.EQL || x.Op == token.NEQ {
					for _, e := range []ast.Expr{x.X, x.Y} {
						if b, ok := e.(*ast.BasicLit); ok {
							skip[b] = "compare"
						}
					}
				}
			case *ast.CompositeLit:
				for _, e := range x.Elts {
					if b, ok := e.(*ast.BasicLit); ok {
						if v, err := strconv.Unquote(b.Value); err == nil && len(v) > 1 && v[0] >= 'A' && v[0] <= 'Z' &&
							!strings.ContainsAny(v, "./_\\:+") && strings.ToLower(v[1:]) == v[1:] {
							ui[b] = true
						}
					}
				}
			case *ast.KeyValueExpr:
				if b, ok := x.Key.(*ast.BasicLit); ok {
					skip[b] = "mapkey"
				}
				if id, ok := x.Key.(*ast.Ident); ok && uiFields[id.Name] {
					if b, ok := x.Value.(*ast.BasicLit); ok {
						ui[b] = true
					}
				}
			case *ast.CallExpr:
				name := ""
				switch fn := x.Fun.(type) {
				case *ast.Ident:
					name = fn.Name
				case *ast.SelectorExpr:
					name = fn.Sel.Name
				}
				if name == "T" {
					for _, a := range x.Args {
						ast.Inspect(a, func(m ast.Node) bool {
							if b, ok := m.(*ast.BasicLit); ok {
								skip[b] = "already"
							}
							return true
						})
					}
				}
				if skipFuncs[name] {
					for _, a := range x.Args {
						if b, ok := a.(*ast.BasicLit); ok {
							skip[b] = "func:" + name
						}
					}
				}
				if uiFuncs[name] {
					for _, a := range x.Args {
						ast.Inspect(a, func(m ast.Node) bool {
							if b, ok := m.(*ast.BasicLit); ok {
								ui[b] = true
							}
							return true
						})
					}
				}
			}
			return true
		})
		ast.Inspect(af, func(n ast.Node) bool {
			b, ok := n.(*ast.BasicLit)
			if !ok || b.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(b.Value)
			if err != nil {
				return true
			}
			want := hungarian(v) || (ui[b] && hasWord(v) && !strings.Contains(v, "://"))
			if !want {
				return true
			}
			note := skip[b]
			all = append(all, hit{f, fs.Position(b.Pos()).Offset, fs.Position(b.End()).Offset, v, note})
			return true
		})
	}
	switch mode {
	case "list":
		uniq := map[string]bool{}
		for _, h := range all {
			if h.note == "" {
				uniq[h.val] = true
			} else if h.note != "already" {
				fmt.Printf("SKIP %s %s:%d %q\n", h.note, filepath.Base(h.file), h.start, trunc(h.val))
			}
		}
		fmt.Println("wrap:", len(all), "unique:", len(uniq))
	case "keys":
		uniq := map[string]bool{}
		for _, h := range all {
			if h.note == "" || h.note == "already" {
				uniq[h.val] = true
			}
		}
		var ks []string
		for k := range uniq {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Println(strconv.Quote(k))
		}
	case "check":
		// a T("…"), Tn(n, "…"), Nf(n, "…") kulcsok közül melyik hiányzik a nyelvi fájlból
		data, err := os.ReadFile(os.Args[3])
		if err != nil {
			panic(err)
		}
		var tr map[string]string
		if err := json.Unmarshal(data, &tr); err != nil {
			panic(err)
		}
		re := regexp.MustCompile(`\b(?:T|Tn|Nf)\((?:[^,()]*(?:\([^()]*\))?[^,()]*,\s*)?("(?:[^"\\]|\\.)*")\)`)
		n := 0
		seen := map[string]bool{}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, _ := os.ReadFile(f)
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				k, err := strconv.Unquote(m[1])
				if err != nil || seen[k] || tr[k] != "" || !hasWord(k) {
					continue
				}
				seen[k] = true
				fmt.Printf("%s: %q\n", filepath.Base(f), k)
				n++
			}
		}
		fmt.Println("hiányzik / missing:", n)
	case "wrap":
		byFile := map[string][]hit{}
		for _, h := range all {
			if h.note == "" {
				byFile[h.file] = append(byFile[h.file], h)
			}
		}
		for f, hs := range byFile {
			src, _ := os.ReadFile(f)
			sort.Slice(hs, func(i, j int) bool { return hs[i].start > hs[j].start })
			for _, h := range hs {
				src = append(src[:h.start], append([]byte("T("+string(src[h.start:h.end])+")"), src[h.end:]...)...)
			}
			os.WriteFile(f, src, 0o644)
		}
		fmt.Println("wrapped files:", len(byFile))
	}
}

func trunc(s string) string {
	if len(s) > 60 {
		return s[:60]
	}
	return s
}
