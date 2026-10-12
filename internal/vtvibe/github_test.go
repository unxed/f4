package vtvibe

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// f4#1842, stage H6: the GitHub token reaches the commands, and only them.

func TestShellCommandsGetTheGitHubToken(t *testing.T) {
	command := `echo "$GH_TOKEN/$GITHUB_TOKEN"`
	if runtime.GOOS == "windows" {
		command = `echo %GH_TOKEN%/%GITHUB_TOKEN%`
	}
	args, _ := json.Marshal(map[string]string{"command": command})
	out, err := ShellTool(t.TempDir(), GitHubEnv(" tok123 ")...).Run(context.Background(), args)
	if err != nil || !strings.Contains(out, "tok123/tok123") {
		t.Fatalf("output %q, %v", out, err)
	}
	if GitHubEnv("  ") != nil {
		t.Fatal("an empty token overrides the environment's own")
	}
}

func TestGitHubTokenIsKeptWithTheDialogOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dialog.json")
	first := NewSession()
	if err := first.SetStorePath(path); err != nil {
		t.Fatal(err)
	}
	first.SetGitHubToken(" tok ")
	second := NewSession()
	if err := second.SetStorePath(path); err != nil {
		t.Fatal(err)
	}
	if second.GitHubToken() != "tok" {
		t.Fatalf("token %q", second.GitHubToken())
	}
	second.Reset(false)
	if second.GitHubToken() != "" {
		t.Fatal("a new dialog kept the old one's token")
	}
}

func TestTheGitHubTokenIsNotSentToTheModel(t *testing.T) {
	srv, got := fakeAgentServer(t, chatReply("ok"))
	s := NewSession()
	s.SetGitHubToken("secret-gh-token")
	if err := s.Ask(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k", ToolEnv: GitHubEnv("secret-gh-token")}, "hi"); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(*got)
	if strings.Contains(string(data), "secret-gh-token") {
		t.Fatal("the token went to the model")
	}
}
