# Változások / Changelog

A kiadások leírása innen készül: a GitHub Actions a verzióhoz tartozó szakaszt
teszi a kiadás oldalára. Új kiadás előtt ide kerül egy új `## X.YY` szakasz.

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
