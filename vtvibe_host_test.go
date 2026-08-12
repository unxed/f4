package main

import (
	"github.com/unxed/vtui"
	"testing"
)

func TestIsAIPanel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()

	if isAIPanel(pf.panels[0]) {
		t.Error("OSVFS should not be identified as AI panel")
	}
}
