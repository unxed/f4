package netfox

import (
	"errors"
	"testing"
)

func TestSFTPCommandExitStatusHandlesNilCoverageBatch40(t *testing.T) {
	if code, err := sftpCommandExitStatus(nil); code != 0 || err != nil {
		t.Fatalf("nil exit status = (%d, %v), want (0, nil)", code, err)
	}
}

func TestSFTPCommandExitStatusExtractsRemoteCodeCoverageBatch40(t *testing.T) {
	if code, err := sftpCommandExitStatus(fakeSFTPExitError{status: 17}); code != 17 || err != nil {
		t.Fatalf("remote exit status = (%d, %v), want (17, nil)", code, err)
	}
}

func TestSFTPCommandExitStatusPreservesUnknownErrorCoverageBatch40(t *testing.T) {
	want := errors.New("connection lost")
	code, err := sftpCommandExitStatus(want)
	if code != 0 || !errors.Is(err, want) {
		t.Fatalf("unknown exit status = (%d, %v), want original error", code, err)
	}
}

func TestQuoteSFTPCommandArgumentHandlesEmptyValueCoverageBatch40(t *testing.T) {
	if got, want := quoteSFTPCommandArgument(""), "''"; got != want {
		t.Fatalf("empty argument = %q, want %q", got, want)
	}
}

func TestQuoteSFTPCommandArgumentEscapesMultipleQuotesCoverageBatch40(t *testing.T) {
	if got, want := quoteSFTPCommandArgument("a'b'c"), `'a'"'"'b'"'"'c'`; got != want {
		t.Fatalf("quoted argument = %q, want %q", got, want)
	}
}

func TestSFTPCommandOutputChunkEndHandlesZeroLimitCoverageBatch40(t *testing.T) {
	if got := sftpCommandOutputChunkEnd([]byte("payload"), 0); got != 0 {
		t.Fatalf("zero-limit chunk end = %d, want 0", got)
	}
}

func TestSFTPCommandOutputChunkEndKeepsInvalidRuneStartCoverageBatch40(t *testing.T) {
	data := []byte{'x', 0x80, 0x80}
	if got := sftpCommandOutputChunkEnd(data, 2); got != 2 {
		t.Fatalf("invalid-rune chunk end = %d, want 2", got)
	}
}

func TestSFTPCommandLineWriterWithoutCallbackOnlyCountsBytesCoverageBatch40(t *testing.T) {
	w := newSFTPCommandLineWriter(nil)
	data := []byte("pending output")
	n, err := w.Write(data)
	if n != len(data) || err != nil || len(w.pending) != 0 {
		t.Fatalf("writer without callback = n:%d err:%v pending:%d", n, err, len(w.pending))
	}
}

func TestSFTPCommandLineWriterFlushStripsCarriageReturnCoverageBatch40(t *testing.T) {
	var got []string
	w := newSFTPCommandLineWriter(func(line string) { got = append(got, line) })
	if _, err := w.Write([]byte("line\r")); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	if len(got) != 1 || got[0] != "line" {
		t.Fatalf("flushed CR line = %#v, want [line]", got)
	}
}

func TestSFTPCommandCodecRejectsUnsupportedCodepageCoverageBatch40(t *testing.T) {
	if _, err := (&SFTPVFS{codepage: "99999"}).commandCodec(); err == nil {
		t.Fatal("unsupported command codepage was accepted")
	}
}
