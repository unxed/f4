package panel

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

const tempPanelSlotCount = 10

// tempPanelReference is a live reference to an item on another VFS. The
// temporary panel deliberately stores no file data: copying into it is the
// same operation as far2l's TmpPanel PutFiles and remains useful for large or
// remote files.
type tempPanelReference struct {
	Id      uint64
	source  vfs.VFS
	path    string
	item    vfs.VFSItem
	display string
}

type TempPanelStore struct {
	mu             sync.RWMutex
	slots          [tempPanelSlotCount][]tempPanelReference
	nextID         uint64
	nextSearchSlot int
}

var GlobalTempPanelStore = &TempPanelStore{}

func normalizeTempPanelSlot(slot int) int {
	if slot < 0 {
		slot = 0
	}
	return slot % tempPanelSlotCount
}

func tempPanelRoot(slot int) string {
	return fmt.Sprintf("tmp:%d", normalizeTempPanelSlot(slot))
}

func cloneTempPanelSource(source vfs.VFS) vfs.VFS {
	if source == nil {
		return nil
	}
	clone := source.Clone()
	if isNilVFS(clone) {
		return source
	}
	return clone
}

func tempPanelDisplayName(source vfs.VFS, path string) string {
	if source != nil {
		if _, local := source.(*vfs.OSVFS); !local {
			if titled, ok := source.(vfs.TitleProvider); ok {
				if title := titled.GetTitle(); title != "" && !strings.HasPrefix(path, title+":") {
					return title + ":" + path
				}
			}
		}
	}
	return path
}

func tempPanelReferenceKey(source vfs.VFS, path string) string {
	return fileops.FileStateKey(source, path)
}

func (s *TempPanelStore) appendReferences(slot int, refs []tempPanelReference) {
	if s == nil || len(refs) == 0 {
		return
	}
	slot = normalizeTempPanelSlot(slot)
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ref := range refs {
		if ref.source == nil || ref.path == "" {
			continue
		}
		key := tempPanelReferenceKey(ref.source, ref.path)
		duplicate := false
		for _, existing := range s.slots[slot] {
			if tempPanelReferenceKey(existing.source, existing.path) == key {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		s.nextID++
		ref.Id = s.nextID
		if ref.display == "" {
			ref.display = tempPanelDisplayName(ref.source, ref.path)
		}
		s.slots[slot] = append(s.slots[slot], ref)
	}
}

func (s *TempPanelStore) references(slot int) []tempPanelReference {
	if s == nil {
		return nil
	}
	slot = normalizeTempPanelSlot(slot)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]tempPanelReference(nil), s.slots[slot]...)
}

func (s *TempPanelStore) reference(slot int, id uint64) (tempPanelReference, bool) {
	if s == nil {
		return tempPanelReference{}, false
	}
	slot = normalizeTempPanelSlot(slot)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ref := range s.slots[slot] {
		if ref.Id == id {
			return ref, true
		}
	}
	return tempPanelReference{}, false
}

func (s *TempPanelStore) referenceByDisplay(slot int, display string) (tempPanelReference, bool) {
	if s == nil {
		return tempPanelReference{}, false
	}
	slot = normalizeTempPanelSlot(slot)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ref := range s.slots[slot] {
		if ref.display == display || ref.path == display {
			return ref, true
		}
	}
	return tempPanelReference{}, false
}

func (s *TempPanelStore) removeReference(slot int, id uint64) bool {
	if s == nil {
		return false
	}
	slot = normalizeTempPanelSlot(slot)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ref := range s.slots[slot] {
		if ref.Id == id {
			copy(s.slots[slot][i:], s.slots[slot][i+1:])
			s.slots[slot] = s.slots[slot][:len(s.slots[slot])-1]
			return true
		}
	}
	return false
}

func (s *TempPanelStore) updateReference(slot int, id uint64, update func(*tempPanelReference)) bool {
	if s == nil || update == nil {
		return false
	}
	slot = normalizeTempPanelSlot(slot)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.slots[slot] {
		if s.slots[slot][i].Id == id {
			update(&s.slots[slot][i])
			return true
		}
	}
	return false
}

func (s *TempPanelStore) ReplaceWithSearchResults(slot int, source vfs.VFS, found []FoundFile) {
	if s == nil {
		return
	}
	slot = normalizeTempPanelSlot(slot)
	clonedSource := cloneTempPanelSource(source)
	refs := make([]tempPanelReference, 0, len(found))
	seen := make(map[string]struct{}, len(found))
	for _, hit := range found {
		if hit.Path == "" || clonedSource == nil {
			continue
		}
		key := tempPanelReferenceKey(clonedSource, hit.Path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, tempPanelReference{
			source:  clonedSource,
			path:    hit.Path,
			item:    hit.Item,
			display: tempPanelDisplayName(source, hit.Path),
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.slots[slot] = nil
	for _, ref := range refs {
		s.nextID++
		ref.Id = s.nextID
		s.slots[slot] = append(s.slots[slot], ref)
	}
}

func (s *TempPanelStore) SearchSlot() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, refs := range s.slots {
		if len(refs) == 0 {
			return i
		}
	}
	slot := normalizeTempPanelSlot(s.nextSearchSlot)
	s.nextSearchSlot = normalizeTempPanelSlot(slot + 1)
	return slot
}

func (s *TempPanelStore) addReferences(ctx context.Context, slot int, source vfs.VFS, names []string) error {
	if s == nil || source == nil {
		return errors.New("temporary panel has no source file system")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var refs []tempPanelReference
	var firstErr error
	if sourcePanel, ok := source.(*TempPanelVFS); ok {
		for _, name := range names {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			entryPath := sourcePanel.Join(sourcePanel.GetPath(), name)
			ref, _, top, ok := sourcePanel.resolve(entryPath)
			if !ok || !top {
				if firstErr == nil {
					firstErr = fmt.Errorf("temporary panel item %q is unavailable", name)
				}
				continue
			}
			refs = append(refs, ref)
		}
	} else {
		storedSource := cloneTempPanelSource(source)
		for _, name := range names {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if name == "" || name == ".." {
				continue
			}
			path := source.Join(source.GetPath(), name)
			item, err := source.Stat(ctx, path)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			refs = append(refs, tempPanelReference{
				source:  storedSource,
				path:    path,
				item:    item,
				display: tempPanelDisplayName(source, path),
			})
		}
	}
	s.appendReferences(slot, refs)
	return firstErr
}

// TempPanelVFS presents one of the ten in-memory reference lists as a normal
// f4 file panel. The synthetic tmp:N paths are only panel-internal handles;
// all actual file operations are delegated to the referenced VFS.
type TempPanelVFS struct {
	store           *TempPanelStore
	slot            int
	currentPath     string
	parent          vfs.VFS
	parentSelection string
	calculatedTotal *vfs.OpStats
}

func NewTempPanelVFS(parent vfs.VFS, store *TempPanelStore, slot int) *TempPanelVFS {
	if store == nil {
		store = GlobalTempPanelStore
	}
	slot = normalizeTempPanelSlot(slot)
	return &TempPanelVFS{store: store, slot: slot, currentPath: tempPanelRoot(slot), parent: parent}
}

func (t *TempPanelVFS) root() string { return tempPanelRoot(t.slot) }

func (t *TempPanelVFS) setParent(parent vfs.VFS, selection string) {
	if t.parent == nil {
		t.parent = parent
		t.parentSelection = selection
	}
}

func (t *TempPanelVFS) GetTitle() string { return "Temp" }

func (t *TempPanelVFS) PanelTitle(string) string {
	return fmt.Sprintf("%s %d", i18n.Msg("TempPanel.Title"), t.slot)
}

func (t *TempPanelVFS) IsAtRoot() bool { return t.currentPath == t.root() }

// CalculateTotal scans every reference in the current temporary-panel slot.
// It deliberately works on the referenced VFSes directly: two references may
// have the same display name, while their source paths remain unambiguous.
func (t *TempPanelVFS) CalculateTotal(ctx context.Context, cb vfs.ScanCallback) (vfs.OpStats, error) {
	var total vfs.OpStats
	if t == nil {
		return total, os.ErrInvalid
	}
	for _, ref := range t.store.references(t.slot) {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		stats, err := vfs.CalculateStats(ctx, ref.source, ref.path, []string{""}, cb)
		if err != nil {
			return total, err
		}
		total.Add(stats)
	}
	return total, nil
}

// SetCalculatedTotal records the result shown in the panel's total status
// line after F3 is pressed on the temporary panel's parent row.
func (t *TempPanelVFS) SetCalculatedTotal(stats vfs.OpStats) {
	if t == nil {
		return
	}
	t.calculatedTotal = &stats
}

// CalculatedPanelTotal lets the panel renderer use the aggregate calculated
// size instead of only summing the top-level reference metadata.
func (t *TempPanelVFS) CalculatedPanelTotal() (vfs.OpStats, bool) {
	if t == nil || t.calculatedTotal == nil {
		return vfs.OpStats{}, false
	}
	return *t.calculatedTotal, true
}

func (t *TempPanelVFS) GetPath() string {
	if t.currentPath == "" {
		return t.root()
	}
	return t.currentPath
}

func (t *TempPanelVFS) IsAbs(path string) bool {
	return path == t.root() || strings.HasPrefix(path, t.root()+"/")
}

func encodeTempPanelComponent(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeTempPanelComponent(value string) (string, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	return string(data), err
}

func (t *TempPanelVFS) entryPath(id uint64) string {
	return fmt.Sprintf("%s/e/%d", t.root(), id)
}

func (t *TempPanelVFS) newNamePath(name string) string {
	return t.root() + "/n/" + encodeTempPanelComponent(name)
}

func (t *TempPanelVFS) parseNewName(path string) (string, bool) {
	prefix := t.root() + "/n/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	name, err := decodeTempPanelComponent(strings.TrimPrefix(path, prefix))
	return name, err == nil && name != ""
}

func (t *TempPanelVFS) resolve(path string) (tempPanelReference, string, bool, bool) {
	if path == t.root() {
		return tempPanelReference{}, "", false, true
	}
	prefix := t.root() + "/e/"
	if !strings.HasPrefix(path, prefix) {
		return tempPanelReference{}, "", false, false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) == 0 || parts[0] == "" {
		return tempPanelReference{}, "", false, false
	}
	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return tempPanelReference{}, "", false, false
	}
	ref, ok := t.store.reference(t.slot, id)
	if !ok || ref.source == nil {
		return tempPanelReference{}, "", false, false
	}
	if len(parts) == 1 {
		return ref, ref.path, true, true
	}
	if len(parts) < 3 || parts[1] != "c" {
		return tempPanelReference{}, "", false, false
	}
	realPath := ref.path
	for _, component := range parts[2:] {
		name, err := decodeTempPanelComponent(component)
		if err != nil || name == "" {
			return tempPanelReference{}, "", false, false
		}
		realPath = ref.source.Join(realPath, name)
	}
	return ref, realPath, false, true
}

func (t *TempPanelVFS) Join(elem ...string) string {
	if len(elem) == 0 {
		return t.GetPath()
	}
	base := elem[0]
	if base == "" {
		base = t.GetPath()
	}
	name := elem[len(elem)-1]
	if name == "" {
		return base
	}
	if name == ".." {
		return t.Dir(base)
	}
	if base == t.root() {
		if strings.HasPrefix(name, t.root()+"/") {
			return name
		}
		if ref, ok := t.store.referenceByDisplay(t.slot, name); ok {
			return t.entryPath(ref.Id)
		}
		return t.newNamePath(name)
	}
	if _, _, _, ok := t.resolve(base); ok {
		return base + "/c/" + encodeTempPanelComponent(name)
	}
	return filepath.Join(elem...)
}

func (t *TempPanelVFS) Abs(path string) (string, error) {
	if path == "" {
		return t.GetPath(), nil
	}
	if t.IsAbs(path) {
		return path, nil
	}
	return t.Join(t.GetPath(), path), nil
}

func (t *TempPanelVFS) Base(path string) string {
	if path == t.root() {
		return ""
	}
	if ref, realPath, _, ok := t.resolve(path); ok {
		return ref.source.Base(realPath)
	}
	if name, ok := t.parseNewName(path); ok {
		return name
	}
	return filepath.Base(path)
}

func (t *TempPanelVFS) Dir(path string) string {
	if path == "" || path == t.root() {
		return t.root()
	}
	if idx := strings.LastIndex(path, "/c/"); idx >= 0 {
		return path[:idx]
	}
	if strings.HasPrefix(path, t.root()+"/e/") {
		return t.root()
	}
	return t.root()
}

func (t *TempPanelVFS) SetPath(path string) error {
	if path == "" {
		path = t.root()
	}
	if !t.IsAbs(path) {
		path = t.Join(t.GetPath(), path)
	}
	if path == t.root() {
		t.currentPath = path
		return nil
	}
	ref, realPath, _, ok := t.resolve(path)
	if !ok {
		return os.ErrNotExist
	}
	item, err := ref.source.Stat(context.Background(), realPath)
	if err != nil {
		return err
	}
	if !item.IsDir {
		return fmt.Errorf("%s is not a directory", realPath)
	}
	t.currentPath = path
	return nil
}

func (t *TempPanelVFS) ReadDir(ctx context.Context, path string, onChunk func([]vfs.VFSItem)) error {
	if path == t.root() {
		items := make([]vfs.VFSItem, 0)
		for _, ref := range t.store.references(t.slot) {
			if err := ctx.Err(); err != nil {
				return err
			}
			item, err := ref.source.Stat(ctx, ref.path)
			if err != nil {
				// A reference is only removed automatically when the backend
				// confirms that the real item is gone. Transient remote errors
				// must not silently destroy a user's saved list.
				if os.IsNotExist(err) {
					t.store.removeReference(t.slot, ref.Id)
				}
				continue
			}
			t.store.updateReference(t.slot, ref.Id, func(updated *tempPanelReference) {
				updated.item = item
			})
			item.Name = ref.display
			items = append(items, item)
		}
		if onChunk != nil && len(items) > 0 {
			onChunk(items)
		}
		return nil
	}
	ref, realPath, _, ok := t.resolve(path)
	if !ok {
		return os.ErrNotExist
	}
	return ref.source.ReadDir(ctx, realPath, onChunk)
}

func (t *TempPanelVFS) Stat(ctx context.Context, path string) (vfs.VFSItem, error) {
	if path == t.root() {
		return vfs.VFSItem{Name: t.root(), IsDir: true}, nil
	}
	ref, realPath, _, ok := t.resolve(path)
	if !ok {
		return vfs.VFSItem{}, os.ErrNotExist
	}
	return ref.source.Stat(ctx, realPath)
}

func (t *TempPanelVFS) MkDir(context.Context, string) error { return os.ErrPermission }

func (t *TempPanelVFS) Remove(ctx context.Context, path string) error {
	ref, realPath, top, ok := t.resolve(path)
	if !ok {
		return os.ErrNotExist
	}
	if top {
		if err := ref.source.Remove(ctx, realPath); err != nil {
			return err
		}
		if t.store.removeReference(t.slot, ref.Id) {
			return nil
		}
		return os.ErrNotExist
	}
	return ref.source.Remove(ctx, realPath)
}

func (t *TempPanelVFS) MoveToTrash(ctx context.Context, path string) error {
	ref, realPath, top, ok := t.resolve(path)
	if !ok {
		return os.ErrNotExist
	}
	if !top {
		trash, ok := ref.source.(vfs.TrashVFS)
		if !ok {
			return vfs.ErrTrashUnsupported
		}
		return trash.MoveToTrash(ctx, realPath)
	}
	trash, ok := ref.source.(vfs.TrashVFS)
	if !ok {
		return vfs.ErrTrashUnsupported
	}
	if err := trash.MoveToTrash(ctx, realPath); err != nil {
		return err
	}
	if t.store.removeReference(t.slot, ref.Id) {
		return nil
	}
	return os.ErrNotExist
}

func (t *TempPanelVFS) Rename(ctx context.Context, oldPath, newPath string) error {
	ref, oldRealPath, top, ok := t.resolve(oldPath)
	if !ok {
		return os.ErrNotExist
	}
	if !top {
		_, newRealPath, _, newOK := t.resolve(newPath)
		if !newOK {
			return os.ErrInvalid
		}
		return ref.source.Rename(ctx, oldRealPath, newRealPath)
	}
	newName, ok := t.parseNewName(newPath)
	if !ok {
		return os.ErrInvalid
	}
	newRealPath := ref.source.Join(ref.source.Dir(oldRealPath), newName)
	if err := ref.source.Rename(ctx, oldRealPath, newRealPath); err != nil {
		return err
	}
	t.store.updateReference(t.slot, ref.Id, func(updated *tempPanelReference) {
		updated.path = newRealPath
		updated.item.Name = newName
		updated.display = tempPanelDisplayName(updated.source, newRealPath)
	})
	return nil
}

func (t *TempPanelVFS) GetCapabilities() vfs.VFSCapabilities {
	return vfs.VFSCapabilities{HasRandomAccess: true}
}

func (t *TempPanelVFS) Search(context.Context, string, string) (chan int64, error) {
	return nil, os.ErrInvalid
}

func (t *TempPanelVFS) Open(ctx context.Context, path string) (vfs.ReadAtCloser, error) {
	_, realPath, _, ok := t.resolve(path)
	if !ok {
		return nil, os.ErrNotExist
	}
	return t.referenceSource(path).Open(ctx, realPath)
}

func (t *TempPanelVFS) referenceSource(path string) vfs.VFS {
	ref, _, _, ok := t.resolve(path)
	if !ok {
		return nil
	}
	return ref.source
}

func (t *TempPanelVFS) Create(context.Context, string) (io.WriteCloser, error) {
	return nil, os.ErrPermission
}

func (t *TempPanelVFS) SetAttributes(ctx context.Context, path string, item vfs.VFSItem) error {
	ref, realPath, _, ok := t.resolve(path)
	if !ok {
		return os.ErrNotExist
	}
	return ref.source.SetAttributes(ctx, realPath, item)
}

func (t *TempPanelVFS) ParentVFS() vfs.VFS { return t.parent }

func (t *TempPanelVFS) Clone() vfs.VFS {
	clone := NewTempPanelVFS(nil, t.store, t.slot)
	clone.currentPath = t.root()
	return clone
}

func (t *TempPanelVFS) Close() error { return nil }

func (t *TempPanelVFS) AddReferences(ctx context.Context, source vfs.VFS, names []string) error {
	err := t.store.addReferences(ctx, t.slot, source, names)
	t.calculatedTotal = nil
	return err
}

func (t *TempPanelVFS) HandlePanelAction(app vfs.App, action vfs.PanelAction, paths []string) bool {
	// F8 must retain the normal delete/trash confirmation. TempPanel.Remove
	// then applies that operation to the referenced real file and removes the
	// reference only after the backend confirms success.
	if action == vfs.PanelActionDelete {
		return false
	}

	if action != vfs.PanelActionActivate && action != vfs.PanelActionEdit {
		return false
	}
	if len(paths) == 0 {
		// SelectedPanelActionPaths omits the synthetic ".." row. Let the
		// panel's normal Enter handler navigate it instead of swallowing it.
		return action != vfs.PanelActionActivate
	}
	// The panel's parent row is navigation, not an item in this VFS. Let the
	// ordinary panel handler process it; otherwise mouse/semantic activation
	// gets swallowed here (the keyboard path handles it before this callback).
	if action == vfs.PanelActionActivate && paths[0] == t.Dir(t.GetPath()) {
		return false
	}
	ref, realPath, _, ok := t.resolve(paths[0])
	if !ok {
		return true
	}
	pf, ok := app.(*PanelsFrame)
	if !ok || pf == nil {
		return true
	}
	if action == vfs.PanelActionEdit {
		OpenEditor(pf, ref.source, realPath)
		return true
	}
	item, err := ref.source.Stat(context.Background(), realPath)
	if err == nil && item.IsDir {
		opened := cloneTempPanelSource(ref.source)
		if opened != nil {
			if err := opened.SetPath(realPath); err == nil {
				if fsp := pf.GetActivePanel(); fsp != nil {
					pf.SwitchToVFS(fsp, opened)
				}
				return true
			} else if isNotListableDirectory(err) {
				// A folder the host will not list (#814) is refused in
				// place; running it through Execute would hand a directory
				// to the shell.
				if fsp := pf.GetActivePanel(); fsp != nil {
					fsp.reportNotListableDirectory(err)
				}
				return true
			}
		}
	}
	Execute(pf, ref.source, ref.source.Dir(realPath), ref.source.Base(realPath), realPath)
	return true
}

func (t *TempPanelVFS) RemovePanelReferences(paths []string) bool {
	if t == nil {
		return true
	}
	for _, path := range paths {
		if ref, _, top, ok := t.resolve(path); ok && top {
			t.store.removeReference(t.slot, ref.Id)
		}
	}
	t.calculatedTotal = nil
	return true
}

func ActionOpenTempPanel(pf *PanelsFrame) {
	if pf == nil {
		return
	}
	fsp := pf.GetActivePanel()
	if fsp == nil {
		return
	}
	if current, ok := fsp.Vfs.(*TempPanelVFS); ok {
		showTempPanelSlots(pf, fsp, current)
		return
	}
	pf.SwitchToVFS(fsp, NewTempPanelVFS(nil, GlobalTempPanelStore, 0))
}

func tempPanelSlotKey(e *vtinput.InputEvent) (int, bool) {
	if e == nil {
		return 0, false
	}
	if e.VirtualKeyCode >= vtinput.VK_0 && e.VirtualKeyCode <= vtinput.VK_9 {
		return int(e.VirtualKeyCode - vtinput.VK_0), true
	}
	if e.VirtualKeyCode >= vtinput.VK_NUMPAD0 && e.VirtualKeyCode <= vtinput.VK_NUMPAD9 {
		return int(e.VirtualKeyCode - vtinput.VK_NUMPAD0), true
	}
	if e.Char >= '0' && e.Char <= '9' {
		return int(e.Char - '0'), true
	}
	return 0, false
}

func (t *TempPanelVFS) showSelectedOnPassive(pf *PanelsFrame) bool {
	if pf == nil {
		return false
	}
	fsp := pf.GetActivePanel()
	passive := pf.GetInactivePanel()
	if fsp == nil || passive == nil {
		return false
	}
	name := fsp.GetRawSelectedName()
	if name == "" || name == ".." {
		return false
	}
	ref, realPath, _, ok := t.resolve(t.Join(t.GetPath(), name))
	if !ok {
		return false
	}
	item, err := ref.source.Stat(context.Background(), realPath)
	if err != nil {
		return false
	}
	opened := cloneTempPanelSource(ref.source)
	if opened == nil {
		return false
	}
	target := realPath
	selection := ".."
	if !item.IsDir {
		target = ref.source.Dir(realPath)
		selection = ref.source.Base(realPath)
	}
	if err := opened.SetPath(target); err != nil {
		if isNotListableDirectory(err) {
			passive.reportNotListableDirectory(err)
			return true
		}
		return false
	}
	passive.PendingSelection = selection
	pf.SwitchToVFS(passive, opened)
	return true
}

func (t *TempPanelVFS) switchToSlot(pf *PanelsFrame, fsp *FileSystemPanel, slot int) bool {
	if pf == nil || fsp == nil {
		return false
	}
	next := NewTempPanelVFS(t.parent, GlobalTempPanelStore, slot)
	next.parentSelection = t.parentSelection
	pf.SwitchToVFS(fsp, next)
	return true
}

func showTempPanelSlots(pf *PanelsFrame, fsp *FileSystemPanel, current *TempPanelVFS) {
	menu := vtui.NewVMenu(i18n.Msg("TempPanel.SwitchTitle"))
	for slot := 0; slot < tempPanelSlotCount; slot++ {
		selectedSlot := slot
		count := len(GlobalTempPanelStore.references(slot))
		menu.AddItem(vtui.MenuItem{
			Text: fmt.Sprintf(i18n.Msg("TempPanel.Slot"), slot, count),
			UserData: func(*FileSystemPanel) {
				next := NewTempPanelVFS(current.parent, GlobalTempPanelStore, selectedSlot)
				next.parentSelection = current.parentSelection
				pf.SwitchToVFS(fsp, next)
			},
		})
	}
	menu.OnAction = func(idx int) {
		if idx < 0 || idx >= len(menu.Items) {
			return
		}
		menu.Close()
		if action, ok := menu.Items[idx].UserData.(func(*FileSystemPanel)); ok {
			action(fsp)
		}
	}
	menu.SetSelectPos(current.slot)
	if pf.LastW > 0 && pf.LastH > 0 {
		width := 30
		height := tempPanelSlotCount + 2
		x := (pf.LastW - width) / 2
		y := (pf.LastH - height) / 2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		menu.SetPosition(x, y, x+width-1, y+height-1)
	}
	vtui.FrameManager.Push(menu)
}

// Semantic panel actions are routed through PanelActionHandler, so the same
// behavior works for keyboard, mouse, and remapped actions.
var _ vfs.PanelActionHandler = (*TempPanelVFS)(nil)

type FoundFile struct {
	Path string
	Item vfs.VFSItem
}

// LocalPaths resolves the named entries of the panel's root to the paths of
// the files they reference, when every one of them lives on the local disk.
// It is what dragging out of a temporary panel offers the desktop (#1604).
func (t *TempPanelVFS) LocalPaths(names []string) ([]string, bool) {
	if t == nil || t.store == nil || len(names) == 0 || !t.IsAtRoot() {
		return nil, false
	}
	paths := make([]string, 0, len(names))
	for _, name := range names {
		ref, realPath, isReference, ok := t.resolve(t.Join(t.root(), name))
		if !ok || !isReference {
			return nil, false
		}
		if _, local := ref.source.(*vfs.OSVFS); !local {
			return nil, false
		}
		paths = append(paths, realPath)
	}
	return paths, true
}
