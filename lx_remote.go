//go:build linux

package main

// Távoli kapcsolatok (FTP, FTPS, SFTP, WebDAV, SMB) – Linux-felület.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// connForm: a kapcsolat adatainak űrlapja. withName: mentett kapcsolat (név, jelszó nélkül).
func (a *App) connForm(parent fyne.Window, s ftpSite, withName bool, title, okText string, done func(ftpSite, string)) {
	labels := make([]string, len(protocols))
	for i, p := range protocols {
		labels[i] = p.Label
	}
	cur := protoIndex(s.Proto, s.TLS, protocols)
	if s.Port == 0 {
		s.Port = protocols[cur].Port
	}
	name, host, port, user, dir, key := widget.NewEntry(), widget.NewEntry(), widget.NewEntry(), widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	pass := widget.NewPasswordEntry()
	name.SetText(s.Name)
	name.SetPlaceHolder(T("pl. Saját honlap"))
	host.SetText(s.Host)
	port.SetText(strconv.Itoa(s.Port))
	user.SetText(s.User)
	dir.SetText(s.Dir)
	dir.SetPlaceHolder(T("üresen: a szerver alapértelmezése"))
	key.SetText(s.KeyFile)
	key.SetPlaceHolder(T("üresen: ~/.ssh kulcsai és az SSH-ügynök"))
	ins := widget.NewCheck(T("Tanúsítvány elfogadása ellenőrzés nélkül"), nil)
	ins.SetChecked(s.Insecure)
	hint := widget.NewLabel("")
	hint.Wrapping = fyne.TextWrapWord
	keyBtn := widget.NewButton(T("Tallózás…"), func() {
		fd := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
			if r != nil {
				key.SetText(r.URI().Path())
				r.Close()
			}
		}, parent)
		if home, err := os.UserHomeDir(); err == nil {
			if l, err := storage.ListerForURI(storage.NewFileURI(filepath.Join(home, ".ssh"))); err == nil {
				fd.SetLocation(l)
			}
		}
		fd.Show()
	})
	keyRow := container.NewBorder(nil, nil, nil, keyBtn, key)
	protoS := widget.NewSelect(labels, nil)
	update := func() {
		p := protocols[cur]
		if p.ID == "sftp" {
			key.Enable()
			keyBtn.Enable()
		} else {
			key.Disable()
			keyBtn.Disable()
		}
		if p.TLS {
			ins.Enable()
		} else {
			ins.Disable()
		}
		switch p.ID {
		case "sftp":
			hint.SetText(T("Belépés jelszóval vagy SSH-kulccsal (a ~/.ssh kulcsait és az SSH-ügynököt magától is kipróbálja). Kulcsnál a jelszó a kulcs jelmondata."))
			host.SetPlaceHolder(T("pl. nas.local vagy 192.168.1.10"))
		case "webdav":
			hint.SetText(T("A kiszolgáló mezőbe az útvonal is írható, pl. cloud.example.com/remote.php/dav/files/nev"))
			host.SetPlaceHolder(T("pl. cloud.example.com/remote.php/dav/files/nev"))
		case "smb":
			hint.SetText(T("Windows-gép vagy NAS megosztásai. Felhasználó: név vagy TARTOMÁNY\\név; üresen vendégként. A kiszolgáló után megosztás is írható: gép/megosztás"))
			host.SetPlaceHolder(T("pl. nas.local vagy 192.168.1.10/Dokumentumok"))
		default:
			hint.SetText(T("Üres felhasználónévvel névtelen (anonymous) belépés."))
			host.SetPlaceHolder(T("pl. ftp.example.com"))
		}
	}
	protoS.SetSelectedIndex(cur)
	protoS.OnChanged = func(string) {
		np := protoS.SelectedIndex()
		if np < 0 {
			return
		}
		if old, err := strconv.Atoi(strings.TrimSpace(port.Text)); err != nil || old == protocols[cur].Port {
			port.SetText(strconv.Itoa(protocols[np].Port))
		}
		cur = np
		update()
	}
	update()
	var items []*widget.FormItem
	items = append(items, widget.NewFormItem(T("Protokoll"), protoS))
	if withName {
		items = append(items, widget.NewFormItem(T("Megnevezés"), name))
	}
	items = append(items, widget.NewFormItem(T("Kiszolgáló"), host), widget.NewFormItem(T("Port"), port),
		widget.NewFormItem(T("Felhasználó"), user))
	if !withName {
		items = append(items, widget.NewFormItem(T("Jelszó"), pass))
	}
	items = append(items, widget.NewFormItem(T("SSH-kulcsfájl"), keyRow), widget.NewFormItem(T("Kezdő mappa"), dir),
		widget.NewFormItem("", ins))
	content := container.NewVBox(widget.NewForm(items...), hint,
		widget.NewLabel(T("A jelszót a program nem menti: kapcsolódáskor kéri.")))
	d := dialog.NewCustomConfirm(title, okText, T("Mégse"), content, func(ok bool) {
		a.active.focus()
		if !ok {
			return
		}
		p := protocols[cur]
		h := cleanHost(host.Text)
		if p.ID != "webdav" && p.ID != "smb" {
			h = strings.Split(h, "/")[0]
		}
		if h == "" {
			a.warn(T("Add meg a kiszolgáló címét."))
			return
		}
		pn, err := strconv.Atoi(strings.TrimSpace(port.Text))
		if err != nil || pn <= 0 || pn > 65535 {
			pn = p.Port
		}
		ns := ftpSite{Proto: p.ID, TLS: p.TLS && p.ID != "sftp" && p.ID != "smb", Host: h, Port: pn,
			User: strings.TrimSpace(user.Text), Dir: strings.TrimSpace(dir.Text), Insecure: ins.Checked,
			KeyFile: strings.Trim(strings.TrimSpace(key.Text), `"'`)}
		ns.Name = strings.TrimSpace(name.Text)
		if ns.Name == "" {
			ns.Name = h
		}
		done(ns, pass.Text)
	}, parent)
	d.Resize(fyne.NewSize(600, 0))
	d.Show()
	if withName || s.Host == "" {
		parent.Canvas().Focus(host)
	} else {
		parent.Canvas().Focus(pass)
	}
}

func (a *App) siteDialog(parent fyne.Window, s ftpSite, done func(ftpSite)) {
	a.connForm(parent, s, true, T("Távoli kapcsolat"), T("Mentés"), func(ns ftpSite, _ string) { done(ns) })
}

func (a *App) quickConnect() {
	s := ftpSite{Proto: a.setting("ftpProto", "ftp"), Host: a.setting("ftpHost", ""), User: a.setting("ftpUser", ""),
		Dir: a.setting("ftpDir", ""), TLS: a.settingBool("ftpTLS", false), Insecure: a.settingBool("ftpInsecure", false),
		KeyFile: a.setting("ftpKey", "")}
	s.Port = a.settingInt("ftpPort", 0)
	a.connForm(a.win, s, false, T("Gyors kapcsolódás"), T("Kapcsolódás"), func(ns ftpSite, pass string) {
		st := a.settings
		st.Put("ftpProto", ns.Proto)
		st.Put("ftpHost", ns.Host)
		st.Put("ftpPort", strconv.Itoa(ns.Port))
		st.Put("ftpUser", ns.User)
		st.Put("ftpDir", ns.Dir)
		st.Put("ftpTLS", boolStr(ns.TLS))
		st.Put("ftpInsecure", boolStr(ns.Insecure))
		st.Put("ftpKey", ns.KeyFile)
		st.Save()
		conf := siteConf(ns)
		conf.Pass = pass
		a.connectRemote(conf)
	})
}

func (a *App) connectSite(s ftpSite) {
	conf := siteConf(s)
	ask, optional := needsPassword(s)
	if !ask {
		a.connectRemote(conf)
		return
	}
	who := s.Host
	if s.User != "" {
		who = s.User + "@" + s.Host
	}
	if optional {
		who += T("\n(SSH-kulccsal üresen hagyható)")
	}
	a.passwordBox(who, func(pw string) {
		conf.Pass = pw
		a.connectRemote(conf)
	})
}

func (a *App) knownHostsFile() string {
	return filepath.Join(filepath.Dir(a.settings.FilePath()), "known_hosts")
}

// confirmHostKey: ismeretlen vagy megváltozott SSH-gépkulcs jóváhagyása (háttérszálról hívják).
func (a *App) confirmHostKey(host, fp string, changed bool) bool {
	ch := make(chan bool, 1)
	fyne.Do(func() {
		title := T("Ismeretlen szerver")
		msg := fmt.Sprintf(T("Most kapcsolódsz először ehhez a szerverhez: %s\n\nGépkulcs ujjlenyomata:\n%s\n\nHa ismered a szerver ujjlenyomatát, hasonlítsd össze. Megbízol benne és kapcsolódsz?\n(Elfogadás esetén a program megjegyzi, és legközelebb nem kérdez.)"), host, fp)
		yes := T("Kapcsolódás")
		if changed {
			title = T("Figyelem – megváltozott gépkulcs")
			msg = fmt.Sprintf(T("A(z) %s szerver gépkulcsa MEGVÁLTOZOTT az előző kapcsolódás óta.\n\nEz lehet a szerver újratelepítése, de egy támadás jele is.\nÚj ujjlenyomat: %s\n\nCsak akkor kapcsolódj, ha biztosan tudod, miért változott."), host, fp)
			yes = T("Mégis kapcsolódom")
		}
		// saját ablakban, hogy a folyamatjelző ne takarja el
		w := a.fa.NewWindow(title)
		w.SetIcon(appIconRes)
		answered := false
		answer := func(v bool) {
			if !answered {
				answered = true
				ch <- v
				w.Close()
			}
		}
		lbl := widget.NewLabel(msg)
		lbl.Wrapping = fyne.TextWrapWord
		yesB := widget.NewButton(yes, func() { answer(true) })
		noB := widget.NewButton(T("Mégse"), func() { answer(false) })
		if changed {
			noB.Importance = widget.HighImportance
		} else {
			yesB.Importance = widget.HighImportance
		}
		w.SetContent(container.NewPadded(container.NewVBox(lbl, container.NewCenter(container.NewHBox(yesB, noB)))))
		w.SetOnClosed(func() { answer(false) })
		w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
			if ev.Name == fyne.KeyEscape {
				answer(false)
			}
		})
		// a tördelt szöveg magasságát a Fyne nem számolja bele a minimális méretbe
		w.Resize(fyne.NewSize(600, wrappedTextHeight(msg, 560)+90))
		w.CenterOnScreen()
		w.Show()
		w.RequestFocus()
	})
	return <-ch
}

func (a *App) connectRemote(conf remoteConf) {
	conf.KnownHosts = a.knownHostsFile()
	conf.ConfirmHost = a.confirmHostKey
	var f remoteVFS
	p := a.active
	a.runJob(T("Kapcsolódás"), func(j *job) error {
		j.Status(fmt.Sprintf(T("Kapcsolódás: %s…"), conf.Host))
		var err error
		f, err = dialRemote(conf)
		if err == nil && j.Cancelled() {
			f.Close()
			f = nil
			return errCancel
		}
		return err
	}, func(err error) {
		if err != nil || f == nil {
			return
		}
		if !p.openVFS(f, f.StartDir()) && f.StartDir() != "/" {
			if f2, err := dialRemote(conf); err == nil {
				p.openVFS(f2, "/")
			}
		}
		p.focus()
	})
}

func (a *App) connectFTP(conf remoteConf) { a.connectRemote(conf) }

func (a *App) ftpDisconnect() {
	for _, p := range []*Panel{a.active, a.other()} {
		if isRemoteKind(p.kind()) {
			p.exitVFS()
			p.focus()
			return
		}
	}
}

// checkIdle: a tétlen távoli kapcsolatokat bontja (az időzítő hívja).
func (a *App) checkIdle() {
	m := a.idleMinutes()
	if m == 0 || a.idleNotice {
		return
	}
	for _, p := range []*Panel{a.left, a.right} {
		v, ok := p.vfs.(remoteVFS)
		if !ok {
			continue
		}
		d, busy := v.Idle()
		if busy || d < time.Duration(m)*time.Minute {
			continue
		}
		info := v.Info()
		dlog("tétlenségi bontás: %s (%v)", info, d)
		p.exitVFS()
		a.idleNotice = true
		a.choice(appName, fmt.Sprintf(T("%s\n\nA kapcsolatot a program %s tétlenség után bontotta.\nÚjra: Távoli menü (Ctrl+F). Az időkorlát a Beállítások / Kezelés lapon módosítható."), info, idleText(d)),
			[]string{"OK"}, 0, func(int) { a.idleNotice = false })
	}
}

// wrappedTextHeight: a szöveg magassága adott szélességre tördelve (becslés).
func wrappedTextHeight(msg string, width float32) float32 {
	size := theme.TextSize()
	lineH := fyne.MeasureText("Ág", size, fyne.TextStyle{}).Height + 2
	lines := 0
	for _, l := range strings.Split(msg, "\n") {
		w := fyne.MeasureText(l, size, fyne.TextStyle{}).Width
		n := int(w/(width-30)) + 1
		lines += n
	}
	return float32(lines)*lineH + 20
}
