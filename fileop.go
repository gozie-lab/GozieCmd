//go:build windows

package main

// Fájlműveletek a Windows IFileOperation felületével: ugyanaz a folyamatablak,
// ütközéskezelés és Lomtár, mint az Intézőben – és szükség esetén rendszergazdai
// jóváhagyást (UAC) kér, például a C:\ gyökerébe íráskor.

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/lxn/win"
)

var (
	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")

	clsidFileOperation = guid{0x3AD05575, 0x8857, 0x4850, [8]byte{0x92, 0x77, 0x11, 0xB8, 0x5B, 0xDB, 0x8E, 0x09}}
	iidFileOperation   = guid{0x947AAB5F, 0x0A5C, 0x4C13, [8]byte{0xB4, 0xD6, 0x4B, 0xF7, 0x83, 0x6F, 0xC9, 0xF8}}
	iidShellItem       = guid{0x43826D1E, 0xE718, 0x42EE, [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

const (
	fofxShowElevationPrompt = 0x00040000
	fofxRecycleOnDelete     = 0x00080000
	fofxAddUndoRecord       = 0x20000000

	// IFileOperation vtábla-indexek
	foSetOperationFlags = 5
	foSetOwnerWindow    = 9
	foMoveItem          = 14
	foCopyItem          = 16
	foDeleteItem        = 18
	foPerform           = 21
	foAnyAborted        = 22

	hrUserCancelled = 0x80270000 // COPYENGINE_E_USER_CANCELLED
	hrCancelled     = 0x800704C7 // ERROR_CANCELLED
)

func shellItem(path string) (uintptr, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var item uintptr
	hr, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)))
	if hr != 0 {
		return 0, fmt.Errorf("%s: %s", filepath.Base(path), hresultText(hr))
	}
	return item, nil
}

// fileOp: op = foCopy / foMove / foDelete (a régi SHFileOperation kódjai).
// target másolásnál/mozgatásnál: létező mappa → bele; egyetlen forrás és nem
// létező út → új név; több forrás → mappa (létrehozza).
func fileOp(owner win.HWND, op uint32, srcs []string, target string, flags uint32) (hr uintptr, aborted bool, err error) {
	var fo uintptr
	r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOperation)), 0, 1|4,
		uintptr(unsafe.Pointer(&iidFileOperation)), uintptr(unsafe.Pointer(&fo)))
	if r != 0 || fo == 0 {
		return r, false, fmt.Errorf(T("IFileOperation nem érhető el (0x%X)"), r)
	}
	defer comRelease(fo)
	comCall(fo, foSetOperationFlags, uintptr(flags))
	comCall(fo, foSetOwnerWindow, uintptr(owner))

	var dest uintptr
	var newName *uint16
	if op != foDelete {
		destDir := target
		if st, serr := os.Stat(target); serr != nil || !st.IsDir() {
			if len(srcs) == 1 {
				destDir = filepath.Dir(target)
				newName, _ = syscall.UTF16PtrFromString(filepath.Base(target))
			}
			if _, serr := os.Stat(destDir); serr != nil {
				if merr := os.MkdirAll(destDir, 0o777); merr != nil {
					return 0, false, merr
				}
			}
		}
		if dest, err = shellItem(destDir); err != nil {
			return 0, false, err
		}
		defer comRelease(dest)
	}
	for _, s := range srcs {
		item, ierr := shellItem(s)
		if ierr != nil {
			return 0, false, ierr
		}
		switch op {
		case foCopy:
			r = comCall(fo, foCopyItem, item, dest, uintptr(unsafe.Pointer(newName)), 0)
		case foMove:
			r = comCall(fo, foMoveItem, item, dest, uintptr(unsafe.Pointer(newName)), 0)
		case foDelete:
			r = comCall(fo, foDeleteItem, item, 0)
		}
		comRelease(item)
		if r != 0 {
			return r, false, fmt.Errorf("%s: %s", filepath.Base(s), hresultText(r))
		}
	}
	hr = comCall(fo, foPerform)
	var ab int32
	comCall(fo, foAnyAborted, uintptr(unsafe.Pointer(&ab)))
	return hr, ab != 0, nil
}

func hresultText(hr uintptr) string {
	if hr>>16 == 0x8007 { // Win32-hiba
		return syscall.Errno(hr & 0xFFFF).Error()
	}
	return fmt.Sprintf("0x%X", hr)
}

func isCancelHR(hr uintptr) bool {
	return hr == hrUserCancelled || hr == hrCancelled || hr == 0x80004004 // E_ABORT
}

// canWriteDir: írható-e a mappa a program saját jogaival (rendszergazdai jóváhagyás nélkül).
func canWriteDir(dir string) bool {
	f, err := os.CreateTemp(dir, ".duoteszt*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
