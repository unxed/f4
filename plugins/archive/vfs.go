package archive

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/unxed/archives"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/sevenzip"
	"github.com/unxed/zipper/archive"

	"github.com/unxed/tar"
	"github.com/unxed/zip"

	"github.com/unxed/vtui"
)

var TestSkipDelay time.Duration

type autoQueueContextKey struct{}

// WithAutoQueue makes a contended archive operation wait without prompting.
func WithAutoQueue(ctx context.Context) context.Context {
	return context.WithValue(ctx, autoQueueContextKey{}, true)
}

func autoQueueRequested(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	autoQueue, _ := ctx.Value(autoQueueContextKey{}).(bool)
	return autoQueue
}

// archiveVFSIdleTTL is kept as a variable so tests can exercise the cleanup
// transition without waiting for the production grace period.
var archiveVFSIdleTTL = 2 * time.Second

type ctxReader struct {
	r   vfs.ReadAtCloser
	ctx context.Context
}

func (cr ctxReader) Read(p []byte) (int, error) {
	return cr.r.Read(cr.ctx, p)
}

type readerAtAdapter struct {
	r   vfs.ReadAtCloser
	ctx context.Context
}

func (a readerAtAdapter) ReadAt(p []byte, off int64) (int, error) {
	return a.r.ReadAt(a.ctx, p, off)
}

type ArchiveVFS struct {
	mu          sync.Mutex
	parent      vfs.VFS
	arcPath     string
	backingPath string
	displayName string
	format      string
	sfxOffset   int64
	sfxSuffix   string
	innerPath   string
	password    string
	// passwordGen counts installed password changes so a concurrent operation
	// that failed with an older password can retry without a second prompt.
	passwordGen int
	// passwordPromptMu serializes password prompts: only one dialog per
	// archive at a time, even when several operations fail concurrently.
	passwordPromptMu sync.Mutex

	fsys   archive.FileSystem
	closer io.Closer

	activeCount  int
	isClosed     bool
	cleanupTimer *time.Timer
}

func (v *ArchiveVFS) IsAtRoot() bool {
	return v.innerPath == "." || v.innerPath == ""
}

func (v *ArchiveVFS) activePath() string {
	if v.backingPath != "" {
		return v.backingPath
	}
	if osvfs, ok := v.parent.(*vfs.OSVFS); ok {
		absPath, _ := osvfs.Abs(v.arcPath)
		return absPath
	}
	return v.arcPath
}

func (v *ArchiveVFS) ensureFSLocked() error {
	if v.fsys != nil {
		return nil
	}
	if v.cleanupTimer == nil || v.activePath() == "" {
		return fmt.Errorf("archive VFS is closed")
	}
	reopened, err := openArchiveFileSystem(context.Background(), v.activePath(), v.displayName, v.password)
	if err != nil {
		return err
	}
	v.fsys = reopened
	return nil
}

func (v *ArchiveVFS) cancelCleanupLocked() {
	if v.cleanupTimer != nil {
		v.cleanupTimer.Stop()
		v.cleanupTimer = nil
	}
}

func (v *ArchiveVFS) finishNonHandleOperationLocked() {
	if v.isClosed && v.activeCount == 0 && (v.fsys != nil || v.closer != nil) {
		v.startCleanupTimer()
	}
}

func NewArchiveVFS(parent vfs.VFS, path string) (*ArchiveVFS, error) {
	return NewArchiveVFSContext(context.Background(), parent, path)
}

func NewArchiveVFSContext(ctx context.Context, parent vfs.VFS, archivePath string) (*ArchiveVFS, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, fmt.Errorf("archive parent VFS is nil")
	}

	canonicalPath := cleanArchiveRootPath(archivePath)
	displayName := parent.Base(archivePath)
	if displayName == "" {
		displayName = path.Base(strings.ReplaceAll(archivePath, "\\", "/"))
	}
	format := archive.DetectFormat(displayName)
	var finalPath string
	var closer io.Closer
	var sfxOffset int64
	var sfxSuffix string
	if osvfs, ok := parent.(*vfs.OSVFS); ok {
		var err error
		finalPath, err = osvfs.Abs(archivePath)
		if err != nil {
			return nil, err
		}
		// SFX files commonly have an executable extension, so the regular
		// extension-based archive detector never sees them. Probe local files
		// and normalize a discovered archive to a private backing file; the
		// original executable remains untouched.
		if format == "" {
			embedded, backingPath, sfxCloser, probeErr := materializeLocalSFX(finalPath)
			if probeErr != nil {
				return nil, probeErr
			}
			if embedded.format != "" {
				format = embedded.format
				sfxOffset = embedded.offset
				sfxSuffix = embedded.suffix
				if embedded.offset > 0 {
					finalPath = backingPath
					closer = sfxCloser
				}
			}
		}
	} else {
		lease, err := acquireArchiveMaterialization(ctx, parent, archivePath, displayName)
		if err != nil {
			return nil, err
		}
		finalPath = lease.Path()
		closer = lease
		// The same probe the local branch runs, for the same reason: a
		// self-extracting archive names itself after its stub, so the
		// extension-based detector never sees it, and the readers below look
		// for the archive at byte zero. The materialized copy is an ordinary
		// local file, so the probe works on it unchanged -- it is only the
		// carved result that has to be owned separately, because the copy it
		// was cut out of is no longer needed once the cut is made.
		if format == "" {
			embedded, backingPath, sfxCloser, probeErr := materializeNestedSFX(finalPath)
			if probeErr != nil {
				_ = lease.Close()
				return nil, probeErr
			}
			if embedded.format != "" {
				format = embedded.format
				sfxOffset = embedded.offset
				sfxSuffix = embedded.suffix
				if embedded.offset > 0 {
					finalPath = backingPath
					closer = sfxCloser
					_ = lease.Close()
				}
			}
		}
	}

	fsys, password, cleanupTransferred, err := openArchiveFSWithPasswordPrompt(ctx, finalPath, displayName, closer)
	if err != nil {
		if closer != nil && !cleanupTransferred {
			_ = closer.Close()
		}
		return nil, err
	}

	return &ArchiveVFS{
		parent: parent, arcPath: canonicalPath, backingPath: finalPath, displayName: displayName,
		format: format, sfxOffset: sfxOffset, sfxSuffix: sfxSuffix,
		password: password, innerPath: ".", fsys: fsys, closer: closer,
	}, nil
}

type archiveFSOpenResult struct {
	fsys archive.FileSystem
	err  error
}

func openArchiveFSWithContext(ctx context.Context, localPath, displayName string, backing io.Closer, password string) (archive.FileSystem, bool, error) {
	result := make(chan archiveFSOpenResult, 1)
	go func() {
		fsys, err := openArchiveFileSystem(ctx, localPath, displayName, password)
		result <- archiveFSOpenResult{fsys: fsys, err: err}
	}()

	update, reporter := archiveProgressTargets(ctx)
	ticker := time.NewTicker(ProgressTickerInterval)
	defer ticker.Stop()
	for {
		select {
		case opened := <-result:
			if err := archiveOperationCancelled(ctx, reporter); err != nil {
				if opened.fsys != nil {
					_ = opened.fsys.Close()
				}
				return nil, false, err
			}
			if opened.err == nil {
				if update != nil {
					update("Opening archive...", 100)
				}
				if reporter != nil {
					reporter.UpdateTransfer("Opening", displayName, 100, "Archive ready", 100, "")
				}
			}
			return opened.fsys, false, opened.err
		case <-ctx.Done():
			// OpenFS has no context-aware entry point. Return cancellation now,
			// then close its late result so neither a decoder nor an fd leaks.
			go func() {
				opened := <-result
				if opened.fsys != nil {
					_ = opened.fsys.Close()
				}
				if backing != nil {
					_ = backing.Close()
				}
			}()
			return nil, backing != nil, ctx.Err()
		case <-ticker.C:
			if err := archiveOperationCancelled(ctx, reporter); err != nil {
				go func() {
					opened := <-result
					if opened.fsys != nil {
						_ = opened.fsys.Close()
					}
					if backing != nil {
						_ = backing.Close()
					}
				}()
				return nil, backing != nil, err
			}
			if update != nil {
				update("Opening archive...", -1)
			}
			if reporter != nil {
				reporter.UpdateTransfer("Opening", displayName, -1, "Reading archive index...", -1, "")
			}
		}
	}
}

func cleanArchiveRootPath(value string) string {
	if vfs.IsURIPath(value) {
		return strings.TrimRight(value, "\\/")
	}
	return filepath.Clean(value)
}

func archivePathHasPrefix(candidate, root string) bool {
	_, ok := archiveRelativePath(candidate, root)
	return ok
}

func archiveRelativePath(candidate, root string) (string, bool) {
	if vfs.IsURIPath(root) {
		if candidate == root {
			return ".", true
		}
		if !strings.HasPrefix(candidate, root) || len(candidate) <= len(root) {
			return "", false
		}
		next := candidate[len(root)]
		if next != '/' && next != '\\' {
			return "", false
		}
		return strings.TrimLeft(candidate[len(root):], "\\/"), true
	}
	cleanRoot := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(root, "\\", "/")))
	cleanCandidate := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(candidate, "\\", "/")))
	relative, err := filepath.Rel(cleanRoot, cleanCandidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func archivePathJoin(root, inner string) string {
	inner = strings.TrimLeft(strings.ReplaceAll(inner, "\\", "/"), "/")
	inner = path.Clean(inner)
	if inner == "." || inner == "" {
		return root
	}
	if vfs.IsURIPath(root) {
		return strings.TrimRight(root, "\\/") + "/" + inner
	}
	return filepath.Join(root, filepath.FromSlash(inner))
}

func (v *ArchiveVFS) resolveInnerPath(candidate string) (string, error) {
	root := v.arcPath
	if candidate == "" || candidate == "." {
		if v.innerPath == "" {
			return ".", nil
		}
		return v.innerPath, nil
	}
	if relative, owned := archiveRelativePath(candidate, root); owned {
		if relative == "." {
			return ".", nil
		}
		return cleanArchiveInnerPath(relative)
	}
	if vfs.IsURIPath(candidate) || filepath.IsAbs(candidate) || path.IsAbs(candidate) || filepath.VolumeName(candidate) != "" {
		return "", fmt.Errorf("path escapes archive: %s", candidate)
	}
	inner := path.Join(v.innerPath, strings.ReplaceAll(candidate, "\\", "/"))
	return cleanArchiveInnerPath(inner)
}

func cleanArchiveInnerPath(inner string) (string, error) {
	inner = path.Clean(strings.TrimLeft(strings.ReplaceAll(inner, "\\", "/"), "/"))
	if inner == "" || inner == "." {
		return ".", nil
	}
	if inner == ".." || strings.HasPrefix(inner, "../") {
		return "", fmt.Errorf("path escapes archive root")
	}
	return inner, nil
}

// cleanArchiveExtractionPath converts an archive member name to the only form
// which may be matched or joined to an extraction destination. Archive member
// names are untrusted: filepath.Join would otherwise let an entry such as
// "folder/../../outside" escape the selected destination.
func cleanArchiveExtractionPath(name string) (string, error) {
	if strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("archive entry path contains NUL")
	}

	normalized := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(normalized, "/") || hasWindowsArchiveVolume(normalized) {
		return "", fmt.Errorf("archive entry path is absolute")
	}

	cleaned := path.Clean(normalized)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("archive entry path escapes archive root")
	}
	return cleaned, nil
}

func hasWindowsArchiveVolume(name string) bool {
	return len(name) >= 2 && name[1] == ':' &&
		((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z'))
}

func archiveExtractionPathSelected(name string, selected map[string]bool) bool {
	for selectedPath := range selected {
		if selectedPath == "." || name == selectedPath || strings.HasPrefix(name, selectedPath+"/") {
			return true
		}
	}
	return false
}

func archiveExtractionRelativePath(name, innerPath string) (string, error) {
	cleanInner, err := cleanArchiveExtractionPath(innerPath)
	if err != nil {
		return "", err
	}
	if cleanInner == "." {
		return name, nil
	}
	if name == cleanInner {
		return ".", nil
	}
	prefix := cleanInner + "/"
	if !strings.HasPrefix(name, prefix) {
		return "", fmt.Errorf("archive entry path is outside the current archive folder")
	}
	return strings.TrimPrefix(name, prefix), nil
}

func archiveExtractionTarget(dstVfs vfs.VFS, dstDir, name, innerPath string) (string, error) {
	relative, err := archiveExtractionRelativePath(name, innerPath)
	if err != nil {
		return "", err
	}
	relative, err = cleanArchiveExtractionPath(relative)
	if err != nil {
		return "", err
	}

	target := dstVfs.Join(dstDir, filepath.FromSlash(relative))
	baseAbs, err := dstVfs.Abs(dstDir)
	if err != nil {
		return "", fmt.Errorf("resolve archive extraction destination: %w", err)
	}
	targetAbs, err := dstVfs.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve archive extraction target: %w", err)
	}
	resolvedRelative, inside := archiveRelativePath(targetAbs, baseAbs)
	if !inside || (resolvedRelative == "." && relative != ".") {
		return "", fmt.Errorf("archive entry path escapes extraction destination")
	}
	return target, nil
}

func (v *ArchiveVFS) GetPath() string {
	if v.innerPath == "." || v.innerPath == "" {
		return archivePathJoin(v.arcPath, ".")
	}
	// Мы возвращаем нативный путь ОС, объединяя путь к архиву и внутренний путь
	return archivePathJoin(v.arcPath, v.innerPath)
}

// LocalArchivePath returns the original archive path when this VFS was opened
// from the local filesystem. A materialized remote or nested archive is not
// exposed as local here: testing it through this action belongs to a separate
// operation with different lifecycle and error-reporting rules.
func (v *ArchiveVFS) LocalArchivePath() (string, bool) {
	osvfs, ok := v.parent.(*vfs.OSVFS)
	if !ok {
		return "", false
	}
	path, err := osvfs.Abs(v.arcPath)
	if err != nil || path == "" {
		return "", false
	}
	return path, true
}

// selectedTestPaths turns the panel's marked names -- relative to the
// directory currently being browsed inside the archive -- into the archive
// root-relative set actionTestArchive restricts a test to. It builds that set
// exactly the way copyBulkFrom does for "extract marked only" below, so a
// marked directory's whole subtree is tested, not only the entry itself. A
// name that fails cleanArchiveExtractionPath is dropped rather than aborting
// the test: marked names always come from entries the panel already listed.
// Empty names yields a nil map, meaning "test the whole archive" (f4#1250).
func (v *ArchiveVFS) selectedTestPaths(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	v.mu.Lock()
	innerPath := v.innerPath
	v.mu.Unlock()

	selected := make(map[string]bool, len(names))
	for _, name := range names {
		fullInner := strings.ReplaceAll(name, "\\", "/")
		if innerPath != "." && innerPath != "" {
			fullInner = path.Join(innerPath, fullInner)
		}
		cleanSelected, err := cleanArchiveExtractionPath(fullInner)
		if err != nil {
			continue
		}
		selected[cleanSelected] = true
	}
	return selected
}

func (v *ArchiveVFS) IsAbs(candidate string) bool {
	return archivePathHasPrefix(candidate, v.arcPath) || (!vfs.IsURIPath(candidate) && (filepath.IsAbs(candidate) || path.IsAbs(candidate)))
}

func (v *ArchiveVFS) SetPath(p string) error {
	v.mu.Lock()
	if err := v.ensureFSLocked(); err != nil {
		v.finishNonHandleOperationLocked()
		v.mu.Unlock()
		if archive.IsPasswordError(err) {
			if retryErr := v.openWithPassword(context.Background(), err); retryErr == nil {
				return v.SetPath(p)
			} else {
				return retryErr
			}
		}
		return err
	}
	v.cancelCleanupLocked()

	newInner, err := v.resolveInnerPath(p)
	if err != nil {
		v.finishNonHandleOperationLocked()
		v.mu.Unlock()
		return err
	}

	if v.fsys != nil && newInner != "." {
		info, err := fs.Stat(v.fsys, newInner)
		if err != nil {
			v.finishNonHandleOperationLocked()
			v.mu.Unlock()
			if archive.IsPasswordError(err) {
				if retryErr := v.openWithPassword(context.Background(), err); retryErr == nil {
					return v.SetPath(p)
				} else {
					return retryErr
				}
			}
			return err
		}
		if !info.IsDir() {
			v.finishNonHandleOperationLocked()
			v.mu.Unlock()
			return fmt.Errorf("not a directory: %s", newInner)
		}
	}

	v.innerPath = newInner
	v.finishNonHandleOperationLocked()
	v.mu.Unlock()
	return nil
}

func (v *ArchiveVFS) ReadDir(ctx context.Context, path string, onChunk func([]vfs.VFSItem)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v.mu.Lock()
	if err := v.ensureFSLocked(); err != nil {
		v.finishNonHandleOperationLocked()
		v.mu.Unlock()
		if archive.IsPasswordError(err) {
			if retryErr := v.openWithPassword(ctx, err); retryErr == nil {
				return v.ReadDir(ctx, path, onChunk)
			} else {
				return retryErr
			}
		}
		return err
	}
	v.cancelCleanupLocked()
	closedView := v.isClosed
	fsPath, pathErr := v.resolveInnerPath(path)
	if pathErr != nil {
		v.finishNonHandleOperationLocked()
		v.mu.Unlock()
		return pathErr
	}

	entries, err := fs.ReadDir(v.fsys, fsPath)
	if err != nil {
		v.finishNonHandleOperationLocked()
		v.mu.Unlock()
		if archive.IsPasswordError(err) {
			if retryErr := v.openWithPassword(ctx, err); retryErr == nil {
				return v.ReadDir(ctx, path, onChunk)
			} else {
				return retryErr
			}
		}
		return err
	}

	items := make([]vfs.VFSItem, 0, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		name := e.Name()
		// Archive containers commonly store an explicit "./" root entry.
		// archive.FileSystem exposes it as an empty child name; returning that
		// row makes a recursive VFS scan join the root with "" and visit the
		// same directory forever (see issue #510).
		if name == "" || name == "." || name == ".." {
			continue
		}

		items = append(items, vfs.VFSItem{
			KnownMetadata: vfs.MetadataExplicit | vfs.MetadataHidden | vfs.MetadataMTime, SizeKnown: true,
			Name:     name,
			IsDir:    e.IsDir(),
			Size:     info.Size(),
			MTime:    info.ModTime(),
			IsHidden: strings.HasPrefix(name, "."),
		})
	}
	if closedView {
		v.finishNonHandleOperationLocked()
	}
	v.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if onChunk != nil {
		onChunk(items)
	}
	return nil
}

func (v *ArchiveVFS) Stat(ctx context.Context, path string) (vfs.VFSItem, error) {
	if err := ctx.Err(); err != nil {
		return vfs.VFSItem{}, err
	}
	v.mu.Lock()
	if err := v.ensureFSLocked(); err != nil {
		v.finishNonHandleOperationLocked()
		v.mu.Unlock()
		if archive.IsPasswordError(err) {
			if retryErr := v.openWithPassword(ctx, err); retryErr == nil {
				return v.Stat(ctx, path)
			} else {
				return vfs.VFSItem{}, retryErr
			}
		}
		return vfs.VFSItem{}, err
	}
	v.cancelCleanupLocked()

	fsPath, pathErr := v.resolveInnerPath(path)
	if pathErr != nil {
		v.finishNonHandleOperationLocked()
		v.mu.Unlock()
		return vfs.VFSItem{}, pathErr
	}

	info, err := fs.Stat(v.fsys, fsPath)
	if err != nil {
		v.finishNonHandleOperationLocked()
		v.mu.Unlock()
		if archive.IsPasswordError(err) {
			if retryErr := v.openWithPassword(ctx, err); retryErr == nil {
				return v.Stat(ctx, path)
			} else {
				return vfs.VFSItem{}, retryErr
			}
		}
		return vfs.VFSItem{}, err
	}

	item := vfs.VFSItem{
		KnownMetadata: vfs.MetadataExplicit | vfs.MetadataHidden | vfs.MetadataMTime, SizeKnown: true,
		Name:     info.Name(),
		IsDir:    info.IsDir(),
		Size:     info.Size(),
		MTime:    info.ModTime(),
		IsHidden: strings.HasPrefix(info.Name(), "."),
	}
	v.finishNonHandleOperationLocked()
	v.mu.Unlock()
	return item, nil
}

type archiveReadWrapper struct {
	v          *ArchiveVFS
	once       sync.Once
	mu         sync.Mutex
	f          fs.File
	fsPath     string
	size       int64
	crc32      uint32
	hasCRC32   bool
	tmpFile    *os.File
	tmpPath    string
	extracted  bool
	extracting bool
	doneChan   chan struct{}
	err        error
	readPos    int64
}

func archiveFileCRC(info fs.FileInfo) (uint32, bool) {
	if info == nil {
		return 0, false
	}
	switch header := info.Sys().(type) {
	case *sevenzip.FileHeader:
		if header != nil && header.CRC32 != 0 {
			return header.CRC32, true
		}
	case sevenzip.FileHeader:
		if header.CRC32 != 0 {
			return header.CRC32, true
		}
	}
	return 0, false
}

// reopenAfterPassword replaces the member handle after the archive backend
// reports an encrypted payload while reading it. Some formats validate the
// password lazily, so Open may succeed and the first Read may be the point at
// which the password is actually needed.
func (w *archiveReadWrapper) reopenAfterPassword(ctx context.Context, cause error) error {
	if !isArchivePasswordRetryError(cause) || w.v == nil {
		return cause
	}
	if err := w.v.openWithPassword(ctx, cause); err != nil {
		return err
	}

	w.v.mu.Lock()
	fsys := w.v.fsys
	w.v.mu.Unlock()
	if fsys == nil {
		return errors.New("archive filesystem is unavailable after password entry")
	}

	replacement, err := fsys.Open(w.fsPath)
	if err != nil {
		return err
	}

	w.mu.Lock()
	old := w.f
	w.f = replacement
	w.mu.Unlock()
	if old != nil {
		_ = old.Close() // The replaced archive member was read-only.
	}
	return nil
}

func seekArchiveFile(file fs.File, offset int64) error {
	if offset <= 0 {
		return nil
	}
	if seeker, ok := file.(io.Seeker); ok {
		_, err := seeker.Seek(offset, io.SeekStart)
		return err
	}

	discard := make([]byte, 32*1024)
	for offset > 0 {
		want := int64(len(discard))
		if want > offset {
			want = offset
		}
		n, err := file.Read(discard[:want])
		offset -= int64(n)
		if err != nil {
			if err == io.EOF && offset > 0 {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}

func (w *archiveReadWrapper) Size() int64 {
	return w.size
}

func (w *archiveReadWrapper) Close() error {
	w.once.Do(func() {
		w.mu.Lock()
		if w.f != nil {
			_ = w.f.Close() // The archive member was opened only for reading.
			w.f = nil
		}
		if w.tmpFile != nil {
			_ = w.tmpFile.Close()    // Materialization is read-only after it is published.
			_ = os.Remove(w.tmpPath) // Removing a private read cache is best-effort cleanup.
			w.tmpFile = nil
		}
		w.mu.Unlock()
		w.v.decrementActive()
	})
	return nil
}

func (w *archiveReadWrapper) TempPath() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tmpPath
}

func (w *archiveReadWrapper) LocalPath() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tmpPath, w.extracted && w.tmpPath != ""
}

func (w *archiveReadWrapper) extractToTemp(ctx context.Context) error {
	// Keep the tar.FileSystem path first: it can use zipper's external or
	// embedded gzip index for O(1)-ish random access. Only a failed compressed
	// read is retried through the sequential extractor below.
	randomErr := w.extractToTempRandom(ctx)
	if randomErr == nil {
		return nil
	}
	if !w.canFallbackToSequential(randomErr) {
		return randomErr
	}
	if err := w.extractToTempSequential(ctx); err != nil {
		return errors.Join(randomErr, err)
	}
	return nil
}

func (w *archiveReadWrapper) extractToTempRandom(ctx context.Context) error {
	w.mu.Lock()
	v := w.v
	fsPath := w.fsPath
	w.mu.Unlock()

	var src io.Reader
	var srcCloser io.Closer

	w.mu.Lock()
	if seeker, ok := w.f.(io.Seeker); ok {
		_, err := seeker.Seek(0, io.SeekStart)
		if err == nil {
			src = w.f
		}
	}
	w.mu.Unlock()

	if src == nil && v != nil {
		v.mu.Lock()
		fsys := v.fsys
		v.mu.Unlock()
		if fsys != nil {
			fNew, err := fsys.Open(fsPath)
			if err == nil {
				src = fNew
				srcCloser = fNew
			}
		}
	}

	if src == nil {
		w.mu.Lock()
		src = w.f
		w.mu.Unlock()
	}

	if src == nil {
		err := fmt.Errorf("no source file available for extraction")
		w.mu.Lock()
		w.err = err
		w.mu.Unlock()
		return err
	}

	tmp, err := os.CreateTemp("", "f4arc-*")
	if err != nil {
		if srcCloser != nil {
			_ = srcCloser.Close() // The fallback archive member is read-only.
		}
		w.mu.Lock()
		w.err = err
		w.mu.Unlock()
		return err
	}

	buf := make([]byte, 128*1024)
	var loopErr error
	var copied int64
	var checksum hash.Hash32
	if w.hasCRC32 {
		checksum = crc32.NewIEEE()
	}
	for {
		if ctx.Err() != nil {
			loopErr = ctx.Err()
			break
		}
		n, errRead := src.Read(buf)
		errRead = w.v.memberReadError(errRead)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				loopErr = werr
				break
			}
			if checksum != nil {
				_, _ = checksum.Write(buf[:n])
			}
			copied += int64(n)
		}
		if errRead != nil {
			if errRead == io.EOF && w.hasCRC32 && w.size >= 0 && copied != w.size {
				errRead = newArchivePasswordValidationError("extracted %d bytes, want %d", copied, w.size)
			}
			if errRead == io.EOF && checksum != nil && checksum.Sum32() != w.crc32 {
				errRead = newArchivePasswordValidationError("extracted data checksum does not match")
			}
			// Keep re-prompting like FAR does: the user decides when to stop
			// by closing the password dialog, which ends the retry loop.
			if errRead != io.EOF && isArchivePasswordRetryError(errRead) {
				if srcCloser != nil {
					_ = srcCloser.Close() // The fallback archive member was read-only.
					srcCloser = nil
				}
				if retryErr := w.reopenAfterPassword(ctx, errRead); retryErr == nil {
					w.mu.Lock()
					src = w.f
					w.mu.Unlock()
					if seeker, ok := src.(io.Seeker); ok {
						if _, err := seeker.Seek(0, io.SeekStart); err != nil {
							loopErr = err
							break
						}
					}
					if _, err := tmp.Seek(0, io.SeekStart); err != nil {
						loopErr = err
						break
					}
					if err := tmp.Truncate(0); err != nil {
						loopErr = err
						break
					}
					copied = 0
					if checksum != nil {
						checksum = crc32.NewIEEE()
					}
					continue
				} else {
					loopErr = retryErr
					break
				}
			}
			if errRead != io.EOF && loopErr == nil {
				loopErr = errRead
			}
			break
		}
	}

	if srcCloser != nil {
		_ = srcCloser.Close() // The fallback archive member is read-only.
	}

	w.mu.Lock()
	readPos := w.readPos
	w.mu.Unlock()

	tmpName := tmp.Name()
	if loopErr == nil {
		loopErr = tmp.Close()
	} else {
		_ = tmp.Close() // The incomplete private materialization will be removed.
	}

	var readTmp *os.File
	if loopErr == nil {
		readTmp, loopErr = os.Open(filepath.Clean(tmpName))
	}
	if loopErr == nil {
		_, loopErr = readTmp.Seek(readPos, io.SeekStart)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if loopErr != nil {
		if readTmp != nil {
			_ = readTmp.Close() // The materialized member was reopened only for reading.
		}
		_ = os.Remove(tmpName) // Removing the unusable private materialization is best-effort cleanup.
		if !errors.Is(loopErr, context.Canceled) && !errors.Is(loopErr, context.DeadlineExceeded) {
			if w.f != nil {
				_ = w.f.Close() // The archive member was opened only for reading.
				w.f = nil
			}
		}
		return loopErr
	} else {
		if w.f != nil {
			_ = w.f.Close() // The archive member was opened only for reading.
			w.f = nil
		}
		w.tmpPath = tmpName
		w.tmpFile = readTmp
		w.extracted = true
		return nil
	}
}

func (w *archiveReadWrapper) canFallbackToSequential(err error) bool {
	if err == nil || isArchivePasswordRetryError(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "corrupt input") && !strings.Contains(message, "unexpected eof") && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}

	w.mu.Lock()
	v := w.v
	w.mu.Unlock()
	if v == nil {
		return false
	}
	v.mu.Lock()
	format := v.format
	archiveName := v.displayName
	v.mu.Unlock()
	if format == "" {
		format = archive.DetectFormat(archiveName)
	}
	return format == "tar" && !strings.HasSuffix(strings.ToLower(archiveName), ".tar")
}

func (w *archiveReadWrapper) extractToTempSequential(ctx context.Context) error {
	w.mu.Lock()
	v := w.v
	fsPath := w.fsPath
	readPos := w.readPos
	w.mu.Unlock()
	if v == nil {
		return errors.New("archive VFS is unavailable for sequential extraction")
	}

	v.mu.Lock()
	password := v.password
	v.mu.Unlock()
	localFile, extractor, err := v.openBulkExtractor(ctx, w, password)
	if err != nil {
		return err
	}
	defer func() { _ = localFile.Close() }()

	tmp, err := os.CreateTemp("", "f4arc-open-fallback-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()

	found := false
	err = extractor.Extract(ctx, localFile, func(ctx context.Context, info archives.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		cleanName, err := cleanArchiveExtractionPath(info.NameInArchive)
		if err != nil {
			return fmt.Errorf("unsafe archive entry %q: %w", info.NameInArchive, err)
		}
		if cleanName != fsPath {
			return nil
		}
		if info.IsDir() {
			return fmt.Errorf("archive member is a directory: %s", fsPath)
		}
		member, err := info.Open()
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tmp, member)
		closeErr := member.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		found = true
		return fs.SkipAll
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("archive member not found: %s", fsPath)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	readTmp, err := os.Open(filepath.Clean(tmpName))
	if err != nil {
		return err
	}
	if _, err := readTmp.Seek(readPos, io.SeekStart); err != nil {
		_ = readTmp.Close() // The materialized archive member was reopened only for reading.
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		_ = w.f.Close() // The archive member was opened only for reading.
		w.f = nil
	}
	if w.size <= 0 {
		if info, statErr := readTmp.Stat(); statErr == nil {
			w.size = info.Size()
		}
	}
	w.tmpPath = tmpName
	w.tmpFile = readTmp
	w.extracted = true
	removeTemp = false
	return nil
}

func (w *archiveReadWrapper) materialize(ctx context.Context, sequential bool) error {
	for {
		w.mu.Lock()
		if w.extracted {
			w.mu.Unlock()
			return nil
		}
		if w.err != nil {
			err := w.err
			w.mu.Unlock()
			return err
		}
		if w.extracting {
			ch := w.doneChan
			w.mu.Unlock()
			select {
			case <-ch:
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		w.extracting = true
		w.doneChan = make(chan struct{})
		w.mu.Unlock()

		var err error
		if sequential {
			err = w.extractToTempSequential(ctx)
		} else {
			err = w.extractToTemp(ctx)
		}

		w.mu.Lock()
		w.extracting = false
		close(w.doneChan)
		w.doneChan = nil
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			w.err = err
		}
		w.mu.Unlock()
		return err
	}
}

func (v *ArchiveVFS) fallbackOpenMemberSequential(ctx context.Context, fsPath string, size int64, cause error) (vfs.ReadAtCloser, error, bool) {
	fallback := &archiveReadWrapper{v: v, fsPath: fsPath, size: size}
	if !fallback.canFallbackToSequential(cause) {
		return nil, cause, false
	}
	if err := fallback.extractToTempSequential(ctx); err != nil {
		return nil, errors.Join(cause, err), true
	}
	return fallback, nil, true
}

func (w *archiveReadWrapper) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if err := w.materialize(ctx, false); err != nil {
		return 0, err
	}
	w.mu.Lock()
	tmp := w.tmpFile
	w.mu.Unlock()

	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return tmp.ReadAt(p, off)
}

func (w *archiveReadWrapper) Read(ctx context.Context, p []byte) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	w.mu.Lock()
	if w.err != nil {
		w.mu.Unlock()
		return 0, w.err
	}
	if w.extracted {
		tmp := w.tmpFile
		w.mu.Unlock()
		return tmp.Read(p)
	}

	f := w.f
	v := w.v
	w.mu.Unlock()

	// 7z may return a full-sized, garbage payload with EOF for a wrong
	// password when its headers remain visible, and a ZipCrypto member whose
	// wrong password slipped past the one-byte check yields garbage until the
	// CRC is compared at EOF. Materialize such members before exposing bytes
	// so the size/CRC checks can retry without leaking data to the caller.
	// Unencrypted TAR/ZIP keep their lazy and random-access paths unchanged.
	if v != nil && (strings.EqualFold(filepath.Ext(v.displayName), ".7z") || v.passwordInstalled()) {
		if err := w.materialize(ctx, false); err != nil {
			return 0, err
		}
		w.mu.Lock()
		tmp := w.tmpFile
		w.mu.Unlock()
		return tmp.Read(p)
	}

	var n int
	var err error
	for {
		n, err = f.Read(p)
		err = w.v.memberReadError(err)
		if n > 0 {
			w.mu.Lock()
			w.readPos += int64(n)
			w.mu.Unlock()
		}
		if err == io.EOF {
			w.mu.Lock()
			shortRead := w.hasCRC32 && w.size >= 0 && w.readPos < w.size
			readPos := w.readPos
			w.mu.Unlock()
			if shortRead {
				err = newArchivePasswordValidationError("extracted %d bytes, want %d", readPos, w.size)
			}
		}
		if err == nil || !isArchivePasswordRetryError(err) {
			break
		}
		if n > 0 {
			w.mu.Lock()
			w.readPos -= int64(n)
			w.mu.Unlock()
		}
		if retryErr := w.reopenAfterPassword(ctx, err); retryErr != nil {
			return 0, retryErr
		}
		w.mu.Lock()
		f = w.f
		position := w.readPos
		w.mu.Unlock()
		if err := seekArchiveFile(f, position); err != nil {
			return 0, err
		}
	}
	if err != nil && w.canFallbackToSequential(err) {
		if n > 0 {
			w.mu.Lock()
			w.readPos -= int64(n)
			w.mu.Unlock()
		}
		if fallbackErr := w.materialize(ctx, true); fallbackErr == nil {
			w.mu.Lock()
			tmp := w.tmpFile
			w.mu.Unlock()
			return tmp.Read(p)
		} else if n > 0 {
			w.mu.Lock()
			w.readPos += int64(n)
			w.mu.Unlock()
		}
	}
	return n, err
}

func formatSize(b int64) string {
	if b < 0 {
		return "?"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func joinArchiveCloseError(primary, closeErr error) error {
	if primary == nil {
		return closeErr
	}
	if closeErr == nil {
		return primary
	}
	return errors.Join(primary, closeErr)
}

func extractWithProgress(ctx context.Context, src io.Reader, dst io.Writer, size int64, name string, update vfs.ProgressCallback, reporter vfs.TaskReporter) error {
	buf := make([]byte, 128*1024)
	var copied int64
	startTime := time.Now()
	lastUpdate := startTime

	done := make(chan struct{})
	defer close(done)

	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		dots := ""
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if atomic.LoadInt64(&copied) == 0 {
					dots += "."
					if len(dots) > 3 {
						dots = ""
					}
					msg := fmt.Sprintf("Seeking/Decompressing%s", dots)
					if update != nil {
						update(msg, -1)
					}
					if reporter != nil {
						elapsed := time.Since(startTime)
						elapsedStr := fmt.Sprintf("Time: %02d:%02d:%02d", int(elapsed.Hours()), int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)
						reporter.UpdateTransfer("Extracting", name, -1, msg, -1, elapsedStr)
					}
				}
			}
		}
	}()

	report := func(current int64, percent int) {
		if update != nil {
			update(fmt.Sprintf("Extracting %s...", name), percent)
		}
		if reporter != nil {
			elapsed := time.Since(startTime)
			elapsedStr := fmt.Sprintf("Time: %02d:%02d:%02d", int(elapsed.Hours()), int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)
			reporter.UpdateTransfer("Extracting", name, percent, fmt.Sprintf("Extracting: %s / %s", formatSize(current), formatSize(size)), percent, elapsedStr)
		}
	}
	report(0, 0)

	for {
		if err := archiveOperationCancelled(ctx, reporter); err != nil {
			return err
		}
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			atomic.AddInt64(&copied, int64(n))

			now := time.Now()
			if now.Sub(lastUpdate) > 50*time.Millisecond || err != nil {
				lastUpdate = now
				pct := 0
				currentCopied := atomic.LoadInt64(&copied)
				if size > 0 {
					pct = int((currentCopied * 100) / size)
					if pct > 100 {
						pct = 100
					}
				}
				report(currentCopied, pct)
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}
	report(atomic.LoadInt64(&copied), 100)
	return nil
}

func (v *ArchiveVFS) Open(ctx context.Context, path string) (vfs.ReadAtCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v.mu.Lock()
	if err := v.ensureFSLocked(); err != nil {
		v.mu.Unlock()
		if archive.IsPasswordError(err) {
			if retryErr := v.openWithPassword(ctx, err); retryErr == nil {
				return v.Open(ctx, path)
			} else {
				return nil, retryErr
			}
		}
		return nil, err
	}
	v.cancelCleanupLocked()
	fsPath, pathErr := v.resolveInnerPath(path)
	if pathErr != nil {
		v.mu.Unlock()
		return nil, pathErr
	}

	// Capture fsys and increment active count EARLY to protect it while unlocked.
	v.activeCount++
	fsys := v.fsys
	v.mu.Unlock()

	var update vfs.ProgressCallback
	if val := ctx.Value(vfs.ProgressKey); val != nil {
		if cb, ok := val.(vfs.ProgressCallback); ok {
			update = cb
		}
	}

	var reporter vfs.TaskReporter
	if val := ctx.Value(vfs.ReporterKey); val != nil {
		if r, ok := val.(vfs.TaskReporter); ok {
			reporter = r
		}
	}

	startTime := time.Now()
	openDone := make(chan struct{})
	go func() {
		if update == nil && reporter == nil {
			return
		}
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		dots := ""
		for {
			select {
			case <-openDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				dots += "."
				if len(dots) > 3 {
					dots = ""
				}
				msg := fmt.Sprintf("Locating file%s", dots)
				if update != nil {
					update(msg, -1)
				}
				if reporter != nil {
					elapsed := time.Since(startTime)
					elapsedStr := fmt.Sprintf("Time: %02d:%02d:%02d", int(elapsed.Hours()), int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)
					reporter.UpdateTransfer("Opening", v.Base(path), -1, msg, -1, elapsedStr)
				}
			}
		}
	}()
	cancelPoll := time.NewTicker(100 * time.Millisecond)
	defer cancelPoll.Stop()

	type openResult struct {
		file fs.File
		err  error
	}
	result := make(chan openResult, 1)
	go func() {
		file, err := fsys.Open(fsPath)
		result <- openResult{file: file, err: err}
	}()
	var srcFile fs.File
	var err error
	for srcFile == nil {
		select {
		case opened := <-result:
			srcFile, err = opened.file, opened.err
			if err == nil && srcFile == nil {
				err = fmt.Errorf("archive returned an empty file handle")
			}
			if err != nil {
				close(openDone)
				if srcFile != nil {
					_ = srcFile.Close() // The archive member was opened only for reading.
					srcFile = nil
				}
				if fallback, fallbackErr, attempted := v.fallbackOpenMemberSequential(ctx, fsPath, 0, err); attempted && fallbackErr == nil {
					return fallback, nil
				} else if attempted {
					err = fallbackErr
				}
				v.decrementActive()
				if archive.IsPasswordError(err) {
					if retryErr := v.openWithPassword(ctx, err); retryErr == nil {
						return v.Open(ctx, path)
					} else {
						return nil, retryErr
					}
				}
				return nil, err
			}
		case <-ctx.Done():
			close(openDone)
			go func() {
				opened := <-result
				if opened.file != nil {
					_ = opened.file.Close()
				}
				v.decrementActive()
			}()
			return nil, ctx.Err()
		case <-cancelPoll.C:
			if reporter != nil && reporter.IsCancelled() {
				close(openDone)
				go func() {
					opened := <-result
					if opened.file != nil {
						_ = opened.file.Close()
					}
					v.decrementActive()
				}()
				return nil, context.Canceled
			}
		}
	}
	close(openDone)

	info, err := srcFile.Stat()
	var size int64
	var expectedCRC uint32
	var hasExpectedCRC bool
	if err == nil && info != nil {
		size = info.Size()
		expectedCRC, hasExpectedCRC = archiveFileCRC(info)
	}

	if update != nil || reporter != nil {
		tmp, errTemp := os.CreateTemp("", "f4arc-open-*")
		if errTemp != nil {
			_ = srcFile.Close() // The archive member was opened only for reading.
			v.decrementActive()
			return nil, errTemp
		}
		tmpName := tmp.Name()

		fileName := "unknown"
		if info != nil {
			fileName = info.Name()
		}
		errExtract := v.memberReadError(extractWithProgress(ctx, srcFile, tmp, size, fileName, update, reporter))
		_ = srcFile.Close() // The archive member was opened only for reading.
		if errExtract == nil && hasExpectedCRC {
			if _, err := tmp.Seek(0, io.SeekStart); err != nil {
				errExtract = err
			} else {
				h := crc32.NewIEEE()
				_, copyErr := io.Copy(h, tmp)
				if copyErr != nil {
					errExtract = copyErr
				} else if h.Sum32() != expectedCRC {
					errExtract = newArchivePasswordValidationError("extracted data checksum does not match")
				}
			}
		}

		if errExtract != nil {
			_ = tmp.Close()        // The incomplete private materialization will be removed.
			_ = os.Remove(tmpName) // Removing the unusable private materialization is best-effort cleanup.
			if fallback, fallbackErr, attempted := v.fallbackOpenMemberSequential(ctx, fsPath, size, errExtract); attempted && fallbackErr == nil {
				return fallback, nil
			} else if attempted {
				errExtract = fallbackErr
			}
			v.decrementActive()
			if isArchivePasswordRetryError(errExtract) {
				if retryErr := v.openWithPassword(ctx, errExtract); retryErr == nil {
					return v.Open(ctx, path)
				} else {
					return nil, retryErr
				}
			}
			return nil, errExtract
		}

		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmpName) // Removing the unusable private materialization is best-effort cleanup.
			v.decrementActive()
			return nil, err
		}
		tmp, err = os.Open(filepath.Clean(tmpName))
		if err != nil {
			_ = os.Remove(tmpName) // Removing the unusable private materialization is best-effort cleanup.
			v.decrementActive()
			return nil, err
		}

		return &archiveReadWrapper{
			v:         v,
			size:      size,
			crc32:     expectedCRC,
			hasCRC32:  hasExpectedCRC,
			tmpFile:   tmp,
			tmpPath:   tmpName,
			extracted: true,
		}, nil
	}

	return &archiveReadWrapper{
		v:        v,
		f:        srcFile,
		fsPath:   fsPath,
		size:     size,
		crc32:    expectedCRC,
		hasCRC32: hasExpectedCRC,
	}, nil
}

func (v *ArchiveVFS) ParentVFS() vfs.VFS { return v.parent }
func (v *ArchiveVFS) Join(elements ...string) string {
	if len(elements) == 0 {
		return ""
	}
	if relative, owned := archiveRelativePath(elements[0], v.arcPath); owned {
		inner := relative
		for _, element := range elements[1:] {
			inner = path.Join(inner, strings.ReplaceAll(element, "\\", "/"))
		}
		clean, err := cleanArchiveInnerPath(inner)
		if err != nil {
			return v.arcPath
		}
		return archivePathJoin(v.arcPath, clean)
	}
	if vfs.IsURIPath(elements[0]) {
		joined := elements[0]
		for _, element := range elements[1:] {
			if element == "" || element == "." {
				continue
			}
			joined = archivePathJoin(joined, element)
		}
		return joined
	}
	return filepath.Join(elements...)
}
func (v *ArchiveVFS) Abs(candidate string) (string, error) {
	if _, owned := archiveRelativePath(candidate, v.arcPath); owned {
		return candidate, nil
	}
	if vfs.IsURIPath(candidate) {
		return "", fmt.Errorf("path escapes archive: %s", candidate)
	}
	if filepath.IsAbs(candidate) || path.IsAbs(candidate) {
		return filepath.Clean(candidate), nil
	}
	return filepath.Clean(v.Join(v.GetPath(), candidate)), nil
}
func (v *ArchiveVFS) Base(candidate string) string {
	if candidate == v.arcPath {
		return v.parent.Base(v.arcPath)
	}
	if relative, owned := archiveRelativePath(candidate, v.arcPath); owned {
		return path.Base(relative)
	}
	return filepath.Base(candidate)
}
func (v *ArchiveVFS) Dir(candidate string) string {
	if candidate == v.arcPath {
		return v.parent.Dir(v.arcPath)
	}
	if relative, owned := archiveRelativePath(candidate, v.arcPath); owned {
		parent := path.Dir(relative)
		return archivePathJoin(v.arcPath, parent)
	}
	return filepath.Dir(candidate)
}

func (v *ArchiveVFS) Create(ctx context.Context, path string) (io.WriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, local := v.parent.(*vfs.OSVFS); !local {
		return nil, fmt.Errorf("remote and nested archives are read-only")
	}
	v.mu.Lock()
	if err := v.ensureFSLocked(); err != nil {
		v.mu.Unlock()
		return nil, err
	}
	v.cancelCleanupLocked()

	fsPath, pathErr := v.resolveInnerPath(path)
	if pathErr != nil || fsPath == "." {
		v.mu.Unlock()
		if pathErr != nil {
			return nil, pathErr
		}
		return nil, fmt.Errorf("cannot replace archive root")
	}

	tmp, err := os.CreateTemp("", "f4arc-write-*")
	if err != nil {
		v.mu.Unlock()
		return nil, err
	}
	v.activeCount++
	v.mu.Unlock()

	return &archiveWriteWrapper{v: v, tmpFile: tmp, destPath: fsPath}, nil
}

type archiveWriteWrapper struct {
	v        *ArchiveVFS
	tmpFile  *os.File
	destPath string
	once     sync.Once
	err      error
}

func (w *archiveWriteWrapper) Write(p []byte) (n int, err error) { return w.tmpFile.Write(p) }
func (w *archiveWriteWrapper) Close() error {
	w.once.Do(func() {
		defer w.v.decrementActive()

		tmpName := w.tmpFile.Name()
		defer func() {
			_ = os.Remove(tmpName) // The archive no longer depends on its private staging file.
		}()
		if err := w.tmpFile.Close(); err != nil {
			w.err = err
			return
		}

		w.v.mu.Lock()
		isClosed := w.v.isClosed
		w.v.mu.Unlock()

		if !isClosed {
			upd, errUpd := archive.NewUpdater(w.v.activePath(), archive.Options{})
			if errUpd == nil {
				w.err = func() (retErr error) {
					defer func() {
						retErr = joinArchiveCloseError(retErr, upd.Close())
					}()
					reader, err := os.Open(filepath.Clean(tmpName))
					if err != nil {
						return err
					}
					defer func() {
						_ = reader.Close() // The staging file is read-only after its checked write close.
					}()
					stat, err := reader.Stat()
					if err != nil {
						return err
					}
					return upd.Append(w.destPath, stat.Size(), reader)
				}()
				if w.err == nil {
					w.err = w.v.reloadFS()
				}
			} else {
				w.err = errUpd
			}
		} else {
			w.err = fmt.Errorf("archive VFS was closed")
		}
	})
	return w.err
}

func (v *ArchiveVFS) MkDir(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, local := v.parent.(*vfs.OSVFS); !local {
		return fmt.Errorf("remote and nested archives are read-only")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	defer v.finishNonHandleOperationLocked()
	if err := v.ensureFSLocked(); err != nil {
		return err
	}
	v.cancelCleanupLocked()

	fsPath, pathErr := v.resolveInnerPath(path)
	if pathErr != nil {
		return pathErr
	}
	if fsPath == "." {
		return fmt.Errorf("cannot create archive root")
	}

	if !strings.HasSuffix(fsPath, "/") {
		fsPath += "/"
	}

	upd, err := archive.NewUpdater(v.activePath(), archive.Options{})
	if err != nil {
		return err
	}

	err = joinArchiveCloseError(upd.Append(fsPath, 0, nil), upd.Close())
	if err != nil {
		return err
	}
	return v.reloadFS()
}

func (v *ArchiveVFS) Remove(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, local := v.parent.(*vfs.OSVFS); !local {
		return fmt.Errorf("remote and nested archives are read-only")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	defer v.finishNonHandleOperationLocked()
	if err := v.ensureFSLocked(); err != nil {
		return err
	}
	v.cancelCleanupLocked()

	fsPath, pathErr := v.resolveInnerPath(path)
	if pathErr != nil {
		return pathErr
	}
	if fsPath == "." {
		return fmt.Errorf("cannot remove archive root")
	}

	upd, err := archive.NewUpdater(v.activePath(), archive.Options{})
	if err != nil {
		return err
	}

	err = joinArchiveCloseError(upd.Remove(fsPath), upd.Close())
	if err != nil {
		return err
	}
	return v.reloadFS()
}

func (v *ArchiveVFS) reloadFS() error {
	newFS, err := openArchiveFileSystem(context.Background(), v.activePath(), v.displayName, v.password)
	if err != nil {
		return err
	}
	oldFS := v.fsys
	v.fsys = newFS
	if oldFS != nil {
		_ = oldFS.Close() // The replacement index is already available.
	}
	return nil
}

func (v *ArchiveVFS) Rename(ctx context.Context, o, n string) error { return fmt.Errorf("read-only") }

func (v *ArchiveVFS) SetAttributes(ctx context.Context, path string, item vfs.VFSItem) error {
	return fmt.Errorf("SetAttributes not supported for Archives yet")
}

func (v *ArchiveVFS) GetCapabilities() vfs.VFSCapabilities {
	return vfs.VFSCapabilities{HasRandomAccess: true, HasUnixPermissions: runtime.GOOS != "windows"}
}

// ReadHead serves vfs.HeadReader, and a ZIP can serve it. Whatever the
// archive file itself came from, it is read through a local backing file --
// the parent's own path when that parent is on disk, a materialization of it
// otherwise -- and a ZIP member is decoded as it is read, so asking for the
// first block costs the first block and reaches no network.
//
// Every other format declines. A member of a RAR, and of anything else that
// arrives through the generic reader, is unpacked whole into a temporary file
// before the first byte of it can be handed back: asking such a backend for
// 256 KiB would quietly charge the member's full size in time and in disk,
// and the caller asked precisely because it cannot afford that.
//
// Which it is follows the backing file and the reader that was opened over
// it, never this archive's declared format: that one comes from the name, and
// a name is exactly what a ZIP reader does not get to be chosen by. A RAR
// renamed to .zip still opens -- the format probe in openArchiveFileSystem
// recognizes it and hands it to the volume-aware RAR reader -- and unpacking
// one of its members whole is the cost this refuses to pay.
//
// It is also deliberately not Open. Open answers a password error by asking
// the user for one, and the caller here may be the very thread that would
// have to draw that dialog. A member the installed password does not open is
// reported as an error instead, which is all the caller wants to know.
func (v *ArchiveVFS) ReadHead(ctx context.Context, path string, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	v.mu.Lock()
	// The same question zipperarchive.OpenFS asks of the same path, so the
	// answer is the reader it built and not a second guess at it.
	if archive.DetectFormat(v.backingPath) != "zip" {
		v.mu.Unlock()
		return 0, vfs.ErrHeadUnavailable
	}
	if err := v.ensureFSLocked(); err != nil {
		v.mu.Unlock()
		return 0, err
	}
	if _, isRAR := v.fsys.(*rarArchiveFileSystem); isRAR {
		v.mu.Unlock()
		return 0, vfs.ErrHeadUnavailable
	}
	fsPath, err := v.resolveInnerPath(path)
	if err != nil {
		v.mu.Unlock()
		return 0, err
	}
	v.cancelCleanupLocked()
	v.activeCount++
	fsys := v.fsys
	v.mu.Unlock()
	defer v.decrementActive()

	file, err := fsys.Open(fsPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }() // The member was opened only to look at its first bytes.

	n, err := io.ReadFull(file, p)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		err = nil
	}
	return n, err
}

func (v *ArchiveVFS) Search(ctx context.Context, p, pat string) (chan int64, error) { return nil, nil }
func (v *ArchiveVFS) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.isClosed = true
	if v.activeCount > 0 {
		return nil
	}

	v.startCleanupTimer()
	return nil
}

func (v *ArchiveVFS) decrementActive() {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.activeCount--
	if v.activeCount == 0 && v.isClosed {
		v.startCleanupTimer()
	}
}

func (v *ArchiveVFS) startCleanupTimer() {
	if v.cleanupTimer != nil {
		v.cleanupTimer.Stop()
	}
	// Release decoder file handles immediately (important on Windows), while
	// retaining the backing lease for a short grace period. A late operation
	// can reopen the decoder from the same backing without another download.
	if v.fsys != nil {
		_ = v.fsys.Close()
		v.fsys = nil
	}
	// Two-second grace period of complete inactivity.
	v.cleanupTimer = time.AfterFunc(archiveVFSIdleTTL, func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		if v.activeCount == 0 && v.isClosed {
			if err := v.performCleanup(); err != nil {
				vtui.DebugLog("archive VFS cleanup failed for %q: %v", v.arcPath, err)
			}
		}
	})
}

func (v *ArchiveVFS) performCleanup() error {
	if v.cleanupTimer != nil {
		v.cleanupTimer.Stop()
		v.cleanupTimer = nil
	}
	if v.fsys != nil {
		_ = v.fsys.Close() // The archive index is read-only and no longer needed.
		v.fsys = nil
	}
	if v.closer != nil {
		err := v.closer.Close()
		if f, ok := v.closer.(*os.File); ok {
			_ = os.Remove(f.Name()) // Removing a private archive backing is best-effort cleanup.
		}
		v.closer = nil
		return err
	}
	return nil
}

func (v *ArchiveVFS) Clone() vfs.VFS {
	v.mu.Lock()
	if v.isClosed {
		v.mu.Unlock()
		return vfs.NewNullVFS(0)
	}
	parent, arcPath, backingPath := v.parent, v.arcPath, v.backingPath
	displayName, format, password, innerPath := v.displayName, v.format, v.password, v.innerPath
	sfxOffset, sfxSuffix := v.sfxOffset, v.sfxSuffix
	v.mu.Unlock()

	var finalPath string
	var closer io.Closer
	if osvfs, ok := parent.(*vfs.OSVFS); ok {
		if sfxOffset > 0 {
			originalPath, err := osvfs.Abs(arcPath)
			if err != nil {
				return vfs.NewNullVFS(0)
			}
			finalPath, closer, err = materializeEmbeddedArchive(originalPath, embeddedArchive{
				format: format,
				suffix: sfxSuffix,
				offset: sfxOffset,
			})
			if err != nil {
				return vfs.NewNullVFS(0)
			}
		} else {
			// The local archive file is already independently owned by its
			// parent VFS, so a second decoder can use the same immutable path.
			finalPath = backingPath
			if finalPath == "" {
				var err error
				finalPath, err = osvfs.Abs(arcPath)
				if err != nil {
					return vfs.NewNullVFS(0)
				}
			}
		}
	} else {
		// Remote archives need their own materialization lease. Sharing the
		// source lease would let one workspace's delayed cleanup remove the
		// backing file from the other workspace.
		lease, err := acquireArchiveMaterialization(context.Background(), parent, arcPath, displayName)
		if err != nil {
			return vfs.NewNullVFS(0)
		}
		finalPath, closer = lease.Path(), lease
		if sfxOffset > 0 {
			// The lease materializes the stub as well, and this decoder needs
			// the archive under it, exactly as the local branch above does.
			carved, sfxCloser, err := materializeEmbeddedArchiveAlone(finalPath, embeddedArchive{
				format: format,
				suffix: sfxSuffix,
				offset: sfxOffset,
			})
			_ = lease.Close()
			if err != nil {
				return vfs.NewNullVFS(0)
			}
			finalPath, closer = carved, sfxCloser
		}
	}

	fsys, _, err := openArchiveFSWithContext(context.Background(), finalPath, displayName, closer, password)
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		return vfs.NewNullVFS(0)
	}
	return &ArchiveVFS{
		parent: parent, arcPath: arcPath, backingPath: finalPath, displayName: displayName,
		format: format, sfxOffset: sfxOffset, sfxSuffix: sfxSuffix,
		password: password, innerPath: innerPath, fsys: fsys, closer: closer,
	}
}

var ProgressTickerInterval = 250 * time.Millisecond

func runProgressTicker(ctx context.Context, done chan struct{}, reporter vfs.TaskReporter, getStatus func() (action, file string, pct int)) {
	if reporter == nil {
		return
	}
	ticker := time.NewTicker(ProgressTickerInterval)
	defer ticker.Stop()
	dots := ""
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			dots += "."
			if len(dots) > 3 {
				dots = ""
			}
			act, file, pct := getStatus()
			if act == "Locating" {
				reporter.UpdateTransfer(act, file+dots, pct, "", pct, "")
			} else if act != "" {
				reporter.UpdateTransfer(act, file, pct, "", pct, "")
			}
		}
	}
}

func startProgressTicker(ctx context.Context, reporter vfs.TaskReporter, getStatus func() (action, file string, pct int)) func() {
	done := make(chan struct{})
	tickerDone := make(chan struct{})
	go func() {
		defer close(tickerDone)
		runProgressTicker(ctx, done, reporter, getStatus)
	}()
	return func() {
		close(done)
		<-tickerDone
	}
}

func (v *ArchiveVFS) CopyBulk(ctx context.Context, srcPaths []string, dstVfs vfs.VFS, dstDir string, reporter vfs.TaskReporter) error {
	return v.copyBulkFrom(ctx, "", false, srcPaths, dstVfs, dstDir, reporter)
}

func (v *ArchiveVFS) CopyBulkAt(ctx context.Context, srcDir string, srcPaths []string, dstVfs vfs.VFS, dstDir string, reporter vfs.TaskReporter) error {
	return v.copyBulkFrom(ctx, srcDir, true, srcPaths, dstVfs, dstDir, reporter)
}

func (v *ArchiveVFS) ScanBulkAt(ctx context.Context, srcDir string, srcPaths []string, cb vfs.ScanCallback) (vfs.OpStats, error) {
	if err := ctx.Err(); err != nil {
		return vfs.OpStats{}, err
	}

	v.mu.Lock()
	if err := v.ensureFSLocked(); err != nil {
		v.mu.Unlock()
		if archive.IsPasswordError(err) {
			if retryErr := v.openWithPassword(ctx, err); retryErr == nil {
				return v.ScanBulkAt(ctx, srcDir, srcPaths, cb)
			} else {
				return vfs.OpStats{}, retryErr
			}
		}
		return vfs.OpStats{}, err
	}
	innerPath, err := v.resolveInnerPath(srcDir)
	if err != nil {
		v.finishNonHandleOperationLocked()
		v.mu.Unlock()
		return vfs.OpStats{}, err
	}
	format := v.format
	archiveName := v.Base(v.arcPath)
	password := v.password
	v.cancelCleanupLocked()
	v.activeCount++
	v.mu.Unlock()
	defer v.decrementActive()

	if format == "" {
		format = archive.DetectFormat(archiveName)
	}
	if format != "tar" || strings.HasSuffix(strings.ToLower(archiveName), ".tar") {
		return vfs.GenericScan(ctx, v, srcDir, srcPaths, cb)
	}

	selectedMap := make(map[string]bool, len(srcPaths))
	for _, selectedPath := range srcPaths {
		fullInner := strings.ReplaceAll(selectedPath, "\\", "/")
		if innerPath != "." && innerPath != "" {
			fullInner = path.Join(innerPath, fullInner)
		}
		cleanSelected, err := cleanArchiveExtractionPath(fullInner)
		if err != nil {
			return vfs.OpStats{}, fmt.Errorf("unsafe selected archive path %q: %w", selectedPath, err)
		}
		selectedMap[cleanSelected] = true
	}

	archiveFile, err := v.openArchiveFile(ctx)
	if err != nil {
		return vfs.OpStats{}, err
	}
	defer func() { _ = archiveFile.Close() }()

	localFile, extractor, err := v.openBulkExtractor(ctx, archiveFile, password)
	if err != nil {
		return vfs.OpStats{}, err
	}
	defer func() { _ = localFile.Close() }()

	var stats vfs.OpStats
	err = extractor.Extract(ctx, localFile, func(ctx context.Context, info archives.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		cleanName, err := cleanArchiveExtractionPath(info.NameInArchive)
		if err != nil {
			return fmt.Errorf("unsafe archive entry %q: %w", info.NameInArchive, err)
		}
		if cleanName == "." || cleanName == "" || !archiveExtractionPathSelected(cleanName, selectedMap) {
			return nil
		}
		size := info.Size()
		if info.IsDir() {
			stats.Dirs++
			if size > 0 {
				stats.DirBytes += size
			}
		} else {
			stats.Files++
			if size >= 0 {
				stats.Bytes += size
			} else {
				stats.UnknownSizeFiles++
			}
		}
		if cb != nil {
			cb(archivePathJoin(v.arcPath, cleanName), stats)
		}
		return nil
	})
	return stats, err
}

func (v *ArchiveVFS) copyBulkFrom(ctx context.Context, srcDir string, useSrcDir bool, srcPaths []string, dstVfs vfs.VFS, dstDir string, reporter vfs.TaskReporter) error {
	if err := archiveOperationCancelled(ctx, reporter); err != nil {
		return err
	}
	v.mu.Lock()
	if err := v.ensureFSLocked(); err != nil {
		v.mu.Unlock()
		if archive.IsPasswordError(err) {
			if retryErr := v.openWithPassword(ctx, err); retryErr == nil {
				return v.copyBulkFrom(ctx, srcDir, useSrcDir, srcPaths, dstVfs, dstDir, reporter)
			} else {
				return retryErr
			}
		}
		return err
	}
	innerPath := v.innerPath
	if useSrcDir {
		var err error
		innerPath, err = v.resolveInnerPath(srcDir)
		if err != nil {
			v.finishNonHandleOperationLocked()
			v.mu.Unlock()
			return err
		}
	}
	v.cancelCleanupLocked()
	v.activeCount++
	absPath := v.activePath()
	password := v.password
	v.mu.Unlock()
	defer v.decrementActive()

	// Create a map of selected paths for fast O(1) lookup
	selectedMap := make(map[string]bool)
	for _, p := range srcPaths {
		fullInner := strings.ReplaceAll(p, "\\", "/")
		if innerPath != "." && innerPath != "" {
			fullInner = path.Join(innerPath, fullInner)
		}
		cleanSelected, err := cleanArchiveExtractionPath(fullInner)
		if err != nil {
			return fmt.Errorf("unsafe selected archive path %q: %w", p, err)
		}
		selectedMap[cleanSelected] = true
	}

	waitLock := true
	if !vfs.GlobalArchiveLockManager.TryLock(absPath) {
		// Headless callers can request queuing without an interactive prompt.
		if autoQueueRequested(ctx) {
			waitLock = true
		} else if vtui.FrameManager == nil {
			// Fallback headless mode
			waitLock = true
		} else {
			resChan := make(chan int, 1)
			vtui.FrameManager.PostTask(func() {
				dlg := vtui.ShowMessage(" Archive Busy ", "This archive is currently being processed.\nRunning multiple operations simultaneously may severely degrade performance.", []string{"&Queue", "&Parallel", "&Cancel"})
				dlg.OnResult = func(c int) { resChan <- c }
			})
			res := <-resChan
			if res == 2 || res < 0 {
				return context.Canceled
			}
			waitLock = (res == 0)
		}
	} else {
		vfs.GlobalArchiveLockManager.Unlock(absPath)
	}

	if waitLock {
		if reporter != nil {
			reporter.UpdateTransfer("Waiting", v.Base(absPath), -1, "Waiting in queue...", -1, "")
		}
		vfs.GlobalArchiveLockManager.Lock(absPath)
		defer vfs.GlobalArchiveLockManager.Unlock(absPath)
	}

	archiveFile, err := v.openArchiveFile(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = archiveFile.Close() // CopyBulk opens the archive only for reading.
	}()

	format := v.format
	if format == "" {
		format = archive.DetectFormat(v.Base(v.arcPath))
	}
	switch format {
	case "zip":
		return v.copyBulkZip(ctx, archiveFile, selectedMap, innerPath, password, dstVfs, dstDir, reporter)
	case "tar":
		// tar.NewReader consumes an uncompressed tar stream. Compressed tar
		// archives are read through the generic extractor, which selects the
		// compression layer before handing entries to the tar extractor.
		if strings.HasSuffix(strings.ToLower(v.Base(v.arcPath)), ".tar") {
			return v.copyBulkTar(ctx, archiveFile, selectedMap, innerPath, dstVfs, dstDir, reporter)
		}
		return v.copyBulkFallback(ctx, archiveFile, selectedMap, innerPath, password, dstVfs, dstDir, reporter)
	}
	return v.copyBulkFallback(ctx, archiveFile, selectedMap, innerPath, password, dstVfs, dstDir, reporter)
}

func (v *ArchiveVFS) openArchiveFile(ctx context.Context) (vfs.ReadAtCloser, error) {
	if osvfs, ok := v.parent.(*vfs.OSVFS); ok {
		archivePath := v.backingPath
		if archivePath == "" {
			archivePath, _ = osvfs.Abs(v.arcPath)
		}
		f, err := os.Open(archivePath)
		if err != nil {
			return nil, err
		}
		stat, _ := f.Stat()
		return &vfs.TempFileWrapper{File: f, SizeVal: stat.Size(), TempPath: ""}, nil
	}
	return v.parent.Open(ctx, v.arcPath)
}

func ensureArchiveExtractionDir(ctx context.Context, dstVfs vfs.VFS, dir string) error {
	if err := dstVfs.MkDir(ctx, dir); err != nil {
		// MkDir is not uniformly idempotent across VFS implementations. In
		// particular, a remote backend may report an existing directory as an
		// error even though it is already suitable as an extraction target.
		item, statErr := dstVfs.Stat(ctx, dir)
		if statErr == nil && item.IsDir {
			return nil
		}
		return fmt.Errorf("create extraction directory %q: %w", dir, err)
	}
	return nil
}

// openZipReader opens the archive's entries for reading. A ZIP split archive
// (archive.z01, archive.z02, ..., archive.zip) keeps its central directory in
// the last volume and its data across all of them, and zip.OpenReaderWithPassword
// joins the volumes; a stream of the one file the panel sits on holds only a
// part of the archive, and copying out of it failed with "zip: not a valid zip
// file" (issue #1186). Opening by name needs a local file, so an archive on
// another backend, or inside another archive, is still read from the stream.
func (v *ArchiveVFS) openZipReader(ctx context.Context, f vfs.ReadAtCloser, password string) (*zip.Reader, func(), error) {
	localPath := ""
	if temp, ok := f.(*vfs.TempFileWrapper); ok && temp.TempPath != "" {
		localPath = temp.TempPath
	} else if _, ok := v.parent.(*vfs.OSVFS); ok || v.backingPath != "" {
		localPath = v.activePath()
	}
	if localPath != "" {
		// A file that cannot be opened by name is read from the stream,
		// which is where every archive was read from before.
		if rc, err := zip.OpenReaderWithPassword(localPath, password); err == nil {
			return &rc.Reader, func() { _ = rc.Close() }, nil
		}
	}
	zr, err := zip.NewReaderWithPassword(readerAtAdapter{r: f, ctx: ctx}, f.Size(), password)
	if err != nil {
		return nil, nil, err
	}
	return zr, nil, nil
}

func (v *ArchiveVFS) copyBulkZip(ctx context.Context, f vfs.ReadAtCloser, selected map[string]bool, innerPath, password string, dstVfs vfs.VFS, dstDir string, reporter vfs.TaskReporter) error {
	zr, closeZip, err := v.openZipReader(ctx, f, password)
	if err != nil {
		return err
	}
	if closeZip != nil {
		defer closeZip()
	}

	var mu sync.Mutex
	lastAction := "Locating"
	lastFile := "Archive data"
	lastPct := -1

	stopProgressTicker := startProgressTicker(ctx, reporter, func() (string, string, int) {
		mu.Lock()
		defer mu.Unlock()
		return lastAction, lastFile, lastPct
	})
	defer stopProgressTicker()

	buf := make([]byte, 128*1024)
	for _, file := range zr.File {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		cleanName, err := cleanArchiveExtractionPath(file.Name)
		if err != nil {
			return fmt.Errorf("unsafe archive entry %q: %w", file.Name, err)
		}
		if cleanName == "." {
			continue
		}
		matched := archiveExtractionPathSelected(cleanName, selected)

		if !matched {
			mu.Lock()
			lastAction = "Locating"
			lastFile = cleanName
			lastPct = -1
			mu.Unlock()
			if TestSkipDelay > 0 {
				time.Sleep(TestSkipDelay)
			}
			continue
		}

		targetPath, err := archiveExtractionTarget(dstVfs, dstDir, cleanName, innerPath)
		if err != nil {
			return fmt.Errorf("unsafe archive entry %q: %w", file.Name, err)
		}

		if file.FileInfo().IsDir() {
			if err := ensureArchiveExtractionDir(ctx, dstVfs, targetPath); err != nil {
				return err
			}
			if fp, ok := reporter.(vfs.FileProgress); ok {
				fp.DirDone()
			}
			continue
		}

		if err := ensureArchiveExtractionDir(ctx, dstVfs, dstVfs.Dir(targetPath)); err != nil {
			return err
		}
		if file.UncompressedSize64 > math.MaxInt64 {
			return fmt.Errorf("archive entry %q is too large: %d bytes exceeds the supported size", file.Name, file.UncompressedSize64)
		}
		// #nosec G115 -- the explicit MaxInt64 check above makes the archive size conversion lossless.
		fileSize := int64(file.UncompressedSize64)

		if fp, ok := reporter.(vfs.FileProgress); ok {
			fp.StartFile(file.Name, fileSize)
		}
		if reporter != nil {
			mu.Lock()
			lastAction = "Extracting"
			lastFile = file.Name
			lastPct = 0
			mu.Unlock()
		}

		rc, err := file.Open()
		if err != nil {
			return err
		}

		wc, err := dstVfs.Create(ctx, targetPath)
		if err != nil {
			_ = rc.Close() // The archive entry is read-only.
			return err
		}

		var copied int64
		for {
			if ctx.Err() != nil {
				_ = rc.Close() // The archive entry is read-only.
				return joinArchiveCloseError(ctx.Err(), wc.Close())
			}
			n, rerr := rc.Read(buf)
			if n > 0 {
				if _, werr := wc.Write(buf[:n]); werr != nil {
					_ = rc.Close() // The archive entry is read-only.
					return joinArchiveCloseError(werr, wc.Close())
				}
				if fp, ok := reporter.(vfs.FileProgress); ok {
					fp.UpdateBytes(n)
				}
				copied += int64(n)
				if reporter != nil && fileSize > 0 {
					pct := archiveProgressPercent(copied, fileSize)
					mu.Lock()
					lastAction = "Extracting"
					lastFile = file.Name
					lastPct = pct
					mu.Unlock()
				}
			}
			if rerr != nil {
				if rerr == io.EOF {
					break
				}
				_ = rc.Close() // The archive entry is read-only.
				return joinArchiveCloseError(rerr, wc.Close())
			}
		}
		_ = rc.Close() // The archive entry is read-only.
		if err := wc.Close(); err != nil {
			return err
		}

		if fp, ok := reporter.(vfs.FileProgress); ok {
			fp.FileDone()
		}

		mode := uint32(file.Mode().Perm())
		if mode == 0 {
			mode = 0644
		}
		item := vfs.VFSItem{
			Name:     file.Name,
			Size:     fileSize,
			IsDir:    false,
			MTime:    file.Modified,
			ATime:    file.Modified,
			UnixMode: mode,
			Uid:      -1,
			Gid:      -1,
		}
		_ = dstVfs.SetAttributes(ctx, targetPath, item) // Metadata support is optional across destination VFSes.
	}
	return nil
}

func (v *ArchiveVFS) copyBulkTar(ctx context.Context, f vfs.ReadAtCloser, selected map[string]bool, innerPath string, dstVfs vfs.VFS, dstDir string, reporter vfs.TaskReporter) error {
	tr := tar.NewReader(ctxReader{r: f, ctx: ctx})
	var mu sync.Mutex
	lastAction := "Locating"
	lastFile := "Archive data"
	lastPct := -1

	stopProgressTicker := startProgressTicker(ctx, reporter, func() (string, string, int) {
		mu.Lock()
		defer mu.Unlock()
		return lastAction, lastFile, lastPct
	})
	defer stopProgressTicker()

	buf := make([]byte, 128*1024)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		cleanName, err := cleanArchiveExtractionPath(hdr.Name)
		if err != nil {
			return fmt.Errorf("unsafe archive entry %q: %w", hdr.Name, err)
		}
		if cleanName == "." {
			continue
		}
		matched := archiveExtractionPathSelected(cleanName, selected)

		if !matched {
			mu.Lock()
			lastAction = "Locating"
			lastFile = cleanName
			lastPct = -1
			mu.Unlock()
			if TestSkipDelay > 0 {
				time.Sleep(TestSkipDelay)
			}
			continue
		}

		targetPath, err := archiveExtractionTarget(dstVfs, dstDir, cleanName, innerPath)
		if err != nil {
			return fmt.Errorf("unsafe archive entry %q: %w", hdr.Name, err)
		}

		if hdr.Typeflag == tar.TypeDir {
			if err := ensureArchiveExtractionDir(ctx, dstVfs, targetPath); err != nil {
				return err
			}
			if fp, ok := reporter.(vfs.FileProgress); ok {
				fp.DirDone()
			}
			continue
		}

		if err := ensureArchiveExtractionDir(ctx, dstVfs, dstVfs.Dir(targetPath)); err != nil {
			return err
		}

		if fp, ok := reporter.(vfs.FileProgress); ok {
			fp.StartFile(cleanName, hdr.Size)
		}
		if reporter != nil {
			mu.Lock()
			lastAction = "Extracting"
			lastFile = cleanName
			lastPct = 0
			mu.Unlock()
		}

		wc, err := dstVfs.Create(ctx, targetPath)
		if err != nil {
			return err
		}

		var copied int64
		for {
			if ctx.Err() != nil {
				return joinArchiveCloseError(ctx.Err(), wc.Close())
			}
			n, rerr := tr.Read(buf)
			if n > 0 {
				if _, werr := wc.Write(buf[:n]); werr != nil {
					return joinArchiveCloseError(werr, wc.Close())
				}
				if fp, ok := reporter.(vfs.FileProgress); ok {
					fp.UpdateBytes(n)
				}
				copied += int64(n)
				if reporter != nil && hdr.Size > 0 {
					pct := archiveProgressPercent(copied, hdr.Size)
					mu.Lock()
					lastAction = "Extracting"
					lastFile = cleanName
					lastPct = pct
					mu.Unlock()
				}
			}
			if rerr != nil {
				if rerr == io.EOF {
					break
				}
				return joinArchiveCloseError(rerr, wc.Close())
			}
		}
		if err := wc.Close(); err != nil {
			return err
		}

		if fp, ok := reporter.(vfs.FileProgress); ok {
			fp.FileDone()
		}

		// Only POSIX permission and special bits are meaningful to the destination.
		// Mask before narrowing so a hostile tar header cannot smuggle high bits
		// into the uint32 VFS metadata field.
		// #nosec G115 -- masking to 0o7777 proves the result is in the uint32 range.
		mode := uint32(hdr.Mode & 0o7777)
		if mode == 0 {
			mode = 0644
		}
		item := vfs.VFSItem{
			Name:     hdr.Name,
			Size:     hdr.Size,
			IsDir:    false,
			MTime:    hdr.ModTime,
			ATime:    hdr.ModTime,
			UnixMode: mode,
			Uid:      -1,
			Gid:      -1,
		}
		_ = dstVfs.SetAttributes(ctx, targetPath, item) // Metadata support is optional across destination VFSes.
	}
	return nil
}

func archiveProgressPercent(copied, total int64) int {
	if copied <= 0 || total <= 0 {
		return 0
	}
	if copied >= total {
		return 100
	}
	// Progress is approximate UI data. Floating-point division avoids an
	// intermediate copied*100 overflow for very large archive members.
	return int(float64(copied) * 100 / float64(total))
}

func (v *ArchiveVFS) openBulkExtractor(ctx context.Context, f vfs.ReadAtCloser, password string) (*archive.Input, archives.Extractor, error) {
	var localPath string
	if temp, ok := f.(*vfs.TempFileWrapper); ok && temp.TempPath != "" {
		localPath = temp.TempPath
	} else {
		localPath = v.activePath()
	}

	// Split archives (name.7z.001, name.7z.002, ...) are read as one stream;
	// copying out of one read only its first volume and failed at the header
	// (issue #1179).
	localF, err := archive.OpenInput(localPath)
	if err != nil {
		return nil, nil, err
	}

	format, _, err := archives.Identify(ctx, localPath, localF)
	if err != nil {
		_ = localF.Close()
		return nil, nil, err
	}
	if configuredFormat, ok := configureRARArchiveFormat(format, localPath, password); ok {
		format = configuredFormat
	} else if passwordFormat, ok := archivePasswordFormat(format, password); ok {
		format = passwordFormat
	}

	ex, ok := format.(archives.Extractor)
	if !ok {
		_ = localF.Close()
		return nil, nil, fmt.Errorf("format %T does not support extraction", format)
	}

	if _, err := localF.Seek(0, io.SeekStart); err != nil {
		_ = localF.Close()
		return nil, nil, err
	}
	return localF, ex, nil
}

func (v *ArchiveVFS) copyBulkFallback(ctx context.Context, f vfs.ReadAtCloser, selected map[string]bool, innerPath, password string, dstVfs vfs.VFS, dstDir string, reporter vfs.TaskReporter) error {
	localF, ex, err := v.openBulkExtractor(ctx, f, password)
	if err != nil {
		return err
	}
	defer func() {
		_ = localF.Close() // The fallback extractor opens the archive only for reading.
	}()

	var mu sync.Mutex
	lastAction := "Locating"
	lastFile := "Archive data"
	lastPct := -1

	stopProgressTicker := startProgressTicker(ctx, reporter, func() (string, string, int) {
		mu.Lock()
		defer mu.Unlock()
		return lastAction, lastFile, lastPct
	})
	defer stopProgressTicker()

	return ex.Extract(ctx, localF, func(ctx context.Context, info archives.FileInfo) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		cleanName, err := cleanArchiveExtractionPath(info.NameInArchive)
		if err != nil {
			return fmt.Errorf("unsafe archive entry %q: %w", info.NameInArchive, err)
		}
		if cleanName == "." || cleanName == "" {
			return nil
		}

		matched := archiveExtractionPathSelected(cleanName, selected)

		size := info.Size()

		if !matched {
			mu.Lock()
			lastAction = "Locating"
			lastFile = cleanName
			lastPct = -1
			mu.Unlock()
			if fp, ok := reporter.(vfs.FileProgress); ok {
				fp.StartFile(cleanName, size)
				fp.FileSkipped()
			}
			if TestSkipDelay > 0 {
				time.Sleep(TestSkipDelay)
			}
			return nil
		}

		targetPath, err := archiveExtractionTarget(dstVfs, dstDir, cleanName, innerPath)
		if err != nil {
			return fmt.Errorf("unsafe archive entry %q: %w", info.NameInArchive, err)
		}

		if info.IsDir() {
			if err := ensureArchiveExtractionDir(ctx, dstVfs, targetPath); err != nil {
				return err
			}
			if fp, ok := reporter.(vfs.FileProgress); ok {
				fp.DirDone()
			}
			return nil
		}

		if err := ensureArchiveExtractionDir(ctx, dstVfs, dstVfs.Dir(targetPath)); err != nil {
			return err
		}

		if fp, ok := reporter.(vfs.FileProgress); ok {
			fp.StartFile(cleanName, size)
		}
		if reporter != nil {
			mu.Lock()
			lastAction = "Extracting"
			lastFile = cleanName
			lastPct = 0
			mu.Unlock()
		}

		rc, err := info.Open()
		if err != nil {
			return err
		}
		defer func() {
			_ = rc.Close() // The fallback archive entry is read-only.
		}()

		wc, err := dstVfs.Create(ctx, targetPath)
		if err != nil {
			return err
		}

		buf := make([]byte, 128*1024)
		var copied int64
		for {
			if ctx.Err() != nil {
				return joinArchiveCloseError(ctx.Err(), wc.Close())
			}
			n, rerr := rc.Read(buf)
			if n > 0 {
				if _, werr := wc.Write(buf[:n]); werr != nil {
					return joinArchiveCloseError(werr, wc.Close())
				}
				if fp, ok := reporter.(vfs.FileProgress); ok {
					fp.UpdateBytes(n)
				}
				copied += int64(n)
				if reporter != nil && size > 0 {
					pct := archiveProgressPercent(copied, size)
					mu.Lock()
					lastAction = "Extracting"
					lastFile = cleanName
					lastPct = pct
					mu.Unlock()
				}
			}
			if rerr != nil {
				if rerr == io.EOF {
					break
				}
				return joinArchiveCloseError(rerr, wc.Close())
			}
		}
		if err := wc.Close(); err != nil {
			return err
		}

		if fp, ok := reporter.(vfs.FileProgress); ok {
			fp.FileDone()
		}

		mode := uint32(info.Mode().Perm())
		if mode == 0 {
			mode = 0644
		}
		item := vfs.VFSItem{
			Name:     info.Name(),
			Size:     info.Size(),
			IsDir:    false,
			MTime:    info.ModTime(),
			ATime:    info.ModTime(),
			UnixMode: mode,
			Uid:      -1,
			Gid:      -1,
		}
		_ = dstVfs.SetAttributes(ctx, targetPath, item) // Metadata support is optional across destination VFSes.
		return nil
	})
}
