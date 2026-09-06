package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// The Advanced Compare dialog and everything it needs to reach the panels.
// The comparison itself is in compare_folders.go.

// compareDialogMinWidth keeps the dialog from shrinking around short
// captions into something that reads as a stray message box.
const compareDialogMinWidth = 54

// compareDialogMaxWidth is the widest we let a translation push the dialog.
// Beyond this a caption is too long for the dialog to be the right shape,
// and an 80 column terminal has to be considered too.
const compareDialogMaxWidth = 100

// compareProgressInterval is how often the progress dialog is refreshed.
// A local comparison walks thousands of names a second; repainting for
// each of them costs more than the comparison itself.
const compareProgressInterval = 50 * time.Millisecond

// panelCanCompareFolders reports whether there are two file panels to
// compare. Anything else on the other side (a terminal, the info panel)
// has no folder of its own, so the menu entry stays out of sight rather
// than being offered and refused.
func panelCanCompareFolders() bool {
	pf := findPanelsFrameAnyScreen()
	if pf == nil {
		return false
	}
	return pf.getActivePanel() != nil && pf.getInactivePanel() != nil
}

// compareCaptionWidth is how many columns a checkbox with this caption
// paints, indent included: the "[x] " prefix is four columns wide.
func compareCaptionWidth(indent int, caption string) int {
	return indent + 4 + vtui.StringWidth(plainLabel(caption))
}

// compareRadioWidth is the same measurement for a radio group, whose
// widest row decides the width: four columns of prefix and two of padding.
func compareRadioWidth(indent int, items []string) int {
	widest := 0
	for _, item := range items {
		if w := vtui.StringWidth(plainLabel(item)); w > widest {
			widest = w
		}
	}
	return indent + 6 + widest
}

// ShowCompareFoldersDialog asks what to compare and how, then runs the
// comparison over the two panels.
func ShowCompareFoldersDialog(pf *PanelsFrame) {
	if pf == nil {
		return
	}
	opts := AppConfig.Compare.normalize()

	const (
		subIndent   = 4
		deepIndent  = 8
		depthWidth  = 5
		groupInset  = 4 // group border and clearance on both sides
		dialogInset = 6 // dialog border and margin on both sides
	)

	capRecursive := Msg("Compare.Recursive")
	capDepth := Msg("Compare.MaxDepth")
	capMarked := Msg("Compare.MarkedOnly")
	capTime := Msg("Compare.ByTime")
	capSlack := Msg("Compare.TimeSlack")
	capZones := Msg("Compare.IgnoreZones")
	capSize := Msg("Compare.BySize")
	capContent := Msg("Compare.ByContent")
	capIgnore := Msg("Compare.Ignore")
	capReport := Msg("Compare.ReportEqual")
	ignoreItems := []string{Msg("Compare.IgnoreEOL"), Msg("Compare.IgnoreSpaces")}

	inGroup := 0
	for _, w := range []int{
		compareCaptionWidth(0, capRecursive),
		compareCaptionWidth(subIndent, capDepth) + 1 + depthWidth,
		compareCaptionWidth(0, capMarked),
		compareCaptionWidth(0, capTime),
		compareCaptionWidth(subIndent, capSlack),
		compareCaptionWidth(subIndent, capZones),
		compareCaptionWidth(0, capSize),
		compareCaptionWidth(0, capContent),
		compareCaptionWidth(subIndent, capIgnore),
		compareRadioWidth(deepIndent, ignoreItems),
	} {
		if w > inGroup {
			inGroup = w
		}
	}

	width := inGroup + groupInset + dialogInset
	if w := compareCaptionWidth(0, capReport) + dialogInset; w > width {
		width = w
	}
	if width < compareDialogMinWidth {
		width = compareDialogMinWidth
	}
	if width > compareDialogMaxWidth {
		width = compareDialogMaxWidth
	}
	// Three rows of "process", eight of "compare", both framed, plus the
	// message checkbox, a blank row and the buttons.
	const processRows, compareRows = 3, 8
	height := (processRows + 2) + (compareRows + 2) + 1 + 1 + 1 + 4

	dlg := vtui.NewCenteredDialog(width, height, Msg("Compare.Title"))
	dlg.ShowClose = true

	// NewGroupBox takes corners, not a size: the bottom row of a box
	// holding n rows is n+1 below its top one.
	gbProcess := vtui.NewGroupBox(0, 0, width-dialogInset, processRows+1, Msg("Compare.Process"))
	gbCompare := vtui.NewGroupBox(0, 0, width-dialogInset, compareRows+1, Msg("Compare.Criteria"))
	cbReport := vtui.NewCheckbox(0, 0, capReport, false)
	cbReport.State = compareCheckState(opts.ReportEqual)
	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	for _, item := range []vtui.UIElement{gbProcess, gbCompare, cbReport, btnOk, btnCancel} {
		dlg.AddItem(item)
	}

	mainVBox := vtui.NewVBoxLayout(dlg.X1+3, dlg.Y1+2, width-dialogInset, height-4)
	mainVBox.Add(gbProcess, vtui.Margins{}, vtui.AlignFill)
	mainVBox.Add(gbCompare, vtui.Margins{}, vtui.AlignFill)
	mainVBox.Add(cbReport, vtui.Margins{}, vtui.AlignLeft)
	btnRow := vtui.NewHBoxLayout(0, 0, width-dialogInset, 1)
	btnRow.HorizontalAlign = vtui.AlignCenter
	btnRow.Spacing = 2
	btnRow.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	btnRow.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	mainVBox.Add(btnRow, vtui.Margins{Top: 1}, vtui.AlignFill)
	// First pass: the group boxes now know where they are, which is what
	// their contents have to be positioned against.
	mainVBox.Apply()

	// "Process" group.
	cbRecursive := vtui.NewCheckbox(0, 0, capRecursive, false)
	cbRecursive.State = compareCheckState(opts.Recursive)
	cbDepth := vtui.NewCheckbox(0, 0, capDepth, false)
	cbDepth.State = compareCheckState(opts.LimitDepth)
	edDepth := vtui.NewEdit(0, 0, depthWidth, strconv.Itoa(opts.MaxDepth))
	edDepth.Validator = &vtui.IntRangeValidator{Min: 1, Max: compareMaxDepthLimit, Title: Msg("Compare.Title")}
	cbMarked := vtui.NewCheckbox(0, 0, capMarked, false)
	cbMarked.State = compareCheckState(opts.MarkedOnly)

	processBox := vtui.NewVBoxLayout(gbProcess.X1+2, gbProcess.Y1+1, gbProcess.X2-gbProcess.X1-3, processRows)
	processBox.Add(cbRecursive, vtui.Margins{}, vtui.AlignLeft)
	depthRow := vtui.NewHBoxLayout(0, 0, gbProcess.X2-gbProcess.X1-3, 1)
	depthRow.Add(cbDepth, vtui.Margins{Left: subIndent}, vtui.AlignTop)
	depthRow.Add(edDepth, vtui.Margins{}, vtui.AlignTop)
	processBox.Add(depthRow, vtui.Margins{}, vtui.AlignFill)
	processBox.Add(cbMarked, vtui.Margins{}, vtui.AlignLeft)
	for _, item := range []vtui.UIElement{cbRecursive, cbDepth, edDepth, cbMarked} {
		gbProcess.AddItem(item)
	}
	processBox.Apply()
	gbProcess.SetFocus(false)

	// "Compare" group.
	cbTime := vtui.NewCheckbox(0, 0, capTime, false)
	cbTime.State = compareCheckState(opts.ByTime)
	cbSlack := vtui.NewCheckbox(0, 0, capSlack, false)
	cbSlack.State = compareCheckState(opts.TimeSlack)
	cbZones := vtui.NewCheckbox(0, 0, capZones, false)
	cbZones.State = compareCheckState(opts.IgnoreZones)
	cbSize := vtui.NewCheckbox(0, 0, capSize, false)
	cbSize.State = compareCheckState(opts.BySize)
	cbContent := vtui.NewCheckbox(0, 0, capContent, false)
	cbContent.State = compareCheckState(opts.ByContent)
	cbIgnore := vtui.NewCheckbox(0, 0, capIgnore, false)
	cbIgnore.State = compareCheckState(opts.Ignore)
	rgIgnore := vtui.NewRadioGroup(0, 0, 1, ignoreItems)
	if opts.IgnoreMode == compareIgnoreSpaces {
		rgIgnore.Selected = 1
	}

	compareBox := vtui.NewVBoxLayout(gbCompare.X1+2, gbCompare.Y1+1, gbCompare.X2-gbCompare.X1-3, compareRows)
	compareBox.Add(cbTime, vtui.Margins{}, vtui.AlignLeft)
	compareBox.Add(cbSlack, vtui.Margins{Left: subIndent}, vtui.AlignLeft)
	compareBox.Add(cbZones, vtui.Margins{Left: subIndent}, vtui.AlignLeft)
	compareBox.Add(cbSize, vtui.Margins{}, vtui.AlignLeft)
	compareBox.Add(cbContent, vtui.Margins{}, vtui.AlignLeft)
	compareBox.Add(cbIgnore, vtui.Margins{Left: subIndent}, vtui.AlignLeft)
	compareBox.Add(rgIgnore, vtui.Margins{Left: deepIndent}, vtui.AlignLeft)
	for _, item := range []vtui.UIElement{cbTime, cbSlack, cbZones, cbSize, cbContent, cbIgnore, rgIgnore} {
		gbCompare.AddItem(item)
	}
	compareBox.Apply()
	gbCompare.SetFocus(false)

	// A sub-option of something switched off is not editable, the way Far
	// greys out the rows below an unchecked box.
	syncEnabled := func() {
		recursive := cbRecursive.State == 1
		cbDepth.SetDisabled(!recursive)
		edDepth.SetDisabled(!recursive || cbDepth.State != 1)
		cbSlack.SetDisabled(cbTime.State != 1)
		cbZones.SetDisabled(cbTime.State != 1)
		cbIgnore.SetDisabled(cbContent.State != 1)
		rgIgnore.SetDisabled(cbContent.State != 1 || cbIgnore.State != 1)
		if vtui.FrameManager != nil {
			vtui.FrameManager.Redraw()
		}
	}
	for _, cb := range []*vtui.Checkbox{cbRecursive, cbDepth, cbTime, cbContent, cbIgnore} {
		cb.OnChange = func(int) { syncEnabled() }
	}
	syncEnabled()

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		next := compareOptions{
			Recursive:   cbRecursive.State == 1,
			LimitDepth:  cbDepth.State == 1,
			MaxDepth:    opts.MaxDepth,
			MarkedOnly:  cbMarked.State == 1,
			ByTime:      cbTime.State == 1,
			TimeSlack:   cbSlack.State == 1,
			IgnoreZones: cbZones.State == 1,
			BySize:      cbSize.State == 1,
			ByContent:   cbContent.State == 1,
			Ignore:      cbIgnore.State == 1,
			IgnoreMode:  rgIgnore.Selected,
			ReportEqual: cbReport.State == 1,
		}
		if depth, err := strconv.Atoi(strings.TrimSpace(edDepth.GetText())); err == nil {
			next.MaxDepth = depth
		}
		if !next.hasCriteria() {
			// Comparing by name alone would call two folders equal
			// whenever they hold the same names, which is not an answer
			// anybody asked for.
			vtui.ShowMessage(Msg("Compare.Title"), Msg("Compare.NoCriteria"), []string{"&Ok"})
			return
		}
		next = next.normalize()
		AppConfig.Compare = next
		if AppConfig.AutoSaveDialogSettings {
			SaveConfig()
		}
		dlg.Close()
		runCompareFolders(pf, next)
	}

	vtui.FrameManager.Push(dlg)
}

func compareCheckState(on bool) int {
	if on {
		return 1
	}
	return 0
}

// comparePanelSnapshot is what a panel looked like when the comparison
// started. The scan runs off the UI thread and may take a while, so the
// marks are only applied if the panel is still showing the same listing.
type comparePanelSnapshot struct {
	panel *FileSystemPanel
	fs    vfs.VFS
	root  string
	allow map[string]bool
	epoch uint64
}

func captureComparePanel(fsp *FileSystemPanel, opts compareOptions) (comparePanelSnapshot, bool) {
	if fsp == nil || fsp.vfs == nil {
		return comparePanelSnapshot{}, false
	}
	snap := comparePanelSnapshot{
		panel: fsp,
		fs:    fsp.vfs,
		root:  fsp.vfs.GetPath(),
		epoch: fsp.directoryEpoch,
	}
	if opts.MarkedOnly {
		marked := fsp.GetMarkedNames()
		if len(marked) == 0 {
			// Far compares everything when nothing is marked, rather
			// than comparing nothing at all.
			return snap, true
		}
		snap.allow = make(map[string]bool, len(marked))
		for _, name := range marked {
			snap.allow[name] = true
		}
	}
	return snap, true
}

// stillCurrent reports whether the panel is showing what it was showing
// when the comparison started.
func (s comparePanelSnapshot) stillCurrent() bool {
	fsp := s.panel
	return fsp != nil && fsp.vfs != nil && sameVFSInstance(fsp.vfs, s.fs) &&
		fsp.vfs.GetPath() == s.root && fsp.directoryEpoch == s.epoch
}

// applyCompareMarks replaces the panel's selection with the comparison
// result. The previous selection is kept as the restorable one, so Ctrl+M
// undoes a comparison the same way it undoes any other mass selection.
func (s comparePanelSnapshot) applyCompareMarks(marks map[string]bool) {
	fsp := s.panel
	if fsp == nil {
		return
	}
	fsp.SaveSelection()
	fsp.setAllItemsSelected(false)
	for name := range marks {
		fsp.SetSelectedByName(name, true)
	}
}

// runCompareFolders compares the two panels and marks what differs.
func runCompareFolders(pf *PanelsFrame, opts compareOptions) {
	if pf == nil {
		return
	}
	active, passive := pf.getActivePanel(), pf.getInactivePanel()
	if active == nil || passive == nil {
		vtui.ShowMessage(Msg("Compare.Title"), Msg("Compare.NoPanels"), []string{"&Ok"})
		return
	}
	leftSnap, okLeft := captureComparePanel(active, opts)
	rightSnap, okRight := captureComparePanel(passive, opts)
	if !okLeft || !okRight {
		vtui.ShowMessage(Msg("Compare.Title"), Msg("Compare.NoPanels"), []string{"&Ok"})
		return
	}

	opDlg := NewFileOpProgressDialog(Msg("Compare.Progress"))
	var taskCtx *vtui.TaskContext
	opDlg.btnCancel.OnClick = func() {
		if taskCtx != nil {
			taskCtx.Cancel()
		}
		opDlg.Close()
	}
	vtui.FrameManager.PostTask(func() {
		vtui.FrameManager.AddScreenHeadless(opDlg)
	})

	taskCtx = vtui.RunAsync(func(ctx *vtui.TaskContext) {
		lastUpdate := time.Now()
		show := func(action, path string, done, total int) {
			now := time.Now()
			if now.Sub(lastUpdate) < compareProgressInterval {
				return
			}
			lastUpdate = now
			ctx.RunOnUI(func() {
				opDlg.UpdateCounting(action, path, int64(done), int64(total))
				vtui.FrameManager.Redraw()
			})
		}

		scanning := Msg("Compare.Scanning")
		comparing := Msg("Compare.Comparing")
		leftItems, err := collectCompareSide(ctx.Context, leftSnap.fs, leftSnap.root, leftSnap.allow, opts,
			func(path string) { show(scanning, path, 0, 0) })
		var rightItems map[string]compareItem
		if err == nil {
			rightItems, err = collectCompareSide(ctx.Context, rightSnap.fs, rightSnap.root, rightSnap.allow, opts,
				func(path string) { show(scanning, path, 0, 0) })
		}
		var outcome *compareOutcome
		if err == nil {
			outcome, err = compareSides(ctx.Context, leftSnap.fs, rightSnap.fs, leftItems, rightItems, opts,
				func(path string, done, total int) { show(comparing, path, done, total) })
		}

		ctx.RunOnUI(func() {
			opDlg.Close()
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			if err != nil {
				vtui.ShowMessage(Msg("Compare.Title"), fmt.Sprintf(Msg("Compare.Failed"), err.Error()), []string{"&Ok"})
				return
			}
			if !leftSnap.stillCurrent() || !rightSnap.stillCurrent() {
				// Both panels moved on while the tree was being read;
				// marking them now would mark the wrong listing.
				vtui.ShowMessage(Msg("Compare.Title"), Msg("Compare.Moved"), []string{"&Ok"})
				return
			}
			leftSnap.applyCompareMarks(outcome.left)
			rightSnap.applyCompareMarks(outcome.right)
			vtui.FrameManager.Redraw()

			if outcome.readErr != nil {
				vtui.ShowMessage(Msg("Compare.Title"),
					fmt.Sprintf(Msg("Compare.ReadFailed"), outcome.readErr.Error()), []string{"&Ok"})
				return
			}
			if outcome.differing == 0 && opts.ReportEqual {
				vtui.ShowMessage(Msg("Compare.Title"), Msg("Compare.Equal"), []string{"&Ok"})
			}
		})
	})
}

// loadCompareOptions reads the [Compare] section, falling back to Far's
// built-in comparison for a profile that has never opened the dialog.
func loadCompareOptions(ini *IniFile) compareOptions {
	defaults := defaultCompareOptions()
	flag := func(key string, def bool) bool {
		fallback := "0"
		if def {
			fallback = "1"
		}
		return ini.GetString("Compare", key, fallback) == "1"
	}
	opts := compareOptions{
		Recursive:   flag("Recursive", defaults.Recursive),
		LimitDepth:  flag("LimitDepth", defaults.LimitDepth),
		MaxDepth:    defaults.MaxDepth,
		MarkedOnly:  flag("MarkedOnly", defaults.MarkedOnly),
		ByTime:      flag("ByTime", defaults.ByTime),
		TimeSlack:   flag("TimeSlack", defaults.TimeSlack),
		IgnoreZones: flag("IgnoreZones", defaults.IgnoreZones),
		BySize:      flag("BySize", defaults.BySize),
		ByContent:   flag("ByContent", defaults.ByContent),
		Ignore:      flag("Ignore", defaults.Ignore),
		IgnoreMode:  defaults.IgnoreMode,
		ReportEqual: flag("ReportEqual", defaults.ReportEqual),
	}
	if depth, err := strconv.Atoi(ini.GetString("Compare", "MaxDepth", strconv.Itoa(defaults.MaxDepth))); err == nil {
		opts.MaxDepth = depth
	}
	if mode, err := strconv.Atoi(ini.GetString("Compare", "IgnoreMode", strconv.Itoa(defaults.IgnoreMode))); err == nil {
		opts.IgnoreMode = mode
	}
	return opts.normalize()
}

// writeCompareOptions emits the [Compare] section body.
func writeCompareOptions(sb *strings.Builder, opts compareOptions) {
	bit := func(on bool) int {
		if on {
			return 1
		}
		return 0
	}
	fmt.Fprintf(sb, "Recursive = %d\n", bit(opts.Recursive))
	fmt.Fprintf(sb, "LimitDepth = %d\n", bit(opts.LimitDepth))
	fmt.Fprintf(sb, "MaxDepth = %d\n", opts.MaxDepth)
	fmt.Fprintf(sb, "MarkedOnly = %d\n", bit(opts.MarkedOnly))
	fmt.Fprintf(sb, "ByTime = %d\n", bit(opts.ByTime))
	fmt.Fprintf(sb, "TimeSlack = %d\n", bit(opts.TimeSlack))
	fmt.Fprintf(sb, "IgnoreZones = %d\n", bit(opts.IgnoreZones))
	fmt.Fprintf(sb, "BySize = %d\n", bit(opts.BySize))
	fmt.Fprintf(sb, "ByContent = %d\n", bit(opts.ByContent))
	fmt.Fprintf(sb, "Ignore = %d\n", bit(opts.Ignore))
	fmt.Fprintf(sb, "IgnoreMode = %d\n", opts.IgnoreMode)
	fmt.Fprintf(sb, "ReportEqual = %d\n", bit(opts.ReportEqual))
}
