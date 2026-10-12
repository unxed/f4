package panel

import (
	"testing"

	"github.com/unxed/vtui"
)

// f4#1148 (mitek1977-coder): the Ins/F4 dialog of the drive menu's Links has
// no blank rows between its fields, and its buttons sit right under a rule.
func TestDriveLinkDialogHasNoBlankRows(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	d := NewDriveBookmarkEditDialog(DriveBookmark{}, "/tmp", nil)
	var sep *vtui.Separator
	var buttons []*vtui.Button
	for _, child := range d.GetChildren() {
		switch c := child.(type) {
		case *vtui.Separator:
			sep = c
		case *vtui.Button:
			buttons = append(buttons, c)
		}
	}
	if sep == nil || len(buttons) != 2 {
		t.Fatalf("separator %v, %d buttons", sep != nil, len(buttons))
	}
	_, nameY, _, _ := d.nameEdit.GetPosition()
	_, pathY, _, _ := d.pathEdit.GetPosition()
	_, hotkeyY, _, _ := d.HotkeyEdit.GetPosition()
	_, sepY, _, _ := sep.GetPosition()
	_, buttonY, _, _ := buttons[0].GetPosition()
	if pathY != nameY+2 {
		t.Errorf("Path field at row %d, Name field at %d: want only the Path label between them", pathY, nameY)
	}
	if hotkeyY != pathY+1 {
		t.Errorf("Hot key at row %d, Path field at %d: want no blank row", hotkeyY, pathY)
	}
	if sepY != hotkeyY+2 {
		t.Errorf("rule at row %d, Hot key at %d: want only the hint between them", sepY, hotkeyY)
	}
	if buttonY != sepY+1 {
		t.Errorf("buttons at row %d, rule at %d: want them right under it", buttonY, sepY)
	}
	if buttonY >= d.Y2 {
		t.Errorf("buttons at row %d are not inside the frame ending at %d", buttonY, d.Y2)
	}
}
