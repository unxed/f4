package plughost

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
)

type rpcVFSTestTransport struct {
	calls   []string
	handler func(method string, params, result any) error
}

func (t *rpcVFSTestTransport) Call(method string, params, result any) error {
	t.calls = append(t.calls, method)
	if t.handler == nil {
		return nil
	}
	return t.handler(method, params, result)
}

func TestRPCVFSOperationsAndWrappers(t *testing.T) {
	transport := &rpcVFSTestTransport{}
	v := NewRPCVFS(transport, "drive")

	if !v.IsAtRoot() || !v.IsAbs("/absolute") || v.IsAbs("relative") {
		t.Fatal("initial root or absolute-path checks failed")
	}
	if err := v.SetPath("/folder/sub"); err != nil {
		t.Fatal(err)
	}
	if got := v.Join("folder", "file.txt"); got != filepath.Join("folder", "file.txt") {
		t.Fatalf("Join = %q", got)
	}
	if got := v.Dir("/folder/file.txt"); got != filepath.FromSlash("/folder") {
		t.Fatalf("Dir = %q", got)
	}
	if got, err := v.Abs("file.txt"); err != nil || got != filepath.FromSlash("/folder/sub/file.txt") {
		t.Fatalf("Abs relative = %q", got)
	}
	absolutePath := filepath.FromSlash("/root.txt")
	if runtime.GOOS == "windows" {
		absolutePath = `C:\root.txt`
	}
	if got, err := v.Abs(absolutePath); err != nil || got != absolutePath {
		t.Fatalf("Abs absolute = %q", got)
	}
	if got := v.Base("/folder/file.txt"); got != "file.txt" {
		t.Fatalf("Base = %q", got)
	}
	clone := v.Clone().(*RPCVFS)
	if clone.path != v.path || clone.driveName != v.driveName || clone == v {
		t.Fatalf("Clone = %+v, original = %+v", clone, v)
	}
	if v.ParentVFS() != nil || v.Close() != nil {
		t.Fatal("RPC VFS parent or close should be nil")
	}
	if ch, err := v.Search(context.Background(), "/", "*.txt"); ch != nil || err != nil {
		t.Fatalf("Search = %v, %v", ch, err)
	}
	_ = v.GetCapabilities()

	readDirCalls := 0
	readMode := "full"
	writeErr := error(nil)
	var openPath, createPath string
	transport.handler = func(method string, params, result any) error {
		switch method {
		case "VFS.ReadDir":
			readDirCalls++
			if readDirCalls == 1 {
				*(result.(*[]vfs.VFSItem)) = []vfs.VFSItem{{Name: "file.txt"}}
			}
		case "VFS.Stat":
			*(result.(*vfs.VFSItem)) = vfs.VFSItem{Name: "file.txt", Size: 7}
		case "VFS.Open":
			openPath = params.(OpenReq).Path
			*(result.(*OpenRes)) = OpenRes{ID: 7, Size: 3}
		case "VFS.ReadAt":
			req := params.(ReadAtReq)
			switch readMode {
			case "partial":
				*(result.(*[]byte)) = []byte("x")
			case "error":
				*(result.(*[]byte)) = []byte("x")
				return errors.New("read failed")
			default:
				if req.Len == 1 {
					*(result.(*[]byte)) = []byte("a")
				} else {
					*(result.(*[]byte)) = []byte("abc")
				}
			}
		case "VFS.Create":
			createPath = params.(OpenReq).Path
			*(result.(*OpenRes)) = OpenRes{ID: 8}
		case "VFS.Write":
			return writeErr
		case "VFS.ProcessKey":
			*(result.(*bool)) = true
		}
		return nil
	}

	var received []vfs.VFSItem
	if err := v.ReadDir(context.Background(), "/", func(items []vfs.VFSItem) { received = append(received, items...) }); err != nil {
		t.Fatalf("ReadDir = %v", err)
	}
	if len(received) != 1 || received[0].Name != "file.txt" {
		t.Fatalf("ReadDir items = %+v", received)
	}
	if err := v.ReadDir(context.Background(), "/empty", func(items []vfs.VFSItem) { t.Fatal("empty ReadDir called callback") }); err != nil {
		t.Fatalf("empty ReadDir = %v", err)
	}
	if item, err := v.Stat(context.Background(), "/"); err != nil || !item.IsDir || item.Name != "drive" {
		t.Fatalf("root Stat = %+v, %v", item, err)
	}
	if item, err := v.Stat(context.Background(), "/file.txt"); err != nil || item.Size != 7 {
		t.Fatalf("file Stat = %+v, %v", item, err)
	}

	if err := v.MkDir(context.Background(), "/new"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove(context.Background(), "/old"); err != nil {
		t.Fatal(err)
	}
	if err := v.Rename(context.Background(), "/old", "/new"); err != nil {
		t.Fatal(err)
	}
	if err := v.SetAttributes(context.Background(), "/new", vfs.VFSItem{Name: "new"}); err != nil {
		t.Fatal(err)
	}

	file, err := v.Open(context.Background(), "file.txt")
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	if file.Size() != 3 {
		t.Fatalf("file size = %d", file.Size())
	}
	first := make([]byte, 1)
	if n, err := file.Read(context.Background(), first); n != 1 || err != nil || string(first) != "a" {
		t.Fatalf("Read = %d, %v, %q", n, err, first)
	}
	buf := make([]byte, 3)
	if n, err := file.ReadAt(context.Background(), buf, 0); n != 3 || err != nil || string(buf) != "abc" {
		t.Fatalf("full ReadAt = %d, %v, %q", n, err, buf)
	}
	readMode = "partial"
	if n, err := file.ReadAt(context.Background(), buf, 0); n != 1 || !errors.Is(err, io.EOF) {
		t.Fatalf("partial ReadAt = %d, %v", n, err)
	}
	readMode = "error"
	if n, err := file.ReadAt(context.Background(), buf, 0); n != 1 || err == nil || err.Error() != "read failed" {
		t.Fatalf("error ReadAt = %d, %v", n, err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	writer, err := v.Create(context.Background(), "created.txt")
	if err != nil {
		t.Fatalf("Create = %v", err)
	}
	if n, err := writer.Write([]byte("data")); n != 4 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	writeErr = errors.New("write failed")
	if n, err := writer.Write([]byte("data")); n != 0 || err == nil || err.Error() != "write failed" {
		t.Fatalf("failed Write = %d, %v", n, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if openPath != "/folder/sub/file.txt" || createPath != "/folder/sub/created.txt" {
		t.Fatalf("relative RPC paths = open %q, create %q", openPath, createPath)
	}

	if handled := v.ProcessPanelKey(nil, &vtinput.InputEvent{}); !handled {
		t.Fatal("ProcessPanelKey did not return handled response")
	}
	if len(transport.calls) == 0 {
		t.Fatal("transport was not called")
	}
}
