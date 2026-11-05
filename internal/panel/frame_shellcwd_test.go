//go:build linux

package panel

import (
	"strings"
	"sync"
	"testing"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtui"
)

// cwdPty is a terminal.PtyBackend that also says which directory its shell is
// in, the way the Linux terminal.PTY does through /proc.
type cwdPty struct {
	*mockPty
	mu  sync.Mutex
	dir string
}

func (c *cwdPty) ShellCwd() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dir, c.dir != ""
}

func (c *cwdPty) setShellDir(dir string) {
	c.mu.Lock()
	c.dir = dir
	c.mu.Unlock()
}

func newShellCwdFrame(t *testing.T, panelDir string) (*PanelsFrame, *cwdPty, *FileSystemPanel) {
	t.Helper()
	vtui.SetDefaultPalette()
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ShellMode = terminal.ShellModeOwn
	pty := &cwdPty{mockPty: &mockPty{}, dir: panelDir}
	pf.Pty = pty
	pf.TermView.Pty = pty
	pf.Parser = terminal.NewAnsiParser(pf.TermView, pty)
	pf.ResizeConsole(80, 25)

	fsp, ok := pf.Panels[pf.ActiveIdx].(*FileSystemPanel)
	if !ok {
		t.Fatalf("active panel is %T, want *FileSystemPanel", pf.Panels[pf.ActiveIdx])
	}
	if !pf.NavigateToPath(fsp, panelDir) {
		t.Fatalf("cannot show %s", panelDir)
	}
	pf.LastPtyPath, pf.LastPtyVFS = fsp.Vfs.GetPath(), fsp.Vfs
	pty.Reset()
	return pf, pty, fsp
}

const (
	osc133C = "\x1b]133;C\x07"
	osc133D = "\x1b]133;D\x07"
)

// A function that changes the shell's directory (`gt() { cd /; }`) moves the
// panel too, and no "cd" line is typed back into the shell (unxed/f4#2272).
func TestPanelFollowsDirectoryChangedByShellFunction(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	pf, pty, fsp := newShellCwdFrame(t, from)

	pf.consumeLocalOutput(pty, []byte(osc133C))
	pty.setShellDir(to)
	pf.consumeLocalOutput(pty, []byte(osc133D))
	pumpTasks(t)

	if got := fsp.Vfs.GetPath(); got != to {
		t.Fatalf("panel shows %q, want %q", got, to)
	}
	if pf.LastPtyPath != to {
		t.Fatalf("LastPtyPath = %q, want %q so that no cd is typed back", pf.LastPtyPath, to)
	}
	if strings.Contains(pty.String(), "f4_sync") {
		t.Fatalf("a sync line was typed back into the shell: %q", pty.String())
	}
}

// A command that leaves the shell where it was changes nothing, even when the
// shell's directory differs from the panel's: a sync line typed while the
// command ran is still waiting in the terminal input, and the stale directory
// must not pull the panel back.
func TestPanelStaysWhenCommandKeepsShellDirectory(t *testing.T) {
	stale, shown := t.TempDir(), t.TempDir()
	pf, pty, fsp := newShellCwdFrame(t, shown)
	pty.setShellDir(stale)

	pf.consumeLocalOutput(pty, []byte(osc133C))
	pf.consumeLocalOutput(pty, []byte(osc133D))
	pumpTasks(t)

	if got := fsp.Vfs.GetPath(); got != shown {
		t.Fatalf("panel shows %q, want it to stay at %q", got, shown)
	}
	if pf.LastPtyPath != shown {
		t.Fatalf("LastPtyPath = %q, want %q", pf.LastPtyPath, shown)
	}
}

// A shell that cannot be asked (no /proc, not Linux) leaves the panel alone.
func TestPanelStaysWhenShellDirectoryUnknown(t *testing.T) {
	shown := t.TempDir()
	pf, pty, fsp := newShellCwdFrame(t, shown)
	pty.setShellDir("")

	pf.consumeLocalOutput(pty, []byte(osc133C))
	pf.consumeLocalOutput(pty, []byte(osc133D))
	pumpTasks(t)

	if got := fsp.Vfs.GetPath(); got != shown {
		t.Fatalf("panel shows %q, want %q", got, shown)
	}
}

func TestShellDirChangedTo(t *testing.T) {
	cases := []struct {
		before, after, want string
		changed             bool
	}{
		{"/a", "/b", "/b", true},
		{"/a", "/a", "", false},
		{"/a/", "/a", "", false},
		{"", "/b", "", false},
		{"/a", "", "", false},
	}
	for _, c := range cases {
		got, changed := shellDirChangedTo(c.before, c.after)
		if got != c.want || changed != c.changed {
			t.Errorf("shellDirChangedTo(%q, %q) = %q, %v; want %q, %v", c.before, c.after, got, changed, c.want, c.changed)
		}
	}
}
