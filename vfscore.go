package main

// Platformfüggetlen segédek a virtuális mappákhoz (archívum, FTP) és az FTP-helyekhez.

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

func entryPaths(items []*Entry) []string {
	out := make([]string, len(items))
	for i, e := range items {
		out[i] = e.Path
	}
	return out
}

func removeLocal(paths []string, pr Progress) {
	pr.SetTotal(int64(len(paths)))
	for _, p := range paths {
		pr.Status(fmt.Sprintf(T("Forrás törlése: %s"), p))
		if err := os.RemoveAll(p); err != nil {
			pr.Err(p, err)
		}
		pr.Add(1)
	}
}

// exportItems a virtuális mappa (archívum, FTP) elemeit egy helyi mappába teszi.
func exportItems(v VFS, items []*Entry, dir string, pr Progress) error {
	switch x := v.(type) {
	case Archive:
		return x.Extract(items, dir, pr)
	case remoteVFS:
		return x.Download(items, dir, pr)
	}
	return errors.New(T("ebből a nézetből nem lehet másolni"))
}

// importPaths helyi fájlokat ír a virtuális mappába (archívumba tömörít, FTP-re feltölt).
func importPaths(v VFS, paths []string, intoDir string, pr Progress) error {
	switch x := v.(type) {
	case Archive:
		if !x.Writable() {
			return errors.New(fmt.Sprintf(T("%s: ebbe az archívumba nem lehet írni.%s"), x.Info(), sevenZipHint(x)))
		}
		return x.AddLocal(paths, intoDir, pr)
	case remoteVFS:
		return x.Upload(paths, intoDir, pr)
	}
	return errors.New(T("ide nem lehet másolni"))
}

func sevenZipHint(x Archive) string {
	if x.Format() == "7z" && sevenZipExe() == "" {
		if runtime.GOOS == "windows" {
			return T("\nA 7Z íráshoz telepítsd a 7-Zip programot (7-zip.org).")
		}
		return T("\nA 7Z íráshoz telepítsd a 7-Zip programot (7zip vagy p7zip csomag).")
	}
	return ""
}

// deleteItems törli az elemeket a virtuális mappából.
func deleteItems(v VFS, items []*Entry, pr Progress) error {
	switch x := v.(type) {
	case Archive:
		if !x.Writable() {
			return errors.New(fmt.Sprintf(T("%s: ebből az archívumból nem lehet törölni.%s"), x.Info(), sevenZipHint(x)))
		}
		return x.Delete(items, pr)
	case remoteVFS:
		return x.Delete(items, pr)
	}
	return errors.New(T("innen nem lehet törölni"))
}

func canDelete(v VFS) bool {
	if x, ok := v.(Archive); ok {
		return x.Writable()
	}
	_, ok := v.(remoteVFS)
	return ok
}

var packFormats = []struct{ label, ext string }{
	{T("ZIP (beépített)"), ".zip"},
	{T("7Z (7-Zippel)"), ".7z"},
	{"TAR", ".tar"},
	{"TAR.GZ", ".tar.gz"},
	{"TAR.XZ", ".tar.xz"},
}

func stripArchiveExt(p string) string {
	l := strings.ToLower(p)
	for _, f := range packFormats {
		if strings.HasSuffix(l, f.ext) {
			return p[:len(p)-len(f.ext)]
		}
	}
	return p
}

type ftpSite struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Dir      string `json:"dir"`
	TLS      bool   `json:"tls"`
	Insecure bool   `json:"insecure"`
	Proto    string `json:"proto,omitempty"` // üres: ftp
	KeyFile  string `json:"key,omitempty"`
}

func siteLabel(s ftpSite) string {
	u := ""
	if s.User != "" {
		u = s.User + "@"
	}
	return fmt.Sprintf(T("%s   —   %s   %s%s:%s"), s.Name, protoLabel(s.Proto, s.TLS), u, s.Host, strconv.Itoa(s.Port))
}
