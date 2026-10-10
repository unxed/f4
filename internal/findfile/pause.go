package findfile

import (
	"context"
	"sync"
)

// pauseGate blocks the worker at progress/result checkpoints. Cancellation
// always releases the wait, including when the window closes while paused.
type pauseGate struct {
	mu     sync.Mutex
	resume chan struct{}
}

func (g *pauseGate) setPaused(paused bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if paused && g.resume == nil {
		g.resume = make(chan struct{})
	} else if !paused && g.resume != nil {
		close(g.resume)
		g.resume = nil
	}
}

func (g *pauseGate) wait(ctx context.Context) bool {
	g.mu.Lock()
	resume := g.resume
	g.mu.Unlock()
	if resume != nil {
		select {
		case <-resume:
		case <-ctx.Done():
			return false
		}
	}
	return ctx.Err() == nil
}
