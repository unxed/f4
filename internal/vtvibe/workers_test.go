package vtvibe

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitResult(t *testing.T, ch <-chan WorkerResult) WorkerResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not finish")
		return WorkerResult{}
	}
}

func TestWorkerDoesItsTaskInACleanDialog(t *testing.T) {
	srv, got := fakeAgentServer(t, toolCallReply, finalReply)
	var w Workers
	calls := 0
	done := make(chan WorkerResult, 1)
	cfg := func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} }
	id := w.Start("echo ping", t.TempDir(), cfg, func() []Tool { return []Tool{echoTool(&calls)} }, func(r WorkerResult) { done <- r })
	r := waitResult(t, done)
	if r.ID != id || r.Err != nil || r.Report != "done: pong" || len(r.Steps) != 1 || calls != 1 {
		t.Fatalf("result = %#v", r)
	}
	first := (*got)[0].Messages
	if len(first) != 2 || first[0].Role != "system" || *first[1].Content != "echo ping" {
		t.Fatalf("the worker did not start from a clean dialog: %#v", first)
	}
	if len(w.Running()) != 0 {
		t.Fatal("a finished worker is still listed")
	}
}

// Out of context: the task starts again, fresh, knowing what was done.
func TestWorkerStartsAgainWhenTheContextRunsOut(t *testing.T) {
	var mu sync.Mutex
	var bodies []agentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var req agentRequest
		_ = json.Unmarshal(data, &req)
		mu.Lock()
		bodies = append(bodies, req)
		n := len(bodies)
		mu.Unlock()
		switch n {
		case 1:
			_, _ = io.WriteString(w, toolCallReply)
		case 2:
			http.Error(w, `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`, http.StatusBadRequest)
		default:
			_, _ = io.WriteString(w, finalReply)
		}
	}))
	defer srv.Close()
	var w Workers
	calls := 0
	done := make(chan WorkerResult, 1)
	cfg := func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} }
	w.Start("long task", t.TempDir(), cfg, func() []Tool { return []Tool{echoTool(&calls)} }, func(r WorkerResult) { done <- r })
	r := waitResult(t, done)
	if r.Err != nil || r.Restarts != 1 || r.Report != "done: pong" {
		t.Fatalf("result = %#v", r)
	}
	mu.Lock()
	restart := bodies[2].Messages
	mu.Unlock()
	if len(restart) != 2 || !strings.Contains(*restart[0].Content, "ran out of context") || !strings.Contains(*restart[0].Content, "echo") {
		t.Fatalf("the restart is not clean or does not know what was done: %#v", restart)
	}
}

func TestWorkerRestartsAreBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"prompt is too long"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	var w Workers
	done := make(chan WorkerResult, 1)
	w.Start("t", t.TempDir(), func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} },
		func() []Tool { return nil }, func(r WorkerResult) { done <- r })
	r := waitResult(t, done)
	if r.Err == nil || r.Restarts != maxWorkerRestarts {
		t.Fatalf("result = %#v", r)
	}
}

func TestWorkerStopsWhenAsked(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	var w Workers
	done := make(chan WorkerResult, 1)
	id := w.Start("t", t.TempDir(), func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} },
		func() []Tool { return nil }, func(r WorkerResult) { done <- r })
	if len(w.Running()) != 1 || !w.Stop(id) {
		t.Fatal("the busy worker is not listed or not stopped")
	}
	if r := waitResult(t, done); r.Err == nil {
		t.Fatal("a stopped worker reported success")
	}
	if w.Stop(id) {
		t.Fatal("a finished worker was stopped again")
	}
}

func TestContextExhausted(t *testing.T) {
	for msg, want := range map[string]bool{
		"HTTP 400: This model's maximum context length is 128000 tokens": true,
		"prompt is too long: 210000 tokens > 200000 maximum":             true,
		"context_length_exceeded":                                        true,
		"HTTP 401: invalid key":                                          false,
	} {
		if got := contextExhausted(errors.New(msg)); got != want {
			t.Errorf("%q: %v", msg, got)
		}
	}
}
