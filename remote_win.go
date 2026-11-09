//go:build windows

package main

// Távoli kapcsolatok (FTP, FTPS, SFTP, WebDAV) – Windows-felület:
// kapcsolatadatok, gyors kapcsolódás, gépkulcs-jóváhagyás, bontás, tétlenségi bontás.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// Windowson az SMB-t a rendszer kezeli (Hálózat menü), ezért itt nincs a listában.
func winProtocols() []protoInfo {
	var out []protoInfo
	for _, p := range protocols {
		if p.ID != "smb" {
			out = append(out, p)
		}
	}
	return out
}

// connDialog: kapcsolat adatai. pass == nil: mentett kapcsolat (név van, jelszó nincs);
// különben gyors kapcsolódás jelszóval.
func connDialog(owner walk.Form, s *ftpSite, pass *string) bool {
	protos := winProtocols()
	labels := make([]string, len(protos))
	for i, p := range protos {
		labels[i] = p.Label
	}
	cur := protoIndex(s.Proto, s.TLS, protos)
	if s.Port == 0 {
		s.Port = protos[cur].Port
	}
	var dlg *walk.Dialog
	var protoCB *walk.ComboBox
	var name, host, port, user, pw, key, dir *walk.LineEdit
	var keyBtn *walk.PushButton
	var insecure *walk.CheckBox
	var hint *walk.Label
	var ok, cancel *walk.PushButton
	accepted := false
	saved := pass == nil
	update := func() {
		p := protos[maxInt(protoCB.CurrentIndex(), 0)]
		key.SetEnabled(p.ID == "sftp")
		keyBtn.SetEnabled(p.ID == "sftp")
		insecure.SetEnabled(p.TLS)
		switch p.ID {
		case "sftp":
			hint.SetText(T("Belépés jelszóval vagy SSH-kulccsal (a ~/.ssh kulcsait magától is kipróbálja).\nKulcsnál a jelszó mező a kulcs jelmondata."))
			host.SetCueBanner(T("pl. nas.local vagy 192.168.1.10"))
		case "webdav":
			hint.SetText(T("A szerver mezőbe az útvonal is írható, pl. cloud.example.com/remote.php/dav/files/nev"))
			host.SetCueBanner(T("pl. cloud.example.com/remote.php/dav/files/nev"))
		default:
			hint.SetText(T("Üres felhasználónévvel névtelen (anonymous) belépés."))
			host.SetCueBanner(T("pl. ftp.example.com"))
		}
	}
	nameRow := []Widget{}
	if saved {
		nameRow = []Widget{Label{Text: T("Megnevezés:")}, LineEdit{AssignTo: &name, Text: s.Name, CueBanner: T("pl. Saját honlap"), ColumnSpan: 2}}
	}
	passRow := []Widget{}
	if !saved {
		passRow = []Widget{Label{Text: T("Jelszó:")}, LineEdit{AssignTo: &pw, PasswordMode: true, ColumnSpan: 2}}
	}
	rows := append([]Widget{
		Label{Text: T("Protokoll:")},
		ComboBox{AssignTo: &protoCB, Model: labels, CurrentIndex: cur, ColumnSpan: 2, OnCurrentIndexChanged: func() {
			if protoCB == nil || port == nil {
				return
			}
			np := protos[maxInt(protoCB.CurrentIndex(), 0)]
			if old, err := strconv.Atoi(strings.TrimSpace(port.Text())); err != nil || old == protos[cur].Port {
				port.SetText(strconv.Itoa(np.Port))
			}
			cur = protoCB.CurrentIndex()
			update()
		}},
	}, nameRow...)
	rows = append(rows,
		Label{Text: T("Szerver:")}, LineEdit{AssignTo: &host, Text: s.Host, ColumnSpan: 2},
		Label{Text: T("Port:")}, LineEdit{AssignTo: &port, Text: strconv.Itoa(s.Port), MaxSize: Size{Width: 80}}, HSpacer{},
		Label{Text: T("Felhasználó:")}, LineEdit{AssignTo: &user, Text: s.User, ColumnSpan: 2},
	)
	rows = append(rows, passRow...)
	rows = append(rows,
		Label{Text: T("SSH-kulcsfájl:")}, LineEdit{AssignTo: &key, Text: s.KeyFile, CueBanner: T("üresen: ~/.ssh kulcsai")},
		PushButton{AssignTo: &keyBtn, Text: T("Tallózás…"), OnClicked: func() {
			fd := walk.FileDialog{Title: T("SSH-kulcs kiválasztása"), Filter: T("Minden fájl (*.*)|*.*")}
			if k := strings.TrimSpace(key.Text()); k != "" {
				fd.InitialDirPath = filepath.Dir(k)
			} else if home, err := homeDir(); err == nil {
				fd.InitialDirPath = filepath.Join(home, ".ssh")
			}
			if okk, _ := fd.ShowOpen(dlg); okk {
				key.SetText(fd.FilePath)
			}
		}},
		Label{Text: T("Kezdőmappa:")}, LineEdit{AssignTo: &dir, Text: s.Dir, CueBanner: T("üresen: a szerver alapértelmezése"), ColumnSpan: 2},
	)
	title, okText := T("Távoli kapcsolat"), T("Mentés")
	if !saved {
		title, okText = T("Gyors kapcsolódás"), T("Kapcsolódás")
	}
	err := Dialog{
		AssignTo: &dlg, Title: title, DefaultButton: &ok, CancelButton: &cancel,
		MinSize: Size{Width: 520, Height: 360}, Layout: VBox{},
		Children: []Widget{
			Composite{Layout: Grid{Columns: 3}, Children: rows},
			CheckBox{AssignTo: &insecure, Text: T("Tanúsítvány elfogadása ellenőrzés nélkül (saját aláírású szerverhez)"), Checked: s.Insecure},
			Label{AssignTo: &hint},
			Label{Text: T("A jelszót a program nem menti: kapcsolódáskor kéri.")},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &ok, Text: okText, OnClicked: func() {
					p := protos[maxInt(protoCB.CurrentIndex(), 0)]
					h := cleanHost(host.Text())
					if p.ID != "webdav" {
						h = strings.Split(h, "/")[0]
					}
					if h == "" {
						walk.MsgBox(dlg, appName, T("Add meg a szerver címét."), walk.MsgBoxIconInformation)
						return
					}
					pn, err := strconv.Atoi(strings.TrimSpace(port.Text()))
					if err != nil || pn <= 0 || pn > 65535 {
						walk.MsgBox(dlg, appName, T("Érvénytelen portszám."), walk.MsgBoxIconInformation)
						return
					}
					s.Proto, s.TLS = p.ID, p.TLS
					if p.ID == "sftp" {
						s.TLS = false
					}
					s.Host, s.Port = h, pn
					s.User, s.Dir = strings.TrimSpace(user.Text()), strings.TrimSpace(dir.Text())
					s.KeyFile = strings.Trim(strings.TrimSpace(key.Text()), `"`)
					s.Insecure = insecure.Checked()
					if saved {
						s.Name = strings.TrimSpace(name.Text())
						if s.Name == "" {
							s.Name = h
						}
					} else {
						*pass = pw.Text()
					}
					accepted = true
					dlg.Accept()
				}},
				PushButton{AssignTo: &cancel, Text: T("Mégse"), OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Create(owner)
	if err != nil {
		return false
	}
	update()
	themeDialog(dlg)
	dlg.Starting().Attach(func() {
		switch {
		case saved:
			name.SetFocus()
		case s.Host == "":
			host.SetFocus()
		default:
			pw.SetFocus()
		}
	})
	dlg.Run()
	return accepted
}

func homeDir() (string, error) { return os.UserHomeDir() }

func siteDialog(owner walk.Form, s *ftpSite) bool { return connDialog(owner, s, nil) }

// quickConnect: egyszeri kapcsolat; az adatokat (jelszó nélkül) megjegyzi.
func (a *App) quickConnect() {
	s := a.settings
	get := func(k, def string) string {
		if v, ok := s.Get(k); ok {
			return v
		}
		return def
	}
	site := ftpSite{
		Proto:    get("ftpProto", "ftp"),
		Host:     get("ftpHost", ""),
		User:     get("ftpUser", ""),
		Dir:      get("ftpDir", ""),
		TLS:      get("ftpTLS", "0") == "1",
		Insecure: get("ftpInsecure", "0") == "1",
		KeyFile:  get("ftpKey", ""),
	}
	site.Port, _ = strconv.Atoi(get("ftpPort", "0"))
	var pass string
	if !connDialog(a.mw, &site, &pass) {
		return
	}
	s.Put("ftpProto", site.Proto)
	s.Put("ftpHost", site.Host)
	s.Put("ftpPort", strconv.Itoa(site.Port))
	s.Put("ftpUser", site.User)
	s.Put("ftpDir", site.Dir)
	s.Put("ftpTLS", boolStr(site.TLS))
	s.Put("ftpInsecure", boolStr(site.Insecure))
	s.Put("ftpKey", site.KeyFile)
	s.Save()
	conf := siteConf(site)
	conf.Pass = pass
	a.connectRemote(conf)
}

// connectSite: mentett kapcsolat; szükség esetén jelszót kér.
func (a *App) connectSite(s ftpSite) {
	conf := siteConf(s)
	if ask, optional := needsPassword(s); ask {
		who := s.Host
		if s.User != "" {
			who = s.User + "@" + s.Host
		}
		if optional {
			who += T("\n(SSH-kulccsal üresen hagyható)")
		}
		pw, ok := passwordBox(a, s.Name, who)
		if !ok {
			return
		}
		conf.Pass = pw
	}
	a.connectRemote(conf)
}

func (a *App) knownHostsFile() string {
	return filepath.Join(filepath.Dir(a.settings.FilePath()), "known_hosts")
}

// confirmHostKey: ismeretlen vagy megváltozott SSH-gépkulcs jóváhagyása (bármely szálról hívható).
func (a *App) confirmHostKey(host, fp string, changed bool) bool {
	ch := make(chan bool, 1)
	a.mw.Synchronize(func() {
		var r int
		if changed {
			r = walk.MsgBox(a.mw, T("Figyelem – megváltozott gépkulcs"),
				fmt.Sprintf(T("A(z) %s szerver gépkulcsa MEGVÁLTOZOTT az előző kapcsolódás óta.\n\nEz lehet a szerver újratelepítése, de egy támadás jele is.\nÚj ujjlenyomat: %s\n\nCsak akkor kapcsolódj, ha biztosan tudod, miért változott. Mégis kapcsolódsz?"), host, fp),
				walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2)
		} else {
			r = walk.MsgBox(a.mw, T("Ismeretlen szerver"),
				fmt.Sprintf(T("Most kapcsolódsz először ehhez a szerverhez: %s\n\nGépkulcs ujjlenyomata:\n%s\n\nHa ismered a szerver ujjlenyomatát, hasonlítsd össze. Megbízol benne és kapcsolódsz?\n(Igen esetén a program megjegyzi, és legközelebb nem kérdez.)"), host, fp),
				walk.MsgBoxYesNo|walk.MsgBoxIconQuestion)
		}
		ch <- r == win.IDYES
	})
	return <-ch
}

// connectRemote kapcsolódik és az aktív panelen megnyitja a szervert.
func (a *App) connectRemote(conf remoteConf) {
	conf.KnownHosts = a.knownHostsFile()
	conf.ConfirmHost = a.confirmHostKey
	var f remoteVFS
	err := runJob(a.mw, T("Kapcsolódás"), func(j *Job) error {
		j.Status(fmt.Sprintf(T("Kapcsolódás: %s…"), conf.Host))
		var err error
		f, err = dialRemote(conf)
		if err == nil && j.Cancelled() {
			f.Close()
			f = nil
			return errCancel
		}
		return err
	})
	if err != nil {
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("Nem sikerült kapcsolódni:\n%s\n\n%s"), conf.Host, errText(err)), walk.MsgBoxIconWarning)
		return
	}
	if f == nil {
		return
	}
	p := a.active
	if !p.openVFS(f, f.StartDir()) && f.StartDir() != "/" {
		if f2, err := dialRemote(conf); err == nil {
			p.openVFS(f2, "/")
		}
	}
	p.tv.SetFocus()
}

func (a *App) connectFTP(conf remoteConf) { a.connectRemote(conf) }

// ftpDisconnect: az aktív (vagy a másik) panel távoli kapcsolatának bontása.
func (a *App) ftpDisconnect() {
	for _, p := range []*Panel{a.active, a.other()} {
		if isRemoteKind(p.kind()) {
			p.exitVFS()
			p.tv.SetFocus()
			return
		}
	}
}

// checkIdle: a tétlen távoli kapcsolatokat bontja (a felület időzítője hívja).
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
		walk.MsgBox(a.mw, appName, fmt.Sprintf(T("%s\n\nA kapcsolatot a program %s tétlenség után bontotta.\nÚjra: Távoli menü (Ctrl+F). Az időkorlát a Beállítások / Kezelés lapon módosítható."), info, idleText(d)), walk.MsgBoxIconInformation)
		a.idleNotice = false
	}
}
