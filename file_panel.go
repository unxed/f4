package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// fileEntry implements vtui.TableRow for display in a table.
type fileEntry struct {
	name  string
	size  int64
	isDir bool
	date  string
}

func (f *fileEntry) GetCellText(col int) string {
	switch col {
	case 0:
		if f.isDir {
			return "/" + f.name
		}
		return f.name
	case 1:
		if f.isDir {
			return Msg("Panel.UpDir")
		}
		return fmt.Sprintf("%d", f.size)
	}
	return ""
}

// FileSystemPanel is a panel displaying files on disk.
type FileSystemPanel struct {
	vtui.ScreenObject
	table  *vtui.Table
	path   string
	entries []*fileEntry
}

func NewFileSystemPanel(x, y, w, h int, path string) *FileSystemPanel {
	absPath, _ := filepath.Abs(path)
	cols := []vtui.TableColumn{
		{Title: Msg("Panel.Column.Name"), Width: w - 15},
		{Title: Msg("Panel.Column.Size"), Width: 12, Alignment: vtui.AlignRight},
	}

	fp := &FileSystemPanel{
		path:  absPath,
		table: vtui.NewTable(x, y, w, h, cols),
	}
	fp.SetCanFocus(true)
	fp.Refresh()
	return fp
}

func (fp *FileSystemPanel) Refresh() {
	files, err := os.ReadDir(fp.path)
	if err != nil {
		return
	}

	fp.entries = make([]*fileEntry, 0, len(files)+1)

	// Add ".." to go up
	fp.entries = append(fp.entries, &fileEntry{name: "..", isDir: true})

	for _, f := range files {
		info, _ := f.Info()
		fp.entries = append(fp.entries, &fileEntry{
			name:  f.Name(),
			size:  info.Size(),
			isDir: f.IsDir(),
			date:  info.ModTime().Format("2006-01-02"),
		})
	}

	// Sort: directories first, then files
	sort.Slice(fp.entries, func(i, j int) bool {
		if fp.entries[i].isDir != fp.entries[j].isDir {
			return fp.entries[i].isDir
		}
		return fp.entries[i].name < fp.entries[j].name
	})

	rows := make([]vtui.TableRow, len(fp.entries))
	for i, e := range fp.entries {
		rows[i] = e
	}
	fp.table.SetRows(rows)
}

func (fp *FileSystemPanel) Show(scr *vtui.ScreenBuf) {
	fp.table.SetFocus(fp.IsFocused())
	fp.table.Show(scr)
}

func (fp *FileSystemPanel) SetPosition(x1, y1, x2, y2 int) {
	fp.ScreenObject.SetPosition(x1, y1, x2, y2)
	fp.table.SetPosition(x1, y1, x2, y2)
}

func (fp *FileSystemPanel) Resize(w, h int) {
	// Resize the table itself
	fp.table.SetPosition(fp.X1, fp.Y1, fp.X1+w-1, fp.Y1+h-1)
	// Adapt columns: "Name" takes all available space
	nameW := w - 15
	if nameW < 5 { nameW = 5 } // Minimum safety width
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
		if selected.isDir {
			oldPath := fp.path
			newPath := filepath.Join(fp.path, selected.name)
			fp.path = filepath.Clean(newPath)
			fp.Refresh()

			if selected.name == ".." {
				// We went up. Find the directory we came from.
				dirToSelect := filepath.Base(oldPath)
				for i, entry := range fp.entries {
					if entry.name == dirToSelect {
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
	if entry.name == ".." {
		return filepath.Dir(fp.path)
	}
	return entry.name
}
