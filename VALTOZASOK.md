# Változások / Changelog

A kiadások leírása innen készül: a GitHub Actions a verzióhoz tartozó szakaszt
teszi a kiadás oldalára. Új kiadás előtt ide kerül egy új `## X.YY` szakasz.

## 0.57

### Flatpak-tároló / Flatpak repository

- A Flatpak-változat saját, aláírt tárolóból telepíthető, és ezután a Szoftverközpont
  (GNOME Szoftverek, Discover) vagy a `flatpak update` frissíti:
  `flatpak install --user https://gozie-lab.github.io/GozieCmd/hu.gozie.commander.flatpakref`
  A telepítési útmutató: https://gozie-lab.github.io/GozieCmd/
  *The Flatpak version can be installed from its own signed repository and is then
  updated by the software center or `flatpak update`.*
- A kiadásokhoz csatolt `.flatpak` fájl telepítéskor a tárolót is hozzáadja, így az is
  frissíthető. A 0.52-es `.flatpak` fájlból telepített példányt egyszer újra kell
  telepíteni a tárolóból vagy az új fájlból.
  *The attached `.flatpak` file now adds the repository on install. Installations from
  the 0.52 `.flatpak` file need to be reinstalled once.*
- Flatpakban a *Súgó › Frissítés keresése* egy gombbal frissít, majd újraindítja a programot.
  *In the Flatpak version, Help › Check for updates updates and restarts with one click.*

### Névjegy / About

- A Névjegy ablakban kattintható link vezet a hibabejelentési és észrevételi
  e-mail-címre, valamint a honlapra (cmd.gozie.hu).
  *The About window links to the contact e-mail address for bugs and feedback and to
  the website (cmd.gozie.hu).*

### Windows

- Induláskor az ablak egy lépésben, kész tartalommal jelenik meg a mentett helyén és
  méretében, teljes méretű ablaknál is, felvillanás nélkül.
  *At start-up the window appears in one step at its saved position and size, also
  when maximized, without flashing.*

## 0.52

- Flatpak-csomag: `GozieCmd-0.52-x86_64.flatpak`. Megnyitva a Szoftverközpont vagy a
  Discover telepíti, vagy terminálban: `flatpak install --user GozieCmd-0.52-x86_64.flatpak`.
  A 7-Zip benne van; a szerkesztő, a terminál, az eszköztár programjai és a fájlok
  megnyitása a rendszer saját programjaival működik; a beállítások közösek a
  kicsomagolt változattal.
  *Flatpak package. 7-Zip is included; editors, terminals, toolbar programs and file
  opening use the system's own programs; settings are shared with the extracted version.*
- Linux: az ablak azonosítója (WM_CLASS) állandó, így a tálca és a menü ikonja
  verzióváltás után is egyezik.
  *Linux: fixed window class, so the taskbar icon matches the menu entry.*

## 0.51

- Titkosított FTP (FTPS): a feltöltés nem akad el többé a második fájlnál, és
  nem marad hiányos vagy 0 bájtos fájl a szerveren. Az adatkapcsolat lezárása
  most szabályos, így a szerver a fájl végét is megkapja; az adatkapcsolat a
  vezérlőkapcsolat TLS-munkamenetét folytatja, ahogy sok szerver megköveteli.
  *FTPS: uploads no longer stall on the second file or leave truncated files –
  data connections are now closed properly and reuse the TLS session.*
- SFTP, WebDAV, SMB: ha a szerver 40 másodpercig nem válaszol, a program újra
  csatlakozik és megismétli a műveletet, ahelyett hogy örökké várna.
  *SFTP, WebDAV, SMB: a server silent for 40 seconds triggers a reconnect and retry.*
- Részletesebb napló az átvitelekről; az előző futás naplója `naplo-elozo.txt`
  néven megmarad.
  *More detailed transfer log; the previous run's log is kept as naplo-elozo.txt.*

## 0.50

- Feltöltés és letöltés után a program ellenőrzi, hogy a fájl teljes méretében
  megérkezett-e. Ha nem, hibát jelez, a hiányos fájlt eltávolítja a szerverről, és
  mozgatásnál a helyi példány megmarad.
  *Uploads and downloads are verified by size; an incomplete file is reported and
  removed, and on move the local copy is kept.*
- SFTP: az életjel-ellenőrzés átvitel közben szünetel, így egy lassú feltöltést
  nem szakít meg tévesen.
  *SFTP: the keep-alive check pauses during transfers, so it can't cut a slow upload.*
- Linux: a fájlkezelőből behúzott fájlok mozgatásakor hiba esetén a forrás megmarad.
  *Linux: when moving files dropped from another file manager, sources are kept on error.*
- Frissítéskor az „Újdonságok” gomb a GitHubon nyitja meg a változások listáját.
  *The update prompt has a “What's new” button that opens the change list on GitHub.*

## 0.49

- SFTP: egy hosszabb letöltés vagy feltöltés közben a program nem fagy le többé.
  Eddig a mappa frissítése megvárta az átvitel végét; most a listázás azonnal
  válaszol, az FTP pedig ilyenkor egy második kapcsolatot nyit.
  *SFTP: no more freezes during a long transfer – listing no longer waits for it.*
- SFTP: ha a kapcsolat csendben megszakad (alvó gép, hálózatváltás), a program
  legfeljebb kb. egy percen belül észreveszi, és nem vár rá a végtelenségig.
  *SFTP: a silently dropped connection is detected within about a minute.*
- FTP: a háttérben futó átvitel és a böngészés már nem zavarja össze egymást.
  *FTP: background transfers and browsing no longer interfere.*
- Windows: induláskor az ablak egyből a mentett helyén és méretében jelenik meg,
  a kisebb, félig fehér ablak felvillanása nélkül.
  *Windows: the window opens at its saved position without flashing.*
- A kiadások egységes neve „Gozie Commander X.YY”, leírással.
  *Releases are consistently named “Gozie Commander X.YY” with release notes.*

## 0.48

- Új README képernyőképekkel, magyarul és angolul.

## 0.47

- GitHub Actions: a szkriptek futtathatósága javítva.

## 0.46

- Gyorsabb témaváltás Windowson.
