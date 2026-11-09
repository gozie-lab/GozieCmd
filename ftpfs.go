package main

// FTP / FTPS kapcsolat mint virtuális mappa.

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
)

type ftpFS struct {
	conf  ftpConf
	c     *ftp.ServerConn
	start string
	idleTrack
}

func (f *ftpFS) StartDir() string { return f.start }

func dialFTP(conf ftpConf) (*ftpFS, error) {
	if conf.Port == 0 {
		conf.Port = 21
	}
	if conf.User == "" {
		conf.User, conf.Pass = "anonymous", "anonymous@"
	}
	f := &ftpFS{conf: conf}
	if err := f.connect(); err != nil {
		return nil, err
	}
	f.start = "/"
	if conf.Dir != "" {
		f.start = path.Clean("/" + strings.ReplaceAll(conf.Dir, `\`, "/"))
	} else if d, err := f.c.CurrentDir(); err == nil && strings.HasPrefix(d, "/") {
		f.start = d
	}
	return f, nil
}

func (f *ftpFS) connect() error {
	addr := net.JoinHostPort(f.conf.Host, strconv.Itoa(f.conf.Port))
	opts := []ftp.DialOption{ftp.DialWithTimeout(20 * time.Second)}
	if f.conf.TLS {
		opts = append(opts, ftp.DialWithExplicitTLS(&tls.Config{
			ServerName:         f.conf.Host,
			InsecureSkipVerify: f.conf.Insecure, //nolint:gosec // a felhasználó kifejezett kérésére
		}))
	}
	c, err := ftp.Dial(addr, opts...)
	if err != nil {
		return err
	}
	if err := c.Login(f.conf.User, f.conf.Pass); err != nil {
		c.Quit()
		return err
	}
	f.c = c
	return nil
}

// do lefuttatja a parancsot; megszakadt kapcsolatnál egyszer újracsatlakozik.
func (f *ftpFS) do(fn func(c *ftp.ServerConn) error) error {
	f.enter()
	defer f.leave()
	if f.c == nil {
		if err := f.connect(); err != nil {
			return err
		}
	}
	err := fn(f.c)
	var te *textproto.Error
	if err == nil || errors.As(err, &te) && te.Code != 421 || errors.Is(err, errCancel) {
		return err
	}
	f.reconnect()
	if f.c == nil {
		return err
	}
	return fn(f.c)
}

func (f *ftpFS) reconnect() {
	if f.c != nil {
		f.c.Quit()
		f.c = nil
	}
	f.connect()
}

func (f *ftpFS) Kind() string { return "ftp" }
func (f *ftpFS) Root() string { return "/" }

func (f *ftpFS) Info() string {
	s := "FTP: " + f.conf.Host
	if f.conf.TLS {
		s += T(" (titkosított)")
	}
	return s
}

func (f *ftpFS) Close() {
	if f.c != nil {
		f.c.Quit()
		f.c = nil
	}
}

func (f *ftpFS) list(dir string) ([]*ftp.Entry, error) {
	var res []*ftp.Entry
	err := f.do(func(c *ftp.ServerConn) error {
		var err error
		res, err = c.List(dir)
		return err
	})
	return res, err
}

func (f *ftpFS) List(dir string) ([]*Entry, error) {
	items, err := f.list(dir)
	if err != nil {
		return nil, err
	}
	var out []*Entry
	for _, it := range items {
		if it.Name == "." || it.Name == ".." || it.Name == "" {
			continue
		}
		isDir := it.Type == ftp.EntryTypeFolder || it.Type == ftp.EntryTypeLink
		out = append(out, newEntry(it.Name, path.Join(dir, it.Name), isDir, int64(it.Size), it.Time.Local()))
	}
	return out, nil
}

func (f *ftpFS) Parent(dir string) (string, bool) {
	if dir == "/" || dir == "" {
		return "", false
	}
	return path.Dir(dir), true
}

func (f *ftpFS) Join(dir, name string) string { return path.Join(dir, name) }

func (f *ftpFS) Display(dir string) string {
	u := ""
	if f.conf.User != "anonymous" {
		u = f.conf.User + "@"
	}
	host := f.conf.Host
	if f.conf.Port != 21 {
		host += ":" + strconv.Itoa(f.conf.Port)
	}
	scheme := "ftp://"
	if f.conf.TLS {
		scheme = "ftps://"
	}
	return scheme + u + host + dir
}

type remoteFile struct {
	path string
	rel  string
	size int64
	mod  time.Time
}

// collect bejárja a távoli mappákat; rel a célhoz viszonyított relatív útvonal.
func (f *ftpFS) collect(items []*Entry, pr Progress) (files []remoteFile, dirs []string, err error) {
	var walk func(remote, rel string) error
	walk = func(remote, rel string) error {
		if pr.Cancelled() {
			return errCancel
		}
		pr.Status(fmt.Sprintf(T("Lista: %s"), remote))
		list, err := f.list(remote)
		if err != nil {
			pr.Err(remote, err)
			return nil
		}
		for _, it := range list {
			if it.Name == "." || it.Name == ".." {
				continue
			}
			rp, rr := path.Join(remote, it.Name), rel+"/"+it.Name
			if it.Type == ftp.EntryTypeFolder {
				dirs = append(dirs, rr)
				if err := walk(rp, rr); err != nil {
					return err
				}
			} else if it.Type == ftp.EntryTypeFile {
				files = append(files, remoteFile{rp, rr, int64(it.Size), it.Time.Local()})
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

// Download letölti az elemeket (mappákat rekurzívan) a localDir mappába.
func (f *ftpFS) Download(items []*Entry, localDir string, pr Progress) error {
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
		err := f.do(func(c *ftp.ServerConn) error {
			resp, err := c.Retr(rf.path)
			if err != nil {
				return err
			}
			out, err := os.Create(target)
			if err != nil {
				resp.Close()
				return err
			}
			_, err = out.ReadFrom(progressReader{resp, pr})
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if cerr := resp.Close(); err == nil {
				err = cerr
			}
			return err
		})
		if err != nil {
			os.Remove(target)
			if errors.Is(err, errCancel) {
				f.reconnect() // a félbehagyott átvitel után tiszta kapcsolat kell
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

// Upload helyi fájlokat/mappákat tölt fel a remoteDir mappába.
func (f *ftpFS) Upload(srcs []string, remoteDir string, pr Progress) error {
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
	existing := map[string]map[string]*ftp.Entry{}
	remoteNames := func(dir string) map[string]*ftp.Entry {
		if m, ok := existing[dir]; ok {
			return m
		}
		m := map[string]*ftp.Entry{}
		if list, err := f.list(dir); err == nil {
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
		old := remoteNames(rdir)[rname]
		if u.dir {
			if old == nil {
				if err := f.do(func(c *ftp.ServerConn) error { return c.MakeDir(remote) }); err != nil {
					pr.Err(remote, err)
				}
				existing[remote] = map[string]*ftp.Entry{}
			}
			continue
		}
		if old != nil {
			if old.Type == ftp.EntryTypeFolder {
				pr.Err(remote, os.ErrExist)
				pr.Add(u.size)
				continue
			}
			ow, err := pr.AskOverwrite(f.Display(remote), fmt.Sprintf(T("A szerveren:  %s\nÚj:  %s"), sizeAndTime(int64(old.Size), old.Time.Local()), sizeAndTime(u.size, u.mod)))
			if err != nil {
				return err
			}
			if !ow {
				pr.Add(u.size)
				continue
			}
		}
		pr.Status(fmt.Sprintf(T("Feltöltés: %s"), u.local))
		err := f.do(func(c *ftp.ServerConn) error {
			in, err := os.Open(u.local)
			if err != nil {
				return err
			}
			defer in.Close()
			return c.Stor(remote, progressReader{in, pr})
		})
		if err != nil {
			if errors.Is(err, errCancel) {
				f.reconnect()
				f.do(func(c *ftp.ServerConn) error { return c.Delete(remote) }) // félkész fájl
				return errCancel
			}
			pr.Err(u.local, err)
		}
	}
	return nil
}

// Delete fájlokat és mappákat (tartalmukkal) töröl a szerverről.
func (f *ftpFS) Delete(items []*Entry, pr Progress) error {
	f.enter()
	defer f.leave()
	files, dirs, err := f.collect(items, pr)
	if err != nil {
		return err
	}
	pr.SetTotal(int64(len(files) + len(dirs)))
	base := map[string]string{} // rel → távoli útvonal a mappákhoz
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
		if err := f.do(func(c *ftp.ServerConn) error { return c.Delete(rf.path) }); err != nil {
			pr.Err(rf.path, err)
		}
		pr.Add(1)
	}
	// a legmélyebb mappák előbb
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
		if err := f.do(func(c *ftp.ServerConn) error { return c.RemoveDir(rp) }); err != nil {
			pr.Err(rp, err)
		}
		pr.Add(1)
	}
	return nil
}

func (f *ftpFS) Rename(from, to string) error {
	return f.do(func(c *ftp.ServerConn) error { return c.Rename(from, to) })
}

func (f *ftpFS) Mkdir(dir, name string) error {
	full := path.Join(dir, strings.ReplaceAll(name, `\`, "/"))
	return f.do(func(c *ftp.ServerConn) error { return c.MakeDir(full) })
}
