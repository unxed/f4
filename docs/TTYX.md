# TTY|Xi for f4 — the X server behind the terminal

A terminal is a stream of bytes, and a great deal of what a file manager wants
is not in that stream. It cannot tell `Ctrl+Shift+Up` from `Ctrl+Up` unless the
terminal implements a modern keyboard protocol. It cannot read the system
clipboard unless the terminal implements OSC 52 and is willing. It cannot show
a picture unless the terminal implements sixel or the kitty protocol. And it
cannot know where on the screen it is at all.

far2l answers this with **TTY|Xi**: a broker process holds a connection to the
X server and hands the terminal session back the things the TTY cannot carry.
`internal/ttyx` is the beginning of the same idea for f4.

Issues: [#662](https://github.com/unxed/f4/issues/662) (keys and clipboard),
[#663](https://github.com/unxed/f4/issues/663) (pictures over the terminal).

## 1. What exists

`internal/ttyx` — no cgo, and there will not be any. Everything goes through
XGB, which speaks the X protocol over a socket in plain Go and which vtui's own
X11 backend already depends on.

- `Session` — the connection, the terminal window, its geometry, its focus, and
  the event loop that keeps all three up to date.
- `Overlay` — an override-redirect window over the terminal that shows pixels,
  which follows the terminal when it moves and comes down when it loses the
  focus.
- `GrabKeys` — the key combinations a TTY cannot carry, taken from the X server
  and delivered as ordinary `vtinput` events.
- `overlay_x11.go` in `internal/media` — the image viewer's use of the overlay.
- `ttyx.go` in `internal/keymap` — the configured combinations, forwarded into the
  stream the frame manager dispatches from.

**The clipboard is not here, deliberately.** Issue #599 decided that all
clipboard work belongs in [goclip](https://github.com/unxed/goclip), and a
second implementation in f4 would be exactly the fragmentation that issue was
written to stop. What was found instead: goclip's native X11 driver did not
work at all — see section 6.

**#662 is done in both halves**, the clipboard through goclip and the keyboard
through here. What is left is judgement rather than code: which combinations
are worth taking from the desktop. See section 7.

## 2. Finding the terminal window

Three ways, best first. Which one succeeded is reported by `Session.Source()`,
and `Source.Trusted()` separates the two that identify a window from the one
that guesses.

1. **`$WINDOWID`** — xterm, urxvt, konsole and others publish their own window
   id for their children. Verified against the server before it is believed,
   because the variable outlives the window it names when a shell is passed
   along.
2. **`_NET_WM_PID`** — every window on `_NET_CLIENT_LIST` is asked which
   process owns it, and the answer is matched against our own ancestors, read
   out of `/proc`. This covers the terminals that publish nothing, including
   the ones where a single server process owns a window per tab: that process
   is an ancestor of the shell and therefore of us. When several tabs match,
   the focused one wins, because from outside the tabs are indistinguishable.
3. **`_NET_ACTIVE_WINDOW`** — whatever had the focus when we looked. Right most
   of the time and wrong exactly when the user was doing something else at the
   moment f4 started, which is why it is not trusted.

## 3. Showing a picture over the terminal

`ImageView` used to answer a terminal with no image protocol with *"This
backend cannot display images."* Under a local X session it now puts the
picture in an X window over the terminal instead.

The window is **override-redirect**, so the window manager neither decorates
it, nor gives it focus, nor lets the user drag it away from the terminal it
belongs to. Its **input region is emptied** through the SHAPE extension, so the
mouse passes through it and selecting text keeps working underneath a picture.
**Backing store** is asked for, because nothing here runs an event loop and
without it the server would expect a repaint on `Expose`.

**The window is not the grid.** The top of a terminal window may be a menu bar
and the right of it a scroll bar, and dividing the window by the number of
cells in it therefore gives a cell slightly too large and an origin slightly
too high — which put a picture a row and a bit above its own space in
gnome-terminal. The terminal knows better, and `ProbeHostTextArea` asks it
three ways, cheapest first:

1. **`TIOCGWINSZ`.** The window size ioctl carries the pixel size of the text
   area beside the size in characters, and nearly every terminal fills it in.
   One system call, nothing to lose on the wire, and no chance of swallowing a
   keystroke.
2. **`CSI 16 t`**, the size of one cell, which multiplied by the grid is the
   text area exactly and owes nothing to padding.
3. **`CSI 14 t`**, the text area directly.

**Those answers are not in the same units as the window.** GTK answers in
logical pixels and the X server reports the window in device pixels, so on a
display at double scale the terminal says its text area is 640x408 while its
window is 1312x868 — half of it, which put the picture at half size in the
bottom left corner. Nothing in either answer says which units it is in, so the
factor is worked out: `hostScale` takes the largest whole number of text areas
that still fits inside the window. It cannot be fooled by a terminal with a lot
of furniture, because choosing two would need the menu bar and the scroll bar
together to be as large as the text between them.

The grid is then placed against the **bottom left** of the window at that size,
because a menu bar is at the top and a scroll bar is on the right and neither
is ever at the bottom left.

Two things narrow the last couple of pixels. Where the terminal draws its grid
into a window of its own, the X server knows exactly where that window is and
nothing has to be worked out from the frame at all; `Session.InnerWindow`
looks for it and believes a candidate only when it is already about the size
that was worked out, because getting this wrong means drawing over some other
part of the screen. Many terminals have no such window — a GTK application
usually draws every widget into the one window of its toplevel — and there the
error that remains is the padding the terminal keeps between its widget and
its grid, which **is reported nowhere**: `CSI 14 t` answers with the widget,
the grid is somewhere inside it, and the difference is a couple of pixels of
theme. `[Images] X11OverlayOffsetX` and `X11OverlayOffsetY` are the way to
supply a number that nothing else knows. They default to zero.

The two questions asked over the wire have to be asked before the input reader
starts: afterwards the answer is just another escape sequence arriving on
standard input and the reader eats it. **And they have to be waited for by
hand.** A descriptor passed from another process — which is exactly what a
session attached to a running server reads from — arrives in blocking mode,
the runtime does not poll it, and `SetReadDeadline` refuses. Giving up there
meant the question was never even asked and both answers came back in the same
millisecond, which is what the debug log showed and how this was found.

The measured cell is handed to `GraphicsLayer.SetCellSize`. Without that the
layer answers zero, everything that lays a picture out falls back to a guessed
eight by sixteen, and the picture comes out the wrong shape as well as in the
wrong place: the cell of a terminal is nothing like square.

The overlay is installed as **vtui's external graphics renderer**, not called
by whoever happens to be drawing a picture. `GraphicsExternal` is a protocol
like kitty or sixel as far as the layer is concerned, so a terminal with no
image protocol of its own reports itself as supporting graphics after all, and
the file viewer, the thumbnail grid, quick view and the pictures a program
prints into the built-in terminal arrive here together, once a frame, without
any of them knowing. A terminal that does have a protocol keeps it: this is the
last resort and not a preference. Issue #273 is what the built-in terminal half
of that closes.

A whole frame goes into **one window with the gaps cut out of it**. The window
covers the rectangle that holds every picture and a SHAPE bounding mask is cut
to the individual pictures, so the text between them — the captions under a
grid of thumbnails — shows through. A window per picture would do the same and
cost a dozen of everything. A server with no SHAPE extension cannot do it and
gets one opaque rectangle, which is a worse picture rather than a broken one.

**The overlay is a child of the terminal's own window**, not a window of its
own over the top of everything. That one argument to `CreateWindow` settles
most of what an overlay has to get right, and settles it in the X server:

- it moves with the terminal, because the server moves children;
- it is clipped to the terminal, so a picture cannot spill past the edge of the
  window it belongs to;
- anything the window manager raises above the terminal — the alt-tab switcher,
  a notification — is above it too. An override-redirect window outranked the
  whole desktop and covered the switcher in Cinnamon;
- it is not a top-level window, so no task list and no alt-tab ever offers it;
- it dies with the connection, so leaving f4 leaves nothing behind.

It also stops feeling like a separate window and starts feeling like part of
the terminal, which is the point. **Nothing takes it down when the terminal
loses the focus** any more, and nothing should: a picture drawn in a window
does not disappear because the window is not on top.

Two rules keep this from being a menace, and neither is optional:

- **The window has to have been identified, not guessed.** An
  override-redirect window is drawn over whatever is underneath it. Guessing
  wrong means painting over a stranger's application, so `SourceActive` stands
  down.
- **It comes down when the terminal loses the focus.** Nothing in X will take
  it down for us. The event loop does it; see section 4.

There is **one overlay per process**, not one per viewer: the connection
carries the event loop that keeps the focus and the geometry warm, two viewers
would fight over which window is on top, and the answer to "can pictures be
shown at all" has to exist before any viewer does. That last one is not
theoretical — `tryOpenImageViewer` refuses to open the viewer when nothing can
show a picture, and until it learned to ask about the overlay, F3 on a PNG in
gnome-terminal opened the hex viewer.

Switched off with `[Images] Overlay=0` — `X11Overlay` is the old name for the
same setting and still works. On by default, because it only ever
runs where the alternative is an apology.

## 4. The event loop

`Open` selects `FocusChange` and `StructureNotify` on the terminal window and
starts one goroutine that reads them. It is the only reader of the connection,
and it never holds the lock while it waits, so a request from another goroutine
is never stuck behind an event that has not arrived.

What it buys:

- `Focused()` and `Geometry()` are reads of cached values rather than round
  trips, so they can be asked on every frame.
- The overlay comes down the moment the terminal loses the focus and goes back
  up when it returns, without the caller having to notice. Nothing else in X
  will move an override-redirect window out of the way.
- The overlay moves with the terminal window while it is dragged.
- The key grabs are taken on focus and released on focus loss, which is not a
  nicety: a grab held by a window nobody is typing into takes that combination
  away from every other application on the desktop.
- `Changed()` carries a token whenever any of that changes, for an application
  that draws on demand and needs to know when to draw.

**A grab is reported as a change of focus.** The X server sends `FocusOut` and
`FocusIn` with mode `NotifyGrab`/`NotifyUngrab` whenever a grab begins or ends,
because from the keyboard's point of view the focus really has moved — to the
grabbing client and back. Taking that at face value is a trap with teeth here
of all places: this package grabs keys, so every grabbed key produced a
`FocusOut`, the grabs were handed straight back, and the next press of that
combination went to the terminal as an unqualified one. It looked exactly like
a key that had never been grabbed. `focusEventIsReal` drops those, and a
`FocusOut` whose detail is `NotifyInferior`, which only means the focus moved
to a child of our own window.

Three things learned the hard way here, each of which cost a hang or a crash:

- **A method that takes the lock must not be called from one that holds it.**
  `watch` asked `focusedNow` for the focus while holding the mutex, and the
  package deadlocked on the first connection it ever made.
- **The event goroutine must hold its own copy of the connection.** `Close`
  sets the field to nil, and a goroutine blocked in `WaitForEvent` would then
  read that nil rather than the connection it was waiting on. Closing the
  connection is what ends the wait, whatever the field says.

There is also a trap in XGB itself, worth knowing before adding another
extension: `shape.Init` and its siblings write two package level maps that
xgb's own reader goroutine reads for every event, without a lock. Initialising
an extension while any connection is live is therefore a data race inside the
library, and a concurrent map access in Go is a crash rather than a warning.
`registerShape` in `overlay.go` sidesteps it by registering only the
per-connection opcode, which is the only thing `shape.Rectangles` needs and
which lives in a map that does have a lock.

## 5. Known limits

- **A terminal that answers no `CSI 14 t` puts the picture out by its
  furniture.** There is nothing else to measure with, and the fallback assumes
  there is no furniture.
- **A picture is only as good as the guess about where the grid is.** See
  section 3 for what is measured and what is not.
- **The identification runs once, at the first picture.** A session that is
  detached and reattached elsewhere keeps pointing at the old window.

## 6. The clipboard lives in goclip, and did not work

Issue #599 says the clipboard is goclip's job and that anything missing there
gets fixed there. Measuring it first turned out to be the whole story: goclip's
pure-Go X11 driver created its window as `InputOnly` with the root depth, and
an `InputOnly` window must have depth zero and the `CopyFromParent` visual.
Every call therefore failed with `BadMatch` on `CreateWindow`, the native path
never ran once on any server, and every copy and paste fell through to `xclip`
— which #599 opens by pointing out is not installed by default anywhere.

Three more defects were behind it, none of which could show while the first one
stood:

- **No `INCR`.** An owner answering a large paste sends a handshake and then the
  value in pieces. goclip read the handshake and returned it as if it were the
  text, so a paste from a browser or an office suite produced four bytes of
  binary rather than the document.
- **`BytesAfter` ignored.** Even a value that came whole was truncated at
  whatever the server chose to put in the first reply.
- **Two readers of one event stream.** `ReadText` polled for events while the
  serving goroutine was blocked in `WaitForEvent` on the same connection, so
  either could swallow what the other was waiting for.

All four are fixed in goclip as of v0.1.1, along with outgoing `INCR`, so a
large copy works in both directions. Eight tests run it against a real server.

vtui's `clipboard_unix.go` now goes through goclip instead of shelling out to
`xclip`, `xsel` and `wl-copy` itself — those remain, as goclip's own fallback,
which is what #599 asked for. One thing there is worth not undoing: **the file
backed driver goclip falls back to last is deliberately skipped.** It always
succeeds, and a success in `setOSClipboard` is what stops `SetClipboard`
falling through to OSC 52 — the only thing that works in a terminal with no
graphical session behind it at all.

## 7. The keyboard, and why it is off by default

`Session.GrabKeys` takes a set of combinations named by keysym — so the caller
needs to know nothing about the layout, and the set survives the user
switching one — and `Session.Keys()` delivers them as `vtinput.InputEvent`
values, translated by `keytrans`, the same translator vtui's X11 backend uses.
A key arriving this way is indistinguishable from one arriving in GUI mode.

`internal/keymap/ttyx.go` reads the configuration, asks for the combinations and
forwards the events onto `vtui.FrameManager.EventChan`, which is the same
channel the terminal's own keys arrive on. Past that point nothing can tell
the two apart, and no change to vtui was needed: the channel was already
exported.

```ini
[TTYXi]
Keys=1
KeyList=Ctrl+Shift+Up, Ctrl+Enter, Ctrl+Tab
```

`Keys` is **`1` by default**. A Ctrl+Enter that only works after the user has
found a setting does not work, and far2l asks nobody either. The setting is the
way out for whoever disagrees about which combinations are worth taking.

`KeyList` defaults to `config.DefaultTTYXKeyList`: the Ctrl+Shift arrows, the
Enter and Tab chords, `Alt+Shift+F3`/`F4`, and `Ctrl+Shift+P`. The last one is
the command palette. Shift over a bare letter does not change the control byte
a TTY sends, so `Ctrl+Shift+P` reaches f4 as `Ctrl+P` — the passive-panel
command — on any terminal without an extended keyboard protocol. vtinput asks
for three of them at startup (Kitty, win32-input-mode, far2l); VTE answers
none, which is why the palette could not be opened by its own documented chord
in GNOME Terminal (issue #980). Taking the key from the X server is the only
route to the real chord there.

This is an X11 route and nothing else. Under Wayland the terminal is a Wayland
client, `ttyx.Open` does not find a window for it, and the grab never happens:
`Ctrl+Alt+P` remains the fallback there, as it does wherever there is no
graphical session at all.

### Why a grab, and why only while focused

A grab is shared state on the X server, and this uses one deliberately, for two
reasons.

**It is what stops the key arriving twice.** A grabbed key does not reach the
terminal at all, so no second and blunter copy of it comes up through the TTY:
f4 sees `Ctrl+Enter`, not `Ctrl+Enter` followed by a bare `Enter`. Selecting
key events on the terminal's window instead — which is not exclusive in X and
would work — would leave both copies arriving, and the bare `Enter` would run
the command line under the filename `Ctrl+Enter` had just inserted.

**It is held exactly when the keys are wanted**, which is while f4's terminal
has the focus. It is released when the focus leaves, and that is the only thing
the release is for: while another application is being typed into, a grab held
by f4 would take those combinations away from it. Nothing is taken from f4
itself at any point.

Three rules the wiring keeps:

- **A bare key is never grabbed.** `F5` on its own is delivered perfectly well
  by every terminal, and taking it from the desktop would be pure loss. The
  parser refuses an entry with no modifier.
- **A guessed window is never grabbed on.** Same rule as the overlay, for a
  sharper reason: a grab on the wrong window takes those keys from whoever
  really owns it.
- **One typo does not cost the rest of the list.** An entry that names nothing
  known is skipped and logged.

## 8. When it does not work

None of this can be seen from outside, and all of it can fail for reasons that
depend on the window manager, on the terminal, and on how f4 was started. Every
decision is therefore written to the debug log under one prefix:

```sh
VTUI_DEBUG=1 f4
grep TTYX ~/.config/f4/Logs/debug.log
```

What to look for, in the order it happens:

- `TTYX: no session` — no display, or nothing on it could be identified.
- `the terminal window was only guessed` — neither `$WINDOWID` nor
  `_NET_WM_PID` matched, so everything stands down on purpose. This is what a
  detached session looks like from the inside: the process that owns the
  window is not an ancestor of the process asking.
- `window N found through ...` — the identification worked, with which method
  and what the geometry is.
- `TIOCGWINSZ -> text area WxH`, or failing that `CSI 16 t -> cell ...,
  CSI 14 t -> text area ...` — how the terminal was measured. All of them
  failing means the picture is placed by treating the window as the grid,
  which is wrong by whatever furniture the window has.
- `the cell is WxH pixels (scale N)` — what was handed to the graphics layer,
  in device pixels, and the logical-to-device factor that was worked out. A
  missing line means everything is laid out on a guessed eight by sixteen; a
  scale of 1 on a HiDPI desktop means the factor was not found and the picture
  will be half the size it should be.
- `keysym 0xNN mods N taken as keycode N` or `refused` — one line per
  combination. A refusal is another client already holding it; a keycode of
  zero is a keysym that is not on this keyboard.

## 9. Testing

`internal/ttyx` tests run against a real X server, which on a machine without
one is no server at all:

```sh
Xvfb :99 -screen 0 1280x800x24 &
DISPLAY=:99 go test ./internal/ttyx/
```

Without `$DISPLAY` they skip. The overlay test writes a gradient, reads it back
with `GetImage` and compares it pixel by pixel, so a channel swap fails it
rather than producing a plausible wrong colour. The pure arithmetic — the
ancestor walk, the window id parsing, the cell to pixel conversion — is tested
without a server.
