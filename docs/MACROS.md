# Macros in f4

f4 has two kinds of macros.

**Recorded macros** are what `Ctrl+.` gives you: press it, type the keys you
want repeated, press `Ctrl+.` again and assign them a shortcut. Nothing to
learn, nothing to write.

**Scripted macros** are written in Lua and can decide things: which file the
cursor is on, whether the command line is empty, which panel is active. They
are compatible with Far Manager and far2m, so a macro collection you already
have will largely just work.

You do not need Lua installed. The interpreter is inside f4.

If a key has both kinds bound to it, the recorded one wins, as in Far.

### Recorded text and AltGr

Input producers mark confirmed modified or composed text separately from key
shortcuts. Such text is recorded and inserted as text even when a command,
remapping rule or macro is bound to the physical key or the resulting character.
Ordinary character shortcuts keep their existing behavior.

Recorded sequences keep normal Far key names. For confirmed text, f4 writes a
versioned `f4-text-v1:` token containing a Base64URL-encoded keyboard record.
Both INI sequences and exported Lua `Keys()` calls understand this token. It
preserves the character, physical key, modifiers, repeat count and source through
save, load and playback. This token is an f4 extension; exported macros containing
it require f4 and are not portable to Far's `Keys()` implementation. Malformed or
unsupported text tokens are ignored.

## Where macros go

Put `.lua` files under the `Macros/scripts` directory inside f4's
configuration directory, on Linux usually:

```
~/.config/f4/Macros/scripts/
```

Subdirectories are read too. Files are read once, at startup. A file with a
syntax error is reported in the debug log and skipped; the rest still load.
Use `Ctrl+Alt+Shift+M`, or search for **Reload Lua macros** in the Command
Palette (`Ctrl+Shift+P`), to reread this directory without restarting f4.
Loaded Lua macros are also listed in the Command Palette and can be run there.

## Your first macro

`~/.config/f4/Macros/scripts/mine.lua`:

```lua
Macro {
  area = "Shell";
  key = "CtrlShiftG";
  description = "Copy the current file name to the command line";
  action = function()
    Keys("CtrlEnter")
  end;
}
```

Restart f4 and press `Ctrl+Shift+G` on a panel.

## The Macro declaration

```lua
Macro {
  area = "Shell Editor";     -- where it works, space separated
  key = "CtrlA CtrlB";       -- what triggers it, space separated
  description = "...";       -- optional, shown in error messages
  condition = function(key)  -- optional, return false to decline
    return not CmdLine.Empty
  end;
  action = function() ... end;
}
```

`area` and `key` are case insensitive and may each list several values, in
which case the macro is bound to every combination. Omitting `area` means
`Common`, which applies everywhere. If two macros claim the same key in the
same area, the one registered last wins.

### Areas

`Shell`, `Editor`, `Viewer`, `Dialog`, `Menu`, `Disks`, `Common`, `Other`.

When the panels are hidden f4 calls that area `Terminal`; macros written for
Far's `Shell` fire there too, since Far has no such area.

### Key names

The same names Far uses. Modifier prefixes `Ctrl`, `Alt` and `Shift` combine
in that order: `CtrlShiftF5`.

`F1` to `F24`, `Enter`, `Esc`, `Space`, `Tab`, `BS`, `Ins`, `Del`, `Home`,
`End`, `PgUp`, `PgDn`, `Up`, `Down`, `Left`, `Right`, `Multiply`, `Add`,
`Subtract`, `Decimal`, `Divide`, plain letters and digits, and `VK_xx` for
anything else by virtual key code in hex.

`NumEnter` and `NumDel` are the numeric keypad's Enter and Del, as distinct
from the main ones.

A plain `BS` or `Del` belongs to the command line while it holds text: neither a
recorded macro nor a remapped hotkey on that key runs then, the key edits the line.
The macro runs on an empty command line, so `BS` can still be "up one folder".

## Sending keys

```lua
Keys("F5 Enter")
Keys("Esc")
```

Keys are queued and delivered as one batch when the macro returns. This
differs from Far, where `Keys()` blocks until the keys have been processed. It
makes no difference to a macro that is a sequence of keystrokes, but a macro
that reads panel state *between* two `Keys()` calls will see the state from
before either of them.

## What a macro can see

These are live: reading a field asks f4 for the current state.

**`Area`** — `Area.Current` is the area name as a string; `Area.Shell`,
`Area.Editor` and so on are booleans.

**`APanel`** and **`PPanel`** are the active and the passive panel:
`Path` is the panel's directory, `Current` the file under the cursor,
`ItemCount` the number of entries, `SelCount` how many are selected, `CurPos`
the cursor position counting from 1, `Folder` whether the current entry is a
directory, `Empty` whether the panel has no entries, `Left` whether this is
the left panel, `Visible` whether panels are shown at all, `Root` whether the
panel is at a filesystem root, and `Bof` and `Eof` whether the cursor is at
the first or last entry.

**`CmdLine`** — `Value`, `Size`, `Empty`.

**`Far`** — `Width`, `Height`, `Version`, `Title`. `Title` is the exact
current f4 window title, including the configured `ConsoleTitleTemplate`.

Fields f4 does not implement read as `nil` rather than raising an error, so a
ported macro fails where it uses the missing thing, not on the line before.

## Functions

`akey()` returns the key that started this macro, which is useful when one
macro is bound to several keys.

`Actions.Run("Editor.Save")` runs a registered f4 action by name and returns
whether it fired. Everything f4 can do interactively — every menu item and
every hotkey — is a registered action, so this is the macro doorway to all of
it. Action names are listed in the Hotkey Configurator.

For debugging, `App.CopyWindowTitle` copies the active f4 frame's help
identity to the clipboard; if that frame has no help identity, it falls back
to the visible frame title. Its default shortcut is `Ctrl+Alt+Shift+T`. A Lua
macro can read the host window title (including `ConsoleTitleTemplate`) through
`Far.Title`.

`exit()` ends the macro. Keys queued before it are still sent.

`msgbox(text, title)` shows a message. `far.Message(text, title)` does the
same.

`mf.*` carries Far's helpers. String positions are **zero based** and `index`
returns `-1` when there is no match, exactly as in Far and unlike Lua's own
string library:

`iif`, `abs`, `max`, `min`, `int`, `float`, `string`, `len`, `lcase`, `ucase`,
`trim`, `substr`, `index`, `rindex`, `replace`, `asc`, `chr`, `env`, `fexist`,
`fattr`, `sleep`, `beep`, `print`, `exit`, `msgbox`, `Keys`, `akey`, `itoa`,
`atoi`, `mod`, `date`.

`mf.trim(s[, mode])` strips both ends (mode 0, the default), the left (1) or the
right (2). `mf.itoa(n[, radix])` and `mf.atoi(s[, radix])` convert between
integers and text in the radix (10 for `itoa` by default; `atoi` reads the
integer at the start of the text, and a radix of 0 or none picks the base from a
`0x` or `0` prefix). `mf.mod(a, b)` is the integer remainder (0 for a zero
divisor). `mf.date([format])` is the current local time in a C `strftime`
format, English names; with no format it is `%a %b %d %H:%M:%S %Y`.

`bit` and `bit64` carry `band`, `bor`, `bxor`, `bnot`, `lshift`, `rshift`.

The Lua standard library is available except for `io` and `os`, which are
withheld: a macro that writes to standard output would corrupt the screen f4
is drawing on. Use `print()`, which goes to the debug log, and `mf.env` and
`mf.fexist` for the things macros actually need `os` for.

## Porting from Far or far2m

Most macros need no changes. What to check:

- `MenuItem{}` and `CommandLine{}` declarations work. `Event{}` works for four
  groups: `ExitFAR` (f4 is closing), `FolderChanged` (a panel entered another
  folder; it is not raised while a macro is running), `EditorEvent` and
  `ViewerEvent`, whose actions are called with the editor's or viewer's id, the
  event and a parameter (0). The editor events are Far's numbers: 0 the file was
  read, 1 it was saved, 3 the editor is closing; the viewer's: 0 the file was
  opened, 1 the viewer is closing. A declaration for any other group is logged and left out; the rest of
  the file still loads.
- The `Editor`, `Viewer`, `Dlg`, `Menu`, `Object` and `Plugin` objects are not
  implemented yet.
- There is no `ffi` and no `cdef`. f4 has its own FFI for plugins, described
  in `FFI.md`, but it is not offered to macros.
- `condition` is evaluated after the key has already been taken. If it
  declines, the key is replayed, so the visible behaviour matches Far at the
  cost of one extra trip through the input queue.
- No `mmode`, `eval` or macro recording from within a macro.

## When something does not work

Macros report their problems to f4's debug log: files that failed to load,
keys `Keys()` could not parse, and errors raised inside an action. A macro
that runs longer than ten seconds is stopped.

## A note on trust

A macro is a program, running with your privileges and f4's. Read one before
you install it, the same as you would a shell script.## See also

`PLUGINS.md` for plugins, which are a different thing: a macro automates the
keyboard, a plugin adds a filesystem, a highlighter or a command of its own.
