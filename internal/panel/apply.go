package panel

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

var LastApplyCommandTemplate string

var foregroundApplyCommands = struct {
	sync.Mutex
	next uint64
	runs map[uint64]foregroundApplyCommand
}{runs: make(map[uint64]foregroundApplyCommand)}

type foregroundApplyCommand struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type ApplyPanelCapture struct {
	Panel    *FileSystemPanel
	PanelVFS vfs.VFS
	Vfs      vfs.VFS
	Dir      string
	Snapshot cmdline.ApplyCommandPanel
}

type ApplyCommandSession struct {
	Pf         *PanelsFrame
	Active     ApplyPanelCapture
	passive    ApplyPanelCapture
	Targets    []string
	Explicit   bool
	Tokens     map[string]PanelSelectionToken
	runner     vfs.CommandRunner
	info       vfs.CommandRunnerInfo
	template   *cmdline.CompiledApplyCommand
	values     cmdline.ApplyCommandPromptValues
	silent     bool
	mode       cmdline.ApplyCommandMode
	workers    int
	activeSide cmdline.ApplyCommandPanelSide
	Items      []cmdline.ApplyBatchItem
	ownedVFS   []vfs.VFS
	releaseVFS sync.Once
}

func RegisterForegroundApplyCommand(cancel context.CancelFunc) func() {
	foregroundApplyCommands.Lock()
	foregroundApplyCommands.next++
	id := foregroundApplyCommands.next
	entry := foregroundApplyCommand{cancel: cancel, done: make(chan struct{})}
	foregroundApplyCommands.runs[id] = entry
	foregroundApplyCommands.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			foregroundApplyCommands.Lock()
			delete(foregroundApplyCommands.runs, id)
			foregroundApplyCommands.Unlock()
			close(entry.done)
		})
	}
}

func CancelAllForegroundApplyCommands() {
	foregroundApplyCommands.Lock()
	cancels := make([]context.CancelFunc, 0, len(foregroundApplyCommands.runs))
	for _, run := range foregroundApplyCommands.runs {
		cancels = append(cancels, run.cancel)
	}
	foregroundApplyCommands.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func activeForegroundApplyCommandCount() int {
	foregroundApplyCommands.Lock()
	defer foregroundApplyCommands.Unlock()
	return len(foregroundApplyCommands.runs)
}

func PanelCanApplyCommand() bool {
	pf := FindPanelsFrame()
	if pf == nil {
		return false
	}
	panel := pf.GetActivePanel()
	if panel == nil || panel.Vfs == nil {
		return false
	}
	_, _, ok := ResolveApplyCommandRunner(panel.Vfs)
	return ok
}

func ActionApplyCommand(pf *PanelsFrame) {
	if pf == nil || vtui.FrameManager == nil {
		return
	}
	active := pf.GetActivePanel()
	passive := pf.GetInactivePanel()
	if active == nil || active.Vfs == nil {
		return
	}

	marked := active.GetMarkedNames()
	targets := append([]string(nil), marked...)
	explicit := len(targets) != 0
	if !explicit {
		targets = active.GetSelectedNames()
	}
	if len(targets) == 0 {
		vtui.ShowMessageOn(pf, i18n.Msg("ApplyCommand.NoTargetsTitle"), i18n.Msg("ApplyCommand.NoTargets"), []string{i18n.Msg("vtui.Ok")})
		return
	}
	runner, info, ok := ResolveApplyCommandRunner(active.Vfs)
	if !ok {
		vtui.ShowMessageOn(pf, i18n.Msg("ApplyCommand.UnsupportedTitle"), i18n.Msg("ApplyCommand.Unsupported"), []string{i18n.Msg("vtui.Ok")})
		return
	}

	tokens := make(map[string]PanelSelectionToken, len(marked))
	for _, name := range marked {
		if token, exists := active.CaptureSelectionToken(name); exists {
			tokens[name] = token
		}
	}
	session := &ApplyCommandSession{
		Pf: pf, Active: captureApplyCommandPanel(active, targets), passive: captureApplyCommandPanel(passive, nil),
		Targets: targets, Explicit: explicit, Tokens: tokens, runner: runner, info: info,
	}
	if pf.ActiveIdx == 1 {
		session.activeSide = cmdline.ApplyCommandRightSide
	}
	showApplyCommandDialog(session)
}

func ResolveApplyCommandRunner(target vfs.VFS) (vfs.CommandRunner, vfs.CommandRunnerInfo, bool) {
	if availability, ok := target.(vfs.CommandRunnerAvailabilityProvider); ok && !availability.CommandRunnerAvailable() {
		return nil, vfs.CommandRunnerInfo{}, false
	}
	if _, local := target.(*vfs.OSVFS); local {
		runner := terminal.NewLocalCommandRunner()
		return runner, runner.CommandRunnerInfo(), true
	}
	runner, ok := target.(vfs.CommandRunner)
	if !ok {
		return nil, vfs.CommandRunnerInfo{}, false
	}
	info := vfs.CommandRunnerInfo{Dialect: vfs.CommandDialectUnknown, MaxParallel: 1}
	if provider, hasInfo := target.(vfs.CommandRunnerInfoProvider); hasInfo {
		info = provider.CommandRunnerInfo()
		if info.MaxParallel < 0 {
			info.MaxParallel = 1
		}
	}
	switch info.Dialect {
	case vfs.CommandDialectUnknown, vfs.CommandDialectPOSIX, vfs.CommandDialectCmd, vfs.CommandDialectPowerShell:
	default:
		// Optional provider metadata is an external extension point. Treat
		// values added by a newer or faulty provider as unknown instead of
		// accidentally selecting the raw/no-quoting path.
		info.Dialect = vfs.CommandDialectUnknown
	}
	return runner, info, true
}

func captureApplyCommandPanel(panel *FileSystemPanel, selectedOverride []string) ApplyPanelCapture {
	if panel == nil || panel.Vfs == nil {
		return ApplyPanelCapture{}
	}
	dir := panel.Vfs.GetPath()
	current := panel.GetRawSelectedName()
	if current == ".." {
		current = ""
	}
	selected := append([]string(nil), selectedOverride...)
	if selected == nil {
		selected = panel.GetMarkedNames()
		if len(selected) == 0 && current != "" {
			selected = []string{current}
		}
	}
	realDir := dir
	if abs, err := panel.Vfs.Abs(dir); err == nil && abs != "" {
		realDir = abs
	}
	if _, local := panel.Vfs.(*vfs.OSVFS); local {
		if resolved, err := filepath.EvalSymlinks(realDir); err == nil {
			realDir = resolved
		}
	}
	shortDir := cmdline.ApplyCommandShortPath(dir)
	realShortDir := cmdline.ApplyCommandShortPath(realDir)
	makeFile := func(name string) cmdline.ApplyCommandFile {
		if name == "" {
			return cmdline.ApplyCommandFile{}
		}
		short := name
		if _, local := panel.Vfs.(*vfs.OSVFS); local {
			if shortPath := cmdline.ApplyCommandShortPath(panel.Vfs.Join(dir, name)); shortPath != "" {
				short = filepath.Base(shortPath)
			}
		}
		return cmdline.ApplyCommandFile{Name: name, ShortName: short}
	}
	selectedFiles := make([]cmdline.ApplyCommandFile, 0, len(selected))
	for _, name := range selected {
		if name != "" && name != ".." {
			selectedFiles = append(selectedFiles, makeFile(name))
		}
	}
	return ApplyPanelCapture{
		Panel: panel, PanelVFS: panel.Vfs, Vfs: panel.Vfs, Dir: dir,
		Snapshot: cmdline.ApplyCommandPanel{
			PathStyle: detectApplyCommandPathStyle(panel.Vfs, dir),
			Directory: dir, ShortDirectory: shortDir, RealDirectory: realDir, RealShortDirectory: realShortDir,
			Current: makeFile(current), Selected: selectedFiles,
		},
	}
}

func detectApplyCommandPathStyle(filesystem vfs.VFS, directory string) cmdline.ApplyCommandPathStyle {
	if filesystem != nil {
		joined := filesystem.Join("f4-apply-style-a", "f4-apply-style-b")
		if strings.Contains(joined, `\`) && !strings.Contains(joined, "/") {
			return cmdline.ApplyCommandPathStyleWindows
		}
		if strings.Contains(joined, "/") {
			return cmdline.ApplyCommandPathStylePOSIX
		}
	}
	return cmdline.EffectiveApplyCommandPathStyle(directory, cmdline.ApplyCommandPathStyleUnknown)
}

func (s *ApplyCommandSession) contextFor(name string) cmdline.ApplyCommandContext {
	active := s.Active.Snapshot
	active.Current = applyCommandFileForTarget(s.Active, name)
	return cmdline.ApplyCommandContext{
		Dialect: applyCommandDialect(s.info.Dialect), ActiveSide: s.activeSide,
		Active: active, Passive: s.passive.Snapshot,
	}
}

func applyCommandFileForTarget(capture ApplyPanelCapture, name string) cmdline.ApplyCommandFile {
	for _, File := range capture.Snapshot.Selected {
		if File.Name == name {
			return File
		}
	}
	return cmdline.ApplyCommandFile{Name: name, ShortName: name}
}

func applyCommandDialect(dialect vfs.CommandDialect) cmdline.ApplyCommandDialect {
	switch dialect {
	case vfs.CommandDialectPOSIX:
		return cmdline.ApplyCommandDialectPOSIX
	case vfs.CommandDialectCmd:
		return cmdline.ApplyCommandDialectCMD
	case vfs.CommandDialectPowerShell:
		return cmdline.ApplyCommandDialectPowerShell
	default:
		return cmdline.ApplyCommandDialectRaw
	}
}

func showApplyCommandDialog(session *ApplyCommandSession) {
	const width = applyDialogWidth
	dlg := &applyCommandDialog{Window: vtui.NewCenteredDialog(width, applyDialogHeight, i18n.Msg("ApplyCommand.Title"))}
	dlg.ShowClose = true
	dlg.SetHelp("ApplyCmd")

	initial := LastApplyCommandTemplate
	editCommand := vtui.NewEdit(0, 0, width-4, initial)
	editCommand.HistoryID = "ApplyCmd"
	editCommand.ShowHistoryButton = true
	editCommand.DeduplicateHistory = true
	if vtui.GlobalHistoryProvider != nil {
		editCommand.History = vtui.GlobalHistoryProvider.LoadHistory(editCommand.HistoryID)
		if initial == "" && len(editCommand.History) > 0 {
			editCommand.SetText(editCommand.History[0])
		}
	}
	lblCommand := vtui.NewLabel(0, 0, i18n.Msg("ApplyCommand.Prompt"), editCommand)
	txtTargets := vtui.NewText(0, 0, fmt.Sprintf(i18n.Msg("ApplyCommand.TargetsFmt"), len(session.Targets)), 0)

	modes := []string{i18n.Msg("ApplyCommand.ModeSequential"), i18n.Msg("ApplyCommand.ModeParallel"), i18n.Msg("ApplyCommand.ModeQueue")}
	comboMode := vtui.NewComboBox(0, 0, applyDialogModeWidth, modes)
	comboMode.DropdownOnly = true
	comboMode.Menu.SetSelectPos(0)
	comboMode.Edit.SetText(modes[0])
	lblMode := vtui.NewLabel(0, 0, i18n.Msg("ApplyCommand.Mode"), comboMode)

	workerDefault := config.App.ApplyCommandParallelism
	if workerDefault <= 0 {
		workerDefault = runtime.NumCPU()
	}
	editWorkers := vtui.NewEdit(0, 0, applyDialogWorkersWidth, strconv.Itoa(workerDefault))
	lblWorkers := vtui.NewLabel(0, 0, i18n.Msg("ApplyCommand.Workers"), editWorkers)
	chkUnlimited := vtui.NewCheckbox(0, 0, i18n.Msg("ApplyCommand.Unlimited"), false)
	if config.App.ApplyCommandParallelism == 0 {
		chkUnlimited.State = 1
	}

	btnRun := vtui.NewButton(0, 0, i18n.Msg("ApplyCommand.Run"))
	btnRun.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	items := []vtui.UIElement{lblCommand, editCommand, txtTargets, lblMode, comboMode, lblWorkers, editWorkers, chkUnlimited, btnRun, btnCancel}
	for _, item := range items {
		dlg.AddItem(item)
	}

	dlg.commandLabel, dlg.command, dlg.targets = lblCommand, editCommand, txtTargets
	dlg.modeLabel, dlg.mode = lblMode, comboMode
	dlg.workersLabel, dlg.workers, dlg.unlimited = lblWorkers, editWorkers, chkUnlimited
	dlg.parametersRule = vtui.NewSeparator(0, 0, width, true, true)
	dlg.buttonsRule = vtui.NewSeparator(0, 0, width, true, true)
	dlg.AddItem(dlg.parametersRule)
	dlg.AddItem(dlg.buttonsRule)
	buttons := vtui.NewHBoxLayout(0, 0, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(btnRun, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	dlg.buttons = buttons
	dlg.MinW = dlg.minimumWidth()
	dlg.layoutControls()

	updateWorkers := func() {
		parallel := comboMode.Menu.SelectPos == int(cmdline.ApplyCommandParallel)
		lblWorkers.SetDisabled(!parallel)
		chkUnlimited.SetDisabled(!parallel)
		editWorkers.SetDisabled(!parallel || chkUnlimited.State == 1)
	}
	comboMode.Menu.OnAction = func(index int) {
		if index < 0 || index >= len(modes) {
			return
		}
		comboMode.Menu.SetSelectPos(index)
		comboMode.Edit.SetText(modes[index])
		updateWorkers()
	}
	chkUnlimited.OnChange = func(int) { updateWorkers() }
	updateWorkers()

	btnCancel.OnClick = func() { dlg.Close() }
	btnRun.OnClick = func() {
		raw := editCommand.GetText()
		executable := strings.TrimLeftFunc(raw, unicode.IsSpace)
		if strings.TrimSpace(executable) == "" {
			vtui.ShowMessageOn(dlg, i18n.Msg("ApplyCommand.Title"), i18n.Msg("ApplyCommand.InvalidCommand"), []string{i18n.Msg("vtui.Ok")})
			return
		}
		silent := false
		if strings.HasPrefix(executable, "@") {
			silent = true
			executable = strings.TrimLeftFunc(strings.TrimPrefix(executable, "@"), unicode.IsSpace)
		}
		if strings.TrimSpace(executable) == "" {
			vtui.ShowMessageOn(dlg, i18n.Msg("ApplyCommand.Title"), i18n.Msg("ApplyCommand.InvalidCommand"), []string{i18n.Msg("vtui.Ok")})
			return
		}
		compiled, err := cmdline.CompileApplyCommand(executable)
		if err != nil {
			vtui.ShowMessageOn(dlg, i18n.Msg("ApplyCommand.Title"), fmt.Sprintf(i18n.Msg("ApplyCommand.InvalidTemplateFmt"), err), []string{i18n.Msg("vtui.Ok")})
			return
		}
		if session.info.Dialect == vfs.CommandDialectUnknown && compiled.Metadata().RequiresDialect {
			vtui.ShowMessageOn(dlg, i18n.Msg("ApplyCommand.Title"), i18n.Msg("ApplyCommand.UnknownDialect"), []string{i18n.Msg("vtui.Ok")})
			return
		}
		mode := cmdline.ApplyCommandSequential
		switch comboMode.Menu.SelectPos {
		case 1:
			mode = cmdline.ApplyCommandParallel
		case 2:
			mode = cmdline.ApplyCommandQueued
		}
		workers := 1
		if mode == cmdline.ApplyCommandParallel {
			if chkUnlimited.State == 1 {
				workers = 0
			} else {
				workers, err = strconv.Atoi(strings.TrimSpace(editWorkers.GetText()))
				if err != nil || workers <= 0 {
					vtui.ShowMessageOn(dlg, i18n.Msg("ApplyCommand.InvalidWorkersTitle"), i18n.Msg("ApplyCommand.InvalidWorkers"), []string{i18n.Msg("vtui.Ok")})
					return
				}
			}
		}
		firstContext := session.contextFor(session.Targets[0])
		prompts, err := compiled.ResolvePrompts(firstContext)
		if err != nil {
			vtui.ShowMessageOn(dlg, i18n.Msg("ApplyCommand.Title"), fmt.Sprintf(i18n.Msg("ApplyCommand.InvalidTemplateFmt"), err), []string{i18n.Msg("vtui.Ok")})
			return
		}
		accept := func(values cmdline.ApplyCommandPromptValues) {
			session.template = compiled
			session.values = values
			session.silent = silent
			session.mode = mode
			session.workers = workers
			if err := session.prepareExecution(); err != nil {
				vtui.ShowMessageOn(dlg, i18n.Msg("ApplyCommand.Title"), fmt.Sprintf(i18n.Msg("ApplyCommand.InvalidTemplateFmt"), err), []string{i18n.Msg("vtui.Ok")})
				return
			}
			LastApplyCommandTemplate = raw
			editCommand.AddHistory(raw)
			if mode == cmdline.ApplyCommandParallel {
				config.App.ApplyCommandParallelism = workers
				config.RequestSaveConfig()
			}
			session.Active.Panel.SaveSelection()
			dlg.Close()
			launchApplyCommandSession(session)
		}
		if len(prompts) == 0 {
			accept(cmdline.ApplyCommandPromptValues{})
			return
		}
		ShowApplyCommandPrompts(dlg, prompts, accept)
	}

	vtui.FrameManager.PushToFrameScreen(session.Pf, dlg)
}

func ShowApplyCommandPrompts(anchor vtui.Frame, prompts []cmdline.ApplyCommandResolvedPrompt, accepted func(cmdline.ApplyCommandPromptValues)) {
	showApplyCommandPrompts(anchor, prompts, accepted, nil)
}

func showApplyCommandPrompts(anchor vtui.Frame, prompts []cmdline.ApplyCommandResolvedPrompt, accepted func(cmdline.ApplyCommandPromptValues), canceled func()) {
	const pageSize = 10

	width := 70
	visibleRows := min(len(prompts), pageSize)
	// Keep the dialog usable on a conventional 25-line terminal. Templates
	// can contain any number of fields; additional fields are paged within
	// this same preflight dialog and retain their values between pages.
	height := 7 + visibleRows
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("ApplyCommand.PromptTitle"))
	dlg.ShowClose = true
	completed := false
	dlg.OnResult = func(int) {
		if !completed && canceled != nil {
			canceled()
		}
	}
	dlg.SetHelp("ApplyCmd")
	contentX := dlg.X1 + 2
	contentRight := dlg.X2 - 2
	rowY := dlg.Y1 + 2
	labelWidth := 26
	editX := contentX + labelWidth + 1
	editWidth := max(1, contentRight-editX+1)

	labels := make([]*vtui.Text, len(prompts))
	edits := make([]*vtui.Edit, len(prompts))
	fieldLocked := make([]bool, len(prompts))
	for i, prompt := range prompts {
		title := prompt.Title
		if title == "" {
			title = fmt.Sprintf(i18n.Msg("ApplyCommand.ValueFmt"), i+1)
		}
		title = dialog.EscapeAmpersand(vtui.TruncateMiddle(title, labelWidth))
		y := rowY + i%pageSize
		edit := vtui.NewEdit(editX, y, editWidth, prompt.Initial)
		if prompt.History != "" {
			edit.HistoryID = prompt.History
			edit.ShowHistoryButton = true
			edit.DeduplicateHistory = true
			if vtui.GlobalHistoryProvider != nil {
				edit.History = vtui.GlobalHistoryProvider.LoadHistory(prompt.History)
			}
		}
		label := vtui.NewLabel(contentX, y, title, edit)
		label.SetPosition(contentX, y, editX-2, y)
		dlg.AddItem(label)
		dlg.AddItem(edit)
		label.SetDisabled(true)
		edit.SetDisabled(true)
		label.Lock()
		edit.Lock()
		fieldLocked[i] = true
		labels[i] = label
		edits[i] = edit
	}

	pageInfo := vtui.NewText(contentX, dlg.Y2-3, "", 0)
	pageInfo.SetPosition(contentX, dlg.Y2-3, contentRight, dlg.Y2-3)
	dlg.AddItem(pageInfo)
	btnBack := vtui.NewButton(0, 0, i18n.Msg("ApplyCommand.PromptBack"))
	btnNext := vtui.NewButton(0, 0, i18n.Msg("ApplyCommand.PromptNext"))
	btnOK := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	dlg.AddItem(btnBack)
	dlg.AddItem(btnNext)
	dlg.AddItem(btnOK)
	dlg.AddItem(btnCancel)
	buttons := vtui.NewHBoxLayout(contentX, dlg.Y2-2, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(btnBack, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnNext, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnOK, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	buttons.Apply()

	page := 0
	renderPage := func(redraw bool) {
		start := page * pageSize
		end := min(start+pageSize, len(prompts))
		for i := range edits {
			visible := i >= start && i < end
			if visible && fieldLocked[i] {
				labels[i].Unlock()
				edits[i].Unlock()
				fieldLocked[i] = false
			} else if !visible && !fieldLocked[i] {
				labels[i].Lock()
				edits[i].Lock()
				fieldLocked[i] = true
			}
			labels[i].SetVisible(visible)
			edits[i].SetVisible(visible)
			labels[i].SetDisabled(!visible)
			edits[i].SetDisabled(!visible)
		}
		pageInfo.SetText(fmt.Sprintf(i18n.Msg("ApplyCommand.PromptPageFmt"), start+1, end, len(prompts)))
		btnBack.SetDisabled(page == 0)
		btnNext.SetDisabled(end == len(prompts))
		btnOK.SetDisabled(end != len(prompts))
		btnNext.IsDefault = end != len(prompts)
		btnOK.IsDefault = end == len(prompts)
		if start < len(edits) {
			dlg.SetFocusedItem(edits[start])
		}
		if redraw {
			vtui.FrameManager.Redraw()
		}
	}
	btnBack.OnClick = func() {
		if page > 0 {
			page--
			renderPage(true)
		}
	}
	btnNext.OnClick = func() {
		if (page+1)*pageSize < len(prompts) {
			page++
			renderPage(true)
		}
	}
	btnCancel.OnClick = func() { dlg.Close() }
	btnOK.OnClick = func() {
		values := make(cmdline.ApplyCommandPromptValues, len(prompts))
		for i, prompt := range prompts {
			value := edits[i].GetText()
			values[prompt.Index] = value
			if edits[i].HistoryID != "" {
				edits[i].AddHistory(value)
			}
		}
		completed = true
		dlg.Close()
		accepted(values)
	}
	renderPage(false)
	vtui.FrameManager.PushToFrameScreen(anchor, dlg)
}

func launchApplyCommandSession(session *ApplyCommandSession) {
	items := session.Items
	workers := EffectiveApplyCommandWorkers(session.mode, session.workers, len(items), session.info.MaxParallel)
	model := cmdline.NewApplyBatchViewModel(len(items))
	observe := func(event cmdline.ApplyBatchEvent) {
		model.Observe(session.mode == cmdline.ApplyCommandParallel, event)
		if event.Kind == cmdline.ApplyBatchItemFinished {
			session.PostItemFinished(event.Result)
		}
	}
	request := cmdline.ApplyBatchRequest{
		Dir: session.Active.Dir, Items: items, Runner: session.runner, Parallelism: workers,
		Expand: session.expandItem, Observe: observe,
	}
	if session.mode == cmdline.ApplyCommandQueued {
		session.enqueue(request, model)
		return
	}

	runCtx, cancel := context.WithCancel(context.Background())
	unregister := RegisterForegroundApplyCommand(cancel)
	cmdline.ShowApplyOutputDialog(session.Pf, model, func() {
		cancel()
	})
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		defer unregister()
		defer cancel()
		defer session.releaseCapturedVFSes()
		result := cmdline.RunApplyCommandBatch(runCtx, request)
		ctx.RunOnUI(func() {
			model.Finish(result)
			session.RefreshCapturedPanels()
		})
	})
}

func (s *ApplyCommandSession) prepareExecution() error {
	items, err := s.batchItems()
	if err != nil {
		return err
	}
	activeClone := s.Active.PanelVFS.Clone()
	if activeClone == nil {
		return fmt.Errorf("apply command: active file system could not be captured")
	}
	passiveClone := vfs.VFS(nil)
	if s.passive.PanelVFS != nil {
		passiveClone = s.passive.PanelVFS.Clone()
		if passiveClone == nil {
			if !fileops.SameVFSInstance(activeClone, s.Active.PanelVFS) {
				_ = activeClone.Close()
			}
			return fmt.Errorf("apply command: passive file system could not be captured")
		}
	}
	runner, info, ok := ResolveApplyCommandRunner(activeClone)
	if !ok {
		if !fileops.SameVFSInstance(activeClone, s.Active.PanelVFS) {
			_ = activeClone.Close()
		}
		if passiveClone != nil && !fileops.SameVFSInstance(passiveClone, s.passive.PanelVFS) {
			_ = passiveClone.Close()
		}
		return fmt.Errorf("%s", i18n.Msg("ApplyCommand.Unsupported"))
	}
	if info.Dialect == vfs.CommandDialectUnknown && s.template.Metadata().RequiresDialect {
		if !fileops.SameVFSInstance(activeClone, s.Active.PanelVFS) {
			_ = activeClone.Close()
		}
		if passiveClone != nil && !fileops.SameVFSInstance(passiveClone, s.passive.PanelVFS) {
			_ = passiveClone.Close()
		}
		return fmt.Errorf("%s", i18n.Msg("ApplyCommand.UnknownDialect"))
	}
	s.Active.Vfs = activeClone
	s.passive.Vfs = passiveClone
	s.runner, s.info, s.Items = runner, info, items
	if !fileops.SameVFSInstance(activeClone, s.Active.PanelVFS) {
		s.ownedVFS = append(s.ownedVFS, activeClone)
	}
	if passiveClone != nil && !fileops.SameVFSInstance(passiveClone, s.passive.PanelVFS) {
		s.ownedVFS = append(s.ownedVFS, passiveClone)
	}
	return nil
}

func (s *ApplyCommandSession) releaseCapturedVFSes() {
	if s == nil {
		return
	}
	s.releaseVFS.Do(func() {
		for i := len(s.ownedVFS) - 1; i >= 0; i-- {
			_ = s.ownedVFS[i].Close()
		}
	})
}

func (s *ApplyCommandSession) batchItems() ([]cmdline.ApplyBatchItem, error) {
	first, err := s.template.Expand(s.contextFor(s.Targets[0]), s.values)
	if err != nil {
		return nil, err
	}
	if first.Cardinality == cmdline.ApplyCommandOnce {
		return []cmdline.ApplyBatchItem{{Name: s.Targets[0], AffectedNames: append([]string(nil), s.Targets...)}}, nil
	}
	items := make([]cmdline.ApplyBatchItem, len(s.Targets))
	for i, name := range s.Targets {
		items[i] = cmdline.ApplyBatchItem{Name: name, AffectedNames: []string{name}}
	}
	return items, nil
}

func (s *ApplyCommandSession) expandItem(ctx context.Context, _ int, item cmdline.ApplyBatchItem) (cmdline.ApplyExpandedCommand, error) {
	if err := ctx.Err(); err != nil {
		return cmdline.ApplyExpandedCommand{}, err
	}
	expansion, err := s.template.Expand(s.contextFor(item.Name), s.values)
	if err != nil {
		return cmdline.ApplyExpandedCommand{}, err
	}
	paths, release, err := cmdline.MaterializeApplyCommandResources(ctx, s.Active.Vfs, s.Active.Dir, s.info.Dialect, expansion.Resources)
	if err != nil {
		return cmdline.ApplyExpandedCommand{}, err
	}
	command, err := expansion.Render(paths)
	if err != nil {
		if release != nil {
			release(false)
		}
		return cmdline.ApplyExpandedCommand{}, err
	}
	return cmdline.ApplyExpandedCommand{Command: command, Silent: s.silent, Cleanup: release}, nil
}

func EffectiveApplyCommandWorkers(mode cmdline.ApplyCommandMode, configured, count, providerCap int) int {
	if mode != cmdline.ApplyCommandParallel {
		return 1
	}
	workers := configured
	if workers <= 0 || workers > count {
		workers = count
	}
	if providerCap > 0 && workers > providerCap {
		workers = providerCap
	}
	if workers < 1 {
		workers = 1
	}
	return workers
}

func (s *ApplyCommandSession) PostItemFinished(result cmdline.ApplyBatchItemResult) {
	if !s.Explicit || len(result.AffectedNames) == 0 {
		return
	}
	clear := func() {
		if s.Pf == nil || s.Active.Panel == nil {
			return
		}
		s.Pf.PtyMutex.Lock()
		closed := s.Pf.Closed
		s.Pf.PtyMutex.Unlock()
		if closed {
			return
		}
		for _, name := range result.AffectedNames {
			if token, ok := s.Tokens[name]; ok {
				s.Active.Panel.ClearSelectionIfUnchanged(token)
			}
		}
		if vtui.FrameManager != nil {
			vtui.FrameManager.Redraw()
		}
	}
	if vtui.FrameManager != nil {
		vtui.FrameManager.PostTask(clear)
	} else {
		clear()
	}
}

func (s *ApplyCommandSession) RefreshCapturedPanels() {
	if s == nil || s.Pf == nil {
		return
	}
	s.Pf.PtyMutex.Lock()
	closed := s.Pf.Closed
	s.Pf.PtyMutex.Unlock()
	if closed {
		return
	}
	seen := make(map[*FileSystemPanel]bool)
	for _, capture := range []ApplyPanelCapture{s.Active, s.passive} {
		if capture.Panel == nil || seen[capture.Panel] || !fileops.SameVFSInstance(capture.Panel.Vfs, capture.PanelVFS) || capture.Panel.Vfs.GetPath() != capture.Dir {
			continue
		}
		seen[capture.Panel] = true
		capture.Panel.ReadDirectory()
	}
}

func (s *ApplyCommandSession) enqueue(request cmdline.ApplyBatchRequest, model *cmdline.ApplyBatchViewModel) {
	preconditions := s.QueuePreconditions()
	task := &fileops.QueueTask{
		Type: i18n.Msg("ApplyCommand.QueueType"), Desc: fmt.Sprintf(i18n.Msg("ApplyCommand.QueueDescriptionFmt"), len(s.Targets)),
		Preconditions: preconditions, ResKeys: []string{fileops.GetResourceKey(s.Active.PanelVFS)},
	}
	task.Run = func(ctx context.Context, reporter fileops.TaskReporter, _ vtui.Frame) error {
		defer s.releaseCapturedVFSes()
		originalObserve := request.Observe
		request.Observe = func(event cmdline.ApplyBatchEvent) {
			originalObserve(event)
			if event.Kind == cmdline.ApplyBatchItemFinished {
				completed := event.Index + 1
				pct := completed * 100 / len(request.Items)
				reporter.UpdateTransfer(i18n.Msg("ApplyCommand.QueueType"), event.Name, pct, fmt.Sprintf("%d/%d", completed, len(request.Items)), pct, "")
			}
		}
		result := cmdline.RunApplyCommandBatch(ctx, request)
		model.Finish(result)
		if err := ctx.Err(); err != nil {
			return err
		}
		if result.Failed > 0 {
			return fmt.Errorf(i18n.Msg("ApplyCommand.QueueFailedFmt"), result.Failed)
		}
		return nil
	}
	task.OpenDetails = func(anchor vtui.Frame) {
		cmdline.ShowApplyOutputDialog(anchor, model, func() { fileops.GlobalQueueManager.Cancel(task.ID) })
	}
	task.Finalize = s.releaseCapturedVFSes
	task.OnComplete = func() {
		s.releaseCapturedVFSes()
		if !model.IsDone() {
			state, _, taskErr := task.Status()
			fallback := cmdline.ApplyBatchResult{Items: make([]cmdline.ApplyBatchItemResult, len(request.Items)), NotStarted: len(request.Items)}
			if state == "Cancelled" {
				model.AddTranscript(i18n.Msg("ApplyCommand.ResultCancelled"))
			} else {
				if taskErr != nil {
					model.AddTranscript(fmt.Sprintf(i18n.Msg("ApplyCommand.ResultFailedFmt"), taskErr))
				}
			}
			model.Finish(fallback)
		}
		s.RefreshCapturedPanels()
		toast.Show(i18n.Msg("ApplyCommand.StatusFinishedToast"), 3*time.Second)
	}
	fileops.GlobalQueueManager.Enqueue(task)
	toast.Show(i18n.Msg("ApplyCommand.QueuedToast"), 3*time.Second)
}

func (s *ApplyCommandSession) QueuePreconditions() []fileops.OpPrecondition {
	if s.Active.Panel == nil || s.Active.Vfs == nil {
		return nil
	}
	wanted := make(map[string]struct{}, len(s.Targets))
	for _, name := range s.Targets {
		wanted[name] = struct{}{}
	}
	_, local := s.Active.PanelVFS.(*vfs.OSVFS)
	conditions := make([]fileops.OpPrecondition, 0, len(wanted))
	for _, panelEntry := range s.Active.Panel.Entries {
		if _, ok := wanted[panelEntry.Name]; !ok {
			continue
		}
		path := s.Active.Vfs.Join(s.Active.Dir, panelEntry.Name)
		entry := panelEntry.VFSItem
		if panelEntry.IsSymlink {
			// Remote Stat would turn Run into an unbounded UI-thread network
			// operation. Omit that precondition; local Stat is cheap and makes
			// its baseline match the queue's follow-symlink comparison.
			if !local {
				continue
			}
			statEntry, err := s.Active.Vfs.Stat(context.Background(), path)
			if err != nil {
				continue
			}
			entry = statEntry
		}
		conditions = append(conditions, fileops.OpPrecondition{
			Vfs: s.Active.Vfs, Path: path,
			MTime: entry.MTime, Size: entry.Size, IsDir: entry.IsDir,
		})
	}
	return conditions
}
