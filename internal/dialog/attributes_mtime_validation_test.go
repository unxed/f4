package dialog

import (
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// findMTimeEdit returns the M-Time field: the only *vtui.Edit in the dialog
// whose text matches how the attributes dialog formats item.MTime.
func findMTimeEdit(t *testing.T, dlg vtui.UIElement, item vfs.VFSItem) *vtui.Edit {
	t.Helper()
	want := item.MTime.Format(attributesTimeFormat)
	var found *vtui.Edit
	walkUI(dlg, func(el vtui.UIElement) bool {
		if e, ok := el.(*vtui.Edit); ok && e.GetText() == want {
			found = e
		}
		return true
	})
	if found == nil {
		t.Fatal("could not find the M-Time edit field")
	}
	return found
}

// f4 #1404: an unparseable date/time used to be silently dropped instead of
// applied — the dialog closed as if the click had done something, and
// nothing explained why the file's mtime never changed.
func TestAttributesDialog_UnixInvalidMTimeIsRejected(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	var called bool
	mockVFS := &mockMetadataVFS{
		VFS: vfs.NewOSVFS(t.TempDir()),
		onSetAttrPath: func(string, vfs.VFSItem) {
			called = true
		},
	}
	item := vfs.VFSItem{Name: "a.txt", UnixMode: 0644, MTime: time.Now()}

	ShowAttributesUnix(nil, mockVFS, "a.txt", item)
	dlg := fm.GetTopFrame().(vtui.Container)
	editMTime := findMTimeEdit(t, dlg.(vtui.UIElement), item)
	editMTime.SetText("not a date")

	var setButton *vtui.Button
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if b, ok := el.(*vtui.Button); ok && b.IsDefault {
			setButton = b
		}
		return true
	})
	if setButton == nil {
		t.Fatal("could not find the Set button")
	}

	setButton.OnClick()

	if called {
		t.Error("an invalid date must not reach SetAttributes")
	}
	if dlg.(vtui.Frame).IsDone() {
		t.Error("the dialog closed on an invalid date instead of reporting the error")
	}
	if top := fm.GetTopFrame(); any(top) == any(dlg) {
		t.Error("no error dialog appeared over the attributes dialog")
	}
}
