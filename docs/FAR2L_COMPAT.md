# far2l compatibility for two-panel file managers

Version: **0.1-draft** (21 October 2026). Plan of work: [unxed/f4#2115](https://github.com/unxed/f4/issues/2115).

This document is a checklist that any two-panel (orthodox) file manager can be
measured against. It answers two questions:

* **A. Does a Far user feel at home?** The keys, the command line, fast find,
  the selection and the clipboard behave the way Far Manager and far2l taught
  the user's fingers.
* **B. Does the manager work predictably with far2l and its ecosystem, in both
  directions?** The manager running inside far2l's terminal (or any terminal
  that speaks the same protocols) gets every key and the shared clipboard; and
  far2l, or any other program, running inside the manager's own terminal gets
  the same from the manager.

The checklist is not about f4. f4 is one implementation that is measured
against it like any other.

## 0. Conventions

**Requirement words.** MUST and SHOULD are used as in RFC 2119. A MUST of a
level is required to claim that level (section 9). A SHOULD is never required,
but the conformance report states it as passed or failed, so that users can see
it. A requirement that names a feature the manager does not have (an internal
editor, a built-in terminal, plugins) is reported as **N/A** and does not count
against the level, unless the requirement says otherwise.

**Identifiers.** Every requirement has an identifier that never changes meaning
once published: `K-xx` keys, `C-xx` clipboard, `P-xx` terminal protocols in the
role of the client, `B-xx` bracketed paste fast path, `T-xx` the built-in
terminal (the manager as the host), `D-xx` drag and drop. A withdrawn
requirement keeps its number and is marked withdrawn.

**Roles.** *Client*: the manager runs inside a terminal and draws its UI there.
*Host*: the manager's built-in terminal runs another program (a shell, far2l, a
second copy of the manager). These are the roles that the far2l extensions call
*client* and *server* ([VTExts.md, 4.1][vtx-4-1]).

**Sources.** Behaviour of far2l is cited by permanent links to far2l master at
commit [`5ec2011`][far2l-commit] (10 October 2026): the built-in help
(`far2l/bootstrap/scripts/FarEng.hlf.m4`) for what the user sees, and the
source for where it is implemented. The far2l terminal extensions are cited by
the protocol description `VTExts.md` (branch `extsdocs` of unxed/far2l at
[`8030902`][vtx-commit], proposed upstream as elfmz/far2l#3082), which was
written from the far2l code and checked against it. Links point to the
behaviour only: a manager with a licence other than the GPL implements it from
the description, not by copying far2l code.

**Version of this document.** The version number changes when a requirement is
added, withdrawn or changes its level or MUST/SHOULD. A conformance claim names
the version it was checked against (section 9).

## 1. Test environments

The procedures below name the environment they need. A requirement is checked
in each environment that the column *Env* lists, or in any one of them if the
list is joined with "or".

| Env | What | Why |
| --- | ---- | --- |
| E1 | far2l with the GUI backend, the manager started in far2l's built-in terminal (`Ctrl+O`, then run the manager). The manager is the client of the **far2l extensions** (far2l calls this backend TTY\|F, [help][hlp-backends]). | The reference host: every key with key-up events, the shared clipboard. |
| E2 | kitty 0.20 or later (or Ghostty, Alacritty, WezTerm on Linux), plain local shell. | The **kitty keyboard protocol** and OSC 52. |
| E3 | Windows Terminal (or WezTerm on Windows) with an SSH session to a Unix host, or a local manager on Windows. | **win32-input-mode**. |
| E4 | xterm or any terminal with none of the above, `TERM=xterm-256color`. | Legacy input: the fallback paths. |
| E5 | tmux 3.x inside E2. | A multiplexer that strips protocol negotiation. |
| E6 | The manager's own built-in terminal, running a probe shell, far2l `--tty`, and a second copy of the manager. | The host role (section 6). |
| E7 | The manager in the built-in terminal of f4 (its GUI window, or f4 in a desktop terminal under X11), and a desktop file manager to drag files from. | Drag and drop (section 7); f4 is the only terminal side of the proposal so far. |

Every test of section 2 uses the same fixture, created by:

```sh
mkdir -p /tmp/fc/left/sub /tmp/fc/right
cd /tmp/fc/left
printf a > a.txt; printf bb > b.txt; printf c > c.log; printf d > .hidden
printf 'line 1\nline 2\n' > text.txt
tar -cf arc.tar a.txt b.txt
```

The left panel shows `/tmp/fc/left`, the right panel `/tmp/fc/right`, the
cursor is on `a.txt` and nothing is selected, unless a test says otherwise.

## 2. Keys (`K`)

A key requirement passes when the key, pressed in the environments that
section 8 names for the claimed level, does what the *Action* column says; a key that cannot be encoded by a legacy terminal (marked ⌨ below) is not checked
in E4, but then the manager MUST still offer the action in another way (menu or
command palette). The *far2l* column links the help topic and the code that
implements the key.

A manager MAY implement the Far keys as an optional mode (for example a "Far
mode" setting); the claim then names the mode (section 9), and every check is
done with the mode on.

### 2.1. Panels

| ID | Keys | Action | Req | far2l |
| -- | ---- | ------ | --- | ----- |
| K-01 | `Tab` | Switch the active panel. | MUST | [help][hlp-panel], [code][fp-tab] |
| K-02 | `Ctrl+O` | Hide both panels and show the user screen (the output of commands); press again to show the panels. | MUST | [help][hlp-panel], [code][fp-ctrlo] |
| K-03 | `Ctrl+U` | Swap the panels (the left panel's folder and mode go right and back). | MUST | [help][hlp-panel], [code][fp-ctrlu] |
| K-04 | `Ctrl+P` | Hide or show the passive panel. | SHOULD | [code][fp-ctrlp] |
| K-05 | `Ctrl+F1`, `Ctrl+F2` | Hide or show the left, the right panel. | SHOULD | [code][fp-ctrlf1], [code][fp-ctrlf2] |
| K-06 | `Ctrl+L`, `Ctrl+Q`, `Ctrl+T` | Toggle the info panel, the quick view panel, the tree panel (in the passive panel). | SHOULD | [code][fp-ctrll] |
| K-07 | `Ctrl+R` | Re-read the active panel: a file created by another program appears. | MUST | [code][fl-ctrlr] |
| K-08 | `Ctrl+B` | Hide or show the key bar. | SHOULD | [code][fp-ctrlb] |
| K-09 | `Ctrl+Left`, `Ctrl+Right` with an empty command line; `Ctrl+Up`, `Ctrl+Down` | Change the width of the panels; change their height. | SHOULD | [code][fp-ctrlleft], [code][fp-ctrlup] |
| K-10 | `Ctrl+1`, `Ctrl+2`, `Ctrl+3` ⌨ | View mode brief, medium, full. | MUST | [help][hlp-panel], [code][fp-ctrl0] |
| K-11 | `Ctrl+4` … `Ctrl+9`, `Ctrl+0` ⌨ | View modes wide, detailed, descriptions, long descriptions, owners, links, alternative full (for the modes the manager has). | SHOULD | [code][fp-ctrl0] |
| K-12 | `Ctrl+F3`, `Ctrl+F4`, `Ctrl+F5`, `Ctrl+F6` | Sort by name, extension, modification time, size; the same key again reverses the order. | MUST | [help][hlp-panel], [code][fl-ctrlf3] |
| K-13 | `Ctrl+F7` … `Ctrl+F11` | Unsorted, by creation time, access time, description, owner (for the orders the manager has). | SHOULD | [code][fl-ctrlf3] |
| K-14 | `Ctrl+F12` | The sort modes menu. | MUST | [code][fl-ctrlf12] |
| K-15 | `Ctrl+H` | Show or hide hidden files (`.hidden` appears and disappears). | SHOULD | [code][fl-ctrlh] |
| K-16 | `Ctrl+Alt+Shift`, held ⌨ | The panels are hidden while the three keys are held and come back on release. Needs key-up events (P-03, P-04 or P-05). | SHOULD | [help][hlp-panel], [code][mg-cas] |
| K-17 | `Alt+Left`, `Alt+Right`, `Alt+Home`, `Alt+End` | Scroll long names in the panel. | SHOULD | [help][hlp-panel] |

### 2.2. Moving around and running

| ID | Keys | Action | Req | far2l |
| -- | ---- | ------ | --- | ----- |
| K-20 | `Enter` | On a folder: enter it. On an executable: run it. On an archive: open it (if the manager has archive support). With text in the command line: run the command line instead. | MUST | [code][fl-enter], [code][cl-key] |
| K-21 | `Shift+Enter` | Run in a separate window, or open the folder in the system file browser. | SHOULD | [help][hlp-func], [code][fl-shiftenter] |
| K-22 | `Ctrl+PgUp` | Go to the parent folder, the cursor on the folder that was left. | MUST | [help][hlp-func], [code][fl-ctrlpgup] |
| K-23 | `Ctrl+PgDn` | Enter the folder or the archive under the cursor. | MUST | [help][hlp-func], [code][fl-ctrlpgdn] |
| K-24 | ``Ctrl+\``, ``Ctrl+` `` | Go to the root folder, to the home folder. | SHOULD | [help][hlp-func], [code][fl-ctrlbs] |
| K-25 | `Alt+F1`, `Alt+F2` | The location (drive) menu for the left, the right panel. | MUST | [help][hlp-func], [code][fp-altf1], [code][fp-altf2] |
| K-26 | `Ctrl+Shift+0` … `Ctrl+Shift+9`; `RightCtrl+0` … `RightCtrl+9` ⌨ | Remember the current folder as a bookmark; go to a bookmark. | SHOULD | [help][hlp-panel], [code][fl-bookmarks] |
| K-27 | `F12` | The list of screens (panels, viewers, editors, background commands). | SHOULD | [code][mg-f12] |

### 2.3. Selection

"Gray" keys are the keys of the numeric keypad. A legacy terminal sends them as
the same characters as the main keyboard; far2l therefore treats `+`, `-` and
`*` of either keyboard as `Gray +`, `Gray -`, `Gray *` while the command line is
empty ([code][fl-plainplus]), and so does K-31 to K-33. With a modifier
(K-34 to K-36) only a terminal that reports the keypad separately can tell them
apart.

| ID | Keys | Action | Req | far2l |
| -- | ---- | ------ | --- | ----- |
| K-30 | `Ins`; `Shift+Up`, `Shift+Down`, `Shift+PgUp`, `Shift+PgDn`, `Shift+Home`, `Shift+End` | Select or deselect the file under the cursor and move down; select a range. | MUST | [help][hlp-panel], [code][fl-ins], [code][fl-shiftdown] |
| K-31 | `Gray +`, `Gray -` with an empty command line | Select, deselect files by a mask (dialog; `*.txt` selects `a.txt`, `b.txt`, `text.txt`). | MUST | [code][fl-add], [code][fl-sub] |
| K-32 | `Gray *` with an empty command line | Invert the selection of files. | MUST | [code][fl-mul] |
| K-33 | `Shift+Gray +`, `Shift+Gray -` | Select all files; deselect all. | MUST | [code][fl-shiftadd] |
| K-34 | `Ctrl+Gray +`, `Ctrl+Gray -` ⌨ | Select, deselect the files with the extension of the current file. | SHOULD | [code][fl-ctrladd], [code][fl-ctrlsub] |
| K-35 | `Alt+Gray +`, `Alt+Gray -` ⌨ | Select, deselect the files with the name of the current file. | SHOULD | [code][fl-altadd] |
| K-36 | `Ctrl+Gray *` ⌨ | Invert the selection including folders. | SHOULD | [code][fl-ctrlmul] |
| K-37 | `Ctrl+M` | Restore the previous selection. | SHOULD | [code][fl-ctrlm] |
| K-38 | `Ctrl+Ins` with an empty command line; `Ctrl+Shift+Ins`; `Alt+Shift+Ins` | Copy the names of the selected files to the clipboard, one per line; copy the full names. | SHOULD | [help][hlp-panel], [code][fl-ctrlins] |

### 2.4. Function keys

| ID | Keys | Action | Req | far2l |
| -- | ---- | ------ | --- | ----- |
| K-40 | `F1` | Help. | MUST | [code][fp-f1] |
| K-41 | `F2` | The user menu. | SHOULD | [help][hlp-func] |
| K-42 | `F3`; `Alt+F3`, `Ctrl+Shift+F3` | View the file (on a folder: compute its size); the other viewer. | MUST for `F3`, SHOULD for the rest | [help][hlp-func], [code][fl-f3] |
| K-43 | `F4`; `Shift+F4`; `Alt+F4`, `Ctrl+Shift+F4` | Edit the file; edit a new file (asks the name); the other editor. | MUST for `F4`, SHOULD for the rest | [help][hlp-func], [code][fl-f4], [code][fl-shiftf4] |
| K-44 | `F5`, `F6` | Copy, move or rename the selected files (or the file under the cursor); the dialog proposes the folder of the passive panel. | MUST | [help][hlp-func], [code][fl-f5] |
| K-45 | `Shift+F5`, `Shift+F6` | The same for the file under the cursor only, proposing its own name. | SHOULD | [code][fl-shiftf5] |
| K-46 | `F7` | Make a folder. | MUST | [code][fl-f7] |
| K-47 | `F8`; `Shift+F8`; `Shift+Del`; `Alt+Del` | Delete the selected files (or the file under the cursor); delete the file under the cursor only; delete without the trash; wipe. | MUST for `F8`, SHOULD for the rest | [help][hlp-delete], [code][fl-f8] |
| K-48 | `F9` | The menu bar. | MUST | [code][fp-f9] |
| K-49 | `F10` | Quit (with a confirmation if the manager has one). | MUST | [code][cl-f10] |
| K-50 | `F11` | The plugin commands menu. | SHOULD | [code][mg-f11] |
| K-51 | `Alt+F7`; `Alt+F10` | Find files; find a folder. | MUST for `Alt+F7`, SHOULD for `Alt+F10` | [help][hlp-func], [code][fp-altf7], [code][cl-altf10] |
| K-52 | `Alt+F8`; `Alt+F12`; `Alt+F11` | The command history; the folder history; the view and edit history. | MUST for `Alt+F8` and `Alt+F12`, SHOULD for `Alt+F11` | [help][hlp-func], [code][cl-altf8], [code][cl-altf12], [code][cl-altf11] |
| K-53 | `Ctrl+A`; `Ctrl+G`; `Ctrl+Z` | File attributes; apply a command to the selected files; describe files. | SHOULD | [help][hlp-func], [code][fl-ctrla], [code][fl-ctrlg], [code][fl-ctrlz] |
| K-54 | `Alt+F5`, `Alt+F6`, `Alt+F9`, `Shift+F1`, `Shift+F2`, `Shift+F3` | Print; make links; maximize the window; archive: add, extract, commands. | SHOULD | [help][hlp-func], [code][fl-altf6], [code][mg-altf9], [code][fl-shiftf1] |

### 2.5. Fast find

Far's fast find is started by holding `Alt` and typing the beginning of a name
([help][hlp-fastfind]). In a legacy terminal `Alt+a` arrives as `ESC a`, so this
section depends on P-08.

| ID | Keys | Action | Req | far2l |
| -- | ---- | ------ | --- | ----- |
| K-60 | `Alt+<character>`, `Alt+Shift+<character>` | Open the fast find box with that character and put the cursor on the first name that starts with it (`Alt+b` puts it on `b.txt`). | MUST | [help][hlp-fastfind], [code][fl-fastfind], [code][pn-fastfind] |
| K-61 | more characters, with or without `Alt` | Extend the search; a character that matches nothing is not added. | MUST | [code][pn-fastfind] |
| K-62 | `Ctrl+Enter`; `Ctrl+Shift+Enter` | The next match; the previous match. | MUST for `Ctrl+Enter`, SHOULD for `Ctrl+Shift+Enter` | [help][hlp-fastfind], [code][pn-ctrlshiftenter] |
| K-63 | `*`, `?` in the box | Wildcards (`Alt+*`, `l`, `o` finds `c.log`). | SHOULD | [help][hlp-fastfind] |
| K-64 | `Ctrl+V`, `Shift+Ins`, or the terminal's paste | Pasted text goes into the box, as far as it matches. | SHOULD | [help][hlp-fastfind], [code][pn-paste] |
| K-65 | any other key | Closes the box and acts normally: `Enter` opens the found file, arrows move the cursor, `Esc` only closes. | MUST | [code][pn-fastfind] |
| K-66 | `Alt+<character>` in a non-Latin keyboard layout | Searches for the character of the layout (a Cyrillic `Alt+ф` finds `файл`). | SHOULD | [code][pn-kbdlayout] |

### 2.6. Command line

| ID | Keys | Action | Req | far2l |
| -- | ---- | ------ | --- | ----- |
| K-70 | printable characters while the panels are shown | Go into the command line, not into a panel action (except `Gray` keys with an empty line, K-31 to K-33). | MUST | [help][hlp-cmdline], [code][cl-key] |
| K-71 | `Ctrl+E`, `Ctrl+X`; `Up`, `Down` while the panels are hidden | The previous and the next command from the history. | MUST | [help][hlp-cmdline], [code][cl-ctrle] |
| K-72 | `Ctrl+Y`; `Ctrl+K`; `Ctrl+BS`, `Ctrl+Del` | Clear the line; delete to the end; delete a word left, right. | MUST for `Ctrl+Y`, SHOULD for the rest | [help][hlp-cmdline], [code][ed-ctrly], [code][ed-ctrlk] |
| K-73 | `Ctrl+Left`, `Ctrl+Right`, `Ctrl+Home`, `Ctrl+End`, `Home`, `End` with text in the line | Move by word; to the start, to the end of the line. | MUST | [help][hlp-cmdline] |
| K-74 | `Ctrl+Enter`, `Ctrl+J` | Insert the name of the file under the cursor (quoted if needed) and a space. | MUST | [help][hlp-cmdline], [code][fl-ctrlenter] |
| K-75 | `Ctrl+Shift+Enter` | Insert the name of the file under the cursor of the passive panel. | SHOULD | [code][fl-ctrlshiftenter] |
| K-76 | `Ctrl+F`; `Ctrl+;` | Insert the full name of the file of the active panel; of the passive panel. | SHOULD | [help][hlp-cmdline], [code][fl-ctrlf] |
| K-77 | `Ctrl+[`, `Ctrl+]` ⌨ | Insert the path of the left panel; of the right panel. | MUST | [help][hlp-cmdline], [code][fl-ctrlbracket] |
| K-78 | `Ctrl+Shift+[`, `Ctrl+Shift+]` ⌨ | Insert the path of the active panel; of the passive panel. | SHOULD | [code][fl-ctrlshiftbracket] |
| K-79 | `Ctrl+End` at the end of the line | Complete the line from the history; again: the next candidate. | SHOULD | [help][hlp-cmdline], [code][ed-ctrlend] |
| K-80 | `Shift+Left`, `Shift+Right`, `Shift+Home`, `Shift+End`; `Ctrl+Ins`; `Shift+Ins`, `Ctrl+V` | Select in the line; copy the selection; paste. | MUST | [help][hlp-cmdline] |

`Ctrl+[` is the byte `ESC` in a legacy terminal, so K-77 is checked only in E1,
E2 and E3; in E4 the manager passes K-77 if `Ctrl+[` is not taken for `Esc` in a
way that breaks `Esc` itself.

### 2.7. Lists, menus, dialogs

| ID | Keys | Action | Req | far2l |
| -- | ---- | ------ | --- | ----- |
| K-85 | in the history lists: `Enter`, `Shift+Enter`; `Del`; `Shift+Del`; `Ins`; `Ctrl+C`, `Ctrl+Ins` | Use the item; run it at once; clear the history; delete the item; lock it; copy its text. | SHOULD | [help][hlp-history] |
| K-86 | in dialogs: `Tab`, `Shift+Tab`; `Enter`; `Esc`; `Space` | Next, previous control; the default button; cancel; toggle a check box. | MUST | [help][hlp-misc] |
| K-87 | in dialog edit lines: `Ctrl+Up`, `Ctrl+Down` | The history of this edit line. | SHOULD | [help][hlp-misc] |
| K-88 | in dialogs: `Ctrl+Enter`; `Shift+Enter`, `Ctrl+Shift+Enter` in an edit line | Press the default button from any control; insert the name of the file under the cursor of the active, of the passive panel. | SHOULD | [help][hlp-misc] |
| K-89 | in menus and lists: `Ctrl+Alt+F` | Filter the items by typed text. | SHOULD | [help][hlp-menu] |

### 2.8. Commands and the user screen

| ID | Keys | Action | Req | far2l |
| -- | ---- | ------ | --- | ----- |
| K-90 | `Ctrl+O` after a command | The output of the command stays on the user screen, under the panels, and is shown again by `Ctrl+O`. | MUST | [help][hlp-terminal] |
| K-91 | `Ctrl+C`, `Ctrl+Z` while a command runs | Reach the command as the usual signals; the manager does not quit. | MUST | [help][hlp-terminal] |
| K-92 | `Ctrl+Shift+F3`, `Ctrl+Shift+F4` while the user screen is shown | Open the output history in the viewer, in the editor. | SHOULD | [help][hlp-terminal] |
| K-93 | `Alt+Ins` | Grab a part of the screen into the clipboard. | SHOULD | [help][hlp-misc], [code][mg-altins] |

### 2.9. Rules for all keys

| ID | Rule | Req | far2l |
| -- | ---- | --- | ----- |
| K-95 | `RightCtrl` acts as `Ctrl` for every key that has no `RightCtrl` meaning of its own (K-26). | SHOULD | [help][hlp-panel] |
| K-96 | `Ctrl+<letter>` and `Alt+<letter>` keys work in a non-Latin keyboard layout as in the Latin one (with P-04 or P-05, which report the key; with P-03, which reports the base layout key). | SHOULD | [code][pn-kbdlayout] |
| K-97 | Every MUST key of this section that the terminal cannot encode (marked ⌨) has another way to the same action: a menu, a command palette or a remapping file. | MUST | — |

## 3. Clipboard (`C`)

"System clipboard" is the clipboard of the desktop the user sits at: X11 or
Wayland, macOS, Windows. When the manager runs over SSH it is the clipboard of
the user's machine, not of the server. The text used below is
`far2l-clip ✓ тест` plus a tab and a second line `line 2`.

| ID | Requirement | Req | Check | Sources |
| -- | ----------- | --- | ----- | ------- |
| C-01 | **Copy reaches the system clipboard** when the manager runs on the user's machine (its own GUI window, or a terminal of the desktop): selecting text in the command line and pressing `Ctrl+Ins`, copying in the viewer or the editor, and K-38 put the text on the system clipboard, directly or through C-03 or P-07. | MUST | E2 local: copy the test text from the command line; paste it in another desktop program: identical. | [far2l help][hlp-paste] |
| C-02 | **Paste takes the system clipboard.** `Shift+Ins` and `Ctrl+V` in the command line, dialogs and the editor paste what another desktop program copied, when the manager can read the system clipboard (directly or through C-03). Where it cannot (a plain terminal over SSH), the terminal's own paste key works through P-01 and section 5, and the help of the manager names that key. | MUST | E2 local and E1: copy the test text in another program, paste in the command line with `Shift+Ins`: identical. E4 over SSH: the terminal's paste key gives the same. | [far2l help][hlp-paste] |
| C-03 | **The far2l clipboard.** While the far2l extensions are on (P-05), the manager uses their clipboard: `CLIP_OPEN` with a client ID of 32 to 256 characters of `0-9 a-z - _` that is generated once and kept per installation (so that far2l's "Share clipboard always" is asked once), `CLIP_SETDATA` with `CF_TEXT` (1, UTF-8) for a copy, `CLIP_GETDATA` right after the user's paste key (the host answers reads only within 5 seconds of it) for a paste, `CLIP_CLOSE` after each operation. An answer `-1` to `CLIP_OPEN` switches the manager to its own clipboard for the session; `0` is retried at the next use. It SHOULD send data longer than 16 KiB with `CLIP_SETDATACHUNK` when the host reports `FARTTY_FEATCLIP_CHUNKED_SET`. | MUST | E1: copy the test text in the manager, quit it with `F10`, `Shift+Ins` in far2l's command line: identical; copy a text in far2l, start the manager again, `Shift+Ins` in its command line: identical. Quit and start the manager twice: with "Share clipboard always" chosen, far2l does not ask again. | [VTExts 5.7][vtx-5-7], [VTExts 5.7.1][vtx-5-7-1], [VTExts 5.7.2][vtx-5-7-2], [far2l client][f2-clip-client] |
| C-04 | **Over SSH.** The manager on a remote host, reached by `ssh` from far2l's built-in terminal (E1) or from putty4far2l or KiTTY, copies to and pastes from the user's clipboard through C-03; reached from E2, it copies through P-07. | MUST | E1: `ssh localhost`, start the manager there, repeat the C-03 check. E2: `ssh localhost`, repeat C-01. | [far2l help][hlp-remote], [VTExts 3][vtx-3] |
| C-05 | **Text survives.** Non-ASCII letters, emoji, tabs and line breaks are copied and pasted unchanged; line breaks become those of the platform (LF on Unix, CRLF on Windows). | MUST | The test text through C-01, C-02, C-03: byte-identical (`xclip -o \| od -c`, `pbpaste \| od -c`). | [VTExts 5.7.6][vtx-5-7-6] |
| C-06 | **Large data.** Copying 1 MiB of text does not freeze the user interface for more than one second (the far2l client sets the clipboard in the background). | SHOULD | E1: `seq 1 150000 > /tmp/fc/big.txt`, open in the viewer or editor, select all, copy: the interface keeps responding; the paste in far2l gives the whole text. | [VTExts 5.7.3][vtx-5-7-3] |
| C-07 | **Files on the clipboard.** Copying files in a panel (far2l: `Ctrl+C`) puts them on the system clipboard as files (`text/uri-list` on Unix, `CF_HDROP` on Windows) as well as their names as text; pasting files copied in another file manager copies them into the active panel. | SHOULD | Local GUI or E2 local: copy `a.txt` in the manager, paste in the desktop file manager: the file is copied; the reverse. | [far2l help][hlp-panel] |
| C-08 | **Copy and paste always work inside the manager.** With no route to the system clipboard (E4 without OSC 52), text copied in the manager can still be pasted in the manager. | MUST | E4: copy the test text in the command line, clear it with `Ctrl+Y`, paste with `Shift+Ins`: identical. | [far2l help][hlp-paste] |

## 4. Terminal protocols, the manager as the client (`P`)

The manager runs inside a terminal. The keys of section 2 and the clipboard of
section 3 reach it only through these protocols, so they are requirements of
their own. Each one is negotiated: the manager switches it on, and a terminal
that does not know it ignores the request.

**Recording what the manager sends.** Several checks below read the bytes the
manager writes to the terminal. Record them with `script` (util-linux):

```sh
script -q -f -c '<manager>' /tmp/fc/out.log   # do the action, quit with F10
cat -v /tmp/fc/out.log | head -c 3000; echo; cat -v /tmp/fc/out.log | tail -c 600
```

On macOS and BSD: `script -q /tmp/fc/out.log <manager>`. In `cat -v` output
`ESC` is shown as `^[`, `BEL` as `^G`.

| ID | Requirement | Req | Check | Sources |
| -- | ----------- | --- | ----- | ------- |
| P-01 | **Bracketed paste.** The manager sends `CSI ? 2004 h` when it starts and `CSI ? 2004 l` when it exits, and takes everything between `CSI 200 ~` and `CSI 201 ~` as one paste (section 5), never as key presses. | MUST | Recording in E2: `^[[?2004h` near the start, `^[[?2004l` near the end. B-01 passes in E2. | [xterm][xt-bp], [far2l][f2-ttyout-init] |
| P-02 | **Modes are given back.** Every mode the manager switches on (P-01, P-03, P-04, P-05, P-09, P-10, the alternate screen) is switched off, and the kitty flags are restored, (a) when it exits, (b) when it is suspended (`Ctrl+Z`, `SIGTSTP`) and again on when it resumes, (c) while an external program runs in the same terminal without a PTY of the manager's own, and again on after it. | MUST | E2: start the manager, quit with `F10`; run `timeout 5 cat -v` and press `a`, `Ctrl+Shift+A`, then paste `x`: expected `a^Ax`, no `^[[` sequences. The same after (b) `Ctrl+Z` in the manager and after (c) a command run from its command line, if the manager runs commands directly in its terminal. | [far2l][f2-ttyout-fini], [VTExts 8.2][vtx-8-2] |
| P-03 | **kitty keyboard protocol.** The manager asks for at least flag 1 (disambiguate) and SHOULD ask for 15 (also event types, alternate keys, all keys as escape codes, which K-16 and K-96 need), by `CSI > flags u` (push) or `CSI = flags ; 1 u`, and restores the previous flags on exit (`CSI < u`, or `CSI = 0 ; 1 u`). It decodes `CSI code ; modifiers[:event] u` and the legacy-shaped forms kitty keeps (`CSI 1 ; modifiers A`…, `CSI number ; modifiers ~`). It MAY detect the protocol with `CSI ? u`, answered by `CSI ? flags u`, followed by `CSI c` whose answer ends the wait. | MUST | E2: K-10 (`Ctrl+1`), K-77 (`Ctrl+[`) and K-34 (`Ctrl+Gray +`) work. Recording: `^[[>` or `^[[=` near the start and `^[[<u` or `^[[=0;1u` near the end. | [kitty spec][kitty-kb], [far2l enable][f2-ttyout-kitty], [far2l decode][f2-parse-kitty] |
| P-04 | **win32-input-mode.** The manager sends `CSI ? 9001 h` at start and `CSI ? 9001 l` at exit and decodes `CSI Vk ; Sc ; Uc ; Kd ; Cs ; Rc _` key records, both key-down (`Kd` = 1) and key-up (`Kd` = 0). It SHOULD unwrap mouse and other sequences that a ConPTY delivers encoded as key records (far2l does). | MUST | E3: K-10 (`Ctrl+1`), K-26 (`RightCtrl+0`) and K-16 (`Ctrl+Alt+Shift` held) work. Recording: `^[[?9001h`, `^[[?9001l`. | [Microsoft spec][w32-spec], [far2l decode][f2-parse-w32], [far2l unwrap][f2-parse-w32double] |
| P-05 | **far2l terminal extensions: activation and input.** The manager writes `ESC _ far2l1 ESC \` followed by `ESC [ 5 n` and reads until the answer to the DSR (`ESC [ 0 n`); the extensions are on if `ESC _ far2lok` (ended by `BEL` or `ST`) came first. While they are on it takes keys and mouse from the events `ESC _ f2l<base64> BEL` (`K`/`k` key down and up, `M` mouse; the compact forms only if it asked for them with `x`), and sends `ESC _ far2l0 ESC \` on exit and on suspend, and probes again on resume. A terminal that does not answer causes no visible text and no delay beyond the DSR answer. | MUST | E1: K-16 (`Ctrl+Alt+Shift` held), K-26 (`RightCtrl+0` differs from `Ctrl+0`) and K-10 work. E4: the recording shows `^[_far2l1^[\` and `^[[5n`, the manager is ready as fast as without the probe, and nothing of the probe stays on the screen. | [VTExts 4.5][vtx-4-5], [VTExts 6][vtx-6], [VTExts 8.4][vtx-8-4], [far2l probe][f2-caps-probe], [far2l exit][f2-caps-fini] |
| P-06 | **far2l terminal extensions: no hang.** A request with a non-zero ID that is never answered does not freeze the manager: the manager has a timeout on replies and then goes on as if the terminal had no such feature. | SHOULD | Review of the code, or a test terminal that acknowledges `far2l1` and answers nothing else (the self-check kit, section 10). | [VTExts 8.4][vtx-8-4] |
| P-07 | **OSC 52 for copying.** When neither the system clipboard of the local machine nor the clipboard of the far2l extensions (C-03) is available, a copy in the manager is sent to the terminal as `OSC 52 ; c ; <base64> ST` (or `BEL`). The manager does not depend on the OSC 52 read form (`?`) for pasting: most terminals refuse it, and paste comes by P-01. | MUST | E2 over `ssh localhost` (kitty: `clipboard_control write-clipboard`; other terminals: their OSC 52 setting): C-01 passes. Recording: `^[]52;` followed by Base64 of the copied text. | [xterm][xt-osc], [far2l][f2-ttyout-osc52], [far2l help][hlp-osc52] |
| P-08 | **Alt as an ESC prefix.** In legacy input `ESC <character>` is `Alt+<character>`, and a lone `ESC` is the `Esc` key once no other byte follows within a short time (SHOULD be at most 100 ms). | MUST | E4: K-60 (`Alt+b` puts the cursor on `b.txt`); `Esc` closes an open dialog at once, without a visible pause. | [far2l parser][f2-parse-esc] |
| P-09 | **Mouse.** The manager asks for button and drag reports in the SGR form (`CSI ? 1000 h`, `CSI ? 1002 h`, `CSI ? 1006 h`) and decodes `CSI < b ; x ; y M/m`: click selects, double click opens, the wheel scrolls. | SHOULD | E2: click a file, double-click a folder, scroll a long folder. | [far2l][f2-ttyout-mouse] |
| P-10 | **Focus reports.** The manager asks for `CSI ? 1004 h` and uses `CSI I` / `CSI O` (for example to show desktop notifications only when it is not focused, as far2l does). | SHOULD | Recording: `^[[?1004h`. | [VTExts 8.2][vtx-8-2] |
| P-11 | **One key, one event.** When several of P-03, P-04, P-05 are on at the same time, each key press is delivered to the manager once. | MUST | E1, E2, E3: type `abc` and press `Ctrl+Y` in the command line: `abc` appears exactly once, and is cleared. | [far2l parser][f2-parse-order] |
| P-12 | **Behind a multiplexer.** In E5 the manager starts, every MUST key without ⌨ works, and no reply text of a probe (P-03, P-05) appears on the screen or in the command line. | MUST | E5 (tmux in kitty): repeat K-01, K-20, K-60, K-74; look at the screen and the command line after start. | — |

## 5. Bracketed paste fast path (`B`)

A paste is a block of text, not typing. far2l collects everything between the
paste markers into one text and hands it to the command line, the edit line or
the editor as one operation ([far2l][f2-kb-paste]). The same is required here
for a paste that arrives through P-01, and for `Shift+Ins` / `Ctrl+V` from the
manager's own clipboard.

The text for the checks is made with `seq 1 2000` (2000 lines) and put on the
clipboard with the desktop's tool (`xclip -selection clipboard`, `wl-copy`,
`pbcopy`, `clip`), unless the check says otherwise. "The terminal's paste key"
is the paste of the terminal emulator (kitty: `Ctrl+Shift+V`), which sends the
text through P-01.

| ID | Requirement | Req | Check | Sources |
| -- | ----------- | --- | ----- | ------- |
| B-01 | **One paste, one edit.** A paste into the editor, a dialog edit line or the command line is inserted as a single change: one undo step removes it whole, and no per-character work is done for it (autocompletion lists, history search, syntax re-colouring per character, a redraw per character). | MUST | E2: `F4` on `text.txt`, the terminal's paste key with the 2000 lines, then one undo (`Ctrl+Z`): the file has its two lines again. | [far2l editor][f2-ed-paste], [far2l edit line][f2-edit-paste] |
| B-02 | **No auto-indent on paste.** The editor does not apply auto-indent or other typing aids to pasted lines. | MUST | E2: auto-indent on, paste the three lines `a`, `␣␣b`, `␣␣␣␣c` (spaces), save: the file holds exactly those lines. | [far2l][f2-ed-autoindent] |
| B-03 | **No line of a multi-line paste runs by itself.** A multi-line paste into the command line is either inserted as text or shown whole with a question before it runs as one script (far2l asks: run, cancel, never ask again); nothing runs before the user confirms, and a line break in the pasted text never acts as `Enter`. | MUST | E2: put `touch /tmp/fc/B03a` and `touch /tmp/fc/B03b` (two lines) on the clipboard; paste with the terminal's paste key into the empty command line; before answering anything, in another terminal `ls /tmp/fc/B03*`: no such files. | [far2l][f2-cl-multiline] |
| B-04 | **Pasted bytes are text.** Escape sequences, control characters, `+`, `-`, `*`, `F`-key sequences inside a paste are inserted as text or dropped, never acted on as keys (no panel selection dialog, no cursor movement, no closed dialog). | MUST | E2: put `+-*` and a line with a literal ESC character and `[A` (`printf '+-*\n\033[A\n'`) on the clipboard; paste into the empty command line and into the editor: no selection dialog opens, the cursor does not jump, the text (with the ESC dropped or shown) is inserted. | [far2l][f2-kb-append] |
| B-05 | **Paste in the panels.** With the panels active, a paste goes into the command line, or into fast find while it is open (K-64). | MUST | E2: panels active, empty command line, the terminal's paste key with `echo pasted`: the command line holds `echo pasted`. | [far2l][pn-paste], [far2l][f2-cl-multiline] |
| B-06 | **Line breaks.** `CR`, `LF` and `CRLF` in a paste all become one line break of the target (some terminals send `LF` inside bracketed paste, others `CR`). | SHOULD | E2 and E3: paste the 2000 lines into the editor: 2000 lines, no empty lines in between, no `^M`. | [far2l][f2-parse-lf] |
| B-07 | **Speed.** Pasting 1 MiB (`seq 1 150000`) into the editor takes at most two seconds, and a 100 KiB single line into the command line at most one second, in E2 on the local machine. | SHOULD | E2: time from the paste key to the last line shown. | [f4 README][f4-readme-paste] |
| B-08 | **The own clipboard takes the same path.** `Shift+Ins` and `Ctrl+V` from the manager's clipboard follow B-01 to B-05 like a terminal paste. | MUST | E2: copy the 2000 lines inside the manager (viewer, select all, copy), paste in the editor and in the command line: as in B-01 and B-03. | [far2l][f2-cl-multiline] |

## 6. The built-in terminal, the manager as the host (`T`)

These requirements apply only to a manager that has a built-in terminal (a PTY
in which it runs the shell and the commands). The manager is then the terminal
for the programs inside, and has to give them what section 4 asks from a
terminal. All checks are made in E6, typed at the shell prompt of the built-in
terminal (with the panels hidden by `Ctrl+O` where the manager needs it). The
probes use `bash`; `timeout 5 cat -v` shows what the program receives and ends
by itself, because some modes also encode `Ctrl+C`.

| ID | Requirement | Req | Check | Sources |
| -- | ----------- | --- | ----- | ------- |
| T-01 | **Status reports.** `CSI 5 n` is answered by `CSI 0 n`, `CSI 6 n` by `CSI <row> ; <col> R`, `CSI c` by a primary device attributes reply `CSI ? … c`. The far2l probe and the kitty probe end on these answers. | MUST | `printf '\e[5n'; IFS= read -rs -t1 -dn r; printf '%q\n' "$r"` prints `$'\E[0'`; with `\e[6n` and `-dR`: `$'\E[<row>;<col>'`; with `\e[c` and `-dc`: `$'\E[?…'`. | [far2l DSR][f2-vt-dsr], [far2l DA][f2-vt-da] |
| T-02 | **Bracketed paste.** The host follows `CSI ? 2004 h/l` of the program. While it is on, a paste into the terminal (the paste key of the manager, `Shift+Ins`, a dropped text) is sent wrapped in `CSI 200 ~` … `CSI 201 ~`, with line breaks sent as `CR`; while it is off, the plain text is sent. | MUST | `printf '\e[?2004h'; timeout 5 cat -v`, paste the two lines `x`, `y`: `^[[200~x^My^[[201~`. Then `printf '\e[?2004l'; timeout 5 cat -v`, paste again: `x`, `y` without markers. | [far2l mode][f2-vt-2004], [far2l paste][f2-vt-paste] |
| T-03 | **kitty keyboard protocol.** The host keeps the flags of the program: `CSI = flags ; mode u` (mode 1 set, 2 or, 3 and-not), `CSI > flags u` push and `CSI < n u` pop (a stack of at least 8 entries), `CSI ? u` answered by `CSI ? flags u`, and encodes keys by those flags, including key releases with flag 2. | MUST | `printf '\e[>1u\e[?u'; IFS= read -rs -t1 -du r; printf '%q\n' "$r"` prints `$'\E[?1'`. `printf '\e[>1u'; timeout 5 cat -v; printf '\e[<u'`, press `Ctrl+Shift+A`: `^[[97;6u`. | [kitty spec][kitty-kb], [far2l flags][f2-vt-kitty], [far2l encode][f2-vt-kittyenc] |
| T-04 | **win32-input-mode.** The host follows `CSI ? 9001 h/l` and, while it is on, sends every key down and up as `CSI Vk ; Sc ; Uc ; Kd ; Cs ; Rc _`. It SHOULD answer the mode query `CSI ? 9001 $ p` (and the same for 2004) with `CSI ? 9001 ; 1 $ y` or `; 2 $ y`. | MUST | `printf '\e[?9001h'; timeout 5 cat -v; printf '\e[?9001l'`, press `a`: `^[[65;30;97;1;0;1_^[[65;30;97;0;0;1_`. | [Microsoft spec][w32-spec], [far2l mode][f2-vt-9001], [far2l encode][f2-vt-w32enc], [far2l DECRQM][f2-vt-decrqm] |
| T-05 | **far2l terminal extensions.** (a) `ESC _ far2l1` is answered by `ESC _ far2lok BEL` before the answer to a following DSR; (b) every request with a non-zero ID gets a reply, the ID alone for what the host does not implement; (c) while the extensions are on, keys and mouse go to the program as events `ESC _ f2l<base64> BEL`, with key-up, the compact forms only if the program asked for them; (d) the clipboard requests either work with the protections of far2l (the user allows a client once or always; a read is answered only within 5 seconds of the user's paste key or middle click) or `CLIP_OPEN` is answered `-1` ("use your own clipboard"); (e) `ESC _ far2l0` and the end of the program switch the extensions off. | MUST | `printf '\e_far2l1\e\\\e[5n'; IFS= read -rs -t2 -dn r; printf '%q\n' "$r"` prints `$'\E_far2lok\a\E[0'`. The rest is checked by T-08 and by the self-check kit (section 10). | [VTExts 4.4][vtx-4-4], [VTExts 5.7][vtx-5-7], [VTExts 8.3][vtx-8-3], [VTExts 8.4][vtx-8-4], [far2l host][f2-vt-apc], [far2l events][f2-ext-key] |
| T-06 | **far2l extensions, the rest.** The host answers `w` (maximum window size), `p` (colour depth), `f` (F-key titles: shows them in its key bar or answers `false`), `n` (shows a desktop notification), and MAY send `S` terminal size events after `x` asked for them, with the argument order of VTExts. | SHOULD | Self-check kit (section 10); far2l `--tty` inside: its F-key titles appear in the host's key bar, if the host shows them. | [VTExts 5][vtx-5], [far2l][f2-ext-interact] |
| T-07 | **Modes end with the program.** When the program that set them exits, the host drops bracketed paste, the kitty flags, win32-input-mode, the mouse modes, focus reports, the far2l extensions and an OSC 52 permission given "for this command". | MUST | Run the command `printf '\e[?2004h\e[?9001h\e[>1u\e[?1000h'` and let it end; then `timeout 5 cat -v`, press `a`, `Ctrl+Shift+A`, paste `x`: `a^Ax`. | [far2l][f2-vt-reset] |
| T-08 | **far2l inside the manager.** `far2l --tty` started in the built-in terminal gets every key: `RightCtrl+0` and `Ctrl+0` differ, holding `Ctrl+Alt+Shift` hides its panels, `Ctrl+1` changes its view mode. Copying text in that far2l (select in its command line, `Ctrl+Ins`) puts the text on the clipboard of the manager's side, and `Shift+Ins` in that far2l pastes the manager's clipboard (after the clipboard question if the host asks). | MUST | E6 with far2l 2.6 or later installed: the steps of the requirement. | [far2l help][hlp-terminal], [VTExts 3][vtx-3] |
| T-09 | **OSC 52 writing.** `OSC 52 ; <selection> ; <base64>` from the program sets the manager's clipboard. The host SHOULD ask the user first (allow once, allow for this command, block), as far2l does. | MUST | `printf '\e]52;c;%s\a' "$(printf hello | base64)"`, allow if asked; paste in the manager's command line: `hello`. | [far2l][f2-vt-osc52] |
| T-10 | **No clipboard reading behind the user's back.** `OSC 52 ; c ; ?` is not answered with the clipboard contents unless the user allowed it for this request or by an explicit setting; the far2l `CLIP_GETDATA` follows T-05 (d). | MUST | Put a secret on the clipboard; `printf '\e]52;c;?\a'; IFS= read -rs -t2 -d $'\a' r; printf '%q\n' "$r"`: empty, or the host asked first and the answer was "deny". | [far2l][f2-vt-osc52], [VTExts 5.7.2][vtx-5-7-2] |
| T-11 | **Mouse and focus.** The host follows `CSI ? 1000/1002/1003/1006 h/l` and `CSI ? 1004 h/l` and sends the reports in the requested form. | SHOULD | `printf '\e[?1000h\e[?1006h'; timeout 5 cat -v; printf '\e[?1000l\e[?1006l'`, click: `^[[<0;x;yM^[[<0;x;ym`. | [far2l][f2-vt-mouse] |
| T-12 | **Keys reach the program.** While a program runs, every key goes to it, except the documented terminal keys of the manager (K-91, K-92 and the like), which the help lists; key remapping of the manager is not applied to it. | SHOULD | Run `timeout 5 cat -v` and press `F3`, `F10`, `Alt+F7`, `Ctrl+O`: each arrives as its sequence or is one of the documented exceptions. | [far2l help][hlp-terminal] |
| T-13 | **The manager inside itself.** A second copy of the manager started in the built-in terminal passes P-01 to P-05 (the first copy is the terminal). | SHOULD | E6: run P-01 to P-05 checks in the inner copy. | — |

## 7. Drag and drop (`D`)

Dropping files from the desktop on a terminal window is carried to the program
inside by the far2l extensions as proposed in [FAR2L_DND.md](FAR2L_DND.md)
(version 1, proposed upstream as elfmz/far2l#3647); the proposed letters are
not allocated in far2l yet, so this section follows that document and changes
with it. At the time of writing the terminal side exists in f4 only, so the
checks use **E7**: the manager running in the built-in terminal of f4 (f4's GUI
window, or f4 in a desktop terminal under X11), with a desktop file manager to
drag from. Dragging inside the manager between its own panels is in
[DRAGDROP.md](DRAGDROP.md) and is D-07.

| ID | Requirement | Req | Check | Sources |
| -- | ----------- | --- | ----- | ------- |
| D-01 | **Bind.** After P-05, the manager sends `BIND` (version 1, enable) when it can take drops, checks the whole reply, and disables its binding on exit (`far2l0` also revokes it). An empty reply means "no DnD" and nothing else changes. | MUST | E7: the drop of D-02 works; E1 (far2l, no DnD yet): the manager starts and works as without DnD. | [FAR2L_DND.md 4](FAR2L_DND.md#4-bind) |
| D-02 | **Drop on a panel.** On `INPUT_DND` the manager takes the offer with `LIST` and copies the items with `READ` into the folder of the panel under the drop position, through its own copy operation (its dialog, conflict questions and progress, as `F5`). The action is copy; with an unknown position the manager uses the active panel or asks. | MUST | E7: drag `a.txt` from the desktop file manager onto the right panel showing `/tmp/fc/right`: the copy dialog of the manager appears; after it, `cmp` of both files reports no difference. | [FAR2L_DND.md 5–7](FAR2L_DND.md#5-input_dnd-notification-only) |
| D-03 | **Close.** After the copy, a cancel or an error, the manager sends `CLOSE` with the reason, and no `READ` after it. | MUST | E7: drop a large file and cancel the copy dialog: the drop of another file afterwards works (the old offer did not stay open). | [FAR2L_DND.md 8](FAR2L_DND.md#8-close) |
| D-04 | **Limits.** The manager never asks for more than the negotiated chunk, accepts short reads and EOF, and keeps the request ID of a cancelled read until its reply has arrived. | MUST | Review of the code, or the self-check kit (section 10). | [FAR2L_DND.md 9](FAR2L_DND.md#9-limits-cancellation-nesting) |
| D-05 | **The built-in terminal is a drop target.** If the manager has a built-in terminal, a drop on it while a program inside has bound DnD is offered to that program (the manager is the terminal side of D-01 to D-04); otherwise the dropped paths are pasted as text through T-02. | MUST | E7 with the manager's own built-in terminal: start a second copy of the manager in it and drop a file on it: the inner copy receives it (D-02). | [FAR2L_DND.md](FAR2L_DND.md) |
| D-06 | **Nesting.** The manager inside a terminal with DnD, and a program inside the manager's built-in terminal: a drop on the outer window reaches the inner program through the manager acting as a proxy. | SHOULD | E7 with two levels: f4, the manager inside it, a second copy inside the manager; drop on the inner one. | [FAR2L_DND.md 9](FAR2L_DND.md#9-limits-cancellation-nesting) |
| D-07 | **Between the panels.** Dragging files with the mouse from one panel to the other copies them; the right button during the drag, or `Shift`, makes it a move. | SHOULD | E2: drag `a.txt` from the left panel to the right one. | [far2l help][hlp-dnd], [DRAGDROP.md](DRAGDROP.md) |

## 8. Levels

A level is claimed for the whole manager, in the environments the level names.
Every MUST listed for the level passes; SHOULD results are reported but never
block the level. Requirements about a feature the manager does not have are
N/A (section 0), except that a manager without a built-in terminal still has to
pass every `K`, `C`, `P` and `B` requirement of the level.

| Area | Basic | Full | Advanced |
| ---- | ----- | ---- | -------- |
| Keys | Every MUST of section 2, checked in one of E1, E2, E3 (the one whose input protocol the manager uses), and every MUST without ⌨ also in E4. | Every MUST of section 2 in E1, E2 and E3. | As Full. |
| Input | P-02, P-08, P-11, P-12 and at least one of P-03, P-04, P-05. | Every MUST of section 4. | As Full. |
| Clipboard | C-01, C-02, C-05, C-08 and at least one of C-03, P-07. | Every MUST of section 3. | As Full. |
| Paste | P-01 and every MUST of section 5. | As Basic. | As Basic. |
| Built-in terminal | T-01, T-02, T-07, T-10 and at least one of T-03, T-04, T-05. | Every MUST of section 6. | As Full. |
| Drag and drop | — | — | Every MUST of section 7 (D-05 only with a built-in terminal). |

An environment that cannot run the manager at all (for example E3 for a manager
that has no Windows build and no SSH use) is N/A for that manager, and the
report says why.

## 9. Claiming conformance

A claim is one line, in the manager's README or documentation:

```text
far2l compatibility: <Basic|Full|Advanced>, spec <version>, checked <YYYY-MM-DD> with <manager> <version> on <OS> (<mode, if any>), report: <link>
```

The report is a public page or a comment in an issue tracker with one row per
requirement of this version, in the order of this document:

```text
| ID | Result | Env | Note |
| K-01 | pass | E1, E2 | |
| K-10 | partial | E2 | Ctrl+1 works, Ctrl+3 opens the wrong mode: <issue link> |
| T-08 | N/A | — | no built-in terminal |
```

`Result` is one of `pass`, `fail`, `partial`, `N/A`; `partial` counts as
`fail` for the level. A failing MUST below the claimed level makes the claim
invalid until it is fixed, and the report links the issue for every `fail` and
`partial`. The claim stays valid for the version of the manager it names; a new
claim is made for a new version of this document.

The README of f4 lists the managers that published such a claim with a report:
name, level, version of this document, date, and the link.

## 10. Self-check kit

Planned (step 8 of [unxed/f4#2115](https://github.com/unxed/f4/issues/2115)):
a script that runs the automatic part of the checklist against any manager
(the probes of section 6, the recordings of section 4, and the paste checks
that can be driven through a PTY). Until it exists, the manual procedures of
this document are the reference.

## 11. Implementations to compare with

| Program | Client (section 4) | Host (section 6) | Notes |
| ------- | ------------------ | ---------------- | ----- |
| far2l | [`WinPort/src/Backend/TTY/`][f2-tty-dir] | [`far2l/src/vt/`][f2-vt-dir] | The reference for every protocol here, except DnD. |
| f4 | the `vtinput` and `vtui` libraries | `internal/terminal` | Implements both roles and the DnD proposal of section 7. |
| putty4far2l, KiTTY, Putty-Zeroes-Mod, putty-nd | — | far2l extensions | SSH clients on Windows that are hosts of the far2l extensions ([far2l help][hlp-terminals], [VTExts 9][vtx-9]). |
| kitty, Ghostty, Alacritty, WezTerm, Windows Terminal | — | kitty keyboard protocol or win32-input-mode, OSC 52 | Hosts for E2 and E3 ([far2l help][hlp-terminals]). |

## 12. Changes

* 0.1-draft (21 October 2026): first version: keys, clipboard, protocols as the
  client and the host, bracketed paste fast path, drag and drop, levels and the
  form of a claim.

[far2l-commit]: https://github.com/elfmz/far2l/commit/5ec2011794fa0c781e6dde92c84c77b5ae02b25b
[vtx-commit]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md
[vtx-4-1]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#41-roles
[hlp-panel]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L434
[hlp-fastfind]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L602
[hlp-cmdline]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L626
[hlp-func]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L696
[hlp-delete]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L864
[hlp-misc]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L892
[hlp-menu]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L420
[hlp-terminal]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L1571
[hlp-backends]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L1656
[hlp-history]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L2089
[fp-f1]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L501
[fp-tab]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L510
[fp-ctrlf1]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L516
[fp-ctrlf2]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L532
[fp-ctrlb]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L548
[fp-ctrll]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L559
[fp-ctrlo]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L606
[fp-ctrlp]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L639
[fp-ctrlu]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L658
[fp-altf1]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L681
[fp-altf2]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L689
[fp-altf7]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L697
[fp-ctrlup]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L713
[fp-ctrlleft]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L778
[fp-f9]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L824
[fp-ctrl0]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/filepanels.cpp#L833
[fl-bookmarks]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L874
[fl-shiftadd]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L928
[fl-add]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L943
[fl-sub]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L946
[fl-ctrladd]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L949
[fl-ctrlsub]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L952
[fl-altadd]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L955
[fl-mul]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L961
[fl-ctrlmul]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L964
[fl-ctrlins]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L977
[fl-ctrlenter]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1028
[fl-ctrlshiftenter]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1029
[fl-ctrlf]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1031
[fl-ctrlbracket]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1062
[fl-ctrlshiftbracket]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1064
[fl-ctrla]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1074
[fl-ctrlg]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1094
[fl-ctrlz]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1112
[fl-ctrlh]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1118
[fl-ctrlm]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1127
[fl-ctrlr]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1181
[fl-enter]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1200
[fl-shiftenter]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1201
[fl-ctrlbs]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1229
[fl-shiftf1]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1264
[fl-f3]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1304
[fl-f4]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1309
[fl-shiftf4]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1311
[fl-f5]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1597
[fl-altf6]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1599
[fl-shiftf5]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1626
[fl-f7]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1665
[fl-f8]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1706
[fl-shiftdown]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1916
[fl-ins]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1945
[fl-ctrlf3]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1961
[fl-ctrlf12]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L1988
[fl-ctrlpgup]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L2005
[fl-ctrlpgdn]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L2031
[fl-plainplus]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L2038
[fl-fastfind]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/filelist.cpp#L2077
[pn-fastfind]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/panel.cpp#L985
[pn-kbdlayout]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/panel.cpp#L962
[pn-paste]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/panel.cpp#L1043
[pn-ctrlshiftenter]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/panels/panel.cpp#L1089
[cl-key]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/cmdline.cpp#L1085
[cl-f10]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/cmdline.cpp#L1109
[cl-altf10]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/cmdline.cpp#L1112
[cl-altf11]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/cmdline.cpp#L1118
[cl-altf12]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/cmdline.cpp#L1122
[cl-altf8]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/cmdline.cpp#L1126
[cl-ctrle]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/cmdline.cpp#L1283
[ed-ctrly]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/edit.cpp#L1342
[ed-ctrlk]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/edit.cpp#L1354
[ed-ctrlend]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/edit.cpp#L1387
[mg-altins]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/manager.cpp#L954
[mg-f11]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/manager.cpp#L972
[mg-altf9]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/manager.cpp#L977
[mg-f12]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/manager.cpp#L1011
[mg-cas]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/manager.cpp#L1023
[f2-ttyout-init]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYOutput.cpp#L163
[f2-ttyout-kitty]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYOutput.cpp#L171
[f2-ttyout-fini]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYOutput.cpp#L192
[f2-ttyout-mouse]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYOutput.cpp#L550
[f2-ttyout-osc52]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYOutput.cpp#L576
[f2-parse-esc]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYInputSequenceParser.cpp#L330
[f2-parse-order]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYInputSequenceParser.cpp#L426
[f2-parse-kitty]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYInputSequenceParserExts.cpp#L114
[f2-parse-w32]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYInputSequenceParserExts.cpp#L425
[f2-parse-w32double]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYInputSequenceParserExts.cpp#L484
[f2-caps-probe]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYCaps.cpp#L195
[f2-caps-fini]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYCaps.cpp#L317
[f2-vt-decrqm]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtansi.cpp#L726
[f2-vt-mouse]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtansi.cpp#L739
[f2-vt-2004]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtansi.cpp#L764
[f2-vt-9001]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtansi.cpp#L768
[f2-vt-kitty]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtansi.cpp#L801
[f2-vt-dsr]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtansi.cpp#L1151
[f2-vt-da]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtansi.cpp#L1219
[f2-vt-apc]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtshell.cpp#L759
[f2-vt-osc52]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtshell.cpp#L848
[f2-vt-paste]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtshell.cpp#L956
[f2-vt-kittyenc]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtshell.cpp#L1005
[f2-vt-w32enc]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtshell.cpp#L1012
[f2-vt-reset]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/vtshell.cpp#L1328
[f2-ext-key]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/VTFar2lExtensios.cpp#L195
[f2-ext-interact]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt/VTFar2lExtensios.cpp#L651
[hlp-osc52]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L205
[vtx-3]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#3-implementations
[vtx-4-4]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#44-requests-request-ids-and-replies
[vtx-4-5]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#45-activation-and-detection
[vtx-5]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#5-requests-client-to-server
[vtx-5-7]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#57-fartty_interact_clipboard-c
[vtx-5-7-2]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#572-safe-reading
[vtx-6]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#6-events-server-to-client
[vtx-8-2]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#82-client-tty-backend
[vtx-8-3]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#83-server-far2l-built-in-terminal
[vtx-8-4]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#84-advice-to-implementers
[xt-bp]: https://invisible-island.net/xterm/ctlseqs/ctlseqs.html#h3-Bracketed-Paste-Mode
[xt-osc]: https://invisible-island.net/xterm/ctlseqs/ctlseqs.html#h3-Operating-System-Commands
[kitty-kb]: https://sw.kovidgoyal.net/kitty/keyboard-protocol/
[w32-spec]: https://github.com/microsoft/terminal/blob/main/doc/specs/%234999%20-%20Improved%20keyboard%20handling%20in%20Conpty.md
[hlp-paste]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L188
[hlp-remote]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L200
[hlp-terminals]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L221
[hlp-dnd]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/bootstrap/scripts/FarEng.hlf.m4#L1327
[f2-clip-client]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYFar2lClipboardBackend.cpp#L124
[f2-parse-lf]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY/TTYInputSequenceParser.cpp#L480
[f2-kb-append]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/console/keyboard.cpp#L86
[f2-kb-paste]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/console/keyboard.cpp#L835
[f2-ed-paste]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/editor.cpp#L3249
[f2-ed-autoindent]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/editor.cpp#L4552
[f2-edit-paste]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/edit.cpp#L1272
[f2-cl-multiline]: https://github.com/elfmz/far2l/blob/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/cmdline.cpp#L1359
[f2-tty-dir]: https://github.com/elfmz/far2l/tree/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/WinPort/src/Backend/TTY
[f2-vt-dir]: https://github.com/elfmz/far2l/tree/5ec2011794fa0c781e6dde92c84c77b5ae02b25b/far2l/src/vt
[f4-readme-paste]: ../README.md#performance-notes
[vtx-5-7-1]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#571-authorization
[vtx-5-7-3]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#573-writing
[vtx-5-7-6]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#576-formats
[vtx-9]: https://github.com/unxed/far2l/blob/8030902caf42498e1612e5a393ab7fc489048f51/VTExts.md#9-links-to-implementations
