package ttyx

import (
	"os"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/unxed/vtinput"
)

func TestModifierXMask(t *testing.T) {
	cases := []struct {
		name string
		mods Modifier
		want uint16
	}{
		{name: "none", want: 0},
		{name: "shift", mods: ModShift, want: xproto.ModMaskShift},
		{name: "control", mods: ModCtrl, want: xproto.ModMaskControl},
		{name: "alt", mods: ModAlt, want: xproto.ModMask1},
		{name: "all", mods: ModShift | ModCtrl | ModAlt, want: xproto.ModMaskShift | xproto.ModMaskControl | xproto.ModMask1},
		{name: "unknown bits ignored", mods: Modifier(1 << 15), want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.mods.xmask(); got != tc.want {
				t.Fatalf("xmask(%#x) = %#x, want %#x", tc.mods, got, tc.want)
			}
		})
	}
}

func TestModsFromState(t *testing.T) {
	cases := []struct {
		name  string
		state uint16
		want  vtinput.ControlKeyState
	}{
		{name: "none"},
		{name: "shift", state: xproto.ModMaskShift, want: vtinput.ShiftPressed},
		{name: "control", state: xproto.ModMaskControl, want: vtinput.LeftCtrlPressed},
		{name: "alt", state: xproto.ModMask1, want: vtinput.LeftAltPressed},
		{name: "locks", state: xproto.ModMaskLock | xproto.ModMask2, want: vtinput.CapsLockOn | vtinput.NumLockOn},
		{name: "all", state: xproto.ModMaskShift | xproto.ModMaskControl | xproto.ModMask1 | xproto.ModMaskLock | xproto.ModMask2, want: vtinput.ShiftPressed | vtinput.LeftCtrlPressed | vtinput.LeftAltPressed | vtinput.CapsLockOn | vtinput.NumLockOn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modsFromState(tc.state); got != tc.want {
				t.Fatalf("modsFromState(%#x) = %#x, want %#x", tc.state, got, tc.want)
			}
		})
	}
}

func TestKeyStateAccessors(t *testing.T) {
	var empty Session
	if empty.Keys() != nil || empty.GrabReport() != nil || empty.Dropped() != 0 || empty.grabsHeld() {
		t.Fatal("an unconfigured session must expose empty key state")
	}

	events := make(chan *vtinput.InputEvent, 1)
	original := []GrabResult{{Keysym: 42, Mods: ModCtrl, Code: 24}}
	s := Session{keys: &keyState{events: events, dropped: 3, held: true, report: original}}
	if s.Keys() != events {
		t.Fatal("Keys must return the configured event stream")
	}
	if got := s.Dropped(); got != 3 {
		t.Fatalf("Dropped = %d, want 3", got)
	}
	if !s.grabsHeld() {
		t.Fatal("grabsHeld must report the key state")
	}
	report := s.GrabReport()
	if len(report) != 1 || report[0] != original[0] {
		t.Fatalf("GrabReport = %#v, want %#v", report, original)
	}
	report[0].Mods = ModAlt
	if original[0].Mods != ModCtrl {
		t.Fatal("GrabReport must return a copy")
	}
}

func TestAncestorPIDs(t *testing.T) {
	tree := map[int]int{100: 50, 50: 20, 20: 1, 1: 0}
	parent := func(pid int) (int, bool) {
		p, ok := tree[pid]
		return p, ok && p > 0
	}
	got := ancestorPIDs(100, parent)
	want := []uint32{100, 50, 20, 1}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// A process table that lies about its parents must not spin forever.
func TestAncestorPIDsStopsOnACycle(t *testing.T) {
	parent := func(pid int) (int, bool) {
		if pid == 7 {
			return 9, true
		}
		return 7, true
	}
	if got := ancestorPIDs(7, parent); len(got) != 2 {
		t.Fatalf("a cycle must end the walk: %v", got)
	}
}

func TestAncestorPIDsOfThisProcess(t *testing.T) {
	got := processAncestors()
	if len(got) == 0 {
		t.Skip("no /proc on this system")
	}
	if got[0] != uint32(os.Getpid()) { // #nosec G115 -- OS process IDs are positive and represented as uint32 by the X11 API.
		t.Errorf("the walk must start at us: %v", got)
	}
}

func TestWindowIDFromEnv(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want xproto.Window
		ok   bool
	}{
		{map[string]string{"WINDOWID": "12345"}, 12345, true},
		{map[string]string{"WINDOWID": "0x1400003"}, 0x1400003, true},
		{map[string]string{"WINDOWID": " 42 "}, 42, true},
		{map[string]string{"WINDOWID": "0"}, 0, false},
		{map[string]string{"WINDOWID": "not a number"}, 0, false},
		{map[string]string{"XTERM_WINDOWID": "77"}, 77, true},
		{map[string]string{}, 0, false},
	}
	for _, c := range cases {
		got, ok := windowIDFromEnv(func(k string) string { return c.env[k] })
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%v: got %d,%v want %d,%v", c.env, got, ok, c.want, c.ok)
		}
	}
}

func TestSourceTrusted(t *testing.T) {
	if SourceActive.Trusted() {
		t.Error("a guess is not trusted")
	}
	if !SourceWindowID.Trusted() || !SourceProcess.Trusted() {
		t.Error("an identified window is trusted")
	}
}

// The rest needs a real X server. Start one with
//
//	Xvfb :99 -screen 0 1280x800x24 & DISPLAY=:99 go test ./internal/ttyx/
//
// and it runs; without a display it skips, which is what happens in CI and on
// a machine that has no X at all.
type xFixture struct {
	conn *xgb.Conn
	root xproto.Window
	term xproto.Window
}

func newXFixture(t *testing.T) *xFixture {
	t.Helper()
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no X display")
	}
	// The server occasionally refuses a connection while another test is
	// still tearing one down, so this retries before giving up.
	var conn *xgb.Conn
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		if conn, err = xgb.NewConn(); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Skipf("no connection to the X server: %v", err)
	}
	t.Cleanup(conn.Close)

	screen := xproto.Setup(conn).DefaultScreen(conn)
	win, err := xproto.NewWindowId(conn)
	if err != nil {
		t.Fatalf("window id: %v", err)
	}
	const structureNotifyMask uint32 = xproto.EventMaskStructureNotify
	err = xproto.CreateWindowChecked(conn, screen.RootDepth, win, screen.Root,
		40, 60, 400, 300, 0, xproto.WindowClassInputOutput, screen.RootVisual,
		xproto.CwBackPixel|xproto.CwEventMask,
		[]uint32{0x00202020, structureNotifyMask}).Check()
	if err != nil {
		t.Fatalf("the stand-in terminal window could not be created: %v", err)
	}
	if err := xproto.MapWindowChecked(conn, win).Check(); err != nil {
		t.Fatalf("map: %v", err)
	}
	t.Cleanup(func() { xproto.DestroyWindow(conn, win) })

	return &xFixture{conn: conn, root: screen.Root, term: win}
}

func (f *xFixture) intern(t *testing.T, name string) xproto.Atom {
	t.Helper()
	reply, err := xproto.InternAtom(f.conn, false, uint16(len(name)), name).Reply() // #nosec G115 -- test atom names are fixed short strings.
	if err != nil || reply == nil {
		t.Fatalf("intern %s: %v", name, err)
	}
	return reply.Atom
}

func (f *xFixture) setWindowProp(t *testing.T, win xproto.Window, name string, typ xproto.Atom, v uint32) {
	t.Helper()
	buf := make([]byte, 4)
	xgb.Put32(buf, v)
	err := xproto.ChangePropertyChecked(f.conn, xproto.PropModeReplace, win,
		f.intern(t, name), typ, 32, 1, buf).Check()
	if err != nil {
		t.Fatalf("set %s: %v", name, err)
	}
}

func TestSessionFindsWindowFromEnvironment(t *testing.T) {
	f := newXFixture(t)
	env := map[string]string{
		"DISPLAY":  os.Getenv("DISPLAY"),
		"WINDOWID": itoa(uint32(f.term)),
	}
	s, err := open(func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if s.Source() != SourceWindowID {
		t.Errorf("source: got %v", s.Source())
	}
	if s.Window() != f.term {
		t.Errorf("window: got %d, want %d", s.Window(), f.term)
	}
}

// A window id that names nothing must not be believed.
func TestSessionIgnoresStaleWindowID(t *testing.T) {
	f := newXFixture(t)
	env := map[string]string{
		"DISPLAY":  os.Getenv("DISPLAY"),
		"WINDOWID": "0x7ffffff",
	}
	s, err := open(func(k string) string { return env[k] }, nil)
	if err != nil {
		// Nothing else identifies a window here, which is a correct
		// outcome: better to refuse than to draw over a stranger.
		return
	}
	defer s.Close()
	if s.Source() == SourceWindowID {
		t.Error("a window id that resolves to nothing must be discarded")
	}
	_ = f
}

func TestSessionFindsWindowByProcess(t *testing.T) {
	f := newXFixture(t)
	f.setWindowProp(t, f.term, "_NET_WM_PID", xproto.AtomCardinal, 4242)
	f.setWindowProp(t, f.root, "_NET_CLIENT_LIST", xproto.AtomWindow, uint32(f.term))
	t.Cleanup(func() {
		xproto.DeleteProperty(f.conn, f.root, f.intern(t, "_NET_CLIENT_LIST"))
	})

	env := map[string]string{"DISPLAY": os.Getenv("DISPLAY")}
	s, err := open(func(k string) string { return env[k] }, []uint32{1, 4242})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if s.Source() != SourceProcess || s.Window() != f.term {
		t.Errorf("got window %d from %v, want %d from _NET_WM_PID", s.Window(), s.Source(), f.term)
	}
}

// A window belonging to somebody else's process is not ours.
func TestSessionRefusesAStrangersWindow(t *testing.T) {
	f := newXFixture(t)
	f.setWindowProp(t, f.term, "_NET_WM_PID", xproto.AtomCardinal, 4242)
	f.setWindowProp(t, f.root, "_NET_CLIENT_LIST", xproto.AtomWindow, uint32(f.term))
	t.Cleanup(func() {
		xproto.DeleteProperty(f.conn, f.root, f.intern(t, "_NET_CLIENT_LIST"))
	})

	env := map[string]string{"DISPLAY": os.Getenv("DISPLAY")}
	s, err := open(func(k string) string { return env[k] }, []uint32{1, 999})
	if err == nil {
		defer s.Close()
		if s.Source() == SourceProcess {
			t.Error("a window whose pid is not an ancestor of ours is not ours")
		}
	}
}

func TestSessionGeometry(t *testing.T) {
	f := newXFixture(t)
	env := map[string]string{
		"DISPLAY":  os.Getenv("DISPLAY"),
		"WINDOWID": itoa(uint32(f.term)),
	}
	s, err := open(func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	got, err := s.Geometry()
	if err != nil {
		t.Fatalf("geometry: %v", err)
	}
	want := Rect{X: 40, Y: 60, W: 400, H: 300}
	if got != want {
		t.Errorf("geometry: got %+v, want %+v", got, want)
	}
}

func TestSessionFocus(t *testing.T) {
	f := newXFixture(t)
	env := map[string]string{
		"DISPLAY":  os.Getenv("DISPLAY"),
		"WINDOWID": itoa(uint32(f.term)),
	}
	s, err := open(func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// Focused is answered from what the event loop last saw, so the answer
	// arrives when the event does and not when the request returns.
	f.focus(t, f.term)
	waitFor(t, "the focus to arrive", s.Focused)

	f.focus(t, f.root)
	waitFor(t, "the focus to leave", func() bool { return !s.Focused() })
}

// The overlay has to actually put the pixels it was given on the screen, so
// the test reads them back out of the window.
func TestOverlayDrawsPixels(t *testing.T) {
	f := newXFixture(t)
	env := map[string]string{
		"DISPLAY":  os.Getenv("DISPLAY"),
		"WINDOWID": itoa(uint32(f.term)),
	}
	s, err := open(func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// An overlay only goes on the screen while the terminal has the focus,
	// so the focus has to be there before anything can be read back.
	if err := xproto.SetInputFocusChecked(f.conn, xproto.InputFocusParent,
		f.term, xproto.TimeCurrentTime).Check(); err != nil {
		t.Fatalf("set focus: %v", err)
	}
	waitFor(t, "the focus", s.Focused)

	ov, err := s.NewOverlay()
	if err != nil {
		t.Fatalf("overlay: %v", err)
	}
	defer ov.Close()

	const w, h = 8, 4
	if err := ov.Place(Rect{X: 50, Y: 70, W: w, H: h}); err != nil {
		t.Fatalf("place: %v", err)
	}
	if !ov.Visible() {
		t.Error("a placed overlay is on the screen")
	}

	pix := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		// A gradient, so that a channel swap shows up as a wrong colour
		// rather than as a plausible one.
		pix[i*4] = byte(i * 8)
		pix[i*4+1] = 0x40
		pix[i*4+2] = 0xC0
		pix[i*4+3] = 0xFF
	}
	if err := ov.Draw(pix, w, h, w*4); err != nil {
		t.Fatalf("draw: %v", err)
	}

	reply, err := xproto.GetImage(s.conn, xproto.ImageFormatZPixmap,
		xproto.Drawable(ov.win), 0, 0, w, h, 0xFFFFFFFF).Reply()
	if err != nil || reply == nil {
		t.Fatalf("read back: %v", err)
	}
	if len(reply.Data) < w*h*4 {
		t.Fatalf("read back %d bytes for %d pixels", len(reply.Data), w*h)
	}
	for i := 0; i < w*h; i++ {
		b, g, r := reply.Data[i*4], reply.Data[i*4+1], reply.Data[i*4+2]
		if r != byte(i*8) || g != 0x40 || b != 0xC0 {
			t.Fatalf("pixel %d came back as r=%d g=%d b=%d, want r=%d g=64 b=192",
				i, r, g, b, byte(i*8))
		}
	}

	ov.Hide()
	if ov.Visible() {
		t.Error("a hidden overlay is off the screen")
	}
}

func TestOverlayRejectsBadBuffers(t *testing.T) {
	f := newXFixture(t)
	env := map[string]string{
		"DISPLAY":  os.Getenv("DISPLAY"),
		"WINDOWID": itoa(uint32(f.term)),
	}
	s, err := open(func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ov, err := s.NewOverlay()
	if err != nil {
		t.Fatalf("overlay: %v", err)
	}
	defer ov.Close()
	if err := ov.Place(Rect{X: 0, Y: 0, W: 4, H: 4}); err != nil {
		t.Fatalf("place: %v", err)
	}
	if err := ov.Draw(make([]byte, 8), 4, 4, 16); err == nil {
		t.Error("a buffer too small for the geometry must be refused")
	}
}

// An empty rectangle hides the overlay instead of asking X for a zero sized
// window, which is an error.
func TestOverlayEmptyRectHides(t *testing.T) {
	f := newXFixture(t)
	env := map[string]string{
		"DISPLAY":  os.Getenv("DISPLAY"),
		"WINDOWID": itoa(uint32(f.term)),
	}
	s, err := open(func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ov, err := s.NewOverlay()
	if err != nil {
		t.Fatalf("overlay: %v", err)
	}
	defer ov.Close()

	if err := ov.Place(Rect{X: 10, Y: 10, W: 20, H: 20}); err != nil {
		t.Fatalf("place: %v", err)
	}
	if err := ov.Place(Rect{}); err != nil {
		t.Fatalf("empty place: %v", err)
	}
	if ov.Visible() {
		t.Error("an empty rectangle hides the overlay")
	}
}

func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
