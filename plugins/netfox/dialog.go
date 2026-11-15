package netfox

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func padLabel(s string) string {
	for runewidth.StringWidth(s) < 11 {
		s += " "
	}
	return s
}

// protoUIContainer is a proxy that routes events and rendering to the selected
// protocol's UI block. By implementing vtui.Container but using custom Show/Focus logic,
// it provides architectural isolation while remaining discoverable by the validator.
type protoUIContainer struct {
	vtui.ScreenObject
	active string
	uis    map[string]vtui.UIElement
}

func (p *protoUIContainer) MoveRelative(dx, dy int) {
	p.ScreenObject.MoveRelative(dx, dy)
	for _, ui := range p.uis {
		if ui != nil {
			ui.MoveRelative(dx, dy)
		}
	}
}

func (p *protoUIContainer) GetChildren() []vtui.UIElement {
	var children []vtui.UIElement
	if ui, ok := p.uis[p.active]; ok && ui != nil {
		children = append(children, ui)
	}
	return children
}

func (p *protoUIContainer) Show(scr *vtui.ScreenBuf) {
	p.ScreenObject.Show(scr)
	if ui, ok := p.uis[p.active]; ok && ui != nil {
		ui.Show(scr)
	}
}

func (p *protoUIContainer) ProcessKey(e *vtinput.InputEvent) bool {
	if ui, ok := p.uis[p.active]; ok && ui != nil {
		return ui.ProcessKey(e)
	}
	return false
}

func (p *protoUIContainer) ProcessMouse(e *vtinput.InputEvent) bool {
	if ui, ok := p.uis[p.active]; ok && ui != nil {
		return ui.ProcessMouse(e)
	}
	return false
}

func (p *protoUIContainer) CanFocus() bool {
	if ui, ok := p.uis[p.active]; ok && ui != nil {
		return ui.CanFocus()
	}
	return false
}

func (p *protoUIContainer) SetFocus(f bool) {
	p.ScreenObject.SetFocus(f)
	if ui, ok := p.uis[p.active]; ok && ui != nil {
		ui.SetFocus(f)
	}
}

func (p *protoUIContainer) WantsChars() bool {
	if ui, ok := p.uis[p.active]; ok && ui != nil {
		return ui.WantsChars()
	}
	return false
}

func showConnectionDialog(app vfs.App, nf *NetFoxVFS, oldName string) {
	var cfg NetFoxConfig
	name := ""
	if oldName != "" {
		name = oldName
		configs := nf.getConfigs()
		cfg = configs[oldName]
	} else {
		cfg.Type = "sftp"
	}

	protos := GetProtocols()
	if len(protos) == 0 {
		protos = []string{"sftp", "ftp"}
	}

	currIdx := 0
	for i, p := range protos {
		if p == cfg.Type {
			currIdx = i
			break
		}
	}
	activeProto := protos[currIdx]

	dlg := vtui.NewCenteredDialog(60, 22, vtui.Msg("NetFox.ConnectionTitle"))
	dlg.ShowClose = true

	editName := vtui.NewEdit(0, 0, 40, name)

	comboProto := vtui.NewComboBox(0, 0, 15, protos)
	comboProto.DropdownOnly = true
	comboProto.Menu.SetSelectPos(currIdx)
	comboProto.Edit.SetText(activeProto)

	editHost := vtui.NewEdit(0, 0, 40, cfg.Host)
	editPort := vtui.NewEdit(0, 0, 10, cfg.Port)
	if editPort.GetText() == "" {
		if h, ok := handlers[activeProto]; ok {
			editPort.SetText(h.DefaultPort())
		}
	}
	editUser := vtui.NewEdit(0, 0, 40, cfg.User)
	editPass := vtui.NewPasswordEdit(0, 0, 40, cfg.Pass)
	editKeyPath := vtui.NewEdit(0, 0, 40, cfg.KeyPath)
	editPath := vtui.NewEdit(0, 0, 40, cfg.Path)
	editTimeout := vtui.NewEdit(0, 0, 10, cfg.Timeout)
	if editTimeout.GetText() == "" {
		editTimeout.SetText("15")
	}

	cpItems := []string{}
	for _, cp := range vfs.AvailableCodepages {
		cpItems = append(cpItems, vfs.CodepageMenuLabel(cp))
	}
	comboCp := vtui.NewComboBox(0, 0, 30, cpItems)
	comboCp.DropdownOnly = true
	currCpIdx := 0
	if cfg.Codepage == "" {
		for i, cp := range vfs.AvailableCodepages {
			if cp.ID == 65001 {
				currCpIdx = i
				break
			}
		}
	}
	configuredCodepage := cfg.Codepage
	if id, err := strconv.Atoi(configuredCodepage); err == nil {
		configuredCodepage = strconv.Itoa(vfs.NormalizeCodepageID(id))
	}
	for i, cp := range vfs.AvailableCodepages {
		if fmt.Sprintf("%d", cp.ID) == configuredCodepage {
			currCpIdx = i
			break
		}
	}
	comboCp.Menu.SetSelectPos(currCpIdx)
	comboCp.Edit.SetText(cpItems[currCpIdx])

	makeRow := func(label string, edit vtui.UIElement) *vtui.HBoxLayout {
		hbox := vtui.NewHBoxLayout(0, 0, 56, 1)
		l := vtui.NewLabel(0, 0, padLabel(label), edit)
		dlg.AddItem(l)
		dlg.AddItem(edit)
		hbox.Add(l, vtui.Margins{Right: 1}, vtui.AlignLeft)
		hbox.Add(edit, vtui.Margins{}, vtui.AlignFill)
		return hbox
	}

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 56, 10)

	vbox.Add(makeRow(vtui.Msg("NetFox.LabelName"), editName), vtui.Margins{}, vtui.AlignFill)
	vbox.Add(makeRow(vtui.Msg("NetFox.LabelProtocol"), comboProto), vtui.Margins{Top: 0}, vtui.AlignFill)
	vbox.Add(makeRow(vtui.Msg("NetFox.LabelHost"), editHost), vtui.Margins{Top: 0}, vtui.AlignFill)
	vbox.Add(makeRow(vtui.Msg("NetFox.LabelPort"), editPort), vtui.Margins{Top: 0}, vtui.AlignFill)
	vbox.Add(makeRow(vtui.Msg("NetFox.LabelUser"), editUser), vtui.Margins{Top: 0}, vtui.AlignFill)
	vbox.Add(makeRow(vtui.Msg("NetFox.LabelPassword"), editPass), vtui.Margins{Top: 0}, vtui.AlignFill)
	vbox.Add(makeRow(vtui.Msg("NetFox.LabelKeyPath"), editKeyPath), vtui.Margins{Top: 0}, vtui.AlignFill)
	vbox.Add(makeRow(vtui.Msg("NetFox.LabelPath"), editPath), vtui.Margins{Top: 0}, vtui.AlignFill)
	vbox.Add(makeRow(vtui.Msg("NetFox.LabelTimeout"), editTimeout), vtui.Margins{Top: 0}, vtui.AlignFill)
	vbox.Add(makeRow(vtui.Msg("NetFox.LabelCodepage"), comboCp), vtui.Margins{Top: 0}, vtui.AlignFill)
	vbox.Apply()

	// 2. Extra Protocol Area (Y: 15) - architectural proxy
	extraX, extraY, extraW, extraH := dlg.X1+2, dlg.Y1+15, 56, 1
	container := &protoUIContainer{
		active: activeProto,
		uis:    make(map[string]vtui.UIElement),
	}
	container.SetPosition(extraX, extraY, extraX+extraW-1, extraY+extraH-1)

	extraSaves := make(map[string]func())
	for _, p := range protos {
		if h, ok := handlers[p]; ok {
			ui, save := h.BuildExtraUI(&cfg, extraX, extraY, extraW, extraH)
			if ui != nil {
				// ARCHITECTURAL FIX: protocol-specific UIs belong to the proxy,
				// not to the main dialog. This ensures true protocol isolation.
				ui.SetOwner(container)
			}
			container.uis[p] = ui
			extraSaves[p] = save
		}
	}
	dlg.AddItem(container)

	origOnAction := comboProto.Menu.OnAction
	comboProto.Menu.OnAction = func(idx int) {
		if origOnAction != nil {
			origOnAction(idx)
		}
		newProto := comboProto.Menu.Items[idx].Text
		if newProto == activeProto {
			return
		}

		if h, ok := handlers[newProto]; ok {
			currPort := editPort.GetText()
			oldH := handlers[activeProto]
			if currPort == "" || (oldH != nil && currPort == oldH.DefaultPort()) {
				editPort.SetText(h.DefaultPort())
			}
		}

		container.active = newProto
		activeProto = newProto

		vtui.FrameManager.Redraw()
	}

	// 3. Bottom Section (Y: 17-19)
	btnOk := vtui.NewButton(0, 0, vtui.Msg("vtui.Save"))
	// The proxy lives in a dialog of its own: it is a rare thing to touch,
	// and the connection form has no room left for five more fields.
	btnProxy := vtui.NewButton(0, 0, vtui.Msg("NetFox.BtnProxy"))
	btnCancel := vtui.NewButton(0, 0, vtui.Msg("vtui.Cancel"))
	btnOk.IsDefault = true
	btnProxy.OnClick = func() { showProxyDialog(&cfg) }

	vboxBottom := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+17, 56, 3)
	btnHbox := vtui.NewHBoxLayout(0, 0, 56, 1)
	btnHbox.HorizontalAlign = vtui.AlignCenter
	btnHbox.Spacing = 2
	dlg.AddItem(btnOk)
	dlg.AddItem(btnProxy)
	dlg.AddItem(btnCancel)
	btnHbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	btnHbox.Add(btnProxy, vtui.Margins{}, vtui.AlignTop)
	btnHbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vboxBottom.Add(btnHbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vboxBottom.Apply()

	btnOk.OnClick = func() {
		newName := editName.GetText()
		if newName == "" {
			vtui.ShowMessageOn(dlg, vtui.Msg("Error.Title"), vtui.Msg("NetFox.ErrorEmptyName"), []string{vtui.Msg("vtui.Ok")})
			return
		}
		if newName == "<Add connection>" || newName == ".." {
			vtui.ShowMessageOn(dlg, vtui.Msg("Error.Title"), vtui.Msg("NetFox.ErrorReservedName"), []string{vtui.Msg("vtui.Ok")})
			return
		}

		cfg.Type = activeProto
		cfg.Host = editHost.GetText()
		cfg.Port = editPort.GetText()
		cfg.User = editUser.GetText()
		cfg.Pass = editPass.GetText()
		cfg.KeyPath = editKeyPath.GetText()
		cfg.Path = strings.TrimSpace(editPath.GetText())
		cfg.Timeout = editTimeout.GetText()
		cfg.Codepage = ""
		cpSel := comboCp.Menu.SelectPos
		if cpSel >= 0 && cpSel < len(vfs.AvailableCodepages) {
			cfg.Codepage = fmt.Sprintf("%d", vfs.AvailableCodepages[cpSel].ID)
		}

		if save, ok := extraSaves[activeProto]; ok {
			save()
		}

		if err := nf.SaveConfig(newName, cfg); err != nil {
			vtui.ShowMessageOn(dlg, vtui.Msg("Error.Title"), "Could not save connection:\n"+err.Error(), []string{vtui.Msg("vtui.Ok")})
			return
		}
		if oldName != "" && oldName != newName {
			if err := nf.Remove(context.Background(), oldName); err != nil {
				vtui.ShowMessageOn(dlg, vtui.Msg("Error.Title"), "The renamed connection was saved, but the old connection could not be removed:\n"+err.Error(), []string{vtui.Msg("vtui.Ok")})
				return
			}
		}

		dlg.Close()
		app.RefreshAll()
	}
	btnCancel.OnClick = func() {
		dlg.Close()
	}

	vtui.FrameManager.Push(dlg)
}
