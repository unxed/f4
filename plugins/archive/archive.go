package archive

import (
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/unxed/archives"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/sevenzip"
	"github.com/unxed/vtinput"
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
	// Shift+F1/Shift+F2, while the legacy two-item archive command menu stays
	// available on Shift+F3.
	api.RegisterGlobalHotkey(vtinput.VK_F1, vtinput.ShiftPressed, actionAddArchive)
	api.RegisterGlobalHotkey(vtinput.VK_F2, vtinput.ShiftPressed, actionExtractArchive)
	api.RegisterGlobalHotkey(vtinput.VK_F3, vtinput.ShiftPressed, actionArchiveCommands)

	return nil
}

func actionArchiveCommands(app vfs.App) {
	app.Menu(" Archive Commands ", []string{"&1. Add to archive", "&2. Extract files", "&3. Test archive"}, func(idx int) {
		switch idx {
		case 0:
			actionAddArchive(app)
		case 1:
			actionExtractArchive(app)
		case 2:
			actionTestArchive(app)
		}
	})
}

// resolveLocalArchivePath returns the absolute path of the selected archive
// when the active panel is a local filesystem.
func resolveLocalArchivePath(app vfs.App) (string, bool) {
	srcVfs := app.GetActivePanelVFS()
	if srcVfs == nil {
		return "", false
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

// actionTestArchive verifies that every member of the selected archive can
// be extracted and passes its size/CRC checks, without writing anything to
// the panels. Password prompts behave like everywhere else in the plugin.
func actionTestArchive(app vfs.App) {
	srcPath, ok := resolveLocalArchivePath(app)
	if !ok {
		if name := app.GetSelectedName(); name != "" && name != ".." {
			go app.Message(" Error ", "Testing supported only for local archives", []string{"&Ok"})
		}
		return
	}
	go func() {
		tempDir, err := os.MkdirTemp("", "f4arc-test-*")
		if err != nil {
			app.Message(" Error ", fmt.Sprintf("Test failed:\n%v", err), []string{"&Ok"})
			return
		}
		app.RunAdvancedProgressTask(" Testing... ", false, func(ctx context.Context, reporter vfs.TaskReporter) error {
			reporter.UpdateTransfer("Testing", filepath.Base(srcPath), -1, "", -1, "")
			return extractArchiveWithPasswordPrompt(ctx, srcPath, tempDir, reporter)
		}, func(err error) {
			_ = os.RemoveAll(tempDir)
			if err == nil {
				go app.Message(" Test archive ", fmt.Sprintf("%s\nNo errors found.", filepath.Base(srcPath)), []string{"&Ok"})
			} else if err != context.Canceled {
				go app.Message(" Error ", fmt.Sprintf("Test failed:\n%v", err), []string{"&Ok"})
			}
		})
	}()
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
