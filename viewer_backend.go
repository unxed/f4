package main

import (
	"context"
	"io"
	"sync"

	"github.com/unxed/f4/piecetable"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// ViewerBackend provides async random access to a file using small cache window.
type ViewerBackend struct {
	file vfs.ReadAtCloser
	size int64

	path    string
	owner   vfs.VFS
	indexer vfs.LineIndexer

	// totalLines is what the far side last reported, and totalForSize is the
	// file size it reported it for. A log file grows while it is being read,
	// so the total is only reused while the size it was counted at still
	// holds; anything else would put the viewer at the wrong offset.
	totalLines   int64
	totalForSize int64

	mu         sync.Mutex
	cacheOff   int64
	cacheData  []byte
	isFetching bool
	readErr    error

	ctx       context.Context
	cancelCtx context.CancelFunc
}

func NewViewerBackend(ctx context.Context, v vfs.VFS, path string) (*ViewerBackend, error) {
	f, err := v.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	bCtx, bCancel := context.WithCancel(context.Background())
	b := &ViewerBackend{
		file:         f,
		size:         f.Size(),
		path:         path,
		owner:        v,
		totalLines:   -1,
		totalForSize: -1,
		ctx:          bCtx,
		cancelCtx:    bCancel,
	}
	if indexer, ok := v.(vfs.LineIndexer); ok {
		b.indexer = indexer
	}
	return b, nil
}

func (b *ViewerBackend) Close() error {
	if b.cancelCtx != nil {
		b.cancelCtx()
	}
	return b.file.Close()
}

func (b *ViewerBackend) Size() int64 {
	if b.file != nil {
		newSize := b.file.Size()
		b.mu.Lock()
		b.size = newSize
		b.mu.Unlock()
	}
	return b.size
}

func (b *ViewerBackend) ReadAt(offset int64, length int) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if offset >= b.size {
		return nil, io.EOF
	}
	if offset+int64(length) > b.size {
		length = int(b.size - offset)
	}
	if b.readErr != nil {
		return nil, b.readErr
	}

	// Check cache hit
	if b.cacheData != nil && offset >= b.cacheOff && (offset+int64(length)) <= (b.cacheOff+int64(len(b.cacheData))) {
		start := offset - b.cacheOff
		return b.cacheData[start : start+int64(length)], nil
	}

	// Cache miss -> Trigger fetch in background
	if !b.isFetching {
		b.isFetching = true

		fetchOff := offset - 64*1024
		if fetchOff < 0 {
			fetchOff = 0
		}
		fetchLen := 256 * 1024 // We only keep 256KB in memory
		if fetchOff+int64(fetchLen) > b.size {
			fetchLen = int(b.size - fetchOff)
		}

		go func() {
			buf := make([]byte, fetchLen)
			n, err := b.file.ReadAt(b.ctx, buf, fetchOff)

			vtui.FrameManager.PostTask(func() {
				b.mu.Lock()
				if b.ctx.Err() == nil {
					if err == nil || err == io.EOF {
						b.cacheOff = fetchOff
						b.cacheData = buf[:n]
					} else {
						b.readErr = err
					}
				}
				b.isFetching = false
				b.mu.Unlock()
				vtui.FrameManager.Redraw()
			})
		}()
	}
	return nil, piecetable.ErrLoading
}

// SearchFrom asks the file system for the first occurrence of pattern at or
// after off. searched is false when the file system cannot answer, and the
// caller then scans the file itself; when it is true, an offset of -1 means
// the file was searched and the pattern is not in it.
//
// The difference matters most where it costs most: searching a remote file
// by reading it means downloading it, and a host that can grep its own copy
// answers in one round trip no matter how large the file is.
func (b *ViewerBackend) SearchFrom(ctx context.Context, pattern string, off int64) (int64, bool) {
	if b.owner == nil || pattern == "" || !b.owner.GetCapabilities().HasSearch {
		return 0, false
	}
	matches, err := b.owner.Search(ctx, b.path, pattern)
	if err != nil || matches == nil {
		return 0, false
	}
	for at := range matches {
		if at >= off {
			return at, true
		}
	}
	return -1, true
}

// SearchBefore asks a searchable file system for the last occurrence strictly
// before off. It mirrors SearchFrom for reverse repeat-search.
func (b *ViewerBackend) SearchBefore(ctx context.Context, pattern string, off int64) (int64, bool) {
	if b.owner == nil || pattern == "" || !b.owner.GetCapabilities().HasSearch {
		return 0, false
	}
	matches, err := b.owner.Search(ctx, b.path, pattern)
	if err != nil || matches == nil {
		return 0, false
	}
	last := int64(-1)
	for at := range matches {
		if at < off && at > last {
			last = at
		}
	}
	return last, true
}

// LineStart reports the byte offset where the given one-based line begins.
// A file system that can index lines answers it without moving the file; one
// that cannot is scanned here, which for a remote file means downloading it,
// so the caller runs this in a background task and lets the user cancel.
//
// The scan reads through the file handle rather than through ReadAt on
// purpose: the cache is a window meant for what is on screen, and pushing a
// whole file through it would evict exactly what the viewer is drawing.
func (b *ViewerBackend) LineStart(ctx context.Context, line int64) (int64, bool) {
	if line <= 1 {
		return 0, true
	}
	if b.indexer != nil {
		idx, err := b.indexer.LineIndex(ctx, b.path, line, 1)
		if err == nil {
			if len(idx.Offsets) > 0 {
				return idx.Offsets[0], true
			}
			// The far side counted the file and the line is not in it.
			if idx.Total >= 0 && line > idx.Total {
				return 0, false
			}
		}
	}

	size := b.Size()
	buf := make([]byte, 64*1024)
	remaining := line - 1
	var off int64
	for off < size {
		if ctx.Err() != nil {
			return 0, false
		}
		n, err := b.file.ReadAt(ctx, buf, off)
		for i := 0; i < n; i++ {
			if buf[i] != '\n' {
				continue
			}
			remaining--
			if remaining == 0 {
				start := off + int64(i) + 1
				if start >= size {
					// The newline that ends the file starts no line.
					return 0, false
				}
				return start, true
			}
		}
		off += int64(n)
		if err != nil {
			break
		}
	}
	return 0, false
}

// LineStartFromEnd reports where the last n lines of the file begin, asking
// the file system to do the counting. It answers false whenever that is not
// possible — a local file, a file system without the feature, a remote host
// without the tool for it, or any error at all — and the caller then falls
// back to reading, which is what it did before this existed.
//
// Two round trips at worst: one for the total, one for the offset. The total
// is kept, so paging around a file that is not growing costs one.
func (b *ViewerBackend) LineStartFromEnd(ctx context.Context, n int64) (int64, bool) {
	if b.indexer == nil || n <= 0 {
		return 0, false
	}
	size := b.Size()

	b.mu.Lock()
	total := b.totalLines
	known := total >= 0 && b.totalForSize == size
	b.mu.Unlock()

	if !known {
		idx, err := b.indexer.LineIndex(ctx, b.path, 1, 0)
		if err != nil || idx.Total < 0 {
			return 0, false
		}
		total = idx.Total
		b.mu.Lock()
		b.totalLines = total
		b.totalForSize = size
		b.mu.Unlock()
	}
	if total <= 0 {
		return 0, false
	}

	first := total - n + 1
	if first < 1 {
		first = 1
	}
	idx, err := b.indexer.LineIndex(ctx, b.path, first, 1)
	if err != nil || len(idx.Offsets) == 0 {
		return 0, false
	}
	return idx.Offsets[0], true
}
func (b *ViewerBackend) FindLineStart(offset int64) int64 {
	if offset <= 0 {
		return 0
	}
	chunkSize := int64(4096)
	curr := offset
	for curr > 0 {
		start := curr - chunkSize
		if start < 0 {
			start = 0
		}

		data, err := b.ReadAt(start, int(curr-start))
		if err == piecetable.ErrLoading {
			return offset // Stay at current offset while loading
		}
		if err != nil {
			return offset
		}

		for i := len(data) - 1; i >= 0; i-- {
			if data[i] == '\n' {
				return start + int64(i) + 1
			}
		}
		curr = start
	}
	return 0
}
