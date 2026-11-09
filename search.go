//go:build windows

package main

// Fájlkeresés (Alt+F7): név (maszkok), tartalom, méret és dátum szerint, háttérben.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

type hit struct {
	path  string
	size  int64
	mod   time.Time
	isDir bool
}

type hitModel struct {
	walk.TableModelBase
	hits []hit
}

func (m *hitModel) RowCount() int { return len(m.hits) }

func (m *hitModel) Value(row, col int) interface{} {
	if row >= len(m.hits) {
		return ""
	}
	h := m.hits[row]
	switch col {
	case 0:
		return filepath.Base(h.path)
	case 1:
		return filepath.Dir(h.path)
	case 2:
		if h.isDir {
			return "<DIR>"
		}
		return fmtSize(h.size)
	case 3:
		return h.mod.Format("2006.01.02 15:04")
	}
	return ""
}

func (m *hitModel) Image(row int) interface{} {
	if row < len(m.hits) {
		return m.hits[row].path
	}
	return nil
}

func (a *App) findFiles() {
	start := a.active.path
	model := &hitModel{}
	var mu sync.Mutex
	var pending []hit
	var stop atomic.Bool
	var running atomic.Bool
	var closed atomic.Bool // bezárt ablakba már ne írjunk
	var gen int64

	var dlg *walk.Dialog
	var whereLE, nameLE, textLE, minLE, maxLE, daysLE *walk.LineEdit
	var caseCB, subCB, dirCB *walk.CheckBox
	var tv *walk.TableView
	var status *walk.Label
	var startBtn, stopBtn, gotoBtn, closeBtn *walk.PushButton

	pat, _ := a.settings.Get("findPattern")
	if pat == "" {
		pat = "*"
	}

	gotoHit := func() {
		i := tv.CurrentIndex()
		if i < 0 || i >= len(model.hits) {
			return
		}
		h := model.hits[i]
		stop.Store(true)
		closed.Store(true)
		dlg.Accept()
		p := a.active
		p.navigate(filepath.Dir(h.path), filepath.Base(h.path), true, false)
		a.mw.Activate()
		p.tv.SetFocus()
	}

	begin := func() {
		if running.Load() {
			return
		}
		root := strings.Trim(strings.TrimSpace(whereLE.Text()), `"`)
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			status.SetText(T("A keresés helye nem létező mappa."))
			return
		}
		var pats []string
		for _, p := range strings.FieldsFunc(nameLE.Text(), func(r rune) bool { return r == ';' || r == ',' }) {
			if p = strings.TrimSpace(p); p != "" {
				if p == "*.*" {
					p = "*"
				}
				if !strings.ContainsAny(p, "*?") {
					p = "*" + p + "*" // részlet is elég
				}
				pats = append(pats, p)
			}
		}
		if len(pats) == 0 {
			pats = []string{"*"}
		}
		a.settings.Put("findPattern", nameLE.Text())
		spec := searchSpec{root: root, patterns: pats, text: textLE.Text(), matchCase: caseCB.Checked(),
			subdirs: subCB.Checked(), dirsToo: dirCB.Checked(), minSize: -1, maxSize: -1}
		if v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(minLE.Text()), ",", "."), 64); err == nil {
			spec.minSize = int64(v * 1024)
		}
		if v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(maxLE.Text()), ",", "."), 64); err == nil {
			spec.maxSize = int64(v * 1024)
		}
		if v, err := strconv.Atoi(strings.TrimSpace(daysLE.Text())); err == nil && v > 0 {
			spec.after = time.Now().AddDate(0, 0, -v)
		}
		var needles [][]byte
		if spec.text != "" {
			t := spec.text
			if !spec.matchCase {
				t = strings.ToLower(t)
			}
			needles = [][]byte{[]byte(t), utf16le(t)}
		}

		model.hits = nil
		model.PublishRowsReset()
		tv.Invalidate()
		stop.Store(false)
		running.Store(true)
		g := atomic.AddInt64(&gen, 1)
		startBtn.SetEnabled(false)
		stopBtn.SetEnabled(true)
		status.SetText(T("Keresés…"))
		var scanned atomic.Int64
		var curDir atomic.Value
		curDir.Store(root)

		// a találatok és az állapot 150 ms-onként kerülnek a felületre
		ticker := time.NewTicker(150 * time.Millisecond)
		go func() {
			for range ticker.C {
				if closed.Load() {
					ticker.Stop()
					return
				}
				done := !running.Load()
				a.mw.Synchronize(func() {
					if closed.Load() || atomic.LoadInt64(&gen) != g {
						return
					}
					mu.Lock()
					if len(pending) > 0 {
						model.hits = append(model.hits, pending...)
						pending = nil
						model.PublishRowsReset()
						tv.Invalidate()
					}
					mu.Unlock()
					if !done {
						status.SetText(fmt.Sprintf(T("Keresés… %s, %s – %s"),
							Nf(len(model.hits), "%d találat"), Nf(int(scanned.Load()), "%d fájl átnézve"), elideMiddle(curDir.Load().(string), 70)))
					}
				})
				if done {
					ticker.Stop()
					a.mw.Synchronize(func() {
						if closed.Load() || atomic.LoadInt64(&gen) != g {
							return
						}
						word := T("Kész")
						if stop.Load() {
							word = T("Leállítva")
						}
						status.SetText(fmt.Sprintf(T("%s: %s, %s."), word, Nf(len(model.hits), "%d találat"), Nf(int(scanned.Load()), "%d fájl átnézve")))
						startBtn.SetEnabled(true)
						stopBtn.SetEnabled(false)
					})
					return
				}
			}
		}()

		go func() {
			defer running.Store(false)
			filepath.WalkDir(spec.root, func(p string, d fs.DirEntry, err error) error {
				if stop.Load() {
					return filepath.SkipAll
				}
				if err != nil {
					if d != nil && d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if p == spec.root {
					return nil
				}
				if d.IsDir() {
					curDir.Store(p)
					if !spec.dirsToo || spec.text != "" {
						if !spec.subdirs {
							return filepath.SkipDir
						}
						return nil
					}
				}
				scanned.Add(1)
				if matchName(d.Name(), spec.patterns, spec.matchCase) {
					info, ierr := d.Info()
					if ierr == nil && (d.IsDir() || ((spec.minSize < 0 || info.Size() >= spec.minSize) &&
						(spec.maxSize < 0 || info.Size() <= spec.maxSize))) &&
						(spec.after.IsZero() || info.ModTime().After(spec.after)) &&
						(len(needles) == 0 || (!d.IsDir() && fileContains(p, needles, spec.matchCase, &stop))) {
						mu.Lock()
						pending = append(pending, hit{p, info.Size(), info.ModTime(), d.IsDir()})
						mu.Unlock()
					}
				}
				if d.IsDir() && !spec.subdirs {
					return filepath.SkipDir
				}
				return nil
			})
		}()
	}

	err := Dialog{
		AssignTo: &dlg, Title: T("Fájlkeresés"), DefaultButton: &startBtn, CancelButton: &closeBtn,
		MinSize: Size{Width: 860, Height: 620}, Layout: VBox{},
		Children: []Widget{
			Composite{Layout: Grid{Columns: 3}, Children: []Widget{
				Label{Text: T("Keresés helye:")},
				LineEdit{AssignTo: &whereLE, Text: start},
				PushButton{Text: T("Tallózás…"), OnClicked: func() {
					fd := walk.FileDialog{Title: T("Keresés helye"), InitialDirPath: whereLE.Text()}
					if ok, _ := fd.ShowBrowseFolder(dlg); ok {
						whereLE.SetText(fd.FilePath)
					}
				}},
				Label{Text: T("Fájlnév:")},
				LineEdit{AssignTo: &nameLE, Text: pat, ColumnSpan: 2, CueBanner: T("pl. *.jpg;*.png  vagy egy névrészlet")},
				Label{Text: T("Tartalmazza:")},
				LineEdit{AssignTo: &textLE, ColumnSpan: 2, CueBanner: T("üresen: csak név szerint keres")},
			}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				CheckBox{AssignTo: &subCB, Text: T("Almappákban is"), Checked: true},
				CheckBox{AssignTo: &caseCB, Text: T("Kis/nagybetű különbözik")},
				CheckBox{AssignTo: &dirCB, Text: T("Mappák is")},
				HSpacer{},
				Label{Text: T("Méret (KB):")},
				LineEdit{AssignTo: &minLE, CueBanner: T("legalább"), MaxSize: Size{Width: 80}},
				LineEdit{AssignTo: &maxLE, CueBanner: T("legfeljebb"), MaxSize: Size{Width: 80}},
				Label{Text: T("Az utolsó")},
				LineEdit{AssignTo: &daysLE, MaxSize: Size{Width: 50}},
				Label{Text: T("napban módosítva")},
			}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{AssignTo: &startBtn, Text: T("Keresés indítása"), OnClicked: begin},
				PushButton{AssignTo: &stopBtn, Text: T("Leállítás"), Enabled: false, OnClicked: func() { stop.Store(true) }},
				PushButton{AssignTo: &gotoBtn, Text: T("Ugrás a fájlra"), OnClicked: gotoHit},
				HSpacer{},
			}},
			TableView{LastColumnStretched: true, AssignTo: &tv, Model: model, OnItemActivated: gotoHit, Columns: []TableViewColumn{
				{Title: T("Név"), Width: 260}, {Title: T("Mappa"), Width: 360}, {Title: T("Méret"), Width: 100, Alignment: AlignFar},
				{Title: T("Dátum"), Width: 120},
			}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				Label{AssignTo: &status, Text: T("Add meg a feltételeket, majd Keresés indítása (Enter).")},
				HSpacer{},
				PushButton{AssignTo: &closeBtn, Text: T("Bezárás"), OnClicked: func() { stop.Store(true); dlg.Cancel() }},
			}},
		},
	}.Create(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, appName, err.Error(), walk.MsgBoxIconError)
		return
	}
	themeDialog(dlg)
	if a.icon != nil {
		dlg.SetIcon(a.icon)
	}
	a.sizeableDialog(dlg, a.mw, "search", 900, 640)
	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) { stop.Store(true); closed.Store(true) })
	dlg.Starting().Attach(func() { nameLE.SetFocus(); nameLE.SetTextSelection(0, len(utf16.Encode([]rune(pat)))) })
	dlg.Run()
	stop.Store(true)
}

func elideMiddle(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return fmt.Sprintf(T("%s…%s"), string(r[:n/2-1]), string(r[len(r)-n/2:]))
}
