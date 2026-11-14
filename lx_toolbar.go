//go:build linux

package main

// Eszköztár Linuxon: színes lapkák fehér jellel (mint a Windows-változatban),
// állítható mérettel; belső parancsok vagy külső programok.

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type command struct {
	id, label string
	icon      fyne.Resource
	tile      color.NRGBA
	run       func(a *App)
}

func tileColor(id string) color.NRGBA {
	r, g, b := tbTile(id)
	return rgb(r, g, b)
}

func commands() []command {
	c := func(id, label string, icon fyne.Resource, run func(a *App)) command {
		return command{id, label, icon, tileColor(id), run}
	}
	return []command{
		c("back", T("Vissza"), theme.NavigateBackIcon(), func(a *App) { a.active.goBack() }),
		c("forward", T("Előre"), theme.NavigateNextIcon(), func(a *App) { a.active.goForward() }),
		c("up", T("Szülőmappa"), theme.MoveUpIcon(), func(a *App) { a.active.goUp() }),
		c("root", T("Gyökérmappa"), theme.HomeIcon(), func(a *App) { a.active.goRoot() }),
		c("refresh", T("Frissítés"), theme.ViewRefreshIcon(), func(a *App) { a.refreshAll() }),
		c("view", T("Nézőke"), theme.VisibilityIcon(), func(a *App) { a.viewFile() }),
		c("edit", T("Szerkesztés"), theme.DocumentCreateIcon(), func(a *App) { a.editFile() }),
		c("copy", T("Másolás"), theme.ContentCopyIcon(), func(a *App) { a.transfer(false) }),
		c("move", T("Mozgatás"), theme.MailForwardIcon(), func(a *App) { a.transfer(true) }),
		c("mkdir", T("Új mappa"), theme.FolderNewIcon(), func(a *App) { a.mkdir() }),
		c("delete", T("Törlés"), theme.DeleteIcon(), func(a *App) { a.deleteSelected(false) }),
		c("rename", T("Átnevezés"), theme.ContentRedoIcon(), func(a *App) { a.rename() }),
		c("multirename", T("Többfájl-átnevezés"), theme.ListIcon(), func(a *App) { a.multiRename() }),
		c("search", T("Fájlkeresés"), theme.SearchIcon(), func(a *App) { a.findFiles() }),
		c("pack", T("Becsomagolás"), theme.DownloadIcon(), func(a *App) { a.pack() }),
		c("unpack", T("Kicsomagolás"), theme.UploadIcon(), func(a *App) { a.unpack() }),
		c("compare", T("Mappák összehasonlítása"), theme.SearchReplaceIcon(), func(a *App) { a.compareDirs() }),
		c("sync", T("Mappák szinkronizálása"), theme.ViewRefreshIcon(), func(a *App) { a.syncDirs() }),
		c("queue", T("Háttérműveletek"), theme.HistoryIcon(), func(a *App) { a.showQueue() }),
		c("diff", T("Fájlok összehasonlítása tartalom szerint"), theme.SearchReplaceIcon(), func(a *App) { a.compareContent() }),
		c("checksum", T("Ellenőrzőösszeg készítése"), theme.ConfirmIcon(), func(a *App) { a.makeChecksums() }),
		c("verify", T("Ellenőrzőösszeg ellenőrzése"), theme.ConfirmIcon(), func(a *App) { a.verifyChecksums() }),
		c("dupes", T("Ismétlődő fájlok keresése"), theme.ContentCopyIcon(), func(a *App) { a.findDuplicates() }),
		c("split", T("Fájl darabolása"), theme.ContentCutIcon(), func(a *App) { a.splitCurrent() }),
		c("join", T("Fájlok összefűzése"), theme.ContentPasteIcon(), func(a *App) { a.joinCurrent() }),
		c("attrs", T("Attribútumok és dátum"), theme.InfoIcon(), func(a *App) { a.changeAttrs() }),
		c("newfile", T("Új fájl"), theme.FileIcon(), func(a *App) { a.newFile() }),
		c("permdelete", T("Végleges törlés"), theme.DeleteIcon(), func(a *App) { a.deleteSelected(true) }),
		c("quickconnect", T("Gyors kapcsolódás"), theme.StorageIcon(), func(a *App) { a.quickConnect() }),
		c("disconnect", T("Kapcsolat bontása"), theme.LogoutIcon(), func(a *App) { a.ftpDisconnect() }),
		c("hotlist", T("Kedvenc mappák"), theme.FolderIcon(), func(a *App) { a.hotlistMenu() }),
		c("history", T("Előzmények"), theme.HistoryIcon(), func(a *App) { a.historyPopup() }),
		c("quicksearch", T("Gyorskeresés a listában"), theme.SearchIcon(), func(a *App) { a.active.startQuickSearch("") }),
		c("quickview", T("Gyorsnézet"), theme.VisibilityIcon(), func(a *App) { a.toggleQuickView() }),
		c("newtab", T("Új fül"), theme.ContentAddIcon(), func(a *App) { a.active.newTab() }),
		c("closetab", T("Fül bezárása"), theme.CancelIcon(), func(a *App) { a.active.closeTab() }),
		c("nexttab", T("Következő fül"), theme.NavigateNextIcon(), func(a *App) { a.active.cycleTab(1) }),
		c("viewdetail", T("Részletes nézet"), theme.ListIcon(), func(a *App) { a.active.setViewMode(viewDetail) }),
		c("viewbrief", T("Rövid nézet"), theme.GridIcon(), func(a *App) { a.active.setViewMode(viewBrief) }),
		c("viewthumbs", T("Bélyegkép nézet"), theme.MediaPhotoIcon(), func(a *App) { a.active.setViewMode(viewThumbs) }),
		c("gotopath", T("Ugrás mappára"), theme.FolderOpenIcon(), func(a *App) { a.gotoPath() }),
		c("samedir", T("Ugyanaz a mappa a másik panelen"), theme.ViewRestoreIcon(), func(a *App) { a.sameDirOther() }),
		c("cmdline", T("Parancssor"), theme.ComputerIcon(), func(a *App) { a.focusCmd() }),
		c("selectall", T("Mindet kijelöl"), theme.CheckButtonCheckedIcon(), func(a *App) { a.active.markAll() }),
		c("unselectall", T("Kijelölés törlése"), theme.CheckButtonIcon(), func(a *App) { a.active.setMarks(map[string]bool{}) }),
		c("invert", T("Kijelölés megfordítása"), theme.CheckButtonCheckedIcon(), func(a *App) { a.active.invertMarks() }),
		c("selpattern", T("Kijelölés mintával"), theme.CheckButtonCheckedIcon(), func(a *App) { a.selectPattern(true) }),
		c("unselpattern", T("Kijelölés megszüntetése mintával"), theme.CheckButtonIcon(), func(a *App) { a.selectPattern(false) }),
		c("copypaths", T("Útvonalak másolása vágólapra"), theme.ContentCopyIcon(), func(a *App) { a.copyPaths() }),
		c("usermenu", T("Saját menü (felugró)"), theme.MenuIcon(), func(a *App) { a.userMenuPopup() }),
		c("keys", T("Billentyűparancsok beállítása"), theme.SettingsIcon(), func(a *App) { a.editKeys() }),
		c("exit", T("Kilépés"), theme.LogoutIcon(), func(a *App) { a.quit() }),
		c("swap", T("Panelek cseréje"), theme.ViewRestoreIcon(), func(a *App) { a.swapPanels() }),
		c("hidden", T("Rejtett fájlok"), theme.VisibilityOffIcon(), func(a *App) { a.toggleHidden() }),
		c("ftp", T("Távoli kapcsolatok (FTP, SFTP, WebDAV, SMB)"), theme.StorageIcon(), func(a *App) { a.ftpConnect() }),
		c("terminal", T("Terminál"), theme.ComputerIcon(), func(a *App) { a.terminal() }),
		c("settings", T("Beállítások"), theme.SettingsIcon(), func(a *App) { a.settingsDialog() }),
		c("help", T("Súgó"), theme.HelpIcon(), func(a *App) { a.help() }),
	}
}

func findCommand(id string) (command, bool) {
	for _, c := range commands() {
		if c.id == id {
			return c, true
		}
	}
	return command{}, false
}

// tbButton: ugyanaz a leírás, mint a Windows-változatban (a Linux-gombok külön kulcson).
type tbButton struct {
	Sep    bool   `json:"sep,omitempty"`
	Cmd    string `json:"cmd,omitempty"`
	Exe    string `json:"exe,omitempty"`
	Params string `json:"params,omitempty"`
	Dir    string `json:"dir,omitempty"`
	Icon   string `json:"icon,omitempty"`
	Tip    string `json:"tip,omitempty"`
}

func defaultButtons() []tbButton {
	var out []tbButton
	for _, id := range tbDefaultIDs(false) {
		if id == "-" {
			out = append(out, tbButton{Sep: true})
		} else {
			out = append(out, tbButton{Cmd: id})
		}
	}
	return out
}

func (a *App) loadButtons() []tbButton {
	v, ok := a.settings.Get("lx.toolbar")
	if !ok || v == "" {
		return defaultButtons()
	}
	var b []tbButton
	if json.Unmarshal([]byte(v), &b) != nil {
		return defaultButtons()
	}
	if tbIsOldDefault(buttonSig(b), false) {
		return defaultButtons() // a régi alapsor: az új, egységes alapsor váltja
	}
	return b
}

// buttonSig: a gombsor rövid leírása az alapsorral való összevetéshez.
func buttonSig(b []tbButton) []string {
	var out []string
	for _, x := range b {
		switch {
		case x.Sep:
			out = append(out, "-")
		case x.Cmd != "":
			out = append(out, x.Cmd)
		case x.Params == "" && x.Dir == "" && x.Icon == "":
			out = append(out, "exe:"+x.Exe)
		default:
			out = append(out, "exe:*")
		}
	}
	return out
}

func (a *App) saveButtons(b []tbButton) {
	j, _ := json.Marshal(b)
	a.settings.Put("lx.toolbar", string(j))
	a.settings.Save()
}

func (b tbButton) tooltip() string {
	if b.Tip != "" {
		return b.Tip
	}
	if c, ok := findCommand(b.Cmd); ok {
		return c.label
	}
	return filepath.Base(b.Exe)
}

func (a *App) toolbarVisible() bool { return a.settingBool("toolbar.show", true) }
func (a *App) toolbarIconSize() float32 {
	n := a.settingInt("toolbar.iconSize", 24)
	if n < 16 || n > 48 {
		n = 24
	}
	return float32(n)
}

var iconSizes = []int{16, 20, 24, 32, 40, 48}

func (a *App) buildToolbar() {
	if a.toolbarItem != nil {
		a.toolbarItem.Checked = a.toolbarVisible()
		if m := a.win.MainMenu(); m != nil {
			m.Refresh()
		}
	}
	if !a.toolbarVisible() {
		a.toolbarHolder.Objects = nil
		a.toolbarHolder.Refresh()
		return
	}
	size := a.toolbarIconSize()
	row := container.NewHBox()
	for _, b := range a.loadButtons() {
		b := b
		if b.Sep {
			sep := canvas.NewRectangle(theme.Color(theme.ColorNameSeparator))
			sep.SetMinSize(fyne.NewSize(1, size))
			row.Add(container.NewPadded(sep))
			continue
		}
		var res fyne.Resource
		var tile color.NRGBA
		if c, ok := findCommand(b.Cmd); ok {
			res, tile = c.icon, c.tile
		} else if b.Exe != "" {
			if strings.ContainsAny(b.Exe, `\:`) || strings.HasSuffix(strings.ToLower(b.Exe), ".exe") {
				continue // Windows-program (a Windowsról áthozott beállításokból)
			}
			if r := appIconFor(b.Exe, b.Icon); r != nil {
				res, tile = r, color.NRGBA{} // a program saját ikonja, színes lapka nélkül
			} else {
				res, tile = theme.MediaPlayIcon(), tileColor("")
			}
		} else {
			continue
		}
		row.Add(newTileButton(a, res, tile, size, b.tooltip(), func() { a.runButton(b) }))
	}
	a.toolbarHolder.Objects = []fyne.CanvasObject{container.NewPadded(row)}
	a.toolbarHolder.Refresh()
}

func (a *App) runButton(b tbButton) {
	defer a.active.focus()
	if b.Cmd != "" {
		if c, ok := findCommand(b.Cmd); ok {
			c.run(a)
		}
		return
	}
	p := a.active
	dir := p.path
	if b.Dir != "" {
		dir = a.expandParams(b.Dir)
	}
	line := shellQuote(b.Exe) + " " + a.expandParams(b.Params)
	c := exec.Command("sh", "-c", line)
	c.Dir = dir
	if err := startDetached(hostify(c)); err != nil {
		a.warn(fmt.Sprintf(T("A program nem indítható:\n%s"), errText(err)))
	}
}

// expandParams: %P mappa, %N név, %F teljes út, %S kijelöltek, %T másik panel.
func (a *App) expandParams(s string) string {
	p := a.active
	cur := p.currentEntry()
	name, full := "", ""
	if cur != nil && !cur.IsParent && p.vfs == nil {
		name, full = cur.Name, cur.Path
	}
	var sel []string
	if p.vfs == nil {
		for _, e := range p.selectedEntries() {
			sel = append(sel, shellQuote(e.Path))
		}
	}
	r := strings.NewReplacer("%P", shellQuote(p.path), "%N", shellQuote(name), "%F", shellQuote(full),
		"%S", strings.Join(sel, " "), "%T", shellQuote(a.other().path), "%%", "%")
	return r.Replace(s)
}

// ── egy lapkás gomb, buboréksúgóval ──

type tileButton struct {
	widget.BaseWidget
	a     *App
	bg    *canvas.Rectangle
	hover *canvas.Rectangle
	icon  *canvas.Image
	size  float32
	tip   string
	onTap func()
	timer *time.Timer
	pop   *widget.PopUp
}

func newTileButton(a *App, res fyne.Resource, tile color.NRGBA, size float32, tip string, onTap func()) *tileButton {
	t := &tileButton{a: a, size: size, tip: tip, onTap: onTap}
	t.bg = canvas.NewRectangle(tile)
	t.bg.CornerRadius = size * 0.22
	t.hover = canvas.NewRectangle(color.Transparent)
	t.hover.CornerRadius = size*0.22 + 3
	if tile.A == 0 {
		t.icon = canvas.NewImageFromResource(res) // saját (színes) ikon
	} else {
		t.icon = canvas.NewImageFromResource(theme.NewColoredResource(res, colWhite))
	}
	t.icon.FillMode = canvas.ImageFillContain
	t.ExtendBaseWidget(t)
	return t
}

func (t *tileButton) CreateRenderer() fyne.WidgetRenderer {
	return &tileRenderer{t: t}
}

func (t *tileButton) Tapped(*fyne.PointEvent) {
	t.hideTip()
	if t.onTap != nil {
		t.onTap()
	}
}

func (t *tileButton) MouseIn(ev *desktop.MouseEvent) {
	t.hover.FillColor = theme.Color(theme.ColorNameHover)
	t.hover.Refresh()
	pos := ev.AbsolutePosition
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(600*time.Millisecond, func() {
		fyne.Do(func() { t.showTip(pos) })
	})
}

func (t *tileButton) MouseMoved(*desktop.MouseEvent) {}

func (t *tileButton) MouseOut() {
	t.hover.FillColor = color.Transparent
	t.hover.Refresh()
	t.hideTip()
}

func (t *tileButton) showTip(pos fyne.Position) {
	if t.pop != nil || t.tip == "" {
		return
	}
	l := widget.NewLabel(t.tip)
	t.pop = widget.NewPopUp(l, t.a.win.Canvas())
	t.pop.ShowAtPosition(fyne.NewPos(pos.X+8, pos.Y+t.size))
}

func (t *tileButton) hideTip() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	if t.pop != nil {
		t.pop.Hide()
		t.pop = nil
	}
}

type tileRenderer struct{ t *tileButton }

func (r *tileRenderer) Destroy() {}
func (r *tileRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.t.hover, r.t.bg, r.t.icon}
}
func (r *tileRenderer) MinSize() fyne.Size {
	s := r.t.size + 8
	return fyne.NewSize(s, s)
}
func (r *tileRenderer) Layout(s fyne.Size) {
	t := r.t
	t.hover.Resize(s)
	inner := t.size
	off := (s.Width - inner) / 2
	t.bg.Resize(fyne.NewSize(inner, inner))
	t.bg.Move(fyne.NewPos(off, (s.Height-inner)/2))
	ic := inner * 0.68
	t.icon.Resize(fyne.NewSize(ic, ic))
	t.icon.Move(fyne.NewPos((s.Width-ic)/2, (s.Height-ic)/2))
}
func (r *tileRenderer) Refresh() {
	r.Layout(r.t.Size())
	canvas.Refresh(r.t)
}

// ── eszköztár szerkesztése ──

func (a *App) editToolbar() { a.editButtons("toolbar") }

// editButtons: az eszköztár ("toolbar") vagy a saját menü ("menu") szerkesztője.
func (a *App) editButtons(kind string) {
	isMenu := kind == "menu"
	buttons := a.loadButtons()
	title := T("Eszköztár szerkesztése")
	if isMenu {
		buttons = a.loadUserMenu()
		title = T("Saját menü szerkesztése")
	}
	w := a.fa.NewWindow(title)
	w.SetIcon(appIconRes)
	sel := -1
	label := func(b tbButton) string {
		switch {
		case b.Sep:
			return T("──── elválasztó ────")
		case b.Cmd != "":
			return b.tooltip()
		}
		return b.tooltip() + "   (" + b.Exe + " " + b.Params + ")"
	}
	list := widget.NewList(func() int { return len(buttons) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) { o.(*widget.Label).SetText(label(buttons[i])) })
	list.OnSelected = func(i widget.ListItemID) { sel = i }
	apply := func() {
		list.Refresh()
		if isMenu {
			a.saveUserMenu(buttons)
			a.rebuildUserMenu()
			return
		}
		a.saveButtons(buttons)
		a.buildToolbar()
	}
	insert := func(b tbButton) {
		at := len(buttons)
		if sel >= 0 {
			at = sel + 1
		}
		buttons = append(buttons[:at], append([]tbButton{b}, buttons[at:]...)...)
		sel = at
		list.Select(at)
		apply()
	}
	var names []string
	for _, c := range commands() {
		names = append(names, c.label)
	}
	cmdSel := widget.NewSelect(names, nil)
	cmdSel.PlaceHolder = T("Belső parancs…")
	addCmd := widget.NewButton(T("Hozzáadás"), func() {
		for _, c := range commands() {
			if c.label == cmdSel.Selected {
				insert(tbButton{Cmd: c.id})
			}
		}
	})
	exe, params, tip, iconE := widget.NewEntry(), widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	iconE.SetPlaceHolder(T("üres: a program saját ikonja; vagy ikonnév, PNG, SVG"))
	iconBrowse := widget.NewButton(T("Tallózás…"), func() {
		d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
			if r != nil {
				iconE.SetText(r.URI().Path())
				r.Close()
			}
		}, w)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".png", ".svg", ".PNG", ".SVG"}))
		if l, err := storage.ListerForURI(storage.NewFileURI(hostPath("/usr/share/icons/hicolor"))); err == nil {
			d.SetLocation(l)
		}
		d.Resize(fyne.NewSize(700, 500))
		d.Show()
	})
	exe.SetPlaceHolder(T("program, pl. gimp"))
	params.SetPlaceHolder(T("paraméterek, pl. %S"))
	tip.SetPlaceHolder(T("felirat"))
	addExe := widget.NewButton(T("Külső program hozzáadása"), func() {
		if strings.TrimSpace(exe.Text) == "" {
			return
		}
		insert(tbButton{Exe: strings.TrimSpace(exe.Text), Params: params.Text, Tip: tip.Text, Icon: strings.TrimSpace(iconE.Text)})
		exe.SetText("")
		params.SetText("")
		tip.SetText("")
		iconE.SetText("")
	})
	move := func(d int) {
		if sel < 0 || sel+d < 0 || sel+d >= len(buttons) {
			return
		}
		buttons[sel], buttons[sel+d] = buttons[sel+d], buttons[sel]
		sel += d
		list.Select(sel)
		apply()
	}
	side := container.NewVBox(
		widget.NewButton(T("Fel"), func() { move(-1) }),
		widget.NewButton(T("Le"), func() { move(1) }),
		widget.NewButton(T("Elválasztó"), func() { insert(tbButton{Sep: true}) }),
		widget.NewButton(T("Eltávolítás"), func() {
			if sel >= 0 && sel < len(buttons) {
				buttons = append(buttons[:sel], buttons[sel+1:]...)
				list.UnselectAll()
				sel = -1
				apply()
			}
		}),
		widget.NewButton(T("Alapállapot"), func() { buttons = defaultButtons(); sel = -1; list.UnselectAll(); apply() }),
		widget.NewSeparator(),
		widget.NewButton(T("Bezárás"), w.Close),
	)
	bottom := container.NewVBox(
		container.NewBorder(nil, nil, nil, addCmd, cmdSel),
		widget.NewForm(widget.NewFormItem(T("Program"), exe), widget.NewFormItem(T("Paraméterek"), params), widget.NewFormItem(T("Felirat"), tip),
			widget.NewFormItem(T("Ikon"), container.NewBorder(nil, nil, nil, iconBrowse, iconE))),
		addExe,
		widget.NewLabel(T("Paraméterek: %P mappa, %N név, %F teljes útvonal, %S kijelöltek, %T a másik panel mappája.")),
	)
	w.SetContent(container.NewBorder(nil, bottom, nil, side, list))
	w.Resize(fyne.NewSize(640, 560))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyEscape {
			w.Close()
		}
	})
	w.Show()
}
