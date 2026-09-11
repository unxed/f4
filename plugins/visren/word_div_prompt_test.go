package visren

import (
	"context"
	"testing"

	"github.com/unxed/f4/vfs"
)

// inputBoxHostStub records what the third argument of App.InputBox actually
// carries. Only InputBox is exercised; the rest satisfies the interface.
type inputBoxHostStub struct {
	defaultText string
	reply       string
}

func (*inputBoxHostStub) GetActivePanelVFS() vfs.VFS  { return nil }
func (*inputBoxHostStub) GetPassivePanelVFS() vfs.VFS { return nil }
func (*inputBoxHostStub) GetSelectedNames() []string  { return nil }
func (*inputBoxHostStub) GetSelectedName() string     { return "" }
func (*inputBoxHostStub) RefreshAll()                 {}
func (*inputBoxHostStub) SetPendingSelection(string)  {}
func (*inputBoxHostStub) RunProgressTask(string, string, bool,
	func(context.Context, func(string, int)) error, func(error)) {
}
func (*inputBoxHostStub) RunAdvancedProgressTask(string, bool,
	func(context.Context, vfs.TaskReporter) error, func(error)) {
}
func (*inputBoxHostStub) Message(string, string, []string) int { return 0 }
func (*inputBoxHostStub) Menu(string, []string, func(int))     {}

func (h *inputBoxHostStub) InputBox(_, _, defaultText string, callback func(string)) {
	h.defaultText = defaultText
	callback(h.reply)
}

func (*inputBoxHostStub) GetMarkedNames() []string             { return nil }
func (*inputBoxHostStub) ReplaceMarkedNames([]string)          {}
func (*inputBoxHostStub) OpenVisRenEditor(EditorRequest) error { return nil }

// The third argument of App.InputBox is the text the field opens with, not a
// history bucket name. Passing a bucket name there put the literal string
// "VisRenWordDiv" into the field, and since it is 13 runes and non-empty, a
// bare Enter stored it as the delimiter set.
func TestEditWordDiv_OpensOnCurrentDelimiters(t *testing.T) {
	host := &inputBoxHostStub{}
	d := &Dialog{host: host, wordDiv: "-. _&"}
	host.reply = d.wordDiv

	d.editWordDiv()

	if host.defaultText != "-. _&" {
		t.Errorf("prompt opened on %q, want the current delimiters %q", host.defaultText, "-. _&")
	}
	if d.wordDiv != "-. _&" {
		t.Errorf("accepting the prompt unchanged rewrote the delimiters to %q", d.wordDiv)
	}
}

func (*inputBoxHostStub) OpenSettings(string, string, string, bool) bool {
	panic("contextual delimiter prompt must not open Settings")
}
