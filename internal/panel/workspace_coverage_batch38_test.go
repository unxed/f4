package panel

import (
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/ini"
)

func batch38Workspace(number int) WorkspaceSessionState {
	return WorkspaceSessionState{Number: number, ActivePanel: number, Left: PanelSessionState{Path: "left"}, Right: PanelSessionState{Path: "right"}}
}

func TestWorkspaceSessionsForRestoreKeepsAllWhenEnabledCoverageBatch38(t *testing.T) {
	states := []WorkspaceSessionState{batch38Workspace(1), batch38Workspace(2)}
	got, active := WorkspaceSessionsForRestore(states, 1, true)
	if !reflect.DeepEqual(got, states) || active != 1 {
		t.Fatalf("restore tabs enabled = %#v, %d; want all states and active 1", got, active)
	}
}

func TestWorkspaceSessionsForRestoreHandlesEmptyInputCoverageBatch38(t *testing.T) {
	if got, active := WorkspaceSessionsForRestore(nil, 7, false); got != nil || active != 7 {
		t.Fatalf("empty restore = %#v, %d; want nil and original active", got, active)
	}
}

func TestWorkspaceSessionsForRestoreNormalizesNegativeActiveCoverageBatch38(t *testing.T) {
	states := []WorkspaceSessionState{batch38Workspace(1), batch38Workspace(2)}
	got, active := WorkspaceSessionsForRestore(states, -1, false)
	if len(got) != 1 || got[0].Number != 1 || active != 0 {
		t.Fatalf("negative active restore = %#v, %d; want first state and 0", got, active)
	}
}

func TestWorkspaceSessionsForRestoreNormalizesLargeActiveCoverageBatch38(t *testing.T) {
	states := []WorkspaceSessionState{batch38Workspace(1), batch38Workspace(2)}
	got, active := WorkspaceSessionsForRestore(states, 99, false)
	if len(got) != 1 || got[0].Number != 1 || active != 0 {
		t.Fatalf("large active restore = %#v, %d; want first state and 0", got, active)
	}
}

func TestWorkspaceSessionsForRestoreSelectsValidActiveCoverageBatch38(t *testing.T) {
	states := []WorkspaceSessionState{batch38Workspace(1), batch38Workspace(2), batch38Workspace(3)}
	got, active := WorkspaceSessionsForRestore(states, 1, false)
	if len(got) != 1 || got[0].Number != 2 || active != 0 {
		t.Fatalf("valid active restore = %#v, %d; want workspace 2 and 0", got, active)
	}
}

func TestValidSessionViewModeKeepsMediumCoverageBatch38(t *testing.T) {
	if got := validSessionViewMode(int(ViewModeMedium)); got != ViewModeMedium {
		t.Fatalf("medium mode = %v, want %v", got, ViewModeMedium)
	}
}

func TestValidSessionViewModeKeepsDetailedCoverageBatch38(t *testing.T) {
	if got := validSessionViewMode(int(ViewModeDetailed)); got != ViewModeDetailed {
		t.Fatalf("detailed mode = %v, want %v", got, ViewModeDetailed)
	}
}

func TestValidSessionViewModeKeepsBriefCoverageBatch38(t *testing.T) {
	if got := validSessionViewMode(int(ViewModeBrief)); got != ViewModeBrief {
		t.Fatalf("brief mode = %v, want %v", got, ViewModeBrief)
	}
}

func TestValidSessionViewModeFallsBackForInvalidValuesCoverageBatch38(t *testing.T) {
	for _, mode := range []int{-1, 0x7fffffff} {
		if got := validSessionViewMode(mode); got != ViewModeMedium {
			t.Errorf("invalid mode %d = %v, want %v", mode, got, ViewModeMedium)
		}
	}
}

// TestValidSessionViewModeAcceptsExtendedModesCoverageBatch38 guards against
// f4#1496: validSessionViewMode used to know only Brief/Medium/Detailed and
// silently fell back to Medium for Wide (Ctrl+4) and the far2l-style
// Ctrl+5..Ctrl+0 extended modes, so anything past the first three modes
// never survived a restart even after an explicit save.
func TestValidSessionViewModeAcceptsExtendedModesCoverageBatch38(t *testing.T) {
	for _, mode := range []ViewMode{ViewModeWide, ViewMode5, ViewMode9, ViewMode0} {
		if got := validSessionViewMode(int(mode)); got != mode {
			t.Errorf("extended mode %v round-tripped as %v", mode, got)
		}
	}
}

// TestWorkspaceSessionViewModeRoundTripsThroughSaveAndReloadCoverageBatch38
// exercises the real save/reload path (WriteWorkspaceSessions ->
// LoadWorkspaceSessions -> validSessionViewMode, the same sequence
// ApplyWorkspaceSession uses) for both the legacy three modes and an
// extended mode, per f4#1496.
func TestWorkspaceSessionViewModeRoundTripsThroughSaveAndReloadCoverageBatch38(t *testing.T) {
	for _, mode := range []ViewMode{ViewModeBrief, ViewModeMedium, ViewModeDetailed, ViewModeWide, ViewMode5, ViewMode0} {
		states := []WorkspaceSessionState{{
			Number: 1,
			Left:   PanelSessionState{ViewMode: int(mode)},
			Right:  PanelSessionState{ViewMode: int(mode)},
		}}
		var b strings.Builder
		WriteWorkspaceSessions(&b, states, 0)
		got, _ := LoadWorkspaceSessions(ini.Parse(strings.NewReader(b.String())))
		if len(got) != 1 {
			t.Fatalf("mode %v: reloaded %d states, want 1", mode, len(got))
		}
		if left := validSessionViewMode(got[0].Left.ViewMode); left != mode {
			t.Errorf("mode %v: left panel reloaded as %v", mode, left)
		}
		if right := validSessionViewMode(got[0].Right.ViewMode); right != mode {
			t.Errorf("mode %v: right panel reloaded as %v", mode, right)
		}
	}
}

func TestWriteWorkspaceSessionsEmitsEmptyAndPopulatedFormsCoverageBatch38(t *testing.T) {
	var empty strings.Builder
	WriteWorkspaceSessions(&empty, nil, 0)
	if empty.Len() != 0 {
		t.Fatalf("empty workspace output = %q, want empty", empty.String())
	}

	var out strings.Builder
	state := batch38Workspace(3)
	state.ActivePanel = 1
	state.WidePanel = -1
	state.ShowPanels = true
	state.ShowLeft = false
	state.ShowRight = true
	state.Left.Cursor = "left.txt"
	state.Right.Cursor = "right.txt"
	WriteWorkspaceSessions(&out, []WorkspaceSessionState{state}, 0)
	for _, want := range []string{
		"[Workspaces]", "Count = 1", "Active = 0", "[Workspace/0]", "Number = 3",
		"ActivePanel = 1", "WidePanel = -1", "ShowPanels = 1", "ShowLeft = 0", "ShowRight = 1",
		"Folder = left", "CurFile = left.txt", "Folder = right", "CurFile = right.txt",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("workspace output missing %q in %q", want, out.String())
		}
	}
}
