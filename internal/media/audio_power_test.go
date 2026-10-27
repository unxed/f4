//go:build !noffi && !lite && !android && (windows || ((linux || darwin || freebsd) && (amd64 || arm64)))

package media

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type fakeOutput struct{ calls []string }

func (f *fakeOutput) Suspend() error { f.calls = append(f.calls, "suspend"); return nil }
func (f *fakeOutput) Resume() error  { f.calls = append(f.calls, "resume"); return nil }

// fakeTimers is a clock and the one timer outputPower may have pending.
type fakeTimers struct {
	now   time.Time
	due   time.Time
	f     func()
	armed bool
}

func (c *fakeTimers) clock() time.Time { return c.now }

func (c *fakeTimers) after(d time.Duration, f func()) func() bool {
	c.due, c.f, c.armed = c.now.Add(d), f, true
	return func() bool {
		was := c.armed
		c.armed = false
		return was
	}
}

// fire moves the clock to the pending timer and runs it.
func (c *fakeTimers) fire(t *testing.T) {
	t.Helper()
	if !c.armed {
		t.Fatal("no timer is pending")
	}
	c.now, c.armed = c.due, false
	c.f()
}

func newTestPower() (*outputPower, *fakeOutput, *fakeTimers) {
	dev := &fakeOutput{}
	timers := &fakeTimers{now: time.Unix(1000, 0)}
	return newOutputPower(dev, timers.clock, timers.after), dev, timers
}

func wantCalls(t *testing.T, dev *fakeOutput, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := append([]string{}, dev.calls...); !reflect.DeepEqual(got, want) {
		t.Fatalf("device calls %v, want %v", got, want)
	}
}

// Once anything was played, oto kept the device rendering silence for the
// rest of the process: 2% of a core and some 800 wakeups a second in an f4
// that sat idle. The device is suspended once nothing has played for the
// delay, and resumed by the next track.
func TestOutputPowerSuspendsTheDeviceWhenNothingPlays(t *testing.T) {
	p, dev, timers := newTestPower()
	p.hold()
	timers.now = timers.now.Add(time.Minute)
	p.release()
	wantCalls(t, dev)
	if !timers.armed || timers.due.Sub(timers.now) != outputSuspendDelay {
		t.Fatalf("suspend timer armed=%v in %s, want in %s", timers.armed, timers.due.Sub(timers.now), outputSuspendDelay)
	}
	timers.fire(t)
	wantCalls(t, dev, "suspend")

	timers.now = timers.now.Add(time.Minute)
	p.hold()
	wantCalls(t, dev, "suspend", "resume")
	if timers.armed {
		t.Fatal("a playing device must have no timer pending")
	}
}

// The device starts with the context, before the first track plays; one that
// nothing plays on is suspended too.
func TestOutputPowerSuspendsANewDeviceNothingPlaysOn(t *testing.T) {
	_, dev, timers := newTestPower()
	timers.fire(t)
	wantCalls(t, dev, "suspend")
}

// The next track, a seek (which reloads the track) and a pause and play stop
// the player and start it again at once; the device stays on through them.
func TestOutputPowerKeepsTheDeviceOnBetweenTracks(t *testing.T) {
	p, dev, timers := newTestPower()
	p.hold()
	p.release()
	timers.now = timers.now.Add(outputSuspendDelay / 2)
	p.hold()
	if timers.armed {
		t.Fatal("the suspend timer must be stopped when a track plays again")
	}
	wantCalls(t, dev)
}

// oto on macOS carries Suspend and Resume out on goroutines of their own, so
// a Resume right after a Suspend could overtake it and leave the device
// stopped under a playing track. The Resume waits until the Suspend settled.
func TestOutputPowerResumesNoSoonerThanSettleAfterASuspend(t *testing.T) {
	p, dev, timers := newTestPower()
	timers.fire(t)
	suspendedAt := timers.now
	timers.now = timers.now.Add(10 * time.Millisecond)
	p.hold()
	wantCalls(t, dev, "suspend")
	if !timers.armed || !timers.due.Equal(suspendedAt.Add(outputSettle)) {
		t.Fatalf("resume timer armed=%v at +%s, want at +%s", timers.armed, timers.due.Sub(suspendedAt), outputSettle)
	}
	timers.fire(t)
	wantCalls(t, dev, "suspend", "resume")
}

// A track that stops before its deferred Resume came leaves the device
// suspended, with nothing pending.
func TestOutputPowerDropsAResumeNobodyWaitsFor(t *testing.T) {
	p, dev, timers := newTestPower()
	timers.fire(t)
	timers.now = timers.now.Add(10 * time.Millisecond)
	p.hold()
	timers.now = timers.now.Add(10 * time.Millisecond)
	p.release()
	if timers.armed {
		t.Fatal("a suspended device with nothing to play must have no timer pending")
	}
	wantCalls(t, dev, "suspend")
}

// time.Timer.Stop can come too late to keep the callback from running. A
// timer that was replaced and fires anyway does not suspend the device before
// the delay since the last player stopped.
func TestOutputPowerIgnoresAStoppedTimerThatFiresAnyway(t *testing.T) {
	p, dev, timers := newTestPower()
	p.hold()
	p.release()
	stale, staleDue := timers.f, timers.due
	timers.now = timers.now.Add(time.Second)
	p.hold()
	timers.now = timers.now.Add(500 * time.Millisecond)
	p.release()
	idleSince := timers.now

	timers.now = staleDue
	stale()
	wantCalls(t, dev)
	if !timers.armed || !timers.due.Equal(idleSince.Add(outputSuspendDelay)) {
		t.Fatalf("suspend timer armed=%v at %s, want at %s", timers.armed, timers.due, idleSince.Add(outputSuspendDelay))
	}
	timers.fire(t)
	wantCalls(t, dev, "suspend")
}

func TestOutputPowerWaitsForEveryPlayer(t *testing.T) {
	p, dev, timers := newTestPower()
	p.hold()
	p.hold()
	p.release()
	if timers.armed {
		t.Fatal("the device must stay on while a player still plays")
	}
	p.release()
	p.release() // one more than there were: ignored
	timers.fire(t)
	wantCalls(t, dev, "suspend")
	if p.holders != 0 {
		t.Fatalf("holders=%d after an extra release, want 0", p.holders)
	}
}

// The engine holds the output while it plays, whichever way it starts and
// stops: Play and Pause, TogglePause, a seek, Stop.
func TestAudioEngineHoldsTheOutputOnlyWhilePlaying(t *testing.T) {
	path := filepath.Join(t.TempDir(), "silence.wav")
	if err := os.WriteFile(path, wavBytes(1, 2, 44100, 16, make([]byte, 44100*AudioBytesPerFrame)), 0o600); err != nil {
		t.Fatal(err)
	}
	a := NewAudioEngine()
	if err := a.Load(path); err != nil {
		t.Skipf("no audio output here: %v", err)
	}
	defer a.Close()
	holders := func() int {
		a.power.mu.Lock()
		defer a.power.mu.Unlock()
		return a.power.holders
	}
	check := func(step string, want bool) {
		t.Helper()
		a.mu.Lock()
		holding := a.holding
		a.mu.Unlock()
		n := 0
		if want {
			n = 1
		}
		if holding != want || holders() != n {
			t.Fatalf("after %s: holding=%v holders=%d, want %v and %d", step, holding, holders(), want, n)
		}
	}
	check("Load", false)
	a.Play()
	check("Play", true)
	a.Pause()
	check("Pause", false)
	a.TogglePause()
	check("TogglePause to playing", true)
	a.Seek(100 * time.Millisecond)
	check("a seek while playing", true)
	a.TogglePause()
	check("TogglePause to paused", false)
	a.Seek(100 * time.Millisecond)
	check("a seek while paused", false)
	a.Play()
	a.Stop()
	check("Stop", false)
}
