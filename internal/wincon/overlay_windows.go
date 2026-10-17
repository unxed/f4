//go:build windows

package wincon

// A picture over a classic Windows console.
//
// Windows Terminal renders sixel itself, so nothing here runs there and
// nothing should: pictures go down the wire as they do on any capable
// terminal. This is for conhost — cmd.exe in its own window — which has no
// image protocol of any kind and never will.
//
// The overlay is a **top-level layered window with no parent and no owner**,
// placed over the console's client area and kept there by a timer. It used
// to be a child of the console window, which is the shape PictureView takes
// in Far and the shape the X side takes, and it was wrong here for one
// reason that no amount of care around it could fix: the console window
// belongs to conhost.exe, and a parent/child or owner/owned relationship
// across processes attaches the two threads' input queues, transitively
// (Raymond Chen, 2013-04-12 and 2011-03-31). Measured in the field on
// 10.0.22000: for as long as the child window existed the console accepted
// no keys and no mouse (docs/WINCON_805_HANDOVER.md F7, F22). That is the
// "flickers, freezes, 17% CPU" of issue #805. A window with no parent and
// no owner has nothing to couple; the same field run showed the layered one
// visible, above the console, surviving a console repaint and freezing
// nothing (F23).
//
// What the child got for free, this window does for itself: a timer on the
// pump thread reads where the console is and what it looks like and moves,
// hides, shows or restacks the overlay to match (trackStep in layered.go,
// tested without a window). The frame goes in with UpdateLayeredWindow --
// position, size and premultiplied pixels in one call -- so there is no
// WM_PAINT, no background to erase, no black rectangle before the first
// paint, and no region: gaps in the picture are alpha zero.
//
// **Two invariants, and both are load-bearing.** No caller ever waits on the
// pump thread: the public methods write what they want into overlay_state.go
// and post one thread message, and every user32/gdi32 call that touches the
// window happens on the pump thread. And the console window is only ever
// read: GetClientRect, ClientToScreen, IsWindow, IsWindowVisible, IsIconic,
// GetWindow and GetWindowLongPtrW, none of which sends it a message. Never
// SendMessage, SetWindowPos, ShowWindow or anything else that would wait on
// conhost -- that is the wait the child window used to be stuck in.

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetConsoleWindow        = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleScreenBuffer  = kernel32.NewProc("GetConsoleScreenBufferInfo")
	procGetCurrentConsoleFontEx = kernel32.NewProc("GetCurrentConsoleFontEx")
	procGetStdHandle            = kernel32.NewProc("GetStdHandle")
	procGetModuleHandleW        = kernel32.NewProc("GetModuleHandleW")

	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procGetClassNameW       = user32.NewProc("GetClassNameW")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPeekMessageW        = user32.NewProc("PeekMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procGetCurrentThreadId  = kernel32.NewProc("GetCurrentThreadId")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procShowWindow          = user32.NewProc("ShowWindow")
	procGetClientRect       = user32.NewProc("GetClientRect")
	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procClientToScreen      = user32.NewProc("ClientToScreen")
	procIsWindow            = user32.NewProc("IsWindow")
	procIsIconic            = user32.NewProc("IsIconic")
	procGetWindow           = user32.NewProc("GetWindow")
	procGetWindowLongPtrW   = user32.NewProc(getWindowLongPtrProc)
	procSetTimer            = user32.NewProc("SetTimer")
	procKillTimer           = user32.NewProc("KillTimer")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
)

const (
	wsVisible = 0x10000000

	swpNoActivate = 0x0010
	swpNoMove     = 0x0002
	swpNoSize     = 0x0001

	swHide           = 0
	swShowNoActivate = 4

	wsPopup         = 0x80000000
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000
	wsExTopmost     = 0x00000008
	gwlExStyle      = ^uintptr(19) // -20
	gwHwndPrev      = 3
	wmTimer         = 0x0113
	ulwAlpha        = 0x00000002
	acSrcOver       = 0x00
	acSrcAlpha      = 0x01
	trackTimerID    = 1
	trackTimerMs    = 33

	wmDestroy     = 0x0002
	wmApp         = 0x8000
	wmOverlayQuit = wmApp + 1
	wmOverlaySync = wmApp + 2
	pmNoRemove    = 0x0000

	diRGBColors     = 0
	srcCopy         = 0x00CC0020
	colorOnColor    = 3
	biRGB           = 0
	stdOutputHandle = ^uintptr(10) // STD_OUTPUT_HANDLE is -11
)

const (
	// overlayReadyTimeout bounds the wait for the window to be created.
	// Creating it is the call that attaches the input queues, so it is the
	// one place at startup where a wedged conhost could hold f4 up. A
	// picture is not worth a hang, and going without the overlay is a
	// perfectly good outcome.
	overlayReadyTimeout = 5 * time.Second
)

type rect struct{ Left, Top, Right, Bottom int32 }

type point struct{ X, Y int32 }

type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassExW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type size struct{ Cx, Cy int32 }

type blendFunction struct {
	BlendOp, BlendFlags, SourceConstantAlpha, AlphaFormat byte
}

type coord struct{ X, Y int16 }

type smallRect struct{ Left, Top, Right, Bottom int16 }

type consoleScreenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

type consoleFontInfoEx struct {
	Size       uint32
	Font       uint32
	FontSize   coord
	FontFamily uint32
	FontWeight uint32
	FaceName   [32]uint16
}

// ConsoleWindow finds the console window and says how much it is worth.
func ConsoleWindow() (uintptr, Source) {
	h, _, _ := procGetConsoleWindow.Call()
	if h == 0 {
		return 0, SourceNone
	}
	visible, _, _ := procIsWindowVisible.Call(h)
	return h, ClassifyConsoleWindow(consoleWindowClass(h), visible != 0)
}

// consoleWindowClass reads the window class name. Under a pseudoconsole this
// is the only cheap fact that separates the terminal's 0x0 helper window from
// a real console window: both answer "visible".
func consoleWindowClass(h uintptr) string {
	var buf [64]uint16
	n, _, _ := procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

// CellSize is the pixel size of one character cell, straight from the console.
// No inference, no rounding, no escape sequence to be swallowed by whoever
// reads standard input next.
func CellSize() (int, int, bool) {
	out, _, _ := procGetStdHandle.Call(stdOutputHandle)
	if out == 0 || out == ^uintptr(0) {
		return 0, 0, false
	}
	var info consoleFontInfoEx
	info.Size = uint32(unsafe.Sizeof(info))
	r, _, _ := procGetCurrentConsoleFontEx.Call(out, 0, uintptr(unsafe.Pointer(&info)))
	if r == 0 || info.FontSize.X <= 0 || info.FontSize.Y <= 0 {
		return 0, 0, false
	}
	return int(info.FontSize.X), int(info.FontSize.Y), true
}

// GridSize is the console's visible size in character cells.
func GridSize() (int, int, bool) {
	out, _, _ := procGetStdHandle.Call(stdOutputHandle)
	if out == 0 || out == ^uintptr(0) {
		return 0, 0, false
	}
	var info consoleScreenBufferInfo
	r, _, _ := procGetConsoleScreenBuffer.Call(out, uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, 0, false
	}
	w := int(info.Window.Right) - int(info.Window.Left) + 1
	h := int(info.Window.Bottom) - int(info.Window.Top) + 1
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// Overlay is one window over the console.
type Overlay struct {
	// st is what the window should look like. Callers write it, the pump
	// thread applies it; see the invariant at the top of the file.
	st overlayState

	// mu guards the handle and the frame buffer, both of which the pump
	// thread reads while it paints.
	mu sync.Mutex
	// target is the console window the picture follows. It is read and
	// never written to: not a parent, not an owner, never sent a message.
	// That is the whole of the difference from the child window this
	// replaced (docs/WINCON_805_HANDOVER.md F7, F22, F23).
	target uintptr
	hwnd   uintptr
	// tracker is what the timer believes; only the pump thread touches it.
	tracker trackerState
	// shown remembers whether a frame has ever been pushed, since a layered
	// window has nothing to show before its first UpdateLayeredWindow.
	shown    bool
	threadID uint32
	ready    chan error
	pix      []byte
	pixW     int
	pixH     int

	// stats is what the pump thread did; see stats.go for why it counts
	// rather than logs.
	stats counters
}

// Stats reads the counters. Safe from any thread.
func (o *Overlay) Stats() Stats {
	if o == nil {
		return Stats{}
	}
	return o.stats.snapshot()
}

var (
	classOnce sync.Once
	classAtom uintptr
	classErr  error
	className = mustUTF16("f4ConsoleOverlay")

	// registry maps a window to its overlay, because the window procedure
	// is a C callback and cannot carry one.
	regMu sync.Mutex
	reg   = map[uintptr]*Overlay{}
)

func mustUTF16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

func registerClass() {
	classOnce.Do(func() {
		inst, _, _ := procGetModuleHandleW.Call(0)
		wc := wndClassExW{
			Size:      uint32(unsafe.Sizeof(wndClassExW{})),
			WndProc:   syscall.NewCallback(wndProc),
			Instance:  inst,
			ClassName: className,
		}
		atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			classErr = fmt.Errorf("the overlay window class could not be registered: %w", err)
			return
		}
		classAtom = atom
	})
}

func wndProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	switch message {
	case wmOverlaySync:
		regMu.Lock()
		o := reg[hwnd]
		regMu.Unlock()
		if o != nil {
			o.apply(hwnd)
		}
		return 0

	case wmOverlayQuit:
		procDestroyWindow.Call(hwnd)
		return 0

	case wmDestroy:
		regMu.Lock()
		delete(reg, hwnd)
		regMu.Unlock()
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wparam, lparam)
	return r
}

func (s *overlayState) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// New creates the overlay over the console window.
func New() (*Overlay, error) {
	parent, src := ConsoleWindow()
	if !src.Trusted() {
		return nil, fmt.Errorf("no console window to draw on: found %v", src)
	}
	registerClass()
	if classErr != nil {
		return nil, classErr
	}

	o := &Overlay{target: parent, ready: make(chan error, 1)}
	// The window lives on a thread of its own, pumping its own messages: a
	// window belongs to the thread that created it, and the tracking timer
	// runs there too. Nothing in f4 ever waits for this thread.
	go o.pump()
	select {
	case err := <-o.ready:
		if err != nil {
			return nil, err
		}
		return o, nil
	case <-time.After(overlayReadyTimeout):
		// The pump thread is stuck inside CreateWindowExW, the call
		// that attaches the queues. Mark the overlay finished; the
		// thread tidies up after itself if it ever comes back.
		o.st.close()
		return nil, fmt.Errorf("the console did not answer in %s", overlayReadyTimeout)
	}
}

func (o *Overlay) pump() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// PostThreadMessageW only works after the target has a message queue. Make
	// that guarantee before publishing the window through ready. A thread
	// message also avoids routing the wake-up through a child HWND whose parent
	// belongs to conhost.exe.
	var m msg
	procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmNoRemove)
	tid, _, _ := procGetCurrentThreadId.Call()
	o.mu.Lock()
	o.threadID = uint32(tid)
	o.mu.Unlock()

	inst, _, _ := procGetModuleHandleW.Call(0)
	// A top-level window: no parent, no owner. A child of the console
	// window coupled the two processes' input queues and froze the console
	// for as long as it existed (F7, measured in the field as F22); an owned
	// window does the same, transitively. A parentless one has nothing to
	// couple. Layered, so the frame is pushed with UpdateLayeredWindow and
	// there is never a paint; transparent to the mouse; a tool window, so it
	// has no taskbar button; never activated, so focus stays in the console.
	hwnd, _, err := procCreateWindowExW.Call(
		uintptr(wsExLayered|wsExTransparent|wsExToolWindow|wsExNoActivate),
		classAtom,
		0,
		uintptr(wsPopup),
		0, 0, 1, 1,
		0, 0, inst, 0,
	)
	if hwnd == 0 {
		o.ready <- fmt.Errorf("the overlay window could not be created: %w", err)
		return
	}

	regMu.Lock()
	reg[hwnd] = o
	regMu.Unlock()

	o.mu.Lock()
	o.hwnd = hwnd
	o.mu.Unlock()
	o.ready <- nil

	if o.st.isClosed() {
		// New gave up waiting for this window. Nobody holds it, so it
		// goes now rather than living on as a hole over the console.
		regMu.Lock()
		delete(reg, hwnd)
		regMu.Unlock()
		procDestroyWindow.Call(hwnd)
		return
	}

	procSetTimer.Call(hwnd, trackTimerID, trackTimerMs, 0)
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		if m.HWnd == 0 {
			switch m.Message {
			case wmOverlaySync:
				o.apply(hwnd)
				continue
			case wmOverlayQuit:
				procKillTimer.Call(hwnd, trackTimerID)
				procDestroyWindow.Call(hwnd)
				continue
			}
		}
		if m.HWnd == hwnd && m.Message == wmTimer {
			o.track(hwnd)
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (o *Overlay) Place(r Rect) error {
	if r.W <= 0 || r.H <= 0 {
		o.Hide()
		return nil
	}
	post, ok := o.st.place(r)
	if !ok {
		return fmt.Errorf("the overlay is closed")
	}
	if post {
		o.post()
	}
	return nil
}

// post asks the pump thread to make the window agree with the state. It never
// waits: PostThreadMessageW leaves the message on that thread's queue and
// returns, and that is the whole difference between this and issue #805.
func (o *Overlay) post() {
	o.mu.Lock()
	tid := o.threadID
	o.mu.Unlock()
	if tid == 0 {
		o.st.wakeFailed()
		return
	}
	if r, _, _ := procPostThreadMessageW.Call(uintptr(tid), wmOverlaySync, 0, 0); r == 0 {
		o.st.wakeFailed()
	}
}

// apply is the other half, on the pump thread: every call that shows, hides
// or paints the window is here, in track, and nowhere else.
//
// Position, size and pixels travel in one UpdateLayeredWindow call, which is
// what makes the old rule -- move only in the wake-up that also paints --
// hold by construction, and makes a black rectangle before the first paint
// impossible: a layered window has no contents at all until this call.
func (o *Overlay) apply(hwnd uintptr) {
	ops := o.st.take()
	if ops.Empty() {
		return
	}
	o.stats.applies.Add(1)
	if ops.Hide {
		procShowWindow.Call(hwnd, swHide)
		o.tracker.WantVisible = false
		o.tracker.OnScreen = false
		return
	}
	// SetRegion is accepted and ignored: gaps between thumbnails are alpha
	// zero in the frame already, and a layered window shows nothing there.
	if ops.Move || ops.Invalidate {
		o.push(hwnd)
	}
}

// push composes the cached frame at the console's current client origin and
// hands it to the window. Screen coordinates come from two calls that only
// read the console window; nothing here can wait on conhost.
func (o *Overlay) push(hwnd uintptr) {
	o.mu.Lock()
	pix, w, h := o.pix, o.pixW, o.pixH
	target := o.target
	o.mu.Unlock()
	rect := o.st.currentRect()
	if len(pix) == 0 || w <= 0 || h <= 0 || rect.W <= 0 || rect.H <= 0 {
		return
	}
	origin := point{}
	procClientToScreen.Call(target, uintptr(unsafe.Pointer(&origin)))
	x := int(origin.X) + rect.X
	y := int(origin.Y) + rect.Y

	screenDC, _, _ := procGetDC.Call(0)
	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	bih := bitmapInfoHeader{
		Size:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:    int32(rect.W),
		Height:   -int32(rect.H), // top-down
		Planes:   1,
		BitCount: 32,
	}
	var bits unsafe.Pointer
	dib, _, _ := procCreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bih)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 || bits == nil {
		procDeleteDC.Call(memDC)
		procReleaseDC.Call(0, screenDC)
		o.stats.blank.Add(1)
		return
	}
	dst := unsafe.Slice((*byte)(bits), rect.W*rect.H*4)
	// The frame may be smaller than the placement rectangle; whatever it
	// does not cover stays alpha zero, which is to say invisible.
	scaled := make([]byte, rect.W*rect.H*4)
	blitInto(scaled, rect.W, rect.H, pix, w, h, w*4, 0, 0)
	premultiplyBGRA(dst, scaled, rect.W, rect.H, rect.W*4)

	old, _, _ := procSelectObject.Call(memDC, dib)
	Pt := point{X: int32(x), Y: int32(y)}
	sz := size{Cx: int32(rect.W), Cy: int32(rect.H)}
	src := point{}
	bf := blendFunction{BlendOp: acSrcOver, SourceConstantAlpha: 255, AlphaFormat: acSrcAlpha}
	r, _, _ := procUpdateLayeredWindow.Call(hwnd, screenDC,
		uintptr(unsafe.Pointer(&Pt)), uintptr(unsafe.Pointer(&sz)),
		memDC, uintptr(unsafe.Pointer(&src)), 0,
		uintptr(unsafe.Pointer(&bf)), ulwAlpha)
	procSelectObject.Call(memDC, old)
	procDeleteObject.Call(dib)
	procDeleteDC.Call(memDC)
	procReleaseDC.Call(0, screenDC)
	if r == 0 {
		o.stats.blank.Add(1)
		return
	}
	o.stats.paints.Add(1)
	o.tracker.WantVisible = true
	o.tracker.X, o.tracker.Y = x, y
	o.tracker.OffX, o.tracker.OffY = rect.X, rect.Y
	if !o.shown {
		o.shown = true
		procShowWindow.Call(hwnd, swShowNoActivate)
		o.tracker.OnScreen = true
	}
	// Land directly above the console now rather than at the next tick.
	o.track(hwnd)
}

// track is one tick of following the console: read it, decide, act. The
// decision is trackStep, tested without a window; everything the console
// window is asked here only reads.
func (o *Overlay) track(hwnd uintptr) {
	o.mu.Lock()
	target := o.target
	o.mu.Unlock()
	obs := observeConsole(target, hwnd)
	ops := trackStep(o.tracker, obs)
	if ops.CloseOverlay {
		o.Close()
		return
	}
	if ops.Hide {
		procShowWindow.Call(hwnd, swHide)
		o.tracker.OnScreen = false
		return
	}
	if ops.Show && o.shown {
		procShowWindow.Call(hwnd, swShowNoActivate)
		o.tracker.OnScreen = true
	}
	if ops.MoveTo && o.shown {
		o.stats.moves.Add(1)
		Pt := point{X: int32(ops.X), Y: int32(ops.Y)}
		procUpdateLayeredWindow.Call(hwnd, 0, uintptr(unsafe.Pointer(&Pt)), 0, 0, 0, 0, 0, 0)
		o.tracker.X, o.tracker.Y = ops.X, ops.Y
	}
	if ops.Restack && o.shown {
		procSetWindowPos.Call(hwnd, ops.After, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	}
}

// observeConsole gathers what trackStep needs, with reads only.
func observeConsole(target, self uintptr) consoleObservation {
	var obs consoleObservation
	obs.Self = self
	if alive, _, _ := procIsWindow.Call(target); alive == 0 {
		return obs
	}
	obs.Alive = true
	v, _, _ := procIsWindowVisible.Call(target)
	obs.Visible = v != 0
	ic, _, _ := procIsIconic.Call(target)
	obs.Iconic = ic != 0
	var r rect
	procGetClientRect.Call(target, uintptr(unsafe.Pointer(&r)))
	obs.ClientW, obs.ClientH = int(r.Right-r.Left), int(r.Bottom-r.Top)
	origin := point{}
	procClientToScreen.Call(target, uintptr(unsafe.Pointer(&origin)))
	obs.ClientX, obs.ClientY = int(origin.X), int(origin.Y)
	prev, _, _ := procGetWindow.Call(target, gwHwndPrev)
	obs.PrevInZOrder = prev
	if prev != 0 {
		ex, _, _ := procGetWindowLongPtrW.Call(prev, gwlExStyle)
		obs.PrevTopmost = ex&wsExTopmost != 0
	}
	return obs
}

// Draw hands over the pixels, RGBA, top row first, and asks for a repaint.
func (o *Overlay) Draw(pix []byte, w, h, stride int) error {
	if w <= 0 || h <= 0 || stride < w*4 || len(pix) < stride*h {
		return fmt.Errorf("a %dx%d picture needs %d bytes at stride %d, got %d",
			w, h, stride*h, stride, len(pix))
	}
	buf := make([]byte, w*h*4)
	blitInto(buf, w, h, pix, w, h, stride, 0, 0)

	o.mu.Lock()
	o.pix, o.pixW, o.pixH = buf, w, h
	hwnd := o.hwnd
	o.mu.Unlock()
	if hwnd == 0 {
		return fmt.Errorf("the overlay is closed")
	}
	if o.st.touchPixels() {
		o.post()
	}
	return nil
}

// SetBounds is kept for the caller and does nothing: the gaps between
// thumbnails are alpha zero in the frame, and a layered window shows nothing
// where alpha is zero. A region was the child window's way of achieving the
// same, and it is gone with the child.
func (o *Overlay) SetBounds(rects []Rect) bool {
	o.mu.Lock()
	hwnd := o.hwnd
	o.mu.Unlock()
	return hwnd != 0
}

// Hide takes the picture off the screen without giving up the window.
func (o *Overlay) Hide() {
	if o.st.hide() {
		o.post()
	}
}

// Visible reports what the picture was last asked to be rather than what is on
// the screen this instant: the caller uses it to decide whether to redraw, and
// the pump thread may be a message behind.
func (o *Overlay) Visible() bool {
	return o.st.visible()
}

// ClientSize is the console window's client area in pixels.
func (o *Overlay) ClientSize() (int, int, bool) {
	o.mu.Lock()
	target := o.target
	o.mu.Unlock()
	var r rect
	res, _, _ := procGetClientRect.Call(target, uintptr(unsafe.Pointer(&r)))
	if res == 0 {
		return 0, 0, false
	}
	return int(r.Right - r.Left), int(r.Bottom - r.Top), true
}

// Close destroys the window and stops its thread.
func (o *Overlay) Close() {
	if !o.st.close() {
		return
	}
	o.mu.Lock()
	hwnd := o.hwnd
	tid := o.threadID
	o.hwnd = 0
	o.threadID = 0
	o.mu.Unlock()
	if hwnd == 0 && tid == 0 {
		return
	}
	// Destroying a window has to happen on the thread that made it.
	if tid != 0 {
		procPostThreadMessageW.Call(uintptr(tid), wmOverlayQuit, 0, 0)
		return
	}
	procPostMessageW.Call(hwnd, wmOverlayQuit, 0, 0)
}

// currentRect is the placement most recently asked for, in console client
// coordinates. The pump thread reads it when it composes a frame.
func (s *overlayState) currentRect() Rect {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wantRect
}
