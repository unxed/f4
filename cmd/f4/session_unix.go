//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type SessionInfo struct {
	PID      int
	Title    string
	SockPath string
}

func SupportsBackgrounding() bool {
	return true
}

func sessionDir() string {
	uid := os.Getuid()
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("f4-sessions-%d", uid))
	os.MkdirAll(dir, 0700)
	return dir
}

func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

func listSessions() []SessionInfo {
	dir := sessionDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var sessions []SessionInfo
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}

			var info SessionInfo
			if err := json.Unmarshal(data, &info); err == nil {
				_, statErr := os.Stat(info.SockPath)
				if isProcessAlive(info.PID) && statErr == nil {
					sessions = append(sessions, info)
				} else {
					os.Remove(path)
					os.Remove(info.SockPath)
				}
			}
		}
	}
	return sessions
}

func writeSessionInfo(sockPath string) {
	pid := os.Getpid()
	infoPath := filepath.Join(sessionDir(), fmt.Sprintf("f4-%d.json", pid))
	title := "f4"
	if top := vtui.FrameManager.GetTopFrame(); top != nil {
		title = top.GetTitle()
	}
	info := SessionInfo{
		PID:      pid,
		Title:    title,
		SockPath: sockPath,
	}
	data, _ := json.Marshal(info)
	os.WriteFile(infoPath, data, 0600)
}

func removeSessionInfo(sockPath string) {
	pid := os.Getpid()
	infoPath := filepath.Join(sessionDir(), fmt.Sprintf("f4-%d.json", pid))
	os.Remove(infoPath)
	os.Remove(sockPath)
}

func ManageSessions() {
	if len(os.Args) > 1 && os.Args[1] == "--server" {
		runServer(os.Args[2])
		return
	}

	if os.Getenv("F4_NESTED") != "" {
		startNewSession()
		return
	}

	sessions := listSessions()
	if len(sessions) > 0 {
		// -e skips the interactive picker: `f4 -e file` should just work
		// non-interactively, reusing whatever session is already running
		// (matching far2l's -e), not stop to ask which one first.
		if editFilePath != "" {
			runClient(sessions[0].SockPath, sessions[0].PID)
			return
		}
		selected := runSessionPicker(sessions)
		if selected != nil {
			if selected.PID == 0 {
				startNewSession()
			} else {
				runClient(selected.SockPath, selected.PID)
			}
			return
		} else {
			return // Picker cancelled
		}
	}
	startNewSession()
}

func startNewSession() {
	pid := os.Getpid()
	sockPath := filepath.Join(sessionDir(), fmt.Sprintf("f4-new-%d-%d.sock", pid, time.Now().Unix()))
	vtui.DebugLog("SESSION: Starting new daemon server at %s", sockPath)

	cmd := selfCommand(os.Args[0], "--server", sockPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // Detach from terminal

	// Crucial for GUI startup: redirect daemon's own I/O to null so it doesn't
	// hold the parent's pipe/TTY.
	null, _ := os.Open(os.DevNull)
	cmd.Stdin = null
	cmd.Stdout = null

	// stderr is a file rather than /dev/null, because the interesting failures
	// happen before the daemon has any logging of its own: a missing shared
	// object, a libc symbol that did not bind, a panic in InitCore. The daemon
	// replaces this descriptor with its own crash log in SetupStderrLog moments
	// later, so what lands here is exactly the pre-startup output that used to
	// be discarded. It is deleted once the daemon is up.
	startupLog := sockPath + ".startup"
	// #nosec G304 -- path is built from the session directory and this pid.
	if f, err := os.OpenFile(startupLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); err == nil {
		cmd.Stderr = f
		defer f.Close()
	} else {
		cmd.Stderr = null
		startupLog = ""
	}

	if err := cmd.Start(); err != nil {
		vtui.DebugLog("SESSION: CRITICAL: Failed to spawn daemon process (path: %s): %v", cmd.Path, err)
		if null != nil {
			null.Close()
		}
		removeStartupLog(startupLog)
		fmt.Println("Failed to start daemon:", err)
		return
	}
	vtui.DebugLog("SESSION: Daemon spawned successfully (PID: %d). Attaching client.", cmd.Process.Pid)
	if null != nil {
		null.Close()
	}

	// Wait for the server to create the socket, and refuse to attach to one
	// that never appeared. Going ahead regardless is how a daemon that died at
	// startup used to surface: WriteMsgUnix to a socket nobody bound fails with
	// ENOENT, and "sendmsg: no such file or directory" reads as a bug in f4's
	// IPC rather than as "the daemon is not running". Waiting on the process
	// too means the usual case -- a daemon that is already gone -- is answered
	// at once instead of at the end of the timeout.
	if !waitForDaemonSocket(cmd, sockPath) {
		fmt.Fprintf(os.Stderr, "f4: the session daemon did not start: it never created %s\n", sockPath)
		if out := readStartupLog(startupLog); out != "" {
			fmt.Fprintf(os.Stderr, "f4: what the daemon said before it died:\n%s\n", out)
		}
		removeStartupLog(startupLog)
		return
	}
	removeStartupLog(startupLog)

	// The daemon's pid is known here. Letting runClient look it up instead
	// would race: it resolves the pid through listSessions(), i.e. through
	// the json that runServer writes *after* creating the socket, while the
	// loop above waits only for the socket itself. Losing that race is
	// silent — serverPID stays 0 and SIGWINCH is simply never forwarded, so
	// a fresh session never learns that the terminal was resized.
	runClient(sockPath, cmd.Process.Pid)
}

// daemonStartTimeout bounds the wait for a daemon that is neither up nor dead:
// one that hangs in startup, or a machine slow enough that a cold binary takes
// its time. Generous because nothing is lost by waiting -- a daemon that
// exits is noticed the moment it does, not when this runs out.
const daemonStartTimeout = 10 * time.Second

// waitForDaemonSocket reports whether the daemon bound sockPath. It gives up
// as soon as the process is gone, and reaps it either way: cmd.Wait here is
// also what keeps a daemon that outlives this client from being left a zombie
// for the rest of the session.
func waitForDaemonSocket(cmd *exec.Cmd, sockPath string) bool {
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	deadline := time.Now().Add(daemonStartTimeout)
	for {
		if _, err := os.Stat(sockPath); err == nil {
			return true
		}
		select {
		case <-exited:
			// One more look: the daemon may have bound the socket and quit
			// between the Stat above and this check.
			_, err := os.Stat(sockPath)
			if err != nil {
				vtui.DebugLog("SESSION: daemon %d exited without creating %s", cmd.Process.Pid, sockPath)
			}
			return err == nil
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			vtui.DebugLog("SESSION: daemon %d did not create %s within %s", cmd.Process.Pid, sockPath, daemonStartTimeout)
			return false
		}
	}
}

// readStartupLog returns what the daemon wrote before it had logging of its
// own, trimmed to something a terminal can show. Empty when it said nothing,
// which is itself the answer for a daemon killed by a signal.
func readStartupLog(path string) string {
	if path == "" {
		return ""
	}
	// #nosec G304 -- path is the one startNewSession created next to the socket.
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	const maxStartupLog = 4 << 10
	if len(data) > maxStartupLog {
		data = data[len(data)-maxStartupLog:]
	}
	return strings.TrimRight(string(data), "\n")
}

func removeStartupLog(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

func runClient(sockPath string, serverPID int) {
	vtui.DebugLog("CLIENT: Start runClient, target socket: %s", sockPath)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		vtui.DebugLog("CLIENT: WARNING: os.Stdin (fd %d) is not a terminal!", os.Stdin.Fd())
	}

	if serverPID <= 0 {
		for _, s := range listSessions() {
			if s.SockPath == sockPath {
				serverPID = s.PID
				break
			}
		}
	}
	if serverPID > 0 {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGWINCH)
		go func() {
			for range sigChan {
				syscall.Kill(serverPID, syscall.SIGWINCH)
			}
		}()
	}

	clntPath := filepath.Join(sessionDir(), fmt.Sprintf("c-%d-%x.ipc", os.Getpid(), time.Now().UnixNano()&0xFFFFFFFF))
	laddr, _ := net.ResolveUnixAddr("unixgram", clntPath)

	conn, err := net.ListenUnixgram("unixgram", laddr)
	if err != nil {
		vtui.DebugLog("CLIENT: CRITICAL: Failed to create client socket: %v", err)
		fmt.Fprintf(os.Stderr, "f4: failed to create IPC socket: %v\n", err)
		return
	}
	defer os.Remove(clntPath)
	defer conn.Close()

	raddr, _ := net.ResolveUnixAddr("unixgram", sockPath)

	notifyPipe := make([]int, 2)
	if err := syscall.Pipe(notifyPipe); err != nil {
		vtui.DebugLog("CLIENT: CRITICAL: Failed to create notify pipe: %v", err)
		fmt.Fprintf(os.Stderr, "f4: failed to create notify pipe: %v\n", err)
		return
	}
	defer syscall.Close(notifyPipe[0])

	oob := syscall.UnixRights(0, 1, notifyPipe[1])
	vtui.DebugLog("CLIENT: FDs to send: In:0 Out:1 Pipe:%d", notifyPipe[1])

	startLeft, startRight := startupDirs()
	n, oobn, err := conn.WriteMsgUnix(attachPayload(editFilePath, startLeft, startRight), oob, raddr)
	if err != nil {
		vtui.DebugLog("CLIENT: ATTACH FAILURE: Failed to send FDs to daemon at %s: %v", sockPath, err)
		fmt.Fprintf(os.Stderr, "f4: failed to attach to session at %s: %v\n", sockPath, err)
		syscall.Close(notifyPipe[1])
		return
	}
	vtui.DebugLog("CLIENT: FDs transmitted (sent %d bytes, %d oob). Relinquishing terminal control.", n, oobn)
	syscall.Close(notifyPipe[1])

	vtui.DebugLog("CLIENT: Waiting for server signal on pipe %d...", notifyPipe[0])
	dummy := make([]byte, 1)
	var nRead int
	for {
		nRead, err = syscall.Read(notifyPipe[0], dummy)
		// Go installs its signal handlers with SA_RESTART, so this is rare
		// rather than impossible. Returning on EINTR would hand the shell
		// its prompt back while the daemon still owns the terminal and is
		// drawing into it.
		if err == syscall.EINTR {
			continue
		}
		break
	}
	vtui.DebugLog("CLIENT: Server released pipe. nRead=%d, err=%v", nRead, err)
}

// setCloseOnExec marks each fd FD_CLOEXEC. Used on descriptors received over
// SCM_RIGHTS, which the kernel never flags this way on their own — see the
// call site in runServer for why that matters. Failures are logged, not
// fatal: the attach can still proceed, just without the safety net.
func setCloseOnExec(fds []int) {
	for _, fd := range fds {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
			vtui.DebugLog("SERVER: WARNING: failed to set FD_CLOEXEC on fd %d: %v", fd, err)
		}
	}
}

// clearNonBlock clears O_NONBLOCK on f, going through the libc fcntl(2) stub
// via unix.FcntlInt rather than a raw syscall — see the call site in
// runServer for why that distinction matters on OpenBSD. Failures are
// logged, not fatal.
func clearNonBlock(f *os.File) {
	flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFL, 0)
	if err != nil {
		vtui.DebugLog("SERVER: WARNING: F_GETFL on fd %d failed: %v", f.Fd(), err)
		return
	}
	if _, err := unix.FcntlInt(f.Fd(), unix.F_SETFL, flags&^unix.O_NONBLOCK); err != nil {
		vtui.DebugLog("SERVER: WARNING: F_SETFL (clear O_NONBLOCK) on fd %d failed: %v", f.Fd(), err)
	}
}

type attachRequest struct {
	in                 *os.File
	out                *os.File
	notifyPipeWriteEnd int
	rawFds             []int
	editPath           string
	startLeft          string
	startRight         string
}

// attachPayload builds the ATTACH datagram body. Plain "ATTACH" when no
// file was requested (preserves the exact wire format every existing f4
// binary already speaks, so an old client can still attach to a new
// server and vice versa); "ATTACH <path>" when -e named a file, parsed
// back out in runServer's accept loop below.
//
// The panel directories ride on further lines, so an older server still reads
// exactly "ATTACH" on the first. They are left out next to -e, where that
// server would take the whole datagram as the file name.
func attachPayload(editPath, left, right string) []byte {
	if editPath != "" {
		return []byte("ATTACH " + editPath)
	}
	if left == "" {
		return []byte("ATTACH")
	}
	msg := "ATTACH\nCWD " + left
	if right != "" && right != left {
		msg += "\nCWD2 " + right
	}
	return []byte(msg)
}

// parseAttachPayload reads back what attachPayload wrote. Unknown lines are
// ignored rather than refused, so a future client stays attachable.
func parseAttachPayload(msg string) (editPath, left, right string) {
	lines := strings.Split(msg, "\n")
	if strings.HasPrefix(lines[0], "ATTACH ") {
		editPath = lines[0][len("ATTACH "):]
	}
	for _, line := range lines[1:] {
		switch {
		case strings.HasPrefix(line, "CWD2 "):
			right = line[len("CWD2 "):]
		case strings.HasPrefix(line, "CWD "):
			left = line[len("CWD "):]
		}
	}
	return editPath, left, right
}

func runServer(sockPath string) {
	vtui.DebugLog("SERVER: Starting daemon at %s", sockPath)

	// Prevent the server from dying if the terminal drops
	signal.Ignore(syscall.SIGPIPE)
	signal.Ignore(syscall.SIGHUP)
	signal.Ignore(syscall.SIGTTOU)
	signal.Ignore(syscall.SIGTTIN)

	scr := InitCore()

	addr, _ := net.ResolveUnixAddr("unixgram", sockPath)
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		vtui.DebugLog("SERVER: Listen error: %v", err)
		return
	}
	defer conn.Close()

	writeSessionInfo(sockPath)
	defer removeSessionInfo(sockPath)

	attachChan := make(chan attachRequest, 16)

	// Acceptor goroutine: continuously reads ATTACH datagrams so incoming
	// clients are received even while FrameManager.Run() is executing.
	// Read on the goroutine that starts this work, not inside it: the
	// work outlives the call, and reading the global from it races
	// anything that reassigns vtui.FrameManager meanwhile.
	frames := vtui.FrameManager
	go func() {
		for {
			// 4096 (not the old 32-byte "ATTACH"-only size) so "ATTACH
			// <path>" fits a real filesystem path -- see attachPayload().
			buf := make([]byte, 4096)
			oob := make([]byte, 1024)

			n, oobn, _, from, err := conn.ReadMsgUnix(buf, oob)
			if err != nil {
				if strings.Contains(err.Error(), "use of closed network connection") {
					return
				}
				vtui.DebugLog("SERVER: IPC read error on %s: %v", sockPath, err)
				time.Sleep(100 * time.Millisecond)
				continue
			}

			vtui.DebugLog("SERVER: Connection received from client %v (Message: %q).", from, string(buf[:n]))

			scms, err := syscall.ParseSocketControlMessage(oob[:oobn])
			if err != nil || len(scms) == 0 {
				vtui.DebugLog("SERVER: ParseSocketControlMessage error or empty: %v", err)
				continue
			}

			fds, err := syscall.ParseUnixRights(&scms[0])
			if err != nil || len(fds) < 3 {
				vtui.DebugLog("SERVER: ParseUnixRights error or len(fds)<3 (len=%d): %v", len(fds), err)
				for _, fd := range fds {
					syscall.Close(fd)
				}
				continue
			}

			setCloseOnExec(fds)

			editPath, startLeft, startRight := parseAttachPayload(string(buf[:n]))

			req := attachRequest{
				in:                 os.NewFile(uintptr(fds[0]), "/dev/stdin"),
				out:                os.NewFile(uintptr(fds[1]), "/dev/stdout"),
				notifyPipeWriteEnd: fds[2],
				rawFds:             fds,
				editPath:           editPath,
				startLeft:          startLeft,
				startRight:         startRight,
			}

			// Preempt the current attached session (if any) so the new client takes over.
			frames.Stop()

			attachChan <- req
		}
	}()

	vtui.DebugLog("SERVER: Daemon listener active on %s. Standing by.", sockPath)
	for {
		if vtui.FrameManager.IsShutdown() {
			vtui.DebugLog("SERVER: Shutdown requested. Exiting.")
			break
		}

		writeSessionInfo(sockPath)

		req, ok := <-attachChan
		if !ok {
			break
		}

		fds := req.rawFds
		newStdin := req.in
		newStdout := req.out
		notifyPipeWriteEnd := req.notifyPipeWriteEnd
		attachEditPath := req.editPath
		attachStartLeft, attachStartRight := req.startLeft, req.startRight

		vtui.DebugLog("SERVER: FDs received (In:%d Out:%d Pipe:%d). Goroutines: %d. Attaching terminal.", fds[0], fds[1], fds[2], runtime.NumGoroutine())

		oldStdin, oldStdout := os.Stdin, os.Stdout
		os.Stdin, os.Stdout = newStdin, newStdout

		var restore func()
		if term.IsTerminal(int(os.Stdin.Fd())) {
			r, err := vtui.PrepareTerminal()
			if err != nil {
				vtui.DebugLog("SERVER: WARNING: Failed to prepare terminal: %v", err)
			} else {
				restore = r
				vtui.DebugLog("SERVER: Raw mode and environment enabled successfully.")
			}
		} else {
			vtui.DebugLog("SERVER: FD %d is NOT a terminal, raw mode skipped.", os.Stdin.Fd())
		}

		// Sync terminal size
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
			vtui.DebugLog("SERVER: Terminal size: %dx%d", w, h)
			scr.AllocBuf(w, h)
			for _, s := range vtui.FrameManager.Screens {
				for _, f := range s.Frames {
					f.ResizeConsole(w, h)
				}
			}
		}
		scr.HardReset()
		vtui.FrameManager.Redraw()

		// Watchdog goroutine: polls notifyPipeWriteEnd and stdin to detect when
		// the client terminal dies or closes abruptly (e.g. closing window on macOS).
		watchStop := make(chan struct{})
		// Read on the goroutine that starts this work, not inside it: the
		// work outlives the call, and reading the global from it races
		// anything that reassigns vtui.FrameManager meanwhile.
		frames := vtui.FrameManager
		go func(pipeWriteFD int, inFD int) {
			pipePollFD, pipeOK := boundedInt32(pipeWriteFD)
			inputPollFD, inputOK := boundedInt32(inFD)
			if !pipeOK || !inputOK {
				frames.Stop()
				return
			}
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-watchStop:
					return
				case <-ticker.C:
					// POLLOUT/POLLIN must be requested explicitly: Linux
					// reports POLLERR/POLLHUP even for an empty event mask,
					// but macOS reports nothing, leaving a dead client
					// undetected forever. The masks below only test
					// error/hangup bits, so a healthy writable pipe or
					// readable stdin never trips them.
					pfds := []unix.PollFd{
						{Fd: pipePollFD, Events: unix.POLLOUT},
						{Fd: inputPollFD, Events: unix.POLLIN},
					}
					_, err := unix.Poll(pfds, 0)
					if err == nil {
						pipeDead := (pfds[0].Revents & (unix.POLLERR | unix.POLLHUP | unix.POLLNVAL)) != 0
						inDead := (pfds[1].Revents & (unix.POLLHUP | unix.POLLNVAL)) != 0
						if pipeDead || inDead {
							vtui.DebugLog("SERVER: Watchdog detected disconnected client (pipeRevents=%x, inRevents=%x). Stopping FrameManager.", pfds[0].Revents, pfds[1].Revents)
							frames.Stop()
							return
						}
					}
				}
			}
		}(notifyPipeWriteEnd, fds[0])

		vtui.DebugLog("SERVER: PRE-RUN: Stdin FD: %d, Stdout FD: %d", os.Stdin.Fd(), os.Stdout.Fd())
		// The terminal is asked how large its text area is before
		// anything starts reading standard input, because afterwards
		// the answer is just another escape sequence and the reader
		// eats it. See ttyx_probe.go.
		ProbeHostTextArea()
		PreferCompatibleGraphicsProtocol(scr)
		// And the window over the terminal, for a terminal that cannot
		// show a picture itself. Before the first frame, because every
		// gate on it is asked from inside one.
		InstallX11Overlay()
		reader := vtinput.NewReader(os.Stdin, false)

		// Re-enter host console for the new client if active workspace had panels hidden
		if top := vtui.FrameManager.GetTopFrame(); top != nil {
			if pf, ok := top.(*PanelsFrame); ok && pf != nil {
				if pf.shellMode == ShellModeHost && !pf.showPanels {
					pf.enterHostConsole()
				}
			}
		}

		// A client that attached to a running daemon moves its workspace to its
		// own directory, as a normal start would.
		if attachStartLeft != "" {
			if top := vtui.FrameManager.GetTopFrame(); top != nil {
				if pf, ok := top.(*PanelsFrame); ok && pf != nil {
					applyStartupDirs(pf, attachStartLeft, attachStartRight)
				}
			}
		}

		if attachEditPath != "" {
			// -e's actual open, deferred all the way to here: the terminal
			// is attached, sized, and drawing (scr.HardReset()+Redraw()
			// already ran above) -- opening any earlier would hand
			// actionOpenEditor a frame nothing has actually rendered to
			// yet. Reuses whatever PanelsFrame is already on top rather
			// than assuming there's exactly one, since -e can attach to an
			// existing multi-tab session, not just a freshly started one.
			if top := vtui.FrameManager.GetTopFrame(); top != nil {
				if pf, ok := top.(*PanelsFrame); ok && pf != nil {
					openEditFileIn(pf, attachEditPath)
				} else {
					vtui.DebugLog("SERVER: -e %q: top frame is not a *PanelsFrame (%T)", attachEditPath, top)
				}
			}
		}

		// The key combinations a TTY cannot carry, taken from the X
		// server. See docs/TTYX.md.
		ttyxKeys := startTTYXKeyboard()

		vtui.DebugLog("SERVER: Entering fm.Run()...")
		vtui.FrameManager.Run(reader)
		vtui.DebugLog("SERVER: fm.Run() EXITED.")
		ttyxKeys.Close()

		// Ensure any active host console is cleanly left before restoring terminal
		for _, s := range vtui.FrameManager.Screens {
			if s == nil {
				continue
			}
			for _, f := range s.Frames {
				if pf, ok := f.(*PanelsFrame); ok && pf != nil {
					if pf.shellMode == ShellModeHost && pf.isHostConsoleActive() {
						pf.leaveHostConsole()
					}
				}
			}
		}

		close(watchStop)

		if restore != nil {
			// Ensure all pending escape sequences are sent before restoring terminal
			os.Stdout.Sync()
			clearNonBlock(os.Stdin)
			clearNonBlock(os.Stdout)
			vtui.DebugLog("SERVER: Calling terminal restore()...")
			restore()
			vtui.DebugLog("SERVER: terminal restore() done.")
		}

		// Redirect standard descriptors to /dev/null to fully release the PTY.
		devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err == nil {
			dnfd := int(devNull.Fd())
			_ = unix.Dup2(dnfd, 0)
			_ = unix.Dup2(dnfd, 1)
			_ = unix.Dup2(dnfd, 2)
			devNull.Close()
		}

		// Close the notify pipe to signal the client it can exit.
		syscall.Close(notifyPipeWriteEnd)
		os.Stdout.Sync()

		if fds[0] > 2 {
			newStdin.Close()
		}
		if fds[1] > 2 {
			newStdout.Close()
		}

		os.Stdin, os.Stdout = oldStdin, oldStdout
	}
}

const sessionPickerDialogPreferredWidth = 100

func sessionPickerDialogWidth(screenWidth int) int {
	if screenWidth > 0 && screenWidth < sessionPickerDialogPreferredWidth {
		return screenWidth
	}
	return sessionPickerDialogPreferredWidth
}

func runSessionPicker(sessions []SessionInfo) *SessionInfo {
	restore, err := vtui.PrepareTerminal()
	if err != nil {
		return nil
	}
	defer restore()

	screenWidth, screenHeight, _ := term.GetSize(0)
	scr := vtui.NewScreenBuf()
	scr.AllocBuf(screenWidth, screenHeight)
	vtui.FrameManager.Init(scr)
	SetDefaultF4Palette()

	const dialogHeight = 15
	dialogWidth := sessionPickerDialogWidth(screenWidth)
	dlg := vtui.NewCenteredDialog(dialogWidth, dialogHeight, Msg("Session.Title"))

	var items []string
	for _, s := range sessions {
		items = append(items, fmt.Sprintf("PID: %d - %s", s.PID, s.Title))
	}
	items = append(items, "--- Start New Session ---")

	lb := vtui.NewListBox(0, 0, 10, 9, items)
	dlg.AddItem(lb)

	var selected *SessionInfo
	lb.OnAction = func(idx int) {
		if idx < len(sessions) {
			selected = &sessions[idx]
		} else {
			selected = &SessionInfo{PID: 0}
		}
		dlg.SetExitCode(1)
	}

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	// Layout Engine
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, dialogWidth-4, dialogHeight-4)
	vbox.Add(lb, vtui.Margins{}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, dialogWidth-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnOk.OnClick = func() {
		if lb.OnAction != nil {
			lb.OnAction(lb.SelectPos)
		}
	}
	btnCancel.OnClick = func() { dlg.SetExitCode(-1) }

	vtui.FrameManager.Push(dlg)
	reader := vtinput.NewReader(os.Stdin, false)
	vtui.FrameManager.Run(reader)

	vtui.FrameManager.Shutdown()

	return selected
}
