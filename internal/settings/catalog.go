package settings

import (
	"reflect"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/sdk/f4settings"
)

var Categories = []f4settings.Category{
	{ID: "appearance", Label: f4settings.Text{English: "Appearance & language"}},
	{ID: "startup", Label: f4settings.Text{English: "Startup & profile"}},
	{ID: "workspaces", Label: f4settings.Text{English: "Workspaces & saving"}},
	{ID: "panels", Label: f4settings.Text{English: "Panels"}},
	{ID: "drives", Label: f4settings.Text{English: "Drive chooser"}},
	{ID: "operations", Label: f4settings.Text{English: "File operations"}},
	{ID: "editor", Label: f4settings.Text{English: "Editor & viewer"}},
	{ID: "syntax", Label: f4settings.Text{English: "Syntax highlighting"}},
	{ID: "keyboard", Label: f4settings.Text{English: "Keyboard & shortcuts"}},
	{ID: "hotkeys", Label: f4settings.Text{Key: "Hotkeys.Title", English: "Hotkey Configurator"}},
	{ID: "terminal", Label: f4settings.Text{English: "Terminal & environment"}},
	{ID: "history", Label: f4settings.Text{English: "History"}},
	{ID: "associations", Label: f4settings.Text{English: "File associations"}},
	{ID: "menus", Label: f4settings.Text{English: "User menus & macros"}},
	{ID: "network", Label: f4settings.Text{English: "Network & connections"}},
	{ID: "metadata", Label: f4settings.Text{English: "Metadata & reports"}},
	{ID: "updates", Label: f4settings.Text{English: "Updates"}},
	{ID: "plugins", Label: f4settings.Text{English: "Plugins"}},
	{ID: "ai", Label: f4settings.Text{English: "AI"}},
}

// Each row is storage field | category | subgroup | label | description |
// stable choice values | apply timing. Choice values never depend on labels.
const coreSettingsDefinitions = `
Language|appearance|Language|Interface language|Choose the language of the interface. Applying reloads resources and rebuilds Settings Center captions.||live
HelpLanguage|appearance|Language|Help language|Choose the built-in help language independently of the interface language.||live
UseLocalLanguageFiles|appearance|Language|Use local translations|Allow local language files to override bundled interface and help translations.||live
ColorStyle|appearance|Colors|Color theme|Select the application palette. Preview is reversible until Apply. Existing custom palette overrides are preserved on Cancel.||preview
EnforceColorCorrection|appearance|Colors|Correct low contrast|Adjust insufficient foreground and background contrast in supported palette and file-highlight colors. Box slots are excluded from the global correction pass.||live
GuiUseSystemMonospace|appearance|Font|Use system monospace font|Use the platform monospace font on supported Windows and macOS graphical frontends instead of the custom font.||restart
GuiFont|appearance|Font|Graphical font|Choose a font for supported graphical frontends. A custom font can be entered manually.||restart
GuiFontSize|appearance|Font|Font size|Set graphical font size. This does not change the font of an external terminal emulator.||restart
AlwaysShowMenuBar|appearance|Titles and menus|Always show menu bar|Keep the main menu bar visible instead of showing it only when activated.||live
ConsoleTitleTemplate|appearance|Titles and menus|Window title template|Format the host title with %State, %Ver, %Platform, %Backend, %Host, %User and %Admin.||live
DisplayFullPathInTitle|appearance|Titles and menus|Full file paths in titles|Show the complete file identity in editor and viewer title bars instead of only the basename.||live
StartupMode|startup|Launch defaults|Startup mode|Choose how plain f4 launches. Explicit --gui or --tty arguments override this default for that launch.|auto:Automatic;tty:Terminal;gui:Graphical|restart
GuiBackend|startup|Launch defaults|Graphical renderer|Default graphical renderer. Automatic uses startup detection. Command-line choices override this value; unknown external renderer IDs are preserved.||restart
TTYBackend|startup|Launch defaults|Terminal renderer|Default terminal renderer. Command-line renderer choices override this preference.|:Automatic;ansi:ANSI;winapi:Windows console|restart
WorkspaceTabMode|workspaces|Tab presentation|Show workspace tabs|Show tabs always, only with multiple workspaces, while Ctrl is held, or never.|0:Always;1:Multiple workspaces;2:While Ctrl is held;3:Never|live
WorkspaceTabsOverlay|workspaces|Tab presentation|Overlay workspace tabs|Draw workspace tabs over the content instead of reserving a layout row.||live
WorkspaceTabNumbering|workspaces|Tab presentation|Workspace numbering|Keep numbers permanently, keep them during this session, or renumber to match the current tab order.|0:Permanent;1:Session;2:Current order|live
CtrlTabShowsMenu|workspaces|Switching|Ctrl+Tab opens chooser|Open the workspace chooser with Ctrl+Tab instead of switching directly.||live
AltNumberSwitchesTabs|workspaces|Switching|Alt+number switches tabs|Reserve Alt+number shortcuts for workspace selection.||live
RestoreWorkspaceTabs|workspaces|Restoration|Restore workspace tabs|Recreate saved workspaces at startup instead of restoring only the initial workspace.||restart
SavePanelPaths|workspaces|Restoration|Save and restore panel paths|Save panel folders and focused files. Turning this off omits these values from subsequently saved workspace sessions.||next session save
AutoSaveDialogSettings|workspaces|Automatic saving|Save preferences automatically|Automatically persist configuration and dialog-related changes. Explicit Apply still saves when this is off.||live
AutoSavePanelSettings|workspaces|Automatic saving|Save panel layout automatically|Remember panel view modes, sorting, visibility, active workspace and workspace layout.||live
AutoSaveCurrentPanel|workspaces|Automatic saving|Save panel locations automatically|Remember locations and focused files for both sides of saved workspaces. Save and restore panel paths must also be enabled.||live
AutoSaveGUIWindow|workspaces|Automatic saving|Save graphical window automatically|Remember supported graphical-window dimensions and position, not external terminal geometry.||live
ShowHiddenFiles|panels|File listing|Show hidden files|Include hidden files and folders in the listing. The parent-directory entry remains visible.||live
ShowDirPrefix|panels|File listing|Prefix folder names|Prefix folder names with a slash, unless a highlight rule already supplies one.||live
ShowHighlightMarks|panels|File listing|Show highlight marks|Show markers from matching file-highlight rules. Also affects path suggestions; symlinks retain their fallback arrow.||live
SeparateFileExtensions|panels|File listing|Separate filename extensions|Align the final extension separately in the name column. Excludes folders, extensionless names and leading dots alone.||live
ShowPanelFileInfo|panels|File listing|Focused-file status row|Reserve a bottom row for the focused name, size and modification time. Short panels suppress this row.||live
PanelScrollbarMode|panels|File listing|Panel scrollbar|Hide the scrollbar, show a minimal one, or show the full scrollbar with arrows.|0:Off;1:Minimal;2:Full|live
SyncPanelLoad|panels|Directory loading|Wait for complete directory listing|Replace the listing only when all directory results are ready and bypass cached previews. Off permits incremental results. It does not block all UI work.||next directory load
InfoPanelCPUGPU|panels|Information panels|Show CPU and GPU information|Include locally collected CPU and GPU sections when the information provider does not supply authoritative information.||live
InfoPanelBytes|panels|Information panels|Show sizes in bytes|Display raw bytes instead of human-readable sizes in information and quick-view panels.||live
NavigationMode|panels|Typing and focus|Panel navigation|Classic types into the command line. Vim adds j/k and double dd/cc/mm actions. Search-first separates filename-search and command focus.|0:Classic;1:Vim;2:Search first|live
SearchCommandStayFocused|panels|Typing and focus|Keep command input focused|In Search-first mode, keep command entry focused after executing a command rather than returning to the panel.||live
CommandLineAutoComplete|terminal|Path suggestions|Enable filesystem suggestions|Enable filesystem path suggestions in the command line and path-enabled dialog fields.||live
DialogAutoComplete|terminal|Path suggestions|Show dialog completion dropdowns|Show completion while typing in fields with history or path suggestions. Does not add sources to arbitrary fields.||live
PathHintFullPath|terminal|Path suggestions|Show full suggestion paths|Display complete paths rather than only the final filename or folder component.||live
PathHintSource|terminal|Path suggestions|Suggestion source|Resolve suggestions from the active panel, passive panel, or both. Both searches active first.|0:Active panel;1:Passive panel;2:Both panels|live
PathHintTimeout|terminal|Path suggestions|Directory read timeout (seconds)|Maximum time allowed for a filesystem directory read behind suggestions. This is not a delay before suggestions appear. Minimum 1 second.||live
PathHintMaxVisible|terminal|Path suggestions|Maximum visible suggestions|Maximum number of visible suggestion rows. Minimum 1.||live
PathHintPerCategory|terminal|Path suggestions|Separate suggestion limits|Apply the visible-row limit separately to active-panel, passive-panel and history suggestions.||live
UseTrash|operations|Deletion|Use trash or recycle bin|Send ordinary Delete operations to trash where supported. Explicit permanent-delete commands still delete permanently.||new operations
DefaultFileOpMode|operations|Execution|Default operation mode|Start operations in Queue, Background or Foreground mode. Individual operation dialogs can override it.|0:Queue;1:Background;2:Foreground|new operations
FileOpPathDisplay|operations|Execution|Progress path display|Show the current name, full path, or source and destination paths in operation progress.|0:Name;1:Full path;2:Source and destination|live
ApplyCommandParallelism|operations|Execution|Concurrent Apply commands|Maximum concurrent commands in Apply command. Zero means unlimited. The initial default is the logical CPU count.||new operations
ConfirmCopy|operations|Confirmations|Confirm copy|Show the copy destination and options dialog before an ordinary copy.||new operations
ConfirmMove|operations|Confirmations|Confirm move|Show the move destination and options dialog before an ordinary move.||new operations
ConfirmDelete|operations|Confirmations|Confirm deletion|Ask before deleting selected items.||new operations
DeleteCancelFocused|operations|Confirmations|Initially focus Cancel for deletion|Place initial keyboard focus on Cancel in deletion confirmations.||new operations
ConfirmExit|startup|Exit|Confirm exit|Ask before exiting f4.||live
Compare.Recursive|operations|Folder comparison defaults|Compare subfolders|Walk subfolders instead of comparing only the two panel folders.||new comparisons
Compare.LimitDepth|operations|Folder comparison defaults|Limit recursion depth|Stop recursive comparison at the maximum depth below the panel folders.||new comparisons
Compare.MaxDepth|operations|Folder comparison defaults|Maximum comparison depth|Maximum folder depth when recursion limiting is enabled, from 1 to 99.||new comparisons
Compare.MarkedOnly|operations|Folder comparison defaults|Compare marked items only|Limit comparison to items marked in the panels.||new comparisons
Compare.ByTime|operations|Folder comparison defaults|Compare modification times|Report files as different when modification times differ beyond enabled tolerances.||new comparisons
Compare.TimeSlack|operations|Folder comparison defaults|Allow two-second time difference|Treat modification times up to two seconds apart as equal, accommodating FAT timestamp precision.||new comparisons
Compare.IgnoreZones|operations|Folder comparison defaults|Ignore timezone offsets|Ignore time differences in whole quarter hours up to 26 hours.||new comparisons
Compare.BySize|operations|Folder comparison defaults|Compare file sizes|Report files as different when their byte sizes differ.||new comparisons
Compare.ByContent|operations|Folder comparison defaults|Compare file contents|Read and compare file contents using the selected content normalization.||new comparisons
Compare.Ignore|operations|Folder comparison defaults|Normalize compared contents|Enable the selected whitespace or line-ending normalization before comparing contents.||new comparisons
Compare.IgnoreMode|operations|Folder comparison defaults|Content normalization|Normalize line endings or remove whitespace bytes before content comparison.|0:Line endings;1:All whitespace|new comparisons
Compare.ReportEqual|operations|Folder comparison defaults|Report equal folders|Show a message when comparison finds no differences.||new comparisons
EditorTabSize|editor|Text input|Tab width|Set tab-stop width for newly opened editors. Viewer text rendering also uses this setting.||new editors; viewer live
EditorExpandTabs|editor|Text input|Tab key inserts|Insert a literal tab or spaces to the next tab stop. Legacy New and All modes currently have identical insertion behavior; existing tabs are not converted.|0:Tab character;1:Spaces;2:Spaces (legacy All)|new editors
EditorAutoIndent|editor|Text input|Automatic indentation|Copy the current line's leading spaces and tabs when inserting a newline.||new editors
EditorCursorBeyondEOL|editor|Text input|Cursor beyond end of line|Allow virtual space after the line end. Inserting text pads the gap with spaces.||new editors
EditorUseEditorConfig|editor|Text input|Read adjacent .editorconfig|Apply matching indent_style, indent_size and tab_width from .editorconfig beside the opened file. Parent-directory discovery and the full standard are not implemented.||new editors
EditorAutoComplete|editor|Completion|Editor completion|Enable completion in newly opened named files whose basenames match the configured masks.||new editors
EditorAutoCompleteMask|editor|Completion|Completion filename masks|Semicolon-separated, case-insensitive filename patterns selecting files eligible for editor completion.||new editors
EditorCrosshair|editor|Visual aids|Show cursor crosshair|Draw cursor guide lines using the selected axis mode.||live
EditorCrossMode|editor|Visual aids|Crosshair axes|Select no guide, a vertical guide, a horizontal guide, or both. Colorer can supply guide colors but the behavior is editor-wide.|0:None;1:Vertical;2:Horizontal;3:Both|live
EditorMarkOccurrences|editor|Visual aids|Highlight selected-text occurrences|Highlight other exact occurrences of an ordinary single-line selection of 2-256 bytes containing non-whitespace. This does not automatically highlight the cursor word.||live
EditorAutodetectCodePage|editor|Text encoding|Detect editor encoding|Detect text encoding when opening a file in the editor.||new editors
EditorDefaultCodePage|editor|Text encoding|Default editor encoding|Fallback encoding when detection is disabled or cannot determine a better result. Per-file overrides remain separate.||new editors
ViewerAutodetectCodePage|editor|Text encoding|Detect viewer encoding|Detect encoding when opening the viewer or a quick-view preview.||new viewers
ViewerDefaultCodePage|editor|Text encoding|Default viewer encoding|Fallback viewer and quick-view encoding when detection is disabled or inconclusive.||new viewers
UseExternalEditor|editor|External editor|Use external editor|Route ordinary Edit commands to an external editor. Remote files are temporarily downloaded and changes can be uploaded afterward.||new edit commands
ExternalEditorConsole|editor|External editor|Console editor command|Command for terminal sessions; the file path is appended as the last argument. Current parsing splits on whitespace and is not a shell-expression parser.||new edit commands
ExternalEditorGUI|editor|External editor|Graphical editor command|Command for graphical sessions; the file path is appended as the last argument. Current parsing splits on whitespace and is not a shell-expression parser.||new edit commands
EditorHighlighter|syntax|Engine|Syntax highlighter|Choose Chroma, Colorer or no highlighting for newly opened editors. Colorer needs installed schemas and may fall back when unavailable.|Chroma:Chroma;Colorer:Colorer;None:None|new editors
EditorSyntaxAnimation|syntax|Engine|Animate syntax colors|Fade arriving RGB syntax colors over about 400 milliseconds. Indexed colors are unchanged.||live
EditorColorerScheme|syntax|Colorer|Colorer scheme|Select the HRD scheme used for syntax, guide and editor base colors. Empty uses the built-in default.||Colorer reload
EditorColorerSyntax|syntax|Colorer|Colorer syntax colors|Enable Colorer syntax coloring while retaining its other style facilities.||Colorer reload
EditorColorerBackground|syntax|Colorer|Use Colorer base colors|Use foreground and background fields supplied by the Colorer scheme instead of only the general editor palette.||Colorer reload
EditorColorerCatalog|syntax|Colorer|Colorer configuration directory|Directory containing Colorer configuration data. Empty uses the profile's colorer/configs directory; this is not a catalog XML filename.||Colorer reload
MacKeyboard|keyboard|Editing chords|Mac keyboard mode|Use Mac-style editing chords in editors and dialog fields. Auto enables them on macOS; panels retain Far navigation. Command translation requires backend support.|auto:Automatic;on:On;off:Off|live
SearchExactOnHit|keyboard|Editing chords|Prefer exact shortcut search matches|In the Hotkey Configurator table, narrow the search to exact matches when available. This does not affect the Settings Center sidebar search.||next open
EscTogglePanels|terminal|Terminal input|Escape toggles panels|Allow Escape to show or hide the file panels to access the terminal.||live
TerminalCtrlNWorkspace|terminal|Terminal input|Ctrl+N creates workspace|Reserve Ctrl+N while terminal input has focus to create a workspace. Off sends the chord to the terminal program.||live
KeepTerminalCursor|terminal|Terminal input|Preserve terminal cursor style|Leave cursor-style management to the terminal instead of letting f4 change it.||live
ConsoleMode|terminal|Presentation|Terminal presentation|Use the embedded terminal, host terminal with f4 overlay, or host terminal without overlay. Unsupported host styles fall back according to terminal capabilities.|own:Embedded;far:Host with overlay;mc:Host without overlay|new workspaces
HistoryDirsPrefixLen|history|Presentation|Command directory column width|Width of the command-history directory prefix while date-and-time display is active. Minimum four characters.||new history dialogs
MacroRecordFormat|menus|Macro recording|Record macros as|Save recorded key macros in the legacy key_macros.ini format or as Lua scripts under Macros/scripts.|0:Legacy INI;1:Lua scripts|new recordings
ProxyMode|network|Global proxy|Proxy mode|Use system/environment proxy settings, direct connections, or an HTTP/SOCKS5 proxy for downloads and network providers unless overridden per connection.|1:System;2:Direct;3:HTTP;4:SOCKS5|new requests
ProxyHost|network|Global proxy|Proxy host|Hostname or address of the configured HTTP or SOCKS5 proxy.||new requests
ProxyPort|network|Global proxy|Proxy port|Port of the configured HTTP or SOCKS5 proxy.||new requests
ProxyUser|network|Global proxy|Proxy username|Username used to authenticate to the configured proxy.||new requests
ProxyPass|network|Global proxy|Proxy password|Password used to authenticate to the configured proxy. Its value is excluded from search.||new requests
UpdateChannel|updates|Automatic checks|Update channel|Check stable releases or nightly builds.|0:Stable;1:Nightly|next check
UpdateInterval|updates|Automatic checks|Check for updates at startup|Check never, every start, after at least 24 hours, or after at least seven days. Checks run at startup, not on a continuous timer.|0:Never;1:Every start;2:Daily;3:Weekly|next launch
`

func coreSettingsFields() []f4settings.Field {
	var fields []f4settings.Field
	for _, line := range strings.Split(strings.TrimSpace(coreSettingsDefinitions), "\n") {
		p := strings.Split(line, "|")
		f := f4settings.Field{ID: p[0], Category: p[1], Group: p[2], Label: f4settings.Text{English: p[3], Key: "SettingsCenter." + p[0] + ".Label"}, Description: f4settings.Text{English: p[4], Key: "SettingsCenter." + p[0] + ".Description"}, Timing: p[6], Aliases: []string{p[0]}}
		v := settingsConfigField(reflect.ValueOf(config.App), f.ID)
		switch v.Kind() {
		case reflect.Bool:
			f.Kind = f4settings.Boolean
		case reflect.Int:
			f.Kind = f4settings.Integer
		default:
			f.Kind = f4settings.String
		}
		if p[5] != "" {
			f.Kind = f4settings.ChoiceKind
			for _, item := range strings.Split(p[5], ";") {
				pair := strings.SplitN(item, ":", 2)
				f.Choices = append(f.Choices, f4settings.Choice{Value: pair[0], Label: f4settings.Text{English: pair[1]}})
			}
		}
		if f.ID == "ProxyPass" {
			f.Kind = f4settings.Secret
		}
		if f.ID == "ProxyPort" {
			f.InputWidth = 6
		}
		if f.ID == "EditorColorerCatalog" {
			f.Kind = f4settings.Path
		}
		if f.Timing == "unavailable" {
			f.Unavailable = f.Description.English
		}
		fields = append(fields, f)
	}
	return fields
}

func init() {
	for i := range Categories {
		Categories[i].Label.Key = "SettingsCenter.Category." + Categories[i].ID
	}
}
