# Issue #511 solution review

## Report and current behavior

The report asks for two things. Terminal multiplexers (tmux, zellij, screen,
dvtm) run their commands from Ctrl/Alt chords, and with f4 in the foreground
the keypress may not survive the trip, so f4's own control combinations should
be at least partially overridable — or the modifier changeable. Separately,
keyboards without an F1-F12 row cannot send the function keys f4 is built
around.

Most of the first half already works. `HotkeyManager` overlays `hotkeys.ini`
onto defaults taken from the action registry, the Hotkey Configurator writes
that file, and every registered command can be rebound per area. The gaps are
narrower than the report suggests but real:

- The manager binds *actions*. Framework shortcuts (`Ctrl+Tab`), dialog keys,
  menu keys and the editor's own bindings are consumed before or beside it and
  cannot be moved at all.
- "Change the modifier" has no compact form: it is one entry per key, per
  modifier row, per area.
- The F-row case is the same problem repeated 48 times.
- The chords that actually collide are f4 defaults: `CtrlB` (tmux prefix),
  `CtrlA` (screen prefix), `CtrlO`, `CtrlP`, `CtrlG`, `CtrlN` (zellij).

## Three-pass review

### Pass 1: extend `hotkeys.ini` with alias entries

Let a binding name another key instead of an action, resolved inside
`HotkeyManager.GetAction`. Reject: the resolution would only run where the
hotkey manager runs, which is precisely the set of keys that is already
configurable. Keys owned by vtui, by dialogs or by the editor — the actual gap
— would still be unreachable, and `HotkeyManager.Save` rewrites the file
wholesale from binding diffs, so alias entries would need a second, parallel
persistence path in the same file.

### Pass 2: teach the input backends a modifier-swap option

Add a setting that rewrites Ctrl to Ctrl+Alt (or similar) in the terminal
translation layer. Reject: the collision is per-chord, not per-modifier — a
zellij user needs `Ctrl+P` and `Ctrl+O` moved while `Ctrl+C` and `Ctrl+D` must
stay where the shell expects them. A global swap trades one conflict set for
another, and it would sit below `TranslateInput`, where the same rewrite would
also hit keys forwarded to a child process.

### Pass 3: a substitution table in front of the input filter

Add `keymap.ini`, read into a `KeyRemap` table, applied at the top of
`MacroManager.Filter`. That filter is the single choke point every real
keystroke passes through, and vtui hands it the same `*vtinput.InputEvent`
that it later dispatches to frames, so rewriting in place reaches macros,
plugin interception, configurable hotkeys and the frames themselves with one
substitution. Source spellings come from `EventToHotkeyString` (the same
strings the Hotkey Configurator shows) and targets are parsed by `ParseFarKey`
(the same parser macro playback uses), so no third key vocabulary appears. A
trailing `*` on both sides rewrites a modifier prefix, which is the report's
"change the modifier" in one line. Select this pass.

## Safety checks

Substitution is applied once and never re-entered, so rules cannot chain or
loop. `keymap.ini` is its own file: `HotkeyManager.Save` rewrites `hotkeys.ini`
in full, and a section living there would be lost on the next save from the
dialog.

Remapping is suspended while the panels are hidden and an AltScreen program or
a busy PTY owns the keyboard, matching the handover the `NoAltScreenApp` and
`NoTerminalApp` conditions already make; otherwise vim or htop would receive a
chord the user never pressed. Bare modifier presses are excluded for the same
reason — the key bar and the terminal forwarder track them separately. Lock
states in `ControlKeyState` describe the keyboard rather than the chord and are
carried over untouched, while the scan code, which named the physical key, is
cleared as macro-injected events already leave it.

An absent or fully commented `keymap.ini` produces an empty table that `Apply`
short-circuits on before building any key string, so users who never touch the
file pay nothing per keystroke. The shipped sample is inert by construction and
a test asserts it: f4's INI reader has no notion of comments, so `;Alt1=F1`
would otherwise register as a rule with a `;Alt1` source.

Unit tests cover area precedence, `RCtrl` falling back to a `Ctrl` rule,
longest-prefix wildcard order, exact rules winning over wildcards, rejection of
one-sided `*` rules, in-place event rewriting including preserved lock state,
the absence of chaining, and bare-modifier and non-key events passing through.
Validation under zellij, tmux and screen, as the report asks, remains a manual
check.
