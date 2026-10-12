package vtvibe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// f4#1842 (Alexey5112, bothub): with a custom address and no model the
// request went out with an empty model and came back as MODEL_NOT_FOUND.
// It is not sent at all now: the error says no model is chosen.
func TestNoModelIsNotSent_Issue1842(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "unexpected", http.StatusTeapot)
	}))
	defer srv.Close()
	cfg := Config{BaseURL: srv.URL, APIKey: "k"}
	msgs := []Message{{Role: "user", Content: "hi"}}
	if _, _, err := cfg.Chat(context.Background(), msgs); !errors.Is(err, ErrNoModel) {
		t.Errorf("Chat err = %v, want ErrNoModel", err)
	}
	if _, _, err := cfg.ChatStream(context.Background(), msgs, func(string) {}); !errors.Is(err, ErrNoModel) {
		t.Errorf("ChatStream err = %v, want ErrNoModel", err)
	}
	if _, _, err := cfg.RunAgent(context.Background(), msgs, nil, AgentOptions{}); !errors.Is(err, ErrNoModel) {
		t.Errorf("RunAgent err = %v, want ErrNoModel", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d requests reached the service without a model", n)
	}
}

// bothub answers {"status":"error","error":{"code":"MODEL_NOT_FOUND","message":""}}:
// the code is what there is to show. Gemini's numeric code must not break
// reading its message.
func TestErrorCodeShownWhenMessageIsEmpty_Issue1842(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"status":"error","error":{"message":"","code":"MODEL_NOT_FOUND"}}`, "HTTP 404: MODEL_NOT_FOUND"},
		{`{"error":{"code":404,"message":"models/x is not found","status":"NOT_FOUND"}}`, "HTTP 404: models/x is not found"},
	} {
		err := &statusError{code: 404, body: []byte(tc.body)}
		if got := err.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
		if HTTPStatus(err) != 404 {
			t.Errorf("HTTPStatus = %d, want 404", HTTPStatus(err))
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","error":{"code":"UNAUTHORIZED","message":""}}`))
	}))
	defer srv.Close()
	_, err := Config{BaseURL: srv.URL, APIKey: "k"}.ModelsWithInfo(context.Background())
	if err == nil || !strings.Contains(err.Error(), "UNAUTHORIZED") {
		t.Errorf("models list err = %v, want the UNAUTHORIZED code", err)
	}
}
