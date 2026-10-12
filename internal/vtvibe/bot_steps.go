package vtvibe

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Steps of a bot round in clean contexts (unxed/f4#1842, docs/VTVIBE.md
// § 19a.5, stage H7). One round of a long instruction (the Lunobot one: pull
// the accounting, triage the tickets, drive the trains, one task, a report)
// does not fit one context well: unrelated steps clog it for each other. So a
// round first asks the model, without tools, to split the instruction into
// its steps; then each step runs in its own fresh agent dialog that sees only
// that step, the reports of the steps before it, and where to read the
// instruction itself. A plan of fewer than two steps runs the round whole.

// maxBotSteps bounds the steps of one round.
const maxBotSteps = 12

// maxStepReport bounds how much of one step's report the next steps see.
const maxStepReport = 4000

var stepLine = regexp.MustCompile(`(?m)^[ \t]*STEP[ \t]*\d*[ \t]*:[ \t]*(\S.*?)[ \t]*$`)

// SetStepped chooses whether rounds are split into steps in clean contexts
// (the default for the bot) or run whole in one context. It counts from the
// next round.
func (b *Bot) SetStepped(on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.whole = !on
}

// SetToolWrapper makes every round pass its tools through wrap first (the
// host's approval of each change, f4#1842 stage H9); nil leaves them as
// they are. It counts from the next round.
func (b *Bot) SetToolWrapper(wrap func([]Tool) []Tool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.wrap = wrap
}

func (b *Bot) stepped() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.whole
}

// BotPlanPrompt asks for the steps of one round.
func BotPlanPrompt(model string) string {
	return fmt.Sprintf(`You plan one round of a bot run by the f4 file manager, on the model %q.
Split one round of the instruction the user gives into its steps, in order. Each step
is carried out by an agent in its own fresh context that sees only that step, the
reports of the steps before it, and can read the instruction itself, so word each
step to stand on its own and name the parts of the instruction it follows. Give
between 2 and %d steps; join steps too small to need a context of their own. Answer
only with lines of the form
STEP: <what to do>`, model, maxBotSteps)
}

// parseSteps reads the planner's answer.
func parseSteps(plan string) []string {
	var steps []string
	for _, m := range stepLine.FindAllStringSubmatch(plan, -1) {
		steps = append(steps, m[1])
	}
	if len(steps) > maxBotSteps {
		steps = steps[:maxBotSteps]
	}
	return steps
}

// BotStepSystemPrompt sets an agent on step i (from 1) of n of a round.
func BotStepSystemPrompt(model, dir, source string, now time.Time, i, n int, earlier []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `You are a bot run by the f4 file manager. You are running on the model %q.
A round of the bot's instruction is split into %d steps, each done in a fresh
context; you do step %d. The whole instruction is at %s: read the parts your
step needs (read_file for a file, the shell for a URL) rather than guessing.
Do your step completely and only it, using the tools: shell runs commands,
read_file, write_file and edit_file work with
files, grep and find_files search them, view_image
shows you a picture file, fetch_url
loads a web page. The working directory is %s. The
current time is %s. When the step is done, answer with a short report of what
you did and what the next steps must know; that report is all they see of it.`,
		model, n, i, source, dir, now.UTC().Format(time.RFC3339))
	if len(earlier) > 0 {
		sb.WriteString("\n\nReports of the steps before yours:\n")
		for k, r := range earlier {
			fmt.Fprintf(&sb, "\n--- step %d ---\n%s\n", k+1, r)
		}
	}
	return sb.String()
}

// steppedRound runs one round step by step; ok is false when the plan had
// fewer than two steps and the round should run whole instead.
func (b *Bot) steppedRound(ctx context.Context, round *BotRound, instruction, dir string, cfg Config, tools []Tool) (ok bool) {
	plan, usage, err := cfg.Chat(ctx, []Message{
		{Role: "system", Content: BotPlanPrompt(cfg.Model)},
		{Role: "user", Content: instruction},
	})
	round.Usage.In += usage.In
	round.Usage.Out += usage.Out
	if err != nil {
		round.Err = err
		return true
	}
	steps := parseSteps(plan)
	if len(steps) < 2 {
		return false
	}
	var reports, parts []string
	for i, step := range steps {
		msgs := []Message{
			{Role: "system", Content: BotStepSystemPrompt(cfg.Model, dir, b.Status().Source, time.Now(), i+1, len(steps), reports) + ProjectInstructions(dir)},
			{Role: "user", Content: step},
		}
		report, usage, err := cfg.RunAgent(ctx, msgs, tools, AgentOptions{
			MaxSteps: 200,
			OnStep:   func(s AgentStep) { round.Steps = append(round.Steps, s) },
		})
		round.Usage.In += usage.In
		round.Usage.Out += usage.Out
		parts = append(parts, fmt.Sprintf("%d/%d. %s\n%s", i+1, len(steps), step, report))
		if err != nil {
			round.Err = fmt.Errorf("step %d of %d (%s): %w", i+1, len(steps), orderSummary(step), err)
			break
		}
		if r := []rune(report); len(r) > maxStepReport {
			report = string(r[:maxStepReport]) + "…"
		}
		reports = append(reports, report)
	}
	round.Report = strings.Join(parts, "\n\n")
	return true
}
