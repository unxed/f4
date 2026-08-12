package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type OpPrecondition struct {
	Vfs   vfs.VFS
	Path  string
	MTime time.Time
	Size  int64
	IsDir bool
}

type TaskReporter interface {
	UpdateScan(currentPath string, files, dirs int64)
	UpdateTransfer(action string, filename string, currentPct int, totalText string, totalPct int, speedText string)
	IsCancelled() bool
}

type DialogReporter struct {
	dlg *FileOpProgressDialog
}

func (r *DialogReporter) UpdateScan(currentPath string, files, dirs int64) {
	vtui.FrameManager.PostTask(func() {
		r.dlg.UpdateScan(currentPath, files, dirs)
		vtui.FrameManager.Redraw()
	})
}

func (r *DialogReporter) UpdateTransfer(action, filename string, currentPct int, totalText string, totalPct int, speedText string) {
	vtui.FrameManager.PostTask(func() {
		r.dlg.UpdateTransfer(action, filename, currentPct, totalText, totalPct, speedText)
		vtui.FrameManager.Redraw()
	})
}

func (r *DialogReporter) IsCancelled() bool {
	return r.dlg.IsDone()
}

type DummyReporter struct{}

func (r *DummyReporter) UpdateScan(currentPath string, files, dirs int64) {}
func (r *DummyReporter) UpdateTransfer(action, filename string, currentPct int, totalText string, totalPct int, speedText string) {
}
func (r *DummyReporter) IsCancelled() bool { return false }

type QueueTask struct {
	mu          sync.Mutex
	ID          int
	Type        string
	Desc        string
	State       string // Queued, Starting, Scanning, Running, Cancelling, Done, Error, Cancelled
	Progress    int
	TotalText   string
	Speed       string
	CurrentFile string
	ErrorMsg    error

	Preconditions []OpPrecondition
	ResKeys       []string

	Run         func(ctx context.Context, reporter TaskReporter, anchor vtui.Frame) error
	OpenDetails func(anchor vtui.Frame)
	Finalize    func()
	OnComplete  func()

	ctx    context.Context
	cancel context.CancelFunc
	// queuedFinalizing distinguishes a queued task whose asynchronous teardown
	// is already scheduled from a running task in the ordinary Cancelling state.
	queuedFinalizing bool

	completionOnce sync.Once
	finalizeOnce   sync.Once
}

func (t *QueueTask) finalize() {
	if t == nil {
		return
	}
	t.finalizeOnce.Do(func() {
		if t.Finalize != nil {
			t.Finalize()
		}
	})
}

func (t *QueueTask) UpdateScan(currentPath string, files, dirs int64) {
	t.mu.Lock()
	if queueTaskTerminal(t.State) || t.State == "Cancelling" {
		vtui.DebugLog("QUEUE_DEBUG: UpdateScan ignored for Task %d (State: %s)", t.ID, t.State)
		t.mu.Unlock()
		return
	}
	vtui.DebugLog("QUEUE_DEBUG: Task %d Scanning -> %s", t.ID, currentPath)
	t.State = "Scanning"
	t.CurrentFile = currentPath
	t.TotalText = fmt.Sprintf("Files: %d, Dirs: %d", files, dirs)
	t.mu.Unlock()

	GlobalQueueManager.RequestRefresh()
}
func (t *QueueTask) UpdateTransfer(action string, filename string, currentPct int, totalText string, totalPct int, speedText string) {
	t.mu.Lock()
	if queueTaskTerminal(t.State) || t.State == "Cancelling" {
		t.mu.Unlock()
		return
	}
	t.State = "Running"
	t.CurrentFile = filename
	t.Progress = totalPct
	t.TotalText = totalText

	// Extract clean speed from composite timeSpeedText string if applicable
	displaySpeed := speedText
	if len(speedText) >= 37 {
		displaySpeed = strings.TrimSpace(speedText[37:])
	}
	t.Speed = displaySpeed

	t.mu.Unlock()

	GlobalQueueManager.RequestRefresh()
}

func (t *QueueTask) IsCancelled() bool {
	if t.ctx != nil {
		return t.ctx.Err() != nil
	}
	return false
}

func queueTaskTerminal(state string) bool {
	return state == "Done" || state == "Error" || state == "Cancelled"
}

func queueTaskActive(state string) bool {
	return state == "Queued" || state == "Starting" || state == "Scanning" || state == "Running" || state == "Cancelling"
}

func queueTaskCancellable(state string) bool {
	return state == "Queued" || state == "Starting" || state == "Scanning" || state == "Running"
}

type OpQueueManager struct {
	mu             sync.Mutex
	tasks          []*QueueTask
	nextID         int
	activeKeys     map[string]bool
	frame          *QueueFrame
	refreshPending bool
}

var GlobalQueueManager *OpQueueManager

func init() {
	GlobalQueueManager = &OpQueueManager{
		activeKeys: make(map[string]bool),
	}
	go GlobalQueueManager.workerLoop()
}
func (qm *OpQueueManager) RequestRefresh() {
	qm.mu.Lock()
	if qm.refreshPending {
		qm.mu.Unlock()
		return
	}
	qm.refreshPending = true
	qm.mu.Unlock()

	vtui.FrameManager.PostTask(func() {
		qm.mu.Lock()
		qm.refreshPending = false
		qm.mu.Unlock()
		qm.RefreshUI()
	})
}

func getResourceKey(v vfs.VFS) string {
	if v == nil {
		return ""
	}
	if _, ok := v.(*vfs.OSVFS); ok {
		if runtime.GOOS == "windows" {
			return filepath.VolumeName(v.GetPath())
		}
		return "local_disk"
	}
	if parent := v.ParentVFS(); parent != nil {
		return getResourceKey(parent)
	}
	return fmt.Sprintf("%p", v)
}

func (qm *OpQueueManager) Enqueue(task *QueueTask) {
	qm.mu.Lock()
	qm.nextID++
	task.ID = qm.nextID
	task.State = "Queued"
	task.ctx, task.cancel = context.WithCancel(context.Background())
	qm.tasks = append(qm.tasks, task)
	qm.mu.Unlock()

	vtui.FrameManager.PostTask(func() {
		qm.EnsureQueueWorkspace()
		qm.RefreshUI()
	})

	go func(id int) {
		time.Sleep(500 * time.Millisecond)
		qm.mu.Lock()
		defer qm.mu.Unlock()
		for _, t := range qm.tasks {
			if t.ID != id {
				continue
			}
			t.mu.Lock()
			active := queueTaskActive(t.State)
			t.mu.Unlock()
			if active {
				vtui.ShowToast("Background operation started. Press Ctrl+Tab for Queue.", 4*time.Second)
			}
			break
		}
	}(task.ID)
}

func (qm *OpQueueManager) EnsureQueueWorkspace() {
	if vtui.FrameManager == nil || vtui.FrameManager.Screens == nil {
		return
	}
	for _, s := range vtui.FrameManager.Screens {
		for _, f := range s.Frames {
			if qf, ok := f.(*QueueFrame); ok {
				qm.mu.Lock()
				qm.frame = qf
				qm.mu.Unlock()
				return
			}
		}
	}

	frame := NewQueueFrame()
	vtui.FrameManager.AddScreenBackground(frame)
	qm.mu.Lock()
	qm.frame = frame
	qm.mu.Unlock()
}

func (qm *OpQueueManager) ActiveTasksCount() int {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	count := 0
	for _, t := range qm.tasks {
		t.mu.Lock()
		isActive := queueTaskActive(t.State)
		t.mu.Unlock()
		if isActive {
			count++
		}
	}
	return count
}

// Cancel requests cancellation without pretending that work or teardown has
// already stopped. Both queued and executing tasks remain active as Cancelling
// until Finalize or executeTask has unwound off the UI thread.
func (qm *OpQueueManager) Cancel(id int) bool {
	qm.mu.Lock()
	var cancel context.CancelFunc
	var complete *QueueTask
	found := false
	for _, t := range qm.tasks {
		if t.ID != id {
			continue
		}
		t.mu.Lock()
		switch t.State {
		case "Queued":
			// Stay active while asynchronous Finalize releases resources captured
			// at enqueue time; it publishes the terminal state when finished.
			t.State = "Cancelling"
			t.queuedFinalizing = true
			cancel = t.cancel
			complete = t
			found = true
		case "Starting", "Scanning", "Running":
			t.State = "Cancelling"
			cancel = t.cancel
			found = true
		case "Cancelling":
			if !t.queuedFinalizing {
				cancel = t.cancel
				found = true
			}
		}
		t.mu.Unlock()
		break
	}
	qm.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if complete != nil {
		qm.RequestRefresh()
		// Plugin/VFS teardown can block. Keep the task active as Cancelling and
		// finish it off-thread so CancelAll can signal every task promptly and
		// the UI never waits inside a button or quit callback.
		go func() {
			complete.finalize()
			complete.mu.Lock()
			complete.queuedFinalizing = false
			complete.State = "Cancelled"
			complete.ErrorMsg = nil
			complete.mu.Unlock()
			qm.postTaskCompletion(complete)
		}()
	} else if found {
		qm.RequestRefresh()
	}
	return found
}

// CancelAll requests cancellation for every task that has not reached a
// terminal state. It intentionally does not wait: shutdown paths must be able
// to signal all operations before tearing down the UI.
func (qm *OpQueueManager) CancelAll() {
	qm.mu.Lock()
	ids := make([]int, 0, len(qm.tasks))
	for _, t := range qm.tasks {
		t.mu.Lock()
		if queueTaskActive(t.State) {
			ids = append(ids, t.ID)
		}
		t.mu.Unlock()
	}
	qm.mu.Unlock()

	for _, id := range ids {
		qm.Cancel(id)
	}
}

func (qm *OpQueueManager) RefreshUI() {
	qm.mu.Lock()
	frame := qm.frame
	tasks := append([]*QueueTask(nil), qm.tasks...)
	qm.mu.Unlock()
	if frame != nil {
		frame.UpdateTasks(tasks)
	}
}

// postTaskCompletion serializes all terminal paths through one UI callback.
// In particular, Cancel and workerLoop race while a task is Queued: whichever
// path claims it is responsible for completion, and completionOnce protects
// against any future path accidentally posting the callback a second time.
func (qm *OpQueueManager) postTaskCompletion(t *QueueTask) {
	t.finalize()
	t.completionOnce.Do(func() {
		vtui.FrameManager.PostTask(func() {
			if t.OnComplete != nil {
				t.OnComplete()
			}
			qm.RefreshUI()
		})
	})
}

func (qm *OpQueueManager) workerLoop() {
	for {
		time.Sleep(200 * time.Millisecond)

		qm.mu.Lock()
		var toRun *QueueTask
		for _, t := range qm.tasks {
			t.mu.Lock()
			isQueued := t.State == "Queued"
			t.mu.Unlock()

			if isQueued {
				canRun := true
				for _, rk := range t.ResKeys {
					if qm.activeKeys[rk] {
						canRun = false
						break
					}
				}
				if canRun {
					toRun = t
					for _, rk := range t.ResKeys {
						qm.activeKeys[rk] = true
					}
					t.mu.Lock()
					t.State = "Starting"
					t.mu.Unlock()
					break
				}
			}
		}
		qm.mu.Unlock()

		if toRun != nil {
			go qm.executeTask(toRun)
		}
	}
}

func (qm *OpQueueManager) executeTask(t *QueueTask) {
	vtui.DebugLog("QUEUE_DEBUG: Executing Task %d (%s)", t.ID, t.Type)
	var taskErr error
	if t.Run == nil {
		taskErr = fmt.Errorf("internal error: task run function is nil")
	}
	if taskErr == nil && t.ctx != nil {
		taskErr = t.ctx.Err()
	}
	for _, pc := range t.Preconditions {
		if taskErr != nil {
			break
		}
		ctx := t.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		st, err := pc.Vfs.Stat(ctx, pc.Path)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				taskErr = context.Canceled
			} else {
				taskErr = fmt.Errorf("conflict: missing %s", pc.Path)
			}
			break
		}
		if st.MTime != pc.MTime || st.Size != pc.Size || st.IsDir != pc.IsDir {
			taskErr = fmt.Errorf("conflict: modified %s", pc.Path)
			break
		}
	}

	if taskErr == nil && t.ctx != nil {
		taskErr = t.ctx.Err()
	}
	if taskErr == nil {
		runCtx := t.ctx
		if runCtx == nil {
			runCtx = context.Background()
		}
		qm.mu.Lock()
		anchor := qm.frame
		qm.mu.Unlock()
		taskErr = t.Run(runCtx, t, anchor)
	}

	// Finalize before publishing a terminal state. Shutdown waits by counting
	// active states, so this ordering guarantees captured VFS sessions and
	// other task-owned resources are gone when the count reaches zero.
	t.finalize()

	t.mu.Lock()
	ctxCancelled := t.ctx != nil && errors.Is(t.ctx.Err(), context.Canceled)
	switch {
	case errors.Is(taskErr, context.Canceled) || ctxCancelled:
		t.State = "Cancelled"
		t.ErrorMsg = nil
	case taskErr != nil:
		t.State = "Error"
		t.ErrorMsg = taskErr
	default:
		t.State = "Done"
		t.Progress = 100
		t.ErrorMsg = nil
	}
	finalState := t.State
	t.mu.Unlock()

	qm.mu.Lock()
	for _, rk := range t.ResKeys {
		qm.activeKeys[rk] = false
	}
	qm.mu.Unlock()

	vtui.DebugLog("QUEUE_DEBUG: Task %d finalized with state %s. Posting OnComplete.", t.ID, finalState)

	qm.postTaskCompletion(t)
}

type QueueFrame struct {
	vtui.BaseWindow
	table *vtui.Table
	tasks []*QueueTask
}

type queueRow struct {
	task *QueueTask
}

func (r queueRow) GetCellText(col int) string {
	t := r.task
	t.mu.Lock()
	defer t.mu.Unlock()
	switch col {
	case 0:
		return fmt.Sprintf("%d", t.ID)
	case 1:
		return t.State
	case 2:
		return t.Type
	case 3:
		if t.State == "Running" || t.State == "Scanning" || t.State == "Cancelling" {
			return t.CurrentFile
		}
		return t.Desc
	case 4:
		pct := t.Progress
		bars := (pct * 10) / 100
		s := ""
		for i := 0; i < 10; i++ {
			if i < bars {
				s += "█"
			} else {
				s += "░"
			}
		}
		return fmt.Sprintf("%3d%% %s", pct, s)
	case 5:
		return t.Speed
	}
	return ""
}
func (r queueRow) GetCellAttr(col int, def uint64) uint64 {
	t := r.task
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.State == "Error" {
		return themedForeground(def, vtui.ColWarnHighlightBoxTitle)
	}
	if t.State == "Done" {
		return themedForeground(def, vtui.ColDialogText)
	}
	if t.State == "Running" || t.State == "Scanning" {
		return themedForeground(def, vtui.ColDialogHighlightText)
	}
	if t.State == "Cancelled" || t.State == "Cancelling" {
		return vtui.DimColor(def)
	}
	return def
}

func NewQueueFrame() *QueueFrame {
	scrW, scrH := 80, 25
	if vtui.FrameManager != nil && vtui.FrameManager.GetScreenSize() > 0 {
		scrW = vtui.FrameManager.GetScreenSize()
		scrH = vtui.FrameManager.GetScreenHeight()
	}

	qf := &QueueFrame{
		BaseWindow: *vtui.NewBaseWindow(0, 2, scrW-1, scrH-1, " Operations Queue "),
	}
	qf.ShowClose = true
	qf.ShowZoom = true
	qf.SetGrowMode(vtui.GrowHiX | vtui.GrowHiY)

	btnCancel := vtui.NewButton(0, 0, Msg("Queue.BtnCancel"))
	btnClear := vtui.NewButton(0, 0, Msg("Queue.BtnClear"))

	qf.table = vtui.NewTableWithButtons(&qf.BaseWindow, []vtui.TableColumn{
		{Title: "ID", Width: 4},
		{Title: "State", Width: 10},
		{Title: "Type", Width: 8},
		{Title: "Description / Current File", MinWidth: 10},
		{Title: "Progress", Width: 24},
		{Title: "Speed", Width: 12},
	}, btnCancel, btnClear)
	useDialogTableColors(qf.table)
	qf.table.Sortable = true
	qf.table.QuickSearch = true
	qf.table.ShowScrollBar = true
	qf.table.OnAction = func(idx int) { qf.openTaskDetails(idx) }

	btnCancel.OnClick = func() {
		idx := qf.table.SelectPos
		if idx >= 0 && idx < len(qf.tasks) {
			t := qf.tasks[idx]
			t.mu.Lock()
			state := t.State
			t.mu.Unlock()
			if queueTaskCancellable(state) {
				vtui.ShowMessageOn(qf, " Confirm ", "Cancel task ID "+fmt.Sprintf("%d", t.ID)+"?", []string{"&Yes", "&No"}).OnResult = func(c int) {
					if c == 0 {
						GlobalQueueManager.Cancel(t.ID)
					}
				}
			}
		}
	}

	btnClear.OnClick = func() {
		GlobalQueueManager.mu.Lock()
		var active []*QueueTask
		for _, t := range GlobalQueueManager.tasks {
			t.mu.Lock()
			isDone := queueTaskTerminal(t.State)
			t.mu.Unlock()
			if !isDone {
				active = append(active, t)
			}
		}
		GlobalQueueManager.tasks = active
		GlobalQueueManager.mu.Unlock()
		GlobalQueueManager.RefreshUI()
	}

	return qf
}

func (qf *QueueFrame) UpdateTasks(tasks []*QueueTask) {
	qf.tasks = append([]*QueueTask(nil), tasks...)
	rows := make([]vtui.TableRow, len(qf.tasks))
	for i, t := range qf.tasks {
		rows[i] = queueRow{task: t}
	}
	qf.table.SetRows(rows)
	vtui.FrameManager.Redraw()
}

func (qf *QueueFrame) GetType() vtui.FrameType { return vtui.TypeUser }

func (qf *QueueFrame) openTaskDetails(idx int) {
	if idx < 0 || idx >= len(qf.tasks) {
		return
	}
	t := qf.tasks[idx]
	t.mu.Lock()
	openDetails := t.OpenDetails
	isErr := t.State == "Error"
	errMsg := t.ErrorMsg
	t.mu.Unlock()

	if openDetails != nil {
		openDetails(qf)
	} else if isErr && errMsg != nil {
		dlg := vtui.ShowMessageOn(qf, " Error Details ", errMsg.Error(), []string{"&Ok"})
		dlg.IsWarning = true
	}
}

func (qf *QueueFrame) ProcessKey(e *vtinput.InputEvent) bool {
	ctrlW := e.KeyDown && e.VirtualKeyCode == vtinput.VK_W &&
		(e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed)) != 0
	if e.KeyDown && (e.VirtualKeyCode == vtinput.VK_ESCAPE || e.VirtualKeyCode == vtinput.VK_F10 || ctrlW) {
		active := false
		GlobalQueueManager.mu.Lock()
		for _, t := range GlobalQueueManager.tasks {
			t.mu.Lock()
			isActive := queueTaskActive(t.State)
			t.mu.Unlock()
			if isActive {
				active = true
				break
			}
		}
		GlobalQueueManager.mu.Unlock()
		if active {
			vtui.ShowToast("Cannot close queue while operations are active. Use Ctrl+Tab to switch.", 3*time.Second)
			return true // Swallow ESC/F10
		}
	}

	// Let BaseWindow process focus cycling and button clicks
	if qf.BaseWindow.ProcessKey(e) {
		return true
	}

	// A task may expose live progress or a retained final result. Errors that do
	// not provide richer details keep the existing message fallback.
	if e.KeyDown && e.VirtualKeyCode == vtinput.VK_RETURN {
		idx := qf.table.SelectPos
		if idx >= 0 && idx < len(qf.tasks) {
			qf.openTaskDetails(idx)
			return true
		}
	}

	return false
}
