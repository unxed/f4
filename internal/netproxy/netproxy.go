// Package netproxy is the single place that answers "how does f4 reach the
// network". The updater, the plugin ring, the colorer downloader and the
// netfox site connections all go through it, so a proxy typed once in the
// settings covers every outgoing byte f4 sends.
//
// Settings are deliberately a plain value type: the app keeps one global set
// (Settings entered in Options), and anything that may want to deviate — a
// netfox connection sitting behind a different gateway — carries its own
// Settings with ModeGlobal meaning "whatever f4 is configured to use".
package netproxy

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
	xproxy "golang.org/x/net/proxy"
)

// Proxy modes. ModeGlobal is the zero value on purpose: a freshly added
// netfox connection, or one saved by an older f4, inherits the app-wide
// setting instead of silently going direct. The app-wide setting in turn
// defaults to ModeSystem, which is exactly what f4 did before this package
// existed — net/http honours HTTP_PROXY/HTTPS_PROXY/NO_PROXY on its own.
const (
	ModeGlobal = 0 // inherit the app-wide settings (per-connection only)
	ModeSystem = 1 // honour the proxy environment variables
	ModeDirect = 2 // no proxy at all, ignore the environment
	ModeHTTP   = 3 // explicit HTTP proxy (CONNECT for raw TCP)
	ModeSOCKS5 = 4 // explicit SOCKS5 proxy
)

// Settings describes one proxy. User/Pass are optional: an empty User means
// the proxy is used without authentication.
type Settings struct {
	Mode int
	Host string
	Port string
	User string
	Pass string
}

var (
	mu     sync.RWMutex
	global = Settings{Mode: ModeSystem}
)

// SetGlobal installs the app-wide settings; f4 calls it whenever the config
// is loaded or saved.
func SetGlobal(s Settings) {
	if s.Mode == ModeGlobal {
		// Nothing to inherit from at the top level.
		s.Mode = ModeSystem
	}
	mu.Lock()
	global = s
	mu.Unlock()
}

// Global returns the app-wide settings.
func Global() Settings {
	mu.RLock()
	defer mu.RUnlock()
	return global
}

// Resolve turns a possibly-inheriting Settings into a concrete one.
func Resolve(s Settings) Settings {
	if s.Mode == ModeGlobal {
		return Global()
	}
	return s
}

// Addr is the proxy endpoint, host:port, with the customary default port
// filled in when the user did not type one.
func (s Settings) Addr() string {
	port := strings.TrimSpace(s.Port)
	if port == "" {
		if s.Mode == ModeSOCKS5 {
			port = "1080"
		} else {
			port = "3128"
		}
	}
	return net.JoinHostPort(strings.TrimSpace(s.Host), port)
}

// Explicit reports whether these settings name a proxy host of their own.
func (s Settings) Explicit() bool {
	return (s.Mode == ModeHTTP || s.Mode == ModeSOCKS5) && strings.TrimSpace(s.Host) != ""
}

// URL builds the proxy URL, credentials included. net/http understands both
// the http and the socks5 scheme here and sends Proxy-Authorization itself.
func (s Settings) URL() *url.URL {
	if !s.Explicit() {
		return nil
	}
	scheme := "http"
	if s.Mode == ModeSOCKS5 {
		scheme = "socks5"
	}
	u := &url.URL{Scheme: scheme, Host: s.Addr()}
	if s.User != "" {
		u.User = url.UserPassword(s.User, s.Pass)
	}
	return u
}

// Describe renders the settings for logs and dialogs. The password is never
// part of the result.
func (s Settings) Describe() string {
	switch s.Mode {
	case ModeGlobal:
		return "global"
	case ModeDirect:
		return "direct"
	case ModeSystem:
		return "system"
	case ModeHTTP, ModeSOCKS5:
		if !s.Explicit() {
			return "direct"
		}
		scheme := "http"
		if s.Mode == ModeSOCKS5 {
			scheme = "socks5"
		}
		auth := ""
		if s.User != "" {
			auth = s.User + "@"
		}
		return scheme + "://" + auth + s.Addr()
	}
	return "direct"
}

// proxyFunc is what net/http wants for Transport.Proxy.
func systemProxyURL(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	u, err := httpproxy.FromEnvironment().ProxyFunc()(req.URL)
	if err != nil || u == nil {
		for _, env := range []string{"ALL_PROXY", "all_proxy", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
			if val := os.Getenv(env); val != "" {
				if parsed, parseErr := url.Parse(val); parseErr == nil && parsed != nil {
					return parsed, nil
				}
			}
		}
	}
	return u, err
}

// proxyFunc is what net/http wants for Transport.Proxy.
func (s Settings) proxyFunc() func(*http.Request) (*url.URL, error) {
	switch s.Mode {
	case ModeSystem:
		return func(req *http.Request) (*url.URL, error) {
			u, err := systemProxyURL(req)
			if u != nil && s.User != "" && u.User == nil {
				uCopy := *u
				uCopy.User = url.UserPassword(s.User, s.Pass)
				return &uCopy, nil
			}
			return u, err
		}
	case ModeHTTP, ModeSOCKS5:
		if u := s.URL(); u != nil {
			return func(*http.Request) (*url.URL, error) { return u, nil }
		}
	}
	return nil
}

// HTTPClient builds a client that talks through these settings. Everything in
// f4 that downloads over HTTP — releases, the plugin catalog, colorer schemes
// — should get its client here rather than reaching for http.DefaultClient.
func (s Settings) HTTPClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy:                 s.proxyFunc(),
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}

// HTTPClient is the app-wide shorthand for Global().HTTPClient.
func HTTPClient(timeout time.Duration) *http.Client {
	return Global().HTTPClient(timeout)
}

// tcpKeepAlive is the probing schedule for every long-lived TCP connection
// netfox opens. A frozen peer — a suspended VM, a yanked cable, a NAT entry
// that quietly expired — leaves the socket ESTABLISHED with nothing on the
// wire to trip a timeout, so without probes a read blocks forever. The old
// Dialer.KeepAlive only set the idle time and left the probe interval and
// count to the OS, where the defaults (Linux: 75s × 9) take over ten minutes
// to declare a peer dead. With all three pinned the kernel gives up after
// Idle + Interval×Count, about 35 seconds, which is quick enough for a file
// manager and still far above any sane round trip.
//
// These probes only ever test the hop the socket is on. Through an HTTP or
// SOCKS proxy that hop ends at the proxy, so a dead target behind a healthy
// proxy is invisible here; the SSH keepalive in netfox covers that case.
var tcpKeepAlive = net.KeepAliveConfig{
	Enable:   true,
	Idle:     15 * time.Second,
	Interval: 5 * time.Second,
	Count:    4,
}

// directDialer is the dialer every raw TCP connection starts from, whether
// it goes straight to the target or to a proxy in front of it.
func directDialer() *net.Dialer {
	return &net.Dialer{Timeout: 30 * time.Second, KeepAliveConfig: tcpKeepAlive}
}

// DialContext opens a plain TCP connection through these settings, which is
// what the netfox protocols need: SSH and FTP control connections are not
// HTTP, so they reach the proxy through CONNECT or SOCKS5 instead.
func (s Settings) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	direct := directDialer()

	switch s.Mode {
	case ModeDirect:
		return direct.DialContext(ctx, network, addr)

	case ModeSystem:
		if reqURL, err := url.Parse("https://" + addr); err == nil {
			if u, _ := systemProxyURL(&http.Request{URL: reqURL}); u != nil {
				user := s.User
				pass := s.Pass
				if u.User != nil {
					user = u.User.Username()
					pass, _ = u.User.Password()
				}
				host := u.Hostname()
				port := u.Port()
				mode := ModeHTTP
				if u.Scheme == "socks5" {
					mode = ModeSOCKS5
				}
				if host != "" {
					return Settings{
						Mode: mode,
						Host: host,
						Port: port,
						User: user,
						Pass: pass,
					}.DialContext(ctx, network, addr)
				}
			}
		}
		d := xproxy.FromEnvironment()
		if cd, ok := d.(xproxy.ContextDialer); ok {
			return cd.DialContext(ctx, network, addr)
		}
		return d.Dial(network, addr)

	case ModeSOCKS5:
		if !s.Explicit() {
			return direct.DialContext(ctx, network, addr)
		}
		var auth *xproxy.Auth
		if s.User != "" {
			auth = &xproxy.Auth{User: s.User, Password: s.Pass}
		}
		d, err := xproxy.SOCKS5("tcp", s.Addr(), auth, direct)
		if err != nil {
			return nil, err
		}
		if cd, ok := d.(xproxy.ContextDialer); ok {
			return cd.DialContext(ctx, network, addr)
		}
		return d.Dial(network, addr)

	case ModeHTTP:
		if !s.Explicit() {
			return direct.DialContext(ctx, network, addr)
		}
		return (&connectDialer{addr: s.Addr(), user: s.User, pass: s.Pass, forward: direct}).DialContext(ctx, network, addr)
	}

	return direct.DialContext(ctx, network, addr)
}

// Dial is the context-free form, handy for libraries that ask for a dial
// function of the classic shape.
func (s Settings) Dial(network, addr string) (net.Conn, error) {
	return s.DialContext(context.Background(), network, addr)
}

// connectDialer tunnels TCP through an HTTP proxy with the CONNECT verb,
// optionally authenticating with Basic credentials.
type connectDialer struct {
	addr    string
	user    string
	pass    string
	forward *net.Dialer
}

func (d *connectDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return nil, fmt.Errorf("netproxy: an HTTP proxy cannot carry %q traffic", network)
	}

	conn, err := d.forward.DialContext(ctx, "tcp", d.addr)
	if err != nil {
		return nil, fmt.Errorf("netproxy: cannot reach proxy %s: %w", d.addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	req.Header.Set("User-Agent", "f4")
	if d.user != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(d.user + ":" + d.pass))
		req.Header.Set("Proxy-Authorization", "Basic "+cred)
	}
	if err := req.Write(conn); err != nil {
		_ = conn.Close() // Preserve the CONNECT write failure.
		return nil, fmt.Errorf("netproxy: CONNECT to %s failed: %w", addr, err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = conn.Close() // Preserve the response-read failure.
		return nil, fmt.Errorf("netproxy: proxy %s gave no answer: %w", d.addr, err)
	}
	if resp.StatusCode != http.StatusOK {
		// Only a refusal has a body worth looking at; on success the rest
		// of the stream is the tunnel and must not be touched.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close() // Response-body cleanup is best effort.
		_ = conn.Close()      // The proxy refusal remains authoritative.
		if resp.StatusCode == http.StatusProxyAuthRequired {
			return nil, fmt.Errorf("netproxy: proxy %s wants credentials (407)", d.addr)
		}
		return nil, fmt.Errorf("netproxy: proxy %s refused CONNECT to %s: %s", d.addr, addr, resp.Status)
	}

	// The deadline was only meant to bound the handshake.
	_ = conn.SetDeadline(time.Time{})
	// The proxy is allowed to pack the first bytes of the tunnel into the
	// same packet as its 200, so hand out a conn that replays what the
	// reader has already swallowed.
	return &bufferedConn{Conn: conn, r: br}, nil
}

// bufferedConn hands out bytes the CONNECT handshake read ahead of time.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (d *connectDialer) Dial(network, addr string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, addr)
}

func init() {
	// Teach x/net/proxy about http proxies so that ALL_PROXY=http://... is
	// honoured for raw TCP in ModeSystem, the same way curl treats it.
	reg := func(u *url.URL, fwd xproxy.Dialer) (xproxy.Dialer, error) {
		d := &connectDialer{addr: u.Host, forward: directDialer()}
		if u.User != nil {
			d.user = u.User.Username()
			d.pass, _ = u.User.Password()
		}
		return d, nil
	}
	xproxy.RegisterDialerType("http", reg)
	xproxy.RegisterDialerType("https", reg)
}

// --- password storage -------------------------------------------------
//
// The proxy password lands in config.ini, a plain text file, so it gets the
// same treatment netfox gives site passwords: AES-GCM under a key derived
// from the machine and the user. That is obfuscation, not security — it
// stops a shared config or a shoulder-surfed file from handing the password
// over, and nothing more.

const secretPrefix = "~ENC~"

// keyOverride exists for the tests.
var keyOverride []byte

func secretKey() []byte {
	if keyOverride != nil {
		return keyOverride
	}
	host, _ := os.Hostname()
	username := ""
	if usr, err := user.Current(); err == nil && usr != nil {
		username = usr.Username
	}
	hash := sha256.Sum256([]byte(host + ":" + username + ":f4-proxy"))
	return hash[:]
}

// EncodeSecret obfuscates a password for storage in config.ini.
func EncodeSecret(plain string) string {
	if plain == "" {
		return ""
	}
	block, err := aes.NewCipher(secretKey())
	if err != nil {
		return plain
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return plain
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return plain
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return secretPrefix + base64.StdEncoding.EncodeToString(sealed)
}

// DecodeSecret reverses EncodeSecret. A value that was never encoded — a
// password typed into config.ini by hand — is returned as it is.
func DecodeSecret(stored string) string {
	if !strings.HasPrefix(stored, secretPrefix) {
		return stored
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, secretPrefix))
	if err != nil {
		return ""
	}
	block, err := aes.NewCipher(secretKey())
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return ""
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return ""
	}
	return string(plain)
}
