package settings

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtui"
)

var settingsRecordCounter atomic.Uint64

type settingsRecordRow struct {
	center     *settingsCenter
	collection f4settings.Collection
	record     f4settings.Record
}

func (r settingsRecordRow) GetCellText(int) string {
	if name := r.record.Values[r.collection.NameField]; name != "" {
		return name
	}
	return Phrase("(unnamed)")
}

type settingsRecordMatchKey struct{ collection, record, name string }

func (r settingsRecordRow) GetCellAttr(_ int, attr uint64) uint64 {
	if strings.TrimSpace(r.center.query) == "" {
		return attr
	}
	r.center.ensureSearchCache()
	key := settingsRecordMatchKey{r.collection.ID, r.record.ID, r.GetCellText(0)}
	matched, known := r.center.recordMatchCache[key]
	if !known {
		f := settingsCollectionField(r.collection)
		f.Aliases = append(f.Aliases, key.name)
		matched = r.center.matches(f)
		r.center.recordMatchCache[key] = matched
	}
	if !matched {
		return vtui.DimColor(attr)
	}
	return attr
}

type settingsButtonRow struct {
	*vtui.Group
	buttons []*vtui.Button
}

func (b *settingsButtonRow) SetPosition(x1, y1, x2, y2 int) {
	b.Group.SetPosition(x1, y1, x2, y2)
	x := x1
	for _, button := range b.buttons {
		width := vtui.StringWidth(button.GetCaption()) + 4
		button.SetPosition(x, y1, min(x2, x+width-1), y1)
		x += width + 1
	}
}

func (c *settingsCenter) rebuildCategory() {
	c.SetTitle(c.windowTitle())
	c.apply.SetText(settingsText("Apply", "&Apply"))
	c.ok.SetText(i18n.Msg("vtui.Ok"))
	c.cancel.SetText(i18n.Msg("vtui.Cancel"))
	c.previous.SetText(settingsText("Previous", "Previous match"))
	c.next.SetText(settingsText("Next", "Next match"))
	c.previous.ScreenObject.SetText("[←]")
	c.next.ScreenObject.SetText("[→]")
	c.clearSearch.SetText(settingsText("ClearSearch", "Clear search"))
	id := c.category
	c.offsets[id] = c.page.scroll
	focused := ""
	if item := c.page.GetFocusedItem(); item != nil {
		focused = item.GetId()
	}
	c.category = ""
	c.selectCategory(id)
	c.layoutWindow()
	for _, row := range c.page.rows {
		if row.control != nil && row.control.GetId() == focused {
			c.page.SetFocusedItem(row.control)
			c.describe(row)
			break
		}
	}
}

func (c *settingsCenter) addCollections(category string) {
	for _, s := range c.sessions {
		for _, col := range s.catalog.Collections {
			if col.Category != category {
				continue
			}
			meta := f4settings.Field{ID: col.ID, Category: category, Group: col.Group, Label: col.Label, Description: col.Description}
			table := vtui.NewTable(0, 0, 20, 5, []vtui.TableColumn{{Width: 0}})
			table.ShowHeader = false
			table.ShowSeparators = false
			settingsDialogTable(table)
			// Record lists are editable settings surfaces, like the adjacent inputs.
			table.ColorTextIdx = vtui.ColDialogEdit
			table.SetId("collection:" + col.ID)
			var rows []vtui.TableRow
			for _, record := range s.draft.Records[col.ID] {
				rows = append(rows, settingsRecordRow{c, col, record})
			}
			table.SetRows(rows)
			key := "record:" + col.ID
			selected := c.offsets[key]
			if selected >= len(rows) {
				selected = max(0, len(rows)-1)
			}
			table.SetSelectPos(selected)
			r := &settingsRow{field: meta, session: s, control: table, controlHeight: 5, match: true}
			c.page.AddItem(table)
			c.page.rows = append(c.page.rows, r)
			table.OnSelect = func(i int) {
				if c.offsets[key] != i {
					c.offsets[key] = i
					c.rebuildCategory()
				}
			}
			bar := &settingsButtonRow{Group: vtui.NewGroup(0, 0, 20, 1)}
			bar.SetId("collection-actions:" + col.ID)
			button := func(label string, run func()) {
				b := vtui.NewButton(0, 0, Phrase(label))
				b.OnClick = run
				bar.buttons = append(bar.buttons, b)
				bar.AddItem(b)
			}
			if !col.Fixed {
				button("Add", func() {
					values := map[string]string{}
					for _, f := range col.Fields {
						value := f.Default
						if value == "" {
							switch f.Kind {
							case f4settings.Boolean:
								value = "false"
							case f4settings.Integer:
								value = "0"
							case f4settings.ChoiceKind:
								if len(f.Choices) > 0 {
									value = f.Choices[0].Value
								}
							}
						}
						values[f.ID] = value
					}
					record := f4settings.Record{ID: fmt.Sprintf("new:%s:%d", col.ID, settingsRecordCounter.Add(1)), Values: values}
					s.draft.Records[col.ID] = append(s.draft.Records[col.ID], record)
					c.offsets[key] = len(s.draft.Records[col.ID]) - 1
					c.rebuildCategory()
				})
				button("Delete", func() {
					records := s.draft.Records[col.ID]
					if selected >= len(records) {
						return
					}
					// Removing a record is the one destructive button on
					// this page, and the row it acts on is a single click
					// away from the one next to it, so it asks first
					// (#1148). The shared Delete.* resources keep the
					// wording in every bundled language.
					name := strings.TrimSpace(records[selected].Values[col.NameField])
					if name == "" {
						name = Phrase("(unnamed)")
					}
					confirm := vtui.ShowMessageOn(c, i18n.Msg("Delete.Title"), fmt.Sprintf(i18n.Msg("Delete.Confirm"), name), []string{i18n.Msg("Delete.Btn"), i18n.Msg("vtui.Cancel")})
					confirm.OnResult = func(choice int) {
						if choice != 0 {
							return
						}
						records := s.draft.Records[col.ID]
						if selected < len(records) {
							s.draft.Records[col.ID] = append(records[:selected], records[selected+1:]...)
							c.rebuildCategory()
						}
					}
				})
			}
			if col.Ordered {
				for _, direction := range []int{-1, 1} {
					label := "Down"
					if direction < 0 {
						label = "Up"
					}
					button(label, func() {
						records := s.draft.Records[col.ID]
						next := selected + direction
						if selected >= 0 && selected < len(records) && next >= 0 && next < len(records) {
							records[selected], records[next] = records[next], records[selected]
							if col.ID == "bookmarks" {
								for i := range records {
									records[i].Values["bookmark.Name"] = fmt.Sprintf("%d: %s", i, records[i].Values["bookmark.Path"])
								}
							}
							c.offsets[key] = next
							c.rebuildCategory()
						}
					})
				}
			}

			for _, action := range col.Actions {
				button(action.Label.Resolve(config.App.Language, i18n.Msg), func() {
					if action.RequiresApplied {
						for _, session := range c.sessions {
							if len(session.draft.Changed()) > 0 {
								c.status = Phrase("Apply pending changes before running this operation.")
								return
							}
						}
					}
					if selected >= len(s.draft.Records[col.ID]) {
						return
					}
					if s.contributed && !settingsProviderAlive(s.provider) {
						c.status = Phrase("Provider is no longer loaded.")
						return
					}
					original := s.draft.Records[col.ID][selected]
					snapshot := original
					snapshot.Values = map[string]string{}
					for k, v := range original.Values {
						snapshot.Values[k] = v
					}
					var updates map[string]string
					c.runBackground(func(ctx context.Context) error { var err error; updates, err = action.Run(ctx, snapshot); return err }, func(err error) {
						if err == nil {
							for k, v := range updates {
								original.Values[k] = v
							}
							c.status = Phrase("Operation completed. Apply to save staged changes.")
							c.rebuildCategory()
						}
					})
				})
			}
			if len(bar.buttons) > 0 {
				c.page.AddItem(bar)
				c.page.rows = append(c.page.rows, &settingsRow{field: f4settings.Field{Category: category, Group: col.Group, Label: f4settings.Text{English: "Edit records"}, Description: col.Description}, session: s, control: bar, match: true})
			}
			if selected < len(s.draft.Records[col.ID]) {
				record := s.draft.Records[col.ID][selected]
				for _, field := range col.Fields {
					f := field
					if strings.HasPrefix(col.ID, "usermenu.") && strings.HasSuffix(f.ID, ".Parent") {
						prefix := strings.TrimSuffix(f.ID, "Parent")
						f.Kind = f4settings.ChoiceKind
						f.Label = f4settings.Text{English: "Parent submenu"}
						f.Choices = []f4settings.Choice{{Value: "", Label: f4settings.Text{English: "Root"}}}
						for _, parent := range s.draft.Records[col.ID] {
							if parent.ID != record.ID && parent.Values[prefix+"Submenu"] == "true" {
								f.Choices = append(f.Choices, f4settings.Choice{Value: parent.ID, Label: f4settings.Text{English: parent.Values[prefix+"Label"], Literal: true}})
							}
						}
					}
					f.Category = category
					f.Group = col.Group
					r := &settingsRow{field: f, session: s, match: true}
					r.values = func() map[string]string { return record.Values }
					r.matchFunc = func() bool {
						meta := f
						meta.Aliases = append(append([]string(nil), f.Aliases...), record.Values[col.NameField])
						return c.matches(meta)
					}
					r.read = func() string { return record.Values[f.ID] }
					r.write = func(value string) {
						record.Values[f.ID] = value
						if col.ID == "bookmarks" {
							record.Values["bookmark.Name"] = fmt.Sprintf("%d: %s", selected, record.Values["bookmark.Path"])
						}
					}
					r.control = c.makeControl(r)
					r.control.SetId("record-field:" + col.ID + ":" + f.ID)
					if help, ok := r.control.(interface{ SetHelp(string) }); ok {
						help.SetHelp("Setting." + f.ID)
					}
					c.page.AddItem(r.control)
					c.page.rows = append(c.page.rows, r)
				}
			}
		}
	}
}

func (c *settingsCenter) addCommands(category string) {
	for _, s := range c.sessions {
		for _, cmd := range s.catalog.Commands {
			if cmd.Category != category {
				continue
			}
			f := f4settings.Field{ID: cmd.ID, Category: category, Group: cmd.Group, Label: cmd.Label, Description: cmd.Description}
			button := vtui.NewButton(0, 0, cmd.Label.Resolve(config.App.Language, i18n.Msg))
			f.Enabled = func(map[string]string) string { return c.commandReason(cmd.Requires) }
			button.OnClick = func() {
				if reason := c.commandReason(cmd.Requires); reason != "" {
					c.status = reason
					return
				}
				if s.contributed && !settingsProviderAlive(s.provider) {
					c.status = Phrase("Provider is no longer loaded.")
					return
				}
				if cmd.Background {
					c.runBackground(cmd.Run, func(err error) {
						if err == nil && cmd.ID == "syntax.download" {
							c.refreshSchemeChoices()
						}
					})
					return
				}
				if err := cmd.Run(context.Background()); err != nil {
					c.status = err.Error()
				} else if cmd.ID == "syntax.reload" {
					c.refreshSchemeChoices()
				}
			}
			button.SetId("settings-command:" + cmd.ID)
			c.page.AddItem(button)
			c.page.rows = append(c.page.rows, &settingsRow{field: f, session: s, control: button, match: true})
		}
	}
}

func settingsCollectionField(col f4settings.Collection) f4settings.Field {
	f := f4settings.Field{Category: col.Category, Group: col.Group, Label: col.Label, Description: col.Description}
	for _, field := range col.Fields {
		f.Aliases = append(f.Aliases, field.Label.Resolve(config.App.Language, i18n.Msg), field.Description.Resolve(config.App.Language, i18n.Msg))
		for _, choice := range field.Choices {
			f.Aliases = append(f.Aliases, choice.Label.Resolve(config.App.Language, i18n.Msg))
		}
	}
	return f
}
func settingsCollectionMatches(c *settingsCenter, s *settingsSession, col f4settings.Collection) bool {
	if strings.TrimSpace(c.query) == "" {
		return true
	}
	meta := settingsCollectionField(col)
	if c.matches(meta) {
		return true
	}
	for _, record := range s.draft.Records[col.ID] {
		f := meta
		f.Aliases = append(append([]string(nil), meta.Aliases...), record.Values[col.NameField])
		if c.matches(f) {
			return true
		}
	}
	return false
}

func settingsSingleLine(value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return settingsError("value must be on one line")
	}
	return nil
}
