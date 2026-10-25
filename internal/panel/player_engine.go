package panel

import (
	"time"

	"github.com/unxed/f4/internal/media"
)

// PlayerEngine is the audio output the player drives: media.AudioEngine on
// the desktop and its stub where there is no sound, or, in the tests, an
// engine that pretends to play so that what the keys do to playback can be
// checked on a machine without a sound device.
type PlayerEngine interface {
	Load(path string) error
	Close()
	Play()
	TogglePause() bool
	Stop()
	Seek(delta time.Duration) bool
	IsPlaying() bool
	IsLoaded() bool
	Finished() bool
	Volume() float64
	SetVolume(v float64)
	Position() time.Duration
	Duration() time.Duration
	Info() media.AudioTrackInfo
	Spectrum(bands int) []float64
}

// NewPlayerEngine makes the audio output of a new player. Tests swap it for
// one that plays nothing.
var NewPlayerEngine = func() PlayerEngine { return media.NewAudioEngine() }
