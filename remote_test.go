package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/webdav"
)

// startSFTP egy helyi SSH/SFTP-szervert indít jelszavas belépéssel; a gyökér a root mappa.
func startSFTP(t *testing.T, root string) (string, int) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if c.User() == "teszt" && string(pw) == "titok" {
			return nil, nil
		}
		return nil, io.EOF
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					ch, in, _ := nch.Accept()
					go func() {
						for req := range in {
							ok := req.Type == "subsystem" && string(req.Payload[4:]) == "sftp"
							req.Reply(ok, nil)
							if ok {
								srv, _ := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(root))
								srv.Serve()
								ch.Close()
							}
						}
					}()
				}
			}()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

// exercise: feltöltés, listázás, átnevezés, letöltés, törlés egy távoli nézeten.
func exercise(t *testing.T, f remoteVFS, base string) {
	local := t.TempDir()
	os.MkdirAll(filepath.Join(local, "mappa", "al"), 0o755)
	os.WriteFile(filepath.Join(local, "mappa", "egy.txt"), []byte("első fájl"), 0o644)
	os.WriteFile(filepath.Join(local, "mappa", "al", "ketto.bin"), make([]byte, 300000), 0o644)
	if err := f.Upload([]string{filepath.Join(local, "mappa")}, base, nopProgress{}); err != nil {
		t.Fatal("feltöltés:", err)
	}
	list, err := f.List(f.Join(base, "mappa"))
	if err != nil || len(list) != 2 {
		t.Fatalf("lista: %v %v", list, err)
	}
	if err := f.Rename(f.Join(base, "mappa/egy.txt"), f.Join(base, "mappa/uj nev.txt")); err != nil {
		t.Fatal("átnevezés:", err)
	}
	if err := f.Mkdir(f.Join(base, "mappa"), "üres"); err != nil {
		t.Fatal("mappa:", err)
	}
	top, _ := f.List(base)
	var dir *Entry
	for _, e := range top {
		if e.Name == "mappa" {
			dir = e
		}
	}
	if dir == nil || !dir.IsDir {
		t.Fatal("nincs meg a mappa", top)
	}
	back := t.TempDir()
	if err := f.Download([]*Entry{dir}, back, nopProgress{}); err != nil {
		t.Fatal("letöltés:", err)
	}
	b, _ := os.ReadFile(filepath.Join(back, "mappa", "uj nev.txt"))
	st, _ := os.Stat(filepath.Join(back, "mappa", "al", "ketto.bin"))
	if string(b) != "első fájl" || st == nil || st.Size() != 300000 {
		t.Fatalf("letöltött tartalom hibás: %q %v", b, st)
	}
	if fi, err := os.Stat(filepath.Join(back, "mappa", "üres")); err != nil || !fi.IsDir() {
		t.Fatal("az üres mappa nem jött le")
	}
	if err := f.Delete([]*Entry{dir}, nopProgress{}); err != nil {
		t.Fatal("törlés:", err)
	}
	top, _ = f.List(base)
	for _, e := range top {
		if e.Name == "mappa" {
			t.Fatal("a mappa nem törlődött")
		}
	}
	if _, busy := f.Idle(); busy {
		t.Fatal("a művelet után is foglaltnak látszik")
	}
}

func TestSFTP(t *testing.T) {
	root := t.TempDir()
	host, port := startSFTP(t, root)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	asked := 0
	conf := remoteConf{Proto: "sftp", Host: host, Port: port, User: "teszt", Pass: "titok", KnownHosts: kh,
		ConfirmHost: func(h, fp string, changed bool) bool {
			asked++
			if !strings.HasPrefix(fp, "SHA256:") || changed {
				t.Errorf("ujjlenyomat: %s %v", fp, changed)
			}
			return true
		}}
	f, err := dialRemote(conf)
	if err != nil {
		t.Fatal(err)
	}
	if f.StartDir() != root {
		t.Fatalf("kezdőmappa: %s", f.StartDir())
	}
	exercise(t, f, root)
	f.Close()
	// másodszor már nem kérdez (known_hosts)
	f, err = dialRemote(conf)
	if err != nil || asked != 1 {
		t.Fatal("második kapcsolódás:", err, asked)
	}
	f.Close()
	// rossz jelszó
	conf.Pass = "rossz"
	if _, err := dialRemote(conf); err == nil || !strings.Contains(err.Error(), "bejelentkezés") {
		t.Fatal("rossz jelszó:", err)
	}
	// elutasított gépkulcs
	conf.Pass = "titok"
	conf.KnownHosts = filepath.Join(t.TempDir(), "kh2")
	conf.ConfirmHost = func(string, string, bool) bool { return false }
	if _, err := dialRemote(conf); err == nil {
		t.Fatal("elutasított gépkulccsal is kapcsolódott")
	}
}

func TestWebDAV(t *testing.T) {
	root := t.TempDir()
	h := &webdav.Handler{Prefix: "/dav", FileSystem: webdav.Dir(root), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "teszt" || p != "titok" {
			w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
			w.WriteHeader(401)
			return
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	hp := strings.TrimPrefix(srv.URL, "http://")
	host, ps, _ := net.SplitHostPort(hp)
	port, _ := strconv.Atoi(ps)
	f, err := dialRemote(remoteConf{Proto: "webdav", Host: host + "/dav", Port: port, User: "teszt", Pass: "titok"})
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, f, "/")
	overwriteCheck(t, f, "/")
	if _, err := dialRemote(remoteConf{Proto: "webdav", Host: host + "/dav", Port: port, User: "teszt", Pass: "x"}); err == nil {
		t.Fatal("rossz jelszóval is kapcsolódott")
	}
}

func TestIdle(t *testing.T) {
	var tr idleTrack
	tr.enter()
	if _, busy := tr.Idle(); !busy {
		t.Fatal()
	}
	tr.leave()
	time.Sleep(20 * time.Millisecond)
	if d, busy := tr.Idle(); busy || d < 15*time.Millisecond {
		t.Fatal(d, busy)
	}
	if cleanHost("sftp://nas.local/") != "nas.local" || cleanHost(`\\gep\megosztas`) != "gep/megosztas" {
		t.Fatal(cleanHost("sftp://nas.local/"))
	}
	if protoIndex("webdav", true, protocols) != 3 || protoIndex("", false, protocols) != 0 {
		t.Fatal("protoIndex")
	}
}

// FTP: külső tesztszerverrel (GZ_FTP_ROOT = a szerver gyökérmappája, 127.0.0.1:2121,
// teszt/jelszo). A szerver nélkül kimarad.
func TestFTP(t *testing.T) {
	root := os.Getenv("GZ_FTP_ROOT")
	if root == "" {
		t.Skip("nincs FTP-tesztszerver")
	}
	conf := remoteConf{Proto: "ftp", Host: "127.0.0.1", Port: 2121, User: "teszt", Pass: "jelszo"}
	f, err := dialRemote(conf)
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, f, f.StartDir())
	// ékezetes, szóközös nevek
	local := t.TempDir()
	os.WriteFile(filepath.Join(local, "árvíztűrő tükörfúrógép.txt"), []byte("x"), 0o644)
	if err := f.Upload([]string{filepath.Join(local, "árvíztűrő tükörfúrógép.txt")}, f.StartDir(), nopProgress{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "árvíztűrő tükörfúrógép.txt")); err != nil {
		t.Error("ékezetes név a szerveren:", err)
	}
	f.Close()
	conf.Pass = "rossz"
	if _, err := dialRemote(conf); err == nil {
		t.Fatal("rossz jelszóval is kapcsolódott")
	}
}

// Feltöltés/letöltés meglévő fájlra: rákérdez, „nem” esetén nem ír felül.
func overwriteCheck(t *testing.T, f remoteVFS, base string) {
	local := t.TempDir()
	p := filepath.Join(local, "ow.txt")
	os.WriteFile(p, []byte("első"), 0o644)
	must(t, f.Upload([]string{p}, base, &testProg{}))
	os.WriteFile(p, []byte("második"), 0o644)
	pr := &testProg{overwrite: false}
	must(t, f.Upload([]string{p}, base, pr))
	if pr.asked != 1 {
		t.Errorf("feltöltés: rákérdezés %d", pr.asked)
	}
	var e *Entry
	es, err := f.List(base)
	must(t, err)
	for _, x := range es {
		if x.Name == "ow.txt" {
			e = x
		}
	}
	if e == nil {
		t.Fatal("nincs ow.txt")
	}
	pr = &testProg{overwrite: false}
	must(t, f.Download([]*Entry{e}, local, pr))
	if b, _ := os.ReadFile(p); string(b) != "második" || pr.asked != 1 {
		t.Errorf("letöltés felülírta / nem kérdezett: %q %d", b, pr.asked)
	}
	pr = &testProg{overwrite: true}
	must(t, f.Download([]*Entry{e}, local, pr))
	if b, _ := os.ReadFile(p); string(b) != "első" {
		t.Errorf("a „nem” ellenére felülírta a feltöltésnél: %q", b)
	}
	must(t, f.Delete([]*Entry{e}, &testProg{}))
}

func TestRemoteOverwrite(t *testing.T) {
	root := t.TempDir()
	host, port := startSFTP(t, root)
	f, err := dialRemote(remoteConf{Proto: "sftp", Host: host, Port: port, User: "teszt", Pass: "titok",
		KnownHosts: filepath.Join(t.TempDir(), "kh"), ConfirmHost: func(string, string, bool) bool { return true }})
	must(t, err)
	t.Run("sftp", func(t *testing.T) { overwriteCheck(t, f, root) })
	if r := os.Getenv("GZ_FTP_ROOT"); r != "" {
		g, err := dialRemote(remoteConf{Proto: "ftp", Host: "127.0.0.1", Port: 2121, User: "teszt", Pass: "jelszo"})
		must(t, err)
		t.Run("ftp", func(t *testing.T) { overwriteCheck(t, g, g.StartDir()) })
	}
}

// stallProgress: az első adag után megáll, amíg a release csatornát be nem zárják
// (egy lassú, hosszú letöltést utánoz).
type stallProgress struct {
	nopProgress
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *stallProgress) Add(n int64) {
	if n > 0 {
		s.once.Do(func() { close(s.started) })
		<-s.release
	}
}

// parallelCheck: futó letöltés közben a listázásnak azonnal válaszolnia kell
// (korábban a felület megvárta a teljes átvitelt, és addig „lefagyott”).
func parallelCheck(t *testing.T, f remoteVFS, base, localRoot string) {
	big := make([]byte, 4<<20)
	must(t, os.WriteFile(filepath.Join(localRoot, "nagy.bin"), big, 0o644))
	pr := &stallProgress{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	back := t.TempDir()
	go func() {
		done <- f.Download([]*Entry{{Name: "nagy.bin", Path: path.Join(base, "nagy.bin"), Size: int64(len(big))}}, back, pr)
	}()
	select {
	case <-pr.started:
	case err := <-done:
		t.Fatal("a letöltés túl korán véget ért:", err)
	case <-time.After(10 * time.Second):
		t.Fatal("a letöltés nem indult el")
	}
	listed := make(chan error, 1)
	go func() { _, err := f.List(base); listed <- err }()
	select {
	case err := <-listed:
		must(t, err)
	case <-time.After(3 * time.Second):
		close(pr.release)
		t.Fatal("letöltés közben a listázás várakozik")
	}
	close(pr.release)
	must(t, <-done)
	if st, err := os.Stat(filepath.Join(back, "nagy.bin")); err != nil || st.Size() != int64(len(big)) {
		t.Fatal("hiányos letöltés", err)
	}
}

func TestRemoteParallel(t *testing.T) {
	root := t.TempDir()
	host, port := startSFTP(t, root)
	conf := remoteConf{Proto: "sftp", Host: host, Port: port, User: "teszt", Pass: "titok",
		KnownHosts: filepath.Join(t.TempDir(), "kh"), ConfirmHost: func(string, string, bool) bool { return true }}
	f, err := dialRemote(conf)
	must(t, err)
	defer f.Close()
	t.Run("sftp", func(t *testing.T) { parallelCheck(t, f, root, root) })
	if r := os.Getenv("GZ_FTP_ROOT"); r != "" {
		g, err := dialRemote(remoteConf{Proto: "ftp", Host: "127.0.0.1", Port: 2121, User: "teszt", Pass: "jelszo"})
		must(t, err)
		defer g.Close()
		t.Run("ftp", func(t *testing.T) { parallelCheck(t, g, g.StartDir(), r) })
	}
}

// halfRaw: a feltöltésből csak a felét írja ki, hiba nélkül (egy hibás szervert utánoz).
type halfRaw struct {
	rawFS
	root string
}

func (h halfRaw) put(p string, in io.Reader, size int64) error {
	b, _ := io.ReadAll(in)
	return os.WriteFile(filepath.Join(h.root, p), b[:len(b)/2], 0o644)
}
func (h halfRaw) size(p string) (int64, error) {
	st, err := os.Stat(filepath.Join(h.root, p))
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}
func (h halfRaw) list(string) ([]rawInfo, error)  { return nil, nil }
func (h halfRaw) remove(p string, _ bool) error   { return os.Remove(filepath.Join(h.root, p)) }
func (h halfRaw) chtimes(string, time.Time) error { return nil }

type errCount struct {
	nopProgress
	n int
}

func (e *errCount) Err(string, error) { e.n++ }

// A hiányosan megérkezett fájl hibának számít, és nem marad ott a valódi nevén
// (mozgatásnál így a forrás sem törlődik).
func TestUploadSizeCheck(t *testing.T) {
	root, local := t.TempDir(), t.TempDir()
	src := filepath.Join(local, "a.bin")
	must(t, os.WriteFile(src, make([]byte, 1000), 0o644))
	f := &genFS{raw: halfRaw{root: root}, kind: "sftp"}
	pr := &errCount{}
	must(t, f.Upload([]string{src}, "/", pr))
	if pr.n != 1 {
		t.Fatalf("hibák: %d", pr.n)
	}
	if _, err := os.Stat(filepath.Join(root, "a.bin")); err == nil {
		t.Fatal("a hiányos fájl ott maradt")
	}
}

// stuckRaw: az első feltöltésnél elakad (a szerver nem válaszol), amíg a kapcsolatot
// nem bontják; újracsatlakozás után már rendben megy.
type stuckRaw struct {
	halfRaw
	mu     sync.Mutex
	stuck  chan struct{}
	stalls int
}

func (s *stuckRaw) put(p string, in io.Reader, size int64) error {
	s.mu.Lock()
	ch := s.stuck
	s.mu.Unlock()
	if ch != nil {
		<-ch
		return errors.New("connection lost")
	}
	b, _ := io.ReadAll(in)
	return os.WriteFile(filepath.Join(s.root, p), b, 0o644)
}
func (s *stuckRaw) size(p string) (int64, error) { return s.halfRaw.size(p) }
func (s *stuckRaw) list(string) ([]rawInfo, error) { return nil, nil }
func (s *stuckRaw) remove(p string, _ bool) error { return os.Remove(filepath.Join(s.root, p)) }
func (s *stuckRaw) chtimes(string, time.Time) error { return nil }
func (s *stuckRaw) close() {
	s.mu.Lock()
	if s.stuck != nil {
		close(s.stuck)
		s.stuck = nil
		s.stalls++
	}
	s.mu.Unlock()
}
func (s *stuckRaw) reconnect() error { return nil }

// Az őr felismeri az elakadt átvitelt, bontja a kapcsolatot, és az újrapróbálás sikerül.
func TestStallWatchdog(t *testing.T) {
	old := stallLimit
	stallLimit = 300 * time.Millisecond
	defer func() { stallLimit = old }()
	root, local := t.TempDir(), t.TempDir()
	for _, n := range []string{"a.bin", "b.bin"} {
		must(t, os.WriteFile(filepath.Join(local, n), make([]byte, 5000), 0o644))
	}
	r := &stuckRaw{halfRaw: halfRaw{root: root}, stuck: make(chan struct{})}
	f := &genFS{raw: r, kind: "sftp"}
	pr := &errCount{}
	done := make(chan error, 1)
	go func() {
		done <- f.Upload([]string{filepath.Join(local, "a.bin"), filepath.Join(local, "b.bin")}, "/", pr)
	}()
	select {
	case err := <-done:
		must(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("az őr nem oldotta fel az elakadást")
	}
	r.mu.Lock()
	stalls := r.stalls
	r.mu.Unlock()
	if pr.n != 0 || stalls != 1 {
		t.Fatalf("hibák: %d, elakadások: %d", pr.n, stalls)
	}
	for _, n := range []string{"a.bin", "b.bin"} {
		if st, err := os.Stat(filepath.Join(root, n)); err != nil || st.Size() != 5000 {
			t.Fatal(n, err)
		}
	}
}
