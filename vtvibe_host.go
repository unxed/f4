package main

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

	"github.com/unxed/f4/vfs"
	"github.com/unxed/f4/vtvibe"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// vtvibe is the AI panel of f4. This file is the only place where it touches
// the core: it publishes the ai:// drive, the "ai:" command prefix and the
// registry actions. Everything else lives in the vtvibe package.

const (
	vtvibeIniName        = "vtvibe.ini"
	vtvibeDefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai"
	vtvibeDefaultModel   = "gemini-3.6-flash"
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
	return filepath.Join(GetF4ConfigDir(), vtvibeIniName)
}

// vtvibeConfig re-reads the settings on every use, so editing vtvibe.ini or
// exporting a key does not need a restart.
func vtvibeConfig() (vtvibe.Config, string) {
	ini := LoadIni(vtvibeIniPath())
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
	ini := LoadIni(path)
	if ini.data["general"] == nil {
		ini.data["general"] = map[string]string{}
	}
	ini.data["general"][key] = value

	keys := make([]string, 0, len(ini.data["general"]))
	for k := range ini.data["general"] {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString("[general]\n")
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s = %s\n", k, ini.data["general"][k])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	// 0600: the file may hold an API key.
	return os.WriteFile(path, []byte(sb.String()), 0600)
}

func init() {
	RegisterDrive("AI", func() vfs.VFS { return &aiVFSWrapper{vtvibe.NewVFS(aiSession())} })

	if _, err := (&coreAPI{}).RegisterCommandPrefix("vtvibe", "ai", aiCommand); err != nil {
		vtui.DebugLog("VTVIBE: cannot register the ai: prefix: %v", err)
	}

	withAI := func(fn func(pf *PanelsFrame)) func() bool {
		return func() bool {
			if pf := findPanelsFrameAnyScreen(); pf != nil {
				fn(pf)
				return true
			}
			return false
		}
	}

	RegisterAction(Action{
		Name:        "AI.TogglePanel",
		Area:        "Shell",
		Label:       "AI Panel",
		LabelKey:    "Action.AI.TogglePanel",
		Description: "Open or close the AI panel on the passive panel",
		DescKey:     "Action.AI.TogglePanel.Desc",
		DefaultKeys: []string{"RCtrlA"},
		MenuPath:    "Commands",
		Handler:     withAI(func(pf *PanelsFrame) { aiTogglePanel(pf) }),
	})
	RegisterAction(Action{
		Name:        "AI.Ask",
		Area:        "Common",
		Label:       "Ask the AI",
		LabelKey:    "Action.AI.Ask",
		Description: "Type a question and send it together with the ai:// context",
		DescKey:     "Action.AI.Ask.Desc",
		MenuPath:    "Commands",
		Handler:     func() bool { return aiAskAction() },
	})
	RegisterAction(Action{
		Name:        "AI.NewSession",
		Area:        "Shell",
		Label:       "New AI Dialog",
		LabelKey:    "Action.AI.NewSession",
		Description: "Clear the AI dialog history and artifacts, keeping the context files",
		DescKey:     "Action.AI.NewSession.Desc",
		MenuPath:    "Commands",
		Visible:     func() bool { return isAIPanelActive() },
		Handler:     withAI(func(pf *PanelsFrame) { aiNewSession(pf) }),
	})
	RegisterAction(Action{
		Name:        "AI.Setup",
		Area:        "Shell",
		Label:       "AI Setup",
		LabelKey:    "Action.AI.Setup",
		Description: "Set the API key and the model used by the AI panel",
		DescKey:     "Action.AI.Setup.Desc",
		MenuPath:    "Options",
		Handler:     withAI(func(pf *PanelsFrame) { aiSetupDialog(pf) }),
	})
}

// aiPrevPath remembers where a panel was before it showed the dialog, so the
// same key brings the files back.
var aiPrevPath [2]string

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
		pf, ok := app.(*PanelsFrame)
		if !ok {
			return false
		}

		idx := -1
		for i, p := range pf.panels {
			if fsp, isFsp := p.(*FileSystemPanel); isFsp && fsp.vfs == w {
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

func aiTogglePanel(pf *PanelsFrame) {
	// If any panel is AI, close it entirely
	for i, p := range pf.panels {
		if fsp, ok := p.(*FileSystemPanel); ok {
			if _, isAI := fsp.vfs.(*aiVFSWrapper); isAI {
				pf.exitWide()
				if pf.altPanels[i] != nil && pf.altPanels[i].Kind() == "ai_chat" {
					if c, ok := pf.altPanels[i].(interface{ Close() }); ok {
						c.Close()
					}
					pf.altPanels[i] = nil
				}
				target := aiPrevPath[i]
				if target == "" {
					target, _ = os.UserHomeDir()
				}
				pf.switchToVFS(fsp, vfs.NewOSVFS(target))
				pf.activeIdx = 1 - i
				return
			}
		}
	}

	idx := 1 - pf.activeIdx
	fsp := pf.panels[idx].(*FileSystemPanel)
	aiPrevPath[idx] = fsp.vfs.GetPath()
	vtvibeConfig()
	pf.switchToVFS(fsp, &aiVFSWrapper{vtvibe.NewVFS(aiSession())})

	AiSetViewModePanel(pf, idx, "ai://chat", true)
}

func AiSetViewModePanel(pf *PanelsFrame, idx int, path string, isChat bool) {
	fsp := pf.panels[idx].(*FileSystemPanel)
	if _, isAI := fsp.vfs.(*aiVFSWrapper); !isAI {
		return
	}

	currentAlt := pf.altPanels[idx]

	if isChat {
		if currentAlt == nil || currentAlt.Kind() != "ai_chat" {
			if currentAlt != nil {
				if c, ok := currentAlt.(interface{ Close() }); ok {
					c.Close()
				}
			}
			chatAlt := NewAIChatPanel(fsp)
			pf.altPanels[idx] = chatAlt
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
			pf.altPanels[idx] = nil
		}
		if path != "" {
			pf.NavigateToPath(fsp, path)
		}
	}
	pf.ResizeConsole(pf.lastW, pf.lastH)
	vtui.FrameManager.HardRefresh()
}

// Support for reflection cast from commands.go
func (fsp *FileSystemPanel) AiSetViewMode(path string, isChat bool) {
	pf := findPanelsFrameAnyScreen()
	if pf != nil {
		idx := -1
		if pf.panels[0] == fsp {
			idx = 0
		}
		if pf.panels[1] == fsp {
			idx = 1
		}
		if idx != -1 {
			AiSetViewModePanel(pf, idx, path, isChat)
		}
	}
}

func aiNewSession(pf *PanelsFrame) {
	aiSession().Reset(true)
	vtvibeConfig()
	pf.RefreshAll()
	vtui.ShowMessage(Msg("AI.Title"), Msg("AI.NewSessionDone"), []string{Msg("vtui.Ok")})
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
	case *EditorView:
		ctxParts = append(ctxParts, "Editor: "+f.vfs.Base(f.filePath))
		ctxParts = append(ctxParts, fmt.Sprintf("Line: %d", f.CursorLine+1))
		if f.selActive || f.rectSelActive {
			ctxParts = append(ctxParts, "[Text is selected]")
		}
	case *ViewerView:
		ctxParts = append(ctxParts, "Viewer: "+f.vfs.Base(f.path))
		ctxParts = append(ctxParts, fmt.Sprintf("Offset: %d", f.TopOffset))
	}

	pf := findPanelsFrameAnyScreen()
	if pf != nil {
		fsp := pf.getActivePanel()
		if fsp != nil {
			_, isAI := fsp.vfs.(*aiVFSWrapper)
			if !isAI {
				ctxParts = append(ctxParts, "Path: "+fsp.vfs.GetPath())
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
	var aiPf *PanelsFrame
	for i, s := range fm.Screens {
		if len(s.Frames) > 0 {
			if screenPf, ok := s.Frames[len(s.Frames)-1].(*PanelsFrame); ok {
				aiIdx := -1
				for idx, p := range screenPf.panels {
					if fsp, ok := p.(*FileSystemPanel); ok && fsp != nil && fsp.vfs != nil {
						if _, isAI := fsp.vfs.(*aiVFSWrapper); isAI {
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
		// Fork current PanelsFrame into a new workspace
		fm.EmitCommand(vtui.CmResize, "fork")
		if topPf, ok := fm.GetTopFrame().(*PanelsFrame); ok {
			aiPf = topPf
			idx := 1 - aiPf.activeIdx
			fsp := aiPf.panels[idx].(*FileSystemPanel)
			aiPrevPath[idx] = fsp.vfs.GetPath()
			vtvibeConfig()
			aiPf.switchToVFS(fsp, &aiVFSWrapper{vtvibe.NewVFS(aiSession())})
		}
	}

	if aiPf == nil && pf != nil {
		// Fork current PanelsFrame into a new workspace
		fm.EmitCommand(vtui.CmResize, "fork")
		if topPf, ok := fm.GetTopFrame().(*PanelsFrame); ok {
			aiPf = topPf
			idx := 1 - aiPf.activeIdx
			fsp := aiPf.panels[idx].(*FileSystemPanel)
			aiPrevPath[idx] = fsp.vfs.GetPath()
			vtvibeConfig()
			aiPf.switchToVFS(fsp, &aiVFSWrapper{vtvibe.NewVFS(aiSession())})
			aiPf.setWidePanel(idx)
		}
	} else if aiPf != nil {
		// Ensure the AI panel is active and wide in the existing workspace
		aiIdx := -1
		for idx, p := range aiPf.panels {
			if fsp, ok := p.(*FileSystemPanel); ok && fsp != nil && fsp.vfs != nil {
				if _, isAI := fsp.vfs.(*aiVFSWrapper); isAI {
					aiIdx = idx
					break
				}
			}
		}
		if aiIdx != -1 {
			aiPf.activeIdx = aiIdx
		}
	}

	// 3. Set up the chat, inject context, and start a new session
	if aiPf != nil {
		AiSetViewModePanel(aiPf, aiPf.activeIdx, "ai://chat", true)
		aiSession().Reset(true) // Keep files in ctx/, clear chat history

		if aiPf.altPanels[aiPf.activeIdx] != nil {
			if cp, ok := aiPf.altPanels[aiPf.activeIdx].(*AIChatPanel); ok {
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
func aiSetupDialog(pf *PanelsFrame) {
	cfg, _ := vtvibeConfig()
	vtui.InputBox(Msg("AI.Title"), Msg("AI.KeyPrompt"), "", func(key string) {
		if key = strings.TrimSpace(key); key != "" {
			if err := vtvibeSaveSetting("key", key); err != nil {
				aiShowError(err)
				return
			}
		}
		vtui.InputBox(Msg("AI.Title"), Msg("AI.ModelPrompt"), cfg.Model, func(model string) {
			if model = strings.TrimSpace(model); model != "" {
				if err := vtvibeSaveSetting("model", model); err != nil {
					aiShowError(err)
					return
				}
			}
			vtvibeConfig()
			pf.RefreshAll()
			vtui.ShowMessage(Msg("AI.Title"), fmt.Sprintf(Msg("AI.Saved"), vtvibeIniPath()), []string{Msg("vtui.Ok")})
		})
	})
}

// aiCommand handles everything typed after "ai:" in the command line. Plain
// text is a question; the few reserved words are the settings the MVP needs.
func aiCommand(app vfs.App, arg string) {
	pf := findPanelsFrameAnyScreen()
	if pf == nil {
		return
	}
	arg = strings.TrimSpace(arg)
	lower := strings.ToLower(arg)

	switch {
	case arg == "":
		draft := aiSession().Draft()
		if draft == "" {
			vtui.ShowMessage(Msg("AI.Title"), Msg("AI.EmptyDraft"), []string{Msg("vtui.Ok")})
			return
		}
		aiSend(pf, draft)
		aiSession().ClearDraft()
	case lower == "help" || lower == "?":
		vtui.ShowMessage(Msg("AI.Title"), Msg("AI.Help"), []string{Msg("vtui.Ok")})
	case lower == "new":
		aiNewSession(pf)
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
		vtui.ShowMessage(Msg("AI.Title"), fmt.Sprintf(Msg("AI.Saved"), vtvibeIniPath()), []string{Msg("vtui.Ok")})
	default:
		aiSend(pf, arg)
	}
}

// aiSend runs the round trip through the background task manager: the UI
// thread never blocks and Cancel actually cancels the HTTP request.
func aiSend(pf *PanelsFrame, question string) {
	cfg, keySource := vtvibeConfig()
	if cfg.APIKey == "" && keySource == "" && !strings.Contains(cfg.BaseURL, "127.0.0.1") &&
		!strings.Contains(cfg.BaseURL, "localhost") {
		vtui.FrameManager.PostTask(func() {
			dlg := vtui.ShowMessage(Msg("AI.Title"), Msg("AI.NoKeyBrowserPrompt"), []string{Msg("AI.BtnGetToken"), Msg("vtui.Cancel")})
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
		vtui.ShowMessage(Msg("AI.Title"), Msg("AI.Busy"), []string{Msg("vtui.Ok")})
		return
	}

	session := aiSession()
	pf.RunProgressTask(Msg("AI.Title"), Msg("AI.Sending"), false,
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
			if pf.altPanels[pf.activeIdx] != nil && pf.altPanels[pf.activeIdx].Kind() == "ai_chat" {
				if cp, ok := pf.altPanels[pf.activeIdx].(*AIChatPanel); ok {
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

func aiListModels(pf *PanelsFrame) {
	cfg, _ := vtvibeConfig()
	var models []string
	pf.RunProgressTask(Msg("AI.Title"), Msg("AI.Sending"), false,
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
				vtui.ShowMessage(Msg("AI.Title"), Msg("AI.NoModels"), []string{Msg("vtui.Ok")})
				return
			}
			if len(models) > 40 {
				models = models[:40]
			}
			vtui.ShowMessage(Msg("AI.Title"), strings.Join(models, "\n"), []string{Msg("vtui.Ok")})
		})
}

func aiShowError(err error) {
	msg := err.Error()
	if err == vtvibe.ErrNoKey {
		msg = Msg("AI.NoKey")
	}
	if len(msg) > 600 {
		msg = msg[:600] + "..."
	}
	vtui.ShowMessage(Msg("AI.ErrorTitle"), msg, []string{Msg("vtui.Ok")})
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
