# f4 Asynchronous Virtual File System (VFS)

## Overview

The VFS in `f4` is designed to be fully non-blocking. This architecture ensures that the UI remains responsive even when performing operations on high-latency remote systems (SFTP, FTP) or slow storage devices.

## Core Design Principles

### 1. Context-Aware Operations
Every method in the `vfs.VFS` interface accepts a `context.Context`. This allows for:
*   **Instant Cancellation:** If a user navigates away from a directory that is still loading, the background operation is immediately aborted.
*   **Timeouts:** Prevention of UI hangs on stale network connections.

### 2. Streaming Directory Listing
`ReadDir` does not return a complete slice of items. Instead, it uses a callback pattern:
```go
ReadDir(ctx context.Context, path string, onChunk func([]VFSItem)) error
```
As chunks of files are read from the source (e.g., first 100 files from a directory of 10,000), they are immediately posted to the UI thread. The user can start interacting with visible files while the rest are still being fetched in the background.

### 3. The `ErrLoading` Pattern (Reactive Rendering)
For random access operations (used by Viewer and Editor), the VFS and its buffers use a "Try-and-Trigger" approach:
1.  The UI requests a range of bytes.
2.  If the data is not in the local cache, the buffer immediately returns `piecetable.ErrLoading` and triggers a background fetch for that specific chunk.
3.  The UI renders a `[ Loading... ]` placeholder and continues its loop.
4.  Once the data arrives, a `Redraw` is triggered, and the actual content replaces the placeholder.

### 4. Background Indexing
To support features like word wrapping and fast navigation in the Editor, `f4` performs background indexing of line breaks (`\n`). As bytes stream in, a background goroutine scans them and updates the `LineIndex` incrementally.

### 5. Streaming File Search

`vfs.StreamingFileFinder` optionally supplies `FindFilesStream(ctx, dir, query, onFound) error`. Each callback publishes one `FoundEntry` on the search worker; widgets must be updated on the UI thread. `FileFinder` remains available for callers that need a completed slice. Providers without the streaming interface use the regular `ReadDir` walk. A finder may return `ErrFindOptionsUnsupported` before emitting any hit to request that fallback.

`internal/findfile` owns the Alt+F7 walk and result window. It opens before I/O, displays the first hit immediately and batches later UI updates every 50 ms. Cancellation or failure retains partial results. The local optimized walker stats only matching candidates.

`FindProgress.DirectoryTotalKnown`, `TotalDirs` and `CompletedDirs` estimate progress using the root's immediate traversable subdirectories. Each child contributes an equal share after its entire subtree has been visited; inaccessible children count as visited. Exclusions and symlink directories are omitted. Root files are searched before descending, without enumerating the whole tree ahead of time. A successful empty-root search reaches 100%; cancellation and errors do not. Remote finders that cannot supply a reliable total leave it unknown.

## Why this matters for FISH+
This architecture was specifically chosen to support the **FISH+** protocol (see [FISH+.md](FISH+.md)). By allowing operations to be partial, cancellable, and asynchronous, we can offload heavy computations (like searching or indexing) to the remote server while keeping the local `f4` instance lightweight and fast.

## Metadata availability and panel grouping

`VFSItem.KnownMetadata` is an additive `MetadataFields` bit mask. Providers should
set `MetadataExplicit` plus bits for values actually returned by their listing.
An absent bit is then authoritative even if a compatibility field contains a
synthetic value. Bits cover physical size, Unix permissions, UID/GID, Windows
attributes, hidden/executable status and MTime/ATime/CTime. Logical size uses
`SizeKnown`. Known UID/GID 0, permissions 0000 and physical size 0 remain valid.
Without `MetadataExplicit`, legacy nonzero values are inferred conservatively;
ambiguous zero values remain unknown. No link-count field is defined.

The host groups the metadata already in `ReadDir` chunks. It never issues a Stat
per file for grouping. Native listings enrich from the FileInfo already obtained;
on native Windows physical allocation size is unavailable from this listing.
Providers must not mark substitute access/creation dates as real. CTime means
creation on some filesystems and metadata change on others.

## Elevated dispatcher on Windows (f4#1768)

On Unix a root dispatcher, started through `sudo`, answers the file operations a
user was refused; the socket's file permissions are what keep other users off
it. Windows has no such boundary between two programs of one user: UAC asks for
consent once, for f4, and a program that only finds the socket must not inherit
it. The channel for the Windows dispatcher therefore starts with `CmdHello`,
which carries a one-time random token (`NewSudoToken`) known only to the f4 that
launched the dispatcher (`DialElevated`, `ServeElevated` in `vfs/sudo_elevated.go`).
Until it is presented nothing is answered, a wrong token drops the connection
without ending the dispatcher, and the dispatcher serves one authenticated client
and exits when it leaves. Windows has no descriptor passing, so the framing
(`vfs/sudo_frame.go`) carries messages only, bounded to 64 MiB each; file contents
will cross as requests, not as handles.

What is done: the authenticated channel, and the dispatcher itself:
`f4 --elevated-dispatcher <socket> <token>` (accepted only as the whole tail of
the command line) listens on the socket, serves the one client with the token and
exits when it leaves; `LaunchElevatedDispatcher` starts it through
`ShellExecute "runas"`, which is where UAC asks. It serves the path operations
(`ElevatedFileOps`: Stat, ReadDir, MkDir, Remove, Rename, SetAttributes, links)
and file contents in chunks of up to 4 MiB (`CmdReadAt`, `CmdWriteAt`); errors
travel as a code (`SudoResponse.Errno`), not as Windows message text.

On Windows the client is on: when an `OSVFS` operation is refused, `SudoClient`
first asks the user whether to continue as administrator (`Elevation.*`
messages, `confirmElevation`), and only after a yes starts the dispatcher, so
UAC asks. The yes holds while the dispatcher works: after `[Elevation]
IdleSeconds` of `settings.ini` (120 by default; f4:config) without a request
the client closes it (`ElevationIdle`, `closeIfIdle`), the dispatcher exits
with its client, and the next refused operation asks again, as Far does. A
request in progress is never cut: the timer waits for it and starts over. 0
keeps the dispatcher until f4 exits, and so does the box "Do not ask again
until f4 exits" in the question (off by default; `remember`, f4#1768 step
6b). A no is not remembered, like a cancelled sudo password. `Open` and `Create` on this path read and write through
`elevatedFile` and `elevatedWriter` (`vfs/sudo_elevated_file.go`);
`SudoClient.Open`, which hands out a descriptor, is refused up front. A create
that must not replace (`O_EXCL`, the editor's staged sibling when it saves)
makes the empty file through the dispatcher first (`SudoWriteExclusive`), so a
taken name fails at `Create` and the editor's temp-then-rename save works
elevated. Still to come: checks on Windows 10/11 and Server 2019.

On Unix, cancelling an operation while it waits for the sudo password withdraws
the prompt. This also applies to elevated mutations such as delete and rename,
not only directory reads and file opens; withdrawal is not recorded as a
password refusal (f4#1993).

## Copying to the null device (f4#1966)

F5 onto the null device reads every selected file (folders recursively) and
keeps nothing, as in far2l: a quick way to check that the files on a flash
drive are readable. The destination is recognised by `IsNullDeviceDest`
(`internal/fileops/null_device.go`): `/dev/null`, `\\.\nul`, `\\?\nul` (a
trailing separator is fine) everywhere, and the bare `nul` / `nul:` on Windows
only — on other systems `nul` stays an ordinary file name. The copy then runs
against `discardVFS`, which accepts any folder or file below its root and
stores nothing, so the progress dialog, read errors and the "ignore errors"
report work as for any other copy. A move onto the null device is not turned
into a copy: it would delete the sources after "copying" them into nothing, so
F6 is refused up front with a message pointing at F5 (check readability) and F8
(delete). Nothing is asked and nothing is touched: before this, Linux took
`/dev/null` for an existing file, offered to overwrite it and asked for the root
password to replace the device node.
