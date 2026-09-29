package observer

// ObserverVFS is the vfs.VFS a Provider.Open call returns (provider.go):
// f4#1563's first slice of actually browsing an Observer-module container
// from a panel. It is deliberately simple next to plugins/archive.ArchiveVFS
// (plugins/archive/vfs.go): read-only, one module instance per opened
// storage with no lazy re-open on a dropped handle, no nested-archive
// materialization lease, no nested SFX probing. Password retry for
// SOR_PASSWORD_REQUIRED (password.go) is one thing it does share with
// ArchiveVFS; another is the representation of "a path inside a
// container": the container's own host/parent path, doubling as this VFS's
// synthetic root, with an inner slash-separated path appended under it
// (containerPathJoin/containerRelativePath below, mirroring
// plugins/archive's archivePathJoin/archiveRelativePath -- there is no
// shared helper between the two packages, the same way plugins/archive and
// plugins/multiarc each define their own).
//
// The whole directory tree is read once, eagerly, right after OpenStorage
// succeeds (buildTree): API v6 has no lazy/paged GetItem, only a flat walk
// from index 0 until GetItemNoMoreItems (see abi.go/runtime.go), so there is
// nothing to gain from deferring it, and eagerly building it here is what
// lets ReadDir/Stat below be plain map lookups.
import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/unxed/f4/vfs"
)

// fileAttributeDirectory is Win32's FILE_ATTRIBUTE_DIRECTORY (0x10), the bit
// ModuleDef.h's StorageItemInfo.Attributes uses to mark a directory entry;
// see plugins/observer/testdata/isoimg/compat/windows.h's own copy of the
// same constant, used on the guest side of the very same ABI.
const fileAttributeDirectory uint32 = 0x10

// windowsEpochDelta100ns is the number of 100ns FILETIME ticks between the
// Windows epoch (1601-01-01) and the Unix epoch (1970-01-01) -- the
// standard constant for converting a Win32 FILETIME (abi.go's FileTime,
// ModuleDef.h's StorageItemInfo.ModificationTime) into a time.Time.
const windowsEpochDelta100ns = 116444736000000000

// filetimeToTime converts a FileTime read off the guest into a time.Time,
// or the zero time for a FileTime the module never filled in (both Low and
// High zero -- genisoimg-built images without RockRidge timestamps commonly
// leave this at zero, and a real zero Win32 FILETIME would itself predate
// 1601, so treating it as "unknown" rather than computing a valid-looking
// but meaningless time is the safer reading).
func filetimeToTime(ft FileTime) time.Time {
	if ft.Low == 0 && ft.High == 0 {
		return time.Time{}
	}
	ticks := int64(ft.High)<<32 | int64(ft.Low) //nolint:gosec // G115: reinterprets bits, not a narrowing conversion.
	return time.Unix(0, (ticks-windowsEpochDelta100ns)*100).UTC()
}

// node is one entry of the tree buildTree assembles from a full GetItem
// walk. Intermediate directories that the module never reports as their own
// item (common: many containers list only files, with directories implied
// by their path) are synthesized with itemIndex -1 -- there is nothing to
// GetItem or ExtractItem for one of those, which Stat/ReadDir never need
// since they only ever read fields already computed here.
type node struct {
	name      string
	isDir     bool
	size      int64
	mtime     time.Time
	itemIndex int32
	// children holds full inner-path keys (see ObserverVFS.byPath), sorted
	// once by buildTree.
	children []string
}

// ObserverVFS is not safe for concurrent use beyond what mu serializes:
// mod (see runtime.go's Module) is a single wasm instance, and "Instances
// are not safe for concurrent use" there applies here identically. Two
// panels browsing the same container each get their own instance through
// Clone, exactly as the ticket's design (module_cbs/doc.go, and the PR
// thread's "each opened storage gets its own module instance") calls for.
type ObserverVFS struct {
	mu     sync.Mutex
	closed bool

	parent    vfs.VFS
	arcPath   string
	innerPath string // "" at root; else a clean, slash-separated path with no leading/trailing slash
	format    string

	wasmBytes []byte // kept only so Clone can re-run newObserverVFS without going back to Provider
	settings  string // this container's module's own Settings string (config.go), kept for Clone the same way wasmBytes is
	mod       *Module
	storage   uint32
	ra        ReaderAt
	tempDir   string
	cancel    context.CancelFunc

	byPath map[string]*node
	root   *node
}

// newObserverVFS drives LoadModule/LoadSubModule/OpenStorage against
// arcPath (as parent sees it) and then walks the whole item list to build
// the tree, returning a ready-to-browse ObserverVFS rooted at innerPath (""
// for the container's own root -- Provider.Open always passes that; Clone
// passes whatever inner path the original instance had navigated to).
// settings is passed straight through to LoadSubModule -- Provider builds it
// per module from observer.ini/observer_user.ini (config.go); every caller
// that has no config-driven module settings of its own (password_test.go's
// direct calls) passes "", exactly what every part before this one already
// hardcoded here.
func newObserverVFS(ctx context.Context, parent vfs.VFS, arcPath string, wasmBytes []byte, settings string, innerPath string) (*ObserverVFS, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, errors.New("observer: parent VFS is nil")
	}

	canonical := containerRootPath(arcPath)
	guestName := baseName(parent, arcPath)
	if guestName == "" {
		guestName = "target"
	}

	ra, err := parent.Open(vfs.WithStreamRead(ctx), arcPath)
	if err != nil {
		return nil, err
	}

	tempDir, err := os.MkdirTemp("", "f4-observer-*")
	if err != nil {
		_ = ra.Close()
		return nil, err
	}

	// The module's own lifetime is independent of ctx: ctx here governs only
	// how long the caller is willing to wait for this constructor, the same
	// way plugins/archive.NewArchiveVFSContext's ctx governs opening but not
	// the resulting ArchiveVFS's later calls. cancel is stored and invoked
	// from Close.
	modCtx, cancel := context.WithCancel(context.Background())
	mount := NewSingleFileFS(modCtx, guestName, ra)
	mod, err := LoadModule(modCtx, wasmBytes, mount, nil, WithExtractDir(tempDir))
	if err != nil {
		cancel()
		_ = os.RemoveAll(tempDir)
		_ = ra.Close()
		return nil, err
	}

	fail := func(err error) (*ObserverVFS, error) {
		_ = mod.Close()
		cancel()
		_ = os.RemoveAll(tempDir)
		_ = ra.Close()
		return nil, err
	}

	if _, err := mod.LoadSubModule(settings); err != nil {
		return fail(err)
	}
	// openStorageWithPasswordPrompt (password.go) retries OpenStorage with a
	// password for as long as the module answers SOR_PASSWORD_REQUIRED,
	// asking the user through the same dialog plugins/archive uses for its
	// own archives; see that file's own doc comment for why this is
	// considerably simpler than plugins/archive's per-member retry logic.
	res, err := openStorageWithPasswordPrompt(ctx, mod, guestName, "/"+guestName)
	if err != nil {
		return fail(err)
	}
	if res.Code != SORSuccess {
		return fail(fmt.Errorf("observer: module did not recognize %s", guestName))
	}

	v := &ObserverVFS{
		parent:    parent,
		arcPath:   canonical,
		format:    res.Info.Format,
		wasmBytes: wasmBytes,
		settings:  settings,
		mod:       mod,
		storage:   res.Storage,
		ra:        ra,
		tempDir:   tempDir,
		cancel:    cancel,
		byPath:    make(map[string]*node),
	}
	v.root = &node{itemIndex: -1, isDir: true}
	v.byPath[""] = v.root

	if err := v.buildTree(); err != nil {
		_ = mod.CloseStorage(res.Storage)
		return fail(err)
	}

	if clean, err := cleanInnerPath(innerPath); err == nil {
		if n, ok := v.byPath[clean]; ok && n.isDir {
			v.innerPath = clean
		}
	}

	return v, nil
}

// buildTree walks every item via GetItem (see runtime.go/abi.go: API v6 has
// no other way to enumerate a storage) and inserts each into the tree.
func (v *ObserverVFS) buildTree() error {
	for idx := int32(0); ; idx++ {
		res, err := v.mod.GetItem(v.storage, idx)
		if err != nil {
			return err
		}
		if res.Code == GetItemNoMoreItems {
			break
		}
		if res.Code != GetItemOK {
			return fmt.Errorf("observer: GetItem(%d): code %d", idx, res.Code)
		}
		v.insertItem(idx, res.Info)
	}
	for _, n := range v.byPath {
		sort.Strings(n.children)
	}
	return nil
}

// insertItem places one GetItem result into the tree, synthesizing any
// ancestor directory the module did not itself report as an item (see
// node's own doc comment). info.Path is trusted to be the item's full path
// within the container -- ModuleDef.h documents no other way to reconstruct
// hierarchy from a flat GetItem walk.
func (v *ObserverVFS) insertItem(idx int32, info StorageItemInfo) {
	clean := path.Clean(strings.Trim(strings.ReplaceAll(info.Path, "\\", "/"), "/"))
	if clean == "" || clean == "." {
		// The container's own root entry ("." or an empty path), which this
		// tree already has as v.root; nothing further to add for it.
		return
	}

	segments := strings.Split(clean, "/")
	isDir := info.Attributes&fileAttributeDirectory != 0

	var built strings.Builder
	for i, seg := range segments {
		if built.Len() > 0 {
			built.WriteByte('/')
		}
		built.WriteString(seg)
		key := built.String()
		last := i == len(segments)-1

		n, ok := v.byPath[key]
		if !ok {
			n = &node{name: seg, itemIndex: -1}
			v.byPath[key] = n
			parentKey := ""
			if i > 0 {
				parentKey = strings.Join(segments[:i], "/")
			}
			parent := v.byPath[parentKey]
			parent.children = append(parent.children, key)
		}
		if last {
			n.isDir = isDir
			n.size = info.Size
			n.mtime = filetimeToTime(info.ModificationTime)
			n.itemIndex = idx
		} else {
			// An intermediate path segment always denotes a directory,
			// whether or not the module ever reports it as its own item.
			n.isDir = true
		}
	}
}

func (v *ObserverVFS) vfsItem(n *node) vfs.VFSItem {
	name := n.name
	if n == v.root {
		name = v.parent.Base(v.arcPath)
	}
	return vfs.VFSItem{
		KnownMetadata: vfs.MetadataExplicit | vfs.MetadataMTime,
		Name:          name,
		IsDir:         n.isDir,
		Size:          n.size,
		SizeKnown:     !n.isDir,
		MTime:         n.mtime,
	}
}

// resolveInner turns a path as any VFS caller might pass it (relative to
// this instance's current innerPath, or the full container-prefixed form
// GetPath/Join hand back) into a clean inner key ("" for the container
// root), the same job plugins/archive.ArchiveVFS.resolveInnerPath does for
// ArchiveVFS.
func (v *ObserverVFS) resolveInner(candidate string) (string, error) {
	if candidate == "" || candidate == "." {
		return v.innerPath, nil
	}
	if relative, owned := containerRelativePath(candidate, v.arcPath); owned {
		return cleanInnerPath(relative)
	}
	if vfs.IsURIPath(candidate) || filepath.IsAbs(candidate) || path.IsAbs(candidate) || filepath.VolumeName(candidate) != "" {
		return "", fmt.Errorf("observer: path escapes container: %s", candidate)
	}
	inner := path.Join(v.innerPath, strings.ReplaceAll(candidate, "\\", "/"))
	return cleanInnerPath(inner)
}

func (v *ObserverVFS) IsAtRoot() bool { return v.innerPath == "" }

func (v *ObserverVFS) GetPath() string {
	if v.innerPath == "" {
		return v.arcPath
	}
	return containerPathJoin(v.arcPath, v.innerPath)
}

func (v *ObserverVFS) IsAbs(candidate string) bool {
	if _, owned := containerRelativePath(candidate, v.arcPath); owned {
		return true
	}
	return v.parent.IsAbs(candidate)
}

func (v *ObserverVFS) SetPath(p string) error {
	inner, err := v.resolveInner(p)
	if err != nil {
		return err
	}
	if inner != "" {
		n, ok := v.byPath[inner]
		if !ok {
			return fmt.Errorf("observer: not found: %s", p)
		}
		if !n.isDir {
			return fmt.Errorf("observer: not a directory: %s", p)
		}
	}
	v.mu.Lock()
	v.innerPath = inner
	v.mu.Unlock()
	return nil
}

func (v *ObserverVFS) ReadDir(ctx context.Context, p string, onChunk func([]vfs.VFSItem)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	inner, err := v.resolveInner(p)
	if err != nil {
		return err
	}
	n, ok := v.byPath[inner]
	if !ok || !n.isDir {
		return fmt.Errorf("observer: not a directory: %s", p)
	}
	items := make([]vfs.VFSItem, 0, len(n.children))
	for _, childKey := range n.children {
		items = append(items, v.vfsItem(v.byPath[childKey]))
	}
	if onChunk != nil {
		onChunk(items)
	}
	return nil
}

func (v *ObserverVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	if err := ctx.Err(); err != nil {
		return vfs.VFSItem{}, err
	}
	inner, err := v.resolveInner(p)
	if err != nil {
		return vfs.VFSItem{}, err
	}
	n, ok := v.byPath[inner]
	if !ok {
		return vfs.VFSItem{}, fmt.Errorf("observer: not found: %s", p)
	}
	return v.vfsItem(n), nil
}

func (v *ObserverVFS) Join(elements ...string) string {
	if len(elements) == 0 {
		return ""
	}
	if relative, owned := containerRelativePath(elements[0], v.arcPath); owned {
		inner := relative
		for _, element := range elements[1:] {
			inner = path.Join(inner, strings.ReplaceAll(element, "\\", "/"))
		}
		clean, err := cleanInnerPath(inner)
		if err != nil {
			return v.arcPath
		}
		return containerPathJoin(v.arcPath, clean)
	}
	if vfs.IsURIPath(elements[0]) {
		joined := elements[0]
		for _, element := range elements[1:] {
			if element == "" || element == "." {
				continue
			}
			joined = containerPathJoin(joined, element)
		}
		return joined
	}
	return filepath.Join(elements...)
}

func (v *ObserverVFS) Abs(candidate string) (string, error) {
	if _, owned := containerRelativePath(candidate, v.arcPath); owned {
		return candidate, nil
	}
	if vfs.IsURIPath(candidate) {
		return "", fmt.Errorf("observer: path escapes container: %s", candidate)
	}
	if filepath.IsAbs(candidate) || path.IsAbs(candidate) {
		return filepath.Clean(candidate), nil
	}
	return filepath.Clean(v.Join(v.GetPath(), candidate)), nil
}

func (v *ObserverVFS) Base(candidate string) string {
	if candidate == v.arcPath {
		return v.parent.Base(v.arcPath)
	}
	if relative, owned := containerRelativePath(candidate, v.arcPath); owned {
		return path.Base(relative)
	}
	return filepath.Base(candidate)
}

func (v *ObserverVFS) Dir(candidate string) string {
	if candidate == v.arcPath {
		return v.parent.Dir(v.arcPath)
	}
	if relative, owned := containerRelativePath(candidate, v.arcPath); owned {
		return containerPathJoin(v.arcPath, path.Dir(relative))
	}
	return filepath.Dir(candidate)
}

// MkDir, Remove and Rename all refuse: every Observer module this package
// drives is a read-only parser (see the package doc comment on
// github.com/lazyhamster/Observer's own scope), and API v6 has no write
// side to call through even if one were not.
func (v *ObserverVFS) MkDir(ctx context.Context, path string) error {
	return errors.New("observer: read-only")
}

func (v *ObserverVFS) Remove(ctx context.Context, path string) error {
	return errors.New("observer: read-only")
}

func (v *ObserverVFS) Rename(ctx context.Context, oldpath, newpath string) error {
	return errors.New("observer: read-only")
}

func (v *ObserverVFS) GetCapabilities() vfs.VFSCapabilities {
	// HasRandomAccess is true because Open below always hands back a real
	// extracted file (vfs.TempFileWrapper over a real os.File), which does
	// support ReadAt properly -- see the ABI limitation this package's own
	// doc.go documents (ExtractItem always writes the whole item first,
	// there is no partial read in API v6 to expose here instead).
	return vfs.VFSCapabilities{HasRandomAccess: true}
}

func (v *ObserverVFS) Search(ctx context.Context, path string, pattern string) (chan int64, error) {
	return nil, nil
}

// Open extracts one item's full content into this instance's private temp
// directory (see newObserverVFS's WithExtractDir) and hands back a
// vfs.TempFileWrapper over the result, which both implements
// vfs.ReadAtCloser and removes the temp file on Close. This is the only
// option API v6 leaves for reading an item's bytes at all (doc.go's "No
// random access to an item's own content"), not a shortcut this package
// chose over a cheaper one.
func (v *ObserverVFS) Open(ctx context.Context, p string) (vfs.ReadAtCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inner, err := v.resolveInner(p)
	if err != nil {
		return nil, err
	}
	n, ok := v.byPath[inner]
	if !ok || n.isDir {
		return nil, fmt.Errorf("observer: not a file: %s", p)
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil, errors.New("observer: VFS is closed")
	}
	if err := v.mod.Err(); err != nil {
		return nil, err
	}

	destName := fmt.Sprintf("item-%d", n.itemIndex)
	code, err := v.mod.ExtractItem(v.storage, ExtractItemParams{ItemIndex: n.itemIndex, DestName: destName})
	if err != nil {
		return nil, err
	}
	if code != SERSuccess {
		return nil, fmt.Errorf("observer: extracting %s: code %d", p, code)
	}

	hostPath := filepath.Join(v.tempDir, destName)
	f, err := os.Open(hostPath) //nolint:gosec // G304: hostPath is built from this instance's own private tempDir + a name this function chose, never from user input.
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &vfs.TempFileWrapper{File: f, SizeVal: stat.Size(), TempPath: hostPath}, nil
}

func (v *ObserverVFS) Create(ctx context.Context, path string) (io.WriteCloser, error) {
	return nil, errors.New("observer: read-only")
}

func (v *ObserverVFS) SetAttributes(ctx context.Context, path string, item vfs.VFSItem) error {
	return errors.New("observer: read-only")
}

func (v *ObserverVFS) ParentVFS() vfs.VFS { return v.parent }

// Clone reopens the same container from scratch -- a fresh Module instance,
// a fresh temp directory, a fresh GetItem walk -- because a Module instance
// is not safe for concurrent use (runtime.go) and the second panel or a
// parallel copy needs one of its own, the same reason
// plugins/archive.ArchiveVFS.Clone reopens its own archive.FileSystem rather
// than sharing v's.
func (v *ObserverVFS) Clone() vfs.VFS {
	v.mu.Lock()
	closed := v.closed
	parent, arcPath, wasmBytes, settings, innerPath := v.parent, v.arcPath, v.wasmBytes, v.settings, v.innerPath
	v.mu.Unlock()
	if closed {
		return vfs.NewNullVFS(0)
	}
	clone, err := newObserverVFS(context.Background(), parent, arcPath, wasmBytes, settings, innerPath)
	if err != nil {
		return vfs.NewNullVFS(0)
	}
	return clone
}

func (v *ObserverVFS) Close() error {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return nil
	}
	v.closed = true
	mod, storage, tempDir, ra, cancel := v.mod, v.storage, v.tempDir, v.ra, v.cancel
	v.mu.Unlock()

	var err error
	if mod.Err() == nil {
		if cerr := mod.CloseStorage(storage); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}
	err = errors.Join(err, mod.Close())
	if cancel != nil {
		cancel()
	}
	if ra != nil {
		err = errors.Join(err, ra.Close())
	}
	if tempDir != "" {
		err = errors.Join(err, os.RemoveAll(tempDir))
	}
	return err
}

// --- container-path helpers ----------------------------------------------
//
// These mirror plugins/archive's own archivePathJoin/archiveRelativePath/
// cleanArchiveInnerPath/cleanArchiveRootPath (plugins/archive/vfs.go)
// exactly in spirit: a container-in-a-file VFS represents "a path inside
// it" as the container's own host/parent path with an inner slash-path
// appended, and every such provider in f4 (plugins/archive,
// plugins/multiarc, now this one) currently defines its own small copy of
// this rather than sharing one -- there is no vfs-level helper for it yet.

func containerRootPath(value string) string {
	if vfs.IsURIPath(value) {
		return strings.TrimRight(value, "\\/")
	}
	return filepath.Clean(value)
}

func containerRelativePath(candidate, root string) (string, bool) {
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

func containerPathJoin(root, inner string) string {
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

// cleanInnerPath normalizes an inner path to "" (this VFS's own root
// convention, matching byPath[""] == root) or a clean slash-separated path
// with no leading/trailing slash, rejecting anything that would escape the
// container root -- mirroring plugins/archive's cleanArchiveInnerPath,
// which uses "." for the same root case instead (each package's own
// convention; ObserverVFS never mixes the two).
func cleanInnerPath(inner string) (string, error) {
	inner = path.Clean(strings.TrimLeft(strings.ReplaceAll(inner, "\\", "/"), "/"))
	if inner == "" || inner == "." {
		return "", nil
	}
	if inner == ".." || strings.HasPrefix(inner, "../") {
		return "", errors.New("observer: path escapes container root")
	}
	return inner, nil
}
