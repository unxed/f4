package settings

import (
	"context"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestSettingsRadioPresentationAudit(t *testing.T) {
	catalog := (coreSettingsProvider{}).Catalog()
	dropdowns := map[string]bool{"Language": true, "HelpLanguage": true, "GuiFont": true, "ColorStyle": true, "GuiBackend": true, "EditorDefaultCodePage": true, "ViewerDefaultCodePage": true, "EditorColorerScheme": true, "TerminalTheme": true}
	for _, f := range catalog.Fields {
		if f.Kind == f4settings.ChoiceKind && settingsUseRadios(f) == dropdowns[f.ID] {
			t.Errorf("unexpected presentation for %s", f.ID)
		}
	}
	f := f4settings.Field{Choices: f4settings.Choices("a:A", "b:B"), AllowCustom: true}
	if settingsUseRadios(f) {
		t.Fatal("custom values need an editor")
	}
}
func TestSettingsRadiosInteractionLayoutAndPalette(t *testing.T) {
	old := config.App
	defer func() { config.App = old; InitLang() }()
	palette := append([]uint64(nil), vtui.Palette...)
	defer copy(vtui.Palette, palette)
	for _, language := range []string{"en", "ru"} {
		config.App.Language = language
		config.App.UseLocalLanguageFiles = false
		InitLang()
		d, _ := (coreSettingsProvider{}).Begin(context.Background())
		defer d.Close()
		d.Values["DefaultFileOpMode"] = "0"
		c := newSettingsCenter([]*settingsSession{{catalog: (coreSettingsProvider{}).Catalog(), draft: d}})
		c.selectCategory("operations")
		var row *settingsRow
		for _, r := range c.page.rows {
			if r.field.ID == "DefaultFileOpMode" {
				row = r
			}
		}
		radios, ok := row.control.(*settingsRadios)
		if !ok {
			t.Fatal("operation mode is not radios")
		}
		scr := vtui.NewSilentScreenBuf()
		for iteration, width := range []int{220, 80, 220} {
			height := 40
			if width == 80 {
				height = 25
			}
			scr.AllocBuf(width, height)
			c.SetPosition(0, 0, width-1, height-1)
			if width == 220 && row.controlHeight != 1 {
				t.Fatal("short choices did not share a row")
			}
			for _, b := range radios.buttons {
				if b.X1 < radios.X1 || b.X2 > radios.X2 || b.Y2 > radios.Y2 {
					t.Fatal("radio exceeds its bounds")
				}
			}
			for _, slot := range []int{vtui.ColDialogText, vtui.ColDialogSelectedButton, vtui.ColDialogHighlightText, vtui.ColDialogHighlightSelectedButton, vtui.ColDialogBox, vtui.ColDialogBoxTitle, vtui.ColDialogIndicatorBackground} {
				vtui.Palette[slot] = vtui.SetRGBBoth(0, testutil.Uint32(0x807060+slot*100+iteration*0x101010), testutil.Uint32(0x101010+slot))
			}
			for _, disabled := range []bool{false, true} {
				radios.SetDisabled(disabled)
				for _, active := range []bool{false, true} {
					if active && !disabled {
						c.SetFocusedItem(c.page)
						c.page.SetFocusedItem(radios)
					} else {
						c.SetFocusedItem(c.sidebar)
					}
					for _, query := range []string{"", "no-matches-xyz"} {
						c.query = query
						c.updateMatches()
						c.Show(scr)
						for _, b := range radios.buttons {
							want, _ := b.GetStateAttrs(vtui.ColDialogText, vtui.ColDialogSelectedButton, vtui.ColDialogHighlightText, vtui.ColDialogHighlightSelectedButton)
							want = vtui.DialogIndicatorAttr(want, b.IsFocused())
							if query != "" {
								want = vtui.DimColor(want)
							}
							if got := scr.GetCell(b.X1, b.Y1).Attributes; got != want {
								t.Fatalf("radio palette: got %x want %x", got, want)
							}
						}
						border := vtui.Palette[vtui.ColDialogBox]
						if query != "" {
							border = vtui.DimColor(border)
						}
						if scr.GetCell(c.page.X1, radios.Y1).Attributes != border {
							t.Fatal("radio group border palette")
						}
						if c.page.total > c.page.Y2-c.page.Y1+1 && scr.GetCell(c.page.bar.X1, c.page.bar.Y1).Attributes != vtui.Palette[vtui.ColDialogBox] {
							t.Fatal("scrollbar palette")
						}
					}
				}
			}
			radios.SetDisabled(false)
		}
		c.query = ""
		c.updateMatches()
		c.SetFocusedItem(c.page)
		c.page.SetFocusedItem(radios)
		radios.SetFocusedItem(radios.buttons[0])
		c.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
		if radios.GetFocusedItem() != radios.buttons[1] || d.Values[row.field.ID] != "0" {
			t.Fatal("browsing must move focus without selecting")
		}
		if !strings.Contains(c.help.text, row.field.Choices[1].Description.Resolve(language, i18n.Msg)) {
			t.Fatal("choice help not shown")
		}
		c.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_SPACE})
		if d.Values[row.field.ID] != "1" || !radios.buttons[1].Selected || radios.buttons[0].Selected {
			t.Fatal("radio selection did not stage exactly one value")
		}
		b := radios.buttons[2]
		c.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: testutil.Int16(b.X1), MouseY: testutil.Int16(b.Y1), MouseEventFlags: vtinput.MouseMoved})
		if d.Values[row.field.ID] != "1" || !strings.Contains(c.help.text, row.field.Choices[2].Description.Resolve(language, i18n.Msg)) {
			t.Fatal("hover help modified selection or missing")
		}
		c.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT})
		if c.GetFocusedItem() != c.sidebar {
			t.Fatal("Left must return to categories")
		}
	}
}

func TestSettingsRadioUnknownValueAndMouseSelection(t *testing.T) {
	f := f4settings.Scalar("mode", "startup", "Modes", "Mode", "Choose a mode.", f4settings.ChoiceKind)
	f.Choices = f4settings.Choices("a:First", "b:Second")
	d := f4settings.NewDraft(map[string]string{"mode": "future"}, nil)
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories, Fields: []f4settings.Field{f}}, draft: d}})
	c.selectCategory("startup")
	c.SetPosition(0, 0, 149, 39)
	row := c.page.rows[1]
	r := row.control.(*settingsRadios)
	if len(r.buttons) != 3 || !r.buttons[2].Selected || len(d.Changed()) != 0 {
		t.Fatal("saved unknown choice was lost or changed")
	}
	b := r.buttons[0]
	c.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed, MouseX: testutil.Int16(b.X1), MouseY: testutil.Int16(b.Y1)})
	other := r.buttons[1]
	c.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed, MouseX: testutil.Int16(other.X1), MouseY: testutil.Int16(other.Y1), MouseEventFlags: vtinput.MouseMoved})
	c.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: testutil.Int16(other.X1), MouseY: testutil.Int16(other.Y1)})
	if d.Values["mode"] != "a" || !b.Selected || other.Selected || r.buttons[2].Selected {
		t.Fatal("radio drag changed selection or mouse click failed")
	}
}

func TestSettingsRadioCaptionThreeLayouts(t *testing.T) {
	oldConfig := config.App
	defer func() { config.App = oldConfig }()
	for _, test := range []struct {
		language, caption string
		labels            []string
	}{
		{"en", "Operation mode", []string{"Queue", "Background", "Foreground"}},
		{"ru", "Режим операции", []string{"Очередь", "В фоне", "На переднем плане"}},
	} {
		config.App.Language = test.language
		changed := false
		radios := newSettingsRadios(test.labels, 1, func(int) { changed = true })
		v := newSettingsViewport()
		row := &settingsRow{field: f4settings.Field{Group: "test", Label: f4settings.Text{English: test.caption, Literal: true}}, control: radios, match: true}
		v.rows = []*settingsRow{row}
		v.AddItem(radios)
		choicesWidth := radios.inlineWidth()
		fullWidth := vtui.StringWidth(test.caption) + 1 + choicesWidth
		for _, width := range []int{fullWidth, fullWidth - 1, choicesWidth, choicesWidth - 1, fullWidth} {
			v.SetPosition(2, 2, 2+width+5, 30)
			inline := width >= fullWidth
			stacked := width < choicesWidth
			if (row.controlX > 0) != inline || (row.controlY == 0) != inline {
				t.Fatalf("%s width %d: wrong caption placement", test.language, width)
			}
			if (row.controlHeight > 1) != stacked {
				t.Fatalf("%s width %d: wrong choice layout", test.language, width)
			}
			if inline && row.height != 1 {
				t.Fatal("inline caption added an extra line")
			}
			for i, b := range radios.buttons {
				if b.X1 < radios.X1 || b.X2 > radios.X2 || b.Y2 > radios.Y2 {
					t.Fatal("choice exceeds available space")
				}
				if i > 0 && ((b.Y1 > radios.buttons[0].Y1) != stacked) {
					t.Fatal("unexpected choice row")
				}
			}
			if changed || !radios.buttons[1].Selected {
				t.Fatal("resizing changed selection")
			}
		}
	}
}
