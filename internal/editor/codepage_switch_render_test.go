package editor

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtui"
)

func TestEditor_CodepageMenuSelectionRedrawsDecodedText(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// These are the UTF-8 bytes for "café", already misdecoded as Windows-1252.
	ev := NewEditorView(piecetable.New([]byte("cafÃ©")), nil, "")
	defer ev.Close()
	ev.Codepage = 1252
	ev.SetPosition(0, 0, 80, 12)

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 13)
	ev.Show(scr)

	ev.ShowCodepageDialog()
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("top frame is %T, want the codepage menu", vtui.FrameManager.GetTopFrame())
	}
	picked := -1
	for i, item := range menu.Items {
		if cp, ok := item.UserData.(int); ok && cp == 65001 {
			picked = i
			break
		}
	}
	if picked < 0 {
		t.Fatal("UTF-8 is not present in the codepage menu")
	}

	// Push() also requests a redraw; drain that request so the assertion below
	// is specifically about applying the codepage choice.
	for {
		select {
		case <-vtui.FrameManager.RedrawChan:
		default:
			goto drained
		}
	}
drained:
	menu.OnAction(picked)
	if ev.Codepage != 65001 || ev.GetText() != "café" {
		t.Fatalf("after selecting UTF-8: codepage=%d text=%q", ev.Codepage, ev.GetText())
	}
	select {
	case <-vtui.FrameManager.RedrawChan:
	default:
		t.Fatal("selecting a codepage did not request a redraw")
	}

	// FrameManager.Redraw is asynchronous. Render the resulting frame here and
	// assert what the user should see after that redraw is serviced.
	ev.Show(scr)
	var got strings.Builder
	for x := 0; x < 5; x++ {
		got.WriteRune(testutil.Rune(scr.GetCell(x, 1).Char))
	}
	if !strings.Contains(got.String(), "café") {
		t.Fatalf("rendered editor text = %q, want café", got.String())
	}
}
