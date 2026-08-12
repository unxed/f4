# Mounting f4 virtual file systems through FUSE

## What this is

`f4` can already read archives, SFTP/FTP hosts, Android and iOS devices. Every
one of those is a `vfs.VFS` implementation, and every one of them is invisible
to the rest of the machine: `grep`, `ffmpeg`, an image viewer or a compiler
cannot follow the user into a `.tar.zst` or onto a remote host.

The `fusefs` package removes that wall. It exposes any VFS the file manager can
open as an ordinary directory, so that *any* program on the system can read it.
Mounting an archive and running `rg` over it is the same gesture as mounting a
remote host and opening a file from it in an editor that knows nothing about
`f4`.

The whole feature is one translator plus a small amount of bookkeeping around
it. `f4` already speaks a file system API; FUSE is a file system API; the job
is to connect the two and to decide who owns what.

---

## Where we are

The feature is built in iterations. Each one ends with something a user can
actually do, so that a wrong assumption surfaces while it is still cheap to
undo. The order below is not the order of difficulty — it is the order in
which the risks are worth retiring.

| # | Iteration | What it lets you do | State |
|---|---|---|---|
| 0 | Read-only bridge | Mount a VFS from inside `f4` and read it with any program | done |
| 1 | The command line | `f4 --mount`, `--umount`, `--list-mounts`, `--daemon`, fstab | done |
| 2 | **The UI command** | One key in the panels; a dialog listing every live mount | **← you are here** (next) |
| 3 | Groundwork for writing | Backend capabilities, a real errno map, a `Statfs` writers accept, `Readlink` | |
| 4 | Writing | `cp`, `tar -x`, an editor saving into a mount | |
| 5 | Native random write | The same, at SFTP speed instead of spool speed | |
| 6 | Parallelism | One slow read stops being one stalled mount | |
| 7 | Beyond POSIX | xattrs, `Revision`, uid/gid mapping, a non-FUSE exporter | |

### Iteration 1 — the command line (current)

**Goal.** Make a mount reachable without a terminal UI, and make the request
for one a piece of data rather than a method call.

**What it delivers.**

    f4 --mount ~/dumps/backup.tar.zst --daemon     # prints the mount point
    rg TODO "$(f4 --mount ~/x.tar.zst --daemon)"   # usable in a shell pipeline
    f4 --list-mounts
    f4 --umount /run/user/1000/f4/mnt/backup

    # /etc/fstab, via a mount.fuse.f4 symlink to the f4 binary
    sftp://user@host/srv  /mnt/srv  fuse.f4  noauto,user,ro  0 0

**Exit criteria.**

* A mount survives the process that asked for it (`--daemon`).
* A mount made from a shell is visible to, and unmountable by, a different
  `f4` — which is what iteration 2 needs in order to show a mounts dialog.
* `--rw` is *accepted and refused* with a clear message. The flag surface does
  not have to change when iteration 4 lands, and nothing silently comes up
  read-only when the caller asked for write.
* The whole command line is testable with no FUSE present, on every platform
  `f4` ships for.

**Risk it retires.** Whether a mount can be a process of its own. Everything
after this depends on the answer, and finding out late would mean rewriting
the lifetime code twice.

**Bonus, and the reason this came before writing:** `--mount --daemon` is also
the integration test harness. From iteration 3 on, a test can mount a `MemVFS`
from a shell script and run `cp`, `tar -x` and `dd` against it — real programs
against a real kernel mount, which is the only way write semantics can honestly
be checked.

### What each later iteration is for

**2 — the UI command.** A key in the panels that mounts what the active panel
is showing, and a dialog over the same registry, listing every live mount with
Unmount / Unmount all / Go to. Cheap once iteration 1 exists, because both
entry points build the same `Spec`. Doing it now, while everything is still
read-only, validates the second of the two user-facing surfaces before any
write code exists to complicate it.

**3 — groundwork for writing.** Four things that are invisible while a mount is
read-only and become load-bearing the moment it is not:

* A capability set on the VFS side (`Write`, `RandomWrite`, `Rename`, `Chmod`,
  `Times`, `Symlink`, `ConcurrentOps`), so a mount can refuse `--rw` for a
  backend that cannot do it instead of failing halfway through a `cp`.
* One central error-to-errno map, tested. Scattered `EIO` is how a file manager
  ends up telling the user nothing.
* A `Statfs` that reports something. Zeroes are a fine answer for a read-only
  mount and a bad one for a writable mount: `cp`, `git` and most file dialogs
  check free space and refuse to write to a file system that claims to have
  none.
* `Readlink`. `vfs.VFS` has no such call, so symlinks are currently presented as
  ordinary files. That is cosmetic while reading and fatal while writing —
  `tar -x` creates symlinks, and an extraction into a mount that cannot make
  them simply fails.

**4 — writing.** `Setattr`, `Create`, `Write`, `Unlink`, `Rmdir`, `Rename`,
`Flush`. The interesting part is not the opcodes but write buffering: FUSE
writes arrive as many small offsets, while `Create` returns a plain
`io.WriteCloser`, so each open handle needs a staging file, committed on
`Flush`/`Release`. Two details decide whether this is correct or merely
plausible:

* A table of open handles keyed by path. Without it, two processes writing to
  one file each stage a private copy and the last close silently wins.
* Cache invalidation on write, in the same iteration and not later. Otherwise
  `cp a b && ls` does not show `b`.

`fsync` becomes a no-op and the semantics are documented as commit-on-close;
committing per `fsync` turns a tool that syncs every chunk into a full re-upload
per chunk. Archive backends stay read-only until this commit path has been
exercised.

**5 — native random write.** An optional interface — `OpenWriteAt(path)`
returning something with `WriteAt` and `Truncate` — implemented by `OSVFS` and
SFTP, both of which really do support positional writes. This removes the
staging file, the read-modify-write download when opening an existing file, and
commit-on-close semantics, for the two backends where writable mounts are
actually wanted.

**6 — parallelism.** Replace the single bridge mutex with per-backend policy.
The mutex is not one decision but three different ones collapsed together:
`*sftp.Client` is already safe for concurrent use over a single connection, a
fully extracted archive is safe because reads go to ordinary files, and an
MTP or iOS session genuinely is one conversation at a time. The same iteration
carries the cheap wins that are not about locking at all: `ReadDirPlus` filled
from the directory listing so `ls -l` costs one round trip, `MaxWrite` raised
to 1 MiB, `MaxBackground` raised from its default of 12, `DisableXAttrs` so a
single `ls` does not produce a burst of `getxattr`, and spooling that releases
the lock between chunks instead of freezing the mount for the length of a 4 GiB
download. The iteration is gated on a benchmark — `dd`, `tar -c`, `rg` over a
mount, with numbers recorded before the changes — because otherwise "faster" is
not a claim anyone can check.

**7 — beyond POSIX.** Extended attributes and `VFSItem.Revision` under a
documented `user.f4.*` namespace, `--uid`/`--gid`/`--umask` in the style of
sshfs, progress for long spool transfers through the existing background-job
machinery, and a second exporter for the platforms where FUSE is not the
answer: macOS, where the macFUSE kernel extension is increasingly hard to
install, can be served by a loopback NFS server instead, entirely in Go.

---

## Layout

    fusefs/fusefs.go            mount manager: Options, Mount, MountVFS,
                                List/Find/Unmount/UnmountAll, mount point
                                naming. No FUSE types, builds everywhere.
    fusefs/bridge.go            the VFS side: call serialization, directory
                                cache, lookup, open, spooling. No FUSE types,
                                builds everywhere.
    fusefs/mountspec.go         Spec: a mount request as plain data, plus the
                                argument parsing that produces one. No FUSE
                                types, builds everywhere.
    fusefs/registry.go          the cross-process record of live mounts.
                                No FUSE types, builds everywhere.
    fusefs/cli.go               --mount / --umount / --list-mounts, and the
                                daemon handshake.
    fusefs/platform_unix.go     detaching a child, unmounting somebody else's
    fusefs/platform_other.go    mount, probing a pid.
    fusefs/node_fuse.go         the FUSE side: go-fuse nodes and the server.
                                Built on linux, darwin and freebsd.
    fusefs/node_unsupported.go  everything else: Supported() == false.

The split matters. Only `node_fuse.go` knows that FUSE exists, so the manager,
the cache, the path handling, the command line and their tests compile and run
on Windows too — where nothing can be mounted, but where the code still has to
build.

The FUSE implementation used is [`hanwen/go-fuse`](https://github.com/hanwen/go-fuse).
Outside of its own tests it depends only on `golang.org/x/sys`, which `f4`
already requires, so the dependency tree does not grow.

CGO is not involved anywhere. It is worth being precise about what that does
and does not buy, because "pure Go" and "no external dependencies" are not the
same claim: on Linux a mount still goes through the setuid `fusermount3`
helper unless the process is root (go-fuse can call `mount(2)` itself with
`DirectMount`), and on macOS it still needs the macFUSE kernel extension. What
CGO-freedom buys is that `CGO_ENABLED=0 go build` keeps working and the
cross-compilation matrix in the README stays intact.

## Ownership: a mount is a location, not a session

The central design decision, and the one most likely to be undone by accident
later: **a mount never borrows the VFS instance a panel is browsing.** The
caller resolves a location — a path, a URI, an archive file — into a *fresh*
VFS, and hands that instance over. `MountVFS` owns it from then on and closes
it when the mount ends, including when mounting fails.

This is not defensive style, it is a correctness requirement. Several backends
are stateful in ways that make sharing unsafe:

* `ArchiveVFS.Clone()` returns the receiver itself, because cloning would mean
  extracting everything a second time. A mount that "cloned" the panel's
  archive would share one object with the panel.
* `Close()` on such a VFS starts its temp-file cleanup. When the user walks out
  of the archive, the panel closes it — and a mount holding the same instance
  would be reading a file system that is being dismantled underneath it.

Because the mount source is always a string that can be re-opened, the UI
command and the command line share exactly one resolver, and a mount survives
whatever the panels do afterwards. The cost is one extra connection (or one
extra extraction) per mount, which is the right trade: mounts are long-lived
and rare, panels are not.

`Spec` is that principle written down as a type. It holds a source string and
some flags, and nothing that could not be put in a pipe — which is exactly what
`--daemon` does with it.

## Two process models

Iteration 1 adds a second way for a mount to live, and both are correct for
different things:

* **In-process.** `f4` mounts and holds the mount itself. This is what the
  panels do today, and it is what `--mount --foreground` does.
* **Detached.** `--mount --daemon` re-execs `f4`, and that second process holds
  the mount. Go cannot `fork()`, so the child is told what to do through its
  arguments — rebuilt from the `Spec`, not copied from `os.Args`, so the round
  trip is deterministic and testable — and reports back on inherited fd 3 with
  one JSON line. The parent waits for that line, prints the mount point and
  exits; it does not wait for the mount to end.

The detached model is there because most of the reasons to want a mount outlive
the reason to want a file manager. A crash in the TUI does not leave a wedged
mount point behind, a long `rg` over a mounted archive does not end when the
user quits `f4`, and a mount is not holding an 8 GiB extraction inside the
process drawing the panels.

`UnmountAll()` therefore has to grow a distinction it does not have yet. It is
correct on exit for mounts this process owns; it must never touch a detached
one. That change belongs to iteration 2, when the UI starts creating detached
mounts.

## The registry

A mount started from a shell has to be visible to a mount dialog in a
completely different `f4` process, and unmountable from a third. The registry
is how: a directory of small JSON records, one per mount, under
`$XDG_RUNTIME_DIR/f4/mounts` (with the same per-user temp-dir fallback as
`MountRoot()`), each named by a hash of its mount point so that re-mounting the
same place replaces its record instead of accumulating duplicates. Records are
written to a temporary file and renamed, so a reader never sees half of one.

It is a directory rather than a daemon with a socket because the thing being
described is already a kernel object. The registry is a description of the
world, not the world itself, and it must not become one more component that can
be up or down.

Liveness is judged by the owning process's pid, and stale records are deleted
when they are read. Probing the mount point itself would catch more cases —
a mount whose server was killed with `SIGKILL`, say — but a `stat()` on a
wedged mount can block, and a `--list-mounts` that hangs is worse than one that
is occasionally optimistic.

## The command line

    f4 --mount SOURCE [MOUNTPOINT] [--at DIR] [--ro|--rw] [--allow-other]
                                   [--daemon|--foreground] [--json]
                                   [--timeout DUR] [-o opt,opt]
    f4 --umount TARGET [--json]
    f4 --list-mounts [--json]

`TARGET` may be a mount point, a relative path to one, a record id or the
original source. `MOUNTPOINT` may be omitted, in which case the manager names
it. `--daemon` prints the mount point on stdout and returns, so
`$(f4 --mount x --daemon)` is a usable shell idiom; `--foreground` blocks, and
`Ctrl+C` unmounts rather than abandoning the mount point to a process that is
about to exit.

Exit codes are small and documented, because the audience is shell scripts and
`mount(8)`, not C programs: `0` success, `1` bad invocation, `2` unsupported on
this platform or not implemented yet, `3` failed, `4` busy, `5` no such mount.

When invoked through a `mount.f4` or `mount.fuse.f4` symlink, the argument
form switches to the one `mount(8)` uses — `helper SOURCE MOUNTPOINT -o opts` —
and `--daemon` is implied, so an `fstab` line works. Options that concern
`mount(8)` and not us (`noauto`, `_netdev`, `user`, `x-*` and friends) are
ignored rather than rejected; unrecognised ones are carried through, so an
`fstab` line does not have to be rewritten every time an option is added.

Everything the command line knows about the mount manager itself lives in two
variables at the top of `cli.go`. That is a deliberate seam: renaming something
in `fusefs.go` is a one-line change there, and the tests substitute fakes at
that seam, which is why the entire command line — parsing, the registry, the
daemon handshake, the failure paths — is tested on machines with no FUSE at
all.

## Concurrency: one lock, deliberately

`f4`'s VFS implementations were written for a single UI thread. FUSE serves
requests from many kernel threads at once. The bridge therefore serializes
*every* VFS call through one mutex.

That is a real limitation — one slow read stalls the whole mount — and it is
what iteration 6 is for. It is also the only assumption that holds for every
backend today, and a mount that is slow under parallel load is strictly better
than a mount that corrupts a session under it.

Two things soften it already:

* **Directory cache.** A listing is kept for `DirCacheTTL` (5s by default) and
  answers subsequent `Lookup` calls. Without it, `ls -l` on a remote directory
  would be one round trip per name.
* **Kernel caching.** `AttrTimeout` and `EntryTimeout` (3s by default) let the
  kernel skip most `Getattr` traffic entirely.

## Random access and spooling

Viewer and editor need `ReadAt`, and so does FUSE: the kernel reads at
arbitrary offsets and in arbitrary order. Backends that advertise
`HasRandomAccess` are used directly.

Backends that do not are **spooled**: on open, the file is read sequentially
into a temporary file, the source is released, and all reads are served from
the spool. The alternative — re-reading a stream from the start on every
backward seek — turns a `mediainfo` call into a full re-download.

The spool is unlinked immediately after creation, so it disappears with the
process even on a crash, and it is written while the bridge lock is held: a
sequential backend cannot serve anything else during the transfer anyway.

## What the kernel sees

* **Read-only.** The mount is created with `-o ro` and `Open` refuses anything
  but `O_RDONLY`, so a write attempt fails as `EROFS` — a clear "this file
  system does not do that" rather than a confusing partial success.
* **Inode numbers** are a FNV-1a hash of the VFS path, with the three reserved
  values mapped away. Backends do not have real inode numbers, and the kernel
  only needs uniqueness among live objects.
* **Permissions** come from `VFSItem.UnixMode` when a backend reports one.
  Otherwise files are `0444`, directories and executables `0555`, owned by the
  user who started `f4`. A mount nobody can read would be worse than an
  approximate one.
* **The mount root** is reported as a directory even when the backend cannot
  `Stat` it. A VFS that only lists is still perfectly mountable.
* **`Statfs`** answers with zeroes rather than failing, because `df` and some
  file dialogs treat a failing `statfs` as a broken file system. This has to
  change in iteration 3; see above.

## Mount points

`MountRoot()` is `$XDG_RUNTIME_DIR/f4/mnt`, falling back to a per-user
directory under the system temp dir: a mount point is per-session state, not a
document.

`SuggestMountPoint()` derives a name from the source and keeps the host for
remote locations, so two mounts of `/srv/backups` from different machines do
not read as the same thing in a shell prompt. Collisions get a numeric suffix.

Mount points are created if missing and removed again on unmount, but only if
`fusefs` created them. An existing directory must be empty: mounting over
somebody's files hides them until unmount, which looks exactly like data loss.

## Lifetime

Every mount runs a watcher goroutine on `Server.Wait()`. Whatever ends the
mount — the in-app command, `fusermount -u` from a shell, or the kernel
connection dying — runs the same cleanup exactly once: drop from the registry,
close the VFS, remove the mount point if it was ours, fire `OnExit`.

`Unmount()` does not force. A busy mount fails with `EBUSY`, which is a
question for the user ("something is still in there"), not an error to paper
over with a lazy unmount. The wait for post-unmount cleanup is bounded, so a
wedged remote backend cannot freeze an interactive command.

A mount belonging to another process cannot be ended by an in-process
`Unmount()`, so `f4 --umount` falls back to what any other tool would do:
`fusermount3 -u`, then `fusermount -u`, then `umount` (on macOS, `umount` and
then `diskutil unmount`). `EBUSY` stops that walk immediately rather than
escalating to a more forceful helper.

## Known gaps

Kept here rather than in an issue tracker because each one is a deliberate
decision for now, not an oversight:

* A mount whose server was killed with `SIGKILL` leaves a wedged mount point
  and a record that looks live until the pid is reused. Detecting it needs a
  bounded probe of the mount point; `--list-mounts` must stay non-blocking.
* `--rw` is refused, not implemented (iteration 4).
* Symlinks are shown as ordinary files (iteration 3).
* A daemon child's stderr goes nowhere. Use `--foreground` to see why a mount
  failed for reasons the handshake could not carry.
* Passwords: a detached mount cannot prompt. Backends that need credentials
  have to get them from the existing configuration, and a `--password-command`
  is needed before SFTP mounts are genuinely usable from `fstab` or a script.
  Until then a mount that would prompt must fail fast rather than hang silently.
