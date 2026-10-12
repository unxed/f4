package vtvibe

import (
	"context"
	"strings"
	"testing"
)

// f4#1842, stage H5 second step: the manager hands tasks to workers.

func TestManagerHandsTasksToWorkers(t *testing.T) {
	srv, got := fakeAgentServer(t, chatReply("I will split it.\nWORKER TASK #1: run go test ./... in the project and report failures\n  WORKER TASK: count the TODO lines\nWORKER TASK #7: unknown order"))
	s := NewSession()
	s.SetDelegation(true)
	end, err := s.Work(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "check the project", true)
	if err != nil || end != WorkDelegated || len(*got) != 1 {
		t.Fatalf("end %v, err %v, requests %d", end, err, len(*got))
	}
	if !strings.Contains(*(*got)[0].Messages[0].Content, "WORKER TASK #N") {
		t.Fatal("the model was not told how to hand work out")
	}
	d := s.TakeDelegations()
	want := []Delegation{{1, "run go test ./... in the project and report failures"}, {0, "count the TODO lines"}, {0, "unknown order"}}
	if len(d) != len(want) {
		t.Fatalf("delegations %#v", d)
	}
	for i := range want {
		if d[i] != want[i] {
			t.Fatalf("delegation %d = %#v, want %#v", i, d[i], want[i])
		}
	}
	if len(s.TakeDelegations()) != 0 {
		t.Fatal("delegations were handed out twice")
	}
}

func TestResumeGoesOnAfterTheReports(t *testing.T) {
	srv, got := fakeAgentServer(t, chatReply("WORKER TASK #1: do it"), chatReply("The worker did it.\nORDERS DONE: #1"))
	s := NewSession()
	s.SetDelegation(true)
	cfg := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	if end, err := s.Work(context.Background(), cfg, "do it", false); err != nil || end != WorkDelegated {
		t.Fatalf("end %v, err %v", end, err)
	}
	s.TakeDelegations()
	s.Note("assistant", "Worker #1 finished order #1: done.")
	end, err := s.Resume(context.Background(), cfg)
	if err != nil || end != WorkOrdersDone || len(*got) != 2 {
		t.Fatalf("end %v, err %v, requests %d", end, err, len(*got))
	}
	if end, err := s.Resume(context.Background(), cfg); err != nil || end != WorkOrdersDone || len(*got) != 2 {
		t.Fatalf("resume with nothing open asked the model: %v, %v", end, err)
	}
}

func TestWithoutDelegationWorkerLinesAreJustText(t *testing.T) {
	srv, got := fakeAgentServer(t, chatReply("WORKER TASK #1: do it"))
	s := NewSession()
	if _, err := s.Work(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "do it", false); err != nil {
		t.Fatal(err)
	}
	if len(s.TakeDelegations()) != 0 || strings.Contains(*(*got)[0].Messages[0].Content, "WORKER TASK #N") {
		t.Fatal("delegation is on although the host did not turn it on")
	}
}
