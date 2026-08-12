package fusefs

import (
	"context"
	"errors"
	"hash/fnv"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/unxed/f4/vfs"
)

const (
	defaultDirCacheTTL = 5 * time.Second
	spoolChunk         = 256 * 1024
)

// errClosed is returned once the mount has released its VFS.
var errClosed = errors.New("mount is closed")

// bridge owns one vfs.VFS and turns path-based FUSE requests into calls on
// it. It contains no FUSE types, so the whole VFS side of a mount compiles
// and can be tested on platforms that cannot mount anything.
//
// Every VFS call happens under mu. f4's VFS implementations are stateful
// and were written for a single UI thread, while FUSE serves requests from
// many kernel threads at once; serializing is the only assumption that
// holds for every backend. It also means one slow read stalls the mount,
// which is the main thing later iterations should improve.
type bridge struct {
	mu     sync.Mutex
	v      vfs.VFS
	closed bool

	root     string
	randomOK bool
	// readOnly is the mount's mode, not the backend's. Until iteration 4
	// every mount sets it, but the write side has to ask a fact rather than
	// a constant, or turning writes on means hunting for hardcoded EROFS.
	readOnly bool
	// writeOK is what the backend says about itself, kept next to readOnly
	// so the two reasons a write can be refused stay distinguishable.
	writeOK  bool
	cacheTTL time.Duration
	cacheMu  sync.Mutex
	dirCache map[string]dirCacheEntry
}

type dirCacheEntry struct {
	items []vfs.VFSItem
	at    time.Time
}

func newBridge(v vfs.VFS, root string, opts Options) *bridge {
	ttl := opts.DirCacheTTL
	if ttl <= 0 {
		ttl = defaultDirCacheTTL
	}
	return &bridge{
		v:        v,
		root:     root,
		randomOK: v.GetCapabilities().HasRandomAccess,
		readOnly: opts.ReadOnly,
		writeOK:  v.GetCapabilities().HasWrite,
		cacheTTL: ttl,
		dirCache: make(map[string]dirCacheEntry),
	}
}

// close releases the VFS. Open file handles keep their own spooled copies,
// so they stay readable until the kernel releases them.
func (b *bridge) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	v := b.v
	b.mu.Unlock()

	if v != nil {
		v.Close()
	}
	b.cacheMu.Lock()
	b.dirCache = make(map[string]dirCacheEntry)
	b.cacheMu.Unlock()
}

// join maps a name inside dirPath to a VFS-native path. Paths are whatever
// the backend uses; only the VFS knows how to build them.
func (b *bridge) join(dirPath, name string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return path.Join(dirPath, name)
	}
	return b.v.Join(dirPath, name)
}

func (b *bridge) readDir(ctx context.Context, dirPath string) ([]vfs.VFSItem, error) {
	if items, ok := b.cachedDir(dirPath); ok {
		return items, nil
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errClosed
	}
	var items []vfs.VFSItem
	err := b.v.ReadDir(ctx, dirPath, func(chunk []vfs.VFSItem) {
		items = append(items, chunk...)
	})
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}

	filtered := items[:0]
	for _, item := range items {
		if item.Name == "" || item.Name == "." || item.Name == ".." {
			continue
		}
		filtered = append(filtered, item)
	}
	items = filtered

	b.cacheMu.Lock()
	b.dirCache[dirPath] = dirCacheEntry{items: items, at: time.Now()}
	b.cacheMu.Unlock()
	return items, nil
}

func (b *bridge) cachedDir(dirPath string) ([]vfs.VFSItem, bool) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	entry, ok := b.dirCache[dirPath]
	if !ok || time.Since(entry.at) > b.cacheTTL {
		return nil, false
	}
	return entry.items, true
}

func (b *bridge) invalidate(dirPath string) {
	b.cacheMu.Lock()
	delete(b.dirCache, dirPath)
	b.cacheMu.Unlock()
}

// stat answers about one path. The root of a mount is reported as a
// directory even when the backend cannot stat it: a VFS that only lists is
// still perfectly mountable.
func (b *bridge) stat(ctx context.Context, itemPath string) (vfs.VFSItem, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return vfs.VFSItem{}, errClosed
	}
	item, err := b.v.Stat(ctx, itemPath)
	b.mu.Unlock()
	if err == nil {
		return item, nil
	}
	if itemPath == b.root {
		return vfs.VFSItem{Name: displayName(itemPath), IsDir: true, MTime: time.Now()}, nil
	}
	return vfs.VFSItem{}, err
}

// lookup resolves one name inside a directory, preferring the listing the
// kernel just walked over a fresh per-name round trip.
// mkdir is the first write the bridge does. It drops the parent's cached
// listing on the way out: without that, `mkdir x && ls` would not show x for
// as long as the cache lives, which reads as the mount having ignored the
// command.
func (b *bridge) mkdir(ctx context.Context, dirPath, name string) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errClosed
	}
	err := b.v.MkDir(ctx, b.join(dirPath, name))
	b.mu.Unlock()
	b.invalidate(dirPath)
	return err
}

// remove deletes one entry. Like mkdir it drops the parent's cached listing,
// so a file the kernel just unlinked stops being offered by the next ls.
func (b *bridge) remove(ctx context.Context, dirPath, name string) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errClosed
	}
	err := b.v.Remove(ctx, b.join(dirPath, name))
	b.mu.Unlock()
	b.invalidate(dirPath)
	return err
}

func (b *bridge) lookup(ctx context.Context, dirPath, name string) (vfs.VFSItem, error) {
	if items, ok := b.cachedDir(dirPath); ok {
		for _, item := range items {
			if item.Name == name {
				return item, nil
			}
		}
		return vfs.VFSItem{}, os.ErrNotExist
	}

	child := b.join(dirPath, name)
	if item, err := b.stat(ctx, child); err == nil {
		if item.Name == "" {
			item.Name = name
		}
		return item, nil
	}

	items, err := b.readDir(ctx, dirPath)
	if err != nil {
		return vfs.VFSItem{}, err
	}
	for _, item := range items {
		if item.Name == name {
			return item, nil
		}
	}
	return vfs.VFSItem{}, os.ErrNotExist
}

// handle is one open file. Backends without random access are spooled to a
// temporary file once, so that a mount never turns a seek into a re-read of
// the whole object.
type handle struct {
	b    *bridge
	mu   sync.Mutex
	r    vfs.ReadAtCloser
	tmp  *os.File
	path string
	size int64
	done bool
}

func (b *bridge) open(ctx context.Context, itemPath string, size int64) (*handle, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errClosed
	}
	reader, err := b.v.Open(ctx, itemPath)
	random := b.randomOK
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}

	h := &handle{b: b, r: reader, path: itemPath, size: size}
	if reader.Size() > 0 {
		h.size = reader.Size()
	}
	if random {
		return h, nil
	}

	if err := h.spool(ctx); err != nil {
		h.release()
		return nil, err
	}
	return h, nil
}

// spool copies a sequential-only source into a temporary file. It holds the
// bridge lock for the whole transfer because the VFS cannot serve anything
// else meanwhile anyway.
func (h *handle) spool(ctx context.Context) error {
	tmp, err := os.CreateTemp("", "f4-fuse-*")
	if err != nil {
		return err
	}
	os.Remove(tmp.Name())

	h.b.mu.Lock()
	defer h.b.mu.Unlock()
	if h.b.closed {
		tmp.Close()
		return errClosed
	}

	buf := make([]byte, spoolChunk)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			tmp.Close()
			return err
		}
		n, readErr := h.r.Read(ctx, buf)
		if n > 0 {
			if _, err := tmp.Write(buf[:n]); err != nil {
				tmp.Close()
				return err
			}
			total += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			tmp.Close()
			return readErr
		}
		if n == 0 {
			break
		}
	}

	h.tmp = tmp
	h.size = total

	// The source has been consumed to the end; releasing it now lets the
	// backend drop its own temporary state while the mount keeps serving.
	if h.r != nil {
		h.r.Close()
		h.r = nil
	}
	return nil
}

// readAt fills dest and returns the number of bytes read. A short read at
// the end of the file is success, not an error: FUSE has no EOF.
func (h *handle) readAt(ctx context.Context, dest []byte, off int64) (int, error) {
	h.mu.Lock()
	tmp := h.tmp
	reader := h.r
	closed := h.done
	h.mu.Unlock()
	if closed {
		return 0, errClosed
	}

	if tmp != nil {
		n, err := tmp.ReadAt(dest, off)
		if err == io.EOF {
			err = nil
		}
		return n, err
	}

	h.b.mu.Lock()
	defer h.b.mu.Unlock()
	if h.b.closed {
		return 0, errClosed
	}
	n, err := reader.ReadAt(ctx, dest, off)
	if err == io.EOF {
		err = nil
	}
	return n, err
}

func (h *handle) release() error {
	h.mu.Lock()
	if h.done {
		h.mu.Unlock()
		return nil
	}
	h.done = true
	reader, tmp := h.r, h.tmp
	h.r, h.tmp = nil, nil
	h.mu.Unlock()

	if tmp != nil {
		tmp.Close()
	}
	if reader != nil {
		h.b.mu.Lock()
		if !h.b.closed {
			reader.Close()
		}
		h.b.mu.Unlock()
	}
	return nil
}

// inodeOf derives a stable inode number from a path. Real inode numbers are
// not available from most backends, and the kernel only needs uniqueness
// among live objects.
func inodeOf(itemPath string) uint64 {
	sum := fnv.New64a()
	sum.Write([]byte(itemPath))
	ino := sum.Sum64()
	switch ino {
	case 0, 1, ^uint64(0):
		return 2
	}
	return ino
}

// unixMode turns VFS metadata into a POSIX mode. Backends that carry real
// permissions win; the rest get sane read-only defaults.
func unixMode(item vfs.VFSItem) uint32 {
	if item.UnixMode != 0 {
		return item.UnixMode
	}
	if item.IsDir {
		return 0o555
	}
	if item.IsExecutable {
		return 0o555
	}
	return 0o444
}

// displayName strips a directory prefix a backend may have included.
func displayName(name string) string {
	name = strings.TrimSuffix(name, "/")
	if idx := strings.LastIndexAny(name, "/\\"); idx >= 0 {
		return name[idx+1:]
	}
	return name
}
