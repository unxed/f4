package netfox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/f4/plugins/netfox/fishplus"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// fishReadDirChunk is how many entries are handed to the panel at once,
// matching what the SFTP backend does.
const fishReadDirChunk = 500

// FishVFS exposes a FISH+ session as an f4 file system. It owns the session
// and closes it with itself; the transport underneath is whatever stream
// the caller handed over, which is what lets the tests run it on a local
// shell instead of on ssh.
type FishVFS struct {
	parent              vfs.VFS
	conn                *fishConn
	pathMu              sync.RWMutex
	path                string
	title               string
	once                sync.Once
	panelTitleFormatter func(title, path string) string
	host                string
	port                string
	user                string

	panelInfoMu sync.RWMutex
	panelInfo   vfs.PanelInfoProvider
}

// fishConn keeps one FISH+ session alive for as long as any of the VFS
// instances built on it is still in use. f4 clones a panel's file system in
// several places — the "other panel" menu item and the frame snapshot taken
// for a background task — and every panel closes its own file system when it
// leaves it. Handing back the same instance, the way the SFTP backend does,
// would therefore let one panel tear the session down under another, and
// would make both panels share a single current directory.
//
// Requests from the clones interleave freely: the session serializes them
// with its own mutex, so each request stays atomic. They do not run in
// parallel, which for a shell that answers one command at a time is what a
// second connection could not fix anyway.
// FishDialer opens a new transport for a session that has to be rebuilt. It
// returns the two halves of the stream and the closer that ties the remote
// shell and its connection to the session speaking through them — the same
// three things NewFishVFSOnStream is handed. Whoever built the connection
// supplies it, which is what keeps SSH out of this file and lets the tests
// reconnect to a local shell instead.
//
// A nil dialer means the connection cannot be rebuilt, which is the honest
// answer for a caller that handed over a pair of streams it has no second copy
// of.
type FishDialer func(ctx context.Context) (io.Writer, io.Reader, io.Closer, error)

type fishConn struct {
	client *fishplus.Client
	ka     *fishplus.Keepalive
	// dial and opts describe how to bring the primary shell flavor up. A
	// site the first attempt cannot reach — a Windows peer whose sshd
	// answers "exec /bin/sh" with a PowerShell parse error — is served by
	// dialAlt/optsAlt instead. Once a fallback succeeds it becomes the
	// primary, so every reconnect after the first goes straight to the
	// flavor that actually works.
	dial    FishDialer
	opts    fishplus.HandshakeOptions
	dialAlt FishDialer
	optsAlt fishplus.HandshakeOptions
	closer  io.Closer

	mu     sync.Mutex
	refs   int
	closed bool

	// key is set once, at construction, on a connection built for a real
	// site (see NewFishVFS). It is what tells fishConn.release whether a
	// connection whose last view just closed belongs in the pool or should
	// simply be shut down — a connection built on a bare stream, with no
	// site of its own to be reopened, carries the zero key and is never
	// pooled.
	key fishPoolKey
}

// ErrNoDialer is what a reconnect answers when nobody told the connection how
// to rebuild itself.
var ErrNoDialer = errors.New("fishplus: this session cannot be reconnected")

// current hands out the client under the lock, so a caller that took one before
// a reconnect and used it after does not talk to the session that died.
func (c *fishConn) current() *fishplus.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

// reconnect replaces a dead session with a new one and reports the client that
// took its place. The new shell knows nothing of what the old one was doing:
// its jobs are gone, and a write or a patch that was in flight cannot be
// resumed, only reported. What survives is what lives on this side — the path a
// panel stands in, a read handle, and the credentials the dialer holds.
//
// A caller that lost a race and finds the session already replaced gets the
// replacement rather than a second reconnect, which is why the check happens
// under the lock and the client it saw is the thing compared.
func (c *fishConn) reconnect(ctx context.Context, dead *fishplus.Client) (*fishplus.Client, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fishplus.ErrBroken
	}
	if dead != nil && c.client != dead {
		fresh := c.client
		c.mu.Unlock()
		return fresh, nil
	}
	dial, opts := c.dial, c.opts
	dialAlt, optsAlt := c.dialAlt, c.optsAlt
	c.mu.Unlock()

	if dial == nil {
		return nil, ErrNoDialer
	}

	// Dialling and the handshake happen outside the lock: both take as long as
	// the network takes, and holding the lock would stall every other view of
	// this connection for that whole time.
	sess, usedAlt, closer, err := establishWithFallback(ctx, dial, opts, dialAlt, optsAlt)
	if err != nil {
		return nil, err
	}
	client := fishplus.NewClient(sess)
	// One round trip before anything is promised: a handshake that answered
	// says the helper is there, and a noop says the request loop is running.
	if err := sess.Noop(ctx); err != nil {
		_ = sess.Close() // Preserve the failed readiness check.
		return nil, err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = sess.Close() // Preserve the connection-closed result.
		return nil, fishplus.ErrBroken
	}
	if dead != nil && c.client != dead {
		// Somebody else reconnected while this one was dialling. Theirs is as
		// good as this one and is already in use, so this one is dropped.
		fresh := c.client
		c.mu.Unlock()
		_ = sess.Close() // The concurrently installed session remains authoritative.
		return fresh, nil
	}
	old, oldKA := c.client, c.ka
	c.client = client
	c.ka = fishplus.StartKeepalive(client, fishplus.DefaultKeepaliveInterval)
	c.closer = closer
	if usedAlt {
		// The fallback answered where the primary did not. Promote it so
		// every reconnect after this one goes straight to what works, and
		// the primary is only tried again if the new primary also fails.
		c.dial, c.opts, c.dialAlt, c.optsAlt = c.dialAlt, c.optsAlt, c.dial, c.opts
	}
	c.mu.Unlock()

	oldKA.Stop()
	if old != nil {
		_ = old.Session().Close() // The replaced broken session is best-effort cleanup.
	}
	return client, nil
}

func (c *fishConn) retain() {
	c.mu.Lock()
	c.refs++
	c.mu.Unlock()
}

func (c *fishConn) release() error {
	c.mu.Lock()
	c.refs--
	if c.refs > 0 || c.closed {
		c.mu.Unlock()
		return nil
	}
	key := c.key
	c.mu.Unlock()

	if key.valid() {
		// Keep the session running rather than closing it: the pool's own
		// idle timer decides when it has genuinely gone unused for too
		// long, which is what step 18 asks for. Closing a panel therefore
		// never blocks on the far side either way.
		globalFishPool.park(c, key)
		return nil
	}
	return c.shutdown()
}

// NewFishVFSOnStream completes the handshake on an already established pair
// of streams and opens the panel in whatever directory the remote shell
// started in. closer may be nil; when set it is closed together with the
// session, which is also what makes the remote helper exit.
func NewFishVFSOnStream(ctx context.Context, parent vfs.VFS, stdin io.Writer, stdout io.Reader, closer io.Closer, title string) (*FishVFS, error) {
	return newFishVFSOnStream(ctx, parent, stdin, stdout, closer, title, nil, fishplus.HandshakeOptions{})
}

// NewFishVFSOnStreamWithOptions is the transport-specific variant of
// NewFishVFSOnStream. Most callers should keep the default line bootstrap;
// transports such as Android shell_v2 can select the single-line base64
// bootstrap without changing the FISH+ filesystem implementation.
func NewFishVFSOnStreamWithOptions(ctx context.Context, parent vfs.VFS, stdin io.Writer, stdout io.Reader, closer io.Closer, title string, opts fishplus.HandshakeOptions) (*FishVFS, error) {
	return newFishVFSOnStream(ctx, parent, stdin, stdout, closer, title, nil, opts)
}

// NewFishVFSOnDialer builds a session the same way and remembers how to build
// it again, which is what a reconnect needs. The dialer is called once here so
// that a site that cannot be reached fails at open time rather than at the
// first request.
func NewFishVFSOnDialer(ctx context.Context, parent vfs.VFS, dial FishDialer, title string) (*FishVFS, error) {
	return NewFishVFSOnDialers(ctx, parent, dial, fishplus.HandshakeOptions{}, nil, fishplus.HandshakeOptions{}, title)
}

// NewFishVFSOnDialers is NewFishVFSOnDialer with a fallback: a peer the
// first (dial, opts) pair cannot bring a helper up on is retried with the
// second pair. Meant for sites where the shell flavor is not known at
// open time — a POSIX login shell wins the common case, and a PowerShell
// peer answers only after the retry. Once the fallback succeeds it is
// promoted to primary, so every reconnect after the first goes straight
// to the flavor that actually works. A nil dialAlt means no fallback.
func NewFishVFSOnDialers(ctx context.Context, parent vfs.VFS, dial FishDialer, opts fishplus.HandshakeOptions, dialAlt FishDialer, optsAlt fishplus.HandshakeOptions, title string) (*FishVFS, error) {
	if dial == nil {
		return nil, ErrNoDialer
	}
	sess, usedAlt, closer, err := establishWithFallback(ctx, dial, opts, dialAlt, optsAlt)
	if err != nil {
		return nil, err
	}
	if usedAlt {
		dial, opts, dialAlt, optsAlt = dialAlt, optsAlt, dial, opts
	}
	return newFishVFSFromSession(parent, sess, closer, title, dial, opts, dialAlt, optsAlt), nil
}

func newFishVFSOnStream(ctx context.Context, parent vfs.VFS, stdin io.Writer, stdout io.Reader, closer io.Closer, title string, dial FishDialer, opts fishplus.HandshakeOptions) (*FishVFS, error) {
	sess := fishplus.NewSession(stdin, stdout, closer)
	if err := sess.HandshakeWithOptions(ctx, opts); err != nil {
		_ = sess.Close() // Preserve the handshake failure.
		return nil, err
	}
	return newFishVFSFromSession(parent, sess, closer, title, dial, opts, nil, fishplus.HandshakeOptions{}), nil
}

// newFishVFSFromSession wraps an already-handshaked session in a FishVFS,
// used by every construction path so the wrapper stays in one place.
func newFishVFSFromSession(parent vfs.VFS, sess *fishplus.Session, closer io.Closer, title string, dial FishDialer, opts fishplus.HandshakeOptions, dialAlt FishDialer, optsAlt fishplus.HandshakeOptions) *FishVFS {
	client := fishplus.NewClient(sess)
	cwd, err := client.Pwd(context.Background())
	if err != nil || !path.IsAbs(cwd) {
		cwd = "/"
	}
	return &FishVFS{
		parent: parent,
		conn: &fishConn{
			client:  client,
			refs:    1,
			ka:      fishplus.StartKeepalive(client, fishplus.DefaultKeepaliveInterval),
			dial:    dial,
			opts:    opts,
			dialAlt: dialAlt,
			optsAlt: optsAlt,
			closer:  closer,
		},
		path:  cwd,
		title: title,
	}
}

// establishSession dials, brings up the helper with the given handshake
// options, and returns a session that is ready to serve requests. The
// closer returned is the one the dialer supplied; it is closed with the
// session on failure and stored on fishConn on success.
func establishSession(ctx context.Context, dial FishDialer, opts fishplus.HandshakeOptions) (*fishplus.Session, io.Closer, error) {
	stdin, stdout, closer, err := dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	sess := fishplus.NewSession(stdin, stdout, closer)
	if err := sess.HandshakeWithOptions(ctx, opts); err != nil {
		_ = sess.Close() // Preserve the handshake failure.
		return nil, nil, err
	}
	return sess, closer, nil
}

// establishWithFallback tries the primary (dial, opts) pair first and
// falls back to (dialAlt, optsAlt) only when the primary handshake fails
// — that is, after the transport already answered. A dial-level failure
// is returned as-is: the network cannot be worked around by trying a
// different shell. A nil dialAlt disables the fallback, which is what
// callers with a known-good flavor pass. The bool report tells the caller
// which pair produced the session so it can promote the fallback for
// future reconnects.
func establishWithFallback(ctx context.Context, dial FishDialer, opts fishplus.HandshakeOptions, dialAlt FishDialer, optsAlt fishplus.HandshakeOptions) (*fishplus.Session, bool, io.Closer, error) {
	sess, closer, err := establishSession(ctx, dial, opts)
	if err == nil {
		return sess, false, closer, nil
	}
	// A dial failure — no route, refused, auth — has no shell flavor to
	// try. Only a handshake failure means the transport is fine but the
	// far side did not recognize the bootstrap; only then is the fallback
	// worth trying.
	if _, isRemoteErr := err.(*fishplus.RemoteError); !isRemoteErr && !isHandshakeFailure(err) {
		return nil, false, nil, err
	}
	if dialAlt == nil {
		return nil, false, nil, err
	}
	sessAlt, closerAlt, errAlt := establishSession(ctx, dialAlt, optsAlt)
	if errAlt != nil {
		// The original error is more informative — the alt was a guess,
		// the primary is what the caller configured.
		return nil, false, nil, err
	}
	return sessAlt, true, closerAlt, nil
}

// isHandshakeFailure recognizes the errors HandshakeWithOptions raises
// when the far side answered but did not speak the expected protocol.
// A read that failed because the transport is broken should not trigger
// a shell-flavor retry, since neither flavor would fare any better.
func isHandshakeFailure(err error) bool {
	if err == nil {
		return false
	}
	// The far side hung up while the bootstrap was being uploaded or
	// waited for. That is what a Windows peer does with the POSIX attempt:
	// sshd resolves the exec request to powershell.exe -c "exec /bin/sh",
	// PowerShell fails to parse it, prints to stderr — which the dialer
	// discards — and exits, closing the channel. Measured against a real
	// sshd this arrives as EOF within half a second, and it is the most
	// direct evidence there is that this flavor is the wrong one.
	//
	// The dial already succeeded by the time this runs, so an EOF here is
	// a shell that would not stay, not a network that will not carry.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	msg := err.Error()
	// The two phrases parseBanner / HandshakeWithOptions build for
	// "answered but wrong": one for the banner shape, one for the version.
	return strings.Contains(msg, "unexpected banner") ||
		strings.Contains(msg, "remote speaks protocol") ||
		strings.Contains(msg, "never reported being ready") ||
		strings.Contains(msg, "bad terminator")
}

// NewFishVFS opens a site over SSH. It goes through a dialer rather than
// through a pair of streams, so the session it hands back is one that can be
// rebuilt after the connection drops; a site opened any other way would have
// to be reopened by hand.
func NewFishVFS(parent vfs.VFS, host, port, user, pass, keyPath string, timeout int, px netproxy.Settings) (*FishVFS, error) {
	key := fishPoolKey{host: host, port: port, user: user, proxy: px}
	if conn := globalFishPool.take(key); conn != nil {
		return newFishVFSFromPooledConn(parent, conn, host, port, user), nil
	}

	vtui.DebugLog("NET: Initiating FISH+ connection to %s:%s (user: %s)", host, port, user)
	title := host
	if user != "" {
		title = user + "@" + host
	}
	ctx, cancel := context.WithTimeout(context.Background(), sshTimeout(timeout))
	defer cancel()
	// The primary flavor is POSIX (exec /bin/sh + line bootstrap): the
	// bulk of hosts a panel opens are Linux or a BSD, and the client's
	// existing tests exercise that path in every commit. A Windows peer
	// with sshd's DefaultShell set to powershell.exe answers the POSIX
	// bootstrap with a parse error; the fallback dialer catches that,
	// asks sshd for a plain shell (which resolves to PowerShell on
	// Windows), and delivers the base64-encoded helper.ps1 through it.
	v, err := NewFishVFSOnDialers(ctx, parent,
		sshFishDialer(host, port, user, pass, keyPath, timeout, px), fishplus.HandshakeOptions{},
		sshFishDialerPwsh(host, port, user, pass, keyPath, timeout, px), fishplus.HandshakeOptions{Bootstrap: fishplus.BootstrapBase64LinePwsh},
		title)
	if err != nil {
		return nil, err
	}
	v.host = host
	v.port = port
	v.user = user
	v.conn.mu.Lock()
	v.conn.key = key
	v.conn.mu.Unlock()
	vtui.DebugLog("NET: FISH+ session established, features: %s", v.client().Session().Features().Raw)
	return v, nil
}

// ConnectionInfo implements vfs.ConnectionInfoProvider.
func (v *FishVFS) ConnectionInfo() (host, port, user string, ok bool) {
	return v.host, v.port, v.user, true
}

// client is how every request reaches the session. It asks the connection
// instead of remembering, so a view whose neighbour reconnected is on the live
// session at its next request rather than at its next Clone: the connection is
// the only thing that sees a session, a FishVFS only ever sees a view of one.
func (v *FishVFS) client() *fishplus.Client {
	if v.conn == nil {
		return nil
	}
	return v.conn.current()
}

// Client exposes the underlying protocol client, mostly so a caller can ask
// what the remote host turned out to be capable of.
func (v *FishVFS) Client() *fishplus.Client { return v.client() }

// PtyAvailable distinguishes SSH-backed FISH+ from transports such as
// Android shell_v2. The concrete FishVFS type has OpenPty for the SSH case,
// but treating every instance as a remote PTY makes Android navigation send
// Unix cd commands into f4's local terminal on every path change.
func (v *FishVFS) PtyAvailable() bool {
	if v.conn == nil {
		return false
	}
	v.conn.mu.Lock()
	closer := v.conn.closer
	v.conn.mu.Unlock()
	_, ok := closer.(vfs.PtyProvider)
	return ok
}

func (v *FishVFS) OpenPty(cols, rows int) (any, error) {
	if v.conn == nil {
		return nil, errors.New("pty not supported on this FISH+ connection")
	}
	v.conn.mu.Lock()
	closer := v.conn.closer
	v.conn.mu.Unlock()
	if pp, ok := closer.(vfs.PtyProvider); ok {
		return pp.OpenPty(cols, rows)
	}
	return nil, errors.New("pty not supported on this FISH+ connection")
}

// CanReconnect reports whether this file system can rebuild its session. A
// site opened from a configuration can; one handed a pair of streams cannot,
// because there is no second pair. A caller offering the user a choice has to
// know which it is before it offers one.
func (v *FishVFS) CanReconnect() bool {
	if v.conn == nil {
		return false
	}
	v.conn.mu.Lock()
	defer v.conn.mu.Unlock()
	return v.conn.dial != nil && !v.conn.closed
}

// SessionLost implements vfs.SessionReconnector. Only a session that stopped
// speaking counts: a file that is not there, a permission that was refused or
// a cancelled request are failures of the request, and offering to reconnect
// over any of them would be noise in front of the real message.
func (v *FishVFS) SessionLost(err error) bool {
	return err != nil && errors.Is(err, fishplus.ErrBroken)
}

// SessionKey implements vfs.SessionIdentity. The connection is the identity:
// a clone shares it, which is exactly the property a caller looking for
// everything that died with one session needs.
func (v *FishVFS) SessionKey() any { return v.conn }

// Reconnect rebuilds the session behind this file system and points this view
// at the result. The new shell knows nothing of what the old one was doing:
// background jobs are gone, and a write or a patch that was in flight cannot be
// resumed, only reported. What survives is what lives on this side — the path
// this panel stands in, a read handle, and the credentials the dialer holds.
//
// It is deliberately not called from inside a request. A request that
// reconnected on its own would turn one failure into a delay of unknown length
// in the middle of a copy, with no way for the user to say no; the choice
// belongs to whoever meets ErrBroken and can ask.
//
// Every view of the connection is repointed, not just this one: they all reach
// the session through the connection, so the panel that asked and the panel
// next to it are on the same shell afterwards. What each of them keeps is its
// own current directory, which never depended on the shell that died.
func (v *FishVFS) Reconnect(ctx context.Context) error {
	if v.conn == nil {
		return ErrNoDialer
	}
	_, err := v.conn.reconnect(ctx, v.client())
	return err
}

func (v *FishVFS) GetTitle() string { return v.title }

// SetPanelTitleFormatter customizes only the path rendered in the panel
// border. The canonical POSIX path and the session title remain untouched.
func (v *FishVFS) SetPanelTitleFormatter(formatter func(title, path string) string) {
	v.panelTitleFormatter = formatter
}

func (v *FishVFS) PanelTitle(path string) string {
	if v.panelTitleFormatter == nil {
		return ""
	}
	return v.panelTitleFormatter(v.title, path)
}

// SetPanelInfoProvider attaches transport-specific facts without wrapping the
// filesystem. Keeping *FishVFS as the mounted concrete type is important to
// the Android session pool and to callers that inspect protocol features.
func (v *FishVFS) SetPanelInfoProvider(provider vfs.PanelInfoProvider) {
	v.panelInfoMu.Lock()
	v.panelInfo = provider
	v.panelInfoMu.Unlock()
}

func (v *FishVFS) panelInfoProvider() vfs.PanelInfoProvider {
	v.panelInfoMu.RLock()
	defer v.panelInfoMu.RUnlock()
	return v.panelInfo
}

func (v *FishVFS) PanelInfoKey(req vfs.PanelInfoRequest) string {
	if provider := v.panelInfoProvider(); provider != nil {
		return provider.PanelInfoKey(req)
	}
	return ""
}

func (v *FishVFS) CachedPanelInfo(req vfs.PanelInfoRequest) (vfs.PanelInfoSnapshot, bool) {
	if provider := v.panelInfoProvider(); provider != nil {
		return provider.CachedPanelInfo(req)
	}
	return vfs.PanelInfoSnapshot{}, true
}

func (v *FishVFS) RefreshPanelInfo(ctx context.Context, req vfs.PanelInfoRequest) (vfs.PanelInfoSnapshot, error) {
	if provider := v.panelInfoProvider(); provider != nil {
		return provider.RefreshPanelInfo(ctx, req)
	}
	return vfs.PanelInfoSnapshot{}, nil
}

func (v *FishVFS) IsAtRoot() bool {
	p := v.GetPath()
	return p == "/" || p == ""
}
func (v *FishVFS) GetPath() string {
	v.pathMu.RLock()
	defer v.pathMu.RUnlock()
	return v.path
}
func (v *FishVFS) IsAbs(p string) bool { return path.IsAbs(p) }

func (v *FishVFS) Join(e ...string) string { return path.Join(e...) }
func (v *FishVFS) Base(p string) string    { return path.Base(p) }
func (v *FishVFS) Dir(p string) string     { return path.Dir(p) }

func (v *FishVFS) Abs(p string) (string, error) { return v.abs(p), nil }

func (v *FishVFS) abs(p string) string {
	if p == "" {
		return v.GetPath()
	}
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(v.GetPath(), p)
}

func (v *FishVFS) SetPath(p string) error {
	target := v.abs(p)
	item, err := v.Stat(context.Background(), target)
	if err != nil {
		return err
	}
	if !item.IsDir {
		return os.ErrInvalid
	}
	v.pathMu.Lock()
	v.path = target
	v.pathMu.Unlock()
	return nil
}

// SetPathOptimistic only changes this view's client-side path. A panel calls
// it for a row already known to be a directory, including a cached row, then
// validates that possibly stale knowledge with its asynchronous ReadDir. FISH+
// sends absolute paths with every command and has no server-side cwd, so no
// protocol request is needed here.
func (v *FishVFS) SetPathOptimistic(p string) error {
	target := v.abs(p)
	v.pathMu.Lock()
	v.path = target
	v.pathMu.Unlock()
	return nil
}

// entryToItem converts one remote entry. A symlink keeps its own mode bits;
// TargetIsDir is filled by the find listing backend, by ReadDir's one batched
// query, or by Stat's single target query before conversion.
func (v *FishVFS) entryToItem(e fishplus.Entry) vfs.VFSItem {
	isDir := e.IsDir()
	if e.IsSymlink() && e.TargetIsDir {
		isDir = true
	}
	known := vfs.MetadataExplicit | vfs.MetadataPermissions | vfs.MetadataUID | vfs.MetadataGID | vfs.MetadataHidden | vfs.MetadataExecutable | vfs.MetadataMTime
	if !e.SyntheticTimes {
		known |= vfs.MetadataATime | vfs.MetadataCTime
	}
	return vfs.VFSItem{
		KnownMetadata: known, SizeKnown: true,
		CTime:        e.CTime,
		Name:         e.Name,
		Size:         e.Size,
		IsDir:        isDir,
		MTime:        e.MTime,
		ATime:        e.ATime,
		IsExecutable: e.IsExecutable(),
		IsHidden:     strings.HasPrefix(e.Name, "."),
		IsSymlink:    e.IsSymlink(),
		UnixMode:     e.Mode,
		Uid:          e.Uid,
		Gid:          e.Gid,
	}
}

func (v *FishVFS) ReadDir(ctx context.Context, p string, onChunk func([]vfs.VFSItem)) error {
	dir := v.abs(p)
	entries, err := v.client().Enum(ctx, dir)
	if err != nil {
		return err
	}
	// Validate every basename before joining it into a path sent back to the
	// remote. Then collect all unresolved symlinks for one request. The find
	// backend already marks links to directories, so those need no work.
	filtered := make([]fishplus.Entry, 0, len(entries))
	var linkIndexes []int
	var linkPaths []string
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			continue
		}
		if err := validateFishEntryName(e.Name); err != nil {
			return err
		}
		filtered = append(filtered, e)
		if e.IsSymlink() && !e.TargetIsDir {
			linkIndexes = append(linkIndexes, len(filtered)-1)
			linkPaths = append(linkPaths, path.Join(dir, e.Name))
		}
	}
	if len(linkPaths) != 0 {
		targetDirs, resolveErr := v.client().TargetDirs(ctx, linkPaths)
		if resolveErr == nil {
			for i, isDir := range targetDirs {
				filtered[linkIndexes[i]].TargetIsDir = isDir
			}
		} else if err := ctx.Err(); err != nil {
			return err
		}
		// A broken, missing or inaccessible target did not make the old
		// per-link Stat loop fail the directory listing. Keep that property:
		// unresolved links remain links, just not enterable directories.
	}
	items := make([]vfs.VFSItem, 0, fishReadDirChunk)
	for _, e := range filtered {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		items = append(items, v.entryToItem(e))
		if len(items) >= fishReadDirChunk {
			onChunk(items)
			items = make([]vfs.VFSItem, 0, fishReadDirChunk)
		}
	}
	if len(items) != 0 {
		onChunk(items)
	}
	return nil
}

// validateFishEntryName keeps a remote basename from becoming a local path
// when a generic VFS copy joins it to its destination. Slash cannot occur in a
// real POSIX directory entry. Backslash can occur on Android/Unix, but on a
// Windows host it is a separator and names such as `..\outside` would escape
// the selected local directory. AOSP adb applies the same host-side rule.
func validateFishEntryName(name string) error {
	if name == "" || strings.IndexByte(name, 0) >= 0 || strings.Contains(name, "/") {
		return fmt.Errorf("fishplus: unsafe directory entry name %q", name)
	}
	if runtime.GOOS == "windows" && strings.Contains(name, `\`) {
		return fmt.Errorf("fishplus: unsafe Windows directory entry name %q", name)
	}
	return nil
}

// Stat reports the link itself rather than its target, so the panel can draw
// a symlink as one, and only resolves it to answer the IsDir question.
func (v *FishVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	target := v.abs(p)
	e, err := v.client().Lstat(ctx, target)
	if err != nil {
		return vfs.VFSItem{}, err
	}
	if e.IsSymlink() && !e.TargetIsDir {
		if followed, followErr := v.client().Stat(ctx, target); followErr == nil {
			e.TargetIsDir = followed.IsDir()
		}
	}
	return v.entryToItem(e), nil
}

func (v *FishVFS) Open(ctx context.Context, p string) (vfs.ReadAtCloser, error) {
	return v.client().Open(ctx, v.abs(p))
}

func (v *FishVFS) GetCapabilities() vfs.VFSCapabilities {
	return vfs.VFSCapabilities{
		HasServerSideCopy:  true,
		HasServerSideMove:  true,
		HasRandomAccess:    true,
		HasUnixPermissions: true,
		HasSearch:          v.client().CanGrep(),
	}
}

// fishSearchMax caps how many matches one search brings back. It is there so
// that a pattern matching half a log cannot fill the panel's memory with
// offsets nobody will ever scroll to.
const fishSearchMax = 10000

// Search hands the pattern to the remote host's own grep and returns the byte
// offset of every match. Only the offsets cross the network, which is what
// lets a panel search a log it would take an hour to download. A host without
// the tools answers nil, and the caller falls back to reading the file,
// exactly as it does with SFTP.
func (v *FishVFS) Search(ctx context.Context, p, pattern string) (chan int64, error) {
	if pattern == "" || !v.client().CanGrep() {
		return nil, nil
	}
	// Case is folded because the one caller, the viewer's search, has always
	// folded it, and a search that starts matching differently depending on
	// which panel the file is open in would be worse than a slow one.
	offsets, err := v.client().Grep(ctx, v.abs(p), pattern,
		fishplus.GrepOptions{Fixed: true, IgnoreCase: true, Limit: fishSearchMax})
	if err != nil {
		return nil, err
	}
	// The channel is filled before it is handed over. The session answers one
	// request at a time anyway, so a producing goroutine would buy no overlap
	// and would only add a way for a caller that stops reading to leave it
	// hanging on a send.
	out := make(chan int64, len(offsets))
	for _, off := range offsets {
		out <- off
	}
	close(out)
	return out, nil
}

// FindFiles implements vfs.FileFinder. The remote host walks the tree and,
// when a content pattern is given, greps the candidates in the same pass, so
// what crosses the network is one request and one line per hit.
//
// A symlink is reported as found without resolving it: the alternative is a
// round trip per hit, which would give back what the whole command saves.
func (v *FishVFS) FindFiles(ctx context.Context, dir string, q vfs.FindQuery) ([]vfs.FoundEntry, error) {
	// The ffind wire command can grep fixed strings or regular expressions,
	// but it cannot express the richer local semantics without changing the
	// helper protocol (folders, symlink leaves, whole words, and inverted
	// matches). Let the caller fall back to the normal VFS walk for those
	// queries instead of silently returning a different result set.
	if q.WholeWords || q.NotContaining || q.FindFolders || q.FindSymlinks {
		return nil, vfs.ErrFindOptionsUnsupported
	}
	opts := fishplus.FindOptions{
		Masks:      q.Masks,
		Text:       q.Text,
		Fixed:      !q.Regex,
		IgnoreCase: q.IgnoreCase,
		Limit:      q.Limit,
	}
	// The client's Progress speaks fishplus.FindProgress; the caller
	// asked for vfs.FindProgress. One field-for-field bridge, so a
	// panel dialog that plumbs a progress callback through FindQuery
	// gets P lines from a Windows peer without knowing anything about
	// job protocols.
	if q.Progress != nil {
		opts.Progress = func(p fishplus.FindProgress) {
			q.Progress(vfs.FindProgress{
				Scanned: p.Scanned,
				Found:   p.Found,
				Path:    p.Path,
			})
		}
	}
	entries, err := v.client().Find(ctx, v.abs(dir), opts)
	if err != nil {
		return nil, err
	}
	out := make([]vfs.FoundEntry, 0, len(entries))
	for _, e := range entries {
		item := v.entryToItem(e)
		item.Name = path.Base(e.Name)
		item.IsHidden = strings.HasPrefix(item.Name, ".")
		out = append(out, vfs.FoundEntry{Path: e.Name, Item: item})
	}
	return out, nil
}

// PtyChangeDirCommand implements vfs.PtyShellIntegration. The panel's
// PTY channel runs the peer's DefaultShell — cmd.exe on a stock
// OpenSSH-Server-Windows host, whatever the login shell is on a POSIX
// host. Send syntax the actual PTY shell understands, converting the
// path from the wire's Cygwin shape (/c/Users/foo) to what the shell
// wants (C:\Users\foo for cmd, /c/Users/foo unchanged for POSIX).
//
// f4_sync is a stray marker helper.sh's own log filter can strip: cmd
// keeps it as a rem comment, POSIX carries it as "&& true f4_sync" —
// not a "#" comment, because stock zsh ships with interactive_comments
// off and would feed the tail to cd as extra arguments.
func (v *FishVFS) PtyChangeDirCommand(dir string) []byte {
	if v.peerIsWindows() {
		winDir := fishplus.WirePathToWindows(dir)
		if winDir == "" {
			return nil
		}
		return []byte(fmt.Sprintf("cd /d \"%s\" & rem f4_sync\r", winDir))
	}
	sq := strings.ReplaceAll(dir, "'", "'\\''")
	return []byte(fmt.Sprintf(" cd '%s' && true f4_sync\r", sq))
}

// PtyRunCommand implements vfs.PtyShellIntegration. Same shell-flavor
// split as PtyChangeDirCommand: cmd wants "cd /d \"path\" & command",
// POSIX wants "cd 'path' && command". An empty dir skips the cd.
func (v *FishVFS) PtyRunCommand(dir, command string) []byte {
	if command == "" {
		return nil
	}
	if v.peerIsWindows() {
		if dir == "" {
			return []byte(fmt.Sprintf("%s\r", command))
		}
		winDir := fishplus.WirePathToWindows(dir)
		if winDir == "" {
			return []byte(fmt.Sprintf("%s\r", command))
		}
		return []byte(fmt.Sprintf("cd /d \"%s\" & %s\r", winDir, command))
	}
	if dir == "" {
		return []byte(fmt.Sprintf(" %s\r", command))
	}
	sq := strings.ReplaceAll(dir, "'", "'\\''")
	return []byte(fmt.Sprintf(" cd '%s' && %s\r", sq, command))
}

// PtyInterrupt implements vfs.PtyShellIntegration. cmd.exe over ConPTY
// and every POSIX shell treat a raw ETX (0x03) as SIGINT / Ctrl+C; no
// flavor-specific wrapping is needed.
func (v *FishVFS) PtyInterrupt() []byte {
	return []byte{0x03}
}

// PtyInitSequence implements vfs.PtyShellIntegration. On a Windows peer
// it sets cmd's PROMPT so that every prompt cmd draws — every time a
// command finishes — embeds an OSC 133 D marker. The ANSI parser in
// terminal_view.go then fires OnBusyChange(false), which is what makes
// panels_frame return to panels after a cmdline command completes. This
// is the same trick panels_frame.go uses for the local Windows PTY
// (see the os.Setenv("PROMPT", ...) around initPTY).
//
// $E is cmd's PROMPT syntax for ESC; $E\ is the OSC ST terminator
// (\x1b\); $P and $G are the standard current-path-and-'>' pieces. The
// leading @ suppresses cmd's echo of the prompt-setup line in batch
// contexts; interactive cmd on ConPTY still echoes it, so the second
// half of the line is a cls that wipes the cmd banner + copyright +
// the echoed setup line, leaving the panel-terminal view starting on
// the first marker-embedded prompt with no visible init noise.
func (v *FishVFS) PtyInitSequence() []byte {
	if !v.peerIsWindows() {
		return nil
	}
	return []byte("@prompt $E]133;D$E\\$P$G & cls\r")
}

// peerIsWindows reports whether the FISH+ helper on the peer announced
// itself as PowerShell (flavor:pwsh feature). A Windows peer's PTY
// channel is what needs cmd-shaped commands; a POSIX peer keeps the
// original bash-style templates.
func (v *FishVFS) peerIsWindows() bool {
	c := v.client()
	if c == nil {
		return false
	}
	s := c.Session()
	if s == nil {
		return false
	}
	for _, n := range s.Features().Names() {
		if n == "flavor:pwsh" {
			return true
		}
	}
	return false
}

// opStatsFromScan converts what the remote walk counted. PhysicalBytes stays
// zero: the remote host reports apparent sizes, and a consumer that sees a
// zero there hides the metric rather than showing a wrong one.
func opStatsFromScan(s fishplus.ScanStats) vfs.OpStats {
	return vfs.OpStats{Bytes: s.Bytes, DirBytes: s.DirBytes, Files: s.Files, Dirs: s.Dirs}
}

// Scan implements vfs.FastScanner. Counting a tree is one remote job instead
// of a listing round trip per directory, which is the difference between a
// directory size dialog that answers and one the user gives up on.
//
// Anything the remote host cannot do falls back to the generic walk, because
// the caller takes this answer as final: vfs.CalculateStats does not retry a
// FastScanner that failed.
func (v *FishVFS) Scan(ctx context.Context, basePath string, names []string, cb vfs.ScanCallback) (vfs.OpStats, error) {
	if !v.client().CanScan() {
		return vfs.GenericScan(ctx, v, basePath, names, cb)
	}
	var total vfs.OpStats
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		full := v.abs(path.Join(basePath, name))
		item, err := v.Stat(ctx, full)
		if err != nil {
			return total, err
		}
		if !item.IsDir {
			total.Files++
			total.Bytes += item.Size
			if cb != nil {
				cb(full, total)
			}
			continue
		}
		// The remote walk counts one tree at a time and starts from zero, so
		// what the caller sees has to be added to what the earlier names
		// already contributed.
		base := total
		stats, err := v.client().Scan(ctx, full, func(p fishplus.ScanProgress) {
			if cb == nil {
				return
			}
			running := base
			running.Add(opStatsFromScan(p.ScanStats))
			cb(p.Path, running)
		})
		if err != nil {
			if ctx.Err() != nil {
				return total, err
			}
			vtui.DebugLog("NET: remote scan unavailable, walking instead: %v", err)
			return vfs.GenericScan(ctx, v, basePath, names, cb)
		}
		total.Add(opStatsFromScan(stats))
		if cb != nil {
			cb(full, total)
		}
	}
	return total, nil
}

// RunCommand implements vfs.CommandRunner. The command runs as a job, so it
// can read stdin, print for an hour or never end without any of that
// reaching the request stream the panel is using.
func (v *FishVFS) RunCommand(ctx context.Context, dir, command string, cb func(line string)) (int, error) {
	if !v.CommandRunnerAvailable() {
		return 0, fishplus.ErrNoJobs
	}
	return v.client().Run(ctx, v.abs(dir), command, cb)
}

func (v *FishVFS) CommandRunnerAvailable() bool {
	client := v.client()
	return client != nil && client.CanRun()
}

// CommandRunnerInfo reports the remote shell syntax and the transport's real
// concurrency limit. A FISH+ connection serializes protocol requests, so
// advertising more workers would only reorder queued requests locally.
func (v *FishVFS) CommandRunnerInfo() vfs.CommandRunnerInfo {
	dialect := vfs.CommandDialectPOSIX
	if v != nil && v.peerIsWindows() {
		dialect = vfs.CommandDialectCmd
	}
	return vfs.CommandRunnerInfo{Dialect: dialect, MaxParallel: 1}
}

// FindDuplicates implements vfs.DuplicateFinder. Only the paths of the files
// that turned out to be identical cross the network; the reading and the
// hashing happen where the disk is.
func (v *FishVFS) FindDuplicates(ctx context.Context, dir string, cb func(vfs.DuplicateProgress)) ([][]string, error) {
	if !v.client().CanHash() {
		return nil, fishplus.ErrNoJobs
	}
	var forward func(fishplus.HashProgress)
	if cb != nil {
		forward = func(p fishplus.HashProgress) {
			cb(vfs.DuplicateProgress{Done: p.Done, Total: p.Total, Path: p.Path})
		}
	}
	return v.client().Duplicates(ctx, v.abs(dir), forward)
}

// PatchFile implements vfs.DeltaWriter. The copying happens on the remote
// host at local disk speed; only the new bytes cross the network.
func (v *FishVFS) PatchFile(ctx context.Context, src, dst string, pieces []vfs.PatchPiece) error {
	if !v.client().CanPatch() {
		return fishplus.ErrNoWrite
	}
	segs := make([]fishplus.PatchSegment, 0, len(pieces))
	for _, p := range pieces {
		if p.Data == nil {
			segs = append(segs, fishplus.Copy(p.Offset, p.Length))
			continue
		}
		segs = append(segs, fishplus.Literal(p.Data))
	}
	return v.client().Patch(ctx, v.abs(src), v.abs(dst), segs)
}

// LineIndex implements vfs.LineIndexer. A count of zero asks for nothing but
// the total, which is one remote pass and three numbers on the wire.
func (v *FishVFS) LineIndex(ctx context.Context, p string, first, count int64) (vfs.LineIndexResult, error) {
	idx, err := v.client().Lines(ctx, v.abs(p), first, count)
	if err != nil {
		return vfs.LineIndexResult{}, err
	}
	return vfs.LineIndexResult{First: idx.First, Offsets: idx.Offsets, Total: idx.Total}, nil
}
func (v *FishVFS) MkDir(ctx context.Context, p string) error {
	return v.client().MkDir(ctx, v.abs(p))
}

// Remove deletes whatever is at the path. A directory is removed with
// everything below it by the remote host itself, in one round trip instead
// of one per entry, which is the main reason a shell based file system is
// worth having at all.
func (v *FishVFS) Remove(ctx context.Context, p string) error {
	target := v.abs(p)
	e, err := v.client().Lstat(ctx, target)
	if err != nil {
		return err
	}
	if e.IsDir() {
		return v.client().RemoveAll(ctx, target)
	}
	return v.client().Remove(ctx, target)
}

func (v *FishVFS) Rename(ctx context.Context, o, n string) error {
	return v.client().Rename(ctx, v.abs(o), v.abs(n))
}

// Copy implements vfs.ServerSideCopier.
func (v *FishVFS) Copy(ctx context.Context, o, n string) error {
	return v.client().Copy(ctx, v.abs(o), v.abs(n))
}

// Create truncates the file, or creates it, and hands back a handle that
// streams from the beginning. The handle buffers up to one transfer chunk,
// so the copier's small writes do not each become a round trip.
func (v *FishVFS) Create(ctx context.Context, p string) (io.WriteCloser, error) {
	w, err := v.client().Create(ctx, v.abs(p))
	if err != nil {
		return nil, err
	}
	return w, nil
}

// SetAttributes applies the permission bits, then the ownership, then the
// timestamps. A Uid or Gid below zero means "leave that half alone", which
// is what the copier passes; a zero timestamp is filled in from the other
// one, the same way the SFTP backend does it, so a file copied onto a FISH+
// panel keeps the times it had.
func (v *FishVFS) SetAttributes(ctx context.Context, p string, item vfs.VFSItem) error {
	target := v.abs(p)
	if item.UnixMode != 0 {
		if err := v.client().Chmod(ctx, target, item.UnixMode); err != nil {
			return err
		}
	}
	if item.Uid >= 0 || item.Gid >= 0 {
		if err := v.client().Chown(ctx, target, item.Uid, item.Gid); err != nil {
			return err
		}
	}
	mtime, atime := item.MTime, item.ATime
	if mtime.IsZero() {
		mtime = atime
	}
	if atime.IsZero() {
		atime = mtime
	}
	if mtime.IsZero() || atime.IsZero() {
		return nil
	}
	return v.client().Chtimes(ctx, target, mtime, atime)
}

func (v *FishVFS) ParentVFS() vfs.VFS { return v.parent }

// Clone returns a second view of the same session, with its own current
// directory. A second login would cost another authentication and another
// password prompt, and would buy nothing: the remote shell answers one
// command at a time either way.
func (v *FishVFS) Clone() vfs.VFS {
	return v.CloneForParent(v.parent)
}

// CloneForParent returns another view of the same live session attached to a
// different mount parent. Session pools use it to retain a parentless anchor
// while every panel still gets the manager instance it was opened from.
func (v *FishVFS) CloneForParent(parent vfs.VFS) *FishVFS {
	if v.conn != nil {
		v.conn.retain()
	}
	return &FishVFS{
		parent:              parent,
		conn:                v.conn,
		path:                v.GetPath(),
		title:               v.title,
		panelTitleFormatter: v.panelTitleFormatter,
		panelInfo:           v.panelInfoProvider(),
		host:                v.host,
		port:                v.port,
		user:                v.user,
	}
}

var (
	_ vfs.PanelInfoProvider                 = (*FishVFS)(nil)
	_ vfs.CommandRunner                     = (*FishVFS)(nil)
	_ vfs.CommandRunnerInfoProvider         = (*FishVFS)(nil)
	_ vfs.CommandRunnerAvailabilityProvider = (*FishVFS)(nil)
)

// Close releases this view. The session itself goes away with its last
// user, and closing the same view twice is harmless: a panel may well be
// closed by both its own teardown and the frame that owned it.
func (v *FishVFS) Close() error {
	if v == nil {
		return nil
	}
	var err error
	v.once.Do(func() {
		if v.conn != nil {
			err = v.conn.release()
		}
	})
	return err
}

// fishTypeMatches reports whether a site configuration asks for FISH+. The
// plus is part of the name because the protocol is more than the classic
// fish, but a configuration that spells it without one is accepted too.
func fishTypeMatches(t string) bool {
	return t == "fish+" || t == "fish"
}

// netFoxConfigAt reads the site configuration a connection entry stands for.
func netFoxConfigAt(ctx context.Context, parent vfs.VFS, pth string) (NetFoxConfig, bool) {
	w, ok := parent.(*netFoxVFSWrapper)
	if !ok {
		return NetFoxConfig{}, false
	}
	item, err := w.Stat(ctx, pth)
	if err != nil || item.IsDir {
		return NetFoxConfig{}, false
	}
	f, err := w.Open(ctx, pth)
	if err != nil {
		return NetFoxConfig{}, false
	}
	defer func() { _ = f.Close() }() // The configuration file is read-only.
	var cfg NetFoxConfig
	if err := json.NewDecoder(ctxReader{f, ctx}).Decode(&cfg); err != nil {
		return NetFoxConfig{}, false
	}
	return cfg, true
}

type fishProvider struct{}

func (p *fishProvider) Name() string  { return "NetFox-FISH+" }
func (p *fishProvider) Priority() int { return 100 }

func (p *fishProvider) CanOpen(ctx context.Context, parent vfs.VFS, pth string) bool {
	cfg, ok := netFoxConfigAt(ctx, parent, pth)
	return ok && fishTypeMatches(cfg.Type)
}

func (p *fishProvider) Open(ctx context.Context, parent vfs.VFS, pth string) (vfs.VFS, error) {
	cfg, ok := netFoxConfigAt(ctx, parent, pth)
	if !ok {
		return nil, os.ErrInvalid
	}
	port := cfg.Port
	if port == "" {
		port = "22"
	}
	timeout := 15
	if cfg.Timeout != "" {
		if t, err := strconv.Atoi(cfg.Timeout); err == nil && t > 0 {
			timeout = t
		}
	}
	res, err := NewFishVFS(parent, cfg.Host, port, cfg.User, cfg.Pass, cfg.KeyPath, timeout, cfg.Proxy())
	if err != nil {
		return nil, err
	}
	return res, nil
}

type fishProtocolHandler struct{}

func (ph *fishProtocolHandler) Prefix() string      { return "fish+" }
func (ph *fishProtocolHandler) DefaultPort() string { return "22" }
func (ph *fishProtocolHandler) BuildExtraUI(cfg *NetFoxConfig, x, y, w, h int) (vtui.UIElement, func()) {
	return nil, func() {}
}

func init() {
	vfs.RegisterProvider(&fishProvider{})
	RegisterProtocol(&fishProtocolHandler{})
}
