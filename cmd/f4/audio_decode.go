package main

// Turning an audio file into the one thing the engine plays: 16-bit little
// endian interleaved stereo PCM, plus what the panel wants to print about it.
//
// Four formats are decoded in Go — MP3, WAV, FLAC and Ogg Vorbis — because
// pure decoders for them exist and are small. Everything else (AMR from a
// dictaphone, AAC/M4A, Opus, WMA, ...) goes through ffmpeg, which is already
// what f4 leans on for video; it writes raw PCM to a pipe and the engine
// reads it like any other source. So a recording plays anywhere ffmpeg does,
// and the missing-tool message says what to install where it does not.

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
	"github.com/mewkiz/flac"
	"github.com/unxed/vtui"
)

// audioSource is a decoded track: Reader yields 16-bit LE stereo PCM at
// Rate. Length is the decoded byte count, or zero when the format cannot say
// up front; Duration is derived from it, or supplied by the decoder for
// formats where the sample count is not known but the time is.
type audioSource struct {
	io.Reader
	Rate     int
	Mono     bool
	Codec    string // "MP3", "FLAC", "AMR-NB", ... for the facts line
	Length   int64
	Duration time.Duration
	closer   func()
}

// Close releases whatever the decoder holds: the file, or the ffmpeg process.
func (s *audioSource) Close() {
	if s != nil && s.closer != nil {
		s.closer()
		s.closer = nil
	}
}

// audioFormat says how a file name is played: natively, or through ffmpeg.
type audioFormat struct {
	Codec    string
	External bool
}

// audioFormats maps extension (lower case, with the dot) to how it is
// played. Anything not here is not audio as far as the player is concerned:
// the extension is what says the file is a recording, and sniffing every
// file a user presses Enter on would promise something quite different.
var audioFormats = map[string]audioFormat{
	".mp3":  {Codec: "MP3"},
	".wav":  {Codec: "WAV"},
	".wave": {Codec: "WAV"},
	".flac": {Codec: "FLAC"},
	".ogg":  {Codec: "Vorbis"},
	".oga":  {Codec: "Vorbis"},

	// Dictaphones and phones: AMR narrow band and wide band.
	".amr": {Codec: "AMR", External: true},
	".awb": {Codec: "AMR-WB", External: true},
	".3ga": {Codec: "AMR", External: true},

	".aac":  {Codec: "AAC", External: true},
	".m4a":  {Codec: "AAC", External: true},
	".m4b":  {Codec: "AAC", External: true},
	".opus": {Codec: "Opus", External: true},
	".wma":  {Codec: "WMA", External: true},
	".ape":  {Codec: "APE", External: true},
	".wv":   {Codec: "WavPack", External: true},
	".mka":  {Codec: "Matroska", External: true},
	".aif":  {Codec: "AIFF", External: true},
	".aiff": {Codec: "AIFF", External: true},
	".mpc":  {Codec: "Musepack", External: true},
	".ac3":  {Codec: "AC-3", External: true},
	".au":   {Codec: "AU", External: true},
	".mp2":  {Codec: "MP2", External: true},
	".tta":  {Codec: "TTA", External: true},
	".spx":  {Codec: "Speex", External: true},
	".dss":  {Codec: "DSS", External: true},
	".gsm":  {Codec: "GSM", External: true},
}

func audioFormatFor(path string) (audioFormat, bool) {
	f, ok := audioFormats[strings.ToLower(filepath.Ext(path))]
	return f, ok
}

// IsAudioFile reports whether the player knows what to do with the name.
func IsAudioFile(path string) bool {
	_, ok := audioFormatFor(path)
	return ok
}

// errNeedFFmpeg is what Load returns for a format only ffmpeg can decode
// when there is no ffmpeg. The panel turns it into the install message.
var errNeedFFmpeg = errors.New("ffmpeg is needed to play this format")

// openAudioSource decodes path. preferRate is the rate the output device
// already runs at, or zero; native decoders ignore it (the engine resamples),
// ffmpeg is asked for it directly so that its output needs no second pass.
func openAudioSource(path string, preferRate int) (*audioSource, error) {
	format, ok := audioFormatFor(path)
	if !ok {
		return nil, fmt.Errorf("%s: not an audio file the player knows", filepath.Base(path))
	}
	if format.External {
		return openExternalAudio(path, format, preferRate)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var src *audioSource
	switch format.Codec {
	case "MP3":
		src, err = decodeMP3(f, path)
	case "WAV":
		src, err = decodeWAV(f)
	case "FLAC":
		src, err = decodeFLAC(f)
	case "Vorbis":
		src, err = decodeVorbis(f)
	default:
		err = fmt.Errorf("no decoder for %s", format.Codec)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	if src.Codec == "" {
		src.Codec = format.Codec
	}
	src.closer = func() { f.Close() }
	if src.Duration == 0 && src.Length > 0 && src.Rate > 0 {
		src.Duration = time.Duration(float64(src.Length) / float64(src.Rate*audioBytesPerFrame) * float64(time.Second))
	}
	return src, nil
}

// ---- MP3 --------------------------------------------------------------

func decodeMP3(f *os.File, path string) (*audioSource, error) {
	dec, err := mp3.NewDecoder(f)
	if err != nil {
		return nil, err
	}
	return &audioSource{
		Reader: dec,
		Rate:   dec.SampleRate(),
		Length: dec.Length(),
		Mono:   mp3FirstFrameIsMono(path),
	}, nil
}

// ---- WAV --------------------------------------------------------------

// wavPCM converts RIFF/WAVE integer or float PCM of any common width and
// channel count to 16-bit stereo. A dictaphone that writes 8 kHz 8-bit mono
// and a DAW that writes 96 kHz 24-bit stereo both end up here.
type wavPCM struct {
	r        io.Reader
	bits     int
	channels int
	float    bool
	left     int64 // data bytes not yet read
	frame    []byte
}

func decodeWAV(f *os.File) (*audioSource, error) {
	br := bufio.NewReader(f)
	var hdr [12]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return nil, fmt.Errorf("wav: %w", err)
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return nil, errors.New("wav: not a RIFF/WAVE file")
	}
	var (
		format, channels, bits int
		rate                   int
		haveFmt                bool
	)
	for {
		var ch [8]byte
		if _, err := io.ReadFull(br, ch[:]); err != nil {
			return nil, fmt.Errorf("wav: no data chunk: %w", err)
		}
		id := string(ch[0:4])
		size := int64(binary.LittleEndian.Uint32(ch[4:8]))
		switch id {
		case "fmt ":
			body := make([]byte, size)
			if _, err := io.ReadFull(br, body); err != nil {
				return nil, fmt.Errorf("wav: fmt chunk: %w", err)
			}
			if len(body) < 16 {
				return nil, errors.New("wav: fmt chunk too short")
			}
			format = int(binary.LittleEndian.Uint16(body[0:2]))
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits = int(binary.LittleEndian.Uint16(body[14:16]))
			// WAVE_FORMAT_EXTENSIBLE carries the real format in a GUID
			// whose first two bytes are the classic code.
			if format == 0xFFFE && len(body) >= 26 {
				format = int(binary.LittleEndian.Uint16(body[24:26]))
			}
			haveFmt = true
			if size%2 == 1 {
				_, _ = br.ReadByte()
			}
		case "data":
			if !haveFmt {
				return nil, errors.New("wav: data before fmt")
			}
			if channels < 1 || rate < 1 {
				return nil, fmt.Errorf("wav: unusable format: %d channels at %d Hz", channels, rate)
			}
			switch {
			case format == 1 && (bits == 8 || bits == 16 || bits == 24 || bits == 32):
			case format == 3 && bits == 32:
			default:
				return nil, fmt.Errorf("wav: unsupported encoding (format %d, %d bits); ffmpeg would play it", format, bits)
			}
			// A streaming writer that never went back to fix the size
			// leaves 0 or 0xFFFFFFFF there; read to EOF then.
			if size == 0 || size == 0xFFFFFFFF {
				if st, err := f.Stat(); err == nil {
					size = st.Size()
				} else {
					size = 1 << 62
				}
			}
			bytesPerFrame := channels * bits / 8
			frames := size / int64(bytesPerFrame)
			pcm := &wavPCM{r: br, bits: bits, channels: channels, float: format == 3, left: frames * int64(bytesPerFrame), frame: make([]byte, bytesPerFrame)}
			return &audioSource{
				Reader: pcm,
				Rate:   rate,
				Mono:   channels == 1,
				Length: frames * audioBytesPerFrame,
			}, nil
		default:
			if size%2 == 1 {
				size++
			}
			if _, err := io.CopyN(io.Discard, br, size); err != nil {
				return nil, fmt.Errorf("wav: skipping %q: %w", id, err)
			}
		}
	}
}

func (w *wavPCM) sample(b []byte) int16 {
	switch {
	case w.float:
		v := math.Float32frombits(binary.LittleEndian.Uint32(b))
		v = max(-1, min(1, v))
		return int16(v * 32767)
	case w.bits == 8:
		return int16(int(b[0])-128) << 8
	case w.bits == 16:
		return int16(binary.LittleEndian.Uint16(b))
	case w.bits == 24:
		return int16(uint16(b[1]) | uint16(b[2])<<8)
	default: // 32
		return int16(binary.LittleEndian.Uint32(b) >> 16)
	}
}

func (w *wavPCM) Read(p []byte) (int, error) {
	n := 0
	width := w.bits / 8
	for n+audioBytesPerFrame <= len(p) {
		if w.left < int64(len(w.frame)) {
			if n == 0 {
				return 0, io.EOF
			}
			return n, nil
		}
		if _, err := io.ReadFull(w.r, w.frame); err != nil {
			if n > 0 {
				return n, nil
			}
			if err == io.ErrUnexpectedEOF {
				err = io.EOF
			}
			return 0, err
		}
		w.left -= int64(len(w.frame))
		l := w.sample(w.frame[:width])
		r := l
		if w.channels > 1 {
			r = w.sample(w.frame[width : 2*width])
		}
		putSample(p[n:], l)
		putSample(p[n+2:], r)
		n += audioBytesPerFrame
	}
	return n, nil
}

func putSample(p []byte, s int16) {
	p[0] = byte(s)
	p[1] = byte(uint16(s) >> 8)
}

// ---- FLAC -------------------------------------------------------------

// flacPCM pulls one frame at a time from mewkiz/flac and hands out its
// samples as 16-bit stereo, shifting whatever bit depth the file has.
type flacPCM struct {
	stream *flac.Stream
	buf    []byte
	err    error
}

func decodeFLAC(f *os.File) (*audioSource, error) {
	stream, err := flac.New(f)
	if err != nil {
		return nil, fmt.Errorf("flac: %w", err)
	}
	info := stream.Info
	if info == nil || info.SampleRate == 0 {
		return nil, errors.New("flac: no stream info")
	}
	return &audioSource{
		Reader: &flacPCM{stream: stream},
		Rate:   int(info.SampleRate),
		Mono:   info.NChannels == 1,
		Length: int64(info.NSamples) * audioBytesPerFrame,
	}, nil
}

func (fp *flacPCM) fill() error {
	frame, err := fp.stream.ParseNext()
	if err != nil {
		return err
	}
	if len(frame.Subframes) == 0 {
		return nil
	}
	bits := int(frame.BitsPerSample)
	if bits == 0 {
		bits = int(fp.stream.Info.BitsPerSample)
	}
	shift := bits - 16
	conv := func(v int32) int16 {
		if shift >= 0 {
			return int16(v >> shift)
		}
		return int16(v << -shift)
	}
	n := frame.Subframes[0].NSamples
	out := make([]byte, 0, n*audioBytesPerFrame)
	left := frame.Subframes[0].Samples
	right := left
	if len(frame.Subframes) > 1 {
		right = frame.Subframes[1].Samples
	}
	var frameBuf [audioBytesPerFrame]byte
	for i := 0; i < n && i < len(left) && i < len(right); i++ {
		putSample(frameBuf[0:], conv(left[i]))
		putSample(frameBuf[2:], conv(right[i]))
		out = append(out, frameBuf[:]...)
	}
	fp.buf = out
	return nil
}

func (fp *flacPCM) Read(p []byte) (int, error) {
	for len(fp.buf) == 0 {
		if fp.err != nil {
			return 0, fp.err
		}
		if err := fp.fill(); err != nil {
			fp.err = err
			if err != io.EOF {
				fp.err = fmt.Errorf("flac: %w", err)
			}
			return 0, fp.err
		}
	}
	n := copy(p, fp.buf)
	fp.buf = fp.buf[n:]
	return n, nil
}

// ---- Ogg Vorbis -------------------------------------------------------

// vorbisPCM reads interleaved float32 from jfreymuth/oggvorbis and folds any
// channel layout down to stereo: mono is doubled, anything wider keeps its
// first two channels, which for the 5.1 layouts Vorbis defines are front
// left and right.
type vorbisPCM struct {
	r        *oggvorbis.Reader
	channels int
	floats   []float32
	pending  []float32
	err      error
}

func decodeVorbis(f *os.File) (*audioSource, error) {
	r, err := oggvorbis.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("vorbis: %w", err)
	}
	ch := r.Channels()
	if ch < 1 || r.SampleRate() < 1 {
		return nil, errors.New("vorbis: unusable stream")
	}
	// Length is total samples per channel; the reader knows it only when
	// the input can seek, which a file can.
	return &audioSource{
		Reader: &vorbisPCM{r: r, channels: ch, floats: make([]float32, 4096*ch)},
		Rate:   r.SampleRate(),
		Mono:   ch == 1,
		Length: r.Length() * audioBytesPerFrame,
	}, nil
}

func (v *vorbisPCM) Read(p []byte) (int, error) {
	n := 0
	for n+audioBytesPerFrame <= len(p) {
		if len(v.pending) < v.channels {
			if v.err != nil {
				break
			}
			m, err := v.r.Read(v.floats)
			// Keep whole frames only; a short read at a page boundary
			// is not a partial frame in practice, but be safe.
			m -= m % v.channels
			v.pending = v.floats[:m]
			if err != nil {
				v.err = err
			}
			if m == 0 {
				continue
			}
		}
		l := v.pending[0]
		r := l
		if v.channels > 1 {
			r = v.pending[1]
		}
		v.pending = v.pending[v.channels:]
		putSample(p[n:], int16(max(-1, min(1, l))*32767))
		putSample(p[n+2:], int16(max(-1, min(1, r))*32767))
		n += audioBytesPerFrame
	}
	if n == 0 && v.err != nil {
		if v.err == io.EOF {
			return 0, io.EOF
		}
		return 0, fmt.Errorf("vorbis: %w", v.err)
	}
	return n, nil
}

// ---- ffmpeg -----------------------------------------------------------

// openExternalAudio has ffmpeg decode the file to raw stereo PCM on its
// standard output. Duration comes from ffprobe when there is one, or from
// the file itself for AMR, whose frames are short enough to count.
func openExternalAudio(path string, format audioFormat, preferRate int) (*audioSource, error) {
	bin, ok := toolFFmpeg.Find()
	if !ok {
		return nil, errNeedFFmpeg
	}
	// ffmpeg reports a missing file on stderr and exits; check here so
	// the panel shows the open error instead of a silent empty track.
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	if preferRate <= 0 {
		preferRate = 48000
	}
	cmd := exec.Command(bin, externalAudioArgs(path, preferRate)...)
	cmd.Env = toolEnv()
	cmd.Stderr = nil
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s would not start: %w", filepath.Base(bin), err)
	}
	vtui.DebugLog("AUDIO: %s decoding %q at %d Hz", bin, path, preferRate)

	src := &audioSource{
		Reader: bufio.NewReaderSize(out, 64*1024),
		Rate:   preferRate,
		Codec:  format.Codec,
		closer: func() {
			_ = out.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		},
	}
	if info, ok := amrFileInfo(path); ok {
		src.Codec, src.Mono, src.Duration = info.Codec, true, info.Duration
	} else if pi, ok := ffprobeAudio(path); ok {
		src.Duration = pi.Duration
		src.Mono = pi.Channels == 1
		if pi.Codec != "" {
			src.Codec = pi.Codec
		}
	}
	if src.Duration > 0 {
		src.Length = int64(src.Duration.Seconds() * float64(preferRate*audioBytesPerFrame))
	}
	return src, nil
}

// externalAudioArgs is ffmpeg's command line, kept as a plain function so
// its shape can be checked without ffmpeg or a sound device.
func externalAudioArgs(path string, rate int) []string {
	return []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", path,
		"-vn", "-sn", "-dn",
		"-f", "s16le", "-acodec", "pcm_s16le",
		"-ac", "2", "-ar", strconv.Itoa(rate),
		"-",
	}
}

type probeInfo struct {
	Codec    string
	Channels int
	Duration time.Duration
}

var toolFFprobe = ExternalTool{Names: []string{"ffprobe", "avprobe"}, Purpose: "reading audio metadata"}

// ffprobeAudio asks ffprobe for the first audio stream's codec and channel
// count and the container's duration. Any failure is "unknown": the track
// still plays, the clock just shows no total.
func ffprobeAudio(path string) (probeInfo, bool) {
	bin, ok := toolFFprobe.Find()
	if !ok {
		return probeInfo{}, false
	}
	cmd := exec.Command(bin, "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=codec_name,channels:format=duration",
		"-of", "default=noprint_wrappers=1", path)
	cmd.Env = toolEnv()
	outBytes, err := cmd.Output()
	if err != nil {
		vtui.DebugLog("AUDIO: ffprobe failed for %q: %v", path, err)
		return probeInfo{}, false
	}
	return parseFFprobeOutput(string(outBytes)), true
}

func parseFFprobeOutput(s string) probeInfo {
	var pi probeInfo
	for _, line := range strings.Split(s, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "codec_name":
			pi.Codec = strings.ToUpper(val)
		case "channels":
			pi.Channels, _ = strconv.Atoi(val)
		case "duration":
			if sec, err := strconv.ParseFloat(val, 64); err == nil && sec > 0 {
				pi.Duration = time.Duration(sec * float64(time.Second))
			}
		}
	}
	return pi
}

// ---- AMR --------------------------------------------------------------

// AMR storage format (RFC 4867): a magic line, then frames of 20 ms each.
// Every frame starts with one header byte whose bits 3–6 are the frame
// type, and the type fixes the frame's size, so the duration of a file is
// its frame count times 20 ms — no decoder needed for the clock.
const (
	amrNBMagic = "#!AMR\n"
	amrWBMagic = "#!AMR-WB\n"
)

// Frame sizes in bytes, header byte included, by frame type.
var (
	amrNBFrameSizes = [16]int{13, 14, 16, 18, 20, 21, 27, 32, 6, 1, 1, 1, 1, 1, 1, 1}
	amrWBFrameSizes = [16]int{18, 24, 33, 37, 41, 47, 51, 59, 61, 6, 1, 1, 1, 1, 1, 1}
)

type amrInfo struct {
	Codec    string
	Rate     int
	Frames   int
	Duration time.Duration
}

// amrFileInfo returns false for anything that is not an AMR file, so the
// caller can fall through to ffprobe for other formats.
func amrFileInfo(path string) (amrInfo, bool) {
	f, err := os.Open(path)
	if err != nil {
		return amrInfo{}, false
	}
	defer f.Close()
	return parseAMR(bufio.NewReader(f))
}

func parseAMR(r *bufio.Reader) (amrInfo, bool) {
	head, err := r.Peek(len(amrWBMagic))
	if err != nil && len(head) < len(amrNBMagic) {
		return amrInfo{}, false
	}
	var info amrInfo
	var sizes *[16]int
	switch {
	case strings.HasPrefix(string(head), amrWBMagic):
		info = amrInfo{Codec: "AMR-WB", Rate: 16000}
		sizes = &amrWBFrameSizes
		_, _ = r.Discard(len(amrWBMagic))
	case strings.HasPrefix(string(head), amrNBMagic):
		info = amrInfo{Codec: "AMR-NB", Rate: 8000}
		sizes = &amrNBFrameSizes
		_, _ = r.Discard(len(amrNBMagic))
	default:
		return amrInfo{}, false
	}
	for {
		b, err := r.ReadByte()
		if err != nil {
			break
		}
		size := sizes[(b>>3)&0x0f]
		if _, err := r.Discard(size - 1); err != nil {
			break
		}
		info.Frames++
	}
	info.Duration = time.Duration(info.Frames) * 20 * time.Millisecond
	return info, true
}
