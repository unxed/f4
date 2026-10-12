package vtvibe

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDialogToolsFollowTheSwitches(t *testing.T) {
	if got := DialogTools(DialogControls{}); len(got) != 0 {
		t.Fatalf("switched-off controls still offer %d tools", len(got))
	}
	var model, title string
	tools := DialogTools(DialogControls{
		SetModel: func(m string) error { model = m; return nil },
		Rename:   func(s string) error { title = s; return nil },
	})
	byName := map[string]Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	ctx := context.Background()
	if _, err := byName["set_model"].Run(ctx, json.RawMessage(`{"model":" gpt-5.5 "}`)); err != nil || model != "gpt-5.5" {
		t.Fatalf("set_model: %q, %v", model, err)
	}
	if _, err := byName["rename_dialog"].Run(ctx, json.RawMessage(`{"title":"Lunobot round"}`)); err != nil || title != "Lunobot round" {
		t.Fatalf("rename_dialog: %q, %v", title, err)
	}
	if _, err := byName["rename_dialog"].Run(ctx, json.RawMessage(`{"title":"two\nlines"}`)); err == nil {
		t.Fatal("a multi-line name was accepted")
	}
	if only := DialogTools(DialogControls{Rename: func(string) error { return nil }}); len(only) != 1 || only[0].Name != "rename_dialog" {
		t.Fatalf("model switching not left out: %#v", only)
	}
}

func TestChatPromptAlwaysNamesTheModel(t *testing.T) {
	srv, got := fakeAgentServer(t, finalReply)
	s := NewSession()
	cfg := Config{BaseURL: srv.URL, Model: "grok-4.6", APIKey: "k"}
	if err := s.Ask(context.Background(), cfg, "hi"); err != nil {
		t.Fatal(err)
	}
	system := (*got)[0].Messages[0]
	if system.Role != "system" || system.Content == nil || !strings.Contains(*system.Content, `"grok-4.6"`) {
		t.Fatalf("system prompt does not name the model: %v", system.Content)
	}
}

func TestSessionTitleIsKeptAndReset(t *testing.T) {
	s := NewSession()
	s.SetTitle("  Release notes  ")
	if s.Title() != "Release notes" {
		t.Fatalf("title = %q", s.Title())
	}
	s.Reset(false)
	if s.Title() != "" {
		t.Fatalf("a new dialog kept the old name %q", s.Title())
	}
}
