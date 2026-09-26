package panel

import (
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/media"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtui"
)

// hotkeyConditions are the checks a binding may name after a colon
// ("Esc:EscToggle"). Every one of them asks this frame what is on screen, which
// is why they live here and not with the hotkey manager.
var hotkeyConditions = map[string]func() bool{
	"searchfirst": func() bool {
		return config.App.NavigationMode == config.NavigationSearchFirst
	},
	"emptycommandline": func() bool {
		if pf := FindPanelsFrameAnyScreen(); pf != nil {
			return pf.CmdLine.IsEmpty()
		}
		return false
	},
	"commandlinenotempty": func() bool {
		if pf := FindPanelsFrameAnyScreen(); pf != nil {
			return !pf.CmdLine.IsEmpty()
		}
		return false
	},
	"esctoggle": func() bool {
		if !config.App.EscTogglePanels {
			return false
		}
		if pf := FindPanelsFrameAnyScreen(); pf != nil {
			if !pf.CmdLine.IsEmpty() {
				return false
			}
			if pf.ShowPanels {
				return true
			}
			if pf.TermView == nil {
				return false
			}
			// A highlighted terminal selection owns plain Esc. PanelsFrame clears
			// that selection and swallows the matching key-up; letting EscToggle
			// run first would show the panels and leave the selected text copied
			// or otherwise handled by the terminal path (#881).
			if pf.TermView.HasSelection() {
				return false
			}
			return !pf.TermView.UseAltScreen && !pf.IsPtyBusy()
		}
		return false
	},
	// noaltscreenapp is the looser gate: it stands down only for an AltScreen
	// application (mc, htop), not for a child that is merely busy. The escape
	// hatch Ctrl+Alt+Z is bound through it so that no running program can lock
	// the panels away (#50); keys that belong to the running program, Ctrl+O
	// included (#249, #1376), use noterminalapp.
	"noaltscreenapp": func() bool {
		if pf := FindPanelsFrameAnyScreen(); pf != nil {
			if pf.ShowPanels {
				return true
			}
			if pf.ShellMode == terminal.ShellModeSimpleInline {
				// This mode has no terminal.PTY, so pf.termView is a leftover
				// background object (kept around for cwd-sync passthrough,
				// see PTY_WIN_TRACE in the debug log) that does not reflect
				// what's on screen. Nothing it does can ever be a foreign
				// full-screen app stealing these keys: the console view is
				// always f4's own overlay. Checking its UseAltScreen here
				// made a stray flip of that background flag swallow every
				// key this condition gates — Ctrl+O included, so a second
				// Ctrl+O while in the console view did nothing at all.
				return true
			}
			return pf.TermView != nil && !pf.TermView.UseAltScreen
		}
		return false
	},
	// noterminalapp is the stricter sibling of noaltscreenapp: it also
	// stands down for a plain child process that is merely busy (a shell
	// command, a REPL). With the panels hidden such a process owns the
	// keyboard, as it does in far2l's terminal, and the command line is not
	// even drawn, so neither actions that type into it nor the file-manager
	// keys (F2, F7, F10, Alt+F1...) may fire. The AltScreen test alone is not
	// enough for those: a Windows console program such as Far Manager draws
	// full screen without ever switching to the alternate screen (#1376).
	// With the panels shown nothing is in the way, which keeps the Shell
	// binding of such a key unconditional -- also when those panels were
	// raised over a program that is still running: the keyboard is f4's
	// then, and Ctrl+O has to be able to hand it back.
	"noterminalapp": func() bool {
		if pf := FindPanelsFrameAnyScreen(); pf != nil {
			// Same reasoning for SimpleInline as noaltscreenapp above: no
			// terminal.PTY means no foreign process can be busy on screen in
			// this mode. A command f4 itself launched (runSimpleInlineCommand)
			// still owns the keyboard while it runs, but that state already
			// routes through SetBusy/isPtyBusy on f4's own frame, not through
			// the background termView. TerminalOwnsKeyboard encodes all of it.
			return !pf.TerminalOwnsKeyboard()
		}
		return false
	},
	// terminalquiet reports a hidden-panels terminal with no AltScreen app
	// and no busy terminal.PTY, so F3/F4 may open the terminal log instead of
	// being forwarded to the running application.
	"terminalquiet": func() bool {
		if pf := FindPanelsFrameAnyScreen(); pf != nil {
			// Same reasoning as noaltscreenapp/noterminalapp above (see
			// TerminalOwnsKeyboard): SimpleInline has no terminal.PTY, so
			// pf.TermView is a leftover background object that does not
			// reflect what's on screen, and no foreign program can ever be
			// "loud" here anyway. Reading its UseAltScreen field hit the
			// exact stray-flip bug that broke Ctrl+O in this mode (f4#1376)
			// before TerminalOwnsKeyboard got the same short-circuit; F3/F4
			// (f4#897) were left reading it directly and so stayed broken.
			if pf.ShellMode == terminal.ShellModeSimpleInline {
				return true
			}
			return pf.TermView != nil && !pf.TermView.UseAltScreen && !pf.IsPtyBusy()
		}
		return false
	},
	// altpanelvisible reports that an info or quick-view panel is shown,
	// gating the plain-letter toggles that belong to those panels.
	"altpanelvisible": func() bool {
		if pf := FindPanelsFrameAnyScreen(); pf != nil {
			for _, a := range pf.AltPanels {
				if a != nil && (a.Kind() == "info" || a.Kind() == "quick_view") {
					return true
				}
			}
		}
		return false
	},
}

// nativeShortcutOwnedByCurrentContext filters framework fallbacks that never
// reach the advertised action in the active frame. This is separate from
// keymap.HotkeyManager overrides: these keys are consumed directly by the frame
// before vtui gets a chance to apply its workspace/help fallback.
func nativeShortcutOwnedByCurrentContext(actionName, key string) bool {
	if vtui.FrameManager == nil {
		return false
	}
	top := nativeShortcutContextFrame()
	if top == nil {
		return false
	}
	// TypeUser modal frames (notably Help and Screen Grabber) own their input
	// before vtui's framework fallbacks. Some deliberately release individual
	// keys, but there is no ownership API that can prove that generically; omit
	// native hints rather than advertise a chord the current modal may swallow.
	if top.IsModal() {
		return true
	}

	// Ctrl+N's native implementation is an active-stack CmResize broadcast.
	// Editor, viewer, image and queue workspaces carry no panels frame of
	// their own, but they serve that broadcast through
	// handleWorkspaceForkCommand, so the chord is truthful there too. What it
	// still cannot do is clone panels that do not exist anywhere: with every
	// workspace panel-less there is nothing to fork, and advertising the key
	// would promise a screen flash and no new workspace.
	if strings.EqualFold(actionName, "Workspace.New") && strings.EqualFold(key, "CtrlN") {
		if FindPanelsFrameAnyScreen() == nil {
			return true
		}
	}

	switch frame := top.(type) {
	case *media.ImageView:
		// F12 belongs to the gallery while the image viewer is active, not to
		// vtui's workspace list fallback.
		return strings.EqualFold(key, "F12")
	case *fileops.QueueFrame:
		// An active queue swallows Ctrl+W to preserve running operations.
		return strings.EqualFold(key, "CtrlW") && fileops.QueueHasActiveTasks()
	case *PanelsFrame:
		terminalOwnsInput := !frame.ShowPanels &&
			((frame.TermView != nil && frame.TermView.UseAltScreen) || frame.IsPtyBusy())
		if !terminalOwnsInput {
			return false
		}
		// PanelsFrame explicitly releases Ctrl+Shift+Tab and, when the
		// preference is enabled, Ctrl+N before raw terminal forwarding.
		// Plain Ctrl+Tab is only released this way without an advanced input
		// protocol: once win32-input-mode or the kitty keyboard protocol is
		// negotiated, frame.go hands Ctrl+Tab to the running program instead
		// (far2l's own panel switch, for one), so the Next Workspace hint
		// must not claim a chord it no longer receives in that state (f4#128).
		if strings.EqualFold(key, "CtrlShiftTab") {
			return false
		}
		if strings.EqualFold(key, "CtrlTab") {
			advanced := frame.TermView != nil && (frame.TermView.Win32InputMode || frame.TermView.KittyFlags != 0)
			return advanced
		}
		if strings.EqualFold(key, "CtrlN") && config.App.TerminalCtrlNWorkspace {
			return false
		}
		return true
	}
	return false
}

// nativeShortcutContextFrame returns the frame whose input context is being
// described by a menu. A VMenu is temporarily placed above its owner while it
// is painted, but it must not make the owner's native shortcuts disappear
// from the menu labels themselves.
func nativeShortcutContextFrame() vtui.Frame {
	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetType() != vtui.TypeMenu {
		return top
	}
	frames := vtui.FrameManager.GetActiveFrames(vtui.FrameManager.ActiveIdx)
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i] != nil && frames[i].GetType() != vtui.TypeMenu {
			return frames[i]
		}
	}
	return nil
}

func init() {
	for name, condition := range hotkeyConditions {
		keymap.RegisterCondition(name, condition)
	}
	keymap.NativeShortcutOwnedByCurrentContext = nativeShortcutOwnedByCurrentContext
}
