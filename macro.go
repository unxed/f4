package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

var MacroMgr *MacroManager

// MacroManager handles recording, playback and storage of simple keyboard macros.
type MacroManager struct {
	Macros    map[string]map[string][]*vtinput.InputEvent
	Recording bool
	Assigning bool
	Buffer    []*vtinput.InputEvent
	iniPath   string
	StartArea string
	// Lua is the Far-compatible macro engine, present only when the user has
	// macros. Recorded macros keep working either way: this is a second
	// backend, not a replacement.
	Lua *LuaMacroEngine
}

func NewMacroManager(iniPath string) *MacroManager {
	mgr := &MacroManager{
		Macros:  make(map[string]map[string][]*vtinput.InputEvent),
		iniPath: iniPath,
	}
	mgr.Load()
	return mgr
}

func normalizeMods(mods vtinput.ControlKeyState) vtinput.ControlKeyState {
	var n vtinput.ControlKeyState
	if mods.Contains(vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed) {
		n |= vtinput.LeftCtrlPressed
	}

	if mods.Contains(vtinput.LeftAltPressed | vtinput.RightAltPressed) {
		n |= vtinput.LeftAltPressed
	}

	if mods.Contains(vtinput.ShiftPressed) {
		n |= vtinput.ShiftPressed
	}

	if mods.Contains(vtinput.EnhancedKey) {
		n |= vtinput.EnhancedKey
	}
	return n
}

var farKeyNames = map[uint16]string{
	vtinput.VK_RETURN:   "Enter",
	vtinput.VK_ESCAPE:   "Esc",
	vtinput.VK_SPACE:    "Space",
	vtinput.VK_TAB:      "Tab",
	vtinput.VK_BACK:     "BS",
	vtinput.VK_INSERT:   "Ins",
	vtinput.VK_DELETE:   "Del",
	vtinput.VK_HOME:     "Home",
	vtinput.VK_END:      "End",
	vtinput.VK_PRIOR:    "PgUp",
	vtinput.VK_NEXT:     "PgDn",
	vtinput.VK_UP:       "Up",
	vtinput.VK_DOWN:     "Down",
	vtinput.VK_LEFT:     "Left",
	vtinput.VK_RIGHT:    "Right",
	vtinput.VK_MULTIPLY: "Multiply",
	vtinput.VK_ADD:      "Add",
	vtinput.VK_SUBTRACT: "Subtract",
	vtinput.VK_DECIMAL:  "Decimal",
	vtinput.VK_DIVIDE:   "Divide",
}

func EventToFarString(e *vtinput.InputEvent) string {
	var sb strings.Builder
	mods := normalizeMods(e.ControlKeyState)
	if mods.Contains(vtinput.LeftCtrlPressed) {
		sb.WriteString("Ctrl")
	}
	if mods.Contains(vtinput.LeftAltPressed) {
		sb.WriteString("Alt")
	}
	if mods.Contains(vtinput.ShiftPressed) {
		sb.WriteString("Shift")
	}

	vk := e.VirtualKeyCode
	// Windows marks the numeric-keypad Enter as enhanced, while the main
	// keyboard Enter is not enhanced. Delete is the opposite: the navigation
	// cluster key is enhanced and the keypad decimal/delete key is not.
	if mods.Contains(vtinput.EnhancedKey) && vk == vtinput.VK_RETURN {
		sb.WriteString("NumEnter")
		return sb.String()
	}
	if !mods.Contains(vtinput.EnhancedKey) {
		if vk == vtinput.VK_DELETE {
			sb.WriteString("NumDel")
			return sb.String()
		}
	}

	if name, ok := farKeyNames[vk]; ok {
		sb.WriteString(name)
	} else if vk >= vtinput.VK_F1 && vk <= vtinput.VK_F24 {
		sb.WriteString(fmt.Sprintf("F%d", vk-vtinput.VK_F1+1))
	} else if vk >= 'A' && vk <= 'Z' {
		// Hotkey key strings are always uppercase for A-Z ("CtrlV",
		// "ShiftA"). Wayland/X11 gui backends deliver Ctrl+letter events
		// with Char set to the lowercase typed letter — writing e.Char
		// here would produce "Ctrlv" and miss every default binding
		// (Ctrl+V paste, Ctrl+A select-all, Ctrl+O toggle-panels, …).
		sb.WriteRune(rune(vk))
	} else if vk >= '0' && vk <= '9' {
		sb.WriteRune(rune(vk))
	} else if e.Char > 32 && e.Char < 127 {
		sb.WriteRune(e.Char)
	} else {
		sb.WriteString(fmt.Sprintf("VK_%X", vk))
	}
	return sb.String()
}

func ParseFarKey(s string) *vtinput.InputEvent {
	e := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true}
	orig := s
	if strings.HasPrefix(s, "RCtrl") {
		e.ControlKeyState |= vtinput.RightCtrlPressed
		s = strings.TrimPrefix(s, "RCtrl")
	} else if strings.HasPrefix(s, "Ctrl") {
		e.ControlKeyState |= vtinput.LeftCtrlPressed
		s = strings.TrimPrefix(s, "Ctrl")
	}
	if strings.HasPrefix(s, "Alt") {
		e.ControlKeyState |= vtinput.LeftAltPressed
		s = strings.TrimPrefix(s, "Alt")
	}
	if strings.HasPrefix(s, "Shift") {
		e.ControlKeyState |= vtinput.ShiftPressed
		s = strings.TrimPrefix(s, "Shift")
	}
	if len(s) == 0 {
		s = orig
	}

	if strings.EqualFold(s, "NumEnter") {
		e.VirtualKeyCode = vtinput.VK_RETURN
		e.Char = '\r'
		e.ControlKeyState |= vtinput.EnhancedKey
		return e
	}
	if strings.EqualFold(s, "NumDel") {
		e.VirtualKeyCode = vtinput.VK_DELETE
		return e
	}

	for vk, name := range farKeyNames {
		if strings.EqualFold(s, name) {
			e.VirtualKeyCode = vk
			if vk == vtinput.VK_RETURN {
				e.Char = '\r'
			}
			if vk == vtinput.VK_SPACE {
				e.Char = ' '
			}
			if vk == vtinput.VK_TAB {
				e.Char = '\t'
			}
			if vk == vtinput.VK_BACK {
				e.Char = '\b'
			}
			if vk == vtinput.VK_INSERT || vk == vtinput.VK_DELETE || vk == vtinput.VK_HOME || vk == vtinput.VK_END ||
				vk == vtinput.VK_PRIOR || vk == vtinput.VK_NEXT || vk == vtinput.VK_UP || vk == vtinput.VK_DOWN ||
				vk == vtinput.VK_LEFT || vk == vtinput.VK_RIGHT {
				e.ControlKeyState |= vtinput.EnhancedKey
			}
			return e
		}
	}

	if len(s) >= 2 && (s[0] == 'F' || s[0] == 'f') {
		if n, err := strconv.Atoi(s[1:]); err == nil && n >= 1 && n <= 24 {
			e.VirtualKeyCode = vtinput.VK_F1 + uint16(n-1)
			return e
		}
	}

	if strings.HasPrefix(s, "VK_") {
		fmt.Sscanf(s, "VK_%X", &e.VirtualKeyCode)
		return e
	}

	if len(s) > 0 {
		char := rune(s[0])
		e.Char = char
		if char >= 'a' && char <= 'z' {
			e.VirtualKeyCode = uint16(char - 'a' + 'A')
		} else if char >= 'A' && char <= 'Z' {
			e.VirtualKeyCode = uint16(char)
		} else if char >= '0' && char <= '9' {
			e.VirtualKeyCode = uint16(char)
		} else {
			switch char {
			case '.':
				e.VirtualKeyCode = vtinput.VK_OEM_PERIOD
			case ',':
				e.VirtualKeyCode = vtinput.VK_OEM_COMMA
			case '-':
				e.VirtualKeyCode = vtinput.VK_OEM_MINUS
			case '=':
				e.VirtualKeyCode = vtinput.VK_OEM_PLUS
			case '/':
				e.VirtualKeyCode = vtinput.VK_OEM_2
			case '`', '~':
				e.VirtualKeyCode = vtinput.VK_OEM_3
			case '[', '{':
				e.VirtualKeyCode = vtinput.VK_OEM_4
			case '\\', '|':
				e.VirtualKeyCode = vtinput.VK_OEM_5
			case ']', '}':
				e.VirtualKeyCode = vtinput.VK_OEM_6
			case '\'', '"':
				e.VirtualKeyCode = vtinput.VK_OEM_7
			case ';', ':':
				e.VirtualKeyCode = vtinput.VK_OEM_1
			}
		}
	}
	return e
}

// Filter is hooked into FrameManager. Returns true if the event was consumed.
func (m *MacroManager) GetCurrentArea() string {
	if vtui.FrameManager == nil {
		return "Common"
	}
	top := vtui.FrameManager.GetTopFrame()
	if top == nil {
		return "Common"
	}
	switch top.GetType() {
	case vtui.TypeDialog:
		return "Dialog"
	case vtui.TypeMenu:
		if menu, ok := top.(*vtui.VMenu); ok {
			if strings.Contains(menu.GetTitle(), "Drive") {
				return "Disks"
			}
		}
		return "Menu"
	case vtui.TypeUser + 1:
		if pf, ok := top.(*PanelsFrame); ok {
			if !pf.showPanels {
				return "Terminal"
			}
		}
		return "Shell"
	case vtui.TypeUser + 2:
		return "Editor"
	case vtui.TypeUser + 3:
		return "Viewer"
	}
	return "Other"
}

// isPanelBookmarkHotkey identifies far2l-compatible folder bookmark keys.
// They must reach PanelsFrame before macro and configurable hotkey handling,
// because EventToFarString intentionally normalizes left and right Ctrl.
func isPanelBookmarkHotkey(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}

	rctrl := (e.ControlKeyState & vtinput.RightCtrlPressed) != 0
	lctrl := (e.ControlKeyState & vtinput.LeftCtrlPressed) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	isGoto := (rctrl && !shift && !alt) || ((lctrl || rctrl) && alt && !shift)
	isSave := (rctrl && shift && !alt) || ((lctrl || rctrl) && alt && shift)

	if e.VirtualKeyCode >= vtinput.VK_0 && e.VirtualKeyCode <= vtinput.VK_9 {
		return isGoto || isSave
	}
	return e.VirtualKeyCode == vtinput.VK_OEM_3 && isGoto
}

// isPanelFastFindToggleKey identifies contextual panel-toggle keys owned by
// Fast Find. They must reach PanelsFrame before macros and configurable
// hotkeys, otherwise Esc/Del -> Panel.Toggle hides the panels first.
func isPanelFastFindToggleKey(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown ||
		(e.VirtualKeyCode != vtinput.VK_ESCAPE && e.VirtualKeyCode != vtinput.VK_DELETE) {
		return false
	}
	mods := e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed |
		vtinput.LeftAltPressed | vtinput.RightAltPressed | vtinput.ShiftPressed)
	if mods != 0 || vtui.FrameManager == nil {
		return false
	}
	pf, ok := vtui.FrameManager.GetTopFrame().(*PanelsFrame)
	if !ok || !pf.showPanels {
		return false
	}
	fsp := pf.getActivePanel()
	return fsp != nil && fsp.fastFindMode
}

func (m *MacroManager) Filter(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType {
		return false
	}

	// Ctrl+. toggles recording. We check both VK and Char for better terminal compatibility.
	isCtrlDot := (e.VirtualKeyCode == vtinput.VK_OEM_PERIOD || e.Char == '.') &&
		(e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed)) != 0

	if isCtrlDot {
		if !e.KeyDown {
			return true // Consume KeyUp of the trigger
		}
		if m.Recording {
			m.Recording = false
			vtui.FrameManager.PostTask(func() {
				m.showAssignDialog()
			})
		} else {
			m.Recording = true
			m.Buffer = make([]*vtinput.InputEvent, 0)
			m.StartArea = m.GetCurrentArea()
			vtui.DebugLog("MACRO: Started recording in area: %s", m.StartArea)
		}
		vtui.FrameManager.Redraw()
		return true // Trigger is ALWAYS consumed
	}

	if m.Recording || m.Assigning {
		if e.KeyDown && m.Recording {
			switch e.VirtualKeyCode {
			case vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT,
				vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL,
				vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU,
				vtinput.VK_CAPITAL, vtinput.VK_NUMLOCK, vtinput.VK_SCROLL:
				// Ignore standalone modifier keys
			default:
				m.Buffer = append(m.Buffer, e)
			}
		}
		return false // Let it pass to the UI so user sees what they type / dialog catches it
	}

	if !e.KeyDown {
		return false
	}

	currentArea := m.GetCurrentArea()
	if currentArea == "Shell" && (isPanelBookmarkHotkey(e) || isPanelFastFindToggleKey(e)) {
		return false
	}

	// Check if this key triggers a macro
	keyStr := EventToFarString(e)

	if areaMacros, ok := m.Macros[currentArea]; ok {
		if seq, ok := areaMacros[keyStr]; ok {
			vtui.DebugLog("MACRO: Playing back macro for %s in area %s", keyStr, currentArea)
			vtui.FrameManager.InjectEvents(seq)
			return true
		}
	}

	if commonMacros, ok := m.Macros["Common"]; ok {
		if seq, ok := commonMacros[keyStr]; ok {
			vtui.DebugLog("MACRO: Playing back macro for %s in area Common", keyStr)
			vtui.FrameManager.InjectEvents(seq)
			return true
		}
	}

	// Recorded macros win over scripted ones, as they do in Far.
	if m.Lua != nil && m.Lua.Trigger(currentArea, e) {
		vtui.DebugLog("MACRO: Running Lua macro for %s in area %s", keyStr, currentArea)
		return true
	}

	// Plugin key interception: global plugin hotkeys and the active
	// panel's PanelController may override built-in hotkeys, so they
	// are consulted before the hotkey manager.
	if vtui.FrameManager != nil {
		if top := vtui.FrameManager.GetTopFrame(); top != nil {
			if pi, ok := top.(interface {
				InterceptPluginKey(*vtinput.InputEvent) bool
			}); ok && pi.InterceptPluginKey(e) {
				return true
			}
		}
	}

	// A frame in a modal input state (e.g. editor autocomplete, panel
	// fast-find) may veto system hotkey dispatch: the key then falls
	// through to the frame's own ProcessKey, which handles such states
	// before anything else.
	if vtui.FrameManager != nil {
		if top := vtui.FrameManager.GetTopFrame(); top != nil {
			if v, ok := top.(interface {
				VetoActionKey(*vtinput.InputEvent) bool
			}); ok && v.VetoActionKey(e) {
				return false
			}
		}
	}

	// Hotkey Manager evaluation (System actions)
	if hm := GlobalHotkeysMgr; hm != nil {
		if actionName := hm.GetAction(currentArea, keyStr); actionName != "" {
			if strings.EqualFold(actionName, "none") {
				return true // Intercept and silence (explicitly unbound)
			}
			vtui.DebugLog("HOTKEY: Executing action %s for %s in area %s", actionName, keyStr, currentArea)
			if RunAction(actionName) {
				return true
			}
		}
	}

	return false
}

// LookupHotkey runs the configured action for e in the current area without
// touching macro-recording or macro-playback state. Frames use it as a
// fallback for synthesized key events that bypass FrameManager.EventFilter —
// notably mouse clicks on the F-key bar, which InjectEvents into the queue
// with is_injected=true and therefore never reach Filter above. Returns true
// when the key is bound (including to "None", which is a deliberate silence).
func (m *MacroManager) LookupHotkey(e *vtinput.InputEvent) bool {
	if m == nil || e == nil || e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}
	hm := GlobalHotkeysMgr
	if hm == nil {
		return false
	}
	keyStr := EventToFarString(e)
	if keyStr == "" {
		return false
	}
	area := m.GetCurrentArea()
	actionName := hm.GetAction(area, keyStr)
	if actionName == "" {
		return false
	}
	if strings.EqualFold(actionName, "none") {
		return true
	}
	vtui.DebugLog("HOTKEY: Injected %s → action %s in area %s", keyStr, actionName, area)
	return RunAction(actionName)
}

func (m *MacroManager) showAssignDialog() {
	m.Assigning = true
	frame := NewMacroAssignFrame(m)
	vtui.FrameManager.Push(frame)
}

func (m *MacroManager) Load() {
	vtui.DebugLog("MACRO: Loading macros from %s", m.iniPath)
	newMacros := make(map[string]map[string][]*vtinput.InputEvent)
	ini := LoadIni(m.iniPath)
	for sectionName, sec := range ini.data {
		if strings.HasPrefix(sectionName, "KeyMacros/") {
			parts := strings.SplitN(sectionName, "/", 3)
			if len(parts) == 3 {
				area := parts[1]
				hotkey := parts[2]
				seqStr := sec["Sequence"]
				if seqStr == "" {
					continue
				}

				var events []*vtinput.InputEvent
				for _, keyStr := range strings.Fields(seqStr) {
					if strings.HasPrefix(keyStr, "callplugin") || strings.HasPrefix(keyStr, "eval") || strings.Contains(keyStr, "(") {
						continue // Skip far2l macro functions for now
					}
					events = append(events, ParseFarKey(keyStr))
				}

				if newMacros[area] == nil {
					newMacros[area] = make(map[string][]*vtinput.InputEvent)
				}
				newMacros[area][hotkey] = events
			}
		} else {
			targetArea := sectionName
			if sectionName == "Macros" {
				targetArea = "Common" // Migration from legacy format
			}
			if newMacros[targetArea] == nil {
				newMacros[targetArea] = make(map[string][]*vtinput.InputEvent)
			}
			for key, val := range sec {
				if strings.Contains(val, ":") && !strings.Contains(val, " ") {
					var events []*vtinput.InputEvent
					for _, p := range strings.Split(val, ",") {
						fields := strings.Split(p, ":")
						if len(fields) == 3 {
							char, _ := strconv.Atoi(fields[0])
							vk, _ := strconv.Atoi(fields[1])
							mods, _ := strconv.Atoi(fields[2])
							events = append(events, &vtinput.InputEvent{
								Type:            vtinput.KeyEventType,
								KeyDown:         true,
								Char:            rune(char),
								VirtualKeyCode:  uint16(vk),
								ControlKeyState: vtinput.ControlKeyState(mods),
							})
						}
					}
					cleanKey := strings.ReplaceAll(key, "+", "")
					newMacros[targetArea][cleanKey] = events
				}
			}
		}
	}
	m.Macros = newMacros
}

func (m *MacroManager) Save() {
	vtui.DebugLog("MACRO: Saving macros to %s", m.iniPath)

	var sb strings.Builder
	for area, areaMacros := range m.Macros {
		for hotkey, seq := range areaMacros {
			if len(seq) == 0 {
				continue
			}
			sb.WriteString(fmt.Sprintf("[KeyMacros/%s/%s]\n", area, hotkey))
			sb.WriteString("DisableOutput=0x1\n")

			var parts []string
			for _, e := range seq {
				parts = append(parts, EventToFarString(e))
			}
			sb.WriteString(fmt.Sprintf("Sequence=%s\n\n", strings.Join(parts, " ")))
		}
	}

	os.MkdirAll(filepath.Dir(m.iniPath), 0755)
	err := os.WriteFile(m.iniPath, []byte(sb.String()), 0644)
	if err != nil {
		vtui.DebugLog("MACRO: Failed to save: %v", err)
	}
}

// MacroAssignFrame is a modal frame that captures a key combination to assign a macro.
type MacroAssignFrame struct {
	vtui.Window
	mgr *MacroManager
}

func NewMacroAssignFrame(m *MacroManager) *MacroAssignFrame {
	width, height := 42, 7
	base := vtui.NewCenteredDialog(width, height, Msg("Macro.AssignTitle"))
	f := &MacroAssignFrame{
		Window: *base,
		mgr:    m,
	}

	prompt := vtui.NewText(0, 0, Msg("Macro.AssignPrompt"), vtui.Palette[vtui.ColDialogText])
	f.AddItem(prompt)

	cancelPrompt := vtui.NewText(0, 0, Msg("Macro.AssignCancel"), vtui.Palette[vtui.ColDialogText])
	f.AddItem(cancelPrompt)

	vbox := vtui.NewVBoxLayout(f.X1+2, f.Y1+2, width-4, height-4)
	vbox.Add(prompt, vtui.Margins{}, vtui.AlignCenter)
	vbox.Add(cancelPrompt, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Apply()

	return f
}

func (f *MacroAssignFrame) ProcessKey(e *vtinput.InputEvent) bool {
	if e.Type == vtinput.FocusEventType {
		return f.Window.ProcessKey(e)
	}

	if !e.KeyDown {
		return false
	}

	if e.VirtualKeyCode == vtinput.VK_ESCAPE {
		f.mgr.Buffer = nil
		f.SetExitCode(-1)
		vtui.FrameManager.Redraw()
		return true
	}

	// Only ignore "pure" modifiers without any other key.
	// Everything else (including Esc and Alt-combos) can be a macro.
	switch e.VirtualKeyCode {
	case vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT,
		vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL,
		vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU,
		vtinput.VK_CAPITAL, vtinput.VK_NUMLOCK, vtinput.VK_SCROLL:
		return false
	}

	key := EventToFarString(e)
	if f.mgr.Macros == nil {
		f.mgr.Macros = make(map[string]map[string][]*vtinput.InputEvent)
	}

	area := f.mgr.StartArea
	if area == "" {
		area = "Common"
	}
	if f.mgr.Macros[area] == nil {
		f.mgr.Macros[area] = make(map[string][]*vtinput.InputEvent)
	}

	keyDesc := key
	var msg string

	removedIni := false
	if f.mgr.Macros[area] != nil {
		if _, exists := f.mgr.Macros[area][key]; exists {
			delete(f.mgr.Macros[area], key)
			removedIni = true
		}
	}
	if !removedIni && f.mgr.Macros["Common"] != nil {
		if _, exists := f.mgr.Macros["Common"][key]; exists {
			delete(f.mgr.Macros["Common"], key)
			removedIni = true
			area = "Common"
		}
	}

	removedLua := false
	if f.mgr.Lua != nil && f.mgr.Lua.Remove(area, key) {
		scriptDir := filepath.Join(GetF4ConfigDir(), "Macros", "scripts")
		scriptPath := filepath.Join(scriptDir, RecordedMacroFileName(area, key))
		os.Remove(scriptPath)
		removedLua = true
	}

	if len(f.mgr.Buffer) == 0 {
		if removedIni || removedLua {
			msg = fmt.Sprintf("Macro removed from key:\n%s\nArea: %s", keyDesc, area)
		} else {
			msg = fmt.Sprintf("No macro found for key:\n%s", keyDesc)
		}
		if removedIni {
			f.mgr.Save()
		}
	} else {
		if removedIni {
			f.mgr.Save()
		}
		if AppConfig.MacroRecordFormat == 1 {
			scriptDir := filepath.Join(GetF4ConfigDir(), "Macros", "scripts")
			err := f.mgr.SaveRecordedMacro(scriptDir, area, key, "", f.mgr.Buffer)
			if err != nil {
				msg = fmt.Sprintf("Failed to save Lua macro:\n%v", err)
			} else {
				msg = fmt.Sprintf("Lua macro assigned to key:\n%s\nArea: %s", keyDesc, area)
			}
		} else {
			if f.mgr.Macros[area] == nil {
				f.mgr.Macros[area] = make(map[string][]*vtinput.InputEvent)
			}
			f.mgr.Macros[area][key] = f.mgr.Buffer
			f.mgr.Save()
			msg = fmt.Sprintf("Macro assigned to key:\n%s\nArea: %s", keyDesc, area)
		}
	}

	f.mgr.Buffer = nil
	f.SetExitCode(0)

	vtui.FrameManager.PostTask(func() {
		vtui.ShowMessage(" Macro ", msg, []string{"&Ok"})
	})

	vtui.FrameManager.Redraw()
	return true
}

func (f *MacroAssignFrame) ProcessMouse(e *vtinput.InputEvent) bool {
	return true // Block clicks from falling through
}
func (f *MacroAssignFrame) GetType() vtui.FrameType { return vtui.TypeDialog }
func (f *MacroAssignFrame) IsModal() bool           { return true }
func (f *MacroAssignFrame) GetTitle() string        { return "Macro Assign" }

func (f *MacroAssignFrame) SetExitCode(code int) {
	f.mgr.Assigning = false
	f.Window.SetExitCode(code)
}
