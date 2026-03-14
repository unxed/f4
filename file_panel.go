package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// fileEntry реализует vtui.TableRow для отображения в таблице.
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
			return "UP-DIR"
		}
		return fmt.Sprintf("%d", f.size)
	}
	return ""
}

// FileSystemPanel — панель, отображающая файлы на диске.
type FileSystemPanel struct {
	vtui.ScreenObject
	table  *vtui.Table
	path   string
	entries []*fileEntry
}

func NewFileSystemPanel(x, y, w, h int, path string) *FileSystemPanel {
	absPath, _ := filepath.Abs(path)
	cols := []vtui.TableColumn{
		{Title: "Name", Width: w - 15},
		{Title: "Size", Width: 12, Alignment: vtui.AlignRight},
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

	// Добавляем ".." для выхода наверх
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

	// Сортировка: сначала папки, потом файлы
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
	// Ресайзим саму таблицу
	fp.table.SetPosition(fp.X1, fp.Y1, fp.X1+w-1, fp.Y1+h-1)
	// Адаптируем колонки: "Name" забирает всё свободное место
	fp.table.Columns[0].Width = w - 15
}

func (fp *FileSystemPanel) ProcessKey(e *vtinput.InputEvent) bool {
	if !e.KeyDown { return false }

	// Обработка перехода по директориям
	if e.VirtualKeyCode == vtinput.VK_RETURN {
		selected := fp.entries[fp.table.SelectPos]
		if selected.isDir {
			newPath := filepath.Join(fp.path, selected.name)
			fp.path = filepath.Clean(newPath)
			fp.Refresh()
			fp.table.SelectPos = 0
			return true
		}
	}

	return fp.table.ProcessKey(e)
}

func (fp *FileSystemPanel) ProcessMouse(e *vtinput.InputEvent) bool {
	return fp.table.ProcessMouse(e)
}