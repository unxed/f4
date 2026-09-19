//go:build windows

package terminal

import (
	"fmt"
	"os"

	"github.com/unxed/f4/internal/gui"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func SupportsBackgrounding() bool {
	return false
}

type SessionInfo struct {
	PID      int
	Title    string
	SockPath string
}

func listSessions() []SessionInfo {
	return nil
}

func runSessionPicker(sessions []SessionInfo) *SessionInfo {
	return nil
}

func ManageSessions() {
	gui.Running = false
	stopWindowAppearanceManager := gui.StartWindowsConsoleWindowAppearanceManager()
	defer stopWindowAppearanceManager()

	// Before InitCore: vtui's Init already sends OSC 104, and the table to
	// hand back is the one the console had before anything of ours ran. The
	// deferred restore runs after vtui's own (PrepareTerminal's below).
	restoreColorTable := keepConsoleColorTable()
	defer restoreColorTable()

	scr := App.InitCore()
	PreferCompatibleGraphicsProtocol(scr)

	restore, err := vtui.PrepareTerminal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	if restore != nil {
		defer restore()
	}

	// Ask the terminal what it can draw, if the environment did not say.
	// This must happen here: after PrepareTerminal, so VT output is on and
	// the query is asked rather than printed; before InstallConsoleOverlay,
	// which decides on the answer; and before vtinput's reader exists, which
	// would otherwise swallow the reply as keystrokes.
	probeGraphicsIfUnknown(scr)

	// The window over the console, for a console that cannot show a picture
	// itself — which is conhost, where cmd.exe lives. Windows Terminal
	// renders sixel and is left alone. Before the first frame, because
	// every gate on it is asked from inside one.
	App.InstallImageOverlay()

	// Unlike the Unix build (session_unix.go's RunServer, attach time),
	// there's no separate daemon/client split here to defer this past --
	// this *is* the one and only session, already fully up (panels pushed
	// inside InitCore(), terminal prepared above), so opening right here is
	// the direct equivalent of that hook. It comes after the graphics probe
	// and the console overlay because a picture named on the command line is
	// sent to the image viewer only when the screen supports graphics, and
	// on conhost the overlay is that support; F3 asks the same question with
	// both already in place. openStartupFilesIfRequested itself no-ops when
	// the command line named no file.
	App.OpenStartupFiles()

	reader := vtinput.NewReader(os.Stdin, false)
	vtui.FrameManager.Run(reader)

	// Run() returns in good order when the input channel closes, and the
	// input channel closes when the console host has died (#397). Say so,
	// or the log just stops after the session is saved.
	if cause := consoleHostGone(); cause != nil {
		reportConsoleHostGone(cause)
	}
}

func RunServer(sockPath string)                {}
func RunClient(sockPath string, serverPID int) {}
