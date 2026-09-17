package editor

import (
	"testing"
	"time"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/vtinput"
)

func TestEditorLowLevelCoverageContracts(t *testing.T) {
	for _, tc := range []struct {
		behind, indexing bool
		want             int
	}{
		{behind: true, want: hlDutyVisible},
		{indexing: true, want: hlDutyIndexing},
		{want: hlDutyAhead},
	} {
		if got := highlightDuty(tc.behind, tc.indexing); got != tc.want {
			t.Fatalf("highlightDuty(%v, %v) = %d, want %d", tc.behind, tc.indexing, got, tc.want)
		}
	}
	if got := highlightIdleGap(time.Second, 100); got != 0 {
		t.Fatalf("100%% highlight idle gap = %v", got)
	}
	if got := highlightIdleGap(time.Second, 0); got != hlIdleMax {
		t.Fatalf("zero-duty idle gap = %v, want %v", got, hlIdleMax)
	}
	if got := highlightIdleGap(time.Nanosecond, 50); got != hlIdleMin {
		t.Fatalf("minimum idle gap = %v, want %v", got, hlIdleMin)
	}
	if got := highlightIdleGap(time.Hour, 50); got != hlIdleMax {
		t.Fatalf("maximum idle gap = %v, want %v", got, hlIdleMax)
	}
	if usesStateChain(nil) || usesStateChain(&ColorerHighlighter{}) {
		t.Fatal("state-chain classification of nil/Colorer highlighter is incorrect")
	}

	clusters := editorVisualClusters("A🙂e\u0301")
	if len(clusters) != 3 || clusters[1].text != "🙂" || clusters[2].runeEnd <= clusters[2].runeStart {
		t.Fatalf("visual clusters = %#v", clusters)
	}

	ev := NewEditorView(piecetable.New([]byte("abcdef\nlast")), nil, "low-level.txt")
	defer ev.Close()
	if got, err := ev.decodeBytes(0, 100); err != nil || string(got) != "abcdef\nlast" {
		t.Fatalf("decodeBytes truncated read = %q, %v", got, err)
	}
	if got, err := ev.decodeBytes(11, 1); err != nil || got != nil {
		t.Fatalf("decodeBytes at EOF = %q, %v", got, err)
	}
	if got, err := ev.decodeBytes(0, 0); err != nil || got != nil {
		t.Fatalf("decodeBytes empty read = %q, %v", got, err)
	}
	if got := ev.EffectiveDisasmMode(); !viewer.DisasmModeValid(got) {
		t.Fatalf("EffectiveDisasmMode = %d", got)
	}
	if got := ev.CycleDisasmMode(); !viewer.DisasmModeValid(got) {
		t.Fatalf("CycleDisasmMode = %d", got)
	}
	if got := ev.DecodeStep(0); got <= 0 {
		t.Fatalf("DecodeStep(0) = %d, want instruction progress", got)
	}

	for _, tc := range []struct {
		current, old, replacement, want string
	}{
		{"One ONE", "one", "x", "x x"},
		{"unchanged", "", "x", "unchanged"},
		{"unchanged", "missing", "x", "unchanged"},
	} {
		if got := replaceAllFold(tc.current, tc.old, tc.replacement); got != tc.want {
			t.Fatalf("replaceAllFold(%q, %q) = %q, want %q", tc.current, tc.old, got, tc.want)
		}
	}

	if got := nextIndexPoll(0); got != indexPollMin || nextIndexPoll(indexPollMax) != indexPollMax {
		t.Fatalf("nextIndexPoll bounds are incorrect: %v, %v", got, nextIndexPoll(indexPollMax))
	}
	if got := nextIndexPoll(indexPollMin); got <= indexPollMin || got > indexPollMax {
		t.Fatalf("nextIndexPoll growth = %v", got)
	}

	if got := ev.bufferRange(-1, 1); got != nil || ev.bufferRange(ev.Pt.Size(), 1) != nil || ev.bufferRange(0, 0) != nil {
		t.Fatal("invalid bufferRange request returned data")
	}
	if got := string(ev.bufferRange(2, 100)); got != "cdef\nlast" {
		t.Fatalf("bufferRange truncated result = %q", got)
	}
	ev.CursorLine, ev.CursorPos = 1, 2
	if got := ev.searchSeedOffset(false, false); got != ev.Li.GetLineOffset(1)+2 {
		t.Fatalf("cursor search seed = %d", got)
	}
	ev.SelActive = true
	ev.SelAnchorOffset = 0
	ev.CursorLine, ev.CursorPos = 0, 3
	if got := ev.searchSeedOffset(false, true); got != 0 {
		t.Fatalf("forward selected search seed = %d", got)
	}
	if got := ev.searchSeedOffset(true, true); got != 3 {
		t.Fatalf("reverse selected search seed = %d", got)
	}

	if !editorAddCursorClick(&vtinput.InputEvent{KeyDown: true, ControlKeyState: vtinput.LeftAltPressed}) {
		t.Fatal("Alt click was not recognized")
	}
	if editorAddCursorClick(&vtinput.InputEvent{KeyDown: true, ControlKeyState: vtinput.LeftAltPressed, MouseEventFlags: vtinput.DoubleClick}) {
		t.Fatal("double Alt click was recognized as an extra cursor")
	}
	if !editorBlockMouseSelection(&vtinput.InputEvent{ControlKeyState: vtinput.LeftAltPressed | vtinput.ShiftPressed}) {
		t.Fatal("Alt+Shift mouse selection was not recognized")
	}
	if editorBlockMouseSelection(&vtinput.InputEvent{ControlKeyState: vtinput.LeftAltPressed}) {
		t.Fatal("plain Alt click blocked mouse selection")
	}
	if got := ev.getVisualColOf(0, 2); got < 0 {
		t.Fatalf("visual column = %d", got)
	}
	if plan := (&EditorView{}).highlightSlice(0); !plan.done || plan.lines != 0 {
		t.Fatalf("empty highlight plan = %#v", plan)
	}
}
