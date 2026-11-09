//go:build windows

package main

// ZIP- és FTP-műveletek a felületről.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

func (a *App) notInVFS() bool {
	if a.active.vfs != nil {
		walk.MsgBox(a.mw, appName, T("Ez a művelet ZIP-archívumon vagy FTP-n belül nem érhető el.\nElőbb másold a fájlt egy helyi mappába (F5)."), walk.MsgBoxIconInformation)
		return true
	}
	return false
}

// openVirtual egy archívumbeli / FTP-s fájlt ideiglenes helyre tesz, és megnyitja vagy megmutatja.
func (a *App) openVirtual(p *Panel, e *Entry, view bool) {
	dir, err := tempViewDir()
	if err != nil {
		walk.MsgBox(a.mw, appName, errText(err), walk.MsgBoxIconWarning)
		return
	}
	title := T("Kicsomagolás")
	if isRemoteKind(p.kind()) {
		title = T("Letöltés")
	}
	err = runJob(a.mw, title, func(j *Job) error { return exportItems(p.vfs, []*Entry{e}, dir, j) })
	local := filepath.Join(dir, e.Name)
	if _, serr := os.Stat(local); err != nil || serr != nil {
		if err != nil {
			walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A fájl nem nyitható meg:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		}
		return
	}
	if view {
		switch strings.ToLower(e.Ext) {
		case "jpg", "jpeg", "png", "gif", "bmp", "webp", "tif", "tiff", "ico", "pdf", "mp4", "mp3", "heic":
		default:
			showViewer(a.mw, local)
			return
		}
	}
	shellOpen(a.mw.Handle(), local, dir)
}

func (a *App) vfsTransfer(src, dst *Panel, items []*Entry, move bool) {
	sk, dk := src.kind(), dst.kind()
	if sk == "net" || dk == "net" {
		walk.MsgBox(a.mw, appName, T("A hálózati listában előbb nyiss meg egy megosztást (Enter), azon belül már ugyanúgy másolhatsz, mint egy helyi mappában."), walk.MsgBoxIconInformation)
		return
	}
	if sk == "local" {
		a.localToVFS(src, entryPaths(items), dst, move)
		return
	}
	verb := T("%s másolása ide:")
	if move {
		verb = T("%s mozgatása ide:")
	}
	where := dst.path
	if dst.vfs != nil {
		where = dst.vfs.Display(dst.vpath)
	}
	if walk.MsgBox(a.mw, appName, fmt.Sprintf(verb, describe(items))+"\n"+where,
		walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != win.IDYES {
		return
	}
	if move && !canDelete(src.vfs) {
		walk.MsgBox(a.mw, appName, T("A forrás csak olvasható, ezért mozgatás helyett másolás történik."), walk.MsgBoxIconInformation)
		move = false
	}
	title := T("Kicsomagolás")
	if isRemoteKind(sk) {
		title = T("Letöltés")
	}
	exportDir := dst.path
	stage := ""
	if dk == "local" {
		if stage = a.stagingFor(dst.path); stage != "" {
			defer os.RemoveAll(stage)
			exportDir = stage
		}
	}
	err := runJob(a.mw, title, func(j *Job) error {
		if dk == "local" {
			if err := exportItems(src.vfs, items, exportDir, j); err != nil {
				return err
			}
		} else {
			// archívumból archívumba, archívum és FTP között: ideiglenes mappán át
			tmp, err := os.MkdirTemp("", "duocopy")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmp)
			if err := exportItems(src.vfs, items, tmp, j); err != nil {
				return err
			}
			var paths []string
			for _, it := range items {
				if _, err := os.Lstat(filepath.Join(tmp, it.Name)); err == nil {
					paths = append(paths, filepath.Join(tmp, it.Name))
				}
			}
			j.Status(T("Átírás a célba…"))
			j.mu.Lock()
			j.done = 0
			j.mu.Unlock()
			if err := importPaths(dst.vfs, paths, dst.vpath, j); err != nil {
				return err
			}
		}
		if move && j.Clean() && stage == "" {
			return deleteItems(src.vfs, items, j)
		}
		return nil
	})
	if err == nil && stage != "" {
		// védett célmappa: előbb a helyére kerüljön, csak utána töröljük a forrást
		if a.moveStaged(stage, dst.path) && move {
			err = runJob(a.mw, T("Törlés"), func(j *Job) error { return deleteItems(src.vfs, items, j) })
		}
	}
	a.afterVFSOp(src, err)
}

// localToVFS helyi elemeket ír archívumba vagy tölt fel FTP-re.
func (a *App) localToVFS(src *Panel, paths []string, dst *Panel, move bool) {
	if dst.kind() == "net" {
		return
	}
	if ar, ok := dst.vfs.(Archive); ok && !ar.Writable() {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("%s: ebbe az archívumba nem lehet írni.%s"), ar.Info(), sevenZipHint(ar)), walk.MsgBoxIconInformation)
		return
	}
	if src != nil {
		verb := T("%s másolása ide:")
		if move {
			verb = T("%s mozgatása ide:")
		}
		what := Nf(len(paths), "%d elem")
		if len(paths) == 1 {
			what = fmt.Sprintf(T("„%s”"), filepath.Base(paths[0]))
		}
		if walk.MsgBox(a.mw, appName, fmt.Sprintf(verb, what)+"\n"+dst.vfs.Display(dst.vpath),
			walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != win.IDYES {
			return
		}
	}
	title := T("Tömörítés")
	if isRemoteKind(dst.kind()) {
		title = T("Feltöltés")
	}
	err := runJob(a.mw, title, func(j *Job) error {
		err := importPaths(dst.vfs, paths, dst.vpath, j)
		if err == nil && move && j.Clean() {
			removeLocal(paths, j)
		}
		return err
	})
	a.afterVFSOp(src, err)
}

func (a *App) vfsDelete(p *Panel, items []*Entry) {
	if p.kind() == "net" {
		a.forgetServer()
		return
	}
	if !canDelete(p.vfs) {
		if ar, ok := p.vfs.(Archive); ok {
			walk.MsgBox(a.mw, appName, fmt.Sprintf(T("%s: ebből az archívumból nem lehet törölni.%s"), ar.Info(), sevenZipHint(ar)), walk.MsgBoxIconInformation)
		}
		return
	}
	where := T("az archívumból")
	if isRemoteKind(p.kind()) {
		where = T("a szerverről")
	}
	if walk.MsgBox(a.mw, T("Törlés"), fmt.Sprintf(T("%s VÉGLEGES törlése %s?\nEz nem vonható vissza."), describe(items), where),
		walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != win.IDYES {
		return
	}
	err := runJob(a.mw, T("Törlés"), func(j *Job) error { return deleteItems(p.vfs, items, j) })
	a.afterVFSOp(p, err)
}

func (a *App) afterVFSOp(src *Panel, err error) {
	if err != nil {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A művelet nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
	} else if src != nil {
		src.model.marked = map[string]bool{}
	}
	a.refreshBoth()
	a.active.tv.SetFocus()
}

// ── becsomagolás / kicsomagolás ──

func (a *App) pack() {
	if a.notInVFS() {
		return
	}
	p := a.active
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
	if name == "" || strings.HasSuffix(name, `\`) || strings.HasSuffix(name, ":") {
		name = "archivum"
	}
	target, level, pw, encNames, remember, ok := packDialog(a, describe(items), filepath.Join(p.path, name))
	if !ok {
		return
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(p.path, target)
	}
	final := target
	stage := a.stagingFor(filepath.Dir(final))
	if stage != "" { // védett célmappa: ideiglenes helyen készül, jóváhagyással kerül a helyére
		defer os.RemoveAll(stage)
		target = filepath.Join(stage, filepath.Base(final))
		if _, err := os.Stat(final); err == nil {
			copyFileProgress(final, target, nullProgress{})
		}
	}
	var err error
	if pw != "" && strings.HasSuffix(strings.ToLower(target), ".zip") { // titkosított ZIP (AES-256)
		if _, e := os.Stat(final); e == nil {
			walk.MsgBox(a.mw, appName, T("Jelszóval csak új ZIP-archívum hozható létre – ez a fájl már létezik. Adj meg másik nevet."), walk.MsgBoxIconWarning)
			return
		}
		os.Remove(target) // a védett mappához készült munkapéldány
		setArchivePassword(final, pw)
		err = runJob(a.mw, T("Becsomagolás"), func(j *Job) error { return packZipEncrypted(entryPaths(items), target, pw, j) })
	} else {
		z, oerr := openArchive(target)
		if oerr != nil {
			walk.MsgBox(a.mw, appName, fmt.Sprintf(T("Az archívum nem hozható létre:\n%s"), errText(oerr)), walk.MsgBoxIconWarning)
			return
		}
		if !z.Writable() {
			walk.MsgBox(a.mw, appName, fmt.Sprintf(T("%s: ebbe a formátumba nem lehet írni.%s"), z.Info(), sevenZipHint(z)), walk.MsgBoxIconWarning)
			return
		}
		old := zipLevel
		zipLevel = level
		packPassword, packEncryptNames = pw, encNames
		if pw != "" { // a kész archívumot (pl. a fájlnevek titkosítása után) ezzel olvassuk vissza
			setArchivePassword(target, pw)
			setArchivePassword(final, pw)
		}
		err = runJob(a.mw, T("Becsomagolás"), func(j *Job) error { return z.AddLocal(entryPaths(items), "", j) })
		zipLevel = old
		packPassword, packEncryptNames = "", false
	}
	if err == nil && stage != "" {
		a.localFileOp(foMove, []string{target}, filepath.Dir(final), fofNoConfirmation|fofNoConfirmMkdir|fofxShowElevationPrompt)
	}
	if pw != "" && !remember { // a jelszót csak a becsomagolás idejére tartottuk meg
		forgetArchivePassword(target)
		forgetArchivePassword(final)
	}
	a.afterVFSOp(p, err)
	if err == nil && strings.EqualFold(filepath.Dir(final), p.path) {
		p.selectName(filepath.Base(final))
	}
}

// packDialog: célfájl, formátum, tömörítési szint és (ZIP, 7Z) jelszó.
func packDialog(a *App, what, base string) (string, int, string, bool, bool, bool) {
	var dlg *walk.Dialog
	var le, pw1, pw2 *walk.LineEdit
	var namesCB, rememberCB *walk.CheckBox
	var password string
	var encNames, remember bool
	pwFor := func(ext string) bool { return ext == ".zip" || ext == ".7z" }
	var fmtCB, lvlCB *walk.ComboBox
	var ok, cancel *walk.PushButton
	var result string
	levels := []struct {
		label string
		v     int
	}{{T("Tárolás (tömörítés nélkül)"), 0}, {T("Gyors"), 1}, {T("Normál"), 6}, {T("Maximális"), 9}}
	lvlNames := make([]string, len(levels))
	for i, l := range levels {
		lvlNames[i] = l.label
	}
	fmtNames := make([]string, len(packFormats))
	for i, f := range packFormats {
		fmtNames[i] = f.label
	}
	fmtIdx := 0
	if v, _ := a.settings.Get("packFormat"); v != "" {
		for i, f := range packFormats {
			if f.ext == v {
				fmtIdx = i
			}
		}
	}
	lvlIdx := 2
	if v, _ := a.settings.Get("packLevel"); v != "" {
		for i, l := range levels {
			if fmt.Sprint(l.v) == v {
				lvlIdx = i
			}
		}
	}
	err := Dialog{
		AssignTo: &dlg, Title: T("Becsomagolás"), DefaultButton: &ok, CancelButton: &cancel,
		MinSize: Size{Width: 680, Height: 200}, Layout: VBox{},
		Children: []Widget{
			Label{Text: fmt.Sprintf(T("%s becsomagolása ebbe az archívumba (ha már létezik, hozzáadja):"), what)},
			LineEdit{AssignTo: &le, Text: base + packFormats[fmtIdx].ext},
			Composite{Layout: Grid{Columns: 2, MarginsZero: true}, Children: []Widget{
				Label{Text: T("Formátum:")},
				ComboBox{AssignTo: &fmtCB, Model: fmtNames, CurrentIndex: fmtIdx, OnCurrentIndexChanged: func() {
					if fmtCB == nil || le == nil || lvlCB == nil {
						return
					}
					i := fmtCB.CurrentIndex()
					if i >= 0 {
						le.SetText(stripArchiveExt(le.Text()) + packFormats[i].ext)
						lvlCB.SetEnabled(packFormats[i].ext == ".zip")
						if pw1 != nil {
							pw1.SetEnabled(pwFor(packFormats[i].ext))
							pw2.SetEnabled(pwFor(packFormats[i].ext))
							namesCB.SetEnabled(packFormats[i].ext == ".7z")
							rememberCB.SetEnabled(pwFor(packFormats[i].ext))
						}
					}
				}},
				Label{Text: T("Tömörítés (ZIP):")},
				ComboBox{AssignTo: &lvlCB, Model: lvlNames, CurrentIndex: lvlIdx},
				Label{Text: T("Jelszó (ZIP, 7Z):")},
				LineEdit{AssignTo: &pw1, PasswordMode: true},
				Label{Text: T("Jelszó újra:")},
				LineEdit{AssignTo: &pw2, PasswordMode: true},
				Label{Text: ""},
				CheckBox{AssignTo: &namesCB, Text: T("A fájlnevek is legyenek titkosak (csak 7Z)"), Checked: true},
				Label{Text: ""},
				CheckBox{AssignTo: &rememberCB, Text: T("Jelszó megjegyzése a program bezárásáig (megnyitáskor nem kérdez rá)")},
			}},
			Label{Text: T("Üresen hagyva nincs jelszó. A jelszót a program nem menti – ha elfelejted, a tartalom nem állítható vissza.")},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &ok, Text: T("Becsomagolás"), OnClicked: func() {
					result = strings.Trim(strings.TrimSpace(le.Text()), `"`)
					password, encNames = "", false
					if fi := fmtCB.CurrentIndex(); fi >= 0 && pwFor(packFormats[fi].ext) && pw1.Text() != "" {
						if pw1.Text() != pw2.Text() {
							walk.MsgBox(dlg, appName, T("A két jelszó nem egyezik."), walk.MsgBoxIconWarning)
							pw2.SetFocus()
							return
						}
						password, encNames, remember = pw1.Text(), namesCB.Checked(), rememberCB.Checked()
					}
					if fi := fmtCB.CurrentIndex(); fi >= 0 {
						a.settings.Put("packFormat", packFormats[fi].ext)
					}
					if li := lvlCB.CurrentIndex(); li >= 0 {
						a.settings.Put("packLevel", fmt.Sprint(levels[li].v))
					}
					dlg.Accept()
				}},
				PushButton{AssignTo: &cancel, Text: T("Mégse"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		return "", 0, "", false, false, false
	}
	themeDialog(dlg)
	lvlCB.SetEnabled(packFormats[fmtIdx].ext == ".zip")
	pw1.SetEnabled(pwFor(packFormats[fmtIdx].ext))
	pw2.SetEnabled(pwFor(packFormats[fmtIdx].ext))
	namesCB.SetEnabled(packFormats[fmtIdx].ext == ".7z")
	rememberCB.SetEnabled(pwFor(packFormats[fmtIdx].ext))
	if a.icon != nil {
		dlg.SetIcon(a.icon)
	}
	if dlg.Run() != walk.DlgCmdOK || result == "" {
		return "", 0, "", false, false, false
	}
	lvl := levels[2].v
	if v, _ := a.settings.Get("packLevel"); v != "" {
		fmt.Sscan(v, &lvl)
	}
	return result, lvl, password, encNames, remember, true
}

func (a *App) unpack() {
	if a.notInVFS() {
		return
	}
	p := a.active
	var arcs []*Entry
	for _, e := range p.selectedEntries() {
		if !e.IsDir && isArchive(e.Name) {
			arcs = append(arcs, e)
		}
	}
	if len(arcs) == 0 {
		walk.MsgBox(a.mw, appName, T("Jelölj ki egy vagy több archívumot a kicsomagoláshoz."), walk.MsgBoxIconInformation)
		return
	}
	def := a.other().path
	if len(arcs) == 1 {
		def = filepath.Join(def, filepath.Base(stripArchiveExt(arcs[0].Path)))
		if strings.EqualFold(def, filepath.Join(a.other().path, arcs[0].Name)) {
			def = filepath.Join(a.other().path, arcs[0].Stem())
		}
	}
	t, ok := inputBox(a.mw, T("Kicsomagolás"), fmt.Sprintf(T("%s kicsomagolása ebbe a mappába:"), describe(arcs)), def, false)
	t = strings.Trim(strings.TrimSpace(t), `"`)
	if !ok || t == "" {
		return
	}
	if !filepath.IsAbs(t) {
		t = filepath.Join(p.path, t)
	}
	os.MkdirAll(t, 0o777)
	final := t
	if stage := a.stagingFor(t); stage != "" {
		defer os.RemoveAll(stage)
		t = stage
	}
	err := runJob(a.mw, T("Kicsomagolás"), func(j *Job) error {
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
	})
	if err == nil && t != final {
		a.moveStaged(t, final)
	}
	a.afterVFSOp(nil, err)
}

// stagingFor: ha a mappába a program saját jogaival nem írhat (pl. C:\), egy
// ideiglenes mappát ad, ahonnan a Windows jóváhagyással teszi a helyére; különben "".
func (a *App) stagingFor(dir string) string {
	if canWriteDir(dir) {
		return ""
	}
	tmp, err := os.MkdirTemp("", "duostage")
	if err != nil {
		return ""
	}
	dlog("védett célmappa (%s): ideiglenes hely %s", dir, tmp)
	return tmp
}

// moveStaged az ideiglenes mappa tartalmát a célba mozgatja (rendszergazdai jóváhagyással, ha kell).
func (a *App) moveStaged(stage, dest string) bool {
	des, err := os.ReadDir(stage)
	if err != nil || len(des) == 0 {
		return false
	}
	var srcs []string
	for _, d := range des {
		srcs = append(srcs, filepath.Join(stage, d.Name()))
	}
	return a.localFileOp(foMove, srcs, dest, fofAllowUndo|fofNoConfirmMkdir|fofxShowElevationPrompt)
}
