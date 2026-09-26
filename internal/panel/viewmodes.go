package panel

// File panel modes, ported from far2l: the column syntax of
// mix/panelmix.cpp (TextToViewSettings / ViewSettingsToText), the ten mode
// slots and their panel_modes.ini of panels/flmodes.cpp, and the width
// distribution of FileList::PrepareColumnWidths in panels/flshow.cpp.
//
// A mode is a list of columns. Several columns whose types repeat form
// stripes: "N,N,N" is three stripes of one name column (Brief), "N,S,N,S"
// two stripes of a name and a size. Files flow down a stripe and then on to
// the next one, exactly as Brief and Medium always did.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// The six slots far2l has beyond the four f4 always had. The numbers go on
// from the legacy enumeration, so a ViewMode already saved in settings.ini
// keeps naming the same mode.
const (
	ViewMode5 ViewMode = iota + ViewModeWide + 1
	ViewMode6
	ViewMode7
	ViewMode8
	ViewMode9
	ViewMode0
)

// PanelViewModeCount is the number of mode slots, Ctrl+0 .. Ctrl+9.
const PanelViewModeCount = 10

// viewModeKeys maps a ViewMode to the digit that selects it with Ctrl.
var viewModeKeys = [PanelViewModeCount]int{
	ViewModeMedium:   2,
	ViewModeDetailed: 3,
	ViewModeBrief:    1,
	ViewModeWide:     4,
	ViewMode5:        5,
	ViewMode6:        6,
	ViewMode7:        7,
	ViewMode8:        8,
	ViewMode9:        9,
	ViewMode0:        0,
}

// Valid reports whether m names one of the ten mode slots.
func (m ViewMode) Valid() bool { return m >= 0 && int(m) < PanelViewModeCount }

// Key is the digit that selects the mode with Ctrl, far2l's mode index.
func (m ViewMode) Key() int {
	if !m.Valid() {
		return -1
	}
	return viewModeKeys[m]
}

// ViewModeForKey is the mode Ctrl+key selects.
func ViewModeForKey(key int) (ViewMode, bool) {
	for mode, k := range viewModeKeys {
		if k == key {
			return ViewMode(mode), true
		}
	}
	return 0, false
}

// PanelColumnType is far2l's PANEL_COLUMN_TYPE, limited to the columns f4
// has data for.
type PanelColumnType int

const (
	NameColumn      PanelColumnType = iota // N
	SizeColumn                             // S
	PhysicalColumn                         // P
	DateColumn                             // D
	TimeColumn                             // T
	WDateColumn                            // DM, modification
	CDateColumn                            // DC, creation (status change on Unix)
	ADateColumn                            // DA, last access
	ChDateColumn                           // DE, change
	AttrColumn                             // A
	OwnerColumn                            // O
	GroupColumn                            // U
	LinkCountColumn                        // LN, far3: number of hard links
)

var panelColumnSymbols = map[PanelColumnType]string{
	NameColumn: "N", SizeColumn: "S", PhysicalColumn: "P", DateColumn: "D",
	TimeColumn: "T", WDateColumn: "DM", CDateColumn: "DC", ADateColumn: "DA",
	ChDateColumn: "DE", AttrColumn: "A", OwnerColumn: "O", GroupColumn: "U",
	LinkCountColumn: "LN",
}

// PanelColumnFlags are the one-letter modifiers that follow a column type.
type PanelColumnFlags uint32

const (
	ColumnMark       PanelColumnFlags = 1 << iota // N: M, a selection mark before the name
	ColumnNameOnly                                // N: O, kept for far2l compatibility
	ColumnRightAlign                              // N: R, kept for far2l compatibility
	ColumnCommas                                  // S/P: C, digit group separators
	ColumnEconomic                                // S/P: E, no space before the unit
	ColumnFloatSize                               // S/P: F, 1.25 K instead of 1 280
	ColumnThousand                                // S/P: T, units of 1000 instead of 1024
	ColumnAuto                                    // S/P: A, kept for far2l compatibility
	ColumnBrief                                   // D*: B, short date
	ColumnMonth                                   // D*: M, month as a name
	ColumnFullOwner                               // O: L, kept for far2l compatibility
)

type panelColumnModifier struct {
	letter byte
	flag   PanelColumnFlags
}

var (
	nameModifiers  = []panelColumnModifier{{'M', ColumnMark}, {'O', ColumnNameOnly}, {'R', ColumnRightAlign}}
	sizeModifiers  = []panelColumnModifier{{'C', ColumnCommas}, {'E', ColumnEconomic}, {'F', ColumnFloatSize}, {'T', ColumnThousand}, {'A', ColumnAuto}}
	dateModifiers  = []panelColumnModifier{{'B', ColumnBrief}, {'M', ColumnMonth}}
	ownerModifiers = []panelColumnModifier{{'L', ColumnFullOwner}}
)

func panelColumnModifiers(t PanelColumnType) []panelColumnModifier {
	switch t {
	case NameColumn:
		return nameModifiers
	case SizeColumn, PhysicalColumn:
		return sizeModifiers
	case WDateColumn, CDateColumn, ADateColumn, ChDateColumn:
		return dateModifiers
	case OwnerColumn:
		return ownerModifiers
	}
	return nil
}

// PanelColumn is one column of a mode: its type, modifiers and width. A zero
// Width takes the type's default width or, for the name column, a share of
// the space left over; Percent makes Width a share of that space.
type PanelColumn struct {
	Type    PanelColumnType
	Flags   PanelColumnFlags
	Width   int
	Percent bool
}

// PanelViewSettings is far2l's PanelViewSettings: the columns of a mode and
// whether it takes the whole screen.
type PanelViewSettings struct {
	Columns    []PanelColumn
	FullScreen bool
}

func (s PanelViewSettings) clone() PanelViewSettings {
	s.Columns = append([]PanelColumn(nil), s.Columns...)
	return s
}

// maxPanelColumns is far2l's PANEL_COLUMNCOUNT.
const maxPanelColumns = 20

// PanelColumnTypeError names the column type TextToViewSettings could not read.
type PanelColumnTypeError struct{ Token string }

func (e *PanelColumnTypeError) Error() string { return "unknown column type " + strconv.Quote(e.Token) }

// PanelColumnWidthError names the column width TextToViewSettings could not read.
type PanelColumnWidthError struct{ Token string }

func (e *PanelColumnWidthError) Error() string {
	return "invalid column width " + strconv.Quote(e.Token)
}

// ErrNoPanelColumns is returned for an empty column list.
var ErrNoPanelColumns = errors.New("no columns")

// TextToViewSettings parses far2l's column types ("N,SC,DM") and widths
// ("0,10,14"). A missing width is 0; a width ending in % is a share of the
// free space.
func TextToViewSettings(types, widths string) ([]PanelColumn, error) {
	var columns []PanelColumn
	for _, token := range strings.Split(types, ",") {
		token = strings.ToUpper(strings.TrimSpace(token))
		if token == "" {
			if strings.TrimSpace(types) == "" {
				break
			}
			return nil, &PanelColumnTypeError{Token: token}
		}
		column, ok := parsePanelColumnType(token)
		if !ok {
			return nil, &PanelColumnTypeError{Token: token}
		}
		if len(columns) == maxPanelColumns {
			break
		}
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		return nil, ErrNoPanelColumns
	}
	if strings.TrimSpace(widths) != "" {
		for i, token := range strings.Split(widths, ",") {
			if i >= len(columns) {
				break
			}
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			percent := strings.HasSuffix(token, "%")
			number, err := strconv.Atoi(strings.TrimSuffix(token, "%"))
			if err != nil || number < 0 || number > 1000 {
				return nil, &PanelColumnWidthError{Token: token}
			}
			columns[i].Width = number
			columns[i].Percent = percent && number > 0
		}
	}
	return columns, nil
}

func parsePanelColumnType(token string) (PanelColumn, bool) {
	var column PanelColumn
	var rest string
	switch {
	case token[0] == 'N':
		column.Type, rest = NameColumn, token[1:]
	case token[0] == 'S':
		column.Type, rest = SizeColumn, token[1:]
	case token[0] == 'P':
		column.Type, rest = PhysicalColumn, token[1:]
	case len(token) >= 2 && token[0] == 'D':
		switch token[1] {
		case 'M':
			column.Type = WDateColumn
		case 'C':
			column.Type = CDateColumn
		case 'A':
			column.Type = ADateColumn
		case 'E':
			column.Type = ChDateColumn
		default:
			return column, false
		}
		rest = token[2:]
	case token[0] == 'O':
		column.Type, rest = OwnerColumn, token[1:]
	case token == "LN":
		column.Type = LinkCountColumn
	case token == "D":
		column.Type = DateColumn
	case token == "T":
		column.Type = TimeColumn
	case token == "A":
		column.Type = AttrColumn
	case token == "U":
		column.Type = GroupColumn
	default:
		return column, false
	}
	modifiers := panelColumnModifiers(column.Type)
	for i := 0; i < len(rest); i++ {
		found := false
		for _, modifier := range modifiers {
			if rest[i] == modifier.letter {
				column.Flags |= modifier.flag
				found = true
				break
			}
		}
		if !found {
			return column, false
		}
	}
	return column, true
}

// ViewSettingsToText is the inverse of TextToViewSettings.
func ViewSettingsToText(columns []PanelColumn) (types, widths string) {
	var typeText, widthText strings.Builder
	for i, column := range columns {
		if i > 0 {
			typeText.WriteByte(',')
			widthText.WriteByte(',')
		}
		typeText.WriteString(panelColumnSymbols[column.Type])
		for _, modifier := range panelColumnModifiers(column.Type) {
			if column.Flags&modifier.flag != 0 {
				typeText.WriteByte(modifier.letter)
			}
		}
		widthText.WriteString(strconv.Itoa(column.Width))
		if column.Percent {
			widthText.WriteByte('%')
		}
	}
	return typeText.String(), widthText.String()
}

// panelColumnDefaultWidth is far2l's ColumnTypeWidth: the width a column
// declared with width 0 gets. 0 keeps it flexible.
func panelColumnDefaultWidth(column PanelColumn) int {
	switch column.Type {
	case SizeColumn, PhysicalColumn:
		return panelSizeColumnWidth
	case DateColumn:
		return 8
	case TimeColumn:
		return 5
	case WDateColumn, CDateColumn, ADateColumn, ChDateColumn:
		width := panelModifiedColumnWidth
		if column.Flags&ColumnBrief != 0 {
			width -= 3
		}
		if column.Flags&ColumnMonth != 0 {
			width++
		}
		return width
	case AttrColumn:
		return 10
	case OwnerColumn, GroupColumn:
		return 8
	case LinkCountColumn:
		return 4
	}
	return 0
}

// panelViewModeDefaults are the modes before the user touches them, indexed
// by the Ctrl digit. 1-4 are the four modes f4 always had, drawn exactly as
// before; the others follow far2l's slots where f4 has the data.
var panelViewModeDefaults = [PanelViewModeCount]struct {
	columns, widths string
	fullScreen      bool
}{
	0: {"NM,SC,D", "0,11,0", false},
	1: {"N,N,N", "0,0,0", false},
	2: {"N,N", "0,0", false},
	3: {"N,SC", "0,11", false},
	4: {"N,SC,DM", "0,11,14", true},
	5: {"N,SC,PC,DM,DC,DA,O,U,A", "0,11,11,14,14,14,8,8,10", true},
	6: {"N,SC,D,T", "0,11,0,0", false},
	7: {"N,S,N,S", "0,7,0,7", false},
	8: {"N,SC,O,U", "0,11,8,8", false},
	9: {"N,SC,A", "0,11,10", false},
}

var defaultPanelViewSettings = func() (settings [PanelViewModeCount]PanelViewSettings) {
	for mode := ViewMode(0); mode.Valid(); mode++ {
		spec := panelViewModeDefaults[mode.Key()]
		columns, err := TextToViewSettings(spec.columns, spec.widths)
		if err != nil {
			columns = []PanelColumn{{Type: NameColumn}}
		}
		settings[mode] = PanelViewSettings{Columns: columns, FullScreen: spec.fullScreen}
	}
	return settings
}()

// DefaultPanelViewSettings is the built-in definition of a mode.
func DefaultPanelViewSettings(mode ViewMode) PanelViewSettings {
	if !mode.Valid() {
		mode = ViewModeMedium
	}
	return defaultPanelViewSettings[mode].clone()
}

// panelViewModes holds the user's changes to the built-in modes. It is read
// and written on the UI goroutine only, like the panels that use it.
var panelViewModes struct {
	loaded    bool
	overrides [PanelViewModeCount]*PanelViewSettings
	// generation changes whenever a mode definition changes, so a panel can
	// tell that the layout it computed is stale.
	generation uint64
}

// PanelViewModeSettings is the current definition of a mode.
func PanelViewModeSettings(mode ViewMode) PanelViewSettings {
	ensurePanelViewModesLoaded()
	if !mode.Valid() {
		mode = ViewModeMedium
	}
	if override := panelViewModes.overrides[mode]; override != nil {
		return override.clone()
	}
	return defaultPanelViewSettings[mode].clone()
}

// PanelViewModeCustomized reports whether the user changed a mode.
func PanelViewModeCustomized(mode ViewMode) bool {
	ensurePanelViewModesLoaded()
	return mode.Valid() && panelViewModes.overrides[mode] != nil
}

// SetPanelViewModeSettings replaces a mode's definition; nil restores the
// built-in one. The change is saved to panel_modes.ini.
func SetPanelViewModeSettings(mode ViewMode, settings *PanelViewSettings) error {
	ensurePanelViewModesLoaded()
	if !mode.Valid() {
		return fmt.Errorf("invalid panel mode %d", mode)
	}
	if settings != nil {
		copied := settings.clone()
		settings = &copied
	}
	panelViewModes.overrides[mode] = settings
	panelViewModes.generation++
	return savePanelViewModes(PanelModesFilePath())
}

// PanelModesFilePath is panel_modes.ini beside bookmarks.ini, the file far2l
// keeps its modes in.
func PanelModesFilePath() string {
	return filepath.Join(filepath.Dir(BookmarksFilePath()), "panel_modes.ini")
}

func ensurePanelViewModesLoaded() {
	if panelViewModes.loaded {
		return
	}
	panelViewModes.loaded = true
	overrides, err := loadPanelViewModes(PanelModesFilePath())
	if err != nil {
		vtui.DebugLog("PANEL MODES: load failed: %v", err)
		return
	}
	panelViewModes.overrides = overrides
	panelViewModes.generation++
}

// resetPanelViewModes forgets the user's modes; tests use it to start clean.
func resetPanelViewModes(loaded bool) {
	panelViewModes.loaded = loaded
	panelViewModes.overrides = [PanelViewModeCount]*PanelViewSettings{}
	panelViewModes.generation++
}

const panelModesSectionPrefix = "Panel/ViewModes/Mode"

// loadPanelViewModes reads far2l's [Panel/ViewModes/ModeN] sections. A
// missing file is no error; a section whose columns do not parse keeps the
// built-in mode.
func loadPanelViewModes(path string) ([PanelViewModeCount]*PanelViewSettings, error) {
	var overrides [PanelViewModeCount]*PanelViewSettings
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return overrides, nil
		}
		return overrides, err
	}
	defer f.Close()

	type rawMode struct{ columns, widths, fullScreen string }
	var raw [PanelViewModeCount]*rawMode
	var current *rawMode
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = nil
			name := strings.TrimSpace(line[1 : len(line)-1])
			if key, err := strconv.Atoi(strings.TrimPrefix(name, panelModesSectionPrefix)); err == nil &&
				strings.HasPrefix(name, panelModesSectionPrefix) && key >= 0 && key < PanelViewModeCount {
				if raw[key] == nil {
					raw[key] = &rawMode{}
				}
				current = raw[key]
			}
			continue
		}
		if current == nil {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(name) {
		case "Columns":
			current.columns = strings.TrimSpace(value)
		case "ColumnWidths":
			current.widths = strings.TrimSpace(value)
		case "FullScreen":
			current.fullScreen = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return overrides, err
	}
	for key, mode := range raw {
		if mode == nil || mode.columns == "" {
			continue
		}
		viewMode, _ := ViewModeForKey(key)
		columns, err := TextToViewSettings(mode.columns, mode.widths)
		if err != nil {
			vtui.DebugLog("PANEL MODES: %s%d ignored: %v", panelModesSectionPrefix, key, err)
			continue
		}
		overrides[viewMode] = &PanelViewSettings{Columns: columns, FullScreen: mode.fullScreen == "1"}
	}
	return overrides, nil
}

func savePanelViewModes(path string) error {
	var buf strings.Builder
	for key := 0; key < PanelViewModeCount; key++ {
		mode, _ := ViewModeForKey(key)
		settings := panelViewModes.overrides[mode]
		if settings == nil {
			continue
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		types, widths := ViewSettingsToText(settings.Columns)
		fullScreen := 0
		if settings.FullScreen {
			fullScreen = 1
		}
		fmt.Fprintf(&buf, "[%s%d]\nColumns=%s\nColumnWidths=%s\nFullScreen=%d\n",
			panelModesSectionPrefix, key, types, widths, fullScreen)
	}
	if buf.Len() == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return config.WriteUserFileAtomically(path, []byte(buf.String()), 0o600)
}

// panelLayout is a mode fitted to a panel: the columns that fit with their
// final widths, and how they group into stripes.
type panelLayout struct {
	columns    []PanelColumn
	perStripe  int
	stripes    int
	mode       ViewMode
	generation uint64
	valid      bool
}

// preparePanelLayout is far2l's FileList::PrepareColumnWidths for a panel
// whose text area is textWidth cells wide.
func preparePanelLayout(settings []PanelColumn, textWidth int) panelLayout {
	columns := append([]PanelColumn(nil), settings...)
	if len(columns) == 0 {
		columns = []PanelColumn{{Type: NameColumn}}
	}
	if textWidth < 1 {
		textWidth = 1
	}
	zeroCount, percentTotal, percentCount := 0, 0, 0
	total := len(columns) - 1
	for i := range columns {
		column := &columns[i]
		if column.Width == 0 {
			column.Percent = false
			column.Width = panelColumnDefaultWidth(*column)
		}
		if column.Width == 0 {
			zeroCount++
		}
		if column.Percent {
			percentTotal += column.Width
			percentCount++
		} else {
			total += column.Width
		}
	}
	extra := textWidth - total
	if percentCount > 0 {
		percentExtra := extra
		if percentTotal <= 100 && zeroCount > 0 {
			percentExtra = extra * percentTotal / 100
		}
		used := 0
		for i := range columns {
			if !columns[i].Percent {
				continue
			}
			width := percentExtra - used
			if percentCount > 1 {
				width = percentExtra * columns[i].Width / percentTotal
			}
			width = max(width, 1)
			used += width
			columns[i].Width = width
			columns[i].Percent = false
			percentCount--
		}
		extra -= used
	}
	for i := range columns {
		if zeroCount == 0 {
			break
		}
		if columns[i].Width == 0 {
			width := max(extra/zeroCount, 1)
			columns[i].Width = width
			extra -= width
			zeroCount--
		}
	}
	for {
		total = len(columns) - 1
		for _, column := range columns {
			total += column.Width
		}
		if total <= textWidth {
			break
		}
		if len(columns) == 1 {
			columns[0].Width = textWidth
			break
		}
		last := len(columns) - 1
		if rest := total - columns[last].Width; textWidth-rest >= 1 {
			columns[last].Width = textWidth - rest
			break
		}
		columns = columns[:last]
	}
	perStripe, stripes := panelColumnStripes(columns)
	return panelLayout{columns: columns, perStripe: perStripe, stripes: stripes, valid: true}
}

// panelColumnStripes finds far2l's ColumnsInGlobal: the shortest group of
// column types that repeats across the whole row.
func panelColumnStripes(columns []PanelColumn) (perStripe, stripes int) {
	n := len(columns)
	if n == 0 {
		return 1, 1
	}
	for k := 1; k < n; k++ {
		if n%k != 0 {
			continue
		}
		repeats := true
		for i := k; i < n && repeats; i++ {
			repeats = columns[i].Type == columns[i-k].Type
		}
		if repeats {
			return k, n / k
		}
	}
	return n, 1
}

// panelViewModeIsSingleStripe reports whether a mode lists one file per row.
func panelViewModeIsSingleStripe(mode ViewMode) bool {
	_, stripes := panelColumnStripes(PanelViewModeSettings(mode).Columns)
	return stripes == 1
}

// SetWideViewMode makes mode the one the panel shows while it is widened
// over the whole screen.
func (fp *FileSystemPanel) SetWideViewMode(mode ViewMode) {
	fp.wideViewMode = mode
	fp.wideViewModeSet = true
}

// WideViewMode is the mode the panel shows while it is widened.
func (fp *FileSystemPanel) WideViewMode() ViewMode {
	if fp.wideViewModeSet && fp.wideViewMode.Valid() {
		return fp.wideViewMode
	}
	return ViewModeWide
}

// tableStateAttr repeats vtui.Table's choice of a cell's base colour, for
// the cells of a stripe that vtui does not know belong to the cursor.
func (fp *FileSystemPanel) tableStateAttr(onCursor, selected bool) uint64 {
	t := fp.Table
	switch {
	case onCursor && t.IsFocused() && selected:
		return vtui.Palette[t.ColorItemSelectCursorIdx]
	case onCursor && t.IsFocused():
		return vtui.Palette[t.ColorSelectedTextIdx]
	case selected:
		return vtui.Palette[t.ColorItemSelectTextIdx]
	case onCursor && t.AlwaysShowCursor:
		return vtui.Palette[t.ColorSelectedTextIdx]
	}
	return vtui.Palette[t.ColorTextIdx]
}

func (fp *FileSystemPanel) layoutValid() bool {
	return fp.layout.valid && fp.layout.mode == fp.EffectiveViewMode() &&
		fp.layout.generation == panelViewModes.generation
}

// columnStripes reports how many table columns one stripe has and how many
// stripes the panel shows.
func (fp *FileSystemPanel) columnStripes() (perStripe, stripes int) {
	if fp.layoutValid() {
		return max(fp.layout.perStripe, 1), max(fp.layout.stripes, 1)
	}
	return panelColumnStripes(PanelViewModeSettings(fp.EffectiveViewMode()).Columns)
}

// stripeOfColumn is the stripe a table column belongs to.
func (fp *FileSystemPanel) stripeOfColumn(col int) int {
	perStripe, _ := fp.columnStripes()
	if col < 0 || perStripe <= 1 {
		return col
	}
	return col / perStripe
}

// stripeWidth is the width of a stripe's columns and the separators between
// them.
func (fp *FileSystemPanel) stripeWidth(stripe int) int {
	perStripe, _ := fp.columnStripes()
	width := 0
	for i := 0; i < perStripe; i++ {
		col := stripe*perStripe + i
		if col >= len(fp.Table.Columns) {
			break
		}
		if i > 0 {
			width++
		}
		width += fp.Table.Columns[col].Width
	}
	return width
}

// panelColumnAt is the definition of table column col, with its width.
func (fp *FileSystemPanel) panelColumnAt(col int) PanelColumn {
	if fp.layoutValid() && col >= 0 && col < len(fp.layout.columns) {
		return fp.layout.columns[col]
	}
	column := PanelColumn{Type: NameColumn}
	columns := PanelViewModeSettings(fp.EffectiveViewMode()).Columns
	if perStripe, _ := panelColumnStripes(columns); col >= 0 && len(columns) > 0 {
		column = columns[col%perStripe]
	}
	column.Width = 0
	if col >= 0 && col < len(fp.Table.Columns) {
		column.Width = fp.Table.Columns[col].Width
	}
	return column
}

// panelTableColumns turns a layout into the table's columns.
func panelTableColumns(layout panelLayout) []vtui.TableColumn {
	columns := make([]vtui.TableColumn, len(layout.columns))
	for i, column := range layout.columns {
		columns[i] = vtui.TableColumn{Title: panelColumnTitle(column.Type), Width: column.Width}
		if column.Type == SizeColumn || column.Type == PhysicalColumn {
			columns[i].Alignment = vtui.AlignRight
		}
	}
	return columns
}

func panelColumnTitle(t PanelColumnType) string {
	switch t {
	case SizeColumn:
		return i18n.Msg("Panel.Column.Size")
	case PhysicalColumn:
		return i18n.Msg("Panel.Column.Physical")
	case DateColumn:
		return i18n.Msg("Panel.Column.Date")
	case TimeColumn:
		return i18n.Msg("Panel.Column.Time")
	case WDateColumn:
		return i18n.Msg("Panel.Column.Modified")
	case CDateColumn:
		return i18n.Msg("Panel.Column.Created")
	case ADateColumn:
		return i18n.Msg("Panel.Column.Accessed")
	case ChDateColumn:
		return i18n.Msg("Panel.Column.Changed")
	case AttrColumn:
		return i18n.Msg("Panel.Column.Attributes")
	case OwnerColumn:
		return i18n.Msg("Panel.Column.Owner")
	case GroupColumn:
		return i18n.Msg("Panel.Column.Group")
	case LinkCountColumn:
		return i18n.Msg("Panel.Column.LinkCount")
	}
	return i18n.Msg("Panel.Column.Name")
}

// panelColumnSortMode is the sort mode a click on the column's header picks.
func panelColumnSortMode(t PanelColumnType) (SortMode, bool) {
	switch t {
	case NameColumn:
		return SortName, true
	case SizeColumn, PhysicalColumn:
		return SortSize, true
	case WDateColumn, DateColumn, TimeColumn:
		return SortTime, true
	}
	return SortUnsorted, false
}

// columnCellText renders entry e in table column col.
func (fp *FileSystemPanel) columnCellText(e *FileEntry, col int) string {
	column := fp.panelColumnAt(col)
	switch column.Type {
	case NameColumn:
		if column.Flags&ColumnMark != 0 && column.Width > 1 {
			mark := " "
			if e.Selected && e.Name != ".." {
				mark = "√"
			}
			return mark + formatPanelFileNameAt(e, column.Width-1, fp.nameLeftPos)
		}
		return formatPanelFileNameAt(e, column.Width, fp.nameLeftPos)
	case SizeColumn, PhysicalColumn:
		return panelSizeColumnText(e, column)
	case DateColumn, TimeColumn, WDateColumn:
		return formatPanelColumnTime(e.MTime, column, time.Now())
	case CDateColumn, ChDateColumn:
		return formatPanelColumnTime(e.CTime, column, time.Now())
	case ADateColumn:
		return formatPanelColumnTime(e.ATime, column, time.Now())
	case AttrColumn:
		return panelAttributesText(e)
	case OwnerColumn:
		return panelOwnerText(fp.Vfs, e, false)
	case GroupColumn:
		return panelOwnerText(fp.Vfs, e, true)
	case LinkCountColumn:
		return panelLinkCountText(e)
	}
	return ""
}

// panelLinkCountText is far3's "LN" column: the number of hard links to the
// file. Empty when the VFS never reported it, same as the other optional
// metadata columns (owner, attributes).
func panelLinkCountText(e *FileEntry) string {
	if !e.HasMetadata(vfs.MetadataNlink) {
		return ""
	}
	return strconv.FormatUint(e.Nlink, 10)
}

// panelSizeColumnText is far2l's FormatStr_Size: folders, links and ".."
// name their kind, files show their size scaled to fit the column.
func panelSizeColumnText(e *FileEntry, column PanelColumn) string {
	if e.IsDir && e.SizeCalculated {
		return formatPanelSize(uint64(max(e.Size, 0)), column.Width, column.Flags)
	}
	// Folders, links and ".." name their kind the way the legacy Size
	// column did; only a plain file's own number is scaled below.
	if label := entrySizeText(e); e.Name == ".." || e.IsDir || label != fileops.FormatIntWithSpaces(e.Size) {
		return label
	}
	size := e.Size
	if column.Type == PhysicalColumn {
		if !e.HasMetadata(vfs.MetadataPhysicalSize) {
			return ""
		}
		size = e.PhysicalSize
	}
	return formatPanelSize(uint64(max(size, 0)), column.Width, column.Flags)
}

const panelSizeUnits = "BKMGTPE"

// formatPanelSize is far2l's FileSizeToStr: the exact number when it fits
// the column, otherwise the number in the smallest unit that does.
func formatPanelSize(size uint64, width int, flags PanelColumnFlags) string {
	divider := uint64(1024)
	if flags&ColumnThousand != 0 {
		divider = 1000
	}
	unitSeparator := " "
	if flags&ColumnEconomic != 0 {
		unitSeparator = ""
	}
	number := func(n uint64) string {
		if flags&ColumnCommas != 0 {
			return fileops.FormatIntWithSpaces(int64(n))
		}
		return strconv.FormatUint(n, 10)
	}
	if flags&ColumnFloatSize != 0 {
		unit, scale := 0, uint64(1)
		for bound := uint64(1000); unit < len(panelSizeUnits)-1 && size >= bound; unit++ {
			scale *= divider
			if bound > ^uint64(0)/1000 {
				unit++
				break
			}
			bound *= 1000
		}
		if unit == 0 {
			return number(size)
		}
		value := float64(size) / float64(scale)
		decimals := 2
		if flags&ColumnEconomic != 0 && value > 9 {
			decimals = 1
		}
		return strconv.FormatFloat(value, 'f', decimals, 64) + unitSeparator + panelSizeUnits[unit:unit+1]
	}
	text := number(size)
	if runewidth.StringWidth(text) <= width || width < 5 {
		return text
	}
	fit := width - 1 - len(unitSeparator)
	for unit := 1; unit < len(panelSizeUnits); unit++ {
		rest := size % divider
		size /= divider
		if rest > divider/2 {
			size++
		}
		text = number(size)
		if runewidth.StringWidth(text) <= fit || unit == len(panelSizeUnits)-1 {
			return text + unitSeparator + panelSizeUnits[unit:unit+1]
		}
	}
	return text
}

// formatPanelColumnTime renders a time for a date or time column, using the
// longest layout the column has room for.
func formatPanelColumnTime(t time.Time, column PanelColumn, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	var layouts []string
	switch {
	case column.Type == DateColumn:
		layouts = []string{"02.01.2006", "02.01.06"}
	case column.Type == TimeColumn:
		layouts = []string{"15:04:05", "15:04"}
	case column.Flags&ColumnBrief != 0:
		if t.Year() == now.Year() {
			return t.Format("02.01 15:04")
		}
		return t.Format("02.01  2006")
	case column.Flags&ColumnMonth != 0:
		layouts = []string{"02 Jan 2006 15:04:05", "02 Jan 06 15:04:05", "02 Jan 2006 15:04", "02 Jan 06 15:04"}
	default:
		layouts = []string{"02.01.2006 15:04:05", "02.01.06 15:04:05", "02.01.2006 15:04", "02.01.06 15:04"}
	}
	for _, layout := range layouts {
		if len(layout) <= column.Width {
			return t.Format(layout)
		}
	}
	return t.Format(layouts[len(layouts)-1])
}

// panelAttributesText is far2l's FormatStr_Attribute: Unix permissions as
// ls prints them, or the Windows attribute letters.
func panelAttributesText(e *FileEntry) string {
	if e.Name == ".." {
		return ""
	}
	if e.HasMetadata(vfs.MetadataPermissions) {
		return unixPermissionsText(e.UnixMode, e.IsDir, e.IsSymlink)
	}
	if e.HasMetadata(vfs.MetadataWinAttrs) {
		return windowsAttributesText(e.WinAttrs)
	}
	return e.Mode
}

func unixPermissionsText(mode uint32, isDir, isSymlink bool) string {
	out := []byte("----------")
	switch {
	case isSymlink:
		out[0] = 'l'
	case isDir:
		out[0] = 'd'
	}
	const rwx = "rwxrwxrwx"
	for i := 0; i < 9; i++ {
		if mode&(1<<uint(8-i)) != 0 {
			out[i+1] = rwx[i]
		}
	}
	special := func(pos int, bit uint32, set, unset byte) {
		if mode&bit == 0 {
			return
		}
		if out[pos] == 'x' {
			out[pos] = set
		} else {
			out[pos] = unset
		}
	}
	special(3, 0o4000, 's', 'S')
	special(6, 0o2000, 's', 'S')
	special(9, 0o1000, 't', 'T')
	return string(out)
}

func windowsAttributesText(attrs uint32) string {
	letter := func(mask uint32, c byte) byte {
		if attrs&mask != 0 {
			return c
		}
		return ' '
	}
	out := []byte{
		letter(0x1, 'R'), letter(0x4, 'S'), letter(0x2, 'H'), letter(0x20, 'A'),
		letter(0x400, 'L'), letter(0x800, 'C'), letter(0x100, 'T'),
		letter(0x2000, 'I'), letter(0x1000, 'O'), letter(0x10000, 'V'),
	}
	if out[4] == ' ' && attrs&0x200 != 0 {
		out[4] = '$'
	}
	if out[5] == ' ' && attrs&0x4000 != 0 {
		out[5] = 'E'
	}
	return string(out)
}

var panelOwnerNames sync.Map // "u:<id>" or "g:<id>" -> name

// panelOwnerText names the owner or group of an entry. Names are looked up
// only for the local filesystem: a remote host's ids mean nothing in the
// local user database, so they stay numbers.
func panelOwnerText(filesystem vfs.VFS, e *FileEntry, group bool) string {
	if e.Name == ".." {
		return ""
	}
	id, field, prefix := e.Uid, vfs.MetadataUID, "u:"
	if group {
		id, field, prefix = e.Gid, vfs.MetadataGID, "g:"
	}
	if !e.HasMetadata(field) {
		return ""
	}
	text := strconv.Itoa(id)
	if _, local := filesystem.(*vfs.OSVFS); !local {
		return text
	}
	if cached, ok := panelOwnerNames.Load(prefix + text); ok {
		return cached.(string)
	}
	name := text
	if group {
		if g, err := user.LookupGroupId(text); err == nil {
			name = g.Name
		}
	} else if u, err := user.LookupId(text); err == nil {
		name = u.Username
	}
	panelOwnerNames.Store(prefix+text, name)
	return name
}
