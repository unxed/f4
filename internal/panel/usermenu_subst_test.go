package panel

import "testing"

func substCtx() *SubstContext {
	return &SubstContext{
		Active:  PanelSnapshot{CurDir: "/home/user/docs", CurrentFile: "report.txt"},
		Passive: PanelSnapshot{CurDir: "/tmp/dest", CurrentFile: "other.txt"},
	}
}

func TestSubstFileName_Tokens(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"filename", "cat !.!", "cat report.txt"},
		{"active dir bare, no closing bang", "ls -la !/ > dir.txt", "ls -la /home/user/docs > dir.txt"},
		{"active dir bare at end of command", "cd !/", "cd /home/user/docs"},
		{"active dir closed form unchanged", "cd !/!", "cd /home/user/docs"},
		{"backslash bare form", "cd !\\", "cd /home/user/docs"},
		{"backslash closed form unchanged", "cd !\\!", "cd /home/user/docs"},
		// f4 #1395: !# switches to the passive panel; the reporter's exact
		// template ("!#!/ ") needs the bare !/ form to work right after it.
		{"passive switch then bare dir (f4 #1395)", "ls -la !#!/ > dir.txt", "ls -la /tmp/dest > dir.txt"},
		{"literal bang", "echo !!", "echo !"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SubstFileName(tc.cmd, substCtx())
			if got.Command != tc.want {
				t.Errorf("SubstFileName(%q) = %q, want %q", tc.cmd, got.Command, tc.want)
			}
		})
	}
}

func TestSubstFileName_MarkedListQuotesNamesWithSpaces(t *testing.T) {
	cases := []struct {
		name   string
		cmd    string
		marked []string
		want   string
	}{
		{"plain names stay bare", "tar cf a.tar !&", []string{"a.txt", "b.txt"}, "tar cf a.tar a.txt b.txt"},
		{"names with spaces quoted", "tar cf a.tar !&", []string{"a.txt", "my file.txt", "x y z"}, `tar cf a.tar a.txt "my file.txt" "x y z"`},
		{"short names plain", "zip a.zip !&~", []string{"a.txt", "b.txt"}, "zip a.zip a.txt b.txt"},
		{"short names with spaces quoted", "zip a.zip !&~ end", []string{"a.txt", "my file.txt"}, `zip a.zip a.txt "my file.txt" end`},
		{"cursor file fallback quoted", "cat !&", nil, `cat "my report.txt"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := substCtx()
			ctx.Active.CurrentFile = "my report.txt"
			ctx.Active.Marked = tc.marked
			got := SubstFileName(tc.cmd, ctx)
			if got.Command != tc.want {
				t.Errorf("SubstFileName(%q) = %q, want %q", tc.cmd, got.Command, tc.want)
			}
			if !got.ListFiles {
				t.Errorf("ListFiles flag should be set")
			}
		})
	}
}

func TestSubstFileName_DirectoryThenFilenameWindowsPaths(t *testing.T) {
	ctx := &SubstContext{
		Active:  PanelSnapshot{CurDir: `D:\Tes`, CurrentFile: "test_merge1_a.cpp"},
		Passive: PanelSnapshot{CurDir: `D:\Test`, CurrentFile: "test_merge1_b.cpp"},
	}
	got := SubstFileName(`echo meld.exe "!\!.!" "!#!\!.!"`, ctx)
	want := `echo meld.exe "D:\Tes\test_merge1_a.cpp" "D:\Test\test_merge1_b.cpp"`
	if got.Command != want {
		t.Fatalf("SubstFileName() = %q, want %q", got.Command, want)
	}
}

func TestTrimTrailingSlash(t *testing.T) {
	cases := map[string]string{
		"/mnt/data/": "/mnt/data",
		"/mnt/data":  "/mnt/data",
		"/":          "/",
		`C:\`:        `C:`,
	}
	for in, want := range cases {
		if got := trimTrailingSlash(in); got != want {
			t.Errorf("trimTrailingSlash(%q) = %q, want %q", in, got, want)
		}
	}
}
