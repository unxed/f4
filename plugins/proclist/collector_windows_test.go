//go:build windows

package proclist

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestFiletimeTicksCombinesHighAndLow(t *testing.T) {
	ft := windows.Filetime{LowDateTime: 0x00000001, HighDateTime: 0x00000002}
	got := filetimeTicks(ft)
	want := uint64(0x0000000200000001)
	if got != want {
		t.Fatalf("filetimeTicks = %#x, want %#x", got, want)
	}
}

func TestCollectFindsTheCurrentProcess(t *testing.T) {
	c := newCollector()
	samples, err := c.collect()
	if err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	for _, s := range samples {
		if s.pid == pid {
			if s.name == "" {
				t.Fatalf("sample for the current process has an empty name: %#v", s)
			}
			// This process can always open itself, so GetProcessTimes gives
			// its creation time: procKey relies on it to tell a reused pid apart.
			if s.start == 0 {
				t.Fatalf("sample for the current process has no start time: %#v", s)
			}
			return
		}
	}
	t.Fatalf("current process (pid %d) missing from %d samples", pid, len(samples))
}

func TestCollectComputesCPUPercentAcrossTwoSamples(t *testing.T) {
	c := newCollector()
	if _, err := c.collect(); err != nil {
		t.Fatal(err)
	}

	// Burn CPU deliberately so kernel+user time is guaranteed to advance
	// between samples; waiting on the clock alone without doing any work
	// would not.
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
	}

	samples, err := c.collect()
	if err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	for _, s := range samples {
		if s.pid == pid {
			if s.cpuPercent <= 0 {
				t.Fatalf("cpuPercent = %v after 150ms of busy work, want > 0", s.cpuPercent)
			}
			return
		}
	}
	t.Fatalf("current process (pid %d) missing from the second sample", pid)
}
