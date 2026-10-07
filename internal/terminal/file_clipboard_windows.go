//go:build windows

package terminal

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var fileClipboardMu sync.Mutex

var (
	fileUser32                  = syscall.NewLazyDLL("user32.dll")
	fileKernel32                = syscall.NewLazyDLL("kernel32.dll")
	fileShell32                 = syscall.NewLazyDLL("shell32.dll")
	fileOpenClipboard           = fileUser32.NewProc("OpenClipboard")
	fileCloseClipboard          = fileUser32.NewProc("CloseClipboard")
	fileEmptyClipboard          = fileUser32.NewProc("EmptyClipboard")
	fileGetClipboardData        = fileUser32.NewProc("GetClipboardData")
	fileSetClipboardData        = fileUser32.NewProc("SetClipboardData")
	fileRegisterClipboardFormat = fileUser32.NewProc("RegisterClipboardFormatW")
	fileGlobalAlloc             = fileKernel32.NewProc("GlobalAlloc")
	fileGlobalFree              = fileKernel32.NewProc("GlobalFree")
	fileGlobalLock              = fileKernel32.NewProc("GlobalLock")
	fileGlobalUnlock            = fileKernel32.NewProc("GlobalUnlock")
	fileDragQueryFile           = fileShell32.NewProc("DragQueryFileW")
)

const (
	fileCFUnicodeText  = 13
	fileCFHDrop        = 15
	fileGmemMoveable   = 0x0002
	fileGmemZeroInit   = 0x0040
	fileDropEffectMove = 2
)

type fileDropFiles struct {
	pFiles uint32
	ptX    int32
	ptY    int32
	fNC    int32
	fWide  int32
}

func fileGlobalBytes(data []byte) (uintptr, error) {
	h, _, _ := fileGlobalAlloc.Call(fileGmemMoveable|fileGmemZeroInit, uintptr(len(data)))
	if h == 0 {
		return 0, fmt.Errorf("GlobalAlloc failed")
	}
	p, _, _ := fileGlobalLock.Call(h)
	if p == 0 {
		fileGlobalFree.Call(h)
		return 0, fmt.Errorf("GlobalLock failed")
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), len(data)), data)
	fileGlobalUnlock.Call(h)
	return h, nil
}

func fileHDROP(paths []string) (uintptr, error) {
	var utf16 []uint16
	for _, path := range paths {
		u, err := syscall.UTF16FromString(path)
		if err == nil {
			utf16 = append(utf16, u...)
		}
	}
	if len(utf16) == 0 {
		return 0, fmt.Errorf("no local files")
	}
	utf16 = append(utf16, 0)
	header := uint32(unsafe.Sizeof(fileDropFiles{}))
	data := make([]byte, int(header)+len(utf16)*2)
	df := (*fileDropFiles)(unsafe.Pointer(&data[0]))
	df.pFiles = header
	df.fWide = 1
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&data[header])), len(utf16)), utf16)
	return fileGlobalBytes(data)
}

func filePreferredDropEffect() uintptr {
	name, _ := syscall.UTF16PtrFromString("Preferred DropEffect")
	r, _, _ := fileRegisterClipboardFormat.Call(uintptr(unsafe.Pointer(name)))
	return r
}

func setFileClipboard(ctx context.Context, text string, paths []string, cut bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fileClipboardMu.Lock()
	defer fileClipboardMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := fileOpenClipboard.Find(); err != nil {
		return err
	}
	opened, _, _ := fileOpenClipboard.Call(0)
	if opened == 0 {
		return fmt.Errorf("OpenClipboard failed")
	}
	defer fileCloseClipboard.Call()
	fileEmptyClipboard.Call()

	u16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	textHandle, err := fileGlobalBytes(unsafe.Slice((*byte)(unsafe.Pointer(&u16[0])), len(u16)*2))
	if err != nil {
		return err
	}
	dropHandle, err := fileHDROP(paths)
	if err != nil {
		fileGlobalFree.Call(textHandle)
		return err
	}
	if r, _, _ := fileSetClipboardData.Call(fileCFUnicodeText, textHandle); r == 0 {
		fileGlobalFree.Call(textHandle)
		fileGlobalFree.Call(dropHandle)
		return fmt.Errorf("SetClipboardData(CF_UNICODETEXT) failed")
	}
	if r, _, _ := fileSetClipboardData.Call(fileCFHDrop, dropHandle); r == 0 {
		fileGlobalFree.Call(dropHandle)
		return fmt.Errorf("SetClipboardData(CF_HDROP) failed")
	}
	if format := filePreferredDropEffect(); format != 0 {
		effect := uint32(1)
		if cut {
			effect = fileDropEffectMove
		}
		data := []byte{byte(effect), byte(effect >> 8), byte(effect >> 16), byte(effect >> 24)}
		if handle, err := fileGlobalBytes(data); err == nil {
			if r, _, _ := fileSetClipboardData.Call(format, handle); r == 0 {
				fileGlobalFree.Call(handle)
			}
		}
	}
	return nil
}

func readFileClipboard(ctx context.Context) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	fileClipboardMu.Lock()
	defer fileClipboardMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := fileOpenClipboard.Find(); err != nil {
		return nil, false, err
	}
	opened, _, _ := fileOpenClipboard.Call(0)
	if opened == 0 {
		return nil, false, fmt.Errorf("OpenClipboard failed")
	}
	defer fileCloseClipboard.Call()
	h, _, _ := fileGetClipboardData.Call(fileCFHDrop)
	if h == 0 {
		return nil, false, errFileClipboardUnavailable
	}
	count, _, _ := fileDragQueryFile.Call(h, 0xffffffff, 0, 0)
	paths := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		length, _, _ := fileDragQueryFile.Call(h, i, 0, 0)
		buf := make([]uint16, length+1)
		fileDragQueryFile.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), length+1)
		paths = append(paths, syscall.UTF16ToString(buf))
	}
	cut := false
	if format := filePreferredDropEffect(); format != 0 {
		preferred, _, _ := fileGetClipboardData.Call(format)
		if preferred != 0 {
			ptr, _, _ := fileGlobalLock.Call(preferred)
			if ptr != 0 {
				cut = *(*uint32)(unsafe.Pointer(ptr))&fileDropEffectMove != 0
				fileGlobalUnlock.Call(preferred)
			}
		}
	}
	return paths, cut, nil
}
