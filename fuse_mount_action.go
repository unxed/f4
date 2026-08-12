package main

import (
	"context"
	"fmt"

	"github.com/unxed/f4/fusefs"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// The panel-side entry point for FUSE mounts (FUSE.md, iteration 2).
//
// This is the first, deliberately small step of that iteration: one command
// that mounts what the active panel is showing. The mounts dialog (Unmount /
// Unmount all / Go to) is a separate step.
func init() {
	RegisterAction(Action{
		Name:        "Panel.Mount",
		Area:        "Shell",
		Label:       "Mount via FUSE",
		Description: "Mount what the active panel shows as an ordinary directory",
		DefaultKeys: []string{"CtrlAltM"},
		MenuPath:    "Commands",
		// Hidden where nothing can be mounted, rather than offered and refused.
		Visible: fusefs.Supported,
		Handler: func() bool {
			pf := findPanelsFrameAnyScreen()
			if pf == nil {
				return false
			}
			mountActivePanel(pf)
			return true
		},
	})
}

// mountActivePanel mounts the location the active panel is showing.
//
// There are two ways to get a VFS the mount can own, and the ownership rule
// in FUSE.md — a mount never borrows the instance a panel is browsing —
// decides which one applies:
//
//   - A local folder has a path that doubles as a source string, so the mount
//     re-opens it as a fresh OSVFS and owns that outright.
//
//   - A NetFox connection has no such string: it is opened from a stored
//     configuration, and re-opening it would mean a second login and a second
//     password prompt. Its Clone() instead returns a handle of its own onto
//     the same refcounted session, which satisfies the same requirement: the
//     mount closes its own handle when it ends, and the session survives for
//     as long as either the panel or the mount still holds one.
//
// A VFS whose Clone() returns the receiver — ArchiveVFS does, because cloning
// would mean extracting everything twice — is refused rather than mounted:
// the mount would be sharing one object with the panel, and the panel closes
// that object when the user walks out of it.
func mountActivePanel(pf *PanelsFrame) {
	fsp := pf.getActivePanel()
	if fsp == nil || fsp.vfs == nil {
		return
	}
	label, run := mountPlan(fsp)
	if run == nil {
		return
	}
	// Opening a location can take as long as it likes: an archive is
	// extracted before it can be listed, a remote host may have to answer.
	// Only the plan above touches the panel's own VFS, and it runs here on
	// the UI thread; everything the task does afterwards belongs to the
	// mount alone.
	var m *fusefs.Mount
	pf.RunProgressTask(" Mount ", "Mounting "+label+"...", false,
		func(ctx context.Context, _ func(msg string, percent int)) error {
			mounted, err := run(ctx)
			if err != nil {
				return err
			}
			m = mounted
			return nil
		},
		func(err error) { reportMount(label, m, err) })
}

// mountPlan decides what the active panel would have mounted, and returns a
// label for it plus the work that produces the mount. It returns a nil run
// when there is nothing to mount, having said why.
func mountPlan(fsp *FileSystemPanel) (string, func(context.Context) (*fusefs.Mount, error)) {
	if _, isOS := fsp.vfs.(*vfs.OSVFS); isOS {
		dir := fsp.vfs.GetPath()
		if dir == "" {
			return "", nil
		}
		// An archive under the cursor is a location of its own, and the
		// file is exactly the source a fresh VFS is opened from. It is
		// opened here rather than through MountSource because an archive
		// needs a parent to read the file through — a brand new OSVFS,
		// never the one the panel is holding.
		if entry := currentPanelEntryPath(fsp); entry != "" && entry != dir {
			parent := vfs.NewOSVFS(dir)
			if prov := vfs.FindProvider(context.Background(), parent, entry); prov != nil {
				return entry, func(ctx context.Context) (*fusefs.Mount, error) {
					v, err := prov.Open(ctx, parent, entry)
					if err != nil {
						return nil, err
					}
					return fusefs.MountVFS(ctx, v, fusefs.Options{
						MountPoint: fusefs.SuggestMountPoint(entry),
						Source:     entry,
						ReadOnly:   true,
					})
				}
			}
		}
		return dir, func(ctx context.Context) (*fusefs.Mount, error) {
			return fusefs.MountSource(dir, fusefs.Options{
				MountPoint: fusefs.SuggestMountPoint(dir),
				ReadOnly:   true,
			})
		}
	}

	clone := fsp.vfs.Clone()
	if clone == nil || clone == fsp.vfs {
		vtui.ShowMessage(" Mount ", "This file system cannot be mounted yet:\n"+
			"it cannot hand out a handle of its own.", []string{"&Ok"})
		return "", nil
	}
	root := clone.GetPath()
	label := root
	if titled, ok := fsp.vfs.(vfs.PanelTitleProvider); ok {
		if title := titled.PanelTitle(root); title != "" {
			label = title
		}
	}
	return label, func(ctx context.Context) (*fusefs.Mount, error) {
		return fusefs.MountVFS(ctx, clone, fusefs.Options{
			MountPoint: fusefs.SuggestMountPoint(label),
			RootPath:   root,
			Source:     label,
			ReadOnly:   true,
		})
	}
}

func reportMount(source string, m *fusefs.Mount, err error) {
	if err != nil {
		vtui.ShowMessage(" Mount ", fmt.Sprintf("Cannot mount %s:\n%v", source, err), []string{"&Ok"})
		return
	}
	vtui.ShowMessage(" Mount ", fmt.Sprintf("%s\nis mounted at\n%s", source, m.MountPoint), []string{"&Ok"})
}
