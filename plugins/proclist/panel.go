//go:build linux || windows || darwin

package proclist

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Column indices into procRow.GetCellText, also used by compareSamples so
// sorting stays numeric on PID/Mem/CPU% instead of the lexical order a Table
// falls back to for column text. These are stable identities across a
// restart -- allColumnSpecs' own key field, not this int, is what
// ProcList.Config (settings.go, f4#312 part 4 of 4) actually persists to
// disk, so renumbering these later stays safe.
const (
	colPID = iota
	colName
	colMem
	colCPU
)

// columnSpec is a self-contained definition of one table column: its
// identity (id, for compareSamples; key, for Settings.VisibleColumns), its
// header (titleKey) and how to render one sample's cell. procListColumns and
// procRow both key off allColumnSpecs instead of hardcoding each column's
// header/format logic in two places, so ProcList.Config's visible-columns
// setting only has to filter *which* columns exist, not duplicate how each
// one renders.
type columnSpec struct {
	id       int
	key      string // stable settings.json identifier; see columnSpecsForKeys/columnKeysValid.
	titleKey string
	width    int
	minWidth int
	align    vtui.Alignment
	cellText func(sample) string
}

// allColumnSpecs is also defaultColumnKeys' own order, and the fixed
// left-to-right order any subset of it is shown in: this plugin lets a user
// hide a column (ProcList.Config), not reorder the rest, matching how
// narrowly it scopes everything else (plugin.go's own package doc).
var allColumnSpecs = []columnSpec{
	{id: colPID, key: "pid", titleKey: "ProcList.ColumnPID", width: 8, align: vtui.AlignRight,
		cellText: func(s sample) string { return strconv.Itoa(s.pid) }},
	{id: colName, key: "name", titleKey: "ProcList.ColumnName", minWidth: 12,
		cellText: func(s sample) string { return s.name }},
	{id: colMem, key: "mem", titleKey: "ProcList.ColumnMem", width: 10, align: vtui.AlignRight,
		cellText: func(s sample) string { return formatRSSKiB(s.rssKiB) }},
	{id: colCPU, key: "cpu", titleKey: "ProcList.ColumnCPU", width: 7, align: vtui.AlignRight,
		cellText: func(s sample) string { return formatCPUPercent(s.cpuPercent) }},
}

// columnSpecsForKeys resolves Settings.VisibleColumns to the columnSpecs
// procListColumns/procRow use, in allColumnSpecs' fixed order and silently
// dropping any key it does not recognize. An empty or entirely-unrecognized
// result falls back to every column instead of an unusable, columnless
// table -- newProcListPanel calls this on every Open, so a settings.json
// this build cannot make sense of at all degrades to v1's own fixed layout
// rather than failing to open the panel.
func columnSpecsForKeys(keys []string) []columnSpec {
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[strings.ToLower(strings.TrimSpace(k))] = true
	}
	specs := make([]columnSpec, 0, len(allColumnSpecs))
	for _, c := range allColumnSpecs {
		if want[c.key] {
			specs = append(specs, c)
		}
	}
	if len(specs) == 0 {
		return append([]columnSpec(nil), allColumnSpecs...)
	}
	return specs
}

// defaultSortDisplayIndex locates CPU% (v1's own default sort column) among
// specs' display positions. specs is Settings-filtered, so CPU% may not be
// there at all -- ProcList.Config let the user hide it -- in which case this
// falls back to whatever ended up first, still a deterministic sort rather
// than none.
func defaultSortDisplayIndex(specs []columnSpec) (idx int, ok bool) {
	for i, c := range specs {
		if c.id == colCPU {
			return i, true
		}
	}
	return 0, len(specs) > 0
}

func procListColumns(specs []columnSpec) []vtui.TableColumn {
	cols := make([]vtui.TableColumn, len(specs))
	for i, c := range specs {
		cols[i] = vtui.TableColumn{Title: i18n.Msg(c.titleKey), Width: c.width, MinWidth: c.minWidth, Alignment: c.align}
	}
	return cols
}

// procRow adapts one sample to vtui.Table's TableRow contract. specs is the
// same slice every row of one table snapshot shares (set by applySamples
// from procListPanel.specs), so GetCellText's col is a *display* column
// index, resolved through specs rather than the fixed colPID..colCPU
// constants directly -- those still identify a column's meaning
// (compareSamples, columnSpecsForKeys), just not its position once some
// columns are hidden. marks is the panel's own selection (marks.go), which
// makes the row a vtui.SelectableRow: Table paints a selected row in the
// file panels' selected-file colours.
type procRow struct {
	s     sample
	specs []columnSpec
	marks markSet
}

func (r procRow) GetCellText(col int) string {
	if col < 0 || col >= len(r.specs) {
		return ""
	}
	return r.specs[col].cellText(r.s)
}

func (r procRow) IsSelected() bool { return r.marks.has(r.s.key()) }

func formatRSSKiB(kb uint64) string {
	switch {
	case kb >= 1<<20:
		return fmt.Sprintf("%.1f G", float64(kb)/(1<<20))
	case kb >= 1<<10:
		return fmt.Sprintf("%.1f M", float64(kb)/(1<<10))
	default:
		return fmt.Sprintf("%d K", kb)
	}
}

func formatCPUPercent(p float64) string {
	if p < 0 {
		p = 0
	}
	return fmt.Sprintf("%.1f", p)
}

// compareSamples orders two samples by one column's underlying numeric (or,
// for Name, string) value. It backs vtui.Table.SortCompare: without it,
// Table would fall back to comparing the columns' formatted display text,
// which sorts "10.0" before "9.0". Names compare as the file panels compare
// file names, ignoring case. A tie goes to the lower PID, so processes with
// equal values -- the dozens idling at 0.0% CPU, a browser's helpers all
// named alike -- keep one order from refresh to refresh instead of whatever
// order the system enumerated them in this time.
func compareSamples(a, b sample, col int) int {
	c := 0
	switch col {
	case colPID:
		return a.pid - b.pid
	case colName:
		c = compareNames(a.name, b.name)
	case colMem:
		c = compareUint64(a.rssKiB, b.rssKiB)
	case colCPU:
		c = compareFloat64(a.cpuPercent, b.cpuPercent)
	default:
		return 0
	}
	if c != 0 {
		return c
	}
	return a.pid - b.pid
}

// compareNames orders names ignoring case, the way the file panels sort
// file names; "Chrome" and "chrome" are a tie, for compareSamples' PID to
// settle. It folds rune by rune instead of lowering whole strings, so the
// sort that runs on every refresh allocates nothing.
func compareNames(a, b string) int {
	for len(a) > 0 && len(b) > 0 {
		ca, wa := utf8.DecodeRuneInString(a)
		cb, wb := utf8.DecodeRuneInString(b)
		if la, lb := unicode.ToLower(ca), unicode.ToLower(cb); la != lb {
			if la < lb {
				return -1
			}
			return 1
		}
		a, b = a[wa:], b[wb:]
	}
	return len(a) - len(b)
}

func compareUint64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareFloat64(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// procListPanel is the live panel a PanelProvider.Open returns: a
// theme-colored vtui.Table drawn inside a vtui.BorderedFrame (the same pair
// FileSystemPanel itself composes, internal/panel/list.go), refreshed from
// /proc on a ticker in the same "goroutine + RunOnUI(Redraw)" pattern
// internal/app/arkanoid.go uses for its game loop. The table is only ever
// mutated from the UI goroutine -- directly in newProcListPanel, and from
// the posted task the background loop hands to RunOnUI -- exactly like
// every other vtui control, since vtui itself is not safe for concurrent
// access from more than one goroutine.
type procListPanel struct {
	frame *vtui.BorderedFrame
	table *vtui.Table
	specs []columnSpec

	collector *collector
	store     *settingsStore
	stop      chan struct{}
	done      chan struct{}
	stopOnce  sync.Once

	// suspended tracks which processes this panel itself has SIGSTOPped, so
	// Ctrl+F8 (toggleSuspend, actions.go) knows whether to suspend or
	// resume next. It is not a read of the process's real state: a process
	// another tool stopped independently is not reflected here, and
	// collect() has no portable, privilege-free way to read "is this pid
	// currently stopped" back from the OS either (unlike priority, which
	// changePriority in actions_unix.go/actions_windows.go does read back
	// before every change) -- see actions.go's own note on the same gap.
	suspended map[procKey]bool

	// marks is the selection (marks.go) and prevMarks the one before the
	// last group change, which Ctrl+M brings back. shiftSession and
	// shiftMark are a Shift+arrow run in progress and whether it selects;
	// rightDrag, rightDragPos and rightMark the same for a drag with the
	// right mouse button.
	marks        markSet
	prevMarks    markSet
	shiftSession bool
	shiftMark    bool
	rightDrag    bool
	rightDragPos int
	rightMark    bool
}

// newProcListPanel builds one panel instance. store is ProcList.Config's
// settings (settings.go, f4#312 part 4 of 4); a nil store (every existing
// test, and any future caller that does not care) falls back to
// DefaultSettings() through settingsStore.snapshot's own nil-safe zero
// value, so this never needs its own separate "no settings" branch.
// Visible columns are resolved here for the first snapshot and then
// re-checked by syncColumns on every Show and every refresh, so a column
// selection changed through ProcList.Config applies to an already-open
// panel as soon as the dialog closes (f4#312, Zeroes1's point 4).
func newProcListPanel(ctx vfs.PanelContext, store *settingsStore) (vfs.PanelController, error) {
	settings := store.snapshot()
	specs := columnSpecsForKeys(settings.VisibleColumns)

	frame := vtui.NewBorderedFrame(0, 0, 1, 1, vtui.SingleBox, "")
	frame.ColorBoxIdx = theme.ColPanelBox
	frame.ColorTitleIdx = theme.ColPanelTitle
	frame.ColorBackgroundIdx = theme.ColPanelText

	table := vtui.NewTable(0, 0, 1, 1, procListColumns(specs))
	table.Sortable = true
	table.QuickSearch = true
	table.ColorBoxIdx = theme.ColPanelBox
	table.ColorTitleIdx = theme.ColPanelColumnTitle
	table.ColorTextIdx = theme.ColPanelText
	table.ColorItemSelectTextIdx = theme.ColPanelSelectedText
	table.SortCompare = func(a, b vtui.TableRow, col int) int {
		ra, aok := a.(procRow)
		rb, bok := b.(procRow)
		if !aok || !bok || col < 0 || col >= len(ra.specs) {
			return 0
		}
		return compareSamples(ra.s, rb.s, ra.specs[col].id)
	}
	if idx, ok := defaultSortDisplayIndex(specs); ok {
		table.SetSort(idx, false)
	}

	p := &procListPanel{
		frame:     frame,
		table:     table,
		specs:     specs,
		collector: newCollector(),
		store:     store,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		suspended: make(map[procKey]bool),
		marks:     make(markSet),
		prevMarks: make(markSet),
	}
	p.SetFocus(false)
	p.SetPosition(ctx.Bounds[0], ctx.Bounds[1], ctx.Bounds[2], ctx.Bounds[3])

	if samples, err := p.collector.collect(); err == nil {
		p.applySamples(samples)
	}

	go p.loop()
	return p, nil
}

// refreshInterval re-reads Settings.RefreshInterval on every tick (loop,
// below), so a change made through ProcList.Config while this panel is
// already open takes effect within one refresh cycle (the visible-columns
// half of the same setting is syncColumns' job). store.snapshot is nil-safe,
// so this needs no separate guard for a panel built with a nil store.
func (p *procListPanel) refreshInterval() time.Duration {
	if d := p.store.snapshot().refreshInterval(); d > 0 {
		return d
	}
	return defaultRefreshInterval
}

func (p *procListPanel) loop() {
	defer close(p.done)
	timer := time.NewTimer(p.refreshInterval())
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			samples, err := p.collector.collect()
			if err != nil {
				vtui.DebugLog("PROCLIST: collect failed: %v", err)
			} else {
				p.runOnUI(func() { p.applySamples(samples) })
			}
			timer.Reset(p.refreshInterval())
		case <-p.stop:
			return
		}
	}
}

// runOnUI safely queues a function to run on the main UI thread, the same
// helper ArkanoidFrame.RunOnUI provides for its own game loop.
func (p *procListPanel) runOnUI(fn func()) {
	if vtui.FrameManager != nil {
		vtui.FrameManager.PostTask(fn)
	}
}

// applySamples replaces the table's rows. It must only run on the UI
// goroutine: newProcListPanel calls it directly (the panel provider contract
// already requires the host to call Open there), and loop only ever reaches
// it through a task posted to RunOnUI, never directly from the background
// goroutine.
//
// The cursor stays on its process, wherever the new snapshot sorts it, the
// way a file panel keeps the cursor on its file across a re-read: under a
// CPU% sort the rows move on every refresh, and a cursor left at its row
// would land F3, F8 or the next Ins on whichever process moved in. Only when
// the process is gone does the cursor keep its row.
func (p *procListPanel) applySamples(samples []sample) {
	p.syncColumns()
	cursor, hadCursor := p.selectedSample()
	rows := make([]vtui.TableRow, len(samples))
	listed := make(map[procKey]bool, len(samples))
	for i, s := range samples {
		rows[i] = procRow{s: s, specs: p.specs, marks: p.marks}
		listed[s.key()] = true
	}
	p.table.SetRows(rows)
	if hadCursor {
		p.moveCursorTo(cursor.key())
	}
	// Forget a process once it is gone (exited, or simply not seen in this
	// snapshot): a reused pid must not inherit a selection or a stale
	// "resume" toggle meant for the process that used to have it.
	for k := range p.suspended {
		if !listed[k] {
			delete(p.suspended, k)
		}
	}
	p.marks.prune(listed)
	p.prevMarks.prune(listed)
	p.redraw()
}

// redraw asks for a repaint, where there is a screen to repaint.
func (p *procListPanel) redraw() {
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

// sameColumnIDs reports whether a and b show the same columns in the same
// order -- all syncColumns needs to know, since a columnSpec's rendering is
// fixed by its id.
func sameColumnIDs(a, b []columnSpec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].id != b[i].id {
			return false
		}
	}
	return true
}

// syncColumns applies Settings.VisibleColumns to an already-open panel: when
// ProcList.Config saved a different selection, the table gets the new
// columns, every current row is re-bound to them (procRow resolves a display
// column through its specs), the sort stays on the same column and
// direction if that column is still shown (otherwise it falls back to
// newProcListPanel's own default), and the cursor stays on the same process.
// It runs on the UI goroutine only (Show and applySamples) and is a no-op
// while the selection is unchanged, so calling it on every redraw is cheap.
// It reports whether the columns changed.
func (p *procListPanel) syncColumns() bool {
	specs := columnSpecsForKeys(p.store.snapshot().VisibleColumns)
	if sameColumnIDs(specs, p.specs) {
		return false
	}

	sortID := -1
	if c := p.table.SortColumn; c >= 0 && c < len(p.specs) {
		sortID = p.specs[c].id
	}
	selected, hadSelection := p.selectedSample()

	sortCol, ascending := -1, p.table.SortAscending
	if sortID >= 0 {
		for i, c := range specs {
			if c.id == sortID {
				sortCol = i
				break
			}
		}
		if sortCol < 0 {
			if idx, ok := defaultSortDisplayIndex(specs); ok {
				sortCol, ascending = idx, false
			}
		}
	}

	p.specs = specs
	p.table.Columns = procListColumns(specs)
	rows := make([]vtui.TableRow, 0, len(p.table.Rows))
	for _, r := range p.table.Rows {
		if pr, ok := r.(procRow); ok {
			rows = append(rows, procRow{s: pr.s, specs: specs, marks: p.marks})
		}
	}
	p.table.SortColumn = sortCol
	p.table.SortAscending = ascending
	p.table.SetRows(rows)

	if hadSelection {
		p.moveCursorTo(selected.key())
	}
	return true
}

// eachShown calls fn for every row the table shows, top to bottom, with its
// display position: every process, or only the QuickSearch matches while a
// search is typed.
func (p *procListPanel) eachShown(fn func(pos int, s sample)) {
	for pos := 0; pos < p.table.ItemCount; pos++ {
		if s, ok := p.sampleAt(pos); ok {
			fn(pos, s)
		}
	}
}

// sampleAt is the process shown at display position pos.
func (p *procListPanel) sampleAt(pos int) (sample, bool) {
	if pos < 0 || pos >= p.table.ItemCount {
		return sample{}, false
	}
	idx := p.table.RowAt(pos)
	if idx < 0 || idx >= len(p.table.Rows) {
		return sample{}, false
	}
	row, ok := p.table.Rows[idx].(procRow)
	if !ok {
		return sample{}, false
	}
	return row.s, true
}

// positionOf is the display position of the first shown process match
// accepts, or -1.
func (p *procListPanel) positionOf(match func(sample) bool) int {
	for pos := 0; pos < p.table.ItemCount; pos++ {
		if s, ok := p.sampleAt(pos); ok && match(s) {
			return pos
		}
	}
	return -1
}

// moveCursorTo puts the cursor on process k and scrolls it into view. It
// leaves the cursor where it is when k is not shown.
func (p *procListPanel) moveCursorTo(k procKey) {
	if pos := p.positionOf(func(s sample) bool { return s.key() == k }); pos >= 0 {
		p.table.SetSelectPos(pos)
	}
}

func (p *procListPanel) SetPosition(x1, y1, x2, y2 int) {
	p.frame.SetPosition(x1, y1, x2, y2)
	b := p.frame.GetBorderThickness()
	ix1, iy1, ix2, iy2 := x1+b, y1+b, x2-b, y2-b
	if ix2 < ix1 {
		ix2 = ix1
	}
	if iy2 < iy1 {
		iy2 = iy1
	}
	p.table.SetPosition(ix1, iy1, ix2, iy2)
}

func (p *procListPanel) GetPosition() (int, int, int, int) {
	return p.frame.GetPosition()
}

func (p *procListPanel) SetFocus(focused bool) {
	if focused {
		p.table.ColorSelectedTextIdx = theme.ColPanelCursor
		p.table.ColorItemSelectCursorIdx = theme.ColPanelSelectedCursor
	} else {
		p.table.ColorSelectedTextIdx = theme.ColPanelInactiveCursor
		p.table.ColorItemSelectCursorIdx = theme.ColPanelInactiveSelectedCursor
	}
	p.table.SetFocus(focused)
}

func (p *procListPanel) IsFocused() bool { return p.table.IsFocused() }

var _ vfs.PanelKeyProvider = (*procListPanel)(nil)

// PanelKeys declares this panel's own keys through the host's shared
// panel-plugin key primitive (vfs.PanelKeyProvider, f4#312): F3 details (a
// read-only snapshot, FAR3's own F3 gesture; showDetails, details.go), F8
// kill (with confirmation, confirmKill in actions.go), Shift+F1/F2
// lower/raise priority (FAR3's own bindings for the same thing), Ctrl+F8
// suspend/resume toggle -- new in f4, not in FAR3, and declared only where
// suspendResumeSupported is true (linux/darwin; not Windows, which has no
// supported API for it) -- and the sort keys Ctrl+F3..Ctrl+F7 and Ctrl+F12
// (sort.go). Declaring them, rather than switching on them in ProcessKey, is
// what makes the host run them ahead of the file panel's own bindings for
// the same keys and put their captions on the keybar. The selection keys
// are not declared: the file panels' own bindings for them stand down and
// hand them to ProcessKey (marks.go).
func (p *procListPanel) PanelKeys() []vfs.PanelKey {
	keys := []vfs.PanelKey{
		vfs.PanelHelpKey(i18n.Msg("KeyBar.F1"), func() string { return i18n.Msg("ProcList.HelpTitle") }, func() string { return i18n.Msg("ProcList.Help") }),
		{VK: vtinput.VK_F3, Label: i18n.Msg("ProcList.KeyBar.Details"), Run: p.showDetails},
		{VK: vtinput.VK_F8, Label: i18n.Msg("ProcList.KeyBar.Kill"), Run: p.confirmKill},
		{VK: vtinput.VK_F1, Mods: vtinput.ShiftPressed, Label: i18n.Msg("ProcList.KeyBar.PriorityDown"), Run: func() { p.adjustPriority(false) }},
		{VK: vtinput.VK_F2, Mods: vtinput.ShiftPressed, Label: i18n.Msg("ProcList.KeyBar.PriorityUp"), Run: func() { p.adjustPriority(true) }},
	}
	if suspendResumeSupported {
		keys = append(keys, vfs.PanelKey{VK: vtinput.VK_F8, Mods: vtinput.LeftCtrlPressed, Label: i18n.Msg("ProcList.KeyBar.Suspend"), Run: p.toggleSuspend})
	}
	keys = append(keys, p.sortPanelKeys()...)
	// A declared key ends a Shift+arrow selecting run, as every other key
	// does in ProcessKey: the host runs these without ProcessKey seeing them.
	for i := range keys {
		run := keys[i].Run
		keys[i].Run = func() {
			p.shiftSession = false
			run()
		}
	}
	return keys
}

// ProcessKey routes the declared PanelKeys first -- the host normally
// dispatches them before the key ever gets here, but a host without the
// PanelKeyProvider hook (and this package's tests) still reaches them --
// then the selection keys (marks.go), and hands everything else to the
// table's own navigation/quick-search handling. Any key but the next step
// of a Shift+arrow run ends the run.
func (p *procListPanel) ProcessKey(e *vtinput.InputEvent) bool {
	if e == nil {
		return false
	}
	if e.KeyDown && !isShiftMarkKey(e) {
		p.shiftSession = false
	}
	if vfs.DispatchPanelKey(p.PanelKeys(), e) {
		return true
	}
	if p.processMarkKey(e) {
		return true
	}
	return p.table.ProcessKey(e)
}

// ProcessMouse gives the right button to selection (marks.go) and the rest
// to the table. A header click that changed the sort is followed up the way
// the sort keys are (sortedByHeader).
func (p *procListPanel) ProcessMouse(e *vtinput.InputEvent) bool {
	if p.processMarkMouse(e) {
		return true
	}
	sortCol, ascending := p.table.SortColumn, p.table.SortAscending
	cursor, hadCursor := p.selectedSample()
	handled := p.table.ProcessMouse(e)
	if p.table.SortColumn != sortCol || p.table.SortAscending != ascending {
		p.sortedByHeader(sortCol, cursor, hadCursor)
	}
	return handled
}

// selectedSample returns the sample under the cursor. It backs
// GetSelectedName below and, from actions.go, every F8/Shift+F1/Shift+F2/
// Ctrl+F8 handler with nothing selected: all of them then act on "the
// process under the cursor" and none can do anything useful with an empty
// table.
func (p *procListPanel) selectedSample() (sample, bool) {
	return p.sampleAt(p.table.SelectPos)
}

// GetSelectedName reports the name of the process under the cursor, the
// closest thing a process list has to a file panel's selected file name.
func (p *procListPanel) GetSelectedName() string {
	s, ok := p.selectedSample()
	if !ok {
		return ""
	}
	return s.name
}

func (p *procListPanel) SetContext(vfs.PanelContext) {}

// SavePanelState and RestorePanelState let a bookmark return to the process
// name under the cursor (vfs.PanelStateProvider). Process ids do not survive a
// restart, so the name is the only stable handle; when no row carries it (the
// list has not filled in yet, or the process is gone) the cursor stays put.
func (p *procListPanel) SavePanelState() string { return p.GetSelectedName() }

func (p *procListPanel) RestorePanelState(state string) {
	if state == "" {
		return
	}
	if pos := p.positionOf(func(s sample) bool { return s.name == state }); pos >= 0 {
		p.table.SetSelectPos(pos)
	}
}

func (p *procListPanel) Show(scr *vtui.ScreenBuf) {
	p.syncColumns()
	p.frame.SetTitle(fmt.Sprintf(i18n.Msg("ProcList.PanelTitle"), p.table.ItemCount))
	p.frame.Show(scr)
	p.table.Show(scr)
	p.drawSelectionSummary(scr)
}

func (p *procListPanel) Close() error {
	p.stopOnce.Do(func() {
		close(p.stop)
	})
	<-p.done
	return nil
}
