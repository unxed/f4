package vtvibe

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Rules the worker manager forms (unxed/f4#1842, docs/VTVIBE.md § 19a.6,
// stage H8, second step). Besides the user's rules, the gate checks rules the
// manager forms from typical mistakes of workers: after a run that went wrong
// (it failed, the gate gave it back, or many of its commands failed) a clean
// dialog distils at most one short general rule that would have prevented
// it. The rules are kept by the host, shown to the user, and given to every
// worker up front as well as to the gate.

// GateRules is where the gate's rules come from.
type GateRules struct {
	// User returns the user's own rules.
	User func() string
	// Learned returns the rules the manager formed.
	Learned func() string
	// Learn keeps a rule the manager formed; nil switches learning off.
	Learn func(rule string)
}

// text is all the rules as the gate and the workers see them.
func (g GateRules) text() string {
	var parts []string
	if g.User != nil {
		if t := strings.TrimSpace(g.User()); t != "" {
			parts = append(parts, t)
		}
	}
	if g.Learned != nil {
		if t := strings.TrimSpace(g.Learned()); t != "" {
			parts = append(parts, "Rules the worker manager formed from earlier mistakes of workers:\n"+t)
		}
	}
	return strings.Join(parts, "\n\n")
}

// failedCommandsToLearn is how many failed tool calls make a run worth
// learning from even when it ended well.
const failedCommandsToLearn = 3

var exitCodeLine = regexp.MustCompile(`\[exit code ([1-9]\d*)\]\s*$`)

// failedSteps counts the tool calls that failed: an error, or a command that
// exited with a code other than 0.
func failedSteps(steps []AgentStep) int {
	n := 0
	for _, s := range steps {
		if s.Err != nil || exitCodeLine.MatchString(s.Result) {
			n++
		}
	}
	return n
}

// worthLearning says whether a finished run shows mistakes to learn from.
func worthLearning(r WorkerResult) bool {
	return r.Err != nil || r.GateReturns > 0 || failedSteps(r.Steps) >= failedCommandsToLearn
}

const noNewRule = "NO NEW RULE"

// DistillRuleSystemPrompt sets the clean dialog that forms a rule.
func DistillRuleSystemPrompt(model, rules string) string {
	if strings.TrimSpace(rules) == "" {
		rules = "(none yet)"
	}
	return fmt.Sprintf(`You are the worker manager of the f4 file manager's AI, running on the model %q.
A worker's run went wrong. From what it did, form at most one short, general rule for future
workers that would have prevented the mistake: one imperative line, about how to work, not
about this task's details, and not already covered by the rules below. If the run shows no
mistake a rule could prevent, answer with the single line %s.

The rules so far:
%s`, model, noNewRule, rules)
}

// DistillRule asks a clean dialog for a rule from a run that went wrong; ""
// when there is nothing to learn.
func (c Config) DistillRule(ctx context.Context, rules string, r WorkerResult) (string, Usage, error) {
	var what strings.Builder
	what.WriteString(gateEvidence(r.Task, r.Report, r.Steps))
	if r.Gate != "" {
		fmt.Fprintf(&what, "\nThe gate's last objections:\n%s\n", r.Gate)
	}
	if r.GateReturns > 0 {
		fmt.Fprintf(&what, "\nThe gate gave the work back %d time(s).\n", r.GateReturns)
	}
	if r.Err != nil {
		fmt.Fprintf(&what, "\nThe run failed: %v\n", r.Err)
	}
	reply, usage, err := c.Chat(ctx, []Message{
		{Role: "system", Content: DistillRuleSystemPrompt(c.Model, rules)},
		{Role: "user", Content: what.String()},
	})
	if err != nil {
		return "", usage, err
	}
	rule := strings.TrimSpace(reply)
	if rule == "" || strings.EqualFold(rule, noNewRule) || strings.Contains(strings.ToUpper(rule), noNewRule) {
		return "", usage, nil
	}
	// One line, as asked; a model that wrote more keeps only its first line.
	rule, _, _ = strings.Cut(rule, "\n")
	return strings.TrimSpace(strings.TrimLeft(rule, "-*• ")), usage, nil
}
