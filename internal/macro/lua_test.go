package macro

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/vtinput"
	lua "github.com/yuin/gopher-lua"
)

// fakeMacroHost stands in for f4's UI so the engine can be tested without a
// terminal. That it is possible at all is the point of the MacroHost seam.
type fakeMacroHost struct {
	mu         sync.Mutex
	area       string
	panels     map[bool]MacroPanelInfo
	cmdLine    string
	width      int
	height     int
	title      string
	injected   []*vtinput.InputEvent
	messages   []string
	logs       []string
	pluginCall func(context.Context, string, []any) ([]any, error)
}

func newFakeMacroHost() *fakeMacroHost {
	return &fakeMacroHost{
		area:   "Shell",
		panels: map[bool]MacroPanelInfo{},
		width:  80,
		height: 25,
		title:  "f4-test",
	}
}

func (h *fakeMacroHost) CurrentArea() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.area
}

func (h *fakeMacroHost) Panel(active bool) MacroPanelInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.panels[active]
}

func (h *fakeMacroHost) CommandLine() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cmdLine
}

func (h *fakeMacroHost) ScreenSize() (int, int) { return h.width, h.height }
func (h *fakeMacroHost) Version() string        { return "f4-test" }
func (h *fakeMacroHost) WindowTitle() string    { return h.title }

func (h *fakeMacroHost) Message(title, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = append(h.messages, title+"|"+text)
}

func (h *fakeMacroHost) InjectKeys(keys []*vtinput.InputEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.injected = append(h.injected, keys...)
}

func (h *fakeMacroHost) Log(format string, args ...any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, format)
}

func (h *fakeMacroHost) RunAction(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, "action:"+name)
	return true
}

func (h *fakeMacroHost) CallPlugin(ctx context.Context, id string, args []any) ([]any, error) {
	if h.pluginCall == nil {
		return nil, errMacroCallProviderNotFound
	}
	return h.pluginCall(ctx, id, args)
}

func (h *fakeMacroHost) injectedKeys() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := make([]string, 0, len(h.injected))
	for _, event := range h.injected {
		names = append(names, keymap.EventToFarString(event))
	}
	return names
}

func newTestMacroEngine(t *testing.T, host MacroHost, source string) *LuaMacroEngine {
	t.Helper()
	Engine, err := NewLuaMacroEngine(host)
	if err != nil {
		t.Fatalf("NewLuaMacroEngine: %v", err)
	}
	t.Cleanup(func() {
		if err := Engine.Close(); err != nil {
			t.Errorf("close Lua macro engine: %v", err)
		}
	})

	if source != "" {
		if err := Engine.LoadString("test", source); err != nil {
			t.Fatalf("LoadString: %v", err)
		}
	}
	return Engine
}

// fireMacro triggers a key and waits for the macro to finish, since macro
// execution is asynchronous by design.
func fireMacro(t *testing.T, Engine *LuaMacroEngine, key string) bool {
	t.Helper()
	consumed := Engine.Trigger(Engine.host.CurrentArea(), keymap.ParseFarKey(key))
	if !Engine.WaitIdle(5 * time.Second) {
		t.Fatal("macro did not finish in time")
	}
	return consumed
}

// macroGlobals reads globals back out of the interpreter, which is how these
// tests observe what an action did.
func macroGlobals(t *testing.T, Engine *LuaMacroEngine, names ...string) map[string]lua.LValue {
	t.Helper()
	values := make(map[string]lua.LValue, len(names))
	err := Engine.rt.Do(func(L *lua.LState) error {
		for _, name := range names {
			values[name] = L.GetGlobal(name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading globals: %v", err)
	}
	return values
}

func TestMacroRegistration(t *testing.T) {
	Engine := newTestMacroEngine(t, newFakeMacroHost(), `
		Macro { area = "Shell Editor"; key = "CtrlA CtrlB"; description = "two by two";
			action = function() end }
	`)

	if Engine.Count() != 1 {
		t.Fatalf("Count = %d, want 1", Engine.Count())
	}
	for _, area := range []string{"Shell", "shell", "Editor"} {
		for _, key := range []string{"CtrlA", "ctrla", "CtrlB"} {
			if Engine.Find(area, key) == nil {
				t.Errorf("Find(%q, %q) found nothing", area, key)
			}
		}
	}
	if Engine.Find("Viewer", "CtrlA") != nil {
		t.Error("a Shell macro leaked into the Viewer area")
	}
	if Engine.Find("Shell", "CtrlC") != nil {
		t.Error("an unbound key resolved to a macro")
	}
}

func TestMacroCommonFallbackAndTerminalAlias(t *testing.T) {
	Engine := newTestMacroEngine(t, newFakeMacroHost(), `
		Macro { key = "CtrlG"; action = function() end }
		Macro { area = "Shell"; key = "CtrlH"; action = function() end }
	`)

	if Engine.Find("Viewer", "CtrlG") == nil {
		t.Error("a macro without an area did not fall back to common")
	}
	if Engine.Find("Terminal", "CtrlH") == nil {
		t.Error("the Terminal area did not resolve to Far's shell")
	}
}

func TestMacroLastRegistrationWins(t *testing.T) {
	Engine := newTestMacroEngine(t, newFakeMacroHost(), `
		Macro { area = "Shell"; key = "CtrlJ"; description = "first"; action = function() Keys("F1") end }
		Macro { area = "Shell"; key = "CtrlJ"; description = "second"; action = function() Keys("F2") end }
	`)

	macro := Engine.Find("Shell", "CtrlJ")
	if macro == nil || macro.Description != "second" {
		t.Fatalf("Find returned %v, want the second registration", macro)
	}
}

func TestMacroRejectsIncompleteDeclarations(t *testing.T) {
	Engine := newTestMacroEngine(t, newFakeMacroHost(), "")

	if err := Engine.LoadString("bad", `Macro { key = "CtrlK" }`); err == nil {
		t.Error("a macro without an action was accepted")
	}
	if err := Engine.LoadString("bad", `Macro { action = function() end }`); err == nil {
		t.Error("a macro without a key was accepted")
	}
	if Engine.Count() != 0 {
		t.Errorf("Count = %d, want 0", Engine.Count())
	}
}

func TestMacroKeysAreInjected(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Shell"; key = "CtrlL"; action = function()
			Keys("F5 Enter")
			Keys("Esc")
		end }
	`)

	if !fireMacro(t, Engine, "CtrlL") {
		t.Fatal("the trigger key was not consumed")
	}
	got := strings.Join(host.injectedKeys(), " ")
	if got != "F5 Enter Esc" {
		t.Fatalf("injected %q, want \"F5 Enter Esc\"", got)
	}
}

func TestMacroUnboundKeyIsNotConsumed(t *testing.T) {
	Engine := newTestMacroEngine(t, newFakeMacroHost(), `
		Macro { area = "Shell"; key = "CtrlM"; action = function() end }
	`)

	if Engine.Trigger("Shell", keymap.ParseFarKey("CtrlN")) {
		t.Fatal("an unbound key was consumed")
	}
}

func TestMacroConditionDeclinesAndReplaysTheKey(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		ran = false
		Macro { area = "Shell"; key = "CtrlO";
			condition = function() return false end;
			action = function() ran = true; Keys("F9") end }
	`)

	if !fireMacro(t, Engine, "CtrlO") {
		t.Fatal("the trigger key was not consumed")
	}
	if macroGlobals(t, Engine, "ran")["ran"] == lua.LTrue {
		t.Error("the action ran even though the condition declined")
	}

	got := host.injectedKeys()
	if len(got) != 1 || got[0] != "CtrlO" {
		t.Fatalf("injected %v, want the original key replayed", got)
	}
}

func TestMacroConditionAccepts(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Shell"; key = "CtrlP";
			condition = function(key) return key == "CtrlP" end;
			action = function() Keys("Tab") end }
	`)

	fireMacro(t, Engine, "CtrlP")
	if got := host.injectedKeys(); len(got) != 1 || got[0] != "Tab" {
		t.Fatalf("injected %v, want [Tab]", got)
	}
}

func TestMacroAKeyAndExit(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Shell"; key = "CtrlQ"; action = function()
			invoked = akey()
			Keys("F3")
			exit()
			Keys("F4")
		end }
	`)

	fireMacro(t, Engine, "CtrlQ")

	if got := lua.LVAsString(macroGlobals(t, Engine, "invoked")["invoked"]); got != "CtrlQ" {
		t.Errorf("akey() returned %q, want CtrlQ", got)
	}
	if got := host.injectedKeys(); len(got) != 1 || got[0] != "F3" {
		t.Fatalf("injected %v, want only the keys queued before exit", got)
	}
}

func TestMacroSeesArea(t *testing.T) {
	host := newFakeMacroHost()
	host.area = "Editor"
	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Editor"; key = "CtrlR"; action = function()
			current = Area.Current
			in_editor = Area.Editor
			in_shell = Area.Shell
		end }
	`)

	fireMacro(t, Engine, "CtrlR")

	values := macroGlobals(t, Engine, "current", "in_editor", "in_shell")
	if got := lua.LVAsString(values["current"]); got != "Editor" {
		t.Errorf("Area.Current = %q, want Editor", got)
	}
	if values["in_editor"] != lua.LTrue {
		t.Error("Area.Editor was not true in the Editor area")
	}
	if values["in_shell"] != lua.LFalse {
		t.Error("Area.Shell was true in the Editor area")
	}
}

func TestMacroSeesPanelsAndCommandLine(t *testing.T) {
	host := newFakeMacroHost()
	host.panels[true] = MacroPanelInfo{
		Path: "/home/user", Current: "notes.txt", ItemCount: 12,
		SelCount: 3, CurPos: 4, Left: true, Visible: true,
	}
	host.panels[false] = MacroPanelInfo{Path: "/tmp", Current: "core", ItemCount: 1}
	host.cmdLine = "grep -r foo"

	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Shell"; key = "CtrlS"; action = function()
			apath = APanel.Path
			acur = APanel.Current
			asel = APanel.SelCount
			aleft = APanel.Left
			ppath = PPanel.Path
			cmd = CmdLine.Value
			cmdempty = CmdLine.Empty
			unknown = APanel.NoSuchField
		end }
	`)

	fireMacro(t, Engine, "CtrlS")

	values := macroGlobals(t, Engine,
		"apath", "acur", "asel", "aleft", "ppath", "cmd", "cmdempty", "unknown")

	if got := lua.LVAsString(values["apath"]); got != "/home/user" {
		t.Errorf("APanel.Path = %q", got)
	}
	if got := lua.LVAsString(values["acur"]); got != "notes.txt" {
		t.Errorf("APanel.Current = %q", got)
	}
	if got := float64(lua.LVAsNumber(values["asel"])); got != 3 {
		t.Errorf("APanel.SelCount = %v, want 3", got)
	}
	if values["aleft"] != lua.LTrue {
		t.Error("APanel.Left was not true")
	}
	if got := lua.LVAsString(values["ppath"]); got != "/tmp" {
		t.Errorf("PPanel.Path = %q, want the passive panel", got)
	}
	if got := lua.LVAsString(values["cmd"]); got != "grep -r foo" {
		t.Errorf("CmdLine.Value = %q", got)
	}
	if values["cmdempty"] != lua.LFalse {
		t.Error("CmdLine.Empty was true for a non-empty command line")
	}
	if values["unknown"] != lua.LNil {
		t.Error("an unknown panel field did not read as nil")
	}
}

func TestMacroPanelsAreReadAtAccessTime(t *testing.T) {
	host := newFakeMacroHost()
	host.panels[true] = MacroPanelInfo{Current: "before.txt"}

	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Shell"; key = "CtrlY"; action = function() seen = APanel.Current end }
	`)

	fireMacro(t, Engine, "CtrlY")
	if got := lua.LVAsString(macroGlobals(t, Engine, "seen")["seen"]); got != "before.txt" {
		t.Fatalf("APanel.Current = %q, want before.txt", got)
	}

	// Far's panel tables are live: a second run must see the new state, not a
	// snapshot taken when the table was built.
	host.mu.Lock()
	host.panels[true] = MacroPanelInfo{Current: "after.txt"}
	host.mu.Unlock()

	fireMacro(t, Engine, "CtrlY")
	if got := lua.LVAsString(macroGlobals(t, Engine, "seen")["seen"]); got != "after.txt" {
		t.Fatalf("APanel.Current = %q, want after.txt", got)
	}
}

func TestMacroMsgBox(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Shell"; key = "CtrlT"; action = function()
			msgbox("body", "Title")
		end }
	`)

	fireMacro(t, Engine, "CtrlT")

	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.messages) != 1 || host.messages[0] != "Title|body" {
		t.Fatalf("messages = %v", host.messages)
	}
}

func TestMacroStringHelpers(t *testing.T) {
	Engine := newTestMacroEngine(t, newFakeMacroHost(), "")

	cases := []struct {
		expression string
		want       string
	}{
		{`mf.substr("Hello world", 6)`, "world"},
		{`mf.substr("Hello world", 0, 5)`, "Hello"},
		{`mf.substr("Hello", 10)`, ""},
		{`tostring(mf.index("Hello", "llo"))`, "2"},
		{`tostring(mf.index("Hello", "zzz"))`, "-1"},
		{`tostring(mf.rindex("abcabc", "b"))`, "4"},
		{`mf.lcase("ABC")`, "abc"},
		{`mf.ucase("abc")`, "ABC"},
		{`mf.trim("  x  ")`, "x"},
		{`mf.replace("a-b-c", "-", "+")`, "a+b+c"},
		{`mf.iif(1 == 1, "yes", "no")`, "yes"},
		{`mf.iif(false, "yes", "no")`, "no"},
		{`tostring(mf.len("abcd"))`, "4"},
		{`mf.chr(65)`, "A"},
		{`tostring(mf.asc("A"))`, "65"},
		{`tostring(bit.band(12, 10))`, "8"},
		{`tostring(bit.bor(12, 10))`, "14"},
		{`tostring(bit.bxor(12, 10))`, "6"},
		{`tostring(bit.lshift(1, 4))`, "16"},
		{`tostring(bit.rshift(16, 4))`, "1"},
	}

	for _, tc := range cases {
		var got string
		err := Engine.rt.Do(func(L *lua.LState) error {
			if err := L.DoString("__result = " + tc.expression); err != nil {
				return err
			}
			got = lua.LVAsString(L.GetGlobal("__result"))
			return nil
		})
		if err != nil {
			t.Errorf("%s: %v", tc.expression, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expression, got, tc.want)
		}
	}
}

func TestMacroFarTitleReportsCurrentWindowTitle(t *testing.T) {
	host := newFakeMacroHost()
	host.title = "f4 | Panels | Linux ARM64"
	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Shell"; key = "CtrlT"; action = function()
			__title = Far.Title
		end }
	`)

	if !fireMacro(t, Engine, "CtrlT") {
		t.Fatal("title macro trigger was not consumed")
	}
	if got := lua.LVAsString(macroGlobals(t, Engine, "__title")["__title"]); got != host.title {
		t.Fatalf("Far.Title = %q, want %q", got, host.title)
	}
}

func TestMacroUnsupportedDeclarationsDoNotAbortAFile(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		Event { group = "ExitFAR"; action = function() end }
		MenuItem { description = "something" }
		Macro { area = "Shell"; key = "CtrlU"; action = function() Keys("F7") end }
	`)

	if Engine.Count() != 1 {
		t.Fatalf("Count = %d, want 1: an unsupported declaration cost the file its macros", Engine.Count())
	}
	fireMacro(t, Engine, "CtrlU")
	if got := host.injectedKeys(); len(got) != 1 || got[0] != "F7" {
		t.Fatalf("injected %v, want [F7]", got)
	}
}

func TestMacroFailingActionIsContained(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Shell"; key = "CtrlV"; action = function()
			error("boom")
		end }
	`)

	fireMacro(t, Engine, "CtrlV")

	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.logs) == 0 {
		t.Fatal("a failing macro was not reported")
	}
}

func TestMacroLoadDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "user")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(filepath.Join(dir, "a.lua"),
		`Macro { area = "Shell"; key = "CtrlW"; action = function() end }`)
	write(filepath.Join(nested, "b.lua"),
		`Macro { area = "Shell"; key = "CtrlX"; action = function() end }`)
	write(filepath.Join(dir, "notes.txt"), "ignored")
	write(filepath.Join(dir, "broken.lua"), "this is not lua")

	Engine := newTestMacroEngine(t, newFakeMacroHost(), "")

	if err := Engine.LoadDir(dir); err == nil {
		t.Error("LoadDir did not report the broken file")
	}
	if Engine.Count() != 2 {
		t.Fatalf("Count = %d, want 2: a broken file cost the other macros", Engine.Count())
	}
	if Engine.Find("Shell", "CtrlX") == nil {
		t.Error("macros in a subdirectory were not loaded")
	}
}

func TestMacroLoadDirIgnoresMissingDirectory(t *testing.T) {
	Engine := newTestMacroEngine(t, newFakeMacroHost(), "")
	if err := Engine.LoadDir(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatalf("LoadDir on a missing directory returned %v", err)
	}
}

func TestMacro_Remove(t *testing.T) {
	Engine := newTestMacroEngine(t, newFakeMacroHost(), `
		Macro { area = "Shell"; key = "CtrlZ"; action = function() Keys("F5") end }
	`)

	if Engine.Count() != 1 {
		t.Fatalf("Count = %d, want 1", Engine.Count())
	}
	if Engine.Find("Shell", "CtrlZ") == nil {
		t.Fatal("Expected to find CtrlZ macro")
	}

	if !Engine.Remove("Shell", "CtrlZ") {
		t.Fatal("Expected Remove to return true")
	}
	if Engine.Count() != 0 {
		t.Errorf("Count = %d, want 0 after Remove", Engine.Count())
	}
	if Engine.Find("Shell", "CtrlZ") != nil {
		t.Error("Expected Find to return nil after Remove")
	}

	if Engine.Remove("Shell", "CtrlZ") {
		t.Error("Expected second Remove to return false")
	}
}

func TestMacroExplicitRunReportsBusy(t *testing.T) {
	Engine := newTestMacroEngine(t, newFakeMacroHost(), `
		Macro { area = "Shell"; key = "CtrlX"; description = "Busy test";
			action = function() end }
	`)
	Engine.running.Store(true)
	defer Engine.running.Store(false)
	if Engine.Run("Shell", "CtrlX") {
		t.Fatal("Run reported success while the macro engine was busy")
	}
	if Engine.RunExact("Shell", "CtrlX") {
		t.Fatal("RunExact reported success while the macro engine was busy")
	}
}

func TestMacroMenuItemIsListedAndRuns(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		MenuItem { description = "Say hello"; action = function(menu, area)
			__menu, __area = menu, area
		end }
		MenuItem { description = "Disks only"; menu = "Disks"; area = "Shell"; action = function() end }
		MenuItem { description = "No action" }
		MenuItem { action = function() end }
	`)

	items := Engine.MenuItems("Plugins", "Shell")
	if len(items) != 1 || items[0].Description != "Say hello" {
		t.Fatalf("Plugins menu items = %+v, want only \"Say hello\"", items)
	}
	if got := Engine.MenuItems("disks", "shell"); len(got) != 1 || got[0].Description != "Disks only" {
		t.Fatalf("Disks menu items = %+v", got)
	}
	if got := Engine.MenuItems("Disks", "Editor"); len(got) != 0 {
		t.Fatalf("an item bound to Shell is offered in the Editor: %+v", got)
	}
	if Engine.RunMenuItem(99, "Plugins", "Shell") || Engine.RunMenuItem(-1, "Plugins", "Shell") {
		t.Fatal("RunMenuItem accepted an id that does not exist")
	}
	if !Engine.RunMenuItem(items[0].ID, "Plugins", "Shell") {
		t.Fatal("RunMenuItem refused a listed item")
	}
	if !Engine.WaitIdle(5 * time.Second) {
		t.Fatal("the menu item never finished")
	}
	got := macroGlobals(t, Engine, "__menu", "__area")
	if lua.LVAsString(got["__menu"]) != "Plugins" || lua.LVAsString(got["__area"]) != "Shell" {
		t.Fatalf("action got menu=%v area=%v", got["__menu"], got["__area"])
	}
	if (*LuaMacroEngine)(nil).MenuItems("Plugins", "Shell") != nil || (*LuaMacroEngine)(nil).RunMenuItem(0, "", "") {
		t.Fatal("a nil engine has menu items")
	}
}

func TestMacroCommandLineIsListedAndRuns(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		CommandLine { description = "Echo"; prefixes = "Echo:say"; action = function(prefix, text)
			__prefix, __text = prefix, text
		end }
		CommandLine { prefixes = "" ; action = function() end }
		CommandLine { prefixes = "nope" }
	`)

	got := Engine.CommandLinePrefixes()
	if len(got) != 2 || got[0].Prefix != "echo" || got[1].Prefix != "say" || got[0].Description != "Echo" {
		t.Fatalf("prefixes = %+v, want echo and say of one declaration", got)
	}
	if Engine.RunCommandLine(9, "echo", "x") || Engine.RunCommandLine(-1, "echo", "x") {
		t.Fatal("RunCommandLine accepted an id that does not exist")
	}
	if !Engine.RunCommandLine(got[1].ID, "say", "hello world") {
		t.Fatal("RunCommandLine refused a listed declaration")
	}
	if !Engine.WaitIdle(5 * time.Second) {
		t.Fatal("the command never finished")
	}
	values := macroGlobals(t, Engine, "__prefix", "__text")
	if lua.LVAsString(values["__prefix"]) != "say" || lua.LVAsString(values["__text"]) != "hello world" {
		t.Fatalf("action got prefix=%v text=%v", values["__prefix"], values["__text"])
	}
	if (*LuaMacroEngine)(nil).CommandLinePrefixes() != nil || (*LuaMacroEngine)(nil).RunCommandLine(0, "", "") {
		t.Fatal("a nil engine has command lines")
	}
}

func TestMacroEventExitFARRuns(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		__n = 0
		Event { group = "ExitFAR"; description = "first"; action = function(group) __n = __n + 1; __group = group end }
		Event { group = "exitfar"; action = function() __n = __n + 10 end }
		Event { group = "DialogEvent"; action = function() __n = __n + 100 end }
		Event { group = "ExitFAR" }
		Event { action = function() end }
	`)

	if got := Engine.RunEvents("ExitFAR", 5*time.Second); got != 2 {
		t.Fatalf("RunEvents ran %d actions, want the 2 declared for ExitFAR", got)
	}
	values := macroGlobals(t, Engine, "__n", "__group")
	if lua.LVAsNumber(values["__n"]) != 11 || lua.LVAsString(values["__group"]) != "ExitFAR" {
		t.Fatalf("n=%v group=%v, want 11 and ExitFAR", values["__n"], values["__group"])
	}
	if Engine.RunEvents("Nothing", time.Second) != 0 || (*LuaMacroEngine)(nil).RunEvents("ExitFAR", time.Second) != 0 {
		t.Fatal("events ran for a group nobody declared, or on a nil engine")
	}
	host.mu.Lock()
	logged := len(host.logs)
	host.mu.Unlock()
	if logged < 3 {
		t.Errorf("the unsupported group and the two malformed declarations were not logged (%d entries)", logged)
	}
}

func TestMacroManagerRunExitEventsIsSafe(t *testing.T) {
	(*MacroManager)(nil).RunExitEvents()
	(&MacroManager{}).RunExitEvents()
}

// dialogHost is a fake host that can also ask the user something.
type dialogHost struct {
	*fakeMacroHost
	inputText  string
	inputOK    bool
	menuAnswer int
	asked      []string
	delay      time.Duration // how long the user takes
}

func (h *dialogHost) InputBox(title, prompt, initial string) (string, bool) {
	h.asked = append(h.asked, title+"|"+prompt+"|"+initial)
	time.Sleep(h.delay)
	return h.inputText, h.inputOK
}

func (h *dialogHost) Menu(title string, items []string) int {
	h.asked = append(h.asked, title+"|"+strings.Join(items, ","))
	time.Sleep(h.delay)
	return h.menuAnswer
}

func TestMacroFarInputBoxAndMenu(t *testing.T) {
	host := &dialogHost{fakeMacroHost: newFakeMacroHost(), inputText: "typed", inputOK: true, menuAnswer: 1}
	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Shell"; key = "CtrlT"; action = function()
			__text = far.InputBox("Title", "Prompt:", "start")
			__item, __pos = far.Menu({ Title = "Pick" }, { "one", { text = "two" }, "three" })
			__empty = far.Menu({ Title = "None" }, {})
		end }
	`)
	if !fireMacro(t, Engine, "CtrlT") {
		t.Fatal("macro not consumed")
	}
	if !Engine.WaitIdle(5 * time.Second) {
		t.Fatal("macro never finished")
	}
	values := macroGlobals(t, Engine, "__text", "__item", "__pos", "__empty")
	if lua.LVAsString(values["__text"]) != "typed" {
		t.Errorf("InputBox = %v", values["__text"])
	}
	if item, ok := values["__item"].(*lua.LTable); !ok || lua.LVAsString(item.RawGetString("text")) != "two" || lua.LVAsNumber(values["__pos"]) != 2 {
		t.Errorf("Menu = %v at %v, want the second item at 2", values["__item"], values["__pos"])
	}
	if values["__empty"] != lua.LNil {
		t.Errorf("a menu with no items answered %v", values["__empty"])
	}
	if len(host.asked) != 2 || host.asked[0] != "Title|Prompt:|start" || host.asked[1] != "Pick|one,two,three" {
		t.Errorf("asked %v", host.asked)
	}

	// Cancelled.
	host.inputOK, host.menuAnswer = false, -1
	fireMacro(t, Engine, "CtrlT")
	Engine.WaitIdle(5 * time.Second)
	values = macroGlobals(t, Engine, "__text", "__item")
	if values["__text"] != lua.LNil || values["__item"] != lua.LNil {
		t.Errorf("cancelled dialogs answered %v and %v", values["__text"], values["__item"])
	}
}

// A host that cannot ask gets nils, not a failing macro.
func TestMacroFarDialogsWithoutAHostThatAsks(t *testing.T) {
	Engine := newTestMacroEngine(t, newFakeMacroHost(), `
		Macro { area = "Shell"; key = "CtrlT"; action = function()
			__a = far.InputBox("t", "p")
			__b = far.Menu({}, { "x" })
			__done = true
		end }
	`)
	fireMacro(t, Engine, "CtrlT")
	Engine.WaitIdle(5 * time.Second)
	values := macroGlobals(t, Engine, "__a", "__b", "__done")
	if values["__a"] != lua.LNil || values["__b"] != lua.LNil || values["__done"] != lua.LTrue {
		t.Errorf("a = %v, b = %v, done = %v", values["__a"], values["__b"], values["__done"])
	}
}

// The time the user spends in a dialog does not count against the macro's
// deadline; the same delay in the script itself does.
func TestMacroDeadlineStandsStillWhileADialogWaits(t *testing.T) {
	old := macroCallTimeout
	macroCallTimeout = 300 * time.Millisecond
	t.Cleanup(func() { macroCallTimeout = old })

	host := &dialogHost{fakeMacroHost: newFakeMacroHost(), inputText: "ok", inputOK: true, menuAnswer: 0, delay: 800 * time.Millisecond}
	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Shell"; key = "CtrlT"; action = function()
			__text = far.InputBox("a", "b")
			__item = far.Menu({}, { "x" })
			__after = true
		end }
	`)
	fireMacro(t, Engine, "CtrlT")
	if !Engine.WaitIdle(10 * time.Second) {
		t.Fatal("macro never finished")
	}
	if Engine.Interrupted() {
		t.Fatal("the macro was interrupted for the time the user took")
	}
	values := macroGlobals(t, Engine, "__text", "__after")
	if lua.LVAsString(values["__text"]) != "ok" || values["__after"] != lua.LTrue {
		t.Fatalf("text=%v after=%v", values["__text"], values["__after"])
	}
}

func waitForGlobal(t *testing.T, Engine *LuaMacroEngine, name string, ok func(lua.LValue) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if ok(macroGlobals(t, Engine, name)[name]) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("global %s never got the expected value", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMacroFarTimerTicksAndStops(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		__n = 0
		__timer = far.Timer(20, function(t)
			__n = __n + 1
			__self = (t == __timer)
			if __n == 3 then t.Enabled = false end
		end)
		__interval = __timer.Interval
		__bad = far.Timer(10, function() error("boom") end)
	`)
	waitForGlobal(t, Engine, "__n", func(v lua.LValue) bool { return lua.LVAsNumber(v) >= 3 })
	time.Sleep(150 * time.Millisecond)
	values := macroGlobals(t, Engine, "__n", "__self", "__interval")
	if n := lua.LVAsNumber(values["__n"]); n != 3 {
		t.Errorf("the timer ran %v times, want exactly 3 (it disabled itself)", n)
	}
	if values["__self"] != lua.LTrue || lua.LVAsNumber(values["__interval"]) != 20 {
		t.Errorf("callback argument / interval: %v %v", values["__self"], values["__interval"])
	}
	host.mu.Lock()
	logged := len(host.logs)
	host.mu.Unlock()
	if logged == 0 {
		t.Error("a failing timer callback was not logged")
	}

	// Enabled again, then closed for good.
	if err := Engine.LoadString("more.lua", `__timer.Enabled = true; __n = 0; __timer.Interval = 1; __timer:Close()`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if n := lua.LVAsNumber(macroGlobals(t, Engine, "__n")["__n"]); n != 0 {
		t.Errorf("a closed timer ticked %v times", n)
	}
}

func TestMacroFarGetConfig(t *testing.T) {
	host := &configHost{fakeMacroHost: newFakeMacroHost()}
	Engine := newTestMacroEngine(t, host, `
		__tab = far.GetConfig("Editor.TabSize")
		__flag = far.GetConfig("Editor.AutoIndent")
		__unknown = far.GetConfig("No.Such")
	`)
	values := macroGlobals(t, Engine, "__tab", "__flag", "__unknown")
	if lua.LVAsNumber(values["__tab"]) != 8 || values["__flag"] != lua.LTrue || values["__unknown"] != lua.LNil {
		t.Errorf("GetConfig: %v %v %v", values["__tab"], values["__flag"], values["__unknown"])
	}
	plain := newTestMacroEngine(t, newFakeMacroHost(), `__x = far.GetConfig("Editor.TabSize")`)
	if macroGlobals(t, plain, "__x")["__x"] != lua.LNil {
		t.Error("a host without settings answered")
	}
}

type configHost struct{ *fakeMacroHost }

func (h *configHost) ConfigValue(key string) (any, bool) {
	switch key {
	case "Editor.TabSize":
		return int64(8), true
	case "Editor.AutoIndent":
		return true, true
	}
	return nil, false
}

// Far 3's own macros open with globals like these; a file that trips over one
// loses all its Macro{} entries.
func TestMacroFar3LoadTimeGlobalsLetAFileLoad(t *testing.T) {
	host := newFakeMacroHost()
	host.panels[true] = MacroPanelInfo{Bof: true, Eof: false, SelCount: 2, Visible: true}
	Engine := newTestMacroEngine(t, host, `
		local F = far.Flags
		local WIF_MODAL = far.Flags.WIF_MODAL
		local GUID = win.Uuid(far.Guids.MakeFolderId)
		__guid = GUID
		__samegui = (far.Guids.MakeFolderId == far.Guids.MakeFolderId)
		__diff = (far.Guids.A ~= far.Guids.B)
		__flag = band(APanel.OPIFlags, far.Flags.OPIF_REALNAMES)
		Macro { area = "Shell"; key = "CtrlT"; condition = function()
			return not APanel.Plugin and APanel.FilePanel and Menu.Id ~= far.Guids.ScreensSwitchId and Dlg.Id == ""
		end; action = function()
			__bof, __eof, __sel = Object.Bof, Object.Eof, APanel.Selected
			__state = band(Editor.State, 3) + Viewer.State + Mouse.X + Editor.Pos
			__value = Menu.Value
		end }
	`)
	if Engine.Count() != 1 {
		t.Fatalf("Count = %d: the file did not load", Engine.Count())
	}
	if !fireMacro(t, Engine, "CtrlT") {
		t.Fatal("macro not consumed")
	}
	Engine.WaitIdle(5 * time.Second)
	v := macroGlobals(t, Engine, "__guid", "__samegui", "__diff", "__flag", "__bof", "__eof", "__sel", "__state", "__value")
	if len(lua.LVAsString(v["__guid"])) != 36 || v["__samegui"] != lua.LTrue || v["__diff"] != lua.LTrue {
		t.Errorf("guids: %v %v %v", v["__guid"], v["__samegui"], v["__diff"])
	}
	if lua.LVAsNumber(v["__flag"]) != 0 || v["__bof"] != lua.LTrue || v["__eof"] != lua.LFalse || v["__sel"] != lua.LTrue ||
		lua.LVAsNumber(v["__state"]) != 0 || lua.LVAsString(v["__value"]) != "" {
		t.Errorf("values: %v", v)
	}
}

// panelHost is a fake host with panels of rows.
type panelHost struct {
	*fakeMacroHost
	rows    map[bool][]MacroPanelEntry
	path    map[bool]string
	pos     map[bool]int
	gotName string
}

func (h *panelHost) PanelEntry(active bool, i int) (MacroPanelEntry, bool) {
	rows := h.rows[active]
	if i < 1 || i > len(rows) {
		return MacroPanelEntry{}, false
	}
	return rows[i-1], true
}
func (h *panelHost) SetPanelPath(active bool, p string) bool { h.path[active] = p; return p != "" }
func (h *panelHost) SetPanelPos(active bool, i int) bool     { h.pos[active] = i; return i > 0 }
func (h *panelHost) SetPanelName(active bool, n string) bool { h.gotName = n; return n != "" }

func TestMacroFar3PanelAPI(t *testing.T) {
	base := newFakeMacroHost()
	base.panels[true] = MacroPanelInfo{Path: "/home", ItemCount: 3, CurPos: 2, TopPos: 1, SelCount: 1}
	host := &panelHost{fakeMacroHost: base,
		rows: map[bool][]MacroPanelEntry{true: {{Name: "..", IsDir: true}, {Name: "a.txt", Size: 12, Selected: true}, {Name: "dir", IsDir: true}}},
		path: map[bool]string{}, pos: map[bool]int{}}
	Engine := newTestMacroEngine(t, host, `
		local ACTIVE_NEW, ACTIVE_OLD = 1, 0
		local info = panel.GetPanelInfo(nil, ACTIVE_NEW)
		__items, __cur, __type = info.ItemsNumber, info.CurrentItem, info.PanelType
		__dir = panel.GetPanelDirectory(nil, ACTIVE_NEW).Name
		__item = panel.GetPanelItem(nil, ACTIVE_NEW, 2)
		__name, __attr, __size, __sel = Panel.Item(ACTIVE_OLD, 2, 0), Panel.Item(ACTIVE_OLD, 3, 2), Panel.Item(ACTIVE_OLD, 2, 6), Panel.Item(ACTIVE_OLD, 2, 8)
		__none = Panel.Item(ACTIVE_OLD, 9, 0)
		__set = panel.SetPanelDirectory(nil, ACTIVE_NEW, "/tmp")
		__pos = Panel.SetPosIdx(ACTIVE_OLD, 3)
		__ok = Panel.SetPos(ACTIVE_OLD, "a.txt")
		__setpath = Panel.SetPath(ACTIVE_OLD, "/var", "x")
		__exist = panel.CheckPanelsExist()
		panel.RedrawPanel(nil, ACTIVE_NEW, { CurrentItem = 1 })
	`)
	v := macroGlobals(t, Engine, "__items", "__cur", "__type", "__dir", "__item", "__name", "__attr", "__size", "__sel", "__none", "__set", "__pos", "__ok", "__setpath", "__exist")
	if lua.LVAsNumber(v["__items"]) != 3 || lua.LVAsNumber(v["__cur"]) != 2 || lua.LVAsNumber(v["__type"]) != 1 || lua.LVAsString(v["__dir"]) != "/home" {
		t.Errorf("info: %v", v)
	}
	if it, ok := v["__item"].(*lua.LTable); !ok || lua.LVAsString(it.RawGetString("FileName")) != "a.txt" || lua.LVAsNumber(it.RawGetString("FileSize")) != 12 {
		t.Errorf("GetPanelItem = %v", v["__item"])
	}
	if lua.LVAsString(v["__name"]) != "a.txt" || lua.LVAsNumber(v["__attr"]) != 0x10 || lua.LVAsNumber(v["__size"]) != 12 || v["__sel"] != lua.LTrue || v["__none"] != lua.LNil {
		t.Errorf("Panel.Item: %v", v)
	}
	if v["__set"] != lua.LTrue || lua.LVAsNumber(v["__pos"]) != 3 || v["__ok"] != lua.LTrue || v["__setpath"] != lua.LTrue || v["__exist"] != lua.LTrue {
		t.Errorf("setters: %v", v)
	}
	if host.path[true] != "/var" {
		t.Errorf("the last directory set was %v, want /var (Panel.SetPath came after SetPanelDirectory)", host.path)
	}
	if host.pos[true] != 1 || host.gotName != "x" {
		t.Errorf("pos=%v name=%q (RedrawPanel goes to row 1, SetPath's third argument names a row)", host.pos, host.gotName)
	}
}

type editorHost struct {
	*fakeMacroHost
	info MacroEditorInfo
	ok   bool
}

func (h *editorHost) EditorInfo() (MacroEditorInfo, bool) { return h.info, h.ok }

func TestMacroEditorGetInfo(t *testing.T) {
	host := &editorHost{fakeMacroHost: newFakeMacroHost(), ok: true,
		info: MacroEditorInfo{FileName: "/tmp/a.txt", CurLine: 7, CurPos: 3, TotalLines: 40, TabSize: 4}}
	Engine := newTestMacroEngine(t, host, `
		local info = editor.GetInfo()
		__file, __line, __col, __total, __tab, __opts = info.FileName, info.CurLine, info.CurPos, info.TotalLines, info.TabSize, info.Options
	`)
	v := macroGlobals(t, Engine, "__file", "__line", "__col", "__total", "__tab", "__opts")
	if lua.LVAsString(v["__file"]) != "/tmp/a.txt" || lua.LVAsNumber(v["__line"]) != 7 || lua.LVAsNumber(v["__col"]) != 3 ||
		lua.LVAsNumber(v["__total"]) != 40 || lua.LVAsNumber(v["__tab"]) != 4 || lua.LVAsNumber(v["__opts"]) != 0 {
		t.Errorf("GetInfo: %v", v)
	}

	host.ok = false
	none := newTestMacroEngine(t, host, `__n = editor.GetInfo()`)
	if macroGlobals(t, none, "__n")["__n"] != lua.LNil {
		t.Error("GetInfo answered with no editor open")
	}
	plain := newTestMacroEngine(t, newFakeMacroHost(), `__n = editor.GetInfo()`)
	if macroGlobals(t, plain, "__n")["__n"] != lua.LNil {
		t.Error("a host with no editors answered")
	}
}

type clipHost struct {
	*fakeMacroHost
	text string
}

func (h *clipHost) SetClipboard(t string) { h.text = t }
func (h *clipHost) Clipboard() string     { return h.text }

func TestMacroClipboardFsplitAndPostmacro(t *testing.T) {
	host := &clipHost{fakeMacroHost: newFakeMacroHost()}
	Engine := newTestMacroEngine(t, host, `
		Macro { area = "Shell"; key = "CtrlT"; action = function()
			__copied = far.CopyToClipboard("hello")
			__pasted = far.PasteFromClipboard()
			__name = mf.fsplit("C:\\dir\\sub\\file.tar.gz", 4 + 8)
			__dir = mf.fsplit("/a/b/c.txt", 2)
			__all = mf.fsplit("D:/x/y.z")
			__bare = mf.fsplit("noext", 4 + 8)
			__order = ""
			mf.postmacro(function(a, b) __order = __order .. "P" .. a .. b end, 1, 2)
			__order = __order .. "M"
		end }
	`)
	fireMacro(t, Engine, "CtrlT")
	Engine.WaitIdle(5 * time.Second)
	v := macroGlobals(t, Engine, "__copied", "__pasted", "__name", "__dir", "__all", "__bare", "__order")
	if v["__copied"] != lua.LTrue || lua.LVAsString(v["__pasted"]) != "hello" || host.text != "hello" {
		t.Errorf("clipboard: %v %v %q", v["__copied"], v["__pasted"], host.text)
	}
	if lua.LVAsString(v["__name"]) != "file.tar.gz" || lua.LVAsString(v["__dir"]) != "/a/b/" ||
		lua.LVAsString(v["__all"]) != "D:/x/y.z" || lua.LVAsString(v["__bare"]) != "noext" {
		t.Errorf("fsplit: %v %v %v %v", v["__name"], v["__dir"], v["__all"], v["__bare"])
	}
	if lua.LVAsString(v["__order"]) != "MP12" {
		t.Errorf("postmacro ran %q, want the macro first (M) then the posted call (P12)", lua.LVAsString(v["__order"]))
	}

	plain := newTestMacroEngine(t, newFakeMacroHost(), `__c = far.CopyToClipboard("x"); __p = far.PasteFromClipboard()`)
	pv := macroGlobals(t, plain, "__c", "__p")
	if pv["__c"] != lua.LFalse || pv["__p"] != lua.LNil {
		t.Errorf("a host with no clipboard: %v %v", pv["__c"], pv["__p"])
	}
}

func TestMacroRegexNew(t *testing.T) {
	host := newFakeMacroHost()
	Engine := newTestMacroEngine(t, host, `
		local upper = regex.new("^\\U+$")            -- Far's \U: not an upper-case letter
		__u1, __u2 = upper:match("abc123"), upper:match("Abc")
		local ext = regex.new([=[
			(\w+)     # the name
			\. (\w+)  # the extension
		]=], "x")
		__n, __e = ext:match("dir/report.txt")
		__first, __last = ext:find("dir/report.txt")
		local words = regex.new("\\i+", "i")
		__all = ""
		for w in words:gmatch("ab cd_ ef") do __all = __all .. "[" .. w .. "]" end
		local sub, count = regex.new("(a)(b)"):gsub("abab xab", "%2%1")
		__sub, __cnt = sub, count
		local viaFn = regex.new("\\d+"):gsub("a1b22", function(d) return "<" .. d .. ">" end)
		__fn = viaFn
		local lookbehind = regex.new("(?<=x)y")       -- RE2 has no look-behind
		__never = lookbehind:match("xy")
		__ok = true
	`)
	v := macroGlobals(t, Engine, "__u1", "__u2", "__n", "__e", "__first", "__last", "__all", "__sub", "__cnt", "__fn", "__never", "__ok")
	if lua.LVAsString(v["__u1"]) != "abc123" || v["__u2"] != lua.LNil {
		t.Errorf("\\U: %v %v", v["__u1"], v["__u2"])
	}
	if lua.LVAsString(v["__n"]) != "report" || lua.LVAsString(v["__e"]) != "txt" || lua.LVAsNumber(v["__first"]) != 5 || lua.LVAsNumber(v["__last"]) != 14 {
		t.Errorf("extended match: %v %v %v %v", v["__n"], v["__e"], v["__first"], v["__last"])
	}
	if lua.LVAsString(v["__all"]) != "[ab][cd_][ef]" || lua.LVAsString(v["__sub"]) != "baba xba" || lua.LVAsNumber(v["__cnt"]) != 3 || lua.LVAsString(v["__fn"]) != "a<1>b<22>" {
		t.Errorf("gmatch/gsub: %v %v %v %v", v["__all"], v["__sub"], v["__cnt"], v["__fn"])
	}
	if v["__never"] != lua.LNil || v["__ok"] != lua.LTrue {
		t.Errorf("an unsupported pattern broke the file: never=%v ok=%v", v["__never"], v["__ok"])
	}
	host.mu.Lock()
	logged := len(host.logs)
	host.mu.Unlock()
	if logged == 0 {
		t.Error("the pattern RE2 cannot compile was not logged")
	}
}
