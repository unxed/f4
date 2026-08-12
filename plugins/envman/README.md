# Environment Manager plugin

Environment Manager (EnvMan) is f4's built-in environment profile manager.
It is an independent, cross-platform implementation of the workflow popularized
by the historical Far Manager plugin of the same name.

## Profiles

Each profile contains a name, an enabled flag, and ordered `NAME=value` lines.
Enabled profiles are evaluated from top to bottom against the environment that
f4 captured at startup. A later assignment overrides an earlier one and an empty
value (`NAME=`) removes the variable.

`%NAME%` references work on every platform. Unix-like builds also accept
`$NAME` and `${NAME}`. References see values produced by earlier lines and
profiles. Use `%%` or `$$` for a literal expansion marker.

Profiles and settings are stored in `<f4-config>/plugins/envman.json`. The file
contains environment values in clear text and must not be treated as a secrets
vault.

## Entry points

- **F11 -> Environment Manager** opens the profile list.
- **Options -> Plugin configuration -> Environment Manager** edits ignored
  variables and the editor preference. On Windows it also imports the legacy
  Environment Manager configuration from Far Manager 3.
- **`envman:`** opens the profile list without the external-change prompt.
- **`envman:+Name`**, **`envman:-Name`**, and **`envman:*Name`** enable, disable,
  or toggle the first profile whose name matches exactly.
- **`envman:<file`** imports `NAME=value` lines and **`envman:>file`** exports the
  current managed environment through the active panel's VFS.
- **`envman:e`** opens the complete managed environment in f4's editor.
- `Plugin.Call("f4.envman", operation)` provides the non-interactive prefix
  operations to macros. `envman`, `4D766E45`, and `0x4D766E45` are aliases.

Macro calls accept exactly one string operation. Profile and file operations
are supported; the empty manager operation and `e` return an explicit
interactive-operation error.

The manager supports inserting, editing, duplicating, deleting, toggling, and
reordering profiles; separators; Unicode clipboard interchange; a multiline
dialog; and editor-based profile or full-environment editing.

## Importing from Far Manager 3

On Windows, open **Options -> Plugin configuration -> Environment Manager**
and choose **Import from Far Manager 3**. EnvMan reads both 64-bit and 32-bit
views of `HKCU\Software\Far2\Plugins\EnvMan`; when both exist, it asks which
one to use. Imported profiles can be appended to the current list or replace
it. Ignored variables and the editor preference are imported as well.

The migration is read-only with respect to Far Manager. Existing Far registry
data is never changed or deleted. Profiles that contain names prohibited by
f4's portable environment model are rejected with a validation error rather
than being silently altered.

Prefix a profile-name character with `&` to assign a manager hotkey (`&&` is a
literal ampersand). `Alt+F4` always opens a profile in f4's editor; ordinary
`F4` does so when **Always use editor** is enabled. `Shift+F3` views the process
environment and `Shift+F4` edits it. Moving an edge profile once more with
`Ctrl+Up` or `Ctrl+Down` creates a group separator.

On a normal F11 open, changes made outside EnvMan can be restored, imported as
an enabled profile, ignored by variable name, or left untouched by cancelling.
The quiet `envman:` form deliberately skips this reconciliation prompt.

## Running shells

Applying profiles updates f4's process environment and future child processes.
For existing local workspace shells, f4 sends a private synchronization script
once that shell is idle. Remote shells are not modified. Values are written to
private runtime files rather than echoed through terminal commands.

Local Unix shells must support POSIX `.`/`export`/`unset` syntax, matching f4's
existing command runner; Windows local shells must be `cmd.exe` compatible.
`SHELL` and `COMSPEC` are therefore protected from profile assignments.

Environment changes made only inside a child shell cannot flow back into the
parent f4 process and therefore cannot be imported automatically.

Run `go test ./plugins/envman` for the focused model, storage, contribution, and
UI tests.
