package terminal

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/vtui"
)

func TestTerminalLogViewerReadsLargeANSIOutput(t *testing.T) {
	const line = "\x1b[32mterminal output line with enough text to make the log large\x1b[0m\n"
	want := bytes.Repeat([]byte(line), 12_000)
	if len(want) <= 700*1024 {
		t.Fatalf("fixture is only %d bytes", len(want))
	}

	frames := vtui.FrameManager
	frames.Init(vtui.NewSilentScreenBuf())
	vfs := NewTerminalLogVFS(nil, func() []byte { return want })
	vv, err := viewer.NewViewerView(context.Background(), vfs, "Terminal Log")
	if err != nil {
		t.Fatalf("NewViewerView: %v", err)
	}
	defer vv.Close()
	if !vv.AnsiMode {
		t.Fatal("terminal log viewer did not enable ANSI mode")
	}

	got := make([]byte, len(want))
	for off := 0; off < len(got); {
		end := off + 64*1024
		if end > len(got) {
			end = len(got)
		}
		deadline := time.After(5 * time.Second)
		for {
			data, readErr := vv.Backend.ReadAt(int64(off), end-off)
			if readErr == piecetable.ErrLoading {
				select {
				case task := <-frames.TaskChan:
					task()
				case <-deadline:
					t.Fatalf("viewer stayed loading at byte %d of %d", off, len(got))
				}
				continue
			}
			if readErr != nil {
				t.Fatalf("ReadAt(%d): %v", off, readErr)
			}
			if len(data) != end-off {
				t.Fatalf("ReadAt(%d) returned %d bytes, want %d", off, len(data), end-off)
			}
			copy(got[off:end], data)
			off = end
			break
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatal("viewer backend returned different terminal log contents")
	}
}
