package main

import (
	"github.com/mattn/go-runewidth"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// CommandLine is a simplified Edit control used for shell input.
type CommandLine struct {
	vtui.ScreenObject
	Edit       *vtui.Edit
	Prompt     string
	RichPrompt []vtui.CharInfo
}

func NewCommandLine(prompt string) *CommandLine {
	cl := &CommandLine{
		Prompt: prompt,
		Edit:   vtui.NewEdit(0, 0, 10, ""),
	}
	cl.Edit.DeduplicateHistory = true
	cl.Edit.HistoryLimit = 100
	cl.Edit.PathHintsEnabled = true
	cl.Edit.ColorTextIdx = ColCommandLineText
	cl.Edit.ColorUnchangedIdx = ColCommandLineText
	cl.Edit.ColorSelectedIdx = ColCommandLineSelectedText
	cl.Edit.SetCanFocus(true)
	cl.SetFocus(true)   // Ensure cursor is active from the start
	cl.SetVisible(true) // Set visible by default so it can process keys in tests before the first render!
	return cl
}

func (cl *CommandLine) SetPosition(x1, y1, x2, y2 int) {
	cl.ScreenObject.SetPosition(x1, y1, x2, y2)
	promptLen := 0
	if len(cl.RichPrompt) > 0 {
		for _, c := range cl.RichPrompt {
			if c.Char != vtui.WideCharFiller {
				promptLen++
			}
		}
	} else {
		promptLen = runewidth.StringWidth(cl.Prompt)
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
		scr.Write(cl.X1, cl.Y1, vtui.StringToCharInfo(cl.Prompt, vtui.Palette[ColCommandLinePrompt]))
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
	if AppConfig.CommandLineAutoComplete && handled && cl.Edit.HistoryPos == -1 && !cl.IsEmpty() {
		isChar := e.Char != 0
		isDel := e.VirtualKeyCode == vtinput.VK_BACK || e.VirtualKeyCode == vtinput.VK_DELETE
		if isChar || isDel {
			top := vtui.FrameManager.GetTopFrame()
			_, isAc := top.(*vtui.AutoCompleteMenu)
			if !isAc {
				ac := vtui.NewAutoCompleteMenu(cl.Edit)
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
