//go:build linux

package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

const helpTextLinux = `NAVIGÁCIÓ
  Tab                     váltás a panelek között
  Enter / dupla kattintás mappa megnyitása, fájl megnyitása a társított programmal
                          (futtatható fájl: elindul)
  Backspace, Ctrl+PgUp    szülőmappa;  Ctrl+Home (vagy Ctrl+Ű) a gyökérmappa
  Ctrl+L                  ugrás megadott mappára (vagy kattints az útvonalsávra)
  Alt+← / Alt+→           vissza / előre az előzményekben
  Ctrl+← / Ctrl+→         a kurzor alatti mappa megnyitása a bal / jobb panelen
  Ctrl+E                  ugyanaz a mappa a másik panelen
  Ctrl+U                  panelek cseréje
  Meghajtóválasztó        a rendszer, a saját mappa és a csatolt lemezek
                          (pendrive, /media, /run/media, /mnt)

LAPFÜLEK, KEDVENCEK, KERESÉS A LISTÁBAN
  Ctrl+T / Ctrl+W         új fül (a kurzor alatti mappával) / fül bezárása
  Ctrl+Tab, Ctrl+Shift+Tab következő / előző fül; a fülsor kattintható
                          (a fülek kilépéskor megmaradnak)
  Ctrl+D                  kedvenc mappák: ugrás, hozzáadás, szerkesztés
  Alt+↓                   a legutóbb látogatott mappák listája
  Ctrl+S                  gyorskeresés: gépeld a név elejét (* után bárhol);
                          ↑/↓ következő találat, Enter megnyit, Esc vége.
                          Ékezet és kis/nagybetű nem számít. Beállítható, hogy
                          a sima gépelés is gyorskeresés legyen (Beállítások → Kezelés).

KIJELÖLÉS
  Insert                  jelölés és tovább lépés
  Szóköz                  jelölés (mappánál a méretet is kiszámolja)
  Shift+↑/↓               jelölés nyilakkal
  Ctrl+kattintás          egy elem jelölése; Shift+kattintás: tartomány
  Num + / Num − / Num *   jelölés mintával / jelölés törlése / megfordítás
  Ctrl+A                  mindet kijelöli
  Shift+F2                mappák összehasonlítása (az eltérő fájlokat jelöli)

MŰVELETEK
  F2, Ctrl+R              frissítés
  F3                      nézőke (szöveg kódolásválasztással, Markdown formázva,
                          hexa; képet, videót a társított program nyit)
  F4 / Shift+F4           szerkesztés / új fájl
  F5 / F6                 másolás / mozgatás (a célmappa vagy új név megadható)
  Shift+F6                átnevezés
  F7                      új mappa
  F8, Delete              törlés a Kukába (a Dolphin és a Nautilus Kukájába)
  Shift+F8, Shift+Delete  végleges törlés
  F9                      terminál az aktuális mappában
  F10                     kilépés
  Ctrl+H                  rejtett (ponttal kezdődő) fájlok mutatása
  Ctrl+F2 / Ctrl+F1       részletes / rövid nézet (sok fájl több oszlopban)
  Ctrl+Shift+F1           bélyegkép nézet (a képek kicsinyítve)
  Ctrl+1 / Ctrl+2 / Ctrl+3 részletes / rövid / bélyegkép nézet (KDE alatt a
                          Ctrl+F1…F4 asztalt vált, ott ezeket használd)
  Ctrl+Q                  gyorsnézet: a másik panel a kurzor alatti fájl
                          tartalmát mutatja (szöveg, kép, mappaadatok)
  Ctrl+F3…F6, Ctrl+4…7    rendezés név / kiterjesztés / dátum / méret szerint
  Ctrl+Shift+C            kijelöltek útvonala a vágólapra
  Jobb gomb, Menü gomb    helyi menü

PARANCSSOR (gépelj a listában, vagy Ctrl+K)
  Enter                   futtatás az aktuális mappában (a háttérben)
  Shift+Enter             futtatás terminálban, ami nyitva marad
  cd <mappa>, cd ~, cd -  mappaváltás
  Esc                     a parancssor törlése

ESZKÖZÖK (Eszközök menü)
  Összehasonlítás         a kurzor alatti fájlt a másik panel azonos nevű (vagy kurzor
                          alatti) fájljával, illetve a két kijelölt fájlt egymás mellett
                          mutatja; a módosult, hiányzó és új sorok színezve. F7 / Shift+F7:
                          következő / előző eltérés. Bináris fájlnál a bájteltérések.
  Ellenőrzőösszeg         MD5, SHA-1, SHA-256, SHA-512 a kijelölt fájlokra (mappákra
                          rekurzívan); mentés .md5 / .sha256 … fájlba vagy vágólapra.
                          Ellenőrzés: kurzor egy ilyen fájlon → Eszközök menü.
  Ismétlődő fájlok        tartalom (vagy név + méret) szerint, több mappában is; a
                          fölösleges példányok kijelölhetők és a Kukába tehetők
                          (minden csoportból legalább egy megmarad).
  Darabolás, összefűzés   .001, .002 … darabok + .crc leíró (a szokásos formátumban);
                          összefűzéskor a CRC alapján ellenőriz.
  Jogok, dátum            olvasás / írás / futtatás tulajdonosnak, csoportnak, másoknak,
                          és a módosítás dátuma, kérésre almappákkal együtt (a mappák
                          belépési joga az olvasási joggal együtt megmarad).

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
                          7-Zippel (7zip / p7zip csomag): CAB, ARJ, LZH is
  Írható                  ZIP, TAR, TAR.GZ, TAR.XZ beépítve; 7Z a 7-Zippel
  Enter az archívumon     belépés, böngészés mint egy mappában
  F5 / F6                 kicsomagolás a másik panelre, vagy fájlok hozzáadása;
                          két archívum (vagy archívum és FTP) között is
  Alt+F5, Ctrl+P          becsomagolás;  Alt+F9, Ctrl+Shift+P kicsomagolás

HÁTTÉRMŰVELETEK, SZINKRONIZÁLÁS
  F5 / F6 ablakában F2    a másolás / mozgatás a háttérben, sorban fut; közben
                          tovább dolgozhatsz. Jelzője a parancssor mellett,
                          a sor ablaka: Ctrl+J (sebesség, hátralévő idő, hibák)
  Mappák szinkronizálása  (Parancsok menü) a két panel mappáját veti össze
                          (almappákkal, kérésre tartalom szerint is); soronként
                          kattintással állítható az irány (→, ←, kihagyás)

TÖBBFÁJL-ÁTNEVEZÉS (Ctrl+M)
  Maszkok: [N] név, [E] kiterjesztés, [N2-5] részlet, [C] számláló, [P] mappa,
  [YMD] dátum; keresés és csere (reguláris kifejezéssel is), kis/nagybetű.
  Élő előnézet, ütközésvédelem és visszavonás.

FÁJLKERESÉS (Alt+F7)
  Név (minta vagy névrészlet), tartalom, méret és dátum szerint;
  Enter vagy „Ugrás a fájlra”: a panel a talált fájlra áll.
  (KDE alatt az Alt+F7 az ablakmozgatásé lehet: használd a Parancsok menüt.)

TÁVOLI KAPCSOLATOK (Távoli menü)
  Protokollok             FTP, FTPS (titkosított FTP), SFTP (SSH), WebDAV (https / http),
                          SMB (Windows-gépek és NAS-ok megosztásai)
  Ctrl+F                  mentett kapcsolatok (jelszó nélkül tárolva), új / szerkesztés,
                          gyors kapcsolódás
  SFTP                    jelszóval vagy SSH-kulccsal (a ~/.ssh kulcsait magától is
                          kipróbálja); új szerver gépkulcsát első kapcsolódáskor
                          jóvá kell hagyni, megváltozott kulcsnál figyelmeztet
  WebDAV                  a szerver mezőbe az útvonal is írható (pl. Nextcloud:
                          felho.hu/remote.php/dav/files/nev)
  SMB                     kiszolgáló: gép vagy gép/megosztás; felhasználó: név vagy
                          TARTOMÁNY\név, üresen vendégként; a gyökérben a megosztások
  F5 / F6                 letöltés / feltöltés a másik panellel
  F3, Enter fájlon        megtekintés / megnyitás letöltés után
  F7 / F8 / Shift+F6      új mappa / törlés / átnevezés a szerveren
  Kapcsolat bontása       gomb a panel tetején, Ctrl+Shift+F, vagy Backspace a gyökérben
  Tétlenség               a kapcsolatot a program egy idő után magától bontja
                          (alapból 15 perc; Beállítások / Kezelés)

EGÉR
  Kattintás               kurzor; Ctrl+kattintás: jelölés; Shift+kattintás: tartomány
  Dupla kattintás         megnyitás
  Húzás a másik panelre   másolás (Shift-tel mozgatás); mappára ejtve abba a mappába
  Fájlkezelőből behúzva   másolás vagy mozgatás a panelre
  Jobb gomb               helyi menü, vagy (Beállítások → Kezelés) kijelölés,
                          1 mp nyomva tartás után helyi menü

BEÁLLÍTÁSOK
  Téma (világos, sötét, automatikus), felületméret, színek és betűk, eszköztár,
  szerkesztő, terminál. A beállításfájl a Windows-változattal is cserélhető.
  Hordozható mód: ha a program mellett GozieCmd.ini van, azt használja.
  Helye egyébként: ~/.config/GozieCommander/settings.ini`

func (a *App) help() {
	w := a.fa.NewWindow(T("Súgó – billentyűparancsok"))
	w.SetIcon(appIconRes)
	tg := widget.NewTextGridFromString(helpT(helpTextLinux))
	tg.Scroll = fyne.ScrollBoth
	w.SetContent(container.NewBorder(nil, container.NewCenter(widget.NewButton(T("Bezárás"), w.Close)), nil, nil, tg))
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyEscape || ev.Name == fyne.KeyF1 {
			w.Close()
		}
	})
	a.restoreWinSize(w, "help", 820, 760)
	w.SetOnClosed(func() { a.saveWinSize(w, "help") })
	w.Show()
}
