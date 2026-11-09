//go:build windows

package main

// Frissítés keresése és telepítése – Windows-felület.

import (
	"fmt"
	"strconv"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// startUpdateCheck: induláskor (késleltetve) automatikus keresés, ha esedékes.
func (a *App) startUpdateCheck() {
	cleanupOldUpdate()
	if !autoCheckDue(a.settings.Get) {
		return
	}
	go func() {
		time.Sleep(5 * time.Second)
		a.mw.Synchronize(func() { a.checkUpdate(false) })
	}()
}

// checkUpdate: manual = a menüből kérték (ekkor a „nincs újabb” és a hiba is látszik).
func (a *App) checkUpdate(manual bool) {
	go func() {
		r, err := latestRelease()
		a.mw.Synchronize(func() {
			a.settings.Put("updateLast", strconv.FormatInt(time.Now().Unix(), 10))
			a.settings.Save()
			switch {
			case err != nil:
				dlog("frissítés keresése: %v", err)
				if manual {
					walk.MsgBox(a.mw, T("Programfrissítés"), fmt.Sprintf(T("A frissítés keresése nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
				}
			case !versionNewer(r.version(), appVersion):
				if manual {
					walk.MsgBox(a.mw, T("Programfrissítés"), fmt.Sprintf(T("A legújabb verziót használod (%s)."), appVersion), walk.MsgBoxIconInformation)
				}
			default:
				if walk.MsgBox(a.mw, T("Programfrissítés"), updateAvailableText(r), walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) == win.IDYES {
					a.installRelease(r)
				}
			}
		})
	}()
}

func (a *App) installRelease(r *release) {
	if err := canReplaceSelf(); err != nil {
		if walk.MsgBox(a.mw, T("Programfrissítés"), fmt.Sprintf(T("Az automatikus frissítés nem lehetséges:\n%s\n\nMegnyissam a letöltési oldalt?"), errText(err)),
			walk.MsgBoxYesNo|walk.MsgBoxIconWarning) == win.IDYES {
			shellOpen(a.mw.Handle(), updatePage, "")
		}
		return
	}
	var newPath string
	err := runJob(a.mw, fmt.Sprintf(T("%s letöltése"), appName+" "+r.version()), func(j *Job) error {
		j.Status(T("Letöltés…"))
		var last int64
		p, err := downloadUpdate(r, func(done, total int64) {
			if last == 0 {
				j.SetTotal(total)
			}
			j.Add(done - last)
			last = done
		}, j.Cancelled)
		newPath = p
		return err
	})
	if err != nil {
		walk.MsgBox(a.mw, T("Programfrissítés"), fmt.Sprintf(T("A programfrissítés nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return
	}
	if newPath == "" { // megszakítva
		return
	}
	if err := installUpdate(newPath); err != nil {
		walk.MsgBox(a.mw, T("Programfrissítés"), fmt.Sprintf(T("A programfrissítés nem sikerült:\n%s"), errText(err)), walk.MsgBoxIconWarning)
		return
	}
	dlog("frissítve: %s -> %s", appVersion, r.version())
	restartAfterExit = true
	a.mw.Close() // ha egy háttérművelet miatt nem lép ki, a következő kilépéskor indul az új
}
