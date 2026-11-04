package panel

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/terminal/far2ldnd"
	"github.com/unxed/f4/vfs/hostpath"
	"github.com/unxed/vtui"
)

// A drop on the embedded terminal is not a file operation. When a program in
// the terminal has bound for drops (far2l DND/BIND, unxed/f4#1628), the files
// are handed to it as a read-only offer: the program lists and reads them
// itself, and f4 writes nothing anywhere. The panels keep the cells they
// cover, so a drop over a visible panel still goes into that panel.

// dropFileSource is the read-only DNDSource of one GUI drop. It holds the
// dropped files open from the moment of the drop until the offer ends, so
// the program reads what the user dropped even if the name is renamed or
// replaced meanwhile; a file that is modified under the transfer is reported
// as changed (-6) rather than read half old, half new.
type dropFileSource struct {
	entries []far2ldnd.Entry
	files   map[uint64]*droppedFile
}

type droppedFile struct {
	f    *os.File
	size int64
	mod  time.Time
}

// newDropFileSource opens every path of a drop. Only regular files can be
// offered (the base profile of the protocol); one that cannot be offered
// fails the whole drop, and nothing stays open.
func newDropFileSource(paths []string) (*dropFileSource, error) {
	s := &dropFileSource{files: make(map[uint64]*droppedFile, len(paths))}
	for i, raw := range paths {
		p := NormalizeExternalDropPath(raw)
		if p == "" {
			s.Close()
			return nil, fmt.Errorf("cannot use %q", raw)
		}
		f, err := os.Open(p) //nolint:gosec // the user dropped this path
		if err != nil {
			s.Close()
			return nil, err
		}
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			s.Close()
			return nil, err
		}
		if !st.Mode().IsRegular() {
			_ = f.Close()
			s.Close()
			return nil, fmt.Errorf("%s is not a regular file", p)
		}
		id := uint64(i) + 1 //nolint:gosec // an index
		name := strings.ToValidUTF8(hostpath.Base(p), "?")
		if name == "" || name == "." || name == ".." {
			_ = f.Close()
			s.Close()
			return nil, fmt.Errorf("cannot name %q", p)
		}
		s.files[id] = &droppedFile{f: f, size: st.Size(), mod: st.ModTime()}
		s.entries = append(s.entries, far2ldnd.Entry{
			ItemID: id,
			Kind:   far2ldnd.KindFile,
			Flags:  far2ldnd.ItemStream | far2ldnd.ItemSizeKnown,
			Size:   uint64(st.Size()), //nolint:gosec // a regular file has no negative size
			Name:   name,
		})
	}
	return s, nil
}

func (s *dropFileSource) Entries() []far2ldnd.Entry { return s.entries }

func (s *dropFileSource) ReadAt(itemID uint64, p []byte, off uint64) (int, bool, error) {
	d := s.files[itemID]
	if d == nil {
		return 0, false, fmt.Errorf("no item %d", itemID)
	}
	if st, err := d.f.Stat(); err != nil {
		return 0, false, err
	} else if st.Size() != d.size || !st.ModTime().Equal(d.mod) {
		return 0, false, fmt.Errorf("%w: %s", terminal.ErrDNDSourceChanged, d.f.Name())
	}
	if off > uint64(d.size) { //nolint:gosec // size is not negative
		return 0, true, nil
	}
	n, err := d.f.ReadAt(p, int64(off)) //nolint:gosec // off <= size, which is an int64
	switch {
	case errors.Is(err, io.EOF):
		return n, true, nil
	case err != nil:
		return n, false, err
	}
	return n, false, nil
}

func (s *dropFileSource) Close() {
	for _, d := range s.files {
		_ = d.f.Close()
	}
}

// terminalDropTarget answers a drag over the terminal. handled is false when
// the terminal does not take part: nothing in it accepts drops, or a visible
// panel is under the pointer and gets the drop instead.
func (pf *PanelsFrame) terminalDropTarget(ev *vtui.DragEvent) (action vtui.DropAction, handled bool) {
	tv := pf.TermView
	if tv == nil || !tv.DropBound() {
		return vtui.DropNone, false
	}
	if _, onPanel := pf.resolveDropTarget(ev.X, ev.Y); onPanel {
		return vtui.DropNone, false
	}
	x1, y1, x2, y2 := tv.GetPosition()
	if ev.X < x1 || ev.X > x2 || ev.Y < y1 || ev.Y > y2 {
		return vtui.DropNone, true
	}
	// The program only reads; nothing can be moved out of the user's files.
	if !ev.Allowed.Has(vtui.DropCopy) {
		return vtui.DropNone, true
	}
	if ev.Phase != vtui.DragDrop {
		return vtui.DropCopy, true
	}
	src, err := newDropFileSource(ev.Payload.Paths)
	if err == nil {
		_, err = tv.OfferDrop(src, ev.X-x1, ev.Y-y1, uint32(ev.Modifiers), ev.Modifiers != 0)
		if err != nil {
			src.Close()
		}
	}
	if err != nil {
		vtui.DebugLog("DND: drop on the terminal refused: %v", err)
		msg := err.Error()
		vtui.FrameManager.PostTask(func() {
			vtui.ShowMessage(" Drag and Drop ", "The drop cannot be given to the program:\n\n"+msg, []string{"&Ok"})
		})
		return vtui.DropNone, true
	}
	return vtui.DropCopy, true
}
