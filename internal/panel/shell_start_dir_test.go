package panel

import (
	"testing"

	"github.com/unxed/f4/vfs"
)

// The first directory sync of a local shell is skipped only when the shell
// already stands where the panel is (unxed/f4#1673, section 4 of
// docs/TERMINAL_JUNK_LOG.md).
func TestLocalShellAlreadyInSkipsOnlyTheFirstMatchingSync(t *testing.T) {
	dir := t.TempDir()
	pty := &mockPty{}
	pf := &PanelsFrame{Pty: pty, localShellStartDir: dir}
	local := vfs.NewOSVFS(dir)

	if !pf.localShellAlreadyIn(dir, local, pty) {
		t.Error("the shell starts in the panel's directory, yet the first sync was not skipped")
	}
	if pf.localShellAlreadyIn(dir+"-elsewhere", local, pty) {
		t.Error("a different directory was skipped")
	}
	if pf.localShellAlreadyIn(dir, vfs.NewNullVFS(0), pty) {
		t.Error("a non-local file system was skipped")
	}
	if pf.localShellAlreadyIn(dir, local, &mockPty{}) {
		t.Error("a PTY that is not the local shell's was skipped")
	}
	pf.localShellStartDir = ""
	if pf.localShellAlreadyIn(dir, local, pty) {
		t.Error("an unknown start directory was skipped")
	}
	pf.localShellStartDir = dir
	pf.LastPtyPath = dir
	if pf.localShellAlreadyIn(dir, local, pty) {
		t.Error("a later sync was skipped")
	}
}

func TestSamePanelDir(t *testing.T) {
	if !samePanelDir("/a/b/", "/a/b") {
		t.Error("a trailing separator matters")
	}
	if samePanelDir("", "") || samePanelDir("/a", "") {
		t.Error("empty paths compared equal")
	}
	if samePanelDir("/a/b", "/a/c") {
		t.Error("different directories compared equal")
	}
}
