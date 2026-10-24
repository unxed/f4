package panel

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type processEnvironmentShellPayload struct {
	token      string
	wire       []byte
	scriptPath string
	cleanup    func()
}

// processEnvironmentSerializedPTY is given to the ANSI parser and terminal
// view for their protocol replies. It delegates every operation to the real
// local terminal.PTY, but serializes writes with private environment delivery and holds
// replies while an update is awaiting acknowledgement.
type processEnvironmentSerializedPTY struct {
	owner   *PanelsFrame
	Backend terminal.PtyBackend
}

func (p *processEnvironmentSerializedPTY) Read(data []byte) (int, error) {
	return p.Backend.Read(data)
}

func (p *processEnvironmentSerializedPTY) Write(data []byte) (int, error) {
	return p.owner.writePTYAuxiliary(p.Backend, data)
}

func (p *processEnvironmentSerializedPTY) Close() error { return p.Backend.Close() }
func (p *processEnvironmentSerializedPTY) SetSize(cols, rows int) {
	p.Backend.SetSize(cols, rows)
}
func (p *processEnvironmentSerializedPTY) Wait() error { return p.Backend.Wait() }
func (p *processEnvironmentSerializedPTY) Run(name string, args ...string) error {
	return p.Backend.Run(name, args...)
}
func (p *processEnvironmentSerializedPTY) IsBusy() bool { return p.Backend.IsBusy() }
func (p *processEnvironmentSerializedPTY) SetSizePixels(cols, rows, xpixel, ypixel int) {
	if sizer, ok := p.Backend.(terminal.PtyPixelSizer); ok {
		sizer.SetSizePixels(cols, rows, xpixel, ypixel)
		return
	}
	p.Backend.SetSize(cols, rows)
}

type processEnvironmentShellInFlight struct {
	token      string
	generation uint64
	changes    []vfs.ProcessEnvironmentChange
	cleanup    func()
	muted      bool
	timeout    *time.Timer
}

var processEnvironmentAcknowledgementTimeout = 10 * time.Second
var processEnvironmentPayloadPreparer = prepareProcessEnvironmentShellPayload

var processEnvironmentFailureToastDuration = 5 * time.Second

// processEnvironmentFailureToast protects the active window and completion
// signal for the process-wide failure toast. Repeated failures can come from several frames;
// coalescing them prevents overlapping vtui toast timers from redrawing the
// same frame manager concurrently.
var processEnvironmentFailureToast struct {
	sync.Mutex
	active bool
	done   chan struct{}
}

func finishProcessEnvironmentFailureToast(done chan struct{}) {
	processEnvironmentFailureToast.Lock()
	if processEnvironmentFailureToast.done == done {
		processEnvironmentFailureToast.active = false
		close(done)
	}
	processEnvironmentFailureToast.Unlock()
}

var (
	processEnvironmentRuntimeOnce sync.Once
	processEnvironmentRuntimeDir  string
	processEnvironmentRuntimeRoot string
	processEnvironmentRuntimeErr  error
)

func InitializeProcessEnvironmentRuntime() error {
	processEnvironmentRuntimeOnce.Do(func() {
		root := filepath.Join(config.GetF4ConfigDir(), "plugins", ".envman-runtime")
		// The session is created once and lives for the whole process, so the
		// root it was created under has to be remembered rather than worked
		// out again later. config.GetF4ConfigDir's answer is not a constant: the
		// config directory is a package variable that tests swap for a
		// temporary one, and shutdown recomputing the root would then compare
		// the session against a directory it was never in, decide the path was
		// unexpected and refuse to remove it.
		processEnvironmentRuntimeRoot = filepath.Clean(root)
		processEnvironmentRuntimeDir, processEnvironmentRuntimeErr = createProcessEnvironmentRuntimeSession(root)
	})
	if processEnvironmentRuntimeErr == nil && processEnvironmentRuntimeDir != "" {
		if err := os.MkdirAll(processEnvironmentRuntimeDir, 0o700); err != nil {
			return err
		}
		// #nosec G302 -- processEnvironmentRuntimeDir is a directory and needs owner execute permission for traversal.
		if err := os.Chmod(processEnvironmentRuntimeDir, 0o700); err != nil {
			return err
		}
	}
	return processEnvironmentRuntimeErr
}

func createProcessEnvironmentRuntimeSession(root string) (string, error) {
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("private environment runtime path is a symbolic link")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	// #nosec G302 -- root is a private directory and needs owner execute permission for traversal.
	if err := os.Chmod(root, 0o700); err != nil {
		return "", err
	}
	// Session names include their owning PID and an unguessable token. Remove
	// only well-formed sessions whose owner is positively known to be dead;
	// an inaccessible or reused PID is deliberately left alone.
	sweepStaleProcessEnvironmentRuntimeSessions(root)
	token, err := newProcessEnvironmentToken()
	if err != nil {
		return "", err
	}
	sessionDir := filepath.Join(root, fmt.Sprintf("%d-%s", os.Getpid(), token))
	if err := os.Mkdir(sessionDir, 0o700); err != nil {
		return "", err
	}
	// #nosec G302 -- sessionDir is a private directory and needs owner execute permission for traversal.
	if err := os.Chmod(sessionDir, 0o700); err != nil {
		_ = os.Remove(sessionDir)
		return "", err
	}
	return sessionDir, nil
}

func parseProcessEnvironmentRuntimeSessionName(name string) (int, bool) {
	pidText, token, ok := strings.Cut(name, "-")
	if !ok || len(token) != 32 {
		return 0, false
	}
	for _, ch := range token {
		if (ch < '0' || ch > '9') && (ch < 'A' || ch > 'F') && (ch < 'a' || ch > 'f') {
			return 0, false
		}
	}
	pid, err := strconv.Atoi(pidText)
	return pid, err == nil && pid > 0
}

func removeProcessEnvironmentRuntimeSession(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// Runtime transport creates files only. Refuse unexpected directories
		// instead of recursively following a path placed by another process.
		if entry.IsDir() {
			return fmt.Errorf("unexpected directory in private environment runtime session")
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func sweepStaleProcessEnvironmentRuntimeSessions(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, ok := parseProcessEnvironmentRuntimeSessionName(entry.Name())
		if !ok {
			continue
		}
		alive, known := terminal.ProcessEnvironmentProcessState(pid)
		if !known || alive {
			continue
		}
		if err := removeProcessEnvironmentRuntimeSession(filepath.Join(root, entry.Name())); err != nil {
			vtui.DebugLog("ENV: could not remove a stale private runtime session: %v", err)
		}
	}
}

func ShutdownProcessEnvironmentRuntime() {
	if vtui.FrameManager != nil {
		for _, screen := range vtui.FrameManager.Screens {
			if screen == nil {
				continue
			}
			for _, frame := range screen.Frames {
				if pf, ok := frame.(*PanelsFrame); ok && pf != nil {
					pf.closeProcessEnvironmentShell()
				}
			}
		}
	}
	if processEnvironmentRuntimeDir == "" {
		return
	}
	root := processEnvironmentRuntimeRoot
	dir := filepath.Clean(processEnvironmentRuntimeDir)
	pid, validName := parseProcessEnvironmentRuntimeSessionName(filepath.Base(dir))
	if filepath.Dir(dir) != root || !validName || pid != os.Getpid() {
		vtui.DebugLog("ENV: refused to remove an unexpected private runtime session path")
		return
	}
	if err := removeProcessEnvironmentRuntimeSession(dir); err != nil {
		vtui.DebugLog("ENV: could not remove the private runtime session: %v", err)
	}
}

func BroadcastProcessEnvironmentGenerations(generations []terminal.ProcessEnvironmentGeneration) {
	if len(generations) == 0 || vtui.FrameManager == nil {
		return
	}
	copyOfGenerations := make([]terminal.ProcessEnvironmentGeneration, len(generations))
	for i, generation := range generations {
		copyOfGenerations[i] = terminal.ProcessEnvironmentGeneration{
			Generation: generation.Generation,
			Changes:    terminal.CloneProcessEnvironmentChanges(generation.Changes),
		}
	}
	// FrameManager owns workspace topology on the UI goroutine. Posting also
	// prevents a plugin macro running in the background from racing a tab close.
	vtui.FrameManager.PostTask(func() {
		for _, screen := range vtui.FrameManager.Screens {
			if screen == nil {
				continue
			}
			for _, frame := range screen.Frames {
				pf, ok := frame.(*PanelsFrame)
				if !ok || pf == nil {
					continue
				}
				for _, generation := range copyOfGenerations {
					pf.queueProcessEnvironment(generation.Generation, generation.Changes, true)
				}
			}
		}
	})
}

func (pf *PanelsFrame) noteLocalShellBusy(busy bool) {
	pf.processEnvironmentMu.Lock()
	pf.processEnvironmentBusy = busy
	pf.processEnvironmentMu.Unlock()
}

func (pf *PanelsFrame) localShellIsActive() bool {
	if fsp := pf.GetActivePanel(); fsp != nil {
		if _, remote := fsp.Vfs.(vfs.PtyProvider); remote && vfsHasRemotePTY(fsp.Vfs) {
			return false
		}
	}
	return true
}

func (pf *PanelsFrame) queueProcessEnvironment(generation uint64, changes []vfs.ProcessEnvironmentChange, flushIfIdle bool) {
	pf.processEnvironmentMu.Lock()
	if pf.processEnvironmentClosed {
		pf.processEnvironmentMu.Unlock()
		return
	}
	if len(changes) == 0 {
		if len(pf.pendingProcessEnvironment) == 0 && pf.processEnvironmentInFlight == nil && generation > pf.processEnvironmentGeneration {
			pf.processEnvironmentGeneration = generation
		}
		pf.processEnvironmentMu.Unlock()
		return
	}
	if generation <= pf.processEnvironmentGeneration && len(pf.pendingProcessEnvironment) == 0 {
		pf.processEnvironmentMu.Unlock()
		return
	}
	pf.pendingProcessEnvironment = terminal.CoalesceProcessEnvironmentChanges(append(
		pf.pendingProcessEnvironment,
		terminal.CloneProcessEnvironmentChanges(changes)...,
	))
	if generation > pf.pendingProcessEnvironmentGeneration {
		pf.pendingProcessEnvironmentGeneration = generation
	}
	pf.processEnvironmentMu.Unlock()

	if flushIfIdle {
		pf.flushProcessEnvironment()
	}
}

func (pf *PanelsFrame) catchUpProcessEnvironment(flushIfIdle bool) {
	pf.processEnvironmentMu.Lock()
	after := pf.processEnvironmentGeneration
	if pf.pendingProcessEnvironmentGeneration > after {
		after = pf.pendingProcessEnvironmentGeneration
	}
	if pf.processEnvironmentInFlight != nil && pf.processEnvironmentInFlight.generation > after {
		after = pf.processEnvironmentInFlight.generation
	}
	pf.processEnvironmentMu.Unlock()
	generation, changes := terminal.GlobalProcessEnvironment.ChangesSince(after)
	pf.queueProcessEnvironment(generation, changes, false)
	// A prompt marker must also retry work that was already pending (for
	// example after an earlier write failure), even when there is no newer
	// process generation to queue.
	if flushIfIdle {
		pf.flushProcessEnvironment()
	}
}

func (pf *PanelsFrame) localShellStarted(inheritedGeneration uint64) {
	pf.processEnvironmentMu.Lock()
	if inheritedGeneration > pf.processEnvironmentGeneration {
		pf.processEnvironmentGeneration = inheritedGeneration
	}
	pf.processEnvironmentMu.Unlock()
	pf.catchUpProcessEnvironment(true)
}

func (pf *PanelsFrame) isLocalPTY(pty terminal.PtyBackend) bool {
	if pty == nil {
		return false
	}
	pf.PtyMutex.Lock()
	defer pf.PtyMutex.Unlock()
	return pf.Pty == pty
}

func (pf *PanelsFrame) flushProcessEnvironment() {
	pf.processEnvironmentWriteMu.Lock()
	defer pf.processEnvironmentWriteMu.Unlock()
	pf.flushProcessEnvironmentLocked(pf.localPTY())
}

func (pf *PanelsFrame) flushProcessEnvironmentLocked(pty terminal.PtyBackend) bool {
	if pty == nil {
		return false
	}
	pf.processEnvironmentMu.Lock()
	busy := pf.processEnvironmentBusy
	hasPending := len(pf.pendingProcessEnvironment) > 0
	inFlight := pf.processEnvironmentInFlight != nil
	pf.processEnvironmentMu.Unlock()
	if !hasPending || inFlight {
		return false
	}
	if busy || pty.IsBusy() {
		return false
	}

	pf.processEnvironmentMu.Lock()
	changes := terminal.CloneProcessEnvironmentChanges(pf.pendingProcessEnvironment)
	generation := pf.pendingProcessEnvironmentGeneration
	pf.pendingProcessEnvironment = nil
	pf.pendingProcessEnvironmentGeneration = 0
	pf.processEnvironmentMu.Unlock()

	payload, err := processEnvironmentPayloadPreparer(changes)
	if err != nil {
		pf.requeueProcessEnvironment(generation, changes)
		pf.setProcessEnvironmentDeliveryFailed(true)
		vtui.DebugLog("ENV: failed to prepare private shell update for %d variables: %v", len(changes), err)
		pf.reportProcessEnvironmentShellFailure()
		return false
	}
	active := pf.localShellIsActive()
	if active && pf.TermView != nil {
		pf.TermView.SetMuted(true)
	}
	inFlightUpdate := &processEnvironmentShellInFlight{
		token:      payload.token,
		generation: generation,
		changes:    terminal.CloneProcessEnvironmentChanges(changes),
		cleanup:    payload.cleanup,
		muted:      active,
	}
	pf.processEnvironmentMu.Lock()
	pf.processEnvironmentInFlight = inFlightUpdate
	inFlightUpdate.timeout = time.AfterFunc(processEnvironmentAcknowledgementTimeout, func() {
		pf.completeProcessEnvironmentShellUpdate(inFlightUpdate.token, false)
	})
	pf.processEnvironmentMu.Unlock()
	_, err = writeProcessEnvironmentPayload(pty, payload.wire)
	if err != nil {
		pf.processEnvironmentMu.Lock()
		if pf.processEnvironmentInFlight == inFlightUpdate {
			pf.processEnvironmentInFlight = nil
		}
		pf.processEnvironmentDeliveryFailed = true
		if inFlightUpdate.timeout != nil {
			inFlightUpdate.timeout.Stop()
		}
		pf.processEnvironmentMu.Unlock()
		if payload.cleanup != nil {
			payload.cleanup()
		}
		if active && pf.TermView != nil {
			pf.TermView.SetMuted(false)
		}
		pf.requeueProcessEnvironment(generation, changes)
		vtui.DebugLog("ENV: failed to write private shell update for %d variables: %v", len(changes), err)
		pf.reportProcessEnvironmentShellFailure()
		return false
	}
	pf.setProcessEnvironmentDeliveryFailed(false)

	// Delivery is acknowledged asynchronously by a value-free OSC marker.
	// Generation intentionally does not advance on write completion alone.
	return true
}

func (pf *PanelsFrame) requeueProcessEnvironment(generation uint64, changes []vfs.ProcessEnvironmentChange) {
	pf.processEnvironmentMu.Lock()
	// These changes happened before anything queued while the write was in
	// progress; coalescing keeps the newer assignment when names overlap.
	pf.pendingProcessEnvironment = terminal.CoalesceProcessEnvironmentChanges(append(
		terminal.CloneProcessEnvironmentChanges(changes),
		pf.pendingProcessEnvironment...,
	))
	if generation > pf.pendingProcessEnvironmentGeneration {
		pf.pendingProcessEnvironmentGeneration = generation
	}
	pf.processEnvironmentMu.Unlock()
}

func (pf *PanelsFrame) setProcessEnvironmentDeliveryFailed(failed bool) {
	pf.processEnvironmentMu.Lock()
	pf.processEnvironmentDeliveryFailed = failed
	pf.processEnvironmentMu.Unlock()
}

func (pf *PanelsFrame) reportProcessEnvironmentShellFailure() {
	manager := vtui.FrameManager
	if manager == nil {
		return
	}

	processEnvironmentFailureToast.Lock()
	if processEnvironmentFailureToast.active {
		processEnvironmentFailureToast.Unlock()
		return
	}
	processEnvironmentFailureToast.active = true
	done := make(chan struct{})
	processEnvironmentFailureToast.done = done
	processEnvironmentFailureToast.Unlock()

	manager.PostTask(func() {
		if vtui.FrameManager != manager {
			finishProcessEnvironmentFailureToast(done)
			return
		}
		// The localized title is intentionally value-free: neither environment
		// names nor values may be copied to terminal or diagnostic output.
		toastDuration := toast.Show(i18n.Msg("EnvMan.ShellSyncError"), processEnvironmentFailureToastDuration)
		// ShowToast posts its setup. Queue our timer behind that setup so the
		// coalescing window cannot end before vtui's own expiry timer.
		manager.PostTask(func() {
			time.AfterFunc(toastDuration, func() {
				finishProcessEnvironmentFailureToast(done)
			})
		})
	})
}

func (pf *PanelsFrame) WritePTY(pty terminal.PtyBackend, data []byte) (int, error) {
	if bytes.ContainsAny(data, "\r\n") || bytes.Equal(data, []byte("\x1b[200~")) || bytes.Equal(data, []byte("\x1b[201~")) {
		vtui.DebugLog("F4#2180 WritePTY input bytes: % x (%q)", data, data)
	}
	if pf.Parser != nil && bytes.HasPrefix(data, terminal.WindowsSyncCommandPrefix) {
		pf.Parser.ExpectWindowsSyncEcho()
	}
	if pf.Parser != nil && bytes.HasPrefix(data, terminal.UnixSyncCommandPrefix) {
		pf.Parser.ExpectUnixSyncEcho()
	}
	if !pf.isLocalPTY(pty) {
		return pty.Write(data)
	}
	// A frame created concurrently with Apply may not have appeared in
	// FrameManager when the broadcast ran. Catch up at every local input
	// boundary, and serialize it with the user's bytes.
	pf.catchUpProcessEnvironment(false)
	pf.processEnvironmentWriteMu.Lock()
	defer pf.processEnvironmentWriteMu.Unlock()
	pf.flushProcessEnvironmentLocked(pty)
	pf.processEnvironmentMu.Lock()
	if pf.processEnvironmentInFlight != nil || pf.processEnvironmentDeliveryFailed {
		pf.deferredProcessEnvironmentInput = append(pf.deferredProcessEnvironmentInput, data...)
		pf.processEnvironmentMu.Unlock()
		return len(data), nil
	}
	pf.processEnvironmentMu.Unlock()
	n, err := pty.Write(data)
	pf.noteLocalShellCommandInput(data[:n], err)
	return n, err
}

func (pf *PanelsFrame) writePTYAuxiliary(pty terminal.PtyBackend, data []byte) (int, error) {
	if !pf.isLocalPTY(pty) {
		return pty.Write(data)
	}
	pf.processEnvironmentWriteMu.Lock()
	defer pf.processEnvironmentWriteMu.Unlock()
	pf.processEnvironmentMu.Lock()
	if pf.processEnvironmentInFlight != nil {
		pf.deferredProcessEnvironmentInput = append(pf.deferredProcessEnvironmentInput, data...)
		pf.processEnvironmentMu.Unlock()
		return len(data), nil
	}
	pf.processEnvironmentMu.Unlock()
	return pty.Write(data)
}

func (pf *PanelsFrame) noteLocalShellCommandInput(data []byte, err error) {
	if err == nil && (bytes.IndexByte(data, '\r') >= 0 || bytes.IndexByte(data, '\n') >= 0) {
		pf.noteLocalShellBusy(true)
	}
}

const processEnvironmentMarkerPrefix = "\x1b]133;"

var processEnvironmentRunOnUI = func(task func()) {
	if frameManager := vtui.FrameManager; frameManager != nil {
		frameManager.PostTask(task)
		return
	}
	task()
}

func (pf *PanelsFrame) processEnvironmentShellOutput(data []byte) {
	pf.processEnvironmentMu.Lock()
	combined := append(append([]byte(nil), pf.processEnvironmentOutputTail...), data...)
	pf.processEnvironmentOutputTail = nil
	var markers []string
	for len(combined) > 0 {
		start := bytes.Index(combined, []byte(processEnvironmentMarkerPrefix))
		if start < 0 {
			keep := len(processEnvironmentMarkerPrefix) - 1
			if keep > len(combined) {
				keep = len(combined)
			}
			pf.processEnvironmentOutputTail = append([]byte(nil), combined[len(combined)-keep:]...)
			break
		}
		payloadStart := start + len(processEnvironmentMarkerPrefix)
		remaining := combined[payloadStart:]
		belEnd := bytes.IndexByte(remaining, '\a')
		stEnd := bytes.Index(remaining, []byte{'\x1b', '\\'})
		end := belEnd
		terminatorSize := 1
		if end < 0 || (stEnd >= 0 && stEnd < end) {
			end = stEnd
			terminatorSize = 2
		}
		if end < 0 {
			// Tokens and status are tiny. A malformed terminal sequence must not
			// retain an unbounded amount of unrelated command output.
			candidate := combined[start:]
			if len(candidate) > 256 {
				candidate = candidate[:256]
			}
			pf.processEnvironmentOutputTail = append([]byte(nil), candidate...)
			break
		}
		end += payloadStart
		markers = append(markers, string(combined[payloadStart:end]))
		combined = combined[end+terminatorSize:]
	}
	pf.processEnvironmentMu.Unlock()

	for _, marker := range markers {
		switch {
		case marker == "C":
			pf.noteLocalShellBusy(true)
		case marker == "D" || strings.HasPrefix(marker, "D;"):
			pf.noteLocalShellBusy(false)
			processEnvironmentRunOnUI(func() { pf.catchUpProcessEnvironment(true) })
		case strings.HasPrefix(marker, "E;"):
			parts := strings.Split(strings.TrimPrefix(marker, "E;"), ";")
			if len(parts) == 2 && (parts[1] == "0" || parts[1] == "1") {
				pf.completeProcessEnvironmentShellUpdate(parts[0], parts[1] == "0")
			}
		}
	}
}

func (pf *PanelsFrame) completeProcessEnvironmentShellUpdate(token string, success bool) {
	pf.processEnvironmentMu.Lock()
	inFlight := pf.processEnvironmentInFlight
	if inFlight == nil || inFlight.token != token {
		pf.processEnvironmentMu.Unlock()
		return
	}
	pf.processEnvironmentInFlight = nil
	if inFlight.timeout != nil {
		inFlight.timeout.Stop()
	}
	if success && inFlight.generation > pf.processEnvironmentGeneration {
		pf.processEnvironmentGeneration = inFlight.generation
	}
	pf.processEnvironmentDeliveryFailed = !success
	if !success {
		// Restore the rejected update before publishing the idle state. Readers
		// must never observe neither an in-flight nor a pending delivery.
		pf.pendingProcessEnvironment = terminal.CoalesceProcessEnvironmentChanges(append(
			terminal.CloneProcessEnvironmentChanges(inFlight.changes),
			pf.pendingProcessEnvironment...,
		))
		if inFlight.generation > pf.pendingProcessEnvironmentGeneration {
			pf.pendingProcessEnvironmentGeneration = inFlight.generation
		}
	}
	closed := pf.processEnvironmentClosed
	pf.processEnvironmentMu.Unlock()

	if inFlight.cleanup != nil {
		inFlight.cleanup()
	}
	if !success {
		vtui.DebugLog("ENV: local shell rejected private update for %d variables", len(inFlight.changes))
	}

	release := func() {
		if inFlight.muted && pf.TermView != nil {
			pf.TermView.SetMuted(false)
		}
		if success {
			pf.releaseDeferredProcessEnvironmentInput(true)
		} else {
			pf.reportProcessEnvironmentShellFailure()
		}
	}
	if closed {
		if inFlight.muted && pf.TermView != nil {
			pf.TermView.SetMuted(false)
		}
		return
	}
	processEnvironmentRunOnUI(release)
}

func (pf *PanelsFrame) releaseDeferredProcessEnvironmentInput(flushPending bool) {
	pf.processEnvironmentWriteMu.Lock()
	defer pf.processEnvironmentWriteMu.Unlock()
	pty := pf.localPTY()
	if pty == nil {
		return
	}
	if flushPending {
		pf.flushProcessEnvironmentLocked(pty)
	}
	pf.processEnvironmentMu.Lock()
	if pf.processEnvironmentInFlight != nil || pf.processEnvironmentDeliveryFailed || len(pf.pendingProcessEnvironment) > 0 {
		pf.processEnvironmentMu.Unlock()
		return
	}
	data := append([]byte(nil), pf.deferredProcessEnvironmentInput...)
	pf.deferredProcessEnvironmentInput = nil
	pf.processEnvironmentMu.Unlock()
	if len(data) == 0 {
		return
	}
	if _, err := writeProcessEnvironmentPayload(pty, data); err != nil {
		vtui.DebugLog("ENV: failed to release %d bytes of deferred local shell input: %v", len(data), err)
	} else {
		pf.noteLocalShellCommandInput(data, nil)
	}
}

func (pf *PanelsFrame) closeProcessEnvironmentShell() {
	pf.processEnvironmentMu.Lock()
	pf.processEnvironmentClosed = true
	pf.pendingProcessEnvironment = nil
	pf.processEnvironmentDeliveryFailed = false
	pf.deferredProcessEnvironmentInput = nil
	pf.processEnvironmentOutputTail = nil
	inFlight := pf.processEnvironmentInFlight
	pf.processEnvironmentInFlight = nil
	if inFlight != nil && inFlight.timeout != nil {
		inFlight.timeout.Stop()
	}
	pf.processEnvironmentMu.Unlock()
	if inFlight != nil {
		if inFlight.cleanup != nil {
			inFlight.cleanup()
		}
		if inFlight.muted && pf.TermView != nil {
			pf.TermView.SetMuted(false)
		}
	}
}

func writeProcessEnvironmentPayload(writer io.Writer, data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		n, err := writer.Write(data)
		total += n
		data = data[n:]
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func prepareProcessEnvironmentShellPayload(changes []vfs.ProcessEnvironmentChange) (processEnvironmentShellPayload, error) {
	if err := InitializeProcessEnvironmentRuntime(); err != nil {
		return processEnvironmentShellPayload{}, err
	}
	if runtime.GOOS == "windows" {
		return prepareWindowsProcessEnvironmentShellPayload(changes)
	}
	return preparePOSIXProcessEnvironmentShellPayload(changes)
}

func posixProcessEnvironmentQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func posixProcessEnvironmentScript(changes []vfs.ProcessEnvironmentChange, token string) []byte {
	var script strings.Builder
	statusName := "__F4_ENV_STATUS_" + token
	xtraceName := "__F4_ENV_XTRACE_" + token
	fmt.Fprintf(&script, "%s=0\ncase $- in *x*) %s=1; set +x ;; *) %s=0 ;; esac\n", statusName, xtraceName, xtraceName)
	for _, change := range changes {
		if change.Unset {
			fmt.Fprintf(&script, "unset %s || %s=1\n", change.Name, statusName)
		} else {
			fmt.Fprintf(&script, "export %s=%s || %s=1\n", change.Name, posixProcessEnvironmentQuote(change.Value), statusName)
		}
	}
	// Finish with the source command's status. The value-free wrapper emits
	// the acknowledgement so a missing or unreadable script is also a failure.
	fmt.Fprintf(&script, "if [ \"$%s\" -eq 0 ]; then [ \"$%s\" -eq 1 ] && set -x; unset %s %s; true; else [ \"$%s\" -eq 1 ] && set -x; unset %s %s; false; fi\n",
		statusName, xtraceName, statusName, xtraceName, xtraceName, statusName, xtraceName)
	return []byte(script.String())
}

func posixProcessEnvironmentCommand(scriptPath, token string) []byte {
	quotedPath := posixProcessEnvironmentQuote(scriptPath)
	statusName := "__F4_ENV_WRAPPER_" + token
	verboseName := "__F4_ENV_VERBOSE_" + token
	return []byte(fmt.Sprintf(" case $- in *v*) %s=1; set +v ;; *) %s=0 ;; esac; if [ -r %s ]; then if . %s; then %s=0; else %s=$?; fi; else %s=1; fi; rm -f %s || :; if [ \"$%s\" -eq 1 ]; then set -v; fi; if [ \"$%s\" -eq 0 ]; then printf '\\033]133;E;%s;0\\007'; else printf '\\033]133;E;%s;1\\007'; fi; unset %s %s\r",
		verboseName, verboseName, quotedPath, quotedPath, statusName, statusName, statusName, quotedPath, verboseName, statusName, token, token, statusName, verboseName))
}

func preparePOSIXProcessEnvironmentShellPayload(changes []vfs.ProcessEnvironmentChange) (processEnvironmentShellPayload, error) {
	token, err := newProcessEnvironmentToken()
	if err != nil {
		return processEnvironmentShellPayload{}, err
	}
	scriptPath, err := writePrivateProcessEnvironmentFile("env-posix-*", posixProcessEnvironmentScript(changes, token))
	if err != nil {
		return processEnvironmentShellPayload{}, err
	}
	cleanup := func() { _ = os.Remove(scriptPath) }
	return processEnvironmentShellPayload{
		token:      token,
		wire:       posixProcessEnvironmentCommand(scriptPath, token),
		scriptPath: scriptPath,
		cleanup:    cleanup,
	}, nil
}

func newProcessEnvironmentToken() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(random[:])), nil
}

func writePrivateProcessEnvironmentFile(pattern string, data []byte) (string, error) {
	if err := InitializeProcessEnvironmentRuntime(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(processEnvironmentRuntimeDir, 0o700); err != nil {
		return "", err
	}
	File, err := os.CreateTemp(processEnvironmentRuntimeDir, pattern)
	if err != nil {
		return "", err
	}
	path := File.Name()
	_ = File.Chmod(0o600)
	if _, err = writeProcessEnvironmentPayload(File, data); err == nil {
		err = File.Close()
	} else {
		_ = File.Close()
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func cmdProcessEnvironmentPath(path string) string {
	return strings.ReplaceAll(path, `"`, `""`)
}

const windowsProcessEnvironmentChunkBytes = 512

func windowsProcessEnvironmentValueChunks(value string) [][]byte {
	chunks := make([][]byte, 0, (len(value)+windowsProcessEnvironmentChunkBytes-1)/windowsProcessEnvironmentChunkBytes)
	for len(value) > 0 {
		size := windowsProcessEnvironmentChunkBytes
		if size > len(value) {
			size = len(value)
		} else {
			for size > 0 && !utf8.RuneStart(value[size]) {
				size--
			}
			if size == 0 {
				size = windowsProcessEnvironmentChunkBytes
			}
		}
		chunks = append(chunks, []byte(value[:size]))
		value = value[size:]
	}
	return chunks
}

func windowsProcessEnvironmentScript(changes []vfs.ProcessEnvironmentChange, valuePaths [][]string, successPath, failurePath, codePageName, statusName, chunkName string) []byte {
	var script strings.Builder
	modeName := "F4_ENV_DELAYED_" + strings.TrimPrefix(statusName, "F4_ENV_STATUS_")
	probeName := "F4_ENV_PROBE_" + strings.TrimPrefix(statusName, "F4_ENV_STATUS_")
	script.WriteString("@echo off\r\n")
	fmt.Fprintf(&script, "set \"%s=0\"\r\n", statusName)
	fmt.Fprintf(&script, "if not exist \"%s\" set \"%s=1\"\r\n", cmdProcessEnvironmentPath(successPath), statusName)
	fmt.Fprintf(&script, "if not exist \"%s\" set \"%s=1\"\r\n", cmdProcessEnvironmentPath(failurePath), statusName)
	for _, paths := range valuePaths {
		for _, path := range paths {
			fmt.Fprintf(&script, "if not exist \"%s\" set \"%s=1\"\r\n", cmdProcessEnvironmentPath(path), statusName)
		}
	}
	fmt.Fprintf(&script, "for /f \"tokens=2 delims=:\" %%%%F in ('chcp') do set \"%s=%%%%F\"\r\n", codePageName)
	fmt.Fprintf(&script, "chcp 65001 >nul || set \"%s=1\"\r\n", statusName)
	fmt.Fprintf(&script, "set \"%s=OFF\"\r\nset \"%s=!\"\r\nif not defined %s set \"%s=ON\"\r\nset \"%s=\"\r\n", modeName, probeName, probeName, modeName, probeName)
	for i, change := range changes {
		var paths []string
		if len(valuePaths) > 0 {
			paths = valuePaths[0]
			valuePaths = valuePaths[1:]
		}
		fmt.Fprintf(&script, "set \"%s=\"\r\n", change.Name)
		if change.Unset || change.Value == "" {
			continue
		}
		labelToken := strings.TrimPrefix(statusName, "F4_ENV_STATUS_") + fmt.Sprintf("_%d", i)
		transferName := "F4_ENV_TRANSFER_" + labelToken
		localFailureName := "F4_ENV_LOCALFAIL_" + labelToken
		fmt.Fprintf(&script, "if \"%%%s%%\"==\"ON\" goto :F4_ENV_ON_%s\r\ngoto :F4_ENV_OFF_%s\r\n", modeName, labelToken, labelToken)
		fmt.Fprintf(&script, ":F4_ENV_ON_%s\r\n", labelToken)
		for _, path := range paths {
			fmt.Fprintf(&script, "set \"%s=\"\r\n", chunkName)
			fmt.Fprintf(&script, "set /p \"%s=\" < \"%s\" || set \"%s=1\"\r\n", chunkName, cmdProcessEnvironmentPath(path), statusName)
			fmt.Fprintf(&script, "set \"%s=!%s!!%s!\"\r\n", change.Name, change.Name, chunkName)
		}
		fmt.Fprintf(&script, "goto :F4_ENV_DONE_%s\r\n", labelToken)

		fmt.Fprintf(&script, ":F4_ENV_OFF_%s\r\nset \"%s=\"\r\nsetlocal EnableDelayedExpansion\r\nset \"%s=\"\r\nset \"%s=0\"\r\n",
			labelToken, transferName, change.Name, localFailureName)
		for _, path := range paths {
			fmt.Fprintf(&script, "set \"%s=\"\r\n", chunkName)
			fmt.Fprintf(&script, "set /p \"%s=\" < \"%s\" || set \"%s=1\"\r\n", chunkName, cmdProcessEnvironmentPath(path), localFailureName)
			fmt.Fprintf(&script, "set \"%s=!%s!!%s!\"\r\n", change.Name, change.Name, chunkName)
		}
		eol := '#'
		first, _ := utf8.DecodeRuneInString(change.Value)
		if first == eol {
			eol = '@'
		}
		fmt.Fprintf(&script, "for /F \"delims= eol=%c\" %%%%A in (\"!%s!\") do endlocal & set \"%s=%%%%A\" & set \"%s=1\" & if \"!%s!\"==\"1\" set \"%s=1\"\r\n",
			eol, change.Name, change.Name, transferName, localFailureName, statusName)
		fmt.Fprintf(&script, "if not defined %s (endlocal & set \"%s=1\")\r\nset \"%s=\"\r\n", transferName, statusName, transferName)
		fmt.Fprintf(&script, ":F4_ENV_DONE_%s\r\n", labelToken)
	}
	fmt.Fprintf(&script, "chcp %%%s%% >nul || set \"%s=1\"\r\n", codePageName, statusName)
	fmt.Fprintf(&script, "set \"%s=\"\r\n", codePageName)
	fmt.Fprintf(&script, "set \"%s=\"\r\n", chunkName)
	fmt.Fprintf(&script, "set \"%s=\"\r\n", modeName)
	fmt.Fprintf(&script, "if \"%%%s%%\"==\"0\" (\r\nset \"%s=\"\r\ntype \"%s\" || (type \"%s\" & exit /b 1)\r\nexit /b 0\r\n) else (\r\nset \"%s=\"\r\ntype \"%s\"\r\nexit /b 1\r\n)\r\n",
		statusName, statusName, cmdProcessEnvironmentPath(successPath), cmdProcessEnvironmentPath(failurePath), statusName, cmdProcessEnvironmentPath(failurePath))
	return []byte(script.String())
}

func windowsProcessEnvironmentCommand(scriptPath, failurePath string) []byte {
	quotedScript := cmdProcessEnvironmentPath(scriptPath)
	quotedFailure := cmdProcessEnvironmentPath(failurePath)
	return []byte(fmt.Sprintf("@call \"%s\" || @type \"%s\"\r@del /q \"%s\"\r", quotedScript, quotedFailure, quotedScript))
}

func prepareWindowsProcessEnvironmentShellPayload(changes []vfs.ProcessEnvironmentChange) (processEnvironmentShellPayload, error) {
	markerToken, err := newProcessEnvironmentToken()
	if err != nil {
		return processEnvironmentShellPayload{}, err
	}
	codePageName := "F4_ENV_CP_" + markerToken
	statusName := "F4_ENV_STATUS_" + markerToken
	chunkName := "F4_ENV_CHUNK_" + markerToken
	valuePaths := make([][]string, len(changes))
	var privatePaths []string
	cleanupPaths := func() {
		for _, path := range privatePaths {
			_ = os.Remove(path)
		}
	}
	for i, change := range changes {
		if change.Unset || change.Value == "" {
			continue
		}
		for _, chunk := range windowsProcessEnvironmentValueChunks(change.Value) {
			path, err := writePrivateProcessEnvironmentFile("f4-env-value-*", chunk)
			if err != nil {
				cleanupPaths()
				return processEnvironmentShellPayload{}, err
			}
			privatePaths = append(privatePaths, path)
			valuePaths[i] = append(valuePaths[i], path)
		}
	}
	successPath, err := writePrivateProcessEnvironmentFile("f4-env-ok-*", []byte("\x1b]133;E;"+markerToken+";0\x07"))
	if err != nil {
		cleanupPaths()
		return processEnvironmentShellPayload{}, err
	}
	privatePaths = append(privatePaths, successPath)
	failurePath, err := writePrivateProcessEnvironmentFile("f4-env-fail-*", []byte("\x1b]133;E;"+markerToken+";1\x07"))
	if err != nil {
		cleanupPaths()
		return processEnvironmentShellPayload{}, err
	}
	privatePaths = append(privatePaths, failurePath)
	script := windowsProcessEnvironmentScript(changes, valuePaths, successPath, failurePath, codePageName, statusName, chunkName)
	scriptPath, err := writePrivateProcessEnvironmentFile("f4-env-script-*.cmd", script)
	if err != nil {
		cleanupPaths()
		return processEnvironmentShellPayload{}, err
	}
	privatePaths = append(privatePaths, scriptPath)
	wire := windowsProcessEnvironmentCommand(scriptPath, failurePath)
	return processEnvironmentShellPayload{token: markerToken, wire: wire, scriptPath: scriptPath, cleanup: cleanupPaths}, nil
}
