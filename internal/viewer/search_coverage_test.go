package viewer

import (
	"context"
	"errors"
	"testing"

	"github.com/unxed/f4/vfs"
)

func cachedViewerBackend(data string) *ViewerBackend {
	b := []byte(data)
	return &ViewerBackend{
		File:      &vfs.MemoryReadAtCloser{Data: b},
		size:      int64(len(b)),
		cacheData: b,
	}
}

func TestSearchMatchHandlesEmptyBoundsAndProgress(t *testing.T) {
	if got, _, err := SearchMatch(context.Background(), nil, "x", 0, SearchOptions{}, nil); got != -1 || err != nil {
		t.Fatalf("nil backend = %d, %v", got, err)
	}
	empty := cachedViewerBackend("")
	if got, _, err := SearchMatch(context.Background(), empty, "x", 0, SearchOptions{}, nil); got != -1 || err != nil {
		t.Fatalf("empty backend = %d, %v", got, err)
	}

	b := cachedViewerBackend("alpha beta alpha")
	var progress []int
	got, length, err := SearchMatch(context.Background(), b, "alpha", -10, SearchOptions{CaseSensitive: true}, func(value int) { progress = append(progress, value) })
	if err != nil || got != 0 || length != 5 || len(progress) == 0 {
		t.Fatalf("forward search = %d, %d, %v, progress=%v", got, length, err, progress)
	}
	if got, _, err := SearchMatch(context.Background(), b, "alpha", 999, SearchOptions{}, nil); got != -1 || err != nil {
		t.Fatalf("past-end search = %d, %v", got, err)
	}
	if got, _, err := SearchMatch(context.Background(), b, "alpha", 999, SearchOptions{Reverse: true}, nil); got != 11 || err != nil {
		t.Fatalf("reverse clamped search = %d, %v", got, err)
	}
}

func TestSearchMatchCancellationAndReadErrors(t *testing.T) {
	b := cachedViewerBackend("alpha beta")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := SearchMatch(ctx, b, "missing", 0, SearchOptions{CaseSensitive: true}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled literal search = %v", err)
	}
	if _, _, err := SearchMatch(ctx, b, "alpha", 0, SearchOptions{Regexp: true}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled materialized search = %v", err)
	}

	b = cachedViewerBackend("alpha")
	b.cacheData = nil
	b.readErr = errors.New("read failed")
	if _, _, err := SearchMatch(context.Background(), b, "alpha", 0, SearchOptions{Regexp: true}, nil); !errors.Is(err, b.readErr) {
		t.Fatalf("materialized read error = %v", err)
	}
}

func TestSearchMatchUsesWholeWordAndReverseLiteral(t *testing.T) {
	b := cachedViewerBackend("alphabet alpha ALPHA")
	if got, length, err := SearchMatch(context.Background(), b, "alpha", 0, SearchOptions{WholeWord: true}, nil); err != nil || got != 9 || length != 5 {
		t.Fatalf("whole-word search = %d, %d, %v", got, length, err)
	}
	if got, length, err := SearchMatch(context.Background(), b, "alpha", int64(len("alphabet alpha ALPHA")), SearchOptions{Reverse: true, CaseSensitive: true}, nil); err != nil || got != 9 || length != 5 {
		t.Fatalf("reverse literal search = %d, %d, %v", got, length, err)
	}
}
