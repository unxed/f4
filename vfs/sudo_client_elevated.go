package vfs

import (
	"errors"
	"fmt"
	"github.com/unxed/vtui"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// launchElevated starts the elevated dispatcher (LaunchElevatedDispatcher,
// UAC on Windows); a variable so a test can start one in-process instead.
var launchElevated = LaunchElevatedDispatcher

// elevatedConnectTimeout is how long Connect waits for the dispatcher to
// listen once UAC was asked: the user has to read and answer the prompt.
var elevatedConnectTimeout = 2 * time.Minute

// declineElevation is the safe default until the application explicitly
// enables prompts after initializing its UI.
func declineElevation() (agree, remember bool) { return false, false }

// confirmElevation asks the user whether to retry an operation as
// administrator before UAC is asked, and whether to keep the rights until f4
// exits; a variable so a test can answer it. Processes without a UI must not
// enqueue a dialog that nobody can handle and wait for the timeout.
var confirmElevation = declineElevation

// EnableElevationPrompt switches UAC confirmation to the UI dialog. The app
// calls this only after FrameManager.Init has started the UI task pump.
func EnableElevationPrompt() {
	confirmElevation = askElevationOnUI
}

// ErrElevationDeclined: the user did not agree to elevate.
var ErrElevationDeclined = errors.New("elevation declined")

// askElevationOnUI shows the question on the UI goroutine and waits for the
// answer, at most elevatedConnectTimeout; like the sudo password dialog, it
// must not be called on the UI goroutine itself. agree is the first button;
// remember is the "do not ask again until f4 exits" box (f4#1768, step 6b).
func askElevationOnUI() (agree, remember bool) {
	if vtui.FrameManager == nil {
		return false, false
	}
	type answer struct{ agree, remember bool }
	got := make(chan answer, 1)
	globalSudoClient.RunOnUI(func() {
		dlg, box := newElevationDialog()
		dlg.OnResult = func(code int) {
			select {
			case got <- answer{code == 0, code == 0 && box.State == 1}:
			default:
			}
		}
		vtui.FrameManager.Push(dlg)
	})
	select {
	case a := <-got:
		return a.agree, a.remember
	case <-time.After(elevatedConnectTimeout):
		return false, false
	}
}

// newElevationDialog is the question: the text, the box "do not ask again
// until f4 exits" (off), and the buttons Continue as administrator (exit code
// 0) and Cancel (1).
func newElevationDialog() (*vtui.Window, *vtui.Checkbox) {
	const maxWidth, padding = 72, 4
	lines := vtui.WrapText(vtui.Msg("Elevation.Text"), maxWidth-padding)
	box := vtui.NewCheckbox(0, 0, vtui.Msg("Elevation.Remember"), false)
	labels := []string{vtui.Msg("Elevation.Continue"), vtui.Msg("vtui.Cancel")}

	widest := 0
	need := func(w int) {
		if w > widest {
			widest = w
		}
	}
	for _, l := range lines {
		need(vtui.StringWidth(l))
	}
	clean, _, _ := vtui.ParseAmpersandString(vtui.Msg("Elevation.Remember"))
	need(vtui.StringWidth(clean) + 4) // "[ ] "
	buttonsWidth := 2 * (len(labels) - 1)
	for _, b := range labels {
		clean, _, _ := vtui.ParseAmpersandString(b)
		buttonsWidth += vtui.StringWidth(clean) + 4 // "[ " and " ]"
	}
	need(buttonsWidth)
	need(vtui.StringWidth(vtui.Msg("Elevation.Title")) + 6 - padding)
	width := widest + padding
	if width > maxWidth {
		width = maxWidth
	}
	// The frame, a blank row, the text, a blank row, the box, a blank row,
	// the buttons, a blank row, the frame.
	height := len(lines) + 8

	dlg := vtui.NewCenteredDialog(width, height, vtui.Msg("Elevation.Title"))
	dlg.Modal = true
	dlg.IsWarning = true
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-padding, height-padding)
	for _, l := range lines {
		txt := vtui.NewText(0, 0, l, vtui.Palette[vtui.ColDialogText])
		dlg.AddItem(txt)
		vbox.Add(txt, vtui.Margins{}, vtui.AlignLeft)
	}
	dlg.AddItem(box)
	vbox.Add(box, vtui.Margins{Top: 1}, vtui.AlignLeft)
	hbox := vtui.NewHBoxLayout(0, 0, width-padding, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	for i, label := range labels {
		code := i
		btn := vtui.NewButton(0, 0, label)
		btn.IsDefault = i == 0
		btn.OnClick = func() { dlg.SetExitCode(code) }
		dlg.AddItem(btn)
		hbox.Add(btn, vtui.Margins{}, vtui.AlignTop)
	}
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()
	return dlg, box
}

// ElevationIdle is how long the elevated dispatcher may wait without a
// request before f4 closes it, so that f4 does not keep administrator rights
// for the rest of the session: the next operation that needs them asks again,
// as Far does (f4#1768, step 6a). 0 keeps it until f4 exits. A function, so
// that the app reads its setting (config.App.ElevationIdleSeconds) each time.
var ElevationIdle = func() time.Duration { return 2 * time.Minute }

// elevationTimer is the part of *time.Timer the idle timer uses.
type elevationTimer interface{ Stop() bool }

// elevationAfterFunc and elevationNow are time.AfterFunc and time.Now;
// variables so a test can drive the idle timer by hand.
var (
	elevationAfterFunc = func(d time.Duration, f func()) elevationTimer { return time.AfterFunc(d, f) }
	elevationNow       = time.Now
)

// armIdleLocked notes that the elevated dispatcher has just answered and
// (re)starts the timer that closes it after ElevationIdle without requests.
// c.mu is held.
func (c *SudoClient) armIdleLocked() {
	idle := ElevationIdle()
	if !connectsByUAC() || idle <= 0 || c.conn == nil || c.remember {
		return
	}
	c.lastUsed = elevationNow()
	if c.idle != nil {
		c.idle.Stop()
	}
	c.idle = elevationAfterFunc(idle, c.closeIfIdle)
}

// closeIfIdle closes the elevated dispatcher and forgets the consent once it
// has been idle for ElevationIdle. A request in progress holds c.mu, so it is
// never cut: this waits for it, sees the fresh lastUsed and waits again.
func (c *SudoClient) closeIfIdle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	idle := ElevationIdle()
	if c.conn == nil || idle <= 0 {
		return
	}
	if left := idle - elevationNow().Sub(c.lastUsed); left > 0 {
		c.idle = elevationAfterFunc(left, c.closeIfIdle)
		return
	}
	vtui.DebugLog("SUDO_CLIENT: the elevated dispatcher was idle for %v; closing it", idle)
	_ = c.conn.Close() // the dispatcher exits when its client goes away
	c.conn, c.idle = nil, nil
	c.consented = false
}

// connectsByUAC reports that this platform elevates through UAC and the
// elevated dispatcher of f4#1768, not through sudo; a variable so a test can
// take the Windows path on any platform.
var connectsByUAC = func() bool { return runtime.GOOS == "windows" }

// connectElevatedLocked starts the elevated dispatcher with a fresh one-time
// token and connects to it (f4#1768, step 4). c.mu is held.
func (c *SudoClient) connectElevatedLocked() error {
	if !c.consented {
		agree, remember := confirmElevation()
		if !agree {
			return ErrElevationDeclined
		}
		c.consented, c.remember = true, remember
	}
	token, err := NewSudoToken()
	if err != nil {
		return err
	}
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("f4-elevated-%d-%s.sock", os.Getpid(), token[:8]))
	if err := launchElevated(c.appPath, sock, token); err != nil {
		return err
	}
	deadline := time.Now().Add(elevatedConnectTimeout)
	for {
		conn, err := DialElevated(sock, token, 10*time.Second)
		if err == nil {
			c.conn, c.sockPath = conn, sock
			c.armIdleLocked()
			return nil
		}
		if errors.Is(err, ErrSudoAccessDenied) || time.Now().After(deadline) {
			return fmt.Errorf("the elevated dispatcher did not answer: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ReadAt reads up to size bytes of path at offset through the dispatcher;
// eof says the file ends there (CmdReadAt).
func (c *SudoClient) ReadAt(path string, offset int64, size int) (data []byte, eof bool, err error) {
	resp, _, err := c.SendRequest(SudoRequest{Cmd: CmdReadAt, Path: path, Offset: offset, Size: size})
	return resp.Data, resp.IsEOF, err
}

// CreateFile creates or truncates path with mode for writing as the
// elevated side: on Windows by requests through the dispatcher (no
// descriptor crosses), elsewhere as the descriptor sudo's dispatcher hands
// over. What was written is only certain once Close returns nil.
func (c *SudoClient) CreateFile(path string, mode uint32) (io.WriteCloser, error) {
	if connectsByUAC() {
		return newElevatedWriter(c, path, mode), nil
	}
	f, err := c.Open(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// CreateExclusive creates path empty with mode through the dispatcher, only
// when nothing is there yet (CmdWriteAt with SudoWriteExclusive).
func (c *SudoClient) CreateExclusive(path string, mode uint32) error {
	_, _, err := c.SendRequest(SudoRequest{Cmd: CmdWriteAt, Path: path, Flags: SudoWriteExclusive, Mode: mode})
	return err
}

// WriteAt writes data into path at offset through the dispatcher, creating
// the file with mode when it is missing; truncate empties it first
// (CmdWriteAt).
func (c *SudoClient) WriteAt(path string, offset int64, data []byte, truncate bool, mode uint32) error {
	flags := 0
	if truncate {
		flags = SudoWriteTruncate
	}
	_, _, err := c.SendRequest(SudoRequest{Cmd: CmdWriteAt, Path: path, Offset: offset, Data: data, Flags: flags, Mode: mode})
	return err
}
