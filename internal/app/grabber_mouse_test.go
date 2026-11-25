package app

import (
	"testing"

	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestForcedMouseSelectionStartsAtShiftClick(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := setupGrabberScreen(t)
	vtui.FrameManager.Push(vtui.NewDesktop())

	press := mouseEvent(1, 0, vtinput.FromLeft1stButtonPressed, false, true)
	press.ControlKeyState = vtinput.ShiftPressed
	if !handleForcedMouseSelectionEvent(press) {
		t.Fatal("Shift+left-click should start screen selection")
	}

	grabber, ok := vtui.FrameManager.GetTopFrame().(*GrabberFrame)
	if !ok {
		t.Fatalf("top frame = %T, want *GrabberFrame", vtui.FrameManager.GetTopFrame())
	}
	if !grabber.hasSnap || grabber.snapW != scr.Width() || grabber.snapH != scr.Height() {
		t.Fatalf("grabber snapshot = %v %dx%d, want initialized %dx%d",
			grabber.hasSnap, grabber.snapW, grabber.snapH, scr.Width(), scr.Height())
	}
	if grabber.anchorX != 1 || grabber.anchorY != 0 || grabber.curX != 1 || grabber.curY != 0 {
		t.Fatalf("selection anchor/current = (%d,%d)/(%d,%d), want (1,0)/(1,0)",
			grabber.anchorX, grabber.anchorY, grabber.curX, grabber.curY)
	}
	if !grabber.mouseSelecting {
		t.Fatal("initial Shift+left-click should keep mouse selection active")
	}

	grabber.ProcessMouse(mouseEvent(5, 2, vtinput.FromLeft1stButtonPressed, true, true))
	grabber.ProcessMouse(mouseEvent(5, 2, 0, false, false))
	if grabber.mouseSelecting {
		t.Fatal("mouse release should end selection")
	}
	if got := grabber.copyText(); got == "" {
		t.Fatal("Shift+mouse drag should select non-empty screen text")
	}
}

func TestForcedMouseSelectionLeavesOtherMouseGesturesAlone(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	setupGrabberScreen(t)
	vtui.FrameManager.Push(vtui.NewDesktop())

	tests := []struct {
		name  string
		mods  vtinput.ControlKeyState
		moved bool
		btn   uint32
		down  bool
	}{
		{name: "plain click", btn: vtinput.FromLeft1stButtonPressed, down: true},
		{name: "ctrl shift", mods: vtinput.LeftCtrlPressed | vtinput.ShiftPressed, btn: vtinput.FromLeft1stButtonPressed, down: true},
		{name: "alt shift", mods: vtinput.LeftAltPressed | vtinput.ShiftPressed, btn: vtinput.FromLeft1stButtonPressed, down: true},
		{name: "middle button", mods: vtinput.ShiftPressed, btn: vtinput.FromLeft2ndButtonPressed, down: true},
		{name: "motion", mods: vtinput.ShiftPressed, moved: true, btn: vtinput.FromLeft1stButtonPressed, down: true},
		{name: "release", mods: vtinput.ShiftPressed, btn: 0, down: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := mouseEvent(1, 0, tt.btn, tt.moved, tt.down)
			e.ControlKeyState = tt.mods
			if handleForcedMouseSelectionEvent(e) {
				t.Fatal("event should remain available to normal mouse dispatch")
			}
			if _, ok := vtui.FrameManager.GetTopFrame().(*GrabberFrame); ok {
				t.Fatal("unexpected grabber opened")
			}
		})
	}
}

func TestViewerTextAreaAllowsUnmodifiedSelectionGesture(t *testing.T) {
	vv := &viewer.ViewerView{}
	vv.SetPosition(10, 4, 50, 20)

	for _, tt := range []struct {
		name string
		x, y int16
		want bool
	}{
		{name: "text", x: 11, y: 5, want: true},
		{name: "title bar", x: 11, y: 4},
		{name: "scrollbar", x: 50, y: 5},
		{name: "outside", x: 51, y: 5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := mouseEvent(tt.x, tt.y, vtinput.FromLeft1stButtonPressed, false, true)
			if got := viewerTextAreaAtMouse(vv, e); got != tt.want {
				t.Fatalf("viewerTextAreaAtMouse(%d,%d) = %t, want %t", tt.x, tt.y, got, tt.want)
			}
		})
	}

	vv.HexMode = true
	if viewerTextAreaAtMouse(vv, mouseEvent(11, 5, vtinput.FromLeft1stButtonPressed, false, true)) {
		t.Fatal("hex viewer should keep the plain mouse gesture")
	}
}

func TestForcedMouseSelectionLeavesVisibleKeyBarClickAlone(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	setupGrabberScreen(t)
	vtui.FrameManager.Push(vtui.NewDesktop())

	keyBar := vtui.NewKeyBar()
	keyBar.SetPosition(0, testGrabberH-1, testGrabberW-1, testGrabberH-1)
	keyBar.SetVisible(true)
	vtui.FrameManager.KeyBar = keyBar

	press := mouseEvent(1, testGrabberH-1, vtinput.FromLeft1stButtonPressed, false, true)
	press.ControlKeyState = vtinput.ShiftPressed
	if handleForcedMouseSelectionEvent(press) {
		t.Fatal("Shift+click on the visible key bar must reach KeyBar.ProcessMouse")
	}
	if _, ok := vtui.FrameManager.GetTopFrame().(*GrabberFrame); ok {
		t.Fatal("Shift+click on the visible key bar unexpectedly opened a grabber")
	}
}

func TestForcedMouseSelectionDragThroughKeyBarCopiesKeyBarRow(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := setupGrabberScreen(t)
	attr := vtui.SetRGBBoth(0, 0xFFFFFF, 0x000000)
	scr.Write(0, testGrabberH-1, vtui.StringToCharInfo("F1 Help / F2 View", attr))
	vtui.FrameManager.Push(vtui.NewDesktop())

	keyBar := vtui.NewKeyBar()
	keyBar.SetPosition(0, testGrabberH-1, testGrabberW-1, testGrabberH-1)
	keyBar.SetVisible(true)
	vtui.FrameManager.KeyBar = keyBar

	press := mouseEvent(0, 0, vtinput.FromLeft1stButtonPressed, false, true)
	press.ControlKeyState = vtinput.ShiftPressed
	if !handleForcedMouseSelectionEvent(press) {
		t.Fatal("Shift+left-click should start screen selection")
	}
	grabber, ok := vtui.FrameManager.GetTopFrame().(*GrabberFrame)
	if !ok {
		t.Fatalf("top frame = %T, want *GrabberFrame", vtui.FrameManager.GetTopFrame())
	}
	if vtui.FrameManager.KeyBar != nil {
		t.Fatal("active grabber must own pointer motion instead of the key bar")
	}

	grabber.ProcessMouse(mouseEvent(testGrabberW-1, testGrabberH-1, vtinput.FromLeft1stButtonPressed, true, true))
	grabber.ProcessMouse(mouseEvent(testGrabberW-1, testGrabberH-1, 0, false, false))
	if got := grabber.copyText(); got != "hello world\nsecond line trailing spaces\nthird\n\n\nF1 Help / F2 View" {
		t.Fatalf("selection through key bar = %q", got)
	}

	grabber.cancel()
	if vtui.FrameManager.KeyBar != keyBar {
		t.Fatal("key bar was not restored after grabber exit")
	}
}

func TestForcedMouseSelectionDoesNotNestGrabbers(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	setupGrabberScreen(t)
	vtui.FrameManager.Push(vtui.NewDesktop())

	first := mouseEvent(1, 0, vtinput.FromLeft1stButtonPressed, false, true)
	first.ControlKeyState = vtinput.ShiftPressed
	if !handleForcedMouseSelectionEvent(first) {
		t.Fatal("first Shift+left-click should start selection")
	}
	grabber := vtui.FrameManager.GetTopFrame()
	second := mouseEvent(2, 0, vtinput.FromLeft1stButtonPressed, false, true)
	second.ControlKeyState = vtinput.ShiftPressed
	if handleForcedMouseSelectionEvent(second) {
		t.Fatal("a Shift+left-click must not open a nested grabber")
	}
	if vtui.FrameManager.GetTopFrame() != grabber {
		t.Fatal("existing grabber was replaced")
	}
}
