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
	r.ssh, r.c = cl, c
	return nil
}

func sshErr(err error) error {
	s := err.Error()
	if strings.Contains(s, "unable to authenticate") {
		return errors.New(T("a bejelentkezés nem sikerült: hibás felhasználónév, jelszó vagy kulcs"))
	}
	return err
}

func (r *sftpRaw) close() {
	if r.c != nil {
		r.c.Close()
		r.c = nil
	}
	if r.ssh != nil {
		r.ssh.Close()
		r.ssh = nil
	}
}

func (r *sftpRaw) reconnect() error {
	r.close()
	return r.connect()
}

func (r *sftpRaw) client() (*sftp.Client, error) {
	if r.c == nil {
		if err := r.connect(); err != nil {
			return nil, err
		}
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
	r.conn, r.s, r.shares = conn, s, map[string]*smb2.Share{}
	return nil
}

func (r *smbRaw) close() {
	for _, sh := range r.shares {
		sh.Umount()
	}
	r.shares = nil
	if r.s != nil {
		r.s.Logoff()
		r.s = nil
	}
	if r.conn != nil {
		r.conn.Close()
		r.conn = nil
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
	if r.s == nil {
		if err := r.connect(); err != nil {
			return nil, "", err
		}
	}
	sh, ok := r.shares[strings.ToLower(share)]
	if !ok {
		var err error
		sh, err = r.s.Mount(`\\` + r.conf.Host + `\` + share)
		if err != nil {
			return nil, "", fmt.Errorf(T("a(z) „%s” megosztás nem nyitható meg: %v"), share, err)
		}
		r.shares[strings.ToLower(share)] = sh
	}
	return sh, strings.ReplaceAll(rest, "/", `\`), nil
}

func (r *smbRaw) list(dir string) ([]rawInfo, error) {
	if path.Clean("/"+dir) == "/" {
		if r.s == nil {
			if err := r.connect(); err != nil {
				return nil, err
			}
		}
		names, err := r.s.ListSharenames()
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
