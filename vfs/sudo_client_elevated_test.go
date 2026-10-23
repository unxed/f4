package vfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/vtui"
)

// f4#1768 step 4: the client starts the elevated dispatcher with a fresh
// token, connects once it listens and then works through it. UAC is replaced
// by a dispatcher started in this process.
func TestSudoClientConnectsToTheElevatedDispatcher(t *testing.T) {
	oldLaunch := launchElevated
	t.Cleanup(func() { launchElevated = oldLaunch })
	served := make(chan error, 1)
	launchElevated = func(exe, sock, token string) error {
		if exe != "f4.exe" || len(token) != 2*sudoTokenBytes {
			t.Errorf("launched %q with token %q", exe, token)
		}
		go func() { served <- RunElevatedDispatcher(sock, token, ElevatedFileOps) }()
		return nil
	}
	// The socket path must stay short enough for AF_UNIX.
	t.Setenv("TMPDIR", "/tmp")

	c := &SudoClient{appPath: "f4.exe", consented: true}
	c.mu.Lock()
	err := c.connectElevatedLocked()
	c.mu.Unlock()
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	file := filepath.Join(t.TempDir(), "x.txt")
	if err := c.WriteAt(file, 0, []byte("abc"), true, 0o600); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	item, err := c.Stat(file)
	if err != nil || item.Size != 3 {
		t.Fatalf("Stat = %+v, %v", item, err)
	}
	data, eof, err := c.ReadAt(file, 1, 10)
	if err != nil || string(data) != "bc" || !eof {
		t.Fatalf("ReadAt = %q eof %v, %v", data, eof, err)
	}
	if _, err := c.Stat(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Stat of a missing file = %v, want not-exist", err)
	}

	_ = c.conn.Close()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Error("the dispatcher did not stop when its client went away")
	}
}

func TestSudoClientElevatedConnectFailures(t *testing.T) {
	oldLaunch, oldTimeout := launchElevated, elevatedConnectTimeout
	t.Cleanup(func() { launchElevated, elevatedConnectTimeout = oldLaunch, oldTimeout })
	t.Setenv("TMPDIR", "/tmp")

	launchElevated = func(string, string, string) error { return errors.New("UAC elevation was canceled") }
	c := &SudoClient{appPath: "f4.exe", consented: true}
	c.mu.Lock()
	err := c.connectElevatedLocked()
	c.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Errorf("a canceled prompt gave %v", err)
	}

	// UAC accepted, but nothing ever listens.
	launchElevated = func(string, string, string) error { return nil }
	elevatedConnectTimeout = 300 * time.Millisecond
	c.mu.Lock()
	err = c.connectElevatedLocked()
	c.mu.Unlock()
	if err == nil || c.conn != nil {
		t.Errorf("a dispatcher that never listened gave %v", err)
	}
}

// f4#1768 step 4: before UAC the user is asked whether to go on as
// administrator. A "no" starts nothing; a "yes" holds for the session, so a
// reconnect goes straight to UAC.
func TestSudoClientAsksBeforeUACOncePerSession(t *testing.T) {
	oldLaunch, oldConfirm := launchElevated, confirmElevation
	t.Cleanup(func() { launchElevated, confirmElevation = oldLaunch, oldConfirm })
	launches := 0
	launchElevated = func(string, string, string) error {
		launches++
		return errors.New("UAC elevation was canceled")
	}
	asked, agree := 0, false
	confirmElevation = func() (bool, bool) {
		asked++
		return agree, false
	}

	c := &SudoClient{appPath: "f4.exe"}
	connect := func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.connectElevatedLocked()
	}
	if err := connect(); !errors.Is(err, ErrElevationDeclined) || launches != 0 {
		t.Fatalf("declined: err %v, UAC asked %d times", err, launches)
	}
	agree = true
	if err := connect(); err == nil || launches != 1 {
		t.Fatalf("agreed: err %v, UAC asked %d times", err, launches)
	}
	if err := connect(); err == nil || launches != 2 || asked != 2 {
		t.Fatalf("again: err %v, UAC asked %d times, question %d times", err, launches, asked)
	}
}

// The question itself is a dialog on the UI goroutine; its first button
// agrees, closing it does not.
func TestAskElevationOnUIWaitsForTheAnswer(t *testing.T) {
	oldFM := vtui.FrameManager
	oldConfirm := confirmElevation
	t.Cleanup(func() { vtui.FrameManager = oldFM })
	t.Cleanup(func() { confirmElevation = oldConfirm })
	vtui.FrameManager = &vtui.FrameManagerType{}
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	EnableElevationPrompt()

	type answer struct{ agree, remember bool }
	for _, tc := range []struct {
		code int
		tick bool
		want answer
	}{
		{0, false, answer{true, false}},
		{0, true, answer{true, true}},   // f4#1768 step 6b: the box keeps the rights until f4 exits
		{1, true, answer{false, false}}, // Cancel with the box ticked remembers nothing
		{-1, false, answer{false, false}},
	} {
		got := make(chan answer, 1)
		before := vtui.FrameManager.GetTopFrame()
		go func() {
			agree, remember := confirmElevation()
			got <- answer{agree, remember}
		}()
		var dlg *vtui.Window
		deadline := time.Now().Add(5 * time.Second)
		for dlg == nil && time.Now().Before(deadline) {
			select {
			case task := <-vtui.FrameManager.TaskChan:
				task()
			case <-time.After(10 * time.Millisecond):
			}
			if w, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window); ok && vtui.Frame(w) != before && w.OnResult != nil {
				dlg = w
			}
		}
		if dlg == nil {
			t.Fatal("no question was shown")
		}
		if tc.tick {
			ticked := false
			for _, child := range dlg.GetChildren() {
				if box, ok := child.(*vtui.Checkbox); ok {
					box.State, ticked = 1, true
				}
			}
			if !ticked {
				t.Fatal("the question has no box to tick")
			}
		}
		dlg.OnResult(tc.code)
		dlg.Close()
		select {
		case a := <-got:
			if a != tc.want {
				t.Errorf("answer %d, box %v: got %+v, want %+v", tc.code, tc.tick, a, tc.want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("answer %d did not reach the caller", tc.code)
		}
	}
}

func TestSudoClientWithoutUIDeclinesElevationImmediately(t *testing.T) {
	oldConfirm, oldLaunch := confirmElevation, launchElevated
	t.Cleanup(func() { confirmElevation, launchElevated = oldConfirm, oldLaunch })
	confirmElevation = declineElevation
	launches := 0
	launchElevated = func(string, string, string) error {
		launches++
		return nil
	}

	c := &SudoClient{appPath: "f4.exe"}
	started := time.Now()
	c.mu.Lock()
	err := c.connectElevatedLocked()
	c.mu.Unlock()
	if !errors.Is(err, ErrElevationDeclined) {
		t.Fatalf("connect without a UI = %v, want elevation declined", err)
	}
	if launches != 0 {
		t.Errorf("started the elevated dispatcher %d times after declining", launches)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("declining without a UI took %v, want an immediate refusal", elapsed)
	}
}
