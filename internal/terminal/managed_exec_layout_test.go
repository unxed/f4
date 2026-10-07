package terminal

import (
	"strings"
	"testing"
)

func TestManagedForegroundCommandInDirectoryAlwaysEmitsCompletionOnCdFailure(t *testing.T) {
	got := ManagedForegroundCommandInDirectory("'/root/private'", "'printf ok'")
	if !strings.Contains(got, "if cd '/root/private'; then") {
		t.Fatalf("directory guard missing: %q", got)
	}
	if !strings.Contains(got, `printf "\033]133;C\007\033]133;D\007"`) {
		t.Fatalf("cd failure does not emit completion markers: %q", got)
	}
	if !strings.Contains(got, ManagedForegroundCommand("'printf ok'")) {
		t.Fatalf("managed command missing from success arm: %q", got)
	}
}
