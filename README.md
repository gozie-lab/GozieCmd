# Gozie Commander

Kétpaneles fájlkezelő Windowsra és Linuxra. · A two-panel file manager for Windows and Linux.

## Magyarul

A Gozie Commander a klasszikus kétpaneles fájlkezelők módján működik: két egymás melletti
panel, billentyűzetről gyorsan kezelhető műveletek.

**Tudja többek közt:** másolás, mozgatás és törlés háttérben, sorba állítva · fülek ·
gyorskeresés és keresés · tömeges átnevezés · könyvtárszinkronizálás · tömörített fájlok
(ZIP, 7z és mások) kezelése · FTP, SFTP, WebDAV és SMB kapcsolatok (jelszót soha nem
ment) · fájlösszehasonlítás · ellenőrzőösszegek · duplikátumkeresés · fájl darabolása és
összefűzése · attribútumok módosítása · gyorsnézet · bélyegképes nézet · változásfigyelés ·
saját billentyűparancsok, menü és fájltípus-színek · világos és sötét téma · magyar és
angol felület (további nyelvek fordítási sablonnal adhatók hozzá).

**Letöltés:** a [Releases](../../releases) oldalon található a Windows (`.zip`) és a Linux
(`.tar.gz`) csomag. Windowson csak ki kell csomagolni és elindítani a `GozieCmd.exe`-t,
Linuxon a `telepit.sh` teszi be a menübe.

## In English

Gozie Commander follows the classic two-panel file manager layout, built for fast keyboard
use. Features include queued background file operations, tabs, quick search, multi-rename,
directory sync, archives, FTP/SFTP/WebDAV/SMB (passwords are never stored), file compare,
checksums, duplicate finder, split/join, attributes, quick view, thumbnails, change
watching, custom keys, menu and file-type colors, light/dark themes, and a Hungarian and
English UI (more languages can be added via a translation template).

**Download:** Windows (`.zip`) and Linux (`.tar.gz`) packages are on the
[Releases](../../releases) page.

## Fordítás / Building

Go 1.24 kell. A Windows-változat bármely rendszeren fordítható, a Linuxoshoz
OpenGL- és X11-fejlécek kellenek (Ubuntun: `libgl1-mesa-dev xorg-dev`).
Requires Go 1.24; the Linux build needs OpenGL/X11 headers.

```sh
./build.sh      # GozieCmd.exe + goziecmd
./package.sh    # kiadási csomagok / release packages
go test ./...
```

Új kiadás / new release: `git tag v0.47 && git push --tags` — a GitHub Actions lefordítja
és feltölti a csomagokat. GitHub Actions builds and uploads the packages.

## Licenc / License

GPL-3.0-or-later, lásd / see [LICENSE](LICENSE). A felhasznált könyvtárak licencei /
third-party licenses: [THIRD_PARTY_LICENSES.txt](THIRD_PARTY_LICENSES.txt).

A Gozie Commander független projekt, más fájlkezelőkhöz nincs köze.
Gozie Commander is an independent project, not affiliated with any other file manager.
