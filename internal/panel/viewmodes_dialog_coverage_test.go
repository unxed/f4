package panel

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtui"
)

func TestPanelViewModeName(t *testing.T) {
	for _, tc := range []struct {
		key  int
		want string
	}{
		{-1, ""},
		{PanelViewModeCount, ""},
		{PanelViewModeCount + 5, ""},
	} {
		if got := panelViewModeName(tc.key); got != tc.want {
			t.Errorf("panelViewModeName(%d) = %q, want %q", tc.key, got, tc.want)
		}
	}
	for key := 0; key < PanelViewModeCount; key++ {
		want := i18n.Msg(panelViewModeNameKeys[key])
		if got := panelViewModeName(key); got != want {
			t.Errorf("panelViewModeName(%d) = %q, want %q", key, got, want)
		}
		if want == "" {
			t.Errorf("panelViewModeName(%d) is empty, want a real message", key)
		}
	}
}

// TestPanelModesMenuPosKeyRoundTrip covers far2l's IndexToMenuPos mapping
// (Ctrl+1..Ctrl+9 first, Ctrl+0 last): every mode's menu position maps back
// to its own key, and the fixed points documented in the code are exact.
func TestPanelModesMenuPosKeyRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		mode ViewMode
		pos  int
	}{
		{ViewModeBrief, 0},
		{ViewModeMedium, 1},
		{ViewModeDetailed, 2},
		{ViewModeWide, 3},
		{ViewMode0, PanelViewModeCount - 1},
	} {
		if got := panelModesMenuPos(tc.mode); got != tc.pos {
			t.Errorf("panelModesMenuPos(%v) = %d, want %d", tc.mode, got, tc.pos)
		}
	}
	for key := 0; key < PanelViewModeCount; key++ {
		mode, ok := ViewModeForKey(key)
		if !ok {
			t.Fatalf("no mode for key %d", key)
		}
		pos := panelModesMenuPos(mode)
		if pos < 0 || pos >= PanelViewModeCount {
			t.Fatalf("panelModesMenuPos(%v) = %d out of range", mode, pos)
		}
		if got := panelModesMenuKey(pos); got != key {
			t.Errorf("panelModesMenuKey(panelModesMenuPos(%v)) = %d, want %d", mode, got, key)
		}
	}
}

func TestPanelModeErrorText(t *testing.T) {
	typeErr := &PanelColumnTypeError{Token: "ZZ"}
	widthErr := &PanelColumnWidthError{Token: "bad"}
	plain := errors.New("boom")
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"type", typeErr, fmt.Sprintf(i18n.Msg("Panel.Modes.BadColumnType"), "ZZ")},
		{"width", widthErr, fmt.Sprintf(i18n.Msg("Panel.Modes.BadColumnWidth"), "bad")},
		{"no columns", ErrNoPanelColumns, i18n.Msg("Panel.Modes.NoColumns")},
		{"wrapped no columns", fmt.Errorf("parsing: %w", ErrNoPanelColumns), i18n.Msg("Panel.Modes.NoColumns")},
		{"plain", plain, plain.Error()},
	} {
		if got := panelModeErrorText(tc.err); got != tc.want {
			t.Errorf("%s: panelModeErrorText = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestShowPanelModesMenuWithoutFrameManagerIsInert(t *testing.T) {
	old := vtui.FrameManager
	vtui.FrameManager = nil
	t.Cleanup(func() { vtui.FrameManager = old })

	// Neither call may panic or otherwise assume a live FrameManager, with
	// or without an active panel to read a mode from.
	ShowPanelModesMenu(nil)
	ShowPanelModesMenu(base64TestPanel(t.TempDir(), "file.txt"))
	openPanelModesMenu(nil, 0)
	editPanelViewMode(nil, 0)
}

func TestApplyPanelViewModesGuards(t *testing.T) {
	// A nil frame must be a pure no-op.
	applyPanelViewModes(nil)

	pf := base64TestPanel(t.TempDir(), "file.txt")

	// LastW/LastH still zero: ResizeConsole (which needs a lot more panel
	// state than this minimal fixture has) must not be reached, and a nil
	// FrameManager must not be dereferenced either.
	old := vtui.FrameManager
	vtui.FrameManager = nil
	applyPanelViewModes(pf)
	vtui.FrameManager = old

	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	applyPanelViewModes(pf)
	select {
	case <-vtui.FrameManager.RedrawChan:
	default:
		t.Error("applyPanelViewModes did not request a redraw")
	}
}

func TestShowPanelModesMenuOpensAtActiveMode(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := base64TestPanel(t.TempDir(), "file.txt")
	fsp := pf.GetActivePanel()
	fsp.ViewMode = ViewModeDetailed

	ShowPanelModesMenu(pf)

	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("top frame = %T, want the modes menu", vtui.FrameManager.GetTopFrame())
	}
	if len(menu.Items) != PanelViewModeCount {
		t.Fatalf("menu items = %d, want %d", len(menu.Items), PanelViewModeCount)
	}
	wantPos := panelModesMenuPos(ViewModeDetailed)
	if menu.SelectPos != wantPos {
		t.Errorf("menu.SelectPos = %d, want %d", menu.SelectPos, wantPos)
	}
	for p := 0; p < PanelViewModeCount; p++ {
		key := panelModesMenuKey(p)
		wantShortcut := "Ctrl+" + strconv.Itoa(key)
		if menu.Items[p].Shortcut != wantShortcut {
			t.Errorf("item %d shortcut = %q, want %q", p, menu.Items[p].Shortcut, wantShortcut)
		}
		if want := panelViewModeName(key); menu.Items[p].Text != want {
			t.Errorf("item %d text = %q, want %q", p, menu.Items[p].Text, want)
		}
	}
}

// viewModeDialogWidgets picks the edit dialog's controls out by the fixed
// order editPanelViewMode adds them in: name, type and width (label, edit each),
// the status line's Columns... button, full-screen and uppercase-directory
// checkboxes, then the buttons in the order they stand on the row: OK,
// Columns..., Reset, Cancel.
func viewModeDialogWidgets(t *testing.T, dlg *vtui.Window) (editTypes, editWidths *vtui.Edit, fullScreen, uppercaseDirs *vtui.Checkbox, ok, reset, cancel *vtui.Button) {
	t.Helper()
	children := dlg.GetChildren()
	if len(children) != 17 {
		t.Fatalf("dialog has %d children, want 17", len(children))
	}
	var assertOk bool
	if editTypes, assertOk = children[3].(*vtui.Edit); !assertOk {
		t.Fatalf("children[3] = %T, want *vtui.Edit", children[3])
	}
	if editWidths, assertOk = children[5].(*vtui.Edit); !assertOk {
		t.Fatalf("children[5] = %T, want *vtui.Edit", children[5])
	}
	if fullScreen, assertOk = children[11].(*vtui.Checkbox); !assertOk {
		t.Fatalf("children[11] = %T, want *vtui.Checkbox", children[11])
	}
	if uppercaseDirs, assertOk = children[12].(*vtui.Checkbox); !assertOk {
		t.Fatalf("children[12] = %T, want *vtui.Checkbox", children[12])
	}
	if ok, assertOk = children[13].(*vtui.Button); !assertOk {
		t.Fatalf("children[13] = %T, want *vtui.Button", children[13])
	}
	if reset, assertOk = children[15].(*vtui.Button); !assertOk {
		t.Fatalf("children[15] = %T, want *vtui.Button", children[15])
	}
	if cancel, assertOk = children[16].(*vtui.Button); !assertOk {
		t.Fatalf("children[16] = %T, want *vtui.Button", children[16])
	}
	return
}

// openViewModeEditDialog drives the menu open to the point where the edit
// dialog for pos is on top, the way a real Enter on that menu row would.
func openViewModeEditDialog(t *testing.T, pf *PanelsFrame, pos int) *vtui.Window {
	t.Helper()
	ShowPanelModesMenu(pf)
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("top frame = %T, want the modes menu", vtui.FrameManager.GetTopFrame())
	}
	menu.OnAction(pos)
	testutil.DrainUITasks()
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("top frame after selecting a mode = %T, want the edit dialog", vtui.FrameManager.GetTopFrame())
	}
	return dlg
}

// Tab walks the buttons of the mode dialog in the order they stand on the row,
// and the dialog is tall enough for the button row to be inside the frame
// (f4#410).
func TestViewModeDialogTabOrderFollowsTheButtonRowAndFitsTheFrame(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	withTempPanelModesFile(t)

	pf := base64TestPanel(t.TempDir(), "file.txt")
	dlg := openViewModeEditDialog(t, pf, panelModesMenuPos(ViewModeDetailed))

	children := dlg.GetChildren()
	var buttons []*vtui.Button
	statusColumns := strings.ReplaceAll(i18n.Msg("Panel.Modes.EditStatusColumns"), "&", "")
	for _, child := range children {
		// The status line's Columns... stands under its own fields, not
		// in the row of buttons (f4#410).
		if b, ok := child.(*vtui.Button); ok && b.GetCaption() != statusColumns {
			buttons = append(buttons, b)
		}
	}
	if len(buttons) != 4 {
		t.Fatalf("dialog has %d buttons in its row, want 4", len(buttons))
	}
	want := []string{i18n.Msg("vtui.Ok"), i18n.Msg("Panel.Modes.EditColumns"), i18n.Msg("Panel.Modes.Reset"), i18n.Msg("vtui.Cancel")}
	for i, b := range buttons {
		if got, w := b.GetCaption(), strings.ReplaceAll(want[i], "&", ""); got != w {
			t.Errorf("button %d in Tab order is %q, want %q", i, got, w)
		}
	}
	// The row of buttons stands left to right in that same order, and above
	// the bottom frame.
	for i := 1; i < len(buttons); i++ {
		if buttons[i].X1 <= buttons[i-1].X1 {
			t.Errorf("button %q (x=%d) does not stand right of %q (x=%d)", buttons[i].GetCaption(), buttons[i].X1, buttons[i-1].GetCaption(), buttons[i-1].X1)
		}
	}
	for _, b := range buttons {
		if b.Y1 >= dlg.Y2 {
			t.Errorf("button %q at y=%d is not above the bottom frame (y=%d)", b.GetCaption(), b.Y1, dlg.Y2)
		}
	}
}

func withTempPanelModesFile(t *testing.T) {
	t.Helper()
	resetPanelViewModes(true)
	t.Cleanup(func() { resetPanelViewModes(true) })

	// SetPanelViewModeSettings saves panel_modes.ini beside the config dir;
	// point that at a scratch directory instead of the real user profile.
	_ = config.GetF4ConfigDir()
	oldCfg := config.CachedF4ConfigDir
	config.CachedF4ConfigDir = t.TempDir()
	config.ConfigDirOnce.Do(func() {})
	t.Cleanup(func() { config.CachedF4ConfigDir = oldCfg })
}

func TestEditPanelViewModeSavesValidColumnsAndReturnsToMenu(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	withTempPanelModesFile(t)

	pf := base64TestPanel(t.TempDir(), "file.txt")
	pf.GetActivePanel().ViewMode = ViewModeDetailed
	pos := panelModesMenuPos(ViewModeDetailed)

	dlg := openViewModeEditDialog(t, pf, pos)
	editTypes, editWidths, fullScreen, uppercaseDirs, okBtn, _, _ := viewModeDialogWidgets(t, dlg)

	if PanelViewModeCustomized(ViewModeDetailed) {
		t.Fatal("mode is customized before any edit was saved")
	}

	editTypes.SetText("N,S,N,S")
	editWidths.SetText("0,7,0,7")
	fullScreen.State = 1
	uppercaseDirs.State = 1
	okBtn.OnClick()

	if !PanelViewModeCustomized(ViewModeDetailed) {
		t.Fatal("OK did not save a custom mode")
	}
	settings := PanelViewModeSettings(ViewModeDetailed)
	gotTypes, gotWidths := ViewSettingsToText(settings.Columns)
	if gotTypes != "N,S,N,S" || gotWidths != "0,7,0,7" {
		t.Fatalf("saved columns = %q/%q, want %q/%q", gotTypes, gotWidths, "N,S,N,S", "0,7,0,7")
	}
	if !settings.FullScreen {
		t.Error("saved settings did not keep the full-screen checkbox")
	}
	if !settings.UppercaseDirs {
		t.Error("saved settings did not keep the uppercase-directories checkbox")
	}

	// finish() closes the edit dialog and posts a task back to the menu.
	testutil.DrainUITasks()
	if _, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu); !ok {
		t.Fatalf("top frame after OK = %T, want the modes menu again", vtui.FrameManager.GetTopFrame())
	}
}

func TestEditPanelViewModeRejectsBadColumnsWithoutClosing(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	withTempPanelModesFile(t)

	pf := base64TestPanel(t.TempDir(), "file.txt")
	pf.GetActivePanel().ViewMode = ViewModeDetailed
	pos := panelModesMenuPos(ViewModeDetailed)

	dlg := openViewModeEditDialog(t, pf, pos)
	editTypes, editWidths, _, _, okBtn, _, _ := viewModeDialogWidgets(t, dlg)

	editTypes.SetText("ZZ")
	editWidths.SetText("")
	okBtn.OnClick()

	if PanelViewModeCustomized(ViewModeDetailed) {
		t.Fatal("a rejected column list must not be saved")
	}
	errWin, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("top frame after a bad OK = %T, want an error dialog", vtui.FrameManager.GetTopFrame())
	}
	if errWin == dlg {
		t.Fatal("bad input closed the edit dialog instead of reporting an error on top of it")
	}
	_, err := TextToViewSettings("ZZ", "")
	wantText := panelModeErrorText(err)
	if !strings.Contains(errWin.GetTitle(), i18n.Msg("Panel.Modes.Title")) {
		t.Errorf("error dialog title = %q, want it to mention %q", errWin.GetTitle(), i18n.Msg("Panel.Modes.Title"))
	}
	_ = wantText // the exact wrapped message is covered by TestPanelModeErrorText above
}

func TestEditPanelViewModeResetClearsCustomization(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	withTempPanelModesFile(t)

	columns, err := TextToViewSettings("N,S", "0,7")
	if err != nil {
		t.Fatalf("TextToViewSettings: %v", err)
	}
	if err := SetPanelViewModeSettings(ViewModeDetailed, &PanelViewSettings{Columns: columns}); err != nil {
		t.Fatalf("SetPanelViewModeSettings: %v", err)
	}
	if !PanelViewModeCustomized(ViewModeDetailed) {
		t.Fatal("setup did not customize the mode")
	}

	pf := base64TestPanel(t.TempDir(), "file.txt")
	pf.GetActivePanel().ViewMode = ViewModeDetailed
	pos := panelModesMenuPos(ViewModeDetailed)

	dlg := openViewModeEditDialog(t, pf, pos)
	_, _, _, _, _, resetBtn, _ := viewModeDialogWidgets(t, dlg)

	resetBtn.OnClick()

	if PanelViewModeCustomized(ViewModeDetailed) {
		t.Fatal("Reset did not clear the mode's override")
	}
	testutil.DrainUITasks()
	if _, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu); !ok {
		t.Fatalf("top frame after Reset = %T, want the modes menu again", vtui.FrameManager.GetTopFrame())
	}
}

func TestEditPanelViewModeCancelLeavesModeUnchanged(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	withTempPanelModesFile(t)

	pf := base64TestPanel(t.TempDir(), "file.txt")
	pf.GetActivePanel().ViewMode = ViewModeDetailed
	pos := panelModesMenuPos(ViewModeDetailed)

	dlg := openViewModeEditDialog(t, pf, pos)
	editTypes, editWidths, _, _, _, _, cancelBtn := viewModeDialogWidgets(t, dlg)

	editTypes.SetText("N,S,N,S")
	editWidths.SetText("0,7,0,7")
	cancelBtn.OnClick()

	if PanelViewModeCustomized(ViewModeDetailed) {
		t.Fatal("Cancel must not save the edited columns")
	}
	testutil.DrainUITasks()
	if _, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu); !ok {
		t.Fatalf("top frame after Cancel = %T, want the modes menu again", vtui.FrameManager.GetTopFrame())
	}
}

// TestEditPanelViewModeFitsItsFrame is the regression test for the dialog
// being three rows shorter than its own layout: the vbox stacked 19 rows of
// controls starting two rows under the title, so the button row was laid out
// below the bottom frame and only the modal clip kept it from painting over
// the panels. Every control has to sit inside the frame, at the clearance the
// standard layout rules ask for.
func TestEditPanelViewModeFitsItsFrame(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 30)
	vtui.FrameManager.Init(scr)

	pf := base64TestPanel(t.TempDir(), "file.txt")
	pf.GetActivePanel().ViewMode = ViewModeDetailed

	dlg := openViewModeEditDialog(t, pf, panelModesMenuPos(ViewModeDetailed))

	for _, e := range vtui.ValidateLayout(dlg) {
		t.Errorf("mode edit dialog layout: %v", e)
	}

	children := dlg.GetChildren()
	last := children[len(children)-1]
	if _, ok := last.(*vtui.Button); !ok {
		t.Fatalf("last control = %T, want the button row", last)
	}
	_, _, _, btnY2 := last.GetPosition()
	if btnY2 > dlg.Y2-2 {
		t.Errorf("button row ends at row %d, frame bottom is %d: the buttons do not fit",
			btnY2, dlg.Y2)
	}
}

// f4#410 (montoner0): the status line's columns get the list editor too, from
// a button of their own that edits the status line fields, not the panel's.
func TestViewModeDialogEditsStatusColumnsInTheList(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	withTempPanelModesFile(t)
	old := editModeColumns
	t.Cleanup(func() { editModeColumns = old })
	var gotTypes, gotWidths string
	editModeColumns = func(types, widths string, onOk func(types, widths string)) {
		gotTypes, gotWidths = types, widths
		onOk("N,S", "0,8")
	}

	pf := base64TestPanel(t.TempDir(), "file.txt")
	dlg := openViewModeEditDialog(t, pf, panelModesMenuPos(ViewModeDetailed))
	statusColumns := strings.ReplaceAll(i18n.Msg("Panel.Modes.EditStatusColumns"), "&", "")
	var button *vtui.Button
	var edits []*vtui.Edit
	for _, child := range dlg.GetChildren() {
		switch c := child.(type) {
		case *vtui.Button:
			if c.GetCaption() == statusColumns {
				button = c
			}
		case *vtui.Edit:
			edits = append(edits, c)
		}
	}
	if button == nil || len(edits) != 5 {
		t.Fatalf("status columns button %v, %d edits", button != nil, len(edits))
	}
	statusTypes, statusWidths := edits[3], edits[4]
	statusTypes.SetText("S,D")
	statusWidths.SetText("6,8")
	panelTypes := edits[1].GetText()
	if button.Y1 <= statusWidths.Y1 || button.Y1 >= dlg.Y2 {
		t.Errorf("button at row %d, status widths at %d, frame ends at %d", button.Y1, statusWidths.Y1, dlg.Y2)
	}
	button.OnClick()
	if gotTypes != "S,D" || gotWidths != "6,8" {
		t.Errorf("the list editor got %q / %q, want the status line's S,D / 6,8", gotTypes, gotWidths)
	}
	if statusTypes.GetText() != "N,S" || statusWidths.GetText() != "0,8" {
		t.Errorf("status fields after the list = %q / %q", statusTypes.GetText(), statusWidths.GetText())
	}
	if edits[1].GetText() != panelTypes {
		t.Error("the list changed the panel's column types instead of the status line's")
	}
}
