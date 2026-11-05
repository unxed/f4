package viewer

import (
	"bytes"
	"testing"
)

// The unwrapped tail layout hands one line at a time to the row layout: a
// window of many short lines used to be handed over whole for every row, which
// made opening a large terminal log at its end take minutes (unxed/f4#2268).
func TestTailRowDataUnwrappedIsOneLine(t *testing.T) {
	window := bytes.Repeat([]byte("\x1b[32mshort\x1b[0m\n"), 5000)
	first := []byte("\x1b[32mshort\x1b[0m\n")

	got := tailRowData(window, false, 80)
	if !bytes.Equal(got, first) {
		t.Fatalf("unwrapped row = %q, want the first line %q", got, first)
	}

	rest := window
	for rows := 0; len(rest) > 0; rows++ {
		row := tailRowData(rest, false, 80)
		if len(row) == 0 || len(row) > len(first) {
			t.Fatalf("row %d is %d bytes, want 1..%d", rows, len(row), len(first))
		}
		rest = rest[len(row):]
	}
}

func TestTailRowDataUnwrappedKeepsLineWithoutNewline(t *testing.T) {
	long := bytes.Repeat([]byte("x"), 4096)
	if got := tailRowData(long, false, 80); !bytes.Equal(got, long) {
		t.Fatalf("a line without a newline was cut to %d bytes of %d", len(got), len(long))
	}
	if got := tailRowData(nil, false, 80); len(got) != 0 {
		t.Fatalf("empty data gave %d bytes", len(got))
	}
}

func TestTailRowDataWrappedIsBoundedByRowRead(t *testing.T) {
	window := bytes.Repeat([]byte("a"), 1000)
	if got := tailRowData(window, true, 128); len(got) != 128 {
		t.Fatalf("wrapped row is %d bytes, want 128", len(got))
	}
	short := []byte("abc")
	if got := tailRowData(short, true, 128); !bytes.Equal(got, short) {
		t.Fatalf("wrapped short data = %q, want %q", got, short)
	}
}
