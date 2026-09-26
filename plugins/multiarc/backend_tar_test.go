package multiarc

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestTarBackendList(t *testing.T) {
	var gotName string
	var gotArgs []string
	withFakeTools(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotName, gotArgs = name, args
		return []byte("dir/\ndir/file.txt\ntop.txt\n"), nil, nil
	})

	entries, err := tarBackend{}.list(context.Background(), "/archives/x.tar.gz")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if gotName != "tar" || !reflect.DeepEqual(gotArgs, []string{"-tf", "/archives/x.tar.gz"}) {
		t.Fatalf("unexpected command: %s %v", gotName, gotArgs)
	}
	want := []entry{
		{Path: "dir", IsDir: true},
		{Path: "dir/file.txt"},
		{Path: "top.txt"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries = %#v, want %#v", entries, want)
	}
}

func TestTarBackendListError(t *testing.T) {
	withFakeTools(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		return nil, []byte("tar: short read"), errors.New("exit status 2")
	})
	if _, err := (tarBackend{}).list(context.Background(), "/x.tar"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestTarBackendExtractAll(t *testing.T) {
	var gotArgs []string
	withFakeTools(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return nil, nil, nil
	})
	if err := (tarBackend{}).extractAll(context.Background(), "/a.tar", "/dest"); err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	want := []string{"-xf", "/a.tar", "-C", "/dest"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
}

func TestTarBackendExtractOne(t *testing.T) {
	var gotArgs []string
	withFakeTools(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return nil, nil, nil
	})
	if err := (tarBackend{}).extractOne(context.Background(), "/a.tar", "/dest", "dir/file.txt"); err != nil {
		t.Fatalf("extractOne: %v", err)
	}
	want := []string{"-xf", "/a.tar", "-C", "/dest", "--", "dir/file.txt"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
}

func TestTarBackendExtractOneNeedsMember(t *testing.T) {
	if err := (tarBackend{}).extractOne(context.Background(), "/a.tar", "/dest", ""); err == nil {
		t.Fatal("expected an error for an empty member")
	}
}
