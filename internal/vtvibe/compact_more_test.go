package vtvibe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// f4#1842, § 19a.3: code of an applied patch gives way to its commit, and
// attached files nobody mentioned are left out when shortening is not enough.

const patchID = "a1b2c3d4"

func TestAppliedPatchCodeBecomesItsCommit(t *testing.T) {
	notes := map[string]string{patchID: "[ap patch a1b2c3d4: applied to main.go, committed as abc1234]"}
	fenced := "Here is the fix:\n```\n" + patchID + " AP 3.2\n" + patchID + " FILE\nmain.go\n```\nOther code:\n```go\nx := 1\n```"
	got := shortenAnswer(fenced, notes)
	if !strings.Contains(got, "committed as abc1234") || strings.Contains(got, patchID+" FILE") || !strings.Contains(got, "[code block of 1 lines left out]") {
		t.Fatalf("fenced: %q", got)
	}
	bare := "Fix:\n" + patchID + " AP 3.2\n\n" + patchID + " FILE\nmain.go\n" + patchID + " REPLACE\nDone."
	got = shortenAnswer(bare, notes)
	if strings.Count(got, "committed as abc1234") != 1 || strings.Contains(got, patchID+" REPLACE") || !strings.Contains(got, "Done.") {
		t.Fatalf("bare: %q", got)
	}
	if got := shortenAnswer("no patch here", notes); got != "no patch here" {
		t.Fatalf("untouched: %q", got)
	}
}

func TestAppliedNotesSayWhetherCommitted(t *testing.T) {
	old := commitOf
	t.Cleanup(func() { commitOf = old })
	commitOf = func(_ context.Context, root string, files []string, _ time.Time) (string, bool) {
		return "abc1234", root == "/committed"
	}
	s := NewSession()
	s.NoteApplied(AppliedPatch{ID: "11111111", Root: "/committed", Files: []string{"a.go"}, At: time.Now()})
	s.NoteApplied(AppliedPatch{ID: "22222222", Root: "/dirty", Files: []string{"b.go", "c.go"}, At: time.Now()})
	notes := s.appliedNotes(context.Background())
	if !strings.Contains(notes["11111111"], "a.go, committed as abc1234") || !strings.Contains(notes["22222222"], "b.go, c.go, not committed yet") {
		t.Fatalf("notes %v", notes)
	}
}

func TestAppliedPatchesAreKeptWithTheDialog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dialog.json")
	first := NewSession()
	if err := first.SetStorePath(path); err != nil {
		t.Fatal(err)
	}
	first.NoteApplied(AppliedPatch{ID: patchID, Root: "/r", Files: []string{"x.go"}, At: time.Now()})
	second := NewSession()
	if err := second.SetStorePath(path); err != nil {
		t.Fatal(err)
	}
	if len(second.applied) != 1 || second.applied[0].ID != patchID {
		t.Fatalf("applied %+v", second.applied)
	}
}

func TestCommitOfFindsTheCommitWithGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) // #nosec G204 -- the test's own git commands
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	since := time.Now().Add(-time.Minute)
	if _, ok := commitOf(context.Background(), dir, []string{"a.go"}, since); ok {
		t.Fatal("a commit was found in an empty repository")
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "a.go")
	run("commit", "-q", "-m", "a")
	if hash, ok := commitOf(context.Background(), dir, []string{"a.go"}, since); !ok || len(hash) < 7 {
		t.Fatalf("commit %q, %v", hash, ok)
	}
}

func TestUnmentionedFilesAreLeftOutWhenShorteningIsNotEnough(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		mu.Unlock()
		if strings.Contains(string(data), "BIG-UNMENTIONED-CONTENT") {
			http.Error(w, `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatReply("fits now"))
	}))
	defer srv.Close()

	s := sessionWithFiles(t, map[string][]byte{
		"big.txt":  []byte(strings.Repeat("BIG-UNMENTIONED-CONTENT ", 10)),
		"notes.md": []byte("NOTES-CONTENT"),
	})
	s.appendTurn(Turn{Role: "user", Text: "an old question", Time: time.Now()})
	s.appendTurn(Turn{Role: "assistant", Text: "an old answer", Time: time.Now()})
	if err := s.Ask(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "what does notes.md say?"); err != nil {
		t.Fatal(err)
	}
	// As is and shortened fail (each also tried without streaming), then
	// the request without big.txt goes through.
	last := bodies[len(bodies)-1]
	if strings.Contains(bodies[0], "left out") {
		t.Fatal("the first request already left files out")
	}
	if !strings.Contains(last, "NOTES-CONTENT") || !strings.Contains(last, "left out") || !strings.Contains(last, "big.txt") {
		t.Fatalf("the last request: %s", last)
	}
	turns := s.Turns()
	if note := turns[len(turns)-1].Text; !strings.Contains(note, "big.txt") || strings.Contains(note, "notes.md") {
		t.Fatalf("the note in the dialog: %q", note)
	}
	// The files stay attached: only that request went without them.
	if len(s.ContextFiles()) != 2 {
		t.Fatalf("attached files now: %v", s.ContextFiles())
	}
}
