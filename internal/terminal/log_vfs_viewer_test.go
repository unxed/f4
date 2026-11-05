package terminal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/vtui"
)

func TestTerminalLogViewerRendersLargeANSIOutput(t *testing.T) {
	const line = "\x1b[32mterminal output line with enough text to make the log large\x1b[0m\n"
	want := strings.Repeat(line, 12_000)
	if len(want) <= 700*1024 {
		t.Fatalf("fixture is only %d bytes", len(want))
	}

	const width, height = 60, 8
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(width, height)
	vtui.FrameManager.Init(scr)

	vfs := NewTerminalLogVFS(nil, func() []byte { return []byte(want) })
	vv, err := viewer.NewViewerView(context.Background(), vfs, "Terminal Log")
	if err != nil {
		t.Fatalf("NewViewerView: %v", err)
	}
	defer vv.Close()
	vv.ResizeConsole(width, height)
	vtui.FrameManager.AddScreen(vv)

	deadline := time.Now().Add(5 * time.Second)
	for !terminalLogScreenContains(scr, "terminal output line") {
		if time.Now().After(deadline) {
			t.Fatal("terminal log viewer did not render the large output before timing out")
		}
		vtui.FrameManager.Step(10 * time.Millisecond)
	}
	if terminalLogScreenContains(scr, "Loading...") {
		t.Fatal("terminal log viewer remained on its loading placeholder after rendering output")
	}
}

func terminalLogScreenContains(scr *vtui.ScreenBuf, text string) bool {
	for y := 0; y < scr.Height(); y++ {
		var row strings.Builder
		for x := 0; x < scr.Width(); x++ {
			row.WriteRune(rune(scr.GetCell(x, y).Char))
		}
		if strings.Contains(row.String(), text) {
			return true
		}
	}
	return false
}
