package settings

import (
	"context"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/settingstest"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtui"
)

func TestSettingsRussianCoverage(t *testing.T) {
	for _, c := range []f4settings.Catalog{(coreSettingsProvider{}).Catalog(), newCoreRecordSettingsProvider().Catalog(), (aiSettingsProvider{}).Catalog(), (hotkeySettingsProvider{}).Catalog(), (settingsOperationsProvider{}).Catalog(), (pluginSettingsProvider{}).Catalog(), (&catalogSettingsProvider{}).Catalog()} {
		settingstest.RussianCatalog(t, c)
	}
}

func TestSettingsRussianRenderedLabelsAndDescriptions(t *testing.T) {
	old := config.App
	defer func() { config.App = old; InitLang() }()
	config.App.Language = "ru"
	config.App.UseLocalLanguageFiles = false
	InitLang()
	d, _ := (coreSettingsProvider{}).Begin(context.Background())
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: (coreSettingsProvider{}).Catalog(), draft: d}})
	c.selectCategory("appearance")
	c.ResizeConsole(130, 35)
	c.SetPosition(0, 0, 129, 34)
	var font *settingsRow
	for _, r := range c.page.rows {
		if r.field.ID == "GuiFontSize" {
			font = r
		}
	}
	if font == nil {
		t.Fatal("missing font size")
	}
	c.describe(font)
	if !strings.Contains(c.help.text, "Размер шрифта") || !strings.Contains(c.help.text, "Вступает в силу: после перезапуска") || strings.Contains(c.help.text, "Set graphical") {
		t.Fatalf("untranslated explanation: %q", c.help.text)
	}
	c.query = "размер шрифта"
	c.updateMatches()
	if !font.match {
		t.Fatal("Russian search does not match translated option")
	}
	c.query = "font size"
	c.updateMatches()
	if !font.match {
		t.Fatal("English search alias lost")
	}
	if c.categoryLabel("history") != "История" {
		t.Fatalf("history category = %q", c.categoryLabel("history"))
	}
	if c.categoryLabel("appearance") != "Внешний вид и язык" {
		t.Fatal("category not translated")
	}
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(130, 35)
	c.Show(screen)
	c.selectCategory("startup")
	for _, row := range c.page.rows {
		if row.field.ID == "ConsoleMode" {
			if row.field.Choices[0].Label.Resolve("ru", i18n.Msg) == row.field.Choices[0].Label.English {
				t.Fatal("choice not translated")
			}
		}
	}
}

func TestSettingsSidebarFitsTranslatedCategoryLabels(t *testing.T) {
	old := config.App
	defer func() { config.App = old; InitLang() }()
	d, _ := (coreSettingsProvider{}).Begin(context.Background())
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: (coreSettingsProvider{}).Catalog(), draft: d}})
	c.ResizeConsole(160, 40)
	c.SetPosition(0, 0, 159, 39)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(160, 40)
	for _, language := range []string{"en", "ru", "en"} {
		config.App.Language = language
		InitLang()
		c.Show(scr)
		longest := 0
		for _, cat := range c.categories {
			longest = max(longest, vtui.StringWidth(cat.Label.Resolve(language, i18n.Msg)))
		}
		if width := c.sidebar.X2 - c.sidebar.X1 + 1; width != longest+6 {
			t.Fatalf("%s sidebar width %d, expected %d", language, width, longest+6)
		}
		if c.sidebar.GetContentWidth() < longest {
			t.Fatal("sidebar clips its longest label")
		}
	}
	c.ResizeConsole(80, 25)
	c.SetPosition(0, 0, 79, 24)
	c.Show(scr)
	if c.page.X2-c.page.X1+1 < 20 || c.sidebar.X2 >= c.page.X1 {
		t.Fatal("narrow layout columns overlap")
	}
}
