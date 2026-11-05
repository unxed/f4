package panel

import (
	"path/filepath"
	"sync"

	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtui"
)

// shellDirTracker remembers the shell's current directory at the start of a
// command (OSC 133;C), so that the end of the command (OSC 133;D) can tell
// whether the command itself changed it. Only the command's own change is
// followed: a "cd ... && true f4_sync" line f4 typed earlier has run before
// the next C, and one typed while the command runs waits in the terminal input
// until the command ends, so neither can look like a change made by the
// command. That is what keeps the panel from being pulled back to a stale
// directory (docs/TERMINAL_JUNK_LOG.md, sections 1 and 6).
type shellDirTracker struct {
	mu     sync.Mutex
	before string
}

func (t *shellDirTracker) start(dir string) {
	t.mu.Lock()
	t.before = dir
	t.mu.Unlock()
}

// finish returns the directory the command moved the shell to, if it did.
func (t *shellDirTracker) finish(after string) (string, bool) {
	t.mu.Lock()
	before := t.before
	t.before = ""
	t.mu.Unlock()
	return shellDirChangedTo(before, after)
}

// shellDirChangedTo is the decision: both directories known and different.
func shellDirChangedTo(before, after string) (string, bool) {
	if before == "" || after == "" {
		return "", false
	}
	if filepath.Clean(before) == filepath.Clean(after) {
		return "", false
	}
	return after, true
}

// localShellCwd asks the local terminal.PTY for the shell's current
// directory; backends that cannot say (not Linux) answer false and the panel
// keeps following only the "cd" commands typed on the command line.
func (pf *PanelsFrame) localShellCwd() (string, bool) {
	pf.PtyMutex.Lock()
	pty := pf.Pty
	pf.PtyMutex.Unlock()
	if cwd, ok := pty.(terminal.PtyShellCwd); ok {
		return cwd.ShellCwd()
	}
	return "", false
}

// handlePOSIXShellMark is the OSC 133 hook of the POSIX shells: C and D come
// from the wrapper f4 puts around every command it runs (managed_exec.go).
// It makes the panel follow a directory changed inside the shell by a
// function, an alias or a script (unxed/f4#2272): the way the shell follows
// the panel is not touched (docs/TERMINAL_JUNK_LOG.md, section 1, point 2).
func (pf *PanelsFrame) handlePOSIXShellMark(mark string) {
	if mark != "C" && mark != "D" {
		return
	}
	if !pf.localShellIsActive() {
		return
	}
	dir, ok := pf.localShellCwd()
	if !ok {
		if mark == "D" {
			pf.shellDir.finish("")
		}
		return
	}
	if mark == "C" {
		pf.shellDir.start(dir)
		return
	}
	if moved, changed := pf.shellDir.finish(dir); changed {
		vtui.FrameManager.PostTask(func() { pf.followShellDirectory(moved) })
	}
}

// followShellDirectory moves the active local panel to the directory the shell
// is in and records it as the directory the shell already has, so the panel's
// own synchronization does not type a "cd" back into the shell.
func (pf *PanelsFrame) followShellDirectory(dir string) bool {
	if pf.ActiveIdx < 0 || pf.ActiveIdx >= len(pf.Panels) {
		return false
	}
	fsp, ok := pf.Panels[pf.ActiveIdx].(*FileSystemPanel)
	if !ok || fsp == nil || !fileops.IsLocalOSVFS(fsp.Vfs) {
		return false
	}
	if filepath.Clean(fsp.Vfs.GetPath()) != filepath.Clean(dir) && !pf.NavigateToPath(fsp, dir) {
		return false
	}
	pf.LastPtyPath = fsp.Vfs.GetPath()
	pf.LastPtyVFS = fsp.Vfs
	pf.ptySyncTriedPath, pf.ptySyncTriedVFS = "", nil
	return true
}
