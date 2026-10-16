package fileops

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/toast"
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
	dlg       *FileOpProgressDialog
	scheduler *dialogProgressScheduler
}

const dialogProgressUpdateInterval = 100 * time.Millisecond

type dialogProgressUpdateKind uint8

const (
	dialogProgressScan dialogProgressUpdateKind = iota + 1
	dialogProgressTransfer
)

type dialogProgressUpdate struct {
	kind dialogProgressUpdateKind

	currentPath string
	files       int64
	dirs        int64

	action     string
	filename   string
	currentPct int
	totalText  string
	totalPct   int
	speedText  string
}

// dialogProgressScheduler keeps progress updates lossless from the worker's
// perspective while limiting UI work to one latest-state update per interval.
// This matters especially for archive extraction with many small files: the
// worker can report thousands of intermediate states, but rendering every one
// of them starves keyboard events and makes the UI queue grow without bound.
type dialogProgressScheduler struct {
	mu        sync.Mutex
	latest    dialogProgressUpdate
	dirty     bool
	scheduled bool
	stopped   bool
	post      func(func())
	schedule  func(time.Duration, func())
	interval  time.Duration
	apply     func(dialogProgressUpdate)
}

func newDialogProgressScheduler(post func(func()), schedule func(time.Duration, func()), interval time.Duration, apply func(dialogProgressUpdate)) *dialogProgressScheduler {
	return &dialogProgressScheduler{
		post:     post,
		schedule: schedule,
		interval: interval,
		apply:    apply,
	}
}

func (s *dialogProgressScheduler) request(update dialogProgressUpdate) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.latest = update
	s.dirty = true
	if s.scheduled {
		s.mu.Unlock()
		return
	}
	s.scheduled = true
	s.dirty = false
	s.mu.Unlock()

	s.post(s.run)
}

func (s *dialogProgressScheduler) run() {
	s.mu.Lock()
	update := s.latest
	s.mu.Unlock()
	s.apply(update)

	s.schedule(s.interval, func() {
		s.mu.Lock()
		if s.stopped || !s.dirty {
			s.scheduled = false
			s.mu.Unlock()
			return
		}
		s.dirty = false
		s.mu.Unlock()
		s.post(s.run)
	})
}

func (s *dialogProgressScheduler) stop() {
	s.mu.Lock()
	s.stopped = true
	s.dirty = false
	s.mu.Unlock()
}

func NewDialogReporter(dlg *FileOpProgressDialog) *DialogReporter {
	r := &DialogReporter{dlg: dlg}
	// Taken once, here, rather than inside the closures below. The scheduler
	// posts and redraws from a timer goroutine that keeps running for as long
	// as the operation does, and reading the global from there races anything
	// that reassigns vtui.FrameManager meanwhile -- in the tests, the next
	// test's swapFrameManager, including the one in its cleanup.
	frames := vtui.FrameManager
	r.scheduler = newDialogProgressScheduler(
		func(task func()) { frames.PostTask(task) },
		func(delay time.Duration, task func()) { time.AfterFunc(delay, task) },
		dialogProgressUpdateInterval,
		func(update dialogProgressUpdate) {
			if r.dlg.IsDone() {
				return
			}
			switch update.kind {
			case dialogProgressScan:
				r.dlg.UpdateScan(update.currentPath, update.files, update.dirs)
			case dialogProgressTransfer:
				r.dlg.UpdateTransfer(update.action, update.filename, update.currentPct, update.totalText, update.totalPct, update.speedText)
			}
			frames.Redraw()
		},
	)
	return r
}

func (r *DialogReporter) UpdateScan(currentPath string, files, dirs int64) {
	r.scheduler.request(dialogProgressUpdate{
		kind:        dialogProgressScan,
		currentPath: currentPath,
		files:       files,
		dirs:        dirs,
	})
}

func (r *DialogReporter) UpdateTransfer(action, filename string, currentPct int, totalText string, totalPct int, speedText string) {
	r.scheduler.request(dialogProgressUpdate{
		kind:       dialogProgressTransfer,
		action:     action,
		filename:   filename,
		currentPct: currentPct,
		totalText:  totalText,
		totalPct:   totalPct,
		speedText:  speedText,
	})
}

func (r *DialogReporter) Stop() {
	r.scheduler.stop()
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
	// mu owns State, Progress, TotalText, Speed, CurrentFile, ErrorMsg,
	// and queuedFinalizing.
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
	if QueueTaskTerminal(t.State) || t.State == "Cancelling" {
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
	if QueueTaskTerminal(t.State) || t.State == "Cancelling" {
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

func QueueTaskTerminal(state string) bool {
	return state == "Done" || state == "Error" || state == "Cancelled"
}

func queueTaskActive(state string) bool {
	return state == "Queued" || state == "Starting" || state == "Scanning" || state == "Running" || state == "Cancelling"
}

func QueueTaskCancellable(state string) bool {
	return state == "Queued" || state == "Starting" || state == "Scanning" || state == "Running"
}

type OpQueueManager struct {
	// When both manager and task state are needed, lock mu before QueueTask.mu.
	mu             sync.Mutex
	tasks          []*QueueTask
	nextID         int
	activeKeys     map[string]bool
	wake           chan struct{}
	frame          *QueueFrame
	refreshPending bool
}

var GlobalQueueManager *OpQueueManager

// QueueShowToast is how the queue tells the user an operation went to the
// background. The root supplies the real toast; a test silences it.
var QueueShowToast = func(message string, duration time.Duration) {
	toast.Show(message, duration)
}

func init() {
	GlobalQueueManager = &OpQueueManager{
		activeKeys: make(map[string]bool),
		wake:       make(chan struct{}, 1),
	}
}

var queueWorkerOnce sync.Once

// StartQueueWorker starts the background scheduler. It is idempotent: the
// worker is started at most once per process, so every entry point may call it
// without checking whether another already did.
//
// Enqueueing before it runs is safe and stays safe: wake is buffered and
// workerLoop has a periodic fallback, so a task queued ahead of the worker is
// picked up on the first cycle.
func StartQueueWorker() {
	queueWorkerOnce.Do(func() {
		go GlobalQueueManager.workerLoop()
	})
}

// signalWorker wakes the scheduler as soon as a task is enqueued or a
// resource is released. The periodic fallback in workerLoop still repairs a
// missed notification and keeps zero-value test managers usable.
func (qm *OpQueueManager) signalWorker() {
	qm.mu.Lock()
	if qm.wake == nil {
		qm.wake = make(chan struct{}, 1)
	}
	wake := qm.wake
	qm.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
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

func GetResourceKey(v vfs.VFS) string {
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
		return GetResourceKey(parent)
	}
	return fmt.Sprintf("%p", v)
}

func (qm *OpQueueManager) Enqueue(task *QueueTask) {
	qm.mu.Lock()
	qm.nextID++
	task.ID = qm.nextID
	task.mu.Lock()
	task.State = "Queued"
	task.mu.Unlock()
	task.ctx, task.cancel = context.WithCancel(context.Background())
	// One queued operation asks for the sudo password at most once (f4#1998).
	task.ctx = vfs.WithElevationScope(task.ctx)
	qm.tasks = append(qm.tasks, task)
	qm.mu.Unlock()
	qm.signalWorker()

	vtui.FrameManager.PostTask(func() {
		qm.EnsureQueueWorkspace()
		qm.RefreshUI()
	})

	go func(id int) {
		time.Sleep(500 * time.Millisecond)
		qm.mu.Lock()
		active := false
		for _, t := range qm.tasks {
			if t.ID != id {
				continue
			}
			t.mu.Lock()
			active = queueTaskActive(t.State)
			t.mu.Unlock()
			break
		}
		qm.mu.Unlock()
		if active {
			QueueShowToast("Background operation started. Press Ctrl+Tab for Queue.", 4*time.Second)
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
		// Read here, on the goroutine that starts the finalizer rather than
		// inside it. The finalizer outlives this call, and a task cancelled
		// during shutdown is regularly still in flight when whatever comes
		// next reassigns vtui.FrameManager -- in the tests, the next test.
		frames := vtui.FrameManager
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
			qm.postTaskCompletionOn(complete, frames)
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
//
// Callers that are about to hand the task to a goroutine of their own use
// postTaskCompletionOn instead, so the callback lands on the frame manager
// their work was started against rather than on whichever one is current by
// the time they finish.
func (qm *OpQueueManager) postTaskCompletion(t *QueueTask) {
	qm.postTaskCompletionOn(t, vtui.FrameManager)
}

func (qm *OpQueueManager) postTaskCompletionOn(t *QueueTask, frames *vtui.FrameManagerType) {
	t.finalize()
	t.completionOnce.Do(func() {
		frames.PostTask(func() {
			if t.OnComplete != nil {
				t.OnComplete()
			}
			qm.RefreshUI()
		})
	})
}

func (qm *OpQueueManager) workerLoop() {
	qm.mu.Lock()
	if qm.wake == nil {
		qm.wake = make(chan struct{}, 1)
	}
	wake := qm.wake
	qm.mu.Unlock()

	for {
		select {
		case <-wake:
		case <-time.After(200 * time.Millisecond):
		}

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
	finalError := t.ErrorMsg
	t.mu.Unlock()

	qm.mu.Lock()
	for _, rk := range t.ResKeys {
		qm.activeKeys[rk] = false
	}
	qm.mu.Unlock()
	qm.signalWorker()

	vtui.DebugLog("QUEUE_DEBUG: Task %d finalized with state %s (Error: %v). Posting OnComplete.", t.ID, finalState, finalError)

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
		return theme.ThemedForeground(def, vtui.ColWarnHighlightBoxTitle)
	}
	if t.State == "Done" {
		return theme.ThemedForeground(def, vtui.ColDialogText)
	}
	if t.State == "Running" || t.State == "Scanning" {
		return theme.ThemedForeground(def, vtui.ColDialogHighlightText)
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
		BaseWindow: *vtui.NewBaseWindow(0, 2, scrW-1, scrH-1, " "+i18n.Msg("Queue.Title")+" "),
	}
	qf.ShowClose = true
	qf.ShowZoom = true
	qf.SetGrowMode(vtui.GrowHiX | vtui.GrowHiY)

	btnCancel := vtui.NewButton(0, 0, i18n.Msg("Queue.BtnCancel"))
	btnClear := vtui.NewButton(0, 0, i18n.Msg("Queue.BtnClear"))

	qf.table = vtui.NewTableWithButtons(&qf.BaseWindow, []vtui.TableColumn{
		{Title: i18n.Msg("Queue.ColID"), Width: 4},
		{Title: i18n.Msg("Queue.ColState"), Width: 10},
		{Title: i18n.Msg("Queue.ColType"), Width: 8},
		{Title: i18n.Msg("Queue.ColDescription"), MinWidth: 10},
		{Title: i18n.Msg("Queue.ColProgress"), Width: 24},
		{Title: i18n.Msg("Queue.ColSpeed"), Width: 12},
	}, btnCancel, btnClear)
	theme.UseTableColors(qf.table)
	qf.table.Sortable = true
	qf.table.QuickSearch = true
	qf.table.ShowScrollBar = true
	qf.table.OnAction = func(idx int) { qf.OpenTaskDetails(idx) }

	btnCancel.OnClick = func() {
		idx := qf.table.SelectPos
		if idx >= 0 && idx < len(qf.tasks) {
			t := qf.tasks[idx]
			t.mu.Lock()
			state := t.State
			t.mu.Unlock()
			if QueueTaskCancellable(state) {
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
			isDone := QueueTaskTerminal(t.State)
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

func (qf *QueueFrame) HandleCommand(cmd int, args any) bool {
	if HandleWorkspaceFork(cmd, args) {
		return true
	}
	return qf.BaseWindow.HandleCommand(cmd, args)
}

func (qf *QueueFrame) GetType() vtui.FrameType { return vtui.TypeUser }

func (qf *QueueFrame) OpenTaskDetails(idx int) {
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

func QueueHasActiveTasks() bool {
	manager := GlobalQueueManager
	if manager == nil {
		return false
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, task := range manager.tasks {
		if task == nil {
			continue
		}
		task.mu.Lock()
		active := queueTaskActive(task.State)
		task.mu.Unlock()
		if active {
			return true
		}
	}
	return false
}

func (qf *QueueFrame) VetoCloseWhileActive() bool {
	if !QueueHasActiveTasks() {
		return false
	}
	QueueShowToast("Cannot close queue while operations are active. Use Ctrl+Tab to switch.", 3*time.Second)
	return true
}

func (qf *QueueFrame) ProcessKey(e *vtinput.InputEvent) bool {
	ctrlW := e.KeyDown && e.VirtualKeyCode == vtinput.VK_W &&
		(e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed)) != 0
	if e.KeyDown && (e.VirtualKeyCode == vtinput.VK_ESCAPE || e.VirtualKeyCode == vtinput.VK_F10 || ctrlW) {
		if qf.VetoCloseWhileActive() {
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
			qf.OpenTaskDetails(idx)
			return true
		}
	}

	return false
}

// Status reads the fields a task changes as it runs. They are guarded by the
// task's own lock, so a caller outside this package cannot read them itself
// without the lock — and cannot take the lock without owning the invariant.
func (t *QueueTask) Status() (state string, id int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.State, t.ID, t.ErrorMsg
}

// SelectedTask returns the task the cursor is on, or nil when the table is
// empty or the cursor sits outside it.
func (qf *QueueFrame) SelectedTask() *QueueTask {
	index := qf.table.SelectPos
	if index < 0 || index >= len(qf.tasks) {
		return nil
	}
	return qf.tasks[index]
}

// SelectedIndex is the row the cursor is on, or -1 when there is no such row.
func (qf *QueueFrame) SelectedIndex() int {
	index := qf.table.SelectPos
	if index < 0 || index >= len(qf.tasks) {
		return -1
	}
	return index
}

// ClearFinished drops every task that has reached a terminal state and reports
// whether any went. The walk holds the manager's lock, which is why it lives
// here rather than at the caller that used to reach for that lock itself.
func (m *OpQueueManager) ClearFinished() bool {
	m.mu.Lock()
	active := make([]*QueueTask, 0, len(m.tasks))
	removed := false
	for _, task := range m.tasks {
		task.mu.Lock()
		terminal := QueueTaskTerminal(task.State)
		task.mu.Unlock()
		if terminal {
			removed = true
			continue
		}
		active = append(active, task)
	}
	m.tasks = active
	m.mu.Unlock()
	if removed {
		m.RefreshUI()
	}
	return removed
}

// NewQueueManagerWithTasks builds a manager already holding the given tasks,
// with no worker behind it. It exists for tests: the ordinary way in — Enqueue
// — starts the work as well as recording it, and the slice it records into is
// private, so a caller outside this package has no other way to set up a queue
// in a chosen state.
func NewQueueManagerWithTasks(tasks ...*QueueTask) *OpQueueManager {
	return &OpQueueManager{
		tasks:      append([]*QueueTask(nil), tasks...),
		activeKeys: make(map[string]bool),
		wake:       make(chan struct{}, 1),
	}
}

// SetState records the task's state under the task's own lock. Callers outside
// this package have to go through here: the field is read by the scheduler on
// another goroutine, and a write without the lock races it.
func (t *QueueTask) SetState(state string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.State = state
}

// NewQueueTaskWithCancel builds a task in a chosen state carrying a
// cancellation the caller owns. It exists for tests: the context and its
// cancel are private because the scheduler installs them when it starts the
// work, and a test that wants to observe a cancellation has to own it.
func NewQueueTaskWithCancel(id int, state string, ctx context.Context, cancel context.CancelFunc) *QueueTask {
	return &QueueTask{ID: id, State: state, ctx: ctx, cancel: cancel}
}

// Tasks copies the queue's task list. The slice itself is private and rewritten
// by the scheduler, so a caller reading it directly races that rewrite.
func (m *OpQueueManager) Tasks() []*QueueTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*QueueTask(nil), m.tasks...)
}

// SetTasks replaces the queue's task list and returns what it held. It exists
// for tests: the ordinary way in is Enqueue, which also starts the work, and a
// test that wants a queue in a chosen state has to put it back afterwards.
func (m *OpQueueManager) SetTasks(tasks []*QueueTask) []*QueueTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.tasks
	m.tasks = append([]*QueueTask(nil), tasks...)
	return previous
}
