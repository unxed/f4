package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/vtvibe"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestAIChatPanelRichMarkdownRenderingAndBusyState(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	session := vtvibe.NewSession()
	ctxVFS := vtvibe.NewVFS(session)
	file, err := ctxVFS.Create(context.Background(), "/ctx/readme.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("context")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	reply := "" +
		"See [context](ai://ctx/readme.txt), ai://out/result.go and a wide 界 line.\n" +
		"```go:ai://out/generated.go\npackage main\n```\n" +
		"f0cacc1a AP 3.1\n\n" +
		"f0cacc1a FILE\nresult.go\n\n" +
		"f0cacc1a DELETE\n"
	var requests atomic.Int32
	busyStarted := make(chan struct{})
	releaseBusy := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n := requests.Add(1); n == 2 {
			close(busyStarted)
			<-releaseBusy
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": reply}}},
			"usage":   map[string]int{"prompt_tokens": 3, "completion_tokens": 5},
		})
	}))
	defer server.Close()
	cfg := vtvibe.Config{BaseURL: server.URL, Model: "coverage-model", APIKey: "test"}
	if err := session.Ask(context.Background(), cfg, "show the context"); err != nil {
		t.Fatal(err)
	}
	if session.LastPatch() == nil {
		t.Fatal("the AP response should expose a patch")
	}

	fp := panel.NewFileSystemPanel(0, 0, 34, 24, &aiVFSWrapper{AIVFS: ctxVFS})
	paneltest.WaitForLoad(t, fp)
	cp := NewAIChatPanel(fp)
	cp.SetPosition(0, 0, 33, 23)
	cp.SetFocus(true)
	cp.updateLines()
	if len(cp.lines) < 5 {
		t.Fatalf("rich reply produced too few chat lines: %d", len(cp.lines))
	}
	var targets []string
	for _, line := range cp.lines {
		for _, target := range line.targets {
			if target != "" {
				targets = append(targets, target)
			}
		}
	}
	joinedTargets := strings.Join(targets, "\n")
	for _, want := range []string{"ai://ctx/readme.txt", "ai://out/result.go", "ai://out/generated.go"} {
		if !strings.Contains(joinedTargets, want) {
			t.Fatalf("rendered links do not contain %q: %q", want, joinedTargets)
		}
	}

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	cp.Show(scr)
	if len(cp.visibleLinks) == 0 {
		t.Fatal("Show did not collect visible markdown links")
	}
	if cp.barKind() != aiBarPatch {
		t.Fatalf("barKind = %d, want patch bar", cp.barKind())
	}

	// Exercise link focus, copy-key handling, paging, horizontal selection,
	// and the mouse path without requiring a real panels frame to navigate.
	cp.focusedLinkIdx = 0
	if !cp.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_F5}) {
		t.Fatal("F5 on a response link was not handled")
	}
	if !cp.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT}) {
		t.Fatal("right-arrow link navigation was not handled")
	}
	cp.focusedLinkIdx = 0
	if !cp.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_ESCAPE}) {
		t.Fatal("Escape did not return focus to input")
	}
	cp.topPos = 20
	if !cp.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_PRIOR}) || cp.topPos < 0 {
		t.Fatal("PageUp was not handled")
	}
	if !cp.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_NEXT}) {
		t.Fatal("PageDown was not handled")
	}
	if !cp.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT, ControlKeyState: vtinput.ShiftPressed}) {
		t.Fatal("Shift+Left was not handled")
	}

	cp.visibleLinks = []chatLink{{row: 1, col: 1, width: 5, target: "ai://ctx/readme.txt"}}
	if !cp.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true, MouseX: 2, MouseY: 2, ButtonState: vtinput.FromLeft1stButtonPressed}) || cp.focusedLinkIdx != 0 {
		t.Fatalf("mouse link focus = %d", cp.focusedLinkIdx)
	}
	if !cp.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, WheelDirection: -1}) {
		t.Fatal("mouse wheel down was not handled")
	}
	if !cp.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, WheelDirection: 1}) {
		t.Fatal("mouse wheel up was not handled")
	}
	if cp.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType}) {
		t.Fatal("empty mouse event was unexpectedly handled")
	}

	errCh := make(chan error, 1)
	go func() { errCh <- session.Ask(context.Background(), cfg, "second question") }()
	select {
	case <-busyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second request did not reach the test server")
	}
	cp.updateLines()
	close(releaseBusy)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestAIChatPanelFormattingAndSessionSelectionContracts(t *testing.T) {
	if formatApplyPatchLabel(nil, 100) != "" {
		t.Fatal("nil patch produced a button")
	}
	patch := &vtvibe.Patch{Files: []string{"main.go"}}
	if got := formatApplyPatchLabel(patch, 100); !strings.Contains(got, "main.go") {
		t.Fatalf("patch button = %q", got)
	}
	if got := formatApplyPatchLabel(patch, 8); got == "" {
		t.Fatal("narrow patch button lost its fallback marker")
	}
	if formatAttachedFilesLabel(nil, 100) != "" {
		t.Fatal("empty attachment list produced a label")
	}
	for _, tc := range []struct {
		prefix string
		files  []string
		width  int
		want   string
	}{
		{"x", nil, 5, "x"},
		{"prefix", []string{"file"}, 5, ""},
		{"x", []string{"long-name"}, 3, ""},
		{"x", []string{"a", "b"}, 8, "x a, b "},
	} {
		if got := formatBarLabel(tc.prefix, tc.files, tc.width); got != tc.want {
			t.Errorf("formatBarLabel(%q, %#v, %d) = %q, want %q", tc.prefix, tc.files, tc.width, got, tc.want)
		}
	}
	for _, tc := range []struct {
		s     string
		width int
		want  int
	}{
		{"", 3, 0}, {"abc", 0, 3}, {"界x", 1, 0}, {"界x", 2, len("界")}, {"abc", 9, 3},
	} {
		if got := cellCutChat(tc.s, tc.width); got != tc.want {
			t.Errorf("cellCutChat(%q, %d) = %d, want %d", tc.s, tc.width, got, tc.want)
		}
	}

	s := vtvibe.NewSession()
	fp := &panel.FileSystemPanel{Vfs: &aiVFSWrapper{AIVFS: vtvibe.NewVFS(s)}}
	cp := &AIChatPanel{src: fp, focusedLinkIdx: -1}
	if cp.getSession() != s {
		t.Fatal("wrapper session was not selected")
	}
	fp.Vfs = vtvibe.NewVFS(s)
	if cp.getSession() != s {
		t.Fatal("bare AIVFS session was not selected")
	}
	fp.Vfs = vfs.NewNullVFS(0)
	if cp.getSession() == nil {
		t.Fatal("fallback AI session is nil")
	}
	if (&AIChatPanel{}).GetSelectedName() != "" {
		t.Fatal("nil chat source returned a selection")
	}
	if (&AIChatPanel{}).ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN}) {
		t.Fatal("unfocused chat panel handled a key")
	}
}
