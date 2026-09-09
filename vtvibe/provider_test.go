package vtvibe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestArtifactName(t *testing.T) {
	tests := []struct {
		info string
		want string
	}{
		{"go", ""},
		{"go:main.go", "main.go"},
		{"python: src/app.py ", "app.py"},
		{"bash: /etc/passwd", "passwd"},
		{"yaml:../secret.yml", "secret.yml"},
		{"ap:ai://out/x11_backend.ap", "x11_backend.ap"},
	}

	for _, tt := range tests {
		if got := artifactName(tt.info); got != tt.want {
			t.Errorf("artifactName(%q) = %q, want %q", tt.info, got, tt.want)
		}
	}
}

func TestIsLocal(t *testing.T) {
	for _, base := range []string{
		"http://localhost:11434/v1",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
		"http://0.0.0.0:8080",
		"HTTP://LOCALHOST:8080",
	} {
		if !isLocal(base) {
			t.Errorf("%q should be local", base)
		}
	}
	if isLocal("https://api.openai.com/v1") {
		t.Error("openai is not local")
	}
}

func TestConfigChat(t *testing.T) {
	var body chatRequest
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request = %s %s, want POST /v1/chat/completions", r.Method, r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":[{"type":"text","text":"first "},{"type":"text","text":"second"}]}}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`)
	}))
	defer server.Close()

	got, usage, err := (Config{BaseURL: server.URL + "/v1/", Model: "test-model", APIKey: "secret"}).Chat(
		context.Background(), []Message{{Role: "user", Content: "hello"}},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got != "first second" || usage != (Usage{In: 3, Out: 5}) {
		t.Fatalf("Chat() = %q, %#v, want %q, %#v", got, usage, "first second", Usage{In: 3, Out: 5})
	}
	if auth != "Bearer secret" {
		t.Errorf("Authorization = %q, want Bearer secret", auth)
	}
	if body.Model != "test-model" || len(body.Messages) != 1 || body.Messages[0].Content != "hello" {
		t.Errorf("request body = %#v", body)
	}
}

func TestChatErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		response string
		wantErr  string
	}{
		{name: "api error", response: `{"error":{"message":"denied"}}`, wantErr: "denied"},
		{name: "no choices", response: `{"choices":[]}`, wantErr: "the model returned no answer"},
		{name: "empty answer", response: `{"choices":[{"message":{"content":"  "}}]}`, wantErr: "the model returned an empty answer"},
		{name: "malformed response", response: "not json", wantErr: "cannot parse the reply"},
		{name: "http error", status: http.StatusBadRequest, response: `{"error":{"message":"bad request"}}`, wantErr: "HTTP 400: bad request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.status != 0 {
					w.WriteHeader(tt.status)
				}
				_, _ = io.WriteString(w, tt.response)
			}))
			defer server.Close()
			_, _, err := (Config{BaseURL: server.URL, Model: "test", APIKey: "key"}).Chat(context.Background(), nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Chat() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}

	_, _, err := (Config{BaseURL: "https://api.example.test", Model: "test"}).Chat(context.Background(), nil)
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("Chat() without key error = %v, want ErrNoKey", err)
	}
}

func TestConfigModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("request = %s %s, want GET /v1/models", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"models/gemini"},{"id":"plain"}]}`)
	}))
	defer server.Close()
	got, err := (Config{BaseURL: server.URL + "/v1/", APIKey: "key"}).Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	if want := []string{"gemini", "plain"}; !equalStrings(got, want) {
		t.Fatalf("Models() = %#v, want %#v", got, want)
	}

	_, err = (Config{BaseURL: "https://api.example.test"}).Models(context.Background())
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("Models() without key error = %v, want ErrNoKey", err)
	}
}

func TestModelsErrors(t *testing.T) {
	responses := []struct {
		body string
		want string
	}{
		{`{"error":{"message":"models unavailable"}}`, "models unavailable"},
		{"not json", "invalid character"},
	}
	for _, tt := range responses {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tt.body) }))
		_, err := (Config{BaseURL: server.URL, APIKey: "key"}).Models(context.Background())
		server.Close()
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Models() error = %v, want substring %q", err, tt.want)
		}
	}
}

func TestDoRetriesAndCancellation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"message":"try again"}}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (Config{}).do(ctx, http.MethodGet, server.URL, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("do() error = %v, want context.Canceled", err)
	}
	if calls.Load() != 0 {
		t.Errorf("do() made %d requests after cancellation, want 0", calls.Load())
	}
}

func TestDoRetriesThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	got, err := (Config{}).do(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil || string(got) != "ok" {
		t.Fatalf("do() = %q, %v, want ok, nil", got, err)
	}
	if calls.Load() != 2 {
		t.Errorf("do() made %d requests, want 2", calls.Load())
	}
}

func TestDoRejectsInvalidRequest(t *testing.T) {
	_, err := (Config{}).do(context.Background(), http.MethodGet, "://invalid", nil)
	if err == nil {
		t.Fatal("do() with invalid URL returned nil error")
	}
}

func TestStatusErrorAndBackoff(t *testing.T) {
	if got := (&statusError{code: http.StatusTeapot, body: []byte(`{"error":{"message":"short"}}`)}).Error(); got != "HTTP 418: short" {
		t.Errorf("statusError JSON = %q", got)
	}
	if got := (&statusError{code: http.StatusTeapot, body: []byte(" plain ")}).Error(); got != "HTTP 418: plain" {
		t.Errorf("statusError plain = %q", got)
	}
	long := strings.Repeat("x", 401)
	if got := (&statusError{code: http.StatusTeapot, body: []byte(long)}).Error(); len(got) != len("HTTP 418: ")+403 {
		t.Errorf("statusError long length = %d", len(got))
	}
	if got := (&statusError{code: http.StatusTeapot}).Error(); got != "HTTP 418" {
		t.Errorf("statusError empty = %q", got)
	}

	if got := backoff(1, nil); got != time.Second || backoff(2, nil) != 2*time.Second {
		t.Errorf("default backoff = %v, %v", got, backoff(2, nil))
	}
	if got := backoff(2, &statusError{retryAfter: "5"}); got != 5*time.Second {
		t.Errorf("Retry-After backoff = %v", got)
	}
	if got := backoff(2, &statusError{retryAfter: "60"}); got != 30*time.Second {
		t.Errorf("Retry-After cap = %v", got)
	}
	if got := backoff(2, &statusError{retryAfter: "bad"}); got != 2*time.Second {
		t.Errorf("invalid Retry-After backoff = %v", got)
	}
}

func TestDecodeContent(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{`"plain"`, "plain"},
		{`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`, "ab"},
		{"", ""},
		{"null", ""},
		{"not json", ""},
	}
	for _, tt := range tests {
		if got := decodeContent(json.RawMessage(tt.raw)); got != tt.want {
			t.Errorf("decodeContent(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
