//go:build !windows

package terminal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/unxed/f4/internal/testutil"
)

func TestSessionDir_Isolation(t *testing.T) {
	dir := sessionDir()
	expectedSuffix := fmt.Sprintf("f4-sessions-%d", os.Getuid())

	if filepath.Base(dir) != expectedSuffix {
		t.Errorf("sessionDir() = %q; want suffix %q", dir, expectedSuffix)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("sessionDir was not created: %v", err)
	}

	if info.Mode().Perm() != 0700 {
		t.Errorf("sessionDir permissions = %v; want 0700", info.Mode().Perm())
	}
}

func TestSessionPickerDialogWidth(t *testing.T) {
	tests := []struct {
		screenWidth int
		want        int
	}{
		{screenWidth: 120, want: 100},
		{screenWidth: 80, want: 80},
		{screenWidth: 0, want: 100},
	}
	for _, tt := range tests {
		if got := sessionPickerDialogWidth(tt.screenWidth); got != tt.want {
			t.Errorf("sessionPickerDialogWidth(%d) = %d, want %d", tt.screenWidth, got, tt.want)
		}
	}
}

// TestClearNonBlock_ClearsFlag guards the OpenBSD 7.5+ regression described
// in PORTABILITY_BSD.md, 4.4: the original code called
// syscall.Syscall(syscall.SYS_FCNTL, ...) directly, an indirect syscall that
// OpenBSD 7.5+ no longer supports (golang/go#63900) — it returns ENOSYS and
// silently leaves O_NONBLOCK set. clearNonBlock instead goes through
// unix.FcntlInt, which uses the libc fcntl(2) stub and keeps working there.
//
// This test can't reproduce the ENOSYS behavior itself (that only happens on
// real OpenBSD 7.5+), but it pins down the contract clearNonBlock must
// satisfy everywhere: given a fd with O_NONBLOCK set, F_GETFL must no longer
// report it afterwards.
func TestClearNonBlock_ClearsFlag(t *testing.T) {
	var p [2]int
	if err := syscall.Pipe(p[:]); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	readEnd, writeEnd := p[0], p[1]
	t.Cleanup(func() {
		if err := syscall.Close(writeEnd); err != nil {
			t.Errorf("close pipe write end: %v", err)
		}
	})
	// os.NewFile takes ownership of readEnd. Closing the raw descriptor while
	// leaving f to its finalizer can later close an unrelated, reused fd.
	f := os.NewFile(uintptr(readEnd), "pipe-read")
	if f == nil {
		_ = syscall.Close(readEnd) // Cleanup is secondary to os.NewFile failing.
		t.Fatal("os.NewFile returned nil for pipe read end")
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close(pipe-read): %v", err)
		}
	}()

	if _, err := unix.FcntlInt(f.Fd(), unix.F_SETFL, unix.O_NONBLOCK); err != nil {
		t.Fatalf("set O_NONBLOCK: %v", err)
	}
	flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("F_GETFL: %v", err)
	}
	if flags&unix.O_NONBLOCK == 0 {
		t.Fatalf("test setup broken: O_NONBLOCK not observed as set")
	}

	clearNonBlock(f)

	flags, err = unix.FcntlInt(f.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("F_GETFL after clearNonBlock: %v", err)
	}
	if flags&unix.O_NONBLOCK != 0 {
		t.Fatalf("O_NONBLOCK still set after clearNonBlock")
	}
}

// mechanism behind the #429 investigation (PORTABILITY_BSD.md, 4.1): fds
// received via SCM_RIGHTS carry no FD_CLOEXEC, so a child process spawned
// afterwards (e.g. the built-in terminal's shell, via initPTY) inherits them
// across fork+exec unless explicitly flagged. If that child outlives the
// daemon, it keeps notifyPipe's write end open and RunClient's blocking read
// on it never returns — a daemon crash then looks like an indefinite hang
// instead of a clean, fast exit.
//
// This test proves the negative directly: a pipe write end is flagged with
// setCloseOnExec, a long-lived child is forked, our own copy of the write
// end is closed, and the read end must then see EOF immediately — meaning
// no other process (i.e. not the child) is still holding the write end open.
// Without the CLOEXEC flag this read blocks instead, because the forked
// child holds its own copy for as long as it runs.
func TestSetCloseOnExec_NotInheritedByChild(t *testing.T) {
	var p [2]int
	if err := syscall.Pipe(p[:]); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	readEnd, writeEnd := p[0], p[1]
	t.Cleanup(func() {
		if err := syscall.Close(readEnd); err != nil {
			t.Errorf("close pipe read end: %v", err)
		}
	})

	setCloseOnExec([]int{writeEnd})

	// A child that outlives this test's assertions if it inherited writeEnd.
	proc, err := os.StartProcess("/bin/sleep", []string{"sleep", "5"}, &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	defer func() {
		_ = proc.Kill()    // The cleanup child may have already exited.
		_, _ = proc.Wait() // Waiting only reaps the cleanup child.
	}()

	// We are now the only process that should hold writeEnd open. Closing it
	// must make the read end observe EOF right away.
	if err := syscall.Close(writeEnd); err != nil {
		t.Fatalf("close(writeEnd): %v", err)
	}

	if _, err := unix.FcntlInt(uintptr(readEnd), unix.F_SETFL, unix.O_NONBLOCK); err != nil {
		t.Fatalf("set O_NONBLOCK on readEnd: %v", err)
	}
	buf := make([]byte, 1)
	n, err := syscall.Read(readEnd, buf)
	if n != 0 || err != nil {
		t.Fatalf("read after close(writeEnd) = (%d, %v); want (0, nil) EOF — "+
			"a non-EOF result means the forked child still holds the write "+
			"end open, i.e. FD_CLOEXEC did not take effect", n, err)
	}
}
func TestListSessions_PurgesMissingSockets(t *testing.T) {
	dir := sessionDir()
	staleSock := filepath.Join(dir, "stale-test.sock")
	staleJSON := filepath.Join(dir, "f4-999999.json")

	info := SessionInfo{
		PID:      os.Getpid(), // Alive process, but socket is missing
		Title:    "stale session",
		SockPath: staleSock,
	}
	data, _ := json.Marshal(info)
	if err := os.WriteFile(staleJSON, data, 0600); err != nil {
		t.Fatalf("write stale json: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(staleJSON); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove stale session metadata: %v", err)
		}
	})

	sessions := listSessions()
	for _, s := range sessions {
		if s.SockPath == staleSock {
			t.Errorf("listSessions() returned session with missing socket: %+v", s)
		}
	}

	if _, err := os.Stat(staleJSON); !os.IsNotExist(err) {
		t.Errorf("listSessions() did not purge stale json file for missing socket")
	}
}

func TestWatchdog_DetectsClientDisconnect(t *testing.T) {
	var p [2]int
	if err := syscall.Pipe(p[:]); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	readEnd, writeEnd := p[0], p[1]
	t.Cleanup(func() {
		if err := syscall.Close(writeEnd); err != nil {
			t.Errorf("close pipe write end: %v", err)
		}
	})

	// Initially, both ends are open: poll should not report hangup/error.
	// POLLOUT must be requested: macOS reports nothing for Events: 0, so
	// polling with an empty event mask never detects the closed read end
	// (the watchdog bug this test guards against).
	pfds := []unix.PollFd{{Fd: testutil.Int32(writeEnd), Events: unix.POLLOUT}}
	_, err := unix.Poll(pfds, 0)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if (pfds[0].Revents & (unix.POLLERR | unix.POLLHUP | unix.POLLNVAL)) != 0 {
		t.Fatalf("unexpected revents on open pipe: %x", pfds[0].Revents)
	}

	// Close client read end
	if err := syscall.Close(readEnd); err != nil {
		t.Fatal(err)
	}

	// Now poll on writeEnd must report POLLERR, POLLHUP, or POLLNVAL
	pfds = []unix.PollFd{{Fd: testutil.Int32(writeEnd), Events: unix.POLLOUT}}
	_, err = unix.Poll(pfds, 0)
	if err != nil {
		t.Fatalf("poll after close: %v", err)
	}
	if (pfds[0].Revents & (unix.POLLERR | unix.POLLHUP | unix.POLLNVAL)) == 0 {
		t.Fatalf("watchdog failed to detect closed read end: revents = %x", pfds[0].Revents)
	}
}

// A client that has just attached is a terminal the daemon has never spoken
// to: whatever the previous client acknowledged says nothing about this one,
// so the far2l state is dropped before the protocols are announced again
// (#922).
func TestAdoptClientTerminal_ForgetsFar2lNegotiation(t *testing.T) {
	calls := 0
	oldReset := resetFar2lNegotiation
	resetFar2lNegotiation = func() { calls++ }
	t.Cleanup(func() { resetFar2lNegotiation = oldReset })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	if restore := adoptClientTerminal(r); restore != nil {
		t.Error("a pipe has no raw mode to restore")
	}
	if calls != 1 {
		t.Fatalf("far2l negotiation was forgotten %d times, want once", calls)
	}
}

// startFakeDaemon spawns a stand-in for a session daemon: a process whose
// argv carries "--server" and the socket path (the identity stopSession
// checks), living in its own session so its process group id equals its pid,
// just like startNewSession's Setsid does. The command must not exec-replace
// itself — an exec'd "sleep 30" would drop the extra argv and break the
// identity check under test — so the script blocks on reading its stdin,
// which the returned closer keeps open.
func startFakeDaemon(t *testing.T, script string, args ...string) (*exec.Cmd, <-chan struct{}, *error) {
	t.Helper()
	argv := append([]string{"/bin/sh", "-c", script}, args...)
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- test helper, fixed "/bin/sh -c" plus this test's own literal script/args, no untrusted input
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake daemon: %v", err)
	}
	// Reap asynchronously: stopSession treats a zombie as exited, and cmd.Wait
	// is what actually reaps our child. The channel is closed rather than
	// sent on so both the test and t.Cleanup can wait for the same exit.
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
		_ = stdin.Close()
	})
	return cmd, done, &waitErr
}

func writeSessionFiles(t *testing.T, info SessionInfo) (jsonPath, startupPath, sudoPath, apPath string) {
	t.Helper()
	dir := sessionDir()
	jsonPath = filepath.Join(dir, fmt.Sprintf("f4-%d.json", info.PID))
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(info.SockPath, []byte("socket placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	startupPath = info.SockPath + ".startup"
	if err := os.WriteFile(startupPath, []byte("startup log"), 0600); err != nil {
		t.Fatal(err)
	}
	sudoPath = filepath.Join(os.TempDir(), fmt.Sprintf("f4-sudo-%d.sock", info.PID))
	apPath = filepath.Join(os.TempDir(), fmt.Sprintf("f4-ap-%d.sock", info.PID))
	for _, p := range []string{sudoPath, apPath} {
		if err := os.WriteFile(p, []byte("sock placeholder"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, p := range []string{jsonPath, info.SockPath, startupPath, sudoPath, apPath} {
			_ = os.Remove(p)
		}
	})
	return jsonPath, startupPath, sudoPath, apPath
}

// stopSession must terminate a daemon that presents the recorded identity and
// then drop every file that described it. Killing happens before removal: the
// daemon rewrites its json on every accept-loop iteration, so removing files
// while it still runs would race a rewrite.
func TestStopSession_TerminatesDaemonAndRemovesFiles(t *testing.T) {
	if _, err := os.Stat("/proc/self/cmdline"); err != nil {
		t.Skip("no procfs; identity check falls back to socket existence")
	}

	sockPath := filepath.Join(sessionDir(), fmt.Sprintf("f4-stop-%d.sock", time.Now().UnixNano()))
	cmd, done, waitErr := startFakeDaemon(t, "read x", "--server", sockPath)
	info := SessionInfo{PID: cmd.Process.Pid, Title: "fake", SockPath: sockPath}
	jsonPath, startupPath, sudoPath, apPath := writeSessionFiles(t, info)

	stopAllSessions([]SessionInfo{info})

	select {
	case <-done:
		if *waitErr == nil {
			t.Error("fake daemon exited cleanly; want killed by signal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake daemon still running after stopAllSessions")
	}

	for name, path := range map[string]string{
		"session json": jsonPath,
		"socket":       info.SockPath,
		"startup log":  startupPath,
		"sudo socket":  sudoPath,
		"askpass sock": apPath,
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("stopAllSessions left %s %s (err=%v)", name, path, err)
		}
	}
}

// A session file can outlive the pid it records; that pid may then belong to
// an unrelated process. stopSession must refuse to signal it (cmdline lacks
// "--server" and the recorded socket) while still clearing the stale files.
func TestStopSession_RefusesUnrelatedProcessStillRemovesFiles(t *testing.T) {
	if _, err := os.Stat("/proc/self/cmdline"); err != nil {
		t.Skip("no procfs; identity check falls back to socket existence")
	}

	sockPath := filepath.Join(sessionDir(), fmt.Sprintf("f4-refuse-%d.sock", time.Now().UnixNano()))
	cmd, done, waitErr := startFakeDaemon(t, "read x") // argv has no --server / socket path
	info := SessionInfo{PID: cmd.Process.Pid, Title: "not a daemon", SockPath: sockPath}
	jsonPath, startupPath, sudoPath, apPath := writeSessionFiles(t, info)

	stopAllSessions([]SessionInfo{info})

	select {
	case <-done:
		t.Fatalf("stopAllSessions killed an unrelated process (%v)", *waitErr)
	case <-time.After(300 * time.Millisecond):
		// Still running, as it must be.
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("unrelated process is no longer signalable: %v", err)
	}
	for name, path := range map[string]string{
		"session json": jsonPath,
		"socket":       info.SockPath,
		"startup log":  startupPath,
		"sudo socket":  sudoPath,
		"askpass sock": apPath,
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("stopAllSessions left stale %s %s (err=%v)", name, path, err)
		}
	}
}

// The picker's own pid must never be signalled: it is the f4 process showing
// the dialog, not a daemon, and killing it would take down the UI mid-click.
// Its metadata still has to go so the next picker does not list it.
func TestStopSession_NeverKillsOwnPidButRemovesItsFiles(t *testing.T) {
	sockPath := filepath.Join(sessionDir(), fmt.Sprintf("f4-self-%d.sock", time.Now().UnixNano()))
	info := SessionInfo{PID: os.Getpid(), Title: "picker", SockPath: sockPath}
	jsonPath, startupPath, sudoPath, apPath := writeSessionFiles(t, info)

	stopAllSessions([]SessionInfo{info})

	// If stopSession had signalled us, the test process would already be gone.
	if !isProcessAlive(os.Getpid()) {
		t.Fatal("stopAllSessions signalled the test process itself")
	}
	for name, path := range map[string]string{
		"session json": jsonPath,
		"socket":       info.SockPath,
		"startup log":  startupPath,
		"sudo socket":  sudoPath,
		"askpass sock": apPath,
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("stopAllSessions left %s %s (err=%v)", name, path, err)
		}
	}
}

func TestSessionProcessMatches_IdentityRules(t *testing.T) {
	sockPath := filepath.Join(sessionDir(), fmt.Sprintf("f4-ident-%d.sock", time.Now().UnixNano()))
	if err := os.WriteFile(sockPath, []byte("socket placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(sockPath) })

	if sessionProcessMatches(SessionInfo{PID: 0, SockPath: sockPath}) {
		t.Error("pid 0 matched")
	}
	if sessionProcessMatches(SessionInfo{PID: 1, SockPath: sockPath}) {
		t.Error("pid 1 (init) matched")
	}
	if sessionProcessMatches(SessionInfo{PID: os.Getpid(), SockPath: sockPath}) {
		t.Error("own pid matched")
	}
	if sessionProcessMatches(SessionInfo{PID: -1, SockPath: sockPath}) {
		t.Error("negative pid matched")
	}
	if sessionProcessMatches(SessionInfo{PID: 0xDEAD, SockPath: sockPath}) {
		t.Error("dead pid matched")
	}

	// Live process whose argv lacks the identity: must not match. (Own pid
	// short-circuits earlier, so this needs a separate process.)
	if _, err := os.Stat("/proc/self/cmdline"); err == nil {
		stranger, _, _ := startFakeDaemon(t, "read x")
		if sessionProcessMatches(SessionInfo{PID: stranger.Process.Pid, SockPath: sockPath}) {
			t.Error("matched a process whose cmdline does not name the socket")
		}
		// And with the identity present, the same check must say yes —
		// otherwise stopSession could never kill a real daemon either.
		daemon, _, _ := startFakeDaemon(t, "read x", "--server", sockPath)
		// Small grace for /proc/<pid>/cmdline to become readable under heavy
		// parallel test load; failure here means the identity check itself is
		// broken, not a timing issue.
		var matched bool
		for i := 0; i < 20; i++ {
			if sessionProcessMatches(SessionInfo{PID: daemon.Process.Pid, SockPath: sockPath}) {
				matched = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !matched {
			t.Error("did not match a daemon whose cmdline names --server and the socket")
		}
	}
}
