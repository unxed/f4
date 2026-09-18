package app

import (
	"testing"

	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtui"
)

func TestParseReadAttemptsPositive(t *testing.T) {
	if got := parseReadAttempts("7"); got != 7 {
		t.Fatalf("parseReadAttempts(7) = %d, want 7", got)
	}
}

func TestParseReadAttemptsTrimsWhitespace(t *testing.T) {
	if got := parseReadAttempts("  4 "); got != 4 {
		t.Fatalf("parseReadAttempts with spaces = %d, want 4", got)
	}
}

func TestParseReadAttemptsRejectsInvalidValues(t *testing.T) {
	for _, text := range []string{"", "nope", "0", "-3"} {
		if got := parseReadAttempts(text); got != 1 {
			t.Errorf("parseReadAttempts(%q) = %d, want 1", text, got)
		}
	}
}

func TestParseReadAttemptsClampsToMaximum(t *testing.T) {
	if got := parseReadAttempts("999999"); got != fileops.MaxReadAttempts {
		t.Fatalf("parseReadAttempts above maximum = %d, want %d", got, fileops.MaxReadAttempts)
	}
}

func TestCaptionCellsRemovesAmpersandMarkers(t *testing.T) {
	if got := captionCells("A &B && C"); got != vtui.StringWidth("A B  C") {
		t.Fatalf("captionCells with markers = %d, want %d", got, vtui.StringWidth("A B  C"))
	}
}

func TestCaptionCellsCountsWideRunes(t *testing.T) {
	if got := captionCells("界面"); got != 4 {
		t.Fatalf("captionCells wide text = %d, want 4", got)
	}
}

func TestNewChoiceComboKeepsValidSelection(t *testing.T) {
	combo := newChoiceCombo([]string{"one", "two"}, 1)
	if combo == nil || !combo.DropdownOnly || combo.Menu.SelectPos != 1 {
		t.Fatalf("valid selection produced %#v, want dropdown at row 1", combo)
	}
}

func TestNewChoiceComboNormalizesInvalidSelection(t *testing.T) {
	combo := newChoiceCombo([]string{"one", "two"}, -1)
	if combo == nil || combo.Menu.SelectPos != 0 {
		t.Fatalf("invalid selection produced %#v, want row 0", combo)
	}
}

func TestShowCopyAdvancedOptionsAcceptsDefaults(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(100, 40)
	vtui.FrameManager.Init(scr)

	opts := fileops.FileOpOptions{}
	showCopyAdvancedOptions(&opts)
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("advanced options top frame = %T, want *vtui.Window", vtui.FrameManager.GetTopFrame())
	}
	testutil.ClickDialogButton(t, dlg, "Ok")
	if opts.ReadAttempts != 1 || opts.IgnoreReadErrors || opts.IgnoreWriteErrors {
		t.Fatalf("accepted defaults = %+v, want one read attempt and no ignored errors", opts)
	}
}

func TestShowCopyAdvancedOptionsCancelPreservesChoices(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(100, 40)
	vtui.FrameManager.Init(scr)

	opts := fileops.FileOpOptions{IgnoreReadErrors: true, IgnoreWriteErrors: true, ReadAttempts: 5}
	want := opts
	showCopyAdvancedOptions(&opts)
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("advanced options top frame = %T, want *vtui.Window", vtui.FrameManager.GetTopFrame())
	}
	testutil.ClickDialogButton(t, dlg, "Cancel")
	if opts != want {
		t.Fatalf("cancel changed options to %+v, want %+v", opts, want)
	}
}
