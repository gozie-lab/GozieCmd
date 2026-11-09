# Változások / Changelog

A kiadások leírása innen készül: a GitHub Actions a verzióhoz tartozó szakaszt
teszi a kiadás oldalára. Új kiadás előtt ide kerül egy új `## X.YY` szakasz.

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
