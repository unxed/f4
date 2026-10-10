package panel

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/sysinfo"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/media"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/wheel"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// FileEntry implements vtui.TableRow for display in a table.
type FileEntry struct {
	vfs.VFSItem
	Selected       bool
	PrevSelected   bool // snapshot of Selected taken by SaveSelection; swapped in by RestoreSelection (Ctrl+M)
	SizeCalculated bool
	IsCached       bool
	sourceOrder    uint64
	// linkTarget is what a symlink points at, filled by symlinkTarget; the
	// two flags say whether the answer is in or still on its way.
	linkTarget   string
	linkResolved bool
	linkPending  bool
}
type mediumRow struct {
	fp *FileSystemPanel
	r  int
}

func (m *mediumRow) GetCellText(col int) string {
	idx := m.fp.entryIndex(m.r, col)
	if idx < 0 || idx >= len(m.fp.Entries) {
		return ""
	}
	e := m.fp.Entries[idx]
	width := 0
	if col >= 0 && col < len(m.fp.Table.Columns) {
		width = m.fp.Table.Columns[col].Width
	}
	return formatPanelFileNameAtWithOptions(e, width, m.fp.nameLeftPos, m.fp.uppercasePanelDirs())
}

type panelMatchSpan struct {
	start int
	width int
}

func (fp *FileSystemPanel) RowCount() int {
	return fp.displayCount() + fp.stickyGroupRows(fp.Table.TopPos)
}

func (fp *FileSystemPanel) GetCellText(row, col int) string {
	idx := fp.entryIndex(row, col)
	if idx < 0 || idx >= len(fp.Entries) {
		return ""
	}
	return fp.columnCellText(fp.Entries[idx], col)
}

// IsCellSelected implements vtui.TableCellColSelectProvider. Unlike a plain
// row number, (row, col) is enough to resolve exactly which entry sits here
// even in multi file-column view modes (Medium, Brief), where one row is
// shared by several columns via the same row+col*H math GetCellAttr uses.
func (fp *FileSystemPanel) IsCellSelected(row, col int) bool {
	idx := fp.entryIndex(row, col)
	if idx < 0 || idx >= len(fp.Entries) {
		return false
	}
	return fp.Entries[idx].IsSelected()
}

// entryIndex resolves the fp.entries index shown at (row, col) for the
// current view mode: every column of a stripe shows the same file, and the
// stripes are file-columns of height ViewHeight (Medium/Brief).
func (fp *FileSystemPanel) entryIndex(row, col int) int {
	return fp.entryAtDisplay(fp.viewportDisplayRow(row, fp.stripeOfColumn(col)))
}

func (fp *FileSystemPanel) GetCellAttr(row, col int, defaultAttr uint64) uint64 {
	idx := fp.entryIndex(row, col)
	inRange := idx >= 0 && idx < len(fp.Entries)
	if perStripe, stripes := fp.columnStripes(); perStripe > 1 && stripes > 1 && fp.Table.CellSelection {
		// vtui paints the cursor on the one column SelectCol names; a stripe
		// of several columns (N,S,N,S) carries it across all of them.
		onCursor := inRange && row == fp.Table.SelectPos && fp.stripeOfColumn(col) == fp.Table.SelectCol
		defaultAttr = fp.tableStateAttr(onCursor, inRange && fp.Entries[idx].Selected)
	}
	if !inRange {
		return defaultAttr
	}
	return fp.Entries[idx].GetCellAttr(col, defaultAttr)
}

func (f *FileEntry) displayName(name string) string {
	return f.displayNameWithOptions(name, false)
}

func (f *FileEntry) displayNameWithOptions(name string, uppercaseDirs bool) string {
	if f.Name == ".." {
		return ".."
	}
	// A name ReadDir/Stat mapped into the private-use range (vfs/pua.go,
	// because it wasn't valid UTF-8 on disk) carries every original byte,
	// but the private-use codepoints it uses have no glyph in any font.
	// f.Name itself stays mapped for selection, sorting, path construction
	// and persistence; only the copy painted to the screen is unmapped back
	// to the original bytes here, so it renders exactly as an unmapped raw
	// name already does (vtui substitutes "?" for whatever it still can't
	// decode, same as before this mapping existed).
	name = vfs.DisplayName(name)
	if uppercaseDirs && f.IsDir {
		name = strings.ToUpper(name)
	}
	marker := ""
	if config.App.ShowHighlightMarks {
		marker = theme.GlobalFileHighlighter.GetMarker(&f.VFSItem)
	}
	// A symlink no highlight rule marks can carry an arrow instead. It is off
	// by default: the panel reads as plain names unless ShowSymlinkArrow asks
	// for the marker.
	if marker == "" && f.IsSymlink && config.App.ShowSymlinkArrow {
		marker = "→"
	}
	prefix := ""
	if f.IsDir {
		if config.App.ShowDirPrefix {
			if marker == "/" {
				marker = ""
			}
			prefix = "/"
		} else {
			if marker == "/" {
				marker = ""
			}
		}
	}
	if marker != "" {
		name = marker + " " + name
	}
	return prefix + name
}

func splitFileExtension(name string) (string, string) {
	lastDot := strings.LastIndex(name, ".")
	if lastDot <= 0 || lastDot == len(name)-1 {
		return name, ""
	}
	return name[:lastDot], name[lastDot+1:]
}

func shouldSeparatePanelExtension(entry *FileEntry) bool {
	return config.App.SeparateFileExtensions && !entry.IsDir && !entry.NoExtension && entry.Name != ".."
}

func formatPanelFileName(entry *FileEntry, width int) string {
	return formatPanelFileNameAt(entry, width, 0)
}

// panelNameOverflow reports how many display cells of entry's name do not
// fit into a name column of the given width, i.e. how far the name can be
// scrolled to the right before its end comes into view. It is 0 for names
// that fit. In separate-extensions mode the extension keeps its right-aligned
// field, so only the base name counts.
func panelNameOverflow(entry *FileEntry, width int) int {
	return panelNameOverflowWithOptions(entry, width, false)
}

func panelNameOverflowWithOptions(entry *FileEntry, width int, uppercaseDirs bool) int {
	if width <= 0 {
		return 0
	}
	if shouldSeparatePanelExtension(entry) {
		if base, extension := splitFileExtension(entry.Name); extension != "" {
			baseWidth := width - panelExtensionFieldWidth(extension) - 1
			if baseWidth <= 0 {
				return 0
			}
			return max(runewidth.StringWidth(entry.displayNameWithOptions(base, uppercaseDirs))-baseWidth, 0)
		}
	}
	return max(runewidth.StringWidth(entry.displayNameWithOptions(entry.Name, uppercaseDirs))-width, 0)
}

// panelNameShift clamps a panel-wide scroll position to what this particular
// name can absorb: a name that fits its column never moves, a longer one
// stops once its last cell is visible (far2l's MakeCurLeftPos).
func panelNameShift(entry *FileEntry, width, leftPos int) int {
	return panelNameShiftWithOptions(entry, width, leftPos, false)
}

func panelNameShiftWithOptions(entry *FileEntry, width, leftPos int, uppercaseDirs bool) int {
	if leftPos <= 0 {
		return 0
	}
	return min(leftPos, panelNameOverflowWithOptions(entry, width, uppercaseDirs))
}

// scrollPanelName drops shift leading display cells from a name. A wide
// character straddling the cut is replaced by a space so the visible
// columns stay aligned.
func scrollPanelName(name string, shift int) string {
	if shift <= 0 {
		return name
	}
	if runewidth.StringWidth(name) <= shift {
		return ""
	}
	return runewidth.TruncateLeft(name, shift, "")
}

func panelExtensionFieldWidth(extension string) int {
	return max(runewidth.StringWidth(extension), 3)
}

// formatPanelFileNameAt renders the name column cell for entry with the
// panel's name scroll position applied (see nameLeftPos). Only the part of
// the name that overflows the column can scroll out of view on the left.
func formatPanelFileNameAt(entry *FileEntry, width, leftPos int) string {
	return formatPanelFileNameAtWithOptions(entry, width, leftPos, false)
}

func formatPanelFileNameAtWithOptions(entry *FileEntry, width, leftPos int, uppercaseDirs bool) string {
	if !shouldSeparatePanelExtension(entry) || width <= 0 {
		return scrollPanelName(entry.displayNameWithOptions(entry.Name, uppercaseDirs), panelNameShiftWithOptions(entry, width, leftPos, uppercaseDirs))
	}
	base, extension := splitFileExtension(entry.Name)
	if extension == "" {
		return scrollPanelName(entry.displayNameWithOptions(entry.Name, uppercaseDirs), panelNameShiftWithOptions(entry, width, leftPos, uppercaseDirs))
	}

	extensionWidth := runewidth.StringWidth(extension)
	extensionFieldWidth := extensionWidth
	if extensionFieldWidth < 3 {
		extensionFieldWidth = 3
	}
	if extensionFieldWidth >= width {
		if extensionFieldWidth > extensionWidth {
			return runewidth.Truncate(extension+strings.Repeat(" ", extensionFieldWidth-extensionWidth), width, "")
		}
		return runewidth.Truncate(extension, width, "")
	}
	left := scrollPanelName(entry.displayNameWithOptions(base, uppercaseDirs), panelNameShiftWithOptions(entry, width, leftPos, uppercaseDirs))
	left = runewidth.Truncate(left, width-extensionFieldWidth-1, "")
	leftWidth := runewidth.StringWidth(left)
	padding := width - leftWidth - extensionFieldWidth
	if padding < 0 {
		padding = 0
	}
	extPadding := extensionFieldWidth - extensionWidth
	if extPadding < 0 {
		extPadding = 0
	}

	var sb strings.Builder
	sb.Grow(len(left) + padding + len(extension) + extPadding)
	sb.WriteString(left)
	for i := 0; i < padding; i++ {
		sb.WriteByte(' ')
	}
	sb.WriteString(extension)
	for i := 0; i < extPadding; i++ {
		sb.WriteByte(' ')
	}
	return sb.String()
}

func clippedPanelMatchSpan(start, width, cellWidth int) (panelMatchSpan, bool) {
	if start < 0 {
		width += start
		start = 0
	}
	if start >= cellWidth || width <= 0 {
		return panelMatchSpan{}, false
	}
	if start+width > cellWidth {
		width = cellWidth - start
	}
	return panelMatchSpan{start: start, width: width}, width > 0
}

func panelFileNameMatchSpans(entry *FileEntry, width, matchStartRunes, matchedRunes int) []panelMatchSpan {
	return panelFileNameMatchSpansAt(entry, width, 0, matchStartRunes, matchedRunes)
}

// panelFileNameMatchSpansAt is panelFileNameMatchSpans for a name column
// scrolled by leftPos cells: the highlighted cells move left together with
// the name (the right-aligned extension field does not scroll), and spans
// that scrolled out of the column are clipped away.
func panelFileNameMatchSpansAt(entry *FileEntry, width, leftPos, matchStartRunes, matchedRunes int) []panelMatchSpan {
	if matchStartRunes < 0 || matchedRunes <= 0 || width <= 0 {
		return nil
	}
	shift := panelNameShift(entry, width, leftPos)
	nameRunes := []rune(entry.Name)
	if matchStartRunes >= len(nameRunes) {
		return nil
	}
	matchEndRunes := matchStartRunes + matchedRunes
	if matchEndRunes > len(nameRunes) {
		matchEndRunes = len(nameRunes)
	}
	prefixWidth := 0
	if entry.Name != ".." {
		prefixWidth = runewidth.StringWidth(entry.displayName(""))
	}

	if !shouldSeparatePanelExtension(entry) {
		if span, ok := clippedPanelMatchSpan(
			prefixWidth+runewidth.StringWidth(string(nameRunes[:matchStartRunes]))-shift,
			runewidth.StringWidth(string(nameRunes[matchStartRunes:matchEndRunes])), width,
		); ok {
			return []panelMatchSpan{span}
		}
		return nil
	}

	base, extension := splitFileExtension(entry.Name)
	if extension == "" {
		if span, ok := clippedPanelMatchSpan(
			prefixWidth+runewidth.StringWidth(string(nameRunes[:matchStartRunes]))-shift,
			runewidth.StringWidth(string(nameRunes[matchStartRunes:matchEndRunes])), width,
		); ok {
			return []panelMatchSpan{span}
		}
		return nil
	}

	baseRunes := []rune(base)
	extensionRunes := []rune(extension)
	extensionFieldWidth := runewidth.StringWidth(extension)
	if extensionFieldWidth < 3 {
		extensionFieldWidth = 3
	}

	spans := make([]panelMatchSpan, 0, 2)
	baseMatchStart := matchStartRunes
	if baseMatchStart < 0 {
		baseMatchStart = 0
	}
	baseMatchEnd := matchEndRunes
	if baseMatchEnd > len(baseRunes) {
		baseMatchEnd = len(baseRunes)
	}
	if baseMatchStart < baseMatchEnd && extensionFieldWidth < width {
		leftWidth := width - extensionFieldWidth - 1
		if span, ok := clippedPanelMatchSpan(
			prefixWidth+runewidth.StringWidth(string(baseRunes[:baseMatchStart]))-shift,
			runewidth.StringWidth(string(baseRunes[baseMatchStart:baseMatchEnd])), leftWidth,
		); ok {
			spans = append(spans, span)
		}
	}

	// The separating dot is intentionally hidden when extensions are aligned.
	// Continue highlighting at the right-aligned extension after that dot.
	extensionNameStart := len(baseRunes) + 1
	extensionMatchStart := matchStartRunes - extensionNameStart
	if extensionMatchStart < 0 {
		extensionMatchStart = 0
	}
	extensionMatchEnd := matchEndRunes - extensionNameStart
	if extensionMatchEnd > len(extensionRunes) {
		extensionMatchEnd = len(extensionRunes)
	}
	if extensionMatchStart < extensionMatchEnd {
		extensionStart := width - extensionFieldWidth
		if extensionStart < 0 {
			extensionStart = 0
		}
		if span, ok := clippedPanelMatchSpan(
			extensionStart+runewidth.StringWidth(string(extensionRunes[:extensionMatchStart])),
			runewidth.StringWidth(string(extensionRunes[extensionMatchStart:extensionMatchEnd])), width,
		); ok {
			spans = append(spans, span)
		}
	}
	return spans
}

func (m *mediumRow) IsColSelected(col int) bool {
	idx := m.fp.entryIndex(m.r, col)
	if idx < 0 || idx >= len(m.fp.Entries) {
		return false
	}
	return m.fp.Entries[idx].Selected
}
func (m *mediumRow) GetCellAttr(col int, defaultAttr uint64) uint64 {
	idx := m.fp.entryIndex(m.r, col)
	if idx < 0 || idx >= len(m.fp.Entries) {
		return defaultAttr
	}
	e := m.fp.Entries[idx]
	attr := defaultAttr
	isCursor := (defaultAttr == vtui.Palette[theme.ColPanelCursor] || defaultAttr == vtui.Palette[theme.ColPanelSelectedCursor] || defaultAttr == vtui.Palette[theme.ColPanelInactiveCursor] || defaultAttr == vtui.Palette[theme.ColPanelInactiveSelectedCursor])

	attr = theme.GlobalFileHighlighter.GetColor(&e.VFSItem, attr, e.Selected, isCursor)

	return attr
}

type ViewMode int

const (
	ViewModeMedium ViewMode = iota
	ViewModeDetailed
	ViewModeBrief
	ViewModeWide
)

const (
	panelSizeColumnWidth      = 11
	panelModifiedColumnWidth  = 14
	panelDragScrollInterval   = 75 * time.Millisecond
	panelLoadingPulseInterval = 100 * time.Millisecond
	// panelLoadingShowDelay hides the loading pulse until an operation has run
	// at least this long. Quick loads (fast directory reads, snappy VFS mounts)
	// then finish without ever showing the spinner, avoiding a brief flash.
	panelLoadingShowDelay = 200 * time.Millisecond
)

var panelLoadingPulse = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type SortMode int

const (
	SortName SortMode = iota
	SortExt
	SortTime
	SortSize
	SortUnsorted
)

func (f *FileEntry) IsSelected() bool {
	return f.Selected
}

func (f *FileEntry) GetCellText(col int) string {
	switch col {
	case 0:
		return f.displayName(f.Name)
	case 1:
		return entrySizeText(f)
	case 2:
		if f.MTime.IsZero() {
			return ""
		}
		return f.MTime.Format("02.01.06 15:04")
	}
	return ""
}
func (f *FileEntry) GetCellAttr(col int, defaultAttr uint64) uint64 {
	attr := defaultAttr
	isCursor := (defaultAttr == vtui.Palette[theme.ColPanelCursor] || defaultAttr == vtui.Palette[theme.ColPanelSelectedCursor] || defaultAttr == vtui.Palette[theme.ColPanelInactiveCursor] || defaultAttr == vtui.Palette[theme.ColPanelInactiveSelectedCursor])

	attr = theme.GlobalFileHighlighter.GetColor(&f.VFSItem, attr, f.Selected, isCursor)

	return attr
}

// FileSystemPanel is a panel displaying files on disk.
const maxDirCache = 50

type DirCacheEntry struct {
	Items       []vfs.VFSItem
	time        time.Time
	showUpEntry bool
}

// dirCacheKey qualifies a path with the filesystem it belongs to. Remote
// roots are commonly all named "/"; using the path alone briefly showed one
// Android device's cached directory while another device was being loaded.
// A shared-session identity preserves the fast preview across pooled FISH+
// views. Other comparable VFS implementations are scoped to their exact
// instance, giving a safe cache miss rather than a false hit after reopening.
type dirCacheKey struct {
	identity      any
	qualifiedPath string
}

type FileSystemPanel struct {
	selMemo selectionMemo

	// folderEventPath is the folder the FolderChanged macro event was last
	// raised for, so a refresh of the same folder does not raise it again.
	folderEventPath        string
	GroupBy                GroupMode
	GroupReverse           bool
	GroupFoldersSeparately bool
	groupKeys              map[*FileEntry]groupKey
	displayRows            []displayRow
	entryRows              []int
	visibleGroups          []PanelGroup
	groupDate              string
	groupLimits            [3]int64
	nextSourceOrder        uint64
	vtui.ScreenObject
	Table                 *vtui.Table
	scrollBar             *vtui.ScrollBar
	scrollMouseActive     bool
	minimalScrollDragGap  int
	headerMouseActive     bool
	columnResizeActive    bool
	columnResizeIndex     int
	columnResizeStartX    int
	columnResizeLeftWidth int
	columnResizeRightW    int
	Frame                 *vtui.BorderedFrame
	Vfs                   vfs.VFS
	Entries               []*FileEntry
	SelectedItems         map[string]bool
	previousSelection     map[string]bool
	previousSelectionVFS  vfs.VFS
	previousSelectionPath string
	selectionEpoch        map[string]uint64
	selectionEpochNext    uint64
	DirectoryEpoch        uint64
	ViewMode              ViewMode
	Wide                  bool
	// wideViewMode is the mode shown while Wide is on (see WideViewMode);
	// layout is the current mode fitted to the panel's width.
	wideViewMode         ViewMode
	wideViewModeSet      bool
	layout               panelLayout
	CursorIdx            int
	lastRightClickedIdx  int
	rightDragActive      bool
	rightDragSelect      bool
	rowDragButton        uint32
	dragScrollDirection  int
	dragScrollTimer      *time.Timer
	dragScrollGeneration uint64
	wheel                wheel.Coast

	loadCtx        context.Context
	CancelLoad     context.CancelFunc
	IsLoading      bool
	LoadingTimer   *time.Timer
	loadingFrame   int
	loadingVisible bool

	LoadingGeneration uint64
	loadQueueMu       sync.Mutex
	// loadIdleCh is closed while no directory-load worker is running for this
	// panel, and replaced with a fresh (open) channel exactly when one starts;
	// both transitions happen under loadQueueMu. WaitForIdle blocks on it
	// instead of a WaitGroup: EnqueueDirectoryLoad only Adds once per "round"
	// (a fresh worker started while none was active), and reusing a WaitGroup
	// across independent rounds like that lets a fresh Add race a Wait that
	// is still unblocking from the round before it -- sync.WaitGroup's own
	// doc comment calls this out. That is exactly what -race caught: a
	// leftover AI download's RefreshAll re-armed the queue while a test
	// helper's join goroutine was still returning from the round that had
	// just finished.
	loadIdleCh           chan struct{}
	loadWorkerActive     bool
	pendingDirectoryLoad func()
	// pendingDetachable and runningDetachable say a load may be left behind
	// once cancelled (a local read that can hang on a sudo password,
	// f4#1411); loadWorkerGen moves on when one is, so its worker stops.
	pendingDetachable          bool
	runningDetachable          bool
	loadWorkerGen              uint64
	ProviderOpenTask           *vtui.TaskContext
	directoryErrorDialog       *vtui.Window
	ProviderOpenTarget         string
	ProviderOpenSourceSelect   string
	providerOpenResult         func(bool) bool
	PendingSelection           string
	clipboardImageRevealName   string
	clipboardImageRevealPath   string
	clipboardImageRevealVFS    vfs.VFS
	ProviderEntryName          string // name of entry used to enter a provider VFS (e.g. NetFox connection name)
	suppressFolderHistoryPath  string // one-shot: history/menu navigation must not reorder MRU
	suppressFolderHistoryToken uint64 // binds suppression to one specific asynchronous directory load
	FastFindMode               bool
	// exactBoxX1..X2, exactBoxY: where the exact-match line of the filter
	// window was last drawn, for the mouse.
	exactBoxX1, exactBoxX2, exactBoxY int
	FastFindStr                       string
	fastFindMatcherKey                string
	fastFindMatcherStrict             bool
	fastFindMatchers                  []*vtui.FuzzyMatcher
	// autoFilterMode is set while the filter window is open (the search
	// box narrows the panel instead of moving the cursor), autoFilterOn
	// while its query is actually hiding rows. unfilteredEntries then holds
	// the complete row list and Entries the matching subset; see
	// autofilter.go.
	autoFilterMode     bool
	autoFilterOn       bool
	unfilteredEntries  []*FileEntry
	showInactiveCursor bool
	// nameLeftPos is how many display cells the name columns are scrolled to
	// the right (far2l's FileList::LeftPos, Alt+Left/Alt+Right). Names that
	// fit their column never move; a longer name is shifted by at most its
	// own overflow, and Show clamps the value to the longest visible name so
	// it decays to 0 by itself once no long name is on screen. A scrolled
	// name is marked with '{' in the cell left of its column.
	nameLeftPos int

	SortMode    SortMode
	SortReverse bool
	// UseSortGroups clusters the panel by the Group-bearing highlight.ini
	// rules before the sort mode is applied (far's Shift+F11).
	UseSortGroups bool
	// SortNumeric makes name comparisons treat runs of digits inside a file
	// name as numbers instead of plain text (far3's "numeric sort" checkbox
	// next to sort-by-name, f4#1471): "2.Track_2" then sorts before
	// "10.Track_10" instead of after it. It modifies how names compare
	// everywhere a name is the sort key or its tie-break, the same way
	// UseSortGroups modifies clustering regardless of SortMode.
	SortNumeric bool

	// SortSelectedFirst makes selected entries sort ahead of unselected ones
	// regardless of the sort mode (far's Shift+F12 "show selected first").
	// It is a key between the directories rule and the sort groups, the
	// same place far puts it, and like every other sort modifier it takes
	// effect through a re-sort that keeps the cursor on its file.
	SortSelectedFirst bool

	lastDirMTime time.Time
	DirCache     map[dirCacheKey]DirCacheEntry

	isCheckingRefresh bool
	currentTitle      string

	// lastLoadedPath is the path readDirectoryEx last saw; used to
	// detect a directory switch so selectedItems can be dropped
	// (selection is per-directory, matches far/far2l).
	lastLoadedPath string

	// calculatedPanelTotal is the recursive total requested for the current
	// directory by pressing F3 on its ".." row.  Keep it on the panel rather
	// than on one particular VFS so every file panel can display the result.
	calculatedPanelTotal     *vfs.OpStats
	calculatedPanelTotalPath string

	// shiftSessionActive / shiftSessionMode implement FAR-style
	// Shift+nav selection. The mode (select vs deselect) is
	// decided on the first Shift+nav from the state of the row
	// under the cursor and held until Shift is released, so all
	// following Shift+nav keys in the same "session" apply that
	// same mode. Any event other than a Shift+nav key closes
	// the session — the next Shift+nav starts a new one.
	shiftSessionActive bool
	shiftSessionMode   bool // true = select, false = deselect

	// entriesRevision counts every change to what fp.Entries actually holds:
	// a directory (re)load, an autofilter query narrowing or widening the
	// visible rows, or anything else that calls Refresh (including a
	// background size scan landing on an entry, f4#884's actionCalcDirSize).
	// panelEntryTotals memoizes its full scan of fp.Entries against it, the
	// same "don't recompute unless something actually changed" fix #1511 and
	// #1536 already applied to the menu bar and the highlight rules — this is
	// the panel's own bottom-border total, the other unconditional per-frame,
	// per-file scan in the render path.
	entriesRevision         uint64
	entryTotalsRevision     uint64
	entryTotalsValid        bool
	cachedTotSize           int64
	cachedTotCount          int
	cachedTotFiles          int
	cachedTotDirs           int
	entryTotalsComputeCount int // test instrumentation: counts actual rescans
}

var DisableLoadingAnimationInTests = true

// DirectoryLoadWorkers counts every directory-load worker alive in the
// process, across all panels. See enqueueDirectoryLoad for why a per-panel
// WaitGroup is not enough on its own.
var DirectoryLoadWorkers sync.WaitGroup

func NewFileSystemPanel(x, y, w, h int, vfs vfs.VFS) *FileSystemPanel {
	path := vfs.GetPath()

	fp := &FileSystemPanel{
		Vfs:                    vfs,
		Frame:                  vtui.NewBorderedFrame(x, y, x+w-1, y+h-1, vtui.SingleBox, path),
		Table:                  vtui.NewTable(x+1, y+1, w-2, h-2, nil),
		ViewMode:               ViewModeMedium,
		GroupFoldersSeparately: true,
		lastRightClickedIdx:    -1,
		DirCache:               make(map[dirCacheKey]DirCacheEntry),
		SelectedItems:          make(map[string]bool),
		selectionEpoch:         make(map[string]uint64),
		//entries:             []*FileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}},
	}
	fp.Frame.ColorBoxIdx = theme.ColPanelBox
	fp.Frame.ColorTitleIdx = theme.ColPanelTitle
	fp.Table.ColorTextIdx = theme.ColPanelText
	fp.Table.ColorSelectedTextIdx = theme.ColPanelCursor
	fp.Table.ColorItemSelectTextIdx = theme.ColPanelSelectedText
	fp.Table.ColorItemSelectCursorIdx = theme.ColPanelSelectedCursor
	fp.Table.ColorTitleIdx = theme.ColPanelColumnTitle
	fp.Table.ColorBoxIdx = theme.ColPanelBox
	fp.Table.ShowScrollBar = false
	fp.initScrollBar()
	fp.SetCanFocus(true)
	fp.SetPosition(x, y, x+w-1, y+h-1)
	fp.SetViewMode(ViewModeMedium)
	fp.ReadDirectory()
	return fp
}

func DirectoryCacheKey(fs vfs.VFS, path string) dirCacheKey {
	key := dirCacheKey{qualifiedPath: fileops.FileStateKey(fs, path)}
	if stable, ok := fs.(vfs.DirectoryCacheIdentity); ok {
		if cacheKey := stable.DirectoryCacheKey(); cacheKey != nil {
			if cacheType := reflect.TypeOf(cacheKey); cacheType != nil && cacheType.Comparable() {
				key.identity = cacheKey
				return key
			}
		}
	}
	if identity, ok := fs.(vfs.SessionIdentity); ok {
		if sessionKey := identity.SessionKey(); sessionKey != nil {
			if sessionType := reflect.TypeOf(sessionKey); sessionType != nil && sessionType.Comparable() {
				key.identity = sessionKey
				return key
			}
		}
	}
	// VFS is an interface and implementations are not required to be
	// comparable. Every built-in VFS is pointer-backed, but keep the fallback
	// safe for plugins that use a slice/map-bearing value implementation.
	if fs != nil && reflect.TypeOf(fs).Comparable() {
		key.identity = fs
	}
	return key
}

// isNilVFS reports whether v is nil, including a typed nil wrapped inside a
// non-nil interface (e.g. (*ArchiveVFS)(nil) returned as vfs.VFS). Such a
// value compares != nil but dereferences to a panic on any method call.
func isNilVFS(v vfs.VFS) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return rv.IsNil()
	}
	return false
}

// isArchiveProvider reports whether p is the archive/zip plugin's provider,
// so archive open failures get an "Open Error" dialog instead of the
// network-oriented "Connection Error" used by remote VFS plugins.
func isArchiveProvider(p vfs.VFSProvider) bool {
	return p != nil && p.Name() == "zipper/archive"
}

func (fp *FileSystemPanel) CacheKey(path string) dirCacheKey {
	return DirectoryCacheKey(fp.Vfs, path)
}

func (fp *FileSystemPanel) saveToCache(path string, items []vfs.VFSItem) {
	showUpEntry := fp.Vfs != nil && (!fp.Vfs.IsAtRoot() || fp.Vfs.ParentVFS() != nil)
	fp.saveToCacheKey(fp.CacheKey(path), items, showUpEntry)
}

func (fp *FileSystemPanel) saveToCacheKey(key dirCacheKey, items []vfs.VFSItem, showUpEntry bool) {
	if fp.DirCache == nil {
		fp.DirCache = make(map[dirCacheKey]DirCacheEntry)
	}
	fp.DirCache[key] = DirCacheEntry{Items: items, time: time.Now(), showUpEntry: showUpEntry}

	if len(fp.DirCache) > maxDirCache {
		var oldestKey dirCacheKey
		var oldestTime time.Time
		hasOldest := false
		for candidate, entry := range fp.DirCache {
			if !hasOldest || entry.time.Before(oldestTime) {
				oldestKey = candidate
				oldestTime = entry.time
				hasOldest = true
			}
		}
		delete(fp.DirCache, oldestKey)
	}
}

func nativeVisualCachePath(value string) string {
	if os.PathSeparator == '\\' {
		return strings.ReplaceAll(value, "/", "\\")
	}
	return strings.ReplaceAll(value, "\\", "/")
}

// showCachedStandalonePath renders a previously visited provider directory
// before the provider reconnect/restore task has completed. The old VFS stays
// installed until that task succeeds, so these rows are presentation-only;
// the panel's provider-open guard prevents actions from being dispatched
// against the old filesystem meanwhile.
func (fp *FileSystemPanel) showCachedStandalonePath(target string) bool {
	if fp == nil || target == "" || fp.DirCache == nil || config.App.SyncPanelLoad {
		return false
	}
	want := nativeVisualCachePath(target)
	var cached DirCacheEntry
	found := false
	for key, candidate := range fp.DirCache {
		if nativeVisualCachePath(key.qualifiedPath) != want {
			continue
		}
		if !found || candidate.time.After(cached.time) {
			cached = candidate
			found = true
		}
	}
	if !found {
		return false
	}

	var entries []*FileEntry
	if cached.showUpEntry {
		entries = append(entries, &FileEntry{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}, IsCached: true})
	}
	for _, item := range cached.Items {
		if !config.App.ShowHiddenFiles && item.Name != ".." && item.IsHidden {
			continue
		}
		entries = append(entries, &FileEntry{VFSItem: item, IsCached: true})
	}
	fp.setEntries(entries)
	fp.SortEntries()
	fp.SetCursorIndex(0)
	return true
}
func (fp *FileSystemPanel) SetItemSelected(idx int, state bool) {
	if idx >= 0 && idx < len(fp.Entries) {
		e := fp.Entries[idx]
		if e.Name != ".." {
			if e.Selected == state {
				return
			}
			e.Selected = state
			if fp.SelectedItems == nil {
				fp.SelectedItems = make(map[string]bool)
			}
			if fp.selectionEpoch == nil {
				fp.selectionEpoch = make(map[string]uint64)
			}
			fp.selectionEpochNext++
			fp.selectionEpoch[e.Name] = fp.selectionEpochNext
			if state {
				fp.SelectedItems[e.Name] = true
			} else {
				delete(fp.SelectedItems, e.Name)
			}
		}
	}
}

func (fp *FileSystemPanel) previousSelectionMatches(filesystem vfs.VFS, path string) bool {
	return fp != nil && fp.previousSelectionVFS != nil && filesystem != nil &&
		fileops.SameVFSInstance(fp.previousSelectionVFS, filesystem) && fp.previousSelectionPath == path
}

func (fp *FileSystemPanel) clearPreviousSelection() {
	if fp == nil {
		return
	}
	fp.previousSelection = nil
	fp.previousSelectionVFS = nil
	fp.previousSelectionPath = ""
	for _, entry := range fp.Entries {
		entry.PrevSelected = false
	}
}

func (fp *FileSystemPanel) applyPersistentSelection(entry *FileEntry, filesystem vfs.VFS, path string) {
	if fp == nil || entry == nil || entry.Name == ".." {
		return
	}
	entry.Selected = fp.SelectedItems[entry.Name]
	entry.PrevSelected = fp.previousSelectionMatches(filesystem, path) && fp.previousSelection[entry.Name]
}

func (fp *FileSystemPanel) ToggleSelection(idx int) {
	if idx >= 0 && idx < len(fp.Entries) {
		e := fp.Entries[idx]
		if e.Name != ".." {
			fp.SetItemSelected(idx, !e.Selected)
		}
	}
}
func (fp *FileSystemPanel) SetFocus(f bool) {
	fp.ScreenObject.SetFocus(f)
	if !f && fp.FastFindMode {
		fp.ExitFastFind()
	}
}
func (fp *FileSystemPanel) SetSortMode(mode SortMode) {
	if fp.SortMode == mode {
		fp.SortReverse = !fp.SortReverse
	} else {
		fp.SortMode = mode
		// Every mode's base comparator is its desired first-use direction:
		// name/extension ascend, while time/size descend. Repeated activation
		// below toggles that direction uniformly for hotkeys, menus and headers.
		fp.SortReverse = false
	}
	fp.updateSortColumnTitles()
	if fp.GroupBy != GroupNone {
		fp.SetGrouping(fp.GroupBy, fp.GroupReverse, fp.GroupFoldersSeparately)
		return
	}
	fp.ReadDirectory()
}

// SetUseSortGroups switches the sort-group clustering of this  Like a
// sort mode change it goes through ReadDirectory, so the cursor is kept on the
// same file while the rows move under it.
func (fp *FileSystemPanel) SetUseSortGroups(use bool) {
	if fp == nil || fp.UseSortGroups == use {
		return
	}
	fp.UseSortGroups = use
	if fp.GroupBy != GroupNone {
		fp.SetGrouping(fp.GroupBy, fp.GroupReverse, fp.GroupFoldersSeparately)
		return
	}
	fp.ReadDirectory()
}

func (fp *FileSystemPanel) ToggleSortGroups() {
	if fp == nil {
		return
	}
	fp.SetUseSortGroups(!fp.UseSortGroups)
}

// SetSortNumeric switches whether name comparisons treat digit runs as
// numbers. Like a sort mode change it goes through ReadDirectory, so the
// cursor is kept on the same file while the rows move under it.
func (fp *FileSystemPanel) SetSortNumeric(numeric bool) {
	if fp == nil || fp.SortNumeric == numeric {
		return
	}
	fp.SortNumeric = numeric
	if fp.GroupBy != GroupNone {
		fp.SetGrouping(fp.GroupBy, fp.GroupReverse, fp.GroupFoldersSeparately)
		return
	}
	fp.ReadDirectory()
}

func (fp *FileSystemPanel) ToggleSortNumeric() {
	if fp == nil {
		return
	}
	fp.SetSortNumeric(!fp.SortNumeric)
}

// SetSortSelectedFirst switches whether marked rows sort ahead of unmarked
// ones (far's Shift+F12). Like the other sort modifiers it goes through a
// re-sort, so the cursor stays on its file while the rows move under it.
func (fp *FileSystemPanel) SetSortSelectedFirst(on bool) {
	if fp == nil || fp.SortSelectedFirst == on {
		return
	}
	fp.SortSelectedFirst = on
	if fp.GroupBy != GroupNone {
		fp.SetGrouping(fp.GroupBy, fp.GroupReverse, fp.GroupFoldersSeparately)
		return
	}
	fp.ReadDirectory()
}

func (fp *FileSystemPanel) ToggleSortSelectedFirst() {
	if fp == nil {
		return
	}
	fp.SetSortSelectedFirst(!fp.SortSelectedFirst)
}

// resortSelectedFirst re-sorts the rows after a selection change while the
// "selected first" mode is on, keeping the cursor on the entry it stood on
// (far's SortFileList(TRUE) after every selection mutation). It is a no-op
// when the mode is off, so plain selection stays as cheap as it was.
func (fp *FileSystemPanel) resortSelectedFirst() {
	if fp == nil || !fp.SortSelectedFirst {
		return
	}
	focused := fp.GetRawSelectedName()
	offset := fp.displayOfEntry(fp.GetCursorIndex()) - fp.Table.TopPos
	fp.sortEntriesAt(time.Now())
	fp.focusEntryByName(focused)
	fp.Table.TopPos = max(0, fp.displayOfEntry(fp.GetCursorIndex())-offset)
	fp.SetCursorIndex(fp.GetCursorIndex())
}

// sortGroupsActive reports whether this panel's entries have to be clustered:
// the panel asked for it and there is at least one configured group.
func (fp *FileSystemPanel) sortGroupsActive() bool {
	return fp != nil && fp.UseSortGroups && GlobalSortGroups.Configured()
}

// SortEntries orders the panel's complete row list -- the rows an active
// autofilter is hiding included, so a query cannot outlive the sort it was
// typed under -- and then re-derives the visible one.
func (fp *FileSystemPanel) SortEntries() { fp.sortEntriesAt(time.Now()) }

func (fp *FileSystemPanel) sortEntriesAt(now time.Time) {
	entries := fp.AllEntries()
	for _, entry := range entries {
		if entry.sourceOrder == 0 {
			fp.nextSourceOrder++
			entry.sourceOrder = fp.nextSourceOrder
		}
	}
	fp.prepareGrouping(entries, now)
	grouped := fp.sortGroupsActive()
	// An unsorted panel normally keeps its arrival order untouched, but with
	// "selected first" on there is something to reorder: the marked rows.
	if (fp.SortMode == SortUnsorted && !grouped && fp.GroupBy == GroupNone && !fp.SortSelectedFirst) || len(entries) <= 1 {
		fp.refilterEntries()
		return
	}

	// far2l uses a string collation order for panel names: punctuation such as
	// '_' sorts before digits and letters, unlike Go's byte/code-point order.
	// Keep the comparator local because the collator reuses iterator state and
	// is not safe for concurrent use.
	nameCompare := newNameComparer()
	compareName := func(left, right string) int {
		if fp.SortNumeric {
			return naturalCompare(left, right, nameCompare)
		}
		return nameCompare(left, right)
	}

	// Group numbers are resolved once per entry: doing it inside the comparator
	// would re-run every mask of every rule O(n log n) times.
	var groups map[*FileEntry]int
	if grouped {
		groups = make(map[*FileEntry]int, len(entries))
		for _, entry := range entries {
			groups[entry] = GlobalSortGroups.GroupOf(&entry.VFSItem)
		}
	}

	less := func(i, j int) bool {
		ei, ej := entries[i], entries[j]

		// ".." всегда сверху
		if ei.Name == ".." {
			return ej.Name != ".."
		}
		if ej.Name == ".." {
			return false
		}

		// Папки всегда сверху. Folders outrank sort groups, as in far: a
		// group never pulls a directory down among the files. In unsorted
		// mode nothing is reordered except the grouping itself, so the rule
		// stays off there exactly as before.
		if fp.GroupBy != GroupNone {
			if n := fp.compareGroups(ei, ej, compareName); n != 0 {
				return n < 0
			}
		}
		if (fp.SortMode != SortUnsorted || fp.GroupBy != GroupNone) && ei.IsDir != ej.IsDir {
			return ei.IsDir
		}

		// Marked rows outrank unmarked ones once the directories rule has
		// had its say (far's "show selected first", Shift+F12): far ranks
		// DirectoriesFirst above SelectedFirst, so a marked file never
		// jumps above an unmarked folder, and the sort groups and the sort
		// key still order the rows inside each half.
		if fp.SortSelectedFirst && ei.Selected != ej.Selected {
			return ei.Selected
		}

		if grouped {
			// The group is the next key and, as in far, it is not affected by
			// the reverse flag: "executables first" must stay first when the
			// name order is flipped.
			if gi, gj := groups[ei], groups[ej]; gi != gj {
				return gi < gj
			}
			if fp.SortMode == SortUnsorted {
				// Grouping an unsorted panel only clusters the rows; the
				// stable sort below keeps the filesystem order inside a group.
				return fp.GroupBy != GroupNone && ei.sourceOrder < ej.sourceOrder
			}
		}

		if fp.SortMode == SortUnsorted {
			return ei.sourceOrder < ej.sourceOrder
		}
		cmp := 0
		switch fp.SortMode {
		case SortName:
			cmp = compareName(ei.Name, ej.Name)
		case SortExt:
			cmp = compareName(filepath.Ext(ei.Name), filepath.Ext(ej.Name))
			if cmp == 0 {
				cmp = compareName(ei.Name, ej.Name)
			}
		case SortTime:
			if ei.MTime.After(ej.MTime) {
				cmp = -1
			} else if ei.MTime.Before(ej.MTime) {
				cmp = 1
			}
		case SortSize:
			if ei.Size > ej.Size {
				cmp = -1
			} else if ei.Size < ej.Size {
				cmp = 1
			}
		default:
			cmp = compareName(ei.Name, ej.Name)
		}

		// Keep equal primary keys deterministic and match far2l's name tie-break.
		if cmp == 0 {
			cmp = compareName(ei.Name, ej.Name)
		}
		if fp.SortReverse {
			cmp = -cmp
		}
		return cmp < 0
	}

	if fp.SortMode == SortUnsorted {
		// Only reachable with grouping on, where equal rows must not be
		// shuffled: sort.Slice is not stable, SliceStable is.
		sort.SliceStable(entries, less)
		fp.refilterEntries()
		return
	}
	sort.Slice(entries, less)
	fp.refilterEntries()
}

func (fp *FileSystemPanel) SetViewMode(mode ViewMode) {
	if !mode.Valid() {
		mode = ViewModeMedium
	}
	if mode == ViewModeWide && PanelViewModeSettings(ViewModeWide).FullScreen {
		fp.SetWideViewMode(ViewModeWide)
		fp.SetWide(true)
		return
	}
	fp.ViewMode = mode
	fp.Wide = false
	fp.configureCellSelection()
	fp.Resize(fp.X2-fp.X1+1, fp.Y2-fp.Y1+1)
	fp.reloadForLinkCounts()
}

// reloadForLinkCounts reads the directory again when the mode now shows the
// "LN" column and the listing on screen was loaded without the counts, which
// only a load asks for (f4#1861).
func (fp *FileSystemPanel) reloadForLinkCounts() {
	if _, ok := fp.Vfs.(linkCountFiller); !ok {
		return
	}
	if !panelColumnsShow(PanelViewModeSettings(fp.EffectiveViewMode()).Columns, LinkCountColumn) {
		return
	}
	for _, e := range fp.AllEntries() {
		if !e.IsDir && e.Name != ".." && !e.HasMetadata(vfs.MetadataNlink) {
			fp.ReadDirectory()
			return
		}
	}
}

// mouseEntryIndex returns the entry under the mouse. Multi-column panel modes
// are filled from top to bottom and then from left to right, so the visual
// column contributes a full table height to the entry index.
func (fp *FileSystemPanel) mouseEntryIndex(mouseX, mouseY int) int {
	if mouseX < fp.Table.X1 || mouseX > fp.Table.X2 {
		return -1
	}

	row := mouseY - (fp.Table.Y1 + fp.Table.MarginTop)
	if row < 0 || row >= fp.Table.ViewHeight {
		return -1
	}

	column := 0
	if fp.gridColumnCount() > 1 {
		column = -1
		columnX := fp.Table.X1
		for i, tableColumn := range fp.Table.Columns {
			if mouseX >= columnX && mouseX < columnX+tableColumn.Width {
				column = i
				break
			}
			columnX += tableColumn.Width + 1 // one-character separator
		}
		if column < 0 {
			return -1
		}
	}

	return fp.entryIndex(fp.Table.TopPos+row, column)
}

func (fp *FileSystemPanel) processRightDrag(idx int) {
	if !fp.rightDragActive {
		fp.rightDragActive = true
		fp.rightDragSelect = !fp.Entries[idx].Selected
		fp.lastRightClickedIdx = idx
		fp.SetItemSelected(idx, fp.rightDragSelect)
		return
	}

	from := fp.lastRightClickedIdx
	step := 1
	if idx < from {
		step = -1
	}
	for current := from; ; current += step {
		fp.SetItemSelected(current, fp.rightDragSelect)
		if current == idx {
			break
		}
	}
	fp.lastRightClickedIdx = idx
}

func (fp *FileSystemPanel) stopDragAutoScroll() {
	fp.dragScrollDirection = 0
	fp.dragScrollGeneration++
	if fp.dragScrollTimer != nil {
		fp.dragScrollTimer.Stop()
		fp.dragScrollTimer = nil
	}
}

func (fp *FileSystemPanel) dragAutoScrollStep(direction int) bool {
	oldTop := fp.Table.TopPos
	fp.setPanelScrollTop(oldTop + direction)
	if fp.Table.TopPos == oldTop {
		return false
	}

	if fp.rightDragActive {
		fp.processRightDrag(fp.GetCursorIndex())
		fp.Refresh()
		vtui.FrameManager.Redraw()
	}
	return true
}

func (fp *FileSystemPanel) scheduleDragAutoScroll(generation uint64) {
	// Read on the goroutine that starts this work, not inside it: the
	// work outlives the call, and reading the global from it races
	// anything that reassigns vtui.FrameManager meanwhile.
	frames := vtui.FrameManager
	fp.dragScrollTimer = time.AfterFunc(panelDragScrollInterval, func() {
		frames.PostTask(func() {
			if generation != fp.dragScrollGeneration || fp.dragScrollDirection == 0 {
				return
			}
			if !fp.dragAutoScrollStep(fp.dragScrollDirection) {
				fp.stopDragAutoScroll()
				return
			}
			fp.scheduleDragAutoScroll(generation)
		})
	})
}

func (fp *FileSystemPanel) updateDragAutoScroll(mouseY int) bool {
	direction := 0
	contentTop := fp.Table.Y1 + fp.Table.MarginTop
	contentBottom := contentTop + fp.Table.ViewHeight - 1
	if mouseY < contentTop {
		direction = -1
	} else if mouseY > contentBottom {
		direction = 1
	}

	if direction == 0 {
		fp.stopDragAutoScroll()
		return false
	}
	if direction == fp.dragScrollDirection && fp.dragScrollTimer != nil {
		return true
	}

	fp.stopDragAutoScroll()
	fp.dragScrollDirection = direction
	fp.dragScrollGeneration++
	generation := fp.dragScrollGeneration
	if !fp.dragAutoScrollStep(direction) {
		fp.stopDragAutoScroll()
		return true
	}
	fp.scheduleDragAutoScroll(generation)
	return true
}

func (fp *FileSystemPanel) SetAllItemsSelected(state bool) {
	for idx := range fp.Entries {
		fp.SetItemSelected(idx, state)
	}
}

func (fp *FileSystemPanel) SetWide(wide bool) {
	fp.Wide = wide
	fp.configureCellSelection()
	fp.Resize(fp.X2-fp.X1+1, fp.Y2-fp.Y1+1)
}

func (fp *FileSystemPanel) EffectiveViewMode() ViewMode {
	if fp.Wide {
		return fp.WideViewMode()
	}
	return fp.ViewMode
}

func (fp *FileSystemPanel) uppercasePanelDirs() bool {
	return PanelViewModeSettings(fp.EffectiveViewMode()).UppercaseDirs
}

// gridColumnCount is the number of stripes: file-columns the entries flow
// through, top to bottom and then left to right.
func (fp *FileSystemPanel) gridColumnCount() int {
	_, stripes := fp.columnStripes()
	return stripes
}

func (fp *FileSystemPanel) columnSortMode(column int) (SortMode, bool) {
	if column < 0 || column >= len(fp.Table.Columns) {
		return SortUnsorted, false
	}
	return panelColumnSortMode(fp.panelColumnAt(column).Type)
}

func (fp *FileSystemPanel) SortIsAscending() bool {
	switch fp.SortMode {
	case SortTime, SortSize:
		// Their base comparators are newest/largest first.
		return fp.SortReverse
	default:
		return !fp.SortReverse
	}
}

func sortModeTitle(mode SortMode) string {
	switch mode {
	case SortName:
		return i18n.Msg("Menu.SortName")
	case SortExt:
		return i18n.Msg("Menu.SortExt")
	case SortTime:
		return i18n.Msg("Menu.SortTime")
	case SortSize:
		return i18n.Msg("Menu.SortSize")
	}
	return ""
}

func composePanelColumnTitle(left, right string, width int) string {
	if right == "" || width <= 0 {
		return left
	}
	right = runewidth.Truncate(right, width, "")
	rightWidth := runewidth.StringWidth(right)
	if rightWidth >= width {
		return right
	}
	left = runewidth.Truncate(left, width-rightWidth-1, "")
	padding := width - runewidth.StringWidth(left) - rightWidth
	return left + strings.Repeat(" ", padding) + right
}

func hiddenSortColumnTitle(mode SortMode, ascending bool, width int) string {
	arrow := "↓"
	if ascending {
		arrow = "↑"
	}
	// Preserve brackets and the direction arrow on narrow Brief columns;
	// truncate only the localized sort name when the full label cannot fit.
	labelWidth := width - runewidth.StringWidth("[]"+arrow)
	if labelWidth <= 0 {
		return runewidth.Truncate(arrow, width, "")
	}
	label := runewidth.Truncate(sortModeTitle(mode), labelWidth, "")
	return "[" + label + "]" + arrow
}

func (fp *FileSystemPanel) updateSortColumnTitles() {
	for column := range fp.Table.Columns {
		fp.Table.Columns[column].Title = fp.sortColumnLabel(column)
	}

	if fp.sortModeIsHidden() {
		right := hiddenSortColumnTitle(
			fp.SortMode, fp.SortIsAscending(), fp.Table.Columns[0].Width)
		fp.Table.Columns[0].Title = composePanelColumnTitle(
			panelColumnTitle(fp.panelColumnAt(0).Type), right, fp.Table.Columns[0].Width)
	}
}

// sortedByColumn reports whether the panel is sorted by what the column holds.
func (fp *FileSystemPanel) sortedByColumn(column int) bool {
	mode, sortable := fp.columnSortMode(column)
	return sortable && fp.SortMode != SortUnsorted && fp.SortMode == mode
}

// sortModeIsHidden reports whether the active sort mode has no column of its
// own in the current view; its label is then written into the first column's
// header (see hiddenSortColumnTitle).
func (fp *FileSystemPanel) sortModeIsHidden() bool {
	if fp.SortMode == SortUnsorted || len(fp.Table.Columns) == 0 {
		return false
	}
	for column := range fp.Table.Columns {
		if fp.sortedByColumn(column) {
			return false
		}
	}
	return true
}

// sortColumnLabel is the text a column's header shows for the column itself:
// its title, followed by the direction arrow when the panel is sorted by it.
func (fp *FileSystemPanel) sortColumnLabel(column int) string {
	title := panelColumnTitle(fp.panelColumnAt(column).Type)
	if fp.sortedByColumn(column) {
		if fp.SortIsAscending() {
			return title + " ↑"
		}
		return title + " ↓"
	}
	return title
}

// headerLabelSpan is where the label of a column's header is drawn, in cells
// counted from the column's left edge: start inclusive, end exclusive. Only
// the label sorts (f4#1769); the blank rest of the header does not, so a
// stray click near a title cannot flip the sort order or mode.
func (fp *FileSystemPanel) headerLabelSpan(column int) (start, end int) {
	tableColumn := fp.Table.Columns[column]
	width := tableColumn.Width
	label := fp.sortColumnLabel(column)
	hiddenLabelColumn := column == 0 && fp.sortModeIsHidden()
	room := width
	if hiddenLabelColumn {
		// The hidden mode's own label takes the right end and a gap; whatever
		// is left of the header belongs to the column title, left-aligned.
		room = width - runewidth.StringWidth(hiddenSortColumnTitle(fp.SortMode, fp.SortIsAscending(), width)) - 1
	}
	if room <= 0 {
		return 0, 0
	}
	labelWidth := runewidth.StringWidth(label)
	if labelWidth > room {
		labelWidth = room
	}
	if !hiddenLabelColumn {
		switch tableColumn.Alignment {
		case vtui.AlignRight:
			start = width - labelWidth
		case vtui.AlignCenter:
			start = (width - labelWidth) / 2
		}
	}
	return start, start + labelWidth
}

func (fp *FileSystemPanel) headerSortModeAt(x, y int) (SortMode, bool) {
	if !fp.Table.ShowHeader || y != fp.Table.Y1 || x < fp.Table.X1 || x > fp.Table.X2 {
		return SortUnsorted, false
	}
	if mode, ok := fp.hiddenSortModeHeaderAt(x); ok {
		return mode, true
	}
	columnX := fp.Table.X1
	for column, tableColumn := range fp.Table.Columns {
		if x >= columnX && x < columnX+tableColumn.Width {
			if start, end := fp.headerLabelSpan(column); x < columnX+start || x >= columnX+end {
				return SortUnsorted, false
			}
			return fp.columnSortMode(column)
		}
		columnX += tableColumn.Width
		if column < len(fp.Table.Columns)-1 {
			// The separator itself is not a sortable column header.
			if x == columnX {
				return SortUnsorted, false
			}
			columnX++
		}
	}
	return SortUnsorted, false
}

func (fp *FileSystemPanel) hiddenSortModeHeaderAt(x int) (SortMode, bool) {
	if !fp.sortModeIsHidden() {
		return SortUnsorted, false
	}

	width := fp.Table.Columns[0].Width
	right := hiddenSortColumnTitle(fp.SortMode, fp.SortIsAscending(), width)
	rightWidth := runewidth.StringWidth(right)
	columnEnd := fp.Table.X1 + width
	if rightWidth >= width {
		return fp.SortMode, x >= fp.Table.X1 && x < columnEnd
	}
	return fp.SortMode, x >= columnEnd-rightWidth && x < columnEnd
}

// headerColumnBorderAt reports the index of the column to the left of the
// one-cell separator at (x, y) in the header row, so a mouse-down there can
// start a live column resize (f4#246) instead of a sort click. It only
// recognizes a single-stripe layout (Detailed-style: one row of several
// field columns) -- Brief/Medium's multiple stripes of repeating columns are
// a separate, harder case (dragging one stripe's border would need to decide
// whether to also resize every other stripe) left for a follow-up part.
func (fp *FileSystemPanel) headerColumnBorderAt(x, y int) (int, bool) {
	if !fp.Table.ShowHeader || y != fp.Table.Y1 || x < fp.Table.X1 || x > fp.Table.X2 {
		return 0, false
	}
	if !fp.layoutValid() || fp.layout.stripes != 1 || len(fp.Table.Columns) < 2 {
		return 0, false
	}
	columnX := fp.Table.X1
	for column, tableColumn := range fp.Table.Columns {
		columnX += tableColumn.Width
		if column == len(fp.Table.Columns)-1 {
			break
		}
		if x == columnX {
			return column, true
		}
		columnX++
	}
	return 0, false
}

// minResizableColumnWidth keeps a dragged column at least this wide -- one
// cell is the same floor preparePanelLayout already enforces when it shrinks
// columns to fit a narrow panel.
const minResizableColumnWidth = 1

// startColumnResize begins a mouse drag of the border between column and
// column+1 (see headerColumnBorderAt), captured at screen column x.
func (fp *FileSystemPanel) startColumnResize(column, x int) {
	if column < 0 || column+1 >= len(fp.Table.Columns) {
		return
	}
	fp.columnResizeActive = true
	fp.columnResizeIndex = column
	fp.columnResizeStartX = x
	fp.columnResizeLeftWidth = fp.Table.Columns[column].Width
	fp.columnResizeRightW = fp.Table.Columns[column+1].Width
}

// dragColumnResize applies the in-progress resize live: it grows the left
// column and shrinks the right one by the same amount (or the reverse),
// never past minResizableColumnWidth, and redraws immediately. The change is
// only persisted to panel_modes.ini once the drag ends (finishColumnResize);
// intermediate positions are not written to disk.
func (fp *FileSystemPanel) dragColumnResize(x int) {
	if !fp.columnResizeActive {
		return
	}
	col := fp.columnResizeIndex
	if col+1 >= len(fp.Table.Columns) {
		fp.columnResizeActive = false
		return
	}
	delta := x - fp.columnResizeStartX
	left := fp.columnResizeLeftWidth + delta
	right := fp.columnResizeRightW - delta
	if left < minResizableColumnWidth {
		right -= minResizableColumnWidth - left
		left = minResizableColumnWidth
	}
	if right < minResizableColumnWidth {
		left -= minResizableColumnWidth - right
		right = minResizableColumnWidth
	}
	if left < minResizableColumnWidth {
		return // the panel is too narrow for both columns to keep the floor
	}
	fp.Table.Columns[col].Width = left
	fp.Table.Columns[col+1].Width = right
	if col+1 < len(fp.layout.columns) {
		fp.layout.columns[col].Width = left
		fp.layout.columns[col].Percent = false
		fp.layout.columns[col+1].Width = right
		fp.layout.columns[col+1].Percent = false
	}
	fp.updateSortColumnTitles()
	fp.Refresh()
}

// finishColumnResize saves the two columns' final widths from the drag
// started by startColumnResize into the mode's persisted settings, the same
// PanelViewModeSettings/SetPanelViewModeSettings pair the "Panel Modes"
// dialog's numeric width field uses (viewmodes_dialog.go) -- so a resize
// done by dragging shows up there too, and survives a restart.
//
// It only updates this panel's own layout immediately; a second panel
// currently showing the same mode picks up the new widths the next time it
// resizes or switches mode (the same lag SetViewMode already has today for
// any other out-of-dialog settings change) rather than being force-relaid
// out here -- reaching across to the sibling panel is left for a follow-up
// if it turns out to matter in practice.
func (fp *FileSystemPanel) finishColumnResize() {
	if !fp.columnResizeActive {
		return
	}
	fp.columnResizeActive = false
	col := fp.columnResizeIndex
	if col+1 >= len(fp.Table.Columns) {
		return
	}
	mode := fp.layout.mode
	settings := PanelViewModeSettings(mode)
	if col+1 >= len(settings.Columns) {
		return
	}
	settings.Columns[col].Width = fp.Table.Columns[col].Width
	settings.Columns[col].Percent = false
	settings.Columns[col+1].Width = fp.Table.Columns[col+1].Width
	settings.Columns[col+1].Percent = false
	if err := SetPanelViewModeSettings(mode, &settings); err != nil {
		vtui.DebugLog("panel column resize: save failed: %v", err)
	}
}

// panelScrollMetrics maps the panel's item-based scrolling onto the
// row-based coordinates expected by vtui.ScrollBar. Multi-column modes show
// two or three times as many entries in the same vertical space, so using the
// table's raw ItemCount would produce an oversized scroll range and a thumb
// that is much too small.
func (fp *FileSystemPanel) panelScrollMetrics() (height, visibleItems, maxTop, virtualMax, virtualValue int) {
	height = fp.Table.ViewHeight
	if height <= 0 {
		return
	}

	columns := fp.gridColumnCount()
	visibleItems = height * columns
	maxTop = fp.displayCount() - visibleItems
	if maxTop > 0 {
		maxTop += fp.stickyGroupRows(maxTop)
	}
	visibleItems -= fp.stickyGroupRows(fp.Table.TopPos)
	if maxTop <= 0 {
		maxTop = 0
		return
	}

	virtualRows := (fp.displayCount() + columns - 1) / columns
	virtualMax = virtualRows - height
	if virtualMax <= 0 {
		virtualMax = 1
	}

	top := fp.Table.TopPos
	if top < 0 {
		top = 0
	}
	if top > maxTop {
		top = maxTop
	}
	virtualValue = (top*virtualMax + maxTop/2) / maxTop
	return
}

func (fp *FileSystemPanel) initScrollBar() {
	fp.scrollBar = vtui.NewScrollBar(0, 0, 0)
	fp.scrollBar.SetOwner(fp)
	fp.scrollBar.SetVisible(true)
	fp.scrollBar.OnScroll = func(value int) {
		_, _, maxTop, virtualMax, _ := fp.panelScrollMetrics()
		if maxTop == 0 || virtualMax == 0 {
			return
		}
		fp.setPanelScrollTop((value*maxTop + virtualMax/2) / virtualMax)
	}
	fp.scrollBar.OnStep = func(step int) {
		_, visibleItems, _, _, _ := fp.panelScrollMetrics()
		delta := 1
		if step < 0 {
			delta = -1
		}
		if step == -2 || step == 2 {
			delta *= visibleItems
		}
		fp.setPanelScrollTop(fp.Table.TopPos + delta)
	}
}

func (fp *FileSystemPanel) syncScrollBar() bool {
	if fp.scrollBar == nil || config.App.PanelScrollbarMode == config.PanelScrollbarOff {
		return false
	}
	height, _, maxTop, virtualMax, virtualValue := fp.panelScrollMetrics()
	if height <= 2 || maxTop == 0 {
		// Keep the previous range until button release so vtui.ScrollBar can
		// cancel an in-progress drag or auto-repeat even if a refresh made the
		// scrollbar unnecessary while the button was held.
		if !fp.scrollMouseActive {
			fp.scrollBar.SetParams(0, 0, 0)
		}
		return false
	}
	y1 := fp.Table.Y1 + fp.Table.MarginTop
	fp.scrollBar.SetPosition(fp.X2, y1, fp.X2, y1+height-1)
	fp.scrollBar.PgStep = height
	fp.scrollBar.SetParams(virtualValue, 0, virtualMax)
	return true
}

func (fp *FileSystemPanel) setPanelScrollTop(top int) {
	_, _, maxTop, _, _ := fp.panelScrollMetrics()
	if top < 0 {
		top = 0
	}
	if top > maxTop {
		top = maxTop
	}

	delta := top - fp.Table.TopPos
	if delta == 0 {
		return
	}
	direction := 1
	if delta < 0 {
		direction = -1
	}
	idx := fp.nearestDisplayEntry(fp.displayOfEntry(fp.GetCursorIndex())+delta, direction)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(fp.Entries) {
		idx = len(fp.Entries) - 1
	}

	fp.Table.TopPos = top
	fp.SetCursorIndex(idx)
	fp.Refresh()
	vtui.FrameManager.Redraw()
}

func (fp *FileSystemPanel) drawScrollBar(scr *vtui.ScreenBuf) {
	if !fp.syncScrollBar() {
		return
	}
	height := fp.scrollBar.Y2 - fp.scrollBar.Y1 + 1
	if config.App.PanelScrollbarMode == config.PanelScrollbarMinimal {
		caretPos, caretLength := minimalPanelScrollThumb(height, fp.scrollBar.Value, fp.scrollBar.Max)
		attr := vtui.Palette[theme.ColPanelMinimalScrollbar]
		for offset := 0; offset < caretLength; offset++ {
			scr.Write(fp.scrollBar.X1, fp.scrollBar.Y1+caretPos+offset,
				vtui.StringToCharInfo("│", attr))
		}
		return
	}
	vtui.DrawScrollBar(scr, fp.scrollBar.X1, fp.scrollBar.Y1, height,
		fp.scrollBar.Value, fp.scrollBar.Max+height, vtui.Palette[theme.ColPanelScrollbar])
}

func minimalPanelScrollThumb(height, value, maximum int) (position, length int) {
	if height <= 0 || maximum <= 0 {
		return 0, 0
	}
	itemsCount := maximum + height
	length = (height*height + itemsCount/2) / itemsCount
	if length < 1 {
		length = 1
	}
	if length >= height {
		length = height - 1
	}
	maxPosition := height - length
	if value < 0 {
		value = 0
	}
	if value > maximum {
		value = maximum
	}
	position = (value*maxPosition + maximum/2) / maximum
	return position, length
}

// drawCursorSeparators restores the cursor background on column separators.
// vtui.Table draws all separators in one pass after drawing its rows, which
// otherwise overwrites the cursor attributes in single-entry-per-row modes.
func (fp *FileSystemPanel) drawCursorSeparators(scr *vtui.ScreenBuf) {
	perStripe, stripes := fp.columnStripes()
	if perStripe <= 1 || !fp.Table.ShowSeparators || !fp.Table.IsFocused() {
		return
	}
	cursorStripe := 0
	if stripes > 1 {
		cursorStripe = fp.Table.SelectCol
	}

	y := fp.Table.Y1 + fp.Table.MarginTop + fp.Table.SelectPos - fp.Table.TopPos
	if y < fp.Table.Y1+fp.Table.MarginTop || y > fp.Table.Y2 {
		return
	}

	x := fp.Table.X1
	for column := 0; column < len(fp.Table.Columns)-1; column++ {
		x += fp.Table.Columns[column].Width
		// Only the separators inside the cursor's stripe belong to it.
		if column/perStripe != cursorStripe || (column+1)/perStripe != cursorStripe {
			x++
			continue
		}
		// Keep the separator's own foreground and copy only the rendered
		// cursor cell's background. The separator must not inherit the file
		// name/highlighter foreground color.
		cursorAttr := scr.GetCell(x-1, y).Attributes
		attr := vtui.Palette[fp.Table.ColorBoxIdx]
		if cursorAttr&vtui.IsBgRGB != 0 {
			attr = vtui.SetRGBBack(attr, vtui.GetRGBBack(cursorAttr))
		} else {
			attr = vtui.SetIndexBack(attr, vtui.GetIndexBack(cursorAttr))
		}
		attr = (attr &^ vtui.BackgroundIntensity) | (cursorAttr & vtui.BackgroundIntensity)
		scr.Write(x, y, vtui.StringToCharInfo("│", attr))
		x++
	}
}

// truncateNameKeepingEnd fits a name into width cells by dropping its
// beginning, so a long file name still shows its extension.
func truncateNameKeepingEnd(name string, width int) string {
	if width <= 0 {
		return ""
	}
	overflow := runewidth.StringWidth(name) - width
	if overflow <= 0 {
		return name
	}
	return runewidth.TruncateLeft(name, overflow, "")
}

// visibleNameCells calls fn for every name cell currently on screen with the
// entry it shows, the cell's left screen column, its row and its width.
func (fp *FileSystemPanel) visibleNameCells(fn func(entry *FileEntry, x, y, width int)) {
	height := fp.Table.ViewHeight
	if height <= 0 {
		return
	}
	for rowOffset := 0; rowOffset < height; rowOffset++ {
		row := fp.Table.TopPos + rowOffset
		y := fp.Table.Y1 + fp.Table.MarginTop + rowOffset
		x := fp.Table.X1
		for column := range fp.Table.Columns {
			width := fp.Table.Columns[column].Width
			if spec := fp.panelColumnAt(column); spec.Type == NameColumn {
				entryIndex := fp.entryIndex(row, column)
				if entryIndex >= 0 && entryIndex < len(fp.Entries) {
					nameX, nameWidth := x, width
					if spec.Flags&ColumnMark != 0 && nameWidth > 1 {
						nameX, nameWidth = nameX+1, nameWidth-1
					}
					fn(fp.Entries[entryIndex], nameX, y, nameWidth)
				}
			}
			x += width + 1
		}
	}
}

// maxVisibleNameOverflow is the overflow of the longest name on screen: the
// farthest the name columns can usefully be scrolled right now.
func (fp *FileSystemPanel) maxVisibleNameOverflow() int {
	maxOverflow := 0
	fp.visibleNameCells(func(entry *FileEntry, _, _, width int) {
		maxOverflow = max(maxOverflow, panelNameOverflow(entry, width))
	})
	return maxOverflow
}

// namesOverflow reports whether at least one name on screen is cut off, so
// Alt+Left/Alt+Right have something to scroll.
func (fp *FileSystemPanel) NamesOverflow() bool {
	return fp.maxVisibleNameOverflow() > 0
}

func (fp *FileSystemPanel) clampNameLeftPos() {
	if fp.nameLeftPos <= 0 {
		fp.nameLeftPos = 0
		return
	}
	fp.nameLeftPos = min(fp.nameLeftPos, fp.maxVisibleNameOverflow())
}

// ScrollNames shifts the name columns by delta cells: positive brings the
// end of long names into view, negative scrolls back toward their
// beginning. It reports whether the position changed.
func (fp *FileSystemPanel) ScrollNames(delta int) bool {
	return fp.SetNameLeftPos(fp.nameLeftPos + delta)
}

// SetNameLeftPos scrolls the name columns to pos cells, clamped to the
// longest visible name (so any large value means "to the end"). It reports
// whether the position changed.
func (fp *FileSystemPanel) SetNameLeftPos(pos int) bool {
	pos = max(0, min(pos, fp.maxVisibleNameOverflow()))
	if pos == fp.nameLeftPos {
		return false
	}
	fp.nameLeftPos = pos
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
	return true
}

// drawNameScrollBrackets marks cut-off names the way far2l does: '{' in the
// cell left of a name whose beginning is scrolled out of view, '}' in the
// cell right of a name whose end is still hidden. Those cells are the panel
// border or the column separator; on the cursor row they take the cursor's
// background like drawCursorSeparators does.
func (fp *FileSystemPanel) drawNameScrollBrackets(scr *vtui.ScreenBuf) {
	if !fp.Table.IsVisible() {
		return
	}
	bracketAttr := func(neighborX, y int) uint64 {
		attr := vtui.Palette[theme.ColPanelBox]
		cellAttr := scr.GetCell(neighborX, y).Attributes
		if cellAttr&vtui.IsBgRGB != 0 {
			attr = vtui.SetRGBBack(attr, vtui.GetRGBBack(cellAttr))
		} else {
			attr = vtui.SetIndexBack(attr, vtui.GetIndexBack(cellAttr))
		}
		return (attr &^ vtui.BackgroundIntensity) | (cellAttr & vtui.BackgroundIntensity)
	}
	fp.visibleNameCells(func(entry *FileEntry, x, y, width int) {
		overflow := panelNameOverflow(entry, width)
		if overflow <= 0 {
			return
		}
		shift := panelNameShift(entry, width, fp.nameLeftPos)
		if shift > 0 && x-1 >= fp.X1 {
			attr := vtui.Palette[theme.ColPanelBox]
			if x-1 > fp.X1 {
				attr = bracketAttr(x, y)
			}
			scr.Write(x-1, y, vtui.StringToCharInfo("{", attr))
		}
		if overflow-shift > 0 && x+width <= fp.X2 {
			attr := vtui.Palette[theme.ColPanelBox]
			if x+width < fp.X2 {
				attr = bracketAttr(x+width-1, y)
			}
			scr.Write(x+width, y, vtui.StringToCharInfo("}", attr))
		}
	})
}

func (fp *FileSystemPanel) processScrollBarMouse(e *vtinput.InputEvent) bool {
	if fp.scrollBar == nil || config.App.PanelScrollbarMode == config.PanelScrollbarOff {
		return false
	}
	// Releases must reach ScrollBar so it can stop dragging and auto-repeat.
	if e.ButtonState == 0 {
		if config.App.PanelScrollbarMode == config.PanelScrollbarFull {
			fp.scrollBar.ProcessMouse(e)
		}
		fp.scrollMouseActive = false
		fp.minimalScrollDragGap = 0
		fp.syncScrollBar()
		return false
	}
	if e.ButtonState&vtinput.FromLeft1stButtonPressed == 0 {
		return false
	}
	if config.App.PanelScrollbarMode == config.PanelScrollbarMinimal {
		if fp.scrollMouseActive {
			height := fp.scrollBar.Y2 - fp.scrollBar.Y1 + 1
			_, caretLength := minimalPanelScrollThumb(height, fp.scrollBar.Value, fp.scrollBar.Max)
			maxPosition := height - caretLength
			position := int(e.MouseY) - fp.scrollBar.Y1 - fp.minimalScrollDragGap
			if position < 0 {
				position = 0
			}
			if position > maxPosition {
				position = maxPosition
			}
			value := 0
			if maxPosition > 0 {
				value = (position*fp.scrollBar.Max + maxPosition/2) / maxPosition
			}
			_, _, maxTop, virtualMax, _ := fp.panelScrollMetrics()
			if virtualMax > 0 {
				fp.setPanelScrollTop((value*maxTop + virtualMax/2) / virtualMax)
			}
			return true
		}
		if !e.KeyDown || e.MouseEventFlags&vtinput.MouseMoved != 0 || !fp.syncScrollBar() || int(e.MouseX) != fp.scrollBar.X1 {
			return false
		}
		height := fp.scrollBar.Y2 - fp.scrollBar.Y1 + 1
		caretPos, caretLength := minimalPanelScrollThumb(height, fp.scrollBar.Value, fp.scrollBar.Max)
		y := int(e.MouseY) - fp.scrollBar.Y1
		if y < caretPos || y >= caretPos+caretLength {
			return false
		}
		fp.scrollMouseActive = true
		fp.minimalScrollDragGap = y - caretPos
		return true
	}
	if fp.scrollMouseActive {
		// Once a scrollbar owns the press, moving over file rows must not
		// turn the same gesture into row selection.
		fp.scrollBar.ProcessMouse(e)
		return true
	}
	// A scrollbar interaction can only start on the initial button-down,
	// never when an existing row drag merely crosses the scrollbar.
	if !e.KeyDown || e.MouseEventFlags&vtinput.MouseMoved != 0 || !fp.syncScrollBar() {
		return false
	}
	handled := fp.scrollBar.ProcessMouse(e)
	if handled {
		fp.scrollMouseActive = true
	}
	return handled
}

func (fp *FileSystemPanel) configureCellSelection() {
	if fp.gridColumnCount() > 1 {
		fp.Table.CellSelection = true
	} else {
		fp.Table.CellSelection = false
		fp.Table.SelectCol = 0
	}
}

func (fp *FileSystemPanel) GetCursorIndex() int {
	if fp.CursorIdx >= len(fp.Entries) {
		fp.CursorIdx = len(fp.Entries) - 1
	}
	if fp.CursorIdx < 0 {
		fp.CursorIdx = 0
	}
	return fp.CursorIdx
}

func (fp *FileSystemPanel) SetCursorIndex(idx int) {
	if len(fp.Entries) == 0 {
		fp.CursorIdx = 0
		return
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(fp.Entries) {
		idx = len(fp.Entries) - 1
	}
	fp.CursorIdx = idx

	visual := fp.displayOfEntry(idx)
	if fp.GroupBy != GroupNone {
		fp.syncGroupedCursor(visual)
		return
	}
	// Sync table visual state
	if fp.gridColumnCount() == 1 {
		fp.Table.SetSelectPos(visual)
		fp.Table.SelectCol = 0
		if fp.FastFindMode {
			H := fp.Table.ViewHeight
			covered := fp.fastFindBoxHeight() - 1 // rows of the box over the list; its last row is the frame
			if H > covered && visual >= fp.Table.TopPos+H-covered {
				fp.Table.TopPos = visual - H + covered + 1
				if fp.Table.TopPos < 0 {
					fp.Table.TopPos = 0
				}
			}
		}
	} else {
		H := fp.Table.ViewHeight
		if H <= 0 {
			H = 1
		}

		// 1. Ensure TopPos is sane for the current cursor
		if visual < fp.Table.TopPos {
			fp.Table.TopPos = visual
		} else if visual >= fp.Table.TopPos+fp.gridColumnCount()*H {
			fp.Table.TopPos = visual - fp.gridColumnCount()*H + 1
		}

		// Far-style 2-column scrolling: ensure cursorIdx is in [TopPos, TopPos + 2*H)
		if visual < fp.Table.TopPos {
			fp.Table.TopPos = visual
		} else if visual >= fp.Table.TopPos+fp.gridColumnCount()*H {
			fp.Table.TopPos = visual - fp.gridColumnCount()*H + 1
		}

		if covered := fp.fastFindBoxHeight() - 1; fp.FastFindMode && H > covered {
			rel := visual - fp.Table.TopPos
			row := rel % H
			if row >= H-covered {
				shift := row - (H - covered - 1)
				fp.Table.TopPos += shift
			}
		}

		if fp.Table.TopPos < 0 {
			fp.Table.TopPos = 0
		}

		rel := visual - fp.Table.TopPos
		fp.Table.SelectCol = rel / H
		// Table internal rendering expects SelectPos to be absolute index in its row space
		// to correctly calculate vertical offset: y = Y1 + (SelectPos - TopPos)
		fp.Table.SelectPos = fp.Table.TopPos + (rel % H)

		// If we landed on a column that is theoretically correct but visually empty,
		// the table will handle it during Show, but we keep the absolute index.
	}
}

func (fp *FileSystemPanel) updateTitle(err error) {
	path := fp.Vfs.GetPath()
	title := ""
	if fp.ProviderOpenTarget != "" {
		// A standalone visual path is already the complete user-facing title.
		// Do not ask the source VFS to interpret a path owned by another provider.
		path = fp.ProviderOpenTarget
		title = path
	} else if tp, ok := fp.Vfs.(vfs.PanelTitleProvider); ok {
		title = tp.PanelTitle(path)
	}
	if title == "" {
		title = path
		if tp, ok := fp.Vfs.(vfs.TitleProvider); ok {
			if prefix := tp.GetTitle(); prefix != "" {
				title = prefix + ":" + title
			}
		}
	}

	if err != nil && err != context.Canceled {
		title += " [Error]"
	} else if fp.IsLoading && fp.loadingVisible {
		title += " " + panelLoadingPulse[fp.loadingFrame%len(panelLoadingPulse)]
	}
	fp.currentTitle = title
	fp.Frame.SetTitle("")
}

func (fp *FileSystemPanel) StopLoadingAnimation() {
	fp.LoadingGeneration++
	if fp.LoadingTimer != nil {
		fp.LoadingTimer.Stop()
		fp.LoadingTimer = nil
	}
	fp.loadingVisible = false
}

func (fp *FileSystemPanel) startLoadingAnimation() {
	fp.StopLoadingAnimation()
	fp.loadingFrame = 0

	// Reflect the loading state in the title immediately (path/error text), but
	// the spinner glyph stays hidden until panelLoadingShowDelay elapses.
	fp.updateTitle(nil)
	vtui.FrameManager.Redraw()

	// In tests, do not run the infinite timer loop to prevent task queue leakage.
	if DisableLoadingAnimationInTests && flag.Lookup("test.v") != nil {
		return
	}

	// Defer the visible pulse: only show it once the operation has been loading
	// for panelLoadingShowDelay. Fast operations finish before this fires and
	// never flash the spinner.
	generation := fp.LoadingGeneration
	frames := vtui.FrameManager
	fp.LoadingTimer = time.AfterFunc(panelLoadingShowDelay, func() {
		// Read on the goroutine that starts this work, not inside it: the
		// work outlives the call, and reading the global from it races
		// anything that reassigns vtui.FrameManager meanwhile.
		f := frames
		f.PostTask(func() {
			if !fp.IsLoading || fp.LoadingGeneration != generation {
				return
			}
			fp.loadingVisible = true
			fp.updateTitle(nil)
			f.Redraw()
			fp.scheduleLoadingPulse(generation)
		})
	})
}

// scheduleLoadingPulse advances the loading spinner while the panel stays in a
// loading state. It reuses fp.loadingTimer, so stopLoadingAnimation cancels it.
func (fp *FileSystemPanel) scheduleLoadingPulse(generation uint64) {
	var scheduleNext func()
	scheduleNext = func() {
		// Read the manager on this goroutine, not inside the posted task: the
		// work outlives the call and reading the global from it races anything
		// that reassigns vtui.FrameManager meanwhile.
		frames := vtui.FrameManager
		if frames == nil {
			return
		}
		fp.LoadingTimer = time.AfterFunc(panelLoadingPulseInterval, func() {
			frames.PostTask(func() {
				if !fp.IsLoading || fp.LoadingGeneration != generation || !fp.loadingVisible {
					return
				}
				fp.loadingFrame = (fp.loadingFrame + 1) % len(panelLoadingPulse)
				fp.updateTitle(nil)
				frames.Redraw()
				scheduleNext()
			})
		})
	}
	scheduleNext()
}

func (fp *FileSystemPanel) pathTitleHitTest(x, y int) bool {
	if y != fp.Y1 || fp.currentTitle == "" {
		return false
	}
	availW := (fp.X2 - fp.X1) - 6
	if availW < 5 {
		availW = 5
	}
	displayTitle := fp.currentTitle
	if runewidth.StringWidth(displayTitle) > availW {
		displayTitle = vtui.TruncateMiddle(displayTitle, availW)
	}
	// Include the one-cell padding drawn on both sides of the path.
	return x >= fp.X1+2 && x <= fp.X1+3+runewidth.StringWidth(displayTitle)
}

func (fp *FileSystemPanel) ReadDirectory() {
	// A fresh listing must not inherit the coast of the previous one.
	fp.wheel.Stop()
	fp.readDirectoryEx(false)
}

// enqueueDirectoryLoad keeps at most one backend read running and one newer
// read waiting. Repeated navigation replaces the pending closure instead of
// creating a FIFO of stale Stat/ReadDir goroutines. The running request is
// cancelled by readDirectoryEx; it may still need to drain one FISH+ response,
// after which only the most recent path is allowed to start.
func (fp *FileSystemPanel) EnqueueDirectoryLoad(load func()) {
	fp.enqueueDirectoryLoad(load, false)
}

// enqueueDirectoryLoad is EnqueueDirectoryLoad; detachable says this load
// need not be waited for once a newer one cancels it. A local read may sit
// on a sudo password prompt (entering /root), and the next directory the user
// walks to stayed empty until the prompt was answered (f4#1411). A remote
// read is still waited for: its connection carries one request at a time.
func (fp *FileSystemPanel) enqueueDirectoryLoad(load func(), detachable bool) {
	fp.loadQueueMu.Lock()
	if fp.loadWorkerActive {
		if !fp.runningDetachable {
			fp.pendingDirectoryLoad, fp.pendingDetachable = load, detachable
			fp.loadQueueMu.Unlock()
			return
		}
		// The running read was cancelled by this navigation: leave it to
		// finish on its own, its results are dropped (loadCtx no longer
		// matches), and start this one now.
		fp.loadWorkerGen++
		fp.pendingDirectoryLoad = nil
	} else {
		fp.loadWorkerActive = true
		fp.loadIdleCh = make(chan struct{})
	}
	fp.runningDetachable = detachable
	gen := fp.loadWorkerGen
	// Every worker is also counted process-wide. A worker reads globals while
	// it runs -- config.App and vtui.FrameManager, and the frame manager's task
	// queue when it posts back -- so anything that replaces one of those has to
	// know whether a worker is still out there. Panels are created deep inside
	// PanelsFrame.ResizeConsole as well as directly, so a per-panel WaitGroup
	// alone leaves no way to ask that question about the panels a caller never
	// sees. Production only counts; the tests are what wait.
	DirectoryLoadWorkers.Add(1)
	fp.loadQueueMu.Unlock()

	go func() {
		defer DirectoryLoadWorkers.Done()
		next := load
		for next != nil {
			next()

			fp.loadQueueMu.Lock()
			if fp.loadWorkerGen != gen {
				// Left behind: a newer worker owns the queue.
				fp.loadQueueMu.Unlock()
				return
			}
			next = fp.pendingDirectoryLoad
			fp.runningDetachable = fp.pendingDetachable
			fp.pendingDirectoryLoad = nil
			if next == nil {
				fp.loadWorkerActive = false
				close(fp.loadIdleCh)
			}
			fp.loadQueueMu.Unlock()
		}
	}()
}

// WaitForIdle blocks until fp has no directory-load worker running. It is
// how a test joins the queue between actions, and it must be used instead of
// waiting on a WaitGroup directly: EnqueueDirectoryLoad above only Add(1)s
// once per round (a fresh worker starting while none was active), so a
// second, unrelated EnqueueDirectoryLoad call can legally re-arm the queue
// at any time -- including while a caller here is still unblocking from the
// round that just finished. sync.WaitGroup does not support that reuse
// pattern without synchronizing the new Add against the outstanding Wait
// itself, so calling Wait() directly races. loadIdleCh only ever changes
// under loadQueueMu, so every wait below is on a channel a concurrent
// EnqueueDirectoryLoad cannot be racing to close.
func (fp *FileSystemPanel) WaitForIdle() {
	for {
		fp.loadQueueMu.Lock()
		idleCh := fp.loadIdleCh
		active := fp.loadWorkerActive
		fp.loadQueueMu.Unlock()
		if !active {
			return
		}
		<-idleCh
	}
}

// cancelProviderOpen invalidates an asynchronous VFS mount before asking its
// context to stop. A completion already queued on the UI thread will see that
// it no longer owns the transition and close any VFS it produced instead of
// replacing the panel's newer file system.
func (fp *FileSystemPanel) cancelProviderOpen() {
	if task := fp.ProviderOpenTask; task != nil {
		fp.ProviderOpenTask = nil
		fp.ProviderOpenTarget = ""
		fp.ProviderOpenSourceSelect = ""
		fp.providerOpenResult = nil
		task.Cancel()
	}
}

func (fp *FileSystemPanel) PersistentPath() string {
	if fp != nil && fp.ProviderOpenTask != nil && fp.ProviderOpenTarget != "" {
		return fp.ProviderOpenTarget
	}
	if fp == nil || fp.Vfs == nil {
		return ""
	}
	path := fp.Vfs.GetPath()
	if !shouldPersistPanelPath(fp, path) {
		return ""
	}
	return path
}

// openVFSAsync runs a provider or URI mount without allowing a slow or
// cancelled result to replace a panel that has since navigated elsewhere.
// The success callback decides whether the source VFS becomes ParentVFS or is
// closed as part of a complete panel switch.
func (fp *FileSystemPanel) openVFSAsync(
	persistentTarget string,
	open func(context.Context) (vfs.VFS, error),
	onSuccess func(vfs.VFS),
	onError func(error),
) bool {
	if fp == nil || fp.Vfs == nil || open == nil || onSuccess == nil {
		return false
	}

	fp.cancelProviderOpen()
	if fp.CancelLoad != nil {
		fp.CancelLoad()
		fp.CancelLoad = nil
	}
	fp.StopLoadingAnimation()

	sourceVFS := fp.Vfs
	sourcePath := sourceVFS.GetPath()
	sourceSelection := fp.GetSelectedName()
	fp.ProviderOpenTarget = persistentTarget
	fp.ProviderOpenSourceSelect = sourceSelection
	fp.IsLoading = true
	fp.startLoadingAnimation()
	cachedPreview := fp.showCachedStandalonePath(persistentTarget)
	if !cachedPreview {
		// Keep the source rows as a stable placeholder when this destination has
		// never been visited. Input is guarded until the provider switch, so they
		// cannot dispatch operations against the wrong VFS.
		fp.Refresh()
		vtui.FrameManager.Redraw()
	} else {
		fp.Refresh()
		vtui.FrameManager.Redraw()
	}
	fp.ProviderOpenTask = vtui.RunAsync(func(task *vtui.TaskContext) {
		newVFS, err := open(task.Context)
		if err == nil && newVFS == nil {
			err = fmt.Errorf("provider returned no file system")
		}
		task.RunOnUI(func() {
			// A provider may hand back a typed nil wrapped in the interface
			// (e.g. (*ArchiveVFS)(nil), err): it compares != nil, so Close()
			// on it would panic. Normalize it to a plain error instead.
			if err == nil && isNilVFS(newVFS) {
				err = fmt.Errorf("provider returned no file system")
			}
			if fp.ProviderOpenTask != task {
				if !isNilVFS(newVFS) {
					_ = newVFS.Close()
				}
				return
			}
			fp.ProviderOpenTask = nil
			resultCallback := fp.providerOpenResult
			fp.providerOpenResult = nil
			fp.ProviderOpenTarget = ""
			fp.ProviderOpenSourceSelect = ""
			if !fileops.SameVFSInstance(fp.Vfs, sourceVFS) || fp.Vfs.GetPath() != sourcePath {
				if !isNilVFS(newVFS) {
					_ = newVFS.Close()
				}
				return
			}
			if err != nil {
				if !isNilVFS(newVFS) {
					_ = newVFS.Close()
				}
				fp.IsLoading = false
				fp.updateTitle(err)
				fp.PendingSelection = sourceSelection
				fp.SuppressNextFolderHistory(sourcePath)
				// Restore the source listing before a history callback starts the
				// next asynchronous mount.  The next mount will cancel this load;
				// without it, an exhausted history walk would leave the panel in
				// the loading state of the failed provider.
				fp.ReadDirectory()
				handled := resultCallback != nil && resultCallback(false)
				if !handled && onError != nil {
					onError(err)
				}
				return
			}
			onSuccess(newVFS)
			if resultCallback != nil {
				resultCallback(true)
			}
		})
	})
	return true
}

// showCurrentVFSLoadingRows atomically stops the panel from exposing rows that
// belonged to a VFS it has just left. ReadDirectory will replace this minimal
// view immediately from cache when allowed; without a cache (or when
// SyncPanelLoad deliberately bypasses it), only a real parent row is safe to
// keep interactive while the new listing is in flight.
func (fp *FileSystemPanel) showCurrentVFSLoadingRows() {
	var entries []*FileEntry
	if fp.Vfs != nil && (!fp.Vfs.IsAtRoot() || fp.Vfs.ParentVFS() != nil) {
		entries = []*FileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}}
	}
	fp.setEntries(entries)
	fp.SetCursorIndex(0)
	fp.Refresh()
	vtui.FrameManager.Redraw()
}

// setKnownDirectoryPath takes the no-I/O route offered by remote VFSes when
// the target came from a panel row. Cancel first so the old background refresh
// starts leaving the shared session before the new cache is rendered.
func (fp *FileSystemPanel) SetKnownDirectoryPath(target string) error {
	if setter, ok := fp.Vfs.(vfs.OptimisticPathSetter); ok {
		if fp.CancelLoad != nil {
			fp.CancelLoad()
			fp.CancelLoad = nil
		}
		return setter.SetPathOptimistic(target)
	}
	return fp.Vfs.SetPath(target)
}

// navigateElevatedDirectoryAsync resolves newPath off the UI goroutine for an
// OSVFS target that NeedsElevation says would otherwise block resolving
// through the sudo helper (f4#1411). It mirrors the synchronous Enter-key
// handling in processKey, just deferred past a RunOnUI hop: the (possibly
// slow) resolution runs on a background goroutine, and only the actual path
// change — an instant field write — happens back on the UI goroutine, which
// is the only place mutating fp.Vfs is safe.
//
// The fix for the freeze (this function existing at all) left a follow-up
// reported live on f4#1411: once the UI goroutine stops blocking, a slow PAM
// prompt (e.g. a fingerprint reader retrying) is silent — nothing on screen
// says an elevation attempt is even happening until it finally resolves. This
// reuses the panel's existing loading pulse (the same title spinner
// readDirectoryEx shows for a slow listing) as the indicator: it appears in
// the panel title after panelLoadingShowDelay so a fast, no-prompt sudo check
// never flashes it, and it is cleared the moment the result comes back,
// whichever way it went.
func (fp *FileSystemPanel) navigateElevatedDirectoryAsync(osfs *vfs.OSVFS, newPath, oldPath, selectedName string) {
	sourceVFS := fp.Vfs
	fp.IsLoading = true
	fp.startLoadingAnimation()
	generation := fp.LoadingGeneration
	vtui.RunAsync(func(task *vtui.TaskContext) {
		abs, err := osfs.ResolveElevated(newPath)
		task.RunOnUI(func() {
			if fp.LoadingGeneration == generation {
				// Nothing newer (e.g. a fresh ReadDirectory, which bumps the
				// generation itself) has claimed the spinner since we started
				// it, so it is still ours to clear here — on every outcome,
				// including the stale-navigation and error returns below.
				fp.IsLoading = false
				fp.StopLoadingAnimation()
				fp.updateTitle(nil)
				vtui.FrameManager.Redraw()
			}
			if !fileops.SameVFSInstance(fp.Vfs, sourceVFS) || fp.Vfs.GetPath() != oldPath {
				// The panel navigated elsewhere while the sudo prompt was up;
				// applying this result now would clobber wherever it is now.
				return
			}
			if err != nil {
				vtui.ShowMessage(" Error ", fmt.Sprintf("Cannot access folder:\n%v", err), []string{"&Ok"})
				return
			}
			osfs.CommitPath(abs)
			if selectedName == ".." {
				fp.PendingSelection = fp.Vfs.Base(oldPath)
			} else {
				fp.PendingSelection = ".."
			}
			fp.ReadDirectory()
		})
	})
}

func (fp *FileSystemPanel) SuppressNextFolderHistory(path string) {
	fp.suppressFolderHistoryToken++
	fp.suppressFolderHistoryPath = path
}

func (fp *FileSystemPanel) clearFolderHistorySuppression() {
	fp.suppressFolderHistoryToken++
	fp.suppressFolderHistoryPath = ""
}

func (fp *FileSystemPanel) FolderHistorySuppression(path string) (uint64, bool) {
	if !SameFolderHistoryPath(path, fp.suppressFolderHistoryPath) {
		return 0, false
	}
	return fp.suppressFolderHistoryToken, true
}

func (fp *FileSystemPanel) ConsumeFolderHistorySuppression(path string, token uint64) bool {
	if token == 0 || token != fp.suppressFolderHistoryToken || !SameFolderHistoryPath(path, fp.suppressFolderHistoryPath) {
		return false
	}
	fp.suppressFolderHistoryPath = ""
	return true
}

// shouldPersistPanelPath keeps paths that can be restored without the VFS
// instance that produced them. A nested provider may expose an absolute
// remote path such as /home/user, but saving it in session.ini would make the
// next startup interpret that path as a local OS directory.
func shouldPersistPanelPath(fp *FileSystemPanel, path string) bool {
	if fp == nil || fp.Vfs == nil || path == "" {
		return false
	}
	if fp.Vfs.ParentVFS() == nil {
		return true
	}
	return fileops.IsPersistentURIPath(path) || vfs.FindStandaloneProvider(context.Background(), nil, path) != nil
}

// shouldRecordFolderHistory prevents an internal path of a nested VFS from
// leaking into the global OS-folder history. Some providers (notably NetFox)
// expose an absolute remote path such as /home/user, but that path cannot be
// reopened after the panel leaves the provider and would otherwise be treated
// as a local directory. Keep URI and standalone-provider paths, which have a
// host-level restore route (archives and other persistent virtual paths).
func ShouldRecordFolderHistory(fp *FileSystemPanel, path string) bool {
	if fp == nil || fp.Vfs == nil || path == "" {
		return false
	}
	if fp.Vfs.ParentVFS() == nil {
		return true
	}
	if fileops.IsPersistentURIPath(path) || vfs.FindStandaloneProvider(context.Background(), nil, path) != nil {
		return true
	}
	// filepath.IsAbs does not treat a slash-rooted POSIX path as absolute on
	// Windows, although a remote Unix VFS can legitimately return one there.
	return !filepath.IsAbs(path) && !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "\\")
}

// RecordFolderHistoryEntry records the folder-history entry, if any, for
// loadVFS's current path. A VFS that implements vfs.HistoryPathProvider
// (a panel plugin's session, e.g. NetFox) owns the decision entirely
// (f4#262): whatever it hands back through HistoryEntry is what gets
// recorded, and it gets nothing at all when the plugin declines. Any other
// VFS falls back to the real-path rules in ShouldRecordFolderHistory, exactly
// as before this hook existed.
func RecordFolderHistoryEntry(fp *FileSystemPanel, loadVFS vfs.VFS, path string) {
	raiseFolderChanged(fp, path)
	if provider, ok := loadVFS.(vfs.HistoryPathProvider); ok {
		if display, ref, ok := provider.HistoryEntry(); ok {
			history.AddPluginFolderHistory(display, fmt.Sprintf("%T", loadVFS), ref)
		}
		return
	}
	if ShouldRecordFolderHistory(fp, path) {
		history.AddFolderHistory(path)
	}
}

// showDirectoryError keeps asynchronous refresh failures from stacking modal
// dialogs. A failed recovery may schedule another read before the user closes
// the first message; only the first live dialog should remain actionable.
func (fp *FileSystemPanel) showDirectoryError(title, message string) {
	if fp.directoryErrorDialog != nil && !fp.directoryErrorDialog.IsDone() {
		return
	}
	dlg := vtui.ShowMessage(title, message, []string{"&Ok"})
	fp.directoryErrorDialog = dlg
	dlg.OnResult = func(int) {
		if fp.directoryErrorDialog == dlg {
			fp.directoryErrorDialog = nil
		}
	}
}

// moveToParentAfterLoadFailure restores a panel using the VFS' canonical
// absolute parent path. Passing a bare ".." is not portable: remote VFSes such
// as AFC deliberately reject it as a possible domain-root escape.
func (fp *FileSystemPanel) moveToParentAfterLoadFailure(loadVFS vfs.VFS, failedPath string) bool {
	parentPath := loadVFS.Dir(failedPath)
	if parentPath == "" || parentPath == failedPath {
		return false
	}
	if err := fp.SetKnownDirectoryPath(parentPath); err != nil {
		vtui.DebugLog("PANEL[%p]: Failed to restore parent %q after reading %q: %v", fp, parentPath, failedPath, err)
		return false
	}
	return true
}

func (fp *FileSystemPanel) readDirectoryEx(keepEntries bool) {
	if fp.CancelLoad != nil {
		fp.CancelLoad()
		fp.CancelLoad = nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	fp.loadCtx = ctx
	fp.CancelLoad = cancel
	fp.IsLoading = true
	fp.startLoadingAnimation()

	loadVFS := fp.Vfs
	loadRevealName := fp.clipboardImageRevealFor(loadVFS, loadVFS.GetPath())
	path := loadVFS.GetPath()
	suppressionToken, hasFolderHistorySuppression := fp.FolderHistorySuppression(path)
	cacheKey := DirectoryCacheKey(loadVFS, path)
	loadAtRoot := loadVFS.IsAtRoot()
	showUpEntry := !loadAtRoot || loadVFS.ParentVFS() != nil
	if fp.previousSelectionVFS != nil && !fp.previousSelectionMatches(loadVFS, path) {
		fp.clearPreviousSelection()
	}

	// Drop persistent selection when we've navigated to a different
	// directory. Without this the map (keyed by bare filename)
	// silently re-applies to any incoming entry with a matching
	// name — e.g. .claude selected in ~/f4 would come back
	// pre-selected in ~/scc or ~. Same rule far/far2l use:
	// selection is per-directory.
	directoryChanged := fp.lastLoadedPath != "" && !SameFolderHistoryPath(fp.lastLoadedPath, path)
	suppressFolderHistory := hasFolderHistorySuppression && fp.ConsumeFolderHistorySuppression(path, suppressionToken)
	if directoryChanged {
		// A quick search belongs to the directory it was typed in. Leaving the
		// directory closes it, so a filter cannot carry a stale query into rows
		// the user has never seen; re-reading the same directory keeps it.
		fp.ExitFastFind()
		for k := range fp.SelectedItems {
			delete(fp.SelectedItems, k)
		}
		fp.DirectoryEpoch++
		fp.selectionEpoch = make(map[string]uint64)
		fp.clearCalculatedPanelTotal()
	}
	fp.lastLoadedPath = path
	if directoryChanged && !suppressFolderHistory {
		// Record accepted navigation in UI order, not in backend completion
		// order. Otherwise an older slow cloud ReadDir can finish after a newer
		// visit and move its path to the front of the global MRU history.
		RecordFolderHistoryEntry(fp, loadVFS, path)
	}

	if fp.PendingSelection == "" {
		oldName := fp.GetRawSelectedName()
		if oldName != "" && oldName != ".." {
			fp.PendingSelection = oldName
		}
	}

	hasCache := false
	cacheInitialCursorName := ""
	cacheInitialCursorIndex := -1
	if !keepEntries {
		if fp.DirCache == nil {
			fp.DirCache = make(map[dirCacheKey]DirCacheEntry)
		}
		if cached, ok := fp.DirCache[cacheKey]; ok && !config.App.SyncPanelLoad {
			hasCache = true
			vtui.DebugLog("PANEL: Using cached entries for %s", path)
			var entries []*FileEntry

			if showUpEntry {
				entries = append(entries, &FileEntry{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}, IsCached: true})
			}

			for _, item := range cached.Items {
				if !config.App.ShowHiddenFiles && item.Name != ".." && item.Name != loadRevealName && item.IsHidden {
					continue
				}
				entry := &FileEntry{VFSItem: item, IsCached: true}
				fp.applyPersistentSelection(entry, loadVFS, path)
				entries = append(entries, entry)
			}

			fp.setEntries(entries)
			fp.SortEntries()

			target := fp.PendingSelection
			if target != "" {
				for i, entry := range fp.Entries {
					if entry.Name == target {
						fp.SetCursorIndex(i)
						fp.PendingSelection = ""
						break
					}
				}
			}
			fp.Refresh()
			cacheInitialCursorName = fp.GetRawSelectedName()
			cacheInitialCursorIndex = fp.GetCursorIndex()
			vtui.FrameManager.Redraw()
		}
	}

	// Building the skeleton drops whatever rows the panel is showing, so it
	// happens only for a directory the panel has nothing to show about: one it
	// has never read. Two cases keep the rows instead:
	//
	//   - a reload of the directory already on screen, where those rows are
	//     the freshest view there is. Replacing them with an empty ".."
	//     skeleton flashes it with the cursor parked on it until the async
	//     ReadDir finishes -- the jump seen after a delete or an in-place
	//     copy, which reloads twice (dirwatch plus the completion callback).
	//   - a navigation with SyncPanelLoad on, which promises to replace the
	//     listing only once the whole directory is ready. Dropping to ".."
	//     until then parks the cursor on it and bounces it onto the row the
	//     user navigated from the moment the listing arrives -- the jump seen
	//     on Ctrl+PgUp.
	// The completion task below swaps in the fresh list atomically either way,
	// the same contract the cached branch above follows.
	buildSkeleton := len(fp.AllEntries()) == 0 ||
		(directoryChanged && !config.App.SyncPanelLoad)
	isFirstChunk := true
	if !keepEntries && !hasCache && buildSkeleton {
		var entries []*FileEntry
		if showUpEntry {
			entries = []*FileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}}
		}
		fp.setEntries(entries)
		fp.SetCursorIndex(0)
		fp.Refresh()
		vtui.FrameManager.Redraw()
	}

	// The worker below runs after this function has returned, so it must not
	// reach for process-wide state as it goes. config.App and vtui.FrameManager
	// can both be replaced while it is still running -- tests do exactly that,
	// and the race detector reports it against whichever test did the
	// replacing rather than against the load that was left behind. Taking the
	// values here also makes each load self-consistent: it renders under the
	// settings that were in force when it was asked for, instead of switching
	// halfway through if something toggles them.
	loadSyncPanel := config.App.SyncPanelLoad
	loadShowHidden := config.App.ShowHiddenFiles
	loadFrames := vtui.FrameManager
	// The "LN" column needs a query per file on Windows, which the plain
	// listing does not make; pay it only while the column is on screen
	// (f4#1861).
	linkCounter, _ := loadVFS.(linkCountFiller)
	if !panelColumnsShow(PanelViewModeSettings(fp.EffectiveViewMode()).Columns, LinkCountColumn) {
		linkCounter = nil
	}

	_, localLoad := loadVFS.(*vfs.OSVFS)
	fp.enqueueDirectoryLoad(func() {
		if ctx.Err() != nil {
			return
		}
		var accumulated []vfs.VFSItem

		err := loadVFS.ReadDir(ctx, path, func(chunk []vfs.VFSItem) {
			if ctx.Err() != nil {
				return
			}
			if linkCounter != nil {
				linkCounter.FillLinkCounts(ctx, path, chunk)
			}
			accumulated = append(accumulated, chunk...)
			if ctx.Err() != nil {
				return
			}

			// A cached directory is already a complete, interactive view. Keep it
			// on screen while all real chunks are collected and replace it once,
			// atomically, in the completion task. Rendering partial real chunks
			// would make the panel jump and could overwrite user interaction with
			// a stale pendingSelection.
			if loadSyncPanel || hasCache {
				return
			}

			newEntries := make([]*FileEntry, 0, len(chunk))
			for _, item := range chunk {
				// Hide hidden files if configured, but never hide '..'
				if !loadShowHidden && item.Name != ".." && item.Name != loadRevealName && item.IsHidden {
					continue
				}
				entry := &FileEntry{VFSItem: item}
				newEntries = append(newEntries, entry)
			}

			if ctx.Err() != nil {
				return
			}

			loadFrames.PostTask(func() {
				if ctx.Err() != nil || fp.loadCtx != ctx {
					return
				}

				currentSelected := fp.GetRawSelectedName()
				currentOffset := fp.displayOfEntry(fp.GetCursorIndex()) - fp.Table.TopPos
				if fp.PendingSelection == "" {
					if currentSelected != "" && currentSelected != ".." {
						fp.PendingSelection = currentSelected
					}
				}

				if isFirstChunk {
					var entries []*FileEntry
					if showUpEntry {
						upItem := vfs.VFSItem{Name: "..", IsDir: true}
						entries = []*FileEntry{{VFSItem: upItem}}
					}
					fp.setEntries(entries)
					isFirstChunk = false
				}

				// Apply persistent selection to incoming items
				for _, e := range newEntries {
					fp.applyPersistentSelection(e, loadVFS, path)
				}

				fp.addEntries(newEntries...)
				fp.SortEntries()

				// Фокусировка на нужном файле
				snapped := false
				target := fp.PendingSelection
				if target == "" {
					target = currentSelected
				}

				if target != "" {
					for i, entry := range fp.Entries {
						if entry.Name == target {
							fp.SetCursorIndex(i)
							if entry.Name == fp.PendingSelection {
								fp.PendingSelection = ""
							}
							snapped = true
							break
						}
					}
				}

				if !snapped && fp.PendingSelection == "" && (fp.CursorIdx >= len(fp.Entries) || fp.CursorIdx < 0) {
					fp.SetCursorIndex(0)
				}

				if fp.GroupBy != GroupNone && fp.GetRawSelectedName() == currentSelected {
					fp.Table.TopPos = max(0, fp.displayOfEntry(fp.GetCursorIndex())-currentOffset)
				}
				fp.Refresh()

				loadFrames.Redraw() // Рисуем каждый чанк!
			})
		})

		if ctx.Err() != nil {
			return
		}

		// The listing is the only mandatory request and gets the shared remote
		// session first. Directory timestamps and metadata for ".." are useful
		// decoration/auto-refresh state, so fetch them only after ReadDir and
		// skip them entirely when the listing failed or was superseded.
		var dirStat vfs.VFSItem
		var upItemStat vfs.VFSItem
		hasUpItemStat := false
		if err == nil {
			var dirStatErr error
			dirStat, dirStatErr = loadVFS.Stat(ctx, path)
			if ctx.Err() != nil {
				return
			}
			if showUpEntry {
				parentPath := loadVFS.Dir(path)
				if parentPath == path && dirStatErr == nil {
					upItemStat = dirStat
					hasUpItemStat = true
				} else if pStat, statErr := loadVFS.Stat(ctx, parentPath); statErr == nil {
					upItemStat = pStat
					hasUpItemStat = true
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		loadFrames.PostTask(func() {
			if ctx.Err() != nil || fp.loadCtx != ctx {
				// This completion no longer owns the  Do not dereference the
				// current VFS here: another navigation may already have closed it.
				return
			}

			if err == nil {
				fp.saveToCacheKey(cacheKey, accumulated, showUpEntry)
			}

			if hasCache && err == nil {
				// The cached rows stayed interactive during ReadDir. Snapshot their
				// live state immediately before replacing them; this is deliberately
				// done in the UI task rather than when the load was started.
				liveCursorName := fp.GetRawSelectedName()
				liveCursorIndex := fp.GetCursorIndex()
				liveCursorOffset := fp.displayOfEntry(liveCursorIndex) - fp.Table.TopPos
				cursorMoved := liveCursorName != cacheInitialCursorName || liveCursorIndex != cacheInitialCursorIndex

				if fp.SelectedItems == nil {
					fp.SelectedItems = make(map[string]bool)
				}
				// The marks of rows the autofilter is hiding are just as real
				// as the visible ones, so the snapshot covers the whole list.
				for _, entry := range fp.AllEntries() {
					if entry.Name == ".." {
						continue
					}
					if entry.Selected {
						fp.SelectedItems[entry.Name] = true
					} else {
						delete(fp.SelectedItems, entry.Name)
					}
				}

				var entries []*FileEntry
				if showUpEntry {
					upItem := vfs.VFSItem{Name: "..", IsDir: true}
					if hasUpItemStat {
						upItem.MTime = upItemStat.MTime
						upItem.ATime = upItemStat.ATime
						upItem.CTime = upItemStat.CTime
						upItem.UnixMode = upItemStat.UnixMode
						upItem.Uid = upItemStat.Uid
						upItem.Gid = upItemStat.Gid
					}
					entries = []*FileEntry{{VFSItem: upItem}}
				}

				for _, item := range accumulated {
					if !loadShowHidden && item.Name != ".." && item.Name != loadRevealName && item.IsHidden {
						continue
					}
					entry := &FileEntry{VFSItem: item}
					fp.applyPersistentSelection(entry, loadVFS, path)
					entries = append(entries, entry)
				}
				fp.setEntries(entries)
				fp.SortEntries()

				// An unresolved navigation target still wins if the user did not
				// move on the cache. Once the cursor moved, the live cached row is
				// the user's latest and therefore authoritative choice.
				target := liveCursorName
				if fp.PendingSelection != "" && !cursorMoved {
					target = fp.PendingSelection
				}
				newCursorIndex := -1
				for i, entry := range fp.Entries {
					if entry.Name == target {
						newCursorIndex = i
						break
					}
				}
				if newCursorIndex < 0 {
					newCursorIndex = liveCursorIndex
				}
				if newCursorIndex >= len(fp.Entries) {
					newCursorIndex = len(fp.Entries) - 1
				}
				if newCursorIndex < 0 {
					newCursorIndex = 0
				}

				newTop := fp.displayOfEntry(newCursorIndex) - liveCursorOffset
				if newTop < 0 {
					newTop = 0
				}
				fp.Table.TopPos = newTop
				fp.SetCursorIndex(newCursorIndex)
				fp.PendingSelection = ""
				isFirstChunk = false
			} else if loadSyncPanel && err == nil {
				var entries []*FileEntry
				if showUpEntry {
					upItem := vfs.VFSItem{Name: "..", IsDir: true}
					if hasUpItemStat {
						upItem.MTime = upItemStat.MTime
						upItem.ATime = upItemStat.ATime
						upItem.CTime = upItemStat.CTime
						upItem.UnixMode = upItemStat.UnixMode
						upItem.Uid = upItemStat.Uid
						upItem.Gid = upItemStat.Gid
					}
					entries = []*FileEntry{{VFSItem: upItem}}
				}

				for _, item := range accumulated {
					if !loadShowHidden && item.Name != ".." && item.Name != loadRevealName && item.IsHidden {
						continue
					}
					entry := &FileEntry{VFSItem: item}
					fp.applyPersistentSelection(entry, loadVFS, path)
					entries = append(entries, entry)
				}
				fp.setEntries(entries)
				fp.SortEntries()

				if fp.PendingSelection != "" {
					fp.SelectName(fp.PendingSelection)
					fp.PendingSelection = ""
				} else if fp.CursorIdx >= len(fp.Entries) || fp.CursorIdx < 0 {
					fp.SetCursorIndex(0)
				}
				isFirstChunk = false
			}

			if err == nil {
				// Clean up persistent selection only after the fresh list has been
				// built. With a cache, doing this earlier would compare the marks
				// against stale rows rather than the completed ReadDir result.
				all := fp.AllEntries()
				validNames := make(map[string]bool, len(all))
				for _, e := range all {
					validNames[e.Name] = true
				}
				for name := range fp.SelectedItems {
					if !validNames[name] {
						delete(fp.SelectedItems, name)
					}
				}
				if fp.previousSelectionMatches(loadVFS, path) {
					for name := range fp.previousSelection {
						if !validNames[name] {
							delete(fp.previousSelection, name)
						}
					}
				}
			}

			fp.StopLoadingAnimation()

			fp.lastDirMTime = dirStat.MTime
			fp.IsLoading = false
			if err != nil && err != context.Canceled {
				// A session that died is a question rather than a message: the
				// panel can often be had back, and going up a level or showing
				// an error would throw away the answer before it was asked.
				if fp.offerPanelReconnect(err, keepEntries) {
					return
				}
				if os.IsNotExist(err) && !loadAtRoot && !keepEntries {
					if !fp.moveToParentAfterLoadFailure(loadVFS, path) {
						fp.updateTitle(err)
						fp.showDirectoryError(" Error ", fmt.Sprintf("Failed to read directory:\n%v", err))
						return
					}
					vtui.DebugLog("PANEL[%p]: Directory disappeared, attempting to go up. Error: %v", fp, err)
					fp.ReadDirectory()
					return
				}

				// For permission or network errors, go back to parent and show the error.
				if !loadAtRoot && !keepEntries {
					if fp.moveToParentAfterLoadFailure(loadVFS, path) {
						fp.PendingSelection = loadVFS.Base(path)
						fp.ReadDirectory()
					} else {
						fp.updateTitle(err)
					}
					fp.showDirectoryError(" Error ", fmt.Sprintf("Cannot access folder:\n%v", err))
					return
				}

				fp.updateTitle(err)
				fp.showDirectoryError(" Error ", fmt.Sprintf("Failed to read directory:\n%v", err))
				return
			} else {
				fp.updateTitle(nil)
			}

			if isFirstChunk {
				var entries []*FileEntry
				if showUpEntry {
					upItem := vfs.VFSItem{Name: "..", IsDir: true}
					if hasUpItemStat {
						upItem.MTime = upItemStat.MTime
						upItem.ATime = upItemStat.ATime
						upItem.CTime = upItemStat.CTime
						upItem.UnixMode = upItemStat.UnixMode
						upItem.Uid = upItemStat.Uid
						upItem.Gid = upItemStat.Gid
					}
					entries = []*FileEntry{{VFSItem: upItem}}
				}
				fp.setEntries(entries)
				fp.SetCursorIndex(0)
			} else if showUpEntry && hasUpItemStat {
				// The ".." row was created with the first chunk, before the
				// parent was stat'ed (that happens only after ReadDir).
				fp.applyUpItemStat(upItemStat)
			}

			if fp.PendingSelection != "" {
				fp.SelectName(fp.PendingSelection)
				fp.PendingSelection = ""
			}
			fp.Refresh()
			loadFrames.Redraw()
		})
	}, localLoad)
}

// applyUpItemStat gives the ".." row the times, mode and owner of the parent
// folder, the same values the other load paths put into a freshly built row.
func (fp *FileSystemPanel) applyUpItemStat(st vfs.VFSItem) {
	for _, e := range fp.AllEntries() {
		if e.Name != ".." {
			continue
		}
		e.MTime = st.MTime
		e.ATime = st.ATime
		e.CTime = st.CTime
		e.UnixMode = st.UnixMode
		e.Uid = st.Uid
		e.Gid = st.Gid
		return
	}
}

func (fp *FileSystemPanel) Refresh() {
	// Refresh is the general "something about this panel's entries changed,
	// redisplay" hook, called after every mutation that is not already routed
	// through setEntries/addEntries/refilterEntries — a background size scan
	// landing on an entry (actionCalcDirSize) chief among them. Bumping the
	// entries revision here too, on top of those three, means panelEntryTotals
	// never needs to trust that every future caller of such a mutation
	// remembers to invalidate it by hand.
	fp.entriesRevision++
	idx := fp.GetCursorIndex()
	fp.updateSortColumnTitles()
	n := fp.displayCount()
	fp.Table.SetCellProvider(fp)
	fp.Table.SetRowCount(n)
	fp.SetCursorIndex(idx)
	_, _, maxTop, _, _ := fp.panelScrollMetrics()
	if fp.Table.TopPos > maxTop {
		fp.Table.TopPos = maxTop
		fp.SetCursorIndex(idx)
	}
}

// panelEntryTotals sums the byte size, file count and directory count across
// every entry the panel holds (not just the visible rows), for the fallback
// bottom-border total Show draws when the VFS has not already supplied a
// recursive one via CalculatedPanelTotal. Show used to redo this full scan of
// fp.Entries on every single render frame regardless of whether anything about
// the entries had changed since the previous one -- the exact same
// per-frame-per-file cost, scaling with how many files are in the directory,
// that #1511 and #1536 already fixed for the menu bar and the highlight
// rules (f4#884). The result is memoized against entriesRevision, which every
// place that can change what fp.Entries holds (setEntries, addEntries,
// refilterEntries, and Refresh as a catch-all for direct entry mutations such
// as actionCalcDirSize) increments.
func (fp *FileSystemPanel) panelEntryTotals() (totSize int64, totCount, totFiles, totDirs int) {
	if fp.entryTotalsValid && fp.entryTotalsRevision == fp.entriesRevision {
		return fp.cachedTotSize, fp.cachedTotCount, fp.cachedTotFiles, fp.cachedTotDirs
	}
	for _, e := range fp.Entries {
		if e.Name == ".." {
			continue
		}
		totCount++
		if e.IsDir {
			totDirs++
		} else {
			totFiles++
			totSize += e.Size
		}
	}
	fp.cachedTotSize, fp.cachedTotCount, fp.cachedTotFiles, fp.cachedTotDirs = totSize, totCount, totFiles, totDirs
	fp.entryTotalsRevision = fp.entriesRevision
	fp.entryTotalsValid = true
	fp.entryTotalsComputeCount++
	return
}

// SetCalculatedPanelTotal stores a recursive total for the panel's current
// directory. The path tag prevents a result from being shown after a VFS has
// moved elsewhere before the asynchronous scan completes.
func (fp *FileSystemPanel) SetCalculatedPanelTotal(stats vfs.OpStats) {
	if fp == nil || fp.Vfs == nil {
		return
	}
	statsCopy := stats
	fp.calculatedPanelTotal = &statsCopy
	fp.calculatedPanelTotalPath = fp.Vfs.GetPath()
}

func (fp *FileSystemPanel) clearCalculatedPanelTotal() {
	if fp == nil {
		return
	}
	fp.calculatedPanelTotal = nil
	fp.calculatedPanelTotalPath = ""
}

// CalculatedPanelTotal returns the recursive total if it still belongs to the
// directory currently displayed by the panel.
func (fp *FileSystemPanel) CalculatedPanelTotal() (vfs.OpStats, bool) {
	if fp == nil || fp.Vfs == nil || fp.calculatedPanelTotal == nil {
		return vfs.OpStats{}, false
	}
	if fp.calculatedPanelTotalPath != fp.Vfs.GetPath() {
		return vfs.OpStats{}, false
	}
	return *fp.calculatedPanelTotal, true
}

func (fp *FileSystemPanel) Show(scr *vtui.ScreenBuf) {
	fp.Frame.Show(scr)
	titleAttr := vtui.Palette[theme.ColPanelTitle]
	if fp.currentTitle != "" {
		availW := (fp.X2 - fp.X1) - 6
		if availW < 5 {
			availW = 5
		}
		displayTitle := fp.currentTitle
		if runewidth.StringWidth(displayTitle) > availW {
			displayTitle = vtui.TruncateMiddle(displayTitle, availW)
		}

		scr.Write(fp.X1+2, fp.Y1, vtui.StringToCharInfo(" ", titleAttr))
		scr.Write(fp.X1+3, fp.Y1, vtui.StringToCharInfo(displayTitle, titleAttr))
		scr.Write(fp.X1+3+runewidth.StringWidth(displayTitle), fp.Y1, vtui.StringToCharInfo(" ", titleAttr))
	}

	// Search-first keeps the active panel cursor visible while keyboard focus
	// is in the command line, but renders it with dedicated inactive colors.
	if fp.showInactiveCursor {
		fp.Table.ColorSelectedTextIdx = theme.ColPanelInactiveCursor
		fp.Table.ColorItemSelectCursorIdx = theme.ColPanelInactiveSelectedCursor
		fp.Table.SetFocus(true)
	} else {
		fp.Table.ColorSelectedTextIdx = theme.ColPanelCursor
		fp.Table.ColorItemSelectCursorIdx = theme.ColPanelSelectedCursor
		fp.Table.SetFocus(fp.IsFocused())
	}
	fp.clampNameLeftPos()
	fp.Table.Show(scr)
	fp.drawGroupHeadings(scr)
	fp.drawFastFindMatches(scr)
	fp.drawCursorSeparators(scr)
	fp.drawNameScrollBrackets(scr)
	fp.drawScrollBar(scr)

	var totSize int64
	var totCount int
	var totFiles int
	var totDirs int
	calculatedTotal, hasCalculatedTotal := fp.CalculatedPanelTotal()
	if !hasCalculatedTotal {
		if provider, ok := fp.Vfs.(interface{ CalculatedPanelTotal() (vfs.OpStats, bool) }); ok && fp.Vfs.IsAtRoot() {
			calculatedTotal, hasCalculatedTotal = provider.CalculatedPanelTotal()
		}
	}
	if hasCalculatedTotal {
		totSize = calculatedTotal.Bytes
		totFiles = int(calculatedTotal.Files)
		totDirs = int(calculatedTotal.Dirs)
		totCount = totFiles + totDirs
	}
	if !hasCalculatedTotal {
		totSize, totCount, totFiles, totDirs = fp.panelEntryTotals()
	}
	freeSpaceStr := ""
	if _, isLocal := fp.Vfs.(*vfs.OSVFS); isLocal {
		if info, ok := sysinfo.FS(fp.Vfs.GetPath()); ok {
			freeSpaceStr = formatBytes(info.Free)
		}
	}

	if config.App.ShowPanelFileInfo && fp.Y2-fp.Y1+1 > 6 {
		p := vtui.NewPainter(scr)
		attrBox := vtui.Palette[theme.ColPanelBox]
		// far2l paints the per-file status line with COL_PANELTEXT;
		// COL_PANELINFOTEXT (Panel.Text.Info) belongs to the info panel and
		// quick view, which use it in info_panel.go and quick_view_panel.go.
		attrInfo := vtui.Palette[theme.ColPanelText]

		p.DrawLine(fp.X1+1, fp.Y2-2, fp.X2-1, fp.Y2-2, '─', attrBox, false, false)
		scr.Write(fp.X1, fp.Y2-2, vtui.StringToCharInfo("├", attrBox))
		scr.Write(fp.X2, fp.Y2-2, vtui.StringToCharInfo("┤", attrBox))

		p.Fill(fp.X1+1, fp.Y2-1, fp.X2-1, fp.Y2-1, ' ', attrInfo)

		idx := fp.GetCursorIndex()
		if idx >= 0 && idx < len(fp.Entries) {
			e := fp.Entries[idx]

			dateStr := e.MTime.Format("02.01.06 15:04")
			sizeStr := entrySizeText(e)
			if e.Name == ".." && hasCalculatedTotal {
				sizeStr = fileops.FormatIntWithSpaces(calculatedTotal.Bytes)
			}

			nameStr := e.Name
			if target, ok := fp.symlinkTarget(e); ok {
				if fp.Vfs.GetPath() == "net://" {
					nameStr = e.Name + " -> " + target
				} else {
					sizeStr = "→ " + target
				}
			}

			rightStr := fmt.Sprintf("%s  %s", sizeStr, dateStr)

			if fp.Vfs != nil && fp.Vfs.GetPath() == "net://" {
				rightStr = ""
			}

			availW := (fp.X2 - 1) - (fp.X1 + 1) + 1
			rightW := runewidth.StringWidth(rightStr)

			if availW > rightW+1 {
				// far2l's status line is a right-aligned name column: a name
				// that does not fit loses its beginning, not its extension.
				nameStr = truncateNameKeepingEnd(nameStr, availW-rightW-1)
			} else {
				nameStr = ""
				rightStr = runewidth.Truncate(rightStr, availW, "")
			}

			if line, custom := fp.statusLineText(e, (fp.X2-1)-(fp.X1+1)+1); custom {
				// The mode has its own status columns (f4#410).
				p.DrawString(fp.X1+1, fp.Y2-1, line, attrInfo)
			} else {
				p.DrawString(fp.X1+1, fp.Y2-1, nameStr, attrInfo)
				if rightStr != "" {
					p.DrawString(fp.X2-runewidth.StringWidth(rightStr), fp.Y2-1, rightStr, attrInfo)
				}
			}
		}
	}

	var selSize int64
	var selFiles int
	var selDirs int

	for _, e := range fp.Entries {
		if e.Name != ".." && e.Selected {
			if e.IsDir {
				selDirs++
				if e.SizeCalculated {
					selSize += e.Size
				}
			} else {
				selFiles++
				selSize += e.Size
			}
		}
	}

	// far2l (FileList::ShowSelectedSize) keeps the two summaries apart when
	// the status line is on: the selection summary sits centred on the
	// separator above the status line, and the panel total stays on the
	// bottom border underneath it. Only with the status line off do they
	// share the bottom border, where the selection summary wins (#394).
	fileInfoShown := config.App.ShowPanelFileInfo && fp.Y2-fp.Y1+1 > 6
	availBottom := fp.X2 - fp.X1 - 1

	selStr := ""
	if selFiles > 0 || selDirs > 0 {
		selStr = fmt.Sprintf(" "+i18n.Msg("Panel.SelectedInfo")+" ", fileops.FormatIntWithSpaces(selSize), selFiles, selDirs)
	}

	totalStr := ""
	var attrTotal uint64
	if selStr != "" && !fileInfoShown {
		totalStr = selStr
		attrTotal = vtui.Palette[theme.ColPanelSelectedInfo]
	} else if totCount > 0 {
		totalStr = fmt.Sprintf(" %s (%d/%d) ", fileops.FormatIntWithSpaces(totSize), totFiles, totDirs)
		if freeSpaceStr != "" {
			totalStr = fmt.Sprintf(" %s (%d/%d) — %s ", fileops.FormatIntWithSpaces(totSize), totFiles, totDirs, freeSpaceStr)
		}
		attrTotal = vtui.Palette[theme.ColPanelTotalInfo]
	}

	if selStr != "" && fileInfoShown {
		if selW := runewidth.StringWidth(selStr); selW < availBottom {
			p := vtui.NewPainter(scr)
			p.DrawString(fp.X1+1+(availBottom-selW)/2, fp.Y2-2, selStr, vtui.Palette[theme.ColPanelSelectedInfo])
		}
	}

	totalStart := fp.X2
	if totalStr != "" {
		totalW := runewidth.StringWidth(totalStr)
		if totalW < availBottom {
			totalStart = fp.X1 + 1 + (availBottom-totalW)/2
			p := vtui.NewPainter(scr)
			p.DrawString(totalStart, fp.Y2, totalStr, attrTotal)
		}
	}

	// The bottom frame now carries two numbers, so they must not read as one.
	// The panel total keeps the centre and its own colour; the entry under
	// the cursor is pinned to the left corner behind a ▸ marker. Both are in
	// exact bytes, the way far2l and the Size column spell them, and so is
	// the selected-files line. Folders, links and ".." name their kind
	// instead, the way the Size column does. When the far2l status line is
	// switched on it already states all of this right above, so the marker
	// steps aside.
	if !config.App.ShowPanelFileInfo && fp.gridColumnCount() > 1 {
		if idx := fp.GetCursorIndex(); idx >= 0 && idx < len(fp.Entries) {
			e := fp.Entries[idx]
			curStr := entrySizeText(e)
			if e.Name == ".." && hasCalculatedTotal {
				curStr = fileops.FormatIntWithSpaces(calculatedTotal.Bytes)
			}
			if target, ok := fp.symlinkTarget(e); ok {
				curStr = "→ " + target
			}
			curStr = " ▸ " + curStr + " "
			maxCurW := totalStart - (fp.X1 + 1)
			if totalStart < fp.X2 {
				// Keep one border cell between the two numbers: the cursor
				// entry ends with a space and the total starts with one, so
				// without it a narrow panel reads "158 114 573 197 216 698"
				// as a single figure (#1640).
				maxCurW--
			}
			if maxCurW > 0 {
				if runewidth.StringWidth(curStr) > maxCurW {
					curStr = runewidth.Truncate(curStr, maxCurW, "")
				}
				p := vtui.NewPainter(scr)
				p.DrawString(fp.X1+1, fp.Y2, curStr, vtui.Palette[theme.ColPanelText])
			}
		}
	}

	if fp.FastFindMode {
		// Ask the screen for the text-entry caret instead of writing
		// DECSCUSR to stdout behind the renderer's back: the renderer
		// knows which terminals take the sequence and which must be
		// driven through the console API (f4 #219, classic conhost draws
		// DECSCUSR's underline as a one-pixel hairline).
		scr.SetCursorShape(vtui.InsertCursorShape())
		boxW := fastFindBoxWidth(fp.autoFilterMode)
		boxH := fp.fastFindBoxHeight()

		fx1 := fp.X1 + 9
		if fx1+boxW-1 >= scr.Width() {
			fx1 = scr.Width() - boxW
		}
		if fx1 < 0 {
			fx1 = 0
		}
		fx2 := fx1 + boxW - 1

		fy1 := fp.Y2 - boxH + 1
		if fy1 < 0 {
			fy1 = 0
		}
		fy2 := fy1 + boxH - 1

		p := vtui.NewPainter(scr)

		p.Fill(fx1, fy1, fx2, fy2, ' ', vtui.Palette[vtui.ColDialogText])
		p.DrawBox(fx1, fy1, fx2, fy2, vtui.Palette[vtui.ColDialogBox], vtui.DoubleBox)
		title := i18n.Msg("Viewer.SearchTitle")
		if fp.autoFilterMode {
			title = i18n.Msg("Panel.AutoFilterTitle")
		}
		p.DrawTitle(fx1, fy1, fx2, title, vtui.Palette[vtui.ColDialogBoxTitle])

		searchStr := fp.FastFindStr
		for runewidth.StringWidth(searchStr) > boxW-3 {
			runes := []rune(searchStr)
			searchStr = string(runes[1:])
		}

		searchColor := vtui.Palette[theme.ColPanelFastFindNoMatch]
		// An empty filter hides nothing, so it is not a failed search either.
		if fp.fastFindHasMatches() || (fp.autoFilterMode && autoFilterQuery(fp.FastFindStr) == "") {
			searchColor = vtui.Palette[vtui.ColMenuHighlight]
		}
		// The query is typed on the field colour every dialog's edit control
		// has; the foreground stays the match / no-match colour of the text
		// (f4#1131).
		editAttr := vtui.Palette[vtui.ColDialogEdit]
		p.Fill(fx1+1, fy1+1, fx2-1, fy1+1, ' ', editAttr)
		searchAttr := fastFindMatchAttr(editAttr, searchColor)
		p.DrawString(fx1+1, fy1+1, searchStr, searchAttr)

		if fp.autoFilterMode {
			// The exact-match option sits in the filter window itself, where the
			// query is typed (f4#1131). The glyph is the one a dialog checkbox uses.
			sym := vtui.SymCheckboxOff
			if config.App.PanelStrictAutoFilter {
				sym = vtui.SymCheckboxOn
			}
			textAttr := vtui.Palette[vtui.ColDialogText]
			p.DrawSymGlyph(fx1+2, fy1+2, sym, textAttr)
			p.DrawString(fx1+5, fy1+2, " "+i18n.Msg("Panel.AutoFilterExact"), textAttr)
			fp.exactBoxX1, fp.exactBoxX2, fp.exactBoxY = fx1+2, fx2-2, fy1+2
		}

		scr.SetCursorPos(fx1+1+runewidth.StringWidth(searchStr), fy1+1)
		scr.SetCursorVisible(true)
	}
}

// fastFindBoxHeight is the height of the search window: the filter has a
// second line for its exact-match option.
func (fp *FileSystemPanel) fastFindBoxHeight() int {
	if fp.autoFilterMode {
		return 4
	}
	return 3
}

// fastFindBoxWidth makes the window wide enough for the exact-match option in
// the current language: its glyph, a space and the label, between two cells
// of frame and one of air on each side.
func fastFindBoxWidth(filter bool) int {
	const base = 24
	if !filter {
		return base
	}
	return max(base, runewidth.StringWidth(i18n.Msg("Panel.AutoFilterExact"))+4+4+1)
}

// ToggleExactAutoFilter flips the exact-match option and re-derives the
// narrowed list at once, so the checkbox shows its effect as it is clicked.
func (fp *FileSystemPanel) ToggleExactAutoFilter() {
	config.App.PanelStrictAutoFilter = !config.App.PanelStrictAutoFilter
	config.SaveConfig()
	if fp.autoFilterMode {
		fp.updateAutoFilter()
	}
}

// exactCheckboxAt reports whether the pointer is over the exact-match line of
// the open filter window.
func (fp *FileSystemPanel) exactCheckboxAt(x, y int) bool {
	return fp.FastFindMode && fp.autoFilterMode && y == fp.exactBoxY && x >= fp.exactBoxX1 && x <= fp.exactBoxX2
}

func (fp *FileSystemPanel) fastFindMatch(name string) (startRunes, matchedRunes int, ok bool) {
	if !fp.FastFindMode || fp.FastFindStr == "" {
		return 0, 0, false
	}
	queryText := fp.FastFindStr
	anywhere := strings.HasPrefix(queryText, "*")
	if anywhere {
		queryText = strings.TrimPrefix(queryText, "*")
	}
	if queryText == "" {
		return 0, 0, anywhere
	}
	// Matchers are cached per query text: the needle tables are built once
	// per keystroke, not once per visible row per redraw. The strict setting
	// is part of the cache key too, so toggling it (a "live" f4:config
	// option) takes effect on the next redraw instead of the next keystroke.
	strict := config.App.PanelStrictAutoFilter
	if fp.fastFindMatcherKey != queryText || fp.fastFindMatcherStrict != strict {
		fp.fastFindMatcherKey = queryText
		fp.fastFindMatcherStrict = strict
		fp.fastFindMatchers = fp.fastFindMatchers[:0]
		// The layout-transcoded variant ("сфы" -> "cas") is a tolerance too:
		// strict mode searches exactly what was typed (f4 #1709).
		queries := []string{queryText}
		if !strict {
			queries = append(queries, vtui.GlobalXlator.TranscodeString(queryText))
		}
		for _, query := range queries {
			if m := vtui.NewFuzzyMatcher(query, false); m != nil {
				if strict {
					m.Strict()
				}
				fp.fastFindMatchers = append(fp.fastFindMatchers, m)
			}
		}
	}
	bestScore := -1
	bestStart, bestEnd := 0, -1
	for _, m := range fp.fastFindMatchers {
		score, start, end, found := m.Match(name)
		if !found || (!anywhere && start != 0) {
			continue
		}
		if bestScore < 0 || score < bestScore || (score == bestScore && start < bestStart) {
			bestScore, bestStart, bestEnd = score, start, end
		}
	}
	if bestScore < 0 {
		return 0, 0, false
	}
	return bestStart, bestEnd - bestStart + 1, true
}

func (fp *FileSystemPanel) fastFindHasMatches() bool {
	for _, entry := range fp.Entries {
		if _, _, ok := fp.fastFindMatch(entry.Name); ok {
			return true
		}
	}
	return false
}

func fastFindMatchAttr(baseAttr, matchAttr uint64) uint64 {
	if matchAttr&vtui.IsFgRGB != 0 {
		return vtui.SetRGBFore(baseAttr, vtui.GetRGBFore(matchAttr))
	}
	return vtui.SetIndexFore(baseAttr, vtui.GetIndexFore(matchAttr))
}

func (fp *FileSystemPanel) drawFastFindMatches(scr *vtui.ScreenBuf) {
	if !fp.FastFindMode || fp.FastFindStr == "" || !fp.Table.IsVisible() {
		return
	}
	if fp.Table.ViewHeight <= 0 {
		return
	}
	matchAttr := vtui.Palette[theme.ColPanelHighlightText]
	fp.visibleNameCells(func(entry *FileEntry, x, y, cellWidth int) {
		matchStart, matchedRunes, _ := fp.fastFindMatch(entry.Name)
		for _, span := range panelFileNameMatchSpansAt(entry, cellWidth, fp.nameLeftPos, matchStart, matchedRunes) {
			for cellOffset := 0; cellOffset < span.width; cellOffset++ {
				cell := scr.GetCell(x+span.start+cellOffset, y)
				cell.Attributes = fastFindMatchAttr(cell.Attributes, matchAttr)
				scr.Write(x+span.start+cellOffset, y, []vtui.CharInfo{cell})
			}
		}
	})
}

func (fp *FileSystemPanel) SetPosition(x1, y1, x2, y2 int) {
	fp.ScreenObject.SetPosition(x1, y1, x2, y2)
	fp.Frame.SetPosition(x1, y1, x2, y2)
	// Table stays inside the frame, reserving space for status info only when enabled.
	if config.App.ShowPanelFileInfo && y2-y1+1 > 6 {
		fp.Table.SetPosition(x1+1, y1+1, x2-1, y2-3)
	} else {
		fp.Table.SetPosition(x1+1, y1+1, x2-1, y2-1)
	}
}

func (fp *FileSystemPanel) Resize(w, h int) {
	fp.SetPosition(fp.X1, fp.Y1, fp.X1+w-1, fp.Y1+h-1)

	// The panel's inner table is w-2 cells wide; the mode's columns share it
	// the way far2l's PrepareColumnWidths shares a panel.
	mode := fp.EffectiveViewMode()
	layout := preparePanelLayout(PanelViewModeSettings(mode).Columns, w-2)
	layout.mode = mode
	layout.generation = panelViewModes.generation
	fp.layout = layout
	fp.Table.Columns = panelTableColumns(layout)
	fp.configureCellSelection()
	fp.updateSortColumnTitles()
	fp.Refresh()
}

// isShiftSelectNavKey reports whether a virtual key is a navigation
// key that participates in the Shift+nav selection session. The set
// mirrors the nav switch below so any change stays in sync.
func isShiftSelectNavKey(vk uint16) bool {
	switch vk {
	case vtinput.VK_UP, vtinput.VK_DOWN, vtinput.VK_LEFT, vtinput.VK_RIGHT,
		vtinput.VK_PRIOR, vtinput.VK_NEXT, vtinput.VK_HOME, vtinput.VK_END:
		return true
	}
	return false
}

// shiftSelectDirection returns +1 for keys that move the cursor
// forward (Down/Right/PgDn/End) and -1 for backward (Up/Left/
// PgUp/Home). Used to look past a ".." starting row so
// session-mode detection can still see a real, selectable row.
func shiftSelectDirection(vk uint16) int {
	switch vk {
	case vtinput.VK_DOWN, vtinput.VK_RIGHT, vtinput.VK_NEXT, vtinput.VK_END:
		return 1
	case vtinput.VK_UP, vtinput.VK_LEFT, vtinput.VK_PRIOR, vtinput.VK_HOME:
		return -1
	}
	return 0
}

func (fp *FileSystemPanel) ProcessKey(e *vtinput.InputEvent) bool {
	return fp.processKey(e, false)
}

// EnterSelectedFromAction enters the selected directory or archive for an
// explicit panel action such as Ctrl+PgDn. It is intentionally distinct from
// ordinary Enter so providers can reserve that action for entries that should
// not open on Enter or a double-click.
func (fp *FileSystemPanel) EnterSelectedFromAction() bool {
	return fp.processKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	}, true)
}

func (fp *FileSystemPanel) processKey(e *vtinput.InputEvent, allowProviderPanelEnter bool) bool {
	if !e.KeyDown {
		return false
	}
	if fp.ProviderOpenTask != nil {
		// A provider row has started an asynchronous mount, but there is no
		// child VFS to navigate yet. Autorepeat Enter must be idempotent here;
		// once the real child is installed, later repeats may navigate it.
		if e.VirtualKeyCode == vtinput.VK_RETURN {
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_ESCAPE {
			sourceSelection := fp.ProviderOpenSourceSelect
			sourcePath := fp.Vfs.GetPath()
			fp.cancelProviderOpen()
			fp.IsLoading = false
			fp.StopLoadingAnimation()
			fp.updateTitle(nil)
			fp.PendingSelection = sourceSelection
			fp.SuppressNextFolderHistory(sourcePath)
			fp.ReadDirectory()
			vtui.FrameManager.Redraw()
			return true
		}
	}

	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0

	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0

	// Detailed view has no horizontal cell navigation. Outside Vim mode,
	// reuse plain Left/Right as Page Up/Page Down while preserving the rest
	// of the event (notably Shift selection).
	if panelViewModeIsSingleStripe(fp.ViewMode) && config.App.NavigationMode != config.NavigationVim && !ctrl && !alt &&
		(e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT) {
		Mapped := *e
		if e.VirtualKeyCode == vtinput.VK_LEFT {
			Mapped.VirtualKeyCode = vtinput.VK_PRIOR
		} else {
			Mapped.VirtualKeyCode = vtinput.VK_NEXT
		}
		e = &Mapped
	}

	// Close the shift-selection session on anything other than a
	// Shift+nav key so the next Shift+nav re-decides its mode
	// from the row under the cursor. The closing key is also far's
	// moment to re-sort: a session's rows only jump up once Shift is
	// let go, so the sweep itself stays visually stable.
	if !shift || !isShiftSelectNavKey(e.VirtualKeyCode) {
		if fp.shiftSessionActive {
			fp.shiftSessionActive = false
			fp.resortSelectedFirst()
		}
	}

	if fp.FastFindMode {
		// While the panel is narrowed every visible row is already a match, so
		// navigation keys walk the result and the filter stays up -- that walk
		// is the point of filtering. The cursor-moving search has nothing to
		// walk and still closes on them.
		filtering := fp.autoFilterMode
		if !filtering && (e.VirtualKeyCode == vtinput.VK_UP || e.VirtualKeyCode == vtinput.VK_DOWN) {
			fp.ExitFastFind()
			vtui.FrameManager.Redraw()
			// Reprocess the key as ordinary panel navigation now that Fast Find
			// no longer owns it.
			return fp.ProcessKey(e)
		}
		switch e.VirtualKeyCode {
		case vtinput.VK_LEFT, vtinput.VK_RIGHT, vtinput.VK_PRIOR, vtinput.VK_NEXT, vtinput.VK_HOME, vtinput.VK_END:
			if !filtering {
				fp.ExitFastFind()
				vtui.FrameManager.Redraw()
			}
			// Проваливаемся дальше, чтобы обработать саму навигацию
		case vtinput.VK_ESCAPE:
			fp.ExitFastFind()
			vtui.FrameManager.Redraw()
			return true
		case vtinput.VK_DELETE:
			// Fast Find keeps its insertion point at the end, so forward Delete
			// has nothing to remove. Consume it here rather than allowing the
			// panel-level Del binding to hide the panels.
			return true
		}
		if filtering && e.VirtualKeyCode == vtinput.VK_E && ctrl && !shift && !alt {
			fp.ToggleExactAutoFilter()
			vtui.FrameManager.Redraw()
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_F2 && !shift && !ctrl && !alt {
			if strings.HasPrefix(fp.FastFindStr, "*") {
				fp.FastFindStr = strings.TrimPrefix(fp.FastFindStr, "*")
			} else {
				fp.FastFindStr = "*" + fp.FastFindStr
			}
			if autoFilterQuery(fp.FastFindStr) == "" && !fp.autoFilterMode {
				fp.ExitFastFind()
			} else {
				fp.applyFastFind()
			}
			vtui.FrameManager.Redraw()
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_BACK {
			if len(fp.FastFindStr) > 0 {
				runes := []rune(fp.FastFindStr)
				fp.FastFindStr = string(runes[:len(runes)-1])
				// A bare "*" is no query at all: erasing the last character of
				// a quick search ends it instead of matching everything. The
				// filter window stays open with every row shown -- it closes
				// only on Alt, Esc or Enter (#1131).
				if autoFilterQuery(fp.FastFindStr) == "" && !fp.autoFilterMode {
					fp.ExitFastFind()
				} else {
					fp.applyFastFind()
				}
			}
			vtui.FrameManager.Redraw()
			return true
		}
		// Up and Down never reach this point: the cursor-moving search gave
		// them back to ordinary navigation above, and under the filter they
		// have to walk the narrowed list, which the navigation switch below
		// does properly (grid columns, Shift-selection, scrolling).
		if e.VirtualKeyCode == vtinput.VK_RETURN && ctrl && !alt {
			dir := 1
			if shift {
				dir = -1
			}
			fp.doFastFind(dir)
			vtui.FrameManager.Redraw()
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_RETURN {
			fp.ExitFastFind()
			vtui.FrameManager.Redraw()
			// Проваливаемся ниже, чтобы обработать Enter как вход в файл/директорию
		} else if e.Char != 0 && !ctrl {
			fp.FastFindStr += string(unicode.ToLower(e.Char))
			fp.applyFastFind()
			vtui.FrameManager.Redraw()
			return true
		}
	} else {
		searchFirstInput := config.App.NavigationMode == config.NavigationSearchFirst && fp.IsFocused() && !alt
		if e.Char != 0 && (alt || searchFirstInput) && !ctrl && unicode.IsPrint(e.Char) {
			// Typing a name is always the quick search, also with the
			// autofilter enabled: the filter has its own key (a lone Alt
			// or Panel.AutoFilter, see autofilter.go).
			fp.FastFindMode = true
			fp.autoFilterMode = false
			fp.FastFindStr = string(unicode.ToLower(e.Char))
			fp.applyFastFind()
			vtui.FrameManager.Redraw()
			return true
		}
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_INSERT:
		if shift || ctrl || alt {
			return false
		}
		idx := fp.GetCursorIndex()
		fp.ToggleSelection(idx)
		fp.SetCursorIndex(idx + 1)
		// Far re-sorts after Ins while "selected first" is on, keeping the
		// cursor on the row it just moved to.
		fp.resortSelectedFirst()
		return true

	case vtinput.VK_UP, vtinput.VK_DOWN, vtinput.VK_LEFT, vtinput.VK_RIGHT, vtinput.VK_PRIOR, vtinput.VK_NEXT, vtinput.VK_HOME, vtinput.VK_END:
		if ctrl && (e.VirtualKeyCode == vtinput.VK_PRIOR || e.VirtualKeyCode == vtinput.VK_NEXT) {
			// CtrlPgUp/CtrlPgDn are hotkeys (Panel.GoParent / Panel.EnterDirectory).
			// A real keypress never reaches here: the hotkey manager consumes it
			// before FrameManager.EventFilter calls ProcessKey. An injected event
			// (a macro's Keys("CtrlPgUp")) bypasses that filter and would
			// otherwise be swallowed here as a plain page-up/down cursor move,
			// landing on ".." without actually entering it (f4 #1394). Declining
			// it lets step 4's MacroHotkey fallback run the real action.
			return false
		}
		// FAR-style Shift+nav selection.
		//
		// The session concept unifies "select" and "deselect"
		// across every navigation key: the first Shift+nav decides
		// the mode from the state of the row under the cursor
		// (unselected → we're selecting; selected → we're
		// deselecting), and every subsequent Shift+nav in the same
		// session applies that same mode. Releasing Shift (or any
		// non-nav key) closes the session; the next Shift+nav
		// starts a new one, potentially with the opposite mode.
		//
		// Range width is per-key: Up/Down affect just the starting
		// row (session grows by one row per tap); Left/Right in
		// grid, PgUp/PgDn, Home/End paint the whole [start..new]
		// sweep. ".." is skipped inside SetItemSelected.
		startIdx := fp.GetCursorIndex()

		if shift {
			if !fp.shiftSessionActive {
				fp.shiftSessionActive = true
				// Decide session mode from the state of the row
				// under the cursor. If it's ".." (never selectable),
				// look past it in the direction of movement to
				// find the first real row — otherwise the ".."
				// start would always resolve to "select" and users
				// couldn't clear a selection with Shift+End/etc
				// from the top of the list.
				modeIdx := startIdx
				if startIdx >= 0 && startIdx < len(fp.Entries) &&
					fp.Entries[startIdx].Name == ".." {
					if dir := shiftSelectDirection(e.VirtualKeyCode); dir != 0 {
						for i := startIdx + dir; i >= 0 && i < len(fp.Entries); i += dir {
							if fp.Entries[i].Name != ".." {
								modeIdx = i
								break
							}
						}
					}
				}
				if modeIdx >= 0 && modeIdx < len(fp.Entries) &&
					fp.Entries[modeIdx].Name != ".." &&
					fp.Entries[modeIdx].Selected {
					fp.shiftSessionMode = false
				} else {
					fp.shiftSessionMode = true
				}
			}
			fp.SetItemSelected(startIdx, fp.shiftSessionMode)
		}

		isMultiStep := false
		switch e.VirtualKeyCode {
		case vtinput.VK_LEFT, vtinput.VK_RIGHT, vtinput.VK_PRIOR, vtinput.VK_NEXT, vtinput.VK_HOME, vtinput.VK_END:
			isMultiStep = true
		}

		idx := startIdx
		H := fp.Table.ViewHeight
		if H <= 0 {
			H = 1
		}

		handled := false
		if fp.GroupBy != GroupNone {
			fp.SetCursorIndex(fp.groupNavigationTarget(e.VirtualKeyCode))
			handled = true
		} else if columns := fp.gridColumnCount(); columns > 1 {
			switch e.VirtualKeyCode {
			case vtinput.VK_UP:
				idx--
			case vtinput.VK_DOWN:
				idx++
			case vtinput.VK_LEFT:
				idx -= H
			case vtinput.VK_RIGHT:
				idx += H
			case vtinput.VK_PRIOR:
				idx -= H * columns
			case vtinput.VK_NEXT:
				idx += H * columns
			case vtinput.VK_HOME:
				idx = 0
			case vtinput.VK_END:
				idx = len(fp.Entries) - 1
			default:
				return false
			}
			fp.SetCursorIndex(idx)
			handled = true
		} else {
			// In Detailed mode, we let the table handle navigation but sync our index back
			if fp.Table.ProcessKey(e) {
				fp.CursorIdx = fp.Table.SelectPos
				handled = true
			}
		}

		if shift && handled && isMultiStep {
			newIdx := fp.GetCursorIndex()
			lo, hi := startIdx, newIdx
			if lo > hi {
				lo, hi = hi, lo
			}
			for i := lo; i <= hi; i++ {
				fp.SetItemSelected(i, fp.shiftSessionMode)
			}
		}
		return handled

	case vtinput.VK_RETURN:
		idx := fp.GetCursorIndex()
		if idx >= 0 && idx < len(fp.Entries) {
			selected := fp.Entries[idx]

			// Logic for leaving a virtual VFS (like an archive)
			if selected.Name == ".." {

				parent := fp.Vfs.ParentVFS()
				isRoot := fp.Vfs.IsAtRoot()

				if isRoot {
					if parent != nil {
						oldPath := fp.Vfs.GetPath()
						parentSelection := ""
						if temp, ok := fp.Vfs.(*TempPanelVFS); ok {
							parentSelection = temp.parentSelection
						}

						fp.cancelProviderOpen()
						// Закрываем текущую систему (удаляем временные файлы)
						_ = fp.Vfs.Close()

						fp.Vfs = parent
						if parentSelection != "" {
							fp.PendingSelection = parentSelection
						} else if fp.ProviderEntryName != "" {
							fp.PendingSelection = fp.ProviderEntryName
							fp.ProviderEntryName = ""
						} else {
							fp.PendingSelection = fp.Vfs.Base(oldPath)
						}
						fp.showCurrentVFSLoadingRows()
						fp.ReadDirectory()
					}
					// A root without a parent has nowhere to go. This is also a
					// final safety net for a stale ".." row left by an asynchronous
					// VFS transition: never turn it into manager.Join(root, "..").
					return true
				}
			}

			// A provider transition can represent a virtual directory just as well
			// as an archive-like file. Try it before ordinary SetPath navigation so
			// those rows can truthfully use IsDir and receive directory rendering.
			fullPath := fp.Vfs.Join(fp.Vfs.GetPath(), selected.Name)
			provider := vfs.FindProvider(context.Background(), fp.Vfs, fullPath)
			if selected.IsDir && provider != nil {
				directoryProvider, ok := provider.(vfs.VirtualDirectoryProvider)
				if !ok || !directoryProvider.OpensVirtualDirectories() {
					provider = nil
				}
			}
			if provider != nil && !allowProviderPanelEnter {
				if policy, ok := provider.(vfs.PanelEnterPolicyProvider); ok &&
					!policy.PanelEnterAllowed(context.Background(), fp.Vfs, fullPath) {
					// The provider reserves ordinary Enter for another
					// action (for example, running an SFX executable).
					// Decline the panel key so PanelsFrame can dispatch
					// that action instead of swallowing the key.
					provider = nil
				}
			}
			if provider != nil {
				sourceVFS := fp.Vfs
				selectedName := selected.Name
				return fp.openVFSAsync(
					"",
					func(ctx context.Context) (vfs.VFS, error) {
						newVFS, err := provider.Open(ctx, sourceVFS, fullPath)
						if err == nil && newVFS == nil {
							err = fmt.Errorf("provider %s returned no file system", provider.Name())
						}
						return newVFS, err
					},
					func(newVFS vfs.VFS) {
						fp.ProviderEntryName = selectedName
						fp.Vfs = newVFS
						fp.PendingSelection = ".."
						fp.showCurrentVFSLoadingRows()
						fp.ReadDirectory()
					},
					func(err error) {
						fp.PendingSelection = selectedName
						if isArchiveProvider(provider) {
							vtui.ShowMessage(" Open Error ", fmt.Sprintf("Failed to open %s:\n%v", selectedName, err), []string{"&Ok"})
						} else {
							vtui.ShowMessage(" Connection Error ", fmt.Sprintf("Failed to connect to %s:\n%v", selectedName, err), []string{"&Ok"})
						}
					},
				)
			}

			if selected.IsDir {
				oldPath := fp.Vfs.GetPath()
				newPath := fp.Vfs.Join(oldPath, selected.Name)
				vtui.DebugLog("PANEL: Navigating %q -> %q", oldPath, newPath)
				if osfs, ok := fp.Vfs.(*vfs.OSVFS); ok && osfs.NeedsElevation(newPath) {
					// Resolving this path would consult the sudo helper, which can
					// block for minutes on a slow PAM prompt. f4 has one cooperative
					// UI goroutine, so doing that here would freeze the whole UI
					// (f4#1411); resolve it off the UI goroutine instead.
					fp.navigateElevatedDirectoryAsync(osfs, newPath, oldPath, selected.Name)
					return true
				}
				if err := fp.SetKnownDirectoryPath(newPath); err == nil {
					if selected.Name == ".." {
						fp.PendingSelection = fp.Vfs.Base(oldPath)
					} else {
						fp.PendingSelection = ".."
					}
					fp.ReadDirectory()
					return true
				} else {
					vtui.FrameManager.PostTask(func() {
						vtui.ShowMessage(" Error ", fmt.Sprintf("Cannot access folder:\n%v", err), []string{"&Ok"})
					})
					return true
				}
			}
		}
	}

	return false
}

func (fp *FileSystemPanel) ProcessMouse(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.MouseEventType {
		return false
	}
	if e.WheelDirection == 0 {
		// Any other pointer gesture ends the coast: the cursor has to stay
		// where the user last saw it when they click (see internal/wheel).
		fp.wheel.Stop()
	}
	if fp.ProviderOpenTask != nil {
		// The visible rows belong to the destination cache while fp.vfs still
		// points at the source. Consume panel mouse input until the switch so a
		// double-click/context action cannot run against the wrong filesystem.
		return true
	}

	isMove := e.MouseEventFlags&vtinput.MouseMoved != 0
	isRelease := !isMove && (e.ButtonState == 0 || !e.KeyDown)
	if isRelease {
		fp.lastRightClickedIdx = -1
		fp.rightDragActive = false
		fp.headerMouseActive = false
		fp.rowDragButton = 0
		fp.stopDragAutoScroll()
		fp.finishColumnResize()
	}

	// A resize drag in progress takes priority over every other header/row
	// hit-test below: once the border is grabbed, the pointer driving it is
	// what matters, not whatever happens to be under it (f4#246).
	if isMove && fp.columnResizeActive {
		fp.dragColumnResize(int(e.MouseX))
		return true
	}

	if e.WheelDirection == 0 && fp.groupHeadingAt(int(e.MouseX), int(e.MouseY)) {
		return true
	}
	if e.WheelDirection == 0 && e.ButtonState&vtinput.FromLeft1stButtonPressed != 0 &&
		e.KeyDown && e.MouseEventFlags&vtinput.MouseMoved == 0 {
		if column, ok := fp.headerColumnBorderAt(int(e.MouseX), int(e.MouseY)); ok {
			fp.startColumnResize(column, int(e.MouseX))
			return true
		}
		if mode, ok := fp.headerSortModeAt(int(e.MouseX), int(e.MouseY)); ok {
			fp.headerMouseActive = true
			fp.SetSortMode(mode)
			return true
		}
	}

	if e.WheelDirection == 0 && e.ButtonState&vtinput.FromLeft1stButtonPressed != 0 && e.KeyDown &&
		!isMove && fp.exactCheckboxAt(int(e.MouseX), int(e.MouseY)) {
		fp.ToggleExactAutoFilter()
		vtui.FrameManager.Redraw()
		return true
	}

	if fp.processScrollBarMouse(e) {
		return true
	}

	if isMove && fp.rowDragButton != 0 {
		if fp.updateDragAutoScroll(int(e.MouseY)) {
			return true
		}
	}

	if fp.FastFindMode && e.ButtonState != 0 && !fp.autoFilterMode {
		// A filtered panel keeps its filter here on purpose: the rows under
		// the pointer are the filtered ones, and giving the hidden rows back
		// before this click is resolved would land it on a different file.
		// The filter closes on Esc, on Enter, and on leaving the directory.
		fp.FastFindMode = false
		fp.FastFindStr = ""
		vtui.FrameManager.Redraw()
	}

	if e.WheelDirection != 0 {
		// Determine direction: up is -1, down is 1
		direction := 1
		speed := config.App.WheelPanelDown
		if e.WheelDirection > 0 {
			direction = -1
			speed = config.App.WheelPanelUp
		}
		step := direction * config.WheelScrollLines(speed)
		// A spin faster than one notch per spin window queues extra
		// lines the panel keeps scrolling on its own (see internal/wheel).
		fp.wheel.Notch(direction, fp.wheelScrollBy)
		fp.wheelScrollBy(step)
		return true
	}

	isRightDragMove := isMove && fp.rightDragActive &&
		(e.ButtonState&vtinput.RightmostButtonPressed != 0 || e.ButtonState == 0)
	if e.ButtonState == vtinput.RightmostButtonPressed && e.KeyDown || isRightDragMove {
		idx := fp.mouseEntryIndex(int(e.MouseX), int(e.MouseY))
		if idx >= 0 {
			fp.rowDragButton = vtinput.RightmostButtonPressed
			fp.SetCursorIndex(idx)
			if fp.Entries[idx].Name == ".." {
				return true
			}
			if e.MouseEventFlags&vtinput.DoubleClick != 0 {
				// Windows reports the second press as a double-click. The first
				// press has already established whether this is a select or
				// deselect operation, so propagate its result to the whole
				state := fp.Entries[idx].Selected
				fp.SetAllItemsSelected(state)
				fp.rightDragActive = true
				fp.rightDragSelect = state
				fp.lastRightClickedIdx = idx
				fp.Refresh()
				return true
			}
			fp.processRightDrag(idx)
			fp.Refresh()
			return true
		}
		// Keep the drag captured while the pointer temporarily leaves a file row.
		return fp.rightDragActive
	}

	if fp.GroupBy != GroupNone {
		if idx := fp.mouseEntryIndex(int(e.MouseX), int(e.MouseY)); idx >= 0 && e.ButtonState&vtinput.FromLeft1stButtonPressed != 0 {
			fp.SetCursorIndex(idx)
			fp.rowDragButton = vtinput.FromLeft1stButtonPressed
			return true
		}
		return false
	}
	handled := fp.Table.ProcessMouse(e)
	if handled {
		if e.KeyDown && !isMove && e.ButtonState&vtinput.FromLeft1stButtonPressed != 0 {
			fp.rowDragButton = vtinput.FromLeft1stButtonPressed
		}
		// Sync absolute index from table's visual selection
		if fp.gridColumnCount() == 1 {
			fp.CursorIdx = fp.Table.SelectPos
		} else {
			H := fp.Table.ViewHeight
			if H <= 0 {
				H = 1
			}
			// vtui set SelectCol to the table column clicked; the panel keeps
			// the stripe there. SelectPos is already absolute (TopPos + row)
			// in Medium mode, so we just add the stripe offset.
			fp.Table.SelectCol = fp.stripeOfColumn(fp.Table.SelectCol)
			newIdx := fp.Table.SelectPos + fp.Table.SelectCol*H

			// Fix for "click in empty space": if we selected an empty slot,
			// snap to the last valid entry.
			if newIdx >= len(fp.Entries) {
				fp.SetCursorIndex(len(fp.Entries) - 1)
			} else {
				fp.CursorIdx = newIdx
			}
		}
	}

	return handled
}

func (fp *FileSystemPanel) GetSelectedName() string {
	idx := fp.GetCursorIndex()
	if len(fp.Entries) == 0 || idx < 0 || idx >= len(fp.Entries) {
		return ""
	}
	entry := fp.Entries[idx]
	if entry.Name == ".." {
		return fp.Vfs.Dir(fp.Vfs.GetPath())
	}
	return entry.Name
}

func (fp *FileSystemPanel) GetRawSelectedName() string {
	idx := fp.GetCursorIndex()
	if len(fp.Entries) == 0 || idx < 0 || idx >= len(fp.Entries) {
		return ""
	}
	return fp.Entries[idx].Name
}

// GetSelectedIsDir backs vfs.SelectedIsDirHost (f4#1356): it reports whether
// the cursor entry is a directory by reading the already-cached
// vfs.VFSItem.IsDir on that *FileEntry, the same field GetSelectedName
// already reads to name the entry -- no Stat, no VFS round trip. The ".."
// pseudo-entry is always built with IsDir: true, so a cursor parked on it is
// correctly reported as a directory too. known is false only when there is
// no entry under the cursor to ask.
func (fp *FileSystemPanel) GetSelectedIsDir() (isDir bool, known bool) {
	idx := fp.GetCursorIndex()
	if len(fp.Entries) == 0 || idx < 0 || idx >= len(fp.Entries) {
		return false, false
	}
	return fp.Entries[idx].IsDir, true
}

// SetSelectedByName picks or unpicks an entry by name and reports whether the
// panel shows such an entry at all. It is how the picture gallery keeps the
// panel underneath in step with what the reader has picked; a panel that has
// walked away to another directory simply answers no.
func (fp *FileSystemPanel) SetSelectedByName(name string, state bool) bool {
	for i, e := range fp.Entries {
		if e.Name == name {
			fp.SetItemSelected(i, state)
			return true
		}
	}
	return false
}

// IsNameSelected reports whether an entry has been picked explicitly.
func (fp *FileSystemPanel) IsNameSelected(name string) bool {
	return fp.SelectedItems[name]
}

type PanelSelectionToken struct {
	Panel          *FileSystemPanel
	Vfs            vfs.VFS
	path           string
	name           string
	DirectoryEpoch uint64
	selectionEpoch uint64
}

func (fp *FileSystemPanel) CaptureSelectionToken(name string) (PanelSelectionToken, bool) {
	if fp == nil || !fp.IsNameSelected(name) || fp.Vfs == nil {
		return PanelSelectionToken{}, false
	}
	return PanelSelectionToken{
		Panel:          fp,
		Vfs:            fp.Vfs,
		path:           fp.Vfs.GetPath(),
		name:           name,
		DirectoryEpoch: fp.DirectoryEpoch,
		selectionEpoch: fp.selectionEpoch[name],
	}, true
}

func (fp *FileSystemPanel) ClearSelectionIfUnchanged(token PanelSelectionToken) bool {
	if fp == nil || token.Panel != fp || fp.Vfs == nil || !fileops.SameVFSInstance(fp.Vfs, token.Vfs) || fp.Vfs.GetPath() != token.path ||
		fp.DirectoryEpoch != token.DirectoryEpoch || fp.selectionEpoch[token.name] != token.selectionEpoch ||
		!fp.IsNameSelected(token.name) {
		return false
	}
	return fp.SetSelectedByName(token.name, false)
}

// ImageSiblings lists the pictures of this panel in the order it shows them,
// together with the position of the one under the cursor, or minus one when
// the cursor is not on a picture.
func (fp *FileSystemPanel) ImageSiblings() ([]string, int) {
	return fp.siblingsWhere(media.IsImageFile)
}

// VideoSiblings is ImageSiblings for films: the video files of this panel in
// the order it shows them, and the position of the one under the cursor, or
// minus one when the cursor is not on one.
func (fp *FileSystemPanel) VideoSiblings() ([]string, int) {
	return fp.siblingsWhere(media.IsVideoFile)
}

// siblingsWhere lists the files of the panel that keep(name) accepts, in the
// order the panel shows them, and the position of the one under the cursor.
func (fp *FileSystemPanel) siblingsWhere(keep func(name string) bool) ([]string, int) {
	current := fp.GetRawSelectedName()
	names := make([]string, 0, len(fp.Entries))
	index := -1
	for _, e := range fp.Entries {
		if e.IsDir || e.Name == ".." || !keep(e.Name) {
			continue
		}
		if e.Name == current {
			index = len(names)
		}
		names = append(names, e.Name)
	}
	return names, index
}

// AudioSiblings is ImageSiblings for recordings: the audio files of this
// panel in the order it shows them, and the position of the one under the
// cursor, or minus one when the cursor is not on one.
func (fp *FileSystemPanel) AudioSiblings() ([]string, int) {
	current := fp.GetRawSelectedName()
	names := make([]string, 0, len(fp.Entries))
	index := -1
	for _, e := range fp.Entries {
		if e.IsDir || e.Name == ".." || !media.IsAudioFile(e.Name) {
			continue
		}
		if e.Name == current {
			index = len(names)
		}
		names = append(names, e.Name)
	}
	return names, index
}

// SelectName searches for an entry by name and moves the cursor to it.
func (fp *FileSystemPanel) SelectName(name string) {
	for i, entry := range fp.Entries {
		if entry.Name == name {
			fp.SetCursorIndex(i)
			fp.Refresh()
			break
		}
	}
}

// selectionMemo lets a caller that asks the same panel for its selection many
// times in a row, with nothing changing in between, walk the listing once
// (f4#1832). While it is on, GetSelectedNames hands back the first answer.
type selectionMemo struct {
	mu    sync.Mutex
	on    bool
	valid bool
	names []string
}

// MemoizeSelection makes GetSelectedNames answer from its first result until
// the returned function is called. The result is shared, so callers must only
// read it. Use it around a pass that does not change the panel, such as
// refreshing the dimmed menu rows.
func (fp *FileSystemPanel) MemoizeSelection() (end func()) {
	fp.selMemo.mu.Lock()
	fp.selMemo.on, fp.selMemo.valid, fp.selMemo.names = true, false, nil
	fp.selMemo.mu.Unlock()
	return func() {
		fp.selMemo.mu.Lock()
		fp.selMemo.on, fp.selMemo.valid, fp.selMemo.names = false, false, nil
		fp.selMemo.mu.Unlock()
	}
}

// GetSelectedNames returns a list of selected files. If none are selected, returns the focused one.
func (fp *FileSystemPanel) GetSelectedNames() []string {
	fp.selMemo.mu.Lock()
	if fp.selMemo.on && fp.selMemo.valid {
		names := fp.selMemo.names
		fp.selMemo.mu.Unlock()
		return names
	}
	fp.selMemo.mu.Unlock()
	names := fp.selectedNames()
	fp.selMemo.mu.Lock()
	if fp.selMemo.on {
		fp.selMemo.names, fp.selMemo.valid = names, true
	}
	fp.selMemo.mu.Unlock()
	return names
}

// selectionWalks counts the walks of a listing that GetSelectedNames made, for
// the responsiveness test of the menu (f4#1832).
var selectionWalks atomic.Int64

// SelectionWalks is the number of times GetSelectedNames has walked a whole
// listing since the process started.
func SelectionWalks() int64 { return selectionWalks.Load() }

func (fp *FileSystemPanel) selectedNames() []string {
	selectionWalks.Add(1)
	var names []string
	// 1. Collect explicitly selected items (ins/shift+arrows)
	for _, e := range fp.Entries {
		if e.Selected && e.Name != ".." {
			names = append(names, e.Name)
		}
	}
	// 2. If nothing is selected, fallback to the item under cursor
	if len(names) == 0 {
		idx := fp.GetCursorIndex()
		if idx >= 0 && idx < len(fp.Entries) {
			entry := fp.Entries[idx]
			// CRITICAL: Prevent actions on parent directory ".."
			if entry.Name != ".." {
				names = append(names, entry.Name)
			}
		}
	}
	return names
}

// GetMarkedNames returns only explicitly marked panel items, in panel order.
// Unlike GetSelectedNames it deliberately does not fall back to the cursor.
func (fp *FileSystemPanel) GetMarkedNames() []string {
	names := make([]string, 0)
	for _, entry := range fp.Entries {
		if entry.Selected && entry.Name != ".." {
			names = append(names, entry.Name)
		}
	}
	return names
}

// GetMarkedFolderPaths returns explicitly marked directories in panel order.
// The cursor alone and directory symlinks do not define a search scope.
func (fp *FileSystemPanel) GetMarkedFolderPaths() []string {
	paths := make([]string, 0)
	for _, entry := range fp.Entries {
		if entry.Selected && entry.IsDir && !entry.IsSymlink && entry.Name != ".." {
			paths = append(paths, fp.Vfs.Join(fp.Vfs.GetPath(), entry.Name))
		}
	}
	return paths
}

// ReplaceMarkedNames atomically replaces the explicit panel selection.
func (fp *FileSystemPanel) ReplaceMarkedNames(names []string) {
	selected := make(map[string]struct{}, len(names))
	for _, name := range names {
		selected[name] = struct{}{}
	}
	for _, entry := range fp.Entries {
		_, entry.Selected = selected[entry.Name]
	}
	fp.Refresh()
}

// GetSuccessorName determines which file should receive focus after the current
// selection (or focused item) is deleted or moved.
func (fp *FileSystemPanel) doFastFind(dir int) {
	if fp.FastFindStr == "" {
		return
	}
	startIdx := fp.GetCursorIndex()

	checkMatch := func(i int) bool {
		_, _, ok := fp.fastFindMatch(fp.Entries[i].Name)
		return ok
	}

	switch dir {
	case 0:
		for i := startIdx; i < len(fp.Entries); i++ {
			if checkMatch(i) {
				fp.SetCursorIndex(i)
				fp.Refresh()
				return
			}
		}
		for i := 0; i < startIdx; i++ {
			if checkMatch(i) {
				fp.SetCursorIndex(i)
				fp.Refresh()
				return
			}
		}
	case 1:
		for i := startIdx + 1; i < len(fp.Entries); i++ {
			if checkMatch(i) {
				fp.SetCursorIndex(i)
				fp.Refresh()
				return
			}
		}
		for i := 0; i <= startIdx; i++ {
			if checkMatch(i) {
				fp.SetCursorIndex(i)
				fp.Refresh()
				return
			}
		}
	case -1:
		for i := startIdx - 1; i >= 0; i-- {
			if checkMatch(i) {
				fp.SetCursorIndex(i)
				fp.Refresh()
				return
			}
		}
		for i := len(fp.Entries) - 1; i >= startIdx; i-- {
			if checkMatch(i) {
				fp.SetCursorIndex(i)
				fp.Refresh()
				return
			}
		}
	}
}

// SaveSelection snapshots the current selection into FileEntry.PrevSelected.
// Called by every mass-selection operation (mask select/deselect, invert,
// select-all/deselect-all) so that RestoreSelection has a well-defined
// state to bring back. Mirrors far2l's FileList::SaveSelection().
func (fp *FileSystemPanel) SaveSelection() {
	previous := make(map[string]bool)
	for _, e := range fp.Entries {
		e.PrevSelected = e.Selected
		if e.Name != ".." && e.Selected {
			previous[e.Name] = true
		}
	}
	fp.previousSelection = previous
	fp.previousSelectionVFS = fp.Vfs
	if fp.Vfs != nil {
		fp.previousSelectionPath = fp.Vfs.GetPath()
	} else {
		fp.previousSelectionPath = ""
	}
}

// RestoreSelection swaps the current selection with the last snapshot taken
// by SaveSelection. Because the swap goes both ways, pressing Ctrl+M twice
// returns to the state you started from. Mirrors far2l's
// FileList::RestoreSelection().
func (fp *FileSystemPanel) RestoreSelection() {
	saved := fp.previousSelection
	if fp.Vfs == nil || !fp.previousSelectionMatches(fp.Vfs, fp.Vfs.GetPath()) {
		saved = nil
	}
	current := make(map[string]bool)
	for i, e := range fp.Entries {
		if e.Name == ".." {
			continue
		}
		if e.Selected {
			current[e.Name] = true
		}
		e.PrevSelected = e.Selected
		fp.SetItemSelected(i, saved[e.Name])
	}
	fp.previousSelection = current
	fp.previousSelectionVFS = fp.Vfs
	if fp.Vfs != nil {
		fp.previousSelectionPath = fp.Vfs.GetPath()
	} else {
		fp.previousSelectionPath = ""
	}
	fp.resortSelectedFirst()
	vtui.FrameManager.Redraw()
}

func (fp *FileSystemPanel) InvertSelection() {
	fp.SaveSelection()
	for i, e := range fp.Entries {
		if e.Name != ".." {
			fp.SetItemSelected(i, !e.Selected)
		}
	}
	fp.resortSelectedFirst()
	vtui.FrameManager.Redraw()
}

func (fp *FileSystemPanel) ApplyMaskSelection(mask string, state bool) {
	if mask == "" {
		return
	}
	// Far style: *.* matches everything
	if mask == "*.*" {
		mask = "*"
	}
	maskLower := strings.ToLower(mask)

	fp.SaveSelection()
	for i, e := range fp.Entries {
		if e.Name == ".." {
			continue
		}
		matchName := e.Name
		if _, ok := fp.Vfs.(*TempPanelVFS); ok {
			// Temp Panel displays the referenced full path.  Far's file-mask
			// selection still applies to the item name, so a mask such as
			// "*.txt" must match the basename rather than the path separators.
			matchName = path.Base(strings.ReplaceAll(matchName, "\\", "/"))
		}
		nameLower := strings.ToLower(matchName)
		matched, _ := filepath.Match(maskLower, nameLower)
		if matched {
			fp.SetItemSelected(i, state)
		}
	}
	fp.resortSelectedFirst()
	vtui.FrameManager.Redraw()
}

func (fp *FileSystemPanel) GetSuccessorName() string {
	if len(fp.Entries) <= 1 {
		return ".."
	}

	anySelected := false
	for _, e := range fp.Entries {
		if e.Selected && e.Name != ".." {
			anySelected = true
			break
		}
	}

	var firstIdx, lastIdx int

	if anySelected {
		// If something is selected, we only care about the selection range
		firstIdx = len(fp.Entries)
		lastIdx = -1
		for i, e := range fp.Entries {
			if e.Selected && e.Name != ".." {
				if i < firstIdx {
					firstIdx = i
				}
				if i > lastIdx {
					lastIdx = i
				}
			}
		}
	} else {
		// If nothing selected, the "range" is just the current cursor
		firstIdx = fp.CursorIdx
		lastIdx = fp.CursorIdx
	}

	// Helper to check if an item at index i is about to be removed
	isToBeRemoved := func(i int) bool {
		if anySelected {
			return fp.Entries[i].Selected && fp.Entries[i].Name != ".."
		}
		return i == fp.CursorIdx
	}

	// 1. Try to find the first valid item AFTER the removed block
	for i := lastIdx + 1; i < len(fp.Entries); i++ {
		if !isToBeRemoved(i) {
			return fp.Entries[i].Name
		}
	}

	// 2. If no "next" item, try to find the first valid item BEFORE the removed block
	for i := firstIdx - 1; i >= 0; i-- {
		if !isToBeRemoved(i) && fp.Entries[i].Name != ".." {
			return fp.Entries[i].Name
		}
	}

	// 3. Fallback to parent directory entry
	return ".."
}

// GetPredecessorName returns the first remaining entry before the item (or
// selected range) that an action is about to remove. If there is no such
// entry, it falls back to the first remaining entry after it. The fallback
// keeps the cursor on a useful row when the first item is removed.
func (fp *FileSystemPanel) GetPredecessorName() string {
	if len(fp.Entries) <= 1 {
		return ".."
	}

	anySelected := false
	for _, e := range fp.Entries {
		if e.Selected && e.Name != ".." {
			anySelected = true
			break
		}
	}

	var firstIdx, lastIdx int
	if anySelected {
		firstIdx = len(fp.Entries)
		lastIdx = -1
		for i, e := range fp.Entries {
			if e.Selected && e.Name != ".." {
				if i < firstIdx {
					firstIdx = i
				}
				if i > lastIdx {
					lastIdx = i
				}
			}
		}
	} else {
		firstIdx = fp.CursorIdx
		lastIdx = fp.CursorIdx
	}

	isToBeRemoved := func(i int) bool {
		if anySelected {
			return fp.Entries[i].Selected && fp.Entries[i].Name != ".."
		}
		return i == fp.CursorIdx
	}

	for i := firstIdx - 1; i >= 0; i-- {
		if !isToBeRemoved(i) && fp.Entries[i].Name != ".." {
			return fp.Entries[i].Name
		}
	}

	for i := lastIdx + 1; i < len(fp.Entries); i++ {
		if !isToBeRemoved(i) && fp.Entries[i].Name != ".." {
			return fp.Entries[i].Name
		}
	}

	return ".."
}

// CurrentPanelEntryPath returns the full path represented by the panel cursor.
// As in Far, the parent entry represents the current directory for path-copy
// and path-insertion commands.
func CurrentPanelEntryPath(fsp *FileSystemPanel) string {
	if fsp == nil || fsp.Vfs == nil {
		return ""
	}
	idx := fsp.GetCursorIndex()
	if idx < 0 || idx >= len(fsp.Entries) {
		return ""
	}
	base := fsp.Vfs.GetPath()
	if fsp.Entries[idx].Name == ".." {
		return base
	}
	return fsp.Vfs.Join(base, fsp.Entries[idx].Name)
}

// raiseFolderChanged raises the FolderChanged event of the Lua macros when the
// panel has entered a folder other than the one it last reported (a refresh of
// the same folder is not a change).
func raiseFolderChanged(fp *FileSystemPanel, path string) {
	if fp == nil || path == "" || fp.folderEventPath == path {
		return
	}
	fp.folderEventPath = path
	macro.MacroMgr.RaiseEvent("FolderChanged")
}
