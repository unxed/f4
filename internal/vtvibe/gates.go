package vtvibe

import (
	"context"
	"fmt"
	"strings"
)

// Gates (unxed/f4#1842, docs/VTVIBE.md § 19a.6, stage H8, first step): rules
// by which a separate clean dialog formally checks what a worker did — formal
// errors and breaches of the user's prohibitions. A worker whose work does
// not pass is given it back with the objections, in a fresh context, rather
// than its report going to the user. This step takes the user's rules; the
// rules the worker manager forms from typical mistakes come next.

// maxGateReturns bounds how often one task goes back to a worker.
const maxGateReturns = 2

// GateVerdict is what the checking dialog decided.
type GateVerdict struct {
	Pass       bool
	Objections string
}

// gatePassLine is the answer of a check that found nothing.
const gatePassLine = "GATE PASS"

// GateSystemPrompt sets the checking dialog on the user's rules.
func GateSystemPrompt(model, rules string) string {
	return fmt.Sprintf(`You are the gate of the f4 file manager's AI workers, running on the model %q.
You check, formally and strictly, what one worker did against the user's rules below: formal
errors and every breach of a rule or prohibition. You do not redo the work and do not judge
style beyond the rules. You get the worker's task, the tool calls it made and its report.
If nothing breaks a rule, answer with the single line %s. Otherwise answer with a list
of the breaches, each naming the rule and the tool call or the part of the report that breaks it,
so the worker can put it right.

The user's rules:
%s`, model, gatePassLine, rules)
}

// gateEvidence is what the checking dialog sees of a worker's run.
func gateEvidence(task, report string, steps []AgentStep) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Task:\n%s\n\nTool calls (%d):\n", task, len(steps))
	for i, s := range steps {
		result := s.Result
		if s.Err != nil {
			result = "error: " + s.Err.Error()
		}
		fmt.Fprintf(&sb, "%d. %s %s\n   -> %s\n", i+1, s.Tool, cutRunes(s.Args, 1000), cutRunes(strings.TrimSpace(result), 300))
	}
	fmt.Fprintf(&sb, "\nReport:\n%s\n", report)
	return sb.String()
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// CheckGate runs the checking dialog, clean, without tools.
func (c Config) CheckGate(ctx context.Context, rules, task, report string, steps []AgentStep) (GateVerdict, Usage, error) {
	reply, usage, err := c.Chat(ctx, []Message{
		{Role: "system", Content: GateSystemPrompt(c.Model, rules)},
		{Role: "user", Content: gateEvidence(task, report, steps)},
	})
	if err != nil {
		return GateVerdict{}, usage, err
	}
	reply = strings.TrimSpace(reply)
	if strings.EqualFold(reply, gatePassLine) || strings.HasPrefix(strings.ToUpper(reply), gatePassLine+"\n") {
		return GateVerdict{Pass: true}, usage, nil
	}
	return GateVerdict{Objections: reply}, usage, nil
}

// gateReturnPrompt is added to a worker's system prompt when its work comes
// back from the gate.
func gateReturnPrompt(objections string) string {
	return "\n\nYour earlier run of this task did not pass the gate that checks it against the user's rules. " +
		"Put right every point below, without breaking any rule again, and report what you changed:\n" + objections
}
