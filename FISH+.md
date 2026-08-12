# FISH+: Enhanced Remote File Management

## The Concept

**FISH+** is an evolutionary step beyond the classic `fish` protocol (Files transferred over SHell) used in Midnight Commander and far2l. While standard `fish` uses simple shell commands (ls, cat, dd) to simulate a file system over SSH, **FISH+** aims to minimize network traffic and latency by offloading heavy processing to the remote server.

## Architectural Advantages

Traditional remote file systems (SFTP, NFS, SMB) treat the server as "dumb storage," requiring the client to download data to process it. FISH+ treats the server as a "remote worker."

### 1. Remote Search (Server-Side Grep)
Instead of downloading a 1GB log file to search for a string, `f4` sends a search request to the FISH+ handler. The server runs a native `grep`-like process and returns only the byte offsets of the matches. The VFS then fetches only the relevant chunks for display.

### 2. Remote Indexing
Calculating line breaks for a large file is expensive over a network. With FISH+, the server-side script calculates the `LineIndex` locally and sends the array of offsets to the client. This allows `f4` to open a multi-gigabyte file over SSH and allow instant jumping to the end or middle of the file.

### 3. Delta-Based Editing (Sparse Saving)
The `PieceTable` model used in `f4` is essentially a list of edit instructions (insert X at Y, delete range A-B).
*   **Classic SFTP:** To save a 1-byte change in a 100MB file, you must re-upload the entire 100MB.
*   **FISH+:** `f4` sends only the `PieceTable` deltas — the ranges of the file that are unchanged and the bytes that are new. The remote host assembles the result from its own copy and the few new bytes, and the editor renames it over the original, the way it already saves a local file. Patching in place would spare the second copy but only works while the length does not change, and leaves a half written file behind when a connection drops; making that a setting is for later.

### 4. Background Offloading
Operations like calculating directory sizes, finding duplicate files, or complex pattern matching are executed as remote background tasks. The FISH+ VFS reports progress back to the `f4` Progress Dialog without saturating the network with raw file data.

## Code Layout

*   `plugins/netfox/fishplus/` — the protocol client. It depends on the standard library only and knows nothing about SSH: it talks to any duplex byte stream, which makes it testable against a local `/bin/sh`.
    *   `helper.sh` — the shell helper that is uploaded to the remote host on every connect.
    *   `script.go` — embedding, token substitution and compaction of the helper.
    *   `session.go` — handshake, request/response framing, feature detection.
    *   `fs.go` — listing and metadata parsers for every backend.
    *   `read.go` — ranged reads and the cached file handle.
    *   `write.go` — ranged writes, truncation and the buffering write handle.
    *   `mutate.go` — directory and metadata mutations.
    *   `search.go` — the remote search and the remote line index.
    *   `patch.go` — assembling a file out of pieces of another one.
    *   `job.go` — background jobs, and the tree scan that is the first of them.
*   `plugins/netfox/fish_vfs.go` — the `vfs.VFS` implementation, registered as the `fish+` protocol of the NetFox connection manager. A plain `fish` type is accepted as a synonym.
*   `plugins/netfox/ssh_dial.go` — the SSH dialer shared with the SFTP backend.
*   `plugins/android/` — an ADB smart-socket, shell-v2 and Sync client. Its Android drive first carries this same FISH+ session over a raw ADB shell; a device whose userspace cannot provide complete FISH+ listing/read/write modes uses ADB Sync for that whole panel session instead.

### Licensing

The helper script and the wire format are written from scratch for f4 (BSD-3-Clause). Nothing is copied or adapted from far2l or Midnight Commander, which are GPL licensed. Their fish implementations are used as a source of ideas only.

## Wire Protocol, Version 1

### Session bootstrap

The client starts a plain POSIX shell on the remote host and then keeps talking over that very same pair of streams. When the client closes its side, the helper's `read` hits EOF and the remote shell exits on its own — no farewell command is needed, so a hung remote can never block the UI thread.

Getting the helper there takes two steps, and the reason is worth writing down. A shell parses its script from the same stream the requests arrive on, and it does not read that stream a byte at a time: `dash` fills a buffer, and whatever lands in the buffer past the end of the script is parsed as part of it. Send the script and a request together and the request is executed as a shell command — `1: not found` — while the client waits forever for an answer. `bash` reads byte by byte and never showed this, so with `/bin/sh` linked to `bash` everything looked fine; with `dash`, which is `/bin/sh` on Debian and Ubuntu, it was a race that the network usually won and a fast link would not.

So the script is not parsed off the stream at all. What the shell parses is one line, the bootstrap, which prints a marker and then reads the script in through the shell's own `read` builtin, ending at `F4EOF`, and `eval`s it. `read` takes its bytes from the file descriptor and cannot run ahead of itself. The client sends that line, waits for the marker, and only then sends the script, so nothing is ever in flight while the parser is working. The marker carries the session token and is printed in two pieces, `F4R"DY"`, so that a terminal echoing the line back cannot be mistaken for the shell answering.

A shell that ended up on a pseudo terminal needs one more step. A terminal echoes back everything it is fed, turns every `\n` on the way out into `\r\n` — which destroys binary frames — and in canonical mode truncates an input line at a few kilobytes, which would cut off a long path. So the helper checks whether its stdin is a terminal and, if it is, puts it into a transparent mode with POSIX `stty` operands only, announcing `tty` among its features. A client whose transport is a terminal and which does not see `tty` in the banner must treat binary payload as unsafe.

A binary-clean transport may instead send the complete helper as base64 inside the first command. The command verifies a token-bearing sentinel after decoding before it calls `eval`, tries the common GNU/BusyBox/toybox, BSD and OpenSSL decoder spellings, and creates no temporary file. Android shell-v2 uses this form because assembling the helper one shell `read` at a time is measurably expensive there; if no decoder accepts it, the shell returns a framed handshake error and Android retries the portable two-step form on a fresh stream.

### Request

One line per request, optionally followed by one line per path the command works on:

    <id> <cmd> [<short arg> ...]
    <path>

`id` is a decimal counter that starts at 1 and never repeats within a session. Short arguments are bare tokens: they must not be empty and must not contain whitespace, which is why every path travels on a line of its own, read by the helper with `IFS= read -r`. A path therefore reaches the remote host byte for byte — spaces, leading and trailing ones included, tabs, backslashes, quotes, dollar signs, any encoding.

Only a path that a line based channel cannot carry — one containing a newline, or one starting with the escape marker itself — is base64 encoded and prefixed with `~`. That keeps the classic fish trade-off the right way round: raw whenever possible, base64 only where nothing else works, so the remote host does not fork a decoder per request.

### Response

Zero or more payload lines, then a terminator:

    .<token> <id> ok|err [message]

`token` is 64 bits of randomness generated by the client for each session and substituted into the helper before upload. That is what makes the terminator unambiguous: output of `ls`, `stat` or `grep` cannot accidentally look like one.

Binary payload is sent in frames, each being a header line followed by exactly `n` raw bytes:

    #<n>

Frames are only recognized for commands the client issued as data commands (`ExecData`), so a text payload line starting with `#` stays a text line.

### Handshake

The helper answers with a terminator carrying the reserved id `0`:

    .<token> 0 ok FISHPLUS 1 dd base64 readlink du grep sed awk wc sha256sum stat find

Everything printed before that line (motd, shell warnings, login banners) is discarded by the client. Such noise does not always end with a newline, and neither does the echo of the uploaded script on a pseudo terminal, so during the handshake — and only there — the client looks for the terminator anywhere in the line rather than at its start, while the helper prints a newline of its own before the banner. The word list after the version is the set of features the helper detected on the remote host; later steps pick their strategy from it (`stat` vs `statbsd` vs `find` vs plain `ls`, `dd` availability and so on). A failing host answers `err` with a reason instead.

### Commands implemented so far

*   `noop` — cheapest possible round trip.
*   `pwd` — the directory the remote shell started in.
*   `ping` + payload line — echoes the payload back; keepalive and synchronization check.
*   `feats` — repeats the version and feature list.
*   `enum` + path — lists a directory.
*   `isdirs <count>` + `count` paths — reports in one round trip which paths resolve to directories; directory listings use it to classify symlink targets without one `info` request per link.
*   `info` + path — metadata, following symlinks.
*   `linfo` + path — metadata of the link itself.
*   `rdlink` + path — the target of a symlink.
*   `read <offset> <length>` + path — a byte range, `length` zero meaning "to the end of the file".
*   `write <offset> <length> raw|b64` + path + payload — the same range in the other direction; the payload follows the path line and is described below.
*   `trunc <size>` + path — sets the size of a file, creating an empty one where there was none.
*   `patch <nsegs> raw|b64` + two paths + one line per segment — builds the second path out of ranges of the first and of new bytes, the literals following their descriptors.
*   `mkdir` + path — creates a directory and its missing parents.
*   `rm`, `rmdir`, `rmtree` + path — a file, an empty directory, a whole tree.
*   `mv` + two paths — the first command that reads more than one path line.
*   `mklink` + two paths — creates a symbolic link at the first path pointing at the second. Only the first is guarded: the target is not a path on the remote host but a string to store in the link, and a relative target, one that does not exist yet, or one containing `..` are all ordinary. A link path that already exists is refused rather than replaced, because `ln -s TARGET DIR` creates the link inside `DIR` instead of failing.
*   `chmod <octal>` + path — the mode is checked for being octal before it reaches the remote `chmod`.
*   `chown <uid> <gid>` + path — either half may be `-`, meaning "leave it alone".
*   `utime <mtime> <atime>` + path — epoch seconds, either of them `-` for "leave it alone".
*   `grep <mode> <limit>` + pattern + path — byte offsets of the matches, at most `limit` of them.
*   `lidx <first> <count>` + path — where the given lines start, and how many lines the file has.
*   `ffind <limit> <nmasks> <grep mode>` + a directory, one line per mask and, unless the mode is `-`, a pattern — every file in the tree whose name matches a mask, in the listing format with the full path in place of the name.
*   `jstart hash 1` + a directory — a job that hashes the files of a tree that could have a twin, reporting `H <hash> <path>` for each.
*   `jstart exec 2` + a directory and a command line — a job that runs the command where the files are and reports what it printed, line by line.
Every mutation refuses a path that is not absolute or that carries a `..` component, and the root directory itself. The client always sends absolute paths, so the rule costs nothing in normal use; it is there because `rmtree` turns one mistake in path assembly into a lot of lost data, and because a check the remote host performs holds even when the client is the thing that went wrong. A name that merely begins with dots is not a `..` component and stays usable.
*   `jstart <kind> <npaths>` + that many paths — starts a detached job and answers with `J <id>`; the only kind so far is `scan`, which takes one directory.
*   `jpoll <id> <limit>` — the state of a job as `S <state> <exit> [message]`, followed by at most `limit` of the lines it produced since the previous poll.
*   `jkill <id>` — stops a job, keeping what it produced.
*   `jdrop <id>` — stops a job and removes every trace of it; dropping one that is not there succeeds.
*   `jlist` — one line per job of this session: `<id> <state> <kind>`.
*   `mode <name>` — forces a metadata backend instead of the auto-detected one; for tests and for troubleshooting.
*   `rmode <name>` — the same for the read backend.
*   `wmode <name>` — the same for the write backend.
*   `exit` — makes the helper leave its loop.

### Metadata backends

Listing is where remote hosts differ the most, so the helper probes what is there and reports the winner as `mode:<name>` among its features. In order of preference:

1.  **find** — `find -H DIR -mindepth 1 -maxdepth 1 -printf '%y %Y %s %T@ %A@ %C@ %m %U %G %f\n'`. One process for the whole directory, no shell glob, hidden entries included, sub-second timestamps, and `%Y` tells for free whether a symlink resolves to a directory. GNU only.
2.  **stat** — `stat -c '%f %s %Y %X %Z %u %g %n' -- DIR/* DIR/.*`. Needs a shell glob, so a huge directory can hit the argument limit, and the type of a symlink target stays unknown until someone asks for it.
3.  **statbsd** — the same shape with `stat -f '%p %z %m %a %c %u %g %N'` for BSD and macOS.

4.  **ls** — the last resort, `ls -lan` with a full timestamp, in whichever of three dialects the host has: `--time-style=+%s`, `--full-time`, or BSD's `-T`. The ids are asked for numerically and the timestamp in full for the same reason: with them the number of fields before the name is fixed, so everything after them is the name, spaces and all. Without them a user name with a space in it and a date without a year make the line a guess. The dialect travels in the mode marker — `M ls epoch`, `M ls iso` or `M ls bsd` — since nothing in the line itself says which.

    Which one it is is decided by looking at the output, not at the exit status. BusyBox accepts `-T` and prints the short format anyway, so a host that answered "yes" to the BSD question would have had every date read as a year and a month that were not there. The probe asks for `-d .`, counts the fields and looks at the one where the timestamp should be.

The first payload line of a listing names the backend that produced the rest, so the client always knows which parser to use — including when the backend was switched at runtime.

### Reading

`read` answers with a text line `S <size>` carrying the size the remote host saw, then one binary frame with the bytes that were actually available in that range. The size travels along because a client following a growing log file needs it anyway, and because it is what the helper used to decide how much to send.

Which tool does the reading is detected as well, and reported as `read:<name>`:

1.  **ddbytes** — `dd bs=1M iflag=skip_bytes,count_bytes skip=OFF count=LEN`. Any offset, any length, one process. GNU and recent BusyBox only.
2.  **dd** — plain `dd bs=BS skip=OFF/BS count=LEN/BS`, where `BS` is the largest power of two up to 64k that divides both the offset and the length. A client that asks for chunk aligned ranges — which `File` does — therefore gets whole block reads and a single `lseek` on BSD, macOS and Solaris too, without the GNU only `iflag`.
3.  **tailc** — `tail -c +OFF | head -c LEN`, for hosts with no usable `dd`.
4.  **cat** — whole files only; a byte range is refused rather than answered with the wrong bytes.

The offset is clamped against the size the helper just read, so the frame length is exact and the stream stays in sync.

### Writing

The payload of a `write` follows the path line and carries no terminator of its own: the helper reads exactly as many bytes as the request announced. One byte too few or too many and the remote shell parses the rest of a file as commands, so the entire design of this step is about who counts those bytes and what happens when the count cannot be kept.

Which tool does the counting is detected during the handshake and reported as `write:<n>`:

1.  **ddbytes** — `dd bs=1M iflag=fullblock,count_bytes count=LEN oflag=seek_bytes seek=OFF conv=notrunc`. One process, any offset, any length; `fullblock` is what makes it stop on the exact byte while still reading whole blocks, because a short read from a socket must not end the copy. GNU and recent BusyBox only.
2.  **b64** — the client sends a single line of base64 and the helper reads it with the shell's own `read`, which cannot overshoot: a line ends where its newline is. Positioning is then a plain `dd` on the largest power of two dividing both the offset and the length, the same arithmetic the read path uses. It costs a third more traffic and is still the better choice on a host without GNU dd.
3.  **ddbs1** — `dd bs=1 count=LEN seek=OFF conv=notrunc`, exact because a one byte read cannot come back short, but a syscall per byte. It is never picked automatically; `wmode` selects it for a host where the other two misbehave.

A range starting past the end of the file leaves a hole, and nothing after the written range is touched — which is what the delta based saving of step 8 needs.

`trunc` sets a size. Zero is a shell redirection and therefore works everywhere, including on a file that does not exist yet; any other size needs the `truncate` utility, announced as the `truncate` feature. `Create` truncates to zero and then writes forward, so the common case never depends on it.

#### When a write fails

A refusal the helper can see coming — a directory, a path it may not write, a bad range — is answered only *after* the payload has been read into `/dev/null`, because a failed request must still leave the stream where the next request expects it. Such a reply carries a `D` line and the client treats the session as healthy.

A write that dies in the middle — the disk fills up, the medium fails — leaves an unknown number of bytes on the wire, and no amount of guessing on the client side can recover them. There is no `D` line then, and the client marks the session broken so that it gets reconnected instead of quietly writing the rest of the payload into the next file. Step 10 is where a broken session learns to resynchronize instead.
### Ownership and timestamps

`chown` is the easy half: a numeric `uid:gid` pair, either side of it droppable, and one utility that behaves the same everywhere.

Timestamps are not. GNU `touch` takes an epoch as `-d @1400000000`; BSD and macOS have never accepted that and want `-t YYYYMMDDhhmm.SS` — in the *local* time of the host, which is exactly the one thing the client cannot compute for it. So the conversion happens on the remote side: the helper tries GNU form first, and if it is refused it asks the host's own `date` to render the epoch, `date -r` for BSD and `date -d @` for GNU, and feeds the result to `touch -t`. The `date -r` attempt is skipped when a file with that name exists in the current directory, because GNU `date` reads `-r` as "reference file" and would then quietly report that file's time instead.

Setting both times is one `touch`; setting them to different values is two, `-m` and then `-a`, because that is all POSIX `touch` offers. A caller that wants neither pays no round trip at all.
### Remote search

This is the first command that is FISH+ rather than fish: the remote host does the work and only the answer travels. `grep -a -b -o` prints one match per line as `offset:text`, and an `awk` behind it throws the text away and stops after the limit, so a pattern matching a million times costs the same handful of bytes on the wire as one matching three times — and grep dies on the broken pipe instead of reading the rest of the file.

The mode argument is `f` for a fixed string or `e` for an extended regular expression, with an `i` appended to fold case. Pattern and path both travel on lines of their own, escaped the same way as any path, so a pattern with spaces, tabs or a leading `~` arrives byte for byte.

`FishVFS.Search` uses it with a fixed pattern and announces `HasSearch` accordingly. A host without `grep` or without `awk` answers `nil`, which is f4's way of saying "search it yourself by reading" — the same fallback SFTP gets.
### The remote line index

Browsing a huge remote file in pieces already works: the viewer renders from a byte offset and asks the VFS for what it needs through `ReadAt`, so a panel opens the head of a gigabyte log instantly. What does not work that way is anything that has to know where the *lines* are — jumping to the end, jumping to line number N, showing how many lines there are — because finding that out means walking the file, and walking it over the network means downloading it.

`lidx` moves that walk to the far side. One `awk` pass prints the byte offset of each requested line and, at the end, `T <total>`. What crosses the network is a handful of numbers no matter how large the file is. The offsets are byte offsets because that is the only currency the rest of the protocol speaks: feed one straight back into `read`, and the viewer is drawing that line.

The pass is a full one — awk cannot know where line N starts without counting the newlines before it — so the cost is one sequential read on a machine that has the file locally, instead of one transfer across the network. That trade is the whole idea of FISH+.
### Background jobs

Everything above answers within one round trip, or at least within one a user
is willing to sit through. Summing a directory tree is not like that: it takes
as long as the remote disk takes, which on a large export is minutes. As a
command it would freeze the panel for its whole duration, and the only way out
would be to drop the session, because a request cannot be abandoned halfway
without desynchronizing the stream.

So the work is detached instead. `jstart` forks a subshell with all three of
its standard streams pointed away from the wire, writes its output into a file
and answers with a job id; `jpoll` brings back whatever accumulated since the
previous poll; `jkill` stops it and `jdrop` forgets it. The session is idle
between polls, and that is what makes a job cancellable at all: cancelling
means the client stops asking, so it never has to interrupt a request in
flight.

Three details are what hold it together.

*   The request carries its own path count, `jstart <kind> <npaths>`. A helper
    that rejected an unknown kind before reading its path lines would leave
    them in the stream, and the next request would be parsed out of somebody's
    directory name. The refusal reads them first and complains afterwards,
    which is the rule a refused `write` already follows with its payload.
*   A poll reports the state *before* it reads the output. The other order
    loses data: a job that finishes between the two reads is then reported as
    done in the very poll whose output was collected a moment earlier, and the
    lines written in between are never delivered.
*   Only whole lines are handed out, which is exactly what `wc -l` counts, so
    a progress line that is still being written waits for its newline instead
    of arriving in two halves.

The job directory lives under `$TMPDIR`, `/tmp` or `$HOME`, whichever is
writable first, and is created when the first job starts rather than at
connect, so a session that never runs one leaves nothing behind. The session
token is part of its name because a pid alone repeats often enough for a new
session to adopt the leftovers of an old one. When the request loop ends —
which is what the client closing its side does — everything still running is
killed and the directory removed, and a trap covers a connection that was cut
rather than closed.

### The remote tree scan

The first job kind is `scan`: the bytes, the files and the directories of a
whole tree, counted by the side that has the disk. One `find` feeds one `awk`,
and what crosses the network is a progress line every two thousand entries
plus a total, no matter how many millions of files there are.

The three listing backends differ only in how the type of an entry arrives — a
type letter from GNU `find -printf`, a hexadecimal mode from GNU `stat`, an
octal one from BSD `stat` — and both spellings of a mode begin with a 4 for a
directory and for nothing else, so one awk rule serves all three. A symlink is
counted as the leaf it is and never followed, which also means a link pointing
at its own parent cannot make the walk grow forever.

Progress needs the remote awk to flush on demand: output to a file is block
buffered, and the first report would otherwise arrive four kilobytes late. An
awk that does not know `fflush` fails to parse a program containing it rather
than skipping the call, so the helper probes for it during the handshake and
builds the program without it where it is missing. Such a host reports its
total and nothing before it.

`Client.Scan` starts the job, follows it, and cancels it on the way out.
Cancelling is a policy rather than a mechanism: `StartScan` and `FollowScan`
underneath kill nothing, so a job meant to outlive the dialog it was started
from is built out of those two. The choice is deliberate. Nobody has done this
before us, so there is no convention to inherit, and the predictable rule is
the one where what the user sees matches what is happening: a dialog that is
gone must not leave a `find` running on somebody else's server with nothing to
notice it by. That makes "cancel" and "send to the background" two different
actions rather than one whose meaning has to be guessed — which is what step
9d will give two different keys, and what a setting can then choose between.

The polls themselves run on a context of their own. A request cancelled while
its answer is on the wire desynchronizes the session, which is the standing
limitation of v1, so `FollowScan` lets the poll it already sent come back and
notices the cancellation between two polls. It costs one round trip and buys a
session that is still usable afterwards, which is the whole reason for making
the scan a job.

### Traps, and how they were found

Three defects here were invisible on the machine they were written on and
would have surfaced on a user's. They are worth knowing before touching the
helper again, because each of them is a property of POSIX shells rather than
a mistake in the logic.

**A shell reads ahead of itself.** The first version sent the script and the
first request down one stream, which works right up until it does not: `dash`
fills a parse buffer, and a request that has already arrived when the parser
refills is parsed as part of the script and executed as a command. `bash`
reads byte by byte and never showed it, so a machine whose `/bin/sh` is
`bash` sees nothing wrong, while Debian and Ubuntu, where it is `dash`, race
on every connect and lose the race whenever the link is fast enough. Hence
the two step bootstrap: what the parser sees is one line, and the script
comes in through `read`, which cannot run ahead.

**A failed redirection on a special builtin kills the shell.** `: > "$file"`
looks like the cheapest way to create or truncate something, and when the
redirection fails POSIX says a non-interactive shell shall exit — `:` being a
special builtin. The session dies with no answer rather than reporting an
error. Wrap such a redirection in a command substitution, as `trunc` and
`patch` both do: the fatal exit then belongs to the subshell. The same holds
for `.`, `eval`, `exec`, `set`, `shift` and the rest of that list.

**Tools disagree about when to stop reading.** macOS `head -c` writes the
bytes it was asked for and then drains the rest of its input, so it can never
be used on a stream something else still has to read from; that is why the
write path uses `dd` and why the helper reports `headc` and `headsafe`
separately. `openssl base64 -d` decodes nothing at all unless its input ends
with a newline, which made it useless as a fallback until the decoder started
terminating what it feeds in.

**A background job outlives the shell that started it.** Killing a job means
killing the subshell wrapping it, and the tool doing the actual work is that
subshell's child. A POSIX shell without job control leaves background commands
in its own process group, so there is no group to aim at that would not take
the session down as well. What is left is `pkill -P`, and the order matters:
kill the wrapper first and its children belong to init, with nothing left to
select them by. Children first, then the wrapper. On a host without `pkill`
the tool runs on until it finishes, writing into a file that has already been
deleted — which is why a dropped job has its directory removed rather than
emptied.

The first two were found by a test harness that speaks the wire protocol to a
real shell with no network in between. Latency hides both: over ssh the shell
almost always wins the race and the failure looks like a rare hang. A test
that runs everything locally and immediately loses the race nearly every
time, which is exactly what makes it useful.
### Known limitations of v1

*   The remote host must provide a base64 decoder (`base64` or `openssl`), even though almost no request needs one: the handshake refuses hosts without it because a path with a newline would otherwise be unreachable. Dropping that requirement for hosts that never see such a path is possible later.
*   A file truncated between the moment the helper reads its size and the moment `dd` runs makes the frame shorter than its header promises, which desynchronizes the session. Growth is harmless; truncation under the reader is not, and detecting it costs a second pass over the data.
*   The helper does its size arithmetic in the shell, so a host whose shell has 32 bit arithmetic cannot address past 2 GB. `tools/fishplus_probe.sh` reports this per host.
*   A listing carries file names on a line each, so a name containing a newline shows up truncated in a directory listing, exactly as in classic fish. Operating on such a file still works, because paths going the other way are escaped.
*   Cancelling a request while its answer is being read costs the rest of that answer: the client reads forward to the terminator to find the next request boundary, so an Escape during a large read waits for the bytes already in flight. Past `DrainAfterCancelTimeout` it gives up and marks the session broken instead.
*   Two panels sharing one session — which is what `Clone` gives them — take turns rather than working in parallel, because the remote shell answers one command at a time. A cancellation in one of them no longer breaks the session for the other, but it does make the other wait for the answer being drained.
*   `SetAttributes` follows the SFTP backend: a `Uid` or `Gid` of -1 means "leave the ownership alone", and a zero-valued `VFSItem` therefore asks for `chown 0:0` rather than for nothing. Callers with nothing to change have to say so with -1, which is what the copier does.
*   In the `stat` and `statbsd` backends a directory is listed through a shell glob, so a directory with very many entries can exceed the argument limit, and a symlink is reported without the type of its target until the user enters it.
*   The `ls` backend needs a full timestamp, which means `--time-style`, `--full-time` or BSD's `-T`. A host with none of the three cannot be listed, because the short form drops the year and no amount of parsing gets it back. No reported host is in that position: BusyBox has `--full-time`, the BSDs have `-T`, and illumos turned out to have both GNU `stat` and `--time-style`.
*   The BSD `ls` dialect is the only one of the three without a zone in it. The `iso` and `epoch` dialects are exact wherever the host is.
*   In the `ls` backend a tree search and the hash job refuse rather than answer: both walk with `find`, and a host that has no usable `find` has none to walk with. The client falls back to walking the tree itself for the search.
*   The BSD `ls` timestamp carries no time zone, so it is read in the client's zone. A host in another zone shows times off by the difference, in that backend only.
*   A host without `dd` cannot be written to at all, and the client refuses to send a payload it knows the remote side cannot take off the wire.
*   The `b64` backend puts a third more bytes on the wire and makes the remote shell read them one syscall per byte, which is why the client sends it smaller chunks than a raw backend.
*   Shrinking a file to a non-zero size needs the `truncate` utility; without it only truncation to zero is possible.
*   A raw payload assumes a binary safe transport. The ssh backend asks for no pseudo terminal, so this holds there; a caller supplying its own terminal backed stream has to select `wmode b64` by hand for now.
*   A host with a `touch` that takes neither `-d @epoch` nor `-t`, or with no `date` able to render an epoch, cannot have its timestamps set. The helper says so instead of writing the wrong time.
*   Times are set with a resolution of one second, because that is what `touch -t` carries. The sub-second part a listing reports is therefore lost on a copy.
*   `chown` follows symlinks, so setting the ownership of a symlink changes its target instead. Doing it the other way round needs `chown -h`, which not every host has.
*   `mklink` needs `ln`, which the banner announces as a tool like any other. A host without it cannot have links created on it, and the client refuses instead of trying. Because `ln` entered the probe list together with the command, a banner listing it also means a helper new enough to know `mklink`, which is what lets one feature answer both questions.
*   A symbolic link is created with the ownership of the account the helper runs as, and its own timestamps are not settable: `touch` follows a link and would stamp the target instead. A copy therefore reproduces where a link points, not when it was made.
*   A search is capped at 10000 matches and the client cannot tell a file with exactly that many hits from a truncated answer. A count line would fix it and costs a second pass over the file, so it waits for a case that needs it.
*   Case folding and regular expression dialect are the remote `grep`'s, not Go's, so a search over a FISH+ panel can match slightly differently than the same search over a local one. That is the price of not moving the file.
*   `grep` cannot match across a line break, so a pattern containing a newline finds nothing. The viewer's search will have to split such a pattern itself.
*   `lidx` reads the whole file to answer, so on a slow remote disk the first jump to the end of a very large file takes a while, even though nothing is transferred. The viewer keeps the line total against the file size it was counted at, so paging around a file that is not growing pays for one pass rather than one per keystroke.
*   Going to a line on a file system without a line index falls back to scanning, which for a remote host means reading the file. It is bounded by the user cancelling the task, not by anything cheaper: the alternative would be refusing the command outright on a host without `awk`.
*   A `patch` needs somewhere to build the result, so a file cannot be edited in place and the remote host briefly holds two copies of it. Renaming the result over the original also replaces it rather than writing through it, so hard links to the file are left pointing at the old content, exactly as with a local save through a temporary file.
*   The delta save applies only to a file loaded as raw UTF-8. With any other codepage the buffer holds decoded text whose offsets say nothing about the bytes on disk, and the file is written out in full.
*   A tree search matches names the way the remote `find -name` does, which is `fnmatch` rather than Go's `filepath.Match`. The two agree on `*`, `?` and simple classes and can differ on the corners, which is the same trade the remote `grep` makes for content.
*   A tree search reports a symlink without resolving it, because resolving would cost a round trip per hit and give back what the single request saves.
*   A raw payload assumes a binary safe transport. The ssh backend asks for no pseudo terminal and the helper tames one with `stty` where it finds it, announcing `tty`; a caller whose stream is terminal backed and which does not see `tty` in the banner has to select `wmode b64` by hand.
*   A line index counts `\n` only. A file with classic Mac line endings is one line to `lidx`, exactly as it is to `awk`, `grep` and `wc` on the remote host.
*   A job's output is re-read from its beginning on every poll, so polling gets slower the more a job has printed. A scan prints a handful of lines, so nothing meets that limit yet; a job kind that streams results will need a byte offset instead of a line count.
*   Killing a job on a host without `pkill` stops the wrapper but not the tool it started, which runs to its end. Its output goes into a file that has already been removed, so nothing is corrupted and nothing is reported.
*   A job left behind by a connection that was cut is killed by the helper's trap, but a host that killed the shell outright keeps both the job and its directory until somebody cleans up `/tmp`.
*   The scan reports its position as the last entry `find` reached, so a name containing a newline shows up truncated there, exactly as it does in a listing.
*   A scan counts a symlink as a leaf, while f4's own scanner walks a symlink that resolves to a directory. The two therefore disagree on a tree full of directory symlinks, and the remote numbers are the ones `find` and far2l would report.
*   A running scan polls the session, so a second panel sharing it waits for the current poll before its own request goes out. The polls back off to three quarters of a second when a job has nothing new to say, which is what keeps that wait from being noticeable.
*   A reconnected session is a new shell. It knows nothing of what the old one was doing: its background jobs are gone, a `write` or a `patch` that was in flight cannot be resumed because nobody knows how many bytes arrived, and the remote working directory is asked for again rather than assumed. What survives is what lives on this side — the path a panel stands in, a read handle, and the credentials.
*   The credentials therefore stay in memory for as long as the panel is open, since a session that has to authenticate again has to have something to authenticate with. They came from a site configuration on disk, so nothing new is being kept secret; the alternatives are in `IDEAS.md`.
*   A job that died with its session is marked lost in the registry rather than restarted. A scan that began again from the beginning would report numbers the user never asked for, and a job whose session cannot be rebuilt at all is never told, so it sits in the list until it is dismissed by hand.
*   Only the panel's directory listing offers a reconnect. Everywhere else — the viewer, the editor, a copy — a lost session is still reported as an error, and the panel behind them is what has to be reconnected before they are tried again.

## Roadmap

### Done

*   **Step 1 — transport and protocol core.** Helper script, handshake, feature detection, request/response framing, base64 arguments, binary frames, error reporting, session teardown. Tested both against an in-memory peer and against a real local shell.
*   **Step 2 — listing and metadata.** `enum`, `info`, `linfo`, `rdlink` and the runtime `mode` switch, with the find, GNU stat and BSD stat backends and their parsers. Paths now travel raw instead of base64. The integration test drives every backend the test machine provides, over names with spaces, tabs, backslashes, trailing blanks and non-ASCII characters.
*   **Step 3 — reading.** `read` with an offset and a length, raw binary frames with no base64 in the way, four read backends with runtime switching through `rmode`, and a `File` handle with a chunk cache that satisfies `vfs.ReadAtCloser`. The terminal safeguards and the first round of compatibility fixes from issue #316 landed here as well.
*   **Step 4a — the VFS.** `plugins/netfox/fish_vfs.go` maps `Entry` onto `vfs.VFSItem` and a `fishplus.File` onto `vfs.ReadAtCloser`, so a FISH+ session is already a browsable and readable file system. The helper learned `pwd`, so a panel opens where an interactive login would land. Mutations answer with a plain error until step 5, and the test drives the whole mapping over a local shell.
*   **Step 4b — transport and registration.** `DialSSH` now carries the agent, key and password logic for both SSH backends, and a FISH+ site opens a shell with no pseudo terminal attached, running `exec /bin/sh` so that a csh or fish login shell cannot get in the way. The protocol is registered as `fish+`, with the plain `fish` type accepted as a synonym.
*   **Step 5a — mutations.** `mkdir`, `rm`, `rmdir`, `rmtree`, `mv` and `chmod`, and the `FishVFS` methods on top of them. A recursive delete is one round trip rather than one per entry, because the remote host does the walking. `Create` is now the only method that still refuses.
*   **Step 5b — writing file content.** The `write` command with an offset and a length, three write backends with runtime switching through `wmode`, `trunc`, and a buffering `Writer` on the client side. A refused request still drains its payload and says so with a `D` line; a write that fails halfway does not, and marks the session broken rather than letting it desynchronize.
*   **Step 5c — the writing VFS.** `FishVFS.Create` on top of `Writer`, plus `chown` and a `touch` that works on GNU and on BSD alike by letting the remote host convert the epoch into its own local time. `SetAttributes` now carries mode, ownership and timestamps, so a file copied onto a FISH+ panel arrives with the attributes it had. Nothing in the VFS refuses any more, and `ErrFishReadOnly` is gone.
*   **Step 5d — a `Close` whose error is not dropped.** `closeOnce` lets the places that write through a VFS close explicitly where the error matters and still keep a `defer` as a safety net. `recursiveCopy` now closes the destination before declaring success, so a failed last chunk removes the incomplete file instead of reporting a copy that did not happen; the download to a temporary file, the upload back after an external editor and the editor's own save report it too. None of this is specific to FISH+, it affects every file system that buffers.
*   **Step 7a — remote search.** The `grep` command, `Client.Grep` and `FishVFS.Search`, with `HasSearch` finally true for one of f4's file systems. Only byte offsets cross the network, and the limit is enforced on the remote side rather than by disconnecting a flood.
*   **Step 7b — the remote line index.** The `lidx` command and `Client.Lines`: the offsets of a range of lines and the total line count, from one remote `awk` pass and a few numbers on the wire.
*   **Step 7c — the viewer on top of it.** `vfs.LineIndexer` is an optional interface, so a local file system and an archive are not made to carry a method they cannot answer; `FishVFS` implements it over `Client.Lines`, and `ViewerBackend` asserts for it. What it buys is the jump to the end of a file: instead of reading back up to a megabyte and scanning all of it, the viewer asks where the last screenful of lines begins and reads exactly that. The line total is cached against the file size, so paging around a log that is not growing costs one round trip. Everything else the viewer does is byte based and needed no index at all.
*   **Step 7d — the file search.** The `ffind` command, `Client.Find` and `vfs.FileFinder`, another optional interface. One request walks the whole tree on the remote host and, when the user asked for a text, greps the candidates in the same `find` pass, so a file is never downloaded only to be rejected. `ExecuteFindFile` uses it when the file system offers it and walks the tree itself otherwise, which is what a host without `find` or without `grep` falls back to.
*   **Step 7e — go to line.** Alt+F8 in the viewer asks for a line number and `ViewerBackend.LineStart` finds it: through `vfs.LineIndexer` where the file system has one, by scanning otherwise. In hex mode the same key asks for a byte offset instead, which needs no counting. The scan reads through the file handle rather than the cache, so walking a file does not evict the window the viewer is drawing from.
*   **Step 8a — the patch command.** `patch` assembles a file from `S <off> <len>` ranges of an existing one, copied at local disk speed on the remote host, and `D <len>` literals that follow their descriptor on the wire. A one byte change in a hundred megabyte file therefore costs one byte of traffic. `Client.Patch` splits oversized literals into chunks and, like a write, tells a refusal that drained its payload from a failure that could not. The result is written forward into a second path, which is why source and destination may not be the same file.
*   **Step 8b — the editor on top of it.** `vfs.DeltaWriter` is the optional interface, `FishVFS` implements it over `Client.Patch`, and `SaveToFile` uses it when the save goes through `.f4tmp` anyway. A piece table already is the description the command wants: a piece pointing at the original buffer is a range of the file on disk, one pointing at the add buffer is what the user typed. It applies only to a raw UTF-8 load, because with any other codepage the buffer holds decoded text whose offsets say nothing about the bytes on disk, and it falls back to writing the file out in full whenever the remote host cannot do it.
*   **Step 9a — background jobs.** `jstart`, `jpoll`, `jkill`, `jdrop` and `jlist`: a detached subshell whose streams point away from the wire, and a client that polls it with a backoff. The first kind is `scan`, which counts a whole tree on the remote host and reports progress while it does. `Client.Scan` cancels the remote job when its caller goes away; `StartScan` and `FollowScan` underneath it cancel nothing, which is what step 9d will build on.
*   **Step 9b — the scanning VFS.** `FishVFS` implements `vfs.FastScanner` over `Client.Scan`, so a directory size and the pre-scan of a copy are walked on the remote host rather than through one listing round trip per directory, and cancelling the dialog cancels the remote work. Anything the host cannot do falls back to the generic walk, since `vfs.CalculateStats` treats a `FastScanner` answer as final and never retries it.
*   **Step 9c — hashing and duplicates.** A `hash` job kind, `Client.Hash` and `Client.Duplicates`. Hashing a whole tree would read every byte of it, so the job walks the tree for sizes first and hashes only the files whose size is shared with another: a file with a size of its own cannot have a twin. Both passes over the list are metadata only, and the reading that remains never crosses the network. Which utility does the hashing is detected and announced as `hash:<name>`, so the values are only ever compared with others from the same host.
*   **Step 9d — the duplicate search in the panel.** `vfs.DuplicateFinder` is the optional interface, `FishVFS` implements it over `Client.Duplicates`, and a Commands menu entry runs it against the current directory with a progress dialog of its own and a cancel button that stops the remote job too. The groups are flattened into the existing search results window with the rows of a group adjacent. A file system that cannot do the work on its own side does not offer the command at all, rather than reading a whole remote tree to answer it: `Action.Visible` decides whether a menu entry appears, asked each time the menu is built, so the entry is simply absent on a local panel.
*   **Step 9e — the job registry.** `BackgroundJobRegistry` keeps what is running, with a title, a status line and a way to ask it to stop, and notifies whoever is showing the list. It holds no session and no context: it knows how to ask a job to stop and what to call it, and everything else stays with the task that started it.
*   **Step 9e2 — leaving a job running.** `FileOpProgressDialog.EnableBackground` adds a second button that closes the window without stopping the work, so "cancel" and "stop watching" are different answers to a dialog that used to have one. It is offered only by an operation that genuinely survives its window: a copy through this client does not, work on a remote host does. The duplicate search uses it and stays in the job registry after the window is gone.
*   **Step 9f — the job window and the parked result.** A job that was sent to the background finishes into the registry rather than onto the screen: the answer waits with a line saying what it found, and a Commands entry lists what is running and what is waiting. An answer that arrives half an hour later must not open a window over whatever the user is doing by then, which is the whole meaning of having stopped watching. A job still running can be cancelled from the same list.
*   **Step 10 — cancellation without losing the session.** A cancelled request used to leave the client with no idea where the next answer began, so the session was marked broken and the site had to be opened again. It now reads forward to the terminator, which no output can forge, and carries on: the cost is the rest of one answer instead of a reconnect. Binary frames are consumed as frames while draining, since a discarded byte that happens to be a newline would otherwise look like a boundary. A remote that never finishes the answer is given `DrainAfterCancelTimeout` and then written off, because past that the session is worth less than the wait.
*   **Step 11a — running a command over there.** The `exec` job kind and `Client.Run`: a command line executed where the files are, its output arriving line by line as the job produces it, its exit status reported, and `jkill` stopping it. It is a job rather than a request because a command can read stdin, print for an hour or never end, none of which a request stream survives.
*   **Step 11b — running a command from the panel.** `vfs.CommandRunner` is the optional interface, `FishVFS` implements it over `Client.Run`, and a Commands entry asks for a command line and shows what it prints while it is still printing. Closing the window stops the command: unlike a search, it was started to be watched, and leaving it running with its output going nowhere would help nobody. The entry is absent on a file system that cannot run anything, which is every local one, where a shell is already at hand.
*   **Step 12a — the viewer's search.** `ViewerBackend.SearchFrom` asks the file system when it can search its own copy, and the viewer's read-and-scan loop stays as the fallback for everything that cannot. Over FISH+ a search in a gigabyte log is now one round trip instead of a gigabyte of traffic. The remote search folds case, because the viewer's own search always has, and a pattern that matched in one panel and not in another would be worse than a slow search.
*   **Step 12b — the ls backend.** A host with neither GNU find nor either stat can now be browsed: `ls -lan` with a full timestamp, in both dialects that offer one. Listing, stat, lstat and file size all go through it. The tree search and the scan job go through it too, by running `ls -d` from inside `find`, so a host that has a `find` without `-printf` keeps both; where there is no `find` at all the search refuses and the client walks the tree itself. The hash job still refuses on this backend — see `IDEAS.md`.
*   **Step 13a — the cursor that came back to the wrong line.** The editor's background indexer posts its batches to the UI thread and asked `absPos` whether the scan was over — a variable the scanning goroutine keeps advancing while those batches wait in the queue. Over FISH+ that is enough to break it: the first read asks for a megabyte, four chunks are fetched at once, their store tasks are drained in a single pass, and the scan then runs to the end of the file without stopping. Every batch it posted is executed afterwards, and the first one to run reads a finished `absPos` with a few hundred lines indexed, takes the jump and clamps the saved line down to the end of the partial index. A batch now carries the position it reached, so the question is asked of the batch rather than of the future. Reproduced by draining the task queue only after the goroutine has finished, which is the ordering a burst of chunks produces.
*   **Step 13b — the key pressed while the index was still catching up.** Reopening a file at a saved position could leave the cursor at the top of it instead. The restore waits for the background index to reach the saved line, and any key at all used to cancel it as "the user took control" — including a key that did nothing, which is what Up at the top of a file is. Locally the wait is a few milliseconds and the window is hard to hit; over FISH+ it is seconds long, so one keystroke while a remote file loads was enough. A key now cancels the restore only when it moved the cursor or changed the text, which is the only reading of "took control" that means anything. Reported from a real session and reproduced by pressing Up during the load.
*   **Step 13c — a saved position that belongs to one file only.** `file_states.json` was keyed by the bare path, so `/etc/hosts` on a remote host, `/etc/hosts` on a second host and `/etc/hosts` here were one entry: opening any of them restored the position left in another. `FileStateKey` now qualifies the path with the file system it was on, using the site name the panel title already shows for a remote one — `user@host`, which is the distinction the user is making anyway. A local path stays bare, so every position saved before this change is still found. Two sessions to the same host agree, which is what makes the position survive a reconnect at all.
*   **Step 14a — keepalive.** Nothing crosses the wire between two requests, so a panel left open while the user reads something else is a silent connection — which is what a NAT table drops and what sshd's `ClientAliveInterval` reaps. The failure was invisible until it mattered: the next request failed, and the user learned about a connection that had died an hour earlier at the moment they wanted to use it. The session now records when it last carried a request, and a loop sends a `noop` once that goes past a minute. It goes through the session's own mutex like any other request, so it cannot interleave with one and a busy session never sees it. `fishConn` owns one per session, since it is already the thing that knows when the last user is gone, and stops it before the session is torn down. A noop that goes unanswered for twenty seconds marks the session broken, which is as far as this can go until the session learns to reconnect.
*   **Step 14b — reconnecting.** A site is opened through a `FishDialer` rather than a pair of streams, so `fishConn` can build a second session with the credentials the first one used; `NewFishVFSOnStream` still exists and still cannot reconnect, which is the honest answer for a caller that handed over streams it has no second copy of. Every view reaches the session through the connection, so one panel reconnecting brings the panel beside it along. `vfs.SessionReconnector` is how the interface asks — was the session lost, can it be rebuilt, rebuild it — and `offerReconnect` is the question the user answers: reconnect, work offline, or close the panel, with Escape meaning the second. The caller says whether repeating its operation is honest; a directory listing is, a half written file is not, and one that is not still gets the session back but is told to start again by hand. Background jobs on the dead session are marked lost in the registry as soon as the loss is known, because the far side kept nothing of them. The panel's directory load is the only caller so far.
*   **Step 15 — server-to-server copy/move (S2S).** Direct copy/move between different remote hosts (Host A and Host B) using bidirectional `scp` (push/pull) probing, completely bypassing the client and resolving firewall/authorization constraints.
*   **Step 16 — the rest of the odd hosts.** Whatever the compatibility issue turns up beyond the `ls` backend; `tools/fishplus_probe.sh` collects the raw material. Nothing is outstanding from the reports so far.
*   **Step 17 — Support for Windows hosts.** Extend FISH+ to support Windows machines as remote servers. This can be implemented by executing commands through PowerShell, or by running `f4` itself on the remote Windows machine as a dedicated FISH+ helper/server (which is highly beneficial for non-Windows platforms as well).

### To do

*   **Step 18 — Connection pooling / Keep-alive.** Do not close the SSH/shell connection immediately when a FISH+ panel is closed. Keep the session alive so that opening it again connects instantly. (Requires looking at far2l's NetRocks implementation as a conceptual reference, but must be written from scratch under BSD-3).
*   **Step 19 — what a reconnect still cannot do.** The keepalive and the reconnect have landed; what is left under this number needs more than a new session. A transfer that resumes where it stopped needs the remote side to say how much of it arrived, which nothing on the wire reports today. A session that outlives the process — a job whose answer nobody looked at before f4 was closed, a panel that reopens where it was — needs state on disk rather than in memory. And the call sites: the viewer, the editor and the file operations all meet `ErrBroken` and none of them asks yet, so a lost session is only a question in the panel and still a plain error everywhere else. Each of the three is separate work and none of them blocks the others.
*   **Step 20 — FISH+ features for SFTP and SCP.** Extend FISH+ specific advantages (like opening the editor from the middle of a file via random access `vfs.ReadAt`) to the SFTP backend, and to SCP when it is ready.

*   **Step 21 — the interactive terminal.** A shell on a pseudo terminal of its own on the far side, which needs a second channel rather than the request stream: the requests are strictly sequential and an interactive session cannot share them. far2l's built-in terminal is the reference for what this half has to get right. LLM MUST consider using techniques from far2l internal virtual terminal as it is time-tested solution. LLM MUST ask user for recent far2l vt code if it was not included in prompt.
UPD: This part is already implemented by another model. We are skipping it for now; if any improvements are needed there, the user will ask to fix them separately.

*   **Step 22 — one command engine for every backend.** SFTP wants `vfs.CommandRunner` too, and SCP will when NetFox grows it; all three sit on an SSH connection that can already run a command. Sharing the contract and the plumbing, and leaving each backend only the starting and the reading, keeps the three from answering the same questions differently. See `IDEAS.md`.

## Testing

    go test ./plugins/netfox/fishplus/ ./plugins/netfox/

The tests whose names end in `AgainstLocalShell`, together with `TestFileReadAtAndCache` and the `FishVFS` tests in the `netfox` package, run the real helper in a local `/bin/sh`. That is the only kind of test that proves the shell side and the Go side agree on the wire format, and it walks every backend the test machine provides, one subtest each. They skip themselves on Windows and on hosts without a shell or without base64.

Every write test ends with a `ping` on purpose. Only what the session answers *after* a refused write can show that the payload really left the wire; a test that just checks the error would pass on a helper that desynchronizes the stream. The same goes for a refused `patch`, and for the tree search, whose request carries a variable number of lines and is therefore the easiest one to get out of step.

The job tests check the scan against a walk done here, over every listing backend the machine provides, because the interesting question is not whether the helper can count but whether both sides agree on what a directory is and what a symlink weighs. Two of them build a tree of a few thousand entries on purpose: the remote awk reports every two thousand, so anything smaller would pass on a helper whose progress never leaves its buffer. Cancellation is triggered from inside a progress report, which is the one moment when nothing of ours is on the wire, so the test is deterministic rather than a race — and it ends with a ping, because the property being checked is that a cancelled job leaves the session usable.

`tools/fishplus_testlab/` holds a Python client that speaks the same wire protocol to a shell started as a subprocess, for the things `go test` cannot reach: it shows the remote `stderr`, it can shadow `PATH` to pretend to be a host we do not own, and it has no network in it, which is what makes it lose the races this protocol has with itself. Both the bootstrap race and the special builtin trap described above were found there.

These tests take `sh` from `PATH`, so what they actually exercise depends on the machine. Run them at least once with `/bin/sh` pointing at `dash`, and preferably also at `busybox sh`: `bash` is forgiving in ways the shells on the hosts people connect to are not, and a helper that only ever meets `bash` is a helper whose worst bugs are still ahead of it.
The timestamp tests run twice: once against the tools the machine has, and once with stubs in front of `touch` and `date` that refuse `-d` and answer `-r`, the way macOS does. Without the second run the BSD branch would never be executed on a GNU build machine, and a mistake in it would only surface on a user's Mac.
### What the compatibility issue changed
Probe version 2 asked for what the `ls` parser would need, and five more reports settled it:

*   **OpenWrt with BusyBox** has no `stat` at all and a `find` without `-printf`, which makes it the first reported host that needs the `ls` backend — and the one that showed the trap. Its `ls` accepts `-T` and ignores it, printing the short format with no year, so a dialect chosen by exit status would have been silently wrong on every date. It does have `--full-time`, which is why that dialect exists.
*   **DragonFly and FreeBSD** land on `statbsd` and `ls -T`, both have a `head -c` that drains its input, neither has `iflag` on `dd`, and DragonFly has neither `sha256sum` nor `md5sum` but does have `cksum` — which the hash job already accepts, since the values are only ever compared with others from the same host. FreeBSD 15 turned out to have `find -printf`, without the sub-second part GNU prints; the parser takes both.
*   **illumos** was expected to be the gap and is not: it has GNU `stat -c` and `ls --time-style`, so it lists through the `stat` backend and never reaches the fallback.
*   Neither DragonFly nor FreeBSD understands `printf '\x41'`, though both take the octal form. Nothing here uses the hexadecimal one, which is worth remembering before something does.

Issue #316 collects probe output from hosts we do not own. The first three reports (macOS 26 on arm64, Git for Windows/MSYS2, Ubuntu under WSL2) already paid for themselves:

*   `openssl base64 -d` decodes *nothing* when its input does not end with a newline, which made the openssl fallback dead weight: a host whose `base64` speaks neither `-d` nor `-D` was refused at the handshake. The decoder probe and `f4_dec` now both terminate their input, and openssl is called with `-A` so a long line survives.
*   macOS `head -c` reads its input to the end even after it stopped writing, so it can never be used on a stream someone else still has to read from. The helper probes for this and reports `headc` and `headsafe` separately; the write step will need it.
*   macOS `find` has no `-printf` and its `stat` no `-c`, so `statbsd` is the backend there, and its `dd` has no `iflag`. Both paths are exercised here against a simulated host built from that report.

Hosts with neither GNU find nor either `stat` are still unlisted; the `ls -l` fallback waits for probe output from the systems that need it. Probe version 2 asks for what that parser will need — `ls -lT`, `--time-style`, `--full-time`, numeric ids, symlink rendering — plus the `dd`, `tail -c`, `stty` and shell arithmetic details the read and write steps depend on.
