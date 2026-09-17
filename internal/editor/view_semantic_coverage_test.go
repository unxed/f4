package editor

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/vtui"
)

func TestEditorSemanticModelAndActions(t *testing.T) {
	ev := NewEditorView(piecetable.New([]byte("one\ntwo\nthree")), nil, "semantic.txt")
	t.Cleanup(func() { ev.Close() })
	ev.SetPosition(0, 0, 79, 3)

	if got := ev.GetText(); got != "one\ntwo\nthree" {
		t.Fatalf("GetText = %q", got)
	}
	rows := ev.semanticRows()
	if len(rows) == 0 || rows[0].Text != "one" || rows[0].LogicalLine != 0 {
		t.Fatalf("semantic rows = %+v", rows)
	}

	surface := ev.SemanticNode(nil)
	if surface["kind"] != "editor" || surface["path"] != "semantic.txt" {
		t.Fatalf("semantic surface = %#v", surface)
	}

	ev.acEnabled = true
	ev.acMatches = []string{"prefixTail"}
	ev.acPrefix = "prefix"
	ev.acCurrentIdx = 0
	ac := ev.semanticAutocomplete()
	if ac["tail"] != "Tail" || ac["prefix"] != "prefix" {
		t.Fatalf("autocomplete = %#v", ac)
	}
	for _, tc := range []struct {
		name string
		set  func()
	}{
		{"disabled", func() { ev.acEnabled = false }},
		{"empty", func() { ev.acEnabled = true; ev.acMatches = nil }},
		{"bad-index", func() { ev.acMatches = []string{"prefixTail"}; ev.acCurrentIdx = 2 }},
		{"short-match", func() { ev.acCurrentIdx = 0; ev.acPrefix = "prefixTail" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.set()
			if got := ev.semanticAutocomplete(); got != nil {
				t.Fatalf("semanticAutocomplete = %#v, want nil", got)
			}
		})
	}

	if ev.HandleSemanticAction(map[string]any{"target": "other", "action": "editor.setText"}) {
		t.Fatal("action for another target was handled")
	}
	target := vtui.SemanticID(ev)
	if !ev.HandleSemanticAction(map[string]any{"target": target, "action": "editor.setText", "text": "changed"}) || ev.GetText() != "changed" {
		t.Fatalf("setText action failed: %q", ev.GetText())
	}
	if !ev.HandleSemanticAction(map[string]any{"target": target, "action": "editor.insertText", "text": "!"}) || !strings.HasSuffix(ev.GetText(), "!") {
		t.Fatalf("insertText action failed: %q", ev.GetText())
	}
	if ev.HandleSemanticAction(map[string]any{"target": target, "action": "unknown"}) {
		t.Fatal("unknown action was handled")
	}
}

func TestEditorSemanticRowsRejectIncompleteViews(t *testing.T) {
	if got := (&EditorView{}).semanticRows(); got != nil {
		t.Fatalf("nil editor rows = %#v", got)
	}
	ev := NewEditorView(piecetable.New([]byte("text")), nil, "")
	t.Cleanup(func() { ev.Close() })
	ev.SetPosition(0, 0, 10, 0)
	if got := ev.semanticRows(); got != nil {
		t.Fatalf("zero-height rows = %#v", got)
	}
}
