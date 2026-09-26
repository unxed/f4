//go:build lite

// The lite build's FISH+ transport (f4#1178, part 3): the console ssh binary
// as a subprocess, in place of the full build's golang.org/x/crypto/ssh
// dialer (ssh_dial.go, ssh_fish_dialer.go) -- the same "wrap the console
// tool, link no library for it" story plugins/multiarc already tells for
// tar/unzip/7z instead of the native archive libraries. fishplus itself (the
// wire-protocol client this dialer feeds) has no dependency beyond the
// standard library and already speaks to any duplex byte stream, so this
// file is the only piece FISH+ needed to run without SSH library weight.
//
// Scope, deliberately: key- and agent-based auth only. ssh handles both on
// its own once this dialer hands it a host/port/user/key -- nothing here
// needs to know how either works. Three things stay out on purpose:
//
//   - Password auth. This dialer's stdin is the FISH+ wire protocol, not a
//     terminal, so there is nowhere for ssh to read a password from even if
//     it asked; dialSSHSubprocess below refuses a configured password
//     outright rather than let ssh hang waiting on a prompt that can never
//     arrive. sshpass, or an interactive prompt relayed through f4's own UI,
//     is a follow-up if this is ever needed.
//   - An explicit HTTP/SOCKS5 proxy (netproxy.Settings.Explicit()). The full
//     build's DialSSH dials through it directly; routing a subprocess
//     through the same setting would need a ProxyCommand helper (nc, socat)
//     this dialer does not assume is on PATH, so it is refused rather than
//     silently ignored. The common case -- no explicit proxy, or the system
//     default -- dials straight out, which is what a bare `ssh host` would
//     do too.
//   - Host-key handling. This dialer keeps none of its own: it is whatever
//     the system ssh's own known_hosts and StrictHostKeyChecking already do,
//     matching far2l's own console-ssh-wrapped FISH+.
package netfox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"github.com/unxed/f4/internal/netproxy"
)

// sshLookPath is exec.LookPath, overridden in tests so they can exercise the
// "no ssh on PATH" error path without touching the real PATH.
var sshLookPath = exec.LookPath

// sshTimeout turns the timeout a site configuration carries into a duration,
// falling back to something sane when the field is empty or nonsense. Same
// contract as the full build's ssh_dial.go (not built here, see that file's
// own comment), which is what lets fish_vfs.go call it without caring which
// build it is in.
func sshTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return 15 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

// sshConnectTimeoutSeconds is sshTimeout's counterpart for the -o
// ConnectTimeout=N argument ssh itself wants as a plain integer.
func sshConnectTimeoutSeconds(seconds int) int {
	if seconds <= 0 {
		return 15
	}
	return seconds
}

// buildSSHFishArgs is the pure part of the dialer: the argument list handed
// to the console ssh binary, kept separate from exec.Command so it can be
// asserted on directly in tests without spawning anything.
//
// -o BatchMode=yes is not a hardening nicety here, it is load-bearing: this
// dialer's stdin is the FISH+ wire protocol, not a terminal, so a passphrase
// or password prompt ssh cannot suppress on its own would simply hang
// forever with nothing able to answer it. -T asks sshd for a command
// execution, not an interactive one, which is what skips the MOTD and keeps
// ssh from allocating a pseudo terminal that would echo every request back
// and turn each \n of a binary frame into \r\n -- see sshFishDialer's own
// comment in the full build for the long version of that story.
func buildSSHFishArgs(host, port, user, keyPath string, timeout int, remoteCommand string) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(sshConnectTimeoutSeconds(timeout)),
		"-T",
	}
	if port != "" {
		args = append(args, "-p", port)
	}
	if keyPath != "" {
		args = append(args, "-i", keyPath)
	}
	target := host
	if user != "" {
		target = user + "@" + host
	}
	return append(args, target, remoteCommand)
}

// sshProcess ties the lifetime of the local ssh subprocess -- and, through
// it, the remote shell it started -- to the FISH+ session speaking over its
// stdin/stdout. Closing it is how the remote shell is told to leave: closing
// stdin delivers the EOF that ends "exec /bin/sh" (or the PowerShell
// fallback) the same way the full build's sshShell.Close ends its own
// ssh.Session, and ssh itself then exits once the remote command does.
type sshProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *sshProcess) Close() error {
	stdinErr := p.stdin.Close()
	_ = p.stdout.Close() // Best effort: stdin's EOF is what actually ends the remote side.
	waitErr := p.cmd.Wait()
	if stdinErr != nil {
		return stdinErr
	}
	// The remote command exiting non-zero -- including "killed" once ssh
	// tears down after stdin's EOF -- is not this Close's problem to report;
	// the session already knows it is the one closing.
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return waitErr
	}
	return nil
}

// dialSSHSubprocess is what both sshFishDialer and sshFishDialerPwsh share:
// spawning "ssh" with the remote command that starts the right shell
// flavor, wiring its stdin/stdout up as the FISH+ transport.
func dialSSHSubprocess(ctx context.Context, host, port, user, pass, keyPath string, timeout int, px netproxy.Settings, remoteCommand string) (io.Writer, io.Reader, io.Closer, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	if pass != "" {
		return nil, nil, nil, errors.New("fishplus: this lite build's ssh dialer does not support password auth (no PTY to prompt on) -- use a key or an ssh-agent identity instead")
	}
	if px.Explicit() {
		return nil, nil, nil, errors.New("fishplus: this lite build's ssh dialer does not support an explicit HTTP/SOCKS5 proxy -- configure a ProxyCommand in ssh_config instead")
	}
	sshPath, err := sshLookPath("ssh")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fishplus: no ssh binary on PATH: %w", err)
	}

	// #nosec G204 -- host/port/user/keyPath/remoteCommand all come from a
	// site the user configured (or an sftp/fish+ URI they typed), the same
	// trust boundary plugins/multiarc's tool invocations already run at.
	cmd := exec.CommandContext(ctx, sshPath, buildSSHFishArgs(host, port, user, keyPath, timeout, remoteCommand)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		proc := &sshProcess{cmd: cmd, stdin: stdin, stdout: stdout}
		_ = proc.Close() // Preserve cancellation as the primary error.
		return nil, nil, nil, err
	}
	return stdin, stdout, &sshProcess{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

// sshFishDialer is the POSIX flavor: same "exec /bin/sh, no pty" contract as
// the full build's sshFishDialer (ssh_fish_dialer.go), reached through the
// console ssh binary instead of golang.org/x/crypto/ssh.
func sshFishDialer(host, port, user, pass, keyPath string, timeout int, px netproxy.Settings) FishDialer {
	return func(ctx context.Context) (io.Writer, io.Reader, io.Closer, error) {
		return dialSSHSubprocess(ctx, host, port, user, pass, keyPath, timeout, px, "exec /bin/sh")
	}
}

// sshFishDialerPwsh is the full build's Windows fallback, reached the same
// way: sshd resolves a plain command through the peer's DefaultShell, which
// is how a stock OpenSSH-Server-Windows install ends up running PowerShell
// for a peer whose login shell is not already one.
func sshFishDialerPwsh(host, port, user, pass, keyPath string, timeout int, px netproxy.Settings) FishDialer {
	return func(ctx context.Context) (io.Writer, io.Reader, io.Closer, error) {
		return dialSSHSubprocess(ctx, host, port, user, pass, keyPath, timeout, px, "powershell.exe -NoLogo -NoProfile")
	}
}
