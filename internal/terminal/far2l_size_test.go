package terminal

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/unxed/vtinput"
)

// The byte strings below are the ones far2l itself produces (VTExts.md, section
// 7): the terminal size event for 80 by 25 is "ESC _ f2lUAAZAFM= BEL".

// far2l decodes the Base64 of an event up to the first character outside the
// alphabet, so a colon after "f2l" turns the whole event into nothing, and it
// pops the height before the width.
func TestFar2lTerminalSizeEvent(t *testing.T) {
	e := newSixelEnv(t)
	e.tv.Width, e.tv.Height = 80, 25
	e.tv.SendFar2lTerminalSize()

	if got, want := e.Pty.String(), "\x1b_f2lUAAZAFM=\x07"; got != want {
		t.Fatalf("size event: got %q, want %q", got, want)
	}
}

// The reply to GET_WINDOW_MAXSIZE is read the same way: height on top.
func TestFar2lWindowMaxSizeReply(t *testing.T) {
	e := newSixelEnv(t)
	e.tv.Width, e.tv.Height = 80, 25

	stk := vtinput.Far2lStack{}
	stk.PushU8('w')
	stk.PushU8(1)
	e.p.Process([]byte("\x1b_far2l:" + base64.StdEncoding.EncodeToString(stk) + "\x07"))

	reply := far2lAwaitReply(t, e)
	_ = reply.PopU8() // the id
	height := reply.PopU16()
	width := reply.PopU16()
	if width != 80 || height != 25 {
		t.Errorf("GET_WINDOW_MAXSIZE: got %dx%d (width x height), want 80x25", width, height)
	}
}

func TestFar2lFKeyTitlesReplyFalseWhenNotDisplayed(t *testing.T) {
	e := newSixelEnv(t)
	stk := vtinput.Far2lStack{}
	stk.PushU8('f')
	for i := 0; i < 12; i++ {
		stk.PushU8(0) // no title for this function key
	}
	stk.PushU8(1) // request id
	e.p.Process([]byte("\x1b_far2l:" + base64.StdEncoding.EncodeToString(stk) + "\x07"))

	reply := far2lAwaitReply(t, e)
	if id := reply.PopU8(); id != 1 {
		t.Fatalf("request id: got %d, want 1", id)
	}
	if shown := reply.PopU8(); shown != 0 {
		t.Errorf("F-key titles reported as displayed (%d), want false", shown)
	}
}

// The title is on top of the stack, the text under it.
func TestFar2lNotificationOrder(t *testing.T) {
	var gotTitle, gotText string
	prev := far2lNotify
	far2lNotify = func(title, text string) { gotTitle, gotText = title, text }
	defer func() { far2lNotify = prev }()

	e := newSixelEnv(t)
	stk := vtinput.Far2lStack{}
	stk.PushString("the text")
	stk.PushString("the title")
	stk.PushU8('n')
	stk.PushU8(0)
	e.tv.ProcessFar2lInteract(stk)

	if gotTitle != "the title" || gotText != "the text" {
		t.Errorf("notification: title %q, text %q", gotTitle, gotText)
	}
}

// far2lAwaitReply returns the reply the handler wrote, decoded.
func far2lAwaitReply(t *testing.T, e *sixelEnv) *vtinput.Far2lStack {
	t.Helper()
	var out string
	for i := 0; i < 1500; i++ {
		if out = e.Pty.String(); strings.HasSuffix(out, "\x07") {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !strings.HasPrefix(out, "\x1b_far2l") || !strings.HasSuffix(out, "\x07") {
		t.Fatalf("no reply: %q", out)
	}
	raw, err := base64.StdEncoding.DecodeString(out[len("\x1b_far2l") : len(out)-1])
	if err != nil {
		t.Fatalf("reply is not base64: %q", out)
	}
	stk := vtinput.Far2lStack(raw)
	return &stk
}
