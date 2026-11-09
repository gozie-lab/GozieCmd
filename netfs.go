//go:build windows

package main

// Hálózat: gépek és megosztásaik listája (virtuális mappa), UNC-utak,
// bejelentkezés megosztásra, hálózati meghajtó csatolása / leválasztása.

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
)

var (
	netapi32 = syscall.NewLazyDLL("netapi32.dll")
	mpr      = syscall.NewLazyDLL("mpr.dll")

	procNetShareEnum         = netapi32.NewProc("NetShareEnum")
	procNetServerEnum        = netapi32.NewProc("NetServerEnum")
	procNetApiBufferFree     = netapi32.NewProc("NetApiBufferFree")
	procWNetAddConnection3W  = mpr.NewProc("WNetAddConnection3W")
	procWNetConnectionDialog = mpr.NewProc("WNetConnectionDialog")
	procWNetDisconnectDialog = mpr.NewProc("WNetDisconnectDialog")
)

const netRootMarker = `\\`

type shareInfo1 struct {
	Netname *uint16
	Type    uint32
	Remark  *uint16
}

type serverInfo100 struct {
	PlatformID uint32
	Name       *uint16
}

type netResource struct {
	Scope       uint32
	Type        uint32
	DisplayType uint32
	Usage       uint32
	LocalName   *uint16
	RemoteName  *uint16
	Comment     *uint16
	Provider    *uint16
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	var buf []uint16
	for ptr := unsafe.Pointer(p); ; ptr = unsafe.Add(ptr, 2) {
		c := *(*uint16)(ptr)
		if c == 0 {
			break
		}
		buf = append(buf, c)
	}
	return syscall.UTF16ToString(buf)
}

var errTimeout = errors.New(T("a gép nem válaszol időben"))

func withTimeout[T any](d time.Duration, f func() (T, error)) (T, error) {
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		v, err := f()
		ch <- res{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(d):
		var zero T
		return zero, errTimeout
	}
}

type shareItem struct {
	name    string
	special bool
	remark  string
}

func enumShares(server string) ([]shareItem, error) {
	sp, err := syscall.UTF16PtrFromString(server)
	if err != nil {
		return nil, err
	}
	var buf uintptr
	var read, total, resume uint32
	r, _, _ := procNetShareEnum.Call(uintptr(unsafe.Pointer(sp)), 1, uintptr(unsafe.Pointer(&buf)), 0xFFFFFFFF,
		uintptr(unsafe.Pointer(&read)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&resume)))
	if buf != 0 {
		defer procNetApiBufferFree.Call(buf)
	}
	if r != 0 && r != 234 { // ERROR_MORE_DATA
		return nil, syscall.Errno(r)
	}
	var out []shareItem
	for _, it := range unsafe.Slice((*shareInfo1)(unsafe.Pointer(buf)), read) {
		if it.Type&0xFF != 0 { // csak lemezmegosztás (nyomtató, IPC$ nem)
			continue
		}
		out = append(out, shareItem{utf16PtrToString(it.Netname), it.Type&0x80000000 != 0, utf16PtrToString(it.Remark)})
	}
	return out, nil
}

// enumServers a hálózatban látható gépek (a régi „Számítógép-tallózó” alapján;
// újabb hálózatokon gyakran üres, ezért a korábban megnyitott gépeket is mutatjuk).
func enumServers() ([]string, error) {
	var buf uintptr
	var read, total, resume uint32
	r, _, _ := procNetServerEnum.Call(0, 100, uintptr(unsafe.Pointer(&buf)), 0xFFFFFFFF,
		uintptr(unsafe.Pointer(&read)), uintptr(unsafe.Pointer(&total)), 0xFFFFFFFF, 0, uintptr(unsafe.Pointer(&resume)))
	if buf != 0 {
		defer procNetApiBufferFree.Call(buf)
	}
	if r != 0 && r != 234 {
		return nil, syscall.Errno(r)
	}
	var out []string
	for _, it := range unsafe.Slice((*serverInfo100)(unsafe.Pointer(buf)), read) {
		out = append(out, utf16PtrToString(it.Name))
	}
	return out, nil
}

// connectShare a Windows saját bejelentkező ablakával csatlakozik egy megosztáshoz.
func connectShare(owner win.HWND, unc string) error {
	remote, _ := syscall.UTF16PtrFromString(unc)
	nr := netResource{Type: 1, RemoteName: remote} // RESOURCETYPE_DISK
	const flags = 0x08 | 0x10                      // CONNECT_INTERACTIVE | CONNECT_PROMPT
	r, _, _ := procWNetAddConnection3W.Call(uintptr(owner), uintptr(unsafe.Pointer(&nr)), 0, 0, flags)
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func isAuthError(err error) bool {
	var en syscall.Errno
	if !errors.As(err, &en) {
		return false
	}
	switch en {
	case 5, 86, 1244, 1272, 1326, 1331, 1907, 1909: // hozzáférés megtagadva, rossz jelszó, nincs hitelesítés…
		return true
	}
	return false
}

// uncParts: `\\gép\megosztás\…` → gép, megosztás.
func uncParts(path string) (server, share string, ok bool) {
	if !strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(path[2:], `\`), `\`)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", true
	}
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], parts[1], true
}

// ── a hálózat mint virtuális mappa ──

type netFS struct{ app *App }

func (n *netFS) Kind() string { return "net" }
func (n *netFS) Root() string { return "" }
func (n *netFS) Info() string { return T("Hálózat") }
func (n *netFS) Close()       {}

func (n *netFS) List(dir string) ([]*Entry, error) {
	if dir == "" {
		seen := map[string]bool{}
		var out []*Entry
		add := func(name string) {
			k := strings.ToUpper(name)
			if name == "" || seen[k] {
				return
			}
			seen[k] = true
			out = append(out, newEntry(name, `\\`+name, true, 0, time.Time{}))
		}
		if found, err := withTimeout(4*time.Second, enumServers); err == nil {
			for _, s := range found {
				add(s)
			}
		}
		for _, s := range n.app.netServers() {
			add(s)
		}
		sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
		return out, nil
	}
	list := func() ([]shareItem, error) { return enumShares(dir) }
	shares, err := withTimeout(10*time.Second, list)
	if err != nil && isAuthError(err) {
		// a gép bejelentkezést kér: a Windows saját ablakával, majd újra
		if connectShare(n.app.mw.Handle(), dir+`\IPC$`) == nil {
			shares, err = withTimeout(10*time.Second, list)
		}
	}
	if err != nil {
		dlog("megosztások listázása (%s): %v", dir, err)
		return nil, err
	}
	var out []*Entry
	for _, sh := range shares {
		if sh.special && !n.app.showHidden {
			continue
		}
		out = append(out, newEntry(sh.name, dir+`\`+sh.name, true, 0, time.Time{}))
	}
	n.app.rememberServer(strings.TrimPrefix(dir, `\\`))
	return out, nil
}

func (n *netFS) Parent(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	return "", true
}

func (n *netFS) Join(dir, name string) string {
	if dir == "" {
		return `\\` + name
	}
	return dir + `\` + name
}

func (n *netFS) Display(dir string) string {
	if dir == "" {
		return T("Hálózat")
	}
	return dir
}

// ── panel és alkalmazás ──

// openNet a hálózati nézetet nyitja: dir == "" a gépek, `\\gép` a megosztások listája.
func (p *Panel) openNet(dir, selectName string) bool {
	if p.kind() == "net" {
		return p.navigateVFS(dir, selectName)
	}
	n := &netFS{app: p.app}
	if !p.openVFS(n, dir) {
		return false
	}
	p.selectName(selectName)
	return true
}

func (a *App) netServers() []string {
	v, _ := a.settings.Get("netServers")
	var out []string
	for _, s := range strings.Split(v, "|") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) rememberServer(name string) {
	if name == "" || strings.ContainsAny(name, `\|`) {
		return
	}
	list := []string{name}
	for _, s := range a.netServers() {
		if !strings.EqualFold(s, name) && len(list) < 30 {
			list = append(list, s)
		}
	}
	a.settings.Put("netServers", strings.Join(list, "|"))
}

func (a *App) forgetServer() {
	p := a.active
	if p.kind() != "net" || p.vpath != "" {
		return
	}
	e := p.currentEntry()
	if e == nil || e.IsParent {
		return
	}
	var list []string
	for _, s := range a.netServers() {
		if !strings.EqualFold(s, e.Name) {
			list = append(list, s)
		}
	}
	a.settings.Put("netServers", strings.Join(list, "|"))
	p.refresh()
}

func (a *App) openNetwork() {
	a.active.openNet("", "")
	a.active.tv.SetFocus()
}

func (a *App) openComputer() {
	t, ok := inputBox(a.mw, T("Gép megnyitása"), T("A gép neve vagy IP-címe (pl. NAS, 192.168.1.10), vagy egy teljes\nhálózati út (pl. \\\\NAS\\Fotók):"), "", false)
	t = strings.Trim(strings.TrimSpace(t), `"`)
	if !ok || t == "" {
		return
	}
	if !strings.HasPrefix(t, `\\`) {
		t = `\\` + strings.TrimLeft(t, `\/`)
	}
	a.active.navigate(t, "", true, false)
	a.active.tv.SetFocus()
}

func (a *App) mapDrive() {
	procWNetConnectionDialog.Call(uintptr(a.mw.Handle()), 1)
	a.left.populateDrives()
	a.right.populateDrives()
}

func (a *App) unmapDrive() {
	procWNetDisconnectDialog.Call(uintptr(a.mw.Handle()), 1)
	a.left.populateDrives()
	a.right.populateDrives()
	a.refreshBoth()
}

// netNavigate a helyi navigate kiegészítése: `\\` és `\\gép` a hálózati nézetbe visz,
// hozzáférési hibánál bejelentkezést kér.
func (p *Panel) netNavigate(path string) (handled bool) {
	server, share, ok := uncParts(path)
	if !ok {
		return false
	}
	if share == "" {
		if server == "" {
			p.openNet("", "")
		} else {
			p.openNet(`\\`+server, "")
		}
		return true
	}
	return false
}

func (p *Panel) tryConnect(path string, err error) bool {
	server, share, ok := uncParts(path)
	if !ok || share == "" || !isAuthError(err) {
		return false
	}
	unc := `\\` + server + `\` + share
	dlog("bejelentkezés kérése: %s (%v)", unc, err)
	if cerr := connectShare(p.app.mw.Handle(), unc); cerr != nil {
		dlog("  sikertelen: %v", cerr)
		return false
	}
	return true
}

func isUNCShareRoot(path string) bool {
	server, share, ok := uncParts(path)
	if !ok || server == "" || share == "" {
		return false
	}
	return strings.EqualFold(strings.TrimRight(path, `\`), strings.TrimRight(filepath.VolumeName(path), `\`))
}
