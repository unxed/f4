//go:build windows

package vfs

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// LaunchElevatedDispatcher asks UAC to start exe as the elevated dispatcher
// for sock and token (f4#1768). It returns once the process is started; the
// caller then connects with DialElevated, which is where a refused prompt
// shows up as a dispatcher that never listens.
func LaunchElevatedDispatcher(exe, sock, token string) error {
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{ElevatedDispatcherFlag, sock, token}))
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, verb, file, params, nil, windows.SW_HIDE); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New("UAC elevation was canceled")
		}
		return fmt.Errorf("start the elevated dispatcher: %w", err)
	}
	return nil
}
