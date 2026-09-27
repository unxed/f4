# ProcList

A live list of running processes, shown as a panel. f4#312: part 1 shipped
Linux (view-only), part 2 added Windows and macOS (still view-only), part 3
added process management (kill, priority, suspend/resume), and part 4 (this
update, the ticket's last) added an F3 process-details view and ProcList.Config,
an F9 "Plugin configuration" entry for visible columns and refresh interval.

## What it does

- Registers a `vfs.PanelProvider` (`internal/plughost/panel_providers.go`) --
  f4's first in-tree consumer of that API. Opening it replaces the active
  panel slot with a `vtui.Table` listing every process the platform's
  collector could read: PID, name, resident memory (`Mem`) and `CPU%`.
- Refreshes itself roughly twice a second from a background goroutine, in
  the same "ticker + `RunOnUI(vtui.FrameManager.Redraw)`" pattern
  `internal/app/arkanoid.go` uses for its game loop. All table mutation
  still happens on the UI goroutine; the background goroutine only collects
  and hands the result to `RunOnUI`.
- CPU% is computed the way `top` does on every platform: the delta of
  cumulative kernel+user CPU time between two samples, divided by the
  elapsed wall time -- not divided by the number of CPUs, so a busy
  multi-threaded process can read above 100%. The unit that cumulative time
  arrives in differs per platform (Linux: `/proc/[pid]/stat`'s `utime`+
  `stime` in clock ticks; Windows: `GetProcessTimes`' kernel+user
  `FILETIME`, 100ns units; macOS: libproc's `pti_total_user`+
  `pti_total_system`, nanoseconds) but the delta-over-wall-time math is the
  same collector-side logic on all three (`compareSamples`/`sample` in
  `panel.go`, format helpers alongside it).
- Sortable by any column (click a header; default sort is CPU%
  descending) and has type-to-filter (`Table.QuickSearch`) across all
  columns.
- Reachable from the plugin menu/command palette ("Open ProcList", added
  automatically by `RegisterPanelProvider`) and from **Commands -> Process
  list** / **Ctrl+Alt+R**, on every platform `Supported()` reports `true`
  for.

## Process management (f4#312 part 3 of 4)

The FAR3 ProcList reference (`Plist.cpp`/`Pclass.cpp`) has F8 kill and
Shift-F1/F2 priority; this plugin adds the same two, plus a third gesture
FAR3 does not have at all:

- **F8 -- kill.** Shows a confirmation dialog styled the same way
  `internal/app/actions.go`'s permanent-delete dialog is (`IsWarning`, Cancel
  focused by default, "this action cannot be undone"), then sends an
  unconditional kill on confirmation: `SIGKILL` on Linux/macOS,
  `TerminateProcess` on Windows -- never a graceful request, matching what F8
  does in FAR3. A failure (no permission, already gone) reaches the user as
  a toast; success shows nothing extra -- the process simply drops out of
  the next refresh, the same way any other exit does.
- **Shift+F1 / Shift+F2 -- lower/raise priority.** Both platforms expose the
  same six-rung ladder (Idle, Below normal, Normal, Above normal, High,
  Realtime) FAR3's Windows-only Shift-F1/F2 already cycles through
  (`SetPriorityClass`); `collector_linux.go`/`collector_darwin.go`'s
  platform, `actions_unix.go`, maps it onto nice values (19, 10, 0, -5, -10,
  -20) since *nix has no named priority classes. There is no priority
  column in the table (a part 4/detail-view candidate), so every change --
  success or failure -- gets a toast.
- **Ctrl+F8 -- suspend/resume (toggle).** Not a FAR3 gesture -- its ProcList
  has no suspend/resume at all -- added because `SIGSTOP`/`SIGCONT` make it
  essentially free on Linux/macOS. **Not available on Windows**: it has no
  supported, documented API for suspending an arbitrary process (the
  undocumented NT `NtSuspendProcess` is exactly the kind of API this plugin
  already refuses to use elsewhere, see "What it deliberately does not do"
  below), so the owner's guidance for this part was not to force it through
  one. The toggle direction is remembered per pid by this panel itself
  (which pid it last suspended), not read back from the OS -- there is no
  portable, privilege-free way to read "is this process currently stopped"
  either, the same gap that keeps this out of the table as a column.

None of the three needs a setting to turn off: kill's confirmation is
unconditional, matching how narrowly this plugin scopes everything else.

## Process details and configuration (f4#312 part 4 of 4)

- **F3 -- process details.** A read-only snapshot dialog (`details.go`),
  FAR3's own F3 gesture: command line, environment and open files, each
  independently readable or not (`procDetailsSection`) -- a process whose
  environment this user cannot read still shows its command line and open
  files. Content comes from `collectProcDetails`, one real implementation per
  platform (`details_linux.go`/`details_windows.go`/`details_darwin.go`),
  cheaply-available-only, not necessarily on par with Linux:
  - **Linux**: the real thing, straight out of `/proc/[pid]/cmdline`,
    `/proc/[pid]/environ` and `/proc/[pid]/fd` (each entry's symlink target).
  - **Windows**: only the executable's own full path
    (`QueryFullProcessImageName`) -- the true command line and any open
    handle listing both need undocumented NT APIs this plugin already
    refuses elsewhere (see "What it deliberately does not do").
  - **macOS**: also only the executable's own full path (libproc's
    `proc_pidpath`) -- environment and open files would need a hand-parsed
    `sysctl KERN_PROCARGS2` buffer or a second libproc struct
    (`vnode_fdinfowithpath`), the same "no CI-executed verification" risk
    `collector_other.go` already declined for the BSDs in part 2.
- **ProcList.Config -- F9 "Plugin configuration".** A checkbox per table
  column (`Settings.VisibleColumns`) and a refresh-interval field in
  milliseconds (`Settings.RefreshIntervalMS`, 100..60000), persisted to
  `<configDir>/plugins/proclist.json` the same atomic-JSON way
  `plugins/mediainfo/settings.go` persists its own settings
  (`settings.go`/`config_dialog.go`). A changed refresh interval applies to
  an already-open panel within one refresh cycle; a changed column selection
  applies the next time the panel is opened (rebuilding the table's columns
  live is more than this ticket asked for). At least one column must always
  stay visible -- `Settings.validate` refuses to save a configuration that
  would leave none.

## Platform support

- **Linux** (`collector_linux.go`): every readable `/proc/[pid]` entry --
  `/proc/[pid]/stat` for name and CPU ticks, `/proc/[pid]/status` for
  `VmRSS`.
- **Windows** (`collector_windows.go`): a Toolhelp32 snapshot
  (`CreateToolhelp32Snapshot`/`Process32First`/`Next`) for PID and image
  name, `GetProcessTimes` for CPU and `psapi.dll`'s `GetProcessMemoryInfo`
  (loaded the same hand-written-struct way `internal/sysinfo/mem_windows.go`
  loads `GlobalMemoryStatusEx`) for working-set size. No WMI, matching the
  owner's decision (f4#312) not to port FAR3 ProcList's WMI-backed metrics.
- **macOS** (`collector_darwin.go`): `sysctl kern.proc.all`
  (`golang.org/x/sys/unix.SysctlKinfoProcSlice`) for PID and name --
  `kinfo_proc`'s own memory/CPU fields are widely known to be stale
  BSD-compatibility leftovers on modern XNU (`x/sys/unix`'s own `KinfoProc`
  even names the relevant embedded fields `Dummy`) -- so CPU% and memory
  instead come from libproc's `proc_pidinfo(PROC_PIDTASKINFO)`, loaded via
  `github.com/ebitengine/purego` the same way `cpu_windows.go` loads
  `pdh.dll`'s counters: no cgo, with `readDarwinTaskInfo` refusing to trust
  the result unless `proc_pidinfo` reports back exactly `sizeof(proc_taskinfo)`
  bytes filled.
- **FreeBSD/NetBSD/OpenBSD**: still the `collector_other.go` stub
  (`Supported() == false`) after part 2. Each exposes its own, differently
  laid out `kinfo_proc`/`kinfo_proc2`, `golang.org/x/sys/unix` defines none
  of them, and -- unlike Windows/macOS -- none of the three has a
  GitHub-hosted runner at all, not even for a one-off `sandbox.yml` manual
  check (its OS choices are limited to ubuntu/windows/macos). A hand-rolled
  struct layout for any of them would only ever be cross-compile-checked by
  CI, never executed, which is a real risk for code that reads raw kernel
  memory by hand: see `collector_other.go`'s own comment.
- Everywhere else (illumos, solaris, dragonfly, js/wasm, ...): the same
  `collector_other.go` stub.

## What it deliberately does not do

- **Suspend/resume on Windows.** See "Process management" above -- no
  supported API, so Ctrl+F8 does nothing there rather than reaching for an
  undocumented one.
- **A true command line, environment or open-handle list on Windows/macOS.**
  See "Process details" above -- both would need an undocumented NT API
  (Windows) or a hand-parsed, CI-unverifiable buffer layout (macOS), the same
  class of risk this plugin already declines elsewhere.
- **Anything else FAR3's ProcList shows**: PPID, thread count, start time,
  WMI performance counters, remote/network process lists. WMI-perf-counters
  and the handle viewer are Windows/NT-specific and not planned to be ported
  at all; PPID/thread count/start time are simply not asked for by f4#312's
  four parts.

## Layout

- `plugin.go` -- `Plugin` (`Init`/`Close`/`GetName`), registers the panel
  provider and, where the host supports it, `ProcList.Config`'s F9 entry. No
  build tag: it defers to `Supported()`, and it (and `settings.go`, below)
  must build on every platform this module targets, not only the three
  `Supported()` can return true for -- see `settings.go`'s own comment.
- `collector_linux.go`, `collector_windows.go`, `collector_darwin.go` -- one
  real collector per supported platform, each defining the same
  package-private shape: `Supported`, `sample`, `collector`/`newCollector`,
  and `(*collector).collect`.
- `actions.go` -- the platform-agnostic process-management handlers (F8 kill
  confirmation dialog, Shift+F1/F2 priority, Ctrl+F8 suspend/resume toggle),
  built on top of the platform-specific primitives below. Shares
  `panel.go`'s build tag (`linux || windows || darwin`).
- `actions_unix.go`, `actions_windows.go` -- one real implementation per
  platform of `killProcess`, `changePriority`, `suspendProcess`,
  `resumeProcess` and the `suspendResumeSupported` constant, the same
  per-platform split the collectors already use.
- `details.go` -- F3's dialog (`showDetails`) and the `procDetails`/
  `procDetailsSection` shape `collectProcDetails` fills in per platform.
  Shares `panel.go`'s build tag.
- `details_linux.go`, `details_windows.go`, `details_darwin.go` -- one real
  `collectProcDetails` per supported platform, the same per-platform split
  the collectors and process-management primitives already use.
- `settings.go` -- `Settings`/`DefaultSettings`/`settingsStore`
  (load/validate/save, atomic JSON at `<configDir>/plugins/proclist.json`),
  and `validColumnKeys`/`defaultColumnKeys`/`columnKeysValid`. No build tag
  (see its own comment) -- `columns_sync_test.go` (which does have `panel.go`'s
  build tag) keeps `validColumnKeys` in lockstep with `panel.go`'s own,
  richer `allColumnSpecs`.
- `config_dialog.go` -- `ProcList.Config`'s dialog (`(*Plugin).configure`):
  one checkbox per `allColumnSpecs` entry plus a refresh-interval field.
  Shares `panel.go`'s build tag.
- `collector_other.go` -- the fallback stub for everything else:
  `Supported() == false`, and a `newProcListPanel`/`(*Plugin).configure` that
  only exist so this file has the same shape as the real ones' (neither is
  ever actually called; `Plugin.Init` checks `Supported()` first).
- `panel.go` -- the panel itself: table columns (`allColumnSpecs`,
  filtered by `Settings.VisibleColumns` through `columnSpecsForKeys`),
  formatting, numeric sort comparator, and the refresh ticker (re-reading
  `Settings.RefreshInterval` every tick). Entirely platform-agnostic (it only
  names the collector's shape above), so it builds and runs on every
  platform that has a real collector (`//go:build linux || windows ||
  darwin`) without change from part 1.
