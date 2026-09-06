package netfox

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/unxed/f4/internal/netproxy"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// sshAgentReadWriter deliberately exposes only Read and Write. This makes
// x/crypto/ssh/agent use its serialized client mode instead of starting a
// background reader. The native Pageant transport is a request/response
// connection whose Read returns EOF between requests, unlike a Unix socket;
// treating it as a streaming io.ReadWriteCloser would make the first agent
// response terminate the client before the SSH signature request.
type sshAgentReadWriter struct{ io.ReadWriter }

func newSSHAgentClient(rw io.ReadWriter) agent.ExtendedAgent {
	return agent.NewClient(sshAgentReadWriter{ReadWriter: rw})
}

// sshTimeout turns the timeout a site configuration carries into a duration,
// falling back to something sane when the field is empty or nonsense.
func sshTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return 15 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

// expandHome turns a leading ~ (or ~/ or ~\) into the user's home directory.
// Go's os package never does this on its own — that expansion is normally
// the shell's job — but a path typed into the connection dialog has no
// shell behind it, so a bare ~/.ssh/key would otherwise resolve to a
// nonexistent file named literally "~" in the working directory.
func expandHome(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// loadKeySigner reads a private key file and returns a Signer for it. If the
// key is encrypted, pass is tried as its passphrase.
func loadKeySigner(keyPath, pass string) (ssh.Signer, error) {
	keyBytes, err := os.ReadFile(expandHome(keyPath))
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil && pass != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(keyBytes, []byte(pass))
	}
	return signer, err
}

// DialSSH opens an SSH connection the way every SSH based NetFox backend
// needs it. When keyPath is set, that key is the only key offered (besides
// whatever the ssh-agent carries) — this avoids servers that cut the
// handshake short after a handful of failed public-key attempts
// (MaxAuthTries) before ever reaching the right key. When keyPath is empty,
// behavior is unchanged: agent, then the usual private keys from ~/.ssh,
// then the password. It is shared by the SFTP and the FISH+ backends so
// that a site behaves identically whichever of them opens it.
func DialSSH(host, port, user, pass, keyPath string, timeout int, px netproxy.Settings) (*ssh.Client, error) {
	hostKeyCallback, err := sshHostKeyCallback()
	if err != nil {
		return nil, err
	}

	auths := []ssh.AuthMethod{}
	var agentConn io.ReadWriteCloser

	if conn, err := openSSHAgent(); err == nil {
		agentConn = conn
		agentClient := newSSHAgentClient(conn)
		auths = append(auths, ssh.PublicKeysCallback(agentClient.Signers))
	}

	if keyPath != "" {
		if signer, err := loadKeySigner(keyPath, pass); err == nil {
			auths = append(auths, ssh.PublicKeys(signer))
		}
	} else {
		home, _ := os.UserHomeDir()
		for _, keyName := range []string{"id_rsa", "id_ed25519", "id_ecdsa", "id_dsa"} {
			defaultKeyPath := filepath.Join(home, ".ssh", keyName)
			if signer, err := loadKeySigner(defaultKeyPath, pass); err == nil {
				auths = append(auths, ssh.PublicKeys(signer))
			}
		}
	}

	if pass != "" {
		auths = append(auths, ssh.Password(pass))
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: hostKeyCallback,
		Timeout:         sshTimeout(timeout),
	}
	// ssh.Dial would open the socket itself; going through netproxy instead
	// is what lets a site sit behind an HTTP CONNECT or SOCKS5 gateway.
	client, err := dialSSHVia(px, host+":"+port, config)
	if err != nil {
		if agentConn != nil {
			_ = agentConn.Close() // Preserve the SSH dial failure.
		}
		return nil, err
	}
	if agentConn != nil {
		// The agent is used only for local authentication. Keeping its socket
		// open after the SSH handshake would make forwarding tempting and would
		// hold a needless connection to the user's agent for the whole session.
		_ = agentConn.Close()
	}
	return client, nil
}

func sshHostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("SSH host-key verification: determine home directory: %w", err)
	}
	return sshHostKeyCallbackForHome(home)
}

func sshHostKeyCallbackForHome(home string) (ssh.HostKeyCallback, error) {
	knownHosts, err := newSSHKnownHosts(home)
	if err != nil {
		return nil, err
	}
	return knownHosts.check, nil
}

// dialSSHVia opens the transport through px and speaks SSH over it.
func dialSSHVia(px netproxy.Settings, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
	ctx := context.Background()
	if config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, config.Timeout)
		defer cancel()
	}
	conn, err := px.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if config.Timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(config.Timeout))
		config = withHostKeyPromptDeadline(conn, config)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		_ = conn.Close() // Preserve the SSH handshake failure.
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(c, chans, reqs)
	startSSHKeepAlive(client, sshKeepAliveInterval, sshKeepAliveTimeout)
	return client, nil
}

// SSH keepalive schedule. Every sshKeepAliveInterval of connection lifetime
// the client asks the server for a reply; a reply that has not arrived after
// sshKeepAliveTimeout means the link is gone. 15s and 45s are what OpenSSH
// does under ServerAliveInterval=15 / ServerAliveCountMax=3, and the same
// trade-off applies: a frozen peer is noticed within a minute, while a slow
// one still has three intervals' worth of grace.
const (
	sshKeepAliveInterval = 15 * time.Second
	sshKeepAliveTimeout  = 45 * time.Second
)

// sshKeepAliveConn is the slice of *ssh.Client the keepalive needs, split
// off so the loop can be driven by a fake in tests.
type sshKeepAliveConn interface {
	SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error)
	Wait() error
	Close() error
}

// sshKeepAlive watches one SSH connection until it is closed, by the peer,
// by its owner, or by the keepalive itself.
type sshKeepAlive struct {
	done   chan struct{}
	rounds int64
}

// startSSHKeepAlive keeps proving that the server on the other side of
// client is still there. TCP keep-alive cannot do that on its own: through
// an HTTP or SOCKS proxy the socket ends at the proxy, so a target that
// froze behind a healthy proxy looks perfectly alive to the kernel, and
// even on a direct socket the transport layer knows nothing about an sshd
// that stopped answering. A keepalive@openssh.com global request travels
// the whole way and back — the server's reply, or its polite refusal, is
// the proof — so it is the only check that covers every transport the same
// way. It is the SSH equivalent of ServerAliveInterval.
//
// When the proof does not come the connection is closed. That is the entire
// remedy on purpose: closing is what turns an endless block inside the SFTP
// or FISH+ backend into an error the backend already knows how to report,
// and, where it has one, to answer with a reconnect.
func startSSHKeepAlive(client sshKeepAliveConn, interval, timeout time.Duration) *sshKeepAlive {
	k := &sshKeepAlive{done: make(chan struct{})}
	go k.run(client, interval, timeout)
	return k
}

// Done is closed once the loop has left, whichever side ended the
// connection.
func (k *sshKeepAlive) Done() <-chan struct{} { return k.done }

// Rounds counts the requests the server has answered so far.
func (k *sshKeepAlive) Rounds() int { return int(atomic.LoadInt64(&k.rounds)) }

func (k *sshKeepAlive) run(client sshKeepAliveConn, interval, timeout time.Duration) {
	defer close(k.done)

	// Wait returns once the connection is gone for any reason, so it doubles
	// as the stop signal: nothing needs to remember to cancel the keepalive
	// when a panel closes.
	gone := make(chan struct{})
	go func() {
		_ = client.Wait()
		close(gone)
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-gone:
			return
		case <-ticker.C:
		}

		// SendRequest blocks until the reply comes or the connection dies,
		// and a frozen connection does neither, so the wait has to be
		// bounded from the outside. Closing the connection is what finally
		// releases the blocked request, so the goroutine never leaks.
		replied := make(chan error, 1)
		go func() {
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			replied <- err
		}()
		expired := time.NewTimer(timeout)
		select {
		case err := <-replied:
			expired.Stop()
			if err != nil {
				// The request could not even be written, which only ever
				// happens on a connection that is already going down.
				// Closing it again costs nothing and makes sure of it.
				_ = client.Close()
				return
			}
			// A refusal is as good as an acceptance: either way the server
			// answered, which is all that was being asked.
			atomic.AddInt64(&k.rounds, 1)
		case <-gone:
			expired.Stop()
			return
		case <-expired.C:
			_ = client.Close()
			return
		}
	}
}

// withHostKeyPromptDeadline copies config so that its host-key callback runs
// with the dial deadline lifted. The callback is where an unknown host asks
// the user whether to trust it, and that question waits for a human: the
// fifteen second deadline the dial arms would otherwise expire under the
// dialog and turn every answer, including yes, into a handshake failure. The
// deadline is rearmed on the way out, so the rest of the handshake stays
// bounded.
func withHostKeyPromptDeadline(conn net.Conn, config *ssh.ClientConfig) *ssh.ClientConfig {
	verify := config.HostKeyCallback
	if verify == nil {
		return config
	}
	timeout := config.Timeout
	relaxed := *config
	relaxed.HostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		_ = conn.SetDeadline(time.Time{})
		defer func() { _ = conn.SetDeadline(time.Now().Add(timeout)) }()
		return verify(hostname, remote, key)
	}
	return &relaxed
}
