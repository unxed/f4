package vfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseElevatedDispatcherArgs(t *testing.T) {
	token := strings.Repeat("a", 2*sudoTokenBytes)
	sock, got, found, err := ParseElevatedDispatcherArgs([]string{ElevatedDispatcherFlag, `C:\t\s`, token})
	if !found || err != nil || sock != `C:\t\s` || got != token {
		t.Fatalf("complete invocation = %q %q %v %v", sock, got, found, err)
	}
	if _, _, found, err := ParseElevatedDispatcherArgs([]string{"file.txt"}); found || err != nil {
		t.Fatalf("a normal start was taken for the dispatcher: %v %v", found, err)
	}
	for _, args := range [][]string{
		{ElevatedDispatcherFlag, "sock"},
		{ElevatedDispatcherFlag, "sock", token, "extra"},
		{ElevatedDispatcherFlag, "sock", "short"},
		{ElevatedDispatcherFlag, " ", token},
	} {
		if _, _, found, err := ParseElevatedDispatcherArgs(args); !found || err == nil {
			t.Errorf("%q accepted (found=%v err=%v)", args, found, err)
		}
	}
}

func TestRunElevatedDispatcherServesItsClientAndCleansUp(t *testing.T) {
	dir, err := os.MkdirTemp("", "f4d")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	// A stale file from an earlier session must not stop the dispatcher.
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	token, _ := NewSudoToken()
	done := make(chan error, 1)
	go func() { done <- RunElevatedDispatcher(sock, token, nil) }()

	var conn interface{ Close() error }
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := DialElevated(sock, token, time.Second)
		if err == nil {
			conn = c
			if err := sendMsg(c, SudoRequest{Cmd: CmdPing}, -1); err != nil {
				t.Fatal(err)
			}
			var resp SudoResponse
			if _, err := recvMsg(c, &resp); err != nil || resp.Error != "" {
				t.Fatalf("ping = %+v, %v", resp, err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Skipf("dispatcher socket never came up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = conn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("dispatcher ended with %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dispatcher did not exit after its client left")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
}

func TestLaunchElevatedDispatcherOnlyOnWindows(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("launching asks UAC; not run in tests")
	}
	if err := LaunchElevatedDispatcher("f4", "s", "t"); err != ErrElevationLaunchUnsupported {
		t.Fatalf("got %v", err)
	}
}
