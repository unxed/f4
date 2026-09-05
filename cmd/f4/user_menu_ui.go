package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/unxed/f4/piecetable"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// User menu UI: F2 opens a vertical menu loaded from FarMenu.ini in the
// current directory (walking up to the root), or from FarMenu.ini next
// to the executable, or from ~/.config/f4/settings/main_menu.ini.
// Shift+F2 cycles the source as far2l does.

// MenuMode picks where the menu items come from.
type MenuMode int

const (
	MenuModeLocal MenuMode = iota // FarMenu.ini in cwd or any parent
	MenuModeFar                   // FarMenu.ini next to the f4 binary
	MenuModeMain                  // main_menu.ini in user config
)

const farMenuFileName = "FarMenu.ini"

// submenuMarker is the right-aligned glyph that flags an item as opening
// a nested submenu, matching far2l's choice (vmenu.cpp:1980).
const submenuMarker = "►" // ►

// userMenuBottomHint is the keybinding cheat-sheet drawn on the bottom
// border of every user-menu level. Far Manager has had a hint like this
// since the original Roshal release (usermenu.cpp:535 sets it via
// SetBottomTitle); listing only the chords f4 actually responds to.
const userMenuBottomHint = " Del Ins Ctrl+F4 Ctrl+Up/Down "

// userMenuFrame wraps a vtui.VMenu so the user menu can draw a hotkey
// hint on its bottom border without extending vtui itself (the library
// ships as a submodule and would be its own PR). All Frame interface
// methods are promoted from the embedded VMenu; we only override Show
// to paint the hint over the bottom border line.
type userMenuFrame struct {
	*vtui.VMenu
	bottomHint string
}

func (u *userMenuFrame) Show(scr *vtui.ScreenBuf) {
	u.VMenu.Show(scr)
	if u.bottomHint == "" {
		return
	}
	x1, _, x2, y2 := u.GetPosition()
	vtui.NewPainter(scr).DrawTitle(x1, y2, x2, u.bottomHint, vtui.Palette[vtui.ColMenuTitle])
}

// MainMenuFilePath returns the user-config location for the persistent
// main menu. The filename matches far2l so the same file can be shared
// between ~/.config/far2l/settings/user_menu.ini and the f4 directory
// without renaming.
func MainMenuFilePath() string {
	// In portable mode (UseSystemProfiles=0) write under <exeDir>/Profile;
	// otherwise use the system %AppData%/f4/settings path as before, matching
	// the other config INIs that resolve through GetF4ConfigDir.
	return filepath.Join(GetF4ConfigDir(), "settings", "user_menu.ini")
}

// findLocalFarMenu walks startDir upward looking for FarMenu.ini.
func findLocalFarMenu(startDir string) (path string, found bool) {
	dir := startDir
	for {
		candidate := filepath.Join(dir, farMenuFileName)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// findFarMenuNearBinary looks for FarMenu.ini next to the running executable.
func findFarMenuNearBinary() (path string, found bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	candidate := filepath.Join(filepath.Dir(exe), farMenuFileName)
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
		return candidate, true
	}
	return "", false
}

// loadFarMenuFile reads a FarMenu.ini (text format) into a slice.
func loadFarMenuFile(path string) ([]UserMenuItem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseFarMenu(f)
}

// saveFarMenuFile writes a FarMenu.ini text-format file atomically.
func saveFarMenuFile(path string, items []UserMenuItem) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	var buf bytes.Buffer
	if err := WriteFarMenu(&buf, items); err != nil {
		return err
	}
	return writeFileAtomically(path, buf.Bytes(), 0o600)
}

// loadRootForMode reads the current root menu from disk based on the
// source mode and path. Missing files are not an error — they yield an
// empty slice so a fresh menu can be authored.
func loadRootForMode(mode MenuMode, path string) []UserMenuItem {
	switch mode {
	case MenuModeMain:
		items, _ := LoadMainMenu(path)
		return items
	default:
		items, err := loadFarMenuFile(path)
		if err != nil {
			return nil
		}
		return items
	}
}

// saveRootForMode writes items back to the source file using whichever
// on-disk format that source uses (flat INI for the main menu, FarMenu.ini
// text for the per-directory and near-binary files).
func saveRootForMode(mode MenuMode, path string, items []UserMenuItem) error {
	switch mode {
	case MenuModeMain:
		return SaveMainMenu(path, items)
	default:
		return saveFarMenuFile(path, items)
	}
}

// defaultSavePath returns the path Ctrl+F4 should open in the editor
// for a given mode when no menu file exists yet, so the user can
// author one from scratch and save it where the loader will find it.
func defaultSavePath(pf *PanelsFrame, mode MenuMode) string {
	switch mode {
	case MenuModeLocal:
		if fsp, ok := pf.panels[pf.activeIdx].(*FileSystemPanel); ok && fsp != nil && fsp.vfs != nil {
			return filepath.Join(fsp.vfs.GetPath(), farMenuFileName)
		}
		return ""
	case MenuModeFar:
		if exe, err := os.Executable(); err == nil {
			return filepath.Join(filepath.Dir(exe), farMenuFileName)
		}
		return ""
	case MenuModeMain:
		return MainMenuFilePath()
	}
	return ""
}

// loadMenuForMode returns (items, title, sourcePath, ok). ok=false means
// this mode has no data — caller may want to try the next mode.
func loadMenuForMode(pf *PanelsFrame, mode MenuMode) (items []UserMenuItem, title, source string, ok bool) {
	switch mode {
	case MenuModeLocal:
		fsp, _ := pf.panels[pf.activeIdx].(*FileSystemPanel)
		if fsp == nil {
			return nil, Msg("UserMenu.LocalMenuTitle"), "", false
		}
		path, found := findLocalFarMenu(fsp.vfs.GetPath())
		if !found {
			return nil, Msg("UserMenu.LocalMenuTitle"), "", false
		}
		loaded, err := loadFarMenuFile(path)
		if err != nil {
			return nil, Msg("UserMenu.LocalMenuTitle"), path, false
		}
		return loaded, Msg("UserMenu.LocalMenuTitle"), path, true
	case MenuModeFar:
		path, found := findFarMenuNearBinary()
		if !found {
			return nil, fmt.Sprintf("%s (%s)", Msg("UserMenu.MainMenuTitle"), Msg("UserMenu.MainMenuFAR")), "", false
		}
		loaded, err := loadFarMenuFile(path)
		if err != nil {
			return nil, fmt.Sprintf("%s (%s)", Msg("UserMenu.MainMenuTitle"), Msg("UserMenu.MainMenuFAR")), path, false
		}
		return loaded, fmt.Sprintf("%s (%s)", Msg("UserMenu.MainMenuTitle"), Msg("UserMenu.MainMenuFAR")), path, true
	case MenuModeMain:
		path := MainMenuFilePath()
		loaded, err := LoadMainMenu(path)
		if err != nil {
			return nil, Msg("UserMenu.MainMenuTitle"), path, false
		}
		return loaded, Msg("UserMenu.MainMenuTitle"), path, len(loaded) > 0
	}
	return nil, "", "", false
}

// resolveMenuStart finds the first non-empty mode at or after initial,
// returning items/title/source and the mode actually used.
func resolveMenuStart(pf *PanelsFrame, initial MenuMode) (items []UserMenuItem, title string, mode MenuMode, source string) {
	for offset := 0; offset < 3; offset++ {
		m := MenuMode((int(initial) + offset) % 3)
		if loaded, t, src, ok := loadMenuForMode(pf, m); ok {
			return loaded, t, m, src
		}
	}
	// Nothing found anywhere — fall back to main with an empty list so
	// the user still sees the menu chrome and can press Shift+F2 / Esc.
	_, t, _, _ := loadMenuForMode(pf, MenuModeMain)
	return nil, t, MenuModeMain, defaultSavePath(pf, MenuModeMain)
}

// userMenuState owns the full menu tree as loaded from disk plus the
// path from the root to the level currently on screen. In-place edits
// (Del, Ctrl+up/down) walk the same tree to find the slice to mutate,
// save the root, and re-render at the same path. far2l shows only one
// menu at a time: ProcessSingleMenu recreates the parent VMenu with
// the saved MenuPos on EC_CLOSE_LEVEL (usermenu.cpp:738-744). We do
// the same — `selStack` is one entry per ancestor.
type userMenuState struct {
	pf         *PanelsFrame
	mode       MenuMode
	sourcePath string // file Ctrl+F4 opens and edits save back to
	rootTitle  string // base title; submenu titles append " -> child"
	rootItems  []UserMenuItem
	path       []int // indices from root to the current level
	selStack   []int // saved cursor position at each ancestor level
}

// ShowUserMenu is the entry point. It loads the user menu starting from
// the local (cwd-relative) mode and pushes a modal VMenu.
func ShowUserMenu(pf *PanelsFrame) {
	items, title, mode, source := resolveMenuStart(pf, MenuModeLocal)
	if source == "" {
		source = defaultSavePath(pf, mode)
	}
	s := &userMenuState{
		pf:         pf,
		mode:       mode,
		sourcePath: source,
		rootTitle:  title,
		rootItems:  items,
	}
	s.openCurrent(0)
}

// currentItems returns the items slice at the level the user is viewing.
func (s *userMenuState) currentItems() []UserMenuItem {
	items := s.rootItems
	for _, idx := range s.path {
		if idx < 0 || idx >= len(items) || !items[idx].IsSubmenu() {
			return nil
		}
		items = items[idx].Submenu
	}
	return items
}

// currentTitle builds the title with " -> " breadcrumbs for each
// submenu the user descended through.
func (s *userMenuState) currentTitle() string {
	items := s.rootItems
	t := s.rootTitle
	for _, idx := range s.path {
		if idx < 0 || idx >= len(items) {
			return t
		}
		t = t + " -> " + stripAmpersand(items[idx].Label)
		items = items[idx].Submenu
	}
	return t
}

// replaceCurrentItems swaps the slice at the current path. Needed when
// a mutation (delete, insert) changes the slice length.
func (s *userMenuState) replaceCurrentItems(newItems []UserMenuItem) {
	if len(s.path) == 0 {
		s.rootItems = newItems
		return
	}
	items := s.rootItems
	for i := 0; i < len(s.path)-1; i++ {
		items = items[s.path[i]].Submenu
	}
	items[s.path[len(s.path)-1]].Submenu = newItems
}

// openCurrent pushes the menu for the current path with the given cursor.
func (s *userMenuState) openCurrent(selected int) {
	s.pushLevel(s.currentItems(), s.currentTitle(), selected)
}

// saveRoot persists rootItems to the source file in its native format.
// Errors are surfaced as a modal message so the user knows.
func (s *userMenuState) saveRoot() bool {
	if s.sourcePath == "" {
		return false
	}
	if err := saveRootForMode(s.mode, s.sourcePath, s.rootItems); err != nil {
		vtui.ShowMessage(" User menu ",
			fmt.Sprintf("Failed to save menu:\n%v", err),
			[]string{"&Ok"})
		return false
	}
	return true
}

func (s *userMenuState) pushLevel(items []UserMenuItem, title string, initialSelect int) {
	menu := vtui.NewVMenu(" " + title + " ")
	markUserMenu(menu)

	// Map F1..F24 hotkeys to item indices for fast lookup in OnKeyDown.
	// vtui already handles single-char (&-prefixed) hotkeys natively.
	fnKeyTarget := map[uint32]int{}

	hasSubmenus := false
	for i, it := range items {
		if it.IsSeparator() {
			// Carry UserData so Del / Ctrl+up/down can find the items
			// index from the UI index uniformly across all entries.
			menu.AddItem(vtui.MenuItem{Separator: true, UserData: i})
			continue
		}
		if fn := parseFunctionKey(it.HotKey); fn > 0 {
			fnKeyTarget[fn] = i
		}
		mi := vtui.MenuItem{
			Text:     formatMenuItemText(it),
			UserData: i,
		}
		if it.IsSubmenu() {
			// far2l uses U+25BA as the submenu marker (vmenu.cpp:1980);
			// the right-aligned Shortcut slot is the closest analogue.
			mi.Shortcut = submenuMarker
			hasSubmenus = true
		}
		menu.AddItem(mi)
	}

	w, h := menuSize(s.pf, menu.GetItemCount(), items, hasSubmenus)
	x := (s.pf.lastW - w) / 2
	y := (s.pf.lastH - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	menu.SetPosition(x, y, x+w-1, y+h-1)
	if initialSelect > 0 && initialSelect < menu.GetItemCount() {
		menu.SetSelectPos(initialSelect)
	}

	menu.OnKeyDown = func(e *vtinput.InputEvent) bool {
		if !e.KeyDown {
			return false
		}
		shift := e.ControlKeyState&vtinput.ShiftPressed != 0
		ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
		alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0

		// Shift+F2 cycles the menu source mode. Drop the path so the
		// fresh root doesn't try to "return" to the previous mode's chain.
		if e.VirtualKeyCode == vtinput.VK_F2 && shift && !ctrl && !alt {
			s.path = nil
			s.selStack = nil
			menu.Close()
			next := MenuMode((int(s.mode) + 1) % 3)
			vtui.FrameManager.PostTask(func() {
				newItems, newTitle, newMode, newSrc := resolveMenuStart(s.pf, next)
				s.mode = newMode
				if newSrc == "" {
					newSrc = defaultSavePath(s.pf, newMode)
				}
				s.sourcePath = newSrc
				s.rootTitle = newTitle
				s.rootItems = newItems
				s.openCurrent(0)
			})
			return true
		}
		openInExternalEditor := func() {
			sourcePath := s.sourcePath
			if sourcePath == "" {
				sourcePath = defaultSavePath(s.pf, s.mode)
			}
			if sourcePath == "" {
				return
			}
			pf := s.pf
			mode := s.mode
			s.path = nil
			s.selStack = nil
			menu.Close()
			vtui.FrameManager.PostTask(func() {
				editCurrentMenuInExternalEditor(pf, mode, sourcePath)
			})
		}

		// F4 on a menu item: edit in-place using dialogs.
		if e.VirtualKeyCode == vtinput.VK_F4 && !ctrl && !shift && !alt {
			pos := menu.SelectPos
			if pos >= 0 && pos < len(menu.Items) {
				if idx, ok := menu.Items[pos].UserData.(int); ok && idx >= 0 && idx < len(items) {
					it := items[idx]
					menu.Close()
					vtui.FrameManager.PostTask(func() {
						showEditItemDialog(s, menu, items, idx, false, it.IsSubmenu())
					})
					return true
				}
			}
			return true
		}
		// Ctrl+F4 edits the menu via a temp file in the text editor.
		if e.VirtualKeyCode == vtinput.VK_F4 && ctrl && !shift && !alt {
			openInExternalEditor()
			return true
		}
		// Insert or Ctrl+N: show dialog to add a command or submenu.
		if (e.VirtualKeyCode == vtinput.VK_INSERT || (e.VirtualKeyCode == vtinput.VK_N && ctrl)) && !shift && !alt {
			pos := menu.SelectPos
			insertIdx := 0
			if pos >= 0 && pos < len(menu.Items) {
				if idx, ok := menu.Items[pos].UserData.(int); ok {
					insertIdx = idx
				}
			}
			dlg := vtui.ShowMessage(" User menu ", "Add to menu:", []string{"&Command", "&Submenu", "Cancel"})
			dlg.OnResult = func(code int) {
				switch code {
				case 0: // Command
					showEditItemDialog(s, menu, items, insertIdx, true, false)
				case 1: // Submenu
					showEditItemDialog(s, menu, items, insertIdx, true, true)
				}
			}
			return true
		}
		// Del: confirm and remove the item under the cursor.
		if e.VirtualKeyCode == vtinput.VK_DELETE && !ctrl && !shift && !alt {
			s.deleteAt(menu, items)
			return true
		}
		// Ctrl+Up / Ctrl+Down: move the current item up/down within
		// this level, no wrap (matches usermenu.cpp:603-617).
		if e.VirtualKeyCode == vtinput.VK_UP && ctrl && !shift && !alt {
			s.swapWithNeighbor(menu, items, -1)
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_DOWN && ctrl && !shift && !alt {
			s.swapWithNeighbor(menu, items, +1)
			return true
		}
		// Shift+F10 quits the entire menu chain (usermenu.cpp:685).
		if e.VirtualKeyCode == vtinput.VK_F10 && shift && !ctrl && !alt {
			s.path = nil
			s.selStack = nil
			menu.Close()
			return true
		}
		// Esc and F10: back one level, or close at the root.
		if !shift && !ctrl && !alt &&
			(e.VirtualKeyCode == vtinput.VK_ESCAPE || e.VirtualKeyCode == vtinput.VK_F10) {
			s.goBack(menu)
			return true
		}
		// F1..F12: jump to the item whose HotKey is "F<n>" and activate it.
		// Any F-key not bound to a menu item is swallowed so it doesn't
		// fall through to the panel-level handler underneath (F3=View,
		// F5=Copy, etc. would be very surprising while the menu is open).
		if !shift && !ctrl && !alt && e.VirtualKeyCode >= vtinput.VK_F1 && e.VirtualKeyCode <= vtinput.VK_F12 {
			fn := uint32(e.VirtualKeyCode-vtinput.VK_F1) + 1
			target, mapped := fnKeyTarget[fn]
			if !mapped {
				return true
			}
			uiIdx, ok := findMenuItemByUserData(menu, target)
			if !ok {
				return true
			}
			menu.SetSelectPos(uiIdx)
			if target >= 0 && target < len(items) && items[target].IsSubmenu() {
				s.enterSubmenu(menu, target)
				return true
			}
			// Leaf: simulate Enter so vtui's default OnAction fires.
			menu.ProcessKey(&vtinput.InputEvent{
				Type: vtinput.KeyEventType, KeyDown: true,
				VirtualKeyCode: vtinput.VK_RETURN,
			})
			return true
		}
		// Enter / Right on a submenu item: descend into the child.
		if !shift && !ctrl && !alt &&
			(e.VirtualKeyCode == vtinput.VK_RETURN || e.VirtualKeyCode == vtinput.VK_RIGHT) {
			pos := menu.SelectPos
			if pos >= 0 && pos < len(menu.Items) {
				if idx, ok := menu.Items[pos].UserData.(int); ok && idx >= 0 && idx < len(items) {
					if items[idx].IsSubmenu() {
						s.enterSubmenu(menu, idx)
						return true
					}
				}
			}
			// Right on a leaf is a no-op; Enter on a leaf falls through to
			// vtui's default RETURN handler so OnAction runs.
			if e.VirtualKeyCode == vtinput.VK_RIGHT {
				return true
			}
			return false
		}
		// Left arrow → back to the parent menu, no-op at the root.
		if !shift && !ctrl && !alt && e.VirtualKeyCode == vtinput.VK_LEFT {
			s.goBack(menu)
			return true
		}
		return false
	}

	menu.OnAction = func(uiIdx int) {
		if uiIdx < 0 || uiIdx >= len(menu.Items) {
			return
		}
		idx, ok := menu.Items[uiIdx].UserData.(int)
		if !ok || idx < 0 || idx >= len(items) {
			return
		}
		chosen := items[idx]
		if chosen.IsSubmenu() {
			// Submenus are handled by OnKeyDown for the keyboard paths.
			// We only land here on mouse click; do the same thing.
			s.enterSubmenu(menu, idx)
			return
		}
		// Leaf item: vtui will pop this menu on its own after we return.
		// Drop the path so we don't try to reopen a parent on the way
		// out, then dispatch the commands once the frame stack has settled.
		cmds := chosen.Commands
		s.path = nil
		s.selStack = nil
		vtui.FrameManager.PostTask(func() {
			executeMenuCommands(s.pf, cmds)
		})
	}

	vtui.FrameManager.Push(&userMenuFrame{VMenu: menu, bottomHint: userMenuBottomHint})
}

// enterSubmenu records the current cursor, descends into items[subIdx],
// and renders the child level.
func (s *userMenuState) enterSubmenu(current *vtui.VMenu, subIdx int) {
	s.selStack = append(s.selStack, current.SelectPos)
	s.path = append(s.path, subIdx)
	current.Close()
	vtui.FrameManager.PostTask(func() {
		s.openCurrent(0)
	})
}

// itemIndexAtUI returns the items[] index for the menu's UI row at pos,
// or -1 if pos is out of bounds or the UserData is missing.
func itemIndexAtUI(menu *vtui.VMenu, pos int) int {
	if pos < 0 || pos >= len(menu.Items) {
		return -1
	}
	idx, ok := menu.Items[pos].UserData.(int)
	if !ok {
		return -1
	}
	return idx
}

// deleteAt removes the item at the cursor with a confirmation dialog
// (none for separators — those are visual-only). On success the root
// is saved and the menu is rerendered with the cursor clamped.
func (s *userMenuState) deleteAt(current *vtui.VMenu, items []UserMenuItem) {
	idx := itemIndexAtUI(current, current.SelectPos)
	if idx < 0 || idx >= len(items) {
		return
	}
	chosen := items[idx]

	apply := func() {
		newItems := make([]UserMenuItem, 0, len(items)-1)
		newItems = append(newItems, items[:idx]...)
		newItems = append(newItems, items[idx+1:]...)
		s.replaceCurrentItems(newItems)
		if !s.saveRoot() {
			return
		}
		newPos := idx
		if newPos >= len(newItems) {
			newPos = len(newItems) - 1
		}
		if newPos < 0 {
			newPos = 0
		}
		current.Close()
		vtui.FrameManager.PostTask(func() {
			s.openCurrent(newPos)
		})
	}

	if chosen.IsSeparator() {
		apply()
		return
	}
	label := stripAmpersand(chosen.Label)
	if label == "" {
		label = chosen.HotKey
	}
	dlg := vtui.ShowMessage(" User menu ",
		fmt.Sprintf("Delete menu item:\n\n  %s", label),
		[]string{"&Delete", "Cancel"})
	// Destructive — render on the WarnDialog palette (see #379).
	dlg.IsWarning = true
	dlg.OnResult = func(code int) {
		if code == 0 {
			apply()
		}
	}
}

// swapWithNeighbor moves the cursor item by `delta` positions (±1)
// within the current level, in place. Separators participate in the
// swap so users can reorder them too.
func (s *userMenuState) swapWithNeighbor(current *vtui.VMenu, items []UserMenuItem, delta int) {
	a := itemIndexAtUI(current, current.SelectPos)
	if a < 0 || a >= len(items) {
		return
	}
	b := a + delta
	if b < 0 || b >= len(items) {
		return
	}
	items[a], items[b] = items[b], items[a]
	if !s.saveRoot() {
		items[a], items[b] = items[b], items[a]
		return
	}
	current.Close()
	vtui.FrameManager.PostTask(func() {
		s.openCurrent(b)
	})
}

// goBack closes the current menu. If there's an ancestor level it is
// reopened with the cursor position that was active when the user
// descended; otherwise the user lands back on the panels.
func (s *userMenuState) goBack(current *vtui.VMenu) {
	current.Close()
	if len(s.path) == 0 {
		return
	}
	s.path = s.path[:len(s.path)-1]
	sel := 0
	if len(s.selStack) > 0 {
		sel = s.selStack[len(s.selStack)-1]
		s.selStack = s.selStack[:len(s.selStack)-1]
	}
	vtui.FrameManager.PostTask(func() {
		s.openCurrent(sel)
	})
}

func showEditItemDialog(s *userMenuState, current *vtui.VMenu, items []UserMenuItem, idx int, isCreate bool, isSubmenu bool) {
	title := Msg("UserMenu.EditTitle")
	if isCreate {
		if isSubmenu {
			title = Msg("UserMenu.CreateSubmenuTitle")
		} else {
			title = Msg("UserMenu.CreateTitle")
		}
	} else {
		if isSubmenu {
			title = Msg("UserMenu.EditSubmenuTitle")
		}
	}

	hotkey := ""
	label := ""
	var cmdLines []string
	if !isCreate && idx >= 0 && idx < len(items) {
		it := items[idx]
		hotkey = it.HotKey
		label = it.Label
		if !isSubmenu {
			cmdLines = append([]string(nil), it.Commands...)
		}
	}

	// Number of visible rows in the multiline command field. Six matches
	// FAR's edit-menu-item dialog and comfortably shows short scripts.
	const cmdRowsVisible = 6
	width := 56
	height := 11
	if !isSubmenu {
		// Extra rows for the multiline command box + one for its label row.
		height = 11 + cmdRowsVisible + 1
	}

	dlg := vtui.NewCenteredDialog(width, height, title)
	dlg.ShowClose = true

	editHotkey := vtui.NewEdit(0, 0, 10, hotkey)
	editLabel := vtui.NewEdit(0, 0, 36, label)
	var editCommand *vtui.MultiLineEdit
	if !isSubmenu {
		editCommand = vtui.NewMultiLineEdit(0, 0, width-4, cmdRowsVisible, "")
		editCommand.SetLines(cmdLines)
	}

	makeRow := func(labelText string, edit vtui.UIElement) *vtui.HBoxLayout {
		hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
		l := vtui.NewLabel(0, 0, padLabel(labelText), edit)
		dlg.AddItem(l)
		dlg.AddItem(edit)
		hbox.Add(l, vtui.Margins{Right: 1}, vtui.AlignLeft)
		hbox.Add(edit, vtui.Margins{}, vtui.AlignFill)
		return hbox
	}

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(makeRow(Msg("UserMenu.LabelHotkey"), editHotkey), vtui.Margins{}, vtui.AlignFill)
	vbox.Add(makeRow(Msg("UserMenu.LabelLabel"), editLabel), vtui.Margins{Top: 1}, vtui.AlignFill)
	if !isSubmenu {
		// Multi-line command box: label sits on its own row above the box
		// (FAR's edit-menu-item dialog layout) so the multi-row edit
		// doesn't leave the label floating next to just its first row.
		cmdLabel := vtui.NewLabel(0, 0, Msg("UserMenu.LabelCommand"), editCommand)
		dlg.AddItem(cmdLabel)
		dlg.AddItem(editCommand)
		cmdBox := vtui.NewHBoxLayout(0, 0, width-4, cmdRowsVisible)
		cmdBox.Add(editCommand, vtui.Margins{}, vtui.AlignFill)
		vbox.Add(cmdLabel, vtui.Margins{Top: 1}, vtui.AlignLeft)
		vbox.Add(cmdBox, vtui.Margins{}, vtui.AlignFill)
	}

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Save"))
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))
	btnOk.IsDefault = true

	btnHbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	btnHbox.HorizontalAlign = vtui.AlignCenter
	btnHbox.Spacing = 2
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)
	btnHbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	btnHbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	vbox.Add(btnHbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		hkey := editHotkey.GetText()
		lbl := editLabel.GetText()
		if lbl == "" {
			vtui.ShowMessageOn(dlg, " Error ", "Label cannot be empty", []string{"&Ok"})
			return
		}

		var cmds []string
		if !isSubmenu && editCommand != nil {
			for _, ln := range editCommand.GetLines() {
				ln = strings.TrimRight(ln, " \t\r")
				// Drop leading/trailing blank rows but keep interior ones,
				// because a blank line inside a script can still be
				// intentional (visual grouping in a shell block).
				if ln == "" && len(cmds) == 0 {
					continue
				}
				cmds = append(cmds, ln)
			}
			// Drop trailing blank lines.
			for len(cmds) > 0 && strings.TrimSpace(cmds[len(cmds)-1]) == "" {
				cmds = cmds[:len(cmds)-1]
			}
		}

		var newItem UserMenuItem
		if isSubmenu {
			newItem = UserMenuItem{
				HotKey:  hkey,
				Label:   lbl,
				Submenu: []UserMenuItem{},
			}
		} else {
			newItem = UserMenuItem{
				HotKey:   hkey,
				Label:    lbl,
				Commands: cmds,
			}
		}

		newItems := make([]UserMenuItem, 0, len(items)+1)
		if isCreate {
			insertIdx := idx
			if insertIdx < 0 {
				insertIdx = 0
			}
			if insertIdx > len(items) {
				insertIdx = len(items)
			}
			newItems = append(newItems, items[:insertIdx]...)
			newItems = append(newItems, newItem)
			newItems = append(newItems, items[insertIdx:]...)
		} else {
			newItems = append(newItems, items...)
			if idx >= 0 && idx < len(newItems) {
				newItems[idx] = newItem
			}
		}

		s.replaceCurrentItems(newItems)
		if s.saveRoot() {
			dlg.Close()
			current.Close()
			targetIdx := idx
			vtui.FrameManager.PostTask(func() {
				s.openCurrent(targetIdx)
			})
		}
	}

	dlg.SetFocusedItem(editLabel)
	vtui.FrameManager.Push(dlg)
}

// editCurrentMenuInExternalEditor implements the Ctrl+F4 flow modelled
// after far2l (usermenu.cpp:619-678): write the current root tree to a
// temp file as FarMenu.ini text, open the editor on the temp file, and
// on close re-parse the file and persist it back to the source path
// using that source's native format. The original source is left
// untouched if the user made no changes or if parsing fails.
func editCurrentMenuInExternalEditor(pf *PanelsFrame, mode MenuMode, sourcePath string) {
	items := loadRootForMode(mode, sourcePath)

	// The temp file is what the user actually edits, so write it as
	// plain UTF-8 even though the on-disk FarMenu.ini we ultimately
	// save back uses the platform's wide encoding. Editors are far
	// happier with UTF-8 and the parser accepts both on the way back.
	initBytes := []byte(renderFarMenuText(items))
	tmp, err := os.CreateTemp("", "f4-usermenu-*.ini")
	if err != nil {
		vtui.ShowMessage(" User menu ", fmt.Sprintf("Cannot create temp file:\n%v", err), []string{"&Ok"})
		return
	}
	tmpPath := tmp.Name()
	if _, werr := tmp.Write(initBytes); werr != nil {
		tmp.Close()
		os.Remove(tmpPath)
		vtui.ShowMessage(" User menu ", fmt.Sprintf("Cannot write temp file:\n%v", werr), []string{"&Ok"})
		return
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpPath)
		vtui.ShowMessage(" User menu ", fmt.Sprintf("Cannot close temp file:\n%v", cerr), []string{"&Ok"})
		return
	}

	onClose := func() {
		defer os.Remove(tmpPath)
		// Always reopen the menu after the editor closes so the user
		// sees their edits applied immediately (matches far2l: control
		// returns to the menu loop after FrameManager->ExecuteModalEV).
		defer vtui.FrameManager.PostTask(func() { ShowUserMenu(pf) })

		// Compare bytes (not mtime): EditorView.SaveToFile restores the
		// original mtime/perms after an atomic rename to preserve file
		// ownership semantics, so size+mtime equal can't tell us whether
		// the user actually edited anything.
		finalBytes, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return
		}
		if bytes.Equal(initBytes, finalBytes) {
			return
		}
		parsed, parseErr := ParseFarMenu(bytes.NewReader(finalBytes))
		if parseErr != nil {
			vtui.FrameManager.PostTask(func() {
				vtui.ShowMessage(" User menu ",
					fmt.Sprintf("Failed to parse edited menu:\n%v\n\nOriginal kept.", parseErr),
					[]string{"&Ok"})
			})
			return
		}
		if saveErr := saveRootForMode(mode, sourcePath, parsed); saveErr != nil {
			vtui.FrameManager.PostTask(func() {
				vtui.ShowMessage(" User menu ",
					fmt.Sprintf("Failed to save menu:\n%v", saveErr),
					[]string{"&Ok"})
			})
		}
	}

	openTempInEditor(pf, tmpPath, onClose)
}

// openTempInEditor creates an EditorView on the given path with an
// OnClose hook. It mirrors actionOpenEditor's setup but reads
// synchronously since user-menu temp files are tiny.
func openTempInEditor(pf *PanelsFrame, path string, onClose func()) {
	dir := filepath.Dir(path)
	v := vfs.NewOSVFS(dir)

	data, _ := os.ReadFile(path)
	pt := piecetable.New(data)

	editor := NewEditorView(pt, v, path)
	editor.OnClose = onClose
	editor.ResizeConsole(pf.lastW, pf.lastH)
	editor.StartIndexing()
	vtui.FrameManager.AddScreen(editor)
}

// formatMenuItemText builds the displayed string for an item:
//
//	"&a    label"   – single-char hotkey, vtui underlines via the '&'
//	"F3    label"   – function key (vtui has no special handling needed)
//	"      label"   – no hotkey
func formatMenuItemText(it UserMenuItem) string {
	const labelCol = 6
	label := escapeAmpersand(it.Label)
	if fn := parseFunctionKey(it.HotKey); fn > 0 {
		return fmt.Sprintf("%-*s%s", labelCol, it.HotKey, label)
	}
	if it.HotKey == "" {
		return strings.Repeat(" ", labelCol) + label
	}
	// Single char (printable) — let vtui wire up the hotkey.
	return fmt.Sprintf("&%s%s%s", it.HotKey, strings.Repeat(" ", labelCol-1-len(it.HotKey)), label)
}

// escapeAmpersand doubles literal '&' so vtui doesn't treat them as
// hotkey markers in the label portion.
func escapeAmpersand(s string) string {
	return strings.ReplaceAll(s, "&", "&&")
}

func stripAmpersand(s string) string {
	// Drop a single (non-doubled) '&' for display in breadcrumbs.
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '&' {
			if i+1 < len(s) && s[i+1] == '&' {
				b.WriteByte('&')
				i++
				continue
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseFunctionKey returns 1..24 for "F1".."F24", or 0 otherwise.
func parseFunctionKey(hk string) uint32 {
	if len(hk) < 2 || (hk[0] != 'F' && hk[0] != 'f') {
		return 0
	}
	n := 0
	for _, c := range hk[1:] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
		if n > 24 {
			return 0
		}
	}
	if n < 1 || n > 24 {
		return 0
	}
	return uint32(n)
}

// findMenuItemByUserData returns the UI index of the item whose UserData
// matches itemIdx.
func findMenuItemByUserData(menu *vtui.VMenu, itemIdx int) (int, bool) {
	for i, it := range menu.Items {
		if v, ok := it.UserData.(int); ok && v == itemIdx {
			return i, true
		}
	}
	return -1, false
}

// menuSize returns suggested width/height for a menu given its content.
// hasSubmenus reserves space for the right-aligned ► marker.
func menuSize(pf *PanelsFrame, itemCount int, items []UserMenuItem, hasSubmenus bool) (int, int) {
	maxLabel := 20
	for _, it := range items {
		if it.IsSeparator() {
			continue
		}
		w := len(it.Label) + 8 // hotkey + spacing
		if w > maxLabel {
			maxLabel = w
		}
	}
	w := maxLabel + 4
	if hasSubmenus {
		w += 2 // reserve room for the trailing "► "
	}
	// Reserve room for the bottom hotkey hint plus two box corners.
	if minForHint := len(userMenuBottomHint) + 2; w < minForHint {
		w = minForHint
	}
	if pf.lastW > 0 && w > pf.lastW-4 {
		w = pf.lastW - 4
	}
	if w < 24 {
		w = 24
	}
	h := itemCount + 2
	maxH := pf.lastH - 6
	if maxH < 5 {
		maxH = 5
	}
	if h > maxH {
		h = maxH
	}
	if h < 3 {
		h = 3
	}
	return w, h
}

// executeMenuCommands resolves substitutions on a list of commands taken
// from a single menu item and dispatches the result through the command
// line as if the user had typed it and pressed Enter.
func executeMenuCommands(pf *PanelsFrame, commands []string) {
	executeMenuCommandsWithResult(pf, commands)
}

// executeMenuCommandsWithResult is the command-palette variant of
// executeMenuCommands. It reports whether at least one expanded command was
// actually handed to the command line, so MRU does not record cancelled or
// comment-only menu entries.
func executeMenuCommandsWithResult(pf *PanelsFrame, commands []string) bool {
	if len(commands) == 0 || pf.cmdLine == nil {
		return false
	}

	active := snapshotPanel(pf, pf.activeIdx)
	passive := snapshotPanel(pf, 1-pf.activeIdx)
	ctx := &SubstContext{Active: active, Passive: passive}

	var lines []string
	interpreter, scriptStart := userMenuInterpreter(commands)
	for i, raw := range commands {
		if scriptStart && i == 0 {
			continue
		}

		if !scriptStart {
			t := strings.TrimSpace(raw)
			if t == "" {
				continue
			}
			// Skip REM-style comments (case-insensitive, with separator) and ::.
			if isMenuComment(t) {
				continue
			}
			// "@" silent prefix isn't supported yet (would need to suppress
			// panel show/hide). Strip it so the command at least runs.
			raw = strings.TrimPrefix(t, "@")
		}

		res := SubstFileName(raw, ctx)
		if res.Cancelled {
			return false
		}
		if scriptStart || res.Command != "" {
			lines = append(lines, res.Command)
		}
	}
	if len(lines) == 0 {
		return false
	}

	if scriptStart {
		command, err := buildUserMenuScriptCommand(interpreter, strings.Join(lines, "\n"), userMenuCommandDialect(pf))
		if err != nil {
			vtui.ShowMessage(" User menu ", fmt.Sprintf("Cannot run script:\n%v", err), []string{"&Ok"})
			return false
		}
		lines = []string{command}
	}

	// far2l feeds every line of a menu item through the command line one
	// after another, so a "cd" line moves the panel and the lines after it
	// run in the new directory (issue #893). f4 keeps that behaviour while
	// still running the ordinary shell lines as one joined command: the
	// item is split into steps at each directory change, and the steps are
	// dispatched sequentially, each one waiting for the previous shell
	// command to finish.
	pf.runMenuCommandSteps(splitMenuCommandSteps(lines, menuCommandSeparator()))
	return true
}

// menuCommandSeparator joins the shell lines of one step so that they run
// sequentially in a single shell invocation.
func menuCommandSeparator() string {
	if runtime.GOOS == "windows" {
		return " & "
	}
	return "; "
}

// splitMenuCommandSteps groups the expanded lines of a menu item into the
// strings that will be typed on the command line. Consecutive shell lines
// are joined with separator; a directory-change line (as recognized by the
// command line itself) becomes a step of its own so that the panel follows
// it and the remaining lines run in the new directory.
func splitMenuCommandSteps(lines []string, separator string) []string {
	var steps []string
	var shell []string
	flush := func() {
		if len(shell) > 0 {
			steps = append(steps, strings.Join(shell, separator))
			shell = nil
		}
	}
	for _, line := range lines {
		if _, isDirChange := parseDirChangeCommand(strings.TrimSpace(line)); isDirChange {
			flush()
			steps = append(steps, line)
			continue
		}
		shell = append(shell, line)
	}
	flush()
	return steps
}

// runMenuCommandSteps types the steps on the command line one by one, as
// if the user had entered them and pressed Enter. A step that starts a
// command in the terminal leaves the rest of the chain pending until that
// command reports completion (endExecution); a step that completes
// synchronously, such as a panel directory change, continues at once.
func (pf *PanelsFrame) runMenuCommandSteps(steps []string) {
	for i, step := range steps {
		if pf.cmdLine == nil {
			return
		}
		pf.cmdLine.Edit.SetText(step)
		pf.ProcessKey(&vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode: vtinput.VK_RETURN,
		})
		if rest := steps[i+1:]; len(rest) > 0 && pf.isPtyBusy() {
			pf.afterExecution = func() { pf.runMenuCommandSteps(rest) }
			return
		}
	}
}

func isMenuComment(line string) bool {
	if strings.HasPrefix(line, "::") {
		return true
	}
	if len(line) < 3 {
		return false
	}
	if !strings.EqualFold(line[:3], "REM") {
		return false
	}
	if len(line) == 3 {
		return true
	}
	c := line[3]
	return c == ' ' || c == '\t'
}

// snapshotPanel captures the panel state SubstContext needs. Returns a
// zero-value snapshot when the slot isn't a file system panel.
func snapshotPanel(pf *PanelsFrame, idx int) PanelSnapshot {
	if idx < 0 || idx >= len(pf.panels) {
		return PanelSnapshot{}
	}
	fsp, ok := pf.panels[idx].(*FileSystemPanel)
	if !ok || fsp == nil || fsp.vfs == nil {
		return PanelSnapshot{}
	}
	snap := PanelSnapshot{
		CurDir: fsp.vfs.GetPath(),
	}
	if cur := fsp.getRawSelectedName(); cur != "" && cur != ".." {
		snap.CurrentFile = cur
	}
	for _, name := range fsp.GetSelectedNames() {
		if name == ".." {
			continue
		}
		snap.Marked = append(snap.Marked, name)
	}
	return snap
}
