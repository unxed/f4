package vfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/unxed/f4/vfs/hostfs"
)

type diskFileWrapper struct {
	hostfs.File
	size int64
}

func probeSeekSize(f io.Seeker) (size int64, found bool, err error) {
	pos, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, false, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, false, fmt.Errorf("restore device offset after size probe: %w", err)
	}
	return pos, pos > 0, nil
}

func (f *diskFileWrapper) Size() int64 { return f.size }
func (f *diskFileWrapper) Read(ctx context.Context, p []byte) (n int, err error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return f.File.Read(p)
}
func (f *diskFileWrapper) ReadAt(ctx context.Context, p []byte, off int64) (n int, err error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return f.File.ReadAt(p, off)
}

// DisksVFS is a specialized virtual filesystem that lists
// physical block devices (PhysicalDriveX on Windows, /dev block devices on Unix)
// allowing the user to view and hex-edit raw disks.
type DisksVFS struct {
	*NullVFS
}

func NewDisksVFS() *DisksVFS {
	return &DisksVFS{NullVFS: NewNullVFS(0)}
}

func (v *DisksVFS) GetPath() string      { return "disks://" }
func (v *DisksVFS) IsAbs(p string) bool  { return true }
func (v *DisksVFS) IsAtRoot() bool       { return true }
func (v *DisksVFS) Base(p string) string { return strings.TrimPrefix(p, "disks://") }
func (v *DisksVFS) Dir(p string) string  { return "disks://" }
func (v *DisksVFS) Clone() VFS           { return NewDisksVFS() }
func (v *DisksVFS) SetPath(p string) error {
	if p == "disks://" || p == "" || p == "/" {
		return nil
	}
	return os.ErrNotExist
}
func (v *DisksVFS) Join(elem ...string) string {
	if len(elem) == 0 {
		return "disks://"
	}
	return "disks://" + elem[len(elem)-1]
}
func (v *DisksVFS) Create(ctx context.Context, path string) (io.WriteCloser, error) {
	return nil, fmt.Errorf("cannot create new files on physical disks")
}

func (v *DisksVFS) ReadDir(ctx context.Context, path string, onChunk func([]VFSItem)) error {
	items := getPlatformBlockDevices(ctx)
	if len(items) > 0 && onChunk != nil {
		onChunk(items)
	}
	return nil
}

func (v *DisksVFS) Stat(ctx context.Context, path string) (VFSItem, error) {
	name := v.Base(path)
	devPath := resolveDevicePath(name)
	size, err := getDeviceSize(devPath, nil)
	if err != nil {
		return VFSItem{}, err
	}
	return VFSItem{KnownMetadata: MetadataExplicit, Name: name, Size: size, SizeKnown: true, MTime: time.Now()}, nil
}

func (v *DisksVFS) Open(ctx context.Context, path string) (ReadAtCloser, error) {
	name := v.Base(path)
	devPath := resolveDevicePath(name)

	// WINE.md §18.2/§19 (f4#1461, task 3): opening a specific device now
	// goes through hostfs, the same "what to do vs. how to reach the OS"
	// split disk *listing* already uses (getPlatformBlockDevices in
	// disks_windows.go). In posix personality this reaches the real
	// /dev/sdX node through libwinescape instead of a bogus \\.\-shaped
	// Win32 path Wine cannot resolve; on every other GOOS/personality
	// hostfs.OpenFile/Open forward to os.OpenFile/os.Open unchanged.
	f, err := hostfs.OpenFile(prepareOSPath(devPath), os.O_RDWR, 0)
	if err != nil {
		f, err = hostfs.Open(prepareOSPath(devPath))
	}

	if err != nil && os.IsPermission(err) && globalSudoClient.IsAvailable() {
		sudoF, sudoErr := globalSudoClient.Open(prepareOSPath(devPath), os.O_RDWR, 0)
		if sudoErr == nil {
			size, sizeErr := getDeviceSize(devPath, sudoF)
			if sizeErr != nil {
				_ = sudoF.Close() // The handle cannot be returned at the wrong offset.
				return nil, sizeErr
			}
			return &diskFileWrapper{File: sudoF, size: size}, nil
		}
		sudoF, sudoErr = globalSudoClient.Open(prepareOSPath(devPath), os.O_RDONLY, 0)
		if sudoErr == nil {
			size, sizeErr := getDeviceSize(devPath, sudoF)
			if sizeErr != nil {
				_ = sudoF.Close() // The handle cannot be returned at the wrong offset.
				return nil, sizeErr
			}
			return &diskFileWrapper{File: sudoF, size: size}, nil
		}
		return nil, sudoErr
	}

	if err != nil {
		return nil, err
	}

	size, sizeErr := getDeviceSize(devPath, f)
	if sizeErr != nil {
		_ = f.Close() // The handle cannot be returned at the wrong offset.
		return nil, sizeErr
	}
	return &diskFileWrapper{File: f, size: size}, nil
}

func (v *DisksVFS) PatchInPlace(ctx context.Context, path string, pieces []PatchPiece) (returnErr error) {
	// A raw disk cannot grow or shift, so a patch that would move the pieces
	// after it has to be refused before anything is written over the device.
	if err := ValidateInPlacePieces(pieces); err != nil {
		return err
	}

	name := v.Base(path)
	devPath := resolveDevicePath(name)

	// Same hostfs conversion as Open above; the sudo/elevation fallback
	// below is untouched -- it is still exactly the Windows-elevation
	// concept it always was (globalSudoClient.Open), just reached after a
	// hostfs.OpenFile attempt instead of a bare os.OpenFile one. A
	// posix/Wine equivalent of that elevation path has not been designed
	// (f4#1461 flags it explicitly), so this still only fires under real
	// Windows permission errors, same as before.
	var f hostfs.File
	var err error
	f, err = hostfs.OpenFile(prepareOSPath(devPath), os.O_RDWR, 0)
	if err != nil && os.IsPermission(err) && globalSudoClient.IsAvailable() {
		f, err = globalSudoClient.Open(prepareOSPath(devPath), os.O_RDWR, 0)
	}
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, f.Close())
	}()

	var newOffset int64 = 0
	for _, p := range pieces {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p.Data != nil {
			if _, err := f.WriteAt(p.Data, newOffset); err != nil {
				return err
			}
		}
		newOffset += p.Length
	}
	return nil
}
