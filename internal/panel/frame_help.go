package panel

// helpTopicOwner is an alternative panel with a help topic of its own.
type helpTopicOwner interface {
	HelpTopic() string
}

// GetHelp is the help topic F1 opens over the panels: the focused alternative
// panel's when it has one, the player's say (#2185), else the panels'.
func (pf *PanelsFrame) GetHelp() string {
	if pf.ShowPanels && pf.ActiveIdx >= 0 && pf.ActiveIdx < len(pf.AltPanels) {
		if a := pf.AltPanels[pf.ActiveIdx]; a != nil && a.IsFocused() {
			if owner, ok := a.(helpTopicOwner); ok && owner.HelpTopic() != "" {
				return owner.HelpTopic()
			}
		}
	}
	return pf.BaseFrame.GetHelp()
}
