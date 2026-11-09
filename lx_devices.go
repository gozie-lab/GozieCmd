//go:build linux

package main

// Cserélhető eszközök: a még nem csatolt pendrive-ok és lemezek is megjelennek a
// meghajtólistában, kiválasztva a rendszer (udisks) csatolja őket. A változást
// (bedugás, kihúzás, csatolás) a percenkénti lekérdezés helyett 1,5 mp-enként,
// olcsón vesszük észre: a /proc/self/mounts és a /dev/disk/by-uuid tartalmából.

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type blockDev struct {
	dev, label string
	size       int64
}

// deviceStamp: ha változik, újra kell építeni a meghajtólistát.
func deviceStamp() string {
	h := sha1.New()
	if b, err := os.ReadFile("/proc/self/mounts"); err == nil {
		h.Write(b)
	}
	if es, err := os.ReadDir("/dev/disk/by-uuid"); err == nil {
		for _, e := range es {
			h.Write([]byte(e.Name()))
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// unmountedRemovable: cserélhető (USB, kártyaolvasó) eszközök fájlrendszerrel, amelyek nincsenek csatolva.
func unmountedRemovable() []blockDev {
	mounted := map[string]bool{}
	if b, err := os.ReadFile("/proc/self/mounts"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Fields(l); len(f) > 1 {
				if r, err := filepath.EvalSymlinks(f[0]); err == nil {
					mounted[r] = true
				}
				mounted[f[0]] = true
			}
		}
	}
	labels := map[string]string{}
	if es, err := os.ReadDir("/dev/disk/by-label"); err == nil {
		for _, e := range es {
			if r, err := filepath.EvalSymlinks(filepath.Join("/dev/disk/by-label", e.Name())); err == nil {
				labels[r] = unescapeMount(strings.ReplaceAll(e.Name(), `\x20`, " "))
			}
		}
	}
	var out []blockDev
	es, err := os.ReadDir("/dev/disk/by-uuid") // csak a fájlrendszert hordozó eszközök
	if err != nil {
		return nil
	}
	for _, e := range es {
		dev, err := filepath.EvalSymlinks(filepath.Join("/dev/disk/by-uuid", e.Name()))
		if err != nil || mounted[dev] {
			continue
		}
		name := filepath.Base(dev)
		disk := parentDisk(name)
		if disk == "" || !removableDisk(disk) {
			continue
		}
		bd := blockDev{dev: dev, label: labels[dev]}
		if b, err := os.ReadFile(sysBlockPath(name) + "/size"); err == nil {
			fmt.Sscan(strings.TrimSpace(string(b)), &bd.size)
			bd.size *= 512
		}
		if bd.size == 0 {
			continue // üres kártyaolvasó-hely
		}
		out = append(out, bd)
	}
	return out
}

// parentDisk: sdb1 → sdb, mmcblk0p1 → mmcblk0 (a /sys/class/block alapján).
func parentDisk(name string) string {
	p, err := filepath.EvalSymlinks("/sys/class/block/" + name)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(p + "/partition"); err == nil {
		return filepath.Base(filepath.Dir(p))
	}
	return name // partíció nélküli eszköz (pl. „superfloppy” pendrive)
}

func sysBlockPath(name string) string {
	p, _ := filepath.EvalSymlinks("/sys/class/block/" + name)
	return p
}

func removableDisk(disk string) bool {
	if b, err := os.ReadFile("/sys/block/" + disk + "/removable"); err == nil && strings.TrimSpace(string(b)) == "1" {
		return true
	}
	p, err := filepath.EvalSymlinks("/sys/block/" + disk)
	return err == nil && strings.Contains(p, "/usb")
}

func (d blockDev) display() string {
	name := d.label
	if name == "" {
		name = filepath.Base(d.dev)
	}
	return fmt.Sprintf(T("%s  (%s, nincs csatolva)"), name, fmtSize(d.size))
}

var mountedAt = regexp.MustCompile(` at (.+?)\.?$`)

// mountDevice a rendszerrel (udisks) csatolja az eszközt, és visszaadja a csatolási pontot.
func mountDevice(dev string) (string, error) {
	out, err := exec.Command("udisksctl", "mount", "-b", dev).CombinedOutput()
	if err != nil {
		if _, e := exec.LookPath("udisksctl"); e != nil {
			return "", errors.New(T("a csatoláshoz a udisksctl program kell (udisks2 csomag)"))
		}
		return "", fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	if m := mountedAt.FindStringSubmatch(strings.TrimSpace(string(out))); m != nil {
		return m[1], nil
	}
	// régebbi kiírás: keressük meg a csatolások között
	for _, l := range strings.Split(readFileStr("/proc/self/mounts"), "\n") {
		if f := strings.Fields(l); len(f) > 1 && f[0] == dev {
			return unescapeMount(f[1]), nil
		}
	}
	return "", errors.New(T("csatolva, de nem találom a helyét"))
}

func readFileStr(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}
