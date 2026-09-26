//go:build !noffi && !android && (windows || ((linux || darwin || freebsd) && (amd64 || arm64)))

package media

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ebitengine/oto/v3"
)

func TestAudioEngineEmptyLifecycle(t *testing.T) {
	a := NewAudioEngine()
	if got := a.Volume(); got != 0.8 {
		t.Fatalf("initial volume=%v, want 0.8", got)
	}
	if a.IsPlaying() || a.IsLoaded() || a.Finished() {
		t.Fatal("new engine must be empty")
	}
	if got := a.Position(); got != 0 || a.Duration() != 0 {
		t.Fatalf("empty clock position=%s duration=%s", got, a.Duration())
	}
	if got := a.Info(); !reflect.DeepEqual(got, audioTrackInfo{}) {
		t.Fatalf("empty info=%+v", got)
	}
	if got := a.Spectrum(4); !reflect.DeepEqual(got, []float64{0, 0, 0, 0}) {
		t.Fatalf("empty spectrum=%v", got)
	}

	// These controls are intentionally no-ops before Load. Calling them is a
	// useful lifecycle contract: a stopped or failed track must not panic.
	a.Play()
	a.Pause()
	if got := a.TogglePause(); got {
		t.Fatal("toggle on an empty engine must report paused")
	}
	a.Stop()
	a.Close()
	a.Close()
}

func TestAudioEngineVolumeClampsWithoutAPlayer(t *testing.T) {
	a := NewAudioEngine()
	a.SetVolume(-1)
	if got := a.Volume(); got != 0 {
		t.Fatalf("negative volume=%v, want 0", got)
	}
	a.SetVolume(2)
	if got := a.Volume(); got != 1 {
		t.Fatalf("high volume=%v, want 1", got)
	}
	a.SetVolume(1)
	if got := a.Volume(); got != 1 {
		t.Fatalf("unchanged volume=%v", got)
	}
}

func TestAudioEnginePositionLockedWithNoTap(t *testing.T) {
	a := &AudioEngine{ctxRate: 48000, duration: 3 * time.Second}
	if got := a.PositionLocked(); got != 0 {
		t.Fatalf("position without PCM tap=%s", got)
	}
}

func TestAudioEngineReusesSharedContextCreationError(t *testing.T) {
	sentinel := errors.New("audio device unavailable")
	sharedAudioContext.Lock()
	oldContext := sharedAudioContext.ctx
	oldRate := sharedAudioContext.rate
	oldErr := sharedAudioContext.creationErr
	sharedAudioContext.ctx = nil
	sharedAudioContext.rate = 0
	sharedAudioContext.creationErr = sentinel
	sharedAudioContext.Unlock()
	oldNewContext := newOtoContext
	called := false
	newOtoContext = func(*oto.NewContextOptions) (*oto.Context, chan struct{}, error) {
		called = true
		return nil, nil, errors.New("must not retry context creation")
	}
	t.Cleanup(func() {
		newOtoContext = oldNewContext
		sharedAudioContext.Lock()
		sharedAudioContext.ctx = oldContext
		sharedAudioContext.rate = oldRate
		sharedAudioContext.creationErr = oldErr
		sharedAudioContext.Unlock()
	})

	a := NewAudioEngine()
	if err := a.ensureContext(48000); !errors.Is(err, sentinel) {
		t.Fatalf("ensureContext error = %v, want shared creation error", err)
	}
	if called {
		t.Fatal("ensureContext retried oto.NewContext after a failed shared creation")
	}
}
