package panel

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtui"
)

func TestDriveMenuPlatformRowsKeepSolidSeparatorsForHiddenParameters(t *testing.T) {
	options := uint32(config.DriveMenuShowLabel | config.DriveMenuShowFilesystem)
	rows := []DriveMenuPlatformRow{
		{Base: "E:"}, // removable drive whose parameters are hidden
		{Base: "C:", Label: "System", Filesystem: "NTFS"},
	}

	texts := DriveMenuPlatformRowsText(rows, options)
	shown := make([]string, len(texts))
	for i, text := range texts {
		shown[i], _, _ = vtui.ParseAmpersandString(text)
	}
	for i, row := range shown {
		if got := strings.Count(row, "│"); got != 2 {
			t.Fatalf("row %d has %d column separators, want 2: %q", i, got, row)
		}
	}
	for column := 0; column < 2; column++ {
		want := strings.Index(shown[1], "│")
		if column == 1 {
			want = strings.Index(shown[1][want+len("│"):], "│") + want + len("│")
		}
		got := strings.Index(shown[0], "│")
		if column == 1 {
			got = strings.Index(shown[0][got+len("│"):], "│") + got + len("│")
		}
		if got != want {
			t.Fatalf("column %d separator drifted: %q / %q", column, shown[0], shown[1])
		}
	}
}
