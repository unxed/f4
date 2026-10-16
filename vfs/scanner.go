package vfs

import (
	"context"
	"errors"
	"fmt"
)

// OpStats holds detailed statistics about a file system subtree.
// Separating Files/Dirs from Bytes allows for highly accurate,
// non-linear ETA calculations during I/O operations. DirBytes is the
// sum of directory-inode Sizes (as reported by Stat) and is tracked
// separately so ETA calculations for copy/move — which only move
// file payload — stay unaffected; UI consumers that display the
// far2l-style "Files size" should sum Bytes + DirBytes (far2l does
// this too: FileSize += FindData.nFileSize for directory entries,
// see far2l/src/dirinfo.cpp).
type OpStats struct {
	Bytes            int64
	DirBytes         int64
	PhysicalBytes    int64 // sum of VFSItem.PhysicalSize (Unix stat.Blocks*512 / Windows GetCompressedFileSize); 0 on VFSes that don't report it
	Files            int64
	Dirs             int64
	UnknownSizeFiles int64
}

// Add merges another OpStats into the current one.
func (s *OpStats) Add(other OpStats) {
	s.Bytes += other.Bytes
	s.DirBytes += other.DirBytes
	s.PhysicalBytes += other.PhysicalBytes
	s.Files += other.Files
	s.Dirs += other.Dirs
	s.UnknownSizeFiles += other.UnknownSizeFiles
}

// ScanCallback is used to report progress during a long scanning operation.
// It returns the path currently being inspected and the accumulated stats so far.
type ScanCallback func(currentPath string, stats OpStats)

// PhysicalSizer is an optional VFS capability declaring that the
// implementation can produce VFSItem.PhysicalSize for individual
// items — either cheaply during ReadDir or through a Stat fallback.
// The scanner uses this to decide whether to bother with a lazy
// Stat when an item comes back with PhysicalSize == 0; VFSes that
// don't implement this (archive, network) skip the fallback entirely,
// which matters because CalculateStats is also on the copy/move
// pre-scan path — one lazy Stat per file across an archive tree
// would be an N+1 mutex-serialised round trip for a field the VFS
// can't fill anyway.
type PhysicalSizer interface {
	SupportsPhysicalSize() bool
}

// FileIdentifier is an optional VFS capability that resolves a stable
// (device, inode) identity for a single path, so the scanner can
// dedup hard links even on backends whose ReadDir can't supply that
// identity cheaply. Unix OSVFS already stamps VFSItem.Device/Inode
// from Stat_t during ReadDir (fillPhysicalSizeCheap), so this is only
// really exercised on Windows — FindNextFile carries no file index,
// so identity needs a per-file GetFileInformationByHandle (see
// os_vfs_physical_windows.go). The scanner calls this only when
// DedupInodes is set (seen != nil), so ordinary listings and non-dedup
// pre-scans never pay the extra syscall.
type FileIdentifier interface {
	FileIdentity(ctx context.Context, path string) (device, inode uint64, ok bool)
}

// ScanOptions tunes the behaviour of CalculateStatsWithOptions.
// The zero value is the historical, size-inflating behaviour that
// copy/move pre-scans rely on (a symlink-to-dir is walked as if it
// were a real directory, hard-linked inodes are counted once per
// path); consumers that want find/far2l-style semantics should flip
// the flags explicitly.
type ScanOptions struct {
	// FollowSymlinkDirs decides how symlinks that resolve to a
	// directory are treated. true (default via plain CalculateStats):
	// recurse into the target — same tree walked twice if the link
	// points inside the scan root. false (used by QuickView): treat
	// the symlink as a leaf, counting it once with its own tiny
	// size, matching what `find` and far2l report.
	FollowSymlinkDirs bool
	// DedupInodes tells the scanner to skip any entry whose
	// (Device, Inode) pair has already been seen in this walk — the
	// far2l behaviour (see far2l/src/dirinfo.cpp:120 ScannedINodes).
	// Hard-linked files are then counted once, matching `du`. false
	// (default) preserves the copy/move ETA path where two hard
	// links to one inode really do count as two work items. QuickView
	// opts in to keep its numbers aligned with far2l.
	DedupInodes bool
}

// FastScanner is an optional interface for VFS implementations.
// If a VFS implements this, it means it can offload the tree traversal
// to the remote server (e.g., FISH+), drastically reducing network roundtrips.
type FastScanner interface {
	Scan(ctx context.Context, basePath string, names []string, cb ScanCallback) (OpStats, error)
}

// CalculateStats is the main entry point for gathering operation statistics.
// It uses FastScanner if available, otherwise falls back to GenericScan.
// stats.PhysicalBytes is populated when the VFS reports per-item
// PhysicalSize (see VFSItem.PhysicalSize) — OSVFS does this on Unix
// via stat.Blocks and on Windows via GetCompressedFileSize; remote
// VFSes leave it zero and the consumer hides the metric.
//
// This entry point preserves historical behaviour (symlink-to-dir is
// walked as if it were a real directory) so file_ops / actions
// pre-scan ETAs continue to match what the actual copy path does.
// Callers that want find / far2l semantics — count symlinks as
// leaves — should use CalculateStatsWithOptions instead.
func CalculateStats(ctx context.Context, v VFS, basePath string, names []string, cb ScanCallback) (OpStats, error) {
	return CalculateStatsWithOptions(ctx, v, basePath, names, ScanOptions{FollowSymlinkDirs: true}, cb)
}

// CalculateStatsWithOptions is CalculateStats with a knob. See
// ScanOptions for the available toggles. FastScanner VFSes fall back
// to the generic walk since the remote-side Scan protocol has no
// place to carry an option struct.
func CalculateStatsWithOptions(ctx context.Context, v VFS, basePath string, names []string, opts ScanOptions, cb ScanCallback) (OpStats, error) {
	// A scan is one operation: one refused password ends elevation for all
	// of it (f4#1998).
	ctx = WithElevationScope(ctx)
	if opts.FollowSymlinkDirs {
		// Historical path — try the FastScanner shortcut for VFSes
		// that implement it (they can offload the walk to the server).
		if fs, ok := v.(FastScanner); ok {
			return fs.Scan(ctx, basePath, names, cb)
		}
	}
	return genericScan(ctx, v, basePath, names, opts, cb)
}

// GenericScan performs a recursive, client-side tree traversal to gather stats.
func GenericScan(ctx context.Context, v VFS, basePath string, names []string, cb ScanCallback) (OpStats, error) {
	ctx = WithElevationScope(ctx)
	return genericScan(ctx, v, basePath, names, ScanOptions{FollowSymlinkDirs: true}, cb)
}

// inodeKey identifies a filesystem object across a walk for
// hard-link deduplication. Device is included so a scan crossing
// bind mounts / overlays doesn't collide inode numbers from
// different filesystems.
type inodeKey struct {
	Dev, Ino uint64
}

func genericScan(ctx context.Context, v VFS, basePath string, names []string, opts ScanOptions, cb ScanCallback) (OpStats, error) {
	var totalStats OpStats
	var seen map[inodeKey]struct{}
	if opts.DedupInodes {
		seen = make(map[inodeKey]struct{})
	}

	for _, name := range names {
		if ctx.Err() != nil {
			return totalStats, ctx.Err()
		}

		fullPath := v.Join(basePath, name)
		itemStat, err := v.Stat(ctx, fullPath)
		if err != nil {
			// If we can't stat the root item, we abort.
			// (During actual copy, AskError handles this, but for pre-scan we just return the error).
			//
			// Name the path. A bare errno from a scan of several names
			// says which thing went wrong but not which of them, and
			// that is the whole question when the path arrived from
			// somewhere else. Cancellation passes through untouched:
			// callers compare it against context.Canceled directly.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return totalStats, err
			}
			return totalStats, fmt.Errorf("%s: %w", fullPath, err)
		}

		err = scanRecursive(ctx, v, fullPath, itemStat, &totalStats, cb, 0, opts, seen)
		if err != nil {
			return totalStats, err
		}
	}

	return totalStats, nil
}

func scanRecursive(ctx context.Context, v VFS, currentPath string, item VFSItem, stats *OpStats, cb ScanCallback, depth int, opts ScanOptions, seen map[inodeKey]struct{}) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Prevent infinite recursion (e.g., cyclic symlinks not caught by VFS)
	if depth > 1000 {
		return fmt.Errorf("maximum recursion depth exceeded at %s", currentPath)
	}

	if cb != nil {
		// Report progress. To avoid spamming the UI thread, the caller should throttle this.
		cb(currentPath, *stats)
	}

	// Hard-link dedup — matches far2l/src/dirinfo.cpp:120 ScannedINodes.
	// A hard-linked file reached through multiple paths in the same
	// walk should count once (its blocks are on disk once). Symlinks
	// each have their own inode so this never dedups them.
	if seen != nil {
		// Unix stamps (Device, Inode) during ReadDir for free; Windows'
		// FindNextFile can't, so resolve identity lazily via the optional
		// FileIdentifier capability — one GetFileInformationByHandle per
		// entry, paid only because DedupInodes was requested. Symlinks are
		// skipped: they're leaf-counted once regardless, and resolving one
		// would mean an extra handle open for no dedup win. Backends that
		// still leave both at zero (remote, or an NTFS-less volume) fall
		// through with no dedup rather than merging everything under (0,0).
		if item.Device == 0 && item.Inode == 0 && !item.IsSymlink {
			if idf, ok := v.(FileIdentifier); ok {
				if dev, ino, ok := idf.FileIdentity(ctx, currentPath); ok {
					item.Device, item.Inode = dev, ino
				}
			}
		}
		if item.Device != 0 || item.Inode != 0 {
			key := inodeKey{item.Device, item.Inode}
			if _, dup := seen[key]; dup {
				return nil
			}
			seen[key] = struct{}{}
		}
	}

	// PhysicalSize is populated cheaply on Unix during ReadDir; on
	// Windows the ReadDir path skips it to keep listings fast. Fall
	// back to a Stat only when (a) the VFS declares it can actually
	// produce the number (PhysicalSizer), (b) the item plausibly has
	// non-zero physical size, and (c) it's NOT a symlink — v.Stat
	// resolves symlinks and would return the target inode's blocks,
	// which is already counted through the direct path in the same
	// walk. Without the symlink guard, a tree with N file-symlinks
	// double-counts their targets' blocks (measured: a uv wheel
	// cache of 59 link/target pairs added ~3.5 GB of ghost physical).
	if item.PhysicalSize == 0 && item.Size > 0 && !item.IsSymlink {
		if ps, ok := v.(PhysicalSizer); ok && ps.SupportsPhysicalSize() {
			if st, err := v.Stat(ctx, currentPath); err == nil {
				item.PhysicalSize = st.PhysicalSize
			}
		}
	}

	// find / far2l-style leaf semantics: a symlink is counted once,
	// no matter what it points at, and we do NOT walk into its
	// target. Only fires when the caller opted out of the historical
	// follow-through — copy/move pre-scans still recurse because the
	// actual copy path recurses.
	if item.IsSymlink && !opts.FollowSymlinkDirs {
		stats.Files++
		if item.Size == 0 && !item.SizeKnown {
			stats.UnknownSizeFiles++
		}
		stats.Bytes += item.Size
		stats.PhysicalBytes += item.PhysicalSize
		return nil
	}

	if !item.IsDir {
		stats.Files++
		if item.Size == 0 && !item.SizeKnown {
			stats.UnknownSizeFiles++
		}
		stats.Bytes += item.Size
		stats.PhysicalBytes += item.PhysicalSize
		return nil
	}

	// It's a directory
	stats.Dirs++
	stats.DirBytes += item.Size
	stats.PhysicalBytes += item.PhysicalSize

	var childItems []VFSItem
	if err := v.ReadDir(ctx, currentPath, func(chunk []VFSItem) {
		childItems = append(childItems, chunk...)
	}); err != nil {
		// Permission denied / transient I/O on this specific directory
		// must NOT kill the whole walk — matches far2l's ScanTree,
		// which silently steps over inaccessible directories and
		// returns partial totals. Root-level failure is still surfaced
		// upstream (via v.Stat in genericScan). Cancellation is
		// preserved by returning ctx.Err() when set.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}

	for _, child := range childItems {
		if child.Name == ".." {
			continue
		}
		childPath := v.Join(currentPath, child.Name)
		// Propagate errors from the recursive call — the depth-limit
		// guard and ctx cancellation live in scanRecursive itself and
		// must be able to abort the whole walk. Per-directory ReadDir
		// failures inside the recursion are already swallowed above,
		// so this only forwards genuine "abort" conditions.
		if err := scanRecursive(ctx, v, childPath, child, stats, cb, depth+1, opts, seen); err != nil {
			return err
		}
	}

	return nil
}
