//go:build !lite

// SFTP support statically links github.com/pkg/sftp and
// golang.org/x/crypto/ssh, which a lite build (f4#1178) exists to shed --
// see internal/plughost/plugins_lite.go for the accounting of what a lite
// build carries instead.

package netfox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/text/encoding"

	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type SFTPVFS struct {
	parent    vfs.VFS
	client    *sftp.Client
	ssh       *ssh.Client
	shared    *sftpConnectionRefs
	codepage  string
	pathMu    sync.RWMutex
	path      string
	title     string
	decoder   *encoding.Decoder
	closeOnce sync.Once
}

type sftpConnectionRefs struct {
	sync.Mutex
	refs   int
	client *sftp.Client
	ssh    *ssh.Client
}

func (r *sftpConnectionRefs) retain() {
	if r == nil {
		return
	}
	r.Lock()
	r.refs++
	r.Unlock()
}

func (r *sftpConnectionRefs) release() error {
	if r == nil {
		return nil
	}
	r.Lock()
	if r.refs > 0 {
		r.refs--
	}
	last := r.refs == 0
	client, sshClient := r.client, r.ssh
	if last {
		r.client, r.ssh = nil, nil
	}
	r.Unlock()
	if !last {
		return nil
	}
	if client != nil {
		_ = client.Close()
	}
	if sshClient != nil {
		return sshClient.Close()
	}
	return nil
}

func (v *SFTPVFS) encodePath(p string) string {
	_, encoder := vfs.GetCodepageDecoderEncoder(v.codepage)
	if encoder == nil {
		return p
	}
	encoded, err := encoder.Bytes([]byte(p))
	if err == nil {
		return string(encoded)
	}
	return p
}

func NewSFTPVFS(parent vfs.VFS, host, port, user, pass, keyPath string, timeout int, cp string, px netproxy.Settings) (*SFTPVFS, error) {
	vtui.DebugLog("NET: Initiating SFTP connection to %s:%s (user: %s)", host, port, user)
	sshClient, err := DialSSH(host, port, user, pass, keyPath, timeout, px)
	if err != nil {
		return nil, err
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close() // Preserve the SFTP startup failure.
		return nil, err
	}
	vtui.DebugLog("NET: SFTP session established successfully")

	pwd, err := sftpClient.Getwd()
	if err != nil || pwd == "" {
		pwd = "/"
	}

	title := host
	if user != "" {
		title = user + "@" + host
	}

	dec, _ := vfs.GetCodepageDecoderEncoder(cp)
	shared := &sftpConnectionRefs{refs: 1, client: sftpClient, ssh: sshClient}
	return &SFTPVFS{
		parent:   parent,
		client:   sftpClient,
		ssh:      sshClient,
		shared:   shared,
		path:     pwd,
		title:    title,
		decoder:  dec,
		codepage: cp,
	}, nil
}

// EncodeCommandListANSI applies the same configured filename codepage used by
// this panel. A fresh encoder keeps parallel Apply workers independent.
func (v *SFTPVFS) EncodeCommandListANSI(text []byte) ([]byte, error) {
	_, encoder := vfs.GetCodepageDecoderEncoder(v.codepage)
	if encoder == nil {
		return append([]byte(nil), text...), nil
	}
	return encoder.Bytes(text)
}

func (v *SFTPVFS) GetTitle() string { return v.title }
func (v *SFTPVFS) SessionKey() any  { return v.client }

func (v *SFTPVFS) IsAtRoot() bool {
	p := v.GetPath()
	return p == "/" || p == ""
}
func (v *SFTPVFS) GetPath() string {
	v.pathMu.RLock()
	defer v.pathMu.RUnlock()
	return v.path
}
func (v *SFTPVFS) IsAbs(p string) bool { return path.IsAbs(p) }
func (v *SFTPVFS) SetPath(p string) error {
	var target string
	if path.IsAbs(p) {
		target = p
	} else {
		target = v.Join(v.GetPath(), p)
	}
	target = path.Clean(target)
	info, err := v.client.Stat(v.encodePath(target))
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.ErrInvalid
	}
	v.pathMu.Lock()
	v.path = target
	v.pathMu.Unlock()
	return nil
}

func (v *SFTPVFS) ReadDir(ctx context.Context, p string, onChunk func([]vfs.VFSItem)) error {
	vtui.DebugLog("SFTP: ReadDir(%q) starting...", p)
	entries, err := v.client.ReadDir(v.encodePath(p))
	if err != nil {
		vtui.DebugLog("SFTP: ReadDir(%q) failed: %v", p, err)
		return err
	}
	var items []vfs.VFSItem
	for i, e := range entries {
		if ctx.Err() != nil {
			vtui.DebugLog("SFTP: ReadDir(%q) aborted by context cancellation after %d items", p, i)
			return ctx.Err()
		}

		isDir := e.IsDir()
		isSymlink := e.Mode()&os.ModeSymlink != 0
		if !isDir && isSymlink {
			if target, err := v.client.Stat(v.encodePath(v.Join(p, e.Name()))); err == nil {
				isDir = target.IsDir()
			}
		}

		var unixMode uint32
		var uid, gid int
		var aTime time.Time
		known := vfs.MetadataExplicit | vfs.MetadataPermissions | vfs.MetadataHidden | vfs.MetadataExecutable | vfs.MetadataMTime
		if stat, ok := e.Sys().(*sftp.FileStat); ok {
			known |= vfs.MetadataUID | vfs.MetadataGID | vfs.MetadataATime
			unixMode = stat.Mode
			uid = int(stat.UID)
			gid = int(stat.GID)
			aTime = time.Unix(int64(stat.Atime), 0)
		} else {
			unixMode = uint32(e.Mode().Perm())
			aTime = e.ModTime()
		}

		name := e.Name()
		if v.decoder != nil {
			if decoded, err := v.decoder.Bytes([]byte(name)); err == nil {
				name = string(decoded)
			}
		}

		items = append(items, vfs.VFSItem{
			KnownMetadata: known, SizeKnown: true,
			Name: name, Size: e.Size(), IsDir: isDir, IsSymlink: isSymlink,
			MTime: e.ModTime(), IsExecutable: e.Mode().Perm()&0111 != 0,
			IsHidden: strings.HasPrefix(name, "."),
			UnixMode: unixMode, Uid: uid, Gid: gid, ATime: aTime,
		})

		if len(items) >= 500 || i == len(entries)-1 {
			onChunk(items)
			items = make([]vfs.VFSItem, 0, 500)
		}
	}
	vtui.DebugLog("SFTP: ReadDir(%q) finished, total: %d", p, len(entries))
	return nil
}

func (v *SFTPVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	info, err := v.client.Stat(v.encodePath(p))
	if err != nil {
		return vfs.VFSItem{}, err
	}

	var unixMode uint32
	var uid, gid int
	var aTime time.Time
	known := vfs.MetadataExplicit | vfs.MetadataPermissions | vfs.MetadataHidden | vfs.MetadataExecutable | vfs.MetadataMTime

	if stat, ok := info.Sys().(*sftp.FileStat); ok {
		known |= vfs.MetadataUID | vfs.MetadataGID | vfs.MetadataATime
		unixMode = stat.Mode
		uid = int(stat.UID)
		gid = int(stat.GID)
		aTime = time.Unix(int64(stat.Atime), 0)
	} else {
		unixMode = uint32(info.Mode().Perm())
		aTime = info.ModTime()
	}

	return vfs.VFSItem{
		KnownMetadata: known, SizeKnown: true,
		Name: info.Name(), Size: info.Size(), IsDir: info.IsDir(),
		MTime: info.ModTime(), IsExecutable: info.Mode().Perm()&0111 != 0,
		IsHidden: strings.HasPrefix(info.Name(), "."),
		UnixMode: unixMode, Uid: uid, Gid: gid, ATime: aTime,
	}, nil
}

func (v *SFTPVFS) Join(e ...string) string { return path.Join(e...) }
func (v *SFTPVFS) Abs(p string) (string, error) {
	if path.IsAbs(p) {
		return path.Clean(p), nil
	}
	return v.Join(v.GetPath(), p), nil
}
func (v *SFTPVFS) Base(p string) string { return path.Base(p) }
func (v *SFTPVFS) Dir(p string) string  { return path.Dir(p) }
func (v *SFTPVFS) MkDir(ctx context.Context, p string) error {
	return v.client.MkdirAll(v.encodePath(p))
}
func (v *SFTPVFS) Remove(ctx context.Context, p string) error {
	info, err := v.client.Lstat(v.encodePath(p))
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return v.client.Remove(v.encodePath(p))
	}

	walker := v.client.Walk(v.encodePath(p))
	var items []string
	for walker.Step() {
		if err := walker.Err(); err != nil {
			return err
		}
		items = append(items, walker.Path())
	}

	for i := len(items) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		itemPath := items[i]
		st, err := v.client.Lstat(itemPath)
		if err != nil {
			continue
		}
		if st.IsDir() {
			if err := v.client.RemoveDirectory(itemPath); err != nil {
				return err
			}
		} else {
			if err := v.client.Remove(itemPath); err != nil {
				return err
			}
		}
	}
	return nil
}
func (v *SFTPVFS) Rename(ctx context.Context, o, n string) error {
	return v.client.Rename(v.encodePath(o), v.encodePath(n))
}

func (v *SFTPVFS) SetAttributes(ctx context.Context, path string, item vfs.VFSItem) error {
	encPath := v.encodePath(path)
	if item.UnixMode != 0 {
		if err := v.client.Chmod(encPath, os.FileMode(item.UnixMode)); err != nil {
			return err
		}
	}
	if item.Uid != -1 && item.Gid != -1 {
		if err := v.client.Chown(encPath, item.Uid, item.Gid); err != nil {
			return err
		}
	}
	atime := item.ATime
	mtime := item.MTime
	if atime.IsZero() {
		atime = mtime
	}
	if mtime.IsZero() {
		mtime = atime
	}
	if !atime.IsZero() && !mtime.IsZero() {
		return v.client.Chtimes(encPath, atime, mtime)
	}
	return nil
}

func (v *SFTPVFS) GetCapabilities() vfs.VFSCapabilities {
	// HasWrite: the panels have been copying files onto SFTP hosts through
	// Create for as long as this backend has existed, and a FUSE mount
	// commits a file through exactly that call. Writable SFTP mounts are
	// the reason the whole feature was worth building.
	return vfs.VFSCapabilities{HasRandomAccess: true, HasUnixPermissions: true, HasWrite: true}
}

// The upstream SFTP client multiplexes independent requests safely over one
// SSH connection. This lets FUSE serve directory listings, stats, and reads
// concurrently instead of making a remote mount wait behind one operation.
func (*SFTPVFS) SupportsConcurrentCalls() bool { return true }

func (v *SFTPVFS) Search(ctx context.Context, p, pat string) (chan int64, error) { return nil, nil }

func (v *SFTPVFS) Open(ctx context.Context, p string) (vfs.ReadAtCloser, error) {
	vtui.DebugLog("SFTP: Opening file %q for reading...", p)
	f, err := v.client.Open(v.encodePath(p))
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close() // Preserve the remote stat failure.
		return nil, err
	}
	return &sftpFileWrapper{File: f, size: info.Size()}, nil
}

func (v *SFTPVFS) Create(ctx context.Context, p string) (io.WriteCloser, error) {
	return v.client.Create(v.encodePath(p))
}
func (v *SFTPVFS) ParentVFS() vfs.VFS { return v.parent }
func (v *SFTPVFS) Close() error {
	if v == nil {
		return nil
	}
	var err error
	v.closeOnce.Do(func() {
		if v.shared != nil {
			err = v.shared.release()
			return
		}
		if v.client != nil {
			_ = v.client.Close()
		}
		if v.ssh != nil {
			err = v.ssh.Close()
		}
	})
	return err
}
func (v *SFTPVFS) Clone() vfs.VFS {
	if v.shared == nil {
		return v
	}
	v.shared.retain()
	decoder, _ := vfs.GetCodepageDecoderEncoder(v.codepage)
	return &SFTPVFS{
		parent: v.parent, client: v.client, ssh: v.ssh, shared: v.shared,
		codepage: v.codepage, path: v.GetPath(), title: v.title, decoder: decoder,
	}
}

func (v *SFTPVFS) OpenPty(cols, rows int) (any, error) {
	pty, err := NewSSHPty(v.ssh)
	if err != nil {
		return nil, err
	}
	pty.SetSize(cols, rows)
	if err := pty.Run(""); err != nil {
		return nil, fmt.Errorf("sftp: start SSH PTY: %w", errors.Join(err, pty.Close()))
	}
	return pty, nil
}

// CommandRunnerInfo describes commands executed in independent SSH sessions.
// A modest cap avoids opening an unbounded number of channels on servers with
// conservative MaxSessions settings while still permitting useful parallelism.
func (*SFTPVFS) CommandRunnerInfo() vfs.CommandRunnerInfo {
	return vfs.CommandRunnerInfo{Dialect: vfs.CommandDialectPOSIX, MaxParallel: 4}
}

// RunCommand starts a new SSH session rather than borrowing either the SFTP
// subsystem's channel or the panel's interactive PTY. The current directory is
// resolved before opening that session, so later panel navigation cannot move
// a command that is already being dispatched.
func (v *SFTPVFS) RunCommand(ctx context.Context, dir, command string, cb func(line string)) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if v.ssh == nil {
		return 0, errors.New("sftp: SSH connection is unavailable")
	}
	if strings.TrimSpace(command) == "" {
		return 0, errors.New("sftp: empty shell command")
	}

	base := v.GetPath()
	cwd := dir
	if cwd == "" {
		cwd = base
	} else if path.IsAbs(cwd) {
		cwd = path.Clean(cwd)
	} else {
		cwd = path.Join(base, cwd)
	}
	if cwd == "" {
		cwd = "/"
	}
	if strings.IndexByte(cwd, 0) >= 0 {
		return 0, errors.New("sftp: command directory contains NUL")
	}

	session, err := v.ssh.NewSession()
	if err != nil {
		return 0, err
	}
	codec, err := v.commandCodec()
	if err != nil {
		_ = session.Close()
		return 0, err
	}
	return runSFTPCommandSessionWithCodec(ctx, &liveSFTPCommandSession{Session: session}, cwd, command, cb, codec)
}

type sftpCommandCodec struct {
	encode func(string) (string, error)
	decode func([]byte) string
}

func (v *SFTPVFS) commandCodec() (sftpCommandCodec, error) {
	if v.codepage == "" || v.codepage == "65001" {
		return sftpCommandCodec{}, nil
	}
	if v.codepage == "1200" || v.codepage == "1201" {
		return sftpCommandCodec{}, fmt.Errorf("sftp: UTF-16 panel codepages cannot carry shell commands")
	}
	_, probe := vfs.GetCodepageDecoderEncoder(v.codepage)
	if probe == nil {
		return sftpCommandCodec{}, fmt.Errorf("sftp: unsupported command codepage %q", v.codepage)
	}
	return sftpCommandCodec{
		encode: func(command string) (string, error) {
			_, encoder := vfs.GetCodepageDecoderEncoder(v.codepage)
			encoded, err := encoder.Bytes([]byte(command))
			return string(encoded), err
		},
		decode: func(output []byte) string {
			decoder, _ := vfs.GetCodepageDecoderEncoder(v.codepage)
			if decoder != nil {
				if decoded, err := decoder.Bytes(output); err == nil {
					return strings.ToValidUTF8(string(decoded), "\uFFFD")
				}
			}
			return strings.ToValidUTF8(string(output), "\uFFFD")
		},
	}, nil
}

type sftpCommandSession interface {
	Configure(stdout, stderr io.Writer)
	Start(command string) error
	Wait() error
	Signal(signal ssh.Signal) error
	Close() error
}

type liveSFTPCommandSession struct{ *ssh.Session }

func (s *liveSFTPCommandSession) Configure(stdout, stderr io.Writer) {
	s.Stdin = strings.NewReader("")
	s.Stdout = stdout
	s.Stderr = stderr
}

func runSFTPCommandSession(
	ctx context.Context,
	session sftpCommandSession,
	dir, command string,
	cb func(line string),
) (int, error) {
	return runSFTPCommandSessionWithCodec(ctx, session, dir, command, cb, sftpCommandCodec{})
}

func runSFTPCommandSessionWithCodec(
	ctx context.Context,
	session sftpCommandSession,
	dir, command string,
	cb func(line string),
	codec sftpCommandCodec,
) (int, error) {
	defer func() { _ = session.Close() }() // Command status comes from Wait.
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	lines := newSFTPCommandLineWriterWithDecoder(cb, codec.decode)
	session.Configure(lines, lines)
	// Keep the closing syntax on its own line so a valid trailing shell comment
	// in the user's raw command cannot comment it out.
	wrapper := "cd " + quoteSFTPCommandArgument(dir) + " && (\n" + command + "\n)"
	if codec.encode != nil {
		var err error
		wrapper, err = codec.encode(wrapper)
		if err != nil {
			return 0, err
		}
	}
	if err := session.Start(wrapper); err != nil {
		return 0, err
	}

	wait := make(chan error, 1)
	go func() { wait <- session.Wait() }()

	select {
	case err := <-wait:
		lines.Flush()
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return sftpCommandExitStatus(err)
	case <-ctx.Done():
		// Servers are not required to honor signals. Closing this command's
		// independent channel after SIGKILL guarantees Wait is released without
		// disturbing SFTP transfers or other Apply workers.
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		<-wait
		lines.Flush()
		return 0, ctx.Err()
	}
}

func sftpCommandExitStatus(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var status interface{ ExitStatus() int }
	if errors.As(err, &status) {
		return status.ExitStatus(), nil
	}
	return 0, err
}

func quoteSFTPCommandArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type sftpCommandLineWriter struct {
	mu      sync.Mutex
	pending []byte
	cb      func(string)
	decode  func([]byte) string
}

const sftpCommandOutputChunkBytes = 64 << 10

func newSFTPCommandLineWriter(cb func(string)) *sftpCommandLineWriter {
	return newSFTPCommandLineWriterWithDecoder(cb, nil)
}

func newSFTPCommandLineWriterWithDecoder(cb func(string), decode func([]byte) string) *sftpCommandLineWriter {
	return &sftpCommandLineWriter{cb: cb, decode: decode}
}

func (w *sftpCommandLineWriter) decoded(line []byte) string {
	if w.decode != nil {
		return w.decode(line)
	}
	return strings.ToValidUTF8(string(line), "\uFFFD")
}

func (w *sftpCommandLineWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 || w.cb == nil {
		return n, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 && len(w.pending) <= sftpCommandOutputChunkBytes {
			break
		}
		end, consumed := i, i+1
		if i < 0 || i > sftpCommandOutputChunkBytes {
			end = sftpCommandOutputChunkEnd(w.pending, sftpCommandOutputChunkBytes)
			consumed = end
		}
		line := w.pending[:end]
		if len(line) != 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		w.cb(w.decoded(line))
		w.pending = w.pending[consumed:]
	}
	return n, nil
}

func sftpCommandOutputChunkEnd(data []byte, limit int) int {
	if len(data) <= limit {
		return len(data)
	}
	end := limit
	for end > 0 && end < len(data) && !utf8.RuneStart(data[end]) {
		end--
	}
	if end == 0 {
		return limit
	}
	return end
}

func (w *sftpCommandLineWriter) Flush() {
	if w.cb == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return
	}
	line := w.pending
	if line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	w.cb(w.decoded(line))
	w.pending = nil
}

var (
	_ io.Writer                     = (*sftpCommandLineWriter)(nil)
	_ vfs.CommandRunner             = (*SFTPVFS)(nil)
	_ vfs.CommandRunnerInfoProvider = (*SFTPVFS)(nil)
	_ vfs.CommandListANSIEncoder    = (*SFTPVFS)(nil)
)

type sftpProvider struct{}

func (p *sftpProvider) Name() string  { return "NetFox-SFTP" }
func (p *sftpProvider) Priority() int { return 100 }
func (p *sftpProvider) CanOpen(ctx context.Context, parent vfs.VFS, pth string) bool {
	w, ok := parent.(*netFoxVFSWrapper)
	if !ok {
		return false
	}
	item, err := w.Stat(ctx, pth)
	if err != nil || item.IsDir {
		return false
	}
	f, err := w.Open(ctx, pth)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }() // The configuration file is read-only.
	var cfg NetFoxConfig
	if err := json.NewDecoder(ctxReader{f, ctx}).Decode(&cfg); err != nil {
		return false
	}
	return cfg.Type == "sftp" || cfg.Type == ""
}
func (p *sftpProvider) Open(ctx context.Context, parent vfs.VFS, pth string) (vfs.VFS, error) {
	w := parent.(*netFoxVFSWrapper)
	f, err := w.Open(ctx, pth)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // The configuration file is read-only.
	var cfg NetFoxConfig
	if err := json.NewDecoder(ctxReader{f, ctx}).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("netfox: decode SFTP connection: %w", err)
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
	res, err := NewSFTPVFS(parent, cfg.Host, port, cfg.User, cfg.Pass, cfg.KeyPath, timeout, cfg.Codepage, cfg.Proxy())
	if err != nil {
		return nil, err
	}
	return res, nil
}

type sftpProtocolHandler struct{}

func (ph *sftpProtocolHandler) Prefix() string      { return "sftp" }
func (ph *sftpProtocolHandler) DefaultPort() string { return "22" }
func (ph *sftpProtocolHandler) BuildExtraUI(cfg *NetFoxConfig, x, y, w, h int) (vtui.UIElement, func()) {
	return nil, func() {}
}

func init() {
	vfs.RegisterProvider(&sftpProvider{})
	RegisterProtocol(&sftpProtocolHandler{})
}

type sftpFileWrapper struct {
	*sftp.File
	size int64
}

func (w *sftpFileWrapper) Size() int64 { return w.size }
func (w *sftpFileWrapper) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	return w.File.ReadAt(p, off)
}
func (w *sftpFileWrapper) Read(ctx context.Context, p []byte) (int, error) {
	return w.File.Read(p)
}

// Readlink and Symlink make SFTPVFS a vfs.SymlinkVFS. The protocol has both
// operations, so a mounted host behaves like the file system it is rather
// than like a listing of one: tar -x can extract into it, and ls -l shows
// what a link points at instead of a file that happens to be short.

func (v *SFTPVFS) Readlink(ctx context.Context, p string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return v.client.ReadLink(v.encodePath(p))
}

func (v *SFTPVFS) Symlink(ctx context.Context, target, linkPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return v.client.Symlink(target, v.encodePath(linkPath))
}

// OpenWriteAt makes SFTPVFS a vfs.RandomWriteVFS. This is the backend the
// interface was written for: a remote file is exactly where re-uploading
// everything to change ten bytes hurts.
func (v *SFTPVFS) OpenWriteAt(ctx context.Context, p string) (vfs.WriterAtCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return v.client.OpenFile(v.encodePath(p), os.O_RDWR|os.O_CREATE)
}
