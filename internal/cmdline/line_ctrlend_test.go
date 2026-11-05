package cmdline

import (
	"testing"

	"github.com/unxed/vtinput"
)

func ctrlEnd() *vtinput.InputEvent { return ctrlKey(vtinput.VK_END) }

// K-79 (docs/FAR2L_COMPAT.md; unxed/f4#2139): Ctrl+End at the end of the line
// completes it from the history, newest first; again: the next candidate.
func TestCommandLine_CtrlEndCompletesFromHistory(t *testing.T) {
	cl := farKeysLine("git ")
	cl.Edit.History = []string{"git push", "ls", "git pull", "git "}
	for i, want := range []string{"git push", "git pull", "git "} {
		if !cl.ProcessKey(ctrlEnd()) || cl.Edit.GetText() != want {
			t.Fatalf("press %d: text %q, want %q", i+1, cl.Edit.GetText(), want)
		}
		if cl.Edit.CursorPos() != len([]rune(want)) {
			t.Fatalf("press %d: cursor at %d, want the end of %q", i+1, cl.Edit.CursorPos(), want)
		}
	}
	if cl.Edit.GetText() != "git " {
		t.Fatalf("after the last candidate: text %q, want the typed %q", cl.Edit.GetText(), "git ")
	}
}

func TestCommandLine_CtrlEndWalkRestartsFromTheTypedText(t *testing.T) {
	cl := farKeysLine("g")
	cl.Edit.History = []string{"git push", "go test"}
	cl.ProcessKey(ctrlEnd())
	cl.ProcessKey(ctrlEnd())
	if cl.Edit.GetText() != "go test" {
		t.Fatalf("second press: text %q, want %q", cl.Edit.GetText(), "go test")
	}
	cl.ProcessKey(ctrlEnd())
	if cl.Edit.GetText() != "g" {
		t.Fatalf("third press: text %q, want the typed %q", cl.Edit.GetText(), "g")
	}
	cl.ProcessKey(ctrlEnd())
	if cl.Edit.GetText() != "git push" {
		t.Fatalf("fourth press: text %q, want a new walk from %q", cl.Edit.GetText(), "git push")
	}
}

func TestCommandLine_CtrlEndEditedLineStartsANewWalk(t *testing.T) {
	cl := farKeysLine("git")
	cl.Edit.History = []string{"git push", "git pull", "ls"}
	cl.ProcessKey(ctrlEnd())
	cl.Edit.SetText("l")
	cl.Edit.SetCursorPos(1)
	cl.ProcessKey(ctrlEnd())
	if cl.Edit.GetText() != "ls" {
		t.Fatalf("after editing: text %q, want %q", cl.Edit.GetText(), "ls")
	}
}

func TestCommandLine_CtrlEndInTheMiddleOnlyMovesTheCursor(t *testing.T) {
	cl := farKeysLine("git ")
	cl.Edit.History = []string{"git push"}
	cl.Edit.SetCursorPos(1)
	cl.ProcessKey(ctrlEnd())
	if cl.Edit.GetText() != "git " || cl.Edit.CursorPos() != 4 {
		t.Fatalf("text %q, cursor %d; want the text kept and the cursor at the end", cl.Edit.GetText(), cl.Edit.CursorPos())
	}
}

func TestCommandLine_CtrlEndWithoutCandidatesOrText(t *testing.T) {
	cl := farKeysLine("zzz")
	cl.Edit.History = []string{"git push"}
	cl.ProcessKey(ctrlEnd())
	if cl.Edit.GetText() != "zzz" {
		t.Fatalf("no candidate: text %q, want %q", cl.Edit.GetText(), "zzz")
	}
	cl = farKeysLine("")
	cl.Edit.History = []string{"git push"}
	cl.ProcessKey(ctrlEnd())
	if cl.Edit.GetText() != "" {
		t.Fatalf("empty line: text %q, want it left empty", cl.Edit.GetText())
	}
}
