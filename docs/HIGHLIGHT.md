# Editor syntax highlighting on large files

Design document and work queue. Rewritten from scratch on 2026-08-13; it
replaces the earlier version, whose numbering had drifted across several
attempts. Everything below describes the code as it stands now.

Origin: issue #458, "Тормоза в редакторе". Reference file for every
measurement in this document: `objdump -d install/far2l > far2l.s`, about 40 MB
and 600 000 lines, opened with F4.

---

## 1. How the editor draws text

Four things cooperate, and all four run on the UI thread.

**Piece table** (`internal/piecetable/piecetable.go`) holds the text. For an unedited
file it is one piece over the loading buffer, so `GetRange` is cheap. It can
return `piecetable.ErrLoading` when the data has not arrived yet.

**Line index** (`internal/piecetable/lineindex.go`) maps line numbers to byte offsets.
It is built in the background by `EditorView.StartIndexing`
(`editor_view.go`), which scans the file in 64 KB chunks off-thread and
publishes batches of offsets through `vtui.FrameManager.PostTask`. Until it
finishes, `li.LineCount()` keeps growing. `ev.indexing` says whether it is
still running. The scroll bar, `Ctrl+End` and any restore of a saved position
all wait for it.

**Wrap engine** (`internal/textlayout/wrap.go`) turns logical lines into visual rows.
Word wrap is off by default, in which case its row bookkeeping is a trivial
`rowOffsets[i] = i` loop. It is not involved in any problem described here.

**Render loop** (`EditorView.DisplayObject`, `editor_view.go`) walks the
logical lines from the top of the viewport, asks the highlighter for the
attributes of each one, and paints. Everything it calls is synchronous: time
spent there is a frame the user waits for.

### Two kinds of highlighter

They share the `vtui.Highlighter` interface

    Highlight(line string, prevState any, baseAttr uint64) ([]uint64, any)

but they are not the same kind of object, and the whole design turns on the
difference.

**Chroma-style — state is a value.** `Highlight` returns a state that fully
describes where the lexer stands. Feed it back for the next line and the
result is correct; keep it in a slice and any line can be resumed later. The
editor stores these in `ev.lineStates`, dense from line 0, where
`lineStates[i]` is the state *after* line `i`.

**Colorer — state is a position.** `ColorerHighlighter` (`colorer_plugin.go`)
drives a wasm session from `github.com/unxed/colorer4go`. The value it returns
is a line number, nothing more. The real state is a parse cache inside the
session, and the C++ wrapper (`colorer_wrapper.cpp`) only ever appends:
`colorer_parse_line` pushes the line into `line_source.lines` and parses with
`TPM_CACHE_UPDATE` at `lno = lines.size()`. Consequences, all load-bearing:

- the session can only be fed **forward**, one line at a time, in order;
- going backwards is impossible; the only way back is
  `colorer_reset_session`, which clears the line vector and the parse cache
  (and keeps the selected file type — verified against v_2.8.0 configs with
  colorer4go v0.1.9);
- everything ever fed to the session stays in wasm memory until that reset.
  A full pass over the reference file puts all 40 MB into the wasm heap as
  UTF-16, plus the parse cache;
- there is no snapshot. The state cannot be saved, copied or restored.

---

## 2. What was wrong, and where it stands

`Ctrl+End` on the reference file froze the editor for 30 to 40 seconds. The
render loop built `ev.lineStates` from line 0 up to the first visible line,
inside the draw path, because a chain has no other way to reach line 600 000.

Fixed, in order:

1. **Never highlight from the draw path.** A gap larger than
   `syncHighlightGapLimit` (50 lines) is not caught up synchronously any more.
   Chroma-style highlighters get one stateless call so the jump lands on
   coloured text immediately; the chain catches up behind it.
2. **A throttled walker.** `startHighlighting` runs slices of at most
   `hlSliceBudget` (4 ms) of UI time each, spaced by a duty cycle
   (`highlightDuty`, `highlightIdleGap`), yielding to the line indexer while it
   is still building. Slices are scheduled through `PostTask` and their
   decisions are made inside the slice, on the thread that owns the fields.
3. **Colorer out of the walker** (`usesStateChain`). See section 3.
4. **Colorer drawn from an anchor** (`HighlightLine`). See section 3.

Reported after (4): the tail of the file appears and colours immediately, with
both highlighters. One problem left from the user's point of view, and it is
item 1 of the queue: **after opening the file the editor shows an empty window
for several seconds** before anything appears.

That one is not about highlighting at all. `DisplayObject` paints a blank
rectangle and returns while `ev.targetLine != -1`, which is the state of an
editor whose saved cursor position has not been reached by the line indexer
yet. On a 40 MB file the indexer needs seconds to get there, and the reasoning
behind the blank — that painting the top of the file and then jumping looks
like a flicker — trades a flicker for several seconds of nothing.

---

## 3. Design decisions

### 3.1 Colorer keeps no state chain

`usesStateChain(h)` in `editor_view.go` returns false for
`*ColorerHighlighter`, and both `startHighlighting` and `highlightSlice` refuse
to run for it.

*Why.* Walking the file for Colorer builds nothing — the chain would hold
consecutive integers. It is also actively harmful: the walk feeds every line
to the session, so the wasm heap fills with the whole file, and it leaves the
parse position ahead of the viewport, so each frame has to rewind it. Before
this change, the render path and the walker took turns dragging the session in
opposite directions; the discarded work is what made `Esc` arrive fifteen
seconds after a held `PgDn`.

### 3.2 Colorer is addressed by line number, from an anchor

`ColorerHighlighter.HighlightLine(idx, line, baseAttr)` is what the render loop
calls. A cache hit returns the stored attributes; a miss queues work and
returns nil — the line stays plain until the result lands. Internally
(`colorer_async.go`):

- `colorerContextPlan(parsedIdx, idx)` — pure, and the whole decision. If the
  wanted line is ahead of the session and no further than `hlColorerForward`
  (2000) lines, feed the session forward. Otherwise reset and restart
  `hlColorerContext` (300) lines above the target.
- `queueLine` runs on the UI thread but never calls into Colorer. It snapshots
  the context lines and the whole uncoloured run starting at `idx` — bounded
  by `hlColorerBatchLines` and `hlColorerBatchBytes`, stopping at the first
  line already coloured or not yet loaded — into one immutable `colorerJob`.
  One job per screen, not per line: the per-line version coloured the viewport
  visibly line by line, one worker round trip and one full redraw each.
- A single worker goroutine owns the session (`runWorker`). It replays the
  context, parses the batch, and posts all the attributes back in one
  `PostTask`, which stores them and triggers one redraw. `workGeneration`
  invalidates results that were overtaken by an edit or a cancel.
- A parse error on the first batch line — the one the viewport asked for —
  disables highlighting for the file. An error on a *prefetched* line only
  cuts the batch short: the finished attributes are posted (`partial`), and
  both sides force a re-anchor, because the session's position after a failed
  `ParseLine` is unknown.

*Why an anchor.* A jump then costs the same at line 500 and at line 500 000,
which is the only way a 600 000-line file can behave. Sequential scrolling
downwards still feeds the same session forward and stays exactly correct.

*What it costs.* A construct opened more than `hlColorerContext` lines above
the viewport is invisible to the parser after a jump, so the first screen after
`Ctrl+End` can be coloured as if a block comment had never started. Scrolling
down to it from above gives the exact answer. This is accepted deliberately —
it is the same trade already accepted for the stateless Chroma call on a jump.

### 3.3 One source of line text

`EditorView.lineTextForHighlight(idx) (string, bool)` returns a line as the
highlighters see it: **line terminator included** (the parse state of the next
line depends on it) and cut at 64 KB so no parser is handed a megabyte of
binary. It feeds both the render loop and, through `ch.lineAt`, the context
lines of a re-anchor, so those are byte for byte the same text. `ok == false`
means the text is not available yet; leave the line plain and come back next
frame.

### 3.4 Invalidation

Colours are cached by line number now, so the highlighter cannot notice an edit
on its own. Two hooks:

- `invalidateStates(fromLine)` — every edit path already calls it. Truncates
  the chain and calls `ch.DropFrom(fromLine)`.
- `clearCaches()` — undo, redo, reload. Calls `ch.DropFrom(0)`.

`DropFrom(idx)` drops cached attributes from `idx` on, and if the session has
already parsed past `idx` it throws the session away: its cache cannot be
unwound.

### 3.5 A fallback engine is handed to the editor

When the Colorer session cannot be created, or no schema matches the file,
`useFallback` moves `ch.fallback` into `ev.highlighter` instead of proxying it.
Otherwise a perfectly ordinary Chroma highlighter would be treated as Colorer
by `usesStateChain` and lose both its chain and its walker.

### 3.6 Everything runs on the UI thread — except Colorer parsing

The walker's slices, the render loop and every Chroma-style highlighter call
happen there, through `PostTask`. Highlighters are not thread-safe and the
wrap engine mutates its caches as a side effect of what look like reads.
Responsiveness comes from bounding each slice in time.

Colorer's `ParseLine` can execute arbitrary grammar code and is the one thing
that moved off-thread: a single worker goroutine owns the session, the UI only
queues immutable line snapshots and consumes posted results
(`colorer_async.go`). One worker, not a pool — the session is stateful and
line order is its state, so concurrent calls are wrong by construction, and
one owner gives cancellation a single well-defined home.

### 3.7 What Colorer reports goes to debug.log

Issue #306. Colorer runs compiled without C++ exceptions, so an exception it
throws — a catalog that does not exist, an HRC file that is not well-formed, a
colour style it does not know — ends the call in a trap. colorer4go turns that
into a `*colorer.FatalError` naming the throw site, and the session refuses
every later call with the same error (`Session.Err`). Everything Colorer
reports on the way there, and everything it survives — a regexp in a scheme
that does not compile, a region nobody defines — arrives through its `Logger`
and is written to debug.log as `COLORER: [level] file:line function(): message`.

- Every session is created with `colorerSessionOptions()`. The level comes
  from `COLORER_VERBOSE`, the variable far2l's FarColorer reads, with the same
  values (`off`, `error`, `warning`/`warn`, `info`, `debug`, `trace`). Unset
  means `warning`, not far2l's `off`: the stock catalog says nothing at that
  level, and a broken one says what is broken.
- A session whose call failed is closed, never pooled
  (`releaseColorerSession`): the next user would get the old failure,
  attributed to whatever it was trying to do.
- A colour style that fails in `newColorerHighlighter` hands the editor to the
  fallback engine, the way a failed session start does (3.5), and says so.

A broken `rare/json.hrc` then reads, instead of a bare `wasm error:
unreachable`:

    COLORER: [error] colorer/xml/libxml2/LibXmlReader.cpp:299 xml_error_func(): /base/hrc/rare/json.hrc:81: parser error : Couldn't find end of Start Tag regexp line 81
    COLORER: SelectType("x.json", len=1) -> selected=false, err=colorer: colorer_select_type failed: C++ exception thrown at colorer/parsers/HrcLibraryImpl.cpp:230 in parseHRC() (wasm error: unreachable)

### 3.8 User schemes and colour styles

Issue #277. `EditorColorerUserHrc` and `EditorColorerUserHrd` (settings.ini
`[Editor] ColorerUserHrc`, `ColorerUserHrd`) are FarColorer's UserHrcPath and
UserHrdPath: a file or a folder each, handed to Colorer after the catalog
through `colorer.WithUserHRC` / `WithUserHRD`, styles first.

- A session is built from a `ColorerSource` — configuration directory plus
  both user paths — and the pool, the colour style list and the editor
  background cache compare the whole source. A session loaded without a user
  path is never handed out after the path is set.
- The module sees each user path through a read-only mount of its folder. A
  `<location link>` in an `<hrd-sets>` file resolves against catalog.xml, not
  against the file that contains it — traced to `fillMapper` in colorer4go's
  vendored Colorer-library, which always resolves an `HrdNode`'s locations
  against `base_catalog_path` — so a style elsewhere on disk could not link
  to a sibling `.hrd` file by a plain relative path (reported by montoner0).
  `materializeUserHRDPath` (`colorer.go`) works around it without touching
  colorer4go: it copies every such link's target into
  `configsDir/base/.f4-user-hrd-cache`, the one place Colorer does resolve
  links against, and hands Colorer a rewritten copy of the file pointing
  there. A link that is empty, absolute, a URL, or uses an XML entity such as
  `&hrd;` that only catalog.xml's own DOCTYPE defines is left exactly as
  written, since it already means "resolve me against the catalog". A folder
  of `.hrd` files (each root `<hrd>` naming class, name and description) has
  no `<location>` indirection to fix.
- File names Colorer opens must be ASCII: its legacy strings read a name as
  CP1251. colorer4go refuses such a path with a warning instead of letting the
  call abort. A path that does not exist is a warning too; a file that does
  not parse fails the session, and debug.log names the host path.
- The Settings Center lists colour styles through Colorer
  (`editor.ListColorerSchemesFor`), off the UI thread. It used to read
  catalog.xml with `encoding/xml`, which stops at the external entities
  (`&catalog-rgb;`) the installed catalog lists its styles through, so the
  list was empty on a real installation.

### 3.9 Checking a configuration before it is used

Issue #277, step 2. A scheme or style Colorer cannot load used to show up only
as an editor that quietly fell back to Chroma, with the reason in debug.log.
`editor.CheckColorerSource` loads a configuration the way an editor starts
Colorer — catalog, user styles and schemes, colour style — and, with
`allTypes`, the scheme of every file type (`Session.LoadFileType`), which is
where a broken scheme otherwise waits until a file of its type is opened. It
returns the failure and everything Colorer reported at warning level or worse.

- Colorer settings dialog: OK loads a changed configuration first and stays
  open if it fails, as FarColorer's OK does; Reload does the same before
  dropping sessions; "Check all schemes" loads every type behind a progress
  dialog and applies nothing. Reports that did not stop the load are shown and
  do not block.
- Settings Center: "Reload schemas" runs the quick check, and the new "Check
  all schemes" the full one; either returns the findings as its error.
- FarColorer's "Reload all" (`TestLoadBase`) calls `getBaseScheme()` on each
  type, which in this Colorer version returns the pointer without loading;
  the full check calls `HrcLibrary::loadFileType` instead.
- Loading every type of the bundled catalog took 89 s on a single-core
  sandbox, and reports two errors: `markdown:markdown` inherits
  `markdown2:markdown2`, which no type defines.

### 3.10 Pairs

Issue #277, step 3. `EditorColorerPairs` (settings.ini `[Editor] ColorerPairs`,
on by default as FarColorer's PairsDraw) draws the paired token under the
cursor and its match.

- Colorer makes pair regions special (`def:PairStart` and `def:PairEnd` are
  children of `def:Special`), so `ParseLine` never returned them.
  colorer4go's `ParseLinePairs` does; the worker stores a line's pairs beside
  its colours (`pairCache`, evicted with `attrCache`).
- `matchColorerPair` is `BaseEditor::getPairMatch` plus `searchPair` over those
  pairs: the last token whose `[Start, End]` holds the cursor (End included),
  then a walk counting starts and ends until the balance is zero.
- Drawing searches only the visible lines, as `searchLocalPair` does, and
  only lines already parsed: a line without cached colours stops the search,
  since unlike Colorer's regions the cache may not have reached the match yet.
  The token under the cursor is painted even without a match. The overlay is
  painted on a copy of the cached colours.
- Match pair, select pair contents and select pair block (actions
  `Editor.ColorerMatchPair`, `Editor.ColorerSelectPair`,
  `Editor.ColorerSelectBlock`; no default keys, as in FarColorer) search the
  whole file, as `searchGlobalPair` does. The search (`colorerPairSearch`) is
  `searchPair` made resumable: it lives on the UI thread, walks the line cache,
  and where a line is missing queues it to the worker and resumes when the
  result lands (`continuePairSearch` in `postColorerResult`). Walking up, the
  job starts a batch below the missing line so one job covers a batch of lines
  above. Lines parsed for the search go through the same anchoring as display,
  so a match agrees with the pair drawn under the cursor. An edit, a moved
  cursor or Esc drops the search; Esc does not stop Colorer while a search owns
  the job.
- Positions are FarColorer's: match pair puts the cursor on the first
  character of a match above and the last character of one below; the
  selections run from the upper position to the lower, where the cursor ends.
  A match off screen is centred, as in FarColorer.

### 3.11 Outline: functions, errors, locate function

Issue #277, step 4. FarColorer's list of functions and list of errors come
from Colorer's `Outliner` over the whole file.

- colorer4go's `Session.LineOutline` gives each parsed line's items (regions
  under `def:Outlined` or `def:Error`); the worker cuts their labels from the
  line and stores them in `outlineCache`, beside and evicted with
  `attrCache`.
- `colorerOutlineBuild` collects the whole file the way the pair search
  walks it: on the UI thread, queueing each line the cache has not reached,
  resuming in `postColorerResult`. An edit or Esc drops it.
- `Editor.ColorerListFunctions` and `Editor.ColorerListErrors` open the list
  (a `VMenu` filtered as you type), rows written as FarEditor::showOutliner
  writes them: line number, two spaces per level from `Outliner::manageTree`,
  the region class letter and the label; with `EditorColorerOldOutline`
  (default on, as FarColorer's OldOutlineView) the line's text instead. The
  item at or above the cursor is selected; choosing one centres it.
- `Editor.ColorerLocateFunction` takes the word under the cursor and goes to
  the last function whose label holds it, ignoring case, preferring one off
  the cursor's line. FarColorer's word loop drops the first character of a
  word at the start of a line and the last at the end; f4 takes the whole
  word.
- The list is `colorerOutlineFrame`, FarEditor::showOutliner's keys on a
  `VMenu` with its own filter: letters, digits, space and `; - : _ ~` narrow
  it to labels holding the filter (a filter nothing matches loses its last
  character); Backspace takes one back; Tab takes the completion shown after
  `?` in the title, which extends the filter with what follows it in the
  first row while every row still holds it; Ctrl+Up/Down go to the previous
  or next item with the list open, and Esc then restores the editor;
  Ctrl+Left/Right show a tree level less or more; Ctrl+Enter inserts the
  label at the cursor.
- With Colorer in charge, the editor's F11 menu has a Colorer submenu with
  these commands in FarColorer's order, then, as in FarColorer:
  - "Update highlighting" (`Editor.ColorerUpdateHighlighting`,
    FarEditor::updateHighlighting): the colours computed are dropped and
    computed again;
  - "Reload Colorer base" (`Editor.ColorerReloadBase`, FarEditorSet::
    ReloadBase): the configuration in use is loaded and checked, errors
    shown, as the settings dialog's Reload does; when it loads, the pool is
    dropped and every open editor highlighted by Colorer, or handed to
    Chroma because Colorer could not start, starts Colorer afresh the next
    time it is drawn (`ReloadColorerEditors`). A type picked from the list
    is forgotten, as FarColorer's reload drops its editors. The settings
    dialog's Reload and a download of schemas now do the same to open
    editors;
  - "Configure": the Colorer settings dialog.
  FarColorer's menu while it is off holds only "Configure"; f4 shows no
  Colorer submenu then, the settings being in the Options menu.

### 3.12 File types and select region

Issue #277, step 5.

- `Editor.ColorerChooseType` is FarEditorSet::chooseType: auto detection,
  the favourites, then every type under its group, the group names painted
  on the separators and the total under the list. Enter picks the type for
  this editor (`ColorerHighlighter.fileTypeOverride`, carried by each job to
  the worker, which gives its session `SetFileType` when the type changes);
  auto detection goes back to choosing by file name. Ins and Del add a type to
  the favourites and take it out; F4 assigns it a one-character hotkey, shown
  as the row's menu hotkey.
- Favourites and hotkeys are file type parameters. Their defaults come from
  far2l's `plug/hrcsettings.xml` in the configuration directory, loaded with
  `colorer.WithHRCSettings` when it exists; the user's values live in
  FarColorer's format in `colorer/HrcSettings.ini` in the profile and are set
  on every session acquired (`applyColorerProfile`). An installation without
  `plug/hrcsettings.xml` has no such parameters, and setting one is logged
  and skipped.
- `Editor.ColorerSelectRegion` selects FarEditor's `cursorRegion`: the last
  region of the cursor line holding the cursor, its end included, a region
  running to the end of the line ending there. The worker keeps each line's
  regions in `regionCache`, beside and evicted with `attrCache`.

### 3.13 File type settings

Issue #277, step 6.

- "File type settings" in the Colorer settings dialog is FarColorer's HRC
  settings dialog (`actionColorerTypeSettings`): a file type, one of its
  parameters — the default type's, then the type's own — and its value.
  show-cross, cross-zorder, fullback and the true/false parameters are
  picked from a list; maxlinelength, backparse, default-fore, default-back,
  firstlines, firstlinebytes and hotkey are typed. `<default-...>`, last in
  the list, takes the user's value back. The value is recorded whenever the
  dialog moves on from a parameter; OK writes the changes to
  `colorer/HrcSettings.ini` and drops pooled sessions.
- The dialog reads every type and parameter up front
  (`editor.LoadColorerTypeParams`) and holds no session while open.
- `EditorColorerHrcSettings` (settings.ini `[Editor]
  ColorerHrcSettings`) is FarColorer's UserHrcSettingsPath: an
  hrc-settings file loaded after the user's schemes.
- The Colorer settings dialog now puts each path's label beside its field,
  to make room for this one and the button.
- What f4 acts on, as FarEditor::reloadTypeSettings reads it ("default"
  first, the file's type on top; `readColorerTypeSettings`):
  - show-cross, through the new cross mode "By file type"
    (`ColorerCrossScheme`, FarColorer's "if included in the scheme"): the
    crosshair's axes are the file type's;
  - maxlinelength: Colorer parses at most that many characters of a line,
    as FarEditor::getLine cuts it; the rest takes the base colour;
  - fullback=no: a region running to the end of the line keeps its colour
    on the text, not on the rest of the row;
  - default-fore and default-back: the base colour of the file's text,
    which regions without colours of their own take.
  The worker reads them when it gives its session a type and hands changes
  to the UI, which recomputes the colours. An editor already open keeps the
  values it read until its type changes or it is reopened; FarColorer applies
  a changed profile to open editors.
- Not applied: backparse limits how far FarColorer's parser runs on from the
  top of the file; f4 anchors near the viewport instead (3.2), with its own
  limits. cross-zorder decides whether a region's own background shows
  through the cross or the cross covers it; f4's cross replaces the background
  of every cell it crosses and keeps the text colour, and the cached colours
  do not record which backgrounds a region set, so the distinction cannot be
  drawn from them.

### 3.14 Highlighting in viewers

Issue #277, step 7. `ViewerHighlighting` (settings.ini `[Viewer]
Highlighting`) is FarColorer's ViewerColoring for f4's viewers: off, the
quick view panel, or every viewer. It is off by default, unlike FarColorer: a viewer is for
looking at a file at once, and highlighting takes time. It is in the viewer
settings dialog and the Settings Center, beside the editor's highlighter.

- The engine is the editor's, by the editor's rules
  (`editor.NewTextColorizer`): none for None; Colorer when it is chosen and
  its schemas are installed, handing over to Chroma when Colorer cannot start
  or knows no type for the file; Chroma otherwise. Colorer in charge with its
  syntax colours off colours nothing.
- The quick view panel is in `panel` and the highlighters in `editor`, which
  imports `panel`'s neighbour `viewer`; `viewer.NewTextColorizer` is the seam,
  set by the application.
- A quick view shows the head of a file, so the colorizer highlights it from
  its first line, which is all the context there is. It runs on one goroutine
  and hands colours to the UI thread in batches of 200 lines; the panel draws
  a line coloured once its colours are there and plain until then.
- It is restarted when the text changes (another file, another code page),
  stopped in hex mode, for binary, image and provider previews, and when the
  panel closes. Colorer's file type parameters apply as in the editor.
- "All viewers" highlights the viewer as well
  (`viewer.NewWindowColorizer`, `editor.NewWindowColorizer`). The viewer
  shows an arbitrary part of the file, so it hands over the logical lines on
  screen, each with the byte offset it starts at, and up to 100 lines above
  them as context, read from at most 64 KiB back, as FarColorer's FarViewer
  takes them. A goroutine highlights the window from its context on — a
  Colorer session reset for each window, or Chroma's state carried from the
  first context line — and hands the colours back; the viewer paints each
  row from its logical line's colours, by rune, over wrapped and tab-expanded
  cells. A window is requested only when the lines on screen change, and a
  newer request replaces one not started yet. As with the editor's anchor, a
  construct opened above the context is not seen.
