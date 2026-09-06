package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
)

func compareTestVFS(t *testing.T, dir string) vfs.VFS {
	t.Helper()
	v := vfs.NewOSVFS(dir)
	if err := v.SetPath(dir); err != nil {
		t.Fatalf("set path %q: %v", dir, err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func compareWriteFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	// A fixed timestamp keeps the metadata comparison out of the way of
	// tests that are about something else.
	stamp := time.Date(2024, 5, 4, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(full, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return full
}

func compareMarkNames(marks map[string]bool) []string {
	names := make([]string, 0, len(marks))
	for name := range marks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func compareRunSides(t *testing.T, left, right string, opts compareOptions) *compareOutcome {
	t.Helper()
	leftVFS := compareTestVFS(t, left)
	rightVFS := compareTestVFS(t, right)
	ctx := context.Background()
	leftItems, err := collectCompareSide(ctx, leftVFS, left, nil, opts, nil)
	if err != nil {
		t.Fatalf("scan left: %v", err)
	}
	rightItems, err := collectCompareSide(ctx, rightVFS, right, nil, opts, nil)
	if err != nil {
		t.Fatalf("scan right: %v", err)
	}
	outcome, err := compareSides(ctx, leftVFS, rightVFS, leftItems, rightItems, opts, nil)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	return outcome
}

func TestCompareTimesEqualHonoursSlackAndZones(t *testing.T) {
	base := time.Date(2024, 5, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		other time.Time
		opts  compareOptions
		equal bool
	}{
		{"exact", base, compareOptions{}, true},
		{"one second without slack", base.Add(time.Second), compareOptions{}, false},
		{"one second with slack", base.Add(time.Second), compareOptions{TimeSlack: true}, true},
		{"three seconds with slack", base.Add(3 * time.Second), compareOptions{TimeSlack: true}, false},
		{"whole hour without the option", base.Add(time.Hour), compareOptions{TimeSlack: true}, false},
		{"whole hour as a zone", base.Add(time.Hour), compareOptions{IgnoreZones: true}, true},
		{"nepal offset as a zone", base.Add(5*time.Hour + 45*time.Minute), compareOptions{IgnoreZones: true}, true},
		{"zone plus a second", base.Add(time.Hour + time.Second), compareOptions{IgnoreZones: true}, false},
		{"zone plus a second with slack", base.Add(time.Hour + time.Second), compareOptions{IgnoreZones: true, TimeSlack: true}, true},
		{"half a minute is not a zone", base.Add(30 * time.Second), compareOptions{IgnoreZones: true, TimeSlack: true}, false},
		{"a year apart is not a zone", base.AddDate(1, 0, 0), compareOptions{IgnoreZones: true, TimeSlack: true}, false},
		{"two days apart is not a zone", base.Add(48 * time.Hour), compareOptions{IgnoreZones: true, TimeSlack: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareTimesEqual(base, tc.other, tc.opts); got != tc.equal {
				t.Fatalf("compareTimesEqual = %v, want %v", got, tc.equal)
			}
			if got := compareTimesEqual(tc.other, base, tc.opts); got != tc.equal {
				t.Fatalf("compareTimesEqual is not symmetric: %v", got)
			}
		})
	}
}

func TestCompareMetadataReportsTheNewerSide(t *testing.T) {
	older := vfs.VFSItem{Size: 10, MTime: time.Date(2024, 5, 4, 12, 0, 0, 0, time.UTC)}
	newer := vfs.VFSItem{Size: 10, MTime: older.MTime.Add(time.Hour)}
	opts := compareOptions{ByTime: true, BySize: true}

	differs, timeOnly, side := compareMetadata(newer, older, opts)
	if !differs || !timeOnly || side != 1 {
		t.Fatalf("left newer: differs=%v timeOnly=%v side=%d", differs, timeOnly, side)
	}
	differs, timeOnly, side = compareMetadata(older, newer, opts)
	if !differs || !timeOnly || side != -1 {
		t.Fatalf("right newer: differs=%v timeOnly=%v side=%d", differs, timeOnly, side)
	}

	// A size difference is not about direction: neither side is the one
	// worth copying over, so both have to be marked.
	bigger := vfs.VFSItem{Size: 20, MTime: newer.MTime}
	differs, timeOnly, _ = compareMetadata(bigger, newer, opts)
	if !differs || timeOnly {
		t.Fatalf("size difference: differs=%v timeOnly=%v", differs, timeOnly)
	}

	// A criterion that is switched off does not speak.
	if differs, _, _ := compareMetadata(bigger, newer, compareOptions{ByTime: true}); differs {
		t.Fatal("size difference reported while comparing by time only")
	}
}

func TestCompareSidesMarksMissingAndNewerFiles(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	compareWriteFile(t, left, "same.txt", "hello")
	compareWriteFile(t, right, "same.txt", "hello")
	compareWriteFile(t, left, "only-left.txt", "x")
	compareWriteFile(t, right, "only-right.txt", "y")
	newer := compareWriteFile(t, left, "newer.txt", "v2")
	compareWriteFile(t, right, "newer.txt", "v1")
	stamp := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(newer, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	opts := defaultCompareOptions()
	opts.BySize = false // isolate the time rule from the size one
	outcome := compareRunSides(t, left, right, opts)

	if got, want := compareMarkNames(outcome.left), []string{"newer.txt", "only-left.txt"}; !equalStrings(got, want) {
		t.Fatalf("left marks = %v, want %v", got, want)
	}
	if got, want := compareMarkNames(outcome.right), []string{"only-right.txt"}; !equalStrings(got, want) {
		t.Fatalf("right marks = %v, want %v", got, want)
	}
	if outcome.differing != 3 {
		t.Fatalf("differing = %d, want 3", outcome.differing)
	}
}

func TestCompareSidesMarksTheTopLevelFolderOfADeepDifference(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	compareWriteFile(t, left, filepath.Join("tree", "nested", "file.txt"), "one")
	compareWriteFile(t, right, filepath.Join("tree", "nested", "file.txt"), "two")

	opts := defaultCompareOptions()
	opts.ByTime, opts.BySize = false, false
	opts.ByContent = true
	outcome := compareRunSides(t, left, right, opts)

	if got, want := compareMarkNames(outcome.left), []string{"tree"}; !equalStrings(got, want) {
		t.Fatalf("left marks = %v, want %v", got, want)
	}
	if got, want := compareMarkNames(outcome.right), []string{"tree"}; !equalStrings(got, want) {
		t.Fatalf("right marks = %v, want %v", got, want)
	}
}

func TestCompareSidesWithoutRecursionIgnoresFolders(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	compareWriteFile(t, left, filepath.Join("tree", "file.txt"), "one")
	compareWriteFile(t, right, filepath.Join("tree", "file.txt"), "two")
	compareWriteFile(t, left, "top.txt", "same")
	compareWriteFile(t, right, "top.txt", "same")

	opts := defaultCompareOptions()
	opts.Recursive = false
	outcome := compareRunSides(t, left, right, opts)

	if len(outcome.left) != 0 || len(outcome.right) != 0 {
		t.Fatalf("marks without recursion: left=%v right=%v",
			compareMarkNames(outcome.left), compareMarkNames(outcome.right))
	}
}

func TestCompareSidesRespectsTheDepthLimit(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	compareWriteFile(t, left, filepath.Join("a", "b", "deep.txt"), "one")
	compareWriteFile(t, right, filepath.Join("a", "b", "deep.txt"), "two")

	opts := defaultCompareOptions()
	opts.ByTime, opts.BySize = false, false
	opts.ByContent = true
	opts.LimitDepth, opts.MaxDepth = true, 1

	if outcome := compareRunSides(t, left, right, opts); len(outcome.left) != 0 {
		t.Fatalf("depth 1 reached into a/b: %v", compareMarkNames(outcome.left))
	}
	opts.MaxDepth = 2
	if outcome := compareRunSides(t, left, right, opts); len(outcome.left) != 1 {
		t.Fatalf("depth 2 missed a/b: %v", compareMarkNames(outcome.left))
	}
}

func TestCompareSidesByContentIgnoringLineEndings(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	compareWriteFile(t, left, "text.txt", "alpha\r\nbeta\r\n")
	compareWriteFile(t, right, "text.txt", "alpha\nbeta\n")
	compareWriteFile(t, left, "spaced.txt", "a b\tc")
	compareWriteFile(t, right, "spaced.txt", "abc")

	opts := defaultCompareOptions()
	opts.ByTime, opts.BySize = false, false
	opts.ByContent = true

	outcome := compareRunSides(t, left, right, opts)
	if got, want := compareMarkNames(outcome.left), []string{"spaced.txt", "text.txt"}; !equalStrings(got, want) {
		t.Fatalf("plain byte comparison marked %v, want %v", got, want)
	}

	opts.Ignore, opts.IgnoreMode = true, compareIgnoreEOL
	outcome = compareRunSides(t, left, right, opts)
	if got, want := compareMarkNames(outcome.left), []string{"spaced.txt"}; !equalStrings(got, want) {
		t.Fatalf("ignoring line endings marked %v, want %v", got, want)
	}

	opts.IgnoreMode = compareIgnoreSpaces
	outcome = compareRunSides(t, left, right, opts)
	if len(outcome.left) != 0 {
		t.Fatalf("ignoring whitespace still marked %v", compareMarkNames(outcome.left))
	}
}

// A carriage return landing on a chunk boundary must still pair up with
// the newline that opens the next chunk.
func TestCompareStreamsCollapsesLineEndingsAcrossChunks(t *testing.T) {
	crlf := make([]byte, 0, compareChunkSize+2)
	lf := make([]byte, 0, compareChunkSize+2)
	for len(crlf) < compareChunkSize-1 {
		crlf = append(crlf, 'a')
		lf = append(lf, 'a')
	}
	crlf = append(crlf, '\r', '\n', 'b')
	lf = append(lf, '\n', 'b')

	ctx := context.Background()
	equal, err := compareStreams(
		newCompareStream(ctx, &vfs.MemoryReadAtCloser{Data: crlf}, compareIgnoreEOL),
		newCompareStream(ctx, &vfs.MemoryReadAtCloser{Data: lf}, compareIgnoreEOL),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatal("a CRLF split across two reads was not collapsed")
	}
}

func TestCompareStreamsStopsAtTheFirstDifference(t *testing.T) {
	ctx := context.Background()
	equal, err := compareStreams(
		newCompareStream(ctx, &vfs.MemoryReadAtCloser{Data: []byte("abc")}, -1),
		newCompareStream(ctx, &vfs.MemoryReadAtCloser{Data: []byte("abd")}, -1),
	)
	if err != nil || equal {
		t.Fatalf("equal=%v err=%v", equal, err)
	}
	// A prefix is not a match either.
	equal, err = compareStreams(
		newCompareStream(ctx, &vfs.MemoryReadAtCloser{Data: []byte("abc")}, -1),
		newCompareStream(ctx, &vfs.MemoryReadAtCloser{Data: []byte("abcd")}, -1),
	)
	if err != nil || equal {
		t.Fatalf("prefix: equal=%v err=%v", equal, err)
	}
}

func TestCollectCompareSideHonoursTheMarkedScope(t *testing.T) {
	dir := t.TempDir()
	compareWriteFile(t, dir, "kept.txt", "x")
	compareWriteFile(t, dir, "skipped.txt", "y")
	compareWriteFile(t, dir, filepath.Join("kept-dir", "inner.txt"), "z")
	compareWriteFile(t, dir, filepath.Join("skipped-dir", "inner.txt"), "z")

	items, err := collectCompareSide(context.Background(), compareTestVFS(t, dir), dir,
		map[string]bool{"kept.txt": true, "kept-dir": true}, defaultCompareOptions(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kept-dir", "kept-dir/inner.txt", "kept.txt"}
	got := make([]string, 0, len(items))
	for rel := range items {
		got = append(got, rel)
	}
	sort.Strings(got)
	if !equalStrings(got, want) {
		t.Fatalf("collected %v, want %v", got, want)
	}
	if items["kept-dir/inner.txt"].top != "kept-dir" {
		t.Fatalf("nested item points at %q", items["kept-dir/inner.txt"].top)
	}
}

func TestCompareOptionsDefaultsWhenTheSectionIsAbsent(t *testing.T) {
	opts := loadCompareOptions(&IniFile{data: make(map[string]map[string]string)})
	if opts != defaultCompareOptions() {
		t.Fatalf("empty config gave %+v", opts)
	}
	if !opts.hasCriteria() {
		t.Fatal("the default comparison has nothing to compare by")
	}
}

func TestCompareOptionsNormalizeClampsHandEditedValues(t *testing.T) {
	opts := compareOptions{MaxDepth: 0, IgnoreMode: 42}.normalize()
	if opts.MaxDepth != 1 || opts.IgnoreMode != compareIgnoreEOL {
		t.Fatalf("normalize gave %+v", opts)
	}
	if opts := (compareOptions{MaxDepth: 1000}).normalize(); opts.MaxDepth != compareMaxDepthLimit {
		t.Fatalf("depth was not clamped: %d", opts.MaxDepth)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
