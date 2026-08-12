package main

import (
	"context"
	"fmt"
	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFileEntry_GetCellText(t *testing.T) {
	// Mock entries
	file := &fileEntry{VFSItem: vfs.VFSItem{Name: "test.txt", Size: 1024, IsDir: false}}
	dir := &fileEntry{VFSItem: vfs.VFSItem{Name: "work", IsDir: true}}

	// 1. Column 0 (Name)
	if file.GetCellText(0) != "test.txt" {
		t.Errorf("File name mismatch: %s", file.GetCellText(0))
	}
	if dir.GetCellText(0) != "work" {
		t.Errorf("Dir name mismatch: %s", dir.GetCellText(0))
	}

	// 2. Column 1 (Size)
	if file.GetCellText(1) != "1 024" {
		t.Errorf("File size mismatch: %s", file.GetCellText(1))
	}

	// Regular directories should have an empty size column
	if dir.GetCellText(1) != "" {
		t.Errorf("Regular dir should have empty size column, got: %q", dir.GetCellText(1))
	}

	// Only ".." directory should have the UP-DIR placeholder
	upDir := &fileEntry{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}
	if upDir.GetCellText(1) != "UP-DIR" {
		t.Errorf("Parent dir (..) should have UP-DIR placeholder, got: %q", upDir.GetCellText(1))
	}

	// Cached rows are deliberately indistinguishable from fresh rows. The
	// loading spinner in the panel title is the sole visual refresh indicator.
	cachedFile := &fileEntry{VFSItem: vfs.VFSItem{Name: "cache.txt"}, IsCached: true}
	freshFile := &fileEntry{VFSItem: vfs.VFSItem{Name: "cache.txt"}}
	baseAttr := uint64(0x00AABBCC) // Mock color
	cachedAttr := cachedFile.GetCellAttr(0, baseAttr)
	freshAttr := freshFile.GetCellAttr(0, baseAttr)
	if cachedAttr != freshAttr {
		t.Errorf("cached attribute %#x differs from fresh attribute %#x", cachedAttr, freshAttr)
	}
}
func TestFileEntry_HighlightDir(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	oldRules := GlobalFileHighlighter.Rules
	defer func() { GlobalFileHighlighter.Rules = oldRules }()

	// Load default rules
	iniData := `[Highlight_0]
Name = Directories
IncludeAttributes = Directory
Mark = /
NormalColor = foreground:#FFFFFF
`
	ini := ParseIni(strings.NewReader(iniData))
	GlobalFileHighlighter.LoadFromIni(ini)

	dir := &fileEntry{VFSItem: vfs.VFSItem{Name: "work", IsDir: true}}

	// Column 0 should have the marker '/' prepended
	oldConfig := AppConfig
	defer func() { AppConfig = oldConfig }()

	// 1. By default, ShowDirPrefix is false, so no prefix should be shown
	AppConfig.ShowDirPrefix = false
	if got, want := dir.GetCellText(0), "work"; got != want {
		t.Errorf("Expected dir name without prefix, got %q, want %q", got, want)
	}

	// 2. When ShowDirPrefix is true, the marker '/' should be prepended (no space)
	AppConfig.ShowDirPrefix = true
	if got, want := dir.GetCellText(0), "/work"; got != want {
		t.Errorf("Expected dir name with '/' prefix, got %q, want %q", got, want)
	}

	// Color should match ColPanelText (since foreground:#FFFFFF resolves to truecolor or index, but we just want a non-zero attribute)
	if attr := dir.GetCellAttr(0, 0); attr == 0 {
		t.Error("Expected highlighted attribute for directory")
	}
}

func TestFileSystemPanel_FocusLoss_FastFind(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(t.TempDir()))
	fp.fastFindMode = true
	fp.fastFindStr = "test"

	// Имитируем потерю фокуса файловой панелью (например, открыто меню)
	fp.SetFocus(false)

	if fp.fastFindMode || fp.fastFindStr != "" {
		t.Error("Focus loss should deactivate FastFind mode")
	}
}

type mockTitleVFS struct {
	vfs.OSVFS
	title      string
	panelTitle string
}

func (m *mockTitleVFS) GetTitle() string         { return m.title }
func (m *mockTitleVFS) PanelTitle(string) string { return m.panelTitle }

type mockCacheSessionVFS struct {
	*mockTitleVFS
	session any
}

func (m *mockCacheSessionVFS) SessionKey() any { return m.session }

type mockStableCacheVFS struct {
	*mockCacheSessionVFS
	stable any
}

func (m *mockStableCacheVFS) DirectoryCacheKey() any { return m.stable }

type panelStatCountingVFS struct {
	*vfs.NullVFS
	currentPath string
	parent      vfs.VFS
	stats       map[string]vfs.VFSItem
	statCalls   atomic.Int64
}

// stagedPanelVFS lets cache-transition tests hold ReadDir between chunks while
// the panel remains interactive. The release/delivered channels are paired by
// chunk index.
type stagedPanelVFS struct {
	*vfs.NullVFS
	parent    vfs.VFS
	chunks    [][]vfs.VFSItem
	started   chan struct{}
	release   []chan struct{}
	delivered []chan struct{}
}

type queuedNavigationVFS struct {
	*vfs.NullVFS
	pathMu             sync.RWMutex
	currentPath        string
	checkedPathCalls   atomic.Int64
	optimisticCalls    atomic.Int64
	readDirCalls       atomic.Int64
	firstReadStarted   chan struct{}
	releaseFirstRead   chan struct{}
	firstReadStartOnce sync.Once
	items              map[string][]vfs.VFSItem
}

// absoluteRecoveryVFS models AFC's path contract: panel navigation may set an
// absolute path optimistically, but a bare ".." is rejected. SystemData is
// visible in the container root while iOS denies listing its contents.
type absoluteRecoveryVFS struct {
	*vfs.NullVFS
	pathMu       sync.RWMutex
	currentPath  string
	readDirCalls atomic.Int64
}

func newAbsoluteRecoveryVFS() *absoluteRecoveryVFS {
	return &absoluteRecoveryVFS{NullVFS: vfs.NewNullVFS(0), currentPath: "/SystemData"}
}

func (v *absoluteRecoveryVFS) GetPath() string {
	v.pathMu.RLock()
	defer v.pathMu.RUnlock()
	return v.currentPath
}

func (v *absoluteRecoveryVFS) IsAtRoot() bool { return v.GetPath() == "/" }
func (*absoluteRecoveryVFS) Dir(p string) string {
	return path.Dir(p)
}
func (*absoluteRecoveryVFS) Base(p string) string {
	return path.Base(p)
}
func (*absoluteRecoveryVFS) Join(elem ...string) string {
	return path.Join(elem...)
}
func (v *absoluteRecoveryVFS) SetPath(p string) error { return v.SetPathOptimistic(p) }
func (v *absoluteRecoveryVFS) SetPathOptimistic(p string) error {
	if !path.IsAbs(p) {
		return fmt.Errorf("absolute path required: %q", p)
	}
	v.pathMu.Lock()
	v.currentPath = path.Clean(p)
	v.pathMu.Unlock()
	return nil
}
func (*absoluteRecoveryVFS) Stat(context.Context, string) (vfs.VFSItem, error) {
	return vfs.VFSItem{Name: "/", IsDir: true}, nil
}
func (v *absoluteRecoveryVFS) ReadDir(ctx context.Context, p string, onChunk func([]vfs.VFSItem)) error {
	v.readDirCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return err
	}
	if path.Clean(p) == "/SystemData" {
		return fmt.Errorf("read dir /SystemData: AFC status 10: %w", os.ErrPermission)
	}
	if onChunk != nil {
		onChunk([]vfs.VFSItem{{Name: "SystemData", IsDir: true}})
	}
	return nil
}

const providerTestRoot = "provider-test://"

type blockingProviderManagerVFS struct {
	*vfs.NullVFS
	rowName                string
	setPathCalls           atomic.Int64
	blockNextRead          atomic.Bool
	blockedReadStarted     chan struct{}
	releaseBlockedRead     chan struct{}
	blockedReadStartedOnce sync.Once
}

func newBlockingProviderManagerVFS(rowName string) *blockingProviderManagerVFS {
	return &blockingProviderManagerVFS{
		NullVFS:            vfs.NewNullVFS(0),
		rowName:            rowName,
		blockedReadStarted: make(chan struct{}),
		releaseBlockedRead: make(chan struct{}),
	}
}

func (m *blockingProviderManagerVFS) GetPath() string  { return providerTestRoot }
func (m *blockingProviderManagerVFS) GetTitle() string { return "Provider test" }
func (m *blockingProviderManagerVFS) IsAtRoot() bool   { return true }
func (m *blockingProviderManagerVFS) Join(elem ...string) string {
	if len(elem) == 0 {
		return ""
	}
	return providerTestRoot + strings.TrimPrefix(elem[len(elem)-1], "/")
}
func (m *blockingProviderManagerVFS) SetPath(p string) error {
	m.setPathCalls.Add(1)
	return fmt.Errorf("provider test manager has no directory %q: %w", p, os.ErrNotExist)
}
func (m *blockingProviderManagerVFS) ReadDir(ctx context.Context, _ string, onChunk func([]vfs.VFSItem)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.blockNextRead.CompareAndSwap(true, false) {
		m.blockedReadStartedOnce.Do(func() { close(m.blockedReadStarted) })
		select {
		case <-m.releaseBlockedRead:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if onChunk != nil {
		onChunk([]vfs.VFSItem{{Name: m.rowName, IsDir: true}})
	}
	return nil
}

type trackedMountedVFS struct {
	*vfs.NullVFS
	parent     vfs.VFS
	closeCalls atomic.Int64
}

func (m *trackedMountedVFS) ParentVFS() vfs.VFS { return m.parent }
func (m *trackedMountedVFS) Close() error {
	m.closeCalls.Add(1)
	return nil
}

type blockingMountProvider struct {
	source      *blockingProviderManagerVFS
	result      *trackedMountedVFS
	virtualDirs bool
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	openCalls   atomic.Int64
}

func newBlockingMountProvider(source *blockingProviderManagerVFS) *blockingMountProvider {
	return &blockingMountProvider{
		source: source, virtualDirs: true,
		result:  &trackedMountedVFS{NullVFS: vfs.NewNullVFS(0), parent: source},
		started: make(chan struct{}), release: make(chan struct{}),
	}
}

func (*blockingMountProvider) Name() string                    { return "blocking-provider-test" }
func (*blockingMountProvider) Priority() int                   { return 10000 }
func (p *blockingMountProvider) OpensVirtualDirectories() bool { return p.virtualDirs }
func (p *blockingMountProvider) CanOpen(_ context.Context, parent vfs.VFS, target string) bool {
	return parent == p.source && target == providerTestRoot+p.source.rowName
}
func (p *blockingMountProvider) Open(_ context.Context, parent vfs.VFS, _ string) (vfs.VFS, error) {
	p.openCalls.Add(1)
	p.startedOnce.Do(func() { close(p.started) })
	<-p.release // deliberately models a provider that completes after cancellation
	p.result.parent = parent
	return p.result, nil
}

func newQueuedNavigationVFS() *queuedNavigationVFS {
	return &queuedNavigationVFS{
		NullVFS:          vfs.NewNullVFS(0),
		currentPath:      "/",
		firstReadStarted: make(chan struct{}),
		releaseFirstRead: make(chan struct{}, 1),
		items: map[string][]vfs.VFSItem{
			"/":       {{Name: "sdcard", IsDir: true}},
			"/sdcard": {{Name: "photo.jpg"}},
		},
	}
}

func (q *queuedNavigationVFS) GetPath() string {
	q.pathMu.RLock()
	defer q.pathMu.RUnlock()
	return q.currentPath
}

func (q *queuedNavigationVFS) IsAtRoot() bool { return q.GetPath() == "/" }

func (q *queuedNavigationVFS) SetPath(p string) error {
	q.checkedPathCalls.Add(1)
	time.Sleep(300 * time.Millisecond)
	return q.SetPathOptimistic(p)
}

func (q *queuedNavigationVFS) SetPathOptimistic(p string) error {
	q.optimisticCalls.Add(1)
	if !path.IsAbs(p) {
		p = path.Join(q.GetPath(), p)
	}
	q.pathMu.Lock()
	q.currentPath = path.Clean(p)
	q.pathMu.Unlock()
	return nil
}

func (q *queuedNavigationVFS) Stat(context.Context, string) (vfs.VFSItem, error) {
	return vfs.VFSItem{Name: "/", IsDir: true}, nil
}

func (q *queuedNavigationVFS) ReadDir(ctx context.Context, p string, onChunk func([]vfs.VFSItem)) error {
	call := q.readDirCalls.Add(1)
	if call == 1 {
		q.firstReadStartOnce.Do(func() { close(q.firstReadStarted) })
		select {
		case <-q.releaseFirstRead:
		case <-ctx.Done():
			// Deliberately remain active until the test releases us. This models
			// a FISH response that has to reach its terminator after cancellation.
			<-q.releaseFirstRead
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if onChunk != nil {
		onChunk(q.items[path.Clean(p)])
	}
	return nil
}

func newStagedPanelVFS(chunks ...[]vfs.VFSItem) *stagedPanelVFS {
	s := &stagedPanelVFS{
		NullVFS:   vfs.NewNullVFS(0),
		parent:    vfs.NewNullVFS(0),
		chunks:    chunks,
		started:   make(chan struct{}),
		release:   make([]chan struct{}, len(chunks)),
		delivered: make([]chan struct{}, len(chunks)),
	}
	for i := range chunks {
		s.release[i] = make(chan struct{}, 1)
		s.delivered[i] = make(chan struct{})
	}
	return s
}

func (s *stagedPanelVFS) GetPath() string     { return "/" }
func (s *stagedPanelVFS) IsAtRoot() bool      { return true }
func (s *stagedPanelVFS) ParentVFS() vfs.VFS  { return s.parent }
func (s *stagedPanelVFS) Dir(p string) string { return path.Dir(p) }
func (s *stagedPanelVFS) GetTitle() string    { return "staged-device" }
func (s *stagedPanelVFS) Stat(context.Context, string) (vfs.VFSItem, error) {
	return vfs.VFSItem{Name: "/", IsDir: true}, nil
}
func (s *stagedPanelVFS) ReadDir(ctx context.Context, _ string, onChunk func([]vfs.VFSItem)) error {
	close(s.started)
	for i, chunk := range s.chunks {
		select {
		case <-s.release[i]:
		case <-ctx.Done():
			return ctx.Err()
		}
		if onChunk != nil {
			onChunk(chunk)
		}
		close(s.delivered[i])
	}
	return nil
}

func (m *panelStatCountingVFS) GetPath() string { return m.currentPath }
func (m *panelStatCountingVFS) IsAtRoot() bool  { return m.currentPath == "/" }
func (m *panelStatCountingVFS) Dir(p string) string {
	return path.Dir(p)
}
func (m *panelStatCountingVFS) ParentVFS() vfs.VFS { return m.parent }
func (m *panelStatCountingVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	m.statCalls.Add(1)
	if item, ok := m.stats[p]; ok {
		return item, nil
	}
	return m.NullVFS.Stat(ctx, p)
}

func TestFileSystemPanel_ReadDirectoryReusesRootStatForUpEntry(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	oldSyncPanelLoad := AppConfig.SyncPanelLoad
	AppConfig.SyncPanelLoad = true
	defer func() { AppConfig.SyncPanelLoad = oldSyncPanelLoad }()

	want := vfs.VFSItem{
		Name:     "/",
		IsDir:    true,
		MTime:    time.Unix(100, 0),
		ATime:    time.Unix(200, 0),
		CTime:    time.Unix(300, 0),
		UnixMode: 0751,
		Uid:      1000,
		Gid:      1001,
	}
	mounted := &panelStatCountingVFS{
		NullVFS:     vfs.NewNullVFS(0),
		currentPath: "/",
		parent:      vfs.NewNullVFS(0),
		stats:       map[string]vfs.VFSItem{"/": want},
	}

	fp := NewFileSystemPanel(0, 0, 40, 20, mounted)
	waitForLoad(t, fp)

	if got := mounted.statCalls.Load(); got != 1 {
		t.Fatalf("Stat calls for mounted root = %d, want 1", got)
	}
	if len(fp.entries) == 0 || fp.entries[0].Name != ".." {
		t.Fatalf("mounted root has no up entry: %+v", fp.entries)
	}
	up := fp.entries[0].VFSItem
	if !up.MTime.Equal(want.MTime) || !up.ATime.Equal(want.ATime) || !up.CTime.Equal(want.CTime) ||
		up.UnixMode != want.UnixMode || up.Uid != want.Uid || up.Gid != want.Gid {
		t.Fatalf("up entry metadata = %+v, want metadata from %+v", up, want)
	}
	if !fp.lastDirMTime.Equal(want.MTime) {
		t.Fatalf("lastDirMTime = %v, want %v", fp.lastDirMTime, want.MTime)
	}
}

func TestFileSystemPanel_ReadDirectoryStatsDistinctParent(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	oldSyncPanelLoad := AppConfig.SyncPanelLoad
	AppConfig.SyncPanelLoad = true
	defer func() { AppConfig.SyncPanelLoad = oldSyncPanelLoad }()

	dirMTime := time.Unix(100, 0)
	parentMTime := time.Unix(200, 0)
	mounted := &panelStatCountingVFS{
		NullVFS:     vfs.NewNullVFS(0),
		currentPath: "/child",
		stats: map[string]vfs.VFSItem{
			"/child": {Name: "child", IsDir: true, MTime: dirMTime, UnixMode: 0700},
			"/":      {Name: "/", IsDir: true, MTime: parentMTime, UnixMode: 0755},
		},
	}

	fp := NewFileSystemPanel(0, 0, 40, 20, mounted)
	waitForLoad(t, fp)

	if got := mounted.statCalls.Load(); got != 2 {
		t.Fatalf("Stat calls for child and distinct parent = %d, want 2", got)
	}
	if len(fp.entries) == 0 || fp.entries[0].Name != ".." {
		t.Fatalf("child directory has no up entry: %+v", fp.entries)
	}
	up := fp.entries[0].VFSItem
	if !up.MTime.Equal(parentMTime) || up.UnixMode != 0755 {
		t.Fatalf("up entry metadata = %+v, want distinct parent metadata", up)
	}
	if !fp.lastDirMTime.Equal(dirMTime) {
		t.Fatalf("lastDirMTime = %v, want current directory time %v", fp.lastDirMTime, dirMTime)
	}
}

func TestFileSystemPanel_UpdateTitle_WithProvider(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	v := &mockTitleVFS{OSVFS: *vfs.NewOSVFS("/tmp"), title: "user@host"}
	fp := NewFileSystemPanel(0, 0, 40, 20, v)

	// Reset loading flag to avoid the spinner suffix.
	fp.isLoading = false
	fp.updateTitle(nil)

	got := fp.currentTitle
	if !strings.Contains(got, "user@host:") {
		t.Errorf("Expected title to contain 'user@host:', got %q", got)
	}

	v.panelTitle = `Pixel:\sdcard\Download`
	fp.updateTitle(nil)
	if got := fp.currentTitle; got != `Pixel:\sdcard\Download` {
		t.Errorf("custom panel title = %q", got)
	}
}

func TestPanelLoadingSpinnerFramesOccupyOneCell(t *testing.T) {
	for _, frame := range panelLoadingPulse {
		if width := runewidth.StringWidth(frame); width != 1 {
			t.Fatalf("spinner frame %q occupies %d cells, want 1", frame, width)
		}
	}
}

func TestFileSystemPanel_ShowHiddenFiles(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// Protect global config from leakage
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()

	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "normal.txt"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmp, ".hidden.txt"), []byte(""), 0644)

	v := vfs.NewOSVFS(tmp)

	// 1. Show hidden files
	AppConfig.ShowHiddenFiles = true
	fp1 := NewFileSystemPanel(0, 0, 80, 24, v)
	waitForLoad(t, fp1)

	foundHidden := false
	for _, e := range fp1.entries {
		if e.Name == ".hidden.txt" {
			foundHidden = true
			break
		}
	}
	if !foundHidden {
		t.Error("Hidden file should be visible when ShowHiddenFiles is true")
	}

	// 2. Hide hidden files
	AppConfig.ShowHiddenFiles = false
	fp2 := NewFileSystemPanel(0, 0, 80, 24, v)
	waitForLoad(t, fp2)

	foundHidden = false
	for _, e := range fp2.entries {
		if e.Name == ".hidden.txt" {
			foundHidden = true
			break
		}
	}
	if foundHidden {
		t.Error("Hidden file should NOT be visible when ShowHiddenFiles is false")
	}
}

func TestFileSystemPanel_NavigateUp_Selection(t *testing.T) {
	vtui.SetDefaultPalette()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmp := t.TempDir()
	sub := filepath.Join(tmp, "target_folder")
	os.Mkdir(sub, 0755)
	os.WriteFile(filepath.Join(tmp, "other.txt"), []byte(""), 0644)

	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(sub))

	// Drain tasks to finish loading the initial directory
	timeout := time.After(1 * time.Second)
	for fp.isLoading {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for initial load")
		}
	}
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			goto done1
		}
	}
done1:

	// Simulate pressing Enter on ".."
	fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}}
	fp.SetCursorIndex(0)
	fp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})

	// Wait for the parent directory to finish loading
	timeout = time.After(1 * time.Second)
	for fp.isLoading {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for parent load")
		}
	}

	// Pump any remaining UI rendering/selection tasks
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			goto done2
		}
	}
done2:

	// Ensure that after returning to the parent directory, the cursor is on the folder we just exited
	if fp.GetSelectedName() != "target_folder" {
		t.Errorf("Expected cursor to land on 'target_folder', got %q", fp.GetSelectedName())
	}
}
func TestFileSystemPanel_SelectedInfo(t *testing.T) {
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.ShowPanelFileInfo = true

	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(t.TempDir()))
	if fp.cancelLoad != nil {
		fp.cancelLoad()
	}
	fp.isLoading = false
	if fp.loadingTimer != nil {
		fp.loadingTimer.Stop()
	}

	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "file1.txt", Size: 1234567, IsDir: false}, Selected: true},
		{VFSItem: vfs.VFSItem{Name: "folder1", IsDir: true}, Selected: true},
		{VFSItem: vfs.VFSItem{Name: "file2.txt", Size: 50, IsDir: false}, Selected: false},
	}
	fp.Refresh()

	fp.Show(scr)

	// Verify that the color of the bottom bar is ColPanelSelectedInfo when items are selected
	cell := scr.GetCell(40, 23)
	if cell.Attributes != vtui.Palette[ColPanelSelectedInfo] {
		t.Errorf("Expected Selected Info color %X, got %X", vtui.Palette[ColPanelSelectedInfo], cell.Attributes)
	}

	var sb strings.Builder
	for x := 0; x < 80; x++ {
		cell := scr.GetCell(x, 23)
		if cell.Char != 0 && cell.Char != ' ' {
			sb.WriteRune(rune(cell.Char))
		}
	}

	result := sb.String()
	expectedBytes := "1234567"
	if !strings.Contains(result, "Bytes:") || !strings.Contains(result, expectedBytes) {
		t.Errorf("Expected bottom bar to contain formatted bytes %q, got: %q", expectedBytes, result)
	}
	if !strings.Contains(result, "files:1") {
		t.Errorf("Expected bottom bar to contain 'files:1', got: %q", result)
	}
	if !strings.Contains(result, "folders:1") {
		t.Errorf("Expected bottom bar to contain 'folders:1', got: %q", result)
	}

	// Hiding the separate file-information line must not hide the selection
	// summary drawn directly on the panel's bottom border.
	AppConfig.ShowPanelFileInfo = false
	fp.SetPosition(0, 0, 79, 23)
	fp.Show(scr)
	if cell = scr.GetCell(40, 23); cell.Attributes != vtui.Palette[ColPanelSelectedInfo] {
		t.Errorf("selected summary disappeared with file info hidden: got color %X", cell.Attributes)
	}
	if cell = scr.GetCell(0, 21); cell.Char == '├' {
		t.Error("file-information separator remained visible when the option was disabled")
	}

	// Clear selection to check ColPanelTotalInfo
	for _, e := range fp.entries {
		e.Selected = false
	}
	fp.selectedItems = make(map[string]bool)
	fp.Refresh()
	fp.Show(scr)

	cell = scr.GetCell(40, 23)
	if cell.Attributes != vtui.Palette[ColPanelTotalInfo] {
		t.Errorf("Expected Total Info color %X, got %X", vtui.Palette[ColPanelTotalInfo], cell.Attributes)
	}
}

func TestFileSystemPanel_HiddenInfoShowsCursorFileSizeOnMulticolumnBorder(t *testing.T) {
	oldCfg := AppConfig
	oldColor := vtui.Palette[ColPanelText]
	defer func() {
		AppConfig = oldCfg
		vtui.Palette[ColPanelText] = oldColor
	}()
	AppConfig.ShowPanelFileInfo = false

	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 12)
	vtui.FrameManager.Init(scr)

	fp := NewFileSystemPanel(0, 0, 80, 12, vfs.NewOSVFS(t.TempDir()))
	if fp.cancelLoad != nil {
		fp.cancelLoad()
	}
	fp.isLoading = false
	if fp.loadingTimer != nil {
		fp.loadingTimer.Stop()
	}
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "file.bin", Size: 1234567}},
		{VFSItem: vfs.VFSItem{Name: "other.bin", Size: 10}},
	}
	fp.SetViewMode(ViewModeMedium)
	fp.SetCursorIndex(0)
	rowText := func(x, y, width int) string {
		runes := make([]rune, width)
		for i := range runes {
			cell := scr.GetCell(x+i, y)
			runes[i] = rune(cell.Char)
			if runes[i] == 0 {
				runes[i] = ' '
			}
		}
		return string(runes)
	}

	const themedAttr uint64 = 0x123456789ABCDEF0
	vtui.Palette[ColPanelText] = themedAttr
	fp.Show(scr)

	if got := rowText(1, fp.Y2, 13); got != " ▸ 1 234 567 " {
		t.Fatalf("medium bottom-left cursor size = %q, want %q", got, " ▸ 1 234 567 ")
	}
	if got := scr.GetCell(2, fp.Y2).Attributes; got != themedAttr {
		t.Fatalf("cursor size color = %#x, want current themed Panel.Text %#x", got, themedAttr)
	}

	// Brief is the other multicolumn mode and must expose the same compact
	// status, while Detailed already has a visible Size column and must not.
	fp.SetViewMode(ViewModeBrief)
	fp.Show(scr)
	if got := rowText(1, fp.Y2, 13); got != " ▸ 1 234 567 " {
		t.Fatalf("brief bottom-left cursor size = %q, want %q", got, " ▸ 1 234 567 ")
	}

	fp.SetViewMode(ViewModeDetailed)
	fp.Show(scr)
	if got := rowText(1, fp.Y2, 13); strings.Contains(got, "1 234 567") {
		t.Fatalf("detailed mode unexpectedly duplicated cursor size on bottom border: %q", got)
	}

	fp.SetViewMode(ViewModeMedium)
	AppConfig.ShowPanelFileInfo = true
	fp.Show(scr)
	if got := rowText(1, fp.Y2, 13); strings.Contains(got, "1 234 567") {
		t.Fatalf("visible file-info row unexpectedly duplicated cursor size on bottom border: %q", got)
	}
}

func TestFileSystemPanel_Initialization(t *testing.T) {
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.ShowPanelFileInfo = false

	if ViewModeMedium != 0 || ViewModeDetailed != 1 {
		t.Fatalf("legacy view mode values changed: Medium=%d Detailed=%d", ViewModeMedium, ViewModeDetailed)
	}
	// Verify that NewFileSystemPanel initializes with valid geometry to prevent collapsed panels
	x, y, w, h := 10, 5, 40, 20
	fp := NewFileSystemPanel(x, y, w, h, vfs.NewOSVFS("."))

	if fp.X1 != x || fp.Y1 != y || fp.X2 != x+w-1 || fp.Y2 != y+h-1 {
		t.Errorf("Panel coordinates not initialized correctly: got (%d,%d)-(%d,%d)", fp.X1, fp.Y1, fp.X2, fp.Y2)
	}

	// Internal table must match panel interior (excluding borders)
	tx1, ty1, tx2, ty2 := fp.table.GetPosition()
	expectedTy2 := y + h - 2
	if tx1 != x+1 || ty1 != y+1 || tx2 != x+w-2 || ty2 != expectedTy2 {
		t.Errorf("Internal table coordinates mismatch: got (%d,%d)-(%d,%d)", tx1, ty1, tx2, ty2)
	}

	AppConfig.ShowPanelFileInfo = true
	fp.SetPosition(x, y, x+w-1, y+h-1)
	_, _, _, ty2 = fp.table.GetPosition()
	if expected := y + h - 4; ty2 != expected {
		t.Errorf("table bottom with status info = %d, want %d", ty2, expected)
	}

	if fp.viewMode != ViewModeMedium {
		t.Errorf("Default view mode should be Medium, got %v", fp.viewMode)
	}

	if !fp.table.CellSelection {
		t.Error("Medium mode should have CellSelection enabled on the table")
	}
}
func TestMediumRow_GetCellText(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 80, 12, vfs.NewOSVFS("."))
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "test.txt", IsDir: false}},
		{VFSItem: vfs.VFSItem{Name: "work", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
	}
	fp.SetViewMode(ViewModeMedium)

	mRow := &mediumRow{fp: fp, r: 0}

	if mRow.GetCellText(0) != "test.txt" {
		t.Errorf("Expected 'test.txt', got %q", mRow.GetCellText(0))
	}
	if mRow.GetCellText(1) != "" {
		t.Errorf("Out of bounds should be empty")
	}

	columnHeight := fp.table.ViewHeight
	fp.entries = make([]*fileEntry, columnHeight*2)
	for i := range fp.entries {
		fp.entries[i] = &fileEntry{VFSItem: vfs.VFSItem{Name: "f"}}
	}
	fp.entries[0].Name = "Left"
	fp.entries[columnHeight].Name = "Right"
	mRow = &mediumRow{fp: fp, r: 0}
	if mRow.GetCellText(0) != "Left" {
		t.Errorf("Expected 'Left', got %q", mRow.GetCellText(0))
	}
	if mRow.GetCellText(1) != "Right" {
		t.Errorf("Expected 'Right', got %q", mRow.GetCellText(1))
	}
}

func TestBriefRowAndColumns(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 90, 12, vfs.NewOSVFS("."))
	fp.entries = make([]*fileEntry, 20)
	for i := range fp.entries {
		fp.entries[i] = &fileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("file-%d", i)}}
	}
	fp.SetViewMode(ViewModeBrief)
	if len(fp.table.Columns) != 3 || !fp.table.CellSelection {
		t.Fatalf("Brief layout has %d columns, CellSelection=%v", len(fp.table.Columns), fp.table.CellSelection)
	}
	row := &mediumRow{fp: fp, r: 0}
	h := fp.table.ViewHeight
	for col := 0; col < 3; col++ {
		want := fmt.Sprintf("file-%d", col*h)
		if got := row.GetCellText(col); got != want {
			t.Errorf("column %d = %q, want %q", col, got, want)
		}
	}
	fp.SetCursorIndex(2*h + 1)
	if fp.table.SelectCol != 2 || fp.table.SelectPos != 1 {
		t.Errorf("Brief cursor mapping: pos=%d col=%d, want pos=1 col=2", fp.table.SelectPos, fp.table.SelectCol)
	}
}

func TestFileEntryModifiedCell(t *testing.T) {
	entry := &fileEntry{VFSItem: vfs.VFSItem{MTime: time.Date(2026, 8, 4, 12, 34, 0, 0, time.Local)}}
	if got := entry.GetCellText(2); got != "04.08.26 12:34" {
		t.Fatalf("modified cell = %q", got)
	}
	entry.MTime = time.Time{}
	if got := entry.GetCellText(2); got != "" {
		t.Fatalf("zero modified cell = %q, want empty", got)
	}
}

func TestFormatPanelFileNameSeparateExtension(t *testing.T) {
	oldConfig := AppConfig
	defer func() { AppConfig = oldConfig }()
	AppConfig.SeparateFileExtensions = true
	AppConfig.ShowDirPrefix = false

	entry := &fileEntry{VFSItem: vfs.VFSItem{Name: "report.txt"}}
	if got, want := formatPanelFileName(entry, 20), "report           txt"; got != want {
		t.Fatalf("separate extension = %q, want %q", got, want)
	}
	entry.Name = "source.go"
	if got, want := formatPanelFileName(entry, 20), "source           go "; got != want {
		t.Fatalf("short extension = %q, want %q", got, want)
	}
	entry.Name = "archive.longext"
	if got, want := formatPanelFileName(entry, 20), "archive      longext"; got != want {
		t.Fatalf("long extension = %q, want %q", got, want)
	}
	if got := runewidth.StringWidth(formatPanelFileName(entry, 20)); got != 20 {
		t.Fatalf("formatted width = %d, want 20", got)
	}

	for _, name := range []string{"README", ".gitignore", "trailing."} {
		entry.Name = name
		if got := formatPanelFileName(entry, 20); got != name {
			t.Errorf("name %q unexpectedly split: %q", name, got)
		}
	}

	entry.Name = "folder.ext"
	entry.IsDir = true
	if got := formatPanelFileName(entry, 20); got != "folder.ext" {
		t.Fatalf("directory extension was separated: %q", got)
	}

	entry = &fileEntry{VFSItem: vfs.VFSItem{
		Name:        "V2454A (192.168.1.100:38477)",
		NoExtension: true,
	}}
	if got := formatPanelFileName(entry, 40); got != entry.Name {
		t.Fatalf("extensionless virtual row was split: %q", got)
	}
}

func TestSeparateExtensionAppliesToEveryViewMode(t *testing.T) {
	oldConfig := AppConfig
	defer func() { AppConfig = oldConfig }()
	AppConfig.SeparateFileExtensions = true

	fp := NewFileSystemPanel(0, 0, 90, 12, vfs.NewOSVFS("."))
	fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "sample.go"}}}
	for _, mode := range []ViewMode{ViewModeBrief, ViewModeMedium, ViewModeDetailed, ViewModeWide} {
		fp.SetViewMode(mode)
		fp.Refresh()
		text := fp.table.Rows[0].GetCellText(0)
		if !strings.HasPrefix(text, "sample") || !strings.HasSuffix(text, "go ") {
			t.Errorf("mode %v did not separate extension: %q", mode, text)
		}
		if got, want := runewidth.StringWidth(text), fp.table.Columns[0].Width; got != want {
			t.Errorf("mode %v formatted width=%d, want %d", mode, got, want)
		}
	}
}

func TestFileSystemPanel_InfoLineRendering(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(t.TempDir()))
	// Force sync items for deterministic state
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "test.txt", Size: 1024}},
	}
	fp.Refresh()
	fp.SetCursorIndex(1)

	// Simply calling Show() validates that the string truncations and layouts
	// don't panic on normal, short or extreme configurations.
	fp.Show(scr)
}
func TestFileSystemPanel_CursorMapping(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 80, 12, vfs.NewOSVFS("."))

	// Simulate 20 items manually so Refresh() doesn't wipe them
	fp.entries = make([]*fileEntry, 20)
	for i := range fp.entries {
		fp.entries[i] = &fileEntry{VFSItem: vfs.VFSItem{Name: "file"}}
	}

	// 1. Medium Mode (Column-Major)
	fp.SetViewMode(ViewModeMedium)
	fp.Refresh()
	fp.SetCursorIndex(3) // Index 3: Row 3, Col 0
	if fp.table.SelectPos != 3 || fp.table.SelectCol != 0 {
		t.Errorf("Medium mapping index 3: expected pos 3 col 0, got pos %d col %d", fp.table.SelectPos, fp.table.SelectCol)
	}

	columnHeight := fp.table.ViewHeight
	fp.SetCursorIndex(columnHeight + 3)
	if fp.table.SelectPos != 3 || fp.table.SelectCol != 1 {
		t.Errorf("Medium mapping index %d: expected pos 3 col 1, got pos %d col %d", columnHeight+3, fp.table.SelectPos, fp.table.SelectCol)
	}

	// 2. Detailed Mode
	fp.SetViewMode(ViewModeDetailed)
	fp.Refresh()
	fp.SetCursorIndex(5)
	if fp.table.SelectPos != 5 || fp.table.SelectCol != 0 {
		t.Errorf("Detailed mapping failed: expected pos 5, got %d", fp.table.SelectPos)
	}
}

func TestFileSystemPanel_SelectName(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS("."))
	fp.SetViewMode(ViewModeDetailed)

	// Mock entries
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "a_folder", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "z_folder", IsDir: true}},
	}

	fp.SelectName("z_folder")

	if fp.table.SelectPos != 2 {
		t.Errorf("SelectName failed: expected index 2, got %d", fp.table.SelectPos)
	}

	// Should not change position if name not found
	fp.SelectName("non_existent")
	if fp.table.SelectPos != 2 {
		t.Errorf("SelectName should not change position on failure, got %d", fp.table.SelectPos)
	}
}

func TestFileSystemPanel_MultiSelect(t *testing.T) {
	// 1. Setup real TempDir with files
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "file1.txt"), []byte("1"), 0644)
	os.WriteFile(filepath.Join(tmp, "file2.txt"), []byte("2"), 0644)
	os.WriteFile(filepath.Join(tmp, "file3.txt"), []byte("3"), 0644)

	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(tmp))
	fp.viewMode = ViewModeDetailed

	// Bypass async ReadDirectory for precise testing
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "file1.txt"}},
		{VFSItem: vfs.VFSItem{Name: "file2.txt"}},
		{VFSItem: vfs.VFSItem{Name: "file3.txt"}},
	}
	fp.Refresh()

	// 2. Select file1.txt (Index 1)
	fp.SetCursorIndex(1)
	fp.Refresh()

	// Press Insert
	fp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_INSERT})

	if !fp.entries[1].Selected {
		t.Error("file1.txt (index 1) should be selected after Insert")
	}

	// Cursor should move to file2.txt (Index 2)
	if fp.GetCursorIndex() != 2 {
		t.Errorf("Cursor should move to 2, got %d", fp.GetCursorIndex())
	}

	// 3. Shift+Down at cursor=2 (file2.txt).
	//    Single-step Up/Down keep FAR's per-tap semantics: only the
	//    starting row is toggled; the cursor moves to the next row
	//    but that row is left alone (the next tap decides what to
	//    do with it). Range-paint is reserved for multi-step jumps
	//    (Home/End/PgUp/PgDn/Left/Right in grid mode).
	fp.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN, ControlKeyState: vtinput.ShiftPressed,
	})

	if !fp.entries[2].Selected {
		t.Error("file2.txt (index 2) should be selected after Shift+Down")
	}
	if fp.entries[3].Selected {
		t.Error("file3.txt (index 3) must NOT be selected — Shift+Down is single-step, next row is left alone")
	}
	if fp.GetCursorIndex() != 3 {
		t.Errorf("Cursor should move to 3, got %d", fp.GetCursorIndex())
	}

	// 4. Verify results — file1 (from Ins) + file2 (from Shift+Down).
	names := fp.GetSelectedNames()
	if len(names) != 2 || names[0] != "file1.txt" || names[1] != "file2.txt" {
		t.Errorf("GetSelectedNames returned wrong result: %v", names)
	}
}

// TestFileSystemPanel_ShiftMultiStepSwipe covers the long-jump
// shift keys: they select every row the cursor sweeps over. It's
// additive — starting on ".." works (nothing to toggle, we just
// paint from there onward), and a second swipe over already-
// selected rows grows the selection instead of flipping it off.
func TestFileSystemPanel_ShiftMultiStepSwipe(t *testing.T) {
	tmp := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		os.WriteFile(filepath.Join(tmp, n), []byte(n), 0644)
	}
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(tmp))
	fp.viewMode = ViewModeDetailed
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "a"}},
		{VFSItem: vfs.VFSItem{Name: "b"}},
		{VFSItem: vfs.VFSItem{Name: "c"}},
		{VFSItem: vfs.VFSItem{Name: "d"}},
		{VFSItem: vfs.VFSItem{Name: "e"}},
	}
	fp.Refresh()

	// Cursor on ".." (idx 0). Shift+End should still paint a..e —
	// starting on an unselectable row is not a reason to skip the
	// sweep. This exact scenario used to return zero selections.
	fp.SetCursorIndex(0)
	fp.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_END,
		ControlKeyState: vtinput.ShiftPressed,
	})
	for _, want := range []string{"a", "b", "c", "d", "e"} {
		for _, e := range fp.entries {
			if e.Name == want && !e.Selected {
				t.Errorf(`Shift+End starting on "..": expected %q selected`, want)
			}
		}
	}

	// Repeating the sweep from "e" back with Shift+Home must NOT
	// deselect anything — swipes are additive. Everything from ".."
	// (skipped) through "a" is already selected, and the return
	// trip leaves the selection intact.
	fp.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_HOME,
		ControlKeyState: vtinput.ShiftPressed,
	})
	for _, want := range []string{"a", "b", "c", "d", "e"} {
		for _, e := range fp.entries {
			if e.Name == want && !e.Selected {
				t.Errorf("Shift+Home return sweep must not deselect %q", want)
			}
		}
	}
}

// TestFileSystemPanel_ShiftSessionModeDecidedOnFirstKey covers
// FAR-style session semantic: the first Shift+nav decides "select"
// or "deselect" based on the starting row, every subsequent Shift+
// nav in the same session applies that same mode, and any non-
// Shift-nav event closes the session so the next Shift+nav re-
// decides.
func TestFileSystemPanel_ShiftSessionModeDecidedOnFirstKey(t *testing.T) {
	tmp := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		os.WriteFile(filepath.Join(tmp, n), []byte(n), 0644)
	}
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(tmp))
	fp.viewMode = ViewModeDetailed
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "a"}},
		{VFSItem: vfs.VFSItem{Name: "b"}},
		{VFSItem: vfs.VFSItem{Name: "c"}},
		{VFSItem: vfs.VFSItem{Name: "d"}},
		{VFSItem: vfs.VFSItem{Name: "e"}},
	}
	fp.Refresh()

	shiftDown := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_DOWN,
		ControlKeyState: vtinput.ShiftPressed,
	}
	plainDown := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_DOWN,
	}

	// Session 1: start on unselected "a" (idx 1) → session picks
	// "select" mode. Two Shift+Down keystrokes should select "a"
	// and "b" one after another.
	fp.SetCursorIndex(1)
	fp.ProcessKey(shiftDown)
	fp.ProcessKey(shiftDown)
	if !fp.entries[1].Selected || !fp.entries[2].Selected {
		t.Errorf("session-1 select: a=%v b=%v, want both true",
			fp.entries[1].Selected, fp.entries[2].Selected)
	}
	if fp.entries[3].Selected {
		t.Error("session-1 select: 'c' must not be selected yet — cursor stopped at 'c'")
	}

	// Plain Down closes the session. Cursor moves from c to d
	// without selecting anything.
	fp.ProcessKey(plainDown)
	if fp.shiftSessionActive {
		t.Error("plain Down must close the shift-selection session")
	}

	// Session 2: cursor now on "d". Move it back to "b" (still
	// selected from session 1) to prove the mode re-decides.
	fp.SetCursorIndex(2)
	fp.ProcessKey(shiftDown)
	// b was selected → session mode = deselect. b should be
	// unselected now, cursor moved to c.
	if fp.entries[2].Selected {
		t.Error("session-2 first Shift+Down on selected 'b' should deselect it")
	}
	// Another Shift+Down: c was selected? no, c wasn't selected
	// after session 1. deselect on an already-unselected row is
	// a no-op — still unselected.
	fp.ProcessKey(shiftDown)
	if fp.entries[3].Selected {
		t.Error("session-2 mode is deselect; 'c' must stay unselected")
	}
}

// TestFileSystemPanel_ShiftSessionDeselectFromParentDir covers the
// asymmetric case: cursor on ".." (unselectable) and everything
// below already selected. Session mode has to look past ".." at
// the first real row in the direction of movement; otherwise
// Shift+End would always start in "select" mode and there'd be
// no way to clear the panel selection from that position.
func TestFileSystemPanel_ShiftSessionDeselectFromParentDir(t *testing.T) {
	tmp := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		os.WriteFile(filepath.Join(tmp, n), []byte(n), 0644)
	}
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(tmp))
	fp.viewMode = ViewModeDetailed
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "a"}},
		{VFSItem: vfs.VFSItem{Name: "b"}},
		{VFSItem: vfs.VFSItem{Name: "c"}},
	}
	// Pre-select everything by hand — this is the state the user
	// would land in after a first Shift+End sweep.
	fp.entries[1].Selected = true
	fp.entries[2].Selected = true
	fp.entries[3].Selected = true
	fp.selectedItems = map[string]bool{"a": true, "b": true, "c": true}
	fp.Refresh()

	// Cursor on ".." (idx 0). Shift+End should look past ".." to
	// "a" (selected) and pick "deselect" mode, then clear a..c.
	fp.SetCursorIndex(0)
	fp.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_END,
		ControlKeyState: vtinput.ShiftPressed,
	})
	for _, want := range []string{"a", "b", "c"} {
		for _, e := range fp.entries {
			if e.Name == want && e.Selected {
				t.Errorf("Shift+End from '..': expected %q deselected", want)
			}
		}
	}
}

// TestFileSystemPanel_ShiftRangeSkipsParentDir makes sure the ".."
// row is never selected by a shift-sweep, matching the same rule
// SetItemSelected / ToggleSelection already enforce for Ins.
func TestFileSystemPanel_ShiftRangeSkipsParentDir(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a"), []byte("a"), 0644)
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(tmp))
	fp.viewMode = ViewModeDetailed
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "a"}},
	}
	fp.Refresh()

	// Cursor on "a"; Shift+Home should not select ".."
	fp.SetCursorIndex(1)
	fp.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_HOME,
		ControlKeyState: vtinput.ShiftPressed,
	})
	if fp.entries[0].Selected {
		t.Error(`Shift-sweep must not select the ".." parent-dir row`)
	}
}

// TestFileSystemPanel_SelectionClearedOnDirChange guards against
// the map[filename]→selected persisting between directories: a
// row with the same name in a sibling dir used to inherit the
// old selection. readDirectoryEx now drops the map when the path
// changes.
func TestFileSystemPanel_SelectionClearedOnDirChange(t *testing.T) {
	// Two sibling directories, each with a file named "same".
	parent := t.TempDir()
	a := filepath.Join(parent, "a")
	b := filepath.Join(parent, "b")
	os.MkdirAll(a, 0755)
	os.MkdirAll(b, 0755)
	os.WriteFile(filepath.Join(a, "same"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(b, "same"), []byte("b"), 0644)

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(a))
	fp.viewMode = ViewModeDetailed
	// Simulate a completed load of directory `a` with "same" selected.
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "same"}},
	}
	fp.selectedItems = map[string]bool{"same": true}
	fp.entries[1].Selected = true
	fp.lastLoadedPath = a

	// Now navigate to sibling `b`. readDirectoryEx should notice
	// the path changed and drop the persistent selection so the
	// "same" file in `b` starts unselected.
	fp.vfs.SetPath(b)
	fp.ReadDirectory()

	// Drain any async loader tasks the ReadDirectory scheduled.
	timeout := time.After(500 * time.Millisecond)
drain:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			break drain
		}
	}

	for _, e := range fp.entries {
		if e.Name == "same" && e.Selected {
			t.Error(`"same" in sibling directory should NOT inherit selection from previous dir`)
		}
	}
	if fp.selectedItems["same"] {
		t.Error("selectedItems should have been cleared on directory change")
	}
}

func TestFileSystemPanel_ProcessMouse(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS("."))
	fp.SetViewMode(ViewModeDetailed)

	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "f1"}},
		{VFSItem: vfs.VFSItem{Name: "f2"}},
	}
	fp.Refresh()

	// Left Click on f1 (Index 1). Table at Y=1, header is 1, so row 0 is Y=2, row 1 is Y=3.
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 5, MouseY: 3, ButtonState: vtinput.FromLeft1stButtonPressed,
	})

	if fp.GetCursorIndex() != 1 {
		t.Errorf("Expected cursorIdx 1, got %d", fp.GetCursorIndex())
	}

	// Right click on f2 (Index 2). Data row 2 is Y=4.
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 5, MouseY: 4, ButtonState: vtinput.RightmostButtonPressed,
	})

	if fp.GetCursorIndex() != 2 {
		t.Errorf("Expected cursorIdx 2, got %d", fp.GetCursorIndex())
	}
	if !fp.entries[2].Selected {
		t.Error("Right click selection failed")
	}

	// Right click again without button release (dragging simulation) - should NOT unselect
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 6, MouseY: 4, ButtonState: vtinput.RightmostButtonPressed,
	})

	if !fp.entries[2].Selected {
		t.Error("Right click drag shouldn't unselect the same item")
	}

	// Release button
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: false,
		MouseX: 6, MouseY: 4, ButtonState: 0,
	})

	// Click again - SHOULD unselect
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 6, MouseY: 4, ButtonState: vtinput.RightmostButtonPressed,
	})

	if fp.entries[2].Selected {
		t.Error("New right click should toggle selection")
	}
}

func newPanelScrollTestFixture(mode ViewMode, entryCount int) *FileSystemPanel {
	table := vtui.NewTable(1, 1, 38, 8, nil)
	fp := &FileSystemPanel{
		table:    table,
		viewMode: mode,
		entries:  make([]*fileEntry, entryCount),
	}
	if mode == ViewModeWide {
		fp.wide = true
		fp.viewMode = ViewModeMedium
	}
	for idx := range fp.entries {
		fp.entries[idx] = &fileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("f%d", idx)}}
	}
	fp.ScreenObject.SetPosition(0, 0, 39, 11)
	fp.initScrollBar()
	return fp
}

func TestPanelFileNameMatchSpans_AlignedExtension(t *testing.T) {
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.SeparateFileExtensions = true

	entry := &fileEntry{VFSItem: vfs.VFSItem{Name: "report.json"}}
	spans := panelFileNameMatchSpans(entry, 16, 0, len([]rune("report.jso")))
	if len(spans) != 2 {
		t.Fatalf("match spans = %#v, want base and extension spans", spans)
	}
	if spans[0] != (panelMatchSpan{start: 0, width: 6}) {
		t.Fatalf("base span = %#v, want start 0 width 6", spans[0])
	}
	if spans[1] != (panelMatchSpan{start: 12, width: 3}) {
		t.Fatalf("extension span = %#v, want start 12 width 3", spans[1])
	}
}

func TestPanelFileNameMatchSpans_NoExtension(t *testing.T) {
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.SeparateFileExtensions = true

	entry := &fileEntry{VFSItem: vfs.VFSItem{
		Name:        "V2454A (192.168.1.100:38477)",
		NoExtension: true,
	}}
	start := strings.Index(entry.Name, "100:38477")
	got := panelFileNameMatchSpans(entry, 40, start, len("100:38477"))
	want := []panelMatchSpan{{start: start, width: len("100:38477")}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("virtual-row match spans = %#v, want %#v", got, want)
	}
}

func TestPanelFileNameMatchSpans_Anywhere(t *testing.T) {
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.SeparateFileExtensions = true

	entry := &fileEntry{VFSItem: vfs.VFSItem{Name: "report.json"}}
	if got := panelFileNameMatchSpans(entry, 16, 2, 4); len(got) != 1 || got[0] != (panelMatchSpan{start: 2, width: 4}) {
		t.Fatalf("middle-of-name spans = %#v, want start 2 width 4", got)
	}
	if got := panelFileNameMatchSpans(entry, 16, 8, 3); len(got) != 1 || got[0] != (panelMatchSpan{start: 13, width: 3}) {
		t.Fatalf("middle-of-extension spans = %#v, want start 13 width 3", got)
	}
}

func TestFileSystemPanel_DrawFastFindMatches(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.SeparateFileExtensions = false

	fp := NewFileSystemPanel(0, 0, 40, 12, vfs.NewOSVFS(t.TempDir()))
	fp.viewMode = ViewModeDetailed
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "alpha.txt"}},
		{VFSItem: vfs.VFSItem{Name: "beta.txt"}},
		{VFSItem: vfs.VFSItem{Name: "alpine.txt"}},
	}
	fp.Resize(40, 12)
	fp.Refresh()
	fp.table.SetFocus(true)
	fp.fastFindMode = true
	fp.fastFindStr = "alp"

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(40, 12)
	fp.table.Show(scr)
	y := fp.table.Y1 + fp.table.MarginTop
	beforeCursor := scr.GetCell(fp.table.X1, y).Attributes
	beforeNonMatch := scr.GetCell(fp.table.X1, y+1).Attributes

	fp.drawFastFindMatches(scr)
	matchColor := vtui.GetRGBFore(vtui.Palette[ColPanelHighlightText])
	for _, row := range []int{0, 2} {
		for x := 0; x < 3; x++ {
			cell := scr.GetCell(fp.table.X1+x, y+row)
			if got := vtui.GetRGBFore(cell.Attributes); got != matchColor {
				t.Fatalf("row %d match cell %d foreground = %#06x, want %#06x", row, x, got, matchColor)
			}
		}
	}
	afterCursor := scr.GetCell(fp.table.X1, y).Attributes
	if vtui.GetRGBBack(afterCursor) != vtui.GetRGBBack(beforeCursor) {
		t.Fatalf("match highlight changed cursor background: before %#x after %#x", beforeCursor, afterCursor)
	}
	if got := scr.GetCell(fp.table.X1, y+1).Attributes; got != beforeNonMatch {
		t.Fatalf("non-matching row color changed: before %#x after %#x", beforeNonMatch, got)
	}
}

func TestFileSystemPanel_DrawFastFindMatchesInEveryGridColumn(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.SeparateFileExtensions = false

	for _, mode := range []ViewMode{ViewModeBrief, ViewModeMedium} {
		fp := NewFileSystemPanel(0, 0, 60, 12, vfs.NewOSVFS(t.TempDir()))
		fp.viewMode = mode
		fp.Resize(60, 12)
		height := fp.table.ViewHeight
		count := height * fp.gridColumnCount()
		fp.entries = make([]*fileEntry, count)
		for idx := range fp.entries {
			fp.entries[idx] = &fileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("other-%d", idx)}}
		}
		for column := 0; column < fp.gridColumnCount(); column++ {
			fp.entries[column*height].Name = fmt.Sprintf("match-%d", column)
		}
		fp.Refresh()
		fp.fastFindMode = true
		fp.fastFindStr = "match"

		scr := vtui.NewSilentScreenBuf()
		scr.AllocBuf(60, 12)
		fp.table.Show(scr)
		fp.drawFastFindMatches(scr)
		wantForeground := vtui.GetRGBFore(vtui.Palette[ColPanelHighlightText])
		x := fp.table.X1
		y := fp.table.Y1 + fp.table.MarginTop
		for column, tableColumn := range fp.table.Columns {
			if got := vtui.GetRGBFore(scr.GetCell(x, y).Attributes); got != wantForeground {
				t.Fatalf("mode %v column %d foreground = %#06x, want %#06x", mode, column, got, wantForeground)
			}
			x += tableColumn.Width + 1
		}
	}
}

func TestFileSystemPanel_DragAutoScrollContinuesAndStops(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	for _, mode := range []ViewMode{ViewModeBrief, ViewModeMedium, ViewModeDetailed, ViewModeWide} {
		fp := newPanelScrollTestFixture(mode, 50)
		lastVisible := fp.table.ViewHeight*fp.gridColumnCount() - 1
		fp.SetCursorIndex(lastVisible)
		fp.rowDragButton = vtinput.FromLeft1stButtonPressed

		if !fp.updateDragAutoScroll(fp.Y2 + 1) {
			t.Fatalf("mode %v: movement below the panel did not start drag auto-scroll", mode)
		}
		if fp.table.TopPos != 1 || fp.GetCursorIndex() != lastVisible+1 {
			t.Fatalf("mode %v: first auto-scroll step = top %d, cursor %d; want 1, %d",
				mode, fp.table.TopPos, fp.GetCursorIndex(), lastVisible+1)
		}
		if fp.dragScrollTimer == nil || fp.dragScrollDirection != 1 {
			t.Fatalf("mode %v: drag auto-scroll timer was not scheduled downward", mode)
		}

		contentY := fp.table.Y1 + fp.table.MarginTop
		if fp.updateDragAutoScroll(contentY) {
			t.Fatalf("mode %v: movement back inside the panel still reported auto-scroll", mode)
		}
		if fp.dragScrollTimer != nil || fp.dragScrollDirection != 0 {
			t.Fatalf("mode %v: drag auto-scroll did not stop after returning inside", mode)
		}
	}
}

func TestFileSystemPanel_RightDragAutoScrollSelectsIntermediateRows(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	fp := newPanelScrollTestFixture(ViewModeDetailed, 30)
	start := fp.table.ViewHeight - 1
	fp.SetCursorIndex(start)
	fp.rightDragActive = true
	fp.rightDragSelect = true
	fp.lastRightClickedIdx = start
	fp.rowDragButton = vtinput.RightmostButtonPressed

	if !fp.dragAutoScrollStep(1) {
		t.Fatal("downward drag auto-scroll did not move")
	}
	want := start + 1
	if fp.GetCursorIndex() != want || !fp.entries[want].Selected {
		t.Fatalf("right drag auto-scroll cursor=%d selected=%v; want cursor %d selected",
			fp.GetCursorIndex(), fp.entries[want].Selected, want)
	}

	if !fp.dragAutoScrollStep(-1) {
		t.Fatal("upward drag auto-scroll did not move")
	}
	if fp.GetCursorIndex() != start || !fp.entries[start].Selected {
		t.Fatalf("upward right drag cursor=%d selected=%v; want cursor %d selected",
			fp.GetCursorIndex(), fp.entries[start].Selected, start)
	}
}

func TestFileSystemPanel_DragAutoScrollStopsOnRelease(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	fp := newPanelScrollTestFixture(ViewModeDetailed, 30)
	fp.SetCursorIndex(fp.table.ViewHeight - 1)
	fp.rowDragButton = vtinput.FromLeft1stButtonPressed
	fp.updateDragAutoScroll(fp.Y2 + 1)

	fp.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType})
	if fp.rowDragButton != 0 || fp.dragScrollTimer != nil || fp.dragScrollDirection != 0 {
		t.Fatal("mouse release did not cancel drag auto-scroll")
	}
}

func TestFileSystemPanel_ScrollBarMetricsAllViewModes(t *testing.T) {
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.PanelScrollbarMode = PanelScrollbarFull

	for _, tc := range []struct {
		name    string
		mode    ViewMode
		columns int
	}{
		{name: "brief", mode: ViewModeBrief, columns: 3},
		{name: "medium", mode: ViewModeMedium, columns: 2},
		{name: "detailed", mode: ViewModeDetailed, columns: 1},
		{name: "wide", mode: ViewModeWide, columns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newPanelScrollTestFixture(tc.mode, 0)
			height := fp.table.ViewHeight
			capacity := height * tc.columns

			fp.entries = make([]*fileEntry, capacity)
			_, visible, maxTop, virtualMax, _ := fp.panelScrollMetrics()
			if visible != capacity || maxTop != 0 || virtualMax != 0 {
				t.Fatalf("fitting panel metrics = visible %d, maxTop %d, virtualMax %d; want %d, 0, 0",
					visible, maxTop, virtualMax, capacity)
			}
			if fp.syncScrollBar() {
				t.Fatal("scrollbar visible while all entries fit")
			}

			fp.entries = make([]*fileEntry, capacity+5)
			_, visible, maxTop, virtualMax, _ = fp.panelScrollMetrics()
			wantVirtualMax := (capacity+5+tc.columns-1)/tc.columns - height
			if visible != capacity || maxTop != 5 || virtualMax != wantVirtualMax {
				t.Fatalf("overflow metrics = visible %d, maxTop %d, virtualMax %d; want %d, 5, %d",
					visible, maxTop, virtualMax, capacity, wantVirtualMax)
			}
			if !fp.syncScrollBar() {
				t.Fatal("scrollbar hidden while entries overflow")
			}
			fp.table.TopPos = maxTop
			_, _, _, virtualMax, virtualValue := fp.panelScrollMetrics()
			if virtualValue != virtualMax {
				t.Fatalf("bottom TopPos maps to virtual value %d, want %d", virtualValue, virtualMax)
			}
		})
	}
}

func TestFileSystemPanel_ScrollBarDrawAndMouse(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.PanelScrollbarMode = PanelScrollbarFull

	fp := newPanelScrollTestFixture(ViewModeMedium, 0)
	capacity := fp.table.ViewHeight * fp.gridColumnCount()
	fp.entries = make([]*fileEntry, capacity+5)
	for idx := range fp.entries {
		fp.entries[idx] = &fileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("f%d", idx)}}
	}

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(40, 12)
	fp.drawScrollBar(scr)
	if got := scr.GetCell(fp.X2, fp.scrollBar.Y1); got.Char != vtui.ScrollUpArrow {
		t.Fatalf("scrollbar top cell = %q, want %q", rune(got.Char), rune(vtui.ScrollUpArrow))
	} else if got.Attributes != vtui.Palette[ColPanelScrollbar] {
		t.Fatalf("scrollbar attr = %#x, want Panel.Scrollbar %#x", got.Attributes, vtui.Palette[ColPanelScrollbar])
	}

	// The down arrow scrolls one item while keeping the cursor on the same
	// visual slot.
	if !fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: int16(fp.X2), MouseY: int16(fp.scrollBar.Y2),
		ButtonState: vtinput.FromLeft1stButtonPressed,
	}) {
		t.Fatal("scrollbar down arrow was not handled")
	}
	fp.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType})
	if fp.table.TopPos != 1 || fp.GetCursorIndex() != 1 {
		t.Fatalf("after scrollbar step: top=%d cursor=%d, want 1,1", fp.table.TopPos, fp.GetCursorIndex())
	}

	// Crossing the scrollbar during a row drag must not start a new
	// scrollbar gesture.
	fp.setPanelScrollTop(0)
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: int16(fp.X2), MouseY: int16(fp.scrollBar.Y2),
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.MouseMoved,
	})
	if fp.scrollMouseActive || fp.table.TopPos != 0 {
		t.Fatalf("row drag started scrollbar: active=%v top=%d", fp.scrollMouseActive, fp.table.TopPos)
	}

	// Dragging the thumb to the bottom reaches the exact item-based maximum,
	// even though the scrollbar itself operates in virtual rows.
	fp.setPanelScrollTop(0)
	fp.syncScrollBar()
	thumbY := fp.scrollBar.Y1 + 1
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: int16(fp.X2), MouseY: int16(thumbY),
		ButtonState: vtinput.FromLeft1stButtonPressed,
	})
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 0, MouseY: int16(fp.scrollBar.Y2 - 1),
		ButtonState: vtinput.FromLeft1stButtonPressed,
	})
	fp.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType})
	if fp.scrollMouseActive {
		t.Fatal("scrollbar kept mouse capture after button release")
	}
	_, _, maxTop, _, _ := fp.panelScrollMetrics()
	if fp.table.TopPos != maxTop {
		t.Fatalf("after thumb drag: top=%d, want maxTop=%d", fp.table.TopPos, maxTop)
	}
}

func TestFileSystemPanel_ScrollBarHiddenWhenGridFits(t *testing.T) {
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.PanelScrollbarMode = PanelScrollbarFull

	fp := newPanelScrollTestFixture(ViewModeBrief, 0)
	fp.entries = make([]*fileEntry, fp.table.ViewHeight*fp.gridColumnCount())
	fp.table.TopPos = 4
	fp.cursorIdx = 4
	fp.Refresh()
	if fp.table.TopPos != 0 {
		t.Fatalf("fitting Brief grid kept stale TopPos %d after refresh", fp.table.TopPos)
	}
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(40, 12)

	fp.drawScrollBar(scr)
	dataY := fp.table.Y1 + fp.table.MarginTop
	if got := scr.GetCell(fp.X2, dataY).Char; got != 0 {
		t.Fatalf("scrollbar drawn for fitting Brief grid: %q", rune(got))
	}
}

func TestFileSystemPanel_ScrollBarDisabled(t *testing.T) {
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.PanelScrollbarMode = PanelScrollbarOff

	fp := newPanelScrollTestFixture(ViewModeDetailed, 50)
	if fp.syncScrollBar() {
		t.Fatal("disabled panel scrollbar reported itself visible")
	}
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(40, 12)
	fp.drawScrollBar(scr)
	y := fp.table.Y1 + fp.table.MarginTop
	if got := scr.GetCell(fp.X2, y).Char; got != 0 {
		t.Fatalf("disabled panel scrollbar drew %q", rune(got))
	}
	if fp.processScrollBarMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: int16(fp.X2), MouseY: int16(y),
		ButtonState: vtinput.FromLeft1stButtonPressed,
	}) {
		t.Fatal("disabled panel scrollbar handled mouse input")
	}
}

func TestMinimalPanelScrollThumbUsesWholeHeight(t *testing.T) {
	position, length := minimalPanelScrollThumb(10, 0, 20)
	if position != 0 || length != 3 {
		t.Fatalf("top thumb = position %d length %d, want 0,3", position, length)
	}
	position, bottomLength := minimalPanelScrollThumb(10, 20, 20)
	if bottomLength != length || position+bottomLength != 10 {
		t.Fatalf("bottom thumb = position %d length %d, want it to touch row 10", position, bottomLength)
	}
}

func TestFileSystemPanel_MinimalScrollBarDrawAndMouse(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.PanelScrollbarMode = PanelScrollbarMinimal

	fp := newPanelScrollTestFixture(ViewModeDetailed, 50)
	fp.Refresh()
	if !fp.syncScrollBar() {
		t.Fatal("minimal scrollbar was hidden for overflowing content")
	}
	height := fp.scrollBar.Y2 - fp.scrollBar.Y1 + 1
	caretPos, caretLength := minimalPanelScrollThumb(height, fp.scrollBar.Value, fp.scrollBar.Max)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(40, 12)
	fp.drawScrollBar(scr)
	for offset := 0; offset < height; offset++ {
		cell := scr.GetCell(fp.scrollBar.X1, fp.scrollBar.Y1+offset)
		inHandle := offset >= caretPos && offset < caretPos+caretLength
		if inHandle {
			if cell.Char != '│' || cell.Attributes != vtui.Palette[ColPanelMinimalScrollbar] {
				t.Fatalf("minimal handle cell %d = %q/%#x, want bright border", offset, rune(cell.Char), cell.Attributes)
			}
		} else if cell.Char != 0 {
			t.Fatalf("minimal scrollbar drew track or arrow at offset %d: %q", offset, rune(cell.Char))
		}
	}

	outsideHandleY := fp.scrollBar.Y1 + caretPos + caretLength
	if outsideHandleY <= fp.scrollBar.Y2 && fp.processScrollBarMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: int16(fp.scrollBar.X1), MouseY: int16(outsideHandleY),
		ButtonState: vtinput.FromLeft1stButtonPressed,
	}) {
		t.Fatal("minimal scrollbar handled a click on the invisible track")
	}
	if !fp.processScrollBarMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: int16(fp.scrollBar.X1), MouseY: int16(fp.scrollBar.Y1 + caretPos),
		ButtonState: vtinput.FromLeft1stButtonPressed,
	}) {
		t.Fatal("minimal scrollbar did not capture its handle")
	}
	if !fp.processScrollBarMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 0, MouseY: int16(fp.scrollBar.Y2),
		ButtonState: vtinput.FromLeft1stButtonPressed, MouseEventFlags: vtinput.MouseMoved,
	}) {
		t.Fatal("minimal scrollbar did not drag its captured handle")
	}
	_, _, maxTop, _, _ := fp.panelScrollMetrics()
	if fp.table.TopPos != maxTop {
		t.Fatalf("minimal handle drag ended at top %d, want %d", fp.table.TopPos, maxTop)
	}
	fp.processScrollBarMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType})
	if fp.scrollMouseActive {
		t.Fatal("minimal scrollbar kept mouse capture after release")
	}
}

func TestFileSystemPanel_SingleRowColumnsFillPanelWidth(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode ViewMode
	}{
		{name: "detailed", mode: ViewModeDetailed},
		{name: "wide", mode: ViewModeWide},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newPanelScrollTestFixture(tc.mode, 1)
			fp.frame = vtui.NewBorderedFrame(0, 0, 39, 11, vtui.SingleBox, "")
			fp.Resize(40, 12)

			if got := fp.table.Columns[1].Width; got != 11 {
				t.Fatalf("Size column width = %d, want 11", got)
			}
			usedWidth := len(fp.table.Columns) - 1 // separators
			for _, column := range fp.table.Columns {
				usedWidth += column.Width
			}
			tableWidth := fp.table.X2 - fp.table.X1 + 1
			if usedWidth != tableWidth {
				t.Fatalf("columns use %d cells of %d; trailing gap=%d", usedWidth, tableWidth, tableWidth-usedWidth)
			}
		})
	}
}

func TestFileSystemPanel_CursorColorsColumnSeparators(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	for _, tc := range []struct {
		name    string
		mode    ViewMode
		columns []vtui.TableColumn
	}{
		{
			name: "detailed", mode: ViewModeDetailed,
			columns: []vtui.TableColumn{{Width: 20}, {Width: 12}},
		},
		{
			name: "wide", mode: ViewModeWide,
			columns: []vtui.TableColumn{{Width: 10}, {Width: 8}, {Width: 12}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newPanelScrollTestFixture(tc.mode, 1)
			fp.entries[0].Selected = true
			fp.table.Columns = tc.columns
			fp.table.ColorTextIdx = ColPanelText
			fp.table.ColorSelectedTextIdx = ColPanelCursor
			fp.table.ColorItemSelectTextIdx = ColPanelSelectedText
			fp.table.ColorItemSelectCursorIdx = ColPanelSelectedCursor
			fp.table.ColorTitleIdx = ColPanelColumnTitle
			fp.table.ColorBoxIdx = ColPanelBox
			fp.Refresh()
			fp.table.SetFocus(true)

			scr := vtui.NewSilentScreenBuf()
			scr.AllocBuf(40, 12)
			fp.table.Show(scr)
			fp.drawCursorSeparators(scr)

			y := fp.table.Y1 + fp.table.MarginTop
			x := fp.table.X1
			for column := 0; column < len(fp.table.Columns)-1; column++ {
				x += fp.table.Columns[column].Width
				cell := scr.GetCell(x, y)
				if cell.Char != '│' {
					t.Fatalf("separator %d char = %q, want │", column, rune(cell.Char))
				}
				boxAttr := vtui.Palette[ColPanelBox]
				cursorAttr := vtui.Palette[ColPanelSelectedCursor]
				if cell.Attributes&vtui.IsFgRGB != boxAttr&vtui.IsFgRGB ||
					vtui.GetRGBFore(cell.Attributes) != vtui.GetRGBFore(boxAttr) {
					t.Fatalf("separator %d foreground = %#x, want Panel.Box foreground %#x",
						column, cell.Attributes, boxAttr)
				}
				if cell.Attributes&vtui.IsBgRGB != cursorAttr&vtui.IsBgRGB ||
					vtui.GetRGBBack(cell.Attributes) != vtui.GetRGBBack(cursorAttr) {
					t.Fatalf("separator %d background = %#x, want selected cursor background %#x",
						column, cell.Attributes, cursorAttr)
				}
				if vtui.GetRGBFore(cell.Attributes) == vtui.GetRGBFore(cursorAttr) {
					t.Fatalf("separator %d inherited selected row foreground", column)
				}
				x++
			}
		})
	}
}

func TestFileSystemPanel_MouseClick_Edges(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS("."))
	fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: ".."}}}
	fp.SetCursorIndex(0)

	// 1. Click on panel border (Y=0)
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 5, MouseY: 0, ButtonState: vtinput.FromLeft1stButtonPressed,
	})
	if fp.GetCursorIndex() != 0 {
		t.Errorf("Clicking on border should not change selection. Got %d", fp.GetCursorIndex())
	}

	// 2. Click on table header (Y=1)
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 5, MouseY: 1, ButtonState: vtinput.FromLeft1stButtonPressed,
	})
	if fp.GetCursorIndex() != 0 {
		t.Errorf("Clicking on header should not change selection. Got %d", fp.GetCursorIndex())
	}
}

func TestFileSystemPanel_RightClick_ResetOnRelease(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS("."))
	fp.viewMode = ViewModeDetailed
	fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "f1"}}}

	// 1. Right click once -> Selects
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true, MouseX: 5, MouseY: 2, ButtonState: vtinput.RightmostButtonPressed,
	})
	if !fp.entries[0].Selected {
		t.Fatal("Should be selected")
	}

	// 2. Release button -> Resets tracker
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: false, MouseX: 5, MouseY: 2, ButtonState: 0,
	})

	// 3. Right click again -> Should toggle (Unselect) even though it's the same index
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true, MouseX: 5, MouseY: 2, ButtonState: vtinput.RightmostButtonPressed,
	})
	if fp.entries[0].Selected {
		t.Error("Item should have been unselected after button release and re-click")
	}
}

func TestFileSystemPanel_RightDragAppliesToSkippedRows(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS("."))
	fp.SetViewMode(ViewModeDetailed)
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "f1"}},
		{VFSItem: vfs.VFSItem{Name: "f2"}},
		{VFSItem: vfs.VFSItem{Name: "f3"}},
		{VFSItem: vfs.VFSItem{Name: "f4"}},
	}
	fp.Refresh()

	dataY := fp.table.Y1 + fp.table.MarginTop
	rightDown := func(idx int) {
		fp.ProcessMouse(&vtinput.InputEvent{
			Type: vtinput.MouseEventType, KeyDown: true,
			MouseX: int16(fp.table.X1), MouseY: int16(dataY + idx),
			ButtonState: vtinput.RightmostButtonPressed,
		})
	}
	release := func(idx int) {
		fp.ProcessMouse(&vtinput.InputEvent{
			Type:   vtinput.MouseEventType,
			MouseX: int16(fp.table.X1), MouseY: int16(dataY + idx),
		})
	}

	// Only the endpoints emit events; f2 and f3 must still be selected.
	rightDown(1)
	rightDown(4)
	for idx := 1; idx <= 4; idx++ {
		if !fp.entries[idx].Selected {
			t.Errorf("entry %d was skipped while selecting", idx)
		}
	}

	release(4)

	// Starting a new drag on a selected item fixes the operation to deselect,
	// including skipped rows while moving in the opposite direction.
	rightDown(4)
	rightDown(1)
	for idx := 1; idx <= 4; idx++ {
		if fp.entries[idx].Selected {
			t.Errorf("entry %d was skipped while deselecting", idx)
		}
	}
}

func TestFileSystemPanel_RightDragTracksGridColumn(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS("."))
	fp.SetViewMode(ViewModeMedium)

	height := fp.table.ViewHeight
	fp.entries = make([]*fileEntry, height*2)
	for idx := range fp.entries {
		fp.entries[idx] = &fileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("f%d", idx)}}
	}
	fp.Refresh()

	startIdx := height - 2
	endIdx := height + 1
	dataY := fp.table.Y1 + fp.table.MarginTop
	leftX := fp.table.X1
	rightX := fp.table.X1 + fp.table.Columns[0].Width + 1

	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: int16(leftX), MouseY: int16(dataY + height - 2),
		ButtonState: vtinput.RightmostButtonPressed,
	})
	fp.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: int16(rightX), MouseY: int16(dataY + 1),
		ButtonState: vtinput.RightmostButtonPressed,
	})

	if fp.GetCursorIndex() != endIdx {
		t.Fatalf("cursor index = %d, want %d", fp.GetCursorIndex(), endIdx)
	}
	for idx := startIdx; idx <= endIdx; idx++ {
		if !fp.entries[idx].Selected {
			t.Errorf("grid entry %d was skipped", idx)
		}
	}
}

func TestFileSystemPanel_RightDoubleClickAppliesToWholePanel(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS("."))
	fp.SetViewMode(ViewModeDetailed)
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "f1"}},
		{VFSItem: vfs.VFSItem{Name: "f2"}},
		{VFSItem: vfs.VFSItem{Name: "f3"}},
	}
	fp.Refresh()

	dataY := fp.table.Y1 + fp.table.MarginTop
	rightClick := func(idx int, flags uint32) {
		fp.ProcessMouse(&vtinput.InputEvent{
			Type: vtinput.MouseEventType, KeyDown: true,
			MouseX: int16(fp.table.X1), MouseY: int16(dataY + idx),
			ButtonState: vtinput.RightmostButtonPressed, MouseEventFlags: flags,
		})
	}
	release := func(idx int) {
		fp.ProcessMouse(&vtinput.InputEvent{
			Type:   vtinput.MouseEventType,
			MouseX: int16(fp.table.X1), MouseY: int16(dataY + idx),
		})
	}

	// The first press selects f1; the second press spreads that state to all.
	rightClick(1, 0)
	release(1)
	rightClick(1, vtinput.DoubleClick)
	for idx := 1; idx < len(fp.entries); idx++ {
		if !fp.entries[idx].Selected {
			t.Errorf("entry %d was not selected by right double-click", idx)
		}
	}
	if fp.entries[0].Selected {
		t.Error("parent directory entry must not be selected")
	}

	release(1)

	// Starting on selected f2 makes the first press deselect it; the second
	// press spreads deselection to the whole panel.
	rightClick(2, 0)
	release(2)
	rightClick(2, vtinput.DoubleClick)
	for idx := 1; idx < len(fp.entries); idx++ {
		if fp.entries[idx].Selected {
			t.Errorf("entry %d was not deselected by right double-click", idx)
		}
	}
}

func TestFileSystemPanel_IncrementalInteraction(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(t.TempDir()))

	// Ensure we have '..' as initial state
	fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}}

	// Симулируем прилет первого чанка
	chunk1 := []vfs.VFSItem{
		{Name: "file_A", IsDir: false},
		{Name: "file_Z", IsDir: false},
	}

	// Вручную вызываем логику обработки чанка (имитируя прилет из горутины)
	fp.entries = append(fp.entries, &fileEntry{VFSItem: chunk1[0]}, &fileEntry{VFSItem: chunk1[1]})
	fp.Refresh()

	// Пользователь выбирает file_Z (это индекс 2, так как 0 это "..")
	fp.SelectName("file_Z")
	if fp.GetSelectedName() != "file_Z" {
		t.Fatalf("Failed to select file_Z, got %s", fp.GetSelectedName())
	}

	// Симулируем прилет второго чанка с файлом, который встанет В НАЧАЛО списка после сортировки
	chunk2 := []vfs.VFSItem{
		{Name: "file_0_first", IsDir: false},
	}

	// Эмуляция PostTask для второго чанка:
	currentSelected := fp.GetSelectedName() // "file_Z"
	fp.entries = append(fp.entries, &fileEntry{VFSItem: chunk2[0]})
	sort.Slice(fp.entries, func(i, j int) bool {
		if fp.entries[i].Name == ".." {
			return true
		}
		if fp.entries[j].Name == ".." {
			return false
		}
		return fp.entries[i].Name < fp.entries[j].Name
	})
	fp.Refresh()
	fp.SelectName(currentSelected) // Удерживаем курсор

	// Проверяем: file_Z теперь должен быть на индексе 3, но курсор должен быть все еще на нем
	if fp.GetSelectedName() != "file_Z" {
		t.Errorf("Cursor jumped! Expected 'file_Z', got '%s'", fp.GetSelectedName())
	}

	// Проверяем, что индекс реально изменился (был 2, стал 3)
	if fp.GetCursorIndex() != 3 {
		t.Errorf("Index should have shifted to 3, got %d", fp.GetCursorIndex())
	}
}
func TestFileSystemPanel_GetSuccessorName(t *testing.T) {
	fp := &FileSystemPanel{}

	setupEntries := func(names ...string) {
		fp.cursorIdx = 0 // Reset state between cases
		fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}}
		for _, n := range names {
			fp.entries = append(fp.entries, &fileEntry{VFSItem: vfs.VFSItem{Name: n}})
		}
	}

	// Case 1: Single item in the middle. Focus on B. Successor should be C.
	setupEntries("A", "B", "C")
	fp.cursorIdx = 2 // B (Index 0 is .., 1 is A, 2 is B)
	if res := fp.GetSuccessorName(); res != "C" {
		t.Errorf("Case 1 failed: expected 'C', got %q", res)
	}

	// Case 2: Single item at the end. Focus on C. Successor should be B.
	fp.cursorIdx = 3 // C
	if res := fp.GetSuccessorName(); res != "B" {
		t.Errorf("Case 2 failed: expected 'B', got %q", res)
	}

	// Case 3: Multiple selected in the middle. Select A, B. Successor should be C.
	setupEntries("A", "B", "C", "D")
	fp.entries[1].Selected = true // A
	fp.entries[2].Selected = true // B
	if res := fp.GetSuccessorName(); res != "C" {
		t.Errorf("Case 3 failed: expected 'C', got %q", res)
	}

	// Case 4: Multiple selected at the end. Select C, D. Successor should be B.
	setupEntries("A", "B", "C", "D")
	fp.entries[3].Selected = true // C
	fp.entries[4].Selected = true // D
	if res := fp.GetSuccessorName(); res != "B" {
		t.Errorf("Case 4 failed: expected 'B', got %q", res)
	}

	// Case 5: Empty list (only .. exists)
	setupEntries()
	if res := fp.GetSuccessorName(); res != ".." {
		t.Errorf("Case 5 failed: expected '..', got %q", res)
	}
}
func TestGetSelectedNames_ParentSafety(t *testing.T) {
	fp := &FileSystemPanel{}
	// Setup entries: 0: "..", 1: "file.txt"
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "file.txt", IsDir: false}},
	}

	// Case 1: Cursor on ".."
	fp.cursorIdx = 0
	names := fp.GetSelectedNames()
	if len(names) != 0 {
		t.Errorf("Security violation: GetSelectedNames returned items when cursor was on '..': %v", names)
	}

	// Case 2: Cursor on "file.txt"
	fp.cursorIdx = 1
	names = fp.GetSelectedNames()
	if len(names) != 1 || names[0] != "file.txt" {
		t.Errorf("Failed to get item under cursor: %v", names)
	}

	// Case 3: "file.txt" selected, but cursor is on ".."
	fp.entries[1].Selected = true
	fp.cursorIdx = 0
	names = fp.GetSelectedNames()
	if len(names) != 1 || names[0] != "file.txt" {
		t.Errorf("Failed to get selected items while cursor is on '..': %v", names)
	}
}
func TestFileSystemPanel_AsyncPendingSelection(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS("."))

	// Target: we want to select "target.txt" which will arrive in the second chunk
	fp.pendingSelection = "target.txt"
	fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}}
	fp.cursorIdx = 0

	// 1. Simulate First Chunk (doesn't contain our target)
	chunk1 := []vfs.VFSItem{{Name: "aaa.txt"}, {Name: "bbb.txt"}}

	// Replicating the logic from ReadDirectory's onChunk callback
	newEntries := make([]*fileEntry, len(chunk1))
	for i, item := range chunk1 {
		newEntries[i] = &fileEntry{VFSItem: item}
	}

	fp.entries = append(fp.entries, newEntries...)
	sort.Slice(fp.entries, func(i, j int) bool { return fp.entries[i].Name < fp.entries[j].Name })

	// Run snapping logic (simplified from file_panel.go)
	for i, entry := range fp.entries {
		if entry.Name == fp.pendingSelection {
			fp.SetCursorIndex(i)
			fp.pendingSelection = ""
			break
		}
	}

	if fp.pendingSelection == "" || fp.GetSelectedName() == "target.txt" {
		t.Error("Snapped prematurely to non-existent item")
	}

	// 2. Simulate Second Chunk (contains our target)
	chunk2 := []vfs.VFSItem{{Name: "target.txt"}, {Name: "zzz.txt"}}
	newEntries2 := make([]*fileEntry, len(chunk2))
	for i, item := range chunk2 {
		newEntries2[i] = &fileEntry{VFSItem: item}
	}

	fp.entries = append(fp.entries, newEntries2...)
	sort.Slice(fp.entries, func(i, j int) bool { return fp.entries[i].Name < fp.entries[j].Name })

	// Run snapping logic again
	for i, entry := range fp.entries {
		if entry.Name == fp.pendingSelection {
			fp.SetCursorIndex(i)
			fp.pendingSelection = ""
			break
		}
	}

	if fp.pendingSelection != "" {
		t.Error("Failed to clear pendingSelection after item arrived")
	}
	if fp.GetSelectedName() != "target.txt" {
		t.Errorf("Cursor failed to snap to 'target.txt'. Currently on: %q", fp.GetSelectedName())
	}
}
func TestFileSystemPanel_NavigateDown_CursorReset(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "subdir")
	os.Mkdir(sub, 0755)

	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(tmp))

	// Mock that we are standing on "subdir" (index 1, as index 0 is "..")
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "subdir", IsDir: true}},
	}
	fp.cursorIdx = 1
	fp.Refresh()

	// 1. Press Enter on "subdir"
	fp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})

	if fp.pendingSelection != ".." {
		t.Errorf("Expected pendingSelection to be '..', got %q", fp.pendingSelection)
	}

	// 2. Simulate data arrival for the new directory
	// Even if the new directory has a file with the same name as the old one,
	// we must stay on ".."
	chunk := []vfs.VFSItem{{Name: "subdir"}} // coincidentally same name
	newEntries := []*fileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}, {VFSItem: chunk[0]}}
	fp.entries = newEntries

	// Run snapping logic from ReadDirectory
	for i, entry := range fp.entries {
		if entry.Name == fp.pendingSelection {
			fp.SetCursorIndex(i)
			fp.pendingSelection = ""
			break
		}
	}

	if fp.GetCursorIndex() != 0 {
		t.Errorf("Cursor did not reset to '..'. Index is %d", fp.GetCursorIndex())
	}
}
func TestFileSystemPanel_FastFind(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(t.TempDir()))
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "apple"}},
		{VFSItem: vfs.VFSItem{Name: "banana"}},
		{VFSItem: vfs.VFSItem{Name: "cherry"}},
		{VFSItem: vfs.VFSItem{Name: "dog"}},
		{VFSItem: vfs.VFSItem{Name: "cat"}},
	}
	fp.Refresh()

	// 1. Trigger FastFind with Alt+C
	fp.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		Char:            'c',
		ControlKeyState: vtinput.LeftAltPressed,
	})

	if !fp.fastFindMode {
		t.Fatal("FastFind mode should be active")
	}
	if fp.fastFindStr != "c" {
		t.Errorf("Expected search string 'c', got %q", fp.fastFindStr)
	}
	if fp.GetSelectedName() != "cherry" {
		t.Errorf("Cursor should jump to 'cherry', got %q", fp.GetSelectedName())
	}

	// 2. Append 'a'
	fp.ProcessKey(&vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    'a',
	})

	if fp.fastFindStr != "ca" {
		t.Errorf("Expected search string 'ca', got %q", fp.fastFindStr)
	}
	if fp.GetSelectedName() != "cat" {
		t.Errorf("Cursor should jump to 'cat', got %q", fp.GetSelectedName())
	}

	// 3. Backspace
	fp.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_BACK,
	})
	if fp.fastFindStr != "c" {
		t.Errorf("Expected search string 'c' after backspace, got %q", fp.fastFindStr)
	}
	if fp.GetSelectedName() != "cat" {
		t.Errorf("Cursor should stay on the current matching 'cat', got %q", fp.GetSelectedName())
	}

	// 4. Ctrl+Enter finds the next match and keeps Fast Find active.
	fp.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if fp.GetSelectedName() != "cherry" {
		t.Errorf("Ctrl+Enter should wrap to 'cherry', got %q", fp.GetSelectedName())
	}
	if !fp.fastFindMode || fp.fastFindStr != "c" {
		t.Fatal("Ctrl+Enter should keep Fast Find active")
	}

	// 5. Ctrl+Shift+Enter finds the previous match.
	fp.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})
	if fp.GetSelectedName() != "cat" {
		t.Errorf("Ctrl+Shift+Enter should wrap back to 'cat', got %q", fp.GetSelectedName())
	}

	// 6. Down closes Fast Find and performs ordinary one-row navigation.
	fp.SetCursorIndex(3) // cherry
	fp.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_DOWN,
	})
	if got := fp.GetSelectedName(); got != "dog" {
		t.Errorf("Down after closing Fast Find selected %q, want dog", got)
	}
	if fp.fastFindMode || fp.fastFindStr != "" {
		t.Fatal("Down should close Fast Find")
	}

	// 7. Up follows the same rule and moves to the previous ordinary item.
	fp.SetCursorIndex(5) // cat
	fp.fastFindMode = true
	fp.fastFindStr = "c"
	fp.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_UP,
	})
	if got := fp.GetSelectedName(); got != "dog" {
		t.Errorf("Up after closing Fast Find selected %q, want dog", got)
	}
	if fp.fastFindMode || fp.fastFindStr != "" {
		t.Fatal("Up should close Fast Find")
	}

	// 8. Escape to cancel.
	fp.fastFindMode = true
	fp.fastFindStr = "c"
	fp.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_ESCAPE,
	})
	if fp.fastFindMode {
		t.Error("Escape should exit FastFind mode")
	}

	// 10. Navigation keys should deactivate FastFind
	fp.fastFindMode = true
	fp.fastFindStr = "c"
	fp.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_LEFT,
	})
	if fp.fastFindMode {
		t.Error("Navigation key (Left) should deactivate FastFind mode")
	}
}
func TestFileSystemPanel_FastFind_Rendering(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(t.TempDir()))
	fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "test-file.txt"}}}
	fp.Refresh()
	fp.fastFindMode = true
	fp.fastFindStr = "test"

	// Отрисовываем
	fp.Show(scr)

	// Проверяем наличие рамки и текста в буфере.
	// Окно поиска рисуется снизу панели (Y2-2 = 17, 18, 19).
	// Проверяем заголовок поиска (по умолчанию " Search " из Viewer.SearchTitle)
	foundTitle := false
	for x := 0; x < 80; x++ {
		if scr.GetCell(x, 17).Char == 'S' && scr.GetCell(x+1, 17).Char == 'e' {
			foundTitle = true
			break
		}
	}
	if !foundTitle {
		t.Error("FastFind window title not found in ScreenBuf")
	}

	// Проверяем набранный текст "test" на строке ввода (Y=18)
	foundText := false
	for x := 0; x < 80; x++ {
		if scr.GetCell(x, 18).Char == 't' && scr.GetCell(x+1, 18).Char == 'e' && scr.GetCell(x+2, 18).Char == 's' {
			foundText = true
			break
		}
	}
	if !foundText {
		t.Error("FastFind search string 'test' not found in ScreenBuf")
	}

	inputX, inputY := fp.X1+11, fp.Y2-1
	matchingAttr := scr.GetCell(inputX, inputY).Attributes
	if got, want := vtui.GetRGBFore(matchingAttr), vtui.GetRGBFore(vtui.Palette[vtui.ColMenuHighlight]); got != want {
		t.Fatalf("matching query foreground = %#06x, want %#06x", got, want)
	}
	if got, want := vtui.GetRGBBack(matchingAttr), vtui.GetRGBBack(vtui.Palette[vtui.ColDialogText]); got != want {
		t.Fatalf("matching query background = %#06x, want dialog background %#06x", got, want)
	}

	fp.fastFindStr = "*test"
	fp.Show(scr)
	if got := scr.GetCell(inputX, inputY).Char; got != '*' {
		t.Fatalf("anywhere-mode marker = %q, want '*'", rune(got))
	}
	if got := scr.GetCell(inputX+1, inputY).Char; got != 't' {
		t.Fatalf("query after anywhere-mode marker starts with %q, want 't'", rune(got))
	}

	fp.fastFindStr = "missing"
	fp.Show(scr)
	missingAttr := scr.GetCell(inputX, inputY).Attributes
	if got, want := vtui.GetRGBFore(missingAttr), vtui.GetRGBFore(vtui.Palette[ColPanelFastFindNoMatch]); got != want {
		t.Fatalf("missing query foreground = %#06x, want %#06x", got, want)
	}
	if got, want := vtui.GetRGBBack(missingAttr), vtui.GetRGBBack(vtui.Palette[vtui.ColDialogText]); got != want {
		t.Fatalf("missing query background = %#06x, want dialog background %#06x", got, want)
	}
}

func TestFileSystemPanel_FastFind_LongString(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)

	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(t.TempDir()))
	fp.fastFindMode = true
	// Строка длиной 26 символов. Окно вмещает 20.
	// Ожидаемый результат после обрезки слева: "D_chars_to_scroll_TAIL"
	fp.fastFindStr = "HEAD_chars_to_scroll_TAIL"

	fp.Show(scr)

	// Окно FastFind рисуется с X=9, текст начинается с X=11.
	fieldX1, fieldX2 := 11, 31

	// Проверяем наличие хвоста "TAIL"
	foundTail := false
	for x := fieldX1; x < fieldX2-3; x++ {
		if scr.GetCell(x, 18).Char == 'T' && scr.GetCell(x+1, 18).Char == 'A' &&
			scr.GetCell(x+2, 18).Char == 'I' && scr.GetCell(x+3, 18).Char == 'L' {
			foundTail = true
			break
		}
	}
	if !foundTail {
		t.Error("Long search string tail 'TAIL' not rendered correctly")
	}

	// Проверяем отсутствие головы "HEAD" (она должна была уйти за левый край)
	foundHead := false
	for x := fieldX1; x < fieldX2-3; x++ {
		if scr.GetCell(x, 18).Char == 'H' && scr.GetCell(x+1, 18).Char == 'E' &&
			scr.GetCell(x+2, 18).Char == 'A' && scr.GetCell(x+3, 18).Char == 'D' {
			foundHead = true
			break
		}
	}
	if foundHead {
		t.Error("Long search string head 'HEAD' should be scrolled out of view")
	}
}
func TestFileSystemPanel_FastFind_XLat(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(t.TempDir()))
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "readme.txt"}},
		{VFSItem: vfs.VFSItem{Name: "заметка.txt"}},
	}
	fp.Refresh()

	// 1. Поиск "readme" через ввод "кефдьу" (в русской раскладке)
	vtui.GlobalXlator.Track('ф') // Включаем русский контекст
	fp.fastFindMode = true
	fp.fastFindStr = "кефд" // "read"
	fp.doFastFind(0)

	if fp.GetSelectedName() != "readme.txt" {
		t.Errorf("XLat FastFind failed: expected 'readme.txt', got %q", fp.GetSelectedName())
	}

	// 2. Поиск "заметка" через ввод "pfvt" (в английской раскладке)
	vtui.GlobalXlator.Track('a') // Включаем английский контекст
	fp.fastFindStr = "pfvt"      // 'p'->'з', 'f'->'а', 'v'->'м', 't'->'е'
	// Сбросим индекс, чтобы гарантированно найти файл с начала списка
	fp.SetCursorIndex(0)
	fp.doFastFind(0)

	if fp.GetSelectedName() != "заметка.txt" {
		t.Errorf("XLat FastFind (reverse) failed: expected 'заметка.txt', got %q", fp.GetSelectedName())
	}
}

func TestFileSystemPanel_FastFindStartsAtCurrentItem(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(t.TempDir()))
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "match-before.txt"}},
		{VFSItem: vfs.VFSItem{Name: "other.txt"}},
		{VFSItem: vfs.VFSItem{Name: "match-after.txt"}},
		{VFSItem: vfs.VFSItem{Name: "tail.txt"}},
	}
	fp.Refresh()
	fp.fastFindMode = true
	fp.fastFindStr = "match"

	fp.SetCursorIndex(1)
	fp.doFastFind(0)
	if got := fp.GetSelectedName(); got != "match-after.txt" {
		t.Fatalf("search from middle selected %q, want match-after.txt", got)
	}
	fp.doFastFind(0)
	if got := fp.GetSelectedName(); got != "match-after.txt" {
		t.Fatalf("search moved away from matching current item to %q", got)
	}
	fp.SetCursorIndex(3)
	fp.doFastFind(0)
	if got := fp.GetSelectedName(); got != "match-before.txt" {
		t.Fatalf("wrapped search selected %q, want match-before.txt", got)
	}
}

func TestFileSystemPanel_ForkDuplication(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "file1.txt"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(tmp, "file2.txt"), []byte("data"), 0644)

	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(tmp))

	// Wait for initial load
	timeout := time.After(1 * time.Second)
	for fp.isLoading {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("timeout")
		}
	}
	// Drain remaining tasks
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			goto done1
		}
	}
done1:

	initialCount := len(fp.entries)
	if initialCount != 3 { // "..", "file1.txt", "file2.txt"
		t.Fatalf("Expected 3 entries initially, got %d", initialCount)
	}

	// Simulate what Clone() does
	cloneFsp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(tmp))
	// Copy entries like Clone()
	cloneFsp.entries = make([]*fileEntry, len(fp.entries))
	for j, e := range fp.entries {
		cloneFsp.entries[j] = &fileEntry{VFSItem: e.VFSItem, Selected: e.Selected}
	}
	cloneFsp.Refresh()

	// Call readDirectoryEx(true) like Clone()
	cloneFsp.readDirectoryEx(true)

	// Wait for clone load
	timeout = time.After(1 * time.Second)
	for cloneFsp.isLoading {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("timeout")
		}
	}
	// Drain remaining tasks
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			goto done2
		}
	}
done2:

	if len(cloneFsp.entries) != initialCount {
		t.Errorf("Duplication bug! Expected %d entries, got %d", initialCount, len(cloneFsp.entries))
	}
}
func TestFileSystemPanel_FastFind_MouseDeactivation(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(t.TempDir()))
	fp.fastFindMode = true

	// Клик мышкой (любой кнопкой) должен выключать поиск
	fp.ProcessMouse(&vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		KeyDown:     true,
		ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseX:      5, MouseY: 5,
	})

	if fp.fastFindMode {
		t.Error("Mouse click should deactivate FastFind mode")
	}
}
func TestFileSystemPanel_DirectoryCache(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	v := vfs.NewOSVFS(t.TempDir())
	fp := NewFileSystemPanel(0, 0, 40, 20, v)

	// 1. Manually populate cache
	items := []vfs.VFSItem{
		{Name: "cached_file.txt", IsDir: false},
	}
	fp.saveToCache(v.GetPath(), items)

	// 2. Call readDirectoryEx and intercept before goroutine returns
	fp.readDirectoryEx(false)

	// At this exact moment, UI should have the cached entries!
	if len(fp.entries) < 2 { // ".." + "cached_file.txt"
		t.Fatalf("Cache not applied immediately, entries len: %d", len(fp.entries))
	}

	found := false
	for _, e := range fp.entries {
		if e.Name == "cached_file.txt" {
			found = true
			if !e.IsCached {
				t.Error("Cached entry IsCached flag not set")
			}
		}
	}
	if !found {
		t.Error("Cached file not found in panel entries")
	}
}

func TestFileSystemPanel_DirectoryCacheIsScopedBySession(t *testing.T) {
	connA, connB := new(int), new(int)
	newDevice := func(title string, session any) *mockCacheSessionVFS {
		return &mockCacheSessionVFS{
			mockTitleVFS: &mockTitleVFS{OSVFS: *vfs.NewOSVFS("/"), title: title},
			session:      session,
		}
	}
	deviceA := newDevice("SM_G930F (serial-a) [FISH+]", connA)
	deviceASecondView := newDevice("SM_G930F (serial-a) [FISH+]", connA)
	deviceB := newDevice("MiTV (serial-b) [FISH+]", connB)
	sameTitleNewSession := newDevice("SM_G930F (serial-a) [FISH+]", connB)

	fp := &FileSystemPanel{
		vfs:      deviceA,
		dirCache: make(map[dirCacheKey]dirCacheEntry),
	}
	fp.saveToCache("/", []vfs.VFSItem{{Name: "only-on-a.txt"}})

	if _, ok := fp.dirCache[directoryCacheKey(deviceASecondView, "/")]; !ok {
		t.Fatal("a second pooled view of the same session did not reuse its cache")
	}
	if _, ok := fp.dirCache[directoryCacheKey(deviceB, "/")]; ok {
		t.Fatal("two Android devices shared the cached root path")
	}
	if _, ok := fp.dirCache[directoryCacheKey(sameTitleNewSession, "/")]; ok {
		t.Fatal("a replacement session reused stale cache from the old connection")
	}
}

func TestDirectoryCacheIdentitySurvivesReconnection(t *testing.T) {
	stable := "cloud-profile-version"
	first := &mockStableCacheVFS{
		mockCacheSessionVFS: &mockCacheSessionVFS{mockTitleVFS: &mockTitleVFS{OSVFS: *vfs.NewOSVFS("/"), title: "Cloud"}, session: new(int)},
		stable:              stable,
	}
	second := &mockStableCacheVFS{
		mockCacheSessionVFS: &mockCacheSessionVFS{mockTitleVFS: &mockTitleVFS{OSVFS: *vfs.NewOSVFS("/"), title: "Cloud"}, session: new(int)},
		stable:              stable,
	}
	if got, want := directoryCacheKey(first, "Cloud:"+string(os.PathSeparator)+"Photos"), directoryCacheKey(second, "Cloud:"+string(os.PathSeparator)+"Photos"); got != want {
		t.Fatalf("stable directory cache keys differ across reconnect: %#v != %#v", got, want)
	}
}

func TestFileSystemPanelShowsStandaloneCacheBeforeProviderOpen(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	separator := string(os.PathSeparator)
	target := "zoin.shadow:" + separator + "Photos"
	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(t.TempDir()))
	if fp.cancelLoad != nil {
		fp.cancelLoad()
		fp.cancelLoad = nil
	}
	fp.dirCache = make(map[dirCacheKey]dirCacheEntry)
	fp.dirCache[dirCacheKey{identity: "old-cloud-session", qualifiedPath: target}] = dirCacheEntry{
		items: []vfs.VFSItem{{Name: "cached.jpg", Size: 42}},
		time:  time.Now(),
	}
	if !fp.showCachedStandalonePath(target) {
		t.Fatal("standalone visual path did not reuse its cached listing")
	}
	if len(fp.entries) != 2 || fp.entries[0].Name != ".." || fp.entries[1].Name != "cached.jpg" || !fp.entries[1].IsCached {
		t.Fatalf("cached preview entries = %#v", fp.entries)
	}
	foreign := "/"
	if os.PathSeparator == '/' {
		foreign = "\\"
	}
	if strings.Contains(target, foreign) {
		t.Fatalf("native visual target %q contains foreign separator %q", target, foreign)
	}
}

func TestDirectoryCacheKeyRejectsNonComparableSessionIdentity(t *testing.T) {
	device := &mockCacheSessionVFS{
		mockTitleVFS: &mockTitleVFS{OSVFS: *vfs.NewOSVFS("/"), title: "invalid session key"},
		session:      []byte("not comparable"),
	}
	cache := make(map[dirCacheKey]dirCacheEntry)
	cache[directoryCacheKey(device, "/")] = dirCacheEntry{}
	if len(cache) != 1 {
		t.Fatal("directory cache rejected safe fallback identity")
	}
}

func TestFileSystemPanel_CacheEviction(t *testing.T) {
	fp := &FileSystemPanel{}
	for i := 0; i < 60; i++ {
		fp.saveToCache(fmt.Sprintf("/path/%d", i), nil)
		time.Sleep(1 * time.Millisecond) // Ensure time diff
	}

	if len(fp.dirCache) > maxDirCache {
		t.Errorf("Cache exceeded max size: %d", len(fp.dirCache))
	}

	// The first inserted path "/path/0" should be evicted
	if _, ok := fp.dirCache[fp.cacheKey("/path/0")]; ok {
		t.Error("Oldest entry was not evicted")
	}
}

func TestFileSystemPanel_MaskSelection(t *testing.T) {
	// Initialize with a dummy table to avoid Refresh() nil pointer panic
	fp := &FileSystemPanel{
		table: vtui.NewTable(0, 0, 10, 10, nil),
		entries: []*fileEntry{
			{VFSItem: vfs.VFSItem{Name: ".."}},
			{VFSItem: vfs.VFSItem{Name: "readme.txt"}},
			{VFSItem: vfs.VFSItem{Name: "source.go"}},
			{VFSItem: vfs.VFSItem{Name: "config.json"}},
			{VFSItem: vfs.VFSItem{Name: "main.go"}},
		},
	}

	// 1. Select by mask *.go
	fp.ApplyMaskSelection("*.go", true)
	if !fp.entries[2].Selected || !fp.entries[4].Selected {
		t.Error("Mask selection failed for *.go")
	}
	if fp.entries[1].Selected || fp.entries[3].Selected {
		t.Error("Mask selection selected wrong files")
	}
	if fp.entries[0].Selected {
		t.Error("Mask selection should never select '..'")
	}

	// 2. Invert selection
	fp.InvertSelection()
	if fp.entries[2].Selected || fp.entries[4].Selected {
		t.Error("Inversion failed: .go files should be unselected")
	}
	if !fp.entries[1].Selected || !fp.entries[3].Selected {
		t.Error("Inversion failed: other files should be selected")
	}

	// 3. Deselect by mask
	fp.ApplyMaskSelection("*.json", false)
	if fp.entries[3].Selected {
		t.Error("Deselection failed for *.json")
	}
	if !fp.entries[1].Selected {
		t.Error("Deselection removed wrong files")
	}
}

func TestFileSystemPanel_TitleDoesNotContainSortIndicator(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	v := vfs.NewOSVFS(t.TempDir())
	fp := NewFileSystemPanel(0, 0, 40, 24, v)
	fp.currentTitle = "C:\\work"

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.SetDefaultPalette()

	fp.sortMode = SortExt
	fp.sortReverse = true
	fp.Show(scr)

	if got := rune(scr.GetCell(2, 0).Char); got != ' ' {
		t.Fatalf("title decoration starts with %q; want a space", got)
	}
	if got := rune(scr.GetCell(3, 0).Char); got != 'C' {
		t.Fatalf("first title character = %q; sort indicator still occupies the title", got)
	}
	if got := rune(scr.GetCell(10, 0).Char); got != ' ' {
		t.Fatalf("title decoration ends with %q; want a space", got)
	}

	fp.sortMode = SortSize
	fp.sortReverse = false
	fp.Show(scr)
	if got := rune(scr.GetCell(3, 0).Char); got != 'C' {
		t.Fatalf("changing sort mode changed the path title prefix to %q", got)
	}
}

func TestFileSystemPanel_CurrentTitleUsesSelectedColor(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	v := vfs.NewOSVFS(t.TempDir())
	fp := NewFileSystemPanel(0, 0, 40, 24, v)
	fp.currentTitle = "C:\\work"

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	oldTitle := vtui.Palette[ColPanelTitle]
	oldSelectedTitle := vtui.Palette[ColPanelSelectedTitle]
	defer func() {
		vtui.Palette[ColPanelTitle] = oldTitle
		vtui.Palette[ColPanelSelectedTitle] = oldSelectedTitle
	}()
	vtui.Palette[ColPanelTitle] = 0x1111
	vtui.Palette[ColPanelSelectedTitle] = 0x2222

	fp.SetFocus(false)
	fp.Show(scr)
	if got := scr.GetCell(3, 0).Attributes; got != vtui.Palette[ColPanelTitle] {
		t.Fatalf("inactive title attributes = %#x; want %#x", got, vtui.Palette[ColPanelTitle])
	}

	fp.SetFocus(true)
	fp.Show(scr)
	if got := scr.GetCell(3, 0).Attributes; got != vtui.Palette[ColPanelSelectedTitle] {
		t.Fatalf("active title attributes = %#x; want %#x", got, vtui.Palette[ColPanelSelectedTitle])
	}

	// Search-first keeps the current panel visually active while the command
	// line owns keyboard focus.
	fp.SetFocus(false)
	fp.showInactiveCursor = true
	fp.Show(scr)
	if got := scr.GetCell(3, 0).Attributes; got != vtui.Palette[ColPanelSelectedTitle] {
		t.Fatalf("current title with command-line focus attributes = %#x; want %#x", got, vtui.Palette[ColPanelSelectedTitle])
	}
}

func TestFileSystemPanel_FastFind_Visibility(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	// Создаем много файлов, чтобы список мог скроллиться
	v := vfs.NewOSVFS(t.TempDir())
	fp := NewFileSystemPanel(0, 0, 40, 10, v)
	fp.viewMode = ViewModeDetailed

	for i := 0; i < 20; i++ {
		fp.entries = append(fp.entries, &fileEntry{
			VFSItem: vfs.VFSItem{Name: fmt.Sprintf("file_%02d", i)},
		})
	}
	fp.Refresh()

	// Включаем поиск
	fp.fastFindMode = true
	// Ищем файл, который находится в самом низу текущего экрана (Row 8-9)
	// Окно поиска перекрывает нижние 2-3 строки.
	fp.fastFindStr = "file_08"
	fp.doFastFind(0)

	// Проверяем, что панель отскроллилась вверх, чтобы "file_08" не был
	// за окном поиска (в последних двух строках ViewHeight)
	H := fp.table.ViewHeight // Должно быть 8 (10 минус рамки)
	relRow := fp.cursorIdx - fp.table.TopPos

	if relRow >= H-2 {
		t.Errorf("Matched item is too low and obscured by search box. RelRow: %d, H: %d", relRow, H)
	}
}

func TestFileSystemPanel_Sorting(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	v := vfs.NewOSVFS(t.TempDir())
	fp := NewFileSystemPanel(0, 0, 80, 24, v)

	t1 := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "beta.txt", Size: 100, MTime: t1}},
		{VFSItem: vfs.VFSItem{Name: "alpha.exe", Size: 50, MTime: t2}},
		{VFSItem: vfs.VFSItem{Name: "folder", IsDir: true}},
	}

	// 1. Sort by Name
	fp.sortMode = SortName
	fp.sortReverse = false
	fp.sortEntries()
	// Expected: .., folder, alpha.exe, beta.txt
	if fp.entries[1].Name != "folder" || fp.entries[2].Name != "alpha.exe" {
		t.Errorf("SortName failed: index 1=%s, index 2=%s", fp.entries[1].Name, fp.entries[2].Name)
	}

	// 2. Sort by Size
	fp.sortMode = SortSize
	fp.sortReverse = false // Descending (large first)
	fp.sortEntries()
	// Expected: .., folder, beta.txt (100), alpha.exe (50)
	if fp.entries[2].Name != "beta.txt" {
		t.Errorf("SortSize failed: index 2=%s", fp.entries[2].Name)
	}

	// 3. Sort by Time
	fp.sortMode = SortTime
	fp.sortReverse = false // Descending (newest first)
	fp.sortEntries()
	// Expected: .., folder, alpha.exe (2024), beta.txt (2023)
	if fp.entries[2].Name != "alpha.exe" {
		t.Errorf("SortTime failed: index 2=%s", fp.entries[2].Name)
	}

	// 4. Test logic in SetSortMode
	fp.SetSortMode(SortName) // Should set reverse = false
	if fp.sortReverse {
		t.Error("SetSortMode(Name) should reset reverse to false")
	}

	fp.SetSortMode(SortName) // Toggle reverse
	if !fp.sortReverse {
		t.Error("SetSortMode(Name) second call should toggle reverse to true")
	}
}

func TestFileSystemPanel_SetSortModeUsesModeDefaultDirection(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewNullVFS(0))
	if fp.cancelLoad != nil {
		fp.cancelLoad()
	}

	tests := []struct {
		name      string
		mode      SortMode
		ascending bool
	}{
		{name: "name ascending", mode: SortName, ascending: true},
		{name: "extension ascending", mode: SortExt, ascending: true},
		{name: "time descending", mode: SortTime, ascending: false},
		{name: "size descending", mode: SortSize, ascending: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Enter from another mode so this is the first activation, not the
			// repeated-activation toggle path.
			fp.sortMode = SortUnsorted
			fp.sortReverse = true
			fp.SetSortMode(tc.mode)
			if fp.sortReverse {
				t.Fatalf("first activation left sortReverse enabled for mode %v", tc.mode)
			}
			if got := fp.sortIsAscending(); got != tc.ascending {
				t.Fatalf("first activation ascending = %v, want %v", got, tc.ascending)
			}

			fp.SetSortMode(tc.mode)
			if !fp.sortReverse || fp.sortIsAscending() == tc.ascending {
				t.Fatalf("second activation did not reverse mode %v", tc.mode)
			}
		})
	}
}

func TestFileSystemPanel_SortColumnIndicators(t *testing.T) {
	for _, tc := range []struct {
		name         string
		viewMode     ViewMode
		sortMode     SortMode
		reverse      bool
		column       int
		wantSuffix   string
		rightAligned bool
	}{
		{name: "brief name ascending", viewMode: ViewModeBrief, sortMode: SortName, column: 0, wantSuffix: " ↑"},
		{name: "medium name descending", viewMode: ViewModeMedium, sortMode: SortName, reverse: true, column: 1, wantSuffix: " ↓"},
		{name: "detailed size descending", viewMode: ViewModeDetailed, sortMode: SortSize, column: 1, wantSuffix: " ↓"},
		{name: "detailed size ascending", viewMode: ViewModeDetailed, sortMode: SortSize, reverse: true, column: 1, wantSuffix: " ↑"},
		{name: "wide time descending", viewMode: ViewModeWide, sortMode: SortTime, column: 2, wantSuffix: " ↓"},
		{name: "brief hidden size", viewMode: ViewModeBrief, sortMode: SortSize, column: 0, wantSuffix: "[" + Msg("Menu.SortSize") + "]↓", rightAligned: true},
		{name: "medium hidden time", viewMode: ViewModeMedium, sortMode: SortTime, reverse: true, column: 0, wantSuffix: "[" + Msg("Menu.SortTime") + "]↑", rightAligned: true},
		{name: "detailed hidden extension", viewMode: ViewModeDetailed, sortMode: SortExt, column: 0, wantSuffix: "[" + Msg("Menu.SortExt") + "]↑", rightAligned: true},
		{name: "wide hidden extension reversed", viewMode: ViewModeWide, sortMode: SortExt, reverse: true, column: 0, wantSuffix: "[" + Msg("Menu.SortExt") + "]↓", rightAligned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newPanelScrollTestFixture(tc.viewMode, 1)
			fp.frame = vtui.NewBorderedFrame(0, 0, 79, 11, vtui.SingleBox, "")
			fp.sortMode = tc.sortMode
			fp.sortReverse = tc.reverse
			fp.Resize(80, 12)
			if got := fp.table.Columns[tc.column].Title; !strings.HasSuffix(got, tc.wantSuffix) {
				t.Fatalf("column title %q does not end in %q", got, tc.wantSuffix)
			} else if tc.rightAligned && runewidth.StringWidth(got) != fp.table.Columns[tc.column].Width {
				t.Fatalf("hidden sort title width = %d, want column width %d: %q",
					runewidth.StringWidth(got), fp.table.Columns[tc.column].Width, got)
			}
		})
	}

	narrow := hiddenSortColumnTitle(SortExt, true, 12)
	if runewidth.StringWidth(narrow) > 12 || !strings.HasSuffix(narrow, "]↑") {
		t.Fatalf("narrow hidden sort title lost brackets/arrow: %q", narrow)
	}
}

func TestFileSystemPanel_HeaderClickSortsAndToggles(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(t.TempDir()))
	fp.SetViewMode(ViewModeDetailed)
	fp.sortMode = SortUnsorted
	fp.sortReverse = false
	fp.updateSortColumnTitles()

	clickName := func(flags uint32) bool {
		return fp.ProcessMouse(&vtinput.InputEvent{
			Type: vtinput.MouseEventType, KeyDown: true,
			MouseX: int16(fp.table.X1), MouseY: int16(fp.table.Y1),
			ButtonState: vtinput.FromLeft1stButtonPressed, MouseEventFlags: flags,
		})
	}
	release := func() {
		fp.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType})
	}

	if !clickName(0) || fp.sortMode != SortName || fp.sortReverse {
		t.Fatalf("first Name header click: mode=%v reverse=%v", fp.sortMode, fp.sortReverse)
	}
	if !fp.headerMouseActive {
		t.Fatal("header click was not marked as a header gesture")
	}
	release()
	if !clickName(vtinput.DoubleClick) || fp.sortMode != SortName || !fp.sortReverse {
		t.Fatalf("second Name header click: mode=%v reverse=%v", fp.sortMode, fp.sortReverse)
	}
	if !strings.HasSuffix(fp.table.Columns[0].Title, " ↓") {
		t.Fatalf("reversed Name title has no down arrow: %q", fp.table.Columns[0].Title)
	}

	// Separator clicks do not select either adjacent sort column.
	release()
	separatorX := fp.table.X1 + fp.table.Columns[0].Width
	if _, ok := fp.headerSortModeAt(separatorX, fp.table.Y1); ok {
		t.Fatal("column separator was treated as a sortable header")
	}
}

func TestFileSystemPanel_HeaderSortMappingAllViewModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode ViewMode
		want []SortMode
	}{
		{name: "brief", mode: ViewModeBrief, want: []SortMode{SortName, SortName, SortName}},
		{name: "medium", mode: ViewModeMedium, want: []SortMode{SortName, SortName}},
		{name: "detailed", mode: ViewModeDetailed, want: []SortMode{SortName, SortSize}},
		{name: "wide", mode: ViewModeWide, want: []SortMode{SortName, SortSize, SortTime}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newPanelScrollTestFixture(tc.mode, 1)
			fp.frame = vtui.NewBorderedFrame(0, 0, 79, 11, vtui.SingleBox, "")
			fp.Resize(80, 12)
			x := fp.table.X1
			for column, want := range tc.want {
				mode, ok := fp.headerSortModeAt(x, fp.table.Y1)
				if !ok || mode != want {
					t.Fatalf("column %d maps to %v,%v; want %v,true", column, mode, ok, want)
				}
				x += fp.table.Columns[column].Width + 1
			}
		})
	}
}

/*
func TestDummyFailure(t *testing.T) {
    vtui.DebugLog("This is a trace log before failure.")
    t.Fatal("Intentional failure for log dump test")
}
*/
// waitForLoad is a test helper to wait for a panel's async loading to complete.
func waitForPanelSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for %s", description)
	}
}

func waitForLoad(t *testing.T, fp *FileSystemPanel) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for fp.isLoading {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for panel to load")
		}
	}
	// Drain any final UI tasks after isLoading becomes false
	for i := 0; i < 5; i++ {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			return
		}
	}
}

func TestFileSystemPanel_PermissionFailureRestoresAbsoluteParentWithoutDialogLoop(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldConfig := AppConfig
	AppConfig.SyncPanelLoad = true
	AppConfig.ShowHiddenFiles = true
	defer func() { AppConfig = oldConfig }()

	remote := newAbsoluteRecoveryVFS()
	fp := NewFileSystemPanel(0, 0, 60, 20, remote)
	t.Cleanup(func() {
		if fp.cancelLoad != nil {
			fp.cancelLoad()
		}
		if fp.loadingTimer != nil {
			fp.loadingTimer.Stop()
		}
	})
	waitForLoad(t, fp)

	if got := remote.GetPath(); got != "/" {
		t.Fatalf("path after denied /SystemData read = %q, want root", got)
	}
	if got := remote.readDirCalls.Load(); got != 2 {
		t.Fatalf("ReadDir calls = %d, want one denied child read and one root recovery read", got)
	}
	if got := fp.getRawSelectedName(); got != "SystemData" {
		t.Fatalf("cursor after recovery = %q, want denied folder", got)
	}

	countLiveErrors := func() int {
		count := 0
		for _, frame := range vtui.FrameManager.GetActiveFrames(vtui.FrameManager.ActiveIdx) {
			if frame.GetType() == vtui.TypeDialog && !frame.IsDone() && frame.GetTitle() == " Error " {
				count++
			}
		}
		return count
	}
	trackedDialog := fp.directoryErrorDialog
	if trackedDialog == nil || trackedDialog.IsDone() {
		t.Fatal("directory error dialog was not tracked as a live dialog")
	}
	liveBeforeDuplicate := countLiveErrors()

	// Even if another completion reports an error before the user presses OK,
	// this panel must keep its existing modal. Other tests may have left their
	// own dialogs in the process-wide FrameManager, so compare against the
	// baseline instead of assuming this is the only dialog globally.
	fp.showDirectoryError(" Error ", "second asynchronous failure")
	if fp.directoryErrorDialog != trackedDialog {
		t.Fatal("duplicate failure replaced the tracked directory error dialog")
	}
	if got := countLiveErrors(); got != liveBeforeDuplicate {
		t.Fatalf("live directory error dialogs after duplicate = %d, want unchanged %d", got, liveBeforeDuplicate)
	}
	trackedDialog.SetExitCode(0)
	if fp.directoryErrorDialog != nil {
		t.Fatal("closing the error dialog did not clear its deduplication slot")
	}
}

func TestFileSystemPanel_StructLiteralLazySelection(t *testing.T) {
	// Create panel as a struct literal (nil selectedItems map)
	fp := &FileSystemPanel{}
	fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "test.txt"}}}

	// This should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Panel panicked on selection toggle with nil map: %v", r)
		}
	}()

	fp.SetItemSelected(0, true)
	if fp.selectedItems == nil || !fp.selectedItems["test.txt"] {
		t.Error("Lazy initialization failed to record selection")
	}
}

func TestFileSystemPanel_CacheLoadPreservesSelection(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	v := vfs.NewOSVFS(t.TempDir())
	fp := NewFileSystemPanel(0, 0, 40, 20, v)

	// Simulate items loaded and selected
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "keep.txt"}},
	}
	fp.SetItemSelected(1, true)

	// Populate cache
	fp.saveToCache(v.GetPath(), []vfs.VFSItem{{Name: "keep.txt"}})

	// Trigger a reload (which will hit the cache synchronously)
	fp.readDirectoryEx(false)

	// The synchronous cache load should have preserved the selection
	found := false
	for _, e := range fp.entries {
		if e.Name == "keep.txt" {
			found = true
			if !e.Selected {
				t.Error("Synchronous cache load wiped out the selection state")
			}
		}
	}
	if !found {
		t.Error("keep.txt not found in panel after cache reload")
	}
}

func TestFileSystemPanel_SyncPanelLoad(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// Save original config and restore after test
	oldSync := AppConfig.SyncPanelLoad
	AppConfig.SyncPanelLoad = true
	defer func() { AppConfig.SyncPanelLoad = oldSync }()

	tmpDir := t.TempDir()
	v := vfs.NewOSVFS(tmpDir)
	os.WriteFile(filepath.Join(tmpDir, "file_sync.txt"), []byte("data"), 0644)

	fp := NewFileSystemPanel(0, 0, 40, 20, v)

	// 1. Manually populate cache with a different item to see if it gets ignored
	items := []vfs.VFSItem{
		{Name: "cached_file.txt", IsDir: false},
	}
	fp.saveToCache(v.GetPath(), items)

	// 2. Call readDirectoryEx. Since SyncPanelLoad is true, it MUST NOT load "cached_file.txt" from cache.
	fp.readDirectoryEx(false)

	// Verify that cache was ignored (fp.entries should not contain "cached_file.txt" immediately)
	for _, e := range fp.entries {
		if e.Name == "cached_file.txt" {
			t.Error("VFS loaded cached entry immediately, but SyncPanelLoad is enabled")
		}
	}

	// 3. Wait for the async load to complete (should load "file_sync.txt" from disk)
	waitForLoad(t, fp)

	foundReal := false
	for _, e := range fp.entries {
		if e.Name == "file_sync.txt" {
			foundReal = true
		}
		if e.Name == "cached_file.txt" {
			t.Error("Cached file appeared after loading, but it should have been overwritten by real disk contents")
		}
	}

	if !foundReal {
		t.Error("Failed to load real file from disk under SyncPanelLoad")
	}
}

func TestFileSystemPanel_SelectionCleanupAfterReload(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	v := vfs.NewOSVFS(t.TempDir())
	fp := NewFileSystemPanel(0, 0, 40, 20, v)

	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "ghost.txt"}},
	}
	fp.SetItemSelected(1, true)

	// Verify it is in the map initially
	if !fp.selectedItems["ghost.txt"] {
		t.Fatal("Initial selection missing from map")
	}

	// Now simulate a reload where "ghost.txt" is NOT in the VFS results
	// (we load a cache that has a different file)
	fp.saveToCache(v.GetPath(), []vfs.VFSItem{{Name: "other.txt"}})
	fp.readDirectoryEx(false)

	// Process the tasks
	waitForLoad(t, fp)

	// Verify "ghost.txt" was removed from selectedItems map because it no longer exists on disk
	if fp.selectedItems["ghost.txt"] {
		t.Error("Ghost selection was not cleaned up from selectedItems map after reload")
	}
}

func TestFileSystemPanel_Cache_FullCycle(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmpDir := t.TempDir()
	v := vfs.NewOSVFS(tmpDir)

	// 1. Initial setup
	os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("b"), 0644)

	fp := NewFileSystemPanel(0, 0, 40, 20, v)

	// 2. Initial fresh read
	waitForLoad(t, fp)

	// 3. Verify cache was populated
	if _, ok := fp.dirCache[fp.cacheKey(tmpDir)]; !ok {
		t.Fatal("Cache not populated after initial read")
	}
	if len(fp.entries) != 3 { // .., a.txt, b.txt
		t.Fatalf("Expected 3 entries, got %d", len(fp.entries))
	}
	for _, e := range fp.entries {
		if e.IsCached {
			t.Fatal("Initial read should not produce cached entries")
		}
	}

	// 4. Set cursor and modify backend
	fp.SelectName("b.txt")
	os.WriteFile(filepath.Join(tmpDir, "c.txt"), []byte("c"), 0644)
	os.Remove(filepath.Join(tmpDir, "a.txt"))

	// 5. Trigger cached read
	fp.readDirectoryEx(false)

	// 6. IMMEDIATE CHECKS (before async finishes)
	if len(fp.entries) != 3 {
		t.Fatalf("Immediately after reload, should show 3 cached entries, got %d", len(fp.entries))
	}
	if fp.GetSelectedName() != "b.txt" {
		t.Errorf("Cursor position lost, expected 'b.txt', got %q", fp.GetSelectedName())
	}
	foundA := false
	for _, e := range fp.entries {
		if e.Name == "c.txt" {
			t.Error("'c.txt' should not be visible in cached view")
		}
		if e.Name == "a.txt" {
			foundA = true
		}
		if e.Name != ".." && !e.IsCached {
			t.Errorf("Entry %q should be marked as cached", e.Name)
		}
	}
	if !foundA {
		t.Error("'a.txt' should still be visible in cached view")
	}

	// 7. ASYNC CHECKS (let the real read complete)
	waitForLoad(t, fp)

	if len(fp.entries) != 3 { // .., b.txt, c.txt
		t.Fatalf("After async update, expected 3 entries, got %d", len(fp.entries))
	}
	if fp.GetSelectedName() != "b.txt" {
		t.Errorf("Cursor position lost after async update, expected 'b.txt', got %q", fp.GetSelectedName())
	}
	foundC := false
	for _, e := range fp.entries {
		if e.Name == "a.txt" {
			t.Error("'a.txt' should have been removed after async update")
		}
		if e.Name == "c.txt" {
			foundC = true
		}
		if e.IsCached {
			t.Errorf("Entry %q should NOT be marked as cached after async update", e.Name)
		}
	}
	if !foundC {
		t.Error("'c.txt' not found after async update")
	}
}

func TestFileSystemPanel_CacheSwapPreservesLiveCursorAndMarks(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldConfig := AppConfig
	AppConfig.SyncPanelLoad = false
	AppConfig.ShowHiddenFiles = true
	defer func() { AppConfig = oldConfig }()

	remote := newStagedPanelVFS(
		[]vfs.VFSItem{{Name: "aardvark.txt"}, {Name: "alpha.txt"}},
		[]vfs.VFSItem{{Name: "beta.txt"}, {Name: "delta.txt"}, {Name: "gamma.txt"}},
	)
	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(t.TempDir()))
	waitForLoad(t, fp)
	fp.vfs = remote
	fp.lastLoadedPath = remote.GetPath()
	fp.pendingSelection = ".."
	fp.selectedItems = map[string]bool{"alpha.txt": true}
	fp.saveToCache(remote.GetPath(), []vfs.VFSItem{
		{Name: "alpha.txt"},
		{Name: "beta.txt"},
		{Name: "gamma.txt"},
	})

	fp.readDirectoryEx(false)
	waitForPanelSignal(t, remote.started, "staged ReadDir to start")

	entryIndex := func(name string) int {
		for i, entry := range fp.entries {
			if entry.Name == name {
				return i
			}
		}
		return -1
	}
	alpha := entryIndex("alpha.txt")
	beta := entryIndex("beta.txt")
	gamma := entryIndex("gamma.txt")
	if alpha < 0 || beta < 0 || gamma < 0 {
		t.Fatalf("cached entries missing: %+v", fp.entries)
	}
	if !fp.entries[alpha].Selected {
		t.Fatal("cached alpha.txt did not restore its mark")
	}

	// These actions happen after the gray cache is visible and while ReadDir
	// is blocked, which is the exact interaction that used to be overwritten.
	fp.SetCursorIndex(beta)
	fp.SetItemSelected(alpha, false)
	fp.SetItemSelected(gamma, true)

	remote.release[0] <- struct{}{}
	waitForPanelSignal(t, remote.delivered[0], "first fresh chunk")
	if got := fp.getRawSelectedName(); got != "beta.txt" {
		t.Fatalf("first chunk moved cached cursor to %q, want beta.txt", got)
	}
	for _, entry := range fp.entries {
		if entry.Name != ".." && !entry.IsCached {
			t.Fatalf("partial fresh chunk replaced cached row %q", entry.Name)
		}
	}
	if fp.entries[entryIndex("alpha.txt")].Selected || !fp.entries[entryIndex("gamma.txt")].Selected {
		t.Fatal("marks changed while the first fresh chunk was pending")
	}

	remote.release[1] <- struct{}{}
	waitForLoad(t, fp)

	if got := fp.getRawSelectedName(); got != "beta.txt" {
		t.Fatalf("atomic cache swap moved cursor to %q, want beta.txt", got)
	}
	if got := fp.GetCursorIndex(); got != 3 { // .., aardvark, alpha, beta
		t.Fatalf("beta.txt cursor index = %d, want shifted index 3", got)
	}
	for _, entry := range fp.entries {
		if entry.IsCached {
			t.Fatalf("fresh row %q is still marked as cached", entry.Name)
		}
	}
	alpha = entryIndex("alpha.txt")
	gamma = entryIndex("gamma.txt")
	if alpha < 0 || gamma < 0 {
		t.Fatalf("fresh entries missing: %+v", fp.entries)
	}
	if fp.entries[alpha].Selected || fp.selectedItems["alpha.txt"] {
		t.Fatal("alpha.txt deselection was lost during cache swap")
	}
	if !fp.entries[gamma].Selected || !fp.selectedItems["gamma.txt"] {
		t.Fatal("gamma.txt mark was lost during cache swap")
	}
}

func TestFileSystemPanel_CacheSwapUsesNearestRowWhenFocusedItemDisappears(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldConfig := AppConfig
	AppConfig.SyncPanelLoad = false
	AppConfig.ShowHiddenFiles = true
	defer func() { AppConfig = oldConfig }()

	remote := newStagedPanelVFS(
		[]vfs.VFSItem{{Name: "alpha.txt"}},
		[]vfs.VFSItem{{Name: "gamma.txt"}},
	)
	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(t.TempDir()))
	waitForLoad(t, fp)
	fp.vfs = remote
	fp.lastLoadedPath = remote.GetPath()
	fp.pendingSelection = ".."
	fp.saveToCache(remote.GetPath(), []vfs.VFSItem{
		{Name: "alpha.txt"},
		{Name: "beta.txt"},
		{Name: "gamma.txt"},
	})

	fp.readDirectoryEx(false)
	waitForPanelSignal(t, remote.started, "staged ReadDir to start")
	for i, entry := range fp.entries {
		if entry.Name == "beta.txt" {
			fp.SetCursorIndex(i)
			fp.SetItemSelected(i, true)
			break
		}
	}
	if got := fp.getRawSelectedName(); got != "beta.txt" {
		t.Fatalf("failed to focus cached beta.txt, got %q", got)
	}

	remote.release[0] <- struct{}{}
	waitForPanelSignal(t, remote.delivered[0], "first fresh chunk")
	remote.release[1] <- struct{}{}
	waitForLoad(t, fp)

	if got := fp.getRawSelectedName(); got != "gamma.txt" {
		t.Fatalf("removed beta.txt should fall through to nearest row gamma.txt, got %q", got)
	}
	if fp.selectedItems["beta.txt"] {
		t.Fatal("mark for removed beta.txt survived fresh directory replacement")
	}
}

func TestFileSystemPanel_DirectoryLoadQueueKeepsOnlyLatest(t *testing.T) {
	fp := &FileSystemPanel{}
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	latestRan := make(chan struct{})
	var middleRuns atomic.Int64

	fp.enqueueDirectoryLoad(func() {
		close(firstStarted)
		<-releaseFirst
	})
	waitForPanelSignal(t, firstStarted, "first directory load")
	fp.enqueueDirectoryLoad(func() { middleRuns.Add(1) })
	fp.enqueueDirectoryLoad(func() { close(latestRan) })
	close(releaseFirst)
	waitForPanelSignal(t, latestRan, "latest directory load")

	if got := middleRuns.Load(); got != 0 {
		t.Fatalf("superseded pending load ran %d times", got)
	}
}

func TestFileSystemPanel_OrdinaryDirectoryDoesNotUseFileProvider(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	manager := newBlockingProviderManagerVFS("backup.zip")
	provider := newBlockingMountProvider(manager)
	provider.virtualDirs = false
	vfs.RegisterProvider(provider)
	fp := NewFileSystemPanel(0, 0, 40, 20, manager)
	t.Cleanup(func() {
		if fp.cancelLoad != nil {
			fp.cancelLoad()
		}
		if fp.loadingTimer != nil {
			fp.loadingTimer.Stop()
		}
	})
	waitForLoad(t, fp)

	enter := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}
	if !fp.ProcessKey(enter) {
		t.Fatal("directory Enter was not handled")
	}
	if got := provider.openCalls.Load(); got != 0 {
		t.Fatalf("ordinary directory started %d provider opens", got)
	}
	if got := manager.setPathCalls.Load(); got != 1 {
		t.Fatalf("ordinary directory made %d SetPath calls, want 1", got)
	}
}

func TestFileSystemPanel_HeldEnterDuringProviderOpenIsCoalesced(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	manager := newBlockingProviderManagerVFS("SM_G930F (serial)")
	provider := newBlockingMountProvider(manager)
	vfs.RegisterProvider(provider)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(provider.release) }) }
	t.Cleanup(release)

	fp := NewFileSystemPanel(0, 0, 40, 20, manager)
	t.Cleanup(func() {
		fp.cancelProviderOpen()
		if fp.cancelLoad != nil {
			fp.cancelLoad()
		}
		if fp.loadingTimer != nil {
			fp.loadingTimer.Stop()
		}
	})
	waitForLoad(t, fp)
	if got := fp.getRawSelectedName(); got != manager.rowName {
		t.Fatalf("initial manager cursor = %q, want %q", got, manager.rowName)
	}

	enter := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}
	if !fp.ProcessKey(enter) {
		t.Fatal("first provider Enter was not handled")
	}
	waitForPanelSignal(t, provider.started, "blocked provider open")
	if got := fp.getRawSelectedName(); got != manager.rowName {
		t.Fatalf("provider transition exposed synthetic row %q", got)
	}

	for i := 0; i < 50; i++ {
		if !fp.ProcessKey(enter) {
			t.Fatalf("repeated provider Enter %d was not consumed", i)
		}
	}
	if got := provider.openCalls.Load(); got != 1 {
		t.Fatalf("held Enter started %d provider opens, want 1", got)
	}
	if got := manager.setPathCalls.Load(); got != 0 {
		t.Fatalf("held Enter made %d manager SetPath calls", got)
	}
	if fp.vfs != manager {
		t.Fatalf("panel left manager before provider completed: %T", fp.vfs)
	}

	release()
	deadline := time.After(2 * time.Second)
	for fp.providerOpenTask != nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("timeout waiting for provider completion")
		}
	}
	waitForLoad(t, fp)
	if fp.vfs != provider.result {
		t.Fatalf("provider result was not installed: %T", fp.vfs)
	}
	if got := fp.getRawSelectedName(); got != ".." {
		t.Fatalf("mounted root cursor = %q, want real parent row", got)
	}

	// Once the real child exists, a later repeat may activate its genuine ".."
	// and return to the cached manager view.
	if !fp.ProcessKey(enter) {
		t.Fatal("Enter on mounted parent row was not handled")
	}
	waitForLoad(t, fp)
	if fp.vfs != manager {
		t.Fatalf("parent row did not restore manager: %T", fp.vfs)
	}
	if got := provider.result.closeCalls.Load(); got != 1 {
		t.Fatalf("mounted VFS close calls = %d, want 1", got)
	}
	if got := fp.getRawSelectedName(); got != manager.rowName {
		t.Fatalf("manager cursor after return = %q, want %q", got, manager.rowName)
	}
}

func TestFileSystemPanel_HeldEnterReturnWithSyncLoadNeverActivatesStaleUpRow(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldConfig := AppConfig
	AppConfig.SyncPanelLoad = true
	defer func() { AppConfig = oldConfig }()

	manager := newBlockingProviderManagerVFS("SM_G930F (sync-load)")
	provider := newBlockingMountProvider(manager)
	vfs.RegisterProvider(provider)
	var providerReleaseOnce sync.Once
	releaseProvider := func() { providerReleaseOnce.Do(func() { close(provider.release) }) }
	var managerReleaseOnce sync.Once
	releaseManager := func() { managerReleaseOnce.Do(func() { close(manager.releaseBlockedRead) }) }
	t.Cleanup(releaseProvider)
	t.Cleanup(releaseManager)

	fp := NewFileSystemPanel(0, 0, 40, 20, manager)
	t.Cleanup(func() {
		fp.cancelProviderOpen()
		if fp.cancelLoad != nil {
			fp.cancelLoad()
		}
		if fp.loadingTimer != nil {
			fp.loadingTimer.Stop()
		}
	})
	waitForLoad(t, fp)

	enter := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}
	if !fp.ProcessKey(enter) {
		t.Fatal("provider Enter was not handled")
	}
	waitForPanelSignal(t, provider.started, "provider open before sync-load return")
	releaseProvider()
	deadline := time.After(2 * time.Second)
	for fp.providerOpenTask != nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("timeout waiting for mounted provider")
		}
	}
	waitForLoad(t, fp)
	if fp.vfs != provider.result || fp.getRawSelectedName() != ".." {
		t.Fatalf("mounted provider view = %T cursor=%q, want child parent row", fp.vfs, fp.getRawSelectedName())
	}

	// SyncPanelLoad deliberately does not render the manager cache. Hold its
	// fresh ReadDir so the interval that used to expose the child's stale ".."
	// is deterministic.
	manager.blockNextRead.Store(true)
	if !fp.ProcessKey(enter) {
		t.Fatal("Enter on mounted parent row was not handled")
	}
	if fp.vfs != manager {
		t.Fatalf("mounted parent row did not install manager: %T", fp.vfs)
	}
	waitForPanelSignal(t, manager.blockedReadStarted, "blocked manager refresh after child return")
	if len(fp.entries) != 0 {
		t.Fatalf("manager root retained stale child rows: %+v", fp.entries)
	}

	// Empty loading rows make ordinary repeats harmless. Also inject the exact
	// stale row from the regression to verify the root-without-parent safety net
	// independently of the atomic loading view.
	for i := 0; i < 50; i++ {
		_ = fp.ProcessKey(enter)
	}
	fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}}
	fp.SetCursorIndex(0)
	fp.Refresh()
	for i := 0; i < 50; i++ {
		if !fp.ProcessKey(enter) {
			t.Fatalf("stale root parent repeat %d was not consumed", i)
		}
	}
	if got := manager.setPathCalls.Load(); got != 0 {
		t.Fatalf("held Enter made %d manager SetPath calls", got)
	}
	if got := provider.openCalls.Load(); got != 1 {
		t.Fatalf("held Enter started %d provider opens before manager listing, want 1", got)
	}

	releaseManager()
	waitForLoad(t, fp)
	if got := fp.getRawSelectedName(); got != manager.rowName {
		t.Fatalf("fresh manager cursor = %q, want %q", got, manager.rowName)
	}
	if got := manager.setPathCalls.Load(); got != 0 {
		t.Fatalf("fresh manager swap followed %d stale parent paths", got)
	}
}

func TestFileSystemPanel_StaleProviderCompletionCannotHijackPanel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	manager := newBlockingProviderManagerVFS("slow-device")
	provider := newBlockingMountProvider(manager)
	vfs.RegisterProvider(provider)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(provider.release) }) }
	t.Cleanup(release)

	fp := NewFileSystemPanel(0, 0, 40, 20, manager)
	t.Cleanup(func() {
		fp.cancelProviderOpen()
		if fp.cancelLoad != nil {
			fp.cancelLoad()
		}
		if fp.loadingTimer != nil {
			fp.loadingTimer.Stop()
		}
	})
	waitForLoad(t, fp)
	enter := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}
	if !fp.ProcessKey(enter) {
		t.Fatal("provider Enter was not handled")
	}
	waitForPanelSignal(t, provider.started, "stale provider open")

	// Model a VFS switch performed elsewhere. Even without the normal explicit
	// cancellation hook, the captured source identity must reject the late Open.
	replacement := vfs.NewNullVFS(0)
	fp.vfs = replacement
	fp.isLoading = false
	fp.providerEntryName = "replacement-provider"
	fp.pendingSelection = "replacement-selection"
	release()

	deadline := time.After(2 * time.Second)
	for provider.result.closeCalls.Load() == 0 {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("timeout waiting for stale provider cleanup")
		}
	}
	if fp.vfs != replacement {
		t.Fatalf("stale provider hijacked panel with %T", fp.vfs)
	}
	if fp.providerOpenTask != nil {
		t.Fatal("stale provider left transition latched")
	}
	if got := provider.result.closeCalls.Load(); got != 1 {
		t.Fatalf("stale provider result close calls = %d, want 1", got)
	}
	if fp.providerEntryName != "replacement-provider" || fp.pendingSelection != "replacement-selection" {
		t.Fatalf("stale completion changed new panel state: provider=%q selection=%q", fp.providerEntryName, fp.pendingSelection)
	}
}

func TestPanelsFrame_FailedNavigationRestoresCanceledProviderLoadingState(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	manager := newBlockingProviderManagerVFS("slow-device-for-navigation")
	provider := newBlockingMountProvider(manager)
	vfs.RegisterProvider(provider)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(provider.release) }) }
	t.Cleanup(release)

	fp := NewFileSystemPanel(0, 0, 40, 20, manager)
	t.Cleanup(func() {
		fp.cancelProviderOpen()
		if fp.cancelLoad != nil {
			fp.cancelLoad()
		}
		if fp.loadingTimer != nil {
			fp.loadingTimer.Stop()
		}
	})
	waitForLoad(t, fp)
	enter := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}
	if !fp.ProcessKey(enter) {
		t.Fatal("provider Enter was not handled")
	}
	waitForPanelSignal(t, provider.started, "provider canceled by navigation")
	if !fp.isLoading || fp.providerOpenTask == nil {
		t.Fatal("provider transition was not marked active")
	}

	pf := &PanelsFrame{}
	if pf.NavigateToPath(fp, "missing-directory") {
		t.Fatal("manager accepted missing navigation target")
	}
	if fp.providerOpenTask != nil {
		t.Fatal("failed navigation did not cancel provider transition")
	}
	if fp.isLoading {
		t.Fatal("failed navigation left canceled provider loading state active")
	}

	release()
	deadline := time.After(2 * time.Second)
	for provider.result.closeCalls.Load() == 0 {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("timeout waiting for canceled provider cleanup")
		}
	}
	if fp.vfs != manager {
		t.Fatalf("canceled provider replaced manager with %T", fp.vfs)
	}
}

func TestFileSystemPanel_CachedEnterStaysResponsiveAndCoalescesRefresh(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldConfig := AppConfig
	AppConfig.SyncPanelLoad = false
	AppConfig.ShowHiddenFiles = true
	defer func() { AppConfig = oldConfig }()

	oldDisable := DisableLoadingAnimationInTests
	DisableLoadingAnimationInTests = false
	defer func() { DisableLoadingAnimationInTests = oldDisable }()

	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(t.TempDir()))
	waitForLoad(t, fp)
	remote := newQueuedNavigationVFS()
	t.Cleanup(func() {
		select {
		case remote.releaseFirstRead <- struct{}{}:
		default:
		}
	})
	fp.vfs = remote
	fp.lastLoadedPath = "/"
	fp.dirCache = make(map[dirCacheKey]dirCacheEntry)
	fp.saveToCache("/", remote.items["/"])
	fp.saveToCache("/sdcard", remote.items["/sdcard"])
	fp.pendingSelection = "sdcard"

	// Start a root refresh and leave it in the same state as an in-flight
	// FISH response that must drain after cancellation.
	fp.readDirectoryEx(false)
	waitForPanelSignal(t, remote.firstReadStarted, "blocked root refresh")
	time.Sleep(220 * time.Millisecond) // let the old loading timer enter TaskChan

	start := time.Now()
	if !fp.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN,
	}) {
		t.Fatal("Enter on cached sdcard was not handled")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("cached Enter blocked for %s", elapsed)
	}
	if got := remote.checkedPathCalls.Load(); got != 0 {
		t.Fatalf("cached Enter made %d checked/remote SetPath calls", got)
	}
	if got := remote.GetPath(); got != "/sdcard" {
		t.Fatalf("path after cached Enter = %q, want /sdcard", got)
	}
	if got := fp.currentTitle; !strings.Contains(got, "/sdcard") || !strings.HasSuffix(got, panelLoadingPulse[0]) {
		t.Fatalf("loading title was not updated immediately: %q", got)
	}
	if got := fp.getRawSelectedName(); got != ".." {
		t.Fatalf("sdcard cache was not rendered synchronously, cursor = %q", got)
	}

	initialLoadingTitle := fp.currentTitle
	pulseDeadline := time.After(time.Second)
	for fp.currentTitle == initialLoadingTitle {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-pulseDeadline:
			t.Fatalf("loading marker did not pulse from %q", initialLoadingTitle)
		}
	}
	if got := fp.currentTitle; !strings.Contains(got, "/sdcard") || !strings.HasSuffix(got, panelLoadingPulse[1]) {
		t.Fatalf("pulsed title lost the current path or marker: %q", got)
	}

	// Execute any old animation task after the new cache is already visible. It
	// must recognize its canceled context and leave the new rows untouched.
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			goto oldTimerDrained
		}
	}
oldTimerDrained:
	if got := fp.getRawSelectedName(); got != ".." || remote.GetPath() != "/sdcard" {
		t.Fatalf("stale loading timer damaged new cache: path=%q cursor=%q", remote.GetPath(), got)
	}

	// Simulate held Enter. Cached /sdcard and / alternate synchronously, while
	// the pending backend refresh is replaced rather than appended to a FIFO.
	for i := 0; i < 40; i++ {
		if !fp.ProcessKey(&vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN,
		}) {
			t.Fatalf("repeated Enter %d was not handled", i)
		}
	}
	if got := remote.GetPath(); got != "/sdcard" {
		t.Fatalf("path after repeated Enter = %q, want /sdcard", got)
	}
	if got := remote.readDirCalls.Load(); got != 1 {
		t.Fatalf("%d backend reads started while the first was active, want 1", got)
	}
	if got := remote.checkedPathCalls.Load(); got != 0 {
		t.Fatalf("repeated cached Enter made %d checked/remote SetPath calls", got)
	}

	remote.releaseFirstRead <- struct{}{}
	waitForLoad(t, fp)
	if got := remote.readDirCalls.Load(); got != 2 {
		t.Fatalf("backend reads after release = %d, want active + latest only", got)
	}
	if got := fp.getRawSelectedName(); got != ".." {
		t.Fatalf("fresh /sdcard replacement moved cursor to %q", got)
	}
	if got := fp.currentTitle; got != "/sdcard" {
		t.Fatalf("completed load title = %q, want marker-free /sdcard", got)
	}
}

func TestPanelsFrame_NavigateToCachedRemotePathIsOptimistic(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldConfig := AppConfig
	AppConfig.SyncPanelLoad = false
	AppConfig.ShowHiddenFiles = true
	defer func() { AppConfig = oldConfig }()

	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(t.TempDir()))
	waitForLoad(t, fp)
	remote := newQueuedNavigationVFS()
	t.Cleanup(func() {
		select {
		case remote.releaseFirstRead <- struct{}{}:
		default:
		}
	})
	fp.vfs = remote
	fp.dirCache = make(map[dirCacheKey]dirCacheEntry)
	fp.saveToCache("/sdcard", remote.items["/sdcard"])

	pf := &PanelsFrame{}
	start := time.Now()
	if !pf.NavigateToPath(fp, "sdcard") {
		t.Fatal("NavigateToPath rejected cached remote directory")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("NavigateToPath blocked for %s", elapsed)
	}
	if got := remote.checkedPathCalls.Load(); got != 0 {
		t.Fatalf("NavigateToPath made %d checked/remote SetPath calls", got)
	}
	if got := remote.optimisticCalls.Load(); got != 1 {
		t.Fatalf("NavigateToPath made %d optimistic path changes, want 1", got)
	}
	if got := remote.GetPath(); got != "/sdcard" {
		t.Fatalf("NavigateToPath path = %q, want /sdcard", got)
	}
	if got := fp.getRawSelectedName(); got != ".." {
		t.Fatalf("cached target was not rendered synchronously, cursor = %q", got)
	}

	waitForPanelSignal(t, remote.firstReadStarted, "background target refresh")
	remote.releaseFirstRead <- struct{}{}
	waitForLoad(t, fp)
}

func TestFileSystemPanelCachedNavigateUpSelectsFolderLeft(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldConfig := AppConfig
	AppConfig.SyncPanelLoad = false
	AppConfig.ShowHiddenFiles = true
	defer func() { AppConfig = oldConfig }()

	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(t.TempDir()))
	waitForLoad(t, fp)
	remote := newQueuedNavigationVFS()
	if err := remote.SetPathOptimistic("/sdcard"); err != nil {
		t.Fatal(err)
	}
	remote.optimisticCalls.Store(0)
	fp.vfs = remote
	fp.lastLoadedPath = "/sdcard"
	fp.dirCache = make(map[dirCacheKey]dirCacheEntry)
	fp.saveToCache("/", remote.items["/"])
	fp.saveToCache("/sdcard", remote.items["/sdcard"])
	fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}, IsCached: true}}
	fp.SetCursorIndex(0)

	if !fp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}) {
		t.Fatal("Enter on cached parent row was not handled")
	}
	if got := remote.GetPath(); got != "/" {
		t.Fatalf("path after cached navigate-up = %q, want /", got)
	}
	if got := fp.getRawSelectedName(); got != "sdcard" {
		t.Fatalf("cached parent listing selected %q, want folder left sdcard", got)
	}
	waitForPanelSignal(t, remote.firstReadStarted, "background parent refresh")
	remote.releaseFirstRead <- struct{}{}
	waitForLoad(t, fp)
	if got := fp.getRawSelectedName(); got != "sdcard" {
		t.Fatalf("fresh parent listing selected %q, want folder left sdcard", got)
	}
}

func TestFileSystemPanel_LiveSelectionPreservation(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmpDir := t.TempDir()
	v := vfs.NewOSVFS(tmpDir)

	// 1. Setup: Panel with cached data
	fp := NewFileSystemPanel(0, 0, 40, 20, v)
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "item1"}, IsCached: true, Selected: true}, // Selected initially
		{VFSItem: vfs.VFSItem{Name: "item2"}, IsCached: true},
	}
	fp.Refresh()
	fp.SetCursorIndex(1) // Stand on item1

	// 2. Simulate User Action: Deselect item1, Select item2 and move cursor to it
	// (while the "real" scan is technically running in background)
	fp.entries[1].Selected = false
	fp.entries[2].Selected = true
	fp.SetCursorIndex(2)

	// 3. Simulate first "real" chunk arrival
	chunk := []vfs.VFSItem{
		{Name: "item1"},
		{Name: "item2"},
		{Name: "item3"},
	}

	// We'll manually call a reconstruction task similar to ReadDirectory
	selectedNames := map[string]bool{"item1": true}
	fp.pendingSelection = "item2"

	newEntries := make([]*fileEntry, len(chunk))
	for i, item := range chunk {
		newEntries[i] = &fileEntry{VFSItem: item}
	}

	// This block mimics the PostTask in ReadDirectory
	for _, e := range fp.entries {
		if e.Name != ".." {
			if e.Selected {
				selectedNames[e.Name] = true
			} else {
				delete(selectedNames, e.Name)
			}
		}
	}

	fp.entries = nil
	fp.entries = append(fp.entries, &fileEntry{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}})
	fp.entries = append(fp.entries, newEntries...)
	for _, e := range fp.entries {
		if e.Name != ".." && selectedNames[e.Name] {
			e.Selected = true
		}
	}

	fp.sortEntries()
	fp.SelectName(fp.pendingSelection)
	fp.Refresh()

	// 4. Verification
	if fp.GetSelectedName() != "item2" {
		t.Errorf("Cursor jump detected! Expected 'item2', got %q", fp.GetSelectedName())
	}

	foundSelected1 := false
	foundSelected2 := false
	for _, e := range fp.entries {
		if e.Name == "item1" && e.Selected {
			foundSelected1 = true
		}
		if e.Name == "item2" && e.Selected {
			foundSelected2 = true
		}
	}
	if foundSelected1 {
		t.Error("Deselection was lost during cache-to-real transition")
	}
	if !foundSelected2 {
		t.Error("Selection was lost during cache-to-real transition")
	}
}
func TestFileSystemPanel_ReadDir_ContextCancel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// Используем NullVFS, так как он поддерживает пагинацию и задержки
	v := vfs.NewNullVFS(0)
	fp := NewFileSystemPanel(0, 0, 40, 20, v)

	// Запускаем чтение сценария IOPS (10 000 файлов)
	v.SetPath("/scenarios/iops")
	fp.ReadDirectory()

	if !fp.isLoading {
		t.Fatal("Panel should be loading")
	}

	// Имитируем отмену (например, переход в другую папку)
	fp.cancelLoad()

	// Прокачиваем задачи. Чанки не должны добавляться в список после отмены.
	timeout := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			break loop
		}
	}

	if len(fp.entries) >= 10001 {
		t.Errorf("ReadDir was not cancelled: got %d entries", len(fp.entries))
	}
}

func TestFileSystemPanel_PendingSelectionPriority(t *testing.T) {
	// Проверяем, что pendingSelection (установленный, например, переименованием)
	// имеет приоритет над текущим положением курсора при прилете чанков данных.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	fp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewNullVFS(0))

	// Устанавливаем цель (новое имя файла после ренейма)
	fp.pendingSelection = "new_name.txt"
	// Текущий курсор "случайно" стоит на другом файле (например, индекс 0 - "..")
	fp.cursorIdx = 0

	// Логика из onChunk:
	// Если pendingSelection пустой, мы берем имя из текущего курсора.
	// В нашем случае он НЕ пустой, значит uName не должен переписать его.
	if fp.pendingSelection == "" {
		uName := fp.getRawSelectedName()
		if uName != "" && uName != ".." {
			fp.pendingSelection = uName
		}
	}

	if fp.pendingSelection != "new_name.txt" {
		t.Errorf("Pending selection was overwritten! Got %q, want 'new_name.txt'", fp.pendingSelection)
	}

	// 3. Симулируем прилет чанка, который СОДЕРЖИТ цель
	fp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: ".."}}, {VFSItem: vfs.VFSItem{Name: "new_name.txt"}}}

	// Отрабатываем снаппинг
	target := fp.pendingSelection
	for i, entry := range fp.entries {
		if entry.Name == target {
			fp.SetCursorIndex(i)
			fp.pendingSelection = ""
			break
		}
	}

	if fp.GetSelectedName() != "new_name.txt" {
		t.Errorf("Cursor failed to snap to the renamed file. On: %q", fp.GetSelectedName())
	}
}
func TestFileEntry_HighlightMarks(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	oldRules := GlobalFileHighlighter.Rules
	defer func() { GlobalFileHighlighter.Rules = oldRules }()

	iniData := `[Highlight_0]
Name = TestGo
Mask = *.go
Mark = •
NormalColor = foreground:#00FF00
`
	ini := ParseIni(strings.NewReader(iniData))
	GlobalFileHighlighter.LoadFromIni(ini)

	entry := &fileEntry{
		VFSItem: vfs.VFSItem{Name: "main.go", IsDir: false},
	}

	oldConfig := AppConfig
	defer func() { AppConfig = oldConfig }()

	AppConfig.ShowHighlightMarks = false
	if got := entry.GetCellText(0); got != "main.go" {
		t.Errorf("Expected name without marker when ShowHighlightMarks=false, got %q", got)
	}

	AppConfig.ShowHighlightMarks = true
	text := entry.GetCellText(0)
	expectedText := "• main.go"
	if text != expectedText {
		t.Errorf("Marker integration in GetCellText failed: got %q, want %q", text, expectedText)
	}
}

func TestFileSystemPanel_BottomFrameShowsCursorEntry(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	was := AppConfig.ShowPanelFileInfo
	AppConfig.ShowPanelFileInfo = false
	t.Cleanup(func() { AppConfig.ShowPanelFileInfo = was })

	fp := NewFileSystemPanel(0, 0, 60, 20, vfs.NewOSVFS(t.TempDir()))
	fp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "sub", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "big.bin", Size: 1234567}},
	}
	fp.isLoading = false
	if fp.loadingTimer != nil {
		fp.loadingTimer.Stop()
	}
	fp.Refresh()

	bottom := func() string { return ScreenRow(scr, fp.Y2, fp.X1, fp.X2) }

	// The total keeps the centre, the entry under the cursor sits in the
	// left corner; both are spelled out in exact bytes.
	fp.SetCursorIndex(2)
	fp.Show(scr)
	if got := bottom(); !strings.Contains(got, "▸ 1 234 567") || !strings.Contains(got, "1 234 567 (2)") {
		t.Errorf("bottom frame for a file: %q", got)
	}

	// Directories say what they are instead of a size.
	fp.SetCursorIndex(1)
	fp.Show(scr)
	if got := bottom(); !strings.Contains(got, "▸ <DIR>") {
		t.Errorf("bottom frame for a dir: %q", got)
	}
	fp.SetCursorIndex(0)
	fp.Show(scr)
	if got := bottom(); !strings.Contains(got, "▸ UP-DIR") {
		t.Errorf("bottom frame for the up-dir: %q", got)
	}

	// With the far2l status line on, the marker steps aside.
	AppConfig.ShowPanelFileInfo = true
	fp.Show(scr)
	if got := bottom(); strings.Contains(got, "▸") {
		t.Errorf("marker should be dropped when the status line is on: %q", got)
	}
}
