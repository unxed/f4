# Architecture: Modular Monolith

## Overview

`f4` is a single static binary that ships a complete file manager: two panels,
editor, viewer, terminal integration, plugin hosts and a virtual filesystem layer.
The architectural pattern is a **Modular Monolith**: one deployment unit, one
process, but hard module boundaries drawn along Go package lines. A module is a Go
package; its public API is its exported identifiers; nobody reaches into another
module's internals.

The tree follows that pattern throughout. `vfs/`, `sdk/` and `plugins/*` are the
public contract; the application is 40 packages under `internal/`, in four
layers, with an acyclic dependency graph and no upward edges. `cmd/f4` is the
composition root and holds five files: `main.go`, four module-wide auditors, and
the Windows `.syso` resources the linker takes from the main package's own
directory.

**The root holds entry points and the plugin contract with its implementations;
the application lives under `internal/`.** The layout states the rule, the
compiler enforces it through `cmd/f4/architecture_test.go`, and an agent
reproduces it without being told again.

## Decision Rationale

- **Project type:** cross-platform TUI file manager, single static binary.
- **Tech stack:** Go 1.26.6, `CGO_ENABLED=0`, no framework; UI and input come from
  the external `vtui` / `vtinput` libraries.
- **Size:** 1360 Go files, 69 packages, AI-generated codebase where the test
  suite is the review mechanism.
- **Key factors:**
  1. A file manager is one interactive process with shared terminal state. There is
     no network traffic between subsystems that would pay for service boundaries —
     microservices and layered ports/adapters both add ceremony with no return.
  2. The subsystems are genuinely independent (piece table, spreadsheet, FUSE,
     Lua engine, VFS backends), and packages express that.
  3. `CGO_ENABLED=0` and the cross-platform matrix mean structure has to survive
     build tags. Package-per-module with `*_windows.go` / `*_unix.go` suffixes does;
     folder-per-technical-layer does not.
  4. Third-party plugins compile against `sdk/` and `vfs/`. Those are a public
     contract and must stay importable from outside the module — they cannot move
     under `internal/`.

## Folder Structure

The layout, as it is.

```text
f4/
├── cmd/
│   └── f4/
│       ├── main.go                # Composition Root: flags, startup mode, wiring
│       ├── *_test.go              # tests for the wiring itself, plus the
│       │                          # module-wide auditor (see below)
│       └── rsrc_windows_*.syso    # linked only from the built package's dir
│
├── sdk/                           # ── PUBLIC API (third-party plugin authors) ──
│   ├── f4plugin/                  #   in-process Go plugin contract
│   ├── f4rpc/                     #   RPC transport mux
│   ├── extui/                     #   external UI model
│   └── lua/                       #   Lua-facing surface
│
├── vfs/                           # ── PUBLIC: filesystem abstraction ──
│   ├── hostfs/  hostmode/  hostpath/
│   └── *_unix.go  *_windows.go    #   per-OS files, not runtime branching
│
├── plugins/                       # ── SHIPPED PLUGINS: implementations of sdk/ ──
│   ├── archive/ cloudfox/ netfox/ mediainfo/ envman/
│   ├── ios/ android/ sqlite/ visren/ id3editor/ chroma/
│   └── dummy_internal/ dummy_rpc/ dummy_lua/   # transport fixtures
│
├── plugring/                      # installable-plugin catalogue: index.yaml and
│                                  # the plugins it points at. Data, no Go files.
│                                  # Keeps the Far-era name it is modelled on, and
│                                  # stays in the root: its published URL is
│                                  # .../main/plugring/index.yaml, so the path is
│                                  # part of the contract.
│
├── internal/                      # ── THE APPLICATION IS MODULE-PRIVATE ──
│   │
│   │  # layer 4 — the composition root's package: the only one that may
│   │  # import every other, and which nothing else may import
│   ├── app/                       # action table, event loop, bootstrap, workspaces
│   │
│   │  # layer 3 — the interactive subsystems
│   ├── panel/                     # file panels, sorting, navigation, quick view, info
│   ├── editor/                    # F4 editor on top of internal/piecetable
│   ├── viewer/                    # F3 viewer, hex, disasm
│   ├── findfile/                   # live Find File search, results and root-child progress
│   ├── dialog/                    # modal dialogs, help, settings screens
│   ├── cmdline/                   # command line, prefixes, apply-command
│   ├── macro/                     # macro engine and Lua macro API
│   ├── terminal/                  # pty, console host, ttyx, ANSI parser, kitty/sixel
│   ├── media/                     # image, audio, video decode and preview
│   │
│   │  # layer 2
│   ├── plughost/                  # plugin host: in-process / RPC / Lua / WASM
│   ├── gui/                       # GUI backends, fonts, window position and icon
│   │
│   │  # layer 1
│   ├── fileops/                   # copy/move/delete, background jobs, clipboard
│   ├── update/                    # self-update, elevation, helper args
│   ├── fusefs/                    # FUSE mounting
│   ├── textlayout/                # text layout and wrapping
│   ├── diffview/                  # side-by-side compare-by-content view
│   ├── vtvibe/                    # vtvibe session/provider layer
│   │
│   │  # layer 0 — leaves; each may import internal/ini and nothing else of ours
│   ├── action/                    # the action registry
│   ├── appcmd/                    # frame command constants
│   ├── colorer/                   # colorer4go integration + embedded radiola.hrd
│   ├── config/                    # F4Config, ini parsing, config overlay
│   ├── history/                   # command, folder and view/edit history
│   ├── i18n/                      # language packs + embedded lang/
│   ├── ini/                       # the ini parser the leaves share; its own
│   │                              # package because none of them may import
│   │                              # another of ours
│   ├── keymap/                    # key names, remapping, the hotkey manager
│   ├── luaplug/                   # Lua plugin engine
│   ├── netproxy/                  # network proxy
│   ├── numeric/                   # numeric helpers
│   ├── piecetable/                # piece table backing the editor
│   ├── semantic/                  # the GUI semantic protocol's shared fields
│   ├── sheet/                     # spreadsheet mode
│   ├── sysinfo/                   # cpu / mem / fs / gpu info, drives
│   ├── textdiff/                  # Myers line diff for the compare-by-content view
│   ├── textsearch/                # text search
│   ├── theme/                     # colours, colour space, styles + embedded styles/
│   ├── toast/                     # transient notifications
│   ├── ttyx/                      # tty extensions
│   ├── unpack/                    # zip / tar.gz / 7z over a directory, and the
│   │                              # path guard its callers share
│   ├── wincon/                    # Windows console
│   │
│   │  # test scaffolding: ordinary packages, imported from _test.go only
│   ├── testutil/                  # layer 0: imports no package of ours
│   ├── paneltest/                 # layer 4: builds a panels frame, so it sits
│   │                              # above the three packages that make one
│   │
│   └── hideconsole/               # NOT our code and NOT in the layer graph: a
│                                  # vendored fork of
│                                  # github.com/ebitengine/hideconsole with its
│                                  # own go.mod and module path, wired in by
│                                  # `replace`. `go list ./internal/...` does not
│                                  # return it — leave the path alone.
│
├── embedded.go                    # root package: embeds README.md, and only that.
│                                  # Must stay in the root — //go:embed cannot
│                                  # reach above its own directory, and README.md
│                                  # has to sit there to render on GitHub.
│
├── scripts/        # *.sh moved out of the repository root
├── docs/                          # subsystem documents; SPREADSHEET.md lands
│   └── ISSUES/                    #   here. Per-issue reviews are named
│                                  #   ISSUE_<number>_<SLUG>.md — the number
│                                  #   addresses the issue, the slug says what it
│                                  #   was about.
├── tools/                         # developer tooling incl. the ttytest harness
├── packaging/                     # distribution packaging
├── .github/
│   ├── workflows/                 # CI build matrix, nightly and tagged releases
│   └── assets/        # screenshot.png and other README media
│
└── README.md  LICENSE  go.mod  go.sum  f4.example.ini  highlight.ini
                                   # reference configs stay next to the README
```

The rule the root expresses: **entry points and the plugin contract with its
implementations live at the top; the application core lives under `internal/`.**

- `sdk/` — third-party plugins compile against it. Under `internal/` Go refuses the
  import and every external plugin stops building.
- `vfs/` — same contract: a plugin implementing a filesystem backend needs the
  types. Twelve in-tree plugins plus `fusefs` and `vtvibe` already depend on it.
- `plugins/` — the shipped implementations of that contract, and the reference an
  external plugin author reads before writing their own. Extensibility through four
  transports is a defining property of f4, so the tree says so at the top level.
  They are leaves of the dependency graph: hiding them buys no enforcement, and
  297 files of extensions sitting beside `panel/` and `editor/` would blur the line
  between the core and what plugs into it.
- `plugring/` — the catalogue of installable plugins, named after the Far-era
  plugin ring it reproduces. It holds data, not Go files, so `./...` ignores it.
  It stays in the root because its `index.yaml` is fetched over HTTP from that
  exact repository path (`defaultPlugRingCatalogURL`,
  `internal/plughost/plugring.go:24`), which makes the path a published
  contract: moving it would leave already-installed builds unable to resolve the
  catalogue until they update.
- `cmd/f4` — the entry point; it is `package main` and nothing can import it anyway.
- `embedded.go` — the root package that bridges root-level files into the binary.
  `//go:embed` cannot reach above its own directory, and `README.md` has to stay in
  the root to render on GitHub, so this one file is pinned there by the toolchain.

Everything else is module-private, so the compiler answers "may I import this?"
before a reviewer has to.

**`internal/app` is the composition root and nothing else.** The dependency rule
below is what keeps it that way: `internal/app` may import every layer 0-3
package, and nothing below layer 4 may import it. Both halves are checked by
`cmd/f4/architecture_test.go`, and the second is the one that matters — a
utility called from six domains does not belong in the root, and if it drifts
there the check turns the drift into a build failure rather than a habit.

That is why the leaves exist at all. `internal/action` holds the registry
mechanism, `internal/toast` the transient notifications, `internal/history` the
command, folder and view/edit histories, `internal/numeric` the bounded-integer
helpers: each is called from several domains, so each is a package below all of
them rather than a file beside the loop.

**`internal/sysinfo` keeps its own copy of the one numeric helper it needs.** Its
single outbound edge would be one call to a bounded-integer conversion.
Importing `internal/numeric` would *create* an edge where the point is to have
none, so a five-line private copy is what makes its outbound count genuinely
zero. Everyone else imports the shared package.

**One test does not follow its subject.** `command_palette_coverage_test.go`
walks the whole module and checks a global invariant: every `ProcessKey` and
every `vtui.NewVMenu` is either reachable from the command palette or listed as
a deliberate exception. That inventory cannot be split per package — a
per-package copy sees only its own subtree, and a handler added in a third
package passes unnoticed, which is the thing the test exists to catch. It stays
in `cmd/f4`.

Its audit keys are the qualified symbol rather than a file path —
`panel.(*FileSystemPanel).ProcessKey`, not
`internal/panel/list.go:(*FileSystemPanel).ProcessKey`. A package changes far
less often than a path, files move freely inside their package, and
the package name is what identifies the subject anyway. The keys have to be
touched regardless; doing it as a re-keying instead of a path update ends the
tax rather than paying it on every commit.

**Embedded resources travel with their package.** The same `//go:embed` rule moves
each resource directory into the package that embeds it:

| resource | embedded by | lands in |
|---|---|---|
| `cmd/f4/styles/*.ini` | `style.go` | `internal/theme` |
| `cmd/f4/lang/*.lng` | `lang.go` **and** `lang_packs.go` | `internal/i18n` |
| `cmd/f4/help/en.hlf` | `help.go` | `internal/dialog` |
| `cmd/f4/assets/icon/generated/f4.icns` | `window_icon_darwin.go` | `internal/gui` |
| `colorer/…/radiola.hrd` | root `embedded.go` today | `internal/colorer` |

`lang/` is embedded from two different files, so both must land in the same
package or the directory ends up duplicated. Moving `radiola.hrd` to its single
consumer leaves root `embedded.go` with `README.md` alone — the case it exists
for. Windows `.syso` files are the exception that does not move: the toolchain
links them only from the directory of the package being built, so
`rsrc_windows_*.syso` stay in `cmd/f4` even though the icon code leaves.

## File Naming Inside a Package

A package's files are named `<topic>.go` for the core and `<topic>_<aspect>.go`
for everything that extends it. The prefix is the **topic inside the package**,
never the package name — `panel/frame.go`, not `panel/panel_frame.go`, the same
way `vfs/hostpath` holds `path.go` rather than `vfs_path.go`.

```text
internal/panel/
├── frame.go                # type PanelsFrame and its core methods
├── frame_dragdrop.go       # dragging files out of a panel
├── frame_translator.go     # the external-UI translator's view of the frame
├── frame_semantic.go       # the frame's half of the GUI semantic protocol
├── list.go                 # type FileSystemPanel
├── list_reconnect.go       # reconnecting a panel whose VFS session died
└── list_semantic.go        # the file panel's half of that protocol
```

Why it matters here specifically: Go requires a type's methods to live in the
type's package, so a package that owns a large type accumulates files. Sorted by
topic they read as one subject; named after unrelated features
(`dragdrop.go`, `translator.go`, `semantic.go`) they read as a pile, and a file
holding methods of six different types cannot be placed at all.

The convention is already half-present in the tree — `editor_view.go`,
`editor_base64.go`, `editor_find_all.go`, `command_palette_*.go`, `image_*.go`.
Extraction finishes it rather than introducing it. Platform suffixes compose on
the end as usual: `frame_dragdrop_windows.go`.

**Splitting a multi-type file.** When one file carries methods of several types
(`semantic.go` holds six), it is split along type lines and each piece lands in
its own package under its own topic name — `frame_semantic.go`,
`editor_semantic.go`, `viewer_semantic.go`. A method whose type lives elsewhere
but whose logic belongs to this package becomes a plain function taking the type
(`func handleDrop(pf *panel.PanelsFrame)`) rather than forcing the whole file
into the type's package.

## Dependency Rules

Dependencies point from the application inward to the subsystems.
`internal/app` is the only place where everything is assembled, and
`cmd/f4/main.go` is one call to it. Layers, bottom up.

The layer of every package is declared once, in `architectureLayers` in
`cmd/f4/architecture_test.go`, and that map is the authority — this list is
written from it. `TestArchitectureLayerMapCoversEveryInternalPackage` fails if a
package under `internal/` is missing from the map, so the map cannot go stale in
silence; this prose can, and the check for that is at the end of this section.

**Layer 0 — leaves, importing no package of ours but `internal/ini`:**
`vfs`, `sdk`, `internal/action`, `internal/appcmd`, `internal/colorer`,
`internal/config`, `internal/frameborder`, `internal/history`, `internal/i18n`,
`internal/ini`, `internal/keymap`, `internal/luaplug`, `internal/netproxy`,
`internal/numeric`, `internal/piecetable`, `internal/semantic`, `internal/sheet`,
`internal/sysinfo`, `internal/testutil`, `internal/textdiff`,
`internal/textsearch`, `internal/theme`, `internal/toast`, `internal/ttyx`,
`internal/unpack`, `internal/vtvibe/ap`, `internal/wheel`, `internal/wincon`.

**Layer 1 — subsystems over the leaves:** `internal/fileops`,
`internal/fusefs` → `vfs`, `internal/textlayout` → `internal/piecetable`,
`internal/diffview` → `internal/textdiff`, `internal/theme`, `internal/i18n`,
`internal/update`, `internal/vtvibe` → `vfs`.

**Layer 2 — plugins and hosts:** `plugins/*` → `vfs`, `sdk`, `internal/*`;
`internal/plughost` → `sdk`, `internal/luaplug`, `vfs`; `internal/gui`.

**Layer 3 — interactive subsystems:** `internal/panel`, `internal/editor`,
`internal/viewer`, `internal/dialog`, `internal/cmdline`, `internal/macro`,
`internal/terminal`, `internal/media`.

**Layer 4 — application:** `internal/app`, then `cmd/f4`. `internal/paneltest`
is layer 4 as well: it builds a panels frame, so it sits above the three
packages that make one, and no production file imports it.

`internal/hideconsole` is in no layer. It is a vendored fork with its own
`go.mod` and module path, so `go list ./internal/...` does not return it and the
auditor is not asked about it.

This prose lists packages and layers by hand, so it can drift from the tree —
and it did, by six packages and four layers, before it was rewritten from the
map. Two commands say whether it has drifted again:

```bash
# every package under internal/ is named here, and nothing here is gone
diff <(grep -oE 'internal/[a-z0-9]+' .ai-factory/ARCHITECTURE.md | sed 's|internal/||' | sort -u) \
     <(ls -d internal/*/ | sed 's|internal/||;s|/||' | sort)

# and the layer each is listed under matches architectureLayers
grep -oE '"internal/\w+": *[0-9]' cmd/f4/architecture_test.go | sort
```

Rules:

- ✅ `cmd/f4` → any package (Composition Root).
- ✅ `internal/app` → every layer 0-3 package; it owns the event loop and the
  global application state that the interactive subsystems share.
- ✅ `plugins/*` → `vfs`, `sdk`, `internal/*` helper packages. Nothing imports a
  plugin back: they are leaves, wired in through `internal/plughost`.
- ✅ Any module → `internal/config`, `internal/i18n`, `internal/theme`,
  `internal/keymap`, `internal/sysinfo`, `vfs`. A layer-0 package imports layer-0
  packages and nothing else, which is what lets `config.App` stay a package-level
  global without creating a cycle: the package can only ever reach layer 0.
- ✅ Higher-layer modules talk to lower ones by calling exported constructors and
  methods; lower ones call back through interfaces they define themselves.
- ❌ `vfs` / `sdk` → any `internal/*` package. They are the public contract: an
  `internal/` import makes them uncompilable for third-party plugins, and the
  breakage surfaces only in someone else's build.
- ❌ `internal/piecetable` / `internal/sheet` → higher-layer packages. Kernel
  subsystems stay leaf nodes; that is what makes them testable in isolation.
- ❌ Any package → `cmd/f4`. It is `package main`; nothing can import it, and
  nothing should want to.
- ❌ Any package → a higher layer. A package at layer N imports layers N and
  below; `internal/app` at layer 4 is the case that matters most, but the rule is
  general and `architecture_test.go` checks every edge, not just that one. Shared
  state flows down through constructor arguments, never up through an import;
  where a lower layer needs something that lives above it, it declares the seam
  and the composition root fills it in.
- ❌ Import cycles between subsystem packages. If two need each other, the shared
  type belongs in a lower layer, or one of them defines an interface the other
  satisfies.
- ❌ Runtime `if runtime.GOOS == …` branching for platform differences. Use
  build-tag files (`*_windows.go`, `*_unix.go`, `*_other.go`).
- ❌ New cgo. FFI goes through `purego` / `ffibridge`.

**Not every directory here is one module.** The repository holds six `go.mod`
files: the main module, `internal/hideconsole` (a vendored fork substituted via
`replace`), and four under `tools/`. `go build ./...` and `go test ./...` see
only the main module's 38 packages — the others are built and tested separately,
which is why a broken test can sit in `tools/` unnoticed. Restructuring must not
rewrite the module path of a vendored fork, and moving one of these directories
means updating the `replace` directive that points at it (`go.mod:187`).

## Layer / Module Communication

- **Composition Root.** `cmd/f4/main.go` parses flags, picks the startup mode
  (terminal, GUI backend, `--update`, plugin scaffolding) and constructs the
  application. Modules do not construct their own dependencies; a `New(...)`
  constructor takes what it needs, so a half-initialised struct is not reachable.
- **Interactive subsystems ↔ app.** `internal/app` owns the event loop and
  dispatches input to the focused subsystem. Panels, editor, viewer and dialogs
  receive the state they need at construction; they do not import `internal/app`
  to reach back for it.
- **Everything filesystem-shaped goes through `vfs`.** Panels, plugins, the viewer
  and FUSE mounting all address files through the `vfs` contract, which is why a
  panel can browse an archive, an SFTP host or an S3 bucket without knowing which.
- **Plugins are hosted, not linked.** `internal/plughost` owns all four transports
  (in-process Go, RPC, Lua via `internal/luaplug`, WASM via `wazero`). The rest of the
  application talks to plugins through the host, never to a transport directly.
  The contract a plugin compiles against is `sdk/`.
- **Rendering is external.** UI and input primitives live in the `vtui` and
  `vtinput` libraries outside this repository. Modules render through them; there
  is no in-tree TUI engine to organise.
- **Platform differences are file-level.** Anything touching the console, the
  filesystem or process spawning gets a per-OS file with a build tag. The exotic
  targets (mips, riscv64, loong64, ppc64, Illumos, Solaris) are part of the
  contract, not a nice-to-have.

## Key Principles

1. **Module = Go package.** One responsibility per package, public API = its
   exported identifiers. The compiler enforces the boundary — this is why breaking
   up `cmd/f4` matters more than any naming convention.

2. **Composition Root in `main.go`.** All wiring in one place; a `New(...)`
   takes what it needs, so a half-built struct is never reachable. Two things are
   banned outright: `init()` with side effects beyond assignment — today
   `queue_manager.go:312` starts a goroutine on import — and mutating a struct
   after construction that another goroutine already reads. A plain package-level
   value in a layer-0 leaf is not covered by this: `config.App` stays a global
   because 133 files read it and its package imports nothing, so it cannot create
   a cycle.

3. **Far heritage stays.** Structures ported from `far2l` / Far Manager keep the
   names of their C++ originals even where Go style would say otherwise. Moving a
   file into a package does not license renaming its types.

4. **Public surface is deliberate.** `sdk/` and `vfs/` are the third-party contract
   and change with care. `plugins/` sits beside them as the reference
   implementation. The application core belongs under `internal/`, where the
   compiler keeps it private.

5. **Tests move with the code.** This is an AI-only codebase where the test suite
   is the review mechanism. A file relocated to a new package takes its
   `_test.go` neighbour along in the same commit; a package extraction that drops
   coverage is not done. Shared test scaffolding gets its own home before the
   packages that use it move: `swapFrameManager` is used by 62 test files and
   `setupMockPanelsFrame` by 28. They cannot share one neutral package:
   `setupMockPanelsFrame` constructs a terminal view, a command line and a file
   panel, so a package holding it imports `panel`, `cmdline` and `term` — and
   those packages' own in-package tests then cannot import it back. The harness
   splits by what it touches: neutral `vtui` glue in `internal/testutil`, the
   frame mock in `internal/paneltest`, and the tests inside the three packages it
   depends on move to `package X_test`. Left undivided, the test import graph
   contradicts the production one.

6. **Portability is a boundary condition.** `CGO_ENABLED=0`, build-tag files, the
   full CI matrix green. A restructuring commit that only builds on the developer's
   own platform is a broken commit.

7. **The repository root is for entry points, not artefacts.** A newcomer should
   reach `README.md` without scrolling. Scripts go to `scripts/`, media to
   `.github/assets/`, prose to `docs/`. Per-issue write-ups belong in
   `docs/ISSUES/` — or, when produced through this harness, in the
   research → plan → archive chain under `.ai-factory/`.

8. **Documentation is part of the change, not its aftermath.** The 48 subsystem
   documents describe where things live, so a change that leaves them stale
   makes them actively misleading — worse than absent. A commit that moves a
   file closes its own references in `docs/`, `README.md` and `AGENTS.md`.

   Per-issue reviews are named `docs/ISSUES/ISSUE_<number>_<SLUG>.md` in
   SCREAMING_SNAKE — `ISSUE_165_CONPTY_SYNC_MARKER.md`,
   `ISSUE_546_CONPTY_FOLLOWUP.md`. The slug is what makes the directory
   navigable: a name distinguished only by a number can only be found by
   somebody who already knows the number, and `SOLUTION_REVIEW` carried no
   information because every file there is one.

## Package Ownership

Every package answers for one subject, and every file belongs to exactly one
package. The rules below are what keeps it that way.

- **A new file goes to the package that owns its subject.** If none owns it,
  create the package — do not widen a neighbouring one because it is close
  enough, and never park it in `internal/app`. A composition root that accretes
  unrelated code is the flat package growing back one file at a time.
- **`internal/app` holds wiring, not features.** It constructs and connects; it
  does not implement. Code that lands there because nothing else fitted is code
  whose owner was not decided.
- **Cross-package work goes through the lower layer's own interface.** A package
  that needs something from a higher layer declares what it needs and lets the
  caller supply it. Reaching upward through an import, or through a shared
  mutable global, is the same mistake wearing two hats.
- **A method whose type lives elsewhere is a function.** When logic belongs here
  but the type belongs there, write `func doX(t *other.Type)` rather than
  dragging the file into the type's package.
- **The compiler is the reviewer.** `cmd/f4/architecture_test.go` asserts the
  layer rules — no `sdk`/`vfs` import of `internal/`, nothing importing the main
  package, no import of a higher layer, no cycles, and every `internal/*` package
  placed in the layer map, because an unplaced one is unchecked rather than
  exempt. A change
  that needs an exemption there is a change to this document first, not a test
  edit.

**Schema travels with the field; application stays behind.** When a package owns a
setting, it owns the setting's *schema* — the enumeration of what the field may
hold, the parser that normalises it, the default it falls back to. What it does
not own is the *use* of that setting by a running application. `config` holds
`StartupMode` and the function that parses it; the code that acts on a startup
mode lives with the code that starts things. `saveSettingsGroups` does not move
into `config` either — capturing the window geometry, writing the ini and saving
the session is orchestration, and orchestration belongs to whoever orchestrates.

The test is what a change would follow. A new value for an enumeration changes the
type and its parser: schema, so it moves with the field. A new place that reacts to
that value changes a caller: application, so it stays. Applied consistently this
keeps a configuration package from slowly becoming the place where everything that
mentions a setting ends up — the failure this whole layout exists to prevent, in
miniature.

Role separation is the same rule seen from the other side: `sdk/` and `vfs/`
define contracts, `plugins/` implement them, `internal/*` runs the application,
`cmd/f4` wires it together, `tools/` serves developers and ships in nothing. A
file that would do two of these jobs is two files.

## Where New Code Goes

- **A file goes to the package that owns its subject.** If none does, create the
  package. Never `internal/app`: it wires the subsystems together and implements
  none of them, and a file parked there is a file whose home was not looked for.
- **Nothing new in `cmd/f4`.** It is `main.go`, the four module-wide auditors and
  the Windows `.syso`.
- **A test lives with its subject.** A test that spans packages is hosted by the
  highest one it needs; if that pulls it away from private members it must reach,
  split it or make it a `package X_test`. Exporting a symbol so a test can reach
  it from another package is that rule broken and then papered over.
- **Registration order is behaviour, not detail.** The action registry's order is
  what the menus and the command palette display, and Go runs `init()` in
  filename order within a package and in import-graph order across them, so a
  file that moves can reorder the menu with nothing to catch it.
  `TestActionOrderIsStable` in `internal/app` holds that order against a golden
  list; a change to it is a change to the UI and has to be argued for.
- **A change is not done until the prose agrees.** `docs/`, `README.md` and
  `AGENTS.md` name files and packages. A surviving reference to a path that
  moved is an unfinished change, not a follow-up.
- **Do not opportunistically restructure while fixing a bug.** Moving code is its
  own commit, so a behavioural change never hides inside a move diff and a
  reviewer can confirm a rename is a rename.

## Code Examples

### Composition Root — one place that knows every subsystem exists

```go
// cmd/f4/main.go
package main

import "github.com/unxed/f4/internal/app"

func main() {
	app.Main()
}
```

`app.Main` reads the flags, picks the startup mode — terminal, GUI backend,
`--update`, the sudo dispatcher, the plugin scaffolder — and fills in every seam
the lower packages declare, which is what makes it the composition root: the
only function in the tree that names all forty packages.

**It receives nothing and returns nothing, and that is the honest description.**
The shape a composition root is supposed to have is a constructor taking its
subsystems and a `Run` returning an error:

```go
application := app.New(cfg, fs, host, term, left, right)
if err := application.Run(context.Background()); err != nil { … }
```

f4 is not built that way. `vtui.FrameManager`, `config.App`,
`keymap.GlobalHotkeysMgr` and `macro.MacroMgr` are package-level values, read
4490 times across `internal/`, so a constructor taking those things as arguments
would not inject them — it would list them, while the body still reached for the
globals. The signature would assert something the code does not do, which is
worse than the plain call above saying nothing.

Making it true is threading those four through, starting with `config.App`
(1575 of the 4490, read rather than mutated) and starting after the entry point
has a test, which it does not. That is a change of its own, not a rename.

### Dependency direction — a lower layer defines the interface

A lower package declares what it needs from above as a variable with an inert
default, and the composition root fills it in. The declaration is the interface;
the assignment is the only place the two layers meet.

```go
// internal/panel/host.go — what the panels need from the application above
// them. Every default is inert, so an unwired panel declines the command and
// shows no menu, which is wrong in a way somebody notices.
var (
	// AppCommand answers a frame command the panel does not implement, and
	// reports whether it did.
	AppCommand = func(pf *PanelsFrame, cmd int, args any) bool { return false }

	// BuildMenuBarItems builds the menu bar for an area from the action table.
	BuildMenuBarItems = func(area string) []vtui.MenuBarItem { return nil }
)
```

```go
// internal/app/bootstrap.go — the root filling them in.
panel.AppCommand = handlePanelsAppCommand
panel.BuildMenuBarItems = BuildMenuBarItems
```

`internal/terminal` states the same thing as an interface rather than a set of
variables, because what it needs is a coherent group rather than eight
unrelated calls:

```go
// internal/terminal/application.go
type Application interface {
	InitCore() *vtui.ScreenBuf
	InstallImageOverlay()
	OpenEditFile()
	ClientAttached(startLeft, startRight, editPath string)
	// …
}

// App is the live application. The composition root sets it once at startup.
var App Application
```

Both shapes are in use and both are correct; the choice is whether the calls
belong together. What is not permitted is the lower package importing the
higher one to make the call directly, which is what `architecture_test.go`'s
rule 3 checks.

### Platform differences stay in build-tag files

```go
// internal/sysinfo/cpu_info_linux.go
//go:build linux

package sysinfo

func readCPUInfo() (Info, error) { … }   // /proc/cpuinfo
```

```go
// internal/sysinfo/cpu_info_windows.go
//go:build windows

package sysinfo

func readCPUInfo() (Info, error) { … }   // registry
```

### Errors carry context and reach the user

```go
// Error strings frequently become dialog text, so they are written for a human.
// ST1005 is disabled in .golangci.yml for exactly this reason.
if err := fs.Copy(ctx, src, dst); err != nil {
    return fmt.Errorf("copying %q to %q: %w", src, dst, err)
}
```

## Anti-Patterns

- ❌ **Adding a file to `cmd/f4`.** It holds `main.go`, the four module-wide
  auditors and the Windows `.syso`. A feature landing there is the flat package
  growing back one file at a time, and the point of the layout is that the
  compiler, not a convention, keeps subsystems apart.
- ❌ **Parking a file in `internal/app` because nothing else fitted.** It wires
  and does not implement. Code whose owner was not decided is code with an owner
  nobody looked for.
- ❌ **Moving `vfs` or `sdk` under `internal/`.** It compiles here and breaks every
  third-party plugin, because Go forbids importing `internal/` from outside the
  module.
- ❌ **Rewriting inside a move commit.** Renamed identifiers and reshuffled logic
  hidden in a large `git mv` diff cannot be reviewed, and this codebase reviews
  by reading diffs and running tests.
- ❌ **Exporting a symbol so a test in another package can reach it.** The test
  belongs with its subject; the export is the rule broken and then papered over.
- ❌ **Package-level mutable state as a shortcut across a boundary.** A `var
  currentPanel *Panel` in one package read by another is an import cycle that the
  compiler happens not to catch.
- ❌ **Renaming Far-derived types during a move.** `PanelViewSettings` stays
  `PanelViewSettings`; navigability for people who know the Far API is a stated
  project goal.
- ❌ **Runtime OS branching instead of build tags.** It compiles unreachable code
  into every target and inflates a binary that is already ~110 MB.
- ❌ **Packages named after technical layers** (`services/`, `handlers/`,
  `models/`). There is no HTTP, no database and no request lifecycle here; the
  subsystems are the domain.

## Unified Settings Center

`internal/settings` is a layer-3 interactive subsystem. It owns the settings
catalog, draft providers, searchable dialog, inline record editors and generated
option help. It imports the storage and interactive subsystems whose preferences
it edits, but never imports `internal/app`. `settings.Host` supplies session and
geometry saves, runtime refresh, backend discovery and application-owned update
and plugin installation workflows. The composition root implements that interface
and wires the optional plugin contribution/opening capabilities. Contextual panel
editors pass a captured source and callbacks; neither panels nor plugin hosts
import the settings renderer. `sdk/f4settings` remains the frontend-neutral public
metadata and provider contract.

`internal/settingstest` is layer-0 test scaffolding for provider localization audits, imported only by tests. It reads the resources owned by `internal/i18n`.
