package main

// Távoli kapcsolatok közös rétege: FTP/FTPS (ftpfs.go), SFTP, WebDAV, SMB.
// Minden távoli nézet a remoteVFS felületet valósítja meg; a tétlenséget
// az idleTrack méri, hogy a felület egy idő után bonthassa a kapcsolatot.

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// remoteConf egy kapcsolat adatai. A jelszót a program soha nem menti.
type remoteConf struct {
	Proto    string // "ftp" (TLS-sel FTPS), "sftp", "webdav", "smb"
	Host     string
	Port     int
	User     string
	Pass     string
	Dir      string
	TLS      bool // FTP: FTPS; WebDAV: https
	Insecure bool
	KeyFile  string // SFTP: privát kulcs (a jelmondat a jelszó mezőben)

	KnownHosts  string                                            // SFTP: a program saját known_hosts fájlja
	ConfirmHost func(host, fingerprint string, changed bool) bool // SFTP: ismeretlen/megváltozott gépkulcs
}

type ftpConf = remoteConf

// protocols: a választható protokollok (az SMB csak Linuxon kell; Windowson a rendszeré).
type protoInfo struct {
	ID    string
	TLS   bool
	Label string
	Port  int
}

var protocols = []protoInfo{
	{"ftp", false, "FTP", 21},
	{"ftp", true, T("FTPS (FTP titkosítva)"), 21},
	{"sftp", false, "SFTP (SSH)", 22},
	{"webdav", true, "WebDAV (https)", 443},
	{"webdav", false, T("WebDAV (http, titkosítás nélkül)"), 80},
	{"smb", false, T("SMB (Windows-megosztás)"), 445},
}

func protoIndex(proto string, tls bool, list []protoInfo) int {
	if proto == "" {
		proto = "ftp"
	}
	for i, p := range list {
		if p.ID == proto && (p.TLS == tls || proto == "sftp" || proto == "smb") {
			return i
		}
	}
	return 0
}

func protoLabel(proto string, tls bool) string {
	return protocols[protoIndex(proto, tls, protocols)].Label
}

// cleanHost: a beírt címből leveszi a protokoll-előtagot; WebDAV-nál az útvonal marad.
func cleanHost(h string) string {
	h = strings.TrimSpace(h)
	for _, p := range []string{"ftp://", "ftps://", "sftp://", "ssh://", "https://", "http://", "davs://", "dav://", "webdav://", "smb://", `\\`} {
		if strings.HasPrefix(strings.ToLower(h), p) {
			h = h[len(p):]
			break
		}
	}
	h = strings.ReplaceAll(h, `\`, "/")
	return strings.TrimSuffix(h, "/")
}

// remoteVFS: amit a felület egy távoli nézettől vár.
type remoteVFS interface {
	VFS
	Download(items []*Entry, localDir string, pr Progress) error
	Upload(srcs []string, remoteDir string, pr Progress) error
	Delete(items []*Entry, pr Progress) error
	Rename(from, to string) error
	Mkdir(dir, name string) error
	StartDir() string
	Idle() (time.Duration, bool) // mióta tétlen; fut-e éppen művelet
}

func isRemoteKind(k string) bool {
	switch k {
	case "ftp", "sftp", "webdav", "smb":
		return true
	}
	return false
}

// dialRemote kapcsolódik a megadott protokollal.
func dialRemote(conf remoteConf) (remoteVFS, error) {
	var f remoteVFS
	var err error
	switch conf.Proto {
	case "sftp":
		f, err = dialSFTP(conf)
	case "webdav":
		f, err = dialWebDAV(conf)
	case "smb":
		f, err = dialSMB(conf)
	default:
		var x *ftpFS
		x, err = dialFTP(conf)
		if err == nil {
			f = x
		}
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// ── tétlenségmérés ──

type idleTrack struct {
	mu   sync.Mutex
	last time.Time
	n    int
}

func (t *idleTrack) enter() {
	t.mu.Lock()
	t.n++
	t.last = time.Now()
	t.mu.Unlock()
}

func (t *idleTrack) leave() {
	t.mu.Lock()
	t.n--
	t.last = time.Now()
	t.mu.Unlock()
}

func (t *idleTrack) Idle() (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last.IsZero() {
		t.last = time.Now()
	}
	return time.Since(t.last), t.n > 0
}

// ── általános távoli fájlrendszer egy egyszerű alsó rétegre építve ──

type rawInfo struct {
	Name string
	Dir  bool
	Size int64
	Mod  time.Time
}

type rawFS interface {
	list(dir string) ([]rawInfo, error)
	open(p string) (io.ReadCloser, error)
	put(p string, r io.Reader, size int64) error
	size(p string) (int64, error)
	mkdir(p string) error
	remove(p string, dir bool) error
	rename(from, to string) error
	chtimes(p string, t time.Time) error
	reconnect() error
	close()
}

type genFS struct {
	raw     rawFS
	kind    string
	display string // pl. sftp://user@host:22
	info    string
	start   string
	// A műveletek párhuzamosan futhatnak (az SFTP, a WebDAV és az SMB is bírja), így egy
	// hosszú háttérmásolás alatt is azonnal listázható a mappa. Csak az újracsatlakozás
	// kizárólagos.
	mu      sync.RWMutex
	gen     uint64 // hányadik kapcsolat (újracsatlakozáskor nő)
	idleTrack
}

func (f *genFS) Kind() string     { return f.kind }
func (f *genFS) Root() string     { return "/" }
func (f *genFS) Info() string     { return f.info }
func (f *genFS) StartDir() string { return f.start }
func (f *genFS) Close()           { f.raw.close() }

func (f *genFS) Parent(dir string) (string, bool) {
	if dir == "/" || dir == "" {
		return "", false
	}
	return path.Dir(dir), true
}

func (f *genFS) Join(dir, name string) string { return path.Join(dir, name) }
func (f *genFS) Display(dir string) string    { return f.display + dir }

func isConnErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, k := range []string{"connection lost", "broken pipe", "reset by peer", "closed network", "connection closed", "eof"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// do: egy művelet; megszakadt kapcsolatnál egyszer újracsatlakozik.
func (f *genFS) do(fn func() error) error {
	f.enter()
	defer f.leave()
	f.mu.RLock()
	gen := f.gen
	err := fn()
	f.mu.RUnlock()
	if err == nil || errors.Is(err, errCancel) || !isConnErr(err) {
		return err
	}
	f.mu.Lock()
	if f.gen == gen { // más még nem csatlakozott újra helyettünk
		if rerr := f.raw.reconnect(); rerr != nil {
			f.mu.Unlock()
			return err
		}
		f.gen++
	}
	f.mu.Unlock()
	f.mu.RLock()
	defer f.mu.RUnlock()
	return fn()
}

func (f *genFS) rawList(dir string) ([]rawInfo, error) {
	var res []rawInfo
	err := f.do(func() error {
		var err error
		res, err = f.raw.list(dir)
		return err
	})
	return res, err
}

func (f *genFS) List(dir string) ([]*Entry, error) {
	items, err := f.rawList(dir)
	if err != nil {
		return nil, err
	}
	out := make([]*Entry, 0, len(items))
	for _, it := range items {
		if it.Name == "." || it.Name == ".." || it.Name == "" {
			continue
		}
		out = append(out, newEntry(it.Name, path.Join(dir, it.Name), it.Dir, it.Size, it.Mod.Local()))
	}
	return out, nil
}

func (f *genFS) collect(items []*Entry, pr Progress) (files []remoteFile, dirs []string, err error) {
	var walk func(remote, rel string) error
	walk = func(remote, rel string) error {
		if pr.Cancelled() {
			return errCancel
		}
		pr.Status(fmt.Sprintf(T("Lista: %s"), remote))
		list, err := f.rawList(remote)
		if err != nil {
			pr.Err(remote, err)
			return nil
		}
		for _, it := range list {
			if it.Name == "." || it.Name == ".." {
				continue
			}
			rp, rr := path.Join(remote, it.Name), rel+"/"+it.Name
			if it.Dir {
				dirs = append(dirs, rr)
				if err := walk(rp, rr); err != nil {
					return err
				}
			} else {
				files = append(files, remoteFile{rp, rr, it.Size, it.Mod.Local()})
			}
		}
		return nil
	}
	for _, it := range items {
		if it.IsDir {
			dirs = append(dirs, it.Name)
			if err := walk(it.Path, it.Name); err != nil {
				return nil, nil, err
			}
		} else {
			files = append(files, remoteFile{it.Path, it.Name, it.Size, it.ModTime})
		}
	}
	return files, dirs, nil
}

func (f *genFS) Download(items []*Entry, localDir string, pr Progress) error {
	f.enter()
	defer f.leave()
	files, dirs, err := f.collect(items, pr)
	if err != nil {
		return err
	}
	var total int64
	for _, rf := range files {
		total += rf.size
	}
	pr.SetTotal(total)
	for _, d := range dirs {
		if p, ok := safeJoin(localDir, d); ok {
			if err := os.MkdirAll(p, 0o777); err != nil {
				pr.Err(p, err)
			}
		}
	}
	for _, rf := range files {
		if pr.Cancelled() {
			return errCancel
		}
		target, ok := safeJoin(localDir, rf.rel)
		if !ok {
			pr.Err(rf.rel, os.ErrPermission)
			continue
		}
		if st, err := os.Stat(target); err == nil {
			if st.IsDir() {
				pr.Err(target, os.ErrExist)
				pr.Add(rf.size)
				continue
			}
			ow, err := pr.AskOverwrite(target, fmt.Sprintf(T("Meglévő:  %s\nA szerveren:  %s"), sizeAndTime(st.Size(), st.ModTime()), sizeAndTime(rf.size, rf.mod)))
			if err != nil {
				return err
			}
			if !ow {
				pr.Add(rf.size)
				continue
			}
			os.Chmod(target, 0o666)
		}
		pr.Status(fmt.Sprintf(T("Letöltés: %s"), rf.path))
		os.MkdirAll(filepath.Dir(target), 0o777)
		err := f.do(func() error {
			in, err := f.raw.open(rf.path)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			n, err := io.Copy(out, progressReader{in, pr})
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err == nil && rf.size > 0 && n != rf.size {
				err = sizeMismatch(n, rf.size)
			}
			return err
		})
		if err != nil {
			os.Remove(target)
			if errors.Is(err, errCancel) {
				return errCancel
			}
			pr.Err(rf.path, err)
			continue
		}
		if !rf.mod.IsZero() {
			os.Chtimes(target, rf.mod, rf.mod)
		}
	}
	return nil
}

// seekProgress: fájl olvasása folyamatjelzéssel, visszatekerhetően (a WebDAV így nem
// tölti a memóriába a teljes fájlt).
type seekProgress struct {
	f  *os.File
	pr Progress
}

func (s *seekProgress) Read(b []byte) (int, error) {
	if s.pr.Cancelled() {
		return 0, errCancel
	}
	n, err := s.f.Read(b)
	s.pr.Add(int64(n))
	return n, err
}

func (s *seekProgress) Seek(off int64, whence int) (int64, error) { return s.f.Seek(off, whence) }

func (f *genFS) Upload(srcs []string, remoteDir string, pr Progress) error {
	f.enter()
	defer f.leave()
	type up struct {
		local string
		rel   string
		dir   bool
		size  int64
		mod   time.Time
	}
	var ups []up
	var total int64
	for _, src := range srcs {
		parent := filepath.Dir(src)
		filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				pr.Err(p, err)
				return nil
			}
			rel, rerr := filepath.Rel(parent, p)
			if rerr != nil {
				return nil
			}
			ups = append(ups, up{p, filepath.ToSlash(rel), info.IsDir(), info.Size(), info.ModTime()})
			if !info.IsDir() {
				total += info.Size()
			}
			return nil
		})
	}
	pr.SetTotal(total)
	existing := map[string]map[string]rawInfo{}
	remoteNames := func(dir string) map[string]rawInfo {
		if m, ok := existing[dir]; ok {
			return m
		}
		m := map[string]rawInfo{}
		if list, err := f.rawList(dir); err == nil {
			for _, it := range list {
				m[it.Name] = it
			}
		}
		existing[dir] = m
		return m
	}
	for _, u := range ups {
		if pr.Cancelled() {
			return errCancel
		}
		remote := path.Join(remoteDir, u.rel)
		rdir, rname := path.Dir(remote), path.Base(remote)
		old, exists := remoteNames(rdir)[rname]
		if u.dir {
			if !exists {
				if err := f.do(func() error { return f.raw.mkdir(remote) }); err != nil {
					pr.Err(remote, err)
				}
				existing[remote] = map[string]rawInfo{}
			}
			continue
		}
		if exists {
			if old.Dir {
				pr.Err(remote, os.ErrExist)
				pr.Add(u.size)
				continue
			}
			ow, err := pr.AskOverwrite(f.Display(remote), fmt.Sprintf(T("A szerveren:  %s\nÚj:  %s"), sizeAndTime(old.Size, old.Mod.Local()), sizeAndTime(u.size, u.mod)))
			if err != nil {
				return err
			}
			if !ow {
				pr.Add(u.size)
				continue
			}
		}
		pr.Status(fmt.Sprintf(T("Feltöltés: %s"), u.local))
		err := f.do(func() error {
			in, err := os.Open(u.local)
			if err != nil {
				return err
			}
			defer in.Close()
			if err := f.raw.put(remote, &seekProgress{in, pr}, u.size); err != nil {
				return err
			}
			return checkSize(f.raw.size(remote))(u.size)
		})
		if err != nil {
			f.do(func() error { return f.raw.remove(remote, false) }) // félkész fájl ne maradjon
			if errors.Is(err, errCancel) {
				return errCancel
			}
			pr.Err(u.local, err)
			continue
		}
		f.do(func() error { return f.raw.chtimes(remote, u.mod) })
	}
	return nil
}

func (f *genFS) Delete(items []*Entry, pr Progress) error {
	f.enter()
	defer f.leave()
	files, dirs, err := f.collect(items, pr)
	if err != nil {
		return err
	}
	pr.SetTotal(int64(len(files) + len(dirs)))
	base := map[string]string{}
	for _, it := range items {
		if it.IsDir {
			base[it.Name] = it.Path
		}
	}
	remoteOf := func(rel string) string {
		top := strings.SplitN(rel, "/", 2)
		if b, ok := base[top[0]]; ok {
			if len(top) == 1 {
				return b
			}
			return path.Join(b, top[1])
		}
		return ""
	}
	for _, rf := range files {
		if pr.Cancelled() {
			return errCancel
		}
		pr.Status(fmt.Sprintf(T("Törlés: %s"), rf.path))
		if err := f.do(func() error { return f.raw.remove(rf.path, false) }); err != nil {
			pr.Err(rf.path, err)
		}
		pr.Add(1)
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.Count(dirs[i], "/") > strings.Count(dirs[j], "/") })
	for _, d := range dirs {
		if pr.Cancelled() {
			return errCancel
		}
		rp := remoteOf(d)
		if rp == "" {
			continue
		}
		pr.Status(fmt.Sprintf(T("Törlés: %s"), rp))
		if err := f.do(func() error { return f.raw.remove(rp, true) }); err != nil {
			pr.Err(rp, err)
		}
		pr.Add(1)
	}
	return nil
}

func (f *genFS) Rename(from, to string) error {
	return f.do(func() error { return f.raw.rename(from, to) })
}

func (f *genFS) Mkdir(dir, name string) error {
	full := path.Join(dir, strings.ReplaceAll(name, `\`, "/"))
	return f.do(func() error { return f.raw.mkdir(full) })
}

// displayURL: séma://felhasználó@gép[:port]
func displayURL(scheme, user, host string, port, defPort int) string {
	u := ""
	if user != "" {
		u = user + "@"
	}
	if port != 0 && port != defPort {
		host += ":" + strconv.Itoa(port)
	}
	return scheme + "://" + u + host
}

// idleText: „15 perc” stb.
func idleText(d time.Duration) string {
	m := int(d.Minutes() + 0.5)
	if m < 1 {
		return fmt.Sprintf(Tn(int(d.Seconds()), "%d másodperc"), int(d.Seconds()))
	}
	return Nf(m, "%d perc")
}

// idleTimeouts: a beállítható időkorlátok percben (0: soha).
var idleTimeouts = []int{5, 10, 15, 30, 60, 0}

func idleTimeoutLabel(m int) string {
	if m == 0 {
		return "soha"
	}
	return fmt.Sprintf(T("%s perc után"), strconv.Itoa(m))
}

func idleTimeoutLabels() []string {
	out := make([]string, len(idleTimeouts))
	for i, m := range idleTimeouts {
		out[i] = idleTimeoutLabel(m)
	}
	return out
}

func idleTimeoutIndex(m int) int {
	for i, v := range idleTimeouts {
		if v == m {
			return i
		}
	}
	return 2
}

// siteConf a mentett kapcsolatból (jelszó nélkül) kapcsolódási adatot készít.
func siteConf(s ftpSite) remoteConf {
	return remoteConf{Proto: s.Proto, Host: s.Host, Port: s.Port, User: s.User, Dir: s.Dir,
		TLS: s.TLS, Insecure: s.Insecure, KeyFile: s.KeyFile}
}

// needsPassword: kell-e jelszót kérni kapcsolódás előtt.
func needsPassword(s ftpSite) (ask bool, optional bool) {
	switch s.Proto {
	case "sftp":
		return true, true // kulccsal üresen is jó
	case "smb", "webdav":
		return s.User != "", false
	}
	return s.User != "" && !strings.EqualFold(s.User, "anonymous"), false
}

// idleMinutes: a tétlenségi időkorlát percben (0: soha); alapértelmezés 15.
func (a *App) idleMinutes() int {
	v, ok := a.settings.Get("remoteIdle")
	if !ok || v == "" {
		return 15
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 15
	}
	return n
}

// checkSize: a feltöltött fájl mérete egyezik-e a helyivel. Ha a szerver nem tudja
// megmondani a méretet, nem tekintjük hibának.
func checkSize(got int64, err error) func(want int64) error {
	return func(want int64) error {
		if err != nil || got == want {
			return nil
		}
		return sizeMismatch(got, want)
	}
}

func sizeMismatch(got, want int64) error {
	return fmt.Errorf(T("hiányos átvitel: %s érkezett meg a %s helyett"), fmtSize(got), fmtSize(want))
}
