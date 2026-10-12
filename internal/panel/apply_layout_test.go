package panel

import (
	"fmt"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestApplyCommandDialogLayout(t *testing.T) {
	baseStrings := vtui.SnapshotStrings()
	t.Cleanup(func() { vtui.ReplaceStrings(baseStrings) })
	for _, pack := range i18n.LoadAllLanguagePacks() {
		t.Run(pack.Name, func(t *testing.T) {
			vtui.ReplaceStrings(baseStrings)
			vtui.AddStrings(pack.Strings)
			t.Cleanup(testutil.SwapFrameManager(t))
			screen := vtui.NewSilentScreenBuf()
			screen.AllocBuf(160, 60)
			vtui.FrameManager.Init(screen)
			theme.SetDefaultF4Palette()
			showApplyCommandDialog(&ApplyCommandSession{Targets: []string{"one.txt"}})
			dlg := vtui.FrameManager.GetTopFrame().(interface {
				vtui.Frame
				vtui.Container
				ChangeSize(int, int)
			})
			x1, _, x2, _ := dlg.GetPosition()
			if x2-x1+1 != 72 {
				t.Error("initial width must remain 72")
			}
			minimumWidth := dlg.(*applyCommandDialog).MinW
			if pack.Name == "en" && minimumWidth != 48 || pack.Name == "ru" && minimumWidth != 59 {
				t.Errorf("minimum width = %d, want en=48, ru=59", minimumWidth)
			}
			t.Logf("minimum width: %d", minimumWidth)
			check := func(t *testing.T) {
				t.Helper()
				x1, y1, x2, y2 := dlg.GetPosition()
				if y2-y1+1 != 10 {
					t.Errorf("height = %d, want fixed 10", y2-y1+1)
				}
				var edits []*vtui.Edit
				var buttons []*vtui.Button
				var separators []*vtui.Separator
				var combo *vtui.ComboBox
				var unlimited *vtui.Checkbox
				for _, item := range dlg.GetChildren() {
					switch c := item.(type) {
					case *vtui.Edit:
						edits = append(edits, c)
					case *vtui.Button:
						buttons = append(buttons, c)
					case *vtui.Separator:
						separators = append(separators, c)
					case *vtui.ComboBox:
						combo = c
					case *vtui.Checkbox:
						unlimited = c
					}
				}
				if len(edits) != 2 || len(buttons) != 2 || combo == nil || unlimited == nil {
					t.Fatal("missing command, workers, mode or action buttons")
				}
				if len(separators) != 2 {
					t.Errorf("separators = %d, want parameters and buttons separators", len(separators))
				} else {
					for i, row := range []int{y1 + 4, y2 - 2} {
						sx, sy, sx2, sy2 := separators[i].GetPosition()
						if sx != x1 || sx2 != x2 || sy != row || sy2 != row {
							t.Errorf("separator %d must span frame at row %d", i, row)
						}
					}
				}
				ex, ey, ex2, _ := edits[0].GetPosition()
				if ex != x1+2 || ex2 != x2-2 || ey != y1+2 {
					t.Error("command field must fill width below its prompt")
				}
				cx, cy, cx2, _ := combo.GetPosition()
				if cy != y1+5 || cx2-cx+1 != 23 {
					t.Error("mode field must keep width 23")
				}
				wx, wy, wx2, _ := edits[1].GetPosition()
				ux, uy, ux2, _ := unlimited.GetPosition()
				if wy != y1+6 || uy != wy || ux2 > x2-2 || ux-wx2-1 != 2 || wx2-wx+1 != 4 {
					t.Error("worker field must keep width 4 and unlimited must follow it with a fixed two-cell gap")
				}
				if x2-x1+1 == minimumWidth && ux2 != x2-2 {
					t.Error("minimum width must leave exactly the normal side margin after unlimited")
				}
				bx, by, bx2, _ := buttons[0].GetPosition()
				cx, by2, cx2, _ := buttons[1].GetPosition()
				if cx-bx2-1 != 2 || by != by2 || by != y2-1 {
					t.Error("buttons must be on last inner row, with exactly two cells between them")
				}
				if pad := (bx - x1) - (x2 - cx2); pad < -1 || pad > 1 {
					t.Error("button group is not centered")
				}
				dlg.Show(screen)
				for y := y1 + 1; y < y2; y++ {
					blank := true
					for x := x1 + 1; x < x2; x++ {
						c := screen.GetCell(x, y).Char
						if c != 0 && c != ' ' {
							blank = false
						}
					}
					if blank && y != y1+2 {
						t.Errorf("blank inner row %d", y-y1)
					}
					if y == y1+4 || y == y2-2 {
						line := screen.GetCell(x1+1, y).Char
						for x := x1 + 1; x < x2; x++ {
							c := screen.GetCell(x, y).Char
							if c == 0 || c == ' ' || c != line {
								t.Errorf("separator row %d has a gap", y-y1)
								break
							}
						}
					}
				}
				rules := vtui.DefaultLayoutRules
				rules.FrameClearanceY = 0
				rules.MaxWidth = 158
				vtui.AssertLayoutWithRules(t, dlg, rules)
			}
			t.Run("initial", func(t *testing.T) { check(t) })
			for _, size := range [][2]int{{minimumWidth, 10}, {90, 20}, {minimumWidth - 1, 4}, {72, 10}} {
				dlg.ChangeSize(size[0], size[1])
				x1, _, x2, _ := dlg.GetPosition()
				if x2-x1+1 != max(minimumWidth, size[0]) {
					t.Errorf("width = %d, want %d", x2-x1+1, max(minimumWidth, size[0]))
				}
				t.Run(fmt.Sprintf("size%dx%d", size[0], size[1]), func(t *testing.T) { check(t) })
			}
			for _, size := range [][2]int{{minimumWidth, 20}, {120, 40}, {80, 25}, {160, 60}} {
				screen.AllocBuf(size[0], size[1])
				dlg.ResizeConsole(size[0], size[1])
				t.Run(fmt.Sprintf("screen%dx%d", size[0], size[1]), func(t *testing.T) { check(t) })
			}
			for _, delta := range [][2]int{{10, 0}, {10, 5}, {-10, -5}, {-100, 5}} {
				x1, _, x2, y2 := dlg.GetPosition()
				mouse := func(x, y int, pressed bool) {
					e := &vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: testutil.Int16(x), MouseY: testutil.Int16(y)}
					if pressed {
						e.KeyDown = true
						e.ButtonState = vtinput.FromLeft1stButtonPressed
					}
					dlg.ProcessMouse(e)
				}
				mouse(x2, y2, true)
				mouse(x2+delta[0], y2+delta[1], true)
				mouse(x2+delta[0], y2+delta[1], false)
				nx, _, nx2, ny2 := dlg.GetPosition()
				if nx2-nx+1 != max(minimumWidth, x2-x1+1+delta[0]) || ny2 != y2 {
					t.Error("corner drag must change width only and respect the localized minimum")
				}
				t.Run(fmt.Sprintf("drag%d,%d", delta[0], delta[1]), func(t *testing.T) { check(t) })
			}
		})
	}
}

func TestApplyCommandDialogPreservesControls(t *testing.T) {
	oldConfig, oldTemplate, oldHistory := config.App, LastApplyCommandTemplate, vtui.GlobalHistoryProvider
	t.Cleanup(func() {
		config.App, LastApplyCommandTemplate, vtui.GlobalHistoryProvider = oldConfig, oldTemplate, oldHistory
	})
	vtui.GlobalHistoryProvider = stubHistoryProvider{"ApplyCmd": {"echo previous"}}
	for _, workers := range []int{7, 0} {
		for _, template := range []string{"", "echo current"} {
			t.Run(fmt.Sprintf("workers%d/template%s", workers, template), func(t *testing.T) {
				t.Cleanup(testutil.SwapFrameManager(t))
				screen := vtui.NewSilentScreenBuf()
				screen.AllocBuf(120, 40)
				vtui.FrameManager.Init(screen)
				theme.SetDefaultF4Palette()
				config.App.ApplyCommandParallelism = workers
				LastApplyCommandTemplate = template
				showApplyCommandDialog(&ApplyCommandSession{Targets: []string{"one.txt"}})
				d := vtui.FrameManager.GetTopFrame().(*applyCommandDialog)
				want := template
				if want == "" {
					want = "echo previous"
				}
				if d.command.GetText() != want || d.command.HistoryID != "ApplyCmd" || !d.command.ShowHistoryButton || !d.command.DeduplicateHistory || len(d.command.History) != 1 {
					t.Fatal("initial command or history settings were lost")
				}
				if !d.ShowClose || d.GetHelp() != "ApplyCmd" || !d.mode.DropdownOnly || d.mode.Menu.SelectPos != 0 || d.mode.Edit.GetText() != i18n.Msg("ApplyCommand.ModeSequential") {
					t.Fatal("window or initial mode settings were lost")
				}
				if workers == 7 && d.workers.GetText() != "7" {
					t.Fatal("configured worker count was lost")
				}
				if (d.unlimited.State == 1) != (workers == 0) || !d.workers.IsDisabled() || !d.unlimited.IsDisabled() {
					t.Fatal("initial sequential mode must disable worker controls")
				}
				d.mode.Menu.OnAction(1)
				if d.unlimited.IsDisabled() || d.workers.IsDisabled() != (workers == 0) {
					t.Fatal("parallel mode must enable applicable controls")
				}
				d.unlimited.State = 0
				d.unlimited.OnChange(0)
				if d.workers.IsDisabled() {
					t.Fatal("worker field must respond to unlimited checkbox")
				}
				d.ChangeSize(62, 30)
				if d.command.GetText() != want {
					t.Fatal("resizing changed command")
				}
				for _, item := range d.GetChildren() {
					if b, ok := item.(*vtui.Button); ok && b.IsDefault {
						if b.OnClick == nil {
							t.Fatal("run callback was lost")
						}
					}
				}
				if !d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_ESCAPE}) || !d.IsDone() {
					t.Fatal("Escape must cancel the dialog")
				}
				if LastApplyCommandTemplate != template {
					t.Fatal("cancel changed last template")
				}
			})
		}
	}
}
