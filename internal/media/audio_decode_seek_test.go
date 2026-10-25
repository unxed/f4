//go:build !noffi && !lite && !android && (windows || ((linux || darwin || freebsd) && (amd64 || arm64)))

package media

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The fixtures in testdata are short tones whose loudness grows with time and
// which differ between the channels, so a stretch of their PCM says where in
// the track it came from. They were made with
//
//	ffmpeg -f lavfi -i 'aevalsrc=0.5*sin(2*PI*(200+150*t)*t)|0.5*sin(2*PI*(600+100*t)*t):s=11025:d=3' -c:a libmp3lame -b:a 32k sweep.mp3
//	ffmpeg -f lavfi -i 'aevalsrc=0.5*sin(2*PI*(200+150*t)*t)|0.5*sin(2*PI*(600+100*t)*t):s=11025:d=3' -c:a vorbis -strict -2 -q:a 0 sweep.ogg
//	ffmpeg -f lavfi -i 'aevalsrc=0.4*t*sin(2*PI*300*t)|0.4*t*sin(2*PI*500*t):s=8000:d=2' -c:a flac -compression_level 12 sweep.flac

// A jump to 90% of a track decoded the 90% before it (#2185). The decoders
// now seek: the PCM after a seek is what decoding from the start and
// dropping up to the same place gives, sample for sample, without the
// decoding. For MP3 that takes the frames before the target (mp3SeekWarmUp):
// go-mp3's own seek got the first five frames after it wrong in this file.
func TestAudioSourceSeekLandsWhereDecodingWould(t *testing.T) {
	wav := filepath.Join(t.TempDir(), "count.wav")
	if err := os.WriteFile(wav, wavBytes(1, 2, 8000, 16, countingWAVData(16000)), 0o600); err != nil {
		t.Fatal(err)
	}
	type seekCase struct {
		path   string
		target time.Duration
	}
	var cases []seekCase
	for _, path := range []string{wav, filepath.Join("testdata", "sweep.flac"), filepath.Join("testdata", "sweep.ogg"), filepath.Join("testdata", "sweep.mp3")} {
		// Near the start, where MP3's warm-up has fewer frames to take,
		// and in the middle.
		cases = append(cases, seekCase{path, 100 * time.Millisecond}, seekCase{path, 1234 * time.Millisecond})
	}
	for _, c := range cases {
		t.Run(filepath.Ext(c.path)+" "+c.target.String(), func(t *testing.T) {
			ref, err := openAudioSource(c.path, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer ref.Close()
			if ref.seek == nil {
				t.Fatalf("%s cannot seek", c.path)
			}
			if _, err := skipPCM(ref, c.target, ref.Rate); err != nil {
				t.Fatal(err)
			}
			want := make([]byte, 2048)
			if _, err := io.ReadFull(ref, want); err != nil {
				t.Fatal(err)
			}

			src, err := openAudioSource(c.path, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			if err := src.seek(int64(c.target.Seconds() * float64(src.Rate))); err != nil {
				t.Fatalf("seek to %s: %v", c.target, err)
			}
			got := make([]byte, len(want))
			if _, err := io.ReadFull(src, got); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < len(want); i += AudioBytesPerFrame {
				if string(got[i:i+AudioBytesPerFrame]) != string(want[i:i+AudioBytesPerFrame]) {
					t.Fatalf("after a seek to %s frame %d is % x, decoding up to it gives % x", c.target, i/AudioBytesPerFrame, got[i:i+AudioBytesPerFrame], want[i:i+AudioBytesPerFrame])
				}
			}
		})
	}
}

// countingWAVData is frames frames of 16-bit stereo, frame i holding i and
// -i, as countingPCM does.
func countingWAVData(frames int) []byte {
	src := &countingPCM{end: int64(frames) * AudioBytesPerFrame}
	data, _ := io.ReadAll(src)
	return data
}
