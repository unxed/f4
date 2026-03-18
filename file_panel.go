package main

import (
	"fmt"
	"sort"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// fileEntry implements vtui.TableRow for display in a table.
type fileEntry struct {
	vtui.VFSItem
}

func (f *fileEntry) GetCellText(col int) string {
	switch col {
	case 0:
		if f.IsDir {
			return "/" + f.Name
		}
		return f.Name
	case 1:
		if f.IsDir {
			return Msg("Panel.UpDir")
		}
		return fmt.Sprintf("%d", f.Size)
	}
	return ""
}

// FileSystemPanel is a panel displaying files on disk.
type FileSystemPanel struct {
	vtui.ScreenObject
	table   *vtui.Table
	frame   *vtui.BorderedFrame
	vfs     vtui.VFS
	entries []*fileEntry
}

func NewFileSystemPanel(x, y, w, h int, vfs vtui.VFS) *FileSystemPanel {
	path := vfs.GetPath()
	// Initial column widths (will be adjusted by Resize)
	cols := []vtui.TableColumn{
		{Title: Msg("Panel.Column.Name"), Width: w - 15 - 2},
		{Title: Msg("Panel.Column.Size"), Width: 12, Alignment: vtui.AlignRight},
	}

	fp := &FileSystemPanel{
		vfs:   vfs,
		frame: vtui.NewBorderedFrame(x, y, x+w-1, y+h-1, vtui.SingleBox, path),
		table: vtui.NewTable(x+1, y+1, w-2, h-2, cols),
	}
	fp.frame.ColorBoxIdx = ColPanelBox
	fp.frame.ColorTitleIdx = ColPanelTitle
	fp.table.ColorTextIdx = ColPanelText
	fp.table.ColorSelectedTextIdx = ColPanelCursor
	fp.table.ColorTitleIdx = ColPanelColumnTitle
	fp.table.ColorBoxIdx = ColPanelBox
	fp.SetCanFocus(true)
	fp.Refresh()
	return fp
}

func (fp *FileSystemPanel) Refresh() {
	path := fp.vfs.GetPath()
	fp.frame.SetTitle(path)
	items, err := fp.vfs.ReadDir(path)
	if err != nil {
		return
	}

	fp.entries = make([]*fileEntry, 0, len(items)+1)

	// Add ".." to go up
	fp.entries = append(fp.entries, &fileEntry{VFSItem: vtui.VFSItem{Name: "..", IsDir: true}})

	for _, item := range items {
		fp.entries = append(fp.entries, &fileEntry{VFSItem: item})
	}

	// Sort: directories first, then files
	sort.Slice(fp.entries, func(i, j int) bool {
		if fp.entries[i].IsDir != fp.entries[j].IsDir {
			return fp.entries[i].IsDir
		}
		return fp.entries[i].Name < fp.entries[j].Name
	})

	rows := make([]vtui.TableRow, len(fp.entries))
	for i, e := range fp.entries {
		rows[i] = e
	}
	fp.table.SetRows(rows)
}

func (fp *FileSystemPanel) Show(scr *vtui.ScreenBuf) {
	fp.frame.Show(scr)
	fp.table.SetFocus(fp.IsFocused())
	fp.table.Show(scr)
}

func (fp *FileSystemPanel) SetPosition(x1, y1, x2, y2 int) {
	fp.ScreenObject.SetPosition(x1, y1, x2, y2)
	fp.frame.SetPosition(x1, y1, x2, y2)
	// Table stays inside the frame
	fp.table.SetPosition(x1+1, y1+1, x2-1, y2-1)
}

func (fp *FileSystemPanel) Resize(w, h int) {
	fp.SetPosition(fp.X1, fp.Y1, fp.X1+w-1, fp.Y1+h-1)
	// Adapt columns: "Name" takes all available space minus borders and size column
	nameW := w - 15 - 2
	if nameW < 5 {
		nameW = 5
	}
	fp.table.Columns[0].Width = nameW
}

func (fp *FileSystemPanel) ProcessKey(e *vtinput.InputEvent) bool {
	if !e.KeyDown { return false }

	// Handle directory navigation
	if e.VirtualKeyCode == vtinput.VK_RETURN {
		if len(fp.entries) == 0 || fp.table.SelectPos < 0 || fp.table.SelectPos >= len(fp.entries) {
			return false
		}
		selected := fp.entries[fp.table.SelectPos]
		if selected.IsDir {
			oldPath := fp.vfs.GetPath()
			newPath := fp.vfs.Join(oldPath, selected.Name)

			if err := fp.vfs.SetPath(newPath); err != nil {
				return false
			}
			fp.Refresh()

			if selected.Name == ".." {
				// We went up. Find the directory we came from.
				dirToSelect := fp.vfs.Base(oldPath)
				for i, entry := range fp.entries {
					if entry.Name == dirToSelect {
						fp.table.SelectPos = i
						fp.table.EnsureVisible()
						return true
					}
				}
			}

			fp.table.SelectPos = 0
			fp.table.EnsureVisible()
			return true
		}
	}

	return fp.table.ProcessKey(e)
}

func (fp *FileSystemPanel) ProcessMouse(e *vtinput.InputEvent) bool {
	return fp.table.ProcessMouse(e)
}

func (fp *FileSystemPanel) GetSelectedName() string {
	if len(fp.entries) == 0 || fp.table.SelectPos < 0 || fp.table.SelectPos >= len(fp.entries) {
		return ""
	}
	entry := fp.entries[fp.table.SelectPos]
	if entry.Name == ".." {
		return fp.vfs.Dir(fp.vfs.GetPath())
	}
	return entry.Name
}
