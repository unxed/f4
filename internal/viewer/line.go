package viewer

import (
	"bytes"

	"github.com/unxed/vtui"
)

// lineEndAt returns the offset after the logical line containing start. A
// long line may take several display windows to scan, so the read happens in
// the background and never replaces the window used to paint the screen.
func (vv *ViewerView) lineEndAt(start int64) (int64, bool) {
	size, codepage := vv.Backend.Size(), vv.Codepage
	if c := vv.lineEndCache; c.valid && c.start == start && c.size == size && c.codepage == codepage {
		return c.end, true
	}
	if s := vv.lineEndScan; s != nil && s.start == start && s.size == size && s.codepage == codepage {
		return 0, false
	}

	scan := &lineEndScan{start: start, size: size, codepage: codepage}
	vv.lineEndScan = scan
	backend := vv.Backend
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		pos := start
		var end int64
		ok := false
		for pos < size {
			if ctx.Err() != nil || (backend.ctx != nil && backend.ctx.Err() != nil) {
				break
			}
			n := size - pos
			if n > 64*1024 {
				n = 64 * 1024
			}
			data, err := backend.readAtWithoutCache(ctx.Context, pos, int(n))
			if err != nil || len(data) == 0 {
				break
			}
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				end, ok = pos+int64(i+1), true
				break
			}
			pos += int64(len(data))
		}
		if pos >= size {
			end, ok = size, true
		}
		ctx.RunOnUI(func() {
			if vv.lineEndScan != scan {
				return
			}
			vv.lineEndScan = nil
			if ok {
				vv.lineEndCache = lineEndCache{valid: true, start: start, end: end, size: size, codepage: codepage}
				vtui.FrameManager.Redraw()
			}
		})
	})
	return 0, false
}

func (vv *ViewerView) moveUpRows(rows int64) {
	if vv.Backend == nil || rows <= 0 || vv.lineMoveScan != nil {
		return
	}
	backend := vv.Backend
	scan := &lineMoveScan{backend: backend}
	start := vv.TopOffset
	vv.lineMoveScan = scan
	vv.Busy = true
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		offset, ok := backend.PreviousRowsStart(ctx.Context, start, rows)
		ctx.RunOnUI(func() {
			if vv.lineMoveScan != scan {
				return
			}
			vv.lineMoveScan = nil
			vv.Busy = false
			if !ok || vv.Backend != backend {
				return
			}
			vv.TopOffset = offset
			vv.eofVisible = false
			vtui.FrameManager.Redraw()
		})
	})
}

type lineEndScan struct {
	start, size int64
	codepage    int
}

type lineEndCache struct {
	valid      bool
	start, end int64
	size       int64
	codepage   int
}

type lineMoveScan struct {
	backend *ViewerBackend
}
