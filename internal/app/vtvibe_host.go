package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/unxed/f4/internal/panel"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
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

const (
	vtvibeIniName        = "vtvibe.ini"
	vtvibeDefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai"
	vtvibeDefaultModel   = vtvibe.DefaultModel
)

var (
	vtvibeOnce    sync.Once
	vtvibeSession *vtvibe.Session
)

// aiSession returns the single dialog shared by every ai:// mount.
func aiSession() *vtvibe.Session {
	vtvibeOnce.Do(func() { vtvibeSession = vtvibe.NewSession() })
	return vtvibeSession
}

func vtvibeIniPath() string {
	return filepath.Join(config.GetF4ConfigDir(), vtvibeIniName)
}

// vtvibeConfig re-reads the settings on every use, so editing vtvibe.ini or
// exporting a key does not need a restart.
func vtvibeConfig() (vtvibe.Config, string) {
	ini := ini.Load(vtvibeIniPath())
	cfg := vtvibe.Config{
		BaseURL: ini.GetString("general", "base_url", vtvibeDefaultBaseURL),
		Model:   ini.GetString("general", "model", vtvibeDefaultModel),
	}
	keySource := ""
	for _, name := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_API_KEY"} {
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
	aiSession().SetStatus(vtvibe.Status{BaseURL: cfg.BaseURL, Model: cfg.Model, KeySource: keySource})
	return cfg, keySource
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
				cp.input.SetText(prompt)
				lines := len(strings.Split(prompt, "\n"))
				if lines > 0 {
					cp.input.SetCursorPos(lines-1, 0)
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
	case lower == "ap" || lower == "spec":
		aiAttachAPSpec(pf)
	case lower == "key":
		aiSetupDialog(pf)
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
	cfg, keySource := vtvibeConfig()
	if cfg.APIKey == "" && keySource == "" && !strings.Contains(cfg.BaseURL, "127.0.0.1") &&
		!strings.Contains(cfg.BaseURL, "localhost") {
		vtui.FrameManager.PostTask(func() {
			dlg := vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.NoKeyBrowserPrompt"), []string{i18n.Msg("AI.BtnGetToken"), i18n.Msg("vtui.Cancel")})
			dlg.OnResult = func(code int) {
				if code == 0 {
					openBrowser("https://aistudio.google.com/apikey")
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
	pf.RunProgressTask(i18n.Msg("AI.Title"), i18n.Msg("AI.Sending"), false,
		func(ctx context.Context, update func(msg string, percent int)) error {
			return session.Ask(ctx, cfg, question)
		},
		func(err error) {
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
		})
}

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
	var models []string
	pf.RunProgressTask(i18n.Msg("AI.Title"), i18n.Msg("AI.Sending"), false,
		func(ctx context.Context, update func(msg string, percent int)) error {
			list, err := cfg.Models(ctx)
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
			if len(models) > 40 {
				models = models[:40]
			}
			vtui.ShowMessage(i18n.Msg("AI.Title"), strings.Join(models, "\n"), []string{i18n.Msg("vtui.Ok")})
		})
}

func aiShowError(err error) {
	msg := err.Error()
	if err == vtvibe.ErrNoKey {
		msg = i18n.Msg("AI.NoKey")
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
