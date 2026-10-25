package paneltest

import (
	"sync"
	"testing"
	"time"

	"github.com/unxed/f4/internal/media"
	"github.com/unxed/f4/internal/panel"
)

// FakeAudio is a panel.PlayerEngine that plays nothing and remembers what it
// was told: the file it was given last, and whether it is playing or paused.
// A test can see what a key did to playback without a sound device, which
// the CI machines do not have.
type FakeAudio struct {
	mu      sync.Mutex
	path    string
	loaded  bool
	playing bool
	volume  float64
	pos     time.Duration
	// Loads lists every file Load was given, in order.
	Loads []string
}

// UseFakeAudio makes every player session started until the test ends play
// into a FakeAudio, and returns the engines in the order they were made. The
// test ends with whatever still plays in the background stopped.
func UseFakeAudio(t *testing.T) func() []*FakeAudio {
	t.Helper()
	panel.EndPlayerSession()
	var mu sync.Mutex
	var made []*FakeAudio
	old := panel.NewPlayerEngine
	panel.NewPlayerEngine = func() panel.PlayerEngine {
		a := &FakeAudio{volume: 0.8}
		mu.Lock()
		made = append(made, a)
		mu.Unlock()
		return a
	}
	t.Cleanup(func() {
		panel.EndPlayerSession()
		panel.NewPlayerEngine = old
	})
	return func() []*FakeAudio {
		mu.Lock()
		defer mu.Unlock()
		return append([]*FakeAudio(nil), made...)
	}
}

// Path is the file the engine has loaded, or "" when it has none.
func (a *FakeAudio) Path() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.loaded {
		return ""
	}
	return a.path
}

func (a *FakeAudio) Load(path string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.path, a.loaded, a.playing, a.pos = path, true, false, 0
	a.Loads = append(a.Loads, path)
	return nil
}

func (a *FakeAudio) Close() { a.Stop() }

func (a *FakeAudio) Play() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.playing = a.loaded
}

func (a *FakeAudio) TogglePause() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loaded {
		a.playing = !a.playing
	}
	return a.playing
}

func (a *FakeAudio) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.path, a.loaded, a.playing, a.pos = "", false, false, 0
}

func (a *FakeAudio) Seek(delta time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.loaded {
		return false
	}
	a.pos = max(0, a.pos+delta)
	return true
}

func (a *FakeAudio) IsPlaying() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.playing
}

func (a *FakeAudio) IsLoaded() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loaded
}

func (a *FakeAudio) Finished() bool { return false }

func (a *FakeAudio) Volume() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.volume
}

func (a *FakeAudio) SetVolume(v float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.volume = max(0, min(1, v))
}

func (a *FakeAudio) Position() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pos
}

func (a *FakeAudio) Duration() time.Duration { return 3 * time.Minute }

func (a *FakeAudio) Info() media.AudioTrackInfo { return media.AudioTrackInfo{} }

func (a *FakeAudio) Spectrum(bands int) []float64 { return make([]float64, bands) }
