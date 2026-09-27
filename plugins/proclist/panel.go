//go:build linux || windows || darwin

package proclist

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

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
// columns are hidden.
type procRow struct {
	s     sample
	specs []columnSpec
}

func (r procRow) GetCellText(col int) string {
	if col < 0 || col >= len(r.specs) {
		return ""
	}
	return r.specs[col].cellText(r.s)
}

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
// which sorts "10.0" before "9.0".
func compareSamples(a, b sample, col int) int {
	switch col {
	case colPID:
		return a.pid - b.pid
	case colName:
		switch {
		case a.name < b.name:
			return -1
		case a.name > b.name:
			return 1
		default:
			return 0
		}
	case colMem:
		return compareUint64(a.rssKiB, b.rssKiB)
	case colCPU:
		return compareFloat64(a.cpuPercent, b.cpuPercent)
	default:
		return 0
	}
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

	// suspended tracks which pids this panel itself has SIGSTOPped, so
	// Ctrl+F8 (toggleSuspend, actions.go) knows whether to suspend or
	// resume next. It is not a read of the process's real state: a process
	// another tool stopped independently is not reflected here, and
	// collect() has no portable, privilege-free way to read "is this pid
	// currently stopped" back from the OS either (unlike priority, which
	// changePriority in actions_unix.go/actions_windows.go does read back
	// before every change) -- see actions.go's own note on the same gap.
	suspended map[int]bool
}

// newProcListPanel builds one panel instance. store is ProcList.Config's
// settings (settings.go, f4#312 part 4 of 4); a nil store (every existing
// test, and any future caller that does not care) falls back to
// DefaultSettings() through settingsStore.snapshot's own nil-safe zero
// value, so this never needs its own separate "no settings" branch.
// Visible columns are resolved once, here -- unlike RefreshInterval (below),
// which loop() re-reads every tick, rebuilding vtui.Table's columns live
// while the panel is open is a bigger change than this ticket asked for, so
// a changed column selection only takes effect the next time the panel is
// opened.
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
		suspended: make(map[int]bool),
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
// already open takes effect within one refresh cycle -- unlike the
// visible-columns half of the same setting, which newProcListPanel captures
// once at Open time (see its own comment). store.snapshot is nil-safe, so
// this needs no separate guard for a panel built with a nil store.
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
func (p *procListPanel) applySamples(samples []sample) {
	rows := make([]vtui.TableRow, len(samples))
	seen := make(map[int]bool, len(samples))
	for i, s := range samples {
		rows[i] = procRow{s: s, specs: p.specs}
		seen[s.pid] = true
	}
	p.table.SetRows(rows)
	// Forget a suspended pid once it is gone (exited, or simply not seen in
	// this snapshot): a reused pid must not inherit a stale "resume" toggle
	// meant for the process that used to have it.
	for pid := range p.suspended {
		if !seen[pid] {
			delete(p.suspended, pid)
		}
	}
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
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

// ProcessKey adds f4#312 part 3's process management, and part 4's F3
// details view (showDetails, details.go), on top of the table's own
// navigation/sort/quick-search handling: F3 details (a read-only snapshot,
// FAR3's own F3 gesture), F8 kill (with confirmation, confirmKill in
// actions.go), Shift+F1/F2 lower/raise priority (FAR3's own bindings for the
// same thing), and Ctrl+F8 suspend/resume toggle -- new in f4, not in FAR3,
// and only offered where suspendResumeSupported is true (linux/darwin; not
// Windows, which has no supported API for it). Unmatched keys, including
// Ctrl+F8 where unsupported, fall through to the table exactly as before
// this change.
func (p *procListPanel) ProcessKey(e *vtinput.InputEvent) bool {
	if e != nil && e.Type == vtinput.KeyEventType && e.KeyDown {
		ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
		alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
		shift := e.ControlKeyState&vtinput.ShiftPressed != 0
		switch {
		case e.VirtualKeyCode == vtinput.VK_F3 && !ctrl && !alt && !shift:
			p.showDetails()
			return true
		case e.VirtualKeyCode == vtinput.VK_F8 && !ctrl && !alt && !shift:
			p.confirmKill()
			return true
		case e.VirtualKeyCode == vtinput.VK_F8 && ctrl && !alt && !shift && suspendResumeSupported:
			p.toggleSuspend()
			return true
		case e.VirtualKeyCode == vtinput.VK_F1 && shift && !ctrl && !alt:
			p.adjustPriority(false)
			return true
		case e.VirtualKeyCode == vtinput.VK_F2 && shift && !ctrl && !alt:
			p.adjustPriority(true)
			return true
		}
	}
	return p.table.ProcessKey(e)
}

func (p *procListPanel) ProcessMouse(e *vtinput.InputEvent) bool { return p.table.ProcessMouse(e) }

// selectedSample returns the sample under the cursor. It backs
// GetSelectedName below and, from actions.go, every F8/Shift+F1/Shift+F2/
// Ctrl+F8 handler: all of them act on "the process under the cursor" and
// none can do anything useful with an empty table.
func (p *procListPanel) selectedSample() (sample, bool) {
	idx := p.table.RowAt(p.table.SelectPos)
	if idx < 0 || idx >= len(p.table.Rows) {
		return sample{}, false
	}
	row, ok := p.table.Rows[idx].(procRow)
	if !ok {
		return sample{}, false
	}
	return row.s, true
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

func (p *procListPanel) Show(scr *vtui.ScreenBuf) {
	p.frame.SetTitle(fmt.Sprintf(i18n.Msg("ProcList.PanelTitle"), p.table.ItemCount))
	p.frame.Show(scr)
	p.table.Show(scr)
}

func (p *procListPanel) Close() error {
	p.stopOnce.Do(func() {
		close(p.stop)
	})
	<-p.done
	return nil
}
