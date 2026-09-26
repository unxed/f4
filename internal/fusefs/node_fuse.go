//go:build (linux || darwin || freebsd) && !lite

package fusefs

import (
	"context"
	"errors"
	"math"
	"os"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/unxed/f4/vfs"
)

const supported = true

const (
	defaultAttrTimeout  = 3 * time.Second
	defaultEntryTimeout = 3 * time.Second
)

// startServer mounts the bridge. fs.Mount already waits for the kernel to
// acknowledge the mount, so a successful return means the directory is
// readable by other processes.
func startServer(ctx context.Context, m *Mount, opts Options) error {
	attrTimeout := opts.AttrTimeout
	if attrTimeout <= 0 {
		attrTimeout = defaultAttrTimeout
	}
	entryTimeout := opts.EntryTimeout
	if entryTimeout <= 0 {
		entryTimeout = defaultEntryTimeout
	}

	fsOpts := &fs.Options{
		AttrTimeout:  &attrTimeout,
		EntryTimeout: &entryTimeout,
		RootStableAttr: &fs.StableAttr{
			Ino: inodeOf(m.RootPath),
		},
	}
	fsOpts.FsName = m.Source
	fsOpts.Name = "f4"
	fsOpts.AllowOther = opts.AllowOther
	fsOpts.Debug = opts.Debug
	// A VFS round trip costs far more than a memory copy, so the fewer and
	// larger the requests, the better. go-fuse defaults to 128 KiB writes
	// and 12 background requests, which were chosen for local file systems.
	fsOpts.MaxWrite = 1 << 20
	fsOpts.MaxBackground = 64
	// Nothing here has extended attributes, and without this a single ls
	// produces a burst of getxattr calls that all have to be refused one at
	// a time, each behind the bridge lock.
	fsOpts.DisableXAttrs = true
	if opts.ReadOnly {
		fsOpts.Options = append(fsOpts.Options, "ro")
	}

	root := &node{b: m.bridge, path: m.RootPath}
	server, err := fs.Mount(m.MountPoint, root, fsOpts)
	if err != nil {
		return err
	}
	m.server = server
	return nil
}

// node is one object in the mounted tree, identified by its VFS-native
// path. Nothing else is cached on it: the bridge decides what is worth
// remembering.
type node struct {
	fs.Inode
	b    *bridge
	path string
}

var (
	_ = (fs.NodeGetattrer)((*node)(nil))
	_ = (fs.NodeLookuper)((*node)(nil))
	_ = (fs.NodeReaddirer)((*node)(nil))
	_ = (fs.NodeOpener)((*node)(nil))
	_ = (fs.NodeStatfser)((*node)(nil))
	_ = (fs.NodeMkdirer)((*node)(nil))
	_ = (fs.NodeUnlinker)((*node)(nil))
	_ = (fs.NodeRmdirer)((*node)(nil))
	_ = (fs.NodeRenamer)((*node)(nil))
	_ = (fs.NodeCreater)((*node)(nil))
	_ = (fs.NodeSetattrer)((*node)(nil))
	_ = (fs.NodeReadlinker)((*node)(nil))
	_ = (fs.NodeSymlinker)((*node)(nil))
	_ = (fs.FileWriter)((*writeFileHandle)(nil))
	_ = (fs.FileFlusher)((*writeFileHandle)(nil))
	_ = (fs.FileReleaser)((*writeFileHandle)(nil))
	_ = (fs.FileFsyncer)((*writeFileHandle)(nil))
)

// writeFileHandle is one open handle on a file being written. The data goes
// into the write handle shared by every FUSE open on that path. Flush makes
// the current data durable; Release performs the same work if Flush was
// skipped.
type writeFileHandle struct {
	b  *bridge
	wh *writeHandle
}

// Create makes a new file. The file exists in the mount immediately — the
// kernel gets an inode and a handle — but reaches the backend only on close,
// because vfs.Create takes one sequential stream and the kernel writes in
// whatever order it likes.
func (n *node) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if errno := n.writeRefusal(); errno != 0 {
		return nil, nil, 0, errno
	}
	childPath := n.b.join(n.path, name)
	wh, _, err := n.b.acquireWriteHandle(ctx, childPath)
	if err != nil {
		return nil, nil, 0, errnoOf(err)
	}
	item := vfs.VFSItem{Name: name, MTime: time.Now()}
	fillAttr(&out.Attr, item, childPath)
	stable := fs.StableAttr{Ino: inodeOf(childPath), Mode: typeBits(item)}
	inode := n.NewInode(ctx, &node{b: n.b, path: childPath}, stable)
	return inode, &writeFileHandle{b: n.b, wh: wh}, 0, 0
}

func (f *writeFileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	written, err := f.wh.writeAt(ctx, f.b, data, off)
	count, ok := fuseWriteCount(written)
	if !ok {
		return 0, syscall.EOVERFLOW
	}
	if err != nil {
		return count, errnoOf(err)
	}
	return count, 0
}

func fuseWriteCount(written int) (uint32, bool) {
	if written < 0 || uint64(written) > math.MaxUint32 {
		return 0, false
	}
	// #nosec G115 -- written was checked against the uint32 FUSE result range above.
	return uint32(written), true
}

// Fsync is a no-op on purpose. Committing per fsync would turn a tool that
// syncs every chunk into one full upload per chunk; the semantics are
// commit-on-close and are documented as such in FUSE.md.
func (f *writeFileHandle) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	return 0
}

// Flush runs on every close(2) and is where an error can still reach the
// program that wrote the file, so the commit happens here when this is the
// last handle. Release does it again only if Flush never ran.
func (f *writeFileHandle) Flush(ctx context.Context) syscall.Errno {
	return errnoOf(f.b.flushWriter(ctx, f.wh))
}

func (f *writeFileHandle) Release(ctx context.Context) syscall.Errno {
	return errnoOf(f.b.finishWriter(ctx, f.wh))
}

// writeRefusal reports why a write cannot happen, or 0 when it can. The mount
// being read-only and the backend being unable to write are different facts
// and are asked separately, so turning writes on is one check to change.
func (n *node) writeRefusal() syscall.Errno {
	if n.b.readOnly || !n.b.writeOK {
		return syscall.EROFS
	}
	return 0
}

// Mkdir is the first write opcode: it needs no staging file, no handle table
// and no commit-on-close semantics, so it exercises the write path end to end
// without any of what iteration 4 still has to decide.
func (n *node) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if errno := n.writeRefusal(); errno != 0 {
		return nil, errno
	}
	if err := n.b.mkdir(ctx, n.path, name); err != nil {
		return nil, errnoOf(err)
	}
	childPath := n.b.join(n.path, name)
	item, err := n.b.stat(ctx, childPath)
	if err != nil {
		// The directory is there; the backend simply cannot describe it
		// yet. Answering with what we know beats failing a mkdir that
		// actually succeeded.
		item = vfs.VFSItem{Name: name, IsDir: true, MTime: time.Now()}
	}
	fillAttr(&out.Attr, item, childPath)
	stable := fs.StableAttr{Ino: inodeOf(childPath), Mode: typeBits(item)}
	return n.NewInode(ctx, &node{b: n.b, path: childPath}, stable), 0
}

func (n *node) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// A file being written does not exist in the backend yet, or exists
	// there in its old shape. Answering from the staging copy is what makes
	// `cp a b && ls -l b` show b at its real size instead of zero, and what
	// stops a reader from being told the file ends before it does.
	if wh := n.b.writerFor(n.path); wh != nil && wh.staged != nil {
		if size, err := wh.staged.Size(); err == nil {
			item, statErr := n.b.stat(ctx, n.path)
			if statErr != nil {
				item = vfs.VFSItem{Name: displayName(n.path)}
			}
			item.Size = size
			item.MTime = time.Now()
			fillAttr(&out.Attr, item, n.path)
			return 0
		}
	}
	item, err := n.b.stat(ctx, n.path)
	if err != nil {
		return errnoOf(err)
	}
	fillAttr(&out.Attr, item, n.path)
	return 0
}

func (n *node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	item, err := n.b.lookup(ctx, n.path, name)
	if err != nil {
		return nil, errnoOf(err)
	}
	childPath := n.b.join(n.path, name)
	fillAttr(&out.Attr, item, childPath)

	stable := fs.StableAttr{Ino: inodeOf(childPath), Mode: typeBits(item)}
	child := n.NewInode(ctx, &node{b: n.b, path: childPath}, stable)
	return child, 0
}

func (n *node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	items, err := n.b.readDir(ctx, n.path)
	if err != nil {
		return nil, errnoOf(err)
	}
	entries := make([]fuse.DirEntry, 0, len(items))
	for _, item := range items {
		name := displayName(item.Name)
		if name == "" || name == "." || name == ".." {
			continue
		}
		entries = append(entries, fuse.DirEntry{
			Name: name,
			Mode: typeBits(item),
			Ino:  inodeOf(n.b.join(n.path, name)),
		})
	}
	return fs.NewListDirStream(entries), 0
}

func (n *node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if flags&uint32(syscall.O_ACCMODE) != uint32(syscall.O_RDONLY) {
		// EROFS rather than EPERM either way: a write attempt gets a
		// clear "this file system does not do that" instead of a
		// confusing partial success. The two reasons stay separate so
		// iteration 4 only has to change the first one.
		if errno := n.writeRefusal(); errno != 0 {
			return nil, 0, errno
		}
		return n.openForWrite(ctx, flags)
	}
	item, err := n.b.stat(ctx, n.path)
	if err != nil {
		return nil, 0, errnoOf(err)
	}
	if item.IsDir {
		return nil, 0, syscall.EISDIR
	}
	h, err := n.b.open(ctx, n.path, item.Size)
	if err != nil {
		return nil, 0, errnoOf(err)
	}
	return &fileHandle{h: h}, 0, 0
}

// Statfs numbers. A VFS backend knows neither its size nor its free space —
// an archive has no such notion, and asking a remote host for one would be a
// round trip per df(1). The figures below are deliberately synthetic and
// deliberately large: their only job is to be an answer a writer accepts.
const (
	statfsBlockSize   = 4096
	statfsTotalBlocks = 1 << 28 // 1 TiB in 4 KiB blocks
	statfsTotalInodes = 1 << 20
)

// Statfs reports a plausible size rather than nothing. Zeroes are a fine
// answer for a read-only mount and a bad one for a writable mount: cp, git
// and most file dialogs check free space first and refuse to write to a file
// system that claims to have none. A failing statfs is worse still — df and
// some dialogs read that as a broken file system.
func (n *node) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	out.Blocks = statfsTotalBlocks
	out.Bfree = statfsTotalBlocks
	out.Bavail = statfsTotalBlocks
	out.Bsize = statfsBlockSize
	out.Frsize = statfsBlockSize
	out.Files = statfsTotalInodes
	out.Ffree = statfsTotalInodes
	out.NameLen = 255
	return 0
}

// fileHandle adapts one open VFS reader to the FUSE file protocol.
type fileHandle struct {
	h *handle
}

var (
	_ = (fs.FileReader)((*fileHandle)(nil))
	_ = (fs.FileReleaser)((*fileHandle)(nil))
)

func (f *fileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	n, err := f.h.readAt(ctx, dest, off)
	if err != nil && n == 0 {
		return nil, errnoOf(err)
	}
	return fuse.ReadResultData(dest[:n]), 0
}

func (f *fileHandle) Release(ctx context.Context) syscall.Errno {
	return errnoOf(f.h.release())
}

func typeBits(item vfs.VFSItem) uint32 {
	if item.IsDir {
		return fuse.S_IFDIR
	}
	if item.IsSymlink {
		return fuse.S_IFLNK
	}
	return fuse.S_IFREG
}

// fillAttr converts VFS metadata into kernel attributes. Backends which
// report neither ownership nor permissions are presented as read-only files
// belonging to whoever started f4, because a mount nobody can read is
// worse than an approximate one.
func fillAttr(out *fuse.Attr, item vfs.VFSItem, itemPath string) {
	out.Ino = inodeOf(itemPath)
	perm := unixMode(item) & 0o7777
	if item.IsDir {
		out.Mode = fuse.S_IFDIR | perm
		out.Nlink = 2
	} else if item.IsSymlink {
		// A link reported as a regular file is a link ls -l cannot show
		// and cp -a cannot copy.
		out.Mode = fuse.S_IFLNK | perm
		out.Nlink = 1
		if item.Size > 0 {
			out.Size = uint64(item.Size)
		}
	} else {
		out.Mode = fuse.S_IFREG | perm
		out.Nlink = 1
		if item.Size > 0 {
			out.Size = uint64(item.Size)
			out.Blocks = (out.Size + 511) / 512
		}
	}

	mtime := item.MTime
	if mtime.IsZero() {
		mtime = time.Now()
	}
	atime, ctime := item.ATime, item.CTime
	if atime.IsZero() {
		atime = mtime
	}
	if ctime.IsZero() {
		ctime = mtime
	}
	out.SetTimes(&atime, &mtime, &ctime)

	if uid, ok := fuseID(item.Uid); ok && uid > 0 {
		out.Uid = uid
	} else if uid, ok := fuseID(os.Getuid()); ok {
		out.Uid = uid
	}
	if gid, ok := fuseID(item.Gid); ok && gid > 0 {
		out.Gid = gid
	} else if gid, ok := fuseID(os.Getgid()); ok {
		out.Gid = gid
	}
	out.Blksize = 4096
}

func fuseID(value int) (uint32, bool) {
	if value < 0 || uint64(value) > math.MaxUint32 {
		return 0, false
	}
	// #nosec G115 -- value was checked against the uint32 uid/gid range above.
	return uint32(value), true
}

// errnoOf maps VFS errors onto errno values. Backends return plain errors,
// so anything unrecognized becomes EIO rather than a guess.
func errnoOf(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	switch {
	case errors.Is(err, context.Canceled):
		return syscall.EINTR
	case errors.Is(err, context.DeadlineExceeded):
		return syscall.ETIMEDOUT
	case errors.Is(err, errClosed):
		return syscall.ENODEV
	case errors.Is(err, os.ErrNotExist):
		return syscall.ENOENT
	case errors.Is(err, os.ErrPermission):
		return syscall.EACCES
	case errors.Is(err, os.ErrExist):
		return syscall.EEXIST
	case errors.Is(err, os.ErrInvalid):
		return syscall.EINVAL
	}
	return syscall.EIO
}

// Unlink and Rmdir are the other two writes that need no open handle. vfs.VFS
// has one Remove for both, so the difference lives here rather than in the
// backend: the kernel has already checked which of the two it is asking for.
func (n *node) Unlink(ctx context.Context, name string) syscall.Errno {
	return n.removeChild(ctx, name)
}

func (n *node) Rmdir(ctx context.Context, name string) syscall.Errno {
	return n.removeChild(ctx, name)
}

func (n *node) removeChild(ctx context.Context, name string) syscall.Errno {
	if errno := n.writeRefusal(); errno != 0 {
		return errno
	}
	if err := n.b.remove(ctx, n.path, name); err != nil {
		return errnoOf(err)
	}
	return 0
}

// Rename is the last write that needs no open handle. The destination arrives
// as a node rather than a path, because the kernel may be moving the entry
// into a different directory of the same mount; anything else is not ours to
// serve and the kernel does not ask us to.
//
// RENAME_EXCHANGE and RENAME_NOREPLACE are refused rather than approximated:
// vfs.Rename has no way to promise either, and a rename that silently loses
// the guarantee the caller asked for is worse than one that fails.
func (n *node) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	if errno := n.writeRefusal(); errno != 0 {
		return errno
	}
	if flags != 0 {
		return syscall.EINVAL
	}
	target, ok := newParent.(*node)
	if !ok {
		return syscall.EXDEV
	}
	if err := n.b.rename(ctx, n.path, name, target.path, newName); err != nil {
		return errnoOf(err)
	}
	return 0
}

// openForWrite opens a file that already exists. Unless the caller asked for
// O_TRUNC, the staging file starts as a copy of what the backend holds:
// the commit sends the whole file back, so anything the caller does not
// overwrite has to be in there to send.
func (n *node) openForWrite(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	wh, created, err := n.b.acquireWriteHandle(ctx, n.path)
	if err != nil {
		return nil, 0, errnoOf(err)
	}
	if flags&uint32(syscall.O_TRUNC) != 0 {
		if err := wh.truncate(ctx, n.b, 0); err != nil {
			if n.b.releaseWriter(wh) {
				err = errors.Join(err, wh.close())
			}
			return nil, 0, errnoOf(err)
		}
	}
	// A directly written file needs no download: the backend already holds
	// what the caller is not overwriting. This is the whole point of
	// iteration 5.
	if created && wh.needsCommit() && flags&uint32(syscall.O_TRUNC) == 0 {
		if item, statErr := n.b.stat(ctx, n.path); statErr == nil && !item.IsDir && item.Size > 0 {
			if err := n.b.loadStaged(ctx, n.path, wh.staged); err != nil {
				// Nothing has been written yet, so the backend's
				// copy is still intact: fail the open rather than
				// commit a file with a hole where the old
				// contents should have been.
				if n.b.releaseWriter(wh) {
					err = errors.Join(err, wh.close())
				}
				return nil, 0, errnoOf(err)
			}
		}
	}
	return &writeFileHandle{b: n.b, wh: wh}, 0, 0
}

// Setattr answers chmod and touch. Size is not handled here yet: truncating
// means rewriting the staged copy, which is its own step.
//
// A no-op Setattr would be worse than a refusal in one specific way: cp -p,
// tar -x and rsync all set the mode after writing the file, and a silent
// success would tell them the permissions were preserved when they were not.
func (n *node) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if errno := n.writeRefusal(); errno != 0 {
		return errno
	}
	var item vfs.VFSItem
	var wanted bool

	if mode, ok := in.GetMode(); ok {
		// Only the permission bits: the file type is not the caller's
		// to change, and passing it through would be a mode the
		// backend cannot honour.
		item.UnixMode = uint32(mode) & 0o7777
		wanted = true
	}
	if mtime, ok := in.GetMTime(); ok {
		item.MTime = mtime
		wanted = true
	}
	if size, ok := in.GetSize(); ok {
		if size > math.MaxInt64 {
			return syscall.EFBIG
		}
		// #nosec G115 -- the explicit MaxInt64 check above makes this conversion lossless.
		if errno := n.truncate(ctx, int64(size)); errno != 0 {
			return errno
		}
		wanted = true
	}
	if !wanted {
		// Something we do not serve — uid/gid, for one. Answering with
		// the current attributes is what a mount that cannot own files
		// can honestly do.
		return n.Getattr(ctx, f, out)
	}
	if item.UnixMode != 0 || !item.MTime.IsZero() {
		if err := n.b.setAttributes(ctx, n.path, item); err != nil {
			return errnoOf(err)
		}
	}
	return n.Getattr(ctx, f, out)
}

// truncate resizes a file. With the file already open for writing it is one
// operation on the staging copy, and the commit at close carries it; with the
// file closed — `truncate -s 0 x`, or a shell redirect onto an existing
// file — the whole round trip happens here, because there is no close coming
// that would otherwise do it.
func (n *node) truncate(ctx context.Context, size int64) (errno syscall.Errno) {
	if wh := n.b.writerFor(n.path); wh != nil {
		if err := wh.truncate(ctx, n.b, size); err != nil {
			return errnoOf(err)
		}
		return 0
	}

	wh, created, err := n.b.acquireWriteHandle(ctx, n.path)
	if err != nil {
		return errnoOf(err)
	}
	defer func() {
		if n.b.releaseWriter(wh) {
			if err := wh.close(); errno == 0 {
				errno = errnoOf(err)
			}
		}
	}()

	// A backend written at an offset resizes its own file; there is nothing
	// to fetch and nothing to send back.
	if !wh.needsCommit() {
		if err := wh.truncate(ctx, n.b, size); err != nil {
			return errnoOf(err)
		}
		n.b.invalidate(n.b.parentOf(n.path))
		return 0
	}

	// Shortening keeps what comes before the cut, so the old contents have
	// to be fetched first. Truncating to nothing does not: downloading a
	// file in order to throw all of it away is the one case worth spotting.
	if created && size > 0 {
		if item, statErr := n.b.stat(ctx, n.path); statErr == nil && !item.IsDir && item.Size > 0 {
			if err := n.b.loadStaged(ctx, n.path, wh.staged); err != nil {
				return errnoOf(err)
			}
		}
	}
	if err := wh.truncate(ctx, n.b, size); err != nil {
		return errnoOf(err)
	}
	r, err := wh.staged.Reader()
	if err != nil {
		return errnoOf(err)
	}
	if err := n.b.commit(ctx, n.path, r); err != nil {
		return errnoOf(err)
	}
	return 0
}

func (n *node) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	target, err := n.b.readlink(ctx, n.path)
	if err != nil {
		return nil, errnoOf(err)
	}
	return []byte(target), 0
}

func (n *node) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if errno := n.writeRefusal(); errno != 0 {
		return nil, errno
	}
	if err := n.b.symlink(ctx, target, n.path, name); err != nil {
		return nil, errnoOf(err)
	}
	childPath := n.b.join(n.path, name)
	item := vfs.VFSItem{Name: name, IsSymlink: true, Size: int64(len(target)), MTime: time.Now()}
	fillAttr(&out.Attr, item, childPath)
	stable := fs.StableAttr{Ino: inodeOf(childPath), Mode: typeBits(item)}
	return n.NewInode(ctx, &node{b: n.b, path: childPath}, stable), 0
}
