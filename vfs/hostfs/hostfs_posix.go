//go:build !windows

// Package hostfs is the single point of contact between f4 and the host
// filesystem's raw operations (WINE.md §13, Part E). Every call site that
// needs to open, stat, rename, link, or otherwise touch a file goes through
// here instead of calling package os directly.
//
// On every non-Windows GOOS this file is the whole story: a direct,
// inlinable forward to package os, identical in every observable way to
// calling os.* directly. There is no Wine here and no trace of it in the
// compiled binary -- the personality switch (hostfs_windows.go) exists on
// exactly one GOOS.
package hostfs

import (
	"io/fs"
	"os"
	"time"
)

// File is the minimal surface os_vfs.go actually uses on an open file
// handle (checked exhaustively against every f.X(/sudoF.X( call site).
// *os.File satisfies it as-is. The Windows posix-personality backend
// (Stage E3) implements it over libwinescape's raw fd instead of a real
// os.File, which is why this is an interface and not *os.File: a raw fd
// obtained via a Linux syscall cannot be wrapped into a genuine *os.File on
// Windows -- there is no real Win32 handle behind it for os.File's Windows
// internals to call into.
type File interface {
	Read(p []byte) (n int, err error)
	ReadAt(p []byte, off int64) (n int, err error)
	Write(p []byte) (n int, err error)
	WriteAt(p []byte, off int64) (n int, err error)
	Seek(offset int64, whence int) (int64, error)
	Stat() (os.FileInfo, error)
	Truncate(size int64) error
	Close() error
	Fd() uintptr
}

func Open(name string) (File, error) { return os.Open(name) }

func OpenFile(name string, flag int, perm os.FileMode) (File, error) {
	return os.OpenFile(name, flag, perm)
}

func Stat(name string) (os.FileInfo, error)  { return os.Stat(name) }
func Lstat(name string) (os.FileInfo, error) { return os.Lstat(name) }

// ReadDir lists a directory's entries. Matches os.ReadDir's shape exactly
// (sorted by name, lazy Info()) so callers don't need to know which
// personality produced the result.
func ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(name) }

func Readlink(name string) (string, error)  { return os.Readlink(name) }
func Symlink(oldname, newname string) error { return os.Symlink(oldname, newname) }
func Link(oldname, newname string) error    { return os.Link(oldname, newname) }

// ReadFile reads the whole of name and returns its contents. Trivially
// os.ReadFile: every non-Windows GOOS has no second personality to switch
// to (see the package doc), so there is nothing here for hostmode to
// change.
func ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

func Rename(oldpath, newpath string) error         { return os.Rename(oldpath, newpath) }
func RemoveAll(path string) error                  { return os.RemoveAll(path) }
func Remove(name string) error                     { return os.Remove(name) }
func MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func Mkdir(name string, perm os.FileMode) error    { return os.Mkdir(name, perm) }

func Chmod(name string, mode os.FileMode) error { return os.Chmod(name, mode) }
func Chown(name string, uid, gid int) error     { return os.Chown(name, uid, gid) }
func Lchown(name string, uid, gid int) error    { return os.Lchown(name, uid, gid) }
func Chtimes(name string, atime, mtime time.Time) error {
	return os.Chtimes(name, atime, mtime)
}
