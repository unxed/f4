package mongofs

import (
	"context"
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- SCRAM-SHA-1 is defined with MD5
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- SCRAM-SHA-1 is defined with SHA-1
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The wire protocol is OP_MSG (MongoDB 3.6 and later): one header, a flags
// word and one section holding the command document. Only what the panel
// needs is here: no pooling, no replica-set discovery, no compression.
const (
	opMsg          = 2013
	maxMessageSize = 48 << 20
	defaultPort    = "27017"
	dialTimeout    = 10 * time.Second
	ioTimeout      = 60 * time.Second
)

var (
	errURI  = errors.New("mongofs: unsupported connection string")
	errAuth = errors.New("mongofs: authentication failed")
)

// connConfig is what a connection string says.
type connConfig struct {
	addr       string
	user, pass string
	passSet    bool // the string carried a password, even an empty one
	authSource string
	authMech   string // "" negotiates
	useTLS     bool
	srv        bool // addr is a mongodb+srv:// name still to be looked up
}

// parseURI reads mongodb://[user:pass@]host[:port][/db][?authSource=x&tls=true].
// A mongodb+srv:// name is looked up when the connection is made (resolveSRV);
// of a replica-set seed list the first host is used.
func parseURI(raw string) (connConfig, error) {
	rest, ok := strings.CutPrefix(raw, "mongodb://")
	srv := false
	if !ok {
		if rest, ok = strings.CutPrefix(raw, "mongodb+srv://"); !ok {
			return connConfig{}, fmt.Errorf("%w: use mongodb://host[:port]", errURI)
		}
		srv = true
	}
	// A seed list ("h1:27017,h2:27017") is not a valid URL host, so the first
	// host is cut out by hand before the rest is parsed.
	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	userinfo, hosts := "", authority
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		userinfo, hosts = authority[:i+1], authority[i+1:]
	}
	host, _, _ := strings.Cut(hosts, ",")
	u, err := url.Parse("mongodb://" + userinfo + host + tail)
	if err == nil && srv && (strings.Contains(hosts, ",") || strings.Contains(u.Host, ":")) {
		return connConfig{}, fmt.Errorf("%w: a mongodb+srv:// name takes no port or host list", errURI)
	}
	if err != nil || u.Host == "" {
		return connConfig{}, fmt.Errorf("%w: use mongodb://host[:port]", errURI)
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil && !srv {
		host = net.JoinHostPort(strings.Trim(u.Host, "[]"), defaultPort)
	} else {
		host = u.Host // an SRV name has no port: the record supplies it
	}
	cfg := connConfig{addr: host, authSource: "admin", srv: srv, useTLS: srv}
	if u.User != nil {
		cfg.user = u.User.Username()
		cfg.pass, cfg.passSet = u.User.Password()
	}
	q := u.Query()
	if v := q.Get("authSource"); v != "" {
		cfg.authSource = v
	} else if db := strings.TrimPrefix(u.Path, "/"); db != "" && cfg.user != "" {
		cfg.authSource = db
	}
	for _, key := range []string{"tls", "ssl"} {
		if v := q.Get(key); v != "" {
			cfg.useTLS = v == "true"
		}
	}
	switch mech := q.Get("authMechanism"); mech {
	case "", "SCRAM-SHA-256", "SCRAM-SHA-1":
		cfg.authMech = mech
	default:
		return connConfig{}, fmt.Errorf("%w: only SCRAM-SHA-256 and SCRAM-SHA-1 authentication are supported", errURI)
	}
	return cfg, nil
}

// lookupSRV and lookupTXT are the DNS calls a mongodb+srv:// name needs; tests
// replace them.
var (
	lookupSRV = net.DefaultResolver.LookupSRV
	lookupTXT = net.DefaultResolver.LookupTXT
)

// resolveSRV turns a mongodb+srv:// name into the first host:port its SRV
// record names, and takes authSource from the TXT record when the string did
// not give one.
func resolveSRV(ctx context.Context, cfg connConfig, explicitAuthSource bool) (connConfig, error) {
	if !cfg.srv {
		return cfg, nil
	}
	_, addrs, err := lookupSRV(ctx, "mongodb", "tcp", cfg.addr)
	if err != nil || len(addrs) == 0 {
		return cfg, fmt.Errorf("%w: no SRV record for %s: %v", errURI, cfg.addr, err)
	}
	if txt, err := lookupTXT(ctx, cfg.addr); err == nil && !explicitAuthSource {
		for _, rec := range txt {
			if v, err := url.ParseQuery(rec); err == nil && v.Get("authSource") != "" {
				cfg.authSource = v.Get("authSource")
			}
		}
	}
	cfg.addr = net.JoinHostPort(strings.TrimSuffix(addrs[0].Target, "."), strconv.Itoa(int(addrs[0].Port)))
	cfg.srv = false
	return cfg, nil
}

// conn is one connection. It is used by one goroutine at a time.
type conn struct {
	c     net.Conn
	reqID int32
}

func dial(ctx context.Context, cfg connConfig) (*conn, error) {
	cfg, err := resolveSRV(ctx, cfg, cfg.authSource != "admin")
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: dialTimeout}
	raw, err := d.DialContext(ctx, "tcp", cfg.addr)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s: %w", mongoText("Mongo.Unreachable",
			"Cannot reach the MongoDB server (check MONGODB_URI and that it is running)",
			"Нет связи с сервером MongoDB (проверьте MONGODB_URI и что сервер запущен)"), err)
	}
	if cfg.useTLS {
		host, _, _ := net.SplitHostPort(cfg.addr)
		tc := tls.Client(raw, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, err
		}
		raw = tc
	}
	c := &conn{c: raw}
	hello := bsonD{{"isMaster", int32(1)}}
	if cfg.user != "" && cfg.authMech == "" {
		hello = append(hello, bsonE{"saslSupportedMechs", cfg.authSource + "." + cfg.user})
	}
	reply, err := c.command(ctx, "admin", hello)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	if cfg.user != "" {
		if err := c.authenticate(ctx, cfg, chooseMechanism(cfg, reply)); err != nil {
			_ = raw.Close()
			return nil, err
		}
	}
	return c, nil
}

func (c *conn) close() {
	if c != nil {
		_ = c.c.Close()
	}
}

// command runs one command against a database and returns the reply document
// if the server says ok.
func (c *conn) command(ctx context.Context, db string, cmd bsonD) (bsonD, error) {
	doc := append(append(bsonD{}, cmd...), bsonE{"$db", db})
	body, err := doc.encode()
	if err != nil {
		return nil, err
	}
	c.reqID++
	msg := make([]byte, 0, 21+len(body))
	msg = appendInt32(msg, int32(21+len(body))) // #nosec G115 -- a command is small
	msg = appendInt32(msg, c.reqID)
	msg = appendInt32(msg, 0)
	msg = appendInt32(msg, opMsg)
	msg = appendInt32(msg, 0) // flag bits
	msg = append(msg, 0)      // section kind 0: one document
	msg = append(msg, body...)

	deadline := time.Now().Add(ioTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.c.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = c.c.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	if _, err := c.c.Write(msg); err != nil {
		return nil, ctxOr(ctx, err)
	}
	reply, err := readReply(c.c)
	if err != nil {
		return nil, ctxOr(ctx, err)
	}
	if ok, _ := numberOf(reply.get("ok")); ok != 1 {
		text, _ := reply.get("errmsg").(string)
		if text == "" {
			text = "the server refused the command"
		}
		return nil, fmt.Errorf("mongodb: %s", text)
	}
	return reply, nil
}

func ctxOr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func readReply(r io.Reader) (bsonD, error) {
	var head [16]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	size := int(binary.LittleEndian.Uint32(head[:4]))
	if size < 21 || size > maxMessageSize {
		return nil, fmt.Errorf("mongodb: bad message length %d", size)
	}
	if op := binary.LittleEndian.Uint32(head[12:]); op != opMsg {
		return nil, fmt.Errorf("mongodb: unexpected opcode %d", op)
	}
	rest := make([]byte, size-16)
	if _, err := io.ReadFull(r, rest); err != nil {
		return nil, err
	}
	flags := binary.LittleEndian.Uint32(rest)
	body := rest[4:]
	if flags&1 != 0 && len(body) >= 4 { // checksum present
		body = body[:len(body)-4]
	}
	if len(body) < 1 || body[0] != 0 {
		return nil, errors.New("mongodb: unsupported reply section")
	}
	return decodeDoc(body[1:])
}

// numberOf reads a BSON number of any width.
func numberOf(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// scramVariant is what differs between SCRAM-SHA-256 and SCRAM-SHA-1.
type scramVariant struct {
	name    string
	hash    func() hash.Hash
	keyLen  int
	prepare func(user, pass string) string // the password as it goes into PBKDF2
}

var (
	scramSHA256 = scramVariant{"SCRAM-SHA-256", sha256.New, sha256.Size,
		func(_, pass string) string { return pass }}
	// SCRAM-SHA-1 is what servers before 4.0 (and users created for them)
	// speak: the password is the hex MD5 of "user:mongo:password" and the hash
	// SHA-1. MongoDB defines it that way, so the weak primitives are required.
	scramSHA1 = scramVariant{"SCRAM-SHA-1", sha1.New, sha1.Size, // #nosec G505 -- the mechanism is defined with SHA-1
		func(user, pass string) string {
			sum := md5.Sum([]byte(user + ":mongo:" + pass)) // #nosec G401 G501 -- the mechanism is defined with MD5
			return hex.EncodeToString(sum[:])
		}}
)

// chooseMechanism picks the mechanism the connection string names, or the
// strongest one the server says the user has, or SCRAM-SHA-256.
func chooseMechanism(cfg connConfig, hello bsonD) scramVariant {
	if cfg.authMech == "SCRAM-SHA-1" {
		return scramSHA1
	}
	if cfg.authMech == "" {
		list, _ := hello.get("saslSupportedMechs").([]any)
		has256, has1 := false, false
		for _, m := range list {
			has256 = has256 || m == "SCRAM-SHA-256"
			has1 = has1 || m == "SCRAM-SHA-1"
		}
		if has1 && !has256 {
			return scramSHA1
		}
	}
	return scramSHA256
}

func mac(h func() hash.Hash, key []byte, msg string) []byte {
	m := hmac.New(h, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// authenticate runs SCRAM (RFC 5802 / 7677 as MongoDB uses it).
func (c *conn) authenticate(ctx context.Context, cfg connConfig, v scramVariant) error {
	nonceRaw := make([]byte, 24)
	if _, err := rand.Read(nonceRaw); err != nil {
		return err
	}
	nonce := base64.StdEncoding.EncodeToString(nonceRaw)
	user := strings.NewReplacer("=", "=3D", ",", "=2C").Replace(cfg.user)
	firstBare := "n=" + user + ",r=" + nonce

	start, err := c.command(ctx, cfg.authSource, bsonD{
		{"saslStart", int32(1)}, {"mechanism", v.name},
		{"payload", []byte("n,," + firstBare)}, {"autoAuthorize", int32(1)},
		{"options", bsonD{{"skipEmptyExchange", true}}},
	})
	if err != nil {
		return fmt.Errorf("%w: %v", errAuth, err)
	}
	serverFirst := payloadString(start)
	fields := parseSCRAM(serverFirst)
	salt, err := base64.StdEncoding.DecodeString(fields["s"])
	iter, iterErr := strconv.Atoi(fields["i"])
	if err != nil || iterErr != nil || iter < 1 || !strings.HasPrefix(fields["r"], nonce) {
		return fmt.Errorf("%w: bad server challenge", errAuth)
	}
	salted, err := pbkdf2.Key(v.hash, v.prepare(cfg.user, cfg.pass), salt, iter, v.keyLen)
	if err != nil {
		return err
	}
	clientKey := mac(v.hash, salted, "Client Key")
	h := v.hash()
	h.Write(clientKey)
	stored := h.Sum(nil)
	finalNoProof := "c=biws,r=" + fields["r"]
	authMessage := firstBare + "," + serverFirst + "," + finalNoProof
	sig := mac(v.hash, stored, authMessage)
	proof := make([]byte, len(clientKey))
	for i := range proof {
		proof[i] = clientKey[i] ^ sig[i]
	}
	final := finalNoProof + ",p=" + base64.StdEncoding.EncodeToString(proof)

	conversation := start.get("conversationId")
	reply, err := c.command(ctx, cfg.authSource, bsonD{
		{"saslContinue", int32(1)}, {"conversationId", conversation}, {"payload", []byte(final)},
	})
	if err != nil {
		return fmt.Errorf("%w: %v", errAuth, err)
	}
	serverSig := parseSCRAM(payloadString(reply))["v"]
	want := base64.StdEncoding.EncodeToString(mac(v.hash, mac(v.hash, salted, "Server Key"), authMessage))
	if subtle.ConstantTimeCompare([]byte(serverSig), []byte(want)) != 1 {
		return fmt.Errorf("%w: the server did not prove it knows the password", errAuth)
	}
	if done, _ := reply.get("done").(bool); !done {
		_, err = c.command(ctx, cfg.authSource, bsonD{
			{"saslContinue", int32(1)}, {"conversationId", conversation}, {"payload", []byte{}},
		})
		return err
	}
	return nil
}

func payloadString(d bsonD) string {
	if b, ok := d.get("payload").(bsonBinary); ok {
		return string(b.Data)
	}
	return ""
}

func parseSCRAM(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(part, "="); ok {
			out[k] = v
		}
	}
	return out
}
