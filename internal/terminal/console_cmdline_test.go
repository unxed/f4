package terminal

import "testing"

// The command line RunOnHostConsole hands CreateProcessW has to parse into
// exactly the argv os/exec would have passed, or the fix for issue #513 would
// quietly change what the shell runs. These cases are the ones that differ
// between naive concatenation and the real rule.
func TestBuildShellCmdLine(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		shell, flag, command string
		want                 string
	}{
		{
			name:  "plain command needs no quoting",
			shell: `C:\ReactOS\System32\cmd.exe`, flag: "/c", command: "dir",
			want: `C:\ReactOS\System32\cmd.exe /c dir`,
		},
		{
			name:  "a space in the command quotes it whole",
			shell: `C:\ReactOS\System32\cmd.exe`, flag: "/c", command: `dir c:\reactos`,
			want: `C:\ReactOS\System32\cmd.exe /c "dir c:\reactos"`,
		},
		{
			name:  "a space in the shell path quotes the shell",
			shell: `C:\Program Files\cmd.exe`, flag: "/c", command: "dir",
			want: `"C:\Program Files\cmd.exe" /c dir`,
		},
		{
			name:  "embedded quotes are escaped",
			shell: `cmd.exe`, flag: "/c", command: `echo "hi there"`,
			want: `cmd.exe /c "echo \"hi there\""`,
		},
		{
			name:  "a trailing backslash before the closing quote is doubled",
			shell: `cmd.exe`, flag: "/c", command: `dir c:\some dir\`,
			want: `cmd.exe /c "dir c:\some dir\\"`,
		},
		{
			name:  "an empty command still becomes an argument",
			shell: `cmd.exe`, flag: "/c", command: "",
			want: `cmd.exe /c ""`,
		},
		{
			name:  "no flag leaves just shell and command",
			shell: `/bin/sh`, flag: "", command: "ls",
			want: `/bin/sh ls`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildShellCmdLine(tc.shell, tc.flag, tc.command); got != tc.want {
				t.Fatalf("BuildShellCmdLine(%q, %q, %q) = %q, want %q",
					tc.shell, tc.flag, tc.command, got, tc.want)
			}
		})
	}
}

// Off Windows the whole path must decline, so the caller falls back to
// os/exec and nothing about a Unix session changes.
func TestRunOnHostConsoleDeclinesWhenNotApplicable(t *testing.T) {
	t.Setenv(LegacyChildStdioEnvVar, "1")
	err := RunOnHostConsole("", "sh", "-c", "true")
	if err != ErrConsoleSpawnUnavailable {
		t.Fatalf("RunOnHostConsole with the escape hatch set = %v, want ErrConsoleSpawnUnavailable", err)
	}
}
