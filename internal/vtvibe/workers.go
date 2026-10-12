package vtvibe

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Workers (unxed/f4#1842, docs/VTVIBE.md § 19a, stage H5, first step). The
// real work is done by workers, each on one subtask in a clean context; when a
// worker runs out of context it is not compacted but started again, fresh,
// with the list of what it had already done. The main dialog only hands the
// tasks out and keeps the user's orders.

// WorkerResult is what a worker reports when its task is over.
type WorkerResult struct {
	ID       int
	Task     string
	Report   string
	Steps    []AgentStep
	Usage    Usage
	Restarts int
	// GateReturns counts the times the gate gave the work back; Gate holds
	// its last objections when the work never passed (gates.go).
	GateReturns int
	Gate        string
	// Learned is the rule the manager formed from this run, if any.
	Learned string
	Err     error
}

// maxWorkerRestarts bounds how often one task is started again after
// running out of context.
const maxWorkerRestarts = 3

// Workers runs tasks concurrently, each with its own agent loop.
type Workers struct {
	mu      sync.Mutex
	next    int
	running map[int]context.CancelFunc
	tasks   map[int]string
	// gates gives the gate's rules; with none there is no gate (gates.go,
	// gates_learn.go).
	gates GateRules
}

// SetGates makes every finished task pass the gate with the rules gates
// gives (asked each time, so edited rules count at once) and lets the
// manager learn rules from runs that went wrong.
func (w *Workers) SetGates(gates GateRules) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.gates = gates
}

// Running returns the tasks in progress by id.
func (w *Workers) Running() map[int]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[int]string, len(w.tasks))
	for id, t := range w.tasks {
		out[id] = t
	}
	return out
}

// Start runs task in the background in dir with tools; config is asked when
// the task (or a restart of it) begins. done is called from the worker's
// goroutine when it is over. It returns the task's id.
func (w *Workers) Start(task, dir string, config func() Config, tools func() []Tool, done func(WorkerResult)) int {
	ctx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	if w.running == nil {
		w.running, w.tasks = map[int]context.CancelFunc{}, map[int]string{}
	}
	w.next++
	id := w.next
	w.running[id], w.tasks[id] = cancel, task
	gates := w.gates
	w.mu.Unlock()
	go func() {
		result := runGatedWorker(ctx, id, task, dir, config, tools, gates.text)
		if gates.Learn != nil && worthLearning(result) && ctx.Err() == nil {
			// The manager learns from the run before reporting it.
			rule, usage, err := config().DistillRule(ctx, gates.text(), result)
			result.Usage.In += usage.In
			result.Usage.Out += usage.Out
			if err == nil && rule != "" {
				gates.Learn(rule)
				result.Learned = rule
			}
		}
		w.mu.Lock()
		delete(w.running, id)
		delete(w.tasks, id)
		w.mu.Unlock()
		cancel()
		if done != nil {
			done(result)
		}
	}()
	return id
}

// Stop cancels the task with id; it reports whether it was running.
func (w *Workers) Stop(id int) bool {
	w.mu.Lock()
	cancel, ok := w.running[id]
	w.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// runGatedWorker runs the task and, when there are rules, has a clean
// dialog check the work; work that does not pass goes back to a fresh worker
// with the objections, at most maxGateReturns times.
func runGatedWorker(ctx context.Context, id int, task, dir string, config func() Config, tools func() []Tool, rules func() string) WorkerResult {
	upfront := ""
	if rules != nil {
		if text := strings.TrimSpace(rules()); text != "" {
			upfront = "\n\nFollow these rules; a gate checks your work against them:\n" + text
		}
	}
	result := runWorker(ctx, id, task, dir, config, tools, upfront)
	for {
		text := ""
		if rules != nil {
			text = strings.TrimSpace(rules())
		}
		if result.Err != nil || text == "" {
			return result
		}
		verdict, usage, err := config().CheckGate(ctx, text, task, result.Report, result.Steps)
		result.Usage.In += usage.In
		result.Usage.Out += usage.Out
		if err != nil {
			result.Err = fmt.Errorf("gate: %w", err)
			return result
		}
		if verdict.Pass {
			result.Gate = ""
			return result
		}
		result.Gate = verdict.Objections
		if result.GateReturns >= maxGateReturns {
			result.Err = fmt.Errorf("the work did not pass the gate after %d returns", result.GateReturns)
			return result
		}
		again := runWorker(ctx, id, task, dir, config, tools, upfront+gateReturnPrompt(verdict.Objections))
		again.Steps = append(result.Steps, again.Steps...)
		again.Usage.In += result.Usage.In
		again.Usage.Out += result.Usage.Out
		again.Restarts += result.Restarts
		again.GateReturns = result.GateReturns + 1
		again.Gate = result.Gate
		result = again
	}
}

func runWorker(ctx context.Context, id int, task, dir string, config func() Config, tools func() []Tool, extra string) WorkerResult {
	result := WorkerResult{ID: id, Task: task}
	var earlier []AgentStep
	for attempt := 0; ; attempt++ {
		cfg := config()
		msgs := []Message{
			{Role: "system", Content: WorkerSystemPrompt(cfg.Model, dir, time.Now(), earlier) + ProjectInstructions(dir) + extra},
			{Role: "user", Content: task},
		}
		var steps []AgentStep
		report, usage, err := cfg.RunAgent(ctx, msgs, tools(), AgentOptions{
			MaxSteps: 200,
			OnStep:   func(s AgentStep) { steps = append(steps, s) },
		})
		result.Steps = append(result.Steps, steps...)
		result.Usage.In += usage.In
		result.Usage.Out += usage.Out
		if err != nil && contextExhausted(err) && attempt < maxWorkerRestarts && ctx.Err() == nil {
			// Out of context: start again in a clean one rather than
			// compacting, carrying over only what was already done.
			earlier = append(earlier, steps...)
			result.Restarts++
			continue
		}
		result.Report, result.Err = report, err
		return result
	}
}

// contextExhausted reports whether err says the request no longer fits the
// model's context window. Providers word it differently.
func contextExhausted(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, sign := range []string{"context length", "context window", "maximum context", "too many tokens", "prompt is too long", "context_length_exceeded", "input is too long"} {
		if strings.Contains(msg, sign) {
			return true
		}
	}
	return false
}

// WorkerSystemPrompt sets a worker on its one task. earlier lists the steps a
// previous run of the same task made before it ran out of context.
func WorkerSystemPrompt(model, dir string, now time.Time, earlier []AgentStep) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `You are a worker of the f4 file manager. You are running on the model %q.
You get one task and do it completely, using the tools: shell runs commands,
read_file, write_file and edit_file work with
files, grep and find_files search them, view_image
shows you a picture file, fetch_url
loads a web page. The working directory is %s. The
current time is %s. When the task is done, answer with a short report of what
you did and what, if anything, is left; the report is all the user sees.`, model, dir, now.UTC().Format(time.RFC3339))
	if len(earlier) > 0 {
		sb.WriteString("\n\nAn earlier run of this task ran out of context after these steps; do not repeat what is done, continue from there:\n")
		for _, s := range earlier {
			fmt.Fprintf(&sb, "- %s %s\n", s.Tool, orderSummary(s.Args))
		}
	}
	return sb.String()
}
