package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/unxed/f4/internal/panel"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/internal/vtvibe"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// vtvibe is the AI panel of f4. This file is the only place where it touches
// the core: it publishes the ai:// drive, the "ai:" command prefix and the
// registry actions. Everything else lives in the vtvibe package.

const vtvibeIniName = "vtvibe.ini"

var (
	vtvibeOnce    sync.Once
	vtvibeSession *vtvibe.Session
)

// aiSession returns the single dialog shared by every ai:// mount.
func aiSession() *vtvibe.Session {
	vtvibeOnce.Do(func() {
		vtvibeSession = vtvibe.NewSession()
		// The dialog outlives f4: it is read back from disk here and saved
		// after every change (f4#1842, stage H3).
		if err := vtvibeSession.SetStorePath(vtvibeDialogPath()); err != nil {
			vtui.DebugLog("AI: dialog not restored: %v", err)
		}
	})
	return vtvibeSession
}

// vtvibeDialogPath is the current dialog's file; finished dialogs are moved
// to vtvibeArchiveDir by ai:new.
func vtvibeDialogPath() string {
	return filepath.Join(config.GetF4ConfigDir(), "ai", "dialog.json")
}

func vtvibeArchiveDir() string {
	return filepath.Join(config.GetF4ConfigDir(), "ai", "dialogs")
}

func vtvibeIniPath() string {
	return filepath.Join(config.GetF4ConfigDir(), vtvibeIniName)
}

// vtvibeConfig re-reads the settings on every use, so editing vtvibe.ini or
// exporting a key does not need a restart.
func vtvibeConfig() (vtvibe.Config, string) {
	cfg, keySource, _ := vtvibeProviderConfig()
	return cfg, keySource
}

// vtvibeProviderConfig is vtvibeConfig together with the preset it used: the
// provider chosen in vtvibe.ini decides the address, the default model and
// which environment variables carry the key (#1842).
func vtvibeProviderConfig() (vtvibe.Config, string, vtvibe.Provider) {
	cfg, keySource, provider := vtvibeReadConfig()
	aiSession().SetStatus(vtvibe.Status{BaseURL: cfg.BaseURL, Model: cfg.Model, KeySource: keySource})
	return cfg, keySource, provider
}

// vtvibeReadConfig reads the settings alone, without touching the panel's
// dialog: f4 --ai uses it before any UI exists.
func vtvibeReadConfig() (vtvibe.Config, string, vtvibe.Provider) {
	ini := ini.Load(vtvibeIniPath())
	provider := vtvibe.ResolveProvider(ini.GetString("general", "provider", ""), ini.GetString("general", "base_url", ""))
	cfg := vtvibe.Config{
		Kind:    provider.Kind,
		BaseURL: provider.Endpoint(ini.GetString("general", "base_url", "")),
		Model:   provider.EffectiveModel(ini.GetString("general", "model", "")),
	}
	keySource := ""
	for _, name := range provider.KeyEnv {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			cfg.APIKey, keySource = v, name
			break
		}
	}
	if cfg.APIKey == "" {
		if v := strings.TrimSpace(ini.GetString("general", "key", "")); v != "" {
			cfg.APIKey, keySource = v, vtvibeIniName
		}
	}
	return cfg, keySource, provider
}

// vtvibeSaveSetting rewrites one key of vtvibe.ini, keeping the rest.
func vtvibeSaveSetting(key, value string) error {
	path := vtvibeIniPath()
	ini := ini.Load(path)
	if ini.Sections()["general"] == nil {
		ini.Sections()["general"] = map[string]string{}
	}
	ini.Sections()["general"][key] = value

	keys := make([]string, 0, len(ini.Sections()["general"]))
	for k := range ini.Sections()["general"] {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString("[general]\n")
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s = %s\n", k, ini.Sections()["general"][k])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// 0600: the file may hold an API key.
	return os.WriteFile(path, []byte(sb.String()), 0600)
}

func init() {
	sysinfo.RegisterDrive("AI", func() vfs.VFS { return &aiVFSWrapper{vtvibe.NewVFS(aiSession())} })

	if _, err := (&coreAPI{}).RegisterCommandPrefix("vtvibe", "ai", aiCommand); err != nil {
		vtui.DebugLog("VTVIBE: cannot register the ai: prefix: %v", err)
	}

	withAI := func(fn func(pf *panel.PanelsFrame)) func() bool {
		return func() bool {
			if pf := panel.FindPanelsFrameAnyScreen(); pf != nil {
				fn(pf)
				return true
			}
			return false
		}
	}

	registerAction(action.Action{
		Name:        "AI.TogglePanel",
		Area:        "Shell",
		Label:       "AI Panel",
		LabelKey:    "Action.AI.TogglePanel",
		Description: "Open or close the AI panel on the passive panel",
		DescKey:     "Action.AI.TogglePanel.Desc",
		DefaultKeys: []string{"RCtrlA"},
		MenuPath:    "Commands",
		MenuSubPath: "AI",
		Handler:     withAI(func(pf *panel.PanelsFrame) { aiTogglePanel(pf) }),
	})
	registerAction(action.Action{
		Name:        "AI.Ask",
		Area:        "Common",
		Label:       "Ask the AI",
		LabelKey:    "Action.AI.Ask",
		Description: "Type a question and send it together with the ai:// context",
		DescKey:     "Action.AI.Ask.Desc",
		MenuPath:    "Commands",
		Handler:     func() bool { return aiAskAction() },
	})
	registerAction(action.Action{
		Name:        "AI.NewSession",
		Area:        "Shell",
		Label:       "New AI Dialog",
		LabelKey:    "Action.AI.NewSession",
		Description: "Clear the AI dialog history and artifacts, keeping the context files",
		DescKey:     "Action.AI.NewSession.Desc",
		MenuPath:    "Commands",
		MenuSubPath: "AI",
		Visible:     func() bool { return isAIPanelActive() },
		Handler:     withAI(func(pf *panel.PanelsFrame) { aiNewSession(pf) }),
	})
	registerAction(action.Action{
		Name:        "AI.ApplyPatch",
		Area:        "Shell",
		Label:       "Apply AP Patch",
		LabelKey:    "Action.AI.ApplyPatch",
		Description: "Apply the ap patch from the last answer to the folder in the other panel",
		DescKey:     "Action.AI.ApplyPatch.Desc",
		DefaultKeys: []string{"RCtrlP"},
		MenuPath:    "Commands",
		MenuSubPath: "AI",
		Visible:     func() bool { return aiSession().LastPatch() != nil },
		Handler:     withAI(func(pf *panel.PanelsFrame) { aiApplyPatch(pf) }),
	})
	registerAction(action.Action{
		Name:        "AI.UndoPatch",
		Area:        "Shell",
		Label:       "Undo AP Patch",
		LabelKey:    "Action.AI.UndoPatch",
		Description: "Put back the files the last applied ap patch changed",
		DescKey:     "Action.AI.UndoPatch.Desc",
		MenuPath:    "Commands",
		MenuSubPath: "AI",
		Visible:     func() bool { return aiTopUndo() != nil },
		Handler:     withAI(func(pf *panel.PanelsFrame) { aiUndoPatch(pf) }),
	})
	registerAction(action.Action{
		Name:        "AI.Setup",
		Area:        "Shell",
		Label:       "AI Setup",
		LabelKey:    "Action.AI.Setup",
		Description: "Set the API key and the model used by the AI panel",
		DescKey:     "Action.AI.Setup.Desc",
		MenuPath:    "Options",
		Handler:     withAI(func(pf *panel.PanelsFrame) { aiSetupDialog(pf) }),
	})
	registerAction(action.Action{
		Name:        "AI.Help",
		Area:        "Shell",
		Label:       "AI command help",
		LabelKey:    "Action.AI.Help",
		Description: "Show the available AI commands and command-line forms",
		DescKey:     "Action.AI.Help.Desc",
		MenuPath:    "Commands",
		MenuSubPath: "AI",
		Handler:     withAI(func(pf *panel.PanelsFrame) { aiCommand(pf, "help") }),
	})
	registerAction(action.Action{
		Name:        "AI.AttachAPSpec",
		Area:        "Shell",
		Label:       "Attach AP specification",
		LabelKey:    "Action.AI.AttachAPSpec",
		Description: "Attach the current project's AP specification to the AI context",
		DescKey:     "Action.AI.AttachAPSpec.Desc",
		MenuPath:    "Commands",
		MenuSubPath: "AI",
		Handler:     withAI(func(pf *panel.PanelsFrame) { aiAttachAPSpec(pf) }),
	})
	registerAction(action.Action{
		Name:        "AI.ListModels",
		Area:        "Shell",
		Label:       "List AI models",
		LabelKey:    "Action.AI.ListModels",
		Description: "Query and show the AI models available from the configured provider",
		DescKey:     "Action.AI.ListModels.Desc",
		MenuPath:    "Commands",
		MenuSubPath: "AI",
		Handler:     withAI(func(pf *panel.PanelsFrame) { aiListModels(pf) }),
	})
}

type aiVFSWrapper struct {
	*vtvibe.AIVFS
}

func (w *aiVFSWrapper) ProcessPanelKey(app vfs.App, e *vtinput.InputEvent) bool {
	if !e.KeyDown {
		return false
	}
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0

	if ctrl && !alt && !shift {
		pf, ok := app.(*panel.PanelsFrame)
		if !ok {
			return false
		}

		idx := -1
		for i, p := range pf.Panels {
			if fsp, isFsp := p.(*panel.FileSystemPanel); isFsp && fsp.Vfs == w {
				idx = i
				break
			}
		}
		if idx == -1 {
			return false
		}

		switch e.VirtualKeyCode {
		case vtinput.VK_1:
			AiSetViewModePanel(pf, idx, "ai://ctx", false)
			return true
		case vtinput.VK_2:
			AiSetViewModePanel(pf, idx, "ai://chat", true)
			return true
		case vtinput.VK_3:
			AiSetViewModePanel(pf, idx, "ai://out", false)
			return true
		case vtinput.VK_4:
			AiSetViewModePanel(pf, idx, "ai://mem", false)
			return true
		}
	}
	return false
}

func aiTogglePanel(pf *panel.PanelsFrame) {
	// If any panel is AI, close it entirely
	for i, p := range pf.Panels {
		if fsp, ok := p.(*panel.FileSystemPanel); ok {
			if _, isAI := fsp.Vfs.(*aiVFSWrapper); isAI {
				pf.ExitWide()
				if pf.AltPanels[i] != nil && pf.AltPanels[i].Kind() == "ai_chat" {
					if c, ok := pf.AltPanels[i].(interface{ Close() }); ok {
						c.Close()
					}
					pf.AltPanels[i] = nil
				}
				target := panel.AIPrevPath[i]
				if target == "" {
					target, _ = hostmode.UserHomeDir()
				}
				pf.SwitchToVFS(fsp, vfs.NewOSVFS(target))
				pf.ActiveIdx = 1 - i
				return
			}
		}
	}

	idx := 1 - pf.ActiveIdx
	fsp := pf.Panels[idx].(*panel.FileSystemPanel)
	panel.AIPrevPath[idx] = fsp.Vfs.GetPath()
	vtvibeConfig()
	pf.SwitchToVFS(fsp, &aiVFSWrapper{vtvibe.NewVFS(aiSession())})

	AiSetViewModePanel(pf, idx, "ai://chat", true)
}

func AiSetViewModePanel(pf *panel.PanelsFrame, idx int, path string, isChat bool) {
	fsp := pf.Panels[idx].(*panel.FileSystemPanel)
	if _, isAI := fsp.Vfs.(*aiVFSWrapper); !isAI {
		return
	}

	currentAlt := pf.AltPanels[idx]

	if isChat {
		if currentAlt == nil || currentAlt.Kind() != "ai_chat" {
			if currentAlt != nil {
				if c, ok := currentAlt.(interface{ Close() }); ok {
					c.Close()
				}
			}
			chatAlt := NewAIChatPanel(fsp)
			pf.AltPanels[idx] = chatAlt
			chatAlt.ScrollToBottom()
		}
		if path != "" {
			pf.NavigateToPath(fsp, path)
		}
	} else {
		if currentAlt != nil && currentAlt.Kind() == "ai_chat" {
			if c, ok := currentAlt.(interface{ Close() }); ok {
				c.Close()
			}
			pf.AltPanels[idx] = nil
		}
		if path != "" {
			pf.NavigateToPath(fsp, path)
		}
	}
	pf.ResizeConsole(pf.LastW, pf.LastH)
	vtui.FrameManager.HardRefresh()
}

func aiNewSession(pf *panel.PanelsFrame) {
	if _, err := aiSession().Archive(vtvibeArchiveDir()); err != nil {
		aiShowError(err)
		return
	}
	aiSession().Reset(true)
	vtvibeConfig()
	pf.RefreshAll()
	vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.NewSessionDone"), []string{i18n.Msg("vtui.Ok")})
}

func aiAskAction() bool {
	fm := vtui.FrameManager
	if fm == nil {
		return false
	}

	// 1. Gather context from the currently active frame before switching workspaces
	var ctxParts []string
	top := fm.GetTopFrame()

	if top != nil {
		if help := top.GetHelp(); help != "" {
			ctxParts = append(ctxParts, "UI Context: "+help)
		}
		if fc, ok := top.(vtui.FocusContainer); ok {
			if foc := fc.GetFocusedItem(); foc != nil {
				if h := foc.GetHelp(); h != "" {
					ctxParts = append(ctxParts, "Focused Item: "+h)
				}
			}
		}
	}

	switch f := top.(type) {
	case *editor.EditorView:
		ctxParts = append(ctxParts, "Editor: "+f.Vfs.Base(f.FilePath))
		ctxParts = append(ctxParts, fmt.Sprintf("Line: %d", f.CursorLine+1))
		if f.SelActive || f.RectSelActive {
			ctxParts = append(ctxParts, "[Text is selected]")
		}
	case *viewer.ViewerView:
		ctxParts = append(ctxParts, "Viewer: "+f.VFS.Base(f.Path))
		ctxParts = append(ctxParts, fmt.Sprintf("Offset: %d", f.TopOffset))
	}

	pf := panel.FindPanelsFrameAnyScreen()
	if pf != nil {
		fsp := pf.GetActivePanel()
		if fsp != nil {
			_, isAI := fsp.Vfs.(*aiVFSWrapper)
			if !isAI {
				ctxParts = append(ctxParts, "Path: "+fsp.Vfs.GetPath())
				if name := fsp.GetSelectedName(); name != "" && name != ".." {
					ctxParts = append(ctxParts, "Focus: "+name)
				}
				marked := fsp.GetMarkedNames()
				if len(marked) > 0 {
					ctxParts = append(ctxParts, fmt.Sprintf("Selected files: %d", len(marked)))
				}
			}
		}
	}

	// 2. Find an existing AI workspace or create a new one by forking
	var aiPf *panel.PanelsFrame
	for i, s := range fm.Screens {
		if len(s.Frames) > 0 {
			if screenPf, ok := s.Frames[len(s.Frames)-1].(*panel.PanelsFrame); ok {
				aiIdx := -1
				for idx, p := range screenPf.Panels {
					if fsp, ok := p.(*panel.FileSystemPanel); ok && fsp != nil && fsp.Vfs != nil {
						if _, isAI := fsp.Vfs.(*aiVFSWrapper); isAI {
							aiIdx = idx
							break
						}
					}
				}
				if aiIdx != -1 {
					fm.SwitchScreen(i)
					aiPf = screenPf
					break
				}
			}
		}
	}

	if aiPf == nil && pf != nil {
		// Fork current panel.PanelsFrame into a new workspace
		fm.EmitCommand(vtui.CmResize, "fork")
		if topPf, ok := fm.GetTopFrame().(*panel.PanelsFrame); ok {
			aiPf = topPf
			idx := 1 - aiPf.ActiveIdx
			fsp := aiPf.Panels[idx].(*panel.FileSystemPanel)
			panel.AIPrevPath[idx] = fsp.Vfs.GetPath()
			vtvibeConfig()
			aiPf.SwitchToVFS(fsp, &aiVFSWrapper{vtvibe.NewVFS(aiSession())})
		}
	}

	if aiPf == nil && pf != nil {
		// Fork current panel.PanelsFrame into a new workspace
		fm.EmitCommand(vtui.CmResize, "fork")
		if topPf, ok := fm.GetTopFrame().(*panel.PanelsFrame); ok {
			aiPf = topPf
			idx := 1 - aiPf.ActiveIdx
			fsp := aiPf.Panels[idx].(*panel.FileSystemPanel)
			panel.AIPrevPath[idx] = fsp.Vfs.GetPath()
			vtvibeConfig()
			aiPf.SwitchToVFS(fsp, &aiVFSWrapper{vtvibe.NewVFS(aiSession())})
			aiPf.SetWidePanel(idx)
		}
	} else if aiPf != nil {
		// Ensure the AI panel is active and wide in the existing workspace
		aiIdx := -1
		for idx, p := range aiPf.Panels {
			if fsp, ok := p.(*panel.FileSystemPanel); ok && fsp != nil && fsp.Vfs != nil {
				if _, isAI := fsp.Vfs.(*aiVFSWrapper); isAI {
					aiIdx = idx
					break
				}
			}
		}
		if aiIdx != -1 {
			aiPf.ActiveIdx = aiIdx
		}
	}

	// 3. Set up the chat, inject context, and start a new session
	if aiPf != nil {
		AiSetViewModePanel(aiPf, aiPf.ActiveIdx, "ai://chat", true)
		aiSession().Reset(true) // Keep files in ctx/, clear chat history

		if aiPf.AltPanels[aiPf.ActiveIdx] != nil {
			if cp, ok := aiPf.AltPanels[aiPf.ActiveIdx].(*AIChatPanel); ok {
				prompt := ""
				if len(ctxParts) > 0 {
					prompt = "[" + strings.Join(ctxParts, ", ") + "]\n"
				}
				cp.Input.SetText(prompt)
				lines := len(strings.Split(prompt, "\n"))
				if lines > 0 {
					cp.Input.SetCursorPos(lines-1, 0)
				}
				cp.ScrollToBottom()
			}
		}
		return true
	}
	return false
}

// aiSetupDialog is the whole first-run wizard at MVP scale: paste a key, name
// a model, done. Both steps may be skipped with an empty answer.
func aiSetupDialog(pf *panel.PanelsFrame) {
	cfg, _ := vtvibeConfig()
	vtui.InputBox(i18n.Msg("AI.Title"), i18n.Msg("AI.KeyPrompt"), "", func(key string) {
		if key = strings.TrimSpace(key); key != "" {
			if err := vtvibeSaveSetting("key", key); err != nil {
				aiShowError(err)
				return
			}
		}
		vtui.InputBox(i18n.Msg("AI.Title"), i18n.Msg("AI.ModelPrompt"), cfg.Model, func(model string) {
			if model = strings.TrimSpace(model); model != "" {
				if err := vtvibeSaveSetting("model", model); err != nil {
					aiShowError(err)
					return
				}
			}
			vtvibeConfig()
			pf.RefreshAll()
			vtui.ShowMessage(i18n.Msg("AI.Title"), fmt.Sprintf(i18n.Msg("AI.Saved"), vtvibeIniPath()), []string{i18n.Msg("vtui.Ok")})
		})
	})
}

// aiCommand handles everything typed after "ai:" in the command line. Plain
// text is a question; the few reserved words are the settings the MVP needs.
func aiCommand(app vfs.App, arg string) {
	pf := panel.FindPanelsFrameAnyScreen()
	if pf == nil {
		return
	}
	arg = strings.TrimSpace(arg)
	lower := strings.ToLower(arg)

	switch {
	case arg == "":
		draft := aiSession().Draft()
		if draft == "" {
			vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.EmptyDraft"), []string{i18n.Msg("vtui.Ok")})
			return
		}
		aiSend(pf, draft)
		aiSession().ClearDraft()
	case lower == "help" || lower == "?":
		vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.Help"), []string{i18n.Msg("vtui.Ok")})
	case lower == "new":
		aiNewSession(pf)
	case lower == "apply" || lower == "patch":
		aiApplyPatch(pf)
	case lower == "undo":
		aiUndoPatch(pf)
	case lower == "ap" || lower == "spec":
		aiAttachAPSpec(pf)
	case lower == "key":
		aiSetupDialog(pf)
	case lower == "task" || strings.HasPrefix(lower, "task "):
		aiTaskCommand(pf, arg[len("task"):])
	case lower == "orders":
		aiOrdersCommand(pf, "orders", "")
	case strings.HasPrefix(lower, "done "):
		aiOrdersCommand(pf, "done", arg[len("done "):])
	case strings.HasPrefix(lower, "undone "):
		aiOrdersCommand(pf, "undone", arg[len("undone "):])
	case lower == "dialogs":
		aiDialogsMenu(pf)
	case strings.HasPrefix(arg, "/") && !strings.HasPrefix(arg, "//"):
		aiUserCommand(pf, arg[1:])
	case lower == "allow":
		aiAllowCommand(pf)
	case lower == "mcp":
		aiMCPCommand()
	case lower == "cost":
		aiCostCommand(pf)
	case lower == "gates" || strings.HasPrefix(lower, "gates "):
		aiGatesCommand(pf, arg[len("gates"):])
	case lower == "token" || strings.HasPrefix(lower, "token "):
		aiTokenCommand(pf, arg[len("token"):])
	case lower == "mode" || strings.HasPrefix(lower, "mode "):
		aiModeCommand(pf, arg[len("mode"):])
	case lower == "bot" || strings.HasPrefix(lower, "bot "):
		aiBotCommand(pf, arg[len("bot"):])
	case lower == "models":
		aiListModels(pf)
	case lower == "model":
		aiSetupDialog(pf)
	case strings.HasPrefix(lower, "model "):
		name := strings.TrimSpace(arg[len("model "):])
		if err := vtvibeSaveSetting("model", name); err != nil {
			aiShowError(err)
			return
		}
		vtvibeConfig()
		pf.RefreshAll()
		vtui.ShowMessage(i18n.Msg("AI.Title"), fmt.Sprintf(i18n.Msg("AI.Saved"), vtvibeIniPath()), []string{i18n.Msg("vtui.Ok")})
	default:
		aiSend(pf, arg)
	}
}

// aiSend runs the round trip through the background task manager: the UI
// thread never blocks and Cancel actually cancels the HTTP request.
func aiSend(pf *panel.PanelsFrame, question string) {
	cfg, keySource, provider := vtvibeProviderConfig()
	if cfg.APIKey == "" && keySource == "" && provider.NeedsKey(cfg.BaseURL) {
		vtui.FrameManager.PostTask(func() {
			if provider.ID != vtvibe.ProviderGemini {
				// The prompt offers a free Google AI Studio key; for any other
				// service only the setup dialog makes sense.
				aiSetupDialog(pf)
				return
			}
			dlg := vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.NoKeyBrowserPrompt"), []string{i18n.Msg("AI.BtnGetToken"), i18n.Msg("vtui.Cancel")})
			dlg.OnResult = func(code int) {
				if code == 0 {
					openBrowser(provider.KeyURL)
					aiSetupDialog(pf)
				}
			}
		})
		return
	}
	if aiSession().Busy() {
		vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.Busy"), []string{i18n.Msg("vtui.Ok")})
		return
	}

	session := aiSession()
	aiRunWork(pf, session, func(ctx context.Context) (vtvibe.WorkEnd, error) {
		return session.Work(ctx, cfg, question, vtvibeNonstopDefault())
	})
}

// aiRunWork runs one piece of the dialog's work (a message of the user, or
// going on after the workers reported) behind the progress dialog; the tasks
// the model handed to workers on the way are offered to the user afterwards.
func aiRunWork(pf *panel.PanelsFrame, session *vtvibe.Session, work func(ctx context.Context) (vtvibe.WorkEnd, error)) {
	// The manager may hand tasks to workers (f4#1842, stage H5).
	session.SetDelegation(true)
	session.SetOnUpdate(aiStreamRedraw(vtui.FrameManager, pf))
	pf.RunProgressTask(i18n.Msg("AI.Title"), i18n.Msg("AI.Sending"), false,
		func(ctx context.Context, update func(msg string, percent int)) error {
			end, err := work(ctx)
			if err == nil && end == vtvibe.WorkRoundLimit {
				session.Note("assistant", fmt.Sprintf(i18n.Msg("AI.NonstopRoundLimit"), vtvibe.MaxNonstopRounds))
			}
			return err
		},
		func(err error) {
			delegations := session.TakeDelegations()
			if err != nil {
				if err == context.Canceled {
					return
				}
				aiShowError(err)
				return
			}
			pf.RefreshAll()
			if pf.AltPanels[pf.ActiveIdx] != nil && pf.AltPanels[pf.ActiveIdx].Kind() == "ai_chat" {
				if cp, ok := pf.AltPanels[pf.ActiveIdx].(*AIChatPanel); ok {
					cp.ScrollToBottom()
				}
				vtui.FrameManager.Redraw()
			} else if path := aiLastAnswerPath(session); path != "" {
				actionOpenViewer(pf, vtvibe.NewVFS(session), path)
			}
			if len(delegations) > 0 {
				aiDelegate(pf, session, delegations)
			}
		})
}

// aiStreamRedraw shows a streamed answer as it grows (f4#1842, stage H2).
// Pieces arrive many times a second from the request's goroutine; the chat
// is redrawn at most every aiStreamRedrawEvery, and the completion handler
// draws the final state, so a skipped last piece is never lost.
func aiStreamRedraw(manager interface{ PostTask(func()) }, pf *panel.PanelsFrame) func() {
	var last atomic.Int64
	return func() {
		now := time.Now().UnixNano()
		if prev := last.Load(); now-prev < int64(aiStreamRedrawEvery) || !last.CompareAndSwap(prev, now) {
			return
		}
		manager.PostTask(func() {
			if pf.AltPanels[pf.ActiveIdx] != nil {
				if cp, ok := pf.AltPanels[pf.ActiveIdx].(*AIChatPanel); ok {
					cp.ScrollToBottom()
				}
			}
			if vtui.FrameManager != nil {
				vtui.FrameManager.Redraw()
			}
		})
	}
}

var aiStreamRedrawEvery = 80 * time.Millisecond

// aiLastAnswerPath is the chat file the reply was just written to.
func aiLastAnswerPath(s *vtvibe.Session) string {
	n := len(s.Turns())
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("/chat/%04d-model.md", n)
}

func aiListModels(pf *panel.PanelsFrame) {
	cfg, _ := vtvibeConfig()
	var models []vtvibe.ModelInfo
	pf.RunProgressTask(i18n.Msg("AI.Title"), i18n.Msg("AI.Sending"), false,
		func(ctx context.Context, update func(msg string, percent int)) error {
			list, err := cfg.ModelsWithInfo(ctx)
			models = list
			return err
		},
		func(err error) {
			if err != nil {
				if err != context.Canceled {
					aiShowError(err)
				}
				return
			}
			if len(models) == 0 {
				vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.NoModels"), []string{i18n.Msg("vtui.Ok")})
				return
			}
			aiModelsMenu(pf, models, cfg.Model)
		})
}

// aiModelsMenu lists the models, free ones first, the current one marked;
// Enter switches the dialog to the chosen one (f4#1842, stage H9: the other
// harnesses pick the model from a menu in the chat).
func aiModelsMenu(pf *panel.PanelsFrame, models []vtvibe.ModelInfo, current string) {
	ordered := aiModelsInOrder(models)
	lines := aiModelLines(ordered, len(ordered))
	menu := vtui.NewVMenu(i18n.Msg("AI.ModelsTitle"))
	width := vtui.StringWidth(i18n.Msg("AI.ModelsTitle")) + 6
	for i, line := range lines {
		mark := "  "
		if ordered[i].ID == current {
			mark = "* "
		}
		width = max(width, vtui.StringWidth(line)+8)
		menu.AddItem(vtui.MenuItem{Text: dialog.EscapeAmpersand(mark + line)})
	}
	menu.OnAction = func(idx int) {
		menu.Close()
		if idx < 0 || idx >= len(ordered) || ordered[idx].ID == current {
			return
		}
		if err := vtvibeSaveSetting("model", ordered[idx].ID); err != nil {
			aiShowError(err)
			return
		}
		vtvibeConfig()
		aiSession().Note("assistant", fmt.Sprintf(i18n.Msg("AI.ModelSwitched"), ordered[idx].ID))
		aiBotRefresh(pf)
	}
	aiShowMenu(menu, width, len(ordered))
}

// aiModelsInOrder puts the free models first, keeping the service's order
// within each group, as aiModelLines prints them.
func aiModelsInOrder(models []vtvibe.ModelInfo) []vtvibe.ModelInfo {
	out := make([]vtvibe.ModelInfo, 0, len(models))
	for _, free := range []bool{true, false} {
		for _, m := range models {
			if m.Free == free {
				out = append(out, m)
			}
		}
	}
	return out
}

// aiModelLines lists free models first, marked, so they stay in sight when
// a service such as OpenRouter offers hundreds and the list is cut (f4#1842).
func aiModelLines(models []vtvibe.ModelInfo, limit int) []string {
	lines := make([]string, 0, len(models))
	for _, free := range []bool{true, false} {
		for _, m := range models {
			if m.Free != free {
				continue
			}
			if free {
				lines = append(lines, fmt.Sprintf(i18n.Msg("AI.ModelFree"), m.ID))
			} else {
				lines = append(lines, m.ID)
			}
		}
	}
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return lines
}

func aiShowError(err error) {
	msg := err.Error()
	switch {
	case err == vtvibe.ErrNoKey:
		msg = i18n.Msg("AI.NoKey")
	case errors.Is(err, vtvibe.ErrNoModel):
		msg = i18n.Msg("AI.NoModel")
	case vtvibe.HTTPStatus(err) == http.StatusNotFound:
		// A service answers 404 for a model it does not have (f4#1842,
		// bothub: MODEL_NOT_FOUND); the error alone does not say which
		// model was asked for.
		if cfg, _, _ := vtvibeReadConfig(); cfg.Model != "" {
			msg += "\n\n" + fmt.Sprintf(i18n.Msg("AI.ModelNotFoundHint"), cfg.Model)
		}
	}
	if len(msg) > 600 {
		msg = msg[:600] + "..."
	}
	vtui.ShowMessage(i18n.Msg("AI.ErrorTitle"), msg, []string{i18n.Msg("vtui.Ok")})
}
func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	}
	if err != nil {
		vtui.DebugLog("VTVIBE: failed to open browser: %v", err)
	}
}

// aiSetViewMode fills panel.AISetViewMode: it finds which side the panel is
// on, because AiSetViewModePanel addresses a panel by index.
func aiSetViewMode(fsp *panel.FileSystemPanel, path string, isChat bool) {
	pf := panel.FindPanelsFrameAnyScreen()
	if pf != nil {
		idx := -1
		if pf.Panels[0] == fsp {
			idx = 0
		}
		if pf.Panels[1] == fsp {
			idx = 1
		}
		if idx != -1 {
			AiSetViewModePanel(pf, idx, path, isChat)
		}
	}
}
