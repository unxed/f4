package panel

import (
	"testing"

	"github.com/unxed/vtui"
)

type overlayModal struct {
	*vtui.VMenu
	allow bool
}

func (m overlayModal) AllowsProgressOverlay() bool { return m.allow }

// A modal that declares itself safe to sit behind a progress screen must not
// hold the screen back, otherwise an update started from the Settings Center
// downloads unseen until the settings are closed (#1202).
func TestProgressBlockedByModalHonoursOverlayHost(t *testing.T) {
	menu := vtui.NewVMenu("modal")
	if !progressBlockedByModal(coverageFrameStack{top: overlayModal{VMenu: menu}}) {
		t.Fatal("a modal that does not opt in must block the progress screen")
	}
	if progressBlockedByModal(coverageFrameStack{top: overlayModal{VMenu: menu, allow: true}}) {
		t.Fatal("a modal that allows an overlay must not block the progress screen")
	}
}
