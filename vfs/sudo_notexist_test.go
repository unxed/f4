//go:build !windows

package vfs

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	sock := filepath.Join(shortSocketDir(t), "d.sock") // unix socket paths are short on macOS
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

// Shift+F6 in a folder that needs sudo renames without replacing, and that
// path had no elevated fallback (#1255): the request has to reach the
// dispatcher and keep its no-replace meaning there.
func TestSudoClientRenameNoReplaceGoesThroughTheDispatcher(t *testing.T) {
	dir := shortSocketDir(t)
	addr, err := net.ResolveUnixAddr("unix", filepath.Join(dir, "d.sock"))
	if err != nil {
		t.Fatal(err)
	}
	listener := listenUnixForTest(t, addr)
	defer func() { _ = listener.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if conn, err := listener.AcceptUnix(); err == nil {
			handleSudoClient(conn)
		}
	}()
	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	client := &SudoClient{conn: conn}
	defer func() {
		_ = conn.Close()
		<-done
	}()

	write := func(name, data string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a, b := write("a.txt", "A"), write("b.txt", "B")

	if err := client.RenameNoReplace(a, b); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("renaming onto an existing name = %v, want ErrDestinationExists", err)
	}
	if got, _ := os.ReadFile(b); string(got) != "B" {
		t.Fatalf("b.txt was replaced: %q", got)
	}

	c := filepath.Join(dir, "c.txt")
	if err := client.RenameNoReplace(a, c); err != nil {
		t.Fatalf("renaming to a free name: %v", err)
	}
	if got, _ := os.ReadFile(c); string(got) != "A" {
		t.Fatalf("c.txt = %q, want the content of a.txt", got)
	}
	if _, err := os.Lstat(a); !os.IsNotExist(err) {
		t.Fatalf("a.txt is still there: %v", err)
	}
}

func TestSudoStderrKeepsWhatSudoSaid(t *testing.T) {
	s := &sudoStderr{}
	_, _ = s.Write([]byte("SUDO_DISPATCHER: STARTING\n[sudo] password for ann: \nSorry, try again.\nann is not in the sudoers"))
	_, _ = s.Write([]byte(" file.  This incident will be reported.\n"))
	got := s.Reason()
	if strings.Contains(got, "SUDO_DISPATCHER") {
		t.Fatalf("the dispatcher's notes were kept: %q", got)
	}
	if !strings.Contains(got, "ann is not in the sudoers file.  This incident will be reported.") {
		t.Fatalf("reason = %q", got)
	}
}

func TestSudoExitedErrorExplainsWhySudoGaveUp(t *testing.T) {
	if got := sudoExitedError("").Error(); got != "sudo process exited prematurely" {
		t.Fatalf("no reason: %q", got)
	}
	notAllowed := sudoExitedError("ann is not in the sudoers file.").Error()
	if !strings.Contains(notAllowed, "not available to this user") || !strings.Contains(notAllowed, "not in the sudoers") {
		t.Fatalf("a user who may not use sudo: %q", notAllowed)
	}
	if other := sudoExitedError("sudo: 3 incorrect password attempts").Error(); !strings.Contains(other, "incorrect password attempts") {
		t.Fatalf("other reasons are kept: %q", other)
	}
}
