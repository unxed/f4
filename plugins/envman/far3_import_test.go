package envman

import (
	"strings"
	"testing"
)

func TestMergeFar3ImportAppendAndReplace(t *testing.T) {
	options := OptionsForGOOS("windows")
	current := Config{
		Version:          CurrentConfigVersion,
		IgnoredVariables: []string{"CURRENT", "Path"},
		Entries: []Entry{{
			Kind: KindProfile, Name: "current", Enabled: true, Variables: []string{"CURRENT=one"},
		}},
	}
	imported := far3ImportCandidate{
		Source:              far3SourceRegistry64,
		Entries:             []Entry{{Kind: KindProfile, Name: "Far", Enabled: true, Variables: []string{"FAR_VALUE=two"}}, {Kind: KindSeparator}},
		IgnoredVariables:    []string{"PATH", "FAR_IGNORED"},
		HasIgnoredVariables: true,
		AlwaysUseEditor:     true,
		HasAlwaysUseEditor:  true,
	}

	appended := mergeFar3Import(current, imported, false, options)
	if len(appended.Entries) != 3 || appended.Entries[0].Name != "current" || appended.Entries[1].Name != "Far" || appended.Entries[2].Kind != KindSeparator {
		t.Fatalf("appended entries = %#v", appended.Entries)
	}
	if strings.Join(appended.IgnoredVariables, ",") != "CURRENT,Path,FAR_IGNORED" {
		t.Fatalf("appended ignored variables = %#v", appended.IgnoredVariables)
	}
	if !appended.AlwaysUseEditor {
		t.Fatal("appended import did not import editor preference")
	}

	replaced := mergeFar3Import(current, imported, true, options)
	if len(replaced.Entries) != 2 || replaced.Entries[0].Name != "Far" || replaced.Entries[1].Kind != KindSeparator {
		t.Fatalf("replaced entries = %#v", replaced.Entries)
	}
	if strings.Join(replaced.IgnoredVariables, ",") != "PATH,FAR_IGNORED" {
		t.Fatalf("replaced ignored variables = %#v", replaced.IgnoredVariables)
	}

	imported.Entries[0].Variables[0] = "FAR_VALUE=mutated"
	if appended.Entries[1].Variables[0] != "FAR_VALUE=two" || replaced.Entries[0].Variables[0] != "FAR_VALUE=two" {
		t.Fatal("merged configurations alias imported entry storage")
	}
}

func TestValidateFar3ImportRejectsIncompatibleProfiles(t *testing.T) {
	candidate := far3ImportCandidate{
		Source: far3SourceRegistry64,
		Entries: []Entry{{
			Kind: KindProfile, Name: "legacy", Enabled: true, Variables: []string{"PROMPT=legacy"},
		}},
	}
	err := validateFar3ImportCandidate(candidate, OptionsForGOOS("windows"))
	if err == nil || !strings.Contains(err.Error(), "64-bit registry view") || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("validation error = %v", err)
	}
}
