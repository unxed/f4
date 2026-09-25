package app

import "testing"

func TestNestedInputModeKeepsExplicitModeCoverageBatch42(t *testing.T) {
	if got := nestedInputMode("console", true, true); got != "console" {
		t.Fatalf("nestedInputMode explicit = %q, want console", got)
	}
}

func TestNestedInputModeKeepsImplicitModeOutsideNestedWindowsCoverageBatch42(t *testing.T) {
	if got := nestedInputMode("", false, true); got != "" {
		t.Fatalf("nestedInputMode non-nested = %q, want empty", got)
	}
}

func TestNestedInputModeSelectsANSIForNestedWindowsCoverageBatch42(t *testing.T) {
	if got := nestedInputMode("", true, true); got != "ansi" {
		t.Fatalf("nestedInputMode nested Windows = %q, want ansi", got)
	}
}

func TestNestedInputModeKeepsDefaultOnNestedUnixCoverageBatch42(t *testing.T) {
	if got := nestedInputMode("", true, false); got != "" {
		t.Fatalf("nestedInputMode nested Unix = %q, want empty", got)
	}
}

func TestShouldPersistGUIWindowSizeCoverageBatch42(t *testing.T) {
	if shouldPersistGUIWindowSize("") {
		t.Fatal("empty backend should not persist GUI window size")
	}
	if !shouldPersistGUIWindowSize("x11") {
		t.Fatal("non-empty backend should persist GUI window size")
	}
}

func TestFormatVersionSHATrimsStandaloneHashCoverageBatch42(t *testing.T) {
	if got := formatVersionSHA("build 12345678"); got != "build 1234567" {
		t.Fatalf("formatVersionSHA = %q, want trimmed hash", got)
	}
}

func TestFormatVersionSHAPreservesLongHashCoverageBatch42(t *testing.T) {
	const version = "commit 123456789abcdef0"
	if got := formatVersionSHA(version); got != version {
		t.Fatalf("formatVersionSHA = %q, want unchanged long hash", got)
	}
}

func TestFormatVersionSHAPreservesAdjacentHashCoverageBatch42(t *testing.T) {
	const version = "a12345678 12345678b"
	if got := formatVersionSHA(version); got != version {
		t.Fatalf("formatVersionSHA = %q, want unchanged adjacent hashes", got)
	}
}

func TestIsHexSequenceCoverageBatch42(t *testing.T) {
	if !isHexSequence([]rune("09abcdef")) {
		t.Fatal("hex sequence was rejected")
	}
	if isHexSequence([]rune("09abcdeg")) {
		t.Fatal("non-hex sequence was accepted")
	}
}

func TestIsHexCharCoverageBatch42(t *testing.T) {
	for _, r := range []rune{'0', '9', 'a', 'f'} {
		if !isHexChar(r) {
			t.Fatalf("isHexChar(%q) = false, want true", r)
		}
	}
	for _, r := range []rune{'/', ':', 'g', 'A'} {
		if isHexChar(r) {
			t.Fatalf("isHexChar(%q) = true, want false", r)
		}
	}
}
