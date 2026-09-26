package panel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/vfs"
)

func TestTextToViewSettingsRoundTrip(t *testing.T) {
	columns, err := TextToViewSettings("nm, sca ,dmb,O,u,A,P,D,T,DC,DA,DE,ln", "0,10,15%,8,8,10")
	if err != nil {
		t.Fatalf("TextToViewSettings: %v", err)
	}
	types, widths := ViewSettingsToText(columns)
	if types != "NM,SCA,DMB,O,U,A,P,D,T,DC,DA,DE,LN" {
		t.Errorf("types = %q", types)
	}
	if widths != "0,10,15%,8,8,10,0,0,0,0,0,0,0" {
		t.Errorf("widths = %q", widths)
	}
}

func TestTextToViewSettingsRejectsWhatItCannotRead(t *testing.T) {
	for _, tc := range []struct{ types, bad string }{
		{"N,X", "X"}, {"NQ", "NQ"}, {"N,,S", ""}, {"DX", "DX"}, {"UL", "UL"},
	} {
		_, err := TextToViewSettings(tc.types, "")
		var typeErr *PanelColumnTypeError
		if !errors.As(err, &typeErr) || typeErr.Token != tc.bad {
			t.Errorf("TextToViewSettings(%q) error = %v, want bad token %q", tc.types, err, tc.bad)
		}
	}
	if _, err := TextToViewSettings("N,S", "0,abc"); !errors.As(err, new(*PanelColumnWidthError)) {
		t.Errorf("bad width error = %v", err)
	}
	if _, err := TextToViewSettings("  ", ""); !errors.Is(err, ErrNoPanelColumns) {
		t.Errorf("empty types error = %v", err)
	}
}

func TestDefaultPanelViewModesParse(t *testing.T) {
	for key, spec := range panelViewModeDefaults {
		if _, err := TextToViewSettings(spec.columns, spec.widths); err != nil {
			t.Errorf("default mode %d does not parse: %v", key, err)
		}
	}
	for key := 0; key < PanelViewModeCount; key++ {
		mode, ok := ViewModeForKey(key)
		if !ok || mode.Key() != key {
			t.Errorf("key %d maps to mode %d (ok=%v), which maps back to %d", key, mode, ok, mode.Key())
		}
	}
}

// legacyGridWidths is how Resize split Brief and Medium before modes were
// configurable.
func legacyGridWidths(w, count int) []int {
	available := max(w-2-(count-1), count)
	widths := make([]int, count)
	remaining := available
	for i := range widths {
		widths[i] = max(remaining/(count-i), 1)
		remaining -= widths[i]
	}
	return widths
}

func layoutWidths(layout panelLayout) []int {
	widths := make([]int, len(layout.columns))
	for i, column := range layout.columns {
		widths[i] = column.Width
	}
	return widths
}

func TestBuiltInModesKeepTheirLegacyLayout(t *testing.T) {
	for _, w := range []int{40, 61, 100, 161} {
		for _, tc := range []struct {
			mode ViewMode
			want []int
		}{
			{ViewModeBrief, legacyGridWidths(w, 3)},
			{ViewModeMedium, legacyGridWidths(w, 2)},
			{ViewModeDetailed, []int{w - 14, panelSizeColumnWidth}},
			{ViewModeWide, []int{w - 2 - 2 - panelSizeColumnWidth - panelModifiedColumnWidth, panelSizeColumnWidth, panelModifiedColumnWidth}},
		} {
			layout := preparePanelLayout(DefaultPanelViewSettings(tc.mode).Columns, w-2)
			if got := layoutWidths(layout); !equalInts(got, tc.want) {
				t.Errorf("width %d, mode %d: widths %v, want %v", w, tc.mode, got, tc.want)
			}
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPreparePanelLayoutDropsColumnsThatDoNotFit(t *testing.T) {
	columns, err := TextToViewSettings("N,S,DM,A", "0,10,14,10")
	if err != nil {
		t.Fatal(err)
	}
	// As in far2l: the attribute column no longer fits at all and goes, the
	// date column that is last now gives up what the panel lacks.
	if got := layoutWidths(preparePanelLayout(columns, 20)); !equalInts(got, []int{1, 10, 7}) {
		t.Fatalf("widths %v, want [1 10 7]", got)
	}
	if got := layoutWidths(preparePanelLayout(columns, 30)); !equalInts(got, []int{1, 10, 14, 2}) {
		t.Fatalf("widths %v, want [1 10 14 2]", got)
	}
}

func TestPreparePanelLayoutSharesPercentWidths(t *testing.T) {
	columns, err := TextToViewSettings("N,N", "25%,0")
	if err != nil {
		t.Fatal(err)
	}
	layout := preparePanelLayout(columns, 101)
	if got := layoutWidths(layout); !equalInts(got, []int{25, 75}) {
		t.Fatalf("widths %v, want [25 75]", got)
	}
}

func TestPanelColumnStripes(t *testing.T) {
	for _, tc := range []struct {
		types             string
		perStripe, stripe int
	}{
		{"N,N,N", 1, 3}, {"N,N", 1, 2}, {"N,SC", 2, 1}, {"N,S,N,S", 2, 2}, {"N,S,N", 3, 1}, {"N,S,DM,N,S,DM", 3, 2},
	} {
		columns, err := TextToViewSettings(tc.types, "")
		if err != nil {
			t.Fatal(err)
		}
		if per, stripes := panelColumnStripes(columns); per != tc.perStripe || stripes != tc.stripe {
			t.Errorf("%s: %d columns x %d stripes, want %d x %d", tc.types, per, stripes, tc.perStripe, tc.stripe)
		}
	}
}

func TestFormatPanelSize(t *testing.T) {
	if got, want := formatPanelSize(1234567, 11, ColumnCommas), fileops.FormatIntWithSpaces(1234567); got != want {
		t.Errorf("fitting size = %q, want %q", got, want)
	}
	if got := formatPanelSize(12345678901, 11, ColumnCommas); !strings.HasSuffix(got, " M") || runewidth.StringWidth(got) > 11 {
		t.Errorf("scaled size = %q", got)
	}
	if got := formatPanelSize(123456789, 7, ColumnEconomic); got != "120563K" {
		t.Errorf("economic size = %q", got)
	}
	if got := formatPanelSize(1536, 11, ColumnFloatSize); got != "1.50 K" {
		t.Errorf("float size = %q", got)
	}
	if got := formatPanelSize(999, 3, 0); got != "999" {
		t.Errorf("small size = %q", got)
	}
}

func TestFormatPanelColumnTime(t *testing.T) {
	stamp := time.Date(2024, 3, 5, 14, 7, 9, 0, time.Local)
	later := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		column PanelColumn
		now    time.Time
		want   string
	}{
		{PanelColumn{Type: WDateColumn, Width: 14}, later, "05.03.24 14:07"},
		{PanelColumn{Type: WDateColumn, Width: 16}, later, "05.03.2024 14:07"},
		{PanelColumn{Type: WDateColumn, Width: 19}, later, "05.03.2024 14:07:09"},
		{PanelColumn{Type: DateColumn, Width: 8}, later, "05.03.24"},
		{PanelColumn{Type: DateColumn, Width: 10}, later, "05.03.2024"},
		{PanelColumn{Type: TimeColumn, Width: 5}, later, "14:07"},
		{PanelColumn{Type: WDateColumn, Flags: ColumnBrief, Width: 11}, later, "05.03  2024"},
		{PanelColumn{Type: WDateColumn, Flags: ColumnBrief, Width: 11}, stamp, "05.03 14:07"},
	} {
		if got := formatPanelColumnTime(stamp, tc.column, tc.now); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.column, got, tc.want)
		}
	}
	if got := formatPanelColumnTime(time.Time{}, PanelColumn{Type: WDateColumn, Width: 14}, later); got != "" {
		t.Errorf("zero time = %q", got)
	}
}

func TestPanelLinkCountText(t *testing.T) {
	known := &FileEntry{VFSItem: vfs.VFSItem{Nlink: 3, KnownMetadata: vfs.MetadataNlink}}
	if got := panelLinkCountText(known); got != "3" {
		t.Errorf("known link count = %q, want %q", got, "3")
	}
	unknown := &FileEntry{}
	if got := panelLinkCountText(unknown); got != "" {
		t.Errorf("unknown link count = %q, want empty", got)
	}
}

// TestPanelLinkCountColumnEndToEnd is a regression test for f4#1400: a real
// file on the local filesystem has one hard link, and a panel mode with an
// LN column must show it.
func TestPanelLinkCountColumnEndToEnd(t *testing.T) {
	resetPanelViewModes(true)
	defer resetPanelViewModes(true)

	dir := t.TempDir()
	filePath := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := vfs.NewOSVFS(dir)
	item, err := fs.Stat(context.Background(), filePath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !item.HasMetadata(vfs.MetadataNlink) {
		t.Fatal("Stat did not report Nlink metadata for a real file")
	}

	fsp := newStableInfoTestPanel(0, 0, 60, 12, fs, []*FileEntry{{VFSItem: item}})
	columns, err := TextToViewSettings("N,LN", "0,4")
	if err != nil {
		t.Fatalf("TextToViewSettings: %v", err)
	}
	panelViewModes.overrides[ViewModeDetailed] = &PanelViewSettings{Columns: columns}
	panelViewModes.generation++
	fsp.SetViewMode(ViewModeDetailed)

	if got := strings.TrimSpace(fsp.GetCellText(0, 1)); got != "1" {
		t.Errorf("hard link count of a new file = %q, want %q", got, "1")
	}
}

func TestUnixPermissionsText(t *testing.T) {
	for _, tc := range []struct {
		mode             uint32
		isDir, isSymlink bool
		want             string
	}{
		{0o4755, false, false, "-rwsr-xr-x"},
		{0o1777, true, false, "drwxrwxrwt"},
		{0o644, false, true, "lrw-r--r--"},
		{0o2640, false, false, "-rw-r-S---"},
	} {
		if got := unixPermissionsText(tc.mode, tc.isDir, tc.isSymlink); got != tc.want {
			t.Errorf("%o: %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestPanelModesFileRoundTrip(t *testing.T) {
	resetPanelViewModes(true)
	defer resetPanelViewModes(true)
	path := filepath.Join(t.TempDir(), "settings", "panel_modes.ini")

	columns, err := TextToViewSettings("N,SC,DM", "0,11,16")
	if err != nil {
		t.Fatal(err)
	}
	panelViewModes.overrides[ViewMode7] = &PanelViewSettings{Columns: columns, FullScreen: true}
	if err := savePanelViewModes(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(data); !strings.Contains(text, "[Panel/ViewModes/Mode7]") ||
		!strings.Contains(text, "Columns=N,SC,DM") || !strings.Contains(text, "ColumnWidths=0,11,16") ||
		!strings.Contains(text, "FullScreen=1") || strings.Contains(text, "Mode1]") {
		t.Fatalf("panel_modes.ini:\n%s", text)
	}
	loaded, err := loadPanelViewModes(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for mode, settings := range loaded {
		if ViewMode(mode) != ViewMode7 && settings != nil {
			t.Errorf("mode %d loaded although it was not saved", mode)
		}
	}
	got := loaded[ViewMode7]
	if got == nil || !got.FullScreen {
		t.Fatalf("mode 7 = %+v", got)
	}
	if types, widths := ViewSettingsToText(got.Columns); types != "N,SC,DM" || widths != "0,11,16" {
		t.Fatalf("mode 7 columns %q / %q", types, widths)
	}

	// Resetting the last customized mode removes the file.
	panelViewModes.overrides[ViewMode7] = nil
	if err := savePanelViewModes(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("panel_modes.ini still exists: %v", err)
	}
}

func viewModesTestEntries() []*FileEntry {
	return []*FileEntry{
		{VFSItem: vfs.VFSItem{Name: "a", Size: 1, SizeKnown: true}},
		{VFSItem: vfs.VFSItem{Name: "b", Size: 2, SizeKnown: true}},
		{VFSItem: vfs.VFSItem{Name: "c", Size: 3, SizeKnown: true}},
	}
}

func TestStripeOfSeveralColumnsShowsEachColumnAndCarriesTheCursor(t *testing.T) {
	resetPanelViewModes(true)
	defer resetPanelViewModes(true)
	fsp := newStableInfoTestPanel(0, 0, 60, 12, vfs.NewNullVFS(0), viewModesTestEntries())
	fsp.SetViewMode(ViewMode7) // N,S,N,S
	if got := len(fsp.Table.Columns); got != 4 {
		t.Fatalf("mode 7 has %d table columns, want 4", got)
	}
	if got := fsp.gridColumnCount(); got != 2 {
		t.Fatalf("mode 7 has %d stripes, want 2", got)
	}
	if !fsp.Table.CellSelection {
		t.Fatalf("a panel of two stripes must select cells")
	}
	for row, want := range []string{"1", "2", "3"} {
		if got := strings.TrimSpace(fsp.GetCellText(row, 1)); got != want {
			t.Errorf("size of row %d = %q, want %q", row, got, want)
		}
	}
	fsp.Table.SetFocus(true)
	fsp.SetCursorIndex(0)
	nameAttr := fsp.GetCellAttr(0, 0, 0)
	sizeAttr := fsp.GetCellAttr(0, 1, 0)
	emptyAttr := fsp.GetCellAttr(0, 3, 0)
	if nameAttr != sizeAttr {
		t.Errorf("cursor covers the name (%#x) but not the size (%#x) of its stripe", nameAttr, sizeAttr)
	}
	if sizeAttr == emptyAttr {
		t.Errorf("the empty second stripe is painted like the cursor (%#x)", emptyAttr)
	}
}

func TestNameMarkColumnShowsSelection(t *testing.T) {
	resetPanelViewModes(true)
	defer resetPanelViewModes(true)
	entries := viewModesTestEntries()
	entries[1].Selected = true
	fsp := newStableInfoTestPanel(0, 0, 60, 12, vfs.NewNullVFS(0), entries)
	fsp.SetViewMode(ViewMode0) // NM,SC,D
	if got := fsp.GetCellText(1, 0); !strings.HasPrefix(got, "√") {
		t.Errorf("selected row = %q, want a mark", got)
	}
	if got := fsp.GetCellText(0, 0); !strings.HasPrefix(got, " ") {
		t.Errorf("unselected row = %q, want a blank mark", got)
	}
}

func TestCustomizedModeChangesTheLayout(t *testing.T) {
	resetPanelViewModes(true)
	defer resetPanelViewModes(true)
	fsp := newStableInfoTestPanel(0, 0, 60, 12, vfs.NewNullVFS(0), viewModesTestEntries())
	fsp.SetViewMode(ViewModeDetailed)
	columns, err := TextToViewSettings("N,S,A", "0,6,10")
	if err != nil {
		t.Fatal(err)
	}
	panelViewModes.overrides[ViewModeDetailed] = &PanelViewSettings{Columns: columns}
	panelViewModes.generation++
	fsp.Resize(60, 12)
	if got := len(fsp.Table.Columns); got != 3 {
		t.Fatalf("customized Detailed has %d columns, want 3", got)
	}
	if got := fsp.Table.Columns[2].Width; got != 10 {
		t.Fatalf("attribute column width %d, want 10", got)
	}
}
