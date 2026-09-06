//go:build !windows

package main

import "testing"

func TestAttachPayloadRoundTrip(t *testing.T) {
	cases := []struct {
		name           string
		editPath, cwd  string
		wantWire       string
		wantEdit, want string
	}{
		{name: "plain", wantWire: "ATTACH"},
		{name: "cwd", cwd: "/home/u/dir", wantWire: "ATTACH\nCWD /home/u/dir", want: "/home/u/dir"},
		// -e keeps the single-line wire format an older daemon understands.
		{name: "edit", editPath: "/tmp/f.txt", cwd: "/home/u/dir", wantWire: "ATTACH /tmp/f.txt", wantEdit: "/tmp/f.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire := string(attachPayload(tc.editPath, tc.cwd))
			if wire != tc.wantWire {
				t.Fatalf("attachPayload = %q, want %q", wire, tc.wantWire)
			}
			edit, cwd := parseAttachPayload(wire)
			if edit != tc.wantEdit || cwd != tc.want {
				t.Fatalf("parseAttachPayload(%q) = (%q, %q), want (%q, %q)", wire, edit, cwd, tc.wantEdit, tc.want)
			}
		})
	}
}
