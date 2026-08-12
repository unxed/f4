package vfs

import (
	"context"
	"io"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

var CustomConfigDir string

// App defines the interface for plugin-to-core UI interactions.
type App interface {
	GetActivePanelVFS() VFS
	GetPassivePanelVFS() VFS
	GetSelectedNames() []string
	GetSelectedName() string
	RefreshAll()
	SetPendingSelection(name string)
	RunProgressTask(title, startMsg string, forked bool, worker func(ctx context.Context, update func(msg string, percent int)) error, onComplete func(err error))
	RunAdvancedProgressTask(title string, forked bool, worker func(ctx context.Context, reporter TaskReporter) error, onComplete func(err error))
	// UI Bridge
	Message(title, msg string, buttons []string) int
	InputBox(title, prompt, history string, callback func(string))
	Menu(title string, items []string, callback func(int))
}

// HostAPI defines the functions f4 exposes to plugins.
type HostAPI interface {
	GetVersion() string
	Log(msg string)
	Message(msg string)

	RegisterHighlighter(p vtui.HighlighterProvider)
	RegisterVFSProvider(p VFSProvider)
	RegisterURIProvider(p URIProvider) error
	RegisterDrive(name string, factory func() VFS)
	RegisterGlobalHotkey(vk uint16, mods vtinput.ControlKeyState, handler func(app App))
	RegisterPluginMenuItem(label string, handler func(app App))
	RunAction(name string) bool
}

// VFSItem represents a generic file or directory entry.
type VFSItem struct {
	Name string
	Size int64
	// SizeKnown distinguishes a real zero-byte file from a remote object whose
	// length is unavailable until Open/materialization. Non-zero Size is always
	// treated as known for backwards compatibility with existing VFS plugins.
	SizeKnown    bool
	IsDir        bool
	MTime        time.Time
	Mode         string
	IsExecutable bool
	IsHidden     bool
	// NoExtension keeps dots in Name as part of the complete display name.
	// Virtual rows such as device selectors are not files and may contain an
	// IP address or another dotted identifier that must not be right-aligned
	// as though its tail were a file extension.
	NoExtension bool
	// IsSymlink is true when the entry is a filesystem symlink (or a
	// Windows reparse point that Go reports as a symlink). IsDir may
	// still be true for symlink-to-directory — the two flags are
	// orthogonal, and callers that want find/far2l-style "leaf" scan
	// semantics should treat any IsSymlink as a leaf regardless of
	// IsDir. Populated by OSVFS.ReadDir; other VFSes leave it false.
	IsSymlink bool
	// Device / Inode identify the underlying filesystem object so a
	// scanner can dedup hard links (same inode reached through
	// multiple paths in one walk). Both zero means "not populated" —
	// the scanner then simply doesn't dedup, matching prior behaviour.
	// Populated by OSVFS on Unix (stat.Dev/Ino). Windows and remote
	// VFSes leave them zero.
	Device uint64
	Inode  uint64
	// PhysicalSize is the real on-disk footprint of the item (compressed
	// size on NTFS / actual allocated blocks on Unix). Zero means the
	// platform didn't populate it (network VFSes, non-Unix/-Windows).
	// Consumers that display "physical size" should hide the metric
	// entirely when the accumulated total is 0.
	PhysicalSize int64
	// Revision is a provider-supplied, opaque strong identity for the current
	// file contents. A non-empty value must change whenever bytes (or a native
	// document's exported representation) can change. It is not a path, an
	// ETag precondition, or user-visible metadata; consumers may use it only as
	// a cache key together with the VFS session and canonical path.
	Revision string
	// Metadata for Attributes dialog
	ATime    time.Time // Last Access
	CTime    time.Time // Creation (Win) or Status Change (Unix)
	UnixMode uint32    // Raw numeric mode for chmod
	Uid, Gid int       // Ownership
	WinAttrs uint32    // Windows file attributes
}

// VFSCapabilities defines what the current VFS implementation can do efficiently.
type VFSCapabilities struct {
	HasServerSideCopy          bool
	HasServerSideMove          bool
	HasRandomAccess            bool // Supports ReadAt
	HasSearch                  bool // Supports server-side search
	HasUnixPermissions         bool // Indicates if VFS natively supports Unix-style permissions
	HasIdentityPreservingWrite bool // Create on an existing writable file updates content without replacing its canonical object identity.
	// HasAtomicNoReplaceRename guarantees that Rename with an explicit
	// DestinationOverwrite=false decision cannot replace an existing target,
	// including one created concurrently after a caller's Stat.
	HasAtomicNoReplaceRename bool
	// HasWrite says the backend can be written through at all. It is the
	// gate a writable FUSE mount asks before it comes up: refusing --rw for
	// a backend that cannot do it is a message, while discovering it in the
	// middle of a cp is a half-copied file. Default false, so a backend
	// opts in only once its write path has actually been exercised.
	HasWrite bool
}

// VFS is the core interface for file operations in f4.
type VFS interface {
	IsAtRoot() bool
	GetPath() string
	IsAbs(path string) bool
	SetPath(path string) error
	ReadDir(ctx context.Context, path string, onChunk func([]VFSItem)) error
	Stat(ctx context.Context, path string) (VFSItem, error)
	Join(elem ...string) string
	Abs(path string) (string, error)
	Base(path string) string
	Dir(path string) string

	// Mutations
	MkDir(ctx context.Context, path string) error
	Remove(ctx context.Context, path string) error
	Rename(ctx context.Context, oldpath, newpath string) error

	// Advanced / Remote Operations
	GetCapabilities() VFSCapabilities
	Search(ctx context.Context, path string, pattern string) (chan int64, error)

	// Random Access (required for high-performance Viewer/Editor)
	// Open returns a ReadAtCloser for the file.
	Open(ctx context.Context, path string) (ReadAtCloser, error)

	// Create returns a WriteCloser for new files.
	Create(ctx context.Context, path string) (io.WriteCloser, error)

	// SetAttributes updates file metadata (mode, ownership, times)
	SetAttributes(ctx context.Context, path string, item VFSItem) error

	ParentVFS() VFS // Returns the underlying VFS if this is a virtual mount, or nil

	Clone() VFS
	Close() error
}

// ManagedTransferWriter marks a destination whose Close performs the actual
// remote transfer and reports that transfer through ReporterKey. File-copy
// code must not count bytes merely staged into such a writer as remotely
// completed; doing so makes the total bar reach 100% before the upload has
// even started.
type ManagedTransferWriter interface {
	TransferProgressManaged() bool
}

// AbortableWriter is a staged writer whose Abort discards all bytes without
// publishing them at the destination. Abort must be idempotent and Close
// after a successful Abort must not commit. Remote VFS implementations use
// this at the error boundary between reading a source and committing an
// upload; context cancellation alone is not an explicit commit contract.
type AbortableWriter interface {
	Abort() error
}

// ManagedTransferDestination lets file-copy progress reserve a separate
// commit/upload phase before Create is called. A writer-level assertion is
// too late for sources which materialize during Open.
type ManagedTransferDestination interface {
	ManagedTransferWrites() bool
}

// RemoteTransferVFS marks a VFS whose sequential reads/writes can represent a
// network phase. It lets cross-cloud progress account for source download and
// destination upload separately while local-to-cloud remains one phase.
type RemoteTransferVFS interface {
	RemoteTransfer() bool
}

// TitleProvider allows a VFS to provide a custom display prefix (e.g. "user@host" for network drives).
type TitleProvider interface {
	GetTitle() string
}

// PanelTitleProvider can replace the complete path shown in a file-panel
// border without changing the canonical path used for filesystem operations.
// Returning an empty string keeps the ordinary TitleProvider + path layout.
type PanelTitleProvider interface {
	PanelTitle(path string) string
}

// PanelInfoRequest describes the file-panel state mirrored by an information
// panel. Providers must treat it as an immutable value; SelectedName may be
// empty when the panel has no actionable row under the cursor.
type PanelInfoRequest struct {
	Path         string
	SelectedName string
}

// PanelInfoValueKind tells the host how to format a provider field. Byte and
// usage values are deliberately typed so the Ctrl+L panel's B toggle applies
// to local and remote values in exactly the same way.
type PanelInfoValueKind uint8

const (
	PanelInfoText PanelInfoValueKind = iota
	PanelInfoBytes
	// PanelInfoUsage renders a two-line total/available meter. TotalBytes and
	// AvailableBytes carry the values; Bytes and Value are ignored.
	PanelInfoUsage
)

// PanelInfoField is one copyable label/value row. LabelKey is optional and,
// when present, is resolved by the host's localization catalog; Label is the
// provider-supplied fallback used by third-party plugins without catalog keys.
type PanelInfoField struct {
	ID       string
	LabelKey string
	Label    string
	Value    string
	Kind     PanelInfoValueKind
	Bytes    uint64
	// TotalBytes and AvailableBytes are used by PanelInfoUsage. Available may
	// legitimately be zero; providers should omit the field only when Total is
	// unknown.
	TotalBytes     uint64
	AvailableBytes uint64
}

// PanelInfoSection groups related fields under one heading. TitleKey follows
// the same localization/fallback rules as PanelInfoField.LabelKey.
type PanelInfoSection struct {
	ID       string
	TitleKey string
	Title    string
	Fields   []PanelInfoField
}

// PanelInfoSnapshot is an immutable rendering snapshot. Authoritative means
// the provider describes the system behind the current VFS (for example an
// Android device), so host computer/disk/memory rows must not be mixed into it.
type PanelInfoSnapshot struct {
	Authoritative bool
	Sections      []PanelInfoSection
	RefreshedAt   time.Time
}

// PanelInfoProvider lets a VFS contribute structured Ctrl+L information
// without blocking the UI thread. PanelInfoKey and CachedPanelInfo MUST be
// local, bounded and non-blocking. PanelInfoKey is also the async generation
// identity: it must change whenever Path or SelectedName would change the
// refresh result. RefreshPanelInfo may perform I/O and is always called by the
// host on a cancellable background task. The bool returned by CachedPanelInfo
// says whether the snapshot is still fresh; a stale snapshot remains useful
// while a refresh is in flight.
type PanelInfoProvider interface {
	PanelInfoKey(PanelInfoRequest) string
	CachedPanelInfo(PanelInfoRequest) (PanelInfoSnapshot, bool)
	RefreshPanelInfo(context.Context, PanelInfoRequest) (PanelInfoSnapshot, error)
}

type BulkCopier interface {
	CopyBulk(ctx context.Context, srcPaths []string, dstVfs VFS, dstDir string, reporter TaskReporter) error
}
type ArchiveLockManager struct {
	mu    sync.Mutex
	conds map[string]*sync.Cond
	busy  map[string]bool
}

var GlobalArchiveLockManager = &ArchiveLockManager{
	conds: make(map[string]*sync.Cond),
	busy:  make(map[string]bool),
}

func (m *ArchiveLockManager) Lock(path string) {
	m.mu.Lock()
	for m.busy[path] {
		cond, ok := m.conds[path]
		if !ok {
			cond = sync.NewCond(&m.mu)
			m.conds[path] = cond
		}
		cond.Wait()
	}
	m.busy[path] = true
	m.mu.Unlock()
}

func (m *ArchiveLockManager) TryLock(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy[path] {
		return false
	}
	m.busy[path] = true
	return true
}

func (m *ArchiveLockManager) Unlock(path string) {
	m.mu.Lock()
	m.busy[path] = false
	if cond, ok := m.conds[path]; ok {
		cond.Broadcast()
	}
	m.mu.Unlock()
}

// PtyProvider allows a VFS to provide its own PTY implementation
// (e.g. an SSH session for remote systems).
type PtyProvider interface {
	OpenPty(cols, rows int) (any, error)
}

// PtyAvailability lets a VFS whose concrete type can open a PTY report that
// the transport behind this particular instance cannot. For example, FISH+
// over SSH has a PTY provider while FISH+ over Android shell_v2 does not.
type PtyAvailability interface {
	PtyAvailable() bool
}

// OptimisticPathSetter changes a VFS view to a directory that the panel has
// already obtained from a directory listing. It must not perform remote I/O:
// the following ReadDir is the authoritative validation and handles a stale
// cached row asynchronously. VFS implementations that need SetPath's normal
// validation should expose this optional fast path in addition to SetPath.
type OptimisticPathSetter interface {
	SetPathOptimistic(path string) error
}

// TransferNameProvider lets a source VFS separate the stable name shown in
// its panel from the name that should be created in another file system. A
// provider may need this for display-only disambiguators or synthetic export
// extensions. Returning an empty string asks the host to use Base(srcPath).
type TransferNameProvider interface {
	TransferName(srcPath string, dst VFS) string
}

// ShareRole is the effective access granted by a share link. Providers expose
// only the roles they can implement for a particular object; callers must not
// assume that every remote file system supports every role.
type ShareRole uint8

const (
	ShareRoleViewer ShareRole = iota + 1
	ShareRoleCommenter
	ShareRoleEditor
	// ShareRoleUploader is a write-only link, such as an S3 presigned PUT.
	ShareRoleUploader
	// ShareRoleServerControlled describes a direct resource URL whose actual
	// access is determined by server ACLs. It is useful for generic WebDAV,
	// where authentication mode alone cannot prove whether GET is public.
	ShareRoleServerControlled
)

// ShareLink is a provider-issued URL and its effective access. ExpiresAt is
// zero for persistent links. Revocable is false for self-contained bearer
// URLs such as an S3 presigned request, which can only be allowed to expire.
type ShareLink struct {
	URL       string
	Role      ShareRole
	ExpiresAt time.Time
	// ExpiresAtIsMaximum means temporary signing credentials or a provider
	// policy can invalidate the link before ExpiresAt. The timestamp is still
	// the latest possible validity time and must not be presented as a promise.
	ExpiresAtIsMaximum bool
	Revocable          bool
}

// ShareLinkInfo describes link-sharing support for one concrete object.
// ExpirationOptions contains exact durations accepted by CreateShareLink; a
// zero duration means no expiration. Notice is safe, user-facing explanatory
// text and must never contain credentials or a signed URL.
type ShareLinkInfo struct {
	Provider          string
	ItemName          string
	Roles             []ShareRole
	ExpirationOptions []time.Duration
	DefaultExpiration time.Duration
	CanCreate         bool
	CanRevoke         bool
	// LinksUnenumerable means Link==nil is not evidence that no previously
	// issued links remain active. S3 presigned URLs are the canonical example:
	// they cannot be listed or individually revoked after creation.
	LinksUnenumerable bool
	// UnmanagedPublicAccess means the provider proved a separate kind of public
	// exposure which this dialog cannot enumerate or revoke. Unlike
	// LinksUnenumerable, it does not make the outcome of creating this dialog's
	// own link class unknowable. Google Workspace published views are the
	// canonical example.
	UnmanagedPublicAccess bool
	// LinkInherited means effective link access comes from a parent. The
	// discoverability flags let the UI explain search exposure without parsing
	// provider prose; LinkDiscoverabilityInherited identifies its source.
	LinkInherited                bool
	LinkDiscoverable             bool
	LinkDiscoverabilityInherited bool
	Link                         *ShareLink
	Notice                       string
}

// ShareLinkRequest asks a provider to create or update a link. Role and
// ExpiresIn must be selected from the corresponding ShareLinkInfo values.
type ShareLinkRequest struct {
	Role      ShareRole
	ExpiresIn time.Duration
}

// ShareLinkProvider is the optional VFS capability used by the Files > Share
// action. Implementations must honor cancellation. Link URLs are bearer
// credentials on some providers and must not be logged by implementations.
type ShareLinkProvider interface {
	ShareLinkInfo(context.Context, string) (ShareLinkInfo, error)
	CreateShareLink(context.Context, string, ShareLinkRequest) (ShareLink, error)
	RevokeShareLink(context.Context, string) error
}

// PanelAction identifies a semantic file-panel operation. Unlike raw key
// interception it follows action remapping, menu invocation and mouse
// activation. Paths passed to PanelActionHandler are paths in the receiver's
// VFS; Create receives the current directory.
type PanelAction uint8

const (
	PanelActionActivate PanelAction = iota
	PanelActionEdit
	PanelActionCreate
	PanelActionDelete
)

// PanelActionHandler lets virtual manager rows implement semantic panel
// actions. It returns true only when the action was consumed; false preserves
// the ordinary host behavior.
type PanelActionHandler interface {
	HandlePanelAction(app App, action PanelAction, paths []string) bool
}

// VFSProvider умеет определять, может ли он открыть путь, и создавать экземпляр VFS.
type VFSProvider interface {
	Name() string
	// Priority: чем выше, тем раньше провайдер опрашивается (архивы обычно имеют низкий приоритет)
	Priority() int
	// CanOpen возвращает true, если провайдер понимает этот путь.
	// parent — текущая VFS, в которой находится объект.
	CanOpen(ctx context.Context, parent VFS, path string) bool
	// Open создает новый экземпляр VFS.
	Open(ctx context.Context, parent VFS, path string) (VFS, error)
}

// StandalonePathProvider can restore a virtual filesystem from its own
// user-facing absolute path even when the current panel belongs to another
// VFS. Implementations must recognize only paths they own.
type StandalonePathProvider interface {
	VFSProvider
	OpensStandalonePaths() bool
}

// VirtualDirectoryProvider marks a provider whose entry is deliberately
// rendered as a directory even though opening it mounts another VFS instead
// of calling SetPath on the current one.
type VirtualDirectoryProvider interface {
	VFSProvider
	OpensVirtualDirectories() bool
}

var providerRegistry = struct {
	sync.RWMutex
	items []VFSProvider
}{}

func RegisterProvider(p VFSProvider) {
	providerRegistry.Lock()
	providerRegistry.items = append(providerRegistry.items, p)
	providerRegistry.Unlock()
	// Сортируем по приоритету
}

// UnregisterProvider removes one exact provider instance. It is primarily
// used by plugin unload/reload and tests; non-comparable value providers are
// deliberately not matched because they have no stable instance identity.
func UnregisterProvider(target VFSProvider) bool {
	if target == nil {
		return false
	}
	providerRegistry.Lock()
	defer providerRegistry.Unlock()
	targetType := reflect.TypeOf(target)
	if targetType == nil || !targetType.Comparable() {
		return false
	}
	for i, provider := range providerRegistry.items {
		if reflect.TypeOf(provider) == targetType && provider == target {
			providerRegistry.items = append(providerRegistry.items[:i], providerRegistry.items[i+1:]...)
			return true
		}
	}
	return false
}

func FindProvider(ctx context.Context, parent VFS, path string) VFSProvider {
	providerRegistry.RLock()
	providers := append([]VFSProvider(nil), providerRegistry.items...)
	providerRegistry.RUnlock()
	for _, p := range providers {
		if p.CanOpen(ctx, parent, path) {
			return p
		}
	}
	return nil
}

// FindStandaloneProvider returns a registered provider which explicitly owns
// path as a standalone user-facing location (for example Account:\\Folder).
func FindStandaloneProvider(ctx context.Context, parent VFS, path string) VFSProvider {
	providerRegistry.RLock()
	providers := append([]VFSProvider(nil), providerRegistry.items...)
	providerRegistry.RUnlock()
	for _, provider := range providers {
		standalone, ok := provider.(StandalonePathProvider)
		if !ok || !standalone.OpensStandalonePaths() {
			continue
		}
		if provider.CanOpen(ctx, parent, path) {
			return provider
		}
	}
	return nil
}

// ReadAtCloser combines reader interfaces with context support.
// CommandRunner is implemented by a file system that can run a command where
// its files are. For a remote one that is the difference between downloading
// a tree to grep it and asking the host a question; for a local one there is
// nothing to implement, since a shell is already there.
type CommandRunner interface {
	// RunCommand runs the command line in dir and hands each line of its
	// output to cb as it arrives, returning the exit status. A non-zero
	// status is not an error: the command ran and said something.
	RunCommand(ctx context.Context, dir, command string, cb func(line string)) (int, error)
}

// CommandDialect describes the syntax understood by a CommandRunner.
type CommandDialect uint8

const (
	CommandDialectUnknown CommandDialect = iota
	CommandDialectPOSIX
	CommandDialectCmd
	CommandDialectPowerShell
)

// CommandLiteralPercentEnv is reserved by F4's cmd.exe argument compiler.
// CommandDialectCmd runners define it as one literal percent character.
const CommandLiteralPercentEnv = "F4_APPLY_LITERAL_PERCENT_8C1E"

type CommandRunnerInfo struct {
	Dialect     CommandDialect
	MaxParallel int
}

type CommandRunnerInfoProvider interface {
	CommandRunnerInfo() CommandRunnerInfo
}

type CommandRunnerAvailabilityProvider interface {
	CommandRunnerAvailable() bool
}

type CommandListANSIEncoder interface {
	EncodeCommandListANSI(text []byte) ([]byte, error)
}

// PrivateCommandFileCreator creates a command list file with private
// permissions from the moment it becomes visible on the command host.
type PrivateCommandFileCreator interface {
	CreatePrivateCommandFile(ctx context.Context, path string) (io.WriteCloser, error)
}

// DuplicateProgress reports how far a duplicate search has got. Total is how
// many files turned out to be worth reading at all, which is known only once
// the tree has been walked, so it is not the number of files in it.
type DuplicateProgress struct {
	Done  int
	Total int
	Path  string
}

// DuplicateFinder is implemented by a file system that can find files with
// identical content on its own side. Doing it from here means reading every
// candidate across the network, which for a remote tree costs more than the
// answer is worth; a file system that cannot do it simply does not offer the
// command. Each group holds two or more paths with the same content.
type DuplicateFinder interface {
	FindDuplicates(ctx context.Context, dir string, cb func(DuplicateProgress)) ([][]string, error)
}

// PatchPiece is one piece of a file being assembled. Data set means literal
// new bytes; Data nil means Length bytes taken from the existing file at
// Offset, which then never have to travel.
type PatchPiece struct {
	Offset int64
	Length int64
	Data   []byte
}

// DeltaWriter is implemented by a file system that can build a file out of
// pieces of another one on its own side. An editor saving a one byte change
// in a large remote file then sends one byte rather than the file. Like the
// other optional interfaces here, a caller that does not find it writes the
// file out in full as before.
type DeltaWriter interface {
	PatchFile(ctx context.Context, src, dst string, pieces []PatchPiece) error
}

// FoundEntry is one hit of a tree search.
type FoundEntry struct {
	// Path is the full path of the file, in the file system's own notation.
	Path string
	// Item describes it the way a listing would.
	Item VFSItem
}

// FindQuery describes a tree search.
type FindQuery struct {
	// Masks are shell globs matched against the file name; at least one.
	Masks []string
	// Text, when set, keeps only files containing it as a plain string.
	Text string
	// IgnoreCase folds case for Text.
	IgnoreCase bool
	// Limit caps the number of hits; zero leaves it to the file system.
	Limit int
	// Progress, when non-nil, is called periodically while the search
	// runs — file systems that support it (currently FISH+ against a
	// Windows peer) report the last path the walk visited and running
	// counters, so a dialog can show real-time state instead of a
	// spinner. Called on the goroutine that drives FindFiles; the
	// callback must not block. A file system that has no progress to
	// report simply never calls it, which is what the interface's
	// callers must be ready for.
	Progress func(FindProgress)
}

// FindProgress is a checkpoint reported by an in-flight tree search.
type FindProgress struct {
	// Scanned is how many entries the walk has visited so far.
	Scanned int64
	// Found is how many entries have matched so far.
	Found int64
	// Path is the last path the walk looked at, in whatever shape the
	// file system uses on the wire.
	Path string
}

// FileFinder is implemented by a file system that can walk a tree on its own
// side. Like LineIndexer it is an optional interface: a local file system is
// no faster for it, so only the ones that gain from it carry it, and a
// caller that does not find it walks the tree itself as before.
type FileFinder interface {
	FindFiles(ctx context.Context, dir string, q FindQuery) ([]FoundEntry, error)
}

// PtyShellIntegration is an optional interface for a VFS that owns a
// remote PTY. It lets the VFS compose the exact bytes to send for the
// integration tasks the panel does through the PTY — syncing its cwd
// to the panel's, running a command line from the cmdline, sending an
// interrupt — so a peer whose PTY runs a non-POSIX shell (cmd.exe on
// Windows) can be driven correctly without the caller learning about
// its shell. Fallback for a VFS that does not implement this is the
// caller's built-in POSIX-style templates.
type PtyShellIntegration interface {
	// PtyChangeDirCommand returns the bytes to send to the PTY to
	// change to dir. Path is in whatever shape the VFS uses on its
	// wire; translation to what the PTY shell expects is the VFS's
	// job. An empty return means "do not sync this path" (the VFS
	// declines gracefully rather than sending broken syntax).
	PtyChangeDirCommand(dir string) []byte

	// PtyRunCommand returns the bytes to send to the PTY to run
	// command in dir. If dir is empty, run wherever the PTY is now.
	// An empty return declines the run.
	PtyRunCommand(dir, command string) []byte

	// PtyInterrupt returns the bytes to send to interrupt whatever
	// command the PTY is currently running. Typically {0x03} — a
	// literal Ctrl+C byte — since both cmd.exe over ConPTY and every
	// POSIX shell treat that as SIGINT.
	PtyInterrupt() []byte

	// PtyInitSequence returns bytes to send to the PTY exactly once,
	// right after it opens and before the caller sends anything else.
	// A Windows peer uses this to install a PROMPT that embeds an OSC
	// 133 D marker, matching what the local Windows PTY does — every
	// time cmd shows the prompt (which is when the last command
	// finished) the caller's OSC 133 handler is fired and the panel
	// frame's OnBusyChange returns to panels. Returning nil means the
	// shell needs no init, which is the honest answer for a POSIX
	// peer whose command templates emit their own OSC 133.
	PtyInitSequence() []byte
}

// LineIndexResult is what a LineIndexer answers with.
type LineIndexResult struct {
	// First is the one-based number of the line Offsets[0] belongs to.
	First int64
	// Offsets holds the byte offset of each line start, in file order. It is
	// shorter than requested when the file ends first.
	Offsets []int64
	// Total is the number of lines in the file.
	Total int64
}

// LineIndexer is implemented by a file system that can have the far side
// work out where lines begin, so that a viewer does not have to read a file
// in order to count it. It is deliberately an optional interface rather than
// a method on VFS: a local file system gains nothing from it, and an archive
// cannot answer it at all, so neither should be made to carry it. A caller
// type asserts for it and keeps its own behaviour when the assertion fails.
type LineIndexer interface {
	LineIndex(ctx context.Context, path string, first, count int64) (LineIndexResult, error)
}

// SessionReconnector is implemented by a file system that lives on a
// connection and can rebuild it. It is optional for the same reason
// LineIndexer is: a local file system has no session to lose, and one that
// was handed a stream it cannot open a second time has nothing to rebuild
// with, so neither should be made to carry it.
//
// Reconnecting is deliberately not done inside a failing request. A request
// that reconnected on its own would turn one failure into a delay of unknown
// length in the middle of an operation, with no way for the user to say no.
// What the interface offers instead is the three questions a caller that met
// the failure needs answered: was the session lost, can it be rebuilt, and
// rebuild it.
type SessionReconnector interface {
	// SessionLost reports whether an error means the connection died rather
	// than the operation itself failing. A missing file is not a lost session
	// and must not be answered as one.
	SessionLost(err error) bool
	// CanReconnect reports whether a new connection can be built. A caller
	// asks before offering the user a choice it cannot honour.
	CanReconnect() bool
	// Reconnect builds it. What survives is what lives on this side; anything
	// the far side was doing is gone, which is why the caller decides what to
	// retry rather than this method retrying anything itself.
	Reconnect(ctx context.Context) error
}

// SessionIdentity is implemented by a file system that shares its connection
// with other views of itself. Two of them answering the same key speak
// through the same session, so whatever happens to it happens to both. The
// key is compared and nothing else, which is why it is only ever an opaque
// value: a caller that starts to read it is asking a question the file system
// did not offer to answer. It must therefore be comparable, and a pointer to
// whatever holds the connection is the obvious thing to hand over.
//
// It exists because a reconnect is not a private matter. Work started from
// one panel dies with a session another panel rebuilt, and the only way to
// find that work is to know which panels were on the same connection.
type SessionIdentity interface {
	SessionKey() any
}

// DirectoryCacheIdentity gives panel directory caches a stable, comparable
// identity across short-lived VFS instances for the same configured remote.
// It is used only as an in-memory cache key and must not contain credentials.
// Implementations should change it whenever connection settings which affect
// directory contents change.
type DirectoryCacheIdentity interface {
	DirectoryCacheKey() any
}

// ServerSideCopier is implemented by a file system that can copy an object
// on the server side, avoiding pulling bytes back and forth through the client.
type ServerSideCopier interface {
	Copy(ctx context.Context, oldpath, newpath string) error
}

// SameSession reports whether two VFS instances share the same
// session/connection. Display titles are deliberately not identities: two
// accounts, endpoints, ports or buckets may have the same user-facing label.
func SameSession(v1, v2 VFS) bool {
	if v1 == nil || v2 == nil {
		return v1 == nil && v2 == nil
	}
	v1Type, v2Type := reflect.TypeOf(v1), reflect.TypeOf(v2)
	if v1Type == v2Type && v1Type.Comparable() && v1 == v2 {
		return true
	}
	id1, ok1 := v1.(SessionIdentity)
	id2, ok2 := v2.(SessionIdentity)
	if !ok1 || !ok2 {
		return false
	}
	k1, k2 := id1.SessionKey(), id2.SessionKey()
	if k1 == nil || k2 == nil {
		return false
	}
	t1, t2 := reflect.TypeOf(k1), reflect.TypeOf(k2)
	return t1.Comparable() && t2.Comparable() && k1 == k2
}

// ConnectionInfoProvider allows a VFS to expose its remote connection details
// (host, port, user) so other VFSes on different hosts can connect to it directly.
type ConnectionInfoProvider interface {
	ConnectionInfo() (host, port, user string, ok bool)
}

type ReadAtCloser interface {
	ReadAt(ctx context.Context, p []byte, off int64) (n int, err error)
	Read(ctx context.Context, p []byte) (n int, err error)
	io.Closer
	Size() int64
} // TempFileWrapper is a helper for VFS that need to extract files to temp storage.
type TempFileWrapper struct {
	*os.File
	SizeVal  int64
	TempPath string
}

func (w *TempFileWrapper) Size() int64 { return w.SizeVal }
func (w *TempFileWrapper) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	return w.File.ReadAt(p, off)
}
func (w *TempFileWrapper) Read(ctx context.Context, p []byte) (int, error) {
	return w.File.Read(p)
}

func (w *TempFileWrapper) Close() error {
	err := w.File.Close()
	os.Remove(w.TempPath)
	return err
}

type progressKeyType struct{}
type reporterKeyType struct{}
type destinationOverwriteKeyType struct{}

var ProgressKey = progressKeyType{}
var ReporterKey = reporterKeyType{}

// WithDestinationOverwrite records the caller's already-resolved conflict
// decision for a destination mutation. Providers can turn this into an
// atomic protocol precondition instead of repeating a racy Stat request.
func WithDestinationOverwrite(ctx context.Context, overwrite bool) context.Context {
	return context.WithValue(ctx, destinationOverwriteKeyType{}, overwrite)
}

// DestinationOverwrite returns the caller's explicit overwrite decision.
// A false second result means that the caller did not resolve a conflict and
// the VFS operation should retain its ordinary replacement semantics.
func DestinationOverwrite(ctx context.Context) (overwrite, known bool) {
	if ctx == nil {
		return false, false
	}
	overwrite, known = ctx.Value(destinationOverwriteKeyType{}).(bool)
	return overwrite, known
}

type ProgressCallback func(msg string, percent int)

type TaskReporter interface {
	UpdateScan(currentPath string, files, dirs int64)
	UpdateTransfer(action string, filename string, currentPct int, totalText string, totalPct int, speedText string)
	IsCancelled() bool
}

type FileProgress interface {
	StartFile(name string, size int64)
	UpdateBytes(n int)
	FileDone()
	DirDone()
	FileSkipped()
}
