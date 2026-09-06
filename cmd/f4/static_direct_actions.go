package main

import (
	"fmt"
	"strings"

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
	mode     ViewMode
}

type fixedPanelSortActionSpec struct {
	id       string
	label    string
	labelKey string
	descKey  string
	mode     SortMode
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
	{id: "ViewBrief", label: "Brief", labelKey: "Menu.Left.Brief", descKey: "Action.Panel.ViewBrief.Desc", mode: ViewModeBrief},
	{id: "ViewMedium", label: "Medium", labelKey: "Menu.Left.Medium", descKey: "Action.Panel.ViewMedium.Desc", mode: ViewModeMedium},
	{id: "ViewDetailed", label: "Detailed", labelKey: "Menu.Left.Detailed", descKey: "Action.Panel.ViewDetailed.Desc", mode: ViewModeDetailed},
	{id: "ViewWide", label: "Wide", labelKey: "Menu.Left.Wide", descKey: "Action.Panel.ViewWide.Desc", mode: ViewModeWide},
}

var fixedPanelSortActionSpecs = []fixedPanelSortActionSpec{
	{id: "SortByName", label: "Name", labelKey: "Menu.SortName", descKey: "Action.Panel.SortByName.Desc", mode: SortName},
	{id: "SortByExt", label: "Extension", labelKey: "Menu.SortExt", descKey: "Action.Panel.SortByExt.Desc", mode: SortExt},
	{id: "SortByTime", label: "Modification Time", labelKey: "Menu.SortTime", descKey: "Action.Panel.SortByTime.Desc", mode: SortTime},
	{id: "SortBySize", label: "Size", labelKey: "Menu.SortSize", descKey: "Action.Panel.SortBySize.Desc", mode: SortSize},
	{id: "SortUnsorted", label: "Unsorted", labelKey: "Menu.SortUnsorted", descKey: "Action.Panel.SortUnsorted.Desc", mode: SortUnsorted},
}

var fixedAIViewActionSpecs = []fixedAIViewActionSpec{
	{id: "ViewContext", label: "AI View: Context", labelKey: "Action.AI.ViewContext", description: "context view", descKey: "Action.AI.ViewContext.Desc", path: "ai://ctx"},
	{id: "ViewChat", label: "AI View: Chat", labelKey: "Action.AI.ViewChat", description: "chat view", descKey: "Action.AI.ViewChat.Desc", path: "ai://chat", isChat: true},
	{id: "ViewOut", label: "AI View: Artifacts", labelKey: "Action.AI.ViewOut", description: "artifacts view", descKey: "Action.AI.ViewOut.Desc", path: "ai://out"},
	{id: "ViewMem", label: "AI View: Memory", labelKey: "Action.AI.ViewMem", description: "memory view", descKey: "Action.AI.ViewMem.Desc", path: "ai://mem"},
}

func fixedRegularPanel(index int) (*PanelsFrame, *FileSystemPanel, bool) {
	pf := findPanelsFrameAnyScreen()
	if pf == nil || index < 0 || index >= len(pf.panels) || isAIPanel(pf.panels[index]) {
		return nil, nil, false
	}
	fsp, ok := pf.panels[index].(*FileSystemPanel)
	if !ok || fsp == nil {
		return nil, nil, false
	}
	return pf, fsp, true
}

func fixedPanelViewChecked(index int, mode ViewMode) bool {
	pf, fsp, ok := fixedRegularPanel(index)
	if !ok {
		return false
	}
	if mode == ViewModeWide {
		return pf.wide && pf.widePanel == index
	}
	return (!pf.wide || pf.widePanel != index) && fsp.viewMode == mode
}

func fixedPanelSortChecked(index int, mode SortMode) bool {
	_, fsp, ok := fixedRegularPanel(index)
	return ok && fsp.sortMode == mode
}

func fixedPanelSortGroupsChecked(index int) bool {
	_, fsp, ok := fixedRegularPanel(index)
	return ok && fsp.useSortGroups
}

func runFixedPanelSortGroups(index int) bool {
	pf, fsp, ok := fixedRegularPanel(index)
	if !ok {
		return false
	}
	fsp.ToggleSortGroups()
	pf.updateMenuCheckmarks()
	return true
}

func runFixedPanelView(index int, mode ViewMode) bool {
	pf, _, ok := fixedRegularPanel(index)
	if !ok {
		return false
	}
	if mode == ViewModeWide {
		pf.setWidePanel(index)
	} else {
		pf.setPanelViewMode(index, mode)
	}
	return true
}

func runFixedPanelSort(index int, mode SortMode) bool {
	pf, fsp, ok := fixedRegularPanel(index)
	if !ok {
		return false
	}
	fsp.SetSortMode(mode)
	pf.updateMenuCheckmarks()
	return true
}

func fixedAIPanelVisible(index int) bool {
	pf := findPanelsFrameAnyScreen()
	return pf != nil && index >= 0 && index < len(pf.panels) && isAIPanel(pf.panels[index])
}

func runFixedAIView(index int, path string, isChat bool) bool {
	pf := findPanelsFrameAnyScreen()
	if pf == nil || index < 0 || index >= len(pf.panels) || !isAIPanel(pf.panels[index]) {
		return false
	}
	fsp, ok := pf.panels[index].(*FileSystemPanel)
	if !ok || fsp == nil {
		return false
	}
	fsp.AiSetViewMode(path, isChat)
	return true
}

func actionBackground() bool {
	if !SupportsBackgrounding() {
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
	_, ok := vtui.FrameManager.GetTopFrame().(*PanelsFrame)
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
	vv, ok := vtui.FrameManager.GetTopFrame().(*ViewerView)
	if !ok || vv == nil {
		return false
	}
	vv.askGoto()
	return true
}

func init() {
	for _, side := range fixedPanelSideActionSpecs {
		side := side
		for _, view := range fixedPanelViewActionSpecs {
			view := view
			RegisterAction(Action{
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
			if sortMode.mode == SortUnsorted {
				description = fmt.Sprintf("Disable sorting for the %s panel", strings.ToLower(side.id))
			}
			RegisterAction(Action{
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

		RegisterAction(Action{
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

		for _, aiView := range fixedAIViewActionSpecs {
			aiView := aiView
			RegisterAction(Action{
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

	RegisterAction(Action{
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
	RegisterAction(Action{
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
	RegisterAction(Action{
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
