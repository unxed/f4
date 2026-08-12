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

## Layout

    fusefs/fusefs.go            mount manager: Options, Mount, MountVFS,
                                List/Find/Unmount/UnmountAll, mount point
                                naming. No FUSE types, builds everywhere.
    fusefs/bridge.go            the VFS side: call serialization, directory
                                cache, lookup, open, spooling. No FUSE types,
                                builds everywhere.
    fusefs/node_fuse.go         the FUSE side: go-fuse nodes and the server.
                                Built on linux, darwin and freebsd.
    fusefs/node_unsupported.go  everything else: Supported() == false.

The split matters. Only `node_fuse.go` knows that FUSE exists, so the manager,
the cache, the path handling and their tests compile and run on Windows too —
where nothing can be mounted, but where the code still has to build.

The FUSE implementation used is [`hanwen/go-fuse`](https://github.com/hanwen/go-fuse).
Outside of its own tests it depends only on `golang.org/x/sys`, which `f4`
already requires, so the dependency tree does not grow.

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

## Concurrency: one lock, deliberately

`f4`'s VFS implementations were written for a single UI thread. FUSE serves
requests from many kernel threads at once. The bridge therefore serializes
*every* VFS call through one mutex.

That is a real limitation — one slow read stalls the whole mount — and it is
the first thing later iterations should improve. It is also the only assumption
that holds for every backend today, and a mount that is slow under parallel
load is strictly better than a mount that corrupts a session under it.

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
  file dialogs treat a failing `statfs` as a broken file system.

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

`UnmountAll()` exists for shutdown. A mount that outlives `f4` is a directory
that hangs every process touching it, owned by a dead server; unmounting on
exit is not optional politeness.

`Unmount()` does not force. A busy mount fails with `EBUSY`, which is a
question for the user ("something is still in there"), not an error to paper
over with a lazy unmount. The wait for post-unmount cleanup is bounded, so a
wedged remote backend cannot freeze an interactive command.

## Roadmap

The package is deliberately an MVP. In rough order of value:

1. **Writing.** `vfs.VFS` already has `Create`, `MkDir`, `Remove`, `Rename` and
   `SetAttributes`, so `Setattr`, `Create`, `Write`, `Unlink`, `Rmdir`,
   `Rename` and `Flush` are mostly mechanical. The interesting part is not the
   opcodes but write buffering: FUSE writes arrive as many small offsets, while
   `Create` returns a plain `io.WriteCloser`, so a staging file per open handle
   is needed, committed on `Flush`/`Release`. Archive backends should stay
   read-only until that commit path is trustworthy.
2. **Parallelism.** Replace the single bridge mutex with per-backend policy: a
   capability such as `SupportsConcurrentReads`, or a small pool of cloned VFS
   sessions for backends whose `Clone` really is independent.
3. **Standalone mount daemon.** Today `--mount` needs an `f4` process. A
   `--mount --daemon` mode that forks, mounts and reports readiness on stdout
   would make `f4` usable from `fstab`-style automation and from scripts.
4. **Invalidation.** The directory cache is time-based. Backends that can
   notice changes (FISH+ can) should be able to push invalidations, and writes
   through the mount should invalidate their own parent immediately.
5. **Symlinks.** `VFSItem.IsSymlink` is reported by `OSVFS`, but `vfs.VFS` has
   no `Readlink`, so symlinks are currently presented as ordinary files. This
   needs an optional `Readlinker` interface on the VFS side first.
6. **Metadata beyond POSIX.** Extended attributes, Windows attributes and
   `VFSItem.Revision` have no representation yet. `Getxattr` exposing a small,
   documented `user.f4.*` namespace would make `Revision` usable by external
   sync tools.
7. **Progress and diagnostics.** Long spool transfers are invisible. Routing
   them through the existing background-job machinery would make a mount that
   is downloading a 4 GiB file explainable instead of merely slow.
