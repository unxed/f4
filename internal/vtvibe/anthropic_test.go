package vtvibe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type seenRequest struct {
	path, key, beta string
	body            map[string]any
}

func fakeMessagesServer(t *testing.T, reply string) (*httptest.Server, *seenRequest) {
	t.Helper()
	seen := &seenRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.path, seen.key, seen.beta = r.URL.Path, r.Header.Get("X-Api-Key"), r.Header.Get("Anthropic-Beta")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &seen.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

const messagesReply = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",
"content":[{"type":"thinking","thinking":"","signature":"s"},{"type":"text","text":"Hello from Claude"}],
"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":4}}`

func TestChatAnthropicSpeaksTheMessagesAPI(t *testing.T) {
	srv, seen := fakeMessagesServer(t, messagesReply)
	cfg := Config{Kind: KindAnthropic, BaseURL: srv.URL, Model: "claude-opus-5-5", APIKey: "test-key"}
	text, usage, err := cfg.Chat(context.Background(), []Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "again"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hello from Claude" || usage.In != 12 || usage.Out != 4 {
		t.Fatalf("text %q usage %#v", text, usage)
	}
	if !strings.HasSuffix(seen.path, "/v1/messages") || seen.key != "test-key" {
		t.Fatalf("request went to %q with key %q", seen.path, seen.key)
	}
	if msgs, _ := seen.body["messages"].([]any); len(msgs) != 3 {
		t.Fatalf("system message not lifted out of messages: %v", seen.body["messages"])
	}
	if seen.body["system"] == nil || seen.body["model"] != "claude-opus-5-5" {
		t.Fatalf("system or model missing: %v", seen.body)
	}
	if seen.body["fallbacks"] != "default" || !strings.Contains(seen.beta, "server-side-fallback-2026-07-01") {
		t.Fatalf("refusal fallback not requested: fallbacks=%v beta=%q", seen.body["fallbacks"], seen.beta)
	}
}

func TestChatAnthropicOlderModelGetsNoFallback(t *testing.T) {
	srv, seen := fakeMessagesServer(t, messagesReply)
	cfg := Config{Kind: KindAnthropic, BaseURL: srv.URL, Model: "claude-haiku-4-5", APIKey: "k"}
	if _, _, err := cfg.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := seen.body["fallbacks"]; ok {
		t.Fatalf("fallbacks sent for a model that does not take them: %v", seen.body["fallbacks"])
	}
}

func TestChatAnthropicReportsARefusal(t *testing.T) {
	srv, _ := fakeMessagesServer(t, `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],
"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"not this"},"usage":{"input_tokens":1,"output_tokens":0}}`)
	cfg := Config{Kind: KindAnthropic, BaseURL: srv.URL, Model: "claude-opus-5-5", APIKey: "k"}
	_, _, err := cfg.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "declined") || !strings.Contains(err.Error(), "not this") {
		t.Fatalf("refusal = %v", err)
	}
}

func TestAnthropicNeedsKey(t *testing.T) {
	cfg := Config{Kind: KindAnthropic, BaseURL: "https://api.anthropic.com", Model: "claude-opus-5-5"}
	if _, _, err := cfg.Chat(context.Background(), nil); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key = %v", err)
	}
	if p := ProviderByID("anthropic"); p.Kind != KindAnthropic || p.KeyEnv[0] != "ANTHROPIC_API_KEY" {
		t.Fatalf("preset = %#v", p)
	}
}

// fakeMessagesSequence answers each Messages API request with the next reply
// and keeps the decoded bodies.
func fakeMessagesSequence(t *testing.T, replies ...string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) > len(replies) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"too many"}}`)
			return
		}
		_, _ = io.WriteString(w, replies[len(bodies)-1])
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func TestRunAgentAnthropicRunsToolUseUntilText(t *testing.T) {
	toolUse := `{"id":"m1","type":"message","role":"assistant","model":"claude-opus-5-5",
"content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"tool_use","id":"tu1","name":"echo","input":{"text":"ping"}}],
"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`
	srv, bodies := fakeMessagesSequence(t, toolUse, messagesReply)
	cfg := Config{Kind: KindAnthropic, BaseURL: srv.URL, Model: "claude-opus-5-5", APIKey: "test-key"}
	calls := 0
	text, usage, err := cfg.RunAgent(context.Background(),
		[]Message{{Role: "system", Content: "s"}, {Role: "user", Content: "go"}},
		[]Tool{echoTool(&calls)}, AgentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hello from Claude" || calls != 1 || usage.In != 22 || usage.Out != 9 {
		t.Fatalf("text %q calls %d usage %#v", text, calls, usage)
	}
	first := (*bodies)[0]
	tools, _ := first["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "echo" {
		t.Fatalf("tools not offered: %v", first["tools"])
	}
	msgs, _ := (*bodies)[1]["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("second request has %d messages, want user, assistant, tool results", len(msgs))
	}
	assistant := msgs[1].(map[string]any)
	content, _ := assistant["content"].([]any)
	if assistant["role"] != "assistant" || len(content) != 2 || content[0].(map[string]any)["type"] != "thinking" {
		t.Fatalf("the reply did not go back unchanged: %v", assistant)
	}
	results := msgs[2].(map[string]any)["content"].([]any)
	result := results[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "tu1" {
		t.Fatalf("tool result = %v", result)
	}
}
