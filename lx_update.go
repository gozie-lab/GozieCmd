//go:build linux

package main

// Frissítés keresése és telepítése – Linux-felület.

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// offerFlatpakUpdate: Flatpakban a program nem cseréli le magát, hanem a gazdarendszer
// flatpak parancsával frissül a Gozie Commander Flatpak-tárolójából. Ha a programot
// tároló nélkül, egy régi .flatpak fájlból telepítették, a letöltési oldalt ajánlja.
func (a *App) offerFlatpakUpdate(r *release) {
	a.choice(T("Programfrissítés"), updateAvailableText(r),
		[]string{T("Frissítés most"), T("Újdonságok"), T("Később")}, 0, func(i int) {
			switch i {
			case 0:
				a.flatpakUpdate(r)
			case 1:
				a.openFile(r.notesURL())
				a.offerFlatpakUpdate(r)
			}
		})
}

func (a *App) flatpakUpdate(r *release) {
	id := os.Getenv("FLATPAK_ID")
	var out []byte
	a.runJob(T("Programfrissítés"), func(j *job) error {
		j.Status(fmt.Sprintf(T("%s letöltése"), appName+" "+r.version()))
		var err error
		out, err = hostify(exec.Command("flatpak", "update", "-y", "--noninteractive", id)).CombinedOutput()
		dlog("flatpak update: %v\n%s", err, out)
		return nil
	}, func(error) {
		list, _ := hostify(exec.Command("flatpak", "list", "--app", "--columns=application,version")).Output()
		if !flatpakHasVersion(string(list), id, r.version()) {
			a.choice(T("Programfrissítés"), fmt.Sprintf(T("A Flatpak nem találta meg az új verziót. Valószínűleg egy régebbi .flatpak fájlból telepítetted, amely még nem kapcsolódik a Gozie Commander tárolójához.\n\nTelepítsd egyszer az új GozieCmd-%s-x86_64.flatpak fájlt a letöltési oldalról: utána a frissítés már magától megy."), r.version()),
				[]string{T("Letöltési oldal"), T("Bezárás")}, 0, func(i int) {
					if i == 0 {
						a.openFile(r.notesURL())
					}
				})
			return
		}
		a.choice(T("Programfrissítés"), fmt.Sprintf(T("A frissítés kész (%s). Újraindítsam most a programot?"), r.version()),
			[]string{T("Újraindítás"), T("Később")}, 0, func(i int) {
				if i == 0 {
					restartAfterExit = true
					a.quit()
				}
			})
	})
}

// flatpakHasVersion: a „flatpak list --columns=application,version” kimenetében a
// program telepített verziója (vagy egyike, ha felhasználói és rendszerszinten is van)
// megegyezik-e a várttal. Ez a kimenet nem függ a rendszer nyelvétől.
func flatpakHasVersion(list, id, ver string) bool {
	for _, ln := range strings.Split(list, "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 && f[0] == id && f[1] == ver {
			return true
		}
	}
	return false
}
