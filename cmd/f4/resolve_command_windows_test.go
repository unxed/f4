//go:build windows

package main

import "testing"

func TestFindCmdTokenRejectsUnmatchedQuote(t *testing.T) {
	for _, command := range []string{`"`, `"program`} {
		if _, _, _, ok := findCmdToken(command); ok {
			t.Fatalf("findCmdToken(%q) accepted an unmatched quote", command)
		}
	}
	if _, _, name, ok := findCmdToken(`'`); !ok || name != `'` {
		t.Fatalf("findCmdToken did not treat a Windows single quote as a literal token: name=%q ok=%v", name, ok)
	}
}

func TestIsBatchCommand(t *testing.T) {
	cases := map[string]bool{
		`foo.bat`:                true,
		`foo.cmd`:                true,
		`FOO.BAT`:                true,
		`foo.Bat`:                true,
		`"my script.bat"`:        true,
		`"my script.cmd" arg1`:   true,
		`foo.exe`:                false,
		`foo.com`:                false,
		`dir`:                    false,
		`foo.bat arg1 & bar.exe`: true,
		``:                       false,
	}
	for cmd, want := range cases {
		if got := isBatchCommand(cmd); got != want {
			t.Errorf("isBatchCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}
