package archive

import "github.com/unxed/f4/vfs"

// The tests of the archiving flow answer the Add to archive window through
// the InputBox of their mock app: the name they give is the archive name,
// with the format its extension names and the other settings at their
// defaults; an empty name cancels, as Cancel does.
func init() {
	showAddArchiveDialog = func(app vfs.App, initialName string, _ bool, done func(addArchiveSettings)) {
		app.InputBox(" Add to archive ", "Archive name:", initialName, func(name string) {
			if name == "" {
				return
			}
			format := addArchiveFormatOf(name)
			if format < 0 {
				format = 0
			}
			done(addArchiveSettings{Name: name, Format: format, Solid: true})
		})
	}
}
