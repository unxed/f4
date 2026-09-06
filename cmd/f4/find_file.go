package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charlievieth/strcase"
	"github.com/coregx/coregex"
	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type FoundFile struct {
	Path string
	Item vfs.VFSItem
}

// FindFileOptions are the portable Find File options. Archive and alternate
// stream searches are intentionally not presented here: those are backend
// capabilities, not properties a generic VFS can promise. Unsupported remote
// combinations fall back to the ordinary VFS walk so the result stays correct.
type FindFileOptions struct {
	CaseSensitive bool
	WholeWords    bool
	Regex         bool
	NotContaining bool
	FindFolders   bool
	FindSymlinks  bool
}

func (o FindFileOptions) usesDefaultSearchEngine() bool {
	return !o.WholeWords && !o.NotContaining && !o.FindFolders && !o.FindSymlinks
}

// padLabelTo returns s padded with trailing spaces to at least w display
// cells. Used to force a vtui label to open at its full future width —
// the widget freezes its width at construction time (label.go: X2 = X1
// + runewidth.StringWidth(cleanText) - 1), and SetText later cannot
// grow it back. A label built from short initial text truncates every
// longer SetText silently to the first-N chars, which was visible in
// the find dialog as "Scanning: /li" — three characters of a wire path
// that would otherwise say the full one. (An existing padLabel in
// attributes_dialog is fixed at 12 columns; this takes an explicit
// target so a caller can size a label to the dialog width it lives in.)
func padLabelTo(s string, w int) string {
	pad := w - runewidth.StringWidth(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// ExecuteFindFile initiates a background search and displays a progress dialog.
func ExecuteFindFile(pf *PanelsFrame, v vfs.VFS, startDir, mask, text string, options FindFileOptions) {
	dlg := vtui.NewCenteredDialog(60, 9, Msg("FindFile.SearchingTitle"))
	dlg.AttentionSuppressed = true

	// lblMask and lblDir sit inside a 56-column vbox; lblFound shares a
	// row with the Cancel button. Pad each to its future width so a
	// longer SetText later shows in full — see padLabelTo above for
	// the underlying vtui gotcha. lblFound holds
	// "Found: 999999 (scanned 999999999)" and then some, and AlignRight
	// on the button anchors it at the far end.
	lblMask := vtui.NewLabel(0, 0, padLabelTo(Msg("FindFile.MaskPrompt")+" "+mask, 56), nil)
	lblDir := vtui.NewLabel(0, 0, padLabelTo(Msg("FindFile.Scanning")+" ...", 56), nil)
	lblFound := vtui.NewLabel(0, 0, padLabelTo(fmt.Sprintf(Msg("FindFile.FoundCount"), 0), 40), nil)

	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	dlg.AddItem(lblMask)
	dlg.AddItem(lblDir)
	dlg.AddItem(lblFound)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 60-4, 9-4)
	vbox.Add(lblMask, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(lblDir, vtui.Margins{Top: 1}, vtui.AlignLeft)

	hbox := vtui.NewHBoxLayout(0, 0, 60-4, 1)
	hbox.Add(lblFound, vtui.Margins{}, vtui.AlignLeft)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignRight)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	var taskCtx *vtui.TaskContext
	btnCancel.OnClick = func() {
		dlg.SetExitCode(1)
	}
	dlg.OnResult = func(code int) {
		if taskCtx != nil {
			taskCtx.Cancel()
		}
	}

	// Since we are inside an action handler (UI thread), we can push directly
	vtui.FrameManager.AddScreenHeadless(dlg)

	taskCtx = vtui.RunAsync(func(ctx *vtui.TaskContext) {
		// Parse masks (e.g. "*.go, *.txt")
		masks := strings.Split(mask, ",")
		for i := range masks {
			masks[i] = strings.TrimSpace(masks[i])
			// Far compatibility: *.* translates to * in filepath.Match logic
			masks[i] = strings.ReplaceAll(masks[i], "*.*", "*")
		}
		if len(masks) == 0 || mask == "" {
			masks = []string{"*"}
		}

		matcher, matcherErr := newFindTextMatcher(text, options)
		if matcherErr != nil {
			ctx.RunOnUI(func() {
				dlg.Close()
				vtui.ShowMessage(" Find File ", fmt.Sprintf("Invalid search pattern:\n%v", matcherErr), []string{"&Ok"})
			})
			return
		}
		var found []FoundFile
		var lastUpdate time.Time // Used for throttling UI redraws
		// A remote finder that supports progress reports intermediate
		// counters before we get the entries themselves: what has been
		// scanned so far and how many have matched. During the walk
		// found is still empty on our side, so the "Found:" line has to
		// prefer the reported number until the final answer lands.
		// remotePath is what a remote progress last reported as the
		// head of the walk; the "final" updateUI at the end otherwise
		// reverts the label to startDir and loses the last frame.
		var remoteFound int64
		var remoteScanned int64
		var remotePath string

		updateUI := func(dir string, force bool) {
			now := time.Now()
			if force || now.Sub(lastUpdate) > 50*time.Millisecond {
				lastUpdate = now
				currentCount := int64(len(found))
				if remoteFound > currentCount {
					currentCount = remoteFound
				}
				showDir := dir
				if remotePath != "" {
					showDir = remotePath
				}
				displayDir := runewidth.Truncate(showDir, 56, "...")
				// A remote job reports how many entries the walk has
				// visited; showing it next to "Found" gives the user
				// a sense of progress even when the pattern is rare
				// enough that the "Found" counter barely moves. The
				// local walk does not report scanned separately (its
				// pace is dominated by the ReadDir round trips it
				// makes), so the parenthetical is only shown when a
				// remote finder actually supplied a number.
				foundText := fmt.Sprintf(Msg("FindFile.FoundCount"), currentCount)
				if remoteScanned > 0 {
					foundText = fmt.Sprintf("%s (scanned %d)", foundText, remoteScanned)
				}
				ctx.RunOnUI(func() {
					lblDir.SetText(Msg("FindFile.Scanning") + " " + displayDir)
					lblFound.SetText(foundText)
					vtui.FrameManager.Redraw()
				})
			}
		}

		var walk func(dir string) error
		walk = func(dir string) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			updateUI(dir, false)

			return v.ReadDir(ctx.Context, dir, func(chunk []vfs.VFSItem) {
				for _, item := range chunk {
					if ctx.Err() != nil {
						return
					}
					if item.Name == ".." {
						continue
					}

					itemPath := v.Join(dir, item.Name)
					if item.IsSymlink {
						if !options.FindSymlinks {
							continue
						}
						// Symlinks are search leaves, even when they point to a
						// directory. Following them can revisit the search root.
						item.IsDir = false
					}

					matched := false
					for _, m := range masks {
						if m == "" {
							continue
						}
						match, _ := filepath.Match(m, item.Name)
						if match {
							matched = true
							break
						}
					}

					if item.IsDir {
						if options.FindFolders && text == "" && matched {
							found = append(found, FoundFile{Path: itemPath, Item: item})
							updateUI(dir, false)
						}
						_ = walk(itemPath) // Ignore permissions/read errors to continue walking
						continue
					}
					if !matched {
						continue
					}

					if matcher != nil && !fileContainsTextWithMatcher(ctx.Context, v, itemPath, matcher) {
						continue
					}

					found = append(found, FoundFile{Path: itemPath, Item: item})
					updateUI(dir, false)
				}
			})
		}

		// A file system that can search its own tree does the walking there:
		// one request instead of a round trip per directory, and a remote
		// grep instead of downloading every candidate only to reject it.
		var err error
		searched := false
		if finder, ok := v.(vfs.FileFinder); ok && options.usesDefaultSearchEngine() {
			updateUI(startDir, true)
			hits, findErr := finder.FindFiles(ctx.Context, startDir, vfs.FindQuery{
				Masks:        masks,
				Text:         text,
				IgnoreCase:   !options.CaseSensitive,
				Regex:        options.Regex,
				FindFolders:  options.FindFolders,
				FindSymlinks: options.FindSymlinks,
				// A remote finder that supports progress reports the
				// last path it visited and running counters. Force the
				// redraw (helper's own P cadence is 300 ms, well above
				// the client-side throttle, so nothing to save here)
				// and remember the path so the final updateUI at the
				// end does not revert the label back to startDir.
				Progress: func(p vfs.FindProgress) {
					remoteFound = p.Found
					remoteScanned = p.Scanned
					if p.Path != "" {
						remotePath = p.Path
					}
					updateUI(p.Path, true)
				},
			})
			if findErr == nil {
				for _, hit := range hits {
					found = append(found, FoundFile{Path: hit.Path, Item: hit.Item})
				}
				searched = true
			} else if ctx.Err() == nil {
				vtui.DebugLog("FIND: remote search unavailable, walking instead: %v", findErr)
			}
		}
		if !searched {
			err = walk(startDir)
		}
		updateUI(startDir, true) // Guarantee final state rendering

		ctx.RunOnUI(func() {
			dlg.Close()
			if err != nil && err != context.Canceled {
				vtui.ShowMessage(" Error ", fmt.Sprintf("Search failed:\n%v", err), []string{"&Ok"})
			} else if len(found) == 0 {
				vtui.ShowMessage(" Find File ", "File not found.", []string{"&Ok"})
			} else {
				ShowSearchResults(pf, v, found)
			}
		})
	})
}

type findTextMatcher struct {
	options FindFileOptions
	needle  string
	regex   *coregex.Regex
}

// newFindTextMatcher prepares the content filter for one Find File run. A
// regular expression is handed to coregex, the engine the viewer and the
// editor already search with, so a pattern that selects a file here selects
// the same text once that file is opened. A literal pattern stays out of the
// engine: strcase folds case while it scans, which is the same split the
// editor makes between its regex and its plain-text search paths.
func newFindTextMatcher(pattern string, options FindFileOptions) (*findTextMatcher, error) {
	if pattern == "" {
		return nil, nil
	}
	m := &findTextMatcher{options: options, needle: pattern}
	if options.Regex {
		expression := pattern
		if !options.CaseSensitive {
			expression = "(?i:" + expression + ")"
		}
		compiled, err := coregex.Compile(expression)
		if err != nil {
			return nil, err
		}
		m.regex = compiled
	}
	return m, nil
}

func (m *findTextMatcher) matches(data []byte) bool {
	if m == nil {
		return true
	}
	return m.hasMatch(data) != m.options.NotContaining
}

func (m *findTextMatcher) hasMatch(data []byte) bool {
	if m == nil {
		return false
	}
	if m.regex != nil {
		// coregex matches bytes, so the chunk does not have to be copied
		// into a string first. Without whole-word filtering the first hit
		// already answers the question and Match stops there instead of
		// walking the rest of a 128K chunk collecting offsets nobody reads.
		if !m.options.WholeWords {
			return m.regex.Match(data)
		}
		for _, span := range m.regex.FindAllIndex(data, -1) {
			if findTextWholeWord(data, span[0], span[1]) {
				return true
			}
		}
		return false
	}
	// Literal search. Offsets have to index the chunk itself, otherwise the
	// whole-word check inspects the wrong neighbours — which rules out
	// lowercasing the chunk first, since folding can change byte lengths
	// ("İ" becomes two runes) and shift everything behind it. strcase folds
	// as it scans and reports offsets into the original text; CutPrefix then
	// gives back the length the match really occupied there.
	haystack := string(data)
	index := strings.Index
	if !m.options.CaseSensitive {
		index = strcase.Index
	}
	for from := 0; from <= len(haystack); {
		at := index(haystack[from:], m.needle)
		if at < 0 {
			return false
		}
		at += from
		end := at + len(m.needle)
		if !m.options.CaseSensitive {
			rest, ok := strcase.CutPrefix(haystack[at:], m.needle)
			if !ok {
				from = at + 1
				continue
			}
			end = len(haystack) - len(rest)
		}
		if !m.options.WholeWords || findTextWholeWord(data, at, end) {
			return true
		}
		from = at + 1
	}
	return false
}

// findTextWholeWord reports whether the match spanning [start, end) is
// delimited by non-word runes. This stays a hand-rolled check instead of
// wrapping the pattern in \b: Go's \b is defined over the ASCII \w class, so
// a Cyrillic or Greek word has no boundary anywhere in it and whole-word
// search would answer "no match" for every non-Latin query.
func findTextWholeWord(data []byte, start, end int) bool {
	wordBefore := false
	if start > 0 {
		r, _ := utf8.DecodeLastRune(data[:start])
		wordBefore = isFindTextWordRune(r)
	}
	wordAfter := false
	if end < len(data) {
		r, _ := utf8.DecodeRune(data[end:])
		wordAfter = isFindTextWordRune(r)
	}
	return !wordBefore && !wordAfter
}

func isFindTextWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func fileContainsText(ctx context.Context, v vfs.VFS, path string, textLower string) bool {
	matcher, err := newFindTextMatcher(textLower, FindFileOptions{})
	if err != nil {
		return false
	}
	return fileContainsTextWithMatcher(ctx, v, path, matcher)
}

// fileContainsTextWithMatcher scans in chunks while retaining enough of the
// previous chunk to catch matches crossing a read boundary. Regex searches use
// a larger carry window because a useful expression may consume more than one
// literal token around the boundary.
func fileContainsTextWithMatcher(ctx context.Context, v vfs.VFS, path string, matcher *findTextMatcher) bool {
	if matcher == nil {
		return true
	}
	f, err := v.Open(ctx, path)
	if err != nil {
		return false
	}
	defer f.Close()

	overlap := len(matcher.needle) + 4
	if matcher.regex != nil && overlap < 4096 {
		overlap = 4096
	}
	if overlap < 1 {
		overlap = 1
	}
	buf := make([]byte, 128*1024)
	var tail []byte
	for {
		if ctx.Err() != nil {
			return false
		}
		n, readErr := f.Read(ctx, buf)
		if n > 0 {
			data := make([]byte, 0, len(tail)+n)
			data = append(data, tail...)
			data = append(data, buf[:n]...)
			if matcher.hasMatch(data) {
				return !matcher.options.NotContaining
			}
			if len(data) > overlap {
				tail = append(tail[:0], data[len(data)-overlap:]...)
			} else {
				tail = append(tail[:0], data...)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return matcher.options.NotContaining
			}
			return false
		}
	}
}

type foundFileRow struct {
	ff FoundFile
	v  vfs.VFS
}

func (r foundFileRow) GetCellText(col int) string {
	switch col {
	case 0:
		return r.ff.Item.Name
	case 1:
		if r.ff.Item.IsDir {
			return "<DIR>"
		}
		return fmt.Sprintf("%d", r.ff.Item.Size)
	case 2:
		return r.v.Dir(r.ff.Path)
	}
	return ""
}

type SearchResultsWindow struct {
	*vtui.Window
	table *vtui.Table
	found []FoundFile
	vfs   vfs.VFS
	pf    *PanelsFrame
}

func (srw *SearchResultsWindow) ProcessKey(e *vtinput.InputEvent) bool {
	if !e.KeyDown {
		return srw.Window.ProcessKey(e)
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_F5:
		return srw.sendToTempPanel()
	case vtinput.VK_F3:
		return srw.HandleCommand(CmView, nil)
	case vtinput.VK_F4:
		return srw.HandleCommand(CmEdit, nil)
	}

	return srw.Window.ProcessKey(e)
}

func (srw *SearchResultsWindow) HandleCommand(cmd int, args any) bool {
	idx := srw.table.SelectPos
	if idx >= 0 && idx < len(srw.found) {
		ff := srw.found[idx]
		switch cmd {
		case CmView:
			actionOpenViewer(srw.pf, srw.vfs, ff.Path)
			return true
		case CmEdit:
			actionOpenEditor(srw.pf, srw.vfs, ff.Path)
			return true
		}
	}
	return srw.Window.HandleCommand(cmd, args)
}

func (srw *SearchResultsWindow) GetKeyLabels() *vtui.KeySet {
	return &vtui.KeySet{
		Normal: vtui.KeyBarLabels{
			"", "", "View", "Edit", Msg("FindFile.BtnPanel"), "", "", "", "", "Quit", "", "",
		},
	}
}

func (srw *SearchResultsWindow) sendToTempPanel() bool {
	if srw == nil || srw.pf == nil || len(srw.found) == 0 {
		return true
	}
	fsp := srw.pf.getActivePanel()
	if fsp == nil {
		return true
	}
	slot := globalTempPanelStore.searchSlot()
	globalTempPanelStore.replaceWithSearchResults(slot, srw.vfs, srw.found)
	srw.Close()
	srw.pf.switchToVFS(fsp, newTempPanelVFS(nil, globalTempPanelStore, slot))
	return true
}

func ShowSearchResults(pf *PanelsFrame, v vfs.VFS, found []FoundFile) {
	dlgW, dlgH := 76, 20
	baseDlg := vtui.NewCenteredDialog(dlgW, dlgH, Msg("FindFile.SearchResultsTitle"))

	srw := &SearchResultsWindow{
		Window: baseDlg,
		found:  found,
		vfs:    v,
		pf:     pf,
	}

	cols := []vtui.TableColumn{
		{Title: "Name", Width: 20},
		{Title: "Size", Width: 10, Alignment: vtui.AlignRight},
		{Title: "Path", Width: 38},
	}
	srw.table = vtui.NewTable(0, 0, 72, 12, cols)
	srw.table.SetOwner(srw) // Explicit owner for command routing
	srw.table.ShowScrollBar = true

	rows := make([]vtui.TableRow, len(found))
	for i, ff := range found {
		rows[i] = foundFileRow{ff, v}
	}
	srw.table.SetRows(rows)

	btnGo := vtui.NewButton(0, 0, Msg("FindFile.BtnGoTo"))
	btnGo.SetOwner(srw)
	btnPanel := vtui.NewButton(0, 0, Msg("FindFile.BtnPanel"))
	btnPanel.SetOwner(srw)
	btnView := vtui.NewButton(0, 0, Msg("FindFile.BtnView"))
	btnView.SetOwner(srw)
	btnEdit := vtui.NewButton(0, 0, Msg("FindFile.BtnEdit"))
	btnEdit.SetOwner(srw)
	btnClose := vtui.NewButton(0, 0, Msg("FindFile.BtnClose"))
	btnClose.SetOwner(srw)

	btnGo.IsDefault = true

	doGoTo := func() {
		idx := srw.table.SelectPos
		if idx >= 0 && idx < len(found) {
			ff := found[idx]
			srw.Close()
			if fsp := pf.getActivePanel(); fsp != nil {
				fsp.vfs.SetPath(v.Dir(ff.Path))
				fsp.pendingSelection = v.Base(ff.Path)
				fsp.ReadDirectory()
				pf.showPanels = true
			}
		}
	}

	srw.table.OnAction = func(idx int) { doGoTo() }
	btnGo.OnClick = doGoTo
	btnPanel.OnClick = func() { srw.sendToTempPanel() }
	btnClose.OnClick = func() { srw.Close() }
	btnView.OnClick = func() { srw.HandleCommand(CmView, nil) }
	btnEdit.OnClick = func() { srw.HandleCommand(CmEdit, nil) }

	vbox := vtui.NewVBoxLayout(srw.X1+2, srw.Y1+2, dlgW-4, dlgH-4)
	vbox.Add(srw.table, vtui.Margins{Bottom: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, dlgW-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnGo, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnPanel, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnView, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnEdit, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnClose, vtui.Margins{}, vtui.AlignTop)

	vbox.Add(hbox, vtui.Margins{}, vtui.AlignFill)
	vbox.Apply()

	srw.AddItem(srw.table)
	srw.AddItem(btnGo)
	srw.AddItem(btnPanel)
	srw.AddItem(btnView)
	srw.AddItem(btnEdit)
	srw.AddItem(btnClose)

	vtui.FrameManager.Push(srw)
}
