# f4 Terminal Architecture Manifest

The built-in terminal in `f4` is one of its most complex components. This document serves as a comprehensive guide for human developers and AI assistants. It explains the fundamental challenges of cross-platform terminal emulation (specifically Windows ConPTY), analyzes how industry-leading terminal emulators solve them, and justifies the final architectural design chosen for `f4`.

## 0. Philosophy: why this component exists at all

far2l proved, by existing, several hypotheses nobody had even ventured before
it -- too exotic for the *nix world and too foreign for the Windows one. This
component inherits them as axioms, so they are written here first, before any
mechanism.

**Hypothesis 1 -- the isolated built-in terminal.** A dual-pane manager does
not have to be an overlay over an existing console, the way Far and mc are. A
fully-fledged, isolated built-in terminal is strictly stronger: it can support
*more* than the hosting terminal does, acting as a **protocol converter**. The
host only speaks win32-input-mode (Konsole does, kitty does not) while the
application only speaks kitty (fish)? The built-in terminal translates. The
host's line wrapping is worse than ours? The built-in terminal outclasses it.
The terminal is not a window onto someone else's session; it is a session of
our own that we present.

**Hypothesis 2 -- win32 input events as the lingua franca.** The win32
`INPUT_RECORD` shape is the ideal *internal* format for such a proxy: it
converts losslessly to and from kitty and win32-input-mode, and far2l showed
that wx/GNOME/Qt events (and for f4, X11 too) convert into it just as cleanly.
f4's one extension: the record carries kitty's `unshifted` field, because
without it a proxied keystroke can lose information.

**Hypothesis 3 -- the win32-style cell grid as the visual model.** A grid of
cells, each described by a win32-like structure extended for truecolor, with
long-line (wrap) support worked in on top, is the ideal description of a
terminal's visual state. In all of far2l's history, no terminal feature has
ever failed to fit it.

Nobody before far2l took a win32 idea into *nix for any reason other than
compatibility (wine, ndiswrapper). far2l did it because the idea was simply
*good*, and it turned out to be extraordinarily effective. f4 carries that
forward and extends it where it asks to be extended -- and takes on the
symmetric impossible task: an honest cross-platform Far, one codebase, no
forks, with the far2l built-in-terminal UX reproduced **on Windows**.

**The target matrix, by name.** The far2l terminal experience -- own
scrollback, correct reflow, protocol conversion -- for: `dir /w` in `cmd.exe`;
PowerShell; WSL; Linux over ssh; and Windows over ssh. All five. Anything less
and the exercise loses its point.

**The channel invariant (corrected form).** f4 consumes the child's output as
a **byte stream** and composes the screen itself. f4 never consumes a
pre-composed grid of cells made by someone else -- that is what the overlay
mode over a real console is for, and it already exists. Metadata *about* the
stream from whoever hosts the child is not a replacement for the stream. The
review question is:
*does f4 still build its screen from the stream?* If yes, the design is in
scope, whoever computed the wrap.

### How this component is verified

ConPTY correctness is checked at two different levels. Platform-independent
tests cover f4's parser, screen state, reflow, and lifecycle invariants. The
Windows integration gate runs the same algorithm against a real, explicitly
selected `OpenConsole.exe`; it does not replace that host with a copied or
reimplemented console model. The complete procedure, including host download,
version checks, diagnostics, and the 300-seed reliability run, is in
[`CONPTY_NATIVE_TEST.md`](CONPTY_NATIVE_TEST.md).

Test doubles may cover f4 lifecycle failures and malformed input. They are not
evidence about Windows console semantics. The Windows gate must use the native
host, real child processes, real ConPTY output, synchronized resizes, and
replayable failure artifacts.

## 1. The Fundamental Conflict: Streams vs. Grids

To build a terminal that supports an infinite scrollback history (like `Ctrl+O` in Far Manager) while correctly displaying interactive applications, we must bridge two completely different OS philosophies:

*   **POSIX (Linux/macOS):** The terminal is fundamentally a **stream of bytes** (`stdout`). The OS sends continuous text. If the text hits the right edge of the window, the terminal emulator (not the OS) decides to wrap it to the next line (Soft Wrap). Applications like `nano` explicitly request to switch to an "Alternate Screen Buffer" to draw 2D interfaces, keeping the main stream clean.
*   **Windows Console API:** The terminal is fundamentally a **2D Grid of cells** (managed by `conhost.exe`). When a program like `cmd.exe` runs a simple `dir` command, it doesn't just output a stream of text. It asks the OS to draw text at specific X/Y coordinates in that 2D grid. 

## 2. The ConPTY Challenge

To support modern Windows features without relying on legacy APIs, `f4` uses **ConPTY** (Windows Pseudo Console). 

ConPTY acts as a "Screen Scraper". It maintains an invisible 2D grid in the background. When `cmd.exe` writes to the console, ConPTY calculates the difference between the old 2D grid and the new one, and generates ANSI escape sequences (Cursor Up, Cursor Down, Erase Line) to replicate this difference.

**The Quirk:** Because `cmd.exe` draws its prompt and output using explicit coordinates, ConPTY emits absolute cursor positioning codes (e.g., `\e[10;1H`) even for simple sequential output. It does not output a continuous flat log.

## 3. Industry Standards: How Others Do It

Before finalizing the `f4` architecture, we analyzed the source code of the most popular terminal emulators (as of 2024/2025):

### A. The "Honest" VTE with Active Reflow (Alacritty, WezTerm, Rio)
*   **How it works:** The entire scrollback history is stored as a massive deque of `Line` or `Cell` objects (`VecDeque<Line>`). Each line holds an `is_wrapped` flag. On window resize, the entire history is re-iterated, wrapped lines are joined, and re-sliced to the new width.
*   **ConPTY Handling:** They use complex heuristics (`enable_conpty_quirks` in WezTerm) on every printed character to guess if ConPTY accidentally inserted a hard break instead of a soft wrap. On Windows, they often pad the screen with empty lines during resize to prevent the cursor from jumping into the scrollback history.

### B. The Web-Based Integrators (Hyper, Tabby, Fluent Terminal)
*   **How it works:** They rely entirely on `node-pty` (backend) and `xterm.js` (frontend). The architecture is fundamentally a bridge passing data chunks between processes.
*   **ConPTY Handling:** Delegated to `xterm.js`, which handles it like a standard 2D grid. Fluent Terminal uses "Resize Throttling" (blocking output during resize) to prevent ConPTY artifacting.

### C. The API Translation Layer (far2l)
*   **How it works:** Parses ANSI codes from the PTY and translates them back into native Windows-like Console API calls (`WINPORT(WriteConsole)`). Scrollback is achieved by intercepting the API's scroll events via a callback.
*   **Why it wasn't chosen:** `f4` aims to render entirely via ANSI escape sequences (`vtui`) to support modern terminals (Kitty, iTerm2). Emulating WinAPI in Go is non-idiomatic and creates unnecessary friction.

## 4. The f4 Decision: VTE Mirror (Grid Extrusion)

*Rule of thumb: Deviating from battle-tested mainstream solutions (like Alacritty's Active Reflow) requires concrete arguments.*

**We explicitly chose NOT to use the Active Reflow architecture (storing history as a `Grid` of `Cell` objects).** Instead, `f4` uses a custom architecture called **VTE Mirror**.

### How VTE Mirror Works (Updated with GridHistory):
1.  **The Active Grid (Viewport):** The terminal maintains a 2D array of cells (`[][]vtui.CharInfo`).
2.  **Horizontal Preservation:** Unlike standard terminals, `f4` allows rows in the Active Grid to temporarily exceed the viewport width. If the terminal is resized horizontally (e.g., shrunk), the off-screen characters are preserved in memory. When the window is enlarged again, the characters reappear. Commands like `EraseLine` respect this preserved length to prevent ghost artifacts.
3.  **Soft Wrap Detection:** If text hits the right edge of the viewport, the `WrapFlags[y]` boolean is set to `true` for that row.
4.  **GridHistory (The Staging Area):** When a line is pushed off the top of the Active Grid (due to scrolling or vertical shrink), it is not immediately serialized. Instead, it enters a `GridHistory` buffer (up to 2000 lines). This allows instant, lossless restoration of the screen when the terminal window is enlarged vertically.
5.  **Extrusion (Linearization):** When the `GridHistory` overflows, the oldest lines are stripped of trailing spaces and permanently serialized into `f4`'s internal `PieceTable` byte stream.
6.  **Reconstruction:** To view the log (`Ctrl+O`), `f4` simply concatenates: `PieceTable` + `GridHistory` + `ActiveGrid`.

### Concrete Arguments for VTE Mirror:
1.  **Domain-Specific Optimization:** `f4` is a file manager, not just a terminal emulator. It already possesses a highly optimized, zero-allocation `PieceTable` engine used for its Editor and Viewer. Extruding the terminal log directly into a `PieceTable` allows the internal Viewer (`F3`) to open a 10-gigabyte terminal log instantly without allocating memory for millions of `Cell` structs.
2.  **Active Reflow on Unix, native validation on Windows:** on Unix a width
    change re-wraps the live 2D grid (`reflowLocked` in
    `cmd/f4/terminal_view.go`). On Windows, resize and reflow behavior is
    changed only after a run through the native ConPTY test described in
    [`CONPTY_NATIVE_TEST.md`](CONPTY_NATIVE_TEST.md). A height-only change
    never reflows on either platform: it moves rows between the viewport and
    `GridHistory`, which was never broken. The `PieceTable`'s `WrapEngine`
    still reflows the scrollback when the user views it (e.g. via `Ctrl+O`).

    This section used to claim that reflowing the live grid is *impossible* because the shell tracks its own cursor and would desync. That claim was wrong, and it talked people out of a design that works. `far2l` reflows exactly this live grid (`WinPort/src/ConsoleBuffer.cpp`, `SetSizeRecomposing`), on two decisions worth copying:

    *   **The cursor is carried as an offset into the flattened cell stream, not recomputed as (x, y).** The desync this section warned about is what happens when you re-derive coordinates arithmetically. Pin the cursor to a *character* and it survives the relayout; the shell then redraws its own prompt line on `SIGWINCH` as it always did.
    *   **The wrap marker lives on the cell, not on the row, and marks the hard break rather than the soft wrap.** A row is a continuation of the next one by default; `EXPLICIT_LINE_BREAK` on the last significant character says the line really ended. `far2l` sets it from cursor movement alone (`\r`, `\n`, or any `SetCursor` leaving the row) — hitting the right edge moves no cursor, so no marker appears and the wrap stays soft. No escape-sequence heuristics are needed at all. `f4`'s `WrapFlags[y]` is indexed by row and must be shifted by hand on every scroll, insert and delete.

    The reflow itself is then short: walk the rows, trim the irrelevant trailing spaces, append the significant cells into one flat vector, and lay that vector back out at the new width, breaking on the markers. Rows pushed off the top go to the scrollback through a callback, the same role `GridHistory` plays here. Two details are load-bearing: cells left of a trimmed space tail get marked "important" so a *second* reflow does not eat legitimate spaces, and the reflow-vs-truncate choice is made by output mode, not by alt-screen — a raw-mode TUI like a Python REPL wants truncation even though it raised no alternate screen.

    Windows behavior is intentionally not assigned from a local transcript or
    a second implementation of the host. The native test records the actual
    stream, resize timeline, f4 state, and a reproducible seed so that a
    failure can be debugged on the same Windows build.

### Why the Unix managed command runs through `eval`

A foreground command is sent wrapped in a group that carries the OSC 133
markers f4 watches for:

    { trap ...; printf "\033]133;C\007"; eval 'CMD' ; FARVTRESULT=$?; printf "\033]133;D\007"; ...; }

The `eval` is load-bearing. A shell parses the whole line before running any of
it, so if `CMD` were pasted in raw, a syntax error anywhere in it would make the
shell reject the group entire — the `printf` that reports completion included.
f4 would then wait forever for a marker that was never going to be printed, with
the panels hidden and the terminal apparently hung. A bare `>` did exactly this.

`eval` parses the user's text separately: a syntax error is reported, `eval`
returns non-zero, and the wrapper around it still runs and reports completion.
It also keeps an unterminated quote from dropping the shell into PS2
continuation, where it would sit silently swallowing whatever was typed next.

The command is single-quoted into the `eval` by `shellSingleQuote`. Anything
that changes this wrapper must keep the property that **no input from the user
can prevent the D marker from being printed**.

### The prompt row belongs to the prompt (`EnsureFreshPromptLine`)

f4 hides the native shell prompt by painting its own command line over the
grid's **bottom row** — the row the prompt always lands on, because "visual
gravity" in `TerminalView.Show()` keeps the cursor row at the bottom of the
viewport. Nothing else may be left there, or it is simply never seen.

A command whose output does not end in a newline breaks that assumption: `cat`
on a file with no trailing newline, `printf foo`, `echo -n`. The cursor stops
after the last character printed, the shell appends its prompt to that same
row, and the overlay swallows both. Issue #863 is exactly this — a two-line
file showed only its first line, while `cat file > out` wrote both, because
nothing was wrong with the output, only with the row it ended on.

zsh solves this for ordinary terminals with `PROMPT_SP`: it prints an inverse
marker plus a carriage return so its prompt always starts on a clean line. f4
*is* the terminal, so it does not need the shell's cooperation. At the `D`
marker of its own wrapper — before any prompt byte has arrived — the view
checks the cursor column and, if it is not zero, performs a line feed itself
(`TerminalView.EnsureFreshPromptLine`). The output keeps its row, the prompt
gets the hidden one, and no second prompt ever becomes visible.

The line feed is only correct while f4 actually covers that row, so the layout
tells the view: `SetPromptOverlaysLastRow(true)` from the own-terminal branch
of `ResizeConsole`, `false` in `ShellModeHost`, where the overlay sits *below*
the mirrored grid and injecting anything would desync the mirror from the real
host terminal. The alternate screen and muted mirrors are skipped as well.

### Known bug: keyboard protocol modes outlive the shell that asked for them

`TerminalView.Win32InputMode` and `TerminalView.KittyFlags` are set by `DECSET`
from whichever shell is driving the panel, and nothing clears them when that
shell exits or when the panel switches to another one. One `TerminalView`
serves them all, so the mode leaks.

Seen in the field: `far2l --tty` on a remote host turns on win32-input-mode and
the kitty keyboard protocol, then dies without resetting them (its "revive
instance" path). Every later keystroke to the plain remote shell is still
encoded, and the shell prints the encoding as text — `;0;0;0;0_ls` on screen,
the tail of `CSI Vk;Sc;Uc;Kd;Cs;Rc _`. Enter goes the same way, as a sequence
rather than a CR, so the terminal appears hung. It does not reproduce when
far2l exits cleanly, because then it resets the modes itself.

This is the same shape as the reply-routing bug fixed in 5cd20cf: state that
belongs to one shell kept in an object shared by all of them.

Mitigated by `TerminalView.ResetKeyboardProtocols`, called when a remote read
loop ends and when the local shell is restarted. That covers the case above —
the modes die with the session that set them. What is still not scoped is a
panel switching between two live shells: the modes of the one being left stay
on for the one being entered. Doing that properly means keeping the modes per
session rather than per `TerminalView`.

3.  **ConPTY Isolation:** ConPTY frequently forces full screen redraws. The VTE Mirror restricts ConPTY's chaos to a fixed-size sandbox (the viewport). The permanent log is immune to cursor-jumping artifacts because lines are only saved when they are mathematically guaranteed to be finished (pushed off the top).
3.  **Golang GC Efficiency:** Go's Garbage Collector handles large contiguous byte slices (`PieceTable` chunks) orders of magnitude better than deep hierarchies of small, pointer-heavy objects (`[]Cell` for infinite scrollback).

## 5. Critical Implementation Rules (Lessons Learned)

Do not attempt to "optimize" or change the following behaviors without consulting this list. They were discovered through painful debugging of Windows ConPTY.

*   **Rule 1: The PTY Drop Order Deadlock.** 
    When shutting down a terminal on Windows, **the ConPTY handle MUST be closed BEFORE the stdin/stdout pipes are closed.** Failing to do this causes a process deadlock (observed in Alacritty, Rio, and `node-pty`).
*   **Rule 2: Bottom-Aligned Initialization.**
    A visual terminal inside a file manager must always initialize with its cursor at `(0, height-1)`. This ensures that incoming text visually "sticks" to the bottom of the screen (like standard `cmd` or `bash` behavior) instead of floating at the top and leaving empty space below. The `GridHistory` buffer cleanly absorbs any "floating UI" glitches during resizing.
*   **Rule 3: Extrusion Guard (The 23 Empty Lines Bug).** 
    When a shell starts, it often issues a `clear` command (`\e[2J`), causing the terminal to scroll. If the log is empty, **do not** extrude empty lines into the `PieceTable`. This prevents the log from starting with 23 blank lines.
*   **Rule 4: Ignore 0x0 Resizes.** 
    If the window is minimized, Windows may send a resize event for `0x0`. Passing this to ConPTY will crash it or corrupt its internal state.
*   **Rule 5: The Shell's Exit Is Not an EOF.**
    ConPTY keeps the output pipe open after the client process has exited; it only closes when `ClosePseudoConsole` is called. A read loop that waits for EOF to learn that the shell is gone waits forever. `f4` therefore waits on the shell's process handle (`PTY.watchExit`) and closes the pseudoconsole itself when the process ends, while the read loop keeps draining the pipe (`ClosePseudoConsole` flushes the last output and does not return until it is read). The case that found this: `exit` inside a batch file ends `cmd.exe` itself (only `exit /b` ends just the batch), and until the watcher existed `f4` sat behind a dead shell with the panels hidden and nothing for Ctrl+C or Ctrl+Break to reach (#409). When the read loop does end, `PanelsFrame.localShellGone` ends the command, brings the panels back and starts a fresh shell, keeping what the old one left on screen.

## 6. Inspiring Features for Future Implementation

Our analysis of competitor codebases revealed several brilliant UX and performance solutions that we intend to implement in `f4`:

1.  **Resize Throttling & Output Blocking (from Fluent Terminal):** 
    During a rapid window resize, ConPTY gets confused and generates garbage. We should buffer incoming PTY data in a `MemoryStream` while the user is dragging the window border, and only flush it to the parser ~60ms after the resize stops.
2.  **Smart Scroll Pinning (from Tabby):** 
    If the user scrolls up the history (`Ctrl+O` in Far), we should set a `pinnedToBottom = false` flag. New output from the PTY should quietly append to the `PieceTable` in the background without forcing the user's view to snap back to the bottom.
3.  **UAC Bridge via Named Pipes (from Tabby):**
    To support opening an "Admin Tab" within the standard user's `f4` window, we can spawn a small elevated helper process (`UAC.exe`). This helper creates the ConPTY session and pipes the data back to the main `f4` process via Windows Named Pipes.
4.  **SPSC / Async PTY IO (from Rio):**
    Reading from anonymous pipes on Windows is synchronous. The `Pty.Read()` call MUST be isolated in its own dedicated goroutine, communicating with the main `FrameManager` via channels (or lock-free queues) to prevent UI freezes during heavy output (`cat /dev/urandom`).

## Appendix A: The ConPTY Observation Log

During the development of `f4`, the following intrinsic behaviors of the Windows ConPTY engine were observed. These are not bugs in `f4`, but fundamental properties of the "Screen Scraper" architecture used by Windows:

1.  **The Echo Chamber:** Every byte sent to the ConPTY input pipe is echoed back to the output pipe as a separate data chunk. This includes long technical strings used for synchronization.
2.  **Absolute Grid Obsession:** Even for simple sequential text output, ConPTY frequently issues absolute cursor positioning commands (e.g., `\x1b[H` to home or `\x1b[row;colH`). It treats the terminal strictly as a 2D grid, not a stream.
3.  **The Premature Prompt:** When a shell command (via `cmd` or `powershell`) finishes, ConPTY renders the next command prompt and moves the cursor *immediately*. This often happens *before* the application can process any trailing escape sequences or control codes sent at the end of the command string.
4.  **Implicit Clears:** ConPTY may unilaterally issue a "Clear Screen" (`\x1b[2J`) or "Erase Line" (`\x1b[K`) sequence during initialization or window resizing, even if the application did not explicitly request one.
5.  **Coordinate Jump Scares:** After an `Erase Display` command, ConPTY almost always forces the cursor back to `(1,1)` (Top-Left), regardless of where the cursor was previously or where the TUI expects it to be.
6.  **Chunk Fragmentation:** Large blocks of output are often fragmented into small, seemingly random chunks, where ANSI escape sequences are split across multiple `Read` operations.

### Current Blockers / Open Issues (Windows specific)

~~1.  **Technical Echo Leakage (Refers to Observation 1):**~~ **SOLVED.** We eliminated the need for `powershell` wrappers entirely by using the `$E` variable in the `PROMPT` environment to automatically emit OSC 133 sequences.
~~2.  **The Duplicate Prompt Problem (Refers to Observation 3):**~~ **SOLVED.** We no longer suppress the native shell prompt. We embrace it, allowing it to act as the true command history delimiter, perfectly mimicking Far Manager.
~~3.  **Bottom-Alignment Defeat (Refers to Observations 2 & 5):**~~ **SOLVED.** Implemented "Visual Gravity" in the render pipeline (`Show()`). The active viewport is dynamically shifted downwards, guaranteeing bottom-alignment regardless of ConPTY's absolute coordinate positioning.
## 7. Host Console Mode (`ShellModeHost`)

In addition to the default built-in terminal emulator (`ShellModeOwn`), `f4` provides an optional **Host Console Mode** (`Panel.ConsoleMode = host` in `settings.ini` or configured in Panel Settings):

### 1. Architecture: PTY Passthrough + Silent VTE Mirror
* **Direct Output Passthrough:** When panels are toggled off (`Ctrl+O`) or a shell command runs, PTY bytes are forwarded byte-for-byte to the real host terminal's primary screen buffer (`vtui.WritePassthrough`).
* **Silent VTE Mirror:** PTY output is simultaneously fed to the internal `AnsiParser` using `mutedPTY`. The mirror maintains full state (PieceTable log, alt screen state, mouse tracking modes, kitty flags) without generating duplicate terminal responses to queries (CPR, DSR, DA, OSC 52).
* **Native Terminal Capabilities:** The host terminal provides true native scrollback, mouse selection, native sixel/kitty graphics, and native job control (`Ctrl+C`, `Ctrl+Z`, `SIGTSTP`).
* **Far-Style Overlay (`ConsoleOverlayUI = true`):** When enabled, `f4` reserves 1–2 lines at the bottom using a `DECSTBM` scroll region (`\x1b[1;<h-n>r`), rendering its command line and keybar directly onto the host console without touching `ScreenBuf`.

### 2. Graceful Degradation Modes
* **`ShellModeSimpleInline`:** Used when PTY/ConPTY is unavailable on Windows (e.g. running under Wine in console). Runs commands with inherited `stdio` via `vtui.Suspend()` / `exec` / `vtui.Resume()` and a keypress pause.
* **`ShellModeSimpleCaptured`:** Used in GUI and non-TTY environments when PTY is unavailable. Runs commands via `LocalCommandRunner` and streams output into an `f4` dialog.

## 8. Graphics in the Built-in Terminal

The built-in terminal accepts pictures from the program running inside it. What
arrives becomes a *placement*: a rectangle of pixels anchored to a row of the
grid, which scrolls with the text around it, follows a resize, is clipped at
the edges of the terminal, and disappears when the row it was printed next to
is gone. `kitty_placements.go` owns that list and every protocol feeds it.

### 1. What is accepted

| Sequence | Protocol | Receiver |
| :--- | :--- | :--- |
| `ESC _ G ... ESC \` | kitty graphics | `kitty_graphics.go` |
| `ESC P ... q ... ESC \` | sixel | `sixel_decode.go` |
| `ESC _ far2l... ESC \` | far2l interact | `HandleFar2lAPC`, images in `far2l_image.go` |

Any other device control string is parsed and dropped. Before sixel support
there were no DCS states at all: `ESC P` was swallowed by `handleEsc` and the
body of the sequence then printed itself onto the screen as text.

### 2. What is answered

A program decides whether to send pictures by asking, so the answers matter as
much as the receivers:

*   **`CSI c` (DA1)** → `CSI ? 62 ; 4 c`. The `4` is what advertises sixel; no
    client will send a picture without it. The level rose from the old `1;2`
    at the same time, because sixel does not exist below VT220.
*   **`CSI 14 t` / `CSI 16 t` / `CSI 18 t`** → the text area in pixels, the
    cell in pixels, the screen in cells.
*   **`CSI ? Pi ; Pa ; Pv S` (XTSMGRAPHICS)** → 256 colour registers, and the
    sixel raster geometry. Some libraries block on this answer.
*   **`CSI ? 80 h` / `l` (DECSDM)** → sixel scrolling off / on. Set disables
    scrolling, which is the way round the hardware behaved and the way round
    xterm settled on after years of the opposite. Reset is the default.

### 3. Where the cursor ends up after a sixel image

At the sixel active position: the column the picture started in, and the row
the sixel cursor reached. A dump ending in a graphics new line (`-`) therefore
leaves the next line of text below the picture; a dump ending in data leaves it
**on top of** the picture.

This is Windows Terminal's rule, chosen deliberately. It looks like a defect
and is regularly reported as one, but it is the only rule under which a program
can print an image on the bottom row at all: anything that moves the cursor
past the picture scrolls a line away to make room for a cursor the client never
asked to move. xterm and mlterm each invented their own algorithm and do not
agree with each other either. A client that wants its text below the image
sends a line feed.

### 3a. far2l inside f4 uses neither sixel nor kitty

far2l's TTY backend decides how to show a picture like this:

```c
if (_tty_caps.kind == TTYCaps::FAR2L) { ...ask over the far2l channel... }
else if (CheckKittyImagesSupport()) { ... }
```

So the moment f4 answers the far2l extension handshake — which it does, and
which is what buys the keyboard and the clipboard — far2l stops looking for
anything else. Its images arrive as `FARTTY_INTERACT_IMAGE`, raw pixels over
the same APC channel as everything else, and the kitty receiver is never
reached at all. Before that channel was answered, far2l running inside f4 said
"backend doesn't support graphics", which was true.

Two fields of that protocol carry two different things depending on a flag.
`WP_IMG_PIXEL_OFFSET`, which far2l's image viewer sets on **every** send, means
the picture is not scaled and the far corner of the area is a pixel offset
rather than a coordinate. Reading it as a coordinate is not a small error: the
viewer sends `area={44:6 10:16}`, and ten as a right-hand column against
forty-four as a left-hand one is a picture minus thirty three cells wide. It
was refused, and the viewer reported that it had failed to send the image to
the terminal.

The offset is applied in whole cells and the remainder dropped, a placement
starting on a cell boundary. The most that costs is the last cell of a pan.

### 4. Known deviations and defects

Read this list before concluding that something is broken.

*   **Text does not punch a hole in a picture; the picture covers the text.**
    On the hardware, and in Windows Terminal, the screen is one bitmap and
    writing a character clears the cell it lands in, so text output over an
    image erases that part of the image. Here a placement is drawn *over* the
    cells at z index 0, so the text goes under it instead. Combined with the
    cursor rule above, a shell prompt that lands on the last row of an image is
    invisible rather than punched through. Fixing it properly means either a
    negative z index, which vtui cannot express yet (see the entry in
    `IMAGES_PLAN.md` section 8), or per-row image slices of the kind Windows
    Terminal keeps.
*   **No byte of child output is ever held back, and that shapes a heuristic.**
    `exciseWindowsSync` hides the background `cd` command that keeps the panel
    and the shell in step. It used to survive a chunk boundary by withholding
    any tail of the data that could begin `cd /d "` — which a lone `c` can, so
    a chunk ending on the `c` of `CSI c` was never answered and every sixel
    client, all of which open with that query, timed out and decided the
    terminal had no graphics. Those bytes are now printed and only remembered
    for matching; when the command does complete, the erase-line the excision
    already writes takes the fragment off the screen with the rest of it. A
    fragment can therefore be visible for one frame, which is the price. The
    rule to keep: **the parser may defer a byte only once it has seen the whole
    seven byte marker, never on a guess.**
*   **A DECRQSS query gets silence.** It used to get its own text printed onto
    the screen, which was worse, but neither is an answer.
*   **The cell size is the real one, not a virtual 10x20.** Windows Terminal
    rasterises sixel into a fixed cell to emulate a VT340. We cannot: `CSI 16 t`
    already tells the child what our cell is, and it has to agree with what
    vtui's sixel encoder assumes, or f4 running inside f4 would draw itself at
    the wrong size.
*   **One sequence, one picture.** Sixel on the hardware writes into a screen
    wide bitmap that later sequences keep drawing into. Here each sequence
    produces its own placement. Paging (`DECGRA`), rectangular copy between
    pages, and a palette shared across images are all consequences of the
    bitmap model and none of them are implemented.
*   **Rule 2 of section 5 applies to the cursor as well.** A fresh terminal
    starts with the cursor at `(0, height-1)`, so a picture printed before
    anything else is placed on the bottom row and scrolls the screen.

## Windows: reflow verification

Windows reflow changes must be validated with the native ConPTY test in
[`CONPTY_NATIVE_TEST.md`](CONPTY_NATIVE_TEST.md). A copied console
implementation, a captured transcript used as a substitute for the host, or a
heuristic result from another console is not an acceptance oracle.
