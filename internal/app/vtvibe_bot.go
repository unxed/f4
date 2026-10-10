package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/vtvibe"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// ai:bot — the simplified bot mode of unxed/f4#1842 (docs/VTVIBE.md § 19a,
// step B2). One bot per f4 process; its rounds are logged into the AI chat.

var aiBot vtvibe.Bot

// aiBotCommand handles "ai:bot", "ai:bot stop" and "ai:bot <source> [pause]".
func aiBotCommand(pf *panel.PanelsFrame, arg string) {
	// The bot and the stop request run in the background; they post back to
	// the manager this command was given on, never the global read later.
	manager := vtui.FrameManager
	arg = strings.TrimSpace(arg)
	switch strings.ToLower(arg) {
	case "":
		vtui.ShowMessage(i18n.Msg("AI.Title"), aiBotStatusText(aiBot.Status()), []string{i18n.Msg("vtui.Ok")})
		return
	case "stop":
		go func() {
			stopped := aiBot.Stop()
			aiBotMCP.finish() // a round stopped halfway leaves its servers running
			manager.PostTask(func() {
				if stopped {
					aiSession().Note("assistant", i18n.Msg("AI.BotStopped"))
					aiBotRefresh(pf)
				}
				vtui.ShowMessage(i18n.Msg("AI.Title"), aiBotStatusText(aiBot.Status()), []string{i18n.Msg("vtui.Ok")})
			})
		}()
		return
	}
	source, pause, whole := parseBotArgs(arg)
	if aiBot.Status().Running {
		vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.BotAlready"), []string{i18n.Msg("vtui.Ok")})
		return
	}
	cfg, keySource, provider := vtvibeProviderConfig()
	if cfg.APIKey == "" && keySource == "" && provider.NeedsKey(cfg.BaseURL) {
		aiShowError(vtvibe.ErrNoKey)
		return
	}
	dir := aiBotDir(pf)
	question := fmt.Sprintf(i18n.Msg("AI.BotConfirm"), source, pause, dir, cfg.Model)
	dlg := vtui.ShowMessage(i18n.Msg("AI.Title"), question, []string{i18n.Msg("AI.BotStart"), i18n.Msg("vtui.Cancel")})
	dlg.OnResult = func(code int) {
		if code != 0 {
			return
		}
		config := aiAgentConfig(aiSession())
		// Rounds go step by step in clean contexts unless "whole" was asked
		// for (f4#1842, stage H7).
		aiBot.SetStepped(!whole)
		aiBot.SetToolWrapper(func(tools []vtvibe.Tool) []vtvibe.Tool {
			return aiWithApproval(manager, func() string { return i18n.Msg("AI.BotLabel") }, tools)
		})
		err := aiBot.Start(source, pause, dir, config,
			func() []vtvibe.Tool {
				// Each round gets the MCP servers of ai/mcp.json afresh,
				// started in the bot's folder (f4#1842, stage H9).
				return append(vtvibe.DialogTools(vtvibeDialogControls(manager, pf)), aiBotMCP.begin(dir)...)
			},
			func(n int) {
				manager.PostTask(func() {
					aiSession().Note("assistant", fmt.Sprintf(i18n.Msg("AI.BotRoundStart"), n, source))
					aiBotRefresh(pf)
				})
			},
			func(r vtvibe.BotRound) {
				text := aiBotRoundText(r)
				if problems := aiBotMCP.finish(); problems != "" {
					text += "\n\n" + problems
				}
				model := config().Model
				manager.PostTask(func() {
					// The bot's spending counts for the dialog (f4#1842, H9).
					aiSession().AddSpent(model, r.Usage)
					aiSession().Note("assistant", text)
					aiBotRefresh(pf)
				})
			})
		if err != nil {
			aiShowError(err)
		}
	}
}

// parseBotArgs splits "source [pause] [whole]": a last word "whole" runs
// each round in one context instead of step by step; a last word that parses
// as a Go duration (30m, 1h, 90s) is the pause.
func parseBotArgs(arg string) (string, time.Duration, bool) {
	fields := strings.Fields(arg)
	whole := false
	if n := len(fields); n > 1 && strings.EqualFold(fields[n-1], "whole") {
		whole, fields = true, fields[:n-1]
	}
	if len(fields) > 1 {
		if d, err := time.ParseDuration(fields[len(fields)-1]); err == nil && d > 0 {
			return strings.Join(fields[:len(fields)-1], " "), d, whole
		}
	}
	return strings.Join(fields, " "), vtvibe.DefaultBotPause, whole
}

// aiBotDir is where the bot's commands start: the active panel's folder when
// it is on the local disk, the home folder otherwise.
func aiBotDir(pf *panel.PanelsFrame) string {
	if fsp := pf.GetActivePanel(); fsp != nil {
		if _, local := fsp.Vfs.(*vfs.OSVFS); local {
			return fsp.Vfs.GetPath()
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}

func aiBotRoundText(r vtvibe.BotRound) string {
	var sb strings.Builder
	if r.Err != nil {
		fmt.Fprintf(&sb, i18n.Msg("AI.BotRoundFailed"), r.N, r.Err)
	} else {
		fmt.Fprintf(&sb, i18n.Msg("AI.BotRoundDone"), r.N, len(r.Steps), r.Usage.In, r.Usage.Out)
		sb.WriteString("\n\n")
		sb.WriteString(r.Report)
	}
	sb.WriteString("\n\n")
	fmt.Fprintf(&sb, i18n.Msg("AI.BotNext"), r.Next.Format("15:04"))
	return sb.String()
}

func aiBotStatusText(st vtvibe.BotStatus) string {
	if !st.Running {
		return i18n.Msg("AI.BotIdle")
	}
	return fmt.Sprintf(i18n.Msg("AI.BotRunning"), st.Source, st.Pause, st.Rounds)
}

func aiBotRefresh(pf *panel.PanelsFrame) {
	pf.RefreshAll()
	if pf.AltPanels[pf.ActiveIdx] != nil {
		if cp, ok := pf.AltPanels[pf.ActiveIdx].(*AIChatPanel); ok {
			cp.ScrollToBottom()
		}
	}
	vtui.FrameManager.Redraw()
}

// vtvibeDialogControls gives the model its handles on the dialog, each only
// while Settings → AI allows it (f4#1842, step B3). Called from the bot's
// goroutine; the UI is touched only through manager.
func vtvibeDialogControls(manager interface{ PostTask(func()) }, pf *panel.PanelsFrame) vtvibe.DialogControls {
	var c vtvibe.DialogControls
	if vtvibeAllowed("allow_model_switch") {
		c.SetModel = func(model string) error {
			if err := vtvibeSaveSetting("model", model); err != nil {
				return err
			}
			manager.PostTask(func() {
				vtvibeConfig()
				aiSession().Note("assistant", fmt.Sprintf(i18n.Msg("AI.ModelSwitched"), model))
				aiBotRefresh(pf)
			})
			return nil
		}
	}
	if vtvibeAllowed("allow_rename") {
		c.Rename = func(title string) error {
			aiSession().SetTitle(title)
			manager.PostTask(func() { aiBotRefresh(pf) })
			return nil
		}
	}
	return c
}

// vtvibeAllowed reads one of the model's permissions from vtvibe.ini; they
// are on unless the user switched them off.
func vtvibeAllowed(key string) bool {
	return ini.Load(vtvibeIniPath()).GetString("general", key, "true") != "false"
}

// aiAgentConfig is the configuration of the bot's and the workers' requests:
// the chat's, with the GitHub token for the commands they run (f4#1842,
// stage H6). It is read again for each round, so a token set meanwhile
// counts from the next one.
func aiAgentConfig(session *vtvibe.Session) func() vtvibe.Config {
	return func() vtvibe.Config {
		c, _ := vtvibeConfig()
		token, _ := aiGitHubToken(session)
		c.ToolEnv = vtvibe.GitHubEnv(token)
		return c
	}
}

// aiGitHubToken is the token the dialog's commands get and where it comes
// from: the dialog's own, else the one in Settings → AI; empty when neither
// is set, and the environment's GH_TOKEN, if any, stays as it is.
func aiGitHubToken(session *vtvibe.Session) (token, source string) {
	if t := session.GitHubToken(); t != "" {
		return t, i18n.Msg("AI.TokenFromDialog")
	}
	if t := aiSettingsGitHubToken(); t != "" {
		return t, i18n.Msg("AI.TokenFromSettings")
	}
	return "", ""
}

// aiSettingsGitHubToken is the token of Settings → AI alone.
func aiSettingsGitHubToken() string {
	return strings.TrimSpace(ini.Load(vtvibeIniPath()).GetString("general", "github_token", ""))
}

// aiTokenCommand is ai:token: it tells where the dialog's GitHub token comes
// from and asks for one bound to this dialog alone; ai:token clear unbinds it.
func aiTokenCommand(pf *panel.PanelsFrame, arg string) {
	session := aiSession()
	unbind := strings.EqualFold(strings.TrimSpace(arg), "clear")
	if unbind {
		session.SetGitHubToken("")
	}
	_, source := aiGitHubToken(session)
	if source == "" {
		source = i18n.Msg("AI.TokenNone")
	}
	if unbind {
		vtui.ShowMessage(i18n.Msg("AI.Title"), fmt.Sprintf(i18n.Msg("AI.TokenSource"), source), []string{i18n.Msg("vtui.Ok")})
		return
	}
	// The token is typed into a dialog box, not the command line, so it does
	// not land in the command history.
	vtui.InputBox(i18n.Msg("AI.Title"), fmt.Sprintf(i18n.Msg("AI.TokenPrompt"), source), "", func(token string) {
		if token = strings.TrimSpace(token); token == "" {
			return
		}
		session.SetGitHubToken(token)
		if err := session.StoreError(); err != nil {
			aiShowError(err)
			return
		}
		vtui.ShowMessage(i18n.Msg("AI.Title"), fmt.Sprintf(i18n.Msg("AI.TokenSource"), i18n.Msg("AI.TokenFromDialog")), []string{i18n.Msg("vtui.Ok")})
	})
}

// vtvibeGatesPath is the file of the user's gate rules (f4#1842, stage H8).
func vtvibeGatesPath() string {
	return filepath.Join(config.GetF4ConfigDir(), "ai", "gates.md")
}

// aiGateRules reads the user's gate rules; none when the file is missing.
func aiGateRules() string {
	data, err := os.ReadFile(vtvibeGatesPath()) // #nosec G304 -- f4's own file in its configuration folder
	if err != nil {
		return ""
	}
	return string(data)
}

// vtvibeLearnedGatesPath keeps the rules the worker manager formed from
// mistakes of workers (f4#1842, stage H8).
func vtvibeLearnedGatesPath() string {
	return filepath.Join(config.GetF4ConfigDir(), "ai", "gates_learned.md")
}

// aiLearnedRules reads the rules the manager formed.
func aiLearnedRules() string {
	data, err := os.ReadFile(vtvibeLearnedGatesPath()) // #nosec G304 -- f4's own file in its configuration folder
	if err != nil {
		return ""
	}
	return string(data)
}

// maxLearnedRules bounds the manager's rules; the oldest go first.
const maxLearnedRules = 50

var aiLearnMu sync.Mutex

// aiLearnRule keeps a rule the manager formed. Workers finish on their own
// goroutines, hence the lock.
func aiLearnRule(rule string) {
	aiLearnMu.Lock()
	defer aiLearnMu.Unlock()
	var lines []string
	for _, line := range strings.Split(aiLearnedRules(), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	lines = append(lines, "- "+strings.ReplaceAll(rule, "\n", " "))
	if len(lines) > maxLearnedRules {
		lines = lines[len(lines)-maxLearnedRules:]
	}
	_ = config.WriteUserFileAtomically(vtvibeLearnedGatesPath(), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// aiGatesCommand is ai:gates: it opens the user's rules in the editor,
// creating the file empty the first time; "ai:gates learned" opens the rules
// the worker manager formed from mistakes, to read, correct or delete. Every
// finished worker task is checked against both in a clean dialog and given
// back to the worker when it breaks one.
func aiGatesCommand(pf *panel.PanelsFrame, arg string) {
	path := vtvibeGatesPath()
	if strings.EqualFold(strings.TrimSpace(arg), "learned") {
		path = vtvibeLearnedGatesPath()
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := config.WriteUserFileAtomically(path, nil, 0o600); err != nil {
			aiShowError(err)
			return
		}
	}
	actionOpenEditor(pf, vfs.NewOSVFS(filepath.Dir(path)), path)
}

// aiCostCommand is ai:cost: what the dialog has spent, by model, priced
// where the service publishes prices (f4#1842, stage H9). Without a price
// list the tokens are still shown.
func aiCostCommand(pf *panel.PanelsFrame) {
	session := aiSession()
	spent := session.Spent()
	if len(spent) == 0 {
		vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.CostNothing"), []string{i18n.Msg("vtui.Ok")})
		return
	}
	cfg, _ := vtvibeConfig()
	var models []vtvibe.ModelInfo
	pf.RunProgressTask(i18n.Msg("AI.Title"), i18n.Msg("AI.Sending"), false,
		func(ctx context.Context, update func(msg string, percent int)) error {
			// A failed price list leaves the tokens; it is not an error.
			models, _ = cfg.ModelsWithInfo(ctx)
			return ctx.Err()
		},
		func(err error) {
			if err != nil {
				return
			}
			vtui.ShowMessage(i18n.Msg("AI.Title"), aiCostText(vtvibe.Costs(spent, models)), []string{i18n.Msg("vtui.Ok")})
		})
}

func aiCostText(costs []vtvibe.ModelCost) string {
	var lines []string
	var total vtvibe.Usage
	var money float64
	priced := false
	for _, c := range costs {
		total.In += c.Usage.In
		total.Out += c.Usage.Out
		line := fmt.Sprintf(i18n.Msg("AI.CostLine"), c.Model, vtvibe.FormatTokens(c.Usage.In), vtvibe.FormatTokens(c.Usage.Out))
		if c.Priced {
			line += fmt.Sprintf(" — $%.4f", c.Cost)
			money += c.Cost
			priced = true
		} else {
			line += " — " + i18n.Msg("AI.CostNoPrice")
		}
		lines = append(lines, line)
	}
	sum := fmt.Sprintf(i18n.Msg("AI.CostTotal"), vtvibe.FormatTokens(total.In), vtvibe.FormatTokens(total.Out))
	if priced {
		sum += fmt.Sprintf(" — $%.4f", money)
	}
	lines = append(lines, "", sum)
	return dialog.EscapeAmpersand(strings.Join(lines, "\n"))
}

// vtvibeCommandsDir holds the user's own commands, one NAME.md each
// (f4#1842, stage H9).
func vtvibeCommandsDir() string {
	return filepath.Join(config.GetF4ConfigDir(), "ai", "commands")
}

// aiUserCommand is "ai:/NAME arguments": it sends the text of NAME.md with
// the arguments put in; "ai:/" alone lists the commands and where they live.
func aiUserCommand(pf *panel.PanelsFrame, arg string) {
	name, args, _ := strings.Cut(strings.TrimSpace(arg), " ")
	dir := vtvibeCommandsDir()
	if name == "" {
		names, err := vtvibe.ListCommands(dir)
		if err != nil {
			aiShowError(err)
			return
		}
		text := fmt.Sprintf(i18n.Msg("AI.CommandsNone"), dir)
		if len(names) > 0 {
			text = fmt.Sprintf(i18n.Msg("AI.CommandsList"), "/"+strings.Join(names, "\n/"), dir)
		}
		vtui.ShowMessage(i18n.Msg("AI.Title"), dialog.EscapeAmpersand(text), []string{i18n.Msg("vtui.Ok")})
		return
	}
	template, err := vtvibe.LoadCommand(dir, name)
	if errors.Is(err, vtvibe.ErrNoCommand) {
		vtui.ShowMessage(i18n.Msg("AI.Title"), dialog.EscapeAmpersand(fmt.Sprintf(i18n.Msg("AI.CommandUnknown"), name, dir)), []string{i18n.Msg("vtui.Ok")})
		return
	}
	if err != nil {
		aiShowError(err)
		return
	}
	question := vtvibe.ExpandCommand(template, args)
	if question == "" {
		vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.EmptyDraft"), []string{i18n.Msg("vtui.Ok")})
		return
	}
	aiSend(pf, question)
}

// vtvibeNonstopDefault is the mode of the dialogs that did not choose their
// own (Settings → AI, f4#1842 stage H6): question-and-answer unless set.
func vtvibeNonstopDefault() bool {
	return ini.Load(vtvibeIniPath()).GetString("general", "nonstop", "false") == "true"
}

// aiNonstop reports the mode the current dialog works in.
func aiNonstop(session *vtvibe.Session) bool {
	switch session.Mode() {
	case vtvibe.ModeNonstop:
		return true
	case vtvibe.ModeQA:
		return false
	}
	return vtvibeNonstopDefault()
}

// aiModeCommand is ai:mode: alone it tells the current dialog's mode, with
// nonstop, qa or default it chooses one for this dialog (f4#1842, stage H6).
func aiModeCommand(pf *panel.PanelsFrame, arg string) {
	session := aiSession()
	if arg = strings.TrimSpace(arg); arg != "" {
		mode, err := vtvibe.ParseMode(arg)
		if err != nil {
			vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.ModeUsage"), []string{i18n.Msg("vtui.Ok")})
			return
		}
		session.SetMode(mode)
		aiBotRefresh(pf)
	}
	name := i18n.Msg("AI.ModeQA")
	if aiNonstop(session) {
		name = i18n.Msg("AI.ModeNonstop")
	}
	text := fmt.Sprintf(i18n.Msg("AI.ModeIs"), name)
	if session.Mode() == vtvibe.ModeDefault {
		text += "\n" + i18n.Msg("AI.ModeFromSettings")
	}
	vtui.ShowMessage(i18n.Msg("AI.Title"), text+"\n\n"+i18n.Msg("AI.ModeUsage"), []string{i18n.Msg("vtui.Ok")})
}

// aiDialogsMenu lists the dialogs ai:new put aside, newest first; Enter makes
// the chosen one current, the current one going to the archive in its place
// (f4#1842, stage H3).
func aiDialogsMenu(pf *panel.PanelsFrame) {
	dialogs, err := vtvibe.ListArchive(vtvibeArchiveDir())
	if err != nil {
		aiShowError(err)
		return
	}
	if len(dialogs) == 0 {
		vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.NoDialogs"), []string{i18n.Msg("vtui.Ok")})
		return
	}
	menu := vtui.NewVMenu(i18n.Msg("AI.DialogsTitle"))
	width := vtui.StringWidth(i18n.Msg("AI.DialogsTitle")) + 6
	for _, d := range dialogs {
		title := d.Title
		if title == "" {
			title = i18n.Msg("AI.DialogUntitled")
		}
		text := fmt.Sprintf("%s  %s (%d)", d.Saved.Format("2006-01-02 15:04"), title, d.Messages)
		width = max(width, vtui.StringWidth(text)+6)
		menu.AddItem(vtui.MenuItem{Text: dialog.EscapeAmpersand(text)})
	}
	menu.OnAction = func(idx int) {
		menu.Close()
		if idx < 0 || idx >= len(dialogs) {
			return
		}
		if err := aiSession().OpenArchived(dialogs[idx].Path, vtvibeArchiveDir()); err != nil {
			aiShowError(err)
			return
		}
		vtvibeConfig()
		aiBotRefresh(pf)
	}
	aiShowMenu(menu, width, len(dialogs))
}

// aiShowMenu centres a menu of rows items, width wide at most, and shows it.
func aiShowMenu(menu *vtui.VMenu, width, rows int) {
	sw, sh := 80, 25
	if vtui.FrameManager != nil {
		if w := vtui.FrameManager.GetScreenSize(); w > 0 {
			sw = w
		}
		if h := vtui.FrameManager.GetScreenHeight(); h > 0 {
			sh = h
		}
	}
	w := min(width, max(sw-4, 20))
	h := min(rows+2, max(sh-4, 3))
	x, y := (sw-w)/2, (sh-h)/2
	menu.SetPosition(x, y, x+w-1, y+h-1)
	vtui.FrameManager.Push(menu)
}

// aiOrdersCommand shows the register of the user's orders, or closes and
// reopens one: "ai:orders", "ai:done N", "ai:undone N" (f4#1842, stage H4).
func aiOrdersCommand(pf *panel.PanelsFrame, verb, arg string) {
	session := aiSession()
	if verb != "orders" {
		id, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(arg), "#"))
		if err == nil {
			err = session.SetOrderDone(id, verb == "done")
		}
		if err != nil {
			vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.OrderUnknown"), []string{i18n.Msg("vtui.Ok")})
			return
		}
		aiBotRefresh(pf)
	}
	vtui.ShowMessage(i18n.Msg("AI.OrdersTitle"), aiOrdersText(session.Orders()), []string{i18n.Msg("vtui.Ok")})
}

// aiOrdersText lists the open orders first, then the done ones.
func aiOrdersText(orders []vtvibe.Order) string {
	if len(orders) == 0 {
		return i18n.Msg("AI.NoOrders")
	}
	var lines []string
	for _, done := range []bool{false, true} {
		for _, o := range orders {
			if o.Done != done {
				continue
			}
			mark := i18n.Msg("AI.OrderOpen")
			if o.Done {
				mark = i18n.Msg("AI.OrderDone")
			}
			text := []rune(strings.ReplaceAll(strings.TrimSpace(o.Text), "\n", " "))
			if len(text) > 70 {
				text = append(text[:70], '…')
			}
			lines = append(lines, fmt.Sprintf("#%d %s %s", o.ID, mark, string(text)))
		}
	}
	return dialog.EscapeAmpersand(strings.Join(lines, "\n"))
}

// aiWorkers are the workers ai:task starts (f4#1842, stage H5).
var aiWorkers vtvibe.Workers

// aiTaskCommand handles "ai:task" (list), "ai:task stop N" and
// "ai:task <task>": the task goes to a worker in a clean context after a
// confirmation, as a bot does, and enters the register of orders; the
// worker's report comes into the chat and closes the order when it succeeded.
func aiTaskCommand(pf *panel.PanelsFrame, arg string) {
	manager := vtui.FrameManager
	arg = strings.TrimSpace(arg)
	lower := strings.ToLower(arg)
	switch {
	case arg == "":
		vtui.ShowMessage(i18n.Msg("AI.Title"), aiTasksText(aiWorkers.Running(), aiManagers.running()), []string{i18n.Msg("vtui.Ok")})
		return
	case strings.HasPrefix(lower, "stop "):
		which := strings.TrimPrefix(strings.TrimSpace(lower[len("stop "):]), "#")
		if rest, ok := strings.CutPrefix(which, "m"); ok {
			// A worker manager: it and every worker it started.
			if id, err := strconv.Atoi(rest); err != nil || !aiManagers.stop(id) {
				vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.TaskUnknown"), []string{i18n.Msg("vtui.Ok")})
			}
			return
		}
		id, err := strconv.Atoi(which)
		if err != nil || !aiWorkers.Stop(id) {
			vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.TaskUnknown"), []string{i18n.Msg("vtui.Ok")})
		}
		return
	case strings.HasPrefix(lower, "undo "):
		id, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(arg[len("undo "):]), "#"))
		if err != nil {
			id = 0
		}
		aiUndoTask(pf, id)
		return
	}
	cfg, keySource, provider := vtvibeProviderConfig()
	if cfg.APIKey == "" && keySource == "" && provider.NeedsKey(cfg.BaseURL) {
		aiShowError(vtvibe.ErrNoKey)
		return
	}
	dir := aiBotDir(pf)
	question := fmt.Sprintf(i18n.Msg("AI.TaskConfirm"), arg, dir, cfg.Model)
	dlg := vtui.ShowMessage(i18n.Msg("AI.Title"), question, []string{i18n.Msg("AI.BotStart"), i18n.Msg("vtui.Cancel")})
	dlg.OnResult = func(code int) {
		if code != 0 {
			return
		}
		session := aiSession()
		order := session.AddOrder(arg)
		aiStartWorker(pf, manager, session, arg, dir, order, true, nil)
	}
}

// aiStartWorker gives task, serving order (0: none), to a worker in dir and
// puts its report into the dialog. closeOrder closes the order when the
// worker succeeds: right for a task the user gave with ai:task, while an
// order the manager split up is closed by the manager. done, if set, runs on
// the UI thread after the report.
func aiStartWorker(pf *panel.PanelsFrame, manager interface{ PostTask(func()) }, session *vtvibe.Session, task, dir string, order int, closeOrder bool, done func(vtvibe.WorkerResult)) int {
	config := aiAgentConfig(session)
	// The worker's file changes are journaled so ai:task undo N can put
	// them back (f4#1842, stage H9).
	journal := &vtvibe.Journal{}
	// The MCP servers of ai/mcp.json run for this task, in its folder.
	mcp := &aiMCPRun{dir: dir}
	var label atomic.Value // the worker's name for the approval question, once known
	label.Store(i18n.Msg("AI.WorkerLabel"))
	tools := func() []vtvibe.Tool {
		list := append(vtvibe.WithJournal(vtvibe.WorkTools(dir, config().ToolEnv...), dir, journal), mcp.tools()...)
		return aiWithApproval(manager, func() string { return label.Load().(string) }, list)
	}
	aiWorkers.SetGates(vtvibe.GateRules{User: aiGateRules, Learned: aiLearnedRules, Learn: aiLearnRule})
	// The tokens are counted for the model the task started on, read now:
	// once a stopped worker winds down, nothing of it reads the settings.
	model := config().Model
	id := aiWorkers.Start(task, dir, config, tools, func(r vtvibe.WorkerResult) {
		text := aiTaskResultText(r, order)
		if problems := mcp.close(); problems != "" {
			text += "\n\n" + problems
		}
		if n := len(journal.Files()); n > 0 {
			text += "\n\n" + fmt.Sprintf(i18n.Msg("AI.TaskUndoHint"), n, r.ID)
		}
		manager.PostTask(func() {
			session.AddSpent(model, r.Usage)
			if r.Err == nil && closeOrder {
				_ = session.SetOrderDone(order, true)
			}
			session.Note("assistant", text)
			aiBotRefresh(pf)
			if done != nil {
				done(r)
			}
		})
	})
	label.Store(fmt.Sprintf(i18n.Msg("AI.WorkerLabelN"), id))
	aiJournals.Store(id, journal)
	if order > 0 {
		session.Note("assistant", fmt.Sprintf(i18n.Msg("AI.TaskStarted"), id, order, task))
	} else {
		session.Note("assistant", fmt.Sprintf(i18n.Msg("AI.WorkerStarted"), id, task))
	}
	aiBotRefresh(pf)
	return id
}

// vtvibeMCPPath is the MCP servers' configuration, in the format Claude
// Code uses (f4#1842, stage H9).
func vtvibeMCPPath() string {
	return filepath.Join(config.GetF4ConfigDir(), "ai", "mcp.json")
}

// aiMCPRun starts the configured MCP servers for one worker task, the first
// time it asks for tools, and stops them when the task is over.
type aiMCPRun struct {
	dir     string
	once    sync.Once
	mu      sync.Mutex
	clients []*vtvibe.MCPClient
	list    []vtvibe.Tool
	errs    []error
}

func (m *aiMCPRun) tools() []vtvibe.Tool {
	m.once.Do(func() {
		servers, err := vtvibe.LoadMCPConfig(vtvibeMCPPath())
		m.mu.Lock()
		defer m.mu.Unlock()
		if err != nil {
			m.errs = append(m.errs, err)
			return
		}
		ctx := context.Background()
		for name, s := range servers {
			c, err := vtvibe.StartMCP(ctx, name, s, m.dir)
			if err != nil {
				m.errs = append(m.errs, err)
				continue
			}
			m.clients = append(m.clients, c)
		}
		var errs []error
		m.list, errs = vtvibe.MCPTools(ctx, m.clients)
		m.errs = append(m.errs, errs...)
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.list
}

// close stops the servers and tells what went wrong with them, if anything.
func (m *aiMCPRun) close() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clients {
		c.Close()
	}
	m.clients = nil
	if len(m.errs) == 0 {
		return ""
	}
	return fmt.Sprintf(i18n.Msg("AI.MCPProblems"), errors.Join(m.errs...))
}

// vtvibeAskEach is Settings → AI → "Ask before each command" (f4#1842,
// stage H9): off, one confirmation covers a whole task or bot.
func vtvibeAskEach() bool {
	return ini.Load(vtvibeIniPath()).GetString("general", "ask_each", "false") == "true"
}

// vtvibeAllowPath is the allow list: one rule a line — a tool name or glob,
// or a shell command prefix.
func vtvibeAllowPath() string {
	return filepath.Join(config.GetF4ConfigDir(), "ai", "allow.txt")
}

var aiAllowMu sync.Mutex

func aiAllowList() vtvibe.AllowList {
	return vtvibe.AllowList{
		Rules: func() []string {
			data, err := os.ReadFile(vtvibeAllowPath()) // #nosec G304 G703 -- f4's own configuration file
			if err != nil {
				return nil
			}
			return strings.Split(string(data), "\n")
		},
		Add: func(rule string) {
			aiAllowMu.Lock()
			defer aiAllowMu.Unlock()
			data, _ := os.ReadFile(vtvibeAllowPath()) // #nosec G304 G703 -- see above
			text := strings.TrimRight(string(data), "\n")
			if text != "" {
				text += "\n"
			}
			_ = config.WriteUserFileAtomically(vtvibeAllowPath(), []byte(text+rule+"\n"), 0o600)
		},
	}
}

// aiWithApproval makes tools ask before each change when the setting is on.
// The question is posted to the UI thread; the tool's goroutine waits for
// the answer or for its task to be stopped.
func aiWithApproval(manager interface{ PostTask(func()) }, who func() string, tools []vtvibe.Tool) []vtvibe.Tool {
	if !vtvibeAskEach() {
		return tools
	}
	approve := func(ctx context.Context, tool, summary string) (vtvibe.Approval, error) {
		answer := make(chan vtvibe.Approval, 1)
		manager.PostTask(func() {
			text := fmt.Sprintf(i18n.Msg("AI.ApproveQuestion"), who(), tool, vtui.TruncateMiddle(summary, 400))
			dlg := vtui.ShowMessage(i18n.Msg("AI.Title"), dialog.EscapeAmpersand(text),
				[]string{i18n.Msg("AI.ApproveOnce"), i18n.Msg("AI.ApproveAlways"), i18n.Msg("AI.ApproveDeny")})
			dlg.OnResult = func(code int) {
				a := vtvibe.Deny
				switch code {
				case 0:
					a = vtvibe.AllowOnce
				case 1:
					a = vtvibe.AllowAlways
				}
				// The first answer counts: closing the window reports again
				// (-1), and a second send must not block the UI thread.
				select {
				case answer <- a:
				default:
				}
			}
		})
		select {
		case a := <-answer:
			return a, nil
		case <-ctx.Done():
			return vtvibe.Deny, ctx.Err()
		}
	}
	return vtvibe.WithApproval(tools, approve, aiAllowList())
}

// aiAllowCommand is ai:allow: the allow list in the editor.
func aiAllowCommand(pf *panel.PanelsFrame) {
	path := vtvibeAllowPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := config.WriteUserFileAtomically(path, nil, 0o600); err != nil {
			aiShowError(err)
			return
		}
	}
	actionOpenEditor(pf, vfs.NewOSVFS(filepath.Dir(path)), path)
}

// aiBotMCPRounds keeps the MCP servers of the bot's current round: begun
// when the round asks for its tools, finished with the round's report or
// when the bot is stopped.
type aiBotMCPRounds struct {
	mu  sync.Mutex
	cur *aiMCPRun
}

var aiBotMCP aiBotMCPRounds

func (b *aiBotMCPRounds) begin(dir string) []vtvibe.Tool {
	b.mu.Lock()
	if b.cur != nil {
		_ = b.cur.close()
	}
	b.cur = &aiMCPRun{dir: dir}
	run := b.cur
	b.mu.Unlock()
	return run.tools()
}

func (b *aiBotMCPRounds) finish() string {
	b.mu.Lock()
	run := b.cur
	b.cur = nil
	b.mu.Unlock()
	if run == nil {
		return ""
	}
	return run.close()
}

// aiMCPCommand is ai:mcp: the configured MCP servers and where they are set.
func aiMCPCommand() {
	path := vtvibeMCPPath()
	servers, err := vtvibe.LoadMCPConfig(path)
	if err != nil {
		aiShowError(err)
		return
	}
	text := fmt.Sprintf(i18n.Msg("AI.MCPNone"), path)
	if len(servers) > 0 {
		names := make([]string, 0, len(servers))
		for name, s := range servers {
			names = append(names, name+": "+strings.Join(append([]string{s.Command}, s.Args...), " "))
		}
		sort.Strings(names)
		text = fmt.Sprintf(i18n.Msg("AI.MCPList"), strings.Join(names, "\n"), path)
	}
	vtui.ShowMessage(i18n.Msg("AI.Title"), dialog.EscapeAmpersand(text), []string{i18n.Msg("vtui.Ok")})
}

// aiJournals keeps each worker's journal by its id, for ai:task undo N.
var aiJournals sync.Map

// aiUndoTask puts back the files worker id changed through its file tools.
func aiUndoTask(pf *panel.PanelsFrame, id int) {
	value, ok := aiJournals.Load(id)
	if !ok {
		vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.TaskUnknown"), []string{i18n.Msg("vtui.Ok")})
		return
	}
	if _, busy := aiWorkers.Running()[id]; busy {
		vtui.ShowMessage(i18n.Msg("AI.Title"), i18n.Msg("AI.TaskUndoBusy"), []string{i18n.Msg("vtui.Ok")})
		return
	}
	done, err := value.(*vtvibe.Journal).Undo()
	text := fmt.Sprintf(i18n.Msg("AI.TaskUndone"), id, len(done))
	if len(done) > 0 {
		text += "\n" + strings.Join(done, "\n")
	}
	if err != nil {
		text += "\n\n" + err.Error()
	}
	aiSession().Note("assistant", text)
	aiBotRefresh(pf)
	vtui.ShowMessage(i18n.Msg("AI.Title"), dialog.EscapeAmpersand(text), []string{i18n.Msg("vtui.Ok")})
}

// aiDelegate offers the tasks the manager handed out to the user; confirmed,
// each goes to its own worker. When the last report is in and the dialog
// works without stopping, the manager goes on by itself (f4#1842, stage H5).
func aiDelegate(pf *panel.PanelsFrame, session *vtvibe.Session, delegations []vtvibe.Delegation) {
	manager := vtui.FrameManager
	cfg, _ := vtvibeConfig()
	dir := aiBotDir(pf)
	// The whole task is shown, within reason: the user approves what the
	// workers will run.
	lines := make([]string, 0, len(delegations))
	for _, d := range delegations {
		task := []rune(d.Task)
		if len(task) > 400 {
			task = append(task[:400], '…')
		}
		if d.Order > 0 {
			lines = append(lines, fmt.Sprintf("#%d: %s", d.Order, string(task)))
		} else {
			lines = append(lines, "- "+string(task))
		}
	}
	question := fmt.Sprintf(i18n.Msg("AI.DelegateConfirm"), len(delegations), dialog.EscapeAmpersand(strings.Join(lines, "\n")), dir, cfg.Model)
	dlg := vtui.ShowMessage(i18n.Msg("AI.Title"), question, []string{i18n.Msg("AI.BotStart"), i18n.Msg("vtui.Cancel")})
	dlg.OnResult = func(code int) {
		if code != 0 {
			// The manager learns it from the dialog, not by guessing.
			session.Note("assistant", i18n.Msg("AI.DelegateDeclined"))
			aiBotRefresh(pf)
			return
		}
		left := len(delegations)
		for _, d := range delegations {
			aiStartManager(pf, manager, session, d.Task, dir, func() {
				if left--; left == 0 && aiNonstop(session) && !session.Busy() {
					aiRunWork(pf, session, func(ctx context.Context) (vtvibe.WorkEnd, error) {
						c, _ := vtvibeConfig()
						return session.Resume(ctx, c)
					})
				}
			})
		}
	}
}

func aiTaskResultText(r vtvibe.WorkerResult, order int) string {
	var sb strings.Builder
	switch {
	case r.Err != nil && order > 0:
		fmt.Fprintf(&sb, i18n.Msg("AI.TaskFailed"), r.ID, order, r.Err)
	case r.Err != nil:
		fmt.Fprintf(&sb, i18n.Msg("AI.WorkerFailed"), r.ID, r.Err)
	default:
		if order > 0 {
			fmt.Fprintf(&sb, i18n.Msg("AI.TaskDone"), r.ID, order, len(r.Steps), r.Usage.In, r.Usage.Out)
		} else {
			fmt.Fprintf(&sb, i18n.Msg("AI.WorkerDone"), r.ID, len(r.Steps), r.Usage.In, r.Usage.Out)
		}
		sb.WriteString("\n\n")
		sb.WriteString(r.Report)
	}
	if r.Restarts > 0 {
		sb.WriteString("\n\n")
		fmt.Fprintf(&sb, i18n.Msg("AI.TaskRestarts"), r.Restarts)
	}
	if r.GateReturns > 0 {
		sb.WriteString("\n\n")
		fmt.Fprintf(&sb, i18n.Msg("AI.GateReturned"), r.GateReturns)
	}
	if r.Gate != "" {
		sb.WriteString("\n\n")
		sb.WriteString(i18n.Msg("AI.GateObjections"))
		sb.WriteString("\n")
		sb.WriteString(r.Gate)
	}
	if r.Learned != "" {
		sb.WriteString("\n\n")
		fmt.Fprintf(&sb, i18n.Msg("AI.GateLearned"), r.Learned)
	}
	return sb.String()
}

func aiTasksText(running, managers map[int]string) string {
	if len(running) == 0 && len(managers) == 0 {
		return i18n.Msg("AI.NoTasks")
	}
	var lines []string
	for _, id := range sortedIDs(managers) {
		lines = append(lines, fmt.Sprintf(i18n.Msg("AI.ManagerLine"), id, orderLine(managers[id])))
	}
	for _, id := range sortedIDs(running) {
		lines = append(lines, fmt.Sprintf("#%d %s", id, orderLine(running[id])))
	}
	return dialog.EscapeAmpersand(strings.Join(lines, "\n"))
}

func sortedIDs(m map[int]string) []int {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func orderLine(text string) string {
	r := []rune(strings.ReplaceAll(strings.TrimSpace(text), "\n", " "))
	if len(r) > 70 {
		r = append(r[:70], '…')
	}
	return string(r)
}

// aiStartManager hands a task of the main dialog to a worker manager of its
// own (f4#1842, docs/VTVIBE.md § 19a.2): it splits the task, runs the parts
// on workers — each started the way ai:task starts one, journal, MCP servers
// and approval included — and its one report goes to the dialog. done is
// called on the UI goroutine when it has reported.
func aiStartManager(pf *panel.PanelsFrame, manager interface{ PostTask(func()) }, session *vtvibe.Session, task, dir string, done func()) {
	config := aiAgentConfig(session)
	ctx, cancel := context.WithCancel(context.Background())
	run := aiManagers.add(task, cancel)
	session.Note("assistant", fmt.Sprintf(i18n.Msg("AI.ManagerStarted"), run.id, task))
	aiBotRefresh(pf)
	go func() {
		defer aiManagers.remove(run.id)
		work := func(ctx context.Context, subtask string) vtvibe.WorkerResult {
			reported := make(chan vtvibe.WorkerResult, 1)
			manager.PostTask(func() {
				if ctx.Err() != nil {
					reported <- vtvibe.WorkerResult{Task: subtask, Err: ctx.Err()}
					return
				}
				run.addWorker(aiStartWorker(pf, manager, session, subtask, dir, 0, false, func(r vtvibe.WorkerResult) { reported <- r }))
			})
			select {
			case r := <-reported:
				return r
			case <-ctx.Done():
				return vtvibe.WorkerResult{Task: subtask, Err: ctx.Err()}
			}
		}
		result := vtvibe.RunWorkerManager(ctx, config, task, vtvibe.DefaultParallelWorkers, work)
		cancel()
		// The workers' tokens were counted as each reported; only the
		// manager's own are left.
		own := result.Usage
		for _, w := range result.Workers {
			own.In -= w.Usage.In
			own.Out -= w.Usage.Out
		}
		model := config().Model
		text := fmt.Sprintf(i18n.Msg("AI.ManagerReport"), len(result.Workers), strings.TrimSpace(result.Report))
		if result.Err != nil {
			text = fmt.Sprintf(i18n.Msg("AI.ManagerFailed"), result.Err, len(result.Workers))
		}
		manager.PostTask(func() {
			session.AddSpent(model, own)
			session.Note("assistant", text)
			aiBotRefresh(pf)
			if done != nil {
				done()
			}
		})
	}()
}

// aiManagers are the worker managers at work: ai:task lists them as mN, and
// ai:task stop mN stops one together with the workers it started.
var aiManagers aiManagerSet

type aiManagerSet struct {
	mu   sync.Mutex
	next int
	runs map[int]*aiManagerRun
}

type aiManagerRun struct {
	id      int
	task    string
	cancel  context.CancelFunc
	mu      sync.Mutex
	workers []int
}

func (r *aiManagerRun) addWorker(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workers = append(r.workers, id)
}

func (m *aiManagerSet) add(task string, cancel context.CancelFunc) *aiManagerRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runs == nil {
		m.runs = map[int]*aiManagerRun{}
	}
	m.next++
	run := &aiManagerRun{id: m.next, task: task, cancel: cancel}
	m.runs[run.id] = run
	return run
}

func (m *aiManagerSet) remove(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.runs, id)
}

func (m *aiManagerSet) running() map[int]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int]string, len(m.runs))
	for id, r := range m.runs {
		out[id] = r.task
	}
	return out
}

// stop cancels the manager and stops its workers; false when there is no
// manager id.
func (m *aiManagerSet) stop(id int) bool {
	m.mu.Lock()
	run := m.runs[id]
	m.mu.Unlock()
	if run == nil {
		return false
	}
	run.cancel()
	run.mu.Lock()
	workers := append([]int(nil), run.workers...)
	run.mu.Unlock()
	for _, w := range workers {
		aiWorkers.Stop(w)
	}
	return true
}
