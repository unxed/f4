# Issue #722 solution review

## Scope

Issue #722 asks for several copy/move options at once: access rights, an
existing-file mode, symlink contents, retry counts and a log. The reporter was
asked which part comes first and answered "first time access rights options",
so this change implements that one option and nothing else. The other parts of
the issue stay open.

## What the option does

The F5/F6 dialog gains an "Access rights" field with the three entries Far
Manager offers, and the choice becomes the default of the next operation, the
way far2l remembers the options of its copy dialog. The same value is a setting
(`[Panel] CopyAccessRights`, Settings Center: File operations -> Execution), so
a copy started without the dialog — Shift+F5, or F5 with its confirmation
turned off — follows it too.

| Choice | New destination | Destination that already existed |
| --- | --- | --- |
| Default | permissions of the source | keeps its own permissions |
| Copy | permissions of the source | permissions of the source |
| Inherit | permissions of the destination folder | permissions of the destination folder |

A file never inherits the folder's execute bits: a folder needs them to be
entered at all, which says nothing about running the files inside it. So a copy
landing in a `0750` folder becomes `0640`, and a folder copied into it becomes
`0750` — and the folder receives that before its contents are copied, so the
whole copied tree inherits rather than only its root.

## Why these three meanings

far2l is no help here: it replaced Far's three-way choice with a single
"Copy access mode" checkbox (`far2l/src/copy.cpp`), and only the commented-out
`ID_SC_ACCOPY` / `ID_SC_ACINHERIT` / `ID_SC_ACLEAVE` remain of the original.
The meanings above were therefore chosen against what the platforms do:

- "Default" is what `cp` and Win32 `CopyFile` do. A new file is created with
  the source's permissions; an overwritten file keeps the permissions it
  already had, because overwriting its contents is not a request to change who
  may read it.
- "Copy" is the explicit request to carry the permissions across in every
  case.
- "Inherit" is the POSIX reading of the Windows "reset the ACL so it is
  inherited from the target folder": the copy is given what the folder gives a
  new object.

"Inherit" cannot be implemented as "do not chmod at all". `OSVFS.Create` opens
a destination that does not yet exist with `0600`, so that its contents are
never readable through a permissive umask while they are still incomplete, and
the final permissions are restored afterwards. Leaving that alone would give
every inherited copy `0600`.

## Behaviour change

Only one case changes for a user who never opens the new field: overwriting an
existing file with "Default" now leaves that file's own permissions in place,
where f4 previously forced the source's permissions onto it. "Copy" restores
the old behaviour explicitly.

## Where it does not apply

- A move within one filesystem is a rename, and a rename carries the
  permissions with the object. The option is about copies, which is also how
  Far behaves.
- A destination VFS that does not carry Unix permissions ignores the mode, as
  it already did: the mode travels through `SetAttributes`, and a zero
  `UnixMode` is how every VFS in the tree is told to leave permissions alone.
- Windows has no permission bits worth choosing between; Go's `Chmod` there
  only toggles the read-only attribute.

## Tests

`internal/fileops/access_rights_test.go` copies a file into a new destination,
onto an existing destination, and a whole folder, in each of the three modes,
and asserts the resulting permission bits. The Unix assertions skip on Windows.
`TestAccessRightsModeFromConfig` covers a stored value f4 does not know and the
path a copy takes when no dialog was shown.
