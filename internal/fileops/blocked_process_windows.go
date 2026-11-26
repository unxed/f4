//go:build windows

package fileops

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	rmErrorMoreData = syscall.Errno(234)
	rmMaxListTries  = 3
)

var (
	rmStartSession      = syscall.NewLazyDLL("rstrtmgr.dll").NewProc("RmStartSession")
	rmRegisterResources = syscall.NewLazyDLL("rstrtmgr.dll").NewProc("RmRegisterResources")
	rmGetList           = syscall.NewLazyDLL("rstrtmgr.dll").NewProc("RmGetList")
	rmEndSession        = syscall.NewLazyDLL("rstrtmgr.dll").NewProc("RmEndSession")
)

type rmUniqueProcess struct {
	processID uint32
	startTime syscall.Filetime
}

type rmProcessInfo struct {
	process          rmUniqueProcess
	appName          [256]uint16
	serviceShortName [64]uint16
	applicationType  uint32
	appStatus        uint32
	tssessionID      uint32
	restartable      int32
}

// ProcessesUsingFile asks Windows Restart Manager which processes currently use
// path. Restart Manager is a best-effort source: access restrictions and races
// can leave a process out of the result.
func ProcessesUsingFile(path string) ([]FileProcess, error) {
	if path == "" {
		return nil, errors.New("empty file path")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve file path: %w", err)
	}
	fileName, err := syscall.UTF16PtrFromString(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("encode file path: %w", err)
	}

	var keyBytes [16]byte
	if _, err := rand.Read(keyBytes[:]); err != nil {
		return nil, fmt.Errorf("create Restart Manager session key: %w", err)
	}
	key, err := syscall.UTF16PtrFromString(hex.EncodeToString(keyBytes[:]))
	if err != nil {
		return nil, fmt.Errorf("encode Restart Manager session key: %w", err)
	}

	var session uint32
	result, _, _ := rmStartSession.Call(uintptr(unsafe.Pointer(&session)), 0, uintptr(unsafe.Pointer(key)))
	if result != 0 {
		return nil, fmt.Errorf("start Restart Manager session: %w", syscall.Errno(result))
	}
	defer func() { _, _, _ = rmEndSession.Call(uintptr(session)) }()

	fileNames := []*uint16{fileName}
	result, _, _ = rmRegisterResources.Call(
		uintptr(session), 1, uintptr(unsafe.Pointer(&fileNames[0])), 0, 0, 0, 0,
	)
	if result != 0 {
		return nil, fmt.Errorf("register file with Restart Manager: %w", syscall.Errno(result))
	}

	for attempt := 0; attempt < rmMaxListTries; attempt++ {
		var needed, count, rebootReasons uint32
		result, _, _ = rmGetList.Call(uintptr(session), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&rebootReasons)))
		if result != 0 && syscall.Errno(result) != rmErrorMoreData {
			return nil, fmt.Errorf("query Restart Manager process count: %w", syscall.Errno(result))
		}
		if needed == 0 {
			return []FileProcess{}, nil
		}

		processes := make([]rmProcessInfo, needed)
		count = needed
		result, _, _ = rmGetList.Call(
			uintptr(session), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&count)),
			uintptr(unsafe.Pointer(&processes[0])), uintptr(unsafe.Pointer(&rebootReasons)),
		)
		if syscall.Errno(result) == rmErrorMoreData {
			continue
		}
		if result != 0 {
			return nil, fmt.Errorf("list Restart Manager processes: %w", syscall.Errno(result))
		}

		out := make([]FileProcess, 0, count)
		for _, process := range processes[:count] {
			out = append(out, FileProcess{PID: process.process.processID, Name: decodeRMName(process.appName[:])})
		}
		return out, nil
	}
	return nil, errors.New("Restart Manager process list kept changing")
}

func decodeRMName(encoded []uint16) string {
	for i, r := range encoded {
		if r == 0 {
			encoded = encoded[:i]
			break
		}
	}
	return syscall.UTF16ToString(encoded)
}
