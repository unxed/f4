package envman

import (
	"errors"

	"github.com/unxed/vtui"
)

// text keeps EnvMan's UI independent from package main while still using the
// host localization catalog. English and Russian fallbacks also make focused
// plugin tests deterministic when the application catalog is not loaded.
func (plugin *Plugin) text(key, english, russian string) string {
	if translated := vtui.Msg(key); translated != "{"+key+"}" {
		return translated
	}
	return english
}

func (plugin *Plugin) notInitializedError() error {
	return errors.New(plugin.text("EnvMan.ErrorNotInitialized", "Environment Manager is not initialized", "Менеджер окружения не инициализирован"))
}
