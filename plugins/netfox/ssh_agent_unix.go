//go:build !windows && !lite

// Feeds the golang.org/x/crypto/ssh-backed dialer (ssh_dial.go); see that
// file's own comment for why this does not build under -tags lite.

package netfox

import (
	"fmt"
	"io"
	"net"
	"os"
)

func openSSHAgent() (io.ReadWriteCloser, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK is not set")
	}
	// #nosec G704 -- SSH_AUTH_SOCK is an explicit user-session Unix socket, not a network URL or remotely supplied address.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("connect to SSH agent: %w", err)
	}
	return conn, nil
}
