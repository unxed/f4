//go:build linux || darwin || freebsd

package fusefs

import (
	"context"
	"errors"
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
	fsOpts.MountOptions.FsName = m.Source
	fsOpts.MountOptions.Name = "f4"
	fsOpts.MountOptions.AllowOther = opts.AllowOther
	fsOpts.MountOptions.Debug = opts.Debug
	if opts.ReadOnly {
		fsOpts.MountOptions.Options = append(fsOpts.MountOptions.Options, "ro")
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
)

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
		// Writing to a file is iteration 4; the mount does not pretend.
		return nil, 0, syscall.ENOSYS
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
	f.h.release()
	return 0
}

func typeBits(item vfs.VFSItem) uint32 {
	if item.IsDir {
		return fuse.S_IFDIR
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

	if item.Uid > 0 {
		out.Uid = uint32(item.Uid)
	} else {
		out.Uid = uint32(os.Getuid())
	}
	if item.Gid > 0 {
		out.Gid = uint32(item.Gid)
	} else {
		out.Gid = uint32(os.Getgid())
	}
	out.Blksize = 4096
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
