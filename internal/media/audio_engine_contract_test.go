//go:build !noffi && !android && (windows || ((linux || darwin || freebsd) && (amd64 || arm64)))

package media

import (
	"reflect"
	"testing"
	"time"
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
	if got := a.Info(); !reflect.DeepEqual(got, AudioTrackInfo{}) {
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
