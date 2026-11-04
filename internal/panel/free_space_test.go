package panel

import (
	"testing"
	"time"
)

// The info line is drawn on every frame; statfs is asked once per
// freeSpaceTTL, again at once for another folder, and again after a reread.
func TestFreeSpaceCache(t *testing.T) {
	queries := 0
	free := uint64(100)
	previous := queryFreeBytes
	queryFreeBytes = func(string) (uint64, bool) {
		queries++
		return free, true
	}
	t.Cleanup(func() { queryFreeBytes = previous })

	var c freeSpaceCache
	t0 := time.Now()
	get := func(path string, at time.Duration) uint64 {
		got, ok := c.get(path, t0.Add(at))
		if !ok {
			t.Fatal("free space not known")
		}
		return got
	}

	if got := get("/a", 0); got != 100 || queries != 1 {
		t.Fatalf("first frame: %d after %d queries, want 100 after 1", got, queries)
	}
	free = 50
	for at := time.Duration(0); at < freeSpaceTTL; at += freeSpaceTTL / 10 {
		if got := get("/a", at); got != 100 {
			t.Fatalf("within the TTL: %d, want the cached 100", got)
		}
	}
	if queries != 1 {
		t.Errorf("frames within the TTL asked statfs %d times, want once", queries)
	}
	if got := get("/a", freeSpaceTTL); got != 50 || queries != 2 {
		t.Errorf("after the TTL: %d after %d queries, want 50 after 2", got, queries)
	}
	if get("/b", freeSpaceTTL); queries != 3 {
		t.Errorf("another folder: %d queries, want 3", queries)
	}
	c.reset()
	if get("/b", freeSpaceTTL); queries != 4 {
		t.Errorf("after a reread: %d queries, want 4", queries)
	}
	if get("/b", 0); queries != 5 {
		t.Errorf("a clock that went back: %d queries, want 5", queries)
	}
}
