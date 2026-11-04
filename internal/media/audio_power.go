//go:build !noffi && !lite && !android && (windows || ((linux || darwin || freebsd) && (amd64 || arm64)))

package media

import (
	"sync"
	"time"

	"github.com/unxed/vtui"
)

// outputDevice is the part of oto.Context that outputPower drives.
type outputDevice interface {
	Suspend() error
	Resume() error
}

// outputPower stops the audio output device while nothing plays. oto runs
// the device from NewContext on and feeds it silence when no player plays.
// On macOS that is an AudioQueue rendering and resampling silence, some 800
// wakeups and 2% of a core, for the rest of the process once anything was
// played. Suspend stops the device and Resume starts it again.
//
// The device is suspended only once it has had nothing to play for
// outputSuspendDelay, so the next track, a seek (which reloads the track) or
// a pause and play do not stop and start it. oto on macOS carries Suspend and
// Resume out on goroutines of their own, so two calls in quick succession
// could land in the reverse order and leave the device stopped under a
// playing track; a call therefore comes at least outputSettle after the
// previous one.
type outputPower struct {
	dev   outputDevice
	now   func() time.Time
	after func(time.Duration, func()) (stop func() bool) // time.AfterFunc

	mu        sync.Mutex
	holders   int       // the players that play
	suspended bool      // what the device was last asked to be
	lastCall  time.Time // when it was asked, or when the device started
	idleSince time.Time // when the last holder let go
	stop      func() bool
}

const (
	outputSuspendDelay = 2 * time.Second
	outputSettle       = 100 * time.Millisecond
)

func afterFunc(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }

// newOutputPower takes over a device that has just started and has nothing to
// play yet; it is suspended unless something plays within the delay.
func newOutputPower(dev outputDevice, now func() time.Time, after func(time.Duration, func()) func() bool) *outputPower {
	p := &outputPower{dev: dev, now: now, after: after}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastCall = now()
	p.idleSince = p.lastCall
	p.reconcileLocked()
	return p
}

// hold is a player that starts playing; release is one that stops.
func (p *outputPower) hold() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.holders++
	p.reconcileLocked()
}

func (p *outputPower) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.holders == 0 {
		return
	}
	p.holders--
	if p.holders == 0 {
		p.idleSince = p.now()
	}
	p.reconcileLocked()
}

// reconcileLocked asks the device for the state the holders want: now, or
// from a timer when that is not due yet. It works the due time out from the
// state each time, so a timer that fires although it was stopped does no
// harm.
func (p *outputPower) reconcileLocked() {
	if p.stop != nil {
		p.stop()
		p.stop = nil
	}
	suspend := p.holders == 0
	if suspend == p.suspended {
		return
	}
	due := p.lastCall.Add(outputSettle)
	if idle := p.idleSince.Add(outputSuspendDelay); suspend && idle.After(due) {
		due = idle
	}
	if wait := due.Sub(p.now()); wait > 0 {
		p.stop = p.after(wait, func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.reconcileLocked()
		})
		return
	}
	p.suspended = suspend
	p.lastCall = p.now()
	if suspend {
		if err := p.dev.Suspend(); err != nil {
			vtui.DebugLog("AUDIO: suspending the output failed: %v", err)
			return
		}
		vtui.DebugLog("AUDIO: output suspended, nothing has played for %s", outputSuspendDelay)
		return
	}
	if err := p.dev.Resume(); err != nil {
		vtui.DebugLog("AUDIO: resuming the output failed: %v", err)
		return
	}
	vtui.DebugLog("AUDIO: output resumed")
}
