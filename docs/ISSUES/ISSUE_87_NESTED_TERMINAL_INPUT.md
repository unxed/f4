# Issue 87 solution review

## Reproduction

Issue 87 reports that starting F4 inside the built-in F4 terminal on Windows
breaks mouse wheel navigation, left/right clicks, menu cursor rendering, and
progress rendering. The real Windows validation session reproduced the input
part: the outer F4 forwarded SGR mouse sequences, while the nested F4 used the
Windows native reader. Windows ConPTY converted those bytes to `MOUSE_EVENT`
records; the nested reader saw both button clicks as `ButtonState=3`, so it
could not distinguish left and right clicks. Wheel events remained visible but
the same protocol mismatch made the nested session unreliable.

## Three candidate fixes

1. Teach `TranslateMouseInput` or the Windows reader to infer button identity
   from the lossy ConPTY `MOUSE_EVENT` records. This cannot recover whether a
   release was left or right, needs stateful heuristics, and could corrupt
   ordinary native-console mouse input.
2. Add a second Windows-specific mouse wire format to the outer F4 and a
   matching decoder to the nested F4. This would be proprietary, would not
   help other terminal applications, and would create another protocol mode to
   keep synchronized with ConPTY and the terminal mirror.
3. Pass the same child environment on Windows as on Unix, keep the existing
   `F4_NESTED=1` marker, and make a nested F4 select the ANSI reader by default.
   The outer F4 already mirrors Win32/Kitty/mouse protocol state and forwards
   the standard SGR bytes; the nested ANSI reader can parse those bytes without
   the lossy ConPTY conversion. An explicit `--input` remains authoritative.

## Three-pass review of candidate 3

### Pass 1: correctness

`pty_windows.go` now supplies the environment block built by
`terminalChildEnv`, so the Windows shell receives `F4_NESTED=1` just like
Unix shells. At startup, only a nested Windows F4 with no explicit `--input`
gets `vtinput.InputMode="ansi"`. The outer F4 continues to receive native
console events and translates them to standard SGR; the nested F4 parses those
events directly, preserving left/right/release and wheel semantics.

### Pass 2: concurrency and lifecycle

The environment block is immutable for the lifetime of `CreateProcess` and is
kept alive until the call returns. No shared reader or PTY state is changed.
The explicit mode check prevents command-line behavior from being overwritten,
and the change affects only a child process marked by `F4_NESTED=1`.

### Pass 3: scope and regressions

Top-level Windows F4 keeps the native ConPTY reader, Unix behavior is
unchanged, and non-F4 programs still receive the ordinary terminal protocol.
The added pure mode-selection tests cover nested, explicit, top-level, and
Unix cases. Native validation must confirm that a nested F4 receives distinct
left/right SGR events and that wheel, F9, and progress rendering remain usable.

## Follow-up: what the ANSI reader was still missing

Native validation of the build above found the nested session no better: the
wheel did nothing, left and right click selected and pasted in the outer f4
rather than reaching the nested one, letters did not arrive, and only some
function keys did.

Selecting the ANSI reader turned out to be half a fix. A pseudoconsole is a
console, and the console host parses everything the outer f4 writes into
`INPUT_RECORD`s before the child sees it. What the child gets back depends on
`ENABLE_VIRTUAL_TERMINAL_INPUT`: without it a read is answered with the *text*
of those records -- nothing for a wheel notch, a button, or a function key,
and nothing for a letter sent in a protocol the host does not read as text --
and with it the host re-encodes them as the VT stream the reader parses.
Nothing set the flag: `vtinput`'s Windows reader clears it on the native path
and leaves the console mode untouched on the ANSI one, and an input stream
that stays empty is indistinguishable from a user who is not typing.

`prepareNestedConsoleInput` (cmd/f4/nested_input_windows.go) sets it, before
the reader's raw-mode switch so that switch preserves it, and only for a
nested f4 -- a top-level one is reading a console that belongs to whoever
started it. The mouse is asked for through the console mode as well as
through the reader's DECSET sequences, because the host announces its client's
mouse mode to the terminal by watching those flags (microsoft/terminal#9970),
which is how the outer f4 learns to stop keeping the mouse for itself.

Two of the report's items are not addressed here. The F9 menu of a nested f4
is missing "Left" and "Right" because `buildMenuItems` drops the side menus
whenever the panels are hidden, which they are while a program runs; that is
deliberate today and a decision rather than a defect. Progress-bar rendering
was last seen in the original report and has not been re-tested since.

## Follow-up: the segmentation fault on Linux

Starting f4 from f4 on Ubuntu 26.04 dumped core with frame #0 at address zero.
The universal Linux build reaches its libc by re-execing through the host
loader and leaves `GOFFI_UNIVERSAL_REEXEC` behind; a child that inherits it is
told the loader has already run when it has not, binds no libc, and dies
before `main`. `update.SelfCommand` has known this since #402 and starts copies of f4
through the loader itself, but the terminal starts other people's programs,
and the program most likely to be a universal build is f4. `buildChildEnv` now
drops the bridge's variables, so a child does its own libc binding -- and with
them `F4_EXE`, which is untagged and would tell a different f4 binary that it
lives at this one's path.

Later, `update.SelfCommand` stopped going through the loader by hand as well:
glibc 2.31's loader refuses the `/proc/self/fd/<n>` image with `loader cannot
load itself` (Debian 11, Ubuntu 20.04). goffi v0.1.11 tags the guard with the pid
it was written for, so copies of f4 are now started from the file on disk,
with the bridge's variables left out of their environment, and run the bridge
themselves.
