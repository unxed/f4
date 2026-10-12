package terminal

import (
	"testing"
	"unicode/utf16"
)

// f4#897 item 3: on XP a read of 80x200 cells does not fit the console's 64 KB
// shared heap. Every read must stay within maxConsoleReadCells.
func TestConsoleReadRows(t *testing.T) {
	for _, width := range []int{1, 80, 120, 250, 8000, 9000} {
		rows := ConsoleReadRows(width)
		if rows < 1 {
			t.Fatalf("ConsoleReadRows(%d) = %d, want at least 1", width, rows)
		}
		if rows > 1 && rows*width > maxConsoleReadCells {
			t.Fatalf("ConsoleReadRows(%d) = %d: %d cells per read, over %d",
				width, rows, rows*width, maxConsoleReadCells)
		}
	}
	if got := ConsoleReadRows(80); got*80*4 >= 80*198*4 {
		t.Fatalf("ConsoleReadRows(80) = %d rows: at or past the 80x198 cells XP cannot read at once", got)
	}
	if got := ConsoleReadRows(0); got != 1 {
		t.Fatalf("ConsoleReadRows(0) = %d, want 1", got)
	}
}

func TestConsoleRowText(t *testing.T) {
	row := func(s string, pad int, fill uint16) []uint16 {
		out := utf16.Encode([]rune(s))
		for i := 0; i < pad; i++ {
			out = append(out, fill)
		}
		return out
	}
	tests := []struct {
		name string
		in   []uint16
		want string
	}{
		{"trailing blanks", row("dir", 5, ' '), "dir"},
		{"a row a failed read left as NULs", row("", 80, 0), ""},
		{"NUL tail", row("C:\\>", 10, 0), "C:\\>"},
		{"NUL inside counts as a blank", []uint16{'a', 0, 'b'}, "a b"},
		{"Cyrillic", row("Том в устройстве", 3, ' '), "Том в устройстве"},
	}
	for _, tt := range tests {
		if got := consoleRowText(tt.in); got != tt.want {
			t.Errorf("%s: consoleRowText = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// The Ctrl+O snapshot of a large window is read and written in strips that
// each fit the console's shared heap on XP, and together cover every row once.
func TestConsoleStrips(t *testing.T) {
	for _, tc := range []struct{ w, h int }{{80, 25}, {240, 67}, {80, 300}, {1, 3}, {80, 0}} {
		next := 0
		for _, s := range ConsoleStrips(tc.w, tc.h) {
			if s.Row != next || s.Rows < 1 {
				t.Fatalf("%dx%d: strip %+v after row %d", tc.w, tc.h, s, next)
			}
			if s.Rows > 1 && s.Rows*tc.w > maxConsoleReadCells {
				t.Fatalf("%dx%d: strip %+v is %d cells, over %d", tc.w, tc.h, s, s.Rows*tc.w, maxConsoleReadCells)
			}
			next += s.Rows
		}
		if next != tc.h {
			t.Fatalf("%dx%d: strips cover %d rows", tc.w, tc.h, next)
		}
	}
	if n := len(ConsoleStrips(240, 67)); n < 2 {
		t.Fatalf("a 240x67 window (16080 cells, 64 KB) went in %d call(s)", n)
	}
}
