//go:build windows

package main

// Szerkeszthető eszköztár ikonokkal („gombsor”): belső parancsok
// és külső programok, paraméterekkel (%P, %N, %F, %S, %T).

import (
	"encoding/json"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

type command struct {
	id, label, glyph string
	run              func(a *App)
}

// commands a gombokra tehető belső parancsok. A glyph a Windows 10/11
// „Segoe MDL2 Assets” / „Segoe Fluent Icons” betűtípus egy ikonja.
func commands() []command {
	return []command{
		{"back", T("Vissza"), "", func(a *App) { a.active.goBack() }},
		{"forward", T("Előre"), "", func(a *App) { a.active.goForward() }},
		{"up", T("Szülőmappa"), "", func(a *App) { a.active.goUp() }},
		{"root", T("Gyökérmappa"), "", func(a *App) { a.active.goRoot() }},
		{"refresh", T("Frissítés"), "", func(a *App) { a.refreshActive() }},
		{"view", T("Nézőke"), "", func(a *App) { a.viewFile() }},
		{"edit", T("Szerkesztés"), "", func(a *App) { a.editFile() }},
		{"copy", T("Másolás"), "", func(a *App) { a.transfer(false) }},
		{"move", T("Mozgatás"), "", func(a *App) { a.transfer(true) }},
		{"mkdir", T("Új mappa"), "", func(a *App) { a.mkdir() }},
		{"delete", T("Törlés"), "", func(a *App) { a.deleteSelected(false) }},
		{"rename", T("Átnevezés"), "", func(a *App) { a.rename() }},
		{"multirename", T("Többfájl-átnevezés"), "", func(a *App) { a.multiRename() }},
		{"search", T("Fájlkeresés"), "", func(a *App) { a.findFiles() }},
		{"pack", T("Becsomagolás"), "", func(a *App) { a.pack() }},
		{"unpack", T("Kicsomagolás"), "", func(a *App) { a.unpack() }},
		{"compare", T("Mappák összehasonlítása"), "", func(a *App) { a.compareDirs() }},
		{"sync", T("Mappák szinkronizálása"), "\uE895", func(a *App) { a.syncDirs() }},
		{"queue", T("Háttérműveletek"), "\uE81C", func(a *App) { a.showQueue() }},
		{"diff", T("Fájlok összehasonlítása tartalom szerint"), "\uE8AB", func(a *App) { a.compareContent() }},
		{"checksum", T("Ellenőrzőösszeg készítése"), "\uE73E", func(a *App) { a.makeChecksums() }},
		{"verify", T("Ellenőrzőösszeg ellenőrzése"), "\uE73E", func(a *App) { a.verifyChecksums() }},
		{"dupes", T("Ismétlődő fájlok keresése"), "\uE8C8", func(a *App) { a.findDuplicates() }},
		{"split", T("Fájl darabolása"), "\uE8C6", func(a *App) { a.splitCurrent() }},
		{"join", T("Fájlok összefűzése"), "\uE71B", func(a *App) { a.joinCurrent() }},
		{"attrs", T("Attribútumok és dátum"), "\uE946", func(a *App) { a.changeAttrs() }},
		{"newfile", T("Új fájl"), "\uE8A5", func(a *App) { a.newFile() }},
		{"permdelete", T("Végleges törlés"), "\uE74D", func(a *App) { a.deleteSelected(true) }},
		{"quickconnect", T("Gyors kapcsolódás"), "\uE774", func(a *App) { a.quickConnect() }},
		{"disconnect", T("Kapcsolat bontása"), "\uE8BB", func(a *App) { a.ftpDisconnect() }},
		{"hotlist", T("Kedvenc mappák"), "\uE734", func(a *App) { a.hotlistMenu() }},
		{"history", T("Előzmények"), "\uE81C", func(a *App) { a.historyPopup() }},
		{"quicksearch", T("Gyorskeresés a listában"), "\uE721", func(a *App) { a.active.tv.SetFocus(); a.active.startQuickSearch("") }},
		{"quickview", T("Gyorsnézet"), "\uE890", func(a *App) { a.toggleQuickView() }},
		{"newtab", T("Új fül"), "\uE710", func(a *App) { a.active.newTab() }},
		{"closetab", T("Fül bezárása"), "\uE711", func(a *App) { a.active.closeTab() }},
		{"nexttab", T("Következő fül"), "\uE72A", func(a *App) { a.active.cycleTab(1) }},
		{"viewdetail", T("Részletes nézet"), "\uE8FD", func(a *App) { a.active.setViewMode(viewDetail) }},
		{"viewbrief", T("Rövid nézet"), "\uE8A9", func(a *App) { a.active.setViewMode(viewBrief) }},
		{"viewthumbs", T("Bélyegkép nézet"), "\uE91B", func(a *App) { a.active.setViewMode(viewThumbs) }},
		{"gotopath", T("Ugrás mappára"), "\uE8AD", func(a *App) { a.gotoPathDialog() }},
		{"samedir", T("Ugyanaz a mappa a másik panelen"), "\uE8AB", func(a *App) { a.other().navigate(a.active.path, "", true, false) }},
		{"cmdline", T("Parancssor"), "\uE756", func(a *App) { a.cmd.SetFocus() }},
		{"selectall", T("Mindet kijelöl"), "\uE8B3", func(a *App) { a.active.markAll() }},
		{"unselectall", T("Kijelölés törlése"), "\uE8E6", func(a *App) { a.active.setMarks(map[string]bool{}) }},
		{"invert", T("Kijelölés megfordítása"), "\uE8B3", func(a *App) { a.active.invertMarks() }},
		{"selpattern", T("Kijelölés mintával"), "\uE8B3", func(a *App) { a.selectPattern(true) }},
		{"unselpattern", T("Kijelölés megszüntetése mintával"), "\uE8E6", func(a *App) { a.selectPattern(false) }},
		{"copypaths", T("Útvonalak másolása vágólapra"), "\uE8C8", func(a *App) { a.copyPaths() }},
		{"copynames", T("Nevek másolása vágólapra"), "\uE8C8", func(a *App) { a.copyNames() }},
		{"usermenu", T("Saját menü (felugró)"), "\uE700", func(a *App) { a.userMenuPopup() }},
		{"keys", T("Billentyűparancsok beállítása"), "\uE765", func(a *App) { a.editKeys() }},
		{"exit", T("Kilépés"), "\uE7E8", func(a *App) { a.mw.Close() }},
		{"swap", T("Panelek cseréje"), "", func(a *App) { a.swapPanels() }},
		{"hidden", T("Rejtett fájlok"), "", func(a *App) { a.toggleHidden() }},
		{"ftp", T("Távoli kapcsolatok (FTP, SFTP, WebDAV)"), "", func(a *App) { a.ftpConnect() }},
		{"network", T("Hálózat"), "", func(a *App) { a.openNetwork() }},
		{"terminal", T("Terminál"), "", func(a *App) { a.terminal() }},
		{"settings", T("Beállítások"), "", func(a *App) { a.settingsDialog() }},
		{"help", T("Súgó"), "", func(a *App) { a.help() }},
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

// tbButton egy gomb leírása. Cmd: belső parancs; ha üres, Exe külső program.
// Sep: elválasztó.
type tbButton struct {
	Sep     bool   `json:"sep,omitempty"`
	Cmd     string `json:"cmd,omitempty"`
	Exe     string `json:"exe,omitempty"`
	Params  string `json:"params,omitempty"`
	Dir     string `json:"dir,omitempty"`
	Icon    string `json:"icon,omitempty"`
	IconIdx int    `json:"iconIdx,omitempty"`
	Tip     string `json:"tip,omitempty"`
}

func defaultButtons() []tbButton {
	var out []tbButton
	for _, id := range tbDefaultIDs(true) {
		if id == "-" {
			out = append(out, tbButton{Sep: true})
		} else {
			out = append(out, tbButton{Cmd: id})
		}
	}
	return out
}

func (a *App) loadButtons() []tbButton {
	v, ok := a.settings.Get("toolbar")
	if !ok || v == "" {
		return defaultButtons()
	}
	var b []tbButton
	if json.Unmarshal([]byte(v), &b) != nil {
		return defaultButtons()
	}
	if tbIsOldDefault(buttonSig(b), true) {
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
	data, _ := json.Marshal(b)
	a.settings.Put("toolbar", string(data))
	a.settings.Save()
}

func (b tbButton) tooltip() string {
	if b.Tip != "" {
		return b.Tip
	}
	if c, ok := findCommand(b.Cmd); ok {
		return c.label
	}
	return strings.TrimSuffix(filepath.Base(b.Exe), filepath.Ext(b.Exe))
}

// ── ikonok ──

var glyphFont string

func iconFontFamily() string {
	if glyphFont == "" {
		glyphFont = "Segoe MDL2 Assets"
		if _, err := os.Stat(filepath.Join(os.Getenv("SystemRoot"), "Fonts", "SegoeIcons.ttf")); err == nil {
			glyphFont = "Segoe Fluent Icons" // Windows 11
		}
	}
	return glyphFont
}

// glyphIcon színes, lekerekített lapkára fehér ikonjelet rajzol: így a jel világos és
// sötét háttéren is jól látszik. (A GDI nem kezel alfacsatornát, ezért fehér alapra
// fekete jelet rajzolunk, és a sötétségből lesz a jel takarása.)
func glyphIcon(glyph string, tile walk.Color, size int) *walk.Icon {
	px := size * 2 // kétszeres felbontás, éles marad nagy DPI-n is
	bmp, err := walk.NewBitmapForDPI(walk.Size{Width: px, Height: px}, 96)
	if err != nil {
		return nil
	}
	defer bmp.Dispose()
	cv, err := walk.NewCanvasFromImage(bmp)
	if err != nil {
		return nil
	}
	if white, err := walk.NewSolidColorBrush(walk.RGB(255, 255, 255)); err == nil {
		cv.FillRectanglePixels(white, walk.Rectangle{Width: px, Height: px})
		white.Dispose()
	}
	font, err := walk.NewFont(iconFontFamily(), px*13/32, 0)
	if err != nil {
		cv.Dispose()
		return nil
	}
	// vastagított jel: kissé eltolva többször rajzolva
	for _, o := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		cv.DrawTextPixels(glyph, font, walk.RGB(0, 0, 0), walk.Rectangle{X: o[0], Y: o[1], Width: px, Height: px},
			walk.TextCenter|walk.TextVCenter|walk.TextSingleLine)
	}
	cv.Dispose()
	font.Dispose()
	src, err := bmp.ToImage()
	if err != nil {
		return nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, px, px))
	m := float64(px) / 16 // lapka margója
	rad := float64(px) * 0.22
	x0, y0, x1, y1 := m, m, float64(px)-m, float64(px)-m
	for y := 0; y < px; y++ {
		for x := 0; x < px; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			dx := math.Max(math.Max(x0+rad-fx, fx-(x1-rad)), 0)
			dy := math.Max(math.Max(y0+rad-fy, fy-(y1-rad)), 0)
			d := math.Sqrt(dx*dx+dy*dy) - rad
			alpha := math.Min(math.Max(0.5-d, 0), 1)
			r, g, b, _ := src.At(x, y).RGBA()
			ink := 1 - float64((r+g+b)/3>>8)/255
			mix := func(c uint8) uint8 { return uint8(float64(c)*(1-ink) + 255*ink + 0.5) }
			img.SetNRGBA(x, y, color.NRGBA{mix(tile.R()), mix(tile.G()), mix(tile.B()), uint8(alpha*255 + 0.5)})
		}
	}
	ic, err := walk.NewIconFromImageForDPI(img, 192) // 2×size képpont = size logikai
	if err != nil {
		return nil
	}
	return ic
}

// tileIcon: színes lapka a közös (svg) ikonnal, pontosan a képernyő felbontásához
// rajzolva (nincs utólagos átméretezés, ezért minden DPI-n éles).
func tileIcon(name, cmd string, size, dpi int) *walk.Icon {
	if dpi < 96 {
		dpi = 96
	}
	r, g, b := tbTile(cmd)
	img := tileImage(name, color.NRGBA{r, g, b, 255}, size*dpi/96)
	ic, err := walk.NewIconFromImageForDPI(img, dpi)
	if err != nil {
		return nil
	}
	return ic
}

// cmdColor a parancs lapkájának színe (csoportonként).
func cmdColor(id string) walk.Color {
	r, g, b := tbTile(id)
	return walk.RGB(r, g, b)
}

var iconCacheTB = map[string]*walk.Icon{}

func (b tbButton) icon(size, dpi int) *walk.Icon {
	key := b.Cmd + "|" + b.Exe + "|" + b.Icon + "|" + strconv.Itoa(b.IconIdx) + "|" + strconv.Itoa(size) + "|" + strconv.Itoa(dpi)
	if ic, ok := iconCacheTB[key]; ok {
		return ic
	}
	var ic *walk.Icon
	if b.Icon != "" {
		ic, _ = walk.NewIconExtractedFromFileWithSize(expandEnv(b.Icon), b.IconIdx, size)
	}
	if ic == nil && b.Cmd != "" {
		if name, ok := tbIconFile[b.Cmd]; ok {
			ic = tileIcon(name, b.Cmd, size, dpi) // ugyanaz az ikon, mint Linuxon
		} else if c, ok := findCommand(b.Cmd); ok {
			ic = glyphIcon(c.glyph, cmdColor(c.id), size)
		}
	}
	if ic == nil && b.Exe != "" {
		exe := expandEnv(b.Exe)
		if !strings.ContainsAny(exe, `\/`) {
			if p := firstExisting(exe); p != "" {
				exe = p
			}
		}
		ic, _ = walk.NewIconExtractedFromFileWithSize(exe, 0, size)
	}
	if ic == nil {
		ic = glyphIcon("", cmdColor(""), size) // általános „indítás” jel
	}
	iconCacheTB[key] = ic
	return ic
}

func expandEnv(s string) string {
	return os.ExpandEnv(strings.ReplaceAll(s, "%SystemRoot%", "$SystemRoot"))
}

// ── felépítés és futtatás ──

func (a *App) buildToolbar() {
	if a.toolbar == nil {
		return
	}
	if a.tbAction != nil {
		a.tbAction.SetChecked(a.toolbarVisible())
	}
	acts := a.toolbar.Actions()
	acts.Clear()
	tbh := a.toolbar.Handle()
	if !a.toolbarVisible() {
		a.toolbar.SetVisible(false)
		a.relayoutMain()
		dlog("eszköztár: kikapcsolva")
		return
	}
	a.toolbar.SetVisible(true)
	size := a.toolbarIconSize()
	dpi := a.toolbar.DPI()
	if iml, err := walk.NewImageListForDPI(walk.SizeFrom96DPI(walk.Size{Width: size, Height: size}, dpi), 0, dpi); err == nil {
		old := a.toolbar.ImageList()
		a.toolbar.SetImageList(iml)
		if old != nil {
			old.Dispose()
		}
		px := walk.IntFrom96DPI(size, dpi)
		win.SendMessage(tbh, 0x0420, 0, uintptr(win.MAKELONG(uint16(px), uint16(px)))) // TB_SETBITMAPSIZE
	}
	if darkActive {
		setTheme(tbh, "DarkMode_Explorer", nil)
	} else {
		setTheme(tbh, "\x00", nil)
	}
	for _, b := range a.loadButtons() {
		b := b
		if b.Sep {
			acts.Add(walk.NewSeparatorAction())
			continue
		}
		act := walk.NewAction()
		act.SetText(b.tooltip())
		act.SetToolTip(b.tooltip())
		if ic := b.icon(size, dpi); ic != nil {
			act.SetImage(ic)
		}
		act.Triggered().Attach(func() { a.runButton(b) })
		acts.Add(act)
	}
	win.SendMessage(tbh, 0x0421, 0, 0) // TB_AUTOSIZE
	a.relayoutMain()
	dlog("eszköztár: %d elem, %d képpont", acts.Len(), size)
}

// relayoutMain: az ablak elrendezése vegye figyelembe az eszköztár (új) magasságát
func (a *App) relayoutMain() {
	var rc win.RECT
	win.GetClientRect(a.mw.Handle(), &rc)
	win.SendMessage(a.mw.Handle(), win.WM_SIZE, 0, uintptr(win.MAKELONG(uint16(rc.Right), uint16(rc.Bottom))))
}

func glyphColor() walk.Color {
	if darkActive {
		return walk.RGB(120, 180, 245)
	}
	return walk.RGB(32, 96, 168)
}

func grayGlyph() walk.Color {
	if darkActive {
		return walk.RGB(190, 190, 190)
	}
	return walk.RGB(90, 90, 90)
}

func (a *App) runButton(b tbButton) {
	if b.Cmd != "" {
		if c, ok := findCommand(b.Cmd); ok {
			c.run(a)
		}
		return
	}
	p := a.active
	dir := p.path
	if b.Dir != "" {
		dir = a.expandParams(b.Dir, false)
	}
	params := a.expandParams(b.Params, true)
	dlog("gomb: %s %s (mappa: %s)", b.Exe, params, dir)
	if err := shellRun(a.mw.Handle(), expandEnv(b.Exe), params, dir); err != nil {
		walk.MsgBox(a.mw, appName, err.Error(), walk.MsgBoxIconWarning)
	}
}

// expandParams: %P aktuális mappa, %N a kurzor alatti név, %F annak teljes útja,
// %S a kijelölt fájlok (idézőjelben, szóközzel), %T a másik panel mappája.
func (a *App) expandParams(s string, quoteAll bool) string {
	p := a.active
	cur := p.currentEntry()
	name, full := "", ""
	if cur != nil && !cur.IsParent && p.vfs == nil {
		name, full = cur.Name, cur.Path
	}
	var sel []string
	if p.vfs == nil {
		for _, e := range p.selectedEntries() {
			sel = append(sel, q(e.Path))
		}
	}
	qq := func(v string) string {
		if quoteAll && strings.ContainsRune(v, ' ') {
			return q(v)
		}
		return v
	}
	r := strings.NewReplacer("%P", qq(p.path), "%N", qq(name), "%F", qq(full), "%S", strings.Join(sel, " "),
		"%T", qq(a.other().path), "%%", "%")
	return r.Replace(s)
}

// ── szerkesztő ──

func (a *App) editToolbar() { a.editButtons("toolbar") }

// editButtons: az eszköztár ("toolbar") vagy a saját menü ("menu") szerkesztője.
func (a *App) editButtons(kind string) {
	isMenu := kind == "menu"
	buttons := a.loadButtons()
	title := T("Eszköztár szerkesztése")
	tipText := T("Elemleírás:")
	if isMenu {
		buttons = a.loadUserMenu()
		title, tipText = T("Saját menü szerkesztése"), T("Menüpont neve:")
	}
	cmds := commands()
	cmdNames := []string{T("– Külső program –")}
	for _, c := range cmds {
		cmdNames = append(cmdNames, c.label)
	}
	var dlg *walk.Dialog
	var lb *walk.ListBox
	var cmdCB *walk.ComboBox
	var exeLE, parLE, dirLE, icoLE, idxLE, tipLE *walk.LineEdit
	var okBtn, cancelBtn *walk.PushButton
	loading := false
	relabel := false // a lista feliratainak frissítése ne töltse újra a mezőket
	cur := -1

	label := func(b tbButton) string {
		if b.Sep {
			return T("──────── elválasztó ────────")
		}
		return b.tooltip()
	}
	labels := func() []string {
		out := make([]string, len(buttons))
		for i, b := range buttons {
			out[i] = label(b)
		}
		return out
	}
	enable := func(on, ext bool) {
		for _, w := range []walk.Widget{cmdCB, tipLE, icoLE, idxLE} {
			w.SetEnabled(on)
		}
		for _, w := range []walk.Widget{exeLE, parLE, dirLE} {
			w.SetEnabled(on && ext)
		}
	}
	show := func(i int) {
		cur = i
		loading = true
		defer func() { loading = false }()
		if i < 0 || i >= len(buttons) || buttons[i].Sep {
			for _, le := range []*walk.LineEdit{exeLE, parLE, dirLE, icoLE, idxLE, tipLE} {
				le.SetText("")
			}
			enable(false, false)
			return
		}
		b := buttons[i]
		idx := 0
		for k, c := range cmds {
			if c.id == b.Cmd {
				idx = k + 1
			}
		}
		cmdCB.SetCurrentIndex(idx)
		exeLE.SetText(b.Exe)
		parLE.SetText(b.Params)
		dirLE.SetText(b.Dir)
		icoLE.SetText(b.Icon)
		idxLE.SetText(strconv.Itoa(b.IconIdx))
		tipLE.SetText(b.Tip)
		enable(true, idx == 0)
	}
	store := func() {
		if loading || cur < 0 || cur >= len(buttons) || buttons[cur].Sep {
			return
		}
		b := &buttons[cur]
		if i := cmdCB.CurrentIndex(); i > 0 {
			b.Cmd = cmds[i-1].id
			b.Exe, b.Params, b.Dir = "", "", ""
		} else {
			b.Cmd = ""
			b.Exe, b.Params, b.Dir = strings.TrimSpace(exeLE.Text()), parLE.Text(), strings.TrimSpace(dirLE.Text())
		}
		b.Icon = strings.TrimSpace(icoLE.Text())
		b.IconIdx, _ = strconv.Atoi(strings.TrimSpace(idxLE.Text()))
		b.Tip = strings.TrimSpace(tipLE.Text())
		enable(true, cmdCB.CurrentIndex() == 0)
		relabel = true
		lb.SetModel(labels())
		lb.SetCurrentIndex(cur)
		relabel = false
	}
	reload := func(sel int) {
		lb.SetModel(labels())
		if sel >= len(buttons) {
			sel = len(buttons) - 1
		}
		lb.SetCurrentIndex(sel)
		show(sel)
	}
	insert := func(b tbButton) {
		i := cur + 1
		if i <= 0 || i > len(buttons) {
			i = len(buttons)
		}
		buttons = append(buttons[:i], append([]tbButton{b}, buttons[i:]...)...)
		reload(i)
	}
	swap := func(d int) {
		j := cur + d
		if cur < 0 || j < 0 || j >= len(buttons) {
			return
		}
		buttons[cur], buttons[j] = buttons[j], buttons[cur]
		reload(j)
	}
	changed := func() {
		if lb != nil && cmdCB != nil && exeLE != nil && parLE != nil && dirLE != nil && icoLE != nil && idxLE != nil && tipLE != nil {
			store()
		}
	}

	err := Dialog{
		AssignTo: &dlg, Title: title, DefaultButton: &okBtn, CancelButton: &cancelBtn,
		MinSize: Size{Width: 820, Height: 520}, Layout: VBox{},
		Children: []Widget{
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				ListBox{AssignTo: &lb, ItemStyler: newListStyler(&lb), Model: labels(), StretchFactor: 2, OnCurrentIndexChanged: func() {
					if !relabel && lb != nil && exeLE != nil && tipLE != nil && cmdCB != nil && idxLE != nil && icoLE != nil && parLE != nil && dirLE != nil {
						show(lb.CurrentIndex())
					}
				}},
				Composite{Layout: VBox{MarginsZero: true}, Children: []Widget{
					PushButton{Text: T("Új gomb"), OnClicked: func() { insert(tbButton{Cmd: "refresh"}) }},
					PushButton{Text: T("Elválasztó"), OnClicked: func() { insert(tbButton{Sep: true}) }},
					PushButton{Text: T("Törlés"), OnClicked: func() {
						if cur >= 0 && cur < len(buttons) {
							buttons = append(buttons[:cur], buttons[cur+1:]...)
							reload(cur)
						}
					}},
					PushButton{Text: T("Fel"), OnClicked: func() { swap(-1) }},
					PushButton{Text: T("Le"), OnClicked: func() { swap(1) }},
					VSpacer{},
					PushButton{Text: T("Alapértelmezés"), OnClicked: func() {
						buttons = defaultButtons()
						reload(0)
					}},
				}},
				Composite{Layout: Grid{Columns: 3}, StretchFactor: 3, Children: []Widget{
					Label{Text: T("Parancs:")},
					ComboBox{AssignTo: &cmdCB, Model: cmdNames, ColumnSpan: 2, OnCurrentIndexChanged: changed},
					Label{Text: T("Program:")},
					LineEdit{AssignTo: &exeLE, OnTextChanged: changed},
					PushButton{Text: T("Tallózás…"), OnClicked: func() {
						fd := walk.FileDialog{Title: T("Program kiválasztása"), Filter: T("Programok (*.exe;*.cmd;*.bat;*.lnk)|*.exe;*.cmd;*.bat;*.lnk|Minden fájl|*.*")}
						if ok, _ := fd.ShowOpen(dlg); ok {
							exeLE.SetText(fd.FilePath)
						}
					}},
					Label{Text: T("Paraméterek:")},
					LineEdit{AssignTo: &parLE, OnTextChanged: changed, ColumnSpan: 2},
					Label{Text: T("Kezdőmappa:")},
					LineEdit{AssignTo: &dirLE, OnTextChanged: changed, ColumnSpan: 2, CueBanner: T("üresen: az aktív panel mappája")},
					Label{Text: T("Ikonfájl:")},
					LineEdit{AssignTo: &icoLE, OnTextChanged: changed, CueBanner: T("üresen: a program saját ikonja")},
					PushButton{Text: T("Tallózás…"), OnClicked: func() {
						fd := walk.FileDialog{Title: T("Ikon kiválasztása"), Filter: T("Ikonok (*.ico;*.exe;*.dll)|*.ico;*.exe;*.dll")}
						if ok, _ := fd.ShowOpen(dlg); ok {
							icoLE.SetText(fd.FilePath)
						}
					}},
					Label{Text: T("Ikon sorszáma:")},
					LineEdit{AssignTo: &idxLE, OnTextChanged: changed, ColumnSpan: 2, MaxSize: Size{Width: 80}},
					Label{Text: tipText},
					LineEdit{AssignTo: &tipLE, OnTextChanged: changed, ColumnSpan: 2, CueBanner: T("üresen: a parancs neve")},
					Label{ColumnSpan: 3, Text: T("Paraméterekben: %P aktuális mappa, %N a kurzor alatti fájl neve, %F teljes útja,\n%S a kijelölt fájlok, %T a másik panel mappája.")},
					VSpacer{ColumnSpan: 3},
				}},
			}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &okBtn, Text: "OK", OnClicked: func() {
					if isMenu {
						a.saveUserMenu(buttons)
						a.rebuildUserMenu()
						dlg.Accept()
						return
					}
					a.saveButtons(buttons)
					for k := range iconCacheTB {
						delete(iconCacheTB, k)
					}
					a.buildToolbar()
					dlg.Accept()
				}},
				PushButton{AssignTo: &cancelBtn, Text: T("Mégse"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, appName, err.Error(), walk.MsgBoxIconError)
		return
	}
	themeDialog(dlg)
	if a.icon != nil {
		dlg.SetIcon(a.icon)
	}
	reload(0)
	dlg.Run()
}
