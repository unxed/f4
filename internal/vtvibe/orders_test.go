package vtvibe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// f4#1842, stage H4: every message of the user is an order the model keeps
// seeing until it is done.
func TestOrdersAreRegisteredAndShownToTheModel(t *testing.T) {
	srv, got := fakeAgentServer(t, finalReply, finalReply)
	cfg := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	s := NewSession()
	if err := s.Ask(context.Background(), cfg, "write the release notes"); err != nil {
		t.Fatal(err)
	}
	if err := s.Ask(context.Background(), cfg, "and bump the version\nin go.mod too"); err != nil {
		t.Fatal(err)
	}
	orders := s.Orders()
	if len(orders) != 2 || orders[0].ID != 1 || orders[1].ID != 2 || orders[1].Text != "and bump the version\nin go.mod too" {
		t.Fatalf("orders = %#v", orders)
	}
	system := *(*got)[1].Messages[0].Content
	if !strings.Contains(system, "#1: write the release notes") || !strings.Contains(system, "#2: and bump the version") {
		t.Fatalf("open orders not in the prompt:\n%s", system)
	}

	if err := s.SetOrderDone(1, true); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	prompt := s.ordersPromptLocked("")
	s.mu.Unlock()
	if strings.Contains(prompt, "#1:") || !strings.Contains(prompt, "#2:") {
		t.Fatalf("a done order is still shown as open:\n%s", prompt)
	}
	if err := s.SetOrderDone(7, true); err == nil {
		t.Fatal("an unknown order was closed")
	}
}

func TestAFailedRequestEntersNoOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	s := NewSession()
	if err := s.Ask(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "try"); err == nil {
		t.Fatal("the request did not fail")
	}
	if len(s.Orders()) != 0 {
		t.Fatalf("a failed request was entered: %#v", s.Orders())
	}
}

func TestOrdersAreSavedWithTheDialog(t *testing.T) {
	srv, _ := fakeAgentServer(t, finalReply)
	path := filepath.Join(t.TempDir(), "dialog.json")
	s := NewSession()
	_ = s.SetStorePath(path)
	_ = s.Ask(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "keep me")
	_ = s.SetOrderDone(1, true)
	again := NewSession()
	_ = again.SetStorePath(path)
	if o := again.Orders(); len(o) != 1 || !o[0].Done || o[0].Text != "keep me" {
		t.Fatalf("restored orders = %#v", o)
	}
	again.Reset(true)
	if len(again.Orders()) != 0 {
		t.Fatal("a new dialog kept the old orders")
	}
}

func TestTheModelClosesOrdersWithItsLastLine(t *testing.T) {
	s := NewSession()
	s.mu.Lock()
	s.addOrderLocked("first")
	s.addOrderLocked("second")
	s.addOrderLocked("third")
	s.orders[2].Done = true
	got := s.closeOrdersFromReplyLocked("Here is the work.\n\nORDERS DONE: #1, #3, #9\n")
	s.mu.Unlock()
	if got != "Here is the work.\n\n✓ #1" {
		t.Fatalf("reply shown as %q", got)
	}
	o := s.Orders()
	if !o[0].Done || o[1].Done {
		t.Fatalf("orders after the reply: %#v", o)
	}

	s.mu.Lock()
	plain := s.closeOrdersFromReplyLocked("No marker here.\nORDERS DONE is mentioned mid-text.")
	only := s.closeOrdersFromReplyLocked("ORDERS DONE: 2")
	s.mu.Unlock()
	if plain != "No marker here.\nORDERS DONE is mentioned mid-text." || only != "✓ #2" {
		t.Fatalf("plain %q, only %q", plain, only)
	}
	if !s.Orders()[1].Done {
		t.Fatal("a bare number on the marker line did not close the order")
	}
}

func TestOrdersPromptExplainsTheMarker(t *testing.T) {
	s := NewSession()
	s.mu.Lock()
	prompt := s.ordersPromptLocked("do it")
	s.mu.Unlock()
	if !strings.Contains(prompt, ordersDoneMarker) {
		t.Fatalf("the prompt does not tell the model how to close orders:\n%s", prompt)
	}
}
