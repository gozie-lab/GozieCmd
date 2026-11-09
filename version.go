package main

// A program neve és verziója – közös a Windows- és a Linux-változatban.

const appName = "Gozie Commander"
const appDir = "GozieCommander" // mappanév (beállítások, ideiglenes fájlok)
const appVersion = "0.47"

// aboutText: a Névjegy ablak szövege.
func aboutText() string {
	return appName + " " + appVersion + "\n\n" +
		T("Szabad szoftver a GNU GPL 3. vagy későbbi változata szerint.") + "\n" +
		T("Mindenféle garancia nélkül.")
}
