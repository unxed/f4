package vtvibe

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// f4#1842, stage H9 item 1: what a dialog spent, and what it cost.

func TestTheDialogAddsUpWhatItSpentAndKeepsIt(t *testing.T) {
	srv, _ := fakeAgentServer(t, finalReply, finalReply)
	path := filepath.Join(t.TempDir(), "dialog.json")
	s := NewSession()
	if err := s.SetStorePath(path); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"a", "b"} {
		if err := s.Ask(context.Background(), Config{BaseURL: srv.URL, Model: model, APIKey: "k"}, "hi "+model); err != nil {
			t.Fatal(err)
		}
	}
	s.AddSpent("a", Usage{In: 100, Out: 7})
	again := NewSession()
	if err := again.SetStorePath(path); err != nil {
		t.Fatal(err)
	}
	spent := again.Spent()
	if spent["a"] != (Usage{In: 120, Out: 10}) || spent["b"] != (Usage{In: 20, Out: 3}) {
		t.Fatalf("spent %#v", spent)
	}
	if total := TotalSpent(spent); total != (Usage{In: 140, Out: 13}) {
		t.Fatalf("total %#v", total)
	}
	again.Reset(false)
	if len(again.Spent()) != 0 {
		t.Fatal("a new dialog starts with the old one's spending")
	}
}

func TestCostsUseThePublishedPrices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"paid","pricing":{"prompt":"0.000002","completion":0.00001}},{"id":"free:free","pricing":{"prompt":"0","completion":"0"}},{"id":"bare"}]}`)
	}))
	defer srv.Close()
	models, err := Config{BaseURL: srv.URL, APIKey: "k"}.ModelsWithInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	costs := Costs(map[string]Usage{"paid": {In: 1000, Out: 100}, "free:free": {In: 5, Out: 5}, "bare": {In: 1, Out: 1}, "gone": {In: 2, Out: 2}}, models)
	if len(costs) != 4 || costs[0].Model != "paid" || !costs[0].Priced || math.Abs(costs[0].Cost-0.003) > 1e-12 {
		t.Fatalf("costs %#v", costs)
	}
	if costs[1].Model != "free:free" || !costs[1].Priced || costs[1].Cost != 0 {
		t.Fatalf("free model %#v", costs[1])
	}
	if costs[2].Priced || costs[3].Priced || costs[2].Model != "bare" || costs[3].Model != "gone" {
		t.Fatalf("unpriced %#v", costs[2:])
	}
}

func TestFormatTokens(t *testing.T) {
	for n, want := range map[int]string{950: "950", 12345: "12.3k", 4_100_000: "4.1M"} {
		if got := FormatTokens(n); got != want {
			t.Errorf("%d: %q", n, got)
		}
	}
}
