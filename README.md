# f4 — efficient and cozy file manager in go

![](https://raw.githubusercontent.com/unxed/f4/refs/heads/main/screenshot.png)
### ⚡ Quick Download (Nightly Builds)

| Platform | Format | Link |
| :--- | :--- | :--- |
| **Windows** | .zip | [amd64](https://github.com/unxed/f4/releases/download/nightly/f4-windows-amd64.zip) / [arm64](https://github.com/unxed/f4/releases/download/nightly/f4-windows-arm64.zip) |
| **macOS** | .tar.gz | [amd64](https://github.com/unxed/f4/releases/download/nightly/f4-darwin-amd64.tar.gz) / [arm64](https://github.com/unxed/f4/releases/download/nightly/f4-darwin-arm64.tar.gz) |
| **Linux** | .tar.gz | [amd64](https://github.com/unxed/f4/releases/download/nightly/f4-linux-amd64.tar.gz) / [arm64](https://github.com/unxed/f4/releases/download/nightly/f4-linux-arm64.tar.gz) / [armv7l](https://github.com/unxed/f4/releases/download/nightly/f4-linux-arm.tar.gz) |
| **FreeBSD** | .tar.gz | [amd64](https://github.com/unxed/f4/releases/download/nightly/f4-freebsd-amd64.tar.gz) / [arm64](https://github.com/unxed/f4/releases/download/nightly/f4-freebsd-arm64.tar.gz) |
| **DragonflyBSD** | .tar.gz | [amd64](https://github.com/unxed/f4/releases/download/nightly/f4-dragonfly-amd64.tar.gz) |
| **OpenBSD** | .tar.gz | [amd64](https://github.com/unxed/f4/releases/download/nightly/f4-openbsd-amd64.tar.gz) / [arm64](https://github.com/unxed/f4/releases/download/nightly/f4-openbsd-arm64.tar.gz) |
| **NetBSD** | .tar.gz | [amd64](https://github.com/unxed/f4/releases/download/nightly/f4-netbsd-amd64.tar.gz) / [arm64](https://github.com/unxed/f4/releases/download/nightly/f4-netbsd-arm64.tar.gz) |
| **Illumos** (experimental) | .tar.gz | [amd64](https://github.com/unxed/f4/releases/download/nightly/f4-illumos-amd64.tar.gz) |
| **Solaris** (experimental) | .tar.gz | [amd64](https://github.com/unxed/f4/releases/download/nightly/f4-solaris-amd64.tar.gz) |

*These builds are automated and represent the current state of the `main` branch.*

### 🍺 Install on macOS via Homebrew

Tagged releases (`vX.Y.Z`) are published to a Homebrew tap, so you can install with one command:

```sh
brew install unxed/tap/f4
```

To upgrade later: `brew upgrade f4`. Both Apple Silicon (arm64) and Intel (amd64) Macs are supported.

**The Core:** Creating an experimental, cross-platform TUI (Terminal User Interface) file manager that aims to fully replicate the features, UX, data structures, and rendering logic of `far2l` and Far Manager, but implemented entirely in Go.

### Philosophy & Goals

This project is built around several core philosophical and technical principles:

1. **The Go Experiment:** Testing the viability of building a heavy-duty TUI in Go. Go provides cross-platform compilation out of the box, fast development, and zero dependency hell (e.g., an x64 Linux binary runs on any x64 Linux without external library issues).
2. **AI-only. Canaries included:** Every line of code in this project is AI-generated. Instead of manually reviewing every change, we rely on an extensive test suite that serves as canaries in a coal mine: if the AI breaks something, the tests fail first. If you'd like to contribute, please include tests with your changes whenever possible.
3. **Far Heritage:** Copying all successful concepts from Far (screen buffer, frame manager, etc.). Keeping internal structures and their names as close to the original C++ versions as possible to lower the entry barrier for developers familiar with Far APIs.
4. **Consistent UX:** Adherence to a strict set of [Navigation and Interaction Guidelines](UX_GUIDELINES.md) that blend the best of classic TUI paradigms.
5. **Bazaar Policy:** Openness to community contributions and patches.

*Trade-offs:* The compiled binary is currently ~50MB, which might not fit in highly constrained environments like home routers.

### UI & Input

UI & input libraries are developed separately ([vtui](https://github.com/unxed/vtui), [vtinput](https://github.com/unxed/vtui))

*   **Modern Terminals Only:** Primary target is actively developed terminals (Konsole, kitty, iTerm2, Windows Terminal). Other terminals won't allow replicating Far's UI accurately.
*   **Input (`vtinput`):** Built as a separate library to handle advanced protocols like the [Kitty Keyboard Protocol](https://sw.kovidgoyal.net/kitty/keyboard-protocol/) and [Win32 Input Mode](https://github.com/microsoft/terminal/blob/main/doc/specs/%234999%20-%20Improved%20keyboard%20handling%20in%20Conpty.md). This is strictly required for distinguishing combinations like `Ctrl+Enter` or `Shift+Tab`.
*   **Framework (`vtui`):** A custom UI framework built from scratch in the style of Far, borrowing responsive layout features (like window resizing and anchors) from Turbo Vision. Ideally, it should cover all capabilities of Far's UI kit and Turbo Vision (excluding non-relevant features like custom serialization engines).
*   **Word Navigation:** `Ctrl+Left`/`Ctrl+Right` and their `Shift` variants follow the exact word boundary rules of `far2l`, down to its intentional asymmetry between moving and selecting. See [Word Navigation Rules](WORDNAV.md).

### GUI Mode & Backends

`f4` can run either directly in your terminal or as a standalone graphical window. GUI mode is particularly useful on Windows to bypass console limitations or on Linux/macOS for high-performance hardware-accelerated rendering.

**Command Line Options:**
*   `--gui`: Start in GUI mode using the best available backend for your OS.
*   `--gui=gogpu`: Use the hardware-accelerated (GPU) renderer.
*   `--gui=x11`: Use native X11 windowing (Linux/BSD/macOS).
*   `--gui=wayland`: Use native Wayland windowing (Linux/BSD).

Example:
```bash
./f4 --gui=gogpu
```

### Integrated Terminal & OS Integration

*   **Built-in Terminal:** A fully-fledged built-in terminal running underneath the panels, just like `far2l`.
*   **VTE Mirror Architecture:** To handle the complex differences between Unix byte-streams and Windows ConPTY 2D-rendering, the terminal uses a custom hybrid grid-and-extrusion engine. Read the [Terminal Architecture Guide](TERMINAL.md) for details.
*   **Windows Strategy:** Currently, we target recent Windows versions only. Reasons are they support ConPTY and console interfaces working via ESC sequences. At the same time, f4's modular architecture makes it possible to implement rendering via Windows Console API in future (in fact, our Far-compatible internal architecture is ideally suited for this), so if you want f4 to run on your XP box you will not have to write too much code. Similarly, no one is stopping you from writing a layer for f4's built-in terminal that uses winpty instead of conpty to work on older Windows versions.

### Plugin Architecture (Out-of-Process RPC)

`f4` uses an ultra-lean, **Out-of-Process** plugin model communicating via `stdin`/`stdout` using the **MessagePack** binary protocol.

1. **Language Agnostic**: Write plugins in Go, Python, Rust, Node.js, C++, or Lua. If it can speak MessagePack over standard I/O streams, it works.
2. **Native Power**: Because plugins are native external processes, they have full access to the OS (sockets, CGO, external libraries) without the severe restrictions of WASI/WASM sandboxes.
3. **Lua Ecosystem Friendly**: A dedicated [Lua SDK Guide](LUA.md) bridges the gap for developers accustomed to the Far3/far2m Lua API.
3. **Binary Efficiency**: MessagePack minimizes serialization overhead, preventing the input lag usually associated with JSON-RPC.
4. **Internal Plugins:** The most critical components (like `NetFox` or native VFS) are statically linked into the binary but use the exact same `HostAPI` conceptual interface.

### Special Features

1. **Asynchronous VFS:** Built from the ground up to be non-blocking, supporting live streaming of directory contents and lazy-loading of file data. See [VFS Architecture](VFS.md).
2. **FISH+ Protocol:** Remote file management that offloads indexing, searching, patching and long-running jobs to the server. See [FISH+](FISH+.md).
3. **Android Filesystem:** A dedicated Android drive discovers devices through the local ADB server and selects FISH+ or an ADB Sync v1/v2 fallback for the session. See [Android filesystem](plugins/android/README.md).
4. **iPhone Filesystem:** The native iOS drive discovers trusted Apple devices and exposes Media, exported application containers, app groups, and crash reports through AFC, House Arrest, and CoreDevice. See [iPhone filesystem](plugins/ios/README.md).
5. **Media Information:** A bounded, pure-Go media metadata analyzer integrates with F11, Ctrl+Q Quick View, command prefixes, templates, macros, and remote VFS panels. See [MediaInfo plugin](plugins/mediainfo/README.md).
6. **Environment Profiles:** The built-in Environment Manager applies ordered, cross-platform environment profiles to f4 and its local workspace shells. See [Environment Manager plugin](plugins/envman/README.md).
7. **Custom File Highlighting:** Highly flexible file highlighting system supporting glob masks, cross-platform attributes, file sizes, absolute/relative dates, cascade blending, and visual marker glyphs. See [File Highlighting Guide](HIGHLIGHTING.md).
8. **Declarative Localization:** Flexible i18n system for UI and Help files with a built-in "Ctrl+Alt+RightClick" Translator Tool. See [Localization Guide](I18N.md).
9. **FUSE Mounts:** Any file system f4 can open — archives, SFTP/FTP hosts, phones — can be mounted as an ordinary directory, so that programs which know nothing about f4 can read it. See [FUSE Mounts](FUSE.md).

---

### Roadmap

**Phase 1: Foundation (Done)**
*   `vtinput`: Advanced keyboard protocol parsing (Kitty, Win32, Legacy).
*   `vtui` Core: `CharInfo`, `ScreenBuf` double-buffering, zero-allocation `Flush()`.
*   `vtui` Primitives: `ScreenObject`, Dialogs, Menus, Buttons, Edits, Layouts (`GrowMode`).

**Phase 2: Core Application (Done)**
*   Base `f4` UI: Panels, CommandLine, KeyBar, MenuBar.
*   `EditorView` powered by an optimized Piece Table (bracketed paste, UTF-8, zero-allocation render).
*   Built-in Terminal (`TerminalView` + ANSI Parser + Unix PTY integration).
*   Plugin Manager foundation.

**Phase 3: Parity (Current)**
*   Add far2l features starting from requested in Issues.

**Phase 4: Future**
*   Add Far3 and far2m starting from requested in Issues.
*   Flesh out `HostAPI` to support comprehensive wrappers for other Far verisons APIs, implement whose wrappers.
*   Python plugin support.

---

## Development & Contribution Guidelines

To maintain the performance and quality of `f4`, all contributors (including AI assistants) must adhere to these development guidelines:

1. **Licensing and IP Cleanliness:** `f4` is licensed under the **BSD 3-Clause License**. Since `far2l` is GPL-licensed, you **must not** copy, translate, or adapt GPL-licensed code from `far2l` or Far Manager directly. All implementations of Far/far2l concepts must be clean-room, independent rewrites.
2. **Rigorous Testing:** Every new feature, VFS provider, or bug fix must be accompanied by automated tests. Verify your changes by running `go test ./...` before submitting a pull request.
3. **Code Formatting:** Code must be formatted strictly according to Go standards. Always run `gofmt -s -w .` on your changes before committing.
4. **Memory Optimization:** Avoid heap allocations in hot paths or loops to prevent Garbage Collection (GC) latency. Optimize hot spots creatively, utilizing local pooling or zero-allocation paradigms.
5. **FFI and Native Interoperability:** Avoid CGO to preserve easy cross-compilation. If native interoperability is necessary, utilize the `unxed/pureffi` library for fast, non-CGO FFI.
6. **Language:** All code, comments, documentation, and commit messages must be in English to facilitate international collaboration.
7. **Plan-First & Fail-Fast:** For complex tasks, start with a clear plan, break the work into incremental, logical chunks, and focus on failing fast to catch architectural flaws early.

Recommended instruction for LLMs:

> If the task is large, break it down into multiple responses and start with a plan. For complex tasks, use an iterative, incremental approach similar to Agile or RUP. Follow the "fail fast" principle. Write tests for the generated code immediately. Use English for comments and similar elements to facilitate international collaboration. Keep licensing compliance in mind: for example, you cannot copy code verbatim—or nearly verbatim—from a GPL project into a BSD project; you must implement your own solution for the same problem. In garbage-collected languages, avoid allocating memory within hot loops.

---
### Getting Started (Ubuntu)

**1. Install Prerequisites**
Ensure you have Go (1.26 or newer) installed:
```bash
sudo apt update
sudo apt install golang git
```

**2. Setup Project**
Clone the repository:
```bash
git clone https://github.com/unxed/f4.git
cd f4
```

**3. Build**
```bash
cd f4
go mod tidy
CGO_ENABLED=0 go build .
```

The generated platform icons are committed to the repository, so a normal
build does not need an image converter. If `assets/icon/f4.svg` is changed,
regenerate PNG, ICO, ICNS, and Windows resources on any supported OS with:

```bash
go generate
```

**4. Run**
```bash
./f4
```

**5. Debug Mode**
To enable detailed logging to `debug.log`, run with the `--debug` flag:
```bash
./f4 --debug
```

You can also specify a custom log file using `--log`:
```bash
./f4 --log /tmp/f4_trace.log
```

---

###  Architecture

**Why vtui? (vtui vs. tcell + tview/cview)**
While `tcell` and `tview` are industry standards for Go-based terminal applications, `f4` utilizes `vtui` to achieve a higher level of interactive performance and UX consistency tailored for heavy-duty TUIs.

| Criterion | tcell + tview/cview | vtui (f4) |
| :--- | :--- | :--- |
| **Layout Philosophy** | Flexbox/Grid (Web-like) | GrowMode/Anchors (Win32/Turbo Vision) |
| **Focus Handling** | Linear or component-specific | Hierarchical |
| **Keyboard** | General terminfo mapping | Full-featured (kitty/win32 protocols) |
| **Rendering** | Full-widget declarative updates | Bitwise diffing (only changed cells are updated) |
| **Target Use Case** | CLI dashboards | Stateful desktop-class applications |

### Performance Notes

**Instant Bracketed Paste**
To achieve near-instantaneous pasting text via terminal Paste feature for large clipboard buffers (comparable to `far2l`), `f4` utilizes several coordinated strategies:
1.  **Atomic Commits:** The `EditorView` detects `PasteStart` and `PasteEnd` events. Instead of modifying the data model byte-by-byte, it accumulates incoming text in a temporary buffer and performs a single, atomic insertion into the `PieceTable`.
2.  **Busy State Signaling:** Components can signal a `Busy` state to the `FrameManager`. While busy, the UI rendering phase and terminal `Flush()` are entirely suppressed, eliminating visual jitter.
3.  **Event Draining (Burst Processing):** The `FrameManager` implements an "event draining" loop with a micro-timeout. It aggressively consumes all pending input events from the OS buffer before attempting a single render pass.
4.  **Zero-Allocation Rendering:** The `vtui` core minimizes heap allocations during the `Flush()` cycle, sending only the minimum necessary ANSI sequences to the terminal.

### Acknowledgements

f4 is inspired by:

* [Apple Auto Layout](https://developer.apple.com/library/archive/documentation/UserExperience/Conceptual/AutolayoutPG/index.html)
* [ConEmu](https://github.com/ConEmu/ConEmu)
* ConPTY — [Win32 Input Mode](https://github.com/microsoft/terminal/blob/main/doc/specs/%234999%20-%20Improved%20keyboard%20handling%20in%20Conpty.md)
* [DN (DOS Navigator)](https://www.ritlabs.com/en/products/dn/)
* [Far Manager 2/3](https://github.com/FarGroup/FarManager), [far2l](https://github.com/elfmz/far2l/), [far2m](https://github.com/shmuz/far2m)
* [FreeType](https://github.com/freetype/freetype) — auto-hinting
* [Midnight Commander](https://midnight-commander.org/) — FISH/SHELL protocol concept
* [Telegram](https://telegram.org/) — single-binary distribution and automatic updates
* [Turbo Text Editor](https://github.com/magiblot/turbo)
* [Turbo Vision](https://github.com/magiblot/tvision)
* TrueType — [bytecode hinting](https://learn.microsoft.com/en-us/typography/opentype/spec/tt_instructions)
* [Visual Studio Code](https://github.com/microsoft/vscode) — piece table
* [vtm](https://github.com/directvt/vtm)
* [Windows Console API](https://learn.microsoft.com/en-us/windows/console/console-functions) — data types
* [Windows Notepad](https://apps.microsoft.com/detail/9msmlrh6lzf3) — word wrapping
