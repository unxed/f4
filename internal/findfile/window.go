package findfile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/appcmd"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Host supplies application actions without importing the composition root.
type Host struct {
	View  func(vfs.VFS, string)
	Edit  func(vfs.VFS, string)
	GoTo  func(vfs.VFS, string)
	Panel func(vfs.VFS, []vfs.FoundEntry)
}

// SearchResultsWindow owns every widget and the displayed result slice on the
// UI thread. Workers publish immutable entries through updates instead.
type SearchResultsWindow struct {
	*vtui.Window
	table                     *vtui.Table
	found                     []vfs.FoundEntry
	vfs                       vfs.VFS
	host                      Host
	current, status           *vtui.Text
	percentage                *vtui.Text
	bar                       *vtui.ProgressBar
	pauseButton               *searchPauseButton
	fileButtons               []*vtui.Button
	cancel                    context.CancelFunc
	running, stopping, closed bool
	paused                    bool
	pauseGate                 pauseGate
	progress                  vfs.FindProgress
	updates                   *searchUpdates
	buttons                   *vtui.HBoxLayout
	header                    []*vtui.Text
	headerLines               []string
	currentText, statusText   string
	searchScope               string
	request                   []requestControl
	requestBottom             int
	scanningSeparator         *vtui.Separator
	tableSeparator            *resultsSeparator
}

// resultsSeparator paints its caption as part of the line, rather than as
// overlapping sibling controls.
type resultsSeparator struct {
	*vtui.Separator
	caption *vtui.Text
}

// vtui.Button.Show makes hidden buttons visible again. Keep the search-only
// button hidden once its work has ended.
type searchPauseButton struct {
	*vtui.Button
}

func (b *searchPauseButton) Show(scr *vtui.ScreenBuf) {
	if b.IsVisible() {
		b.Button.Show(scr)
	}
}

func (s *resultsSeparator) Show(scr *vtui.ScreenBuf) {
	s.Separator.Show(scr)
	if s.caption != nil {
		s.caption.Show(scr)
	}
}

type requestControl struct {
	element        vtui.UIElement
	x1, y1, x2, y2 int
}

type searchUpdates struct {
	mu                         sync.Mutex
	rows                       []vfs.FoundEntry
	progress                   vfs.FindProgress
	queued, firstHit, finished bool
	err                        error
}

// Start pushes the result window before starting any I/O.
func Start(v vfs.VFS, root, mask, text string, options Options, host Host) *SearchResultsWindow {
	w := newWindow(v, host, true)
	w.addSearchHeader(root, mask, text, options)
	return start(w, root, mask, text, options)
}

// Expand keeps the submitted dialog and its controls, adding results below them.
// The caller hides its submit buttons before handing the window over.
func Expand(dlg *vtui.Window, controls []vtui.UIElement, v vfs.VFS, root, mask, text string, options Options, host Host) *SearchResultsWindow {
	for _, child := range dlg.GetChildren() {
		if button, ok := child.(*vtui.Button); ok {
			button.SetDisabled(true)
			button.SetVisible(false)
			button.IsDefault = false
			button.OnClick = nil
			button.Lock()
		}
	}
	request := make([]requestControl, 0, len(controls))
	bottom := 2
	for _, element := range controls {
		x1, y1, x2, y2 := element.GetPosition()
		request = append(request, requestControl{
			element: element,
			x1:      x1 - dlg.X1, y1: y1 - dlg.Y1,
			x2: x2 - dlg.X1, y2: y2 - dlg.Y1,
		})
		bottom = max(bottom, y2-dlg.Y1)
		element.SetDisabled(true)
	}
	vtui.FrameManager.RemoveFrame(dlg)
	w := newWindowOn(v, host, true, dlg)
	// Reclaim empty request rows on short screens so separators do not
	// displace the first result or the action row.
	available := vtui.FrameManager.GetScreenHeight() - 1
	for row := bottom - 1; bottom+12 > available && row > 3; row-- {
		occupied := false
		for _, control := range request {
			if control.y1 <= row && control.y2 >= row {
				occupied = true
				break
			}
		}
		if occupied {
			continue
		}
		for i := range request {
			if request[i].y1 > row {
				request[i].y1--
				request[i].y2--
			}
		}
		bottom--
	}
	w.request, w.requestBottom = request, bottom
	w.MinH = bottom + 12
	height := min(max(bottom+19, w.Y2-w.Y1+1), available)
	w.ChangeSize(w.X2-w.X1+1, height)
	if w.Y2 >= vtui.FrameManager.GetScreenHeight()-1 {
		w.MoveRelative(0, vtui.FrameManager.GetScreenHeight()-2-w.Y2)
	}
	w.layout()
	return start(w, root, mask, text, options)
}

func start(w *SearchResultsWindow, root, mask, text string, options Options) *SearchResultsWindow {
	options.SelectedFolders = append([]string(nil), options.SelectedFolders...)
	w.searchScope = root
	if len(options.SelectedFolders) > 0 {
		w.searchScope = strings.Join(options.SelectedFolders, ", ")
	}
	w.running = true
	w.updates = &searchUpdates{progress: vfs.FindProgress{Path: root}}
	w.progress.Path = root
	w.refreshState(nil)
	vtui.FrameManager.Push(w)
	task := vtui.RunAsync(func(ctx *vtui.TaskContext) {
		vtui.DebugLog("FIND: starting root=%q mask=%q options=%+v", root, mask, options)
		done := make(chan struct{})
		go func() {
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					w.updates.schedule(ctx, w)
				case <-ctx.Done():
					return
				case <-done:
					return
				}
			}
		}()
		err := run(ctx.Context, w.vfs, root, mask, text, options, func(hit vfs.FoundEntry) {
			if !w.pauseGate.wait(ctx.Context) {
				return
			}
			w.updates.mu.Lock()
			w.updates.rows = append(w.updates.rows, hit)
			first := !w.updates.firstHit
			w.updates.firstHit = true
			w.updates.mu.Unlock()
			if first {
				w.updates.schedule(ctx, w)
			}
		}, func(p vfs.FindProgress) {
			if !w.pauseGate.wait(ctx.Context) {
				return
			}
			w.updates.mu.Lock()
			w.updates.progress = p
			w.updates.mu.Unlock()
		})
		w.updates.mu.Lock()
		w.updates.finished, w.updates.err = true, err
		w.updates.mu.Unlock()
		close(done)
		w.updates.schedule(ctx, w)
		vtui.DebugLog("FIND: finished root=%q error=%v", root, err)
	})
	w.cancel = task.Cancel
	return w
}

// Show displays an already completed result set, including duplicate searches.
func Show(v vfs.VFS, found []vfs.FoundEntry, host Host) *SearchResultsWindow {
	w := newWindow(v, host, false)
	w.appendRows(found)
	vtui.FrameManager.Push(w)
	return w
}

func (u *searchUpdates) schedule(ctx *vtui.TaskContext, w *SearchResultsWindow) {
	u.mu.Lock()
	if u.queued {
		u.mu.Unlock()
		return
	}
	u.queued = true
	u.mu.Unlock()
	ctx.RunOnUI(func() {
		u.mu.Lock()
		rows, p, finished, err := u.rows, u.progress, u.finished, u.err
		u.rows, u.queued = nil, false
		u.mu.Unlock()
		if w.closed {
			return
		}
		w.appendRows(rows)
		w.progress = p
		if finished {
			w.running = false
			w.paused = false
		}
		w.refreshState(err)
		vtui.FrameManager.Redraw()
	})
}

func newWindow(v vfs.VFS, host Host, live bool) *SearchResultsWindow {
	return newWindowOn(v, host, live, nil)
}

func newWindowOn(v vfs.VFS, host Host, live bool, dlg *vtui.Window) *SearchResultsWindow {
	height, tableHeight := 20, 12
	if live {
		height, tableHeight = 24, 8
	}
	if dlg == nil {
		dlg = vtui.NewCenteredDialog(78, height, i18n.Msg("FindFile.SearchResultsTitle"))
	}
	w := &SearchResultsWindow{Window: dlg, vfs: v, host: host}
	w.ShowClose = true
	w.ShowZoom = true
	w.OnResult = func(int) {
		w.closed = true
		if w.running {
			vtui.DebugLog("FIND: closing and cancelling search")
		}
		if w.cancel != nil {
			w.cancel()
		}
	}
	w.table = vtui.NewTable(0, 0, 74, tableHeight, []vtui.TableColumn{
		{Title: i18n.Msg("FindFile.ColName"), Width: 20},
		{Title: i18n.Msg("FindFile.ColPath"), Width: 0},
		{Title: i18n.Msg("FindFile.ColSize"), Width: 10, Alignment: vtui.AlignRight},
	})
	w.table.SetOwner(w)
	w.tableSeparator = &resultsSeparator{Separator: vtui.NewSeparator(0, 0, 74, false, false)}
	w.AddItem(w.tableSeparator)
	theme.UseTableColors(w.table)
	w.table.SetCellProvider(w)
	w.table.ShowScrollBar = true
	w.table.OnAction = func(int) { w.goTo() }
	w.AddItem(w.table)
	buttons := vtui.NewHBoxLayout(0, 0, 74, 1)
	w.buttons = buttons
	buttons.HorizontalAlign, buttons.Spacing = vtui.AlignCenter, 1
	add := func(key string, click func()) *vtui.Button {
		b := vtui.NewButton(0, 0, i18n.Msg(key))
		b.SetOwner(w)
		b.OnClick = click
		w.AddItem(b)
		buttons.Add(b, vtui.Margins{}, vtui.AlignTop)
		return b
	}
	goButton := add("FindFile.BtnGoTo", w.goTo)
	goButton.IsDefault = true
	panelButton := add("FindFile.BtnPanel", func() { w.sendToPanel() })
	viewButton := add("FindFile.BtnView", func() { w.HandleCommand(appcmd.CmView, nil) })
	editButton := add("FindFile.BtnEdit", func() { w.HandleCommand(appcmd.CmEdit, nil) })
	w.fileButtons = []*vtui.Button{goButton, panelButton, viewButton, editButton}
	if live {
		w.pauseButton = &searchPauseButton{Button: vtui.NewButton(0, 0, i18n.Msg("FindFile.BtnPause"))}
		w.pauseButton.SetOwner(w)
		w.pauseButton.OnClick = w.TogglePause
		w.AddItem(w.pauseButton)
	}
	add("FindFile.BtnClose", func() { w.Close() })
	vbox := vtui.NewVBoxLayout(w.X1+2, w.Y1+2, 74, height-4)
	if live {
		// The header is inserted by addSearchHeader before the window is shown.
		w.scanningSeparator = vtui.NewSeparator(0, 0, 74, false, false)
		w.AddItem(w.scanningSeparator)
		w.current = label(74)
		w.status = label(74)
		w.status.SetOwner(w)
		w.tableSeparator.caption = w.status
		w.percentage = label(4)
		w.bar = vtui.NewProgressBar(0, 0, 74)
		for _, el := range []vtui.UIElement{w.current, w.bar, w.percentage} {
			w.AddItem(el)
		}
		vbox.SetPosition(w.X1+2, w.Y1+7, w.X2-2, w.Y2-2)
		vbox.Add(w.current, vtui.Margins{}, vtui.AlignFill)
		vbox.Add(w.bar, vtui.Margins{}, vtui.AlignFill)
	}
	vbox.Add(w.table, vtui.Margins{Bottom: 1}, vtui.AlignFill)
	vbox.Add(buttons, vtui.Margins{}, vtui.AlignFill)
	vbox.Apply()
	w.SetFocusedItem(w.table)
	w.updateButtons()
	w.layout()
	return w
}

func label(width int) *vtui.Text { return vtui.NewLabel(0, 0, strings.Repeat(" ", width), nil) }

func (w *SearchResultsWindow) addSearchHeader(root, mask, text string, options Options) {
	flags := []string{}
	for _, flag := range []struct {
		set bool
		key string
	}{
		{options.CaseSensitive, "FindFile.CaseSensitive"}, {options.WholeWords, "FindFile.WholeWords"},
		{options.Regex, "FindFile.Regexp"}, {options.NotContaining, "FindFile.NotContaining"},
		{options.FindFolders, "FindFile.Folders"}, {options.FindSymlinks, "FindFile.Symlinks"},
		{len(options.SelectedFolders) > 0, "FindFile.SelectedFolders"},
	} {
		if flag.set {
			flags = append(flags, strings.ReplaceAll(i18n.Msg(flag.key), "&", ""))
		}
	}
	lines := []string{
		i18n.Msg("FindFile.Root") + " " + root,
		i18n.Msg("FindFile.MaskSummary") + " " + mask,
		i18n.Msg("FindFile.TextSummary") + " " + text,
	}
	optionsText := i18n.Msg("FindFile.OptionsSummary") + " " + strings.Join(flags, ", ")
	if len(flags) == 0 {
		optionsText += i18n.Msg("FindFile.DefaultOptions")
	}
	w.headerLines = append(lines, optionsText)
	lines = append(lines, optionsText, "")
	for i, line := range lines {
		l := label(74)
		l.SetPosition(w.X1+2, w.Y1+2+i, w.X2-2, w.Y1+2+i)
		l.SetText(plainLabel(runewidth.Truncate(line, 74, "...")))
		w.AddItem(l)
		w.header = append(w.header, l)
	}
	w.layout()
}

func (w *SearchResultsWindow) contentWidth() int {
	return max(1, w.X2-w.X1-3)
}

// layout derives every control's bounds from the current window rectangle.
// The table consumes the flexible space; the action row stays at the bottom.
func (w *SearchResultsWindow) layout() {
	width := w.contentWidth()
	x1, x2 := w.X1+2, w.X2-2
	tableY := w.Y1 + 3
	tableBottom := w.Y2 - 4
	if w.current != nil {
		tableY = w.Y1 + 12
		if len(w.request) > 0 {
			tableY = w.Y1 + w.requestBottom + 6
			for _, control := range w.request {
				right := w.X1 + control.x2
				if control.x2 >= 74 {
					right = x2
				}
				control.element.SetPosition(w.X1+control.x1, w.Y1+control.y1, right, w.Y1+control.y2)
			}
		}
		w.current.SetPosition(x1, tableY-3, x2, tableY-3)
		percentageRight := x2
		stopWidth := runewidth.StringWidth(strings.ReplaceAll(w.pauseButton.GetText(), "&", ""))
		stopX := x2 - stopWidth + 1
		w.pauseButton.SetPosition(stopX, tableY-2, x2, tableY-2)
		if w.pauseButton.IsVisible() {
			percentageRight = stopX - 2
		}
		w.bar.SetPosition(x1, tableY-2, percentageRight-5, tableY-2)
		w.percentage.SetPosition(percentageRight-3, tableY-2, percentageRight, tableY-2)
		w.scanningSeparator.SetPosition(x1, tableY-5, x2, tableY-5)
		w.current.SetText(plainLabel(runewidth.Truncate(w.currentText, width, "...")))
		counts := " " + runewidth.Truncate(w.statusText, max(1, width-2), "...") + " "
		countWidth := runewidth.StringWidth(counts)
		countX := x1 + (width-countWidth)/2
		w.status.SetPosition(countX, tableY-1, countX+countWidth-1, tableY-1)
		w.status.SetText(plainLabel(counts))
	}
	if len(w.header) == 5 {
		lines := append([]string(nil), w.headerLines...)
		first, rest := lines[3], ""
		if runewidth.StringWidth(first) > width {
			first = runewidth.Truncate(first, width, "")
			if at := strings.LastIndex(first, " "); at > 0 {
				first = first[:at]
			}
			rest = strings.TrimSpace(strings.TrimPrefix(lines[3], first))
		}
		lines[3] = first
		lines = append(lines, rest)
		for i, label := range w.header {
			label.SetPosition(x1, w.Y1+2+i, x2, w.Y1+2+i)
			label.SetText(plainLabel(runewidth.Truncate(lines[i], width, "...")))
		}
	}
	w.tableSeparator.SetPosition(x1, tableY-1, x2, tableY-1)
	w.table.SetPosition(x1, tableY, x2, max(tableY, tableBottom))
	w.buttons.SetPosition(x1, w.Y2-2, x2, w.Y2-2)
	w.buttons.Apply()
}

func (w *SearchResultsWindow) Show(scr *vtui.ScreenBuf) {
	w.layout()
	if w.bar != nil && !w.progress.DirectoryTotalKnown {
		// Show normally makes controls visible again. Unknown progress must
		// stay hidden even during a redraw, especially for remote searches.
		w.bar.SetVisible(false)
		w.percentage.SetVisible(false)
		w.bar.Lock()
		w.percentage.Lock()
		defer w.bar.Unlock()
		defer w.percentage.Unlock()
	}
	// vtui dims disabled controls (edit text twice). Draw the submitted
	// request with its normal theme colors, restoring the input lock before
	// returning to event processing on this same UI thread.
	for _, control := range w.request {
		control.element.SetDisabled(false)
	}
	defer func() {
		for _, control := range w.request {
			control.element.SetDisabled(true)
		}
	}()
	w.Window.Show(scr)
}

func (w *SearchResultsWindow) GetChildren() []vtui.UIElement {
	children := make([]vtui.UIElement, 0, len(w.Window.GetChildren()))
	for _, child := range w.Window.GetChildren() {
		if button, ok := child.(*vtui.Button); ok && button.IsLocked() && !button.IsVisible() {
			continue
		}
		if child == w.pauseButton && !w.pauseButton.IsVisible() {
			continue
		}
		children = append(children, child)
	}
	return children
}

func (w *SearchResultsWindow) ResizeConsole(width, height int) {
	if len(w.request) == 0 {
		w.Window.ResizeConsole(width, height)
		return
	}
	// The original dialog's viewport remembers its pre-search content size.
	// Keep the expanded bounds instead of reverting to that size on resize.
	w.ChangeSize(min(w.X2-w.X1+1, width), min(w.Y2-w.Y1+1, height-1))
	w.BaseWindow.ResizeConsole(width, height-1)
	w.layout()
}

func (w *SearchResultsWindow) appendRows(rows []vfs.FoundEntry) {
	if len(rows) == 0 {
		return
	}
	top, selection := w.table.TopPos, w.table.SelectPos
	w.found = append(w.found, rows...)
	w.table.SetRowCount(len(w.found))
	w.table.TopPos, w.table.SelectPos = top, selection
	w.updateButtons()
}

func (w *SearchResultsWindow) updateButtons() {
	for _, b := range w.fileButtons {
		b.SetDisabled(len(w.found) == 0)
	}
	if w.pauseButton != nil {
		caption := "FindFile.BtnPause"
		if w.paused {
			caption = "FindFile.BtnResume"
		}
		w.pauseButton.SetText(i18n.Msg(caption))
		if !w.running && w.GetFocusedItem() == w.pauseButton {
			w.SetFocusedItem(w.table)
		}
		w.pauseButton.SetVisible(w.running)
		w.pauseButton.SetDisabled(!w.running || w.stopping)
	}
}

func (w *SearchResultsWindow) refreshState(err error) {
	if w.current == nil {
		return
	}
	w.currentText = i18n.Msg("FindFile.Scanning") + " " + w.progress.Path
	w.current.SetText(plainLabel(runewidth.Truncate(w.currentText, w.contentWidth(), "...")))
	titleKey := "FindFile.SearchingTitle"
	switch {
	case w.stopping || errors.Is(err, context.Canceled):
		titleKey = "FindFile.Stopped"
		w.currentText = i18n.Msg("FindFile.StoppedPath") + " " + w.progress.Path
	case w.running && w.paused:
		titleKey = "FindFile.PausedTitle"
		w.currentText = i18n.Msg("FindFile.PausedPath") + " " + w.progress.Path
	case w.running:
	case err != nil:
		titleKey = "FindFile.FailedTitle"
	default:
		titleKey = "FindFile.Completed"
	}
	w.SetTitle(strings.TrimSpace(i18n.Msg(titleKey)))
	if !w.running && !w.stopping && !errors.Is(err, context.Canceled) {
		if err != nil {
			w.currentText = fmt.Sprintf(i18n.Msg("FindFile.Failed"), err)
		} else {
			w.currentText = i18n.Msg("FindFile.Scanned") + " " + w.searchScope
		}
	}
	count := int64(len(w.found))
	if w.progress.Found > count {
		count = w.progress.Found
	}
	line := fmt.Sprintf(i18n.Msg("FindFile.FoundCount"), count)
	if w.progress.Scanned > 0 {
		line += " | " + fmt.Sprintf(i18n.Msg("FindFile.ScannedCount"), w.progress.Scanned)
	}
	if !w.running && !w.stopping && err == nil && len(w.found) == 0 {
		line += " | " + i18n.Msg("FindFile.NoResults")
	}
	known := w.progress.DirectoryTotalKnown
	percent := 0
	if known && w.progress.TotalDirs > 0 {
		percent = int(w.progress.CompletedDirs * 100 / w.progress.TotalDirs)
	}
	if percent >= 100 {
		percent = 99
	}
	if !w.running && !w.stopping && err == nil {
		percent = 100
	}
	w.bar.SetVisible(known)
	w.percentage.SetVisible(known)
	w.bar.SetPercent(percent)
	w.percentage.SetText(fmt.Sprintf("%3d%%", percent))
	w.statusText = line
	w.updateButtons()
	w.layout()
}

// TogglePause freezes the worker at its next checkpoint, retaining its state
// and results so the same search can resume without starting over.
func (w *SearchResultsWindow) TogglePause() {
	if !w.running || w.stopping || w.closed {
		return
	}
	w.paused = !w.paused
	w.pauseGate.setPaused(w.paused)
	vtui.DebugLog("FIND: paused=%v path=%q", w.paused, w.progress.Path)
	w.refreshState(nil)
	vtui.FrameManager.Redraw()
}

// Stop cancels work while leaving this window and its accumulated hits open.
func (w *SearchResultsWindow) Stop() {
	if !w.running || w.stopping || w.closed {
		return
	}
	w.stopping = true
	vtui.DebugLog("FIND: stopping search")
	if w.cancel != nil {
		w.cancel()
	}
	// The worker may still be draining a remote response. Flush everything
	// already published before cancellation without waiting for that drain.
	w.updates.mu.Lock()
	rows, p := w.updates.rows, w.updates.progress
	w.updates.rows = nil
	w.updates.mu.Unlock()
	w.appendRows(rows)
	w.progress = p
	w.refreshState(nil)
}

func (w *SearchResultsWindow) Running() bool { return w.running && !w.stopping && !w.closed }

func (w *SearchResultsWindow) RowCount() int { return len(w.found) }

func (w *SearchResultsWindow) GetCellText(row, col int) string {
	if row < 0 || row >= len(w.found) {
		return ""
	}
	ff := w.found[row]
	switch col {
	case 0:
		return ff.Item.Name
	case 1:
		return w.vfs.Dir(ff.Path)
	case 2:
		if ff.Item.IsDir {
			return "<DIR>"
		}
		return fmt.Sprintf("%d", ff.Item.Size)
	}
	return ""
}

func (w *SearchResultsWindow) selected() (vfs.FoundEntry, bool) {
	idx := w.table.SelectPos
	if idx < 0 || idx >= len(w.found) {
		return vfs.FoundEntry{}, false
	}
	return w.found[idx], true
}

func (w *SearchResultsWindow) goTo() {
	if hit, ok := w.selected(); ok && w.host.GoTo != nil {
		w.Close()
		w.host.GoTo(w.vfs, hit.Path)
	}
}

func (w *SearchResultsWindow) sendToPanel() bool {
	if len(w.found) > 0 && w.host.Panel != nil {
		rows := append([]vfs.FoundEntry(nil), w.found...)
		w.Close()
		w.host.Panel(w.vfs, rows)
	}
	return true
}

func (w *SearchResultsWindow) ProcessKey(e *vtinput.InputEvent) bool {
	if e.KeyDown {
		switch e.VirtualKeyCode {
		case vtinput.VK_F3:
			return w.HandleCommand(appcmd.CmView, nil)
		case vtinput.VK_F4:
			return w.HandleCommand(appcmd.CmEdit, nil)
		case vtinput.VK_F6:
			return w.sendToPanel()
		}
	}
	return w.Window.ProcessKey(e)
}

func (w *SearchResultsWindow) HandleCommand(cmd int, args any) bool {
	if hit, ok := w.selected(); ok {
		switch cmd {
		case appcmd.CmView:
			if w.host.View != nil {
				w.host.View(w.vfs, hit.Path)
			}
			return true
		case appcmd.CmEdit:
			if w.host.Edit != nil {
				w.host.Edit(w.vfs, hit.Path)
			}
			return true
		}
	}
	return w.Window.HandleCommand(cmd, args)
}

func (w *SearchResultsWindow) GetKeyLabels() *vtui.KeySet {
	caption := func(key string) string { return strings.ReplaceAll(i18n.Msg(key), "&", "") }
	return &vtui.KeySet{Normal: vtui.KeyBarLabels{"", "", caption("FindFile.BtnView"), caption("FindFile.BtnEdit"), "", caption("FindFile.BtnPanel"), "", "", "", caption("FindFile.BtnClose"), "", ""}}
}

func plainLabel(text string) string { return strings.ReplaceAll(text, "&", "&&") }
