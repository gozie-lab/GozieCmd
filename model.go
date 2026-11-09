//go:build windows

package main

// A fájllista adatmodellje.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/lxn/walk"
)

func readEntries(dir string, showHidden bool) ([]*Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil && len(des) == 0 {
		return nil, err
	}
	out := make([]*Entry, 0, len(des))
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			continue
		}
		var attr uint32
		if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
			attr = d.FileAttributes
		}
		if !showHidden && attr&fileAttrHidden != 0 {
			continue
		}
		name := de.Name()
		isDir := attr&fileAttrDirectory != 0 || info.IsDir()
		e := newEntry(name, filepath.Join(dir, name), isDir, info.Size(), info.ModTime())
		e.Attr = attr
		out = append(out, e)
	}
	return out, nil
}

type FileModel struct {
	walk.TableModelBase
	walk.SorterBase

	items     []*Entry
	virtual   bool // ZIP / FTP tartalom: az ikonokhoz mintafájl kell
	marked    map[string]bool
	dirSizes  map[string]int64 // -1: számítás folyamatban
	sortCol   int
	sortOrder walk.SortOrder

	beforeSort func()
	afterSort  func()
	afterReset func()
}

func NewFileModel() *FileModel {
	return &FileModel{marked: map[string]bool{}, dirSizes: map[string]int64{}}
}

func (m *FileModel) RowCount() int { return len(m.items) }

func (m *FileModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.items) {
		return ""
	}
	e := m.items[row]
	switch col {
	case 0:
		if e.IsParent {
			return "[..]"
		}
		if e.IsDir {
			return "[" + e.Name + "]"
		}
		return e.Stem()
	case 1:
		return e.Ext
	case 2:
		if e.IsParent {
			return ""
		}
		if e.IsDir {
			s, ok := m.dirSizes[e.Name]
			switch {
			case !ok:
				return "<DIR>"
			case s < 0:
				return T("…")
			default:
				return fmtSize(s)
			}
		}
		return fmtSize(e.Size)
	case 3:
		if e.IsParent {
			return ""
		}
		return e.ModTime.Format("2006.01.02 15:04")
	case 4:
		if e.IsParent {
			return ""
		}
		return attrString(e.Attr)
	}
	return ""
}

// Image: a fájl útvonala, ebből a TableView a rendszer ikonját veszi.
func (m *FileModel) Image(row int) interface{} {
	if row < 0 || row >= len(m.items) {
		return nil
	}
	e := m.items[row]
	if m.virtual || !ownIcon(e) {
		// A rendszer ikonja kiterjesztésenként egyszer: fájlonkénti lekérdezés
		// (SHGetFileInfo a valódi fájlon) nagy, hálózati vagy felhős mappában
		// másodpercekre megakaszthatta a felületet.
		return iconStub(e)
	}
	return e.Path
}

// ownIcon: a fájlnak saját ikonja lehet (program, parancsikon, ikonfájl), vagy a
// mappának egyéni ikonja (a desktop.ini csak csak olvasható / rendszer mappán hat).
func ownIcon(e *Entry) bool {
	if e.IsParent {
		return false
	}
	if e.IsDir {
		return e.Attr&(fileAttrReadonly|fileAttrSystem) != 0
	}
	switch strings.ToLower(e.Ext) {
	case "exe", "lnk", "ico", "url", "cur", "ani", "scr", "msc", "cpl", "appref-ms", "website":
		return true
	}
	return false
}

func (m *FileModel) Sort(col int, order walk.SortOrder) error {
	if m.beforeSort != nil {
		m.beforeSort()
	}
	m.sortCol, m.sortOrder = col, order
	m.sortItems()
	err := m.SorterBase.Sort(col, order)
	if m.afterSort != nil {
		m.afterSort()
	}
	return err
}

func (m *FileModel) sortItems() {
	var parent, dirs, files []*Entry
	for _, e := range m.items {
		switch {
		case e.IsParent:
			parent = append(parent, e)
		case e.IsDir:
			dirs = append(dirs, e)
		default:
			files = append(files, e)
		}
	}
	desc := m.sortOrder == walk.SortDescending
	name := func(e *Entry) string { return strings.ToLower(e.Name) }
	less := func(a, b *Entry, isDir bool) bool {
		switch m.sortCol {
		case 1:
			if !isDir && !strings.EqualFold(a.Ext, b.Ext) {
				return strings.ToLower(a.Ext) < strings.ToLower(b.Ext)
			}
		case 2:
			sa, sb := a.Size, b.Size
			if isDir {
				sa, sb = m.dirSize(a), m.dirSize(b)
			}
			if sa != sb {
				return sa < sb
			}
		case 3:
			if !a.ModTime.Equal(b.ModTime) {
				return a.ModTime.Before(b.ModTime)
			}
		}
		return name(a) < name(b)
	}
	sortGroup := func(g []*Entry, isDir bool) {
		sort.SliceStable(g, func(i, j int) bool {
			if desc && !(isDir && m.sortCol == 1) {
				return less(g[j], g[i], isDir)
			}
			return less(g[i], g[j], isDir)
		})
	}
	sortGroup(dirs, true)
	sortGroup(files, false)
	m.items = append(append(parent, dirs...), files...)
}

func (m *FileModel) dirSize(e *Entry) int64 {
	if s, ok := m.dirSizes[e.Name]; ok {
		return s
	}
	return -1
}

// setItems új tartalmat tölt be; a jelöléseket a megmaradt nevekre szűkíti.
func (m *FileModel) setItems(entries []*Entry, parentPath string) {
	m.items = nil
	if parentPath != "" {
		m.items = append(m.items, &Entry{Name: "..", Path: parentPath, IsDir: true, IsParent: true})
	}
	m.items = append(m.items, entries...)
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	for n := range m.marked {
		if !names[n] {
			delete(m.marked, n)
		}
	}
	for n := range m.dirSizes {
		if !names[n] {
			delete(m.dirSizes, n)
		}
	}
	m.sortItems()
	m.PublishRowsReset()
	// A lista csak az elemszámot frissíti, a sorokat nem rajzolja újra – ezt kérjük ki.
	if m.afterReset != nil {
		m.afterReset()
	}
}
