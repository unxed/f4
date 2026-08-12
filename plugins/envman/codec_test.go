package envman

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestEntryCodecLegacyCompatibleRoundTrip(t *testing.T) {
	entry := Entry{
		Kind:      KindProfile,
		Name:      "Разработка",
		Enabled:   true,
		Variables: []string{"A=one", "", "B="},
	}
	encoded, err := EncodeEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	wantText := "=Name=Разработка\n=Enabled=1\n\nA=one\nB=\n"
	if encoded != wantText {
		t.Fatalf("encoded = %q, want %q", encoded, wantText)
	}
	decoded, err := DecodeEntry(encoded, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	entry.Variables = []string{"A=one", "B="}
	if !reflect.DeepEqual(decoded, entry) {
		t.Fatalf("decoded = %#v, want %#v", decoded, entry)
	}
}

func TestEntryCodecSeparatorAndExplicitKind(t *testing.T) {
	encoded, err := EncodeEntry(Entry{Kind: KindSeparator})
	if err != nil {
		t.Fatal(err)
	}
	if encoded != "=Name=-\n" {
		t.Fatalf("separator = %q", encoded)
	}
	decoded, err := DecodeEntry(encoded, EngineOptions{})
	if err != nil || decoded.Kind != KindSeparator {
		t.Fatalf("decoded separator = %#v, %v", decoded, err)
	}

	decoded, err = DecodeEntry("=Kind=profile\r\n=Name=Explicit\r\n=Enabled=false\r\n\r\nA=1\r\n", EngineOptions{})
	if err != nil || decoded.Kind != KindProfile || decoded.Enabled || decoded.Name != "Explicit" {
		t.Fatalf("explicit Kind decode = %#v, %v", decoded, err)
	}
}

func TestDecodeEntryReportsAbsoluteVariableLine(t *testing.T) {
	_, err := DecodeEntry("=Name=test\n=Enabled=1\n\nA=1\nbad\n", EngineOptions{})
	var lineErr *LineError
	if !errors.As(err, &lineErr) || lineErr.Line != 5 {
		t.Fatalf("error = %#v, want line 5", err)
	}
}

func TestEnvironmentCodecSortedFiltersUnportableAndReserved(t *testing.T) {
	opts := EngineOptions{WindowsCaseFold: true}
	encoded, err := EncodeEnvironment([]string{
		"z=last",
		"PROMPT=private",
		"=C:=C:\\work",
		"ProgramFiles(x86)=C:\\Program Files (x86)",
		"A=first",
		"a=updated",
		"TERM=editable",
	}, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := "A=updated\nTERM=editable\nz=last\n"
	if encoded != want {
		t.Fatalf("encoded environment = %q, want %q", encoded, want)
	}
	decoded, err := DecodeEnvironment("\ufeff"+encoded, opts)
	if err != nil {
		t.Fatal(err)
	}
	if wantLines := []string{"A=updated", "TERM=editable", "z=last"}; !reflect.DeepEqual(decoded, wantLines) {
		t.Fatalf("decoded = %#v, want %#v", decoded, wantLines)
	}
}

func TestDecodeEnvironmentLineErrorsAndCRLF(t *testing.T) {
	decoded, err := DecodeEnvironment("A=1\r\n\r\nB=2\r\nA=\r\n", EngineOptions{})
	if err != nil || !reflect.DeepEqual(decoded, []string{"B=2"}) {
		t.Fatalf("CRLF decode = %#v, %v", decoded, err)
	}
	_, err = DecodeEnvironment("A=1\n\nbad\n", EngineOptions{})
	var lineErr *LineError
	if !errors.As(err, &lineErr) || lineErr.Line != 3 {
		t.Fatalf("error = %#v, want line 3", err)
	}
}

func TestEncodeEnvironmentRejectsUnsafePortableValuesWithoutEchoingValue(t *testing.T) {
	for _, value := range []string{"secret\x00tail", "secret\rtail", "secret\ntail"} {
		_, err := EncodeEnvironment([]string{"SAFE=" + value}, EngineOptions{})
		if err == nil || !strings.Contains(err.Error(), "SAFE") {
			t.Fatalf("value %q error = %v", value, err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("error exposed value %q: %v", value, err)
		}
	}
}
