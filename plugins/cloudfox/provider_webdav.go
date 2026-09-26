package cloudfox

import (
	"context"
	"crypto/md5" // #nosec G501 -- required for RFC 7616 compatibility with legacy DAV servers.
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/unxed/f4/vfs"
)

type WebDAVSettings struct {
	BaseURL             string `json:"base_url"`
	Root                string `json:"root,omitempty"`
	Auth                string `json:"auth,omitempty"` // anonymous, basic, digest, bearer
	Username            string `json:"username,omitempty"`
	CustomCA            string `json:"custom_ca,omitempty"`
	AllowInsecureDigest bool   `json:"allow_insecure_digest,omitempty"`
}

type WebDAVFactory struct {
	HTTPClient *http.Client
}

var errWebDAVMutationRedirect = errors.New("cloudfox: WebDAV mutation redirect refused")

func safeWebDAVMutationTransportError(err error) error {
	if errors.Is(err, errWebDAVMutationRedirect) {
		// net/http wraps CheckRedirect failures in url.Error and copies the
		// server-controlled Location into its URL field. A redirect may contain a
		// bearer token or signed query, so retain only the stable sentinel.
		return errWebDAVMutationRedirect
	}
	return err
}

func (f *WebDAVFactory) Provider() ProviderType { return ProviderWebDAV }

func (f *WebDAVFactory) settings(c Connection) (WebDAVSettings, *url.URL, error) {
	var settings WebDAVSettings
	if err := jsonUnmarshalSettings(c.Settings, &settings, "WebDAV"); err != nil {
		return settings, nil, err
	}
	settings.BaseURL = strings.TrimSpace(settings.BaseURL)
	base, err := url.Parse(settings.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return settings, nil, errors.New("cloudfox: invalid WebDAV base URL")
	}
	escapedBasePath := strings.ToLower(base.EscapedPath())
	if strings.Contains(escapedBasePath, "%2f") || strings.Contains(escapedBasePath, "%5c") ||
		strings.ContainsRune(base.Path, '\\') || strings.Contains(strings.TrimSuffix(base.Path, "/"), "//") ||
		webDAVPathHasControl(base.Path) || webDAVPathHasDotSegment(escapedBasePath) {
		return settings, nil, errors.New("cloudfox: invalid or ambiguous WebDAV base URL path")
	}
	settings.Auth = strings.ToLower(strings.TrimSpace(settings.Auth))
	if settings.Auth == "" {
		settings.Auth = "basic"
	}
	switch settings.Auth {
	case "anonymous", "basic", "digest", "bearer":
	default:
		return settings, nil, fmt.Errorf("cloudfox: unsupported WebDAV authentication %q", settings.Auth)
	}
	if base.Scheme != "https" && settings.Auth != "anonymous" {
		if settings.Auth != "digest" || !settings.AllowInsecureDigest {
			return settings, nil, errors.New("cloudfox: HTTPS is required for WebDAV Basic/Bearer; HTTP Digest requires explicit confirmation")
		}
	}
	root := strings.TrimSpace(strings.ReplaceAll(settings.Root, "\\", "/"))
	if strings.ContainsRune(root, '\x00') {
		return settings, nil, errors.New("cloudfox: WebDAV root contains NUL")
	}
	if root == "" {
		root = "/"
	}
	root = path.Clean("/" + strings.TrimPrefix(root, "/"))
	settings.Root = root
	settings.CustomCA = strings.TrimSpace(settings.CustomCA)
	base.Path = strings.TrimSuffix(base.Path, "/")
	return settings, base, nil
}

func webDAVPathHasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func webDAVPathHasDotSegment(escapedPath string) bool {
	for _, segment := range strings.Split(escapedPath, "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil || decoded == "." || decoded == ".." {
			return true
		}
	}
	return false
}

func jsonUnmarshalSettings(raw []byte, dst any, provider string) error {
	if len(raw) == 0 {
		return fmt.Errorf("cloudfox: %s settings are required", provider)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("cloudfox: decode %s settings: %w", provider, err)
	}
	return nil
}

func (f *WebDAVFactory) Validate(c Connection) error {
	_, _, err := f.settings(c)
	return err
}

func (f *WebDAVFactory) Open(ctx context.Context, c Connection, secrets SecretValues) (Backend, error) {
	settings, base, err := f.settings(c)
	if err != nil {
		return nil, err
	}
	password := secrets["password"]
	bearer := secrets["bearer_token"]
	if settings.Auth == "bearer" && strings.TrimSpace(bearer) == "" {
		return nil, ErrAuthenticationRequired
	}
	if (settings.Auth == "basic" || settings.Auth == "digest") && (settings.Username == "" || password == "") {
		return nil, ErrAuthenticationRequired
	}
	var customCAPEM []byte
	if settings.CustomCA != "" {
		customCAPEM, err = os.ReadFile(settings.CustomCA)
		if err != nil {
			return nil, fmt.Errorf("cloudfox: read WebDAV custom CA: %w", err)
		}
	}
	// Credentials can be scope-checked before this factory call. Verify the
	// exact bytes used below again so a writable CA path or symlink cannot be
	// swapped between credential verification and TLS client construction.
	caFingerprint := customCAContentsFingerprint(customCAPEM)
	if err := verifyCredentialScopeWithCAFingerprint(c, secrets, false, &caFingerprint); err != nil {
		return nil, err
	}
	client := f.HTTPClient
	if client == nil {
		client, err = webDAVHTTPClientWithCAPEM(customCAPEM)
		if err != nil {
			return nil, err
		}
	} else {
		clone := *client
		client = &clone
	}
	baseTransport := client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	switch settings.Auth {
	case "anonymous":
	case "basic":
		client.Transport = &webDAVStaticAuthTransport{base: baseTransport, basicUser: settings.Username, basicPassword: password}
	case "bearer":
		client.Transport = &webDAVStaticAuthTransport{base: baseTransport, bearer: bearer}
	case "digest":
		client.Transport = &webDAVDigestTransport{base: baseTransport, username: settings.Username, password: password}
	}
	allowedRoot := path.Clean(path.Join(base.Path, settings.Root))
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return errors.New("cloudfox: WebDAV redirect has no originating request")
		}
		// Reject mutation redirects with an ordinary error instead of
		// http.ErrUseLastResponse. For 307/308 net/http has already opened a
		// replay body before CheckRedirect runs; its ErrUseLastResponse special
		// case returns the response without closing that unsent body. On Windows
		// the leaked descriptor pins our upload spool file indefinitely. An
		// ordinary error makes net/http close the replay body while retaining the
		// invariant that an uncertain mutation is never retried at another URL.
		initialMethod := via[0].Method
		if initialMethod != http.MethodGet && initialMethod != http.MethodHead && initialMethod != "PROPFIND" {
			return errWebDAVMutationRedirect
		}
		if !sameWebDAVOrigin(req.URL, base) {
			return http.ErrUseLastResponse
		}
		redirectPath := path.Clean(req.URL.Path)
		if redirectPath != allowedRoot && !strings.HasPrefix(redirectPath, strings.TrimSuffix(allowedRoot, "/")+"/") {
			return http.ErrUseLastResponse
		}
		escapedPath := strings.ToLower(req.URL.EscapedPath())
		if strings.Contains(escapedPath, "%2f") || strings.Contains(escapedPath, "%5c") {
			return http.ErrUseLastResponse
		}
		if len(via) >= 5 {
			return errors.New("cloudfox: too many WebDAV redirects")
		}
		// Go rewrites every non-GET/HEAD request to GET for 301, 302 and 303.
		// Never follow redirects for DAV mutations: even a method-preserving
		// 307/308 can move a write outside the resource the user selected. Read
		// redirects are allowed only while the method remains unchanged.
		if req.Method != via[len(via)-1].Method {
			return http.ErrUseLastResponse
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &webDAVBackend{client: client, base: base, rootPath: settings.Root, downloads: newWebDAVDownloadCache()}, nil
}

func webDAVHTTPClient(customCA string) (*http.Client, error) {
	var customCAPEM []byte
	if strings.TrimSpace(customCA) != "" {
		var err error
		customCAPEM, err = os.ReadFile(customCA)
		if err != nil {
			return nil, fmt.Errorf("cloudfox: read WebDAV custom CA: %w", err)
		}
	}
	return webDAVHTTPClientWithCAPEM(customCAPEM)
}

func webDAVHTTPClientWithCAPEM(customCAPEM []byte) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if customCAPEM != nil {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(customCAPEM) {
			return nil, errors.New("cloudfox: WebDAV custom CA does not contain a certificate")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	return &http.Client{Transport: transport}, nil
}

type webDAVStaticAuthTransport struct {
	base          http.RoundTripper
	basicUser     string
	basicPassword string
	bearer        string
}

func (t *webDAVStaticAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if t.bearer != "" {
		clone.Header.Set("Authorization", "Bearer "+t.bearer)
	} else {
		clone.SetBasicAuth(t.basicUser, t.basicPassword)
	}
	return t.base.RoundTrip(clone)
}

func (t *webDAVStaticAuthTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

type digestChallenge struct {
	realm     string
	nonce     string
	opaque    string
	algorithm string
	qop       string
	stale     bool
}

type webDAVDigestTransport struct {
	base     http.RoundTripper
	username string
	password string
	mu       sync.Mutex
	current  *digestChallenge
	nonceCnt uint32
}

func (t *webDAVDigestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	challenge, nonceCount := t.cachedChallenge()
	hadChallenge := challenge != nil
	// Before a challenge is known, send a replay body without progress
	// instrumentation. If the server challenges the request, the still-unused
	// original body is sent on the authenticated retry and reports progress
	// exactly once.
	resp, err := t.roundTrip(req, challenge, nonceCount, challenge != nil)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	parsed, parseErr := parseDigestChallenge(resp.Header.Values("WWW-Authenticate"))
	if parseErr != nil {
		_ = resp.Body.Close() // Response-body cleanup is best effort.
		return nil, parseErr
	}
	_ = resp.Body.Close() // Response-body cleanup is best effort.
	t.setChallenge(parsed)
	challenge, nonceCount = t.cachedChallenge()
	return t.roundTrip(req, challenge, nonceCount, !hadChallenge)
}

func (t *webDAVDigestTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (t *webDAVDigestTransport) cachedChallenge() (*digestChallenge, uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return nil, 0
	}
	t.nonceCnt++
	copy := *t.current
	return &copy, t.nonceCnt
}

func (t *webDAVDigestTransport) setChallenge(challenge *digestChallenge) {
	t.mu.Lock()
	t.current = challenge
	t.nonceCnt = 0
	t.mu.Unlock()
}

func (t *webDAVDigestTransport) roundTrip(req *http.Request, challenge *digestChallenge, nonceCount uint32, useOriginalBody bool) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if req.Body != nil && req.GetBody != nil && !useOriginalBody {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		clone.Body = body
	}
	if challenge != nil {
		authorization, err := digestAuthorization(t.username, t.password, req, challenge, nonceCount)
		if err != nil {
			return nil, err
		}
		clone.Header.Set("Authorization", authorization)
	} else if req.Method == http.MethodPut && clone.Body != nil && clone.ContentLength > 0 && clone.Header.Get("Expect") == "" {
		// Give a Digest server an opportunity to send its initial 401 before a
		// large upload body crosses the network. Some servers still drain the
		// body, so replay bodies remain progress-instrumented below as well.
		clone.Header.Set("Expect", "100-continue")
	}
	return t.base.RoundTrip(clone)
}

func parseDigestChallenge(headers []string) (*digestChallenge, error) {
	var best *digestChallenge
	bestRank := -1
	var candidateErr error
	foundDigest := false
	for _, header := range headers {
		for _, raw := range digestChallengeSegments(header) {
			foundDigest = true
			params := parseAuthParams(raw)
			challenge := &digestChallenge{
				realm:     params["realm"],
				nonce:     params["nonce"],
				opaque:    params["opaque"],
				algorithm: strings.ToUpper(params["algorithm"]),
				stale:     strings.EqualFold(params["stale"], "true"),
			}
			if challenge.algorithm == "" {
				challenge.algorithm = "MD5"
			}
			qopValue := strings.TrimSpace(params["qop"])
			for _, qop := range strings.Split(qopValue, ",") {
				if strings.EqualFold(strings.TrimSpace(qop), "auth") {
					challenge.qop = "auth"
					break
				}
			}
			if challenge.realm == "" || challenge.nonce == "" {
				candidateErr = errors.New("cloudfox: incomplete WebDAV Digest challenge")
				continue
			}
			if qopValue != "" && challenge.qop == "" {
				candidateErr = errors.New("cloudfox: WebDAV Digest server does not offer qop=auth")
				continue
			}
			if _, err := digestHash(challenge.algorithm, ""); err != nil {
				candidateErr = err
				continue
			}
			rank := digestAlgorithmRank(challenge.algorithm) * 2
			if challenge.qop == "auth" {
				rank++
			}
			if rank > bestRank {
				copy := *challenge
				best, bestRank = &copy, rank
			}
		}
	}
	if best != nil {
		return best, nil
	}
	if foundDigest && candidateErr != nil {
		return nil, candidateErr
	}
	return nil, errors.New("cloudfox: WebDAV server did not provide a Digest challenge")
}

func digestChallengeSegments(header string) []string {
	lower := strings.ToLower(header)
	var starts []int
	inQuote, escaped := false, false
	for index := 0; index < len(header); index++ {
		if inQuote {
			if escaped {
				escaped = false
				continue
			}
			switch header[index] {
			case '\\':
				escaped = true
			case '"':
				inQuote = false
			}
			continue
		}
		if header[index] == '"' {
			inQuote = true
			continue
		}
		if !strings.HasPrefix(lower[index:], "digest ") {
			continue
		}
		previous := index - 1
		for previous >= 0 && (header[previous] == ' ' || header[previous] == '\t') {
			previous--
		}
		if previous < 0 || header[previous] == ',' {
			starts = append(starts, index)
			index += len("digest ") - 1
		}
	}
	segments := make([]string, 0, len(starts))
	for index, start := range starts {
		end := len(header)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		raw := strings.Trim(strings.TrimSpace(header[start+len("digest "):end]), ",")
		if raw != "" {
			segments = append(segments, raw)
		}
	}
	return segments
}

func digestAlgorithmRank(algorithm string) int {
	base := strings.TrimSuffix(strings.ToUpper(algorithm), "-SESS")
	rank := 0
	switch base {
	case "MD5":
		rank = 1
	case "SHA-256":
		rank = 3
	case "SHA-512-256":
		rank = 5
	}
	if rank != 0 && strings.HasSuffix(strings.ToUpper(algorithm), "-SESS") {
		rank++
	}
	return rank
}

func parseAuthParams(raw string) map[string]string {
	result := make(map[string]string)
	for len(raw) > 0 {
		raw = strings.TrimLeft(raw, " ,\t")
		eq := strings.IndexByte(raw, '=')
		if eq <= 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(raw[:eq]))
		raw = strings.TrimLeft(raw[eq+1:], " \t")
		var value string
		if strings.HasPrefix(raw, "\"") {
			raw = raw[1:]
			var builder strings.Builder
			escaped := false
			consumed := 0
			for i, r := range raw {
				consumed = i + 1
				if escaped {
					builder.WriteRune(r)
					escaped = false
					continue
				}
				if r == '\\' {
					escaped = true
					continue
				}
				if r == '"' {
					break
				}
				builder.WriteRune(r)
			}
			value = builder.String()
			raw = raw[consumed:]
		} else {
			comma := strings.IndexByte(raw, ',')
			if comma < 0 {
				value, raw = strings.TrimSpace(raw), ""
			} else {
				value, raw = strings.TrimSpace(raw[:comma]), raw[comma+1:]
			}
		}
		result[key] = value
	}
	return result
}

func digestHash(algorithm, value string) (string, error) {
	switch strings.TrimSuffix(strings.ToUpper(algorithm), "-SESS") {
	case "MD5":
		sum := md5.Sum([]byte(value)) // #nosec G401 -- protocol interoperability, not password storage.
		return hex.EncodeToString(sum[:]), nil
	case "SHA-256":
		sum := sha256.Sum256([]byte(value))
		return hex.EncodeToString(sum[:]), nil
	case "SHA-512-256":
		sum := sha512.Sum512_256([]byte(value))
		return hex.EncodeToString(sum[:]), nil
	default:
		return "", fmt.Errorf("cloudfox: unsupported WebDAV Digest algorithm %q", algorithm)
	}
}

func digestAuthorization(username, password string, req *http.Request, challenge *digestChallenge, nonceCount uint32) (string, error) {
	cnonceBytes := make([]byte, 16)
	if _, err := rand.Read(cnonceBytes); err != nil {
		return "", err
	}
	cnonce := hex.EncodeToString(cnonceBytes)
	uri := req.URL.RequestURI()
	ha1, err := digestHash(challenge.algorithm, username+":"+challenge.realm+":"+password)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(strings.ToUpper(challenge.algorithm), "-SESS") {
		ha1, err = digestHash(challenge.algorithm, ha1+":"+challenge.nonce+":"+cnonce)
		if err != nil {
			return "", err
		}
	}
	ha2, err := digestHash(challenge.algorithm, req.Method+":"+uri)
	if err != nil {
		return "", err
	}
	nc := fmt.Sprintf("%08x", nonceCount)
	responseInput := ha1 + ":" + challenge.nonce + ":" + ha2
	if challenge.qop != "" {
		responseInput = ha1 + ":" + challenge.nonce + ":" + nc + ":" + cnonce + ":" + challenge.qop + ":" + ha2
	}
	response, err := digestHash(challenge.algorithm, responseInput)
	if err != nil {
		return "", err
	}
	quote := func(value string) string {
		return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	}
	parts := []string{
		`Digest username="` + quote(username) + `"`,
		`realm="` + quote(challenge.realm) + `"`,
		`nonce="` + quote(challenge.nonce) + `"`,
		`uri="` + quote(uri) + `"`,
		`response="` + response + `"`,
		`algorithm=` + challenge.algorithm,
	}
	if challenge.opaque != "" {
		parts = append(parts, `opaque="`+quote(challenge.opaque)+`"`)
	}
	if challenge.qop != "" {
		parts = append(parts, `qop=`+challenge.qop, `nc=`+nc, `cnonce="`+cnonce+`"`)
	} else if strings.HasSuffix(strings.ToUpper(challenge.algorithm), "-SESS") {
		parts = append(parts, `cnonce="`+cnonce+`"`)
	}
	return strings.Join(parts, ", "), nil
}

type webDAVBackend struct {
	client    *http.Client
	base      *url.URL
	rootPath  string
	downloads *webDAVDownloadCache
}

type webDAVDownloadCacheEntry struct {
	fingerprint string
	path        string
	size        int64
	readers     int
	retired     bool
	lastUsed    uint64
}

const (
	defaultWebDAVCacheEntries = 8
	defaultWebDAVCacheBytes   = int64(2 << 30)
)

// webDAVDownloadCache keeps fully materialized responses for the lifetime of
// one backend session. This is particularly important for servers such as
// Apache mod_dav that expose only weak ETags and therefore cannot safely back
// arbitrary range reads directly.
type webDAVDownloadCache struct {
	mu         sync.Mutex
	entries    map[string]*webDAVDownloadCacheEntry
	retired    map[*webDAVDownloadCacheEntry]struct{}
	closed     bool
	bytes      int64
	clock      uint64
	maxEntries int
	maxBytes   int64
}

func newWebDAVDownloadCache() *webDAVDownloadCache {
	return &webDAVDownloadCache{
		entries:    make(map[string]*webDAVDownloadCacheEntry),
		retired:    make(map[*webDAVDownloadCacheEntry]struct{}),
		maxEntries: defaultWebDAVCacheEntries,
		maxBytes:   defaultWebDAVCacheBytes,
	}
}

type webDAVCachedReader struct {
	*os.File
	size  int64
	cache *webDAVDownloadCache
	entry *webDAVDownloadCacheEntry
	once  sync.Once
	err   error
}

func (r *webDAVCachedReader) Size() int64 { return r.size }
func (r *webDAVCachedReader) LocalPath() (string, bool) {
	if r.File == nil || r.Name() == "" {
		return "", false
	}
	return r.Name(), true
}
func (r *webDAVCachedReader) Read(ctx context.Context, p []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return r.File.Read(p)
}
func (r *webDAVCachedReader) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return r.File.ReadAt(p, off)
}

func (r *webDAVCachedReader) Close() error {
	r.once.Do(func() {
		r.err = r.File.Close()
		if r.cache != nil {
			r.cache.release(r.entry)
		}
	})
	return r.err
}

// retireLocked removes an entry from future cache lookups. If no open reader
// still owns it, the returned path can be deleted immediately; on Windows an
// open cached file cannot be unlinked, so active entries are deleted by the
// last reader's Close instead.
func (c *webDAVDownloadCache) retireLocked(entry *webDAVDownloadCacheEntry) string {
	if entry == nil || entry.retired {
		return ""
	}
	entry.retired = true
	if entry.readers == 0 {
		return entry.path
	}
	c.retired[entry] = struct{}{}
	return ""
}

func (c *webDAVDownloadCache) touchLocked(entry *webDAVDownloadCacheEntry) {
	c.clock++
	entry.lastUsed = c.clock
}

func (c *webDAVDownloadCache) evictLocked(protected *webDAVDownloadCacheEntry) []string {
	var removePaths []string
	overBudget := func() bool {
		return (c.maxEntries > 0 && len(c.entries) > c.maxEntries) ||
			(c.maxBytes > 0 && c.bytes > c.maxBytes)
	}
	for overBudget() {
		oldestKey := ""
		var oldest *webDAVDownloadCacheEntry
		for key, entry := range c.entries {
			if entry == protected || entry.readers != 0 {
				continue
			}
			if oldest == nil || entry.lastUsed < oldest.lastUsed {
				oldestKey, oldest = key, entry
			}
		}
		if oldest == nil {
			// Existing active readers remain cache-owned. If they consume the
			// whole budget, leave the just-materialized response private to its
			// caller rather than growing the session cache without bound.
			for key, entry := range c.entries {
				if entry == protected {
					oldestKey, oldest = key, entry
					break
				}
			}
		}
		if oldest == nil {
			break
		}
		delete(c.entries, oldestKey)
		c.bytes -= oldest.size
		if retiredPath := c.retireLocked(oldest); retiredPath != "" {
			removePaths = append(removePaths, retiredPath)
		}
	}
	return removePaths
}

func (c *webDAVDownloadCache) release(entry *webDAVDownloadCacheEntry) {
	if c == nil || entry == nil {
		return
	}
	removePath := ""
	c.mu.Lock()
	if entry.readers > 0 {
		entry.readers--
	}
	if entry.retired && entry.readers == 0 {
		delete(c.retired, entry)
		removePath = entry.path
	}
	c.mu.Unlock()
	if removePath != "" {
		_ = os.Remove(removePath)
	}
}

func newWebDAVCachedReader(file *os.File, cache *webDAVDownloadCache, entry *webDAVDownloadCacheEntry) *webDAVCachedReader {
	return &webDAVCachedReader{File: file, size: entry.size, cache: cache, entry: entry}
}

func (c *webDAVDownloadCache) open(key, fingerprint string) (vfs.ReadAtCloser, bool) {
	if c == nil || fingerprint == "" {
		return nil, false
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, false
	}
	entry, ok := c.entries[key]
	if !ok {
		c.mu.Unlock()
		return nil, false
	}
	if entry.fingerprint != fingerprint {
		delete(c.entries, key)
		c.bytes -= entry.size
		removePath := c.retireLocked(entry)
		c.mu.Unlock()
		if removePath != "" {
			_ = os.Remove(removePath)
		}
		return nil, false
	}
	f, err := os.Open(entry.path)
	if err != nil {
		delete(c.entries, key)
		c.bytes -= entry.size
		removePath := c.retireLocked(entry)
		c.mu.Unlock()
		if removePath != "" {
			_ = os.Remove(removePath)
		}
		return nil, false
	}
	entry.readers++
	c.touchLocked(entry)
	c.mu.Unlock()
	return newWebDAVCachedReader(f, c, entry), true
}

func (c *webDAVDownloadCache) install(key, fingerprint string, temp *providerTempReader) (vfs.ReadAtCloser, error) {
	if c == nil || fingerprint == "" {
		return temp, nil
	}
	if c.maxBytes > 0 && temp.size > c.maxBytes {
		return temp, nil
	}
	tempPath, size, err := temp.detach()
	if err != nil {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		f, openErr := os.Open(tempPath)
		if openErr != nil {
			_ = os.Remove(tempPath)
			return nil, openErr
		}
		return newProviderTempReader(f, tempPath, size), nil
	}
	var removePaths []string
	if current, ok := c.entries[key]; ok && current.fingerprint == fingerprint {
		if f, openErr := os.Open(current.path); openErr == nil {
			current.readers++
			c.touchLocked(current)
			c.mu.Unlock()
			_ = os.Remove(tempPath)
			return newWebDAVCachedReader(f, c, current), nil
		}
		delete(c.entries, key)
		c.bytes -= current.size
		if retiredPath := c.retireLocked(current); retiredPath != "" {
			removePaths = append(removePaths, retiredPath)
		}
	}
	if old, ok := c.entries[key]; ok {
		delete(c.entries, key)
		c.bytes -= old.size
		if retiredPath := c.retireLocked(old); retiredPath != "" {
			removePaths = append(removePaths, retiredPath)
		}
	}
	f, openErr := os.Open(tempPath)
	if openErr != nil {
		c.mu.Unlock()
		_ = os.Remove(tempPath)
		for _, retiredPath := range removePaths {
			_ = os.Remove(retiredPath)
		}
		return nil, openErr
	}
	entry := &webDAVDownloadCacheEntry{fingerprint: fingerprint, path: tempPath, size: size, readers: 1}
	c.touchLocked(entry)
	c.entries[key] = entry
	c.bytes += size
	removePaths = append(removePaths, c.evictLocked(entry)...)
	c.mu.Unlock()
	for _, retiredPath := range removePaths {
		_ = os.Remove(retiredPath)
	}
	return newWebDAVCachedReader(f, c, entry), nil
}

func (c *webDAVDownloadCache) invalidate(location string) {
	if c == nil {
		return
	}
	prefix := strings.TrimSuffix(location, "/") + "/"
	var removePaths []string
	c.mu.Lock()
	for key, entry := range c.entries {
		if key == location || strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
			c.bytes -= entry.size
			if retiredPath := c.retireLocked(entry); retiredPath != "" {
				removePaths = append(removePaths, retiredPath)
			}
		}
	}
	c.mu.Unlock()
	for _, retiredPath := range removePaths {
		_ = os.Remove(retiredPath)
	}
}

func (c *webDAVDownloadCache) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	var paths []string
	for _, entry := range c.entries {
		if retiredPath := c.retireLocked(entry); retiredPath != "" {
			paths = append(paths, retiredPath)
		}
	}
	c.entries = make(map[string]*webDAVDownloadCacheEntry)
	c.bytes = 0
	c.mu.Unlock()
	for _, cachedPath := range paths {
		_ = os.Remove(cachedPath)
	}
}

func webDAVDownloadFingerprint(entry RemoteEntry) string {
	revision := canonicalCacheETag(entry.Revision)
	// Last-Modified has only one-second precision on common DAV servers and is
	// not a representation validator. Same-size writes within that second must
	// never hit a stale session cache. A weak ETag is insufficient for ranged
	// If-Match reads, but it is still a valid whole-response cache identity.
	if revision == "" {
		return ""
	}
	modified := int64(0)
	if !entry.MTime.IsZero() {
		modified = entry.MTime.UTC().UnixNano()
	}
	return fmt.Sprintf("%s|%d|%d", revision, entry.Size, modified)
}

func webDAVFullResponseFingerprint(expectedRevision, fingerprint, responseRevision string) (string, error) {
	expected := cacheETag(expectedRevision)
	actual := cacheETag(responseRevision)
	if expected == "" || actual == "" {
		return "", nil
	}
	if !weakETagEqual(expected, actual) {
		return "", ErrRemoteObjectChanged
	}
	return fingerprint, nil
}

func mapWebDAVHTTPError(resp *http.Response, message string) error {
	err := mapProviderHTTPError(resp, message)
	// RFC 4918 uses 409 primarily for a missing intermediate collection, not
	// for an already-existing destination. Do not expose the generic
	// os.ErrExist mapping to WebDAV callers in that case.
	if resp != nil && resp.StatusCode == http.StatusConflict {
		var httpErr *providerHTTPError
		if errors.As(err, &httpErr) {
			return httpErr
		}
	}
	return err
}

func webDAVHTTPMutationError(operation string, resp *http.Response, message string) error {
	err := mapWebDAVHTTPError(resp, message)
	if resp == nil || resp.StatusCode < 400 || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500 {
		return &vfs.UnknownOperationStateError{Operation: operation, Err: err}
	}
	return err
}

func (b *webDAVBackend) Root() string { return "/" }

func (b *webDAVBackend) Normalize(location string) (string, error) {
	location = strings.ReplaceAll(location, "\\", "/")
	cleaned := path.Clean("/" + strings.TrimPrefix(location, "/"))
	if strings.ContainsRune(cleaned, '\x00') {
		return "", errors.New("cloudfox: WebDAV path contains NUL")
	}
	return cleaned, nil
}

func (b *webDAVBackend) Join(base string, elems ...string) string {
	joined, err := b.Normalize(path.Join(append([]string{base}, elems...)...))
	if err != nil {
		return base
	}
	return joined
}

func (b *webDAVBackend) Base(location string) string { return path.Base(location) }
func (b *webDAVBackend) Dir(location string) string {
	if location == "/" {
		return "/"
	}
	return path.Dir(location)
}
func (b *webDAVBackend) IsRoot(location string) bool {
	normalized, err := b.Normalize(location)
	return err == nil && normalized == "/"
}

func (b *webDAVBackend) urlFor(location string) (*url.URL, error) {
	location, err := b.Normalize(location)
	if err != nil {
		return nil, err
	}
	copy := *b.base
	serverPath := path.Join(copy.Path, b.rootPath, strings.TrimPrefix(location, "/"))
	if location == "/" && !strings.HasSuffix(serverPath, "/") {
		serverPath += "/"
	}
	copy.Path = serverPath
	copy.RawPath = ""
	return &copy, nil
}

func (b *webDAVBackend) locationFromHref(href string, requestURL *url.URL) (string, error) {
	u, err := url.Parse(href)
	if err != nil {
		return "", err
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: unsupported WebDAV href userinfo, query, or fragment", os.ErrPermission)
	}
	// Network-path references (//host/path) have an authority but no scheme,
	// so url.URL.IsAbs reports false. Resolve them before the origin check;
	// otherwise a foreign authority could be mistaken for a local absolute path.
	if u.Host != "" && !u.IsAbs() {
		expectedOrigin := requestURL
		if expectedOrigin == nil {
			expectedOrigin = b.base
		}
		if expectedOrigin == nil {
			return "", os.ErrPermission
		}
		u = expectedOrigin.ResolveReference(u)
	}
	if u.IsAbs() {
		expectedOrigin := requestURL
		if expectedOrigin == nil {
			expectedOrigin = b.base
		}
		if expectedOrigin == nil || !sameWebDAVOrigin(u, expectedOrigin) {
			return "", os.ErrPermission
		}
	}
	escapedPath := strings.ToLower(u.EscapedPath())
	if strings.Contains(escapedPath, "%2f") || strings.Contains(escapedPath, "%5c") || strings.ContainsRune(u.Path, '\\') {
		return "", fmt.Errorf("%w: WebDAV href contains an encoded or literal path separator", os.ErrPermission)
	}
	withoutTrailingSlash := strings.TrimSuffix(u.Path, "/")
	if strings.Contains(withoutTrailingSlash, "//") {
		return "", fmt.Errorf("%w: WebDAV href contains ambiguous empty path segments", os.ErrPermission)
	}
	if !u.IsAbs() && !strings.HasPrefix(u.Path, "/") && requestURL != nil {
		u = requestURL.ResolveReference(u)
	}
	serverRoot := path.Clean(path.Join(b.base.Path, b.rootPath))
	serverPath := path.Clean(u.Path)
	if serverPath != serverRoot && !strings.HasPrefix(serverPath, strings.TrimSuffix(serverRoot, "/")+"/") {
		return "", os.ErrPermission
	}
	relative := strings.TrimPrefix(serverPath, serverRoot)
	if relative == "" {
		relative = "/"
	}
	return b.Normalize(relative)
}

func sameWebDAVOrigin(first, second *url.URL) bool {
	if first == nil || second == nil || !strings.EqualFold(first.Scheme, second.Scheme) ||
		!strings.EqualFold(first.Hostname(), second.Hostname()) {
		return false
	}
	effectivePort := func(u *url.URL) string {
		if port := u.Port(); port != "" {
			return port
		}
		switch strings.ToLower(u.Scheme) {
		case "http":
			return "80"
		case "https":
			return "443"
		default:
			return ""
		}
	}
	return effectivePort(first) == effectivePort(second)
}

type davMultiStatus struct {
	Responses []davResponse `xml:"DAV: response"`
}

type davResponse struct {
	Href      string        `xml:"DAV: href"`
	Status    string        `xml:"DAV: status"`
	PropStats []davPropStat `xml:"DAV: propstat"`
}

type davPropStat struct {
	Status string  `xml:"DAV: status"`
	Prop   davProp `xml:"DAV: prop"`
}

type davProp struct {
	ResourceType struct {
		Collection *struct{} `xml:"DAV: collection"`
	} `xml:"DAV: resourcetype"`
	ContentLength string `xml:"DAV: getcontentlength"`
	LastModified  string `xml:"DAV: getlastmodified"`
	DisplayName   string `xml:"DAV: displayname"`
	ETag          string `xml:"DAV: getetag"`
}

const davPropFindBody = `<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:"><D:prop><D:displayname/><D:resourcetype/><D:getcontentlength/><D:getlastmodified/><D:getetag/></D:prop></D:propfind>`

const maxWebDAVPropfindResponse = 32 << 20

func (b *webDAVBackend) request(ctx context.Context, method, location string, body io.Reader) (*http.Request, error) {
	u, err := b.urlFor(location)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func davStatusCode(status string) int {
	parts := strings.Fields(status)
	if len(parts) < 2 {
		return 0
	}
	code, _ := strconv.Atoi(parts[1])
	return code
}

func davResponseEntry(response davResponse, location string) (RemoteEntry, error) {
	var prop davProp
	found := false
	for i := range response.PropStats {
		code := davStatusCode(response.PropStats[i].Status)
		if code >= 200 && code < 300 {
			found = true
			candidate := response.PropStats[i].Prop
			if candidate.ResourceType.Collection != nil {
				prop.ResourceType.Collection = candidate.ResourceType.Collection
			}
			if candidate.ContentLength != "" {
				prop.ContentLength = candidate.ContentLength
			}
			if candidate.LastModified != "" {
				prop.LastModified = candidate.LastModified
			}
			if candidate.DisplayName != "" {
				prop.DisplayName = candidate.DisplayName
			}
			if candidate.ETag != "" {
				prop.ETag = candidate.ETag
			}
		}
	}
	if !found {
		statusCode := davStatusCode(response.Status)
		switch statusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return RemoteEntry{}, fmt.Errorf("%w: WebDAV resource %s returned HTTP %d", os.ErrPermission, location, statusCode)
		case http.StatusNotFound:
			return RemoteEntry{}, fmt.Errorf("%w: WebDAV resource %s returned HTTP %d", os.ErrNotExist, location, statusCode)
		}
		return RemoteEntry{}, fmt.Errorf("cloudfox: WebDAV resource %s has no successful property status", location)
	}
	var size int64
	sizeKnown := prop.ResourceType.Collection != nil
	if strings.TrimSpace(prop.ContentLength) != "" {
		parsedSize, err := strconv.ParseInt(strings.TrimSpace(prop.ContentLength), 10, 64)
		if err != nil || parsedSize < 0 {
			return RemoteEntry{}, fmt.Errorf("cloudfox: invalid WebDAV content length %q for %s", prop.ContentLength, location)
		}
		size = parsedSize
		sizeKnown = true
	}
	modified, _ := http.ParseTime(prop.LastModified)
	name := prop.DisplayName
	if name == "" {
		name = path.Base(location)
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		name = path.Base(location)
	}
	if location == "/" && name == "." {
		name = "/"
	}
	return RemoteEntry{
		VFSItem: vfs.VFSItem{
			Name:     name,
			Size:     size,
			IsDir:    prop.ResourceType.Collection != nil,
			MTime:    modified,
			Revision: strongETag(strings.TrimSpace(prop.ETag)),
		},
		Location:     location,
		TransferName: name,
		SizeKnown:    sizeKnown,
		Revision:     strings.TrimSpace(prop.ETag),
	}, nil
}

func webDAVTrailingSlashRedirect(resp *http.Response) (*url.URL, bool) {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return nil, false
	}
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		return nil, false
	}
	location := strings.TrimSpace(resp.Header.Get("Location"))
	if location == "" {
		return nil, false
	}
	target, err := resp.Request.URL.Parse(location)
	if err != nil || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return nil, false
	}
	escapedPath := strings.ToLower(target.EscapedPath())
	if strings.Contains(escapedPath, "%2f") || strings.Contains(escapedPath, "%5c") || strings.ContainsRune(target.Path, '\\') {
		return nil, false
	}
	original := resp.Request.URL
	if !sameWebDAVOrigin(target, original) ||
		strings.HasSuffix(original.Path, "/") || target.Path != original.Path+"/" {
		return nil, false
	}
	return target, true
}

func (b *webDAVBackend) retryCanonicalCollectionRequest(req *http.Request, resp *http.Response) (*http.Response, error) {
	target, ok := webDAVTrailingSlashRedirect(resp)
	if !ok {
		return resp, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	retry := req.Clone(req.Context())
	retry.URL = target
	retry.Header = req.Header.Clone()
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		retry.Body = body
	}
	return b.client.Do(retry)
}

func (b *webDAVBackend) propfind(ctx context.Context, location, depth string, acceptLocation func(string) bool, onEntry func(RemoteEntry) error) error {
	req, err := b.request(ctx, "PROPFIND", location, strings.NewReader(davPropFindBody))
	if err != nil {
		return err
	}
	// Depth 1 is used only for a known collection. Send its collection-form
	// URI directly so strict servers such as Apache do not need a 301 roundtrip.
	if depth == "1" && !strings.HasSuffix(req.URL.Path, "/") {
		req.URL.Path += "/"
		req.URL.RawPath = ""
	}
	req.Header.Set("Depth", depth)
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	resp, err = b.retryCanonicalCollectionRequest(req, resp)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // Response-body cleanup is best effort.
	if resp.StatusCode != http.StatusMultiStatus {
		return mapWebDAVHTTPError(resp, readSmallResponse(resp))
	}
	if resp.ContentLength > maxWebDAVPropfindResponse {
		return fmt.Errorf("cloudfox: WebDAV PROPFIND response exceeds %d MiB", maxWebDAVPropfindResponse>>20)
	}
	limitedBody := &io.LimitedReader{R: resp.Body, N: maxWebDAVPropfindResponse + 1}
	decoder := xml.NewDecoder(limitedBody)
	var responseURL *url.URL
	if resp.Request != nil {
		responseURL = resp.Request.URL
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if limitedBody.N == 0 {
				return fmt.Errorf("cloudfox: WebDAV PROPFIND response exceeds %d MiB", maxWebDAVPropfindResponse>>20)
			}
			return nil
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if limitedBody.N == 0 {
				return fmt.Errorf("cloudfox: WebDAV PROPFIND response exceeds %d MiB", maxWebDAVPropfindResponse>>20)
			}
			return fmt.Errorf("cloudfox: decode WebDAV multistatus: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Space != "DAV:" || start.Name.Local != "response" {
			continue
		}
		var response davResponse
		if err := decoder.DecodeElement(&response, &start); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if limitedBody.N == 0 {
				return fmt.Errorf("cloudfox: WebDAV PROPFIND response exceeds %d MiB", maxWebDAVPropfindResponse>>20)
			}
			return fmt.Errorf("cloudfox: decode WebDAV response: %w", err)
		}
		itemLocation, err := b.locationFromHref(response.Href, responseURL)
		if err != nil {
			return err
		}
		// Resolve and confine every href first, but do not parse properties for
		// responses the requested operation intentionally ignores. A malformed
		// deep child must not break a direct-child listing, and an unrelated
		// response must not break an exact Stat.
		if acceptLocation != nil && !acceptLocation(itemLocation) {
			continue
		}
		entry, err := davResponseEntry(response, itemLocation)
		if err != nil {
			return err
		}
		if err := onEntry(entry); err != nil {
			return err
		}
	}
}

func (b *webDAVBackend) ReadDir(ctx context.Context, location string, onChunk func([]RemoteEntry)) error {
	location, err := b.Normalize(location)
	if err != nil {
		return err
	}
	batch := make([]RemoteEntry, 0, 200)
	err = b.propfind(ctx, location, "1", func(itemLocation string) bool {
		return itemLocation != location && b.Dir(itemLocation) == location
	}, func(entry RemoteEntry) error {
		batch = append(batch, entry)
		if len(batch) == cap(batch) {
			onChunk(batch)
			batch = make([]RemoteEntry, 0, 200)
		}
		return ctx.Err()
	})
	if len(batch) != 0 {
		onChunk(batch)
	}
	return err
}

func (b *webDAVBackend) Stat(ctx context.Context, location string) (RemoteEntry, error) {
	location, err := b.Normalize(location)
	if err != nil {
		return RemoteEntry{}, err
	}
	var result RemoteEntry
	found := false
	err = b.propfind(ctx, location, "0", func(itemLocation string) bool {
		return itemLocation == location
	}, func(entry RemoteEntry) error {
		if !found && entry.Location == location {
			result, found = entry, true
		}
		return nil
	})
	if err != nil {
		return RemoteEntry{}, err
	}
	if !found {
		return RemoteEntry{}, os.ErrNotExist
	}
	return result, nil
}

func (b *webDAVBackend) MkDir(ctx context.Context, location string) error {
	location, err := b.Normalize(location)
	if err != nil {
		return err
	}
	if location == "/" {
		return nil
	}
	segments := strings.Split(strings.TrimPrefix(location, "/"), "/")
	current := "/"
	created := make([]string, 0, len(segments))
	failure := func(failed string, err error) error {
		if len(created) == 0 {
			return err
		}
		return &vfs.PartialOperationError{
			Operation: "WebDAV MKCOL",
			Completed: append([]string(nil), created...),
			Failed:    []string{failed},
			Err:       err,
		}
	}
	for _, segment := range segments {
		current = path.Join(current, segment)
		if err := ctx.Err(); err != nil {
			return failure(current, err)
		}
		req, err := b.request(ctx, "MKCOL", current, nil)
		if err != nil {
			return failure(current, err)
		}
		// MKCOL creates a collection, so use its canonical collection-form URI
		// from the outset. Strict DAV servers redirect slashless collection
		// paths, and replaying a mutation after a redirect would be unsafe.
		if !strings.HasSuffix(req.URL.Path, "/") {
			req.URL.Path += "/"
			req.URL.RawPath = ""
		}
		resp, err := b.client.Do(req)
		if err != nil {
			unknown := &vfs.UnknownOperationStateError{Operation: "WebDAV MKCOL", Err: safeWebDAVMutationTransportError(err)}
			return failure(current, unknown)
		}
		_, canonicalCollection := webDAVTrailingSlashRedirect(resp)
		if resp.StatusCode == http.StatusMethodNotAllowed || canonicalCollection {
			_ = resp.Body.Close() // Response-body cleanup is best effort.
			entry, statErr := b.Stat(ctx, current)
			if statErr != nil {
				if canonicalCollection {
					statErr = &vfs.UnknownOperationStateError{Operation: "WebDAV MKCOL", Err: statErr}
				}
				return failure(current, statErr)
			}
			if !entry.IsDir {
				return failure(current, fmt.Errorf("%w: WebDAV path %s is not a collection", os.ErrExist, current))
			}
			continue
		}
		if resp.StatusCode != http.StatusCreated {
			message := readSmallResponse(resp)
			_ = resp.Body.Close() // Response-body cleanup is best effort.
			return failure(current, webDAVHTTPMutationError("WebDAV MKCOL", resp, message))
		}
		_ = resp.Body.Close() // Response-body cleanup is best effort.
		created = append(created, current)
	}
	return nil
}

func (b *webDAVBackend) mutation(ctx context.Context, method, location string, headers map[string]string, collection ...bool) error {
	req, err := b.request(ctx, method, location, nil)
	if err != nil {
		return err
	}
	if len(collection) != 0 && collection[0] && !strings.HasSuffix(req.URL.Path, "/") {
		req.URL.Path += "/"
		req.URL.RawPath = ""
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return &vfs.UnknownOperationStateError{Operation: "WebDAV " + method, Err: safeWebDAVMutationTransportError(err)}
	}
	defer func() { _ = resp.Body.Close() }() // Response-body cleanup is best effort.
	if resp.Request != nil && resp.Request.Method != method {
		return &vfs.UnknownOperationStateError{
			Operation: "WebDAV " + method,
			Err:       fmt.Errorf("request completed as %s after a redirect", resp.Request.Method),
		}
	}
	if webDAVMutationSuccess(method, resp.StatusCode) {
		return nil
	}
	if resp.StatusCode == http.StatusMultiStatus {
		return b.multiStatusResult("WebDAV "+method, method, resp, location)
	}
	return webDAVHTTPMutationError("WebDAV "+method, resp, readSmallResponse(resp))
}

func webDAVMutationSuccess(method string, statusCode int) bool {
	switch method {
	case http.MethodDelete:
		return statusCode == http.StatusOK || statusCode == http.StatusNoContent
	case "COPY", "MOVE":
		return statusCode == http.StatusCreated || statusCode == http.StatusNoContent
	case http.MethodPut:
		return statusCode == http.StatusOK || statusCode == http.StatusCreated || statusCode == http.StatusNoContent
	default:
		return false
	}
}

func (b *webDAVBackend) multiStatusResult(operation, method string, resp *http.Response, requestedLocation string) error {
	const maxMultiStatus = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMultiStatus+1))
	if err != nil || len(data) > maxMultiStatus {
		if err == nil {
			err = errors.New("WebDAV Multi-Status response exceeds 1 MiB")
		}
		return &vfs.UnknownOperationStateError{Operation: operation, Err: err}
	}
	var multiStatus davMultiStatus
	if err := xml.Unmarshal(data, &multiStatus); err != nil || len(multiStatus.Responses) == 0 {
		if err == nil {
			err = errors.New("empty WebDAV Multi-Status response")
		}
		return &vfs.UnknownOperationStateError{Operation: operation, Err: err}
	}
	var responseURL *url.URL
	if resp.Request != nil {
		responseURL = resp.Request.URL
	}
	completed := make([]string, 0, len(multiStatus.Responses))
	failed := make([]string, 0, len(multiStatus.Responses))
	failureDetails := make([]string, 0, len(multiStatus.Responses))
	unknownDetails := make([]string, 0, len(multiStatus.Responses))
	for _, response := range multiStatus.Responses {
		location := requestedLocation
		if strings.TrimSpace(response.Href) != "" {
			resolved, resolveErr := b.locationFromHref(response.Href, responseURL)
			if resolveErr != nil {
				return &vfs.UnknownOperationStateError{Operation: operation, Err: resolveErr}
			}
			location = resolved
		}
		statusCode := davStatusCode(response.Status)
		if statusCode == 0 {
			return &vfs.UnknownOperationStateError{
				Operation: operation,
				Err:       fmt.Errorf("WebDAV Multi-Status entry %s has no response status", location),
			}
		}
		if webDAVMutationSuccess(method, statusCode) {
			completed = append(completed, location)
			continue
		}
		if statusCode >= 200 && statusCode < 300 {
			unknownDetails = append(unknownDetails, fmt.Sprintf("%s: HTTP %d is not a final %s result", location, statusCode, method))
			continue
		}
		failed = append(failed, location)
		failureDetails = append(failureDetails, fmt.Sprintf("%s: HTTP %d", location, statusCode))
	}
	if len(unknownDetails) != 0 {
		return &vfs.UnknownOperationStateError{
			Operation: operation,
			Err:       errors.New(strings.Join(unknownDetails, "; ")),
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return &vfs.PartialOperationError{
		Operation: operation,
		Completed: completed,
		Failed:    failed,
		Err:       errors.New(strings.Join(failureDetails, "; ")),
	}
}

func (b *webDAVBackend) Remove(ctx context.Context, location string) error {
	if b.IsRoot(location) {
		return os.ErrPermission
	}
	entry, err := b.Stat(ctx, location)
	if err != nil {
		return err
	}
	err = b.mutation(ctx, http.MethodDelete, location, nil, entry.IsDir)
	// Even an uncertain response may have committed the DELETE remotely.
	// Retiring the local copy is cheap and avoids serving stale data.
	b.downloads.invalidate(entry.Location)
	return err
}

func (b *webDAVBackend) Rename(ctx context.Context, oldLocation, newLocation string) error {
	oldLocation, err := b.Normalize(oldLocation)
	if err != nil {
		return err
	}
	newLocation, err = b.Normalize(newLocation)
	if err != nil {
		return err
	}
	if oldLocation == "/" || newLocation == "/" {
		return os.ErrPermission
	}
	if oldLocation == newLocation {
		return os.ErrInvalid
	}
	entry, err := b.Stat(ctx, oldLocation)
	if err != nil {
		return err
	}
	destination, err := b.urlFor(newLocation)
	if err != nil {
		return err
	}
	if entry.IsDir && !strings.HasSuffix(destination.Path, "/") {
		destination.Path += "/"
		destination.RawPath = ""
	}
	overwrite := "T"
	if allowed, known := vfs.DestinationOverwrite(ctx); known && !allowed {
		overwrite = "F"
	}
	err = b.mutation(ctx, "MOVE", oldLocation, map[string]string{"Destination": destination.String(), "Overwrite": overwrite}, entry.IsDir)
	b.downloads.invalidate(oldLocation)
	b.downloads.invalidate(newLocation)
	return err
}

func (b *webDAVBackend) Copy(ctx context.Context, oldLocation, newLocation string) error {
	oldLocation, err := b.Normalize(oldLocation)
	if err != nil {
		return err
	}
	newLocation, err = b.Normalize(newLocation)
	if err != nil {
		return err
	}
	if oldLocation == "/" || newLocation == "/" {
		return os.ErrPermission
	}
	if oldLocation == newLocation {
		return os.ErrInvalid
	}
	entry, err := b.Stat(ctx, oldLocation)
	if err != nil {
		return err
	}
	overwrite := "F"
	if allowed, known := vfs.DestinationOverwrite(ctx); known {
		if allowed {
			overwrite = "T"
		}
	} else if _, destinationErr := b.Stat(ctx, newLocation); destinationErr == nil {
		overwrite = "T"
	} else if !errors.Is(destinationErr, os.ErrNotExist) {
		return destinationErr
	}
	destination, err := b.urlFor(newLocation)
	if err != nil {
		return err
	}
	if entry.IsDir && !strings.HasSuffix(destination.Path, "/") {
		destination.Path += "/"
		destination.RawPath = ""
	}
	err = b.mutation(ctx, "COPY", oldLocation, map[string]string{"Destination": destination.String(), "Overwrite": overwrite}, entry.IsDir)
	b.downloads.invalidate(newLocation)
	return err
}

type webDAVRangeReader struct {
	client      *http.Client
	url         string
	size        int64
	etag        string
	ctx         context.Context
	cancel      context.CancelFunc
	cache       *webDAVDownloadCache
	cacheKey    string
	fingerprint string
	displayName string
	once        sync.Once
	closeErr    error
	stateMu     sync.Mutex
	local       vfs.ReadAtCloser
	mu          sync.Mutex
	offset      int64
}

func (r *webDAVRangeReader) Size() int64 { return r.size }
func (r *webDAVRangeReader) Close() error {
	r.once.Do(func() {
		r.cancel()
		r.stateMu.Lock()
		if r.local != nil {
			r.closeErr = r.local.Close()
		}
		r.stateMu.Unlock()
	})
	return r.closeErr
}

func (r *webDAVRangeReader) localReader() vfs.ReadAtCloser {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return r.local
}

func (r *webDAVRangeReader) installLocal(reader vfs.ReadAtCloser) vfs.ReadAtCloser {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.local != nil {
		_ = reader.Close()
		return r.local
	}
	if r.ctx.Err() != nil {
		_ = reader.Close()
		return nil
	}
	r.local = reader
	return reader
}

func (r *webDAVRangeReader) readRange(ctx context.Context, p []byte, start, requestedEnd int64) (int, error) {
	if local := r.localReader(); local != nil {
		return local.ReadAt(ctx, p, start)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	requestCtx, done := providerOperationContext(ctx, r.ctx)
	defer done()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, r.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, requestedEnd))
	req.Header.Set("If-Match", r.etag)
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	if err := requestCtx.Err(); err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }() // Response-body cleanup is best effort.
	if resp.StatusCode == http.StatusPreconditionFailed {
		return 0, ErrRemoteObjectChanged
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		contentRange := strings.TrimSpace(resp.Header.Get("Content-Range"))
		if strings.HasPrefix(strings.ToLower(contentRange), "bytes */") {
			actualSize, sizeErr := strconv.ParseInt(strings.TrimSpace(contentRange[len("bytes */"):]), 10, 64)
			if sizeErr == nil && actualSize != r.size {
				return 0, ErrRemoteObjectChanged
			}
		}
		return 0, mapWebDAVHTTPError(resp, readSmallResponse(resp))
	}
	if resp.StatusCode == http.StatusOK {
		if responseETag := cacheETag(resp.Header.Get("ETag")); responseETag != "" && !weakETagEqual(responseETag, r.etag) {
			return 0, ErrRemoteObjectChanged
		}
		cacheFingerprint, fingerprintErr := webDAVFullResponseFingerprint(r.etag, r.fingerprint, resp.Header.Get("ETag"))
		if fingerprintErr != nil {
			return 0, fingerprintErr
		}
		temp, err := responseToTempReader(requestCtx, resp, r.displayName, r.size, true)
		if err != nil {
			return 0, err
		}
		local, err := r.cache.install(r.cacheKey, cacheFingerprint, temp)
		if err != nil {
			return 0, err
		}
		local = r.installLocal(local)
		if local == nil {
			return 0, context.Canceled
		}
		return local.ReadAt(ctx, p, start)
	}
	if resp.StatusCode != http.StatusPartialContent {
		return 0, mapWebDAVHTTPError(resp, readSmallResponse(resp))
	}
	responseStart, responseEnd, responseTotal, rangeErr := parseContentRange(resp.Header.Get("Content-Range"))
	if rangeErr == nil && responseTotal != r.size {
		return 0, ErrRemoteObjectChanged
	}
	if rangeErr != nil || responseStart != start || responseEnd > requestedEnd {
		return 0, fmt.Errorf("cloudfox: Content-Range %q does not match requested bytes %d-%d/%d", resp.Header.Get("Content-Range"), start, requestedEnd, r.size)
	}
	if responseETag := cacheETag(resp.Header.Get("ETag")); responseETag != "" && !weakETagEqual(responseETag, r.etag) {
		return 0, ErrRemoteObjectChanged
	}
	responseCount := int(responseEnd - responseStart + 1)
	if responseCount > len(p) {
		return 0, fmt.Errorf("cloudfox: WebDAV range response exceeds destination buffer")
	}
	n, readErr := io.ReadFull(resp.Body, p[:responseCount])
	if readErr == io.ErrUnexpectedEOF {
		readErr = fmt.Errorf("cloudfox: truncated WebDAV range response: %w", readErr)
	}
	return n, readErr
}

func (r *webDAVRangeReader) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	end, count, err := requestedByteRange(r.size, len(p), off)
	if err != nil || count == 0 {
		return 0, err
	}
	written := 0
	for written < count {
		n, readErr := r.readRange(ctx, p[written:count], off+int64(written), end)
		written += n
		if readErr != nil {
			return written, readErr
		}
		if n == 0 {
			return written, io.ErrNoProgress
		}
	}
	if count < len(p) {
		return written, io.EOF
	}
	return written, nil
}

func (r *webDAVRangeReader) Read(ctx context.Context, p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.ReadAt(ctx, p, r.offset)
	r.offset += int64(n)
	return n, err
}

func copyWebDAVResponse(ctx context.Context, dst io.Writer, src io.Reader, total int64, displayName string) (int64, error) {
	reporter, _ := ctx.Value(vfs.ReporterKey).(vfs.TaskReporter)
	update, _ := ctx.Value(vfs.ProgressKey).(vfs.ProgressCallback)
	if reporter != nil {
		reporter.UpdateTransfer("Downloading", displayName, 0, "", 0, "")
	}
	if update != nil {
		update("Downloading file...", 0)
	}
	buffer := make([]byte, 256*1024)
	var written int64
	lastPercent := 0
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		if reporter != nil && reporter.IsCancelled() {
			return written, context.Canceled
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			count, writeErr := dst.Write(buffer[:n])
			written += int64(count)
			if writeErr != nil {
				return written, writeErr
			}
			if count != n {
				return written, io.ErrShortWrite
			}
			if total > 0 {
				percent := int(written * 100 / total)
				if percent >= 100 {
					percent = 99
				}
				if percent != lastPercent {
					if reporter != nil {
						reporter.UpdateTransfer("Downloading", displayName, percent, "", percent, "")
					}
					if update != nil {
						update("Downloading file...", percent)
					}
					lastPercent = percent
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return written, nil
			}
			return written, readErr
		}
	}
}

func responseToTempReader(ctx context.Context, resp *http.Response, displayName string, expectedSize int64, requireExpectedSize bool) (*providerTempReader, error) {
	if requireExpectedSize && resp.ContentLength >= 0 && resp.ContentLength != expectedSize {
		return nil, fmt.Errorf("%w: WebDAV response size changed from %d to %d bytes", ErrRemoteObjectChanged, expectedSize, resp.ContentLength)
	}
	f, err := os.CreateTemp("", "f4-cloudfox-webdav-download-*")
	if err != nil {
		return nil, err
	}
	tempPath := f.Name()
	cleanup := func() error {
		return errors.Join(f.Close(), os.Remove(tempPath))
	}
	if err := f.Chmod(0o600); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	total := resp.ContentLength
	if total < 0 {
		total = expectedSize
	}
	var body io.Reader = resp.Body
	if requireExpectedSize {
		// Read at most one byte beyond the advertised representation. This
		// detects an oversized chunked response without allowing a malicious or
		// broken server to fill the temp volume before the post-copy check.
		const maxInt64 = int64(^uint64(0) >> 1)
		if expectedSize < maxInt64 {
			body = io.LimitReader(body, expectedSize+1)
		}
	}
	written, err := copyWebDAVResponse(ctx, f, &contextReader{ctx: ctx, r: body}, total, displayName)
	if err != nil {
		return nil, errors.Join(err, cleanup())
	}
	if requireExpectedSize && written != expectedSize {
		return nil, errors.Join(fmt.Errorf("%w: WebDAV response size changed from %d to %d bytes", ErrRemoteObjectChanged, expectedSize, written), cleanup())
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	if reporter, ok := ctx.Value(vfs.ReporterKey).(vfs.TaskReporter); ok {
		reporter.UpdateTransfer("Downloading", displayName, 100, "", 100, "")
	}
	if update, ok := ctx.Value(vfs.ProgressKey).(vfs.ProgressCallback); ok {
		update("Downloading file...", 100)
	}
	return newProviderTempReader(f, tempPath, written), nil
}

func (b *webDAVBackend) Open(ctx context.Context, location string) (vfs.ReadAtCloser, error) {
	entry, err := b.Stat(ctx, location)
	if err != nil {
		return nil, err
	}
	if entry.IsDir {
		return nil, os.ErrInvalid
	}
	fingerprint := webDAVDownloadFingerprint(entry)
	if cached, ok := b.downloads.open(entry.Location, fingerprint); ok {
		return cached, nil
	}
	u, err := b.urlFor(location)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if entry.Size > 0 {
		req.Header.Set("Range", "bytes=0-0")
	}
	if etag := strongETag(entry.Revision); etag != "" {
		req.Header.Set("If-Match", etag)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusPreconditionFailed {
		_ = resp.Body.Close() // Response-body cleanup is best effort.
		return nil, ErrRemoteObjectChanged
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		contentRange := strings.TrimSpace(resp.Header.Get("Content-Range"))
		if strings.HasPrefix(strings.ToLower(contentRange), "bytes */") {
			actualSize, sizeErr := strconv.ParseInt(strings.TrimSpace(contentRange[len("bytes */"):]), 10, 64)
			if sizeErr == nil && entry.SizeKnown && actualSize != entry.Size {
				_ = resp.Body.Close() // Response-body cleanup is best effort.
				return nil, ErrRemoteObjectChanged
			}
		}
	}
	if resp.StatusCode == http.StatusPartialContent {
		probeStart, probeEnd, probeTotal, rangeErr := parseContentRange(resp.Header.Get("Content-Range"))
		if rangeErr == nil && probeTotal != entry.Size {
			_ = resp.Body.Close() // Response-body cleanup is best effort.
			return nil, ErrRemoteObjectChanged
		}
		if rangeErr != nil || probeStart != 0 || probeEnd != 0 {
			_ = resp.Body.Close() // Response-body cleanup is best effort.
			return nil, fmt.Errorf("cloudfox: Content-Range %q does not match WebDAV range probe bytes 0-0/%d", resp.Header.Get("Content-Range"), entry.Size)
		}
		probe, readErr := io.ReadAll(io.LimitReader(resp.Body, 2))
		_ = resp.Body.Close() // Response-body cleanup is best effort.
		if readErr != nil || len(probe) != 1 {
			if readErr == nil {
				readErr = fmt.Errorf("received %d bytes, want 1", len(probe))
			}
			return nil, fmt.Errorf("cloudfox: invalid WebDAV range probe: %w", readErr)
		}
		entryValidator := cacheETag(entry.Revision)
		responseValidator := cacheETag(resp.Header.Get("ETag"))
		if entryValidator != "" && responseValidator != "" && !weakETagEqual(entryValidator, responseValidator) {
			return nil, ErrRemoteObjectChanged
		}
		etag := strongETag(entry.Revision)
		responseETag := strongETag(resp.Header.Get("ETag"))
		if etag == "" {
			etag = responseETag
		}
		if etag != "" {
			readerCtx, cancel := context.WithCancel(context.Background())
			return &webDAVRangeReader{
				client: b.client, url: u.String(), size: entry.Size, etag: etag,
				ctx: readerCtx, cancel: cancel, cache: b.downloads, cacheKey: entry.Location,
				fingerprint: fingerprint, displayName: b.Base(location),
			}, nil
		}
		// Ranges without a strong validator can mix two generations. Fetch one
		// complete response into a private temp file instead.
		fullReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		fullResp, err := b.client.Do(fullReq)
		if err != nil {
			return nil, err
		}
		defer func() { _ = fullResp.Body.Close() }() // Response-body cleanup is best effort.
		if fullResp.StatusCode != http.StatusOK {
			return nil, mapWebDAVHTTPError(fullResp, readSmallResponse(fullResp))
		}
		cacheFingerprint, fingerprintErr := webDAVFullResponseFingerprint(entry.Revision, fingerprint, fullResp.Header.Get("ETag"))
		if fingerprintErr != nil {
			return nil, fingerprintErr
		}
		temp, err := responseToTempReader(ctx, fullResp, b.Base(location), entry.Size, entry.SizeKnown)
		if err != nil {
			return nil, err
		}
		return b.downloads.install(entry.Location, cacheFingerprint, temp)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }() // Response-body cleanup is best effort.
		return nil, mapWebDAVHTTPError(resp, readSmallResponse(resp))
	}
	defer func() { _ = resp.Body.Close() }() // Response-body cleanup is best effort.
	cacheFingerprint, fingerprintErr := webDAVFullResponseFingerprint(entry.Revision, fingerprint, resp.Header.Get("ETag"))
	if fingerprintErr != nil {
		return nil, fingerprintErr
	}
	temp, err := responseToTempReader(ctx, resp, b.Base(location), entry.Size, entry.SizeKnown)
	if err != nil {
		return nil, err
	}
	return b.downloads.install(entry.Location, cacheFingerprint, temp)
}

func (b *webDAVBackend) Create(ctx context.Context, location string) (io.WriteCloser, error) {
	location, err := b.Normalize(location)
	if err != nil {
		return nil, err
	}
	if location == "/" {
		return nil, os.ErrPermission
	}
	return newProviderSpoolWriter(ctx, b.Base(location), func(uploadCtx context.Context, file *os.File, size int64) error {
		u, err := b.urlFor(location)
		if err != nil {
			return err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		// Keep ownership of the spool descriptor here. net/http may close a
		// request body asynchronously after Do returns, which can race cleanup
		// and leave the descriptor temporarily pinned on Windows.
		var body io.Reader = io.NewSectionReader(file, 0, size)
		reporter, hasReporter := uploadCtx.Value(vfs.ReporterKey).(vfs.TaskReporter)
		progress, _ := uploadCtx.Value(providerUploadProgressContextKey{}).(*providerUploadProgress)
		if hasReporter {
			body = &providerProgressReader{r: body, ctx: uploadCtx, reporter: reporter, progress: progress, name: b.Base(location), total: size}
		}
		req, err := http.NewRequestWithContext(uploadCtx, http.MethodPut, u.String(), body)
		if err != nil {
			return err
		}
		req.ContentLength = size
		if overwrite, known := vfs.DestinationOverwrite(uploadCtx); known && !overwrite {
			req.Header.Set("If-None-Match", "*")
		}
		req.GetBody = func() (io.ReadCloser, error) {
			var replay io.Reader = io.NewSectionReader(file, 0, size)
			if !hasReporter {
				return io.NopCloser(replay), nil
			}
			replay = &providerProgressReader{r: replay, ctx: uploadCtx, reporter: reporter, progress: progress, name: b.Base(location), total: size}
			return io.NopCloser(replay), nil
		}
		resp, err := b.client.Do(req)
		// Once PUT is submitted its final state can be unknown even when the
		// response is lost, so never keep serving a pre-upload cached version.
		b.downloads.invalidate(location)
		if err != nil {
			return &vfs.UnknownOperationStateError{Operation: "WebDAV PUT", Err: safeWebDAVMutationTransportError(err)}
		}
		defer func() { _ = resp.Body.Close() }() // Response-body cleanup is best effort.
		if resp.Request != nil && resp.Request.Method != http.MethodPut {
			return &vfs.UnknownOperationStateError{
				Operation: "WebDAV PUT",
				Err:       fmt.Errorf("request completed as %s after a redirect", resp.Request.Method),
			}
		}
		if resp.StatusCode == http.StatusMultiStatus {
			return b.multiStatusResult("WebDAV PUT", http.MethodPut, resp, location)
		}
		if !webDAVMutationSuccess(http.MethodPut, resp.StatusCode) {
			return webDAVHTTPMutationError("WebDAV PUT", resp, readSmallResponse(resp))
		}
		return nil
	})
}

func (b *webDAVBackend) SetAttributes(context.Context, string, vfs.VFSItem) error {
	return ErrUnsupportedOperation
}

func (b *webDAVBackend) Capabilities() vfs.VFSCapabilities {
	return vfs.VFSCapabilities{HasServerSideCopy: true, HasServerSideMove: true, HasRandomAccess: true, HasAtomicNoReplaceRename: true}
}

func (b *webDAVBackend) TransferName(location string) string { return b.Base(location) }
func (b *webDAVBackend) Close() error {
	b.downloads.close()
	if b.client != nil {
		b.client.CloseIdleConnections()
	}
	return nil
}

var _ Backend = (*webDAVBackend)(nil)
var _ BackendCopier = (*webDAVBackend)(nil)
var _ BackendTransferNamer = (*webDAVBackend)(nil)
