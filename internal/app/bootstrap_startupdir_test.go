package app

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/unxed/f4/internal/panel"
)

// A start with no terminal -- Dock, the detached GUI copy, the daemon -- names
// no startup directory, so the panels keep what the session restored. `go test`
// runs without a terminal on stdin, which is exactly that case.
func TestRememberStartupDirsIgnoresStartWithoutTerminal(t *testing.T) {
	t.Setenv(startupDirEnv, "")
	t.Setenv(startupDirRightEnv, "")
	rememberStartupDirs([]string{"/home/u/a"})
	if left, right := startupDirs(); left != "" || right != "" {
		t.Fatalf("startupDirs() = (%q, %q), want empty for a start without a terminal", left, right)
	}
}

// The parent's answer travels through the environment; the child inherits it.
func TestRememberStartupDirsKeepsInheritedValue(t *testing.T) {
	t.Setenv(startupDirEnv, "/from/parent")
	t.Setenv(startupDirRightEnv, "/from/parent/right")
	rememberStartupDirs([]string{"/ignored"})
	left, right := startupDirs()
	if left != "/from/parent" || right != "/from/parent/right" {
		t.Fatalf("startupDirs() = (%q, %q), want the inherited values", left, right)
	}
}

func TestStartupDirsForCommandLine(t *testing.T) {
	// Real absolute paths of this platform: what counts as absolute, and which
	// separator joins the parts, is what the answer is made of.
	cwd := t.TempDir()
	other := t.TempDir()
	cases := []struct {
		name       string
		args       []string
		left, righ string
	}{
		{name: "no arguments", left: cwd},
		{name: "one directory", args: []string{other}, left: other, righ: cwd},
		{name: "relative", args: []string{"src"}, left: filepath.Join(cwd, "src"), righ: cwd},
		{
			name: "dot", args: []string{filepath.Join(".", "a"), "."},
			left: filepath.Join(cwd, "a"), righ: cwd,
		},
		{
			name: "parent", args: []string{other, filepath.Join("..", "other")},
			left: other, righ: filepath.Join(filepath.Dir(cwd), "other"),
		},
		// A third argument has no panel to go to.
		{
			name: "extra arguments", args: []string{other, cwd, other},
			left: other, righ: cwd,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left, right := startupDirsFor(cwd, tc.args)
			if left != tc.left || right != tc.righ {
				t.Fatalf("startupDirsFor(%q, %v) = (%q, %q), want (%q, %q)",
					cwd, tc.args, left, right, tc.left, tc.righ)
			}
		})
	}
}

// Folders on the command line name themselves everywhere; only a plain start
// depends on plainOpensCwd.
func TestStartupDirsOverride(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	cases := []struct {
		name          string
		args          []string
		plainOpensCwd bool
		left, right   string
		ok            bool
	}{
		{name: "plain start opens the current directory", plainOpensCwd: true, left: cwd, ok: true},
		{name: "plain start keeps the restored session"},
		{name: "one folder", args: []string{other}, left: other, right: cwd, ok: true},
		{name: "one folder, plain start opening cwd", args: []string{other}, plainOpensCwd: true, left: other, right: cwd, ok: true},
		{name: "two folders", args: []string{other, cwd}, left: other, right: cwd, ok: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left, right, ok := startupDirsOverride(cwd, tc.args, tc.plainOpensCwd)
			if left != tc.left || right != tc.right || ok != tc.ok {
				t.Fatalf("startupDirsOverride(%q, %v, %t) = (%q, %q, %t), want (%q, %q, %t)",
					cwd, tc.args, tc.plainOpensCwd, left, right, ok, tc.left, tc.right, tc.ok)
			}
		})
	}
}

// `cd dir && f4` shows dir in both panels, like mc (issue #822). The change made
// for #823 turned that off on every platform and no test failed: the one that
// covers a plain start checks startupDirsFor, below the change, and the one
// added with it asserted the new behaviour. On macOS the panels went back to
// the previous session (issue #1152). This checks the decision
// rememberStartupDirs actually makes, on each platform CI runs it on.
func TestPlainTerminalStartOpensCurrentDirectory(t *testing.T) {
	cwd := t.TempDir()
	left, right, ok := startupDirsOverride(cwd, nil, plainStartOpensCwd)
	if runtime.GOOS == "windows" {
		// A console started from Explorer or a shortcut has a terminal on stdin
		// too, and a working directory nobody chose: the session wins there.
		if ok || left != "" || right != "" {
			t.Fatalf("plain start on Windows = (%q, %q, %t), want (empty, empty, false)", left, right, ok)
		}
		return
	}
	// An empty right one sends both panels to left.
	if !ok || left != cwd || right != "" {
		t.Fatalf("plain start = (%q, %q, %t), want (%q, empty, true)", left, right, ok, cwd)
	}
}

func TestStartupDirArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{name: "nothing"},
		{name: "one folder", args: []string{"src"}, want: []string{"src"}},
		{name: "two folders", args: []string{"a", "b"}, want: []string{"a", "b"}},
		{name: "folders then switches", args: []string{"a", "b", "--tty", "ansi"}, want: []string{"a", "b"}},
		// --tty takes its backend as a separate word, so nothing after a switch
		// is read as a folder.
		{name: "after a switch", args: []string{"--tty", "a", "b"}},
		{name: "separator", args: []string{"--debug", "--", "a", "b"}, want: []string{"a", "b"}},
		{name: "separator keeps dashed names", args: []string{"--", "-weird-dir"}, want: []string{"-weird-dir"}},
		{name: "unknown switch", args: []string{"--nope", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := startupDirArgs(tc.args)
			if len(got) != len(tc.want) {
				t.Fatalf("startupDirArgs(%v) = %v, want %v", tc.args, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("startupDirArgs(%v) = %v, want %v", tc.args, got, tc.want)
				}
			}
		})
	}
}

// `f4 file` opens the file in the viewer (issue #991). Of the startup paths,
// only those naming something other than a folder are files to view; a folder
// and a word that names nothing stay panel paths.
func TestStartupViewFiles(t *testing.T) {
	cwd := t.TempDir()
	sub := filepath.Join(cwd, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(cwd, "other.txt")
	if err := os.WriteFile(other, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{name: "nothing"},
		{name: "a folder", args: []string{"sub"}},
		{name: "a missing path", args: []string{"nope.txt"}},
		{name: "relative file", args: []string{filepath.Join("sub", "notes.txt")}, want: []string{file}},
		{name: "absolute file", args: []string{other}, want: []string{other}},
		{name: "unclean path", args: []string{filepath.Join(".", "sub", "..", "other.txt")}, want: []string{other}},
		{name: "folder and files", args: []string{"sub", "other.txt", file}, want: []string{other, file}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := startupViewFiles(cwd, tc.args); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("startupViewFiles(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// A path from the command line is made absolute where it was typed: the Unix
// session daemon that opens it may run in another directory.
func TestResolveStartupPath(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	cases := []struct{ path, want string }{
		{path: "notes.txt", want: filepath.Join(cwd, "notes.txt")},
		{path: filepath.Join("sub", "..", "notes.txt"), want: filepath.Join(cwd, "notes.txt")},
		{path: filepath.Join(other, "new.txt"), want: filepath.Join(other, "new.txt")},
	}
	for _, tc := range cases {
		if got := resolveStartupPath(cwd, tc.path); got != tc.want {
			t.Errorf("resolveStartupPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// far2l and Far (issue #495): no folders leaves the panels as the last session
// left them, a first folder replaces its own panel and a second one the other;
// with a single folder the other panel is not touched.
func TestFarStartupDirs(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	cases := []struct {
		name        string
		args        []string
		left, right string
		ok          bool
	}{
		{name: "no arguments keeps the session"},
		{name: "one folder leaves the other panel alone", args: []string{other}, left: other, right: panel.StartupKeepPanel, ok: true},
		{name: "relative folder", args: []string{"src"}, left: filepath.Join(cwd, "src"), right: panel.StartupKeepPanel, ok: true},
		{name: "two folders", args: []string{other, "."}, left: other, right: cwd, ok: true},
		{name: "a third has no panel", args: []string{other, cwd, other}, left: other, right: cwd, ok: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left, right, ok := farStartupDirs(cwd, tc.args)
			if left != tc.left || right != tc.right || ok != tc.ok {
				t.Fatalf("farStartupDirs(%q, %v) = (%q, %q, %t), want (%q, %q, %t)",
					cwd, tc.args, left, right, ok, tc.left, tc.right, tc.ok)
			}
		})
	}
}

// The setting decides which reading applies. Off, the default, is far2l's: a
// plain start names nothing. On, it is mc's: the current folder in both panels
// (issue #822), except on Windows, where a console that Explorer opened has no
// folder anyone chose.
func TestStartupDirsChoice(t *testing.T) {
	cwd := t.TempDir()
	if left, right, ok := startupDirsChoice(cwd, nil, false); ok || left != "" || right != "" {
		t.Errorf("plain start, far2l style = (%q, %q, %t), want nothing", left, right, ok)
	}
	left, right, ok := startupDirsChoice(cwd, nil, true)
	if runtime.GOOS == "windows" {
		if ok {
			t.Errorf("plain start, current-folder style on Windows = (%q, %q, %t), want nothing", left, right, ok)
		}
	} else if !ok || left != cwd || right != "" {
		t.Errorf("plain start, current-folder style = (%q, %q, %t), want (%q, empty, true)", left, right, ok, cwd)
	}

	// One folder: mc shows the current folder in the other panel, far2l leaves it.
	dir := t.TempDir()
	if _, right, _ := startupDirsChoice(cwd, []string{dir}, true); right != cwd {
		t.Errorf("one folder, current-folder style: right = %q, want the current folder %q", right, cwd)
	}
	if _, right, _ := startupDirsChoice(cwd, []string{dir}, false); right != panel.StartupKeepPanel {
		t.Errorf("one folder, far2l style: right = %q, want the keep marker", right)
	}
}
