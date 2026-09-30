# Junk in the embedded terminal: the directory-sync log

**Read this whole file before you touch anything that puts stray text, stray
paths or stray line feeds into the terminal, or that changes how the shell's
current directory follows the panel.** The same problem keeps coming back in
new clothes (unxed/f4#49, #165, #507, #1376, #1621, #1673 and more); this file
is what we have learned, so that the same mistakes are not made twice.

**This file is never removed, shortened by a cleanup, or treated as garbage.**
Not by the fleet's sweep (LUNOBOT.md section 8.1), not by a docs tidy-up, not by
a "stale document" pass. Add to it; do not prune it. Rewrite an entry only to
correct it, and say what was wrong.

Everything below was learned from logs and screenshots of users. Anything that
was only guessed is marked *hypothesis*; anything nobody could test (we have no
Windows console) is marked *untested on Windows*.

## 1. How the directory follows the panel

f4 does not own the shell. To make the shell's current directory follow the
active panel it **types a command into the shell** (`internal/panel/frame.go`,
`PanelsFrame.syncPTYDirectory`):

| Shell | Line typed | Marker |
| --- | --- | --- |
| cmd.exe | `cd /d "PATH" & rem f4_sync` + CR | `& rem f4_sync` |
| POSIX shells | ` cd 'PATH' && true f4_sync` + CR (leading space keeps it out of history) | `&& true f4_sync` |
| a VFS with its own PTY dialect (FISH+ to Windows) | `PtyChangeDirCommand` | as the VFS decides |

Running a program from a panel types `cd /d "PATH" & program` the same way.

The shell **echoes what it is given**, and cmd/ConPTY draws the echo itself; it
cannot be silenced from outside. Redirecting to `nul` hides only the command's
output, never its echo, so it is not a fix. Because of this, f4 cuts the echo
out of the output stream: `AnsiParser.exciseWindowsSync` and the Unix variant in
`internal/terminal/ansi.go`.

Consequences that every change in this area has to respect:

1. **The excision is the only thing standing between the user and the typed
   text.** If it does not match (wrong order, split chunk, text f4 did not
   expect), the user sees `cd /d "..." & rem f4_sync` on the console.
2. **The owner does not want the way of synchronizing changed** (#1673,
   2026-09-29: "смена способа синхронизации мне очень не нравится. Мы и этот-то
   вариант далеко не сразу смогли заставить работать"). Do not replace typing
   `cd` into the shell with something else (an environment trick, sending the
   `cd` together with the next command, a hidden helper). Work around the
   symptoms on the receiving side instead.
3. The parser must **never hold a byte back on a guess** (see
   docs/TERMINAL.md, the entry about `exciseWindowsSync`): a lone `c` can begin
   `cd /d "` but is also `CSI c`, and holding it back made sixel clients time
   out. It may defer only once it has seen the whole marker.
4. The parser tracks the echo only after f4 announced one
   (`ExpectWindowsSyncEcho` / `NoteLocalShellLineSent`, at most four armed). The
   same text elsewhere in the output is left alone, otherwise Far Manager's
   repainting of console copies of those lines was mangled (#1376). Log line
   when the text is seen but nothing was expected:
   `ANSI_PARSER: cd /d text with no typed line awaiting its echo; left as is`.
   Log line when the echo is cut: `ANSI_PARSER: Excising background Windows CD sync`.

## 2. The two extra line feeds (#1673, Embedded mode)

*Symptom.* Ctrl-F1/Ctrl-F2 (hide/show a panel) moved the console content up one
line per cycle. *Cause* (from the reporter's log 1c2e8c8): when the toggle
changes the active directory, f4 types the `cd` line; the echo is cut and its
row erased, but cmd.exe then sends **two line feeds** (one ending the echoed
line, one blank line before the prompt), and each scrolls the bottom row.
*Not the cause:* the repeated `ResizePseudoConsole` with the same size; ConPTY
sends nothing after it. The hypothesis "do not resize when the size did not
change" was therefore not the fix and was not made.

*Fix.* After an excised echo the parser leaves a private marker
(`CSI 9713 ; n z`, `syncScrollGuardParam`) so the view learns about it **in
stream order**; `TerminalView.SuppressSyncScroll(2)` then keeps the next two
line feeds on the bottom row from scrolling, for at most one second and until
text arrives (`syncScrollBudget`, `syncScrollUntil`). The prompt is drawn on the
erased row, which is where ConPTY draws it too. Verified by the reporter in
Embedded mode on Windows (build 643c192).

*Rule.* Anything printed by the shell as a consequence of the typed line (line
feeds, prompt redraws, OSC 133 marks, title changes) is part of the junk and
must be judged together with the echo, not only the echo.

## 3. Host with overlay / Host without overlay (#1673)

*Symptom.* The typed lines (`cd /d "..." & rem f4_sync`, `cd /d "..." &
far.exe`) appeared in the real console and travelled up and down when panels
were hidden and shown. *Cause.* In the Host modes the shell's output is
written to the real console byte for byte (`vtui.WritePassthrough`); f4 had cut
the echo only from its own mirror copy of the screen, so the console kept it.

*Fix.* The same excision is applied to what goes to the host console (Windows).
Reporter's confirmation: not yet (asked on 2026-09-30, 10:56Z). *Untested on
Windows.*

While at least one panel is visible, Host modes draw through the same parser as
Embedded, so the line-feed fix of section 2 applies there too.

## 4. Stray path at startup (#1673, open)

*Symptom.* Sometimes at the start of f4 a stray prompt-like path
(`C:\WinUtils\File Managers\F4>`) is left on the console, above the
`Microsoft Windows [Version ...]` banner and the real prompt. It then travels
with the content on Ctrl-F1/Ctrl-F2 cycles. The reporter says it does not
happen when f4 starts with the panels hidden. It is not every start. Seen in
Embedded, Host with overlay and Host without overlay alike.

*The log the reporter sent* (build fb112e4, Embedded, `--debug`; kept in the
ticket as `debug_parasite_path.log`), reading in time order, all within 13 ms:

1. `SHELL: mode=own`, `TERM_VIEW: ResetBuffer to 80x24` — the mirror starts
   at 80x24 while the real window is 91x29.
2. The panels open the two directories, then `PTY: local shell started`.
3. `TERM_VIEW: ScrollUp skipped extruding row 0 (Extrusion Guard active)` three
   times, then `TERM_OSC133: A`, `B` (the first prompt).
4. `ANSI_PARSER: Excising background Windows CD sync`, then two window title
   changes: `... cmd.exe - cd  /d "..."` and back to `... cmd.exe`, then OSC 133
   `A`, `B` again (the second prompt, `CMD_SESSION: prompt 2 settled (sent=1)`).
5. `REFLOW_RESIZE: 80x24 -> 91x28; history 0 -> 0 rows, cursor (29,9)` —
   **the resize to the real size comes only after the sync is over.** The
   cursor x of 29 is the length of that very prompt.

*Hypothesis, untested on Windows.* At startup f4 types the sync line while the
shell is still at the initial 80x24 grid, before the first resize. cmd draws
its banner and prompt 1, the sync echo is cut and prompt 2 is drawn, and the
grid is then reflowed to the real size. Prompt 1 survives in the grid as the
row that is not the cursor row; visual gravity and the reflow move the rest to
the bottom but leave that prompt where it was. On a start with hidden panels
the shell's own console is used at the real size from the beginning and no
sync line is typed before the first resize, which fits the reporter's remark.
Things to check if this is worked on: whether the initial sync can be
skipped when the shell's directory already equals the panel's (it is, at
startup: f4 starts cmd in that very directory); what the erase-line written
by the excision does to the row of prompt 1 (it erases the cursor row, which
was prompt 1's row **only if** the echo landed on it); whether the first resize
can be made before the shell is started.

*Rule of thumb.* The startup sync is the only sync typed at a moment when
there is nothing to synchronize; skipping it when the paths already match
cannot change the behaviour the owner wants kept.

*Change made (2026-09-30, unxed/f4#1673; not yet confirmed by the reporter,
untested on Windows).* The first directory sync of a local cmd.exe shell is now
skipped when the panel shows the directory the shell was started in
(`PanelsFrame.localShellAlreadyIn`: f4's working directory, which the shell
inherits, against the active local panel's path, compared case-insensitively).
No line is typed, so there is no echo, no second prompt and no erase-line on
the row of prompt 1 before the first resize. Every later sync, other VFSes,
POSIX shells and a start with different directories in f4's working directory
and the panel are unchanged. The way of synchronizing is not changed (section 1,
point 2). If the stray path still shows up after this, the cause is somewhere
else in the startup sequence (look at the erase-line and the first resize again).

## 5. Tickets with the same family of problem (for the trail)

| Ticket | State | What it was |
| --- | --- | --- |
| unxed/f4#49 | closed | Windows: artifacts in the embedded terminal's log, panels not coming back after commands |
| unxed/f4#165 | closed | Changing the directory scrolled the output up (the hidden `cd /d ... & rem f4_sync`); on long paths the command became visible |
| unxed/f4#55 | closed | Windows: `cd %Temp%` typed by the user was appended to the sync line (`cd /d "C:\1" & cd %Temp%`) and visible in the console: the user's command and f4's own line met in one typed line |
| unxed/f4#158 | closed | POSIX: the bash history filled with f4's own lines (`set +H; cd '...' && { printf "\033]133;C\007"; ... }`) after running commands from f4; the typed line must stay out of the history (leading space, `HISTCONTROL=ignorespace`) |
| unxed/f4#424 | closed | Several workspaces (Ctrl-N): the sync line and its echo showed up in the terminal of another workspace and a directory change in one workspace changed another; the sync belongs to the workspace's own PTY |
| unxed/f4#425 | closed | Umbrella: the embedded terminal on Windows, the ConPTY observation log (docs/TERMINAL.md Appendix A) |
| unxed/f4#507 | closed | macOS: `cd '/tmp' # f4_sync` gave "cd: too many arguments" (zsh: interactive comments off), `~` did not work. Marker became `&& true f4_sync` |
| unxed/f4#1376 | open | Far Manager run from f4: excision cut `cd /d "` out of the middle of repainted rows of FAR's panels; now only an announced echo is cut |
| unxed/f4#1621 | closed | Ctrl-F1/Ctrl-F2 with the terminal shown (panel toggle behaviour, not the junk itself, but it triggers the sync) |
| unxed/f4#1672 | open | Host modes: FAR does not start from f4 (runs through the same typed `cd /d ... & far.exe` line) |
| unxed/f4#1673 | open | The extra line feeds, Host modes, the stray path at startup (this file, sections 2 to 4) |

## 6. Rules for the next change in this area

- Reproduce or read a **debug.log** (`--debug`) first; look for
  `ANSI_PARSER`, `TERM_OSC133`, `REFLOW_*`, `PTY_WIN_SIZE`, `PTY_WIN_TRACE`,
  `CMD_SESSION`. Do not fix from a screenshot alone.
- Do not change the sync mechanism (section 1, point 2).
- Do not hold bytes back on a guess (section 1, point 3).
- Test at the parser level with the byte stream of a real log (see
  `internal/terminal/ansi_sync_test.go`); we cannot run a Windows console in
  CI beyond ConPTY unit tests, so say plainly what only the reporter can check.
- When something new is found, **add it here in the same change**, with the
  ticket number, the symptom, the cause or the hypothesis, and who confirmed it.
- Put this file's path (`docs/TERMINAL_JUNK_LOG.md`) into every ticket of this
  family, closed ones too.
