# Custom File Highlighting in f4

`f4` features a highly flexible file highlighting system modeled after `far2l` and Far Manager. It allows you to colorize file entries and prepend custom visual markers on panels based on file names, glob masks, file sizes, timestamps, and cross-platform attributes.

---

## 1. How It Works

Highlighting is configured via the `highlight.ini` file located in the `f4` configuration directory:
* **Linux/macOS/BSD:** `~/.config/f4/highlight.ini`
* **Windows:** `%APPDATA%\f4\highlight.ini` (or `f4/Profile/highlight.ini` in portable mode)

On the very first launch, `f4` automatically generates a default, balanced `highlight.ini` file with basic rules for executables, archives, hidden files, and directories. You can modify this file to customize colors and visual marker glyphs.

---

## 2. Configuration Format

The file is parsed as a standard INI file. Rules are defined in sections starting with `[Highlight_N]`, where `N` is a non-negative integer indicating the rule's precedence (lower indices are evaluated first).

A comment occupies a whole line and starts with `#`. There are no trailing comments: `#` also opens a color literal, so anything written after a value stays part of that value.

### Available Parameters per Rule

| Parameter | Type | Description |
| :--- | :--- | :--- |
| `Name` | String | A descriptive label for the rule (used for logging and human reference). It plays no part in matching. |
| `Mask` | String | Comma-separated list of glob patterns (e.g., `*.zip, *.tar.gz`). |
| `IncludeAttributes` | String | Comma-separated attributes that **must** be present (see below). |
| `ExcludeAttributes` | String | Comma-separated attributes that **must not** be present. |
| `SizeAbove` | Integer | Minimum file size in bytes (e.g., `1048576` for 1MB). |
| `SizeBelow` | Integer | Maximum file size in bytes. |
| `DateType` | String | Target timestamp: `Modified` (default), `Created`, or `Accessed`. |
| `DateRelative` | Boolean | If `1`, `DateAfter`/`DateBefore` are relative intervals (e.g., `2d`). |
| `DateAfter` | String | Match if file timestamp is after this. Absolute format: `YYYY-MM-DD HH:MM:SS`. |
| `DateBefore` | String | Match if file timestamp is before this. |
| `Mark` (or `MarkChar`) | String | A single character/glyph to prepend before the filename on panels. |
| `ContinueProcessing`| Boolean | If `1`, matching continues to subsequent rules, merging colors. |
| `NormalColor` (or `NormalFileName`) | String | Color expression for unmodified files. |
| `SelectedColor` (or `SelectedFileName`) | String | Color expression for selected files. |
| `CursorColor` (or `NormalColorUnderCursor`, `FileNameUnderCursor`) | String | Color expression for unselected files currently under the cursor. |
| `SelectedCursorColor` (or `SelectedColorUnderCursor`, `FileNameSelectedUnderCursor`) | String| Color expression for selected files under the cursor. |

Each of the four colors takes a foreground, a background, or both, so a rule
can set the background of an ordinary name just as it sets the background of a
selected one. The alternative spellings are the names Far Manager gives the
same four colors in its *Files highlighting* dialog (*Normal file name*,
*Selected file name*, *File name under cursor*, *File name selected under
cursor*); a group copied from Far therefore works here as written.

The four keys are independent, as in Far Manager. A state whose key is
omitted keeps the panel's own color for that state: `Panel.Text`,
`Panel.Text.Selected`, `Panel.Cursor` or `Panel.Cursor.Selected`. In
particular `SelectedColor` does not paint a selected file under the cursor, so
a group can give selected files a background of their own and the cursor still
stands out on them. Set `SelectedCursorColor` to color that state as well.

### Matching Order and the Missing Mask

Rules are evaluated in the order of their section numbers and the **first
match wins**, unless that rule sets `ContinueProcessing = 1`.

A rule without a `Mask` matches every name, and `Name` is only a label, so a
section that describes folders but filters by neither mask nor attribute
colorizes the entire panel and shadows every rule after it:

```ini
[Highlight_1]
Name = Directory
NormalColor = foreground:#FFFFFF
```

`Name = Directory` is a caption, `#FFFFFF` is the color every file on the
panel now gets.

Adding `ExcludeAttributes = Directory` to the *other* rules does not help:
they are never reached. What the rule needs is its own filter,
`IncludeAttributes = Directory`.

---

## 3. Supported Attributes

You can filter files by specifying the following flags in `IncludeAttributes` or `ExcludeAttributes` (comma-separated, case-insensitive):

* `Directory` (or `dir`, `d`): Match directories.
* `Hidden` (or `h`): Match hidden files.
* `Executable` (or `exec`, `e`): Match executable files.
* `ReadOnly` (or `ro`): Match write-protected files (lacking write perms on Unix, or having the read-only attribute on Windows).
* `System` (or `sys`): Match Windows system files.
* `Archive` (or `arc`): Match Windows archive files.
* `Symlink` (or `symlink`, `link`, `sym`, `l`): Match symbolic links.

---

## 4. Date and Time Filtering

Dates can be evaluated either absolutely or relatively:

* **Absolute Filtering (`DateRelative = 0`):**
  Matches if the file's timestamp falls within an exact time frame.
  ```ini
  DateRelative = 0
  DateAfter = 2026-01-01 00:00:00
  ```
* **Relative Filtering (`DateRelative = 1`):**
  Matches files modified/created recently. The values are parsed as Go-style durations (`h` for hours, `m` for minutes, `s` for seconds) or with the custom `d` suffix representing days (e.g., `2d` or `7d`).
  ```ini
  DateRelative = 1
  DateAfter = 48h
  DateBefore = 2h
  ```
  The pair above matches files touched in the last 48 hours but not in the
  last 2.

---

## 5. Color Expressions and Cascade Blending

Color expressions match the standard f4 format: `foreground:<color> | background:<color>`, where `<color>` is a hex RGB value (e.g. `#8AE234` or `#0000FF`).

### Theme rules and `highlight.ini`

The active Color Style and the user's `highlight.ini` contribute separate
rule lists; sections with the same number are not merged key by key. By
default the user's list is placed first, so a matching user rule wins before
the theme is considered. `Appearance.HighlightPriority = 0` in `settings.ini`
selects this order. Set it to `1` when the theme should be tried first.

A rule that matches an item normally ends the search, even if it does not set
a colour for the current state. Use `ContinueProcessing = 1` when a later
matching rule should fill in or blend the remaining colour components. This is
also how a user rule can override only the foreground while retaining a
background supplied by a later theme rule:

```ini
[Highlight_100]
Mask = *.log
NormalColor = foreground:#FF5555
ContinueProcessing = 1
```

Conversely, a broad user rule without a mask or attribute filter can stop all
theme rules below it. For a folder-only override include
`IncludeAttributes = Directory`; for a file-only override exclude that
attribute.

### Cascade Blending (`ContinueProcessing = 1`)

Normally, `f4` evaluates rules from top to bottom and stops on the first match. However, if `ContinueProcessing = 1` is specified, `f4` merges the colors of this rule with subsequent matches.

Since f4 color parsing is transparent, if a rule only specifies `foreground:#FF0000`, the background will be inherited from the panels (or from subsequent matching rules), allowing layered styles.

---

## 6. Full Example Configuration

```ini
[Highlight_0]
Name = Temporary files
Mask = *.tmp, *.bak, *~
Mark = •
NormalColor = foreground:#888888

[Highlight_1]
Name = Huge Logs
Mask = *.log
SizeAbove = 104857600
NormalColor = foreground:#FF5555 | background:#220000
SelectedColor = foreground:#FF5555 | background:#0000A0

[Highlight_2]
Name = Executables
Mask = *.sh, *.bash
IncludeAttributes = Executable
ExcludeAttributes = Directory
Mark = *
NormalColor = foreground:#8AE234

[Highlight_3]
Name = Read-Only Files
IncludeAttributes = ReadOnly
Mark = 🔒
NormalColor = foreground:#D3D7CF
ContinueProcessing = 1

[Highlight_4]
Name = Archives
Mask = *.zip, *.tar, *.gz, *.7z
NormalColor = foreground:#AD7FA8
SelectedColor = foreground:#AD7FA8 | background:#0000A0

[Highlight_5]
Name = Directories
IncludeAttributes = Directory
NormalFileName = foreground:#FFFFFF | background:#000000
SelectedFileName = foreground:#FFFF00 | background:#000000
FileNameUnderCursor = foreground:#FFFFFF | background:#008080
FileNameSelectedUnderCursor = foreground:#FFFF00 | background:#008080
```

The last rule spells its colors the Far way and covers all four states of a
folder name: ordinary, selected, under the cursor, and selected under the
cursor. Because the cursor keys keep their own background, the cursor stays
visible on a selected folder instead of merging into the selection color.

---

## 7. Sort Groups

Sort groups reuse this file and this rule syntax to answer a different
question: not *what colour is a file*, but *where on the panel does it belong*.
A coloured rule can do both jobs, just as in Far: add `Group` to the existing
`[Highlight_N]` section instead of copying its mask and attributes into a
second section.
A panel with sort groups switched on clusters its files by group first and
applies the current sort mode inside each cluster — "all images together",
"executables at the top".

### Configuration

The preferred form is an existing `[Highlight_N]` section with one additional
key. The rule's matcher and its colours are then shared:

```ini
[Highlight_100]
Name = Archives
Group = 1
Mask = *.zip, *.rar, *.7z
ExcludeAttributes = Directory
NormalColor = foreground:#FF00FF | background:#000000
```

`Group` is the position of the cluster on the panel. Rules with the same
number form one cluster.

A `[SortGroup_N]` section is the other way to define a group: a rule that only
sorts and colours nothing (the sample `highlight.ini` is written this way). It
has the same matching keys. Use it when a group has no colour of its own; a
`[Highlight_N]` section written only for sorting would hide the colours of the
sections below it, because the first matching section wins, unless it also
says `ContinueProcessing = 1`.

The file is read when f4 starts, so restart f4 after editing it.

Two keys are specific to group configuration:

| Parameter | Type | Description |
| :--- | :--- | :--- |
| `Name` | String | Label for the group. Defaults to its mask list. |
| `Group` | Integer | Position of the cluster on the panel. Defaults to the section's position in the file. |

Rules are tried in section order and the first match wins, so a narrow rule
placed before a broad one carves items out of it. Sections that share a `Group`
number form a single cluster — that is how a group can match either by
attribute or by name:

```ini
[Highlight_101]
Name = Executables
Group = 0
IncludeAttributes = Executable
ExcludeAttributes = Directory
NormalColor = foreground:#00FF00

[Highlight_102]
Name = Executables (by name)
Group = 0
Mask = *.exe, *.com, *.bat, *.cmd, *.ps1, *.sh
NormalColor = foreground:#00FF00

[Highlight_103]
Name = Images
Group = 2
Mask = *.png, *.jpg, *.jpeg, *.gif, *.webp
NormalColor = foreground:#00FFFF
```

Files that match no group fall into the default group, number `10000`, which
puts them after every configured cluster. A group meant to sit *below* the
unclassified files therefore just needs a larger number, e.g. `Group = 20000`.

### Using them

Grouping is a per-panel switch, off by default, and the panel remembers it
across restarts:

* **Left**/**Right** menu → *Use sort groups*
* the sort menu (`Ctrl+F12`) → *Use sort groups*
* action `Panel.SortUseGroups` (and `Panel.Left.SortUseGroups` /
  `Panel.Right.SortUseGroups`), bindable from the hotkey settings and reachable
  from the command palette

Two properties are worth knowing. Directories still come first: a group never
pulls a folder down among the files. And the group order is not flipped by the
reverse-sort toggle — "executables first" stays first when the name order is
reversed, only the contents of each cluster turn around. Switching a grouped
panel to *Unsorted* keeps the filesystem order inside every cluster instead of
sorting it.
