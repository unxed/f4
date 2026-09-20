//go:build !windows

package vfs

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSudoRemoteErrorKeepsTheErrnoOfTheText(t *testing.T) {
	for _, tc := range []struct {
		errno  syscall.Errno
		target error
	}{
		{syscall.ENOENT, fs.ErrNotExist},
		{syscall.EEXIST, fs.ErrExist},
		{syscall.EACCES, fs.ErrPermission},
		{syscall.EPERM, fs.ErrPermission},
	} {
		err := newSudoRemoteError("stat /root/x: " + tc.errno.Error())
		if !errors.Is(err, tc.target) {
			t.Errorf("%q is not %v", err, tc.target)
		}
		var got syscall.Errno
		if !errors.As(err, &got) || got != tc.errno {
			t.Errorf("%q carries errno %v, want %v", err, got, tc.errno)
		}
		if err.Error() != "stat /root/x: "+tc.errno.Error() {
			t.Errorf("the text changed: %q", err)
		}
	}
	if err := newSudoRemoteError("cannot open special file"); errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		t.Errorf("a text without an errno was given one: %v", err)
	}
}

// answeringDispatcher serves one connection and answers every request with
// answer.
func answeringDispatcher(t *testing.T, answer string) *SudoClient {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "d.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		conn, err := l.AcceptUnix()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			var req SudoRequest
			if _, err := recvMsg(conn, &req); err != nil {
				return
			}
			if err := sendMsg(conn, SudoResponse{Error: answer}, -1); err != nil {
				return
			}
		}
	}()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &SudoClient{conn: conn}
}

// The folder can be read only with sudo, and the file that is about to be
// copied into it is not there yet: asking for it must say "does not exist" and
// not "permission denied", or the copy stops before it creates the file (#1255).
func TestOSVFSLookupInAFolderThatNeedsSudoReportsAMissingName(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root is refused nothing, so there is nothing for sudo to do")
	}
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	target := filepath.Join(locked, "new.bin")

	old := globalSudoClient
	t.Cleanup(func() { globalSudoClient = old })
	v := NewOSVFS(locked)

	globalSudoClient = answeringDispatcher(t, "stat "+target+": "+syscall.ENOENT.Error())
	if _, err := v.Stat(context.Background(), target); !os.IsNotExist(err) {
		t.Errorf("Stat = %v, want a missing name", err)
	}
	if _, err := v.Lstat(context.Background(), target); !os.IsNotExist(err) {
		t.Errorf("Lstat = %v, want a missing name", err)
	}
	if _, err := v.Open(context.Background(), target); !os.IsNotExist(err) {
		t.Errorf("Open = %v, want a missing name", err)
	}

	globalSudoClient = answeringDispatcher(t, "stat "+target+": "+syscall.EACCES.Error())
	if _, err := v.Stat(context.Background(), target); !os.IsPermission(err) {
		t.Errorf("Stat = %v, want the refusal to stay a refusal", err)
	}
}
