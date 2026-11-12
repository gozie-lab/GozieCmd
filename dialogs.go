//go:build windows

package main

// Párbeszédablakok: szövegbekérés, nézőke, súgó.

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

func inputBox(owner walk.Form, title, label, text string, selectStem bool) (string, bool) {
	s, ok, _ := inputBoxEx(owner, title, label, text, selectStem, false)
	return s, ok
}

// inputBoxEx: allowBg esetén „Háttérben (F2)” gomb is van; a harmadik érték jelzi, ha azt választották.
func inputBoxEx(owner walk.Form, title, label, text string, selectStem, allowBg bool) (string, bool, bool) {
	var dlg *walk.Dialog
	var le *walk.LineEdit
	var ok, cancel, bgBtn *walk.PushButton
	var result string // az ablak bezárása után a mező már nem olvasható, ezért itt őrizzük
	background := false
	goBg := func() {
		result = le.Text()
		background = true
		dlg.Accept()
	}
	err := Dialog{
		AssignTo:      &dlg,
		Title:         title,
		DefaultButton: &ok,
		CancelButton:  &cancel,
		MinSize:       Size{Width: 680, Height: 150},
		Layout:        VBox{},
		Children: []Widget{
			Label{Text: label},
			LineEdit{AssignTo: &le, Text: text, OnKeyDown: func(k walk.Key) {
				if k == walk.KeyF2 && allowBg {
					goBg()
				}
			}},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &ok, Text: "OK", OnClicked: func() {
						result = le.Text()
						dlg.Accept()
					}},
					PushButton{AssignTo: &bgBtn, Text: T("Háttérben (F2)"), Visible: allowBg, OnClicked: goBg},
					PushButton{AssignTo: &cancel, Text: T("Mégse"), OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		return "", false, false
	}
	themeDialog(dlg)
	if theApp != nil && theApp.icon != nil {
		dlg.SetIcon(theApp.icon)
	}
	dlg.Starting().Attach(func() {
		le.SetFocus()
		end := len(utf16.Encode([]rune(text)))
		if selectStem {
			if ext := filepath.Ext(text); ext != "" && ext != text {
				end -= len(utf16.Encode([]rune(ext)))
			}
		}
		// A szöveg újraírása a mező görgetését az elejére állítja; utána kijelölünk úgy,
		// hogy a kurzor elöl legyen (EM_SETSEL fordítva), és oda is görgetünk.
		// Minden méretváltozáskor megismételjük, amíg a felhasználó nem kezdett gépelni,
		// mert a végleges szélességet az elrendezés csak később adja meg.
		sel := func() {
			if le.Text() != text {
				return
			}
			h := le.Handle()
			le.SetText(text)
			win.SendMessage(h, 0x00B1, uintptr(end), 0) // EM_SETSEL
			win.SendMessage(h, 0x00B7, 0, 0)            // EM_SCROLLCARET
		}
		sel()
		le.SizeChanged().Attach(sel)
		dlg.Synchronize(sel)
	})
	res := dlg.Run()
	dlog("  inputBox(%q): eredmény %d, szöveg %q", title, res, result)
	if res != walk.DlgCmdOK {
		return "", false, false
	}
	return result, true, background
}

const viewLimit = 4 << 20

func loadText(path string) (text, enc string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, viewLimit+1))
	if err != nil {
		return "", "", err
	}
	truncated := len(data) > viewLimit
	if truncated {
		data = data[:viewLimit]
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		u := make([]uint16, (len(data)-2)/2)
		for i := range u {
			u[i] = uint16(data[2+2*i]) | uint16(data[3+2*i])<<8
		}
		text, enc = string(utf16.Decode(u)), "UTF-16"
	case bytes.IndexByte(head, 0) >= 0:
		limit := len(data)
		if limit > 256<<10 {
			limit, truncated = 256<<10, true
		}
		text, enc = hexDump(data[:limit]), T("bináris (hexa)")
	case utf8.Valid(data):
		text, enc = string(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})), "UTF-8"
	default:
		text, enc = ansiToString(data), T("ANSI (rendszer kódlap)")
	}
	if truncated {
		text += T("\n\n[… a fájl további része nem jelenik meg]")
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r\n")
	return text, enc, nil
}

func hexDump(b []byte) string {
	var sb strings.Builder
	for off := 0; off < len(b); off += 16 {
		end := off + 16
		if end > len(b) {
			end = len(b)
		}
		fmt.Fprintf(&sb, "%08x  ", off)
		for i := off; i < off+16; i++ {
			if i < end {
				fmt.Fprintf(&sb, "%02x ", b[i])
			} else {
				sb.WriteString("   ")
			}
		}
		sb.WriteString(" |")
		for _, c := range b[off:end] {
			if c >= 32 && c < 127 {
				sb.WriteByte(c)
			} else {
				sb.WriteByte('.')
			}
		}
		sb.WriteString("|\n")
	}
	return sb.String()
}

func showViewer(owner walk.Form, path string) {
	if isMarkdown(path) && showMarkdown(owner, path) {
		return
	}
	text, enc, err := loadText(path)
	if err != nil {
		walk.MsgBox(owner, appName, fmt.Sprintf(T("A fájl nem olvasható:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return
	}
	var dlg *walk.Dialog
	var te *walk.TextEdit
	var closeBtn *walk.PushButton
	err = Dialog{
		AssignTo:      &dlg,
		Title:         fmt.Sprintf(T("Nézőke – %s"), path),
		DefaultButton: &closeBtn,
		CancelButton:  &closeBtn,
		MinSize:       Size{Width: 500, Height: 300},
		Size:          Size{Width: 980, Height: 680},
		Layout:        VBox{},
		Children: []Widget{
			TextEdit{AssignTo: &te, ReadOnly: true, VScroll: true, HScroll: true,
				Font: Font{Family: "Consolas", PointSize: 10}},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: fmt.Sprintf(T("Kódolás: %s"), enc)},
					HSpacer{},
					PushButton{AssignTo: &closeBtn, Text: T("Bezárás (Esc)"), OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		walk.MsgBox(owner, appName, err.Error(), walk.MsgBoxIconError)
		return
	}
	themeDialog(dlg)
	theApp.sizeableDialog(dlg, owner, "viewer", 980, 680)
	win.SendMessage(te.Handle(), win.EM_SETLIMITTEXT, 0x7FFFFFFE, 0)
	te.SetText(text)
	te.KeyDown().Attach(func(key walk.Key) {
		if key == walk.KeyF3 || key == walk.KeyF10 {
			dlg.Cancel()
		}
	})
	dlg.Starting().Attach(func() {
		te.SetFocus()
		te.SetTextSelection(0, 0)
	})
	dlg.Run()
}

const helpText = `NAVIGÁCIÓ
  Tab                     váltás a panelek között
  Enter / dupla kattintás mappa megnyitása, fájl megnyitása a társított programmal
  Backspace, Ctrl+PgUp    szülőmappa
  Ctrl+Home               gyökérmappa
  Ctrl+L                  ugrás megadott mappára (vagy kattints az útvonalsávra)
  Ctrl+B                  vissza az előző mappába
  Ctrl+← / Ctrl+→         a kurzor alatti mappa megnyitása a bal / jobb panelen
  Ctrl+E                  ugyanaz a mappa a másik panelen
  Ctrl+U                  panelek cseréje

NÉZETEK, GYORSNÉZET
  Ctrl+F2 / Ctrl+F1       részletes / rövid nézet (sok fájl több oszlopban)
  Ctrl+Shift+F1           bélyegkép nézet (a képek kicsinyítve, a többi nagy ikonnal)
                          A nézet panelenként megmarad.
  Ctrl+Q                  gyorsnézet: a másik panel a kurzor alatti fájl tartalmát
                          mutatja (szöveg, kép, mappaadatok); újra Ctrl+Q: vissza

HÁTTÉRMŰVELETEK, SZINKRONIZÁLÁS
  F5 / F6 ablakában F2    a másolás / mozgatás a háttérben, sorban fut (a Windows
                          másolóablakában, sebességgel); közben tovább dolgozhatsz.
                          Jelzője a parancssor mellett, a sor ablaka: Ctrl+J
  Mappák szinkronizálása  (Parancsok menü) a két panel mappáját veti össze
                          (almappákkal, kérésre tartalom szerint is); az irány
                          soronként váltható: kattintás az irányjelre vagy Szóköz

LAPFÜLEK, KEDVENCEK, KERESÉS A LISTÁBAN
  Ctrl+T / Ctrl+W         új fül (a kurzor alatti mappával) / fül bezárása
  Ctrl+Tab, Ctrl+Shift+Tab következő / előző fül; a fülekre kattintva is,
                          középső gombbal a fül bezárul. Kilépéskor megmaradnak.
  Ctrl+D                  kedvenc mappák: ugrás, hozzáadás, szerkesztés
  Alt+↓                   a legutóbb látogatott mappák listája
  Ctrl+S                  gyorskeresés: gépeld a név elejét (* után bárhol);
                          ↑/↓ következő találat, Enter megnyit, Esc vége.
                          Ékezet és kis/nagybetű nem számít. Beállítható, hogy
                          a sima gépelés is gyorskeresés legyen (Beállítások → Kezelés).

KIJELÖLÉS (piros)
  Insert                  jelölés és tovább lépés
  Szóköz                  jelölés (mappánál a méretet is kiszámolja)
  Shift+↑/↓               jelölés nyilakkal
  Ctrl+kattintás          egy elem jelölése; Shift+kattintás: tartomány
  Num + / Num − / Num *   jelölés mintával / jelölés törlése / megfordítás
  Ctrl+A                  mindet kijelöl
  Shift+F2                mappák összehasonlítása (az eltérő fájlokat jelöli)

MŰVELETEK
  F2                      frissítés
  F3                      nézőke (szöveg, Markdown formázva, hexa; képet a
                          társított program nyit)
  F4 / Shift+F4           szerkesztés / új fájl
  F5 / F6                 másolás / mozgatás a másik panelre
  Shift+F6                átnevezés
  F7                      új mappa
  F8, Delete              törlés a Lomtárba
  Shift+F8, Shift+Delete  végleges törlés
  F9                      terminál az aktuális mappában (a Beállításokban választható)
  Beállítások             a gombsor végén, vagy Nézet menü: téma, színek, eszköztár,
                          egér, beállításfájl, szerkesztő, terminál
  Ctrl+H                  rejtett fájlok mutatása
  Ctrl+F3…F6              rendezés név / kiterjesztés / dátum / méret szerint
  Ctrl+Shift+C            kijelöltek útvonala a vágólapra
  Jobb gomb               helyi menü (Tulajdonságok is)

PARANCSSOR (gépelj a listában, vagy Ctrl+K)
  Enter                   futtatás az aktuális mappában
  Shift+Enter             futtatás parancsablakban, ami nyitva marad
  cd <mappa>, d:          mappa- / meghajtóváltás
  Tab                     vissza a listára

ESZKÖZÖK (Eszközök menü)
  Összehasonlítás         a kurzor alatti fájlt a másik panel azonos nevű (vagy kurzor
                          alatti) fájljával, illetve a két kijelölt fájlt egymás mellett
                          mutatja; a módosult, hiányzó és új sorok színezve. F7 / Shift+F7:
                          következő / előző eltérés. Bináris fájlnál a bájteltérések.
  Ellenőrzőösszeg         MD5, SHA-1, SHA-256, SHA-512 a kijelölt fájlokra (mappákra
                          rekurzívan); mentés .md5 / .sha256 … fájlba vagy vágólapra.
                          Ellenőrzés: kurzor egy ilyen fájlon → Eszközök menü.
  Ismétlődő fájlok        tartalom (vagy név + méret) szerint, több mappában is; a
                          fölösleges példányok kijelölhetők és a Lomtárba tehetők
                          (minden csoportból legalább egy megmarad).
  Darabolás, összefűzés   .001, .002 … darabok + .crc leíró (a szokásos formátumban);
                          összefűzéskor a CRC alapján ellenőriz.
  Attribútumok, dátum     csak olvasható / rejtett / rendszer / archiválandó és a
                          módosítás dátuma, kérésre almappákkal együtt.

TESTRESZABÁS
  Billentyűparancsok      Beállítások / Billentyűk, színek: bármely parancs kaphat saját
                          billentyűt, egy beépített billentyű átállítható vagy kikapcsolható
                          (a saját beállítás mindig elsőbbséget kap)
  Saját menü              a menüsor „Saját” menüje: programok, parancsok, paraméterekkel,
                          mint az eszköztár gombjai; billentyűre is köthető felugró menüként
  Fájltípusok színezése   ugyanott: minták (pl. *.jpg;*.png) és szín; alapból képek,
                          archívumok, programok, zene és videó, dokumentumok, jegyzetek;
                          sötét témában világosabb

ARCHÍVUMOK
  Formátumok              ZIP, 7Z, RAR, TAR, TAR.GZ/TGZ, TAR.XZ, TAR.BZ2, GZ, BZ2, XZ;
                          7-Zippel: CAB, ARJ, LZH is (ACE és UC2 nem lehetséges)
  Írható                  ZIP, TAR, TAR.GZ, TAR.XZ beépítve; 7Z a telepített 7-Zippel
  Enter az archívumon     belépés, böngészés mint egy mappában
  F5 / F6                 kicsomagolás a másik panelre, vagy fájlok hozzáadása;
                          két archívum (vagy archívum és FTP) között is közvetlenül
  F3, Enter fájlon        megtekintés / megnyitás (ideiglenes kicsomagolással)
  F7 / F8                 mappa létrehozása / törlés az archívumban
  Alt+F5, Ctrl+P          becsomagolás (formátum és tömörítési szint választható)
  Alt+F9, Ctrl+Shift+P    kijelölt archívumok kicsomagolása
  Backspace a gyökérben   kilépés az archívumból

TÖBBFÁJL-ÁTNEVEZÉS (Ctrl+M)
  Maszkok: [N] név, [E] kiterjesztés, [N2-5] részlet, [C] számláló, [P] mappa,
  [YMD] dátum; keresés és csere (reguláris kifejezéssel is), kis/nagybetű.
  Élő előnézet, ütközésvédelem és visszavonás.

FÁJLKERESÉS (Alt+F7)
  Név (maszkok vagy névrészlet), tartalom, méret és dátum szerint; dupla
  kattintásra a panel a talált fájlra ugrik.

TÉMA
  Beállítások → Megjelenés: Világos, Sötét vagy Automatikus (a Windows
  „alkalmazásmód” beállítását követi, menet közben is). A színek és betűk
  témánként külön tárolódnak.

ESZKÖZTÁR
  Nézet → Eszköztár: be- és kikapcsolás. Beállítások → Kezelés: ikonméret
  (16–48 képpont) és a gombok szerkesztése.
  Jobb gomb az eszköztáron vagy Nézet menü: gombok hozzáadása, sorrend,
  belső parancsok vagy külső programok (%P mappa, %N név, %F teljes út,
  %S kijelöltek, %T másik panel), saját ikon.

TÁVOLI KAPCSOLATOK (Távoli menü)
  Protokollok             FTP, FTPS (titkosított FTP), SFTP (SSH), WebDAV (https / http)
  Ctrl+F                  mentett kapcsolatok (jelszó nélkül tárolva), új / szerkesztés,
                          gyors kapcsolódás
  SFTP                    jelszóval vagy SSH-kulccsal (a ~/.ssh kulcsait magától is
                          kipróbálja); új szerver gépkulcsát első kapcsolódáskor
                          jóvá kell hagyni, megváltozott kulcsnál figyelmeztet
  WebDAV                  a szerver mezőbe az útvonal is írható (pl. Nextcloud:
                          felho.hu/remote.php/dav/files/nev)
  F5 / F6                 letöltés / feltöltés a másik panellel
  F3, Enter fájlon        megtekintés / megnyitás letöltés után
  F7 / F8 / Shift+F6      új mappa / törlés / átnevezés a szerveren
  Kapcsolat bontása       gomb a panel tetején, Ctrl+Shift+F, vagy Backspace a gyökérben
  Tétlenség               a kapcsolatot a program egy idő után magától bontja
                          (alapból 15 perc; Beállítások / Kezelés)

HÁLÓZAT
  Ctrl+N                  hálózati gépek (vagy a meghajtólista végén: „Hálózat”)
  Ctrl+Shift+N            gép vagy megosztás megnyitása név / IP alapján
  \\gép\megosztás         a parancssorba vagy Ctrl+L-lel is beírható
  Enter a gépen           a gép megosztásai; Enter a megosztáson: belépés
  Backspace               megosztás gyökeréből vissza a gép megosztásaihoz
  F8 a géplistában        gép eltávolítása a megjegyzett listából
  Ha a megosztás jelszót kér, a Windows bejelentkező ablaka jelenik meg.
  Menü: hálózati meghajtó csatolása / leválasztása

EGÉR
  Kattintás               kurzor; Ctrl+kattintás: jelölés; Shift+kattintás: tartomány
  Dupla kattintás         mappa / fájl / ZIP megnyitása
  Jobb gomb               helyi menü
  Jobb gombos kijelölés   (Beállítások → Kezelés) a jobb gomb jelöl, húzva több
                          sort is; 1 mp-ig mozdulatlanul nyomva tartva helyi menü
  Oldalsó gombok          vissza / előre az előzményekben

HÚZD ÉS EJTSD
  Húzás a másik panelre   másolás; mappára ejtve abba a mappába, „..”-ra a szülőbe
  Shift + húzás           mozgatás
  Jobb gombos húzás       ejtéskor menü: Másolás ide / Mozgatás ide / Mégse
  Jelölt fájlon kezdve    az összes jelölt fájl megy
  Az Intézőbe, asztalra, levelezőbe is húzhatsz, és onnan vissza – ZIP-be és
  FTP-re is (ilyenkor tömörít / feltölt)

BEÁLLÍTÁSFÁJL
  Beállítások → Kezelés: mentés fájlba / betöltés fájlból (másik gépre vihető).
  Hordozható mód: ha a program mellett GozieCmd.ini van, azt használja
  (pendrive-ról futtatva minden beállítás vele utazik).

Saját szerkesztő a GOZIE_EDITOR környezeti változóval adható meg.`

func (a *App) help() {
	var dlg *walk.Dialog
	var closeBtn *walk.PushButton
	err := Dialog{
		AssignTo:      &dlg,
		Title:         T("Súgó – billentyűparancsok"),
		DefaultButton: &closeBtn,
		CancelButton:  &closeBtn,
		Size:          Size{Width: 720, Height: 700},
		Layout:        VBox{},
		Children: []Widget{
			TextEdit{ReadOnly: true, VScroll: true, Text: strings.ReplaceAll(helpT(helpText), "\n", "\r\n"),
				Font: Font{Family: "Consolas", PointSize: 10}},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &closeBtn, Text: T("Bezárás"), OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(a.mw)
	if err != nil {
		return
	}
	themeDialog(dlg)
	a.sizeableDialog(dlg, a.mw, "help", 760, 780)
	dlg.Run()
}

// showAbout: névjegy a honlap és a hibabejelentési cím linkjével.
func (a *App) showAbout() {
	var dlg *walk.Dialog
	var ok *walk.PushButton
	mail := contactEmail()
	open := func(link *walk.LinkLabelLink) { shellOpen(a.mw.Handle(), link.URL(), "") }
	err := Dialog{
		AssignTo:      &dlg,
		Title:         T("Névjegy"),
		DefaultButton: &ok,
		CancelButton:  &ok,
		MinSize:       Size{Width: 380},
		Layout:        VBox{},
		Children: []Widget{
			// TextLabel: a többsoros szöveg minden sora látszik (a Label csak egy sort mutat)
			TextLabel{Text: aboutText()},
			VSpacer{Size: 6},
			Label{Text: T("Hiba, észrevétel, ötlet:")},
			LinkLabel{
				Text:            fmt.Sprintf(`<a href="mailto:%s?subject=%s">%s</a>`, mail, url.PathEscape(appName+" "+appVersion), mail),
				Alignment:       AlignHNearVCenter,
				OnLinkActivated: open,
			},
			LinkLabel{
				Text:            fmt.Sprintf(`<a href="%s">%s</a>`, homePage, strings.TrimSuffix(strings.TrimPrefix(homePage, "https://"), "/")),
				Alignment:       AlignHNearVCenter,
				OnLinkActivated: open,
			},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &ok, Text: "OK", OnClicked: func() { dlg.Accept() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		return
	}
	themeDialog(dlg)
	dlg.Run()
}
