package main

import (
	"strconv"
	"strings"

	"github.com/unxed/f4/vfs"
)

// defaultSortGroupOrder is the group number every file that matches no rule
// falls into. far/far2l use the same idea (DEFAULT_SORT_GROUP): the number is
// deliberately large so unclassified files land after the configured groups,
// while a user who wants a group *below* them can still say Group = 20000.
const defaultSortGroupOrder = 10000

// SortGroupRule is one entry of the sort-group list. The matching criteria are
// exactly those of a highlight rule (mask, attributes, size, dates), so a user
// who already knows highlight.ini needs no second syntax; only the colour keys
// are ignored here.
type SortGroupRule struct {
	Name   string
	Order  int
	Filter HighlightRule
}

// SortGroupSet holds the configured groups in the order they must appear on
// the panel.
type SortGroupSet struct {
	Groups []SortGroupRule
}

// GlobalSortGroups is populated from highlight.ini at startup. It stays empty
// when the user configured no groups, which turns the whole feature into a
// no-op even for panels that have grouping switched on.
var GlobalSortGroups *SortGroupSet

func init() {
	GlobalSortGroups = &SortGroupSet{}
}

func (s *SortGroupSet) LoadFromIni(ini *IniFile) {
	if s == nil {
		return
	}
	s.Groups = parseSortGroups(ini)
}

// Configured reports whether any group is defined. Grouping a panel by an
// empty rule list would put every file into the default group, which is just
// the ungrouped order with extra work.
func (s *SortGroupSet) Configured() bool {
	return s != nil && len(s.Groups) > 0
}

// GroupOf returns the sort-group number of an item: the Order of the first
// matching rule, or defaultSortGroupOrder when nothing matches. First match
// wins, so an earlier, narrower rule can carve items out of a later one.
func (s *SortGroupSet) GroupOf(item *vfs.VFSItem) int {
	if s == nil || item == nil {
		return defaultSortGroupOrder
	}
	for i := range s.Groups {
		if s.Groups[i].Filter.Match(item) {
			return s.Groups[i].Order
		}
	}
	return defaultSortGroupOrder
}

// parseSortGroups reads the [SortGroup_N] sections. The section number decides
// the default order, so the plain case — SortGroup_1, SortGroup_2, … — needs no
// Group key at all.
func parseSortGroups(ini *IniFile) []SortGroupRule {
	sections := parseRuleSections(ini, "sortgroup_")
	groups := make([]SortGroupRule, 0, len(sections))
	for i, section := range sections {
		group := SortGroupRule{
			Name:   ini.GetString(section.Section, "Name", ""),
			Order:  i,
			Filter: section.Rule,
		}
		if raw := strings.TrimSpace(ini.GetString(section.Section, "Group", "")); raw != "" {
			if order, err := strconv.Atoi(raw); err == nil {
				group.Order = order
			}
		}
		if group.Name == "" {
			group.Name = strings.Join(group.Filter.Masks, ", ")
		}
		groups = append(groups, group)
	}
	return groups
}
