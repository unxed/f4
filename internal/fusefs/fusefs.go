// Package fusefs exposes any f4 virtual file system through FUSE, so that
// programs which know nothing about f4 can read an archive or a remote host
// as an ordinary directory.
//
// The package is a translator and nothing else: it turns the FUSE kernel
// protocol (as spoken by github.com/hanwen/go-fuse) into calls on the
// vfs.VFS interface. Everything policy-like — which location to mount,
// where to mount it, when to unmount — belongs to the caller.
//
// A mount owns its vfs.VFS instance exclusively. Callers must hand over a
// freshly opened VFS, never the one a panel is currently browsing: several
// f4 VFS implementations are stateful, and Clone is allowed to return the
// receiver itself.
//
// The current implementation is read-only. See FUSE.md for the roadmap.
package fusefs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/f4/vfs/hostmode"
)

// ErrUnsupported is returned on platforms without a FUSE implementation.
var ErrUnsupported = errors.New("FUSE mounts are not supported on " + runtime.GOOS)

// Options configures a single mount. The zero value is usable: only
// MountPoint is mandatory.
type Options struct {
	// MountPoint is the local directory the VFS becomes visible at. It is
	// created if missing and removed again after unmount in that case.
	MountPoint string
	// RootPath is the VFS-native path exposed as the root of the mount.
	// Empty means the VFS's current path.
	RootPath string
	// Source is a human-readable description of what is mounted, such as
	// the URI or archive path the caller resolved. It is shown in the UI
	// and in /proc/mounts.
	Source string
	// AllowOther lets other users (including root) see the mount. It needs
	// user_allow_other in /etc/fuse.conf.
	AllowOther bool
	// ReadOnly forces the mount to be read-only.
	ReadOnly bool
	// Debug turns on go-fuse's protocol logging.
	Debug bool
	// AttrTimeout and EntryTimeout are how long the kernel may cache
	// metadata. Remote file systems want these high, local ones low.
	AttrTimeout  time.Duration
	EntryTimeout time.Duration
	// DirCacheTTL is how long a directory listing is reused for Lookup
	// before the VFS is asked again. Zero picks a default.
	DirCacheTTL time.Duration
	// OnExit runs after the mount is gone, whether it was unmounted by f4,
	// by fusermount, or because the kernel connection died.
	OnExit func(m *Mount, err error)
}

// Mount is a live FUSE mount. Every exported field is set before the mount
// becomes visible in List and is never written again.
type Mount struct {
	ID         string
	MountPoint string
	Source     string
	RootPath   string
	Since      time.Time
	// ReadOnly is the mode the mount came up in, so a caller listing mounts
	// can say which is which without asking the bridge.
	ReadOnly bool

	server     mountServer
	bridge     *bridge
	createdDir bool
	onExit     func(*Mount, error)

	mu      sync.Mutex
	stopped bool
	err     error
	done    chan struct{}
}

// mountServer is the part of *fuse.Server this package uses. Keeping it an
// interface is what lets the manager compile on platforms without FUSE.
type mountServer interface {
	Unmount() error
	Wait()
}

var registry = struct {
	sync.Mutex
	byID map[string]*Mount
	seq  int
}{byID: make(map[string]*Mount)}

// Supported reports whether this build can mount anything at all. UI code
// should hide the mount command when it returns false.
func Supported() bool { return supported }

// MountVFS mounts v at opts.MountPoint and returns once the mount is live,
// so a successful return means other processes can already read the tree.
//
// MountVFS takes ownership of v unconditionally: it is closed when the mount
// ends, and also when mounting fails. Callers pass a VFS opened for this
// mount alone and never one a panel is browsing.
func MountVFS(ctx context.Context, v vfs.VFS, opts Options) (mount *Mount, err error) {
	if v != nil {
		defer func() {
			if mount == nil {
				err = errors.Join(err, v.Close())
			}
		}()
	}
	if !supported {
		return nil, ErrUnsupported
	}
	if v == nil {
		return nil, errors.New("mount: no file system given")
	}
	if !opts.ReadOnly && !v.GetCapabilities().HasWrite {
		return nil, errors.New("mount: this file system cannot be written through")
	}
	point := strings.TrimSpace(opts.MountPoint)
	if point == "" {
		return nil, errors.New("mount: no mount point given")
	}
	point, err = filepath.Abs(point)
	if err != nil {
		return nil, err
	}
	if used := findByPoint(point); used != nil {
		return nil, fmt.Errorf("mount: %s is already used by mount %s", point, used.ID)
	}

	root := opts.RootPath
	if root == "" {
		root = v.GetPath()
	}
	created, err := ensureMountPoint(point)
	if err != nil {
		return nil, err
	}

	m := &Mount{
		ReadOnly:   opts.ReadOnly,
		MountPoint: point,
		Source:     describeSource(opts.Source, v, root),
		RootPath:   root,
		Since:      time.Now(),
		createdDir: created,
		onExit:     opts.OnExit,
		done:       make(chan struct{}),
	}
	m.bridge = newBridge(v, root, opts)

	registry.Lock()
	registry.seq++
	m.ID = "m" + strconv.Itoa(registry.seq)
	registry.Unlock()

	if err := startServer(ctx, m, opts); err != nil {
		var removeErr error
		if created {
			removeErr = os.Remove(point)
		}
		return nil, errors.Join(err, removeErr)
	}

	registry.Lock()
	registry.byID[m.ID] = m
	registry.Unlock()

	go m.watch()
	return m, nil
}

// MountSource resolves source into a fresh VFS, mounts it at opts.MountPoint,
// and returns the live Mount.
func MountSource(source string, opts Options) (*Mount, error) {
	ctx := context.Background()
	// An omitted mount point means "you name it", which is what the command
	// line documents and what makes $(f4 --mount x --daemon) a usable shell
	// idiom. Only the manager knows where mounts live, so only it can.
	if strings.TrimSpace(opts.MountPoint) == "" {
		opts.MountPoint = SuggestMountPoint(source)
	}
	if strings.TrimSpace(opts.Source) == "" {
		opts.Source = source
	}
	v, err := resolveSource(ctx, source)
	if err != nil {
		return nil, err
	}
	return MountVFS(ctx, v, opts)
}

// resolveSource turns a source string into a VFS the mount will own.
//
// The three kinds of source are resolved through three different registries,
// and using the wrong one is silent rather than loud: a URI handed to the
// path resolver becomes a relative filename, and a file handed to a provider
// with no parent becomes a provider that cannot read it.
func resolveSource(ctx context.Context, source string) (vfs.VFS, error) {
	// A URI — sftp://, ftp:// and friends — lives in the URI registry.
	// FindProvider does not look there, which is why a mount of
	// sftp://host/srv used to end up stat()ing a directory called "sftp:".
	if vfs.IsURIPath(source) {
		prov := vfs.FindURIProvider(source)
		if prov == nil {
			return nil, fmt.Errorf("no handler for %s", source)
		}
		v, err := prov.OpenURI(ctx, nil, source)
		if err != nil {
			return nil, fmt.Errorf("open source %s: %w", source, err)
		}
		return v, nil
	}

	abs, err := filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return vfs.NewOSVFS(abs), nil
	}

	// A file may be an archive. The provider needs something to read it
	// through, so it gets a fresh OSVFS on the containing directory —
	// fresh, because a mount owns what it was given and closes it. Passing
	// nil here is what made a mounted archive come up empty: the provider
	// accepted the file, then had no way to open it.
	parent := vfs.NewOSVFS(filepath.Dir(abs))
	if prov := vfs.FindProvider(ctx, parent, abs); prov != nil {
		v, err := prov.Open(ctx, parent, abs)
		if err != nil {
			return nil, fmt.Errorf("open source %s: %w", source, err)
		}
		return v, nil
	}
	return nil, fmt.Errorf("%s is a file nothing can mount", source)
}

// Unmount detaches the mount identified by target (an ID or a mount point).
func Unmount(target string) error {
	m := Find(target)
	if m == nil {
		return fmt.Errorf("no such mount: %s", target)
	}
	return m.Unmount()
}

// watch waits for the kernel connection to end, whatever ended it, and does
// the cleanup exactly once. Unmounting from a shell must free the VFS just
// like the in-app command does.
func (m *Mount) watch() {
	m.server.Wait()

	registry.Lock()
	delete(registry.byID, m.ID)
	registry.Unlock()

	m.bridge.close()
	cleanupErr := m.bridge.closeError()
	if m.createdDir {
		cleanupErr = errors.Join(cleanupErr, os.Remove(m.MountPoint))
	}

	m.mu.Lock()
	m.stopped = true
	err := errors.Join(m.err, cleanupErr)
	m.mu.Unlock()

	close(m.done)
	if m.onExit != nil {
		m.onExit(m, err)
	}
}

// unmountSettle bounds the wait for the post-unmount cleanup. The kernel
// side is already detached at that point; only closing the VFS is left, and
// a remote backend must not be able to hold up an interactive command.
const unmountSettle = 5 * time.Second

// Unmount detaches the mount. Busy mounts fail with EBUSY, which is a
// question for the user ("something is still in there"), not an error to
// paper over by forcing.
func (m *Mount) Unmount() error {
	if m == nil {
		return errors.New("unmount: no mount")
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	if err := m.server.Unmount(); err != nil {
		m.mu.Lock()
		m.err = err
		m.mu.Unlock()
		return err
	}
	select {
	case <-m.done:
	case <-time.After(unmountSettle):
	}
	return nil
}

// Active reports whether the mount is still serving.
func (m *Mount) Active() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.stopped
}

// Wait blocks until the mount has ended and all cleanup has completed.
// Stats renders what this mount asked of its backend, or "" unless
// F4_FUSE_STATS is set. It is printed when the mount ends, which is the only
// moment the whole picture exists.
func (m *Mount) Stats() string {
	if m == nil || m.bridge == nil {
		return ""
	}
	return m.bridge.statsReport()
}

func (m *Mount) Wait() {
	if m == nil {
		return
	}
	<-m.done
}

// List returns the live mounts, oldest first.
func List() []*Mount {
	registry.Lock()
	out := make([]*Mount, 0, len(registry.byID))
	for _, m := range registry.byID {
		out = append(out, m)
	}
	registry.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out
}

// Find returns the mount identified by an ID or by a mount point.
func Find(target string) *Mount {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	registry.Lock()
	m := registry.byID[target]
	registry.Unlock()
	if m != nil {
		return m
	}
	if abs, err := filepath.Abs(target); err == nil {
		return findByPoint(abs)
	}
	return nil
}

// UnmountAll detaches every mount and returns whatever went wrong. It is
// called on shutdown, where a leftover mount would outlive f4 as an
// unreadable directory owned by a dead process.
func UnmountAll() []error {
	var errs []error
	for _, m := range List() {
		if err := m.Unmount(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.MountPoint, err))
		}
	}
	return errs
}

func findByPoint(point string) *Mount {
	registry.Lock()
	defer registry.Unlock()
	for _, m := range registry.byID {
		if pathsEqual(m.MountPoint, point) {
			return m
		}
	}
	return nil
}

// pathsEqual compares two mount-point paths the way the underlying
// filesystem would: case-insensitively on darwin and native Windows,
// case-sensitively everywhere else. Wine's posix personality (WINE.md
// §18.2, "регистр в сравнениях") counts as "everywhere else" here too --
// FUSE mounting is not wired up for GOOS=windows regardless of personality
// today (node_unsupported.go), but the comparison should not silently
// assume Windows case-folding the day it is.
func pathsEqual(a, b string) bool {
	if runtime.GOOS == "darwin" || (runtime.GOOS == "windows" && !hostmode.Posix()) {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// MountRoot is where automatically named mount points are created. It
// follows XDG_RUNTIME_DIR when there is one, because a mount point is
// per-session state, not a document.
func MountRoot() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "f4", "mnt")
	}
	return filepath.Join(os.TempDir(), "f4-mnt-"+strconv.Itoa(os.Getuid()))
}

// SuggestMountPoint turns a source description into a free mount point
// under MountRoot, so that the common case needs no user input at all.
func SuggestMountPoint(source string) string {
	base := sanitizeName(source)
	if base == "" {
		base = "mount"
	}
	root := MountRoot()
	candidate := filepath.Join(root, base)
	for i := 2; ; i++ {
		if findByPoint(candidate) == nil && !isNonEmptyDir(candidate) {
			return candidate
		}
		candidate = filepath.Join(root, base+"-"+strconv.Itoa(i))
	}
}

func isNonEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

// sanitizeName turns a source description into a short directory name.
// A remote location keeps its host, because two mounts of /srv/backups from
// different machines must not read as the same thing in a shell prompt.
func sanitizeName(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}

	host := ""
	if idx := strings.Index(source, "://"); idx > 0 {
		rest := source[idx+3:]
		host = rest
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			host, rest = rest[:slash], rest[slash:]
		} else {
			rest = ""
		}
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
		source = rest
	}

	source = strings.TrimRight(source, "/\\")
	base := ""
	if source != "" {
		base = filepath.Base(filepath.FromSlash(source))
		if base == "." || base == string(filepath.Separator) {
			base = ""
		}
	}

	name := strings.Trim(cleanNameChars(host)+"-"+cleanNameChars(base), "-.")
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func cleanNameChars(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func describeSource(source string, v vfs.VFS, root string) string {
	if strings.TrimSpace(source) != "" {
		return source
	}
	if titled, ok := v.(vfs.TitleProvider); ok {
		if title := strings.TrimSpace(titled.GetTitle()); title != "" {
			return title + root
		}
	}
	return root
}

// ensureMountPoint creates the directory if needed and reports whether it
// had to. A mount point that already exists must be empty: mounting over
// somebody's files hides them until unmount, which looks like data loss.
func ensureMountPoint(point string) (created bool, err error) {
	info, err := os.Stat(point)
	switch {
	case err == nil && !info.IsDir():
		return false, fmt.Errorf("mount point %s is not a directory", point)
	case err == nil:
		if isNonEmptyDir(point) {
			return false, fmt.Errorf("mount point %s is not empty", point)
		}
		return false, nil
	case !os.IsNotExist(err):
		return false, err
	}
	if err := os.MkdirAll(point, 0o700); err != nil {
		return false, err
	}
	return true, nil
}
