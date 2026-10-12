package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type applyTestHistoryProvider struct{ values map[string][]string }

// Apply Command owns a specialized frame; its behavioral tests depend on the
// dialog contract rather than the embedded Window's concrete type.
type applyTestDialog interface {
	vtui.Frame
	vtui.Container
}

type unavailableApplyCommandRunner struct{ *vfs.OSVFS }

type invalidDialectApplyCommandRunner struct{ *vfs.OSVFS }

type panicPathApplyVFS struct {
	*vfs.OSVFS
	panicOnPath bool
}

func (v *panicPathApplyVFS) GetPath() string {
	if v.panicOnPath {
		panic("closed workspace VFS was touched")
	}
	return v.OSVFS.GetPath()
}

func (r *unavailableApplyCommandRunner) RunCommand(context.Context, string, string, func(string)) (int, error) {
	return 0, nil
}
func (r *unavailableApplyCommandRunner) CommandRunnerAvailable() bool { return false }

func (r *invalidDialectApplyCommandRunner) RunCommand(context.Context, string, string, func(string)) (int, error) {
	return 0, nil
}

func (r *invalidDialectApplyCommandRunner) CommandRunnerInfo() vfs.CommandRunnerInfo {
	return vfs.CommandRunnerInfo{Dialect: vfs.CommandDialect(99), MaxParallel: 8}
}

func (p *applyTestHistoryProvider) LoadHistory(id string) []string {
	return append([]string(nil), p.values[id]...)
}

func (p *applyTestHistoryProvider) SaveHistory(id string, history []string) {
	p.values[id] = append([]string(nil), history...)
}

func TestApplyCommandActionRegistered(t *testing.T) {
	action, ok := GetAction("File.ApplyCommand")
	if !ok {
		t.Fatal("File.ApplyCommand is not registered")
	}
	if action.Area != "Shell" || action.MenuPath != "Files" || action.LabelKey != "Action.File.ApplyCommand" || len(action.DefaultKeys) != 1 || action.DefaultKeys[0] != "CtrlG" {
		t.Fatalf("action = %+v", action)
	}
}

// TestApplyCommandActionDisabledWithoutSupport checks the other half of
// f4#1356's "silent no-op" class for File.ApplyCommand: the action already
// had Visible: panel.PanelCanApplyCommand, which hides the menu item on a
// VFS with no command runner, but Ctrl+G stayed live and still opened the
// "ApplyCommand.Unsupported" error dialog. Wiring the same predicate to
// Enabled makes RunAction refuse the hotkey too, instead of just the menu
// entry disappearing.
func TestApplyCommandActionDisabledWithoutSupport(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	fsp := pf.GetActivePanel()
	fsp.Vfs = vfs.NewNullVFS(0)

	act, ok := GetAction("File.ApplyCommand")
	if !ok {
		t.Fatal("File.ApplyCommand is not registered")
	}
	if act.Enabled == nil {
		t.Fatal("File.ApplyCommand has no Enabled predicate")
	}
	if act.Enabled() {
		t.Error("Enabled should be false on a VFS with no command runner")
	}

	if RunAction("File.ApplyCommand") {
		t.Error("RunAction should refuse a disabled File.ApplyCommand instead of opening the Unsupported dialog")
	}
	if top := vtui.FrameManager.GetTopFrame(); top != pf {
		t.Fatalf("a disabled File.ApplyCommand opened a frame instead of being refused: %T", top)
	}

	fsp.Vfs = vfs.NewOSVFS(t.TempDir())
	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "file.txt"}},
	}
	fsp.SetCursorIndex(1)
	if !act.Enabled() {
		t.Error("Enabled should be true once the active VFS supports Apply Command")
	}
}

func TestApplyCommandActionUsesActivePanelWorkspace(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pfA := paneltest.SetupMockPanelsFrame(t)
	pfB := paneltest.SetupMockPanelsFrame(t)
	defer pfA.Close()
	defer pfB.Close()
	pfA.ResizeConsole(80, 25)
	pfB.ResizeConsole(80, 25)
	panelA := pfA.GetActivePanel()
	panelB := pfB.GetActivePanel()
	panelA.Vfs = vfs.NewNullVFS(0)
	panelB.Vfs = vfs.NewOSVFS(t.TempDir())
	panelB.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "active.txt"}},
	}
	panelB.SetCursorIndex(1)

	vtui.FrameManager.Push(pfA)
	vtui.FrameManager.AddScreen(pfB)
	if got := panel.FindPanelsFrame(); got != pfB {
		t.Fatalf("active panels frame = %p, want workspace B %p", got, pfB)
	}
	if !panel.PanelCanApplyCommand() {
		t.Fatal("Apply visibility was taken from unsupported background workspace A")
	}

	panelA.Vfs = vfs.NewOSVFS(t.TempDir())
	panelA.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "background.txt"}},
	}
	panelA.SetCursorIndex(1)
	action, ok := GetAction("File.ApplyCommand")
	if !ok || !action.Handler() {
		t.Fatal("Apply action was not dispatched")
	}
	if got := panel.FindPanelsFrame(); got != pfB {
		t.Fatalf("Apply switched to background workspace A; active frame = %p", got)
	}
	top, ok := vtui.FrameManager.GetTopFrame().(applyTestDialog)
	if !ok || top.GetType() != vtui.TypeDialog || top.GetTitle() != i18n.Msg("ApplyCommand.Title") {
		t.Fatalf("active workspace top frame = %T %q", vtui.FrameManager.GetTopFrame(), vtui.FrameManager.GetTopFrame().GetTitle())
	}
	top.Close()
	vtui.FrameManager.RemoveFrame(top)
}

func TestResolveApplyCommandRunnerHonorsNegotiatedAvailability(t *testing.T) {
	runner := &unavailableApplyCommandRunner{OSVFS: vfs.NewOSVFS(t.TempDir())}
	if _, _, ok := panel.ResolveApplyCommandRunner(runner); ok {
		t.Fatal("unavailable command provider was accepted")
	}
}

func TestResolveApplyCommandRunnerNormalizesInvalidDialect(t *testing.T) {
	runner := &invalidDialectApplyCommandRunner{OSVFS: vfs.NewOSVFS(t.TempDir())}
	_, info, ok := panel.ResolveApplyCommandRunner(runner)
	if !ok {
		t.Fatal("command provider was unexpectedly rejected")
	}
	if info.Dialect != vfs.CommandDialectUnknown {
		t.Fatalf("dialect = %d, want Unknown", info.Dialect)
	}
}

func TestEffectiveApplyCommandWorkers(t *testing.T) {
	tests := []struct {
		mode                        cmdline.ApplyCommandMode
		configured, count, provider int
		want                        int
	}{
		{cmdline.ApplyCommandSequential, runtime.NumCPU(), 20, 0, 1},
		{cmdline.ApplyCommandQueued, 20, 20, 0, 1},
		{cmdline.ApplyCommandParallel, 3, 20, 0, 3},
		{cmdline.ApplyCommandParallel, 0, 7, 0, 7},
		{cmdline.ApplyCommandParallel, 99, 7, 4, 4},
	}
	for _, tc := range tests {
		if got := panel.EffectiveApplyCommandWorkers(tc.mode, tc.configured, tc.count, tc.provider); got != tc.want {
			t.Errorf("effective(%d,%d,%d,%d) = %d, want %d", tc.mode, tc.configured, tc.count, tc.provider, got, tc.want)
		}
	}
}

func TestApplyQueuePreconditionUsesStatSemanticsForSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("target contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(filepath.Base(target), link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	fs := vfs.NewOSVFS(dir)
	linkInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	pnl := &panel.FileSystemPanel{
		Vfs: fs,
		Entries: []*panel.FileEntry{{VFSItem: vfs.VFSItem{
			Name: linkInfo.Name(), Size: linkInfo.Size(), MTime: linkInfo.ModTime(), IsSymlink: true,
		}}},
	}
	session := &panel.ApplyCommandSession{
		Active:  panel.ApplyPanelCapture{Panel: pnl, PanelVFS: fs, Vfs: fs, Dir: dir},
		Targets: []string{filepath.Base(link)},
	}
	conditions := session.QueuePreconditions()
	if len(conditions) != 1 {
		t.Fatalf("preconditions = %d, want 1", len(conditions))
	}
	want, err := fs.Stat(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	got := conditions[0]
	if got.Size != want.Size || got.MTime != want.MTime || got.IsDir != want.IsDir {
		t.Fatalf("precondition = %+v, want Stat metadata %+v", got, want)
	}
}

func TestApplySelectionTokenDoesNotClearLaterRemark(t *testing.T) {
	pf := seedPanelForRestore(t, []string{"one.txt"})
	pnl := pf.GetActivePanel()
	pnl.SetItemSelected(1, true)
	token, ok := pnl.CaptureSelectionToken("one.txt")
	if !ok {
		t.Fatal("selection token not captured")
	}
	pnl.SetItemSelected(1, false)
	pnl.SetItemSelected(1, true)
	if pnl.ClearSelectionIfUnchanged(token) {
		t.Fatal("stale Apply completion cleared a later user selection")
	}
	if !pnl.IsNameSelected("one.txt") {
		t.Fatal("later selection was lost")
	}
}

func TestApplySelectionTokenClearsUnchangedMark(t *testing.T) {
	pf := seedPanelForRestore(t, []string{"one.txt"})
	pnl := pf.GetActivePanel()
	pnl.SetItemSelected(1, true)
	token, ok := pnl.CaptureSelectionToken("one.txt")
	if !ok || !pnl.ClearSelectionIfUnchanged(token) || pnl.IsNameSelected("one.txt") {
		t.Fatalf("unchanged mark was not cleared: token=%+v ok=%v", token, ok)
	}
}

func TestApplySelectionTokenDoesNotCrossVFSIdentity(t *testing.T) {
	pf := seedPanelForRestore(t, []string{"one.txt"})
	pnl := pf.GetActivePanel()
	dir := t.TempDir()
	pnl.Vfs = vfs.NewOSVFS(dir)
	pnl.SetItemSelected(1, true)
	token, ok := pnl.CaptureSelectionToken("one.txt")
	if !ok {
		t.Fatal("selection token not captured")
	}
	pnl.Vfs = vfs.NewOSVFS(dir)
	if pnl.ClearSelectionIfUnchanged(token) || !pnl.IsNameSelected("one.txt") {
		t.Fatal("completion from the prior VFS cleared the new panel mark")
	}
}

func TestApplyFinalRefreshPreservesCtrlMSelection(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pf := seedPanelForRestore(t, nil)
	pnl := pf.GetActivePanel()
	pnl.Vfs = vfs.NewOSVFS(dir)
	pnl.ReadDirectory()
	paneltest.WaitForLoad(t, pnl)
	if !pnl.SetSelectedByName("one.txt", true) || !pnl.SetSelectedByName("two.txt", true) {
		t.Fatalf("test files missing after initial load: %+v", pnl.Entries)
	}
	pnl.SaveSelection()
	pnl.SetSelectedByName("one.txt", false)
	pnl.SetSelectedByName("two.txt", false)

	session := &panel.ApplyCommandSession{
		Pf: pf,
		Active: panel.ApplyPanelCapture{
			Panel: pnl, PanelVFS: pnl.Vfs, Vfs: pnl.Vfs, Dir: dir,
		},
	}
	session.RefreshCapturedPanels()
	paneltest.WaitForLoad(t, pnl)
	if got := collectSelected(pnl); len(got) != 0 {
		t.Fatalf("final refresh resurrected processed marks early: %v", got)
	}

	pnl.RestoreSelection()
	if got := collectSelected(pnl); len(got) != 2 || got[0] != "one.txt" || got[1] != "two.txt" {
		t.Fatalf("Ctrl+M after Apply refresh selected %v, want [one.txt two.txt]", got)
	}
}

func TestApplyCompletionDoesNotTouchClosedWorkspace(t *testing.T) {
	pf := seedPanelForRestore(t, []string{"one.txt"})
	pnl := pf.GetActivePanel()
	closedVFS := &panicPathApplyVFS{OSVFS: vfs.NewOSVFS(t.TempDir())}
	pnl.Vfs = closedVFS
	pnl.SetItemSelected(1, true)
	token, ok := pnl.CaptureSelectionToken("one.txt")
	if !ok {
		t.Fatal("selection token not captured")
	}
	closedVFS.panicOnPath = true
	pf.PtyMutex.Lock()
	pf.Closed = true
	pf.PtyMutex.Unlock()
	session := &panel.ApplyCommandSession{
		Pf: pf, Explicit: true, Tokens: map[string]panel.PanelSelectionToken{"one.txt": token},
		Active: panel.ApplyPanelCapture{Panel: pnl, PanelVFS: closedVFS, Vfs: closedVFS, Dir: "/captured"},
	}
	session.PostItemFinished(cmdline.ApplyBatchItemResult{AffectedNames: []string{"one.txt"}})
	drained := make(chan struct{})
	vtui.FrameManager.PostTask(func() { close(drained) })
	deadline := time.After(time.Second)
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-drained:
			if !pnl.IsNameSelected("one.txt") {
				t.Fatal("completion cleared selection in a closed workspace")
			}
			session.RefreshCapturedPanels()
			return
		case <-deadline:
			t.Fatal("timed out draining completion UI task")
		}
	}
}

func TestCancelAllForegroundApplyCommands(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	unregister := panel.RegisterForegroundApplyCommand(cancel)
	defer unregister()
	panel.CancelAllForegroundApplyCommands()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("foreground context = %v", ctx.Err())
	}
}

func TestApplyQueueDetailsConsumesCtrlWAndClosesOnlyDialog(t *testing.T) {
	model := cmdline.NewApplyBatchViewModel(1)
	dlg := cmdline.ShowApplyOutputDialog(nil, model, nil)
	defer vtui.FrameManager.RemoveFrame(dlg)
	if !dlg.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_W,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}) {
		t.Fatal("queue Details did not consume Ctrl+W")
	}
	if !dlg.IsDone() {
		t.Fatal("queue Details did not close its own dialog on Ctrl+W")
	}
}

func TestApplyCommandCtrlGOpensHistoryDialogAndPreservesCommandLine(t *testing.T) {
	pf := seedPanelForRestore(t, []string{"one.txt"})
	pnl := pf.GetActivePanel()
	pnl.Vfs = vfs.NewOSVFS(t.TempDir())
	pnl.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}, {VFSItem: vfs.VFSItem{Name: "one.txt"}}}
	pnl.SetCursorIndex(1)
	pf.CmdLine.Edit.SetText("keep this command")

	handled := pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_G,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if !handled {
		t.Fatal("Ctrl+G was not handled")
	}
	top, ok := vtui.FrameManager.GetTopFrame().(applyTestDialog)
	if !ok || top.GetType() != vtui.TypeDialog || top.GetTitle() != i18n.Msg("ApplyCommand.Title") {
		t.Fatalf("top frame = %T %q", vtui.FrameManager.GetTopFrame(), vtui.FrameManager.GetTopFrame().GetTitle())
	}
	foundHistory := false
	for _, child := range top.GetChildren() {
		if edit, ok := child.(*vtui.Edit); ok && edit.HistoryID == "ApplyCmd" && edit.ShowHistoryButton {
			foundHistory = true
		}
	}
	if !foundHistory {
		t.Fatal("ApplyCmd history editor not found")
	}
	if got := pf.CmdLine.Edit.GetText(); got != "keep this command" {
		t.Fatalf("command line changed to %q", got)
	}
	top.Close()
}

func TestApplyCommandPromptCancellationKeepsHistoryAndSelection(t *testing.T) {
	pf := seedPanelForRestore(t, []string{"one.txt"})
	pnl := pf.GetActivePanel()
	pnl.Vfs = vfs.NewOSVFS(t.TempDir())
	pnl.SetItemSelected(1, true)

	oldProvider := vtui.GlobalHistoryProvider
	provider := &applyTestHistoryProvider{values: make(map[string][]string)}
	vtui.GlobalHistoryProvider = provider
	defer func() { vtui.GlobalHistoryProvider = oldProvider }()
	oldTemplate := panel.LastApplyCommandTemplate
	panel.LastApplyCommandTemplate = ""
	defer func() { panel.LastApplyCommandTemplate = oldTemplate }()

	panel.ActionApplyCommand(pf)
	applyDialog, ok := vtui.FrameManager.GetTopFrame().(applyTestDialog)
	if !ok || applyDialog.GetType() != vtui.TypeDialog || applyDialog.GetTitle() != i18n.Msg("ApplyCommand.Title") {
		t.Fatalf("top frame = %T, want Apply dialog", vtui.FrameManager.GetTopFrame())
	}
	var commandEdit *vtui.Edit
	var runButton *vtui.Button
	for _, child := range applyDialog.GetChildren() {
		switch child := child.(type) {
		case *vtui.Edit:
			if child.HistoryID == "ApplyCmd" {
				commandEdit = child
			}
		case *vtui.Button:
			if child.IsDefault {
				runButton = child
			}
		}
	}
	if commandEdit == nil || runButton == nil {
		t.Fatal("Apply dialog command editor or Run button not found")
	}
	commandEdit.SetText("echo !?Value?default!")
	runButton.OnClick()

	promptDialog, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok || promptDialog == applyDialog {
		t.Fatalf("top frame = %T, want prompt dialog", vtui.FrameManager.GetTopFrame())
	}
	var cancelButton *vtui.Button
	for _, child := range promptDialog.GetChildren() {
		if button, ok := child.(*vtui.Button); ok && !button.IsDefault {
			cancelButton = button
			break
		}
	}
	if cancelButton == nil {
		t.Fatal("prompt Cancel button not found")
	}
	cancelButton.OnClick()
	vtui.FrameManager.RemoveFrame(promptDialog)

	if vtui.FrameManager.GetTopFrame() != applyDialog {
		t.Fatal("prompt cancellation did not return to Apply dialog")
	}
	if len(provider.values["ApplyCmd"]) != 0 || panel.LastApplyCommandTemplate != "" {
		t.Fatalf("cancelled preflight changed history=%v template=%q", provider.values["ApplyCmd"], panel.LastApplyCommandTemplate)
	}
	if !pnl.IsNameSelected("one.txt") || pnl.Entries[1].PrevSelected {
		t.Fatal("cancelled preflight changed or saved the panel selection")
	}
	applyDialog.Close()
}

func TestApplyCommandPromptDialogPagesEveryField(t *testing.T) {
	prompts := make([]cmdline.ApplyCommandResolvedPrompt, 12)
	for i := range prompts {
		prompts[i] = cmdline.ApplyCommandResolvedPrompt{Index: i, Title: fmt.Sprintf("Field %d", i+1), Initial: fmt.Sprintf("default-%d", i+1)}
	}
	var accepted cmdline.ApplyCommandPromptValues
	panel.ShowApplyCommandPrompts(nil, prompts, func(values cmdline.ApplyCommandPromptValues) { accepted = values })
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("top frame = %T, want prompt dialog", vtui.FrameManager.GetTopFrame())
	}
	defer vtui.FrameManager.RemoveFrame(dlg)

	var edits []*vtui.Edit
	buttons := make(map[string]*vtui.Button)
	for _, child := range dlg.GetChildren() {
		switch child := child.(type) {
		case *vtui.Edit:
			edits = append(edits, child)
		case *vtui.Button:
			buttons[child.GetCaption()] = child
		}
	}
	if len(edits) != len(prompts) {
		t.Fatalf("edit count = %d, want %d", len(edits), len(prompts))
	}
	cleanCaption := func(message string) string {
		clean, _, _ := vtui.ParseAmpersandString(message)
		return clean
	}
	next := buttons[cleanCaption(i18n.Msg("ApplyCommand.PromptNext"))]
	back := buttons[cleanCaption(i18n.Msg("ApplyCommand.PromptBack"))]
	okButton := buttons[cleanCaption(i18n.Msg("vtui.Ok"))]
	if next == nil || back == nil || okButton == nil {
		t.Fatalf("pagination buttons missing: %#v", buttons)
	}
	for i, edit := range edits {
		edit.SetText(fmt.Sprintf("answer-%d", i+1))
	}
	if !back.IsDisabled() || next.IsDisabled() || !next.IsDefault || !okButton.IsDisabled() {
		t.Fatal("first prompt page has incorrect navigation state")
	}
	enabled := 0
	for i, edit := range edits {
		if !edit.IsDisabled() {
			enabled++
		}
		if edit.IsVisible() != (i < 10) {
			t.Errorf("field %d initial visibility = %v", i+1, edit.IsVisible())
		}
	}
	if enabled != 10 {
		t.Fatalf("enabled fields on first page = %d, want 10", enabled)
	}

	next.OnClick()
	if back.IsDisabled() || !next.IsDisabled() || next.IsDefault || okButton.IsDisabled() || !okButton.IsDefault {
		t.Fatal("last prompt page has incorrect navigation state")
	}
	enabled = 0
	for i, edit := range edits {
		if !edit.IsDisabled() {
			enabled++
		}
		if edit.IsVisible() != (i >= 10) {
			t.Errorf("field %d last-page visibility = %v", i+1, edit.IsVisible())
		}
	}
	if enabled != 2 {
		t.Fatalf("enabled fields on last page = %d, want 2", enabled)
	}
	back.OnClick()
	for i, edit := range edits {
		if edit.IsVisible() != (i < 10) {
			t.Errorf("field %d visibility after Back = %v", i+1, edit.IsVisible())
		}
	}
	next.OnClick()
	okButton.OnClick()
	if len(accepted) != len(prompts) {
		t.Fatalf("accepted values = %d, want %d", len(accepted), len(prompts))
	}
	for i := range prompts {
		if got, want := accepted[i], fmt.Sprintf("answer-%d", i+1); got != want {
			t.Errorf("value %d = %q, want %q", i+1, got, want)
		}
	}
}
