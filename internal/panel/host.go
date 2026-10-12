package panel

import (
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// What the panels need from the application above them.
//
// The largest of these is AppCommand. A frame command the panel raises — copy,
// rename, open the settings — is answered by the action table, which lives at
// layer 4 and reaches back into every subsystem. The panel keeps the switch
// that decides *which* command it is, because that is its own dispatch order,
// and hands the ones it does not serve itself to the root with the frame it
// was raised on. Passing the frame matters: HandleCommand can reach a frame
// that is not the active one, and looking it up again would answer for the
// wrong panels.
//
// The root fills these in. Every default is inert: an unwired panel declines
// the command and shows no menu, which is wrong in a way somebody notices.
var (
	// AppCommand answers a frame command the panel does not implement, and
	// reports whether it did.
	AppCommand = func(pf *PanelsFrame, cmd int, args any) bool { return false }

	// RunAction runs a registered action by name.
	RunAction = func(name string) bool { return false }

	// BuildMenuBarItems builds the menu bar for an area from the action table.
	BuildMenuBarItems = func(area string) []vtui.MenuBarItem { return nil }

	// RefreshMenuRowStates brings the dimmed flag of each row of a menu built
	// by BuildMenuBarItems up to date with the application's state. The menu
	// itself is cached between frames, but whether a row can be used depends on
	// things that change without the cache noticing, the cursor above all
	// (f4#1814).
	RefreshMenuRowStates = func(items []vtui.MenuBarItem) {}

	// SaveSession persists the workspace layout after a change to it.
	SaveSession = func() {}
)

// MenuContentSignalValue is a cheap-to-compare snapshot of application-level
// state that can change what BuildMenuItems' generated menus contain, but
// that this package has no way to read for itself (it does not import the
// application layer). GetMenuContentSignal reports it; BuildMenuItems' cache
// treats any change to it, since the last time it was asked, as a reason to
// rebuild.
type MenuContentSignalValue struct {
	// PluginCommandGeneration is plughost.PluginCommandRegistryGeneration():
	// it moves when a panel plugin command is registered or unregistered.
	PluginCommandGeneration uint64
	// AIPatchPresent is whether the AI session currently holds an applicable
	// patch (AI.ApplyPatch's Visible check).
	AIPatchPresent bool
}

// GetMenuContentSignal reports the current MenuContentSignalValue. The
// default always returns the zero value, which is correct for a build that
// never wires plugins or the AI panel in: the cache simply never sees this
// component change.
var GetMenuContentSignal = func() MenuContentSignalValue { return MenuContentSignalValue{} }

// Opening a file is the application's business. It picks the frame, keeps the
// editor's table of open files and writes the view/edit history; the panel
// only says which file, on which VFS, from which frame.
var (
	// OpenEditor and OpenViewer open path for editing or viewing, choosing the
	// tool by the file and the settings.
	OpenEditor = func(pf *PanelsFrame, v vfs.VFS, path string) {}
	OpenViewer = func(pf *PanelsFrame, v vfs.VFS, path string) {}

	// OpenViewerInternal skips the external-viewer setting: the caller has
	// already decided that f4's own viewer is what it wants.
	OpenViewerInternal = func(pf *PanelsFrame, v vfs.VFS, path string) {}

	// OpenEditFileIn edits a path typed at the command line, resolved against
	// the active panel by the caller.
	OpenEditFileIn = func(pf *PanelsFrame, path string) {}

	// ShowViewer and ShowEditor push a frame the caller has already built, and
	// FindOpenedEditor finds the frame that ShowEditor pushed, so a caller
	// working on a scratch file can hang its cleanup on the editor's OnClose.
	ShowViewer       = func(pf *PanelsFrame, vv *viewer.ViewerView, path string) {}
	ShowEditor       = func(pf *PanelsFrame, v vfs.VFS, path string, f vfs.ReadAtCloser) {}
	FindOpenedEditor = func(v vfs.VFS, path string) (*editor.EditorView, int) { return nil, -1 }

	// Execute runs the file under the cursor the way Enter does.
	Execute = func(pf *PanelsFrame, v vfs.VFS, dir, name, path string) {}

	// SortMenuForPanel opens the sort menu of one panel, built from the action
	// table so its rows carry the current shortcuts.
	SortMenuForPanel = func(pf *PanelsFrame, fsp *FileSystemPanel) {}

	// WorkspaceClose closes the active workspace, and Arkanoid is the Ctrl+Alt+A
	// easter egg. Both are plain actions, reached directly rather than through
	// RunAction because RunAction cancels Fast Find on the way.
	WorkspaceClose = func() bool { return false }
	Arkanoid       = func() bool { return false }

	// CurrentArea names the hotkey area of the frame on top — "Shell",
	// "Editor", "Dialog" and so on. Which frame types exist is the
	// application's knowledge, not the panel's.
	CurrentArea = func() string { return "Common" }

	// MacroHotkey routes an injected key (a key-bar click, a macro) through the
	// configured bindings, and reports whether an action consumed it.
	MacroHotkey = func(e *vtinput.InputEvent) bool { return false }
)

// AISetViewMode switches a panel between the AI chat and the AI context views.
// The AI host owns both; the panel carries only the method that the reflection
// cast in the plugin command path looks for by name.
var AISetViewMode = func(fsp *FileSystemPanel, path string, isChat bool) {}

// AiSetViewMode is that method. The spelling is the one the cast asks for.
func (fsp *FileSystemPanel) AiSetViewMode(path string, isChat bool) {
	AISetViewMode(fsp, path, isChat)
}

// KeyFilter is the application's global key filter: the key remap, macro
// recording and playback, and the configurable-hotkey dispatch that all answer
// a key before any frame sees it. It reports whether the key was consumed.
var KeyFilter = func(e *vtinput.InputEvent) bool { return false }
