package app

import (
	"testing"

	"github.com/unxed/f4/internal/i18n"
)

func TestCopyMovePromptText_NamesASingleItem(t *testing.T) {
	i18n.InitLang("en", "", "")
	t.Cleanup(func() { i18n.InitLang("en", "", "") })

	cases := []struct {
		move  bool
		names []string
		want  string
	}{
		{false, []string{"report.txt"}, `Copy "report.txt" to:`},
		{true, []string{"photos"}, `Rename or move "photos" to:`},
		{false, []string{"a", "b"}, "Copy 2 item(s) to:"},
		{true, []string{"a", "b", "c"}, "Rename or move 3 item(s) to:"},
	}
	for _, c := range cases {
		if got := copyMovePromptText(c.move, c.names); got != c.want {
			t.Errorf("copyMovePromptText(%v, %v) = %q, want %q", c.move, c.names, got, c.want)
		}
	}
}

func TestCopyMovePromptText_ShortensALongName(t *testing.T) {
	i18n.InitLang("en", "", "")
	long := "a-very-long-file-name-that-would-push-the-dialog-wider-than-the-screen-allows.txt"
	got := copyMovePromptText(false, []string{long})
	if len([]rune(got)) > len([]rune(`Copy "" to:`))+40 {
		t.Errorf("prompt for a long name is %d runes, the name must be cut to 40: %q", len([]rune(got)), got)
	}
}
