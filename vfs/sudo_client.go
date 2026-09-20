package vfs

import (
	"errors"
	"fmt"
	"github.com/unxed/vtui"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SudoClient manages a persistent connection to the elevated f4 dispatcher.
type SudoClient struct {
	mu       sync.Mutex
	conn     *net.UnixConn
	sockPath string
	appPath  string
	askPass  string // Command to run for asking password
	attempts int
}

var globalSudoClient *SudoClient

// InitSudoClient initializes the global SudoClient.
func InitSudoClient(appPath, askPass string) {
	globalSudoClient = &SudoClient{
		appPath: appPath,
		askPass: askPass,
	}
}

// GetSudoClient returns the global instance.
func GetSudoClient() *SudoClient {
	return globalSudoClient
}

// IsAvailable checks if the SudoClient has been initialized.
func (c *SudoClient) IsAvailable() bool {
	res := c != nil && sudoClientSupported()
	if c == nil {
		vtui.DebugLog("SUDO_CLIENT: IsAvailable() returning FALSE (client is nil)")
	} else if !sudoClientSupported() {
		vtui.DebugLog("SUDO_CLIENT: IsAvailable() returning FALSE (platform does not support the Unix dispatcher)")
	}
	return res
}

// Connect attempts to start the elevated dispatcher via sudo and connect to its socket.
func (c *SudoClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil // Already connected
	}

	vtui.DebugLog("SUDO_CLIENT: Initializing connection... UID=%d GID=%d", os.Getuid(), os.Getgid())
	c.sockPath = filepath.Join(os.TempDir(), fmt.Sprintf("f4-sudo-%d.sock", os.Getpid()))
	_ = os.Remove(c.sockPath) // The root dispatcher retries removal before its authoritative bind.

	// Start internal askpass server to handle dialog requests from the helper process
	askPassSock := getAskpassSocketPath(os.Getpid())
	if err := os.Remove(askPassSock); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove stale askpass socket %q; remove it and retry: %w", askPassSock, err)
	}
	go c.runAskpassServer(askPassSock)
	// We don't remove askPassSock here, it will be removed by the server goroutine or on exit

	cmd := exec.Command("sudo", "-A", c.appPath, "--sudo-dispatcher", c.sockPath)

	// Not os.Environ(): both children started here -- the dispatcher sudo
	// runs and the askpass helper sudo runs SUDO_ASKPASS for -- are this same
	// binary, and a universal build must not hand them its own "already came
	// through the loader" guard. See childEnv.
	env := childEnv()
	absApp, _ := filepath.Abs(c.appPath)
	env = append(env, "SUDO_ASKPASS="+absApp)
	env = append(env, fmt.Sprintf("F4_ASKPASS_PARENT=%d", os.Getpid()))
	// Ensure the child has access to basic path
	env = append(env, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	cmd.Env = env

	// Capture subprocess stderr to the main log, and keep what sudo itself
	// said: when it gives up, that is the only explanation there is. A writer
	// rather than a pipe read by hand, because Wait then returns only after
	// the last of it has been copied.
	sudoSaid := &sudoStderr{}
	cmd.Stderr = sudoSaid

	c.attempts = 0
	vtui.DebugLog("SUDO_CLIENT: Spawning %q with SUDO_ASKPASS=%q", cmd.String(), c.appPath)
	if err := cmd.Start(); err != nil {
		vtui.DebugLog("SUDO_CLIENT: ERROR: Failed to start sudo: %v (Path: %q)", err, c.appPath)
		return fmt.Errorf("failed to spawn sudo process: %w", err)
	}
	vtui.DebugLog("SUDO_CLIENT: sudo process started, PID: %d", cmd.Process.Pid)

	if fi, err := os.Stat(c.appPath); err == nil {
		vtui.DebugLog("SUDO_CLIENT: Binary perms: %v, owner: %d:%d", fi.Mode(), fi.Sys(), fi.Sys())
	} else {
		vtui.DebugLog("SUDO_CLIENT: Cannot stat binary %q: %v", c.appPath, err)
	}

	sudoExited := make(chan struct{})
	go func() {
		vtui.DebugLog("SUDO_CLIENT: Stderr collector goroutine STARTED for PID %d", cmd.Process.Pid)
		waitErr := cmd.Wait()
		vtui.DebugLog("SUDO_CLIENT: Sudo process %d EXITED. Result: %v", cmd.Process.Pid, waitErr)
		close(sudoExited)
	}()

	// Wait up to 5 minutes for the socket to appear (user might take time to type password)
	var err error
	for i := 0; i < 3000; i++ {
		select {
		case <-sudoExited:
			vtui.DebugLog("SUDO_CLIENT: Sudo process exited prematurely.")
			return sudoExitedError(sudoSaid.Reason())
		default:
		}

		if _, errStat := os.Stat(c.sockPath); errStat == nil {
			var addr *net.UnixAddr
			addr, err = net.ResolveUnixAddr("unix", c.sockPath)
			if err == nil {
				c.conn, err = net.DialUnix("unix", nil, addr)
				if err == nil {
					vtui.DebugLog("SUDO_CLIENT: Successfully connected to dispatcher.")
					return nil
				}
				vtui.DebugLog("SUDO_CLIENT: DialUnix(%q) attempt %d failed: %v", c.sockPath, i, err)
			} else {
				vtui.DebugLog("SUDO_CLIENT: ResolveUnixAddr(%q) failed: %v", c.sockPath, err)
			}
			if os.IsPermission(err) {
				if fi, statErr := os.Lstat(c.sockPath); statErr == nil {
					// Use fmt.Sprintf for raw stat info to avoid missing Sys() fields in some environments
					vtui.DebugLog("SUDO_CLIENT: CRITICAL: Socket %q access denied. Mode: %v, RawStat: %+v", c.sockPath, fi.Mode(), fi.Sys())
				} else {
					vtui.DebugLog("SUDO_CLIENT: Socket %q reported permission error, but Lstat failed: %v", c.sockPath, statErr)
				}
			}
		} else {
			if !os.IsNotExist(errStat) {
				vtui.DebugLog("SUDO_CLIENT: os.Stat(%q) attempt %d failed with unexpected error: %v", c.sockPath, i, errStat)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	vtui.DebugLog("SUDO_CLIENT: ERROR: Dispatcher socket timed out.")

	// Check if any canary files exist (dispatcher actually started)
	if matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "f4-canary-*.txt")); len(matches) > 0 {
		vtui.DebugLog("SUDO_CLIENT: DEBUG: Canary files found: %v. Dispatcher WAS running.", matches)
		for _, m := range matches {
			_ = os.Remove(m) // Canary cleanup is diagnostic only.
		}
	} else {
		vtui.DebugLog("SUDO_CLIENT: DEBUG: No canary files found. Dispatcher never reached RunSudoDispatcher.")
	}

	// Try to harvest logs from the dispatcher's private debug file
	debugLogPath := filepath.Join(os.TempDir(), fmt.Sprintf("f4-sudo-debug-%d.txt", os.Getuid()))
	if logData, errLog := os.ReadFile(debugLogPath); errLog == nil {
		lines := strings.Split(string(logData), "\n")
		for _, l := range lines {
			if l != "" {
				vtui.DebugLog("SUDO_HARVESTED_LOG: %s", l)
			}
		}
		// Clean up to avoid double-logging on next attempt
		_ = os.Remove(debugLogPath) // Harvested debug-log cleanup is best-effort.
	}

	return fmt.Errorf("failed to connect to elevated dispatcher: %v", err)
}

// sudoStderr is the stderr of the sudo process: logged line by line, and the
// last few lines that sudo wrote itself (not the dispatcher's progress notes)
// are kept to say why it exited.
type sudoStderr struct {
	mu      sync.Mutex
	partial string
	lines   []string
}

func (s *sudoStderr) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial += string(p)
	for {
		i := strings.IndexByte(s.partial, '\n')
		if i < 0 {
			break
		}
		s.keep(s.partial[:i])
		s.partial = s.partial[i+1:]
	}
	return len(p), nil
}

func (s *sudoStderr) keep(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	vtui.DebugLog("SUDO_SUBPROCESS: %s", line)
	if strings.HasPrefix(line, "SUDO_DISPATCHER:") {
		return
	}
	s.lines = append(s.lines, line)
	if len(s.lines) > 3 {
		s.lines = s.lines[len(s.lines)-3:]
	}
}

// Reason is what sudo said before it exited, or "" when it said nothing.
func (s *sudoStderr) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rest := strings.TrimSpace(s.partial); rest != "" {
		s.partial = ""
		s.keep(rest)
	}
	return strings.Join(s.lines, "; ")
}

// sudoExitedError explains that sudo gave up before the dispatcher started.
// A user who is not allowed to use sudo used to be told only that the process
// "exited prematurely" (#1255).
func sudoExitedError(reason string) error {
	if reason == "" {
		return errors.New("sudo process exited prematurely")
	}
	lower := strings.ToLower(reason)
	if strings.Contains(lower, "not in the sudoers") || strings.Contains(lower, "not allowed to") {
		return fmt.Errorf("sudo is not available to this user (%s)", reason)
	}
	return fmt.Errorf("sudo process exited prematurely (%s)", reason)
}

// SendRequest sends a command to the dispatcher and waits for the response.
func (c *SudoClient) SendRequest(req SudoRequest) (SudoResponse, *os.File, error) {
	if err := c.Connect(); err != nil {
		return SudoResponse{}, nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	vtui.DebugLog("SUDO_CLIENT: Sending request: Cmd=%d, Path=%q", req.Cmd, req.Path)
	if err := sendMsg(c.conn, req, -1); err != nil {
		_ = c.conn.Close() // Preserve the protocol error that forced disconnect.
		c.conn = nil
		return SudoResponse{}, nil, err
	}

	var resp SudoResponse
	f, err := recvMsg(c.conn, &resp)
	if err != nil {
		_ = c.conn.Close() // Preserve the protocol error that forced disconnect.
		c.conn = nil
		return SudoResponse{}, nil, err
	}

	if resp.Error != "" {
		if f != nil {
			_ = f.Close() // The rejected response's descriptor was never used.
		}
		return resp, nil, newSudoRemoteError(resp.Error)
	}

	return resp, f, nil
}

// sudoRemoteErrnos are the answers a caller decides by, as the dispatcher's
// text spells them.
var sudoRemoteErrnos = []syscall.Errno{
	syscall.ENOENT, syscall.ENOTDIR, syscall.EEXIST, syscall.ENOTEMPTY, syscall.EACCES, syscall.EPERM,
}

// sudoRemoteError is what the dispatcher reported. Only its text crosses the
// socket, so the errno it ended with is read back from that text: the caller
// can then tell "this name is not there" from "this was refused" with errors.Is
// (or errors.As for the errno), as it can for a local call.
type sudoRemoteError struct {
	msg   string
	errno syscall.Errno
}

func (e *sudoRemoteError) Error() string { return e.msg }
func (e *sudoRemoteError) Unwrap() error { return e.errno }

func newSudoRemoteError(msg string) error {
	if strings.HasSuffix(msg, ErrDestinationExists.Error()) {
		return ErrDestinationExists
	}
	for _, errno := range sudoRemoteErrnos {
		if strings.HasSuffix(msg, errno.Error()) {
			return &sudoRemoteError{msg: msg, errno: errno}
		}
	}
	return errors.New(msg)
}

// Open uses SudoClient to securely fetch a File Descriptor to a protected file.
func (c *SudoClient) Open(path string, flags int, mode uint32) (*os.File, error) {
	_, f, err := c.SendRequest(SudoRequest{Cmd: CmdOpen, Path: path, Flags: flags, Mode: mode})
	return f, err
}

func (c *SudoClient) Stat(path string) (VFSItem, error) {
	resp, _, err := c.SendRequest(SudoRequest{Cmd: CmdStat, Path: path})
	return resp.Item, err
}

func (c *SudoClient) ReadDir(path string) ([]VFSItem, error) {
	resp, _, err := c.SendRequest(SudoRequest{Cmd: CmdReadDir, Path: path})
	return resp.Items, err
}

func (c *SudoClient) MkDir(path string, mode uint32) error {
	_, _, err := c.SendRequest(SudoRequest{Cmd: CmdMkDir, Path: path, Mode: mode})
	return err
}

func (c *SudoClient) Remove(path string) error {
	_, _, err := c.SendRequest(SudoRequest{Cmd: CmdRemove, Path: path})
	return err
}

func (c *SudoClient) Rename(oldPath, newPath string) error {
	_, _, err := c.SendRequest(SudoRequest{Cmd: CmdRename, Path: oldPath, Path2: newPath})
	return err
}

// RenameNoReplace renames as root without replacing what is at newPath.
func (c *SudoClient) RenameNoReplace(oldPath, newPath string) error {
	_, _, err := c.SendRequest(SudoRequest{Cmd: CmdRename, Path: oldPath, Path2: newPath, Flags: SudoRenameNoReplace})
	return err
}

func (c *SudoClient) SetAttributes(path string, item VFSItem) error {
	_, _, err := c.SendRequest(SudoRequest{Cmd: CmdSetAttributes, Path: path, Item: item})
	return err
}

func getAskpassSocketPath(pid int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("f4-ap-%d.sock", pid))
}
