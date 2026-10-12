package sysinfo

import (
	"errors"
	"testing"
	"time"
)

func TestResolveForVolumeFollowsLinkAndCaches(t *testing.T) {
	oldEval, oldClock := evalSymlinks, resolveClock
	defer func() { evalSymlinks, resolveClock = oldEval, oldClock }()
	resolveCache = map[string]resolvedPath{}

	now := time.Unix(1000, 0)
	resolveClock = func() time.Time { return now }
	calls := 0
	evalSymlinks = func(p string) (string, error) {
		calls++
		return `D:\data\series`, nil
	}

	if got := resolveForVolume(`E:\link`); got != `D:\data\series` {
		t.Fatalf("link not followed: %q", got)
	}
	resolveForVolume(`E:\link`)
	if calls != 1 {
		t.Fatalf("answer was not kept: %d lookups", calls)
	}
	now = now.Add(resolveCacheTTL + time.Millisecond)
	resolveForVolume(`E:\link`)
	if calls != 2 {
		t.Fatalf("stale answer was kept: %d lookups", calls)
	}
}

func TestResolveForVolumeKeepsPathWhenUnresolvable(t *testing.T) {
	oldEval := evalSymlinks
	defer func() { evalSymlinks = oldEval }()
	resolveCache = map[string]resolvedPath{}
	evalSymlinks = func(string) (string, error) { return "", errors.New("gone") }
	if got := resolveForVolume(`E:\nowhere`); got != `E:\nowhere` {
		t.Fatalf("path changed: %q", got)
	}
}
