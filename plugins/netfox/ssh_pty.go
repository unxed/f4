//go:build !lite

// A pseudo terminal over the golang.org/x/crypto/ssh-backed dialer
// (ssh_dial.go); see that file's own comment for why this does not build
// under -tags lite.

package netfox

import (
	"io"

	"github.com/unxed/vtui"
	"golang.org/x/crypto/ssh"
)

type SSHPty struct {
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
}

func NewSSHPty(client *ssh.Client) (*SSHPty, error) {
	sess, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 38400,
		ssh.TTY_OP_OSPEED: 38400,
		ssh.ICANON:        1,
		ssh.ISIG:          1,
		ssh.IUTF8:         1,
	}
	if err := sess.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		_ = sess.Close() // Preserve the PTY request failure.
		return nil, err
	}
	in, _ := sess.StdinPipe()
	out, _ := sess.StdoutPipe()
	return &SSHPty{session: sess, stdin: in, stdout: out}, nil
}

func (p *SSHPty) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *SSHPty) Write(b []byte) (int, error) { return p.stdin.Write(b) }
func (p *SSHPty) Close() error                { return p.session.Close() }
func (p *SSHPty) SetSize(cols, rows int) {
	if err := p.session.WindowChange(rows, cols); err != nil {
		vtui.DebugLog("NET: failed to resize SSH PTY: %v", err)
	}
}
func (p *SSHPty) IsBusy() bool { return false }
func (p *SSHPty) Wait() error  { return p.session.Wait() }
func (p *SSHPty) Run(name string, args ...string) error {
	if name == "" {
		return p.session.Shell()
	}
	cmd := name
	for _, a := range args {
		cmd += " " + a
	}
	return p.session.Start(cmd)
}
