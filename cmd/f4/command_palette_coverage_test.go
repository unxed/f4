package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/unxed/vtui"
)

// This file deliberately combines two different invariants:
//
//   1. Action-generated menu leaves are executable actions, so every visible
//      non-separator leaf must have a palette entry carrying the same Action ID.
//   2. ProcessKey methods and VMenu constructors are broader command-surface
//      indicators. They cannot be proved complete mechanically, so an exact AST
//      inventory makes every new or removed surface require an explicit audit.
//
// The inventory is intentionally semantic rather than line based. Moving code
// does not churn it; changing a receiver/function or adding a second menu does.

type commandPaletteSurfaceAudit struct {
	class     string
	rationale string
}

const (
	paletteAuditActionArea         = "action-area"
	paletteAuditFrameProvider      = "frame-provider"
	paletteAuditPanelProvider      = "panel-provider"
	paletteAuditParentControl      = "parent-control"
	paletteAuditModalLocal         = "modal-local"
	paletteAuditPluginLocal        = "plugin-local"
	paletteAuditTransportHook      = "transport-hook"
	paletteAuditDynamicAction      = "dynamic-action"
	paletteAuditDynamicProvider    = "dynamic-provider"
	paletteAuditPluginDialogBridge = "plugin-dialog-bridge"
)

var commandPaletteAuditClasses = map[string]bool{
	paletteAuditActionArea:         true,
	paletteAuditFrameProvider:      true,
	paletteAuditPanelProvider:      true,
	paletteAuditParentControl:      true,
	paletteAuditModalLocal:         true,
	paletteAuditPluginLocal:        true,
	paletteAuditTransportHook:      true,
	paletteAuditDynamicAction:      true,
	paletteAuditDynamicProvider:    true,
	paletteAuditPluginDialogBridge: true,
}

var commandPaletteProcessKeyAudit = map[string]commandPaletteSurfaceAudit{
	"cmd/f4/ai_chat_panel.go:(*AIChatPanel).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "focused AI panel commands are supplied by the panel-context palette provider; text and link navigation remain local",
	},
	"cmd/f4/arkanoid.go:(*ArkanoidFrame).ProcessKey": {
		class: paletteAuditFrameProvider, rationale: "Arkanoid commands are supplied by commandPaletteArkanoidEntries",
	},
	"cmd/f4/apply_command_output.go:(*applyOutputDialog).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "the apply-output window is modal and only adds its local close key",
	},
	"cmd/f4/command_line.go:(*CommandLine).ProcessKey": {
		class: paletteAuditParentControl, rationale: "command-line editing primitives belong to PanelsFrame rather than being standalone commands",
	},
	"cmd/f4/command_palette_ui.go:(*commandPaletteDialog).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "the palette dialog owns query, navigation, execution, and cancellation while it is open",
	},
	"cmd/f4/editor_view.go:(*EditorView).ProcessKey": {
		class: paletteAuditActionArea, rationale: "editor commands are registered actions; raw text and cursor editing remain local primitives",
	},
	"cmd/f4/find_file.go:(*SearchResultsWindow).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "find results are a modal result picker whose F3/F4/F5 buttons route to existing view/edit/temporary-panel operations",
	},
	"cmd/f4/file_panel.go:(*FileSystemPanel).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "panel actions and audited transient panel keys are exposed by the action registry and panel-context provider",
	},
	"cmd/f4/grabber.go:(*GrabberFrame).ProcessKey": {
		class: paletteAuditFrameProvider, rationale: "screen-grabber commands are supplied by commandPaletteGrabberEntries",
	},
	"cmd/f4/sheet_frame.go:(*SheetFrame).ProcessKey": {
		class: paletteAuditFrameProvider, rationale: "spreadsheet commands are supplied by commandPaletteSheetEntries; cell editing, cursor movement and block marking remain local primitives",
	},
	"cmd/f4/hotkeys_ui.go:(*HotkeyAssignFrame).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "the hotkey-capture dialog must consume the next key locally and is not a global command surface",
	},
	"cmd/f4/image_view.go:(*ImageView).ProcessKey": {
		class: paletteAuditFrameProvider, rationale: "image-viewer commands are supplied by commandPaletteImageEntries",
	},
	"cmd/f4/video_view.go:(*VideoView).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "the video player is a modal frame over a window of its own; play, seek and volume are local primitives sent down mpv's socket",
	},
	"cmd/f4/player_panel.go:(*PlayerPanel).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "the player's transport, volume and playlist keys are navigation inside one panel; the panel toggle itself is the Panel.Player action",
	},
	"cmd/f4/info_panel.go:(*InfoPanel).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "the focused information-panel command is supplied by the panel-context palette provider",
	},
	"cmd/f4/macro.go:(*MacroAssignFrame).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "macro assignment intentionally captures the next key inside its modal dialog",
	},
	"cmd/f4/panels_frame.go:(*PanelsFrame).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "PanelsFrame combines registered actions with audited transient panel-context entries",
	},
	"cmd/f4/panel_plugins.go:(*pluginPanelInstance).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "native panel plugins receive raw input inside their registered panel surface; their semantic commands are plugin-owned",
	},
	"cmd/f4/quick_view_panel.go:(*QuickViewPanel).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "the focused Quick View toggle is supplied by the panel-context palette provider",
	},
	"cmd/f4/queue_manager.go:(*QueueFrame).ProcessKey": {
		class: paletteAuditFrameProvider, rationale: "queue commands are supplied by commandPaletteQueueEntries",
	},
	"cmd/f4/viewer_view.go:(*ViewerView).ProcessKey": {
		class: paletteAuditActionArea, rationale: "viewer commands are registered actions; scrolling and selection remain local primitives",
	},
	"plugins/dummy_rpc/main.go:(*DummyPlugin).ProcessKey": {
		class: paletteAuditTransportHook, rationale: "this is the RPC plugin ProcessKey protocol hook, not an in-process frame",
	},
	"plugins/envman/manager_frame.go:(*managerWindow).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "Environment Manager owns these keys inside its plugin window, reached through its rich command",
	},
	"plugins/mediainfo/dialog.go:(*reportWindow).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "MediaInfo owns its F4 editor handoff while the modal report window is open",
	},
	"plugins/mediainfo/report_view.go:(*reportTextView).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "the MediaInfo report view consumes scrolling and navigation keys as an embedded dialog control",
	},
	"plugins/netfox/dialog.go:(*protoUIContainer).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "NetFox protocol controls consume keys inside the connection dialog",
	},
	"plugins/sqlite/ui.go:(*browserWindow).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "the SQLite client owns F9 inside its modal browser window, which is reached through its own command",
	},
	"plugins/visren/dialog.go:(*Dialog).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "VisRen owns these keys inside the rename dialog, reached through its rich command",
	},
	"plugins/visren/dialog.go:(*previewList).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "the preview list is an embedded VisRen dialog control",
	},
	"plugins/visren/dialog.go:(*tokenButton).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "the token button is an embedded VisRen dialog control",
	},
	"cmd/f4/rpc_panel.go:(*rpcVUIPanel).ProcessKey": {
		class: paletteAuditTransportHook, rationale: "RPC panel input is forwarded to the remote plugin, whose .vui document owns its semantic commands",
	},
}

var commandPaletteNewVMenuAudit = map[string]commandPaletteSurfaceAudit{
	"cmd/f4/actions.go:actionFoldersHistory#1": {
		class: paletteAuditDynamicAction, rationale: "the registered folder-history action opens a runtime history list",
	},
	"cmd/f4/actions.go:actionCommandHistory#1": {
		class: paletteAuditDynamicAction, rationale: "the registered command-history action opens a runtime history list",
	},
	"cmd/f4/actions.go:actionSortMenuForPanel#1": {
		class: paletteAuditDynamicAction, rationale: "the registered sort-menu action opens choices that are also backed by sort actions",
	},
	"cmd/f4/bookmarks_dialog.go:(*bookmarksDialog).open#1": {
		class: paletteAuditDynamicProvider, rationale: "bookmark slots are runtime data and live slots are exposed by commandPaletteBookmarkEntries",
	},
	"cmd/f4/editor_find_all.go:(*EditorView).showFindAllMenu#1": {
		class: paletteAuditModalLocal, rationale: "Find All results are a query-local result selector reached through the registered editor search action",
	},
	"cmd/f4/codepage_settings.go:newCodepageMenu#1": {
		class: paletteAuditDynamicAction, rationale: "the registered viewer, editor and convert-codepage actions all open the runtime codepage list through this builder",
	},
	"cmd/f4/editor_base64.go:(*EditorView).showBase64Menu#1": {
		class: paletteAuditDynamicAction, rationale: "the registered editor Base64 action opens its two fixed transformations",
	},
	"cmd/f4/file_associations_editor.go:(*assocEditorState).openList#1": {
		class: paletteAuditModalLocal, rationale: "association rows are edited inside the file-association settings workflow",
	},
	"cmd/f4/file_associations_ui.go:showAssociationPicker#1": {
		class: paletteAuditDynamicAction, rationale: "matching file associations are runtime choices reached through the registered file operation",
	},
	"cmd/f4/fuse_mount_list.go:showMountList#1": {
		class: paletteAuditDynamicAction, rationale: "the registered mount-list action opens the current mount inventory",
	},
	"cmd/f4/panels_frame.go:(*PanelsFrame).menuItems#1": {
		class: paletteAuditPluginDialogBridge, rationale: "vfs.App.Menu is the generic callback-based plugin dialog bridge; its rows are not globally enumerable commands",
	},
	"cmd/f4/panels_frame.go:(*PanelsFrame).showDriveMenuAt#1": {
		class: paletteAuditDynamicProvider, rationale: "registered drives are mirrored by commandPaletteDriveEntries with live factory re-resolution",
	},
	"cmd/f4/user_menu_ui.go:(*userMenuState).pushLevel#1": {
		class: paletteAuditDynamicProvider, rationale: "executable user-menu leaves are flattened by commandPaletteUserMenuEntries",
	},
	"cmd/f4/viewer_editor_history.go:actionViewerEditorHistory#1": {
		class: paletteAuditDynamicAction, rationale: "the registered viewer/editor history action opens runtime history entries",
	},
	"cmd/f4/quick_view_panel.go:(*QuickViewPanel).showCodepageDialog#1": {
		class: paletteAuditDynamicAction, rationale: "the focused Quick View codepage action opens the runtime codepage list",
	},
	"cmd/f4/temp_panel.go:showTempPanelSlots#1": {
		class: paletteAuditModalLocal, rationale: "the temporary-panel slot picker is a local modal menu; its entries are dynamic panel state, not standalone actions",
	},
}

func TestCommandPaletteResolvesEveryActionGeneratedMenuLeafByID(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	areas := make(map[string]bool)
	for _, action := range GetOrderedActions() {
		if action.MenuPath != "" && !action.HideFromMenu && !strings.EqualFold(action.Area, "Common") {
			areas[action.Area] = true
		}
	}
	orderedAreas := make([]string, 0, len(areas))
	for area := range areas {
		orderedAreas = append(orderedAreas, area)
	}
	sort.Strings(orderedAreas)

	for _, area := range orderedAreas {
		area := area
		t.Run(area, func(t *testing.T) {
			expected := commandPaletteAuditedActionMenuGroups(area)
			actual := BuildMenuBarItems(area)
			if len(actual) != len(expected) {
				t.Fatalf("top-level action menus = %d, audited action groups = %d", len(actual), len(expected))
			}

			paletteByID := make(map[string]commandPaletteEntry)
			for _, entry := range commandPaletteActionEntries(area) {
				if entry.source == commandPaletteSourceAction {
					paletteByID[strings.ToLower(entry.ID)] = entry
				}
			}

			for groupIndex, group := range expected {
				var leaves []vtui.MenuItem
				for _, item := range actual[groupIndex].SubItems {
					if !item.Separator {
						leaves = append(leaves, item)
					}
				}
				if len(leaves) != len(group.actions) {
					t.Fatalf("menu group %q has %d non-separator leaves, want %d action leaves", group.path, len(leaves), len(group.actions))
				}
				for index, action := range group.actions {
					if leaves[index].OnClick == nil {
						t.Errorf("menu group %q leaf %d for action %q has no executor", group.path, index, action.Name)
					}
					gotLabel := plainLabel(strings.TrimPrefix(leaves[index].Text, "√ "))
					wantLabel := plainLabel(action.DisplayLabel())
					if gotLabel != wantLabel {
						t.Errorf("menu group %q leaf %d = %q, want action %q label %q", group.path, index, gotLabel, action.Name, wantLabel)
					}
					entry, ok := paletteByID[strings.ToLower(action.Name)]
					if !ok {
						t.Errorf("menu action %q has no command-palette entry in area %q", action.Name, area)
						continue
					}
					wantKey := "action:" + strings.ToLower(action.Name)
					if entry.ID != action.Name || entry.Key != wantKey {
						t.Errorf("menu action %q resolves to palette ID/key %q/%q, want %q/%q", action.Name, entry.ID, entry.Key, action.Name, wantKey)
					}
				}
			}
		})
	}
}

type commandPaletteActionMenuGroup struct {
	path    string
	actions []Action
	pinned  []Action
}

// commandPaletteAuditedActionMenuGroups mirrors BuildMenuBarItems' grouping
// and ordering rules, including MenuLast pinning, so the coverage test can
// compare it against the actual generated menu leaf-by-leaf.
func commandPaletteAuditedActionMenuGroups(area string) []commandPaletteActionMenuGroup {
	var groups []commandPaletteActionMenuGroup
	byPath := make(map[string]int)
	appendAction := func(action Action) {
		if action.Visible != nil && !action.Visible() {
			return
		}
		index, ok := byPath[action.MenuPath]
		if !ok {
			index = len(groups)
			byPath[action.MenuPath] = index
			groups = append(groups, commandPaletteActionMenuGroup{path: action.MenuPath})
		}
		if action.MenuLast {
			groups[index].pinned = append(groups[index].pinned, action)
			return
		}
		groups[index].actions = append(groups[index].actions, action)
	}

	for _, action := range GetOrderedActions() {
		if action.MenuPath != "" && !action.HideFromMenu && action.Area == area {
			appendAction(action)
		}
	}
	for _, action := range GetOrderedActions() {
		if action.MenuPath == "" || action.HideFromMenu || !strings.EqualFold(action.Area, "Common") {
			continue
		}
		if _, exists := byPath[action.MenuPath]; exists {
			appendAction(action)
		}
	}
	for i := range groups {
		groups[i].actions = append(groups[i].actions, groups[i].pinned...)
	}
	return groups
}

func TestCommandPaletteProductionCommandSurfaceInventory(t *testing.T) {
	files := commandPaletteParseProductionGo(t)
	processKeys := make(map[string]bool)
	newVMenus := make(map[string]bool)

	for _, source := range files {
		aliases, dotImport := commandPaletteVTUIImportAliases(source.file)
		packageOrdinal := 0
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				ast.Inspect(declaration, func(node ast.Node) bool {
					call, isCall := node.(*ast.CallExpr)
					if !isCall || !commandPaletteIsNewVMenuCall(call, aliases, dotImport) {
						return true
					}
					packageOrdinal++
					newVMenus[source.path+":package-init#"+strconv.Itoa(packageOrdinal)] = true
					return true
				})
				continue
			}
			identity := commandPaletteFunctionIdentity(t, source.fset, function)
			if function.Recv != nil && function.Name.Name == "ProcessKey" {
				processKeys[source.path+":"+identity] = true
			}
			if function.Body == nil {
				continue
			}
			ordinal := 0
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || !commandPaletteIsNewVMenuCall(call, aliases, dotImport) {
					return true
				}
				ordinal++
				newVMenus[source.path+":"+identity+"#"+strconv.Itoa(ordinal)] = true
				return true
			})
		}
	}

	commandPaletteAssertSurfaceInventory(t, "ProcessKey receiver", processKeys, commandPaletteProcessKeyAudit)
	commandPaletteAssertSurfaceInventory(t, "vtui.NewVMenu call", newVMenus, commandPaletteNewVMenuAudit)
}

type commandPaletteParsedGo struct {
	path string
	file *ast.File
	fset *token.FileSet
}

func commandPaletteParseProductionGo(t *testing.T) []commandPaletteParsedGo {
	t.Helper()
	// The inventory spans the whole module (main package and plugins alike),
	// so walk from the module root, not this package's directory.
	root := moduleRootDir(t)
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root {
				// Nested repositories and worktrees are not module production
				// sources. Skipping their .git marker keeps local worktree copies
				// (commonly under _work/) from duplicating this inventory.
				if _, markerErr := os.Lstat(filepath.Join(path, ".git")); markerErr == nil {
					return fs.SkipDir
				} else if !os.IsNotExist(markerErr) {
					return markerErr
				}
			}
			switch entry.Name() {
			case ".git", "testdata", "vendor":
				if path != root {
					return fs.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)

	files := make([]commandPaletteParsedGo, 0, len(paths))
	for _, path := range paths {
		fset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse production Go source %q: %v", path, parseErr)
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			t.Fatal(relErr)
		}
		files = append(files, commandPaletteParsedGo{
			path: filepath.ToSlash(relative),
			file: parsed,
			fset: fset,
		})
	}
	return files
}

func commandPaletteFunctionIdentity(t *testing.T, fset *token.FileSet, function *ast.FuncDecl) string {
	t.Helper()
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	var receiver bytes.Buffer
	if err := format.Node(&receiver, fset, function.Recv.List[0].Type); err != nil {
		t.Fatalf("format receiver for %s: %v", function.Name.Name, err)
	}
	return "(" + receiver.String() + ")." + function.Name.Name
}

func commandPaletteVTUIImportAliases(file *ast.File) (map[string]bool, bool) {
	aliases := make(map[string]bool)
	dotImport := false
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != "github.com/unxed/vtui" {
			continue
		}
		name := "vtui"
		if spec.Name != nil {
			name = spec.Name.Name
		}
		switch name {
		case ".":
			dotImport = true
		case "_":
		default:
			aliases[name] = true
		}
	}
	return aliases, dotImport
}

func commandPaletteIsNewVMenuCall(call *ast.CallExpr, aliases map[string]bool, dotImport bool) bool {
	switch function := call.Fun.(type) {
	case *ast.SelectorExpr:
		packageName, ok := function.X.(*ast.Ident)
		return ok && aliases[packageName.Name] && function.Sel.Name == "NewVMenu"
	case *ast.Ident:
		return dotImport && function.Name == "NewVMenu"
	default:
		return false
	}
}

func commandPaletteAssertSurfaceInventory(t *testing.T, name string, discovered map[string]bool, audited map[string]commandPaletteSurfaceAudit) {
	t.Helper()
	var invalid, unexpected, missing []string
	for key, audit := range audited {
		if !commandPaletteAuditClasses[audit.class] || strings.TrimSpace(audit.rationale) == "" {
			invalid = append(invalid, key)
		}
		if !discovered[key] {
			missing = append(missing, key)
		}
	}
	for key := range discovered {
		if _, ok := audited[key]; !ok {
			unexpected = append(unexpected, key)
		}
	}
	sort.Strings(invalid)
	sort.Strings(unexpected)
	sort.Strings(missing)
	if len(invalid) != 0 || len(unexpected) != 0 || len(missing) != 0 {
		t.Fatalf("%s inventory requires an explicit command-palette audit\ninvalid classifications: %v\nunexpected production surfaces: %v\nstale allowlist entries: %v", name, invalid, unexpected, missing)
	}
}
