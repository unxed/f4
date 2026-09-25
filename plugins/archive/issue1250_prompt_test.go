package archive

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
)

// issue1250PromptStub answers password prompts with the given passwords in
// turn and counts them.
func issue1250PromptStub(t *testing.T, answers ...string) *int {
	t.Helper()
	prompts := 0
	prev := archivePasswordPrompt
	archivePasswordPrompt = func(context.Context, string) (string, error) {
		if prompts >= len(answers) {
			return "", errors.New("too many prompts")
		}
		prompts++
		return answers[prompts-1], nil
	}
	t.Cleanup(func() { archivePasswordPrompt = prev })
	return &prompts
}

// TestIssue1250_TestInsideArchiveReusesEnteredPassword: after entering a
// password-protected archive, testing it must not ask for the password again.
func TestIssue1250_TestInsideArchiveReusesEnteredPassword(t *testing.T) {
	prompts := issue1250PromptStub(t, "Correct")
	p := issue1250RAR4Fixture(t)
	v, err := NewArchiveVFS(vfs.NewOSVFS(t.TempDir()), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	if err := v.ReadDir(context.Background(), v.GetPath(), func([]vfs.VFSItem) {}); err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if *prompts != 1 || v.installedPassword() != "Correct" {
		t.Fatalf("entering: %d prompts, installed password %q", *prompts, v.installedPassword())
	}
	if err := testArchiveStartingWith(context.Background(), p, v.installedPassword(), &dummyReporter{}); err != nil {
		t.Fatalf("test: %v", err)
	}
	if *prompts != 1 {
		t.Fatalf("testing asked for the password again: %d prompts in total", *prompts)
	}
}

// issue1250PromptHoldProbe records, from inside the running test, whether the
// interactive prompt hold ends while the operation is still going. The
// progress screen waits for that hold, so a hold that outlives the prompt
// hides the progress of the whole operation.
type issue1250PromptHoldProbe struct {
	dummyReporter
	prompts  *int
	once     sync.Once
	released bool
	checked  bool
}

func (r *issue1250PromptHoldProbe) UpdateTransfer(string, string, int, string, int, string) {
	if *r.prompts == 0 {
		return
	}
	r.once.Do(func() {
		r.checked = true
		deadline := time.Now().Add(passwordRetryGrace + 2*time.Second)
		for time.Now().Before(deadline) {
			if !vfs.InteractivePromptPending() {
				r.released = true
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// TestIssue1250_ProgressAllowedAfterPassword: once the right password is in,
// the test runs with the progress screen allowed, not held back until the
// test is over.
func TestIssue1250_ProgressAllowedAfterPassword(t *testing.T) {
	for _, op := range []string{"test", "extract"} {
		t.Run(op, func(t *testing.T) {
			prompts := issue1250PromptStub(t, "Correct")
			p := issue1250RAR4Fixture(t)
			probe := &issue1250PromptHoldProbe{prompts: prompts}
			var err error
			if op == "test" {
				err = testArchiveWithPasswordPrompt(context.Background(), p, probe)
			} else {
				err = extractArchiveWithPasswordPrompt(context.Background(), p, t.TempDir(), probe)
			}
			if err != nil {
				t.Fatalf("%s: %v", op, err)
			}
			if !probe.checked {
				t.Fatalf("%s reported no progress after the password", op)
			}
			if !probe.released {
				t.Fatalf("%s: the prompt hold lasted into the operation, so its progress screen could not appear", op)
			}
		})
	}
}

func TestIssue1250_CancelledPromptReleasesHold(t *testing.T) {
	prev := archivePasswordPrompt
	archivePasswordPrompt = func(context.Context, string) (string, error) { return "", context.Canceled }
	t.Cleanup(func() { archivePasswordPrompt = prev })
	if _, err := promptArchivePasswordForRetry(context.Background(), "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	deadline := time.Now().Add(passwordRetryGrace + 2*time.Second)
	for time.Now().Before(deadline) && vfs.InteractivePromptPending() {
		 time.Sleep(10 * time.Millisecond)
	}
	if vfs.InteractivePromptPending() {
		 t.Fatal("a cancelled prompt left the interactive hold in place")
	}
}
