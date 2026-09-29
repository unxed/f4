package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/update"
	"github.com/unxed/vtui"
)

// sessionDismissedUpdateKey remembers which update the user
// declined during the current f4 session, so an interval-driven
// auto-check does not re-prompt for the same version this run.
// The dismissal deliberately does NOT persist across restarts —
// see #374 — and a manual "Check for updates" always ignores it.
var sessionDismissedUpdateKey string

// updateSettings and applyUpdateSettings are how internal/update reaches the
// configuration: it takes the four fields as values, so the package does not
// depend on where they are stored.
func updateSettings() update.Settings {
	return update.Settings{
		Channel:     config.App.UpdateChannel,
		Interval:    config.App.UpdateInterval,
		LastCheck:   config.App.LastUpdateCheck,
		LastVersion: config.App.LastUpdateVersion,
	}
}

func applyUpdateSettings(s update.Settings) {
	config.App.UpdateChannel = s.Channel
	config.App.UpdateInterval = s.Interval
	config.App.LastUpdateCheck = s.LastCheck
	config.App.LastUpdateVersion = s.LastVersion
	config.SaveConfig()
}

// currentBuild describes this binary to the update check. A release tag is an
// exact version marker; a manually built binary exposes a commit hash instead
// and is dated by its build timestamp.
func currentBuild() update.Build {
	version := getCurrentVersion()
	_, _, buildTime := getVCSInfo()
	return update.Build{
		Version:   version,
		IsRelease: isReleaseVersion(version),
		TimeText:  buildTime,
	}
}

func getCurrentVersion() string {
	api := &coreAPI{}
	ver := api.GetVersion()
	parts := strings.Split(ver, "-")
	if len(parts) >= 3 {
		return strings.Join(parts[:len(parts)-1], "-")
	}
	return ver
}

func shouldCheck() bool {
	if config.App.UpdateInterval == 0 {
		return false
	}
	if config.App.LastUpdateCheck == 0 {
		return true
	}
	last := time.Unix(config.App.LastUpdateCheck, 0)
	now := time.Now()
	switch config.App.UpdateInterval {
	case 1:
		return true
	case 2:
		return now.Sub(last) >= 24*time.Hour
	case 3:
		return now.Sub(last) >= 7*24*time.Hour
	}
	return false
}

func CheckForUpdates(pf *panel.PanelsFrame, manual bool) {
	checkForUpdates(pf, manual, false)
}

// checkForUpdates is CheckForUpdates for a caller that knows the user has just
// picked a different channel: moving from nightly to stable then offers the
// stable release even when it is older than the running build (#1218).
func checkForUpdates(pf *panel.PanelsFrame, manual, switchedChannel bool) {
	if !manual && !shouldCheck() {
		return
	}

	config.App.LastUpdateCheck = time.Now().Unix()
	config.SaveConfig()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := updateSettings()
	cfg.SwitchedChannel = switchedChannel
	cand, err := update.Check(ctx, cfg, currentBuild())
	if err != nil {
		reportUpdateError(manual, err.Error())
		return
	}

	if !cand.NeedsUpdate {
		if manual {
			vtui.FrameManager.PostTask(func() {
				vtui.ShowMessage(" Auto Update ", "You are using the latest version.", []string{"&Ok"})
			})
		}
		return
	}

	// An update is available, but the user already said "no" to this
	// exact release earlier in this session. Skip the auto-prompt so
	// the next interval-driven check does not nag; a manual "Check
	// for updates" from the settings dialog goes through regardless
	// (see #374).
	if !manual && sessionDismissedUpdateKey == cand.UpdateKey {
		vtui.DebugLog("UPDATER: skipping prompt — update %q dismissed this session", cand.UpdateKey)
		return
	}

	vtui.FrameManager.PostTask(func() {
		msg := updatePromptText(cand)
		dlg := vtui.ShowMessage(" Auto Update ", msg, []string{"&Yes", "&No"})
		dlg.OnResult = func(code int) {
			if code == 0 {
				performUpdate(pf, cand)
				return
			}
			// User declined. Remember only for this session — the
			// next restart (or a manual check) will offer it again.
			// config.App.LastUpdateVersion is deliberately NOT touched
			// here: that field is the "we already installed this
			// version" marker and must survive across restarts, while
			// a declined prompt must not (see #374).
			sessionDismissedUpdateKey = cand.UpdateKey
		}
	})
}

// updatePromptText words the offer. A stable release offered after a move
// from nightly can be older than the running build, and the user should know
// that before agreeing to it.
func updatePromptText(cand update.Candidate) string {
	if cand.OlderThanRunning {
		return fmt.Sprintf("The stable channel offers %s.\nIt is older than the build you are running.\n\nDo you want to download and install it now?", cand.DisplayVersion)
	}
	return fmt.Sprintf("An update is available: %s\n\nDo you want to download and install it now?", cand.DisplayVersion)
}

func reportUpdateError(manual bool, msg string) {
	vtui.DebugLog("UPDATER ERROR: %s", msg)
	if manual {
		vtui.FrameManager.PostTask(func() {
			vtui.ShowMessage(" Update Error ", msg, []string{"&Ok"})
		})
	}
}

func restartCommand(executable string, args []string, workingDir string) *exec.Cmd {
	cmd := update.SelfCommand(executable, args...)
	cmd.Dir = workingDir
	return cmd
}

func startUpdatedF4() error {
	executable, err := update.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate the updated f4 executable: %w", err)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot preserve the working directory: %w", err)
	}
	cmd := restartCommand(executable, os.Args[1:], workingDir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot restart the updated f4: %w", err)
	}
	return nil
}

func performUpdate(pf *panel.PanelsFrame, cand update.Candidate) {
	if pf == nil {
		return
	}
	previousLastVersion := config.App.LastUpdateVersion
	backupPath := ""
	pf.RunProgressTask(" Updating f4 ", "Downloading...", false, func(ctx context.Context, updateProgress func(msg string, percent int)) error {
		if _, err := update.TargetDir(); err != nil {
			return err
		}

		data, err := update.Download(ctx, cand.DownloadURL, func(percent int) {
			updateProgress("Downloading update...", percent)
		})
		if err != nil {
			return err
		}

		updateProgress("Extracting and installing...", -1)

		backupPath, err = update.BackupExecutable()
		if err != nil {
			return fmt.Errorf("failed to back up executable: %w\n(Close other f4 instances, check Task Manager for ghost f4 processes, or try running as admin/root)", err)
		}

		if err := update.Install(data, cand.ArchiveKind); err != nil {
			return fmt.Errorf("failed to extract/install update: %w\n(Close other f4 instances, check Task Manager for ghost f4 processes, or try running as admin/root)", err)
		}

		// A build that does not start here is not kept: the error path below
		// puts this one back, which can still fetch the fix.
		updateProgress("Checking the new build...", -1)
		if err := update.CheckInstalled(); err != nil {
			return fmt.Errorf("%w\nThe previous build has been put back.", err)
		}

		return nil
	}, func(err error) {
		if err != nil {
			if backupPath != "" {
				if restoreErr := update.RestoreExecutable(backupPath); restoreErr != nil {
					err = fmt.Errorf("%v; rollback failed: %w", err, restoreErr)
				}
				backupPath = ""
			}
			if err != context.Canceled {
				vtui.ShowMessage(" Update Failed ", err.Error(), []string{"&Ok"})
			}
			return
		}

		config.App.LastUpdateVersion = cand.UpdateKey
		config.SaveConfig()

		dlg := vtui.ShowMessage(" Update Successful ", "f4 has been updated successfully.\nRestart the application now to apply changes?", []string{"&Restart now", "&Later"})
		dlg.OnResult = func(code int) {
			if code == 0 {
				if err := startUpdatedF4(); err != nil {
					if restoreErr := update.RestoreExecutable(backupPath); restoreErr != nil {
						err = fmt.Errorf("%v; rollback failed: %w", err, restoreErr)
					}
					config.App.LastUpdateVersion = previousLastVersion
					config.SaveConfig()
					backupPath = ""
					vtui.ShowMessage(" Update Failed ", err.Error(), []string{"&Ok"})
					return
				}
				if err := update.RemoveExecutableBackup(backupPath); err != nil {
					vtui.DebugLog("UPDATER: %v", err)
				}
				backupPath = ""
				panel.CancelOperationsForShutdown()
				vtui.FrameManager.Shutdown()
			} else if err := update.RemoveExecutableBackup(backupPath); err != nil {
				vtui.DebugLog("UPDATER: %v", err)
			}
		}
	})
}
