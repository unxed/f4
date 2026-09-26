# FISH+ Windows / PowerShell backend — design notes

Working notes written during the port, kept afterward as reference: the
command-by-command mapping and the wire path convention below aren't
duplicated anywhere else. Not documentation for users. The port itself has
landed — `helper.ps1`, `script.go`'s `HelperSourcePwsh`/`HelperScriptPwsh`/
`BootstrapBase64LinePwsh`, `session.go`'s `flavor:pwsh` banner tag and
`Features.Flavor()`, and `fish_vfs.go`'s `NewFishVFS` trying the POSIX
bootstrap first and falling back to the PowerShell one on a handshake
failure — see `docs/FISH+.md` Step 17 for the summary and the flavor-choice
rationale. The "Go-side changes needed" section below is what Phase 2 asked
for, kept as a record of the plan against what was actually built (choice A,
not B — see that section).

## Scope and non-goals

**In:** a helper for a plain PowerShell 5.1+ session reached over SSH
(`OpenSSH-Server` on Windows, subsystem `sftp` unused, shell is
`powershell.exe` or `pwsh.exe`). Same wire protocol v1 as `helper.sh`.
Same client, same VFS, same test lab pattern.

**Out (this pass):**
- `cmd.exe`-only hosts. `cmd`'s scripting is a much worse target than PS
  and virtually every Windows box with OpenSSH-Server also has PS.
- WSL/Git-Bash on Windows. Those already work through `helper.sh`.
- ACL-preserving copy/move, Windows-specific attributes (readonly,
  hidden, archive) beyond a reasonable rwx projection.
- Anything that requires elevation.

## What the wire protocol actually requires from the backend

From `helper.sh` (v1 — the only version):

1. **Line-oriented request stream.** One line per request:
   `<ID> <cmd> [<a1> <a2> <a3>]`. Path lines follow, raw or
   `~<base64>` if they contain LF/CR/tab or start with `~`.
2. **Line-oriented reply stream ending in a terminator.**
   `.<TOKEN> <ID> ok|err [flat-message]`. Payload is any lines the
   command emits before the terminator.
3. **One-shot greeting.** The helper's first line after `printf '\n'` is
   the terminator for id `0`, carrying `FISHPLUS <PROTO><space-feats>` on
   ok, or an error message.
4. **Binary frame primitive.** `#<n>\n` followed by exactly n raw bytes.
   Used by `read` (server → client) and `write`/`patch raw` (client →
   server). Bytes are literal, not encoded.
5. **Line boundary is LF.** `IFS= read -r` on the server, and the client
   is a Go program that will read `\n`-terminated lines. CRLF from the
   server would either break parsing (if the client is strict) or leave
   `\r` in every parsed field (if it's lax).
6. **UTF-8 filenames must survive round-trip.** The whole protocol is
   byte-transparent for paths; `~base64` handles bytes that would break
   the line channel.

The Windows backend must respect all six. The riskiest ones under
PowerShell are (4) — PS pipeline is text — and (5)/(6) — PS 5.1
defaults to the OEM code page for stdout.

## PowerShell command mapping

One row per wire command. `PS API` is what the helper.ps1 body will
call; where an idiom needs a wrapper, the wrapper name goes in the
`helper.ps1 fn` column and the details land in a later section.

| Wire cmd  | POSIX (`helper.sh`)      | PowerShell approach                                          | helper.ps1 fn      |
| --------- | ------------------------ | ------------------------------------------------------------ | ------------------ |
| `noop`    | just terminator          | just terminator                                              | inline             |
| `pwd`     | `pwd`                    | `$PWD.Path`                                                  | inline             |
| `ping`    | echo path                | echo path                                                    | inline             |
| `enum`    | find/stat/ls listing     | `Get-ChildItem -Force -LiteralPath`                          | `f4_list`          |
| `isdirs`  | `[ -d ]` per path        | `[System.IO.Directory]::Exists($p)`                          | `f4_cmd_isdirs`    |
| `info`    | find/stat entry (follow) | `[IO.FileInfo]` after `Resolve-Path`                         | `f4_cmd_info`      |
| `linfo`   | same, no follow          | `[IO.FileSystemInfo]` no resolve; check `ReparsePoint` attr  | `f4_cmd_info -L`   |
| `rdlink`  | `readlink`               | `(Get-Item -LiteralPath $p).Target`                          | `f4_cmd_rdlink`    |
| `mkdir`   | `mkdir -p`               | `New-Item -Type Directory -Force`                            | inline             |
| `rm`      | `rm -f`                  | `Remove-Item -LiteralPath -Force`                            | inline             |
| `rmdir`   | `rmdir`                  | `[IO.Directory]::Delete($p, $false)`                         | inline             |
| `rmtree`  | `rm -rf`                 | `Remove-Item -Recurse -Force`                                | inline             |
| `mv`      | `mv -f`                  | `Move-Item -Force` (`Rename-Item` if same dir)               | inline             |
| `cp`      | `cp -R -f`               | `Copy-Item -Recurse -Force`                                  | inline             |
| `chmod`   | `chmod <octal>`          | project owner-write bit onto `ReadOnly` attribute; ignore rest | `f4_cmd_chmod`   |
| `chown`   | `chown u:g`              | **not implemented** — reply `err "chown unsupported"`; the client already handles servers without chown | inline |
| `read`    | dd/head/tail             | `FileStream.Seek + Read` → raw bytes to stdout               | `f4_read_range`   |
| `write`   | dd raw / base64          | `FileStream.Seek + Write` (raw or `[Convert]::FromBase64String`) | `f4_write_run`  |
| `trunc`   | `truncate`               | `FileStream.SetLength`                                       | inline             |
| `patch`   | segments + writes        | segments as `FileStream` reads from src, writes to dst       | `f4_cmd_patch`     |
| `utime`   | `touch -t`/`-d`          | `[IO.File]::SetLastWriteTimeUtc` / `SetLastAccessTimeUtc`    | `f4_cmd_utime`     |
| `grep`    | `grep -abo` + awk        | streaming reader over the file, byte-offset per match        | `f4_cmd_grep`      |
| `lidx`    | awk pass                 | streaming reader over the file, byte offset per line         | `f4_cmd_lidx`      |
| `ffind`   | `find` + optional grep   | `Get-ChildItem -Recurse -Include` + optional `Select-String` | `f4_cmd_ffind`     |
| `jstart`  | subshell `&`             | `Start-ThreadJob` writing to files under a job dir           | `f4_cmd_jstart`    |
| `jpoll`   | wc/tail/head over files  | same shape, `[IO.File]` reads                                | `f4_cmd_jpoll`     |
| `jkill`   | `kill`                   | `Stop-Job` + `Stop-Process` of children                      | `f4_cmd_jkill`     |
| `jdrop`   | kill + `rm -rf`          | same                                                         | `f4_cmd_jdrop`     |
| `jlist`   | glob                     | `Get-ChildItem` of job dir                                   | `f4_cmd_jlist`     |
| `mode`    | switch listing backend   | one backend only (`Get-ChildItem`); accept `getchilditem`, reject others | inline |
| `rmode`   | switch read strategy     | one strategy only (`filestream`); accept it, reject others  | inline             |
| `wmode`   | switch write strategy    | one strategy only (`filestream`); accept it, reject others  | inline             |
| `feats`   | echo feature list        | same, precomputed at startup                                 | inline             |
| `exit`    | clean jobs, break        | same                                                         | inline             |

Notable simplifications vs `helper.sh`:

- **Single backend per capability.** No probes for `dd` variants, `head -c`
  quirks, GNU vs BSD stat. .NET is a stable substrate: one code path.
  The features string still gets advertised (client-facing contract), but
  the values are constants: `mode:getchilditem read:filestream write:filestream`.
- **Uniform binary framing.** `.NET FileStream` reads and writes exact
  byte counts by contract; no equivalent of the `ddbytes`/`b64`
  fallback dance.
- **Symlinks are `ReparsePoint`.** They exist on Windows but need SeCreateSymbolicLink;
  the helper treats them as-is on read, refuses `mkdir -s` operations
  the protocol doesn't have anyway.

## Feature string the Windows helper advertises

```
FISHPLUS 1 base64 grep sed awk wc head tail touch date sha256sum \
         findbin jobs mode:getchilditem read:filestream write:filestream
```

Rationale: everything the client uses `feats` to gate is either always
available under .NET (read/write/find/hash/utime/jobs) or is meaningless
on Windows (`chown`, `truncate` as a separate binary — trunc is inline).
`sha256sum` here means "the helper can hash", not "the coreutils
binary is on PATH".

`chown` is deliberately absent. `chmod` is present but degraded to a
readonly-bit projection; the alternative (silent no-op) is a worse
contract.

## Encoding, CRLF and console mode — the three hard problems

1. **stdout must be raw bytes.** Two fixes at startup, both mandatory:
   ```powershell
   [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
   $OutputEncoding = [System.Text.Encoding]::UTF8   # for pipeline → external
   ```
   For binary frames the helper bypasses PS entirely:
   ```powershell
   $stdout = [Console]::OpenStandardOutput()
   $stdout.Write($bytes, 0, $n); $stdout.Flush()
   ```
2. **Line endings must be LF, not CRLF.**
   ```powershell
   [Console]::Out.NewLine = "`n"
   ```
   `Write-Host`/`Write-Output` respect it after this. `WriteLine` on
   `[Console]::Out` does. `Write-Output "x"` piped elsewhere doesn't
   necessarily — safer to use `[Console]::Out.WriteLine($s)` everywhere
   in the helper.
3. **stdin must deliver raw bytes for binary payloads.**
   ```powershell
   $stdin = [Console]::OpenStandardInput()
   # ReadExact:
   $buf = [byte[]]::new($n); $off = 0
   while ($off -lt $n) {
     $r = $stdin.Read($buf, $off, $n - $off)
     if ($r -le 0) { throw 'eof' }; $off += $r
   }
   ```
   For line-oriented reads, keep a `[BufferedStream]` in front of
   `$stdin` and pull one line at a time (LF-terminated, strip trailing
   CR if present — some SSH servers still cook).

Also disable progress and prompts:
```powershell
$ProgressPreference   = 'SilentlyContinue'
$ErrorActionPreference = 'Stop'
$PSStyle.OutputRendering = 'PlainText'   # PS 7.2+
```

## Session bootstrap

The client already ships `helper.sh` down the pipe as the first thing.
For the Windows flavor:

1. Client detects the peer is PowerShell (see `flavor detection`,
   pending Explore report).
2. Client ships `helper.ps1` with `__F4_TOKEN__` substituted, terminated
   by a newline and possibly a sentinel line so the helper knows the
   script ended and can start its `while` loop.
3. Because PS parses the whole script before running, the shipping is
   just "paste text, then press Enter". No `. { ... }` block wrapper
   needed if the file ends with a call to the dispatch loop.

## Go-side integration points (from a client-code map)

- **Helper delivery** is `HandshakeWithOptions` in `session.go:246-304`.
  Two strategies today, both POSIX-shell:
  - `BootstrapScriptLines`: writes `BootstrapLine(token)`, waits for the
    ready marker, then pipes `HelperScript(token) + "F4EOF\n"`. The
    wrapper is `while IFS= read -r F4L; do [ "$F4L" = F4EOF ] && break;
    F4S=$F4S$F4L$F4NL; done; eval "$F4S"` (`script.go:75-80`).
  - `BootstrapBase64Line`: single ASCII line, whole helper base64-encoded,
    decoded on the peer by one of `'base64 -d' 'base64 -D' 'base64 --decode'
    'openssl base64 -A -d'`, then `eval` on the result (`script.go:96-117`).
- **Prompt-drain / login-noise consumption:** `waitForReady` scans up to
  `maxBootstrapLines = 1000` lines for the token-anchored `F4RDY<token>`
  (`session.go:328-351`, `script.go:54`). No client-side stty/PS1 taming —
  all hygiene is inside the helper.
- **`Compact` strips CRs** from the embedded helper source
  (`script.go:33-36`). The PS port has the mirror-image concern: keep
  CRLF **out** of outbound bytes.
- **Feature-token enumerations are validated by the client**:
  `ListingModes` (`fs.go:56`), `ReadModes` (`read.go:31`), `WriteModes`
  (`write.go:30`). Any new mode name has to be added to both the helper's
  advertised feats AND those Go enums. **Big simplification: reusing
  existing tokens skips the Go change** —
  - `write:b64` already exists; the client sends payload as one trailing
    base64 line, no byte-exact stdin needed on the peer. This is the
    natural choice for PS (`[Console]::In.ReadLine`).
  - `read:cat` exists as the whole-file-dump fallback; a byte-range read
    needs one of `dd`/`ddbytes`/`tailc`, none of which map to a real PS
    idiom, so adding a new token like `read:filestream` is honest — one
    line into `ReadModes`.
- **`readLine` strips both `\r` and `\n`** (`session.go:617`) — so
  incoming CRLF from PowerShell is tolerated. That is one less thing to
  fight; we still prefer LF, but a stray CR won't sync-break.
- **Argument grammar refuses whitespace** (`session.go:431-434`). Any new
  arg tokens must be single non-whitespace words.
- **`mockPeer`** (`session_test.go:59-124`) is language-agnostic — it
  parses wire tokens and returns caller-supplied replies. The PS port
  needs no new mock; existing tests validate the wire, and helper.ps1 is
  validated by running it under a real `pwsh`, the same pattern
  `TestHelperAgainstLocalShell` (`session_test.go:805-…`) uses for
  `/bin/sh`, currently skipped on `runtime.GOOS == "windows"`. The PS
  variant is gated by presence of `pwsh` on PATH.

## Flavor detection — pick strategy

**A. Retry-on-fail (dumb, robust).** Send POSIX bootstrap first. On
`waitForReady` timeout or peer syntax error, close the pipe, reopen, send
PS bootstrap. Zero probe overhead on POSIX, one reconnect on PS.

**B. Explicit probe before bootstrap.** One line that both shells accept
but that answers differently:
`echo __F4A__$([regex]::Match($PSVersionTable.PSVersion.ToString(),'^\d+').Value)__F4B__`
– POSIX prints `__F4A____F4B__`, PS prints `__F4A__5__F4B__` or similar.
One extra roundtrip on every connect, no reconnect on either path.

**Recommendation: (A) for the first pass** — fits the existing
`HandshakeWithOptions` shape, needs zero new probe code. Switch to (B) if
the reconnect proves painful in practice.

## Bootstrap for PowerShell

Analog of `BootstrapBase64Line`, one line:

```
$s=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('<b64>'));iex $s
```

Prompt/PSReadLine noise is kept out by invoking pwsh with `-NoProfile
-NonInteractive -Command -` if the SSH server can be told to; otherwise
the helper's first act is `Remove-Module PSReadLine -Force -ErrorAction
SilentlyContinue`.

Go-side, new exports next to `BootstrapBase64Line`:

```go
func BootstrapBase64LinePwsh(token string) string { ... }
```

`Session` gains a `flavor` field, `HandshakeWithOptions` picks the right
bootstrap+helper pair based on it, and `Features` gains a `Flavor()`
accessor derived from a new banner tag `flavor:posix|pwsh` (added to both
helpers for symmetry).

## Test strategy

The existing `*_test.go` uses in-process `mockPeer` fakes that speak
the wire protocol. The port needs:

1. A `mockPeer` variant that answers Windows-flavored replies (mode
   line `M getchilditem`, feature string with the Windows set).
2. A real integration lane, gated by an env var or build tag, that
   pipes `helper.ps1` into a local `pwsh` and runs the full suite
   against it. Analogous to the `helper.sh`/`/bin/sh` lane if one
   exists (verify from Explore report).

## Path convention on the wire

`FishVFS` (`fish_vfs.go`) uses `path.Join`/`path.IsAbs`/`path.Clean`
— strictly POSIX. Returning `C:\Users\foo` from `pwd` would break
every join and every abs-check in the VFS. So:

**helper.ps1 speaks POSIX-shaped paths on the wire.** Windows
translation lives entirely inside helper.ps1.

Convention (matches Cygwin / Git-Bash):

```
/c/Users/foo/bar        ↔  C:\Users\foo\bar
/d/Backup               ↔  D:\Backup
//server/share/dir      ↔  \\server\share\dir
/                       ↔  (virtual root listing drive letters)
```

Enum of `/` returns one entry per fixed local drive:
`{c d e ...}` as directory entries with size 0, mtime epoch.
Enum of `/c` behaves like `C:\` — top-level listing.

Filenames with `\` inside are impossible on Windows (invalid char).
Filenames with `/` in them are also impossible on Windows. So a name
returned in a listing never needs re-encoding.

Path arguments the client sends arrive at helper.ps1 as POSIX-shaped
already (client-side is path-agnostic and just relays what pwd/enum
gave it). helper.ps1 does the translation, and every `f4_safe_target`
analog is done post-translation on the Windows path.

`f4_safe_target` port: reject anything that (a) is not `^/[a-z]/` or
`^//[^/]+/` shaped, (b) contains a `/../` or ends in `/..`. Same
spirit as POSIX: absolute, no dot-dot components.

## Feats string, final draft

```
FISHPLUS 1 flavor:pwsh base64 grep sed awk wc head tail truncate touch \
         date sha256sum findbin jobs cp dd readlink du chown \
         mode:stat read:filestream write:b64 headc headsafe tailc \
         ddnotrunc statl ddbytes awkflush
```

Every `feats.Has(...)` check on the client side is satisfied by
announcing the fake feature — real work goes through .NET. Rationale
per token:

- `mode:stat` — required by `parseListing` (hard enum
  `{find,stat,statbsd,ls}`); we emit stat's `%f %s %Y %X %Z %u %g %n`
  with hex mode.
- `read:filestream` — informational; `ReadMode()` just returns the
  string.
- `write:b64` — **mandatory**; `Client.Write` gates the base64 payload
  path on `writeMode == "b64"`. Any other value makes the client send
  raw bytes and PS's line-buffered stdin can't count them.
- `chown` — announced yes (fake) so the client offers the menu; the
  actual `chown` command returns `err "chown unsupported on windows"`.
  Alternative would be to hide the menu; announcing lets the user
  discover the limitation instead of finding it missing.
- `findbin`, `awk`, `jobs`, `sha256sum` — gate `CanFind/CanIndexLines/
  CanRunJobs/CanScan/hash` on the client side. All fake — .NET does
  the actual work.
- `sed`, `grep`, `wc`, `head`, `tail`, `cp`, `dd`, `du`, `readlink`,
  `truncate`, `touch`, `date`, `base64` — checked by `feats.Has(...)`
  various places; announce for compatibility.
- `headc`, `headsafe`, `tailc`, `ddnotrunc`, `statl`, `ddbytes`,
  `awkflush` — sub-feature flags helper.sh advertises; announced for
  symmetry, meaning "capability implied by using .NET". Cost is zero
  and it keeps the feature-parity story clean.

## helper.ps1 layout (final)

1. **Prologue** (~30 lines) — `$F4TOKEN`, `$F4PROTO`, constants,
   pragma comments, license header.
2. **Setup** (~50 lines) — force UTF-8, force LF, disable prompts /
   progress / PSReadLine, capture raw stdin/stdout streams as
   `[FileStream]` for byte-exact I/O.
3. **Path translation** (~80 lines) — `PosixToWin`, `WinToPosix`,
   `SafeTarget`, `DriveList`, `IsVirtualRoot`.
4. **Wire I/O primitives** (~100 lines) — `Emit-Line`, `Emit-Frame`,
   `Emit-End`, `Emit-EndErr`, `Read-CmdLine`, `Read-PathLine`,
   `Read-Base64Line`, `Read-ExactBytes`, `Flat-Message`.
5. **Entry synthesis** (~120 lines) — `Get-StatMode` (project
   `FileAttributes` onto a hex st_mode with type bits), `Format-StatEntry`
   (emit `%f %s %Y %X %Z %u %g %n` line), owner SID → uid/gid stub
   (0/0 by default; TBD if we ever want real Windows SIDs).
6. **Command implementations** (~700 lines) — one function per wire
   command, `Cmd-Foo`. Grouped by domain in the file (listing,
   mutation, I/O, search, jobs).
7. **Job runtime** (~150 lines) — job dir under `%TEMP%\.f4jobs.<pid>.<token>`,
   `Start-ThreadJob` (fallback: `Start-Job`) with output/err/rc/kill/n files,
   scan/hash/exec job bodies.
8. **Banner + dispatch loop** (~50 lines) — emit `printf '\n'` analog,
   emit banner, then `while ($line = Read-CmdLine) { switch ($cmd) ... }`.
9. **Trap cleanup** (~10 lines) — `try/finally` to `Remove-Item -Recurse
   -Force $F4JDIR` on exit, kill any running jobs.

Total estimate: ~1200 lines of PowerShell.

## Go-side changes needed (Phase 2 of the port) — done, as choice A

Minimal set to make an auto-probing client find and drive helper.ps1. Landed
as planned, except flavor selection: choice A (retry-on-fail) was built, not
choice B (explicit probe) — `NewFishVFS`/`NewFishVFSOnDialers` in
`fish_vfs.go` try the POSIX bootstrap first and fall back to the PowerShell
one only on a handshake failure, with the working flavor promoted to
primary for future reconnects. `ProbeFlavor` (choice B) was never written;
nothing here needs it unless the reconnect-on-every-attempt cost of choice A
proves painful in practice, per this doc's own original recommendation.

- `script.go`: `//go:embed helper.ps1`, `HelperSourcePwsh()`,
  `HelperScriptPwsh(token)`, `BootstrapBase64LinePwsh(token)`.
- `session.go`: `flavor:` banner tag read back through `Features.Flavor()`;
  `HandshakeOptions.Bootstrap` gained the `BootstrapBase64LinePwsh` method,
  and `HandshakeWithOptions` branches on it to pick the helper source.
- Testing: `session_pwsh_test.go` and `script_pwsh_test.go`, gated on `pwsh`
  being on `PATH`, in the same shape as `TestHelperAgainstLocalShell` uses
  for `/bin/sh`.

The wire tests in `*_test.go` needed no change — `mockPeer` is
language-agnostic.
