//go:build !lite

// FTP support statically links github.com/jlaffaye/ftp, which a lite build
// (f4#1178) exists to shed -- see internal/plughost/plugins_lite.go for the
// accounting of what a lite build carries instead.

package netfox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

import "golang.org/x/text/encoding"

type FTPVFS struct {
	mu        sync.Mutex
	parent    vfs.VFS
	conn      *ftp.ServerConn
	session   *ftpSession
	cwd       string
	title     string
	decoder   *encoding.Decoder
	encoder   *encoding.Encoder
	closeOnce sync.Once
}

// ftpSession owns one control connection shared by independent VFS views.
// FTP keeps a current directory on the control connection itself, so opening
// a second view must at least serialize every command and keep the view's cwd
// outside the shared object. Reference counting also prevents a cloned
// workspace from quitting the connection still used by its source.
type ftpSession struct {
	mu     sync.Mutex
	conn   *ftp.ServerConn
	refs   int
	closed bool
}

func (s *ftpSession) retain() {
	s.mu.Lock()
	if !s.closed {
		s.refs++
	}
	s.mu.Unlock()
}

func (s *ftpSession) release() error {
	s.mu.Lock()
	if s.refs > 0 {
		s.refs--
	}
	if s.refs != 0 || s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Quit()
}

type timeoutConn struct {
	net.Conn
	timeout time.Duration
}

func (c *timeoutConn) Read(b []byte) (int, error) {
	// Only fails on a connection already closed, which the Read below
	// reports properly; swallowing it here would hide nothing.
	_ = c.SetReadDeadline(time.Now().Add(c.timeout))
	return c.Conn.Read(b)
}

func (c *timeoutConn) Write(b []byte) (int, error) {
	_ = c.SetWriteDeadline(time.Now().Add(c.timeout))
	return c.Conn.Write(b)
}

func (v *FTPVFS) encodePath(p string) string {
	if v.encoder == nil {
		return p
	}
	encoded, err := v.encoder.Bytes([]byte(p))
	if err == nil {
		return string(encoded)
	}
	return p
}

func NewFTPVFS(parent vfs.VFS, host, port, user, pass string, timeout int, options map[string]string, cp string, px netproxy.Settings) (*FTPVFS, error) {
	addr := host + ":" + port

	timeoutDur := time.Duration(timeout) * time.Second
	if timeoutDur <= 0 {
		timeoutDur = 15 * time.Second
	}

	dialOpts := []ftp.DialOption{
		ftp.DialWithTimeout(timeoutDur),
		ftp.DialWithDialFunc(func(network, address string) (net.Conn, error) {
			// Both the control connection and every passive data
			// connection come through here, so a proxied site stays
			// proxied for its transfers too.
			ctx, cancel := context.WithTimeout(context.Background(), timeoutDur)
			defer cancel()
			conn, err := px.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &timeoutConn{Conn: conn, timeout: timeoutDur}, nil
		}),
	}
	if val, ok := options["Passive"]; ok && val == "false" {
		dialOpts = append(dialOpts, ftp.DialWithDisabledEPSV(true))
	}

	c, err := ftp.Dial(addr, dialOpts...)
	if err != nil {
		return nil, err
	}

	err = c.Login(user, pass)
	if err != nil {
		_ = c.Quit() // Preserve the authentication failure.
		return nil, err
	}
	vtui.DebugLog("NET: FTP logged in successfully")

	pwd, err := c.CurrentDir()
	if err != nil {
		pwd = "/"
	}

	title := host
	if user != "" && user != "anonymous" {
		title = user + "@" + host
	}

	dec, enc := vfs.GetCodepageDecoderEncoder(cp)
	return &FTPVFS{
		parent:  parent,
		conn:    c,
		session: &ftpSession{conn: c, refs: 1},
		cwd:     pwd,
		title:   title,
		decoder: dec,
		encoder: enc,
	}, nil
}

func (v *FTPVFS) GetTitle() string { return v.title }
func (v *FTPVFS) SessionKey() any {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.conn != nil {
		return v.conn
	}
	if v.session != nil {
		return v.session
	}
	return nil
}

func (v *FTPVFS) operationLock() func() {
	v.mu.Lock()
	if v.session != nil {
		v.session.mu.Lock()
		return func() {
			v.session.mu.Unlock()
			v.mu.Unlock()
		}
	}
	return v.mu.Unlock
}

func (v *FTPVFS) operationConn() *ftp.ServerConn {
	if v.session != nil {
		return v.session.conn
	}
	return v.conn
}

func (v *FTPVFS) pathLocked(p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(v.cwd, p)
}

func (v *FTPVFS) IsAtRoot() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.cwd == "/" || v.cwd == "" || v.cwd == "."
}
func (v *FTPVFS) GetPath() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.cwd
}
func (v *FTPVFS) IsAbs(p string) bool { return path.IsAbs(p) }
func (v *FTPVFS) SetPath(p string) error {
	unlock := v.operationLock()
	defer unlock()
	target := v.pathLocked(p)
	if err := v.operationConn().ChangeDir(v.encodePath(target)); err != nil {
		return err
	}
	v.cwd = target
	return nil
}

func (v *FTPVFS) ReadDir(ctx context.Context, p string, onChunk func([]vfs.VFSItem)) error {
	unlock := v.operationLock()
	defer unlock()
	target := v.pathLocked(p)
	vtui.DebugLog("FTP: ReadDir(%q) starting...", target)
	entries, err := v.operationConn().List(v.encodePath(target))
	if err != nil {
		vtui.DebugLog("FTP: ReadDir(%q) failed: %v", target, err)
		return err
	}
	var items []vfs.VFSItem
	for i, e := range entries {
		if ctx.Err() != nil {
			vtui.DebugLog("FTP: ReadDir(%q) aborted by context cancellation after %d items", target, i)
			return ctx.Err()
		}
		if e.Name == "." || e.Name == ".." {
			continue
		}

		name := e.Name
		if v.decoder != nil {
			if decoded, err := v.decoder.Bytes([]byte(name)); err == nil {
				name = string(decoded)
			}
		}
		if e.Size > math.MaxInt64 {
			return fmt.Errorf("FTP entry %q is too large: %d bytes exceeds the supported size", name, e.Size)
		}

		// #nosec G115 -- the explicit MaxInt64 check above makes this conversion lossless.
		size := int64(e.Size)
		items = append(items, vfs.VFSItem{
			KnownMetadata: vfs.MetadataExplicit | vfs.MetadataHidden | vfs.MetadataMTime, SizeKnown: true,
			Name: name, Size: size,
			IsDir: e.Type == ftp.EntryTypeFolder, MTime: e.Time,
			IsHidden: strings.HasPrefix(name, "."),
		})
		if len(items) >= 500 || i == len(entries)-1 {
			onChunk(items)
			items = make([]vfs.VFSItem, 0, 500)
		}
	}
	vtui.DebugLog("FTP: ReadDir(%q) finished, total: %d", target, len(entries))
	return nil
}

func (v *FTPVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	unlock := v.operationLock()
	defer unlock()
	fullPath := v.pathLocked(p)
	dir, base := path.Dir(fullPath), path.Base(fullPath)
	entries, err := v.operationConn().List(v.encodePath(dir))
	if err != nil {
		return vfs.VFSItem{}, err
	}
	for _, e := range entries {
		if e.Name == base {
			if e.Size > math.MaxInt64 {
				return vfs.VFSItem{}, fmt.Errorf("FTP entry %q is too large: %d bytes exceeds the supported size", e.Name, e.Size)
			}
			// #nosec G115 -- the explicit MaxInt64 check above makes this conversion lossless.
			size := int64(e.Size)
			return vfs.VFSItem{
				KnownMetadata: vfs.MetadataExplicit | vfs.MetadataHidden | vfs.MetadataMTime, SizeKnown: true,
				Name: e.Name, Size: size,
				IsDir: e.Type == ftp.EntryTypeFolder, MTime: e.Time,
				IsHidden: strings.HasPrefix(e.Name, "."),
			}, nil
		}
	}
	return vfs.VFSItem{}, os.ErrNotExist
}

func (v *FTPVFS) Join(e ...string) string { return path.Join(e...) }
func (v *FTPVFS) Abs(p string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if path.IsAbs(p) {
		return path.Clean(p), nil
	}
	return path.Join(v.cwd, p), nil
}
func (v *FTPVFS) Base(p string) string { return path.Base(p) }
func (v *FTPVFS) Dir(p string) string  { return path.Dir(p) }
func (v *FTPVFS) MkDir(ctx context.Context, p string) error {
	unlock := v.operationLock()
	defer unlock()
	return v.operationConn().MakeDir(v.encodePath(v.pathLocked(p)))
}
func (v *FTPVFS) Remove(ctx context.Context, p string) error {
	unlock := v.operationLock()
	defer unlock()
	return v.removeRecursiveLocked(ctx, v.pathLocked(p))
}

func (v *FTPVFS) removeRecursiveLocked(ctx context.Context, p string) error {
	enc := v.encodePath(p)
	conn := v.operationConn()
	err := conn.Delete(enc)
	if err == nil {
		return nil
	}

	entries, err := conn.List(enc)
	if err != nil {
		return conn.RemoveDir(enc)
	}

	for _, e := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e.Name == "." || e.Name == ".." {
			continue
		}
		full := path.Join(p, e.Name)
		if e.Type == ftp.EntryTypeFolder {
			if err := v.removeRecursiveLocked(ctx, full); err != nil {
				return err
			}
		} else {
			if err := conn.Delete(v.encodePath(full)); err != nil {
				return err
			}
		}
	}

	return conn.RemoveDir(enc)
}
func (v *FTPVFS) Rename(ctx context.Context, o, n string) error {
	unlock := v.operationLock()
	defer unlock()
	return v.operationConn().Rename(v.encodePath(v.pathLocked(o)), v.encodePath(v.pathLocked(n)))
}

func (v *FTPVFS) SetAttributes(ctx context.Context, path string, item vfs.VFSItem) error {
	return fmt.Errorf("SetAttributes not supported for FTP")
}

func (v *FTPVFS) GetCapabilities() vfs.VFSCapabilities {
	return vfs.VFSCapabilities{HasUnixPermissions: true}
}
func (v *FTPVFS) Search(ctx context.Context, p, pat string) (chan int64, error) {
	return nil, nil
}

func (v *FTPVFS) Open(ctx context.Context, p string) (vfs.ReadAtCloser, error) {
	unlock := v.operationLock()
	defer unlock()
	fullPath := v.pathLocked(p)
	vtui.DebugLog("FTP: Opening file %q for reading...", fullPath)
	resp, err := v.operationConn().Retr(v.encodePath(fullPath))
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "f4ftp-*")
	if err != nil {
		_ = resp.Close() // Preserve the temp-file creation failure.
		return nil, err
	}
	cleanup := func() error {
		return errors.Join(tmp.Close(), os.Remove(tmp.Name()))
	}
	size, copyErr := io.Copy(tmp, &ioCtxReader{r: resp, ctx: ctx})
	if err := errors.Join(copyErr, resp.Close()); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	return &ftpFileWrapper{File: tmp, size: size, path: tmp.Name()}, nil
}

func (v *FTPVFS) Create(ctx context.Context, p string) (io.WriteCloser, error) {
	v.mu.Lock()
	conn := v.operationConn()
	encodedPath := v.encodePath(v.pathLocked(p))
	session := v.session
	v.mu.Unlock()
	pr, pw := io.Pipe()
	go func() {
		if session != nil {
			session.mu.Lock()
			defer session.mu.Unlock()
		}
		err := conn.Stor(encodedPath, pr)
		_ = pr.CloseWithError(err) // io.PipeReader.CloseWithError always returns nil.
	}()
	return pw, nil
}

func (v *FTPVFS) ParentVFS() vfs.VFS { return v.parent }
func (v *FTPVFS) Close() error {
	if v == nil {
		return nil
	}
	var err error
	v.closeOnce.Do(func() {
		if v.session != nil {
			err = v.session.release()
		} else if v.conn != nil {
			err = v.conn.Quit()
		}
	})
	return err
}
func (v *FTPVFS) Clone() vfs.VFS {
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.session
	if session == nil && v.conn != nil {
		session = &ftpSession{conn: v.conn, refs: 1}
		v.session = session
	}
	if session != nil {
		session.retain()
	}
	return &FTPVFS{
		parent: v.parent, conn: v.conn, session: session, cwd: v.cwd,
		title: v.title, decoder: v.decoder, encoder: v.encoder,
	}
}

type ftpProvider struct{}

func (p *ftpProvider) Name() string  { return "NetFox-FTP" }
func (p *ftpProvider) Priority() int { return 100 }
func (p *ftpProvider) CanOpen(ctx context.Context, parent vfs.VFS, pth string) bool {
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
	return cfg.Type == "ftp"
}
func (p *ftpProvider) Open(ctx context.Context, parent vfs.VFS, pth string) (vfs.VFS, error) {
	w := parent.(*netFoxVFSWrapper)
	f, err := w.Open(ctx, pth)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // The configuration file is read-only.
	var cfg NetFoxConfig
	if err := json.NewDecoder(ctxReader{f, ctx}).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("netfox: decode FTP connection: %w", err)
	}
	port := cfg.Port
	if port == "" {
		port = "21"
	}
	timeout := 15
	if cfg.Timeout != "" {
		if t, err := strconv.Atoi(cfg.Timeout); err == nil && t > 0 {
			timeout = t
		}
	}
	res, err := NewFTPVFS(parent, cfg.Host, port, cfg.User, cfg.Pass, timeout, cfg.Options, cfg.Codepage, cfg.Proxy())
	if err != nil {
		return nil, err
	}
	return res, nil
}

type ftpProtocolHandler struct{}

func (ph *ftpProtocolHandler) Prefix() string      { return "ftp" }
func (ph *ftpProtocolHandler) DefaultPort() string { return "21" }
func (ph *ftpProtocolHandler) BuildExtraUI(cfg *NetFoxConfig, x, y, w, h int) (vtui.UIElement, func()) {
	passive := true
	if val, ok := cfg.Options["Passive"]; ok {
		passive = (val == "true")
	}

	chk := vtui.NewCheckbox(x, y, vtui.Msg("NetFox.PassiveMode"), false)
	if passive {
		chk.State = 1
	} else {
		chk.State = 0
	}

	save := func() {
		if cfg.Options == nil {
			cfg.Options = make(map[string]string)
		}
		if chk.State == 1 {
			cfg.Options["Passive"] = "true"
		} else {
			cfg.Options["Passive"] = "false"
		}
	}
	return chk, save
}

func init() {
	vfs.RegisterProvider(&ftpProvider{})
	RegisterProtocol(&ftpProtocolHandler{})
}

type ftpFileWrapper struct {
	*os.File
	size int64
	path string
}

func (w *ftpFileWrapper) Size() int64 { return w.size }
func (w *ftpFileWrapper) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	return w.File.ReadAt(p, off)
}
func (w *ftpFileWrapper) Read(ctx context.Context, p []byte) (int, error) { return w.File.Read(p) }
func (w *ftpFileWrapper) Close() error                                    { return errors.Join(w.File.Close(), os.Remove(w.path)) }
