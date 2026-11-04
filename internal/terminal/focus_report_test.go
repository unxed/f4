package terminal

import "testing"

// CSI ? 1004 h/l: the program hears of the window's focus only while it has
// asked for it (docs/FAR2L_COMPAT.md, T-11; unxed/f4#2161).

func TestFocusReport_OnlyWhileModeIsOn(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, &mockPty{})

	if got := tv.FocusReport(true); got != nil {
		t.Fatalf("a report without the mode: %q", got)
	}
	p.Process([]byte("\x1b[?1004h"))
	if !tv.FocusReportingMode {
		t.Fatal("CSI ? 1004 h did not switch the mode on")
	}
	if got := string(tv.FocusReport(true)); got != "\x1b[I" {
		t.Errorf("focus in: got %q, want CSI I", got)
	}
	if got := string(tv.FocusReport(false)); got != "\x1b[O" {
		t.Errorf("focus out: got %q, want CSI O", got)
	}
	p.Process([]byte("\x1b[?1004l"))
	if got := tv.FocusReport(false); got != nil {
		t.Errorf("a report after CSI ? 1004 l: %q", got)
	}
}

func TestFocusReport_ModeEndsWithTheCommandAndTheReset(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, &mockPty{})

	p.Process([]byte("\x1b]133;C\x07\x1b[?1004h"))
	p.Process([]byte("\x1b]133;D;0\x07"))
	if tv.FocusReportingMode {
		t.Error("focus reporting of a finished command survives")
	}
	p.Process([]byte("\x1b[?1004h\x1bc"))
	if tv.FocusReportingMode {
		t.Error("RIS left focus reporting on")
	}
	tv.FocusReportingMode = true
	tv.ResetKeyboardProtocols()
	if tv.FocusReportingMode {
		t.Error("ResetKeyboardProtocols left focus reporting on")
	}
}

func TestFocusReport_ModeQuery(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	pty := &mockPty{}
	p := NewAnsiParser(tv, pty)

	p.Process([]byte("\x1b[?1004$p"))
	if string(pty.written) != "\x1b[?1004;2$y" {
		t.Errorf("mode 1004 reset: got %q", pty.written)
	}
	pty.written = nil
	p.Process([]byte("\x1b[?1004h\x1b[?1004$p"))
	if string(pty.written) != "\x1b[?1004;1$y" {
		t.Errorf("mode 1004 set: got %q", pty.written)
	}
}
