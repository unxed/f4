package macro

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

var MacroMgr *MacroManager

// MacroManager handles recording, playback and storage of simple keyboard macros.
type MacroManager struct {
	Macros map[string]map[string][]*vtinput.InputEvent
	// Descriptions are the texts given in the assign dialog, by area and
	// key, saved beside the sequence (f4#1870).
	Descriptions map[string]map[string]string
	Recording    bool
	Assigning    bool
	Buffer       []*vtinput.InputEvent
	iniPath      string
	StartArea    string
	// Lua is the Far-compatible macro engine, present only when the user has
	// macros. Recorded macros keep working either way: this is a second
	// backend, not a replacement.
	Lua *LuaMacroEngine

	// OnLuaLoaded, when set, is called after the Lua engine has been built or
	// rebuilt from disk (start, reload, recovery from a runaway macro), with
	// the engine that is now current or nil. The host uses it to publish what
	// the scripts declare beyond keys, such as MenuItem{} entries.
	OnLuaLoaded func(*LuaMacroEngine)

	// luaHost and luaDir are what the engine was last built from, so that an
	// interrupted one can be rebuilt (RefreshInterruptedLua).
	luaHost MacroHost
	luaDir  string
}

func NewMacroManager(iniPath string) *MacroManager {
	mgr := &MacroManager{
		Macros:  make(map[string]map[string][]*vtinput.InputEvent),
		iniPath: iniPath,
	}
	mgr.Load()
	return mgr
}

// ToggleRecording takes the area rather than deriving it: naming the current
// area means naming the view types, which is the application's knowledge.
func (m *MacroManager) ToggleRecording(area string) bool {
	if m == nil || vtui.FrameManager == nil {
		return false
	}
	if m.Recording {
		m.Recording = false
		vtui.FrameManager.PostTask(func() { m.showAssignDialog() })
	} else {
		m.Recording = true
		m.Buffer = make([]*vtinput.InputEvent, 0)
		m.StartArea = area
		vtui.DebugLog("MACRO: Started recording in area: %s", m.StartArea)
	}
	vtui.FrameManager.Redraw()
	return true
}

// LookupHotkey runs the configured action for e in the current area without
// touching macro-recording or macro-playback state. Frames use it as a
// fallback for synthesized key events that bypass FrameManager.EventFilter —
// notably mouse clicks on the F-key bar, which InjectEvents into the queue
// with is_injected=true and therefore never reach Filter above. Returns true
// when the key is bound (including to "None", which is a deliberate silence).
func (m *MacroManager) showAssignDialog() {
	m.Assigning = true
	frame := NewMacroAssignFrame(m)
	vtui.FrameManager.Push(frame)
}

func (m *MacroManager) Load() {
	vtui.DebugLog("MACRO: Loading macros from %s", m.iniPath)
	newMacros := make(map[string]map[string][]*vtinput.InputEvent)
	newDescriptions := make(map[string]map[string]string)
	ini := ini.Load(m.iniPath)
	for sectionName, sec := range ini.Sections() {
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
					if event := decodeMacroEvent(keyStr); event != nil {
						events = append(events, event)
					}
				}

				if newMacros[area] == nil {
					newMacros[area] = make(map[string][]*vtinput.InputEvent)
				}
				newMacros[area][hotkey] = events
				if desc := sec["Description"]; desc != "" {
					if newDescriptions[area] == nil {
						newDescriptions[area] = make(map[string]string)
					}
					newDescriptions[area][hotkey] = desc
				}
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
							charValue, charErr := strconv.ParseInt(fields[0], 10, 32)
							vkValue, vkErr := strconv.ParseUint(fields[1], 10, 16)
							modsValue, modsErr := strconv.ParseUint(fields[2], 10, 32)
							if charErr != nil || vkErr != nil || modsErr != nil {
								continue
							}
							// #nosec G115 -- ParseInt with bitSize 32 bounds the conversion; ValidRune rejects negative and surrogate values.
							char := rune(charValue)
							if char != 0 && !utf8.ValidRune(char) {
								continue
							}
							// #nosec G115 -- ParseUint with bitSize 16 bounds the virtual key code.
							vk := uint16(vkValue)
							// #nosec G115 -- ParseUint with bitSize 32 bounds the control-state bit field.
							mods := vtinput.ControlKeyState(uint32(modsValue))
							events = append(events, &vtinput.InputEvent{
								Type:            vtinput.KeyEventType,
								KeyDown:         true,
								Char:            char,
								VirtualKeyCode:  vk,
								ControlKeyState: mods,
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
	m.Descriptions = newDescriptions
}

// SetDescription records the description of a key's macro; an empty text
// removes it.
func (m *MacroManager) SetDescription(area, key, text string) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if text == "" {
		if m.Descriptions[area] != nil {
			delete(m.Descriptions[area], key)
		}
		return
	}
	if m.Descriptions == nil {
		m.Descriptions = make(map[string]map[string]string)
	}
	if m.Descriptions[area] == nil {
		m.Descriptions[area] = make(map[string]string)
	}
	m.Descriptions[area][key] = text
}

// Description is the description of a key's macro, or "".
func (m *MacroManager) Description(area, key string) string {
	return m.Descriptions[area][key]
}

func (m *MacroManager) Save() {
	vtui.DebugLog("MACRO: Saving macros to %s", m.iniPath)

	var sb strings.Builder
	for area, areaMacros := range m.Macros {
		for hotkey, seq := range areaMacros {
			if len(seq) == 0 {
				continue
			}
			fmt.Fprintf(&sb, "[KeyMacros/%s/%s]\n", area, hotkey)
			sb.WriteString("DisableOutput=0x1\n")
			if desc := m.Description(area, hotkey); desc != "" {
				fmt.Fprintf(&sb, "Description=%s\n", desc)
			}

			var parts []string
			for _, e := range seq {
				if token := encodeMacroEvent(e); token != "" {
					parts = append(parts, token)
				}
			}
			fmt.Fprintf(&sb, "Sequence=%s\n\n", strings.Join(parts, " "))
		}
	}

	if err := os.MkdirAll(filepath.Dir(m.iniPath), 0700); err != nil {
		vtui.DebugLog("MACRO: Failed to create macro directory: %v", err)
		return
	}
	err := os.WriteFile(m.iniPath, []byte(sb.String()), 0600)
	if err != nil {
		vtui.DebugLog("MACRO: Failed to save: %v", err)
		return
	}
	_ = os.Chmod(m.iniPath, 0600)
}

// MacroAssignFrame is the dialog that assigns a recorded macro to a key, laid
// out as far2's (DkmS1953, f4#1870): a description, a field that takes the
// key pressed in it, OK and Cancel. With nothing recorded it says first that
// the key's macro will be removed.
type MacroAssignFrame struct {
	*vtui.Window
	mgr      *MacroManager
	desc     *vtui.Edit
	keyField *macroKeyEdit
	ok       *vtui.Button
}

// macroKeyEdit shows the key pressed in it instead of typing: every key but
// Esc, Tab included, can be a macro's key. After a key it hands the focus to
// OK, so Enter confirms.
type macroKeyEdit struct {
	*vtui.Edit
	key     string
	onKey   func()
	capture func(e *vtinput.InputEvent) string
}

func (k *macroKeyEdit) ProcessKey(e *vtinput.InputEvent) bool {
	// Text cannot name a physical shortcut. Only the capture field consumes
	// it here; the description field still accepts committed text normally.
	if e.TextInput {
		return true
	}
	if e.Type != vtinput.KeyEventType || !e.KeyDown || e.VirtualKeyCode == vtinput.VK_ESCAPE {
		return false
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT,
		vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL,
		vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU,
		vtinput.VK_CAPITAL, vtinput.VK_NUMLOCK, vtinput.VK_SCROLL:
		return true
	}
	k.key = k.capture(e)
	k.SetText(k.key)
	if k.onKey != nil {
		k.onKey()
	}
	return true
}

func NewMacroAssignFrame(m *MacroManager) *MacroAssignFrame {
	const width = 68
	height := 9
	// An empty recording removes the macro of the key chosen here: say so
	// before the key is chosen, not only afterwards (f4#1870).
	var warning []string
	if len(m.Buffer) == 0 {
		warning = vtui.WrapText(i18n.Msg("Macro.AssignEmpty"), width-4)
		height += len(warning) + 1
	}
	base := vtui.NewCenteredDialog(width, height, i18n.Msg("Macro.AssignTitle"))
	f := &MacroAssignFrame{Window: base, mgr: m}
	labelWidth := max(vtui.StringWidth(strings.ReplaceAll(i18n.Msg("Macro.AssignDescription"), "&", "")),
		vtui.StringWidth(strings.ReplaceAll(i18n.Msg("Macro.AssignPrompt"), "&", ""))) + 2
	fieldWidth := width - 5 - labelWidth
	f.desc = vtui.NewEdit(0, 0, fieldWidth, "")
	f.keyField = &macroKeyEdit{Edit: vtui.NewEdit(0, 0, fieldWidth, ""), capture: keymap.EventToFarString}
	f.ok = vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	f.ok.IsDefault = true
	cancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	f.keyField.onKey = func() { f.SetFocusedItem(f.ok) }
	descLabel := vtui.NewLabel(0, 0, i18n.Msg("Macro.AssignDescription"), f.desc)
	keyLabel := vtui.NewLabel(0, 0, i18n.Msg("Macro.AssignPrompt"), f.keyField)
	separator := vtui.NewSeparator(0, 0, width, true, true)

	vbox := vtui.NewVBoxLayout(f.X1+2, f.Y1+2, width-4, height-4)
	for i, line := range warning {
		text := vtui.NewText(0, 0, line, vtui.Palette[vtui.ColDialogHighlightText])
		f.AddItem(text)
		margins := vtui.Margins{}
		if i == len(warning)-1 {
			margins.Bottom = 1
		}
		vbox.Add(text, margins, vtui.AlignLeft)
	}
	f.AddItem(descLabel)
	f.AddItem(f.desc)
	f.AddItem(keyLabel)
	f.AddItem(f.keyField)
	f.AddItem(separator)
	f.AddItem(f.ok)
	f.AddItem(cancel)
	descRow := vtui.NewHBoxLayout(0, 0, width-4, 1)
	descRow.Add(descLabel, vtui.Margins{}, vtui.AlignLeft)
	descRow.Add(f.desc, vtui.Margins{Left: labelWidth - vtui.StringWidth(strings.ReplaceAll(i18n.Msg("Macro.AssignDescription"), "&", ""))}, vtui.AlignFill)
	vbox.Add(descRow, vtui.Margins{}, vtui.AlignFill)
	keyRow := vtui.NewHBoxLayout(0, 0, width-4, 1)
	keyRow.Add(keyLabel, vtui.Margins{}, vtui.AlignLeft)
	keyRow.Add(f.keyField, vtui.Margins{Left: labelWidth - vtui.StringWidth(strings.ReplaceAll(i18n.Msg("Macro.AssignPrompt"), "&", ""))}, vtui.AlignFill)
	vbox.Add(keyRow, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(separator, vtui.Margins{Left: -2, Right: -2}, vtui.AlignFill)
	buttons := vtui.NewHBoxLayout(0, 0, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(f.ok, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(cancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(buttons, vtui.Margins{}, vtui.AlignFill)
	vbox.Apply()

	f.ok.OnClick = func() {
		if f.keyField.key == "" {
			vtui.ShowMessageOn(f, i18n.Msg("Macro.AssignTitle"), i18n.Msg("Macro.AssignNoKey"), []string{"&Ok"})
			f.SetFocusedItem(f.keyField)
			return
		}
		f.assign(f.keyField.key, f.desc.GetText())
	}
	cancel.OnClick = func() { f.cancel() }
	f.SetFocusedItem(f.keyField)
	return f
}

// Key is the key chosen in the dialog so far, "" before one is pressed.
func (f *MacroAssignFrame) Key() string { return f.keyField.key }

// SetDescriptionText fills the description field.
func (f *MacroAssignFrame) SetDescriptionText(text string) { f.desc.SetText(text) }

func (f *MacroAssignFrame) cancel() {
	f.mgr.Buffer = nil
	f.SetExitCode(-1)
	vtui.FrameManager.Redraw()
}

func (f *MacroAssignFrame) ProcessKey(e *vtinput.InputEvent) bool {
	if e.Type == vtinput.KeyEventType && e.KeyDown && !e.TextInput && e.VirtualKeyCode == vtinput.VK_ESCAPE {
		f.cancel()
		return true
	}
	return f.Window.ProcessKey(e)
}

// assign gives key the recorded macro, or takes its macro away when nothing
// was recorded, and reports what happened.
func (f *MacroAssignFrame) assign(key, description string) {
	keyDesc := key
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

	var msg string

	removedIni := false
	if f.mgr.Macros[area] != nil {
		if _, exists := f.mgr.Macros[area][key]; exists {
			delete(f.mgr.Macros[area], key)
			f.mgr.SetDescription(area, key, "")
			removedIni = true
		}
	}
	if !removedIni && f.mgr.Macros["Common"] != nil {
		if _, exists := f.mgr.Macros["Common"][key]; exists {
			delete(f.mgr.Macros["Common"], key)
			f.mgr.SetDescription("Common", key, "")
			removedIni = true
			area = "Common"
		}
	}

	removedLua := false
	if f.mgr.Lua != nil && f.mgr.Lua.Remove(area, key) {
		scriptDir := filepath.Join(config.GetF4ConfigDir(), "Macros", "scripts")
		scriptPath := filepath.Join(scriptDir, RecordedMacroFileName(area, key))
		// Best effort: the macro is already gone from the engine, and a script
		// left on disk is reloaded into nothing.
		_ = os.Remove(scriptPath)
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
		if config.App.MacroRecordFormat == 1 {
			scriptDir := filepath.Join(config.GetF4ConfigDir(), "Macros", "scripts")
			err := f.mgr.SaveRecordedMacro(scriptDir, area, key, strings.TrimSpace(description), f.mgr.Buffer)
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
			f.mgr.SetDescription(area, key, description)
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
}

func (f *MacroAssignFrame) GetType() vtui.FrameType { return vtui.TypeDialog }
func (f *MacroAssignFrame) IsModal() bool           { return true }
func (f *MacroAssignFrame) GetTitle() string        { return "Macro Assign" }

func (f *MacroAssignFrame) SetExitCode(code int) {
	f.mgr.Assigning = false
	f.Window.SetExitCode(code)
}
