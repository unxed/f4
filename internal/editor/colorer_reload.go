//go:build !lite

package editor

import (
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtui"
)

// colorerReloadGen counts reloads of the Colorer base. UI thread only.
var colorerReloadGen int

// ReloadColorerEditors is FarEditorSet::ReloadBase for the editors already
// open: every editor Colorer highlights, or handed to Chroma because Colorer
// could not start, starts Colorer afresh the next time it is drawn, with the
// configuration as it is now. As in FarColorer, whose reload drops its
// editors, a file type picked from the list of types is forgotten.
func ReloadColorerEditors() {
	colorerReloadGen++
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

// startColorer gives the editor a Colorer highlighter and remembers what it
// was started with, for a reload. The caller begins its startup.
func (ev *EditorView) startColorer(path, filename, firstLine string) *ColorerHighlighter {
	ch := newColorerHighlighter(ev, filename, firstLine, vtui.GetHighlighter(path, ""))
	ev.Highlighter = ch
	ev.colorerPath, ev.colorerFile, ev.colorerFirstLine = path, filename, firstLine
	ev.colorerGen, ev.colorerFellBack = colorerReloadGen, false
	return ch
}

// restartColorerAfterReload starts Colorer afresh after ReloadColorerEditors.
func (ev *EditorView) restartColorerAfterReload() {
	if ev.colorerFile == "" || ev.colorerGen == colorerReloadGen {
		return
	}
	ev.colorerGen = colorerReloadGen
	ch, isColorer := ev.Highlighter.(*ColorerHighlighter)
	if !isColorer && !ev.colorerFellBack {
		return
	}
	if isColorer {
		ev.finishColorerWork(ch.startupWorkID)
		ch.Close()
	}
	if !strings.EqualFold(config.App.EditorHighlighter, "Colorer") || !SchemasExist() {
		ev.Highlighter = vtui.GetHighlighter(ev.colorerPath, "")
		ev.colorerFile = ""
	} else {
		ev.startColorer(ev.colorerPath, ev.colorerFile, ev.colorerFirstLine).beginColorerStartup()
	}
	ev.invalidateStates(0)
}

// ColorerUpdateHighlighting is FarEditor::updateHighlighting: the colours
// already computed are dropped and computed again.
func (ev *EditorView) ColorerUpdateHighlighting() {
	ch, ok := ev.Highlighter.(*ColorerHighlighter)
	if !ok {
		return
	}
	ch.DropFrom(0)
	ev.invalidateStates(0)
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}
