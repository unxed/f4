package vtvibe

import (
	"bytes"
	"go/format"
	"os"
	"testing"
)

func TestGofmtProbe(t *testing.T) {
	source, err := os.ReadFile("provider_test.go")
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := format.Source(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, formatted) {
		t.Fatalf("FORMATTED PROVIDER TEST:\n%s", formatted)
	}
}
