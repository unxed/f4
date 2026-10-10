package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/textdiff"
	"github.com/unxed/f4/internal/vtvibe"
	"github.com/unxed/f4/internal/vtvibe/ap"
	"github.com/unxed/f4/internal/wheel"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Patch review after a dry run (f4#1606, docs/VTVIBE.md §7.3).
//
// This is the first, deliberately small cut of the review screen: a dry run
// no longer ends in a wall of patcher text but in a table with one row per
// modification (ap.Result.ModificationResults) - which file, which action,
// what it searched for, and whether it would apply, is already applied, or
// fails and why. From there the human either applies the patch for real or
// walks away having written nothing.
//
// Every row can be switched off and on again (Space, or Ins, which also
// moves down as it does in the panels). "Apply" then applies only the
// checked rows (ap.Options.Only), and "Dry run" runs the check again with
// the current choice - a row left out comes back as "excluded", and
// anything that only worked together with it shows up as failing before a
// byte is written.
//
// The table shares the dialog with a diff pane (aiReviewDiffPane) that is
// always on screen, below the table: it shows the row under the cursor's
// own edit (ap.ModificationResult.Preview, unified style - a line number
// and a leading ' '/'-'/'+' the way "diff -u" marks context/removed/added
// lines), and follows the cursor as it moves, no keypress needed. Tab (or
// Shift+Tab) moves the keyboard focus to the pane and back to the table
// (both aiReviewTable and aiReviewDiffPane answer it directly, ahead of
// vtui.Group's own Tab handling, which would walk on to the buttons; they
// stay reachable by their hotkeys). Ctrl+Tab is left alone and switches
// screens as anywhere else, the review being a screen of level 0. While the
// pane has focus, the arrow/paging keys scroll it instead of moving the
// table's cursor. Enter opens the file at the edit and F3 the whole patch
// (docs/VTVIBE.md §7.3); the table swallows both even when there is
// nothing to open, so Enter never falls through to the dialog's default
// button (see aiReviewTable.ProcessKey).
//
// F8 rejects the edit under the cursor with a reason: it is left out like a
// row switched off, and a line about it goes into the draft of the next
// message to the model (vtvibe_ap_reject.go). Ctrl+Z undoes the newest applied patch.

// aiReview is the review screen's state: the dry run's rows, which of them
// are checked and which are rejected. A rejected row is never checked.
type aiReview struct {
	mods  []ap.ModificationResult
	on    []bool
	patch *vtvibe.Patch
	rej   map[ap.ModKey]aiReject // nil: rejecting is not offered
}

// newAIReview checks every row except the ones the dry run itself was told
// to leave out, so a re-run dry run keeps the choice it was made with.
func newAIReview(mods []ap.ModificationResult) *aiReview {
	r := &aiReview{mods: mods, on: make([]bool, len(mods))}
	for i, m := range mods {
		r.on[i] = m.Status != ap.ModExcluded
	}
	return r
}

// attachRejections ties the review to patch's rejection list and switches
// the rows already rejected off.
func (r *aiReview) attachRejections(patch *vtvibe.Patch) {
	r.patch = patch
	r.rej = aiRejectionsFor(patch)
	for i := range r.mods {
		if r.isRejected(i) {
			r.on[i] = false
		}
	}
}

func (r *aiReview) toggle(i int) {
	if i >= 0 && i < len(r.on) && !r.isRejected(i) {
		r.on[i] = !r.on[i]
	}
}

func (r *aiReview) rejection(i int) (aiReject, bool) {
	if r.rej == nil || i < 0 || i >= len(r.mods) {
		return aiReject{}, false
	}
	rj, ok := r.rej[r.mods[i].Key()]
	return rj, ok
}

func (r *aiReview) isRejected(i int) bool {
	_, ok := r.rejection(i)
	return ok
}

func (r *aiReview) rejected() int {
	n := 0
	for i := range r.mods {
		if r.isRejected(i) {
			n++
		}
	}
	return n
}

// reject turns row i down (again, with a new reason if it already was):
// off, and the draft's rejection list rewritten.
func (r *aiReview) reject(i int, reason string) {
	if r.rej == nil || i < 0 || i >= len(r.mods) {
		return
	}
	r.rej[r.mods[i].Key()] = aiRejectOf(i, r.mods[i], reason)
	r.on[i] = false
	r.syncDraft()
}

// unreject takes row i's rejection back: the row is checked again and its
// line leaves the draft.
func (r *aiReview) unreject(i int) {
	if !r.isRejected(i) {
		return
	}
	delete(r.rej, r.mods[i].Key())
	r.on[i] = true
	r.syncDraft()
}

func (r *aiReview) syncDraft() {
	aiReviewSession().SetDraftSection(aiRejectSection, aiRejectDraftText(r.patch, r.rej))
}

func (r *aiReview) checked() int {
	n := 0
	for _, on := range r.on {
		if on {
			n++
		}
	}
	return n
}

// only is the ap.Options.Only for the current choice: nil when every row is
// checked (the whole patch, exactly as before there was a choice), else the
// checked rows' keys.
func (r *aiReview) only() map[ap.ModKey]bool {
	if r.checked() == len(r.on) {
		return nil
	}
	only := make(map[ap.ModKey]bool, len(r.on))
	for i, on := range r.on {
		if on {
			only[r.mods[i].Key()] = true
		}
	}
	return only
}

// canApply: some checked row would write something. An excluded row that
// has been checked again counts too - the dry run did not look at it, so
// nothing says it would not apply.
func (r *aiReview) canApply() bool {
	for i, on := range r.on {
		if on && aiReviewMayApply(r.mods[i].Status) {
			return true
		}
	}
	return false
}

func aiReviewMayApply(s ap.ModStatus) bool { return s == ap.ModOK || s == ap.ModExcluded }

// aiReviewRow is one ModificationResult as a table row.
type aiReviewRow struct {
	r *aiReview
	i int
}

const (
	aiReviewColCheck = iota
	aiReviewColFile
	aiReviewColAction
	aiReviewColStatus
	aiReviewColLocator
)

func (row aiReviewRow) GetCellText(col int) string {
	r := row.r.mods[row.i]
	switch col {
	case aiReviewColCheck:
		if row.r.isRejected(row.i) {
			return "[-]"
		}
		if row.r.on[row.i] {
			return "[x]"
		}
		return "[ ]"
	case aiReviewColFile:
		return r.FilePath
	case aiReviewColAction:
		if r.Action == "" {
			return "-"
		}
		return r.Action
	case aiReviewColStatus:
		if row.r.isRejected(row.i) {
			return i18n.Msg("AI.ReviewRejected")
		}
		return aiReviewStatusText(r.Status)
	case aiReviewColLocator:
		return aiReviewOneLine(r.Locator)
	}
	return ""
}

func aiReviewStatusText(s ap.ModStatus) string {
	switch s {
	case ap.ModOK:
		return i18n.Msg("AI.ReviewOK")
	case ap.ModSkipped:
		return i18n.Msg("AI.ReviewSkipped")
	case ap.ModExcluded:
		return i18n.Msg("AI.ReviewExcluded")
	default:
		return i18n.Msg("AI.ReviewFailed")
	}
}

// aiReviewOneLine squeezes a multi-line locator into one table cell: the
// first non-blank line, trimmed, with an ellipsis when there was more.
func aiReviewOneLine(s string) string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	switch len(lines) {
	case 0:
		return ""
	case 1:
		return lines[0]
	default:
		return lines[0] + " …"
	}
}

// aiReviewDetail is the line under the table for the selected row: why it
// fails, or, for a row that is fine, the locator it matched.
func aiReviewDetail(m ap.ModificationResult) string {
	if m.Status == ap.ModFailed && m.Err != nil {
		msg := aiReviewOneLine(m.Err.Message)
		if msg == "" {
			return string(m.Err.Code)
		}
		return string(m.Err.Code) + ": " + msg
	}
	return aiReviewOneLine(m.Locator)
}

// aiReviewCounts splits the modifications by outcome. Excluded rows are
// their own count: leaving an edit out is a choice, not an error.
func aiReviewCounts(mods []ap.ModificationResult) (ok, skipped, failed, excluded int) {
	for _, m := range mods {
		switch m.Status {
		case ap.ModOK:
			ok++
		case ap.ModSkipped:
			skipped++
		case ap.ModExcluded:
			excluded++
		default:
			failed++
		}
	}
	return
}

// aiReviewTotals is the dry run's summary line.
func aiReviewTotals(mods []ap.ModificationResult) string {
	ok, skipped, failed, excluded := aiReviewCounts(mods)
	if excluded > 0 {
		return fmt.Sprintf(i18n.Msg("AI.ReviewTotalsExcluded"), ok, skipped, failed, excluded)
	}
	return fmt.Sprintf(i18n.Msg("AI.ReviewTotals"), ok, skipped, failed)
}

// aiReviewTable is the review table with the row toggles on top of the
// ordinary vtui.Table keys.
type aiReviewTable struct {
	*vtui.Table
	onToggle    func(idx int)
	onReject    func(idx int)
	onOpen      func(idx int)
	onUndo      func()
	onViewPatch func()
	onTab       func()
}

func (t *aiReviewTable) ProcessKey(e *vtinput.InputEvent) bool {
	if aiReviewIsCtrlZ(e) {
		if t.onUndo != nil {
			t.onUndo()
		}
		return true
	}
	if aiReviewIsTab(e) {
		if t.onTab != nil {
			t.onTab()
		}
		return true
	}
	if e != nil && e.KeyDown && e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed|
		vtinput.LeftAltPressed|vtinput.RightAltPressed|vtinput.ShiftPressed) == 0 {
		switch e.VirtualKeyCode {
		case vtinput.VK_SPACE, vtinput.VK_INSERT:
			t.onToggle(t.RowAt(t.SelectPos))
			if e.VirtualKeyCode == vtinput.VK_INSERT {
				t.MoveSelection(1)
			}
			return true
		case vtinput.VK_RETURN:
			// Enter opens the file at the edit (docs/VTVIBE.md §7.3). It is
			// swallowed even when there is nothing to open, so that it never
			// falls through to the group and presses the dialog's default
			// button (usually "Apply") just because the table has focus.
			if t.onOpen != nil {
				t.onOpen(t.RowAt(t.SelectPos))
			}
			return true
		case vtinput.VK_F3:
			// F3 shows the whole patch, as the AI panel's log button shows
			// the patcher's output.
			if t.onViewPatch != nil {
				t.onViewPatch()
			}
			return true
		case vtinput.VK_F8:
			if t.onReject != nil {
				t.onReject(t.RowAt(t.SelectPos))
			}
			return true
		}
	}
	return t.Table.ProcessKey(e)
}

// aiReviewDiffLine is one line of an aiReviewDiffPane's unified rendering:
// a leading marker (' ' context, '-' only in Before, '+' only in After),
// the line number on whichever side the marker points at, and the text.
type aiReviewDiffLine struct {
	marker byte
	lineNo int
	text   string
}

// aiReviewBuildDiffLines turns a Preview's Before/After fragment into a
// unified-diff-shaped line list, the layout docs/VTVIBE.md §7.3 draws for
// the review screen's diff panel: context once, a removed line then the
// added line(s) that replaced it, each numbered on its own side. It reuses
// internal/textdiff (the same line-diff algorithm internal/diffview draws
// full-screen) rather than a diff of its own; Preview's own Before/After
// line indices, already 0-based into the fragment, plus StartLine, are
// enough to number every line without tracking two running counters here.
func aiReviewBuildDiffLines(p *ap.Preview) []aiReviewDiffLine {
	ops, err := textdiff.Diff(p.Before, p.After)
	if err != nil {
		return nil
	}
	rows := textdiff.Rows(p.Before, p.After, ops)
	lines := make([]aiReviewDiffLine, 0, len(rows)+1)
	for _, row := range rows {
		switch {
		case row.Left.Kind == textdiff.RowFiller:
			lines = append(lines, aiReviewDiffLine{'+', p.StartLine + row.Right.Line, row.Right.Text})
		case row.Right.Kind == textdiff.RowFiller:
			lines = append(lines, aiReviewDiffLine{'-', p.StartLine + row.Left.Line, row.Left.Text})
		case row.Left.Kind == textdiff.RowChanged:
			lines = append(lines, aiReviewDiffLine{'-', p.StartLine + row.Left.Line, row.Left.Text})
			lines = append(lines, aiReviewDiffLine{'+', p.StartLine + row.Right.Line, row.Right.Text})
		default: // RowEqual
			// Both sides carry the same text, but only Right.Line, the
			// after-edit number, stays meaningful once an earlier
			// insertion or deletion in this fragment has shifted line
			// numbers; ahead of the edit the two are equal anyway.
			lines = append(lines, aiReviewDiffLine{' ', p.StartLine + row.Right.Line, row.Left.Text})
		}
	}
	return lines
}

// aiReviewDiffPane is the review screen's permanent diff panel (f4#1606
// step e, replacing the Enter/F3 modal of 8/N): a read-only, scrollable
// view of the table row under the cursor's own edit. setPreview is called
// once at construction and again from the table's OnSelect, so the pane
// always shows the current row without a keypress. Tab (see
// aiReviewTable.ProcessKey and this type's own ProcessKey) moves the
// dialog's keyboard focus onto the pane and back; while focused, the pane
// scrolls with the same keys internal/diffview does.
type aiReviewDiffPane struct {
	vtui.ScreenObject
	title   string
	lines   []aiReviewDiffLine
	message string // shown instead of lines, e.g. AI.ReviewNoDiff
	topPos  int
	onTab   func()
	onUndo  func()

	// wheelCoast is what a fast wheel spin leaves behind: lines the pane
	// still owes the scroll position (see internal/wheel).
	wheelCoast wheel.Coast
}

func newAIReviewDiffPane(w, h int) *aiReviewDiffPane {
	p := &aiReviewDiffPane{}
	p.SetPosition(0, 0, w-1, h-1)
	p.SetCanFocus(true)
	return p
}

// setPreview points the pane at m's own edit: nothing (AI.ReviewNoDiff) for
// a row with no Preview (already applied, failing, excluded, RENAME, a
// whole file deleted or created) or, degenerately, a Preview whose Before
// and After turn out equal.
func (p *aiReviewDiffPane) setPreview(m ap.ModificationResult) {
	p.topPos = 0
	p.lines = nil
	p.title = ""
	p.message = ""
	if m.Preview == nil {
		p.message = i18n.Msg("AI.ReviewNoDiff")
		return
	}
	p.title = fmt.Sprintf("%s:%d", m.FilePath, m.Preview.StartLine)
	p.lines = aiReviewBuildDiffLines(m.Preview)
	if len(p.lines) == 0 {
		p.message = i18n.Msg("AI.ReviewNoDiff")
	}
}

// viewHeight is how many lines fit under the pane's title and its dashed
// rule (one row each).
func (p *aiReviewDiffPane) viewHeight() int {
	if h := p.Y2 - p.Y1 - 1; h > 0 {
		return h
	}
	return 0
}

func (p *aiReviewDiffPane) clampTop() {
	if maxTop := len(p.lines) - p.viewHeight(); p.topPos > maxTop {
		p.topPos = max(maxTop, 0)
	}
	if p.topPos < 0 {
		p.topPos = 0
	}
}

// ProcessKey: Tab hands focus back to the table (answered here, ahead of
// vtui.Group's own Tab handling, the same way aiReviewTable answers it -
// see that type's comment); while focused, the pane scrolls instead of
// moving anything in the table it no longer has the cursor on.
func (p *aiReviewDiffPane) ProcessKey(e *vtinput.InputEvent) bool {
	if aiReviewIsCtrlZ(e) {
		if p.onUndo != nil {
			p.onUndo()
		}
		return true
	}
	if e == nil || !e.KeyDown {
		return false
	}
	if aiReviewIsTab(e) {
		if p.onTab != nil {
			p.onTab()
		}
		return true
	}
	if e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0 {
		return false
	}
	h := max(p.viewHeight(), 1)
	switch e.VirtualKeyCode {
	case vtinput.VK_UP:
		p.topPos--
	case vtinput.VK_DOWN:
		p.topPos++
	case vtinput.VK_PRIOR: // PgUp
		p.topPos -= h
	case vtinput.VK_NEXT: // PgDn
		p.topPos += h
	case vtinput.VK_HOME:
		p.topPos = 0
	case vtinput.VK_END:
		p.topPos = len(p.lines)
	default:
		return false
	}
	p.clampTop()
	return true
}

// ProcessMouse gives the pane the same wheel scrolling internal/diffview
// has; clicking it does nothing yet (moving focus there is Tab's job).
func (p *aiReviewDiffPane) ProcessMouse(e *vtinput.InputEvent) bool {
	if e.WheelDirection == 0 {
		return false
	}
	direction := 1
	if e.WheelDirection > 0 {
		direction = -1
	}
	// A spin faster than one notch per spin window queues extra lines the
	// pane keeps scrolling on its own (see internal/wheel).
	p.wheelCoast.Notch(direction, p.scrollBy)
	p.scrollBy(direction * 3)
	return true
}

// scrollBy moves the first shown line by step lines, positive down the
// diff, and reports whether anything moved so a coast stops at an end of
// the patch instead of spinning in place.
func (p *aiReviewDiffPane) scrollBy(step int) bool {
	before := p.topPos
	p.topPos += step
	p.clampTop()
	return p.topPos != before
}

// aiReviewDiffLineAttr tints the base text attribute by marker, the same
// muted red/green internal/diffview uses for a deleted/inserted row.
func aiReviewDiffLineAttr(base uint64, marker byte) uint64 {
	switch marker {
	case '-':
		return vtui.SetRGBBack(base, 0x5A2323)
	case '+':
		return vtui.SetRGBBack(base, 0x235A23)
	default:
		return base
	}
}

func (p *aiReviewDiffPane) Show(scr *vtui.ScreenBuf) {
	p.ScreenObject.Show(scr)
	x1, y1, x2, y2 := p.X1, p.Y1, p.X2, p.Y2
	if x2 < x1 || y2 < y1 {
		return
	}
	width := x2 - x1 + 1
	textAttr := p.GetStateAttr(vtui.ColDialogText, vtui.ColDialogText)
	titleAttr := p.GetStateAttr(vtui.ColDialogBoxTitle, vtui.ColDialogHighlightBoxTitle)
	boxAttr := vtui.Palette[vtui.ColDialogBox]

	pnt := vtui.NewPainter(scr)
	pnt.Fill(x1, y1, x2, y2, ' ', textAttr)
	pnt.DrawString(x1, y1, aiReviewLabel(p.title, width), titleAttr)
	if y2 > y1 {
		pnt.Fill(x1, y1+1, x2, y1+1, '─', boxAttr)
	}

	p.clampTop()
	h := p.viewHeight()
	if len(p.lines) == 0 {
		if p.message != "" && h > 0 {
			pnt.DrawString(x1, y1+2, aiReviewLabel(p.message, width), textAttr)
		}
		return
	}
	for i := 0; i < h; i++ {
		idx := p.topPos + i
		if idx >= len(p.lines) {
			break
		}
		y := y1 + 2 + i
		ln := p.lines[idx]
		attr := aiReviewDiffLineAttr(textAttr, ln.marker)
		pnt.Fill(x1, y, x2, y, ' ', attr)
		text := fmt.Sprintf("%c%5d %s", ln.marker, ln.lineNo, ln.text)
		pnt.DrawString(x1, y, vtui.TruncateString(text, width, ""), attr)
	}
}

// aiReviewLabel fits s into exactly w columns for a vtui.Text: '&' would
// otherwise be read as a hotkey marker, and a long line is cut rather than
// drawn over the frame.
func aiReviewLabel(s string, w int) string {
	s = runewidth.Truncate(s, w, "…")
	return strings.ReplaceAll(dialog.PadLabelTo(s, w), "&", "&&")
}

// aiReviewRunPatcher is what the review screen's Apply and Dry run buttons
// call; a variable only so tests can see what they asked for (assigned in
// init, as a plain initializer would be an initialization cycle through
// aiRunPatcher -> aiShowPatchReview).
var aiReviewRunPatcher func(pf *panel.PanelsFrame, patch *vtvibe.Patch, root string, dry bool, only map[ap.ModKey]bool)

func init() { aiReviewRunPatcher = aiRunPatcher }

// aiShowPatchReview is what a dry run ends in when the patcher got as far
// as reasoning about individual modifications. exitCode/output are the same
// as for aiShowPatchResult, which remains the screen for a real run and for
// a dry run that failed before any modification (a patch that does not
// parse, say).
func aiShowPatchReview(pf *panel.PanelsFrame, patch *vtvibe.Patch, root string, mods []ap.ModificationResult, exitCode int, output string) *vtui.Window {
	scrW := vtui.FrameManager.GetScreenSize()
	scrH := vtui.FrameManager.GetScreenHeight()
	// The review is a screen of its own -- level 0 of the navigation, docs/
	// VTVIBE.md §7.3 -- not a modal box over the AI panel: it fills the
	// workspace above the key bar, so the reader can leave it for the editor
	// (Enter opens the file there) and come back to it by switching screens.
	//
	// The sizes below come from aiReviewGeometry, so that a resized terminal
	// gets the same layout again (aiReviewScreen.ResizeConsole).
	top, dlgW, dlgH, diffH, inner := aiReviewGeometry(scrW, scrH)

	dlg := vtui.NewCenteredDialog(dlgW, dlgH, i18n.Msg("AI.ReviewTitle"))
	dlg.ShowClose = true
	dlg.Modal = false
	dlg.SetPosition(0, top, dlgW-1, top+dlgH-1)

	statusW := runewidth.StringWidth(i18n.Msg("AI.ReviewColStatus"))
	for _, s := range []ap.ModStatus{ap.ModOK, ap.ModSkipped, ap.ModFailed, ap.ModExcluded} {
		statusW = max(statusW, runewidth.StringWidth(aiReviewStatusText(s)))
	}
	statusW = max(statusW, runewidth.StringWidth(i18n.Msg("AI.ReviewRejected")))
	cols := []vtui.TableColumn{
		{Title: "", Width: 3},
		{Title: i18n.Msg("AI.ReviewColFile"), MinWidth: 12},
		{Title: i18n.Msg("AI.ReviewColAction"), Width: 13},
		{Title: i18n.Msg("AI.ReviewColStatus"), Width: statusW},
		{Title: i18n.Msg("AI.ReviewColLocator"), MinWidth: 12},
	}
	// Below the table: the selected row's detail, the totals, how many rows
	// are checked, the diff hint, the permanent diff pane, a blank line and
	// the buttons.
	rev := newAIReview(mods)
	if patch != nil {
		rev.attachRejections(patch)
	}
	table := &aiReviewTable{Table: vtui.NewTable(0, 0, inner, dlgH-9-diffH, cols)}
	table.SetOwner(dlg)
	table.ShowHeader = true
	table.ShowScrollBar = true
	rows := make([]vtui.TableRow, len(mods))
	for i := range mods {
		rows[i] = aiReviewRow{r: rev, i: i}
	}
	table.SetRows(rows)

	detail := vtui.NewText(0, 0, aiReviewLabel("", inner), 0)
	showDetail := func(idx int) {
		text := ""
		if rj, ok := rev.rejection(idx); ok {
			reason := rj.reason
			if reason == "" {
				reason = i18n.Msg("AI.RejectNoReason")
			}
			text = fmt.Sprintf(i18n.Msg("AI.ReviewRejectedDetail"), reason)
		} else if idx >= 0 && idx < len(mods) {
			text = aiReviewDetail(mods[idx])
		}
		detail.SetText(aiReviewLabel(text, inner))
	}
	showDetail(0)

	diffPane := newAIReviewDiffPane(inner, diffH)
	diffPane.SetOwner(dlg)
	if len(mods) > 0 {
		diffPane.setPreview(mods[0])
	}
	table.OnSelect = func(idx int) {
		showDetail(idx)
		if idx >= 0 && idx < len(mods) {
			diffPane.setPreview(mods[idx])
		}
		vtui.FrameManager.Redraw()
	}
	table.onOpen = func(idx int) {
		if idx >= 0 && idx < len(mods) {
			aiOpenReviewedFile(pf, root, mods[idx])
		}
	}
	if patch != nil {
		table.onViewPatch = func() { aiViewPatchText(pf, patch.Text) }
	}
	table.onUndo = func() { aiUndoPatch(pf) }
	diffPane.onUndo = table.onUndo
	table.onTab = func() { dlg.SetFocusedItem(diffPane) }
	diffPane.onTab = func() { dlg.SetFocusedItem(table) }

	totals := vtui.NewText(0, 0, aiReviewLabel(aiReviewTotals(mods), inner), 0)
	checkedLabel := func() string {
		if n := rev.rejected(); n > 0 {
			return aiReviewLabel(fmt.Sprintf(i18n.Msg("AI.ReviewCheckedRejected"), rev.checked(), len(mods), n), inner)
		}
		return aiReviewLabel(fmt.Sprintf(i18n.Msg("AI.ReviewChecked"), rev.checked(), len(mods)), inner)
	}
	checked := vtui.NewText(0, 0, checkedLabel(), 0)
	hint := i18n.Msg("AI.ReviewDiffHint")
	if rev.rej != nil {
		hint += " " + i18n.Msg("AI.ReviewRejectHint")
	}
	diffHint := vtui.NewText(0, 0, aiReviewLabel(hint, inner), 0)

	var buttons []*vtui.Button
	addButton := func(label string, onClick func()) *vtui.Button {
		b := vtui.NewButton(0, 0, label)
		b.SetOwner(dlg)
		b.OnClick = onClick
		buttons = append(buttons, b)
		return b
	}
	// Apply is offered only when some row could write something: a patch
	// whose every row is "already applied" or "fails" would write nothing
	// whatever is checked. It is disabled while no such row is checked.
	var applyBtn *vtui.Button
	mayApply := false
	for _, m := range mods {
		mayApply = mayApply || aiReviewMayApply(m.Status)
	}
	if mayApply && patch != nil {
		applyBtn = addButton(i18n.Msg("AI.BtnApplyPatch"), func() {
			if !rev.canApply() {
				return
			}
			dlg.Close()
			aiReviewRunPatcher(pf, patch, root, false, rev.only())
		})
	}
	if patch != nil {
		addButton(i18n.Msg("AI.ReviewBtnDryRun"), func() {
			dlg.Close()
			aiReviewRunPatcher(pf, patch, root, true, rev.only())
		})
	}
	if output = strings.TrimSpace(output); output != "" {
		addButton(i18n.Msg("AI.BtnViewLog"), func() { aiViewPatchLog(pf, output) })
	}
	reportPath := filepath.Join(root, "afailed.md")
	if exitCode != 0 {
		if st, err := os.Stat(reportPath); err == nil && !st.IsDir() {
			addButton(i18n.Msg("AI.BtnAttachReport"), func() { aiAttachFailureReport(reportPath) })
		}
	}
	closeBtn := addButton(i18n.Msg("AI.ReviewBtnClose"), func() { dlg.Close() })
	syncButtons := func() {
		can := applyBtn != nil && rev.canApply()
		if applyBtn != nil {
			applyBtn.SetDisabled(!can)
			applyBtn.IsDefault = can
		}
		closeBtn.IsDefault = !can
	}
	syncButtons()
	refresh := func() {
		checked.SetText(checkedLabel())
		showDetail(table.RowAt(table.SelectPos))
		syncButtons()
		vtui.FrameManager.Redraw()
	}
	table.onToggle = func(idx int) {
		rev.toggle(idx)
		refresh()
	}
	if rev.rej != nil {
		table.onReject = func(idx int) {
			if idx < 0 || idx >= len(mods) {
				return
			}
			cur, was := rev.rejection(idx)
			what := fmt.Sprintf(i18n.Msg("AI.RejectWhat"), idx+1, aiRejectSubject(aiRejectOf(idx, mods[idx], "")))
			aiShowRejectDialog(what, cur.reason, was,
				func(reason string) { rev.reject(idx, reason); refresh() },
				func() { rev.unreject(idx); refresh() })
		}
	}

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+1, inner, dlgH-2)
	vbox.Add(table, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(detail, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(totals, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(checked, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(diffHint, vtui.Margins{Bottom: 1}, vtui.AlignFill)
	vbox.Add(diffPane, vtui.Margins{Bottom: 1}, vtui.AlignFill)
	btnRow := vtui.NewHBoxLayout(0, 0, inner, 1)
	btnRow.HorizontalAlign = vtui.AlignCenter
	btnRow.Spacing = 2
	for _, b := range buttons {
		btnRow.Add(b, vtui.Margins{}, vtui.AlignTop)
	}
	vbox.Add(btnRow, vtui.Margins{}, vtui.AlignFill)
	vbox.Apply()

	dlg.AddItem(table)
	dlg.AddItem(detail)
	dlg.AddItem(totals)
	dlg.AddItem(checked)
	dlg.AddItem(diffHint)
	dlg.AddItem(diffPane)
	for _, b := range buttons {
		dlg.AddItem(b)
	}

	// relayout builds the same screen again for a terminal of another size:
	// the dialog takes the whole workspace, the table and the diff pane are
	// resized and the one-line labels are cut to the new width. Nothing the
	// reader has done (checked rows, rejections, the cursor, the pane's
	// scroll) is touched.
	relayout := func(w, h int) {
		var top, dlgW, dlgH, diffH int
		top, dlgW, dlgH, diffH, inner = aiReviewGeometry(w, h)
		dlg.SetPosition(0, top, dlgW-1, top+dlgH-1)
		table.SetPosition(0, 0, inner-1, dlgH-9-diffH-1)
		diffPane.SetPosition(0, 0, inner-1, diffH-1)
		btnRow.SetPosition(0, 0, inner-1, 0)
		totals.SetText(aiReviewLabel(aiReviewTotals(mods), inner))
		diffHint.SetText(aiReviewLabel(hint, inner))
		vbox.SetPosition(dlg.X1+2, dlg.Y1+1, dlg.X1+2+inner-1, dlg.Y1+dlgH-2)
		vbox.Apply()
		diffPane.clampTop()
		refresh()
	}
	screen := &aiReviewScreen{Window: dlg, relayout: relayout}
	vtui.FrameManager.AddScreen(screen)
	return dlg
}

// aiReviewGeometry is the review screen's size for a terminal of scrW x scrH:
// the workspace's top row, the dialog's width and height, the diff pane's
// height and the width inside the frame.
//
// The diff pane sits under the table rather than beside it: the table's
// columns need most of the width to stay readable (File/Locator are MinWidth
// 12 each, and go narrower than that fast), so splitting the dialog left/right
// the way docs/VTVIBE.md §7.3's mockup draws it would starve one side or the
// other on anything but a very wide screen.
func aiReviewGeometry(scrW, scrH int) (top, dlgW, dlgH, diffH, inner int) {
	top = vtui.FrameManager.WorkspaceTopInset()
	dlgW = scrW
	diffH = min(max(scrH/4, 6), 14)
	dlgH = max(scrH-1-top, 16+diffH)
	inner = dlgW - 4
	return top, dlgW, dlgH, diffH, inner
}

// aiReviewScreen is the review window as the frame manager holds it: the
// window itself plus the re-layout a resized terminal asks for (a plain
// non-modal vtui.Window would only re-centre itself).
type aiReviewScreen struct {
	*vtui.Window
	relayout func(w, h int)
}

func (s *aiReviewScreen) ResizeConsole(w, h int) { s.relayout(w, h) }

// aiViewPatchLog opens the patcher's text output in the viewer.
func aiViewPatchLog(pf *panel.PanelsFrame, output string) {
	dir, err := os.MkdirTemp("", "vtvibe-ap-log-")
	if err != nil {
		return
	}
	logPath := filepath.Join(dir, "ap_output.log")
	if os.WriteFile(logPath, []byte(output), 0600) == nil {
		actionOpenViewer(pf, vfs.NewOSVFS(dir), "ap_output.log")
	}
}

// aiOpenReviewedFile opens the file a modification edits in the editor, on the
// line where the edit starts (its Preview), as a screen of its own above the
// review: closing the editor comes back to the review with its checks as they
// were. A file that is not there (a CREATE not yet applied, a deleted file)
// is reported rather than opened as an empty one.
func aiOpenReviewedFile(pf *panel.PanelsFrame, root string, m ap.ModificationResult) {
	if m.FilePath == "" {
		return
	}
	full := filepath.Join(root, filepath.FromSlash(m.FilePath))
	if st, err := os.Stat(full); err != nil || st.IsDir() {
		vtui.ShowMessage(" "+i18n.Msg("AI.ReviewTitle")+" ", fmt.Sprintf(i18n.Msg("AI.ReviewNoFile"), m.FilePath), []string{"&Ok"})
		return
	}
	v := vfs.NewOSVFS(filepath.Dir(full))
	name := filepath.Base(full)
	_, already := findOpenedEditor(v, name)
	actionOpenEditor(pf, v, name)
	if already >= 0 || m.Preview == nil || m.Preview.StartLine < 1 {
		return
	}
	if ev, idx := findOpenedEditor(v, name); ev != nil && idx >= 0 {
		ev.TargetLine = m.Preview.StartLine - 1
		ev.TargetPos = 0
		ev.TargetTopRow = 0
		ev.TargetLeft = 0
	}
}

// aiViewPatchText shows the whole patch in the viewer.
func aiViewPatchText(pf *panel.PanelsFrame, text string) {
	dir, err := os.MkdirTemp("", "vtvibe-ap-patch-")
	if err != nil {
		return
	}
	if os.WriteFile(filepath.Join(dir, "patch.ap"), []byte(text), 0600) == nil {
		actionOpenViewer(pf, vfs.NewOSVFS(dir), "patch.ap")
	}
}

// aiReviewIsCtrlZ is Ctrl+Z (either Ctrl, no Alt or Shift): undo the newest
// applied patch from the review screen, as in the AI panel (docs/VTVIBE.md
// §7.4). It is answered by the table and the diff pane alike, so it works
// whichever of them has the focus.
// aiReviewIsTab reports a plain Tab or Shift+Tab press: the review's
// list<->diff focus switch (docs/VTVIBE.md §7.3). Ctrl/Alt+Tab are not it -
// Ctrl+Tab belongs to the workspace switcher.
func aiReviewIsTab(e *vtinput.InputEvent) bool {
	return e != nil && e.KeyDown && e.VirtualKeyCode == vtinput.VK_TAB &&
		e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed|
			vtinput.LeftAltPressed|vtinput.RightAltPressed) == 0
}

func aiReviewIsCtrlZ(e *vtinput.InputEvent) bool {
	if e == nil || e.Type != vtinput.KeyEventType || !e.KeyDown || e.VirtualKeyCode != vtinput.VK_Z {
		return false
	}
	const ctrl = vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed
	const others = vtinput.LeftAltPressed | vtinput.RightAltPressed | vtinput.ShiftPressed
	return e.ControlKeyState&ctrl != 0 && e.ControlKeyState&others == 0
}
