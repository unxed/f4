package panel

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type applyCoverageRunner struct {
	vfs.VFS
	available bool
	info      vfs.CommandRunnerInfo
}

func (*applyCoverageRunner) RunCommand(context.Context, string, string, func(string)) (int, error) {
	return 0, nil
}

func (r *applyCoverageRunner) CommandRunnerAvailable() bool             { return r.available }
func (r *applyCoverageRunner) CommandRunnerInfo() vfs.CommandRunnerInfo { return r.info }

type applyCoveragePathVFS struct {
	vfs.VFS
	joined string
}

func (v *applyCoveragePathVFS) Join(...string) string { return v.joined }

func TestApplyCoverageNormalizesRunnerMetadataAndDialects(t *testing.T) {
	base := vfs.NewNullVFS(0)
	if _, _, ok := ResolveApplyCommandRunner(nil); ok {
		t.Fatal("nil VFS unexpectedly resolved to a command runner")
	}
	unavailable := &applyCoverageRunner{VFS: base}
	if _, _, ok := ResolveApplyCommandRunner(unavailable); ok {
		t.Fatal("unavailable command runner was accepted")
	}
	runner := &applyCoverageRunner{
		VFS:       base,
		available: true,
		info:      vfs.CommandRunnerInfo{Dialect: vfs.CommandDialect(99), MaxParallel: -4},
	}
	_, info, ok := ResolveApplyCommandRunner(runner)
	if !ok || info.Dialect != vfs.CommandDialectUnknown || info.MaxParallel != 1 {
		t.Fatalf("normalized runner = (%+v, %v), want unknown/1/true", info, ok)
	}

	for _, tc := range []struct {
		from vfs.CommandDialect
		want cmdline.ApplyCommandDialect
	}{
		{vfs.CommandDialectPOSIX, cmdline.ApplyCommandDialectPOSIX},
		{vfs.CommandDialectCmd, cmdline.ApplyCommandDialectCMD},
		{vfs.CommandDialectPowerShell, cmdline.ApplyCommandDialectPowerShell},
		{vfs.CommandDialectUnknown, cmdline.ApplyCommandDialectRaw},
	} {
		if got := applyCommandDialect(tc.from); got != tc.want {
			t.Errorf("applyCommandDialect(%d) = %d, want %d", tc.from, got, tc.want)
		}
	}
}

func TestApplyCoverageCapturesPanelSnapshotAndPathStyle(t *testing.T) {
	dir := t.TempDir()
	fsp := &FileSystemPanel{
		Vfs:   vfs.NewOSVFS(dir),
		Table: vtui.NewTable(0, 0, 20, 10, nil),
		Entries: []*FileEntry{
			{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
			{VFSItem: vfs.VFSItem{Name: "one.txt"}},
			{VFSItem: vfs.VFSItem{Name: "two.txt"}},
		},
	}
	fsp.SetCursorIndex(2)
	capture := captureApplyCommandPanel(fsp, []string{"one.txt", "..", "two.txt"})
	if capture.Panel != fsp || capture.Dir != dir || capture.Snapshot.PathStyle != cmdline.ApplyCommandPathStylePOSIX {
		t.Fatalf("capture identity/path = (%p, %q, %d)", capture.Panel, capture.Dir, capture.Snapshot.PathStyle)
	}
	if capture.Snapshot.Current.Name != "two.txt" || capture.Snapshot.Current.ShortName != "two.txt" {
		t.Fatalf("current snapshot = %+v", capture.Snapshot.Current)
	}
	if len(capture.Snapshot.Selected) != 2 || capture.Snapshot.Selected[0].Name != "one.txt" || capture.Snapshot.Selected[1].Name != "two.txt" {
		t.Fatalf("selected snapshot = %#v", capture.Snapshot.Selected)
	}
	if got := applyCommandFileForTarget(capture, "one.txt"); got.ShortName != "one.txt" {
		t.Fatalf("selected target = %+v", got)
	}
	if got := applyCommandFileForTarget(capture, "missing.txt"); got.Name != "missing.txt" || got.ShortName != "missing.txt" {
		t.Fatalf("fallback target = %+v", got)
	}

	for _, tc := range []struct {
		joined string
		want   cmdline.ApplyCommandPathStyle
	}{
		{joined: `f4-apply-style-a\\f4-apply-style-b`, want: cmdline.ApplyCommandPathStyleWindows},
		{joined: "f4-apply-style-a/f4-apply-style-b", want: cmdline.ApplyCommandPathStylePOSIX},
	} {
		pathVFS := &applyCoveragePathVFS{VFS: vfs.NewNullVFS(0), joined: tc.joined}
		if got := detectApplyCommandPathStyle(pathVFS, filepath.Join(dir, "sub")); got != tc.want {
			t.Errorf("detectApplyCommandPathStyle(%q) = %d, want %d", tc.joined, got, tc.want)
		}
	}
}
