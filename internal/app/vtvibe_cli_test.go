package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/unxed/f4/internal/vtvibe"
)

// f4#1842, H9 item 12: f4 --ai answers without the UI.

func aiCLIServer(t *testing.T, answer string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		mu.Unlock()
		reply, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": answer}}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

func TestAICLIPrintsTheAnswer(t *testing.T) {
	srv, bodies := aiCLIServer(t, "it adds a test")
	file := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(file, []byte("NOTES-CONTENT"), 0600); err != nil {
		t.Fatal(err)
	}
	config := func() (vtvibe.Config, string, vtvibe.Provider) {
		return vtvibe.Config{BaseURL: srv.URL, Model: "from-settings", APIKey: "k"}, "TEST_KEY", vtvibe.Provider{}
	}
	var stdout, stderr bytes.Buffer
	code, handled := runAICLI([]string{"--ai", "review this", "--ai-file=" + file, "--ai-model", "other-model"},
		strings.NewReader("DIFF-CONTENT"), &stdout, &stderr, config)
	if !handled || code != 0 || stdout.String() != "it adds a test\n" || stderr.Len() != 0 {
		t.Fatalf("code %d, handled %v, stdout %q, stderr %q", code, handled, stdout.String(), stderr.String())
	}
	got := bodies()
	if len(got) != 1 {
		t.Fatalf("requests %d", len(got))
	}
	for _, want := range []string{"review this", "DIFF-CONTENT", "stdin.txt", "NOTES-CONTENT", "notes.md", `"model":"other-model"`} {
		if !strings.Contains(got[0], want) {
			t.Errorf("the request lacks %q: %s", want, got[0])
		}
	}
}

func TestAICLIIsNotTakenWithoutAI(t *testing.T) {
	if _, handled := runAICLI([]string{"/tmp", "--tty"}, nil, io.Discard, io.Discard, nil); handled {
		t.Fatal("a normal start was taken for f4 --ai")
	}
}

func TestAICLIUsageAndNoKey(t *testing.T) {
	for _, args := range [][]string{{"--ai"}, {"--ai="}, {"--ai-model", "m"}} {
		var stderr bytes.Buffer
		if code, handled := runAICLI(args, nil, io.Discard, &stderr, nil); !handled || code != 2 || !strings.Contains(stderr.String(), "usage") {
			t.Errorf("%q: code %d, handled %v, stderr %q", args, code, handled, stderr.String())
		}
	}
	noKey := func() (vtvibe.Config, string, vtvibe.Provider) {
		return vtvibe.Config{BaseURL: "https://example.invalid/v1", Model: "m"}, "", vtvibe.Provider{}
	}
	var stderr bytes.Buffer
	if code, _ := runAICLI([]string{"--ai", "hi"}, nil, io.Discard, &stderr, noKey); code != 1 || !strings.Contains(stderr.String(), "no API key") {
		t.Fatalf("code %d, stderr %q", code, stderr.String())
	}
}

func TestAIBotCLIRunsOneRound(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	instruction := filepath.Join(dir, "bot.md")
	if err := os.WriteFile(instruction, []byte("Write done.txt with the word ok."), 0600); err != nil {
		t.Fatal(err)
	}
	call := `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"done.txt\",\"content\":\"ok\"}"}}]}}]}`
	final := `{"choices":[{"message":{"content":"wrote done.txt"}}]}`
	replies := []string{call, final}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if len(replies) == 0 {
			http.Error(w, `{"error":{"message":"too many requests in test"}}`, http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, replies[0])
		replies = replies[1:]
	}))
	t.Cleanup(srv.Close)
	config := func() (vtvibe.Config, string, vtvibe.Provider) {
		return vtvibe.Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "TEST_KEY", vtvibe.Provider{}
	}
	var stdout, stderr bytes.Buffer
	code, handled := runAICLI([]string{"--ai-bot", instruction, "--ai-yes", "--ai-whole"}, nil, &stdout, &stderr, config)
	if !handled || code != 0 || stdout.String() != "wrote done.txt\n" || !strings.Contains(stderr.String(), "f4 --ai-bot: write_file") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if data, err := os.ReadFile(filepath.Join(dir, "done.txt")); err != nil || string(data) != "ok" {
		t.Fatalf("done.txt: %q, %v", data, err)
	}
}

func TestAIBotCLIWantsConsent(t *testing.T) {
	var stderr bytes.Buffer
	config := func() (vtvibe.Config, string, vtvibe.Provider) { return vtvibe.Config{}, "", vtvibe.Provider{} }
	if code, handled := runAICLI([]string{"--ai-bot", "bot.md"}, nil, io.Discard, &stderr, config); !handled || code != 2 || !strings.Contains(stderr.String(), "--ai-yes") {
		t.Fatalf("code %d, stderr %q", code, stderr.String())
	}
	for _, args := range [][]string{{"--ai", "q", "--ai-bot", "b.md"}, {"--ai", "q", "--ai-yes"}, {"--ai-bot", "b.md", "--ai-file", "x"}, {"--ai-yes"}} {
		if code, _ := runAICLI(args, nil, io.Discard, io.Discard, config); code != 2 {
			t.Errorf("%q: code %d", args, code)
		}
	}
}

func TestAIBotCLIStartsTheMCPServers(t *testing.T) {
	setupPortableIni(t, "0")
	if err := os.MkdirAll(filepath.Dir(vtvibeMCPPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vtvibeMCPPath(), []byte(`{"mcpServers":{"broken":{"command":"f4-no-such-mcp-server"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	instruction := filepath.Join(dir, "bot.md")
	if err := os.WriteFile(instruction, []byte("Say done."), 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"}}]}`)
	}))
	t.Cleanup(srv.Close)
	config := func() (vtvibe.Config, string, vtvibe.Provider) {
		return vtvibe.Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "TEST_KEY", vtvibe.Provider{}
	}
	var stdout, stderr bytes.Buffer
	code, _ := runAICLI([]string{"--ai-bot", instruction, "--ai-yes", "--ai-whole"}, nil, &stdout, &stderr, config)
	if code != 0 || stdout.String() != "done\n" || !strings.Contains(stderr.String(), "broken") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}
