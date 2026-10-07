# Key remapping (`keymap.ini`)

`hotkeys.ini` and the Hotkey Configurator bind *commands* to keys. `keymap.ini`
works one layer below: it substitutes one key for another as the keystroke
arrives, before anything in f4 has looked at it. Two problems need that layer.

Before reaching for either file: **`Ctrl+Shift+P` opens the command palette**,
which finds any command by name and shows the key it currently sits on. On a
legacy terminal that cannot distinguish `Ctrl+Shift+letter` from `Ctrl+letter`,
use the built-in **`Ctrl+Alt+P`** fallback — and in an X11 session the real
`Ctrl+Shift+P` is taken from the X server instead, so it works there even on a
terminal that cannot encode it (see [TTY|Xi](TTYX.md)). When a multiplexer has eaten one
chord, or a laptop has no `F5`, running the command from the palette is usually
faster than writing a rule, and it is the only thing needed when the key was
never the point.

**A terminal multiplexer takes the chord first.** tmux, zellij, GNU screen and
dvtm own their prefixes upstream of f4, so those keys never reach the
application at all: `Ctrl+B` (tmux), `Ctrl+A` (screen), `Ctrl+P`, `Ctrl+T`,
`Ctrl+N`, `Ctrl+O`, `Ctrl+G`, `Ctrl+Q` (zellij). Several of them are f4
defaults — `Ctrl+B` toggles the key bar, `Ctrl+O` the panels, `Ctrl+P` the
passive panel.

**The keyboard has no F-row.** Many laptops and compact boards reach F1-F12
only through a Fn layer, and `hotkeys.ini` would need one binding per key per
modifier row per area to work around that.

A rule solves both in one line, and it also covers keys `hotkeys.ini` cannot
reach at all: framework shortcuts such as `Ctrl+Tab`, dialog and menu keys,
and the editor's own bindings.

## Panel selection without a numpad

Far Manager's standard `Ctrl+Numpad +` and `Ctrl+Numpad -` commands select and
deselect files with the same extension as the file under the cursor (see the
[official panel-command help](https://github.com/FarGroup/FarManager/blob/master/far/FarEng.hlf.m4#L351-L367)).
f4 also binds `Ctrl+=` and `Ctrl+-` on the main keyboard so this operation is
available on laptops and compact keyboards without a numeric keypad.

Extension matching ignores case and uses the last extension; virtual entries
marked `NoExtension` join extensionless files. With a folder under the cursor,
the command marks or unmarks folders instead. The parent entry is never marked,
and autofilter-hidden rows are left alone. `Ctrl+M` restores the previous marks.

`Ctrl+Shift+=` and `Ctrl+Shift+-` open the selection and deselection mask dialogs;
`Alt+=` inverts selection. `Numpad 5` opens the viewer with Num Lock on or off,
while `F3` remains the primary viewer shortcut. All of these are configurable
panel actions. OEM keys use names such as `CtrlVK_BB` and `CtrlVK_BD` in
`hotkeys.ini`, and the UI displays them as `Ctrl+=` and `Ctrl+-`.

## Inserting a panel filename

`Ctrl+Enter` (`Panel.InsertFileName`) inserts the current filename into the
command line followed by a space, ready for the next argument. Shell quoting
is applied to the filename only; the trailing space stays outside the quotes.
No leading space is inserted, so an already typed path prefix stays attached
to the filename. While Fast Find is active, `Ctrl+Enter` still finds the next
match instead of inserting a filename.

## The file

`keymap.ini` lives in the profile directory next to `hotkeys.ini`
(`~/.config/f4`, `%APPDATA%\f4`, or the portable profile). f4 writes a
commented sample on first start; every line in it is inert until you remove a
semicolon.

```ini
[Common]
CtrlAltO=CtrlO
```

The left side is the key you press, the right side is the key f4 sees instead.
Spell both the way the Hotkey Configurator (`Options > Hotkey Configuration`)
shows them: `Ctrl`, `Alt`, `Shift` and `RCtrl` prefixes, then the key (`A`,
`5`, `F7`, `Enter`, `Ins`, `PgDn`, `VK_DC`). Neither case nor the order of the
prefixes matters, so `ShiftAlt1`, `AltShift1` and `altshift1` are one rule.

Everything after a `;` or a `#` is a note rather than part of the rule, on
either side of the `=`. A marker only starts a note at the beginning of a
field or after whitespace, so `Alt;=F1` still binds the semicolon key.

A rule must sit under a section header. The sample file ships with a live
`[Common]` line at the top for that reason — uncommenting a rule under a still
commented-out `;[Common]` would leave it in no section, and it would be
ignored. Section names are the areas of `hotkeys.ini` — `Shell`, `Terminal`,
`Editor`, `Viewer`, `Dialog`, `Menu`, `Disks` — and `Common` applies to all of
them. An area rule wins over a `Common` one.

## Rewriting a whole modifier

A trailing `*` on both sides rewrites the leading modifiers of every key at
once, which is the compact way to move f4 off a prefix the multiplexer wants:

```ini
[Common]
CtrlAlt*=Ctrl*
```

Now `Ctrl+Alt+O` reaches f4 as `Ctrl+O`, `Ctrl+Alt+F5` as `Ctrl+F5`, and so on,
while the plain `Ctrl` chords keep working for whatever the multiplexer leaves
alone. Longer source prefixes are matched first, so `CtrlAltShift*` wins over
`CtrlAlt*`. Exact rules always win over wildcard ones.

A `*` on one side only is ignored: `Ctrl*=F1` would collapse every `Ctrl`
chord onto a single key, and `CtrlB=Ctrl*` names no key at all.

## Keyboards without an F-row

```ini
[Common]
Alt1=F1
Alt2=F2
Alt0=F10
Alt-=F11
AltShift-=F12
AltShift1=ShiftF1
```

The key bar follows: its modifier row is re-derived from the substituted key,
so `Alt+1` shows and runs the plain `F1` command rather than the `Alt+F1` one.

`=` cannot be named on the left-hand side: the first `=` of a line separates
the two sides of the rule, so that one key has to stay as it is.

## Shifted keys have one name

A terminal that speaks neither the kitty keyboard protocol nor win32 input
mode cannot report Shift separately for a printable key. `Shift+1` arrives as
a bare `!`, `Alt+Shift+1` as `Alt!`; under the kitty protocol the same chords
come back as `Shift!` and `AltShift!`, and a backend that reports virtual keys
calls them `Shift1` and `AltShift1`. Since multiplexers routinely strip the
protocol negotiation, the spelling can change underneath a file that used to
work.

f4 folds a shifted character back onto the key that produced it, so all of
those name the same rule. Write the `Shift1 ... Shift0` form: it says which
key you meant, and it does not collide with the wildcard `*`.

## What a terminal cannot send

`Ctrl` does nothing to a digit in a plain terminal — `Ctrl+1`, `Alt+1` and
`Ctrl+Alt+1` produce the same bytes. A `CtrlAlt0=F11` rule therefore cannot be
told apart from `Alt0=F10`, and whichever of the two f4 sees first wins; the
symptom is a key that appears to do the other rule's job. The kitty keyboard
protocol and win32 input mode do distinguish them, but a multiplexer in
between usually removes that. Give such a rule a letter or a punctuation key
instead, or run the command from the palette.

## What a rule does not do

* **Substitution happens once.** The result is never fed back through the
  table, so `AltO=CtrlO` plus `CtrlO=F9` makes `Alt+O` a `Ctrl+O`, not an
  `F9`. Rules cannot chain or loop.
* **A foreign program keeps its keys.** While the panels are hidden and a
  full-screen or busy child owns the terminal (vim, htop, a running command),
  remapping is suspended and every key is forwarded verbatim — the same
  handover the `NoAltScreenApp` and `NoTerminalApp` hotkey conditions make.
* **Right Ctrl follows Ctrl.** As in Far, a rule written for `Ctrl` also
  answers Right Ctrl unless an `RCtrl` rule says otherwise.
* **Bare modifiers are untouched.** Pressing Ctrl alone is not a chord.

## The Mac layout

macOS needs the same layer for a different reason: `Cmd` and `Opt` carry the
editing chords there, and Far gives several of those keys other meanings. That
one is built in and does not have to be written out by hand — see
[Mac keyboard mode](MACKEYS.md). A rule here still wins over it, so a key you
have remapped yourself keeps what you gave it.

## Example: matching keys from another file manager

`keymap.ini` swaps *keys*, not commands, so giving f4 a layout that matches
muscle memory from Total Commander, an older Far build, or anything else is
the same recipe regardless of where the habit comes from: for every command
whose old key differs from f4's, find the key f4 already has it on and add
one line teaching the old key to reach it too.

`Ctrl+Shift+P` is the fastest way to find that key: it looks commands up by
name and shows the chord currently bound to each one. Say your fingers
expect a chord for some command — open the palette, find that command by
name, note the key it already answers to in f4, and add a line teaching your
old chord to reach it too:

```ini
[Shell]
<your old chord>=<the key the palette showed>
```

For "Swap Panels", for instance, f4's own default already happens to be
`Ctrl+U`, so nothing would be needed there — but the same one-line recipe
covers whichever commands your particular habit does expect somewhere else.

Repeat for every other command your muscle memory expects on a different
key. There is no separate "Total Commander preset" to keep in sync here:
each command keeps working under its own key exactly as before, and this
file only teaches the old chord to reach it too — the same mechanism as the
multiplexer and F-row workarounds earlier in this document, just aimed at a
different habit. See also `Options > Hotkey Configuration` in the next
section if what you actually want is to give a command a *new* key rather
than have an old one reach its current one.

## Choosing between the two files

Rebinding a command is still the better tool when a command is what you want
to move: `Options > Hotkey Configuration` lists every command with its key,
and its *Assign* button waits for you to press the new chord and writes
`hotkeys.ini` for you. Menus and help then show the shortcut you chose.

Reach for `keymap.ini` when the key itself has to change — one modifier for
everything, an F-row that does not exist, or a key that belongs to a dialog
rather than to a command.

And reach for neither when you only need the command once: `Ctrl+Shift+P`
runs it by name.

### Panel clipboard paste

`Panel.Paste` defaults to Ctrl+V and Shift+Insert and can be remapped through the
normal action hotkey settings. In panels it saves clipboard images or offers an
image/text choice; text-only clipboard data goes to the command line. See
[Clipboard images](SETTINGS_CENTER.md#clipboard-images) for encoding and naming
preferences. Editor and dialog clipboard shortcuts keep their existing actions.

### Panel file clipboard (f4#1767)

`Panel.CopyFilesToClipboard` (Ctrl+C) and `Panel.CutFilesToClipboard` remember the
selected files of the active panel, or the file under the cursor when nothing is
marked; the Files menu lists both. The files stay where they are. The next
`Panel.Paste` (Ctrl+V, Shift+Insert) copies them, or, after a cut, moves them, into
the directory of the active panel through the same file operations as F5 and F6, so
the usual queue, conflict questions and progress apply. A copy can be pasted again;
a cut is spent by the first paste.

The paths of the files are put on the system clipboard as text, one per line, and
as the platform's file representation where it is available (`text/uri-list` on
Unix and `CF_HDROP` on Windows). That representation lets a file manager paste
files copied in f4, while f4 can also paste local files copied in another file
manager. A remembered f4 paste recognizes its files by the clipboard contents;
if the clipboard holds anything else by then, the remembered files are forgotten
and the paste is an ordinary text or image paste. While the command line holds
text, Ctrl+C and Ctrl+V stay with the command line, and the paste is a text paste.

Cut has no default key because Ctrl+X belongs to the command line history; bind it, or
any other key, in `Options > Hotkey Configuration`. The remembered files are kept
inside f4, while the native file clipboard additionally carries the cut/copy
state where the platform exposes it.
