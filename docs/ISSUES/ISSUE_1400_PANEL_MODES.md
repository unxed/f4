# Issue 1400: configurable file panel modes

The panel modes were four hard-coded column sets in `FileSystemPanel.Resize`.
They are now far2l's ten configurable modes (`panels/flmodes.cpp`), with
far2l's column syntax (`mix/panelmix.cpp`) and width distribution
(`FileList::PrepareColumnWidths` in `panels/flshow.cpp`).

## What the user sees

- **Ctrl+0 .. Ctrl+9** select the ten modes. Ctrl+1..4 are the four modes f4
  always had and, until they are edited, draw exactly as before.
- **Options → File panel modes** lists the ten modes. Enter opens a mode:
  column types, column widths, *Full screen*, and *Reset* back to the built-in
  definition. Closing the dialog returns to the list, as in far2l.
- A mode with *Full screen* set takes the whole width through f4's existing
  Wide layout (`PanelsFrame.Wide`), which is what Ctrl+4 has always done.

## Column syntax

Types are comma separated; widths match them by position. A width of `0`
takes the type's default width, or for a name column a share of the free
space; `N%` is a share of the free space. When the columns do not fit, the
last one is shortened and then dropped, as far2l does.

| Type | Column | Modifiers |
| --- | --- | --- |
| `N` | name | `M` selection mark; `O`, `R` accepted for far2l compatibility |
| `S` / `P` | size / physical size | `C` digit groups, `E` no space before the unit, `F` fractional units, `T` units of 1000; `A` accepted |
| `D`, `T` | modification date, time | |
| `DM`, `DC`, `DA`, `DE` | modified, created, accessed, changed | `B` short date, `M` month name |
| `A` | Unix permissions or Windows attributes | |
| `O`, `U` | owner, group | `L` accepted |
| `LN` | hard link count | |

A group of types that repeats forms stripes: `N,N,N` is Brief, `N,S,N,S` is
two stripes of a name and a size. Files flow down a stripe and on to the next,
and the cursor covers every column of its stripe.

`DC` and `DE` both show `VFSItem.CTime`: creation time on Windows, status
change time on Unix. Owner and group names are looked up only on the local
filesystem; a remote host's ids are shown as numbers.

## Built-in modes

| Ctrl | Name | Columns | Widths | Full screen |
| --- | --- | --- | --- | --- |
| 1 | Brief | `N,N,N` | `0,0,0` | |
| 2 | Medium | `N,N` | `0,0` | |
| 3 | Detailed | `N,SC` | `0,11` | |
| 4 | Wide | `N,SC,DM` | `0,11,14` | yes |
| 5 | Full screen details | `N,SC,PC,DM,DC,DA,O,U,A` | `0,11,11,14,14,14,8,8,10` | yes |
| 6 | Full | `N,SC,D,T` | `0,11,0,0` | |
| 7 | Medium with sizes | `N,S,N,S` | `0,7,0,7` | |
| 8 | File owners | `N,SC,O,U` | `0,11,8,8` | |
| 9 | Permissions | `N,SC,A` | `0,11,10` | |
| 0 | Alternative full | `NM,SC,D` | `0,11,0` | |

`ViewMode` keeps its saved numbers (Medium 0, Detailed 1, Brief 2, Wide 3);
the new modes are 4..9, so a `ViewMode` already in `settings.ini` still names
the same mode.

## Storage

Changed modes are written to `panel_modes.ini` beside `bookmarks.ini`, in
far2l's sections: `[Panel/ViewModes/ModeN]` with `Columns`, `ColumnWidths`
and `FullScreen`, where N is the Ctrl digit. Only modes that differ from the
built-in ones are written, and the file is removed when none do.

## Code

- `internal/panel/viewmodes.go`: the model, parser, defaults, layout, cell
  formatting and `panel_modes.ini`.
- `internal/panel/viewmodes_dialog.go`: the list and the edit dialog.
- `FileSystemPanel` keeps the fitted layout in `layout`; `gridColumnCount` is
  the number of stripes, `Table.SelectCol` stays the cursor's stripe, and
  `stripeOfColumn` maps a table column to its stripe wherever a column index
  used to be taken for a stripe.

## Not done yet

- The status line (`ShowPanelFileInfo`) keeps its own layout; far2l's status
  columns per mode are not implemented.
- far2l's per-mode case conversion and extension alignment are not
  implemented; extension alignment stays the global setting.
- Descriptions (`Z`) and custom columns (`C0`..`C19`) are not available: f4
  has no data for them. Hard link count (`LN`) is available (`VFSItem.Nlink`,
  read from `syscall.Stat_t.Nlink` on Unix and `GetFileInformationByHandle`'s
  `nNumberOfLinks` via libwinescape on Windows).
- The Left and Right menus still list only the first four modes.
- After a restart a widened panel shows mode 4 even if another full screen
  mode was active.
