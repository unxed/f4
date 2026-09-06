package main

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

func TestAudioFormatsByExtension(t *testing.T) {
	for _, name := range []string{"a.mp3", "b.WAV", "c.flac", "d.ogg", "REC001.AMR", "e.m4a", "f.opus"} {
		if !IsAudioFile(name) {
			t.Errorf("%s should be audio", name)
		}
	}
	for _, name := range []string{"a.txt", "b.jpg", "c.mp4", "noext"} {
		if IsAudioFile(name) {
			t.Errorf("%s should not be audio", name)
		}
	}
	if f, _ := audioFormatFor("x.amr"); !f.External {
		t.Errorf("AMR must go through ffmpeg")
	}
	if f, _ := audioFormatFor("x.flac"); f.External {
		t.Errorf("FLAC is decoded natively")
	}
}

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
	path := filepath.Join(dir, "rec.wav")
	// Three 8-bit unsigned samples: silence, full positive, full negative.
	if err := os.WriteFile(path, wavBytes(1, 1, 8000, 8, []byte{128, 255, 0}), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := openAudioSource(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if src.Rate != 8000 || !src.Mono || src.Codec != "WAV" {
		t.Errorf("rate=%d mono=%v codec=%q", src.Rate, src.Mono, src.Codec)
	}
	if src.Length != 3*audioBytesPerFrame {
		t.Errorf("length=%d want %d", src.Length, 3*audioBytesPerFrame)
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
	path := filepath.Join(dir, "s.wav")
	// One frame: left = 0x123456 (→ 0x1234), right = 0xFFFF00, i.e. -256
	// in 24 bits, which is -1 once the low byte is dropped.
	data := []byte{0x56, 0x34, 0x12, 0x00, 0xFF, 0xFF}
	if err := os.WriteFile(path, wavBytes(1, 2, 44100, 24, data), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := openAudioSource(path, 0)
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
	path := filepath.Join(dir, "adpcm.wav")
	if err := os.WriteFile(path, wavBytes(0x11, 1, 8000, 4, []byte{0, 0}), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openAudioSource(path, 0); err == nil || !strings.Contains(err.Error(), "unsupported") {
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
	args := externalAudioArgs("/tmp/x.amr", 48000)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-nostdin", "-i /tmp/x.amr", "-f s16le", "-ac 2", "-ar 48000", "-vn"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if args[len(args)-1] != "-" {
		t.Errorf("output must be stdout, got %q", args[len(args)-1])
	}
}

func TestParseFFprobeOutput(t *testing.T) {
	pi := parseFFprobeOutput("codec_name=amr_nb\nchannels=1\nduration=12.500000\n")
	if pi.Codec != "AMR_NB" || pi.Channels != 1 || pi.Duration != 12500*time.Millisecond {
		t.Errorf("%+v", pi)
	}
}

func TestPlayerStopIfPlayingTrimsQueue(t *testing.T) {
	pp := newTestPlayerPanel()
	pp.queue = []string{"/r/1.amr", "/r/2.amr", "/r/3.amr", "/r/4.amr"}
	pp.queuePos = 2
	pp.current = &playlistItem{Name: "3", Path: "/r/3.amr"}

	// Deleting an earlier file shifts the position; the current one stays.
	pp.StopIfPlaying([]string{"/r/1.amr"})
	if pp.current == nil || pp.queuePos != 1 || len(pp.queue) != 3 {
		t.Fatalf("after deleting 1: pos=%d queue=%v current=%v", pp.queuePos, pp.queue, pp.current)
	}
	// Deleting the playing file stops it and drops it from the queue.
	pp.StopIfPlaying([]string{"/r/3.amr"})
	if pp.current != nil || len(pp.queue) != 2 || pp.queue[1] != "/r/4.amr" {
		t.Fatalf("after deleting 3: queue=%v current=%v", pp.queue, pp.current)
	}
	// An unrelated file changes nothing.
	pp.StopIfPlaying([]string{"/elsewhere.mp3"})
	if len(pp.queue) != 2 {
		t.Fatalf("queue=%v", pp.queue)
	}
}

func TestPlaylistItemForFileAcceptsRecordings(t *testing.T) {
	dir := t.TempDir()
	amr := filepath.Join(dir, "2026-09-06 12.00.amr")
	if err := os.WriteFile(amr, []byte(amrNBMagic), 0o600); err != nil {
		t.Fatal(err)
	}
	it := playlistItemForFile(amr)
	if it == nil || it.Name != "2026-09-06 12.00" {
		t.Errorf("item=%+v", it)
	}
	if playlistItemForFile(filepath.Join(dir, "notes.txt")) != nil {
		t.Errorf("text file accepted")
	}
}
