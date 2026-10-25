# Audio player panel — status, design and plan

**Formats (Sept 2026).** MP3, WAV, FLAC and Ogg Vorbis are decoded in Go
(`go-mp3`, own RIFF reader, `mewkiz/flac`, `jfreymuth/oggvorbis`); AMR,
AAC/M4A, Opus, WMA, APE and the rest go through `ffmpeg` writing raw PCM to
a pipe (`audio_decode.go`). With the player open, Enter on an audio file in
the file panel plays it at once and the panel's audio files become the
queue (Far AudioPlayer style); F8 on the playing file stops it first. The
text below predates this and still says MP3 in places.

The entry point for the player work. Written so that it can be continued
with nothing but the repository at hand. Related reading:

- unxed/f4#1685 — the same kind of document for pictures; the rules in
  its section 1 (tests with every patch, English commit messages, nothing
  copied from far2l) apply here unchanged.
- unxed/f4#1680 — the far2l extensions / FISH+ transport ideas that the "over
  the network" part below builds on.
- `plugins/id3editor` — the ID3 tag editor; the player reuses its
  `unxed/id3-go` dependency for playlist names.

## 1. What exists (first patch)

`Ctrl+Shift+M` (`Panel.Player`) opens the player as an alternate panel on
the passive side, exactly like `Ctrl+L` / `Ctrl+Q` do for Info and Quick
view (`toggleAltPanel`). Files are added from the opposite file panel with
`F5`; `F6` is refused with a message so a reflex cannot move music around
on disk.

Files:

- `internal/media/audio.go` — `audioEngine`: one `oto` output context per
  process, one `go-mp3` decoder at a time, a `pcmTap` between them that
  counts bytes for the clock and keeps 512 subsampled samples for the
  spectrum, and a linear resampler for tracks whose rate differs from the
  context's.
- `internal/panel/player.go` — `PlayerPanel` (`Kind() == "player"`): the
  WinAmp-shaped control block, the playlist tree, keyboard handling,
  persistence to `<config dir>/playlist.json`.
- `internal/app/actions.go` — the `F5`/`F6` intercept at the top of
  `actionCopyMove`, next to the temp-panel one it mirrors.
- `internal/panel/frame.go` — plain characters go to the player while it has
  the cursor (WinAmp's `Z X C V B`, `+`/`-`), same mechanism as `ai_chat`.

### 1.1 Libraries and platforms

`ebitengine/oto/v3` and `hajimehoshi/go-mp3` were already in the module
graph through ebiten, so the patch pins the versions the graph resolved to
and changes nothing else. Both are pure Go on the desktop: oto reaches
ALSA/PulseAudio, CoreAudio and WASAPI through `purego`, which in this
repository is `unxed/pureffi`. Linux, Windows and macOS type-check; Linux
builds and the unit tests run. Nothing has been heard yet — the development
container has no sound device, and `oto.NewContext` failing is reported in
the title row of the panel rather than crashing. **First thing to do on a
real machine: press F5 on an MP3 and listen.**

`beep` was not used: it wraps oto with its own streamer abstraction and
brings `faiface/beep`'s `speaker` global, and at this size the wrapper does
not pay for itself. If the player grows formats (§3), `gopxl/beep/v2`'s
decoders (`vorbis`, `flac`, `wav`) can be pulled in individually without
the mixer.

### 1.2 Panel layout

```
┌ Player ──────────────────────────────────┐
│ ♪ Artist - Title            (marquee)     │  row 0
│ 02:15 ━━━━━━━━━━━━●──────────────── 04:31 │  row 1: playback bar
│ MP3 128kbps 44kHz stereo                  │  row 2
│ ▂▃▅▇▆▄▂▁▂▃▅▆▇█▆▄ ...                       │  ┐ spectrum
│ █████████████▇▆▅▄ ...                     │  ┘ (16 levels)
│ [|<] [ > ] [||] [ # ] [>|] ████████░░ 80% │  buttons
├───────────────────────────────────────────┤
│ ▾ Album                                   │
│   ♪ 01 - First track                      │  ♪ = loaded now
│     02 - Second track                     │
│ ▸ Singles                                 │
│   loose.mp3                               │
└───────────────────────────────────────────┘
```

The playback bar fills between the two clocks as the track plays; a track
whose length is not known leaves it empty (#2185). The bar and the spectrum
are settings, both on by default (Settings, Panels, Audio player,
`PlayerBar`, `PlayerSpectrum`). With the bar off the clock and the stream
facts share row 1, "02:15 / 04:31   MP3 128kbps...", which with the spectrum
on is the layout the player had before the bar.

The spectrum is a Goertzel evaluation at 16–24 log-spaced frequencies over
the tap's ring buffer, drawn with eighth-block characters. It costs a few
thousand multiply-adds per repaint at ~7 Hz and needs no FFT package; the
design choice was "cheap enough to never matter", not accuracy. Bitrate is
file size over duration (what most players show for VBR anyway); the
mono/stereo flag is read from the first frame header because `go-mp3`
always emits stereo PCM.

### 1.3 Keyboard

F1 with the focus in the player opens its own help topic, `@Player` in
`internal/dialog/help/en.hlf` and `ru.hlf` (an alternative panel names its
topic with `HelpTopic`, which `PanelsFrame.GetHelp` asks); keep it in step
with the table below.

`Tab` keeps its f4 meaning (switch panels), which rules out using it to
hop between the controls and the playlist. Instead the panel is one column,
from the playback bar (the clock row with the bar off) over the buttons into
the playlist. Each row keeps its arrows whatever plays: they used to seek on
the buttons and in the playlist while a track was loaded, so the cursor could
not get from Play to Stop then, nor fold a folder (#2185).

| Cursor on        | Key            | Action                                |
|------------------|----------------|---------------------------------------|
| playback bar     | Left / Right   | seek 5 s back / forward               |
| playback bar     | Enter / Space  | play or pause                         |
| playback bar     | Down / PgDn    | to the control row                    |
| control row      | Left / Right   | previous / next button; on the volume slider: −5 % / +5 % |
| control row      | Enter / Space  | press the button                      |
| control row      | Up             | onto the playback bar                 |
| control row      | Down / PgDn    | into the playlist                     |
| playlist         | Up (top row)   | back to the control row               |
| playlist         | Enter          | play track / toggle folder            |
| playlist         | Space          | pause/resume what is playing, or play the track if nothing is |
| playlist         | Left / Right   | collapse / expand; Left on a track goes to its folder |
| playlist         | Shift+Up/Down  | reorder among siblings                |
| playlist         | Shift+Right    | move into the nearest folder above    |
| playlist         | Shift+Left     | move out of the folder, right after it |
| playlist         | Del / F8       | remove (stops playback if it was the current track) |
| playlist         | F7             | new folder after the cursor           |
| control row      | F7             | new folder at the end of the playlist |
| anywhere         | Z X C V B      | prev / play / pause / stop / next (WinAmp) |
| anywhere         | K              | play or pause (YouTube)               |
| anywhere         | J / L          | 10 s back / forward (YouTube)         |
| anywhere         | Shift+P / Shift+N | previous / next track (YouTube)    |
| anywhere         | M              | mute, and back (YouTube)              |
| anywhere         | 0 … 9          | jump to that tenth of the track (YouTube) |
| anywhere         | + / −          | volume                                |
| anywhere         | H              | show the keys on the player, or hide them |
| file panel       | Ctrl+Alt+Left/Right | seek 5 s without leaving the file panel |

The keys are hidden by default, so the player is not a page of letters: its
bottom border says "H: keys", and H puts the letters on the buttons and as
many of the other keys as fit into the border, the way B flips the units of
the info panel (`PlayerKeyHints`, remembered in settings.ini). The key bar
over a focused player names F7 (folder) and F8 (remove) and leaves the file
keys, which do nothing there, blank.

Ctrl with the arrows still moves playlist items too, where it reaches f4:
macOS keeps Ctrl+Up/Down/Left/Right for Mission Control and the switching
of Spaces, which is why Shift is the key that is documented (#2185).
Which letters are on is a setting (Settings, Panels, Audio player,
`PlayerKeys`): WinAmp's, YouTube's, or both, the default, since the two
share no key. The command palette and macros run the player's commands
(`PlayerPanel.Do`), not their keys, so Player: Stop works whatever the
scheme.

Z X C V B are matched by the key, not by the character it types, so they
work with the Russian layout on: the virtual key when the terminal reports
one, else the character put back on its Latin key
(`keymap.LayoutLetterVK`).

With the player open, the cursor of the file panel it was opened from is
followed: moving onto a recording plays it. That happens only while the
player plays the panel's files or nothing at all; a track started from the
playlist is never swapped for the file under the cursor (#2185).

`F5` in the other panel adds the selection: directories are walked
recursively and become folders (nested ones too), non-MP3 files are
skipped silently. Only local (`OSVFS`) sources are accepted for now.

Play order is depth-first over the whole tree regardless of what is
expanded; when a track ends the next one starts, and after the last one the
engine stops.

The mouse (`player_mouse.go`, #2185): a click presses a button, sets the
volume where it lands on the slider and moves the track where it lands on
the playback bar. The slider and the bar follow a drag; the bar only moves
its knob while dragged and seeks when the button is let go, as a seek loads
the track again. In the playlist a click puts the cursor on the row, a
click on ▸/▾ folds the folder, and a double click plays the track or folds
the folder. The wheel scrolls the playlist (`followed`: the view follows the
cursor only when the cursor moves) and turns the volume over the slider;
the player takes every wheel event, which the panels would otherwise turn
into Up and Down for the file panel behind it. `Show` records where it drew
what a click can hit (`playerLayout`).

### 1.4 Playing in the background

What plays is a `playerSession` (`internal/panel/player_session.go`), one
for the process: the engine, the playlist, the track loaded, the file
panel's recordings when they are what plays, and the ticker that goes on to
the next track. A `PlayerPanel` shows the session and keeps only the cursor
and the buttons; a second player, in another workspace, shows the same one.
The session ends when no player shows it and nothing is loaded.

Closing the player while something plays (`ToggleAltPanel`: Ctrl+Shift+M,
or another alternative panel opened in its slot) asks first
(`altPanelCloseAsker`): In the background, Stop, or Cancel and Esc, which
keep it open. A player closed otherwise, with its workspace say, leaves the
music playing without a question.

In the background `DrawPlayerIndicator`, which the application calls after
each frame next to the macro recording mark, puts ♪ (‖ when paused) in the
top right corner of the screen, unless a player is on the screen; a click
on it (`PlayerIndicatorClick`, from the key router) opens the player, whose
cursor starts on the track that plays. The `Music.*` actions (play or
pause, next, previous, stop; area Common, no default keys) work while a
track is loaded and no player has the focus (condition `MusicPlaying`).

## 2. Loose ends in the first patch

Known and deliberate; each is small:

- **Not in the command palette / panel menu list** yet
  (`command_palette_panels.go` switch on panel type).
- **English only**; `Player.*` keys exist only in `en.lng`.
- **Position clock** subtracts `BufferedSize()` from bytes read, which is
  the right idea but untested against real hardware latency.
- **Volume is not persisted**; add `PlayerVolume` to `AppConfig`.
- `Player.Close` in oto is marked deprecated; it still exists in the pinned
  alpha. If it goes, `Pause()` and dropping the reference is what the
  library recommends.

## 3. Plan

In rough order of value per hour:

1. **Listen on a real machine.** Verify start-up latency, the clock, and that
   `Finished()` fires (oto flips `IsPlaying()` to false once the source hits
   EOF and the buffer drains). Adjust `BufferSize` in `NewContextOptions` if
   the spectrum lags the sound noticeably.
2. **Seeking.** `Left`/`Right` with the cursor on the time row, or `,`/`.`
   ±5 s. `go-mp3` decoders implement `io.Seeker` in decoded bytes; the tap's
   counter needs resetting to the new offset and the resampler its state.
3. **Remote sources.** For non-`OSVFS` panels, either download to a temp file
   (what F4/edit already does for remote files) and play that, or stream via
   the VFS reader — `go-mp3` only needs `io.Reader` (no seek, no duration
   then). Both are reasonable; start with temp-file for correctness.
4. **Mouse, KeyBar, palette, translations** (§2).
5. **Repeat / shuffle** flags on the control row; a `[R]`/`[S]` pair after
   the volume bar.
6. **More formats.** Wave and FLAC are pure Go (`gopxl/beep/v2/wav`,
   `mewkiz/flac`), Ogg Vorbis via `jfreymuth/oggvorbis`. The engine only
   needs a `decoder` interface `{ io.Reader; SampleRate() int; Length() int64 }`.
7. **M3U import/export** so a playlist folder can be shared; `F5` of an
   `.m3u` file should expand into a folder.
8. **Cover art** in the control block when the terminal has a graphics
   protocol — the image pipeline from unxed/f4#1685 already caches
   decoded pictures; ID3 APIC frames are what `id3-go` exposes.

## 4. Over the network — thoughts for later

The player is local by design, but two of the pieces already point the
other way:

- **Remote file, local speakers** is the easy direction and is item 3 of
  the plan: the bytes come through the VFS, the decoding and the device stay
  on this side.
- **Local file, remote speakers** — playing on the machine that runs the
  terminal while f4 runs on a server — is the interesting one, and the one
  that needs a transport. The natural vehicle is the far2l terminal
  extensions channel that unxed/f4#1680 already proposes for drag and drop and
  for VTUI-apps-as-panels: add a request that opens an *audio sink* on the
  terminal side (`rate`, `channels`, `format`) and a stream of PCM (or,
  better, of the undecoded MP3 frames, which are 10× smaller and let the
  terminal decode with whatever it has). Volume and pause become messages
  on the same channel; the spectrum stays on the f4 side because it is
  computed from the same bytes before they are sent.
- The audio engine is already the right seam: `audioEngine` is the only
  thing that touches `oto`, so a `remoteAudioSink` implementing the same
  `Load/Play/Pause/Stop/SetVolume/Position` surface can be dropped in
  behind a feature check ("does this terminal advertise an audio sink?")
  without the panel knowing.
- If far2l does not grow the extension, the fallback is FISH+ carrying the
  file to the local side and item 3 above doing the rest — slower to start,
  but nothing new to write on the protocol level.
