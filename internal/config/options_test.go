package config

import (
	"reflect"
	"testing"
)

func optionValue(options []Option, section, key string) (string, bool) {
	for _, option := range options {
		if option.Section == section && option.Key == key {
			return option.Value, true
		}
	}
	return "", false
}

func TestOptionsFollowTheOrderSaveConfigWrites(t *testing.T) {
	options := Options(DefaultConfig())
	if len(options) == 0 || options[0].Section != "Interface" || options[0].Key != "ColorStyle" {
		t.Fatalf("first option = %#v, want Interface.ColorStyle", options)
	}
	if _, ok := optionValue(options, "Layout", "WidthDecrement"); !ok {
		t.Fatal("Options lacks [Layout] WidthDecrement, which SaveConfig writes as key=value")
	}
}

func TestDefaultConfigIsWhatAnEmptySettingsFileGives(t *testing.T) {
	// App's literal says Modern; LoadConfig falls back to Radiola.
	if got, _ := optionValue(Options(DefaultConfig()), "Interface", "ColorStyle"); got != "Radiola" {
		t.Fatalf("default ColorStyle = %q, want Radiola", got)
	}
}

func TestWithOptionReadsTheValueBackLikeLoadConfig(t *testing.T) {
	oldPaths := GetConfigIniPaths
	GetConfigIniPaths = func() []string { return nil }
	t.Cleanup(func() { GetConfigIniPaths = oldPaths })

	cfg := DefaultConfig()
	if next := WithOption(cfg, "Editor", "TabSize", "8"); next.EditorTabSize != 8 {
		t.Errorf("TabSize = %d, want 8", next.EditorTabSize)
	}
	// LoadConfig takes only "1" for on, so "yes" turns the option off.
	if next := WithOption(cfg, "Panel", "ShowHiddenFiles", "yes"); next.ShowHiddenFiles {
		t.Error("ShowHiddenFiles = yes was kept as on")
	}
	// The symlink arrow is off unless the key asks for it.
	if cfg.ShowSymlinkArrow {
		t.Error("ShowSymlinkArrow is on by default; the arrow must appear only when it is switched on")
	}
	if next := WithOption(cfg, "Panel", "ShowSymlinkArrow", "1"); !next.ShowSymlinkArrow {
		t.Error("ShowSymlinkArrow = 1 was not honoured")
	}
	// A plain start restores the panels (far2l) unless the key asks for the
	// current folder (mc).
	if cfg.StartInCurrentFolder {
		t.Error("StartInCurrentFolder is on by default; a plain start must restore the panels like far2l")
	}
	if next := WithOption(cfg, "Startup", "StartInCurrentFolder", "1"); !next.StartInCurrentFolder {
		t.Error("Startup/StartInCurrentFolder = 1 was not honoured")
	}
	// The tar index cache is on unless the key turns it off.
	if !cfg.ArchiveTarIndexCache {
		t.Error("ArchiveTarIndexCache is off by default")
	}
	if next := WithOption(cfg, "Panel", "ArchiveTarIndexCache", "0"); next.ArchiveTarIndexCache {
		t.Error("Panel/ArchiveTarIndexCache = 0 was not honoured")
	}
	cfg.EditorTabSize = 2
	next := WithOption(cfg, "Panel", "ShowDirPrefix", "1")
	if !next.ShowDirPrefix || next.EditorTabSize != 2 {
		t.Errorf("ShowDirPrefix = %v, TabSize = %d; want true and the untouched 2", next.ShowDirPrefix, next.EditorTabSize)
	}
}

func TestChangedFieldsNamesTheFieldsThatDiffer(t *testing.T) {
	before := DefaultConfig()
	after := before
	after.EditorTabSize++
	after.ColorStyle = "Other"
	if got, want := ChangedFields(before, after), []string{"ColorStyle", "EditorTabSize"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedFields = %q, want %q", got, want)
	}
	if got := ChangedFields(before, before); len(got) != 0 {
		t.Fatalf("ChangedFields of equal configs = %q", got)
	}
}
