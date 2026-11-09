//go:build windows

package main

// Közvetlen Windows API hívások: fájlműveletek (Shell), meghajtóadatok, kódlap.

import (
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/lxn/win"
)

var (
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procSHFileOperationW     = shell32.NewProc("SHFileOperationW")
	procGetDiskFreeSpaceExW  = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetVolumeInformation = kernel32.NewProc("GetVolumeInformationW")
	procMultiByteToWideChar  = kernel32.NewProc("MultiByteToWideChar")
)

const (
	foMove   = 1
	foCopy   = 2
	foDelete = 3

	fofMultiDestFiles  = 0x0001
	fofNoConfirmation  = 0x0010
	fofAllowUndo       = 0x0040
	fofNoConfirmMkdir  = 0x0200
	fofWantNukeWarning = 0x4000

	errCancelled = 1223

	createNewConsole = 0x00000010
	createNoWindow   = 0x08000000

	fileAttrReadonly  = 0x1
	fileAttrHidden    = 0x2
	fileAttrSystem    = 0x4
	fileAttrDirectory = 0x10
	fileAttrArchive   = 0x20
)

// shFileOpStruct a SHFILEOPSTRUCTW megfelelője 64 bites Windowsra.
type shFileOpStruct struct {
	Hwnd                 win.HWND
	Func                 uint32
	From                 *uint16
	To                   *uint16
	Flags                uint16
	AnyOperationsAborted int32
	NameMappings         uintptr
	ProgressTitle        *uint16
}

// doubleNull dupla nullával lezárt UTF-16 listát készít, ahogy a Shell várja.
func doubleNull(paths []string) *uint16 {
	var buf []uint16
	for _, p := range paths {
		buf = append(buf, utf16.Encode([]rune(p))...)
		buf = append(buf, 0)
	}
	buf = append(buf, 0)
	return &buf[0]
}

// shellFileOp Windows-os másolás/mozgatás/törlés a rendszer saját
// folyamatjelzőjével, ütközéskezelésével és Lomtárával.
func shellFileOp(owner win.HWND, op uint32, from []string, to string, flags uint16) (code uintptr, aborted bool) {
	s := shFileOpStruct{Hwnd: owner, Func: op, From: doubleNull(from), Flags: flags}
	if to != "" {
		s.To = doubleNull([]string{to})
	}
	r, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&s)))
	return r, s.AnyOperationsAborted != 0
}

func diskFree(path string) (free, total uint64, ok bool) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	r, _, _ := procGetDiskFreeSpaceExW.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), 0)
	return free, total, r != 0
}

func volumeName(root string) string {
	p, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 261)
	r, _, _ := procGetVolumeInformation.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0, 0, 0, 0)
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// ansiToString a rendszer ANSI kódlapjáról (magyar Windowson 1250) alakít.
func ansiToString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	n, _, _ := procMultiByteToWideChar.Call(0, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return string(b)
	}
	buf := make([]uint16, n)
	procMultiByteToWideChar.Call(0, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)),
		uintptr(unsafe.Pointer(&buf[0])), n)
	return string(utf16.Decode(buf))
}

func shellOpen(owner win.HWND, path, dir string) bool {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, _ := syscall.UTF16PtrFromString(path)
	cwd, _ := syscall.UTF16PtrFromString(dir)
	return win.ShellExecute(owner, verb, file, nil, cwd, win.SW_SHOWNORMAL)
}

// runCmdLine nyers parancssort indít a cmd.exe-vel.
func runCmdLine(dir, cmdline string, flags uint32) error {
	c := exec.Command("cmd.exe")
	c.Dir = dir
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdline, CreationFlags: flags}
	if err := c.Start(); err != nil {
		return err
	}
	go c.Wait()
	return nil
}

func removeTabStop(h win.HWND) {
	style := win.GetWindowLong(h, win.GWL_STYLE)
	win.SetWindowLong(h, win.GWL_STYLE, style&^win.WS_TABSTOP)
}

func attrString(a uint32) string {
	var sb strings.Builder
	for _, f := range []struct {
		ch   byte
		flag uint32
	}{{'r', fileAttrReadonly}, {'a', fileAttrArchive}, {'h', fileAttrHidden}, {'s', fileAttrSystem}} {
		if a&f.flag != 0 {
			sb.WriteByte(f.ch)
		} else {
			sb.WriteByte('-')
		}
	}
	return sb.String()
}

var procShellExecuteExW = shell32.NewProc("ShellExecuteExW")

type shellExecuteInfo struct {
	CbSize        uint32
	FMask         uint32
	Hwnd          win.HWND
	Verb          *uint16
	File          *uint16
	Parameters    *uint16
	Directory     *uint16
	Show          int32
	InstApp       uintptr
	IDList        uintptr
	Class         *uint16
	KeyClass      uintptr
	HotKey        uint32
	IconOrMonitor uintptr
	Process       uintptr
}

const seeMaskInvokeIDList = 0x0000000C

// shellVerbEx pl. a "properties" igéhez (Tulajdonságok ablak).
func shellVerbEx(owner win.HWND, verb, path string) bool {
	v, _ := syscall.UTF16PtrFromString(verb)
	f, _ := syscall.UTF16PtrFromString(path)
	info := shellExecuteInfo{FMask: seeMaskInvokeIDList, Hwnd: owner, Verb: v, File: f, Show: win.SW_SHOWNORMAL}
	info.CbSize = uint32(unsafe.Sizeof(info))
	r, _, _ := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	return r != 0
}

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procToUnicode        = user32.NewProc("ToUnicode")
	procGetKeyboardState = user32.NewProc("GetKeyboardState")
	procMapVirtualKeyW   = user32.NewProc("MapVirtualKeyW")
)

// keyChar a lenyomott billentyű karakterét adja az aktuális kiosztás szerint.
func keyChar(vk uint32) string {
	var state [256]byte
	if r, _, _ := procGetKeyboardState.Call(uintptr(unsafe.Pointer(&state[0]))); r == 0 {
		return ""
	}
	scan, _, _ := procMapVirtualKeyW.Call(uintptr(vk), 0)
	var buf [8]uint16
	// 4-es jelző: ne módosítsa a billentyűzet állapotát (holt billentyűk)
	n, _, _ := procToUnicode.Call(uintptr(vk), scan, uintptr(unsafe.Pointer(&state[0])),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 4)
	if int32(n) <= 0 {
		return ""
	}
	return string(utf16.Decode(buf[:n]))
}

// oemToString a DOS (OEM) kódlapról alakít – régi ZIP-fájlnevekhez (magyarul 852).
func oemToString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	n, _, _ := procMultiByteToWideChar.Call(1, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return string(b)
	}
	buf := make([]uint16, n)
	procMultiByteToWideChar.Call(1, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)),
		uintptr(unsafe.Pointer(&buf[0])), n)
	return string(utf16.Decode(buf))
}

// errnoText: Windowson a rendszer maga adja a magyar hibaszöveget.
func errnoText(syscall.Errno) string { return "" }
