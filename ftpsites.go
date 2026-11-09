//go:build windows

package main

// Mentett FTP-kapcsolatok (jelszó nélkül) és kapcsolatkezelő ablak.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

func (a *App) loadSites() []ftpSite {
	v, _ := a.settings.Get("ftpSites")
	var sites []ftpSite
	if v != "" {
		json.Unmarshal([]byte(v), &sites)
	}
	sort.Slice(sites, func(i, j int) bool { return strings.ToLower(sites[i].Name) < strings.ToLower(sites[j].Name) })
	return sites
}

func (a *App) saveSites(sites []ftpSite) {
	b, _ := json.Marshal(sites) // egy sorban marad, az ini-fájlba így írható
	a.settings.Put("ftpSites", string(b))
	a.settings.Save()
}

// ftpConnect a kapcsolatkezelőt nyitja (Ctrl+F).
func (a *App) ftpConnect() {
	sites := a.loadSites()
	var dlg *walk.Dialog
	var lb *walk.ListBox
	var connectBtn, closeBtn *walk.PushButton
	labels := func() []string {
		out := make([]string, len(sites))
		for i, s := range sites {
			out[i] = siteLabel(s)
		}
		return out
	}
	refresh := func(sel int) {
		lb.SetModel(labels())
		if len(sites) > 0 {
			if sel < 0 {
				sel = 0
			}
			if sel >= len(sites) {
				sel = len(sites) - 1
			}
			lb.SetCurrentIndex(sel)
		}
	}
	var chosen *ftpSite
	connect := func() {
		i := lb.CurrentIndex()
		if i < 0 || i >= len(sites) {
			return
		}
		s := sites[i]
		chosen = &s
		dlg.Accept()
	}
	err := Dialog{
		AssignTo: &dlg, Title: T("Távoli kapcsolatok (FTP, SFTP, WebDAV)"), DefaultButton: &connectBtn, CancelButton: &closeBtn,
		MinSize: Size{Width: 640, Height: 400}, Layout: HBox{},
		Children: []Widget{
			ListBox{AssignTo: &lb, ItemStyler: newListStyler(&lb), Model: labels(), OnItemActivated: connect, StretchFactor: 3},
			Composite{Layout: VBox{MarginsZero: true}, StretchFactor: 1, Children: []Widget{
				PushButton{AssignTo: &connectBtn, Text: T("Kapcsolódás"), OnClicked: connect},
				PushButton{Text: T("Új kapcsolat…"), OnClicked: func() {
					s := ftpSite{Proto: "ftp"}
					if siteDialog(dlg, &s) {
						sites = append(sites, s)
						a.saveSites(sites)
						sites = a.loadSites()
						for i := range sites {
							if sites[i].Name == s.Name {
								refresh(i)
							}
						}
					}
				}},
				PushButton{Text: T("Szerkesztés…"), OnClicked: func() {
					i := lb.CurrentIndex()
					if i < 0 || i >= len(sites) {
						return
					}
					if siteDialog(dlg, &sites[i]) {
						a.saveSites(sites)
						refresh(i)
					}
				}},
				PushButton{Text: T("Másolat"), OnClicked: func() {
					i := lb.CurrentIndex()
					if i < 0 || i >= len(sites) {
						return
					}
					c := sites[i]
					c.Name += T(" (másolat)")
					sites = append(sites, c)
					a.saveSites(sites)
					refresh(len(sites) - 1)
				}},
				PushButton{Text: T("Törlés"), OnClicked: func() {
					i := lb.CurrentIndex()
					if i < 0 || i >= len(sites) {
						return
					}
					if walk.MsgBox(dlg, appName, fmt.Sprintf(T("Törlöd ezt a kapcsolatot?\n%s"), sites[i].Name),
						walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != 6 {
						return
					}
					sites = append(sites[:i], sites[i+1:]...)
					a.saveSites(sites)
					refresh(i)
				}},
				VSpacer{},
				PushButton{Text: T("Gyors kapcsolódás…"), OnClicked: func() { dlg.Close(100) }},
				PushButton{AssignTo: &closeBtn, Text: T("Bezárás"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		return
	}
	themeDialog(dlg)
	if a.icon != nil {
		dlg.SetIcon(a.icon)
	}
	refresh(0)
	switch dlg.Run() {
	case 100:
		a.quickConnect()
		return
	case walk.DlgCmdOK:
	default:
		return
	}
	if chosen == nil {
		return
	}
	a.connectSite(*chosen)
}

func passwordBox(a *App, title, who string) (string, bool) {
	var dlg *walk.Dialog
	var le *walk.LineEdit
	var ok, cancel *walk.PushButton
	var result string
	err := Dialog{
		AssignTo: &dlg, Title: fmt.Sprintf(T("Jelszó – %s"), title), DefaultButton: &ok, CancelButton: &cancel,
		MinSize: Size{Width: 420, Height: 140}, Layout: VBox{},
		Children: []Widget{
			Label{Text: fmt.Sprintf(T("Jelszó ehhez: %s"), who)},
			LineEdit{AssignTo: &le, PasswordMode: true},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &ok, Text: T("Kapcsolódás"), OnClicked: func() { result = le.Text(); dlg.Accept() }},
				PushButton{AssignTo: &cancel, Text: T("Mégse"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		return "", false
	}
	themeDialog(dlg)
	dlg.Starting().Attach(func() { le.SetFocus() })
	res := dlg.Run() // előbb futtatni, utána olvasni a result-ot
	return result, res == walk.DlgCmdOK
}
