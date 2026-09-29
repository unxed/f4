# AGENTS.md

> Structural map of the repository for AI agents and new contributors. Keep it
> factual — describe only what exists. Update it when the structure changes.

## Project Overview

`f4` is a cross-platform TUI file manager written entirely in Go that reproduces the
features, UX and internal structures of `far2l` / Far Manager. It ships as a single
static binary and runs either in a terminal or as a standalone graphical window.

## Tech Stack

- **Programming language:** Go 1.26.6, `CGO_ENABLED=0`
- **Framework:** none — custom TUI; UI and input come from the external `vtui` and
  `vtinput` libraries
- **Database:** none for the application; `plugins/sqlite` browses user SQLite files
- **Lint:** golangci-lint v2 (staticcheck, errcheck, ineffassign, unused, gosec)

## Project Structure

```
cmd/f4/          # the composition root: main.go, four module-wide auditors,
                 # and the Windows .syso resources the linker takes from here
internal/        # everything the application is, in layers
  app/           #   layer 4: the action table, the event loop, the bootstrap;
                 #   the only package allowed to import every other one
  panel/         #   layer 3: the panels frame and the file panel
  editor/        #   layer 3: the editor view
  viewer/        #   layer 3: the viewer
  terminal/      #   layer 3: the terminal view, PTY sessions, ConPTY
  cmdline/       #   layer 3: the command line and apply-command
  dialog/        #   layer 3: dialogs, help, settings screens
  media/         #   layer 3: images, audio, video
  macro/         #   layer 3: the macro engine
  plughost/      #   layer 2: the plugin host and its registries
  gui/           #   layer 2: the GUI backends
  fileops/       #   layer 1: file operations and the operation queue
  update/        #   layer 1: the updater
  fusefs/        #   layer 1: FUSE mounting
  textlayout/    #   layer 1: text layout and wrapping
  vtvibe/        #   layer 1: the vtvibe session/provider layer
                 #   layer 0 leaves, imported by anything above them:
  action/        #     the action registry
  appcmd/        #     frame command constants
  colorer/       #     the colour scheme f4 installs for colorer4go
  config/        #     configuration and the profile directory
  history/       #     command, folder and view/edit history
  i18n/          #     the message catalogue and the .lng files
  ini/           #     the INI parser
  keymap/        #     key names, remapping and the hotkey manager
  luaplug/       #     the Lua plugin engine
  netproxy/      #     network proxy
  numeric/       #     numeric helpers
  piecetable/    #     the piece table backing the editor
  semantic/      #     the GUI semantic protocol's shared fields
  sheet/         #     spreadsheet mode
  sysinfo/       #     drives, CPU, memory
  textsearch/    #     text search
  theme/         #     colours, styles, file highlighting
  toast/         #     transient notifications
  ttyx/          #     tty extensions
  unpack/        #     archive extraction
  wincon/        #     Windows console
  wincondrag/    #     #1604: drag files out of a Windows console (Burlak's tool window)
  winex11drag/   #     #566 steps 1-2: f4's X11 window, an X connection from Wine (unwired)
  hideconsole/   #     a vendored fork, console hiding on Windows
  testutil/      #   test scaffolding shared across packages; _test.go use only
  paneltest/     #   the same, for helpers that need a panels frame
vfs/             # filesystem abstraction used by every panel and plugin
  hostfs/        #   host filesystem access
  hostmode/      #   host console mode
  hostpath/      #   path translation
plugring/        # community catalogue of installable plugins: data, not a
                 # package; a contributor adds an entry here and opens a PR
plugins/         # one package per plugin: archive, cloudfox, netfox, mediainfo,
                 # envman, ios, android, sqlite, visren, id3editor, chroma
                 # dummy_internal / dummy_rpc / dummy_lua are transport fixtures
sdk/             # plugin API: f4plugin, f4rpc, lua, extui
tools/           # developer tooling, incl. the ttytest terminal harness and
                 # releasecheck, the release gate (docs/UPDATER.md)
docs/            # 56 subsystem documents — read the relevant one before editing
packaging/       # distribution packaging and the Nix Home Manager module
artifacts/       # build artifacts
.ai-factory/     # AI Factory context: config, description, rules, plans
```

## Key Entry Points

| File | Purpose |
| --- | --- |
| `cmd/f4/main.go` | The entry point: one call to `app.Main` |
| `internal/app/bootstrap.go` | CLI flags, startup mode selection, and the wiring of every subsystem's seam |
| `internal/app/api.go` | `coreAPI`, the host surface plugins are given |
| `internal/app/actions_table.go`, `internal/action/registry.go` | Action definitions and dispatch |
| `embedded.go` | Assets embedded into the binary |
| `go.mod` | Module `github.com/unxed/f4`, Go 1.26.6, dependency set |
| `flake.nix`, `flake.lock`, `packaging/nix/` | Nix package, overlay, dev shell and Home Manager module |
| `f4.example.ini` | Reference configuration file |
| `highlight.ini` | Syntax highlighting configuration |
| `.golangci.yml`, `.golangci-strict.yml` | Lint configuration |
| `.github/workflows/build.yml` | CI: cross-platform build matrix, tests, releases |

## Documentation

| Document | Path | Description |
| --- | --- | --- |
| README | `README.md` | Project overview, downloads, backends, philosophy |
| Subsystem docs | `docs/*.md` | 56 documents: VFS, PLUGINS, MACROS, KEYMAP, TERMINAL, CONPTY, WINCON, UX_GUIDELINES and others |
| Updater | `docs/UPDATER.md` | Release asset names as an interface with every installed f4, the release check, the rollback |
| Archive libraries | `docs/ARCHIVE_DEPENDENCIES.md` | The chain of archive libraries (zipper, zip, tar, xz, sevenzip, archives, rardecode) and the rules for updating them |
| Issue reviews | `docs/ISSUES/` | Per-issue solution reviews |
| Spreadsheet | `docs/SPREADSHEET.md` | Spreadsheet mode specification |

## AI Context Files

| File | Purpose |
| --- | --- |
| `AGENTS.md` | This structural map of the repository |
| `.ai-factory/DESCRIPTION.md` | Project specification: stack, features, architecture notes |
| `.ai-factory/ARCHITECTURE.md` | Architecture pattern, boundaries and dependency rules |
| `.ai-factory/rules/base.md` | Detected code conventions: naming, errors, logging, tests |
| `.ai-factory/config.yaml` | AI Factory configuration: paths, language, git workflow |
| `.mcp.json` | MCP servers for this project: the gopls Go language server |

## Agent Rules

### Code navigation (gopls MCP)

- The MCP server is the Go language server itself: `.mcp.json` runs `gopls mcp`.
  Nothing is indexed into the repository — gopls reuses the same build cache as
  `go build`.
- **It is optional, so check before you reach for it:** `command -v gopls`. When
  it is absent the server never starts and its tools are simply not offered —
  fall back to `grep`, `go doc` and `go list`, and say that navigation ran
  without it. Do not install it as a side effect of another task; the one-time
  setup is `go install golang.org/x/tools/gopls@latest` with
  `$(go env GOPATH)/bin` on PATH.
- Answers come from `go/types`, so they are the compiler's view of the code
  rather than a text match: a symbol resolves inside its own package even where
  the name repeats, and files behind another platform's build tag still resolve.
- Use it instead of `grep` for symbol questions. The tree is 69 packages and a
  symbol's package is not always the one its name suggests, so grep over the
  whole module is both slow and imprecise:
  - `go_search` — find a symbol by name across the workspace
  - `go_symbol_references` — every reference to one symbol
  - `go_package_api` — the exported surface of a package
  - `go_file_context` — what a file declares and what it depends on
  - `go_diagnostics` — build and vet errors for the files you just changed
- The server follows edits on its own; there is nothing to rebuild after a rebase.
- Grep stays the right tool for text that is not a symbol: comments, error
  strings, build tags, config keys.

### Where new code goes

- Put a new file in the package that owns its subject. If no package owns it,
  create one — do not widen a neighbouring package because it is close enough,
  and never park the file in `internal/app`.
- `internal/app` is the composition root: it wires packages together and does not
  implement features. Code that lands there for lack of a better place is code
  whose owner was not decided.
- Do not add to `cmd/f4`. It holds `main.go`, the wiring tests, the module-wide
  auditors and the Windows `.syso` files, and nothing else.
- Inside a package, name files `<topic>.go` and `<topic>_<aspect>.go`, where the
  prefix is the topic inside the package, not the package name — `panel/frame.go`,
  never `panel/panel_frame.go`. Platform suffixes go on the end:
  `frame_dragdrop_windows.go`.
- Need something from a higher layer? Declare an interface in your package and
  let the caller supply the implementation. Never import upward, and never reach
  across a boundary through a shared mutable global.
- Logic belongs here but the type belongs elsewhere? Write a function taking the
  type, not a method — a method would drag the whole file into the type's
  package.
- `cmd/f4/architecture_test.go` enforces the layer rules. If a change needs an
  exemption there, the architecture document is what changes first, not the test.
- The full rules, with the reasoning, are in `.ai-factory/ARCHITECTURE.md`.

### Archive libraries

- Before changing a `go.mod` line for zipper, zip, tar, xz, sevenzip, archives,
  rardecode or their helpers, read `docs/ARCHIVE_DEPENDENCIES.md`.
- Libraries are referred to by tag only, never by commit or branch.
- After such a change, run `scripts/check_archive_deps.sh` and commit only when
  it prints `OK`.

### Go build cache

- Use the system Go build cache reported by `go env GOCACHE` for all Go builds and tests.
- Do not redirect `GOCACHE` to `/tmp`, the repository, or another task-local directory unless the user explicitly asks for it.
- If the system cache is unavailable or not writable, report that constraint instead of silently creating a substitute cache.

### Shell commands

- Run shell commands one step at a time instead of chaining them, so a failing step is visible.
  - Wrong: `git checkout main && git pull`
  - Right: first `git checkout main`, then `git pull origin main`

### Portability

- `CGO_ENABLED=0` must stay: the single static binary depends on it. FFI goes through `purego` / `ffibridge`.
- Platform differences belong in build-tag files (`*_windows.go`, `*_unix.go`), not runtime branching.
- Changes must keep the full CI matrix building, exotic targets included.

### Tests

- This is an AI-only codebase; the test suite is the review mechanism. New behaviour lands with a test, a bug fix lands with a regression test.
