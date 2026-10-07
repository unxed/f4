package vfs

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

// ElevatedDispatcherFlag starts f4 as the elevated dispatcher of f4#1768:
// `f4 --elevated-dispatcher <socket> <token>`. The f4 that needs administrator
// rights launches it through UAC (LaunchElevatedDispatcher) and is its only
// client.
const ElevatedDispatcherFlag = "--elevated-dispatcher"

// ErrElevationLaunchUnsupported is what LaunchElevatedDispatcher answers where
// there is no UAC to ask.
var ErrElevationLaunchUnsupported = errors.New("launching an elevated dispatcher is supported on Windows only")

// ParseElevatedDispatcherArgs finds the dispatcher invocation in args. Like
// the updater's helper flags it is accepted only as the complete tail of the
// command line, so a user argument that merely looks like it cannot turn a
// normal start into a dispatcher.
func ParseElevatedDispatcherArgs(args []string) (sock, token string, found bool, err error) {
	for i, arg := range args {
		if arg != ElevatedDispatcherFlag {
			continue
		}
		if len(args) != i+3 {
			return "", "", true, fmt.Errorf("%s requires a socket path and a token", ElevatedDispatcherFlag)
		}
		// A fixed array keeps the length check above visible to gosec.
		var tail [2]string
		copy(tail[:], args[i+1:])
		sock, token = strings.TrimSpace(tail[0]), strings.TrimSpace(tail[1])
		if sock == "" || len(token) != 2*sudoTokenBytes {
			return "", "", true, fmt.Errorf("%s requires a socket path and a %d-character token", ElevatedDispatcherFlag, 2*sudoTokenBytes)
		}
		return sock, token, true, nil
	}
	return "", "", false, nil
}

// RunElevatedDispatcher listens on sock and serves the one client that
// presents token (ServeElevated), then removes the socket. handle answers the
// file operations; with nil only CmdPing is answered.
func RunElevatedDispatcher(sock, token string, handle func(SudoRequest) SudoResponse) error {
	// A socket left by an earlier dispatcher would make Listen fail; the path
	// is the one the client just chose for this session.
	if err := os.Remove(sock); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale dispatcher socket: %w", err)
	}
	addr, err := net.ResolveUnixAddr("unix", sock)
	if err != nil {
		return err
	}
	l, err := net.ListenUnix("unix", addr)
	if err != nil {
		return fmt.Errorf("listen for the elevated client: %w", err)
	}
	defer func() {
		_ = l.Close()
		_ = os.Remove(sock)
	}()
	return ServeElevated(l, token, handle)
}
