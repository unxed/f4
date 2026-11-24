# f4 Plugin Architecture (F4-RPC) & API

`f4` plugins speak **F4-RPC**: a small, highly efficient request/response protocol carried in **MessagePack**. Method names look like `Plugin.Init`, `VFS.ReadDir`, and `Host.Log`. `MessagePack` was chosen over JSON-RPC to keep serialization overhead and network latency low for large `ReadDir` chunks.

One protocol, several transports. The same plugin can run in its own process, communicating over `stdin`/`stdout`, or inside `f4` on an embedded interpreter. Nothing above the transport changes, so a plugin moves between them without being rewritten.

The full build includes Android, iOS and CloudFox in-process. They appear directly
in the drive menu and do not require downloading a plugin. Lite builds install
them on demand from the drive menu or PlugRing and run them as subprocess plugins.
A full build ignores installed subprocess copies of these bundled plugins.
Device prerequisites still apply: Android uses ADB; iOS uses usbmuxd or Apple
Mobile Device Support. Cloud connections require the provider's credentials.

In lite, CloudFox's **Add cloud connection**, **Edit cloud connection** and
**Unlock cloud vault** commands are available in F11 and Ctrl+Shift+P. Settings
are entered through cancellable prompts; password, token and vault fields are
masked and excluded from input history. S3 and WebDAV setup is supported, while
Google Drive and Yandex Disk accept existing OAuth tokens. Browser/device-code
authorization and the advanced profile dialog remain available in the full build.

Windows plugin archives contain `.exe` files. The loader also repairs older
archives containing extensionless PE executables. Installation reports success
only after `Plugin.Init` succeeds; a download followed by a launch failure reports
the actual error instead.

## 1. The Transports

**Subprocess.** `f4` launches an executable and talks to it over pipes. Write the plugin in Go, Python, Rust, C++, Node.js or LuaJIT; anything that can read and write MessagePack on stdio works. The plugin is a native OS process with unrestricted access to the network, native libraries and the filesystem.

For credential entry, the Go SDK's `Host.Prompt(PromptRequest)` returns
`PromptResponse{Text, Accepted}` and a transport error. `Password: true` creates a
masked edit with no initial secret or history; cancellation returns
`Accepted: false`. The existing `Host.InputBox` wire format remains supported.
`Host.Profile()` calls `Host.GetProfile` to obtain the host's resolved profile
directory and portable flag; plugins storing configuration must use that value
instead of deriving a profile from their own executable's location.

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
    `Host.InputBox` and `Host.Menu` are refused with an error while f4's interface thread is itself waiting for the plugin (for example inside a `VFS.*` request): a dialog cannot be shown then, and waiting for it would freeze f4. Ask from a command or a hotkey handler instead.
*   `Host.Language`: returns the language codes to try, in order (the interface language, its fallback, then `en`), for a plugin that localizes its own dialogs and messages, the way command labels are localized. The language can change at run time, so ask when building a text.
*   `Host.RunAction`: Triggers any internal f4 semantic action (e.g., `Editor.Save`, `Panel.Swap`).
*   `Host.RunProgressTask` / `Host.UpdateProgress`: Safely offloads long-running plugin operations to f4's background job manager, displaying a progress dialog to the user with standard Cancel functionality.
*   `Host.AskOverwrite` / `Host.AskError`: Invokes f4's native, rich collision/error dialogs (Retry, Skip, Overwrite All, etc.) during mutations.
*   `Host.RegisterGlobalHotkey`: Registers a system-wide hotkey that triggers the plugin's `Plugin.OnHotkey` endpoint regardless of which panel is active.

### E. Panel-only plugins

A plugin does not have to mount a VFS to provide a full-screen panel surface.
Native Go plugins can opt into the optional `vfs.PanelContributionHost` and
register a `vfs.PanelProvider`. f4 publishes one searchable command for each
provider, labelled with its title. Opening it replaces the active file-panel surface
for that slot; the underlying `FileSystemPanel` remains alive as the logical
source for normal file actions.

The host calls the panel controller with the ordinary `vtui` drawing and input
interfaces. Before each draw and input event it supplies a `PanelContext` with
the panel side, screen bounds, active side, current path, cursor name, marked
names, and the corresponding snapshot from the other file panel. `Esc` and
plain `F10` close the panel when the controller does not consume them (F10 is
Quit elsewhere; under a panel plugin it returns to the file panel, and the
keybar caption says so). This keeps panel plugins
portable and prevents them from reaching into f4's private panel state.

A panel plugin's own keys go through one shared primitive instead of a
hand-written `ProcessKey` switch: the controller also implements
`vfs.PanelKeyProvider` and returns `[]vfs.PanelKey` -- virtual key,
modifiers, an already-localized keybar caption, a `Run` callback and an
optional `Enabled` predicate. While a panel plugin has the focus in the
active slot, f4 applies the same rules to every panel plugin, whether it
declares keys or not:

* a declared key runs before any global plugin hotkey or configured f4
  hotkey, including keys injected by a click on the keybar;
* bindings that act on the hidden file panel's cursor or selection (every
  `File.*` action, the group-selection actions and the file-panel navigation
  ones such as `Panel.GoParent`/`Panel.EnterDirectory` on Ctrl+PgUp/Ctrl+PgDn) and global plugin
  hotkeys stand down, so their keys reach the controller's `ProcessKey`
  instead; the hidden file panel itself never receives keys;
* the keybar shows the declared captions, blanks the file-panel captions and
  keeps every other f4 binding (Help, menus, panel toggles); F10 is captioned as closing the panel.

A folder bookmark taken while a panel plugin covers the directory remembers
that plugin (`Plugin=f4-panel:<provider ID>` in `bookmarks.ini`) and opens it
again over the directory. A controller that also implements
`vfs.PanelStateProvider` (`SavePanelState`/`RestorePanelState`, an opaque
one-line string such as the selected entry) gets its state stored in the
bookmark's `PluginData` and handed back after the panel reopens; state it does
not recognise must be ignored. A provider that is not registered on the
machine leaves the directory open and shows a message.

A VFS whose `GetPath()` is not an address the host can reopen (a remote
session's, which is the folder on the server) implements
`vfs.BookmarkPathProvider`: `BookmarkPath(cursor)` returns the address a
folder bookmark or a drive-menu link made on it stores, typically a URI the
plugin's own URI provider opens (NetFox: `net://<connection>/<folder>`,
[NETFOX.md](NETFOX.md)). `cursor` is the entry under the panel cursor, so a
list of connections can give the address of the one the cursor is on. The host
asks when a bookmark or a link is made and nowhere else; folder history has
`vfs.HistoryPathProvider` for its own rules.

`vfs.DispatchPanelKey` is the host's dispatcher, exported so a controller can
route the same declarations from its own `ProcessKey`.

RPC plugins declare panel descriptors in the structured `Plugin.Init` result:

```text
Panels: [{ ID, Title, Description, Help, LocalizedHelp }]
```

`Help` is optional Markdown shown on F1 in f4's Markdown viewer, titled with the
panel's `Title`; `LocalizedHelp` maps language codes to translations and wins when
it has the interface language (then its fallback languages, then `en`). F1 is
captioned on the keybar. A panel that declares its own F1 key keeps it, and a panel
without help leaves F1 to f4's global Help.

The host then uses these calls lazily:

* `Plugin.OpenPanel` receives `{ ID, Context }` and returns a UTF-8 JSON `.vui`
  document in `Document`.
* `Plugin.PanelEvent` receives `{ ID, Kind, Event, Context }`, where `Kind` is
  `key` or `mouse`, and returns `{ Handled, Document, Close }`. `Document` is
  optional; when present it replaces the current widget tree.
* `Plugin.ClosePanel` receives `{ ID }` when the panel is dismissed.

An RPC panel declares its keys (the same `vfs.PanelKey` contract as in-process
panels, see above) by adding `Keys` and `HasKeys: true` to the `OpenPanel` and
`PanelEvent` answers. Each key is `{ VK, Mods, Label, Disabled }`; a declared
key is delivered as an ordinary `PanelEvent` of `Kind` `key`, and a `Disabled`
one is consumed by f4 without a call. f4 keeps the last declaration it
received (it never asks for it while drawing the keybar), so a plugin re-sends
the set with any answer after which it may differ; an answer without `HasKeys`
keeps it, and `HasKeys` with no `Keys` clears it. Plugins that never set
`HasKeys` behave exactly as before, and f4 builds that predate panel keys
ignore the fields. In Go, implement `f4plugin.PanelKeyProvider` next to
`f4plugin.PanelProvider`: the SDK attaches the set to every answer and also
consumes disabled keys under older f4 builds; `plugins/dummy_rpc` has a
counter panel that uses it.

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

## 6. Full-screen viewer providers

In-process plugins may optionally implement `vfs.ViewerContributionHost` on the
host and register a `vfs.ViewerProvider`. `CanView` is a quick, local check;
providers are tried by descending priority. `OpenViewer` may return
`vfs.ErrViewerUnsupported` if it cannot handle the selected file after all;
the next matching provider is tried. If every provider declines, the caller
falls back to f4's ordinary viewer. Other errors are returned to the caller so
it can report a real plugin failure rather than silently masking it. A
successful provider returns a `vtui.Frame` for the host to display.

`ViewerRequest.Panel` is a captured snapshot of the source panel: entries in
display order with their host URIs and metadata, the cursor URI, and selected
URIs. It remains tied to the panel that opened the viewer even if the user
changes the active panel. `ViewerRequest.HostVFS` is the optional read-only
host-URI opener; its handles support size and random-access reads and should be
closed by the provider. Pass the viewer context to open/read calls so the host
can cancel outstanding I/O when it cancels that context.

## Grouping virtual panels

Any ordinary file panel backed by a VFS supports the same independent Group by
modes as native panels. `sdk/f4plugin.VFSItem` includes the additive metadata
mask and optional physical size, times, permissions, ownership, Windows
attributes, link and extension flags. The numeric mask bits match `vfs`; set
`MetadataExplicit | MetadataUID` to report UID 0 without inventing other fields.
Old MessagePack/RPC and embedded Lua tables can omit these fields unchanged.
Unknown fields remain unknown; a nonempty `Mode` is an opaque grouping value.
See [VFS metadata availability](VFS.md#metadata-availability-and-panel-grouping).

The semantic panel model adds `groupBy`, `groupReverse`,
`groupFoldersSeparately`, `displayTop`, and `groups`. Each group contains `key`,
`title`, `startIndex`, and `count`. Ranges index the file-only `entries` array.
`cursor`, `top`, actions and counts still refer to real files; `top` is the first
file at or after the visual scroll origin. `displayTop` includes heading rows.
