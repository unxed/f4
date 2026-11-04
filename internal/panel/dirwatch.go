package panel

import (
	"sync"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dirwatch"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// dirWatchRetry is how long a change reported while the panel is still
// loading waits before the reload is attempted again.
const dirWatchRetry = 300 * time.Millisecond

// panelDirWatch keeps one dirwatch.Watcher on the directory a
// FileSystemPanel shows, so the listing refreshes when files appear, vanish
// or change there (f4#1668). It watches only a local OSVFS directory: an
// archive, a plugin or a remote VFS is never read as a local path.
type panelDirWatch struct {
	mu      sync.Mutex
	path    string
	watcher *dirwatch.Watcher
	opts    dirwatch.Options
	// post hands a task to the UI goroutine. The UI goroutine stores it on
	// every sync, and the watcher goroutine reads it under mu, so the watcher
	// never reads the global vtui.FrameManager itself: a test that swaps the
	// manager while a watcher is still alive would otherwise race with it.
	post func(func())
}

// setPoster records where watcher callbacks post their UI tasks. It must be
// called on the UI goroutine, the one that may read vtui.FrameManager.
func (pd *panelDirWatch) setPoster(post func(func())) {
	pd.mu.Lock()
	pd.post = post
	pd.mu.Unlock()
}

// postTask hands task to the UI goroutine; it is safe on a watcher goroutine.
func (pd *panelDirWatch) postTask(task func()) {
	pd.mu.Lock()
	post := pd.post
	pd.mu.Unlock()
	if post != nil {
		post(task)
	}
}

// uiTaskPoster returns the task poster of the current frame manager, or nil
// when there is none. Call it on the UI goroutine only.
func uiTaskPoster() func(func()) {
	frames := vtui.FrameManager
	if frames == nil {
		return nil
	}
	return frames.PostTask
}

// watchedPath returns the local directory fsp shows, or "" when there is
// none to watch: fsp is nil, its VFS is not the plain OS one, or watching is
// off in the configuration.
func watchedPath(fsp *FileSystemPanel) string {
	if fsp == nil || !config.App.WatchDirectories {
		return ""
	}
	osfs, ok := fsp.Vfs.(*vfs.OSVFS)
	if !ok || osfs == nil {
		return ""
	}
	return osfs.GetPath()
}

// sync starts, moves or stops the watch so that it follows fsp's current
// directory; it does nothing while the directory stays the same. It must be
// called on the UI goroutine.
func (pd *panelDirWatch) sync(fsp *FileSystemPanel, onChange func()) {
	want := watchedPath(fsp)
	pd.mu.Lock()
	defer pd.mu.Unlock()
	if want == pd.path && (want == "" || pd.watcher != nil) {
		return
	}
	pd.stopLocked()
	pd.path = want
	if want == "" {
		return
	}
	w, err := dirwatch.Watch(want, pd.opts, onChange)
	if err != nil {
		// Unreadable or vanished directory: the panel shows its own error;
		// try again when the path changes.
		return
	}
	pd.watcher = w
}

// stop ends the watch.
func (pd *panelDirWatch) stop() {
	pd.mu.Lock()
	defer pd.mu.Unlock()
	pd.stopLocked()
	pd.path = ""
}

func (pd *panelDirWatch) stopLocked() {
	if pd.watcher != nil {
		w := pd.watcher
		pd.watcher = nil
		// Close waits for a running callback, which only posts a task and so
		// never needs pd.mu.
		w.Close()
	}
}

// syncDirWatches makes each side's watcher follow its panel.
func (pf *PanelsFrame) syncDirWatches() {
	if pf.Closed {
		return
	}
	post := uiTaskPoster()
	for i := range pf.dirWatch {
		fsp, _ := pf.Panels[i].(*FileSystemPanel)
		i := i
		pf.dirWatch[i].setPoster(post)
		pf.dirWatch[i].sync(fsp, func() { pf.dirChanged(i) })
	}
}

// dirChanged runs on a watcher goroutine: it hands the reload over to the UI
// goroutine. It must not read vtui.FrameManager: see panelDirWatch.post.
func (pf *PanelsFrame) dirChanged(side int) {
	pf.dirWatch[side].postTask(func() { pf.reloadWatched(side) })
}

// reloadWatched re-reads the panel's directory if it is still the one that
// was watched. ReadDirectory keeps the cursor on the same name and the
// selection; a panel that is busy loading is retried a moment later instead
// of restarting its load over and over.
func (pf *PanelsFrame) reloadWatched(side int) {
	if pf.Closed {
		return
	}
	fsp, _ := pf.Panels[side].(*FileSystemPanel)
	if fsp == nil || watchedPath(fsp) == "" || watchedPath(fsp) != pf.dirWatch[side].currentPath() {
		return
	}
	if fsp.IsLoading {
		frames := vtui.FrameManager
		time.AfterFunc(dirWatchRetry, func() {
			if frames != nil {
				frames.PostTask(func() { pf.reloadWatched(side) })
			}
		})
		return
	}
	fsp.ReadDirectory()
}

func (pd *panelDirWatch) currentPath() string {
	pd.mu.Lock()
	defer pd.mu.Unlock()
	return pd.path
}
