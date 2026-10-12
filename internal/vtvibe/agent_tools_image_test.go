package vtvibe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// f4#1842, item 11: view_image shows a worker or the bot a picture file.

const viewImageCall = `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"view_image","arguments":"{\"path\":\"shot.png\"}"}}]}}]}`

func sequenceServer(t *testing.T, refuse func(string) bool, replies ...string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	answered := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, string(data))
		if refuse != nil && refuse(string(data)) {
			http.Error(w, `{"error":{"message":"image input is not supported"}}`, http.StatusBadRequest)
			return
		}
		if answered >= len(replies) {
			http.Error(w, `{"error":{"message":"too many requests in test"}}`, http.StatusBadRequest)
			return
		}
		answered++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, replies[answered-1])
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

func pictureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), pngBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.png"), []byte("text, not a picture"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestViewImageShowsThePictureAfterTheResults(t *testing.T) {
	srv, bodies := sequenceServer(t, nil, viewImageCall, finalReply)
	cfg := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	text, _, err := cfg.RunAgent(context.Background(), []Message{{Role: "user", Content: "what is on shot.png?"}},
		[]Tool{ViewImageTool(pictureDir(t))}, AgentOptions{})
	if err != nil || text != "done: pong" {
		t.Fatalf("text %q, err %v", text, err)
	}
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	got := bodies()
	if err := json.Unmarshal([]byte(got[1]), &req); err != nil {
		t.Fatal(err)
	}
	n := len(req.Messages)
	if n != 4 || req.Messages[2].Role != "tool" || req.Messages[3].Role != "user" ||
		!strings.Contains(string(req.Messages[2].Content), "is shown to you after the tool results") ||
		!strings.Contains(string(req.Messages[3].Content), `"image_url"`) ||
		!strings.Contains(string(req.Messages[3].Content), "data:image/png;base64,") {
		t.Fatalf("second request: %s", got[1])
	}
}

func TestViewImageOnAModelWithoutPictures(t *testing.T) {
	srv, bodies := sequenceServer(t, func(body string) bool { return strings.Contains(body, "image_url") }, viewImageCall, finalReply)
	cfg := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	text, _, err := cfg.RunAgent(context.Background(), []Message{{Role: "user", Content: "look"}},
		[]Tool{ViewImageTool(pictureDir(t))}, AgentOptions{})
	if err != nil || text != "done: pong" {
		t.Fatalf("text %q, err %v", text, err)
	}
	got := bodies()
	if len(got) != 3 || strings.Contains(got[2], "image_url") || !strings.Contains(got[2], "did not accept pictures") {
		t.Fatalf("requests:\n%s", strings.Join(got, "\n"))
	}
}

func TestViewImageRefusesWhatIsNotAPicture(t *testing.T) {
	tool := ViewImageTool(pictureDir(t))
	ctx, sink := withImageSink(context.Background())
	if _, err := tool.Run(ctx, json.RawMessage(`{"path":"notes.png"}`)); err == nil || !strings.Contains(err.Error(), "not a PNG") {
		t.Fatalf("err %v", err)
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"shot.png"}`)); err == nil {
		t.Fatal("a picture was taken with nowhere to show it")
	}
	if imgs := sink.take(); len(imgs) != 0 {
		t.Fatalf("%d pictures collected", len(imgs))
	}
}

func TestRunAgentAnthropicShowsViewImagePictures(t *testing.T) {
	toolUse := `{"id":"m1","type":"message","role":"assistant","model":"claude-opus-5-5",
"content":[{"type":"tool_use","id":"tu1","name":"view_image","input":{"path":"shot.png"}}],
"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`
	srv, bodies := fakeMessagesSequence(t, toolUse, messagesReply)
	cfg := Config{Kind: KindAnthropic, BaseURL: srv.URL, Model: "claude-opus-5-5", APIKey: "k"}
	if _, _, err := cfg.RunAgent(context.Background(), []Message{{Role: "user", Content: "look"}},
		[]Tool{ViewImageTool(pictureDir(t))}, AgentOptions{}); err != nil {
		t.Fatal(err)
	}
	msgs := (*bodies)[1]["messages"].([]any)
	blocks := msgs[len(msgs)-1].(map[string]any)["content"].([]any)
	types := make([]string, len(blocks))
	for i, b := range blocks {
		types[i], _ = b.(map[string]any)["type"].(string)
	}
	if strings.Join(types, ",") != "tool_result,text,image" {
		t.Fatalf("blocks %v", types)
	}
}

func TestRunAgentAnthropicWithoutPictures(t *testing.T) {
	toolUse := `{"id":"m1","type":"message","role":"assistant","model":"claude-opus-5-5",
"content":[{"type":"tool_use","id":"tu1","name":"view_image","input":{"path":"shot.png"}}],
"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`
	srv, bodies := sequenceServer(t, func(body string) bool { return strings.Contains(body, `"media_type":"image/png"`) }, toolUse, messagesReply)
	cfg := Config{Kind: KindAnthropic, BaseURL: srv.URL, Model: "claude-opus-5-5", APIKey: "k"}
	text, _, err := cfg.RunAgent(context.Background(), []Message{{Role: "user", Content: "look"}},
		[]Tool{ViewImageTool(pictureDir(t))}, AgentOptions{})
	if err != nil || text != "Hello from Claude" {
		t.Fatalf("text %q, err %v", text, err)
	}
	got := bodies()
	if len(got) != 3 || !strings.Contains(got[2], "did not accept pictures") {
		t.Fatalf("requests:\n%s", strings.Join(got, "\n"))
	}
}
