# Extra-lite profile and OpenWrt targets (f4#1671)

Status: design notes and measurements. The existing lite profile
(`go build -tags lite,vtui_noebiten,vtui_nogogpu`, see "Lite build" in the
README) is the starting point; nothing here changes the regular or the lite
build. This file records what was measured and what is still open, so that the
extra-lite profile and the OpenWrt artifacts are built from facts.

## Toolchain assumptions

- Go's own toolchain, `CGO_ENABLED=0`, `-trimpath -ldflags "-s -w"`. The binary
  is static, so no libc (musl on OpenWrt) is linked or required at run time.
- OpenWrt is `GOOS=linux`. Only the GOARCH and its floating-point/ABI variable
  differ per target.

## Target matrix (candidate, to be confirmed on real OpenWrt SDK targets)

| OpenWrt architecture family | GOOS/GOARCH | Variable |
| --- | --- | --- |
| `mips_24kc` (big endian, ath79 and others) | linux/mips | `GOMIPS=softfloat` |
| `mipsel_24kc`, `mipsel_74kc`, `mipsel_mips32` (ramips, ath79) | linux/mipsle | `GOMIPS=softfloat` |
| `arm_arm926ej-s`, `arm_xscale` (ARMv5) | linux/arm | `GOARM=5` |
| `arm_cortex-a7`, `-a9`, `-a15` (ARMv7) | linux/arm | `GOARM=7` |
| `aarch64_*` | linux/arm64 | none |
| `x86_64` | linux/amd64 | none |
| `i386_pentium4`, `i386_pentium-mmx` | linux/386 | `GO386=softfloat` |
| `riscv64_riscv64` | linux/riscv64 | none |
| `mips64_octeonplus` and other 64-bit MIPS | linux/mips64 | none |

The mapping comes from the OpenWrt architecture names and Go's port list; it
has not yet been checked by running a binary on each family, so it is a
candidate list, not a promise of support.

## Measured lite binary sizes

Stripped, `-trimpath`, current `lunobot/staging`
(sandbox run https://github.com/unxed/f4/actions/runs/36620252583, all nine
targets build):

| Target | Size, bytes |
| --- | --- |
| linux/riscv64 | 39649442 |
| linux/arm (GOARM=7) | 40501410 |
| linux/arm (GOARM=5) | 40632482 |
| linux/386 (softfloat) | 40886434 |
| linux/arm64 | 44237065 |
| linux/mips, linux/mipsle (softfloat) | 45023425 |
| linux/mips64 | 45809826 |
| linux/amd64 | 47218953 |

## Extra-lite profile

Built with `-tags lite,extralite,vtui_noebiten,vtui_nogogpu`. `extralite`
implies everything the lite profile leaves out and adds its own exclusions.
Current exclusions:

- Embedded translations other than English and Russian
  (`internal/i18n/langfs_extralite.go`, about 5 MB). A translation is still
  loaded from the language directories on disk, so the feature is not lost,
  only the copy inside the binary. (The F1 help is embedded in English in every
  build; other help languages are read from disk.)
- Every built-in plugin without an equivalent in mc (list under "Midnight
  Commander parity"); `plugins_internal_extralite.go` registers syntax
  highlighting only, and the action files that referenced the process, service
  and git plugins and the visual renamer bridge are built with `!extralite`.
- `golang.org/x/text/collate` (about 1.2 MB): file names are ordered by
  `internal/panel/namecompare_extralite.go`, which follows the root collation
  for white space, punctuation, digits and letters, but does not fold accented
  Latin letters onto their base letters.
- The East Asian code pages (Shift JIS, ISO-2022-JP, EUC-JP, EUC-KR, GBK,
  HZ-GB-2312, GB18030, Big5; `vfs/codepages_nocjk.go`, about 0.6 MB). UTF-8,
  UTF-16 and the single-byte code pages remain.
- The Lua interpreter (`gopher-lua`): Lua macros (`internal/macro`) and Lua
  plugins (`internal/plughost`) report themselves as unavailable
  (`lua_extralite.go`, `transport_lua_extralite.go`). Recorded keyboard macros
  and out-of-process plugins work.
- The WebAssembly runtime (`wazero`): WASM plugins and Observer modules report
  themselves as unavailable (`transport_wazero_extralite.go`,
  `plugins_observer_extralite.go`).

Extralite amd64 after these two slices: 37 224 713 bytes raw (40 907 017 before
them), with neither `gopher-lua` nor `wazero` in `go list -deps`. The sizes
tables above are from before them and are lower bounds of the gain.

## Measurements

Linux amd64, stripped, `-trimpath`; start-up and peak RSS from
`scripts/openwrt_smoke.py` (pseudo-terminal, panel listing plus a typed `cd`),
GitHub ubuntu-latest runner:

| | binary, bytes | listing shown | peak RSS |
| --- | --- | --- | --- |
| f4 lite | 47 534 345 | about 0.9 s | about 38 MB |
| f4 extralite | 42 111 241 | about 0.75 s | about 39 MB |
| mc 4.8.30 (Ubuntu package) | 1 140 880 (package 1 555 KB installed) | about 0.2 s | about 11 MB |

Cross-built sizes, bytes (lite / extralite): mipsle-softfloat 45 351 105 /
39 911 617, arm v7 40 829 090 / 35 389 602, arm64 44 499 209 / 39 125 257,
amd64 47 534 345 / 42 111 241. All eight build; the amd64 pair passes the
smoke check (panel listing and a typed `cd`). RSS and start-up were measured
on a GitHub runner, not on router hardware; the extra-lite profile saves
binary size, not memory, since the embedded translations are read lazily.

The `openwrt` workflow prints these numbers for every run.

Compressed sizes, which are what an `.ipk` or a squashfs image carries
(bytes, `gzip -9` / `xz -9e`), lite and extralite, after the collation and
East Asian code page slice:

| target | lite gz | lite xz | extralite gz | extralite xz |
| --- | --- | --- | --- | --- |
| mipsle-softfloat | 14 283 600 | 8 952 380 | 11 925 423 | 7 947 420 |
| arm v7 | 14 858 962 | 9 669 036 | 12 505 721 | 8 661 440 |
| arm64 | 15 364 297 | 10 122 452 | 13 007 442 | 9 118 464 |
| amd64 | 16 739 968 | 11 676 116 | 14 374 672 | 10 658 996 |

(Raw extralite sizes at that point: mipsle 38 600 897, arm 34 013 346, arm64
37 880 073, amd64 40 907 017.)

What is not reachable against mc: a statically linked Go binary carries its
own runtime, garbage collector, TLS/crypto and reflection tables, so it cannot
come near mc's roughly 1 MB executable plus shared libraries; the floor found
so far is about 8 MB compressed. Resident memory of a Go program is likewise
above mc's. What extralite keeps is the function set the panels, viewer and
editor give; what it gives up is listed under "Extra-lite profile" above and
grows with each slice.

Where the lite binary's 47 MB are: `.text` 17.4 MB, `.rodata` 13.1 MB,
`.gopclntab` 13.6 MB. The 32 MiB `crypto/internal/fips140/drbg.memory` that
`go tool nm` lists first is a zero-filled `.bss` buffer of Go's own FIPS pool:
it is not in the file and does not count towards the binary size.

Largest optional parts by linked size (candidates for later slices, each only
if it can go without taking a feature away): embedded translations 5.7 MB
(done above), `golang.org/x/text/collate` 1.25 MB, wazero about 1 MB (wasm
plugins), `github.com/yuin/gopher-lua` 0.29 MB (Lua plugins), `net/http` and
`crypto/tls` about 0.6 MB together, `plugins/mediainfo` 0.27 MB,
`golang.org/x/text/encoding` CJK tables about 0.6 MB, `ebitengine/purego`
0.9 MB.

## Target and decision

The owner's target for this profile is to reach mc's size or go below it while
giving the same or more capabilities and, where that cannot be done, to fix mc's
features that are not reachable and shrink as far as possible. The measured
floor for a static Go binary is far above mc's roughly 1 MB (see "What takes
the space"), so the profile keeps the *capabilities* of mc and drops what mc
does not have, biggest first: all languages but English and Russian, every
built-in plugin without an mc equivalent, the Lua and WebAssembly runtimes, the
collation tables. Each step is measured by the `openwrt` workflow.

## Midnight Commander parity

What mc offers, and what the extra-lite profile keeps for it:

| mc capability | extra-lite | how |
| --- | --- | --- |
| two panels, copy, move, delete, mkdir, mark, sort, filters, quick search, tree, find files, panelize, directory hotlist, user menu, background jobs | kept | f4 core (`internal/app`, `panel`, `fileops`, `vfs`) |
| internal viewer with hex mode, internal editor (mcedit), syntax highlighting | kept | `internal/viewer`, `internal/editor`, `plugins/chroma` (plus the viewer's disassembly mode, which mc lacks) |
| command line, subshell, `cd` | kept | `internal/cmdline`, the built-in terminal (`internal/terminal`) |
| archives as directories (extfs/uarc: tar, zip, 7z, rar, ...) | kept | `plugins/multiarc`, wrapping the host's `tar`, `unzip`/`zip`, `7z`, `gzip` |
| FISH and SFTP virtual file systems | kept as FISH+ | `plugins/netfox` (FISH+ over the host's `ssh`), see [FISH+.md](FISH+.md) |
| FTP virtual file system | not reachable | needs `jlaffaye/ftp`, which the lite family leaves out; f4's full build has it |
| SMB, SCP addresses | not in extra-lite | SMB is `!lite`; `scp://` is the SFTP backend, also `!lite` |
| several languages | English and Russian | see above |

Built-in plugins removed from extra-lite because mc has nothing like them:
visual renamer (`visren`), audio tag editor (`id3editor`), checksum tool
(`intchecker`), environment manager (`envman`), media information
(`mediainfo`), SQLite client (`sqlite`), process list (`proclist`), Windows
services (`svcmgr`), git status (`git`), .NET assemblies (`dotnet`), PDF
(`pdfview`), IDE mode (`ide`), the test dummy, Observer modules, Lua and WASM
plugins. The Docker, Kubernetes and MongoDB panels, cloud storage, Android and
iPhone drives were already outside the lite family.

## What takes the space

The extra-lite amd64 binary after the parity slices is 32 129 289 bytes
(stripped; run https://github.com/unxed/f4/actions/runs/36705091886 and
https://github.com/unxed/f4/actions/runs/36705536367). By section: `.text`
13.2 MB, `.rodata` 7.1 MB, `.gopclntab` 9.8 MB (the function tables, growing
with the code), `.noptrdata`/`.data` 2.0 MB. The table lists the largest
consumers of code and data (`go tool nm -size`, in MB; each one also drags its
share of the function tables along) and says what was decided.

| consumer | MB | kept / removed | why |
| --- | --- | --- | --- |
| function tables (`.gopclntab`) | 9.8 | kept | the price of the Go runtime for all the code below |
| `go:func` metadata | 1.6 | kept | same |
| core UI and file operations (`internal/app`, `panel`, `dialog`, `settings`, `config`, `cmdline`, `fileops`, `vfs`, `plughost`, `keymap`) | 3.2 | kept | this is the file manager |
| terminal UI and drawing code (`vtui`, `purego`, `goffi`, `xgb`, `xkb-go`, wayland, freetype, `x/image`) | 2.3 | kept | measured: the `noffi` tag (the console-only configuration the release uses for 386, mips and the like) changes the extralite size by 40 KB only (32 092 425 against 32 129 289 bytes), because `vtui_noebiten` and `vtui_nogogpu` already leave the GPU backends out; what remains is the terminal UI itself and the X11/Wayland loader code that a console-only split would still have to keep or rewrite |
| runtime, `reflect`, generic instantiations | 1.1 | kept | Go itself |
| `net/http`, `crypto/tls`, `crypto/x509`, `net` | 0.75 | kept | updater, PlugRing catalog, AI provider, proxy setting import them: seven packages, removing them takes those features away |
| editor, viewer, disassembler, Markdown (`editor`, `viewer`, `x/arch/x86asm`, `goldmark`) | 0.6 | kept | mc's editor and viewer; disassembly and Markdown are f4's own |
| Chroma lexers and regexp engines (`chroma`, `regexp2`, `coregex`) | 0.7 | kept | syntax highlighting, which mc has |
| East Asian encoding tables (`x/text/encoding/{japanese,korean,simplifiedchinese,traditionalchinese}`) | 0.56 | kept, for now | linked through `unxed/localecp` (see "Not done: the East Asian tables"); a patch is ready |
| crypto internals (`nistec`, `edwards25519`, `chacha20poly1305`) | 0.31 | kept | TLS and SSH host keys |
| terminal emulator (`internal/terminal`, `keytrans`) | 0.3 | kept | mc's subshell counterpart |
| NetFox FISH+ and multiarc | 0.3 | kept | mc's FISH/SFTP and archive file systems |
| AI chat (`vtvibe`) | 0.2 | kept | f4's own feature, not a plugin; a candidate if the owner wants only mc's features |
| images and spreadsheet (`internal/media`, `internal/sheet`) | 0.2 | kept | f4's own features, not plugins; candidates like the AI chat |
| YAML, MessagePack, `strcase` tables | 0.36 | kept | settings and the plugin protocol |
| `.NET` info action (`internal/dotnet`, 0.06) | 0.06 | kept | referenced by an action registered for every build; the panel plugin itself is gone |
| built-in plugins without an mc equivalent (about 12 packages) | about 5 | **removed** | see "Midnight Commander parity"; the binary went from 37.4 to 32.1 MB with the languages included |
| translations other than English and Russian | about 5 | **removed** | still read from disk |
| Lua interpreter | about 1 | **removed** | no mc equivalent |
| WebAssembly runtime | about 2.7 | **removed** | no mc equivalent |
| collation tables (`x/text/collate`) | 1.25 | **removed** | own name comparison |

## First run of the `openwrt` workflow (main, before the Lua and wasm slices)

Run https://github.com/unxed/f4/actions/runs/36673427522, all eight jobs
green. The `.ipk` files (gzip'd, what `opkg install` downloads and unpacks),
bytes: `mipsel_24kc` 12 032 119, `arm_cortex-a7_neon-vfpv4` 12 616 492,
`aarch64_generic` 13 060 983, `x86_64` 14 464 393. Smoke check on amd64
(GitHub runner): lite 47 964 425 bytes, listing shown after 820 ms, peak RSS
40 476 KB; extralite 41 062 665 bytes, 799 ms, 38 556 KB. That main did not yet
have the Lua and wasm slices, so a rerun after they reach it will show smaller
extralite numbers (37.2 MB raw at the time of those slices). Installing an
`.ipk` on a real router has not been tried.

## Second run of the `openwrt` workflow (main with the Lua and wasm slices)

Run https://github.com/unxed/f4/actions/runs/36675798812, all jobs green. The
extralite `.ipk` files, bytes: `mipsel_24kc` 11 237 261,
`arm_cortex-a7_neon-vfpv4` 11 743 488, `aarch64_generic` 11 873 448, `x86_64`
13 131 493 (12 032 119 to 14 464 393 in the first run). Smoke check on amd64:
lite 48 115 977 bytes, listing after 964 ms, peak RSS 41 408 KB; extralite
37 363 977 bytes (41 062 665 in the first run), 951 ms, 37 188 KB. Lua and wasm
cost about 3.7 MB of the raw size and about 1.3 MB of an `.ipk`; memory barely
moves.

## Third run of the `openwrt` workflow (staging with the parity slices)

Run https://github.com/unxed/f4/actions/runs/36706090880 on `lunobot/staging`
(commit `1e83e66f`), all jobs green. The extralite `.ipk` files, bytes:
`mipsel_24kc` 9 715 243, `arm_cortex-a7_neon-vfpv4` 10 135 192,
`aarch64_generic` 10 261 900, `x86_64` 11 320 155 (11.2 to 13.1 million in the
second run). Smoke check on amd64: extralite 32 133 385 bytes (37 363 977
before the parity slices), 803 ms to the listing, peak RSS 35 680 KB; lite
48 300 297 bytes, 848 ms, 38 636 KB. mc 4.8.30 on the same runner class was
1 140 880 bytes, 190 ms, 10 928 KB.

## Not done: the East Asian tables still in the binary

The extra-lite build leaves the East Asian code pages out of f4's own list, but
their tables (about 0.6 MB) stay linked because `golang.org/x/text/encoding/
htmlindex` pulls in every encoding and is imported by f4's dependency
`github.com/unxed/localecp` (`localecp.go`, to turn a locale's charset name into
an encoding), and, in f4 itself, by `vfs/codepages.go` and `codepages_unix.go`.
Removing them from f4 alone changes nothing (measured: 37 286 153 bytes before
and after). Getting them out needs a build-tag split inside `localecp`, a new
tag of it and a `go.mod` bump in f4, for 1.6% of the size; not done.

## Not done: `net/http`

Seven packages of f4 import `net/http` (`internal/app`, `netproxy`, `plughost`,
`settings`, `terminal`, `update`, `vtvibe`; no third-party package does), so it
leaves the binary only if all seven consumers (the updater, the PlugRing
catalog, the AI provider, the proxy setting and their neighbours) go. That is
about 0.3-0.9 MB of 37 MB and it removes functions, against the owner's target
of keeping at least mc's capabilities; it is not done. Further extralite
slices are taken only where they cost no function.

## Packages

- `.ipk`: the `openwrt` workflow wraps each extralite binary with
  `scripts/build_ipk.sh` for `mipsel_24kc`, `arm_cortex-a7_neon-vfpv4`,
  `aarch64_generic` and `x86_64`. A device installs only the one that matches
  `opkg print-architecture`; run the script with another architecture name for
  other families.
- Feed recipe: `packaging/openwrt/f4/Makefile` builds the same profile inside
  the OpenWrt build system (`golang-package.mk`). It has not been built there
  yet.

## Open points

- The mc baseline was measured on the amd64 runner only; a comparison on the
  target router hardware is not done.
- Further exclusions (the list above) are separate slices.
