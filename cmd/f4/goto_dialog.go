package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/unxed/vtui"
)

// parseGotoOffset parses the byte position entered in a viewer/editor jump
// dialog. The checkbox selects the default base, while an explicit Far-style
// prefix or suffix always wins: 0xNN, $NN and NNh are hexadecimal, and NNd is
// decimal.
func parseGotoOffset(text string, hexadecimal bool) (int64, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return 0, fmt.Errorf("empty offset")
	}

	base := 10
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "0x"):
		base = 16
		s = s[2:]
	case strings.HasPrefix(s, "$"):
		base = 16
		s = s[1:]
	case strings.HasSuffix(lower, "h"):
		base = 16
		s = s[:len(s)-1]
	case strings.HasSuffix(lower, "d"):
		base = 10
		s = s[:len(s)-1]
	default:
		if hexadecimal {
			base = 16
		}
	}
	if s == "" {
		return 0, fmt.Errorf("invalid offset %q", text)
	}
	n, err := strconv.ParseInt(s, base, 64)
	if err != nil || n < 0 {
		if err == nil {
			err = fmt.Errorf("offset must not be negative")
		}
		return 0, fmt.Errorf("invalid offset %q: %w", text, err)
	}
	return n, nil
}

func gotoText(key, fallback string) string {
	value := Msg(key)
	if strings.HasPrefix(value, "{") {
		return fallback
	}
	return value
}

func showGotoOffsetDialog(anchor vtui.Frame, title, prompt string, current int64, onOK func(int64)) *vtui.Window {
	const width, height = 48, 11
	dlg := vtui.NewCenteredDialog(width, height, title)
	dlg.ShowClose = true

	lblOffset := vtui.NewLabel(0, 0, prompt, nil)
	editOffset := vtui.NewEdit(0, 0, 34, strconv.FormatInt(current, 10))
	editOffset.SelectAll()
	lblOffset.FocusLink = editOffset
	dlg.SetFocusedItem(editOffset)
	chkHex := vtui.NewCheckbox(0, 0, gotoText("Goto.Hexadecimal", "Hexadecimal"), false)
	btnOK := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOK.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	dlg.AddItem(lblOffset)
	dlg.AddItem(editOffset)
	dlg.AddItem(chkHex)
	dlg.AddItem(btnOK)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(lblOffset, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editOffset, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(chkHex, vtui.Margins{Top: 1}, vtui.AlignLeft)

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOK, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnOK.OnClick = func() {
		offset, err := parseGotoOffset(editOffset.GetText(), chkHex.State == 1)
		if err != nil {
			vtui.ShowMessageOn(dlg, title, err.Error(), []string{"&Ok"})
			return
		}
		if onOK != nil {
			onOK(offset)
		}
		dlg.Close()
	}
	btnCancel.OnClick = func() { dlg.Close() }

	vtui.FrameManager.PushToFrameScreen(anchor, dlg)
	return dlg
}

func showEditorPositionDialog(anchor vtui.Frame, line, position int, onOK func(line, position int)) *vtui.Window {
	const width, height = 48, 13
	dlg := vtui.NewCenteredDialog(width, height, gotoText("Editor.GotoTitle", "Go to position"))
	dlg.ShowClose = true

	lblLine := vtui.NewLabel(0, 0, gotoText("Editor.GotoLine", "Line:"), nil)
	editLine := vtui.NewEdit(0, 0, 18, strconv.Itoa(line))
	lblLine.FocusLink = editLine
	dlg.SetFocusedItem(editLine)
	lblPosition := vtui.NewLabel(0, 0, gotoText("Editor.GotoPosition", "Position:"), nil)
	editPosition := vtui.NewEdit(0, 0, 18, strconv.Itoa(position))

	btnOK := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOK.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	dlg.AddItem(lblLine)
	dlg.AddItem(editLine)
	dlg.AddItem(lblPosition)
	dlg.AddItem(editPosition)
	dlg.AddItem(btnOK)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(lblLine, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editLine, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(lblPosition, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editPosition, vtui.Margins{Top: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOK, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnOK.OnClick = func() {
		line, lineErr := strconv.Atoi(strings.TrimSpace(editLine.GetText()))
		position, positionErr := strconv.Atoi(strings.TrimSpace(editPosition.GetText()))
		if lineErr != nil || positionErr != nil || line < 1 || position < 1 {
			vtui.ShowMessageOn(dlg, dlg.GetTitle(), gotoText("Editor.GotoInvalid", "Line and position must be positive numbers."), []string{"&Ok"})
			return
		}
		if onOK != nil {
			onOK(line, position)
		}
		dlg.Close()
	}
	btnCancel.OnClick = func() { dlg.Close() }

	vtui.FrameManager.PushToFrameScreen(anchor, dlg)
	return dlg
}
