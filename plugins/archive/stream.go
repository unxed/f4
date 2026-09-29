package archive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"

	"github.com/unxed/archives"
	"github.com/unxed/f4/vfs"
	zipperarchive "github.com/unxed/zipper/archive"
)

func withStreamRead(ctx context.Context) context.Context {
	return vfs.WithStreamRead(ctx)
}

func streamReadRequested(ctx context.Context) bool {
	return vfs.StreamReadRequested(ctx)
}

// hasVirtualParent is kept defensive because a few test and plugin VFS
// implementations embed a nil vfs.VFS while overriding only the operations
// they need. Calling the promoted ParentVFS method on those values panics.
func hasVirtualParent(parent vfs.VFS) (virtual bool) {
	if parent == nil {
		return false
	}
	if _, local := parent.(*vfs.OSVFS); local {
		return false
	}
	defer func() {
		if recover() != nil {
			virtual = false
		}
	}()
	return parent.ParentVFS() != nil
}

// streamReaderAtSeeker turns the VFS contract into the contract used by the
// generic archives package. Reads are delegated to the parent, so no source
// archive is copied to a temporary file. Sequential reads are serialized to
// preserve the adapter's current offset; ReadAt remains independent.
type streamReaderAtSeeker struct {
	source vfs.ReadAtCloser

	mu     sync.Mutex
	offset int64
	ctx    context.Context

	readMu sync.Mutex
}

func (r *streamReaderAtSeeker) context() context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx == nil {
		return context.Background()
	}
	return r.ctx
}

func (r *streamReaderAtSeeker) setContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()
}

func (r *streamReaderAtSeeker) Read(p []byte) (int, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()

	ctx := r.context()
	r.mu.Lock()
	offset := r.offset
	r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// ReadAt is the primitive that carries an explicit offset through the VFS
	// stack. Using the source's sequential Read here would make Seek update
	// only this adapter while leaving the parent member at its old position.
	n, err := r.source.ReadAt(ctx, p, offset)
	if n > 0 {
		r.mu.Lock()
		r.offset = offset + int64(n)
		r.mu.Unlock()
	}
	return n, err
}

func (r *streamReaderAtSeeker) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, fmt.Errorf("negative read offset %d", offset)
	}
	ctx := r.context()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.ReadAt(ctx, p, offset)
}

func (r *streamReaderAtSeeker) Seek(offset int64, whence int) (int64, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	var base int64
	switch whence {
	case io.SeekStart:
		base = 0
	case io.SeekCurrent:
		base = r.offset
	case io.SeekEnd:
		if r.source.Size() < 0 {
			return 0, errors.New("archive source size is unknown")
		}
		base = r.source.Size()
	default:
		return 0, fmt.Errorf("invalid seek mode %d", whence)
	}
	next := base + offset
	if next < 0 {
		return 0, errors.New("negative archive source position")
	}
	r.offset = next
	return next, nil
}

func (r *streamReaderAtSeeker) Size() int64 { return r.source.Size() }

// streamArchiveFS owns the parent member handle for as long as the generic
// archive filesystem is alive. Its Close path is what releases a nested
// provider's source and cancellation chain.
type streamArchiveFS struct {
	fsys   fs.FS
	source *streamReaderAtSeeker
	cancel context.CancelFunc

	closeOnce sync.Once
	closeErr  error
}

func (s *streamArchiveFS) Open(name string) (fs.File, error) { return s.fsys.Open(name) }

func (s *streamArchiveFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(s.fsys, name)
}

func (s *streamArchiveFS) Stat(name string) (fs.FileInfo, error) {
	return fs.Stat(s.fsys, name)
}

func (s *streamArchiveFS) setContext(ctx context.Context) { s.source.setContext(ctx) }

func (s *streamArchiveFS) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.closeErr = s.source.source.Close()
	})
	return s.closeErr
}

func streamArchiveKind(format archives.Format) string {
	if format == nil {
		return ""
	}
	extension := strings.ToLower(format.Extension())
	switch {
	case strings.Contains(extension, "zip"):
		return "zip"
	case strings.Contains(extension, "tar"):
		return "tar"
	default:
		return "fallback"
	}
}

// openReaderBackedArchiveFS is the generic composition point for nested
// providers. It intentionally accepts the archives package's registered
// extraction formats instead of branching on zip/tar pairs here. If a format
// cannot be represented by that reader-backed API (for example an encrypted
// backend or an implementation requiring a filename), the caller falls back
// to the existing materialization path.
func openReaderBackedArchiveFS(ctx context.Context, parent vfs.VFS, archivePath, displayName string) (zipperarchive.FileSystem, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	source, err := parent.Open(withStreamRead(ctx), archivePath)
	if err != nil {
		return nil, "", err
	}
	if source.Size() < 0 {
		_ = source.Close()
		return nil, "", errors.New("reader-backed archive requires a known source size")
	}

	lifetimeCtx, cancel := context.WithCancel(context.Background())
	reader := &streamReaderAtSeeker{source: source, ctx: ctx}
	format, _, err := archives.Identify(ctx, displayName, reader)
	if err != nil {
		cancel()
		_ = source.Close()
		return nil, "", fmt.Errorf("identify reader-backed archive: %w", err)
	}
	if _, ok := format.(archives.Extraction); !ok {
		cancel()
		_ = source.Close()
		return nil, "", fmt.Errorf("reader-backed source format %q is not an archive extractor", format.Extension())
	}

	fsys, err := archives.FileSystem(lifetimeCtx, displayName, reader)
	if err != nil {
		cancel()
		_ = source.Close()
		return nil, "", fmt.Errorf("open reader-backed archive: %w", err)
	}
	reader.setContext(lifetimeCtx)
	return &streamArchiveFS{fsys: fsys, source: reader, cancel: cancel}, streamArchiveKind(format), nil
}
