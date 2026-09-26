//go:build windows && !lite

package gui

import (
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/vtui"
)

// RunGui opens a window on the named backend. setupUI is called once the
// window exists and must build the interface inside it; everything after it
// runs on the GUI thread.
func RunGui(backend string, setupUI func()) error {
	return WithRuntime(func() error {
		if backend == "qt" || strings.HasPrefix(backend, "ext:") {
			return plughost.RunExternalUIWithMapping(backend)
		}
		if err := checkGUIBackendAvailability(backend); err != nil {
			return err
		}
		stopIconManager := startWindowsWindowIconManager()
		defer stopIconManager()
		return vtui.RunInGUIWindow(config.App.GuiCols, config.App.GuiRows, backend, effectiveGuiFont(), float64(config.App.GuiFontSize), func() {
			setupUI()
			restoreGuiWindowPosition()
		})
	})
}
