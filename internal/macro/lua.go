//go:build !extralite

package macro

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/luaplug"
	"github.com/unxed/vtinput"
	lua "github.com/yuin/gopher-lua"
)

// macroExitSentinel is what exit() raises. It ends the macro without being an
// error, the way Far's exit() does.
const macroExitSentinel = "f4macro:exit"

// macroCallTimeout bounds one macro. A macro is user code triggered by a key
// press, so it gets far less rope than a plugin.
var macroCallTimeout = 10 * time.Second

// LuaMacro is one Macro{} declaration.
type LuaMacro struct {
	Areas []string
	Keys  []string
	// EmptyCommandLine is Far's EmptyCommandLine macro flag. Such a macro
	// claims its key only while the host command line is empty; otherwise the
	// original key continues through the ordinary input route.
	EmptyCommandLine bool
	Description      string
	Source           string

	action    *lua.LFunction
	condition *lua.LFunction

	// callArgs are passed to action: a MenuItem{}'s action is called with the
	// menu and the area it was chosen from, as in Far.
	callArgs []string
	// callValues, when set, are passed to action instead of callArgs (an event
	// that carries numbers, such as EditorEvent).
	callValues []lua.LValue
}

// luaEvent is one Event{} declaration: the group it listens to ("ExitFAR"...)
// and what to run.
type luaEvent struct {
	group string
	macro *LuaMacro
}

// supportedEventGroups are the Event{} groups f4 raises; a declaration for
// another group is kept out and logged.
var supportedEventGroups = map[string]bool{"exitfar": true, "folderchanged": true, "editorevent": true, "viewerevent": true}

// luaCommandLine is one CommandLine{} declaration.
type luaCommandLine struct {
	prefixes []string
	macro    *LuaMacro
}

// luaMenuItem is one MenuItem{} declaration.
type luaMenuItem struct {
	menus []string
	areas []string
	macro *LuaMacro
}

// LuaMacroEngine runs Far-compatible macros written in Lua.
type LuaMacroEngine struct {
	rt   *luaplug.Runtime
	host MacroHost

	mu     sync.Mutex
	byArea map[string]map[string][]*LuaMacro
	all    []*LuaMacro

	running atomic.Bool

	items    []*luaMenuItem
	events   []*luaEvent
	timers   []*luaTimer
	cmdLines []*luaCommandLine

	// The fields below belong to the interpreter's worker goroutine while a
	// macro is running, and are read by the caller once it has finished.
	pendingKeys []*vtinput.InputEvent
	// postponed are the calls mf.postmacro asked for, run when the macro that
	// asked has returned.
	postponed  []postponedCall
	invokedKey string
}

// NewLuaMacroEngine starts an engine with no macros loaded.
func NewLuaMacroEngine(host MacroHost) (*LuaMacroEngine, error) {
	Engine := &LuaMacroEngine{
		host:   host,
		byArea: make(map[string]map[string][]*LuaMacro),
	}

	runtime, err := luaplug.New(luaplug.Options{
		Name:        "macros",
		CallTimeout: macroCallTimeout,
		Host: luaplug.HostFunc(func(method string, params any) (any, error) {
			if method == "Host.Log" {
				host.Log("MACRO: %v", params)
			}
			return nil, nil
		}),
	})
	if err != nil {
		return nil, err
	}
	Engine.rt = runtime

	if err := runtime.Do(func(L *lua.LState) error {
		Engine.installAPI(L)
		return nil
	}); err != nil {
		runtime.Close()
		return nil, err
	}
	return Engine, nil
}

// LoadDir loads every .lua file under dir, the way Far reads its Macros
// directory. A file that fails to load is reported and skipped: one broken
// macro must not cost the user all the others.
func (e *LuaMacroEngine) LoadDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}

	var failures int
	walkErr := filepath.Walk(dir, func(path string, entry os.FileInfo, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".lua") {
			return nil
		}
		if loadErr := e.rt.LoadFile(path); loadErr != nil {
			failures++
			e.host.Log("MACRO: failed to load %s: %v", path, loadErr)
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	if failures > 0 {
		return fmt.Errorf("%d macro file(s) failed to load", failures)
	}
	return nil
}

// LoadString loads macros from a chunk, which is how tests and the eventual
// macro editor feed the engine.
func (e *LuaMacroEngine) LoadString(name, source string) error {
	return e.rt.LoadString(name, source)
}

// Count reports how many macros are registered.
func (e *LuaMacroEngine) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.all)
}

func (e *LuaMacroEngine) add(m *LuaMacro) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, area := range m.Areas {
		byKey := e.byArea[area]
		if byKey == nil {
			byKey = make(map[string][]*LuaMacro)
			e.byArea[area] = byKey
		}
		for _, key := range m.Keys {
			byKey[key] = append(byKey[key], m)
		}
	}
	e.all = append(e.all, m)
}

func (e *LuaMacroEngine) addEvent(ev *luaEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
}

// RunEvents runs every Event{} declared for group (any case), one after the
// other, each with the group as its argument, and waits up to wait for all of
// them: it is what f4 calls as it exits, when nobody can be waited on for long.
// It reports how many actions ran to the end in time.
func (e *LuaMacroEngine) RunEvents(group string, wait time.Duration) int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	var todo []*LuaMacro
	for _, ev := range e.events {
		if strings.EqualFold(ev.group, group) {
			m := *ev.macro
			m.callArgs = []string{ev.group}
			todo = append(todo, &m)
		}
	}
	e.mu.Unlock()
	if len(todo) == 0 {
		return 0
	}
	done := make(chan int, 1)
	go func() {
		n := 0
		for _, m := range todo {
			e.execute(m, "", nil)
			n++
		}
		done <- n
	}()
	select {
	case n := <-done:
		return n
	case <-time.After(wait):
		return 0
	}
}

// RaiseEvent runs, in the background, every Event{} declared for group (any
// case), one after the other, each with the group as its argument. It is what
// f4 calls when something happens that macros may want to react to (a panel
// entered another folder). It reports whether anything was started: nothing is
// when no Event{} names the group, or a macro is already running, which also
// keeps an event action that itself changes the folder from raising the event
// again without end.
func (e *LuaMacroEngine) RaiseEvent(group string) bool {
	return e.raise(group, nil)
}

// RaiseEventNumbers is RaiseEvent for an event whose action is called with
// numbers instead of the group name (Far's EditorEvent gets the editor id, the
// event and a parameter).
func (e *LuaMacroEngine) RaiseEventNumbers(group string, numbers ...int) bool {
	values := make([]lua.LValue, len(numbers))
	for i, n := range numbers {
		values[i] = lua.LNumber(n)
	}
	return e.raise(group, values)
}

func (e *LuaMacroEngine) raise(group string, values []lua.LValue) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	var todo []*LuaMacro
	for _, ev := range e.events {
		if strings.EqualFold(ev.group, group) {
			m := *ev.macro
			m.callArgs = []string{ev.group}
			m.callValues = values
			todo = append(todo, &m)
		}
	}
	e.mu.Unlock()
	if len(todo) == 0 || !e.running.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer e.running.Store(false)
		for _, m := range todo {
			e.execute(m, "", nil)
		}
	}()
	return true
}

func (e *LuaMacroEngine) addCommandLine(c *luaCommandLine) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cmdLines = append(e.cmdLines, c)
}

// CommandLinePrefixes lists every prefix the CommandLine{} declarations claim,
// one entry per prefix.
func (e *LuaMacroEngine) CommandLinePrefixes() []LuaCommandLineInfo {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []LuaCommandLineInfo
	for id, c := range e.cmdLines {
		for _, prefix := range c.prefixes {
			out = append(out, LuaCommandLineInfo{ID: id, Prefix: prefix, Description: c.macro.Description, Source: c.macro.Source})
		}
	}
	return out
}

// RunCommandLine runs a CommandLine{}'s action for a line typed as
// "prefix:text": the action gets the prefix and the text after it, as in Far.
// It reports whether there was an action to run (and nothing else was running).
func (e *LuaMacroEngine) RunCommandLine(id int, prefix, text string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	var c *luaCommandLine
	if id >= 0 && id < len(e.cmdLines) {
		c = e.cmdLines[id]
	}
	e.mu.Unlock()
	if c == nil {
		return false
	}
	if !e.running.CompareAndSwap(false, true) {
		return false
	}
	macro := *c.macro
	macro.callArgs = []string{prefix, text}
	go func() {
		defer e.running.Store(false)
		e.execute(&macro, "", nil)
	}()
	return true
}

func (e *LuaMacroEngine) addMenuItem(item *luaMenuItem) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.items = append(e.items, item)
}

// MenuItems lists the MenuItem{} declarations offered in menu ("Plugins",
// "Disks" or "Config", any case) when area is the current one, or in any area
// when area is empty; a declaration that names no menu is in "Plugins", one
// that names no area is in every area.
func (e *LuaMacroEngine) MenuItems(menu, area string) []LuaMenuItemInfo {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []LuaMenuItemInfo
	for id, item := range e.items {
		if containsFold(item.menus, menu) && (area == "" || len(item.areas) == 0 || containsFold(item.areas, area) || containsFold(item.areas, "common")) {
			out = append(out, LuaMenuItemInfo{ID: id, Description: item.macro.Description, Source: item.macro.Source})
		}
	}
	return out
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// RunMenuItem runs a MenuItem{}'s action, chosen from menu while area was
// current, and reports whether there was one to run (and nothing else was
// running).
func (e *LuaMacroEngine) RunMenuItem(id int, menu, area string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	var item *luaMenuItem
	if id >= 0 && id < len(e.items) {
		item = e.items[id]
	}
	e.mu.Unlock()
	if item == nil {
		return false
	}
	if !e.running.CompareAndSwap(false, true) {
		return false
	}
	macro := *item.macro
	macro.callArgs = []string{menu, area}
	go func() {
		defer e.running.Store(false)
		e.execute(&macro, "", nil)
	}()
	return true
}

// Find returns the macro bound to a key in an area, falling back to common.
// The most recently registered wins, so a user file loaded later can shadow an
// earlier one.
func (e *LuaMacroEngine) Find(area, key string) *LuaMacro {
	e.mu.Lock()
	defer e.mu.Unlock()

	area = strings.ToLower(area)
	if alias, ok := macroAreaAliases[area]; ok {
		area = alias
	}
	key = strings.ToLower(key)

	if list := e.byArea[area][key]; len(list) > 0 {
		return list[len(list)-1]
	}
	if area != "common" {
		if list := e.byArea["common"][key]; len(list) > 0 {
			return list[len(list)-1]
		}
	}
	return nil
}

// findExact returns only a macro registered in area. Unlike Find it never
// falls back to Common. Palette entries use this at execution time so a stale
// area-specific entry cannot accidentally invoke a different Common macro
// that happens to use the same key.
func (e *LuaMacroEngine) findExact(area, key string) *LuaMacro {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	area = strings.ToLower(area)
	if alias, ok := macroAreaAliases[area]; ok {
		area = alias
	}
	key = strings.ToLower(key)
	if list := e.byArea[area][key]; len(list) > 0 {
		return list[len(list)-1]
	}
	return nil
}

// Bindings returns the effective Lua macro bindings for area. Area-specific
// macros shadow Common bindings just as they do in Trigger.
func (e *LuaMacroEngine) Bindings(area string) []LuaMacroBinding {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	area = strings.ToLower(area)
	if alias, ok := macroAreaAliases[area]; ok {
		area = alias
	}
	seen := make(map[string]bool)
	var result []LuaMacroBinding
	appendArea := func(bindingArea string) {
		for key, list := range e.byArea[bindingArea] {
			if len(list) == 0 || seen[key] {
				continue
			}
			seen[key] = true
			macro := list[len(list)-1]
			result = append(result, LuaMacroBinding{
				Area:        bindingArea,
				Key:         key,
				Description: macro.Description,
				Source:      macro.Source,
			})
		}
	}
	appendArea(area)
	if area != "common" {
		appendArea("common")
	}
	return result
}

// Remove drops a macro from the engine.
func (e *LuaMacroEngine) Remove(area, key string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	area = strings.ToLower(area)
	if alias, ok := macroAreaAliases[area]; ok {
		area = alias
	}
	key = strings.ToLower(key)

	if e.byArea[area] == nil {
		return false
	}
	list := e.byArea[area][key]
	if len(list) == 0 {
		return false
	}

	macroToRemove := list[len(list)-1]
	e.byArea[area][key] = list[:len(list)-1]

	for i, m := range e.all {
		if m == macroToRemove {
			e.all = append(e.all[:i], e.all[i+1:]...)
			break
		}
	}
	return true
}

// Trigger consumes a key if a macro claims it and starts that macro.
//
// The macro runs on its own goroutine, never on the caller's. The caller is
// the input loop running on the UI goroutine, and a macro that asks for panel
// state or shows a message needs that goroutine to be free to answer.
func (e *LuaMacroEngine) Trigger(area string, event *vtinput.InputEvent) bool {
	return e.TriggerWithCommandLine(area, event, "")
}

// TriggerWithCommandLine is Trigger with the command-line snapshot already
// collected by the UI event filter. The real f4 host cannot be queried from
// that goroutine: its MacroHost deliberately posts reads back to the UI
// goroutine, while Lua actions run on a worker. Keeping the snapshot at this
// boundary lets EmptyCommandLine decide before the key is consumed without a
// UI deadlock.
func (e *LuaMacroEngine) TriggerWithCommandLine(area string, event *vtinput.InputEvent, commandLine string) bool {
	if e == nil || event == nil {
		return false
	}
	key := keymap.EventToFarString(event)
	macro := e.Find(area, key)
	if macro == nil {
		return false
	}
	if macro.EmptyCommandLine && commandLine != "" {
		return false
	}
	if !e.running.CompareAndSwap(false, true) {
		// Far does not nest macro execution either.
		return true
	}

	original := *event
	go func() {
		defer e.running.Store(false)
		e.execute(macro, key, &original)
	}()
	return true
}

// Run invokes a macro explicitly, without replaying its trigger when a
// condition declines it. This is the command-palette counterpart of Trigger.
func (e *LuaMacroEngine) Run(area, key string) bool {
	if e == nil {
		return false
	}
	macro := e.Find(area, key)
	if macro == nil {
		return false
	}
	if !e.running.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer e.running.Store(false)
		e.execute(macro, key, nil)
	}()
	return true
}

// RunExact explicitly invokes the binding registered in area without Common
// fallback. It is intended for discoverable command entries that already
// captured a concrete binding identity.
func (e *LuaMacroEngine) RunExact(area, key string) bool {
	if e == nil {
		return false
	}
	macro := e.findExact(area, key)
	if macro == nil {
		return false
	}
	if !e.running.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer e.running.Store(false)
		e.execute(macro, key, nil)
	}()
	return true
}

// execute runs one macro to completion and flushes whatever keys it queued.
func (e *LuaMacroEngine) execute(macro *LuaMacro, key string, original *vtinput.InputEvent) {
	skipped := false

	err := e.rt.Do(func(L *lua.LState) error {
		e.invokedKey = key
		e.pendingKeys = nil
		e.postponed = nil

		if macro.condition != nil {
			L.Push(macro.condition)
			L.Push(lua.LString(key))
			if err := L.PCall(1, 1, nil); err != nil {
				return err
			}
			passed := lua.LVAsBool(L.Get(-1))
			L.Pop(1)
			if !passed {
				skipped = true
				return nil
			}
		}

		L.Push(macro.action)
		argc := len(macro.callArgs)
		if macro.callValues != nil {
			for _, v := range macro.callValues {
				L.Push(v)
			}
			argc = len(macro.callValues)
		} else {
			for _, a := range macro.callArgs {
				L.Push(lua.LString(a))
			}
		}
		if err := L.PCall(argc, 0, nil); err != nil {
			if strings.Contains(err.Error(), macroExitSentinel) {
				return nil
			}
			return err
		}
		return e.runPostponed(L)
	})

	keys := e.pendingKeys
	e.pendingKeys = nil

	if err != nil {
		e.host.Log("MACRO: %s (%s): %v", macro.Description, macro.Source, err)
	}

	if skipped {
		// The key was already consumed on the caller's side, so put it back
		// rather than swallowing a keystroke the macro declined to handle.
		if original != nil {
			e.host.InjectKeys([]*vtinput.InputEvent{original})
		}
		return
	}
	if len(keys) > 0 {
		e.host.InjectKeys(keys)
	}
}

// WaitIdle blocks until no macro is running. Tests use it; macro execution is
// asynchronous by design.
func (e *LuaMacroEngine) WaitIdle(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !e.running.Load() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return !e.running.Load()
}

// Interrupted reports whether a macro hit its call deadline, which leaves the
// interpreter unusable (luaplug.ErrInterrupted).
func (e *LuaMacroEngine) Interrupted() bool {
	return e != nil && e.rt != nil && e.rt.Interrupted()
}

// Close releases the interpreter.
func (e *LuaMacroEngine) Close() error {
	if e == nil || e.rt == nil {
		return nil
	}
	e.stopTimers()
	return e.rt.Close()
}

// parseMacroKeys turns a Far key sequence such as "F5 Enter" into events.
func parseMacroKeys(spec string) []*vtinput.InputEvent {
	var events []*vtinput.InputEvent
	for _, token := range strings.Fields(spec) {
		if event := keymap.ParseFarKey(token); event != nil {
			events = append(events, event)
		}
	}
	return events
}

// splitMacroList splits the space separated lists Far uses for area and key.
func splitMacroList(value string) []string {
	fields := strings.Fields(strings.ToLower(value))
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}
