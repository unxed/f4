# Upstreaming: changes sent back to the libraries f4 uses

f4 needs changes in several third-party libraries. We make them in our forks
under github.com/unxed first, so f4 can use them right away, and then offer
them to the original projects. This document tracks where each change is.

State as of 2026-09-25. Update it when a pull request is merged, closed or
opened.

What the marks mean:

- **Merged**: the change is in the upstream project.
- **Open**: sent as a pull request, waiting for the maintainer.
- **Ready**: the branch is pushed to our fork and tested, the pull request
  (or issue) is not opened yet. The link leads to the compare page.
- **Not sent**: still only in our fork. The reason and the next step are given.
- **Closed**: a pull request that was withdrawn or replaced.

Other rules:

- Forks whose `main` mirrors upstream (sevenzip, archives) keep our code on the
  `write` branch. See [ARCHIVE_DEPENDENCIES.md](ARCHIVE_DEPENDENCIES.md).
- A pull-request branch is cut from upstream's default branch and carries one
  topic. It keeps upstream's module path and imports, and has no fork-only CI.
  Before a branch is sent, the `upstream-pr-test` workflow in
  [unxed/sandbox](https://github.com/unxed/sandbox) runs `go vet` and
  `go test` on it on Linux, Windows and macOS.
- After a merge, update the fork from upstream, so that the fork differs from
  upstream only by what is still open or not sent.

## Summary

| Project | Our fork | Merged | Open | Ready | Not sent |
| --- | --- | --- | --- | --- | --- |
| [ulikunitz/xz](#ulikunitzxz) | unxed/xz `master` | 0 | 4 | 1 issue | encoder speed-ups, LZMA2 parallel decoding |
| [bodgit/sevenzip](#bodgitsevenzip) | unxed/sevenzip `write` | 0 | 2 | 0 | 7z writing |
| [mholt/archives](#mholtarchives) | unxed/archives `write` | 0 | 4 | 0 | 7z writing, parallel 7z extraction |
| [go-webgpu/goffi](#go-webgpugoffi) | unxed/goffi `main` | 1 | 5 | 1 (Profile U) | nothing |
| [mikkyang/id3-go](#mikkyangid3-go) | unxed/id3-go `master` | 0 | 3 | 3 | nothing of our own |
| [godesktop/xkb-go](#godesktopxkb-go) | unxed/xkb-go `main` | 0 | 2 | 0 | nothing |
| [neurlang/wayland](#neurlangwayland) | unxed/wayland | 2 | 1 | 0 | nothing |
| [nwaples/rardecode](#nwaplesrardecode) | unxed/rardecode (branches only) | 0 | 3 | 0 | nothing |
| [gogpu/gg](#gogpugg-gogpugogpu), [gogpu/gogpu](#gogpugg-gogpugogpu) | unxed/gg, unxed/gogpu | 2 | 0 | 0 | nothing |
| [alecthomas/chroma](#alecthomaschroma) | unxed/chroma | 1 | 0 | 0 | nothing |
| [korli/go](#korligo-go-for-haiku) (Go for Haiku) | unxed/go | 0 | 0 | 0 | nothing |

## ulikunitz/xz

Used by zip, tar, sevenzip, archives and zipper, through our fork
`github.com/unxed/xz`. The fork's own module path means the fork must be kept
in step with upstream by merging (done up to upstream v0.5.17).

| PR | What | State |
| --- | --- | --- |
| [#74](https://github.com/ulikunitz/xz/pull/74) | Faster LZMA decoding, fewer allocations (buffered range decoder, no allocation per operation, match copy with `copy`) | Open since 2026-06-17 |
| [#85](https://github.com/ulikunitz/xz/pull/85) | Opt-in parallel LZMA2 compression (`Workers` > 1); the default writer is unchanged | Open |
| [#86](https://github.com/ulikunitz/xz/pull/86) | `ParseBlocks`, `NewBlockReader`, `ParallelReader`: random access to and parallel decoding of `.xz` blocks (fixes upstream issue #72) | Open |
| [#87](https://github.com/ulikunitz/xz/pull/87) | Reading the branch-conversion (BCJ) and delta filters (upstream issue #20) | Open |
| Issue | "LZMA speed-up techniques from the unxed/xz fork": a description of every technique, detailed enough to implement without our code, with a proposed order of PRs | Ready (text written, issue not filed yet) |
| [#73](https://github.com/ulikunitz/xz/pull/73) | First version of #74, from the fork's `master` | Closed, replaced by #74 |

Not sent:

- **Encoder and match-finder speed-ups, including AMD64 assembly**
  (fork commits 6968e62, 45cfa6b, e79e487). They change the compressed output
  (more match candidates, sparse search), need Go 1.25.5, and are tied to the
  fork's rewritten writer. The assembly also fails `go vet` (frame pointer
  clobbered, wrong result names in `match_amd64.s`). Next step: the issue above
  proposes how to split them; send them one by one after #74, starting with
  the changes that keep the output identical.
- **LZMA2 parallel decoding inside the `lzma` package** (588d7d3). It changes
  the public signature of `NewReader2`. Next step: none planned; #86 covers
  parallel decoding at the `.xz` block level instead.
- **Dictionary and buffer pooling**. Overlaps #74 and #86. Next step: after
  both are merged.

Found while preparing the PRs, to fix in the fork: `lzma.Reader` in the fork
always reads ahead, so data after an LZMA stream is consumed from the
underlying reader (#74 does not have this problem), and the fork's range
encoder may write past its buffer for a plain `lzma.Writer`.

## bodgit/sevenzip

Our fork: `github.com/unxed/sevenzip`, code on `write`, `main` equals upstream.

| PR | What | State |
| --- | --- | --- |
| [#471](https://github.com/bodgit/sevenzip/pull/471) | Faster LZMA/LZMA2 decoding through unxed/xz: parallel LZMA2 decoding, buffered section readers, releasing decoder buffers on Close. The maintainer measured about 30% | Open since 2026-06-22 |
| [#490](https://github.com/bodgit/sevenzip/pull/490) | A read that ends before the entry's declared size returns a `ReadError` wrapping `io.ErrUnexpectedEOF` (a wrong password no longer gives an empty file and no error) | Open |

Not sent:

- **Writing 7z archives** (`writer.go`, `write_struct.go`, `write_types.go`,
  `spool.go`): solid and non-solid archives, LZMA2, AES encryption, parallel
  compression. It needs two APIs that upstream xz does not have yet: a parallel
  `lzma.Writer2` and `Writer2.Reset` for reusing writers. Next steps:
  1. wait for xz #85 (parallel writer, `Workers` field; it has no public
     `Reset`) and move the writer to that API;
  2. fix upstream issue #489 (the salt can overwrite the IV in the AES key
     derivation) before or together with writing, since the writer uses that
     code;
  3. translate the remaining Russian comments and clean up for upstream's
     golangci-lint settings;
  4. send as one PR.
- The zstd decoder limits the fork used to set were removed from the fork
  instead of being sent: the window limit was already the default, and the
  memory limit broke large frames.

## mholt/archives

Our fork: `github.com/unxed/archives`, code on `write`, `main` equals upstream.

| PR | What | State |
| --- | --- | --- |
| [#76](https://github.com/mholt/archives/pull/76) | Parallel XZ decompression for seekable input, through unxed/xz | Open since 2026-06-22 |
| [#84](https://github.com/mholt/archives/pull/84) | File names ending with a dot or a space on Windows (`\\?\` paths, without `filepath.Abs`, which strips them) | Open |
| [#85](https://github.com/mholt/archives/pull/85) | `FileFS` reports a compressed file (`name.gz`) by its decompressed name and size | Open |
| [#86](https://github.com/mholt/archives/pull/86) | Reuse a pooled copy buffer when archiving many small files | Open |

Not sent:

- **7z writing, split strategies for solid and non-solid archives, parallel
  7z extraction** (and the `ArchiveFS` locks, the trailing slash on folder
  names and `UncompressedSize` that come with them). They need 7z writing in
  bodgit/sevenzip. Next step: after the sevenzip writer is merged.
- The `recover()` around registering zip codecs is needed only because our
  unxed/zip registers the same codecs; it stays in the fork.

## go-webgpu/goffi

Our fork: `github.com/unxed/goffi`, used by f4 through a `replace` directive.
The large PR #76 was split at the maintainer's request; the plan is in
[the last comment on #76](https://github.com/go-webgpu/goffi/pull/76).

| PR | What | State |
| --- | --- | --- |
| [#50](https://github.com/go-webgpu/goffi/pull/50) | README: pureffi as a solution for the purego duplicate-symbol conflict | Merged 2026-05-26 |
| [#83](https://github.com/go-webgpu/goffi/pull/83) | Build `callback_amd64.s` on FreeBSD, and a CI check that the trampoline links as code | Open |
| [#84](https://github.com/go-webgpu/goffi/pull/84) | `docs/CALLBACK_ABI.md`: what callbacks accept on each platform | Open |
| [#85](https://github.com/go-webgpu/goffi/pull/85) | CI: stop `setup-android` from asking for the removed `tools` package (the Android jobs of the other PRs fail until this is merged) | Open |
| [#87](https://github.com/go-webgpu/goffi/pull/87) | `goffi_musl` build tag for Alpine and other musl systems, on top of upstream's `goffi_static` | Open |
| [#88](https://github.com/go-webgpu/goffi/pull/88) | NetBSD amd64/arm64, a platform matrix check, a job that runs goffi next to purego. Carries #83; rebase after #83 is merged | Open |
| [#76](https://github.com/go-webgpu/goffi/pull/76) | Profile U + musl + platform matrix in one PR | Closed, split into the PRs above |
| [#77](https://github.com/go-webgpu/goffi/pull/77) | Second copy of #50 | Closed |
| [`unxed:feature/profile-u`](https://github.com/go-webgpu/goffi/compare/main...unxed:goffi:feature/profile-u) | Profile U (`goffi_universal`): one binary for glibc and musl systems, re-executed through the host's dynamic loader. Stacked on #87. Includes what was promised on #76: the re-exec guard carries the pid, so a child started with `exec.Command(os.Args[0])` runs the bridge itself (checked by a respawn probe on glibc 2.31 and newer and on Alpine), and `ffi.Available()` documents its three modes | Ready |

## mikkyang/id3-go

Our fork: `github.com/unxed/id3-go`. Upstream has had no commits since 2023
and has no `go.mod`, so #41 adds one and most other PRs depend on it.

| PR | What | State |
| --- | --- | --- |
| [#41](https://github.com/mikkyang/id3-go/pull/41) | Replace cgo iconv with `golang.org/x/text`; add `go.mod` | Open |
| [#42](https://github.com/mikkyang/id3-go/pull/42) | Write ID3v2.2/v2.3 text frames as UTF-16, not UTF-8 (needs #41) | Open |
| [#43](https://github.com/mikkyang/id3-go/pull/43) | Fix file corruption when a tag grows; TXXX description terminator (needs #41) | Open |
| [`unxed:v1-codepage`](https://github.com/mikkyang/id3-go/compare/master...unxed:v1-codepage?expand=1) | Optional code page for ID3v1 text; `v1.UseLocaleEncoding()` picks it from the system locale through [localecp](https://github.com/unxed/localecp). Off by default (needs #41) | Ready |
| [`unxed:json-converter`](https://github.com/mikkyang/id3-go/compare/master...unxed:json-converter?expand=1) | JSON tag converter and the `id3json` and `id3lister` tools (needs #41 and #43) | Ready |
| [`unxed:more-tests`](https://github.com/mikkyang/id3-go/compare/master...unxed:more-tests?expand=1) | Tests for frames, tags, file handling and encodedbytes (needs #41) | Ready |

Not sent: the fork also carries four open upstream PRs by other people
(#8, #16, #30, #37). They are not ours to send; they are merged when upstream
merges them.

## godesktop/xkb-go

Our fork: `github.com/unxed/xkb-go` (upstream's module path is
`github.com/thegrumpylion/xkb-go`).

| PR | What | State |
| --- | --- | --- |
| [#3](https://github.com/godesktop/xkb-go/pull/3) | Several keyboard layouts and XKB group offsets, per-group key types, symbolic modifier names, variants with group suffixes in rules, XKB search paths for macOS and Windows, `State` getters | Open since 2026-05-10, no reply yet |
| [#4](https://github.com/godesktop/xkb-go/pull/4) | `ExampleNewContext` no longer depends on which XKB directories exist, so tests pass on Windows and macOS | Open |
| [#2](https://github.com/godesktop/xkb-go/pull/2) | First version of #3, from the fork's `main` | Closed, replaced by #3 |

## neurlang/wayland

| PR | What | State |
| --- | --- | --- |
| [#25](https://github.com/neurlang/wayland/pull/25) | No panic when libxkbcommon is missing | Merged 2026-05-09 |
| [#33](https://github.com/neurlang/wayland/pull/33) | Send client-allocated object ids in allocation order (compositors dropped the connection) | Merged 2026-09-11 |
| [#36](https://github.com/neurlang/wayland/pull/36) | `Window.SetAppID` for the xdg_toplevel application id (issue #37) | Open |

## nwaples/rardecode

Not forked in f4's `go.mod` (see rule 8 in
[ARCHIVE_DEPENDENCIES.md](ARCHIVE_DEPENDENCIES.md)); the fixes live only in
branches of unxed/rardecode and are offered upstream.

| PR | What | State |
| --- | --- | --- |
| [#67](https://github.com/nwaples/rardecode/pull/67) | A wrong password for RAR 1.5-4.x encrypted headers is reported as `ErrBadPassword` | Open |
| [#68](https://github.com/nwaples/rardecode/pull/68) | RAR 1.5-2.x archives with comments, subblocks, and RAR 1.5 files | Open |
| [#69](https://github.com/nwaples/rardecode/pull/69) | Data loss at the end of the window; RAR 2.0 multimedia decoding | Open |

## gogpu/gg, gogpu/gogpu

| PR | What | State |
| --- | --- | --- |
| [gogpu/gg#332](https://github.com/gogpu/gg/pull/332) | HiDPI: scale damage rectangles to physical pixels | Merged 2026-05-19 |
| [gogpu/gogpu#435](https://github.com/gogpu/gogpu/pull/435) | Drag and drop under X11 | Merged 2026-08-07 |

## alecthomas/chroma

| PR | What | State |
| --- | --- | --- |
| [#1242](https://github.com/alecthomas/chroma/pull/1242) | f4 added to "Projects using Chroma" | Merged 2026-05-01 |

## korli/go (Go for Haiku)

| PR | What | State |
| --- | --- | --- |
| [#2](https://github.com/korli/go/pull/2) | Merge go1.26.6 into `golang-1.26-haiku` | Closed: the maintainer updated the branch directly and pointed to `korli/sys_haiku` for `x/sys` |
