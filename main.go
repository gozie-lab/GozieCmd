//go:build windows

package main

// GozieCommander – kétpaneles fájlkezelő Windowsra.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

var (
	activeBg   = walk.RGB(0, 102, 204)
	activeFg   = walk.RGB(255, 255, 255)
	inactiveBg = walk.RGB(218, 218, 218)
	inactiveFg = walk.RGB(40, 40, 40)
	markColor  = walk.RGB(210, 0, 0)
	dropColor  = walk.RGB(204, 228, 247)
)

var theApp *App

type App struct {
	mw           *walk.MainWindow
	left, right  *Panel
	active       *Panel
	showHidden   bool
	startArgs    bool
	queue        *transferQueue
	queueBtn     *walk.PushButton
	queueDlg     *walk.Dialog
	queueRefresh func()
	qvOn         bool
	qvTimer      *time.Timer
	qvGen        int
	idleNotice   bool           // tétlenségi bontás üzenete látszik
	keyActions   []*walk.Action // saját billentyűk
	userMenu     *walk.Menu
	typeCol      *typeColors
	cmd          *walk.LineEdit
	cmdLabel     *walk.Label
	boldFont     *walk.Font // jelölt fájlok betűje
	listFont     *walk.Font
	toolbar      *walk.ToolBar
	hiddenAction *walk.Action
	tbAction     *walk.Action
	lastPattern  string
	portable     bool
	settings     *walk.IniFileSettings
	icon         *walk.Icon
	dragSrc      *Panel   // a saját húzás forráspanelje
	dragFiles    []string // és a húzott fájlok
}

func main() {
	runtime.LockOSThread() // az OLE-nak és az ablakoknak ugyanazon a szálon kell lenniük
	dlogReset()
	dlog("%s %s indul", appName, appVersion)
	// a végzetes futtatókörnyezeti hibák (bármely szálon) is a naplóba kerüljenek
	if f, err := os.OpenFile(dlogPath(), os.O_APPEND|os.O_WRONLY, 0o666); err == nil {
		debug.SetCrashOutput(f, debug.CrashOptions{})
		f.Close()
	}
	defer func() {
		if r := recover(); r != nil {
			dlog("PROGRAMHIBA: %v\n%s", r, debug.Stack())
			walk.MsgBox(nil, appName, fmt.Sprintf(T("Váratlan programhiba miatt a program leáll:\n%v\n\nA részletek a naplóban: %s"), r, dlogPath()), walk.MsgBoxIconError)
		}
	}()
	walk.App().Panicking().Attach(func(err error) {
		dlog("PROGRAMHIBA (ablakkezelő): %v\n%s", err, debug.Stack())
	})
	oleInit() // húzd és ejtsd
	decodeOEM = oemToString
	defer cleanupTemp()
	app := &App{lastPattern: "*.*"}
	theApp = app
	wapp := walk.App()
	wapp.SetOrganizationName(appDir)
	wapp.SetProductName(appDir)
	migrateSettings()
	app.settings, app.portable = openSettings()
	_ = app.settings.Load()
	dlog("beállítások: %s", app.settings.FilePath())
	wapp.SetSettings(app.settings)

	darkActive = app.wantDark()
	app.loadLook()
	if v, ok := app.settings.Get("showHidden"); ok && v == "1" {
		app.showHidden = true
	}
	home, _ := os.UserHomeDir()
	startLeft, startRight := home, home
	if v, ok := app.settings.Get("left"); ok && v != "" {
		startLeft = v
	}
	if v, ok := app.settings.Get("right"); ok && v != "" {
		startRight = v
	}
	if len(os.Args) > 1 {
		startLeft = os.Args[1]
	}
	if len(os.Args) > 2 {
		startRight = os.Args[2]
	}

	app.startArgs = len(os.Args) > 1
	app.left = newPanel(app, "left")
	app.right = newPanel(app, "right")
	startLeft = app.left.initTabs(startLeft)
	startRight = app.right.initTabs(startRight)

	var err error
	app.boldFont, err = walk.NewFont("Segoe UI", 9, walk.FontBold)
	if err != nil {
		walk.MsgBox(nil, appName, err.Error(), walk.MsgBoxIconError)
		return
	}

	fkey := func(text string, f func()) Widget {
		return PushButton{Text: text, OnClicked: f}
	}
	var btnBar *walk.Composite

	err = MainWindow{
		AssignTo:    &app.mw,
		Visible:     false, // a mentett méret beállítása után jelenik meg, villanás nélkül
		Title:       appName + " " + appVersion,
		Size:        Size{Width: 1200, Height: 760},
		MinSize:     Size{Width: 700, Height: 400},
		Font:        Font{Family: "Segoe UI", PointSize: 9},
		Layout:      VBox{Margins: Margins{Left: 4, Top: 4, Right: 4, Bottom: 4}, Spacing: 4},
		MenuItems:   app.menu(),
		OnDropFiles: app.dropFiles,
		Children: []Widget{
			HSplitter{
				HandleWidth: 5,
				Children:    []Widget{app.left.widget(), app.right.widget()},
			},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 4},
				Children: []Widget{
					Label{AssignTo: &app.cmdLabel},
					LineEdit{AssignTo: &app.cmd, OnKeyDown: app.cmdKey},
					PushButton{AssignTo: &app.queueBtn, Visible: false, OnClicked: func() { app.showQueue() }},
				},
			},
			Composite{
				AssignTo: &btnBar,
				Layout:   HBox{MarginsZero: true, Spacing: 3},
				Children: []Widget{
					fkey(T("F2 Frissít"), app.refreshActive),
					fkey(T("F3 Nézőke"), app.viewFile),
					fkey(T("F4 Szerkeszt"), app.editFile),
					fkey(T("F5 Másol"), func() { app.transfer(false) }),
					fkey(T("F6 Mozgat"), func() { app.transfer(true) }),
					fkey(T("F7 Új mappa"), app.mkdir),
					fkey(T("F8 Töröl"), func() { app.deleteSelected(false) }),
					fkey(T("F9 Terminál"), app.terminal),
					fkey(T("Beállítások"), app.settingsDialog),
					fkey(T("Alt+F4 Kilépés"), func() { app.mw.Close() }),
				},
			},
		},
	}.Create()
	if err != nil {
		walk.MsgBox(nil, appName, fmt.Sprintf(T("Az ablak nem hozható létre:\n%s"), err.Error()), walk.MsgBoxIconError)
		return
	}

	// A Tab csak a két lista között váltson.
	removeTabStop(app.cmd.Handle())
	for i := 0; i < btnBar.Children().Len(); i++ {
		removeTabStop(btnBar.Children().At(i).Handle())
	}

	app.initQueue()
	app.loadTypeColors()
	app.hookDeviceChange()
	app.left.afterCreate(startLeft)
	app.right.afterCreate(startRight)
	app.left.setupMouse(1)
	app.right.setupMouse(2)
	app.applyLook()
	// a főablak beépített eszköztára (a deklaratív eszköztár csak induló gombokkal jönne létre)
	app.toolbar = app.mw.ToolBar()
	if m, err := walk.NewMenu(); err == nil {
		act := walk.NewAction()
		act.SetText(T("Eszköztár szerkesztése…"))
		act.Triggered().Attach(app.editToolbar)
		m.Actions().Add(act)
		app.toolbar.SetContextMenu(m)
	}
	app.applyTheme() // téma + színek + eszköztár
	// Tab a listákban (panelváltás), Esc/Tab a parancssorban: a Windows
	// párbeszéd-kezelője elnyelné őket, ezért a vezérlők maguk kérik el.
	subclassPanels[20] = app.left
	procSetWindowSubclass.Call(uintptr(app.cmd.Handle()), subclassProc, 20, 0)
	if app.hiddenAction != nil {
		app.hiddenAction.SetChecked(app.showHidden)
	}

	app.rebuildUserMenu()
	app.applyUserKeys()
	first := app.left
	if v, _ := app.settings.Get("active"); v == "right" {
		first = app.right
	}
	app.setActive(first)
	// A mentett méretet a walk induló méretezése UTÁN állítjuk vissza (különben
	// felülírja), az ablak addig rejtve marad.
	app.mw.Starting().Attach(func() {
		app.mw.Synchronize(func() {
			ok := restorePlacement(app, app.mw.Handle(), "main")
			dlog("ablakhely visszaállítása: %v", ok)
			app.mw.Show()
			first.tv.SetFocus()
			app.startUpdateCheck()
			app.initPasswordPrompt()
		})
	})

	app.mw.Activating().Attach(func() {
		app.left.checkChanged()
		app.right.checkChanged()
	})
	go func() {
		for range time.Tick(1500 * time.Millisecond) {
			app.mw.Synchronize(func() {
				app.left.checkChanged()
				app.right.checkChanged()
				app.checkIdle()
				// automatikus téma: a Windows-beállítás változásának követése
				if app.themeMode() == "auto" && systemDark() != darkActive {
					app.applyTheme()
				}
				healForms()
			})
		}
	}()

	// programikon (ablak bal felső sarka, tálca) és a mentett ablakméret
	if app.icon = loadAppIcon(); app.icon != nil {
		app.mw.SetIcon(app.icon)
	}

	app.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		savePlacement(app, app.mw.Handle(), "main")
		s := app.settings
		app.left.saveTabs()
		app.right.saveTabs()
		s.Put("left", app.left.path)
		s.Put("right", app.right.path)
		s.Put("active", app.active.side)
		if app.showHidden {
			s.Put("showHidden", "1")
		} else {
			s.Put("showHidden", "0")
		}
	})

	app.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if n := app.queue.Active(); n > 0 && walk.MsgBox(app.mw, T("Kilépés"),
			fmt.Sprintf(Tn(n, "%d háttérművelet még fut vagy vár. Kilépve a várakozók elmaradnak.\nKilépsz?"), n),
			walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != win.IDYES {
			*canceled = true
		}
	})
	app.mw.Run()
	_ = app.settings.Save()
	for _, p := range []*Panel{app.left, app.right} {
		if p.vfs != nil {
			p.vfs.Close()
		}
	}
	if restartAfterExit {
		startSelf()
	}
}

func (a *App) menu() []MenuItem {
	sc := func(m walk.Modifiers, k walk.Key) Shortcut { return Shortcut{Modifiers: m, Key: k} }
	return []MenuItem{
		Menu{Text: T("&Fájl"), Items: []MenuItem{
			Action{Text: T("Megnyitás\tEnter"), OnTriggered: func() { a.active.openCurrent() }},
			Action{Text: T("Nézőke"), Shortcut: sc(0, walk.KeyF3), OnTriggered: a.viewFile},
			Action{Text: T("Szerkesztés"), Shortcut: sc(0, walk.KeyF4), OnTriggered: a.editFile},
			Action{Text: T("Új fájl…"), Shortcut: sc(walk.ModShift, walk.KeyF4), OnTriggered: a.newFile},
			Separator{},
			Action{Text: T("Másolás…"), Shortcut: sc(0, walk.KeyF5), OnTriggered: func() { a.transfer(false) }},
			Action{Text: T("Mozgatás…"), Shortcut: sc(0, walk.KeyF6), OnTriggered: func() { a.transfer(true) }},
			Action{Text: T("Átnevezés…"), Shortcut: sc(walk.ModShift, walk.KeyF6), OnTriggered: a.rename},
			Action{Text: T("Többfájl-átnevezés…"), Shortcut: sc(walk.ModControl, walk.KeyM), OnTriggered: a.multiRename},
			Action{Text: T("Új mappa…"), Shortcut: sc(0, walk.KeyF7), OnTriggered: a.mkdir},
			Action{Text: T("Törlés (Lomtárba)"), Shortcut: sc(0, walk.KeyF8), OnTriggered: func() { a.deleteSelected(false) }},
			Action{Text: T("Végleges törlés"), Shortcut: sc(walk.ModShift, walk.KeyF8), OnTriggered: func() { a.deleteSelected(true) }},
			Separator{},
			Action{Text: T("Tulajdonságok"), OnTriggered: func() { a.active.properties() }},
			Action{Text: T("Terminál itt"), Shortcut: sc(0, walk.KeyF9), OnTriggered: a.terminal},
			Separator{},
			Action{Text: T("Kilépés\tAlt+F4"), OnTriggered: func() { a.mw.Close() }},
		}},
		Menu{Text: T("&Kijelölés"), Items: []MenuItem{
			Action{Text: T("Kijelölés mintával…\tNum +"), OnTriggered: func() { a.selectPattern(true) }},
			Action{Text: T("Kijelölés megszüntetése mintával…\tNum −"), OnTriggered: func() { a.selectPattern(false) }},
			Action{Text: T("Kijelölés megfordítása\tNum *"), OnTriggered: func() { a.active.invertMarks() }},
			Action{Text: T("Mindet kijelöl\tCtrl+A"), OnTriggered: func() { a.active.markAll() }},
			Action{Text: T("Kijelölés törlése"), OnTriggered: func() { a.active.setMarks(map[string]bool{}) }},
			Separator{},
			Action{Text: T("Útvonalak másolása vágólapra"), Shortcut: sc(walk.ModControl|walk.ModShift, walk.KeyC), OnTriggered: a.copyPaths},
			Action{Text: T("Nevek másolása vágólapra"), OnTriggered: a.copyNames},
			Separator{},
			Action{Text: T("Mappák összehasonlítása"), Shortcut: sc(walk.ModShift, walk.KeyF2), OnTriggered: a.compareDirs},
		}},
		Menu{Text: T("&Archívum"), Items: []MenuItem{
			Action{Text: T("Archívum megnyitása\tEnter"), OnTriggered: func() { a.active.openCurrent() }},
			Action{Text: T("Becsomagolás…\tAlt+F5"), Shortcut: sc(walk.ModControl, walk.KeyP), OnTriggered: a.pack},
			Action{Text: T("Kicsomagolás…\tAlt+F9"), Shortcut: sc(walk.ModControl|walk.ModShift, walk.KeyP), OnTriggered: a.unpack},
			Separator{},
			Action{Text: T("Kilépés az archívumból"), OnTriggered: func() { a.active.exitVFS() }},
		}},
		Menu{Text: T("&Távoli"), Items: []MenuItem{
			Action{Text: T("Kapcsolatok (FTP, SFTP, WebDAV)…"), Shortcut: sc(walk.ModControl, walk.KeyF), OnTriggered: a.ftpConnect},
			Action{Text: T("Gyors kapcsolódás…"), OnTriggered: a.quickConnect},
			Action{Text: T("Kapcsolat bontása"), Shortcut: sc(walk.ModControl|walk.ModShift, walk.KeyF), OnTriggered: a.ftpDisconnect},
		}},
		Menu{Text: T("&Hálózat"), Items: []MenuItem{
			Action{Text: T("Hálózati gépek"), Shortcut: sc(walk.ModControl, walk.KeyN), OnTriggered: a.openNetwork},
			Action{Text: T("Gép vagy megosztás megnyitása…"), Shortcut: sc(walk.ModControl|walk.ModShift, walk.KeyN), OnTriggered: a.openComputer},
			Separator{},
			Action{Text: T("Hálózati meghajtó csatolása…"), OnTriggered: a.mapDrive},
			Action{Text: T("Hálózati meghajtó leválasztása…"), OnTriggered: a.unmapDrive},
			Separator{},
			Action{Text: T("Gép eltávolítása a listából\tF8"), OnTriggered: a.forgetServer},
		}},
		Menu{Text: T("&Parancsok"), Items: []MenuItem{
			Action{Text: T("Fájlkeresés…\tAlt+F7"), OnTriggered: a.findFiles},
			Separator{},
			Action{Text: T("Ugrás mappára…"), Shortcut: sc(walk.ModControl, walk.KeyL), OnTriggered: a.gotoPathDialog},
			Action{Text: T("Panelek cseréje"), Shortcut: sc(walk.ModControl, walk.KeyU), OnTriggered: a.swapPanels},
			Action{Text: T("Mappa a bal panelen\tCtrl+←"), OnTriggered: func() { a.openInSide(a.left) }},
			Action{Text: T("Mappa a jobb panelen\tCtrl+→"), OnTriggered: func() { a.openInSide(a.right) }},
			Action{Text: T("Ugyanaz a mappa a másik panelen"), Shortcut: sc(walk.ModControl, walk.KeyE), OnTriggered: func() {
				a.other().navigate(a.active.path, "", true, false)
			}},
			Separator{},
			Action{Text: T("Vissza\t(egér: vissza gomb)"), Shortcut: sc(walk.ModControl, walk.KeyB), OnTriggered: func() { a.active.goBack() }},
			Action{Text: T("Előre\t(egér: előre gomb)"), Shortcut: sc(walk.ModControl|walk.ModShift, walk.KeyB), OnTriggered: func() { a.active.goForward() }},
			Action{Text: T("Szülőmappa\tBackspace"), OnTriggered: func() { a.active.goUp() }},
			Action{Text: T("Gyökérmappa"), Shortcut: Shortcut{Modifiers: walk.ModControl, Key: walk.KeyHome}, OnTriggered: func() { a.active.goRoot() }},
			Separator{},
			Action{Text: T("Parancssor"), Shortcut: sc(walk.ModControl, walk.KeyK), OnTriggered: func() { a.cmd.SetFocus() }},
			Separator{},
			Action{Text: T("Kedvenc mappák…"), Shortcut: sc(walk.ModControl, walk.KeyD), OnTriggered: a.hotlistMenu},
			Action{Text: T("Mappák szinkronizálása…"), OnTriggered: a.syncDirs},
			Action{Text: T("Háttérműveletek…"), Shortcut: sc(walk.ModControl, walk.KeyJ), OnTriggered: a.showQueue},
			Action{Text: T("Előzmények…\tAlt+↓"), OnTriggered: a.historyPopup},
			Action{Text: T("Gyorskeresés a listában"), Shortcut: sc(walk.ModControl, walk.KeyS), OnTriggered: func() {
				a.active.tv.SetFocus()
				a.active.startQuickSearch("")
			}},
		}},
		Menu{Text: T("&Eszközök"), Items: []MenuItem{
			Action{Text: T("Fájlok összehasonlítása tartalom szerint…"), OnTriggered: a.compareContent},
			Separator{},
			Action{Text: T("Ellenőrzőösszeg készítése (MD5, SHA)…"), OnTriggered: a.makeChecksums},
			Action{Text: T("Ellenőrzőösszeg ellenőrzése"), OnTriggered: a.verifyChecksums},
			Separator{},
			Action{Text: T("Ismétlődő fájlok keresése…"), OnTriggered: a.findDuplicates},
			Separator{},
			Action{Text: T("Fájl darabolása…"), OnTriggered: a.splitCurrent},
			Action{Text: T("Fájlok összefűzése…"), OnTriggered: a.joinCurrent},
			Separator{},
			Action{Text: T("Attribútumok és dátum módosítása…"), OnTriggered: a.changeAttrs},
		}},
		Menu{Text: T("F&ülek"), Items: []MenuItem{
			Action{Text: T("Új fül"), Shortcut: sc(walk.ModControl, walk.KeyT), OnTriggered: func() { a.active.newTab() }},
			Action{Text: T("Fül bezárása"), Shortcut: sc(walk.ModControl, walk.KeyW), OnTriggered: func() { a.active.closeTab() }},
			Action{Text: T("Következő fül\tCtrl+Tab"), OnTriggered: func() { a.active.cycleTab(1) }},
			Action{Text: T("Előző fül\tCtrl+Shift+Tab"), OnTriggered: func() { a.active.cycleTab(-1) }},
		}},
		Menu{Text: T("&Nézet"), Items: []MenuItem{
			Action{Text: T("Részletes nézet"), Shortcut: sc(walk.ModControl, walk.KeyF2), OnTriggered: func() { a.active.setViewMode(viewDetail) }},
			Action{Text: T("Rövid nézet"), Shortcut: sc(walk.ModControl, walk.KeyF1), OnTriggered: func() { a.active.setViewMode(viewBrief) }},
			Action{Text: T("Bélyegkép nézet"), Shortcut: sc(walk.ModControl|walk.ModShift, walk.KeyF1), OnTriggered: func() { a.active.setViewMode(viewThumbs) }},
			Action{Text: T("Gyorsnézet a másik panelen"), Shortcut: sc(walk.ModControl, walk.KeyQ), OnTriggered: a.toggleQuickView},
			Separator{},
			Action{AssignTo: &a.hiddenAction, Text: T("Rejtett fájlok mutatása"), Checkable: true,
				Shortcut: sc(walk.ModControl, walk.KeyH), OnTriggered: a.toggleHidden},
			Separator{},
			Action{Text: T("Rendezés név szerint"), Shortcut: sc(walk.ModControl, walk.KeyF3), OnTriggered: func() { a.sortActive(0) }},
			Action{Text: T("Rendezés kiterjesztés szerint"), Shortcut: sc(walk.ModControl, walk.KeyF4), OnTriggered: func() { a.sortActive(1) }},
			Action{Text: T("Rendezés dátum szerint"), Shortcut: sc(walk.ModControl, walk.KeyF5), OnTriggered: func() { a.sortActive(3) }},
			Action{Text: T("Rendezés méret szerint"), Shortcut: sc(walk.ModControl, walk.KeyF6), OnTriggered: func() { a.sortActive(2) }},
			Separator{},
			Action{Text: T("Frissítés"), Shortcut: sc(0, walk.KeyF2), OnTriggered: a.refreshActive},
			Action{Text: T("Meghajtólista frissítése"), OnTriggered: func() { a.left.populateDrives(); a.right.populateDrives() }},
			Separator{},
			Action{AssignTo: &a.tbAction, Text: T("Eszköztár"), Checkable: true, OnTriggered: func() {
				a.settings.Put("toolbar.show", boolStr(!a.toolbarVisible()))
				a.buildToolbar()
			}},
			Action{Text: T("Eszköztár szerkesztése…"), OnTriggered: a.editToolbar},
			Action{Text: T("Fájltípusok színezése…"), OnTriggered: a.editTypeColors},
			Action{Text: T("Billentyűparancsok…"), OnTriggered: a.editKeys},
			Action{Text: T("Beállítások…"), OnTriggered: a.settingsDialog},
		}},
		Menu{AssignTo: &a.userMenu, Text: T("Sa&ját"), Items: []MenuItem{
			Action{Text: T("Saját menü szerkesztése…"), OnTriggered: a.editUserMenu},
		}},
		Menu{Text: T("&Súgó"), Items: []MenuItem{
			Action{Text: T("Billentyűparancsok"), Shortcut: sc(0, walk.KeyF1), OnTriggered: a.help},
			Action{Text: T("Diagnosztikai napló"), OnTriggered: func() { showViewer(a.mw, dlogPath()) }},
			Action{Text: T("Frissítés keresése…"), OnTriggered: func() { a.checkUpdate(true) }},
			Action{Text: T("Névjegy"), OnTriggered: func() {
				walk.MsgBox(a.mw, T("Névjegy"), aboutText(), walk.MsgBoxIconInformation)
			}},
		}},
	}
}

// ── panelkezelés ──

func (a *App) setActive(p *Panel) {
	if a.active == p {
		return
	}
	if a.active != nil {
		a.active.setActive(false)
	}
	a.active = p
	p.setActive(true)
	a.updateCmdLabel()
	if a.qvOn {
		a.placeQuickView()
	}
}

func (a *App) tabKey() {
	if a.active.tv.Focused() {
		o := a.other()
		a.setActive(o)
		o.tv.SetFocus()
		return
	}
	a.active.tv.SetFocus()
}

func (a *App) other() *Panel {
	if a.active == a.left {
		return a.right
	}
	return a.left
}

func (a *App) updateCmdLabel() {
	if a.cmdLabel == nil || a.active == nil {
		return
	}
	path := a.active.path
	if r := []rune(path); len(r) > 50 {
		path = fmt.Sprintf(T("…%s"), string(r[len(r)-49:]))
	}
	a.cmdLabel.SetText(path + ">")
}

func (a *App) refreshActive() { a.active.refresh() }

func (a *App) refreshBoth() {
	a.left.refresh()
	a.right.refresh()
}

func (a *App) toggleHidden() {
	a.showHidden = !a.showHidden
	a.hiddenAction.SetChecked(a.showHidden)
	a.refreshBoth()
}

func (a *App) sortActive(col int) {
	m := a.active.model
	order := walk.SortAscending
	if m.sortCol == col && m.sortOrder == walk.SortAscending {
		order = walk.SortDescending
	}
	m.Sort(col, order)
}

func (a *App) swapPanels() {
	if a.left.vfs != nil || a.right.vfs != nil {
		walk.MsgBox(a.mw, appName, T("Archívum vagy távoli kapcsolat megnyitása mellett a panelek nem cserélhetők."), walk.MsgBoxIconInformation)
		return
	}
	l, r := a.left.path, a.right.path
	a.left.navigate(r, "", true, false)
	a.right.navigate(l, "", true, false)
}

func (a *App) openInSide(target *Panel) {
	if a.notInVFS() {
		return
	}
	path := a.active.path
	if e := a.active.currentEntry(); e != nil && e.IsDir && !e.IsParent {
		path = e.Path
	}
	target.navigate(path, "", true, false)
}

func (a *App) gotoPathDialog() {
	p := a.active
	if p.vfs != nil && p.kind() != "net" {
		t, ok := inputBox(a.mw, T("Ugrás mappára"), T("Útvonal az archívumon / szerveren belül:"), p.vpath, false)
		if ok {
			t = strings.Trim(strings.ReplaceAll(strings.TrimSpace(t), `\`, "/"), `"`)
			if p.vfs.Kind() == "arc" {
				t = strings.Trim(t, "/")
			} else if !strings.HasPrefix(t, "/") {
				t = p.vfs.Join(p.vpath, t)
			}
			p.navigateVFS(t, "")
		}
		p.tv.SetFocus()
		return
	}
	t, ok := inputBox(a.mw, T("Ugrás mappára"), T("Mappa vagy fájl útvonala:"), p.path, false)
	if ok && strings.TrimSpace(t) != "" {
		p.navigate(strings.Trim(strings.TrimSpace(t), `"`), "", true, false)
	}
	p.tv.SetFocus()
}

// ── parancssor ──

func (a *App) cmdKey(key walk.Key) {
	switch key {
	case walk.KeyEscape:
		a.cmd.SetText("")
		a.active.tv.SetFocus()
		return
	case walk.KeyTab:
		a.active.tv.SetFocus()
		return
	}
	if key != walk.KeyReturn {
		return
	}
	keep := walk.ModifiersDown()&walk.ModShift != 0
	c := strings.TrimSpace(a.cmd.Text())
	a.cmd.SetText("")
	p := a.active
	defer p.tv.SetFocus()
	if c == "" {
		return
	}
	low := strings.ToLower(c)
	if len(c) == 2 && c[1] == ':' {
		p.navigate(c+`\`, "", true, false)
		return
	}
	if strings.HasPrefix(c, `\\`) { // hálózati út
		p.navigate(strings.Trim(c, `"`), "", true, false)
		return
	}
	if low == "cd" || strings.HasPrefix(low, "cd ") || strings.HasPrefix(low, "cd..") ||
		strings.HasPrefix(low, `cd\`) || strings.HasPrefix(low, "cd/") {
		arg := strings.Trim(strings.TrimSpace(c[2:]), `"`)
		switch {
		case arg == "":
			arg, _ = os.UserHomeDir()
		case arg == `\` || arg == "/":
			p.goRoot()
			return
		case len(arg) == 2 && arg[1] == ':':
			arg += `\`
		}
		if strings.HasPrefix(arg, `\\`) {
			p.navigate(arg, "", true, false)
			return
		}
		if !filepath.IsAbs(arg) {
			arg = filepath.Join(p.path, arg)
		}
		arg = filepath.Clean(arg)
		prev := p.path
		if p.navigate(arg, "", true, false) && strings.EqualFold(arg, filepath.Dir(prev)) {
			p.selectName(filepath.Base(prev))
		}
		return
	}
	var err error
	if keep {
		err = a.openTerminalAt(p.path, c)
	} else {
		err = runCmdLine(p.path, `cmd.exe /c start "" `+c, createNoWindow)
	}
	if err != nil {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("Nem sikerült futtatni:\n%s\n\n%s"), c, err.Error()), walk.MsgBoxIconWarning)
	}
}

func (a *App) cmdInsert(ch string) {
	t := a.cmd.Text() + ch
	a.cmd.SetFocus()
	a.cmd.SetText(t)
	n := len(utf16.Encode([]rune(t)))
	a.cmd.SetTextSelection(n, n)
}

func (a *App) terminal() {
	if err := a.openTerminalAt(a.active.path, ""); err != nil {
		walk.MsgBox(a.mw, appName, err.Error(), walk.MsgBoxIconWarning)
	}
}

// ── fájlműveletek ──

func (a *App) viewFile() {
	p := a.active
	e := p.currentEntry()
	if e == nil || e.IsParent {
		return
	}
	if e.IsDir {
		p.computeDirSize(e)
		return
	}
	if p.vfs != nil {
		a.openVirtual(p, e, true)
		return
	}
	switch strings.ToLower(e.Ext) {
	case "jpg", "jpeg", "png", "gif", "bmp", "webp", "tif", "tiff", "ico", "pdf", "mp4", "mp3", "heic":
		shellOpen(a.mw.Handle(), e.Path, p.path)
		return
	}
	showViewer(a.mw, e.Path)
}

func (a *App) editFile() {
	if a.notInVFS() {
		return
	}
	e := a.active.currentEntry()
	if e == nil || e.IsDir {
		return
	}
	if err := a.openInEditor(e.Path, a.active.path); err != nil {
		walk.MsgBox(a.mw, appName, err.Error(), walk.MsgBoxIconWarning)
	}
}

func (a *App) newFile() {
	if a.notInVFS() {
		return
	}
	p := a.active
	name, ok := inputBox(a.mw, T("Új fájl"), T("Az új fájl neve:"), T("új.txt"), true)
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return
	}
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.path, name)
	}
	if _, err := os.Stat(path); err != nil {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
		if err != nil {
			walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A fájl nem hozható létre:\n%s"), errText(err)), walk.MsgBoxIconWarning)
			return
		}
		f.Close()
	}
	p.refresh()
	p.selectName(filepath.Base(path))
	a.editFile()
}

func (a *App) mkdir() {
	p := a.active
	name, ok := inputBox(a.mw, T("Új mappa"), T("Az új mappa neve (alkönyvtárakkal is, pl. a\\b\\c):"), "", false)
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return
	}
	if p.kind() == "net" {
		return
	}
	if p.vfs != nil {
		var err error
		switch v := p.vfs.(type) {
		case Archive:
			if !v.Writable() {
				err = errors.New(fmt.Sprintf(T("%s: ebbe az archívumba nem lehet írni.%s"), v.Info(), sevenZipHint(v)))
			} else {
				err = v.Mkdir(p.vpath, name)
			}
		case remoteVFS:
			err = v.Mkdir(p.vpath, name)
		}
		if err != nil {
			walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A mappa nem hozható létre:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		}
		p.refresh()
		p.selectName(strings.Split(strings.ReplaceAll(name, `\`, "/"), "/")[0])
		return
	}
	if err := os.MkdirAll(filepath.Join(p.path, name), 0o777); err != nil {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A mappa nem hozható létre:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return
	}
	p.refresh()
	p.selectName(strings.Split(filepath.Clean(name), `\`)[0])
}

func (a *App) rename() {
	p := a.active
	e := p.currentEntry()
	if e == nil || e.IsParent {
		return
	}
	if p.kind() == "net" {
		return
	}
	if _, isArc := p.vfs.(Archive); isArc {
		walk.MsgBox(a.mw, appName, T("Archívumon belül az átnevezés nem támogatott."), walk.MsgBoxIconInformation)
		return
	}
	nn, ok := inputBox(a.mw, T("Átnevezés"), fmt.Sprintf(T("„%s” új neve:"), e.Name), e.Name, !e.IsDir)
	nn = strings.TrimSpace(nn)
	if !ok || nn == "" || nn == e.Name {
		return
	}
	if f, isFTP := p.vfs.(remoteVFS); isFTP {
		if err := f.Rename(e.Path, f.Join(p.vpath, nn)); err != nil {
			walk.MsgBox(a.mw, appName, fmt.Sprintf(T("Az átnevezés nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
			return
		}
		p.refresh()
		p.selectName(nn)
		return
	}
	dst := filepath.Join(p.path, nn)
	if _, err := os.Lstat(dst); err == nil && !strings.EqualFold(nn, e.Name) {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("Már létezik ilyen nevű elem:\n%s"), nn), walk.MsgBoxIconWarning)
		return
	}
	if err := os.Rename(e.Path, dst); err != nil {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("Az átnevezés nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return
	}
	p.refresh()
	p.selectName(nn)
}

func (a *App) transfer(move bool) {
	p := a.active
	items := p.selectedEntries()
	dlog("transfer (mozgatás: %v) %s panel: %d elem, kurzor %d, jelölt %d, kind %s→%s",
		move, p.side, len(items), p.cursorRow(), len(p.model.marked), p.kind(), a.other().kind())
	if len(items) == 0 {
		return
	}
	if p.vfs != nil || a.other().vfs != nil {
		a.vfsTransfer(p, a.other(), items, move)
		return
	}
	title, verb := T("Másolás"), T("%s másolása ide:")
	if move {
		title, verb = T("Mozgatás / átnevezés"), T("%s mozgatása ide:")
	}
	t, ok, bg := inputBoxEx(a.mw, title, fmt.Sprintf(verb, describe(items)), a.other().path, false, true)
	dlog("  cél bekérése: ok=%v, %q", ok, t)
	t = strings.Trim(strings.TrimSpace(t), `"`)
	if !ok || t == "" {
		return
	}
	if len(t) == 2 && t[1] == ':' {
		t += `\`
	}
	if !filepath.IsAbs(t) {
		t = filepath.Join(p.path, t)
	}
	srcs := make([]string, len(items))
	for i, e := range items {
		srcs[i] = e.Path
	}
	if bg {
		a.queueTransfer(p, srcs, t, move)
		return
	}
	a.runTransfer(p, srcs, t, move)
}

func (a *App) runTransfer(src *Panel, srcs []string, target string, move bool) {
	dlog("  runTransfer: %v → %s (mozgatás: %v)", srcs, target, move)
	clean := filepath.Clean(target)
	for _, s := range srcs {
		st, err := os.Stat(s)
		if err == nil && st.IsDir() {
			ls, lt := strings.ToLower(filepath.Clean(s)), strings.ToLower(clean)
			if lt == ls || strings.HasPrefix(lt, ls+`\`) {
				walk.MsgBox(a.mw, appName, fmt.Sprintf(T("Egy mappa nem másolható / mozgatható önmagába:\n%s"), s), walk.MsgBoxIconWarning)
				return
			}
		}
	}
	op, title := uint32(foCopy), T("Másolás")
	if move {
		op, title = foMove, T("Mozgatás")
	}
	what := filepath.Base(srcs[0])
	if len(srcs) > 1 {
		what = Nf(len(srcs), "%d elem")
	}
	// A Windows másolóablaka saját szálon fut, a program közben használható
	// (ez a „háttérbe küldés”); a sor ablakában is látszik, kilépéskor rákérdez.
	it := a.queue.Adopt(title+": "+what+" → "+target, T("a Windows másolóablakában"))
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		procCoInitializeEx.Call(0, 2)
		defer procCoUninitialize.Call()
		hr, aborted, err := fileOp(0, op, srcs, target, fofAllowUndo|fofNoConfirmMkdir|fofxShowElevationPrompt|fofxAddUndoRecord)
		dlog("IFileOperation(%d) %v → %s: 0x%X, megszakítva: %v, hiba: %v", op, srcs, target, hr, aborted, err)
		var fail error
		switch {
		case err != nil && hr == 0:
			fail = err
		case err != nil: // a COM-objektum sem jött létre: régi út
			code, ab := shellFileOp(0, op, srcs, target, uint16((fofAllowUndo|fofNoConfirmMkdir)&0xFFFF))
			if ab || code == errCancelled {
				fail = errCancel
			} else if code != 0 {
				fail = fmt.Errorf("%s (0x%X)", shellOpError(code), code)
			}
		case aborted || isCancelHR(hr):
			fail = errCancel
		case hr != 0:
			fail = fmt.Errorf("%s", hresultText(hr))
		}
		a.mw.Synchronize(func() {
			a.queue.Finish(it, fail, nil)
			if fail != nil && fail != errCancel {
				walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A művelet nem sikerült:\n%s"), errText(fail)), walk.MsgBoxIconWarning)
			}
			if fail == nil && src != nil {
				src.model.marked = map[string]bool{}
			}
			a.refreshBoth()
		})
	}()
}

func (a *App) deleteSelected(permanent bool) {
	p := a.active
	items := p.selectedEntries()
	if len(items) == 0 {
		return
	}
	if p.vfs != nil {
		a.vfsDelete(p, items)
		return
	}
	msg := fmt.Sprintf(T("%s áthelyezése a Lomtárba?"), describe(items))
	icon := walk.MsgBoxIconQuestion
	if permanent {
		msg = fmt.Sprintf(T("%s VÉGLEGES törlése?\nEz nem vonható vissza."), describe(items))
		icon = walk.MsgBoxIconWarning
	}
	if walk.MsgBox(a.mw, T("Törlés"), msg, walk.MsgBoxYesNo|icon) != win.IDYES {
		return
	}
	paths := make([]string, len(items))
	for i, e := range items {
		paths[i] = e.Path
	}
	flags := uint32(fofNoConfirmation | fofxShowElevationPrompt)
	if !permanent {
		flags |= fofAllowUndo | fofWantNukeWarning | fofxRecycleOnDelete | fofxAddUndoRecord
	}
	if a.localFileOp(foDelete, paths, "", flags) {
		p.model.marked = map[string]bool{}
	}
	a.refreshBoth()
	p.tv.SetFocus()
}

func (a *App) reportOp(code uintptr, aborted bool) {
	if code != 0 && code != errCancelled {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A művelet nem sikerült (hibakód: 0x%X).\n%s"), code, shellOpError(code)),
			walk.MsgBoxIconWarning)
	}
}

func (a *App) dropFiles(files []string) {
	if len(files) == 0 {
		return
	}
	target := a.active
	var pt win.POINT
	if win.GetCursorPos(&pt) {
		for _, p := range []*Panel{a.left, a.right} {
			var r win.RECT
			if win.GetWindowRect(p.tv.Handle(), &r) && pt.X >= r.Left && pt.X < r.Right && pt.Y >= r.Top && pt.Y < r.Bottom {
				target = p
			}
		}
	}
	move := walk.ModifiersDown()&walk.ModShift != 0
	where := target.path
	if target.vfs != nil {
		where = target.vfs.Display(target.vpath)
	}
	if !a.confirmDrop(files, where, move) {
		return
	}
	a.setActive(target)
	if target.vfs != nil {
		a.localToVFS(nil, files, target, move)
		return
	}
	a.runTransfer(nil, files, target.path, move)
}

// ── kijelölés ──

func (a *App) selectPattern(add bool) {
	title := T("Kijelölés mintával")
	if !add {
		title = T("Kijelölés megszüntetése")
	}
	t, ok := inputBox(a.mw, title, T("Minta, több is megadható pontosvesszővel; mappához tegyél \\ jelet a végére (pl. *.txt;*.jpg):"), a.lastPattern, false)
	if !ok || strings.TrimSpace(t) == "" {
		return
	}
	a.lastPattern = t
	var pats []string
	for _, s := range strings.Split(t, ";") {
		if s = strings.TrimSpace(s); s != "" {
			pats = append(pats, s)
		}
	}
	a.active.markPattern(pats, add)
	a.active.tv.SetFocus()
}

func (a *App) copyPaths() { a.copyList(true) }
func (a *App) copyNames() { a.copyList(false) }

func (a *App) copyList(full bool) {
	var lines []string
	for _, e := range a.active.selectedEntries() {
		if full {
			lines = append(lines, e.Path)
		} else {
			lines = append(lines, e.Name)
		}
	}
	if len(lines) > 0 {
		walk.Clipboard().SetText(strings.Join(lines, "\r\n"))
	}
}

func (a *App) compareDirs() {
	files := func(p *Panel) map[string]*Entry {
		m := map[string]*Entry{}
		for _, e := range p.model.items {
			if !e.IsDir {
				m[strings.ToLower(e.Name)] = e
			}
		}
		return m
	}
	fa, fb := files(a.left), files(a.right)
	diff := func(mine, other map[string]*Entry) map[string]bool {
		out := map[string]bool{}
		for k, e := range mine {
			o, ok := other[k]
			if !ok {
				out[e.Name] = true
				continue
			}
			dt := e.ModTime.Sub(o.ModTime)
			if dt > 2*time.Second || (dt >= -2*time.Second && dt <= 2*time.Second && e.Size != o.Size) {
				out[e.Name] = true
			}
		}
		return out
	}
	da, db := diff(fa, fb), diff(fb, fa)
	a.left.setMarks(da)
	a.right.setMarks(db)
	if len(da) == 0 && len(db) == 0 {
		walk.MsgBox(a.mw, appName, T("A két mappa fájljai megegyeznek."), walk.MsgBoxIconInformation)
	}
}

func (a *App) confirmDrop(files []string, where string, move bool) bool {
	verb := T("%s másolása ide:")
	if move {
		verb = T("%s mozgatása ide:")
	}
	what := Nf(len(files), "%d elem")
	if len(files) == 1 {
		what = fmt.Sprintf(T("„%s”"), filepath.Base(files[0]))
	}
	return walk.MsgBox(a.mw, appName, fmt.Sprintf(verb, what)+"\n"+where, walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) == win.IDYES
}

// handleDrop: a panelre (vagy annak egy mappájára) ejtett fájlok feldolgozása.
func (a *App) handleDrop(p *Panel, files []string, target string, move, right bool) {
	var src *Panel
	if a.dragSrc != nil && len(a.dragFiles) == len(files) && strings.EqualFold(a.dragFiles[0], files[0]) {
		src = a.dragSrc
	}
	a.dragSrc, a.dragFiles = nil, nil
	if right {
		switch popupChoice(a.mw.Handle(), []string{T("Másolás ide"), T("Mozgatás ide"), "-", T("Mégse")}) {
		case 1:
			move = false
		case 2:
			move = true
		default:
			return
		}
	} else if !a.confirmDrop(files, target, move) {
		return
	}
	a.setActive(p)
	if p.vfs != nil {
		a.localToVFS(nil, files, p, move)
		return
	}
	a.runTransfer(src, files, target, move)
}

// shellOpError a SHFileOperation gyakori hibakódjainak magyarázata.
func shellOpError(code uintptr) string {
	switch code {
	case 0x71:
		return T("A forrás és a cél ugyanaz.")
	case 0x72:
		return T("Több forrás, de egyetlen célfájl lett megadva.")
	case 0x73, 0x74:
		return T("Más meghajtóra / mappába nem lehet így átnevezni.")
	case 0x75:
		return T("A művelet a felhasználó vagy a rendszer által megszakadt.")
	case 0x76:
		return T("Egy mappa nem másolható / mozgatható önmagába.")
	case 0x78:
		return T("Hozzáférés megtagadva (a fájlt valószínűleg egy másik program használja).")
	case 0x79:
		return T("Az útvonal túl hosszú.")
	case 0x7C:
		return T("A forrás vagy a cél útvonala érvénytelen.")
	case 0x80:
		return T("A cél már létezik.")
	case 0x81:
		return T("A célútvonal túl hosszú.")
	case 0x86:
		return T("A forrás írásvédett adathordozón van.")
	case 0x402:
		return T("Ismeretlen hiba a cél elérésekor.")
	}
	if e := syscall.Errno(code); e != 0 {
		return e.Error()
	}
	return ""
}

// localFileOp helyi másolás / mozgatás / törlés; sikeres-e. Az IFileOperation kéri
// a rendszergazdai jóváhagyást, ha kell; ha nem érhető el, a régi SHFileOperation fut.
func (a *App) localFileOp(op uint32, srcs []string, target string, flags uint32) bool {
	hr, aborted, err := fileOp(a.mw.Handle(), op, srcs, target, flags)
	dlog("IFileOperation(%d) %v → %s: 0x%X, megszakítva: %v, hiba: %v", op, srcs, target, hr, aborted, err)
	if err != nil && hr == 0 {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A művelet nem sikerült:\n%s"), err.Error()), walk.MsgBoxIconWarning)
		return false
	}
	if err != nil { // a COM-objektum sem jött létre: régi út
		code, ab := shellFileOp(a.mw.Handle(), op, srcs, target, uint16(flags&0xFFFF))
		dlog("SHFileOperation(%d): kód 0x%X, megszakítva: %v", op, code, ab)
		a.reportOp(code, ab)
		return code == 0 && !ab
	}
	if hr != 0 && !isCancelHR(hr) {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("A művelet nem sikerült:\n%s"), hresultText(hr)), walk.MsgBoxIconWarning)
		return false
	}
	return hr == 0 && !aborted
}

// migrateSettings: a korábbi „DuoCommander” beállításait átveszi, ha még nincs újabb.
func migrateSettings() {
	base := os.Getenv("APPDATA")
	if base == "" {
		return
	}
	newP := filepath.Join(base, appDir, appDir, "settings.ini")
	if _, err := os.Stat(newP); err == nil {
		return
	}
	if b, err := os.ReadFile(filepath.Join(base, "DuoCommander", "DuoCommander", "settings.ini")); err == nil {
		os.MkdirAll(filepath.Dir(newP), 0o777)
		os.WriteFile(newP, b, 0o666)
	}
}
