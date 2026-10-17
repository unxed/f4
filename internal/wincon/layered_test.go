package wincon

import "testing"

func TestPremultiplyBGRAConvertsOrderAndScalesByAlpha(t *testing.T) {
	// One opaque, one transparent, one half-transparent pixel: the three cases
	// that go wrong differently. RGBA in, BGRA premultiplied out.
	src := []byte{
		200, 100, 50, 255, // opaque
		200, 100, 50, 0, // fully transparent
		200, 100, 50, 128, // half
	}
	dst := make([]byte, 3*4)
	premultiplyBGRA(dst, src, 3, 1, 12)

	if got := dst[0:4]; got[0] != 50 || got[1] != 100 || got[2] != 200 || got[3] != 255 {
		t.Fatalf("opaque pixel: got %v, want BGRA 50,100,200,255", got)
	}
	// A transparent pixel must be all zero: leaving the colour behind is what
	// paints a coloured haze where nothing should be drawn.
	for i, v := range dst[4:8] {
		if v != 0 {
			t.Fatalf("transparent pixel byte %d is %d, want 0 (%v)", i, v, dst[4:8])
		}
	}
	if got := dst[8:12]; got[0] != 25 || got[1] != 50 || got[2] != 100 || got[3] != 128 {
		t.Fatalf("half-transparent pixel: got %v, want each channel scaled by 128/255", got)
	}
}

func TestPremultiplyBGRAStopsAtShortBuffers(t *testing.T) {
	// A short source must not panic: the frame arrives from the caller.
	dst := make([]byte, 16)
	premultiplyBGRA(dst, []byte{1, 2, 3}, 2, 2, 8)
}

func aliveConsole() consoleObservation {
	return consoleObservation{
		Alive: true, Visible: true,
		ClientX: 100, ClientY: 200, ClientW: 800, ClientH: 400,
		PrevInZOrder: 0x99, Self: 0x99,
	}
}

func TestTrackStepClosesWhenTheConsoleIsGone(t *testing.T) {
	// A top-level window outlives the console; a child used to be destroyed
	// with it. Without this the picture would hang over the desktop.
	ops := trackStep(trackerState{WantVisible: true, OnScreen: true}, consoleObservation{})
	if !ops.CloseOverlay {
		t.Fatalf("a dead console must close the overlay, got %+v", ops)
	}
}

func TestTrackStepHidesForAMinimizedOrHiddenConsole(t *testing.T) {
	for name, o := range map[string]consoleObservation{
		"minimized": func() consoleObservation { o := aliveConsole(); o.Iconic = true; return o }(),
		"hidden":    func() consoleObservation { o := aliveConsole(); o.Visible = false; return o }(),
		"no client": func() consoleObservation { o := aliveConsole(); o.ClientW = 0; return o }(),
	} {
		ops := trackStep(trackerState{WantVisible: true, OnScreen: true}, o)
		if !ops.Hide || ops.Show || ops.MoveTo {
			t.Errorf("%s console: got %+v, want a plain hide", name, ops)
		}
	}
}

func TestTrackStepFollowsTheConsoleWhenItMoves(t *testing.T) {
	s := trackerState{WantVisible: true, OnScreen: true, X: 100, Y: 200}
	if ops := trackStep(s, aliveConsole()); ops.MoveTo {
		t.Fatalf("a console that did not move must not be chased: %+v", ops)
	}
	o := aliveConsole()
	o.ClientX, o.ClientY = 140, 260
	ops := trackStep(s, o)
	if !ops.MoveTo || ops.X != 140 || ops.Y != 260 {
		t.Fatalf("got %+v, want a move to 140,260", ops)
	}
}

// f4#1885 (sogonov, cmd.exe): a picture whose frame starts inside the client
// area, quick view in the left panel, was moved to the client's top left
// corner by the next tick, since the tick compared the window's place with
// the client origin alone.
func TestTrackStepKeepsTheFrameOffsetInTheClientArea(t *testing.T) {
	s := trackerState{WantVisible: true, OnScreen: true, X: 100 + 96, Y: 200 + 64, OffX: 96, OffY: 64}
	if ops := trackStep(s, aliveConsole()); ops.MoveTo {
		t.Fatalf("a frame in its place inside the client area was moved: %+v", ops)
	}
	o := aliveConsole()
	o.ClientX, o.ClientY = 140, 260
	ops := trackStep(s, o)
	if !ops.MoveTo || ops.X != 140+96 || ops.Y != 260+64 {
		t.Fatalf("got %+v, want a move to %d,%d: the console's move with the frame's offset kept", ops, 140+96, 260+64)
	}
}

func TestTrackStepKeepsTheOverlayDirectlyAboveTheConsole(t *testing.T) {
	s := trackerState{WantVisible: true, OnScreen: true, X: 100, Y: 200}

	o := aliveConsole()
	o.PrevInZOrder = 0x123 // some other window slipped in between
	ops := trackStep(s, o)
	if !ops.Restack || ops.After != 0x123 {
		t.Fatalf("got %+v, want a restack after 0x123", ops)
	}

	// A topmost window above the console must not be matched: sitting with it
	// would put the picture above every ordinary window on the desktop.
	o.PrevTopmost = true
	ops = trackStep(s, o)
	if !ops.Restack || ops.After != 0 {
		t.Fatalf("got %+v, want HWND_TOP of the ordinary band", ops)
	}
}

func TestTrackStepShowsAndPositionsOnFirstAppearance(t *testing.T) {
	ops := trackStep(trackerState{WantVisible: true}, aliveConsole())
	if !ops.Show || !ops.MoveTo || ops.X != 100 || ops.Y != 200 {
		t.Fatalf("got %+v, want show and move to the console's client origin", ops)
	}
}

func TestTrackStepDoesNothingWhenNothingIsWanted(t *testing.T) {
	if ops := trackStep(trackerState{}, aliveConsole()); (ops != trackOps{}) {
		t.Fatalf("got %+v, want no operations", ops)
	}
}
