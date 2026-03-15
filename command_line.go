package main

import (
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// CommandLine is a simplified Edit control used for shell input.
type CommandLine struct {
	vtui.ScreenObject
	Edit   *vtui.Edit
	Prompt string
}

func NewCommandLine(prompt string) *CommandLine {
	cl := &CommandLine{
		Prompt: prompt,
		Edit:   vtui.NewEdit(0, 0, 10, ""),
	}
	cl.Edit.SetCanFocus(true)
	return cl
}

func (cl *CommandLine) SetPosition(x1, y1, x2, y2 int) {
	cl.ScreenObject.SetPosition(x1, y1, x2, y2)
	// Edit starts after the prompt
	promptLen := len(cl.Prompt)
	cl.Edit.SetPosition(x1+promptLen, y1, x2, y2)
}

func (cl *CommandLine) SetFocus(f bool) {
	cl.ScreenObject.SetFocus(f)
	cl.Edit.SetFocus(f)
}
func (cl *CommandLine) SetPrompt(prompt string) {
	if cl.Prompt == prompt { return }
	cl.Prompt = prompt
	// Trigger reposition of Edit control
	cl.SetPosition(cl.X1, cl.Y1, cl.X2, cl.Y2)
}

func (cl *CommandLine) Show(scr *vtui.ScreenBuf) {
	cl.ScreenObject.Show(scr)
	cl.DisplayObject(scr)
}

func (cl *CommandLine) DisplayObject(scr *vtui.ScreenBuf) {
	if !cl.IsVisible() { return }

	// 1. Draw Prompt
	scr.Write(cl.X1, cl.Y1, vtui.StringToCharInfo(cl.Prompt, vtui.Palette[ColCommandLinePrompt]))

	// 2. Draw Edit (input field)
	cl.Edit.Show(scr)
}

func (cl *CommandLine) ProcessKey(e *vtinput.InputEvent) bool {
	return cl.Edit.ProcessKey(e)
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
