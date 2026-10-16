// Package winex11drag is the foundation for speaking XDND directly to a
// native X11 application from inside a Wine-hosted f4 process (issue #566).
//
// Wine bridges XDND into OLE for drops coming into a Wine window, but not
// the other way: ole32!DoDragDrop only ever finds a target among Wine's own
// windows, so dragging a file out of f4 onto Nemo, Nautilus or the desktop
// shows the "no" cursor for the whole gesture (see docs/DRAGDROP.md). The
// fix, per the issue, is to bypass ole32 under Wine and be an XDND source on
// the user's real X server directly -- the way yabridge does it for VST
// plugins.
//
// This package holds the first two steps and the platform backend for step C2,
// landed ahead of the rest so each piece can be reviewed and tested on its own:
//
//   - step 1 (x11window_windows.go, x11window.go): find the Xlib Window of
//     f4's own top-level HWND, via the "__wine_x11_whole_window" property
//     winex11.drv sets on it.
//   - step 2 (display.go, xauthority.go, wineconn.go): open an X connection
//     to that server from inside Wine, since Wine's winsock cannot reach an
//     AF_UNIX socket on the Linux side. This goes through libwinescape for
//     the raw socket and file I/O, and github.com/jezek/xgb for the X11
//     protocol and its authentication handshake.
//   - step C2 (backend.go, install_windows.go, pointer_windows.go): wrap the
//     existing GUI drag backend under Wine and follow the Windows pointer.
//
// The wiring is additive: Install only wraps an existing backend when
// vfs/hostmode.Posix() is true, which -- by that package's contract -- is never
// the case on real Windows or a native (non-Wine) build. The remaining work is
// the Wine/X11 end-to-end check; see the issue for its acceptance criteria.
package winex11drag
