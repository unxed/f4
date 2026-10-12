package vtvibe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func sseServer(t *testing.T, events ...string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			_, _ = io.WriteString(w, e+"\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestChatStreamDeliversPiecesAndUsage(t *testing.T) {
	srv, _ := sseServer(t,
		`data: {"choices":[{"delta":{"role":"assistant","content":"Hel"}}]}`,
		`: keep-alive`,
		`data: {"choices":[{"delta":{"content":"lo"}}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2}}`,
		`data: [DONE]`)
	cfg := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	var pieces []string
	text, usage, err := cfg.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		func(p string) { pieces = append(pieces, p) })
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hello" || strings.Join(pieces, "|") != "Hel|lo" || usage.In != 7 || usage.Out != 2 {
		t.Fatalf("text %q pieces %q usage %#v", text, pieces, usage)
	}
}

func TestChatStreamFallsBackWhenStreamingIsRefused(t *testing.T) {
	var streamed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if req["stream"] == true {
			streamed.Store(true)
			http.Error(w, `{"error":{"message":"stream not supported"}}`, http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, finalReply)
	}))
	defer srv.Close()
	cfg := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	var got string
	text, _, err := cfg.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(p string) { got += p })
	if err != nil || text != "done: pong" || !streamed.Load() {
		t.Fatalf("text %q err %v streamed %v", text, err, streamed.Load())
	}
}

func TestChatStreamAcceptsAServerThatIgnoresStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, finalReply)
	}))
	defer srv.Close()
	cfg := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	var got string
	text, _, err := cfg.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(p string) { got += p })
	if err != nil || text != "done: pong" || got != "done: pong" {
		t.Fatalf("text %q delivered %q err %v", text, got, err)
	}
}

func TestChatStreamAnthropic(t *testing.T) {
	srv, _ := sseServer(t,
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-opus-5-5\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi \"}}",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"there\"}}",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":3}}",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}")
	cfg := Config{Kind: KindAnthropic, BaseURL: srv.URL, Model: "claude-opus-5-5", APIKey: "test-key"}
	var pieces []string
	text, usage, err := cfg.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		func(p string) { pieces = append(pieces, p) })
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hi there" || len(pieces) != 2 || usage.In != 5 || usage.Out != 3 {
		t.Fatalf("text %q pieces %q usage %#v", text, pieces, usage)
	}
}

func TestAskShowsTheAnswerWhileItStreams(t *testing.T) {
	srv, _ := sseServer(t,
		`data: {"choices":[{"delta":{"content":"one "}}]}`,
		`data: {"choices":[{"delta":{"content":"two"}}]}`,
		`data: [DONE]`)
	s := NewSession()
	var seen []string
	s.SetOnUpdate(func() { seen = append(seen, s.Pending()) })
	if err := s.Ask(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "count"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, "|") != "one |one two" {
		t.Fatalf("pending as it grew: %q", seen)
	}
	if s.Pending() != "" {
		t.Fatalf("pending left after the answer: %q", s.Pending())
	}
	turns := s.Turns()
	if last := turns[len(turns)-1]; last.Role != "assistant" || last.Text != "one two" {
		t.Fatalf("last turn = %#v", last)
	}
}
