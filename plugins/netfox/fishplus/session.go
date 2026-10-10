package fishplus

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MaxLineLen caps a single protocol line. Payload lines are produced by
// remote tools (ls, stat, grep), so they are bounded by path and match
// lengths; anything bigger means the stream went out of sync.
const MaxLineLen = 1 << 20

// MaxFrameLen caps a single binary frame. The client never asks for more
// than this, so a bigger frame means either a confused helper or a hostile
// host trying to make the panel allocate a disk worth of memory.
const MaxFrameLen = 64 << 20

// ErrBroken is returned once a session lost synchronization with the remote
// helper. Such a session cannot be repaired, the caller has to reconnect.
var ErrBroken = errors.New("fishplus: session is out of sync")

// RemoteError carries a failure reported by the remote helper itself.
type RemoteError struct {
	Cmd string
	Msg string
}

func (e *RemoteError) Error() string {
	if e.Cmd == "" {
		return "fishplus: remote error: " + e.Msg
	}
	return "fishplus: " + e.Cmd + ": " + e.Msg
}

// Features describes what the remote host is capable of, as detected by the
// helper script during startup.
type Features struct {
	Proto int
	Raw   string
	names map[string]bool
}

// Has reports whether the remote helper announced the named feature.
func (f Features) Has(name string) bool { return f.names[name] }

// Names returns the announced feature names in a stable order.
func (f Features) Names() []string {
	out := make([]string, 0, len(f.names))
	for name := range f.names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ListingMode returns the metadata backend the helper picked for itself,
// announced as "mode:<name>" among the features.
func (f Features) ListingMode() string {
	for name := range f.names {
		if strings.HasPrefix(name, "mode:") {
			return strings.TrimPrefix(name, "mode:")
		}
	}
	return ""
}

// Response is the outcome of a single request.
type Response struct {
	// Lines holds the textual payload, newlines stripped.
	Lines []string
	// Data holds the concatenated binary payload of a data request.
	Data []byte
	// Status is either "ok" or "err".
	Status string
	// Msg is the optional message that follows the status.
	Msg string
}

// BootstrapMethod selects how the embedded helper reaches the remote shell.
// The zero value deliberately preserves the original streaming bootstrap.
type BootstrapMethod uint8

const (
	// BootstrapScriptLines uploads the compact helper as newline-delimited
	// source after a small bootstrap has taken control of the shell input.
	BootstrapScriptLines BootstrapMethod = iota
	// BootstrapBase64Line sends one printable-ASCII line containing the
	// complete base64-encoded helper. It avoids per-line shell read overhead.
	BootstrapBase64Line
	// BootstrapBase64LinePwsh is the PowerShell counterpart of
	// BootstrapBase64Line: it fits the compacted helper.ps1 into one
	// printable-ASCII line and drives a PowerShell peer through the same
	// wire protocol. Selected explicitly by callers today; flavor auto-probe
	// will pick it based on the peer's response to a probe line.
	BootstrapBase64LinePwsh
	// BootstrapNative talks to an f4 that runs as the server (Server, started
	// as the remote command) instead of a shell: no helper is uploaded, the
	// client just says hello with the session token and waits for the banner.
	BootstrapNative
)

// HandshakeOptions controls helper upload. Callers that do not need a
// specialized transport should keep using Handshake, whose behavior is
// unchanged.
type HandshakeOptions struct {
	Bootstrap BootstrapMethod
}

// OK reports whether the remote helper completed the request successfully.
func (r *Response) OK() bool { return r.Status == "ok" }

// Err converts a failed response into an error, cmd is used for context.
func (r *Response) Err(cmd string) error {
	if r.OK() {
		return nil
	}
	return &RemoteError{Cmd: cmd, Msg: r.Msg}
}

// Session speaks the FISH+ protocol over a duplex byte stream, typically the
// stdin/stdout pair of a remote shell started through ssh. All requests are
// serialized: the protocol is strictly request/response over one stream.
type Session struct {
	mu          sync.Mutex
	featuresMu  sync.RWMutex
	requestGate chan struct{}
	closeCh     chan struct{}
	w           io.Writer
	r           *bufio.Reader
	closer      io.Closer
	token       string
	seq         uint64
	feats       Features
	broken      atomic.Bool
	lastUse     time.Time
	closing     atomic.Bool
	closeOnce   sync.Once
	closeErr    error
	ioMu        sync.Mutex
}

// NewSession wires a session to the remote shell's stdin and stdout. closer
// may be nil; when set it must be safe to close locally and is closed by
// Close, which also makes the remote helper exit because its stdin hits EOF.
func NewSession(stdin io.Writer, stdout io.Reader, closer io.Closer) *Session {
	s := &Session{
		requestGate: make(chan struct{}, 1),
		closeCh:     make(chan struct{}),
		w:           stdin,
		r:           bufio.NewReaderSize(stdout, 64*1024),
		closer:      closer,
		token:       newToken(),
		lastUse:     time.Now(),
	}
	s.requestGate <- struct{}{}
	return s
}

// acquireRequest waits for the strictly serial protocol stream without
// joining an uncancellable sync.Mutex queue. Superseded panel loads cancel
// their contexts while another response is still being drained; they must
// disappear here so the newest live request is the only waiter left.
func (s *Session) acquireRequest(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.closeCh:
		return ErrBroken
	case <-s.requestGate:
	}
	if err := ctx.Err(); err != nil {
		s.releaseRequest()
		return err
	}
	if s.closing.Load() {
		s.releaseRequest()
		return ErrBroken
	}
	return nil
}

func (s *Session) tryAcquireRequest() bool {
	select {
	case <-s.requestGate:
		return true
	default:
		return false
	}
}

func (s *Session) releaseRequest() { s.requestGate <- struct{}{} }

func newToken() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Rather than failing the connection, fall back to a fixed token:
		// it only protects against accidental collisions with payload.
		return "f4f1shplusf4f1"
	}
	return hex.EncodeToString(buf[:])
}

// Token returns the random terminator token of this session.
func (s *Session) Token() string { return s.token }

// Features returns what the remote helper announced during the handshake.
func (s *Session) Features() Features {
	// Features have their own lock because GetCapabilities is often called
	// from the UI thread and must not wait for an unrelated remote request that
	// currently owns the protocol mutex.
	s.featuresMu.RLock()
	defer s.featuresMu.RUnlock()
	return s.feats
}

// Broken reports whether the session lost synchronization.
func (s *Session) Broken() bool {
	if s.closing.Load() {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.broken.Load()
}

// IdleFor is how long it has been since the session last carried a request.
// It is what tells a session nobody is using from one that is merely between
// two chunks of a copy, which is the only distinction a keepalive needs.
func (s *Session) IdleFor() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastUse)
}

// Handshake uploads the helper script and waits for its banner. Everything
// the remote shell printed before the banner (motd, shell warnings) is
// discarded.
func (s *Session) Handshake(ctx context.Context) error {
	return s.HandshakeWithOptions(ctx, HandshakeOptions{})
}

// HandshakeWithOptions uploads the helper using the requested bootstrap and
// waits for its banner. BootstrapBase64Line is useful for Android and other
// shells where feeding the helper through read one line at a time is costly.
func (s *Session) HandshakeWithOptions(ctx context.Context, opts HandshakeOptions) error {
	if err := s.acquireRequest(ctx); err != nil {
		return err
	}
	defer s.releaseRequest()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() || s.broken.Load() {
		return ErrBroken
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch opts.Bootstrap {
	case BootstrapScriptLines:
		if _, err := io.WriteString(s.w, BootstrapLine(s.token)); err != nil {
			s.broken.Store(true)
			return err
		}
		if err := s.waitForReady(ctx); err != nil {
			return err
		}
		if _, err := io.WriteString(s.w, HelperScript(s.token)+HelperEndMarker+"\n"); err != nil {
			s.broken.Store(true)
			return err
		}
	case BootstrapBase64Line:
		if _, err := io.WriteString(s.w, Base64BootstrapLine(s.token)); err != nil {
			s.broken.Store(true)
			return err
		}
		if err := s.waitForReady(ctx); err != nil {
			return err
		}
	case BootstrapBase64LinePwsh:
		if _, err := io.WriteString(s.w, Base64BootstrapLinePwsh(s.token)); err != nil {
			s.broken.Store(true)
			return err
		}
		if err := s.waitForReady(ctx); err != nil {
			return err
		}
	case BootstrapNative:
		if _, err := io.WriteString(s.w, NativeHelloLine(s.token)); err != nil {
			s.broken.Store(true)
			return err
		}
	default:
		return fmt.Errorf("fishplus: unsupported bootstrap method %d", opts.Bootstrap)
	}
	resp, err := s.readResponse(ctx, 0, false)
	if err != nil {
		return err
	}
	if !resp.OK() {
		s.broken.Store(true)
		return &RemoteError{Cmd: "handshake", Msg: resp.Msg}
	}
	feats, err := parseBanner(resp.Msg)
	if err != nil {
		s.broken.Store(true)
		return err
	}
	if feats.Proto != ProtocolVersion {
		s.broken.Store(true)
		return fmt.Errorf("fishplus: remote speaks protocol %d, expected %d", feats.Proto, ProtocolVersion)
	}
	s.featuresMu.Lock()
	s.feats = feats
	s.featuresMu.Unlock()
	return nil
}

func parseBanner(msg string) (Features, error) {
	fields := strings.Fields(msg)
	if len(fields) < 2 || fields[0] != "FISHPLUS" {
		return Features{}, fmt.Errorf("fishplus: unexpected banner %q", msg)
	}
	proto, err := strconv.Atoi(fields[1])
	if err != nil {
		return Features{}, fmt.Errorf("fishplus: unexpected protocol version %q", fields[1])
	}
	feats := Features{
		Proto: proto,
		Raw:   strings.Join(fields[2:], " "),
		names: make(map[string]bool, len(fields)),
	}
	for _, name := range fields[2:] {
		feats.names[name] = true
	}
	return feats, nil
}

// ParseBannerForTest exports banner parsing for testing.
func ParseBannerForTest(msg string) (Features, error) {
	return parseBanner(msg)
}

// SetFeaturesForTest sets session features for testing.
func (s *Session) SetFeaturesForTest(f Features) {
	s.featuresMu.Lock()
	s.feats = f
	s.featuresMu.Unlock()
}

// maxBootstrapLines bounds how much login noise is skipped while waiting
// for the shell to report itself ready. A motd is long; it is not endless.
const maxBootstrapLines = 1000

// ReadyTimeout bounds how long the bootstrap may stay silent before the peer
// is declared unable to run it. A shell that does not understand the
// bootstrap does not answer at all: a PowerShell peer given the POSIX line
// prints its parse error on stderr, which the transport discards, and then
// waits for input forever. Without a bound the handshake would block there
// for good, and a caller with a fallback flavor would never get the failure
// it needs to try the other one.
//
// It bounds silence rather than the whole wait, so a long motd arriving one
// slow line at a time still gets through.
var ReadyTimeout = 20 * time.Second

// waitForReady consumes whatever the login printed until the bootstrap says
// it is running. Sending the helper before that is what the whole two step
// upload exists to avoid.
func (s *Session) waitForReady(ctx context.Context) error {
	marker := ReadyMarker(s.token)
	for i := 0; i < maxBootstrapLines; i++ {
		if err := ctx.Err(); err != nil {
			s.broken.Store(true)
			return err
		}
		line, err := s.readLineWithin(ctx, ReadyTimeout)
		if err != nil {
			s.broken.Store(true)
			return err
		}
		if strings.Contains(line, marker) {
			return nil
		}
	}
	s.broken.Store(true)
	return fmt.Errorf("fishplus: the remote shell never reported being ready")
}

// readLineWithin reads one line, giving up once the peer has been silent for
// d or the context is done. The read itself cannot be interrupted — it is a
// blocking read on a network stream — so it is left to finish in a goroutine
// on the line it owns; the session is on its way out either way, since every
// caller of this treats a failure here as fatal to the session.
//
// The timeout message is the one isHandshakeFailure already recognizes, so a
// peer that stays silent reads as "this flavor is wrong" rather than as a
// broken network.
func (s *Session) readLineWithin(ctx context.Context, d time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := s.readLine()
		ch <- result{line, err}
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.line, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", fmt.Errorf("fishplus: the remote shell never reported being ready within %s", d)
	}
}

// Exec runs a command that takes only short tokens as arguments. A token
// must be non-empty and free of whitespace; anything path shaped belongs in
// ExecPath instead.
func (s *Session) Exec(ctx context.Context, cmd string, args ...string) (*Response, error) {
	return s.exec(ctx, false, cmd, args, nil)
}

// ExecPath runs a command that operates on a path. The path travels on a
// line of its own, verbatim whenever the channel can carry it: only a path
// containing a newline (or starting with the escape marker) is base64
// encoded. Staying out of base64 keeps a fork per request off the remote
// host and keeps the traffic readable in a protocol log.
func (s *Session) ExecPath(ctx context.Context, cmd, path string, args ...string) (*Response, error) {
	return s.exec(ctx, false, cmd, args, []string{path})
}

// ExecPaths runs a command that operates on more than one path, each on a
// line of its own and in the order given. Rename is the first such command.
func (s *Session) ExecPaths(ctx context.Context, cmd string, paths []string, args ...string) (*Response, error) {
	return s.exec(ctx, false, cmd, args, paths)
}

// ExecPathsLines delivers text payload lines while the response is arriving.
// onLine executes under the session's request lock: it must not issue another
// request on this session or block on a consumer. Payload is not accumulated
// in Response.Lines. Existing cancellation/draining and framing stay shared.
func (s *Session) ExecPathsLines(ctx context.Context, cmd string, paths []string, onLine func(string), args ...string) (*Response, error) {
	return s.execFullLines(ctx, false, cmd, args, paths, nil, false, nil, onLine)
}

// ExecData and ExecPathData behave like Exec and ExecPath but also accept
// binary frames: a line "#<n>" followed by exactly n raw bytes.
func (s *Session) ExecData(ctx context.Context, cmd string, args ...string) (*Response, error) {
	return s.exec(ctx, true, cmd, args, nil)
}

func (s *Session) ExecPathData(ctx context.Context, cmd, path string, args ...string) (*Response, error) {
	return s.exec(ctx, true, cmd, args, []string{path})
}

// EncodePathLine renders a path as one protocol line, escaping it only when
// a raw line would not survive the round trip.
func EncodePathLine(p string) string {
	if p == "" || strings.HasPrefix(p, "~") || strings.ContainsAny(p, "\r\n") {
		return "~" + base64.StdEncoding.EncodeToString([]byte(p))
	}
	return p
}

// ExecPayload runs a command that carries a payload of its own after the
// path lines. A raw payload is exactly the announced number of bytes with
// nothing around it; an encoded one is a single base64 line, which the
// remote helper can consume with the shell alone and which therefore stays
// exact on hosts whose dd cannot stop on a byte boundary.
func (s *Session) ExecPayload(ctx context.Context, cmd string, paths, args []string, payload []byte, encoded bool) (*Response, error) {
	return s.execFull(ctx, false, cmd, args, paths, payload, encoded, nil)
}

// ExecStream runs a command whose request body the caller writes itself,
// after the path lines and before the answer is read. A command whose
// payload is interleaved with descriptions of it cannot be handed over as
// one slice, which is what patch needs.
func (s *Session) ExecStream(ctx context.Context, cmd string, paths, args []string, body func(w io.Writer) error) (*Response, error) {
	return s.execFull(ctx, false, cmd, args, paths, nil, false, body)
}

// MarkBroken poisons the session after the caller found out, by means the
// session itself cannot see, that the two sides disagree about how much of
// the stream has been consumed.
func (s *Session) MarkBroken() {
	if s.closing.Load() {
		return
	}
	s.mu.Lock()
	s.broken.Store(true)
	s.mu.Unlock()
}

func (s *Session) exec(ctx context.Context, binary bool, cmd string, args, paths []string) (*Response, error) {
	return s.execFull(ctx, binary, cmd, args, paths, nil, false, nil)
}

func (s *Session) execFull(ctx context.Context, binary bool, cmd string, args, paths []string, payload []byte, encoded bool, body func(w io.Writer) error) (*Response, error) {
	return s.execFullLines(ctx, binary, cmd, args, paths, payload, encoded, body, nil)
}

func (s *Session) execFullLines(ctx context.Context, binary bool, cmd string, args, paths []string, payload []byte, encoded bool, body func(w io.Writer) error, onLine func(string)) (*Response, error) {

	if cmd == "" || strings.ContainsAny(cmd, " \t\r\n") {
		return nil, fmt.Errorf("fishplus: invalid command %q", cmd)
	}
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\r\n") {
			return nil, fmt.Errorf("fishplus: invalid argument %q for command %q", arg, cmd)
		}
	}
	if err := s.acquireRequest(ctx); err != nil {
		return nil, err
	}
	defer s.releaseRequest()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() || s.broken.Load() {
		return nil, ErrBroken
	}
	// The idle clock is refreshed when the request is done rather than when it
	// started, under the lock the request already holds: a read that took ten
	// minutes was ten minutes of activity, not ten minutes of silence.
	defer func() { s.lastUse = time.Now() }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.seq++
	id := s.seq
	var req strings.Builder
	req.WriteString(strconv.FormatUint(id, 10))
	req.WriteByte(' ')
	req.WriteString(cmd)
	for _, arg := range args {
		req.WriteByte(' ')
		req.WriteString(arg)
	}
	req.WriteByte('\n')
	for _, p := range paths {
		req.WriteString(EncodePathLine(p))
		req.WriteByte('\n')
	}
	if encoded {
		// The line is written even for an empty payload: the helper reads
		// one line per encoded request and would otherwise wait for a line
		// that never comes.
		req.WriteString(base64.StdEncoding.EncodeToString(payload))
		req.WriteByte('\n')
	}
	if _, err := s.writeCtx(ctx, []byte(req.String())); err != nil {
		s.broken.Store(true)
		return nil, err
	}
	if !encoded && len(payload) > 0 {
		// The raw payload carries no terminator of its own: the remote
		// helper reads exactly as many bytes as the request announced, so
		// a stray newline here would end up at the head of the next
		// request.
		if _, err := s.writeCtx(ctx, payload); err != nil {
			s.broken.Store(true)
			return nil, err
		}
	}
	if body != nil {
		// A body that stops halfway leaves the remote host waiting for
		// bytes that will never come, so there is no recovering the stream.
		// Route the body through the session writer so a frozen transport
		// cannot hang the patch and cancellation is honoured.
		if err := body(&sessionWriter{s: s, ctx: ctx}); err != nil {
			s.broken.Store(true)
			return nil, err
		}
	}
	return s.readResponseLines(ctx, id, binary, onLine)
}

func (s *Session) readResponse(ctx context.Context, id uint64, binary bool) (*Response, error) {
	return s.readResponseLines(ctx, id, binary, nil)
}

func (s *Session) readResponseLines(ctx context.Context, id uint64, binary bool, onLine func(string)) (*Response, error) {

	prefix := "." + s.token + " " + strconv.FormatUint(id, 10) + " "
	resp := &Response{}
	for {
		if err := ctx.Err(); err != nil {
			// The response is only half-read. Reading forward to the
			// terminator puts the stream back where the next request
			// expects it, which costs the rest of one answer and saves a
			// whole reconnect.
			if drainErr := s.drainToTerminator(ctx, prefix, binary); drainErr != nil {
				s.broken.Store(true)
			}
			return nil, err
		}
		line, err := s.readLineCtx(ctx)
		if err != nil {
			s.broken.Store(true)
			return nil, err
		}
		// The request may have been cancelled while this line was in flight.
		// Abandon the rest of the answer (draining to the terminator keeps
		// the stream at a request boundary) and report the cancellation; the
		// session survives and can be reused for the next request.
		if ctx.Err() != nil {
			if !strings.HasPrefix(line, prefix) {
				if derr := s.drainToTerminator(ctx, prefix, binary); derr != nil {
					s.broken.Store(true)
				}
			}
			return nil, ctx.Err()
		}
		// The handshake is the one place where the terminator may not start
		// its line: a motd, a shell warning or the echo of the uploaded
		// script on a pseudo terminal can end without a newline and glue
		// itself to the banner. Later responses are strict, the helper
		// controls every byte by then.
		if id == 0 {
			if at := strings.Index(line, prefix); at > 0 {
				line = line[at:]
			}
		}
		if strings.HasPrefix(line, prefix) {
			status, msg, _ := strings.Cut(strings.TrimSpace(line[len(prefix):]), " ")
			if status != "ok" && status != "err" {
				s.broken.Store(true)
				return nil, fmt.Errorf("fishplus: bad terminator %q", line)
			}
			resp.Status = status
			resp.Msg = strings.TrimSpace(msg)
			return resp, nil
		}
		if id == 0 {
			// Pre-handshake noise, drop it.
			continue
		}
		if binary && strings.HasPrefix(line, "#") {
			n, convErr := strconv.Atoi(line[1:])
			if convErr != nil || n < 0 || n > MaxFrameLen {
				s.broken.Store(true)
				return nil, fmt.Errorf("fishplus: bad data frame header %q", line)
			}
			buf := make([]byte, n)
			if err := s.readFullCtx(ctx, buf); err != nil {
				s.broken.Store(true)
				return nil, err
			}
			resp.Data = append(resp.Data, buf...)
			continue
		}
		if onLine != nil {
			onLine(line)
		} else {
			resp.Lines = append(resp.Lines, line)
		}
	}
}

// DrainAfterCancelTimeout bounds how long the client will keep reading an
// answer nobody wants any more. Past it the session is worth less than the
// wait, and a reconnect is the cheaper answer.
var DrainAfterCancelTimeout = 10 * time.Second

// sessionIOTimeout is the backstop applied to every blocking read and write on
// the transport. The SSH session pipes are not net.Conns and so cannot carry a
// deadline of their own; without a backstop a request whose context lacks a
// deadline (a directory read that simply blocks) would hang forever on a
// silently frozen connection -- a suspended VM, say, whose TCP link stays
// ESTABLISHED and never returns an error. A request context that already
// carries an earlier deadline wins over this one.
const sessionIOTimeout = 90 * time.Second

// withIOTimeout returns a context that carries at least sessionIOTimeout. A
// caller that already bounded its context keeps its (tighter) deadline; one
// that did not gets the backstop so a frozen transport cannot hang it.
// withIOTimeout returns a context that carries at least sessionIOTimeout. A
// caller that already bounded its context keeps its (tighter) deadline; one
// that did not gets the backstop so a frozen transport cannot hang it. The
// ownTimeout result reports whether the backstop (and not a caller deadline)
// is what will fire, which is how the caller tells a frozen transport from a
// caller-initiated timeout.
func withIOTimeout(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}, false
	}
	c, cancel := context.WithTimeout(ctx, sessionIOTimeout)
	return c, cancel, true
}

// drainToTerminator reads and discards the rest of a response. It is what
// makes cancelling a request survivable: the terminator is unforgeable, so
// finding it means the stream is back at a request boundary no matter what
// the remote tools printed on the way.
//
// It cannot interrupt a read that is already blocked — nothing here can, and
// the ordinary path has the same property — so the deadline is checked
// between lines rather than during one.
// drainToTerminator reads and discards the rest of a response. It is what
// makes cancelling a request survivable: the terminator is unforgeable, so
// finding it means the stream is back at a request boundary no matter what
// the remote tools printed on the way.
//
// It cannot interrupt a read that is already blocked — nothing here can, and
// the ordinary path has the same property — so the deadline is checked
// between lines rather than during one. Crucially it uses its OWN timeout
// context, never the (already cancelled) request context, otherwise the drain
// would give up instantly and poison a session that could have been saved.
func (s *Session) drainToTerminator(ctx context.Context, prefix string, binary bool) error {
	dctx, dcancel := context.WithTimeout(context.Background(), DrainAfterCancelTimeout)
	defer dcancel()
	for {
		line, err := s.readLineCtx(dctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return ErrBroken
			}
			return err
		}
		if strings.HasPrefix(line, prefix) {
			return nil
		}
		if !binary || !strings.HasPrefix(line, "#") {
			continue
		}
		// A frame header has to be honoured even while discarding, or its
		// payload would be read as lines and a byte that happens to be a
		// newline would look like a boundary.
		n, convErr := strconv.Atoi(line[1:])
		if convErr != nil || n < 0 || n > MaxFrameLen {
			return fmt.Errorf("fishplus: bad data frame header %q while draining", line)
		}
		if err := s.copyNCtx(dctx, int64(n)); err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return ErrBroken
			}
			return err
		}
	}
}
func (s *Session) readLine() (string, error) {
	var buf []byte
	for {
		chunk, err := s.r.ReadSlice('\n')
		if len(buf)+len(chunk) > MaxLineLen {
			return "", fmt.Errorf("fishplus: response line exceeds %d bytes", MaxLineLen)
		}
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return "", err
		}
		break
	}
	return strings.TrimRight(string(buf), "\r\n"), nil
}

// The SSH session pipes are not net.Conns and cannot be given a deadline, so a
// read that blocks on a silently frozen connection would never return and the
// context could not interrupt it. readLineCtx races the read against the
// context and is the ONLY place that touches s.r, so the bufio.Reader is never
// read by two goroutines at once.
//
// When the context fires the in-flight read goroutine is always settled before
// this call returns: a cancellation tries to preserve the session (the peer is
// usually still responsive, so the line is collected and the caller drains to
// the terminator), while a deadline means the peer will not answer and the
// transport is closed. Either way the caller gets a deterministic error and no
// goroutine is left owning s.r.
func (s *Session) readLineCtx(ctx context.Context) (string, error) {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	if s.closing.Load() || s.broken.Load() {
		return "", ErrBroken
	}
	rctx, rcancel, own := withIOTimeout(ctx)
	defer rcancel()
	type out struct {
		line string
		err  error
	}
	ch := make(chan out, 1)
	go func() {
		line, err := s.readLine()
		ch <- out{line, err}
	}()
	select {
	case r := <-ch:
		return r.line, r.err
	case <-rctx.Done():
		if rctx.Err() == context.Canceled {
			// Try to preserve the session: let the in-flight read finish if
			// the peer is still responsive, then let the caller drain.
			timer := time.NewTimer(DrainAfterCancelTimeout)
			defer timer.Stop()
			select {
			case r := <-ch:
				return r.line, r.err
			case <-timer.C:
				s.poisonAndClose()
				<-ch
				return "", ErrBroken
			case <-s.closeCh:
				<-ch
				return "", ErrBroken
			}
		}
		// Deadline: the peer is not answering (frozen transport, or the
		// caller gave up). The session can no longer be trusted.
		s.poisonAndClose()
		<-ch
		if own {
			return "", ErrBroken
		}
		return "", context.DeadlineExceeded
	}
}

// readFullCtx is readLineCtx's counterpart for the binary data frames.
func (s *Session) readFullCtx(ctx context.Context, buf []byte) error {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	if s.closing.Load() || s.broken.Load() {
		return ErrBroken
	}
	rctx, rcancel, own := withIOTimeout(ctx)
	defer rcancel()
	type out struct{ err error }
	ch := make(chan out, 1)
	go func() {
		_, err := io.ReadFull(s.r, buf)
		ch <- out{err}
	}()
	select {
	case r := <-ch:
		return r.err
	case <-rctx.Done():
		s.poisonAndClose()
		<-ch
		if rctx.Err() == context.Canceled || own {
			return ErrBroken
		}
		return context.DeadlineExceeded
	}
}

// writeCtx bounds a write to the context the same way readLineCtx bounds a read.
// A frozen transport may let a write fill the local buffer and then block until
// the remote reads again, which never happens; the race makes that recoverable.
// A write can never be "drained", so any interruption closes the transport and
// returns ErrBroken.
func (s *Session) writeCtx(ctx context.Context, p []byte) (int, error) {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	if s.closing.Load() || s.broken.Load() {
		return 0, ErrBroken
	}
	rctx, rcancel, _ := withIOTimeout(ctx)
	defer rcancel()
	type out struct {
		n   int
		err error
	}
	ch := make(chan out, 1)
	go func() {
		n, err := s.w.Write(p)
		ch <- out{n, err}
	}()
	select {
	case r := <-ch:
		return r.n, r.err
	case <-rctx.Done():
		s.poisonAndClose()
		<-ch
		return 0, ErrBroken
	}
}

// copyNCtx bounds io.CopyN the same way, used while discarding a frame during
// a cancelled drain.
func (s *Session) copyNCtx(ctx context.Context, n int64) error {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	if s.closing.Load() || s.broken.Load() {
		return ErrBroken
	}
	rctx, rcancel, own := withIOTimeout(ctx)
	defer rcancel()
	type out struct{ err error }
	ch := make(chan out, 1)
	go func() {
		_, err := io.CopyN(io.Discard, s.r, n)
		ch <- out{err}
	}()
	select {
	case r := <-ch:
		return r.err
	case <-rctx.Done():
		s.poisonAndClose()
		<-ch
		if rctx.Err() == context.Canceled || own {
			return ErrBroken
		}
		return context.DeadlineExceeded
	}
}

// Ping asks the remote helper to echo the payload back. It doubles as a
// keepalive and as a synchronization check.
func (s *Session) Ping(ctx context.Context, payload string) (string, error) {
	resp, err := s.ExecPath(ctx, "ping", payload)
	if err != nil {
		return "", err
	}
	if err := resp.Err("ping"); err != nil {
		return "", err
	}
	return strings.Join(resp.Lines, "\n"), nil
}

// Noop is the cheapest possible round trip.
func (s *Session) Noop(ctx context.Context) error {
	resp, err := s.Exec(ctx, "noop")
	if err != nil {
		return err
	}
	return resp.Err("noop")
}

// TryNoop validates an idle session without queueing behind another request.
// attempted is false when the protocol mutex is already owned, which lets a
// session pool open an independent connection instead of freezing a new panel
// behind a long copy or scan. Once the noop has gone onto the wire it must
// finish or the stream is no longer reusable; cancellation therefore closes
// the transport to interrupt a peer that stopped answering.
func (s *Session) TryNoop(ctx context.Context) (attempted bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if s.closing.Load() {
		return false, ErrBroken
	}
	// Once a noop is on the wire, a timeout must interrupt its blocked read.
	// Without a closer the caller could return, but the worker would leak while
	// holding both the protocol mutex and request gate forever.
	if s.closer == nil {
		return false, nil
	}
	if !s.tryAcquireRequest() {
		return false, nil
	}
	if !s.mu.TryLock() {
		s.releaseRequest()
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		s.releaseRequest()
		return false, err
	}
	done := make(chan error, 1)
	// 0 means the response is still in flight, 1 means the worker owns normal
	// completion, and 2 means the timeout path owns transport interruption.
	// This makes the response/timeout boundary deterministic: a completed noop
	// never loses the select lottery and gets its healthy session closed.
	var outcome atomic.Uint32
	go func() {
		err := s.noopLocked(ctx)
		outcome.CompareAndSwap(0, 1)
		s.mu.Unlock()
		s.releaseRequest()
		done <- err
	}()

	select {
	case err := <-done:
		return true, err
	case <-ctx.Done():
		if !outcome.CompareAndSwap(0, 2) {
			// The response finished before cancellation won ownership. Wait for
			// the worker's small local cleanup rather than closing a synchronized
			// and otherwise healthy transport.
			return true, <-done
		}
		// Close reaches an available transport without waiting for s.mu. That
		// makes the blocked read fail and always poisons this half-read stream.
		_ = s.Close()
		return true, ctx.Err()
	}
}

// noopLocked is the specialized request path used by TryNoop after it has
// acquired s.mu with TryLock. Calling Exec here would attempt to lock it again.
func (s *Session) noopLocked(ctx context.Context) error {
	if s.closing.Load() || s.broken.Load() {
		return ErrBroken
	}
	defer func() { s.lastUse = time.Now() }()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.seq++
	id := s.seq
	if _, err := s.writeCtx(ctx, []byte(strconv.FormatUint(id, 10)+" noop\n")); err != nil {
		s.broken.Store(true)
		return err
	}
	resp, err := s.readResponse(ctx, id, false)
	if err != nil {
		return err
	}
	return resp.Err("noop")
}

// Close tears the session down. The remote helper terminates on its own once
// its stdin reaches EOF, so no farewell command is sent: a stuck remote must
// never be able to block the UI thread inside Close.
// sessionWriter adapts the session's cancellable, single-owner write path to
// io.Writer so callers such as ExecStream can stream a request body without
// ever touching s.w directly (and therefore without hanging on a frozen
// transport).
type sessionWriter struct {
	s   *Session
	ctx context.Context
}

func (w *sessionWriter) Write(p []byte) (int, error) {
	return w.s.writeCtx(w.ctx, p)
}

// poisonAndClose marks the session broken and tears the transport down. It is
// called from inside request-processing goroutines that already hold s.mu, so
// s.broken is set directly; the closeOnce guard makes it safe to call more
// than once and keeps it in sync with Close.
func (s *Session) poisonAndClose() {
	s.broken.Store(true)
	s.closeOnce.Do(func() {
		close(s.closeCh)
		if s.closer != nil {
			_ = s.closer.Close()
		}
	})
}

func (s *Session) Close() error {
	s.closing.Store(true)
	s.broken.Store(true)
	s.closeOnce.Do(func() {
		// Wake requests waiting for the protocol gate even if the transport has
		// no closer (or its Close cannot interrupt a currently blocked read).
		// The active request still owns requestGate and may finish draining, but
		// no new caller should remain queued behind a session that is closing.
		close(s.closeCh)
		// Interrupt a request blocked in Read without first waiting for the
		// protocol mutex it owns. Closing a net.Conn/ShellStream is local and
		// cannot wait for a farewell from a stuck remote.
		if s.closer != nil {
			s.closeErr = s.closer.Close()
		}
	})
	return s.closeErr
}
