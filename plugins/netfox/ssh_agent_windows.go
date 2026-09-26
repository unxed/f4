//go:build windows && !lite

// Pageant support statically links github.com/kbolino/pageant, which a lite
// build (f4#1178) exists to shed; see ssh_dial.go's own comment for why the
// golang.org/x/crypto/ssh-backed dialer this feeds does not build under
// -tags lite either.

package netfox

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/kbolino/pageant"
	"golang.org/x/sys/windows"
)

const windowsOpenSSHAgentPipe = `\\.\pipe\openssh-ssh-agent`

// openSSHAgent finds the agent exposed by the current Windows session. The
// environment variable is tried first: it may name either a Unix socket (for
// MSYS/Cygwin-style agents) or a Windows named pipe, including Pageant's
// per-session pipe written by its OpenSSH integration. If it is absent, use
// the standard Windows OpenSSH pipe and finally Pageant's legacy native
// transport.
func openSSHAgent() (io.ReadWriteCloser, error) {
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if isWindowsNamedPipe(sock) {
			if conn, err := openWindowsAgentPipe(sock); err == nil {
				return conn, nil
			}
		} else {
			// #nosec G704 -- SSH_AUTH_SOCK is an explicit user-session Unix socket, not a network URL or remotely supplied address.
			if conn, err := net.Dial("unix", sock); err == nil {
				return conn, nil
			}
		}
	}

	if conn, err := openWindowsAgentPipe(windowsOpenSSHAgentPipe); err == nil {
		return conn, nil
	}

	conn, err := pageant.NewConn()
	if err != nil {
		return nil, fmt.Errorf("connect to Pageant: %w", err)
	}
	// NewConn only allocates the legacy transport; the first request is what
	// tells us whether a Pageant instance is actually available. Avoid
	// installing a permanently failing auth method when no agent is running.
	if _, err := newSSHAgentClient(conn).List(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("connect to Pageant: %w", err)
	}
	// Keep the probe and dial transports separate so DialSSH can create its own
	// client without two clients sharing one stream.
	_ = conn.Close()
	conn, err = pageant.NewConn()
	if err != nil {
		return nil, fmt.Errorf("connect to Pageant: %w", err)
	}
	return conn, nil
}

func isWindowsNamedPipe(path string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	return strings.HasPrefix(normalized, `\\.\pipe\`) || strings.HasPrefix(normalized, `\\?\pipe\`)
}

func openWindowsAgentPipe(path string) (io.ReadWriteCloser, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("encode SSH agent pipe path: %w", err)
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open SSH agent pipe %q: %w", path, err)
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("open SSH agent pipe %q: create file handle", path)
	}
	return file, nil
}
