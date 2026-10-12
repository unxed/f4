package settings

import (
	"reflect"
	"testing"

	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestConfirmedTextSettingsKeepsInputFocus(t *testing.T) {
	field := f4settings.Scalar("location", "startup", "Locations", "Location", "Example location.", f4settings.String)
	draft := f4settings.NewDraft(map[string]string{"location": ""}, nil)
	defer draft.Close()
	center := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories, Fields: []f4settings.Field{field}}, draft: draft}})
	center.selectCategory("startup")
	var edit *settingsEdit
	for _, row := range center.page.rows {
		if row.field.ID == "location" {
			edit = row.control.(*settingsEdit)
		}
	}
	if edit == nil {
		t.Fatal("test input missing")
	}
	center.page.SetFocusedItem(edit)
	center.SetFocusedItem(center.page)
	event := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: 'ƒ', VirtualKeyCode: vtinput.VK_F, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed}
	original := event
	if !center.ProcessKey(&event) || center.GetFocusedItem() != center.page || edit.GetText() != "ƒ" {
		t.Fatalf("committed text moved focus or lost input: focus=%T text=%q", center.GetFocusedItem(), edit.GetText())
	}
	if !reflect.DeepEqual(event, original) {
		t.Fatal("settings changed source event")
	}
}

func TestConfirmedTextCannotModifyReadOnlySettings(t *testing.T) {
	edit := &settingsEdit{Edit: vtui.NewEdit(0, 0, 30, "unchanged")}
	edit.SetDisabled(true)
	event := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: 'ç', VirtualKeyCode: vtinput.VK_C, ControlKeyState: vtinput.LeftCtrlPressed}
	if !edit.ProcessKey(&event) || edit.GetText() != "unchanged" {
		t.Fatalf("committed text changed read-only setting: %q", edit.GetText())
	}
}
