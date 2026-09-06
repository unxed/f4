package archive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/klauspost/compress/flate"
	"github.com/unxed/archives"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/sevenzip"
	"github.com/unxed/vtui"
	"github.com/unxed/zip"
	zipperarchive "github.com/unxed/zipper/archive"
)

type archivePasswordResult struct {
	password string
	err      error
}

// archivePasswordPrompt is replaceable in tests so password handling can be
// verified without depending on an interactive terminal.
var archivePasswordPrompt = promptArchivePassword

type archivePasswordValidationError struct {
	message string
}

func (e archivePasswordValidationError) Error() string {
	return "archive password rejected: " + e.message
}

func newArchivePasswordValidationError(format string, args ...any) error {
	return archivePasswordValidationError{message: fmt.Sprintf(format, args...)}
}

// isArchivePasswordRetryError extends zipper's password classification with
// lazy 7z payload errors.  7z archives may leave their headers unencrypted;
// in that case opening and listing the archive succeeds with an empty or
// wrong password, and the decoder reports only a sevenzip.ReadError on the
// first file read.  The ReadError does not always carry the Encrypted flag,
// so zipperarchive.IsPasswordError cannot identify this case by itself.
func isArchivePasswordRetryError(err error) bool {
	if zipperarchive.IsPasswordError(err) {
		return true
	}
	var readErr sevenzip.ReadError
	if errors.As(err, &readErr) {
		return true
	}
	var readErrPtr *sevenzip.ReadError
	return errors.As(err, &readErrPtr) && readErrPtr != nil
}

func promptArchivePassword(ctx context.Context, archiveName string) (string, error) {
	if vtui.FrameManager == nil {
		return "", errors.New("archive: cannot request a password without an active UI")
	}

	result := make(chan archivePasswordResult, 1)
	vtui.FrameManager.PostTask(func() {
		showArchivePasswordDialog(archiveName, result)
	})

	select {
	case value := <-result:
		return value.password, value.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func showArchivePasswordDialog(archiveName string, result chan<- archivePasswordResult) {
	dlg := vtui.NewCenteredDialog(52, 7, vtui.Msg("Archive.PasswordTitle"))
	dlg.ShowClose = true

	x := dlg.X1 + 2
	y := dlg.Y1 + 2
	password := vtui.NewPasswordEdit(x+12, y, 34, "")
	dlg.AddItem(vtui.NewLabel(x, y, vtui.Msg("Archive.Password"), password))
	dlg.AddItem(password)

	ok := vtui.NewButton(dlg.X1+15, dlg.Y2-2, vtui.Msg("vtui.Ok"))
	ok.IsDefault = true
	cancel := vtui.NewButton(dlg.X1+28, dlg.Y2-2, vtui.Msg("vtui.Cancel"))
	dlg.AddItem(ok)
	dlg.AddItem(cancel)

	finished := false
	finish := func(value archivePasswordResult) {
		if finished {
			return
		}
		finished = true
		result <- value
	}
	ok.OnClick = func() {
		finish(archivePasswordResult{password: password.GetText()})
		password.SetText("")
		dlg.Close()
	}
	cancel.OnClick = func() { dlg.Close() }
	dlg.OnResult = func(code int) {
		if code < 0 {
			finish(archivePasswordResult{err: context.Canceled})
		}
	}

	vtui.FrameManager.Push(dlg)
}

// zipCryptoPayloadError reports the errors a ZipCrypto member produces when
// a wrong password slips past the format's one-byte password check, which
// happens for 1 in 256 wrong passwords: the stored payload then fails its
// CRC (zip.ErrChecksum) or the deflate stream turns out to be garbage.
// Neither error mentions a password, so isArchivePasswordRetryError cannot
// see it; the archive VFS applies this only after it has installed a
// password, i.e. when the member is known to be encrypted.
func zipCryptoPayloadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, zip.ErrChecksum) {
		return true
	}
	var corrupt flate.CorruptInputError
	return errors.As(err, &corrupt)
}

// passwordInstalled reports whether the archive is open with a password,
// i.e. whether its members are known to be encrypted.
func (v *ArchiveVFS) passwordInstalled() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.password != ""
}

// memberReadError is applied to every error returned while reading a member
// after a password has been installed. It reclassifies the ZipCrypto
// wrong-password symptoms above as a rejected password, so the password
// dialog comes back exactly as it does for the other 255 wrong passwords.
func (v *ArchiveVFS) memberReadError(err error) error {
	if v == nil || err == nil || !zipCryptoPayloadError(err) {
		return err
	}
	if !v.passwordInstalled() {
		return err
	}
	return fmt.Errorf("%w: %w", newArchivePasswordValidationError("payload does not decrypt with this password"), err)
}

// promptArchivePasswordUntilProvided asks for a password the way FAR does:
// an empty answer simply shows the dialog again, and only closing the
// dialog (Cancel/Esc) gives up.
func promptArchivePasswordUntilProvided(ctx context.Context, displayName string) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		password, err := archivePasswordPrompt(ctx, displayName)
		if err != nil {
			return "", err
		}
		if password != "" {
			return password, nil
		}
	}
}

// openArchiveFSWithPasswordPrompt opens the archive and, when it needs a
// password, keeps asking until the archive opens or the user gives up. The
// whole ask/retry cycle is held as one interactive prompt: the retry with a
// wrong password fails within milliseconds and brings the dialog back, and
// without the hold the delayed "Opening..." progress screen could appear in
// that gap with the next password dialog on top of it (issue #816).
func openArchiveFSWithPasswordPrompt(ctx context.Context, localPath, displayName string, backing io.Closer) (zipperarchive.FileSystem, string, bool, error) {
	var password string
	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()
	for {
		fsys, cleanupTransferred, err := openArchiveFSWithContext(ctx, localPath, displayName, backing, password)
		if err == nil {
			return fsys, password, cleanupTransferred, nil
		}
		if cleanupTransferred || !isArchivePasswordRetryError(err) {
			return nil, "", cleanupTransferred, err
		}

		if release == nil {
			release = vfs.HoldInteractivePrompt()
		}
		password, err = promptArchivePasswordUntilProvided(ctx, displayName)
		if err != nil {
			return nil, "", cleanupTransferred, err
		}
	}
}

// openWithPassword is called after an archive operation failed because the
// archive (or a member) needs a password or rejected the current one. Every
// backend used by f4 opens archives lazily and only reports a wrong password
// while listing or reading, so the password that is currently installed has
// already been tried by the failed operation. It is therefore never reused
// silently: the user is asked for a new one, and the caller retries the
// operation with it. A retry that fails again simply lands here again, so
// the prompt keeps coming back until the password is right or the dialog is
// closed, which is exactly FAR's behaviour. Concurrent failures share one
// prompt: whoever arrives after a newer password was installed just retries.
func (v *ArchiveVFS) openWithPassword(ctx context.Context, cause error) error {
	if !isArchivePasswordRetryError(cause) {
		return cause
	}
	if ctx == nil {
		ctx = context.Background()
	}

	v.mu.Lock()
	localPath := v.activePath()
	displayName := v.displayName
	failedGen := v.passwordGen
	v.mu.Unlock()

	v.passwordPromptMu.Lock()
	defer v.passwordPromptMu.Unlock()

	v.mu.Lock()
	if v.isClosed {
		v.mu.Unlock()
		return errors.New("archive VFS is closed")
	}
	if v.passwordGen != failedGen {
		// Another operation has just installed a newer password. Let the
		// caller retry with it before bothering the user again.
		v.mu.Unlock()
		return nil
	}
	v.mu.Unlock()

	// Hold the prompt for the whole ask/verify cycle, including the
	// caller's retry that follows a lazily rejected password: the retry
	// fails in milliseconds and lands here again, and a progress screen
	// showing up in between would end up underneath the next dialog.
	release := vfs.HoldInteractivePrompt()
	defer v.releasePasswordPromptAfterRetry(release)

	for {
		password, err := promptArchivePasswordUntilProvided(ctx, displayName)
		if err != nil {
			return err
		}
		fsys, _, err := openArchiveFSWithContext(ctx, localPath, displayName, nil, password)
		if err != nil {
			if isArchivePasswordRetryError(err) {
				// Eager rejection (encrypted headers): ask again right away.
				continue
			}
			return err
		}
		return v.installPasswordFS(fsys, password)
	}
}

// releasePasswordPromptAfterRetry ends the interactive prompt hold taken
// by openWithPassword. A lazily rejected password is only discovered by
// the caller's retry, which then calls openWithPassword again; keeping the
// hold for a moment after returning covers that retry, so the delayed
// progress screen does not slip in between two password dialogs. The
// grace period is longer than the progress screen's retry interval and
// far longer than a rejected retry takes; a successful retry simply
// releases the hold a little later than strictly necessary.
func (v *ArchiveVFS) releasePasswordPromptAfterRetry(release func()) {
	time.AfterFunc(passwordRetryGrace, release)
}

// passwordRetryGrace is how long openWithPassword keeps the interactive
// prompt hold after returning, so that the caller's retry of a lazily
// rejected password reaches the next prompt before any progress screen
// appears.
const passwordRetryGrace = 250 * time.Millisecond

func (v *ArchiveVFS) installPasswordFS(fsys zipperarchive.FileSystem, password string) error {
	v.mu.Lock()
	if v.isClosed {
		v.mu.Unlock()
		_ = fsys.Close()
		return errors.New("archive VFS is closed")
	}
	v.cancelCleanupLocked()
	oldFS := v.fsys
	v.fsys = fsys
	v.password = password
	v.passwordGen++
	v.mu.Unlock()
	if oldFS != nil {
		_ = oldFS.Close()
	}
	return nil
}

func archivePasswordFormat(format archives.Format, password string) (archives.Format, bool) {
	if password == "" {
		return format, false
	}
	switch format := format.(type) {
	case archives.Rar:
		format.Password = password
		return format, true
	case *archives.Rar:
		format.Password = password
		return format, true
	case archives.SevenZip:
		format.Password = password
		return format, true
	case *archives.SevenZip:
		format.Password = password
		return format, true
	default:
		return format, false
	}
}
