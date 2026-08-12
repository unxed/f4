package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Action represents a bindable command in the application.
//
// An action is the single source of truth for everything the user can
// trigger interactively: it is a macro command (by Name), a menu/keybar
// entry (Label/MenuPath), a help topic line (Description) and a default
// hotkey (DefaultKeys) at the same time.
type Action struct {
	Name        string // stable ID and macro command, e.g. "Editor.Save"
	Area        string // primary area: "Shell", "Editor", "Viewer", "Terminal", "Common"
	Label       string // English fallback for menu/keybar
	LabelKey    string // optional i18n key resolved via Msg()
	Description string // English fallback for help
	DescKey     string // optional i18n key resolved via Msg()
	// DefaultKeys are Far-style key names ("F2", "CtrlIns") with an
	// optional ":Condition" suffix per key (e.g. "Esc:EscToggle").
	DefaultKeys []string
	// DefaultAreas lists extra areas (besides Area) that get the default
	// bindings too (e.g. Panel.Toggle works in both Shell and Terminal).
	DefaultAreas []string
	// MenuPath is the top-level menu the action appears in ("File",
	// "Edit", ...). Empty means the action is not listed in menus.
	MenuPath string
	// MenuSeparatorBefore inserts a separator above this action's menu item.
	MenuSeparatorBefore bool
	// Checked, when set, reports the toggle state shown in menus ("√ ").
	Checked func() bool
	// Visible, when set, decides whether the action appears in a menu at
	// all. It is asked every time the menu is built, so it can depend on
	// what the panel is showing right now: an action the current file
	// system cannot perform is better left out than offered and refused.
	// It does not affect key bindings, which reach the handler regardless.
	Visible func() bool
	Handler func() bool
}

// DisplayLabel returns the localized label, falling back to the English one.
func (a Action) DisplayLabel() string {
	if a.LabelKey != "" {
		if s := Msg(a.LabelKey); !strings.HasPrefix(s, "{") {
			return s
		}
	}
	return a.Label
}

// DisplayDescription returns the localized description, falling back to English.
func (a Action) DisplayDescription() string {
	if a.DescKey != "" {
		if s := Msg(a.DescKey); !strings.HasPrefix(s, "{") {
			return s
		}
	}
	return a.Description
}

var actionRegistry = make(map[string]Action)

// actionOrder keeps registration order so generated menus are deterministic.
var actionOrder []string

// RegisterAction adds an action to the global registry.
func RegisterAction(action Action) {
	key := strings.ToLower(action.Name)
	if _, exists := actionRegistry[key]; !exists {
		actionOrder = append(actionOrder, key)
	}
	actionRegistry[key] = action
}

// RunAction executes an action by name if it exists.
func RunAction(name string) bool {
	if a, ok := actionRegistry[strings.ToLower(name)]; ok && a.Handler != nil {
		// Fast Find is a transient panel input mode. Any action means the user
		// is leaving it, including actions that replace a file panel in place
		// (Info/Quick View) and therefore do not push a focus-stealing frame.
		if pf := findPanelsFrame(); pf != nil && pf.cancelFastFind() && vtui.FrameManager != nil {
			vtui.FrameManager.Redraw()
		}
		return a.Handler()
	}
	return false
}

// GetActions returns a list of all registered actions, sorted by name.
func GetActions() []Action {
	var actions []Action
	for _, a := range actionRegistry {
		actions = append(actions, a)
	}
	sort.Slice(actions, func(i, j int) bool {
		return actions[i].Name < actions[j].Name
	})
	return actions
}

// GetOrderedActions returns all registered actions in registration order.
func GetOrderedActions() []Action {
	actions := make([]Action, 0, len(actionOrder))
	for _, key := range actionOrder {
		actions = append(actions, actionRegistry[key])
	}
	return actions
}

// GetAction returns an action by name.
func GetAction(name string) (Action, bool) {
	a, ok := actionRegistry[strings.ToLower(name)]
	return a, ok
}

// cursorOnParent reports whether the panel's cursor sits on the ".."
// (parent-directory) entry — used by the far2l Ins clipboard shortcuts
// that treat this position as the current folder itself.
func cursorOnParent(fsp *FileSystemPanel) bool {
	if fsp == nil {
		return false
	}
	idx := fsp.GetCursorIndex()
	return idx >= 0 && idx < len(fsp.entries) && fsp.entries[idx].Name == ".."
}

// currentPanelEntryPath returns the full path represented by the panel cursor.
// As in Far, the parent entry represents the current directory for path-copy
// and path-insertion commands.
func currentPanelEntryPath(fsp *FileSystemPanel) string {
	if fsp == nil || fsp.vfs == nil {
		return ""
	}
	idx := fsp.GetCursorIndex()
	if idx < 0 || idx >= len(fsp.entries) {
		return ""
	}
	base := fsp.vfs.GetPath()
	if fsp.entries[idx].Name == ".." {
		return base
	}
	return fsp.vfs.Join(base, fsp.entries[idx].Name)
}

// plainLabel strips hotkey markers ('&') from a menu label for contexts
// that cannot render them (keybar, plain lists). '&&' unescapes to '&'.
func plainLabel(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '&' {
			if i+1 < len(s) && s[i+1] == '&' {
				b.WriteByte('&')
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// isAIPanelActive returns true if the currently active panel is an AI panel.
// Used to dynamically show/hide specific menu items.
func isAIPanelActive() bool {
	pf := findPanelsFrameAnyScreen()
	if pf == nil {
		return false
	}
	fsp := pf.getActivePanel()
	if fsp == nil {
		return false
	}
	if tp, ok := fsp.vfs.(vfs.TitleProvider); ok {
		return tp.GetTitle() == "ai"
	}
	return false
}

func repeatEditorSearchDirection(ev *EditorView, reverse bool) {
	if LastEditorSearch == "" {
		return
	}
	rememberedDirection := LastEditorSearchReverse
	ev.Search(LastEditorSearch, LastEditorSearchCase, reverse, LastEditorSearchRegexp, LastEditorSearchWholeWord, true)
	LastEditorSearchReverse = rememberedDirection
}

func init() {
	withPF := func(fn func(pf *PanelsFrame)) func() bool {
		return func() bool {
			if pf := findPanelsFrameAnyScreen(); pf != nil {
				fn(pf)
				return true
			}
			return false
		}
	}

	withEditor := func(fn func(ev *EditorView)) func() bool {
		return func() bool {
			if vtui.FrameManager == nil {
				return false
			}
			if ev, ok := vtui.FrameManager.GetTopFrame().(*EditorView); ok {
				fn(ev)
				return true
			}
			return false
		}
	}

	withViewer := func(fn func(vv *ViewerView)) func() bool {
		return func() bool {
			if vtui.FrameManager == nil {
				return false
			}
			if vv, ok := vtui.FrameManager.GetTopFrame().(*ViewerView); ok {
				fn(vv)
				return true
			}
			return false
		}
	}

	editorState := func(fn func(ev *EditorView) bool) func() bool {
		return func() bool {
			if vtui.FrameManager == nil {
				return false
			}
			if ev, ok := vtui.FrameManager.GetTopFrame().(*EditorView); ok {
				return fn(ev)
			}
			return false
		}
	}

	viewerState := func(fn func(vv *ViewerView) bool) func() bool {
		return func() bool {
			if vtui.FrameManager == nil {
				return false
			}
			if vv, ok := vtui.FrameManager.GetTopFrame().(*ViewerView); ok {
				return fn(vv)
			}
			return false
		}
	}

	// --- Common actions (available in every area) ---
	RegisterAction(Action{
		Name:        "App.ScreenGrab",
		Area:        "Common",
		Label:       "Screen Grab",
		LabelKey:    "Action.App.ScreenGrab",
		Description: "Select and copy a screen region",
		DescKey:     "Action.App.ScreenGrab.Desc",
		DefaultKeys: []string{"AltIns"},
		MenuPath:    "File",
		Handler:     func() bool { OpenGrabber(); return true },
	})

	// --- Shell (panels) actions ---
	// Registration order defines the menu order inside each top-level menu.
	RegisterAction(Action{
		Name:        "File.View",
		Area:        "Shell",
		Label:       "View",
		LabelKey:    "Menu.Files.View",
		Description: "Open file in viewer",
		DescKey:     "Action.File.View.Desc",
		DefaultKeys: []string{"F3"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *PanelsFrame) { actionViewFile(pf) }),
	})
	RegisterAction(Action{
		Name:        "File.Edit",
		Area:        "Shell",
		Label:       "Edit",
		LabelKey:    "Menu.Files.Edit",
		Description: "Open file in editor",
		DescKey:     "Action.File.Edit.Desc",
		DefaultKeys: []string{"F4"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *PanelsFrame) { actionEditFile(pf) }),
	})
	RegisterAction(Action{
		Name:         "File.New",
		Area:         "Shell",
		Label:        "New File",
		LabelKey:     "Action.File.New",
		Description:  "Create and open a new file in editor",
		DescKey:      "Action.File.New.Desc",
		DefaultKeys:  []string{"ShiftF4:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		MenuPath:     "Files",
		Handler:      withPF(func(pf *PanelsFrame) { actionNewFile(pf) }),
	})
	RegisterAction(Action{
		Name:        "File.ApplyCommand",
		Area:        "Shell",
		Label:       "Apply command",
		LabelKey:    "Action.File.ApplyCommand",
		Description: "Apply a command template to selected files or the current file",
		DescKey:     "Action.File.ApplyCommand.Desc",
		DefaultKeys: []string{"CtrlG"},
		MenuPath:    "Files",
		Visible:     panelCanApplyCommand,
		Handler: func() bool {
			if pf := findPanelsFrame(); pf != nil {
				actionApplyCommand(pf)
				return true
			}
			return false
		},
	})
	RegisterAction(Action{
		Name:        "File.Copy",
		Area:        "Shell",
		Label:       "Copy",
		LabelKey:    "Menu.Files.Copy",
		Description: "Copy selected files or current file",
		DescKey:     "Action.File.Copy.Desc",
		DefaultKeys: []string{"F5"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *PanelsFrame) { actionCopyMove(pf, false) }),
	})
	RegisterAction(Action{
		Name:        "File.CopyInPlace",
		Area:        "Shell",
		Label:       "Copy In Place",
		LabelKey:    "Action.File.CopyInPlace",
		Description: "Copy file under a new name in the same directory",
		DescKey:     "Action.File.CopyInPlace.Desc",
		DefaultKeys: []string{"ShiftF5"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *PanelsFrame) { actionCopyInPlace(pf) }),
	})
	RegisterAction(Action{
		Name:        "File.Move",
		Area:        "Shell",
		Label:       "Move",
		LabelKey:    "Menu.Files.RenMov",
		Description: "Rename or move selected files",
		DescKey:     "Action.File.Move.Desc",
		DefaultKeys: []string{"F6"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *PanelsFrame) { actionCopyMove(pf, true) }),
	})
	RegisterAction(Action{
		Name:        "File.Rename",
		Area:        "Shell",
		Label:       "Rename",
		LabelKey:    "Action.File.Rename",
		Description: "Rename current file",
		DescKey:     "Action.File.Rename.Desc",
		DefaultKeys: []string{"ShiftF6"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *PanelsFrame) { actionRename(pf) }),
	})
	RegisterAction(Action{
		Name:         "File.MakeDir",
		Area:         "Shell",
		Label:        "Make Folder",
		LabelKey:     "Menu.Files.MkDir",
		Description:  "Create a new directory",
		DescKey:      "Action.File.MakeDir.Desc",
		DefaultKeys:  []string{"F7:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		MenuPath:     "Files",
		Handler:      withPF(func(pf *PanelsFrame) { actionMkDir(pf) }),
	})
	RegisterAction(Action{
		Name:        "File.Delete",
		Area:        "Shell",
		Label:       "Delete",
		LabelKey:    "Menu.Files.Delete",
		Description: "Delete selected files",
		DescKey:     "Action.File.Delete.Desc",
		DefaultKeys: []string{"F8"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *PanelsFrame) { actionDelete(pf) }),
	})
	RegisterAction(Action{
		Name:        "File.DeletePermanent",
		Area:        "Shell",
		Label:       "Delete permanently",
		LabelKey:    "Action.File.DeletePermanent",
		Description: "Permanently delete selected files without using trash",
		DescKey:     "Action.File.DeletePermanent.Desc",
		DefaultKeys: []string{"ShiftDel", "ShiftNumDel"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *PanelsFrame) { actionDeletePermanent(pf) }),
	})
	RegisterAction(Action{
		Name:        "File.Attributes",
		Area:        "Shell",
		Label:       "File Attributes",
		LabelKey:    "Action.File.Attributes",
		Description: "View and change file attributes",
		DescKey:     "Action.File.Attributes.Desc",
		DefaultKeys: []string{"CtrlA"},
		MenuPath:    "Files",
		Handler:     withPF(func(pf *PanelsFrame) { actionFileAttributes(pf) }),
	})
	RegisterAction(Action{
		Name:        "File.Share",
		Area:        "Shell",
		Label:       "Share...",
		LabelKey:    "Action.File.Share",
		Description: "Create, copy, or revoke a cloud share link",
		DescKey:     "Action.File.Share.Desc",
		MenuPath:    "Files",
		Visible: func() bool {
			pf := findPanelsFrame()
			if pf == nil {
				return false
			}
			panel := pf.getActivePanel()
			if panel == nil || panel.vfs == nil {
				return false
			}
			_, ok := panel.vfs.(vfs.ShareLinkProvider)
			return ok
		},
		Handler: withPF(func(pf *PanelsFrame) { actionShareLink(pf) }),
	})
	RegisterAction(Action{
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
		Handler: withPF(func(pf *PanelsFrame) {
			fsp := pf.getActivePanel()
			if fsp == nil {
				return
			}
			idx := fsp.GetCursorIndex()
			if idx < 0 || idx >= len(fsp.entries) {
				return
			}
			name := fsp.entries[idx].Name
			var fullPath string
			if name == ".." {
				fullPath = fsp.vfs.GetPath()
			} else {
				fullPath = fsp.vfs.Join(fsp.vfs.GetPath(), name)
			}
			if _, isLocal := fsp.vfs.(*vfs.OSVFS); isLocal {
				// Capture entry state before spawning: the entries
				// slice may be replaced by a refresh at any time.
				isDir := fsp.entries[idx].IsDir || name == ".."
				go func() {
					command, args, ok := systemFileManagerCommand(fullPath, isDir)
					if ok {
						_ = pf.runExternalUICommand(command, args, "")
					}
				}()
			} else {
				vtui.ShowMessage(" Error ", "Cannot open remote paths in system explorer.", []string{"&Ok"})
			}
		}),
	})

	RegisterAction(Action{
		Name:        "Panel.SelectGroup",
		Area:        "Shell",
		Label:       "Select Group",
		LabelKey:    "Action.Panel.SelectGroup",
		Description: "Select files by mask",
		DescKey:     "Action.Panel.SelectGroup.Desc",
		DefaultKeys: []string{"Add"},
		MenuPath:    "Files",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				vtui.InputBox(Msg("Select.Title"), Msg("Select.Mask"), "*", func(mask string) {
					fsp.ApplyMaskSelection(mask, true)
				})
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.DeselectGroup",
		Area:        "Shell",
		Label:       "Deselect Group",
		LabelKey:    "Action.Panel.DeselectGroup",
		Description: "Deselect files by mask",
		DescKey:     "Action.Panel.DeselectGroup.Desc",
		DefaultKeys: []string{"Subtract"},
		MenuPath:    "Files",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				vtui.InputBox(Msg("Deselect.Title"), Msg("Select.Mask"), "*", func(mask string) {
					fsp.ApplyMaskSelection(mask, false)
				})
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.InvertSelection",
		Area:        "Shell",
		Label:       "Invert Selection",
		LabelKey:    "Action.Panel.InvertSelection",
		Description: "Invert file selection",
		DescKey:     "Action.Panel.InvertSelection.Desc",
		DefaultKeys: []string{"Multiply"},
		MenuPath:    "Files",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				fsp.InvertSelection()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.RestoreSelection",
		Area:        "Shell",
		Label:       "Restore Selection",
		LabelKey:    "Action.Panel.RestoreSelection",
		Description: "Restore the previous selection state",
		DescKey:     "Action.Panel.RestoreSelection.Desc",
		DefaultKeys: []string{"CtrlM"},
		MenuPath:    "Files",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				fsp.RestoreSelection()
			}
		}),
	})

	RegisterAction(Action{
		Name:         "Panel.UserMenu",
		Area:         "Shell",
		Label:        "User Menu",
		LabelKey:     "Action.Panel.UserMenu",
		Description:  "Show the user menu",
		DescKey:      "Action.Panel.UserMenu.Desc",
		DefaultKeys:  []string{"F2:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		MenuPath:     "Commands",
		Handler:      withPF(func(pf *PanelsFrame) { ShowUserMenu(pf) }),
	})
	RegisterAction(Action{
		Name:        "Panel.FileAssociations",
		Area:        "Shell",
		Label:       "File Associations…",
		LabelKey:    "Action.Panel.FileAssociations",
		Description: "Configure per-mask commands for Enter, F3 and F4",
		DescKey:     "Action.Panel.FileAssociations.Desc",
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *PanelsFrame) { ShowFileAssociations(pf) }),
	})
	RegisterAction(Action{
		Name:        "File.Find",
		Area:        "Shell",
		Label:       "Find File",
		LabelKey:    "Menu.Commands.FindFile",
		Description: "Search for files",
		DescKey:     "Action.File.Find.Desc",
		DefaultKeys: []string{"AltF7"},
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *PanelsFrame) { actionFindFile(pf) }),
	})
	RegisterAction(Action{
		Name:        "File.FindDuplicates",
		Area:        "Shell",
		Label:       "Find Duplicates",
		LabelKey:    "Menu.Commands.FindDuplicates",
		Description: "Find files with identical content",
		DescKey:     "Action.File.FindDuplicates.Desc",
		MenuPath:    "Commands",
		Visible:     panelCanFindDuplicates,
		Handler:     withPF(func(pf *PanelsFrame) { actionFindDuplicates(pf) }),
	})
	RegisterAction(Action{
		Name:        "File.RunRemoteCommand",
		Area:        "Shell",
		Label:       "Run Command Remotely",
		LabelKey:    "Menu.Commands.RunRemote",
		Description: "Run a command on the host the panel is showing",
		DescKey:     "Action.File.RunRemoteCommand.Desc",
		MenuPath:    "Commands",
		Visible:     panelCanRunCommand,
		Handler:     withPF(func(pf *PanelsFrame) { actionRunRemoteCommand(pf) }),
	})
	RegisterAction(Action{
		Name:        "Panel.BackgroundJobs",
		Area:        "Shell",
		Label:       "Background Jobs",
		LabelKey:    "Menu.Commands.BackgroundJobs",
		Description: "Show work still running and results waiting to be seen",
		DescKey:     "Action.Panel.BackgroundJobs.Desc",
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *PanelsFrame) { ShowBackgroundJobs(pf) }),
	})
	RegisterAction(Action{
		Name:        "Panel.Bookmarks",
		Area:        "Shell",
		Label:       "Bookmarks",
		LabelKey:    "Menu.Commands.Bookmarks",
		Description: "Show folder bookmarks dialog",
		DescKey:     "Action.Panel.Bookmarks.Desc",
		DefaultKeys: []string{"CtrlShiftVK_DC"},
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *PanelsFrame) { ShowBookmarksDialog(pf) }),
	})
	RegisterAction(Action{
		Name:        "Panel.PluginMenu",
		Area:        "Shell",
		Label:       "Plugin Commands",
		LabelKey:    "Action.Panel.PluginMenu",
		Description: "Show plugin commands menu",
		DescKey:     "Action.Panel.PluginMenu.Desc",
		DefaultKeys: []string{"F11"},
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *PanelsFrame) { pf.showPluginMenu() }),
	})
	RegisterAction(Action{
		Name:        "Panel.CommandHistory",
		Area:        "Shell",
		Label:       "Command History",
		LabelKey:    "Action.Panel.CommandHistory",
		Description: "Show command line history",
		DescKey:     "Action.Panel.CommandHistory.Desc",
		DefaultKeys: []string{"AltF8"},
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *PanelsFrame) { actionCommandHistory(pf) }),
	})
	RegisterAction(Action{
		Name:        "Panel.FoldersHistory",
		Area:        "Shell",
		Label:       "Folders History",
		LabelKey:    "Action.Panel.FoldersHistory",
		Description: "Show folders history",
		DescKey:     "Action.Panel.FoldersHistory.Desc",
		DefaultKeys: []string{"AltF12"},
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *PanelsFrame) { actionFoldersHistory(pf) }),
	})
	RegisterAction(Action{
		Name:        "Panel.ViewerEditorHistory",
		Area:        "Shell",
		Label:       "Viewer and Editor History",
		LabelKey:    "Action.Panel.ViewerEditorHistory",
		Description: "Show viewer and editor history",
		DescKey:     "Action.Panel.ViewerEditorHistory.Desc",
		DefaultKeys: []string{"AltF11"},
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *PanelsFrame) { actionViewerEditorHistory(pf) }),
	})
	RegisterAction(Action{
		Name:        "History.ImportFar2l",
		Area:        "Shell",
		Label:       "Import far2l History",
		Description: "Import command history from far2l (.hst)",
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *PanelsFrame) { actionImportFar2lHistory(pf) }),
	})
	RegisterAction(Action{
		Name:        "Panel.GoParent",
		Area:        "Shell",
		Label:       "Parent Folder",
		LabelKey:    "Action.Panel.GoParent",
		Description: "Go to parent directory",
		DescKey:     "Action.Panel.GoParent.Desc",
		DefaultKeys: []string{"CtrlPgUp"},
		MenuPath:    "Commands",
		Handler: withPF(func(pf *PanelsFrame) {
			fsp := pf.getActivePanel()
			if fsp == nil {
				return
			}
			if fsp.vfs.IsAtRoot() {
				if fsp.vfs.ParentVFS() == nil {
					pf.showDriveMenu(pf.activeIdx)
				} else {
					// Exit an archive or a NetFox connection to the parent VFS
					pf.NavigateToPath(fsp, "..")
				}
			} else {
				oldPath := fsp.vfs.GetPath()
				parentPath := fsp.vfs.Dir(oldPath)
				if err := fsp.setKnownDirectoryPath(parentPath); err == nil {
					fsp.pendingSelection = fsp.vfs.Base(oldPath)
					fsp.ReadDirectory()
				} else {
					pf.NavigateToPath(fsp, "..")
				}
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.GoRoot",
		Area:        "Shell",
		Label:       "Root Folder",
		LabelKey:    "Action.Panel.GoRoot",
		Description: "Go to the filesystem root",
		DescKey:     "Action.Panel.GoRoot.Desc",
		DefaultKeys: []string{"CtrlVK_DC"},
		MenuPath:    "Commands",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				rootPath := "/"
				if runtime.GOOS == "windows" {
					rootPath = string(os.PathSeparator)
					if _, isOS := fsp.vfs.(*vfs.OSVFS); isOS {
						rootPath = filepath.VolumeName(fsp.vfs.GetPath()) + string(os.PathSeparator)
					}
				}
				pf.NavigateToPath(fsp, rootPath)
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.HistoryBack",
		Area:        "Shell",
		Label:       "History Back",
		LabelKey:    "Action.Panel.HistoryBack",
		Description: "Move backward through folders history",
		DescKey:     "Action.Panel.HistoryBack.Desc",
		DefaultKeys: []string{"AltLeft"},
		MenuPath:    "Commands",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				pf.moveFolderHistory(fsp, -1)
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.HistoryForward",
		Area:        "Shell",
		Label:       "History Forward",
		LabelKey:    "Action.Panel.HistoryForward",
		Description: "Move forward through folders history",
		DescKey:     "Action.Panel.HistoryForward.Desc",
		DefaultKeys: []string{"AltRight"},
		MenuPath:    "Commands",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				pf.moveFolderHistory(fsp, 1)
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.CopyPath",
		Area:        "Shell",
		Label:       "Copy Path",
		LabelKey:    "Action.Panel.CopyPath",
		Description: "Copy the full path of the current file to clipboard",
		DescKey:     "Action.Panel.CopyPath.Desc",
		DefaultKeys: []string{"CtrlD"},
		MenuPath:    "Commands",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				if path := currentPanelEntryPath(fsp); path != "" {
					vtui.SetClipboard(path)
				}
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.InsertPath",
		Area:        "Shell",
		Label:       "Insert Path into Command Line",
		LabelKey:    "Action.Panel.InsertPath",
		Description: "Insert the full path of the current file into the command line",
		DescKey:     "Action.Panel.InsertPath.Desc",
		DefaultKeys: []string{"CtrlF"},
		MenuPath:    "Commands",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				pf.insertPathToCmdLine(currentPanelEntryPath(fsp))
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.CopyName",
		Area:        "Shell",
		Label:       "Copy Name",
		LabelKey:    "Action.Panel.CopyName",
		Description: "Copy the current file name to clipboard",
		DescKey:     "Action.Panel.CopyName.Desc",
		DefaultKeys: []string{"CtrlIns:EmptyCommandLine"},
		MenuPath:    "Commands",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				idx := fsp.GetCursorIndex()
				if idx < 0 || idx >= len(fsp.entries) {
					return
				}
				name := fsp.entries[idx].Name
				if name == ".." {
					// far2l docs: with the cursor on ".." this hotkey
					// treats it as the name of the current folder.
					// Mirrors far2l's PointToName(GetCurDir()) branch
					// in FileList::CopyNames() (FullPathName=false).
					name = fsp.vfs.Base(fsp.vfs.GetPath())
				}
				vtui.SetClipboard(name)
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.CopySelectedNames",
		Area:        "Shell",
		Label:       "Copy Selected Names",
		LabelKey:    "Action.Panel.CopySelectedNames",
		Description: "Copy names of selected files to clipboard",
		DescKey:     "Action.Panel.CopySelectedNames.Desc",
		DefaultKeys: []string{"CtrlShiftIns"},
		MenuPath:    "Commands",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				if names := fsp.GetSelectedNames(); len(names) > 0 {
					// SetClipboard can block up to ~4s on far2l IPC or
					// while shelling out to xclip/wl-copy — do it off the
					// UI goroutine (matches Grabber's copyAndExit).
					go vtui.SetClipboard(strings.Join(names, "\n"))
				}
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.CopySelectedPaths",
		Area:        "Shell",
		Label:       "Copy Selected Paths",
		LabelKey:    "Action.Panel.CopySelectedPaths",
		Description: "Copy full paths of selected files to clipboard",
		DescKey:     "Action.Panel.CopySelectedPaths.Desc",
		DefaultKeys: []string{"AltShiftIns"},
		MenuPath:    "Commands",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				base := fsp.vfs.GetPath()
				names := fsp.GetSelectedNames()
				if len(names) == 0 {
					// far2l note: with the cursor on ".." this action
					// treats it as the name of the current folder.
					if cursorOnParent(fsp) {
						go vtui.SetClipboard(base)
					}
					return
				}
				paths := make([]string, 0, len(names))
				for _, n := range names {
					paths = append(paths, fsp.vfs.Join(base, n))
				}
				go vtui.SetClipboard(strings.Join(paths, "\n"))
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.CopySelectedRealPaths",
		Area:        "Shell",
		Label:       "Copy Selected Real Paths",
		LabelKey:    "Action.Panel.CopySelectedRealPaths",
		Description: "Copy full paths of selected files with symlink resolution",
		DescKey:     "Action.Panel.CopySelectedRealPaths.Desc",
		DefaultKeys: []string{"CtrlAltIns"},
		MenuPath:    "Commands",
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.getActivePanel(); fsp != nil {
				base := fsp.vfs.GetPath()
				_, isOS := fsp.vfs.(*vfs.OSVFS)
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
						go vtui.SetClipboard(resolve(base))
					}
					return
				}
				paths := make([]string, 0, len(names))
				for _, n := range names {
					paths = append(paths, resolve(fsp.vfs.Join(base, n)))
				}
				go vtui.SetClipboard(strings.Join(paths, "\n"))
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.SortMenu",
		Area:        "Shell",
		Label:       "Sort Modes",
		LabelKey:    "Action.Panel.SortMenu",
		Description: "Show sort modes menu",
		DescKey:     "Action.Panel.SortMenu.Desc",
		DefaultKeys: []string{"CtrlF12"},
		MenuPath:    "Commands",
		Handler:     withPF(func(pf *PanelsFrame) { actionSortMenu(pf) }),
	})

	RegisterAction(Action{
		Name:        "Settings.Language",
		Area:        "Shell",
		Label:       "Language",
		LabelKey:    "Menu.Language",
		Description: "Open language selection dialog",
		DescKey:     "Action.Settings.Language.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *PanelsFrame) { actionLanguage(pf) }),
	})
	RegisterAction(Action{
		Name:        "Settings.HelpLanguage",
		Area:        "Shell",
		Label:       "Help Language",
		LabelKey:    "Menu.HelpLanguage",
		Description: "Open help language selection dialog",
		DescKey:     "Action.Settings.HelpLanguage.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *PanelsFrame) { actionHelpLanguage(pf) }),
	})
	RegisterAction(Action{
		Name:                "Settings.Panel",
		Area:                "Shell",
		Label:               "Panel Settings",
		LabelKey:            "Menu.PanelSettings",
		Description:         "Open panel settings dialog",
		DescKey:             "Action.Settings.Panel.Desc",
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *PanelsFrame) { actionPanelSettings(pf) }),
	})
	RegisterAction(Action{
		Name:        "Settings.Editor",
		Area:        "Shell",
		Label:       "Editor Settings",
		LabelKey:    "Menu.EditorSettings",
		Description: "Open editor settings dialog",
		DescKey:     "Action.Settings.Editor.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *PanelsFrame) { actionEditorSettings(pf) }),
	})
	RegisterAction(Action{
		Name:        "Settings.Colorer",
		Area:        "Shell",
		Label:       "Colorer Settings",
		LabelKey:    "Menu.ColorerSettings",
		Description: "Open Colorer settings dialog",
		DescKey:     "Action.Settings.Colorer.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *PanelsFrame) { actionColorerSettings(pf) }),
	})
	RegisterAction(Action{
		Name:        "Settings.Appearance",
		Area:        "Shell",
		Label:       "Appearance Settings",
		LabelKey:    "Menu.AppearanceSettings",
		Description: "Open appearance settings dialog",
		DescKey:     "Action.Settings.Appearance.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *PanelsFrame) { actionAppearanceSettings(pf) }),
	})
	RegisterAction(Action{
		Name:        "Settings.Confirmations",
		Area:        "Shell",
		Label:       "Confirmations Settings",
		LabelKey:    "Menu.ConfirmationsSettings",
		Description: "Open confirmations settings dialog",
		DescKey:     "Action.Settings.Confirmations.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *PanelsFrame) { actionConfirmationsSettings(pf) }),
	})
	RegisterAction(Action{
		Name:        "Settings.MouseWheel",
		Area:        "Shell",
		Label:       "Mouse Wheel Settings",
		LabelKey:    "Menu.MouseWheelSettings",
		Description: "Open mouse wheel scroll speed settings dialog",
		DescKey:     "Action.Settings.MouseWheel.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *PanelsFrame) { actionMouseWheelSettings(pf) }),
	})
	RegisterAction(Action{
		Name:        "Settings.PathHints",
		Area:        "Shell",
		Label:       "Path Hints Settings",
		LabelKey:    "Menu.PathHintSettings",
		Description: "Open path hints settings dialog",
		DescKey:     "Action.Settings.PathHints.Desc",
		MenuPath:    "Options",
		Handler:     withPF(func(pf *PanelsFrame) { actionPathHintSettings(pf) }),
	})
	RegisterAction(Action{
		Name:                "Settings.Hotkeys",
		Area:                "Shell",
		Label:               "Hotkey Configuration",
		LabelKey:            "Action.Settings.Hotkeys",
		Description:         "Open Hotkey Configurator",
		DescKey:             "Action.Settings.Hotkeys.Desc",
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *PanelsFrame) { actionHotkeyConfig(pf) }),
	})
	RegisterAction(Action{
		Name:                "Settings.AutoUpdate",
		Area:                "Shell",
		Label:               "Auto Update Settings",
		LabelKey:            "Menu.AutoUpdateSettings",
		Description:         "Open auto update settings dialog",
		DescKey:             "Action.Settings.AutoUpdate.Desc",
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *PanelsFrame) { vtui.FrameManager.EmitCommand(CmUpdateSettings, nil) }),
	})
	RegisterAction(Action{
		Name:                "Settings.PluginConfiguration",
		Area:                "Shell",
		Label:               "Plugin Configuration",
		LabelKey:            "Menu.PluginConfiguration",
		Description:         "Configure loaded plugins",
		DescKey:             "Action.Settings.PluginConfiguration.Desc",
		DefaultKeys:         []string{"ShiftF11"},
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *PanelsFrame) { actionPluginConfiguration(pf) }),
	})
	RegisterAction(Action{
		Name:                "Settings.Plugins",
		Area:                "Shell",
		Label:               "Plugins Menu",
		LabelKey:            "Menu.Options.Plugins",
		Description:         "Manage plugins dialog",
		DescKey:             "Action.Settings.Plugins.Desc",
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *PanelsFrame) { actionManagePlugins(pf) }),
	})
	RegisterAction(Action{
		Name:                "App.PlugRing",
		Area:                "Shell",
		Label:               "f4 PlugRing",
		LabelKey:            "Action.App.PlugRing",
		Description:         "Open the PlugRing plugin catalog",
		DescKey:             "Action.App.PlugRing.Desc",
		MenuPath:            "Options",
		MenuSeparatorBefore: true,
		Handler:             withPF(func(pf *PanelsFrame) { vtui.FrameManager.EmitCommand(CmPlugRing, nil) }),
	})
	RegisterAction(Action{
		Name:         "App.SaveSettings",
		Area:         "Shell",
		Label:        "Save Settings",
		LabelKey:     "Action.App.SaveSettings",
		Description:  "Save settings and session",
		DescKey:      "Action.App.SaveSettings.Desc",
		DefaultKeys:  []string{"ShiftF9:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		MenuPath:     "Options",
		Handler: withPF(func(pf *PanelsFrame) {
			SaveConfig()
			SaveSession()
			vtui.ShowToast("Settings saved", 2*time.Second)
		}),
	})
	RegisterAction(Action{
		Name:        "App.ToggleWindowSize",
		Area:        "Shell",
		Label:       "Toggle Window Size",
		LabelKey:    "Action.App.ToggleWindowSize",
		Description: "Toggle between two window sizes",
		DescKey:     "Action.App.ToggleWindowSize.Desc",
		DefaultKeys: []string{"AltF9"},
		MenuPath:    "Options",
		Handler: withPF(func(pf *PanelsFrame) {
			targetCols, targetRows := AppConfig.GuiCols, AppConfig.GuiRows
			if pf.lastW == AppConfig.GuiCols && pf.lastH == AppConfig.GuiRows {
				targetCols, targetRows = AppConfig.GuiCols+40, AppConfig.GuiRows+15
			}
			// xterm resize sequence for console mode
			os.Stdout.WriteString(fmt.Sprintf("\x1b[8;%d;%dt", targetRows, targetCols))
			os.Stdout.Sync()
			// Forced OS window resize for GUI mode
			if vtui.FrameManager != nil {
				vtui.FrameManager.ResizeWindow(targetCols, targetRows)
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.ToggleKeyBar",
		Area:        "Shell",
		Label:       "Toggle KeyBar",
		LabelKey:    "Action.Panel.ToggleKeyBar",
		Description: "Show or hide the KeyBar",
		DescKey:     "Action.Panel.ToggleKeyBar.Desc",
		DefaultKeys: []string{"CtrlB"},
		MenuPath:    "Options",
		Handler: withPF(func(pf *PanelsFrame) {
			pf.showKeyBar = !pf.showKeyBar
			pf.ResizeConsole(pf.lastW, pf.lastH)
		}),
	})

	// --- Shell key-only actions (no menu entries) ---
	RegisterAction(Action{
		Name:        "Panel.Rescan",
		Area:        "Shell",
		Label:       "Rescan",
		Description: "Refresh panel contents",
		DescKey:     "Action.Panel.Rescan.Desc",
		DefaultKeys: []string{"CtrlR"},
		Handler:     withPF(func(pf *PanelsFrame) { pf.RefreshAll() }),
	})
	RegisterAction(Action{
		Name:        "Panel.Swap",
		Area:        "Shell",
		Label:       "Swap Panels",
		Description: "Swap left and right panels",
		DescKey:     "Action.Panel.Swap.Desc",
		DefaultKeys: []string{"CtrlU"},
		Handler:     withPF(func(pf *PanelsFrame) { vtui.FrameManager.EmitCommand(CmSwapPanels, nil) }),
	})
	RegisterAction(Action{
		Name:         "Panel.Toggle",
		Area:         "Shell",
		Label:        "Toggle Panels",
		Description:  "Show or hide panels",
		DescKey:      "Action.Panel.Toggle.Desc",
		DefaultKeys:  []string{"CtrlO:NoAltScreenApp", "Esc:EscToggle", "Del:EscToggle", "NumDel:EscToggle"},
		DefaultAreas: []string{"Terminal"},
		Handler: withPF(func(pf *PanelsFrame) {
			pf.showPanels = !pf.showPanels
			if pf.showPanels && !pf.showLeftPanel && !pf.showRightPanel {
				pf.showLeftPanel = true
				pf.showRightPanel = true
			}
			if pf.menuBar != nil && pf.lastW > 0 && pf.lastH > 0 {
				pf.ResizeConsole(pf.lastW, pf.lastH)
				pf.lastShowPanels = pf.showPanels
			}
			vtui.FrameManager.HardRefresh()
			if pf.showPanels {
				pf.RefreshAll()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.ToggleLeftPanel",
		Area:        "Shell",
		Label:       "Toggle Left Panel",
		Description: "Show or hide the left panel",
		DescKey:     "Action.Panel.ToggleLeftPanel.Desc",
		DefaultKeys: []string{"CtrlF1"},
		Handler: withPF(func(pf *PanelsFrame) {
			pf.exitWide()
			pf.showLeftPanel = !pf.showLeftPanel
			pf.showPanels = pf.showLeftPanel || pf.showRightPanel
			if !pf.showLeftPanel && pf.showPanels {
				pf.activeIdx = 1
			}
			vtui.FrameManager.HardRefresh()
			if pf.showPanels {
				pf.RefreshAll()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.ToggleRightPanel",
		Area:        "Shell",
		Label:       "Toggle Right Panel",
		Description: "Show or hide the right panel",
		DescKey:     "Action.Panel.ToggleRightPanel.Desc",
		DefaultKeys: []string{"CtrlF2"},
		Handler: withPF(func(pf *PanelsFrame) {
			pf.exitWide()
			pf.showRightPanel = !pf.showRightPanel
			pf.showPanels = pf.showLeftPanel || pf.showRightPanel
			if !pf.showRightPanel && pf.showPanels {
				pf.activeIdx = 0
			}
			vtui.FrameManager.HardRefresh()
			if pf.showPanels {
				pf.RefreshAll()
			}
		}),
	})
	RegisterAction(Action{
		Name:         "Panel.TogglePassivePanel",
		Area:         "Shell",
		Label:        "Toggle Passive Panel",
		Description:  "Show or hide the passive panel",
		DescKey:      "Action.Panel.TogglePassivePanel.Desc",
		DefaultKeys:  []string{"CtrlP:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		Handler: withPF(func(pf *PanelsFrame) {
			pf.exitWide()
			if pf.activeIdx == 0 {
				pf.showRightPanel = !pf.showRightPanel
			} else {
				pf.showLeftPanel = !pf.showLeftPanel
			}
			pf.showPanels = pf.showLeftPanel || pf.showRightPanel
			vtui.FrameManager.HardRefresh()
			if pf.showPanels {
				pf.RefreshAll()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.InfoPanel",
		Area:        "Shell",
		Label:       "Info Panel",
		Description: "Toggle the info panel",
		DescKey:     "Action.Panel.InfoPanel.Desc",
		DefaultKeys: []string{"CtrlL"},
		Handler: withPF(func(pf *PanelsFrame) {
			pf.toggleAltPanel("info", func(src *FileSystemPanel) AltPanel { return NewInfoPanel(src) })
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.QuickView",
		Area:        "Shell",
		Label:       "Quick View",
		Description: "Toggle the quick view panel",
		DescKey:     "Action.Panel.QuickView.Desc",
		DefaultKeys: []string{"CtrlQ"},
		Handler: withPF(func(pf *PanelsFrame) {
			pf.toggleAltPanel("quick_view", func(src *FileSystemPanel) AltPanel { return NewQuickViewPanel(src) })
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.SplitLeft",
		Area:        "Shell",
		Label:       "Move Split Left",
		Description: "Move the vertical split to the left",
		DescKey:     "Action.Panel.SplitLeft.Desc",
		DefaultKeys: []string{"CtrlLeft:EmptyCommandLine"},
		Handler: withPF(func(pf *PanelsFrame) {
			next := pf.widthDecrement + 1
			if maxWD := (pf.lastW / 2) - 10; maxWD > 0 && next <= maxWD && next >= -maxWD {
				pf.widthDecrement = next
				AppConfig.WidthDecrement = next
				RequestSaveConfig()
				pf.ResizeConsole(pf.lastW, pf.lastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.SplitRight",
		Area:        "Shell",
		Label:       "Move Split Right",
		Description: "Move the vertical split to the right",
		DescKey:     "Action.Panel.SplitRight.Desc",
		DefaultKeys: []string{"CtrlRight:EmptyCommandLine"},
		Handler: withPF(func(pf *PanelsFrame) {
			next := pf.widthDecrement - 1
			if maxWD := (pf.lastW / 2) - 10; maxWD > 0 && next <= maxWD && next >= -maxWD {
				pf.widthDecrement = next
				AppConfig.WidthDecrement = next
				RequestSaveConfig()
				pf.ResizeConsole(pf.lastW, pf.lastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.SplitUp",
		Area:        "Shell",
		Label:       "Move Split Up",
		Description: "Shrink both panels vertically",
		DescKey:     "Action.Panel.SplitUp.Desc",
		DefaultKeys: []string{"CtrlUp:EmptyCommandLine"},
		Handler: withPF(func(pf *PanelsFrame) {
			nextL := pf.leftHeightDecrement + 1
			nextR := pf.rightHeightDecrement + 1
			maxHD := pf.lastH - 7
			if nextL >= 0 && nextR >= 0 && (maxHD <= 0 || (nextL <= maxHD && nextR <= maxHD)) {
				pf.leftHeightDecrement = nextL
				pf.rightHeightDecrement = nextR
				AppConfig.LeftHeightDecrement = nextL
				AppConfig.RightHeightDecrement = nextR
				RequestSaveConfig()
				pf.ResizeConsole(pf.lastW, pf.lastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.SplitDown",
		Area:        "Shell",
		Label:       "Move Split Down",
		Description: "Grow both panels vertically",
		DescKey:     "Action.Panel.SplitDown.Desc",
		DefaultKeys: []string{"CtrlDown:EmptyCommandLine"},
		Handler: withPF(func(pf *PanelsFrame) {
			nextL := pf.leftHeightDecrement - 1
			nextR := pf.rightHeightDecrement - 1
			maxHD := pf.lastH - 7
			if nextL >= 0 && nextR >= 0 && (maxHD <= 0 || (nextL <= maxHD && nextR <= maxHD)) {
				pf.leftHeightDecrement = nextL
				pf.rightHeightDecrement = nextR
				AppConfig.LeftHeightDecrement = nextL
				AppConfig.RightHeightDecrement = nextR
				RequestSaveConfig()
				pf.ResizeConsole(pf.lastW, pf.lastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.SplitActiveUp",
		Area:        "Shell",
		Label:       "Shrink Active Panel",
		Description: "Shrink the active panel vertically",
		DescKey:     "Action.Panel.SplitActiveUp.Desc",
		DefaultKeys: []string{"CtrlShiftUp:EmptyCommandLine"},
		Handler: withPF(func(pf *PanelsFrame) {
			cur := &pf.rightHeightDecrement
			cfg := &AppConfig.RightHeightDecrement
			if pf.activeIdx == 0 {
				cur = &pf.leftHeightDecrement
				cfg = &AppConfig.LeftHeightDecrement
			}
			next := *cur + 1
			maxHD := pf.lastH - 7
			if next >= 0 && (maxHD <= 0 || next <= maxHD) {
				*cur = next
				*cfg = next
				RequestSaveConfig()
				pf.ResizeConsole(pf.lastW, pf.lastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.SplitActiveDown",
		Area:        "Shell",
		Label:       "Grow Active Panel",
		Description: "Grow the active panel vertically",
		DescKey:     "Action.Panel.SplitActiveDown.Desc",
		DefaultKeys: []string{"CtrlShiftDown:EmptyCommandLine"},
		Handler: withPF(func(pf *PanelsFrame) {
			cur := &pf.rightHeightDecrement
			cfg := &AppConfig.RightHeightDecrement
			if pf.activeIdx == 0 {
				cur = &pf.leftHeightDecrement
				cfg = &AppConfig.LeftHeightDecrement
			}
			next := *cur - 1
			maxHD := pf.lastH - 7
			if next >= 0 && (maxHD <= 0 || next <= maxHD) {
				*cur = next
				*cfg = next
				RequestSaveConfig()
				pf.ResizeConsole(pf.lastW, pf.lastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.SplitReset",
		Area:        "Shell",
		Label:       "Reset Split",
		Description: "Reset the panel split to defaults",
		DescKey:     "Action.Panel.SplitReset.Desc",
		DefaultKeys: []string{"CtrlVK_C"},
		Handler: withPF(func(pf *PanelsFrame) {
			if pf.widthDecrement != 0 || pf.leftHeightDecrement != 0 || pf.rightHeightDecrement != 0 {
				pf.widthDecrement = 0
				pf.leftHeightDecrement = 0
				pf.rightHeightDecrement = 0
				AppConfig.WidthDecrement = 0
				AppConfig.LeftHeightDecrement = 0
				AppConfig.RightHeightDecrement = 0
				RequestSaveConfig()
				pf.ResizeConsole(pf.lastW, pf.lastH)
				vtui.FrameManager.HardRefresh()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.ToggleInfoBytes",
		Area:        "Shell",
		Label:       "Toggle Bytes Format",
		Description: "Flip number formatting in info and quick view panels",
		DescKey:     "Action.Panel.ToggleInfoBytes.Desc",
		DefaultKeys: []string{"B:AltPanelVisible"},
		Handler: withPF(func(pf *PanelsFrame) {
			AppConfig.InfoPanelBytes = !AppConfig.InfoPanelBytes
			RequestSaveConfig()
			vtui.FrameManager.HardRefresh()
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.ToggleHidden",
		Area:        "Shell",
		Label:       "Toggle Hidden",
		LabelKey:    "Action.Panel.ToggleHidden",
		Description: "Show or hide hidden and system files on both panels",
		DescKey:     "Action.Panel.ToggleHidden.Desc",
		DefaultKeys: []string{"CtrlH"},
		Checked:     func() bool { return AppConfig.ShowHiddenFiles },
		Handler: withPF(func(pf *PanelsFrame) {
			AppConfig.ShowHiddenFiles = !AppConfig.ShowHiddenFiles
			pf.RefreshAll()
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.ViewBrief",
		Area:        "Shell",
		Label:       "Brief Mode",
		Description: "Set active panel to brief mode",
		DescKey:     "Action.Panel.ViewBrief.Desc",
		DefaultKeys: []string{"Ctrl1"},
		Visible:     func() bool { return !isAIPanelActive() },
		Handler:     withPF(func(pf *PanelsFrame) { pf.setPanelViewMode(pf.activeIdx, ViewModeBrief) }),
	})
	RegisterAction(Action{
		Name:        "Panel.ViewMedium",
		Area:        "Shell",
		Label:       "Medium Mode",
		Description: "Set active panel to medium mode",
		DescKey:     "Action.Panel.ViewMedium.Desc",
		DefaultKeys: []string{"Ctrl2"},
		Visible:     func() bool { return !isAIPanelActive() },
		Handler:     withPF(func(pf *PanelsFrame) { pf.setPanelViewMode(pf.activeIdx, ViewModeMedium) }),
	})
	RegisterAction(Action{
		Name:        "Panel.ViewDetailed",
		Area:        "Shell",
		Label:       "Detailed Mode",
		Description: "Set active panel to detailed mode",
		DescKey:     "Action.Panel.ViewDetailed.Desc",
		DefaultKeys: []string{"Ctrl3"},
		Visible:     func() bool { return !isAIPanelActive() },
		Handler:     withPF(func(pf *PanelsFrame) { pf.setPanelViewMode(pf.activeIdx, ViewModeDetailed) }),
	})
	RegisterAction(Action{
		Name:        "Panel.ViewWide",
		Area:        "Shell",
		Label:       "Wide Mode",
		Description: "Set active panel to wide mode",
		DescKey:     "Action.Panel.ViewWide.Desc",
		DefaultKeys: []string{"Ctrl4"},
		Visible:     func() bool { return !isAIPanelActive() },
		Handler:     withPF(func(pf *PanelsFrame) { pf.setWidePanel(pf.activeIdx) }),
	})
	RegisterAction(Action{
		Name:        "Panel.SortByName",
		Area:        "Shell",
		Label:       "Sort by Name",
		Description: "Sort panel by name",
		DescKey:     "Action.Panel.SortByName.Desc",
		DefaultKeys: []string{"CtrlF3"},
		Handler:     withPF(func(pf *PanelsFrame) { vtui.FrameManager.EmitCommand(CmSortName, nil) }),
	})
	RegisterAction(Action{
		Name:        "Panel.SortByExt",
		Area:        "Shell",
		Label:       "Sort by Extension",
		Description: "Sort panel by extension",
		DescKey:     "Action.Panel.SortByExt.Desc",
		DefaultKeys: []string{"CtrlF4"},
		Handler:     withPF(func(pf *PanelsFrame) { vtui.FrameManager.EmitCommand(CmSortExt, nil) }),
	})
	RegisterAction(Action{
		Name:        "Panel.SortByTime",
		Area:        "Shell",
		Label:       "Sort by Time",
		Description: "Sort panel by modification time",
		DescKey:     "Action.Panel.SortByTime.Desc",
		DefaultKeys: []string{"CtrlF5"},
		Handler:     withPF(func(pf *PanelsFrame) { vtui.FrameManager.EmitCommand(CmSortTime, nil) }),
	})
	RegisterAction(Action{
		Name:        "Panel.SortBySize",
		Area:        "Shell",
		Label:       "Sort by Size",
		Description: "Sort panel by size",
		DescKey:     "Action.Panel.SortBySize.Desc",
		DefaultKeys: []string{"CtrlF6"},
		Handler:     withPF(func(pf *PanelsFrame) { vtui.FrameManager.EmitCommand(CmSortSize, nil) }),
	})
	RegisterAction(Action{
		Name:        "Panel.SortUnsorted",
		Area:        "Shell",
		Label:       "Unsorted",
		Description: "Disable panel sorting",
		DescKey:     "Action.Panel.SortUnsorted.Desc",
		DefaultKeys: []string{"CtrlF7"},
		Handler:     withPF(func(pf *PanelsFrame) { vtui.FrameManager.EmitCommand(CmSortUnsorted, nil) }),
	})
	RegisterAction(Action{
		Name:         "Panel.LeftDriveMenu",
		Area:         "Shell",
		Label:        "Left Drive Menu",
		Description:  "Show the drive menu for the left panel",
		DescKey:      "Action.Panel.LeftDriveMenu.Desc",
		DefaultKeys:  []string{"AltF1:NoAltScreenApp", "CtrlShiftLeft:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		Handler:      withPF(func(pf *PanelsFrame) { pf.showDriveMenu(0) }),
	})
	RegisterAction(Action{
		Name:         "Panel.RightDriveMenu",
		Area:         "Shell",
		Label:        "Right Drive Menu",
		Description:  "Show the drive menu for the right panel",
		DescKey:      "Action.Panel.RightDriveMenu.Desc",
		DefaultKeys:  []string{"AltF2:NoAltScreenApp", "CtrlShiftRight:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		Handler:      withPF(func(pf *PanelsFrame) { pf.showDriveMenu(1) }),
	})
	RegisterAction(Action{
		Name:        "Panel.EnterDirectory",
		Area:        "Shell",
		Label:       "Enter Directory",
		Description: "Enter the directory or archive under the cursor",
		DescKey:     "Action.Panel.EnterDirectory.Desc",
		DefaultKeys: []string{"CtrlPgDn", "CtrlShiftPgDn"},
		Handler: withPF(func(pf *PanelsFrame) {
			fsp := pf.getActivePanel()
			if fsp == nil {
				return
			}
			idx := fsp.GetCursorIndex()
			if idx < 0 || idx >= len(fsp.entries) {
				return
			}
			selected := fsp.entries[idx]
			isDir := selected.IsDir
			isArchive := false
			if !isDir {
				fullPath := fsp.vfs.Join(fsp.vfs.GetPath(), selected.Name)
				isArchive = vfs.FindProvider(context.Background(), fsp.vfs, fullPath) != nil
			}
			if isDir || isArchive {
				pf.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
			}
		}),
	})
	RegisterAction(Action{
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
		Handler: withPF(func(pf *PanelsFrame) {
			pf.insertSelectedFileName()
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.InsertLeftPath",
		Area:        "Shell",
		Label:       "Insert Left Path",
		Description: "Insert the left panel path into the command line",
		DescKey:     "Action.Panel.InsertLeftPath.Desc",
		DefaultKeys: []string{"CtrlVK_DB"},
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.visualLeftFSP(); fsp != nil {
				pf.insertPathToCmdLine(fsp.vfs.GetPath())
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Panel.InsertRightPath",
		Area:        "Shell",
		Label:       "Insert Right Path",
		Description: "Insert the right panel path into the command line",
		DescKey:     "Action.Panel.InsertRightPath.Desc",
		DefaultKeys: []string{"CtrlVK_DD"},
		Handler: withPF(func(pf *PanelsFrame) {
			if fsp := pf.visualRightFSP(); fsp != nil {
				pf.insertPathToCmdLine(fsp.vfs.GetPath())
			}
		}),
	})
	RegisterAction(Action{
		Name:         "App.Quit",
		Area:         "Shell",
		Label:        "Quit",
		Description:  "Quit f4",
		DescKey:      "Action.App.Quit.Desc",
		DefaultKeys:  []string{"F10:NoAltScreenApp"},
		DefaultAreas: []string{"Terminal"},
		Handler:      func() bool { return vtui.FrameManager.EmitCommand(vtui.CmQuit, nil) },
	})
	RegisterAction(Action{
		Name:        "Debug.DummyOperation",
		Area:        "Shell",
		Label:       "Dummy Long Operation",
		Description: "Show a dummy long operation dialog (debug)",
		DescKey:     "Action.Debug.DummyOperation.Desc",
		DefaultKeys: []string{"AltF5"},
		Handler:     withPF(func(pf *PanelsFrame) { pf.showDummyOpDialog() }),
	})

	// --- Terminal actions ---
	RegisterAction(Action{
		Name:        "Terminal.ViewLog",
		Area:        "Terminal",
		Label:       "View Terminal Log",
		LabelKey:    "Action.Terminal.ViewLog",
		Description: "Open terminal log in viewer",
		DescKey:     "Action.Terminal.ViewLog.Desc",
		DefaultKeys: []string{"F3:TerminalQuiet", "CtrlShiftF3"},
		MenuPath:    "File",
		Handler:     withPF(func(pf *PanelsFrame) { actionViewTerminalLog(pf) }),
	})
	RegisterAction(Action{
		Name:        "Terminal.EditLog",
		Area:        "Terminal",
		Label:       "Edit Terminal Log",
		LabelKey:    "Action.Terminal.EditLog",
		Description: "Open terminal log in editor",
		DescKey:     "Action.Terminal.EditLog.Desc",
		DefaultKeys: []string{"F4:TerminalQuiet", "CtrlShiftF4"},
		MenuPath:    "File",
		Handler:     withPF(func(pf *PanelsFrame) { actionEditTerminalLog(pf) }),
	})

	// --- Editor actions (menu order follows registration order) ---
	RegisterAction(Action{
		Name:        "Editor.Save",
		Area:        "Editor",
		Label:       "Save",
		LabelKey:    "Action.Editor.Save",
		Description: "Save file",
		DescKey:     "Action.Editor.Save.Desc",
		DefaultKeys: []string{"F2"},
		MenuPath:    "File",
		Handler:     withEditor(func(ev *EditorView) { ev.SaveToFile(nil) }),
	})
	RegisterAction(Action{
		Name:        "Editor.SwitchToViewer",
		Area:        "Editor",
		Label:       "Switch to Viewer",
		LabelKey:    "Action.Editor.SwitchToViewer",
		Description: "Switch to viewer mode",
		DescKey:     "Action.Editor.SwitchToViewer.Desc",
		DefaultKeys: []string{"F6"},
		MenuPath:    "File",
		Handler:     withEditor(func(ev *EditorView) { vtui.FrameManager.EmitCommand(CmSwitchToViewer, ev) }),
	})
	RegisterAction(Action{
		Name:        "Editor.Quit",
		Area:        "Editor",
		Label:       "Quit",
		LabelKey:    "Action.Editor.Quit",
		Description: "Close editor",
		DescKey:     "Action.Editor.Quit.Desc",
		DefaultKeys: []string{"F10", "Esc", "F4"},
		MenuPath:    "File",
		Handler:     withEditor(func(ev *EditorView) { ev.tryClose() }),
	})

	RegisterAction(Action{
		Name:        "Editor.Undo",
		Area:        "Editor",
		Label:       "Undo",
		LabelKey:    "Action.Editor.Undo",
		Description: "Undo last change",
		DescKey:     "Action.Editor.Undo.Desc",
		DefaultKeys: []string{"CtrlZ"},
		MenuPath:    "Edit",
		Handler:     withEditor(func(ev *EditorView) { ev.Undo() }),
	})
	RegisterAction(Action{
		Name:        "Editor.Redo",
		Area:        "Editor",
		Label:       "Redo",
		LabelKey:    "Action.Editor.Redo",
		Description: "Redo last undone change",
		DescKey:     "Action.Editor.Redo.Desc",
		DefaultKeys: []string{"CtrlShiftZ"},
		MenuPath:    "Edit",
		Handler:     withEditor(func(ev *EditorView) { ev.Redo() }),
	})
	RegisterAction(Action{
		Name:        "Editor.Copy",
		Area:        "Editor",
		Label:       "Copy",
		LabelKey:    "Action.Editor.Copy",
		Description: "Copy selection to clipboard",
		DescKey:     "Action.Editor.Copy.Desc",
		DefaultKeys: []string{"CtrlC", "CtrlIns"},
		MenuPath:    "Edit",
		Handler: withEditor(func(ev *EditorView) {
			if ev.selActive || ev.rectSelActive {
				ev.CopySelection()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Editor.Cut",
		Area:        "Editor",
		Label:       "Cut",
		LabelKey:    "Action.Editor.Cut",
		Description: "Cut selection to clipboard",
		DescKey:     "Action.Editor.Cut.Desc",
		// Ctrl+X is intentionally not a default key: the editor keeps it as
		// the classic down-movement alias when no selection exists (see
		// EditorView.ProcessKey), and delegates to this action when there
		// is a selection. Shift+Del is the advertised Cut hotkey.
		DefaultKeys: []string{"ShiftDel"},
		MenuPath:    "Edit",
		Handler: withEditor(func(ev *EditorView) {
			if ev.selActive || ev.rectSelActive {
				ev.CopySelection()
				ev.DeleteSelection()
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Editor.Paste",
		Area:        "Editor",
		Label:       "Paste",
		LabelKey:    "Action.Editor.Paste",
		Description: "Paste text from clipboard",
		DescKey:     "Action.Editor.Paste.Desc",
		DefaultKeys: []string{"ShiftIns", "CtrlV"},
		MenuPath:    "Edit",
		Handler: withEditor(func(ev *EditorView) {
			if text := vtui.GetClipboard(); text != "" {
				ev.PasteText(text)
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Editor.SelectAll",
		Area:        "Editor",
		Label:       "Select All",
		LabelKey:    "Action.Editor.SelectAll",
		Description: "Select all text",
		DescKey:     "Action.Editor.SelectAll.Desc",
		DefaultKeys: []string{"CtrlA"},
		MenuPath:    "Edit",
		Handler: withEditor(func(ev *EditorView) {
			ev.rectSelActive = false
			ev.selActive = true
			ev.selAnchorOffset = 0
			lastLine := ev.li.LineCount() - 1
			ev.CursorLine = lastLine
			ev.CursorPos = ev.getLineLength(lastLine)
			ev.ensureCursorVisible()
		}),
	})
	RegisterAction(Action{
		Name:        "Editor.DeleteLine",
		Area:        "Editor",
		Label:       "Delete Line",
		LabelKey:    "Action.Editor.DeleteLine",
		Description: "Delete current line",
		DescKey:     "Action.Editor.DeleteLine.Desc",
		DefaultKeys: []string{"CtrlY"},
		MenuPath:    "Edit",
		Handler:     withEditor(func(ev *EditorView) { ev.DeleteCurrentLine() }),
	})
	RegisterAction(Action{
		Name:        "Editor.ToggleOvertype",
		Area:        "Editor",
		Label:       "Insert/Overtype",
		LabelKey:    "Action.Editor.ToggleOvertype",
		Description: "Toggle insert/overtype mode",
		DescKey:     "Action.Editor.ToggleOvertype.Desc",
		DefaultKeys: []string{"Ins"},
		MenuPath:    "Edit",
		Checked:     editorState(func(ev *EditorView) bool { return ev.overtype }),
		Handler: withEditor(func(ev *EditorView) {
			ev.overtype = !ev.overtype
			ev.ensureCursorVisible()
		}),
	})

	RegisterAction(Action{
		Name:        "Editor.Search",
		Area:        "Editor",
		Label:       "Search",
		LabelKey:    "Action.Editor.Search",
		Description: "Find text",
		DescKey:     "Action.Editor.Search.Desc",
		DefaultKeys: []string{"F7"},
		MenuPath:    "Search",
		Handler:     withEditor(func(ev *EditorView) { vtui.FrameManager.EmitCommand(CmSearch, nil) }),
	})
	RegisterAction(Action{
		Name:        "Editor.Replace",
		Area:        "Editor",
		Label:       "Replace",
		LabelKey:    "Action.Editor.Replace",
		Description: "Replace text",
		DescKey:     "Action.Editor.Replace.Desc",
		DefaultKeys: []string{"CtrlF7"},
		MenuPath:    "Search",
		Handler:     withEditor(func(ev *EditorView) { vtui.FrameManager.EmitCommand(CmReplace, nil) }),
	})
	RegisterAction(Action{
		Name:        "Editor.SearchNext",
		Area:        "Editor",
		Label:       "Search Next",
		LabelKey:    "Action.Editor.SearchNext",
		Description: "Continue search",
		DescKey:     "Action.Editor.SearchNext.Desc",
		DefaultKeys: []string{"ShiftF7"},
		MenuPath:    "Search",
		Handler: withEditor(func(ev *EditorView) {
			if LastEditorSearch != "" {
				ev.Search(LastEditorSearch, LastEditorSearchCase, LastEditorSearchReverse, LastEditorSearchRegexp, LastEditorSearchWholeWord, true)
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Editor.SearchForward",
		Area:        "Editor",
		Label:       "Search Next",
		LabelKey:    "Action.Editor.SearchNext",
		Description: "Continue search forwards",
		DescKey:     "Action.Editor.SearchNext.Desc",
		DefaultKeys: []string{"CtrlEnter"},
		Handler:     withEditor(func(ev *EditorView) { repeatEditorSearchDirection(ev, false) }),
	})
	RegisterAction(Action{
		Name:        "Editor.SearchPrevious",
		Area:        "Editor",
		Label:       "Search Backwards",
		LabelKey:    "Action.Editor.SearchPrevious",
		Description: "Continue search backwards",
		DescKey:     "Action.Editor.SearchPrevious.Desc",
		DefaultKeys: []string{"CtrlShiftEnter"},
		Handler:     withEditor(func(ev *EditorView) { repeatEditorSearchDirection(ev, true) }),
	})

	RegisterAction(Action{
		Name:        "Editor.WordWrap",
		Area:        "Editor",
		Label:       "Word Wrap",
		LabelKey:    "Action.Editor.WordWrap",
		Description: "Toggle word wrap",
		DescKey:     "Action.Editor.WordWrap.Desc",
		DefaultKeys: []string{"F3"},
		MenuPath:    "Options",
		Checked:     editorState(func(ev *EditorView) bool { return ev.WordWrap }),
		Handler: withEditor(func(ev *EditorView) {
			ev.WordWrap = !ev.WordWrap
			ev.ScrollLeft = 0
			ev.clearCaches()
			ev.ensureCursorVisible()
		}),
	})
	RegisterAction(Action{
		Name:        "Editor.ShowWhitespaces",
		Area:        "Editor",
		Label:       "Show Whitespaces",
		LabelKey:    "Action.Editor.ShowWhitespaces",
		Description: "Toggle visible whitespaces",
		DescKey:     "Action.Editor.ShowWhitespaces.Desc",
		DefaultKeys: []string{"F5"},
		MenuPath:    "Options",
		Checked:     editorState(func(ev *EditorView) bool { return ev.ShowWhitespaces }),
		Handler:     withEditor(func(ev *EditorView) { ev.ShowWhitespaces = !ev.ShowWhitespaces }),
	})
	RegisterAction(Action{
		Name:        "Editor.CodepageNext",
		Area:        "Editor",
		Label:       "Next Codepage",
		LabelKey:    "Action.Editor.CodepageNext",
		Description: "Cycle to next codepage",
		DescKey:     "Action.Editor.CodepageNext.Desc",
		DefaultKeys: []string{"F8"},
		MenuPath:    "Options",
		Handler: withEditor(func(ev *EditorView) {
			next := vfs.GetNextFastSwitchCodepage(ev.Codepage)
			AppConfig.EditorAutodetectCodePage = false
			AppConfig.EditorDefaultCodePage = next
			SaveConfig()
			ev.ReloadWithCodepage(next)
			vtui.ShowToast(fmt.Sprintf("Codepage: %s", vfs.DisplayCodepageName(next)), time.Second)
		}),
	})
	RegisterAction(Action{
		Name:        "Editor.CodepageMenu",
		Area:        "Editor",
		Label:       "Codepage Menu",
		LabelKey:    "Action.Editor.CodepageMenu",
		Description: "Select codepage",
		DescKey:     "Action.Editor.CodepageMenu.Desc",
		DefaultKeys: []string{"ShiftF8"},
		MenuPath:    "Options",
		Handler:     withEditor(func(ev *EditorView) { ev.showCodepageDialog() }),
	})
	RegisterAction(Action{
		Name:        "Editor.ConvertCodepage",
		Area:        "Editor",
		Label:       "Convert codepage...",
		LabelKey:    "Action.Editor.ConvertCodepage",
		Description: "Change the file codepage without modifying text",
		DescKey:     "Action.Editor.ConvertCodepage.Desc",
		MenuPath:    "Options",
		Handler:     withEditor(func(ev *EditorView) { ev.showConvertCodepageDialog() }),
	})

	RegisterAction(Action{
		Name:        "Editor.InsertLeftPanelPath",
		Area:        "Editor",
		Label:       "Insert Left Panel Path",
		LabelKey:    "Action.Editor.InsertLeftPanelPath",
		Description: "Insert the left panel's path at cursor",
		DescKey:     "Action.Editor.InsertLeftPanelPath.Desc",
		DefaultKeys: []string{"CtrlVK_DB"},
		MenuPath:    "Insert",
		Handler: withEditor(func(ev *EditorView) {
			if s := leftPanelPathForEditor(); s != "" {
				ev.insertTextAtCursor([]byte(s))
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Editor.InsertRightPanelPath",
		Area:        "Editor",
		Label:       "Insert Right Panel Path",
		LabelKey:    "Action.Editor.InsertRightPanelPath",
		Description: "Insert the right panel's path at cursor",
		DescKey:     "Action.Editor.InsertRightPanelPath.Desc",
		DefaultKeys: []string{"CtrlVK_DD"},
		MenuPath:    "Insert",
		Handler: withEditor(func(ev *EditorView) {
			if s := rightPanelPathForEditor(); s != "" {
				ev.insertTextAtCursor([]byte(s))
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Editor.InsertActivePanelFileName",
		Area:        "Editor",
		Label:       "Insert Current File Name",
		LabelKey:    "Action.Editor.InsertActivePanelFileName",
		Description: "Insert the active panel's current file name at cursor",
		DescKey:     "Action.Editor.InsertActivePanelFileName.Desc",
		MenuPath:    "Insert",
		Handler: withEditor(func(ev *EditorView) {
			if s := activePanelNameForEditor(); s != "" {
				ev.insertTextAtCursor([]byte(s))
			}
		}),
	})
	RegisterAction(Action{
		Name:        "Editor.DeleteSpacersForward",
		Area:        "Editor",
		Label:       "Delete Word Forward",
		LabelKey:    "Action.Editor.DeleteSpacersForward",
		Description: "Delete spaces and word forward",
		DescKey:     "Action.Editor.DeleteSpacersForward.Desc",
		DefaultKeys: []string{"CtrlDel"},
		MenuPath:    "Insert",
		Handler:     withEditor(func(ev *EditorView) { ev.deleteSpacersForward() }),
	})

	// --- Viewer actions ---
	RegisterAction(Action{
		Name:        "Viewer.SwitchToEditor",
		Area:        "Viewer",
		Label:       "Switch to Editor",
		LabelKey:    "Action.Viewer.SwitchToEditor",
		Description: "Switch to editor mode",
		DescKey:     "Action.Viewer.SwitchToEditor.Desc",
		DefaultKeys: []string{"F6"},
		MenuPath:    "File",
		Handler:     withViewer(func(vv *ViewerView) { vtui.FrameManager.EmitCommand(CmSwitchToEditor, vv) }),
	})
	RegisterAction(Action{
		Name:        "Viewer.Quit",
		Area:        "Viewer",
		Label:       "Quit",
		LabelKey:    "Action.Viewer.Quit",
		Description: "Close viewer",
		DescKey:     "Action.Viewer.Quit.Desc",
		DefaultKeys: []string{"Esc", "F10", "F3"},
		MenuPath:    "File",
		Handler:     withViewer(func(vv *ViewerView) { vv.Close() }),
	})

	RegisterAction(Action{
		Name:        "Viewer.WrapMode",
		Area:        "Viewer",
		Label:       "Wrap Mode",
		LabelKey:    "Action.Viewer.WrapMode",
		Description: "Toggle word wrap",
		DescKey:     "Action.Viewer.WrapMode.Desc",
		DefaultKeys: []string{"F2"},
		MenuPath:    "View",
		Checked:     viewerState(func(vv *ViewerView) bool { return vv.WrapMode }),
		Handler:     withViewer(func(vv *ViewerView) { vv.WrapMode = !vv.WrapMode }),
	})
	RegisterAction(Action{
		Name:        "Viewer.HexMode",
		Area:        "Viewer",
		Label:       "Hex Mode",
		LabelKey:    "Action.Viewer.HexMode",
		Description: "Toggle hex view",
		DescKey:     "Action.Viewer.HexMode.Desc",
		DefaultKeys: []string{"F4"},
		MenuPath:    "View",
		Checked:     viewerState(func(vv *ViewerView) bool { return vv.HexMode }),
		Handler: withViewer(func(vv *ViewerView) {
			vv.HexMode = !vv.HexMode
			if vv.HexMode {
				vv.TopOffset &= ^int64(0xF)
			}
		}),
	})

	RegisterAction(Action{
		Name:        "Viewer.Search",
		Area:        "Viewer",
		Label:       "Search",
		LabelKey:    "Action.Viewer.Search",
		Description: "Find text",
		DescKey:     "Action.Viewer.Search.Desc",
		DefaultKeys: []string{"F7"},
		MenuPath:    "Search",
		Handler:     withViewer(func(vv *ViewerView) { vtui.FrameManager.EmitCommand(CmSearch, nil) }),
	})
	RegisterAction(Action{
		Name:        "Viewer.SearchNext",
		Area:        "Viewer",
		Label:       "Search Next",
		LabelKey:    "Action.Viewer.SearchNext",
		Description: "Continue search forwards",
		DescKey:     "Action.Viewer.SearchNext.Desc",
		DefaultKeys: []string{"CtrlEnter"},
		MenuPath:    "Search",
		Handler:     withViewer(func(vv *ViewerView) { actionViewerSearchAgain(vv, false) }),
	})
	RegisterAction(Action{
		Name:        "Viewer.SearchPrevious",
		Area:        "Viewer",
		Label:       "Search Backwards",
		LabelKey:    "Action.Viewer.SearchPrevious",
		Description: "Continue search backwards",
		DescKey:     "Action.Viewer.SearchPrevious.Desc",
		DefaultKeys: []string{"CtrlShiftEnter"},
		MenuPath:    "Search",
		Handler:     withViewer(func(vv *ViewerView) { actionViewerSearchAgain(vv, true) }),
	})

	RegisterAction(Action{
		Name:        "Viewer.CodepageNext",
		Area:        "Viewer",
		Label:       "Next Codepage",
		LabelKey:    "Action.Viewer.CodepageNext",
		Description: "Cycle to next codepage",
		DescKey:     "Action.Viewer.CodepageNext.Desc",
		DefaultKeys: []string{"F8"},
		MenuPath:    "Options",
		Handler: withViewer(func(vv *ViewerView) {
			next := vfs.GetNextFastSwitchCodepage(vv.Codepage)
			AppConfig.ViewerAutodetectCodePage = false
			AppConfig.ViewerDefaultCodePage = next
			SaveConfig()
			vv.ReloadWithCodepage(next)
			vtui.ShowToast(fmt.Sprintf("Codepage: %s", vfs.DisplayCodepageName(next)), time.Second)
		}),
	})
	RegisterAction(Action{
		Name:        "Viewer.CodepageMenu",
		Area:        "Viewer",
		Label:       "Codepage Menu",
		LabelKey:    "Action.Viewer.CodepageMenu",
		Description: "Select codepage",
		DescKey:     "Action.Viewer.CodepageMenu.Desc",
		DefaultKeys: []string{"ShiftF8"},
		MenuPath:    "Options",
		Handler:     withViewer(func(vv *ViewerView) { vv.showCodepageDialog() }),
	})
}
