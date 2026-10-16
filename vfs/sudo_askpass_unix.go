//go:build !windows

package vfs

import (
	"fmt"
	"github.com/unxed/vtui"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

// RunSudoAskpass is executed when f4 is called with --askpass flag by sudo -A.
// It connects back to the main f4 process to get the password from the UI.
func RunSudoAskpass() {
	parentStr := os.Getenv("F4_ASKPASS_PARENT")
	fmt.Fprintf(os.Stderr, "F4_ASKPASS: Helper started for parent PID %s\n", parentStr)

	// The dispatcher's stderr is not visible to the f4 process, so this file is
	// how a failed elevation gets diagnosed: SudoClient reads it back when the
	// socket never appears.
	//
	// It deliberately does not record the environment. The path is predictable
	// and lives in a directory every local user can write, so anything written
	// here is within reach of another user who created the file first, and the
	// environment is where tokens and passwords live. O_NOFOLLOW stops the
	// other half of that: a symlink planted here would redirect the write.
	debugLogPath := filepath.Join(os.TempDir(), fmt.Sprintf("f4-sudo-debug-%d.txt", os.Getuid()))
	debugLog, _ := os.OpenFile(debugLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if debugLog != nil {
		_, _ = fmt.Fprintf(debugLog, "[%s] ASKPASS: PID=%d, ParentPID=%s, Args=%v\n", time.Now().Format("15:04:05"), os.Getpid(), parentStr, os.Args) // Debug logging is best-effort.
		_ = debugLog.Close()                                                                                                                          // Debug log persistence is best-effort.
	}
	parentPID, _ := strconv.Atoi(parentStr)
	if parentPID == 0 {
		os.Exit(1)
	}

	sockPath := getAskpassSocketPath(parentPID)

	// Retry connection a few times in case of race during startup
	var conn net.Conn
	var err error
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("unix", sockPath)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err != nil {
		os.Exit(1)
	}

	// Request password
	if _, err := fmt.Fprintf(conn, "GET\n"); err != nil {
		os.Exit(1)
	}

	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		os.Exit(1)
	}

	// Output password to sudo
	vtui.DebugLog("F4_ASKPASS: Sending %d bytes to sudo stdout", n)
	if _, err := os.Stdout.Write(buf[:n]); err != nil {
		os.Exit(1)
	}
	// Sudo expects the password followed by a newline or just the password depending on the version.
	// Most implementations use a trailing newline.
	if _, err := os.Stdout.Write([]byte("\n")); err != nil {
		os.Exit(1)
	}
	_ = conn.Close() // The request is complete; process exit remains authoritative.
	os.Exit(0)
}

func (c *SudoClient) runAskpassServer(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		vtui.DebugLog("SUDO_CLIENT: remove stale askpass socket %q: %v", path, err)
		return
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		vtui.DebugLog("SUDO_CLIENT: runAskpassServer Listen failed for %q: %v", path, err)
		return
	}
	defer func() {
		_ = l.Close() // Listener cleanup cannot affect a completed request.
	}()
	if err := os.Chmod(path, 0600); err != nil {
		vtui.DebugLog("SUDO_CLIENT: secure askpass socket %q: %v", path, err)
		return
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}

		// Each connection is one password request
		c.handleAskpassRequest(conn)
	}
}

func (c *SudoClient) handleAskpassRequest(conn net.Conn) {
	vtui.DebugLog("SUDO_CLIENT: Received askpass request from helper, current attempts: %d", c.attempts)
	defer func() {
		_ = conn.Close() // Request writes are checked before transport cleanup.
	}()

	// Max 3 attempts per connection session to prevent UI lockup
	if c.attempts >= 3 {
		return
	}
	c.attempts++

	resChan := make(chan string, 1)
	// withdrawn: the dialog was closed by WithdrawPrompt, not by the user.
	var withdrawn atomic.Bool

	// We need to show the dialog on the UI thread
	c.RunOnUI(func() {
		title := " f4: Sudo Elevation "
		prompt := "Enter root password:"

		dlg := vtui.NewCenteredDialog(45, 9, title)
		dlg.Modal = true
		dlg.ShowClose = true

		edit := vtui.NewPasswordEdit(0, 0, 35, "")
		lbl := vtui.NewLabel(0, 0, prompt, edit)
		btnOk := vtui.NewButton(0, 0, vtui.Msg("vtui.Ok"))
		btnOk.IsDefault = true
		btnCancel := vtui.NewButton(0, 0, vtui.Msg("vtui.Cancel"))

		dlg.AddItem(lbl)
		dlg.AddItem(edit)
		dlg.AddItem(btnOk)
		dlg.AddItem(btnCancel)

		vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 45-4, 9-4)
		vbox.Add(lbl, vtui.Margins{}, vtui.AlignLeft)
		vbox.Add(edit, vtui.Margins{Top: 1}, vtui.AlignFill)

		hbox := vtui.NewHBoxLayout(0, 0, 45-4, 1)
		hbox.HorizontalAlign = vtui.AlignCenter
		hbox.Spacing = 2
		hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
		hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
		vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
		vbox.Apply()

		btnOk.OnClick = func() {
			c.setPrompt(nil)
			resChan <- edit.GetText()
			dlg.Close()
		}
		btnCancel.OnClick = func() {
			c.setPrompt(nil)
			resChan <- ""
			dlg.Close()
		}
		dlg.OnResult = func(code int) {
			if code < 0 {
				c.setPrompt(nil)
				select {
				case resChan <- "":
				default:
				}
			}
		}
		c.setPrompt(func() {
			withdrawn.Store(true)
			c.setPrompt(nil)
			select {
			case resChan <- "":
			default:
			}
			dlg.Close()
		})

		if vtui.FrameManager != nil {
			// The request above was logged when it arrived; this line is
			// when the UI goroutine got to it. A gap between the two is time
			// the prompt spent waiting in the task queue.
			vtui.DebugLog("SUDO_CLIENT: Password dialog shown")
			vtui.FrameManager.Push(dlg)
		}
	})

	password := <-resChan
	if password != "" {
		vtui.DebugLog("SUDO_CLIENT: Password received from UI, sending to helper...")
		if _, err := conn.Write([]byte(password)); err != nil {
			vtui.DebugLog("SUDO_CLIENT: Failed to send password to helper: %v", err)
		}
	} else if withdrawn.Load() {
		vtui.DebugLog("SUDO_CLIENT: Password dialog withdrawn: the operation that asked was cancelled.")
	} else {
		vtui.DebugLog("SUDO_CLIENT: Password dialog cancelled by user.")
		c.markDeclined()
	}
}

// setPrompt records how to close the password dialog that was just shown;
// nil when it has gone.
func (c *SudoClient) setPrompt(withdraw func()) {
	c.promptMu.Lock()
	c.withdrawPrompt = withdraw
	c.promptMu.Unlock()
}

// markDeclined records that the user cancelled the password dialog.
func (c *SudoClient) markDeclined() {
	c.declines.Add(1)
}

// Dummy for SudoClient to satisfy RunOnUI if not imported
func (c *SudoClient) RunOnUI(fn func()) {
	if vtui.FrameManager != nil {
		vtui.FrameManager.PostTask(fn)
	}
}
