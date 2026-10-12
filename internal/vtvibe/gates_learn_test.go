package vtvibe

import (
	"errors"
	"strings"
	"testing"
)

// f4#1842, stage H8 second step: the manager forms rules from mistakes.

func startLearning(t *testing.T, learned *[]string, replies ...string) (WorkerResult, *[]agentRequest) {
	t.Helper()
	srv, got := fakeAgentServer(t, replies...)
	var w Workers
	w.SetGates(GateRules{
		User:    func() string { return "Never use rm -rf." },
		Learned: func() string { return strings.Join(*learned, "\n") },
		Learn:   func(rule string) { *learned = append(*learned, rule) },
	})
	done := make(chan WorkerResult, 1)
	cfg := func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} }
	w.Start("clean the build folder", t.TempDir(), cfg, func() []Tool { return nil }, func(r WorkerResult) { done <- r })
	return waitResult(t, done), got
}

func TestTheManagerLearnsARuleFromAReturnedRun(t *testing.T) {
	learned := []string{"Check a folder exists before working in it."}
	r, got := startLearning(t, &learned,
		chatReply("removed it with rm -rf build"),
		chatReply("- breaks \"Never use rm -rf\""),
		chatReply("removed the files one by one"),
		chatReply("GATE PASS"),
		chatReply("- Remove files one by one, never recursively by force.\nBecause..."))
	if r.Err != nil || r.Learned != "Remove files one by one, never recursively by force." || len(learned) != 2 || len(*got) != 5 {
		t.Fatalf("result %#v, learned %q, requests %d", r, learned, len(*got))
	}
	worker := *(*got)[0].Messages[0].Content
	if !strings.Contains(worker, "Never use rm -rf.") || !strings.Contains(worker, "Check a folder exists") {
		t.Fatalf("the worker did not get the rules up front: %s", worker)
	}
	distil := (*got)[4].Messages
	if len(distil) != 2 || !strings.Contains(*distil[0].Content, "Check a folder exists") || !strings.Contains(*distil[1].Content, "gave the work back 1 time") {
		t.Fatalf("the distilling dialog lacks the rules or the run: %#v", distil)
	}
	gate := *(*got)[1].Messages[0].Content
	if !strings.Contains(gate, "Rules the worker manager formed") {
		t.Fatalf("the gate does not check the learned rules: %s", gate)
	}
}

func TestACleanRunTeachesNothing(t *testing.T) {
	var learned []string
	r, got := startLearning(t, &learned, chatReply("done"), chatReply("GATE PASS"))
	if r.Err != nil || r.Learned != "" || len(learned) != 0 || len(*got) != 2 {
		t.Fatalf("result %#v, learned %q, requests %d", r, learned, len(*got))
	}
}

func TestNoNewRuleIsNotKept(t *testing.T) {
	var learned []string
	r, _ := startLearning(t, &learned,
		chatReply("bad"), chatReply("- breaks it"), chatReply("good"), chatReply("GATE PASS"), chatReply("NO NEW RULE"))
	if r.Learned != "" || len(learned) != 0 {
		t.Fatalf("learned %q", learned)
	}
}

func TestFailedSteps(t *testing.T) {
	steps := []AgentStep{
		{Tool: "shell", Result: "ok\n[exit code 0]"},
		{Tool: "shell", Result: "boom\n[exit code 2]"},
		{Tool: "read_file", Err: errors.New("no such file")},
		{Tool: "shell", Result: "[exit code 10]\n"},
	}
	if n := failedSteps(steps); n != 3 {
		t.Fatalf("failed steps %d", n)
	}
	if !worthLearning(WorkerResult{Steps: steps}) || worthLearning(WorkerResult{Steps: steps[:2]}) {
		t.Fatal("worthLearning is off")
	}
}
