package multiarc

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGzipBackendList(t *testing.T) {
	entries, err := (gzipBackend{}).list(context.Background(), "/logs/messages.log.gz")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []entry{{Path: "messages.log"}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries = %#v, want %#v", entries, want)
	}
}

func TestGzipBackendExtractOne(t *testing.T) {
	var gotBin string
	var gotArgs []string
	withFakeTools(t, func(name string) (string, error) {
		if name == "gzip" {
			return "/bin/gzip", nil
		}
		return "", errNotFoundStub
	}, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotBin, gotArgs = name, args
		return []byte("decompressed content"), nil, nil
	})

	dest := t.TempDir()
	if err := (gzipBackend{}).extractOne(context.Background(), "/logs/messages.log.gz", dest, "messages.log"); err != nil {
		t.Fatalf("extractOne: %v", err)
	}
	if gotBin != "gzip" || !reflect.DeepEqual(gotArgs, []string{"-dc", "/logs/messages.log.gz"}) {
		t.Fatalf("command = %s %v", gotBin, gotArgs)
	}
	got, err := os.ReadFile(filepath.Join(dest, "messages.log"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "decompressed content" {
		t.Fatalf("extracted content = %q", got)
	}
}

func TestGzipBackendExtractOneWrongMember(t *testing.T) {
	if err := (gzipBackend{}).extractOne(context.Background(), "/a.gz", t.TempDir(), "not-the-name"); err == nil {
		t.Fatal("expected an error for a member that is not the single gzip entry")
	}
}
