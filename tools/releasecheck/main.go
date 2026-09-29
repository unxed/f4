// Command releasecheck refuses a release that an f4 already installed would
// misread.
//
//	go run ./tools/releasecheck dist
//
// It takes the files in the directory a release is about to be published
// from, replays the asset choice of every generation of f4 still installed
// (update.AuditRelease), and unpacks each archive they would install with the
// updater's own code, over a stand-in for the executable it has to replace.
// Any problem is printed as a GitHub Actions error and the exit status is 1,
// so the release is not published and the one before it stays in place
// (#1656).
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/unxed/f4/internal/update"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, out io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(out, "usage: releasecheck <directory of release assets>")
		return 2
	}
	dir := args[0]
	entries, err := os.ReadDir(dir)
	if err != nil {
		_, _ = fmt.Fprintf(out, "::error::%v\n", err)
		return 1
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}

	failed := false
	audit := update.AuditRelease(names)
	for _, p := range audit.Problems {
		_, _ = fmt.Fprintf(out, "::error::%s\n", p)
		failed = true
	}
	for _, a := range audit.Archives {
		data, err := os.ReadFile(filepath.Join(dir, a.Name))
		if err == nil {
			err = update.CheckReleaseArchive(data, a)
		}
		if err != nil {
			_, _ = fmt.Fprintf(out, "::error::%s: %v\n", a.Name, err)
			failed = true
			continue
		}
		_, _ = fmt.Fprintf(out, "ok: %s replaces %s\n", a.Name, a.Executable)
	}
	if failed {
		return 1
	}
	return 0
}
