package theme

import (
	"os"
	"strings"
	"testing"
)

func indexOfLine(lines []string, want string) int {
	for i, line := range lines {
		if strings.TrimSpace(line) == want {
			return i
		}
	}
	return -1
}

// f4#234: a new element goes to the end of its own group, among its
// neighbours, not into a block at the end of the file.
func TestPlaceColorLinesPutsALineAtTheEndOfItsGroup(t *testing.T) {
	text := `[style]
Name = Custom

[farcolors]

# Panel
Panel.Text = a
Panel.Cursor = b

# Menu
Menu.Text = c

# Terminal
Terminal.Cursor = d
`
	lines := strings.Split(text, "\n")
	out, ok := placeColorLines(lines, map[string][]string{
		"Panel":    {"Panel.New = x"},
		"Terminal": {"Terminal.New = y"},
	})
	if !ok {
		t.Fatal("no section found")
	}
	panelNew, menuHeader := indexOfLine(out, "Panel.New = x"), indexOfLine(out, "# Menu")
	if panelNew < 0 || panelNew != indexOfLine(out, "Panel.Cursor = b")+1 || panelNew > menuHeader {
		t.Errorf("Panel.New is not right after the last Panel line:\n%s", strings.Join(out, "\n"))
	}
	terminalNew := indexOfLine(out, "Terminal.New = y")
	if terminalNew != indexOfLine(out, "Terminal.Cursor = d")+1 {
		t.Errorf("Terminal.New is not right after the last Terminal line:\n%s", strings.Join(out, "\n"))
	}
	// The group blocks stay apart and the file still ends with its newline.
	if got := out[indexOfLine(out, "# Menu")-1]; got != "" {
		t.Errorf("the blank line above # Menu is gone: %q", got)
	}
	if out[len(out)-1] != "" {
		t.Error("the file lost its last newline")
	}
	if strings.Contains(strings.Join(out, "\n"), addedByNewerMarker) {
		t.Error("a heading of the old kind was written")
	}
}

// The block an earlier release wrote at the end is taken apart: its lines
// move to their groups, and nothing is left of the heading.
func TestPlaceColorLinesDissolvesTheOldBlocks(t *testing.T) {
	text := `[farcolors]

# Panel
Panel.Text = a

# Terminal
Terminal.Cursor = d

# Panel: added by a newer f4
Panel.One = 1
Panel.Two = 2

# Terminal: added by a newer f4
Terminal.One = 3

[other]
x = y
`
	out, ok := placeColorLines(strings.Split(text, "\n"), map[string][]string{"Panel": {"Panel.Three = 4"}})
	if !ok {
		t.Fatal("no section found")
	}
	joined := strings.Join(out, "\n")
	if strings.Contains(joined, addedByNewerMarker) {
		t.Fatalf("the old heading is still there:\n%s", joined)
	}
	terminalHeader := indexOfLine(out, "# Terminal")
	for _, want := range []string{"Panel.One = 1", "Panel.Two = 2", "Panel.Three = 4"} {
		i := indexOfLine(out, want)
		if i < indexOfLine(out, "# Panel") || i > terminalHeader {
			t.Errorf("%q is not inside the Panel group:\n%s", want, joined)
		}
	}
	if i := indexOfLine(out, "Terminal.One = 3"); i != indexOfLine(out, "Terminal.Cursor = d")+1 {
		t.Errorf("Terminal.One is not right after the Terminal line:\n%s", joined)
	}
	if indexOfLine(out, "[other]") < 0 || indexOfLine(out, "x = y") < 0 {
		t.Error("the next section was damaged")
	}
	for _, key := range []string{"Panel.One", "Panel.Two", "Terminal.One"} {
		if strings.Count(joined, key+" =") != 1 {
			t.Errorf("%s is present %d times", key, strings.Count(joined, key+" ="))
		}
	}
}

// A group the file has no heading for gets one at the end of the section, and a
// file without the section is left alone.
func TestPlaceColorLinesAddsAMissingGroupAndSkipsFilesWithoutTheSection(t *testing.T) {
	out, ok := placeColorLines(strings.Split("[farcolors]\nPanel.Text = a\n", "\n"), map[string][]string{"Viewer": {"Viewer.New = v"}})
	if !ok || indexOfLine(out, "# Viewer") < 0 || indexOfLine(out, "Viewer.New = v") != indexOfLine(out, "# Viewer")+1 {
		t.Errorf("the missing group was not made:\n%s", strings.Join(out, "\n"))
	}
	if out[len(out)-1] != "" {
		t.Error("the file lost its last newline")
	}
	if _, ok := placeColorLines([]string{"[style]", "Name = Custom"}, map[string][]string{"Panel": {"x = y"}}); ok {
		t.Error("a file with no [farcolors] section was changed")
	}
}

// End to end on a real export: a dropped element returns inside its group, and
// a file an earlier release left with an "added by a newer f4" block is put
// right by the first start of this one, once (f4#234).
func TestAddMissingColorKeysOnARealExport(t *testing.T) {
	SetDefaultF4Palette()
	dir := t.TempDir()
	path := dir + "/farcolors.ini"
	if err := ExportColors(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept, moved []string
	for _, line := range strings.Split(string(data), "\n") {
		key := strings.TrimSpace(strings.SplitN(line, "=", 2)[0])
		switch key {
		case "CommandLine.Path": // gone altogether
			continue
		case "Terminal.Cursor": // sits in the old kind of block at the end
			moved = append(moved, line)
			continue
		}
		kept = append(kept, line)
	}
	if len(moved) != 1 {
		t.Fatalf("test setup: %v", moved)
	}
	aged := strings.TrimRight(strings.Join(kept, "\n"), "\n") + "\n\n# Terminal" + addedByNewerMarker + "\n" + moved[0] + "\n"
	// #nosec G703 -- path is inside the test's temp directory.
	if err := os.WriteFile(path, []byte(aged), 0o600); err != nil {
		t.Fatal(err)
	}

	AddMissingColorKeys(path, loadIni(t, path))
	got, _ := os.ReadFile(path)
	lines := strings.Split(string(got), "\n")
	find := func(prefix string) int {
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				return i
			}
		}
		return -1
	}
	if strings.Contains(string(got), addedByNewerMarker) {
		t.Errorf("the old block is still there:\n%s", got)
	}
	path1, h1, h2 := find("CommandLine.Path"), find("# Command line"), find("# Viewer")
	if path1 < 0 || path1 < h1 || path1 > h2 {
		t.Errorf("CommandLine.Path (line %d) is not inside the Command line group (%d..%d)", path1, h1, h2)
	}
	if cursor, header := find("Terminal.Cursor"), find("# Terminal"); cursor < 0 || cursor < header {
		t.Errorf("Terminal.Cursor (line %d) is not under # Terminal (line %d)", cursor, header)
	}
	if strings.Count(string(got), "Terminal.Cursor =") != 1 {
		t.Error("Terminal.Cursor is there more than once")
	}
	// A second start changes nothing.
	AddMissingColorKeys(path, loadIni(t, path))
	again, _ := os.ReadFile(path)
	if string(again) != string(got) {
		t.Error("a second start rewrote a file that was already in order")
	}
}
