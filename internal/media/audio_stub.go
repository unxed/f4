//go:build noffi || lite || android || !(windows || ((linux || darwin || freebsd) && (amd64 || arm64)))

package media

import "time"

// AudioEngine on platforms where oto (and therefore purego) is not
// available: every Load fails with errAudioUnavailable and the panel shows
// that in its title row. The playlist still works, so the tree is usable
// as a plain playlist editor there.
type AudioEngine struct {
	volume float64
}

func NewAudioEngine() *AudioEngine             { return &AudioEngine{volume: 0.8} }
func (a *AudioEngine) Load(string) error       { return errAudioUnavailable }
func (a *AudioEngine) Close()                  {}
func (a *AudioEngine) Play()                   {}
func (a *AudioEngine) Pause()                  {}
func (a *AudioEngine) TogglePause() bool       { return false }
func (a *AudioEngine) Stop()                   {}
func (a *AudioEngine) Seek(time.Duration) bool { return false }
func (a *AudioEngine) IsPlaying() bool         { return false }
func (a *AudioEngine) IsLoaded() bool          { return false }
func (a *AudioEngine) Finished() bool          { return false }
func (a *AudioEngine) Volume() float64         { return a.volume }
func (a *AudioEngine) SetVolume(v float64)     { a.volume = max(0, min(1, v)) }
func (a *AudioEngine) Position() time.Duration { return 0 }
func (a *AudioEngine) Duration() time.Duration { return 0 }
func (a *AudioEngine) Info() audioTrackInfo    { return audioTrackInfo{} }
func (a *AudioEngine) Spectrum(bands int) []float64 {
	return make([]float64, bands)
}
