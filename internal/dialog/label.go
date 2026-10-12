package dialog

import (
	"github.com/mattn/go-runewidth"
	"strings"
)

// PadLabel pads a dialog label to twelve columns so the fields beside a column
// of labels line up. Width is counted in display cells, not bytes: a label
// translated into a wide script pads to the same visual column.
func PadLabel(s string) string {
	return PadLabelTo(s, 12)
}

// PadLabelTo gives a label enough display cells for subsequent text updates.
func PadLabelTo(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-runewidth.StringWidth(s)))
}
