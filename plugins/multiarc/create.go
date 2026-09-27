package multiarc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// createBuild writes a new archive into workDir from names, which are
// relative to srcDir, and returns the name, relative to workDir, of the file
// it wrote. outName is the new archive's own base name: zip and 7z are
// handed a path ending in it, so they pick the format the user asked for.
type createBuild func(ctx context.Context, srcDir string, names []string, workDir, outName string) (string, error)

// createSuffixes are the archive names Add to archive knows how to make,
// in the order its default is picked from what the tools can do: zip,
// which the regular build suggests too, then the tarball every router can
// make, then 7z, then a bare tar. The rest are listed when a name matches
// none of them.
var createSuffixes = []string{".zip", ".tar.gz", ".7z", ".tar", ".tar.bz2", ".tar.xz", ".tar.zst", ".tar.lz"}

// errNoCreator is what Add to archive says when no archiver it knows is on
// PATH at all.
var errNoCreator = errors.New("multiarc: no archiver on PATH can create an archive (looked for zip, 7z, 7za, 7zr and tar)")

// planCreate picks, from the new archive's name and the tools on PATH, how
// Add to archive builds it -- or says why it cannot.
//
//   - .zip/.jar: Info-ZIP's zip, else 7z/7za, else bsdtar (Windows 10+'s
//     tar.exe included), which writes a zip from scratch with --format zip.
//   - .7z: 7z, 7za or 7zr.
//   - .tar: any tar.
//   - .tar.gz and the other compressed tarballs: bsdtar with its own
//     compression option when it has the library (or the stand-alone
//     compressor to fall back to); any other tar -- GNU, BusyBox, the rest
//     -- writes a plain tar that the stand-alone compressor then compresses,
//     which works whether or not that tar knows -z, -J or --zstd.
func planCreate(ctx context.Context, name string) (createBuild, error) {
	lower := strings.ToLower(name)
	switch {
	case hasAnySuffix(lower, ".zip", ".jar"):
		return planCreateZip(ctx)
	case hasAnySuffix(lower, ".7z"):
		bin, ok := sevenZipTool()
		if !ok {
			return nil, errors.New("multiarc: creating a .7z archive needs 7z, 7za or 7zr on PATH")
		}
		return sevenZipCreate(bin, "-t7z"), nil
	}
	comp, plain, ok := tarCompressionFor(lower)
	if !ok {
		return nil, unknownCreateFormat(ctx, name)
	}
	if !tarAvailable() {
		return nil, fmt.Errorf("multiarc: creating %s needs tar on PATH", name)
	}
	info := probeTar(ctx)
	if plain {
		return tarCreate(info, ""), nil
	}
	if info.flavor == tarBSD && bsdCanCompress(info, comp) {
		return tarCreate(info, comp.flag), nil
	}
	if !toolAvailable(comp.tool) {
		return nil, fmt.Errorf("multiarc: creating %s needs %s on PATH", name, comp.tool)
	}
	return tarThenCompress(info, comp), nil
}

func planCreateZip(ctx context.Context) (createBuild, error) {
	if toolAvailable("zip") {
		return func(ctx context.Context, srcDir string, names []string, workDir, outName string) (string, error) {
			args := append([]string{"-q", "-r", "-nw", filepath.Join(workDir, outName), "--"}, names...)
			return outName, runToolChecked(ctx, srcDir, "zip", args...)
		}, nil
	}
	for _, bin := range []string{"7z", "7za"} {
		if toolAvailable(bin) {
			return sevenZipCreate(bin, "-tzip"), nil
		}
	}
	if tarAvailable() {
		if info := probeTar(ctx); info.flavor == tarBSD {
			return func(ctx context.Context, srcDir string, names []string, workDir, outName string) (string, error) {
				args := append([]string{"-c", "--format", "zip", "-f", filepath.Join(workDir, outName), "-C", srcDir, "--"}, tarMemberArgs(info, names)...)
				return outName, runToolChecked(ctx, workDir, "tar", args...)
			}, nil
		}
	}
	return nil, errors.New("multiarc: creating a .zip archive needs zip, 7z, 7za or bsdtar on PATH")
}

// sevenZipCreate runs "7z a" in srcDir, so every name is stored under its
// path relative to the panel's directory.
func sevenZipCreate(bin, typeSwitch string) createBuild {
	return func(ctx context.Context, srcDir string, names []string, workDir, outName string) (string, error) {
		return outName, sevenZipNames(names, func(switches, tail []string) error {
			args := append([]string{"a"}, sevenZipNameSwitches(names, append([]string{typeSwitch, "-y"}, switches...)...)...)
			return runToolChecked(ctx, srcDir, bin, append(append(args, filepath.Join(workDir, outName)), tail...)...)
		})
	}
}

// tarCreate runs "tar -c" with an optional compression option.
func tarCreate(info tarInfo, flag string) createBuild {
	return func(ctx context.Context, srcDir string, names []string, workDir, outName string) (string, error) {
		args := []string{"-c"}
		if flag != "" {
			args = append(args, flag)
		}
		args = append(args, "-f", filepath.Join(workDir, outName), "-C", srcDir, "--")
		return outName, runToolChecked(ctx, workDir, "tar", append(args, tarMemberArgs(info, names)...)...)
	}
}

// tarThenCompress writes a plain tar and compresses it with the
// stand-alone tool, which leaves "work.tar" plus the tool's extension.
func tarThenCompress(info tarInfo, comp tarCompression) createBuild {
	return func(ctx context.Context, srcDir string, names []string, workDir, _ string) (string, error) {
		const tarName = "work.tar"
		if _, err := tarCreate(info, "")(ctx, srcDir, names, workDir, tarName); err != nil {
			return "", err
		}
		if err := runToolChecked(ctx, workDir, comp.tool, "-f", tarName); err != nil {
			return "", err
		}
		return tarName + comp.ext, nil
	}
}

// availableCreateSuffixes lists the createSuffixes the tools on PATH can
// make right now.
func availableCreateSuffixes(ctx context.Context) []string {
	var out []string
	for _, suffix := range createSuffixes {
		if _, err := planCreate(ctx, "archive"+suffix); err == nil {
			out = append(out, suffix)
		}
	}
	return out
}

func unknownCreateFormat(ctx context.Context, name string) error {
	available := availableCreateSuffixes(ctx)
	if len(available) == 0 {
		return errNoCreator
	}
	return fmt.Errorf("multiarc: cannot tell which archive to create from %q; end the name with one of %s", name, strings.Join(available, ", "))
}

// defaultCreateSuffix is the suffix Add to archive suggests: the first of
// createSuffixes the tools on PATH can make. With none on PATH it still
// suggests .zip, the regular build's choice, and the attempt then explains
// what is missing.
func defaultCreateSuffix(ctx context.Context) string {
	if available := availableCreateSuffixes(ctx); len(available) > 0 {
		return available[0]
	}
	return ".zip"
}

// createArchive builds a new archive at target from names, relative to
// srcDir. It is built in a work directory next to target and only renamed
// onto it once complete, so a failed or canceled attempt leaves no partial
// archive behind -- and an existing target, which the caller has already
// had the user agree to overwrite, is left as it was.
func createArchive(ctx context.Context, srcDir string, names []string, target string) error {
	outName := filepath.Base(target)
	build, err := planCreate(ctx, outName)
	if err != nil {
		return err
	}
	for _, name := range names {
		inside := filepath.Join(srcDir, name) + string(filepath.Separator)
		if strings.HasPrefix(target, inside) {
			return fmt.Errorf("multiarc: %s cannot be created inside %s, which is being archived", outName, name)
		}
	}
	workDir, err := workDirNextTo(target)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(workDir) }() // Scratch; the archive was moved out of it.
	built, err := build(ctx, srcDir, names, workDir, outName)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(workDir, built), target); err != nil {
		return fmt.Errorf("multiarc: cannot move the new archive to %s: %w", target, err)
	}
	return nil
}
