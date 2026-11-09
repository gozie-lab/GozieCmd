//go:build windows

package main

// Változásfigyelés Windowson: ReadDirectoryChangesW.
// A figyelt mappát a program nyitva tartja, ami megakadályozná a pendrive
// biztonságos eltávolítását. Ezért a kezelőt bejelentjük a rendszernek
// (RegisterDeviceNotification): eltávolítási kérésnél a főablak WM_DEVICECHANGE
// üzenetet kap, és a figyelést azonnal leállítjuk.

import (
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var (
	procRegisterDeviceNotification   = syscall.NewLazyDLL("user32.dll").NewProc("RegisterDeviceNotificationW")
	procUnregisterDeviceNotification = syscall.NewLazyDLL("user32.dll").NewProc("UnregisterDeviceNotification")
	procGetDriveTypeW                = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDriveTypeW")
)

type devBroadcastHandle struct {
	Size       uint32
	DeviceType uint32
	Reserved   uint32
	Handle     windows.Handle
	HDevNotify uintptr
	EventGUID  windows.GUID
	NameOffset int32
	Data       [1]byte
}

type dirWatch struct {
	h      windows.Handle
	stopEv windows.Handle
	notify uintptr
	done   chan struct{}
	once   sync.Once
	sink   *watchSink
	net    bool
}

// pathIsRemote: hálózati meghajtó vagy UNC-útvonal.
func pathIsRemote(path string) bool {
	if strings.HasPrefix(path, `\\`) {
		return true
	}
	if len(path) >= 2 && path[1] == ':' {
		root, _ := syscall.UTF16PtrFromString(path[:2] + `\`)
		t, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(root)))
		return t == 4 // DRIVE_REMOTE
	}
	return false
}

func startWatch(path string, hwnd win.HWND, fire func()) (*dirWatch, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	stopEv, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	w := &dirWatch{h: h, stopEv: stopEv, done: make(chan struct{}), sink: newWatchSink(fire), net: pathIsRemote(path)}
	if hwnd != 0 {
		filter := devBroadcastHandle{DeviceType: 6, Handle: h} // DBT_DEVTYP_HANDLE
		filter.Size = uint32(unsafe.Sizeof(filter))
		w.notify, _, _ = procRegisterDeviceNotification.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&filter)), 0)
	}
	go w.loop()
	return w, nil
}

func (w *dirWatch) loop() {
	defer close(w.done)
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return
	}
	defer windows.CloseHandle(ev)
	// A rendszer a művelet közben (aszinkron) írja a puffert és az OVERLAPPED-et:
	// mindkettő a halmon legyen, ne a goroutine vermén (az áthelyeződhet).
	buf := make([]byte, 64*1024)
	ov := new(windows.Overlapped)
	const filter = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
		windows.FILE_NOTIFY_CHANGE_ATTRIBUTES | windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE
	for {
		*ov = windows.Overlapped{HEvent: ev}
		windows.ResetEvent(ev)
		if err := windows.ReadDirectoryChanges(w.h, &buf[0], uint32(len(buf)), false, filter, nil, ov, 0); err != nil {
			return // pl. a mappát törölték: a lekérdezés átveszi
		}
		r, err := windows.WaitForMultipleObjects([]windows.Handle{ev, w.stopEv}, false, windows.INFINITE)
		if err != nil || r != windows.WAIT_OBJECT_0 {
			windows.CancelIoEx(w.h, ov)
			var n uint32
			windows.GetOverlappedResult(w.h, ov, &n, true)
			return
		}
		var n uint32
		if err := windows.GetOverlappedResult(w.h, ov, &n, false); err != nil {
			return
		}
		w.sink.poke() // n == 0: túl sok változás egyszerre – ilyenkor is frissítünk
	}
}

// Stop leállítja a figyelést és elengedi a mappát (a felület szálán hívandó).
func (w *dirWatch) Stop() {
	w.once.Do(func() {
		w.sink.stop()
		if w.notify != 0 {
			procUnregisterDeviceNotification.Call(w.notify)
		}
		windows.SetEvent(w.stopEv)
		<-w.done
		windows.CloseHandle(w.h)
		windows.CloseHandle(w.stopEv)
	})
}

func (w *dirWatch) remote() bool { return w.net }

// ── a főablak WM_DEVICECHANGE kezelése ──

const (
	dbtDeviceArrival        = 0x8000
	dbtDeviceQueryRemove    = 0x8001
	dbtDeviceQueryRemoveBad = 0x8002
	dbtDeviceRemovePending  = 0x8003
	dbtDeviceRemoveComplete = 0x8004
)

var devChangeProc = syscall.NewCallback(func(hwnd, msg, wp, lp, id, ref uintptr) uintptr {
	if msg == 0x0219 && theApp != nil && lp != 0 { // WM_DEVICECHANGE
		hdr := (*devBroadcastHandle)(unsafe.Pointer(lp))
		switch {
		case hdr.DeviceType == 6 && (wp == dbtDeviceQueryRemove || wp == dbtDeviceRemovePending || wp == dbtDeviceRemoveComplete):
			for _, p := range []*Panel{theApp.left, theApp.right} {
				if p != nil && p.watch != nil && p.watch.h == hdr.Handle {
					dlog("változásfigyelés felfüggesztve (eszköz eltávolítása): %s", p.watchPath)
					p.watch.Stop()
					p.watch = nil
					p.watchOff = true
				}
			}
			return 1 // engedélyezzük az eltávolítást
		case hdr.DeviceType == 6 && wp == dbtDeviceQueryRemoveBad:
			for _, p := range []*Panel{theApp.left, theApp.right} {
				if p != nil && p.watchOff {
					p.watchOff = false
					p.watchPath = ""
				}
			}
		case hdr.DeviceType == 2 && (wp == dbtDeviceArrival || wp == dbtDeviceRemoveComplete): // DBT_DEVTYP_VOLUME
			theApp.left.populateDrives()
			theApp.right.populateDrives()
		}
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wp, lp)
	return r
})

func (a *App) hookDeviceChange() { subclassOnce(a.mw.Handle(), devChangeProc, 0xC040) }

// syncWatch: a figyelés kövesse a panel mappáját.
func (p *Panel) syncWatch() {
	want := ""
	if p.vfs == nil && p.path != "" && !p.watchOff {
		want = p.path
	}
	if want == p.watchPath && (p.watch != nil || want == "" || p.watchFailed) {
		return
	}
	if p.watch != nil {
		p.watch.Stop()
		p.watch = nil
	}
	p.watchPath, p.watchFailed = want, false
	if want == "" {
		return
	}
	path := want
	w, err := startWatch(path, p.app.mw.Handle(), func() {
		p.app.mw.Synchronize(func() {
			if p.vfs == nil && strings.EqualFold(p.path, path) {
				p.refresh()
			}
		})
	})
	if err != nil {
		dlog("változásfigyelés nem indult (%s): %v", path, err)
		p.watchFailed = true
		return
	}
	p.watch = w
}
