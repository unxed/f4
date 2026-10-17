package wincon

// The arithmetic and the decisions behind the layered overlay, kept apart from
// the syscalls so both can be tested on any platform.
//
// The overlay is a top-level window with no parent and no owner
// (docs/WINCON_805_HANDOVER.md step 3). That shape is what makes it safe --
// a child of another process's window couples the two input queues and freezes
// the console, measured in the field as F22 -- but it also means the window has
// to do for itself two things a child got for free: it must be told where the
// console is, and it must be told what the console looks like now. Both answers
// are computed here.

// premultiplyBGRA converts a top-down RGBA frame into the premultiplied BGRA
// that UpdateLayeredWindow requires, writing w*h*4 bytes into dst.
//
// Two conversions at once, and both matter. The byte order is Windows' (blue
// first), and every colour channel is scaled by its own alpha: a layered window
// blends with AC_SRC_ALPHA, which assumes the multiplication has already
// happened. Feeding it straight alpha makes translucent pixels too bright, and
// makes fully transparent ones show as a coloured haze rather than nothing --
// which on a console overlay looks like a dirty rectangle around the picture.
func premultiplyBGRA(dst, src []byte, w, h, stride int) {
	if w <= 0 || h <= 0 {
		return
	}
	for y := 0; y < h; y++ {
		srcRow := y * stride
		dstRow := y * w * 4
		for x := 0; x < w; x++ {
			s := srcRow + x*4
			d := dstRow + x*4
			if s+3 >= len(src) || d+3 >= len(dst) {
				return
			}
			a := uint32(src[s+3])
			dst[d+0] = byte(uint32(src[s+2]) * a / 255) // B
			dst[d+1] = byte(uint32(src[s+1]) * a / 255) // G
			dst[d+2] = byte(uint32(src[s+0]) * a / 255) // R
			dst[d+3] = byte(a)
		}
	}
}

// consoleObservation is what one tick of the tracker read from the console
// window. Everything in it comes from a call that only reads: the pump thread
// must never wait on conhost (F7/F8).
type consoleObservation struct {
	Alive        bool // IsWindow
	Visible      bool // IsWindowVisible
	Iconic       bool // IsIconic
	ClientX      int  // ClientToScreen of the client origin
	ClientY      int
	ClientW      int // GetClientRect
	ClientH      int
	PrevInZOrder uintptr // GetWindow(target, GW_HWNDPREV)
	PrevTopmost  bool    // that window has WS_EX_TOPMOST
	Self         uintptr // our own hwnd, to compare against PrevInZOrder
}

// trackerState is what the tracker believes is true right now.
type trackerState struct {
	WantVisible bool // the caller asked for a picture to be on screen
	OnScreen    bool // the window is currently shown
	X, Y        int  // where the window was last put, in screen coordinates
	// OffX, OffY is where the picture's frame starts in the console's client
	// area: the window goes there, not to the client origin (f4#1885).
	OffX, OffY int
}

// trackOps is what the tick should do. Each field maps to exactly one syscall,
// so the decision can be tested without making any.
type trackOps struct {
	CloseOverlay bool // the console is gone
	Hide         bool
	Show         bool
	MoveTo       bool
	X, Y         int
	Restack      bool
	After        uintptr // the window to sit above; 0 means HWND_TOP
}

// trackStep decides what one tick of the tracking timer should do.
//
// The rules, in the order they can each cancel the ones below:
//
//   - a console that no longer exists ends the overlay, because a top-level
//     window is not destroyed with it the way a child was;
//   - a minimized or hidden console hides the picture: its client rectangle
//     still reports a size, so without this the overlay would sit over the
//     desktop showing a picture for a window nobody can see;
//   - a moved console moves the picture, since nothing repositions a
//     parentless window for us;
//   - and the overlay is kept directly above the console in the z-order, so
//     anything the user raises above the console covers the picture too. If
//     the window directly above is topmost, the overlay goes to the top of the
//     non-topmost band instead: matching a topmost window would put the
//     picture above every ordinary window on the desktop.
func trackStep(s trackerState, o consoleObservation) trackOps {
	if !o.Alive {
		return trackOps{CloseOverlay: true}
	}
	if !s.WantVisible {
		if s.OnScreen {
			return trackOps{Hide: true}
		}
		return trackOps{}
	}
	if o.Iconic || !o.Visible || o.ClientW <= 0 || o.ClientH <= 0 {
		if s.OnScreen {
			return trackOps{Hide: true}
		}
		return trackOps{}
	}

	ops := trackOps{}
	if !s.OnScreen {
		ops.Show = true
	}
	if x, y := o.ClientX+s.OffX, o.ClientY+s.OffY; x != s.X || y != s.Y || !s.OnScreen {
		ops.MoveTo = true
		ops.X, ops.Y = x, y
	}
	if o.PrevInZOrder != o.Self {
		ops.Restack = true
		ops.After = o.PrevInZOrder
		if o.PrevTopmost {
			ops.After = 0 // HWND_TOP of the ordinary band
		}
	}
	return ops
}
