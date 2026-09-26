//go:build !windows

package vfs

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/vfs/hostfs"
)

// succeedingDispatcher is answeringDispatcher's counterpart for a Stat call
// that should succeed instead of fail — used to exercise ResolveElevated's
// success path without a real sudo dispatcher.
func succeedingDispatcher(t *testing.T, item VFSItem) *SudoClient {
	t.Helper()
	sock := filepath.Join(shortSocketDir(t), "d.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		conn, err := l.AcceptUnix()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			var req SudoRequest
			if _, err := recvMsg(conn, &req); err != nil {
				return
			}
			if err := sendMsg(conn, SudoResponse{Item: item}, -1); err != nil {
				return
			}
		}
	}()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &SudoClient{conn: conn}
}

// slowSucceedingDispatcher is succeedingDispatcher, but the dispatcher goroutine
// sleeps for delay before answering the request — standing in for the slow PAM
// prompt (fingerprint reader, etc.) that f4#1411 reported freezing the whole UI,
// since it was previously waited on synchronously from the single UI goroutine.
func slowSucceedingDispatcher(t *testing.T, item VFSItem, delay time.Duration) *SudoClient {
	t.Helper()
	sock := filepath.Join(shortSocketDir(t), "d.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		conn, err := l.AcceptUnix()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			var req SudoRequest
			if _, err := recvMsg(conn, &req); err != nil {
				return
			}
			time.Sleep(delay)
			if err := sendMsg(conn, SudoResponse{Item: item}, -1); err != nil {
				return
			}
		}
	}()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &SudoClient{conn: conn}
}

// f4#1411, timing-based regression: the bug was that pressing Enter on a
// directory needing sudo called SetPath synchronously on f4's single
// cooperative UI goroutine, and SetPath waits on the sudo dispatcher — a real
// slow PAM prompt (fingerprint reader retries) could hold that for many
// seconds, freezing the entire UI, not just that one navigation.
//
// This proves both halves of the fix with an actual slow dispatcher rather
// than reasoning about the code: NeedsElevation (what internal/panel/list.go's
// Enter handler now calls synchronously, f4#1411) must return long before the
// dispatcher would ever answer, because it never talks to it at all — while
// SetPath (still used exactly as before by every caller that never got
// migrated to the async path, and by this test as a stand-in for "the old
// synchronous call the UI goroutine used to make") does sit for the full
// delay, demonstrating the freeze is real and that avoiding SetPath here is
// what fixes it.
func TestOSVFSNeedsElevationDoesNotBlockOnSlowDispatcher(t *testing.T) {
	const delay = 300 * time.Millisecond
	target, startPath := makeLocallyDeniedDir(t)
	v := NewOSVFS(startPath)

	old := globalSudoClient
	t.Cleanup(func() { globalSudoClient = old })
	globalSudoClient = slowSucceedingDispatcher(t, VFSItem{IsDir: true}, delay)

	start := time.Now()
	needs := v.NeedsElevation(target)
	elapsed := time.Since(start)
	if !needs {
		t.Fatal("NeedsElevation = false, want true (target is locally permission-denied and sudo is available)")
	}
	if elapsed >= delay {
		t.Fatalf("NeedsElevation took %v, want well under the dispatcher's %v delay -- it must never consult the dispatcher itself", elapsed, delay)
	}

	// Sanity check that the dispatcher delay above is real and would indeed
	// have frozen a caller waiting on it synchronously -- otherwise the
	// assertion above would pass for the wrong reason (a broken dispatcher
	// answering instantly, say).
	start = time.Now()
	if err := v.SetPath(target); err != nil {
		t.Fatalf("SetPath: %v", err)
	}
	if elapsed := time.Since(start); elapsed < delay {
		t.Fatalf("SetPath took %v, want at least the dispatcher's %v delay -- the slow dispatcher isn't exercising the code path this test relies on", elapsed, delay)
	}
}

// makeLocallyDeniedDir creates root/denied-parent (search permission denied)
// containing root/denied-parent/target, and returns target's absolute path.
// Stat-by-name of a permission-denied directory itself still succeeds
// (stat only needs search permission on the ancestors, not the target); what
// #1411's code path actually hits is a child that can't be reached because
// an ancestor along the way denies search. Root is refused nothing, so the
// test is skipped rather than exercising a no-op.
func makeLocallyDeniedDir(t *testing.T) (target, root string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root is refused nothing, so there is nothing for sudo to do")
	}
	root = t.TempDir()
	deniedParent := filepath.Join(root, "denied-parent")
	if err := os.Mkdir(deniedParent, 0o755); err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(deniedParent, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(deniedParent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(deniedParent, 0o755) })
	if _, err := hostfs.Stat(target); !os.IsPermission(err) {
		t.Skipf("precondition not met: Stat(%q) = %v, want a permission error", target, err)
	}
	return target, root
}

// f4#1411: the UI goroutine calls NeedsElevation before ever touching the
// sudo helper, so it must never itself report that sudo is needed unless
// sudo is actually configured, and it must always be answerable from a
// plain, fast, local Stat.
func TestOSVFSNeedsElevation(t *testing.T) {
	target, root := makeLocallyDeniedDir(t)
	v := NewOSVFS(filepath.Dir(target))

	old := globalSudoClient
	t.Cleanup(func() { globalSudoClient = old })

	globalSudoClient = nil
	if v.NeedsElevation(target) {
		t.Fatal("NeedsElevation = true with no sudo client configured")
	}

	globalSudoClient = &SudoClient{} // IsAvailable() only needs a non-nil client.
	if !v.NeedsElevation(target) {
		t.Fatal("NeedsElevation = false for a locally-permission-denied directory with sudo available")
	}

	// A not-found path under an ORDINARY (readable) directory is a plain
	// ENOENT: nothing sudo could fix, so NeedsElevation must say no. (A
	// not-found path under the denied directory would itself be permission
	// denied, since finding out it doesn't exist still needs to search the
	// denied directory — that path needs elevation same as target.)
	if v.NeedsElevation(filepath.Join(root, "does-not-exist-at-all")) {
		t.Fatal("NeedsElevation = true for a plain not-found path, want false (nothing sudo could fix)")
	}
}

// ResolveElevated must resolve exactly what SetPath would have resolved,
// through the sudo helper, without mutating v — the whole point is to let a
// caller run it off the UI goroutine and apply the result later.
func TestOSVFSResolveElevated(t *testing.T) {
	target, startPath := makeLocallyDeniedDir(t)
	v := NewOSVFS(startPath)

	old := globalSudoClient
	t.Cleanup(func() { globalSudoClient = old })
	globalSudoClient = succeedingDispatcher(t, VFSItem{IsDir: true})

	if !v.NeedsElevation(target) {
		t.Fatal("precondition failed: NeedsElevation = false, want true")
	}
	abs, err := v.ResolveElevated(target)
	if err != nil {
		t.Fatalf("ResolveElevated: %v", err)
	}
	if abs != target {
		t.Fatalf("ResolveElevated = %q, want %q", abs, target)
	}
	if v.GetPath() != startPath {
		t.Fatalf("ResolveElevated mutated the current path to %q, want it left at %q", v.GetPath(), startPath)
	}

	v.CommitPath(abs)
	if v.GetPath() != target {
		t.Fatalf("CommitPath left path at %q, want %q", v.GetPath(), target)
	}
}

// A dispatcher answer saying the elevated path is not a directory must be
// reported the same way SetPath reports it, and must still not mutate v.
func TestOSVFSResolveElevatedNotADirectory(t *testing.T) {
	target, startPath := makeLocallyDeniedDir(t)
	v := NewOSVFS(startPath)

	old := globalSudoClient
	t.Cleanup(func() { globalSudoClient = old })
	globalSudoClient = succeedingDispatcher(t, VFSItem{IsDir: false})

	if _, err := v.ResolveElevated(target); err != os.ErrInvalid {
		t.Fatalf("ResolveElevated error = %v, want os.ErrInvalid", err)
	}
	if v.GetPath() != startPath {
		t.Fatalf("ResolveElevated mutated the current path to %q on failure", v.GetPath())
	}
}
