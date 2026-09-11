package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/unxed/f4/internal/panel"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/appcmd"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// RunAction executes an action by name if it exists.
func RunAction(name string) bool {
	if a, ok := action.Lookup(name); ok && a.Handler != nil {
		// Fast Find is a transient panel input mode. Any action means the user
		// is leaving it, including actions that replace a file panel in place
		// (Info/Quick View) and therefore do not push a focus-stealing frame.
		if !strings.EqualFold(name, commandPaletteActionName) {
			if pf := panel.FindPanelsFrame(); pf != nil && pf.CancelFastFind() && vtui.FrameManager != nil {
				vtui.FrameManager.Redraw()
			}
		}
		return a.Handler()
	}
	if a, ok := panel.PluginActionForName(name); ok && a.Handler != nil {
		return a.Handler()
	}
	return false
}

// GetAction returns an action by name.
func GetAction(name string) (action.Action, bool) {
	a, ok := action.Lookup(name)
	if !ok {
		return panel.PluginActionForName(name)
	}
	return a, ok
}

// cursorOnParent reports whether the panel's cursor sits on the ".."
// (parent-directory) entry — used by the far2l Ins clipboard shortcuts
// that treat this position as the current folder itself.
func cursorOnParent(fsp *panel.FileSystemPanel) bool {
	if fsp == nil {
		return false
	}
	idx := fsp.GetCursorIndex()
	return idx >= 0 && idx < len(fsp.Entries) && fsp.Entries[idx].Name == ".."
}

// isAIPanelActive returns true if the currently active panel is an AI panel.
// Used to dynamically show/hide specific menu items.
func isAIPanelActive() bool {
	pf := panel.FindPanelsFrameAnyScreen()
	if pf == nil {
		return false
	}
	fsp := pf.GetActivePanel()
	if fsp == nil {
		return false
	}
	if tp, ok := fsp.Vfs.(vfs.TitleProvider); ok {
		return tp.GetTitle() == "ai"
	}
	return false
}

func repeatEditorSearchDirection(ev *editor.EditorView, reverse bool) {
	if editor.LastEditorSearch == "" {
		return
	}
	rememberedDirection := editor.LastEditorSearchReverse
	ev.Search(editor.LastEditorSearch, editor.LastEditorSearchCase, reverse, editor.LastEditorSearchRegexp, editor.LastEditorSearchWholeWord, true)
	editor.LastEditorSearchReverse = rememberedDirection
}

func init() {
	withPF := func(fn func(pf *panel.PanelsFrame)) func() bool {
		return func() bool {
			if pf := panel.FindPanelsFrameAnyScreen(); pf != nil {
				fn(pf)
				return true
			}
			return false
		}
	}

	// withMultiEditor is for the handful of actions that know about the
	// multi-caret set and act on it themselves.
	withMultiEditor := func(fn func(ev *editor.EditorView)) func() bool {
		return func() bool {
			if vtui.FrameManager == nil {
				return false
			}
			if ev, ok := vtui.FrameManager.GetTopFrame().(*editor.EditorView); ok {
				fn(ev)
				return true
			}
			return false
		}
	}

	// Actions reach the editor without passing through its key handling, so
	// the caret set is put down here. An action that knows nothing about it
	// would work through the primary caret alone and leave the others
	// painted over text they no longer describe.
	withEditor := func(fn func(ev *editor.EditorView)) func() bool {
		return withMultiEditor(func(ev *editor.EditorView) {
			ev.ClearExtraCursors()
			fn(ev)
		})
	}

	withViewer := func(fn func(vv *viewer.ViewerView)) func() bool {
		return func() bool {
			if vtui.FrameManager == nil {
				return false
			}
			if vv, ok := vtui.FrameManager.GetTopFrame().(*viewer.ViewerView); ok {
				fn(vv)
				return true
			}
			return false
		}
	}

	editorState := func(fn func(ev *editor.EditorView) bool) func() bool {
		return func() bool {
			if vtui.FrameManager == nil {
				return false
			}
			if ev, ok := vtui.FrameManager.GetTopFrame().(*editor.EditorView); ok {
				return fn(ev)
			}
			return false
		}
	}

	viewerState := func(fn func(vv *viewer.ViewerView) bool) func() bool {
		return func() bool {
			if vtui.FrameManager == nil {
				return false
			}
			if vv, ok := vtui.FrameManager.GetTopFrame().(*viewer.ViewerView); ok {
				return fn(vv)
			}
			return false
		}
	}

	// --- Common actions (available in every area) ---
	registerAction(action.Action{
		Name:        "App.ScreenGrab",
		Area:        "Common",
		Label:       "Screen Grab",
		LabelKey:    "Action.App.ScreenGrab",
		Description: "Select and copy a screen region",
		DescKey:     "Action.App.ScreenGrab.Desc",
		DefaultKeys: []string{"AltIns"},
		MenuPath:    "File",
		Handler:     actionScreenGrab,
	})
	registerAction(action.Action{
		Name:                "App.CopyWindowTitle",
		Area:                "Common",
		Label:               "Copy Window Identity",
		LabelKey:            "Action.App.CopyWindowTitle",
		Description:         "Copy the active f4 frame help identity to the clipboard",
		DescKey:             "Action.App.CopyWindowTitle.Desc",
		DefaultKeys:         []string{"CtrlAltShiftT"},
		MenuPath:            "Commands",
		MenuSeparatorBefore: true,
		Handler:             actionCopyWindowTitle,
	})
	registerAction(action.Action{
		Name:        "Macro.Reload",
		Area:        "Common",
		Label:       "Reload Lua Macros",
		LabelKey:    "Action.Macro.Reload",
		Description: "Reload Lua macros from the scripts directory",
		DescKey:     "Action.Macro.Reload.Desc",
		DefaultKeys: []string{"CtrlAltShiftM"},
		MenuPath:    "Commands",
		Handler:     actionReloadLuaMacros,
	})
	registerAction(action.Action{
		Name:        commandPaletteActionName,
		Area:        "Common",
		Label:       "Command Palette",
		LabelKey:    "Action.App.CommandPalette",
		Description: "Search and run available commands",
		DescKey:     "Action.App.CommandPalette.Desc",
		DefaultKeys: []string{"CtrlShiftP"},
		// Legacy terminals cannot distinguish Ctrl+Shift+letter from Ctrl+letter.
		// MacroManager.Filter handles this additional escape hatch without
		// claiming Ctrl+Alt+P as a configurable default.
		NativeKeys:          []string{"CtrlAltP"},
		MenuPath:            "Commands",
		MenuSeparatorBefore: true,
		MenuLast:            true,
		Handler:             ShowCommandPalette,
	})
	registerAction(action.Action{
		Name:        "App.Help",
		Area:        "Common",
		Label:       "Context Help",
		LabelKey:    "Action.App.Help",
		SearchKeys:  []string{"KeyBar.F1"},
		Description: "Open help for the current screen or focused control",
		DescKey:     "Action.App.Help.Desc",
		NativeKeys:  []string{"F1:FrameworkNoTerminalApp"},
		Visible:     contextHelpActionAvailable,
		Handler:     actionContextHelp,
	})
	registerAction(action.Action{
		Name:        "App.MainMenu",
		Area:        "Common",
		Label:       "Main Menu",
		LabelKey:    "Action.App.MainMenu",
		SearchKeys:  []string{"UserMenu.MainMenuTitle"},
		Description: "Open the main menu for the current screen",
		DescKey:     "Action.App.MainMenu.Desc",
		NativeKeys:  []string{"F9:FrameworkNoTerminalApp"},
		Visible:     mainMenuActionAvailable,
		Handler:     actionActivateMainMenu,
	})
	registerAction(action.Action{
		Name:        "App.LastMenuItem",
		Area:        "Common",
		Label:       "Last Menu Item",
		LabelKey:    "Action.App.LastMenuItem",
		Description: "Open the main menu at the last executed item",
		DescKey:     "Action.App.LastMenuItem.Desc",
		DefaultKeys: []string{"ShiftF10"},
		Handler:     actionSelectLastMenuItem,
	})
	registerAction(action.Action{
		Name:        "Workspace.New",
		Area:        "Common",
		Label:       "New Workspace",
		LabelKey:    "Action.Workspace.New",
		SearchKeys:  []string{"AppearanceSettings.WorkspaceTabs", "PanelSettings.TerminalCtrlNWorkspace"},
		Description: "Clone the current panels into a new workspace",
		DescKey:     "Action.Workspace.New.Desc",
		NativeKeys:  []string{"CtrlN:TerminalCtrlNWorkspace"},
		Handler:     actionWorkspaceNew,
	})
	registerAction(action.Action{
		Name:        "Workspace.NewTerminal",
		Area:        "Common",
		Label:       "Terminal in New Workspace",
		LabelKey:    "Action.Workspace.NewTerminal",
		SearchKeys:  []string{"AppearanceSettings.WorkspaceTabs"},
		Description: "Open the terminal in a workspace of its own",
		DescKey:     "Action.Workspace.NewTerminal.Desc",
		DefaultKeys: []string{"CtrlShiftO"},
		Handler:     panel.ActionWorkspaceNewTerminal,
	})
	registerAction(action.Action{
		Name:        "Workspace.Close",
		Area:        "Common",
		Label:       "Close Workspace",
		LabelKey:    "Action.Workspace.Close",
		SearchKeys:  []string{"AppearanceSettings.WorkspaceTabs", "AppearanceSettings.RestoreWorkspaceTabs"},
		Description: "Close the active workspace",
		DescKey:     "Action.Workspace.Close.Desc",
		NativeKeys:  []string{"CtrlW:FrameworkNoTerminalApp"},
		Handler:     actionWorkspaceClose,
	})
	registerAction(action.Action{
		Name:        "Workspace.Next",
		Area:        "Common",
		Label:       "Next Workspace",
		LabelKey:    "Action.Workspace.Next",
		SearchKeys:  []string{"AppearanceSettings.WorkspaceTabs"},
		Description: "Switch to the next workspace",
		DescKey:     "Action.Workspace.Next.Desc",
		NativeKeys:  []string{"CtrlTab"},
		Visible:     multipleWorkspacesAvailable,
		Handler:     actionWorkspaceNext,
	})
	registerAction(action.Action{
		Name:        "Workspace.Previous",
		Area:        "Common",
		Label:       "Previous Workspace",
		LabelKey:    "Action.Workspace.Previous",
		SearchKeys:  []string{"AppearanceSettings.WorkspaceTabs"},
		Description: "Switch to the previous workspace",
		DescKey:     "Action.Workspace.Previous.Desc",
		NativeKeys:  []string{"CtrlShiftTab"},
		Visible:     multipleWorkspacesAvailable,
		Handler:     actionWorkspacePrevious,
	})
	registerAction(action.Action{
		Name:        "Workspace.List",
		Area:        "Common",
		Label:       "Workspace List",
		LabelKey:    "Action.Workspace.List",
		SearchKeys:  []string{"AppearanceSettings.WorkspaceTabs"},
		Description: "Show all workspaces",
		DescKey:     "Action.Workspace.List.Desc",
		NativeKeys:  []string{"F12:FrameworkNoTerminalApp"},
		Handler:     actionWorkspaceList,
	})
	registerAction(action.Action{
		Name:        "Debug.ScreenDump",
		Area:        "Common",
		Label:       "Dump Screen",
		LabelKey:    "Action.Debug.ScreenDump",
		Description: "Write the current screen buffer to vtui.screen.log",
		DescKey:     "Action.Debug.ScreenDump.Desc",
		// No hotkey. The dump is reached from the command palette, which is
		// how it was registered before CtrlAltP was tried here.
		//
		// CtrlAltP was taken to reach the dump on a Wine tty, where
		// Ctrl+Shift+<letter> collapses to plain Ctrl+<letter> and only an
		// ESC-prefixed chord should have survived (WINE.md §15.1). It did not
		// survive either — Wine's tty backend does not deliver
		// Ctrl+Alt+<letter> at all, measured on live Wine (WINE.md §15.2) —
		// and the -e flag replaced the need for a keyboard route entirely.
		//
		// What the binding did do was claim Common/CtrlAltP in
		// HotkeyManager.Defaults, and that is the chord App.CommandPalette
		// advertises as its legacy fallback. commandPaletteLegacyShortcut
		// stands down for any bound action and NativeShortcutsForAction hides
		// a native key another action holds, so the fallback neither fired
		// nor was shown anywhere (issue #980).
		Handler: actionScreenDump,
	})

	// --- Shell (panels) actions ---
	// Registration order defines the menu order inside each top-level menu.
	registerAction(action.Action{
		Name:        "File.View",
		Area:        "Shell",
		Label:       "View",
		LabelKey:    "Menu.Files.View",
		Description: "Open file in viewer",
		DescKey:     "Action.File.View.Desc",
		DefaultKeys: []string{"F3"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionViewFile(pf) }),
	})
	registerAction(action.Action{
		Name:        "File.Edit",
		Area:        "Shell",
		Label:       "Edit",
		LabelKey:    "Menu.Files.Edit",
		Description: "Open file in editor",
		DescKey:     "Action.File.Edit.Desc",
		DefaultKeys: []string{"F4"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionEditFile(pf) }),
	})
	registerAction(action.Action{
		Name:         "File.New",
		Area:         "Shell",
		Label:        "New File",
		LabelKey:     "Action.File.New",
		Description:  "Create and open a new file in editor",
		DescKey:      "Action.File.New.Desc",
		DefaultKeys:  []string{"ShiftF4:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		MenuPath:     "Files",
		Handler:      withPF(func(pf *panel.PanelsFrame) { actionNewFile(pf) }),
	})
	registerAction(action.Action{
		Name:        "File.ApplyCommand",
		Area:        "Shell",
		Label:       "Apply command",
		LabelKey:    "Action.File.ApplyCommand",
		Description: "Apply a command template to selected files or the current file",
		DescKey:     "Action.File.ApplyCommand.Desc",
		DefaultKeys: []string{"CtrlG"},
		MenuPath:    "Files",
		Visible:     panel.PanelCanApplyCommand,
		Handler: func() bool {
			if pf := panel.FindPanelsFrame(); pf != nil {
				panel.ActionApplyCommand(pf)
				return true
			}
			return false
		},
	})
	registerAction(action.Action{
		Name:                "File.Copy",
		Area:                "Shell",
		Label:               "Copy",
		LabelKey:            "Menu.Files.Copy",
		Description:         "Copy selected files or current file",
		DescKey:             "Action.File.Copy.Desc",
		DefaultKeys:         []string{"F5"},
		MenuPath:            "Files",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { actionCopyMove(pf, false) }),
	})
	registerAction(action.Action{
		Name:        "File.CopyInPlace",
		Area:        "Shell",
		Label:       "Copy In Place",
		LabelKey:    "Action.File.CopyInPlace",
		Description: "Copy file under a new name in the same directory",
		DescKey:     "Action.File.CopyInPlace.Desc",
		DefaultKeys: []string{"ShiftF5"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionCopyInPlace(pf) }),
	})
	registerAction(action.Action{
		Name:        "File.Move",
		Area:        "Shell",
		Label:       "Move",
		LabelKey:    "Menu.Files.RenMov",
		Description: "Rename or move selected files",
		DescKey:     "Action.File.Move.Desc",
		DefaultKeys: []string{"F6"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionCopyMove(pf, true) }),
	})
	registerAction(action.Action{
		Name:        "File.CreateLink",
		Area:        "Shell",
		Label:       "Create Link",
		LabelKey:    "Action.File.CreateLink",
		Description: "Create symbolic link, directory junction or hard link",
		DescKey:     "Action.File.CreateLink.Desc",
		DefaultKeys: []string{"AltF6"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionCreateLink(pf) }),
	})
	registerAction(action.Action{
		Name:        "File.EditSymlink",
		Area:        "Shell",
		Label:       "Edit Symlink",
		LabelKey:    "Action.File.EditSymlink",
		Description: "Edit the target of the selected symbolic link",
		DescKey:     "Action.File.EditSymlink.Desc",
		MenuPath:    "Files",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionEditSymlink(pf) }),
	})
	registerAction(action.Action{
		Name:        "File.Rename",
		Area:        "Shell",
		Label:       "Rename",
		LabelKey:    "Action.File.Rename",
		Description: "Rename current file",
		DescKey:     "Action.File.Rename.Desc",
		DefaultKeys: []string{"ShiftF6"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionRename(pf) }),
	})
	registerAction(action.Action{
		Name:         "File.MakeDir",
		Area:         "Shell",
		Label:        "Make Folder",
		LabelKey:     "Menu.Files.MkDir",
		Description:  "Create a new directory",
		DescKey:      "Action.File.MakeDir.Desc",
		DefaultKeys:  []string{"F7:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		MenuPath:     "Files",
		Handler:      withPF(func(pf *panel.PanelsFrame) { actionMkDir(pf) }),
	})
	registerAction(action.Action{
		Name:        "File.Delete",
		Area:        "Shell",
		Label:       "Delete",
		LabelKey:    "Menu.Files.Delete",
		Description: "Delete selected files",
		DescKey:     "Action.File.Delete.Desc",
		DefaultKeys: []string{"F8"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionDelete(pf) }),
	})
	registerAction(action.Action{
		Name:        "File.DeletePermanent",
		Area:        "Shell",
		Label:       "Delete permanently",
		LabelKey:    "Action.File.DeletePermanent",
		Description: "Permanently delete selected files without using trash",
		DescKey:     "Action.File.DeletePermanent.Desc",
		DefaultKeys: []string{"ShiftDel", "ShiftNumDel"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionDeletePermanent(pf) }),
	})
	registerAction(action.Action{
		Name:                "File.Attributes",
		Area:                "Shell",
		Label:               "File Attributes",
		LabelKey:            "Action.File.Attributes",
		Description:         "View and change file attributes",
		DescKey:             "Action.File.Attributes.Desc",
		DefaultKeys:         []string{"CtrlA"},
		MenuPath:            "Files",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { actionFileAttributes(pf) }),
	})
	registerAction(action.Action{
		Name:        "File.Share",
		Area:        "Shell",
		Label:       "Share...",
		LabelKey:    "Action.File.Share",
		Description: "Create, copy, or revoke a cloud share link",
		DescKey:     "Action.File.Share.Desc",
		MenuPath:    "Files",
		Visible: func() bool {
			pf := panel.FindPanelsFrame()
			if pf == nil {
				return false
			}
			pnl := pf.GetActivePanel()
			if pnl == nil || pnl.Vfs == nil {
				return false
			}
			_, ok := pnl.Vfs.(vfs.ShareLinkProvider)
			return ok
		},
		Handler: withPF(func(pf *panel.PanelsFrame) { actionShareLink(pf) }),
	})
	registerAction(action.Action{
		Name:        "Panel.SystemExplorer",
		Area:        "Shell",
		Label:       "Open in Explorer",
		LabelKey:    "Action.Panel.SystemExplorer",
		Description: "Open current file in the system file manager",
		DescKey:     "Action.Panel.SystemExplorer.Desc",
		// Same reasoning as Panel.InsertFileName: the panel cursor
		// survives Ctrl+O, so Shift+Enter keeps working with the panels
		// hidden, but yields to a child process that is using the
		// terminal (many REPLs read Shift+Enter themselves).
		DefaultKeys:  []string{"ShiftEnter:NoTerminalApp"},
		DefaultAreas: []string{"Terminal"},
		MenuPath:     "Files",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			fsp := pf.GetActivePanel()
			if fsp == nil {
				return
			}
			idx := fsp.GetCursorIndex()
			if idx < 0 || idx >= len(fsp.Entries) {
				return
			}
			name := fsp.Entries[idx].Name
			var fullPath string
			if name == ".." {
				fullPath = fsp.Vfs.GetPath()
			} else {
				fullPath = fsp.Vfs.Join(fsp.Vfs.GetPath(), name)
			}
			if _, isLocal := fsp.Vfs.(*vfs.OSVFS); isLocal {
				// Capture entry state before spawning: the entries
				// slice may be replaced by a refresh at any time.
				isDir := fsp.Entries[idx].IsDir || name == ".."
				go func() {
					command, args, ok := panel.SystemFileManagerCommand(fullPath, isDir)
					if ok {
						_ = pf.RunExternalUICommand(command, args, "")
					}
				}()
			} else {
				vtui.ShowMessage(" Error ", "Cannot open remote paths in system explorer.", []string{"&Ok"})
			}
		}),
	})

	registerAction(action.Action{
		Name:                "Panel.SelectGroup",
		Area:                "Shell",
		Label:               "Select Group",
		LabelKey:            "Action.Panel.SelectGroup",
		Description:         "Select files by mask",
		DescKey:             "Action.Panel.SelectGroup.Desc",
		DefaultKeys:         []string{"Add"},
		MenuPath:            "Files",
		MenuSeparatorBefore: true,
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				var maskEdit *vtui.Edit
				dlg := vtui.InputBox(i18n.Msg("Select.Title"), i18n.Msg("Select.Mask"), "*", func(mask string) {
					history.CommitHistory(maskEdit, mask)
					fsp.ApplyMaskSelection(mask, true)
				})
				// Plain DIF_HISTORY, as in far2l: the dialog opens on "*"
				// rather than on whatever was selected last time.
				maskEdit = history.AttachHistory(history.InputBoxEdit(dlg), history.FileMasksHistoryID)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.DeselectGroup",
		Area:        "Shell",
		Label:       "Deselect Group",
		LabelKey:    "Action.Panel.DeselectGroup",
		Description: "Deselect files by mask",
		DescKey:     "Action.Panel.DeselectGroup.Desc",
		DefaultKeys: []string{"Subtract"},
		MenuPath:    "Files",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				var maskEdit *vtui.Edit
				dlg := vtui.InputBox(i18n.Msg("Deselect.Title"), i18n.Msg("Select.Mask"), "*", func(mask string) {
					history.CommitHistory(maskEdit, mask)
					fsp.ApplyMaskSelection(mask, false)
				})
				maskEdit = history.AttachHistory(history.InputBoxEdit(dlg), history.FileMasksHistoryID)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.InvertSelection",
		Area:        "Shell",
		Label:       "Invert Selection",
		LabelKey:    "Action.Panel.InvertSelection",
		Description: "Invert file selection",
		DescKey:     "Action.Panel.InvertSelection.Desc",
		DefaultKeys: []string{"Multiply"},
		MenuPath:    "Files",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				fsp.InvertSelection()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.RestoreSelection",
		Area:        "Shell",
		Label:       "Restore Selection",
		LabelKey:    "Action.Panel.RestoreSelection",
		Description: "Restore the previous selection state",
		DescKey:     "Action.Panel.RestoreSelection.Desc",
		DefaultKeys: []string{"CtrlM"},
		MenuPath:    "Files",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				fsp.RestoreSelection()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.SelectNavigation",
		Area:        "Shell",
		Label:       "Select While Navigating",
		Description: "Select or deselect files with Insert and Shift-navigation",
		NativeKeys: []string{
			"Ins", "ShiftUp", "ShiftDown", "ShiftLeft", "ShiftRight",
			"ShiftPgUp", "ShiftPgDn", "ShiftHome", "ShiftEnd",
		},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				fsp.ProcessKey(keymap.ParseFarKey("Ins"))
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.ToggleCommandLineFocus",
		Area:        "Shell",
		Label:       "Toggle Command Line Focus",
		Description: "Toggle command-line focus in Search by default navigation mode",
		NativeKeys:  []string{"VK_C0:SearchFirst", "`:SearchFirst", "ё:SearchFirst"},
		Handler: func() bool {
			pf := panel.FindPanelsFrameAnyScreen()
			if pf == nil || !pf.SearchFirstMode() || !pf.ShowPanels {
				return false
			}
			pf.SetCommandLineFocus(!pf.CommandLineFocused)
			return true
		},
	})

	registerAction(action.Action{
		Name:         "Panel.UserMenu",
		Area:         "Shell",
		Label:        "User Menu",
		LabelKey:     "Action.Panel.UserMenu",
		Description:  "Show the user menu",
		DescKey:      "Action.Panel.UserMenu.Desc",
		DefaultKeys:  []string{"F2:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		MenuPath:     "Commands",
		Handler:      withPF(func(pf *panel.PanelsFrame) { panel.ShowUserMenu(pf) }),
	})
	registerAction(action.Action{
		Name:        "Panel.FileAssociations",
		Area:        "Shell",
		Label:       "File Associations…",
		LabelKey:    "Action.Panel.FileAssociations",
		Description: "Configure per-mask commands for Enter, F3 and F4",
		DescKey:     "Action.Panel.FileAssociations.Desc",
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *panel.PanelsFrame) { panel.ShowFileAssociations(pf) }),
	})
	registerAction(action.Action{
		Name:                "File.Find",
		Area:                "Shell",
		Label:               "Find File",
		LabelKey:            "Menu.Commands.FindFile",
		Description:         "Search for files",
		DescKey:             "Action.File.Find.Desc",
		DefaultKeys:         []string{"AltF7"},
		MenuPath:            "Commands",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { actionFindFile(pf) }),
	})
	registerAction(action.Action{
		Name:        "File.FindDuplicates",
		Area:        "Shell",
		Label:       "Find Duplicates",
		LabelKey:    "Menu.Commands.FindDuplicates",
		Description: "Find files with identical content",
		DescKey:     "Action.File.FindDuplicates.Desc",
		MenuPath:    "Commands",
		Visible:     panelCanFindDuplicates,
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionFindDuplicates(pf) }),
	})
	registerAction(action.Action{
		Name:        "Panel.CompareFolders",
		Area:        "Shell",
		Label:       "Compare Folders",
		LabelKey:    "Menu.Commands.CompareFolders",
		Description: "Compare the two panels and mark what differs",
		DescKey:     "Action.Panel.CompareFolders.Desc",
		MenuPath:    "Commands",
		Visible:     panelCanCompareFolders,
		Handler:     withPF(func(pf *panel.PanelsFrame) { ShowCompareFoldersDialog(pf) }),
	})
	registerAction(action.Action{
		Name:        "File.RunRemoteCommand",
		Area:        "Shell",
		Label:       "Run Command Remotely",
		LabelKey:    "Menu.Commands.RunRemote",
		Description: "Run a command on the host the panel is showing",
		DescKey:     "Action.File.RunRemoteCommand.Desc",
		MenuPath:    "Commands",
		Visible:     panel.PanelCanRunCommand,
		Handler:     withPF(func(pf *panel.PanelsFrame) { panel.ActionRunRemoteCommand(pf) }),
	})
	registerAction(action.Action{
		Name:        "Panel.BackgroundJobs",
		Area:        "Shell",
		Label:       "Background Jobs",
		LabelKey:    "Menu.Commands.BackgroundJobs",
		Description: "Show work still running and results waiting to be seen",
		DescKey:     "Action.Panel.BackgroundJobs.Desc",
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *panel.PanelsFrame) { ShowBackgroundJobs(pf) }),
	})
	registerAction(action.Action{
		Name:                "Panel.Bookmarks",
		Area:                "Shell",
		Label:               "Bookmarks",
		LabelKey:            "Menu.Commands.Bookmarks",
		Description:         "Show folder bookmarks dialog",
		DescKey:             "Action.Panel.Bookmarks.Desc",
		DefaultKeys:         []string{"CtrlShiftVK_DC"},
		MenuPath:            "Commands",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { panel.ShowBookmarksDialog(pf) }),
	})
	registerAction(action.Action{
		Name:        "Panel.PluginMenu",
		Area:        "Shell",
		Label:       "Plugin Commands",
		LabelKey:    "Action.Panel.PluginMenu",
		Description: "Show plugin commands menu",
		DescKey:     "Action.Panel.PluginMenu.Desc",
		DefaultKeys: []string{"F11"},
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *panel.PanelsFrame) { pf.ShowPluginMenu() }),
	})
	registerAction(action.Action{
		Name:        "Panel.TempPanel",
		Area:        "Shell",
		Label:       "Temporary panel",
		LabelKey:    "Menu.Commands.TempPanel",
		Description: "Open the temporary panel or switch its saved list",
		DescKey:     "Action.Panel.TempPanel.Desc",
		DefaultKeys: []string{"AltShiftF12"},
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *panel.PanelsFrame) { panel.ActionOpenTempPanel(pf) }),
	})
	registerAction(action.Action{
		Name:                "Panel.CommandHistory",
		Area:                "Shell",
		Label:               "Command History",
		LabelKey:            "Action.Panel.CommandHistory",
		Description:         "Show command line history",
		DescKey:             "Action.Panel.CommandHistory.Desc",
		DefaultKeys:         []string{"AltF8"},
		MenuPath:            "Commands",
		MenuSubPath:         "History",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { actionCommandHistory(pf) }),
	})
	registerAction(action.Action{
		Name:        "Panel.FoldersHistory",
		Area:        "Shell",
		Label:       "Folders History",
		LabelKey:    "Action.Panel.FoldersHistory",
		Description: "Show folders history",
		DescKey:     "Action.Panel.FoldersHistory.Desc",
		DefaultKeys: []string{"AltF12"},
		MenuPath:    "Commands",
		MenuSubPath: "History",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionFoldersHistory(pf) }),
	})
	registerAction(action.Action{
		Name:        "Panel.ViewerEditorHistory",
		Area:        "Shell",
		Label:       "Viewer and Editor History",
		LabelKey:    "Action.Panel.ViewerEditorHistory",
		Description: "Show viewer and editor history",
		DescKey:     "Action.Panel.ViewerEditorHistory.Desc",
		DefaultKeys: []string{"AltF11"},
		MenuPath:    "Commands",
		MenuSubPath: "History",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionViewerEditorHistory(pf) }),
	})
	registerAction(action.Action{
		Name:        "History.ImportFar2l",
		Area:        "Shell",
		Label:       "Import far2l History",
		Description: "Import command history from far2l (.hst)",
		MenuPath:    "Commands",
		MenuSubPath: "History",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionImportFar2lHistory(pf) }),
	})
	registerAction(action.Action{
		Name:        "Panel.GoParent",
		Area:        "Shell",
		Label:       "Parent Folder",
		LabelKey:    "Action.Panel.GoParent",
		Description: "Go to parent directory",
		DescKey:     "Action.Panel.GoParent.Desc",
		DefaultKeys: []string{"CtrlPgUp"},
		MenuPath:    "Commands",
		MenuSubPath: "Navigation",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			fsp := pf.GetActivePanel()
			if fsp == nil {
				return
			}
			if fsp.Vfs.IsAtRoot() {
				if fsp.Vfs.ParentVFS() == nil {
					pf.ShowDriveMenu(pf.ActiveIdx)
				} else {
					// Exit an archive or a NetFox connection to the parent VFS
					pf.NavigateToPath(fsp, "..")
				}
			} else {
				oldPath := fsp.Vfs.GetPath()
				parentPath := fsp.Vfs.Dir(oldPath)
				if err := fsp.SetKnownDirectoryPath(parentPath); err == nil {
					fsp.PendingSelection = fsp.Vfs.Base(oldPath)
					fsp.ReadDirectory()
				} else {
					pf.NavigateToPath(fsp, "..")
				}
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.GoRoot",
		Area:        "Shell",
		Label:       "Root Folder",
		LabelKey:    "Action.Panel.GoRoot",
		Description: "Go to the filesystem root",
		DescKey:     "Action.Panel.GoRoot.Desc",
		DefaultKeys: []string{"CtrlVK_DC"},
		MenuPath:    "Commands",
		MenuSubPath: "Navigation",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				rootPath := "/"
				if runtime.GOOS == "windows" {
					rootPath = string(os.PathSeparator)
					if _, isOS := fsp.Vfs.(*vfs.OSVFS); isOS {
						rootPath = filepath.VolumeName(fsp.Vfs.GetPath()) + string(os.PathSeparator)
					}
				}
				pf.NavigateToPath(fsp, rootPath)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.HistoryBack",
		Area:        "Shell",
		Label:       "History Back",
		LabelKey:    "Action.Panel.HistoryBack",
		Description: "Scroll long file names left, or move backward through folders history",
		DescKey:     "Action.Panel.HistoryBack.Desc",
		DefaultKeys: []string{"AltLeft"},
		MenuPath:    "Commands",
		MenuSubPath: "Navigation",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				// far2l scrolls long names with Alt+Left/Right (#890). Keep
				// folder history on the same keys, but only when nothing on
				// screen is cut off, so scrolling never jumps directories.
				if fsp.NamesOverflow() {
					fsp.ScrollNames(-1)
					return
				}
				pf.MoveFolderHistory(fsp, -1)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.HistoryForward",
		Area:        "Shell",
		Label:       "History Forward",
		LabelKey:    "Action.Panel.HistoryForward",
		Description: "Scroll long file names right, or move forward through folders history",
		DescKey:     "Action.Panel.HistoryForward.Desc",
		DefaultKeys: []string{"AltRight"},
		MenuPath:    "Commands",
		MenuSubPath: "Navigation",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				if fsp.NamesOverflow() {
					fsp.ScrollNames(1)
					return
				}
				pf.MoveFolderHistory(fsp, 1)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.ScrollNamesLeft",
		Area:        "Shell",
		Label:       "Scroll Names Left",
		LabelKey:    "Action.Panel.ScrollNamesLeft",
		Description: "Scroll long file names one cell left",
		DescKey:     "Action.Panel.ScrollNamesLeft.Desc",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				fsp.ScrollNames(-1)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.ScrollNamesRight",
		Area:        "Shell",
		Label:       "Scroll Names Right",
		LabelKey:    "Action.Panel.ScrollNamesRight",
		Description: "Scroll long file names one cell right",
		DescKey:     "Action.Panel.ScrollNamesRight.Desc",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				fsp.ScrollNames(1)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.ScrollNamesHome",
		Area:        "Shell",
		Label:       "Scroll Names to Start",
		LabelKey:    "Action.Panel.ScrollNamesHome",
		Description: "Show the beginning of long file names",
		DescKey:     "Action.Panel.ScrollNamesHome.Desc",
		DefaultKeys: []string{"AltHome"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				fsp.SetNameLeftPos(0)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.ScrollNamesEnd",
		Area:        "Shell",
		Label:       "Scroll Names to End",
		LabelKey:    "Action.Panel.ScrollNamesEnd",
		Description: "Show the end of long file names",
		DescKey:     "Action.Panel.ScrollNamesEnd.Desc",
		DefaultKeys: []string{"AltEnd"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				fsp.SetNameLeftPos(1 << 30)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.CopyPath",
		Area:        "Shell",
		Label:       "Copy Path",
		LabelKey:    "Action.Panel.CopyPath",
		Description: "Copy the full path of the current file to clipboard",
		DescKey:     "Action.Panel.CopyPath.Desc",
		DefaultKeys: []string{"CtrlD"},
		MenuPath:    "Commands",
		MenuSubPath: "Paths",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				if path := panel.CurrentPanelEntryPath(fsp); path != "" {
					terminal.SetF4Clipboard(path)
				}
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.InsertPath",
		Area:        "Shell",
		Label:       "Insert Path into Command Line",
		LabelKey:    "Action.Panel.InsertPath",
		Description: "Insert the full path of the current file into the command line",
		DescKey:     "Action.Panel.InsertPath.Desc",
		DefaultKeys: []string{"CtrlF"},
		MenuPath:    "Commands",
		MenuSubPath: "Paths",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				pf.InsertPathToCmdLine(panel.CurrentPanelEntryPath(fsp))
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.CopyName",
		Area:        "Shell",
		Label:       "Copy Name",
		LabelKey:    "Action.Panel.CopyName",
		Description: "Copy the current file name to clipboard",
		DescKey:     "Action.Panel.CopyName.Desc",
		DefaultKeys: []string{"CtrlIns"},
		MenuPath:    "Commands",
		MenuSubPath: "Paths",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if !pf.CmdLine.IsEmpty() {
				terminal.SetF4Clipboard(pf.CmdLine.Edit.GetText())
				return
			}
			if fsp := pf.GetActivePanel(); fsp != nil {
				idx := fsp.GetCursorIndex()
				if idx < 0 || idx >= len(fsp.Entries) {
					return
				}
				name := fsp.Entries[idx].Name
				if name == ".." {
					// far2l docs: with the cursor on ".." this hotkey
					// treats it as the name of the current folder.
					// Mirrors far2l's PointToName(GetCurDir()) branch
					// in FileList::CopyNames() (FullPathName=false).
					name = fsp.Vfs.Base(fsp.Vfs.GetPath())
				}
				terminal.SetF4Clipboard(name)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.CopySelectedNames",
		Area:        "Shell",
		Label:       "Copy Selected Names",
		LabelKey:    "Action.Panel.CopySelectedNames",
		Description: "Copy names of selected files to clipboard",
		DescKey:     "Action.Panel.CopySelectedNames.Desc",
		DefaultKeys: []string{"CtrlShiftIns"},
		MenuPath:    "Commands",
		MenuSubPath: "Paths",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				if names := fsp.GetSelectedNames(); len(names) > 0 {
					// SetClipboard can block up to ~4s on far2l IPC or
					// while shelling out to xclip/wl-copy — do it off the
					// UI goroutine (matches Grabber's copyAndExit).
					terminal.SetClipboardAsync(strings.Join(names, "\n"))
				}
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.CopySelectedPaths",
		Area:        "Shell",
		Label:       "Copy Selected Paths",
		LabelKey:    "Action.Panel.CopySelectedPaths",
		Description: "Copy full paths of selected files to clipboard",
		DescKey:     "Action.Panel.CopySelectedPaths.Desc",
		DefaultKeys: []string{"AltShiftIns"},
		MenuPath:    "Commands",
		MenuSubPath: "Paths",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				base := fsp.Vfs.GetPath()
				names := fsp.GetSelectedNames()
				if len(names) == 0 {
					// far2l note: with the cursor on ".." this action
					// treats it as the name of the current folder.
					if cursorOnParent(fsp) {
						terminal.SetClipboardAsync(base)
					}
					return
				}
				paths := make([]string, 0, len(names))
				for _, n := range names {
					paths = append(paths, fsp.Vfs.Join(base, n))
				}
				terminal.SetClipboardAsync(strings.Join(paths, "\n"))
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.CopySelectedRealPaths",
		Area:        "Shell",
		Label:       "Copy Selected Real Paths",
		LabelKey:    "Action.Panel.CopySelectedRealPaths",
		Description: "Copy full paths of selected files with symlink resolution",
		DescKey:     "Action.Panel.CopySelectedRealPaths.Desc",
		DefaultKeys: []string{"CtrlAltIns"},
		MenuPath:    "Commands",
		MenuSubPath: "Paths",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.GetActivePanel(); fsp != nil {
				base := fsp.Vfs.GetPath()
				_, isOS := fsp.Vfs.(*vfs.OSVFS)
				resolve := func(p string) string {
					if isOS {
						if r, err := filepath.EvalSymlinks(p); err == nil {
							return r
						}
					}
					return p
				}
				names := fsp.GetSelectedNames()
				if len(names) == 0 {
					// far2l note: with the cursor on ".." this action
					// treats it as the name of the current folder.
					if cursorOnParent(fsp) {
						terminal.SetClipboardAsync(resolve(base))
					}
					return
				}
				paths := make([]string, 0, len(names))
				for _, n := range names {
					paths = append(paths, resolve(fsp.Vfs.Join(base, n)))
				}
				terminal.SetClipboardAsync(strings.Join(paths, "\n"))
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.SortUseGroups",
		Area:        "Shell",
		Label:       "Use Sort Groups",
		LabelKey:    "Menu.SortUseGroups",
		Description: "Group panel entries by the configured sort groups",
		DescKey:     "Action.Panel.SortUseGroups.Desc",
		Checked: func() bool {
			pf := panel.FindPanelsFrameAnyScreen()
			if pf == nil {
				return false
			}
			fsp := pf.GetActivePanel()
			return fsp != nil && fsp.UseSortGroups
		},
		Handler: withPF(func(pf *panel.PanelsFrame) { vtui.FrameManager.EmitCommand(appcmd.CmSortGroups, nil) }),
	})
	registerAction(action.Action{
		Name:                "Panel.SortMenu",
		Area:                "Shell",
		Label:               "Sort Modes",
		LabelKey:            "Action.Panel.SortMenu",
		Description:         "Show sort modes menu",
		DescKey:             "Action.Panel.SortMenu.Desc",
		DefaultKeys:         []string{"CtrlF12"},
		MenuPath:            "Commands",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { actionSortMenu(pf) }),
	})

	registerAction(action.Action{
		Name:        "Settings.Language",
		Area:        "Shell",
		Label:       "Language",
		LabelKey:    "Menu.Language",
		Description: "Open language selection dialog",
		DescKey:     "Action.Settings.Language.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionLanguage(pf) }),
	})
	registerAction(action.Action{
		Name:                "Settings.Panel",
		Area:                "Shell",
		Label:               "Panel Settings",
		LabelKey:            "Menu.PanelSettings",
		Description:         "Open panel settings dialog",
		DescKey:             "Action.Settings.Panel.Desc",
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { actionPanelSettings(pf) }),
	})
	registerAction(action.Action{
		Name:        "Settings.Editor",
		Area:        "Shell",
		Label:       "Editor Settings",
		LabelKey:    "Menu.EditorSettings",
		Description: "Open editor settings dialog",
		DescKey:     "Action.Settings.Editor.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionEditorSettings(pf) }),
	})
	registerAction(action.Action{
		Name:        "Settings.Viewer",
		Area:        "Shell",
		Label:       "Viewer Settings",
		LabelKey:    "Menu.ViewerSettings",
		Description: "Open viewer settings dialog",
		DescKey:     "Action.Settings.Viewer.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { dialog.ShowViewerSettings() }),
	})
	registerAction(action.Action{
		Name:        "Settings.Colorer",
		Area:        "Shell",
		Label:       "Colorer Settings",
		LabelKey:    "Menu.ColorerSettings",
		Description: "Open Colorer settings dialog",
		DescKey:     "Action.Settings.Colorer.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionColorerSettings(pf) }),
	})
	registerAction(action.Action{
		Name:        "Settings.Appearance",
		Area:        "Shell",
		Label:       "Appearance Settings",
		LabelKey:    "Menu.AppearanceSettings",
		Description: "Open appearance settings dialog",
		DescKey:     "Action.Settings.Appearance.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionAppearanceSettings(pf) }),
	})
	registerAction(action.Action{
		Name:        "Settings.Startup",
		Area:        "Shell",
		Label:       "Startup Settings",
		LabelKey:    "Menu.StartupSettings",
		Description: "Choose the startup mode and the renderer backends f4 uses by default",
		DescKey:     "Action.Settings.Startup.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionStartupSettings(pf) }),
	})
	registerAction(action.Action{
		Name:        "Settings.Portable",
		Area:        "Shell",
		Label:       "Portable Mode",
		LabelKey:    "Menu.PortableSettings",
		Description: "Keep the profile next to the program (Far-style f4.ini) or in the user directory",
		DescKey:     "Action.Settings.Portable.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { dialog.ShowPortableSettings() }),
	})
	registerAction(action.Action{
		Name:        "Settings.Confirmations",
		Area:        "Shell",
		Label:       "Confirmations Settings",
		LabelKey:    "Menu.ConfirmationsSettings",
		Description: "Open confirmations settings dialog",
		DescKey:     "Action.Settings.Confirmations.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionConfirmationsSettings(pf) }),
	})
	registerAction(action.Action{
		Name:        "Settings.MouseWheel",
		Area:        "Shell",
		Label:       "Mouse Wheel Settings",
		LabelKey:    "Menu.MouseWheelSettings",
		Description: "Open mouse wheel scroll speed settings dialog",
		DescKey:     "Action.Settings.MouseWheel.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionMouseWheelSettings(pf) }),
	})
	registerAction(action.Action{
		Name:        "Settings.PathHints",
		Area:        "Shell",
		Label:       "Path Hints Settings",
		LabelKey:    "Menu.PathHintSettings",
		Description: "Open path hints settings dialog",
		DescKey:     "Action.Settings.PathHints.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionPathHintSettings(pf) }),
	})
	registerAction(action.Action{
		Name:                "Settings.Hotkeys",
		Area:                "Shell",
		Label:               "Hotkey Configuration",
		LabelKey:            "Action.Settings.Hotkeys",
		Description:         "Open Hotkey Configurator",
		DescKey:             "Action.Settings.Hotkeys.Desc",
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { actionHotkeyConfig(pf) }),
	})
	registerAction(action.Action{
		Name:                "Settings.AutoUpdate",
		Area:                "Shell",
		Label:               "Auto Update Settings",
		LabelKey:            "Menu.AutoUpdateSettings",
		Description:         "Open auto update settings dialog",
		DescKey:             "Action.Settings.AutoUpdate.Desc",
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { vtui.FrameManager.EmitCommand(appcmd.CmUpdateSettings, nil) }),
	})
	registerAction(action.Action{
		Name:        "Settings.Proxy",
		Area:        "Shell",
		Label:       "Proxy Settings",
		LabelKey:    "Menu.ProxySettings",
		Description: "Configure the proxy used for updates, plugins and network connections",
		DescKey:     "Action.Settings.Proxy.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { vtui.FrameManager.EmitCommand(appcmd.CmProxySettings, nil) }),
	})
	registerAction(action.Action{
		Name:                "Settings.PluginConfiguration",
		Area:                "Shell",
		Label:               "Plugin Configuration",
		LabelKey:            "Menu.PluginConfiguration",
		Description:         "Configure loaded plugins",
		DescKey:             "Action.Settings.PluginConfiguration.Desc",
		DefaultKeys:         []string{"ShiftF11"},
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { actionPluginConfiguration(pf) }),
	})
	registerAction(action.Action{
		Name:                "Settings.Plugins",
		Area:                "Shell",
		Label:               "Plugins Menu",
		LabelKey:            "Menu.Options.Plugins",
		Description:         "Manage plugins dialog",
		DescKey:             "Action.Settings.Plugins.Desc",
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { actionManagePlugins(pf) }),
	})
	registerAction(action.Action{
		Name:                "App.PlugRing",
		Area:                "Shell",
		Label:               "f4 PlugRing",
		LabelKey:            "Action.App.PlugRing",
		Description:         "Open the PlugRing plugin catalog",
		DescKey:             "Action.App.PlugRing.Desc",
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *panel.PanelsFrame) { vtui.FrameManager.EmitCommand(appcmd.CmPlugRing, nil) }),
	})
	registerAction(action.Action{
		Name:         "App.SaveSettings",
		Area:         "Shell",
		Label:        "Save Settings",
		LabelKey:     "Action.App.SaveSettings",
		Description:  "Save settings and session",
		DescKey:      "Action.App.SaveSettings.Desc",
		DefaultKeys:  []string{"ShiftF9:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		MenuPath:     "Options",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			actionSaveSettings(pf)
		}),
	})
	registerAction(action.Action{
		Name:        "App.ToggleWindowSize",
		Area:        "Common",
		Label:       "Toggle Window Size",
		LabelKey:    "Action.App.ToggleWindowSize",
		Description: "Toggle between two window sizes",
		DescKey:     "Action.App.ToggleWindowSize.Desc",
		DefaultKeys: []string{"AltF9"},
		MenuPath:    "Options",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			targetCols, targetRows := config.App.GuiCols, config.App.GuiRows
			if pf.LastW == config.App.GuiCols && pf.LastH == config.App.GuiRows {
				targetCols, targetRows = config.App.GuiCols+40, config.App.GuiRows+15
			}
			// xterm resize sequence for console mode
			// Terminal writes here are best effort: if stdout is gone there is
			// nothing left to resize and the next write reports it anyway.
			_, _ = fmt.Fprintf(os.Stdout, "\x1b[8;%d;%dt", targetRows, targetCols)
			_ = os.Stdout.Sync()
			// Forced OS window resize for GUI mode
			if vtui.FrameManager != nil {
				vtui.FrameManager.ResizeWindow(targetCols, targetRows)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.ToggleKeyBar",
		Area:        "Shell",
		Label:       "Toggle KeyBar",
		LabelKey:    "Action.Panel.ToggleKeyBar",
		Description: "Show or hide the KeyBar",
		DescKey:     "Action.Panel.ToggleKeyBar.Desc",
		DefaultKeys: []string{"CtrlB"},
		MenuPath:    "Options",
		Handler: withPF(func(pf *panel.PanelsFrame) {
			pf.ShowKeyBar = !pf.ShowKeyBar
			pf.ResizeConsole(pf.LastW, pf.LastH)
		}),
	})
	registerAction(action.Action{
		Name:        "Settings.MacKeyboard",
		Area:        "Shell",
		Label:       "Mac keyboard",
		LabelKey:    "Action.Settings.MacKeyboard",
		Description: "Use the macOS editing chords: Cmd for line and document edges, Opt for word navigation",
		DescKey:     "Action.Settings.MacKeyboard.Desc",
		MenuPath:    "Options",
		// The menu entry is offered where the keyboard it describes is, and
		// stays reachable for anyone who has already asked for the layout on
		// another platform, so the switch is never one-way.
		Visible: func() bool { return runtime.GOOS == "darwin" || keymap.MacKeysEnabled() },
		Checked: keymap.MacKeysEnabled,
		Handler: func() bool {
			if keymap.MacKeysEnabled() {
				config.App.MacKeyboard = config.MacKeysOff
			} else {
				config.App.MacKeyboard = config.MacKeysOn
			}
			config.RequestSaveConfig()
			return true
		},
	})

	// --- Shell key-only actions (no menu entries) ---
	registerAction(action.Action{
		Name:        "Panel.Rescan",
		Area:        "Shell",
		Label:       "Rescan",
		Description: "Refresh panel contents",
		DescKey:     "Action.Panel.Rescan.Desc",
		DefaultKeys: []string{"CtrlR"},
		Handler:     withPF(func(pf *panel.PanelsFrame) { pf.RefreshAll() }),
	})
	registerAction(action.Action{
		Name:        "Panel.Swap",
		Area:        "Shell",
		Label:       "Swap Panels",
		Description: "Swap left and right panels",
		DescKey:     "Action.Panel.Swap.Desc",
		DefaultKeys: []string{"CtrlU"},
		Handler:     withPF(func(pf *panel.PanelsFrame) { vtui.FrameManager.EmitCommand(appcmd.CmSwapPanels, nil) }),
	})
	registerAction(action.Action{
		Name:         "Panel.Toggle",
		Area:         "Shell",
		Label:        "Toggle Panels",
		Description:  "Show or hide panels",
		DescKey:      "Action.Panel.Toggle.Desc",
		DefaultKeys:  []string{"CtrlO:NoAltScreenApp", "Esc:EscToggle", "Del:EscToggle", "NumDel:EscToggle"},
		DefaultAreas: []string{"Terminal"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			pf.TogglePanelsVisibility()
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.ToggleLeftPanel",
		Area:        "Shell",
		Label:       "Toggle Left Panel",
		Description: "Show or hide the left panel",
		DescKey:     "Action.Panel.ToggleLeftPanel.Desc",
		DefaultKeys: []string{"CtrlF1"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			pf.ExitWide()
			pf.ShowLeftPanel = !pf.ShowLeftPanel
			if !pf.ShowLeftPanel && pf.ActiveIdx == 0 && pf.ShowRightPanel {
				pf.ActiveIdx = 1
			}
			if !pf.ShowLeftPanel && !pf.ShowRightPanel {
				pf.ShowPanels = false
			}
			if pf.LastW > 0 && pf.LastH > 0 {
				pf.ResizeConsole(pf.LastW, pf.LastH)
			}
			vtui.FrameManager.HardRefresh()
			if pf.ShowPanels {
				pf.RefreshAll()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.ToggleRightPanel",
		Area:        "Shell",
		Label:       "Toggle Right Panel",
		Description: "Show or hide the right panel",
		DescKey:     "Action.Panel.ToggleRightPanel.Desc",
		DefaultKeys: []string{"CtrlF2"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			pf.ExitWide()
			pf.ShowRightPanel = !pf.ShowRightPanel
			if !pf.ShowRightPanel && pf.ActiveIdx == 1 && pf.ShowLeftPanel {
				pf.ActiveIdx = 0
			}
			if !pf.ShowLeftPanel && !pf.ShowRightPanel {
				pf.ShowPanels = false
			}
			if pf.LastW > 0 && pf.LastH > 0 {
				pf.ResizeConsole(pf.LastW, pf.LastH)
			}
			vtui.FrameManager.HardRefresh()
			if pf.ShowPanels {
				pf.RefreshAll()
			}
		}),
	})
	registerAction(action.Action{
		Name:         "Panel.TogglePassivePanel",
		Area:         "Shell",
		Label:        "Toggle Passive Panel",
		Description:  "Show or hide the passive panel",
		DescKey:      "Action.Panel.TogglePassivePanel.Desc",
		DefaultKeys:  []string{"CtrlP:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			pf.ExitWide()
			if pf.ActiveIdx == 0 {
				pf.ShowRightPanel = !pf.ShowRightPanel
			} else {
				pf.ShowLeftPanel = !pf.ShowLeftPanel
			}
			if !pf.ShowLeftPanel && !pf.ShowRightPanel {
				pf.ShowPanels = false
			}
			if pf.LastW > 0 && pf.LastH > 0 {
				pf.ResizeConsole(pf.LastW, pf.LastH)
			}
			vtui.FrameManager.HardRefresh()
			if pf.ShowPanels {
				pf.RefreshAll()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.InfoPanel",
		Area:        "Shell",
		Label:       "Info Panel",
		Description: "Toggle the info panel",
		DescKey:     "Action.Panel.InfoPanel.Desc",
		DefaultKeys: []string{"CtrlL"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			pf.ToggleAltPanel("info", func(src *panel.FileSystemPanel) panel.AltPanel { return panel.NewInfoPanel(src) })
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.QuickView",
		Area:        "Shell",
		Label:       "Quick View",
		Description: "Toggle the quick view panel",
		DescKey:     "Action.Panel.QuickView.Desc",
		DefaultKeys: []string{"CtrlQ"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			pf.ToggleAltPanel("quick_view", func(src *panel.FileSystemPanel) panel.AltPanel { return panel.NewQuickViewPanel(src) })
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.Player",
		Area:        "Shell",
		Label:       "Player",
		LabelKey:    "Menu.Panel.Player",
		Description: "Toggle the audio player panel",
		DescKey:     "Action.Panel.Player.Desc",
		DefaultKeys: []string{"CtrlShiftM"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			pf.ToggleAltPanel("player", func(src *panel.FileSystemPanel) panel.AltPanel { return panel.NewPlayerPanel(src) })
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.SplitLeft",
		Area:        "Shell",
		Label:       "Move Split Left",
		Description: "Move the vertical split to the left",
		DescKey:     "Action.Panel.SplitLeft.Desc",
		DefaultKeys: []string{"CtrlLeft:EmptyCommandLine"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			next := pf.WidthDecrement + 1
			if maxWD := (pf.LastW / 2) - 10; maxWD > 0 && next <= maxWD && next >= -maxWD {
				pf.WidthDecrement = next
				config.App.WidthDecrement = next
				config.RequestSaveConfig()
				pf.ResizeConsole(pf.LastW, pf.LastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.SplitRight",
		Area:        "Shell",
		Label:       "Move Split Right",
		Description: "Move the vertical split to the right",
		DescKey:     "Action.Panel.SplitRight.Desc",
		DefaultKeys: []string{"CtrlRight:EmptyCommandLine"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			next := pf.WidthDecrement - 1
			if maxWD := (pf.LastW / 2) - 10; maxWD > 0 && next <= maxWD && next >= -maxWD {
				pf.WidthDecrement = next
				config.App.WidthDecrement = next
				config.RequestSaveConfig()
				pf.ResizeConsole(pf.LastW, pf.LastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.SplitUp",
		Area:        "Shell",
		Label:       "Move Split Up",
		Description: "Shrink both panels vertically",
		DescKey:     "Action.Panel.SplitUp.Desc",
		DefaultKeys: []string{"CtrlUp:EmptyCommandLine"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			nextL := pf.LeftHeightDecrement + 1
			nextR := pf.RightHeightDecrement + 1
			maxHD := pf.LastH - 7
			if nextL >= 0 && nextR >= 0 && (maxHD <= 0 || (nextL <= maxHD && nextR <= maxHD)) {
				pf.LeftHeightDecrement = nextL
				pf.RightHeightDecrement = nextR
				config.App.LeftHeightDecrement = nextL
				config.App.RightHeightDecrement = nextR
				config.RequestSaveConfig()
				pf.ResizeConsole(pf.LastW, pf.LastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.SplitDown",
		Area:        "Shell",
		Label:       "Move Split Down",
		Description: "Grow both panels vertically",
		DescKey:     "Action.Panel.SplitDown.Desc",
		DefaultKeys: []string{"CtrlDown:EmptyCommandLine"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			nextL := pf.LeftHeightDecrement - 1
			nextR := pf.RightHeightDecrement - 1
			maxHD := pf.LastH - 7
			if nextL >= 0 && nextR >= 0 && (maxHD <= 0 || (nextL <= maxHD && nextR <= maxHD)) {
				pf.LeftHeightDecrement = nextL
				pf.RightHeightDecrement = nextR
				config.App.LeftHeightDecrement = nextL
				config.App.RightHeightDecrement = nextR
				config.RequestSaveConfig()
				pf.ResizeConsole(pf.LastW, pf.LastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.SplitActiveUp",
		Area:        "Shell",
		Label:       "Shrink Active Panel",
		Description: "Shrink the active panel vertically",
		DescKey:     "Action.Panel.SplitActiveUp.Desc",
		DefaultKeys: []string{"CtrlShiftUp:EmptyCommandLine"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			cur := &pf.RightHeightDecrement
			cfg := &config.App.RightHeightDecrement
			if pf.ActiveIdx == 0 {
				cur = &pf.LeftHeightDecrement
				cfg = &config.App.LeftHeightDecrement
			}
			next := *cur + 1
			maxHD := pf.LastH - 7
			if next >= 0 && (maxHD <= 0 || next <= maxHD) {
				*cur = next
				*cfg = next
				config.RequestSaveConfig()
				pf.ResizeConsole(pf.LastW, pf.LastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.SplitActiveDown",
		Area:        "Shell",
		Label:       "Grow Active Panel",
		Description: "Grow the active panel vertically",
		DescKey:     "Action.Panel.SplitActiveDown.Desc",
		DefaultKeys: []string{"CtrlShiftDown:EmptyCommandLine"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			cur := &pf.RightHeightDecrement
			cfg := &config.App.RightHeightDecrement
			if pf.ActiveIdx == 0 {
				cur = &pf.LeftHeightDecrement
				cfg = &config.App.LeftHeightDecrement
			}
			next := *cur - 1
			maxHD := pf.LastH - 7
			if next >= 0 && (maxHD <= 0 || next <= maxHD) {
				*cur = next
				*cfg = next
				config.RequestSaveConfig()
				pf.ResizeConsole(pf.LastW, pf.LastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.SplitReset",
		Area:        "Shell",
		Label:       "Reset Split",
		Description: "Reset the panel split to defaults",
		DescKey:     "Action.Panel.SplitReset.Desc",
		DefaultKeys: []string{"CtrlVK_C"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if pf.WidthDecrement != 0 || pf.LeftHeightDecrement != 0 || pf.RightHeightDecrement != 0 {
				pf.WidthDecrement = 0
				pf.LeftHeightDecrement = 0
				pf.RightHeightDecrement = 0
				config.App.WidthDecrement = 0
				config.App.LeftHeightDecrement = 0
				config.App.RightHeightDecrement = 0
				config.RequestSaveConfig()
				pf.ResizeConsole(pf.LastW, pf.LastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.SyncPanels",
		Area:        "Shell",
		Label:       "Sync Panels",
		Description: "Open the active panel's directory in the passive panel",
		DescKey:     "Action.Panel.SyncPanels.Desc",
		DefaultKeys: []string{"AltI"},
		Handler:     withPF(func(pf *panel.PanelsFrame) { pf.SyncPassivePanel() }),
	})
	registerAction(action.Action{
		Name:        "Panel.ToggleInfoBytes",
		Area:        "Shell",
		Label:       "Toggle Bytes Format",
		Description: "Flip number formatting in info and quick view panels",
		DescKey:     "Action.Panel.ToggleInfoBytes.Desc",
		DefaultKeys: []string{"B:AltPanelVisible"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			config.App.InfoPanelBytes = !config.App.InfoPanelBytes
			config.RequestSaveConfig()
			vtui.FrameManager.HardRefresh()
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.ToggleHidden",
		Area:        "Shell",
		Label:       "Toggle Hidden",
		LabelKey:    "Action.Panel.ToggleHidden",
		Description: "Show or hide hidden and system files on both panels",
		DescKey:     "Action.Panel.ToggleHidden.Desc",
		DefaultKeys: []string{"CtrlH"},
		Checked:     func() bool { return config.App.ShowHiddenFiles },
		Handler: withPF(func(pf *panel.PanelsFrame) {
			config.App.ShowHiddenFiles = !config.App.ShowHiddenFiles
			pf.RefreshAll()
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.ViewBrief",
		Area:        "Shell",
		Label:       "Brief Mode",
		Description: "Set active panel to brief mode",
		DescKey:     "Action.Panel.ViewBrief.Desc",
		DefaultKeys: []string{"Ctrl1"},
		Visible:     func() bool { return !isAIPanelActive() },
		Handler:     withPF(func(pf *panel.PanelsFrame) { pf.SetPanelViewMode(pf.ActiveIdx, panel.ViewModeBrief) }),
	})
	registerAction(action.Action{
		Name:        "Panel.ViewMedium",
		Area:        "Shell",
		Label:       "Medium Mode",
		Description: "Set active panel to medium mode",
		DescKey:     "Action.Panel.ViewMedium.Desc",
		DefaultKeys: []string{"Ctrl2"},
		Visible:     func() bool { return !isAIPanelActive() },
		Handler:     withPF(func(pf *panel.PanelsFrame) { pf.SetPanelViewMode(pf.ActiveIdx, panel.ViewModeMedium) }),
	})
	registerAction(action.Action{
		Name:        "Panel.ViewDetailed",
		Area:        "Shell",
		Label:       "Detailed Mode",
		Description: "Set active panel to detailed mode",
		DescKey:     "Action.Panel.ViewDetailed.Desc",
		DefaultKeys: []string{"Ctrl3"},
		Visible:     func() bool { return !isAIPanelActive() },
		Handler:     withPF(func(pf *panel.PanelsFrame) { pf.SetPanelViewMode(pf.ActiveIdx, panel.ViewModeDetailed) }),
	})
	registerAction(action.Action{
		Name:        "Panel.ViewWide",
		Area:        "Shell",
		Label:       "Wide Mode",
		Description: "Set active panel to wide mode",
		DescKey:     "Action.Panel.ViewWide.Desc",
		DefaultKeys: []string{"Ctrl4"},
		Visible:     func() bool { return !isAIPanelActive() },
		Handler:     withPF(func(pf *panel.PanelsFrame) { pf.SetWidePanel(pf.ActiveIdx) }),
	})
	registerAction(action.Action{
		Name:        "Panel.SortByName",
		Area:        "Shell",
		Label:       "Sort by Name",
		Description: "Sort panel by name",
		DescKey:     "Action.Panel.SortByName.Desc",
		DefaultKeys: []string{"CtrlF3"},
		Handler:     withPF(func(pf *panel.PanelsFrame) { vtui.FrameManager.EmitCommand(appcmd.CmSortName, nil) }),
	})
	registerAction(action.Action{
		Name:        "Panel.SortByExt",
		Area:        "Shell",
		Label:       "Sort by Extension",
		Description: "Sort panel by extension",
		DescKey:     "Action.Panel.SortByExt.Desc",
		DefaultKeys: []string{"CtrlF4"},
		Handler:     withPF(func(pf *panel.PanelsFrame) { vtui.FrameManager.EmitCommand(appcmd.CmSortExt, nil) }),
	})
	registerAction(action.Action{
		Name:        "Panel.SortByTime",
		Area:        "Shell",
		Label:       "Sort by Time",
		Description: "Sort panel by modification time",
		DescKey:     "Action.Panel.SortByTime.Desc",
		DefaultKeys: []string{"CtrlF5"},
		Handler:     withPF(func(pf *panel.PanelsFrame) { vtui.FrameManager.EmitCommand(appcmd.CmSortTime, nil) }),
	})
	registerAction(action.Action{
		Name:        "Panel.SortBySize",
		Area:        "Shell",
		Label:       "Sort by Size",
		Description: "Sort panel by size",
		DescKey:     "Action.Panel.SortBySize.Desc",
		DefaultKeys: []string{"CtrlF6"},
		Handler:     withPF(func(pf *panel.PanelsFrame) { vtui.FrameManager.EmitCommand(appcmd.CmSortSize, nil) }),
	})
	registerAction(action.Action{
		Name:        "Panel.SortUnsorted",
		Area:        "Shell",
		Label:       "Unsorted",
		Description: "Disable panel sorting",
		DescKey:     "Action.Panel.SortUnsorted.Desc",
		DefaultKeys: []string{"CtrlF7"},
		Handler:     withPF(func(pf *panel.PanelsFrame) { vtui.FrameManager.EmitCommand(appcmd.CmSortUnsorted, nil) }),
	})
	registerAction(action.Action{
		Name:         "Panel.LeftDriveMenu",
		Area:         "Shell",
		Label:        "Left Drive Menu",
		LabelKey:     "Menu.Left.DriveMenu",
		Description:  "Show the drive menu for the left panel",
		DescKey:      "Action.Panel.LeftDriveMenu.Desc",
		DefaultKeys:  []string{"AltF1:NoAltScreenApp", "CtrlShiftLeft:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		Handler:      withPF(func(pf *panel.PanelsFrame) { pf.ShowDriveMenu(0) }),
	})
	registerAction(action.Action{
		Name:         "Panel.RightDriveMenu",
		Area:         "Shell",
		Label:        "Right Drive Menu",
		LabelKey:     "Menu.Right.DriveMenu",
		Description:  "Show the drive menu for the right panel",
		DescKey:      "Action.Panel.RightDriveMenu.Desc",
		DefaultKeys:  []string{"AltF2:NoAltScreenApp", "CtrlShiftRight:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		Handler:      withPF(func(pf *panel.PanelsFrame) { pf.ShowDriveMenu(1) }),
	})
	registerAction(action.Action{
		Name:        "Panel.EnterDirectory",
		Area:        "Shell",
		Label:       "Enter Directory",
		Description: "Enter the directory or archive under the cursor",
		DescKey:     "Action.Panel.EnterDirectory.Desc",
		DefaultKeys: []string{"CtrlPgDn", "CtrlShiftPgDn"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			fsp := pf.GetActivePanel()
			if fsp == nil {
				return
			}
			idx := fsp.GetCursorIndex()
			if idx < 0 || idx >= len(fsp.Entries) {
				return
			}
			selected := fsp.Entries[idx]
			isDir := selected.IsDir
			isArchive := false
			if !isDir {
				fullPath := fsp.Vfs.Join(fsp.Vfs.GetPath(), selected.Name)
				isArchive = vfs.FindProvider(context.Background(), fsp.Vfs, fullPath) != nil
			}
			if isDir || isArchive {
				fsp.EnterSelectedFromAction()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.InsertFileName",
		Area:        "Shell",
		Label:       "Insert File Name",
		Description: "Insert the current file name into the command line",
		DescKey:     "Action.Panel.InsertFileName.Desc",
		// Hiding the panels does not drop the panel cursor: the command
		// line is still on screen and Ctrl+Enter still pastes the current
		// file name into it, as in far2l. NoTerminalApp keeps the key out
		// of the way of a running child process, which owns Ctrl+Enter and
		// leaves the command line hidden anyway.
		DefaultKeys:  []string{"CtrlEnter:NoTerminalApp"},
		DefaultAreas: []string{"Terminal"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			pf.InsertSelectedFileName()
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.InsertLeftPath",
		Area:        "Shell",
		Label:       "Insert Left Path",
		Description: "Insert the left panel path into the command line",
		DescKey:     "Action.Panel.InsertLeftPath.Desc",
		DefaultKeys: []string{"CtrlVK_DB"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.VisualLeftFSP(); fsp != nil {
				pf.InsertPathToCmdLine(fsp.Vfs.GetPath())
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Panel.InsertRightPath",
		Area:        "Shell",
		Label:       "Insert Right Path",
		Description: "Insert the right panel path into the command line",
		DescKey:     "Action.Panel.InsertRightPath.Desc",
		DefaultKeys: []string{"CtrlVK_DD"},
		Handler: withPF(func(pf *panel.PanelsFrame) {
			if fsp := pf.VisualRightFSP(); fsp != nil {
				pf.InsertPathToCmdLine(fsp.Vfs.GetPath())
			}
		}),
	})
	registerAction(action.Action{
		Name:         "App.Quit",
		Area:         "Shell",
		Label:        "Quit",
		Description:  "Quit f4",
		DescKey:      "Action.App.Quit.Desc",
		DefaultKeys:  []string{"F10:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		Handler:      func() bool { return vtui.FrameManager.EmitCommand(vtui.CmQuit, nil) },
	})
	registerAction(action.Action{
		Name:        "Debug.DummyOperation",
		Area:        "Shell",
		Label:       "Dummy Long Operation",
		Description: "Show a dummy long operation dialog (debug)",
		DescKey:     "Action.Debug.DummyOperation.Desc",
		DefaultKeys: []string{"AltF5"},
		Handler:     withPF(func(pf *panel.PanelsFrame) { pf.ShowDummyOpDialog() }),
	})

	// --- Terminal actions ---
	registerAction(action.Action{
		Name:        "Terminal.ViewLog",
		Area:        "Terminal",
		Label:       "View Terminal Log",
		LabelKey:    "Action.Terminal.ViewLog",
		Description: "Open terminal log in viewer",
		DescKey:     "Action.Terminal.ViewLog.Desc",
		DefaultKeys: []string{"F3:TerminalQuiet", "CtrlShiftF3"},
		MenuPath:    "File",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionViewTerminalLog(pf) }),
	})
	registerAction(action.Action{
		Name:        "Terminal.EditLog",
		Area:        "Terminal",
		Label:       "Edit Terminal Log",
		LabelKey:    "Action.Terminal.EditLog",
		Description: "Open terminal log in editor",
		DescKey:     "Action.Terminal.EditLog.Desc",
		DefaultKeys: []string{"F4:TerminalQuiet", "CtrlShiftF4"},
		MenuPath:    "File",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionEditTerminalLog(pf) }),
	})

	// --- Editor actions (menu order follows registration order) ---
	registerAction(action.Action{
		Name:        "Editor.Save",
		Area:        "Editor",
		Label:       "Save",
		LabelKey:    "Action.Editor.Save",
		Description: "Save file",
		DescKey:     "Action.Editor.Save.Desc",
		DefaultKeys: []string{"F2"},
		MenuPath:    "File",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.SaveToFile(nil) }),
	})
	registerAction(action.Action{
		Name:        "Editor.SaveAs",
		Area:        "Editor",
		Label:       "Save as...",
		LabelKey:    "Action.Editor.SaveAs",
		Description: "Write the text under another name, codepage or line breaks",
		DescKey:     "Action.Editor.SaveAs.Desc",
		DefaultKeys: []string{"ShiftF2"},
		MenuPath:    "File",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.ShowSaveAsDialog() }),
	})
	registerAction(action.Action{
		Name:        "Editor.SwitchToViewer",
		Area:        "Editor",
		Label:       "Switch to Viewer",
		LabelKey:    "Action.Editor.SwitchToViewer",
		Description: "Switch to viewer mode",
		DescKey:     "Action.Editor.SwitchToViewer.Desc",
		DefaultKeys: []string{"F6"},
		MenuPath:    "File",
		Handler:     withEditor(func(ev *editor.EditorView) { actionSwitchEditorToViewer(ev) }),
	})
	registerAction(action.Action{
		Name:        "Editor.Quit",
		Area:        "Editor",
		Label:       "Quit",
		LabelKey:    "Action.Editor.Quit",
		Description: "Close editor",
		DescKey:     "Action.Editor.Quit.Desc",
		DefaultKeys: []string{"F10", "Esc"},
		MenuPath:    "File",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.TryClose() }),
	})

	registerAction(action.Action{
		Name:        "Editor.Undo",
		Area:        "Editor",
		Label:       "Undo",
		LabelKey:    "Action.Editor.Undo",
		Description: "Undo last change",
		DescKey:     "Action.Editor.Undo.Desc",
		DefaultKeys: []string{"CtrlZ"},
		MenuPath:    "Edit",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.Undo() }),
	})
	registerAction(action.Action{
		Name:        "Editor.Redo",
		Area:        "Editor",
		Label:       "Redo",
		LabelKey:    "Action.Editor.Redo",
		Description: "Redo last undone change",
		DescKey:     "Action.Editor.Redo.Desc",
		DefaultKeys: []string{"CtrlShiftZ"},
		MenuPath:    "Edit",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.Redo() }),
	})
	registerAction(action.Action{
		Name:        "Editor.Copy",
		Area:        "Editor",
		Label:       "Copy",
		LabelKey:    "Action.Editor.Copy",
		Description: "Copy selection to clipboard",
		DescKey:     "Action.Editor.Copy.Desc",
		DefaultKeys: []string{"CtrlC", "CtrlIns"},
		MenuPath:    "Edit",
		Handler: withEditor(func(ev *editor.EditorView) {
			if ev.SelActive || ev.RectSelActive {
				ev.CopySelection()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.Cut",
		Area:        "Editor",
		Label:       "Cut",
		LabelKey:    "Action.Editor.Cut",
		Description: "Cut selection to clipboard",
		DescKey:     "Action.Editor.Cut.Desc",
		// Ctrl+X is intentionally not a default key: the editor keeps it as
		// the classic down-movement alias when no selection exists (see
		// editor.EditorView.ProcessKey), and delegates to this action when there
		// is a selection. Shift+Del is the advertised Cut hotkey.
		DefaultKeys: []string{"ShiftDel"},
		MenuPath:    "Edit",
		Handler: withEditor(func(ev *editor.EditorView) {
			if ev.SelActive || ev.RectSelActive {
				ev.CopySelection()
				ev.DeleteSelection()
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.Paste",
		Area:        "Editor",
		Label:       "Paste",
		LabelKey:    "Action.Editor.Paste",
		Description: "Paste text from clipboard",
		DescKey:     "Action.Editor.Paste.Desc",
		DefaultKeys: []string{"ShiftIns", "CtrlV"},
		MenuPath:    "Edit",
		Handler: withEditor(func(ev *editor.EditorView) {
			if text := vtui.GetClipboard(); text != "" {
				ev.PasteText(text)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.SelectAll",
		Area:        "Editor",
		Label:       "Select All",
		LabelKey:    "Action.Editor.SelectAll",
		Description: "Select all text",
		DescKey:     "Action.Editor.SelectAll.Desc",
		DefaultKeys: []string{"CtrlA"},
		MenuPath:    "Edit",
		Handler: withEditor(func(ev *editor.EditorView) {
			ev.RectSelActive = false
			ev.SelActive = true
			ev.SelAnchorOffset = 0
			lastLine := ev.Li.LineCount() - 1
			ev.CursorLine = lastLine
			ev.CursorPos = ev.GetLineLength(lastLine)
			ev.EnsureCursorVisible()
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.DeleteLine",
		Area:        "Editor",
		Label:       "Delete Line",
		LabelKey:    "Action.Editor.DeleteLine",
		Description: "Delete current line",
		DescKey:     "Action.Editor.DeleteLine.Desc",
		DefaultKeys: []string{"CtrlY"},
		MenuPath:    "Edit",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.DeleteCurrentLine() }),
	})
	registerAction(action.Action{
		Name:        "Editor.DuplicateLine",
		Area:        "Editor",
		Label:       "Duplicate Line",
		LabelKey:    "Action.Editor.DuplicateLine",
		Description: "Duplicate the current line or the selected lines",
		DescKey:     "Action.Editor.DuplicateLine.Desc",
		// Ctrl+D is the classic WordStar right-arrow alias in the editor
		// and Alt+Arrows draw the block selection, so neither the
		// Notepad++ nor the VS Code spelling of this command is free.
		DefaultKeys: []string{"CtrlShiftD"},
		MenuPath:    "Edit",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.DuplicateLines() }),
	})
	registerAction(action.Action{
		Name:        "Editor.AddCursorAtNextOccurrence",
		Area:        "Editor",
		Label:       "Add Cursor at Next Occurrence",
		LabelKey:    "Action.Editor.AddCursorAtNextOccurrence",
		Description: "Select the word under the cursor, then put another cursor on each following copy of it",
		DescKey:     "Action.Editor.AddCursorAtNextOccurrence.Desc",
		// Ctrl+D, which other editors use for this, is the WordStar
		// right-arrow alias here and Ctrl+Shift+D duplicates a line.
		DefaultKeys: []string{"CtrlShiftN"},
		MenuPath:    "Edit",
		Handler:     withMultiEditor(func(ev *editor.EditorView) { ev.AddCursorAtNextOccurrence() }),
	})
	registerAction(action.Action{
		Name:        "Editor.SelectAllOccurrences",
		Area:        "Editor",
		Label:       "Select All Occurrences",
		LabelKey:    "Action.Editor.SelectAllOccurrences",
		Description: "Put a cursor on every copy of the selected text",
		DescKey:     "Action.Editor.SelectAllOccurrences.Desc",
		DefaultKeys: []string{"CtrlShiftL"},
		MenuPath:    "Edit",
		Handler:     withMultiEditor(func(ev *editor.EditorView) { ev.SelectAllOccurrences() }),
	})
	registerAction(action.Action{
		Name:        "Editor.MoveLineUp",
		Area:        "Editor",
		Label:       "Move Line Up",
		LabelKey:    "Action.Editor.MoveLineUp",
		Description: "Move the current line or the selected lines up",
		DescKey:     "Action.Editor.MoveLineUp.Desc",
		// Alt+Arrows already draw the block selection here, so these take
		// the Notepad++ spelling rather than the VS Code one.
		DefaultKeys: []string{"CtrlShiftUp"},
		MenuPath:    "Edit",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.MoveLines(-1) }),
	})
	registerAction(action.Action{
		Name:        "Editor.MoveLineDown",
		Area:        "Editor",
		Label:       "Move Line Down",
		LabelKey:    "Action.Editor.MoveLineDown",
		Description: "Move the current line or the selected lines down",
		DescKey:     "Action.Editor.MoveLineDown.Desc",
		DefaultKeys: []string{"CtrlShiftDown"},
		MenuPath:    "Edit",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.MoveLines(1) }),
	})
	registerAction(action.Action{
		Name:        "Editor.Base64Menu",
		Area:        "Editor",
		Label:       "Base64 Tools",
		LabelKey:    "Action.Editor.Base64Menu",
		Description: "Encode or decode the selected text as Base64",
		DescKey:     "Action.Editor.Base64Menu.Desc",
		DefaultKeys: []string{"F11"},
		Handler:     withEditor(func(ev *editor.EditorView) { ev.ShowPluginsMenu() }),
	})
	registerAction(action.Action{
		Name:        "Editor.Base64Encode",
		Area:        "Editor",
		Label:       "Encode selection as Base64",
		LabelKey:    "Action.Editor.Base64Encode",
		Description: "Replace the selected text with its Base64 encoding",
		DescKey:     "Action.Editor.Base64Encode.Desc",
		MenuPath:    "Edit",
		Handler: withEditor(func(ev *editor.EditorView) {
			if err := ev.TransformBase64Selection(true); err != nil {
				vtui.ShowMessage(i18n.Msg("Editor.Plugins.Title"), err.Error(), []string{i18n.Msg("vtui.Ok")})
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.Base64Decode",
		Area:        "Editor",
		Label:       "Decode selection from Base64",
		LabelKey:    "Action.Editor.Base64Decode",
		Description: "Replace selected Base64 text with its decoded bytes",
		DescKey:     "Action.Editor.Base64Decode.Desc",
		MenuPath:    "Edit",
		Handler: withEditor(func(ev *editor.EditorView) {
			if err := ev.TransformBase64Selection(false); err != nil {
				vtui.ShowMessage(i18n.Msg("Editor.Plugins.Title"), err.Error(), []string{i18n.Msg("vtui.Ok")})
			}
		}),
	})
	registerAction(action.Action{
		Name:                "Editor.SortLines",
		Area:                "Editor",
		Label:               "Sort lines",
		LabelKey:            "Action.Editor.SortLines",
		Description:         "Sort selected lines or all lines",
		DescKey:             "Action.Editor.SortLines.Desc",
		MenuPath:            "Edit",
		MenuSeparatorBefore: true,
		Handler:             withEditor(func(ev *editor.EditorView) { ev.ShowSortDialog() }),
	})
	registerAction(action.Action{
		Name:        "Editor.ToggleOvertype",
		Area:        "Editor",
		Label:       "Insert/Overtype",
		LabelKey:    "Action.Editor.ToggleOvertype",
		Description: "Toggle insert/overtype mode",
		DescKey:     "Action.Editor.ToggleOvertype.Desc",
		DefaultKeys: []string{"Ins"},
		MenuPath:    "Edit",
		Checked:     editorState(func(ev *editor.EditorView) bool { return ev.Overtype }),
		Handler: withEditor(func(ev *editor.EditorView) {
			ev.Overtype = !ev.Overtype
			ev.EnsureCursorVisible()
		}),
	})

	registerAction(action.Action{
		Name:        "Editor.Search",
		Area:        "Editor",
		Label:       "Search",
		LabelKey:    "Action.Editor.Search",
		Description: "Find text",
		DescKey:     "Action.Editor.Search.Desc",
		DefaultKeys: []string{"F7"},
		MenuPath:    "Search",
		Handler:     withEditor(func(ev *editor.EditorView) { vtui.FrameManager.EmitCommand(appcmd.CmSearch, nil) }),
	})
	registerAction(action.Action{
		Name:        "Editor.Replace",
		Area:        "Editor",
		Label:       "Replace",
		LabelKey:    "Action.Editor.Replace",
		Description: "Replace text",
		DescKey:     "Action.Editor.Replace.Desc",
		DefaultKeys: []string{"CtrlF7"},
		MenuPath:    "Search",
		Handler:     withEditor(func(ev *editor.EditorView) { vtui.FrameManager.EmitCommand(appcmd.CmReplace, nil) }),
	})
	registerAction(action.Action{
		Name:        "Editor.SearchNext",
		Area:        "Editor",
		Label:       "Search Next",
		LabelKey:    "Action.Editor.SearchNext",
		Description: "Continue search",
		DescKey:     "Action.Editor.SearchNext.Desc",
		DefaultKeys: []string{"ShiftF7"},
		MenuPath:    "Search",
		Handler: withEditor(func(ev *editor.EditorView) {
			if editor.LastEditorSearch != "" {
				ev.Search(editor.LastEditorSearch, editor.LastEditorSearchCase, editor.LastEditorSearchReverse, editor.LastEditorSearchRegexp, editor.LastEditorSearchWholeWord, true)
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.SearchForward",
		Area:        "Editor",
		Label:       "Search Next",
		LabelKey:    "Action.Editor.SearchNext",
		Description: "Continue search forwards",
		DescKey:     "Action.Editor.SearchNext.Desc",
		DefaultKeys: []string{"CtrlEnter"},
		Handler:     withEditor(func(ev *editor.EditorView) { repeatEditorSearchDirection(ev, false) }),
	})
	registerAction(action.Action{
		Name:        "Editor.SearchPrevious",
		Area:        "Editor",
		Label:       "Search Backwards",
		LabelKey:    "Action.Editor.SearchPrevious",
		Description: "Continue search backwards",
		DescKey:     "Action.Editor.SearchPrevious.Desc",
		DefaultKeys: []string{"CtrlShiftEnter"},
		Handler:     withEditor(func(ev *editor.EditorView) { repeatEditorSearchDirection(ev, true) }),
	})

	registerAction(action.Action{
		Name:        "Editor.WordWrap",
		Area:        "Editor",
		Label:       "Word Wrap",
		LabelKey:    "Action.Editor.WordWrap",
		Description: "Toggle word wrap",
		DescKey:     "Action.Editor.WordWrap.Desc",
		DefaultKeys: []string{"F3"},
		MenuPath:    "Options",
		Checked:     editorState(func(ev *editor.EditorView) bool { return ev.WordWrap }),
		Handler: withEditor(func(ev *editor.EditorView) {
			if ev.WordWrapSuppressed {
				ev.WordWrap = false
				return
			}
			if !ev.WordWrap && ev.CurrentLineUnsafeForWordWrap() {
				ev.DisableUnsafeWordWrap()
				return
			}
			ev.SetWordWrap(!ev.WordWrap)
			ev.ScrollLeft = 0
			ev.ClearCaches()
			ev.EnsureCursorVisible()
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.HexMode",
		Area:        "Editor",
		Label:       "Hex Mode",
		LabelKey:    "Action.Editor.HexMode",
		Description: "Toggle hex view/edit",
		DescKey:     "Action.Editor.HexMode.Desc",
		DefaultKeys: []string{"F4"},
		MenuPath:    "Options",
		Checked:     editorState(func(ev *editor.EditorView) bool { return ev.HexMode || ev.DecodeMode }),
		Handler: withEditor(func(ev *editor.EditorView) {
			if !ev.HexMode && !ev.DecodeMode {
				ev.HexMode = true
				ev.HexTopOffset = (ev.Li.GetLineOffset(ev.CursorLine) + ev.CursorPos) &^ 0xF
				ev.HexNibble = 0
			} else if ev.HexMode {
				ev.HexMode = false
				ev.DecodeMode = true
				ev.HexTopOffset = ev.Li.GetLineOffset(ev.CursorLine) + ev.CursorPos
				ev.HexNibble = 0
			} else {
				ev.DecodeMode = false
				// Hex and decode address the buffer by byte offset, and a
				// file opened straight into them never ran the indexer, so
				// the cursor may be "line 0, column five million". Text mode
				// needs the real line, counted as far as the cursor before
				// it is shown; the scan then carries on from there.
				// Do not perform the initial index read synchronously here:
				// switching a large binary to text must leave the editor
				// responsive while the index is built in the background.
				ev.AwaitOffsetAsync(ev.Li.GetLineOffset(ev.CursorLine) + ev.CursorPos)
			}
			ev.EnsureCursorVisible()
			vtui.FrameManager.Redraw()
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.DisasmMode",
		Area:        "Editor",
		Label:       "Disassembler mode",
		LabelKey:    "Action.Editor.DisasmMode",
		Description: "Cycle the decode view between 64-, 32- and 16-bit x86 code",
		DescKey:     "Action.Editor.DisasmMode.Desc",
		DefaultKeys: []string{"ShiftF4"},
		MenuPath:    "Options",
		Handler: withEditor(func(ev *editor.EditorView) {
			// The mode is a property of the file, not of the view, so it
			// is switched wherever the editor is: the toast says what the
			// decode view will read the bytes as.
			mode := ev.CycleDisasmMode()
			toast.Show(fmt.Sprintf(i18n.Msg("Viewer.DisasmBits"), mode), time.Second)
			vtui.FrameManager.Redraw()
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.ShowWhitespaces",
		Area:        "Editor",
		Label:       "Show Whitespaces",
		LabelKey:    "Action.Editor.ShowWhitespaces",
		Description: "Toggle visible whitespaces",
		DescKey:     "Action.Editor.ShowWhitespaces.Desc",
		DefaultKeys: []string{"F5"},
		MenuPath:    "Options",
		Checked:     editorState(func(ev *editor.EditorView) bool { return ev.ShowWhitespaces }),
		Handler:     withEditor(func(ev *editor.EditorView) { ev.ShowWhitespaces = !ev.ShowWhitespaces }),
	})
	registerAction(action.Action{
		Name:        "Editor.CodepageNext",
		Area:        "Editor",
		Label:       "Next Codepage",
		LabelKey:    "Action.Editor.CodepageNext",
		Description: "Cycle to next codepage",
		DescKey:     "Action.Editor.CodepageNext.Desc",
		DefaultKeys: []string{"F8"},
		MenuPath:    "Options",
		Handler: withEditor(func(ev *editor.EditorView) {
			next := vfs.GetNextFastSwitchCodepage(ev.Codepage)
			fileops.SaveCodepageOverride(ev.Vfs, ev.FilePath, next)
			ev.ReloadWithCodepage(next)
			toast.Show(fmt.Sprintf("Codepage: %s", vfs.DisplayCodepageName(next)), time.Second)
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.CodepageMenu",
		Area:        "Editor",
		Label:       "Codepage Menu",
		LabelKey:    "Action.Editor.CodepageMenu",
		Description: "Select codepage",
		DescKey:     "Action.Editor.CodepageMenu.Desc",
		DefaultKeys: []string{"ShiftF8"},
		MenuPath:    "Options",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.ShowCodepageDialog() }),
	})
	registerAction(action.Action{
		Name:        "Editor.ConvertCodepage",
		Area:        "Editor",
		Label:       "Convert codepage...",
		LabelKey:    "Action.Editor.ConvertCodepage",
		Description: "Change the file codepage without modifying text",
		DescKey:     "Action.Editor.ConvertCodepage.Desc",
		MenuPath:    "Options",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.ShowConvertCodepageDialog() }),
	})

	registerAction(action.Action{
		Name:        "Editor.InsertLeftPanelPath",
		Area:        "Editor",
		Label:       "Insert Left Panel Path",
		LabelKey:    "Action.Editor.InsertLeftPanelPath",
		Description: "Insert the left panel's path at cursor",
		DescKey:     "Action.Editor.InsertLeftPanelPath.Desc",
		DefaultKeys: []string{"CtrlVK_DB"},
		MenuPath:    "Insert",
		Handler: withEditor(func(ev *editor.EditorView) {
			if s := panel.LeftPanelPathForEditor(); s != "" {
				ev.InsertTextAtCursor([]byte(s))
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.InsertRightPanelPath",
		Area:        "Editor",
		Label:       "Insert Right Panel Path",
		LabelKey:    "Action.Editor.InsertRightPanelPath",
		Description: "Insert the right panel's path at cursor",
		DescKey:     "Action.Editor.InsertRightPanelPath.Desc",
		DefaultKeys: []string{"CtrlVK_DD"},
		MenuPath:    "Insert",
		Handler: withEditor(func(ev *editor.EditorView) {
			if s := panel.RightPanelPathForEditor(); s != "" {
				ev.InsertTextAtCursor([]byte(s))
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.InsertActivePanelFileName",
		Area:        "Editor",
		Label:       "Insert Current File Name",
		LabelKey:    "Action.Editor.InsertActivePanelFileName",
		Description: "Insert the active panel's current file name at cursor",
		DescKey:     "Action.Editor.InsertActivePanelFileName.Desc",
		MenuPath:    "Insert",
		Handler: withEditor(func(ev *editor.EditorView) {
			if s := panel.ActivePanelNameForEditor(); s != "" {
				ev.InsertTextAtCursor([]byte(s))
			}
		}),
	})
	registerAction(action.Action{
		Name:        "Editor.DeleteSpacersForward",
		Area:        "Editor",
		Label:       "Delete Word Forward",
		LabelKey:    "Action.Editor.DeleteSpacersForward",
		Description: "Delete spaces and word forward",
		DescKey:     "Action.Editor.DeleteSpacersForward.Desc",
		DefaultKeys: []string{"CtrlDel"},
		MenuPath:    "Insert",
		Handler:     withEditor(func(ev *editor.EditorView) { ev.DeleteSpacersForward() }),
	})

	// --- Viewer actions ---
	registerAction(action.Action{
		Name:        "Viewer.SwitchToEditor",
		Area:        "Viewer",
		Label:       "Switch to Editor",
		LabelKey:    "Action.Viewer.SwitchToEditor",
		Description: "Switch to editor mode",
		DescKey:     "Action.Viewer.SwitchToEditor.Desc",
		DefaultKeys: []string{"F6"},
		MenuPath:    "File",
		Handler:     withViewer(func(vv *viewer.ViewerView) { actionSwitchViewerToEditor(vv) }),
	})
	registerAction(action.Action{
		Name:        "Viewer.Reload",
		Area:        "Viewer",
		Label:       "Reread File",
		LabelKey:    "Action.Viewer.Reload",
		Description: "Reread the file and follow it to the end if the end is on screen",
		DescKey:     "Action.Viewer.Reload.Desc",
		DefaultKeys: []string{"CtrlR"},
		MenuPath:    "File",
		Handler:     withViewer(func(vv *viewer.ViewerView) { vv.Reload() }),
	})
	registerAction(action.Action{
		Name:        "Viewer.Quit",
		Area:        "Viewer",
		Label:       "Quit",
		LabelKey:    "Action.Viewer.Quit",
		Description: "Close viewer",
		DescKey:     "Action.Viewer.Quit.Desc",
		DefaultKeys: []string{"Esc", "F10", "F3"},
		MenuPath:    "File",
		Handler:     withViewer(func(vv *viewer.ViewerView) { vv.Close() }),
	})

	registerAction(action.Action{
		Name:        "Viewer.WrapMode",
		Area:        "Viewer",
		Label:       "Wrap Mode",
		LabelKey:    "Action.Viewer.WrapMode",
		Description: "Toggle word wrap",
		DescKey:     "Action.Viewer.WrapMode.Desc",
		DefaultKeys: []string{"F2"},
		MenuPath:    "View",
		Checked:     viewerState(func(vv *viewer.ViewerView) bool { return vv.WrapMode }),
		Handler:     withViewer(func(vv *viewer.ViewerView) { vv.WrapMode = !vv.WrapMode }),
	})
	registerAction(action.Action{
		Name:        "Viewer.HexMode",
		Area:        "Viewer",
		Label:       "Hex Mode",
		LabelKey:    "Action.Viewer.HexMode",
		Description: "Toggle hex view",
		DescKey:     "Action.Viewer.HexMode.Desc",
		DefaultKeys: []string{"F4"},
		MenuPath:    "View",
		Checked:     viewerState(func(vv *viewer.ViewerView) bool { return vv.HexMode || vv.DecodeMode }),
		Handler: withViewer(func(vv *viewer.ViewerView) {
			// Whichever way this goes, the view mode is now the user's and
			// a later codepage switch must not second-guess it.
			vv.HexAuto = false
			if !vv.HexMode && !vv.DecodeMode {
				vv.HexMode = true
				vv.TopOffset &= ^int64(0xF)
			} else if vv.HexMode {
				vv.HexMode = false
				vv.DecodeMode = true
			} else {
				vv.DecodeMode = false
			}
			vtui.FrameManager.Redraw()
		}),
	})
	registerAction(action.Action{
		Name:        "Viewer.DisasmMode",
		Area:        "Viewer",
		Label:       "Disassembler mode",
		LabelKey:    "Action.Viewer.DisasmMode",
		Description: "Cycle the decode view between 64-, 32- and 16-bit x86 code",
		DescKey:     "Action.Viewer.DisasmMode.Desc",
		DefaultKeys: []string{"ShiftF4"},
		MenuPath:    "View",
		Handler: withViewer(func(vv *viewer.ViewerView) {
			mode := vv.CycleDisasmMode()
			toast.Show(fmt.Sprintf(i18n.Msg("Viewer.DisasmBits"), mode), time.Second)
			vtui.FrameManager.Redraw()
		}),
	})

	registerAction(action.Action{
		Name:        "Viewer.Search",
		Area:        "Viewer",
		Label:       "Search",
		LabelKey:    "Action.Viewer.Search",
		Description: "Find text",
		DescKey:     "Action.Viewer.Search.Desc",
		DefaultKeys: []string{"F7"},
		MenuPath:    "Search",
		Handler:     withViewer(func(vv *viewer.ViewerView) { vtui.FrameManager.EmitCommand(appcmd.CmSearch, nil) }),
	})
	registerAction(action.Action{
		Name:        "Viewer.SearchNext",
		Area:        "Viewer",
		Label:       "Search Next",
		LabelKey:    "Action.Viewer.SearchNext",
		Description: "Continue search forwards",
		DescKey:     "Action.Viewer.SearchNext.Desc",
		DefaultKeys: []string{"CtrlEnter"},
		MenuPath:    "Search",
		Handler:     withViewer(func(vv *viewer.ViewerView) { actionViewerSearchAgain(vv, false) }),
	})
	registerAction(action.Action{
		Name:        "Viewer.SearchPrevious",
		Area:        "Viewer",
		Label:       "Search Backwards",
		LabelKey:    "Action.Viewer.SearchPrevious",
		Description: "Continue search backwards",
		DescKey:     "Action.Viewer.SearchPrevious.Desc",
		DefaultKeys: []string{"CtrlShiftEnter"},
		MenuPath:    "Search",
		Handler:     withViewer(func(vv *viewer.ViewerView) { actionViewerSearchAgain(vv, true) }),
	})

	registerAction(action.Action{
		Name:        "Viewer.CodepageNext",
		Area:        "Viewer",
		Label:       "Next Codepage",
		LabelKey:    "Action.Viewer.CodepageNext",
		Description: "Cycle to next codepage",
		DescKey:     "Action.Viewer.CodepageNext.Desc",
		DefaultKeys: []string{"F8"},
		MenuPath:    "Options",
		Handler: withViewer(func(vv *viewer.ViewerView) {
			next := vfs.GetNextFastSwitchCodepage(vv.Codepage)
			fileops.SaveCodepageOverride(vv.VFS, vv.Path, next)
			vv.ReloadWithCodepage(next)
			toast.Show(fmt.Sprintf("Codepage: %s", vfs.DisplayCodepageName(next)), time.Second)
		}),
	})
	registerAction(action.Action{
		Name:        "Viewer.CodepageMenu",
		Area:        "Viewer",
		Label:       "Codepage Menu",
		LabelKey:    "Action.Viewer.CodepageMenu",
		Description: "Select codepage",
		DescKey:     "Action.Viewer.CodepageMenu.Desc",
		DefaultKeys: []string{"ShiftF8"},
		MenuPath:    "Options",
		Handler:     withViewer(func(vv *viewer.ViewerView) { vv.ShowCodepageDialog() }),
	})
	// The shell menu is not present while an Editor or Viewer owns the
	// workspace. Mirror the settings commands into those area menus so the
	// codepage defaults remain discoverable in the context where they apply.
	registerAction(action.Action{
		Name:        "Editor.Settings",
		Area:        "Editor",
		Label:       "Editor Settings",
		LabelKey:    "Menu.EditorSettings",
		Description: "Open editor settings dialog",
		DescKey:     "Action.Settings.Editor.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { actionEditorSettings(pf) }),
	})
	registerAction(action.Action{
		Name:        "Viewer.Settings",
		Area:        "Viewer",
		Label:       "Viewer Settings",
		LabelKey:    "Menu.ViewerSettings",
		Description: "Open viewer settings dialog",
		DescKey:     "Action.Settings.Viewer.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *panel.PanelsFrame) { dialog.ShowViewerSettings() }),
	})
}
