package vfs

import "testing"

// Issue #368: on Linux the ANSI and OEM codepages are guessed from the locale,
// so the user must be able to state them instead.
func TestSetSystemCodepages_ForcedAndRestored(t *testing.T) {
	t.Cleanup(func() {
		if err := SetSystemCodepages(0, 0); err != nil {
			t.Fatalf("restoring detected codepages: %v", err)
		}
	})

	if err := SetSystemCodepages(1251, 866); err != nil {
		t.Fatalf("SetSystemCodepages(1251, 866): %v", err)
	}
	if SystemANSICodepage() != 1251 || SystemOEMCodepage() != 866 {
		t.Fatalf("forced ids not in effect: ANSI=%d OEM=%d", SystemANSICodepage(), SystemOEMCodepage())
	}
	if cp, ok := FindCodepage(1251); !ok || cp.Enc != GetSystemANSIEncoding() {
		t.Fatalf("ANSI encoding does not match the forced codepage")
	}
	if cp, ok := FindCodepage(866); !ok || cp.Enc != GetSystemOEMEncoding() {
		t.Fatalf("OEM encoding does not match the forced codepage")
	}
	// The aliases follow the override everywhere: stored legacy ids, the
	// status line, and the menu.
	if NormalizeCodepageID(legacySystemANSI) != 1251 || NormalizeCodepageID(legacySystemOEM) != 866 {
		t.Fatalf("legacy ids still resolve to the detected codepages")
	}
	if got := DisplayCodepageName(866); got != "OEM" {
		t.Fatalf("DisplayCodepageName(866) = %q, want OEM", got)
	}
	seen := 0
	for _, cp := range AvailableCodepages {
		if cp.ID == 1251 || cp.ID == 866 {
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("forced codepages appear %d times in the list, want 2 (one entry each)", seen)
	}

	// A codepage this build does not know costs only its own half.
	if err := SetSystemCodepages(999999, 866); err == nil {
		t.Fatal("an unknown codepage was accepted")
	}
	if SystemANSICodepage() != detectedANSI {
		t.Fatalf("rejected ANSI id left %d, want the detected %d", SystemANSICodepage(), detectedANSI)
	}
	if SystemOEMCodepage() != 866 {
		t.Fatalf("a bad ANSI id also lost the OEM override: %d", SystemOEMCodepage())
	}

	if err := SetSystemCodepages(0, 0); err != nil {
		t.Fatalf("clearing the override: %v", err)
	}
	if SystemANSICodepage() != detectedANSI || SystemOEMCodepage() != detectedOEM {
		t.Fatalf("clearing did not restore detection: ANSI=%d OEM=%d", SystemANSICodepage(), SystemOEMCodepage())
	}
	if GetSystemANSIEncoding() != detectedANSIEncoding || GetSystemOEMEncoding() != detectedOEMEncoding {
		t.Fatal("clearing did not restore the detected encodings")
	}
}
