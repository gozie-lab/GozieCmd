//go:build windows

package main

// Megjelenés: betűtípusok, színek (lista, jelölés, kurzor, fejléc), a Beállítások ablak.

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

var (
	comdlg32         = syscall.NewLazyDLL("comdlg32.dll")
	procChooseFontW  = comdlg32.NewProc("ChooseFontW")
	procGetDeviceCap = syscall.NewLazyDLL("gdi32.dll").NewProc("GetDeviceCaps")
)

// ── betűtípus-leírás ──

type fontSpec struct {
	Family       string
	Size         int
	Bold, Italic bool
	Color        walk.Color
	Weight       int // a betűválasztó pontos vastagsága (0: a Bold dönt)
}

func (f fontSpec) weight() int {
	if f.Weight > 0 {
		return f.Weight
	}
	if f.Bold {
		return 700
	}
	return 400
}

func (f fontSpec) String() string {
	b := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	bold := b(f.Bold)
	if w := f.weight(); w != 400 && w != 700 {
		bold = strconv.Itoa(w)
	}
	return f.Family + "|" + strconv.Itoa(f.Size) + "|" + bold + "|" + b(f.Italic) + "|" + colorToHex(f.Color)
}

func parseFont(s string, def fontSpec) fontSpec {
	p := strings.Split(s, "|")
	if len(p) != 5 || p[0] == "" {
		return def
	}
	size, err := strconv.Atoi(p[1])
	if err != nil || size < 5 || size > 48 {
		return def
	}
	bold, weight := p[2] == "1", 0
	if n, err := strconv.Atoi(p[2]); err == nil && n >= 100 { // a Windows-betűválasztó pontos vastagsága (pl. 600)
		bold, weight = n >= 600, n
	} else if w := weightFromName(p[0]); w > 0 { // korábbi mentés: a vastagság a névből ("Segoe UI Semibold")
		bold, weight = w >= 600, w
	}
	return fontSpec{p[0], size, bold, p[3] == "1", hexToColor(p[4], def.Color), weight}
}

func (f fontSpec) Describe() string {
	s := fmt.Sprintf("%s, %d pt", f.Family, f.Size)
	switch w := f.weight(); {
	case w >= 800:
		s += T(", fekete")
	case w >= 700:
		s += T(", félkövér")
	case w >= 600:
		s += T(", kicsit kövér")
	case w >= 500:
		s += T(", közepes")
	case w <= 300:
		s += T(", vékony")
	}
	if f.Italic {
		s += T(", dőlt")
	}
	return s
}

func (f fontSpec) font() *walk.Font {
	var st walk.FontStyle
	if f.weight() >= 700 {
		st |= walk.FontBold
	}
	if f.Italic {
		st |= walk.FontItalic
	}
	ft, err := walk.NewFont(f.Family, f.Size, st)
	if err != nil {
		ft, _ = walk.NewFont("Segoe UI", 9, st)
	}
	return ft
}

// ── színek ──

func colorToHex(c walk.Color) string {
	return fmt.Sprintf("#%02X%02X%02X", c.R(), c.G(), c.B())
}

func hexToColor(s string, def walk.Color) walk.Color {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return def
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return def
	}
	return walk.RGB(byte(v>>16), byte(v>>8), byte(v))
}

// look a teljes beállítható megjelenés.
type look struct {
	ListFont, MarkFont   fontSpec
	ListBg, MarkBg       walk.Color
	CursorBg, CursorFg   walk.Color
	CursorInBg, CurInFg  walk.Color
	HeadBg, HeadFg, Drop walk.Color
	HeadInBg, HeadInFg   walk.Color
}

var defaultLook = look{
	ListFont:   fontSpec{"Segoe UI", 9, false, false, walk.RGB(0, 0, 0), 0},
	MarkFont:   fontSpec{"Segoe UI", 9, true, false, walk.RGB(210, 0, 0), 0},
	ListBg:     walk.RGB(255, 255, 255),
	MarkBg:     walk.RGB(255, 255, 255),
	CursorBg:   walk.RGB(0, 120, 215),
	CursorFg:   walk.RGB(255, 255, 255),
	CursorInBg: walk.RGB(217, 217, 217),
	CurInFg:    walk.RGB(0, 0, 0),
	HeadBg:     walk.RGB(0, 102, 204),
	HeadFg:     walk.RGB(255, 255, 255),
	Drop:       walk.RGB(255, 230, 160),
	HeadInBg:   walk.RGB(218, 218, 218),
	HeadInFg:   walk.RGB(40, 40, 40),
}

var defaultDarkLook = look{
	ListFont:   fontSpec{"Segoe UI", 9, false, false, walk.RGB(220, 220, 220), 0},
	MarkFont:   fontSpec{"Segoe UI", 9, true, false, walk.RGB(255, 110, 110), 0},
	ListBg:     walk.RGB(25, 25, 25),
	MarkBg:     walk.RGB(25, 25, 25),
	CursorBg:   walk.RGB(0, 95, 170),
	CursorFg:   walk.RGB(255, 255, 255),
	CursorInBg: walk.RGB(60, 60, 60),
	CurInFg:    walk.RGB(230, 230, 230),
	HeadBg:     walk.RGB(0, 95, 184),
	HeadFg:     walk.RGB(255, 255, 255),
	Drop:       walk.RGB(110, 90, 30),
	HeadInBg:   walk.RGB(50, 50, 50),
	HeadInFg:   walk.RGB(200, 200, 200),
}

// lookKey: a sötét téma színei külön kulcsokon vannak.
func lookKey(k string, dark bool) string {
	if dark {
		return "dark_" + k
	}
	return k
}

var cur = defaultLook

type colorKey struct {
	key string
	ptr func(l *look) *walk.Color
}

var colorKeys = []colorKey{
	{"colorListBg", func(l *look) *walk.Color { return &l.ListBg }},
	{"colorMarkBg", func(l *look) *walk.Color { return &l.MarkBg }},
	{"colorCursorBg", func(l *look) *walk.Color { return &l.CursorBg }},
	{"colorCursorFg", func(l *look) *walk.Color { return &l.CursorFg }},
	{"colorCursorInBg", func(l *look) *walk.Color { return &l.CursorInBg }},
	{"colorCursorInFg", func(l *look) *walk.Color { return &l.CurInFg }},
	{"colorActiveBg", func(l *look) *walk.Color { return &l.HeadBg }},
	{"colorActiveFg", func(l *look) *walk.Color { return &l.HeadFg }},
	{"colorDrop2", func(l *look) *walk.Color { return &l.Drop }},
	{"colorInactiveBg", func(l *look) *walk.Color { return &l.HeadInBg }},
	{"colorInactiveFg", func(l *look) *walk.Color { return &l.HeadInFg }},
}

func (a *App) loadLook() {
	l := a.lookFor(darkActive)
	cur = l
	activeBg, activeFg, markColor, dropColor = l.HeadBg, l.HeadFg, l.MarkFont.Color, l.Drop
	inactiveBg, inactiveFg = l.HeadInBg, l.HeadInFg
}

// lookFor a világos vagy a sötét téma mentett színei.
func (a *App) lookFor(dark bool) look {
	l := defaultLook
	if dark {
		l = defaultDarkLook
	}
	get := func(k string) string { v, _ := a.settings.Get(lookKey(k, dark)); return v }
	l.ListFont = parseFont(get("fontList"), l.ListFont)
	l.MarkFont = parseFont(get("fontMark"), l.MarkFont)
	if get("fontMark") == "" && get("colorMark") != "" { // korábbi verzió beállítása
		l.MarkFont.Color = hexToColor(get("colorMark"), l.MarkFont.Color)
	}
	for _, ck := range colorKeys {
		p := ck.ptr(&l)
		*p = hexToColor(get(ck.key), *p)
	}
	return l
}

func (a *App) saveLook(l look, dark bool) {
	a.settings.Put(lookKey("fontList", dark), l.ListFont.String())
	a.settings.Put(lookKey("fontMark", dark), l.MarkFont.String())
	for _, ck := range colorKeys {
		a.settings.Put(lookKey(ck.key, dark), colorToHex(*ck.ptr(&l)))
	}
}

// applyLook a fontokat és színeket a két panelre alkalmazza.
func (a *App) applyLook() {
	a.listFont = cur.ListFont.font()
	a.boldFont = cur.MarkFont.font()
	for _, p := range []*Panel{a.left, a.right} {
		p.applyLook()
		p.setActive(p == a.active)
	}
}

// setWalkColor a walk TableView belső színmezőit írja (ezekkel rajzolja a sorokat).
func setWalkColor(tv *walk.TableView, field string, c walk.Color) {
	f := reflect.ValueOf(tv).Elem().FieldByName(field)
	if !f.IsValid() || f.Type() != reflect.TypeOf(c) {
		return
	}
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(c))
}

func (p *Panel) applyLook() {
	tv := p.tv
	if p.app.listFont != nil {
		tv.SetFont(p.app.listFont)
	}
	setWalkColor(tv, "themeNormalBGColor", cur.ListBg)
	setWalkColor(tv, "themeNormalTextColor", cur.ListFont.Color)
	setWalkColor(tv, "themeSelectedBGColor", cur.CursorBg)
	setWalkColor(tv, "themeSelectedTextColor", cur.CursorFg)
	setWalkColor(tv, "themeSelectedNotFocusedBGColor", cur.CursorInBg)
	for _, lv := range []win.HWND{p.lv, listViewField(tv, "hwndFrozenLV")} {
		if lv == 0 {
			continue
		}
		setListColors(lv, cur.ListBg, cur.ListFont.Color)
	}
	// a táblázat keretablakának háttere is (mappaváltáskor, újrarajzolás előtt ez látszik)
	if br, err := walk.NewSolidColorBrush(cur.ListBg); err == nil {
		tv.SetBackground(br) // a régit a walk magától felszabadítja
	}
	tv.Invalidate()
}

// styleCell: kurzor, jelölt, célmappa színezése.
func (p *Panel) styleCell(style *walk.CellStyle) {
	row := style.Row()
	if row < 0 || row >= len(p.model.items) {
		return
	}
	e := p.model.items[row]
	marked := !e.IsParent && p.model.marked[e.Name]
	// A betűt minden sorban meg kell adni: a walk csak akkor választja ki a sor betűjét,
	// ha az nem üres, különben a rajzolásnál az előző (pl. jelölt) sor betűje maradna érvényben.
	if marked {
		style.Font = p.app.boldFont
	} else if p.app.listFont != nil {
		style.Font = p.app.listFont
	}
	switch {
	case row == p.dropRow:
		style.BackgroundColor = dropColor
		if marked {
			style.TextColor = cur.MarkFont.Color
		}
	case row == p.cursorRow():
		if p.tv.Focused() {
			style.BackgroundColor, style.TextColor = cur.CursorBg, cur.CursorFg
		} else {
			style.BackgroundColor, style.TextColor = cur.CursorInBg, cur.CurInFg
		}
		if marked {
			style.TextColor = cur.MarkFont.Color
		}
	case marked:
		style.BackgroundColor = cur.MarkBg
		style.TextColor = cur.MarkFont.Color
	default:
		if c, ok := p.app.typeColorOf(e); ok {
			style.TextColor = c
		}
	}
}

// ── a Windows saját kiemelésének kikapcsolása rajzoláskor ──
// Az „Explorer” téma maga rajzolja a kijelölt sort, és felülírná a kurzorszínt;
// ezért a sor rajzolásakor levesszük róla a kijelölt/fókusz állapotot.

type nmCustomDraw struct {
	Hdr        nmHdr
	DrawStage  uint32
	Hdc        uintptr
	Rc         win.RECT
	ItemSpec   uintptr
	ItemState  uint32
	ItemlParam uintptr
}

const (
	nmCustomDrawCode = 0xFFFFFFF4 // NM_CUSTOMDRAW
	cddsItemPrepaint = 0x00010001
	cdisSelected     = 0x0001
	cdisFocus        = 0x0010
)

func stripSelectionState(lp uintptr) {
	cd := (*nmCustomDraw)(unsafe.Pointer(lp))
	if cd.DrawStage&cddsItemPrepaint == cddsItemPrepaint {
		cd.ItemState &^= cdisSelected | cdisFocus
	}
}

// ── betűtípus-választó ──

type chooseFontW struct {
	StructSize uint32
	Owner      win.HWND
	DC         uintptr
	LogFont    *win.LOGFONT
	PointSize  int32
	Flags      uint32
	Colors     uint32
	CustData   uintptr
	Hook       uintptr
	Template   *uint16
	Instance   uintptr
	Style      *uint16
	FontType   uint16
	_          uint16
	SizeMin    int32
	SizeMax    int32
}

func chooseFont(owner win.HWND, f fontSpec) (fontSpec, bool) {
	hdc := win.GetDC(0)
	dpi, _, _ := procGetDeviceCap.Call(uintptr(hdc), 90) // LOGPIXELSY
	win.ReleaseDC(0, hdc)
	if dpi == 0 {
		dpi = 96
	}
	var lf win.LOGFONT
	lf.LfHeight = -int32((f.Size*int(dpi) + 36) / 72)
	lf.LfWeight = int32(f.weight())
	if f.Italic {
		lf.LfItalic = 1
	}
	lf.LfCharSet = 1 // DEFAULT_CHARSET
	name := syscall.StringToUTF16(f.Family)
	copy(lf.LfFaceName[:len(lf.LfFaceName)-1], name)
	cf := chooseFontW{Owner: owner, LogFont: &lf, Colors: uint32(f.Color),
		Flags: 0x1 | 0x100 | 0x40 | 0x10000 | 0x01000000} // SCREENFONTS|EFFECTS|INITTOLOGFONT|FORCEFONTEXIST|NOVERTFONTS
	cf.StructSize = uint32(unsafe.Sizeof(cf))
	if r, _, _ := procChooseFontW.Call(uintptr(unsafe.Pointer(&cf))); r == 0 {
		return f, false
	}
	out := fontSpec{
		Family: syscall.UTF16ToString(lf.LfFaceName[:]),
		Size:   int(cf.PointSize+5) / 10,
		Bold:   lf.LfWeight >= 600,
		Italic: lf.LfItalic != 0,
		Color:  walk.Color(cf.Colors),
		Weight: int(lf.LfWeight),
	}
	if out.Size < 5 {
		out.Size = f.Size
	}
	return out, true
}

var customColors [16]win.COLORREF

func chooseColor(owner win.HWND, c walk.Color) (walk.Color, bool) {
	cc := win.CHOOSECOLOR{
		HwndOwner:    owner,
		RgbResult:    win.COLORREF(c),
		LpCustColors: &customColors,
		Flags:        win.CC_RGBINIT | win.CC_FULLOPEN | win.CC_ANYCOLOR,
	}
	cc.LStructSize = uint32(unsafe.Sizeof(cc))
	if !win.ChooseColor(&cc) {
		return c, false
	}
	return walk.Color(cc.RgbResult), true
}

// ── Beállítások ablak ──

func (a *App) settingsDialog() { a.settingsDialogAt(0) }

// settingsDialogAt: a Beállítások a megadott lappal megnyitva.
func (a *App) settingsDialogAt(page int) {
	custom := a.newCustomState()
	var tabW *walk.TabWidget
	dlog("Beállítások: megnyitás")
	// mindkét téma színei szerkeszthetők; a Téma választó dönti el, melyik látszik
	looks := [2]look{a.lookFor(false), a.lookFor(true)}
	editDark := darkActive
	l := looks[boolInt(editDark)] // a módosítások ezen a másolaton mennek, OK-ra élesednek
	dl := defaultLook
	if editDark {
		dl = defaultDarkLook
	}
	var dlg *walk.Dialog
	langChanged := false
	var themeCB, iconCB *walk.ComboBox
	var tbShowCB, rightSelCB, portableCB, typeSearchCB, updateCB *walk.CheckBox
	var idleCB, langCB *walk.ComboBox
	langList, langCodes, langCur := langNames()
	var okBtn, cancelBtn *walk.PushButton
	var editorCB, termCB *walk.ComboBox
	var editorLE, sevenLE *walk.LineEdit

	// színsorok
	type colorRow struct {
		label  string
		val    *walk.Color
		def    *walk.Color
		swatch *walk.Composite
	}
	type fontRow struct {
		label string
		val   *fontSpec
		def   *fontSpec
		desc  *walk.Label
	}
	fonts := []*fontRow{
		{label: T("Fájllista betűje és színe"), val: &l.ListFont, def: &dl.ListFont},
		{label: T("Jelölt fájlok betűje és színe"), val: &l.MarkFont, def: &dl.MarkFont},
	}
	colorGroups := []struct {
		title string
		rows  []*colorRow
	}{
		{T("Fájllista"), []*colorRow{
			{label: T("Háttér"), val: &l.ListBg, def: &dl.ListBg},
			{label: T("Jelölt fájlok háttere"), val: &l.MarkBg, def: &dl.MarkBg},
		}},
		{T("Kurzor (kiválasztott sor)"), []*colorRow{
			{label: T("Háttér – aktív panel"), val: &l.CursorBg, def: &dl.CursorBg},
			{label: T("Szöveg – aktív panel"), val: &l.CursorFg, def: &dl.CursorFg},
			{label: T("Háttér – inaktív panel"), val: &l.CursorInBg, def: &dl.CursorInBg},
			{label: T("Szöveg – inaktív panel"), val: &l.CurInFg, def: &dl.CurInFg},
		}},
		{T("Egyéb"), []*colorRow{
			{label: T("Aktív panel fejléce"), val: &l.HeadBg, def: &dl.HeadBg},
			{label: T("Aktív fejléc szövege"), val: &l.HeadFg, def: &dl.HeadFg},
			{label: T("Inaktív panel fejléce"), val: &l.HeadInBg, def: &dl.HeadInBg},
			{label: T("Inaktív fejléc szövege"), val: &l.HeadInFg, def: &dl.HeadInFg},
			{label: T("Célmappa húzáskor"), val: &l.Drop, def: &dl.Drop},
		}},
	}
	setSwatch := func(r *colorRow) {
		if b, err := walk.NewSolidColorBrush(*r.val); err == nil {
			r.swatch.SetBackground(b)
			r.swatch.Invalidate()
		}
	}

	var fontWidgets []Widget
	for _, fr := range fonts {
		fr := fr
		fontWidgets = append(fontWidgets,
			Label{Text: fr.label + ":"},
			Label{AssignTo: &fr.desc, Text: fr.val.Describe()},
			PushButton{Text: T("Módosítás…"), OnClicked: func() {
				if f, ok := chooseFont(dlg.Handle(), *fr.val); ok {
					*fr.val = f
					fr.desc.SetText(f.Describe())
				}
			}},
			PushButton{Text: T("Alapérték"), OnClicked: func() {
				*fr.val = *fr.def
				fr.desc.SetText(fr.def.Describe())
			}},
		)
	}
	themeIdx := map[string]int{"light": 0, "dark": 1, "auto": 2}[a.themeMode()]
	whichText := func(dark bool) string {
		if dark {
			return T("Az alábbi színek és betűk a sötét témához tartoznak.")
		}
		return T("Az alábbi színek és betűk a világos témához tartoznak.")
	}
	var whichLbl *walk.Label
	switchProfile := func() {
		if themeCB == nil || whichLbl == nil {
			return // létrehozáskor is lefut
		}
		want := themeCB.CurrentIndex() == 1 || themeCB.CurrentIndex() == 2 && systemDark()
		if want == editDark {
			return
		}
		looks[boolInt(editDark)] = l
		editDark = want
		l = looks[boolInt(editDark)]
		dl = defaultLook
		if editDark {
			dl = defaultDarkLook
		}
		whichLbl.SetText(whichText(editDark))
		for _, fr := range fonts {
			fr.desc.SetText(fr.val.Describe())
		}
		for _, g := range colorGroups {
			for _, r := range g.rows {
				setSwatch(r)
			}
		}
	}
	lookChildren := []Widget{
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			Label{Text: T("Téma:")},
			ComboBox{AssignTo: &themeCB, Model: []string{T("Világos"), T("Sötét"), T("Automatikus (a Windows beállítása szerint)")},
				CurrentIndex: themeIdx, OnCurrentIndexChanged: func() { switchProfile() }},
			HSpacer{},
		}},
		Label{AssignTo: &whichLbl, Text: whichText(editDark)},
		GroupBox{Title: T("Betűtípusok"), Layout: Grid{Columns: 4}, Children: fontWidgets},
	}
	for _, g := range colorGroups {
		var ws []Widget
		for _, r := range g.rows {
			r := r
			ws = append(ws,
				Label{Text: r.label + ":"},
				Composite{AssignTo: &r.swatch, Layout: HBox{MarginsZero: true},
					MinSize: Size{Width: 60, Height: 20}, MaxSize: Size{Width: 60, Height: 20},
					Background: SolidColorBrush{Color: *r.val}},
				PushButton{Text: T("Módosítás…"), OnClicked: func() {
					if c, ok := chooseColor(dlg.Handle(), *r.val); ok {
						*r.val = c
						setSwatch(r)
					}
				}},
				PushButton{Text: T("Alapérték"), OnClicked: func() {
					*r.val = *r.def
					setSwatch(r)
				}},
			)
		}
		lookChildren = append(lookChildren, GroupBox{Title: g.title, Layout: Grid{Columns: 4}, Children: ws})
	}
	lookChildren = append(lookChildren, VSpacer{})

	editors := detectEditors()
	editorNames := make([]string, 0, len(editors)+1)
	for _, e := range editors {
		editorNames = append(editorNames, e.label)
	}
	editorNames = append(editorNames, T("Egyéni…"))
	curEditor := a.editorPath()
	editorIdx := len(editorNames) - 1
	for i, e := range editors {
		if strings.EqualFold(e.path, curEditor) || strings.EqualFold(filepath.Base(e.path), curEditor) {
			editorIdx = i
		}
	}
	terms := detectTerminals()
	termNames := make([]string, len(terms))
	termIdx := len(terms) - 1
	for i, t := range terms {
		termNames[i] = t.label
		if strings.EqualFold(t.path, a.terminalPath()) || strings.EqualFold(filepath.Base(t.path), a.terminalPath()) {
			termIdx = i
		}
	}

	err := Dialog{
		AssignTo:      &dlg,
		Title:         T("Beállítások"),
		DefaultButton: &okBtn,
		CancelButton:  &cancelBtn,
		MinSize:       Size{Width: 560, Height: 480},
		Layout:        VBox{},
		Children: []Widget{
			TabWidget{AssignTo: &tabW, Pages: []TabPage{
				{Title: T("Kezelés"), Layout: VBox{}, Children: []Widget{
					GroupBox{Title: "Nyelv / Language", Layout: HBox{}, Children: []Widget{
						ComboBox{AssignTo: &langCB, Model: langList, CurrentIndex: langCur},
						Label{Text: T("Újraindítás után érvényes.")},
						HSpacer{},
						PushButton{Text: T("Fordítási sablon…"), OnClicked: func() {
							p, err := saveLangTemplate()
							if err != nil {
								walk.MsgBox(dlg, appName, errText(err), walk.MsgBoxIconWarning)
								return
							}
							walk.MsgBox(dlg, appName, langTemplateHelp(p), walk.MsgBoxIconInformation)
						}},
					}},
					GroupBox{Title: T("Eszköztár"), Layout: Grid{Columns: 3}, Children: []Widget{
						CheckBox{AssignTo: &tbShowCB, Text: T("Eszköztár megjelenítése"), Checked: a.toolbarVisible(), ColumnSpan: 3},
						Label{Text: T("Ikonméret:")},
						ComboBox{AssignTo: &iconCB, Model: iconSizeNames(), CurrentIndex: iconSizeIndex(a.toolbarIconSize())},
						PushButton{Text: T("Gombok szerkesztése…"), OnClicked: func() { a.editToolbar() }},
					}},
					GroupBox{Title: T("Billentyűzet"), Layout: VBox{}, Children: []Widget{
						Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
							CheckBox{AssignTo: &typeSearchCB, Text: T("Gépelés a listában: gyorskeresés (kikapcsolva a parancssorba kerül; gyorskeresés mindig: Ctrl+S)"),
								Checked: a.typingSearches()},
							HSpacer{},
						}},
					}},
					GroupBox{Title: T("Egér"), Layout: VBox{}, Children: []Widget{
						Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
							CheckBox{AssignTo: &rightSelCB, Text: T("Kijelölés jobb egérgombbal (a gombot nyomva tartva 1 mp után helyi menü)"),
								Checked: a.rightSelect()},
							HSpacer{},
						}},
						Label{Text: T("Bekapcsolva a jobb gomb jelöl, és húzva több fájlt is – a klasszikus kétpaneles fájlkezelők módján.\nIlyenkor jobb gombos húzással nem lehet fájlt mozgatni, arra a bal gomb (Shift-tel mozgatás) való.")},
						HSpacer{},
					}},
					GroupBox{Title: T("Távoli kapcsolatok (FTP, SFTP, WebDAV)"), Layout: HBox{}, Children: []Widget{
						Label{Text: T("Tétlenség esetén a kapcsolat bontása:")},
						ComboBox{AssignTo: &idleCB, Model: idleTimeoutLabels(), CurrentIndex: idleTimeoutIndex(a.idleMinutes())},
						HSpacer{},
					}},
					GroupBox{Title: T("Programfrissítés"), Layout: HBox{}, Children: []Widget{
						CheckBox{AssignTo: &updateCB, Text: T("Új verzió keresése induláskor (naponta egyszer)"), Checked: a.updateCheckOn()},
						HSpacer{},
						PushButton{Text: T("Keresés most"), OnClicked: func() { a.checkUpdate(true) }},
					}},
					GroupBox{Title: T("Beállításfájl"), Layout: Grid{Columns: 3}, Children: []Widget{
						CheckBox{AssignTo: &portableCB, Text: T("Hordozható mód: a beállítások a program mellett, a GozieCmd.ini fájlban"),
							Checked: a.portable, ColumnSpan: 3},
						Label{Text: T("Jelenlegi fájl:")},
						Label{Text: elideMiddle(a.settings.FilePath(), 70), ColumnSpan: 2},
						PushButton{Text: T("Mentés fájlba…"), OnClicked: func() { a.exportSettings(dlg) }},
						PushButton{Text: T("Betöltés fájlból…"), OnClicked: func() {
							if a.importSettings(dlg) {
								dlg.Cancel()
							}
						}},
						HSpacer{},
					}},
					VSpacer{},
				}},
				{Title: T("Megjelenés"), Layout: VBox{}, Children: lookChildren},
				{Title: T("Billentyűk, színek"), Layout: VBox{}, Children: a.customPage(&dlg, custom)},
				{Title: T("Programok"), Layout: VBox{}, Children: []Widget{
					GroupBox{
						Title:  T("Szövegszerkesztő (F4)"),
						Layout: Grid{Columns: 3},
						Children: []Widget{
							Label{Text: T("Program:")},
							ComboBox{AssignTo: &editorCB, Model: editorNames, CurrentIndex: editorIdx, ColumnSpan: 2,
								OnCurrentIndexChanged: func() {
									if editorCB == nil || editorLE == nil {
										return // létrehozáskor is lefut
									}
									if i := editorCB.CurrentIndex(); i >= 0 && i < len(editors) {
										editorLE.SetText(editors[i].path)
									}
								}},
							Label{Text: T("Útvonal:")},
							LineEdit{AssignTo: &editorLE, Text: curEditor},
							PushButton{Text: T("Tallózás…"), OnClicked: func() {
								fd := walk.FileDialog{Title: T("Szövegszerkesztő kiválasztása"), Filter: T("Programok (*.exe;*.cmd;*.bat)|*.exe;*.cmd;*.bat")}
								if ok, _ := fd.ShowOpen(dlg); ok {
									editorLE.SetText(fd.FilePath)
									editorCB.SetCurrentIndex(len(editorNames) - 1)
								}
							}},
						},
					},
					GroupBox{
						Title:  T("7-Zip (7Z írás, CAB / ARJ / LZH / ISO olvasás)"),
						Layout: Grid{Columns: 3},
						Children: []Widget{
							Label{Text: T("7z.exe:")},
							LineEdit{AssignTo: &sevenLE, Text: sevenZipExe(), CueBanner: T("nincs telepítve – 7-zip.org")},
							PushButton{Text: T("Tallózás…"), OnClicked: func() {
								fd := walk.FileDialog{Title: T("7z.exe kiválasztása"), Filter: T("7z.exe|7z.exe;7za.exe|Programok (*.exe)|*.exe")}
								if ok, _ := fd.ShowOpen(dlg); ok {
									sevenLE.SetText(fd.FilePath)
								}
							}},
						},
					},
					GroupBox{
						Title:  T("Terminál (F9, és Shift+Enter a parancssorban)"),
						Layout: Grid{Columns: 2},
						Children: []Widget{
							Label{Text: T("Program:")},
							ComboBox{AssignTo: &termCB, Model: termNames, CurrentIndex: termIdx},
						},
					},
					VSpacer{},
				}},
			}},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okBtn, Text: "OK", OnClicked: func() {
						looks[boolInt(editDark)] = l
						a.saveLook(looks[0], false)
						a.saveLook(looks[1], true)
						a.settings.Put("theme", []string{"light", "dark", "auto"}[maxInt(themeCB.CurrentIndex(), 0)])
						a.settings.Put("toolbar.show", boolStr(tbShowCB.Checked()))
						a.settings.Put("toolbar.iconSize", strconv.Itoa(iconSizes[maxInt(iconCB.CurrentIndex(), 0)]))
						a.settings.Put("rightSelect", boolStr(rightSelCB.Checked()))
						a.settings.Put("quickSearchTyping", boolStr(typeSearchCB.Checked()))
						if i := langCB.CurrentIndex(); i >= 0 && langCodes[i] != curLang {
							a.settings.Put("lang", langCodes[i])
							langChanged = true
						}
						a.settings.Put("remoteIdle", strconv.Itoa(idleTimeouts[maxInt(idleCB.CurrentIndex(), 0)]))
						a.settings.Put("updateCheck", boolStr(updateCB.Checked()))
						wantPortable := portableCB.Checked()
						a.settings.Put("editor", strings.Trim(strings.TrimSpace(editorLE.Text()), `"`))
						a.settings.Put("sevenZip", strings.Trim(strings.TrimSpace(sevenLE.Text()), `"`))
						if i := termCB.CurrentIndex(); i >= 0 && i < len(terms) {
							a.settings.Put("terminal", terms[i].path)
						}
						a.settings.Save()
						if wantPortable != a.portable {
							a.setPortable(wantPortable)
						}
						custom.save(a)
						a.settings.Save()
						a.applyTheme() // téma, színek, eszköztár
						custom.apply(a)
						dlg.Accept()
						if langChanged {
							walk.MsgBox(a.mw, appName, T("A nyelv a program következő indításakor vált.\n\nThe language changes the next time the program starts."), walk.MsgBoxIconInformation)
						}
					}},
					PushButton{AssignTo: &cancelBtn, Text: T("Mégse"), OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(a.mw)
	if err != nil {
		dlog("Beállítások: hiba %v", err)
		walk.MsgBox(a.mw, appName, err.Error(), walk.MsgBoxIconError)
		return
	}
	themeDialog(dlg)
	if a.icon != nil {
		dlg.SetIcon(a.icon)
	}
	a.sizeableDialog(dlg, a.mw, "settings", 640, 640)
	if page > 0 && page < tabW.Pages().Len() {
		tabW.SetCurrentIndex(page)
	}
	dlg.Run()
	dlog("Beállítások: bezárva")
}
