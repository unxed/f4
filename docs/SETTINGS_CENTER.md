# Settings Center: implementation inventory

Baseline: `main` at `4cd62a34`. Settings are grouped by the behavior they control. The canonical runtime descriptions live in `sdk/f4settings` descriptors contributed by core and bundled providers. English UI resources use `SettingsCenter.*`; plugins may supply localized text with an English fallback.

The dialog opens centered at approximately half the screen width (minimum 72 columns, bounded by the screen), supports native dragging, resizing and F5 maximize/restore, and uses column separators and bordered setting groups. Boolean settings use directly labeled checkboxes; long labels wrap beside the checkbox. The selected category remains marked with the neutral gray background derived from the live dialog palette, matching Environment Manager when navigation loses focus.

Up/Down navigation stays within its active pane; content controls wrap at the ends. Right moves from categories to content; Left returns to categories when the control does not consume it for editing. Tab and Shift+Tab move through search, categories, content and the footer buttons. Up from the first category reaches search, and Down from search reaches categories. Category titles display only localized text; English names remain search aliases.

Numeric fields use compact inline inputs. Providers can set the optional InputWidth character-width hint for short text values such as ports; it does not restrict stored value length. Long-text fields, including filename masks, paths and commands, retain full-width editors beneath their captions.

Russian labels, descriptions, choices, group names and operation messages are provided in `cmd/f4/lang/ru.lng`. Unkeyed provider text can resolve through `f4settings.ResourceKey`; provider-owned translations take precedence. Literal user values and external identifiers bypass translation. Formatted descriptions preserve arguments such as profile paths, and provider errors retain their English diagnostics and error chains while exposing localized display text. Russian coverage tests exercise core and all bundled providers.

## Entry points and editing contract

`Settings.Open` is the single menu entry in every application menu context. Legacy settings action IDs are hidden from menus and remain category deep links. `vfs.SettingsNavigationHost` optionally targets a collection and record for contextual editing. File encoding for the current document, connection opening, user-menu execution, history navigation, and operation dialogs remain contextual operations.

All preference edits are drafts across page changes. Apply validates changed providers before committing. Successful fields or records become the new Cancel baseline; failed edits remain pending. Theme Cancel restores the palette snapshot. Commands operate explicitly on applied configuration. Authentication stages credentials for Apply.

Search matches all tokens across labels, descriptions, category/group names, choices and aliases. Record display names are searchable; credentials, arbitrary field values and command bodies are not. Nothing is filtered, moved, hidden, or disabled by search.

The layout keeps search, categories and actions fixed. At 110 columns or wider the explanation pane is on the right; narrower screens place it below the independently scrolling page.

## Core scalar controls

The original-dialog column links to the original implementation and its backing control, including duplicated access routes. Storage keys continue using existing INI formats. Consumer links identify implementation rather than help prose.

| Canonical setting | Category / visible group | Original dialog/control implementation | Persistence | Consuming code | Description / timing |
|---|---|---|---|---|---|
| Language | appearance / Language | [actionAppearanceSettings](../cmd/f4/actions.go#L4604), [actionAppearanceSettings](../cmd/f4/actions.go#L4606), [actionAppearanceSettings](../cmd/f4/actions.go#L4811) | settings.ini / Interface / Language | [actions.go](../cmd/f4/actions.go#L4604), [actions.go](../cmd/f4/actions.go#L4606), [actions.go](../cmd/f4/actions.go#L4811) | Interface language: Choose the language of the interface. Applying reloads resources and rebuilds Settings Center captions. Takes effect: live. |
| HelpLanguage | appearance / Language | [actionLanguage](../cmd/f4/actions.go#L5275), [actionLanguage](../cmd/f4/actions.go#L5336), [actionLanguage](../cmd/f4/actions.go#L5337) | settings.ini / Interface / HelpLanguage | [actions.go](../cmd/f4/actions.go#L5275), [actions.go](../cmd/f4/actions.go#L5336), [actions.go](../cmd/f4/actions.go#L5337) | Help language: Choose the built-in help language independently of the interface language. Takes effect: live. |
| UseLocalLanguageFiles | appearance / Language | [actionLanguage](../cmd/f4/actions.go#L5285), [actionLanguage](../cmd/f4/actions.go#L5326), [actionLanguage](../cmd/f4/actions.go#L5342) | settings.ini / Interface / UseLocalLanguageFiles | [actions.go](../cmd/f4/actions.go#L5285), [actions.go](../cmd/f4/actions.go#L5326), [actions.go](../cmd/f4/actions.go#L5342) | Use local translations: Allow local language files to override bundled interface and help translations. Takes effect: live. |
| ColorStyle | appearance / Colors | [actionAppearanceSettings](../cmd/f4/actions.go#L4582), [actionAppearanceSettings](../cmd/f4/actions.go#L4808) | settings.ini / Interface / ColorStyle | [actions.go](../cmd/f4/actions.go#L4582), [actions.go](../cmd/f4/actions.go#L4808), [colors.go](../cmd/f4/colors.go#L368) | Color theme: Select the application palette. Preview is reversible until Apply. Existing custom palette overrides are preserved on Cancel. Takes effect: preview. |
| EnforceColorCorrection | appearance / Colors | [actionAppearanceSettings](../cmd/f4/actions.go#L4698), [actionAppearanceSettings](../cmd/f4/actions.go#L4824) | settings.ini / Dialogs / EnforceColorCorrection | [actions.go](../cmd/f4/actions.go#L4698), [actions.go](../cmd/f4/actions.go#L4824), [colors.go](../cmd/f4/colors.go#L442) | Correct low contrast: Adjust insufficient foreground and background contrast in supported palette and file-highlight colors. Box slots are excluded from the global correction pass. Takes effect: live. |
| GuiUseSystemMonospace | appearance / Font | [actionAppearanceSettings](../cmd/f4/actions.go#L4611), [actionAppearanceSettings](../cmd/f4/actions.go#L4812), [actionAppearanceSettings](../cmd/f4/actions.go#L4816) | settings.ini / Appearance / GuiUseSystemMonospace | [actions.go](../cmd/f4/actions.go#L4611), [actions.go](../cmd/f4/actions.go#L4812), [actions.go](../cmd/f4/actions.go#L4816) | Use system monospace font: Use the platform monospace font on supported Windows and macOS graphical frontends instead of the custom font. Takes effect: restart. |
| GuiFont | appearance / Font | [actionAppearanceSettings](../cmd/f4/actions.go#L4604), [actionAppearanceSettings](../cmd/f4/actions.go#L4606), [actionAppearanceSettings](../cmd/f4/actions.go#L4811) | settings.ini / Appearance / GuiFont | [actions.go](../cmd/f4/actions.go#L4604), [actions.go](../cmd/f4/actions.go#L4606), [actions.go](../cmd/f4/actions.go#L4811) | Graphical font: Choose a font for supported graphical frontends. A custom font can be entered manually. Takes effect: restart. |
| GuiFontSize | appearance / Font | [actionAppearanceSettings](../cmd/f4/actions.go#L4622), [actionAppearanceSettings](../cmd/f4/actions.go#L4812), [actionAppearanceSettings](../cmd/f4/actions.go#L4818) | settings.ini / Appearance / GuiFontSize | [actions.go](../cmd/f4/actions.go#L4622), [actions.go](../cmd/f4/actions.go#L4812), [actions.go](../cmd/f4/actions.go#L4818) | Font size: Set graphical font size. This does not change the font of an external terminal emulator. Takes effect: restart. |
| AlwaysShowMenuBar | appearance / Titles and menus | [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3887), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4017) | settings.ini / Interface / AlwaysShowMenuBar | [actions.go](../cmd/f4/actions.go#L3887), [actions.go](../cmd/f4/actions.go#L4017), [panels_frame.go](../cmd/f4/panels_frame.go#L1390) | Always show menu bar: Keep the main menu bar visible instead of showing it only when activated. Takes effect: live. |
| ConsoleTitleTemplate | appearance / Titles and menus | [actionAppearanceSettings](../cmd/f4/actions.go#L4626), [actionAppearanceSettings](../cmd/f4/actions.go#L4814) | settings.ini / Interface / ConsoleTitleTemplate | [actions.go](../cmd/f4/actions.go#L4626), [actions.go](../cmd/f4/actions.go#L4814), [title.go](../cmd/f4/title.go#L228) | Window title template: Format the host title with %State, %Ver, %Platform, %Backend, %Host, %User and %Admin. Takes effect: live. |
| DisplayFullPathInTitle | appearance / Titles and menus | [actionAppearanceSettings](../cmd/f4/actions.go#L4629), [actionAppearanceSettings](../cmd/f4/actions.go#L4815) | settings.ini / Interface / DisplayFullPathInTitle | [actions.go](../cmd/f4/actions.go#L4629), [actions.go](../cmd/f4/actions.go#L4815), [file_title.go](../cmd/f4/file_title.go#L17) | Full file paths in titles: Show the complete file identity in editor and viewer title bars instead of only the basename. Takes effect: live. |
| StartupMode | startup / Launch defaults | [actionStartupSettings](../cmd/f4/startup_settings.go#L39), [actionStartupSettings](../cmd/f4/startup_settings.go#L107) | settings.ini / Startup / Mode | [main.go](../cmd/f4/main.go#L531), [main.go](../cmd/f4/main.go#L542), [startup_settings.go](../cmd/f4/startup_settings.go#L39) | Startup mode: Choose how plain f4 launches. Explicit --gui or --tty arguments override this default for that launch. Takes effect: restart. Choices: auto:Automatic;tty:Terminal;gui:Graphical |
| GuiBackend | startup / Launch defaults | [actionStartupSettings](../cmd/f4/startup_settings.go#L48), [actionStartupSettings](../cmd/f4/startup_settings.go#L108) | settings.ini / Startup / GuiBackend | [main.go](../cmd/f4/main.go#L538), [main.go](../cmd/f4/main.go#L539), [startup_settings.go](../cmd/f4/startup_settings.go#L48) | Graphical renderer: Default graphical renderer. Automatic uses startup detection. Command-line choices override this value; unknown external renderer IDs are preserved. Takes effect: restart. |
| TTYBackend | startup / Launch defaults | [actionStartupSettings](../cmd/f4/startup_settings.go#L57), [actionStartupSettings](../cmd/f4/startup_settings.go#L109) | settings.ini / Startup / TTYBackend | [main.go](../cmd/f4/main.go#L540), [startup_settings.go](../cmd/f4/startup_settings.go#L57), [startup_settings.go](../cmd/f4/startup_settings.go#L109) | Terminal renderer: Default terminal renderer. Command-line renderer choices override this preference. Takes effect: restart. Choices: :Automatic;ansi:ANSI;winapi:Windows console |
| WorkspaceTabMode | workspaces / Tab presentation | [actionAppearanceSettings](../cmd/f4/actions.go#L4641), [actionAppearanceSettings](../cmd/f4/actions.go#L4825), [actionAppearanceSettings](../cmd/f4/actions.go#L4841) | settings.ini / Interface / WorkspaceTabMode | [actions.go](../cmd/f4/actions.go#L4641), [actions.go](../cmd/f4/actions.go#L4825), [actions.go](../cmd/f4/actions.go#L4841) | Show workspace tabs: Show tabs always, only with multiple workspaces, while Ctrl is held, or never. Takes effect: live. Choices: 0:Always;1:Multiple workspaces;2:While Ctrl is held;3:Never |
| WorkspaceTabsOverlay | workspaces / Tab presentation | [actionAppearanceSettings](../cmd/f4/actions.go#L4648), [actionAppearanceSettings](../cmd/f4/actions.go#L4649), [actionAppearanceSettings](../cmd/f4/actions.go#L4826) | settings.ini / Interface / WorkspaceTabsOverlay | [actions.go](../cmd/f4/actions.go#L4648), [actions.go](../cmd/f4/actions.go#L4649), [actions.go](../cmd/f4/actions.go#L4826) | Overlay workspace tabs: Draw workspace tabs over the content instead of reserving a layout row. Takes effect: live. |
| WorkspaceTabNumbering | workspaces / Tab presentation | [actionAppearanceSettings](../cmd/f4/actions.go#L4682), [actionAppearanceSettings](../cmd/f4/actions.go#L4830), [actionAppearanceSettings](../cmd/f4/actions.go#L4831) | settings.ini / Interface / WorkspaceTabNumbering | [actions.go](../cmd/f4/actions.go#L4682), [actions.go](../cmd/f4/actions.go#L4830), [actions.go](../cmd/f4/actions.go#L4831) | Workspace numbering: Keep numbers permanently, keep them during this session, or renumber to match the current tab order. Takes effect: live. Choices: 0:Permanent;1:Session;2:Current order |
| CtrlTabShowsMenu | workspaces / Switching | [actionAppearanceSettings](../cmd/f4/actions.go#L4660), [actionAppearanceSettings](../cmd/f4/actions.go#L4827), [actionAppearanceSettings](../cmd/f4/actions.go#L4838) | settings.ini / Interface / CtrlTabShowsMenu | [actions.go](../cmd/f4/actions.go#L4660), [actions.go](../cmd/f4/actions.go#L4827), [actions.go](../cmd/f4/actions.go#L4838) | Ctrl+Tab opens chooser: Open the workspace chooser with Ctrl+Tab instead of switching directly. Takes effect: live. |
| AltNumberSwitchesTabs | workspaces / Switching | [actionAppearanceSettings](../cmd/f4/actions.go#L4667), [actionAppearanceSettings](../cmd/f4/actions.go#L4668), [actionAppearanceSettings](../cmd/f4/actions.go#L4828) | settings.ini / Interface / AltNumberSwitchesTabs | [actions.go](../cmd/f4/actions.go#L4667), [actions.go](../cmd/f4/actions.go#L4668), [actions.go](../cmd/f4/actions.go#L4828) | Alt+number switches tabs: Reserve Alt+number shortcuts for workspace selection. Takes effect: live. |
| RestoreWorkspaceTabs | workspaces / Restoration | [actionAppearanceSettings](../cmd/f4/actions.go#L4671), [actionAppearanceSettings](../cmd/f4/actions.go#L4672), [actionAppearanceSettings](../cmd/f4/actions.go#L4829) | settings.ini / Interface / RestoreWorkspaceTabs | [actions.go](../cmd/f4/actions.go#L4671), [actions.go](../cmd/f4/actions.go#L4672), [actions.go](../cmd/f4/actions.go#L4829) | Restore workspace tabs: Recreate saved workspaces at startup instead of restoring only the initial workspace. Takes effect: restart. |
| SavePanelPaths | workspaces / Restoration | [actionPanelSettings](../cmd/f4/actions.go#L3733), [actionPanelSettings](../cmd/f4/actions.go#L3841) | settings.ini / Panel / SavePanelPaths | [actions.go](../cmd/f4/actions.go#L3733), [actions.go](../cmd/f4/actions.go#L3841), [main.go](../cmd/f4/main.go#L842) | Save and restore panel paths: Save panel folders and focused files. Turning this off omits these values from subsequently saved workspace sessions. Takes effect: next session save. |
| AutoSaveDialogSettings | workspaces / Automatic saving | [actionAutoSaveSettings](../cmd/f4/actions.go#L3642), [actionAutoSaveSettings](../cmd/f4/actions.go#L3670), [actionPanelSettings](../cmd/f4/actions.go#L3843) | settings.ini / System / AutoSaveDialogSettings | [actions.go](../cmd/f4/actions.go#L3642), [actions.go](../cmd/f4/actions.go#L3670), [actions.go](../cmd/f4/actions.go#L3843) | Save preferences automatically: Automatically persist configuration and dialog-related changes. Explicit Apply still saves when this is off. Takes effect: live. |
| AutoSavePanelSettings | workspaces / Automatic saving | [actionAutoSaveSettings](../cmd/f4/actions.go#L3643), [actionAutoSaveSettings](../cmd/f4/actions.go#L3671), [actionPanelSettings](../cmd/f4/actions.go#L3844) | settings.ini / System / AutoSavePanelSettings | [actions.go](../cmd/f4/actions.go#L3643), [actions.go](../cmd/f4/actions.go#L3671), [actions.go](../cmd/f4/actions.go#L3844) | Save panel layout automatically: Remember panel view modes, sorting, visibility, active workspace and workspace layout. Takes effect: live. |
| AutoSaveCurrentPanel | workspaces / Automatic saving | [actionAutoSaveSettings](../cmd/f4/actions.go#L3644), [actionAutoSaveSettings](../cmd/f4/actions.go#L3672), [actionPanelSettings](../cmd/f4/actions.go#L3845) | settings.ini / System / AutoSaveCurrentPanel | [actions.go](../cmd/f4/actions.go#L3644), [actions.go](../cmd/f4/actions.go#L3672), [actions.go](../cmd/f4/actions.go#L3845) | Save panel locations automatically: Remember locations and focused files for both sides of saved workspaces. Save and restore panel paths must also be enabled. Takes effect: live. |
| AutoSaveGUIWindow | workspaces / Automatic saving | [actionAutoSaveSettings](../cmd/f4/actions.go#L3645), [actionAutoSaveSettings](../cmd/f4/actions.go#L3673), [actionPanelSettings](../cmd/f4/actions.go#L3846) | settings.ini / System / AutoSaveGUIWindow | [actions.go](../cmd/f4/actions.go#L3645), [actions.go](../cmd/f4/actions.go#L3673), [actions.go](../cmd/f4/actions.go#L3846) | Save graphical window automatically: Remember supported graphical-window dimensions and position, not external terminal geometry. Takes effect: live. |
| ShowHiddenFiles | panels / File listing | [actionPanelSettings](../cmd/f4/actions.go#L3695), [actionPanelSettings](../cmd/f4/actions.go#L3835) | settings.ini / Panel / ShowHiddenFiles | [actions.go](../cmd/f4/actions.go#L3695), [actions.go](../cmd/f4/actions.go#L3835), [action_registry.go](../cmd/f4/action_registry.go#L1918) | Show hidden files: Include hidden files and folders in the listing. The parent-directory entry remains visible. Takes effect: live. |
| ShowDirPrefix | panels / File listing | [actionPanelSettings](../cmd/f4/actions.go#L3701), [actionPanelSettings](../cmd/f4/actions.go#L3836) | settings.ini / Panel / ShowDirPrefix | [actions.go](../cmd/f4/actions.go#L3701), [actions.go](../cmd/f4/actions.go#L3836), [file_panel.go](../cmd/f4/file_panel.go#L143) | Prefix folder names: Prefix folder names with a slash, unless a highlight rule already supplies one. Takes effect: live. |
| ShowHighlightMarks | panels / File listing | [actionPanelSettings](../cmd/f4/actions.go#L3707), [actionPanelSettings](../cmd/f4/actions.go#L3837) | settings.ini / Panel / ShowHighlightMarks | [actions.go](../cmd/f4/actions.go#L3707), [actions.go](../cmd/f4/actions.go#L3837), [file_panel.go](../cmd/f4/file_panel.go#L135) | Show highlight marks: Show markers from matching file-highlight rules. Also affects path suggestions; symlinks retain their fallback arrow. Takes effect: live. |
| SeparateFileExtensions | panels / File listing | [actionPanelSettings](../cmd/f4/actions.go#L3712), [actionPanelSettings](../cmd/f4/actions.go#L3838) | settings.ini / Panel / SeparateFileExtensions | [actions.go](../cmd/f4/actions.go#L3712), [actions.go](../cmd/f4/actions.go#L3838), [file_panel.go](../cmd/f4/file_panel.go#L169) | Separate filename extensions: Align the final extension separately in the name column. Excludes folders, extensionless names and leading dots alone. Takes effect: live. |
| ShowPanelFileInfo | panels / File listing | [actionPanelSettings](../cmd/f4/actions.go#L3716), [actionPanelSettings](../cmd/f4/actions.go#L3839) | settings.ini / Panel / ShowPanelFileInfo | [actions.go](../cmd/f4/actions.go#L3716), [actions.go](../cmd/f4/actions.go#L3839), [file_panel.go](../cmd/f4/file_panel.go#L2702) | Focused-file status row: Reserve a bottom row for the focused name, size and modification time. Short panels suppress this row. Takes effect: live. |
| PanelScrollbarMode | panels / File listing | [actionPanelSettings](../cmd/f4/actions.go#L3727), [actionPanelSettings](../cmd/f4/actions.go#L3728), [actionPanelSettings](../cmd/f4/actions.go#L3840) | settings.ini / Panel / PanelScrollbarMode | [actions.go](../cmd/f4/actions.go#L3727), [actions.go](../cmd/f4/actions.go#L3728), [actions.go](../cmd/f4/actions.go#L3840) | Panel scrollbar: Hide the scrollbar, show a minimal one, or show the full scrollbar with arrows. Takes effect: live. Choices: 0:Off;1:Minimal;2:Full |
| SyncPanelLoad | panels / Directory loading | [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3880), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4015) | settings.ini / Panel / SyncPanelLoad | [actions.go](../cmd/f4/actions.go#L3880), [actions.go](../cmd/f4/actions.go#L4015), [file_panel.go](../cmd/f4/file_panel.go#L736) | Wait for complete directory listing: Replace the listing only when all directory results are ready and bypass cached previews. Off permits incremental results. It does not block all UI work. Takes effect: next directory load. |
| InfoPanelCPUGPU | panels / Information panels | [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3891), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4018) | settings.ini / Panel / InfoPanelCPUGPU | [actions.go](../cmd/f4/actions.go#L3891), [actions.go](../cmd/f4/actions.go#L4018), [info_panel.go](../cmd/f4/info_panel.go#L843) | Show CPU and GPU information: Include locally collected CPU and GPU sections when the information provider does not supply authoritative information. Takes effect: live. |
| InfoPanelBytes | panels / Information panels | Core setting bound by its canonical backing value | settings.ini / Panel / InfoPanelBytes | [action_registry.go](../cmd/f4/action_registry.go#L1905), [info_panel.go](../cmd/f4/info_panel.go#L1116) | Show sizes in bytes: Display raw bytes instead of human-readable sizes in information and quick-view panels. Takes effect: live. |
| NavigationMode | panels / Typing and focus | [actionPanelSettings](../cmd/f4/actions.go#L3759), [actionPanelSettings](../cmd/f4/actions.go#L3769), [actionPanelSettings](../cmd/f4/actions.go#L3857) | settings.ini / Panel / NavigationMode | [actions.go](../cmd/f4/actions.go#L3759), [actions.go](../cmd/f4/actions.go#L3769), [actions.go](../cmd/f4/actions.go#L3857) | Panel navigation: Classic types into the command line. Vim adds j/k and double dd/cc/mm actions. Search-first separates filename-search and command focus. Takes effect: live. Choices: 0:Classic;1:Vim;2:Search first |
| SearchCommandStayFocused | panels / Typing and focus | [actionPanelSettings](../cmd/f4/actions.go#L3766), [actionPanelSettings](../cmd/f4/actions.go#L3858) | settings.ini / Panel / SearchCommandStayFocused | [actions.go](../cmd/f4/actions.go#L3766), [actions.go](../cmd/f4/actions.go#L3858), [panels_frame.go](../cmd/f4/panels_frame.go#L2418) | Keep command input focused: In Search-first mode, keep command entry focused after executing a command rather than returning to the panel. Takes effect: live. |
| CommandLineAutoComplete | terminal / Path suggestions | [actionPanelSettings](../cmd/f4/actions.go#L3749), [actionPanelSettings](../cmd/f4/actions.go#L3855), [actionPanelSettings](../cmd/f4/actions.go#L3856) | settings.ini / Panel / CommandLineAutoComplete | [actions.go](../cmd/f4/actions.go#L3749), [actions.go](../cmd/f4/actions.go#L3855), [actions.go](../cmd/f4/actions.go#L3856) | Enable filesystem suggestions: Enable filesystem path suggestions in the command line and path-enabled dialog fields. Takes effect: live. |
| DialogAutoComplete | terminal / Path suggestions | [actionPathHintSettings](../cmd/f4/actions.go#L4265), [actionPathHintSettings](../cmd/f4/actions.go#L4339) | settings.ini / PathHints / DialogAutoComplete | [actions.go](../cmd/f4/actions.go#L4265), [actions.go](../cmd/f4/actions.go#L4339), [path_hints.go](../cmd/f4/path_hints.go#L27) | Show dialog completion dropdowns: Show completion while typing in fields with history or path suggestions. Does not add sources to arbitrary fields. Takes effect: live. |
| PathHintFullPath | terminal / Path suggestions | [actionPathHintSettings](../cmd/f4/actions.go#L4240), [actionPathHintSettings](../cmd/f4/actions.go#L4324) | settings.ini / PathHints / FullPath | [actions.go](../cmd/f4/actions.go#L4240), [actions.go](../cmd/f4/actions.go#L4324), [path_hints.go](../cmd/f4/path_hints.go#L253) | Show full suggestion paths: Display complete paths rather than only the final filename or folder component. Takes effect: live. |
| PathHintSource | terminal / Path suggestions | [actionPathHintSettings](../cmd/f4/actions.go#L4247), [actionPathHintSettings](../cmd/f4/actions.go#L4248), [actionPathHintSettings](../cmd/f4/actions.go#L4249) | settings.ini / PathHints / Source | [actions.go](../cmd/f4/actions.go#L4247), [actions.go](../cmd/f4/actions.go#L4248), [actions.go](../cmd/f4/actions.go#L4249) | Suggestion source: Resolve suggestions from the active panel, passive panel, or both. Both searches active first. Takes effect: live. Choices: 0:Active panel;1:Passive panel;2:Both panels |
| PathHintTimeout | terminal / Path suggestions | [actionPathHintSettings](../cmd/f4/actions.go#L4253), [actionPathHintSettings](../cmd/f4/actions.go#L4331) | settings.ini / PathHints / Timeout | [actions.go](../cmd/f4/actions.go#L4253), [actions.go](../cmd/f4/actions.go#L4331), [path_hints.go](../cmd/f4/path_hints.go#L170) | Directory read timeout (seconds): Maximum time allowed for a filesystem directory read behind suggestions. This is not a delay before suggestions appear. Minimum 1 second. Takes effect: live. |
| PathHintMaxVisible | terminal / Path suggestions | [actionPathHintSettings](../cmd/f4/actions.go#L4256), [actionPathHintSettings](../cmd/f4/actions.go#L4337) | settings.ini / PathHints / MaxVisible | [actions.go](../cmd/f4/actions.go#L4256), [actions.go](../cmd/f4/actions.go#L4337), [path_hints.go](../cmd/f4/path_hints.go#L22) | Maximum visible suggestions: Maximum number of visible suggestion rows. Minimum 1. Takes effect: live. |
| PathHintPerCategory | terminal / Path suggestions | [actionPathHintSettings](../cmd/f4/actions.go#L4260), [actionPathHintSettings](../cmd/f4/actions.go#L4338) | settings.ini / PathHints / PerCategory | [actions.go](../cmd/f4/actions.go#L4260), [actions.go](../cmd/f4/actions.go#L4338), [path_hints.go](../cmd/f4/path_hints.go#L23) | Separate suggestion limits: Apply the visible-row limit separately to active-panel, passive-panel and history suggestions. Takes effect: live. |
| UseTrash | operations / Deletion | [actionPanelSettings](../cmd/f4/actions.go#L3744), [actionPanelSettings](../cmd/f4/actions.go#L3854) | settings.ini / System / UseTrash | [actions.go](../cmd/f4/actions.go#L3070), [actions.go](../cmd/f4/actions.go#L3744), [actions.go](../cmd/f4/actions.go#L3854) | Use trash or recycle bin: Send ordinary Delete operations to trash where supported. Explicit permanent-delete commands still delete permanently. Takes effect: new operations. |
| DefaultFileOpMode | operations / Execution | [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3935), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3936), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4028) | settings.ini / Panel / DefaultFileOpMode | [actions.go](../cmd/f4/actions.go#L2405), [actions.go](../cmd/f4/actions.go#L2410), [actions.go](../cmd/f4/actions.go#L2428) | Default operation mode: Start operations in Queue, Background or Foreground mode. Individual operation dialogs can override it. Takes effect: new operations. Choices: 0:Queue;1:Background;2:Foreground |
| FileOpPathDisplay | operations / Execution | [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3942), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3943), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4029) | settings.ini / Panel / FileOpPathDisplay | [actions.go](../cmd/f4/actions.go#L3942), [actions.go](../cmd/f4/actions.go#L3943), [actions.go](../cmd/f4/actions.go#L4029) | Progress path display: Show the current name, full path, or source and destination paths in operation progress. Takes effect: live. Choices: 0:Name;1:Full path;2:Source and destination |
| ApplyCommandParallelism | operations / Execution | [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3883), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4016), [showApplyCommandDialog](../cmd/f4/apply_command.go#L306) | settings.ini / Panel / ApplyCommandParallelism | [actions.go](../cmd/f4/actions.go#L3883), [actions.go](../cmd/f4/actions.go#L4016), [apply_command.go](../cmd/f4/apply_command.go#L306) | Concurrent Apply commands: Maximum concurrent commands in Apply command. Zero means unlimited. The initial default is the logical CPU count. Takes effect: new operations. |
| ConfirmCopy | operations / Confirmations | [actionConfirmationsSettings](../cmd/f4/actions.go#L4047), [actionConfirmationsSettings](../cmd/f4/actions.go#L4105) | settings.ini / System / ConfirmCopy | [actions.go](../cmd/f4/actions.go#L2409), [actions.go](../cmd/f4/actions.go#L4047), [actions.go](../cmd/f4/actions.go#L4105) | Confirm copy: Show the copy destination and options dialog before an ordinary copy. Takes effect: new operations. |
| ConfirmMove | operations / Confirmations | [actionConfirmationsSettings](../cmd/f4/actions.go#L4053), [actionConfirmationsSettings](../cmd/f4/actions.go#L4106) | settings.ini / System / ConfirmMove | [actions.go](../cmd/f4/actions.go#L2404), [actions.go](../cmd/f4/actions.go#L4053), [actions.go](../cmd/f4/actions.go#L4106) | Confirm move: Show the move destination and options dialog before an ordinary move. Takes effect: new operations. |
| ConfirmDelete | operations / Confirmations | [actionConfirmationsSettings](../cmd/f4/actions.go#L4059), [actionConfirmationsSettings](../cmd/f4/actions.go#L4107) | settings.ini / System / ConfirmDelete | [actions.go](../cmd/f4/actions.go#L3110), [actions.go](../cmd/f4/actions.go#L4059), [actions.go](../cmd/f4/actions.go#L4107) | Confirm deletion: Ask before deleting selected items. Takes effect: new operations. |
| DeleteCancelFocused | operations / Confirmations | [actionConfirmationsSettings](../cmd/f4/actions.go#L4071), [actionConfirmationsSettings](../cmd/f4/actions.go#L4109) | settings.ini / System / DeleteCancelFocused | [actions.go](../cmd/f4/actions.go#L3151), [actions.go](../cmd/f4/actions.go#L3182), [actions.go](../cmd/f4/actions.go#L4071) | Initially focus Cancel for deletion: Place initial keyboard focus on Cancel in deletion confirmations. Takes effect: new operations. |
| ConfirmExit | startup / Exit | [actionConfirmationsSettings](../cmd/f4/actions.go#L4065), [actionConfirmationsSettings](../cmd/f4/actions.go#L4108) | settings.ini / System / ConfirmExit | [actions.go](../cmd/f4/actions.go#L4065), [actions.go](../cmd/f4/actions.go#L4108), [panels_frame.go](../cmd/f4/panels_frame.go#L3338) | Confirm exit: Ask before exiting f4. Takes effect: live. |
| Compare.Recursive | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Compare subfolders: Walk subfolders instead of comparing only the two panel folders. Takes effect: new comparisons. |
| Compare.LimitDepth | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Limit recursion depth: Stop recursive comparison at the maximum depth below the panel folders. Takes effect: new comparisons. |
| Compare.MaxDepth | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Maximum comparison depth: Maximum folder depth when recursion limiting is enabled, from 1 to 99. Takes effect: new comparisons. |
| Compare.MarkedOnly | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Compare marked items only: Limit comparison to items marked in the panels. Takes effect: new comparisons. |
| Compare.ByTime | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Compare modification times: Report files as different when modification times differ beyond enabled tolerances. Takes effect: new comparisons. |
| Compare.TimeSlack | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Allow two-second time difference: Treat modification times up to two seconds apart as equal, accommodating FAT timestamp precision. Takes effect: new comparisons. |
| Compare.IgnoreZones | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Ignore timezone offsets: Ignore time differences in whole quarter hours up to 26 hours. Takes effect: new comparisons. |
| Compare.BySize | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Compare file sizes: Report files as different when their byte sizes differ. Takes effect: new comparisons. |
| Compare.ByContent | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Compare file contents: Read and compare file contents using the selected content normalization. Takes effect: new comparisons. |
| Compare.Ignore | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Normalize compared contents: Enable the selected whitespace or line-ending normalization before comparing contents. Takes effect: new comparisons. |
| Compare.IgnoreMode | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Content normalization: Normalize line endings or remove whitespace bytes before content comparison. Takes effect: new comparisons. Choices: 0:Line endings;1:All whitespace |
| Compare.ReportEqual | operations / Folder comparison defaults | [Compare folders → defaults](../cmd/f4/compare_folders_ui.go) | settings.ini / Compare / Compare | [comparison engine](../cmd/f4/compare_folders.go) | Report equal folders: Show a message when comparison finds no differences. Takes effect: new comparisons. |
| EditorTabSize | editor / Text input | [actionEditorSettings](../cmd/f4/actions.go#L2799), [actionEditorSettings](../cmd/f4/actions.go#L3014), [actionEditorSettings](../cmd/f4/actions.go#L3015) | settings.ini / Editor / TabSize | [actions.go](../cmd/f4/actions.go#L2799), [actions.go](../cmd/f4/actions.go#L3014), [actions.go](../cmd/f4/actions.go#L3015) | Tab width: Set tab-stop width for newly opened editors. Viewer text rendering also uses this setting. Takes effect: new editors; viewer live. |
| EditorExpandTabs | editor / Text input | [actionEditorSettings](../cmd/f4/actions.go#L2762), [actionEditorSettings](../cmd/f4/actions.go#L2763), [actionEditorSettings](../cmd/f4/actions.go#L2764) | settings.ini / Editor / ExpandTabs | [actions.go](../cmd/f4/actions.go#L2762), [actions.go](../cmd/f4/actions.go#L2763), [actions.go](../cmd/f4/actions.go#L2764) | Tab key inserts: Insert a literal tab or spaces to the next tab stop. Legacy New and All modes currently have identical insertion behavior; existing tabs are not converted. Takes effect: new editors. Choices: 0:Tab character;1:Spaces;2:Spaces (legacy All) |
| EditorAutoIndent | editor / Text input | [actionEditorSettings](../cmd/f4/actions.go#L2817), [actionEditorSettings](../cmd/f4/actions.go#L3019) | settings.ini / Editor / AutoIndent | [actions.go](../cmd/f4/actions.go#L2817), [actions.go](../cmd/f4/actions.go#L3019), [editor_view.go](../cmd/f4/editor_view.go#L450) | Automatic indentation: Copy the current line's leading spaces and tabs when inserting a newline. Takes effect: new editors. |
| EditorCursorBeyondEOL | editor / Text input | [actionEditorSettings](../cmd/f4/actions.go#L2822), [actionEditorSettings](../cmd/f4/actions.go#L3020) | settings.ini / Editor / CursorBeyondEOL | [actions.go](../cmd/f4/actions.go#L2822), [actions.go](../cmd/f4/actions.go#L3020), [editor_view.go](../cmd/f4/editor_view.go#L451) | Cursor beyond end of line: Allow virtual space after the line end. Inserting text pads the gap with spaces. Takes effect: new editors. |
| EditorUseEditorConfig | editor / Text input | [actionEditorSettings](../cmd/f4/actions.go#L2827), [actionEditorSettings](../cmd/f4/actions.go#L3021) | settings.ini / Editor / UseEditorConfig | [actions.go](../cmd/f4/actions.go#L2827), [actions.go](../cmd/f4/actions.go#L3021), [editor_view.go](../cmd/f4/editor_view.go#L452) | Read adjacent .editorconfig: Apply matching indent_style, indent_size and tab_width from .editorconfig beside the opened file. Parent-directory discovery and the full standard are not implemented. Takes effect: new editors. |
| EditorAutoComplete | editor / Completion | [actionEditorSettings](../cmd/f4/actions.go#L2832), [actionEditorSettings](../cmd/f4/actions.go#L3022) | settings.ini / Editor / AutoComplete | [actions.go](../cmd/f4/actions.go#L2832), [actions.go](../cmd/f4/actions.go#L3022), [editor_view.go](../cmd/f4/editor_view.go#L463) | Editor completion: Enable completion in newly opened named files whose basenames match the configured masks. Takes effect: new editors. |
| EditorAutoCompleteMask | editor / Completion | [actionEditorSettings](../cmd/f4/actions.go#L2855), [actionEditorSettings](../cmd/f4/actions.go#L3027) | settings.ini / Editor / AutoCompleteMask | [actions.go](../cmd/f4/actions.go#L2855), [actions.go](../cmd/f4/actions.go#L3027), [editor_view.go](../cmd/f4/editor_view.go#L464) | Completion filename masks: Semicolon-separated, case-insensitive filename patterns selecting files eligible for editor completion. Takes effect: new editors. |
| EditorCrosshair | editor / Visual aids | [actionEditorSettings](../cmd/f4/actions.go#L2837), [actionEditorSettings](../cmd/f4/actions.go#L3023) | settings.ini / Editor / Crosshair | [actions.go](../cmd/f4/actions.go#L2837), [actions.go](../cmd/f4/actions.go#L3023), [colorer_settings.go](../cmd/f4/colorer_settings.go#L80) | Show cursor crosshair: Draw cursor guide lines using the selected axis mode. Takes effect: live. |
| EditorCrossMode | editor / Visual aids | [actionColorerSettings](../cmd/f4/colorer_settings.go#L130), [actionColorerSettings](../cmd/f4/colorer_settings.go#L225) | settings.ini / Editor / CrossMode | [colorer_settings.go](../cmd/f4/colorer_settings.go#L83), [colorer_settings.go](../cmd/f4/colorer_settings.go#L130), [colorer_settings.go](../cmd/f4/colorer_settings.go#L225) | Crosshair axes: Select no guide, a vertical guide, a horizontal guide, or both. Colorer can supply guide colors but the behavior is editor-wide. Takes effect: live. Choices: 0:None;1:Vertical;2:Horizontal;3:Both |
| EditorMarkOccurrences | editor / Visual aids | [actionEditorSettings](../cmd/f4/actions.go#L2846), [actionEditorSettings](../cmd/f4/actions.go#L3024) | settings.ini / Editor / MarkOccurrences | [actions.go](../cmd/f4/actions.go#L2846), [actions.go](../cmd/f4/actions.go#L3024), [editor_view.go](../cmd/f4/editor_view.go#L4863) | Highlight selected-text occurrences: Highlight other exact occurrences of an ordinary single-line selection of 2-256 bytes containing non-whitespace. This does not automatically highlight the cursor word. Takes effect: live. |
| EditorAutodetectCodePage | editor / Text encoding | [actionEditorSettings](../cmd/f4/actions.go#L2812), [actionEditorSettings](../cmd/f4/actions.go#L3010) | settings.ini / Editor / AutodetectCodePage | [actions.go](../cmd/f4/actions.go#L865), [actions.go](../cmd/f4/actions.go#L2812), [actions.go](../cmd/f4/actions.go#L3010) | Detect editor encoding: Detect text encoding when opening a file in the editor. Takes effect: new editors. |
| EditorDefaultCodePage | editor / Text encoding | [actionEditorSettings](../cmd/f4/actions.go#L2806), [actionEditorSettings](../cmd/f4/actions.go#L3012) | settings.ini / Editor / DefaultCodePage | [actions.go](../cmd/f4/actions.go#L848), [actions.go](../cmd/f4/actions.go#L865), [actions.go](../cmd/f4/actions.go#L2806) | Default editor encoding: Fallback encoding when detection is disabled or cannot determine a better result. Per-file overrides remain separate. Takes effect: new editors. |
| ViewerAutodetectCodePage | editor / Text encoding | [actionViewerSettings](../cmd/f4/codepage_settings.go#L106), [actionViewerSettings](../cmd/f4/codepage_settings.go#L135) | settings.ini / Viewer / AutodetectCodePage | [codepage_settings.go](../cmd/f4/codepage_settings.go#L106), [codepage_settings.go](../cmd/f4/codepage_settings.go#L135), [quick_view_panel.go](../cmd/f4/quick_view_panel.go#L369) | Detect viewer encoding: Detect encoding when opening the viewer or a quick-view preview. Takes effect: new viewers. |
| ViewerDefaultCodePage | editor / Text encoding | [actionViewerSettings](../cmd/f4/codepage_settings.go#L100), [actionViewerSettings](../cmd/f4/codepage_settings.go#L137) | settings.ini / Viewer / DefaultCodePage | [codepage_settings.go](../cmd/f4/codepage_settings.go#L100), [codepage_settings.go](../cmd/f4/codepage_settings.go#L137), [quick_view_panel.go](../cmd/f4/quick_view_panel.go#L369) | Default viewer encoding: Fallback viewer and quick-view encoding when detection is disabled or inconclusive. Takes effect: new viewers. |
| UseExternalEditor | editor / External editor | [actionEditorSettings](../cmd/f4/actions.go#L2859), [actionEditorSettings](../cmd/f4/actions.go#L3028) | settings.ini / Editor / UseExternalEditor | [actions.go](../cmd/f4/actions.go#L2164), [actions.go](../cmd/f4/actions.go#L2309), [actions.go](../cmd/f4/actions.go#L2859) | Use external editor: Route ordinary Edit commands to an external editor. Remote files are temporarily downloaded and changes can be uploaded afterward. Takes effect: new edit commands. |
| ExternalEditorConsole | editor / External editor | [configuredExternalEditorCommand](../cmd/f4/actions.go#L806), [configuredExternalEditorCommand](../cmd/f4/actions.go#L807), [actionEditorSettings](../cmd/f4/actions.go#L2863) | settings.ini / Editor / ExternalEditorConsole | [actions.go](../cmd/f4/actions.go#L806), [actions.go](../cmd/f4/actions.go#L807), [actions.go](../cmd/f4/actions.go#L2863) | Console editor command: Command for terminal sessions; the file path is appended as the last argument. Current parsing splits on whitespace and is not a shell-expression parser. Takes effect: new edit commands. |
| ExternalEditorGUI | editor / External editor | [configuredExternalEditorCommand](../cmd/f4/actions.go#L803), [configuredExternalEditorCommand](../cmd/f4/actions.go#L804), [actionEditorSettings](../cmd/f4/actions.go#L2872) | settings.ini / Editor / ExternalEditorCommandGUI | [actions.go](../cmd/f4/actions.go#L803), [actions.go](../cmd/f4/actions.go#L804), [actions.go](../cmd/f4/actions.go#L2872) | Graphical editor command: Command for graphical sessions; the file path is appended as the last argument. Current parsing splits on whitespace and is not a shell-expression parser. Takes effect: new edit commands. |
| EditorHighlighter | syntax / Engine | [actionEditorSettings](../cmd/f4/actions.go#L2770), [actionEditorSettings](../cmd/f4/actions.go#L3003), [actionColorerSettings](../cmd/f4/colorer_settings.go#L217) | settings.ini / Editor / Highlighter | [actions.go](../cmd/f4/actions.go#L1018), [actions.go](../cmd/f4/actions.go#L1029), [actions.go](../cmd/f4/actions.go#L1037) | Syntax highlighter: Choose Chroma, Colorer or no highlighting for newly opened editors. Colorer needs installed schemas and may fall back when unavailable. Takes effect: new editors. Choices: Chroma:Chroma;Colorer:Colorer;None:None |
| EditorSyntaxAnimation | syntax / Engine | [actionEditorSettings](../cmd/f4/actions.go#L2851), [actionEditorSettings](../cmd/f4/actions.go#L3026) | settings.ini / Editor / SyntaxAnimation | [actions.go](../cmd/f4/actions.go#L2851), [actions.go](../cmd/f4/actions.go#L3026), [editor_fade.go](../cmd/f4/editor_fade.go#L24) | Animate syntax colors: Fade arriving RGB syntax colors over about 400 milliseconds. Indexed colors are unchanged. Takes effect: live. |
| EditorColorerScheme | syntax / Colorer | [actionEditorSettings](../cmd/f4/actions.go#L2788), [actionEditorSettings](../cmd/f4/actions.go#L3004), [actionEditorSettings](../cmd/f4/actions.go#L3006) | settings.ini / Editor / ColorerScheme | [actions.go](../cmd/f4/actions.go#L2788), [actions.go](../cmd/f4/actions.go#L3004), [actions.go](../cmd/f4/actions.go#L3006) | Colorer scheme: Select the HRD scheme used for syntax, guide and editor base colors. Empty uses the built-in default. Takes effect: Colorer reload. |
| EditorColorerSyntax | syntax / Colorer | [actionColorerSettings](../cmd/f4/colorer_settings.go#L141), [actionColorerSettings](../cmd/f4/colorer_settings.go#L226) | settings.ini / Editor / ColorerSyntax | [colorer_async.go](../cmd/f4/colorer_async.go#L271), [colorer_settings.go](../cmd/f4/colorer_settings.go#L141), [colorer_settings.go](../cmd/f4/colorer_settings.go#L226) | Colorer syntax colors: Enable Colorer syntax coloring while retaining its other style facilities. Takes effect: Colorer reload. |
| EditorColorerBackground | syntax / Colorer | [actionEditorSettings](../cmd/f4/actions.go#L2841), [actionEditorSettings](../cmd/f4/actions.go#L3025), [actionColorerSettings](../cmd/f4/colorer_settings.go#L146) | settings.ini / Editor / ColorerBackground | [actions.go](../cmd/f4/actions.go#L2841), [actions.go](../cmd/f4/actions.go#L3025), [colorer_plugin.go](../cmd/f4/colorer_plugin.go#L374) | Use Colorer base colors: Use foreground and background fields supplied by the Colorer scheme instead of only the general editor palette. Takes effect: Colorer reload. |
| EditorColorerCatalog | syntax / Colorer | [actionColorerSettings](../cmd/f4/colorer_settings.go#L150), [actionColorerSettings](../cmd/f4/colorer_settings.go#L228) | settings.ini / Editor / ColorerCatalog | [colorer_plugin.go](../cmd/f4/colorer_plugin.go#L228), [colorer_settings.go](../cmd/f4/colorer_settings.go#L150), [colorer_settings.go](../cmd/f4/colorer_settings.go#L228) | Colorer configuration directory: Directory containing Colorer configuration data. Empty uses the profile's colorer/configs directory; this is not a catalog XML filename. Takes effect: Colorer reload. |
| MacKeyboard | keyboard / Editing chords | Core setting bound by its canonical backing value | settings.ini / Interface / MacKeyboard | [action_registry.go](../cmd/f4/action_registry.go#L1586), [action_registry.go](../cmd/f4/action_registry.go#L1588), [mackeys.go](../cmd/f4/mackeys.go#L68) | Mac keyboard mode: Use Mac-style editing chords in editors and dialog fields. Auto enables them on macOS; panels retain Far navigation. Command translation requires backend support. Takes effect: live. Choices: auto:Automatic;on:On;off:Off |
| SearchExactOnHit | keyboard / Compatibility | [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3903), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4021), [configureHotkeyTableSearch](../cmd/f4/hotkeys_ui.go#L195) | settings.ini / Panel / SearchExactOnHit | [actions.go](../cmd/f4/actions.go#L3903), [actions.go](../cmd/f4/actions.go#L4021), [hotkeys_ui.go](../cmd/f4/hotkeys_ui.go#L195) | Legacy exact-hit preference: The old hotkey table used this to narrow exact search hits. Settings Center uses dim-only search and does not consume this preference. Its saved value is retained. Takes effect: unavailable. |
| EscTogglePanels | terminal / Terminal input | [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3895), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4019) | settings.ini / Panel / EscTogglePanels | [actions.go](../cmd/f4/actions.go#L3895), [actions.go](../cmd/f4/actions.go#L4019), [hotkeys.go](../cmd/f4/hotkeys.go#L38) | Escape toggles panels: Allow Escape to show or hide the file panels to access the terminal. Takes effect: live. |
| TerminalCtrlNWorkspace | terminal / Terminal input | [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3899), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4020) | settings.ini / Panel / TerminalCtrlNWorkspace | [actions.go](../cmd/f4/actions.go#L3899), [actions.go](../cmd/f4/actions.go#L4020), [hotkeys.go](../cmd/f4/hotkeys.go#L419) | Ctrl+N creates workspace: Reserve Ctrl+N while terminal input has focus to create a workspace. Off sends the chord to the terminal program. Takes effect: live. |
| KeepTerminalCursor | terminal / Terminal input | [actionAppearanceSettings](../cmd/f4/actions.go#L4692), [actionAppearanceSettings](../cmd/f4/actions.go#L4822), [actionAppearanceSettings](../cmd/f4/actions.go#L4823) | settings.ini / Panel / KeepTerminalCursor | [actions.go](../cmd/f4/actions.go#L4692), [actions.go](../cmd/f4/actions.go#L4822), [actions.go](../cmd/f4/actions.go#L4823) | Preserve terminal cursor style: Leave cursor-style management to the terminal instead of letting f4 change it. Takes effect: live. |
| ConsoleMode | terminal / Presentation | [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3912), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4023), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4025) | settings.ini / Panel / ConsoleMode | [actions.go](../cmd/f4/actions.go#L3912), [actions.go](../cmd/f4/actions.go#L4023), [actions.go](../cmd/f4/actions.go#L4025) | Terminal presentation: Use the embedded terminal, host terminal with f4 overlay, or host terminal without overlay. Unsupported host styles fall back according to terminal capabilities. Takes effect: new workspaces. Choices: own:Embedded;far:Host with overlay;mc:Host without overlay |
| HistoryDirsPrefixLen | history / Presentation | [actionCommandHistory](../cmd/f4/actions.go#L307), [actionCommandHistory](../cmd/f4/actions.go#L313) | settings.ini / History / DirsPrefixLen | [actions.go](../cmd/f4/actions.go#L307), [actions.go](../cmd/f4/actions.go#L313) | Command directory column width: Width of the command-history directory prefix while date-and-time display is active. Minimum four characters. Takes effect: new history dialogs. |
| MacroRecordFormat | menus / Macro recording | [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3949), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L3950), [actionPanelAdditionalSettings](../cmd/f4/actions.go#L4030) | settings.ini / System / MacroRecordFormat | [actions.go](../cmd/f4/actions.go#L3949), [actions.go](../cmd/f4/actions.go#L3950), [actions.go](../cmd/f4/actions.go#L4030) | Record macros as: Save recorded key macros in the legacy key_macros.ini format or as Lua scripts under Macros/scripts. Takes effect: new recordings. Choices: 0:Legacy INI;1:Lua scripts |
| ProxyMode | network / Global proxy | [actionProxySettings](../cmd/f4/proxy_settings_ui.go#L46), [actionProxySettings](../cmd/f4/proxy_settings_ui.go#L108) | settings.ini / Proxy / Mode | [proxy_settings_ui.go](../cmd/f4/proxy_settings_ui.go#L46), [proxy_settings_ui.go](../cmd/f4/proxy_settings_ui.go#L108) | Proxy mode: Use system/environment proxy settings, direct connections, or an HTTP/SOCKS5 proxy for downloads and network providers unless overridden per connection. Takes effect: new requests. Choices: 1:System;2:Direct;3:HTTP;4:SOCKS5 |
| ProxyHost | network / Global proxy | [actionProxySettings](../cmd/f4/proxy_settings_ui.go#L51), [actionProxySettings](../cmd/f4/proxy_settings_ui.go#L109) | settings.ini / Proxy / Host | [proxy_settings_ui.go](../cmd/f4/proxy_settings_ui.go#L51), [proxy_settings_ui.go](../cmd/f4/proxy_settings_ui.go#L109) | Proxy host: Hostname or address of the configured HTTP or SOCKS5 proxy. Takes effect: new requests. |
| ProxyPort | network / Global proxy | [actionProxySettings](../cmd/f4/proxy_settings_ui.go#L53), [actionProxySettings](../cmd/f4/proxy_settings_ui.go#L110) | settings.ini / Proxy / Port | [proxy_settings_ui.go](../cmd/f4/proxy_settings_ui.go#L53), [proxy_settings_ui.go](../cmd/f4/proxy_settings_ui.go#L110) | Proxy port: Port of the configured HTTP or SOCKS5 proxy. Takes effect: new requests. |
| ProxyUser | network / Global proxy | [actionProxySettings](../cmd/f4/proxy_settings_ui.go#L55), [actionProxySettings](../cmd/f4/proxy_settings_ui.go#L111) | settings.ini / Proxy / User | [proxy_settings_ui.go](../cmd/f4/proxy_settings_ui.go#L55), [proxy_settings_ui.go](../cmd/f4/proxy_settings_ui.go#L111) | Proxy username: Username used to authenticate to the configured proxy. Takes effect: new requests. |
| ProxyPass | network / Global proxy | [actionProxySettings](../cmd/f4/proxy_settings_ui.go#L57), [actionProxySettings](../cmd/f4/proxy_settings_ui.go#L112) | settings.ini / Proxy / Password | [proxy_settings_ui.go](../cmd/f4/proxy_settings_ui.go#L57), [proxy_settings_ui.go](../cmd/f4/proxy_settings_ui.go#L112) | Proxy password: Password used to authenticate to the configured proxy. Its value is excluded from search. Takes effect: new requests. |
| UpdateChannel | updates / Automatic checks | [actionUpdateSettings](../cmd/f4/actions.go#L4355), [actionUpdateSettings](../cmd/f4/actions.go#L4356), [actionUpdateSettings](../cmd/f4/actions.go#L4357) | settings.ini / Update / Channel | [actions.go](../cmd/f4/actions.go#L4355), [actions.go](../cmd/f4/actions.go#L4356), [actions.go](../cmd/f4/actions.go#L4357) | Update channel: Check stable releases or nightly builds. Takes effect: next check. Choices: 0:Stable;1:Nightly |
| UpdateInterval | updates / Automatic checks | [actionUpdateSettings](../cmd/f4/actions.go#L4364), [actionUpdateSettings](../cmd/f4/actions.go#L4365), [actionUpdateSettings](../cmd/f4/actions.go#L4366) | settings.ini / Update / Interval | [actions.go](../cmd/f4/actions.go#L4364), [actions.go](../cmd/f4/actions.go#L4365), [actions.go](../cmd/f4/actions.go#L4366) | Check for updates at startup: Check never, every start, after at least 24 hours, or after at least seven days. Checks run at startup, not on a continuous timer. Takes effect: next launch. Choices: 0:Never;1:Every start;2:Daily;3:Weekly |

### Repeated scalar controls

| Canonical IDs | Original path | Category | Persistence / implementation | Effect |
|---|---|---|---|---|
| `Wheel{Panel,Editor,Viewer,Menu,Table}{Up,Down}` (10 independent controls) | Options → Mouse wheel → context/direction | keyboard | settings.ini; `applyWheelSettings` | Rows per wheel notch; zero follows the system value. |
| `HistoryShowTimes.0`, `.1`, `.2` | Command / folder / viewer-editor history → timestamp format | history | settings.ini; `history_dialog.go`, `actions.go` | Independent date/time, date-only or hidden timestamps for each history. |
| `DriveMenuOptions.0`–`.13` | Drive chooser → options → each checkbox | drives | settings.ini bit mask; `drive_menu_options.go` | See the per-bit mapping below; all other bits are retained. |

| Bit | Canonical meaning | Availability |
|---|---|---|
| 0 | Drive type | Editable; applied to the next drive menu. |
| 1 | Volume label | Editable; applied to the next drive menu. |
| 2 | Use shell name | Unavailable compatibility field; no effective consumer. |
| 3 | Filesystem | Editable; applied to the next drive menu. |
| 4 | Total/free capacity | Editable; applied to the next drive menu. |
| 5 | Fractional capacity | Editable; applied to the next drive menu. |
| 6 | Network target | Editable; applied to the next drive menu. |
| 7 | Plugin entries | Editable; applied to the next drive menu. |
| 8 | Order plugin entries by shortcut | Editable; applied to the next drive menu. |
| 9 | Removable drives | Editable; applied to the next drive menu. |
| 10 | Optical drives | Editable; applied to the next drive menu. |
| 11 | Network drives | Editable; applied to the next drive menu. |
| 12 | Detect virtual drives | Unavailable compatibility field; no effective consumer. |
| 13 | Bookmarks and links | Editable; applied to the next drive menu. |

## Collections and record fields

Every listed field is independently described in its provider catalog. Ordered stores retain ordering; name-keyed connection maps retain their existing format. Collections use an inline selected-record editor rather than Details or Additional Settings.

| Canonical collection / fields | Original menu → dialog → subdialog | New category | Store and consuming implementation |
|---|---|---|---|
| `associations`: mask, description; Enter/AltEnter/F3/AltF3/F4/AltF4 commands and enable flags | Files → File associations → Edit association | associations | `settings/associations.ini`; `file_associations.go`, `file_associations_ui.go`. Alternate slots remain stored and unavailable because current dispatch has no consumer. |
| `bookmarks`: ten paths; retained plugin/file/data metadata | Drive chooser → bookmarks; numbered folder shortcuts | drives | Existing bookmark file; `bookmarks.go`. Reordering changes digit slots. |
| `drive-links`: name, path, hotkey | Drive chooser → named links → edit | drives | Existing links file; `drive_bookmarks.go`. |
| `usermenu.*`: label, activation key, submenu flag, parent, multiline commands | User menu → F4 / Insert → item/submenu editor | menus | Global INI, executable-scoped and ancestor-local FarMenu files remain separate sources and separate drafts; `user_menu_ui.go`. |
| `bindings`: action, chord, area, condition | Options → Hotkey configurator → assign → area/condition → chord | hotkeys | `hotkeys.ini`; `hotkeys.go`. Native frame-owned chords are not editable bindings. |
| `envman.profiles`: kind, name, enabled, ordered variable lines | Environment Manager → profile editor | terminal | Environment Manager JSON; `plugins/envman`. Apply reconciles the environment only after a successful save. Separators are preserved. |
| `netfox.connections`: name, protocol, host, port, username, password, key path, timeout, codepage, FTP passive mode; proxy mode/host/port/user/password | NetFox → Add/Edit connection → Proxy | network | `NetFox.json`; `plugins/netfox/{dialog,netfox,proxy,settings_center}.go`. Unknown Options entries survive rename and edits. |
| `cloudfox.{gdrive,yandex,s3,webdav}`: name, credential-storage choice, keep/replace/clear credentials; provider fields below | CloudFox → Add/Edit profile → provider → authentication/storage | network | `CloudFox.json`, vault or keyring; `plugins/cloudfox/{dialog,secrets,credential_scope,settings_center}.go`. Metadata revisions and credential scope are checked before writes. |
| `plugins.registered`: ordered paths | Options → Plugins → Add/Remove | plugins | Main INI registered-plugin list; startup plugin manager. Restart loads the saved list. |
| `plugins.permissions`: remembered answer / keep | Options → Plugins → Permissions → revoke | plugins | `plugin_permissions.json`; `plugin_permissions.go`. Revocation saves before publishing runtime state. |
| `plugins.catalog`: package metadata and installation status | Options → Plugin catalog → Install/Remove | plugins | PlugRing manifests; `plugring*.go`. Explicit operations; Cancel does not uninstall packages. |

### CloudFox provider fields

| Provider | Field | Actual effect |
|---|---|---|
| gdrive | client_id | OAuth client ID: OAuth audience used for desktop loopback authorization with PKCE. |
| gdrive | secret.client_secret | OAuth client secret: Client secret used during Google authorization. Leave blank to retain stored credentials. |
| yandex | client_id | OAuth client ID: OAuth application used for browser authorization with PKCE. |
| yandex | root | Remote root: Root directory exposed by this connection, such as disk:/. |
| yandex | secret.oauth_token | OAuth token: Optional existing OAuth token. Leave blank to preserve stored credentials. |
| s3 | bucket | Bucket: Bucket name. Empty enables bucket discovery rather than selecting a bucket. |
| s3 | region | Region: AWS region used for signing and endpoint selection. Default us-east-1. |
| s3 | root_prefix | Root prefix: Object-key prefix exposed as the root of this connection. |
| s3 | endpoint | Custom endpoint: Optional S3-compatible server endpoint instead of the standard AWS endpoint. |
| s3 | auth | Authentication source: Use the default credential chain, an AWS profile, explicit static keys, or anonymous access. |
| s3 | profile | AWS profile: Named AWS credential profile used with profile authentication. |
| s3 | secret.access_key_id | Access key ID: Static S3 access key. Entering static keys selects static authentication. |
| s3 | secret.secret_access_key | Secret access key: Static S3 signing secret. Excluded from search. |
| s3 | secret.session_token | Session token: Optional temporary-session credential for static S3 authentication. |
| s3 | custom_ca | Custom CA file: Additional certificate-authority file for the S3 endpoint. |
| s3 | use_path_style | Use path-style addressing: Address buckets in the URL path instead of the endpoint hostname. |
| s3 | allow_insecure | Allow credentials over HTTP: Permit explicit insecure S3 transport where the existing provider allows it. |
| webdav | base_url | Server URL: Base URL of the WebDAV server. |
| webdav | root | Remote root: Directory exposed as the root of this WebDAV connection. |
| webdav | auth | Authentication method: Use Basic, Digest, Bearer token, or anonymous authentication. |
| webdav | username | Username: Account name for Basic or Digest authentication. |
| webdav | secret.password | Password: Account password. Leave blank to preserve stored credentials. |
| webdav | secret.bearer_token | Bearer token: Token used with Bearer authentication. Leave blank to preserve stored credentials. |
| webdav | custom_ca | Custom CA file: Additional certificate-authority file for the server. |
| webdav | allow_insecure_digest | Allow Digest over HTTP: Permit Digest authentication over unencrypted HTTP according to existing provider policy. |

## Other bundled preferences

| IDs / controls | Original route | Category | Persistence and implementation |
|---|---|---|---|
| `mediainfo.ShowInPluginMenu`, `EnableQuickView`, `UseEditor`, `Prefix`, `Language`, `Template` | Plugin configuration → MediaInfo | metadata | MediaInfo settings JSON; plugin report rendering, prefix registration and Quick View contribution. |
| `visren.EditorFormat`, `visren.WordDiv` | VisRen configuration; rename dialog → word delimiters | operations | `visren.json`; `plugins/visren/config.go`, transformations and editor-format generation. Saving delimiters retains the editor format. |
| `envman.IgnoredVariables`, `envman.AlwaysUseEditor` | Environment Manager configuration | terminal | Environment Manager configuration; process/shell reconciliation and editor workflow preference. |
| `ai.key`, `ai.model` | Options → AI setup | ai | `vtvibe.ini`, `vtvibe_host.go`. Key precedence: GEMINI_API_KEY, GOOGLE_API_KEY, OPENAI_API_KEY, saved key. The effective source is displayed without its secret. |

## Explicit operations and exclusions

- Manual preference/session/window saves remain explicit operations, independent of four autosave policies. The former aggregate autosave switch is derived rather than an independently conflicting fifth preference.
- Profile copy/move selects a target for the next launch. Authentication prompts, file selection and operation confirmations remain genuine interactions.
- Schema download/reload, palette export, update checking, plugin installation/removal and legacy external-plugin configuration are explicit commands. They never silently apply other drafts.
- Current-file encodings, compare/copy/move/delete dialogs, connection opening, user-menu execution, macro recording, history entry details and file navigation are contextual workflows.
- The Editor/Colorer highlighter and crosshair duplicates map to one backing setting each.
- The two ineffective drive flags remain unavailable compatibility values. Exact-hit searching applies only to the restored Hotkey Configurator table and does not affect Center search.
- Both legacy tab-expansion modes retain their stored numeric values. Their descriptions explain that current insertion behavior is identical.
- Configuration-only image-decoder priorities, X11 interception, custom highlight-rule editors and custom panel modes are outside consolidation. Their values are preserved.
- Saved `qt` and `ext:*` backend values remain intact even if the current frontend cannot enumerate them.

## Extension contract

`sdk/f4settings` contains category/group/field/choice/collection/command descriptors without vtui imports. Providers own storage, validation, reversible previews and commit results. `vfs.SettingsContributionHost` is optional beside existing contribution APIs and returns an unregister handle; HostAPI and RPC contracts are unchanged. A provider can add multiple categories or groups and keep its own persistence. Qt and remote-plugin schema integration are deferred.

Descriptions contain behavior and limits, while runtime availability can explain dependencies. Secret controls are masked and omitted from the search index. CloudFox Begin reads metadata without unlocking credentials; authentication is an explicit operation.

Providers register a catalog with a stable provider ID, category IDs and setting IDs. Categories shared with core retain their original position; new categories are appended. `Catalog.Groups` supplies localized subgroup headings. Fields use stable string values for choices, independently of their translated captions; `AllowCustom` supports editable choice lists such as installed fonts. Collections retain provider record IDs, revisions and ordering, and expose their selected record inline.

`Begin` returns an isolated `Draft`. Validation must not publish draft values. `CommitFunc` patches changed fields or records into current provider state and returns acknowledgements only for successful writes. `Result.Values` and `Result.Records` can return canonicalized saved data, while `Revisions` identifies updated records and `Deleted` acknowledges deletions. The renderer accepts results on the UI thread; a partial failure leaves unsuccessful edits pending. Providers declaring background commits must marshal UI work through the task context. Registered provider removal disables further editing and callback dispatch in an already-open Center.

Legacy `CmLanguage`, `CmHelpLanguage`, `CmHotkeyConfig`, `CmPlugins` and `CmPlugRing` commands also deep-link into the Center, alongside the action IDs in `settings_routes.go`. Contextual user-menu editing preserves the originating file, scope and submenu. Contextual drive-link creation seeds the active panel path without saving it. Reopening Settings while it is already active navigates the existing draft instead of opening another settings dialog.

## Validation

### Toolkit mouse fixes

The vtui fixes are submitted in https://github.com/unxed/vtui/pull/112.
Until an upstream release includes them, go.mod pins the published Zoinen/vtui
commit `8499a597a607` using a versioned replacement. No sibling checkout is
required. Build with `GOWORK=off` and the system Go cache. Replace this temporary
fork pin with an upstream release after the dependency PR is merged and tagged.

The toolkit distinguishes presses, held-button movement and both console and
ANSI/SGR releases. Scrollbars and their containing groups retain capture outside
their bounds. Opening a dropdown transfers the held gesture from its owner;
hover highlights and release confirms. Menu-bar gestures stay active outside
menu items until release, and checkboxes toggle once per physical press.
The Settings Center routes captured scrollbar and child events before clipping
hit tests, including its explanation pane. Regression tests cover both release
forms, leaving the window, cross-pane dragging and the next fresh click.

### Dropdown choice help

`f4settings.Choice.Description` supplies localized help for an individual choice.
While a dropdown is open, the explanation pane follows its highlighted row,
including keyboard navigation and mouse hover. Browsing does not edit the draft;
closing the list restores the field explanation. Choice descriptions participate
in the existing dim-only search. Providers can supply descriptions without any
renderer changes; missing descriptions use the field explanation alongside the
choice label, suitable for language, font and other self-explanatory lists.

The built-in behavioral choice inventory is `cmd/f4/settings_choice_help.tsv`.
It covers startup backends, navigation, operation modes, editor choices, update
policies and bundled connection/authentication choices. Translations use the
ordinary English and Russian resources. Contribution enrichment copies descriptor
slices and preserves provider-supplied descriptions and all stored choice values.
Backend explanations follow `startup_backend.go`, `main.go`'s startup dispatch,
`gui_backend_capability.go`, and the pinned vtui backend implementations; they do
not claim benchmark superiority for a particular graphics stack.

### Opening performance (Windows, 2026-09-09)

`BenchmarkSettingsOpen` measures provider snapshots, catalogs, dialog construction,
layout and first paint into a silent 160×50 screen. It does not launch a
PanelsFrame or measure terminal presentation, asynchronous schema enumeration,
or providers registered only by a running application. It reads local settings
but never commits drafts. Measurements on a Ryzen 9 5950X:

| Implementation | Time per opening | Allocated bytes | Allocations |
|---|---:|---:|---:|
| Before | 1,158 ms | 138.0 MB | 1,784,230 |
| Batch Windows font-label lookup | 56 ms | 26.6 MB | 99,098 |
| Also share core catalog within an opening | 31–35 ms | 13.8 MB | 51,815 |

The initial CPU profile attributed about 90% of samples to font-label resolution:
each installed font caused another full Windows font-registry enumeration.
The core catalog was also constructed twice, for the draft and the renderer.
Font labels now use one registry snapshot per catalog, and each opening shares
one core catalog. Nothing is cached between openings, preserving newly installed
fonts, current language and custom font values.

Reproduce with the system Go cache and `GOWORK=off`:

```powershell
go test ./cmd/f4 -run '^$' -bench '^BenchmarkSettingsOpen$' -benchtime=3s -benchmem -count=3 -timeout 60s
go test ./cmd/f4 -run '^$' -bench '^BenchmarkSettingsOpen$' -benchtime=3s -cpuprofile "$env:TEMP\f4-settings.cpu" -o "$env:TEMP\f4-settings.test.exe" -timeout 60s
go tool pprof -top -cum "$env:TEMP\f4-settings.test.exe" "$env:TEMP\f4-settings.cpu"
```

The remaining profile suggests possible smaller improvements: replace repeated
pairwise font-path comparisons with a canonical-key index, and avoid repeated
language-resource parsing within catalog construction. If live sessions with
large connection/profile stores remain slow, measure each contributed provider's
`Begin` before considering lazy snapshots or background loading; those changes
must preserve cancellation, provider removal and record revision semantics.

### Functional coverage

Focused coverage lives in `sdk/f4settings/*_test.go`, `cmd/f4/settings*_test.go`, and bundled-provider settings tests. Theme checks render after replacing semantic dialog palette entries; persistence checks cover cancellation, failed writes, partial acknowledgement, concurrent changes and unknown-value preservation.

On Windows, use the pinned ConPTY runtime from the build workflow and the system Go cache. Run the main suite with `go test ./... -skip '^TestAllDialogs_LayoutValidation$' -timeout 300s`, then run the legacy layout suite separately with `GOMAXPROCS=1`, as CI does. The Center has dedicated clipped-viewport layout tests at 80×25, 110×25 and 160×50; its offscreen controls deliberately extend beyond the visible scrolling page.

The implementation was verified locally with all packages passing and the timing-sensitive schema-download test run alongside the serialized layout suite. Windows vet uses CI's `-unsafeptr=false` flag for native syscall wrappers; formatting and language-file ordering are checked separately. Colorer scheme enumeration runs asynchronously, including catalogs on unavailable network shares, and late results are ignored after the Center closes.

Regenerate the core field inventory with `F4_SETTINGS_EXPORT=<docs directory> go test ./cmd/f4 -run '^TestSettingsExportDescriptions$'`. This emits the JSON inventory and a temporary language fragment; merge missing language keys into `cmd/f4/lang/en.lng`, remove the fragment, and run `tools/langfmt`. Bundled-provider metadata and descriptions are kept beside each provider's implementation in `plugins/*/settings_center.go`.

### Choice presentation audit

Small fixed choice sets now use visible radio buttons. The renderer places all
choices on one row when their translated display widths fit; otherwise it stacks
and wraps them. When the caption and all choices fit together, they share one line; otherwise
the caption appears once above the choices. Up/Down browse
choices without changing the draft, Space selects, and Left/Right retain pane
navigation. Hover and keyboard focus show the existing per-choice explanation.

| Presentation | Audited settings |
| --- | --- |
| Radios | Startup mode; terminal renderer; workspace tab visibility and numbering; panel scrollbar; navigation mode; suggestion source; default operation mode; progress path display; comparison normalization; tab insertion; crosshair axes; syntax highlighter; Mac keyboard mode; terminal presentation; macro recording format; global proxy; update channel and frequency; all three history timestamp formats |
| Radios in embedded editors | VisRen editor format; environment entry kind; NetFox protocol and proxy mode; CloudFox secret storage, credential changes, and S3/WebDAV authentication |
| Dropdowns | Interface/help/report languages; font; theme; graphical renderer (extensible backend list); editor/viewer encodings; Colorer scheme; shortcut action and area (long lists) |

`Field.ChoicePresentation` is an optional frontend-neutral hint (`radio` or
`dropdown`). With no hint, fixed lists of up to five choices use radios; longer
lists and fields accepting custom values use dropdowns. Dynamic catalogs explicitly
request dropdowns so their appearance does not change with installed resources.
Unknown saved values remain selected and preserved until the user changes them.
Existing choice labels and descriptions supply localization for both presentations.

Checkbox and radio marks share `Dialog.Indicator.Background`. Only the three
indicator characters use this background; captions, spacing, foreground state
and focus highlighting retain their existing colors. An omitted value or
`inherit` preserves the original appearance. Modern explicitly uses `#232323`,
matching its input background. The slot is resolved at render time and exported
with other palette settings; theme previews and Cancel include it.

Search now belongs to the left category pane. During a nonempty search, compact
`[←]` / `[→]` match-navigation buttons appear to the right of the input and the
separator below it shows the total match count. Clearing search hides both arrows
and the count, and removes the arrows from keyboard traversal. Apply, OK and
Cancel are aligned at the lower right. The category heading and explanations
start at the top of their respective panes, independently of the search area.

`Dialog.Settings.Background` controls the central content surface. Modern uses
`#343434`; other bundled themes retain their previous dialog surface. An omitted
value or `inherit` preserves the normal dialog background. Rendering retains
semantic foregrounds, input surfaces, focus highlighting and search dimming.

The category column reserves five text columns for ` (99)` before searching
(with its scrollbar kept separate), so queries and match counts never resize it.
The search separator is immediately followed by the first category; its active
count is labeled Matches / Совпадений. All columns start on the search-caption
row and extend to the action row, with status messages sharing the action row.

A nonempty search input includes an inline × button on the input surface. It
clears all text (including whitespace), hides match controls, restores the input
width and returns keyboard focus to search without changing setting drafts.

Status messages are left-aligned on the Apply/OK/Cancel row and truncated before
the buttons; they never change content height. Typing and focus belongs to Panels.
Path suggestions belongs to Terminal & environment because it controls completion
in both the command line and eligible dialog fields. The former Navigation &
suggestions category is removed; its terminology remains searchable, and the
legacy path-hints action opens Terminal & environment.

The former Options → Save Settings entry is now a deep link into Workspaces &
saving, alongside the existing Manual saving commands. App.SaveSettings and its
Shift+F9 binding remain compatible; it no longer exposes a separate menu item
or opens the legacy save-settings dialog.
Settings search hover profiling (2026-09-09): `BenchmarkSettingsHoverSearch`
measures a mouse move followed by a silent-screen repaint with real core/provider
snapshots. The nonempty `editor` query previously repeated catalog matching for
category labels, row colors and the total count on every paint: 3.27 ms and
3.18 MB allocated per iteration. Caching category counts and record-row matches
reduced this run to 0.23 ms and 121 KB per iteration (about 14x faster). These
figures exclude terminal output. Cache invalidation follows draft edits,
collection/category rebuilds, query changes and language changes. Record cache
keys contain display names only; arbitrary record values and secrets are not
indexed. Mouse hover still updates choice explanations immediately.

## Upstream package migration

The audit above records original locations at `4cd62a34`. After upstream's
package split (`15fedb14`), implementation lives in `internal/settings` (catalog,
drafts, renderer, record editors and option help), `internal/config` (snapshot
serialization and preference patching), `internal/dialog` (profile transfer and
chord capture), `internal/panel` (record storage and contextual source snapshots),
and `internal/app` (entrypoints and cross-subsystem wiring). Localization resources
are in `internal/i18n/lang`, themes in `internal/theme/styles`, and help assets in
`internal/dialog/help`. `sdk/f4settings` and provider identifiers remain unchanged.

`settings.Host` is injected by the composition root; it owns session/window saves,
runtime refresh, update checks and package installation. Panel callbacks carry
captured menu scope and update its tree only after a successful Apply. The public
plugin contribution and opening capabilities remain optional. Explicit Copy/Move
uses the upstream profile transfer helpers, selects the target only after success,
and a conflicting move preserves both the source and current profile selection.

## Restored Hotkey Configurator tab

Hotkey Configurator has its own category beside Keyboard & shortcuts. It embeds
its original sortable five-column table (command, chord, area, condition,
description), normalized quick search, native read-only shortcuts, plugin
commands, Assign/Unbind confirmation and area/condition/chord capture workflow.
The old Settings.Hotkeys action and CmHotkeyConfig command open this tab. The
inline binding record editor is replaced in the application UI. The table uses
the whole content width; its description column replaces the side help pane.

Editing retains a draft across category switches. Apply/OK persist changes and
Cancel discards edits since the last Apply. Explicit unbinding retains the None
override so default chords cannot reappear after saving. The existing settings
provider checks concurrent changes and saves before replacing runtime bindings.
SearchExactOnHit is available under Keyboard & shortcuts and takes effect when
the configurator is next opened; it never changes sidebar search behavior.


## Contextual editor restoration

The Center consolidates global configuration menu entries, not in-place editing
commands. The consolidation introduced early Settings redirects in the following
workflows; those redirects are removed:

| Context | Local interaction | Shared Settings storage/logic |
| --- | --- | --- |
| Drive chooser Insert / edit link | Name, path and shortcut dialog; returns to drive chooser | `panel.DriveBookmark`, `LoadDriveBookmarks`, `SaveDriveBookmarks`; Drive chooser drive links |
| Numbered bookmarks edit | Path input for the selected slot | `panel.BookmarkSet`, `SaveBookmarks`; Drive chooser bookmark slots |
| User menu create/edit item or submenu | Original entry editor with source/scope retained | User-menu tree and source-specific writers; User menus |
| NetFox add/edit connection | Original connection dialog | NetFox configuration store; Network connections |
| CloudFox add/edit profile | Provider chooser and original profile editor | Credential validation, scope checks and repository; Network connections |
| Visual Renamer word delimiters | Small prompt within the active rename operation | `loadConfig`/`saveConfig`; File operations |

The two UI surfaces continue to use their existing domain models and persistence
routines. Contextual saves are immediate; Settings edits remain staged until
Apply. This restores the original forms without introducing another record format
or replacing the Settings inline editors. Global drive options, plugin
configuration (including Environment Manager), and application preference actions
still open the Center. Environment Manager's contextual profile editor was not
redirected and needs no rollback.
