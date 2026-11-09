//go:build windows

package main

// Testreszabás (Windows): saját billentyűk, saját menü, fájltípus-színezés.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// ── billentyűk ──

var vkNames = map[uint32]string{0x2D: "Insert", 0x2E: "Delete", 0x24: "Home", 0x23: "End", 0x21: "PageUp", 0x22: "PageDown",
	0x25: "Left", 0x26: "Up", 0x27: "Right", 0x28: "Down", 0x20: "Space", 0x0D: "Enter", 0x08: "Backspace", 0x09: "Tab"}

func vkToKey(vk uint32) string {
	switch {
	case vk >= 0x41 && vk <= 0x5A, vk >= 0x30 && vk <= 0x39:
		return string(rune(vk))
	case vk >= 0x70 && vk <= 0x7B:
		return "F" + strconv.Itoa(int(vk-0x6F))
	}
	return vkNames[vk]
}

func keyToVK(k string) uint32 {
	if len(k) == 1 {
		return uint32(k[0])
	}
	if strings.HasPrefix(k, "F") {
		var n int
		fmt.Sscanf(k[1:], "%d", &n)
		if n >= 1 && n <= 12 {
			return uint32(0x6F + n)
		}
	}
	for vk, n := range vkNames {
		if n == k {
			return vk
		}
	}
	return 0
}

func (a *App) userKeys() []keyBinding {
	v, _ := a.settings.Get("keys")
	return decodeKeys(v)
}

func (a *App) runCommand(id string) {
	if id == "-" {
		return
	}
	if c, ok := findCommand(id); ok {
		c.run(a)
	}
}

// applyUserKeys: a saját billentyűk az ablak gyorsbillentyű-listájának elejére kerülnek,
// így megelőzik a menük és a lista beépített billentyűit.
func (a *App) applyUserKeys() {
	sa := a.mw.ShortcutActions()
	for _, act := range a.keyActions {
		sa.Remove(act)
	}
	a.keyActions = nil
	for _, b := range a.userKeys() {
		kc, ok := parseCombo(b.Key)
		vk := keyToVK(kc.Key)
		if !ok || vk == 0 {
			continue
		}
		var mods walk.Modifiers
		if kc.Ctrl {
			mods |= walk.ModControl
		}
		if kc.Alt {
			mods |= walk.ModAlt
		}
		if kc.Shift {
			mods |= walk.ModShift
		}
		act := walk.NewAction()
		act.SetShortcut(walk.Shortcut{Modifiers: mods, Key: walk.Key(vk)})
		id := b.Cmd
		act.Triggered().Attach(func() { a.runCommand(id) })
		sa.Insert(0, act)
		a.keyActions = append(a.keyActions, act)
	}
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

// sortedCommands: a parancsok ábécérendben, az első helyen a „semmi”.
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

var captureFn func(keyCombo)

// keyCaptureProc: a beviteli mező minden billentyűt elkap (Tab, Alt, F-billentyűk is).
var keyCaptureProc = syscall.NewCallback(func(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	switch msg {
	case 0x0087: // WM_GETDLGCODE
		return 0x0004 // DLGC_WANTALLKEYS
	case 0x0100, 0x0104: // WM_KEYDOWN, WM_SYSKEYDOWN
		vk := uint32(wp)
		if vk == 0x10 || vk == 0x11 || vk == 0x12 || vk == 0x5B || vk == 0x5C {
			return 0
		}
		down := func(v int32) bool { return win.GetKeyState(v)&-0x8000 != 0 }
		kc := keyCombo{Ctrl: down(0x11), Alt: down(0x12), Shift: down(0x10), Key: vkToKey(vk)}
		if captureFn != nil && kc.Key != "" {
			captureFn(kc)
		}
		return 0
	case 0x0102, 0x0106, 0x0105, 0x0101: // WM_CHAR, WM_SYSCHAR, WM_SYSKEYUP, WM_KEYUP
		return 0
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
	return r
})

// bindingDialog: egy billentyű és parancs kiválasztása.
func (a *App) bindingDialog(owner walk.Form, b *keyBinding, others []keyBinding) bool {
	ids, labels := sortedCommands()
	cur := 0
	for i, id := range ids {
		if id == b.Cmd {
			cur = i
		}
	}
	var dlg *walk.Dialog
	var keyLE *walk.LineEdit
	var cmdCB *walk.ComboBox
	var note *walk.Label
	var ok, cancel *walk.PushButton
	combo, _ := parseCombo(b.Key)
	describeKey := func() {
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
	err := Dialog{
		AssignTo: &dlg, Title: T("Billentyűparancs"), DefaultButton: &ok, CancelButton: &cancel,
		MinSize: Size{Width: 520, Height: 220}, Layout: Grid{Columns: 2},
		Children: []Widget{
			Label{Text: T("Billentyű:")},
			LineEdit{AssignTo: &keyLE, ReadOnly: true, Text: b.Key, CueBanner: T("kattints ide, és nyomd le a billentyűt")},
			Label{Text: T("Parancs:")},
			ComboBox{AssignTo: &cmdCB, Model: labels, CurrentIndex: cur},
			Label{AssignTo: &note, ColumnSpan: 2, Text: ""},
			Composite{ColumnSpan: 2, Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &ok, Text: "OK", OnClicked: func() {
					if u, why := combo.usable(); !u {
						walk.MsgBox(dlg, appName, why, walk.MsgBoxIconInformation)
						return
					}
					b.Key = combo.String()
					b.Cmd = ids[maxInt(cmdCB.CurrentIndex(), 0)]
					dlg.Accept()
				}},
				PushButton{AssignTo: &cancel, Text: T("Mégse"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(owner)
	if err != nil {
		walk.MsgBox(owner, appName, err.Error(), walk.MsgBoxIconError)
		return false
	}
	subclassOnce(keyLE.Handle(), keyCaptureProc, 0xC030)
	captureFn = func(kc keyCombo) {
		combo = kc
		keyLE.SetText(kc.String())
		describeKey()
	}
	defer func() { captureFn = nil }()
	themeDialog(dlg)
	dlg.Starting().Attach(func() {
		keyLE.SetFocus()
		describeKey()
		if b.Key == "" {
			note.SetText(T("Kattints a Billentyű mezőbe, és nyomd le a kívánt kombinációt (pl. Ctrl+Shift+E)."))
		}
	})
	return dlg.Run() == walk.DlgCmdOK
}

// ── saját menü ──

func (a *App) loadUserMenu() []tbButton {
	v, _ := a.settings.Get("userMenu")
	var b []tbButton
	if v != "" {
		json.Unmarshal([]byte(v), &b)
	}
	return b
}

func (a *App) saveUserMenu(b []tbButton) {
	data, _ := json.Marshal(b)
	a.settings.Put("userMenu", string(data))
	a.settings.Save()
}

// rebuildUserMenu a menüsor „Saját” menüjét tölti fel.
func (a *App) rebuildUserMenu() {
	if a.userMenu == nil {
		return
	}
	acts := a.userMenu.Actions()
	acts.Clear()
	items := a.loadUserMenu()
	for _, b := range items {
		if b.Sep {
			acts.Add(walk.NewSeparatorAction())
			continue
		}
		b := b
		act := walk.NewAction()
		act.SetText(strings.ReplaceAll(b.tooltip(), "&", "&&"))
		act.Triggered().Attach(func() { a.runButton(b) })
		acts.Add(act)
	}
	if len(items) > 0 {
		acts.Add(walk.NewSeparatorAction())
	}
	ed := walk.NewAction()
	ed.SetText(T("Saját menü szerkesztése…"))
	ed.Triggered().Attach(a.editUserMenu)
	acts.Add(ed)
}

// userMenuPopup: a saját menü a kurzornál (billentyűre köthető).
func (a *App) userMenuPopup() {
	items := a.loadUserMenu()
	var labels []string
	for _, b := range items {
		if b.Sep {
			labels = append(labels, "-")
		} else {
			labels = append(labels, b.tooltip())
		}
	}
	if len(labels) > 0 {
		labels = append(labels, "-")
	}
	labels = append(labels, T("Saját menü szerkesztése…"))
	r := a.popupAtCursor(labels) - 1
	switch {
	case r < 0:
	case r < len(items):
		a.runButton(items[r])
	case r == len(labels)-1:
		a.editUserMenu()
	}
}

func (a *App) editUserMenu() { a.editButtons("menu") }

// ── fájltípus-színezés ──

func (a *App) loadTypeColors() {
	v, _ := a.settings.Get("typeColors")
	on, ok := a.settings.Get("typeColors.on")
	a.typeCol = newTypeColors(!ok || on != "0", decodeTypeRules(v))
}

func rgbColor(c uint32) walk.Color { return walk.RGB(byte(c>>16), byte(c>>8), byte(c)) }
func colorRGB(c walk.Color) uint32 {
	return uint32(c.R())<<16 | uint32(c.G())<<8 | uint32(c.B())
}

// typeColorOf: a fájl színe a lista szövegéhez (ha van rá szabály).
func (a *App) typeColorOf(e *Entry) (walk.Color, bool) {
	if a.typeCol == nil || e.IsParent {
		return 0, false
	}
	c, ok := a.typeCol.colorFor(e.Name, e.IsDir, darkActive)
	if !ok {
		return 0, false
	}
	return rgbColor(c), true
}

func (a *App) ruleDialog(owner walk.Form, r *typeRule) bool {
	var dlg *walk.Dialog
	var nameLE, masksLE *walk.LineEdit
	var sample *walk.Label
	var ok, cancel *walk.PushButton
	col := rgbColor(r.Color)
	err := Dialog{
		AssignTo: &dlg, Title: T("Fájltípus színe"), DefaultButton: &ok, CancelButton: &cancel,
		MinSize: Size{Width: 560}, Layout: Grid{Columns: 3},
		Children: []Widget{
			Label{Text: T("Megnevezés:")}, LineEdit{AssignTo: &nameLE, Text: r.Name, ColumnSpan: 2},
			Label{Text: T("Minták:")}, LineEdit{AssignTo: &masksLE, Text: r.Masks, ColumnSpan: 2, CueBanner: T("pl. *.jpg;*.png")},
			Label{Text: T("Szín:")},
			Label{AssignTo: &sample, Text: T("  Minta.jpg  "), Font: Font{Family: "Segoe UI", PointSize: 11, Bold: true}},
			PushButton{Text: T("Szín választása…"), OnClicked: func() {
				if c, ok := chooseColor(dlg.Handle(), col); ok {
					col = c
					sample.SetTextColor(col)
				}
			}},
			Label{ColumnSpan: 3, Text: T("Több minta pontosvesszővel. Sötét témában a program a sötét színeket magától világosítja.")},
			Composite{ColumnSpan: 3, Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &ok, Text: "OK", OnClicked: func() {
					if strings.TrimSpace(masksLE.Text()) == "" {
						walk.MsgBox(dlg, appName, T("Adj meg legalább egy mintát (pl. *.jpg)."), walk.MsgBoxIconInformation)
						return
					}
					r.Name = strings.TrimSpace(nameLE.Text())
					if r.Name == "" {
						r.Name = strings.TrimSpace(masksLE.Text())
					}
					r.Masks = strings.TrimSpace(masksLE.Text())
					r.Color = colorRGB(col)
					dlg.Accept()
				}},
				PushButton{AssignTo: &cancel, Text: T("Mégse"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(owner)
	if err != nil {
		walk.MsgBox(owner, appName, err.Error(), walk.MsgBoxIconError)
		return false
	}
	sample.SetTextColor(col)
	themeDialog(dlg)
	return dlg.Run() == walk.DlgCmdOK
}

type ruleModel struct {
	walk.TableModelBase
	rules *[]typeRule
}

func (m *ruleModel) RowCount() int { return len(*m.rules) }
func (m *ruleModel) Value(row, col int) interface{} {
	r := (*m.rules)[row]
	if col == 0 {
		return r.Name
	}
	return r.Masks
}

// A billentyűk és a fájlszínek a Beállítások „Billentyűk, színek” lapján szerkeszthetők.
func (a *App) editKeys()       { a.settingsDialogAt(2) }
func (a *App) editTypeColors() { a.settingsDialogAt(2) }

type keyModel struct {
	walk.TableModelBase
	binds *[]keyBinding
}

func (m *keyModel) RowCount() int { return len(*m.binds) }
func (m *keyModel) Value(row, col int) interface{} {
	b := (*m.binds)[row]
	if col == 0 {
		return b.Key
	}
	return commandLabel(b.Cmd)
}

// customState: a „Billentyűk, színek” lap szerkesztett adatai (mentés az OK-ra).
type customState struct {
	binds  []keyBinding
	rules  []typeRule
	on     bool
	keyTV  *walk.TableView
	ruleTV *walk.TableView
	onCB   *walk.CheckBox
	keyM   *keyModel
	ruleM  *ruleModel
}

func (a *App) newCustomState() *customState {
	v, _ := a.settings.Get("typeColors")
	st := &customState{binds: a.userKeys(), rules: decodeTypeRules(v), on: a.typeCol == nil || a.typeCol.on}
	sort.Slice(st.binds, func(i, j int) bool { return st.binds[i].Key < st.binds[j].Key })
	st.keyM = &keyModel{binds: &st.binds}
	st.ruleM = &ruleModel{rules: &st.rules}
	return st
}

func (st *customState) save(a *App) {
	a.settings.Put("keys", encodeKeys(st.binds))
	a.settings.Put("typeColors", encodeTypeRules(st.rules))
	a.settings.Put("typeColors.on", boolStr(st.onCB.Checked()))
}

func (st *customState) apply(a *App) {
	a.applyUserKeys()
	a.loadTypeColors()
	a.left.repaint()
	a.right.repaint()
}

// customPage: a lap tartalma. dlg a Beállítások ablaka (a kis párbeszédablakok tulajdonosa).
func (a *App) customPage(dlg **walk.Dialog, st *customState) []Widget {
	editKey := func(i int) {
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
		if !a.bindingDialog(*dlg, &b, others) {
			return
		}
		var nb []keyBinding
		for _, o := range others {
			if o.Key != b.Key {
				nb = append(nb, o)
			}
		}
		st.binds = append(nb, b)
		sort.Slice(st.binds, func(i, j int) bool { return st.binds[i].Key < st.binds[j].Key })
		st.keyM.PublishRowsReset()
		for j, x := range st.binds {
			if x.Key == b.Key {
				st.keyTV.SetCurrentIndex(j)
			}
		}
	}
	var defaults []string
	for k, c := range defaultKeys {
		defaults = append(defaults, k+"   "+commandLabel(c))
	}
	sort.Strings(defaults)
	ruleSel := func() int { return st.ruleTV.CurrentIndex() }
	reloadRules := func(i int) {
		st.ruleM.PublishRowsReset()
		if i >= 0 && i < len(st.rules) {
			st.ruleTV.SetCurrentIndex(i)
		}
	}
	moveRule := func(d int) {
		i := ruleSel()
		j := i + d
		if i < 0 || j < 0 || j >= len(st.rules) {
			return
		}
		st.rules[i], st.rules[j] = st.rules[j], st.rules[i]
		reloadRules(j)
	}
	return []Widget{
		GroupBox{Title: T("Billentyűparancsok"), Layout: VBox{}, Children: []Widget{
			Label{Text: T("Saját billentyűk, a beépítettek előtt: egy beépített billentyű átállítható vagy kikapcsolható,\nés bármely parancs (a menük, az eszköztár parancsai) kaphat billentyűt.")},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				TableView{LastColumnStretched: true, AssignTo: &st.keyTV, Model: st.keyM, StretchFactor: 3, MinSize: Size{Height: 120},
					Columns:         []TableViewColumn{{Title: T("Billentyű"), Width: 130}, {Title: T("Parancs"), Width: 300}},
					OnItemActivated: func() { editKey(st.keyTV.CurrentIndex()) }},
				Composite{Layout: VBox{MarginsZero: true}, Children: []Widget{
					PushButton{Text: T("Új…"), OnClicked: func() { editKey(-1) }},
					PushButton{Text: T("Módosítás…"), OnClicked: func() {
						if i := st.keyTV.CurrentIndex(); i >= 0 {
							editKey(i)
						}
					}},
					PushButton{Text: T("Törlés"), OnClicked: func() {
						if i := st.keyTV.CurrentIndex(); i >= 0 && i < len(st.binds) {
							st.binds = append(st.binds[:i], st.binds[i+1:]...)
							st.keyM.PublishRowsReset()
						}
					}},
					PushButton{Text: T("Beépített billentyűk…"), OnClicked: func() {
						walk.MsgBox(*dlg, T("Beépített billentyűk (a legfontosabbak)"), strings.Join(defaults, "\n"), walk.MsgBoxIconInformation)
					}},
					VSpacer{},
				}},
			}},
		}},
		GroupBox{Title: T("Fájltípusok színezése"), Layout: VBox{}, Children: []Widget{
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				CheckBox{AssignTo: &st.onCB, Text: T("Fájlok színezése típus szerint"), Checked: st.on},
				HSpacer{},
			}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				TableView{LastColumnStretched: true, AssignTo: &st.ruleTV, Model: st.ruleM, StretchFactor: 3, MinSize: Size{Height: 140},
					Columns: []TableViewColumn{{Title: T("Típus"), Width: 170}, {Title: T("Minták"), Width: 330}},
					StyleCell: func(cs *walk.CellStyle) {
						if cs.Row() < len(st.rules) && cs.Col() == 0 {
							c := st.rules[cs.Row()].Color
							if darkActive {
								c = lightenForDark(c)
							}
							cs.TextColor = rgbColor(c)
						}
					},
					OnItemActivated: func() {
						if i := ruleSel(); i >= 0 {
							a.ruleDialog(*dlg, &st.rules[i])
							reloadRules(i)
						}
					}},
				Composite{Layout: VBox{MarginsZero: true}, Children: []Widget{
					PushButton{Text: T("Új…"), OnClicked: func() {
						r := typeRule{Color: 0x2060C0}
						if a.ruleDialog(*dlg, &r) {
							st.rules = append(st.rules, r)
							reloadRules(len(st.rules) - 1)
						}
					}},
					PushButton{Text: T("Módosítás…"), OnClicked: func() {
						if i := ruleSel(); i >= 0 {
							a.ruleDialog(*dlg, &st.rules[i])
							reloadRules(i)
						}
					}},
					PushButton{Text: T("Törlés"), OnClicked: func() {
						if i := ruleSel(); i >= 0 {
							st.rules = append(st.rules[:i], st.rules[i+1:]...)
							reloadRules(i)
						}
					}},
					PushButton{Text: T("Fel"), OnClicked: func() { moveRule(-1) }},
					PushButton{Text: T("Le"), OnClicked: func() { moveRule(1) }},
					PushButton{Text: T("Alapértelmezés"), OnClicked: func() {
						st.rules = append([]typeRule(nil), defaultTypeRules...)
						reloadRules(0)
					}},
					VSpacer{},
				}},
			}},
			Label{Text: T("Ha egy fájlra több szabály is illik, a listában előrébb álló érvényes.\nA mappák, a kurzor és a jelölt fájlok saját színűek maradnak.")},
		}},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			PushButton{Text: T("Saját menü szerkesztése…"), OnClicked: a.editUserMenu},
			HSpacer{},
		}},
	}
}
