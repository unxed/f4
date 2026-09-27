package multiarc

import (
	"context"
	"errors"
	"os"
	"strings"
)

// errNoSevenZip is what every 7z write answers when no 7z, 7za or 7zr is on
// PATH any more (it was when the archive was opened).
var errNoSevenZip = errors.New("multiarc: changing a .7z archive needs 7z, 7za or 7zr on PATH")

// checkWrite: 7-Zip can add, replace and delete in every build of it, 7zr
// included (7zr only drops the other formats, and this backend only ever
// handles .7z).
func (b sevenZipBackend) checkWrite(context.Context, string, writeOp, string) error {
	if _, ok := b.tool(); !ok {
		return errNoSevenZip
	}
	return nil
}

// sevenZipNameSwitches is the switch list that goes before "--" in a 7z
// command naming members: -spd turns off wildcard matching, needed only for
// a name that holds "*" or "?" and left out otherwise so that an old p7zip
// without that switch still takes every ordinary name. "--" after it stops
// 7-Zip's parsing of switches, so a member called "-x" is just a name. Only
// some 7-Zip builds also stop reading "@list" as a list file there (the one
// on macOS runners does not), so a name starting with "@" goes through a
// list file of its own instead; see sevenZipNames.
func sevenZipNameSwitches(names []string, switches ...string) []string {
	if hasWildcard(names) {
		switches = append(switches, "-spd")
	}
	return switches
}

// sevenZipNames runs a 7z command that names members: run gets the extra
// switches and the arguments that follow the archive. Normally that is
// "--" and the names. When a name starts with "@", which some 7-Zip builds
// read as a list file even after "--", every name goes into a UTF-8 list
// file instead (-scsUTF-8 @file): names inside a list file are names,
// whatever they start with.
func sevenZipNames(names []string, run func(switches, tail []string) error) error {
	if !hasAtName(names) {
		return run(nil, append([]string{"--"}, names...))
	}
	f, err := os.CreateTemp("", "f4-7z-names-*.txt")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = f.WriteString(strings.Join(names, "\n") + "\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return run([]string{"-scsUTF-8"}, []string{"@" + f.Name()})
}

// runSevenZip runs "bin command... archive -- names..." in dir, split over
// as many command lines as the names need (runChunked), or once with a list
// file when a name starts with "@" (sevenZipNames).
func runSevenZip(ctx context.Context, dir, bin string, command []string, archive string, names []string) error {
	if !hasAtName(names) {
		head := append(append([]string(nil), command[:1]...), sevenZipNameSwitches(names, command[1:]...)...)
		return runChunked(ctx, dir, bin, append(head, archive), names)
	}
	return sevenZipNames(names, func(switches, tail []string) error {
		head := append(append([]string(nil), command[:1]...), sevenZipNameSwitches(names, append(append([]string(nil), command[1:]...), switches...)...)...)
		return runToolChecked(ctx, dir, bin, append(append(head, archive), tail...)...)
	})
}

// hasAtName reports whether any name starts with "@".
func hasAtName(names []string) bool {
	for _, name := range names {
		if strings.HasPrefix(name, "@") {
			return true
		}
	}
	return false
}

// add runs "7z a" in stageDir, so each member is stored under the relative
// path it was staged at. 7z a replaces a member of the same name whatever
// the two timestamps say, so replaced needs no work of its own. 7-Zip
// builds the updated archive in a temporary file and renames it over the
// original only once it is complete.
func (b sevenZipBackend) add(ctx context.Context, localPath, stageDir string, members, _ []string) error {
	bin, ok := b.tool()
	if !ok {
		return errNoSevenZip
	}
	return runSevenZip(ctx, stageDir, bin, []string{"a", "-t7z", "-y"}, localPath, members)
}

// remove runs "7z d". Naming a directory deletes everything under it too,
// so only the covering names are passed.
func (b sevenZipBackend) remove(ctx context.Context, localPath string, raws []string) error {
	bin, ok := b.tool()
	if !ok {
		return errNoSevenZip
	}
	return runSevenZip(ctx, "", bin, []string{"d", "-y"}, localPath, coveringNames(raws))
}
