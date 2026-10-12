package app

import (
	"github.com/unxed/f4/internal/findfile"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/vfs"
)

// FindFileOptions keeps the action's existing options surface.
type FindFileOptions = findfile.Options

func findFileHost(pf *panel.PanelsFrame) findfile.Host {
	return findfile.Host{
		View: func(v vfs.VFS, path string) { actionOpenViewer(pf, v, path) },
		Edit: func(v vfs.VFS, path string) { actionOpenEditor(pf, v, path) },
		GoTo: func(v vfs.VFS, path string) {
			if pf == nil {
				return
			}
			if fsp := pf.GetActivePanel(); fsp != nil {
				_ = fsp.Vfs.SetPath(v.Dir(path))
				fsp.PendingSelection = v.Base(path)
				fsp.ReadDirectory()
				pf.ShowPanels = true
			}
		},
		Panel: func(v vfs.VFS, hits []vfs.FoundEntry) {
			if pf == nil {
				return
			}
			fsp := pf.GetActivePanel()
			if fsp == nil {
				return
			}
			found := make([]panel.FoundFile, len(hits))
			for i, hit := range hits {
				found[i] = panel.FoundFile{Path: hit.Path, Item: hit.Item}
			}
			slot := panel.GlobalTempPanelStore.SearchSlot()
			panel.GlobalTempPanelStore.ReplaceWithSearchResults(slot, v, found)
			pf.SwitchToVFS(fsp, panel.NewTempPanelVFS(nil, panel.GlobalTempPanelStore, slot))
		},
	}
}

func ExecuteFindFile(pf *panel.PanelsFrame, v vfs.VFS, root, mask, text string, options FindFileOptions) {
	findfile.Start(v, root, mask, text, options, findFileHost(pf))
}

func ShowSearchResults(pf *panel.PanelsFrame, v vfs.VFS, found []panel.FoundFile) {
	hits := make([]vfs.FoundEntry, len(found))
	for i, hit := range found {
		hits[i] = vfs.FoundEntry{Path: hit.Path, Item: hit.Item}
	}
	findfile.Show(v, hits, findFileHost(pf))
}
