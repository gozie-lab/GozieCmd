package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"0.45", "0.44", true}, {"0.44", "0.44", false}, {"0.5", "0.44", false}, {"1.0", "0.99", true}, {"0.44.1", "0.44", true}, {"0.43", "0.44", false}}
	for _, c := range cases {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q,%q)=%v", c.a, c.b, got)
		}
	}
	if (&release{Tag: "v0.45"}).version() != "0.45" {
		t.Error("tag")
	}
}

func makePkg(t *testing.T, goos string, content []byte) []byte {
	var buf bytes.Buffer
	name := "GozieCmd/" + binaryName(goos)
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		zw.Create("GozieCmd/OLVASS.txt")
		w, _ := zw.Create(name)
		w.Write(content)
		zw.Close()
	} else {
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: "GozieCmd/", Typeflag: tar.TypeDir, Mode: 0o755})
		tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))})
		tw.Write(content)
		tw.Close()
		gz.Close()
	}
	return buf.Bytes()
}

func TestDownloadAndInstall(t *testing.T) {
	newBin := []byte("uj program tartalma")
	pkgName := "GozieCmd-9.0" + assetSuffix(runtime.GOOS)
	pkg := makePkg(t, runtime.GOOS, newBin)
	sum := sha256.Sum256(pkg)
	good := hex.EncodeToString(sum[:])
	sums := good + "  " + pkgName + "\n"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			json.NewEncoder(w).Encode(release{Tag: "v9.0", Body: "újdonság", Assets: []relAsset{
				{Name: pkgName, URL: srv.URL + "/pkg", Size: int64(len(pkg))},
				{Name: "SHA256SUMS.txt", URL: srv.URL + "/sums"},
			}})
		case "/pkg":
			w.Write(pkg)
		case "/sums":
			fmt.Fprint(w, sums)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldAPI, oldExe := updateAPI, selfExe
	defer func() { updateAPI, selfExe = oldAPI, oldExe }()
	dir := t.TempDir()
	exe := filepath.Join(dir, binaryName(runtime.GOOS))
	os.WriteFile(exe, []byte("regi"), 0o755)
	selfExe = func() (string, error) { return exe, nil }
	updateAPI = srv.URL + "/latest"

	r, err := latestRelease()
	if err != nil || r.version() != "9.0" || !versionNewer(r.version(), appVersion) {
		t.Fatalf("release: %v %+v", err, r)
	}
	if err := canReplaceSelf(); err != nil {
		t.Fatal(err)
	}
	var seen int64
	p, err := downloadUpdate(r, func(d, total int64) { seen = d }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if seen != int64(len(pkg)) {
		t.Errorf("progress %d", seen)
	}
	if err := installUpdate(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, newBin) {
		t.Errorf("tartalom: %q", b)
	}

	// rossz ellenőrzőösszeg: nem telepít
	sums = "00" + good[2:] + "  " + pkgName + "\n"
	if _, err := downloadUpdate(r, nil, nil); err == nil || err.Error() != errUpdateChecksum().Error() {
		t.Errorf("checksum hiba várt, kapott: %v", err)
	}
	// hiányzó ellenőrzőösszeg-fájl: nem telepít
	r.Assets = r.Assets[:1]
	if _, err := downloadUpdate(r, nil, nil); err == nil {
		t.Error("SHA256SUMS nélkül nem szabad telepíteni")
	}
}

func TestExtractZip(t *testing.T) {
	pkg := makePkg(t, "windows", []byte("exe"))
	f, _ := os.CreateTemp(t.TempDir(), "p")
	f.Write(pkg)
	out := filepath.Join(t.TempDir(), "o")
	if err := extractBinary(f, "GozieCmd.exe", out, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "exe" {
		t.Error(string(b))
	}
}

func TestAutoCheckDue(t *testing.T) {
	m := map[string]string{}
	get := func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	if !autoCheckDue(get) {
		t.Error("alapból esedékes")
	}
	m["updateCheck"] = "0"
	if autoCheckDue(get) {
		t.Error("kikapcsolva")
	}
}
