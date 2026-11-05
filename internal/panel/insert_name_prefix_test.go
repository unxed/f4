package panel

import (
	"testing"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/vfs"
)

func TestWithDotSlash(t *testing.T) {
	w := defaultCommandWrappers
	cases := []struct {
		name, before string
		exec, dir    bool
		want         string
	}{
		{"script.sh", "", true, false, "./script.sh"},
		{"script.sh", "sudo ", true, false, "./script.sh"},
		{"script.sh", "vim ", true, false, "script.sh"},
		{"notes.txt", "", false, false, "notes.txt"},
		{"-rf", "rm ", false, false, "./-rf"},
		{"-rf", "", false, true, "./-rf"},
		{"my file.txt", "cp ", false, false, "my file.txt"},
		{"script.sh", "ls; ", true, false, "./script.sh"},
		{"script.sh", "ls;", true, false, "./script.sh"},
		{"script.sh", "a && ", true, false, "./script.sh"},
		{"script.sh", "a | ", true, false, "./script.sh"},
		{"script.sh", "(", true, false, "./script.sh"},
		{"script.sh", "sudo time ", true, false, "./script.sh"},
		{"script.sh", "echo sudo ", true, false, "script.sh"},
		{"script.sh", "ls", true, false, "script.sh"},
		{"script.sh", "  ", true, false, "./script.sh"},
		{"subdir", "", true, true, "subdir"},
		{"/home/user", "", true, true, "/home/user"},
		{"-x", "", true, false, "./-x"},
		{"", "", true, false, ""},
	}
	for _, c := range cases {
		if got := withDotSlash(c.name, c.before, c.exec, c.dir, w); got != c.want {
			t.Errorf("withDotSlash(%q, before=%q, exec=%v, dir=%v) = %q, want %q",
				c.name, c.before, c.exec, c.dir, got, c.want)
		}
	}
	if got := withDotSlash("script.sh", "chrt ", true, false, append([]string{"chrt"}, w...)); got != "./script.sh" {
		t.Errorf("custom wrapper not honoured: %q", got)
	}
}

func TestInsertSelectedFileNameDotSlash(t *testing.T) {
	if terminal.WindowsShellSyntax() {
		t.Skip("the ./ prefix is for POSIX shells")
	}
	entries := []*FileEntry{
		{VFSItem: vfs.VFSItem{Name: "run.sh", IsExecutable: true}},
		{VFSItem: vfs.VFSItem{Name: "notes.txt"}},
		{VFSItem: vfs.VFSItem{Name: "-rf"}},
		{VFSItem: vfs.VFSItem{Name: "my run.sh", IsExecutable: true}},
		{VFSItem: vfs.VFSItem{Name: "tool", IsDir: true, IsExecutable: true}},
	}
	cases := []struct {
		idx         int
		typed, want string
	}{
		{0, "", "./run.sh "},
		{0, "sudo ", "sudo ./run.sh "},
		{0, "vim ", "vim run.sh "},
		{1, "", "notes.txt "},
		{2, "rm ", "rm ./-rf "},
		{3, "", "'./my run.sh' "},
		{4, "", "tool "},
	}
	for _, c := range cases {
		fsp := &FileSystemPanel{Vfs: vfs.NewOSVFS(t.TempDir()), Entries: entries, CursorIdx: c.idx}
		pf := &PanelsFrame{ActiveIdx: 0, CmdLine: cmdline.NewCommandLine(">"), Panels: [2]Panel{fsp}}
		pf.CmdLine.Edit.SetText(c.typed)
		pf.CmdLine.Edit.SetCursorPos(len([]rune(c.typed)))
		if !pf.InsertSelectedFileName() {
			t.Fatalf("%q: name was not inserted", c.typed)
		}
		if got := pf.CmdLine.Edit.GetText(); got != c.want {
			t.Errorf("entry %d after %q: command line = %q, want %q", c.idx, c.typed, got, c.want)
		}
	}
}
