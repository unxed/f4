//go:build windows

package proclist

import (
	"os"
	"testing"
)

// TestCollectProcDetailsOnSelf reads the current test binary's own
// executable path -- always present, always readable by this same
// process -- the same "exercise the real syscall path on ourselves" idiom
// collector_windows_test.go already uses for collect() itself.
func TestCollectProcDetailsOnSelf(t *testing.T) {
	details := collectProcDetails(os.Getpid())

	if details.cmdline.err != nil {
		t.Fatalf("cmdline: %v", details.cmdline.err)
	}
	if len(details.cmdline.lines) != 1 || details.cmdline.lines[0] == "" {
		t.Fatalf("cmdline = %#v, want one non-empty executable path", details.cmdline.lines)
	}

	// Windows has no documented, unprivileged way to read another process'
	// environment or open handles (details_windows.go's own comment): both
	// must report the same explanatory error, not silently empty sections.
	if details.environ.err != errDetailUnsupportedWindows {
		t.Fatalf("environ.err = %v, want errDetailUnsupportedWindows", details.environ.err)
	}
	if details.openFiles.err != errDetailUnsupportedWindows {
		t.Fatalf("openFiles.err = %v, want errDetailUnsupportedWindows", details.openFiles.err)
	}
}

func TestReadWindowsExecutablePathOnANonexistentPidFails(t *testing.T) {
	section := readWindowsExecutablePath(1 << 30)
	if section.err == nil {
		t.Fatal("readWindowsExecutablePath on a made-up pid unexpectedly succeeded")
	}
}
