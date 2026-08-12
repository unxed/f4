package envman

import (
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func showEnvManMessage(app vfs.App, title, message string, buttons []string, kind vtui.MessageKind) *vtui.Window {
	if vtui.FrameManager == nil {
		return nil
	}
	title = " " + title + " "
	if anchor, ok := app.(vtui.Frame); ok {
		return vtui.ShowMessageOnEx(anchor, title, message, buttons, kind)
	}
	return vtui.ShowMessageEx(title, message, buttons, kind)
}
