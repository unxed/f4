package terminal

import (
	"reflect"
	"testing"
)

func TestHistoryQuietArgsOnlyForAnInteractiveZsh(t *testing.T) {
	want := []string{"-o", "HIST_IGNORE_SPACE"}
	for _, tc := range []struct {
		name string
		args []string
		out  []string
	}{
		{"/bin/zsh", nil, want},
		{"/usr/bin/zsh", []string{"-l"}, append(append([]string{}, want...), "-l")},
		{"zsh", []string{"-c", "ls"}, []string{"-c", "ls"}},
		{"zsh", []string{"script.zsh"}, []string{"script.zsh"}},
		{"/bin/bash", nil, nil},
		{"/usr/bin/fish", nil, nil},
		{"/usr/bin/vim", []string{"x"}, []string{"x"}},
	} {
		got := historyQuietArgs(tc.name, tc.args)
		if len(got) == 0 && len(tc.out) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tc.out) {
			t.Errorf("historyQuietArgs(%q, %v) = %v, want %v", tc.name, tc.args, got, tc.out)
		}
	}
}

func TestHistoryQuietEnvAddsIgnorespaceToBashOnly(t *testing.T) {
	base := []string{"HOME=/h", "TERM=xterm"}
	if got := historyQuietEnv("/bin/bash", nil, base); !reflect.DeepEqual(got, append(append([]string{}, base...), "HISTCONTROL=ignorespace")) {
		t.Errorf("no HISTCONTROL: got %v", got)
	}
	if got := historyQuietEnv("bash", nil, []string{"HISTCONTROL=erasedups"}); !reflect.DeepEqual(got, []string{"HISTCONTROL=erasedups:ignorespace"}) {
		t.Errorf("HISTCONTROL=erasedups: got %v", got)
	}
	for _, kept := range []string{"HISTCONTROL=ignoreboth", "HISTCONTROL=ignorespace:erasedups", "HISTCONTROL=erasedups:ignorespace"} {
		if got := historyQuietEnv("bash", nil, []string{kept}); !reflect.DeepEqual(got, []string{kept}) {
			t.Errorf("%s was changed to %v", kept, got)
		}
	}
	if got := historyQuietEnv("zsh", nil, base); !reflect.DeepEqual(got, base) {
		t.Errorf("zsh env changed: %v", got)
	}
	if got := historyQuietEnv("bash", []string{"-c", "x"}, base); !reflect.DeepEqual(got, base) {
		t.Errorf("a bash -c run got the history setting: %v", got)
	}
	// The input is never modified.
	in := []string{"HISTCONTROL=erasedups"}
	_ = historyQuietEnv("bash", nil, in)
	if in[0] != "HISTCONTROL=erasedups" {
		t.Errorf("input slice was modified: %v", in)
	}
}
