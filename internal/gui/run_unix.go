//go:build (linux || darwin || openbsd || netbsd || dragonfly || freebsd || illumos || solaris) && !lite

package gui

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/vtui"
)

func runGuiWithStartupRecovery(backend string, startupComplete *atomic.Bool, run func() error) (err error) {
	// A display backend is an optional integration and may panic while
	// initializing (for example, while loading a Wayland cursor theme). Treat
	// that as a backend failure so automatic GUI selection can try another
	// available backend. Panics after SetupUI completes are application faults
	// and must retain the normal crash-reporting path.
	defer func() {
		if recovered := recover(); recovered != nil {
			if startupComplete.Load() {
				panic(recovered)
			}
			err = fmt.Errorf("%s GUI backend panicked during startup: %v", backend, recovered)
		}
	}()
	return run()
}

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

		var startupComplete atomic.Bool
		return runGuiWithStartupRecovery(backend, &startupComplete, func() error {
			applyDarwinDockIcon(backend)
			return vtui.RunInGUIWindow(config.App.GuiCols, config.App.GuiRows, backend, effectiveGuiFont(), float64(config.App.GuiFontSize), func() {
				setupUI()
				restoreGuiWindowPosition()
				startupComplete.Store(true)
			})
		})
	})
}
