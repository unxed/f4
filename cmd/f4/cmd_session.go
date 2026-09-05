package main

import (
	"strings"
	"sync"
	"time"

	"github.com/unxed/vtui"
)

// cmdShellSession decides when the local cmd.exe has finished the line f4
// typed into it.
//
// Why a prompt mark alone cannot decide it (issue #409): f4 injects PROMPT so
// that cmd prints an OSC 133 mark with every prompt. But cmd also prints the
// prompt in front of every line of a batch file that runs with ECHO on — so
// the first line of foo.bat looks exactly like "command finished", the panels
// come back and the batch keeps running behind them. cmd interprets batch
// files in-process, so a child-process check is blind to them too.
//
// What tells a real prompt apart is that nothing follows it: after an echoed
// batch line the command text and a line break follow within the same frame,
// while a prompt that waits for input leaves the cursor resting right after
// the prompt text. The session therefore ends an execution only when a prompt
// mark (B, printed after $P$G) has arrived since the command was sent, the
// screen in front of the cursor looks like cmd's prompt and has not changed
// for cmdPromptSettleDelay, and no console child process that would own the
// terminal is running.
//
// What was learned from the field (docs/TERMINAL_WINDOWS.md §3.1–3.3):
//
//   - The screen is examined at settle time, never from a snapshot taken when
//     the mark arrived. ConPTY passes the mark through before it has
//     necessarily rendered the prompt text that precedes it, so a snapshot
//     taken at the mark can be empty; comparing against it failed every plain
//     `dir` and left the panels to a five-second fallback.
//   - The console title is unreadable behind a pseudoconsole, so there is no
//     title veto: cmd's "<title> - <command>" form never reaches us.
//   - A child process vetoes completion only if it is a console program that
//     is not itself cmd. A nested cmd prints the same prompt and is the shell
//     now; a GUI program (notepad) is not waited for by cmd, so cmd is already
//     at its prompt. The veto for a running console child is not bounded:
//     `ping -t` keeps the terminal busy for as long as it runs.
//
// The session is only created for the local Windows shell; the remote FISH+
// peer and Unix shells keep their own completion signals.
type cmdShellSession struct {
	mu sync.Mutex
	pf *PanelsFrame

	// promptSeq counts every prompt-end mark; sentSeq is its value when the
	// line now running was typed. A prompt can only end that line if it was
	// printed after it — a startup prompt still crossing ConPTY does not
	// count.
	promptSeq uint64
	sentSeq   uint64
	pending   bool // a typed line (command or directory sync) has no prompt yet
	inBatch   bool // a .bat/.cmd file is being executed: nested cmd is not the shell
	observed  promptSnapshot
	timer     *time.Timer
	attempts  int
	closed    bool
}

// cmdPromptSettleDelay is how long after a prompt mark the screen is first
// examined. ConPTY renders the text that follows an echoed batch prompt within
// a frame or two; a prompt still alone after this is a prompt.
//
// Lowered from 150ms to keep the return-to-panels snappy: the settle check
// still requires two unchanged screen looks, so a shorter window only means we
// confirm prompt stability a few frames sooner.
var cmdPromptSettleDelay = 50 * time.Millisecond

// cmdPromptRecheckDelay is the poll interval while the prompt has not settled
// yet, or while a console child holds the terminal.
//
// Lowered from 250ms: this was the dominant ~250ms lag felt after external
// commands and batches finished. The prompt must be unchanged across two looks
// to release, so a tighter interval just polls faster without false positives.
var cmdPromptRecheckDelay = 100 * time.Millisecond

// cmdPromptMaxAttempts bounds how long a prompt-shaped screen that will not
// hold still is waited on before the wait is released (roughly five seconds).
// It bounds only that flickering-prompt case: a busy shell is waited on
// without limit (rescheduleWhileBusy), and a console child holds the terminal
// for as long as it runs. A batch step longer than five seconds is therefore
// no longer cut off -- that was the "batch runs in the background" bug.
var cmdPromptMaxAttempts = 20

// windowsShellPrompt marks the prompt start and end. The prompt end mark is
// printed after $P$G, so the cursor position at its arrival is the position
// the shell reads input from.
const windowsShellPrompt = `$E]133;A$E\$P$G$E]133;B$E\`

// childProcess is what the session needs to know about a child of the shell.
type childProcess struct {
	Name string // image file name, e.g. "PING.EXE"
	GUI  bool   // built for the Windows GUI subsystem: cmd does not wait for it
}

// childInspector is implemented by PTY backends that can list the shell's
// direct children. Backends that cannot are treated as having none.
type childInspector interface {
	ChildProcesses() []childProcess
}

// nestedShellImages are children that print a cmd-style prompt and accept
// the lines f4 types (cd /d "..." & command). Their prompt ends the outer
// command's wait, and the panels come back with the nested shell as the shell.
// cmd.exe is deliberately absent: it keeps the terminal in raw mode until
// the user leaves it (exit), like ssh or python. PowerShell is also absent
// for the same reason.
var nestedShellImages = map[string]bool{}

// childHoldsTerminal reports whether one of the shell's children is a console
// program f4 has to wait for.
func childHoldsTerminal(children []childProcess) bool {
	for _, c := range children {
		if c.GUI {
			continue
		}
		if nestedShellImages[strings.ToLower(c.Name)] {
			continue
		}
		return true
	}
	return false
}

// promptShaped reports whether text is what cmd's $P$G leaves in front of the
// cursor: a path with a drive, then ">". A batch line echo has the command
// text after the ">", a `set /p` prompt has no drive, and program output that
// happens to end in ">" has no path.
func promptShaped(text string) bool {
	text = strings.TrimRight(text, " ")
	return strings.HasSuffix(text, ">") && strings.Contains(text, `:\`)
}

func newCmdShellSession(pf *PanelsFrame) *cmdShellSession {
	return &cmdShellSession{pf: pf}
}

// noteSent records that a line was typed into the shell and that the
// prompt f4 is waiting for has not been printed yet.
func (s *cmdShellSession) noteSent() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.pending = true
	s.sentSeq = s.promptSeq
	if s.promptSeq == 0 {
		// No prompt has been seen yet, so the shell's startup prompt is
		// still on its way and will arrive after the line: it is not the
		// answer to it.
		s.sentSeq = 1
	}
	s.mu.Unlock()
	s.pf.noteLocalShellBusy(true)
}

// noteBatchExecution marks the current command as a .bat/.cmd file execution.
// In batch mode a nested cmd.exe child is not treated as "the shell now"
// because the batch file will continue after it exits.
func (s *cmdShellSession) noteBatchExecution() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.inBatch = true
	s.mu.Unlock()
}

// idle reports whether every typed line has been answered by a settled
// prompt, i.e. whether the shell is known to be reading input.
func (s *cmdShellSession) idle() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.pending
}

func (s *cmdShellSession) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
}

// handleMark runs on the PTY goroutine for every OSC 133 mark of the local
// shell.
func (s *cmdShellSession) handleMark(mark string, snap promptSnapshot) {
	if s == nil || mark != "B" {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.promptSeq++
	s.observed = snap
	s.attempts = 0
	seq := s.promptSeq
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(cmdPromptSettleDelay, func() { s.settle(seq) })
	s.mu.Unlock()
}

// settle examines, on the UI goroutine, whether prompt seq is the shell
// waiting for input, and if so ends whatever f4 was waiting for.
func (s *cmdShellSession) settle(seq uint64) {
	// Close can race the timer callback while test cleanup replaces the global
	// frame manager. Check liveness while holding the session lock, before
	// touching that global, so a stopped callback cannot read a manager that
	// cleanup is restoring.
	s.mu.Lock()
	if s.closed || seq != s.promptSeq {
		s.mu.Unlock()
		return
	}
	manager := vtui.FrameManager
	s.mu.Unlock()
	if manager == nil {
		return
	}
	manager.PostTask(func() {
		s.mu.Lock()
		if s.closed || seq != s.promptSeq {
			s.mu.Unlock()
			return
		}
		previous, sentSeq, pending := s.observed, s.sentSeq, s.pending
		inBatch := s.inBatch
		s.mu.Unlock()

		pf := s.pf
		if pf.termView == nil {
			return
		}

		// What is on screen now, not what was there when the mark arrived.
		current := pf.termView.PromptSnapshot()

		if pf.termView.UseAltScreen || !promptShaped(current.Text) {
			// The screen is not a prompt: an interactive full-screen program,
			// a batch line still echoing, program output, or a batch step
			// that is simply taking its time (`timeout /t 10`, a `pause` the
			// user is reading). f4 waits, with no bound on how long -- a
			// batch step is as long as it is, and returning the panels in the
			// middle of one is the "batch runs in the background" bug over
			// again. The escape hatch, if the shell truly wedged, is Ctrl+O,
			// which is not gated on the terminal being busy. A later prompt
			// mark reschedules a fresh look; failing that, keep polling.
			s.rescheduleWhileBusy(seq)
			return
		}

		if current != previous {
			// The screen is prompt-shaped but changed since the last look:
			// the prompt is still being drawn. ConPTY can pass the prompt
			// mark through before the text in front of it, so the first look
			// often lands here. This settles within a frame or two, so a
			// short bounded retry is right -- and bounded so a prompt that
			// flickers forever cannot strand the wait.
			s.mu.Lock()
			if !s.closed && seq == s.promptSeq {
				s.observed = current
			}
			s.mu.Unlock()
			s.retryOrRelease(seq)
			return
		}

		if pending && seq <= sentSeq {
			// This prompt was printed before the line we typed; the shell has
			// not even started on it. The prompt for our line will come.
			vtui.DebugLog("CMD_SESSION: prompt %d predates the typed line (sent=%d), ignoring", seq, sentSeq)
			return
		}

		if inspector, ok := pf.localPTY().(childInspector); ok {
			children := inspector.ChildProcesses()
			held := childHoldsTerminal(children)
			if !held && inBatch {
				// In batch mode a nested cmd.exe child is not "the
				// shell now": the batch file will continue after it
				// exits, so the terminal is still held.
				for _, c := range children {
					if !c.GUI && strings.ToLower(c.Name) == "cmd.exe" {
						held = true
						break
					}
				}
			}
			if held {
				vtui.DebugLog("CMD_SESSION: prompt %d held by child %v, rechecking", seq, children)
				s.mu.Lock()
				if !s.closed && seq == s.promptSeq {
					s.timer = time.AfterFunc(cmdPromptRecheckDelay, func() { s.settle(seq) })
				}
				s.mu.Unlock()
				return
			}
		}

		vtui.DebugLog("CMD_SESSION: prompt %d settled (sent=%d pending=%v)", seq, sentSeq, pending)
		s.release()
	})
}

// rescheduleWhileBusy looks again after cmdPromptRecheckDelay, with no
// attempt limit: the shell is busy and f4 waits for as long as that lasts.
// The attempt counter is reset so that the bounded settle-retry, once the
// screen does become a prompt, gets its full budget of looks.
func (s *cmdShellSession) rescheduleWhileBusy(seq uint64) {
	s.mu.Lock()
	if !s.closed && seq == s.promptSeq {
		s.attempts = 0
		s.timer = time.AfterFunc(cmdPromptRecheckDelay, func() { s.settle(seq) })
	}
	s.mu.Unlock()
}

// retryOrRelease looks at prompt seq again shortly while a prompt-shaped
// screen is still settling, and after cmdPromptMaxAttempts releases the wait.
// This bound applies only to a prompt that will not hold still -- never to a
// busy shell, which rescheduleWhileBusy waits on without limit. Releasing
// rather than holding matters because a stuck wait leaves pf.executing set,
// and isPtyBusy reports that as busy, disabling every hotkey gated on a quiet
// terminal (Esc) while leaving the ungated ones (Ctrl+O) alive.
func (s *cmdShellSession) retryOrRelease(seq uint64) {
	s.mu.Lock()
	if s.closed || seq != s.promptSeq {
		s.mu.Unlock()
		return
	}
	s.attempts++
	if s.attempts < cmdPromptMaxAttempts {
		s.timer = time.AfterFunc(cmdPromptRecheckDelay, func() { s.settle(seq) })
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	vtui.DebugLog("CMD_SESSION: prompt %d never settled, releasing the wait", seq)
	s.release()
}

// release ends the wait: the shell is taken to be at its prompt.
func (s *cmdShellSession) release() {
	pf := s.pf
	s.mu.Lock()
	s.pending = false
	s.inBatch = false
	s.mu.Unlock()
	pf.shellPromptReady = true
	pf.ignoreNextPrompt = false
	if pf.executing {
		pf.endExecution()
	}
	pf.noteLocalShellBusy(false)
	pf.catchUpProcessEnvironment(true)
	// The shell is idle at a prompt with no console child: the one moment
	// a resize reaches nothing that would react to it.
}
