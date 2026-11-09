//go:build linux

package main

// Frissítés keresése és telepítése – Linux-felület.

import (
	"fmt"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
)

func (a *App) startUpdateCheck() {
	cleanupOldUpdate()
	if !autoCheckDue(a.settings.Get) {
		return
	}
	go func() {
		time.Sleep(5 * time.Second)
		fyne.Do(func() { a.checkUpdate(false) })
	}()
}

func (a *App) checkUpdate(manual bool) {
	if !updatesAllowed() {
		dialog.ShowInformation(T("Programfrissítés"), T("A Flatpak-változatot a Flatpak frissíti (pl. a Szoftverközpont vagy a „flatpak update” parancs)."), a.win)
		return
	}
	go func() {
		r, err := latestRelease()
		fyne.Do(func() {
			a.settings.Put("updateLast", strconv.FormatInt(time.Now().Unix(), 10))
			a.settings.Save()
			switch {
			case err != nil:
				dlog("frissítés keresése: %v", err)
				if manual {
					a.warn(fmt.Sprintf(T("A frissítés keresése nem sikerült:\n%s"), errText(err)))
				}
			case !versionNewer(r.version(), appVersion):
				if manual {
					dialog.ShowInformation(T("Programfrissítés"), fmt.Sprintf(T("A legújabb verziót használod (%s)."), appVersion), a.win)
				}
			default:
				a.confirm(T("Programfrissítés"), updateAvailableText(r), T("Letöltés és telepítés"), func() { a.installRelease(r) })
			}
		})
	}()
}

func (a *App) installRelease(r *release) {
	if err := canReplaceSelf(); err != nil {
		a.confirm(T("Programfrissítés"), fmt.Sprintf(T("Az automatikus frissítés nem lehetséges:\n%s\n\nMegnyissam a letöltési oldalt?"), errText(err)),
			T("Megnyitás"), func() { a.openFile(updatePage) })
		return
	}
	var newPath string
	a.runJob(fmt.Sprintf(T("%s letöltése"), appName+" "+r.version()), func(j *job) error {
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
	}, func(err error) {
		if err != nil || newPath == "" {
			return // a hibát a runJob már megmutatta
		}
		if err := installUpdate(newPath); err != nil {
			a.warn(fmt.Sprintf(T("A programfrissítés nem sikerült:\n%s"), errText(err)))
			return
		}
		dlog("frissítve: %s -> %s", appVersion, r.version())
		restartAfterExit = true
		a.quit()
	})
}
