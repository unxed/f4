# f4 Plugin Architecture (F4-RPC) & API

`f4` plugins speak **F4-RPC**: a small, highly efficient request/response protocol carried in **MessagePack**. Method names look like `Plugin.Init`, `VFS.ReadDir`, and `Host.Log`. `MessagePack` was chosen over JSON-RPC to keep serialization overhead and network latency low for large `ReadDir` chunks.

One protocol, several transports. The same plugin can run in its own process, communicating over `stdin`/`stdout`, or inside `f4` on an embedded interpreter. Nothing above the transport changes, so a plugin moves between them without being rewritten.

## 1. The Transports

**Subprocess.** `f4` launches an executable and talks to it over pipes. Write the plugin in Go, Python, Rust, C++, Node.js or LuaJIT; anything that can read and write MessagePack on stdio works. The plugin is a native OS process with unrestricted access to the network, native libraries and the filesystem.

**Embedded Lua.** `f4` runs a `.lua` plugin on a built-in interpreter, with no system Lua and no rocks to install. The plugin is a single file. `require('f4rpc')` resolves to a preloaded module, so a script written for the subprocess transport runs here unmodified.

**Embedded wasm.** `f4` runs a `.wasm` plugin on a built-in WebAssembly runtime. The guest is a WASI command reading F4-RPC from stdin and writing it to stdout, exactly as a subprocess plugin does, so the same source builds either way. It is the only transport that is genuinely a sandbox: the guest gets no filesystem, only stdio, a clock and a random source. Native calls reach it through `Host.FFI.*` (see `FFI.md`).

## 2. The F4-RPC Object Model & API

Unlike classic terminal file managers, the `f4` plugin API is strictly **Data-Centric**, split into VFS/Drive operations, Editor Syntax Highlighting, and UI/Global actions.

### A. Virtual Filesystems: Drives vs. Providers

`f4` supports two distinct ways of mounting virtual file systems:
1.  **Drives (External & Internal):** Standalone file systems mounted under a specific label. They are registered during `Plugin.Init` (e.g., returning `{ Drives = { "My Lua Drive" } }`) and appear in the `Alt+F1/F2` drive selection menus.
2.  **VFS Providers (Internal):** High-priority hooks that can intercept path navigation. For example, when you press `Enter` on a `.zip` or `.tar.gz` file, the **Archive Plugin** (an internal Go provider) intercepts the open request and transparently mounts a virtual file system *over* the archive. Similarly, the **NetFox (Network) Plugin** intercepts connection files in `net://` and mounts FTP/SFTP sessions.

To expose a VFS, an external plugin registers handlers for these requests:
*   `VFS.ReadDir`: Returns a list of `VFSItem` objects (Name, Size, IsDir, MTime).
*   `VFS.Stat`: Returns metadata for a single item.
*   `VFS.Open` / `VFS.ReadAt`: Returns a file handle ID and provides random-access byte reading. This enables f4's instant, zero-allocation Viewer and Editor to work over network or archive connections without downloading the whole file.
*   `VFS.Create` / `VFS.Write` / `VFS.MkDir` / `VFS.Remove` / `VFS.Rename`: Standard filesystem mutations.
*   `VFS.ProcessKey`: Allows the plugin to intercept specific keystrokes while the user is browsing its virtual drive.

### B. Searchable commands

`Plugin.Init` may also return a `Commands` array. Each descriptor contains a stable `ID`, `Location` (`0` for the panel plugin menu, `1` for plugin configuration), an English `Label` and optional `Description`, `Shortcut`, localized label/description maps, literal `SearchTerms`, and optional `ActiveDrives`. f4 shows these commands in its normal plugin menus and in the `Ctrl+Shift+P` command palette, indexes every supplied language, and renders the current interface language.

When the user selects a command, f4 calls `Plugin.RunCommand` with its ID. Contributions are tied to the RPC session and disappear when the plugin disconnects. `VFS.ProcessKey` and `Host.RegisterGlobalHotkey` remain supported for compatibility, but they carry no command name or search metadata; plugins should publish a matching command descriptor for every semantic operation reachable only through one of those raw-key callbacks.

The Go SDK exposes this as the optional `f4plugin.CommandProvider` interface, so existing plugins do not need to change. A user can select a command in the `F11` plugin menu and press `F4` to assign or change its f4 shortcut; the binding is stored in the profile's `hotkeys.ini`. See `plugins/dummy_rpc/main.go` for a drive-scoped, multilingual example.

### C. Editor Syntax Highlighting (Highlighter API)

Plugins can colorize files in the text editor dynamically.
1.  During initialization, the plugin calls `Host.RegisterHighlighter`.
2.  When `f4` opens a file, it starts sending lines to the plugin's `VFS.Highlight` endpoint.

**The `VFS.Highlight` Protocol:**
*   **Request Payload (`HighlightReq`):**
    *   `Line` (string): The raw text of the line to highlight.
    *   `Prev` (any): The state returned by the plugin after highlighting the *previous* line. This allows stateful, line-by-line parsing (e.g. tracking whether we are inside a multi-line comment block) without re-parsing the entire file.
    *   `Base` (uint64): The default terminal text attribute (colors).
*   **Response Payload (`HighlightRes`):**
    *   `Attrs` ([]uint64): A parallel array of terminal attribute words (colors/styles), one per rune of the line.
    *   `Next` (any): Any state object the plugin wants to pass to the next line's `Prev` parameter.

### D. Host Callbacks (Plugin -> Host)
Because F4-RPC is a full-duplex protocol, plugins can call back into `f4` at any time:
*   `Host.Log` / `Host.Message` / `Host.InputBox` / `Host.Menu`: Standard UI and debugging interactions.
*   `Host.RunAction`: Triggers any internal f4 semantic action (e.g., `Editor.Save`, `Panel.Swap`).
*   `Host.RunProgressTask` / `Host.UpdateProgress`: Safely offloads long-running plugin operations to f4's background job manager, displaying a progress dialog to the user with standard Cancel functionality.
*   `Host.AskOverwrite` / `Host.AskError`: Invokes f4's native, rich collision/error dialogs (Retry, Skip, Overwrite All, etc.) during mutations.
*   `Host.RegisterGlobalHotkey`: Registers a system-wide hotkey that triggers the plugin's `Plugin.OnHotkey` endpoint regardless of which panel is active.

### E. Panel-only plugins

A plugin does not have to mount a VFS to provide a full-screen panel surface.
Native Go plugins can opt into the optional `vfs.PanelContributionHost` and
register a `vfs.PanelProvider`. f4 publishes one searchable `Open <title>`
command for each provider. Opening it replaces the active file-panel surface
for that slot; the underlying `FileSystemPanel` remains alive as the logical
source for normal file actions.

The host calls the panel controller with the ordinary `vtui` drawing and input
interfaces. Before each draw and input event it supplies a `PanelContext` with
the panel side, screen bounds, active side, current path, cursor name, marked
names, and the corresponding snapshot from the other file panel. `Esc` closes
the panel when the controller does not consume it. This keeps panel plugins
portable and prevents them from reaching into f4's private panel state.

RPC plugins declare panel descriptors in the structured `Plugin.Init` result:

```text
Panels: [{ ID, Title, Description }]
```

The host then uses these calls lazily:

* `Plugin.OpenPanel` receives `{ ID, Context }` and returns a UTF-8 JSON `.vui`
  document in `Document`.
* `Plugin.PanelEvent` receives `{ ID, Kind, Event, Context }`, where `Kind` is
  `key` or `mouse`, and returns `{ Handled, Document, Close }`. `Document` is
  optional; when present it replaces the current widget tree.
* `Plugin.ClosePanel` receives `{ ID }` when the panel is dismissed.

The `.vui` document is rendered by the same `vtui` controls and wire format
used by the bindings in the `vtui` repository. The plugin owns semantic
behavior and may return a new document after any event; f4 owns placement,
focus routing, and panel context delivery. Existing VFS-only plugins continue
to use the legacy response shape unchanged.

## 3. Design Philosophy & Comparisons

If you are coming from the classic Far Manager ecosystem or modern text editors, here is how F4-RPC compares.

### f4 vs. Classic Far Manager (Far3 / far2l / far2m)
*   **Object Model:** Classic Far APIs are *UI-Centric*. A plugin receives raw console events, manually allocates arrays of `PluginPanelItem` C-structs, manages pointer lifecycles, and often hooks directly into the UI drawing loop. F4-RPC is *Data-Centric*. The plugin just returns arrays of standard data structures.
*   **Concurrency:** In classic Far, background operations often required complex threading hacks or blocked the main UI thread. In `f4`, *every* RPC call is inherently asynchronous. If `VFS.ReadDir` takes 5 seconds to fetch data from an FTP server, `f4` simply renders a `[ Loading... ]` placeholder and keeps the UI perfectly responsive.

### Escaping Technical Debt
Do we drag the technical debt of old Far APIs into f4? **No.**
F4-RPC is a completely clean-room, modern design. We intentionally abandoned the legacy C-struct memory model for plugins.
*Note:* We *do* provide a high degree of compatibility for Lua **Macros** (see `MACROS.md`), and we plan to implement a `far2l` Lua API compatibility shim. However, this shim will live entirely on the host side as a translation layer. The core plugin protocol remains strictly modern, decoupled, and free of legacy debt.

### f4 vs. Modern Code Editors (VS Code / Neovim)
The F4-RPC architecture is heavily inspired by modern editors.
*   **VS Code** uses the Language Server Protocol (LSP) over JSON-RPC.
*   **Neovim** uses MessagePack RPC for remote plugins.
Like them, `f4`'s out-of-process architecture ensures **Crash Isolation** (a segfault in your plugin will not kill the file manager), **Security** (WASM sandboxing), and **Language Agnosticism** (write plugins in Go, Python, or Lua).

## 4. Getting Started: Your First Plugin & Publishing

You don't need a compiler or complex SDKs to write your first plugin.

### Step 1: Scaffold the Plugin
Run the built-in generator:
```bash
f4 --new-plugin mydrive
```
This creates a directory named `mydrive` containing a `plugin.lua` and a `manifest.json`.

### Step 2: Understand and Test
Open `plugin.lua`. It contains a fully working, self-documented Virtual File System implemented in about 50 lines of Lua.
To load it:
1. Open `f4`, press `F9` -> **Options** -> **Manage plugins**.
2. Press `Insert` and point it to the `plugin.lua` you just generated.
3. Restart `f4`, press `Alt+F1`, and select **mydrive** from the drive menu.

### Step 3: Publish to PlugRing
Once you've built something cool, share it with the community via **PlugRing**:
1. Host your `.lua` or `.wasm` file somewhere accessible via HTTPS (e.g., GitHub Releases or raw Gists). *Do not publish compiled native binaries.*
2. Fork the `unxed/f4` repository.
3. Create a Markdown file in the `plugring/` directory (e.g., `plugring/mydrive.md`) containing YAML frontmatter and your description. Use the generated `manifest.json` as a guide.
4. Submit a Pull Request! (See `PLUGRING.md` for detailed rules).

## 5. Building a Native Plugin (Go SDK)

For Go developers building high-performance or complex native subprocess plugins, an SDK is provided in `sdk/f4plugin`. It handles all the MessagePack multiplexing and exposes a clean, synchronous Go interface. Check `plugins/dummy_rpc/main.go` for a reference implementation.
