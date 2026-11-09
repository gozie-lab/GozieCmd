package main

// Archívumok közös kezelése: formátum felismerése, általános (nem ZIP) archívum
// mint virtuális mappa, olvasó/író „motorok”: TAR(+GZ/BZ2/XZ), GZ/BZ2/XZ, 7Z, RAR,
// valamint a telepített 7-Zip (7z.exe) a CAB/ARJ/LZH formátumokhoz és a 7Z íráshoz.

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
	"github.com/ulikunitz/xz"
)

// Archive minden archívum (ZIP és a többi) közös felülete.
type Archive interface {
	VFS
	ArchivePath() string
	Format() string
	Writable() bool
	Extract(items []*Entry, dstDir string, pr Progress) error
	AddLocal(srcs []string, intoDir string, pr Progress) error
	Delete(items []*Entry, pr Progress) error
	Mkdir(dir, name string) error
}

var errReadOnly = errors.New(T("ez az archívumformátum csak olvasható"))

// sevenZipExe a telepített 7-Zip parancssori programja (üres, ha nincs).
var sevenZipExe = func() string { return "" }

// archiveFormat a fájlnévből a formátum ("" ha nem archívum).
func archiveFormat(name string) string {
	n := strings.ToLower(name)
	for _, s := range []struct{ suf, f string }{
		{".tar.gz", "tgz"}, {".tgz", "tgz"}, {".tar.bz2", "tbz"}, {".tbz2", "tbz"}, {".tbz", "tbz"},
		{".tar.xz", "txz"}, {".txz", "txz"}, {".tar", "tar"},
		{".zip", "zip"}, {".jar", "zip"}, {".apk", "zip"}, {".epub", "zip"}, {".docx", ""}, {".xlsx", ""},
		{".7z", "7z"}, {".rar", "rar"}, {".gz", "gz"}, {".bz2", "bz2"}, {".xz", "xz"},
		{".cab", "ext"}, {".arj", "ext"}, {".lzh", "ext"}, {".lha", "ext"},
		{".ace", "nope"}, {".uc2", "nope"},
	} {
		if strings.HasSuffix(n, s.suf) {
			return s.f
		}
	}
	return ""
}

func isArchive(name string) bool {
	f := archiveFormat(name)
	return f != ""
}

// openArchive megnyitja (vagy újként előkészíti) az archívumot.
func openArchive(p string) (Archive, error) {
	switch f := archiveFormat(p); f {
	case "zip":
		return openZipFS(p)
	case "tar", "tgz", "tbz", "txz":
		return newArcFS(p, f, &tarBackend{path: p, comp: f})
	case "gz", "bz2", "xz":
		return newArcFS(p, f, &singleBackend{path: p, comp: f})
	case "7z":
		return newArcFS(p, f, &sevenBackend{path: p})
	case "rar":
		return newArcFS(p, f, &rarBackend{path: p})
	case "ext":
		exe := sevenZipExe()
		if exe == "" {
			return nil, errors.New(fmt.Sprintf(T("ehhez a formátumhoz (%s) a 7-Zip program szükséges. Telepítsd a 7-zip.org oldalról, vagy add meg a helyét a Beállításokban."), strings.ToUpper(strings.TrimPrefix(filepath.Ext(p), "."))))
		}
		return newArcFS(p, f, &exeBackend{exe: exe, path: p})
	case "nope":
		return nil, errors.New(T("az ACE és UC2 archívumokat a program nem tudja kezelni: az ACE formátumhoz nincs szabadon használható kicsomagoló, az UC2 pedig 16 bites DOS-program, ami 64 bites Windowson nem fut"))
	}
	return nil, errors.New(T("ismeretlen archívumformátum"))
}

// ── általános archívum ──

type arcItem struct {
	Name  string // "/"-rel tagolt belső útvonal
	Size  int64
	Mod   time.Time
	IsDir bool
}

type arcBackend interface {
	list() ([]arcItem, error)
	// extract minden kívánt fájlra meghívja a sink-et a tartalmával.
	extract(want func(name string) bool, sink func(it arcItem, r io.Reader) error, pr Progress) error
}

// arcWriter: írható formátumok (TAR-félék natívan, 7Z a 7-Zippel).
type arcWriter interface {
	add(adds []arcAdd, replace map[string]bool, pr Progress) error
	remove(match func(name string) bool, pr Progress) error
}

type arcAdd struct {
	local string
	name  string
	info  os.FileInfo
}

type arcFS struct {
	archive  string
	format   string
	be       arcBackend
	children map[string][]*Entry
	dirs     map[string]*Entry
	files    map[string]*Entry
}

func newArcFS(p, format string, be arcBackend) (*arcFS, error) {
	a := &arcFS{archive: p, format: format, be: be}
	return a, a.load()
}

func cleanArcName(n string) string {
	n = strings.ReplaceAll(n, `\`, "/")
	n = path.Clean("/" + n)
	return strings.TrimPrefix(n, "/")
}

func (a *arcFS) load() error {
	a.children = map[string][]*Entry{}
	a.dirs = map[string]*Entry{}
	a.files = map[string]*Entry{}
	if _, err := os.Stat(a.archive); os.IsNotExist(err) {
		return nil
	}
	items, err := a.be.list()
	if err != nil {
		return err
	}
	var ensureDir func(d string) *Entry
	ensureDir = func(d string) *Entry {
		if d == "" {
			return nil
		}
		if e, ok := a.dirs[d]; ok {
			return e
		}
		ensureDir(pdir(d))
		e := newEntry(path.Base(d), d, true, 0, time.Time{})
		a.dirs[d] = e
		a.children[pdir(d)] = append(a.children[pdir(d)], e)
		return e
	}
	for _, it := range items {
		name := cleanArcName(it.Name)
		if name == "" {
			continue
		}
		if it.IsDir {
			if e := ensureDir(name); e != nil && e.ModTime.IsZero() {
				e.ModTime = it.Mod
			}
			continue
		}
		if _, dup := a.files[name]; dup || a.dirs[name] != nil {
			continue
		}
		ensureDir(pdir(name))
		e := newEntry(path.Base(name), name, false, it.Size, it.Mod)
		a.files[name] = e
		a.children[pdir(name)] = append(a.children[pdir(name)], e)
	}
	return nil
}

func (a *arcFS) Kind() string        { return "arc" }
func (a *arcFS) Root() string        { return "" }
func (a *arcFS) Close()              {}
func (a *arcFS) ArchivePath() string { return a.archive }
func (a *arcFS) Format() string      { return a.format }
func (a *arcFS) Info() string {
	return fmt.Sprintf(T("%s-archívum%s"), strings.ToUpper(a.format), map[bool]string{true: "", false: T(" (csak olvasható)")}[a.Writable()])
}

func (a *arcFS) Writable() bool {
	w, ok := a.be.(arcWriter)
	if !ok {
		return false
	}
	if sb, ok := w.(*sevenBackend); ok {
		return sb.exe() != ""
	}
	return true
}

func (a *arcFS) List(dir string) ([]*Entry, error) {
	src := a.children[dir]
	out := make([]*Entry, len(src))
	copy(out, src)
	return out, nil
}

func (a *arcFS) Parent(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	return pdir(dir), true
}

func (a *arcFS) Join(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func (a *arcFS) Display(dir string) string {
	if dir == "" {
		return a.archive
	}
	return a.archive + string(os.PathSeparator) + filepath.FromSlash(dir)
}

func (a *arcFS) Extract(items []*Entry, dstDir string, pr Progress) error {
	strip := map[string]int{}
	var total int64
	for _, it := range items {
		s := len(pdir(it.Path))
		if s > 0 {
			s++
		}
		strip[it.Path] = s
		if it.IsDir {
			if p, ok := safeJoin(dstDir, it.Path[s:]); ok {
				os.MkdirAll(p, 0o777)
			}
		}
	}
	owner := func(name string) (string, bool) {
		for _, it := range items {
			if inside(name, it.Path) {
				return it.Path, true
			}
		}
		return "", false
	}
	for n, e := range a.files {
		if _, ok := owner(n); ok {
			total += e.Size
		}
	}
	for d := range a.dirs {
		if o, ok := owner(d); ok {
			if p, ok := safeJoin(dstDir, d[strip[o]:]); ok {
				os.MkdirAll(p, 0o777)
			}
		}
	}
	pr.SetTotal(total)
	return a.be.extract(func(name string) bool {
		_, ok := owner(cleanArcName(name))
		return ok
	}, func(it arcItem, r io.Reader) error {
		name := cleanArcName(it.Name)
		o, _ := owner(name)
		rel := name[strip[o]:]
		target, ok := safeJoin(dstDir, rel)
		if !ok {
			pr.Err(rel, os.ErrPermission)
			return nil
		}
		if st, err := os.Stat(target); err == nil {
			if st.IsDir() {
				pr.Err(target, os.ErrExist)
				return nil
			}
			ow, err := pr.AskOverwrite(target, fmt.Sprintf(T("Meglévő:  %s\nAz archívumban:  %s"), sizeAndTime(st.Size(), st.ModTime()), sizeAndTime(it.Size, it.Mod)))
			if err != nil {
				return err
			}
			if !ow {
				pr.Add(it.Size)
				return nil
			}
			os.Chmod(target, 0o666)
		}
		pr.Status(fmt.Sprintf(T("Kicsomagolás: %s"), rel))
		os.MkdirAll(filepath.Dir(target), 0o777)
		out, err := os.Create(target)
		if err != nil {
			pr.Err(target, err)
			return nil
		}
		_, err = io.Copy(progressWriter{out, pr}, r)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(target)
			if errors.Is(err, errCancel) {
				return err
			}
			pr.Err(target, err)
			return nil
		}
		if !it.Mod.IsZero() {
			os.Chtimes(target, it.Mod, it.Mod)
		}
		return nil
	}, pr)
}

func (a *arcFS) AddLocal(srcs []string, intoDir string, pr Progress) error {
	w, ok := a.be.(arcWriter)
	if !ok || !a.Writable() {
		return errReadOnly
	}
	var adds []arcAdd
	for _, src := range srcs {
		parent := filepath.Dir(src)
		filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				pr.Err(p, err)
				return nil
			}
			rel, rerr := filepath.Rel(parent, p)
			if rerr == nil {
				adds = append(adds, arcAdd{p, a.Join(intoDir, filepath.ToSlash(rel)), info})
			}
			return nil
		})
	}
	replace := map[string]bool{}
	var keep []arcAdd
	var total int64
	for _, ad := range adds {
		if ad.info.IsDir() {
			if a.dirs[ad.name] == nil {
				keep = append(keep, ad)
			}
			continue
		}
		if old, ok := a.files[ad.name]; ok {
			ow, err := pr.AskOverwrite(a.Display(ad.name), fmt.Sprintf(T("Az archívumban:  %s\nÚj:  %s"), sizeAndTime(old.Size, old.ModTime), sizeAndTime(ad.info.Size(), ad.info.ModTime())))
			if err != nil {
				return err
			}
			if !ow {
				continue
			}
			replace[ad.name] = true
		}
		keep = append(keep, ad)
		total += ad.info.Size()
	}
	pr.SetTotal(total)
	if err := w.add(keep, replace, pr); err != nil {
		return err
	}
	return a.load()
}

func (a *arcFS) Delete(items []*Entry, pr Progress) error {
	w, ok := a.be.(arcWriter)
	if !ok || !a.Writable() {
		return errReadOnly
	}
	pr.Status(T("Törlés az archívumból…"))
	err := w.remove(func(n string) bool {
		n = cleanArcName(n)
		for _, it := range items {
			if inside(n, it.Path) {
				return true
			}
		}
		return false
	}, pr)
	if err != nil {
		return err
	}
	return a.load()
}

func (a *arcFS) Mkdir(dir, name string) error {
	w, ok := a.be.(arcWriter)
	if !ok || !a.Writable() {
		return errReadOnly
	}
	tmp, err := os.MkdirTemp("", "duo")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	full := a.Join(dir, strings.Trim(strings.ReplaceAll(name, `\`, "/"), "/"))
	info, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	if err := w.add([]arcAdd{{tmp, full, info}}, nil, nullProgress{}); err != nil {
		return err
	}
	return a.load()
}

type nullProgress struct{}

func (nullProgress) Cancelled() bool                           { return false }
func (nullProgress) SetTotal(int64)                            {}
func (nullProgress) Status(string)                             {}
func (nullProgress) Add(int64)                                 {}
func (nullProgress) Err(string, error)                         {}
func (nullProgress) AskOverwrite(string, string) (bool, error) { return true, nil }

// ── TAR (tömörítetlen, GZ, BZ2, XZ) ──

type tarBackend struct {
	path string
	comp string // tar, tgz, tbz, txz
}

func decompressor(r io.Reader, comp string) (io.Reader, func(), error) {
	switch comp {
	case "tgz", "gz":
		g, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return g, func() { g.Close() }, nil
	case "tbz", "bz2":
		return bzip2.NewReader(r), func() {}, nil
	case "txz", "xz":
		x, err := xz.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return x, func() {}, nil
	}
	return r, func() {}, nil
}

func (t *tarBackend) open() (*tar.Reader, func(), error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, nil, err
	}
	r, closeDec, err := decompressor(f, t.comp)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return tar.NewReader(r), func() { closeDec(); f.Close() }, nil
}

func tarItem(h *tar.Header) arcItem {
	return arcItem{Name: h.Name, Size: h.Size, Mod: h.ModTime, IsDir: h.Typeflag == tar.TypeDir}
}

func (t *tarBackend) list() ([]arcItem, error) {
	tr, done, err := t.open()
	if err != nil {
		return nil, err
	}
	defer done()
	var out []arcItem
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		if h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeDir || h.Typeflag == tar.TypeRegA {
			out = append(out, tarItem(h))
		}
	}
}

func (t *tarBackend) extract(want func(string) bool, sink func(arcItem, io.Reader) error, pr Progress) error {
	tr, done, err := t.open()
	if err != nil {
		return err
	}
	defer done()
	for {
		if pr.Cancelled() {
			return errCancel
		}
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if (h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA) && want(h.Name) {
			if err := sink(tarItem(h), tr); err != nil {
				return err
			}
		}
	}
}

func compressor(w io.Writer, comp string) (io.WriteCloser, error) {
	switch comp {
	case "tgz":
		return gzip.NewWriterLevel(w, gzip.BestCompression)
	case "txz":
		return xz.NewWriter(w)
	case "tar":
		return nopWriteCloser{w}, nil
	}
	return nil, errReadOnly
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// rewrite új archívumot ír a régi (szűrt) tartalmával és az új elemekkel.
func (t *tarBackend) rewrite(skip func(string) bool, adds []arcAdd, pr Progress) error {
	if t.comp == "tbz" {
		return errors.New(T("a TAR.BZ2 csak olvasható (nincs beépített BZ2-tömörítő)"))
	}
	tmp := t.path + ".duotmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	fail := func(e error) error { out.Close(); os.Remove(tmp); return e }
	cw, err := compressor(out, t.comp)
	if err != nil {
		return fail(err)
	}
	tw := tar.NewWriter(cw)
	if _, err := os.Stat(t.path); err == nil {
		tr, done, err := t.open()
		if err != nil {
			return fail(err)
		}
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				done()
				return fail(err)
			}
			if skip != nil && skip(cleanArcName(h.Name)) {
				continue
			}
			if err := tw.WriteHeader(h); err != nil {
				done()
				return fail(err)
			}
			if _, err := io.Copy(tw, tr); err != nil {
				done()
				return fail(err)
			}
		}
		done()
	}
	for _, ad := range adds {
		if pr.Cancelled() {
			return fail(errCancel)
		}
		h, err := tar.FileInfoHeader(ad.info, "")
		if err != nil {
			return fail(err)
		}
		h.Name = ad.name
		h.Format = tar.FormatPAX // ékezetes nevek
		if ad.info.IsDir() {
			h.Name += "/"
			if err := tw.WriteHeader(h); err != nil {
				return fail(err)
			}
			continue
		}
		pr.Status(fmt.Sprintf(T("Tömörítés: %s"), ad.name))
		if err := tw.WriteHeader(h); err != nil {
			return fail(err)
		}
		f, err := os.Open(ad.local)
		if err != nil {
			return fail(err)
		}
		_, err = io.Copy(progressWriter{tw, pr}, f)
		f.Close()
		if err != nil {
			return fail(err)
		}
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := cw.Close(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, t.path)
}

func (t *tarBackend) add(adds []arcAdd, replace map[string]bool, pr Progress) error {
	return t.rewrite(func(n string) bool { return replace[n] }, adds, pr)
}

func (t *tarBackend) remove(match func(string) bool, pr Progress) error {
	return t.rewrite(match, nil, pr)
}

// ── egyetlen tömörített fájl (.gz, .bz2, .xz) ──

type singleBackend struct {
	path string
	comp string
}

func (s *singleBackend) innerName() string {
	b := filepath.Base(s.path)
	return strings.TrimSuffix(b, filepath.Ext(b))
}

func (s *singleBackend) list() ([]arcItem, error) {
	st, err := os.Stat(s.path)
	if err != nil {
		return nil, err
	}
	size := int64(0)
	if s.comp == "gz" { // a GZ utolsó 4 bájtja az eredeti méret (4 GB alatt pontos)
		if f, err := os.Open(s.path); err == nil {
			var b [4]byte
			if _, err := f.ReadAt(b[:], st.Size()-4); err == nil {
				size = int64(b[0]) | int64(b[1])<<8 | int64(b[2])<<16 | int64(b[3])<<24
			}
			f.Close()
		}
	}
	return []arcItem{{Name: s.innerName(), Size: size, Mod: st.ModTime()}}, nil
}

func (s *singleBackend) extract(want func(string) bool, sink func(arcItem, io.Reader) error, pr Progress) error {
	if !want(s.innerName()) {
		return nil
	}
	f, err := os.Open(s.path)
	if err != nil {
		return err
	}
	defer f.Close()
	r, done, err := decompressor(f, s.comp)
	if err != nil {
		return err
	}
	defer done()
	items, _ := s.list()
	return sink(items[0], r)
}

// ── 7Z (olvasás beépítve, írás a 7-Zippel) ──

type sevenBackend struct{ path string }

func (s *sevenBackend) exe() string { return sevenZipExe() }

// open: a megadott jelszóval, ha van; titkosított fejlécnél *needPasswordError.
func (s *sevenBackend) open() (*sevenzip.ReadCloser, error) {
	pw, has := archivePassword(s.path)
	var r *sevenzip.ReadCloser
	var err error
	if has {
		r, err = sevenzip.OpenReaderWithPassword(s.path, pw)
	} else {
		r, err = sevenzip.OpenReader(s.path)
	}
	if err != nil {
		if sevenHeaderEncrypted(s.path) {
			return nil, &needPasswordError{Path: s.path, Wrong: has}
		}
		return nil, explain7zErr(err)
	}
	return r, nil
}

func (s *sevenBackend) list() ([]arcItem, error) {
	r, err := s.open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []arcItem
	for _, f := range r.File {
		out = append(out, arcItem{Name: f.Name, Size: int64(f.UncompressedSize), Mod: f.Modified, IsDir: f.FileInfo().IsDir()})
	}
	return out, nil
}

func explain7zErr(err error) error {
	m := strings.ToLower(err.Error())
	if strings.Contains(m, "password") || strings.Contains(m, "aes") {
		return &needPasswordError{}
	}
	return err
}

func (s *sevenBackend) extract(want func(string) bool, sink func(arcItem, io.Reader) error, pr Progress) error {
	return withPassword(s.path, func() error { return s.extractOnce(want, sink, pr) })
}

func (s *sevenBackend) extractOnce(want func(string) bool, sink func(arcItem, io.Reader) error, pr Progress) error {
	r, err := s.open()
	if err != nil {
		return err
	}
	defer r.Close()
	// próbaolvasás: kell-e jelszó, jó-e a megadott (még mielőtt bármit kiírnánk)
	_, has := archivePassword(s.path)
	for _, f := range r.File {
		if f.FileInfo().IsDir() || !want(f.Name) || f.UncompressedSize == 0 {
			continue
		}
		rc, err := f.Open()
		if err == nil {
			_, err = io.Copy(io.Discard, io.LimitReader(rc, 256<<10))
			rc.Close()
		}
		if err != nil {
			m := strings.ToLower(err.Error())
			if strings.Contains(m, "password") || (has && err != io.EOF) {
				return &needPasswordError{Path: s.path, Wrong: has && !strings.Contains(m, "no password")}
			}
		}
		break
	}
	for _, f := range r.File {
		if pr.Cancelled() {
			return errCancel
		}
		if f.FileInfo().IsDir() || !want(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			pr.Err(f.Name, err)
			continue
		}
		err = sink(arcItem{Name: f.Name, Size: int64(f.UncompressedSize), Mod: f.Modified}, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *sevenBackend) add(adds []arcAdd, replace map[string]bool, pr Progress) error {
	return exeAdd(s.exe(), s.path, adds, pr)
}

func (s *sevenBackend) remove(match func(string) bool, pr Progress) error {
	items, err := s.list()
	if err != nil {
		return err
	}
	return exeRemove(s.exe(), s.path, items, match)
}

// ── RAR (csak olvasás) ──

type rarBackend struct{ path string }

func (rb *rarBackend) iterate(fn func(h *rardecode.FileHeader, r io.Reader) error) error {
	var opts []rardecode.Option
	pw, has := archivePassword(rb.path)
	if has {
		opts = append(opts, rardecode.Password(pw))
	}
	rc, err := rardecode.OpenReader(rb.path, opts...)
	if err != nil {
		return rb.explain(err, has)
	}
	defer rc.Close()
	for {
		h, err := rc.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return rb.explain(err, has)
		}
		if err := fn(h, rc); err != nil {
			return rb.explain(err, has)
		}
	}
}

func (rb *rarBackend) explain(err error, has bool) error {
	switch {
	case errors.Is(err, rardecode.ErrBadPassword):
		return &needPasswordError{Path: rb.path, Wrong: true}
	case errors.Is(err, rardecode.ErrArchiveEncrypted), errors.Is(err, rardecode.ErrArchivedFileEncrypted):
		return &needPasswordError{Path: rb.path, Wrong: has}
	case has && strings.Contains(strings.ToLower(err.Error()), "checksum"): // RAR4: rossz jelszó → hibás adat
		return &needPasswordError{Path: rb.path, Wrong: true}
	}
	return err
}

func (rb *rarBackend) list() ([]arcItem, error) {
	var out []arcItem
	err := rb.iterate(func(h *rardecode.FileHeader, _ io.Reader) error {
		out = append(out, arcItem{Name: h.Name, Size: h.UnPackedSize, Mod: h.ModificationTime, IsDir: h.IsDir})
		return nil
	})
	return out, err
}

func (rb *rarBackend) extract(want func(string) bool, sink func(arcItem, io.Reader) error, pr Progress) error {
	return withPassword(rb.path, func() error { return rb.extractOnce(want, sink, pr) })
}

func (rb *rarBackend) extractOnce(want func(string) bool, sink func(arcItem, io.Reader) error, pr Progress) error {
	return rb.iterate(func(h *rardecode.FileHeader, r io.Reader) error {
		if pr.Cancelled() {
			return errCancel
		}
		if h.IsDir || !want(h.Name) {
			return nil
		}
		return sink(arcItem{Name: h.Name, Size: h.UnPackedSize, Mod: h.ModificationTime}, r)
	})
}

// ── külső 7-Zip (CAB, ARJ, LZH, ISO …) ──

type exeBackend struct {
	exe  string
	path string
}

func (e *exeBackend) list() ([]arcItem, error) {
	out, err := run7z(e.exe, "", "l", "-slt", "-ba", "-sccUTF-8", e.path)
	if err != nil {
		return nil, err
	}
	var items []arcItem
	var cur *arcItem
	flush := func() {
		if cur != nil && cur.Name != "" {
			items = append(items, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if line == "" {
			flush()
			continue
		}
		k, v, ok := strings.Cut(line, " = ")
		if !ok {
			continue
		}
		if k == "Path" {
			flush()
			cur = &arcItem{Name: v}
			continue
		}
		if cur == nil {
			continue
		}
		switch k {
		case "Size":
			fmt.Sscan(v, &cur.Size)
		case "Modified":
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.SplitN(v, ".", 2)[0], time.Local); err == nil {
				cur.Mod = t
			}
		case "Folder":
			cur.IsDir = v == "+"
		case "Attributes":
			if strings.HasPrefix(v, "D") {
				cur.IsDir = true
			}
		}
	}
	flush()
	return items, nil
}

func (e *exeBackend) extract(want func(string) bool, sink func(arcItem, io.Reader) error, pr Progress) error {
	items, err := e.list()
	if err != nil {
		return err
	}
	var names []string
	byName := map[string]arcItem{}
	for _, it := range items {
		if !it.IsDir && want(it.Name) {
			names = append(names, it.Name)
			byName[it.Name] = it
		}
	}
	if len(names) == 0 {
		return nil
	}
	tmp, err := os.MkdirTemp("", "duo7z")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	list := filepath.Join(tmp, "lista.txt")
	os.WriteFile(list, []byte(strings.Join(names, "\r\n")), 0o666)
	outDir := filepath.Join(tmp, "x")
	pr.Status(T("Kicsomagolás a 7-Zippel…"))
	if _, err := run7z(e.exe, "", "x", "-y", "-scsUTF-8", "-o"+outDir, e.path, "@"+list); err != nil {
		return err
	}
	sort.Strings(names)
	for _, n := range names {
		if pr.Cancelled() {
			return errCancel
		}
		f, err := os.Open(filepath.Join(outDir, filepath.FromSlash(cleanArcName(n))))
		if err != nil {
			pr.Err(n, err)
			continue
		}
		err = sink(byName[n], f)
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// exeAdd a 7-Zippel ír archívumba: a fájlokat a belső szerkezet szerint egy
// ideiglenes mappába rendezi, majd onnan adja hozzá.
func exeAdd(exe, archive string, adds []arcAdd, pr Progress) error {
	if exe == "" {
		return errReadOnly
	}
	tmp, err := os.MkdirTemp("", "duo7z")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	tops := map[string]bool{}
	for _, ad := range adds {
		dst := filepath.Join(tmp, filepath.FromSlash(ad.name))
		tops[strings.SplitN(ad.name, "/", 2)[0]] = true
		if ad.info.IsDir() {
			os.MkdirAll(dst, 0o777)
			continue
		}
		os.MkdirAll(filepath.Dir(dst), 0o777)
		if err := copyFileProgress(ad.local, dst, pr); err != nil {
			return err
		}
	}
	pr.Status(T("Tömörítés a 7-Zippel…"))
	args := []string{"a", "-y", "-scsUTF-8", archive}
	if packPassword != "" { // jelszavas becsomagolás (csak a becsomagolás idejére beállítva)
		args = append(args, "-p"+packPassword)
		if packEncryptNames && strings.HasSuffix(strings.ToLower(archive), ".7z") {
			args = append(args, "-mhe=on")
		}
	}
	for t := range tops {
		args = append(args, t)
	}
	_, err = run7z(exe, tmp, args...)
	return err
}

func exeRemove(exe, archive string, items []arcItem, match func(string) bool) error {
	if exe == "" {
		return errReadOnly
	}
	var names []string
	for _, it := range items {
		if match(cleanArcName(it.Name)) {
			names = append(names, it.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	tmp, err := os.MkdirTemp("", "duo7z")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	list := filepath.Join(tmp, "lista.txt")
	os.WriteFile(list, []byte(strings.Join(names, "\r\n")), 0o666)
	_, err = run7z(exe, "", "d", "-y", "-scsUTF-8", archive, "@"+list)
	return err
}

func copyFileProgress(src, dst string, pr Progress) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(progressWriter{out, pr}, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// run7z: a 7z.exe futtatása ablak nélkül; a kimenetet adja vissza.
var run7z = func(exe, dir string, args ...string) (string, error) {
	return "", errors.New(T("a 7-Zip futtatása nem érhető el"))
}
