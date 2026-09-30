package macro

import (
	"context"

	"github.com/unxed/vtinput"
)

// MacroPanelInfo is the panel state a macro can see, gathered in one shot.
// Reading it costs a round trip to the UI goroutine, so it is fetched whole
// rather than field by field.
type MacroPanelInfo struct {
	Path      string
	Current   string
	ItemCount int
	SelCount  int
	CurPos    int
	TopPos    int
	IsFolder  bool
	Empty     bool
	Left      bool
	Visible   bool
	Root      bool
	Bof       bool
	Eof       bool
	Type      int
}

// MacroHost is everything the macro engine needs from f4. Keeping it an
// interface is what makes the engine testable without a terminal, and it is
// also the seam where the "must run on the UI goroutine" rule is enforced
// exactly once instead of in every API function.
type MacroHost interface {
	CurrentArea() string
	Panel(active bool) MacroPanelInfo
	CommandLine() string
	ScreenSize() (width, height int)
	Version() string
	WindowTitle() string
	Message(title, text string)
	InjectKeys(keys []*vtinput.InputEvent)
	Log(format string, args ...any)
	RunAction(name string) bool
	CallPlugin(context.Context, string, []any) ([]any, error)
}

// MacroDialogHost is what a host adds to MacroHost to let macros ask the user
// something: far.InputBox and far.Menu use it, and are nil-returning no-ops on
// a host without it. Both block the macro until the answer.
type MacroDialogHost interface {
	// InputBox shows a one-line input; ok is false when it was cancelled.
	InputBox(title, prompt, initial string) (text string, ok bool)
	// Menu shows the items; the answer is the chosen position, or -1.
	Menu(title string, items []string) int
}

// MacroPanelEntry is one row of a panel as a macro sees it.
type MacroPanelEntry struct {
	Name     string
	IsDir    bool
	Selected bool
	Size     int64
}

// MacroPanelHost is what a host adds to let macros read the rows of a panel and
// move it (panel.GetPanelItem, Panel.Item, panel.SetPanelDirectory,
// Panel.SetPosIdx...). Rows are numbered from 1 and ok is false past the ends.
type MacroPanelHost interface {
	PanelEntry(active bool, index int) (entry MacroPanelEntry, ok bool)
	// SetPanelPath changes the directory the panel shows.
	SetPanelPath(active bool, path string) bool
	// SetPanelPos puts the cursor on row index.
	SetPanelPos(active bool, index int) bool
	// SetPanelName puts the cursor on the row with that name.
	SetPanelName(active bool, name string) bool
}

// MacroEditorInfo is the editor a macro runs in, as editor.GetInfo tells it.
type MacroEditorInfo struct {
	FileName   string
	CurLine    int // 1-based
	CurPos     int // 1-based column
	TotalLines int
	TabSize    int
}

// MacroEditorHost is what a host adds to let macros ask about the editor on
// top; ok is false when there is none.
type MacroEditorHost interface {
	EditorInfo() (info MacroEditorInfo, ok bool)
}

// MacroConfigHost is what a host adds to let far.GetConfig read its settings.
type MacroConfigHost interface {
	// ConfigValue answers a setting by name: a number, a bool or a string.
	ConfigValue(key string) (value any, known bool)
}

// LuaMacroBinding is the discoverable, immutable part of a Lua macro. It is
// used by command surfaces without exposing interpreter-owned functions.
type LuaMacroBinding struct {
	Area        string
	Key         string
	Description string
	Source      string
}

// macroAreaAliases maps f4's own area names onto Far's. f4 reports Terminal
// when the panels are hidden; Far has no such area, and its Shell macros are
// what a user expects to fire there.
var macroAreaAliases = map[string]string{
	"terminal": "shell",
}

// LuaMenuItemInfo is the discoverable part of a MenuItem{} declaration: what
// a menu shows for it and the id RunMenuItem takes.
type LuaMenuItemInfo struct {
	ID          int
	Description string
	Source      string
}

// LuaCommandLineInfo is one command-line prefix a CommandLine{} declaration
// claims: what the host registers and the id RunCommandLine takes.
type LuaCommandLineInfo struct {
	ID          int
	Prefix      string
	Description string
	Source      string
}
