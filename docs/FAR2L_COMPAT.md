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

A key requirement passes when the key, pressed in the named environment, does
what the *Action* column says. Every key requirement is checked in **E1 and E2**;
a key that cannot be encoded by a legacy terminal (marked ⌨ below) is not checked
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
the same characters as the main keyboard unless the application keypad mode is
on (P-09).

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
