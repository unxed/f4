//go:build !noffi && !lite && !android && (windows || ((linux || darwin || freebsd) && (amd64 || arm64)))

package media

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// wavBytes builds a canonical RIFF/WAVE file of the given shape.
func wavBytes(format, channels, rate, bits int, data []byte) []byte {
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + len(data)))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	w(uint32(16))
	w(uint16(format))
	w(uint16(channels))
	w(uint32(rate))
	w(uint32(rate * channels * bits / 8))
	w(uint16(channels * bits / 8))
	w(uint16(bits))
	// A LIST chunk before the data, as most recorders write.
	b.WriteString("LIST")
	w(uint32(4))
	b.WriteString("INFO")
	b.WriteString("data")
	w(uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}

func TestWAVMono8BitBecomesStereo16(t *testing.T) {
	dir := t.TempDir()
	Path := filepath.Join(dir, "rec.wav")
	// Three 8-bit unsigned samples: silence, Full positive, Full negative.
	if err := os.WriteFile(Path, wavBytes(1, 1, 8000, 8, []byte{128, 255, 0}), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := openAudioSource(Path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if src.Rate != 8000 || !src.Mono || src.Codec != "WAV" {
		t.Errorf("rate=%d mono=%v codec=%q", src.Rate, src.Mono, src.Codec)
	}
	if src.Length != 3*AudioBytesPerFrame {
		t.Errorf("length=%d want %d", src.Length, 3*AudioBytesPerFrame)
	}
	out, err := io.ReadAll(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 12 {
		t.Fatalf("decoded %d bytes, want 12: %v", len(out), out)
	}
	sample := func(i int) int16 { return int16(binary.LittleEndian.Uint16(out[i*2:])) }
	if sample(0) != 0 || sample(1) != 0 {
		t.Errorf("first frame = %d,%d want silence", sample(0), sample(1))
	}
	if sample(2) != 127<<8 || sample(3) != 127<<8 {
		t.Errorf("second frame = %d,%d want %d in both channels", sample(2), sample(3), 127<<8)
	}
	if sample(4) != -128<<8 {
		t.Errorf("third frame = %d want %d", sample(4), -128<<8)
	}
	if src.Duration != 3*time.Second/8000 {
		t.Errorf("duration=%s", src.Duration)
	}
}

func TestWAVStereo24Bit(t *testing.T) {
	dir := t.TempDir()
	Path := filepath.Join(dir, "s.wav")
	// One frame: left = 0x123456 (→ 0x1234), right = 0xFFFF00, i.e. -256
	// in 24 bits, which is -1 once the low byte is dropped.
	data := []byte{0x56, 0x34, 0x12, 0x00, 0xFF, 0xFF}
	if err := os.WriteFile(Path, wavBytes(1, 2, 44100, 24, data), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := openAudioSource(Path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	out, err := io.ReadAll(src)
	if err != nil || len(out) != 4 {
		t.Fatalf("read: %v, %d bytes", err, len(out))
	}
	if l := int16(binary.LittleEndian.Uint16(out[0:])); l != 0x1234 {
		t.Errorf("left=%#x", l)
	}
	if r := int16(binary.LittleEndian.Uint16(out[2:])); r != -1 {
		t.Errorf("right=%d", r)
	}
	if src.Mono {
		t.Errorf("stereo file reported mono")
	}
}

func TestWAVRejectsCompressed(t *testing.T) {
	dir := t.TempDir()
	Path := filepath.Join(dir, "adpcm.wav")
	if err := os.WriteFile(Path, wavBytes(0x11, 1, 8000, 4, []byte{0, 0}), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openAudioSource(Path, 0); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("err=%v", err)
	}
}

func TestParseAMRCountsFrames(t *testing.T) {
	// Three MR122 frames (type 7, 32 bytes) and one MR475 (type 0, 13
	// bytes): 4 × 20 ms.
	var b bytes.Buffer
	b.WriteString(amrNBMagic)
	for i := 0; i < 3; i++ {
		b.WriteByte(7 << 3)
		b.Write(make([]byte, 31))
	}
	b.WriteByte(0)
	b.Write(make([]byte, 12))
	info, ok := parseAMR(bufio.NewReader(&b))
	if !ok {
		t.Fatal("not recognised as AMR")
	}
	if info.Codec != "AMR-NB" || info.Rate != 8000 || info.Frames != 4 || info.Duration != 80*time.Millisecond {
		t.Errorf("%+v", info)
	}

	var wb bytes.Buffer
	wb.WriteString(amrWBMagic)
	wb.WriteByte(8 << 3) // type 8: 61 bytes
	wb.Write(make([]byte, 60))
	info, ok = parseAMR(bufio.NewReader(&wb))
	if !ok || info.Codec != "AMR-WB" || info.Rate != 16000 || info.Frames != 1 {
		t.Errorf("%+v ok=%v", info, ok)
	}

	if _, ok := parseAMR(bufio.NewReader(strings.NewReader("RIFF...."))); ok {
		t.Errorf("a RIFF file is not AMR")
	}
}

func TestExternalAudioArgsShape(t *testing.T) {
	args := externalAudioArgs("/tmp/x.amr", 48000, 0)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-nostdin", "-i /tmp/x.amr", "-f s16le", "-ac 2", "-ar 48000", "-vn"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if args[len(args)-1] != "-" {
		t.Errorf("output must be stdout, got %q", args[len(args)-1])
	}
	if strings.Contains(joined, "-ss") {
		t.Errorf("a track from its start has a start: %q", joined)
	}

	// A seek starts ffmpeg at the target, given before the input so that
	// ffmpeg seeks in the file rather than decoding up to it.
	joined = strings.Join(externalAudioArgs("/tmp/x.amr", 48000, 90500*time.Millisecond), " ")
	if !strings.Contains(joined, "-ss 90.500 -i /tmp/x.amr") {
		t.Errorf("a start at 1:30.5 is not before the input: %q", joined)
	}
}

func TestParseFFprobeOutput(t *testing.T) {
	pi := parseFFprobeOutput("codec_name=amr_nb\nchannels=1\nduration=12.500000\n")
	if pi.Codec != "AMR_NB" || pi.Channels != 1 || pi.Duration != 12500*time.Millisecond {
		t.Errorf("%+v", pi)
	}
}
