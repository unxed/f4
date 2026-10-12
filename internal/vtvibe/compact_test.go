package vtvibe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// f4#1842, stage H4 / H9 item 10: a dialog too long for the model goes
// again with the model's own earlier answers shortened, never the user's.

func TestTooLongDialogIsSentAgainWithTheModelsAnswersShortened(t *testing.T) {
	var mu sync.Mutex
	var bodies []agentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var req agentRequest
		_ = json.Unmarshal(data, &req)
		mu.Lock()
		bodies = append(bodies, req)
		mu.Unlock()
		full := 0
		for _, m := range req.Messages {
			if m.Role == "assistant" && m.Content != nil && strings.Contains(*m.Content, "```go") {
				full++
			}
		}
		if full > keepFullAnswers { // too long until the older answers are shortened
			http.Error(w, `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`, http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, chatReply("short answer"))
	}))
	defer srv.Close()

	s := NewSession()
	userWords := strings.Repeat("the user's own words ", 200)
	code := "```go\n" + strings.Repeat("x := 1\n", 50) + "```"
	for i := 0; i < 4; i++ {
		s.appendTurn(Turn{Role: "user", Text: userWords, Time: time.Now()})
		s.appendTurn(Turn{Role: "assistant", Text: "answer with code\n" + code + "\n" + strings.Repeat("long text ", 300), Time: time.Now()})
	}
	if err := s.Ask(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "next question"); err != nil {
		t.Fatal(err)
	}
	again := bodies[len(bodies)-1].Messages
	var answers []string
	for _, m := range again {
		switch m.Role {
		case "user":
			if *m.Content != userWords && *m.Content != "next question" {
				t.Fatal("a message of the user was shortened")
			}
		case "assistant":
			answers = append(answers, *m.Content)
		}
	}
	if len(answers) != 4 {
		t.Fatalf("%d answers sent", len(answers))
	}
	for i, a := range answers {
		short := strings.Contains(a, "[code block of 50 lines left out]") && strings.HasSuffix(a, "[shortened]")
		if want := i < 2; short != want {
			t.Fatalf("answer %d shortened=%v, want %v: %q", i, short, want, a[:min(len(a), 120)]+" ... "+a[max(0, len(a)-60):])
		}
	}
	turns := s.Turns()
	if last := turns[len(turns)-1].Text; last != compactNote {
		t.Fatalf("the user was not told: %q", last)
	}
	for _, turn := range turns {
		if turn.Role == "assistant" && strings.Contains(turn.Text, "left out") {
			t.Fatal("the dialog itself was shortened")
		}
	}
}

func TestDialogTooLongEvenShortenedSaysWhatToDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"prompt is too long"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	s := NewSession()
	err := s.Ask(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "q")
	if err == nil || !strings.Contains(err.Error(), "ai:new") {
		t.Fatalf("err %v", err)
	}
}
