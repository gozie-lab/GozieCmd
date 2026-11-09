// i18nfix: a T("…") + változó + T("…") láncokat egyetlen fmt.Sprintf(T("…%s…"), …) hívássá alakítja.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type edit struct {
	start, end int
	text       string
}

func isT(e ast.Expr) (string, bool) {
	c, ok := e.(*ast.CallExpr)
	if !ok || len(c.Args) != 1 {
		return "", false
	}
	id, ok := c.Fun.(*ast.Ident)
	if !ok || id.Name != "T" {
		return "", false
	}
	b, ok := c.Args[0].(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(b.Value)
	return v, err == nil
}

func flatten(e ast.Expr, out *[]ast.Expr) {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}
		e = p.X
	}
	if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
		flatten(b.X, out)
		flatten(b.Y, out)
		return
	}
	*out = append(*out, e)
}

func main() {
	mode := os.Args[1]
	files, _ := filepath.Glob(filepath.Join(os.Args[2], "*.go"))
	total := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(f)
		fs := token.NewFileSet()
		af, err := parser.ParseFile(fs, f, src, parser.ParseComments)
		if err != nil {
			panic(err)
		}
		var edits []edit
		done := map[ast.Node]bool{}
		ast.Inspect(af, func(n ast.Node) bool {
			b, ok := n.(*ast.BinaryExpr)
			if !ok || b.Op != token.ADD || done[b] {
				return true
			}
			var ops []ast.Expr
			flatten(b, &ops)
			hasT := false
			for _, o := range ops {
				if _, ok := isT(o); ok {
					hasT = true
				}
			}
			if !hasT {
				return true
			}
			// a belső ADD csomópontokat ne dolgozzuk fel újra
			ast.Inspect(b, func(m ast.Node) bool {
				if x, ok := m.(*ast.BinaryExpr); ok && x.Op == token.ADD {
					done[x] = true
				}
				return true
			})
			var format strings.Builder
			var args []string
			for _, o := range ops {
				if v, ok := isT(o); ok {
					format.WriteString(strings.ReplaceAll(v, "%", "%%"))
					continue
				}
				if bl, ok := o.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					v, _ := strconv.Unquote(bl.Value)
					format.WriteString(strings.ReplaceAll(v, "%", "%%"))
					continue
				}
				format.WriteString("%s")
				args = append(args, string(src[fs.Position(o.Pos()).Offset:fs.Position(o.End()).Offset]))
			}
			var text string
			if len(args) == 0 {
				text = "T(" + strconv.Quote(strings.ReplaceAll(format.String(), "%%", "%")) + ")"
			} else {
				text = "fmt.Sprintf(T(" + strconv.Quote(format.String()) + "), " + strings.Join(args, ", ") + ")"
			}
			s, e := fs.Position(b.Pos()).Offset, fs.Position(b.End()).Offset
			if mode == "list" {
				fmt.Printf("%s:%d\n   %s\n=> %s\n", filepath.Base(f), fs.Position(b.Pos()).Line, strings.Join(strings.Fields(string(src[s:e])), " "), text)
			}
			edits = append(edits, edit{s, e, text})
			return false
		})
		total += len(edits)
		if mode == "fix" && len(edits) > 0 {
			sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
			for _, ed := range edits {
				src = append(src[:ed.start], append([]byte(ed.text), src[ed.end:]...)...)
			}
			if !bytes.Contains(src, []byte(`"fmt"`)) {
				src = bytes.Replace(src, []byte("import (\n"), []byte("import (\n\t\"fmt\"\n"), 1)
			}
			os.WriteFile(f, src, 0o644)
		}
	}
	fmt.Println("láncok:", total)
}
