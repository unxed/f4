package media

import (
	"errors"
	"io"
	"math"
	"os"
	"sync"
	"time"

	"github.com/unxed/vtui"
)

// AudioTrackInfo is what the panel prints in the "128kbps 44kHz stereo"
// slot. Bitrate is the average over the file (size / duration), which is
// what most players show for VBR anyway; for MP3 the channel mode comes from
// the first frame header because go-mp3 always emits stereo PCM.
type AudioTrackInfo struct {
	BitrateKbps int
	SampleRate  int
	Mono        bool
	Codec       string
}

const AudioBytesPerFrame = 4 // go-mp3 output: 16-bit LE stereo

var errAudioUnavailable = errors.New("audio output is not available on this system")

// ErrNeedFFmpeg is what Load returns for a format only ffmpeg can decode
// when there is no ffmpeg. The panel turns it into the install message.
// It lives here, not in audio_decode.go, because that file is built only
// where the real AudioEngine is (see its //go:build comment); the stub
// engine never returns this error but panel/player.go still names the
// symbol unconditionally, so it must exist on every build.
var ErrNeedFFmpeg = errors.New("ffmpeg is needed to play this format")

// pcmTap sits between the decoder and the device. It counts bytes for the
// position clock and keeps the last pcmTapWindow mono samples for the
// spectrum display. It is the only place that sees the PCM, so it is cheap:
// one pass over the bytes oto asks for anyway.
type pcmTap struct {
	r    io.Reader
	rate int

	mu          sync.Mutex
	n           int64
	done        bool
	dataLogged  bool
	errorLogged bool
	eofLogged   bool
	ring        [pcmTapWindow]float64
	pos         int
}

const pcmTapWindow = 512

func NewPCMTap(r io.Reader, rate int) *pcmTap {
	return &pcmTap{r: r, rate: rate}
}

func (t *pcmTap) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	t.mu.Lock()
	t.n += int64(n)
	// Keep a few hundred samples, subsampled: enough for a 16-band bar
	// display, far too few for anything an analyser would call a
	// spectrum. Taking every 4th frame keeps the loop trivial.
	for i := 0; i+3 < n; i += AudioBytesPerFrame * 4 {
		l := int16(uint16(p[i]) | uint16(p[i+1])<<8)
		r := int16(uint16(p[i+2]) | uint16(p[i+3])<<8)
		t.ring[t.pos] = (float64(l) + float64(r)) / (2 * 32768)
		t.pos = (t.pos + 1) % pcmTapWindow
	}
	logData := n > 0 && !t.dataLogged
	if logData {
		t.dataLogged = true
	}
	logError := err != nil && err != io.EOF && !t.errorLogged
	if logError {
		t.errorLogged = true
	}
	logEOF := err == io.EOF && !t.eofLogged
	if logEOF {
		t.eofLogged = true
	}
	total := t.n
	if err == io.EOF {
		t.done = true
	}
	t.mu.Unlock()
	if logData {
		vtui.DebugLog("AUDIO: PCM data reached output tap: read=%d total=%d sample_rate=%d", n, total, t.rate)
	}
	if logError {
		vtui.DebugLog("AUDIO: PCM source read failed: read=%d total=%d err=%v", n, total, err)
	}
	if logEOF {
		vtui.DebugLog("AUDIO: PCM source reached EOF: total=%d", total)
	}
	return n, err
}

// startAt makes the tap count from n bytes, for a track a seek started in
// its middle, so that the clock says where it is.
func (t *pcmTap) startAt(n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.n = n
}

func (t *pcmTap) BytesRead() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.n
}

func (t *pcmTap) Eof() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done
}

// spectrum evaluates the Goertzel filter at bands log-spaced centre
// frequencies between 60 Hz and ~12 kHz over the ring buffer. That is
// bands×window multiply-adds (about 8k for 16 bands), which is nothing at
// the ~7 Hz the panel repaints; an FFT would be no faster to write or run
// at this size. Output is dB-ish scaled into 0..1.
func (t *pcmTap) Spectrum(bands int) []float64 {
	out := make([]float64, bands)
	if bands <= 0 {
		return out
	}
	t.mu.Lock()
	var buf [pcmTapWindow]float64
	copy(buf[:], t.ring[:])
	rate := float64(t.rate) / 4 // subsampled in Read
	t.mu.Unlock()
	lo, hi := 60.0, math.Min(12000, rate/2*0.95)
	for b := 0; b < bands; b++ {
		f := lo * math.Pow(hi/lo, float64(b)/float64(max(bands-1, 1)))
		coeff := 2 * math.Cos(2*math.Pi*f/rate)
		var s0, s1, s2 float64
		for i := 0; i < pcmTapWindow; i++ {
			// Hann window keeps neighbouring bands from bleeding into
			// each other on a flat spectrum.
			w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/pcmTapWindow)
			s0 = buf[i]*w + coeff*s1 - s2
			s2, s1 = s1, s0
		}
		power := s1*s1 + s2*s2 - coeff*s1*s2
		mag := math.Sqrt(math.Max(power, 0)) / (pcmTapWindow / 4)
		db := 20 * math.Log10(mag+1e-9)
		// -50 dB .. 0 dB → 0..1
		v := (db + 50) / 50
		out[b] = math.Max(0, math.Min(1, v))
	}
	return out
}

// skipPCM reads and drops the PCM of the first d of r, a stream of
// AudioBytesPerFrame-byte frames at rate, and says how many bytes it dropped.
// It drops whole frames only. Dropping part of one shifts every sample after it
// by a byte or two, which the speakers play as white noise: the first seek of
// a track was noise and the next one, by luck back on a frame, played (#2185).
// And the resampler hands out whole frames only, so the last few bytes of a
// partial frame never came and the copy waited for them forever.
func skipPCM(r io.Reader, d time.Duration, rate int) (int64, error) {
	frames := int64(d.Seconds() * float64(rate))
	if frames <= 0 {
		return 0, nil
	}
	return io.CopyN(io.Discard, r, frames*AudioBytesPerFrame)
}

// linearResampler converts 16-bit stereo LE PCM from srcRate to dstRate.
// It reads whole frames from the source and interpolates between adjacent
// ones; good enough for the 44.1↔48 kHz cases that occur in practice.
type linearResampler struct {
	src     io.Reader
	step    float64 // source frames per output frame
	pos     float64 // fractional position in source frames
	prev    [2]int16
	cur     [2]int16
	have    bool
	Eof     bool
	err     error
	scratch [AudioBytesPerFrame]byte
}

func NewLinearResampler(src io.Reader, srcRate, dstRate int) *linearResampler {
	return &linearResampler{src: src, step: float64(srcRate) / float64(dstRate)}
}

func (r *linearResampler) next() bool {
	if _, err := io.ReadFull(r.src, r.scratch[:]); err != nil {
		r.Eof = true
		if err != io.EOF {
			r.err = err
		}
		return false
	}
	r.prev = r.cur
	r.cur[0] = int16(uint16(r.scratch[0]) | uint16(r.scratch[1])<<8)
	r.cur[1] = int16(uint16(r.scratch[2]) | uint16(r.scratch[3])<<8)
	return true
}

func (r *linearResampler) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if !r.have {
		if !r.next() {
			if r.err != nil {
				return 0, r.err
			}
			return 0, io.EOF
		}
		r.prev = r.cur
		r.have = true
	}
	n := 0
	for n+AudioBytesPerFrame <= len(p) {
		for r.pos >= 1 {
			if !r.next() {
				if n == 0 {
					if r.err != nil {
						return 0, r.err
					}
					return 0, io.EOF
				}
				if r.err != nil {
					return n, r.err
				}
				return n, nil
			}
			r.pos--
		}
		for ch := 0; ch < 2; ch++ {
			v := float64(r.prev[ch])*(1-r.pos) + float64(r.cur[ch])*r.pos
			s := int16(v)
			p[n+ch*2] = byte(s)
			p[n+ch*2+1] = byte(uint16(s) >> 8)
		}
		n += AudioBytesPerFrame
		r.pos += r.step
	}
	return n, nil
}

// Mp3FirstFrameIsMono skips an ID3v2 tag if present and reads the channel
// mode bits of the first frame header. Any doubt → stereo, which is what
// the decoder outputs regardless.
func Mp3FirstFrameIsMono(Path string) bool {
	f, err := os.Open(Path)
	if err != nil {
		vtui.DebugLog("AUDIO: MP3 frame inspection failed to open %q: %v", Path, err)
		return false
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	n, readErr := io.ReadFull(f, buf)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		vtui.DebugLog("AUDIO: MP3 frame inspection read failed for %q: %v", Path, readErr)
	}
	buf = buf[:n]
	off := 0
	if len(buf) >= 10 && buf[0] == 'I' && buf[1] == 'D' && buf[2] == '3' {
		size := int(buf[6]&0x7f)<<21 | int(buf[7]&0x7f)<<14 | int(buf[8]&0x7f)<<7 | int(buf[9]&0x7f)
		off = 10 + size
	}
	for i := off; i+3 < len(buf); i++ {
		if buf[i] == 0xFF && buf[i+1]&0xE0 == 0xE0 && buf[i+1]&0x18 != 0x08 && buf[i+1]&0x06 != 0 {
			mono := buf[i+3]>>6 == 3
			vtui.DebugLog("AUDIO: MP3 first frame inspected Path=%q offset=%d mono=%v", Path, i, mono)
			return mono
		}
	}
	vtui.DebugLog("AUDIO: MP3 first frame not found Path=%q id3_offset=%d bytes=%d", Path, off, len(buf))
	return false
}
