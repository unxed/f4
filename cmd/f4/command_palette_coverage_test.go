package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/testutil"
)

// This file deliberately combines two different invariants:
//
//   1. action.Action-generated menu leaves are executable actions, so every visible
//      non-separator leaf must have a palette entry carrying the same action.Action ID.
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

// commandPaletteF4Surfaces is how many audited command surfaces belong to the f4
// application itself, as opposed to a plugin. Restructuring moves a surface from
// one package to another; it never removes one. A smaller number here means an
// audit entry was dropped together with its subject, which the set comparison
// below cannot see because both sides shrink at once.
const commandPaletteF4Surfaces = 48

// commandPaletteTargetPackage named the package each audited cmd/f4 file would
// end up in once the split reached it, so an audit key survived the move that
// carried its subject. Every entry has now been reached: nothing audited is
// left in cmd/f4, the directory a file sits in gives the same answer the map
// used to, and the map has emptied itself as its own rule said it would.
//
// It is kept, empty, because the mechanism is not spent: a file that moves
// again — Task 44 asks whether some should — needs an entry here for exactly
// one commit, or its key changes and the allowlist goes stale in a way that
// reads as a missing surface rather than a move.
var commandPaletteTargetPackage = map[string]string{}

var commandPaletteProcessKeyAudit = map[string]commandPaletteSurfaceAudit{
	"app.(*AIChatPanel).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "focused AI panel commands are supplied by the panel-context palette provider; text and link navigation remain local",
	},
	"app.(*ArkanoidFrame).ProcessKey": {
		class: paletteAuditFrameProvider, rationale: "Arkanoid commands are supplied by commandPaletteArkanoidEntries",
	},
	"cmdline.(*ApplyOutputDialog).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "the apply-output window is modal and only adds its local close key",
	},
	"cmdline.(*CommandLine).ProcessKey": {
		class: paletteAuditParentControl, rationale: "command-line editing primitives belong to PanelsFrame rather than being standalone commands",
	},
	"app.(*commandPaletteDialog).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "the palette dialog owns query, navigation, execution, and cancellation while it is open",
	},
	"editor.(*EditorView).ProcessKey": {
		class: paletteAuditActionArea, rationale: "editor commands are registered actions; raw text and cursor editing remain local primitives",
	},
	"app.(*SearchResultsWindow).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "find results are a modal result picker whose F3/F4/F5 buttons route to existing view/edit/temporary-panel operations",
	},
	"panel.(*FileSystemPanel).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "panel actions and audited transient panel keys are exposed by the action registry and panel-context provider",
	},
	"app.(*GrabberFrame).ProcessKey": {
		class: paletteAuditFrameProvider, rationale: "screen-grabber commands are supplied by commandPaletteGrabberEntries",
	},
	"app.(*SheetFrame).ProcessKey": {
		class: paletteAuditFrameProvider, rationale: "spreadsheet commands are supplied by commandPaletteSheetEntries; cell editing, cursor movement and block marking remain local primitives",
	},
	"settings.(*settingsCenter).ProcessKey":   {class: paletteAuditModalLocal, rationale: "Settings Center local search, editing, scrolling and pane navigation; Settings.Open is the registered entry point"},
	"settings.(*settingsEdit).ProcessKey":     {class: paletteAuditModalLocal, rationale: "Settings Center local search, editing, scrolling and pane navigation; Settings.Open is the registered entry point"},
	"app.(*hotkeyPage).ProcessKey":            {class: paletteAuditModalLocal, rationale: "the embedded Hotkey Configurator wraps local vertical focus; Settings.Open is its registered entry point"},
	"settings.(*settingsHelp).ProcessKey":     {class: paletteAuditModalLocal, rationale: "Settings Center local search, editing, scrolling and pane navigation; Settings.Open is the registered entry point"},
	"settings.(*settingsRadios).ProcessKey":   {class: paletteAuditModalLocal, rationale: "Settings Center local search, editing, scrolling and pane navigation; Settings.Open is the registered entry point"},
	"settings.(*settingsViewport).ProcessKey": {class: paletteAuditModalLocal, rationale: "Settings Center local search, editing, scrolling and pane navigation; Settings.Open is the registered entry point"},
	"dialog.(*HotkeyAssignFrame).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "the hotkey-capture dialog must consume the next key locally and is not a global command surface",
	},
	"panel.(*PluginHotkeyAssignFrame).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "the plugin hotkey assignment dialog captures its next key locally and is not a global command surface",
	},
	"media.(*ImageView).ProcessKey": {
		class: paletteAuditFrameProvider, rationale: "image-viewer commands are supplied by commandPaletteImageEntries",
	},
	"media.(*VideoView).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "the video player is a modal frame over a window of its own; play, seek and volume are local primitives sent down mpv's socket",
	},
	"panel.(*PlayerPanel).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "the player's transport, volume and playlist keys are navigation inside one panel; the panel toggle itself is the Panel.Player action",
	},
	"panel.(*InfoPanel).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "the focused information-panel command is supplied by the panel-context palette provider",
	},
	"macro.(*MacroAssignFrame).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "macro assignment intentionally captures the next key inside its modal dialog",
	},
	"panel.(*PanelsFrame).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "PanelsFrame combines registered actions with audited transient panel-context entries",
	},
	"panel.(*menuKeyLabelsFrame).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "the key-label menu wrapper only forwards menu navigation and local cancellation handling",
	},
	"panel.(*driveBookmarkEditDialog).ProcessKey": {
		class: paletteAuditModalLocal, rationale: "the drive-bookmark editor captures its optional hotkey and delegates the remaining field and button handling locally",
	},
	"panel.(*DriveMenuFrame).ProcessKey": {
		class: paletteAuditDynamicProvider, rationale: "the drive menu wrapper preserves local menu handling while its runtime drive and bookmark entries come from dynamic providers",
	},
	"panel.(*PluginPanelInstance).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "native panel plugins receive raw input inside their registered panel surface; their semantic commands are plugin-owned",
	},
	"panel.(*QuickViewPanel).ProcessKey": {
		class: paletteAuditPanelProvider, rationale: "the focused Quick View toggle is supplied by the panel-context palette provider",
	},
	"fileops.(*QueueFrame).ProcessKey": {
		class: paletteAuditFrameProvider, rationale: "queue commands are supplied by commandPaletteQueueEntries",
	},
	"viewer.(*ViewerView).ProcessKey": {
		class: paletteAuditActionArea, rationale: "viewer commands are registered actions; scrolling and selection remain local primitives",
	},
	"dummy_rpc.(*DummyPlugin).ProcessKey": {
		class: paletteAuditTransportHook, rationale: "this is the RPC plugin ProcessKey protocol hook, not an in-process frame",
	},
	"envman.(*managerWindow).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "Environment Manager owns these keys inside its plugin window, reached through its rich command",
	},
	"mediainfo.(*reportWindow).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "MediaInfo owns its F4 editor handoff while the modal report window is open",
	},
	"mediainfo.(*reportTextView).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "the MediaInfo report view consumes scrolling and navigation keys as an embedded dialog control",
	},
	"netfox.(*protoUIContainer).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "NetFox protocol controls consume keys inside the connection dialog",
	},
	"sqlite.(*browserWindow).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "the SQLite client owns F9 inside its modal browser window, which is reached through its own command",
	},
	"visren.(*Dialog).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "VisRen owns these keys inside the rename dialog, reached through its rich command",
	},
	"visren.(*previewList).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "the preview list is an embedded VisRen dialog control",
	},
	"visren.(*tokenButton).ProcessKey": {
		class: paletteAuditPluginLocal, rationale: "the token button is an embedded VisRen dialog control",
	},
	"plughost.(*rpcVUIPanel).ProcessKey": {
		class: paletteAuditTransportHook, rationale: "RPC panel input is forwarded to the remote plugin, whose .vui document owns its semantic commands",
	},
}

var commandPaletteNewVMenuAudit = map[string]commandPaletteSurfaceAudit{
	"app.actionFoldersHistory#1": {
		class: paletteAuditDynamicAction, rationale: "the registered folder-history action opens a runtime history list",
	},
	"app.actionCommandHistory#1": {
		class: paletteAuditDynamicAction, rationale: "the registered command-history action opens a runtime history list",
	},
	"app.actionSortMenuForPanel#1": {
		class: paletteAuditDynamicAction, rationale: "the registered sort-menu action opens choices that are also backed by sort actions",
	},
	"panel.(*BookmarksDialog).open#1": {
		class: paletteAuditDynamicProvider, rationale: "bookmark slots are runtime data and live slots are exposed by commandPaletteBookmarkEntries",
	},
	"editor.(*EditorView).showFindAllMenu#1": {
		class: paletteAuditModalLocal, rationale: "Find All results are a query-local result selector reached through the registered editor search action",
	},
	"dialog.NewCodepageMenu#1": {
		class: paletteAuditDynamicAction, rationale: "the registered viewer, editor and convert-codepage actions all open the runtime codepage list through this builder",
	},
	"editor.(*EditorView).ShowPluginsMenu#1": {
		class: paletteAuditDynamicAction, rationale: "the registered editor plugins action opens Base64 transformations and the line-sort operation",
	},
	"panel.(*AssocEditorState).openList#1": {
		class: paletteAuditModalLocal, rationale: "association rows are edited inside the file-association settings workflow",
	},
	"panel.showAssociationPicker#1": {
		class: paletteAuditDynamicAction, rationale: "matching file associations are runtime choices reached through the registered file operation",
	},
	"panel.showMountList#1": {
		class: paletteAuditDynamicAction, rationale: "the registered mount-list action opens the current mount inventory",
	},
	"panel.(*PanelsFrame).menuItemsWithKeyLabels#1": {
		class: paletteAuditPluginDialogBridge, rationale: "the generic callback-based plugin menu bridge adds runtime plugin rows and optional key labels that are not globally enumerable commands",
	},
	"panel.(*PanelsFrame).showDriveMenuAt#1": {
		class: paletteAuditDynamicProvider, rationale: "registered drives are mirrored by commandPaletteDriveEntries with live factory re-resolution",
	},
	"panel.(*userMenuState).pushLevel#1": {
		class: paletteAuditDynamicProvider, rationale: "executable user-menu leaves are flattened by commandPaletteUserMenuEntries",
	},
	"app.actionViewerEditorHistory#1": {
		class: paletteAuditDynamicAction, rationale: "the registered viewer/editor history action opens runtime history entries",
	},
	"panel.(*QuickViewPanel).showCodepageDialog#1": {
		class: paletteAuditDynamicAction, rationale: "the focused Quick View codepage action opens the runtime codepage list",
	},
	"panel.showTempPanelSlots#1": {
		class: paletteAuditModalLocal, rationale: "the temporary-panel slot picker is a local modal menu; its entries are dynamic panel state, not standalone actions",
	},
}

func TestCommandPaletteProductionCommandSurfaceInventory(t *testing.T) {
	files := commandPaletteParseProductionGo(t)
	processKeys := make(map[string]bool)
	newVMenus := make(map[string]bool)

	for _, source := range files {
		aliases, dotImport := commandPaletteVTUIImportAliases(source.File)
		packageOrdinal := 0
		for _, declaration := range source.File.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				ast.Inspect(declaration, func(node ast.Node) bool {
					call, isCall := node.(*ast.CallExpr)
					if !isCall || !commandPaletteIsNewVMenuCall(call, aliases, dotImport) {
						return true
					}
					packageOrdinal++
					newVMenus[source.pkg+".package-init#"+strconv.Itoa(packageOrdinal)] = true
					return true
				})
				continue
			}
			identity := commandPaletteFunctionIdentity(t, source.fset, function)
			if function.Recv != nil && function.Name.Name == "ProcessKey" {
				processKeys[source.pkg+"."+identity] = true
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
				newVMenus[source.pkg+"."+identity+"#"+strconv.Itoa(ordinal)] = true
				return true
			})
		}
	}

	commandPaletteAssertSurfaceInventory(t, "ProcessKey receiver", processKeys, commandPaletteProcessKeyAudit)
	commandPaletteAssertSurfaceInventory(t, "vtui.NewVMenu call", newVMenus, commandPaletteNewVMenuAudit)

	if got := commandPaletteCountF4Surfaces(commandPaletteProcessKeyAudit, commandPaletteNewVMenuAudit); got != commandPaletteF4Surfaces {
		t.Fatalf("f4 command surfaces under audit = %d, want %d: a surface and its audit entry were removed together", got, commandPaletteF4Surfaces)
	}
}

type commandPaletteParsedGo struct {
	// path is module-relative and reported to a human; pkg is what audit keys
	// are built from, so that moving a file does not rewrite them.
	path string
	pkg  string
	File *ast.File
	fset *token.FileSet
}

func commandPaletteParseProductionGo(t *testing.T) []commandPaletteParsedGo {
	t.Helper()
	// The inventory spans the whole module (main package and plugins alike),
	// so walk from the module root, not this package's directory.
	root := testutil.ModuleRootDir(t)
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
		slashed := filepath.ToSlash(relative)
		files = append(files, commandPaletteParsedGo{
			path: slashed,
			pkg:  commandPalettePackageOf(slashed),
			File: parsed,
			fset: fset,
		})
	}
	return files
}

// commandPalettePackageOf answers which package a module-relative source path
// will belong to. Everything outside cmd/f4 is already where it will stay, so
// its directory is the answer; a cmd/f4 file is asked of the target map first,
// and falling through to "f4" is how an unaudited new surface announces itself.
func commandPalettePackageOf(relative string) string {
	if strings.HasPrefix(relative, "cmd/f4/") {
		if target, ok := commandPaletteTargetPackage[path.Base(relative)]; ok {
			return target
		}
		return "f4"
	}
	if directory := path.Dir(relative); directory != "." {
		return path.Base(directory)
	}
	return "f4"
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

func commandPaletteVTUIImportAliases(File *ast.File) (map[string]bool, bool) {
	aliases := make(map[string]bool)
	dotImport := false
	for _, spec := range File.Imports {
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

// commandPaletteCountF4Surfaces counts audited surfaces that belong to f4 rather
// than to a plugin, by the package half of their key.
func commandPaletteCountF4Surfaces(audits ...map[string]commandPaletteSurfaceAudit) int {
	// f4's own packages, as opposed to a plugin's. commandPaletteTargetPackage
	// cannot be the only source: it empties itself as waves land, so a surface
	// whose file has finished moving would stop being counted and the constant
	// below would drift down with it. The layer map names every package of
	// ours that exists, which is what the count is actually about.
	packages := map[string]bool{"f4": true, "app": true}
	for path := range architectureLayers {
		packages[strings.TrimPrefix(path, "internal/")] = true
	}
	for _, target := range commandPaletteTargetPackage {
		packages[target] = true
	}
	total := 0
	for _, audit := range audits {
		for key := range audit {
			name, _, _ := strings.Cut(key, ".")
			if packages[name] {
				total++
			}
		}
	}
	return total
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
