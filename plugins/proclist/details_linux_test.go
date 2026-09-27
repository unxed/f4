//go:build linux

package proclist

import (
	"os"
	"strconv"
	"testing"
)

// TestCollectProcDetailsOnSelf reads the current test binary's own /proc
// entry -- always present, always readable by this same process -- the same
// "exercise the real syscall path on ourselves" idiom
// collector_linux_test.go already uses for collect() itself.
func TestCollectProcDetailsOnSelf(t *testing.T) {
	details := collectProcDetails(os.Getpid())

	if details.cmdline.err != nil {
		t.Fatalf("cmdline: %v", details.cmdline.err)
	}
	if len(details.cmdline.lines) == 0 {
		t.Fatal("cmdline has no lines for our own process")
	}

	if details.environ.err != nil {
		t.Fatalf("environ: %v", details.environ.err)
	}
	if len(details.environ.lines) == 0 {
		t.Fatal("environ has no lines for our own process")
	}
	for _, l := range details.environ.lines {
		if l == "" {
			t.Fatal("environ contains an empty entry")
		}
	}

	if details.openFiles.err != nil {
		t.Fatalf("openFiles: %v", details.openFiles.err)
	}
	// stdin/stdout/stderr (fd 0/1/2) are always open, so this is never empty
	// for a real process.
	if len(details.openFiles.lines) == 0 {
		t.Fatal("openFiles has no lines for our own process")
	}
}

func TestSplitProcNulListTrimsTrailingNulAndSplits(t *testing.T) {
	got := splitProcNulList([]byte("a\x00bc\x00\x00"))
	want := []string{"a", "bc", ""}
	if len(got) != len(want) {
		t.Fatalf("splitProcNulList = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitProcNulList = %#v, want %#v", got, want)
		}
	}

	if got := splitProcNulList(nil); got != nil {
		t.Fatalf("splitProcNulList(nil) = %#v, want nil", got)
	}
	if got := splitProcNulList([]byte{0}); got != nil {
		t.Fatalf("splitProcNulList([]byte{0}) = %#v, want nil", got)
	}
}

func TestReadProcOpenFilesOnANonexistentPidFails(t *testing.T) {
	section := readProcOpenFiles(1 << 30)
	if section.err == nil {
		t.Fatal("readProcOpenFiles on a made-up pid unexpectedly succeeded")
	}
}

func TestReadProcOpenFilesOrdersByDescriptorNumber(t *testing.T) {
	section := readProcOpenFiles(os.Getpid())
	if section.err != nil {
		t.Fatalf("readProcOpenFiles: %v", section.err)
	}
	last := -1
	for _, line := range section.lines {
		num, err := strconv.Atoi(line[:indexOfArrow(line)])
		if err != nil {
			t.Fatalf("line %q does not start with a descriptor number", line)
		}
		if num <= last {
			t.Fatalf("open files are not sorted by descriptor number: %v", section.lines)
		}
		last = num
	}
}

// indexOfArrow finds " -> " so the test above can pull out the leading
// descriptor number without duplicating readProcOpenFiles' own formatting
// logic.
func indexOfArrow(line string) int {
	for i := 0; i+3 < len(line); i++ {
		if line[i] == ' ' {
			return i
		}
	}
	return len(line)
}
