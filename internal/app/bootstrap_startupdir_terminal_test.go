//go:build linux || darwin

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/unxed/f4/internal/terminal"
)

// The terminal the test starts f4 in: wide enough for each panel to list a
// short file name in full.
const (
	startTermCols = 120
	startTermRows = 40
)

// startF4InTerminal starts f4 the way a person does -- `cd here && f4` in a
// terminal -- and reads what it draws there. settingsIni, when not empty, is the
// content of the settings.ini it starts with.
//
// The unit tests in bootstrap_startupdir_test.go check the decision
// rememberStartupDirs makes. What issue #1152 broke lies around that decision
// and `go test` has no terminal to reach it: the terminal on stdin, the
// directory travelling to the session daemon in its environment, and the panels
// actually opening it over the ones session.ini restores. So this makes a pty
// and runs the real startup on it: this test binary re-executed as f4 (see
// runAsF4Env), which spawns its daemon the same way a release build does, and
// the daemon draws into the pty.
//
// session.ini sends both panels to another directory. Each of the two
// directories holds one file whose name appears nowhere else on the screen, so
// which directory a panel shows is read off its file list, not off a title that
// may be shortened.
//
// A plain start keeps the session on Windows (see plainStartOpensCwd), and
// Windows has no pty to run this on; the build tag leaves it out.
func startF4InTerminal(t *testing.T, settingsIni string) terminalStart {
	if testing.Short() {
		t.Skip("starts f4 and its session daemon in a pty")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate the test binary to run as f4: %v", err)
	}

	root := startupTestRoot(t)
	home := filepath.Join(root, "home")
	configHome := filepath.Join(root, "config")
	tmp := filepath.Join(root, "tmp")
	logDir := filepath.Join(root, "log")
	here := filepath.Join(root, "here")
	restored := filepath.Join(root, "restored")
	for _, dir := range []string{home, configHome, tmp, logDir, here, restored} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const hereMarker, restoredMarker = "cwdmark", "sessmark"
	writeStartupFixture(t, filepath.Join(here, hereMarker), "")
	writeStartupFixture(t, filepath.Join(restored, restoredMarker), "")

	sessionPath := filepath.Join(childUserConfigDir(home, configHome), "f4", "session.ini")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeStartupFixture(t, sessionPath,
		"[Panel/Left]\nFolder = "+restored+"\n\n[Panel/Right]\nFolder = "+restored+"\n")
	if settingsIni != "" {
		writeStartupFixture(t, filepath.Join(filepath.Dir(sessionPath), "settings.ini"), settingsIni)
	}

	// What a shell in a terminal hands f4, and nothing of this process: no
	// F4_STARTUP_DIR or F4_NESTED from a developer running the tests inside f4.
	env := []string{
		runAsF4Env + "=1",
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + configHome,
		"TMPDIR=" + tmp,
		"PWD=" + here,
		"PATH=" + os.Getenv("PATH"),
		"SHELL=/bin/sh",
		"HISTFILE=/dev/null",
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"LANG=en_US.UTF-8",
		// Client and daemon both log here. The log is what shows that f4 read
		// the session above, and what a failure prints.
		"VTUI_DEBUG=" + filepath.Join(logDir, "f4.log"),
	}
	for _, name := range []string{"USER", "LOGNAME"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}

	pty, err := terminal.NewPTY()
	if err != nil {
		t.Skipf("PTY allocation unavailable in this environment: %v", err)
	}
	pty.SetSize(startTermCols, startTermRows)
	screen := newStartupScreen(startTermCols, startTermRows)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := pty.Master.Read(buf)
			if n > 0 {
				screen.Feed(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	// #nosec G204 G702 -- exe is this test binary, started as f4 on purpose.
	cmd := exec.Command(exe)
	cmd.Dir = here
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = pty.Slave, pty.Slave, pty.Slave
	// What a terminal emulator does for the shell it starts, and what
	// PTY.Run does for f4's own: a session of its own, the pty as its
	// controlling terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		_ = pty.Close()
		t.Fatalf("start f4 in a pty: %v", err)
	}
	client := &startedProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		client.err = cmd.Wait()
		close(client.done)
	}()
	t.Cleanup(func() { stopStartupTest(t, client, pty, readDone, tmp) })

	// Wait for either directory to fill both panels. The very first start with
	// an empty profile builds its configuration and takes seconds.
	deadline := time.Now().Add(60 * time.Second)
wait:
	for time.Now().Before(deadline) {
		select {
		case <-client.done:
			break wait
		default:
		}
		rows, complete := screen.Snapshot()
		if complete && (bothHalves(rows, hereMarker) || bothHalves(rows, restoredMarker)) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// A later redraw would still replace what is on the screen now; give it the
	// time before judging.
	time.Sleep(2 * time.Second)
	rows := screen.Settled(5 * time.Second)

	report := func() string {
		return "screen:\n" + formatStartupScreen(rows) + "\nf4 debug log (tail):\n" + startupLogTail(logDir, 120)
	}
	select {
	case <-client.done:
		t.Fatalf("f4 exited before its panels were checked: %v\n%s", client.err, report())
	default:
	}
	if logs := readStartupLogs(logDir); !strings.Contains(logs, "SESSION: Loaded state from "+sessionPath) {
		t.Fatalf("f4 did not report loading the session written for this test (%s). Without it the panels have "+
			"nothing to prefer over the current directory, and the check below would pass whatever f4 does.\n%s",
			sessionPath, report())
	}
	return terminalStart{here: here, rows: rows, hereMarker: hereMarker, restoredMarker: restoredMarker, report: report}
}

// terminalStart is what f4 drew after a start in a terminal: which of the two
// directories, the current one and the session's, each panel shows.
type terminalStart struct {
	here                       string
	rows                       [][]rune
	hereMarker, restoredMarker string
	report                     func() string
}

func (s terminalStart) halves() (hereLeft, hereRight, restoredLeft, restoredRight bool) {
	hereLeft, hereRight = markerHalves(s.rows, s.hereMarker)
	restoredLeft, restoredRight = markerHalves(s.rows, s.restoredMarker)
	return
}

// With "Open the current folder at start" on, `cd here && f4` shows here in both
// panels, like mc, over the panels session.ini restores (issues #822, #1152).
func TestTerminalStartOpensItsDirectoryOverTheSessionWhenAsked(t *testing.T) {
	start := startF4InTerminal(t, "[Startup]\nStartInCurrentFolder = 1\n")
	hereLeft, hereRight, restoredLeft, restoredRight := start.halves()
	if !hereLeft || !hereRight || restoredLeft || restoredRight {
		t.Fatalf("`cd %s && f4` in a terminal, with the current folder asked for, must open that directory in both panels (issues #822, #1152).\n"+
			"current directory shown: left panel %t, right panel %t\n"+
			"restored session's directory shown: left panel %t, right panel %t\n%s",
			start.here, hereLeft, hereRight, restoredLeft, restoredRight, start.report())
	}
}

// By default a plain start restores the session, as far2l and Far do, whatever
// directory the terminal happens to be in (issue #495).
func TestTerminalStartRestoresTheSessionByDefault(t *testing.T) {
	start := startF4InTerminal(t, "")
	hereLeft, hereRight, restoredLeft, restoredRight := start.halves()
	if hereLeft || hereRight || !restoredLeft || !restoredRight {
		t.Fatalf("a plain `f4` in a terminal must restore the session's panels, not open %s (issue #495).\n"+
			"current directory shown: left panel %t, right panel %t\n"+
			"restored session's directory shown: left panel %t, right panel %t\n%s",
			start.here, hereLeft, hereRight, restoredLeft, restoredRight, start.report())
	}
}

// startedProcess is the f4 client the test started, with the result of the one
// Wait that may be called on it.
type startedProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// startupTestRoot is a short directory holding everything the test gives f4.
// Short because the daemon binds a unix socket under TMPDIR, and a socket path
// is limited to about a hundred bytes; t.TempDir on macOS lives under
// /var/folders and, with the test name and the socket name appended, runs past
// that.
func startupTestRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "f4-start-")
	if err != nil {
		root, err = os.MkdirTemp("", "f4-start-")
	}
	if err != nil {
		t.Fatalf("create the test directory: %v", err)
	}
	t.Cleanup(func() {
		// Something f4 started may still be writing on its way out. A leftover
		// temporary directory says nothing about what this test checks.
		for attempt := 0; ; attempt++ {
			err := os.RemoveAll(root)
			if err == nil {
				return
			}
			if attempt == 20 {
				t.Logf("leaving %s behind: %v", root, err)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return root
}

func writeStartupFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// childUserConfigDir is os.UserConfigDir for the environment the test gives
// f4 rather than for this process: $HOME/Library/Application Support on
// darwin, $XDG_CONFIG_HOME elsewhere.
func childUserConfigDir(home, xdgConfigHome string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support")
	}
	return xdgConfigHome
}

// stopStartupTest ends everything the test started. The daemon runs in a
// session of its own and outlives its client, as it does for a person, so it is
// found through the session file it writes under TMPDIR and killed with its
// process group.
func stopStartupTest(t *testing.T, client *startedProcess, pty *terminal.PTY, readDone <-chan struct{}, tmp string) {
	for _, pid := range startupDaemonPIDs(tmp) {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
		if !waitProcessGone(pid, 5*time.Second) {
			t.Logf("f4 session daemon %d still exists after SIGKILL", pid)
		}
	}
	_ = syscall.Kill(-client.cmd.Process.Pid, syscall.SIGKILL)
	select {
	case <-client.done:
	case <-time.After(5 * time.Second):
		t.Logf("f4 client %d did not exit after SIGKILL", client.cmd.Process.Pid)
	}
	_ = pty.Close()
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Log("the pty reader did not stop after the pty was closed")
	}
}

// startupDaemonPIDs reads the pids of the session daemons from the files
// RunServer writes, waiting briefly for one whose socket exists but whose file
// is not written yet.
func startupDaemonPIDs(tmp string) []int {
	deadline := time.Now().Add(3 * time.Second)
	for {
		var pids []int
		infos, _ := filepath.Glob(filepath.Join(tmp, "f4-sessions-*", "f4-*.json"))
		for _, path := range infos {
			// #nosec G304 G703 -- a session file under this test's own TMPDIR.
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var info struct{ PID int }
			if json.Unmarshal(data, &info) == nil && info.PID > 0 {
				pids = append(pids, info.PID)
			}
		}
		sockets, _ := filepath.Glob(filepath.Join(tmp, "f4-sessions-*", "*.sock"))
		if len(pids) >= len(sockets) || time.Now().After(deadline) {
			return pids
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readStartupLogs returns every log file f4 wrote, oldest rotation first. The
// daemon starts after its client has already created the log, and vtui rotates
// a log that exists when a process first writes to it, so the two processes end
// up in different files.
func readStartupLogs(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Sprintf("(cannot read %s: %v)\n", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			names = append(names, entry.Name())
		}
	}
	// f4.2.log, f4.1.log, f4.log: the highest rotation is the oldest.
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] > names[j]
	})
	var b strings.Builder
	for _, name := range names {
		// #nosec G304 G703 -- a log file under this test's own directory.
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			fmt.Fprintf(&b, "== %s: %v\n", name, err)
			continue
		}
		fmt.Fprintf(&b, "== %s\n%s", name, data)
	}
	return b.String()
}

func startupLogTail(dir string, lines int) string {
	all := strings.Split(strings.TrimRight(readStartupLogs(dir), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n") + "\n"
}

func formatStartupScreen(rows [][]rune) string {
	var b strings.Builder
	for y, row := range rows {
		fmt.Fprintf(&b, "%2d|%s|\n", y, strings.TrimRight(string(row), " "))
	}
	return b.String()
}

// markerHalves says whether marker is on the left half of the screen, where the
// left panel is, and on the right half. A match across the middle counts for
// neither.
func markerHalves(rows [][]rune, marker string) (left, right bool) {
	want := []rune(marker)
	for _, row := range rows {
		cols := len(row)
		for x := 0; x+len(want) <= cols; x++ {
			if string(row[x:x+len(want)]) != marker {
				continue
			}
			switch {
			case x+len(want) <= cols/2:
				left = true
			case x >= cols/2:
				right = true
			}
		}
	}
	return left, right
}

func bothHalves(rows [][]rune, marker string) bool {
	left, right := markerHalves(rows, marker)
	return left && right
}

// startupScreen is as much of a terminal as reading f4's panels needs. vtui's
// ANSI renderer places text with CUP, CUF and CUB and colours it with SGR (see
// its screenbuf.go); the rest of what f4 sends -- modes, queries, OSC, DCS and
// APC strings -- is skipped here and never answered. A cell holds one rune:
// wide and combining characters are not modelled, and since the renderer
// repositions after any cell a terminal might measure differently, they cannot
// shift the ASCII file names this test looks for.
type startupScreen struct {
	mu      sync.Mutex
	cols    int
	rows    int
	cells   [][]rune
	x, y    int
	state   int
	seq     []byte
	pending []byte // an incomplete UTF-8 sequence
	inFrame bool   // inside a synchronized update, ESC[?2026h ... ESC[?2026l
}

const (
	screenGround = iota
	screenEsc
	screenEscIntermediate
	screenCSI
	screenOSC
	screenString // DCS, SOS, PM, APC: skipped up to ESC \
)

func newStartupScreen(cols, rows int) *startupScreen {
	s := &startupScreen{cols: cols, rows: rows, cells: make([][]rune, rows)}
	for y := range s.cells {
		s.cells[y] = []rune(strings.Repeat(" ", cols))
	}
	return s
}

// Feed hands the screen what f4 wrote to the pty.
func (s *startupScreen) Feed(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range data {
		s.feed(b)
	}
}

// Snapshot copies the screen, and says whether it was taken between frames
// rather than in the middle of one.
func (s *startupScreen) Snapshot() ([][]rune, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([][]rune, len(s.cells))
	for y, row := range s.cells {
		rows[y] = append([]rune(nil), row...)
	}
	return rows, !s.inFrame
}

// Settled is Snapshot taken between frames, or the last one when no frame ends
// within timeout.
func (s *startupScreen) Settled(timeout time.Duration) [][]rune {
	deadline := time.Now().Add(timeout)
	for {
		rows, complete := s.Snapshot()
		if complete || time.Now().After(deadline) {
			return rows
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *startupScreen) feed(b byte) {
	switch s.state {
	case screenEsc:
		switch {
		case b == '[':
			s.state, s.seq = screenCSI, s.seq[:0]
		case b == ']':
			s.state = screenOSC
		case b == 'P' || b == 'X' || b == '^' || b == '_':
			s.state = screenString
		case b >= 0x20 && b <= 0x2f:
			s.state = screenEscIntermediate
		default:
			s.state = screenGround
		}
		return
	case screenEscIntermediate:
		if b < 0x20 || b > 0x2f {
			s.state = screenGround
		}
		return
	case screenCSI:
		switch {
		case b == 0x1b:
			s.state = screenEsc
		case b >= 0x20 && b <= 0x3f:
			s.seq = append(s.seq, b)
		case b >= 0x40 && b <= 0x7e:
			s.csi(b)
			s.state = screenGround
		}
		return
	case screenOSC:
		switch b {
		case 0x07:
			s.state = screenGround
		case 0x1b:
			s.state = screenEsc
		}
		return
	case screenString:
		if b == 0x1b {
			s.state = screenEsc
		}
		return
	}

	if b >= 0x80 {
		s.pending = append(s.pending, b)
		if utf8.FullRune(s.pending) {
			r, _ := utf8.DecodeRune(s.pending)
			s.pending = s.pending[:0]
			s.put(r)
		}
		return
	}
	if len(s.pending) > 0 {
		s.pending = s.pending[:0]
		s.put(utf8.RuneError)
	}
	switch b {
	case 0x1b:
		s.state = screenEsc
	case '\r':
		s.x = 0
	case '\n', '\v', '\f':
		if s.y < s.rows-1 {
			s.y++
		}
	case '\b':
		if s.x > 0 {
			s.x--
		}
	case '\t':
		s.x = min((s.x/8+1)*8, s.cols-1)
	default:
		if b >= 0x20 && b != 0x7f {
			s.put(rune(b))
		}
	}
}

func (s *startupScreen) put(r rune) {
	if s.x >= s.cols {
		s.x = 0
		if s.y < s.rows-1 {
			s.y++
		}
	}
	s.cells[s.y][s.x] = r
	s.x++
}

func (s *startupScreen) csi(final byte) {
	params := string(s.seq)
	if strings.IndexFunc(params, func(r rune) bool { return r >= 0x20 && r <= 0x2f }) >= 0 {
		return // an intermediate byte: cursor shape and the like
	}
	if params != "" && strings.ContainsRune("<=>?", rune(params[0])) {
		if params[0] == '?' && (final == 'h' || final == 'l') {
			for _, mode := range csiNumbers(params[1:]) {
				switch mode {
				case 2026:
					s.inFrame = final == 'h'
				case 47, 1047, 1049:
					s.erase(0, s.rows*s.cols)
				}
			}
		}
		return
	}
	args := csiNumbers(params)
	arg := func(i, def int) int {
		if i < len(args) && args[i] > 0 {
			return args[i]
		}
		return def
	}
	mode := 0
	if len(args) > 0 {
		mode = args[0]
	}
	cursor := s.y*s.cols + s.x
	switch final {
	case 'H', 'f':
		s.y, s.x = arg(0, 1)-1, arg(1, 1)-1
	case 'A':
		s.y -= arg(0, 1)
	case 'B':
		s.y += arg(0, 1)
	case 'C':
		s.x += arg(0, 1)
	case 'D':
		s.x -= arg(0, 1)
	case 'G':
		s.x = arg(0, 1) - 1
	case 'd':
		s.y = arg(0, 1) - 1
	case 'J':
		switch mode {
		case 0:
			s.erase(cursor, s.rows*s.cols)
		case 1:
			s.erase(0, cursor+1)
		default:
			s.erase(0, s.rows*s.cols)
		}
	case 'K':
		line := s.y * s.cols
		switch mode {
		case 0:
			s.erase(cursor, line+s.cols)
		case 1:
			s.erase(line, cursor+1)
		default:
			s.erase(line, line+s.cols)
		}
	}
	s.x = max(0, min(s.x, s.cols-1))
	s.y = max(0, min(s.y, s.rows-1))
}

// erase blanks the cells in [from, to), counted row by row.
func (s *startupScreen) erase(from, to int) {
	from = max(0, from)
	to = min(to, s.rows*s.cols)
	for i := from; i < to; i++ {
		s.cells[i/s.cols][i%s.cols] = ' '
	}
}

func csiNumbers(params string) []int {
	if params == "" {
		return nil
	}
	var out []int
	for _, field := range strings.Split(params, ";") {
		field, _, _ = strings.Cut(field, ":")
		n, _ := strconv.Atoi(field) // an empty or malformed field is 0, the default
		out = append(out, n)
	}
	return out
}
