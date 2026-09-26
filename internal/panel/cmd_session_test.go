package panel

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtui"
)

// windowsBuild models what a given Windows build does at the seams the cmd
// session depends on. Everything here was observed in the field on real
// machines (docs/TERMINAL_WINDOWS.md §3.1–3.3); a test that runs under every
// build in windowsBuilds is a test against all of it.
type windowsBuild struct {
	name string

	// markBeforeText: ConPTY passes the OSC 133 prompt-end mark through to
	// the pipe before it has rendered the prompt text that precedes it, so
	// the text arrives in a later read. Seen on 10.0.19045 as a five-second
	// delay on every `dir` while the session compared the screen against an
	// empty snapshot taken at the mark. On 10.0.26200 the same commands
	// ended at once, so there the text comes first.
	markBeforeText bool

	// The console title is never readable from outside a pseudoconsole: the
	// probe found it empty for every process on both builds. It is listed
	// here as a constant of the model so nobody reintroduces a title veto.
	titleReadable bool
}

var windowsBuilds = []windowsBuild{
	{name: "Windows 10 19045", markBeforeText: true, titleReadable: false},
	{name: "Windows 11 26200", markBeforeText: false, titleReadable: false},
}

// Children observed by the process probe on both builds while the user did
// the checklist. cmd waits for the console ones; notepad is a GUI program
// cmd returns from immediately; `start notepad` detaches and is not a child
// at all.
var (
	childPing      = terminal.ChildProcess{Name: "PING.EXE", GUI: false}
	childTimeout   = terminal.ChildProcess{Name: "timeout.exe", GUI: false}
	childNestedCmd = terminal.ChildProcess{Name: "cmd.exe", GUI: false}
	childNotepad   = terminal.ChildProcess{Name: "notepad.exe", GUI: true}
)

const promptText = `C:\work>`

// fakeWinPty stands in for the ConPTY-backed terminal.PTY: it reports whatever
// children the test says the shell has.
type fakeWinPty struct {
	mockPty
	mu       sync.Mutex
	children []terminal.ChildProcess
}

func (p *fakeWinPty) ChildProcesses() []terminal.ChildProcess {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]terminal.ChildProcess(nil), p.children...)
}

func (p *fakeWinPty) setChildren(children ...terminal.ChildProcess) {
	p.mu.Lock()
	p.children = children
	p.mu.Unlock()
}

// cmdShellSim feeds the parser what cmd.exe under ConPTY would send, in the
// order the modelled build sends it.
type cmdShellSim struct {
	t     *testing.T
	pf    *PanelsFrame
	pty   *fakeWinPty
	build windowsBuild
}

func newCmdShellSim(t *testing.T, build windowsBuild) *cmdShellSim {
	t.Helper()
	oldSettle, oldRecheck := cmdPromptSettleDelay, cmdPromptRecheckDelay
	cmdPromptSettleDelay, cmdPromptRecheckDelay = 20*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { cmdPromptSettleDelay, cmdPromptRecheckDelay = oldSettle, oldRecheck })

	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame(t)
	t.Cleanup(pf.Close)
	pty := &fakeWinPty{}
	pf.Pty = pty
	pf.CmdSession = newCmdShellSession(pf)
	pf.TermView.OnShellMark = func(mark string, snap terminal.PromptSnapshot) { pf.CmdSession.handleMark(mark, snap) }
	pf.Parser = terminal.NewAnsiParser(pf.TermView, nil)
	return &cmdShellSim{t: t, pf: pf, pty: pty, build: build}
}

func (s *cmdShellSim) feed(data string) { s.pf.Parser.Process([]byte(data)) }

func (s *cmdShellSim) wait(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		testutil.DrainUITasks()
		time.Sleep(5 * time.Millisecond)
	}
}

// prompt sends what PROMPT=$E]133;A$E\$P$G$E]133;B$E\ produces, in the order
// this build delivers it. The text after a batch-echoed prompt (the command
// line and its line break) goes with it, as it does in one ConPTY frame.
func (s *cmdShellSim) prompt(after string) {
	if s.build.markBeforeText {
		s.feed("\x1b]133;A\x1b\\\x1b]133;B\x1b\\")
		s.wait(10 * time.Millisecond)
		s.feed(promptText + after)
		return
	}
	s.feed("\x1b]133;A\x1b\\" + promptText + "\x1b]133;B\x1b\\" + after)
}

// start brings the shell up: banner, first prompt, settled.
func (s *cmdShellSim) start() {
	s.feed("Microsoft Windows [Version 10.0]\r\n\r\n")
	s.prompt("")
	s.wait(80 * time.Millisecond)
	if !s.pf.CmdSession.idle() {
		s.t.Fatalf("[%s] startup prompt did not settle", s.build.name)
	}
}

// run types a command: f4 marks the execution and the shell echoes the line.
func (s *cmdShellSim) run(command string) {
	s.pf.Executing = true
	s.pf.ReturnToPanels = true
	s.pf.ShowPanels = false
	s.pf.CmdSession.noteSent()
	s.feed(command + "\r\n")
}

// settledWithin is the time a prompt may take to end an execution without
// falling back to the release: the first look plus one re-look for the build
// that delivers the text late, with slack for the test scheduler.
const settledWithin = 120 * time.Millisecond

func (s *cmdShellSim) expectExecuting(want bool, what string) {
	s.t.Helper()
	if s.pf.Executing != want {
		s.t.Errorf("[%s] %s: executing=%v, want %v", s.build.name, what, s.pf.Executing, want)
	}
}

func forEachBuild(t *testing.T, fn func(t *testing.T, sim *cmdShellSim)) {
	for _, build := range windowsBuilds {
		t.Run(build.name, func(t *testing.T) { fn(t, newCmdShellSim(t, build)) })
	}
}

// A plain command ends when its prompt has settled -- promptly, on every
// build, and not via the five-second release. On 19045 this is the case that
// took five seconds before the screen was examined at settle time.
func TestCmdSessionPlainCommandEndsPromptly(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		sim.start()
		sim.run("dir")
		sim.feed(" Volume in drive C\r\nfile.txt\r\n\r\n")
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(false, "after dir's prompt")
		if !sim.pf.ShowPanels {
			t.Error("panels did not come back")
		}
	})
}

// A batch file with ECHO on prints the prompt in front of every line it
// runs. Those prompts must not be taken for the one after the batch (#409):
// the command text and the line break after them mean the screen in front
// of the cursor is not a prompt.
func TestCmdSessionBatchEchoPromptsDoNotEndExecution(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		sim.start()
		sim.run("foo.bat")
		sim.feed("\r\n")
		sim.prompt("echo started\r\nstarted\r\n\r\n")
		sim.wait(settledWithin)
		sim.expectExecuting(true, "after the first echoed batch line")
		sim.prompt("timeout /t 5\r\nWaiting for 5 seconds...")
		sim.wait(settledWithin)
		sim.expectExecuting(true, "while the batch waits")
		sim.prompt("pause\r\nPress any key to continue . . . ")
		sim.wait(settledWithin)
		sim.expectExecuting(true, "while the batch pauses")
		sim.feed("\r\n\r\n")
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(false, "after the prompt that follows the batch")
	})
}

// A console child holds the terminal for as long as it runs; there is no
// timeout on that, `ping -t` is legitimately forever. When it exits, the
// prompt that had already settled is taken.
func TestCmdSessionConsoleChildHoldsTheTerminal(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		sim.start()
		sim.run("ping -t 127.0.0.1")
		sim.pty.setChildren(childPing)
		// The shell printed nothing new -- but suppose a prompt did appear
		// while the child runs (a batch that spawned ping with echo on and
		// the prompt text alone on screen): the child still holds it.
		sim.feed("\r\n")
		sim.prompt("")
		sim.wait(settledWithin + 4*cmdPromptRecheckDelay)
		sim.expectExecuting(true, "while ping runs")
		sim.pty.setChildren()
		sim.wait(settledWithin)
		sim.expectExecuting(false, "after ping exited")
	})
}

// A nested cmd keeps the terminal: its child process holds it until exit.
// The panels stay hidden while the user interacts with the nested shell.
func TestCmdSessionNestedCmdHoldsTerminal(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		sim.start()
		sim.run("cmd")
		sim.pty.setChildren(childNestedCmd)
		sim.feed("Microsoft Windows [Version 10.0]\r\n\r\n")
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(true, "at the nested shell's prompt")

		// A command typed into the nested shell keeps executing.
		sim.run("dir")
		sim.feed("file.txt\r\n\r\n")
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(true, "after dir in the nested shell")

		// exit closes the nested shell; the outer prompt ends the wait.
		sim.run("exit")
		sim.pty.setChildren()
		sim.feed("\r\n")
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(false, "after exit")
		if !sim.pf.ShowPanels {
			t.Error("panels did not come back after exit")
		}
	})
}

// cmd does not wait for a GUI program, so it is at its prompt while notepad
// is open; f4 must not stay busy for as long as the window does.
func TestCmdSessionGUIChildDoesNotHoldTheTerminal(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		sim.start()
		sim.run("notepad")
		sim.pty.setChildren(childNotepad)
		sim.feed("\r\n")
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(false, "with notepad open")
	})
}

// A prompt that reaches the pipe after the command was typed, but was
// printed before it (the startup prompt still crossing ConPTY), is not the
// answer to the command.
func TestCmdSessionStalePromptDoesNotEndExecution(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		sim.run("dir")
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(true, "after a prompt that predates the command")
		sim.feed("dir\r\nfile.txt\r\n\r\n")
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(false, "after the command's own prompt")
	})
}

// The console title is not readable behind a pseudoconsole (both builds), so
// nothing may depend on it. Even if a title did arrive it must change
// nothing.
func TestCmdSessionIgnoresConsoleTitle(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		if sim.build.titleReadable {
			t.Fatal("no build with a readable title has been observed; update the model before relying on it")
		}
		sim.start()
		sim.run("dir")
		sim.feed("\x1b]0;C:\\Windows\\system32\\cmd.exe - dir\x07file.txt\r\n\r\n")
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(false, "with a running-style title still set")
	})
}

// The directory sync is a typed line like any other: no second sync may be
// typed until its prompt has settled.
func TestCmdSessionSyncWaitsForPrompt(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		sim.start()
		sim.pf.CmdSession.noteSent()
		if sim.pf.CmdSession.idle() {
			t.Fatal("session reported idle with a line outstanding")
		}
		sim.feed("cd /d \"C:\\work\" & rem f4_sync\r\n\r\n")
		sim.prompt("")
		sim.wait(settledWithin)
		if !sim.pf.CmdSession.idle() {
			t.Fatal("session did not become idle after the prompt settled")
		}
	})
}

func TestPromptShaped(t *testing.T) {
	cases := map[string]bool{
		`C:\work>`:                        true,
		`C:\>`:                            true,
		`Z:\share\deep\path> `:            true,
		`PS C:\work> `:                    true, // shape only; nested powershell is held by the child veto
		`C:\work>pause`:                   false,
		`Press any key to continue . . .`: false,
		`Enter value>`:                    false, // set /p prompt: no drive
		`>>> `:                            false, // python
		``:                                false,
	}
	for text, want := range cases {
		if got := promptShaped(text); got != want {
			t.Errorf("promptShaped(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestChildHoldsTerminal(t *testing.T) {
	cases := []struct {
		children []terminal.ChildProcess
		want     bool
	}{
		{nil, false},
		{[]terminal.ChildProcess{childPing}, true},
		{[]terminal.ChildProcess{childTimeout}, true},
		{[]terminal.ChildProcess{childNestedCmd}, true}, // cmd.exe keeps the terminal until exit
		{[]terminal.ChildProcess{childNotepad}, false},
		{[]terminal.ChildProcess{childNestedCmd, childPing}, true}, // ping inside the nested cmd
		{[]terminal.ChildProcess{{Name: "powershell.exe"}}, true},  // rejects cd /d: stays raw
		{[]terminal.ChildProcess{{Name: "python.exe"}}, true},
	}
	for _, c := range cases {
		if got := childHoldsTerminal(c.children); got != c.want {
			t.Errorf("childHoldsTerminal(%v) = %v, want %v", c.children, got, c.want)
		}
	}
}

// A batch step longer than the old five-second release bound must not return
// the panels while it runs. This is the regression that made "batch files
// run in the background": timeout /t 10 and a pause the user reads for more
// than five seconds both left the screen non-prompt for longer than the
// bound, and the release fired mid-batch. A busy screen is now waited on
// without limit.
func TestCmdSessionLongBatchStepIsNotCutOff(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		oldMax := cmdPromptMaxAttempts
		cmdPromptMaxAttempts = 3 // if the bound applied here, it would fire fast
		t.Cleanup(func() { cmdPromptMaxAttempts = oldMax })

		sim.start()
		sim.run("slow.bat")
		sim.feed("\r\n")
		sim.prompt("timeout /t 10\r\nWaiting for 10 seconds, press a key to continue ...")
		// Far past panel.CmdPromptMaxAttempts * panel.CmdPromptRecheckDelay at test scale.
		sim.wait(settledWithin + 10*cmdPromptRecheckDelay)
		sim.expectExecuting(true, "while the batch step runs")

		// When the batch finally reaches its closing prompt, it ends.
		sim.feed("\r\n\r\n")
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(false, "after the batch's final prompt")
	})
}

// A prompt that is shaped but never stops changing is still released, so a
// genuinely stuck settle cannot disable Esc forever. This is the case the
// bound is for, and it must keep working.
func TestCmdSessionFlickeringPromptIsReleased(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		oldMax := cmdPromptMaxAttempts
		cmdPromptMaxAttempts = 3
		t.Cleanup(func() { cmdPromptMaxAttempts = oldMax })

		// Drive the checker directly: a prompt-shaped screen that differs at
		// every look never settles, and must be released after the bound so
		// that a stuck settle cannot keep Esc disabled. Driving retryOrRelease
		// itself keeps the test independent of timer scheduling.
		sim.pf.Executing = true
		sim.pf.CmdSession.pending = true
		sim.pf.CmdSession.promptSeq = 5
		sim.pf.CmdSession.sentSeq = 4
		seq := sim.pf.CmdSession.promptSeq
		for i := 0; i < cmdPromptMaxAttempts; i++ {
			sim.pf.CmdSession.retryOrRelease(seq)
		}
		testutil.DrainUITasks()
		sim.expectExecuting(false, "after a prompt that never settled was released")
	})
}

// A prompt-shaped screen that keeps changing while a console child is still
// running must not be released just because the flicker outlasted the retry
// bound: the child, not the bound, decides. This is f4#1376's "cls sends me
// back to f4" report -- Far Manager draws full screen without an alternate
// screen of its own, so its own command line ("C:\path>") is promptShaped,
// and `cls` clearing and redrawing it can flicker across a few looks while
// Far is still the one running. Compare TestCmdSessionFlickeringPromptIsReleased,
// which is the same flicker with no child present and must still release.
func TestCmdSessionFlickeringPromptHeldByChildIsNotReleased(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		oldMax := cmdPromptMaxAttempts
		cmdPromptMaxAttempts = 3
		t.Cleanup(func() { cmdPromptMaxAttempts = oldMax })

		sim.pty.setChildren(terminal.ChildProcess{Name: "far.exe", GUI: false})

		sim.pf.Executing = true
		sim.pf.CmdSession.pending = true
		sim.pf.CmdSession.promptSeq = 5
		sim.pf.CmdSession.sentSeq = 4
		seq := sim.pf.CmdSession.promptSeq
		for i := 0; i < cmdPromptMaxAttempts+2; i++ {
			sim.pf.CmdSession.retryOrRelease(seq)
		}
		testutil.DrainUITasks()
		sim.expectExecuting(true, "far.exe still running through a flickering, prompt-shaped repaint")

		// Far exits; nothing holds the terminal any more, so the same bound
		// releases exactly as the unheld case already does.
		sim.pty.setChildren()
		for i := 0; i < cmdPromptMaxAttempts; i++ {
			sim.pf.CmdSession.retryOrRelease(seq)
		}
		testutil.DrainUITasks()
		sim.expectExecuting(false, "after far.exe exited")
	})
}

// f4#1376's residual report, after the fix in #1495: the first `cls` typed
// at Far Manager's own command line still dropped back to f4 (Esc bounced
// back into Far), but a second `cls` right after worked correctly. The
// difference is not in whether the child veto fires -- TestCmdSession
// FlickeringPromptHeldByChildIsNotReleased above already proves that in
// isolation -- but in how much retry budget a flicker gets before that veto
// even has to be asked: retryOrRelease's own bound-exceeded fallback resets
// the attempt counter once it vetoes (so the *next* flicker gets a full
// budget), and rescheduleWhileBusy does the same for a busy, non-prompt
// screen, but settle()'s own "screen unchanged, still held by a child"
// branch did not. Far Manager, drawing full screen without an alternate
// screen of its own, settles into exactly that branch as soon as it is
// launched -- its own command line is prompt-shaped and holds still for as
// long as the user takes before typing anything -- and used to freeze the
// counter wherever the brief flicker of Far's own startup drawing left it.
// The very next flicker (`cls` redrawing that same line) then inherited
// whatever was left instead of a fresh budget, reaching the bound-exceeded
// fallback -- and therefore having to trust a single, uncached
// child-process scan -- after only a look or two instead of
// cmdPromptMaxAttempts of them. This test drives the real settle() path
// (not retryOrRelease directly) through exactly that "already primed by an
// earlier flicker, then settled, still held" sequence and checks the
// budget survives it.
func TestCmdSessionStableHoldResetsRetryBudget(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		sim.start()
		sim.run("far.exe")
		sim.pty.setChildren(terminal.ChildProcess{Name: "far.exe", GUI: false})

		// Far settles at its own idle command line: prompt-shaped, and held
		// still there for as long as the user takes before typing anything.
		// This is examined by whatever settle chain the mark below leaves
		// running -- Far does not remark a screen that is not changing, so
		// nothing supersedes that chain until the screen changes again.
		sim.prompt("")
		seq := sim.pf.CmdSession.promptSeq
		sim.wait(2 * cmdPromptRecheckDelay)
		sim.expectExecuting(true, "while far.exe holds Far's own idle command line")

		// Models a brief flicker while Far's own UI first painted: one look
		// short of the bound retryOrRelease would enforce on a screen that
		// never holds still. The screen is unchanged (still Far's idle
		// prompt), so the next settle() call takes the held-by-child branch
		// below, not retryOrRelease.
		sim.pf.CmdSession.attempts = cmdPromptMaxAttempts - 1
		sim.pf.CmdSession.settle(seq)
		testutil.DrainUITasks()
		sim.expectExecuting(true, "while far.exe holds an unchanged, prompt-shaped screen")

		// This is the decisive check: a fake child list that always
		// correctly reports far.exe (as it does throughout this test) can't
		// by itself distinguish a veto that fires promptly from one that
		// only fires after the bound is already exhausted -- both leave
		// Executing true. What actually differs in the field is how much
		// budget the *next* flicker gets before it has to trust that live,
		// uncached check at all, which is exactly this counter.
		if got := sim.pf.CmdSession.attempts; got != 0 {
			t.Fatalf("[%s] settle's held-by-child branch left attempts=%d, want 0 -- the next flicker (cls) would inherit a used-up budget instead of cmdPromptMaxAttempts=%d fresh looks", sim.build.name, got, cmdPromptMaxAttempts)
		}

		// The next flicker -- cls clearing and redrawing Far's own command
		// line -- now gets the full retry budget. Vary only trailing
		// spaces so the screen keeps changing (current != previous) while
		// staying prompt-shaped (promptShaped trims trailing spaces),
		// mirroring cls redrawing the same "C:\path>" line repeatedly while
		// Far repaints.
		for i := 0; i < cmdPromptMaxAttempts-1; i++ {
			sim.feed("\r" + promptText + strings.Repeat(" ", i+1))
			sim.pf.CmdSession.settle(seq)
			testutil.DrainUITasks()
			sim.expectExecuting(true, "cls's flicker must not exhaust an already-spent budget")
		}
	})
}

// When a batch file calls cmd, the nested shell's prompt must not end the
// batch's execution: the batch will continue after the nested cmd exits.
// The panels must stay hidden until the batch truly finishes.
func TestCmdSessionBatchWithNestedCmdDoesNotRelease(t *testing.T) {
	forEachBuild(t, func(t *testing.T, sim *cmdShellSim) {
		sim.start()
		sim.run("foo.bat")
		sim.pty.setChildren(childNestedCmd)
		sim.feed("Microsoft Windows [Version 10.0]\r\n\r\n")
		sim.pf.CmdSession.NoteBatchExecution()
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(true, "at the nested cmd prompt inside a batch")

		// Nested cmd exits; batch continues.
		sim.run("exit")
		sim.pty.setChildren()
		sim.feed("\r\n")
		sim.prompt("echo done\r\ndone\r\n\r\n")
		sim.wait(settledWithin)
		sim.expectExecuting(true, "while the batch continues after nested cmd")

		// Batch finishes.
		sim.feed("\r\n\r\n")
		sim.prompt("")
		sim.wait(settledWithin)
		sim.expectExecuting(false, "after the batch's final prompt")
		if !sim.pf.ShowPanels {
			t.Error("panels did not come back after batch finished")
		}
	})
}
