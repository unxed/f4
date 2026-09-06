# Drag and drop in f4

Dragging files between f4 and the rest of the desktop. The protocol side
lives in vtui (see its DRAGDROP.md); this file is the f4 side and the
roadmap.

Only graphical backends can do this. Terminals have no protocol for it, so
in a terminal nothing registers and nothing changes.

## What works now

The panels are a drop target. Files dropped on a panel land in the directory
under the cursor, or in the panel's current directory when the cursor is not
on a directory. Shift moves, Ctrl copies, otherwise the source's suggestion
is followed and copy is the fallback.

The destination is always the panel's own VFS, so a panel showing an archive
or a network connection is not a special case: the transfer is handed to
`ExecuteFileOp`, exactly like F5, with its progress, overwrite and error
dialogs, and its queue / background / foreground mode from the config. Files
coming from several source directories become several operations, run one
after another.

A file system that knows it is read-only can implement `IsReadOnly() bool`
and the pointer will say "no" before the drop instead of failing after it.

The other direction works too. Press the left button on a marked file and
move: the marked files are offered to the desktop as a file list. When
nothing is marked, a press on a file and a drag out of the panel's rows
offers that one file - the current file, as every other command understands
it. Inside the rows a left drag still only moves the cursor, and with marks
present a press on an unmarked file still only moves the cursor, so the old
mouse behaviour is kept. Only copy is offered, and only from a local panel - an
archive or a network panel says so in a toast instead.

Under the gogpu backend a drop works as well, with two differences gogpu's
own API imposes: it always copies, because gogpu tells us neither what the
source allows nor which modifiers are held, and nothing happens before the
drop, because gogpu reports nothing before it. The destination comes from
the pointer rather than from the position gogpu reports, which is always
0,0; see vtui's DRAGDROP.md. It needs gogpu v0.50.0 or later.

Both directions under gogpu need gogpu 0.50.1 or later. Before it, a drag
out reached no target and every drop landed in the first cell of the
screen; everything that caused either was in gogpu itself.

Under Wine the DOS paths a drop carries, and the DOS paths a drag out has
to offer, are translated by Wine itself through ntdll's
`wine_get_unix_file_name` / `wine_get_dos_file_name`. Only Wine knows how
the prefix maps drives, and a payload names files through whatever drive
covers them: `Z:` for the root is the common case but not the only one.
The old string rules are kept as the fallback for when those exports are
missing, which is every real Windows.

A drag out under Wine reaches other Wine windows only, including f4's own
other panel. Wine bridges XDND into OLE for drops coming in, but has no
bridge the other way, so files offered to a native X11 application find no
target and the pointer says no. That is upstream, not here.

## When nothing happens

Run with `VTUI_DEBUG=1` and read `Logs/debug.log` inside the active f4 profile. f4 logs why a drag out was not
started and what a drop at a cell would do; vtui logs the protocol side, and
the diagnosing section of its DRAGDROP.md lists every line and what a
missing one means.
## Roadmap

1. done: backend-agnostic core in vtui, drop target in f4.
2. done: X11 XDND, receiving side. Dropping files from another application
   onto a panel works under X11. Two known limits: an INCR selection
   transfer is refused rather than half read, and move is only offered when
   the source publishes `XdndActionList`, since only the source can honour a
   move by deleting the original.
3. done: drag out under X11, from local panels, as copy.
4. done: gogpu, both directions, which covers Windows, macOS, X11 and
   Wayland at once through gogpu's own backends.
5. Dragging to and out of an archive or a network panel.
6. Offering move as well as copy. Everything is in place except the trust:
   the source deletes the originals on the receiver's word, so this wants
   testing against real desktops before it is switched on.
7. Highlighting the drop target while the pointer is over it. It needs the
   panel to paint a hover state, and under gogpu there would be nothing to
   hover with: no event arrives before the drop itself.
8. Wayland under vtui's own backend (`wl_data_device`), for people running
   the native backend rather than gogpu.
9. A protocol for terminals. None exists; if we invent one, far2l's
   extension channel is the natural place for it.

## Open questions, to review

- Dropping on ".." currently means the panel's directory, not its parent.
  Far itself has no drop target to copy here, and "the panel you see" is the
  less surprising of the two. Revisit if it annoys anyone.
- The destination is passed to `ExecuteFileOp` as a path in the target VFS.
  For archive VFSes whose paths are not absolute in the `IsAbs` sense this
  could take the wrong branch there; needs a test with a real archive panel.
- A drop on a panel covered by info / quick view goes to that panel's
  directory. Alternatives are refusing it or dropping into the *other*
  panel; both looked worse than the obvious one.
- Move from another application deletes the source, and the source is
  outside f4. Whether `XdndActionMove` really means "we have deleted it" for
  the common senders is still unconfirmed, which is why move is not offered
  outwards. Under gogpu the question does not arise: only copy is ever
  announced, in both directions.
