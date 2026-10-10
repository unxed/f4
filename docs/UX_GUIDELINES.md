# The `vtui` Navigation & Interaction Philosophy

## Abstract

This document outlines the core principles of user experience (UX) and navigation that govern the `vtui` framework and its primary implementation, the `f4` file manager. Our philosophy is rooted in blending the speed and power of classic keyboard-driven interfaces like **Far Manager** with the structured, predictable component model of **Turbo Vision**.

The goal is to create an environment that is instantly familiar to veterans of TUI applications while remaining discoverable and consistent for new users. We achieve this by ensuring that every navigation key has a clear, context-dependent purpose, maximizing user efficiency and respecting "muscle memory".

### 0. The Golden Rule: One Action, Three Faces

Every interactive capability of `f4` (everything above `vtui` widget internals like `Checkbox.ProcessKey`) MUST exist in three equivalent forms, all driven by a single entry in the action registry (`action_registry.go`):

1. **A clickable menu or KeyBar item.** Menus (`F9`, available in the panels, the editor and the viewer) and KeyBar labels are generated from the registry, never hardcoded.
2. **A macro command.** The action's `Name` (e.g. `Editor.Save`) is callable from macros via `Actions.Run(...)`.
3. **A hotkey.** Default bindings ship with the action (`DefaultKeys`) and users can rebind them in the Hotkey Configurator.

The built-in help pages for key bindings are generated from the same registry, so documentation, menus, KeyBar and actual behavior can never drift apart.

### 0.1. Adding a New Action: Checklist

1. **Implement the handler next to its Area's owner**, as a method of the
   view that does the work (e.g. `(*ViewerView).showCodepageDialog`,
   `(*PanelsFrame).moveFolderHistory`). The registry entry only
   dispatches:
   ```go
   Handler: withViewer(func(vv *ViewerView) { vv.showCodepageDialog() })
   ```
   Keep `action_registry.go` thin — no business logic beyond a small
   glue closure. If the handler needs the frame, use the `withPF` /
   `withEditor` / `withViewer` adapters; they resolve the top frame of
   the matching type and silently do nothing when it is absent.
2. **Register the action** with every field that applies: `Name`
   (`Area.Verb` style), `Area`, `DefaultKeys` (Far-style, optional
   `:Condition` per key), `MenuPath` (empty = key-only action),
   `Checked` for toggles, `MenuSeparatorBefore` for menu grouping.
3. **Localize**: add `LabelKey`/`DescKey` entries to both `lang/en.lng`
   and `lang/ru.lng` (`Action.<Name>` and `Action.<Name>.Desc`).
   `Label`/`Description` remain the English fallbacks. Menus get a
   first-letter hotkey automatically unless the label carries its own
   `&` marker. New keys belong inside their namespace group, not at the
   end of the file; run `go run ./tools/langfmt -w internal/i18n/lang/*.lng`
   afterwards, because CI runs the same tool with `-check` and a block
   appended at the end fails it. See [I18N.md](I18N.md).
4. **Guard the shortcut and the menu entry, not the top frame.**
   `DefaultKeys` must not repeat a key another action already claims in
   the same `Area`: only one of them wins the binding and the other is
   left with no shortcut at all, silently
   (`TestDefaultKeysAreUniquePerArea` catches this). If the action needs
   a `Visible` predicate, do not write it against the top frame — an
   open dropdown is the top frame while the menu is being rebuilt, so
   such a predicate hides the item from the very menu the user is
   reading. Search the frame stack instead.
5. **Do not add a hardcoded key handler** in any `ProcessKey`. If the
   key must lose to a modal input state (fast find, autocomplete),
   extend that frame's `VetoActionKey` instead.
6. **Tests**: drive keys through the `pressKey` test helper (filter →
   ProcessKey), never through a bare `ProcessKey` call for action keys.
   Menus, KeyBar labels and the generated help topics pick the new
   action up for free — assert them instead of duplicating key tables.

### 1. The Four Tiers of Navigation

Every interactive screen in `vtui` adheres to a hierarchical navigation model. This ensures there is always a way to move focus, from global workspace management to specific character input.

#### Tier 0: Global Workspace Switching (`Ctrl+Tab` / `Ctrl+Shift+Tab`)

This is the highest level of navigation, allowing the user to jump between entire application states (Screens).

*   **Rule:** `Ctrl+Tab` cycles forward through active screens; `Ctrl+Shift+Tab` cycles backward.
*   **Visuals:** A switcher overlay appears in the center of the screen, showing titles and progress of all workspaces.
*   **Commit:** The switch is finalized only when the `Ctrl` key is released.

#### Tier 1: The Reliable Cycle (`Tab` / `Shift+Tab`)

This is the most fundamental and predictable way to navigate.

*   **Rule:** `Tab` always moves focus to the *next* focusable element in the logical order. `Shift+Tab` always moves to the *previous* one.
*   **Behavior:** This cycle is typically wrapped. Pressing `Tab` on the last element moves focus to the first, and `Shift+Tab` on the first moves to the last.
*   **Purpose:** Guarantees that a user can always reach any interactive element on the screen, regardless of its visual layout. It is the bedrock of accessibility and predictability.

#### Tier 2: The Contextual Jump (Arrow Keys)

Arrow keys provide fast, spatial navigation. Their behavior is highly dependent on the currently focused widget.

*   **Rule:** Arrow keys are primarily used for *internal navigation* within a component (e.g., moving up/down a list, left/right in a text field).
*   **Boundary Behavior:** Arrow keys transfer focus to a neighboring component **only** when the cursor is at the absolute boundary of the current component's data set.
    *   Pressing `Up` or `Left` on the very first item of a list, table, or group will exit focus to the previous element in the `Tab` cycle.
    *   Pressing `Down` or `Right` on the very last item will exit focus to the next element.
    *   In all other cases (e.g., pressing `Up` in the middle of a list), the event is "swallowed" by the component.
*   **Purpose:** This creates an intuitive flow. The user stays "inside" a widget while working with its data, but can seamlessly "flow" to the next widget by continuing to press the arrow key after reaching the end.

#### Tier 3: The Direct Shortcut (Hotkeys)

Hotkeys provide the fastest way to activate a specific function.

*   **Rule:** Hotkeys are typically activated with `Alt+<char>`. An ampersand (`&`) in a label (e.g., "`&Save`") defines the hotkey.
*   **Modeless Activation:** In dialogs where no text input field is focused, hotkeys may be activated directly by pressing the character key without `Alt`.
*   **Purpose:** To provide expert users with immediate access to any action on the screen, bypassing the `Tab` cycle entirely.

### 2. Component-Specific Interaction Patterns

#### Dialogs and Windows

*   `Enter`: Triggers the "default" action. This is either the button marked as `IsDefault`, or the first actionable button in the tab order if none is marked. This applies even if an `Edit` field is focused.
*   `Esc`: Closes the window or dialog.
*   `F1`: Opens the help topic associated with the currently focused element.
*   **Mouse:** Click-and-drag on the top border moves the window. Click-and-drag on the bottom-right corner resizes it.

Select Group, Deselect Group and New File share a compact six-row input dialog:
prompt, input, full-width separator and centered buttons occupy consecutive
rows inside the frame. The minimum width is 30 cells. Screen resizing adjusts
the width; corner dragging changes only the width and preserves the height.
New File uses the same width calculation on opening and screen resizing:
`min(screenWidth, max(30, screenWidth/2))`. It keeps its empty initial input,
path hints and `NewEdit` history. Select/Deselect Group open 40 cells wide.

#### Groups (`RadioGroup`, `CheckGroup`)

*   **Interaction Model:** These components separate the concepts of *cursor* and *selection*.
    *   **Arrow Keys** move the internal cursor/focus within the group. The selection itself does not change.
    *   `Space` or `Enter` toggles the state (checked/unchecked, selected radio button) of the item under the cursor.
*   **Snake Navigation:** In multi-column layouts, navigation is two-dimensional and wraps intuitively:
    *   Pressing `Right` at the end of a row moves the cursor to the beginning of the next row.
    *   Pressing `Down` at the bottom of a column moves the cursor to the top of the next column. `Up` at the top of a column moves to the bottom of the previous.
*   **Rationale:** This model is vastly more efficient for keyboard users than the "arrows change selection" model, as it allows rapid navigation across many options without triggering an action on every key press.

#### Lists (`Table`, `ListBox`)

*   **Navigation:** `Up`/`Down`, `PgUp`/`PgDn`, `Home`/`End` provide standard list navigation.
*   **Boundary Behavior:** As per the core principle, `Up` on the first item and `Down` on the last item will pass focus to the previous/next widget in the dialog.
*   **Action:** `Enter` or `Double-Click` triggers the primary action for the selected item (e.g., opening a file, confirming a choice).
*   **`ListBox` as a `Table`:** `ListBox` is implemented as a single-column `Table` and inherits all its navigation behaviors, ensuring consistency.

#### Text Input (`Edit`)

*   **Navigation:**
    *   `Left`/`Right`: Move the cursor by one character.
    *   `Ctrl+Left`/`Ctrl+Right`: Jump to the beginning of the previous/next word.
    *   `Home`/`End`: Jump to the beginning/end of the line.
*   **Selection:** Holding `Shift` while using any navigation key creates or expands a selection. `Ctrl+C` and `Ctrl+Ins` copy to the system clipboard.
*   **Auto-Clear Logic:** Fields that are opened with a default "unchanged" value (grayed out) will automatically clear their entire content the moment the user starts typing, unless a navigation key is pressed first.

#### Dropdowns (`ComboBox`)

*   **Interaction:** Combines an `Edit` field with a hidden `VMenu`.
*   **Activation:** `Ctrl+Down` or clicking the down arrow (`↓`) icon opens the list.
*   **Selection:** Selecting an item from the list automatically populates the `Edit` field and returns focus to it.
*   **`DropdownOnly` Mode:** If enabled, the user cannot type custom text and must select from the provided options. As in `far2l`, `Enter` presses the dialog's default button; the list opens with `Ctrl+Down` or the mouse. Only when the dialog has no default button does `Enter` open the list.

#### File Panels (`f4` Specific)

File panels are a special, highly optimized version of a `Table`.

*   `Up`/`Down`, `PgUp`/`PgDn`: Navigate vertically within the current column.
*   `Left`/`Right`: Jump one full page (view height) up or down within the *current column*. If at the top/bottom, jump to the top/bottom of the adjacent column. **These keys do not change the active panel.**
*   `Enter`: Enters a directory or executes a file.
*   `Ctrl+Enter`: Inserts the selected filename into the command line. While command-line suggestions are open, it accepts the selected suggestion (or the first match) without executing the command.
*   `Alt+F7`: When directories are explicitly marked, Find File shows "In selected folders", checked by default. It searches those directory trees; unchecking it searches the current directory tree. Marked files and the panel cursor alone do not enable the option.

#### Menus (`MenuBar`, `VMenu`)

*   **`MenuBar` (Top-level):**
    *   Activated by `F9` or `Alt+<char>`.
    *   When active, `Left`/`Right` cycles through the main menu items (`File`, `Edit`, etc.), automatically opening their respective submenus.
    *   `Down` or `Enter` opens the submenu for the currently selected item.
    *   **Multi-level `Esc`:** `Esc` closes an open submenu but keeps the `MenuBar` active. A second `Esc` deactivates the `MenuBar` entirely.
*   **`VMenu` (Vertical/Submenu):**
    *   **As a Submenu:** If opened from a `MenuBar`, `Left`/`Right` closes the current submenu and opens the adjacent one from the `MenuBar`. `Up` on the first item or `Down` on the last item wraps around within the `VMenu` to provide fast circular access.
    *   **As a Standalone Dialog:** If opened as a context menu (not tied to a `MenuBar`), its boundary behavior follows the standard `Tier 2` rule: `Up` on the first item or `Down` on the last will pass focus to the previous/next element in the parent dialog.
*   **Rationale:** This dual behavior makes menus feel integrated when part of a larger structure, but behave like any other standard list widget when used for context-specific actions.

### 3. Mouse Interaction Principles

While `vtui` is keyboard-first, mouse interaction is designed to be consistent and predictable.

*   **Left Click:** Focuses and/or activates an element.
*   **Double-Click:** The primary "action" command, equivalent to `Enter`.
*   **Right Click (Contextual):** In specific components like file panels, right-click can be used for secondary actions like multi-selection.
*   **Wheel:** Scrolls the component under the cursor, regardless of focus.

### 4. Live File Search (Alt+F7)

Starting a search expands the parameters dialog immediately. The submitted mask, text and checkboxes stay at the top, frozen for the duration of the search; progress, results and result actions appear below them. Find and Cancel are replaced by the result actions. Hits appear in discovery order without resetting the selected row, focus or scroll position. The window displays the current path, counts and approximate progress when the provider knows the number of root subdirectories. The list uses the remaining space on an 80×25 terminal and grows when the window is resized or maximized. Searches started without a parameters dialog show a request summary instead.

Pause, beside the progress percentage, suspends the search at its next progress/result checkpoint and becomes Resume. The title and current-path label reflect the paused state. Resume continues the same search and retains its results and selection. The button disappears after completion. Remote background jobs can continue on the server while the client is paused. The configurable `FindFile.Stop` action (default `Ctrl+Shift+F7`) still cancels the search and keeps its results open. Esc or Close cancels and closes it, including while paused. View (F3) and Edit (F4) remain available while searching or paused; Go to and Panel (F6) close and cancel, using the currently displayed results. F5 maximizes or restores the dialog. File actions are disabled while the list is empty. Completion, no matches and errors are shown in the same expanded dialog; errors retain partial results. Search history is saved when Find is pressed.

Completed duplicate-search results use the same table without live search controls.
By adhering to these rules, we aim to build TUI applications that are powerful, efficient, and a pleasure to use for both novice and expert users.
### Panel group headings

Group by adds decorative `──── Title ────` rows with a centered title in the
column-title color and lines in the panel column-separator color. The file
model contains only files: cursor, marks and file operations never see headings.
Keyboard movement skips them, and mouse clicks (including double and middle
clicks) cannot focus or activate them. When the first visible group's heading
scrolls out, it stays pinned at the start of the list until that group's last
file scrolls out. The pinned row reserves a cell without obscuring files or the
cursor. A one-row viewport prioritizes the file and does not pin a heading.
Medium/Brief flow through file columns without repeating a heading at column
boundaries. Detailed/Wide headings span the list.
