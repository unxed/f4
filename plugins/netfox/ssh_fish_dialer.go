//go:build !lite

// The full build's FISH+ transport: golang.org/x/crypto/ssh over DialSSH
// (ssh_dial.go), which is exactly the dependency weight a lite build exists
// to shed (f4#1178). fish_dialer_lite.go is the lite build's replacement --
// same two function names, built by shelling out to the console ssh binary
// instead, so fish_vfs.go's own NewFishVFS calls either one without knowing
// which it got.

package netfox

import (
	"context"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/ssh"

	"github.com/unxed/f4/internal/netproxy"
)

// sshShell ties the lifetime of the remote shell and of the connection that
// carries it to the session that speaks through them.
type sshShell struct {
	sess   *ssh.Session
	client *ssh.Client
}

func (s *sshShell) Close() error {
	return errors.Join(s.sess.Close(), s.client.Close())
}

func (s *sshShell) OpenPty(cols, rows int) (any, error) {
	pty, err := NewSSHPty(s.client)
	if err != nil {
		return nil, err
	}
	pty.SetSize(cols, rows)
	if err := pty.Run(""); err != nil {
		return nil, fmt.Errorf("fishplus: start SSH PTY: %w", errors.Join(err, pty.Close()))
	}
	return pty, nil
}

// sshFishDialer builds the transport a FISH+ site speaks over, and — the whole
// point of it being a dialer — can build it again. Everything it needs is in
// the site configuration, which is what makes a reconnect possible at all: the
// credentials are here, not on the far side.
//
// The shell deliberately runs without a pseudo terminal: a terminal would echo
// every request back, turn each \n of a binary frame into \r\n and cut long
// request lines at the canonical buffer limit. The helper can tame a terminal
// with stty when it has to, but not asking for one in the first place is
// cheaper and cannot fail.
//
// The command is "exec /bin/sh" rather than a plain shell request, because the
// account's login shell may well be csh, fish or something else that does not
// speak the POSIX syntax the helper is written in.
func sshFishDialer(host, port, user, pass, keyPath string, timeout int, px netproxy.Settings) FishDialer {
	return sshFishDialerWith(host, port, user, pass, keyPath, timeout, px, func(s *ssh.Session) error {
		return s.Start("exec /bin/sh")
	})
}

// sshFishDialerPwsh is the fallback for a Windows peer. It asks sshd to
// run "powershell.exe -NoLogo -NoProfile" rather than sess.Shell(): the
// exec request resolves through the DefaultShell of the peer, which is
// cmd.exe on a stock OpenSSH-Server-Windows install, so a bare shell
// request would land the helper in cmd — which does not understand a
// single line of PowerShell. Routing through
// "cmd.exe /c powershell.exe -NoLogo -NoProfile" instead lets cmd fork
// PowerShell for us; a peer whose DefaultShell is already powershell.exe
// or pwsh pays for a nested launch (~200 ms) but the same helper runs
// on both. No pseudo-terminal is requested for the same reason as the
// POSIX side: a ConPTY would echo every request back and inject VT
// sequences.
func sshFishDialerPwsh(host, port, user, pass, keyPath string, timeout int, px netproxy.Settings) FishDialer {
	return sshFishDialerWith(host, port, user, pass, keyPath, timeout, px, func(s *ssh.Session) error {
		return s.Start("powershell.exe -NoLogo -NoProfile")
	})
}

// sshFishDialerWith is what both flavors share. The only thing they
// disagree about is how they ask sshd to give them the shell: exec+cmd
// on POSIX (which bypasses an exotic login shell), plain shell on
// Windows (which respects DefaultShell).
func sshFishDialerWith(host, port, user, pass, keyPath string, timeout int, px netproxy.Settings, startShell func(*ssh.Session) error) FishDialer {
	return func(ctx context.Context) (io.Writer, io.Reader, io.Closer, error) {
		// DialSSH carries a timeout of its own and cannot be interrupted, so
		// the context is honoured where it can be: before the dial, and again
		// after it, so a reconnect the user gave up on does not leave a shell
		// running on the far side.
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		client, err := DialSSH(host, port, user, pass, keyPath, timeout, px)
		if err != nil {
			return nil, nil, nil, err
		}
		sess, err := client.NewSession()
		if err != nil {
			_ = client.Close() // Preserve the session-creation failure.
			return nil, nil, nil, err
		}
		shell := &sshShell{sess: sess, client: client}
		stdin, err := sess.StdinPipe()
		if err != nil {
			_ = shell.Close() // Preserve the pipe-creation failure.
			return nil, nil, nil, err
		}
		stdout, err := sess.StdoutPipe()
		if err != nil {
			_ = shell.Close() // Preserve the pipe-creation failure.
			return nil, nil, nil, err
		}
		sess.Stderr = io.Discard
		if err := startShell(sess); err != nil {
			_ = shell.Close() // Preserve the remote-shell startup failure.
			return nil, nil, nil, err
		}
		if err := ctx.Err(); err != nil {
			_ = shell.Close() // Preserve cancellation as the primary error.
			return nil, nil, nil, err
		}
		return stdin, stdout, shell, nil
	}
}
