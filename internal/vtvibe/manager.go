package vtvibe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The worker manager (unxed/f4#1842, docs/VTVIBE.md § 19a.2, stage H5): a
// subagent between the main dialog and the workers. The main dialog hands it
// one task; it splits the task into subtasks, runs them on workers — each in
// a clean context, no more than a few at a time — checks their reports, gives
// a failed subtask out again, and answers with one report. When its own
// context runs out it is started again in a clean one with the workers'
// reports so far (§ 19a.3).

// ManagerResult is what one run of the worker manager did.
type ManagerResult struct {
	Report   string
	Workers  []WorkerResult
	Usage    Usage
	Restarts int
	Err      error
}

const (
	// DefaultParallelWorkers bounds the workers a manager runs at once.
	DefaultParallelWorkers = 3
	// maxManagerWorkers bounds the workers of one manager run, retries
	// included, so a confused manager cannot spend without end.
	maxManagerWorkers = 20
	// maxManagerRestarts bounds the clean restarts of the manager.
	maxManagerRestarts = 2
	// managerReportChars is how much of each worker's report the manager
	// gets back, and carries over into a restart.
	managerReportChars = 4000
)

// WorkerManagerPrompt sets the manager on its task.
func WorkerManagerPrompt(model string, parallel int, now time.Time) string {
	return fmt.Sprintf(`You are the worker manager of the f4 file manager, running on the model %q.
The main dialog gives you one task. Do not do the work yourself: split it into
small subtasks and give them to workers with the run_workers tool. A worker
starts in a clean context and sees only the text of its subtask, so make each
subtask self-contained: say what to do, where, and what to report. Subtasks
given in one call run at the same time (at most %d at once), so put
subtasks that depend on each other into separate calls, in order. Read every
report; when a worker failed or did only part, give a corrected subtask out
again (at most twice for the same subtask). When the task is done or cannot
be done, answer with a short report for the main dialog: what was done, by
which subtasks, and what, if anything, is left. The current time is %s.`, model, parallel, now.UTC().Format(time.RFC3339))
}

// RunWorkerManager runs the manager on task. run does one subtask on a
// worker; it is called from several goroutines at once, at most parallel.
func RunWorkerManager(ctx context.Context, config func() Config, task string, parallel int,
	run func(ctx context.Context, subtask string) WorkerResult) ManagerResult {
	if parallel <= 0 {
		parallel = DefaultParallelWorkers
	}
	var (
		mu     sync.Mutex
		result ManagerResult
	)
	runWorkers := Tool{
		Name:        "run_workers",
		Description: "Run subtasks on workers, each in a clean context, and get their reports. Subtasks of one call run at the same time.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tasks": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
			},
			"required": []string{"tasks"},
		},
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Tasks []string `json:"tasks"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			var tasks []string
			for _, t := range args.Tasks {
				if t = strings.TrimSpace(t); t != "" {
					tasks = append(tasks, t)
				}
			}
			if len(tasks) == 0 {
				return "", errors.New("no subtask given")
			}
			mu.Lock()
			left := maxManagerWorkers - len(result.Workers)
			mu.Unlock()
			if len(tasks) > left {
				return "", fmt.Errorf("only %d more workers may run for this task; finish with what you have or give fewer subtasks", left)
			}
			reports := make([]WorkerResult, len(tasks))
			slots := make(chan struct{}, parallel)
			var wg sync.WaitGroup
			for i, t := range tasks {
				wg.Add(1)
				go func(i int, t string) {
					defer wg.Done()
					select {
					case slots <- struct{}{}:
					case <-ctx.Done():
						reports[i] = WorkerResult{Task: t, Err: ctx.Err()}
						return
					}
					defer func() { <-slots }()
					reports[i] = run(ctx, t)
					reports[i].Task = t
				}(i, t)
			}
			wg.Wait()
			mu.Lock()
			result.Workers = append(result.Workers, reports...)
			for _, r := range reports {
				result.Usage.In += r.Usage.In
				result.Usage.Out += r.Usage.Out
			}
			mu.Unlock()
			var sb strings.Builder
			for i, r := range reports {
				fmt.Fprintf(&sb, "Subtask %d: %s\n", i+1, r.Task)
				sb.WriteString(workerSummary(r))
				sb.WriteString("\n\n")
			}
			return strings.TrimSpace(sb.String()), ctx.Err()
		},
	}

	for attempt := 0; ; attempt++ {
		cfg := config()
		question := task
		mu.Lock()
		if done := result.Workers; len(done) > 0 {
			// A restart: the clean context gets what the workers already did.
			var sb strings.Builder
			sb.WriteString(task)
			sb.WriteString("\n\n[f4] You ran out of context and were started again. The workers you already ran reported:\n\n")
			for i, r := range done {
				fmt.Fprintf(&sb, "%d. %s\n%s\n\n", i+1, r.Task, workerSummary(r))
			}
			sb.WriteString("Do not run what is done again.")
			question = sb.String()
		}
		mu.Unlock()
		msgs := []Message{
			{Role: "system", Content: WorkerManagerPrompt(cfg.Model, parallel, time.Now())},
			{Role: "user", Content: question},
		}
		report, usage, err := cfg.RunAgent(ctx, msgs, []Tool{runWorkers}, AgentOptions{MaxSteps: 2*maxManagerWorkers + 5, MaxToolOutput: 64 << 10})
		mu.Lock()
		result.Usage.In += usage.In
		result.Usage.Out += usage.Out
		mu.Unlock()
		if err != nil && contextExhausted(err) && attempt < maxManagerRestarts && ctx.Err() == nil {
			result.Restarts++
			continue
		}
		result.Report, result.Err = report, err
		return result
	}
}

// workerSummary is a worker's report as the manager sees it.
func workerSummary(r WorkerResult) string {
	text := strings.TrimSpace(r.Report)
	if r.Err != nil {
		text = "FAILED: " + r.Err.Error() + "\n" + text
	}
	if r.Gate != "" {
		text += "\nThe gate did not pass it: " + r.Gate
	}
	if runes := []rune(text); len(runes) > managerReportChars {
		text = string(runes[:managerReportChars]) + " […]"
	}
	if text == "" {
		text = "(no report)"
	}
	return text
}
