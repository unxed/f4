package archive

import (
	"io"
	"io/fs"
	"sync"
)

// seqMemberReader serves ReadAt on an archive member that can only be read
// from its beginning (a member of a compressed TAR, a stored stream). Every
// ReadAt used to reopen the member and discard the bytes before the offset, so
// a nested archive reading its parent in chunks -- which is how every layer of
// a chain reads the one below it -- cost time proportional to the square of the
// member size. The reader keeps the handle open between calls: a read at or
// after where the last one ended continues from there, and only a read that
// goes back reopens the member (f4#1678).
//
// A handle that can Seek is never kept: seeking it is already cheap.
type seqMemberReader struct {
	open func() (fs.File, error)

	mu   sync.Mutex
	file fs.File
	pos  int64
}

// setOpen installs how the member is opened, once; later calls keep the first.
func (r *seqMemberReader) setOpen(open func() (fs.File, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.open == nil {
		r.open = open
	}
}

func (r *seqMemberReader) ReadAt(p []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file != nil && off < r.pos {
		r.dropLocked()
	}
	if r.file == nil {
		file, err := r.open()
		if err != nil {
			return 0, err
		}
		if err := seekArchiveFile(file, off); err != nil {
			_ = file.Close()
			return 0, err
		}
		r.file, r.pos = file, off
	} else if off > r.pos {
		if err := discardArchiveBytes(r.file, off-r.pos); err != nil {
			r.dropLocked()
			return 0, err
		}
		r.pos = off
	}

	n, err := io.ReadFull(r.file, p)
	r.pos += int64(n)
	if err != nil {
		r.dropLocked()
	} else if _, seekable := r.file.(io.Seeker); seekable {
		r.dropLocked()
	}
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		err = io.EOF
	}
	return n, err
}

func (r *seqMemberReader) Position() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pos
}

func (r *seqMemberReader) dropLocked() {
	if r.file != nil {
		_ = r.file.Close()
		r.file = nil
	}
}

func (r *seqMemberReader) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropLocked()
}
