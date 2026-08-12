package fusefs

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name    string
		argv    []string
		wantCmd Command
		check   func(*testing.T, *Spec)
	}{
		{
			name:    "no mount arguments leaves f4 alone",
			argv:    []string{"f4", "--gui=x11", "--debug"},
			wantCmd: CmdNone,
		},
		{
			name:    "source only",
			argv:    []string{"f4", "--mount", "/tmp/a.tar.zst"},
			wantCmd: CmdMount,
			check: func(t *testing.T, s *Spec) {
				if s.Source != "/tmp/a.tar.zst" {
					t.Fatalf("source = %q", s.Source)
				}
				if s.MountPoint != "" {
					t.Fatalf("mount point should be left to the manager, got %q", s.MountPoint)
				}
				if !s.ReadOnly {
					t.Fatal("mounts must default to read-only")
				}
			},
		},
		{
			name:    "positional mount point",
			argv:    []string{"f4", "--mount", "sftp://h/srv", "/mnt/x"},
			wantCmd: CmdMount,
			check: func(t *testing.T, s *Spec) {
				if s.MountPoint != "/mnt/x" {
					t.Fatalf("mount point = %q", s.MountPoint)
				}
			},
		},
		{
			name:    "inline values and --at",
			argv:    []string{"f4", "--mount=sftp://h/srv", "--at=/mnt/y", "--json", "--daemon"},
			wantCmd: CmdMount,
			check: func(t *testing.T, s *Spec) {
				if s.Source != "sftp://h/srv" || s.MountPoint != "/mnt/y" {
					t.Fatalf("got %+v", s)
				}
				if !s.JSON || !s.Daemon {
					t.Fatalf("flags lost: %+v", s)
				}
			},
		},
		{
			name:    "--foreground beats --daemon",
			argv:    []string{"f4", "--mount", "x", "--daemon", "--foreground"},
			wantCmd: CmdMount,
			check: func(t *testing.T, s *Spec) {
				if s.Daemon {
					t.Fatal("--foreground must win")
				}
			},
		},
		{
			name:    "-o options",
			argv:    []string{"f4", "--mount", "x", "-o", "rw,allow_other,timeout=5s,noauto,weird"},
			wantCmd: CmdMount,
			check: func(t *testing.T, s *Spec) {
				if s.ReadOnly {
					t.Fatal("-o rw not applied")
				}
				if !s.AllowOther {
					t.Fatal("-o allow_other not applied")
				}
				if s.Timeout != 5*time.Second {
					t.Fatalf("timeout = %s", s.Timeout)
				}
				if len(s.Extra) != 1 || s.Extra[0] != "weird" {
					t.Fatalf("unknown options should be carried through, got %v", s.Extra)
				}
			},
		},
		{
			name:    "umount",
			argv:    []string{"f4", "--umount", "/mnt/x"},
			wantCmd: CmdUmount,
			check: func(t *testing.T, s *Spec) {
				if s.Source != "/mnt/x" {
					t.Fatalf("target = %q", s.Source)
				}
			},
		},
		{
			name:    "list",
			argv:    []string{"f4", "--list-mounts", "--json"},
			wantCmd: CmdList,
			check: func(t *testing.T, s *Spec) {
				if !s.JSON {
					t.Fatal("--json lost")
				}
			},
		},
		{
			name:    "fstab helper form",
			argv:    []string{"/sbin/mount.fuse.f4", "sftp://h/srv", "/mnt/x", "-o", "ro,noauto,_netdev"},
			wantCmd: CmdMount,
			check: func(t *testing.T, s *Spec) {
				if s.Source != "sftp://h/srv" || s.MountPoint != "/mnt/x" {
					t.Fatalf("got %+v", s)
				}
				if !s.Daemon {
					t.Fatal("an fstab mount has to come up in the background")
				}
				if len(s.Extra) != 0 {
					t.Fatalf("fstab noise leaked through: %v", s.Extra)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, spec, err := ParseArgs(tc.argv)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cmd != tc.wantCmd {
				t.Fatalf("command = %v, want %v", cmd, tc.wantCmd)
			}
			if tc.check != nil {
				tc.check(t, spec)
			}
		})
	}
}

func TestParseArgsRejectsConflictingCommands(t *testing.T) {
	if _, _, err := ParseArgs([]string{"f4", "--mount", "x", "--list-mounts"}); err == nil {
		t.Fatal("expected an error for two commands at once")
	}
}

func TestValidateRefusesWrite(t *testing.T) {
	s := NewSpec()
	s.Source = "x"
	s.ReadOnly = false
	if err := s.Validate(); !errors.Is(err, ErrWriteNotImplemented) {
		t.Fatalf("--rw must be refused outright, got %v", err)
	}
}

func TestValidateRefusesEmptySource(t *testing.T) {
	if err := NewSpec().Validate(); err == nil {
		t.Fatal("expected an error for an empty source")
	}
}

// A daemon child is only ever told what to do through its arguments, so the
// round trip has to be lossless.
func TestChildArgsRoundTrip(t *testing.T) {
	orig := Spec{
		Source:     "sftp://user@host/srv",
		MountPoint: "/mnt/x",
		ReadOnly:   true,
		AllowOther: true,
		Extra:      []string{"weird=1"},
	}
	argv := append([]string{"f4"}, orig.ChildArgs()...)
	cmd, got, err := ParseArgs(argv)
	if err != nil {
		t.Fatalf("re-parsing the child arguments failed: %v", err)
	}
	if cmd != CmdMount {
		t.Fatalf("command = %v", cmd)
	}
	if got.Source != orig.Source || got.MountPoint != orig.MountPoint {
		t.Fatalf("location changed: %+v", got)
	}
	if got.ReadOnly != orig.ReadOnly || got.AllowOther != orig.AllowOther {
		t.Fatalf("flags changed: %+v", got)
	}
	if len(got.Extra) != 1 || got.Extra[0] != "weird=1" {
		t.Fatalf("extra options lost: %v", got.Extra)
	}
	if got.Daemon {
		t.Fatal("the child must not try to daemonise again")
	}
}

func useTempRegistry(t *testing.T) {
	t.Helper()
	t.Setenv("F4_FUSE_REGISTRY", filepath.Join(t.TempDir(), "mounts"))
}

func TestRegistryRoundTrip(t *testing.T) {
	useTempRegistry(t)

	rec, err := Register(Record{Source: "a.tar", MountPoint: "/mnt/a", ReadOnly: true})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if rec.ID == "" || rec.PID != os.Getpid() || rec.Started.IsZero() {
		t.Fatalf("Register did not fill the record in: %+v", rec)
	}

	recs, err := Mounts()
	if err != nil {
		t.Fatalf("Mounts: %v", err)
	}
	if len(recs) != 1 || recs[0].Source != "a.tar" {
		t.Fatalf("got %+v", recs)
	}

	for _, key := range []string{"/mnt/a", rec.ID, "a.tar"} {
		if _, ok := FindMount(key); !ok {
			t.Fatalf("FindMount(%q) found nothing", key)
		}
	}

	if err := Deregister(rec.ID); err != nil {
		t.Fatalf("Deregister: %v", err)
	}
	if recs, _ := Mounts(); len(recs) != 0 {
		t.Fatalf("record survived deregistration: %+v", recs)
	}
	// Deregistering twice is normal: anything may have cleaned up first.
	if err := Deregister(rec.ID); err != nil {
		t.Fatalf("second Deregister: %v", err)
	}
}

func TestRegistryReplacesRecordForSameMountPoint(t *testing.T) {
	useTempRegistry(t)
	if _, err := Register(Record{Source: "a.tar", MountPoint: "/mnt/a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(Record{Source: "b.tar", MountPoint: "/mnt/a"}); err != nil {
		t.Fatal(err)
	}
	recs, _ := Mounts()
	if len(recs) != 1 || recs[0].Source != "b.tar" {
		t.Fatalf("a mount point must have exactly one record, got %+v", recs)
	}
}

func TestRegistryPrunesDeadOwners(t *testing.T) {
	useTempRegistry(t)

	// A PID that is certainly gone: run something and reap it.
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot produce a dead pid here: %v", err)
	}
	dead := cmd.Process.Pid

	rec := Record{ID: recordID("/mnt/ghost"), Source: "ghost", MountPoint: "/mnt/ghost", PID: dead, Started: time.Now()}
	blob, _ := json.Marshal(rec)
	dir := RegistryDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rec.ID+".json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}

	recs, err := Mounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("a record whose owner is gone must be pruned, got %+v", recs)
	}
	if _, err := os.Stat(filepath.Join(dir, rec.ID+".json")); !os.IsNotExist(err) {
		t.Fatal("the stale record file is still on disk")
	}
}

func TestRegistryDropsCorruptRecords(t *testing.T) {
	useTempRegistry(t)
	dir := RegistryDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "deadbeef.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if recs, err := Mounts(); err != nil || len(recs) != 0 {
		t.Fatalf("recs=%+v err=%v", recs, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a record nobody can read should not stay on disk")
	}
}

// withFakeBackend swaps out the one place the CLI touches the mount manager.
func withFakeBackend(t *testing.T, mountPoint string, mountErr error) (release func()) {
	t.Helper()
	oldMount, oldUnmount := mountOnce, unmountOwn
	done := make(chan struct{})
	var once bool

	mountOnce = func(spec Spec) (string, func(), error) {
		if mountErr != nil {
			return "", nil, mountErr
		}
		return mountPoint, func() { <-done }, nil
	}
	unmountOwn = func(string) error {
		if !once {
			once = true
			close(done)
		}
		return nil
	}
	t.Cleanup(func() {
		mountOnce, unmountOwn = oldMount, oldUnmount
	})
	return func() {
		if !once {
			once = true
			close(done)
		}
	}
}

func TestMountAndServeReportsAndRegisters(t *testing.T) {
	useTempRegistry(t)
	mp := filepath.Join(t.TempDir(), "mnt")
	release := withFakeBackend(t, mp, nil)

	spec := NewSpec()
	spec.Source = "a.tar"

	var out, errOut bytes.Buffer
	registered := make(chan struct{})
	finished := make(chan int, 1)

	go func() {
		// Give the goroutine a moment to register before we look.
		finished <- mountAndServe(spec, &out, &errOut)
	}()

	deadline := time.After(2 * time.Second)
	for {
		if recs, _ := Mounts(); len(recs) == 1 {
			if recs[0].MountPoint != filepath.Clean(mp) {
				t.Fatalf("registered %q, want %q", recs[0].MountPoint, mp)
			}
			close(registered)
			break
		}
		select {
		case <-deadline:
			t.Fatal("the mount was never registered")
		case <-time.After(5 * time.Millisecond):
		}
	}
	<-registered

	release()
	select {
	case code := <-finished:
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr=%q", code, errOut.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mountAndServe did not return after the mount ended")
	}

	if got := strings.TrimSpace(out.String()); got != mp {
		t.Fatalf("stdout = %q, want the mount point %q", got, mp)
	}
	if recs, _ := Mounts(); len(recs) != 0 {
		t.Fatalf("the record outlived the mount: %+v", recs)
	}
}

func TestMountAndServeFailureIsReported(t *testing.T) {
	useTempRegistry(t)
	withFakeBackend(t, "", errors.New("no such archive"))

	spec := NewSpec()
	spec.Source = "missing.tar"
	spec.JSON = true

	var out, errOut bytes.Buffer
	if code := mountAndServe(spec, &out, &errOut); code != ExitFailed {
		t.Fatalf("exit code = %d", code)
	}
	var msg readyMessage
	if err := json.Unmarshal(errOut.Bytes(), &msg); err != nil {
		t.Fatalf("--json output is not an object: %q", errOut.String())
	}
	if msg.OK || !strings.Contains(msg.Error, "no such archive") {
		t.Fatalf("got %+v", msg)
	}
	if recs, _ := Mounts(); len(recs) != 0 {
		t.Fatalf("a failed mount must not be registered: %+v", recs)
	}
}

func TestListMountsJSONIsAlwaysAnArray(t *testing.T) {
	useTempRegistry(t)
	spec := NewSpec()
	spec.JSON = true
	var out, errOut bytes.Buffer
	if code := runList(spec, &out, &errOut); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	var recs []Record
	if err := json.Unmarshal(out.Bytes(), &recs); err != nil {
		t.Fatalf("empty list is not valid JSON: %q", out.String())
	}
	if len(recs) != 0 {
		t.Fatalf("got %+v", recs)
	}
}

func TestUmountUnknownTarget(t *testing.T) {
	useTempRegistry(t)
	spec := NewSpec()
	spec.Source = filepath.Join(t.TempDir(), "nothing-here")
	var out, errOut bytes.Buffer
	if code := runUmount(spec, &out, &errOut); code != ExitNoSuchMount && code != ExitFailed {
		t.Fatalf("exit code = %d, stderr=%q", code, errOut.String())
	}
}

func TestIsBusy(t *testing.T) {
	if !isBusy(errors.New("fusermount3: failed to unmount /mnt/x: Device or resource busy")) {
		t.Fatal("helper output not recognised as EBUSY")
	}
	if isBusy(errors.New("no such file or directory")) {
		t.Fatal("false positive")
	}
}

// The readiness handshake is the whole contract between a --daemon parent and
// its child, so all three of its outcomes are worth pinning down.

func TestAwaitReadySuccess(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go reportReady(w, readyMessage{OK: true, MountPoint: "/mnt/x", PID: 42})

	msg, err := awaitReady(r, 2*time.Second)
	if err != nil {
		t.Fatalf("awaitReady: %v", err)
	}
	if !msg.OK || msg.MountPoint != "/mnt/x" || msg.PID != 42 {
		t.Fatalf("got %+v", msg)
	}
}

func TestAwaitReadyChildDiedSilently(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	w.Close() // the child exited without saying anything

	if _, err := awaitReady(r, 2*time.Second); err == nil {
		t.Fatal("a silent child must be reported as a failure, not as success")
	}
}

func TestAwaitReadyTimesOut(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close() // held open: the child is up but wedged

	start := time.Now()
	_, err = awaitReady(r, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the timeout did not bound the wait: %s", elapsed)
	}
}
