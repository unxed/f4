//go:build lite

package netfox

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/netproxy"
)

func TestBuildSSHFishArgsPOSIX(t *testing.T) {
	got := buildSSHFishArgs("example.com", "2222", "bob", "/home/bob/.ssh/id_ed25519", 20, "exec /bin/sh")
	want := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=20",
		"-T",
		"-p", "2222",
		"-i", "/home/bob/.ssh/id_ed25519",
		"bob@example.com",
		"exec /bin/sh",
	}
	if len(got) != len(want) {
		t.Fatalf("buildSSHFishArgs = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("buildSSHFishArgs[%d] = %q, want %q (full: %q)", i, got[i], want[i], got)
		}
	}
}

func TestBuildSSHFishArgsOmitsEmptyPortAndKey(t *testing.T) {
	got := buildSSHFishArgs("example.com", "", "", "", 0, "exec /bin/sh")
	for _, flag := range []string{"-p", "-i"} {
		for _, arg := range got {
			if arg == flag {
				t.Fatalf("buildSSHFishArgs kept %q with nothing to pass it: %q", flag, got)
			}
		}
	}
	if got[len(got)-2] != "example.com" {
		t.Fatalf("buildSSHFishArgs target = %q, want bare host (no user@)", got[len(got)-2])
	}
	// timeout <= 0 falls back the same way sshConnectTimeoutSeconds/sshTimeout do.
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "ConnectTimeout=15") {
		t.Fatalf("buildSSHFishArgs = %q, want the 15s default for a non-positive timeout", got)
	}
}

func TestBuildSSHFishArgsPwshCommand(t *testing.T) {
	got := buildSSHFishArgs("host", "22", "u", "", 5, "powershell.exe -NoLogo -NoProfile")
	if got[len(got)-1] != "powershell.exe -NoLogo -NoProfile" {
		t.Fatalf("buildSSHFishArgs remote command = %q", got[len(got)-1])
	}
}

func TestSSHTimeoutAndConnectTimeoutDefaults(t *testing.T) {
	if got := sshTimeout(0); got != 15*time.Second {
		t.Errorf("sshTimeout(0) = %v, want 15s", got)
	}
	if got := sshTimeout(-3); got != 15*time.Second {
		t.Errorf("sshTimeout(-3) = %v, want 15s", got)
	}
	if got := sshTimeout(7); got != 7*time.Second {
		t.Errorf("sshTimeout(7) = %v, want 7s", got)
	}
	if got := sshConnectTimeoutSeconds(0); got != 15 {
		t.Errorf("sshConnectTimeoutSeconds(0) = %d, want 15", got)
	}
	if got := sshConnectTimeoutSeconds(9); got != 9 {
		t.Errorf("sshConnectTimeoutSeconds(9) = %d, want 9", got)
	}
}

// TestDialSSHSubprocessRejectsAPassword is the load-bearing guard: a password
// handed to this dialer would otherwise leave ssh waiting on a prompt that
// can never come, since this dialer's stdin is the FISH+ wire protocol.
func TestDialSSHSubprocessRejectsAPassword(t *testing.T) {
	_, _, closer, err := dialSSHSubprocess(context.Background(), "host", "22", "u", "secret", "", 5, netproxy.Settings{}, "exec /bin/sh")
	if err == nil {
		if closer != nil {
			_ = closer.Close() // unexpected dial cleanup only
		}
		t.Fatal("a password was accepted by a dialer with no PTY to prompt on")
	}
	if closer != nil {
		t.Fatal("a rejected dial handed back something to close")
	}
}

// TestDialSSHSubprocessRejectsAnExplicitProxy: routing through an explicit
// HTTP/SOCKS5 proxy needs a ProxyCommand helper this dialer does not assume
// is on PATH, so it must fail loudly rather than silently dial direct.
func TestDialSSHSubprocessRejectsAnExplicitProxy(t *testing.T) {
	px := netproxy.Settings{Mode: netproxy.ModeSOCKS5, Host: "proxy.example.com", Port: "1080"}
	if !px.Explicit() {
		t.Fatal("test setup: expected an explicit proxy")
	}
	_, _, closer, err := dialSSHSubprocess(context.Background(), "host", "22", "u", "", "", 5, px, "exec /bin/sh")
	if err == nil {
		if closer != nil {
			_ = closer.Close() // unexpected dial cleanup only
		}
		t.Fatal("an explicit proxy was silently accepted")
	}
	if closer != nil {
		t.Fatal("a rejected dial handed back something to close")
	}
}

// TestDialSSHSubprocessHonoursACancelledContext: a reconnect the user gave up
// on must not spawn ssh at all.
func TestDialSSHSubprocessHonoursACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, closer, err := dialSSHSubprocess(ctx, "host", "22", "u", "", "", 5, netproxy.Settings{}, "exec /bin/sh")
	if !errors.Is(err, context.Canceled) {
		if closer != nil {
			_ = closer.Close() // unexpected dial cleanup only
		}
		t.Fatalf("dial answered %v, want context.Canceled", err)
	}
	if closer != nil {
		t.Fatal("a cancelled dial handed back something to close")
	}
}

// TestDialSSHSubprocessReportsAMissingBinary: no ssh on PATH must fail the
// dial itself rather than spawn something that can never work.
func TestDialSSHSubprocessReportsAMissingBinary(t *testing.T) {
	orig := sshLookPath
	sshLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	defer func() { sshLookPath = orig }()

	_, _, closer, err := dialSSHSubprocess(context.Background(), "host", "22", "u", "", "", 5, netproxy.Settings{}, "exec /bin/sh")
	if err == nil {
		if closer != nil {
			_ = closer.Close() // unexpected dial cleanup only
		}
		t.Fatal("dialling with no ssh binary on PATH succeeded")
	}
	if closer != nil {
		t.Fatal("a failed dial handed back something to close")
	}
}

// TestSSHFishDialersBuildAFishDialer just confirms both flavors return
// something callable with the FishDialer signature -- the behavior itself is
// dialSSHSubprocess's, already covered above.
func TestSSHFishDialersBuildAFishDialer(t *testing.T) {
	var _ FishDialer = sshFishDialer("host", "22", "u", "", "", 5, netproxy.Settings{})
	var _ FishDialer = sshFishDialerPwsh("host", "22", "u", "", "", 5, netproxy.Settings{})
}
