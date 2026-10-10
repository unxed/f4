package findfile

import (
	"context"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestParametersVerticalNavigation(t *testing.T) {
	setupUI(t)
	w := NewParametersWindow()
	mask := vtui.NewEdit(0, 0, 20, "*.txt")
	text := vtui.NewEdit(0, 0, 20, "needle")
	checkbox := vtui.NewCheckbox(0, 0, "Case sensitive", false)
	find := vtui.NewButton(0, 0, "Find")
	invoked := false
	find.OnClick = func() { invoked = true }
	for _, element := range []vtui.UIElement{mask, checkbox, text, find} {
		w.AddItem(element)
	}
	w.SetNavigation(mask, text, find)
	w.SetFocusedItem(mask)
	for _, step := range []struct {
		key  uint16
		want vtui.UIElement
	}{
		{vtinput.VK_DOWN, text}, {vtinput.VK_DOWN, find}, {vtinput.VK_DOWN, mask},
		{vtinput.VK_UP, find}, {vtinput.VK_UP, text}, {vtinput.VK_UP, mask},
	} {
		if !w.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: step.key}) || w.GetFocusedItem() != step.want {
			t.Fatal("Up/Down did not cycle through primary fields")
		}
		w.ProcessKey(&vtinput.InputEvent{VirtualKeyCode: step.key})
		if w.GetFocusedItem() != step.want {
			t.Fatal("key release moved focus again")
		}
	}
	if invoked || mask.GetText() != "*.txt" || text.GetText() != "needle" || checkbox.State != 0 {
		t.Fatal("navigation invoked Find or changed the search request")
	}
	w.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
	if w.GetFocusedItem() != checkbox {
		t.Fatal("Tab must retain normal access to checkboxes")
	}
	w.SetFocusedItem(find)
	w.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	if !invoked {
		t.Fatal("Enter on Find no longer submits")
	}
}

func TestParametersWindowUsesResizedSpace(t *testing.T) {
	setupUI(t)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(120, 45)
	vtui.FrameManager.Init(scr)
	w := NewParametersWindow()
	edit := vtui.NewEdit(w.X1+2, w.Y1+3, 74, "*.txt")
	separator := vtui.NewSeparator(w.X1+2, w.Y1+6, 74, false, false)
	button := vtui.NewButton(0, 0, "Find")
	w.AddItem(edit)
	w.AddItem(separator)
	w.AddItem(button)
	buttons := vtui.NewHBoxLayout(0, 0, 74, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Add(button, vtui.Margins{}, vtui.AlignTop)
	w.SetLayout(buttons)
	w.SetFocusedItem(edit)
	w.Show(scr)
	initialWidth := edit.X2 - edit.X1
	w.ChangeSize(100, 35)
	w.Show(scr)
	if edit.X2-edit.X1 <= initialWidth || edit.X2 != w.X2-2 || separator.X2 != w.X2-2 {
		t.Fatal("inputs and separator did not grow with the form")
	}
	if button.Y1 != w.Y2-2 || button.X1+button.X2 != w.X1+w.X2 {
		t.Fatal("buttons are not centered at the bottom")
	}
	if edit.GetText() != "*.txt" || w.GetFocusedItem() != edit || !edit.IsFocused() {
		t.Fatal("resizing changed input text or focus")
	}
	if edit.Y1-w.Y1 != 3 || separator.Y1-w.Y1 != 6 || !w.ShowZoom {
		t.Fatal("form rows moved or maximize button is missing")
	}
	w.ResizeConsole(120, 45)
	w.Show(scr)
	if w.X2-w.X1+1 != 100 || w.Y2-w.Y1+1 != 35 {
		t.Fatal("console resize restored the original form size")
	}
	w.ToggleZoom()
	w.Show(scr)
	if edit.X2 != w.X2-2 || button.Y1 != w.Y2-2 {
		t.Fatal("maximized form did not use available space")
	}
	w.ToggleZoom()
	w.Show(scr)
	if w.X2-w.X1+1 != 100 || w.Y2-w.Y1+1 != 35 {
		t.Fatal("restore lost resized bounds")
	}
	vtui.FrameManager.Push(w)
	base := vfs.NewOSVFS(t.TempDir())
	provider := &streamVFS{VFS: base, search: func(ctx context.Context, _ vfs.FindQuery, _ func(vfs.FoundEntry)) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	results := w.Expand([]vtui.UIElement{edit, separator}, provider, base.GetPath(), edit.GetText(), "", Options{}, Host{})
	results.Show(scr)
	if results.Window != w.Window || results.Y2-results.Y1+1 < 35 || vtui.FrameManager.GetTopFrame() != results {
		t.Fatal("search replaced or shrank the resized form")
	}
	if edit.X2 != results.X2-2 || !edit.IsDisabled() {
		t.Fatal("expanded search lost the submitted field layout or lock")
	}
	results.Stop()
	drainUntil(t, func() bool { return !results.running })
	results.Close()
}
