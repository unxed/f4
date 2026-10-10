package app

import (
	"context"
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/internal/vtvibe"
	"github.com/unxed/f4/internal/vtvibe/ap"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// Applying an ap patch that the model wrote (github.com/unxed/ap).
//
// f4#1606 ported the reference patcher (implementation/ap.py) to Go
// (internal/vtvibe/ap): applying a patch is now an in-process call, not a
// shell-out to a downloaded Python script. No interpreter, no network
// fetch of the patcher itself, no cached copy in the config directory -
// aiRunPatcher below is what changed; everything about the confirmation
// dialog and the result screen (aiApplyPatch, aiShowPatchResult) already
// worked the same way regardless of what actually applies the patch, so
// neither needed to change. A dry run that reached individual
// modifications now ends on the review table instead (aiShowPatchReview,
// vtvibe_ap_review.go).

const (
	vtvibeAPSpecURL = "https://raw.githubusercontent.com/unxed/ap/main/ap.md"
	// vtvibeAPMaxDownload caps the specification download: it is a single
	// Markdown file, nowhere near a megabyte.
	vtvibeAPMaxDownload = 4 << 20
)

// aiPatchTargetDir picks the folder the patch applies to: the other panel,
// because the AI panel itself holds the dialog, not the project. Paths inside
// an ap patch are relative to that folder.
func aiPatchTargetDir(pf *panel.PanelsFrame) (string, bool) {
	if pf == nil {
		return "", false
	}
	for _, p := range pf.Panels {
		fsp, ok := p.(*panel.FileSystemPanel)
		if !ok || fsp == nil || fsp.Vfs == nil {
			continue
		}
		if _, isAI := fsp.Vfs.(*aiVFSWrapper); isAI {
			continue
		}
		if _, isOS := fsp.Vfs.(*vfs.OSVFS); isOS {
			return fsp.Vfs.GetPath(), true
		}
	}
	return "", false
}

// aiApplyPatch is the whole feature from the human side: confirm what will be
// touched, then run the patcher.
func aiApplyPatch(pf *panel.PanelsFrame) {
	if pf == nil {
		return
	}
	patch := aiSession().LastPatch()
	if patch == nil {
		vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.NoPatch"), []string{i18n.Msg("vtui.Ok")})
		return
	}
	root, ok := aiPatchTargetDir(pf)
	if !ok {
		vtui.ShowMessage(i18n.Msg("AI.ErrorTitle"), i18n.Msg("AI.PatchNoLocalDir"), []string{i18n.Msg("vtui.Ok")})
		return
	}

	body := fmt.Sprintf(i18n.Msg("AI.PatchConfirm"), root, len(patch.Files)) + aiPathList(patch.Files)
	if patch.Ignored > 0 {
		body += "\n\n" + fmt.Sprintf(i18n.Msg("AI.PatchIgnored"), patch.Ignored)
	}

	if aiInGitWorkTree(root) {
		// Only a suggestion (docs/VTVIBE.md §7.4): somebody else's repository
		// is not ours to stash.
		body += "\n\n" + i18n.Msg("AI.PatchGitHint")
	}

	dlg := vtui.ShowMessage(i18n.Msg("AI.PatchTitle"), body,
		[]string{i18n.Msg("AI.BtnApplyPatch"), i18n.Msg("AI.BtnDryRun"), i18n.Msg("vtui.Cancel")})
	dlg.OnResult = func(code int) {
		switch code {
		case 0:
			aiRunPatcher(pf, patch, root, false, nil)
		case 1:
			aiRunPatcher(pf, patch, root, true, nil)
		}
	}
}

// aiInGitWorkTree reports whether dir is inside a git working tree: a .git
// (a directory, or a file for a worktree or submodule) in dir or above it.
func aiInGitWorkTree(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// aiRunPatcher writes the patch to a temporary file and applies it with the
// native Go patcher (internal/vtvibe/ap). The patch file itself never
// touches the target folder: projectDir is what decides where the changes
// land, same as --dir did for the old ap.py subprocess. only is passed
// through as ap.Options.Only: nil runs the whole patch, anything else only
// the modifications the review screen left checked.
func aiRunPatcher(pf *panel.PanelsFrame, patch *vtvibe.Patch, root string, dry bool, only map[ap.ModKey]bool) {
	var output string
	var mods []ap.ModificationResult
	var undo *ap.Undo
	exitCode := 0

	title := i18n.Msg("AI.PatchTitle")
	pf.RunProgressTask(title, i18n.Msg("AI.PatchRunning"), false,
		func(ctx context.Context, update func(msg string, percent int)) error {
			if err := ctx.Err(); err != nil {
				return err
			}

			dir, err := os.MkdirTemp("", "vtvibe-ap-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(dir)

			patchPath := filepath.Join(dir, "afix.ap")
			if err := os.WriteFile(patchPath, []byte(patch.Text), 0600); err != nil {
				return err
			}

			update(i18n.Msg("AI.PatchRunning"), -1)
			var out strings.Builder
			result := ap.Apply(patchPath, root, ap.Options{DryRun: dry, Only: only, Out: &out})
			output = out.String()
			mods = result.ModificationResults
			undo = result.Undo
			exitCode = aiPatchExitCode(result.Status)
			return nil
		},
		func(err error) {
			if err != nil {
				if err != context.Canceled {
					aiShowError(err)
				}
				return
			}
			pf.RefreshAll()
			// A real run that wrote something is remembered with the
			// dialog: when it no longer fits the model's context, the
			// patch's code gives way to the commit it went into (f4#1842,
			// § 19a.3).
			if !dry && (exitCode == 0 || exitCode == 2) {
				aiSession().NoteApplied(vtvibe.AppliedPatch{ID: patch.ID, Root: root, Files: patch.Files, At: time.Now()})
			}
			// A real run that wrote something can be undone (Ctrl+Z in
			// the AI panel, ai:undo, or Undo on the result right below).
			aiPushUndo(undo)
			// The journal is also kept on disk, so the patch can still be
			// undone after f4 is restarted (aiUndoPatch loads it back).
			if err := ap.SaveUndo(undo, aiUndoDepth); err != nil {
				vtui.DebugLog("VTVIBE: undo snapshot not saved: %v", err)
			}
			// A dry run that got as far as individual modifications
			// ends on the review table (vtvibe_ap_review.go); a real
			// run, or a dry run that failed before any modification,
			// keeps the plain result message.
			if dry && len(mods) > 0 {
				aiShowPatchReview(pf, patch, root, mods, exitCode, output)
				return
			}
			aiShowPatchResult(pf, root, dry, exitCode, output, undo)
		})
}

// aiPatchExitCode mirrors the exit codes the old ap.py subprocess used to
// return, since aiShowPatchResult's status text already keys off them:
// 0 applied in full, 2 applied in part (tolerant mode), anything else
// nothing was written.
func aiPatchExitCode(status ap.Status) int {
	switch status {
	case ap.StatusSuccess:
		return 0
	case ap.StatusPartial:
		return 2
	default:
		return 1
	}
}

// aiShowPatchResult reports what the patcher said. exitCode comes from
// aiPatchExitCode: 0 applied, 2 applied in part, anything else nothing was
// written. undo, when non-nil, is the run's transaction (ap.Result.Undo) and
// adds an Undo button that reverts it without asking again - the result is
// the moment the human sees what the patch did.
func aiShowPatchResult(pf *panel.PanelsFrame, root string, dry bool, exitCode int, output string, undo *ap.Undo) {
	var head string
	switch {
	case exitCode == 0 && dry:
		head = i18n.Msg("AI.PatchDryOk")
	case exitCode == 0:
		head = i18n.Msg("AI.PatchOk")
	case exitCode == 2:
		head = i18n.Msg("AI.PatchPartial")
	default:
		head = i18n.Msg("AI.PatchFailed")
	}

	output = strings.TrimSpace(output)
	preview := output
	if len(preview) > 500 {
		preview = preview[:500] + "\n..."
	}

	body := head
	if preview != "" {
		body += "\n\n" + preview
	}

	buttons := []string{i18n.Msg("vtui.Ok")}
	hasOutput := output != ""
	if hasOutput {
		buttons = append(buttons, i18n.Msg("AI.BtnViewLog"))
	}

	reportPath := filepath.Join(root, "afailed.md")
	hasReport := false
	if exitCode != 0 {
		if st, err := os.Stat(reportPath); err == nil && !st.IsDir() {
			buttons = append(buttons, i18n.Msg("AI.BtnAttachReport"))
			hasReport = true
		}
	}

	hasUndo := undo != nil
	if hasUndo {
		buttons = append(buttons, i18n.Msg("AI.BtnUndoPatch"))
	}

	dlg := vtui.ShowMessage(i18n.Msg("AI.PatchTitle"), body, buttons)
	dlg.OnResult = func(code int) {
		// -1 is BaseFrame.Close's exit code for "dismissed without picking a
		// button" (Escape, or a caller force-closing the dialog), not a real
		// button index. viewLogIdx/attachReportIdx must never match it when
		// their button is not on the dialog at all: comparing code against a
		// bare "not present" sentinel of -1 would make a dismissal collide
		// with whichever of them stayed unset and fire that action anyway.
		// Gate each case on its own hasX flag so an unset index can never
		// match.
		viewLogIdx := -1
		attachReportIdx := -1
		undoIdx := -1

		currIdx := 1
		if hasOutput {
			viewLogIdx = currIdx
			currIdx++
		}
		if hasReport {
			attachReportIdx = currIdx
			currIdx++
		}
		if hasUndo {
			undoIdx = currIdx
		}

		switch {
		case hasOutput && code == viewLogIdx:
			aiViewPatchLog(pf, output)
		case hasReport && code == attachReportIdx:
			aiAttachFailureReport(reportPath)
		case hasUndo && code == undoIdx:
			aiRevertPatch(pf, undo)
		}
	}
}

// aiAttachFailureReport puts afailed.md into the dialog context. The
// patcher writes that file precisely so a model can be told what went
// wrong without the human retyping it.
func aiAttachFailureReport(reportPath string) {
	data, err := os.ReadFile(reportPath)
	if err != nil {
		aiShowError(err)
		return
	}
	if err := aiWriteContextFile("afailed.md", data); err != nil {
		aiShowError(err)
		return
	}
	toast.Show(i18n.Msg("AI.PatchReportAttached"), 3*time.Second)
	if pf := panel.FindPanelsFrameAnyScreen(); pf != nil {
		pf.RefreshAll()
	}
}

// aiWriteContextFile drops a file into ai://ctx through the normal VFS, so it
// obeys the same size limits as a file copied there with F5.
func aiWriteContextFile(name string, data []byte) error {
	return vtvibeWriteContextFile(aiSession(), name, data)
}

func vtvibeWriteContextFile(session *vtvibe.Session, name string, data []byte) error {
	v := vtvibe.NewVFS(session)
	w, err := v.Create(context.Background(), "/ctx/"+name)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// aiAttachAPSpec teaches the model the format: the specification goes into the
// context and the dialog switches to asking for patches.
func aiAttachAPSpec(pf *panel.PanelsFrame) {
	if pf == nil {
		return
	}
	url := vtvibeAPSpecURL
	if ini := ini.Load(vtvibeIniPath()); ini != nil {
		url = ini.GetString("general", "ap_spec_url", vtvibeAPSpecURL)
	}
	var spec []byte
	pf.RunProgressTask(i18n.Msg("AI.PatchTitle"), i18n.Msg("AI.SpecDownloading"), false,
		func(ctx context.Context, update func(msg string, percent int)) error {
			data, err := aiDownload(ctx, url)
			if err != nil {
				return err
			}
			spec = data
			return nil
		},
		func(err error) {
			if err != nil {
				if err != context.Canceled {
					aiShowError(err)
				}
				return
			}
			if err := aiWriteContextFile("ap.md", spec); err != nil {
				aiShowError(err)
				return
			}
			aiSession().SetPatchMode(true)
			pf.RefreshAll()
			vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.SpecAttached"), []string{i18n.Msg("vtui.Ok")})
		})
}

// aiDownload is one small HTTP GET with the context of the progress task, so
// Cancel really cancels it.
func aiDownload(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "f4-vtvibe")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, vtvibeAPMaxDownload))
}
