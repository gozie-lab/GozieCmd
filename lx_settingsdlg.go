//go:build linux

package main

// Beállítások ablak Linuxon: Megjelenés, Kezelés, Programok.

import (
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func (a *App) settingsDialog() { a.settingsDialogAt(0) }

// settingsDialogAt: a Beállítások a megadott lappal megnyitva.
func (a *App) settingsDialogAt(page int) {
	w := a.fa.NewWindow(T("Beállítások"))
	custom := a.newCustomState()
	w.SetIcon(appIconRes)

	looks := [2]look{a.lookFor(false), a.lookFor(true)}
	editDark := a.isDark()
	l := looks[boolIdx(editDark)]
	dl := defaultLook
	if editDark {
		dl = defaultDarkLook
	}

	// ── Megjelenés ──
	themeNames := []string{T("Világos"), T("Sötét"), T("Automatikus (a rendszer beállítása szerint)")}
	themeKeys := []string{"light", "dark", "auto"}
	themeSel := widget.NewSelect(themeNames, nil)
	for i, k := range themeKeys {
		if k == a.themeMode() {
			themeSel.SetSelectedIndex(i)
		}
	}
	which := widget.NewLabel("")
	whichText := func() string {
		if editDark {
			return T("Az alábbi színek és betűk a sötét témához tartoznak.")
		}
		return T("Az alábbi színek és betűk a világos témához tartoznak.")
	}
	which.SetText(whichText())

	type colorRow struct {
		ck     colorKey
		swatch *canvas.Rectangle
	}
	var rows []*colorRow
	refreshSwatches := func() {
		for _, r := range rows {
			r.swatch.FillColor = *r.ck.ptr(&l)
			r.swatch.Refresh()
		}
	}
	sizes := []string{"8", "9", "10", "11", "12", "13", "14", "16", "18"}
	listSize := widget.NewSelect(sizes, nil)
	listBold := widget.NewCheck(T("Félkövér"), nil)
	markBold := widget.NewCheck(T("Jelölt fájlok félkövérrel"), nil)
	listColor := canvas.NewRectangle(l.ListFont.Color)
	markColor := canvas.NewRectangle(l.MarkFont.Color)
	for _, sw := range []*canvas.Rectangle{listColor, markColor} {
		sw.SetMinSize(fyne.NewSize(60, 22))
		sw.StrokeWidth = 1
		sw.StrokeColor = color.Gray{128}
	}
	loadFontWidgets := func() {
		listSize.SetSelected(strconv.Itoa(l.ListFont.Size))
		listBold.SetChecked(l.ListFont.Bold)
		markBold.SetChecked(l.MarkFont.Bold)
		listColor.FillColor = l.ListFont.Color
		markColor.FillColor = l.MarkFont.Color
		listColor.Refresh()
		markColor.Refresh()
	}
	listSize.OnChanged = func(s string) {
		if n, err := strconv.Atoi(s); err == nil {
			l.ListFont.Size, l.MarkFont.Size = n, n
		}
	}
	listBold.OnChanged = func(b bool) { l.ListFont.Bold = b }
	markBold.OnChanged = func(b bool) { l.MarkFont.Bold = b }
	pick := func(title string, cur color.NRGBA, set func(color.NRGBA)) {
		d := dialog.NewColorPicker(title, "", func(c color.Color) {
			r, g, b, _ := c.RGBA()
			set(rgb(uint8(r>>8), uint8(g>>8), uint8(b>>8)))
		}, w)
		d.Advanced = true
		d.SetColor(cur)
		d.Show()
	}
	fontGrid := container.New(layout.NewFormLayout(),
		widget.NewLabel(T("Lista betűmérete (pont):")), container.NewHBox(listSize, listBold),
		widget.NewLabel(T("Fájlok színe:")), container.NewHBox(listColor, widget.NewButton(T("Módosítás…"), func() {
			pick(T("Fájlok színe"), l.ListFont.Color, func(c color.NRGBA) { l.ListFont.Color = c; listColor.FillColor = c; listColor.Refresh() })
		}), widget.NewButton(T("Alapérték"), func() { l.ListFont.Color = dl.ListFont.Color; loadFontWidgets() })),
		widget.NewLabel(T("Jelölt fájlok színe:")), container.NewHBox(markColor, widget.NewButton(T("Módosítás…"), func() {
			pick(T("Jelölt fájlok színe"), l.MarkFont.Color, func(c color.NRGBA) { l.MarkFont.Color = c; markColor.FillColor = c; markColor.Refresh() })
		}), widget.NewButton(T("Alapérték"), func() { l.MarkFont.Color = dl.MarkFont.Color; loadFontWidgets() }), markBold),
	)
	colorGrid := container.New(layout.NewFormLayout())
	for _, ck := range colorKeys {
		ck := ck
		r := &colorRow{ck: ck, swatch: canvas.NewRectangle(*ck.ptr(&l))}
		r.swatch.SetMinSize(fyne.NewSize(60, 22))
		r.swatch.StrokeWidth = 1
		r.swatch.StrokeColor = color.Gray{128}
		rows = append(rows, r)
		colorGrid.Add(widget.NewLabel(ck.label + ":"))
		colorGrid.Add(container.NewHBox(r.swatch,
			widget.NewButton(T("Módosítás…"), func() {
				pick(ck.label, *ck.ptr(&l), func(c color.NRGBA) { *ck.ptr(&l) = c; refreshSwatches() })
			}),
			widget.NewButton(T("Alapérték"), func() { *ck.ptr(&l) = *ck.ptr(&dl); refreshSwatches() })))
	}
	loadFontWidgets()
	themeSel.OnChanged = func(string) {
		i := themeSel.SelectedIndex()
		want := i == 1 || (i == 2 && a.fa.Settings().ThemeVariant() == theme.VariantDark)
		if want == editDark {
			return
		}
		looks[boolIdx(editDark)] = l
		editDark = want
		l = looks[boolIdx(editDark)]
		dl = defaultLook
		if editDark {
			dl = defaultDarkLook
		}
		which.SetText(whichText())
		refreshSwatches()
		loadFontWidgets()
	}
	scaleNames := []string{"80%", "90%", "100%", "110%", "125%", "150%", "175%"}
	scaleVals := []int{80, 90, 100, 110, 125, 150, 175}
	scaleSel := widget.NewSelect(scaleNames, nil)
	scaleSel.SetSelectedIndex(2)
	for i, v := range scaleVals {
		if v == a.settingInt("lx.uiScale", 100) {
			scaleSel.SetSelectedIndex(i)
		}
	}
	lookTab := container.NewVScroll(container.NewVBox(
		container.New(layout.NewFormLayout(), widget.NewLabel(T("Téma:")), themeSel,
			widget.NewLabel(T("Felület mérete:")), scaleSel),
		which,
		widget.NewCard("", T("Betűk"), fontGrid),
		widget.NewCard("", T("Színek"), colorGrid),
	))

	// ── Kezelés ──
	tbShow := widget.NewCheck(T("Eszköztár megjelenítése"), nil)
	tbShow.SetChecked(a.toolbarVisible())
	var iconNames []string
	for _, s := range iconSizes {
		iconNames = append(iconNames, fmt.Sprintf(T("%s képpont"), strconv.Itoa(s)))
	}
	iconSel := widget.NewSelect(iconNames, nil)
	for i, s := range iconSizes {
		if float32(s) == a.toolbarIconSize() {
			iconSel.SetSelectedIndex(i)
		}
	}
	rightSel := widget.NewCheck(T("Kijelölés jobb egérgombbal (a gombot nyomva tartva 1 mp után helyi menü)"), nil)
	rightSel.SetChecked(a.rightSelect())
	typeSearch := widget.NewCheck(T("Gépelés a listában: gyorskeresés (kikapcsolva a parancssorba kerül; gyorskeresés mindig: Ctrl+S)"), nil)
	typeSearch.SetChecked(a.typingSearches())
	idleSel := widget.NewSelect(idleTimeoutLabels(), nil)
	idleSel.SetSelectedIndex(idleTimeoutIndex(a.idleMinutes()))
	langList, langCodes, langCur := langNames()
	langSel := widget.NewSelect(langList, nil)
	langSel.SetSelectedIndex(langCur)
	langTpl := widget.NewButton(T("Fordítási sablon…"), func() {
		p, err := saveLangTemplate()
		if err != nil {
			a.warn(errText(err))
			return
		}
		dialog.ShowInformation(appName, langTemplateHelp(p), w)
	})
	updChk := widget.NewCheck(T("Új verzió keresése induláskor (naponta egyszer)"), nil)
	updChk.SetChecked(a.updateCheckOn())
	var updCard fyne.CanvasObject = widget.NewCard("", T("Programfrissítés"), container.NewHBox(updChk, layout.NewSpacer(),
		widget.NewButton(T("Keresés most"), func() { a.checkUpdate(true) })))
	if !updatesAllowed() {
		updCard = widget.NewCard("", T("Programfrissítés"), widget.NewLabel(T("A Flatpak-változatot a Flatpak frissíti.")))
	}
	portable := widget.NewCheck(T("Hordozható mód: a beállítások a program mellett, a GozieCmd.ini fájlban"), nil)
	portable.SetChecked(a.portable)
	fileLbl := widget.NewLabel(a.settings.FilePath())
	fileLbl.Truncation = fyne.TextTruncateEllipsis
	manageTab := container.NewVBox(
		widget.NewCard("", "Nyelv / Language", container.NewHBox(sized(langSel, 180),
			widget.NewLabel(T("Újraindítás után érvényes.")), layout.NewSpacer(), langTpl)),
		widget.NewCard("", T("Eszköztár"), container.NewVBox(tbShow,
			container.NewHBox(widget.NewLabel(T("Ikonméret:")), iconSel, widget.NewButton(T("Gombok szerkesztése…"), a.editToolbar)))),
		widget.NewCard("", T("Billentyűzet"), container.NewVBox(typeSearch)),
		widget.NewCard("", T("Egér"), container.NewVBox(rightSel,
			widget.NewLabel(T("Bekapcsolva a jobb gomb jelöl – a klasszikus kétpaneles fájlkezelők módján.")))),
		widget.NewCard("", T("Távoli kapcsolatok (FTP, SFTP, WebDAV, SMB)"), container.NewHBox(
			widget.NewLabel(T("Tétlenség esetén a kapcsolat bontása:")), sized(idleSel, 160))),
		updCard,
		widget.NewCard("", T("Beállításfájl"), container.NewVBox(portable,
			container.NewBorder(nil, nil, widget.NewLabel(T("Jelenlegi fájl:")), nil, fileLbl),
			container.NewHBox(
				widget.NewButton(T("Mentés fájlba…"), func() { a.exportSettings(w) }),
				widget.NewButton(T("Betöltés fájlból…"), func() { a.importSettings(w) })),
			widget.NewLabel(T("A fájl a Windows-változattal is cserélhető (ugyanaz a formátum).")))),
	)

	// ── Programok ──
	editors := detectEditors()
	edNames := []string{}
	for _, e := range editors {
		edNames = append(edNames, e.label)
	}
	edNames = append(edNames, T("Egyéni…"))
	edEntry := widget.NewEntry()
	edEntry.SetText(a.editorPath())
	edSel := widget.NewSelect(edNames, func(s string) {
		for _, e := range editors {
			if e.label == s {
				edEntry.SetText(e.path)
			}
		}
	})
	for _, e := range editors {
		if e.path == a.editorPath() {
			edSel.SetSelected(e.label)
		}
	}
	if edSel.Selected == "" {
		edSel.SetSelected(T("Egyéni…"))
	}
	terms := detectTerminals()
	var tNames []string
	for _, t := range terms {
		tNames = append(tNames, t.label)
	}
	tEntry := widget.NewEntry()
	tEntry.SetText(a.terminalPath())
	tSel := widget.NewSelect(append(tNames, T("Egyéni…")), func(s string) {
		for _, t := range terms {
			if t.label == s {
				tEntry.SetText(t.path)
			}
		}
	})
	for _, t := range terms {
		if t.path == a.terminalPath() {
			tSel.SetSelected(t.label)
		}
	}
	sevenE := widget.NewEntry()
	sevenE.SetText(a.setting("lx.sevenZip", ""))
	sevenE.SetPlaceHolder(fmt.Sprintf(T("%s  (automatikus)"), findExe("7zz", "7z", "7za")))
	progTab := container.NewVBox(
		widget.NewCard("", T("Szövegszerkesztő (F4)"), container.New(layout.NewFormLayout(),
			widget.NewLabel(T("Program:")), edSel, widget.NewLabel(T("Parancs:")), edEntry)),
		widget.NewCard("", T("Terminál (F9, és Shift+Enter a parancssorban)"), container.New(layout.NewFormLayout(),
			widget.NewLabel(T("Program:")), tSel, widget.NewLabel(T("Parancs:")), tEntry)),
		widget.NewCard("", T("7-Zip (7Z írás; CAB, ARJ, LZH olvasás)"), container.New(layout.NewFormLayout(),
			widget.NewLabel(T("7z program:")), sevenE)),
	)

	tabs := container.NewAppTabs(
		container.NewTabItem(T("Kezelés"), manageTab),
		container.NewTabItem(T("Megjelenés"), lookTab),
		container.NewTabItem(T("Billentyűk, színek"), a.customPage(w, custom)),
		container.NewTabItem(T("Programok"), progTab),
	)
	if page > 0 && page < len(tabs.Items) {
		tabs.SelectIndex(page)
	}
	okB := widget.NewButton("OK", func() {
		looks[boolIdx(editDark)] = l
		a.saveLook(looks[0], false)
		a.saveLook(looks[1], true)
		a.settings.Put("theme", themeKeys[maxIdx(themeSel.SelectedIndex())])
		a.settings.Put("lx.uiScale", strconv.Itoa(scaleVals[maxIdx(scaleSel.SelectedIndex())]))
		a.uiScale = float32(scaleVals[maxIdx(scaleSel.SelectedIndex())]) / 100
		a.settings.Put("toolbar.show", boolStr(tbShow.Checked))
		a.settings.Put("toolbar.iconSize", strconv.Itoa(iconSizes[maxIdx(iconSel.SelectedIndex())]))
		a.settings.Put("rightSelect", boolStr(rightSel.Checked))
		a.settings.Put("quickSearchTyping", boolStr(typeSearch.Checked))
		a.settings.Put("remoteIdle", strconv.Itoa(idleTimeouts[maxIdx(idleSel.SelectedIndex())]))
		a.settings.Put("updateCheck", boolStr(updChk.Checked))
		langChanged := false
		if i := langSel.SelectedIndex(); i >= 0 && langCodes[i] != curLang {
			a.settings.Put("lang", langCodes[i])
			langChanged = true
		}
		a.settings.Put("lx.editor", edEntry.Text)
		a.settings.Put("lx.terminal", tEntry.Text)
		a.settings.Put("lx.sevenZip", sevenE.Text)
		if err := a.settings.Save(); err != nil {
			a.warn(fmt.Sprintf(T("A beállítások nem menthetők:\n%s"), errText(err)))
		}
		if portable.Checked != a.portable {
			a.setPortable(portable.Checked)
		}
		custom.save(a)
		a.settings.Save()
		a.applyTheme()
		custom.apply(a)
		w.Close()
		if langChanged {
			a.warn(T("A nyelv a program következő indításakor vált.\n\nThe language changes the next time the program starts."))
		}
	})
	okB.Importance = widget.HighImportance
	buttons := container.NewHBox(layout.NewSpacer(), okB, widget.NewButton(T("Mégse"), w.Close))
	w.SetContent(container.NewBorder(nil, buttons, nil, nil, tabs))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyEscape {
			w.Close()
		}
	})
	a.restoreWinSize(w, "settings", 760, 640)
	w.SetOnClosed(func() { a.saveWinSize(w, "settings"); a.active.focus() })
	w.Show()
}

func boolIdx(b bool) int {
	if b {
		return 1
	}
	return 0
}

func maxIdx(i int) int {
	if i < 0 {
		return 0
	}
	return i
}

// ── beállításfájl: hordozható mód, mentés, betöltés ──

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func (a *App) setPortable(on bool) {
	a.settings.Save()
	from := a.settings.FilePath()
	if on {
		to := filepath.Join(exeDir(), portableFile)
		if err := copyFile(from, to); err != nil {
			a.warn(fmt.Sprintf(T("A beállításfájl nem írható a program mappájába:\n%s\n\n%s"), to, errText(err)))
			return
		}
		a.settings = newIniSettings(to)
		a.settings.Load()
		a.portable = true
		return
	}
	to := filepath.Join(configDir(), "settings.ini")
	if err := copyFile(from, to); err != nil {
		a.warn(fmt.Sprintf(T("A beállítások nem másolhatók vissza:\n%s"), errText(err)))
		return
	}
	os.Remove(from)
	a.settings = newIniSettings(to)
	a.settings.Load()
	a.portable = false
}

func (a *App) exportSettings(parent fyne.Window) {
	a.saveState()
	d := dialog.NewFileSave(func(uc fyne.URIWriteCloser, err error) {
		if uc == nil || err != nil {
			return
		}
		defer uc.Close()
		src, err := os.Open(a.settings.FilePath())
		if err != nil {
			a.warn(errText(err))
			return
		}
		defer src.Close()
		if _, err := io.Copy(uc, src); err != nil {
			a.warn(fmt.Sprintf(T("A mentés nem sikerült:\n%s"), errText(err)))
			return
		}
		dialog.ShowInformation(appName, fmt.Sprintf(T("A beállítások elmentve:\n%s\n\nMásik gépen (Linuxon vagy Windowson) a Beállítások → Kezelés → „Betöltés fájlból…” gombbal töltheted be."), uc.URI().Path()), parent)
	}, parent)
	d.SetFileName(portableFile)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".ini"}))
	d.Resize(fyne.NewSize(760, 520))
	d.Show()
}

func (a *App) importSettings(parent fyne.Window) {
	d := dialog.NewFileOpen(func(uc fyne.URIReadCloser, err error) {
		if uc == nil || err != nil {
			return
		}
		path := uc.URI().Path()
		uc.Close()
		probe := newIniSettings(path)
		if err := probe.Load(); err != nil {
			a.warn(fmt.Sprintf(T("Ez nem érvényes beállításfájl:\n%s"), errText(err)))
			return
		}
		if err := copyFile(path, a.settings.FilePath()); err != nil {
			a.warn(fmt.Sprintf(T("A betöltés nem sikerült:\n%s"), errText(err)))
			return
		}
		a.settings.Load()
		parent.Close()
		a.showHidden = a.settingBool("showHidden", false)
		a.applyTheme()
		a.refreshAll()
		dialog.ShowInformation(appName, T("A beállítások betöltve.\nAz ablak mérete és a panelek mappái a következő indításkor érvényesülnek."), a.win)
	}, parent)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".ini"}))
	d.Resize(fyne.NewSize(760, 520))
	d.Show()
}
