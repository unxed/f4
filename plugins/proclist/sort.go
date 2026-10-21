//go:build linux || windows || darwin

package proclist

import (
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Sorting from the keyboard (f4#2130) takes the file panels' Ctrl+F3..Ctrl+F7
// and Ctrl+F12, each on the process column that means the same thing here:
//
//	Ctrl+F3   name      the panels' name
//	Ctrl+F4   PID       the panels' extension: a process has none, and its
//	                    PID is the identifier left
//	Ctrl+F5   CPU%      the panels' time: CPU time is what a process spends
//	Ctrl+F6   memory    the panels' size
//	Ctrl+F7   unsorted  the order the system lists processes in
//	Ctrl+F12  a menu of the above
//
// As in the panels, the first press sorts a column the way it is wanted
// first -- names and PIDs ascending, CPU% and memory descending, the
// busiest on top -- and pressing the same key again reverses the order
// (FileSystemPanel.SetSortMode, internal/panel/list.go). A click on a column
// header starts a column the same way, so the mouse and the keys agree.
// Ctrl+F8 stays the suspend toggle (actions.go). A column hidden in
// ProcList.Config has nothing to sort by: its key is dimmed and consumed,
// and its menu row disabled. The cursor stays on its process throughout.

// sortUnsorted stands for Ctrl+F7 where sortModes holds a column id.
const sortUnsorted = -1

type sortMode struct {
	vk        uint16
	shortcut  string
	col       int    // colPID..colCPU, or sortUnsorted
	labelKey  string // the keybar caption
	menuKey   string // the sort menu row
	ascending bool   // the direction the first press sorts in
}

var sortModes = []sortMode{
	{vk: vtinput.VK_F3, shortcut: "Ctrl+F3", col: colName, labelKey: "ProcList.KeyBar.SortName", menuKey: "Menu.SortName", ascending: true},
	{vk: vtinput.VK_F4, shortcut: "Ctrl+F4", col: colPID, labelKey: "ProcList.KeyBar.SortPID", menuKey: "ProcList.Sort.PID", ascending: true},
	{vk: vtinput.VK_F5, shortcut: "Ctrl+F5", col: colCPU, labelKey: "ProcList.KeyBar.SortCPU", menuKey: "ProcList.Sort.CPU"},
	{vk: vtinput.VK_F6, shortcut: "Ctrl+F6", col: colMem, labelKey: "ProcList.KeyBar.SortMem", menuKey: "ProcList.Sort.Mem"},
	{vk: vtinput.VK_F7, shortcut: "Ctrl+F7", col: sortUnsorted, labelKey: "ProcList.KeyBar.SortNone", menuKey: "Menu.SortUnsorted", ascending: true},
}

// firstSortAscending is the direction col sorts in when it is picked anew.
func firstSortAscending(col int) bool {
	for _, m := range sortModes {
		if m.col == col {
			return m.ascending
		}
	}
	return true
}

// displayColumn is where specs shows the column with id, or -1 when it is
// hidden.
func displayColumn(specs []columnSpec, id int) int {
	for i, c := range specs {
		if c.id == id {
			return i
		}
	}
	return -1
}

func (p *procListPanel) canSortBy(col int) bool {
	return col == sortUnsorted || displayColumn(p.specs, col) >= 0
}

// sortedBy names the column the table is sorted by, or sortUnsorted.
func (p *procListPanel) sortedBy() (col int, ascending bool) {
	if c := p.table.SortColumn; c >= 0 && c < len(p.specs) {
		return p.specs[c].id, p.table.SortAscending
	}
	return sortUnsorted, p.table.SortAscending
}

// sortBy is one sort key: the column's first direction when it is picked
// anew, the opposite direction when it is already the one in force.
// Pressing Ctrl+F7 again has nothing to reverse: unsorted is the system's
// own order, and Table keeps no reversed form of it.
func (p *procListPanel) sortBy(col int) {
	if !p.canSortBy(col) {
		return
	}
	cursor, hadCursor := p.selectedSample()
	if col == sortUnsorted {
		p.table.ClearSort()
	} else {
		idx := displayColumn(p.specs, col)
		ascending := firstSortAscending(col)
		if p.table.SortColumn == idx {
			ascending = !p.table.SortAscending
		}
		p.table.SetSort(idx, ascending)
	}
	if hadCursor {
		p.moveCursorTo(cursor.key())
	}
	p.redraw()
}

// sortedByHeader follows up a click on a column header, which Table sorts
// on its own: a column picked anew gets its first direction, as from its
// key, and the cursor goes back to its process.
func (p *procListPanel) sortedByHeader(previousCol int, cursor sample, hadCursor bool) {
	if c := p.table.SortColumn; c != previousCol && c >= 0 && c < len(p.specs) {
		p.table.SetSort(c, firstSortAscending(p.specs[c].id))
	}
	if hadCursor {
		p.moveCursorTo(cursor.key())
	}
}

// sortPanelKeys declares Ctrl+F3..Ctrl+F7 and Ctrl+F12. They have to be
// declared rather than read from the keymap, as the selection keys are
// (marks.go): the panels' sort actions do not stand down under a panel
// plugin, so an undeclared Ctrl+F3 would re-sort the hidden file panel.
func (p *procListPanel) sortPanelKeys() []vfs.PanelKey {
	keys := make([]vfs.PanelKey, 0, len(sortModes)+1)
	for _, m := range sortModes {
		col := m.col
		keys = append(keys, vfs.PanelKey{
			VK:      m.vk,
			Mods:    vtinput.LeftCtrlPressed,
			Label:   i18n.Msg(m.labelKey),
			Run:     func() { p.sortBy(col) },
			Enabled: func() bool { return p.canSortBy(col) },
		})
	}
	return append(keys, vfs.PanelKey{VK: vtinput.VK_F12, Mods: vtinput.LeftCtrlPressed, Label: i18n.Msg("ProcList.KeyBar.SortMenu"), Run: p.showSortMenu})
}

// sortCheckMark and sortMarker are the marks of the file panels' sort menu
// (internal/panel/menu_marks.go), which a plugin cannot import from a layer
// above it: √ beside "unsorted" in force, ▲ or ▼ beside the column in force.
const sortCheckMark = "√"

func sortMarker(ascending bool) string {
	if ascending {
		return "▲"
	}
	return "▼"
}

// sortMenuWidth is the file panels' sort menu width.
const sortMenuWidth = 36

// showSortMenu is Ctrl+F12, laid out like the file panels' sort menu: one
// row per sort key with its shortcut, the mode in force marked, opened over
// the middle of this panel.
func (p *procListPanel) showSortMenu() {
	current, ascending := p.sortedBy()
	menu := vtui.NewVMenu(i18n.Msg("Sort.Title"))
	selected := 0
	for i, m := range sortModes {
		prefix := "  "
		if m.col == current {
			selected = i
			prefix = sortCheckMark + " "
			if m.col != sortUnsorted {
				prefix = sortMarker(ascending) + " "
			}
		}
		menu.AddItem(vtui.MenuItem{Text: prefix + i18n.Msg(m.menuKey), Shortcut: m.shortcut, Disabled: !p.canSortBy(m.col)})
	}
	menu.SetSelectPos(selected)
	menu.OnAction = func(idx int) {
		if idx >= 0 && idx < len(sortModes) {
			p.sortBy(sortModes[idx].col)
		}
	}

	x1, y1, x2, y2 := p.GetPosition()
	panelW, panelH := x2-x1+1, y2-y1+1
	w, h := min(sortMenuWidth, max(panelW-2, 1)), min(len(sortModes)+2, panelH)
	x, y := x1+(panelW-w)/2, y1+(panelH-h)/2
	menu.SetPosition(x, y, x+w-1, y+h-1)
	vtui.FrameManager.Push(menu)
}
