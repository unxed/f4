package main

import (
	"path/filepath"
	"testing"
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
