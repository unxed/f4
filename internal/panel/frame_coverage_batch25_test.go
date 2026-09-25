package panel

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/unxed/vtinput"
)

func TestIsCommandFocusToggleKeyCoverageBatch25(t *testing.T) {
	cases := []struct {
		name  string
		event *vtinput.InputEvent
		want  bool
	}{
		{"virtual grave", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_OEM_3}, true},
		{"text grave", &vtinput.InputEvent{Char: rune(96)}, true},
		{"russian yo", &vtinput.InputEvent{Char: 'ё'}, true},
		{"other text", &vtinput.InputEvent{Char: '~'}, false},
		{"other virtual key", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_OEM_4}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCommandFocusToggleKey(tc.event); got != tc.want {
				t.Fatalf("isCommandFocusToggleKey(%#v) = %v, want %v", tc.event, got, tc.want)
			}
		})
	}
}

func TestParsePlainEditCommandCoverageBatch25(t *testing.T) {
	cases := []struct {
		command string
		path    string
		ok      bool
	}{
		{"edit:file.txt", "file.txt", true},
		{" EDIT:  file with spaces  ", "file with spaces", true},
		{"edit:", "", false},
		{"edit:<<capture", "", false},
		{"open:file.txt", "", false},
	}
	for _, tc := range cases {
		path, ok := parsePlainEditCommand(tc.command)
		if path != tc.path || ok != tc.ok {
			t.Errorf("parsePlainEditCommand(%q) = %q, %v; want %q, %v", tc.command, path, ok, tc.path, tc.ok)
		}
	}
}

func TestParseDirChangeCommandCoverageBatch25(t *testing.T) {
	cases := []struct {
		command string
		target  string
		ok      bool
	}{
		{"cd /tmp", "/tmp", true},
		{"chdir 'folder with spaces'", "folder with spaces", true},
		{"cd \"folder with spaces\"", "folder with spaces", true},
		{"cd..", "..", true},
		{"cd ..", "..", true},
		{"cd/", string(os.PathSeparator), true},
		{"pwd", "", false},
	}
	for _, tc := range cases {
		target, ok := parseDirChangeCommand(tc.command)
		if target != tc.target || ok != tc.ok {
			t.Errorf("parseDirChangeCommand(%q) = %q, %v; want %q, %v", tc.command, target, ok, tc.target, tc.ok)
		}
	}
}

func TestParseDirChangeCommandWindowsFormsCoverageBatch25(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows shell forms are platform-specific")
	}
	drivePath := "C:" + string(os.PathSeparator) + "work"
	if got, ok := parseDirChangeCommand("cd /d " + drivePath); !ok || got != drivePath {
		t.Errorf("cd /d form = %q, %v; want %q, true", got, ok, drivePath)
	}
	if got, ok := parseDirChangeCommand("C:"); !ok || got != drivePath[:2]+string(os.PathSeparator) {
		t.Errorf("drive form = %q, %v; want %q, true", got, ok, drivePath[:2]+string(os.PathSeparator))
	}
	root := string(os.PathSeparator)
	if got, ok := parseDirChangeCommand("cd" + root); !ok || got != root {
		t.Errorf("cd root form = %q, %v; want %q, true", got, ok, root)
	}
}

func TestExpandEnvironmentVariablesCoverageBatch25(t *testing.T) {
	t.Setenv("F4_BATCH25_VALUE", "value")
	cases := map[string]string{
		"$F4_BATCH25_VALUE":        "value",
		"${F4_BATCH25_VALUE}/x":    "value/x",
		"%F4_BATCH25_VALUE%/x":     "value/x",
		"$F4_BATCH25_VALUE-suffix": "value-suffix",
		"$F4_BATCH25_UNKNOWN":      "$F4_BATCH25_UNKNOWN",
		"%F4_BATCH25_UNKNOWN%":     "%F4_BATCH25_UNKNOWN%",
		"$":                        "$",
	}
	for input, want := range cases {
		if got := expandEnvironmentVariables(input); got != want {
			t.Errorf("expandEnvironmentVariables(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExpandPathEnvCoverageBatch25(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("home directory is unavailable")
	}
	if got := ExpandPathEnv("~"); got != home {
		t.Errorf("ExpandPathEnv(~) = %q, want %q", got, home)
	}
	wantChild := filepath.Join(home, "f4-batch25")
	if got := ExpandPathEnv("~/f4-batch25"); got != wantChild {
		t.Errorf("ExpandPathEnv(~/f4-batch25) = %q, want %q", got, wantChild)
	}
	if got := ExpandPathEnv("plain-path"); got != "plain-path" {
		t.Errorf("ExpandPathEnv(plain-path) = %q", got)
	}
}

func TestFolderHistoryStepCoverageBatch25(t *testing.T) {
	history := []string{"/new", "/current", "/old"}
	if pos, target, ok := FolderHistoryStep(history, "/current", -1, -1); !ok || pos != 2 || target != "/old" {
		t.Fatalf("back from current = %d, %q, %v; want 2, /old, true", pos, target, ok)
	}
	if pos, target, ok := FolderHistoryStep(history, "/current", -1, 1); !ok || pos != 0 || target != "/new" {
		t.Fatalf("forward from current = %d, %q, %v; want 0, /new, true", pos, target, ok)
	}
}

func TestFolderHistoryStepBoundariesCoverageBatch25(t *testing.T) {
	history := []string{"/new", "/current", "/old"}
	cases := []struct {
		current   string
		direction int
	}{
		{"/new", 1},
		{"/old", -1},
		{"missing", 1},
	}
	for _, tc := range cases {
		if _, _, ok := FolderHistoryStep(history, tc.current, -1, tc.direction); ok {
			t.Errorf("FolderHistoryStep(%q, %d) unexpectedly found a target", tc.current, tc.direction)
		}
	}
	if _, _, ok := FolderHistoryStep(nil, "/current", -1, -1); ok {
		t.Error("empty history unexpectedly produced a target")
	}
	if _, _, ok := FolderHistoryStep(history, "/current", -1, 0); ok {
		t.Error("zero direction unexpectedly produced a target")
	}
}

func TestSameFolderHistoryPathCoverageBatch25(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"foo/../bar", "bar", true},
		{"https://example.test/a", "https://example.test/a", true},
		{"https://example.test/a", "/tmp/a", false},
		{"", "", false},
		{"/a", "/b", false},
	}
	for _, tc := range cases {
		if got := SameFolderHistoryPath(tc.a, tc.b); got != tc.want {
			t.Errorf("SameFolderHistoryPath(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestHandlePanelPathEditHotkeyRejectsInvalidEventsCoverageBatch25(t *testing.T) {
	cases := []*vtinput.InputEvent{
		nil,
		{Type: vtinput.KeyEventType},
		{Type: vtinput.KeyEventType, KeyDown: true},
		{Type: vtinput.KeyEventType, KeyDown: true, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed, VirtualKeyCode: vtinput.VK_OEM_4},
	}
	for _, event := range cases {
		if HandlePanelPathEditHotkey(event) {
			t.Errorf("HandlePanelPathEditHotkey(%#v) unexpectedly handled the event", event)
		}
	}
}
