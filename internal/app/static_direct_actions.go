package app

import (
	"fmt"
	"strings"

	"github.com/unxed/f4/internal/panel"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/vtui"
)

type fixedPanelSideActionSpec struct {
	id       string
	menuPath string
	index    int
}

type fixedPanelViewActionSpec struct {
	id       string
	label    string
	labelKey string
	descKey  string
	mode     panel.ViewMode
}

type fixedPanelSortActionSpec struct {
	id       string
	label    string
	labelKey string
	descKey  string
	mode     panel.SortMode
}

type fixedAIViewActionSpec struct {
	id          string
	label       string
	labelKey    string
	description string
	descKey     string
	path        string
	isChat      bool
}

var fixedPanelSideActionSpecs = []fixedPanelSideActionSpec{
	{id: "Left", menuPath: "Left", index: 0},
	{id: "Right", menuPath: "Right", index: 1},
}

var fixedPanelViewActionSpecs = []fixedPanelViewActionSpec{
	{id: "ViewBrief", label: "Brief", labelKey: "Menu.Left.Brief", descKey: "Action.Panel.ViewBrief.Desc", mode: panel.ViewModeBrief},
	{id: "ViewMedium", label: "Medium", labelKey: "Menu.Left.Medium", descKey: "Action.Panel.ViewMedium.Desc", mode: panel.ViewModeMedium},
	{id: "ViewDetailed", label: "Detailed", labelKey: "Menu.Left.Detailed", descKey: "Action.Panel.ViewDetailed.Desc", mode: panel.ViewModeDetailed},
	{id: "ViewWide", label: "Wide", labelKey: "Menu.Left.Wide", descKey: "Action.Panel.ViewWide.Desc", mode: panel.ViewModeWide},
}

var fixedPanelSortActionSpecs = []fixedPanelSortActionSpec{
	{id: "SortByName", label: "Name", labelKey: "Menu.SortName", descKey: "Action.Panel.SortByName.Desc", mode: panel.SortName},
	{id: "SortByExt", label: "Extension", labelKey: "Menu.SortExt", descKey: "Action.Panel.SortByExt.Desc", mode: panel.SortExt},
	{id: "SortByTime", label: "Modification Time", labelKey: "Menu.SortTime", descKey: "Action.Panel.SortByTime.Desc", mode: panel.SortTime},
	{id: "SortBySize", label: "Size", labelKey: "Menu.SortSize", descKey: "Action.Panel.SortBySize.Desc", mode: panel.SortSize},
	{id: "SortUnsorted", label: "Unsorted", labelKey: "Menu.SortUnsorted", descKey: "Action.Panel.SortUnsorted.Desc", mode: panel.SortUnsorted},
}

var fixedAIViewActionSpecs = []fixedAIViewActionSpec{
	{id: "ViewContext", label: "AI View: Context", labelKey: "Action.AI.ViewContext", description: "context view", descKey: "Action.AI.ViewContext.Desc", path: "ai://ctx"},
	{id: "ViewChat", label: "AI View: Chat", labelKey: "Action.AI.ViewChat", description: "chat view", descKey: "Action.AI.ViewChat.Desc", path: "ai://chat", isChat: true},
	{id: "ViewOut", label: "AI View: Artifacts", labelKey: "Action.AI.ViewOut", description: "artifacts view", descKey: "Action.AI.ViewOut.Desc", path: "ai://out"},
	{id: "ViewMem", label: "AI View: Memory", labelKey: "Action.AI.ViewMem", description: "memory view", descKey: "Action.AI.ViewMem.Desc", path: "ai://mem"},
}

func fixedRegularPanel(index int) (*panel.PanelsFrame, *panel.FileSystemPanel, bool) {
	pf := panel.FindPanelsFrameAnyScreen()
	if pf == nil || index < 0 || index >= len(pf.Panels) || panel.IsAIPanel(pf.Panels[index]) {
		return nil, nil, false
	}
	fsp, ok := pf.Panels[index].(*panel.FileSystemPanel)
	if !ok || fsp == nil {
		return nil, nil, false
	}
	return pf, fsp, true
}

func fixedPanelViewChecked(index int, mode panel.ViewMode) bool {
	pf, fsp, ok := fixedRegularPanel(index)
	if !ok {
		return false
	}
	if mode == panel.ViewModeWide {
		return pf.Wide && pf.WidePanel == index
	}
	return (!pf.Wide || pf.WidePanel != index) && fsp.ViewMode == mode
}

func fixedPanelSortChecked(index int, mode panel.SortMode) bool {
	_, fsp, ok := fixedRegularPanel(index)
	return ok && fsp.SortMode == mode
}

func fixedPanelSortGroupsChecked(index int) bool {
	_, fsp, ok := fixedRegularPanel(index)
	return ok && fsp.UseSortGroups
}

func runFixedPanelSortGroups(index int) bool {
	pf, fsp, ok := fixedRegularPanel(index)
	if !ok {
		return false
	}
	fsp.ToggleSortGroups()
	pf.UpdateMenuCheckmarks()
	return true
}

func fixedPanelSortNumericChecked(index int) bool {
	_, fsp, ok := fixedRegularPanel(index)
	return ok && fsp.SortNumeric
}

func runFixedPanelSortNumeric(index int) bool {
	pf, fsp, ok := fixedRegularPanel(index)
	if !ok {
		return false
	}
	fsp.ToggleSortNumeric()
	pf.UpdateMenuCheckmarks()
	return true
}

func runFixedPanelView(index int, mode panel.ViewMode) bool {
	pf, _, ok := fixedRegularPanel(index)
	if !ok {
		return false
	}
	pf.SetPanelViewMode(index, mode)
	return true
}

func runFixedPanelSort(index int, mode panel.SortMode) bool {
	pf, fsp, ok := fixedRegularPanel(index)
	if !ok {
		return false
	}
	fsp.SetSortMode(mode)
	pf.UpdateMenuCheckmarks()
	return true
}

func fixedAIPanelVisible(index int) bool {
	pf := panel.FindPanelsFrameAnyScreen()
	return pf != nil && index >= 0 && index < len(pf.Panels) && panel.IsAIPanel(pf.Panels[index])
}

func runFixedAIView(index int, path string, isChat bool) bool {
	pf := panel.FindPanelsFrameAnyScreen()
	if pf == nil || index < 0 || index >= len(pf.Panels) || !panel.IsAIPanel(pf.Panels[index]) {
		return false
	}
	fsp, ok := pf.Panels[index].(*panel.FileSystemPanel)
	if !ok || fsp == nil {
		return false
	}
	fsp.AiSetViewMode(path, isChat)
	return true
}

func actionBackground() bool {
	if !terminal.SupportsBackgrounding() {
		vtui.ShowMessage(" Background ", "Backgrounding is not supported on this OS.", []string{"&Ok"})
		return true
	}
	if vtui.FrameManager == nil {
		return false
	}
	vtui.FrameManager.Stop()
	return true
}

func arkanoidActionVisible() bool {
	if vtui.FrameManager == nil {
		return false
	}
	_, ok := vtui.FrameManager.GetTopFrame().(*panel.PanelsFrame)
	return ok
}

func actionArkanoid() bool {
	if vtui.FrameManager == nil {
		return false
	}
	for index, screen := range vtui.FrameManager.Screens {
		if screen == nil {
			continue
		}
		for _, frame := range screen.Frames {
			if frame != nil && frame.GetTitle() == "Arkanoid" {
				vtui.FrameManager.SwitchScreen(index)
				return true
			}
		}
	}
	vtui.FrameManager.AddScreenHeadless(NewArkanoidFrame())
	return true
}

func actionViewerGoTo() bool {
	if vtui.FrameManager == nil {
		return false
	}
	vv, ok := vtui.FrameManager.GetTopFrame().(*viewer.ViewerView)
	if !ok || vv == nil {
		return false
	}
	vv.AskGoto()
	return true
}

func actionEditorGoTo() bool {
	if vtui.FrameManager == nil {
		return false
	}
	ev, ok := vtui.FrameManager.GetTopFrame().(*editor.EditorView)
	if !ok || ev == nil {
		return false
	}
	ev.AskGoto()
	return true
}

func init() {
	for _, side := range fixedPanelSideActionSpecs {
		registerAction(action.Action{Name: "Panel." + side.id + ".GroupMenu", Area: "Shell", Label: "Group by...", LabelKey: "Group.Menu", Description: "Show panel grouping modes", DescKey: "Group.Menu.Desc", MenuPath: side.menuPath, HideFromMenu: true, Handler: func() bool {
			_, fp, ok := fixedRegularPanel(side.index)
			if ok {
				fp.ShowGroupMenu()
			}
			return ok
		}})
	}

	for _, side := range fixedPanelSideActionSpecs {
		side := side
		for _, view := range fixedPanelViewActionSpecs {
			view := view
			registerAction(action.Action{
				Name:         "Panel." + side.id + "." + view.id,
				Area:         "Shell",
				Label:        view.label,
				LabelKey:     view.labelKey,
				Description:  fmt.Sprintf("Set the %s panel to %s mode", strings.ToLower(side.id), strings.ToLower(view.label)),
				DescKey:      view.descKey,
				MenuPath:     side.menuPath,
				HideFromMenu: true,
				Visible: func() bool {
					_, _, ok := fixedRegularPanel(side.index)
					return ok
				},
				Checked: func() bool { return fixedPanelViewChecked(side.index, view.mode) },
				Handler: func() bool { return runFixedPanelView(side.index, view.mode) },
			})
		}

		for _, sortMode := range fixedPanelSortActionSpecs {
			sortMode := sortMode
			description := fmt.Sprintf("Sort the %s panel by %s", strings.ToLower(side.id), strings.ToLower(sortMode.label))
			if sortMode.mode == panel.SortUnsorted {
				description = fmt.Sprintf("Disable sorting for the %s panel", strings.ToLower(side.id))
			}
			registerAction(action.Action{
				Name:         "Panel." + side.id + "." + sortMode.id,
				Area:         "Shell",
				Label:        sortMode.label,
				LabelKey:     sortMode.labelKey,
				Description:  description,
				DescKey:      sortMode.descKey,
				MenuPath:     side.menuPath,
				HideFromMenu: true,
				Visible: func() bool {
					_, _, ok := fixedRegularPanel(side.index)
					return ok
				},
				Checked: func() bool { return fixedPanelSortChecked(side.index, sortMode.mode) },
				Handler: func() bool { return runFixedPanelSort(side.index, sortMode.mode) },
			})
		}

		registerAction(action.Action{
			Name:         "Panel." + side.id + ".SortUseGroups",
			Area:         "Shell",
			Label:        "Use Sort Groups",
			LabelKey:     "Menu.SortUseGroups",
			Description:  fmt.Sprintf("Group the %s panel by the configured sort groups", strings.ToLower(side.id)),
			DescKey:      "Action.Panel.SortUseGroups.Desc",
			MenuPath:     side.menuPath,
			HideFromMenu: true,
			Visible: func() bool {
				_, _, ok := fixedRegularPanel(side.index)
				return ok
			},
			Checked: func() bool { return fixedPanelSortGroupsChecked(side.index) },
			Handler: func() bool { return runFixedPanelSortGroups(side.index) },
		})

		registerAction(action.Action{
			Name:         "Panel." + side.id + ".SortNumeric",
			Area:         "Shell",
			Label:        "Numeric Sort",
			LabelKey:     "Menu.SortNumeric",
			Description:  fmt.Sprintf("Sort the %s panel by treating digit runs in names as numbers", strings.ToLower(side.id)),
			DescKey:      "Action.Panel.SortNumeric.Desc",
			MenuPath:     side.menuPath,
			HideFromMenu: true,
			Visible: func() bool {
				_, _, ok := fixedRegularPanel(side.index)
				return ok
			},
			Checked: func() bool { return fixedPanelSortNumericChecked(side.index) },
			Handler: func() bool { return runFixedPanelSortNumeric(side.index) },
		})

		for _, aiView := range fixedAIViewActionSpecs {
			aiView := aiView
			registerAction(action.Action{
				Name:         "AI." + side.id + "." + aiView.id,
				Area:         "Shell",
				Label:        aiView.label,
				LabelKey:     aiView.labelKey,
				Description:  fmt.Sprintf("Switch the %s AI panel to %s", strings.ToLower(side.id), aiView.description),
				DescKey:      aiView.descKey,
				MenuPath:     side.menuPath,
				HideFromMenu: true,
				Visible:      func() bool { return fixedAIPanelVisible(side.index) },
				Handler:      func() bool { return runFixedAIView(side.index, aiView.path, aiView.isChat) },
			})
		}
	}

	registerAction(action.Action{
		Name:        "Viewer.GoTo",
		Area:        "Viewer",
		Label:       "Go To",
		LabelKey:    "KeyBar.ViewerAltF8",
		Description: "Go to a line or byte offset",
		DescKey:     "Action.Viewer.GoTo.Desc",
		DefaultKeys: []string{"AltF8"},
		MenuPath:    "Search",
		Handler:     actionViewerGoTo,
	})
	registerAction(action.Action{
		Name:        "Editor.GoTo",
		Area:        "Editor",
		Label:       "Go To",
		LabelKey:    "KeyBar.EditorAltF8",
		Description: "Go to a line and position or byte offset",
		DescKey:     "Action.Editor.GoTo.Desc",
		DefaultKeys: []string{"AltF8"},
		MenuPath:    "Search",
		Handler:     actionEditorGoTo,
	})
	registerAction(action.Action{
		Name:         "App.Background",
		Area:         "Shell",
		Label:        "Background",
		LabelKey:     "FileOp.BtnBackground",
		Description:  "Suspend f4 and return to the parent shell",
		DescKey:      "Action.App.Background.Desc",
		MenuPath:     "Files",
		HideFromMenu: true,
		Handler:      actionBackground,
	})
	registerAction(action.Action{
		Name:         "App.Arkanoid",
		Area:         "Shell",
		Label:        "Arkanoid",
		LabelKey:     "Action.App.Arkanoid",
		Description:  "Open the hidden Arkanoid game workspace",
		DescKey:      "Action.App.Arkanoid.Desc",
		NativeKeys:   []string{"CtrlAltA"},
		HideFromMenu: true,
		Visible:      arkanoidActionVisible,
		Handler:      actionArkanoid,
	})
}
