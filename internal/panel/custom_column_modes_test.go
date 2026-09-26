package panel

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/vfs"
)

// TestCustomModeWithDuplicateColumnTypeSurvivesConfigAndSessionRoundTrip is
// an end-to-end regression test for f4#410 ("Добавить кастомные режимы
// колонок"). The issue asked for the ability to build an arbitrary column
// mode -- any set of columns, in any order, with a column type allowed to
// repeat (its own example: two Name+Size column pairs side by side). f4
// already models this on Far3's numbered file-panel modes: any of the ten
// Ctrl+0..Ctrl+9 slots can hold an arbitrary column list, edited as text in
// Options -> File panel modes or directly in panel_modes.ini.
//
// This test builds exactly the issue's example -- N,SC,N,SF, a comma-
// grouped size next to a fractional-unit size -- and pushes it through both
// halves of f4's persistence: the mode's column definition (panel_modes.ini)
// and the mode NUMBER a panel is showing (the workspace session). It then
// renders the restored mode and checks that the two SizeColumn occurrences
// kept independent formatting. Any code that keyed a column lookup off its
// Type as though that were unique within the mode -- instead of the
// column's position -- would show the same text twice, drop a column, or
// mix up the two columns' widths/flags, and this test would catch it.
func TestCustomModeWithDuplicateColumnTypeSurvivesConfigAndSessionRoundTrip(t *testing.T) {
	resetPanelViewModes(true)
	defer resetPanelViewModes(true)

	// 1. Define the custom mode in slot Ctrl+6: two Name+Size stripes, the
	// second Size column formatted differently from the first so a mix-up
	// between the two occurrences is observable.
	columns, err := TextToViewSettings("N,SC,N,SF", "0,10,0,10")
	if err != nil {
		t.Fatalf("TextToViewSettings: %v", err)
	}
	panelViewModes.overrides[ViewMode6] = &PanelViewSettings{Columns: columns}
	panelViewModes.generation++

	// 2. Persist the mode definition to panel_modes.ini and reload it the
	// way a fresh process would, forgetting the in-memory definition first.
	modesPath := filepath.Join(t.TempDir(), "panel_modes.ini")
	if err := savePanelViewModes(modesPath); err != nil {
		t.Fatalf("savePanelViewModes: %v", err)
	}
	resetPanelViewModes(true)
	reloadedModes, err := loadPanelViewModes(modesPath)
	if err != nil {
		t.Fatalf("loadPanelViewModes: %v", err)
	}
	panelViewModes.overrides = reloadedModes
	panelViewModes.generation++

	// 3. Persist which mode the panel was showing (the workspace session,
	// settings.ini's [Workspaces] section) and reload that through the real
	// save/reload path, per f4#1496's validSessionViewMode.
	states := []WorkspaceSessionState{{
		Number: 1,
		Left:   PanelSessionState{ViewMode: int(ViewMode6)},
		Right:  PanelSessionState{ViewMode: int(ViewMode6)},
	}}
	var sb strings.Builder
	WriteWorkspaceSessions(&sb, states, 0)
	sessions, _ := LoadWorkspaceSessions(ini.Parse(strings.NewReader(sb.String())))
	if len(sessions) != 1 {
		t.Fatalf("reloaded %d workspace sessions, want 1", len(sessions))
	}
	restoredMode := validSessionViewMode(sessions[0].Left.ViewMode)
	if restoredMode != ViewMode6 {
		t.Fatalf("restored mode = %v, want %v", restoredMode, ViewMode6)
	}

	// 4. Render a panel in the fully restored mode (definition reloaded
	// from panel_modes.ini, slot number reloaded from the session) and
	// check that both Size columns kept their own formatting. Two stripes
	// means the second Name+Size pair shows a different *entry* than the
	// first (files flow down a stripe and then on to the next); giving
	// every entry the same name and size keeps the row-0 assertions below
	// independent of exactly how many rows one stripe holds. There are
	// enough entries that the second stripe's first row is never empty.
	entries := make([]*FileEntry, 30)
	for i := range entries {
		entries[i] = &FileEntry{VFSItem: vfs.VFSItem{Name: "a.bin", Size: 1234567, SizeKnown: true}}
	}
	fsp := newStableInfoTestPanel(0, 0, 60, 12, vfs.NewNullVFS(0), entries)
	fsp.SetViewMode(restoredMode)

	if got := len(fsp.Table.Columns); got != 4 {
		t.Fatalf("restored mode has %d table columns, want 4", got)
	}
	if got := fsp.gridColumnCount(); got != 2 {
		t.Fatalf("restored mode has %d stripes, want 2", got)
	}

	if nameA, nameB := strings.TrimSpace(fsp.GetCellText(0, 0)), strings.TrimSpace(fsp.GetCellText(0, 2)); nameA != "a.bin" || nameB != "a.bin" {
		t.Errorf("both Name columns = %q / %q, want %q twice", nameA, nameB, "a.bin")
	}

	commaSize := strings.TrimSpace(fsp.GetCellText(0, 1))
	floatSize := strings.TrimSpace(fsp.GetCellText(0, 3))
	if want := "1 234 567"; commaSize != want {
		t.Errorf("comma-grouped size column (SC, 1st occurrence) = %q, want %q", commaSize, want)
	}
	if want := "1.18 M"; floatSize != want {
		t.Errorf("fractional-unit size column (SF, 2nd occurrence) = %q, want %q", floatSize, want)
	}
	if commaSize == floatSize {
		t.Fatalf("both SizeColumn occurrences rendered identically (%q): the second one's flags were lost", commaSize)
	}
}
