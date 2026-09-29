// Command releasecheck refuses a release that an f4 already installed would
// misread.
//
//	go run ./tools/releasecheck dist
//	go run ./tools/releasecheck -listed assets.txt
//
// Given a directory -- the one a release is about to be published from --
// it replays the asset choice of every generation of f4 still installed
// (update.AuditRelease) over the file names, and unpacks each archive they
// would install with the updater's own code, over a stand-in for the
// executable it has to replace, checking that the executable is the build
// the archive's name promises.
//
// Given -listed, a file with a published release's asset names one per line
// in the order the GitHub API returns them, it replays the choice in that
// order (update.AuditListedRelease). The check before publishing sorts the
// names the way the API lists them today; this one reads back what the
// updaters will actually see.
//
// Any problem is printed as a GitHub Actions error and the exit status is 1
// (#1656).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/update"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, out io.Writer) int {
	flags := flag.NewFlagSet("releasecheck", flag.ContinueOnError)
	flags.SetOutput(out)
	listed := flags.String("listed", "", "a file of published asset names in API order, one per line")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	switch {
	case *listed != "" && flags.NArg() == 0:
		return checkListed(*listed, out)
	case *listed == "" && flags.NArg() == 1:
		return checkDirectory(flags.Arg(0), out)
	}
	_, _ = fmt.Fprintln(out, "usage: releasecheck <directory of release assets> | releasecheck -listed <file>")
	return 2
}

func checkDirectory(dir string, out io.Writer) int {
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

	audit := update.AuditRelease(names)
	failed := report(audit, out)
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

func checkListed(path string, out io.Writer) int {
	f, err := os.Open(path)
	if err != nil {
		_, _ = fmt.Fprintf(out, "::error::%v\n", err)
		return 1
	}
	defer func() { _ = f.Close() }()
	var names []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if name := strings.TrimSpace(scanner.Text()); name != "" {
			names = append(names, name)
		}
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintf(out, "::error::%v\n", err)
		return 1
	}

	audit := update.AuditListedRelease(names)
	if report(audit, out) {
		return 1
	}
	_, _ = fmt.Fprintf(out, "ok: %d assets as listed, %d archives of f4 taken as intended\n", len(names), len(audit.Archives))
	return 0
}

// report prints the audit's problems and says whether there were any.
func report(audit update.ReleaseAudit, out io.Writer) bool {
	for _, p := range audit.Problems {
		_, _ = fmt.Fprintf(out, "::error::%s\n", p)
	}
	return len(audit.Problems) > 0
}
