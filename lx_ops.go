//go:build linux

package main

// Fájlműveletek Linuxon: másolás, mozgatás, törlés (Kukába vagy véglegesen),
// új mappa, átnevezés, új fájl.

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ── másolás / mozgatás ──

func (a *App) transfer(move bool) {
	p, o := a.active, a.other()
	items := p.selectedEntries()
	if len(items) == 0 {
		return
	}
	verb := T("%s másolása ide:")
	title := T("Másolás")
	if move {
		verb, title = T("%s mozgatása ide:"), T("Mozgatás")
	}
	if p.vfs != nil || o.vfs != nil {
		where := o.path
		if o.vfs != nil {
			where = o.vfs.Display(o.vpath)
		}
		a.confirm(title, fmt.Sprintf(verb, describe(items))+"\n"+where, title, func() {
			a.vfsTransfer(p, o, items, move)
		})
		return
	}
	a.inputBoxBg(title, fmt.Sprintf(verb, describe(items)), o.path+"/", func(t string, bg bool) {
		t = strings.TrimSpace(t)
		if t == "" {
			return
		}
		a.localTransferBg(p, entryPaths(items), t, move, bg)
	})
}

// transferTo: húzd és ejtsd a másik panelre (targetDir üres: a panel mappája).
func (a *App) transferTo(src, dst *Panel, targetDir string, move bool, ask bool) {
	items := src.selectedEntries()
	if len(items) == 0 {
		return
	}
	if src.vfs != nil || dst.vfs != nil {
		a.vfsTransfer(src, dst, items, move)
		return
	}
	if targetDir == "" {
		targetDir = dst.path
	}
	verb, title := T("%s másolása ide:"), T("Másolás")
	if move {
		verb, title = T("%s mozgatása ide:"), T("Mozgatás")
	}
	a.choice(title, fmt.Sprintf(verb, describe(items))+"\n"+targetDir, []string{T("Másolás ide"), T("Mozgatás ide"), T("Mégse")},
		map[bool]int{false: 0, true: 1}[move], func(i int) {
			if i < 2 {
				a.localTransfer(src, entryPaths(items), targetDir, i == 1)
			}
		})
}

// copyIn: a rendszer fájlkezelőjéből behúzott fájlok.
func (a *App) copyIn(files []string, p *Panel, target string, move bool) {
	if p.vfs != nil {
		a.runJob(T("Másolás"), func(j *job) error {
			err := importPaths(p.vfs, files, p.vpath, j)
			if err == nil && move && j.Clean() { // hiba vagy kihagyás esetén a forrás megmarad
				removeLocal(files, j)
			}
			return err
		}, func(error) { p.refresh() })
		return
	}
	a.localTransfer(p, files, target, move)
}

// localTransfer: a cél lehet mappa, vagy egyetlen elemnél új név is.
func (a *App) localTransfer(p *Panel, srcs []string, target string, move bool) {
	a.localTransferBg(p, srcs, target, move, false)
}

func (a *App) localTransferBg(p *Panel, srcs []string, target string, move bool, background bool) {
	if strings.HasPrefix(target, "~") {
		home, _ := os.UserHomeDir()
		target = home + target[1:]
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(p.path, target)
	}
	destDir, newName := target, ""
	st, err := os.Stat(target)
	if err != nil || !st.IsDir() {
		if len(srcs) == 1 && !strings.HasSuffix(target, "/") {
			destDir, newName = filepath.Dir(target), filepath.Base(target)
		}
	}
	destDir = filepath.Clean(destDir)
	title := T("Másolás")
	if move {
		title = T("Mozgatás")
	}
	work := func(j Progress) error {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return err
		}
		return copyItems(srcs, destDir, newName, move, j)
	}
	if background {
		what := filepath.Base(srcs[0])
		if len(srcs) > 1 {
			what = Nf(len(srcs), "%d elem")
		}
		p.marked = map[string]bool{}
		p.view.refreshAll()
		p.updateStatus()
		a.queue.Add(title+": "+what+" → "+destDir, "", work, func(error) {
			a.left.refresh()
			a.right.refresh()
		})
		a.showQueueHint()
		return
	}
	what := filepath.Base(srcs[0])
	if len(srcs) > 1 {
		what = Nf(len(srcs), "%d elem")
	}
	a.runJobBg(title, what+" → "+destDir, func(j *job) error { return work(j) }, func(err error) {
		if err == nil {
			p.marked = map[string]bool{} // a sikeresen átvitt elemek jelölése megszűnik
		}
		a.left.refresh()
		a.right.refresh()
		if newName != "" && destDir == a.other(p).path {
			a.other(p).selectName(newName)
		}
	})
}

func treeSize(p string) int64 {
	var n int64
	filepath.Walk(p, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n
}

func copyItems(srcs []string, destDir, newName string, move bool, j Progress) error {
	var total int64
	j.Status(T("Méret számítása…"))
	for _, s := range srcs {
		total += treeSize(s)
	}
	j.SetTotal(total)
	for _, src := range srcs {
		if j.Cancelled() {
			return errCancel
		}
		name := filepath.Base(src)
		if newName != "" {
			name = newName
		}
		dst := filepath.Join(destDir, name)
		if src == dst {
			if !move {
				j.Err(name, errors.New(T("a forrás és a cél ugyanaz")))
			}
			continue
		}
		if strings.HasPrefix(dst, src+"/") {
			j.Err(name, errors.New(T("mappa nem másolható önmagába")))
			continue
		}
		if move {
			if done, err := tryRename(src, dst, j); done {
				if err == errCancel {
					return err
				}
				continue
			}
		}
		// mozgatásnál fájlonként törlődik a forrás, csak a sikeresen átvitt elem:
		// a kihagyott (felül nem írt) vagy hibás fájl a helyén marad
		if err := transferAny(src, dst, j, move); err != nil {
			if err == errCancel {
				return err
			}
			j.Err(src, err)
			continue
		}
	}
	return nil
}

// tryRename: azonos fájlrendszeren a mozgatás átnevezés (azonnali).
// done=false: más fájlrendszer vagy mappák összefésülése – másolni kell.
func tryRename(src, dst string, j Progress) (bool, error) {
	if dst_, err := os.Lstat(dst); err == nil {
		sst, err := os.Lstat(src)
		if err != nil {
			j.Err(src, err)
			return true, nil
		}
		if dst_.IsDir() && sst.IsDir() {
			return false, nil // összefésülés
		}
		ok, err := j.AskOverwrite(dst, overwriteDetail(sst, dst_))
		if err != nil {
			return true, err
		}
		if !ok {
			return true, nil
		}
		if err := os.RemoveAll(dst); err != nil {
			j.Err(dst, err)
			return true, nil
		}
	}
	size := treeSize(src)
	j.Status(src)
	err := os.Rename(src, dst)
	if err == nil {
		j.Add(size)
		return true, nil
	}
	if errors.Is(err, syscall.EXDEV) {
		return false, nil
	}
	j.Err(src, err)
	return true, nil
}

func overwriteDetail(src, dst os.FileInfo) string {
	return fmt.Sprintf(T("Új:    %s\nMeglévő: %s"), sizeAndTime(src.Size(), src.ModTime()), sizeAndTime(dst.Size(), dst.ModTime()))
}

func copyAny(src, dst string, j Progress) error { return transferAny(src, dst, j, false) }

// transferAny másol; move esetén minden sikeresen átvitt elem forrását törli.
func transferAny(src, dst string, j Progress, move bool) error {
	if j.Cancelled() {
		return errCancel
	}
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		link, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if dt, err := os.Lstat(dst); err == nil {
			ok, err := j.AskOverwrite(dst, overwriteDetail(st, dt))
			if err != nil || !ok {
				return err
			}
			os.Remove(dst)
		}
		if err := os.Symlink(link, dst); err != nil {
			return err
		}
		if move {
			return os.Remove(src)
		}
		return nil
	case st.IsDir():
		if err := os.MkdirAll(dst, st.Mode().Perm()|0o700); err != nil {
			return err
		}
		des, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, d := range des {
			if err := transferAny(filepath.Join(src, d.Name()), filepath.Join(dst, d.Name()), j, move); err != nil {
				if err == errCancel {
					return err
				}
				j.Err(filepath.Join(src, d.Name()), err)
			}
		}
		os.Chmod(dst, st.Mode().Perm())
		os.Chtimes(dst, st.ModTime(), st.ModTime())
		if move {
			os.Remove(src) // csak üresen sikerül: ha valami kimaradt, a mappa megmarad
		}
		return nil
	case !st.Mode().IsRegular():
		return errors.New(T("különleges fájl (eszköz, cső), kihagyva"))
	}
	if dt, err := os.Stat(dst); err == nil {
		if dt.IsDir() {
			return errors.New(T("a célban ilyen nevű mappa van"))
		}
		ok, err := j.AskOverwrite(dst, overwriteDetail(st, dt))
		if err != nil || !ok {
			return err
		}
	}
	j.Status(src)
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, st.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	_, err = io.Copy(progressWriter{out, j}, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return err
	}
	os.Chmod(dst, st.Mode().Perm())
	os.Chtimes(dst, st.ModTime(), st.ModTime())
	if move {
		return os.Remove(src)
	}
	return nil
}

// ── törlés ──

func (a *App) deleteSelected(permanent bool) {
	p := a.active
	items := p.selectedEntries()
	if len(items) == 0 {
		return
	}
	if p.vfs != nil {
		if !canDelete(p.vfs) {
			a.warn(T("Innen nem lehet törölni."))
			return
		}
		a.confirm(T("Törlés"), fmt.Sprintf(T("%s törlése innen:\n%s"), describe(items), p.vfs.Display(p.vpath)), T("Törlés"), func() {
			a.runJob(T("Törlés"), func(j *job) error { return deleteItems(p.vfs, items, j) },
				func(error) { p.refresh() })
		})
		return
	}
	msg, btn := fmt.Sprintf(T("%s a Kukába kerül."), describe(items)), T("Kukába")
	if permanent {
		msg, btn = fmt.Sprintf(T("%s végleges törlése?\nEz nem vonható vissza."), describe(items)), T("Végleges törlés")
	}
	a.confirm(T("Törlés"), msg, btn, func() {
		var failed []string
		a.runJob(T("Törlés"), func(j *job) error {
			j.SetTotal(int64(len(items)))
			for _, e := range items {
				if j.Cancelled() {
					return errCancel
				}
				j.Status(e.Path)
				var err error
				if permanent {
					err = os.RemoveAll(e.Path)
				} else if err = trashPath(e.Path); err != nil {
					dlog("kuka: %s: %v", e.Path, err)
					failed = append(failed, e.Path)
					err = nil
				}
				if err != nil {
					j.Err(e.Name, err)
				}
				j.Add(1)
			}
			return nil
		}, func(error) {
			p.refresh()
			a.other(p).refresh()
			if len(failed) > 0 {
				a.confirm(T("Kuka"), fmt.Sprintf(Tn(len(failed), "%d elem nem tehető a Kukába (például cserélhető lemezen vagy hálózati helyen).\nVéglegesen törlöd?"), len(failed)),
					T("Végleges törlés"), func() {
						a.runJob(T("Törlés"), func(j *job) error {
							for _, f := range failed {
								j.Status(f)
								if err := os.RemoveAll(f); err != nil {
									j.Err(f, err)
								}
							}
							return nil
						}, func(error) { p.refresh() })
					})
			}
		})
	})
}

// trashPath a freedesktop.org Kuka-szabvány szerint (ezt használja a Dolphin és a Nautilus is).
func trashPath(p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err != nil {
		return err
	}
	home := homeTrashDir()
	if sameDevice(abs, filepath.Dir(home)) {
		return moveToTrash(home, abs, abs)
	}
	if gio, err := hostLookPath("gio"); err == nil { // Flatpakban a gazdarendszer gio-ja (a saját Kukájával)
		if out, err := hostify(exec.Command(gio, "trash", "--", abs)).CombinedOutput(); err == nil {
			return nil
		} else {
			dlog("gio trash: %v %s", err, out)
		}
	}
	top := mountTop(abs)
	if top == "" {
		return errors.New(T("a Kuka nem érhető el"))
	}
	tdir := filepath.Join(top, ".Trash-"+strconv.Itoa(os.Getuid()))
	rel, err := filepath.Rel(top, abs)
	if err != nil {
		return err
	}
	return moveToTrash(tdir, abs, rel)
}

func homeTrashDir() string {
	data := os.Getenv("XDG_DATA_HOME")
	if inFlatpak { // a sandbox XDG_DATA_HOME-ja a saját mappája; a valódi Kuka a gazdarendszeré
		data = os.Getenv("HOST_XDG_DATA_HOME")
	}
	if data == "" {
		home, _ := os.UserHomeDir()
		data = filepath.Join(home, ".local", "share")
	}
	t := filepath.Join(data, "Trash")
	os.MkdirAll(filepath.Join(t, "files"), 0o700)
	os.MkdirAll(filepath.Join(t, "info"), 0o700)
	return t
}

func devOf(p string) (uint64, bool) {
	var st syscall.Stat_t
	if err := syscall.Lstat(p, &st); err != nil {
		return 0, false
	}
	return uint64(st.Dev), true
}

func sameDevice(a, b string) bool {
	da, ok1 := devOf(a)
	db, ok2 := devOf(b)
	return ok1 && ok2 && da == db
}

// mountTop a fájlt tartalmazó fájlrendszer gyökere.
func mountTop(p string) string {
	d, ok := devOf(p)
	if !ok {
		return ""
	}
	cur := filepath.Dir(p)
	for cur != "/" {
		up := filepath.Dir(cur)
		if du, ok := devOf(up); !ok || du != d {
			return cur
		}
		cur = up
	}
	return "/"
}

func moveToTrash(trash, abs, infoPath string) error {
	files, info := filepath.Join(trash, "files"), filepath.Join(trash, "info")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(info, 0o700); err != nil {
		return err
	}
	base := filepath.Base(abs)
	name := base
	for i := 2; ; i++ {
		f, err := os.OpenFile(filepath.Join(info, name+".trashinfo"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			u := (&url.URL{Path: infoPath}).EscapedPath()
			fmt.Fprintf(f, "[Trash Info]\nPath=%s\nDeletionDate=%s\n", u, time.Now().Format("2006-01-02T15:04:05"))
			f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if _, serr := os.Lstat(filepath.Join(files, name)); serr != nil && i > 1000 {
			return err
		}
		ext := filepath.Ext(base)
		name = strings.TrimSuffix(base, ext) + "." + strconv.Itoa(i) + ext
	}
	if err := os.Rename(abs, filepath.Join(files, name)); err != nil {
		os.Remove(filepath.Join(info, name+".trashinfo"))
		return err
	}
	return nil
}

// ── új mappa, átnevezés, új fájl ──

func (a *App) mkdir() {
	p := a.active
	if p.vfs != nil {
		if x, ok := p.vfs.(Archive); ok && !x.Writable() {
			a.warn(fmt.Sprintf(T("%s: ebbe az archívumba nem lehet írni.%s"), x.Info(), sevenZipHint(x)))
			return
		}
	}
	a.inputBox(T("Új mappa"), T("Az új mappa neve:"), "", func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if p.vfs != nil {
			var err error
			switch x := p.vfs.(type) {
			case Archive:
				err = x.Mkdir(p.vpath, name)
			case remoteVFS:
				err = x.Mkdir(p.vpath, name)
			}
			if err != nil {
				a.warn(fmt.Sprintf(T("A mappa nem hozható létre:\n%s"), errText(err)))
			}
			p.refresh()
			p.selectName(name)
			return
		}
		full := name
		if !filepath.IsAbs(full) {
			full = filepath.Join(p.path, name)
		}
		if err := os.MkdirAll(full, 0o755); err != nil {
			a.warn(fmt.Sprintf(T("A mappa nem hozható létre:\n%s"), errText(err)))
			return
		}
		p.refresh()
		first := strings.Split(strings.TrimPrefix(strings.TrimPrefix(full, p.path), "/"), "/")[0]
		p.selectName(first)
	})
}

func (a *App) rename() {
	p := a.active
	e := p.currentEntry()
	if e == nil || e.IsParent {
		return
	}
	stem := len([]rune(e.Stem()))
	if e.IsDir {
		stem = len([]rune(e.Name))
	}
	a.inputBoxAt(T("Átnevezés"), T("Új név:"), e.Name, stem, func(nn string) {
		nn = strings.TrimSpace(nn)
		if nn == "" || nn == e.Name {
			return
		}
		if strings.Contains(nn, "/") {
			a.warn(T("A név nem tartalmazhat „/” jelet. Áthelyezéshez használd az F6-ot."))
			return
		}
		if p.vfs != nil {
			f, ok := p.vfs.(remoteVFS)
			if !ok {
				a.warn(T("Archívumon belül nem lehet átnevezni."))
				return
			}
			if err := f.Rename(f.Join(p.vpath, e.Name), f.Join(p.vpath, nn)); err != nil {
				a.warn(fmt.Sprintf(T("Az átnevezés nem sikerült:\n%s"), errText(err)))
			}
			p.refresh()
			p.selectName(nn)
			return
		}
		to := filepath.Join(filepath.Dir(e.Path), nn)
		do := func() {
			if err := os.Rename(e.Path, to); err != nil {
				a.warn(fmt.Sprintf(T("Az átnevezés nem sikerült:\n%s"), errText(err)))
			}
			p.refresh()
			p.selectName(nn)
		}
		if _, err := os.Lstat(to); err == nil && !strings.EqualFold(nn, e.Name) {
			a.confirm(T("Átnevezés"), fmt.Sprintf(T("Már létezik ilyen nevű elem:\n%s\n\nFelülírod?"), nn), T("Felülírás"), do)
			return
		}
		do()
	})
}

func (a *App) newFile() {
	p := a.active
	if p.vfs != nil {
		a.warn(T("Új fájlt csak helyi mappában lehet létrehozni."))
		return
	}
	a.inputBox(T("Új fájl"), T("Az új fájl neve:"), "", func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		full := filepath.Join(p.path, name)
		if _, err := os.Stat(full); err != nil {
			f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				a.warn(fmt.Sprintf(T("A fájl nem hozható létre:\n%s"), errText(err)))
				return
			}
			f.Close()
		}
		p.refresh()
		p.selectName(filepath.Base(full))
		a.openInEditor(full)
	})
}
