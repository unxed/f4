//go:build darwin

package proclist

import (
	"os"
	"testing"
)

// TestCollectProcDetailsOnSelf reads the current test binary's own
// executable path via libproc's proc_pidpath -- always present, always
// readable by this same process -- the same "exercise the real syscall path
// on ourselves" idiom collector_darwin_test.go already uses for collect()
// itself.
func TestCollectProcDetailsOnSelf(t *testing.T) {
	details := collectProcDetails(os.Getpid())

	if details.cmdline.err != nil {
		t.Fatalf("cmdline: %v", details.cmdline.err)
	}
	if len(details.cmdline.lines) != 1 || details.cmdline.lines[0] == "" {
		t.Fatalf("cmdline = %#v, want one non-empty executable path", details.cmdline.lines)
	}

	// macOS has no CI-verifiable way to parse KERN_PROCARGS2's raw buffer
	// layout for another process' environment, or a real open-files listing
	// with paths (details_darwin.go's own comment): both must report the
	// same explanatory error, not silently empty sections.
	if details.environ.err != errDetailUnsupportedDarwin {
		t.Fatalf("environ.err = %v, want errDetailUnsupportedDarwin", details.environ.err)
	}
	if details.openFiles.err != errDetailUnsupportedDarwin {
		t.Fatalf("openFiles.err = %v, want errDetailUnsupportedDarwin", details.openFiles.err)
	}
}

func TestReadDarwinExecutablePathOnANonexistentPidFails(t *testing.T) {
	section := readDarwinExecutablePath(1 << 20)
	if section.err == nil {
		t.Fatal("readDarwinExecutablePath on a made-up pid unexpectedly succeeded")
	}
}
