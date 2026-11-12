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
			case !updatesAllowed():
				a.offerFlatpakUpdate(r)
			default:
				a.offerUpdate(r)
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

// offerUpdate: Letöltés és telepítés / Újdonságok (a GitHubon) / Később.
func (a *App) offerUpdate(r *release) {
	a.choice(T("Programfrissítés"), updateAvailableText(r),
		[]string{T("Letöltés és telepítés"), T("Újdonságok"), T("Később")}, 0, func(i int) {
			switch i {
			case 0:
				a.installRelease(r)
			case 1:
				a.openFile(r.notesURL())
				a.offerUpdate(r) // utána újra kérdez
			}
		})
}

// offerFlatpakUpdate: a Flatpak-csomag nem cserélheti le magát; az új .flatpak fájlt a
// felhasználó tölti le és nyitja meg (a Szoftverközpont vagy a flatpak install telepíti).
func (a *App) offerFlatpakUpdate(r *release) {
	msg := fmt.Sprintf(T("Elérhető az új verzió: %s (most: %s).\n\nA Flatpak-változathoz töltsd le a GozieCmd-%s-x86_64.flatpak fájlt a letöltési oldalról, és nyisd meg (a Szoftverközpont telepíti), vagy terminálban:\nflatpak install --user GozieCmd-%s-x86_64.flatpak"),
		r.version(), appVersion, r.version(), r.version())
	a.choice(T("Programfrissítés"), msg, []string{T("Letöltési oldal"), T("Később")}, 0, func(i int) {
		if i == 0 {
			a.openFile(r.notesURL())
		}
	})
}
