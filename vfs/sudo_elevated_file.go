package vfs

import (
	"context"
	"io"
	"sync"
)

// elevatedFile reads a file through the elevated dispatcher (CmdReadAt), for
// the platform where a descriptor cannot be handed over (Windows, f4#1768).
type elevatedFile struct {
	client *SudoClient
	path   string
	size   int64

	mu  sync.Mutex
	pos int64
}

// openElevatedFile is OSVFS.Open through the dispatcher.
func openElevatedFile(c *SudoClient, path string) (*elevatedFile, error) {
	item, err := c.Stat(path)
	if err != nil {
		return nil, err
	}
	return &elevatedFile{client: c, path: path, size: item.Size}, nil
}

func (f *elevatedFile) Size() int64 { return f.size }

func (f *elevatedFile) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	n := 0
	for n < len(p) {
		if ctx != nil && ctx.Err() != nil {
			return n, ctx.Err()
		}
		chunk := min(len(p)-n, SudoChunkLimit)
		data, eof, err := f.client.ReadAt(f.path, off+int64(n), chunk)
		n += copy(p[n:], data)
		if err != nil {
			return n, err
		}
		if eof {
			if n < len(p) {
				return n, io.EOF
			}
			break
		}
	}
	return n, nil
}

func (f *elevatedFile) Read(ctx context.Context, p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.ReadAt(ctx, p, f.pos)
	f.pos += int64(n)
	return n, err
}

func (f *elevatedFile) Close() error { return nil }

// elevatedWriter writes a file anew through the elevated dispatcher
// (CmdWriteAt): the first chunk empties it, Close sends what is left.
type elevatedWriter struct {
	client *SudoClient
	path   string
	mode   uint32
	buf    []byte
	off    int64
	sent   bool
	closed bool
}

func newElevatedWriter(c *SudoClient, path string, mode uint32) *elevatedWriter {
	return &elevatedWriter{client: c, path: path, mode: mode}
}

func (w *elevatedWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	w.buf = append(w.buf, p...)
	for len(w.buf) >= SudoChunkLimit {
		if err := w.flush(SudoChunkLimit); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (w *elevatedWriter) flush(n int) error {
	if err := w.client.WriteAt(w.path, w.off, w.buf[:n], !w.sent, w.mode); err != nil {
		return err
	}
	w.sent = true
	w.off += int64(n)
	w.buf = append(w.buf[:0], w.buf[n:]...)
	return nil
}

// Close sends the rest; a file nothing was written to is still created
// empty, as a local Create would leave it.
func (w *elevatedWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if len(w.buf) == 0 && w.sent {
		return nil
	}
	return w.flush(len(w.buf))
}
