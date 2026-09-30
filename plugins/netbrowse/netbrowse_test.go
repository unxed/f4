package netbrowse

import (
	"errors"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestKindName(t *testing.T) {
	want := map[uint32]string{
		displayDomain: "Domain", displayServer: "Server", displayShare: "Share", displayShareAdmn: "Share",
		displayNetwork: "Network", displayRoot: "Root", displayDirectory: "Directory", displayTree: "Tree",
		displayNDSContnr: "Container", displayGeneric: "", 99: "",
	}
	for d, name := range want {
		if got := kindName(d); got != name {
			t.Errorf("kindName(%d) = %q, want %q", d, got, name)
		}
	}
}

// fakeNetwork is a tiny network: one provider holding one domain, holding two
// servers, of which the first has two shares.
func fakeNetwork(parent *resource) ([]resource, error) {
	switch {
	case parent == nil:
		return []resource{{Remote: "Microsoft Windows Network", Display: displayNetwork, Container: true, Provider: "P"}}, nil
	case parent.Remote == "Microsoft Windows Network":
		return []resource{{Remote: "WORKGROUP", Display: displayDomain, Container: true, Provider: "P"}}, nil
	case parent.Remote == "WORKGROUP":
		return []resource{
			{Remote: `\\alpha`, Display: displayServer, Container: true, Comment: "first", Provider: "P"},
			{Remote: `\\beta`, Display: displayServer, Container: true, Provider: "P"},
		}, nil
	case parent.Remote == `\\alpha`:
		return []resource{
			{Remote: `\\alpha\docs`, Display: displayShare, Comment: "documents"},
			{Remote: `\\alpha\pub`, Display: displayShare},
		}, nil
	case parent.Remote == `\\beta`:
		return nil, errors.New("access denied")
	}
	return nil, nil
}

func openFake(t *testing.T, list enumerator) *netPanel {
	t.Helper()
	c, err := newNetPanel(vfs.PanelContext{Bounds: [4]int{0, 0, 59, 19}}, list)
	if err != nil {
		t.Fatal(err)
	}
	return c.(*netPanel)
}

func names(p *netPanel) []string {
	var out []string
	for _, r := range p.table.Rows {
		out = append(out, r.(netRow).GetCellText(colName))
	}
	return out
}

func key(vk uint16) *vtinput.InputEvent {
	return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk}
}

func TestNetRowCells(t *testing.T) {
	r := netRow{res: &resource{Remote: `\\alpha`, Display: displayServer, Comment: "first"}}
	got := []string{r.GetCellText(colName), r.GetCellText(colKind), r.GetCellText(colComment), r.GetCellText(99)}
	want := []string{`\\alpha`, "Server", "first", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cell %d = %q, want %q", i, got[i], want[i])
		}
	}
	up := netRow{}
	if up.GetCellText(colName) != ".." || up.GetCellText(colKind) != "" {
		t.Errorf("the up row shows %q / %q", up.GetCellText(colName), up.GetCellText(colKind))
	}
}

// TestPanelWalksDownAndBackUp: Enter enters containers level by level, the
// ".." row goes back with the cursor on the container just left, and Enter on
// a share leaves the panel where it is.
func TestPanelWalksDownAndBackUp(t *testing.T) {
	p := openFake(t, fakeNetwork)
	if got := names(p); len(got) != 1 || got[0] != "Microsoft Windows Network" {
		t.Fatalf("top of the network = %v", got)
	}
	p.ProcessKey(key(vtinput.VK_RETURN)) // into the provider; the cursor starts on ".."
	if p.GetSelectedName() != "" {
		t.Fatalf("after entering, the cursor is on %q, want the .. row", p.GetSelectedName())
	}
	p.selectRemote("WORKGROUP")
	p.ProcessKey(key(vtinput.VK_RETURN)) // into WORKGROUP
	if got := names(p); len(got) != 3 || got[0] != ".." || got[1] != `\\alpha` {
		t.Fatalf("WORKGROUP lists %v", got)
	}
	p.selectRemote(`\\alpha`)
	p.open()
	if got := names(p); len(got) != 3 || got[1] != `\\alpha\docs` {
		t.Fatalf(`\\alpha lists %v`, got)
	}
	p.selectRemote(`\\alpha\docs`)
	before := len(p.path)
	p.open() // a share: only named
	if len(p.path) != before {
		t.Error("Enter on a share changed the level")
	}
	if p.GetSelectedName() != `\\alpha\docs` {
		t.Errorf("selected %q", p.GetSelectedName())
	}

	p.table.SelectPos = 0 // the ".." row
	p.open()
	if p.GetSelectedName() != `\\alpha` || len(p.path) != before-1 {
		t.Errorf("after going up: on %q, level %d (want \\\\alpha, %d)", p.GetSelectedName(), len(p.path), before-1)
	}
}

func TestPanelStaysWhenAContainerCannotBeListed(t *testing.T) {
	p := openFake(t, fakeNetwork)
	p.enter(resource{Remote: "Microsoft Windows Network", Container: true})
	p.enter(resource{Remote: "WORKGROUP", Container: true})
	depth := len(p.path)
	p.enter(resource{Remote: `\\beta`, Container: true}) // access denied
	if len(p.path) != depth || p.current().Remote != "WORKGROUP" {
		t.Errorf("a failed enter left the panel at depth %d on %q", len(p.path), p.current().Remote)
	}
}

func TestPanelRefreshKeepsTheCursorAndReportsFailure(t *testing.T) {
	calls := 0
	var fail error
	p := openFake(t, func(parent *resource) ([]resource, error) {
		calls++
		if fail != nil {
			return nil, fail
		}
		return fakeNetwork(parent)
	})
	p.enter(resource{Remote: "Microsoft Windows Network", Container: true})
	p.enter(resource{Remote: "WORKGROUP", Container: true})
	p.selectRemote(`\\beta`)
	p.ProcessKey(key(vtinput.VK_F5))
	if p.GetSelectedName() != `\\beta` {
		t.Errorf("cursor after F5 on %q", p.GetSelectedName())
	}
	fail = errors.New("network down")
	p.refresh()
	if len(p.table.Rows) != 3 {
		t.Errorf("a failed refresh changed the rows: %d", len(p.table.Rows))
	}
	if calls < 4 {
		t.Errorf("enumerator called %d times", calls)
	}
}

func TestPanelOpenFailsWhenTheTopCannotBeListed(t *testing.T) {
	if _, err := newNetPanel(vfs.PanelContext{}, func(*resource) ([]resource, error) { return nil, errUnsupported }); err == nil {
		t.Fatal("opening the panel succeeded although the network cannot be listed")
	}
}

func TestPanelGeometryFocusAndDraw(t *testing.T) {
	p := openFake(t, fakeNetwork)
	p.SetPosition(1, 2, 40, 10)
	if x1, y1, x2, y2 := p.GetPosition(); x1 != 1 || y1 != 2 || x2 != 40 || y2 != 10 {
		t.Errorf("position = %d,%d,%d,%d", x1, y1, x2, y2)
	}
	p.SetFocus(true)
	if !p.IsFocused() {
		t.Error("SetFocus(true) left the panel unfocused")
	}
	p.SetFocus(false)
	p.SetContext(vfs.PanelContext{})
	p.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType})
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(60, 20)
	p.Show(scr)
	p.enter(resource{Remote: "Microsoft Windows Network", Container: true})
	p.Show(scr)
	p.up()
	p.up() // already at the top: nothing happens
	if err := p.Close(); err != nil {
		t.Error(err)
	}
	if got := len(p.PanelKeys()); got != 3 { // F5, Enter and F1, the help
		t.Errorf("panel keys = %d, want 3", got)
	}
}

type fakeRegistration struct{ unregistered bool }

func (r *fakeRegistration) Unregister() { r.unregistered = true }

type fakePanelHost struct {
	vfs.HostAPI
	provider  vfs.PanelProvider
	reg       *fakeRegistration
	driveName string
	drive     func() vfs.VFS
	uri       vfs.URIProvider
	uriErr    error
}

func (h *fakePanelHost) RegisterURIProvider(p vfs.URIProvider) error {
	h.uri = p
	return h.uriErr
}

func (h *fakePanelHost) RegisterDrive(name string, factory func() vfs.VFS) {
	h.driveName, h.drive = name, factory
}

func (h *fakePanelHost) RegisterPanelProvider(p vfs.PanelProvider) (vfs.Registration, error) {
	h.provider = p
	h.reg = &fakeRegistration{}
	return h.reg, nil
}

func withSupported(t *testing.T, on bool) {
	t.Helper()
	old := supported
	supported = on
	t.Cleanup(func() { supported = old })
}

func TestPluginRegistersOnlyWhereSupported(t *testing.T) {
	p := NewPlugin()
	if p.GetName() != "NetBrowse" {
		t.Errorf("name = %q", p.GetName())
	}
	if err := p.Init(nil); err == nil {
		t.Error("nil host accepted")
	}
	withSupported(t, false)
	host := &fakePanelHost{}
	if err := p.Init(host); err != nil || host.reg != nil {
		t.Fatalf("unsupported Init: err = %v, registered = %v", err, host.reg != nil)
	}
	withSupported(t, true)
	if err := p.Init(struct{ vfs.HostAPI }{}); err == nil {
		t.Error("a host without panel contributions accepted")
	}
	if err := p.Init(host); err != nil {
		t.Fatal(err)
	}
	if host.provider.ID != panelProviderID || host.provider.Open == nil {
		t.Errorf("provider = %+v", host.provider)
	}
	if host.driveName != "Network" || host.drive == nil {
		t.Errorf("drive = %q, factory %v", host.driveName, host.drive != nil)
	} else if _, ok := host.drive().(*networkVFS); !ok {
		t.Error("the Network drive is not a networkVFS")
	}
	if host.uri == nil || host.uri.Scheme() != "network" {
		t.Errorf("URI provider = %v", host.uri)
	}
	if err := p.Init(host); err == nil {
		t.Error("second Init accepted")
	}
	if err := p.Close(); err != nil || !host.reg.unregistered {
		t.Errorf("Close: err = %v, unregistered = %v", err, host.reg.unregistered)
	}
}

func TestEnumerateNetworkOffWindows(t *testing.T) {
	if Supported() {
		t.Skip("this OS has the WNet API")
	}
	if _, err := enumerateNetwork(nil); !errors.Is(err, errUnsupported) {
		t.Errorf("enumerateNetwork error = %v, want errUnsupported", err)
	}
}

func TestPluginUnregistersWhenTheURIProviderFails(t *testing.T) {
	withSupported(t, true)
	host := &fakePanelHost{uriErr: errors.New("taken")}
	if err := NewPlugin().Init(host); err == nil {
		t.Fatal("Init succeeded although the URI provider could not be registered")
	}
	if host.reg == nil || !host.reg.unregistered {
		t.Error("the panel provider was left registered")
	}
}
