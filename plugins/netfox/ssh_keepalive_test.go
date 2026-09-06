package netfox

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/unxed/f4/internal/netproxy"
	"golang.org/x/crypto/ssh/knownhosts"
)

// fakeSSHConn stands in for *ssh.Client. Its SendRequest answers, or hangs
// until Close, depending on frozen; Wait returns once Close has been called,
// as the real one does.
type fakeSSHConn struct {
	frozen bool

	mu       sync.Mutex
	closed   chan struct{}
	once     sync.Once
	requests int
}

func newFakeSSHConn(frozen bool) *fakeSSHConn {
	return &fakeSSHConn{frozen: frozen, closed: make(chan struct{})}
}

func (c *fakeSSHConn) SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error) {
	if name != "keepalive@openssh.com" || !wantReply {
		return false, nil, errors.New("fake: unexpected request " + name)
	}
	c.mu.Lock()
	c.requests++
	c.mu.Unlock()
	select {
	case <-c.closed:
		return false, nil, errors.New("fake: connection closed")
	default:
	}
	if c.frozen {
		<-c.closed
		return false, nil, errors.New("fake: connection closed")
	}
	// OpenSSH answers keepalive@openssh.com with REQUEST_FAILURE; that is a
	// reply all the same, and the loop must treat it as one.
	return false, nil, nil
}

func (c *fakeSSHConn) Wait() error {
	<-c.closed
	return errors.New("fake: connection closed")
}

func (c *fakeSSHConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *fakeSSHConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func (c *fakeSSHConn) sent() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

func waitFor(t *testing.T, what string, done <-chan struct{}, within time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatalf("%s did not happen within %v", what, within)
	}
}

func TestSSHKeepAliveClosesAFrozenConnection(t *testing.T) {
	conn := newFakeSSHConn(true)
	k := startSSHKeepAlive(conn, 10*time.Millisecond, 50*time.Millisecond)

	waitFor(t, "keepalive giving up", k.Done(), 2*time.Second)
	if !conn.isClosed() {
		t.Fatal("a frozen connection must be closed, not left hanging")
	}
	if conn.sent() != 1 {
		t.Errorf("one unanswered request is enough to decide, got %d", conn.sent())
	}
	if k.Rounds() != 0 {
		t.Errorf("no round was ever answered, got %d", k.Rounds())
	}
}

func TestSSHKeepAliveLeavesAnAnsweringConnectionAlone(t *testing.T) {
	conn := newFakeSSHConn(false)
	k := startSSHKeepAlive(conn, 5*time.Millisecond, 50*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for k.Rounds() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if k.Rounds() < 3 {
		t.Fatalf("expected a few answered rounds, got %d", k.Rounds())
	}
	if conn.isClosed() {
		t.Fatal("an answering connection must not be closed")
	}
	select {
	case <-k.Done():
		t.Fatal("the loop left while the connection was still up")
	default:
	}

	// Closing it from the owner's side is what stops the loop.
	_ = conn.Close()
	waitFor(t, "keepalive leaving after Close", k.Done(), 2*time.Second)
}

func TestSSHKeepAliveStopsWhenThePeerHangsUp(t *testing.T) {
	conn := newFakeSSHConn(false)
	k := startSSHKeepAlive(conn, time.Hour, time.Hour)

	// Nothing has been sent yet: the loop is parked on the ticker. A closed
	// connection must still release it.
	_ = conn.Close()
	waitFor(t, "keepalive leaving after the connection went down", k.Done(), 2*time.Second)
	if conn.sent() != 0 {
		t.Errorf("no request should have been sent, got %d", conn.sent())
	}
}

// The real client, through the real server: the request the loop sends is
// one the server answers (with a refusal, as ssh.DiscardRequests does), and
// the loop leaves on its own once the server drops the connection.
func TestSSHKeepAliveRoundTripsThroughARealServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SSH_AUTH_SOCK", "")
	port, publicKey := startTestSSHServer(t)
	writeKnownHosts(t, home, knownhosts.Normalize("127.0.0.1:"+port), publicKey)

	client, err := DialSSH("127.0.0.1", port, "user", "pass", "", 3, netproxy.Settings{Mode: netproxy.ModeDirect})
	if err != nil {
		t.Fatalf("DialSSH: %v", err)
	}
	defer func() { _ = client.Close() }()

	k := startSSHKeepAlive(client, 5*time.Millisecond, 2*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for k.Rounds() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if k.Rounds() < 2 {
		t.Fatalf("the server never answered a keepalive, rounds=%d", k.Rounds())
	}

	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	waitFor(t, "keepalive leaving after the client closed", k.Done(), 5*time.Second)
}

// A direct dial hands out a plain TCP socket, so the kernel keep-alive
// configured in netproxy actually reaches it.
func TestDialSSHUsesAKernelKeepAliveCapableSocket(t *testing.T) {
	conn, err := (netproxy.Settings{Mode: netproxy.ModeDirect}).Dial("tcp", startTCPSink(t))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, ok := conn.(*net.TCPConn); !ok {
		t.Fatalf("a direct dial must yield a *net.TCPConn, got %T", conn)
	}
}

func startTCPSink(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.Close() }()
		}
	}()
	return l.Addr().String()
}
