//go:build darwin

package proclist

import (
	"runtime"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// Throwaway stress test for reproducing the darwin/amd64 SIGSEGV in
// readDarwinTaskInfo on a CI runner. Not for merging.
func TestReproStressReadTaskInfo(t *testing.T) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	calls := 0
	for round := 0; round < 300; round++ {
		for _, kp := range procs {
			if pid := int(kp.Proc.P_pid); pid > 0 {
				readDarwinTaskInfo(pid)
				calls++
			}
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("%d calls over %d processes without a crash", calls, len(procs))
}

func TestReproStressCollectParallel(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := newCollector()
			for i := 0; i < 100; i++ {
				if _, err := c.collect(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
