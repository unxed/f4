package vtvibe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadInstructionFromFileAndURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.md")
	if err := os.WriteFile(path, []byte("  do the round  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadInstruction(context.Background(), path); err != nil || got != "do the round" {
		t.Fatalf("file: %q, %v", got, err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "from the web")
	}))
	defer srv.Close()
	if got, err := LoadInstruction(context.Background(), srv.URL+"/LUNOBOT.md"); err != nil || got != "from the web" {
		t.Fatalf("url: %q, %v", got, err)
	}
	if _, err := LoadInstruction(context.Background(), srv.URL+"/missing"); err == nil {
		t.Fatal("a 404 instruction was accepted")
	}
}

func TestBotSystemPromptNamesTheModel(t *testing.T) {
	got := BotSystemPrompt("grok-4.6", "/work", time.Unix(0, 0))
	if !strings.Contains(got, `"grok-4.6"`) || !strings.Contains(got, "/work") {
		t.Fatalf("prompt lacks model or folder: %s", got)
	}
}

func TestBotRunsRoundsInCleanDialogsAndStops(t *testing.T) {
	srv, _ := fakeAgentServer(t, finalReply, finalReply, finalReply)
	instruction := filepath.Join(t.TempDir(), "bot.md")
	if err := os.WriteFile(instruction, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	var b Bot
	b.SetStepped(false) // a round in one context
	rounds := make(chan BotRound, 4)
	cfg := func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} }
	if err := b.Start(instruction, 10*time.Millisecond, t.TempDir(), cfg, nil, nil, func(r BotRound) { rounds <- r }); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(instruction, time.Minute, ".", cfg, nil, nil, nil); err != ErrBotRunning {
		t.Fatalf("second start = %v", err)
	}
	for i := 1; i <= 2; i++ {
		select {
		case r := <-rounds:
			if r.N != i || r.Err != nil || r.Report != "done: pong" {
				t.Fatalf("round %d = %#v", i, r)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d did not come", i)
		}
	}
	if !b.Stop() || b.Status().Running {
		t.Fatal("bot did not stop")
	}
	if b.Stop() {
		t.Fatal("stopping a stopped bot reported a running one")
	}
}

func TestBotRoundsDoNotCarryHistory(t *testing.T) {
	srv, got := fakeAgentServer(t, finalReply, finalReply)
	instruction := filepath.Join(t.TempDir(), "bot.md")
	_ = os.WriteFile(instruction, []byte("report"), 0o600)
	var b Bot
	b.SetStepped(false) // a round in one context
	done := make(chan struct{}, 2)
	cfg := func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} }
	_ = b.Start(instruction, time.Millisecond, t.TempDir(), cfg, nil, nil, func(BotRound) { done <- struct{}{} })
	<-done
	<-done
	b.Stop()
	for i, req := range (*got)[:2] {
		if len(req.Messages) != 2 {
			t.Fatalf("round %d sent %d messages, want a clean system+instruction pair", i+1, len(req.Messages))
		}
	}
}
