package archive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
)

type archiveTestMessages struct {
	vfs.App
	messages chan archiveTestMessage
}

type archiveTestMessage struct {
	title, text string
	buttons     []string
}

func (app *archiveTestMessages) Message(title, text string, buttons []string) int {
	app.messages <- archiveTestMessage{title, text, buttons}
	return 1
}

func finishedArchiveTest(t *testing.T, err error) archiveTestMessage {
	t.Helper()
	app := &archiveTestMessages{messages: make(chan archiveTestMessage, 1)}
	finishArchiveTest(app, "/tmp/some/tests.7z", err)
	select {
	case msg := <-app.messages:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no message was shown")
	}
	return archiveTestMessage{}
}

// A test the user interrupted is announced, not reported as a failure with the
// text of the cancellation in it (#1250).
func TestFinishArchiveTestInterruptedByUserIsNotAFailure(t *testing.T) {
	member := fmt.Errorf("dir/file.bin: %w", context.Canceled)
	for name, err := range map[string]error{
		"plain":   context.Canceled,
		"wrapped": fmt.Errorf("f4.exe: %w", context.Canceled),
		"joined":  errors.Join(member, context.Canceled),
	} {
		t.Run(name, func(t *testing.T) {
			msg := finishedArchiveTest(t, err)
			if msg.text != "Test for tests.7z has been interrupted by user." {
				t.Fatalf("text = %q", msg.text)
			}
			if len(msg.buttons) != 1 || !strings.Contains(msg.buttons[0], "Ok") {
				t.Fatalf("buttons = %q, want the single Ok", msg.buttons)
			}
			if strings.Contains(msg.text, "context canceled") || strings.Contains(msg.text, "failed") {
				t.Fatalf("the interruption reads as a failure: %q", msg.text)
			}
		})
	}
}

func TestFinishArchiveTestStillReportsRealFailuresAndSuccess(t *testing.T) {
	if msg := finishedArchiveTest(t, nil); !strings.Contains(msg.text, "No errors found.") {
		t.Fatalf("success text = %q", msg.text)
	}
	msg := finishedArchiveTest(t, errors.New("a.bin: unexpected EOF"))
	if !strings.Contains(msg.text, "Test failed for tests.7z") || !strings.Contains(msg.text, "unexpected EOF") {
		t.Fatalf("failure text = %q", msg.text)
	}
	if len(msg.buttons) != 2 {
		t.Fatalf("failure buttons = %q, want the two of the report", msg.buttons)
	}
}
