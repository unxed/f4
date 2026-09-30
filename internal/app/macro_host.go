package app

import (
	"context"
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"path/filepath"
	"strings"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// uiDeadline bounds a round trip to the UI goroutine, so a macro cannot hang
// forever if the UI is busy or gone.
const uiDeadline = 5 * time.Second

// onUI runs fn on the UI goroutine and returns its result.
//
// Macros run on their own goroutine precisely so that this is safe: the UI
// goroutine is free to answer. Calling it from the UI goroutine would
// deadlock, which is why nothing in the macro path does.
func onUI[T any](fn func() T) T {
	var zero T
	if vtui.FrameManager == nil {
		return zero
	}
	result := make(chan T, 1)
	vtui.FrameManager.PostTask(func() {
		result <- fn()
	})
	select {
	case value := <-result:
		return value
	case <-time.After(uiDeadline):
		return zero
	}
}

// f4MacroHost is the real macro.MacroHost, bound to f4's panels and screen.
type f4MacroHost struct{}

func (f4MacroHost) CurrentArea() string {
	return onUI(func() string {
		if macro.MacroMgr == nil {
			return "Common"
		}
		return macroCurrentArea()
	})
}

func (f4MacroHost) Panel(active bool) macro.MacroPanelInfo {
	return onUI(func() (info macro.MacroPanelInfo) {
		// Panel contents are replaced wholesale by directory reads. Today
		// those land on this goroutine, but a future background reader would
		// not, and a macro is not worth a crash: report what is safe.
		defer func() {
			if recovered := recover(); recovered != nil {
				vtui.DebugLog("MACRO: panel state unavailable: %v", recovered)
			}
		}()

		frame := panel.FindPanelsFrame()
		if frame == nil {
			return macro.MacroPanelInfo{}
		}

		index := frame.ActiveIdx
		if !active {
			index = 1 - index
		}

		info = macro.MacroPanelInfo{
			Left:    index == 0,
			Visible: frame.ShowPanels,
		}

		pnl, ok := frame.Panels[index].(*panel.FileSystemPanel)
		if !ok {
			return info
		}

		info.Path = pnl.Vfs.GetPath()
		if pnl.IsLoading {
			// Mid-read the entry list means nothing: its length and its
			// contents belong to different directories.
			return info
		}

		// One read of the slice header, so length and indexing below cannot
		// disagree even if the field is reassigned underneath.
		entries := pnl.Entries

		info.ItemCount = len(entries)
		info.SelCount = len(pnl.GetSelectedNames())
		info.Current = pnl.GetSelectedName()

		cursor := pnl.GetCursorIndex()
		info.CurPos = cursor + 1
		if cursor >= 0 && cursor < len(entries) && entries[cursor] != nil {
			info.IsFolder = entries[cursor].IsDir
		}

		info.Empty = info.ItemCount == 0
		info.Bof = info.CurPos <= 1
		info.Eof = info.CurPos >= info.ItemCount
		info.Root = info.Path == "" || filepath.Dir(info.Path) == info.Path
		return info
	})
}

func (f4MacroHost) CommandLine() string {
	return onUI(func() string {
		frame := panel.FindPanelsFrame()
		if frame == nil || frame.CmdLine == nil {
			return ""
		}
		return frame.CmdLine.Edit.GetText()
	})
}

func (f4MacroHost) ScreenSize() (int, int) {
	type size struct{ width, height int }
	got := onUI(func() size {
		frame := panel.FindPanelsFrame()
		if frame == nil {
			return size{}
		}
		return size{frame.LastW, frame.LastH}
	})
	return got.width, got.height
}

func (f4MacroHost) Version() string {
	return getShortVersionInfo()
}

func (f4MacroHost) WindowTitle() string {
	return onUI(currentWindowTitle)
}

func (f4MacroHost) Message(title, text string) {
	if vtui.FrameManager == nil {
		return
	}
	vtui.FrameManager.PostTask(func() {
		vtui.ShowMessage(title, text, []string{"&Ok"})
	})
}

// EditorInfo is macro.MacroEditorHost: the editor on top of the screen.
func (f4MacroHost) EditorInfo() (macro.MacroEditorInfo, bool) {
	type answer struct {
		info macro.MacroEditorInfo
		ok   bool
	}
	got := onUI(func() answer {
		if vtui.FrameManager == nil {
			return answer{}
		}
		ev, ok := vtui.FrameManager.GetTopFrame().(*editor.EditorView)
		if !ok || ev == nil {
			return answer{}
		}
		line, total, column := ev.MacroPosition()
		return answer{macro.MacroEditorInfo{FileName: ev.FilePath, CurLine: line, CurPos: column, TotalLines: total, TabSize: config.App.EditorTabSize}, true}
	})
	return got.info, got.ok
}

// macroFilePanel is the file panel a macro means by "active"/"passive", or nil.
// It is called on the UI goroutine.
func macroFilePanel(active bool) (*panel.PanelsFrame, *panel.FileSystemPanel) {
	frame := panel.FindPanelsFrame()
	if frame == nil {
		return nil, nil
	}
	index := frame.ActiveIdx
	if !active {
		index = 1 - index
	}
	if index < 0 || index >= len(frame.Panels) {
		return frame, nil
	}
	pnl, _ := frame.Panels[index].(*panel.FileSystemPanel)
	return frame, pnl
}

// PanelEntry, SetPanelPath, SetPanelPos and SetPanelName are
// macro.MacroPanelHost.
func (f4MacroHost) PanelEntry(active bool, index int) (macro.MacroPanelEntry, bool) {
	type answer struct {
		row macro.MacroPanelEntry
		ok  bool
	}
	got := onUI(func() answer {
		_, pnl := macroFilePanel(active)
		if pnl == nil || pnl.IsLoading {
			return answer{}
		}
		entries := pnl.Entries
		if index < 1 || index > len(entries) || entries[index-1] == nil {
			return answer{}
		}
		e := entries[index-1]
		return answer{macro.MacroPanelEntry{Name: e.Name, IsDir: e.IsDir, Selected: e.Selected, Size: e.Size}, true}
	})
	return got.row, got.ok
}

func (f4MacroHost) SetPanelPath(active bool, path string) bool {
	return onUI(func() bool {
		frame, pnl := macroFilePanel(active)
		return frame != nil && pnl != nil && frame.NavigateToPath(pnl, path)
	})
}

func (f4MacroHost) SetPanelPos(active bool, index int) bool {
	return onUI(func() bool {
		_, pnl := macroFilePanel(active)
		if pnl == nil || index < 1 || index > len(pnl.Entries) {
			return false
		}
		pnl.SetCursorIndex(index - 1)
		pnl.Refresh()
		return true
	})
}

func (f4MacroHost) SetPanelName(active bool, name string) bool {
	return onUI(func() bool {
		_, pnl := macroFilePanel(active)
		if pnl == nil {
			return false
		}
		for _, e := range pnl.Entries {
			if e != nil && e.Name == name {
				pnl.SelectName(name)
				return true
			}
		}
		return false
	})
}

// SetClipboard and Clipboard are macro.MacroClipboardHost.
func (f4MacroHost) SetClipboard(text string) { terminal.SetF4Clipboard(text) }
func (f4MacroHost) Clipboard() string        { return vtui.GetClipboard() }

// ConfigValue is macro.MacroConfigHost: the few settings far.GetConfig reads.
func (f4MacroHost) ConfigValue(key string) (any, bool) {
	switch strings.ToLower(key) {
	case "editor.tabsize":
		return int64(config.App.EditorTabSize), true
	case "editor.expandtabs":
		return int64(config.App.EditorExpandTabs), true
	case "editor.autoindent":
		return config.App.EditorAutoIndent, true
	}
	return nil, false
}

// InputBox and Menu are macro.MacroDialogHost: they wait for the answer, with no
// deadline but the user's, because the macro is the one waiting.
func (f4MacroHost) InputBox(title, prompt, initial string) (string, bool) {
	if vtui.FrameManager == nil {
		return "", false
	}
	type answer struct {
		text string
		ok   bool
	}
	result := make(chan answer, 1)
	send := func(a answer) {
		select {
		case result <- a:
		default:
		}
	}
	vtui.FrameManager.PostTask(func() {
		dlg := vtui.InputBox(title, prompt, initial, func(text string) { send(answer{text, true}) })
		dlg.OnResult = func(int) { send(answer{}) } // after OK the answer is already sent
	})
	a := <-result
	return a.text, a.ok
}

func (f4MacroHost) Menu(title string, items []string) int {
	if vtui.FrameManager == nil {
		return -1
	}
	result := make(chan int, 1)
	vtui.FrameManager.PostTask(func() {
		pf := panel.FindPanelsFrameAnyScreen()
		if pf == nil {
			result <- -1
			return
		}
		pf.MenuCancelable(title, items, func(index int) { result <- index }, func() { result <- -1 })
	})
	return <-result
}

func (f4MacroHost) InjectKeys(keys []*vtinput.InputEvent) {
	if vtui.FrameManager == nil || len(keys) == 0 {
		return
	}
	vtui.FrameManager.PostTask(func() {
		vtui.FrameManager.InjectEvents(keys)
	})
}

func (f4MacroHost) Log(format string, args ...any) {
	vtui.DebugLog(format, args...)
}
func (f4MacroHost) RunAction(name string) bool {
	return onUI(func() bool {
		return RunAction(name)
	})
}

func (f4MacroHost) CallPlugin(ctx context.Context, id string, args []any) ([]any, error) {
	callContext := onUI(func() (snapshot vfs.MacroCallContext) {
		frame := panel.FindPanelsFrame()
		if frame == nil {
			return snapshot
		}
		pnl := frame.GetActivePanel()
		if pnl == nil || pnl.Vfs == nil {
			return snapshot
		}
		dir := pnl.Vfs.GetPath()
		name := pnl.GetSelectedName()
		path := ""
		if name != "" && name != ".." {
			path = pnl.Vfs.Join(dir, name)
		}
		snapshot.Current = vfs.FileRef{VFS: pnl.Vfs, Dir: dir, Name: name, Path: path}
		return snapshot
	})
	return macro.DispatchMacroPluginCall(ctx, id, callContext, args)
}

func actionReloadLuaMacros() bool {
	if macro.MacroMgr == nil {
		return false
	}
	dir := filepath.Join(config.GetF4ConfigDir(), "Macros", "scripts")
	count, err := macro.MacroMgr.ReloadLuaMacros(f4MacroHost{}, dir)
	if err != nil {
		vtui.DebugLog("MACRO: reload: %v", err)
		toast.Show(fmt.Sprintf("%s (%d loaded)", i18n.Msg("Macro.ReloadFailed"), count), 3*time.Second)
		return true
	}
	toast.Show(fmt.Sprintf(i18n.Msg("Macro.Reloaded"), count), 3*time.Second)
	return true
}
