package envman

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestEngineEvaluateOrderedProfilesAndExpansion(t *testing.T) {
	opts := EngineOptions{ExpandDollarSyntax: true}
	engine, err := NewEngine([]string{"A=one", "REMOVE=old", "X=old"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		Version: CurrentConfigVersion,
		Entries: []Entry{
			{
				Kind:    KindProfile,
				Name:    "first",
				Enabled: true,
				Variables: []string{
					"A=two",
					"EXPANDED=%A%|$A|${MISSING}|%MISSING%|%%|$$",
					"EMPTY=%MISSING%",
					"REMOVE=",
					"X=",
					"X=again",
				},
			},
			{Kind: KindSeparator},
			{Kind: KindProfile, Name: "disabled", Enabled: false, Variables: []string{"A=wrong"}},
		},
	}

	result, err := engine.Evaluate(config)
	if err != nil {
		t.Fatal(err)
	}
	values := environmentValues(t, result.Environment)
	want := map[string]string{
		"A":        "two",
		"EXPANDED": "two|two|||%|$",
		"EMPTY":    "",
		"X":        "again",
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("environment = %#v, want %#v", values, want)
	}
	if countEnvironmentName(result.Environment, "X", opts) != 1 {
		t.Fatalf("unset then reset duplicated X: %#v", result.Environment)
	}
	if _, exists := values["REMOVE"]; exists {
		t.Fatalf("REMOVE was not unset: %#v", result.Environment)
	}
}

func TestEngineCapturesBaselineValuesWithLineBreaks(t *testing.T) {
	baseline := []string{"MULTI=first\nsecond\rthird"}
	engine, err := NewEngine(baseline, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Evaluate(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Environment, baseline) {
		t.Fatalf("environment = %#v, want %#v", result.Environment, baseline)
	}
}

func TestExpansionPlatformSemantics(t *testing.T) {
	engine, err := NewEngine([]string{"Path=base"}, EngineOptions{WindowsCaseFold: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Evaluate(Config{
		Version: CurrentConfigVersion,
		Entries: []Entry{{
			Kind:      KindProfile,
			Name:      "windows",
			Enabled:   true,
			Variables: []string{"PATH=%path%;$Path;$$"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := environmentValues(t, result.Environment)["Path"]; got != "base;$Path;$$" {
		t.Fatalf("Windows expansion = %q", got)
	}
	if countEnvironmentName(result.Environment, "PATH", EngineOptions{WindowsCaseFold: true}) != 1 {
		t.Fatalf("case-folded assignment duplicated Path: %#v", result.Environment)
	}
}

func TestParseAssignmentPortableAndTransportValidation(t *testing.T) {
	valid := []string{"TERM=x", "_A9=x", "A=x=y", "A=x\r"}
	for _, line := range valid {
		if _, err := ParseAssignment(line, EngineOptions{}); err != nil {
			t.Errorf("ParseAssignment(%q): %v", line, err)
		}
	}
	invalid := []string{
		"", "NOVALUE", "9A=x", "A-B=x", "=C:=x", "PROMPT=x",
		"COMSPEC=x", "SHELL=x", "F4_NESTED=x", "TERM_PROGRAM=x", "KITTY_WINDOW_ID=x",
		"A=x\x00y", "A=x\ny", "A=x\ry", "A=x\r\r",
	}
	for _, line := range invalid {
		if _, err := ParseAssignment(line, EngineOptions{}); err == nil {
			t.Errorf("ParseAssignment(%q) unexpectedly succeeded", line)
		}
	}
	if _, err := ParseAssignment("prompt=x", EngineOptions{}); err != nil {
		t.Fatalf("Unix names are case-sensitive: %v", err)
	}
	if _, err := ParseAssignment("prompt=x", EngineOptions{WindowsCaseFold: true}); err == nil {
		t.Fatal("Windows case-folding did not protect PROMPT")
	}
	if _, err := ParseAssignment("EXTRA=x", EngineOptions{ReservedNames: []string{"EXTRA"}}); err == nil {
		t.Fatal("custom reserved name was accepted")
	}
}

func TestParseAssignmentsReportsSourceLine(t *testing.T) {
	_, err := ParseAssignments([]string{"A=1", "", "bad"}, EngineOptions{})
	var lineErr *LineError
	if !errors.As(err, &lineErr) || lineErr.Line != 3 {
		t.Fatalf("error = %#v, want LineError at line 3", err)
	}
	_, err = ParseAssignments([]string{"A=1", "\r\r"}, EngineOptions{})
	if !errors.As(err, &lineErr) || lineErr.Line != 2 {
		t.Fatalf("embedded carriage return error = %#v, want line 2", err)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (Config{}).Validate(EngineOptions{}); err == nil {
		t.Fatal("zero version was accepted")
	}
	if err := (Config{Version: CurrentConfigVersion, Entries: []Entry{{Kind: KindSeparator, Name: "not empty"}}}).Validate(EngineOptions{}); err == nil {
		t.Fatal("separator profile data was accepted")
	}
	config := Config{Version: CurrentConfigVersion, IgnoredVariables: []string{"Path", "PATH"}}
	if err := config.Validate(EngineOptions{}); err != nil {
		t.Fatalf("case-distinct Unix ignored variables: %v", err)
	}
	if err := config.Validate(EngineOptions{WindowsCaseFold: true}); err == nil {
		t.Fatal("case-duplicate Windows ignored variables were accepted")
	}
}

func TestConfigRejectsShellSelectorAssignments(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		for _, name := range []string{"COMSPEC", "SHELL"} {
			config := Config{Version: CurrentConfigVersion, Entries: []Entry{{
				Kind: KindProfile, Name: "invalid", Enabled: true, Variables: []string{name + "=alternate-shell"},
			}}}
			if err := config.Validate(OptionsForGOOS(goos)); err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("%s %s assignment error = %v", goos, name, err)
			}
		}
	}
}

func TestReconcileIgnoredReservedAndExplicitAssignments(t *testing.T) {
	opts := EngineOptions{WindowsCaseFold: true}
	baseline := []string{"A=baseline", "IGN=baseline", "PROMPT=baseline", "=C:=baseline"}
	current := []string{"A=external", "IGN=current", "PROMPT=current", "EXTRA=current", "=C:=current"}
	engine, err := NewEngine(baseline, opts)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		Version:          CurrentConfigVersion,
		IgnoredVariables: []string{"IGN"},
		Entries: []Entry{{
			Kind: KindProfile, Name: "managed", Enabled: true, Variables: []string{"A=profile"},
		}},
	}
	reconciliation, err := engine.Reconcile(config, current)
	if err != nil {
		t.Fatal(err)
	}
	target := environmentValues(t, reconciliation.Target)
	if target["A"] != "profile" || target["IGN"] != "current" || target["PROMPT"] != "current" {
		t.Fatalf("target did not preserve/manage expected values: %#v", target)
	}
	if _, exists := target["EXTRA"]; exists {
		t.Fatalf("unmanaged external value survived: %#v", target)
	}
	if !containsEnvironmentLine(reconciliation.Target, "=C:=current") {
		t.Fatalf("current pseudo-variable was not preserved: %#v", reconciliation.Target)
	}

	config.Entries[0].Variables = append(config.Entries[0].Variables, "IGN=profile")
	reconciliation, err = engine.Reconcile(config, current)
	if err != nil {
		t.Fatal(err)
	}
	if got := environmentValues(t, reconciliation.Target)["IGN"]; got != "profile" {
		t.Fatalf("explicit assignment did not override ignored preservation: %q", got)
	}

	config.Entries[0].Variables[len(config.Entries[0].Variables)-1] = "IGN="
	reconciliation, err = engine.Reconcile(config, current)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := environmentValues(t, reconciliation.Target)["IGN"]; exists {
		t.Fatalf("explicit unset did not override ignored preservation: %#v", reconciliation.Target)
	}
}

func TestReconcileProtectsNonportableOSNamesFromDriftAndChanges(t *testing.T) {
	engine, err := NewEngine(
		[]string{"A=baseline", "ProgramFiles(x86)=baseline"},
		EngineOptions{WindowsCaseFold: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	reconciliation, err := engine.Reconcile(
		DefaultConfig(),
		[]string{"A=external", "ProgramFiles(x86)=current"},
	)
	if err != nil {
		t.Fatal(err)
	}
	target := environmentValues(t, reconciliation.Target)
	if target["ProgramFiles(x86)"] != "current" {
		t.Fatalf("nonportable current value was not preserved: %#v", target)
	}
	if got := append(changeNames(reconciliation.Drift.Changed), changeNames(reconciliation.Changes.Changed)...); !reflect.DeepEqual(got, []string{"A", "A"}) {
		t.Fatalf("drift/changes included a protected name: drift=%#v changes=%#v", reconciliation.Drift, reconciliation.Changes)
	}

	reconciliation, err = engine.Reconcile(DefaultConfig(), []string{"A=baseline"})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := environmentValues(t, reconciliation.Target)["ProgramFiles(x86)"]; exists {
		t.Fatalf("absent nonportable value was resurrected: %#v", reconciliation.Target)
	}
	if !reconciliation.Drift.Empty() || !reconciliation.Changes.Empty() {
		t.Fatalf("nonportable absence produced drift/change: drift=%#v changes=%#v", reconciliation.Drift, reconciliation.Changes)
	}
}

func TestDiffEnvironmentStableAndReturnsParseErrors(t *testing.T) {
	diff, err := DiffEnvironment(
		[]string{"B=one", "A=old", "C=gone"},
		[]string{"D=added", "A=new", "B=one"},
		EngineOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := changeNames(diff.Added), []string{"D"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("added names = %#v", got)
	}
	if got, want := changeNames(diff.Changed), []string{"A"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("changed names = %#v", got)
	}
	if got, want := changeNames(diff.Removed), []string{"C"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("removed names = %#v", got)
	}
	if _, err := DiffEnvironment([]string{"not-an-entry"}, nil, EngineOptions{}); err == nil || !strings.Contains(err.Error(), "before") {
		t.Fatalf("before parse error = %v", err)
	}
	if _, err := DiffEnvironment(nil, []string{"not-an-entry"}, EngineOptions{}); err == nil || !strings.Contains(err.Error(), "after") {
		t.Fatalf("after parse error = %v", err)
	}

	diff, err = DiffEnvironment([]string{"Path=one"}, []string{"PATH=two"}, EngineOptions{WindowsCaseFold: true})
	if err != nil || len(diff.Changed) != 1 || diff.Changed[0].Name != "Path" {
		t.Fatalf("Windows diff = %#v, %v", diff, err)
	}
}

func environmentValues(t *testing.T, environment []string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, line := range environment {
		if strings.HasPrefix(line, "=") {
			continue
		}
		separator := strings.IndexByte(line, '=')
		if separator <= 0 {
			t.Fatalf("invalid environment line %q", line)
		}
		result[line[:separator]] = line[separator+1:]
	}
	return result
}

func countEnvironmentName(environment []string, name string, opts EngineOptions) int {
	count := 0
	for _, line := range environment {
		separator := strings.IndexByte(line, '=')
		if separator > 0 && normalizeName(line[:separator], opts) == normalizeName(name, opts) {
			count++
		}
	}
	return count
}

func containsEnvironmentLine(environment []string, line string) bool {
	for _, candidate := range environment {
		if candidate == line {
			return true
		}
	}
	return false
}

func changeNames(changes []Change) []string {
	result := make([]string, len(changes))
	for index, change := range changes {
		result[index] = change.Name
	}
	return result
}
