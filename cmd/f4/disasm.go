package main

import (
	"bytes"
	"fmt"

	"golang.org/x/arch/x86/x86asm"
)

// The decode view of the viewer and of the editor share one x86 decoder,
// golang.org/x/arch/x86/x86asm, and one notion of processor mode: the
// DisasmMode field of either view holds 16, 32 or 64, the mode Decode is
// told to assume. Zero means the mode has not been decided yet. It is set
// from the file header when a file is opened (detectX86Mode), decided the
// same way on first use for a view built without a header, and cycled by
// the user with Editor.DisasmMode / Viewer.DisasmMode, the way Hiew
// switches its disassembler between 16, 32 and 64 bits.

// disasmMaxInstLen is the longest x86 instruction there is, and so the
// read window every decode step asks for.
const disasmMaxInstLen = 15

// disasmModeDefault is what bytes with no recognisable header decode as.
const disasmModeDefault = 64

// disasmModeValid reports whether mode is one of the three x86asm accepts.
func disasmModeValid(mode int) bool {
	return mode == 16 || mode == 32 || mode == 64
}

// nextDisasmMode returns the mode that follows mode in the 64 -> 32 -> 16
// -> 64 cycle. Anything that is not a valid mode restarts the cycle at 64,
// so a stale or hand-edited value cannot leave the view stuck.
func nextDisasmMode(mode int) int {
	switch mode {
	case 64:
		return 32
	case 32:
		return 16
	default:
		return 64
	}
}

// detectX86Mode picks the processor mode for a file from its header: the
// class of an ELF or the machine of a PE. Anything else, a raw code blob or
// a DOS .com included, gets the default; the user can switch from there.
func detectX86Mode(data []byte) int {
	if len(data) >= 6 && bytes.HasPrefix(data, []byte("\x7fELF")) {
		if data[4] == 1 {
			return 32
		}
		return 64
	}
	if len(data) >= 0x40 && bytes.HasPrefix(data, []byte("MZ")) {
		peOff := int(data[0x3C]) | (int(data[0x3D]) << 8) | (int(data[0x3E]) << 16) | (int(data[0x3F]) << 24)
		if peOff > 0 && peOff+6 <= len(data) && bytes.Equal(data[peOff:peOff+4], []byte("PE\x00\x00")) {
			machine := uint16(data[peOff+4]) | (uint16(data[peOff+5]) << 8)
			if machine == 0x014C { // IMAGE_FILE_MACHINE_I386
				return 32
			}
			if machine == 0x8664 { // IMAGE_FILE_MACHINE_AMD64
				return 64
			}
		}
	}
	return disasmModeDefault
}

// disasmInstruction decodes the instruction at the start of data in the
// given processor mode and returns its Intel-syntax text and the number of
// bytes it occupies. A byte the decoder rejects is shown as a one-byte
// "db", so a walk over code always makes progress and resynchronises on
// the next byte, as Hiew's does. Empty input decodes to nothing.
func disasmInstruction(data []byte, mode int, pc int64) (text string, length int) {
	if len(data) == 0 {
		return "", 0
	}
	inst, err := x86asm.Decode(data, mode)
	if err != nil || inst.Len <= 0 {
		return fmt.Sprintf("db 0x%02X", data[0]), 1
	}
	return x86asm.IntelSyntax(inst, nonNegativeUint64(pc), nil), inst.Len
}

// disasmInstLen is the length half of disasmInstruction, for the callers
// that only walk instruction boundaries: cursor and page movement.
func disasmInstLen(data []byte, mode int) int {
	if len(data) == 0 {
		return 0
	}
	inst, err := x86asm.Decode(data, mode)
	if err != nil || inst.Len <= 0 {
		return 1
	}
	return inst.Len
}

// disasmModeLabel is the status-line form of a mode, "Dec:64", shared by
// the viewer's top bar and the editor's status line.
func disasmModeLabel(mode int) string {
	return fmt.Sprintf("Dec:%d", mode)
}
