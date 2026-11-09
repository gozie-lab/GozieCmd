//go:build linux

package main

// Változásfigyelés Linuxon: inotify. Hálózati és FUSE-fájlrendszeren (NFS, SMB,
// sshfs, gvfs…) az inotify nem lát távoli változást, ott marad a lekérdezés.

import (
	"errors"

	"fyne.io/fyne/v2"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

type dirWatch struct {
	fd   int
	stop chan struct{}
	done chan struct{}
	once sync.Once
	sink *watchSink
}

var errRemoteFS = errors.New("hálózati fájlrendszer")

// isRemoteFS: NFS, SMB/CIFS, FUSE (sshfs, gvfs, rclone…), 9p, AFS, Ceph.
func isRemoteFS(path string) bool {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return false
	}
	switch uint32(st.Type) {
	case 0x6969, 0x517B, 0xFF534D42, 0xFE534D42, 0x65735546, 0x01021997, 0x5346414F, 0x00C36400, 0x6B414653:
		return true
	}
	return false
}

func startWatch(path string, fire func()) (*dirWatch, error) {
	if isRemoteFS(path) {
		return nil, errRemoteFS
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	mask := uint32(unix.IN_CREATE | unix.IN_DELETE | unix.IN_MODIFY | unix.IN_ATTRIB | unix.IN_CLOSE_WRITE |
		unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF)
	if _, err := unix.InotifyAddWatch(fd, path, mask); err != nil {
		unix.Close(fd)
		return nil, err
	}
	w := &dirWatch{fd: fd, stop: make(chan struct{}), done: make(chan struct{}), sink: newWatchSink(fire)}
	go w.loop()
	return w, nil
}

func (w *dirWatch) loop() {
	defer close(w.done)
	buf := make([]byte, 64*1024)
	fds := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}}
	for {
		select {
		case <-w.stop:
			return
		default:
		}
		n, err := unix.Poll(fds, 400)
		if err != nil && err != unix.EINTR {
			return
		}
		if n <= 0 {
			continue
		}
		for {
			k, err := unix.Read(w.fd, buf)
			if k <= 0 || err != nil {
				break
			}
			changed := false
			for off := 0; off+unix.SizeofInotifyEvent <= k; {
				ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
				if ev.Mask&unix.IN_IGNORED == 0 {
					changed = true
				}
				off += unix.SizeofInotifyEvent + int(ev.Len)
			}
			if changed {
				w.sink.poke()
			}
		}
	}
}

func (w *dirWatch) Stop() {
	w.once.Do(func() {
		w.sink.stop()
		close(w.stop)
		<-w.done
		unix.Close(w.fd)
	})
}

func (w *dirWatch) remote() bool { return false }

// syncWatch: a figyelés kövesse a panel mappáját.
func (p *Panel) syncWatch() {
	want := ""
	if p.vfs == nil && p.path != "" {
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
	w, err := startWatch(path, func() {
		fyne.Do(func() {
			if p.vfs == nil && p.path == path {
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
