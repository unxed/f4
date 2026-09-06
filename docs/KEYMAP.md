# Key remapping (`keymap.ini`)

`hotkeys.ini` and the Hotkey Configurator bind *commands* to keys. `keymap.ini`
works one layer below: it substitutes one key for another as the keystroke
arrives, before anything in f4 has looked at it. Two problems need that layer.

**A terminal multiplexer takes the chord first.** tmux, zellij, GNU screen and
dvtm own their prefixes upstream of f4, so those keys never reach the
application at all: `Ctrl+B` (tmux), `Ctrl+A` (screen), `Ctrl+P`, `Ctrl+T`,
`Ctrl+N`, `Ctrl+O`, `Ctrl+G`, `Ctrl+Q` (zellij). Several of them are f4
defaults — the key bar toggle, the console view, the command palette.

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
Spell both the way the Hotkey Configurator shows them: `Ctrl`, `Alt`, `Shift`
and `RCtrl` prefixes in that order, then the key (`A`, `5`, `F7`, `Enter`,
`Ins`, `PgDn`, `VK_DC`). Case does not matter.

Section names are the areas of `hotkeys.ini` — `Shell`, `Terminal`, `Editor`,
`Viewer`, `Dialog`, `Menu`, `Disks` — and `Common` applies to all of them. An
area rule wins over a `Common` one.

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
AltShift1=ShiftF1
```

The key bar follows: its modifier row is re-derived from the substituted key,
so `Alt+1` shows and runs the plain `F1` command rather than the `Alt+F1` one.

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

## Choosing between the two files

Rebinding a command is still the better tool when a command is what you want
to move: `hotkeys.ini` (or the Hotkey Configurator) keeps menus and help
showing the right shortcut. Reach for `keymap.ini` when the key itself has to
change — one modifier for everything, an F-row that does not exist, or a key
that belongs to a dialog rather than to a command.
