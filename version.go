package main

import "strings"

// A program neve és verziója – közös a Windows- és a Linux-változatban.

const appName = "Gozie Commander"
const appDir = "GozieCommander" // mappanév (beállítások, ideiglenes fájlok)
const appVersion = "0.57"

// aboutText: a Névjegy ablak szövege.
// contactEmail: hibabejelentés, észrevétel. Darabokból áll össze, hogy a forráskódban
// (GitHub) ne legyen olvasható a címgyűjtő robotok számára.
func contactEmail() string {
	return strings.Join([]string{"cmd", strings.Join([]string{"gozie", "hu"}, ".")}, "@")
}

const homePage = "https://cmd.gozie.hu/"

func aboutText() string {
	return appName + " " + appVersion + "\n\n" +
		T("Szabad szoftver a GNU GPL 3. vagy későbbi változata szerint.") + "\n" +
		T("Mindenféle garancia nélkül.")
}
