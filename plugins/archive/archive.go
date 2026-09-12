package archive

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/unxed/archives"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/sevenzip"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"github.com/unxed/zipper/archive"
)

var activeOps sync.Map

const (
	archiveAddCommandID     = "archive.add"
	archiveExtractCommandID = "archive.extract"
)

type ArchivePlugin struct {
	registrations []vfs.Registration
}

// archiveHostAPI lets archive commands trigger built-in panel actions, such
// as copying the selected members out of an opened archive.
var archiveHostAPI vfs.HostAPI

func (p *ArchivePlugin) Init(api vfs.HostAPI) error {
	archiveHostAPI = api
	if contributions, ok := api.(vfs.ContributionHost); ok {
		addRegistration, err := contributions.RegisterPluginCommand(vfs.PluginCommand{
			ID:             archiveAddCommandID,
			Location:       vfs.PluginCommandPanel,
			Label:          "Add to archive",
			LabelKey:       "Archive.Command.Add",
			MenuPath:       "Files",
			Shortcut:       "Shift+F1",
			Description:    "Create an archive from the selected files",
			DescriptionKey: "Archive.Command.Add.Desc",
			SearchKeys:     []string{"Attributes.Archive"},
			Run:            actionAddArchive,
		})
		if err != nil {
			return fmt.Errorf("archive: register add command: %w", err)
		}

		extractRegistration, err := contributions.RegisterPluginCommand(vfs.PluginCommand{
			ID:             archiveExtractCommandID,
			Location:       vfs.PluginCommandPanel,
			Label:          "Extract files",
			LabelKey:       "Archive.Command.Extract",
			MenuPath:       "Files",
			Shortcut:       "Shift+F2",
			Description:    "Extract the selected archive to the passive panel",
			DescriptionKey: "Archive.Command.Extract.Desc",
			SearchKeys:     []string{"Attributes.Archive"},
			Run:            actionExtractArchive,
		})
		if err != nil {
			addRegistration.Unregister()
			return fmt.Errorf("archive: register extract command: %w", err)
		}
		p.registrations = append(p.registrations, addRegistration, extractRegistration)
	}

	api.RegisterVFSProvider(&ArchiveProvider{})

	// Keep far2l's Files-menu shortcuts: direct archive operations are
	// Shift+F1/Shift+F2, while Shift+F3 tests the selected archive directly.
	api.RegisterGlobalHotkey(vtinput.VK_F1, vtinput.ShiftPressed, actionAddArchive)
	api.RegisterGlobalHotkey(vtinput.VK_F2, vtinput.ShiftPressed, actionExtractArchive)
	api.RegisterGlobalHotkey(vtinput.VK_F3, vtinput.ShiftPressed, actionTestArchive)

	return nil
}

// resolveLocalArchivePath returns the absolute path of the archive to operate
// on when the active panel is a local filesystem or a local ArchiveVFS.
func resolveLocalArchivePath(app vfs.App) (string, bool) {
	srcVfs := app.GetActivePanelVFS()
	if srcVfs == nil {
		return "", false
	}
	if archiveVFS, ok := srcVfs.(*ArchiveVFS); ok {
		return archiveVFS.LocalArchivePath()
	}
	name := app.GetSelectedName()
	if name == "" || name == ".." {
		return "", false
	}
	osvfs, ok := srcVfs.(*vfs.OSVFS)
	if !ok {
		return "", false
	}
	srcPath, _ := osvfs.Abs(srcVfs.Join(srcVfs.GetPath(), name))
	return srcPath, true
}

// actionExtractArchive runs on the UI thread as a global hotkey handler, so
// every blocking prompt (app.Message waits for the UI loop) must run on a
// separate goroutine. Calling app.Message synchronously here deadlocked f4
// on Shift+F2 inside an archive.
func actionExtractArchive(app vfs.App) {
	srcVfs := app.GetActivePanelVFS()
	dstVfs := app.GetPassivePanelVFS()
	if srcVfs == nil || dstVfs == nil {
		return
	}

	if _, ok := srcVfs.(*ArchiveVFS); ok {
		// Inside an archive, "Extract files" means copying the selected
		// members to the passive panel, which is exactly the built-in copy.
		if archiveHostAPI != nil {
			go archiveHostAPI.RunAction("File.Copy")
		}
		return
	}

	srcPath, ok := resolveLocalArchivePath(app)
	if !ok {
		if name := app.GetSelectedName(); name != "" && name != ".." {
			go app.Message(" Error ", "Extraction supported only from local filesystem", []string{"&Ok"})
		}
		return
	}
	destDir := dstVfs.GetPath()

	go extractArchiveAsync(app, srcPath, destDir)
}

func extractArchiveAsync(app vfs.App, srcPath, destDir string) {
	isBusy := false
	if _, active := activeOps.Load(srcPath); active {
		isBusy = true
	} else if !vfs.GlobalArchiveLockManager.TryLock(srcPath) {
		isBusy = true
	} else {
		// TryLock succeeded, meaning it was NOT busy. We must unlock it here
		// so that the background worker can safely Lock() it later.
		vfs.GlobalArchiveLockManager.Unlock(srcPath)
	}

	waitLock := true
	if isBusy {
		res := app.Message(" Archive Busy ", "This archive is currently being processed.\nRunning multiple operations simultaneously may severely degrade performance.", []string{"&Queue", "&Parallel", "&Cancel"})
		if res == 2 || res < 0 {
			return
		}
		waitLock = (res == 0)
	}

	app.RunAdvancedProgressTask(" Extracting... ", false, func(ctx context.Context, reporter vfs.TaskReporter) error {
		if waitLock {
			reporter.UpdateTransfer("Waiting", "in queue...", -1, "", -1, "")
			vfs.GlobalArchiveLockManager.Lock(srcPath)
			defer vfs.GlobalArchiveLockManager.Unlock(srcPath)
		}
		reporter.UpdateTransfer("Extracting", "files...", -1, "", -1, "")
		return extractArchiveWithPasswordPrompt(ctx, srcPath, destDir, reporter)

	}, func(err error) {
		if err != nil && err != context.Canceled {
			go app.Message(" Error ", fmt.Sprintf("Extraction failed:\n%v", err), []string{"&Ok"})
		}
		app.RefreshAll()
	})
}

func extractArchiveWithPasswordPrompt(ctx context.Context, srcPath, destDir string, reporter vfs.TaskReporter) error {
	var password string
	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()
	for {
		err := extractArchiveOnce(ctx, srcPath, destDir, password, reporter)
		if err == nil || !isArchivePasswordRetryError(err) {
			return err
		}

		// One hold for the whole ask/retry cycle; see
		// openArchiveFSWithPasswordPrompt.
		if release == nil {
			release = vfs.HoldInteractivePrompt()
		}
		password, err = promptArchivePasswordUntilProvided(ctx, filepath.Base(srcPath))
		if err != nil {
			return err
		}
	}
}

// actionTestArchive verifies every regular member of the selected archive by
// reading it to completion, without writing anything to the panels. Password
// prompts behave like everywhere else in the plugin.
func actionTestArchive(app vfs.App) {
	srcPath, ok := resolveLocalArchivePath(app)
	if !ok {
		if name := app.GetSelectedName(); name != "" && name != ".." {
			go app.Message(" Error ", "Testing supported only for local archives", []string{"&Ok"})
		}
		return
	}
	go func() {
		app.RunAdvancedProgressTask(" Testing... ", false, func(ctx context.Context, reporter vfs.TaskReporter) error {
			reporter.UpdateTransfer("Testing", filepath.Base(srcPath), -1, "", -1, "")
			return testArchiveWithPasswordPrompt(ctx, srcPath, reporter)
		}, func(err error) {
			if err == nil {
				go app.Message(" Test archive ", fmt.Sprintf("%s\nNo errors found.", filepath.Base(srcPath)), []string{"&Ok"})
			} else if err != context.Canceled {
				go showArchiveTestFailure(app, srcPath, err)
			}
		})
	}()
}

func testArchiveWithPasswordPrompt(ctx context.Context, srcPath string, reporter vfs.TaskReporter) error {
	var password string
	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()

	for {
		err := testArchiveOnce(ctx, srcPath, password, reporter)
		if err == nil || !isArchivePasswordRetryError(err) {
			return err
		}

		if release == nil {
			release = vfs.HoldInteractivePrompt()
		}
		password, err = promptArchivePasswordUntilProvided(ctx, filepath.Base(srcPath))
		if err != nil {
			return err
		}
	}
}

type archiveTestingReader struct {
	io.Reader
	read int64
}

func (r *archiveTestingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	atomic.AddInt64(&r.read, int64(n))
	return n, err
}

type archiveTestingReaderAtSeeker struct {
	*archiveTestingReader
	io.ReaderAt
	io.Seeker
}

func (r *archiveTestingReaderAtSeeker) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, offset)
	atomic.AddInt64(&r.read, int64(n))
	return n, err
}

func archiveTestingPercent(current, total int64) int {
	if total <= 0 {
		return -1
	}
	pct := int(float64(current) * 100 / float64(total))
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// archiveTestingTotalPercent keeps the overall bar on screen for an archive
// that holds no data at all -- one of only empty files, say. Its testing pass
// is finished rather than of unknown length, which is what a hidden bar would
// otherwise say.
func archiveTestingTotalPercent(done, total int64, known bool) int {
	if known && total <= 0 {
		return 100
	}
	return archiveTestingPercent(done, total)
}

// archiveTestTotals is what the overall progress bar counts against: the
// uncompressed volume the archive says it holds, taken from the archive's own
// directory before anything is decoded. known is false when no such figure
// could be obtained, and the testing pass then falls back to a coarser
// measure.
type archiveTestTotals struct {
	bytes int64
	files int64
	known bool
}

// archiveFormatListsWithoutDecoding reports whether a format can enumerate its
// members from a directory or a header instead of by decoding payloads: zip
// keeps a central directory, 7z a header, and rar a chain of file block
// headers that is walked by skipping over packed data. Tar has none of that,
// and neither do the compressed-tar combinations built on it, so listing one
// costs a full decode -- the testing pass must not pay for that twice just to
// label its progress bar.
func archiveFormatListsWithoutDecoding(format archives.Format) bool {
	switch format.(type) {
	case archives.Zip, *archives.Zip,
		archives.SevenZip, *archives.SevenZip,
		archives.Rar, *archives.Rar:
		return true
	}
	return false
}

// openArchiveTestStream opens srcPath and identifies it with the same password
// and RAR volume configuration the rest of the plugin applies, so that the
// listing pass and the testing pass see the same archive. The caller owns the
// returned file.
func openArchiveTestStream(ctx context.Context, srcPath, password string) (*os.File, archives.Format, io.Reader, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, nil, nil, err
	}
	format, stream, err := archives.Identify(ctx, srcPath, f)
	if err != nil {
		_ = f.Close() // Identification failed; nothing was read out of the archive.
		return nil, nil, nil, err
	}
	if configured, ok := configureRARArchiveFormat(format, srcPath, password); ok {
		format = configured
	} else {
		format, _ = archivePasswordFormat(format, password)
	}
	return f, format, stream, nil
}

// collectArchiveTestTotals adds up the size of every regular member the
// archive lists. Only a format that can be listed without decoding is walked,
// so this costs a header read rather than a second decompression pass.
//
// A password error is passed back so the caller can ask for one and retry.
// Every other failure only leaves the totals unknown: testing a damaged
// archive is precisely what the operation is for, and it has to run even when
// the directory cannot be read.
func collectArchiveTestTotals(ctx context.Context, srcPath, password string) (archiveTestTotals, error) {
	f, format, stream, err := openArchiveTestStream(ctx, srcPath, password)
	if err != nil {
		if isArchivePasswordRetryError(err) {
			return archiveTestTotals{}, err
		}
		return archiveTestTotals{}, nil
	}
	defer func() { _ = f.Close() }()

	extractor, ok := format.(archives.Extractor)
	if !ok || !archiveFormatListsWithoutDecoding(format) {
		return archiveTestTotals{}, nil
	}

	var (
		mu      sync.Mutex
		totals  archiveTestTotals
		unsized bool
	)
	// 7z reports its members from several goroutines at once, one per
	// compression stream, so the accumulator below is locked.
	err = extractor.Extract(ctx, stream, func(ctx context.Context, info archives.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		size := info.Size()
		mu.Lock()
		if size < 0 {
			unsized = true
		} else {
			totals.bytes += size
			totals.files++
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		if isArchivePasswordRetryError(err) {
			return archiveTestTotals{}, err
		}
		return archiveTestTotals{}, nil
	}
	if unsized {
		return archiveTestTotals{}, nil
	}
	totals.known = true
	return totals, nil
}

func testArchiveOnce(ctx context.Context, srcPath, password string, reporter vfs.TaskReporter) error {
	totals, err := collectArchiveTestTotals(ctx, srcPath, password)
	if err != nil {
		return err
	}

	f, format, stream, err := openArchiveTestStream(ctx, srcPath, password)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	archiveStat, err := f.Stat()
	if err != nil {
		return err
	}
	archiveSize := archiveStat.Size()

	extractor, ok := format.(archives.Extractor)
	if !ok {
		return fmt.Errorf("format %T does not support testing", format)
	}

	countedReader := &archiveTestingReader{Reader: stream}
	var countedStream io.Reader = countedReader
	if readerAt, ok := stream.(io.ReaderAt); ok {
		if seeker, ok := stream.(io.Seeker); ok {
			countedStream = &archiveTestingReaderAtSeeker{
				archiveTestingReader: countedReader,
				ReaderAt:             readerAt,
				Seeker:               seeker,
			}
		}
	}
	// testedBytes counts the data read out of the members themselves. That is
	// what the overall bar shows whenever the archive told us how much it
	// holds; counting consumed archive bytes instead pins the bar at 100% on
	// formats whose decoder re-reads and seeks around its input, 7z among
	// them. 7z also hands members to several goroutines at once, so the
	// counter is atomic.
	var testedBytes int64
	overallProgress := func() (done, total int64) {
		if totals.known {
			return atomic.LoadInt64(&testedBytes), totals.bytes
		}
		// Nothing to count against: fall back to how much of the archive file
		// has been consumed. The formats that land here are read front to
		// back, so that figure still climbs steadily.
		return atomic.LoadInt64(&countedReader.read), archiveSize
	}
	startTime := time.Now()
	// Reading the counter and delivering the update are two separate steps,
	// and 7z runs this callback on one goroutine per compression stream. The
	// counter itself only grows, but without a lock around both steps a
	// goroutine that read the smaller figure can still reach the reporter
	// last, and the overall bar then jumps backwards even though nothing was
	// un-tested. Holding one lock across the read and the call makes the
	// order the reporter sees the order the counter actually went through.
	var reportMu sync.Mutex
	reportProgress := func(name string, current, size int64) {
		reportMu.Lock()
		defer reportMu.Unlock()
		done, total := overallProgress()
		elapsed := time.Since(startTime)
		speed := int64(0)
		if elapsed > 0 {
			speed = int64(float64(done) / elapsed.Seconds())
		}
		totalText := fmt.Sprintf("Total: %s / %s", formatSize(done), formatSize(total))
		reporter.UpdateTransfer("Testing", name, archiveTestingPercent(current, size), totalText,
			archiveTestingTotalPercent(done, total, totals.known), formatSize(speed)+"/s")
	}
	reportProgress(filepath.Base(srcPath), 0, 1)

	var failuresMu sync.Mutex
	var failures []error
	addFailure := func(failure error) {
		failuresMu.Lock()
		failures = append(failures, failure)
		failuresMu.Unlock()
	}
	err = extractor.Extract(ctx, countedStream, func(ctx context.Context, info archives.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			reportProgress(info.NameInArchive, -1, 0)
			return nil
		}

		member, openErr := info.Open()
		if openErr != nil {
			reportProgress(info.NameInArchive, 0, info.Size())
			addFailure(fmt.Errorf("%s: %w", info.NameInArchive, openErr))
			return nil
		}

		memberSize := info.Size()
		var memberBytes int64
		var readErr error
		buf := make([]byte, 128*1024)
		for {
			if err := ctx.Err(); err != nil {
				readErr = err
				break
			}
			n, err := member.Read(buf)
			if n > 0 {
				memberBytes += int64(n)
				atomic.AddInt64(&testedBytes, int64(n))
				reportProgress(info.NameInArchive, memberBytes, memberSize)
			}
			if err != nil {
				if err != io.EOF {
					readErr = err
				}
				break
			}
		}
		closeErr := member.Close()
		if failure := errors.Join(readErr, closeErr); failure != nil {
			addFailure(fmt.Errorf("%s: %w", info.NameInArchive, failure))
		}
		return nil
	})
	if err != nil {
		addFailure(err)
	}
	failuresMu.Lock()
	defer failuresMu.Unlock()
	if len(failures) == 0 {
		done, total := overallProgress()
		if !totals.known {
			done = archiveSize
		}
		if total < done {
			// A member that held more data than its header promised: report
			// what was actually read rather than a bar past its own end.
			total = done
		}
		reportMu.Lock()
		reporter.UpdateTransfer("Testing", filepath.Base(srcPath), 100,
			fmt.Sprintf("Total: %s / %s", formatSize(done), formatSize(total)), 100, "")
		reportMu.Unlock()
	}
	return errors.Join(failures...)
}

func showArchiveTestFailure(app vfs.App, srcPath string, err error) {
	report := formatArchiveTestFailure(srcPath, err)
	if app.Message(" Test archive ", report, []string{"&Copy list", "&Close"}) == 0 {
		go vtui.SetClipboard(report)
	}
}

func formatArchiveTestFailure(srcPath string, err error) string {
	return fmt.Sprintf("Test failed for %s:\n%s", filepath.Base(srcPath), err)
}

func extractArchiveOnce(ctx context.Context, srcPath, destDir, password string, reporter vfs.TaskReporter) error {
	ex, err := archive.NewExtractor(srcPath, destDir, archive.Options{Xattrs: false, SafeWrites: true, Password: password})
	if err != nil {
		return err
	}
	defer func() {
		_ = ex.Close() // Extraction is complete; the archive input is read-only.
	}()

	done := make(chan struct{})
	tickerDone := make(chan struct{})
	defer func() {
		close(done)
		<-tickerDone
	}()
	startTime := time.Now()

	showProgress := func() {
		bytes, entries := ex.Written()
		elapsed := time.Since(startTime)
		speed := float64(0)
		if elapsed.Seconds() > 0 {
			speed = float64(bytes) / elapsed.Seconds()
		}
		speedStr := formatSize(int64(speed)) + "/s"

		elapsedStr := fmt.Sprintf("Time: %02d:%02d:%02d", int(elapsed.Hours()), int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)

		// Нам также нужно поправить и второе вхождение в actionAddArchive:
		timeSpeedText := fmt.Sprintf("%-16s %-21s %15s", elapsedStr, "", speedStr)

		totalText := fmt.Sprintf("Total: %s", formatSize(bytes))

		currFile := fmt.Sprintf("%d files", entries)
		if fp, ok := ex.(interface{ CurrentFile() string }); ok {
			if name := fp.CurrentFile(); name != "" {
				currFile = name
			}
		}

		reporter.UpdateTransfer("Extracting", currFile, -1, totalText, -1, timeSpeedText)
	}
	showProgress()

	go func() {
		defer close(tickerDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				showProgress()
			}
		}
	}()

	if err := ex.Extract(ctx); err != nil {
		return err
	}
	return validateExtracted7z(ctx, srcPath, destDir, password)
}

// validateExtracted7z checks the uncompressed size and CRC recorded in a 7z
// header against the files written by zipper.  7z permits encrypting payloads
// while leaving headers visible; with a wrong password some small stored files
// can therefore be produced as an empty, successful extraction.  The normal
// extractor has no error to trigger a retry in that case, while the header
// checksum gives us a reliable postcondition for the password attempt.
func validateExtracted7z(ctx context.Context, srcPath, destDir, password string) error {
	if !strings.EqualFold(filepath.Ext(srcPath), ".7z") {
		return nil
	}

	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	format, stream, err := archives.Identify(ctx, srcPath, f)
	if err != nil {
		return err
	}
	format, _ = archivePasswordFormat(format, password)
	extractor, ok := format.(archives.Extractor)
	if !ok {
		return nil
	}

	return extractor.Extract(ctx, stream, func(ctx context.Context, info archives.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}

		var header sevenzip.FileHeader
		switch value := info.Header.(type) {
		case sevenzip.FileHeader:
			header = value
		case *sevenzip.FileHeader:
			if value == nil {
				return nil
			}
			header = *value
		default:
			return nil
		}
		target, err := validatedArchiveOutputPath(destDir, info.NameInArchive)
		if err != nil {
			return err
		}
		stat, err := os.Stat(target)
		if err != nil {
			return fmt.Errorf("%w: output file is missing: %v", newArchivePasswordValidationError("%s", info.NameInArchive), err)
		}
		if stat.Size() != int64(header.UncompressedSize) {
			return newArchivePasswordValidationError("%s: extracted %d bytes, want %d", info.NameInArchive, stat.Size(), header.UncompressedSize)
		}
		if header.CRC32 == 0 {
			return nil
		}

		out, err := os.Open(target)
		if err != nil {
			return err
		}
		h := crc32.NewIEEE()
		_, copyErr := io.Copy(h, out)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if h.Sum32() != header.CRC32 {
			return newArchivePasswordValidationError("%s: extracted data checksum does not match", info.NameInArchive)
		}
		return nil
	})
}

func validatedArchiveOutputPath(destDir, name string) (string, error) {
	cleanName := filepath.Clean(filepath.FromSlash(name))
	if cleanName == "." || filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive entry path %q", name)
	}

	base, err := filepath.Abs(destDir)
	if err != nil {
		return "", fmt.Errorf("resolve extraction destination: %w", err)
	}
	target, err := filepath.Abs(filepath.Join(base, cleanName))
	if err != nil {
		return "", fmt.Errorf("resolve extraction target: %w", err)
	}
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry path escapes extraction destination: %q", name)
	}
	return target, nil
}

func actionAddArchive(app vfs.App) {
	activeVfs := app.GetActivePanelVFS()
	if activeVfs == nil {
		return
	}

	names := app.GetSelectedNames()
	// The parent row is navigation, not an archive input. Treating ".." as a
	// selected file opened the archive prompt for an invalid operation and
	// could leave a modal overlay without a useful target (#983).
	validNames := make([]string, 0, len(names))
	for _, name := range names {
		if name != ".." {
			validNames = append(validNames, name)
		}
	}
	names = validNames
	if len(names) == 0 {
		return
	}

	arcName := activeVfs.Base(activeVfs.GetPath())
	if arcName == "." || arcName == "" {
		arcName = "archive"
	}
	arcName += ".zip"

	app.InputBox(" Add to archive ", "Archive name:", arcName, func(name string) {
		if name == "" {
			return
		}
		fullArcPath := activeVfs.Join(activeVfs.GetPath(), name)

		go func() {
			var absArcPath string
			if osvfs, ok := activeVfs.(*vfs.OSVFS); ok {
				absArcPath, _ = osvfs.Abs(fullArcPath)
			} else {
				absArcPath = fullArcPath
			}

			isBusy := false
			if _, active := activeOps.Load(absArcPath); active {
				isBusy = true
			} else if !vfs.GlobalArchiveLockManager.TryLock(absArcPath) {
				isBusy = true
			} else {
				vfs.GlobalArchiveLockManager.Unlock(absArcPath)
			}

			waitLock := true
			if isBusy {
				res := app.Message(" Archive Busy ", "This archive is currently being processed.\nRunning multiple operations simultaneously may severely degrade performance.", []string{"&Queue", "&Parallel", "&Cancel"})
				if res == 2 || res < 0 {
					return
				}
				waitLock = (res == 0)
			}

			if _, err := activeVfs.Stat(context.Background(), fullArcPath); err == nil {
				msg := "The target archive already exists.\nDo you want to overwrite it?"
				if app.Message(" Warning ", msg, []string{"&Yes", "&No"}) != 0 {
					return
				}
			}

			app.RunAdvancedProgressTask(" Archiving... ", false, func(ctx context.Context, reporter vfs.TaskReporter) (retErr error) {
				if waitLock {
					reporter.UpdateTransfer("Waiting", "in queue...", -1, "", -1, "")
					vfs.GlobalArchiveLockManager.Lock(absArcPath)
					defer vfs.GlobalArchiveLockManager.Unlock(absArcPath)
				}
				reporter.UpdateTransfer("Archiving", "files...", -1, "", -1, "")

				fileMap := make(map[string]os.FileInfo)
				var totalBytes int64
				for _, n := range names {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					reporter.UpdateScan(n, int64(len(fileMap)), 0)

					fullPath := activeVfs.Join(activeVfs.GetPath(), n)
					if osvfs, ok := activeVfs.(*vfs.OSVFS); ok {
						absPath, _ := osvfs.Abs(fullPath)
						if err := filepath.Walk(absPath, func(p string, fi os.FileInfo, e error) error {
							if e != nil {
								return e
							}
							fileMap[p] = fi
							if !fi.IsDir() {
								totalBytes += fi.Size()
							}
							return nil
						}); err != nil {
							return fmt.Errorf("scan %q for archiving: %w", n, err)
						}
					}
				}

				a, err := archive.NewArchiver(fullArcPath, activeVfs.GetPath(), archive.Options{Xattrs: false})
				if err != nil {
					return err
				}
				defer func() {
					retErr = joinArchiveCloseError(retErr, a.Close())
				}()

				done := make(chan struct{})
				defer close(done)
				startTime := time.Now()

				showProgress := func() {
					bytes, entries := a.Written()
					elapsed := time.Since(startTime)
					speed := float64(0)
					if elapsed.Seconds() > 0 {
						speed = float64(bytes) / elapsed.Seconds()
					}

					pct := -1
					if totalBytes > 0 {
						pct = int((bytes * 100) / totalBytes)
					}
					if pct > 100 {
						pct = 100
					}

					speedStr := formatSize(int64(speed)) + "/s"

					etaStr := "Remaining: ??:??:??"
					if totalBytes > 0 && bytes > 0 && elapsed.Seconds() > 0.5 {
						ratio := float64(bytes) / float64(totalBytes)
						etaSecs := (elapsed.Seconds() / ratio) - elapsed.Seconds()
						if etaSecs >= 0 && etaSecs < 3600*100 {
							etaDur := time.Duration(etaSecs * float64(time.Second))
							etaStr = fmt.Sprintf("Remaining: %02d:%02d:%02d", int(etaDur.Hours()), int(etaDur.Minutes())%60, int(etaDur.Seconds())%60)
						}
					}

					elapsedStr := fmt.Sprintf("Time: %02d:%02d:%02d", int(elapsed.Hours()), int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)
					timeSpeedText := fmt.Sprintf("%-16s %-21s %15s", elapsedStr, etaStr, speedStr)

					totalText := fmt.Sprintf("Total: %s / %s", formatSize(bytes), formatSize(totalBytes))

					reporter.UpdateTransfer("Archiving", fmt.Sprintf("%d files", entries), -1, totalText, pct, timeSpeedText)
				}
				showProgress()

				go func() {
					ticker := time.NewTicker(100 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-done:
							return
						case <-ticker.C:
							showProgress()
						}
					}
				}()

				return a.Archive(ctx, fileMap)
			}, func(err error) {
				if err != nil && err != context.Canceled {
					go app.Message(" Error ", fmt.Sprintf("Archiving failed:\n%v", err), []string{"&Ok"})
				}
				if err == nil {
					app.SetPendingSelection(name)
				}
				app.RefreshAll()
			})
		}()
	})
}

func (p *ArchivePlugin) Close() error {
	registrations := p.registrations
	p.registrations = nil
	for index := len(registrations) - 1; index >= 0; index-- {
		registrations[index].Unregister()
	}
	closeSharedArchiveMaterializations()
	return nil
}
func (p *ArchivePlugin) GetName() string { return "Archive Support" }
