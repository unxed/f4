package vtvibe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProviderChatAndModelsUseOpenAICompatibleEndpoints(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/v1/models" {
			if r.Method != http.MethodGet {
				t.Errorf("models method = %s, want GET", r.Method)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"models/alpha"},{"id":"beta"}]}`))
			return
		}
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Errorf("chat request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode chat request: %v", err)
		}
		if request.Model != "gemini-test" || len(request.Messages) != 1 || request.Messages[0].Content != "hello" {
			t.Errorf("chat request = %#v", request)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"answer"}}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`))
	}))
	defer server.Close()

	cfg := Config{BaseURL: server.URL + "/v1/", Model: "gemini-test", APIKey: "secret"}
	answer, usage, err := cfg.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}})
	if err != nil || answer != "answer" || usage != (Usage{In: 3, Out: 5}) {
		t.Fatalf("Chat = %q, %#v, %v", answer, usage, err)
	}
	models, err := cfg.Models(context.Background())
	if err != nil || strings.Join(models, ",") != "alpha,beta" {
		t.Fatalf("Models = %#v, %v", models, err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("Authorization = %q, want Bearer secret", gotAuth)
	}
}

func TestProviderRejectsMissingKeysButAllowsLocalEndpoints(t *testing.T) {
	remote := Config{BaseURL: "https://example.invalid/v1"}
	if _, _, err := remote.Chat(context.Background(), nil); !errors.Is(err, ErrNoKey) {
		t.Fatalf("remote Chat error = %v, want ErrNoKey", err)
	}
	if _, err := remote.Models(context.Background()); !errors.Is(err, ErrNoKey) {
		t.Fatalf("remote Models error = %v, want ErrNoKey", err)
	}
	for _, base := range []string{
		"http://127.0.0.1:8080",
		"http://localhost:11434",
		"http://[::1]:8080",
		"http://0.0.0.0:8080",
	} {
		if !isLocal(base) {
			t.Errorf("local endpoint %q was not recognized", base)
		}
	}
}

func TestProviderReportsChatAndModelPayloadErrors(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{"invalid JSON", "not json", "cannot parse the reply"},
		{"API error", `{"error":{"message":"bad model"}}`, "bad model"},
		{"no choices", `{"choices":[]}`, "no answer"},
		{"empty answer", `{"choices":[{"message":{"content":"  "}}]}`, "empty answer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			_, _, err := (Config{BaseURL: server.URL, APIKey: "key", Model: "m"}).Chat(context.Background(), nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Chat error = %v, want %q", err, tc.want)
			}
		})
	}

	modelTests := []struct {
		name     string
		response string
		want     string
	}{
		{"invalid JSON", "not json", "invalid character"},
		{"API error", `{"error":{"message":"models failed"}}`, "models failed"},
	}
	for _, tc := range modelTests {
		t.Run("Models "+tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			_, err := (Config{BaseURL: server.URL, APIKey: "key"}).Models(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Models error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestProviderDecodesContentAndFormatsHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{"empty", nil, ""},
		{"string", json.RawMessage(`"hello"`), "hello"},
		{"parts", json.RawMessage(`[{"type":"text","text":"one"},{"type":"text","text":"two"}]`), "onetwo"},
		{"invalid", json.RawMessage(`{"text":1}`), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := decodeContent(tc.raw); got != tc.want {
				t.Fatalf("decodeContent(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
	long := strings.Repeat("x", 401)
	for _, tc := range []struct {
		name, body, want string
		code             int
	}{
		{"json message", `{"error":{"message":"bad"}}`, "HTTP 400: bad", 400},
		{"plain message", " plain ", "HTTP 500: plain", 500},
		{"empty message", "", "HTTP 404", 404},
		{"truncated message", long, "HTTP 500: " + strings.Repeat("x", 400) + "...", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (&statusError{code: tc.code, body: []byte(tc.body)}).Error()
			if got != tc.want {
				t.Fatalf("statusError.Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProviderRetriesTransientFailuresAndHonorsCancellation(t *testing.T) {
	attempts := 0
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		cancel()
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("temporary"))
	}))
	defer server.Close()
	_, err := (Config{BaseURL: server.URL, APIKey: "key"}).get(ctx, server.URL)
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("cancelled retry = %v after %d attempt(s), want context.Canceled after 1", err, attempts)
	}

	for _, tc := range []struct {
		attempt int
		last    error
		want    time.Duration
	}{
		{1, nil, time.Second},
		{2, nil, 2 * time.Second},
		{3, &statusError{code: 429, retryAfter: "9"}, 9 * time.Second},
		{3, &statusError{code: 429, retryAfter: "90"}, 30 * time.Second},
		{3, &statusError{code: 429, retryAfter: "nope"}, 4 * time.Second},
	} {
		t.Run(fmt.Sprintf("backoff-%d", tc.attempt), func(t *testing.T) {
			if got := backoff(tc.attempt, tc.last); got != tc.want {
				t.Fatalf("backoff(%d, %v) = %s, want %s", tc.attempt, tc.last, got, tc.want)
			}
		})
	}
	if got := (Config{BaseURL: "https://host/"}).endpoint("/models"); got != "https://host/models" {
		t.Fatalf("endpoint = %q", got)
	}
	if got := strconv.Itoa(attempts); got != "1" {
		t.Fatalf("unexpected retry count %s", got)
	}
}
