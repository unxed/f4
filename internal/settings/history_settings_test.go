package settings

import (
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/sdk/f4settings"
)

func TestHistoryBucketSettingsControlIndependentBuckets(t *testing.T) {
	cfg := config.DefaultConfig()
	cmdline := "HistoryDisabledBuckets.cmdline"
	folders := "HistoryDisabledBuckets.folders"
	if got := coreSettingValue(cfg, cmdline); got != "true" {
		t.Fatalf("command history starts enabled: %q", got)
	}
	if err := setCoreSetting(&cfg, cmdline, "false"); err != nil {
		t.Fatal(err)
	}
	if err := setCoreSetting(&cfg, folders, "false"); err != nil {
		t.Fatal(err)
	}
	if cfg.HistoryDisabledBuckets != "cmdline,folders" {
		t.Fatalf("disabled buckets = %q", cfg.HistoryDisabledBuckets)
	}
	if got := coreSettingValue(cfg, cmdline); got != "false" {
		t.Fatalf("disabled command history setting = %q", got)
	}
	if err := setCoreSetting(&cfg, cmdline, "true"); err != nil {
		t.Fatal(err)
	}
	if cfg.HistoryDisabledBuckets != "folders" || coreSettingValue(cfg, folders) != "false" {
		t.Fatalf("independent toggle lost: disabled=%q folders=%q", cfg.HistoryDisabledBuckets, coreSettingValue(cfg, folders))
	}

	fields := map[string]bool{}
	for _, field := range coreSettingsStaticFields() {
		fields[field.ID] = field.Kind == f4settings.Boolean
	}
	for _, bucket := range history.PreferenceBuckets {
		id := "HistoryDisabledBuckets." + bucket.ID
		if !fields[id] {
			t.Errorf("history bucket %q has no boolean Settings Center control", bucket.ID)
		}
	}
}
