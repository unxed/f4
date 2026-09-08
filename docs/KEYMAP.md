# Key remapping (`keymap.ini`)

`hotkeys.ini` and the Hotkey Configurator bind *commands* to keys. `keymap.ini`
works one layer below: it substitutes one key for another as the keystroke
arrives, before anything in f4 has looked at it. Two problems need that layer.

Before reaching for either file: **`Ctrl+Shift+P` opens the command palette**,
which finds any command by name and shows the key it currently sits on. On a
legacy terminal that cannot distinguish `Ctrl+Shift+letter` from `Ctrl+letter`,
use the built-in **`Ctrl+Alt+P`** fallback. When a multiplexer has eaten one
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
