package media

import (
	"io"
	"testing"
	"time"
)

// countingPCM is a stream of frames that say where they are: frame i holds
// i in the left sample and -i in the right one, so a frame read from the
// middle of the stream shows both where it came from and whether it was read
// from its first byte.
type countingPCM struct {
	pos, end int64 // bytes
}

func (c *countingPCM) Read(p []byte) (int, error) {
	if c.pos >= c.end {
		return 0, io.EOF
	}
	n := 0
	for ; n < len(p) && c.pos < c.end; n, c.pos = n+1, c.pos+1 {
		sample := c.pos / AudioBytesPerFrame % 30000
		if c.pos%AudioBytesPerFrame >= 2 {
			sample = -sample
		}
		bits := uint16(sample)                 // #nosec G115 -- |sample| < 30000, a 16-bit sample in two's complement
		p[n] = byte(bits >> (8 * (c.pos % 2))) // #nosec G115 -- one byte of the sample, the low one first
	}
	return n, nil
}

func readFrame(t *testing.T, r io.Reader) (l, r2 int16) {
	t.Helper()
	var b [AudioBytesPerFrame]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		t.Fatal(err)
	}
	return int16(uint16(b[0]) | uint16(b[1])<<8), int16(uint16(b[2]) | uint16(b[3])<<8) // #nosec G115 -- the bits of a 16-bit sample
}

// The first seek of a track played white noise and the next one played the
// track: the seek dropped a byte count that was not whole frames, so every
// sample after it was read a byte or two off. skipPCM drops whole frames, so
// the frame after it is read from its first byte.
func TestSkipPCMDropsWholeFrames(t *testing.T) {
	const rate = 44100
	for _, d := range []time.Duration{
		5 * time.Second,
		1234567891 * time.Nanosecond, // a position the clock gives back
		7*time.Second + 333*time.Millisecond,
		10 * time.Microsecond, // less than a frame
	} {
		src := &countingPCM{end: 60 * rate * AudioBytesPerFrame}
		dropped, err := skipPCM(src, d, rate)
		if err != nil {
			t.Fatalf("skipPCM(%s): %v", d, err)
		}
		if dropped%AudioBytesPerFrame != 0 {
			t.Errorf("skipPCM(%s) dropped %d bytes, not whole frames", d, dropped)
		}
		want := int16(int64(d.Seconds()*rate) % 30000)
		if l, r := readFrame(t, src); l != want || r != -want {
			t.Errorf("after skipPCM(%s) the next frame is %d/%d, want %d/%d", d, l, r, want, -want)
		}
	}
}

// Through the resampler (a 44.1 kHz file on a 48 kHz device) the seek did
// not come back at all: the resampler gives whole frames only, and the bytes
// of a partial one it was asked for never came.
func TestSkipPCMThroughTheResamplerReturns(t *testing.T) {
	src := NewLinearResampler(&countingPCM{end: 60 * 44100 * AudioBytesPerFrame}, 44100, 48000)
	done := make(chan error, 1)
	go func() {
		_, err := skipPCM(src, 1234567891*time.Nanosecond, 48000)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("skipPCM through the resampler did not return")
	}
	if l, r := readFrame(t, src); l != -r {
		t.Errorf("after the seek the resampler gives the frame %d/%d, which is not whole", l, r)
	}
}
