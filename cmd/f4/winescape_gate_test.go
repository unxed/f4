package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/testutil"
)

// The libwinescape gate auditor.
//
// libwinescape lets a Windows f4 make raw Linux syscalls under Wine. The
// "UseWinescape" setting and the personality decision in vfs/hostmode have to
// cover every use of it, or turning the setting off leaves some code still
// talking to the host kernel behind the user's back. That was the shape of the
// original gap: redirectDetachedStdout lived outside vfs/ and did not ask.
//
// So the set of files that import libwinescape is closed. A file that wants to
// join it -- a native pty backend for the built-in terminal is the standing
// example -- must be added below together with a line saying how it obeys the
// setting: ask hostmode.Allowed() before using the library, or hostmode.Posix()
// to choose the personality. The test fails until that line exists, which is the
// point: it is where the question gets asked.
//
// Build tags are ignored on purpose; almost every importer is windows-only and
// the test has to see them from a Linux runner.

const winescapeImportPath = "github.com/unxed/libwinescape/go"

var winescapeImporters = map[string]string{
	"internal/app/bootstrap_detach_windows.go":      "asks hostmode.Allowed() before redirecting Wine's fd 2",
	"internal/sysinfo/fs_windows.go":                "FS()'s posix branch, gated by hostmode.Posix()",
	"internal/terminal/native_command_windows.go":   "native simple/captured commands; every use is gated by hostmode.Posix()",
	"internal/terminal/pty_wine_windows.go":         "the native terminal; every use is gated by hostmode.Posix() (winePTYUsable)",
	"internal/terminal/wineprobe_escape_windows.go": "diagnostics only; reports hostmode.Posix() and Allowed()",
	"vfs/hostfs/errno_windows.go":                   "error translation for hostfs; reached only through it",
	"vfs/hostfs/hostfs_windows.go":                  "every libwinescape call sits behind hostmode.Posix()",
	"vfs/hostfs/hostfs_winescape.go":                "the posix backend of hostfs; reached only when hostmode.Posix()",
	"vfs/hostmode/hostmode.go":                      "the decision itself: Allowed(), then the probe",
	"vfs/os_vfs_physical_windows.go":                "branches on hostmode.Posix()",
	"vfs/os_vfs_windows.go":                         "names the *winescape.Stat_t type in an assertion; makes no calls",
	"vfs/rename_noreplace_windows.go":               "branches on hostmode.Posix()",
}

func TestLibwinescapeImportersAreAccountedFor(t *testing.T) {
	root := testutil.ModuleRootDir(t)
	found := map[string]bool{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "artifacts":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if perr != nil {
			return nil // a file that does not parse is another test's finding
		}
		for _, imp := range file.Imports {
			if strings.Trim(imp.Path.Value, `"`) == winescapeImportPath {
				rel, rerr := filepath.Rel(root, path)
				if rerr != nil {
					return rerr
				}
				found[filepath.ToSlash(rel)] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}

	var unexpected, stale []string
	for f := range found {
		if _, ok := winescapeImporters[f]; !ok {
			unexpected = append(unexpected, f)
		}
	}
	for f := range winescapeImporters {
		if !found[f] {
			stale = append(stale, f)
		}
	}
	sort.Strings(unexpected)
	sort.Strings(stale)

	for _, f := range unexpected {
		t.Errorf("%s imports libwinescape but is not in winescapeImporters. Whatever it does with the library "+
			"has to follow the UseWinescape setting: ask hostmode.Allowed() (or hostmode.Posix()) before using it, "+
			"then add the file here with a line saying how it does", f)
	}
	for _, f := range stale {
		t.Errorf("%s is listed in winescapeImporters but no longer imports libwinescape; remove the entry", f)
	}
}
