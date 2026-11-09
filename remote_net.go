package main

// SFTP, WebDAV és SMB alsó rétegei a genFS-hez.

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hirochachacha/go-smb2"
	"github.com/pkg/sftp"
	"github.com/studio-b12/gowebdav"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const dialTimeout = 20 * time.Second

// ── SFTP ──

type sftpRaw struct {
	conf remoteConf
	busy func() bool // fut-e éppen átvitel (akkor nem kell életjel)
	mu   sync.Mutex // csak a mezőkhöz; maguk a műveletek párhuzamosan futhatnak
	ssh  *ssh.Client
	c    *sftp.Client
}

func dialSFTP(conf remoteConf) (*genFS, error) {
	if conf.Port == 0 {
		conf.Port = 22
	}
	r := &sftpRaw{conf: conf}
	if err := r.connect(); err != nil {
		return nil, err
	}
	f := &genFS{raw: r, kind: "sftp", info: "SFTP: " + conf.Host,
		display: displayURL("sftp", conf.User, conf.Host, conf.Port, 22)}
	r.mu.Lock()
	r.busy = func() bool { _, b := f.Idle(); return b }
	r.mu.Unlock()
	f.start = "/"
	if conf.Dir != "" {
		f.start = path.Clean("/" + strings.ReplaceAll(conf.Dir, `\`, "/"))
	} else if wd, err := r.c.Getwd(); err == nil && strings.HasPrefix(wd, "/") {
		f.start = wd
	}
	return f, nil
}

// sshFingerprint: SHA256:… (ahogy az ssh is kiírja).
func sshFingerprint(k ssh.PublicKey) string { return ssh.FingerprintSHA256(k) }

func (r *sftpRaw) hostKeyCallback() ssh.HostKeyCallback {
	var files []string
	if home, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(home, ".ssh", "known_hosts"); fileExists(p) {
			files = append(files, p)
		}
	}
	if r.conf.KnownHosts != "" && fileExists(r.conf.KnownHosts) {
		files = append(files, r.conf.KnownHosts)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		changed := false
		if len(files) > 0 {
			cb, err := knownhosts.New(files...)
			if err == nil {
				err = cb(hostname, remote, key)
				if err == nil {
					return nil
				}
				var ke *knownhosts.KeyError
				if errors.As(err, &ke) && len(ke.Want) > 0 {
					changed = true
				}
			}
		}
		if r.conf.ConfirmHost == nil || !r.conf.ConfirmHost(hostname, sshFingerprint(key), changed) {
			if changed {
				return errors.New(T("a szerver gépkulcsa megváltozott, a kapcsolódás megszakítva"))
			}
			return errors.New(T("a szerver gépkulcsát nem fogadtad el"))
		}
		if r.conf.KnownHosts != "" {
			os.MkdirAll(filepath.Dir(r.conf.KnownHosts), 0o700)
			if fh, err := os.OpenFile(r.conf.KnownHosts, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				fmt.Fprintln(fh, knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key))
				fh.Close()
			}
		}
		return nil
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// authMethods: kulcsfájl, ssh-ügynök, a szokásos kulcsok, végül jelszó.
func (r *sftpRaw) authMethods() ([]ssh.AuthMethod, error) {
	var signers []ssh.Signer
	tryKey := func(p string, must bool) error {
		b, err := os.ReadFile(p)
		if err != nil {
			if must {
				return fmt.Errorf(T("a kulcsfájl nem olvasható: %v"), err)
			}
			return nil
		}
		s, err := ssh.ParsePrivateKey(b)
		var pe *ssh.PassphraseMissingError
		if errors.As(err, &pe) {
			if r.conf.Pass == "" {
				if must {
					return errors.New(T("a kulcsfájl jelmondattal védett: add meg a jelszó mezőben"))
				}
				return nil
			}
			s, err = ssh.ParsePrivateKeyWithPassphrase(b, []byte(r.conf.Pass))
		}
		if err != nil {
			if must {
				return fmt.Errorf(T("a kulcsfájl nem használható: %v"), err)
			}
			return nil
		}
		signers = append(signers, s)
		return nil
	}
	if r.conf.KeyFile != "" {
		if err := tryKey(r.conf.KeyFile, true); err != nil {
			return nil, err
		}
	}
	var methods []ssh.AuthMethod
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.DialTimeout("unix", sock, 3*time.Second); err == nil {
			ag := agent.NewClient(conn)
			if list, err := ag.Signers(); err == nil {
				signers = append(signers, list...)
			}
		}
	}
	if r.conf.KeyFile == "" {
		if home, err := os.UserHomeDir(); err == nil {
			for _, n := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
				tryKey(filepath.Join(home, ".ssh", n), false)
			}
		}
	}
	if len(signers) > 0 {
		methods = append(methods, ssh.PublicKeys(signers...))
	}
	if r.conf.Pass != "" {
		pw := r.conf.Pass
		methods = append(methods, ssh.Password(pw),
			ssh.KeyboardInteractive(func(user, instr string, qs []string, echos []bool) ([]string, error) {
				ans := make([]string, len(qs))
				for i := range qs {
					ans[i] = pw
				}
				return ans, nil
			}))
	}
	if len(methods) == 0 {
		return nil, errors.New(T("nincs jelszó és nem található SSH-kulcs"))
	}
	return methods, nil
}

func (r *sftpRaw) connect() error {
	auth, err := r.authMethods()
	if err != nil {
		return err
	}
	user := r.conf.User
	if user == "" {
		user = os.Getenv("USER")
		if user == "" {
			user = os.Getenv("USERNAME")
		}
	}
	cfg := &ssh.ClientConfig{User: user, Auth: auth, HostKeyCallback: r.hostKeyCallback(), Timeout: dialTimeout}
	cl, err := ssh.Dial("tcp", net.JoinHostPort(r.conf.Host, strconv.Itoa(r.conf.Port)), cfg)
	if err != nil {
		return sshErr(err)
	}
	c, err := sftp.NewClient(cl)
	if err != nil {
		cl.Close()
		return fmt.Errorf(T("a szerveren nem érhető el az SFTP: %v"), err)
	}
	r.mu.Lock()
	r.ssh, r.c = cl, c
	r.mu.Unlock()
	go sshKeepalive(cl, r.isBusy)
	return nil
}

// sshKeepalive félpercenként jelez a szervernek. Ha a kapcsolat csendben elhalt
// (alvó gép, Wi-Fi-váltás, tűzfal), a választ nem kapja meg, és bontja a kapcsolatot:
// így a rajta várakozó műveletek hibával visszatérnek, ahelyett hogy örökké várnának.
//
// Átvitel közben nem kérdez: egy lassú feltöltésnél a válasz a kimenő adatok mögött
// sorban állva akár percekig is késhet, és ilyenkor tévesen bontaná a kapcsolatot.
// Ha az átvitel közben szakad meg a hálózat, azt maga az átvitel jelzi hibával.
func sshKeepalive(cl *ssh.Client, busy func() bool) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		if busy != nil && busy() {
			continue
		}
		res := make(chan error, 1)
		go func() {
			_, _, err := cl.SendRequest("keepalive@openssh.com", true, nil)
			res <- err
		}()
		select {
		case err := <-res:
			if err != nil {
				cl.Close()
				return
			}
		case <-time.After(45 * time.Second):
			dlog("SFTP: a szerver nem válaszol, a kapcsolat bontva")
			cl.Close()
			return
		}
	}
}

func sshErr(err error) error {
	s := err.Error()
	if strings.Contains(s, "unable to authenticate") {
		return errors.New(T("a bejelentkezés nem sikerült: hibás felhasználónév, jelszó vagy kulcs"))
	}
	return err
}

func (r *sftpRaw) close() {
	r.mu.Lock()
	c, cl := r.c, r.ssh
	r.c, r.ssh = nil, nil
	r.mu.Unlock()
	if c != nil {
		c.Close()
	}
	if cl != nil {
		cl.Close()
	}
}

func (r *sftpRaw) reconnect() error {
	r.close()
	return r.connect()
}

func (r *sftpRaw) client() (*sftp.Client, error) {
	r.mu.Lock()
	c := r.c
	r.mu.Unlock()
	if c != nil {
		return c, nil
	}
	if err := r.connect(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c == nil {
		return nil, errors.New(T("a kapcsolat megszakadt"))
	}
	return r.c, nil
}

func (r *sftpRaw) list(dir string) ([]rawInfo, error) {
	c, err := r.client()
	if err != nil {
		return nil, err
	}
	fis, err := c.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]rawInfo, 0, len(fis))
	for _, fi := range fis {
		isDir := fi.IsDir()
		size := fi.Size()
		if fi.Mode()&os.ModeSymlink != 0 { // a hivatkozás célja számít
			if st, err := c.Stat(path.Join(dir, fi.Name())); err == nil {
				isDir, size = st.IsDir(), st.Size()
			}
		}
		if isDir {
			size = 0
		}
		out = append(out, rawInfo{Name: fi.Name(), Dir: isDir, Size: size, Mod: fi.ModTime()})
	}
	return out, nil
}

func (r *sftpRaw) open(p string) (io.ReadCloser, error) {
	c, err := r.client()
	if err != nil {
		return nil, err
	}
	return c.Open(p)
}

func (r *sftpRaw) put(p string, in io.Reader, size int64) error {
	c, err := r.client()
	if err != nil {
		return err
	}
	out, err := c.Create(p)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

func (r *sftpRaw) isBusy() bool {
	r.mu.Lock()
	b := r.busy
	r.mu.Unlock()
	return b != nil && b()
}

func (r *sftpRaw) size(p string) (int64, error) {
	c, err := r.client()
	if err != nil {
		return 0, err
	}
	st, err := c.Stat(p)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (r *sftpRaw) mkdir(p string) error {
	c, err := r.client()
	if err != nil {
		return err
	}
	return c.Mkdir(p)
}

func (r *sftpRaw) remove(p string, dir bool) error {
	c, err := r.client()
	if err != nil {
		return err
	}
	if dir {
		return c.RemoveDirectory(p)
	}
	return c.Remove(p)
}

func (r *sftpRaw) rename(from, to string) error {
	c, err := r.client()
	if err != nil {
		return err
	}
	if err := c.PosixRename(from, to); err == nil {
		return nil
	}
	return c.Rename(from, to)
}

func (r *sftpRaw) chtimes(p string, t time.Time) error {
	c, err := r.client()
	if err != nil {
		return err
	}
	return c.Chtimes(p, t, t)
}

// ── WebDAV ──

type davRaw struct {
	c *gowebdav.Client
}

func dialWebDAV(conf remoteConf) (*genFS, error) {
	host := cleanHost(conf.Host)
	hostOnly, base, _ := strings.Cut(host, "/")
	scheme := "http"
	defPort := 80
	if conf.TLS {
		scheme, defPort = "https", 443
	}
	if conf.Port == 0 {
		conf.Port = defPort
	}
	hp := hostOnly
	if conf.Port != defPort && !strings.Contains(hostOnly, ":") {
		hp = net.JoinHostPort(hostOnly, strconv.Itoa(conf.Port))
	}
	url := scheme + "://" + hp + "/" + base
	c := gowebdav.NewClient(url, conf.User, conf.Pass)
	c.SetTimeout(0)
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 60 * time.Second,
		DialContext: (&net.Dialer{Timeout: dialTimeout}).DialContext}
	if conf.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // a felhasználó kérésére
	}
	c.SetTransport(tr)
	if err := c.Connect(); err != nil {
		return nil, davErr(err)
	}
	f := &genFS{raw: &davRaw{c: c}, kind: "webdav", info: "WebDAV: " + hostOnly,
		display: strings.TrimSuffix(displayURL(map[bool]string{true: "davs", false: "dav"}[conf.TLS], conf.User, hostOnly, conf.Port, defPort)+"/"+base, "/")}
	f.start = "/"
	if conf.Dir != "" {
		f.start = path.Clean("/" + strings.ReplaceAll(conf.Dir, `\`, "/"))
	}
	return f, nil
}

func davErr(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "401"), strings.Contains(strings.ToLower(s), "unauthorized"):
		return errors.New(T("a bejelentkezés nem sikerült: hibás felhasználónév vagy jelszó"))
	case strings.Contains(s, "404"):
		return errors.New(T("a megadott címen nincs WebDAV-mappa (404)"))
	}
	return err
}

func (r *davRaw) list(dir string) ([]rawInfo, error) {
	fis, err := r.c.ReadDir(dir)
	if err != nil {
		return nil, davErr(err)
	}
	out := make([]rawInfo, 0, len(fis))
	for _, fi := range fis {
		out = append(out, rawInfo{Name: fi.Name(), Dir: fi.IsDir(), Size: fi.Size(), Mod: fi.ModTime()})
	}
	return out, nil
}

func (r *davRaw) open(p string) (io.ReadCloser, error)         { return r.c.ReadStream(p) }
func (r *davRaw) put(p string, in io.Reader, size int64) error { return r.c.WriteStream(p, in, 0o644) }
func (r *davRaw) size(p string) (int64, error) {
	st, err := r.c.Stat(p)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}
func (r *davRaw) mkdir(p string) error                         { return r.c.Mkdir(p, 0o755) }
func (r *davRaw) remove(p string, dir bool) error              { return r.c.Remove(p) }
func (r *davRaw) rename(from, to string) error                 { return r.c.Rename(from, to, false) }
func (r *davRaw) chtimes(string, time.Time) error              { return nil } // a WebDAV nem tudja
func (r *davRaw) reconnect() error                             { return nil } // HTTP: nincs állandó kapcsolat
func (r *davRaw) close()                                       {}

// ── SMB (Windows-megosztás) ──
// Útvonal: "/" a megosztások listája, "/megosztás/mappa/…" a tartalom.

type smbRaw struct {
	conf   remoteConf
	mu     sync.Mutex // a kapcsolat mezőihez és a megosztáslistához
	conn   net.Conn
	s      *smb2.Session
	shares map[string]*smb2.Share
}

func dialSMB(conf remoteConf) (*genFS, error) {
	host := cleanHost(conf.Host)
	h, rest, _ := strings.Cut(host, "/")
	conf.Host = h
	if conf.Port == 0 {
		conf.Port = 445
	}
	r := &smbRaw{conf: conf}
	if err := r.connect(); err != nil {
		return nil, err
	}
	f := &genFS{raw: r, kind: "smb", info: "SMB: " + h,
		display: strings.TrimSuffix(displayURL("smb", conf.User, h, conf.Port, 445), "")}
	start := conf.Dir
	if start == "" {
		start = rest
	}
	f.start = path.Clean("/" + strings.ReplaceAll(start, `\`, "/"))
	return f, nil
}

func (r *smbRaw) connect() error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(r.conf.Host, strconv.Itoa(r.conf.Port)), dialTimeout)
	if err != nil {
		return err
	}
	user, domain := r.conf.User, ""
	if d, u, ok := strings.Cut(user, `\`); ok {
		domain, user = d, u
	} else if u, d, ok := strings.Cut(user, "@"); ok {
		user, domain = u, d
	}
	d := &smb2.Dialer{Initiator: &smb2.NTLMInitiator{User: user, Password: r.conf.Pass, Domain: domain}}
	s, err := d.Dial(conn)
	if err != nil {
		conn.Close()
		if strings.Contains(err.Error(), "LOGON_FAILURE") || strings.Contains(err.Error(), "logon failure") {
			return errors.New(T("a bejelentkezés nem sikerült: hibás felhasználónév vagy jelszó"))
		}
		return err
	}
	r.mu.Lock()
	r.conn, r.s, r.shares = conn, s, map[string]*smb2.Share{}
	r.mu.Unlock()
	return nil
}

// session az élő munkamenet; ha nincs, csatlakozik.
func (r *smbRaw) session() (*smb2.Session, error) {
	r.mu.Lock()
	s := r.s
	r.mu.Unlock()
	if s != nil {
		return s, nil
	}
	if err := r.connect(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.s == nil {
		return nil, errors.New(T("a kapcsolat megszakadt"))
	}
	return r.s, nil
}

func (r *smbRaw) close() {
	r.mu.Lock()
	shares, s, conn := r.shares, r.s, r.conn
	r.shares, r.s, r.conn = nil, nil, nil
	r.mu.Unlock()
	for _, sh := range shares {
		sh.Umount()
	}
	if s != nil {
		s.Logoff()
	}
	if conn != nil {
		conn.Close()
	}
}

func (r *smbRaw) reconnect() error {
	r.close()
	return r.connect()
}

// split: "/megosztás/a/b" → megosztás, `a\b`
func (r *smbRaw) split(p string) (*smb2.Share, string, error) {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	share, rest, _ := strings.Cut(p, "/")
	if share == "" {
		return nil, "", errors.New(T("ez a megosztások listája: itt nem lehet fájlművelet"))
	}
	s, err := r.session()
	if err != nil {
		return nil, "", err
	}
	key := strings.ToLower(share)
	r.mu.Lock()
	sh, ok := r.shares[key]
	r.mu.Unlock()
	if !ok {
		sh, err = s.Mount(`\\` + r.conf.Host + `\` + share)
		if err != nil {
			return nil, "", fmt.Errorf(T("a(z) „%s” megosztás nem nyitható meg: %v"), share, err)
		}
		r.mu.Lock()
		if old, dup := r.shares[key]; dup { // közben egy másik művelet is csatolta
			r.mu.Unlock()
			sh.Umount()
			sh = old
		} else {
			if r.shares != nil {
				r.shares[key] = sh
			}
			r.mu.Unlock()
		}
	}
	return sh, strings.ReplaceAll(rest, "/", `\`), nil
}

func (r *smbRaw) list(dir string) ([]rawInfo, error) {
	if path.Clean("/"+dir) == "/" {
		s, err := r.session()
		if err != nil {
			return nil, err
		}
		names, err := s.ListSharenames()
		if err != nil {
			return nil, err
		}
		var out []rawInfo
		for _, n := range names {
			if strings.HasSuffix(n, "$") { // rejtett és rendszermegosztások
				continue
			}
			out = append(out, rawInfo{Name: n, Dir: true})
		}
		return out, nil
	}
	sh, rel, err := r.split(dir)
	if err != nil {
		return nil, err
	}
	fis, err := sh.ReadDir(rel)
	if err != nil {
		return nil, err
	}
	out := make([]rawInfo, 0, len(fis))
	for _, fi := range fis {
		out = append(out, rawInfo{Name: fi.Name(), Dir: fi.IsDir(), Size: fi.Size(), Mod: fi.ModTime()})
	}
	return out, nil
}

func (r *smbRaw) open(p string) (io.ReadCloser, error) {
	sh, rel, err := r.split(p)
	if err != nil {
		return nil, err
	}
	return sh.Open(rel)
}

func (r *smbRaw) put(p string, in io.Reader, size int64) error {
	sh, rel, err := r.split(p)
	if err != nil {
		return err
	}
	out, err := sh.Create(rel)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

func (r *smbRaw) size(p string) (int64, error) {
	sh, rel, err := r.split(p)
	if err != nil {
		return 0, err
	}
	st, err := sh.Stat(rel)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (r *smbRaw) mkdir(p string) error {
	sh, rel, err := r.split(p)
	if err != nil {
		return err
	}
	return sh.Mkdir(rel, 0o755)
}

func (r *smbRaw) remove(p string, dir bool) error {
	sh, rel, err := r.split(p)
	if err != nil {
		return err
	}
	if rel == "" {
		return errors.New(T("magát a megosztást nem lehet törölni"))
	}
	return sh.Remove(rel)
}

func (r *smbRaw) rename(from, to string) error {
	sh, a, err := r.split(from)
	if err != nil {
		return err
	}
	sh2, b, err := r.split(to)
	if err != nil {
		return err
	}
	if sh != sh2 {
		return errors.New(T("megosztások között nem lehet átnevezni"))
	}
	return sh.Rename(a, b)
}

func (r *smbRaw) chtimes(p string, t time.Time) error {
	sh, rel, err := r.split(p)
	if err != nil {
		return err
	}
	return sh.Chtimes(rel, t, t)
}
