package main

// Frissítés a GitHub Releases oldalról: a legújabb kiadás lekérdezése, a csomag
// letöltése és ellenőrzése (SHA256SUMS.txt), majd a futó program cseréje.
// A felület (kérdések, folyamatjelző) a frissites_win.go / lx_update.go fájlban van.

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const updateRepo = "gozie-lab/GozieCmd"

var (
	updateAPI        = "https://api.github.com/repos/" + updateRepo + "/releases/latest"
	updatePage       = "https://github.com/" + updateRepo + "/releases/latest"
	updateHTTP       = &http.Client{Timeout: 60 * time.Second}
	restartAfterExit bool // a kilépés után az új programot indítjuk
)

func errNoUpdateAsset() error {
	return errors.New(T("a kiadásban nincs ehhez a rendszerhez való csomag"))
}

func errUpdateChecksum() error {
	return errors.New(T("a letöltött csomag ellenőrzőösszege nem egyezik – nem telepítem"))
}

type relAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type release struct {
	Tag    string     `json:"tag_name"`
	Name   string     `json:"name"`
	Body   string     `json:"body"`
	Page   string     `json:"html_url"`
	Assets []relAsset `json:"assets"`
}

func (r *release) version() string { return strings.TrimPrefix(strings.TrimPrefix(r.Tag, "v"), "V") }

// updatesAllowed: Flatpak alatt a frissítést a Flatpak végzi, nem a program.
func updatesAllowed() bool { return os.Getenv("FLATPAK_ID") == "" }

func httpGet(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", appDir+"/"+appVersion)
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		switch resp.StatusCode {
		case 404:
			return nil, errors.New(T("még nincs kiadott verzió"))
		case 403, 429:
			return nil, errors.New(T("a GitHub most nem válaszol (túl sok lekérdezés vagy hozzáférési korlát) – próbáld később"))
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// latestRelease: a legújabb (nem előzetes) kiadás adatai.
func latestRelease() (*release, error) {
	resp, err := httpGet(updateAPI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// versionNewer: a newer verziószám (pl. "0.45" vagy "1.2.3") nagyobb-e a cur-nál.
func versionNewer(newer, cur string) bool {
	a, b := strings.Split(newer, "."), strings.Split(cur, ".")
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x, _ = strconv.Atoi(strings.TrimSpace(a[i]))
		}
		if i < len(b) {
			y, _ = strconv.Atoi(strings.TrimSpace(b[i]))
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func assetSuffix(goos string) string {
	if goos == "windows" {
		return "-windows.zip"
	}
	return "-linux-x86_64.tar.gz"
}

func binaryName(goos string) string {
	if goos == "windows" {
		return "GozieCmd.exe"
	}
	return "goziecmd"
}

func (r *release) asset(name string) *relAsset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

func (r *release) packageAsset(goos string) *relAsset {
	for i := range r.Assets {
		if strings.HasSuffix(r.Assets[i].Name, assetSuffix(goos)) {
			return &r.Assets[i]
		}
	}
	return nil
}

var selfExe = sync.OnceValues(func() (string, error) {
	// a futó program valódi útvonala (induláskor rögzítve: Linuxon a csere után a
	// /proc/self/exe már a törölt régi fájlra mutat)
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return exe, nil
})

// canReplaceSelf: írható-e a program mappája (különben kézzel kell frissíteni).
func canReplaceSelf() error {
	exe, err := selfExe()
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(exe), ".gozie-update-*")
	if err != nil {
		return fmt.Errorf(T("a program mappája nem írható: %s"), filepath.Dir(exe))
	}
	f.Close()
	os.Remove(f.Name())
	return nil
}

type progressFn func(done, total int64)

type countWriter struct {
	w     io.Writer
	done  int64
	total int64
	prog  progressFn
	stop  func() bool
}

func (c *countWriter) Write(p []byte) (int, error) {
	if c.stop != nil && c.stop() {
		return 0, errCancel
	}
	n, err := c.w.Write(p)
	c.done += int64(n)
	if c.prog != nil {
		c.prog(c.done, c.total)
	}
	return n, err
}

// expectedSum: a SHA256SUMS.txt-ből a csomag ellenőrzőösszege.
func expectedSum(r *release, name string) (string, error) {
	a := r.asset("SHA256SUMS.txt")
	if a == nil {
		return "", errors.New(T("a kiadásból hiányzik az ellenőrzőösszeg-fájl"))
	}
	resp, err := httpGet(a.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", errors.New(T("az ellenőrzőösszeg-fájlban nincs adat a csomaghoz"))
}

// downloadUpdate letölti és ellenőrzi a csomagot, kibontja belőle a programot a
// futó program mellé (<exe>.new), és visszaadja ennek útvonalát.
func downloadUpdate(r *release, prog progressFn, stop func() bool) (string, error) {
	pkg := r.packageAsset(runtime.GOOS)
	if pkg == nil {
		return "", errNoUpdateAsset()
	}
	want, err := expectedSum(r, pkg.Name)
	if err != nil {
		return "", err
	}
	exe, err := selfExe()
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "gozie-update-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	resp, err := httpGet(pkg.URL)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	total := resp.ContentLength
	if total <= 0 {
		total = pkg.Size
	}
	cw := &countWriter{w: io.MultiWriter(tmp, h), total: total, prog: prog, stop: stop}
	_, err = io.Copy(cw, resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return "", errUpdateChecksum()
	}
	out := exe + ".new"
	if err := extractBinary(tmp, binaryName(runtime.GOOS), out, strings.HasSuffix(pkg.Name, ".zip")); err != nil {
		os.Remove(out)
		return "", err
	}
	return out, nil
}

// extractBinary: a csomagból a megadott nevű fájl kiírása az out útvonalra.
func extractBinary(f *os.File, name, out string, isZip bool) error {
	write := func(r io.Reader) error {
		o, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(o, r); err != nil {
			o.Close()
			return err
		}
		return o.Close()
	}
	if isZip {
		st, err := f.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			return err
		}
		for _, zf := range zr.File {
			if filepath.Base(zf.Name) == name && !zf.FileInfo().IsDir() {
				rc, err := zf.Open()
				if err != nil {
					return err
				}
				defer rc.Close()
				return write(rc)
			}
		}
		return errNoUpdateAsset()
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return errNoUpdateAsset()
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == name {
			return write(tr)
		}
	}
}

// installUpdate a letöltött programot a futó helyére teszi. Windowson a futó
// .exe nem írható felül, de átnevezhető: <exe>.old lesz, a következő indításkor törlődik.
func installUpdate(newPath string) error {
	exe, err := selfExe()
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return err
		}
		if err := os.Rename(newPath, exe); err != nil {
			os.Rename(old, exe) // visszaállítás
			return err
		}
		return nil
	}
	os.Chmod(newPath, 0o755)
	return os.Rename(newPath, exe) // Linuxon a csere atomi, a futó program zavartalan
}

// cleanupOldUpdate: az előző frissítés maradékai (a régi program még futhat pár
// másodpercig, ezért néhányszor újrapróbáljuk).
func cleanupOldUpdate() {
	exe, err := selfExe()
	if err != nil {
		return
	}
	os.Remove(exe + ".new")
	old := exe + ".old"
	if _, err := os.Stat(old); err != nil {
		return
	}
	go func() {
		for i := 0; i < 10; i++ {
			if os.Remove(old) == nil {
				return
			}
			time.Sleep(time.Second)
		}
	}()
}

// startSelf: a (frissített) program indítása a kilépés után.
func startSelf() {
	exe, err := selfExe()
	if err != nil {
		return
	}
	c := exec.Command(exe)
	c.Dir, _ = os.Getwd()
	if err := c.Start(); err != nil {
		dlog("újraindítás: %v", err)
		return
	}
	c.Process.Release()
}

// notesURL: a kiadás oldala a GitHubon, ahol a változások teljes listája olvasható.
func (r *release) notesURL() string {
	if strings.HasPrefix(r.Page, "https://github.com/") {
		return r.Page
	}
	return "https://github.com/" + updateRepo + "/releases/tag/" + r.Tag
}

// autoCheckDue: az automatikus keresés be van-e kapcsolva, és eltelt-e egy nap.
func autoCheckDue(get func(string) (string, bool)) bool {
	if !updatesAllowed() {
		return false
	}
	if v, ok := get("updateCheck"); ok && v == "0" {
		return false
	}
	if v, ok := get("updateLast"); ok {
		if t, err := strconv.ParseInt(v, 10, 64); err == nil && time.Since(time.Unix(t, 0)) < 20*time.Hour {
			return false
		}
	}
	return true
}

func (a *App) updateCheckOn() bool {
	v, ok := a.settings.Get("updateCheck")
	return !ok || v != "0"
}

func updateAvailableText(r *release) string {
	return fmt.Sprintf(T("Elérhető az új verzió: %s (most: %s).\n\nLetöltsem és telepítsem? A program utána újraindul.\nA változások listája az „Újdonságok” gombbal a GitHubon olvasható."), r.version(), appVersion)
}
