package fusefs

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

// ---------------------------------------------------------------------------
// Integration point
//
// Everything the command line knows about the existing mount manager lives in
// the two variables below. If a name or a signature in fusefs.go differs from
// what is assumed here, this is the only place that needs editing — and the
// tests substitute fakes here, which is why the whole CLI can be tested on a
// machine with no FUSE at all.
// ---------------------------------------------------------------------------

// mountOnce resolves spec.Source into a fresh VFS, mounts it, and returns the
// mount point actually used together with a function that blocks until the
// mount ends. Passing an empty MountPoint leaves the naming to the manager.
var mountOnce = func(spec Spec) (mountPoint string, wait func(), err error) {
	m, err := MountSource(spec.Source, Options{
		MountPoint: spec.MountPoint,
		ReadOnly:   spec.ReadOnly,
		AllowOther: spec.AllowOther,
	})
	if err != nil {
		return "", nil, err
	}
	return m.MountPoint, m.Wait, nil
}

// unmountOwn ends a mount this process owns.
var unmountOwn = func(mountPoint string) error { return Unmount(mountPoint) }

// ---------------------------------------------------------------------------

// daemonReadyFDEnv carries the write end of the readiness pipe to the child.
const daemonReadyFDEnv = "F4_FUSE_READY_FD"

// readyMessage is the one line a daemon child writes back to its parent.
type readyMessage struct {
	OK         bool   `json:"ok"`
	MountPoint string `json:"mountpoint,omitempty"`
	Error      string `json:"error,omitempty"`
	PID        int    `json:"pid,omitempty"`
}

// RunCLI handles the mount-related part of f4's command line.
//
// It returns handled=false for every argument vector that says nothing about
// mounting, so main() can call it first and then carry on as usual:
//
//	if code, handled := fusefs.RunCLI(os.Args); handled {
//	        os.Exit(code)
//	}
func RunCLI(argv []string) (code int, handled bool) {
	cmd, spec, err := ParseArgs(argv)
	if cmd == CmdNone {
		return 0, false
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "f4: %v\n", err)
		return ExitUsage, true
	}
	switch cmd {
	case CmdMount:
		return runMount(spec, os.Stdout, os.Stderr), true
	case CmdUmount:
		return runUmount(spec, os.Stdout, os.Stderr), true
	case CmdList:
		return runList(spec, os.Stdout, os.Stderr), true
	}
	return 0, false
}

func runMount(spec *Spec, stdout, stderr io.Writer) int {
	if err := spec.Validate(); err != nil {
		fmt.Fprintf(stderr, "f4: %v\n", err)
		if errors.Is(err, ErrWriteNotImplemented) {
			return ExitUnsupported
		}
		return ExitUsage
	}
	if !Supported() {
		fmt.Fprintf(stderr, "f4: mounting is not supported on this platform\n")
		return ExitUnsupported
	}
	if spec.Daemon {
		return spawnDaemon(spec, stdout, stderr)
	}
	return mountAndServe(spec, stdout, stderr)
}

// mountAndServe is the body of a mount: in the foreground it is the whole
// command, in the background it is what the child does.
func mountAndServe(spec *Spec, stdout, stderr io.Writer) int {
	ready := readyPipe()

	mountPoint, wait, err := mountOnce(*spec)
	if err != nil {
		reportReady(ready, readyMessage{OK: false, Error: err.Error()})
		emit(stderr, spec.JSON, readyMessage{OK: false, Error: err.Error()},
			fmt.Sprintf("f4: mount %s: %v\n", spec.Source, err))
		return ExitFailed
	}

	rec, regErr := Register(Record{
		Source:     spec.Source,
		MountPoint: mountPoint,
		ReadOnly:   spec.ReadOnly,
		Detached:   ready != nil,
	})
	if regErr != nil {
		// A mount that works but is not listed is still a working mount.
		fmt.Fprintf(stderr, "f4: warning: could not record the mount: %v\n", regErr)
	}
	defer Deregister(rec.ID)

	reportReady(ready, readyMessage{OK: true, MountPoint: mountPoint, PID: os.Getpid()})
	emit(stdout, spec.JSON, readyMessage{OK: true, MountPoint: mountPoint, PID: os.Getpid()},
		mountPoint+"\n")

	waitOrSignal(wait, mountPoint)
	return ExitOK
}

// waitOrSignal blocks until the mount ends by itself or the process is asked
// to stop. Ctrl+C on a foreground mount has to unmount: leaving the mount
// point behind, owned by a process that just exited, hangs every program that
// walks into it.
func waitOrSignal(wait func(), mountPoint string) {
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigc)

	select {
	case <-done:
	case <-sigc:
		_ = unmountOwn(mountPoint)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			// A backend that will not let go must not keep the
			// process alive forever; the kernel connection dies
			// with us either way.
		}
	}
}

// spawnDaemon re-execs f4 to hold the mount, waits for the child to say it is
// up, and returns. Go cannot fork, so the child is a fresh process that is
// told what to do through its arguments and reports back through a pipe.
func spawnDaemon(spec *Spec, stdout, stderr io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "f4: cannot locate the f4 binary: %v\n", err)
		return ExitFailed
	}
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(stderr, "f4: %v\n", err)
		return ExitFailed
	}
	defer r.Close()

	cmd := exec.Command(exe, spec.ChildArgs()...)
	cmd.Env = append(os.Environ(), daemonReadyFDEnv+"=3")
	cmd.ExtraFiles = []*os.File{w} // becomes fd 3 in the child
	cmd.Stdin = nil
	cmd.Stdout = nil // the child's own output goes nowhere; it talks over the pipe
	cmd.Stderr = nil
	cmd.SysProcAttr = detachAttr()

	if err := cmd.Start(); err != nil {
		w.Close()
		fmt.Fprintf(stderr, "f4: cannot start the mount process: %v\n", err)
		return ExitFailed
	}
	// The parent must drop its copy, or the read below never sees EOF.
	w.Close()

	msg, err := awaitReady(r, spec.Timeout)
	if err != nil {
		_ = cmd.Process.Kill()
		fmt.Fprintf(stderr, "f4: mount %s: %v (try --foreground to see what happens)\n", spec.Source, err)
		return ExitFailed
	}
	if !msg.OK {
		emit(stderr, spec.JSON, msg, fmt.Sprintf("f4: mount %s: %s\n", spec.Source, msg.Error))
		return ExitFailed
	}
	// Let the child outlive us.
	_ = cmd.Process.Release()
	emit(stdout, spec.JSON, msg, msg.MountPoint+"\n")
	return ExitOK
}

// awaitReady reads the child's single status line, bounded in time.
func awaitReady(r *os.File, timeout time.Duration) (readyMessage, error) {
	type result struct {
		msg readyMessage
		err error
	}
	resc := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		if !scanner.Scan() {
			err := scanner.Err()
			if err == nil {
				err = errors.New("the mount process exited without reporting")
			}
			resc <- result{err: err}
			return
		}
		var msg readyMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			resc <- result{err: fmt.Errorf("unreadable status from the mount process: %w", err)}
			return
		}
		resc <- result{msg: msg}
	}()

	select {
	case res := <-resc:
		return res.msg, res.err
	case <-time.After(timeout):
		return readyMessage{}, fmt.Errorf("timed out after %s waiting for the mount to come up", timeout)
	}
}

// readyPipe returns the write end of the readiness pipe when this process is
// a daemon child, and nil when it is not.
func readyPipe() *os.File {
	v := os.Getenv(daemonReadyFDEnv)
	if v == "" {
		return nil
	}
	fd, err := strconv.Atoi(v)
	if err != nil || fd < 3 {
		return nil
	}
	return os.NewFile(uintptr(fd), "f4-ready")
}

func reportReady(w *os.File, msg readyMessage) {
	if w == nil {
		return
	}
	blob, err := json.Marshal(msg)
	if err == nil {
		_, _ = w.Write(append(blob, '\n'))
	}
	w.Close()
}

func runUmount(spec *Spec, stdout, stderr io.Writer) int {
	target := spec.Source
	if strings.TrimSpace(target) == "" {
		fmt.Fprintf(stderr, "f4: --umount needs a mount point\n")
		return ExitUsage
	}

	rec, found := FindMount(target)
	mountPoint := rec.MountPoint
	if !found {
		abs, err := filepath.Abs(target)
		if err != nil {
			fmt.Fprintf(stderr, "f4: %v\n", err)
			return ExitUsage
		}
		mountPoint = filepath.Clean(abs)
	}

	var err error
	switch {
	case found && rec.PID == os.Getpid():
		err = unmountOwn(mountPoint)
	default:
		// The mount belongs to another process, so it has to be taken
		// down the way any other tool would take it down.
		err = systemUnmount(mountPoint)
	}

	if err != nil {
		if isBusy(err) {
			emit(stderr, spec.JSON, readyMessage{Error: "busy", MountPoint: mountPoint},
				fmt.Sprintf("f4: %s is busy — something is still inside it\n", mountPoint))
			return ExitBusy
		}
		if !found {
			emit(stderr, spec.JSON, readyMessage{Error: err.Error(), MountPoint: mountPoint},
				fmt.Sprintf("f4: no mount at %s (%v)\n", mountPoint, err))
			return ExitNoSuchMount
		}
		emit(stderr, spec.JSON, readyMessage{Error: err.Error(), MountPoint: mountPoint},
			fmt.Sprintf("f4: unmount %s: %v\n", mountPoint, err))
		return ExitFailed
	}

	if found {
		_ = Deregister(rec.ID)
	}
	emit(stdout, spec.JSON, readyMessage{OK: true, MountPoint: mountPoint}, "")
	return ExitOK
}

// isBusy recognises EBUSY however it reaches us — as an errno from umount(2),
// or as the exit status and complaint of the fusermount helper.
func isBusy(err error) bool {
	if errors.Is(err, syscall.EBUSY) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "busy")
}

func runList(spec *Spec, stdout, stderr io.Writer) int {
	recs, err := Mounts()
	if err != nil {
		fmt.Fprintf(stderr, "f4: %v\n", err)
		return ExitFailed
	}
	if spec.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if recs == nil {
			recs = []Record{}
		}
		if err := enc.Encode(recs); err != nil {
			fmt.Fprintf(stderr, "f4: %v\n", err)
			return ExitFailed
		}
		return ExitOK
	}
	if len(recs) == 0 {
		return ExitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MOUNT POINT\tMODE\tPID\tAGE\tSOURCE")
	for _, r := range recs {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			r.MountPoint, r.Mode(), r.PID, r.Age().Truncate(time.Second), r.Source)
	}
	tw.Flush()
	return ExitOK
}

// emit writes either the JSON object or the human line, never both.
func emit(w io.Writer, asJSON bool, msg readyMessage, human string) {
	if asJSON {
		blob, err := json.Marshal(msg)
		if err == nil {
			fmt.Fprintf(w, "%s\n", blob)
		}
		return
	}
	if human != "" {
		fmt.Fprint(w, human)
	}
}
