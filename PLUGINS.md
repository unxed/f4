# f4 Plugin Architecture

f4 uses a hybrid, in-process plugin architecture designed to be secure, fast, and cross-platform. It avoids the latency of JSON-RPC while maintaining isolation.

## Core Concepts

The architecture relies on the "Adapter" (Wrapper) pattern. The application (`f4`) exposes a single, modern Go interface called `HostAPI`.

Plugins are not executed via `os/exec` or RPC. They run in the same memory space but within sandboxed runtimes.

We support four types of plugins:
1. **Internal Plugins**: Compiled directly into the `f4` binary. Used for critical, performance-sensitive features (e.g., SFTP, VFS).
2. **Modern WASM Plugins**: Written in Go (or Rust/Zig) and compiled to `wasm32-wasi`. They use the modern `f4` API.
3. **far2l Compat WASM Plugins**: Written in C/C++ using the legacy Far Manager / far2l headers.
4. **Lua Scripts**: Loaded via `gopher-lua`. Provides far2m/far3 compatible API (`far.Message`, `far.AdvControl`).

## Why WebAssembly (WASM)?

Traditional Far plugins use dynamic libraries (`.dll`, `.so`). This causes several issues:
- **Dependency Hell**: Requires separate builds for Windows, Linux (amd64, arm64), macOS, etc.
- **CGO Requirement**: Forces the Go host to use CGO, complicating cross-compilation.
- **Stability**: A segfault in a C++ plugin crashes the entire file manager.

WASM solves this:
- **100% Portability**: One `.wasm` file runs everywhere.
- **Zero CGO**: The `wazero` engine executes WASM natively in Go.
- **Sandboxing**: Memory panics in the plugin are trapped as normal Go errors. The host survives.

## Far2l C-API Compatibility Layer

To support legacy C/C++ plugins without source modification, we implement a memory thunking bridge in `far2l_compat.go`.

**How it works:**
1. The C plugin is compiled to WASM using `clang --target=wasm32-wasi`.
2. The Go host allocates memory inside the WASM guest's linear memory.
3. The Go host constructs the `PluginStartupInfo` struct inside this memory.
4. The function pointers inside `PluginStartupInfo` point to WebAssembly import indices.
5. These imports are intercepted by `wazero` and routed to Go methods (e.g., `far2lMessage` -> `HostAPI.Message`).
6. Finally, Go calls the WASM exported function `SetStartupInfoW` with the pointer to the struct.

## Future Roadmap

To develop this foundation into a fully-fledged system, the following steps are required:

1. **Guest Memory Management**: Expose `malloc` and `free` from the WASM guest so the Go host can dynamically allocate structs like `PluginPanelItem` and `PluginStartupInfo` safely.
2. **Complete HostAPI**: Extend the `HostAPI` interface with methods for interacting with `vtui` (Dialogs, Menus, InputBoxes).
3. **Virtual File System (VFS)**: Add `GetFindData` and `GetOpenPluginInfo` wrappers to allow plugins to act as virtual panels (e.g., Archives, FTP).
4. **Lua Expansion**: Map the rest of the Lua far3 API to `gopher-lua` tables.
