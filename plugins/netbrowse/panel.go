package netbrowse

import (
	"fmt"
	"strings"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Column indices into netRow.GetCellText.
const (
	colName = iota
	colKind
	colComment
)

func netColumns() []vtui.TableColumn {
	return []vtui.TableColumn{
		{Title: i18n.Msg("NetBrowse.ColumnName"), MinWidth: 20},
		{Title: i18n.Msg("NetBrowse.ColumnKind"), Width: 10},
		{Title: i18n.Msg("NetBrowse.ColumnComment"), MinWidth: 10},
	}
}

// netRow is one row of the table: an entry, or the ".." row that goes up.
type netRow struct {
	res *resource // nil for ".."
}

func (r netRow) GetCellText(col int) string {
	if r.res == nil {
		if col == colName {
			return ".."
		}
		return ""
	}
	switch col {
	case colName:
		return r.res.Remote
	case colKind:
		return kindName(r.res.Display)
	case colComment:
		return r.res.Comment
	}
	return ""
}

// netPanel is the panel a PanelProvider.Open returns: a theme-colored table in
// a bordered frame, like plugins/git and plugins/svcmgr. path is the chain of
// containers entered so far; the table lists the entries of the last one (the
// top of the network when the chain is empty).
type netPanel struct {
	frame *vtui.BorderedFrame
	table *vtui.Table
	list  enumerator
	path  []resource
}

func newNetPanel(ctx vfs.PanelContext, list enumerator) (vfs.PanelController, error) {
	frame := vtui.NewBorderedFrame(0, 0, 1, 1, vtui.SingleBox, "")
	frame.ColorBoxIdx = theme.ColPanelBox
	frame.ColorTitleIdx = theme.ColPanelTitle
	frame.ColorBackgroundIdx = theme.ColPanelText

	table := vtui.NewTable(0, 0, 1, 1, netColumns())
	table.Sortable = false // the system's order (and the ".." row on top) is the order shown
	table.QuickSearch = true
	table.ColorBoxIdx = theme.ColPanelBox
	table.ColorTitleIdx = theme.ColPanelColumnTitle
	table.ColorTextIdx = theme.ColPanelText
	table.ColorItemSelectTextIdx = theme.ColPanelSelectedText

	p := &netPanel{frame: frame, table: table, list: list}
	p.SetFocus(false)
	p.SetPosition(ctx.Bounds[0], ctx.Bounds[1], ctx.Bounds[2], ctx.Bounds[3])
	if err := p.load(); err != nil {
		return nil, err
	}
	return p, nil
}

// current is the container being listed, nil at the top of the network.
func (p *netPanel) current() *resource {
	if len(p.path) == 0 {
		return nil
	}
	return &p.path[len(p.path)-1]
}

// load lists the current container into the table.
func (p *netPanel) load() error {
	entries, err := p.list(p.current())
	if err != nil {
		return err
	}
	rows := make([]vtui.TableRow, 0, len(entries)+1)
	if len(p.path) > 0 {
		rows = append(rows, netRow{})
	}
	for i := range entries {
		rows = append(rows, netRow{res: &entries[i]})
	}
	p.table.SetRows(rows)
	return nil
}

func (p *netPanel) selectedRow() (netRow, bool) {
	idx := p.table.RowAt(p.table.SelectPos)
	if idx < 0 || idx >= len(p.table.Rows) {
		return netRow{}, false
	}
	row, ok := p.table.Rows[idx].(netRow)
	return row, ok
}

// GetSelectedName reports the entry under the cursor.
func (p *netPanel) GetSelectedName() string {
	row, ok := p.selectedRow()
	if !ok || row.res == nil {
		return ""
	}
	return row.res.Remote
}

// open is Enter: ".." goes up, a container is entered, and anything else (a
// share) is reported by its UNC name for now -- opening it as a directory is
// the next part (f4#1702).
func (p *netPanel) open() {
	row, ok := p.selectedRow()
	if !ok {
		return
	}
	switch {
	case row.res == nil:
		p.up()
	case row.res.Container:
		p.enter(*row.res)
	default:
		toast.Show(fmt.Sprintf(i18n.Msg("NetBrowse.ShareName"), row.res.Remote), 3e9)
	}
}

// enter descends into a container. When it cannot be listed the panel stays
// where it was and the reason is shown.
func (p *netPanel) enter(r resource) {
	p.path = append(p.path, r)
	if err := p.load(); err != nil {
		p.path = p.path[:len(p.path)-1]
		toast.Show(fmt.Sprintf(i18n.Msg("NetBrowse.EnterFailed"), r.Remote, err), 3e9)
		return
	}
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

// up goes back to the container above, with the cursor on the one just left.
func (p *netPanel) up() {
	if len(p.path) == 0 {
		return
	}
	left := p.path[len(p.path)-1].Remote
	p.path = p.path[:len(p.path)-1]
	if err := p.load(); err != nil {
		toast.Show(fmt.Sprintf(i18n.Msg("NetBrowse.RefreshFailed"), err), 3e9)
		return
	}
	p.selectRemote(left)
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

func (p *netPanel) selectRemote(name string) {
	for pos := 0; pos < p.table.ItemCount; pos++ {
		idx := p.table.RowAt(pos)
		if idx < 0 || idx >= len(p.table.Rows) {
			continue
		}
		if row, ok := p.table.Rows[idx].(netRow); ok && row.res != nil && strings.EqualFold(row.res.Remote, name) {
			p.table.SelectPos = pos
			p.table.EnsureVisible()
			return
		}
	}
}

// refresh is F5: list the current container again, keeping the cursor.
func (p *netPanel) refresh() {
	keep := p.GetSelectedName()
	if err := p.load(); err != nil {
		toast.Show(fmt.Sprintf(i18n.Msg("NetBrowse.RefreshFailed"), err), 3e9)
		return
	}
	if keep != "" {
		p.selectRemote(keep)
	}
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

func (p *netPanel) SetPosition(x1, y1, x2, y2 int) {
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

func (p *netPanel) GetPosition() (int, int, int, int) { return p.frame.GetPosition() }

func (p *netPanel) SetFocus(focused bool) {
	if focused {
		p.table.ColorSelectedTextIdx = theme.ColPanelCursor
		p.table.ColorItemSelectCursorIdx = theme.ColPanelSelectedCursor
	} else {
		p.table.ColorSelectedTextIdx = theme.ColPanelInactiveCursor
		p.table.ColorItemSelectCursorIdx = theme.ColPanelInactiveSelectedCursor
	}
	p.table.SetFocus(focused)
}

func (p *netPanel) IsFocused() bool { return p.table.IsFocused() }

var _ vfs.PanelKeyProvider = (*netPanel)(nil)

// PanelKeys declares F5 (reload) and Enter (open: enter a container, go up on
// "..", name a share); Enter is declared so that it runs ahead of the file
// panel's own Enter.
func (p *netPanel) PanelKeys() []vfs.PanelKey {
	return []vfs.PanelKey{
		vfs.PanelHelpKey(i18n.Msg("KeyBar.F1"), func() string { return i18n.Msg("NetBrowse.HelpTitle") }, func() string { return i18n.Msg("NetBrowse.Help") }),
		{VK: vtinput.VK_F5, Label: i18n.Msg("NetBrowse.KeyBar.Refresh"), Run: p.refresh},
		{VK: vtinput.VK_RETURN, Run: p.open},
	}
}

func (p *netPanel) ProcessKey(e *vtinput.InputEvent) bool {
	if vfs.DispatchPanelKey(p.PanelKeys(), e) {
		return true
	}
	return p.table.ProcessKey(e)
}

func (p *netPanel) ProcessMouse(e *vtinput.InputEvent) bool { return p.table.ProcessMouse(e) }

func (p *netPanel) SetContext(vfs.PanelContext) {}

func (p *netPanel) Show(scr *vtui.ScreenBuf) {
	title := i18n.Msg("NetBrowse.PanelTitle")
	if cur := p.current(); cur != nil {
		title = fmt.Sprintf(i18n.Msg("NetBrowse.PanelTitleAt"), cur.Remote)
	}
	p.frame.SetTitle(title)
	p.frame.Show(scr)
	p.table.Show(scr)
}

func (p *netPanel) Close() error { return nil }
