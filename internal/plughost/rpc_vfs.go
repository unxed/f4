package plughost

import (
	"context"
	"io"
	"path"
	"path/filepath"
	"sync"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
)

// RPCVFS acts as a local proxy that forwards VFS calls to the plugin process over RPC.
type RPCVFS struct {
	sess      PluginTransport
	driveName string
	path      string
}

type rpcFileWrapper struct {
	sess PluginTransport
	id   uint32
	size int64
	mu   sync.Mutex
	off  int64
}

func (w *rpcFileWrapper) Size() int64 { return w.size }
func (w *rpcFileWrapper) Read(ctx context.Context, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.ReadAt(ctx, p, w.off)
	w.off += int64(n)
	if n > 0 && err == io.EOF {
		return n, nil
	}
	return n, err
}
func (w *rpcFileWrapper) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	req := ReadAtReq{ID: w.id, Len: len(p), Off: off}
	var data []byte
	err := w.sess.Call("VFS.ReadAt", req, &data)
	if len(data) > len(p) {
		data = data[:len(p)]
	}
	if len(data) > 0 {
		copy(p, data)
	}
	if err != nil {
		return len(data), err
	}
	if len(data) < len(p) {
		return len(data), io.EOF
	}
	return len(data), nil
}
func (w *rpcFileWrapper) Close() error {
	req := CloseReq{ID: w.id}
	return w.sess.Call("VFS.CloseFile", req, nil)
}

type rpcWriteWrapper struct {
	sess PluginTransport
	id   uint32
}

func (w *rpcWriteWrapper) Write(p []byte) (int, error) {
	req := WriteReq{ID: w.id, Data: p}
	err := w.sess.Call("VFS.Write", req, nil)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *rpcWriteWrapper) Close() error {
	req := CloseReq{ID: w.id}
	return w.sess.Call("VFS.CloseFile", req, nil)
}

func NewRPCVFS(sess PluginTransport, driveName string) *RPCVFS {
	return &RPCVFS{
		sess:      sess,
		driveName: driveName,
		path:      "/",
	}
}

func (v *RPCVFS) IsAtRoot() bool {
	return v.path == "/" || v.path == ""
}

func (v *RPCVFS) SetPath(p string) error {
	v.path = filepath.ToSlash(filepath.Clean(p))
	return nil
}

func (v *RPCVFS) GetPath() string {
	return filepath.FromSlash(v.path)
}
func (v *RPCVFS) IsAbs(p string) bool { return path.IsAbs(p) }

func (v *RPCVFS) pathForRPC(p string) string {
	abs, err := v.Abs(p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(abs)
}

func (v *RPCVFS) Join(e ...string) string {
	return filepath.Join(e...)
}

func (v *RPCVFS) Abs(p string) (string, error) {
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Join(filepath.FromSlash(v.path), p), nil
}

func (v *RPCVFS) Base(p string) string {
	return filepath.Base(p)
}

func (v *RPCVFS) Dir(p string) string {
	return filepath.Dir(p)
}

func (v *RPCVFS) ReadDir(ctx context.Context, path string, onChunk func([]vfs.VFSItem)) error {
	var items []vfs.VFSItem
	req := map[string]string{"Drive": v.driveName, "Path": v.pathForRPC(path)}
	err := v.sess.Call("VFS.ReadDir", req, &items)
	if err == nil && len(items) > 0 {
		onChunk(items)
	}
	return err
}

func (v *RPCVFS) Stat(ctx context.Context, path string) (vfs.VFSItem, error) {
	var item vfs.VFSItem
	path = v.pathForRPC(path)
	// Provide a fallback dummy response for the root itself if the plugin doesn't handle it well
	if path == "/" || path == "" {
		return vfs.VFSItem{Name: v.driveName, IsDir: true}, nil
	}
	req := map[string]string{"Drive": v.driveName, "Path": path}
	err := v.sess.Call("VFS.Stat", req, &item)
	return item, err
}

func (v *RPCVFS) MkDir(ctx context.Context, p string) error {
	req := MkDirReq{Drive: v.driveName, Path: v.pathForRPC(p)}
	return v.sess.Call("VFS.MkDir", req, nil)
}

func (v *RPCVFS) Remove(ctx context.Context, p string) error {
	req := RemoveReq{Drive: v.driveName, Path: v.pathForRPC(p)}
	return v.sess.Call("VFS.Remove", req, nil)
}

func (v *RPCVFS) Rename(ctx context.Context, old, new string) error {
	req := RenameReq{Drive: v.driveName, Old: v.pathForRPC(old), New: v.pathForRPC(new)}
	return v.sess.Call("VFS.Rename", req, nil)
}
func (v *RPCVFS) SetAttributes(ctx context.Context, path string, item vfs.VFSItem) error {
	req := SetAttrReq{Drive: v.driveName, Path: v.pathForRPC(path), Item: item}
	return v.sess.Call("VFS.SetAttributes", req, nil)
}

func (v *RPCVFS) GetCapabilities() vfs.VFSCapabilities {
	return vfs.VFSCapabilities{}
}

func (v *RPCVFS) Search(ctx context.Context, p, pat string) (chan int64, error) {
	return nil, nil
}

func (v *RPCVFS) Open(ctx context.Context, p string) (vfs.ReadAtCloser, error) {
	req := OpenReq{Drive: v.driveName, Path: v.pathForRPC(p)}
	var res OpenRes
	err := v.sess.Call("VFS.Open", req, &res)
	if err != nil {
		return nil, err
	}
	return &rpcFileWrapper{sess: v.sess, id: res.ID, size: res.Size}, nil
}

func (v *RPCVFS) Create(ctx context.Context, p string) (io.WriteCloser, error) {
	req := OpenReq{Drive: v.driveName, Path: v.pathForRPC(p)}
	var res OpenRes
	err := v.sess.Call("VFS.Create", req, &res)
	if err != nil {
		return nil, err
	}
	return &rpcWriteWrapper{sess: v.sess, id: res.ID}, nil
}

func (v *RPCVFS) ParentVFS() vfs.VFS {
	return nil
}

func (v *RPCVFS) Close() error {
	return nil
}

func (v *RPCVFS) Clone() vfs.VFS {
	clone := NewRPCVFS(v.sess, v.driveName)
	clone.path = v.path
	return clone
}

func (v *RPCVFS) ProcessPanelKey(app vfs.App, e *vtinput.InputEvent) bool {
	type PKReq struct {
		Drive string
		Event vtinput.InputEvent
	}
	var handled bool
	err := v.sess.Call("VFS.ProcessKey", PKReq{Drive: v.driveName, Event: *e}, &handled)
	if err != nil {
		return false
	}
	return handled
}
