package mongofs

import (
	"context"
	"crypto/pbkdf2"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

var (
	oidA = objectID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	oidB = objectID{0xAA, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0xFF}
)

// fakeServer is a MongoDB that knows the commands the panel sends.
type fakeServer struct {
	addr     string
	user     string
	password string
	cursors  int // getMore calls served
	mechs    []string

	mu    sync.Mutex
	store map[string][]bsonD // collections changed since the start
	extra []string           // collections created since the start
}

func (f *fakeServer) docs(coll string) []bsonD {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.store[coll]; ok {
		return append([]bsonD(nil), d...)
	}
	return f.seed(coll)
}

func (f *fakeServer) put(coll string, docs []bsonD) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.store == nil {
		f.store = map[string][]bsonD{}
	}
	f.store[coll] = docs
}

func (f *fakeServer) seed(coll string) []bsonD {
	switch coll {
	case "orders":
		return []bsonD{
			{{"_id", oidA}, {"total", 12.5}, {"paid", true}, {"note", "a <b> & c"},
				{"when", time.Date(2026, 1, 2, 3, 4, 5, 6_000_000, time.UTC)},
				{"lines", []any{int32(1), int64(1) << 40, int64(7), 3.0, nil}}, {"meta", bsonD{{"k", "v"}}},
				{"blob", bsonBinary{Subtype: 0, Data: []byte("hi")}}, {"ts", bsonTimestamp{T: 7, I: 1}}},
			{{"_id", "abc def"}, {"total", int32(3)}},
			{{"_id", int32(42)}},
			{{"_id", oidB}, {"empty", bsonD{}}, {"list", []any{}}},
		}
	case "paged":
		var out []bsonD
		for i := 0; i < 5; i++ {
			out = append(out, bsonD{{"_id", int32(i)}})
		}
		return out
	case "big":
		out := make([]bsonD, 1200)
		for i := range out {
			out[i] = bsonD{{"_id", int64(i)}}
		}
		return out
	}
	return nil
}

func startFake(t *testing.T, user, password string) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{addr: ln.Addr().String(), user: user, password: password, mechs: []string{"SCRAM-SHA-1", "SCRAM-SHA-256"}}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeServer) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	authed := f.user == ""
	var scram scramState
	for {
		var head [16]byte
		if _, err := io.ReadFull(c, head[:]); err != nil {
			return
		}
		size := int(binary.LittleEndian.Uint32(head[:4]))
		rest := make([]byte, size-16)
		if _, err := io.ReadFull(c, rest); err != nil {
			return
		}
		cmd, err := decodeDoc(rest[5:])
		if err != nil {
			return
		}
		reply := f.handle(cmd, &authed, &scram)
		body, _ := reply.encode()
		msg := appendInt32(nil, int32(21+len(body))) // #nosec G115 -- a test message is small
		msg = appendInt32(msg, 1)
		msg = appendInt32(msg, int32(binary.LittleEndian.Uint32(head[4:]))) // #nosec G115 -- echoing a request id
		msg = appendInt32(msg, opMsg)
		msg = appendInt32(msg, 0)
		msg = append(msg, 0)
		msg = append(msg, body...)
		if _, err := c.Write(msg); err != nil {
			return
		}
	}
}

func fail(text string) bsonD {
	return bsonD{{"ok", float64(0)}, {"errmsg", text}}
}

func cursorReply(ns string, id int64, key string, docs []bsonD) bsonD {
	items := make([]any, len(docs))
	for i, d := range docs {
		items[i] = d
	}
	return bsonD{{"cursor", bsonD{{key, items}, {"id", id}, {"ns", ns}}}, {"ok", float64(1)}}
}

// scramState is one SCRAM conversation as the fake server sees it.
type scramState struct {
	firstBare, serverFirst string
	variant                scramVariant
}

func (f *fakeServer) handle(cmd bsonD, authed *bool, scram *scramState) bsonD {
	name := cmd[0].Key
	db, _ := cmd.get("$db").(string)
	switch name {
	case "isMaster":
		reply := bsonD{{"ismaster", true}, {"ok", float64(1)}}
		if cmd.get("saslSupportedMechs") != nil {
			mechs := make([]any, len(f.mechs))
			for i, m := range f.mechs {
				mechs[i] = m
			}
			reply = append(reply, bsonE{"saslSupportedMechs", mechs})
		}
		return reply
	case "saslStart":
		scram.variant = scramSHA256
		if cmd.get("mechanism") == "SCRAM-SHA-1" {
			scram.variant = scramSHA1
		}
		payload := string(cmd.get("payload").(bsonBinary).Data)
		scram.firstBare = strings.TrimPrefix(payload, "n,,")
		nonce := parseSCRAM(scram.firstBare)["r"]
		salt := []byte("saltsalt")
		scram.serverFirst = "r=" + nonce + "srv,s=" + base64.StdEncoding.EncodeToString(salt) + ",i=4096"
		return bsonD{{"conversationId", int32(1)}, {"done", false}, {"payload", []byte(scram.serverFirst)}, {"ok", float64(1)}}
	case "saslContinue":
		if len(cmd.get("payload").(bsonBinary).Data) == 0 { // the empty last exchange
			return bsonD{{"conversationId", int32(1)}, {"done", true}, {"payload", []byte{}}, {"ok", float64(1)}}
		}
		v := scram.variant
		payload := string(cmd.get("payload").(bsonBinary).Data)
		fields := parseSCRAM(payload)
		salted, _ := pbkdf2.Key(v.hash, v.prepare(f.user, f.password), []byte("saltsalt"), 4096, v.keyLen)
		noProof := payload[:strings.LastIndex(payload, ",p=")]
		authMessage := scram.firstBare + "," + scram.serverFirst + "," + noProof
		clientKey := mac(v.hash, salted, "Client Key")
		h := v.hash()
		h.Write(clientKey)
		sig := mac(v.hash, h.Sum(nil), authMessage)
		proof, _ := base64.StdEncoding.DecodeString(fields["p"])
		for i := range proof {
			proof[i] ^= sig[i]
		}
		if string(proof) != string(clientKey) {
			return fail("Authentication failed.")
		}
		*authed = true
		serverSig := base64.StdEncoding.EncodeToString(mac(v.hash, mac(v.hash, salted, "Server Key"), authMessage))
		// SHA-1 conversations end with an extra empty exchange.
		return bsonD{{"conversationId", int32(1)}, {"done", v.name == "SCRAM-SHA-256"}, {"payload", []byte("v=" + serverSig)}, {"ok", float64(1)}}
	}
	if !*authed {
		return fail("command " + name + " requires authentication")
	}
	switch name {
	case "listDatabases":
		return bsonD{{"databases", []any{bsonD{{"name", "admin"}}, bsonD{{"name", "shop"}}}}, {"ok", float64(1)}}
	case "listCollections":
		if db != "shop" {
			return cursorReply(db+".$cmd.listCollections", 0, "firstBatch", nil)
		}
		var docs []bsonD
		f.mu.Lock()
		names := append([]string{"orders", "paged", "big", "system.views"}, f.extra...)
		f.mu.Unlock()
		for _, n := range names {
			docs = append(docs, bsonD{{"name", n}})
		}
		return cursorReply("shop.$cmd.listCollections", 0, "firstBatch", docs)
	case "find":
		coll, _ := cmd[0].Value.(string)
		all := f.docs(coll)
		if filter, ok := cmd.get("filter").(bsonD); ok {
			var hit []bsonD
			for _, d := range all {
				if fmt.Sprint(d.get("_id")) == fmt.Sprint(filter.get("_id")) ||
					toJSON(d.get("_id"), "") == toJSON(filter.get("_id"), "") {
					hit = append(hit, d)
				}
			}
			return cursorReply("shop."+coll, 0, "firstBatch", hit)
		}
		limit := len(all)
		if l, ok := numberOf(cmd.get("limit")); ok && int(l) < limit {
			limit = int(l)
		}
		all = all[:limit]
		if coll == "paged" {
			return cursorReply("shop.paged", 77, "firstBatch", all[:2])
		}
		if _, ok := cmd.get("projection").(bsonD); ok {
			for i, d := range all {
				all[i] = bsonD{{"_id", d.get("_id")}}
			}
		}
		return cursorReply("shop."+coll, 0, "firstBatch", all)
	case "getMore":
		f.cursors++
		all := f.docs("paged")
		from := 2 * f.cursors
		to := min(from+2, len(all))
		id := int64(77)
		if to >= len(all) {
			id = 0
		}
		return cursorReply("shop.paged", id, "nextBatch", all[from:to])
	case "killCursors":
		return bsonD{{"ok", float64(1)}}
	case "create":
		f.mu.Lock()
		f.extra = append(f.extra, cmd[0].Value.(string))
		f.mu.Unlock()
		return bsonD{{"ok", float64(1)}}
	case "insert":
		coll := cmd[0].Value.(string)
		docs := f.docs(coll)
		for _, item := range cmd.get("documents").([]any) {
			d := item.(bsonD)
			for _, have := range docs {
				if toJSON(have.get("_id"), "") == toJSON(d.get("_id"), "") {
					return bsonD{{"n", int32(0)}, {"writeErrors", []any{bsonD{{"errmsg", "E11000 duplicate key"}}}}, {"ok", float64(1)}}
				}
			}
			docs = append(docs, d)
		}
		f.put(coll, docs)
		return bsonD{{"n", int32(1)}, {"ok", float64(1)}}
	case "update":
		coll := cmd[0].Value.(string)
		docs := f.docs(coll)
		u := cmd.get("updates").([]any)[0].(bsonD)
		want := toJSON(u.get("q").(bsonD).get("_id"), "")
		for i, have := range docs {
			if toJSON(have.get("_id"), "") == want {
				docs[i] = u.get("u").(bsonD)
				f.put(coll, docs)
				return bsonD{{"n", int32(1)}, {"nModified", int32(1)}, {"ok", float64(1)}}
			}
		}
		return bsonD{{"n", int32(0)}, {"ok", float64(1)}}
	case "delete":
		coll := cmd[0].Value.(string)
		docs := f.docs(coll)
		want := toJSON(cmd.get("deletes").([]any)[0].(bsonD).get("q").(bsonD).get("_id"), "")
		for i, have := range docs {
			if toJSON(have.get("_id"), "") == want {
				f.put(coll, append(docs[:i:i], docs[i+1:]...))
				return bsonD{{"n", int32(1)}, {"ok", float64(1)}}
			}
		}
		return bsonD{{"n", int32(0)}, {"ok", float64(1)}}
	}
	return fail("no such command: " + name)
}

func connectTo(f *fakeServer, user, pass string) func(context.Context) (*conn, error) {
	return func(ctx context.Context) (*conn, error) {
		return dial(ctx, connConfig{addr: f.addr, user: user, pass: pass, authSource: "admin"})
	}
}

func names(t *testing.T, v *mongoVFS, p string) ([]string, error) {
	t.Helper()
	var out []string
	err := v.ReadDir(context.Background(), p, func(items []vfs.VFSItem) {
		for _, it := range items {
			out = append(out, it.Name)
		}
	})
	return out, err
}

func TestMongoVFSBrowses(t *testing.T) {
	f := startFake(t, "", "")
	v := newMongoVFS(connectTo(f, "", ""))
	defer func() { _ = v.Close() }()
	ctx := context.Background()

	dbs, err := names(t, v, "/")
	if err != nil || strings.Join(dbs, ",") != "admin,shop" {
		t.Fatalf("databases %v, %v", dbs, err)
	}
	colls, err := names(t, v, "/shop")
	if err != nil || len(colls) != 4 || colls[0] != "orders" {
		t.Fatalf("collections %v, %v", colls, err)
	}
	docs, err := names(t, v, "/shop/orders")
	want := oidA.hex() + ".json,s_abc%20def.json,i_42.json," + oidB.hex() + ".json"
	if err != nil || strings.Join(docs, ",") != want {
		t.Fatalf("documents %v, %v (want %s)", docs, err, want)
	}

	f2, err := v.Open(ctx, "/shop/orders/"+oidA.hex()+".json")
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, f2.Size())
	_, _ = f2.ReadAt(ctx, data, 0)
	_ = f2.Close()
	text := string(data)
	for _, frag := range []string{
		`"_id": {"$oid": "` + oidA.hex() + `"}`, `"total": 12.5`, `"paid": true`, `"note": "a <b> & c"`,
		`"$date": "2026-01-02T03:04:05.006Z"`, `1099511627776`, `null`, `"k": "v"`,
		`"base64": "aGk="`, `"$timestamp": {"t": 7, "i": 1}`,
	} {
		if !strings.Contains(text, frag) {
			t.Errorf("document lacks %q:\n%s", frag, text)
		}
	}
	if !strings.HasSuffix(text, "}\n") {
		t.Errorf("document should end with a newline: %q", text[len(text)-3:])
	}

	// A string id and an integer id are found from their names.
	for _, name := range []string{"s_abc%20def.json", "i_42.json"} {
		if _, err := v.Stat(ctx, "/shop/orders/"+name); err != nil {
			t.Errorf("Stat(%s): %v", name, err)
		}
	}
	// A name never listed is found from its form; one that says nothing is not.
	v.mu.Lock()
	v.ids = map[string]any{}
	v.mu.Unlock()
	if it, err := v.Stat(ctx, "/shop/orders/"+oidA.hex()+".json"); err != nil || !it.SizeKnown || it.Size < 50 {
		t.Fatalf("Stat by name only: %+v, %v", it, err)
	}
	if _, err := v.Stat(ctx, "/shop/orders/bogus.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bogus name: %v", err)
	}
	if _, err := v.Stat(ctx, "/shop/orders/i_7.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing id: %v", err)
	}
	if _, err := v.Open(ctx, "/shop/orders"); !errors.Is(err, errIsADirectory) {
		t.Fatalf("Open on a collection: %v", err)
	}
	if err := v.ReadDir(ctx, "/shop/orders/x.json", nil); !errors.Is(err, errNotADirectory) {
		t.Fatalf("ReadDir on a document: %v", err)
	}
}

func TestMongoVFSPagingAndTruncation(t *testing.T) {
	f := startFake(t, "", "")
	v := newMongoVFS(connectTo(f, "", ""))
	defer func() { _ = v.Close() }()

	docs, err := names(t, v, "/shop/paged")
	if err != nil || len(docs) != 5 {
		t.Fatalf("a paged collection: %v, %v", docs, err)
	}
	big, err := names(t, v, "/shop/big")
	if len(big) != maxDocs || !errors.Is(err, truncatedError{}) {
		t.Fatalf("a big collection: %d names, %v", len(big), err)
	}
	if !strings.HasPrefix(big[0], "i_0") {
		t.Fatalf("an int64 id should be named i_N: %q", big[0])
	}
}

func TestMongoVFSStatSetPathAndReadOnly(t *testing.T) {
	f := startFake(t, "", "")
	v := newMongoVFS(connectTo(f, "", ""))
	defer func() { _ = v.Close() }()
	ctx := context.Background()

	if err := v.SetPath("/shop/orders"); err != nil {
		t.Fatal(err)
	}
	if v.GetPath() != "mongo:///shop/orders" || v.IsAtRoot() || v.PanelTitle(v.GetPath()) != "MongoDB:shop/orders" || v.PanelTitle("/") != "MongoDB" {
		t.Fatalf("path %q", v.GetPath())
	}
	for _, p := range []string{"/", "/shop", "/shop/orders"} {
		if it, err := v.Stat(ctx, p); err != nil || !it.IsDir {
			t.Errorf("Stat(%q) = %+v, %v", p, it, err)
		}
	}
	for _, p := range []string{"/nodb", "/shop/nocoll"} {
		if _, err := v.Stat(ctx, p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Stat(%q): %v", p, err)
		}
	}
	if err := v.SetPath("/shop/orders/i_42.json"); !errors.Is(err, errNotADirectory) {
		t.Fatalf("SetPath on a document: %v", err)
	}
	if v.Clone().(*mongoVFS).GetPath() != "mongo:///shop/orders" {
		t.Fatal("clone lost the path")
	}
	_, createErr := v.Create(ctx, "/x")
	_, createColl := v.Create(ctx, "/shop/orders")
	_, createNotJSON := v.Create(ctx, "/shop/orders/plain")
	for i, err := range []error{v.MkDir(ctx, "/x"), v.MkDir(ctx, "/shop"), v.Remove(ctx, "/x"), v.Remove(ctx, "/shop/orders"),
		v.Rename(ctx, "/x", "/y"), v.SetAttributes(ctx, "/x", vfs.VFSItem{}), createErr, createColl, createNotJSON} {
		if !errors.Is(err, os.ErrPermission) || err.Error() == "" {
			t.Errorf("mutation %d: %v", i, err)
		}
	}
	_ = v.Close()
	if _, err := v.databases(ctx); err == nil {
		t.Fatal("a closed panel should not reconnect")
	}
}

func TestSCRAMAuthentication(t *testing.T) {
	f := startFake(t, "user=,x", "pässword")
	ctx := context.Background()
	login := func(mech, user, pass string) error {
		v := newMongoVFS(func(ctx context.Context) (*conn, error) {
			return dial(ctx, connConfig{addr: f.addr, user: user, pass: pass, authSource: "admin", authMech: mech})
		})
		defer func() { _ = v.Close() }()
		_, err := v.databases(ctx)
		return err
	}
	if err := login("", "user=,x", "pässword"); err != nil {
		t.Fatalf("negotiated, the right password: %v", err)
	}
	if err := login("SCRAM-SHA-256", "user=,x", "pässword"); err != nil {
		t.Fatalf("SHA-256: %v", err)
	}
	if err := login("SCRAM-SHA-1", "user=,x", "pässword"); err != nil {
		t.Fatalf("SHA-1, the right password: %v", err)
	}
	f.mechs = []string{"SCRAM-SHA-1"} // an old user: the negotiation picks SHA-1
	if err := login("", "user=,x", "pässword"); err != nil {
		t.Fatalf("negotiated SHA-1: %v", err)
	}
	f.mechs = nil
	if err := login("", "user=,x", "pässword"); err != nil {
		t.Fatalf("no mechanisms listed, SHA-256 by default: %v", err)
	}
	if err := login("", "user=,x", "wrong"); !errors.Is(err, errAuth) {
		t.Fatalf("with a wrong password: %v", err)
	}
	if err := login("SCRAM-SHA-1", "user=,x", "wrong"); !errors.Is(err, errAuth) {
		t.Fatalf("SHA-1 with a wrong password: %v", err)
	}
	if err := login("", "", ""); err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("without credentials: %v", err)
	}
	if got := chooseMechanism(connConfig{}, bsonD{{"saslSupportedMechs", []any{"SCRAM-SHA-256", "SCRAM-SHA-1"}}}); got.name != "SCRAM-SHA-256" {
		t.Fatalf("the strongest should win: %s", got.name)
	}
}

func TestSRVResolution(t *testing.T) {
	oldSRV, oldTXT := lookupSRV, lookupTXT
	defer func() { lookupSRV, lookupTXT = oldSRV, oldTXT }()
	lookupSRV = func(_ context.Context, service, proto, name string) (string, []*net.SRV, error) {
		if service != "mongodb" || proto != "tcp" || name != "cluster0.example.net" {
			return "", nil, errors.New("no such record")
		}
		return "", []*net.SRV{{Target: "shard-00.example.net.", Port: 27017}, {Target: "shard-01.example.net.", Port: 27017}}, nil
	}
	lookupTXT = func(context.Context, string) ([]string, error) {
		return []string{"authSource=users&replicaSet=rs0"}, nil
	}

	cfg, err := parseURI("mongodb+srv://u:p@cluster0.example.net/")
	if err != nil || !cfg.srv || !cfg.useTLS || cfg.addr != "cluster0.example.net" {
		t.Fatalf("parse: %+v, %v", cfg, err)
	}
	got, err := resolveSRV(context.Background(), cfg, false)
	if err != nil || got.addr != "shard-00.example.net:27017" || got.authSource != "users" || got.srv {
		t.Fatalf("resolved: %+v, %v", got, err)
	}
	if got, _ := resolveSRV(context.Background(), cfg, true); got.authSource != "admin" {
		t.Fatalf("an explicit authSource must win over TXT: %+v", got)
	}
	if got, _ := resolveSRV(context.Background(), connConfig{addr: "h:1"}, false); got.addr != "h:1" {
		t.Fatalf("a plain address is left alone: %+v", got)
	}
	missing := cfg
	missing.addr = "nowhere.example.net"
	if _, err := resolveSRV(context.Background(), missing, false); !errors.Is(err, errURI) {
		t.Fatalf("a missing SRV record: %v", err)
	}
	if _, err := dial(context.Background(), missing); !errors.Is(err, errURI) {
		t.Fatalf("dial with a missing SRV record: %v", err)
	}
	if cfg, err := parseURI("mongodb+srv://h/?tls=false"); err != nil || cfg.useTLS {
		t.Fatalf("tls=false: %+v, %v", cfg, err)
	}
	for _, bad := range []string{"mongodb+srv://h:27017/", "mongodb+srv://a,b/"} {
		if _, err := parseURI(bad); !errors.Is(err, errURI) {
			t.Errorf("parseURI(%q) = %v", bad, err)
		}
	}
	if cfg, err := parseURI("mongodb://h/?authMechanism=SCRAM-SHA-1"); err != nil || cfg.authMech != "SCRAM-SHA-1" {
		t.Fatalf("authMechanism: %+v, %v", cfg, err)
	}
}

func TestConnectionFailures(t *testing.T) {
	dead := newMongoVFS(func(ctx context.Context) (*conn, error) {
		return dial(ctx, connConfig{addr: "127.0.0.1:1"})
	})
	if _, err := dead.databases(context.Background()); err == nil || !strings.Contains(err.Error(), "MongoDB") {
		t.Fatalf("an unreachable server: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := startFake(t, "", "")
	if _, err := newMongoVFS(connectTo(f, "", "")).databases(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled context: %v", err)
	}
	// A server that hangs up mid-conversation drops the connection, and the
	// next call connects again.
	v := newMongoVFS(connectTo(f, "", ""))
	if _, err := v.databases(context.Background()); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	v.c.close()
	v.mu.Unlock()
	if _, err := v.databases(context.Background()); err == nil {
		t.Fatal("a dead connection should fail once")
	}
	if _, err := v.databases(context.Background()); err != nil {
		t.Fatalf("and then reconnect: %v", err)
	}
	if _, err := v.run(context.Background(), "admin", bsonD{{"nonsense", int32(1)}}); err == nil || !strings.HasPrefix(err.Error(), "mongodb: ") {
		t.Fatalf("a refused command: %v", err)
	}
}

func TestParseURI(t *testing.T) {
	cfg, err := parseURI("mongodb://bob:p%40ss@db1.example:27018,db2/shop?tls=true")
	if err != nil || cfg.addr != "db1.example:27018" || cfg.user != "bob" || cfg.pass != "p@ss" || cfg.authSource != "shop" || !cfg.useTLS {
		t.Fatalf("%+v, %v", cfg, err)
	}
	cfg, err = parseURI("mongodb://localhost/?authSource=users")
	if err != nil || cfg.addr != "localhost:27017" || cfg.authSource != "users" || cfg.useTLS {
		t.Fatalf("%+v, %v", cfg, err)
	}
	if cfg, _ := parseURI("mongodb://[::1]"); cfg.addr != "[::1]:27017" {
		t.Fatalf("ipv6: %+v", cfg)
	}
	for _, bad := range []string{"", "http://x", "mongodb://", "mongodb://h/?authMechanism=PLAIN"} {
		if _, err := parseURI(bad); !errors.Is(err, errURI) {
			t.Errorf("parseURI(%q) = %v", bad, err)
		}
	}
}

func TestBSONRoundTripAndErrors(t *testing.T) {
	in := bsonD{{"s", "x"}, {"i", 5}, {"big", int64(1) << 40}, {"i32", int32(-2)}, {"i64", int64(-3)}, {"f", 1.5}, {"b", true}, {"n", nil},
		{"o", oidA}, {"bin", []byte{1, 2}}, {"doc", bsonD{{"a", int32(1)}}}, {"arr", []any{"p", int32(2)}}}
	raw, err := in.encode()
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeDoc(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := toJSON(out, ""); !strings.Contains(got, `"big": 1099511627776`) || !strings.Contains(got, `"i": 5`) || !strings.Contains(got, `"arr": ["p",2]`) {
		t.Fatalf("round trip: %s", got)
	}
	if _, err := (bsonD{{"x", struct{}{}}}).encode(); err == nil {
		t.Fatal("an unsupported Go type should not encode")
	}
	for _, bad := range [][]byte{nil, {1, 2, 3}, {9, 0, 0, 0, 0}, append(append([]byte{}, raw[:len(raw)-1]...), 1), {12, 0, 0, 0, 0x7F, 'a', 0, 0, 0, 0, 0, 0}} {
		if _, err := decodeDoc(bad); !errors.Is(err, errBSON) {
			t.Errorf("decodeDoc(%v) = %v", bad, err)
		}
	}
	if got := toJSON(bsonD{{"d", bsonRaw{Type: tDecimal, Data: []byte{1}}}, {"nan", nanValue()}, {"empty", bsonD{}}}, ""); !strings.Contains(got, "$unsupported") || !strings.Contains(got, "$numberDouble") {
		t.Fatalf("odd values: %s", got)
	}
}

func nanValue() float64 {
	zero := 0.0
	return zero / zero
}

func TestPluginAndNaming(t *testing.T) {
	p := NewPlugin()
	if p.GetName() != "MongoDB" || p.Close() != nil || p.Init(nil) == nil {
		t.Fatal("plugin identity")
	}
	t.Setenv("MONGODB_URI", "http://x")
	if _, err := connectFromEnv(context.Background()); !errors.Is(err, errURI) {
		t.Fatalf("connectFromEnv: %v", err)
	}
	t.Setenv("MONGODB_URI", "")
	if _, err := connectFromEnv(context.Background()); err == nil {
		t.Skip("a MongoDB is listening on the default port here")
	}
	for id, want := range map[any]string{oidA: oidA.hex() + ".json", "a/b": "s_a%2Fb.json", int32(5): "i_5.json", int64(6): "i_6.json"} {
		if got := docFileName(id); got != want {
			t.Errorf("docFileName(%v) = %q, want %q", id, got, want)
		}
		if back, ok := idFromName(want); !ok || toJSON(back, "") == "" {
			t.Errorf("idFromName(%q) = %v, %v", want, back, ok)
		}
	}
	if got := docFileName(bsonD{{"a", int32(1)}}); !strings.HasPrefix(got, "j_") {
		t.Errorf("a compound id: %q", got)
	}
	if _, ok := idFromName("j_x.json"); ok {
		t.Error("a compound id cannot be read back from its name")
	}
	if _, ok := idFromName("plain"); ok {
		t.Error("a name without .json is not a document")
	}
	if loc := parseLocation("/a/b/c.json"); loc.db != "a" || loc.coll != "b" || loc.doc != "c.json" || loc.depth != 3 {
		t.Errorf("location %+v", loc)
	}
}

func writeDoc(t *testing.T, v *mongoVFS, p, text string) error {
	t.Helper()
	w, err := v.Create(context.Background(), p)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, text); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		return err
	}
	return w.Close() // closing twice is harmless
}

func readDoc(t *testing.T, v *mongoVFS, p string) string {
	t.Helper()
	f, err := v.Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	data := make([]byte, f.Size())
	_, _ = f.ReadAt(context.Background(), data, 0)
	return string(data)
}

func TestMongoVFSEditsDocuments(t *testing.T) {
	f := startFake(t, "", "")
	v := newMongoVFS(connectTo(f, "", ""))
	defer func() { _ = v.Close() }()
	ctx := context.Background()
	orders := "/shop/orders/"

	// An opened document saved unchanged keeps every type, and one edited
	// keeps the rest.
	name := orders + oidA.hex() + ".json"
	original := readDoc(t, v, name)
	if err := writeDoc(t, v, name, original); err != nil {
		t.Fatal(err)
	}
	if again := readDoc(t, v, name); again != original {
		t.Fatalf("a save without edits changed the document:\n%s\n---\n%s", original, again)
	}
	for _, frag := range []string{`{"$numberLong": "7"}`, `3.0`, `1099511627776`} {
		if !strings.Contains(original, frag) {
			t.Errorf("the text should carry %q:\n%s", frag, original)
		}
	}
	edited := strings.Replace(original, `"total": 12.5`, `"total": 99.25`, 1)
	if err := writeDoc(t, v, name, edited); err != nil {
		t.Fatal(err)
	}
	if got := readDoc(t, v, name); !strings.Contains(got, `"total": 99.25`) || !strings.Contains(got, `"paid": true`) {
		t.Fatalf("after an edit:\n%s", got)
	}

	// A new document under an id-shaped name takes that id; under another name
	// it gets a fresh one; a text with the wrong _id is refused.
	if err := writeDoc(t, v, orders+"i_99.json", `{"x": 1, "y": {"$date": "2026-05-06T07:08:09.010Z"}}`); err != nil {
		t.Fatal(err)
	}
	if got := readDoc(t, v, orders+"i_99.json"); !strings.Contains(got, `"_id": {"$numberLong": "99"}`) || !strings.Contains(got, `2026-05-06T07:08:09.010Z`) {
		t.Fatalf("a new document:\n%s", got)
	}
	if err := writeDoc(t, v, orders+"fresh.json", `{"x": 2}`); err != nil {
		t.Fatal(err)
	}
	if err := writeDoc(t, v, orders+"i_99.json", `{"_id": 5}`); !errors.Is(err, errIDMismatch) {
		t.Fatalf("a wrong _id: %v", err)
	}
	if err := writeDoc(t, v, orders+"i_1.json", `{"_id": 1, "x": 1}`); err != nil {
		t.Fatalf("a matching _id: %v", err)
	}
	for _, bad := range []string{`{`, `[1]`, `{"a": 1} {"b": 2}`, `{"_id": {"$oid": "zz"}}`, `{"a": {"$numberLong": "x"}}`} {
		if err := writeDoc(t, v, orders+"i_2.json", bad); !errors.Is(err, errEJSON) {
			t.Errorf("writeDoc(%q) = %v", bad, err)
		}
	}
	// A compound id cannot be found again from its name once the listing is
	// forgotten, so saving it inserts, and the server's refusal is reported.
	if err := writeDoc(t, v, orders+"j_x.json", `{"_id": {"k": 1}}`); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	v.ids = map[string]any{}
	v.mu.Unlock()
	if err := writeDoc(t, v, orders+"j_x.json", `{"_id": {"k": 1}}`); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("a duplicate insert: %v", err)
	}

	// Delete.
	if err := v.Remove(ctx, name); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove(ctx, name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a second delete: %v", err)
	}
	if err := v.Remove(ctx, orders+"nonsense"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a delete by an unreadable name: %v", err)
	}
	if _, err := v.Stat(ctx, name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a deleted document: %v", err)
	}

	// New collection.
	if err := v.MkDir(ctx, "/shop/fresh"); err != nil {
		t.Fatal(err)
	}
	if colls, _ := names(t, v, "/shop"); !contains(colls, "fresh") {
		t.Fatalf("collections after create: %v", colls)
	}
}

func TestParseEJSON(t *testing.T) {
	text := `{"a": {"$oid": "0102030405060708090a0b0c"}, "b": {"$numberInt": "5"}, "c": {"$numberDouble": "Infinity"},
		"d": {"$numberDouble": "-Infinity"}, "e": {"$numberDouble": "NaN"}, "f": {"$numberDouble": "1.5"},
		"g": {"$date": {"$numberLong": "1700000000000"}}, "h": {"$date": 5}, "i": {"$binary": {"base64": "AQI=", "subType": "80"}},
		"j": {"$timestamp": {"t": 1, "i": 2}}, "k": {"$set": 1}, "l": [1, 2.5, 3000000000, "s", null, false], "m": 1e3}`
	v, err := parseEJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	doc := v.(bsonD)
	if doc.get("a") != oidA0() || doc.get("b") != int32(5) || doc.get("f") != 1.5 {
		t.Fatalf("parsed: %+v", doc)
	}
	if b := doc.get("i").(bsonBinary); b.Subtype != 0x80 || string(b.Data) != "\x01\x02" {
		t.Fatalf("binary: %+v", b)
	}
	if doc.get("j") != (bsonTimestamp{T: 1, I: 2}) || doc.get("m") != 1000.0 {
		t.Fatalf("timestamp/exponent: %+v %+v", doc.get("j"), doc.get("m"))
	}
	if k, ok := doc.get("k").(bsonD); !ok || k[0].Key != "$set" {
		t.Fatalf("a $-key that is not a wrapper stays a document: %+v", doc.get("k"))
	}
	l := doc.get("l").([]any)
	if l[0] != int32(1) || l[1] != 2.5 || l[2] != int64(3000000000) {
		t.Fatalf("array: %+v", l)
	}
	if !math.IsInf(doc.get("c").(float64), 1) || !math.IsInf(doc.get("d").(float64), -1) || !math.IsNaN(doc.get("e").(float64)) {
		t.Fatal("non-finite doubles")
	}

	// Everything written by toJSON reads back to the same text.
	raw, _ := bsonD{{"x", bsonRaw{Type: tDecimal, Data: make([]byte, 16)}}, {"n", int64(-2)}, {"z", 0.0}, {"big", 1e21}}.encode()
	back, err := decodeDoc(raw)
	if err != nil {
		t.Fatal(err)
	}
	once := toJSON(back, "")
	parsed, err := parseEJSON([]byte(once))
	if err != nil {
		t.Fatal(err)
	}
	if twice := toJSON(parsed, ""); twice != once {
		t.Fatalf("round trip:\n%s\n%s", once, twice)
	}
	for _, bad := range []string{`{"a": {"$binary": {}}}`, `{"a": {"$timestamp": {}}}`, `{"a": {"$numberInt": "9999999999"}}`,
		`{"a": {"$numberDouble": "x"}}`, `{"a": {"$date": "x"}}`, `{"a": {"$unsupported": {"type": 1, "hex": "00"}}}`, `{"a": 1e999}`, `}`} {
		if _, err := parseEJSON([]byte(bad)); !errors.Is(err, errEJSON) {
			t.Errorf("parseEJSON(%s) = %v", bad, err)
		}
	}
	if _, err := (bsonD{{"x", bsonRaw{Type: 1}}}).encode(); err == nil {
		t.Fatal("a raw value of another type should not encode")
	}
}

func oidA0() objectID { return objectID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} }

func TestURIProviderAndURIPaths(t *testing.T) {
	f := startFake(t, "", "")
	p := uriProvider{open: func() func(context.Context) (*conn, error) { return connectTo(f, "", "") }}
	ctx := context.Background()
	if p.Scheme() != "mongo" {
		t.Fatal(p.Scheme())
	}
	got, err := p.OpenURI(ctx, nil, "mongo:///shop/orders")
	if err != nil {
		t.Fatal(err)
	}
	v := got.(*mongoVFS)
	defer func() { _ = v.Close() }()
	if v.GetPath() != "mongo:///shop/orders" || v.plainPath() != "/shop/orders" {
		t.Fatalf("path %q / %q", v.GetPath(), v.plainPath())
	}
	file := v.Join(v.GetPath(), "i_42.json")
	if file != "mongo:///shop/orders/i_42.json" || v.Base(file) != "i_42.json" || v.Dir(file) != "mongo:///shop/orders" {
		t.Fatalf("Join/Base/Dir: %q %q %q", file, v.Base(file), v.Dir(file))
	}
	if v.Join("/a", "b") != "/a/b" || v.Dir("/a/b") != "/a" || v.Join() != "" {
		t.Fatal("plain paths keep their form")
	}
	if !v.IsAbs("mongo:///x") || !v.IsAbs("/x") || v.IsAbs("x") {
		t.Fatal("IsAbs")
	}
	if abs, _ := v.Abs(file); abs != "/shop/orders/i_42.json" {
		t.Fatalf("Abs(uri) = %q", abs)
	}
	if abs, _ := v.Abs("i_42.json"); abs != "/shop/orders/i_42.json" {
		t.Fatalf("Abs(relative) = %q", abs)
	}
	if _, err := v.Stat(ctx, file); err != nil {
		t.Fatalf("Stat(uri): %v", err)
	}
	if names, err := names(t, v, v.GetPath()); err != nil || len(names) != 4 {
		t.Fatalf("ReadDir(uri): %v, %v", names, err)
	}
	if root, err := p.OpenURI(ctx, nil, "MONGO://"); err != nil || root.GetPath() != "mongo:///" {
		t.Fatalf("the root: %v", err)
	}
	if _, err := p.OpenURI(ctx, nil, "mongo:///nodb"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing database: %v", err)
	}
	if _, err := p.OpenURI(ctx, nil, "mongo:///shop/orders/i_42.json"); !errors.Is(err, errNotADirectory) {
		t.Fatalf("a document: %v", err)
	}
	if _, err := p.OpenURI(ctx, nil, "ftp://x"); err == nil {
		t.Fatal("a foreign scheme should be refused")
	}
}

func TestPasswordIsAskedForAndRemembered(t *testing.T) {
	f := startFake(t, "alice", "s3cret")
	t.Setenv("MONGODB_URI", "mongodb://alice@"+f.addr+"/admin")
	var asked []string
	answers := []string{"wrong", "s3cret"}
	prev := passwordPrompt
	t.Cleanup(func() { passwordPrompt = prev })
	passwordPrompt = func(_ context.Context, who string) (string, error) {
		asked = append(asked, who)
		if len(answers) == 0 {
			return "", context.Canceled
		}
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	c := &connector{}
	ctx := context.Background()
	cn, err := c.open(ctx)
	if err != nil {
		t.Fatalf("after one wrong password and one right: %v", err)
	}
	cn.close()
	if len(asked) != 2 || asked[0] != "alice@"+f.addr {
		t.Fatalf("asked %v", asked)
	}
	// The right password is remembered by this connector: no new prompt.
	cn, err = c.open(ctx)
	if err != nil || len(asked) != 2 {
		t.Fatalf("reconnect: %v, asked %v", err, asked)
	}
	cn.close()

	// Another panel asks for itself, and giving up ends the attempt.
	if _, err := (&connector{}).open(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled prompt: %v", err)
	}
	// An empty answer is not a password.
	passwordPrompt = func(context.Context, string) (string, error) { return "", nil }
	if _, err := (&connector{}).open(ctx); !errors.Is(err, errPasswordNotEntered) {
		t.Fatalf("an empty answer: %v", err)
	}
	// Three wrong answers in a row end in an authentication error.
	passwordPrompt = func(context.Context, string) (string, error) { return "nope", nil }
	if _, err := (&connector{}).open(ctx); !errors.Is(err, errAuth) {
		t.Fatalf("three wrong passwords: %v", err)
	}
	// A password in the string, or no user at all, never prompts.
	passwordPrompt = func(context.Context, string) (string, error) {
		t.Fatal("prompted")
		return "", nil
	}
	t.Setenv("MONGODB_URI", "mongodb://alice:s3cret@"+f.addr+"/admin")
	cn, err = (&connector{}).open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cn.close()
	t.Setenv("MONGODB_URI", "mongodb://alice:@"+f.addr+"/admin")
	if _, err := (&connector{}).open(ctx); !errors.Is(err, errAuth) {
		t.Fatalf("an explicitly empty password: %v", err)
	}
}

func TestPromptPasswordWithoutUI(t *testing.T) {
	previous := vtui.FrameManager
	vtui.FrameManager = nil
	t.Cleanup(func() { vtui.FrameManager = previous })
	if _, err := promptPassword(context.Background(), "a@b"); err == nil {
		t.Fatal("asking for a password without a UI should fail")
	}
}
