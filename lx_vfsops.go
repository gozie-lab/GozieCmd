//go:build linux

package main

// Archívum- és FTP-műveletek Linuxon (a mag közös a Windows-változattal).

import (
	"compress/flate"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// vfsTransfer: archívum / FTP és helyi mappa (vagy két virtuális hely) között.
// vfsTransfer: másolás / mozgatás, ha valamelyik oldal archívum vagy távoli hely.
// target: a célmappa (húzásnál a kiemelt mappa); üres: a célpanel mappája.
func (a *App) vfsTransfer(src, dst *Panel, items []*Entry, move bool, target ...string) {
	dstDir := dst.path
	if dst.vfs != nil {
		dstDir = dst.vpath
	}
	if len(target) > 0 && target[0] != "" {
		dstDir = target[0]
	}
	title := T("Másolás")
	if move {
		title = T("Mozgatás")
	}
	if dst.vfs == nil && src.vfs != nil { // kicsomagolás / letöltés
		a.runJob(title, func(j *job) error {
			if err := exportItems(src.vfs, items, dstDir, j); err != nil {
				return err
			}
			if move && canDelete(src.vfs) && j.Clean() {
				return deleteItems(src.vfs, items, j)
			}
			return nil
		}, func(err error) {
			if err == nil {
				src.marked = map[string]bool{}
			}
			src.refresh()
			dst.refresh()
		})
		return
	}
	if src.vfs == nil && dst.vfs != nil { // tömörítés / feltöltés
		paths := entryPaths(items)
		a.runJob(title, func(j *job) error {
			if err := importPaths(dst.vfs, paths, dstDir, j); err != nil {
				return err
			}
			if move && j.Clean() { // hiba vagy kihagyás esetén a forrás megmarad
				removeLocal(paths, j)
			}
			return nil
		}, func(err error) {
			if err == nil {
				src.marked = map[string]bool{}
			}
			src.refresh()
			dst.refresh()
		})
		return
	}
	// két virtuális hely között: ideiglenes mappán át
	a.runJob(title, func(j *job) error {
		tmp, err := tempViewDir()
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if err := exportItems(src.vfs, items, tmp, j); err != nil {
			return err
		}
		var paths []string
		for _, e := range items {
			paths = append(paths, filepath.Join(tmp, e.Name))
		}
		if err := importPaths(dst.vfs, paths, dstDir, j); err != nil {
			return err
		}
		if move && canDelete(src.vfs) && j.Clean() {
			return deleteItems(src.vfs, items, j)
		}
		return nil
	}, func(err error) {
		if err == nil {
			src.marked = map[string]bool{}
		}
		src.refresh()
		dst.refresh()
	})
}

// ── becsomagolás / kicsomagolás ──

func (a *App) pack() {
	p := a.active
	if p.vfs != nil {
		a.warn(T("Archívumon vagy FTP-n belül nem lehet becsomagolni. Előbb másold a fájlokat egy helyi mappába."))
		return
	}
	items := p.selectedEntries()
	if len(items) == 0 {
		return
	}
	name := filepath.Base(p.path)
	if len(items) == 1 {
		name = items[0].Stem()
		if items[0].IsDir {
			name = items[0].Name
		}
	}
	if name == "" || name == "/" {
		name = "archivum"
	}
	fmtIdx := 0
	saved := a.setting("packFormat", ".zip")
	var labels []string
	for i, f := range packFormats {
		labels = append(labels, f.label)
		if f.ext == saved {
			fmtIdx = i
		}
	}
	target := widget.NewEntry()
	target.SetText(filepath.Join(p.path, name) + packFormats[fmtIdx].ext)
	levels := []string{T("Tárolás (0)"), T("Gyors (1)"), T("Normál (6)"), T("Legjobb (9)")}
	levelVals := []int{flate.NoCompression, flate.BestSpeed, flate.DefaultCompression, flate.BestCompression}
	lvl := widget.NewSelect(levels, nil)
	lvl.SetSelectedIndex(a.settingInt("packLevel", 2))
	pw1, pw2 := widget.NewPasswordEntry(), widget.NewPasswordEntry()
	pw1.SetPlaceHolder(T("üresen hagyva nincs jelszó"))
	names := widget.NewCheck(T("A fájlnevek is legyenek titkosak (csak 7Z)"), nil)
	names.SetChecked(true)
	remember := widget.NewCheck(T("Jelszó megjegyzése a program bezárásáig (megnyitáskor nem kérdez rá)"), nil)
	pwFor := func(ext string) bool { return ext == ".zip" || ext == ".7z" }
	setPwState := func(ext string) {
		if pwFor(ext) {
			pw1.Enable()
			pw2.Enable()
			remember.Enable()
		} else {
			pw1.Disable()
			pw2.Disable()
			remember.Disable()
		}
		if ext == ".7z" {
			names.Enable()
		} else {
			names.Disable()
		}
	}
	format := widget.NewSelect(labels, func(s string) {
		for i, f := range packFormats {
			if f.label == s {
				old := target.Text
				target.SetText(stripArchiveExt(old) + f.ext)
				fmtIdx = i
				if f.ext == ".zip" {
					lvl.Enable()
				} else {
					lvl.Disable()
				}
				setPwState(f.ext)
			}
		}
	})
	format.SetSelectedIndex(fmtIdx)
	setPwState(packFormats[fmtIdx].ext)
	form := widget.NewForm(
		widget.NewFormItem(T("Archívum"), target),
		widget.NewFormItem(T("Formátum"), format),
		widget.NewFormItem(T("Tömörítés (ZIP)"), lvl),
		widget.NewFormItem(T("Jelszó (ZIP, 7Z)"), pw1),
		widget.NewFormItem(T("Jelszó újra"), pw2),
		widget.NewFormItem("", names),
		widget.NewFormItem("", remember),
	)
	pwNote := widget.NewLabel(T("A jelszót a program nem menti – ha elfelejted, a tartalom nem állítható vissza."))
	pwNote.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(form, pwNote)
	d := dialog.NewCustomConfirm(fmt.Sprintf(T("Becsomagolás – %s"), describe(items)), T("Becsomagolás"), T("Mégse"), content, func(ok bool) {
		a.active.focus()
		if !ok {
			return
		}
		t := strings.TrimSpace(target.Text)
		if t == "" {
			return
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(p.path, t)
		}
		a.settings.Put("packFormat", packFormats[fmtIdx].ext)
		a.settings.Put("packLevel", strconv.Itoa(lvl.SelectedIndex()))
		ext := packFormats[fmtIdx].ext
		pw := ""
		if pwFor(ext) && pw1.Text != "" {
			if pw1.Text != pw2.Text {
				a.warn(T("A két jelszó nem egyezik."))
				return
			}
			pw = pw1.Text
		}
		keep := remember.Checked
		after := func(err error) {
			if pw != "" && !keep { // a jelszót csak a becsomagolás idejére tartottuk meg
				forgetArchivePassword(t)
			}
			p.refresh()
			a.other(p).refresh()
			if err == nil && filepath.Dir(t) == p.path {
				p.selectName(filepath.Base(t))
			}
		}
		if pw != "" && strings.HasSuffix(strings.ToLower(t), ".zip") { // titkosított ZIP (AES-256)
			setArchivePassword(t, pw)
			a.runJob(T("Becsomagolás"), func(j *job) error { return packZipEncrypted(entryPaths(items), t, pw, j) }, after)
			return
		}
		encNames := names.Checked
		if pw != "" { // a kész archívumot (pl. a fájlnevek titkosítása után) ezzel olvassuk vissza
			setArchivePassword(t, pw)
		}
		z, err := openArchive(t)
		if err != nil {
			a.warn(fmt.Sprintf(T("Az archívum nem hozható létre:\n%s"), errText(err)))
			return
		}
		if !z.Writable() {
			a.warn(fmt.Sprintf(T("%s: ebbe a formátumba nem lehet írni.%s"), z.Info(), sevenZipHint(z)))
			return
		}
		level := levelVals[lvl.SelectedIndex()]
		a.runJob(T("Becsomagolás"), func(j *job) error {
			old := zipLevel
			zipLevel = level
			packPassword, packEncryptNames = pw, encNames
			defer func() { zipLevel = old; packPassword, packEncryptNames = "", false }()
			return z.AddLocal(entryPaths(items), "", j)
		}, after)
	}, a.win)
	d.Resize(fyne.NewSize(640, 0))
	d.Show()
	a.win.Canvas().Focus(target)
}

func (a *App) unpack() {
	p := a.active
	if p.vfs != nil {
		a.warn(T("Archívumon belül a fájlokat F5-tel másolhatod ki."))
		return
	}
	var arcs []*Entry
	for _, e := range p.selectedEntries() {
		if !e.IsDir && isArchive(e.Name) {
			arcs = append(arcs, e)
		}
	}
	if len(arcs) == 0 {
		a.warn(T("Jelölj ki egy vagy több archívumot a kicsomagoláshoz."))
		return
	}
	o := a.other()
	def := o.path
	if o.vfs != nil {
		def = p.path
	}
	if len(arcs) == 1 {
		def = filepath.Join(def, filepath.Base(stripArchiveExt(arcs[0].Path)))
		if def == filepath.Join(o.path, arcs[0].Name) {
			def = filepath.Join(o.path, arcs[0].Stem())
		}
	}
	a.inputBox(T("Kicsomagolás"), fmt.Sprintf(T("%s kicsomagolása ebbe a mappába:"), describe(arcs)), def, func(t string) {
		t = strings.TrimSpace(t)
		if t == "" {
			return
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(p.path, t)
		}
		a.runJob(T("Kicsomagolás"), func(j *job) error {
			if err := os.MkdirAll(t, 0o755); err != nil {
				return err
			}
			for _, ae := range arcs {
				z, err := openArchiveWorker(ae.Path)
				if err != nil {
					j.Err(ae.Path, err)
					continue
				}
				root, _ := z.List("")
				if err := z.Extract(root, t, j); err != nil {
					return err
				}
			}
			return nil
		}, func(error) { a.left.refresh(); a.right.refresh() })
	})
}

// ── FTP ──

func (a *App) loadSites() []ftpSite {
	v, _ := a.settings.Get("ftpSites")
	var sites []ftpSite
	if v != "" {
		json.Unmarshal([]byte(v), &sites)
	}
	return sites
}

func (a *App) saveSites(sites []ftpSite) {
	b, _ := json.Marshal(sites) // egy sorban marad, az ini-fájlba így írható
	a.settings.Put("ftpSites", string(b))
	a.settings.Save()
}

// ftpConnect: a mentett kapcsolatok listája (jelszót a program nem ment).
func (a *App) ftpConnect() {
	sites := a.loadSites()
	w := a.fa.NewWindow(T("Távoli kapcsolatok (FTP, SFTP, WebDAV, SMB)"))
	w.SetIcon(appIconRes)
	sel := -1
	list := widget.NewList(func() int { return len(sites) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) { o.(*widget.Label).SetText(siteLabel(sites[i])) })
	list.OnSelected = func(i widget.ListItemID) { sel = i }
	connect := func() {
		if sel < 0 || sel >= len(sites) {
			return
		}
		s := sites[sel]
		w.Close()
		a.connectSite(s)
	}
	edit := func(i int) {
		s := ftpSite{Proto: "ftp"}
		if i >= 0 {
			s = sites[i]
		}
		a.siteDialog(w, s, func(ns ftpSite) {
			if i >= 0 {
				sites[i] = ns
			} else {
				sites = append(sites, ns)
			}
			a.saveSites(sites)
			list.Refresh()
		})
	}
	buttons := container.NewVBox(
		widget.NewButton(T("Kapcsolódás"), connect),
		widget.NewButton(T("Új…"), func() { edit(-1) }),
		widget.NewButton(T("Szerkesztés…"), func() {
			if sel >= 0 {
				edit(sel)
			}
		}),
		widget.NewButton(T("Másolat"), func() {
			if sel >= 0 {
				c := sites[sel]
				c.Name += T(" (másolat)")
				sites = append(sites, c)
				a.saveSites(sites)
				list.Refresh()
			}
		}),
		widget.NewButton(T("Törlés"), func() {
			if sel >= 0 && sel < len(sites) {
				sites = append(sites[:sel], sites[sel+1:]...)
				sel = -1
				list.UnselectAll()
				a.saveSites(sites)
				list.Refresh()
			}
		}),
		widget.NewSeparator(),
		widget.NewButton(T("Gyors kapcsolódás…"), func() { w.Close(); a.quickConnect() }),
		widget.NewButton(T("Bezárás"), w.Close),
	)
	note := widget.NewLabel(T("A jelszót a program nem menti: kapcsolódáskor kérdezi meg."))
	w.SetContent(container.NewBorder(nil, note, nil, buttons, list))
	w.Resize(fyne.NewSize(620, 400))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		switch ev.Name {
		case fyne.KeyEscape:
			w.Close()
		case fyne.KeyReturn, fyne.KeyEnter:
			connect()
		}
	})
	list.OnSelected = func(i widget.ListItemID) { sel = i }
	w.Show()
}

func (a *App) passwordBox(who string, cb func(string)) {
	pw := widget.NewPasswordEntry()
	var d dialog.Dialog
	d = dialog.NewCustomConfirm(T("Jelszó"), T("Kapcsolódás"), T("Mégse"),
		container.NewVBox(widget.NewLabel(fmt.Sprintf(T("Jelszó ehhez: %s"), who)), pw), func(ok bool) {
			a.active.focus()
			if ok {
				cb(pw.Text)
			}
		}, a.win)
	pw.OnSubmitted = func(string) { d.Hide(); a.active.focus(); cb(pw.Text) }
	d.Resize(fyne.NewSize(420, 0))
	d.Show()
	a.win.Canvas().Focus(pw)
}
