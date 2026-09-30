//go:build !extralite

package macro

// Load-time compatibility with the Far 3 / luafar macro vocabulary (f4#1686,
// step 7). A macro file is one chunk, so a single missing global at its top -
// `local F = far.Flags`, `win.Uuid(far.Guids.X)` - aborts the whole file and
// every Macro{} in it is lost. What is here makes such files load and run with
// the answers of a program that has none of Far's own windows: no plugin
// panels, no Far dialogs or menus to be inside of, flags that are all zero. A
// script that tests for them takes its "not in that window" branch, which is
// the truth in f4.
//
// The list of what to cover was taken from a corpus of Far 3's own macros
// (FarManager's extra/Addons/Macros, 49 scripts): the names are the ones the
// most scripts use.

import (
	"crypto/sha256"
	"fmt"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// installCompat adds the tables the Far 3 macros reach for at load time.
func (e *LuaMacroEngine) installCompat(L *lua.LState) {
	// Object is the object the macro runs in: the panel in the panels' areas.
	L.SetGlobal("Object", dynamicTable(L, func(key string) lua.LValue {
		switch strings.ToLower(key) {
		case "bof":
			return lua.LBool(e.host.Panel(true).Bof)
		case "eof":
			return lua.LBool(e.host.Panel(true).Eof)
		case "empty":
			return lua.LBool(e.host.Panel(true).Empty)
		case "selected":
			return lua.LBool(e.host.Panel(true).SelCount > 0)
		}
		return lua.LNil
	}))

	// Menu, Dlg: Far's own menus and dialogs, none of which f4 has to be in.
	L.SetGlobal("Menu", dynamicTable(L, func(key string) lua.LValue {
		switch strings.ToLower(key) {
		case "value", "id":
			return lua.LString("")
		}
		return lua.LNil
	}))
	L.SetGlobal("Dlg", dynamicTable(L, func(key string) lua.LValue {
		if strings.EqualFold(key, "id") {
			return lua.LString("")
		}
		return lua.LNil
	}))

	// Editor, Viewer state words and the mouse: zero, as when nothing is open.
	L.SetGlobal("Editor", dynamicTable(L, func(key string) lua.LValue {
		switch strings.ToLower(key) {
		case "state", "pos", "sel":
			return lua.LNumber(0)
		case "selvalue":
			return lua.LString("")
		}
		return lua.LNil
	}))
	L.SetGlobal("Viewer", dynamicTable(L, func(key string) lua.LValue {
		if strings.EqualFold(key, "state") {
			return lua.LNumber(0)
		}
		return lua.LNil
	}))
	L.SetGlobal("Mouse", dynamicTable(L, func(key string) lua.LValue {
		switch strings.ToLower(key) {
		case "x", "y":
			return lua.LNumber(0)
		}
		return lua.LNil
	}))

	// Far's macro environment has the bit operations as plain globals too.
	if bits, ok := L.GetGlobal("bit").(*lua.LTable); ok {
		for _, name := range []string{"band", "bor", "bxor", "bnot", "lshift", "rshift"} {
			L.SetGlobal(name, bits.RawGetString(name))
		}
	}

	e.installPanelAPI(L)
	e.installRegex(L)

	// editor.GetInfo([id]): the editor on top, or nil when there is none.
	editorNS := L.NewTable()
	L.SetFuncs(editorNS, map[string]lua.LGFunction{
		"GetInfo": func(L *lua.LState) int {
			host, ok := e.host.(MacroEditorHost)
			if !ok {
				L.Push(lua.LNil)
				return 1
			}
			info, ok := host.EditorInfo()
			if !ok {
				L.Push(lua.LNil)
				return 1
			}
			t := L.NewTable()
			t.RawSetString("FileName", lua.LString(info.FileName))
			t.RawSetString("CurLine", lua.LNumber(info.CurLine))
			t.RawSetString("CurPos", lua.LNumber(info.CurPos))
			t.RawSetString("TotalLines", lua.LNumber(info.TotalLines))
			t.RawSetString("TabSize", lua.LNumber(info.TabSize))
			t.RawSetString("Options", lua.LNumber(0))
			t.RawSetString("CurTabPos", lua.LNumber(info.CurPos))
			L.Push(t)
			return 1
		},
	})
	L.SetGlobal("editor", editorNS)

	// win: only what a script needs at load time. Uuid turns a GUID string
	// into Far's binary form; here the string is its own form.
	win := L.NewTable()
	L.SetFuncs(win, map[string]lua.LGFunction{
		"Uuid": func(L *lua.LState) int {
			L.Push(lua.LString(L.CheckString(1)))
			return 1
		},
	})
	L.SetGlobal("win", win)
}

// The two panel-API generations of Far 3 number the panels oppositely:
// panel.*(handle, which) has 1 for the active panel and 0 for the passive,
// Panel.*(which, ...) has 0 for the active and 1 for the passive.
func panelWhichNew(v lua.LValue) bool { return lua.LVAsNumber(v) != 0 }
func panelWhichOld(v lua.LValue) bool { return lua.LVAsNumber(v) == 0 }

const fileAttributeDirectory = 0x10

// installPanelAPI adds Far 3's panel.* functions and Panel.* macro functions,
// over the host's panels as file panels. They exist for a host that has
// MacroPanelHost (and Panel(), always); without it they answer nothing.
func (e *LuaMacroEngine) installPanelAPI(L *lua.LState) {
	entry := func(active bool, index int) (MacroPanelEntry, bool) {
		if host, ok := e.host.(MacroPanelHost); ok {
			return host.PanelEntry(active, index)
		}
		return MacroPanelEntry{}, false
	}
	panelHost, hasHost := e.host.(MacroPanelHost)

	panelNS := L.NewTable()
	L.SetFuncs(panelNS, map[string]lua.LGFunction{
		"CheckPanelsExist": func(L *lua.LState) int { L.Push(lua.LTrue); return 1 },
		"GetPanelInfo": func(L *lua.LState) int {
			info := e.host.Panel(panelWhichNew(L.Get(2)))
			t := L.NewTable()
			t.RawSetString("PanelType", lua.LNumber(1)) // PTYPE_FILEPANEL
			t.RawSetString("ItemsNumber", lua.LNumber(info.ItemCount))
			t.RawSetString("CurrentItem", lua.LNumber(info.CurPos))
			t.RawSetString("TopPanelItem", lua.LNumber(info.TopPos))
			t.RawSetString("SelectedItemsNumber", lua.LNumber(info.SelCount))
			t.RawSetString("ViewMode", lua.LNumber(0))
			t.RawSetString("Flags", lua.LNumber(0))
			L.Push(t)
			return 1
		},
		"GetPanelDirectory": func(L *lua.LState) int {
			t := L.NewTable()
			t.RawSetString("Name", lua.LString(e.host.Panel(panelWhichNew(L.Get(2))).Path))
			L.Push(t)
			return 1
		},
		"SetPanelDirectory": func(L *lua.LState) int {
			ok := hasHost && panelHost.SetPanelPath(panelWhichNew(L.Get(2)), L.CheckString(3))
			L.Push(lua.LBool(ok))
			return 1
		},
		"GetPanelItem": func(L *lua.LState) int {
			row, ok := entry(panelWhichNew(L.Get(2)), L.CheckInt(3))
			if !ok {
				L.Push(lua.LNil)
				return 1
			}
			t := L.NewTable()
			t.RawSetString("FileName", lua.LString(row.Name))
			attr := 0
			if row.IsDir {
				attr = fileAttributeDirectory
			}
			t.RawSetString("FileAttributes", lua.LNumber(attr))
			t.RawSetString("FileSize", lua.LNumber(row.Size))
			t.RawSetString("Selected", lua.LBool(row.Selected))
			L.Push(t)
			return 1
		},
		"RedrawPanel": func(L *lua.LState) int {
			pos := 0
			switch v := L.Get(3).(type) {
			case *lua.LTable:
				pos = int(lua.LVAsNumber(v.RawGetString("CurrentItem")))
			case lua.LNumber:
				pos = int(v)
			}
			if hasHost && pos > 0 {
				panelHost.SetPanelPos(panelWhichNew(L.Get(2)), pos)
			}
			L.Push(lua.LTrue)
			return 1
		},
	})
	L.SetGlobal("panel", panelNS)

	// Panel.* of the macro API: Panel.Item(which, index, what) with what 0/1
	// the name, 2 the attributes, 6 the size, 8 whether it is selected.
	macroPanel := L.NewTable()
	L.SetFuncs(macroPanel, map[string]lua.LGFunction{
		"Item": func(L *lua.LState) int {
			row, ok := entry(panelWhichOld(L.Get(1)), L.CheckInt(2))
			if !ok {
				L.Push(lua.LNil)
				return 1
			}
			switch int(lua.LVAsNumber(L.Get(3))) {
			case 0, 1:
				L.Push(lua.LString(row.Name))
			case 2:
				if row.IsDir {
					L.Push(lua.LNumber(fileAttributeDirectory))
				} else {
					L.Push(lua.LNumber(0))
				}
			case 6:
				L.Push(lua.LNumber(row.Size))
			case 8:
				L.Push(lua.LBool(row.Selected))
			default:
				L.Push(lua.LNil)
			}
			return 1
		},
		"SetPosIdx": func(L *lua.LState) int {
			ok := hasHost && panelHost.SetPanelPos(panelWhichOld(L.Get(1)), L.CheckInt(2))
			if ok {
				L.Push(lua.LNumber(L.CheckInt(2)))
			} else {
				L.Push(lua.LNumber(0))
			}
			return 1
		},
		"SetPos": func(L *lua.LState) int {
			L.Push(lua.LBool(hasHost && panelHost.SetPanelName(panelWhichOld(L.Get(1)), L.CheckString(2))))
			return 1
		},
		"SetPath": func(L *lua.LState) int {
			ok := hasHost && panelHost.SetPanelPath(panelWhichOld(L.Get(1)), L.CheckString(2))
			if ok && L.GetTop() >= 3 && L.Get(3).Type() == lua.LTString {
				panelHost.SetPanelName(panelWhichOld(L.Get(1)), L.CheckString(3))
			}
			L.Push(lua.LBool(ok))
			return 1
		},
	})
	L.SetGlobal("Panel", macroPanel)
}

// farConstants adds far.Flags and far.Guids to the far namespace: every flag
// is zero (so a test of it is false), every GUID a stable string of its own
// name (so it never equals the id of a window f4 has).
func farConstants(L *lua.LState, namespace *lua.LTable) {
	namespace.RawSetString("Flags", dynamicTable(L, func(string) lua.LValue { return lua.LNumber(0) }))
	namespace.RawSetString("Guids", dynamicTable(L, func(key string) lua.LValue {
		sum := sha256.Sum256([]byte(key))
		return lua.LString(fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16]))
	}))
}

// postponedCall is a function mf.postmacro left for after the macro.
type postponedCall struct {
	fn   lua.LValue
	args []lua.LValue
}

// runPostponed calls what mf.postmacro queued, in order; a call may queue more.
func (e *LuaMacroEngine) runPostponed(L *lua.LState) error {
	for len(e.postponed) > 0 {
		call := e.postponed[0]
		e.postponed = e.postponed[1:]
		L.Push(call.fn)
		for _, a := range call.args {
			L.Push(a)
		}
		if err := L.PCall(len(call.args), 0, nil); err != nil {
			return err
		}
	}
	return nil
}

// luaPostMacro is mf.postmacro(fn, ...): run fn after the macro that called it.
func (e *LuaMacroEngine) luaPostMacro(L *lua.LState) int {
	fn := L.CheckFunction(1)
	call := postponedCall{fn: fn}
	for i := 2; i <= L.GetTop(); i++ {
		call.args = append(call.args, L.Get(i))
	}
	e.postponed = append(e.postponed, call)
	L.Push(lua.LTrue)
	return 1
}

// macroFsplit is mf.fsplit(path, flags): parts of a path, joined - 1 the drive,
// 2 the directory (with its closing slash), 4 the name, 8 the extension (with
// its dot); flags 0 keeps them all.
func macroFsplit(L *lua.LState) int {
	path := L.CheckString(1)
	flags := 15
	if L.GetTop() >= 2 {
		if f := L.CheckInt(2); f != 0 {
			flags = f
		}
	}
	rest, drive := path, ""
	if len(rest) >= 2 && rest[1] == ':' {
		drive, rest = rest[:2], rest[2:]
	}
	dir := ""
	if i := strings.LastIndexAny(rest, `/\`); i >= 0 {
		dir, rest = rest[:i+1], rest[i+1:]
	}
	name, ext := rest, ""
	if i := strings.LastIndex(rest, "."); i > 0 {
		name, ext = rest[:i], rest[i:]
	}
	var out strings.Builder
	for _, part := range []struct {
		flag int
		text string
	}{{1, drive}, {2, dir}, {4, name}, {8, ext}} {
		if flags&part.flag != 0 {
			out.WriteString(part.text)
		}
	}
	L.Push(lua.LString(out.String()))
	return 1
}

// installClipboard adds far.CopyToClipboard / far.PasteFromClipboard.
func (e *LuaMacroEngine) installClipboard(namespace *lua.LTable, L *lua.LState) {
	L.SetFuncs(namespace, map[string]lua.LGFunction{
		"CopyToClipboard": func(L *lua.LState) int {
			host, ok := e.host.(MacroClipboardHost)
			if ok {
				host.SetClipboard(lua.LVAsString(L.Get(1)))
			}
			L.Push(lua.LBool(ok))
			return 1
		},
		"PasteFromClipboard": func(L *lua.LState) int {
			host, ok := e.host.(MacroClipboardHost)
			if !ok {
				L.Push(lua.LNil)
				return 1
			}
			L.Push(lua.LString(host.Clipboard()))
			return 1
		},
	})
}
