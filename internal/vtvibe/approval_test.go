package vtvibe

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// f4#1842, stage H9 item 8: asking before each change.

func TestAllowListRules(t *testing.T) {
	a := AllowList{Rules: func() []string { return []string{"# comment", "go test", "git status", "edit_file", "mcp__fs__*"} }}
	for _, c := range []struct {
		tool, cmd string
		want      bool
	}{
		{"shell", "go test ./...", true},
		{"shell", "go  test", true},
		{"shell", "go tester", false},
		{"shell", "go test ./...; rm -rf ~", false},
		{"shell", "go test $(evil)", false},
		{"shell", "git status | sh", false},
		{"shell", "git push", false},
		{"edit_file", "", true},
		{"write_file", "", false},
		{"mcp__fs__read", "", true},
		{"mcp__web__get", "", false},
	} {
		if got := a.Allowed(c.tool, c.cmd); got != c.want {
			t.Errorf("%s %q: %v", c.tool, c.cmd, got)
		}
	}
}

func TestRuleFor(t *testing.T) {
	for in, want := range map[string]string{"git status --short": "git status", "go test ./...": "go test", "ls -la": "ls", "make": "make", "./build.sh x": "./build.sh x", "./build.sh --all": "./build.sh", "cat a.txt": "cat"} {
		if got := ruleFor("shell", in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	if ruleFor("edit_file", "") != "edit_file" {
		t.Error("tool rule")
	}
}

func TestWithApprovalAsksDeniesAndRemembers(t *testing.T) {
	ran := 0
	shell := Tool{Name: "shell", Run: func(context.Context, json.RawMessage) (string, error) { ran++; return "ok", nil }}
	read := Tool{Name: "read_file", Run: func(context.Context, json.RawMessage) (string, error) { return "text", nil }}
	var rules []string
	var asked []string
	answers := []Approval{Deny, AllowAlways}
	approve := func(_ context.Context, tool, summary string) (Approval, error) {
		asked = append(asked, summary)
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	tools := WithApproval([]Tool{shell, read}, approve, AllowList{Rules: func() []string { return rules }, Add: func(r string) { rules = append(rules, r) }})
	args := json.RawMessage(`{"command":"git status --short"}`)
	if _, err := tools[0].Run(context.Background(), args); !errors.Is(err, ErrDenied) || ran != 0 {
		t.Fatalf("denied call: %v, ran %d", err, ran)
	}
	if out, err := tools[0].Run(context.Background(), args); err != nil || out != "ok" || ran != 1 {
		t.Fatalf("allowed call: %q %v", out, err)
	}
	if len(rules) != 1 || rules[0] != "git status" {
		t.Fatalf("rules %q", rules)
	}
	if _, err := tools[0].Run(context.Background(), json.RawMessage(`{"command":"git status"}`)); err != nil || len(asked) != 2 {
		t.Fatalf("an allowed command asked again: %v, asked %q", err, asked)
	}
	if _, err := tools[1].Run(context.Background(), nil); err != nil || len(asked) != 2 {
		t.Fatal("reading asked")
	}
}
