package netfox

import (
	"testing"

	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtui"
)

func TestProxyModeItemsHaveAllRows(t *testing.T) {
	items := proxyModeItems()
	if len(items) != len(proxyModeOrder) {
		t.Fatalf("proxyModeItems returned %d rows, want %d", len(items), len(proxyModeOrder))
	}
	for i, item := range items {
		if item == "" {
			t.Errorf("proxy mode row %d is empty", i)
		}
	}
}

func TestPadProxyLabelEmpty(t *testing.T) {
	if got := padProxyLabel(""); got != " " {
		t.Fatalf("padProxyLabel(\"\") = %q, want one space", got)
	}
}

func TestPadProxyLabelPreservesText(t *testing.T) {
	if got := padProxyLabel("Proxy"); got != "Proxy " {
		t.Fatalf("padProxyLabel(\"Proxy\") = %q, want trailing space", got)
	}
}

func TestProxyModeIndexGlobal(t *testing.T) {
	if got := proxyModeIndex(netproxy.ModeGlobal); got != 0 {
		t.Fatalf("global mode index = %d, want 0", got)
	}
}

func TestProxyModeIndexSystem(t *testing.T) {
	if got := proxyModeIndex(netproxy.ModeSystem); got != 1 {
		t.Fatalf("system mode index = %d, want 1", got)
	}
}

func TestProxyModeIndexDirect(t *testing.T) {
	if got := proxyModeIndex(netproxy.ModeDirect); got != 2 {
		t.Fatalf("direct mode index = %d, want 2", got)
	}
}

func TestProxyModeIndexHTTP(t *testing.T) {
	if got := proxyModeIndex(netproxy.ModeHTTP); got != 3 {
		t.Fatalf("HTTP mode index = %d, want 3", got)
	}
}

func TestProxyModeIndexSOCKS5(t *testing.T) {
	if got := proxyModeIndex(netproxy.ModeSOCKS5); got != 4 {
		t.Fatalf("SOCKS5 mode index = %d, want 4", got)
	}
}

func TestProxyModeIndexUnknownFallsBackToGlobal(t *testing.T) {
	if got := proxyModeIndex(-1); got != 0 {
		t.Fatalf("unknown mode index = %d, want 0", got)
	}
}

func TestShowProxyDialogCommitsTrimmedFields(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(100, 40)
	vtui.FrameManager.Init(scr)

	cfg := NetFoxConfig{
		ProxyMode: netproxy.ModeHTTP,
		ProxyHost: "  proxy.example  ",
		ProxyPort: " 8080 ",
		ProxyUser: "user",
		ProxyPass: "secret",
	}
	showProxyDialog(&cfg)
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("showProxyDialog top frame = %T, want *vtui.Window", vtui.FrameManager.GetTopFrame())
	}
	testutil.ClickDialogButton(t, dlg, vtui.Msg("vtui.Ok"))

	if cfg.ProxyHost != "proxy.example" || cfg.ProxyPort != "8080" {
		t.Fatalf("proxy fields = (%q, %q), want trimmed values", cfg.ProxyHost, cfg.ProxyPort)
	}
	if cfg.ProxyUser != "user" || cfg.ProxyPass != "secret" {
		t.Fatalf("credentials changed to (%q, %q)", cfg.ProxyUser, cfg.ProxyPass)
	}
}
