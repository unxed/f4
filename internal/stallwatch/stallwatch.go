// Package stallwatch reports what the UI thread was doing when it stopped
// responding.
//
// A CPU profile answers "where did the cycles go", which is the wrong question
// for a freeze: a thread waiting on a lock, a page fault or a console write
// burns no cycles and leaves no samples, and one stalled second is lost among
// two minutes of ordinary frames either way. This watches the wall clock.
//
// Two shapes of freeze are caught, because they need different evidence:
//
//   - A unit of work that overruns — a render, an input event, a posted task
//     that will not finish. The stacks of every goroutine are written while it
//     is still stuck, so the one that is stuck is there to read.
//
//   - A gap between units of work, where the UI thread has nothing to do
//     because nothing reached it. A freeze in input delivery looks exactly
//     like this, and nothing is "running" to catch, so a gap that interrupts a
//     busy stretch is recorded too — the stacks then show what everyone else
//     was waiting on.
//
// Everything is also appended to one log file, which exists from the moment
// the watchdog arms. An empty log means it never armed; a log with no entries
// means it armed and saw nothing.
package stallwatch

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"
)

// MaxDumps bounds how many stack dumps one run writes. A freeze that repeats
// writes the same stacks, and the point is to read them, not collect them.
const MaxDumps = 20

// tightGap is how close together two units of work have to be to count as one
// busy stretch. Below it the UI thread is working steadily -- scrolling, say.
const tightGap = 100 * time.Millisecond

// busyRun is how many units of work in a row, each within tightGap of the
// last, mark the thread as busy. A gap that interrupts that is worth
// recording; the same gap when the user has simply stopped is not.
const busyRun = 10

// burstWindow is how long the work after a gap is counted for. Enough to tell
// a released backlog from an ordinary rate, short enough to stay about the gap.
const burstWindow = 500 * time.Millisecond

var (
	enabled   atomic.Bool
	threshold atomic.Int64 // nanoseconds

	// openedAt is when the unit of work now running began, in Unix
	// nanoseconds, or zero when none is. The UI thread writes it; the watcher
	// goroutine reads it.
	openedAt atomic.Int64
	openName atomic.Value // string

	// lastEnd is when the last unit of work finished, and tightRun how many
	// finished back to back before it.
	lastEnd  atomic.Int64
	tightRun atomic.Int64

	// reported guards one dump per stall, gapReported one per gap.
	reported    atomic.Bool
	gapReported atomic.Bool

	// burstStart is when the work after a gap began, and burstCount how many
	// units have run since. What resumes after a gap says where the gap came
	// from: a flood is work that was held up somewhere and released at once,
	// an ordinary trickle is work that genuinely was not there to do.
	burstStart atomic.Int64
	burstCount atomic.Int64

	dumpDir string
	logPath string
	mu      sync.Mutex
	dumped  int
)

// Start arms the watchdog and reports the log file it will write to, so that a
// caller can say where the evidence will be. Work longer than limit, and gaps
// longer than limit that interrupt a busy stretch, are recorded in dir. A
// limit of zero leaves it disarmed and returns "".
func Start(dir string, limit time.Duration) string {
	if limit <= 0 {
		return ""
	}
	// The directory has to work before anything claims to be armed: a
	// watchdog whose evidence cannot be written is worse than none, because
	// the user waits for a freeze and collects nothing.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	// dumpDir and logPath belong to mu: a watcher started by an earlier call is
	// still reading them when this one rewrites them, and did so unlocked
	// (a data race seen on main in TestArmDiagnostics_*).
	path := filepath.Join(dir, "stall-watchdog.log")
	mu.Lock()
	dumpDir = dir
	logPath = path
	mu.Unlock()
	threshold.Store(int64(limit))

	// Written before anything else can go wrong, so that the file's existence
	// is proof the switch was understood and names the folder to look in.
	// A fresh run starts from nothing: leftover state would let the gap
	// detector fire on a stretch of work that belongs to the previous one.
	mu.Lock()
	dumped = 0
	mu.Unlock()
	openedAt.Store(0)
	lastEnd.Store(0)
	tightRun.Store(0)
	burstStart.Store(0)
	burstCount.Store(0)
	reported.Store(false)
	gapReported.Store(false)
	logf("armed at %s, limit %v, pid %d", time.Now().Format(time.RFC3339), limit, os.Getpid())
	if _, err := os.Stat(path); err != nil {
		mu.Lock()
		dumpDir, logPath = "", ""
		mu.Unlock()
		threshold.Store(0)
		return ""
	}

	enabled.Store(true)
	go watch()
	return path
}

// Enabled reports whether the watchdog is armed.
func Enabled() bool { return enabled.Load() }

// Frame marks one unit of work the UI thread is about to do and returns the
// function that marks its end. It is meant to be deferred:
//
//	defer stallwatch.Frame("render")()
//
// Units do not nest: an inner call is ignored, so the outermost one measures
// what the user actually waited for.
func Frame(name string) func() {
	if !enabled.Load() {
		return func() {}
	}
	now := time.Now()
	// The name goes up before the CAS that publishes the unit, or the watcher
	// can sample between the two and head its dump with the name of the unit
	// before this one.
	openName.Store(name)
	if !openedAt.CompareAndSwap(0, now.UnixNano()) {
		return func() {}
	}

	if prev := lastEnd.Load(); prev != 0 {
		gap := now.Sub(time.Unix(0, prev))
		if gap < tightGap {
			tightRun.Add(1)
		} else {
			if gap >= time.Duration(threshold.Load()) && tightRun.Load() >= busyRun {
				// Recorded here rather than while it was happening: work has
				// resumed, which is what proves the quiet was a stall and not
				// the user simply stopping.
				logf("GAP %v of nothing before %q, after %d units of steady work", gap.Round(time.Millisecond), name, tightRun.Load())
				burstStart.Store(now.UnixNano())
				burstCount.Store(0)
			}
			tightRun.Store(0)
		}
	}

	if started := burstStart.Load(); started != 0 {
		if since := now.Sub(time.Unix(0, started)); since >= burstWindow {
			burstStart.Store(0)
			logf("RESUMED %d units in %v after the gap", burstCount.Load(), since.Round(time.Millisecond))
		} else {
			burstCount.Add(1)
		}
	}

	return func() {
		end := time.Now()
		if d := end.Sub(now); d >= time.Duration(threshold.Load()) {
			logf("SLOW %q took %v", name, d.Round(time.Millisecond))
		}
		lastEnd.Store(end.UnixNano())
		openedAt.Store(0)
		reported.Store(false)
		gapReported.Store(false)
	}
}

func watch() {
	for {
		limit := time.Duration(threshold.Load())
		if !enabled.Load() || limit <= 0 {
			return
		}
		time.Sleep(limit / 4)
		if !enabled.Load() {
			return
		}

		if started := openedAt.Load(); started != 0 {
			if !reported.Load() {
				if elapsed := time.Since(time.Unix(0, started)); elapsed >= limit {
					reported.Store(true)
					name, _ := openName.Load().(string)
					dump(fmt.Sprintf("%q has been running for %v", name, elapsed.Round(time.Millisecond)))
				}
			}
			continue
		}

		// Nothing running. A quiet stretch that interrupts steady work is the
		// other shape of freeze: the UI thread is idle because nothing is
		// reaching it, and the stacks show what everyone else is waiting on.
		if gapReported.Load() || tightRun.Load() < busyRun {
			continue
		}
		prev := lastEnd.Load()
		if prev == 0 {
			continue
		}
		if quiet := time.Since(time.Unix(0, prev)); quiet >= limit {
			gapReported.Store(true)
			dump(fmt.Sprintf("nothing has run for %v, after %d units of steady work", quiet.Round(time.Millisecond), tightRun.Load()))
		}
	}
}

func dump(reason string) {
	mu.Lock()
	defer mu.Unlock()
	if dumped >= MaxDumps {
		return
	}
	dumped++
	name := fmt.Sprintf("stall-%s-%02d.txt", time.Now().Format("20060102-150405.000"), dumped)
	logLocked("STALL %s -> %s", reason, name)

	// #nosec G304 -- dumpDir is the profile directory f4 already owns.
	f, err := os.Create(filepath.Join(dumpDir, name))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "f4 stall: %s (limit %v)\n", reason, time.Duration(threshold.Load()))
	_, _ = fmt.Fprintf(f, "taken at %s\n\n", time.Now().Format(time.RFC3339Nano))
	if p := pprof.Lookup("goroutine"); p != nil {
		_ = p.WriteTo(f, 2)
	}
}

func logf(format string, a ...any) {
	mu.Lock()
	defer mu.Unlock()
	logLocked(format, a...)
}

func logLocked(format string, a ...any) {
	if logPath == "" {
		return
	}
	// #nosec G304 -- as for the dumps: the profile directory.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, a...))
}
