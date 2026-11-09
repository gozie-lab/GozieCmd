//go:build linux

package main

// Testreszabás (Linux): saját billentyűk, saját menü, fájltípus-színezés.

import (
	"encoding/json"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ── billentyűk ──

var fyneKeyNames = map[fyne.KeyName]string{
	fyne.KeyInsert: "Insert", fyne.KeyDelete: "Delete", fyne.KeyHome: "Home", fyne.KeyEnd: "End",
	fyne.KeyPageUp: "PageUp", fyne.KeyPageDown: "PageDown", fyne.KeyUp: "Up", fyne.KeyDown: "Down",
	fyne.KeyLeft: "Left", fyne.KeyRight: "Right", fyne.KeySpace: "Space", fyne.KeyReturn: "Enter",
	fyne.KeyEnter: "Enter", fyne.KeyBackspace: "Backspace", fyne.KeyTab: "Tab",
}

func fyneToKey(n fyne.KeyName) string {
	if s, ok := fyneKeyNames[n]; ok {
		return s
	}
	s := string(n)
	if len(s) == 1 && (s[0] >= 'A' && s[0] <= 'Z' || s[0] >= '0' && s[0] <= '9') {
		return s
	}
	if len(s) >= 2 && s[0] == 'F' {
		for i := 1; i <= 12; i++ {
			if s == "F"+strconv.Itoa(i) {
				return s
			}
		}
	}
	return ""
}

func comboOf(n fyne.KeyName, m fyne.KeyModifier) keyCombo {
	return keyCombo{Ctrl: m&fyne.KeyModifierControl != 0, Alt: m&fyne.KeyModifierAlt != 0,
		Shift: m&fyne.KeyModifierShift != 0, Key: fyneToKey(n)}
}

// shortcutCombo: a Fyne gyorsbillentyűje szöveges alakban.
func shortcutCombo(s fyne.Shortcut) (keyCombo, bool) {
	switch x := s.(type) {
	case *desktop.CustomShortcut:
		k := comboOf(x.KeyName, x.Modifier)
		return k, k.Key != ""
	case *fyne.ShortcutSelectAll:
		return keyCombo{Ctrl: true, Key: "A"}, true
	case *fyne.ShortcutCopy:
		return keyCombo{Ctrl: true, Key: "C"}, true
	case *fyne.ShortcutPaste:
		return keyCombo{Ctrl: true, Key: "V"}, true
	case *fyne.ShortcutCut:
		return keyCombo{Ctrl: true, Key: "X"}, true
	case *fyne.ShortcutUndo:
		return keyCombo{Ctrl: true, Key: "Z"}, true
	case *fyne.ShortcutRedo:
		return keyCombo{Ctrl: true, Key: "Y"}, true
	}
	return keyCombo{}, false
}

func currentMods() fyne.KeyModifier {
	if d, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok {
		return d.CurrentKeyModifiers()
	}
	return 0
}

func (a *App) userKeys() []keyBinding {
	v, _ := a.settings.Get("keys")
	return decodeKeys(v)
}

func (a *App) applyUserKeys() {
	a.keyMap = map[string]string{}
	for _, b := range a.userKeys() {
		a.keyMap[b.Key] = b.Cmd
	}
	a.win.SetMainMenu(a.menu())
}

// userKey: ha a billentyűhöz saját parancs tartozik, lefuttatja (igaz: el is nyelte).
func (a *App) userKey(k keyCombo) bool {
	if k.Key == "" || a.keyMap == nil {
		return false
	}
	id, ok := a.keyMap[k.String()]
	if !ok {
		return false
	}
	if id != "-" {
		if c, ok := findCommand(id); ok {
			c.run(a)
		}
	}
	return true
}

func commandLabel(id string) string {
	if id == "-" {
		return T("(semmi – a billentyű kikapcsolva)")
	}
	if c, ok := findCommand(id); ok {
		return c.label
	}
	return id
}

func sortedCommands() ([]string, []string) {
	cs := commands()
	sort.Slice(cs, func(i, j int) bool { return strings.ToLower(cs[i].label) < strings.ToLower(cs[j].label) })
	ids, labels := []string{"-"}, []string{commandLabel("-")}
	for _, c := range cs {
		l := c.label
		if d := defaultKeyFor(c.id); d != "" {
			l += "   (" + d + ")"
		}
		ids = append(ids, c.id)
		labels = append(labels, l)
	}
	return ids, labels
}

// keyCatch: fókuszálható mező, amely a lenyomott kombinációt rögzíti.
type keyCatch struct {
	widget.BaseWidget
	text   *canvas.Text
	border *canvas.Rectangle
	on     func(keyCombo)
}

func newKeyCatch(initial string, on func(keyCombo)) *keyCatch {
	k := &keyCatch{on: on, text: canvas.NewText(initial, theme.Color(theme.ColorNameForeground)),
		border: canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))}
	if initial == "" {
		k.text.Text = T("kattints ide, és nyomd le a billentyűt")
		k.text.Color = theme.Color(theme.ColorNamePlaceHolder)
	}
	k.text.TextSize = theme.TextSize() * 1.15
	k.text.TextStyle.Bold = true
	k.border.StrokeWidth = 1.5
	k.border.CornerRadius = 4
	k.border.StrokeColor = theme.Color(theme.ColorNameInputBorder)
	k.ExtendBaseWidget(k)
	return k
}

func (k *keyCatch) set(c keyCombo) {
	k.text.Text = c.String()
	k.text.Color = theme.Color(theme.ColorNameForeground)
	k.text.Refresh()
	if k.on != nil {
		k.on(c)
	}
}

func (k *keyCatch) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(k.border, container.NewPadded(k.text)))
}
func (k *keyCatch) MinSize() fyne.Size { return fyne.NewSize(300, theme.TextSize()*2.6) }
func (k *keyCatch) Tapped(*fyne.PointEvent) {
	if c := fyne.CurrentApp().Driver().CanvasForObject(k); c != nil {
		c.Focus(k)
	}
}
func (k *keyCatch) FocusGained() {
	k.border.StrokeColor = theme.Color(theme.ColorNamePrimary)
	k.border.Refresh()
}
func (k *keyCatch) FocusLost() {
	k.border.StrokeColor = theme.Color(theme.ColorNameInputBorder)
	k.border.Refresh()
}
func (k *keyCatch) TypedRune(rune) {}
func (k *keyCatch) TypedKey(ev *fyne.KeyEvent) {
	if c := comboOf(ev.Name, currentMods()); c.Key != "" {
		k.set(c)
	}
}
func (k *keyCatch) TypedShortcut(s fyne.Shortcut) {
	if c, ok := shortcutCombo(s); ok {
		k.set(c)
	}
}
func (k *keyCatch) AcceptsTab() bool { return true }

func (a *App) bindingDialog(parent fyne.Window, b keyBinding, others []keyBinding, done func(keyBinding)) {
	ids, labels := sortedCommands()
	cmdS := widget.NewSelect(labels, nil)
	cur := 0
	for i, id := range ids {
		if id == b.Cmd {
			cur = i
		}
	}
	cmdS.SetSelectedIndex(cur)
	note := widget.NewLabel("")
	note.Wrapping = fyne.TextWrapWord
	combo, _ := parseCombo(b.Key)
	describe := func() {
		msg := ""
		if u, why := combo.usable(); !u {
			msg = why
		} else {
			if d, ok := defaultKeys[combo.String()]; ok {
				msg = fmt.Sprintf(T("Most ez a billentyű: %s. A saját beállításod felülírja."), commandLabel(d))
			}
			for _, o := range others {
				if o.Key == combo.String() {
					msg = fmt.Sprintf(T("Ez a billentyű már be van állítva: %s. Mentéskor lecseréled."), commandLabel(o.Cmd))
				}
			}
		}
		note.SetText(msg)
	}
	kc := newKeyCatch(b.Key, func(c keyCombo) { combo = c; describe() })
	content := container.NewVBox(
		widget.NewForm(widget.NewFormItem(T("Billentyű"), kc), widget.NewFormItem(T("Parancs"), cmdS)),
		note)
	d := dialog.NewCustomConfirm(T("Billentyűparancs"), "OK", T("Mégse"), content, func(ok bool) {
		if !ok {
			return
		}
		if u, why := combo.usable(); !u {
			dialog.ShowInformation(appName, why, parent)
			return
		}
		done(keyBinding{Key: combo.String(), Cmd: ids[maxIdx(cmdS.SelectedIndex())]})
	}, parent)
	d.Resize(fyne.NewSize(620, 0))
	d.Show()
	parent.Canvas().Focus(kc)
	if b.Key == "" {
		note.SetText(T("Kattints a Billentyű mezőbe, és nyomd le a kívánt kombinációt (pl. Ctrl+Shift+E)."))
	} else {
		describe()
	}
}

// ── saját menü ──

func (a *App) loadUserMenu() []tbButton {
	v, _ := a.settings.Get("lx.userMenu")
	var b []tbButton
	if v != "" {
		json.Unmarshal([]byte(v), &b)
	}
	return b
}

func (a *App) saveUserMenu(b []tbButton) {
	data, _ := json.Marshal(b)
	a.settings.Put("lx.userMenu", string(data))
	a.settings.Save()
}

func (a *App) rebuildUserMenu() { a.win.SetMainMenu(a.menu()) }

func (a *App) editUserMenu() { a.editButtons("menu") }

// userMenuItems: a „Saját” menü elemei.
func (a *App) userMenuItems() []*fyne.MenuItem {
	var items []*fyne.MenuItem
	list := a.loadUserMenu()
	for _, b := range list {
		if b.Sep {
			items = append(items, fyne.NewMenuItemSeparator())
			continue
		}
		b := b
		items = append(items, fyne.NewMenuItem(b.tooltip(), func() { a.runButton(b); a.active.focus() }))
	}
	if len(list) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
	}
	return append(items, fyne.NewMenuItem(T("Saját menü szerkesztése…"), a.editUserMenu))
}

func (a *App) userMenuPopup() {
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", a.userMenuItems()...), a.win.Canvas(), a.active.cursorScreenPos())
}

// ── fájltípus-színezés ──

func (a *App) loadTypeColors() {
	v, _ := a.settings.Get("typeColors")
	on, ok := a.settings.Get("typeColors.on")
	a.typeCol = newTypeColors(!ok || on != "0", decodeTypeRules(v))
}

func rgbNRGBA(c uint32) color.NRGBA {
	return color.NRGBA{uint8(c >> 16), uint8(c >> 8), uint8(c), 255}
}

func (a *App) typeColorOf(e *Entry) (color.Color, bool) {
	if a.typeCol == nil || e.IsParent {
		return nil, false
	}
	c, ok := a.typeCol.colorFor(e.Name, e.IsDir, a.isDark())
	if !ok {
		return nil, false
	}
	return rgbNRGBA(c), true
}

func (a *App) ruleDialog(parent fyne.Window, r typeRule, done func(typeRule)) {
	name, masks := widget.NewEntry(), widget.NewEntry()
	name.SetText(r.Name)
	masks.SetText(r.Masks)
	masks.SetPlaceHolder(T("pl. *.jpg;*.png"))
	col := r.Color
	sample := canvas.NewText(T("Minta.jpg"), rgbNRGBA(col))
	sample.TextStyle.Bold = true
	sample.TextSize = theme.TextSize() * 1.3
	pick := widget.NewButton(T("Szín választása…"), func() {
		p := dialog.NewColorPicker(T("Szín"), "", func(c color.Color) {
			n := color.NRGBAModel.Convert(c).(color.NRGBA)
			col = uint32(n.R)<<16 | uint32(n.G)<<8 | uint32(n.B)
			sample.Color = rgbNRGBA(col)
			sample.Refresh()
		}, parent)
		p.Advanced = true
		p.SetColor(rgbNRGBA(col))
		p.Show()
	})
	hint := widget.NewLabel(T("Több minta pontosvesszővel. Sötét témában a program a sötét színeket magától világosítja."))
	hint.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(widget.NewForm(widget.NewFormItem(T("Megnevezés"), name), widget.NewFormItem(T("Minták"), masks),
		widget.NewFormItem(T("Szín"), container.NewHBox(sample, pick))), hint)
	d := dialog.NewCustomConfirm(T("Fájltípus színe"), "OK", T("Mégse"), content, func(ok bool) {
		if !ok || strings.TrimSpace(masks.Text) == "" {
			return
		}
		n := strings.TrimSpace(name.Text)
		if n == "" {
			n = strings.TrimSpace(masks.Text)
		}
		done(typeRule{Name: n, Masks: strings.TrimSpace(masks.Text), Color: col})
	}, parent)
	d.Resize(fyne.NewSize(560, 0))
	d.Show()
}

// A billentyűk és a fájlszínek a Beállítások „Billentyűk, színek” lapján szerkeszthetők.
func (a *App) editKeys()       { a.settingsDialogAt(2) }
func (a *App) editTypeColors() { a.settingsDialogAt(2) }

// customState: a lap szerkesztett adatai (mentés az OK-ra).
type customState struct {
	binds []keyBinding
	rules []typeRule
	onC   *widget.Check
}

func (a *App) newCustomState() *customState {
	v, _ := a.settings.Get("typeColors")
	st := &customState{binds: a.userKeys(), rules: decodeTypeRules(v)}
	sort.Slice(st.binds, func(i, j int) bool { return st.binds[i].Key < st.binds[j].Key })
	st.onC = widget.NewCheck(T("Fájlok színezése típus szerint"), nil)
	st.onC.SetChecked(a.typeCol == nil || a.typeCol.on)
	return st
}

func (st *customState) save(a *App) {
	a.settings.Put("keys", encodeKeys(st.binds))
	a.settings.Put("typeColors", encodeTypeRules(st.rules))
	a.settings.Put("typeColors.on", boolStr(st.onC.Checked))
}

func (st *customState) apply(a *App) {
	a.loadTypeColors()
	a.applyUserKeys()
	a.left.view.refreshAll()
	a.right.view.refreshAll()
}

// customPage: a „Billentyűk, színek” lap.
func (a *App) customPage(w fyne.Window, st *customState) fyne.CanvasObject {
	// ── billentyűk ──
	keySel := -1
	var keyList *widget.List
	keyList = widget.NewList(func() int { return len(st.binds) },
		func() fyne.CanvasObject { return newToolRow([]float32{150, -1}, 99, false) },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			row := o.(*toolRow)
			row.setTexts(st.binds[i].Key, commandLabel(st.binds[i].Cmd))
			row.cells[0].TextStyle.Bold = true
			row.setSelected(i == keySel)
			row.onTap = func() { keySel = i; keyList.Refresh() }
			row.onDouble = func() { keySel = i; editKeyAt(a, w, st, keyList, i) }
			row.Refresh()
		})
	var defaults []string
	for k, c := range defaultKeys {
		defaults = append(defaults, k+"   "+commandLabel(c))
	}
	sort.Strings(defaults)
	keyBtns := container.NewVBox(
		widget.NewButton(T("Új…"), func() { editKeyAt(a, w, st, keyList, -1) }),
		widget.NewButton(T("Módosítás…"), func() {
			if keySel >= 0 {
				editKeyAt(a, w, st, keyList, keySel)
			}
		}),
		widget.NewButton(T("Törlés"), func() {
			if keySel >= 0 && keySel < len(st.binds) {
				st.binds = append(st.binds[:keySel], st.binds[keySel+1:]...)
				keySel = -1
				keyList.Refresh()
			}
		}),
		widget.NewButton(T("Beépített billentyűk…"), func() {
			dialog.ShowInformation(T("Beépített billentyűk (a legfontosabbak)"), strings.Join(defaults, "\n"), w)
		}),
	)
	keyHead := widget.NewLabel(T("Saját billentyűk. Ezek a beépítettek elé kerülnek: egy beépített billentyű így másik parancsra állítható vagy kikapcsolható, és bármely parancs (a menük, az eszköztár parancsai) kaphat billentyűt."))
	keyHead.Wrapping = fyne.TextWrapWord
	keyCard := widget.NewCard("", T("Billentyűparancsok"), container.NewBorder(keyHead, nil, nil, keyBtns, keyList))

	// ── fájltípusok ──
	sel := -1
	var list *widget.List
	list = widget.NewList(func() int { return len(st.rules) },
		func() fyne.CanvasObject { return newToolRow([]float32{190, -1}, 99, false) },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			row := o.(*toolRow)
			row.setTexts(st.rules[i].Name, st.rules[i].Masks)
			c := st.rules[i].Color
			if a.isDark() {
				c = lightenForDark(c)
			}
			row.cells[0].Color = rgbNRGBA(c)
			row.cells[0].TextStyle.Bold = true
			row.setSelected(i == sel)
			row.onTap = func() { sel = i; list.Refresh() }
			row.onDouble = func() {
				sel = i
				a.ruleDialog(w, st.rules[i], func(r typeRule) { st.rules[i] = r; list.Refresh() })
			}
			row.Refresh()
		})
	move := func(d int) {
		j := sel + d
		if sel < 0 || j < 0 || j >= len(st.rules) {
			return
		}
		st.rules[sel], st.rules[j] = st.rules[j], st.rules[sel]
		sel = j
		list.Refresh()
	}
	ruleBtns := container.NewVBox(
		widget.NewButton(T("Új…"), func() {
			a.ruleDialog(w, typeRule{Color: 0x2060C0}, func(r typeRule) {
				st.rules = append(st.rules, r)
				sel = len(st.rules) - 1
				list.Refresh()
			})
		}),
		widget.NewButton(T("Módosítás…"), func() {
			if i := sel; i >= 0 && i < len(st.rules) {
				a.ruleDialog(w, st.rules[i], func(r typeRule) { st.rules[i] = r; list.Refresh() })
			}
		}),
		widget.NewButton(T("Törlés"), func() {
			if sel >= 0 && sel < len(st.rules) {
				st.rules = append(st.rules[:sel], st.rules[sel+1:]...)
				sel = -1
				list.Refresh()
			}
		}),
		container.NewGridWithColumns(2, widget.NewButton(T("Fel"), func() { move(-1) }), widget.NewButton(T("Le"), func() { move(1) })),
		widget.NewButton(T("Alapértelmezés"), func() {
			st.rules = append([]typeRule(nil), defaultTypeRules...)
			sel = -1
			list.Refresh()
		}),
	)
	note := widget.NewLabel(T("Ha egy fájlra több szabály is illik, a listában előrébb álló érvényes. A mappák, a kurzor és a jelölt fájlok saját színűek maradnak."))
	note.Wrapping = fyne.TextWrapWord
	typeCard := widget.NewCard("", T("Fájltípusok színezése"), container.NewBorder(st.onC, note, nil, ruleBtns, list))

	userMenu := container.NewHBox(widget.NewButton(T("Saját menü szerkesztése…"), a.editUserMenu))
	return container.NewBorder(nil, userMenu, nil, nil, container.NewGridWithRows(2, keyCard, typeCard))
}

func editKeyAt(a *App, w fyne.Window, st *customState, l *widget.List, i int) {
	b := keyBinding{}
	if i >= 0 && i < len(st.binds) {
		b = st.binds[i]
	}
	var others []keyBinding
	for j, o := range st.binds {
		if j != i {
			others = append(others, o)
		}
	}
	a.bindingDialog(w, b, others, func(nb keyBinding) {
		var out []keyBinding
		for _, o := range others {
			if o.Key != nb.Key {
				out = append(out, o)
			}
		}
		st.binds = append(out, nb)
		sort.Slice(st.binds, func(i, j int) bool { return st.binds[i].Key < st.binds[j].Key })
		l.Refresh()
	})
}
