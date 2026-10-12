package vtvibe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAgentServer answers each request with the next reply in order and
// records what it was sent.
func fakeAgentServer(t *testing.T, replies ...string) (*httptest.Server, *[]agentRequest) {
	t.Helper()
	var got []agentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req agentRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		got = append(got, req)
		if len(got) > len(replies) {
			http.Error(w, `{"error":{"message":"too many requests in test"}}`, http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, replies[len(got)-1])
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

const toolCallReply = `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"ping\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`
const finalReply = `{"choices":[{"message":{"content":"done: pong"}}],"usage":{"prompt_tokens":20,"completion_tokens":3}}`

func echoTool(calls *int) Tool {
	return Tool{
		Name:       "echo",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			*calls++
			var a struct{ Text string }
			_ = json.Unmarshal(raw, &a)
			return strings.Replace(a.Text, "i", "o", 1), nil
		},
	}
}

func TestRunAgentRunsToolsUntilTextAnswer(t *testing.T) {
	srv, got := fakeAgentServer(t, toolCallReply, finalReply)
	cfg := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	calls := 0
	var steps []AgentStep
	text, usage, err := cfg.RunAgent(context.Background(), []Message{{Role: "user", Content: "go"}},
		[]Tool{echoTool(&calls)}, AgentOptions{OnStep: func(s AgentStep) { steps = append(steps, s) }})
	if err != nil {
		t.Fatal(err)
	}
	if text != "done: pong" || calls != 1 || len(steps) != 1 || steps[0].Result != "pong" {
		t.Fatalf("text %q, calls %d, steps %#v", text, calls, steps)
	}
	if usage.In != 30 || usage.Out != 5 {
		t.Fatalf("usage not summed: %#v", usage)
	}
	if len((*got)[0].Tools) != 1 || (*got)[0].Tools[0].Function.Name != "echo" {
		t.Fatalf("tools not offered: %#v", (*got)[0].Tools)
	}
	second := (*got)[1].Messages
	last := second[len(second)-1]
	if last.Role != "tool" || last.ToolCallID != "c1" || last.Content == nil || *last.Content != "pong" {
		t.Fatalf("tool result not handed back: %#v", last)
	}
	if second[len(second)-2].Role != "assistant" || len(second[len(second)-2].ToolCalls) != 1 {
		t.Fatalf("assistant tool call not kept in history: %#v", second[len(second)-2])
	}
}

func TestRunAgentReportsUnknownToolToTheModel(t *testing.T) {
	srv, got := fakeAgentServer(t, toolCallReply, finalReply)
	cfg := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	if _, _, err := cfg.RunAgent(context.Background(), []Message{{Role: "user", Content: "go"}}, nil, AgentOptions{}); err != nil {
		t.Fatal(err)
	}
	msgs := (*got)[1].Messages
	if c := msgs[len(msgs)-1].Content; c == nil || !strings.Contains(*c, `unknown tool "echo"`) {
		t.Fatalf("unknown tool not reported: %v", c)
	}
}

func TestRunAgentStopsAtStepLimit(t *testing.T) {
	srv, _ := fakeAgentServer(t, toolCallReply, toolCallReply, toolCallReply)
	cfg := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	calls := 0
	_, _, err := cfg.RunAgent(context.Background(), []Message{{Role: "user", Content: "go"}},
		[]Tool{echoTool(&calls)}, AgentOptions{MaxSteps: 2})
	if !errors.Is(err, ErrAgentSteps) || calls != 2 {
		t.Fatalf("err %v after %d calls", err, calls)
	}
}

func TestTailBytesKeepsTheEnd(t *testing.T) {
	s := strings.Repeat("a\n", 100) + "last line"
	got := tailBytes(s, 20)
	if !strings.HasSuffix(got, "last line") || !strings.Contains(got, "omitted") {
		t.Fatalf("tail = %q", got)
	}
	if tailBytes("short", 20) != "short" {
		t.Fatal("short output changed")
	}
}

func TestWorkToolsShellReadWrite(t *testing.T) {
	dir := t.TempDir()
	tools := map[string]Tool{}
	for _, tool := range WorkTools(dir) {
		tools[tool.Name] = tool
	}
	ctx := context.Background()
	if out, err := tools["write_file"].Run(ctx, json.RawMessage(`{"path":"sub/a.txt","content":"hello"}`)); err != nil {
		t.Fatalf("write: %v %s", err, out)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "sub", "a.txt")); err != nil || string(data) != "hello" {
		t.Fatalf("written %q, %v", data, err)
	}
	if out, err := tools["read_file"].Run(ctx, json.RawMessage(`{"path":"sub/a.txt"}`)); err != nil || out != "hello" {
		t.Fatalf("read %q, %v", out, err)
	}
	out, err := tools["shell"].Run(ctx, json.RawMessage(`{"command":"echo hi"}`))
	if err != nil || !strings.Contains(out, "hi") || !strings.Contains(out, "[exit code 0]") {
		t.Fatalf("shell %q, %v", out, err)
	}
	out, err = tools["shell"].Run(ctx, json.RawMessage(`{"command":"exit 3"}`))
	if err != nil || !strings.Contains(out, "[exit code 3]") {
		t.Fatalf("failing command %q, %v", out, err)
	}
}
