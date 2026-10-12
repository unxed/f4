package vtvibe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// f4#1842, stage H7: the steps of a round run in clean contexts.

func runOneRound(t *testing.T, replies ...string) (BotRound, *[]agentRequest, string) {
	t.Helper()
	srv, got := fakeAgentServer(t, replies...)
	instruction := filepath.Join(t.TempDir(), "bot.md")
	if err := os.WriteFile(instruction, []byte("1. pull the accounting\n2. triage\n3. report"), 0o600); err != nil {
		t.Fatal(err)
	}
	var b Bot
	rounds := make(chan BotRound, 1)
	cfg := func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} }
	if err := b.Start(instruction, time.Hour, t.TempDir(), cfg, nil, nil, func(r BotRound) { rounds <- r }); err != nil {
		t.Fatal(err)
	}
	defer b.Stop()
	select {
	case r := <-rounds:
		return r, got, instruction
	case <-time.After(5 * time.Second):
		t.Fatal("the round did not come")
		return BotRound{}, nil, ""
	}
}

func TestBotRoundRunsStepByStepInCleanContexts(t *testing.T) {
	r, got, instruction := runOneRound(t,
		chatReply("STEP: pull the accounting\nSTEP 2: triage the tickets"),
		chatReply("pulled, 3 new tickets"),
		chatReply("triaged #1, #2, #3"))
	if r.Err != nil || len(*got) != 3 {
		t.Fatalf("round %#v, requests %d", r, len(*got))
	}
	plan := (*got)[0]
	if len(plan.Tools) != 0 || !strings.Contains(*plan.Messages[0].Content, "STEP: <what to do>") || !strings.Contains(*plan.Messages[1].Content, "2. triage") {
		t.Fatalf("the planner did not get the instruction without tools: %#v", plan)
	}
	first, second := (*got)[1].Messages, (*got)[2].Messages
	if len(first) != 2 || *first[1].Content != "pull the accounting" || !strings.Contains(*first[0].Content, "step 1") || !strings.Contains(*first[0].Content, instruction) {
		t.Fatalf("step 1 context: %#v", first)
	}
	if strings.Contains(*first[0].Content, "1. pull the accounting\n2. triage") {
		t.Fatal("the whole instruction was put into a step's context")
	}
	if len(second) != 2 || *second[1].Content != "triage the tickets" || !strings.Contains(*second[0].Content, "pulled, 3 new tickets") {
		t.Fatalf("step 2 does not start clean with step 1's report: %#v", second)
	}
	if !strings.Contains(r.Report, "1/2. pull the accounting\npulled, 3 new tickets") || !strings.Contains(r.Report, "2/2. triage the tickets\ntriaged #1, #2, #3") {
		t.Fatalf("report %q", r.Report)
	}
}

func TestBotRoundWithoutAPlanRunsWhole(t *testing.T) {
	r, got, _ := runOneRound(t, chatReply("STEP: everything at once"), chatReply("done whole"))
	if r.Err != nil || r.Report != "done whole" || len(*got) != 2 {
		t.Fatalf("round %#v, requests %d", r, len(*got))
	}
	if whole := (*got)[1].Messages; !strings.Contains(*whole[1].Content, "2. triage") {
		t.Fatalf("the fallback round did not get the instruction: %#v", whole)
	}
}

func TestParseStepsIsBounded(t *testing.T) {
	plan := strings.Repeat("STEP: x\n", maxBotSteps+5) + "not a step\n  STEP 3:  padded  "
	if got := parseSteps(plan); len(got) != maxBotSteps {
		t.Fatalf("%d steps", len(got))
	}
	if got := parseSteps("STEP 1: a\nSTEP 2:  b  "); len(got) != 2 || got[1] != "b" {
		t.Fatalf("%q", got)
	}
}
