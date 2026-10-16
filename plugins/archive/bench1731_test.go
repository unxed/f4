//go:build linux

package archive

// Temporary measurement stand for unxed/f4#1731 (lives on a tmp branch only).
// Skipped unless BENCH1731_ARCHIVE names a 7z archive.

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"syscall"
	"testing"
	"time"

	"github.com/unxed/sevenzip"
)

type bench1731Reporter struct{}

func (bench1731Reporter) UpdateScan(string, int64, int64)                         {}
func (bench1731Reporter) UpdateTransfer(string, string, int, string, int, string) {}
func (bench1731Reporter) IsCancelled() bool                                       { return false }

type bench1731Snap struct {
	at   time.Time
	user float64
	sys  float64
}

func bench1731Take() bench1731Snap {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return bench1731Snap{
		at:   time.Now(),
		user: float64(ru.Utime.Sec) + float64(ru.Utime.Usec)/1e6,
		sys:  float64(ru.Stime.Sec) + float64(ru.Stime.Usec)/1e6,
	}
}

func (s bench1731Snap) report(label string, bytes int64) {
	e := bench1731Take()
	wall := e.at.Sub(s.at).Seconds()
	user := e.user - s.user
	sys := e.sys - s.sys
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	mbs := 0.0
	if wall > 0 {
		mbs = float64(bytes) / 1048576 / wall
	}
	fmt.Printf("BENCH1731 %s: wall=%.2fs user=%.2fs sys=%.2fs busy_cores=%.2f bytes=%d speed=%.1f MB/s maxrss=%d MB heapsys=%d MB numgc=%d gcpause=%.0f ms\n",
		label, wall, user, sys, (user+sys)/wall, bytes, mbs, int64(ru.Maxrss)/1024, ms.HeapSys>>20, ms.NumGC, float64(ms.PauseTotalNs)/1e6)
}

func bench1731ReadAll(f *sevenzip.File, buf []byte) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	var total int64
	for {
		n, rerr := rc.Read(buf)
		total += int64(n)
		if rerr != nil {
			if rerr != io.EOF {
				_ = rc.Close()
				return total, rerr
			}
			break
		}
	}
	return total, rc.Close()
}

func TestBench1731(t *testing.T) {
	path := os.Getenv("BENCH1731_ARCHIVE")
	if path == "" {
		t.Skip("BENCH1731_ARCHIVE is not set")
	}
	mode := os.Getenv("BENCH1731_MODE")
	fmt.Printf("BENCH1731 start: mode=%s GOMAXPROCS=%d NumCPU=%d archive=%s\n", mode, runtime.GOMAXPROCS(0), runtime.NumCPU(), path)

	switch mode {
	case "test":
		// The production path of Shift+F3 (Test archive), progress reporter is a no-op.
		var total int64
		if zr, oerr := sevenzip.OpenReader(path); oerr == nil {
			for _, f := range zr.File {
				total += int64(f.UncompressedSize)
			}
			_ = zr.Close()
		}
		s := bench1731Take()
		err := testArchiveOnce(context.Background(), path, path, "", bench1731Reporter{})
		s.report("testArchiveOnce", total)
		if err != nil {
			t.Fatal(err)
		}

	case "seq":
		// One goroutine, members in archive order, sevenzip.Reader as-is.
		zr, err := sevenzip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		buf := make([]byte, 128*1024)
		var total int64
		s := bench1731Take()
		for _, f := range zr.File {
			if f.UncompressedSize == 0 {
				continue
			}
			n, err := bench1731ReadAll(f, buf)
			total += n
			if err != nil {
				t.Fatal(f.Name, err)
			}
		}
		s.report("sequential single goroutine", total)

	case "folders":
		// Per solid block: last member of the block, read from a fresh Reader
		// (no pool reuse): this is what opening that file from the panel costs.
		s0 := bench1731Take()
		zr0, err := sevenzip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		type grp struct {
			files int
			size  uint64
			last  int
		}
		groups := map[int]*grp{}
		for i, f := range zr0.File {
			if f.UncompressedSize == 0 {
				continue
			}
			g := groups[f.Stream]
			if g == nil {
				g = &grp{}
				groups[f.Stream] = g
			}
			g.files++
			g.size += f.UncompressedSize
			g.last = i
		}
		_ = zr0.Close()
		s0.report("open header + group", 0)
		ids := make([]int, 0, len(groups))
		for id := range groups {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		buf := make([]byte, 128*1024)
		for _, id := range ids {
			g := groups[id]
			zr, err := sevenzip.OpenReader(path)
			if err != nil {
				t.Fatal(err)
			}
			f := zr.File[g.last]
			fmt.Printf("BENCH1731 LAST stream=%d files=%d blockbytes=%d name=%s\n", id, g.files, g.size, f.Name)
			s := bench1731Take()
			n, err := bench1731ReadAll(f, buf)
			s.report(fmt.Sprintf("last member of stream %d (%s)", id, f.Name), n)
			_ = zr.Close()
			if err != nil {
				t.Fatal(err)
			}
		}

	default:
		t.Fatalf("unknown BENCH1731_MODE %q", mode)
	}
}
