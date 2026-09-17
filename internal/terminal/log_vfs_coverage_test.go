package terminal

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestTerminalLogVFSFallbackRefreshAndEdges(t *testing.T) {
	current := []byte("first snapshot")
	v := NewTerminalLogVFS(nil, func() []byte { return current })
	raw, err := v.Open(context.Background(), "term://log")
	if err != nil {
		t.Fatal(err)
	}
	if raw.Size() != int64(len(current)) {
		t.Fatalf("fallback size = %d", raw.Size())
	}
	buf := make([]byte, 5)
	if n, err := raw.ReadAt(context.Background(), buf, int64(len(current))); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read at end = %d, %v", n, err)
	}
	if n, err := raw.ReadAt(context.Background(), []byte{}, 0); n != 0 || err != nil {
		t.Fatalf("empty read = %d, %v", n, err)
	}

	current = []byte("second snapshot")
	if size, err := raw.(interface {
		RefreshSize(context.Context) (int64, error)
	}).RefreshSize(context.Background()); err != nil || size != int64(len(current)) {
		t.Fatalf("refresh = %d, %v", size, err)
	}
	cancel, cancelFn := context.WithCancel(context.Background())
	cancelFn()
	if _, err := raw.(interface {
		RefreshSize(context.Context) (int64, error)
	}).RefreshSize(cancel); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled refresh = %v", err)
	}

	noSource := &terminalLogWrapper{data: []byte("stable")}
	if size, err := noSource.RefreshSize(context.Background()); err != nil || size != 6 {
		t.Fatalf("nil-source refresh = %d, %v", size, err)
	}
}
