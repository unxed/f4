package vtvibe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// f4#1842, stage H5: the worker manager is a subagent of its own.

func runWorkersCall(tasks string) string {
	return `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"m1","type":"function","function":{"name":"run_workers","arguments":"{\"tasks\":` + tasks + `}"}}]}}]}`
}

// managerServer answers in turn; a request for which overflow says so gets
// the provider's context error instead.
func managerServer(t *testing.T, overflow func(body string) bool, replies ...string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	answered := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, string(data))
		if overflow != nil && overflow(string(data)) {
			http.Error(w, `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`, http.StatusBadRequest)
			return
		}
		if answered >= len(replies) {
			http.Error(w, `{"error":{"message":"too many requests in test"}}`, http.StatusBadRequest)
			return
		}
		answered++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, replies[answered-1])
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

func TestWorkerManagerRunsSubtasksOnWorkers(t *testing.T) {
	srv, bodies := managerServer(t, nil,
		runWorkersCall(`[\"one\",\"two\",\"three\"]`),
		`{"choices":[{"message":{"content":"all three done"}}]}`)
	config := func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} }
	var now, peak int32
	result := RunWorkerManager(context.Background(), config, "do the three things", 2, func(_ context.Context, subtask string) WorkerResult {
		n := atomic.AddInt32(&now, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		defer atomic.AddInt32(&now, -1)
		return WorkerResult{Report: "did " + subtask, Usage: Usage{In: 1, Out: 1}}
	})
	if result.Err != nil || result.Report != "all three done" || len(result.Workers) != 3 {
		t.Fatalf("%+v", result)
	}
	if peak > 2 {
		t.Fatalf("%d workers ran at once, the limit is 2", peak)
	}
	got := bodies()
	if !strings.Contains(got[0], "worker manager") || !strings.Contains(got[1], "Subtask 3: three") || !strings.Contains(got[1], "did two") {
		t.Fatalf("requests:\n%s", strings.Join(got, "\n"))
	}
	if result.Usage.In < 3 {
		t.Fatalf("the workers' usage is not counted: %+v", result.Usage)
	}
}

func TestWorkerManagerRestartsInACleanContext(t *testing.T) {
	srv, bodies := managerServer(t,
		func(body string) bool { return strings.Contains(body, "Subtask 1:") && !strings.Contains(body, "started again") },
		runWorkersCall(`[\"build it\"]`),
		`{"choices":[{"message":{"content":"built"}}]}`)
	config := func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} }
	runs := 0
	result := RunWorkerManager(context.Background(), config, "build the thing", 0, func(_ context.Context, subtask string) WorkerResult {
		runs++
		return WorkerResult{Report: "the build passed"}
	})
	if result.Err != nil || result.Report != "built" || result.Restarts != 1 || runs != 1 {
		t.Fatalf("%+v, runs %d", result, runs)
	}
	got := bodies()
	last := got[len(got)-1]
	if !strings.Contains(last, "started again") || !strings.Contains(last, "the build passed") || strings.Contains(last, `"tool_call_id"`) {
		t.Fatalf("the restart did not get a clean context with the reports: %s", last)
	}
}

func TestWorkerManagerBoundsTheWorkers(t *testing.T) {
	tasks := `[` + strings.TrimSuffix(strings.Repeat(`\"x\",`, maxManagerWorkers+1), ",") + `]`
	srv, bodies := managerServer(t, nil, runWorkersCall(tasks), `{"choices":[{"message":{"content":"gave up"}}]}`)
	config := func() Config { return Config{BaseURL: srv.URL, Model: "m", APIKey: "k"} }
	result := RunWorkerManager(context.Background(), config, "too much", 0, func(context.Context, string) WorkerResult {
		t.Error("a worker ran past the bound")
		return WorkerResult{}
	})
	if result.Report != "gave up" || len(result.Workers) != 0 || !strings.Contains(bodies()[1], "more workers may run") {
		t.Fatalf("%+v", result)
	}
}
