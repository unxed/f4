package main

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/unxed/f4/vfs"
)

type TerminalLogVFS struct {
	tv *TerminalView
	// fallback, when set, replaces tv as the data source entirely. Used for
	// ShellModeSimpleInline's no-PTY console view, where tv is never fed by
	// anything the user actually ran -- see readHostConsoleFullText.
	fallback func() []byte
}

func (v *TerminalLogVFS) IsAtRoot() bool            { return true }
func (v *TerminalLogVFS) GetPath() string           { return "term://" }
func (v *TerminalLogVFS) IsAbs(p string) bool       { return strings.HasPrefix(p, "term://") }
func (v *TerminalLogVFS) SetPath(path string) error { return nil }
func (v *TerminalLogVFS) ReadDir(ctx context.Context, path string, onChunk func([]vfs.VFSItem)) error {
	return nil
}
func (v *TerminalLogVFS) Stat(ctx context.Context, path string) (vfs.VFSItem, error) {
	return vfs.VFSItem{Name: "Terminal Log", IsDir: false}, nil
}
func (v *TerminalLogVFS) Join(elem ...string) string {
	if len(elem) == 0 {
		return ""
	}
	return elem[len(elem)-1]
}
func (v *TerminalLogVFS) Abs(path string) (string, error)               { return path, nil }
func (v *TerminalLogVFS) Base(path string) string                       { return path }
func (v *TerminalLogVFS) Dir(path string) string                        { return "term://" }
func (v *TerminalLogVFS) MkDir(ctx context.Context, path string) error  { return os.ErrPermission }
func (v *TerminalLogVFS) Remove(ctx context.Context, path string) error { return os.ErrPermission }
func (v *TerminalLogVFS) Rename(ctx context.Context, oldpath, newpath string) error {
	return os.ErrPermission
}
func (v *TerminalLogVFS) SetAttributes(ctx context.Context, path string, item vfs.VFSItem) error {
	return os.ErrPermission
}
func (v *TerminalLogVFS) GetCapabilities() vfs.VFSCapabilities {
	return vfs.VFSCapabilities{HasRandomAccess: true, HasUnixPermissions: false}
}
func (v *TerminalLogVFS) Search(ctx context.Context, path string, pattern string) (chan int64, error) {
	return nil, nil
}
func (v *TerminalLogVFS) Create(ctx context.Context, path string) (io.WriteCloser, error) {
	return nil, os.ErrPermission
}
func (v *TerminalLogVFS) ParentVFS() vfs.VFS { return nil }
func (v *TerminalLogVFS) Clone() vfs.VFS     { return v }
func (v *TerminalLogVFS) Close() error       { return nil }

func (v *TerminalLogVFS) Open(ctx context.Context, path string) (vfs.ReadAtCloser, error) {
	src := func() []byte { return v.tv.GetAllLogBytes() }
	if v.fallback != nil {
		src = v.fallback
	}
	return &terminalLogWrapper{data: src(), src: src}, nil
}

type terminalLogWrapper struct {
	// mu guards data, which RefreshSize replaces while the viewer's
	// background fetches are reading it.
	mu   sync.Mutex
	data []byte
	// src re-reads the log. The wrapper holds a snapshot so that offsets stay
	// put while the viewer pages around; this is how it asks for a newer one.
	src func() []byte
}

func (w *terminalLogWrapper) Size() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return int64(len(w.data))
}

// RefreshSize takes a fresh snapshot of the terminal log, so a viewer opened
// on it follows output that arrives after it was opened.
func (w *terminalLogWrapper) RefreshSize(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return w.Size(), err
	}
	if w.src == nil {
		return w.Size(), nil
	}
	next := w.src()
	w.mu.Lock()
	w.data = next
	size := int64(len(w.data))
	w.mu.Unlock()
	return size, nil
}

func (w *terminalLogWrapper) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if off >= int64(len(w.data)) {
		return 0, io.EOF
	}

	readLen := len(p)
	if off+int64(readLen) > int64(len(w.data)) {
		readLen = int(int64(len(w.data)) - off)
	}

	n := copy(p, w.data[off:off+int64(readLen)])

	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (w *terminalLogWrapper) Read(ctx context.Context, p []byte) (int, error) {
	return 0, io.EOF
}

func (w *terminalLogWrapper) Close() error { return nil }
