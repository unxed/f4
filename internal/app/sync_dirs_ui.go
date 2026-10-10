package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/numeric"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// "Synchronize dirs", the way Total Commander offers it: compare the two
// panel folders, show every pair with the direction it would be copied in,
// let the user overrule any of them, and only then move anything.
//
// The comparison and the execution are in fileops/sync.go. What lives here
// is the option dialog, the result window and the confirmation.

// syncOptionsDialogWidth is wide enough for two panel paths without being
// wider than an 80 column terminal.
const syncOptionsDialogWidth = 70

// syncMaskWidth is the mask field, which holds a far2l-style mask with an
// exclude section after "|".
const syncMaskWidth = 24

// syncProgressInterval is how often the comparison progress is repainted.
// Scanning walks thousands of names a second and repainting for each of
// them costs more than the comparison.
const syncProgressInterval = 50 * time.Millisecond

// panelCanSyncDirs reports whether there are two file panels to
// synchronize. Anything else on the other side has no folder of its own.
func panelCanSyncDirs() bool {
	pf := panel.FindPanelsFrameAnyScreen()
	if pf == nil {
		return false
	}
	return pf.GetActivePanel() != nil && pf.GetInactivePanel() != nil
}

// ShowSyncDirsDialog asks what to compare and how, then runs the
// comparison that fills the synchronize window.
func ShowSyncDirsDialog(pf *panel.PanelsFrame) {
	if pf == nil {
		return
	}
	active, passive := pf.GetActivePanel(), pf.GetInactivePanel()
	if active == nil || passive == nil || active.Vfs == nil || passive.Vfs == nil {
		vtui.ShowMessage(i18n.Msg("Sync.Title"), i18n.Msg("Sync.NoPanels"), []string{"&Ok"})
		return
	}
	opts := config.App.Sync.Normalize()

	const height = 15
	dlg := vtui.NewCenteredDialog(syncOptionsDialogWidth, height, i18n.Msg("Sync.Title"))
	dlg.ShowClose = true
	inner := syncOptionsDialogWidth - 6

	pathW := inner - vtui.StringWidth(i18n.Msg("Sync.Right")) - 1
	lblLeft := vtui.NewText(0, 0, dialog.PadLabelTo(syncPathLine(i18n.Msg("Sync.Left"), active.Vfs.GetPath(), pathW), inner), 0)
	lblRight := vtui.NewText(0, 0, dialog.PadLabelTo(syncPathLine(i18n.Msg("Sync.Right"), passive.Vfs.GetPath(), pathW), inner), 0)

	lblMask := vtui.NewLabel(0, 0, i18n.Msg("Sync.Mask"), nil)
	edMask := vtui.NewEdit(0, 0, syncMaskWidth, opts.Mask)

	cbAsym := vtui.NewCheckbox(0, 0, i18n.Msg("Sync.Asymmetric"), false)
	cbAsym.State = compareCheckState(opts.Asymmetric)
	cbSubdirs := vtui.NewCheckbox(0, 0, i18n.Msg("Sync.Subdirs"), false)
	cbSubdirs.State = compareCheckState(opts.Subdirs)
	cbContent := vtui.NewCheckbox(0, 0, i18n.Msg("Sync.ByContent"), false)
	cbContent.State = compareCheckState(opts.ByContent)
	cbIgnoreDate := vtui.NewCheckbox(0, 0, i18n.Msg("Sync.IgnoreDate"), false)
	cbIgnoreDate.State = compareCheckState(opts.IgnoreDate)

	btnOk := vtui.NewButton(0, 0, i18n.Msg("Sync.BtnCompare"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	for _, item := range []vtui.UIElement{lblLeft, lblRight, lblMask, edMask, cbAsym, cbSubdirs, cbContent, cbIgnoreDate, btnOk, btnCancel} {
		dlg.AddItem(item)
	}

	vbox := vtui.NewVBoxLayout(dlg.X1+3, dlg.Y1+2, inner, height-4)
	vbox.Add(lblLeft, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(lblRight, vtui.Margins{}, vtui.AlignFill)
	maskRow := vtui.NewHBoxLayout(0, 0, inner, 1)
	maskRow.Spacing = 1
	maskRow.Add(lblMask, vtui.Margins{Top: 1}, vtui.AlignTop)
	maskRow.Add(edMask, vtui.Margins{Top: 1}, vtui.AlignTop)
	vbox.Add(maskRow, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(cbAsym, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(cbSubdirs, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(cbContent, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(cbIgnoreDate, vtui.Margins{}, vtui.AlignLeft)
	btnRow := vtui.NewHBoxLayout(0, 0, inner, 1)
	btnRow.HorizontalAlign = vtui.AlignCenter
	btnRow.Spacing = 2
	btnRow.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	btnRow.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(btnRow, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		next := config.SyncOptions{
			Asymmetric: cbAsym.State == 1,
			Subdirs:    cbSubdirs.State == 1,
			ByContent:  cbContent.State == 1,
			IgnoreDate: cbIgnoreDate.State == 1,
			Mask:       strings.TrimSpace(edMask.GetText()),
		}.Normalize()
		config.App.Sync = next
		if config.App.AutoSaveDialogSettings {
			config.SaveConfig()
		}
		dlg.Close()
		runSyncCompare(pf, next)
	}

	vtui.FrameManager.Push(dlg)
}

// syncPathLine is one of the two folder captions above the options.
func syncPathLine(caption, path string, width int) string {
	if width < 8 {
		width = 8
	}
	return caption + " " + vtui.TruncateMiddle(path, width)
}

// syncSnapshot is what one panel looked like when the comparison started.
// The scan runs off the UI thread, so the plan records the folder it was
// built for rather than asking the panel again when it runs.
type syncSnapshot struct {
	pnl   *panel.FileSystemPanel
	fs    vfs.VFS
	root  string
	epoch uint64
}

func captureSyncPanel(fsp *panel.FileSystemPanel) (syncSnapshot, bool) {
	if fsp == nil || fsp.Vfs == nil {
		return syncSnapshot{}, false
	}
	return syncSnapshot{pnl: fsp, fs: fsp.Vfs, root: fsp.Vfs.GetPath(), epoch: fsp.DirectoryEpoch}, true
}

// stillCurrent reports whether the panel is showing what it was showing
// when the comparison started.
func (s syncSnapshot) stillCurrent() bool {
	fsp := s.pnl
	return fsp != nil && fsp.Vfs != nil && fileops.SameVFSInstance(fsp.Vfs, s.fs) &&
		fsp.Vfs.GetPath() == s.root && fsp.DirectoryEpoch == s.epoch
}

// runSyncCompare compares the two panel folders and opens the synchronize
// window on the result.
func runSyncCompare(pf *panel.PanelsFrame, opts config.SyncOptions) {
	if pf == nil {
		return
	}
	active, passive := pf.GetActivePanel(), pf.GetInactivePanel()
	leftSnap, okLeft := captureSyncPanel(active)
	rightSnap, okRight := captureSyncPanel(passive)
	if !okLeft || !okRight {
		vtui.ShowMessage(i18n.Msg("Sync.Title"), i18n.Msg("Sync.NoPanels"), []string{"&Ok"})
		return
	}
	sides := fileops.SyncSides{
		LeftFS: leftSnap.fs, LeftRoot: leftSnap.root,
		RightFS: rightSnap.fs, RightRoot: rightSnap.root,
	}

	// The mask is a panel-layer notion, so fileops is handed the decision
	// rather than the syntax.
	mask := opts.Mask
	keep := func(name string) bool { return panel.MatchMask(name, mask, true) }

	opDlg := fileops.NewFileOpProgressDialog(i18n.Msg("Sync.ComparingTitle"))
	var taskCtx *vtui.TaskContext
	opDlg.SetOnCancel(func() {
		if taskCtx != nil {
			taskCtx.Cancel()
		}
		opDlg.Close()
	})
	vtui.FrameManager.PostTask(func() {
		vtui.FrameManager.AddScreenHeadless(opDlg)
	})

	taskCtx = vtui.RunAsync(func(ctx *vtui.TaskContext) {
		lastUpdate := time.Now()
		show := func(action, path string, done, total int) {
			now := time.Now()
			if now.Sub(lastUpdate) < syncProgressInterval {
				return
			}
			lastUpdate = now
			ctx.RunOnUI(func() {
				opDlg.UpdateCounting(action, path, int64(done), int64(total))
				vtui.FrameManager.Redraw()
			})
		}
		scanning := i18n.Msg("Compare.Scanning")
		comparing := i18n.Msg("Compare.Comparing")

		pairs, err := fileops.BuildSyncPairs(ctx.Context, sides, opts, keep,
			func(path string) { show(scanning, path, 0, 0) },
			func(path string, done, total int) { show(comparing, path, done, total) })

		ctx.RunOnUI(func() {
			opDlg.Close()
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			if err != nil && pairs == nil {
				vtui.ShowMessage(i18n.Msg("Sync.Title"), fmt.Sprintf(i18n.Msg("Compare.Failed"), err.Error()), []string{"&Ok"})
				return
			}
			if !leftSnap.stillCurrent() || !rightSnap.stillCurrent() {
				vtui.ShowMessage(i18n.Msg("Sync.Title"), i18n.Msg("Compare.Moved"), []string{"&Ok"})
				return
			}
			if err != nil {
				// Files that could not be read count as differing; the
				// list is still worth showing, with a word about it.
				vtui.ShowMessage(i18n.Msg("Sync.Title"),
					fmt.Sprintf(i18n.Msg("Compare.ReadFailed"), err.Error()), []string{"&Ok"})
			}
			if len(pairs) == 0 {
				vtui.ShowMessage(i18n.Msg("Sync.Title"), i18n.Msg("Sync.NothingToCompare"), []string{"&Ok"})
				return
			}
			ShowSyncResults(pf, sides, opts, pairs)
		})
	})
}

// The four groups the window can filter by. They follow the comparison
// result, not the current action, so that hiding the equal files does not
// make a row vanish the moment its direction is cleared.
const (
	syncGroupToRight = iota
	syncGroupEqual
	syncGroupDiffers
	syncGroupToLeft
	syncGroupCount
)

func syncGroupOf(p fileops.SyncPair) int {
	if p.Locked {
		return syncGroupDiffers
	}
	switch p.State {
	case fileops.SyncLeftOnly, fileops.SyncLeftNewer:
		return syncGroupToRight
	case fileops.SyncRightOnly, fileops.SyncRightNewer:
		return syncGroupToLeft
	case fileops.SyncEqual:
		return syncGroupEqual
	default:
		return syncGroupDiffers
	}
}

// The tokens of the direction column. They are symbols rather than words
// on purpose: the column is four cells wide and has to read the same in
// every language.
const (
	syncTokenToRight     = "-->"
	syncTokenToLeft      = "<--"
	syncTokenDeleteRight = "--\u00d7"
	syncTokenDeleteLeft  = "\u00d7--"
	syncTokenEqual       = "="
	syncTokenDiffers     = "!="
	syncTokenSkip        = "-"
)

// syncActionToken is what the direction column shows for one row: the
// action if there is one, and otherwise what the comparison found.
func syncActionToken(p fileops.SyncPair) string {
	switch p.Action {
	case fileops.SyncCopyToRight:
		return syncTokenToRight
	case fileops.SyncCopyToLeft:
		return syncTokenToLeft
	case fileops.SyncDeleteRight:
		return syncTokenDeleteRight
	case fileops.SyncDeleteLeft:
		return syncTokenDeleteLeft
	}
	switch {
	case p.Locked, p.State == fileops.SyncDiffers:
		return syncTokenDiffers
	case p.State == fileops.SyncEqual:
		return syncTokenEqual
	}
	return syncTokenSkip
}

// syncRow is one table row, pointing at the pair it shows so that a
// direction the user changes is visible without rebuilding the table.
type syncRow struct {
	pair *fileops.SyncPair
}

func (r syncRow) GetCellText(col int) string {
	p := r.pair
	switch col {
	case 0:
		return p.Rel
	case 1:
		return syncSizeCell(p.HasLeft, p.Left)
	case 2:
		return syncDateCell(p.HasLeft, p.Left)
	case 3:
		return syncActionToken(*p)
	case 4:
		return syncDateCell(p.HasRight, p.Right)
	case 5:
		return syncSizeCell(p.HasRight, p.Right)
	}
	return ""
}

func syncSizeCell(has bool, item vfs.VFSItem) string {
	switch {
	case !has:
		return ""
	case item.IsDir:
		return i18n.Msg("Sync.Dir")
	}
	return fileops.FormatIntWithSpaces(item.Size)
}

func syncDateCell(has bool, item vfs.VFSItem) string {
	if !has || item.MTime.IsZero() {
		return ""
	}
	return item.MTime.Format("02.01.06 15:04")
}

// SyncResultsWindow is the synchronize window: every compared pair, the
// direction each of them will be copied in, and the totals that says what
// pressing Synchronize would do.
type SyncResultsWindow struct {
	*vtui.Window
	pf    *panel.PanelsFrame
	sides fileops.SyncSides
	opts  config.SyncOptions

	table *vtui.Table
	// pairs is the whole comparison; view maps a table row back to it.
	// The slice is never appended to after the window opens, so the row
	// pointers into it stay valid.
	pairs []fileops.SyncPair
	view  []int

	filters  [syncGroupCount]*vtui.Checkbox
	lblStats *vtui.Text
	statsW   int
}

// ShowSyncResults opens the window on a finished comparison.
func ShowSyncResults(pf *panel.PanelsFrame, sides fileops.SyncSides, opts config.SyncOptions, pairs []fileops.SyncPair) {
	scrW := vtui.FrameManager.GetScreenSize()
	scrH := vtui.FrameManager.GetScreenHeight()
	dlgW := scrW - 4
	if dlgW > 110 {
		dlgW = 110
	}
	if dlgW < 60 {
		dlgW = 60
	}
	dlgH := scrH - 2
	if dlgH > 30 {
		dlgH = 30
	}
	if dlgH < 14 {
		dlgH = 14
	}

	base := vtui.NewCenteredDialog(dlgW, dlgH, i18n.Msg("Sync.ResultsTitle"))
	base.ShowClose = true
	w := &SyncResultsWindow{Window: base, pf: pf, sides: sides, opts: opts, pairs: pairs}

	inner := dlgW - 4
	cols := []vtui.TableColumn{
		{Title: i18n.Msg("Sync.ColName"), MinWidth: 16},
		{Title: i18n.Msg("Sync.ColSize"), Width: 11, Alignment: vtui.AlignRight},
		{Title: i18n.Msg("Sync.ColDate"), Width: 14},
		{Title: "", Width: 4, Alignment: vtui.AlignCenter},
		{Title: i18n.Msg("Sync.ColDate"), Width: 14},
		{Title: i18n.Msg("Sync.ColSize"), Width: 11, Alignment: vtui.AlignRight},
	}
	// Four rows go below the table: the filters, the totals, a blank one
	// and the buttons.
	w.table = vtui.NewTable(0, 0, inner, dlgH-8, cols)
	w.table.SetOwner(w)
	w.table.ShowHeader = true
	w.table.ShowScrollBar = true

	counts := syncGroupCounts(pairs)
	for group := 0; group < syncGroupCount; group++ {
		cb := vtui.NewCheckbox(0, 0, fmt.Sprintf("%s %d", syncGroupToken(group), counts[group]), false)
		cb.State = 1
		cb.SetOwner(w)
		cb.OnChange = func(int) { w.rebuild() }
		w.filters[group] = cb
	}

	w.statsW = inner
	w.lblStats = vtui.NewText(0, 0, dialog.PadLabelTo(syncTotalsText(fileops.SyncPlanTotals(pairs)), inner), 0)

	btnSync := vtui.NewButton(0, 0, i18n.Msg("Sync.BtnSynchronize"))
	btnSync.IsDefault = true
	btnSync.SetOwner(w)
	btnSync.OnClick = func() { w.confirmAndRun() }
	btnClose := vtui.NewButton(0, 0, i18n.Msg("Sync.BtnClose"))
	btnClose.SetOwner(w)
	btnClose.OnClick = func() { w.Close() }

	vbox := vtui.NewVBoxLayout(w.X1+2, w.Y1+1, inner, dlgH-2)
	vbox.Add(w.table, vtui.Margins{}, vtui.AlignFill)
	filterRow := vtui.NewHBoxLayout(0, 0, inner, 1)
	filterRow.Spacing = 3
	filterRow.HorizontalAlign = vtui.AlignCenter
	for _, cb := range w.filters {
		filterRow.Add(cb, vtui.Margins{}, vtui.AlignTop)
	}
	vbox.Add(filterRow, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(w.lblStats, vtui.Margins{}, vtui.AlignFill)
	btnRow := vtui.NewHBoxLayout(0, 0, inner, 1)
	btnRow.HorizontalAlign = vtui.AlignCenter
	btnRow.Spacing = 2
	btnRow.Add(btnSync, vtui.Margins{}, vtui.AlignTop)
	btnRow.Add(btnClose, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(btnRow, vtui.Margins{}, vtui.AlignFill)
	vbox.Apply()

	w.AddItem(w.table)
	for _, cb := range w.filters {
		w.AddItem(cb)
	}
	w.AddItem(w.lblStats)
	w.AddItem(btnSync)
	w.AddItem(btnClose)

	w.rebuild()
	vtui.FrameManager.Push(w)
}

func syncGroupToken(group int) string {
	switch group {
	case syncGroupToRight:
		return syncTokenToRight
	case syncGroupEqual:
		return syncTokenEqual
	case syncGroupDiffers:
		return syncTokenDiffers
	default:
		return syncTokenToLeft
	}
}

func syncGroupCounts(pairs []fileops.SyncPair) [syncGroupCount]int {
	var counts [syncGroupCount]int
	for _, p := range pairs {
		counts[syncGroupOf(p)]++
	}
	return counts
}

// syncTotalsText is the line under the table: what pressing Synchronize
// would copy and delete.
func syncTotalsText(t fileops.SyncTotals) string {
	return fmt.Sprintf(i18n.Msg("Sync.Totals"),
		t.ToRight, numeric.FormatSize(t.BytesToRight),
		t.ToLeft, numeric.FormatSize(t.BytesToLeft),
		t.DeleteRight+t.DeleteLeft)
}

// rebuild refills the table from the filters and refreshes the totals.
func (w *SyncResultsWindow) rebuild() {
	keepPos := w.table.SelectPos
	w.view = w.view[:0]
	rows := make([]vtui.TableRow, 0, len(w.pairs))
	for i := range w.pairs {
		if w.filters[syncGroupOf(w.pairs[i])].State != 1 {
			continue
		}
		w.view = append(w.view, i)
		rows = append(rows, syncRow{pair: &w.pairs[i]})
	}
	w.table.SetRows(rows)
	if keepPos >= len(rows) {
		keepPos = len(rows) - 1
	}
	if keepPos < 0 {
		keepPos = 0
	}
	w.table.SetSelectPos(keepPos)
	w.refreshStats()
}

// refreshStats rewrites the totals line in place.
func (w *SyncResultsWindow) refreshStats() {
	w.lblStats.SetText(dialog.PadLabelTo(syncTotalsText(fileops.SyncPlanTotals(w.pairs)), w.statsW))
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

// current is the pair under the cursor, or nil.
func (w *SyncResultsWindow) current() *fileops.SyncPair {
	pos := w.table.SelectPos
	if pos < 0 || pos >= len(w.view) {
		return nil
	}
	return &w.pairs[w.view[pos]]
}

// setAction gives the row under the cursor an action, if that action is
// one the row can have at all.
func (w *SyncResultsWindow) setAction(action fileops.SyncAction) bool {
	p := w.current()
	if p == nil {
		return true
	}
	for _, allowed := range p.AllowedActions() {
		if allowed == action {
			p.Action = action
			w.refreshStats()
			return true
		}
	}
	return true
}

// cycleAction steps the row under the cursor through the actions it can
// have, which is how a row gets a direction its comparison did not give it.
func (w *SyncResultsWindow) cycleAction() bool {
	p := w.current()
	if p == nil {
		return true
	}
	allowed := p.AllowedActions()
	next := 0
	for i, a := range allowed {
		if a == p.Action {
			next = (i + 1) % len(allowed)
			break
		}
	}
	p.Action = allowed[next]
	w.refreshStats()
	return true
}

func (w *SyncResultsWindow) ProcessKey(e *vtinput.InputEvent) bool {
	if !e.KeyDown {
		return w.Window.ProcessKey(e)
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_SPACE:
		return w.cycleAction()
	case vtinput.VK_RIGHT:
		return w.setAction(fileops.SyncCopyToRight)
	case vtinput.VK_LEFT:
		return w.setAction(fileops.SyncCopyToLeft)
	case vtinput.VK_DELETE:
		p := w.current()
		if p == nil {
			return true
		}
		if p.Action == fileops.SyncDeleteRight {
			return w.setAction(fileops.SyncDeleteLeft)
		}
		if p.HasRight {
			return w.setAction(fileops.SyncDeleteRight)
		}
		return w.setAction(fileops.SyncDeleteLeft)
	case vtinput.VK_BACK:
		return w.setAction(fileops.SyncSkip)
	case vtinput.VK_F3:
		return w.viewCurrent()
	}
	return w.Window.ProcessKey(e)
}

// viewCurrent opens the file under the cursor in the viewer, preferring
// the side that has it.
func (w *SyncResultsWindow) viewCurrent() bool {
	p := w.current()
	if p == nil || p.Locked {
		return true
	}
	if p.HasLeft {
		actionOpenViewer(w.pf, w.sides.LeftFS, syncSidePath(w.sides.LeftFS, w.sides.LeftRoot, p.Rel))
		return true
	}
	if p.HasRight {
		actionOpenViewer(w.pf, w.sides.RightFS, syncSidePath(w.sides.RightFS, w.sides.RightRoot, p.Rel))
	}
	return true
}

// syncSidePath turns a relative path from the plan into a path of one
// side's file system.
func syncSidePath(v vfs.VFS, root, rel string) string {
	full := root
	for _, part := range strings.Split(rel, "/") {
		if part != "" {
			full = v.Join(full, part)
		}
	}
	return full
}

func (w *SyncResultsWindow) GetKeyLabels() *vtui.KeySet {
	return &vtui.KeySet{
		Normal: vtui.KeyBarLabels{
			"", "", i18n.Msg("Sync.KeyView"), "", "", "", "", "", "", i18n.Msg("Sync.KeyQuit"), "", "",
		},
	}
}

// confirmAndRun shows what the plan adds up to and, once confirmed, runs
// it. Total Commander asks the same question with the same three switches,
// so that a plan built one way can still be applied one way only.
func (w *SyncResultsWindow) confirmAndRun() {
	totals := fileops.SyncPlanTotals(w.pairs)
	if totals.Empty() {
		vtui.ShowMessage(i18n.Msg("Sync.Title"), i18n.Msg("Sync.NothingToDo"), []string{"&Ok"})
		return
	}

	const width, height = 62, 15
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("Sync.ConfirmTitle"))
	dlg.ShowClose = true
	inner := width - 6

	lines := []string{
		fmt.Sprintf(i18n.Msg("Sync.ConfirmToRight"), totals.ToRight, numeric.FormatSize(totals.BytesToRight)),
		fmt.Sprintf(i18n.Msg("Sync.ConfirmToLeft"), totals.ToLeft, numeric.FormatSize(totals.BytesToLeft)),
		fmt.Sprintf(i18n.Msg("Sync.ConfirmDelete"), totals.DeleteRight+totals.DeleteLeft),
	}
	texts := make([]*vtui.Text, len(lines))
	for i, line := range lines {
		texts[i] = vtui.NewText(0, 0, dialog.PadLabelTo(line, inner), 0)
	}

	cbRight := vtui.NewCheckbox(0, 0, i18n.Msg("Sync.DoToRight"), false)
	cbRight.State = compareCheckState(totals.ToRight > 0)
	cbLeft := vtui.NewCheckbox(0, 0, i18n.Msg("Sync.DoToLeft"), false)
	cbLeft.State = compareCheckState(totals.ToLeft > 0)
	cbDelete := vtui.NewCheckbox(0, 0, i18n.Msg("Sync.DoDelete"), false)
	cbDelete.State = compareCheckState(totals.DeleteRight+totals.DeleteLeft > 0)

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	items := []vtui.UIElement{cbRight, cbLeft, cbDelete, btnOk, btnCancel}
	for _, t := range texts {
		dlg.AddItem(t)
	}
	for _, item := range items {
		dlg.AddItem(item)
	}

	vbox := vtui.NewVBoxLayout(dlg.X1+3, dlg.Y1+2, inner, height-4)
	for _, t := range texts {
		vbox.Add(t, vtui.Margins{}, vtui.AlignFill)
	}
	vbox.Add(cbRight, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(cbLeft, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(cbDelete, vtui.Margins{}, vtui.AlignLeft)
	btnRow := vtui.NewHBoxLayout(0, 0, inner, 1)
	btnRow.HorizontalAlign = vtui.AlignCenter
	btnRow.Spacing = 2
	btnRow.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	btnRow.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(btnRow, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		plan := syncSelectedPlan(w.pairs, cbRight.State == 1, cbLeft.State == 1, cbDelete.State == 1)
		dlg.Close()
		if len(plan) == 0 {
			vtui.ShowMessage(i18n.Msg("Sync.Title"), i18n.Msg("Sync.NothingToDo"), []string{"&Ok"})
			return
		}
		w.Close()
		disposition := vfs.DeletePermanently
		if config.App.UseTrash {
			disposition = vfs.DeleteToTrash
		}
		pf := w.pf
		fileops.ExecuteSyncPlan(w.sides, plan, disposition, func() {
			if pf == nil {
				return
			}
			for _, fsp := range []*panel.FileSystemPanel{pf.GetActivePanel(), pf.GetInactivePanel()} {
				if fsp != nil {
					fsp.ReadDirectory()
				}
			}
			vtui.FrameManager.Redraw()
		})
	}

	vtui.FrameManager.Push(dlg)
}

// syncSelectedPlan is the plan restricted to the kinds of action the
// confirmation dialog left switched on.
func syncSelectedPlan(pairs []fileops.SyncPair, toRight, toLeft, deletions bool) []fileops.SyncPair {
	out := make([]fileops.SyncPair, 0, len(pairs))
	for _, p := range pairs {
		switch p.Action {
		case fileops.SyncCopyToRight:
			if !toRight {
				continue
			}
		case fileops.SyncCopyToLeft:
			if !toLeft {
				continue
			}
		case fileops.SyncDeleteRight, fileops.SyncDeleteLeft:
			if !deletions {
				continue
			}
		default:
			continue
		}
		out = append(out, p)
	}
	return out
}
