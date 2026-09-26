//go:build !lite

package editor

import (
	"context"
	"errors"
	"fmt"

	colorer "github.com/unxed/colorer4go"
)

// maxColorerCheckReports bounds how many of Colorer's messages a check keeps.
// The first ones name the broken file; a long tail only repeats that.
const maxColorerCheckReports = 20

// ColorerCheck is what loading a Colorer configuration found.
type ColorerCheck struct {
	// Err is why an editor could not highlight with this configuration: the
	// session, the colour style or a scheme failed to load. Nil when it can.
	Err error
	// Reports are the errors and warnings Colorer reported on the way, the ones
	// it survived included, oldest first: a file and line where Err names only
	// a throw site, a user path that was skipped, a scheme link that does not
	// resolve.
	Reports []string
	// Types is how many file types were loaded; zero without allTypes.
	Types int
}

// Clean reports whether nothing failed and nothing was reported.
func (c ColorerCheck) Clean() bool {
	return c.Err == nil && len(c.Reports) == 0
}

// CheckColorerSource loads the configuration src names the way an editor does
// when it starts Colorer — the catalog, the user's colour styles and schemes,
// the colour style — and, with allTypes, the scheme of every file type as
// well, which is where a broken scheme otherwise shows up only once a file of
// its type is opened. It is FarColorer's TestLoadBase, except that allTypes
// really loads each type: TestLoadBase calls getBaseScheme(), which in this
// Colorer version does not load anything.
//
// progress, if not nil, is called before each type is loaded. Cancelling ctx
// stops between types and is reported as Err.
func CheckColorerSource(ctx context.Context, src ColorerSource, scheme string, allTypes bool, progress func(done, total int, label string)) ColorerCheck {
	var check ColorerCheck
	if err := colorerRuntimeCheck(); err != nil {
		check.Err = err
		return check
	}
	logLevel, logging := colorerDiagnosticsLevel()
	level := colorer.LevelWarn
	if logging && logLevel > level {
		level = logLevel
	}
	collect := func(d colorer.Diagnostic) {
		if logging && d.Level <= logLevel {
			logColorerDiagnostic(d)
		}
		if d.Level <= colorer.LevelWarn && len(check.Reports) < maxColorerCheckReports {
			check.Reports = append(check.Reports, d.String())
		}
	}

	ensureRadiolaSchema(src.ConfigsDir)
	opts := append([]colorer.Option{colorer.WithDiagnostics(level, collect)}, src.userOptions()...)
	session, err := colorer.NewSession(ctx, "/base/catalog.xml", src.ConfigsDir, opts...)
	if err != nil {
		check.Err = err
		return check
	}
	defer session.Close()

	if scheme == "" {
		scheme = "default"
	}
	if err := session.SetHRD("rgb", scheme); err != nil {
		check.Err = fmt.Errorf("colour style %q: %w", scheme, err)
		return check
	}
	if !allTypes {
		return check
	}

	types, err := session.FileTypes()
	if err != nil {
		check.Err = err
		return check
	}
	for i, ft := range types {
		if err := ctx.Err(); err != nil {
			check.Err = err
			return check
		}
		if progress != nil {
			progress(i, len(types), ft.Group+": "+ft.Description)
		}
		ok, err := session.LoadFileType(ft.Name)
		if err != nil {
			check.Err = fmt.Errorf("file type %q (%s): %w", ft.Name, ft.Description, err)
			return check
		}
		check.Types++
		if !ok && len(check.Reports) < maxColorerCheckReports {
			check.Reports = append(check.Reports, fmt.Sprintf("file type %q (%s) has no scheme", ft.Name, ft.Description))
		}
	}
	return check
}

// IsColorerCheckCancelled tells a check the user stopped from one that failed.
func IsColorerCheckCancelled(c ColorerCheck) bool {
	return errors.Is(c.Err, context.Canceled)
}
