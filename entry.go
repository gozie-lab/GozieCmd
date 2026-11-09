package main

// Fájlbejegyzés és platformfüggetlen segédfüggvények.

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

type Entry struct {
	Name     string
	Path     string
	Ext      string
	IsDir    bool
	IsParent bool
	Size     int64
	ModTime  time.Time
	Attr     uint32
}

func newEntry(name, path string, isDir bool, size int64, mod time.Time) *Entry {
	e := &Entry{Name: name, Path: path, IsDir: isDir, ModTime: mod}
	if !isDir {
		e.Size = size
		if ext := filepath.Ext(name); len(ext) > 1 && len(ext) < len(name) {
			e.Ext = ext[1:]
		}
	}
	return e
}

func (e *Entry) Stem() string {
	if e.Ext == "" {
		return e.Name
	}
	return e.Name[:len(e.Name)-len(e.Ext)-1]
}

var errCancel = errors.New(T("megszakítva"))

// Progress a hosszú műveletek visszajelzése (a Job valósítja meg).
type Progress interface {
	Cancelled() bool
	SetTotal(n int64)
	Status(text string)
	Add(n int64)
	Err(name string, err error)
	AskOverwrite(name, detail string) (bool, error)
}

type progressWriter struct {
	w  io.Writer
	pr Progress
}

func (pw progressWriter) Write(b []byte) (int, error) {
	if pw.pr.Cancelled() {
		return 0, errCancel
	}
	n, err := pw.w.Write(b)
	pw.pr.Add(int64(n))
	return n, err
}

type progressReader struct {
	r  io.Reader
	pr Progress
}

func (pr progressReader) Read(b []byte) (int, error) {
	if pr.pr.Cancelled() {
		return 0, errCancel
	}
	n, err := pr.r.Read(b)
	pr.pr.Add(int64(n))
	return n, err
}

func fmtSize(n int64) string {
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ' ')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func fmtHuman(n uint64) string {
	f := float64(n)
	for _, u := range []string{"B", "KB", "MB", "GB", "TB"} {
		if f < 1024 || u == "TB" {
			if u == "B" {
				return fmt.Sprintf("%d B", n)
			}
			return strings.Replace(fmt.Sprintf("%.1f %s", f, u), ".", ",", 1)
		}
		f /= 1024
	}
	return ""
}

func sizeAndTime(size int64, t time.Time) string {
	return fmt.Sprintf(T("%s bájt, %s"), fmtSize(size), t.Format("2006.01.02 15:04:05"))
}
