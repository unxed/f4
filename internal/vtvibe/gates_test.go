package vtvibe

import (
	"strings"
	"testing"
)

// f4#1842, stage H8: the gate checks a worker's work in a clean dialog.

func startGated(t *testing.T, rules string, replies ...string) (WorkerResult, *[]agentRequest) {
	t.Helper()
	srv, got := fakeAgentServer(t, replies...)
	var w Workers
	w.SetGates(GateRules{User: func() string { return rules }})
	done := make(chan WorkerResult, 1)
	cfg := func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} }
	w.Start("clean the build folder", t.TempDir(), cfg, func() []Tool { return nil }, func(r WorkerResult) { done <- r })
	return waitResult(t, done), got
}

func TestWorkThatFailsTheGateGoesBackToTheWorker(t *testing.T) {
	r, got := startGated(t, "Never use rm -rf.",
		chatReply("removed it with rm -rf build"),
		chatReply("- Rule \"Never use rm -rf\": the report says rm -rf build."),
		chatReply("removed the files one by one"),
		chatReply("GATE PASS"))
	if r.Err != nil || r.GateReturns != 1 || r.Gate != "" || r.Report != "removed the files one by one" || len(*got) != 4 {
		t.Fatalf("result %#v, requests %d", r, len(*got))
	}
	gate := (*got)[1].Messages
	if len(gate) != 2 || !strings.Contains(*gate[0].Content, "Never use rm -rf.") || !strings.Contains(*gate[1].Content, "clean the build folder") ||
		!strings.Contains(*gate[1].Content, "removed it with rm -rf build") || len((*got)[1].Tools) != 0 {
		t.Fatalf("the gate dialog is not clean or lacks the rules or the work: %#v", gate)
	}
	again := (*got)[2].Messages
	if len(again) != 2 || !strings.Contains(*again[0].Content, "did not pass the gate") || !strings.Contains(*again[0].Content, "the report says rm -rf build") {
		t.Fatalf("the worker did not get the objections in a fresh context: %#v", again)
	}
}

func TestWithoutRulesThereIsNoGate(t *testing.T) {
	r, got := startGated(t, "  ", chatReply("done"))
	if r.Err != nil || r.Report != "done" || len(*got) != 1 {
		t.Fatalf("result %#v, requests %d", r, len(*got))
	}
}

func TestGateReturnsAreBounded(t *testing.T) {
	replies := []string{chatReply("bad")}
	for i := 0; i <= maxGateReturns; i++ {
		replies = append(replies, chatReply("- breaks rule 1"), chatReply("bad again"))
	}
	r, _ := startGated(t, "rule 1", replies[:2*maxGateReturns+2]...)
	if r.Err == nil || r.GateReturns != maxGateReturns || r.Gate != "- breaks rule 1" {
		t.Fatalf("result %#v", r)
	}
}
