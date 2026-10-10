package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/vtvibe"
	"github.com/unxed/vtui"
)

// f4#1842, § 19a.2: a task the main dialog hands out goes to a worker
// manager, which runs it on workers and reports once.

type queuedTasks struct{ ch chan func() }

func (q queuedTasks) PostTask(fn func()) { q.ch <- fn }

func TestAIStartManagerRunsWorkersAndReports(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body := string(data)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(body, "worker manager") && strings.Contains(body, "Subtask 1:"):
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"manager: the probe is written"}}]}`)
		case strings.Contains(body, "worker manager"):
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"m1","type":"function","function":{"name":"run_workers","arguments":"{\"tasks\":[\"write the probe\"]}"}}]}}]}`)
		default:
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"worker: wrote the probe"}}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	setupPortableIni(t, "0")
	writeVtvibeINI(t, "[general]\nbase_url = "+srv.URL+"\nmodel = m\nkey = k\n")

	session := vtvibe.NewSession()
	q := queuedTasks{ch: make(chan func(), 16)}
	pf := &panel.PanelsFrame{}
	reported := false
	aiStartManager(pf, q, session, "write the probe file", t.TempDir(), func() { reported = true })
	deadline := time.After(10 * time.Second)
	for !reported {
		select {
		case fn := <-q.ch:
			fn()
		case <-deadline:
			t.Fatal("the manager did not report")
		}
	}
	var notes []string
	for _, turn := range session.Turns() {
		notes = append(notes, turn.Text)
	}
	all := strings.Join(notes, "\n---\n")
	for _, want := range []string{"write the probe file", "worker: wrote the probe", "manager: the probe is written"} {
		if !strings.Contains(all, want) {
			t.Errorf("the dialog lacks %q:\n%s", want, all)
		}
	}
}
