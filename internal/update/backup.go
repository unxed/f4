package update

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// BackupExecutable saves the currently installed executable in a temporary
// file so protected installation directories do not prevent an update.
func BackupExecutable() (string, error) {
	executable, err := Executable()
	if err != nil {
		return "", fmt.Errorf("failed to locate executable for backup: %w", err)
	}
	info, err := os.Stat(executable)
	if err != nil {
		return "", fmt.Errorf("failed to stat executable for backup: %w", err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		return "", fmt.Errorf("failed to read executable for backup: %w", err)
	}
	tmp, err := os.CreateTemp("", "f4-update-backup-*")
	if err != nil {
		return "", fmt.Errorf("failed to create executable backup: %w", err)
	}
	backup := tmp.Name()
	removeBackup := true
	defer func() {
		if removeBackup {
			_ = os.Remove(backup)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("failed to write executable backup: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("failed to close executable backup: %w", err)
	}
	if err := os.Chmod(backup, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("failed to preserve executable backup permissions: %w", err)
	}
	removeBackup = false
	return backup, nil
}

// RestoreExecutable replaces the installed executable with a backup and then
// removes the backup. The caller uses this while the old process is still
// alive, before handing control to the restored version.
func RestoreExecutable(backup string) error {
	if err := restoreExecutable(backup); err != nil {
		if !isPermissionError(err) {
			return err
		}
		if elevatedErr := restoreExecutableElevated(backup); elevatedErr != nil {
			return fmt.Errorf("failed to restore executable directly: %v; elevated restore failed: %w", err, elevatedErr)
		}
	}
	return nil
}

// restoreExecutable puts the backup back in place of the executable -- the
// file itself, not a symlink to it, which is what the update replaced.
func restoreExecutable(backup string) error {
	executable, err := targetExecutable()
	if err != nil {
		return fmt.Errorf("failed to locate executable for restore: %w", err)
	}
	if backup == "" {
		return fmt.Errorf("executable backup path is empty")
	}
	// An install that failed before reaching the executable left it as it
	// was. Removing it would still fail on Windows while it runs, and ask for
	// elevation to do nothing.
	if sameContent(executable, backup) {
		return RemoveExecutableBackup(backup)
	}
	if err := os.Remove(executable); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove failed executable: %w", err)
	}
	if err := renameFile(backup, executable); err != nil {
		// The backup lives in the temp directory, often another filesystem
		// than the installation (a tmpfs /tmp), and rename cannot cross
		// that. Before this fallback the executable was already gone by
		// then, and a failed update left no f4 at all.
		if copyErr := copyExecutable(backup, executable); copyErr != nil {
			return fmt.Errorf("failed to restore executable backup: %v; copying it failed too: %w", err, copyErr)
		}
		_ = os.Remove(backup)
	}
	return nil
}

// renameFile is os.Rename; a variable so a test can make it fail the way a
// rename across filesystems does.
var renameFile = os.Rename

func sameContent(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	if errA != nil || errB != nil || ai.Size() != bi.Size() {
		return false
	}
	da, errA := os.ReadFile(a)
	db, errB := os.ReadFile(b)
	return errA == nil && errB == nil && bytes.Equal(da, db)
}

func copyExecutable(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// RunRestoreHelper is used by the elevated Windows helper. It deliberately
// calls the direct implementation so a failed permission check cannot recurse
// into another elevation request.
var RunRestoreHelper = restoreExecutable

// RemoveExecutableBackup discards a backup after the new executable has
// started successfully or when the user chooses to postpone the restart.
func RemoveExecutableBackup(backup string) error {
	if backup == "" {
		return nil
	}
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove executable backup: %w", err)
	}
	return nil
}
