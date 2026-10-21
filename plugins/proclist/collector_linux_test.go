//go:build linux

package proclist

import (
	"os"
	"testing"
	"time"
)

func TestParseProcStatExtractsNameAndTicksDespiteParensInComm(t *testing.T) {
	// comm containing both a space and its own parentheses -- the case a
	// naive strings.Fields split of the whole line would get wrong. Real
	// example: the kernel's "(sd-pam)" helper reports comm "(sd-pam)", which
	// /proc/[pid]/stat then wraps in one more pair: "((sd-pam))".
	line := "1234 (my (weird) process) S 1 1234 1234 0 -1 4194560 100 0 0 0 55 66"
	raw, ok := parseProcStat(line)
	if !ok {
		t.Fatal("parseProcStat: not ok")
	}
	if raw.name != "my (weird) process" {
		t.Fatalf("name = %q, want %q", raw.name, "my (weird) process")
	}
	if raw.utime != 55 || raw.stime != 66 {
		t.Fatalf("utime/stime = %d/%d, want 55/66", raw.utime, raw.stime)
	}
	// The line stops at stime, before starttime: still a process, with no
	// start to tell a reused pid by.
	if raw.start != 0 {
		t.Fatalf("start = %d for a line without starttime, want 0", raw.start)
	}
}

// TestParseProcStatReadsStartTime covers field 22, what procKey tells a
// reused pid by: a full line from a real kernel, comm with a space in it.
func TestParseProcStatReadsStartTime(t *testing.T) {
	line := "4242 (Web Content) S 4100 4100 4100 0 -1 4194560 9133 0 0 0 812 140 0 0 20 0 31 0 987654 2913849344 51200 18446744073709551615"
	raw, ok := parseProcStat(line)
	if !ok {
		t.Fatal("parseProcStat: not ok")
	}
	if raw.name != "Web Content" || raw.utime != 812 || raw.stime != 140 {
		t.Fatalf("parseProcStat = %+v, want Web Content, 812/140", raw)
	}
	if raw.start != 987654 {
		t.Fatalf("start = %d, want 987654", raw.start)
	}
}

func TestParseProcStatRejectsTruncatedLine(t *testing.T) {
	if _, ok := parseProcStat("1234 (short) S 1"); ok {
		t.Fatal("expected ok=false for a line missing the utime/stime fields")
	}
	if _, ok := parseProcStat("no parens here at all"); ok {
		t.Fatal("expected ok=false for a line without a comm in parens")
	}
}

func TestParseVmRSSKiB(t *testing.T) {
	status := "Name:\tbash\nVmPeak:\t   12345 kB\nVmRSS:\t    6789 kB\nVmSize:\t 111 kB\n"
	got, ok := parseVmRSSKiB(status)
	if !ok || got != 6789 {
		t.Fatalf("parseVmRSSKiB = %d, %v, want 6789, true", got, ok)
	}
}

func TestParseVmRSSKiBMissingLine(t *testing.T) {
	// A kernel thread's /proc/[pid]/status has no VmRSS line: no address
	// space, nothing to report.
	if _, ok := parseVmRSSKiB("Name:\tkthreadd\n"); ok {
		t.Fatal("expected ok=false for a status block without VmRSS")
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
			// /proc/self/stat always carries starttime: procKey relies on
			// it to tell a reused pid apart.
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

	// Burn CPU deliberately so utime+stime is guaranteed to advance by at
	// least one clock tick (10ms at the standard 100Hz) between samples;
	// waiting on the clock alone without doing any work would not.
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
