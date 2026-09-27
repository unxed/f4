package multiarc

import (
	"context"
	"errors"
)

// errNoZipWriter is what a zip write answers when neither Info-ZIP's zip nor
// a 7-Zip that handles zip is on PATH. unzip, which is all browsing needs,
// only reads.
var errNoZipWriter = errors.New("multiarc: changing a .zip archive needs zip, 7z or 7za on PATH (unzip only reads)")

// zipWriteTool picks what changes a zip: Info-ZIP's zip when it is on PATH,
// otherwise 7z or 7za, which handle zip as well. 7zr does not: it is the
// 7z-format-only build. bsdtar is no use here either -- it writes a zip
// from scratch but refuses to append to one.
func zipWriteTool() (bin string, sevenZip bool, ok bool) {
	if toolAvailable("zip") {
		return "zip", false, true
	}
	for _, name := range []string{"7z", "7za"} {
		if toolAvailable(name) {
			return name, true, true
		}
	}
	return "", false, false
}

func (zipBackend) checkWrite(context.Context, string, writeOp, string) error {
	if _, _, ok := zipWriteTool(); !ok {
		return errNoZipWriter
	}
	return nil
}

// add runs in stageDir, so each member is stored under the relative path
// it was staged at. Info-ZIP's zip replaces an entry of the same name, and
// so does 7z a, so replaced needs no work of its own. -nw keeps zip from
// expanding "*", "?" or "[...]" in a name. Both tools write the updated
// archive to a temporary file and rename it over the original.
func (zipBackend) add(ctx context.Context, localPath, stageDir string, members, _ []string) error {
	bin, sevenZip, ok := zipWriteTool()
	if !ok {
		return errNoZipWriter
	}
	if sevenZip {
		return runSevenZip(ctx, stageDir, bin, []string{"a", "-tzip", "-y"}, localPath, members)
	}
	return runChunked(ctx, stageDir, bin, []string{"-q", "-nw", localPath}, members)
}

// remove deletes by raw name. zip -d does not reach into a directory named
// by its "dir/" entry, so it is handed every entry under it as well; 7z d
// does, and gets the covering names only.
func (zipBackend) remove(ctx context.Context, localPath string, raws []string) error {
	bin, sevenZip, ok := zipWriteTool()
	if !ok {
		return errNoZipWriter
	}
	if sevenZip {
		return runSevenZip(ctx, "", bin, []string{"d", "-y"}, localPath, coveringNames(raws))
	}
	return runChunked(ctx, "", bin, []string{"-q", "-nw", "-d", localPath}, raws)
}
