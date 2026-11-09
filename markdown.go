//go:build windows

package main

// Markdown (.md) fájlok formázott megjelenítése a Nézőkében.

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

func isMarkdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	}
	return false
}

const mdCSS = `
body { font-family: "Segoe UI", Arial, sans-serif; font-size: 15px; line-height: 1.55;
       color: #1f2328; margin: 24px 32px; max-width: 980px; }
h1, h2 { border-bottom: 1px solid #d8dee4; padding-bottom: .3em; }
h1 { font-size: 2em; } h2 { font-size: 1.5em; } h3 { font-size: 1.25em; }
code { font-family: Consolas, monospace; background: #eff1f3; padding: .15em .35em; border-radius: 4px; font-size: 90%; }
pre { background: #f6f8fa; padding: 12px 14px; border-radius: 6px; overflow: auto; }
pre code { background: none; padding: 0; font-size: 90%; }
blockquote { margin: 0; padding: 0 1em; color: #59636e; border-left: 4px solid #d1d9e0; }
table { border-collapse: collapse; margin: 12px 0; }
th, td { border: 1px solid #d1d9e0; padding: 6px 12px; }
th { background: #f6f8fa; }
img { max-width: 100%; }
a { color: #0969da; }
hr { border: 0; border-top: 1px solid #d8dee4; margin: 24px 0; }
`

const mdDarkCSS = `
body { background: #1e1e1e; color: #e6e6e6; }
h1, h2, hr { border-color: #444; }
code { background: #2d2d2d; } pre, th { background: #262626; }
blockquote { color: #a0a0a0; border-left-color: #555; }
th, td { border-color: #444; }
a { color: #6cb6ff; }
`

func darkCSS() string {
	if darkActive {
		return mdDarkCSS
	}
	return ""
}

// renderMarkdown HTML-oldalt készít; a nyers HTML-t a forrásból nem engedi át.
func renderMarkdown(src []byte, baseDir string) (string, error) {
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	var body bytes.Buffer
	if err := md.Convert(bytes.TrimPrefix(src, []byte{0xEF, 0xBB, 0xBF}), &body); err != nil {
		return "", err
	}
	base := "file:///" + strings.ReplaceAll(filepath.ToSlash(baseDir), " ", "%20") + "/"
	return `<!DOCTYPE html><html><head><meta charset="utf-8">` +
		`<meta http-equiv="X-UA-Compatible" content="IE=edge">` +
		`<base href="` + html.EscapeString(base) + `">` +
		`<style>` + mdCSS + darkCSS() + `</style></head><body>` + body.String() + `</body></html>`, nil
}

// showMarkdown formázott nézet, „Forrás” gombbal a nyers szövegre váltáshoz.
func showMarkdown(owner walk.Form, path string) bool {
	src, err := os.ReadFile(path)
	if err != nil || len(src) > viewLimit {
		return false
	}
	page, err := renderMarkdown(src, filepath.Dir(path))
	if err != nil {
		return false
	}
	dir, err := tempViewDir()
	if err != nil {
		return false
	}
	htmlPath := filepath.Join(dir, "nezoke.html")
	if err := os.WriteFile(htmlPath, []byte(page), 0o644); err != nil {
		return false
	}
	text, _, _ := loadText(path)

	var dlg *walk.Dialog
	var wv *walk.WebView
	var te *walk.TextEdit
	var toggle, closeBtn *walk.PushButton
	err = Dialog{
		AssignTo:      &dlg,
		Title:         fmt.Sprintf(T("Nézőke – %s"), path),
		DefaultButton: &closeBtn,
		CancelButton:  &closeBtn,
		MinSize:       Size{Width: 500, Height: 300},
		Size:          Size{Width: 1000, Height: 720},
		Layout:        VBox{},
		Children: []Widget{
			WebView{AssignTo: &wv, OnNavigating: func(ev *walk.WebViewNavigatingEventData) {
				// hivatkozásra kattintva a böngésző nyissa meg, ne a Nézőke
				u := ev.Url()
				if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "mailto:") {
					ev.SetCanceled(true)
					shellOpen(dlg.Handle(), u, "")
				}
			}},
			TextEdit{AssignTo: &te, ReadOnly: true, VScroll: true, HScroll: true, Visible: false,
				Font: Font{Family: "Consolas", PointSize: 10}},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: T("Markdown – formázott nézet")},
					HSpacer{},
					PushButton{AssignTo: &toggle, Text: T("Forrás"), OnClicked: func() {
						src := !te.Visible()
						te.SetVisible(src)
						wv.SetVisible(!src)
						if src {
							toggle.SetText(T("Formázott"))
							te.SetFocus()
						} else {
							toggle.SetText(T("Forrás"))
						}
					}},
					PushButton{AssignTo: &closeBtn, Text: T("Bezárás (Esc)"), OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		return false
	}
	themeDialog(dlg)
	theApp.sizeableDialog(dlg, owner, "viewer", 980, 680)
	win.SendMessage(te.Handle(), win.EM_SETLIMITTEXT, 0x7FFFFFFE, 0)
	te.SetText(text)
	te.KeyDown().Attach(func(key walk.Key) {
		if key == walk.KeyF3 || key == walk.KeyF10 {
			dlg.Cancel()
		}
	})
	wv.SetURL("file:///" + filepath.ToSlash(htmlPath))
	dlg.Run()
	return true
}
