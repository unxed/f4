package vfs

import (
	"context"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Registration is a live plugin contribution. Unregister is safe to call
// more than once.
type Registration interface {
	Unregister()
}

// PluginCommandLocation identifies the menu that hosts a plugin command.
type PluginCommandLocation uint8

const (
	PluginCommandPanel PluginCommandLocation = iota
	PluginCommandConfig
)

// PluginCommand contributes one command to an f4 plugin menu.
type PluginCommand struct {
	ID       string
	Location PluginCommandLocation
	Label    string // English fallback shown when LabelKey is empty or unavailable.
	LabelKey string // optional host localization key resolved in the active UI language.
	// MenuPath optionally places a panel command in the generated main menu
	// (for example, "Files" or "Commands"). Empty keeps it available through
	// the plugin menu and command palette without adding another main-menu row.
	MenuPath string
	// LocalizedLabels optionally carries plugin-owned translations keyed by
	// language code (for example, "en", "ru", or "pt-BR"). Host catalog
	// LabelKey translations take precedence; Label remains the final fallback.
	LocalizedLabels map[string]string
	Description     string // optional English fallback for discoverability surfaces.
	// DescriptionKey is an optional host localization key for Description.
	DescriptionKey string
	// LocalizedDescriptions is the plugin-owned equivalent of
	// LocalizedLabels for Description.
	LocalizedDescriptions map[string]string
	// SearchKeys are additional host localization keys whose translations are
	// indexed by discoverability surfaces but are not rendered as the label.
	SearchKeys []string
	// SearchTerms are literal aliases indexed as supplied. They complement
	// SearchKeys for plugins whose vocabulary is not part of the host catalog.
	SearchTerms []string
	// Shortcut is display-only metadata, formatted for the host UI (for example,
	// "Shift+F4"). The plugin remains responsible for registering the hotkey.
	Shortcut string
	Visible  func(App) bool
	// NotInPluginMenu keeps the command out of the F11 plugin menu: it is a
	// basic shell command in spirit (Add to archive, Extract), with its own key
	// and its row in the generated main menu and the command palette, and having
	// it among the plugins made users ask how to add files to an archive
	// (unxed/f4#918).
	NotInPluginMenu bool
	// Enabled, when set, decides whether the command can actually run right
	// now, without removing it from the menu the way Visible does: a
	// generated menu item stays on screen dimmed instead of disappearing
	// (BuildMenuBarItems sets vtui.MenuItem.Disabled from it), and
	// ExecutePluginCommand refuses the call before Run ever runs. This
	// mirrors the Visible/Enabled split action.Action already uses (see
	// internal/action/registry.go) and exists for the same reason: f4#1356's
	// "shows an error dialog instead of a disabled control" class of bug
	// (for example ID3 Tag Editor on a non-MP3 file, or Media Information on
	// a directory). Left nil, a command behaves exactly as it did before
	// this field existed -- enabled whenever Visible admits it.
	Enabled func(App) bool
	Run     func(App)
}

// CommandPrefixRegistration controls a registered command-line prefix. An
// empty prefix disables it without discarding the handler.
type CommandPrefixRegistration interface {
	Registration
	SetPrefix(prefix string) error
}

// FileRef is a stable snapshot of one panel file in a VFS namespace.
type FileRef struct {
	VFS  VFS
	Dir  string
	Name string
	Path string
}

// MacroCallContext contains UI state captured before a macro provider runs.
// Providers run outside the UI goroutine and must treat this as a snapshot.
type MacroCallContext struct {
	Current FileRef
}

// MacroCallProvider exposes a synchronous Plugin.Call target to Lua macros.
// Values are limited to nil, bool, integer and floating-point numbers,
// strings, and recursively nested slices of those values.
type MacroCallProvider struct {
	IDs  []string
	Call func(context.Context, MacroCallContext, []any) ([]any, error)
}

// ContributionHost is an optional extension implemented by hosts that accept
// rich in-process plugin contributions. Keeping it separate from HostAPI
// preserves compatibility with existing and out-of-process plugins.
type ContributionHost interface {
	RegisterQuickViewProvider(QuickViewProvider) (Registration, error)
	RegisterPluginCommand(PluginCommand) (Registration, error)
	RegisterCommandPrefix(id, prefix string, handler func(App, string)) (CommandPrefixRegistration, error)
	RegisterMacroCallProvider(MacroCallProvider) (Registration, error)
}

// ViewerContributionHost is an optional extension for plugins that can open
// selected files in their own full-screen viewer. It is separate from
// ContributionHost so existing hosts and plugin test doubles remain compatible.
type ViewerContributionHost interface {
	RegisterViewerProvider(ViewerProvider) (Registration, error)
}

// PanelState is the immutable, UI-safe snapshot of one file panel exposed to
// a panel plugin. VFS handles are deliberately omitted: a panel plugin gets
// paths and selection metadata, while file operations continue to go through
// the normal VFS/plugin APIs.
type PanelState struct {
	Side          int
	Active        bool
	Path          string
	SelectedName  string
	SelectedNames []string
}

// PanelContext describes the panel slot occupied by a plugin and both file
// panels around it. It is refreshed before every draw and input event, so a
// plugin never has to guess whether the neighbouring selection changed.
type PanelContext struct {
	Side       int
	ActiveSide int
	Bounds     [4]int
	Current    PanelState
	Other      PanelState
}

// Panel is the drawable/input surface a native or RPC panel plugin supplies.
// Implementations normally compose vtui controls and forward these methods
// to their root control.
type Panel interface {
	Show(*vtui.ScreenBuf)
	ProcessKey(*vtinput.InputEvent) bool
	ProcessMouse(*vtinput.InputEvent) bool
	SetFocus(bool)
	IsFocused() bool
	SetPosition(x1, y1, x2, y2 int)
	GetPosition() (x1, y1, x2, y2 int)
	GetSelectedName() string
}

// PanelController adds lifecycle and state delivery to a drawable panel.
type PanelController interface {
	Panel
	SetContext(PanelContext)
	Close() error
}

// PanelStateProvider is the optional PanelController extension that lets a
// bookmark (unxed/f4#1669) return to more than the panel's directory. The host
// stores what SavePanelState returns in the bookmark and hands it back to
// RestorePanelState right after it opens the panel again. The state is an
// opaque single-line string owned by the plugin (for example the name of the
// selected entry); it may be stale or foreign by then, so RestorePanelState
// must ignore what it does not understand. Both are called on the UI goroutine.
type PanelStateProvider interface {
	SavePanelState() string
	RestorePanelState(state string)
}

// PanelKey is one key a panel plugin binds while its panel has the focus.
// It is the single, shared key/keybar primitive for every PanelProvider
// (f4#312): a plugin declares *what* its keys do and how they are captioned,
// and the host owns *how* that reaches the user -- dispatch order, the
// keybar, and which of f4's own panel bindings stand down meanwhile -- the
// same way for all panel plugins, instead of each plugin hand-rolling a
// ProcessKey switch and leaving the file panel's F-key captions and actions
// live underneath it.
//
// VK is a vtinput virtual key code; Mods uses the same vtinput modifier bits
// Host.RegisterGlobalHotkey does, and matching compares only whether Ctrl,
// Alt and Shift are held (left and right variants are equivalent; lock and
// enhanced-key bits are ignored). Label is the keybar caption, already
// localized; it is shown only for F1..F12 with no modifier or exactly one of
// Shift, Ctrl or Alt, and an empty Label binds the key without a caption.
// Run is called on the UI goroutine. Enabled is optional: when it reports
// false the caption is dimmed and the key is still consumed (and Run not
// called), matching how f4's own configured hotkeys own a key even when
// their action cannot currently run.
type PanelKey struct {
	VK      uint16
	Mods    vtinput.ControlKeyState
	Label   string
	Run     func()
	Enabled func() bool
}

// ShowPanelHelp opens a panel plugin's help: markdown in the Markdown viewer,
// the window f4's own help and the F3 view of .md files use (f4#272). The
// text is the plugin's, in the interface language it already renders itself
// in; nothing is parsed or looked up here.
func ShowPanelHelp(title, markdown string) {
	if vtui.FrameManager == nil {
		return
	}
	view := vtui.NewMarkdownView(title, markdown)
	view.SetTitle(" " + title + " ")
	vtui.FrameManager.Push(view)
}

// PanelHelpKey is the F1 key of a panel plugin that has a help of its own:
// declare it among PanelKeys and the host runs it ahead of the global Help
// binding and captions F1 with label. title and markdown are called when the
// key is pressed, so they may follow the interface language.
func PanelHelpKey(label string, title, markdown func() string) PanelKey {
	return PanelKey{VK: vtinput.VK_F1, Label: label, Run: func() { ShowPanelHelp(title(), markdown()) }}
}

// Matches reports whether e is a key-down event for k. Non-key events and
// key-up events never match.
func (k PanelKey) Matches(e *vtinput.InputEvent) bool {
	if e == nil || e.Type != vtinput.KeyEventType || !e.KeyDown || e.VirtualKeyCode != k.VK {
		return false
	}
	return panelKeyMods(e.ControlKeyState) == panelKeyMods(k.Mods)
}

// panelKeyMods folds left/right modifier variants together and drops lock
// and enhanced-key bits, so a declaration and an event compare on intent.
func panelKeyMods(m vtinput.ControlKeyState) vtinput.ControlKeyState {
	var out vtinput.ControlKeyState
	if m&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0 {
		out |= vtinput.LeftCtrlPressed
	}
	if m&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0 {
		out |= vtinput.LeftAltPressed
	}
	if m&vtinput.ShiftPressed != 0 {
		out |= vtinput.ShiftPressed
	}
	return out
}

// PanelKeyProvider is the optional PanelController extension that declares
// PanelKeys. The host calls PanelKeys on the UI goroutine every time it
// dispatches a key or draws the keybar, so a plugin may return a different
// set as its state changes (a key that only exists on some platforms simply
// is not in the slice there). It should be cheap and must not block.
//
// While a panel plugin has the focus in the active slot, the host
// guarantees, whether or not the controller implements this interface:
//
//   - a declared key runs its PanelKey before any global plugin hotkey or
//     configured f4 hotkey, including keys injected by a keybar click;
//   - f4 bindings that act on the file panel's cursor or selection (the
//     File.* actions and the group-selection keys) and global plugin
//     hotkeys stand down; the key reaches the controller's ProcessKey
//     instead, and is dropped if the controller does not claim it;
//   - the file panel hidden under the plugin never receives keys;
//   - the keybar shows the declared captions, the file-panel captions are
//     blank, and every other f4 binding (Help, menus, panel toggles, quit,
//     ...) keeps its caption and its key.
type PanelKeyProvider interface {
	PanelKeys() []PanelKey
}

// DispatchPanelKey runs the first key in keys that matches e and reports
// whether one did. A disabled match is consumed without running. It is the
// host's own dispatcher, exported so a controller (or its tests) can route
// the same declarations through ProcessKey when hosted without it.
func DispatchPanelKey(keys []PanelKey, e *vtinput.InputEvent) bool {
	for _, k := range keys {
		if !k.Matches(e) {
			continue
		}
		if k.Run != nil && (k.Enabled == nil || k.Enabled()) {
			k.Run()
		}
		return true
	}
	return false
}

// PanelProvider describes a panel-only plugin contribution. The host exposes
// an automatically searchable command for every provider, labelled with Title.
// Open is called on the UI goroutine and should construct controls quickly;
// long-running work belongs in the existing task APIs.
type PanelProvider struct {
	ID          string
	Title       string
	Description string
	Open        func(PanelContext) (PanelController, error)
}

// PanelContributionHost is separate from ContributionHost so older host
// implementations and test doubles remain source-compatible.
type PanelContributionHost interface {
	RegisterPanelProvider(PanelProvider) (Registration, error)
}

// ProcessEnvironmentVariable is one name/value pair in f4's actual process
// environment. Snapshots are returned in a stable name order. Names are
// compared case-insensitively on Windows and case-sensitively elsewhere.
type ProcessEnvironmentVariable struct {
	Name  string
	Value string
}

// ProcessEnvironmentChange updates or removes one process environment
// variable. Name must be a portable identifier matching
// [A-Za-z_][A-Za-z0-9_]*. A set Value may not contain NUL, CR, or LF and may
// also be rejected when a host shell cannot transport it exactly (including
// cmd.exe's live-update length bound on Windows); Unset ignores Value. If a
// name occurs more than once, changes are applied in slice order and the last
// value wins. Hosts validate the whole batch before mutation and apply it
// atomically: when an OS update fails, earlier changes in the same batch are
// rolled back as far as the operating system permits.
type ProcessEnvironmentChange struct {
	Name  string
	Value string
	Unset bool
}

// ProcessEnvironmentSnapshot describes a process environment observed by the
// host. Successful reads and changes return the actual current state; after an
// OS mutation or rollback failure, Apply returns the actual state it could
// observe. Generation changes only when the observed environment changes and
// lets plugins detect edits made outside their own state.
type ProcessEnvironmentSnapshot struct {
	Generation uint64
	Variables  []ProcessEnvironmentVariable
}

// ProcessEnvironmentHost is an optional in-process capability. Applying a
// batch also schedules the same changes for f4's existing local shell
// workspaces; remote shells are deliberately outside this process-global API.
type ProcessEnvironmentHost interface {
	SnapshotProcessEnvironment() ProcessEnvironmentSnapshot
	ApplyProcessEnvironment([]ProcessEnvironmentChange) (ProcessEnvironmentSnapshot, error)
}

// TextEditorRequest opens an editor over supplied UTF-8 content. When
// Temporary is false, VFS and Path name the create-new save target. Temporary
// requests are backed by a core-owned scratch file removed on close.
type TextEditorRequest struct {
	VFS          VFS
	Path         string
	DisplayTitle string
	Content      []byte
	Modified     bool
	CursorLine   int
	CursorCol    int
	Temporary    bool
	// TargetKnownAbsent lets a caller that already performed the potentially
	// slow VFS probe off the UI goroutine skip the host's redundant check.
	// The editor still enforces no-replace semantics on its first save.
	TargetKnownAbsent bool
	OnClose           func([]byte, error)
}

// TextEditorHost is an optional UI capability exposed by PanelsFrame.
type TextEditorHost interface {
	OpenTextEditor(TextEditorRequest) error
}

// SelectedIsDirHost is an optional App capability implemented by hosts that
// can answer "is the current selection a directory?" synchronously, from
// already-cached panel state, without any new filesystem round trip
// (f4#1356). PanelsFrame implements it by reading the cursor entry's cached
// vfs.VFSItem.IsDir, exactly what GetSelectedName already reads to name that
// entry -- so this costs no extra I/O over what a Visible/Enabled predicate
// already pays.
//
// known is false when the host cannot answer at all right now (for example,
// nothing under the cursor); it is deliberately not a place for a host to
// report "unknown" merely because a real Stat would be needed, since no
// current implementation needs one. Callers must treat known==false the
// same as "this host does not implement SelectedIsDirHost at all": absence
// of information, never grounds to assume either true or false.
type SelectedIsDirHost interface {
	GetSelectedIsDir() (isDir bool, known bool)
}

// SelectionToken is an opaque handle returned by
// SelectionClearHost.CaptureSelectionToken.
type SelectionToken interface {
	// Clear drops the captured entry's selection, but only if it is still
	// exactly what was captured (same panel, same directory, same VFS
	// instance, entry still selected). It reports whether it actually
	// cleared anything.
	Clear() bool
}

// SelectionClearHost is an optional App capability that lets a plugin drop a
// panel entry's explicit ("Insert"/marked) selection once whatever it
// started on that entry finishes successfully, without touching entries the
// user selected or deselected in the meantime (f4#1623: after a successful
// checksum generation or validation, the processed files and folders should
// no longer be marked). It mirrors the capture-then-clear pair f4's own
// "Apply command" already uses on marked panel entries
// (internal/panel/apply.go, PanelSelectionToken): capture a token right
// before work starts, then Clear it once the work is done.
type SelectionClearHost interface {
	// CaptureSelectionToken snapshots whether name is currently selected on
	// the active panel. exists is false when name is not currently selected
	// (or the host has no active panel to ask) -- there is then nothing to
	// capture and nothing to clear later.
	CaptureSelectionToken(name string) (token SelectionToken, exists bool)
}
