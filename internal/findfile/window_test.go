package findfile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/appcmd"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type streamVFS struct {
	vfs.VFS
	search func(context.Context, vfs.FindQuery, func(vfs.FoundEntry)) error
}

func (v *streamVFS) FindFilesStream(ctx context.Context, _ string, q vfs.FindQuery, hit func(vfs.FoundEntry)) error {
	return v.search(ctx, q, hit)
}

func setupUI(t *testing.T) {
	t.Helper()
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.SetDefaultPalette()
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	vtui.FrameManager.Init(screen)
}

func TestResultsColumnsPutSizeLast(t *testing.T) {
	setupUI(t)
	v := vfs.NewOSVFS(t.TempDir())
	w := newWindow(v, Host{}, false)
	w.appendRows([]vfs.FoundEntry{hit(v, "file.txt")})
	want := []string{"file.txt", v.GetPath(), "1"}
	for col, text := range want {
		if got := w.GetCellText(0, col); got != text {
			t.Errorf("column %d: got %q, want %q", col, got, text)
		}
	}
	if w.table.Columns[1].Title != i18n.Msg("FindFile.ColPath") || w.table.Columns[2].Title != i18n.Msg("FindFile.ColSize") {
		t.Fatal("column headers do not match Name, Path, Size order")
	}
	if w.table.Columns[1].Width != 0 || w.table.Columns[2].Alignment != vtui.AlignRight {
		t.Fatal("path must fill available space and size must align right")
	}
}

func TestResultsF5ZoomAndF6Panel(t *testing.T) {
	setupUI(t)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(120, 45)
	vtui.FrameManager.Init(scr)
	base := vfs.NewOSVFS(t.TempDir())
	panels := 0
	w := Show(base, []vfs.FoundEntry{hit(base, "match.txt")}, Host{Panel: func(_ vfs.VFS, entries []vfs.FoundEntry) {
		panels++
		if len(entries) != 1 || entries[0].Item.Name != "match.txt" {
			t.Fatal("Panel received wrong results")
		}
	}})
	key := func(code uint16) { w.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: code}) }
	key(vtinput.VK_F5)
	if w.SavedBounds == nil || panels != 0 || w.IsDone() {
		t.Fatal("F5 must maximize without sending results to Panel")
	}
	key(vtinput.VK_F5)
	if w.SavedBounds != nil {
		t.Fatal("F5 must restore the dialog")
	}
	labels := w.GetKeyLabels().Normal
	if labels[4] != "" || labels[5] != strings.ReplaceAll(i18n.Msg("FindFile.BtnPanel"), "&", "") {
		t.Fatal("key bar did not move Panel from F5 to F6")
	}
	key(vtinput.VK_F6)
	if panels != 1 || !w.IsDone() {
		t.Fatal("F6 did not send results to Panel and close the dialog")
	}
}

func TestProgressPercentageAndCountersLayout(t *testing.T) {
	setupUI(t)
	w := newWindow(vfs.NewOSVFS(t.TempDir()), Host{}, true)
	w.running = true
	w.addSearchHeader("root", "*", "", Options{})
	w.progress = vfs.FindProgress{Path: "root/sub", Found: 3, Scanned: 10, DirectoryTotalKnown: true, TotalDirs: 4, CompletedDirs: 1}
	w.refreshState(nil)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	w.Show(scr)
	want := fmt.Sprintf(i18n.Msg("FindFile.FoundCount"), 3) + " | " + fmt.Sprintf(i18n.Msg("FindFile.ScannedCount"), 10)
	if strings.TrimSpace(w.status.GetText()) != want {
		t.Fatalf("separator caption=%q, want counters only %q", w.status.GetText(), want)
	}
	centerOffset := w.status.X1 + w.status.X2 - w.current.X1 - w.current.X2
	if w.status.Y1 != w.tableSeparator.Y1 || centerOffset < -1 || centerOffset > 1 {
		t.Fatal("counters are not centered on the table separator")
	}
	if strings.TrimSpace(w.percentage.GetText()) != "25%" || w.percentage.X2 != w.pauseButton.X1-2 || w.percentage.X1 <= w.bar.X2 || w.percentage.Y1 != w.bar.Y1 {
		t.Fatal("percentage must appear to the right of the progress bar")
	}
	for x := w.current.X1; x <= w.current.X2; x++ {
		if cell := scr.GetCell(x, w.current.Y1-1); cell.Char != ' ' {
			t.Fatal("expected a blank line before scanning")
		}
	}
	if scr.GetCell(w.status.X1+1, w.status.Y1).Char != uint64([]rune(want)[0]) {
		t.Fatal("separator caption was not painted")
	}
	w.progress.DirectoryTotalKnown = false
	w.refreshState(nil)
	w.Show(scr)
	if w.bar.IsVisible() || w.percentage.IsVisible() {
		t.Fatal("unknown progress must not show a made-up percentage")
	}
	w.progress.DirectoryTotalKnown = true
	w.running = false
	w.refreshState(nil)
	w.Show(scr)
	if w.pauseButton.IsVisible() || w.percentage.X2 != w.current.X2 {
		t.Fatal("natural completion must hide Stop and reclaim its space")
	}
}

func TestFrozenRequestRetainsReadableThemeColors(t *testing.T) {
	setupUI(t)
	previous := append([]uint64(nil), vtui.Palette...)
	t.Cleanup(func() { vtui.Palette = previous })
	w := newWindow(vfs.NewOSVFS(t.TempDir()), Host{}, true)
	label := vtui.NewLabel(w.X1+2, w.Y1+2, "File mask:", nil)
	edit := vtui.NewEdit(w.X1+2, w.Y1+3, 74, ".gitignore")
	checkbox := vtui.NewCheckbox(w.X1+2, w.Y1+4, "Case sensitive", false)
	checkbox.State = 1
	for _, element := range []vtui.UIElement{label, edit, checkbox} {
		w.AddItem(element)
		x1, y1, x2, y2 := element.GetPosition()
		w.request = append(w.request, requestControl{
			element: element, x1: x1 - w.X1, y1: y1 - w.Y1, x2: x2 - w.X1, y2: y2 - w.Y1,
		})
	}
	w.requestBottom = 4
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	for _, offset := range []uint32{0, 0x101010} {
		vtui.Palette[vtui.ColDialogText] = vtui.SetRGBBoth(0, 0xdddddd-offset, 0x444444)
		vtui.Palette[vtui.ColDialogEdit] = vtui.SetRGBBoth(0, 0xdddddd-offset, 0x222222)
		want := make([]uint64, len(w.request))
		for i, control := range w.request {
			control.element.SetDisabled(false)
			control.element.Show(scr)
			x, y, _, _ := control.element.GetPosition()
			want[i] = scr.GetCell(x, y).Attributes
			control.element.SetDisabled(true)
		}
		w.Show(scr)
		for i, control := range w.request {
			x, y, _, _ := control.element.GetPosition()
			if got := scr.GetCell(x, y).Attributes; got != want[i] {
				t.Errorf("request control %d dimmed: got %#x, want %#x", i, got, want[i])
			}
			if !control.element.IsDisabled() || control.element.IsFocused() {
				t.Errorf("request control %d became interactive after rendering", i)
			}
		}
	}
}

func TestSearchExpandsSubmittedDialog(t *testing.T) {
	for _, screenHeight := range []int{25, 45} {
		t.Run(fmt.Sprint(screenHeight), func(t *testing.T) {
			setupUI(t)
			scr := vtui.NewSilentScreenBuf()
			scr.AllocBuf(80, screenHeight)
			vtui.FrameManager.Init(scr)
			dlg := vtui.NewCenteredDialog(78, 20, "Find File")
			mask := vtui.NewEdit(dlg.X1+2, dlg.Y1+3, 74, "*.txt")
			option := vtui.NewCheckbox(dlg.X1+2, dlg.Y1+14, "Case sensitive", false)
			option.State = 1
			dlg.AddItem(mask)
			dlg.AddItem(option)
			findButton := vtui.NewButton(dlg.X1+20, dlg.Y1+16, "Find")
			cancelButton := vtui.NewButton(dlg.X1+35, dlg.Y1+16, "Cancel")
			dlg.AddItem(findButton)
			dlg.AddItem(cancelButton)
			vtui.FrameManager.Push(dlg)
			originalHeight := dlg.Y2 - dlg.Y1 + 1
			base := vfs.NewOSVFS(t.TempDir())
			provider := &streamVFS{VFS: base, search: func(ctx context.Context, _ vfs.FindQuery, emit func(vfs.FoundEntry)) error {
				emit(hit(base, "first.txt"))
				<-ctx.Done()
				return ctx.Err()
			}}
			w := Expand(dlg, []vtui.UIElement{mask, option}, provider, base.GetPath(), mask.GetText(), "", Options{CaseSensitive: true}, Host{})
			w.Show(scr)
			if findButton.IsVisible() || cancelButton.IsVisible() || !findButton.IsDisabled() || !cancelButton.IsDisabled() {
				t.Fatal("Find and Cancel reappeared or remained interactive after search started")
			}
			for _, child := range w.GetChildren() {
				if child == findButton || child == cancelButton {
					t.Fatal("retired submit buttons remain in the exposed controls")
				}
			}
			if !w.pauseButton.IsVisible() || w.pauseButton.Y1 != w.bar.Y1 || w.pauseButton.X1 <= w.percentage.X2 || w.pauseButton.X2 != w.current.X2 {
				t.Fatal("Stop must be to the right of percentage while searching")
			}
			if w.Window != dlg || w.IsDone() {
				t.Fatal("submitted window was replaced or closed")
			}
			if w.Y2-w.Y1+1 <= originalHeight || w.Y2 >= screenHeight {
				t.Fatal("dialog did not expand within screen bounds")
			}
			if len(w.header) != 0 || mask.GetText() != "*.txt" || option.State != 1 {
				t.Fatal("request was duplicated or changed")
			}
			if !mask.IsDisabled() || !option.IsDisabled() || mask.Y1-w.Y1 != 3 || option.Y1-w.Y1 != w.requestBottom {
				t.Fatal("submitted controls did not stay frozen in their original positions")
			}
			if w.current.Y1 <= option.Y2 || w.table.Y1 <= w.bar.Y2 || w.table.Y2 >= w.buttons.Y1 {
				t.Fatal("new controls overlap the submitted request or action row")
			}
			if w.scanningSeparator.Y1 != w.current.Y1-2 || w.scanningSeparator.Y1 <= option.Y2 {
				t.Fatal("separator missing between request and scanning")
			}
			if w.tableSeparator.Y1 != w.table.Y1-1 || w.tableSeparator.Y1 <= w.bar.Y2 {
				t.Fatal("separator missing before results table")
			}
			if w.buttons.Y1 != w.table.Y2+2 {
				t.Fatal("expected one empty row before result buttons")
			}
			if w.table.Y2-w.table.Y1 < 1 {
				t.Fatal("80x25 dialog has no room for a result below the column headers")
			}
			boundsHeight := w.Y2 - w.Y1 + 1
			w.ResizeConsole(80, screenHeight)
			w.Show(scr)
			if w.Y2-w.Y1+1 != boundsHeight {
				t.Fatal("console resize reverted to the parameters dialog height")
			}
			drainUntil(t, func() bool { return w.RowCount() == 1 })
			if !w.Running() {
				t.Fatal("first result was delayed until completion")
			}
			w.Stop()
			drainUntil(t, func() bool { return !w.running })
			w.Show(scr)
			if w.pauseButton.IsVisible() || findButton.IsVisible() || cancelButton.IsVisible() {
				t.Fatal("completed search redraw restored retired buttons")
			}
			if w.RowCount() != 1 || mask.GetText() != "*.txt" {
				t.Fatal("stop lost results or original request")
			}
			w.Close()
		})
	}
}

func TestResultsWindowUsesResizedSpaceAndSupportsZoom(t *testing.T) {
	setupUI(t)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(120, 45)
	vtui.FrameManager.Init(scr)
	for _, live := range []bool{false, true} {
		w := newWindow(vfs.NewOSVFS(t.TempDir()), Host{}, live)
		if live {
			w.addSearchHeader(strings.Repeat("root/", 18), "*", "", Options{})
		}
		w.appendRows(make([]vfs.FoundEntry, 100))
		w.table.SelectPos, w.table.TopPos = 30, 25
		w.Show(scr)
		oldWidth, oldHeight := w.table.X2-w.table.X1, w.table.ViewHeight
		w.ChangeSize(100, 35)
		w.Show(scr)
		if w.table.X2-w.table.X1 <= oldWidth || w.table.ViewHeight <= oldHeight {
			t.Errorf("live=%v: table did not expand", live)
		}
		if w.table.SelectPos != 30 || w.table.TopPos != 25 {
			t.Errorf("live=%v: resizing changed selection or scroll", live)
		}
		if !w.ShowZoom {
			t.Fatal("maximize button disabled")
		}
		width, height := w.X2-w.X1, w.Y2-w.Y1
		w.ToggleZoom()
		w.Show(scr)
		if w.X2-w.X1 <= width || w.Y2-w.Y1 <= height || w.table.X2 != w.X2-2 || w.table.Y2 != w.Y2-4 {
			t.Errorf("live=%v: maximized list does not fill available space", live)
		}
		w.ToggleZoom()
		w.Show(scr)
		if w.X2-w.X1 != width || w.Y2-w.Y1 != height {
			t.Error("restore did not recover window bounds")
		}
	}
}

func drainUntil(t *testing.T, ready func() bool) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for !ready() {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timer.C:
			t.Fatal("search/UI operation timed out")
		}
	}
}

func hit(v vfs.VFS, name string) vfs.FoundEntry {
	return vfs.FoundEntry{Path: v.Join(v.GetPath(), name), Item: vfs.VFSItem{Name: name, Size: 1}}
}

func TestLiveResultsPreserveSelectionAndAllowActions(t *testing.T) {
	setupUI(t)
	base := vfs.NewOSVFS(t.TempDir())
	release := make(chan struct{})
	provider := &streamVFS{VFS: base, search: func(ctx context.Context, q vfs.FindQuery, emit func(vfs.FoundEntry)) error {
		for i := 0; i < 20; i++ {
			emit(hit(base, fmt.Sprintf("%02d.txt", i)))
		}
		q.Progress(vfs.FindProgress{Found: 20, Scanned: 35, Path: "branch", DirectoryTotalKnown: true, TotalDirs: 4, CompletedDirs: 1})
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		emit(hit(base, "last.txt"))
		return nil
	}}
	var viewed, edited string
	w := Start(provider, base.GetPath(), "*.txt", "", Options{}, Host{
		View: func(_ vfs.VFS, p string) { viewed = p }, Edit: func(_ vfs.VFS, p string) { edited = p },
	})
	if vtui.FrameManager.GetTopFrame() != w || len(w.found) != 0 {
		t.Fatal("results window was not opened empty before I/O")
	}
	if w.GetTitle() != strings.TrimSpace(i18n.Msg("FindFile.SearchingTitle")) {
		t.Fatalf("active search title: %q", w.GetTitle())
	}
	vtui.AssertLayout(t, w)
	drainUntil(t, func() bool { return len(w.found) == 20 && w.bar.Percent == 25 })
	if !w.Running() {
		t.Fatal("initial hits waited for completion")
	}
	w.table.SelectPos, w.table.TopPos = 12, 9
	selected := w.found[12].Path
	w.HandleCommand(appcmd.CmView, nil)
	w.HandleCommand(appcmd.CmEdit, nil)
	if viewed != selected || edited != selected || !w.Running() {
		t.Fatal("view/edit stopped or used the wrong hit")
	}
	close(release)
	drainUntil(t, func() bool { return !w.running })
	if len(w.found) != 21 || w.table.SelectPos != 12 || w.table.TopPos != 9 {
		t.Fatal("append moved selection or scroll position")
	}
	if w.bar.Percent != 100 || vtui.FrameManager.GetTopFrame() != w {
		t.Fatal("completion replaced/closed the window or did not complete progress")
	}
}

func TestStopFlushesResultsAndKeepsWindow(t *testing.T) {
	setupUI(t)
	base := vfs.NewOSVFS(t.TempDir())
	ready := make(chan struct{})
	p := &streamVFS{VFS: base, search: func(ctx context.Context, q vfs.FindQuery, emit func(vfs.FoundEntry)) error {
		emit(hit(base, "one.txt"))
		emit(hit(base, "two.txt"))
		q.Progress(vfs.FindProgress{DirectoryTotalKnown: true, TotalDirs: 4, CompletedDirs: 1})
		close(ready)
		<-ctx.Done()
		return ctx.Err()
	}}
	w := Start(p, base.GetPath(), "*", "", Options{}, Host{})
	drainUntil(t, func() bool {
		select {
		case <-ready:
			return len(w.found) > 0
		default:
			return false
		}
	})
	w.Stop()
	drainUntil(t, func() bool { return !w.running })
	if len(w.found) != 2 || vtui.FrameManager.GetTopFrame() != w || w.bar.Percent == 100 || !w.pauseButton.IsDisabled() {
		t.Fatal("stop lost hits, closed the window or claimed completion")
	}
	if w.GetTitle() != i18n.Msg("FindFile.Stopped") {
		t.Fatal(w.GetTitle())
	}
}

func TestCloseIgnoresLateCompletionOfPreviousSearch(t *testing.T) {
	setupUI(t)
	base := vfs.NewOSVFS(t.TempDir())
	release := make(chan struct{})
	old := Start(&streamVFS{VFS: base, search: func(ctx context.Context, q vfs.FindQuery, emit func(vfs.FoundEntry)) error {
		<-ctx.Done()
		<-release
		emit(hit(base, "late.txt"))
		return ctx.Err()
	}}, base.GetPath(), "*", "", Options{}, Host{})
	old.Close()
	current := Start(base, base.GetPath(), "*", "", Options{}, Host{})
	close(release)
	drainUntil(t, func() bool {
		old.updates.mu.Lock()
		finished := old.updates.finished
		old.updates.mu.Unlock()
		return finished && !current.running
	})
	// Force delivery of the previous session's final callback even if it was
	// queued after the new session completed.
	old.updates.schedule(&vtui.TaskContext{Context: context.Background()}, old)
	select {
	case task := <-vtui.FrameManager.TaskChan:
		task()
	case <-time.After(time.Second):
		t.Fatal("missing final callback")
	}
	if len(old.found) != 0 || len(current.found) != 0 || vtui.FrameManager.GetTopFrame() != current {
		t.Fatal("late update touched a closed or replacement window")
	}
}

func TestStopFlushesHitsBeforeWorkerFinishes(t *testing.T) {
	setupUI(t)
	base := vfs.NewOSVFS(t.TempDir())
	second, emitted, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	w := Start(&streamVFS{VFS: base, search: func(ctx context.Context, _ vfs.FindQuery, emit func(vfs.FoundEntry)) error {
		emit(hit(base, "first.txt"))
		<-second
		emit(hit(base, "pending.txt"))
		close(emitted)
		<-ctx.Done()
		<-finish // Simulate draining a remote response after cancellation.
		return ctx.Err()
	}}, base.GetPath(), "*", "", Options{}, Host{})
	drainUntil(t, func() bool { return len(w.found) == 1 })
	close(second)
	select {
	case <-emitted:
	case <-time.After(time.Second):
		t.Fatal("second hit was not published")
	}
	w.Stop()
	if len(w.found) != 2 || !w.running {
		t.Error("pending hits were not flushed while the worker was draining")
	}
	if w.GetTitle() != i18n.Msg("FindFile.Stopped") {
		t.Errorf("cancelled search title: %q", w.GetTitle())
	}
	close(finish)
	drainUntil(t, func() bool { return !w.running })
}

func TestSlowGenericVFSShowsFirstHitBeforeListingCompletes(t *testing.T) {
	setupUI(t)
	base := vfs.NewOSVFS(t.TempDir())
	release := make(chan struct{})
	provider := &walkVFS{VFS: base, read: func(ctx context.Context, _ string, chunk func([]vfs.VFSItem)) error {
		chunk([]vfs.VFSItem{{Name: "first.txt"}})
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	w := Start(provider, base.GetPath(), "*", "", Options{}, Host{})
	drainUntil(t, func() bool { return len(w.found) == 1 })
	if !w.running || w.progress.DirectoryTotalKnown || w.bar.IsVisible() {
		t.Error("first hit waited for listing completion or invented a total")
	}
	close(release)
	drainUntil(t, func() bool { return !w.running })
	if !w.progress.DirectoryTotalKnown || w.bar.Percent != 100 {
		t.Fatal("root with no subdirectories did not finish at 100%")
	}
}

func TestFinishedStates(t *testing.T) {
	for _, tc := range []struct {
		name, mask, text string
		options          Options
		failure          error
		wantKey          string
		hits             bool
	}{
		{name: "empty", mask: "*", wantKey: "FindFile.NoResults"},
		{name: "completed", mask: "*", wantKey: "FindFile.Completed", hits: true},
		{name: "partial error", mask: "*", failure: errors.New("unavailable"), wantKey: "FindFile.Failed", hits: true},
		{name: "invalid mask", mask: "a|b|c", wantKey: "FindFile.Failed"},
		{name: "invalid glob", mask: "[", wantKey: "FindFile.Failed"},
		{name: "invalid excluded glob", mask: "* | [", wantKey: "FindFile.Failed"},
		{name: "invalid regex", mask: "*", text: "[", options: Options{Regex: true}, wantKey: "FindFile.Failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupUI(t)
			base := vfs.NewOSVFS(t.TempDir())
			w := Start(&streamVFS{VFS: base, search: func(_ context.Context, _ vfs.FindQuery, emit func(vfs.FoundEntry)) error {
				if tc.hits {
					emit(hit(base, "partial.txt"))
				}
				return tc.failure
			}}, base.GetPath(), tc.mask, tc.text, tc.options, Host{})
			drainUntil(t, func() bool { return !w.running })
			want := strings.Split(i18n.Msg(tc.wantKey), "%s")[0]
			message := w.current.GetText()
			if tc.wantKey == "FindFile.NoResults" {
				message = w.status.GetText()
			}
			if (tc.wantKey != "FindFile.Completed" && !strings.Contains(message, want)) || (tc.hits && len(w.found) != 1) {
				t.Fatal(message)
			}
			titleKey := "FindFile.Completed"
			if tc.wantKey == "FindFile.Failed" {
				titleKey = "FindFile.FailedTitle"
			}
			if w.GetTitle() != i18n.Msg(titleKey) {
				t.Fatalf("finished search title: %q, want %q", w.GetTitle(), i18n.Msg(titleKey))
			}
			if w.bar.Percent == 100 && tc.wantKey == "FindFile.Failed" {
				t.Fatal("failed search claimed 100%")
			}
			for _, b := range w.fileButtons {
				if b.IsDisabled() == tc.hits {
					t.Fatal("file action availability disagrees with hits")
				}
			}
		})
	}
}

func TestCompletedPathShowsSearchedFolders(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(fmt.Sprint(selected), func(t *testing.T) {
			setupUI(t)
			base := vfs.NewOSVFS(t.TempDir())
			options := Options{}
			scope := base.GetPath()
			if selected {
				options.SelectedFolders = []string{base.Join(scope, "one"), base.Join(scope, "two")}
				scope = strings.Join(options.SelectedFolders, ", ")
			}
			provider := &streamVFS{VFS: base, search: func(_ context.Context, query vfs.FindQuery, emit func(vfs.FoundEntry)) error {
				query.Progress(vfs.FindProgress{Path: "last/nested/file.txt"})
				emit(hit(base, "match.txt"))
				return nil
			}}
			w := Start(provider, base.GetPath(), "*", "", options, Host{})
			drainUntil(t, func() bool { return !w.running })
			want := i18n.Msg("FindFile.Scanned") + " " + scope
			if w.currentText != want || strings.Contains(w.currentText, "last/nested") {
				t.Fatalf("completed path=%q, want %q", w.currentText, want)
			}
			w.Close()
		})
	}
}

func TestPauseResumesSameSearchAndCloseCancelsPausedWorker(t *testing.T) {
	for _, closePaused := range []bool{false, true} {
		t.Run(fmt.Sprint(closePaused), func(t *testing.T) {
			setupUI(t)
			base := vfs.NewOSVFS(t.TempDir())
			release := make(chan struct{})
			checkpoint := make(chan struct{})
			advanced := make(chan struct{})
			provider := &streamVFS{VFS: base, search: func(ctx context.Context, query vfs.FindQuery, emit func(vfs.FoundEntry)) error {
				emit(hit(base, "first.txt"))
				<-release
				close(checkpoint)
				query.Progress(vfs.FindProgress{Path: "next", Scanned: 2})
				close(advanced)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				emit(hit(base, "second.txt"))
				return nil
			}}
			w := Start(provider, base.GetPath(), "*", "", Options{}, Host{})
			drainUntil(t, func() bool { return w.RowCount() == 1 })
			w.pauseButton.OnClick()
			if !w.paused || w.GetTitle() != i18n.Msg("FindFile.PausedTitle") || !strings.HasPrefix(w.currentText, i18n.Msg("FindFile.PausedPath")) {
				t.Fatal("Pause did not update the title and path state")
			}
			if w.pauseButton.GetCaption() != strings.ReplaceAll(i18n.Msg("FindFile.BtnResume"), "&", "") {
				t.Fatal("paused search has no Resume action")
			}
			close(release)
			select {
			case <-checkpoint:
			case <-time.After(time.Second):
				t.Fatal("worker did not reach its checkpoint")
			}
			select {
			case <-advanced:
				t.Fatal("worker advanced while paused")
			case <-time.After(30 * time.Millisecond):
			}
			if closePaused {
				w.Close()
				select {
				case <-advanced:
				case <-time.After(time.Second):
					t.Fatal("closing a paused search left the worker blocked")
				}
				return
			}
			w.pauseButton.OnClick()
			if w.paused || !strings.HasPrefix(w.currentText, i18n.Msg("FindFile.Scanning")) || w.GetTitle() != strings.TrimSpace(i18n.Msg("FindFile.SearchingTitle")) {
				t.Fatal("Resume did not restore scanning state")
			}
			drainUntil(t, func() bool { return !w.running })
			if w.RowCount() != 2 || w.GetCellText(0, 0) != "first.txt" || w.GetCellText(1, 0) != "second.txt" || w.pauseButton.IsVisible() {
				t.Fatal("resuming restarted the search, lost results, or retained its pause button")
			}
			if !strings.HasPrefix(w.currentText, i18n.Msg("FindFile.Scanned")) {
				t.Fatal("completion did not replace the scanning state")
			}
			w.Close()
		})
	}
}

func TestGoToAndPanelCancelSearch(t *testing.T) {
	for _, panel := range []bool{false, true} {
		t.Run(fmt.Sprint(panel), func(t *testing.T) {
			setupUI(t)
			base := vfs.NewOSVFS(t.TempDir())
			cancelled := make(chan struct{})
			var got string
			var rows []vfs.FoundEntry
			w := Start(&streamVFS{VFS: base, search: func(ctx context.Context, _ vfs.FindQuery, emit func(vfs.FoundEntry)) error {
				emit(hit(base, "ready.txt"))
				<-ctx.Done()
				close(cancelled)
				return ctx.Err()
			}}, base.GetPath(), "*", "", Options{}, Host{
				GoTo: func(_ vfs.VFS, p string) { got = p }, Panel: func(_ vfs.VFS, h []vfs.FoundEntry) { rows = h },
			})
			drainUntil(t, func() bool { return len(w.found) == 1 })
			if panel {
				w.sendToPanel()
			} else {
				w.goTo()
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("navigation did not cancel")
			}
			drainUntil(t, func() bool { w.updates.mu.Lock(); defer w.updates.mu.Unlock(); return w.updates.finished })
			if !w.closed || (panel && len(rows) != 1) || (!panel && got != w.found[0].Path) {
				t.Fatal("navigation lost the current result")
			}
		})
	}
}

func TestGenericWalkMasksAndProgress(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"root.txt", "a/nested/one.txt", "b/two.txt", "c/three.txt", "d/four.txt", "skip/hidden.txt", "skip.txt"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("needle"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Hide the local optimized interface to exercise the generic VFS route.
	provider := struct{ vfs.VFS }{vfs.NewOSVFS(root)}
	var hits []vfs.FoundEntry
	var completed []int64
	err := run(context.Background(), provider, root, "*.txt | skip, skip.txt", "needle", Options{}, func(h vfs.FoundEntry) { hits = append(hits, h) }, func(p vfs.FindProgress) {
		if p.DirectoryTotalKnown {
			if p.TotalDirs != 4 {
				t.Errorf("total=%d, want four direct children", p.TotalDirs)
			}
			if len(completed) == 0 || completed[len(completed)-1] != p.CompletedDirs {
				completed = append(completed, p.CompletedDirs)
			}
		}
	})
	if err != nil || len(hits) != 5 || fmt.Sprint(completed) != "[0 1 2 3 4]" {
		t.Fatalf("hits=%v progress=%v err=%v", hits, completed, err)
	}
	if hits[0].Item.Name != "root.txt" {
		t.Fatal("root files did not precede subtree completion")
	}
}

func TestResultsLayoutLanguagesAndCompletedList(t *testing.T) {
	setupUI(t)
	packs := i18n.LoadAllLanguagePacks()
	selected := packs[:0]
	for _, p := range packs {
		if p.Name == "en" || p.Name == "ru" {
			selected = append(selected, p)
		}
	}
	if len(selected) != 2 {
		t.Fatal("English/Russian packs missing")
	}
	for _, paused := range []bool{false, true} {
		vtui.AssertLayoutInLanguages(t, selected, func() vtui.Container {
			w := newWindow(vfs.NewOSVFS(t.TempDir()), Host{}, true)
			w.addSearchHeader("/a/long/root", "*.go | .git", "needle", Options{CaseSensitive: true, WholeWords: true, Regex: true, NotContaining: true, FindFolders: true, FindSymlinks: true})
			w.running, w.paused = true, paused
			w.refreshState(nil)
			return w
		})
	}
	base := vfs.NewOSVFS(t.TempDir())
	w := Show(base, []vfs.FoundEntry{hit(base, "duplicate.txt")}, Host{})
	vtui.AssertLayout(t, w)
	if w.running || w.pauseButton != nil || len(w.found) != 1 {
		t.Fatal("completed list gained live-search controls")
	}
}

func TestResultsTableFollowsDialogTheme(t *testing.T) {
	setupUI(t)
	previous := vtui.Palette
	t.Cleanup(func() { vtui.Palette = previous })
	for _, live := range []bool{false, true} {
		t.Run(fmt.Sprint(live), func(t *testing.T) {
			base := vfs.NewOSVFS(t.TempDir())
			w := newWindow(base, Host{}, live)
			w.appendRows([]vfs.FoundEntry{hit(base, "first.txt"), hit(base, "second.txt")})
			scr := vtui.NewSilentScreenBuf()
			scr.AllocBuf(80, 25)
			for _, offset := range []uint32{0, 0x101010} {
				// Change the palette after creating the window to cover runtime
				// theme switching as well as its initial appearance.
				vtui.Palette[vtui.ColDialogText] = vtui.SetRGBBoth(0, 0x123456+offset, 0x223344+offset)
				vtui.Palette[vtui.ColDialogHighlightText] = vtui.SetRGBBoth(0, 0x345678+offset, 0x223344+offset)
				vtui.Palette[vtui.ColDialogSelectedButton] = vtui.SetRGBBoth(0, 0x456789+offset, 0x334455+offset)
				w.table.SetFocus(true)
				w.table.Show(scr)
				for _, cell := range []struct {
					y, palette int
				}{
					{w.table.Y1, vtui.ColDialogHighlightText},
					{w.table.Y1 + 1, vtui.ColDialogSelectedButton},
					{w.table.Y1 + 2, vtui.ColDialogText},
				} {
					if got, want := scr.GetCell(w.table.X1, cell.y).Attributes, vtui.Palette[cell.palette]; got != want {
						t.Fatalf("cell at y=%d: color=%#x, want dialog theme %#x", cell.y, got, want)
					}
				}
				if w.table.ScrollBar.ColorIdx != vtui.ColDialogBox {
					t.Fatal("results scrollbar does not use the dialog theme")
				}
			}
		})
	}
}
