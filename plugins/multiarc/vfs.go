package multiarc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/unxed/f4/vfs"
)

// errReadOnly is what every mutation answers with: multiarc only lists and
// extracts, the same scope far2l's multiarc plugin has, so there is nothing
// backing a write.
var errReadOnly = errors.New("multiarc: read-only archive")

// multiArcState is the archive's listing, built once and shared by every
// clone of the MultiArcVFS that opened it (Clone is what a second panel or
// a background copy gets), and the registry of temp directories Open
// extracted into. The directories are not removed until the plugin itself
// closes (see multiarc.go): a clone or a viewer/editor session can still be
// reading an extracted file when another clone's ReadDir runs, and nothing
// here tracks reference counts to make an earlier cleanup safe.
type multiArcState struct {
	once     sync.Once
	listErr  error
	mu       sync.Mutex
	entries  map[string]entry           // full member path -> entry, no leading slash
	children map[string]map[string]bool // dir path ("" = root) -> immediate child names
}

func (st *multiArcState) ensureListed(ctx context.Context, b backend, localPath string) error {
	st.once.Do(func() {
		raw, err := b.list(ctx, localPath)
		if err != nil {
			st.listErr = err
			return
		}
		st.build(raw)
	})
	return st.listErr
}

func (st *multiArcState) build(raw []entry) {
	entries := map[string]entry{}
	children := map[string]map[string]bool{"": {}}
	ensureDir := func(p string) {
		if _, ok := children[p]; !ok {
			children[p] = map[string]bool{}
		}
	}
	addChild := func(parent, name string) {
		ensureDir(parent)
		children[parent][name] = true
	}
	for _, e := range raw {
		p := strings.Trim(strings.ReplaceAll(e.Path, "\\", "/"), "/")
		if p == "" {
			continue
		}
		parts := strings.Split(p, "/")
		for i, part := range parts {
			parentDir := strings.Join(parts[:i], "/")
			addChild(parentDir, part)
			if i < len(parts)-1 {
				ensureDir(strings.Join(parts[:i+1], "/"))
			}
		}
		if e.IsDir {
			ensureDir(p)
		}
		entries[p] = entry{Path: p, IsDir: e.IsDir, Size: e.Size, SizeKnown: e.SizeKnown, MTime: e.MTime}
	}
	st.mu.Lock()
	st.entries = entries
	st.children = children
	st.mu.Unlock()
}

// MultiArcVFS is a read-only vfs.VFS over one archive, browsed and
// extracted through backend rather than a native decoder. It is the lite
// build's stand-in for plugins/archive's ArchiveVFS (f4#1178, part 2).
type MultiArcVFS struct {
	parent      vfs.VFS
	localPath   string // absolute path of the archive on the local disk
	displayName string
	backend     backend
	backendID   string
	state       *multiArcState

	path string // current directory inside the archive, "/"-rooted
}

// NewMultiArcVFS opens localPath (an archive already known to sit on the
// local disk -- see Provider.CanOpen) for browsing through b.
func NewMultiArcVFS(parent vfs.VFS, localPath, displayName string, b backend, backendID string) *MultiArcVFS {
	return &MultiArcVFS{
		parent:      parent,
		localPath:   localPath,
		displayName: displayName,
		backend:     b,
		backendID:   backendID,
		state:       &multiArcState{},
		path:        "/",
	}
}

func (v *MultiArcVFS) GetTitle() string { return v.displayName }

func (v *MultiArcVFS) PanelTitle(p string) string {
	key := v.key(p)
	if key == "" {
		return "MultiArc:" + v.backendID + ":" + v.displayName
	}
	return "MultiArc:" + v.backendID + ":" + v.displayName + "/" + key
}

func (v *MultiArcVFS) IsAtRoot() bool             { return v.path == "" || v.path == "/" }
func (v *MultiArcVFS) GetPath() string            { return v.path }
func (v *MultiArcVFS) IsAbs(p string) bool        { return path.IsAbs(p) }
func (v *MultiArcVFS) Join(elem ...string) string { return path.Join(elem...) }
func (v *MultiArcVFS) Base(p string) string       { return path.Base(p) }
func (v *MultiArcVFS) Dir(p string) string        { return path.Dir(p) }

func (v *MultiArcVFS) abs(p string) string {
	if p == "" {
		return v.path
	}
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(v.path, p)
}

// key is abs(p) with the leading slash trimmed, which is how entries and
// children are keyed ("" for the archive root).
func (v *MultiArcVFS) key(p string) string {
	return strings.Trim(v.abs(p), "/")
}

func (v *MultiArcVFS) Abs(p string) (string, error) { return v.abs(p), nil }

func (v *MultiArcVFS) SetPath(p string) error {
	if err := v.state.ensureListed(context.Background(), v.backend, v.localPath); err != nil {
		return err
	}
	key := v.key(p)
	v.state.mu.Lock()
	_, ok := v.state.children[key]
	v.state.mu.Unlock()
	if !ok {
		return os.ErrInvalid
	}
	if key == "" {
		v.path = "/"
	} else {
		v.path = "/" + key
	}
	return nil
}

func (v *MultiArcVFS) ReadDir(ctx context.Context, p string, onChunk func([]vfs.VFSItem)) error {
	if err := v.state.ensureListed(ctx, v.backend, v.localPath); err != nil {
		return err
	}
	dir := v.key(p)
	v.state.mu.Lock()
	children, ok := v.state.children[dir]
	if !ok {
		v.state.mu.Unlock()
		return fmt.Errorf("multiarc: no such directory: %q", p)
	}
	items := make([]vfs.VFSItem, 0, len(children))
	for name := range children {
		full := name
		if dir != "" {
			full = dir + "/" + name
		}
		items = append(items, v.itemFor(name, full))
	}
	v.state.mu.Unlock()
	if onChunk != nil {
		onChunk(items)
	}
	return nil
}

// itemFor builds a VFSItem for full (already known to be a child of some
// listed directory). The caller holds v.state.mu.
func (v *MultiArcVFS) itemFor(name, full string) vfs.VFSItem {
	item := vfs.VFSItem{Name: name}
	if info, known := v.state.entries[full]; known {
		item.IsDir = info.IsDir
		item.Size = info.Size
		item.SizeKnown = info.SizeKnown
		item.MTime = info.MTime
		if !info.MTime.IsZero() {
			item.KnownMetadata |= vfs.MetadataMTime
		}
	}
	if !item.IsDir {
		if _, isDir := v.state.children[full]; isDir {
			// A directory synthesized from its children's paths: no explicit
			// entry for it exists in the archive (common for zip archives
			// built without directory entries), but something is in it.
			item.IsDir = true
		}
	}
	return item
}

func (v *MultiArcVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	if err := v.state.ensureListed(ctx, v.backend, v.localPath); err != nil {
		return vfs.VFSItem{}, err
	}
	key := v.key(p)
	name := path.Base("/" + key)
	if key == "" {
		name = v.displayName
	}
	v.state.mu.Lock()
	defer v.state.mu.Unlock()
	if info, ok := v.state.entries[key]; ok {
		return vfs.VFSItem{
			Name: name, IsDir: info.IsDir, Size: info.Size,
			SizeKnown: info.SizeKnown, MTime: info.MTime,
		}, nil
	}
	if _, ok := v.state.children[key]; ok {
		return vfs.VFSItem{Name: name, IsDir: true}, nil
	}
	return vfs.VFSItem{}, os.ErrNotExist
}

func (v *MultiArcVFS) MkDir(context.Context, string) error                      { return errReadOnly }
func (v *MultiArcVFS) Remove(context.Context, string) error                     { return errReadOnly }
func (v *MultiArcVFS) Rename(context.Context, string, string) error             { return errReadOnly }
func (v *MultiArcVFS) SetAttributes(context.Context, string, vfs.VFSItem) error { return errReadOnly }

func (v *MultiArcVFS) GetCapabilities() vfs.VFSCapabilities {
	return vfs.VFSCapabilities{HasRandomAccess: true}
}

func (v *MultiArcVFS) Search(context.Context, string, string) (chan int64, error) {
	return nil, nil
}

// extractedFile is a plain *os.File opened on a member extractOne staged
// into a temp directory, adapted to vfs.ReadAtCloser's context-aware
// signature.
type extractedFile struct {
	f    *os.File
	size int64
}

func (e *extractedFile) Size() int64 { return e.size }

func (e *extractedFile) Read(ctx context.Context, p []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return e.f.Read(p)
}

func (e *extractedFile) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return e.f.ReadAt(p, off)
}

func (e *extractedFile) Close() error { return e.f.Close() }

func (v *MultiArcVFS) Open(ctx context.Context, p string) (vfs.ReadAtCloser, error) {
	if err := v.state.ensureListed(ctx, v.backend, v.localPath); err != nil {
		return nil, err
	}
	key := v.key(p)
	v.state.mu.Lock()
	info, known := v.state.entries[key]
	v.state.mu.Unlock()
	if !known || info.IsDir {
		return nil, fmt.Errorf("multiarc: %q is not a file in this archive", p)
	}
	dir, err := os.MkdirTemp("", "f4-multiarc-")
	if err != nil {
		return nil, err
	}
	registerTempDir(dir)
	if err := v.backend.extractOne(ctx, v.localPath, dir, key); err != nil {
		return nil, err
	}
	extractedPath := filepath.Join(dir, filepath.FromSlash(key))
	f, err := os.Open(extractedPath)
	if err != nil {
		return nil, fmt.Errorf("multiarc: extracted member missing at %s: %w", extractedPath, err)
	}
	size := info.Size
	if fi, err := f.Stat(); err == nil {
		size = fi.Size()
	}
	return &extractedFile{f: f, size: size}, nil
}

func (v *MultiArcVFS) Create(context.Context, string) (io.WriteCloser, error) {
	return nil, errReadOnly
}

func (v *MultiArcVFS) ParentVFS() vfs.VFS { return v.parent }

func (v *MultiArcVFS) Clone() vfs.VFS {
	return &MultiArcVFS{
		parent: v.parent, localPath: v.localPath, displayName: v.displayName,
		backend: v.backend, backendID: v.backendID, state: v.state, path: v.path,
	}
}

func (v *MultiArcVFS) Close() error { return nil }

var (
	_ vfs.VFS                = (*MultiArcVFS)(nil)
	_ vfs.TitleProvider      = (*MultiArcVFS)(nil)
	_ vfs.PanelTitleProvider = (*MultiArcVFS)(nil)
)
