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

- `IMAGES_PLAN.md` — the same kind of document for pictures; the rules in
  its section 1 (tests with every patch, English commit messages, nothing
  copied from far2l) apply here unchanged.
- `IDEAS.md` — the far2l extensions / FISH+ transport ideas that the "over
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

- `cmd/f4/audio_engine.go` — `audioEngine`: one `oto` output context per
  process, one `go-mp3` decoder at a time, a `pcmTap` between them that
  counts bytes for the clock and keeps 512 subsampled samples for the
  spectrum, and a linear resampler for tracks whose rate differs from the
  context's.
- `cmd/f4/player_panel.go` — `PlayerPanel` (`Kind() == "player"`): the
  WinAmp-shaped control block, the playlist tree, keyboard handling,
  persistence to `<config dir>/playlist.json`.
- `cmd/f4/actions.go` — the `F5`/`F6` intercept at the top of
  `actionCopyMove`, next to the temp-panel one it mirrors.
- `cmd/f4/panels_frame.go` — plain characters go to the player while it has
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
│ 02:15 / 04:31       128kbps 44kHz stereo  │  row 1
│ ▂▃▅▇▆▄▂▁▂▃▅▆▇█▆▄ ...                       │  row 2 ┐ spectrum
│ █████████████▇▆▅▄ ...                     │  row 3 ┘ (16 levels)
│ [|<] [ > ] [||] [ # ] [>|] ████████░░ 80% │  row 4
├───────────────────────────────────────────┤
│ ▾ Album                                   │
│   ♪ 01 - First track                      │  ♪ = loaded now
│     02 - Second track                     │
│ ▸ Singles                                 │
│   loose.mp3                               │
└───────────────────────────────────────────┘
```

The spectrum is a Goertzel evaluation at 16–24 log-spaced frequencies over
the tap's ring buffer, drawn with eighth-block characters. It costs a few
thousand multiply-adds per repaint at ~7 Hz and needs no FFT package; the
design choice was "cheap enough to never matter", not accuracy. Bitrate is
file size over duration (what most players show for VBR anyway); the
mono/stereo flag is read from the first frame header because `go-mp3`
always emits stereo PCM.

### 1.3 Keyboard

`Tab` keeps its f4 meaning (switch panels), which rules out using it to
hop between the controls and the playlist. Instead the panel is one column:

| Cursor on        | Key            | Action                                |
|------------------|----------------|---------------------------------------|
| control row      | Left / Right   | previous / next button; on the volume slider: −5 % / +5 % |
| control row      | Enter / Space  | press the button                      |
| control row      | Down / PgDn    | into the playlist                     |
| playlist         | Up (top row)   | back to the control row               |
| playlist         | Enter          | play track / toggle folder            |
| playlist         | Space          | pause/resume what is playing, or play the track if nothing is |
| playlist         | Left / Right   | collapse / expand; Left on a track goes to its folder |
| playlist         | Ctrl+Up/Down   | reorder among siblings                |
| playlist         | Ctrl+Right     | move into the nearest folder above    |
| playlist         | Ctrl+Left      | move out of the folder, right after it |
| playlist         | Del            | remove (stops playback if it was the current track) |
| playlist         | F7             | new folder after the cursor           |
| anywhere         | Z X C V B      | prev / play / pause / stop / next (WinAmp) |
| anywhere         | + / −          | volume                                |

`F5` in the other panel adds the selection: directories are walked
recursively and become folders (nested ones too), non-MP3 files are
skipped silently. Only local (`OSVFS`) sources are accepted for now.

Play order is depth-first over the whole tree regardless of what is
expanded; when a track ends the next one starts, and after the last one the
engine stops.

## 2. Loose ends in the first patch

Known and deliberate; each is small:

- **No KeyBar labels** for the player (F7 = new folder, Del, F5 in the other
  panel). `GetKeyLabels` has the quick-view precedent.
- **Not in the command palette / panel menu list** yet
  (`command_palette_panels.go` switch on panel type).
- **No mouse**: buttons and playlist rows should be clickable, the volume
  bar draggable. `ProcessMouse` returns false.
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
   protocol — the image pipeline from `IMAGES_PLAN.md` already caches
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
  extensions channel that `IDEAS.md` already proposes for drag and drop and
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
