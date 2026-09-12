package fileops

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/numeric"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type GlobalAwareReporter struct {
	original  TaskReporter
	getGlobal func(action string) (string, int, string)
	tracker   *FileOpTracker
	onBytes   func(int)
	// providerProgress becomes true once the source or destination reports
	// its own network transfer. The later local copy from a materialized cache
	// must then not count the same bytes or speed a second time.
	providerProgress atomic.Bool
	phaseMu          sync.Mutex
	phaseAction      string
	phasePercent     int
	phaseCount       int
	phaseIndex       int
	phaseProgress    [2]int
	bytePhase        int
	sourceBytes      int64
	fileSize         int64
}

func (w *GlobalAwareReporter) StartFile(name string, size int64) {
	w.StartFileKnown(name, size, true, 1, 0)
}

func (w *GlobalAwareReporter) StartFileKnown(name string, size int64, sizeKnown bool, phases, bytePhase int) {
	if phases < 1 {
		phases = 1
	}
	if phases > len(w.phaseProgress) {
		phases = len(w.phaseProgress)
	}
	if bytePhase < 0 || bytePhase >= phases {
		bytePhase = 0
	}
	w.providerProgress.Store(false)
	w.phaseMu.Lock()
	w.phaseAction = ""
	w.phasePercent = 0
	w.phaseCount = phases
	w.phaseIndex = 0
	w.phaseProgress = [2]int{}
	w.bytePhase = bytePhase
	w.sourceBytes = 0
	w.fileSize = size
	w.phaseMu.Unlock()
	if w.tracker != nil {
		w.tracker.StartFileKnown(name, size, sizeKnown)
	}
}

func (w *GlobalAwareReporter) SetCurrentSize(size int64) {
	if size <= 0 {
		return
	}
	w.phaseMu.Lock()
	if w.fileSize <= 0 {
		w.fileSize = size
	}
	w.phaseMu.Unlock()
	if w.tracker != nil {
		w.tracker.SetCurrentSize(size)
	}
}

func (w *GlobalAwareReporter) UpdateBytes(n int) {
	if n <= 0 {
		return
	}
	if w.providerProgress.Load() {
		w.phaseMu.Lock()
		bytePhase := w.bytePhase
		w.phaseMu.Unlock()
		if bytePhase == 0 {
			return
		}
	}
	w.phaseMu.Lock()
	w.sourceBytes += int64(n)
	sourceBytes, size, phases, bytePhase := w.sourceBytes, w.fileSize, w.phaseCount, w.bytePhase
	if bytePhase > 0 && !w.providerProgress.Load() {
		// A cached/materialized source can complete without emitting a network
		// phase. Treat it as instant before counting destination streaming.
		w.phaseProgress[0] = 100
	}
	w.phaseMu.Unlock()
	if phases < 1 {
		phases = 1
	}
	if w.tracker != nil {
		if phases > 1 && size > 0 {
			percent := int(sourceBytes * 100 / size)
			if percent > 100 {
				percent = 100
			}
			w.phaseMu.Lock()
			if percent > w.phaseProgress[bytePhase] {
				w.phaseProgress[bytePhase] = percent
			}
			logicalPercent := 0
			for i := 0; i < phases; i++ {
				logicalPercent += w.phaseProgress[i]
			}
			w.phaseMu.Unlock()
			w.tracker.SetCurrentPercent(logicalPercent / phases)
		} else {
			w.tracker.UpdateBytes(n)
		}
	}
	if w.onBytes != nil {
		w.onBytes(n)
	}
}

func (w *GlobalAwareReporter) FileDone() {
	if w.tracker != nil {
		w.tracker.FileDone()
	}
}
func (w *GlobalAwareReporter) FileSkipped() {
	if w.tracker != nil {
		w.tracker.FileSkipped()
	}
}

func (w *GlobalAwareReporter) DirDone() {
	if w.tracker != nil {
		w.tracker.DirDone()
	}
}

func (w *GlobalAwareReporter) UpdateScan(currentPath string, files, dirs int64) {
	w.original.UpdateScan(currentPath, files, dirs)
}

func (w *GlobalAwareReporter) IsCancelled() bool {
	return w.original.IsCancelled()
}

func (w *GlobalAwareReporter) UpdateTransfer(action, filename string, currentPct int, totalText string, totalPct int, speedText string) {
	if (action == "Uploading" || action == "Downloading") && w.tracker != nil && currentPct >= 0 {
		if action == "Downloading" {
			w.providerProgress.Store(true)
		}
		if currentPct > 100 {
			currentPct = 100
		}
		w.phaseMu.Lock()
		if action != w.phaseAction {
			w.phaseAction = action
			if w.phaseCount > 1 && action == "Uploading" {
				w.phaseIndex = w.phaseCount - 1
				if w.fileSize == 0 {
					// EOF on a known empty source has no byte callback. Reaching the
					// upload phase proves only the source half, not remote commit.
					w.phaseProgress[0] = 100
				}
			} else {
				w.phaseIndex = 0
			}
			// Visible per-phase progress restarts for a distinct action. Logical
			// aggregate progress below remains monotonic through phaseProgress.
			w.phasePercent = 0
		}
		if currentPct < w.phasePercent {
			currentPct = w.phasePercent
		}
		previousPhasePercent := w.phasePercent
		w.phasePercent = currentPct
		phases, phaseIndex := w.phaseCount, w.phaseIndex
		if currentPct > w.phaseProgress[phaseIndex] {
			w.phaseProgress[phaseIndex] = currentPct
		}
		logicalPercent := 0
		for i := 0; i < phases && i < len(w.phaseProgress); i++ {
			logicalPercent += w.phaseProgress[i]
		}
		w.phaseMu.Unlock()
		if phases < 1 {
			phases = 1
		}

		if phases > 1 {
			logicalPercent /= phases
		} else {
			logicalPercent = currentPct
		}
		trackerDelta := w.tracker.SetCurrentPercent(logicalPercent)
		phaseDelta := w.tracker.BytesBetweenPercents(previousPhasePercent, currentPct)
		if phaseDelta < trackerDelta {
			phaseDelta = trackerDelta
		}
		if phaseDelta > 0 && w.onBytes != nil {
			w.onBytes(phaseDelta)
		}
		// Retries within one transfer phase are monotonic. A real phase change
		// (materialize source -> upload destination) intentionally starts a new
		// current-file bar while total progress remains commit-reserved.
	}
	gTotalText, gTotalPct, gTimeSpeedText := w.getGlobal(action)
	displayFileName := filename
	if totalText != "" && !strings.HasPrefix(totalText, "Total:") && !strings.HasPrefix(totalText, "Extracting:") && !strings.HasPrefix(totalText, "Moving:") && !strings.HasPrefix(totalText, "Copying:") {
		displayFileName = filename + " (" + totalText + ")"
	} else if strings.HasPrefix(totalText, "Extracting:") {
		displayFileName = filename + " (" + totalText + ")"
	}
	w.original.UpdateTransfer(action, displayFileName, currentPct, gTotalText, gTotalPct, gTimeSpeedText)
}

type FileOpState struct {
	OverwriteAll bool
	SkipAll      bool
	SkippedCount int
	OnBytes      func(int)
	StartFile    func(name string, size int64, sizeKnown bool, phases, bytePhase int)
	SetFileSize  func(size int64)
	Tracker      *FileOpTracker
	UpdateUI     func(force bool)
	Anchor       vtui.Frame
	Buffer       []byte
	IsMove       bool
	S2SDir       int // 0: unknown, 1: push, 2: pull, 3: disabled
	// AccessRights is the F5/F6 "Access rights" choice for this operation.
	AccessRights AccessRightsMode
	// parentRights caches destination folder permissions for the inherit
	// mode. See parentRights() for why it needs no lock.
	parentRights map[string]uint32
}

// FormatIntWithSpaces converts an int64 to string with spaces as thousands separators.
func FormatIntWithSpaces(n int64) string {
	s := strconv.FormatInt(n, 10)
	l := len(s)
	var res strings.Builder
	for i, char := range s {
		res.WriteRune(char)
		if (l-i-1)%3 == 0 && i != l-1 {
			res.WriteByte(' ')
		}
	}
	return res.String()
}

func resolveFileOpDestination(srcVfs, dstVfs vfs.VFS, destInput string) (vfs.VFS, string) {
	return resolveFileOpDestinationAt(srcVfs, dstVfs, srcVfs.GetPath(), destInput)
}

func resolveFileOpDestinationAt(srcVfs, dstVfs vfs.VFS, srcBasePath, destInput string) (vfs.VFS, string) {
	// The passive panel supplies the initial absolute destination shown in the
	// dialog. Once the user enters a relative path, however, it is relative to
	// the active (source) panel, just like other panel path operations.
	if dstVfs.IsAbs(destInput) {
		return dstVfs, destInput
	}
	if srcVfs.IsAbs(destInput) {
		return srcVfs, destInput
	}
	return srcVfs, srcVfs.Join(srcBasePath, destInput)
}

func transferItemName(srcVFS vfs.VFS, srcPath string, dstVFS vfs.VFS, fallback string) string {
	fallback = safeTransferItemName(dstVFS, fallback)
	provider, ok := srcVFS.(vfs.TransferNameProvider)
	if !ok {
		return fallback
	}
	name := provider.TransferName(srcPath, dstVFS)
	if name == "" {
		return fallback
	}
	// A transfer name is one destination entry, never a path. Treat an
	// invalid plugin answer as a request for the safe display-name fallback.
	if !isSafeTransferItemName(dstVFS, name) {
		return fallback
	}
	return name
}

// transferItemNameForMask applies a rename mask to the name that far2l would
// feed to ConvertWildcards. A selected item can be represented by a path (for
// example in a search or temporary panel), but the mask is applied to its
// final component, not to a sanitized representation of that path. Providers
// still get to supply their exported name, which may differ from the panel
// label (for example document.gdoc -> document.docx).
func transferItemNameForMask(srcVFS vfs.VFS, srcPath string, dstVFS vfs.VFS, fallback, mask string) string {
	if provider, ok := srcVFS.(vfs.TransferNameProvider); ok {
		if name := provider.TransferName(srcPath, dstVFS); name != "" && isSafeTransferItemName(dstVFS, name) {
			return applyFileMask(name, mask)
		}
	}

	// Do not call transferItemName here: when fallback contains a path it
	// deliberately adds a hash after sanitizing the whole label to prevent
	// collisions, while far2l discards that path before applying the mask.
	base := srcVFS.Base(fallback)
	base = safeTransferItemName(dstVFS, base)
	return applyFileMask(base, mask)
}

func isSafeTransferItemName(dstVFS vfs.VFS, name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '\x00') || strings.ContainsAny(name, `/\\`) {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"|?*`, r) {
			return false
		}
	}
	if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") || windowsReservedTransferName(name) {
		return false
	}
	return dstVFS == nil || dstVFS.Base(name) == name
}

// safeTransferItemName turns an untrusted display label into exactly one
// portable destination entry. A hash is added whenever characters change so
// distinct remote labels cannot silently collapse onto the same local file.
func safeTransferItemName(dstVFS vfs.VFS, original string) string {
	if isSafeTransferItemName(dstVFS, original) {
		return original
	}
	var b strings.Builder
	for _, r := range original {
		switch {
		case r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.TrimRight(b.String(), " .")
	if name == "" || name == "." || name == ".." {
		name = "item"
	}
	if windowsReservedTransferName(name) {
		name = "_" + name
	}
	sum := sha256.Sum256([]byte(original))
	suffix := fmt.Sprintf(" [%x]", sum[:4])
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	name = base + suffix + ext
	if !isSafeTransferItemName(dstVFS, name) {
		name = fmt.Sprintf("item-%x", sum[:8])
	}
	return name
}

func windowsReservedTransferName(name string) bool {
	base := name
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}

func transferNamesAreIdentity(srcVFS, dstVFS vfs.VFS, srcBasePath string, names []string) bool {
	for _, name := range names {
		srcPath := srcVFS.Join(srcBasePath, name)
		if transferItemName(srcVFS, srcPath, dstVFS, name) != name {
			return false
		}
	}
	return true
}

func ExecuteFileOp(srcVfs, dstVfs vfs.VFS, names []string, destInput string, isMove bool, mode int, onComplete func()) {
	ExecuteFileOpAt(srcVfs, dstVfs, srcVfs.GetPath(), names, destInput, isMove, mode, onComplete)
}

// ExecuteFileOpAt uses the source directory captured at the user-action
// boundary. Panel VFS instances are navigable, so consulting GetPath after a
// goroutine or queued task starts can otherwise target same-named files in a
// different directory.
func ExecuteFileOpAt(srcVfs, dstVfs vfs.VFS, srcBasePath string, names []string, destInput string, isMove bool, mode int, onComplete func()) {
	ExecuteFileOpAtWithOptions(srcVfs, dstVfs, srcBasePath, names, destInput, isMove, mode, DefaultFileOpOptions(), onComplete)
}

// ExecuteFileOpAtWithOptions is ExecuteFileOpAt for a caller that has asked
// the user what the operation should do, instead of taking the configured
// defaults.
func ExecuteFileOpAtWithOptions(srcVfs, dstVfs vfs.VFS, srcBasePath string, names []string, destInput string, isMove bool, mode int, opts FileOpOptions, onComplete func()) {
	// A wildcard in the last component is a rename mask, as in far2l: the
	// files land in the directory before it, under names the mask generates.
	// Taken literally it would instead create a file called "*.1".
	mask := destMask(destInput)
	if mask != "" {
		destInput = destWithoutMask(destInput)
	}

	names = append([]string(nil), names...)
	dstVfs, destPath := resolveFileOpDestinationAt(srcVfs, dstVfs, srcBasePath, destInput)

	isTargetDir := len(names) > 1 || mask != ""
	if !isTargetDir {
		if strings.HasSuffix(destInput, "/") || strings.HasSuffix(destInput, "\\") {
			isTargetDir = true
		} else if stat, err := dstVfs.Stat(context.Background(), destPath); err == nil && stat.IsDir {
			isTargetDir = true
		} else if destInput == "." || destInput == ".." {
			isTargetDir = true
		}
	}

	var preconds []OpPrecondition
	for _, name := range names {
		if st, err := srcVfs.Stat(context.Background(), srcVfs.Join(srcBasePath, name)); err == nil {
			preconds = append(preconds, OpPrecondition{
				Vfs: srcVfs, Path: srcVfs.Join(srcBasePath, name), MTime: st.MTime, Size: st.Size, IsDir: st.IsDir,
			})
		}
	}

	actionDesc := "Copy"
	actionTitle := "Copying"
	dialogTitle := " Copying... "
	if isMove {
		actionDesc = "Move"
		actionTitle = "Moving"
		dialogTitle = " Moving... "
	} else if srcVfs.ParentVFS() != nil && dstVfs.ParentVFS() == nil {
		actionDesc = "Extract"
		actionTitle = "Extracting"
		dialogTitle = " Extracting... "
	} else if srcVfs.ParentVFS() == nil && dstVfs.ParentVFS() != nil {
		actionDesc = "Archive"
		actionTitle = "Archiving"
		dialogTitle = " Archiving... "
	}
	desc := fmt.Sprintf("%d item(s) -> %s", len(names), vtui.TruncateMiddle(destInput, 15))

	vtui.DebugLog("FILEOP: %s src=%T base=%q names=%v dst=%T destPath=%q isTargetDir=%v mask=%q mode=%d",
		actionDesc, srcVfs, srcBasePath, names, dstVfs, destPath, isTargetDir, mask, mode)

	runFunc := func(ctx context.Context, reporter TaskReporter, anchor vtui.Frame) error {
		startTime := time.Now()
		dirToEnsure := destPath
		if !isTargetDir {
			dirToEnsure = dstVfs.Dir(destPath)
		}

		if dirToEnsure != "" && dirToEnsure != "." {
			st, err := dstVfs.Stat(ctx, dirToEnsure)
			if err != nil {
				if mkErr := dstVfs.MkDir(ctx, dirToEnsure); mkErr != nil {
					return fmt.Errorf("failed to create target dir: %w", mkErr)
				}
			} else if !st.IsDir {
				return fmt.Errorf("target path component is not a directory: %s", dirToEnsure)
			}
		}

		var totalStats vfs.OpStats
		scanErr := error(nil)
		lastScanUpdate := startTime
		scanCallback := func(currentPath string, stats vfs.OpStats) {
			now := time.Now()
			if now.Sub(lastScanUpdate) > 50*time.Millisecond {
				lastScanUpdate = now
				reporter.UpdateScan(currentPath, stats.Files, stats.Dirs)
			}
		}
		bulkCopyEligible := !isMove && mask == "" && !SameVFSInstance(srcVfs, dstVfs) && transferNamesAreIdentity(srcVfs, dstVfs, srcBasePath, names)
		if _, ok := srcVfs.(vfs.BulkCopierAt); ok && bulkCopyEligible {
			if bulkScanner, ok := srcVfs.(vfs.BulkScannerAt); ok {
				totalStats, scanErr = bulkScanner.ScanBulkAt(ctx, srcBasePath, names, scanCallback)
			} else {
				totalStats, scanErr = vfs.CalculateStats(ctx, srcVfs, srcBasePath, names, scanCallback)
			}
		} else {
			totalStats, scanErr = vfs.CalculateStats(ctx, srcVfs, srcBasePath, names, scanCallback)
		}
		reporter.UpdateScan("", totalStats.Files, totalStats.Dirs)

		if scanErr != nil {
			vtui.DebugLog("FILEOP: scan of %v under %q failed: %v", names, srcBasePath, scanErr)
			return scanErr
		}
		vtui.DebugLog("FILEOP: scan found %d file(s), %d dir(s), %d byte(s)",
			totalStats.Files, totalStats.Dirs, totalStats.Bytes)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		tracker := NewFileOpTracker(totalStats)
		lastUpdate := startTime
		lastSpeedUpdate := startTime
		bytesSinceLastSpeedUpdate := int64(0)
		currentSpeed := float64(0)

		lastLoggedTime := startTime
		lastLoggedPct := -1

		getGlobalStats := func(action string) (string, int, string) {
			now := time.Now()
			_, totalPct, _ := tracker.GetProgress()
			processed, total := tracker.GetStats()

			var totalText string
			if total.Bytes > 0 && total.UnknownSizeFiles == 0 {
				totalText = fmt.Sprintf("Total: %s / %s", numeric.FormatSize(processed.Bytes), numeric.FormatSize(total.Bytes))
			} else {
				totalText = fmt.Sprintf("Total: %d / %d items", processed.Files+processed.Dirs, total.Files+total.Dirs)
			}

			elapsed := now.Sub(startTime)
			elapsedStr := fmt.Sprintf("Time: %02d:%02d:%02d", int(elapsed.Hours()), int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)

			const ItemOverhead = 32 * 1024
			vProcessed := float64(processed.Bytes + (processed.Files+processed.Dirs)*ItemOverhead)
			vTotal := float64(total.Bytes + (total.Files+total.Dirs)*ItemOverhead)
			if total.UnknownSizeFiles > 0 {
				// The byte denominator is incomplete by definition. Base ETA on
				// the already normalized item/percentage progress instead.
				vProcessed = float64(totalPct)
				vTotal = 100
			}

			etaStr := "Remaining: ??:??:??"
			if vTotal > 0 && vProcessed > 0 && elapsed.Seconds() > 0.5 {
				if action == "Locating" || action == "Waiting" || action == "Scanning" || action == "Archiving" {
					etaStr = "Remaining: ??:??:??"
				} else {
					ratio := vProcessed / vTotal
					etaSecs := (elapsed.Seconds() / ratio) - elapsed.Seconds()
					if etaSecs < 0 {
						etaSecs = 0
					}
					if etaSecs > 359999 {
						etaStr = "Remaining: >99 hours"
					} else {
						etaDur := time.Duration(etaSecs * float64(time.Second))
						etaStr = fmt.Sprintf("Remaining: %02d:%02d:%02d", int(etaDur.Hours()), int(etaDur.Minutes())%60, int(etaDur.Seconds())%60)
					}
				}
			}

			speedStr := ""
			if currentSpeed > 0 {
				speedStr = numeric.FormatSize(int64(currentSpeed)) + "/s"
			}

			timeSpeedText := fmt.Sprintf("%-16s %-21s %15s", elapsedStr, etaStr, speedStr)
			return totalText, totalPct, timeSpeedText
		}

		var updateUI func(force bool)
		wrapRep := &GlobalAwareReporter{
			original:  reporter,
			getGlobal: getGlobalStats,
			tracker:   tracker,
			onBytes: func(n int) {
				bytesSinceLastSpeedUpdate += int64(n)
				if updateUI != nil {
					updateUI(false)
				}
			},
		}
		ctx = context.WithValue(ctx, vfs.ReporterKey, wrapRep)

		updateUI = func(force bool) {
			now := time.Now()
			if force || now.Sub(lastUpdate) >= 100*time.Millisecond {
				speedDur := now.Sub(lastSpeedUpdate).Seconds()
				if speedDur >= 1.0 {
					currentSpeed = float64(bytesSinceLastSpeedUpdate) / speedDur
					lastSpeedUpdate = now
					bytesSinceLastSpeedUpdate = 0
				}
				lastUpdate = now

				filePct, _, currName := tracker.GetProgress()
				processed, total := tracker.GetStats()

				action := actionTitle
				gTotalText, gTotalPct, gTimeSpeedText := getGlobalStats(action)

				if gTotalPct >= lastLoggedPct+5 || now.Sub(lastLoggedTime) >= 5*time.Second {
					parts := strings.Fields(gTimeSpeedText)
					elapsedStr, etaStr, speedStr := "", "", ""
					if len(parts) >= 2 {
						elapsedStr = parts[1]
					}
					if len(parts) >= 4 {
						etaStr = parts[3]
					}
					if len(parts) >= 5 {
						speedStr = parts[4]
					}

					vtui.DebugLog("FILEOP: %d%% | Items: %d/%d | Proc: %d/%d B | %s | %s | %s",
						gTotalPct,
						processed.Files+processed.Dirs, total.Files+total.Dirs,
						processed.Bytes, total.Bytes,
						elapsedStr, etaStr, speedStr)
					lastLoggedPct = gTotalPct
					lastLoggedTime = now
				}

				reporter.UpdateTransfer(action, currName, filePct, gTotalText, gTotalPct, gTimeSpeedText)
			}
		}

		state := &FileOpState{
			Tracker:      tracker,
			UpdateUI:     updateUI,
			StartFile:    wrapRep.StartFileKnown,
			SetFileSize:  wrapRep.SetCurrentSize,
			OnBytes:      wrapRep.UpdateBytes,
			Anchor:       anchor,
			Buffer:       make([]byte, 128*1024),
			IsMove:       isMove,
			AccessRights: opts.AccessRights,
		}

		updateUI(true)
		// OPTIMIZATION: Check if the source VFS supports bulk copying (e.g. for sequential archives).
		// Snapshot-aware bulk copying is safe in every mode. BulkCopier's legacy
		// API remains restricted to foreground work because it is relative to
		// mutable VFS state.
		// Bulk copy keeps the source names, so it cannot serve a mask.
		if bulkCopyEligible {
			var bulkErr error
			bulkAttempted := false
			if bulkCopier, ok := srcVfs.(vfs.BulkCopierAt); ok {
				bulkAttempted = true
				bulkErr = bulkCopier.CopyBulkAt(ctx, srcBasePath, names, dstVfs, destPath, wrapRep)
			} else if mode == 2 {
				if bulkCopier, ok := srcVfs.(vfs.BulkCopier); ok {
					bulkAttempted = true
					bulkErr = bulkCopier.CopyBulk(ctx, names, dstVfs, destPath, wrapRep)
				}
			}
			if bulkAttempted {
				if bulkErr == nil {
					updateUI(true)
					return nil
				}
				if OperationMustNotRetry(bulkErr) {
					return bulkErr
				}
				vtui.DebugLog("FILEOP: Bulk copy failed, falling back to sequential: %v", bulkErr)
			}
		}

		for _, name := range names {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			srcPath := srcVfs.Join(srcBasePath, name)
			targetItemPath := destPath
			if isTargetDir {
				var targetName string
				if mask == "" {
					targetName = transferItemName(srcVfs, srcPath, dstVfs, name)
				} else {
					targetName = transferItemNameForMask(srcVfs, srcPath, dstVfs, name, mask)
				}
				targetItemPath = dstVfs.Join(destPath, targetName)
			}

			if isMove && vfs.SameSession(srcVfs, dstVfs) {
				renamed, err := tryOptimizedRename(ctx, srcVfs, dstVfs, srcPath, targetItemPath)
				if err != nil {
					return err
				}
				if renamed {
					vtui.DebugLog("FILEOP: Optimized server-side rename: %s -> %s", srcPath, targetItemPath)
					handleArchiveIndexOp(srcVfs, srcPath, dstVfs, targetItemPath, true)

					itemStat, _ := dstVfs.Stat(ctx, targetItemPath)
					if itemStat.IsDir {
						tracker.DirDone()
					} else {
						displayString := name
						switch config.App.FileOpPathDisplay {
						case 1:
							displayString = srcPath
						case 2:
							displayString = srcPath + " -> " + targetItemPath
						}
						wrapRep.StartFileKnown(displayString, itemStat.Size, itemStat.SizeKnown || itemStat.Size > 0, 1, 0)
						tracker.UpdateBytes(int(itemStat.Size))
						tracker.FileDone()
					}
					updateUI(true)
					continue
				}
			}

			err := recursiveCopy(ctx, srcVfs, srcPath, dstVfs, targetItemPath, state, 0)
			if err != nil {
				vtui.DebugLog("FILEOP: copy %q -> %q failed: %v", srcPath, targetItemPath, err)
				return err
			}

			if isMove && state.SkippedCount == 0 {
				if err := srcVfs.Remove(ctx, srcPath); err != nil {
					return &vfs.PartialOperationError{
						Operation: "move source cleanup",
						Completed: []string{targetItemPath},
						Failed:    []string{srcPath},
						Err:       err,
					}
				}
			}
			updateUI(true)
		}
		return nil
	}

	if mode == 0 { // Queue
		rk1 := GetResourceKey(srcVfs)
		rk2 := GetResourceKey(dstVfs)
		var keys []string
		if rk1 != "" {
			keys = append(keys, rk1)
		}
		if rk2 != "" && rk2 != rk1 {
			keys = append(keys, rk2)
		}
		task := &QueueTask{
			Type:          actionDesc,
			Desc:          desc,
			Preconditions: preconds,
			ResKeys:       keys,
			Run:           runFunc,
			OnComplete:    onComplete,
		}
		GlobalQueueManager.Enqueue(task)
	} else { // Foreground or Background
		dlg := NewFileOpProgressDialog(dialogTitle)
		// RunAsync starts the worker before it returns the context assigned
		// here, and the worker closes the dialog when it finishes, which runs
		// OnResult. A plain variable is therefore written and read from two
		// goroutines with nothing ordering them, so hold it atomically. A nil
		// read only means the task ended before the assignment landed, and
		// there is nothing left to cancel in that case.
		var taskCtx atomic.Pointer[vtui.TaskContext]
		dlg.btnCancel.OnClick = func() { dlg.SetExitCode(1) }
		dlg.OnResult = func(code int) {
			if ctx := taskCtx.Load(); ctx != nil {
				ctx.Cancel()
			}
		}

		reporter := NewDialogReporter(dlg)

		vtui.FrameManager.PostTask(func() {
			// The workspace has to be asked for, not assumed: the seam can be
			// wired and still have nothing to copy, in a run with no panels.
			if workspace := BackgroundScreen(mode); workspace != nil {
				vtui.FrameManager.AddScreen(workspace)
				vtui.FrameManager.Push(dlg)
				return
			}
			vtui.FrameManager.AddScreenHeadless(dlg)
		})

		taskCtx.Store(vtui.RunAsync(func(ctx *vtui.TaskContext) {
			err := runFunc(ctx.Context, reporter, dlg)
			ctx.RunOnUI(func() {
				reporter.Stop()
				dlg.Close()
				if onComplete != nil {
					onComplete()
				}
				if err != nil && err != context.Canceled {
					vtui.ShowMessage(" Error ", fmt.Sprintf("Operation failed:\n%v", err), []string{"&Ok"})
				}
			})
		}))
	}
}

func ExecuteDeleteOp(activeVfs vfs.VFS, names []string, mode int, onComplete func()) {
	ExecuteDeleteOpWithDisposition(activeVfs, names, mode, vfs.DeletePermanently, onComplete)
}

func DeletePathWithDisposition(ctx context.Context, filesystem vfs.VFS, path string, disposition vfs.DeleteDisposition) error {
	switch disposition {
	case vfs.DeleteToTrash:
		return vfs.MoveToTrash(ctx, filesystem, path)
	case vfs.DeletePermanently:
		return filesystem.Remove(ctx, path)
	default:
		return fmt.Errorf("unknown delete disposition: %d", disposition)
	}
}

func CalculateDeleteStats(ctx context.Context, filesystem vfs.VFS, basePath string, names []string, disposition vfs.DeleteDisposition, cb vfs.ScanCallback) (vfs.OpStats, error) {
	switch disposition {
	case vfs.DeleteToTrash:
		if err := ctx.Err(); err != nil {
			return vfs.OpStats{}, err
		}
		return vfs.OpStats{Files: int64(len(names))}, nil
	case vfs.DeletePermanently:
		return vfs.CalculateStats(ctx, filesystem, basePath, names, cb)
	default:
		return vfs.OpStats{}, fmt.Errorf("unknown delete disposition: %d", disposition)
	}
}

// ExecuteDeleteOpWithDisposition performs either a recoverable trash move or
// a permanent Remove. disposition is an explicit task argument instead of a
// config lookup so a queued operation cannot change meaning while waiting.
func ExecuteDeleteOpWithDisposition(activeVfs vfs.VFS, names []string, mode int, disposition vfs.DeleteDisposition, onComplete func()) {
	ExecuteDeleteOpWithDispositionAt(activeVfs, activeVfs.GetPath(), names, mode, disposition, onComplete)
}

// ExecuteDeleteOpWithDispositionAt performs deletion relative to a directory
// captured before asynchronous scheduling.
func ExecuteDeleteOpWithDispositionAt(activeVfs vfs.VFS, basePath string, names []string, mode int, disposition vfs.DeleteDisposition, onComplete func()) {
	// The panel and its VFS may navigate while a queued operation is waiting.
	// Capture the directory alongside the disposition so the task cannot drift
	// onto identically named items in a later directory.
	names = append([]string(nil), names...)
	var preconds []OpPrecondition
	for _, name := range names {
		fullPath := activeVfs.Join(basePath, name)
		if st, err := activeVfs.Stat(context.Background(), fullPath); err == nil {
			preconds = append(preconds, OpPrecondition{
				Vfs: activeVfs, Path: fullPath, MTime: st.MTime, Size: st.Size, IsDir: st.IsDir,
			})
		}
	}
	toTrash := disposition == vfs.DeleteToTrash
	descKey := "Delete.QueuePermanent"
	progressVerbKey := "Delete.ProgressPermanent"
	progressTitleKey := "Delete.ProgressTitlePermanent"
	failedKey := "Delete.FailedPermanent"
	if toTrash {
		descKey = "Delete.QueueTrash"
		progressVerbKey = "Delete.ProgressTrash"
		progressTitleKey = "Delete.ProgressTitleTrash"
		failedKey = "Delete.FailedTrash"
	}
	desc := fmt.Sprintf(i18n.Msg(descKey), len(names))

	runFunc := func(ctx context.Context, reporter TaskReporter, anchor vtui.Frame) error {
		ctx = context.WithValue(ctx, vfs.ReporterKey, reporter)
		var totalStats vfs.OpStats
		// Native/service trash operations move each selected root as one
		// object. CalculateDeleteStats therefore skips recursive enumeration
		// for trash while permanent deletion retains its detailed scan.
		totalStats, scanErr := CalculateDeleteStats(ctx, activeVfs, basePath, names, disposition, func(currentPath string, stats vfs.OpStats) {
			reporter.UpdateScan(currentPath, stats.Files, stats.Dirs)
		})

		if scanErr != nil && !errors.Is(scanErr, context.Canceled) {
			return fmt.Errorf(i18n.Msg("Delete.ScanFailed"), scanErr)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		tracker := NewFileOpTracker(totalStats)
		lastUpdate := time.Now()

		updateUI := func(force bool) {
			now := time.Now()
			if force || now.Sub(lastUpdate) >= 100*time.Millisecond {
				lastUpdate = now
				filePct, totalPct, currName := tracker.GetProgress()
				processed, total := tracker.GetStats()

				totalText := fmt.Sprintf(i18n.Msg("Delete.Total"), processed.Files+processed.Dirs, total.Files+total.Dirs)

				reporter.UpdateTransfer(i18n.Msg(progressVerbKey), currName, filePct, totalText, totalPct, "")
			}
		}

		updateUI(true)

		var allErrors []string
		var skipAll bool
		for _, name := range names {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fullPath := activeVfs.Join(basePath, name)

			displayString := name
			if config.App.FileOpPathDisplay > 0 {
				displayString = fullPath
			}
			tracker.StartFile(displayString, 0)
			updateUI(true)
			archiveIndexes := collectArchiveIndexes(ctx, activeVfs, fullPath)
			deleted := false

			for {
				err := DeletePathWithDisposition(ctx, activeVfs, fullPath, disposition)
				if err == nil {
					deleted = true
					removeArchiveIndexes(archiveIndexes)
					break
				}
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return err
				}
				// The provider has already committed some work or cannot tell
				// whether it did. Retrying would apply a destructive operation a
				// second time, so surface the original state unchanged.
				if errors.Is(err, vfs.ErrOperationPartial) || errors.Is(err, vfs.ErrOperationStateUnknown) {
					return err
				}
				if errors.Is(err, vfs.ErrTrashUnsupported) {
					err = fmt.Errorf("%s", i18n.Msg("Trash.Unsupported"))
				}

				if skipAll {
					allErrors = append(allErrors, fmt.Sprintf(i18n.Msg("Delete.Skipped"), name, err))
					break
				}

				choice := askDeleteError(ctx, fmt.Sprintf(i18n.Msg(failedKey), name), err, anchor)
				if choice == 0 { // Retry
					continue
				} else if choice == 1 { // Skip
					allErrors = append(allErrors, fmt.Sprintf(i18n.Msg("Delete.Skipped"), name, err))
					break
				} else if choice == 2 { // Skip All
					skipAll = true
					allErrors = append(allErrors, fmt.Sprintf(i18n.Msg("Delete.Skipped"), name, err))
					break
				} else { // Abort
					return context.Canceled
				}
			}

			if deleted {
				tracker.FileDone()
			} else {
				tracker.FileSkipped()
			}
			updateUI(true)
		}
		if len(allErrors) > 0 {
			vtui.FrameManager.PostTask(func() {
				dlgW, dlgH := 60, 15
				scrH := vtui.FrameManager.GetScreenHeight()
				if dlgH > scrH-2 {
					dlgH = scrH - 2
				}
				if dlgH < 8 {
					dlgH = 8
				}

				dlg := vtui.NewCenteredDialog(dlgW, dlgH, i18n.Msg("FileOp.DeletionErrors"))
				dlg.ShowClose = true

				var listItems []string
				for _, errStr := range allErrors {
					lines := vtui.WrapText(errStr, dlgW-6)
					listItems = append(listItems, lines...)
					listItems = append(listItems, strings.Repeat("-", dlgW-6))
				}
				if len(listItems) > 0 {
					listItems = listItems[:len(listItems)-1]
				}

				lb := vtui.NewListBox(0, 0, dlgW-4, dlgH-6, listItems)
				btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
				btnOk.IsDefault = true
				btnOk.OnClick = func() { dlg.Close() }

				dlg.AddItem(lb)
				dlg.AddItem(btnOk)

				vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, dlgW-4, dlgH-4)
				vbox.Add(lb, vtui.Margins{Bottom: 1}, vtui.AlignFill)

				hbox := vtui.NewHBoxLayout(0, 0, dlgW-4, 1)
				hbox.HorizontalAlign = vtui.AlignCenter
				hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)

				vbox.Add(hbox, vtui.Margins{}, vtui.AlignFill)
				vbox.Apply()

				if anchor != nil {
					vtui.FrameManager.PushToFrameScreen(anchor, dlg)
				} else {
					vtui.FrameManager.Push(dlg)
				}
			})
		}
		return nil
	}

	if mode == 0 { // Queue
		rk := GetResourceKey(activeVfs)
		var keys []string
		if rk != "" {
			keys = append(keys, rk)
		}
		taskType := "Delete"
		if toTrash {
			taskType = "Trash"
		}
		task := &QueueTask{
			Type:          taskType,
			Desc:          desc,
			Preconditions: preconds,
			ResKeys:       keys,
			Run:           runFunc,
			OnComplete:    onComplete,
		}
		GlobalQueueManager.Enqueue(task)
	} else {
		dlg := NewFileOpProgressDialog(" " + strings.TrimSpace(i18n.Msg(progressTitleKey)) + " ")
		// RunAsync starts the worker before it returns the context assigned
		// here, and the worker closes the dialog when it finishes, which runs
		// OnResult. A plain variable is therefore written and read from two
		// goroutines with nothing ordering them, so hold it atomically. A nil
		// read only means the task ended before the assignment landed, and
		// there is nothing left to cancel in that case.
		var taskCtx atomic.Pointer[vtui.TaskContext]
		dlg.btnCancel.OnClick = func() { dlg.SetExitCode(1) }
		dlg.OnResult = func(code int) {
			if ctx := taskCtx.Load(); ctx != nil {
				ctx.Cancel()
			}
		}

		reporter := NewDialogReporter(dlg)

		vtui.FrameManager.PostTask(func() {
			// The workspace has to be asked for, not assumed: the seam can be
			// wired and still have nothing to copy, in a run with no panels.
			if workspace := BackgroundScreen(mode); workspace != nil {
				vtui.FrameManager.AddScreen(workspace)
				vtui.FrameManager.Push(dlg)
				return
			}
			vtui.FrameManager.AddScreenHeadless(dlg)
		})

		taskCtx.Store(vtui.RunAsync(func(ctx *vtui.TaskContext) {
			err := runFunc(ctx.Context, reporter, dlg)
			ctx.RunOnUI(func() {
				reporter.Stop()
				dlg.Close()
				if onComplete != nil {
					onComplete()
				}
				if shouldDisplayFileOpError(err) {
					vtui.ShowMessage(" "+strings.TrimSpace(i18n.Msg("Error.Title"))+" ", fmt.Sprintf(i18n.Msg("Delete.OperationFailed"), err), []string{i18n.Msg("vtui.Ok")})
				}
			})
		}))
	}
}

// CloseOnce wraps a Close so that it can be called explicitly where its
// error matters and still be left in a defer as a safety net. A writer that
// buffers — every network file system does, FISH+ among them — only sends
// its last chunk from Close, so a copy is not finished until Close has
// succeeded, and a dropped error there leaves a truncated file behind while
// the panel reports success.
func CloseOnce(c io.Closer) func() error {
	closed := false
	return func() error {
		if closed {
			return nil
		}
		closed = true
		return c.Close()
	}
}

func OperationMustNotRetry(err error) bool {
	return errors.Is(err, vfs.ErrOperationPartial) ||
		errors.Is(err, vfs.ErrOperationStateUnknown) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

func shouldDisplayFileOpError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, vfs.ErrOperationPartial) || errors.Is(err, vfs.ErrOperationStateUnknown) {
		return true
	}
	return !errors.Is(err, context.Canceled)
}

// tryOptimizedRename only renames into a proven-empty destination. A remote
// Stat failure is not evidence of absence, and an uncertain mutation must not
// be retried as a streaming copy.
func tryOptimizedRename(ctx context.Context, srcVFS, dstVFS vfs.VFS, srcPath, dstPath string) (bool, error) {
	if _, err := dstVFS.Stat(ctx, dstPath); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := srcVFS.Rename(vfs.WithDestinationOverwrite(ctx, false), srcPath, dstPath); err != nil {
		if OperationMustNotRetry(err) {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

// resolveSymlinksForCompare resolves symlinks in the longest existing
// ancestor of p and re-appends the not-yet-existing remainder. A plain
// EvalSymlinks fails on a destination that does not exist yet, leaving it
// unresolved while the source IS resolved — and the copy-into-itself
// prefix check then compares paths from two different namespaces. On macOS
// every TempDir sits behind the /var -> /private/var symlink, so that
// asymmetry made the protection miss and recursion ran until
// ENAMETOOLONG.
func resolveSymlinksForCompare(p string) string {
	remainder := ""
	for cur := p; ; {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, remainder)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		remainder = filepath.Join(filepath.Base(cur), remainder)
		cur = parent
	}
}

func recursiveCopy(ctx context.Context, srcVfs vfs.VFS, srcPath string, dstVfs vfs.VFS, destPath string, state *FileOpState, depth int) (resultErr error) {
	if depth > 1000 {
		return fmt.Errorf("maximum recursion depth exceeded (circular structure?)")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	stat, err := srcVfs.Stat(ctx, srcPath)
	if err != nil {
		return err
	}

	absSrc, _ := srcVfs.Abs(srcPath)
	absDst, _ := dstVfs.Abs(destPath)

	realSrc := absSrc
	realDst := absDst

	_, srcIsOS := srcVfs.(*vfs.OSVFS)
	_, dstIsOS := dstVfs.(*vfs.OSVFS)
	if srcIsOS {
		realSrc = resolveSymlinksForCompare(absSrc)
	}
	if dstIsOS {
		realDst = resolveSymlinksForCompare(absDst)
	}

	cleanSrc, srcIsURI := NormalizedURIIdentity(realSrc)
	cleanDst, dstIsURI := NormalizedURIIdentity(realDst)
	if !srcIsURI && srcIsOS {
		cleanSrc = filepath.ToSlash(filepath.Clean(realSrc))
	} else if !srcIsURI {
		cleanSrc = path.Clean("/" + strings.TrimLeft(strings.ReplaceAll(realSrc, "\\", "/"), "/"))
	}
	if !dstIsURI && dstIsOS {
		cleanDst = filepath.ToSlash(filepath.Clean(realDst))
	} else if !dstIsURI {
		cleanDst = path.Clean("/" + strings.TrimLeft(strings.ReplaceAll(realDst, "\\", "/"), "/"))
	}
	if runtime.GOOS == "windows" && srcIsOS && dstIsOS {
		cleanSrc = strings.ToLower(cleanSrc)
		cleanDst = strings.ToLower(cleanDst)
	}
	sameNamespace := (srcIsOS && dstIsOS) || vfs.SameSession(srcVfs, dstVfs)

	if sameNamespace && cleanSrc == cleanDst {
		if stat.IsDir {
			return fmt.Errorf("cannot copy folder into itself (source equals destination)")
		}
		return fmt.Errorf("cannot copy file onto itself (source equals destination)")
	}

	prefixSrc := cleanSrc
	if !strings.HasSuffix(prefixSrc, "/") {
		prefixSrc += "/"
	}

	if sameNamespace && strings.HasPrefix(cleanDst, prefixSrc) {
		if stat.IsDir {
			return fmt.Errorf("cannot copy folder into itself (destination is a subfolder)")
		}
		return fmt.Errorf("cannot copy file into its own subfolder")
	}

	dstStat, err := dstVfs.Stat(ctx, destPath)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if stat.IsDir {
		if !exists {
			if err := dstVfs.MkDir(ctx, destPath); err != nil {
				return err
			}
		} else if !dstStat.IsDir {
			return fmt.Errorf("cannot overwrite file with folder: %s", dstVfs.Base(destPath))
		}

		dirRights := destinationRights(ctx, state, dstVfs, destPath, stat.UnixMode, true, exists)
		if state != nil && state.AccessRights == AccessRightsInherit && dirRights != 0 {
			// What the children inherit is this folder, so it has to carry
			// its own inherited permissions before they are copied into it.
			// The other two modes keep applying the source's permissions
			// after the walk, where a read-only source folder cannot stop
			// the walk from writing into the copy.
			_ = dstVfs.SetAttributes(ctx, destPath, vfs.VFSItem{UnixMode: dirRights, Uid: -1, Gid: -1})
		}

		var items []vfs.VFSItem
		err := srcVfs.ReadDir(ctx, srcPath, func(chunk []vfs.VFSItem) {
			items = append(items, chunk...)
		})
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.Name == ".." {
				continue
			}
			childSource := srcVfs.Join(srcPath, item.Name)
			childName := transferItemName(srcVfs, childSource, dstVfs, item.Name)
			if err := recursiveCopy(ctx, srcVfs, childSource, dstVfs, dstVfs.Join(destPath, childName), state, depth+1); err != nil {
				return err
			}
		}
		itemToSet := stat
		itemToSet.Uid = -1
		itemToSet.Gid = -1
		itemToSet.UnixMode = dirRights
		_ = dstVfs.SetAttributes(ctx, destPath, itemToSet)

		if state.Tracker != nil {
			state.Tracker.DirDone()
			if state.UpdateUI != nil {
				state.UpdateUI(false)
			}
		}
		return nil
	}

	itemName := dstVfs.Base(destPath)
	if state.Tracker != nil || state.StartFile != nil {
		displayString := itemName
		switch config.App.FileOpPathDisplay {
		case 1:
			displayString = srcPath
		case 2:
			displayString = srcPath + " -> " + destPath
		}
		phases := 1
		bytePhase := 0
		if managed, ok := dstVfs.(vfs.ManagedTransferDestination); ok && managed.ManagedTransferWrites() {
			phases = 2
		} else {
			sourceRemote, sourceIsRemote := srcVfs.(vfs.RemoteTransferVFS)
			destinationRemote, destinationIsRemote := dstVfs.(vfs.RemoteTransferVFS)
			if sourceIsRemote && destinationIsRemote && sourceRemote.RemoteTransfer() && destinationRemote.RemoteTransfer() {
				phases = 2
				// For a streaming destination, Write is the upload phase. For a
				// staged destination it remains part of source materialization and
				// Close reports upload separately.
				bytePhase = 1
			}
		}
		if state.StartFile != nil {
			state.StartFile(displayString, stat.Size, stat.SizeKnown || stat.Size > 0, phases, bytePhase)
		} else {
			state.Tracker.StartFileKnown(displayString, stat.Size, stat.SizeKnown || stat.Size > 0)
		}
		if state.UpdateUI != nil {
			state.UpdateUI(false)
		}
	}

	skipFile := func() {
		state.SkippedCount++
		if state.Tracker != nil {
			state.Tracker.FileSkipped()
			if state.UpdateUI != nil {
				state.UpdateUI(true)
			}
		}
	}

	destPathForFile := destPath
	destinationExisted := false

	for {
		dstStat, err := dstVfs.Stat(ctx, destPathForFile)
		exists := err == nil
		destinationExisted = exists
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		if !exists {
			break
		}
		if dstStat.IsDir {
			return fmt.Errorf("cannot overwrite folder with file: %s", dstVfs.Base(destPathForFile))
		}
		if state.SkipAll {
			skipFile()
			return nil
		}
		if state.OverwriteAll {
			break
		}

		choice, remember := AskOverwrite(ctx, destPathForFile, stat, dstStat, state.Anchor)
		if choice == 1 { // Overwrite
			if remember {
				state.OverwriteAll = true
				vtui.DebugLog("FILEOP: User chose OVERWRITE ALL")
			}
			break
		} else if choice == 2 { // Skip
			if remember {
				state.SkipAll = true
				vtui.DebugLog("FILEOP: User chose SKIP ALL")
			}
			skipFile()
			return nil
		} else if choice == 3 { // Rename
			newName := AskRename(ctx, dstVfs.Base(destPathForFile), state.Anchor)
			if newName == "" {
				return context.Canceled
			}
			destPathForFile = dstVfs.Join(dstVfs.Dir(destPathForFile), newName)
			continue
		} else if choice == 4 || choice == 5 { // Append, Resume
			resultChan := make(chan int, 1)
			vtui.FrameManager.PostTask(func() {
				errDlg := vtui.ShowMessage(" Unsupported ", "Append/Resume not supported by current VFS implementation.", []string{"&Ok"})
				errDlg.OnResult = func(c int) { resultChan <- c }
			})
			<-resultChan
			continue
		} else { // Cancel
			return context.Canceled
		}
	}
	destinationCtx := vfs.WithDestinationOverwrite(ctx, destinationExisted)

	// Optimize using server-side copy if both VFS share the same session/connection
	if ssc, ok := dstVfs.(vfs.ServerSideCopier); ok && vfs.SameSession(srcVfs, dstVfs) {
		err := ssc.Copy(destinationCtx, srcPath, destPathForFile)
		if err == nil {
			if state.Tracker != nil {
				state.Tracker.UpdateBytes(int(stat.Size))
				handleArchiveIndexOp(srcVfs, srcPath, dstVfs, destPathForFile, state.IsMove)
				state.Tracker.FileDone()
				if state.UpdateUI != nil {
					state.UpdateUI(true)
				}
			}
			return nil
		}
		// A provider may have committed only part of a non-atomic remote
		// operation, or lost the response after submitting it. Streaming over
		// that uncertain destination can duplicate data or destroy the only
		// recoverable copy, so these errors must reach the user unchanged.
		if OperationMustNotRetry(err) || (!destinationExisted && errors.Is(err, os.ErrExist)) {
			return err
		}
		vtui.DebugLog("FILEOP: Server-side copy failed, falling back to streaming: %v", err)
	}

	// Optimize using server-to-server direct copy if they are on different hosts,
	// but we can run commands on one of them and have connection info for the other.
	if state.S2SDir != 3 { // Not disabled
		pushed, pulled := false, false

		tryPush := state.S2SDir == 0 || state.S2SDir == 1
		tryPull := state.S2SDir == 0 || state.S2SDir == 2

		if tryPush {
			if rner, ok1 := srcVfs.(vfs.CommandRunner); ok1 {
				if cip, ok2 := dstVfs.(vfs.ConnectionInfoProvider); ok2 {
					if host, port, user, ok := cip.ConnectionInfo(); ok {
						var scpDst string
						if user != "" {
							scpDst = fmt.Sprintf("%s@%s:%q", user, host, destPathForFile)
						} else {
							scpDst = fmt.Sprintf("%s:%q", host, destPathForFile)
						}

						scpCmd := fmt.Sprintf("scp -o ConnectTimeout=10 -P %s -o StrictHostKeyChecking=no -p %q %s",
							port, srcPath, scpDst)
						vtui.DebugLog("FILEOP: Attempting server-to-server push: %s", scpCmd)
						codePush, errPush := rner.RunCommand(ctx, srcVfs.Dir(srcPath), scpCmd, nil)
						if errPush == nil && codePush == 0 {
							pushed = true
							state.S2SDir = 1
						} else {
							vtui.DebugLog("FILEOP: Server-to-server push failed (code: %d): %v", codePush, errPush)
						}
					}
				}
			}
		}

		if !pushed && tryPull {
			if rner, ok1 := dstVfs.(vfs.CommandRunner); ok1 {
				if cip, ok2 := srcVfs.(vfs.ConnectionInfoProvider); ok2 {
					if host, port, user, ok := cip.ConnectionInfo(); ok {
						var scpSrc string
						if user != "" {
							scpSrc = fmt.Sprintf("%s@%s:%q", user, host, srcPath)
						} else {
							scpSrc = fmt.Sprintf("%s:%q", host, srcPath)
						}

						scpCmd := fmt.Sprintf("scp -o ConnectTimeout=10 -P %s -o StrictHostKeyChecking=no -p %s %q",
							port, scpSrc, destPathForFile)
						vtui.DebugLog("FILEOP: Attempting server-to-server pull: %s", scpCmd)
						codePull, errPull := rner.RunCommand(ctx, dstVfs.Dir(destPathForFile), scpCmd, nil)
						if errPull == nil && codePull == 0 {
							pulled = true
							state.S2SDir = 2
						} else {
							vtui.DebugLog("FILEOP: Server-to-server pull failed (code: %d): %v", codePull, errPull)
						}
					}
				}
			}
		}

		if pushed || pulled {
			if state.Tracker != nil {
				state.Tracker.UpdateBytes(int(stat.Size))
				handleArchiveIndexOp(srcVfs, srcPath, dstVfs, destPathForFile, state.IsMove)
				state.Tracker.FileDone()
				if state.UpdateUI != nil {
					state.UpdateUI(true)
				}
			}
			return nil
		} else if state.S2SDir == 0 {
			// If both probed and failed (or couldn't even probe), disable S2S for this operation
			state.S2SDir = 3
			vtui.DebugLog("FILEOP: Server-to-server copy disabled after probing failed or unavailable")
		}
	}

	var srcFile vfs.ReadAtCloser
	for {
		srcFile, err = srcVfs.Open(ctx, srcPath)
		if err == nil {
			break
		}
		choice := AskError(ctx, "Cannot open source file", err, state.Anchor)
		if choice == 1 {
			skipFile()
			return nil
		}
		if choice == 2 {
			return context.Canceled
		}
	}
	defer srcFile.Close()
	if stat.Size <= 0 {
		if state.SetFileSize != nil {
			state.SetFileSize(srcFile.Size())
		} else if state.Tracker != nil {
			state.Tracker.SetCurrentSize(srcFile.Size())
		}
	}

	writeCtx, cancelWrite := context.WithCancel(destinationCtx)
	defer cancelWrite()
	var dstFile io.WriteCloser
	for {
		dstFile, err = dstVfs.Create(writeCtx, destPathForFile)
		if err == nil {
			break
		}
		choice := AskError(ctx, "Cannot create destination file", err, state.Anchor)
		if choice == 1 {
			skipFile()
			return nil
		}
		if choice == 2 {
			return context.Canceled
		}
	}

	destinationFinished := false
	var destinationCloseErr error
	closeDestination := func() error {
		if destinationFinished {
			return destinationCloseErr
		}
		destinationFinished = true
		destinationCloseErr = dstFile.Close()
		return destinationCloseErr
	}
	abortAttempted := false
	abortDestination := func() error {
		if destinationFinished {
			return destinationCloseErr
		}
		// Cancel first so even a concurrently-running remote request cannot
		// consume an EOF and turn a partial source into a successful commit.
		cancelWrite()
		destinationFinished = true
		if aborter, ok := dstFile.(vfs.AbortableWriter); ok {
			abortAttempted = true
			destinationCloseErr = aborter.Abort()
		} else {
			// Legacy writers have no discard contract. Closing after cancellation
			// is the best available behavior; cloud writers are required to expose
			// AbortableWriter and never reach this fallback.
			destinationCloseErr = dstFile.Close()
		}
		return destinationCloseErr
	}
	copySuccess := false
	commitAttempted := false
	defer func() {
		var finishErr error
		if !destinationFinished {
			if commitAttempted {
				finishErr = closeDestination()
			} else {
				finishErr = abortDestination()
			}
		}
		// A failed discard can leave a local spool or billable multipart upload
		// behind. Preserve the source/read error while surfacing cleanup failure;
		// silently dropping it makes the user believe the failed copy was fully
		// rolled back when manual provider cleanup may still be required.
		if finishErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("discard incomplete destination: %w", finishErr))
		}
		// Never delete a pre-existing destination, a concurrent creator's
		// object, or a remote upload whose commit result is unknown. For a
		// newly-created destination with a definitive local-style failure,
		// removing a partial stream remains the safest rollback.
		rollbackSafe := destinationCloseErr == nil ||
			(!OperationMustNotRetry(destinationCloseErr) &&
				!errors.Is(destinationCloseErr, os.ErrExist) &&
				!errors.Is(destinationCloseErr, os.ErrPermission))
		if !copySuccess && !abortAttempted && !destinationExisted && rollbackSafe {
			dstVfs.Remove(context.Background(), destPathForFile)
		}
	}()

	buf := state.Buffer
	if buf == nil {
		buf = make([]byte, 128*1024)
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, rerr := srcFile.Read(ctx, buf)
		if n > 0 {
			if _, werr := dstFile.Write(buf[:n]); werr != nil {
				return werr
			}
			if state.OnBytes != nil {
				state.OnBytes(n)
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return rerr
		}
	}

	commitAttempted = true
	if cerr := closeDestination(); cerr != nil {
		return cerr
	}
	copySuccess = true

	if copySuccess {
		itemToSet := stat
		itemToSet.Uid = -1
		itemToSet.Gid = -1
		itemToSet.UnixMode = destinationRights(ctx, state, dstVfs, destPathForFile, stat.UnixMode, false, destinationExisted)
		_ = dstVfs.SetAttributes(ctx, destPathForFile, itemToSet)
	}

	if state.Tracker != nil {
		if copySuccess {
			handleArchiveIndexOp(srcVfs, srcPath, dstVfs, destPathForFile, state.IsMove)
		}
		state.Tracker.FileDone()
		if state.UpdateUI != nil {
			state.UpdateUI(true)
		}
	}
	return nil
}

// AskOverwrite shows a rich modal dialog for file conflicts.
func AskOverwrite(ctx context.Context, destPath string, srcStat, dstStat vfs.VFSItem, anchor vtui.Frame) (int, bool) {
	resultChan := make(chan int, 1)
	rememberChan := make(chan bool, 1)
	var dlg *vtui.Window

	vtui.FrameManager.PostTask(func() {
		if ctx.Err() != nil {
			return
		}

		width := 76
		buttons := []*vtui.Button{
			vtui.NewButton(0, 0, i18n.Msg("FileOp.Overwrite")),
			vtui.NewButton(0, 0, i18n.Msg("FileOp.Skip")),
			vtui.NewButton(0, 0, i18n.Msg("FileOp.Rename")),
			vtui.NewButton(0, 0, i18n.Msg("FileOp.Append")),
			vtui.NewButton(0, 0, i18n.Msg("FileOp.Resume")),
			vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel")),
		}
		buttonRows := ButtonRows(buttons, width-4, 1)
		height := 11 + 2*len(buttonRows)
		dlg = vtui.NewCenteredDialog(width, height, i18n.Msg("Warning.Title"))
		dlg.IsWarning = true

		lbl1 := vtui.NewLabel(0, 0, i18n.Msg("FileOp.FileAlreadyExists"), nil)
		truncPath := vtui.TruncateMiddle(destPath, width-6)
		lbl2 := vtui.NewLabel(0, 0, truncPath, nil)

		sep1 := vtui.NewSeparator(0, 0, width, true, true)

		formatInfo := func(label string, stat vfs.VFSItem) string {
			dateStr := stat.MTime.Format("02.01.2006 15:04:05")
			return fmt.Sprintf("%-10s %15d  %s", label, stat.Size, dateStr)
		}

		lblNew := vtui.NewLabel(0, 0, formatInfo("New", srcStat), nil)
		lblExist := vtui.NewLabel(0, 0, formatInfo("Existing", dstStat), nil)

		sep2 := vtui.NewSeparator(0, 0, width, true, true)

		chkRem := vtui.NewCheckbox(0, 0, i18n.Msg("FileOp.RememberChoice"), false)

		sep3 := vtui.NewSeparator(0, 0, width, true, true)

		btnOver, btnSkip, btnRen := buttons[0], buttons[1], buttons[2]
		btnApp, btnRes, btnCan := buttons[3], buttons[4], buttons[5]
		btnOver.IsDefault = true

		dlg.AddItem(lbl1)
		dlg.AddItem(lbl2)
		dlg.AddItem(sep1)
		dlg.AddItem(lblNew)
		dlg.AddItem(lblExist)
		dlg.AddItem(sep2)
		dlg.AddItem(chkRem)
		dlg.AddItem(sep3)
		dlg.AddItem(btnOver)
		dlg.AddItem(btnSkip)
		dlg.AddItem(btnRen)
		dlg.AddItem(btnApp)
		dlg.AddItem(btnRes)
		dlg.AddItem(btnCan)

		vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
		vbox.Add(lbl1, vtui.Margins{}, vtui.AlignCenter)
		vbox.Add(lbl2, vtui.Margins{}, vtui.AlignCenter)
		vbox.Add(sep1, vtui.Margins{Left: -2, Right: -2}, vtui.AlignFill)
		vbox.Add(lblNew, vtui.Margins{}, vtui.AlignLeft)
		vbox.Add(lblExist, vtui.Margins{}, vtui.AlignLeft)
		vbox.Add(sep2, vtui.Margins{Left: -2, Right: -2}, vtui.AlignFill)
		vbox.Add(chkRem, vtui.Margins{}, vtui.AlignLeft)
		vbox.Add(sep3, vtui.Margins{Left: -2, Right: -2}, vtui.AlignFill)

		for i, row := range buttonRows {
			margin := vtui.Margins{}
			if i < len(buttonRows)-1 {
				margin.Bottom = 1
			}
			vbox.Add(row, margin, vtui.AlignFill)
		}
		vbox.Apply()

		btnOver.OnClick = func() { resultChan <- 1; rememberChan <- (chkRem.State == 1); dlg.Close() }
		btnSkip.OnClick = func() { resultChan <- 2; rememberChan <- (chkRem.State == 1); dlg.Close() }
		btnRen.OnClick = func() { resultChan <- 3; rememberChan <- (chkRem.State == 1); dlg.Close() }
		btnApp.OnClick = func() { resultChan <- 4; rememberChan <- (chkRem.State == 1); dlg.Close() }
		btnRes.OnClick = func() { resultChan <- 5; rememberChan <- (chkRem.State == 1); dlg.Close() }
		btnCan.OnClick = func() { resultChan <- 6; rememberChan <- false; dlg.Close() }

		dlg.OnResult = func(code int) {
			if code < 0 {
				select {
				case resultChan <- 6:
					rememberChan <- false
				default:
				}
			}
		}
		if anchor != nil {
			vtui.FrameManager.PushToFrameScreen(anchor, dlg)
		} else {
			vtui.FrameManager.Push(dlg)
		}
	})

	select {
	case res := <-resultChan:
		rem := <-rememberChan
		return res, rem
	case <-ctx.Done():
		vtui.FrameManager.PostTask(func() {
			if dlg != nil && !dlg.IsDone() {
				dlg.Close()
			}
		})
		return 6, false
	}
}

// askDeleteError handles delete errors with Retry/Skip/Skip All/Abort options.
func askDeleteError(ctx context.Context, op string, err error, anchor vtui.Frame) int {
	resultChan := make(chan int, 1)
	var dlg *vtui.Window

	vtui.FrameManager.PostTask(func() {
		if ctx.Err() != nil {
			return
		}
		msg := fmt.Sprintf(i18n.Msg("Delete.ErrorPrompt"), op, err.Error())
		buttons := []string{i18n.Msg("Btn.Retry"), i18n.Msg("Delete.BtnSkip"), i18n.Msg("Btn.SkipAll"), i18n.Msg("Delete.BtnAbort")}
		title := " " + strings.TrimSpace(i18n.Msg("Error.Title")) + " "
		if anchor != nil {
			dlg = vtui.ShowMessageOn(anchor, title, msg, buttons)
		} else {
			dlg = vtui.ShowMessage(title, msg, buttons)
		}
		dlg.OnResult = func(code int) {
			if code < 0 {
				code = 3
			}
			select {
			case resultChan <- code:
			default:
			}
		}
	})

	select {
	case res := <-resultChan:
		return res
	case <-ctx.Done():
		vtui.FrameManager.PostTask(func() {
			if dlg != nil && !dlg.IsDone() {
				dlg.Close()
			}
		})
		return 3
	}
}

func AskRename(ctx context.Context, oldName string, anchor vtui.Frame) string {
	resultChan := make(chan string, 1)
	var dlg *vtui.Window
	vtui.FrameManager.PostTask(func() {
		if ctx.Err() != nil {
			return
		}
		dlg = vtui.InputBoxOn(anchor, " Rename ", "New name:", oldName, func(s string) {
			select {
			case resultChan <- s:
			default:
			}
		})
		dlg.OnResult = func(code int) {
			if code < 0 {
				select {
				case resultChan <- "":
				default:
				}
			}
		}
	})
	select {
	case res := <-resultChan:
		return res
	case <-ctx.Done():
		vtui.FrameManager.PostTask(func() {
			if dlg != nil && !dlg.IsDone() {
				dlg.Close()
			}
		})
		return ""
	}
}

// AskError handles I/O errors by asking user for Retry/Skip/Abort
func AskError(ctx context.Context, op string, err error, anchor vtui.Frame) int {
	resultChan := make(chan int, 1)
	var dlg *vtui.Window

	vtui.FrameManager.PostTask(func() {
		if ctx.Err() != nil {
			return
		}
		msg := fmt.Sprintf("%s:\n%s\n\n%s", op, err.Error(), "What to do?")
		if anchor != nil {
			dlg = vtui.ShowMessageOn(anchor, " Error ", msg, []string{i18n.Msg("Btn.Retry"), "&Skip", "&Abort"})
		} else {
			dlg = vtui.ShowMessage(" Error ", msg, []string{i18n.Msg("Btn.Retry"), "&Skip", "&Abort"})
		}
		dlg.OnResult = func(code int) {
			if code < 0 {
				code = 2
			}
			select {
			case resultChan <- code:
			default:
			}
		}
	})

	select {
	case res := <-resultChan:
		return res
	case <-ctx.Done():
		vtui.FrameManager.PostTask(func() {
			if dlg != nil && !dlg.IsDone() {
				dlg.Close()
			}
		})
		return 2 // 2 matches Abort button index
	}
}

// NewGlobalAwareReporter wraps a reporter so per-file progress is rewritten in
// terms of the whole operation. The fields are private because the wrapper
// also carries running counters it owns; a caller supplies the four things it
// cannot derive.
func NewGlobalAwareReporter(original TaskReporter, getGlobal func(action string) (string, int, string), tracker *FileOpTracker, onBytes func(int)) *GlobalAwareReporter {
	return &GlobalAwareReporter{
		original:  original,
		getGlobal: getGlobal,
		tracker:   tracker,
		onBytes:   onBytes,
	}
}
