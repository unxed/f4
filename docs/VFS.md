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

## Elevated dispatcher on Windows (f4#1768, in progress)

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
`ShellExecute "runas"`, which is where UAC asks. Still to come, one part at a time:
the operations it serves, and the prompt in panels and in the editor that offers to
retry a refused operation as administrator. Until the last part lands the Windows build still
reports elevation as unavailable.
