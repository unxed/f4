package cmdline

import (
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// CommandLine is a simplified Edit control used for shell input.
type CommandLine struct {
	vtui.ScreenObject
	Edit                   *vtui.Edit
	Prompt                 string
	RichPrompt             []vtui.CharInfo
	AutoCompleteSuppressed bool

	// Ctrl+End completion from the history (K-79): the text the walk started
	// from, the text this walk put into the line, and where to go on from.
	histBase string
	histText string
	histNext int
	histLive bool

	keysTaken uint64 // key events given to ProcessKey; see KeysTaken
}

func NewCommandLine(prompt string) *CommandLine {
	cl := &CommandLine{
		Prompt: prompt,
		Edit:   vtui.NewEdit(0, 0, 10, ""),
	}
	cl.Edit.DeduplicateHistory = true
	cl.Edit.HistoryLimit = 100
	cl.Edit.PathHintsEnabled = config.App.CommandLineAutoComplete
	// CommandLine.ProcessKey drives the completion menu itself, under
	// gating that vtui knows nothing about: CommandLineAutoComplete,
	// AutoCompleteSuppressed, and whether history browsing is in progress.
	// Leaving vtui's own trigger on would open the menu from inside
	// Edit.ProcessKey, one call before any of that is consulted.
	cl.Edit.NoAutoComplete = true
	// Far command-line keys of the edit line: Ctrl+K deletes to the end,
	// Ctrl+Backspace and Ctrl+Delete delete a word (docs/FAR2L_COMPAT.md,
	// K-72; unxed/f4#2139). Dialog edit lines keep their behaviour.
	cl.Edit.FarKeys = true
	cl.Edit.ColorTextIdx = theme.ColCommandLineText
	cl.Edit.ColorUnchangedIdx = theme.ColCommandLineText
	cl.Edit.ColorSelectedIdx = theme.ColCommandLineSelectedText
	cl.Edit.SetCanFocus(true)
	cl.SetFocus(true)   // Ensure cursor is active from the start
	cl.SetVisible(true) // Set visible by default so it can process keys in tests before the first render!
	return cl
}

func (cl *CommandLine) SetPosition(x1, y1, x2, y2 int) {
	cl.ScreenObject.SetPosition(x1, y1, x2, y2)
	promptLen := 0
	if len(cl.RichPrompt) > 0 {
		// RichPrompt already contains one entry per rendered screen cell,
		// including the continuation cells of wide characters.
		promptLen = len(cl.RichPrompt)
	} else {
		promptLen = len(vtui.StringToCharInfo(cl.Prompt, 0))
	}
	cl.Edit.SetPosition(x1+promptLen, y1, x2, y2)
}

func (cl *CommandLine) SetFocus(f bool) {
	cl.ScreenObject.SetFocus(f)
	cl.Edit.SetFocus(f)
}
func (cl *CommandLine) SetPrompt(prompt string) {
	cl.RichPrompt = nil
	if cl.Prompt == prompt {
		return
	}
	cl.Prompt = prompt
	// Trigger reposition of Edit control
	cl.SetPosition(cl.X1, cl.Y1, cl.X2, cl.Y2)
}

func (cl *CommandLine) SetRichPrompt(prompt []vtui.CharInfo) {
	cl.RichPrompt = prompt
	cl.Prompt = ""
	cl.SetPosition(cl.X1, cl.Y1, cl.X2, cl.Y2)
}

func (cl *CommandLine) Show(scr *vtui.ScreenBuf) {
	cl.ScreenObject.Show(scr)
	cl.DisplayObject(scr)
}

func (cl *CommandLine) DisplayObject(scr *vtui.ScreenBuf) {
	if !cl.IsVisible() {
		return
	}

	// 1. Draw Prompt
	if len(cl.RichPrompt) > 0 {
		scr.Write(cl.X1, cl.Y1, cl.RichPrompt)
	} else if cl.Prompt != "" {
		scr.Write(cl.X1, cl.Y1, vtui.StringToCharInfo(cl.Prompt, vtui.Palette[theme.ColCommandLinePrompt]))
	}

	// 2. Draw Edit (input field)
	cl.Edit.Show(scr)
}

func (cl *CommandLine) ProcessKey(e *vtinput.InputEvent) bool {
	cl.keysTaken++
	if isCtrlY(e) {
		// Far: Ctrl+Y clears the command line (docs/FAR2L_COMPAT.md, K-72).
		clearLine(cl.Edit)
		return true
	}
	if isCtrlEnd(e) && cl.completeFromHistory() {
		return true
	}
	handled := cl.Edit.ProcessKey(e)
	if handled && cl.Edit.HistoryPos != -1 {
		// If a key was handled by the edit control, it means the text was modified.
		// We should exit history browsing mode.
		// We exclude simple cursor movements from this logic.
		isNav := false
		switch e.VirtualKeyCode {
		case vtinput.VK_LEFT, vtinput.VK_RIGHT, vtinput.VK_HOME, vtinput.VK_END, vtinput.VK_E, vtinput.VK_X:
			isNav = true
		}
		if !isNav {
			cl.Edit.HistoryPos = -1
		}
	}

	// AutoComplete logic:
	if config.App.CommandLineAutoComplete && !cl.AutoCompleteSuppressed && handled && cl.Edit.HistoryPos == -1 && !cl.IsEmpty() {
		isChar := e.Char != 0
		isDel := e.VirtualKeyCode == vtinput.VK_BACK || e.VirtualKeyCode == vtinput.VK_DELETE
		if isChar || isDel {
			top := vtui.FrameManager.GetTopFrame()
			_, isAc := AsCompletionMenu(top)
			if !isAc {
				ac := NewCompletionMenu(cl.Edit)
				if ac.HasMatches() {
					vtui.FrameManager.Push(ac)
				}
			}
		}
	}

	return handled
}

func (cl *CommandLine) ProcessMouse(e *vtinput.InputEvent) bool {
	return cl.Edit.ProcessMouse(e)
}

// Clear empties the command line text.
func (cl *CommandLine) Clear() {
	cl.Edit.SetText("")
}

// HasSelection reports whether part of the command line text is selected.
// vtui.Edit keeps its selection unexported; its semantic node is the one
// place that reports it.
func (cl *CommandLine) HasSelection() bool {
	start, ok := cl.Edit.SemanticNode(nil)["selectionStart"].(int)
	return ok && start != -1
}

// ClearSelection drops the selection, if there is one, and leaves the text
// and the cursor as they are.
func (cl *CommandLine) ClearSelection() {
	if cl.HasSelection() {
		cl.Edit.ClearSelection()
	}
}

// KeysTaken counts the key events ProcessKey has been given. A caller that
// routes a key compares it before and after to learn whether the key reached
// the command line, however many routes lead there.
func (cl *CommandLine) KeysTaken() uint64 {
	return cl.keysTaken
}

// IsEmpty returns true if there is no text in the command line.
func (cl *CommandLine) IsEmpty() bool {
	return cl.Edit.GetText() == ""
}

// InsertString adds text to the command line.
func (cl *CommandLine) InsertString(text string) {
	cl.Edit.InsertString(text)
}

// PasteText inserts a captured clipboard value and leaves history browsing,
// matching the editing semantics of the native paste shortcuts.
func (cl *CommandLine) PasteText(text string) {
	cl.Edit.InsertString(singleLine(text))
	cl.Edit.HistoryPos = -1
}

// isCtrlEnd: Ctrl+End alone, no Alt and no Shift.
func isCtrlEnd(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown || e.VirtualKeyCode != vtinput.VK_END {
		return false
	}
	mods := e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed | vtinput.LeftAltPressed | vtinput.RightAltPressed | vtinput.ShiftPressed)
	return mods == vtinput.LeftCtrlPressed || mods == vtinput.RightCtrlPressed
}

// completeFromHistory is Ctrl+End at the end of a non-empty line (K-79,
// docs/FAR2L_COMPAT.md; unxed/f4#2139): the line becomes the newest history
// entry that starts with it, and the same key again goes to the next older
// one. After the last candidate the walk starts over from the typed text.
// Anywhere else Ctrl+End is the plain move to the end of the line (K-73),
// so false means "not handled here".
func (cl *CommandLine) completeFromHistory() bool {
	text := cl.Edit.GetText()
	n := len([]rune(text))
	if n == 0 || cl.Edit.CursorPos() != n {
		return false
	}
	base, start := text, 0
	if cl.histLive && text == cl.histText {
		base, start = cl.histBase, cl.histNext
	}
	for i := start; i < len(cl.Edit.History); i++ {
		h := cl.Edit.History[i]
		if h == base || h == text || !strings.HasPrefix(h, base) {
			continue
		}
		cl.Edit.SetText(h)
		cl.Edit.SetCursorPos(len([]rune(h)))
		cl.Edit.HistoryPos = -1
		cl.histBase, cl.histText, cl.histNext, cl.histLive = base, h, i+1, true
		return true
	}
	if cl.histLive && text != base {
		// The walk is over: back to what was typed.
		cl.Edit.SetText(base)
		cl.Edit.SetCursorPos(len([]rune(base)))
		cl.histLive = false
		return true
	}
	return false
}

// clearLine is what Ctrl+Y does: the text goes, and history browsing with it.
func clearLine(edit *vtui.Edit) {
	edit.SetText("")
	edit.HistoryPos = -1
}

// isCtrlY: Ctrl+Y alone, no Alt and no Shift.
func isCtrlY(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown || e.VirtualKeyCode != vtinput.VK_Y {
		return false
	}
	mods := e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed | vtinput.LeftAltPressed | vtinput.RightAltPressed | vtinput.ShiftPressed)
	return mods == vtinput.LeftCtrlPressed || mods == vtinput.RightCtrlPressed
}

// singleLine turns every line break of a pasted text (CR, LF, CRLF) into one
// space: the command line is one line, and a raw line feed in it would run the
// parts of the text as separate commands (docs/FAR2L_COMPAT.md, B-03, B-08).
func singleLine(text string) string {
	if !strings.ContainsAny(text, "\r\n") {
		return text
	}
	text = strings.ReplaceAll(text, "\r\n", " ")
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(text)
}
