//go:build !windows

package vfs

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/unxed/vtui"
)

// showPasswordDialog runs handleAskpassRequest on a pipe, runs the UI tasks it
// posts until its dialog is up, and returns the dialog and a channel that is
// closed when the handler has finished.
func showPasswordDialog(t *testing.T, c *SudoClient) (*vtui.Window, <-chan struct{}) {
	t.Helper()
	oldFM := vtui.FrameManager
	t.Cleanup(func() { vtui.FrameManager = oldFM })
	vtui.FrameManager = &vtui.FrameManagerType{}
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	server, client := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	before := vtui.FrameManager.GetTopFrame()
	finished := make(chan struct{})
	go func() {
		c.handleAskpassRequest(server)
		close(finished)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(10 * time.Millisecond):
		}
		if w, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window); ok && vtui.Frame(w) != before && w.OnResult != nil {
			return w, finished
		}
	}
	t.Fatal("the password dialog was not shown")
	return nil, nil
}

// An operation that was cancelled while it waited on the password dialog
// takes the dialog away, and that is not a refusal: the next operation may
// ask again at once (f4#1993).
func TestWithdrawPromptClosesTheDialogWithoutDeclining(t *testing.T) {
	c := &SudoClient{}
	_, finished := showPasswordDialog(t, c)

	go c.WithdrawPrompt()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-finished:
			if c.declines.Load() != 0 {
				t.Fatal("a withdrawn dialog must not silence the next ones")
			}
			c.promptMu.Lock()
			left := c.withdrawPrompt
			c.promptMu.Unlock()
			if left != nil {
				t.Fatal("the withdrawn dialog is still registered")
			}
			return
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("WithdrawPrompt did not release the request")
		}
	}
}

// The same dialog closed by the user is a refusal (f4#1998).
func TestClosingThePasswordDialogDeclines(t *testing.T) {
	c := &SudoClient{}
	dlg, finished := showPasswordDialog(t, c)

	dlg.OnResult(-1)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("closing the dialog did not release the request")
	}
	if c.declines.Load() == 0 {
		t.Fatal("a dialog closed by the user should count as a refusal")
	}
}

// With no dialog open there is nothing to withdraw, and no client is fine.
func TestWithdrawPromptWithoutDialogDoesNothing(t *testing.T) {
	(&SudoClient{}).WithdrawPrompt()
	var none *SudoClient
	none.WithdrawPrompt()
}

// Any elevated operation that waits on Connect must withdraw its prompt when
// the operation is cancelled, not only Open and ReadDir (f4#1993).
func TestElevatedCallWithdrawsPromptOnCancellation(t *testing.T) {
	c := &SudoClient{}
	old := globalSudoClient
	globalSudoClient = c
	t.Cleanup(func() { globalSudoClient = old })
	_, finished := showPasswordDialog(t, c)

	ctx, cancel := context.WithCancel(context.Background())
	wantErr := errors.New("prompt closed")
	result := make(chan error, 1)
	go func() {
		result <- elevatedCall(ctx, func() error {
			<-finished // Connect is blocked until its password prompt closes.
			return wantErr
		})
	}()
	cancel()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case err := <-result:
			if !errors.Is(err, wantErr) {
				t.Fatalf("elevated call error = %v, want %v", err, wantErr)
			}
			if c.declines.Load() != 0 {
				t.Fatal("withdrawing a cancelled operation must not count as refusal")
			}
			return
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("cancelling the operation did not withdraw its password prompt")
		}
	}
}
