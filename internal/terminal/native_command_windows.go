//go:build windows

package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/unxed/f4/vfs/hostmode"
	winescape "github.com/unxed/libwinescape/go"
)

const hostWaitNoHang = 1

// HostStderrFD is the file descriptor an inherited host command
// (simple-inline: dir/pause-style commands that share f4's own stdio) should
// use as its own descriptor 2. It defaults to 2, the process's own
// descriptor unmodified.
//
// The console-mode Wine stderr redirect (bootstrap, WINE.md §18.4) points
// f4's own descriptor 2 at a session log file instead of the terminal, so
// that Wine's fixme/err chatter stops landing mid-frame on the screen f4 is
// drawing. A command the user runs from the panels is not Wine's chatter,
// though, and still belongs on the terminal it always went to; the redirect
// stores a fresh descriptor onto the same terminal here before it repoints
// descriptor 2, so runHostCommand can hand an inherited child the terminal
// rather than f4's own diagnostics file.
var HostStderrFD = 2

// ErrLocalCommandUnavailable means that the requested host-shell transport
// could not be created. In POSIX Wine mode this is deliberately an error, not
// a reason to retry the command through cmd.exe.
var ErrLocalCommandUnavailable = errors.New("terminal: local host-shell transport unavailable")

func runNativeLocalCommand(ctx context.Context, dir, command string, emit func([]byte)) (bool, int, error) {
	if !hostmode.Posix() {
		return false, 0, nil
	}
	if err := ctx.Err(); err != nil {
		return true, 0, err
	}
	code, err := runHostCommand(ctx, dir, command, emit, false)
	return true, code, err
}

func runNativeLocalCommandCapture(ctx context.Context, dir, command string) (bool, []byte, error) {
	if !hostmode.Posix() {
		return false, nil, nil
	}
	if err := ctx.Err(); err != nil {
		return true, nil, err
	}
	var output bytes.Buffer
	code, err := runHostCommand(ctx, dir, command, func(chunk []byte) {
		_, _ = output.Write(chunk)
	}, false)
	if err == nil && code != 0 {
		err = fmt.Errorf("exit status %d", code)
	}
	return true, output.Bytes(), err
}

func runNativeLocalCommandInline(dir, command string) (bool, error) {
	if !hostmode.Posix() {
		return false, nil
	}
	code, err := runHostCommand(context.Background(), dir, command, nil, true)
	if err != nil {
		return true, err
	}
	if code != 0 {
		return true, fmt.Errorf("exit status %d", code)
	}
	return true, nil
}

func runHostCommand(ctx context.Context, dir, command string, emit func([]byte), inherited bool) (int, error) {
	exe, argv, env, err := hostShellCommand(command)
	if err != nil {
		return 0, err
	}

	var (
		readFD  = -1
		writeFD = -1
		nullFD  = -1
	)
	if inherited {
		nullFD = 0 // The command is connected to the host's stdin.
	} else {
		readFD, writeFD, err = winescape.Pipe2(winescape.O_CLOEXEC)
		if err != nil {
			return 0, fmt.Errorf("create host-shell output pipe: %w", err)
		}
		nullFD, err = winescape.Open("/dev/null", winescape.O_RDONLY|winescape.O_CLOEXEC, 0)
		if err != nil {
			_ = winescape.Close(readFD)
			_ = winescape.Close(writeFD)
			return 0, fmt.Errorf("open host-shell stdin: %w", err)
		}
	}

	files := []int{nullFD, 1, HostStderrFD}
	if !inherited {
		files[1], files[2] = writeFD, writeFD
	}
	attr := &winescape.SpawnAttr{Dir: dir, Env: env, Files: files}
	if !inherited {
		// A captured command has no job-control terminal, but a private process
		// group lets cancellation terminate shell children as one unit.
		attr.Setsid = true
	}
	pid, err := winescape.Spawn(exe, argv, attr)
	if !inherited {
		_ = winescape.Close(readFD)
		_ = winescape.Close(writeFD)
	}
	if nullFD != 0 {
		_ = winescape.Close(nullFD)
	}
	if err != nil {
		return 0, fmt.Errorf("start host shell %q: %w", exe, err)
	}

	if inherited {
		return waitHostCommand(ctx, pid)
	}

	readDone := make(chan error, 1)
	go func() {
		readDone <- drainHostCommandOutput(readFD, emit)
	}()

	code, waitErr := waitHostCommand(ctx, pid)
	readErr := <-readDone
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if waitErr != nil {
		return 0, waitErr
	}
	if readErr != nil {
		return 0, fmt.Errorf("read host-shell output: %w", readErr)
	}
	return code, nil
}

func hostShellCommand(command string) (string, []string, []string, error) {
	hostEnv, err := winescape.HostEnviron()
	if err != nil {
		return "", nil, nil, fmt.Errorf("read host environment: %w", err)
	}
	shell := nativeShellName(hostEnvValue(hostEnv, "SHELL"))
	exe, ok := resolveHostExecutable(shell, hostEnvValue(hostEnv, "PATH"), isHostExecutable)
	if !ok {
		return "", nil, nil, fmt.Errorf("cannot find host shell %q", shell)
	}
	return exe, []string{shell, "-c", command}, BuildChildEnv(hostEnv, false, false), nil
}

func waitHostCommand(ctx context.Context, pid int) (int, error) {
	var cancelled bool
	for {
		var status int32
		got, err := winescape.Wait4(pid, &status, hostWaitNoHang, nil)
		if err != nil {
			return 0, err
		}
		if got == pid {
			return hostExitStatus(status)
		}
		if ctx.Err() != nil && !cancelled {
			// Captured commands are session leaders, so kill their process group
			// first and the shell itself as a fallback.
			_ = winescape.Kill(-pid, winescape.SIGKILL)
			_ = winescape.Kill(pid, winescape.SIGKILL)
			cancelled = true
		}
		time.Sleep(4 * time.Millisecond)
	}
}

func hostExitStatus(status int32) (int, error) {
	ws := winescape.WaitStatus(status)
	switch {
	case ws.Exited():
		return ws.ExitStatus(), nil
	case ws.Signaled():
		return 128 + ws.Signal(), fmt.Errorf("signal %d", ws.Signal())
	default:
		return 1, errors.New("host shell stopped unexpectedly")
	}
}

func drainHostCommandOutput(fd int, emit func([]byte)) error {
	defer winescape.Close(fd)
	var idle time.Duration
	for {
		fds := []winescape.PollFd{{Fd: int32(fd), Events: winescape.POLLIN}}
		ready, err := winescape.Poll(fds, 0)
		if err != nil {
			return err
		}
		if ready == 0 {
			idle = nextIdle(idle)
			time.Sleep(idle)
			continue
		}
		buf := make([]byte, winePTYChunk)
		n, readErr := winescape.Read(fd, buf)
		if n > 0 && emit != nil {
			emit(buf[:n])
		}
		if readErr != nil {
			if errors.Is(readErr, winescape.EIO) {
				return nil
			}
			return readErr
		}
		if n == 0 {
			return nil
		}
		idle = 0
	}
}
