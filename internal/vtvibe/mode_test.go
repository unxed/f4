package vtvibe

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// f4#1842, stage H6: the dialog's working mode.

func chatReply(text string) string {
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": text}}}})
	return string(data)
}

func TestNonstopGoesOnUntilTheOrdersAreDone(t *testing.T) {
	srv, got := fakeAgentServer(t, chatReply("first part"), chatReply("second part\nORDERS DONE: #1"))
	s := NewSession()
	end, err := s.Work(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "write the report", true)
	if err != nil || end != WorkOrdersDone || len(*got) != 2 {
		t.Fatalf("end %v, err %v, requests %d", end, err, len(*got))
	}
	second := (*got)[1].Messages
	if !strings.Contains(*second[0].Content, "non-stop mode") || !strings.Contains(*second[len(second)-1].Content, "[f4, non-stop mode] Go on with the open orders: #1") {
		t.Fatalf("the second round is not a go-on from f4: %#v", second)
	}
	if orders := s.Orders(); len(orders) != 1 || !orders[0].Done {
		t.Fatalf("the go-on message entered the register or the order is open: %#v", orders)
	}
	turns := s.Turns()
	if turns[len(turns)-1].Text != "second part\n\n✓ #1" {
		t.Fatalf("last turn %q", turns[len(turns)-1].Text)
	}
}

func TestQuestionAndAnswerStopsAfterOneAnswer(t *testing.T) {
	srv, got := fakeAgentServer(t, chatReply("an answer"))
	s := NewSession()
	// The dialog's own mode wins over the default.
	s.SetMode(ModeQA)
	end, err := s.Work(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "a question", true)
	if err != nil || end != WorkAnswered || len(*got) != 1 {
		t.Fatalf("end %v, err %v, requests %d", end, err, len(*got))
	}
	if !strings.Contains(*(*got)[0].Messages[0].Content, "question-and-answer mode") {
		t.Fatal("the model was not told the mode")
	}
}

func TestNonstopStopsWhenTheModelNeedsTheUser(t *testing.T) {
	srv, got := fakeAgentServer(t, chatReply("Which branch?\nWAITING FOR USER"))
	s := NewSession()
	s.SetMode(ModeNonstop)
	end, err := s.Work(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "merge it", false)
	if err != nil || end != WorkWaiting || len(*got) != 1 {
		t.Fatalf("end %v, err %v, requests %d", end, err, len(*got))
	}
}

func TestNonstopRoundsAreBounded(t *testing.T) {
	replies := make([]string, MaxNonstopRounds+1)
	for i := range replies {
		replies[i] = chatReply("still working")
	}
	srv, got := fakeAgentServer(t, replies...)
	s := NewSession()
	end, err := s.Work(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "endless", true)
	if err != nil || end != WorkRoundLimit || len(*got) != MaxNonstopRounds+1 {
		t.Fatalf("end %v, err %v, requests %d", end, err, len(*got))
	}
}

func TestModeIsKeptWithTheDialog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dialog.json")
	first := NewSession()
	if err := first.SetStorePath(path); err != nil {
		t.Fatal(err)
	}
	first.SetMode(ModeNonstop)
	second := NewSession()
	if err := second.SetStorePath(path); err != nil {
		t.Fatal(err)
	}
	if second.Mode() != ModeNonstop {
		t.Fatalf("mode %q", second.Mode())
	}
	second.Reset(false)
	if second.Mode() != ModeDefault {
		t.Fatal("a new dialog kept the old one's mode")
	}
}

func TestParseMode(t *testing.T) {
	for text, want := range map[string]Mode{"nonstop": ModeNonstop, "Non-Stop": ModeNonstop, "qa": ModeQA, "default": ModeDefault} {
		if got, err := ParseMode(text); err != nil || got != want {
			t.Errorf("%q: %q, %v", text, got, err)
		}
	}
	if _, err := ParseMode("sometimes"); err == nil {
		t.Error("an unknown mode was accepted")
	}
}
