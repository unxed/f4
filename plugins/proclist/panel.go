//go:build linux || windows || darwin

package proclist

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// procListRefreshInterval controls how often the background collector takes
// a new /proc snapshot. A few times a second is enough to watch CPU% move
// without turning the process list itself into a meaningful background load.
const procListRefreshInterval = 500 * time.Millisecond

// Column indices into procListColumns/procRow.GetCellText, also used by
// compareSamples so sorting stays numeric on PID/Mem/CPU% instead of the
// lexical order a Table falls back to for column text.
const (
	colPID = iota
	colName
	colMem
	colCPU
)

func procListColumns() []vtui.TableColumn {
	return []vtui.TableColumn{
		{Title: i18n.Msg("ProcList.ColumnPID"), Width: 8, Alignment: vtui.AlignRight},
		{Title: i18n.Msg("ProcList.ColumnName"), MinWidth: 12},
		{Title: i18n.Msg("ProcList.ColumnMem"), Width: 10, Alignment: vtui.AlignRight},
		{Title: i18n.Msg("ProcList.ColumnCPU"), Width: 7, Alignment: vtui.AlignRight},
	}
}

// procRow adapts one sample to vtui.Table's TableRow contract.
type procRow struct{ s sample }

func (r procRow) GetCellText(col int) string {
	switch col {
	case colPID:
		return strconv.Itoa(r.s.pid)
	case colName:
		return r.s.name
	case colMem:
		return formatRSSKiB(r.s.rssKiB)
	case colCPU:
		return formatCPUPercent(r.s.cpuPercent)
	default:
		return ""
	}
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

	collector *collector
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

func newProcListPanel(ctx vfs.PanelContext) (vfs.PanelController, error) {
	frame := vtui.NewBorderedFrame(0, 0, 1, 1, vtui.SingleBox, "")
	frame.ColorBoxIdx = theme.ColPanelBox
	frame.ColorTitleIdx = theme.ColPanelTitle
	frame.ColorBackgroundIdx = theme.ColPanelText

	table := vtui.NewTable(0, 0, 1, 1, procListColumns())
	table.Sortable = true
	table.QuickSearch = true
	table.ColorBoxIdx = theme.ColPanelBox
	table.ColorTitleIdx = theme.ColPanelColumnTitle
	table.ColorTextIdx = theme.ColPanelText
	table.ColorItemSelectTextIdx = theme.ColPanelSelectedText
	table.SortCompare = func(a, b vtui.TableRow, col int) int {
		ra, aok := a.(procRow)
		rb, bok := b.(procRow)
		if !aok || !bok {
			return 0
		}
		return compareSamples(ra.s, rb.s, col)
	}
	table.SetSort(colCPU, false)

	p := &procListPanel{
		frame:     frame,
		table:     table,
		collector: newCollector(),
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

func (p *procListPanel) loop() {
	defer close(p.done)
	ticker := time.NewTicker(procListRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			samples, err := p.collector.collect()
			if err != nil {
				vtui.DebugLog("PROCLIST: collect failed: %v", err)
				continue
			}
			p.runOnUI(func() { p.applySamples(samples) })
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
		rows[i] = procRow{s: s}
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

// ProcessKey adds f4#312 part 3's process management on top of the table's
// own navigation/sort/quick-search handling: F8 kill (with confirmation,
// confirmKill in actions.go), Shift+F1/F2 lower/raise priority (FAR3's own
// bindings for the same thing), and Ctrl+F8 suspend/resume toggle -- new in
// f4, not in FAR3, and only offered where suspendResumeSupported is true
// (linux/darwin; not Windows, which has no supported API for it). Unmatched
// keys, including Ctrl+F8 where unsupported, fall through to the table
// exactly as before this change.
func (p *procListPanel) ProcessKey(e *vtinput.InputEvent) bool {
	if e != nil && e.Type == vtinput.KeyEventType && e.KeyDown {
		ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
		alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
		shift := e.ControlKeyState&vtinput.ShiftPressed != 0
		switch {
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
