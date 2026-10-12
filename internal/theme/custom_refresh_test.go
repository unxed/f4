package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/ini"
)

// A Custom scheme written by an older f4 lacks the elements added since. Starting
// with it must add those lines, with the colours in force, and leave the
// user's own lines and comments exactly as they were (f4#234).
func TestApplyColorStyle_CustomFileGainsNewKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "farcolors.ini")
	oldOverrides := UserColorOverridesPath
	UserColorOverridesPath = func() string { return path }
	t.Cleanup(func() { UserColorOverridesPath = oldOverrides })
	oldStyles := getUserStylesDir
	getUserStylesDir = func() string { return filepath.Join(dir, "styles") }
	t.Cleanup(func() { getUserStylesDir = oldStyles })
	oldCfg := config.App
	config.App.EnforceColorCorrection = false
	config.App.ColorStyle = "Modern"
	t.Cleanup(func() { config.App = oldCfg })

	if err := ApplyColorStyle("Modern"); err != nil {
		t.Fatal(err)
	}
	if err := ExportColors(path); err != nil {
		t.Fatal(err)
	}

	// Age the file: drop two elements, annotate one line of the user's.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	dropped := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		key := strings.TrimSpace(strings.SplitN(line, "=", 2)[0])
		if key == "CommandLine.Path" || key == "Editor.Text.Selected" {
			dropped[key] = true
			continue
		}
		if key == "Panel.Text" {
			kept = append(kept, "# my favourite panel colours")
			line = "Panel.Text = foreground:#112233 | background:#445566"
		}
		kept = append(kept, line)
	}
	if len(dropped) != 2 {
		t.Fatalf("test setup: dropped %v, want both keys", dropped)
	}
	aged := strings.Join(kept, "\n")
	// #nosec G703 -- path is inside the test's temp directory.
	if err := os.WriteFile(path, []byte(aged), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ApplyColorStyle(CustomColorStyleName); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, key := range []string{"CommandLine.Path = ", "Editor.Text.Selected = "} {
		if !strings.Contains(text, key) {
			t.Errorf("the new element %q was not added to farcolors.ini:\n%s", key, text)
		}
	}
	for _, mine := range []string{"# my favourite panel colours", "Panel.Text = foreground:#112233 | background:#445566"} {
		if !strings.Contains(text, mine) {
			t.Errorf("the user's own line %q was lost", mine)
		}
	}
	// The aged file is a prefix of the new one up to the section's end: only
	// lines were inserted.
	for _, line := range kept {
		if line != "" && !strings.Contains(text, line) {
			t.Errorf("line %q of the old file is gone", line)
		}
	}

	// Run again: nothing is missing now, the file stays byte for byte.
	if err := ApplyColorStyle(CustomColorStyleName); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != text {
		t.Error("a second start rewrote a file that was already complete")
	}
}

// The file's own line ending survives, and a file without the section is left alone.
func TestAddMissingColorKeys_KeepsCRLFAndIgnoresFilesWithoutTheSection(t *testing.T) {
	// The export reads the palette; run alone (or first in a shuffled run) it is still empty.
	SetDefaultF4Palette()
	dir := t.TempDir()
	crlf := filepath.Join(dir, "crlf.ini")
	if err := os.WriteFile(crlf, []byte("[style]\r\nName = Custom\r\n\r\n[farcolors]\r\nPanel.Text = foreground:#ffffff | background:#000000\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := AddMissingColorKeys(crlf, loadIni(t, crlf)); n == 0 {
		t.Fatal("nothing added to a nearly empty scheme")
	}
	data, _ := os.ReadFile(crlf)
	if strings.Contains(strings.ReplaceAll(string(data), "\r\n", ""), "\n") {
		t.Error("a bare LF was written into a CRLF file")
	}

	none := filepath.Join(dir, "none.ini")
	if err := os.WriteFile(none, []byte("[style]\nName = Custom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := AddMissingColorKeys(none, loadIni(t, none)); n != 0 {
		t.Errorf("added %d keys to a file with no [farcolors] section", n)
	}
	if AddMissingColorKeys(filepath.Join(dir, "missing.ini"), nil) != 0 {
		t.Error("a missing file must not be created")
	}
}

func loadIni(t *testing.T, path string) *ini.File {
	t.Helper()
	return ini.Load(path)
}
