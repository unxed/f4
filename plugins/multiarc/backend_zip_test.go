package multiarc

import (
	"context"
	"reflect"
	"testing"
)

func TestZipBackendList(t *testing.T) {
	var gotArgs []string
	withFakeTools(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return []byte("readme.txt\nsub/\nsub/data.bin\n"), nil, nil
	})
	entries, err := zipBackend{}.list(context.Background(), "/a.zip")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !reflect.DeepEqual(gotArgs, []string{"-Z1", "/a.zip"}) {
		t.Fatalf("args = %v", gotArgs)
	}
	want := []entry{
		{Path: "readme.txt"},
		{Path: "sub", IsDir: true},
		{Path: "sub/data.bin"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries = %#v, want %#v", entries, want)
	}
}

func TestZipBackendExtractOne(t *testing.T) {
	var gotArgs []string
	withFakeTools(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return nil, nil, nil
	})
	if err := (zipBackend{}).extractOne(context.Background(), "/a.zip", "/dest", "sub/data.bin"); err != nil {
		t.Fatalf("extractOne: %v", err)
	}
	want := []string{"-o", "-q", "/a.zip", "sub/data.bin", "-d", "/dest"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
}

func TestZipBackendExtractAll(t *testing.T) {
	var gotArgs []string
	withFakeTools(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return nil, nil, nil
	})
	if err := (zipBackend{}).extractAll(context.Background(), "/a.zip", "/dest"); err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	want := []string{"-o", "-q", "/a.zip", "-d", "/dest"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
}
