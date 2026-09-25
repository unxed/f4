package panel

import (
	"bytes"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtinput"
)

// hostConsoleReplyKind identifies a query that the child sent through the
// host console. Windows turns the host terminal's answer into KEY_EVENT
// records on f4's input handle, so those records need to be sent back to the
// child instead of being interpreted as command-line text.
type hostConsoleReplyKind byte

const (
	hostConsoleReplyCPR hostConsoleReplyKind = iota // ESC [ row ; col R
	hostConsoleReplyDSR                             // ESC [ status n
	hostConsoleReplyDA                              // ESC [ ... c
)

type hostConsoleReplyState struct {
	pending []hostConsoleReplyKind
	pty     terminal.PtyBackend
	tail    []byte
	buffer  []byte
	keyUps  int
}

// noteHostConsoleQueries records only the protocol queries for which a host
// terminal can answer with keyboard-looking bytes. Keeping this list makes a
// plain Escape key safe: it is considered a reply candidate only while the
// child has an outstanding query.
func (pf *PanelsFrame) noteHostConsoleQueries(pty terminal.PtyBackend, data []byte) {
	if len(data) == 0 || pf.ShellMode != terminal.ShellModeHost {
		return
	}

	pf.hostConsoleMu.Lock()
	defer pf.hostConsoleMu.Unlock()
	if !pf.HostConsoleActive {
		return
	}

	oldTailLen := len(pf.hostConsoleReplyState.tail)
	combined := make([]byte, 0, oldTailLen+len(data))
	combined = append(combined, pf.hostConsoleReplyState.tail...)
	combined = append(combined, data...)
	for i := 0; i < len(combined); i++ {
		kind, length := hostConsoleQueryAt(combined[i:])
		if length == 0 {
			continue
		}
		// A query fully contained in the old tail was already counted on the
		// preceding read. A sequence that reaches into the new data is new.
		if i < oldTailLen && i+length <= oldTailLen {
			continue
		}
		pf.hostConsoleReplyState.pending = append(pf.hostConsoleReplyState.pending, kind)
	}
	if len(pf.hostConsoleReplyState.pending) > 0 {
		pf.hostConsoleReplyState.pty = pty
	}

	const tailSize = 3 // enough to join the four-byte ESC [ 6 n query
	if len(combined) > tailSize {
		combined = combined[len(combined)-tailSize:]
	}
	pf.hostConsoleReplyState.tail = append(pf.hostConsoleReplyState.tail[:0], combined...)
}

func hostConsoleQueryAt(data []byte) (hostConsoleReplyKind, int) {
	queries := []struct {
		sequence []byte
		kind     hostConsoleReplyKind
	}{
		{[]byte("\x1b[6n"), hostConsoleReplyCPR},
		{[]byte("\x1b[5n"), hostConsoleReplyDSR},
		{[]byte("\x1b[c"), hostConsoleReplyDA},
		{[]byte("\x1b[0c"), hostConsoleReplyDA},
		{[]byte("\x1b[>c"), hostConsoleReplyDA},
		{[]byte("\x1b[>0c"), hostConsoleReplyDA},
	}
	for _, query := range queries {
		if bytes.HasPrefix(data, query.sequence) {
			return query.kind, len(query.sequence)
		}
	}
	return 0, 0
}

// consumeHostConsoleReply handles one KEY_EVENT byte produced by the host
// terminal. It returns true only for a complete, or currently matching,
// terminal reply; ordinary input follows ProcessKey's normal routing.
func (pf *PanelsFrame) consumeHostConsoleReply(e *vtinput.InputEvent) bool {
	if e == nil || e.Type != vtinput.KeyEventType || e.InputSource != "ConPTY" ||
		pf.ShellMode != terminal.ShellModeHost || !pf.IsHostConsoleActive() {
		return false
	}

	pf.hostConsoleMu.Lock()
	state := &pf.hostConsoleReplyState
	if !e.KeyDown {
		if len(state.buffer) > 0 {
			pf.hostConsoleMu.Unlock()
			return true
		}
		if state.keyUps > 0 {
			state.keyUps--
			pf.hostConsoleMu.Unlock()
			return true
		}
		pf.hostConsoleMu.Unlock()
		return false
	}
	if len(state.pending) == 0 {
		pf.hostConsoleMu.Unlock()
		return false
	}

	if e.Char < 0 || e.Char > 0xff {
		state.buffer = nil
		state.pending = state.pending[1:]
		if len(state.pending) == 0 {
			state.pty = nil
		}
		pf.hostConsoleMu.Unlock()
		return false
	}
	state.buffer = append(state.buffer, byte(e.Char))
	kind := state.pending[0]
	valid, complete := hostConsoleReplyProgress(kind, state.buffer)
	if !valid {
		state.buffer = nil
		state.pending = state.pending[1:]
		if len(state.pending) == 0 {
			state.pty = nil
		}
		pf.hostConsoleMu.Unlock()
		return false
	}
	if !complete {
		pf.hostConsoleMu.Unlock()
		return true
	}

	sequence := append([]byte(nil), state.buffer...)
	pty := state.pty
	state.buffer = nil
	state.pending = state.pending[1:]
	state.keyUps = 1
	if len(state.pending) == 0 {
		state.pty = nil
	}
	pf.hostConsoleMu.Unlock()

	if pty != nil {
		_, _ = pf.WritePTY(pty, sequence)
	}
	return true
}

func hostConsoleReplyProgress(kind hostConsoleReplyKind, sequence []byte) (valid, complete bool) {
	if len(sequence) == 0 || sequence[0] != 0x1b {
		return false, false
	}
	if len(sequence) == 1 {
		return true, false
	}
	if sequence[1] != '[' {
		return false, false
	}
	if len(sequence) == 2 {
		return true, false
	}

	last := sequence[len(sequence)-1]
	expected := byte(0)
	switch kind {
	case hostConsoleReplyCPR:
		expected = 'R'
	case hostConsoleReplyDSR:
		expected = 'n'
	case hostConsoleReplyDA:
		expected = 'c'
	default:
		return false, false
	}
	if last == expected {
		body := sequence[2 : len(sequence)-1]
		if kind == hostConsoleReplyCPR {
			if len(body) < 3 || bytes.Count(body, []byte(";")) != 1 {
				return false, false
			}
			for _, b := range body {
				if (b < '0' || b > '9') && b != ';' {
					return false, false
				}
			}
		} else {
			for _, b := range body {
				if b < 0x20 || b > 0x3f {
					return false, false
				}
			}
		}
		return true, true
	}

	// Parameter and intermediate bytes may precede the final byte. No other
	// printable character belongs to a CSI reply, so an accidental user string
	// cannot keep the candidate alive indefinitely.
	if last < 0x20 || last > 0x3f {
		return false, false
	}
	return true, false
}

func (pf *PanelsFrame) resetHostConsoleReplyState() {
	pf.hostConsoleReplyState = hostConsoleReplyState{}
}
