package main

// Fájlkeresés: névminta és tartalomkeresés (közös a Windows- és a Linux-változatban).

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf16"
)

type searchSpec struct {
	root      string
	patterns  []string
	text      string
	matchCase bool
	subdirs   bool
	dirsToo   bool
	minSize   int64
	maxSize   int64
	after     time.Time
}

func matchName(name string, pats []string, matchCase bool) bool {
	if !matchCase {
		name = strings.ToLower(name)
	}
	for _, p := range pats {
		if !matchCase {
			p = strings.ToLower(p)
		}
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return b
}

// fileContains darabonként olvas; UTF-8 és UTF-16 szöveget is megtalál.
func fileContains(path string, needles [][]byte, matchCase bool, stop *atomic.Bool) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	overlap := 0
	for _, n := range needles {
		if len(n) > overlap {
			overlap = len(n)
		}
	}
	buf := make([]byte, 1<<20)
	var prev []byte
	for !stop.Load() {
		n, err := f.Read(buf)
		if n > 0 {
			chunk := append(prev, buf[:n]...)
			hay := chunk
			if !matchCase {
				hay = bytes.ToLower(chunk)
			}
			for _, nd := range needles {
				if bytes.Contains(hay, nd) {
					return true
				}
			}
			if len(chunk) > overlap {
				prev = append([]byte(nil), chunk[len(chunk)-overlap:]...)
			} else {
				prev = chunk
			}
		}
		if err == io.EOF || err != nil {
			return false
		}
	}
	return false
}
