package main

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/testutil"
)

// The module boundary auditor. Four of the dependency rules in
// .ai-factory/ARCHITECTURE.md are mechanically checkable, and this is where
// they are checked. A rule that is only written down is a rule that gets
// broken by the next person who does not know it exists.
//
// Standard library only, and no new module dependency: the import graph comes
// from the toolchain itself.

const architectureModule = "github.com/unxed/f4"

// architectureLayers places each internal package in the dependency order the
// architecture document defines: 0 depends on nothing of ours, 4 is the
// application that composes the rest. Extracting a package adds one line here.
//
// internal/hideconsole is deliberately absent. It is a vendored fork carrying
// its own go.mod, so it is a separate module and `go list ./...` never returns
// it — adding it here would describe a package this test cannot see.
var architectureLayers = map[string]int{
	"internal/netproxy": 0,
	"internal/ttyx":     0,
	"internal/wincon":   0,

	// Checked conversions, shared by seven packages. Zero imports of ours.
	"internal/numeric": 0,

	// The --install/--self-install CLI command. Standalone: it copies the
	// running executable and edits a shell profile, importing nothing else
	// of ours.
	"internal/install": 0,

	// Key naming, remapping, input translation and the X key grabs. Reads
	// internal/config like internal/theme does, and internal/numeric for the
	// checked conversions the kitty and mouse decoders need.
	"internal/keymap": 0,

	// Colours, colour space maths, the styles/ that ship with f4, and the rules
	// that colour a file by its name. It reads internal/config the way any
	// package may — 0 to 0 is not an upward import — because eight settings
	// steer it and threading eight parameters through a colour table buys
	// nothing.
	"internal/theme": 0,

	// The string table and the language packs, with lang/ embedded beside them.
	// It takes the two configured languages and the profile directory as
	// arguments rather than importing internal/config: netfox's own test
	// imports the plugin, the plugin imports this, and that would close a cycle.
	"internal/i18n": 0,

	// The user's configuration: F4Config, config.App, the ini round trip, and
	// the schema of every field — the enumeration it may hold, the parser that
	// normalises it, the default it falls back to.
	"internal/config": 0,

	// The ini parser. Its own package because the four configuration leaves —
	// config, i18n, theme, keymap — all parse ini files and none of them may
	// import another of ours; a package that imports nothing can be shared by
	// all four without putting one of them under another.
	"internal/ini": 0,

	// The far2l file mask matcher. A leaf because a file mask is a string
	// question with no owner: the panel matches associations with it, the
	// archive plugin asks it which names Enter must leave to their
	// association, and neither may import the other.
	"internal/filemask": 0,

	// Menu hotkeys made distinct once a menu is built. A leaf over vtui only.
	"internal/menuhotkeys": 0,

	// Where f4 keeps the indexes of the tar archives it opened: paths and file
	// names only, so the archive plugin and the file operations can share it.
	"internal/tarindexcache": 0,

	// The frame watchdog: a leaf that imports nothing of ours, so any view
	// can mark its frame and the root can arm it from a command line switch.
	"internal/stallwatch": 0,

	// The shared primitives: a notification channel and the history store.
	// Both are leaves and both take what they cannot reach as a seam —
	// history.SamePath and the config directory are set by the root.
	"internal/toast":   0,
	"internal/history": 0,

	// The registry mechanism. The table that fills it stays in the root: its
	// closures reach every view in the application.
	"internal/action":   0,
	"internal/appcmd":   0,
	"internal/semantic": 0,

	// The hardware probes. A leaf in the strict sense: it imports no package of
	// ours, which is what lets any layer call it.
	"internal/sysinfo": 0,

	// Archive extraction over a directory on disk, and the path guard that
	// keeps an archive member inside it. Three callers, none of which is a
	// dependency of the other two.
	"internal/unpack": 0,

	// Self-update: check, download, install, elevate, and the self-exec rules
	// the install has to know. A leaf over the network, not an interactive
	// subsystem, which is why it is layer 1 and not layer 3.
	"internal/update": 1,

	// Data with an embed directive beside it, nothing else.
	"internal/colorer": 0,

	// Self-contained subsystems. textlayout is the only edge among them: it
	// reads the piece table it lays out.
	"internal/piecetable": 0,
	"internal/sheet":      0,
	"internal/luaplug":    0,
	"internal/textlayout": 1,

	// These two sit on vfs, which stays public.
	"internal/fusefs": 1,
	"internal/vtvibe": 1,

	// Modal dialogs, the help viewer and help/ beside it. Layer 3 for the
	// company it keeps rather than for what it imports: it may reach every
	// leaf and none of the interactive subsystems.
	"internal/plughost": 2,

	"internal/gui": 2,

	"internal/macro": 3,

	"internal/viewer": 3,

	// fileops starts here with the per-file state store, which the viewer and
	// the editor both read; Task 32 brings the rest.
	"internal/fileops": 1,
	"internal/editor":  3,
	"internal/cmdline": 3,

	// Layer 3, not the 1 the plan assigned: the terminal reads gui.Running to
	// tell a window from a TTY, and the viewer's URL model to underline a link
	// under the mouse. Nothing below layer 3 imports it.
	"internal/terminal": 3,

	// media reads the terminal's graphics protocols and the viewer's title bar,
	// so it is layer 3 beside them, not the 1 the plan assigned.
	"internal/media": 3,

	"internal/textsearch": 0,

	"internal/dialog":   3,
	"internal/settings": 3,

	// The panels frame sits at the top of the interactive layer: it holds the
	// command line, the terminal view and the file panels at once, and reaches
	// the editor and the viewer to open a file. Everything above it is the
	// application, which is why what it needs from there is a seam in host.go
	// rather than an import.
	"internal/panel": 3,

	// The composition root. It is the only package allowed to import every
	// other, and rule 3 is the other half of that: nothing below layer 4 may
	// import it. cmd/f4 is now a call to app.Main and the four auditors here.
	"internal/app": 4,

	// Test scaffolding, placed by what it may import: testutil imports no
	// package of ours, paneltest sits above the three it builds a frame from.
	// Neither may be imported from production code.
	"internal/testutil":     0,
	"internal/settingstest": 0,
	"internal/paneltest":    4,
}

// architectureGOOS is the set of platforms the graph is collected for. One
// pass would only see the files the host's build tags select, and this
// repository keeps a quarter of its sources behind a tag: a windows-only
// upward import would be invisible from a darwin test run.
var architectureGOOS = []string{"", "linux", "windows"}

func TestArchitectureModuleBoundaries(t *testing.T) {
	graph := architectureImportGraph(t)

	// Rule 1: the public contract stays public. sdk/ is what third-party
	// plugins compile against and vfs/ is what they speak; an internal
	// import in either turns a private decision into a published one.
	t.Run("PublicContractStaysPublic", func(t *testing.T) {
		var offenders []string
		for importer, imports := range graph {
			if !underAny(importer, architectureModule+"/sdk/", architectureModule+"/vfs/", architectureModule+"/sdk", architectureModule+"/vfs") {
				continue
			}
			for _, imported := range imports {
				if strings.Contains(imported, "/internal/") {
					offenders = append(offenders, importer+" -> "+imported)
				}
			}
		}
		reportEdges(t, "a public package imports module-private code", offenders)
	})

	// Rule 2: nothing imports the entry point. cmd/f4 is a composition root,
	// and a composition root with importers is just another library.
	t.Run("NothingImportsTheEntryPoint", func(t *testing.T) {
		var offenders []string
		for importer, imports := range graph {
			for _, imported := range imports {
				if imported == architectureModule+"/cmd/f4" {
					offenders = append(offenders, importer+" -> "+imported)
				}
			}
		}
		reportEdges(t, "the entry point is imported", offenders)
	})

	// Rule 3: no upward imports into the application. internal/app composes
	// the lower layers; a lower layer reaching back into it is the cycle the
	// whole extraction exists to prevent.
	t.Run("NothingBelowTheApplicationImportsIt", func(t *testing.T) {
		application := architectureModule + "/internal/app"
		var offenders []string
		for importer, imports := range graph {
			if importer == architectureModule+"/cmd/f4" || importer == application {
				continue
			}
			for _, imported := range imports {
				if imported == application {
					offenders = append(offenders, importer+" -> "+imported)
				}
			}
		}
		reportEdges(t, "a package below the application imports it", offenders)
	})

	// Rule 5: sysinfo imports nothing of ours. It is the one package with a
	// private copy of a shared helper — cpu_darwin.go's boundedUint64ToInt —
	// and this is what stops a future contributor from "cleaning that up" into
	// an import that makes the leaf stop being one.
	t.Run("SysinfoImportsNothingOfOurs", func(t *testing.T) {
		sysinfo := architectureModule + "/internal/sysinfo"
		var offenders []string
		for importer, imports := range graph {
			if !underAny(importer, sysinfo) {
				continue
			}
			for _, imported := range imports {
				if strings.HasPrefix(imported, architectureModule+"/internal/") {
					offenders = append(offenders, importer+" -> "+imported)
				}
			}
		}
		reportEdges(t, "internal/sysinfo imports a package of ours", offenders)
	})

	// Rule 6: nothing imports upward. A package at layer N may import layers
	// N and below and nothing above, which is what the layer numbers above
	// have always meant and what nothing checked until now: rule 4 stays
	// quiet about `internal/config` -> `internal/panel`, because one edge is
	// not a cycle. It is what makes `config.App` safe as a global — the
	// package can only ever reach layer 0.
	t.Run("NothingImportsUpward", func(t *testing.T) {
		var offenders []string
		for importer, imports := range graph {
			from, known := architectureLayerOf(importer)
			if !known {
				continue
			}
			for _, imported := range imports {
				to, known := architectureLayerOf(imported)
				if !known || from >= to {
					continue
				}
				offenders = append(offenders,
					fmt.Sprintf("%s (layer %d) -> %s (layer %d)", importer, from, imported, to))
			}
		}
		sort.Strings(offenders)
		reportEdges(t, "a package imports one from a higher layer", offenders)
	})

	// Rule 4: the module's own import graph is acyclic. The compiler refuses
	// a cycle before this test ever runs, so this is a second pair of eyes
	// whose value is the message: it names the path, which a build error on
	// a twelve-package loop does not.
	t.Run("ImportGraphIsAcyclic", func(t *testing.T) {
		if cycle := findImportCycle(graph); cycle != nil {
			t.Fatalf("import cycle: %s", strings.Join(cycle, " -> "))
		}
	})
}

// TestArchitectureLayerMapMatchesTheTree keeps the layer map honest: a package
// listed here that no longer exists is a stale line, and the map is what later
// layer rules and the architecture document are checked against.
func TestArchitectureLayerMapMatchesTheTree(t *testing.T) {
	graph := architectureImportGraph(t)
	for suffix := range architectureLayers {
		if _, ok := graph[architectureModule+"/"+suffix]; !ok {
			t.Errorf("architectureLayers names %q, which is not a package in this module", suffix)
		}
	}
}

// architectureLayerOf places one import path, and reports whether the map knows
// it. Anything outside this module — and `cmd/f4`, which is the root rather
// than a layer — is not placed.
func architectureLayerOf(importPath string) (int, bool) {
	suffix, found := strings.CutPrefix(importPath, architectureModule+"/")
	if !found {
		return 0, false
	}
	layer, known := architectureLayers[suffix]
	return layer, known
}

// TestArchitectureLayerMapCoversEveryInternalPackage is the other half of rule
// 6: an unplaced package is not an exempt package, it is an unchecked one, and
// the map is edited by hand once per extraction.
func TestArchitectureLayerMapCoversEveryInternalPackage(t *testing.T) {
	graph := architectureImportGraph(t)
	var missing []string
	for importPath := range graph {
		suffix, found := strings.CutPrefix(importPath, architectureModule+"/internal/")
		if !found {
			continue
		}
		if _, known := architectureLayers["internal/"+suffix]; !known {
			missing = append(missing, "internal/"+suffix)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("these packages have no layer and are therefore checked by nothing:\n\t%s",
			strings.Join(missing, "\n\t"))
	}
}

func architectureImportGraph(t *testing.T) map[string][]string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	graph := make(map[string][]string)
	for _, goos := range architectureGOOS {
		command := exec.Command("go", "list", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", "./...")
		command.Dir = testutil.ModuleRootDir(t)
		if goos != "" {
			command.Env = append(command.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
		}
		out, err := command.Output()
		if err != nil {
			stderr := ""
			if exitErr, ok := err.(*exec.ExitError); ok {
				stderr = string(exitErr.Stderr)
			}
			// Not a skip: a silent skip would disable the auditor for the
			// rest of the migration, which is exactly when it is needed.
			t.Fatalf("go list for GOOS=%q: %v\n%s", goos, err, stderr)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			graph[fields[0]] = append(graph[fields[0]], fields[1:]...)
		}
	}
	return graph
}

func underAny(path string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
			return true
		}
	}
	return false
}

// findImportCycle returns one cycle as the path that closes it, restricted to
// this module's own packages, or nil when there is none.
func findImportCycle(graph map[string][]string) []string {
	const (
		visiting = 1
		done     = 2
	)
	state := make(map[string]int, len(graph))
	var path []string
	var walk func(node string) []string
	walk = func(node string) []string {
		switch state[node] {
		case done:
			return nil
		case visiting:
			for index, seen := range path {
				if seen == node {
					return append(append([]string(nil), path[index:]...), node)
				}
			}
			return []string{node, node}
		}
		state[node] = visiting
		path = append(path, node)
		for _, next := range graph[node] {
			if !strings.HasPrefix(next, architectureModule+"/") && next != architectureModule {
				continue
			}
			if cycle := walk(next); cycle != nil {
				return cycle
			}
		}
		path = path[:len(path)-1]
		state[node] = done
		return nil
	}

	roots := make([]string, 0, len(graph))
	for node := range graph {
		roots = append(roots, node)
	}
	sort.Strings(roots)
	for _, node := range roots {
		if cycle := walk(node); cycle != nil {
			return cycle
		}
	}
	return nil
}

func reportEdges(t *testing.T, what string, offenders []string) {
	t.Helper()
	if len(offenders) == 0 {
		return
	}
	sort.Strings(offenders)
	offenders = dedupeStrings(offenders)
	t.Fatalf("%s:\n\t%s", what, strings.Join(offenders, "\n\t"))
}

func dedupeStrings(sorted []string) []string {
	out := sorted[:0]
	for index, value := range sorted {
		if index == 0 || value != sorted[index-1] {
			out = append(out, value)
		}
	}
	return out
}
