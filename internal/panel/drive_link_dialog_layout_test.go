package panel

import (
	"testing"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtui"
)

// f4#1148 (mitek1977-coder): the Ins/F4 dialog of the drive menu's Links has
// Name: right under the top border, one blank row between the path and the
// hot key and none elsewhere, and its buttons right under a rule and right
// above the bottom border (12.10 list, point 7).
func TestDriveLinkDialogRows(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	d := NewDriveBookmarkEditDialog(DriveBookmark{}, "/tmp", nil)
	var sep *vtui.Separator
	var quickCall *vtui.Checkbox
	var buttons []*vtui.Button
	for _, child := range d.GetChildren() {
		switch c := child.(type) {
		case *vtui.Separator:
			sep = c
		case *vtui.Checkbox:
			quickCall = c
		case *vtui.Button:
			buttons = append(buttons, c)
		}
	}
	if sep == nil || len(buttons) != 2 {
		t.Fatalf("separator %v, %d buttons", sep != nil, len(buttons))
	}
	if quickCall == nil {
		t.Fatal("quick-call checkbox is missing")
	}
	_, nameY, _, _ := d.nameEdit.GetPosition()
	_, pathY, _, _ := d.pathEdit.GetPosition()
	hotkeyX, hotkeyY, hotkeyX2, _ := d.HotkeyEdit.GetPosition()
	quickX, quickY, _, _ := quickCall.GetPosition()
	_, sepY, _, _ := sep.GetPosition()
	_, buttonY, _, _ := buttons[0].GetPosition()
	if pathY != nameY+2 {
		t.Errorf("Path field at row %d, Name field at %d: want only the Path label between them", pathY, nameY)
	}
	if nameY != d.Y1+2 {
		t.Errorf("Name field at row %d, top border at %d: want only the Name label between them", nameY, d.Y1)
	}
	if hotkeyY != pathY+2 {
		t.Errorf("Hot key at row %d, Path field at %d: want one blank row between", hotkeyY, pathY)
	}
	if quickY != hotkeyY || quickX != hotkeyX2+2 {
		t.Errorf("quick-call checkbox at (%d,%d), hotkey at (%d,%d) width %d: want same row, one cell after field", quickX, quickY, hotkeyX, hotkeyY, hotkeyX2-hotkeyX+1)
	}
	if hotkeyX != d.X1+2+vtui.StringWidth(i18n.Msg("DriveLink.Hotkey")) {
		t.Errorf("hotkey field x = %d: want label immediately followed by field after shifting it left one cell", hotkeyX)
	}
	d.HotkeyEdit.SetText("Ctrl+Alt+anything")
	if got := d.HotkeyEdit.GetText(); got != "Ctrl+Alt+anything" {
		t.Errorf("hotkey field constrained arbitrary input: got %q", got)
	}
	if sepY != hotkeyY+2 {
		t.Errorf("rule at row %d, Hot key at %d: want only the hint between them", sepY, hotkeyY)
	}
	if buttonY != sepY+1 {
		t.Errorf("buttons at row %d, rule at %d: want them right under it", buttonY, sepY)
	}
	if buttonY != d.Y2-1 {
		t.Errorf("buttons at row %d, bottom border at %d: want them right above it", buttonY, d.Y2)
	}
}
