package app

import (
	"testing"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtui"
)

func TestVtvibeCommandsCoverHelpAndEmptyDraftPaths(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := &panel.PanelsFrame{}
	t.Cleanup(testutil.SetFrameManagerScreens(t, []*vtui.AppScreen{{Number: 1, Frames: []vtui.Frame{pf}}}, 0))

	aiSession().ClearDraft()
	for _, command := range []string{"help", "?", ""} {
		aiCommand(pf, command)
		top := vtui.FrameManager.GetTopFrame()
		if top == nil || top == pf {
			t.Fatalf("ai:%q did not open its feedback dialog; top=%T", command, top)
		}
		top.Close()
		vtui.FrameManager.RemoveFrame(top)
	}
}
