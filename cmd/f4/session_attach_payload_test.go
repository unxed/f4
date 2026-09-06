//go:build !windows

package main

import "testing"

func TestAttachPayloadRoundTrip(t *testing.T) {
	cases := []struct {
		name                  string
		editPath, left, right string
		wantWire              string
		wantEdit              string
		wantLeft, wantRight   string
	}{
		{name: "plain", wantWire: "ATTACH"},
		{
			name: "one directory", left: "/home/u/dir",
			wantWire: "ATTACH\nCWD /home/u/dir", wantLeft: "/home/u/dir",
		},
		{
			// Both panels going to the same place is what a plain start looks
			// like, and it stays a one-line datagram.
			name: "same on both sides", left: "/home/u/dir", right: "/home/u/dir",
			wantWire: "ATTACH\nCWD /home/u/dir", wantLeft: "/home/u/dir",
		},
		{
			name: "two directories", left: "/home/u/a", right: "/home/u/b",
			wantWire: "ATTACH\nCWD /home/u/a\nCWD2 /home/u/b",
			wantLeft: "/home/u/a", wantRight: "/home/u/b",
		},
		// -e keeps the single-line wire format an older daemon understands.
		{
			name: "edit", editPath: "/tmp/f.txt", left: "/home/u/a", right: "/home/u/b",
			wantWire: "ATTACH /tmp/f.txt", wantEdit: "/tmp/f.txt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire := string(attachPayload(tc.editPath, tc.left, tc.right))
			if wire != tc.wantWire {
				t.Fatalf("attachPayload = %q, want %q", wire, tc.wantWire)
			}
			edit, left, right := parseAttachPayload(wire)
			if edit != tc.wantEdit || left != tc.wantLeft || right != tc.wantRight {
				t.Fatalf("parseAttachPayload(%q) = (%q, %q, %q), want (%q, %q, %q)",
					wire, edit, left, right, tc.wantEdit, tc.wantLeft, tc.wantRight)
			}
		})
	}
}

// A datagram from a client that speaks of something this build knows nothing
// about must still attach, with the lines it does understand.
func TestParseAttachPayloadIgnoresUnknownLines(t *testing.T) {
	edit, left, right := parseAttachPayload("ATTACH\nCWD /home/u/a\nSOMETHING new\nCWD2 /home/u/b")
	if edit != "" || left != "/home/u/a" || right != "/home/u/b" {
		t.Fatalf("got (%q, %q, %q), want (\"\", \"/home/u/a\", \"/home/u/b\")", edit, left, right)
	}
}
