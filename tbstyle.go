package main

// Az eszköztár közös leírása Windowsra és Linuxra: a gombok alapsora, a lapkák
// színe és az ikonok (a Fyne Material-ikonjai; Linuxon a Fyne témából, Windowson a
// tbicons mappából rajzolva), hogy a két változat eszköztára egyforma legyen.

// tbIconFile: a parancs ikonja (svg-fájl a Fyne ikonkészletéből).
var tbIconFile = map[string]string{
	"back": "arrow-back", "forward": "arrow-forward", "up": "arrow-up", "root": "home",
	"refresh": "view-refresh", "view": "visibility", "edit": "document-create",
	"copy": "content-copy", "move": "mail-forward", "mkdir": "folder-new", "delete": "delete",
	"rename": "content-redo", "multirename": "list", "search": "search",
	"pack": "download", "unpack": "upload", "compare": "search-replace", "sync": "view-refresh",
	"queue": "history", "diff": "search-replace", "checksum": "check", "verify": "check",
	"dupes": "content-copy", "split": "content-cut", "join": "content-paste", "attrs": "info",
	"newfile": "file", "permdelete": "delete", "quickconnect": "storage", "disconnect": "logout",
	"hotlist": "folder", "history": "history", "quicksearch": "search", "quickview": "visibility",
	"newtab": "content-add", "closetab": "cancel", "nexttab": "arrow-forward",
	"viewdetail": "list", "viewbrief": "grid", "viewthumbs": "media-photo",
	"gotopath": "folder-open", "samedir": "view-zoom-fit", "cmdline": "computer",
	"selectall": "check-box-checked", "unselectall": "check-box", "invert": "check-box-checked",
	"selpattern": "check-box-checked", "unselpattern": "check-box",
	"copypaths": "content-copy", "copynames": "content-copy", "usermenu": "menu",
	"keys": "settings", "exit": "logout", "swap": "view-zoom-fit", "hidden": "visibility-off",
	"ftp": "storage", "network": "desktop", "terminal": "computer", "settings": "settings", "help": "help",
}

// tbTile: a parancs lapkájának színe (csoportonként).
func tbTile(id string) (r, g, b uint8) {
	switch id {
	case "back", "forward", "up", "root", "refresh":
		return 0, 103, 192
	case "view", "edit":
		return 0, 133, 120
	case "copy", "move":
		return 16, 124, 16
	case "mkdir":
		return 196, 120, 0
	case "delete":
		return 196, 43, 28
	case "rename", "multirename":
		return 124, 77, 175
	case "search", "compare", "sync", "diff", "dupes":
		return 70, 84, 170
	case "pack", "unpack":
		return 168, 92, 30
	case "ftp", "network":
		return 0, 120, 150
	case "terminal":
		return 60, 60, 60
	}
	return 100, 108, 118
}

// tbDefaultIDs: az eszköztár alapsora; a „network” csak Windowson van.
func tbDefaultIDs(windows bool) []string {
	ids := []string{"back", "forward", "up", "refresh", "-", "view", "edit", "copy", "move", "mkdir", "delete", "-",
		"search", "multirename", "compare", "sync", "-", "pack", "unpack", "-", "ftp"}
	if windows {
		ids = append(ids, "network")
	}
	return append(ids, "-", "terminal", "-", "settings")
}

// tbOldDefault: a korábbi változatok alapsora. Aki ezt sosem változtatta meg, az
// automatikusan az új, egységes alapsort kapja.
func tbOldDefault(windows bool) []string {
	if windows {
		return []string{"back", "forward", "up", "refresh", "-", "view", "edit", "copy", "move", "mkdir", "delete", "-",
			"search", "multirename", "compare", "-", "pack", "unpack", "-", "ftp", "network", "-", "terminal", "exe:notepad.exe", "-", "settings"}
	}
	return []string{"back", "forward", "up", "refresh", "-", "view", "edit", "copy", "move", "mkdir", "delete", "-",
		"search", "multirename", "compare", "sync", "-", "pack", "unpack", "-", "ftp", "-", "terminal", "-", "settings"}
}

// tbIsOldDefault: a mentett sor (jelekként: "-", parancs, "exe:program") a régi alapsor-e.
func tbIsOldDefault(sig []string, windows bool) bool {
	old := tbOldDefault(windows)
	if len(sig) != len(old) {
		return false
	}
	for i := range old {
		if sig[i] != old[i] {
			return false
		}
	}
	return true
}
