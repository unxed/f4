package vtvibe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// f4#1842, stage H9 item 5: the project's instructions for agents.

func TestProjectInstructionsFromTheFolderUpToTheRepository(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "pkg", "sub")
	for _, d := range []string{filepath.Join(repo, ".git"), sub} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for path, text := range map[string]string{
		filepath.Join(root, "AGENTS.md"):        "outside the repository",
		filepath.Join(repo, "AGENTS.md"):        "repo rules",
		filepath.Join(repo, "CLAUDE.md"):        "claude rules",
		filepath.Join(repo, "pkg", "AGENTS.md"): "  \n",
		filepath.Join(sub, "AGENTS.md"):         "sub rules",
	} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := ProjectInstructions(sub)
	if strings.Contains(got, "outside the repository") {
		t.Fatal("a file above the repository root was read")
	}
	repoAt, claudeAt, subAt := strings.Index(got, "repo rules"), strings.Index(got, "claude rules"), strings.Index(got, "sub rules")
	if repoAt < 0 || claudeAt < repoAt || subAt < claudeAt {
		t.Fatalf("not all files, or not the nearest last: %q", got)
	}
	if strings.Count(got, "===") != 6 {
		t.Fatalf("an empty file was included: %q", got)
	}
}

func TestProjectInstructionsOutsideARepositoryReadOnlyTheFolder(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "work")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("parent"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ProjectInstructions(sub); got != "" {
		t.Fatalf("read a parent folder outside any repository: %q", got)
	}
	if err := os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte("here"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ProjectInstructions(sub); !strings.Contains(got, "here") || strings.Contains(got, "parent") {
		t.Fatalf("%q", got)
	}
}

func TestWorkersReadTheProjectInstructions(t *testing.T) {
	srv, got := fakeAgentServer(t, chatReply("done"))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Run go vet before you report."), 0o600); err != nil {
		t.Fatal(err)
	}
	var w Workers
	done := make(chan WorkerResult, 1)
	w.Start("task", dir, func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} }, func() []Tool { return nil }, func(r WorkerResult) { done <- r })
	waitResult(t, done)
	if !strings.Contains(*(*got)[0].Messages[0].Content, "Run go vet before you report.") {
		t.Fatal("the worker did not get AGENTS.md")
	}
}
