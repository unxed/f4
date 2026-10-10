package cmdline

import (
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
	cl.Edit.InsertString(text)
	cl.Edit.HistoryPos = -1
}
