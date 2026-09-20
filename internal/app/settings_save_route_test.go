package app

import "testing"

// Shift+F9 is Far's "save setup", and it opens the small dialog that asks what
// to save. It had been turned into a link to the Settings Center, which has no
// hotkey and no such question (#1282); the Center keeps its own manual saving
// commands next to it.
func TestSettingsSaveActionIsTheSaveDialogNotADeepLink(t *testing.T) {
	action, ok := GetAction("App.SaveSettings")
	if !ok {
		t.Fatal("App.SaveSettings is not registered")
	}
	if _, linked := settingsDeepLinks["app.savesettings"]; linked {
		t.Fatal("Save Settings must not be redirected to the Settings Center")
	}
	if action.HideFromMenu {
		t.Fatal("Save Settings must be in the Options menu, where Far has it")
	}
	if len(action.DefaultKeys) != 1 || action.DefaultKeys[0] != "ShiftF9:NoAltScreenApp" {
		t.Fatal("Shift+F9 is not the shortcut of Save Settings")
	}
}

func TestSettingsPathHintsRoute(t *testing.T) {
	if settingsDeepLinks["settings.pathhints"] != "terminal" {
		t.Fatal("old shortcut points to removed category")
	}
}
