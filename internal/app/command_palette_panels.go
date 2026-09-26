package app

import (
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"strings"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/vtui"
)

func commandPalettePanelsContextEntries(pf *panel.PanelsFrame) []commandPaletteEntry {
	if pf == nil || pf.Closed || !pf.ShowPanels {
		return nil
	}
	category := action.PlainLabel(i18n.Msg("Help.Area.Shell"))
	entries := []commandPaletteEntry{
		commandPaletteLocalizedPanelKeyEntry(pf, "Panel.ActivateSelected", "CommandPalette.Panel.ActivateSelected", "Activate selected item", "CommandPalette.Panel.ActivateSelected.Desc", "Open the selected item or execute it", "Enter", "Enter", category, nil, "Menu.Files.View"),
		commandPaletteLocalizedPanelKeyEntry(pf, "Panel.SwitchActive", "CommandPalette.Panel.SwitchActive", "Switch active panel", "CommandPalette.Panel.SwitchActive.Desc", "Move focus to the other panel", "Tab", "Tab", category, nil, "Panel.Other"),
	}
	if pf.GetActivePanel() != nil {
		entries = append(entries, commandPaletteLocalizedPanelKeyEntry(pf, "Panel.ToggleSelection", "CommandPalette.Panel.ToggleSelection", "Toggle item selection", "CommandPalette.Panel.ToggleSelection.Desc", "Toggle selection of the current item and advance", "Ins", "Ins", category, nil, "Help.PanelNav"))
	}
	if pnl := pf.GetActivePanel(); pnl != nil {
		if task := pnl.ProviderOpenTask; task != nil {
			entries = append(entries, commandPaletteLocalizedPanelKeyEntry(
				pf,
				"Provider.CancelOpen",
				"CommandPalette.Provider.CancelOpen",
				"Cancel opening provider",
				"CommandPalette.Provider.CancelOpen.Desc",
				"Cancel the pending provider connection and restore the source panel",
				"Esc",
				"Esc",
				category,
				func() bool { return pf.GetActivePanel() == pnl && pnl.ProviderOpenTask == task },
				"Provider.Opening",
			))
		}
		if pnl.FastFindMode {
			entry := commandPaletteLocalizedPanelKeyEntry(
				pf,
				"FastFind.ToggleMatchMode",
				"CommandPalette.FastFind.ToggleMatchMode",
				"Toggle Fast Find match mode",
				"CommandPalette.FastFind.ToggleMatchMode.Desc",
				"Switch Fast Find between prefix matching and matching anywhere in the name",
				"F2",
				"F2",
				category,
				func() bool { return pf.GetActivePanel() == pnl && pnl.FastFindMode },
				"Help.FastFind",
			)
			entry.Checked = strings.HasPrefix(pnl.FastFindStr, "*")
			entries = append(entries, entry)
		}
	}

	if pf.SearchFirstMode() {
		commandLineFocused := pf.CommandLineFocused
		entry := commandPaletteLocalizedPanelKeyEntry(
			pf,
			"Panel.ToggleCommandLineFocus",
			"CommandPalette.Panel.ToggleCommandLineFocus",
			"Toggle command-line focus",
			"CommandPalette.Panel.ToggleCommandLineFocus.Desc",
			"Switch input focus between the active file panel and the command line",
			"` / ~ / ё",
			"`",
			category,
			func() bool {
				return pf.ShowPanels && pf.SearchFirstMode() && pf.CommandLineFocused == commandLineFocused
			},
			"Config.NavigationMode.SearchFirst",
		)
		entry.Checked = commandLineFocused
		entries = append(entries, entry)
	}

	if target := pf.CurrentRemotePTYInterruptTarget(); target != nil {
		entries = append(entries, commandPaletteLocalizedPanelKeyEntry(
			pf,
			"Panel.InterruptRemoteCommand",
			"CommandPalette.Panel.InterruptRemoteCommand",
			"Interrupt remote command",
			"CommandPalette.Panel.InterruptRemoteCommand.Desc",
			"Send the remote shell's interrupt sequence to the active panel term.PTY",
			"Ctrl+C",
			"CtrlC",
			category,
			func() bool { return target.Matches(pf.CurrentRemotePTYInterruptTarget()) },
			"Help.Terminal",
		))
	}

	if pf.ActiveIdx >= 0 && pf.ActiveIdx < len(pf.AltPanels) {
		switch pnl := pf.AltPanels[pf.ActiveIdx].(type) {
		case *panel.InfoPanel:
			if pnl != nil && pnl.IsFocused() {
				entries = append(entries, commandPaletteLocalizedPanelKeyEntry(
					pf,
					"InfoPanel.CopyCurrent",
					"CommandPalette.Info.CopyCurrent",
					"Copy current information value",
					"CommandPalette.Info.CopyCurrent.Desc",
					"Copy the focused information value or selected rows to the clipboard",
					"C",
					"C",
					action.PlainLabel(i18n.Msg("InfoPanel.Title")),
					func() bool { return commandPaletteInfoPanelFocused(pf, pnl) },
					"InfoPanel.Title",
				))
			}
		case *panel.QuickViewPanel:
			if pnl != nil && pnl.IsFocused() {
				labelKey := "KeyBar.F2Wrap"
				english := "Enable wrapping in Quick View"
				if pnl.Wrap {
					labelKey = "KeyBar.F2Unwrap"
					english = "Disable wrapping in Quick View"
				}
				entry := commandPaletteLocalizedPanelKeyEntry(
					pf,
					"QuickView.ToggleWrap",
					labelKey,
					english,
					"CommandPalette.QuickView.ToggleWrap.Desc",
					"Toggle long-line wrapping in Quick View",
					"F2",
					"F2",
					action.PlainLabel(i18n.Msg("QuickView.Title")),
					func() bool { return commandPaletteQuickViewPanelFocused(pf, pnl) },
					"QuickView.Title",
				)
				entry.Checked = pnl.Wrap
				entries = append(entries, entry)
			}
		case *AIChatPanel:
			if pnl != nil && pnl.IsFocused() {
				entries = append(entries, commandPaletteLocalizedPanelKeyEntry(
					pf,
					"AI.CopyLastResponse",
					"CommandPalette.AI.CopyLastResponse",
					"Copy last AI response",
					"CommandPalette.AI.CopyLastResponse.Desc",
					"Copy the latest assistant response to the clipboard",
					"Right Ctrl+C",
					"RCtrlC",
					action.PlainLabel(i18n.Msg("Action.AI.ViewChat")),
					func() bool { return commandPaletteAIChatPanelFocused(pf, pnl) },
					"Action.AI.ViewChat",
				))
				barKind := aiBarNone
				if pnl.focusedLinkIdx == -2 {
					barKind = pnl.barKind()
				}
				entries = append(entries, commandPaletteAIChatFocusedEntries(pf, pnl, barKind)...)
			}
		}
	}

	entries = append(entries, commandPaletteBookmarkEntries(pf)...)
	return entries
}

func commandPaletteLocalizedPanelKeyEntry(
	pf *panel.PanelsFrame,
	id, labelKey, englishLabel, descKey, englishDescription, shortcut, key, category string,
	valid func() bool,
	aliasKeys ...string,
) commandPaletteEntry {
	description := i18n.Msg(descKey)
	if description == "" || strings.HasPrefix(description, "{") {
		description = englishDescription
	}
	entry := commandPalettePanelKeyEntry(
		pf, id, labelKey, englishLabel, description, shortcut, key, category,
		append(aliasKeys, descKey)...,
	)
	entry.EnglishDescription = englishDescription
	entry.run = func() bool {
		if vtui.FrameManager == nil || vtui.FrameManager.GetTopFrame() != pf || pf.Closed {
			return false
		}
		if valid != nil && !valid() {
			return false
		}
		return pf.ProcessKey(keymap.ParseFarKey(key))
	}
	return entry
}

func commandPaletteInfoPanelFocused(pf *panel.PanelsFrame, pnl *panel.InfoPanel) bool {
	if pf == nil || pnl == nil || pf.ActiveIdx < 0 || pf.ActiveIdx >= len(pf.AltPanels) {
		return false
	}
	current, ok := pf.AltPanels[pf.ActiveIdx].(*panel.InfoPanel)
	return ok && current == pnl && pnl.IsFocused()
}

func commandPaletteQuickViewPanelFocused(pf *panel.PanelsFrame, pnl *panel.QuickViewPanel) bool {
	if pf == nil || pnl == nil || pf.ActiveIdx < 0 || pf.ActiveIdx >= len(pf.AltPanels) {
		return false
	}
	current, ok := pf.AltPanels[pf.ActiveIdx].(*panel.QuickViewPanel)
	return ok && current == pnl && pnl.IsFocused()
}

func commandPaletteAIChatPanelFocused(pf *panel.PanelsFrame, pnl *AIChatPanel) bool {
	if pf == nil || pnl == nil || pf.ActiveIdx < 0 || pf.ActiveIdx >= len(pf.AltPanels) {
		return false
	}
	current, ok := pf.AltPanels[pf.ActiveIdx].(*AIChatPanel)
	return ok && current == pnl && pnl.IsFocused()
}

// commandPaletteAIChatFocusedEntries exposes the keys owned by the currently
// focused response link or strip. barKind is passed in so discovery remains a
// pure snapshot; every callback revalidates the live focus and target before
// routing the key back through AIChatPanel.ProcessKey.
func commandPaletteAIChatFocusedEntries(pf *panel.PanelsFrame, pnl *AIChatPanel, barKind int) []commandPaletteEntry {
	if !commandPaletteAIChatPanelFocused(pf, pnl) {
		return nil
	}
	category := action.PlainLabel(i18n.Msg("Action.AI.ViewChat"))
	if pnl.focusedLinkIdx == -1 {
		draft := pnl.input.GetText()
		if strings.TrimSpace(draft) == "" {
			return nil
		}
		return []commandPaletteEntry{commandPaletteLocalizedPanelKeyEntry(
			pf,
			"AI.SendDraft",
			"CommandPalette.AI.SendDraft",
			"Send AI message",
			"CommandPalette.AI.SendDraft.Desc",
			"Send the current AI chat draft",
			"Enter",
			"Enter",
			category,
			func() bool {
				return commandPaletteAIChatPanelFocused(pf, pnl) &&
					pnl.focusedLinkIdx == -1 && pnl.input.GetText() == draft
			},
			"AI.InputLabel",
		)}
	}
	if pnl.focusedLinkIdx == -2 {
		if barKind == aiBarNone {
			return nil
		}
		labelKey := "CommandPalette.AI.OpenContextBar"
		englishLabel := "Open attached AI context"
		descKey := "CommandPalette.AI.OpenContextBar.Desc"
		englishDescription := "Open the attached context files shown in the focused AI bar"
		aliasKeys := []string{"Action.AI.ViewContext"}
		if barKind == aiBarPatch {
			labelKey = "Action.AI.ApplyPatch"
			englishLabel = "Apply AI patch"
			descKey = "Action.AI.ApplyPatch.Desc"
			englishDescription = "Apply the patch shown in the focused AI bar"
			aliasKeys = []string{"AI.ApplyPatchBar"}
		}
		barStillFocused := func() bool {
			return commandPaletteAIChatPanelFocused(pf, pnl) &&
				pnl.focusedLinkIdx == -2 && pnl.barKind() == barKind
		}
		entries := []commandPaletteEntry{commandPaletteLocalizedPanelKeyEntry(
			pf,
			"AI.ActivateFocusedBar",
			labelKey,
			englishLabel,
			descKey,
			englishDescription,
			"Enter",
			"Enter",
			category,
			barStillFocused,
			aliasKeys...,
		)}
		if barKind == aiBarPatch {
			entries = append(entries, commandPaletteLocalizedPanelKeyEntry(
				pf,
				"AI.InspectFocusedPatch",
				"CommandPalette.AI.InspectPatch",
				"Inspect AI patch",
				"CommandPalette.AI.InspectPatch.Desc",
				"Open the focused patch in the viewer before applying it",
				"F3",
				"F3",
				category,
				barStillFocused,
				"AI.PatchTitle",
			))
		}
		return entries
	}

	linkIndex := pnl.focusedLinkIdx
	if linkIndex < 0 || linkIndex >= len(pnl.visibleLinks) {
		return nil
	}
	linkTarget := pnl.visibleLinks[linkIndex].target
	linkStillFocused := func() bool {
		return commandPaletteAIChatPanelFocused(pf, pnl) &&
			pnl.focusedLinkIdx == linkIndex && linkIndex < len(pnl.visibleLinks) &&
			pnl.visibleLinks[linkIndex].target == linkTarget
	}
	return []commandPaletteEntry{
		commandPaletteLocalizedPanelKeyEntry(
			pf,
			"AI.OpenFocusedLink",
			"CommandPalette.AI.OpenFocusedLink",
			"Open focused AI link",
			"CommandPalette.AI.OpenFocusedLink.Desc",
			"Open the file or AI view referenced by the focused response link",
			"Enter",
			"Enter",
			category,
			linkStillFocused,
			"Action.AI.ViewOut",
		),
		commandPaletteLocalizedPanelKeyEntry(
			pf,
			"AI.CopyFocusedLinkTarget",
			"CommandPalette.AI.CopyFocusedLinkTarget",
			"Copy focused AI link target",
			"CommandPalette.AI.CopyFocusedLinkTarget.Desc",
			"Copy the file referenced by the focused response link to the other panel",
			"F5",
			"F5",
			category,
			linkStillFocused,
			"Menu.Files.Copy",
		),
	}
}

func commandPalettePanelKeyEntry(pf *panel.PanelsFrame, id, labelKey, englishLabel, description, shortcut, key, category string, aliasKeys ...string) commandPaletteEntry {
	label := i18n.Msg(labelKey)
	if label == "" || strings.HasPrefix(label, "{") {
		label = englishLabel
	}
	translationKeys := append([]string{labelKey}, aliasKeys...)
	return commandPaletteEntry{
		Key:                "panel-context:" + strings.ToLower(id),
		Label:              action.PlainLabel(label),
		EnglishLabel:       englishLabel,
		Description:        description,
		EnglishDescription: description,
		ID:                 id,
		Category:           category,
		Shortcut:           shortcut,
		SearchFields:       commandPaletteTranslations(translationKeys...),
		run: func() bool {
			if vtui.FrameManager == nil || vtui.FrameManager.GetTopFrame() != pf || pf.Closed {
				return false
			}
			return pf.ProcessKey(keymap.ParseFarKey(key))
		},
	}
}

func commandPaletteBookmarkEntries(pf *panel.PanelsFrame) []commandPaletteEntry {
	category := action.PlainLabel(i18n.Msg("Menu.Commands.Bookmarks"))
	aliases := commandPaletteTranslations("Menu.Commands.Bookmarks", "Action.Panel.Bookmarks.Desc")
	bookmarks, _ := panel.LoadBookmarks(panel.BookmarksFilePath())
	entries := make([]commandPaletteEntry, 0, 21)
	for slot := range bookmarks {
		slot := slot
		if strings.TrimSpace(bookmarks[slot].Path) != "" {
			path := bookmarks[slot].Path
			entries = append(entries, commandPaletteEntry{
				Key:                fmt.Sprintf("bookmark:goto:%d", slot),
				Label:              fmt.Sprintf(i18n.Msg("CommandPalette.Bookmark.GoTo"), slot, path),
				EnglishLabel:       fmt.Sprintf("Go to bookmark %d: %s", slot, path),
				Description:        path,
				EnglishDescription: path,
				ID:                 fmt.Sprintf("Bookmark.GoTo.%d", slot),
				Category:           category,
				Shortcut:           fmt.Sprintf("Right Ctrl+%d, Ctrl+Alt+%d", slot, slot),
				SearchFields:       append([]string{path}, aliases...),
				run: func() bool {
					return runCommandPaletteBookmarkGoto(pf, slot)
				},
			})
		}
		entries = append(entries, commandPaletteEntry{
			Key:                fmt.Sprintf("bookmark:save:%d", slot),
			Label:              fmt.Sprintf(i18n.Msg("CommandPalette.Bookmark.Save"), slot),
			EnglishLabel:       fmt.Sprintf("Save current folder to bookmark %d", slot),
			Description:        i18n.Msg("CommandPalette.Bookmark.Save.Desc"),
			EnglishDescription: "Store the active panel folder in this bookmark slot",
			ID:                 fmt.Sprintf("Bookmark.Save.%d", slot),
			Category:           category,
			Shortcut:           fmt.Sprintf("Right Ctrl+Shift+%d, Ctrl+Alt+Shift+%d", slot, slot),
			SearchFields: append(append([]string(nil), aliases...), commandPaletteTranslations(
				"CommandPalette.Bookmark.Save", "CommandPalette.Bookmark.Save.Desc",
			)...),
			run: func() bool {
				return runCommandPaletteBookmarkSave(pf, slot)
			},
		})
	}
	entries = append(entries, commandPaletteEntry{
		Key:                "bookmark:home",
		Label:              i18n.Msg("CommandPalette.Bookmark.Home"),
		EnglishLabel:       "Go to home folder",
		Description:        i18n.Msg("CommandPalette.Bookmark.Home.Desc"),
		EnglishDescription: "Open the home folder in the active panel",
		ID:                 "Bookmark.Home",
		Category:           category,
		Shortcut:           "Right Ctrl+`, Ctrl+Alt+`",
		SearchFields: append(append([]string(nil), aliases...), commandPaletteTranslations(
			"CommandPalette.Bookmark.Home", "CommandPalette.Bookmark.Home.Desc",
		)...),
		run: func() bool {
			if !commandPaletteBookmarkFrameActive(pf) {
				return false
			}
			home, _ := hostmode.UserHomeDir()
			fsp := pf.GetActivePanel()
			if home == "" || fsp == nil {
				return false
			}
			pf.NavigateToPath(fsp, home)
			return true
		},
	})
	return entries
}

func runCommandPaletteBookmarkGoto(pf *panel.PanelsFrame, slot int) bool {
	if !commandPaletteBookmarkFrameActive(pf) || slot < 0 || slot > 9 {
		return false
	}
	bookmarks, err := panel.LoadBookmarks(panel.BookmarksFilePath())
	fsp := pf.GetActivePanel()
	if err != nil || fsp == nil || strings.TrimSpace(bookmarks[slot].Path) == "" {
		return false
	}
	pf.NavigateToBookmark(fsp, bookmarks[slot])
	return true
}

func runCommandPaletteBookmarkSave(pf *panel.PanelsFrame, slot int) bool {
	if !commandPaletteBookmarkFrameActive(pf) || slot < 0 || slot > 9 {
		return false
	}
	fsp := pf.GetActivePanel()
	if fsp == nil || fsp.Vfs == nil {
		return false
	}
	path := panel.BookmarksFilePath()
	bookmarks, err := panel.LoadBookmarks(path)
	if err != nil {
		return false
	}
	bookmarks[slot] = panel.Bookmark{Path: fsp.Vfs.GetPath()}
	return panel.SaveBookmarks(path, bookmarks) == nil
}

func commandPaletteBookmarkFrameActive(pf *panel.PanelsFrame) bool {
	return pf != nil && !pf.Closed && vtui.FrameManager != nil && vtui.FrameManager.GetTopFrame() == pf
}
