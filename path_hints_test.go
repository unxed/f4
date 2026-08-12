package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func setupPathHintDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "subdir1"), 0755)
	os.Mkdir(filepath.Join(dir, "subdir2"), 0755)
	os.WriteFile(filepath.Join(dir, "subdir1", "inner.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "beta.exe"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "gamma.log"), []byte("x"), 0644)
	return dir
}

func TestPathHintItems_AbsoluteEmptyNeedle(t *testing.T) {
	dir := setupPathHintDir(t)
	v := vfs.NewOSVFS(dir)

	items := pathHintItems(v, dir+string(filepath.Separator), 5, 9)
	if len(items) != 5 {
		t.Fatalf("Expected 5 items, got %d", len(items))
	}
	// Directories first, then files alphabetically
	if !strings.HasPrefix(items[0].Text, dir) {
		t.Errorf("Item text should start with the dir part: %q", items[0].Text)
	}
	if !strings.HasSuffix(items[0].Text, string(filepath.Separator)) {
		t.Errorf("Directory item should end with a separator: %q", items[0].Text)
	}
	if !strings.Contains(items[0].Text, "subdir1") || !strings.Contains(items[1].Text, "subdir2") {
		t.Errorf("Directories should come first: %q, %q", items[0].Text, items[1].Text)
	}
	if !strings.HasSuffix(items[2].Text, "alpha.txt") {
		t.Errorf("First file should be alpha.txt: %q", items[2].Text)
	}
	// Replace span is passed through
	if items[0].ReplaceFrom != 5 || items[0].ReplaceTo != 9 {
		t.Errorf("Replace span lost: from=%d to=%d", items[0].ReplaceFrom, items[0].ReplaceTo)
	}
}

func TestPathHintItems_FuzzyNeedle(t *testing.T) {
	dir := setupPathHintDir(t)
	v := vfs.NewOSVFS(dir)

	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.PathHintFullPath = true
	AppConfig.ShowHighlightMarks = false

	dirPart := dir + string(filepath.Separator)

	items := pathHintItems(v, dirPart+"alp", 0, 0)
	if len(items) != 1 {
		t.Fatalf("Expected exactly alpha.txt, got %d items: %v", len(items), items)
	}
	if !strings.HasSuffix(items[0].Text, "alpha.txt") {
		t.Errorf("Wrong match: %q", items[0].Text)
	}
	// Needle span is offset by the dir part length (runes)
	dirRunes := len([]rune(dirPart))
	if items[0].MatchStart != dirRunes || items[0].MatchEnd != dirRunes+2 {
		t.Errorf("Needle span: got %d-%d, want %d-%d", items[0].MatchStart, items[0].MatchEnd, dirRunes, dirRunes+2)
	}
}

func TestPathHintItems_RelativeToPanel(t *testing.T) {
	dir := setupPathHintDir(t)
	v := vfs.NewOSVFS(dir)

	items := pathHintItems(v, "subdir1/", 0, 0)
	if len(items) != 1 || !strings.HasSuffix(items[0].Text, "inner.txt") {
		t.Fatalf("Relative resolution against the panel path failed: %v", items)
	}
	if !strings.HasPrefix(items[0].Text, "subdir1/") {
		t.Errorf("Replacement text should keep the user's typed form: %q", items[0].Text)
	}
}

func TestPathHintItems_Rejects(t *testing.T) {
	dir := setupPathHintDir(t)
	v := vfs.NewOSVFS(dir)

	if items := pathHintItems(v, "justafile", 0, 0); items != nil {
		t.Errorf("No separator -> no hints, got %v", items)
	}
	if items := pathHintItems(v, filepath.Join(dir, "nosuch")+"/", 0, 0); items != nil {
		t.Errorf("Invalid dir -> no hints, got %v", items)
	}
	if items := pathHintItems(v, "", 0, 0); items != nil {
		t.Errorf("Empty word -> no hints, got %v", items)
	}
}

func TestPathHintItems_QuotedPath(t *testing.T) {
	dir := setupPathHintDir(t)
	v := vfs.NewOSVFS(dir)

	items := pathHintItems(v, `"`+dir+string(filepath.Separator)+`"`, 0, 0)
	if len(items) != 5 {
		t.Fatalf("Quoted path should resolve, got %d items", len(items))
	}
}

func TestPathHintItems_FinalElementOnly(t *testing.T) {
	dir := setupPathHintDir(t)
	v := vfs.NewOSVFS(dir)

	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.PathHintFullPath = false
	AppConfig.ShowHighlightMarks = false

	dirPart := dir + string(filepath.Separator)
	items := pathHintItems(v, dirPart+"alp", 0, 0)
	if len(items) != 1 {
		t.Fatalf("Expected alpha.txt only, got %d items", len(items))
	}
	if items[0].Display != "alpha.txt" {
		t.Errorf("Display should be the final element: %q", items[0].Display)
	}
	if !strings.HasSuffix(items[0].Text, string(filepath.Separator)+"alpha.txt") && items[0].Text != dirPart+"alpha.txt" {
		t.Errorf("Text keeps the full replacement: %q", items[0].Text)
	}
	if items[0].MatchStart != 0 || items[0].MatchEnd != 2 {
		t.Errorf("Needle span relative to display: got %d-%d, want 0-2", items[0].MatchStart, items[0].MatchEnd)
	}

	AppConfig.PathHintFullPath = true
	items = pathHintItems(v, dirPart+"alp", 0, 0)
	if items[0].Display != dirPart+"alpha.txt" {
		t.Errorf("FullPath display: %q", items[0].Display)
	}
	dirRunes := len([]rune(dirPart))
	if items[0].MatchStart != dirRunes {
		t.Errorf("Needle span with full path: got %d, want %d", items[0].MatchStart, dirRunes)
	}
}

func TestPathHintItems_HighlightMarker(t *testing.T) {
	dir := setupPathHintDir(t)
	v := vfs.NewOSVFS(dir)

	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.PathHintFullPath = false
	AppConfig.ShowHighlightMarks = true

	ini := ParseIni(strings.NewReader("[Highlight_0]\nMask = *.exe\nMark = !\n"))
	GlobalFileHighlighter.LoadFromIni(ini)

	dirPart := dir + string(filepath.Separator)
	items := pathHintItems(v, dirPart+"bet", 0, 0)
	if len(items) != 1 {
		t.Fatalf("Expected beta.exe only, got %d items", len(items))
	}
	if items[0].Display != "! beta.exe" {
		t.Errorf("Marker should prefix the name like in panels: %q", items[0].Display)
	}
	if items[0].MatchStart != 2 || items[0].MatchEnd != 4 {
		t.Errorf("Needle span should account for the marker: got %d-%d, want 2-4", items[0].MatchStart, items[0].MatchEnd)
	}
	if items[0].Attr == 0 {
		t.Error("Expected highlight color for *.exe")
	}

	// Same file without the panel marks setting: no marker
	AppConfig.ShowHighlightMarks = false
	items = pathHintItems(v, dirPart+"bet", 0, 0)
	if items[0].Display != "beta.exe" {
		t.Errorf("Marker should follow the panel setting: %q", items[0].Display)
	}
}

func TestPathHintProvider_BothPanels(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	dirA := t.TempDir() // active panel
	dirB := t.TempDir() // passive panel
	os.Mkdir(filepath.Join(dirA, "sub"), 0755)
	os.Mkdir(filepath.Join(dirB, "sub"), 0755)
	os.WriteFile(filepath.Join(dirA, "sub", "active.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dirB, "sub", "passive.txt"), []byte("x"), 0644)

	pf := setupMockPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.panels[1].(*FileSystemPanel).vfs = vfs.NewOSVFS(dirA) // activeIdx = 1
	pf.panels[0].(*FileSystemPanel).vfs = vfs.NewOSVFS(dirB)
	vtui.FrameManager.Push(pf)
	defer vtui.FrameManager.Pop()

	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.PathHintSource = PathHintSourceBoth
	AppConfig.PathHintFullPath = false

	items := pathHintProvider(nil, "sub/", 0, 4)
	// active group, separator, passive group
	if len(items) != 3 {
		t.Fatalf("Expected 3 items (active + separator + passive), got %d: %v", len(items), items)
	}
	if !strings.HasSuffix(items[0].Display, "active.txt") {
		t.Errorf("Active panel group first: %q", items[0].Display)
	}
	if !items[1].Separator {
		t.Error("Groups should be split by a separator")
	}
	if !strings.HasSuffix(items[2].Display, "passive.txt") {
		t.Errorf("Passive panel group second: %q", items[2].Display)
	}

	// Passive only
	AppConfig.PathHintSource = PathHintSourcePassive
	items = pathHintProvider(nil, "sub/", 0, 4)
	if len(items) != 1 || !strings.HasSuffix(items[0].Display, "passive.txt") {
		t.Fatalf("Passive-only source failed: %v", items)
	}

	// Active only (default)
	AppConfig.PathHintSource = PathHintSourceActive
	items = pathHintProvider(nil, "sub/", 0, 4)
	if len(items) != 1 || !strings.HasSuffix(items[0].Display, "active.txt") {
		t.Fatalf("Active-only source failed: %v", items)
	}
}
