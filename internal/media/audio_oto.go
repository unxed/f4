//go:build !noffi && !lite && !android && (windows || ((linux || darwin || freebsd) && (amd64 || arm64)))

package media

import (
	"errors"
	"io"
	"math"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/unxed/vtui"
)

// AudioEngine owns the single output device of the process and plays one
// track at a time. Decoding is audio_decode.go's business (pure Go for MP3,
// WAV, FLAC and Vorbis, ffmpeg for the rest); output is oto, which talks to
// ALSA / CoreAudio / WASAPI through purego, so no cgo is needed on the three
// desktop platforms.
//
// The oto context is created lazily on the first Load and its sample rate is
// fixed for the life of the process, so a track recorded at another rate is
// resampled (linear interpolation) before it reaches the device. That is not
// audiophile-grade, but it keeps one context, one player type and no
// re-initialisation of the device between tracks.
//
// All methods are safe to call from the UI thread; nothing here blocks on
// the device except NewContext, which is done once.
type AudioEngine struct {
	mu      sync.Mutex
	ctx     *oto.Context
	ctxRate int
	ctxErr  error

	player *oto.Player
	source *audioSource
	tap    *pcmTap
	Path   string

	duration time.Duration
	info     audioTrackInfo
	volume   float64
	loaded   bool

	contextErrorLogged bool
	playerErrorLogged  bool
	finishedLogged     bool
	bufferState        int // -1 unknown, 0 empty, 1 non-empty while playing
}

func NewAudioEngine() *AudioEngine {
	a := &AudioEngine{volume: 0.8}
	vtui.DebugLog("AUDIO: engine created")
	return a
}

// oto.NewContext may be called at most once per process — a second call
// always fails with "oto: context is already created", regardless of
// whether the first context is still in use. Every AudioEngine therefore
// shares this single process-wide context instead of creating its own, so
// that closing and reopening the player panel (e.g. by switching which side
// it is on) does not lose audio output for the rest of the process.
var (
	sharedAudioCtxMu sync.Mutex
	sharedAudioCtx   *oto.Context
	sharedAudioRate  int
	sharedAudioErr   error
)

func (a *AudioEngine) ensureContext(rate int) error {
	if a.ctx != nil {
		if err := a.contextErrLocked(); err != nil {
			return err
		}
		vtui.DebugLog("AUDIO: reusing oto context sample_rate=%d requested_rate=%d", a.ctxRate, rate)
		return nil
	}
	if a.ctxErr != nil {
		return a.ctxErr
	}

	sharedAudioCtxMu.Lock()
	defer sharedAudioCtxMu.Unlock()
	if sharedAudioCtx != nil {
		a.ctx = sharedAudioCtx
		a.ctxRate = sharedAudioRate
		vtui.DebugLog("AUDIO: reusing process-wide oto context sample_rate=%d requested_rate=%d", a.ctxRate, rate)
		return nil
	}
	if sharedAudioErr != nil {
		a.ctxErr = sharedAudioErr
		return a.ctxErr
	}

	vtui.DebugLog("AUDIO: creating oto context goos=%s goarch=%s sample_rate=%d channels=2 format=signed-int16-le", runtime.GOOS, runtime.GOARCH, rate)
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   rate,
		ChannelCount: 2,
		Format:       oto.FormatSignedInt16LE,
	})
	if err != nil {
		sharedAudioErr = errors.Join(errAudioUnavailable, err)
		a.ctxErr = sharedAudioErr
		vtui.DebugLog("AUDIO: oto.NewContext returned an error: %v", a.ctxErr)
		return a.ctxErr
	}
	<-ready
	if err := ctx.Err(); err != nil {
		sharedAudioErr = errors.Join(errAudioUnavailable, err)
		a.ctxErr = sharedAudioErr
		a.contextErrorLogged = true
		vtui.DebugLog("AUDIO: oto context failed during asynchronous initialization: %v", err)
		return a.ctxErr
	}
	sharedAudioCtx = ctx
	sharedAudioRate = rate
	a.ctx = ctx
	a.ctxRate = rate
	vtui.DebugLog("AUDIO: oto context ready sample_rate=%d channels=2 format=signed-int16-le", rate)
	return nil
}

func (a *AudioEngine) contextErrLocked() error {
	if a.ctxErr != nil {
		return a.ctxErr
	}
	if a.ctx == nil {
		return nil
	}
	if err := a.ctx.Err(); err != nil {
		a.ctxErr = errors.Join(errAudioUnavailable, err)
		if !a.contextErrorLogged {
			a.contextErrorLogged = true
			vtui.DebugLog("AUDIO: oto context reported a runtime error: %v", err)
		}
		return a.ctxErr
	}
	return nil
}

func (a *AudioEngine) checkErrorsLocked() {
	_ = a.contextErrLocked()
	if a.player != nil && !a.playerErrorLogged {
		if err := a.player.Err(); err != nil {
			a.playerErrorLogged = true
			vtui.DebugLog("AUDIO: oto player reported a runtime error Path=%q: %v", a.Path, err)
		}
	}
	a.observePlaybackLocked()
}

func (a *AudioEngine) observePlaybackLocked() {
	if a.player == nil || !a.player.IsPlaying() {
		return
	}
	buffered := a.player.BufferedSize()
	state := 0
	if buffered > 0 {
		state = 1
	}
	if state == a.bufferState {
		return
	}
	a.bufferState = state
	pcmBytes := int64(0)
	if a.tap != nil {
		pcmBytes = a.tap.BytesRead()
	}
	description := "empty"
	if state == 1 {
		description = "non-empty"
	}
	vtui.DebugLog("AUDIO: playback buffer became %s Path=%q buffered=%d pcm_bytes=%d", description, a.Path, buffered, pcmBytes)
}

// Load opens Path, decodes its header and prepares a paused player. Play
// starts it. Any previously loaded track is dropped.
func (a *AudioEngine) Load(Path string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loadLocked(Path)
}

func (a *AudioEngine) loadLocked(Path string) error {
	vtui.DebugLog("AUDIO: load requested Path=%q", Path)
	a.unloadLocked()

	// ffmpeg is told the device rate when there already is a device, so
	// that its output goes straight through; the native decoders run at
	// the file's rate and are resampled below when that differs.
	source, err := openAudioSource(Path, a.ctxRate)
	if err != nil {
		vtui.DebugLog("AUDIO: load could not decode %q: %v", Path, err)
		return err
	}
	srcRate := source.Rate
	vtui.DebugLog("AUDIO: %s decoder ready Path=%q sample_rate=%d decoded_bytes=%d", source.Codec, Path, srcRate, source.Length)
	if err := a.ensureContext(srcRate); err != nil {
		vtui.DebugLog("AUDIO: load cannot initialize output for %q: %v", Path, err)
		source.Close()
		return err
	}
	var src io.Reader = source
	if srcRate != a.ctxRate {
		vtui.DebugLog("AUDIO: resampling Path=%q from_rate=%d to_rate=%d", Path, srcRate, a.ctxRate)
		src = NewLinearResampler(source, srcRate, a.ctxRate)
	}
	a.duration = source.Duration
	a.info = audioTrackInfo{SampleRate: srcRate, Mono: source.Mono, Codec: source.Codec}
	if st, err := os.Stat(Path); err == nil && a.duration > 0 {
		a.info.BitrateKbps = int(math.Round(float64(st.Size()) * 8 / a.duration.Seconds() / 1000))
	} else if err != nil {
		vtui.DebugLog("AUDIO: stat failed for %q while calculating bitrate: %v", Path, err)
	}

	a.tap = NewPCMTap(src, a.ctxRate)
	a.source = source
	a.player = a.ctx.NewPlayer(a.tap)
	a.player.SetVolume(a.volume)
	a.Path = Path
	a.playerErrorLogged = false
	a.finishedLogged = false
	a.bufferState = -1
	a.loaded = true
	vtui.DebugLog("AUDIO: track prepared Path=%q duration=%s bitrate_kbps=%d source_rate=%d output_rate=%d mono=%v buffered=%d", Path, a.duration, a.info.BitrateKbps, srcRate, a.ctxRate, a.info.Mono, a.player.BufferedSize())
	return nil
}

// Seek moves the current track by delta and keeps its playing/paused state.
// Decoders expose a common PCM stream rather than a common seek interface,
// so the engine reloads the source and discards decoded PCM up to the target.
// This is deliberately format-independent: native FLAC/MP3/WAV and ffmpeg
// backed AMR use exactly the same path.
func (a *AudioEngine) Seek(delta time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkErrorsLocked()
	if a.player == nil || !a.loaded || a.Path == "" {
		return false
	}

	path := a.Path
	wasPlaying := a.player.IsPlaying()
	target := a.PositionLocked() + delta
	if target < 0 {
		target = 0
	}
	if a.duration > 0 && target > a.duration {
		target = a.duration
	}
	if err := a.loadLocked(path); err != nil {
		vtui.DebugLog("AUDIO: seek reload failed Path=%q: %v", path, err)
		return false
	}

	bytes := int64(float64(target) / float64(time.Second) * float64(a.ctxRate*AudioBytesPerFrame))
	if bytes > 0 {
		_, _ = io.CopyN(io.Discard, a.tap, bytes)
	}
	if wasPlaying {
		a.player.Play()
	}
	vtui.DebugLog("AUDIO: seek requested Path=%q delta=%s target=%s playing=%v", path, delta, target, wasPlaying)
	return true
}

func (a *AudioEngine) unloadLocked() {
	if a.player != nil {
		a.checkErrorsLocked()
		vtui.DebugLog("AUDIO: unloading track Path=%q playing=%v buffered=%d pcm_bytes=%d", a.Path, a.player.IsPlaying(), a.player.BufferedSize(), a.tap.BytesRead())
		a.player.Pause()
		//_ = a.player.Close() // fix linter error
		// SA1019: (*github.com/ebitengine/oto/v3.Player).Close is deprecated: as of v3.4. you don't have to call Close. (staticcheck)
		a.player = nil
	}
	if a.source != nil {
		a.source.Close()
		a.source = nil
	}
	a.tap = nil
	a.Path = ""
	a.loaded = false
}

func (a *AudioEngine) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	vtui.DebugLog("AUDIO: engine closing")
	a.unloadLocked()
}

func (a *AudioEngine) Play() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkErrorsLocked()
	if a.player == nil {
		vtui.DebugLog("AUDIO: play ignored because no track is loaded")
		return
	}
	a.player.Play()
	vtui.DebugLog("AUDIO: play requested Path=%q playing=%v buffered=%d", a.Path, a.player.IsPlaying(), a.player.BufferedSize())
	a.checkErrorsLocked()
}

func (a *AudioEngine) Pause() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkErrorsLocked()
	if a.player == nil {
		vtui.DebugLog("AUDIO: pause ignored because no track is loaded")
		return
	}
	a.player.Pause()
	vtui.DebugLog("AUDIO: pause requested Path=%q buffered=%d", a.Path, a.player.BufferedSize())
}

// TogglePause flips between playing and paused and reports the new state.
func (a *AudioEngine) TogglePause() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkErrorsLocked()
	if a.player == nil {
		vtui.DebugLog("AUDIO: toggle pause ignored because no track is loaded")
		return false
	}
	if a.player.IsPlaying() {
		a.player.Pause()
		vtui.DebugLog("AUDIO: toggled to paused Path=%q buffered=%d", a.Path, a.player.BufferedSize())
		return false
	}
	a.player.Play()
	vtui.DebugLog("AUDIO: toggled to playing Path=%q playing=%v buffered=%d", a.Path, a.player.IsPlaying(), a.player.BufferedSize())
	a.checkErrorsLocked()
	return true
}

// Stop unloads the track entirely; Play after Stop needs a Load first.
func (a *AudioEngine) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	vtui.DebugLog("AUDIO: stop requested")
	a.unloadLocked()
}

func (a *AudioEngine) IsPlaying() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkErrorsLocked()
	return a.player != nil && a.player.IsPlaying()
}

func (a *AudioEngine) IsLoaded() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkErrorsLocked()
	return a.loaded
}

// Finished is true once the decoder hit EOF and the player drained its
// buffer — the moment to advance to the next track.
func (a *AudioEngine) Finished() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkErrorsLocked()
	finished := a.loaded && a.tap != nil && a.player != nil && a.tap.Eof() && !a.player.IsPlaying()
	if finished && !a.finishedLogged {
		a.finishedLogged = true
		vtui.DebugLog("AUDIO: track finished Path=%q position=%s duration=%s", a.Path, a.PositionLocked(), a.duration)
	}
	return finished
}

func (a *AudioEngine) Volume() float64 { return a.volume }

func (a *AudioEngine) SetVolume(v float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.volume
	a.volume = math.Max(0, math.Min(1, v))
	if a.player != nil {
		a.player.SetVolume(a.volume)
	}
	if old != a.volume {
		vtui.DebugLog("AUDIO: volume changed Path=%q from=%.3f to=%.3f", a.Path, old, a.volume)
	}
}

func (a *AudioEngine) Position() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkErrorsLocked()
	return a.PositionLocked()
}

func (a *AudioEngine) PositionLocked() time.Duration {
	if a.tap == nil {
		return 0
	}
	// Subtract what oto has read from us but not yet played, so the
	// clock does not run ahead of the speakers by the buffer size.
	consumed := a.tap.BytesRead()
	if a.player != nil {
		consumed -= int64(a.player.BufferedSize())
	}
	if consumed < 0 {
		consumed = 0
	}
	return time.Duration(float64(consumed) / float64(a.ctxRate*AudioBytesPerFrame) * float64(time.Second))
}

func (a *AudioEngine) Duration() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.duration
}

func (a *AudioEngine) Info() audioTrackInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.info
}

// Spectrum returns bands bar heights in 0..1 computed from the most recent
// PCM that passed through the tap. See pcmTap.spectrum.
func (a *AudioEngine) Spectrum(bands int) []float64 {
	a.mu.Lock()
	tap := a.tap
	a.mu.Unlock()
	if tap == nil {
		return make([]float64, bands)
	}
	return tap.Spectrum(bands)
}
