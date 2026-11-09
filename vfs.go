package main

// Virtuális fájlrendszerek (ZIP-archívum, FTP) közös felülete.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// VFS egy panelen megnyitható nem helyi „mappa”.
// Az Entry.Path mezője ilyenkor a belső útvonal ("/"-rel tagolva).
type VFS interface {
	Kind() string
	Root() string
	List(dir string) ([]*Entry, error)
	Parent(dir string) (string, bool)
	Join(dir, name string) string
	Display(dir string) string
	Info() string
	Close()
}

// ── ikonok virtuális elemekhez ──
// A rendszer ikonját csak létező fájlhoz adja meg, ezért kiterjesztésenként
// egy üres mintafájlt használunk a Temp mappában.

var (
	iconMu   sync.Mutex
	iconDir  string
	iconMade = map[string]string{}
)

func iconStub(e *Entry) string {
	iconMu.Lock()
	defer iconMu.Unlock()
	if iconDir == "" {
		iconDir = filepath.Join(os.TempDir(), appDir, "icons")
		os.MkdirAll(iconDir, 0o777)
	}
	if e.IsDir {
		return iconDir
	}
	ext := strings.ToLower(e.Ext)
	if strings.ContainsAny(ext, `\/:*?"<>|`) || len(ext) > 16 {
		ext = ""
	}
	if p, ok := iconMade[ext]; ok {
		return p
	}
	p := filepath.Join(iconDir, "minta")
	if ext != "" {
		p += "." + ext
	}
	if _, err := os.Stat(p); err != nil {
		if f, err := os.Create(p); err == nil {
			f.Close()
		}
	}
	iconMade[ext] = p
	return p
}

// tempViewDir egy új, egyedi ideiglenes mappát ad megtekintéshez.
func tempViewDir() (string, error) {
	base := filepath.Join(os.TempDir(), appDir, "view")
	os.MkdirAll(base, 0o777)
	return os.MkdirTemp(base, "f")
}

func cleanupTemp() {
	os.RemoveAll(filepath.Join(os.TempDir(), appDir, "view"))
}

// safeJoin a célmappán belül tartja a kicsomagolt útvonalat (zip slip ellen).
func safeJoin(base, rel string) (string, bool) {
	rel = filepath.FromSlash(rel)
	p := filepath.Join(base, rel)
	b := filepath.Clean(base)
	fold := func(x string) string { // Windowson a kis- és nagybetű nem számít, Linuxon igen
		if runtime.GOOS == "windows" {
			return strings.ToLower(x)
		}
		return x
	}
	if fold(p) != fold(b) && !strings.HasPrefix(fold(p), fold(b)+string(os.PathSeparator)) {
		return "", false
	}
	return p, true
}

// ── diagnosztikai napló (húzd és ejtsd, hálózat) ──

var dlogMu sync.Mutex

func dlogPath() string { return filepath.Join(os.TempDir(), appDir, "naplo.txt") }

func dlog(format string, args ...interface{}) {
	dlogMu.Lock()
	defer dlogMu.Unlock()
	os.MkdirAll(filepath.Dir(dlogPath()), 0o777)
	f, err := os.OpenFile(dlogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("15:04:05.000 ")+format+"\r\n", args...)
}

// dlogReset új naplót kezd; az előző futásé naplo-elozo.txt néven megmarad
// (egy lefagyás után kilőtt program naplója így nem vész el az újraindítással).
func dlogReset() {
	os.MkdirAll(filepath.Dir(dlogPath()), 0o777)
	prev := filepath.Join(filepath.Dir(dlogPath()), "naplo-elozo.txt")
	os.Remove(prev)
	os.Rename(dlogPath(), prev)
	os.WriteFile(dlogPath(), nil, 0o666)
}
