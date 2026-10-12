package theme

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type AttrFlags uint32

const (
	AttrDirectory AttrFlags = 1 << iota
	AttrHidden
	AttrExecutable
	AttrReadOnly
	AttrSystem
	AttrArchive
	AttrSymlink
	AttrJunction
)

type DateType int

const (
	DateModified DateType = iota
	DateCreated
	DateAccessed
)

type HighlightRule struct {
	Name              string
	SortGroup         int
	HasSortGroup      bool
	Masks             []string
	AttrSet           AttrFlags
	AttrClear         AttrFlags
	IgnoreCase        bool
	NormalStr         string
	SelectedStr       string
	CursorStr         string
	SelectedCursorStr string
	Mark              string

	// Фильтрация по размеру (0 означает, что лимит не задан)
	SizeAbove int64
	SizeBelow int64

	// Фильтрация по датам
	DateType      DateType
	DateAfter     time.Time
	DateBefore    time.Time
	DateAfterDur  time.Duration
	DateBeforeDur time.Duration
	DateRelative  bool

	// Каскадная обработка (Continue Processing)
	ContinueProcessing bool

	// UseDefaults makes the rule an override of the built-in colours of its
	// attribute (f4#912): it does not take part in the first-match walk, so its
	// place in the file does not matter, and it paints only the colour states it
	// names, leaving the rest to the rules of the active colour style.
	UseDefaults bool
	// implicitClear is set by CombineRules on an override: the attributes that
	// outrank the rule's own (see overrideTier), so a Directory override does
	// not repaint a symlink to a directory.
	implicitClear AttrFlags
}

type FileHighlighter struct {
	UserRules  []HighlightRule
	ThemeRules []HighlightRule
	// Rules is the first-match list. overrides are the UseDefaults rules of the
	// user's file, in the order they are applied (f4#912); a matched index at or
	// above len(Rules) names overrides[index-len(Rules)].
	Rules     []HighlightRule
	overrides []HighlightRule

	// matchCache and hasVolatileRules are GetColor/GetMarker's cache over
	// HighlightRule.Match, added for #884: vtui's render loop asks the
	// active panel to redraw every frame, and every visible row's Match
	// walked every configured rule's filepath.Match(mask, name) again on
	// every single frame even though neither the file nor the ruleset had
	// changed since the last frame. A CPU profile from a slow-SSH session
	// with many files on the panels put path/filepath.Match at 75%
	// cumulative time, called from HighlightRule.Match by way of GetMarker
	// and GetColor — this is that hot path.
	//
	// The cache is reset (see CombineRules) whenever Rules itself is
	// rebuilt, i.e. on theme switch or rule reload, which is the only way
	// fh.Rules's identity changes; that mirrors how the #884 menu-bar cache
	// (PanelsFrame.BuildMenuItems) invalidates on whatever actually changed
	// rather than on a timer.
	matchCache       map[highlightCacheKey][]int
	hasVolatileRules bool
}

// highlightCacheMaxEntries bounds matchCache's size. GlobalFileHighlighter
// lives for the whole process, so without a cap a long session that visits
// many distinct directories would let the cache grow forever; when the cap
// is hit the whole map is dropped and starts over, which is simple and, since
// hit rate depends only on the files shown on screen right now, costs
// nothing beyond a handful of cache misses right after the reset.
const highlightCacheMaxEntries = 4096

// highlightCacheKey captures every field HighlightRule.Match reads off a
// vfs.VFSItem. Two items with equal keys are, by construction, items Match
// cannot tell apart — Match is a pure function of exactly these fields (plus
// the rule set and, for a DateRelative rule, the current time, which is why
// hasVolatileRules bypasses the cache instead of trying to key on "now") — so
// caching on this key alone can never serve one item's result for another.
type highlightCacheKey struct {
	name         string
	isDir        bool
	isHidden     bool
	isExecutable bool
	isSymlink    bool
	winAttrs     uint32
	unixMode     uint32
	size         int64
	mtimeNano    int64
	ctimeNano    int64
	atimeNano    int64
}

func highlightCacheKeyFor(item *vfs.VFSItem) highlightCacheKey {
	return highlightCacheKey{
		name:         item.Name,
		isDir:        item.IsDir,
		isHidden:     item.IsHidden,
		isExecutable: item.IsExecutable,
		isSymlink:    item.IsSymlink,
		winAttrs:     item.WinAttrs,
		unixMode:     item.UnixMode,
		size:         item.Size,
		mtimeNano:    item.MTime.UnixNano(),
		ctimeNano:    item.CTime.UnixNano(),
		atimeNano:    item.ATime.UnixNano(),
	}
}

var GlobalFileHighlighter *FileHighlighter

func init() {
	GlobalFileHighlighter = &FileHighlighter{}
}

func (fh *FileHighlighter) LoadFromIni(ini *ini.File) {
	fh.LoadUserRules(ini)
}

func (fh *FileHighlighter) LoadUserRules(ini *ini.File) {
	fh.UserRules = ParseHighlightRules(ini)
	fh.CombineRules()
}

func (fh *FileHighlighter) LoadThemeRules(ini *ini.File) {
	fh.ThemeRules = ParseHighlightRules(ini)
	fh.CombineRules()
}

// Attribute tiers of the UseDefaults overrides, highest first (f4#912): a
// junction outranks a symlink, a symlink outranks a hidden or system item, and
// those outrank an ordinary directory. A rule that names none of them is
// outside the ladder and is applied first, under all of them.
const (
	tierJunction = iota
	tierSymlink
	tierHiddenSystem
	tierDirectory
	tierOther
)

func overrideTier(r *HighlightRule) int {
	switch {
	case r.AttrSet&AttrJunction != 0:
		return tierJunction
	case r.AttrSet&AttrSymlink != 0:
		return tierSymlink
	case r.AttrSet&(AttrHidden|AttrSystem) != 0:
		return tierHiddenSystem
	case r.AttrSet&AttrDirectory != 0:
		return tierDirectory
	}
	return tierOther
}

// outranking is what an override of the given tier must not match: the
// attributes of the tiers above it.
func outranking(tier int) AttrFlags {
	var flags AttrFlags
	if tier > tierJunction && tier != tierOther {
		flags |= AttrJunction
	}
	if tier > tierSymlink && tier != tierOther {
		flags |= AttrSymlink
	}
	if tier > tierHiddenSystem && tier != tierOther {
		flags |= AttrHidden | AttrSystem
	}
	return flags
}

func (fh *FileHighlighter) CombineRules() {
	fh.Rules = nil
	var plain, overrides []HighlightRule
	for _, r := range fh.UserRules {
		if r.UseDefaults {
			r.implicitClear = outranking(overrideTier(&r))
			overrides = append(overrides, r)
		} else {
			plain = append(plain, r)
		}
	}
	if config.App.HighlightPriority == 1 { // Theme wins
		fh.Rules = append(fh.Rules, fh.ThemeRules...)
		fh.Rules = append(fh.Rules, plain...)
	} else { // User wins
		fh.Rules = append(fh.Rules, plain...)
		fh.Rules = append(fh.Rules, fh.ThemeRules...)
	}
	// The overrides are applied last, lowest tier first and, inside a tier,
	// the later section first, so that the highest tier and the earliest
	// section decide a colour both name.
	sort.SliceStable(overrides, func(i, j int) bool {
		return overrideTier(&overrides[i]) > overrideTier(&overrides[j])
	})
	for start := 0; start < len(overrides); {
		end := start
		for end < len(overrides) && overrideTier(&overrides[end]) == overrideTier(&overrides[start]) {
			end++
		}
		for i, j := start, end-1; i < j; i, j = i+1, j-1 {
			overrides[i], overrides[j] = overrides[j], overrides[i]
		}
		start = end
	}
	fh.overrides = overrides

	// Rules just got a new identity (theme switch or rule reload is the only
	// way CombineRules runs), so every entry matchCache holds was matched
	// against a ruleset that no longer applies. Drop it rather than try to
	// key around it: nil is enough, matchedRulesCached allocates lazily.
	fh.matchCache = nil

	// A rule whose DateRelative window is set reads time.Now() inside
	// Match, so its answer for the very same file drifts on its own as the
	// clock advances — caching that against the file's own attributes would
	// go stale without any file, theme or rule actually changing. That is
	// rare enough (most highlight rules only match name masks and static
	// attributes) that bypassing the cache entirely for the whole ruleset
	// whenever it happens is simpler, and safer, than trying to add a time
	// bucket to the cache key.
	fh.hasVolatileRules = false
	for _, r := range append(append([]HighlightRule(nil), fh.Rules...), fh.overrides...) {
		if r.DateRelative && (r.DateAfterDur > 0 || r.DateBeforeDur > 0) {
			fh.hasVolatileRules = true
			break
		}
	}
}

// matchedRules returns, in order, the indices into fh.Rules of every rule
// that GetColor/GetMarker would actually consult for item: each rule whose
// Match(item) is true, stopping right after the first such rule whose
// ContinueProcessing is false (both callers already stop there today), or
// running to the end of Rules if every match along the way cascades. Rules
// that do not match are skipped over, exactly as both callers' own loops
// already did, so this trace is the one piece of work GetColor and GetMarker
// actually share, and the one worth caching: it is where every
// filepath.Match call the profile for #884 found happens.
func (fh *FileHighlighter) matchedRules(item *vfs.VFSItem) []int {
	var matched []int
	for i := range fh.Rules {
		if fh.Rules[i].Match(item) {
			matched = append(matched, i)
			if !fh.Rules[i].ContinueProcessing {
				break
			}
		}
	}
	// UseDefaults overrides come on top of whatever the walk found, wherever
	// the walk stopped (f4#912).
	for i := range fh.overrides {
		if fh.overrides[i].Match(item) {
			matched = append(matched, len(fh.Rules)+i)
		}
	}
	return matched
}

// matchedRulesCached is matchedRules with GetColor/GetMarker's cache in
// front of it, keyed by highlightCacheKeyFor(item). A hit returns the exact
// slice computed last time without touching a single rule's Match/
// filepath.Match; nothing here mutates the returned slice afterwards.
func (fh *FileHighlighter) matchedRulesCached(item *vfs.VFSItem) []int {
	if fh.hasVolatileRules {
		return fh.matchedRules(item)
	}
	key := highlightCacheKeyFor(item)
	if fh.matchCache != nil {
		if cached, ok := fh.matchCache[key]; ok {
			return cached
		}
	} else {
		fh.matchCache = make(map[highlightCacheKey][]int)
	}
	if len(fh.matchCache) >= highlightCacheMaxEntries {
		fh.matchCache = make(map[highlightCacheKey][]int)
	}
	matched := fh.matchedRules(item)
	fh.matchCache[key] = matched
	return matched
}

// ruleSection pairs a parsed rule with the ini section it came from. The
// section name is retained for legacy [SortGroup_N] sections; ordinary
// [Highlight_N] rules carry their Name and Group in HighlightRule itself.
type ruleSection struct {
	Section string
	Rule    HighlightRule
}

func ParseHighlightRules(ini *ini.File) []HighlightRule {
	sections := ParseRuleSections(ini, "highlight_")
	rules := make([]HighlightRule, 0, len(sections))
	for _, section := range sections {
		rules = append(rules, section.Rule)
	}
	return rules
}

// ParseRuleSections reads every "<prefix>N" section into a HighlightRule,
// ordered by the numeric suffix. Highlighting and sort groups share this
// parser so both accept the same mask, attribute, size and date keys; the
// colour keys are simply left empty for rules that do not use them.
func ParseRuleSections(ini *ini.File, prefix string) []ruleSection {
	var rules []ruleSection
	var sections []string
	for secName := range ini.Sections() {
		if strings.HasPrefix(strings.ToLower(secName), prefix) {
			sections = append(sections, secName)
		}
	}
	sort.Slice(sections, func(i, j int) bool {
		idxI, _ := strconv.Atoi(strings.TrimPrefix(strings.ToLower(sections[i]), prefix))
		idxJ, _ := strconv.Atoi(strings.TrimPrefix(strings.ToLower(sections[j]), prefix))
		return idxI < idxJ
	})

	for _, secName := range sections {
		rule := HighlightRule{
			IgnoreCase: true,
			Name:       ini.GetString(secName, "Name", ""),
		}
		if raw := strings.TrimSpace(ini.GetString(secName, "Group", "")); raw != "" {
			if group, err := strconv.Atoi(raw); err == nil {
				rule.SortGroup = group
				rule.HasSortGroup = true
			}
		}
		maskStr := ini.GetString(secName, "Mask", "")
		if maskStr != "" {
			rawMasks := strings.Split(maskStr, ",")
			for _, m := range rawMasks {
				m = strings.TrimSpace(m)
				if m == "*.*" {
					m = "*"
				}
				if m != "" {
					rule.Masks = append(rule.Masks, m)
				}
			}
		} else {
			rule.Masks = []string{"*"}
		}

		attrInclude := strings.ToLower(ini.GetString(secName, "IncludeAttributes", ""))
		attrExclude := strings.ToLower(ini.GetString(secName, "ExcludeAttributes", ""))
		rule.AttrSet = parseAttrFlags(attrInclude)
		rule.AttrClear = parseAttrFlags(attrExclude)

		// Чтение размеров
		sizeAboveStr := ini.GetString(secName, "SizeAbove", "")
		if sizeAboveStr != "" {
			fmt.Sscanf(sizeAboveStr, "%d", &rule.SizeAbove)
		}
		sizeBelowStr := ini.GetString(secName, "SizeBelow", "")
		if sizeBelowStr != "" {
			fmt.Sscanf(sizeBelowStr, "%d", &rule.SizeBelow)
		}

		// Чтение дат
		dateTypeStr := strings.ToLower(ini.GetString(secName, "DateType", ""))
		switch dateTypeStr {
		case "create", "created", "c":
			rule.DateType = DateCreated
		case "access", "accessed", "a":
			rule.DateType = DateAccessed
		default:
			rule.DateType = DateModified
		}

		rule.DateRelative = ini.GetString(secName, "DateRelative", "0") == "1"

		parseDuration := func(s string) (time.Duration, error) {
			s = strings.TrimSpace(s)
			if strings.HasSuffix(s, "d") {
				daysStr := strings.TrimSuffix(s, "d")
				days, err := strconv.Atoi(daysStr)
				if err != nil {
					return 0, err
				}
				return time.Duration(days) * 24 * time.Hour, nil
			}
			return time.ParseDuration(s)
		}

		dateAfterStr := ini.GetString(secName, "DateAfter", "")
		if dateAfterStr != "" {
			if rule.DateRelative {
				if dur, err := parseDuration(dateAfterStr); err == nil {
					rule.DateAfterDur = dur
				}
			} else {
				if t, err := time.Parse("2006-01-02 15:04:05", dateAfterStr); err == nil {
					rule.DateAfter = t
				}
			}
		}

		dateBeforeStr := ini.GetString(secName, "DateBefore", "")
		if dateBeforeStr != "" {
			if rule.DateRelative {
				if dur, err := parseDuration(dateBeforeStr); err == nil {
					rule.DateBeforeDur = dur
				}
			} else {
				if t, err := time.Parse("2006-01-02 15:04:05", dateBeforeStr); err == nil {
					rule.DateBefore = t
				}
			}
		}

		rule.ContinueProcessing = ini.GetString(secName, "ContinueProcessing", "0") == "1"
		rule.UseDefaults = ini.GetString(secName, "UseDefaults", "0") == "1"

		rule.Mark = firstIniValue(ini, secName, "Mark", "MarkChar")

		// Each of the four colours answers to several spellings. Far Manager
		// names them after what they paint ("File name under cursor") and a
		// group copied out of its Files highlighting dialog should work here
		// as written, so those names are accepted next to f4's own (#912).
		rule.NormalStr = firstIniValue(ini, secName, "NormalColor", "NormalFileName")
		rule.SelectedStr = firstIniValue(ini, secName, "SelectedColor", "SelectedFileName")
		rule.CursorStr = firstIniValue(ini, secName,
			"CursorColor", "NormalColorUnderCursor", "FileNameUnderCursor")
		rule.SelectedCursorStr = firstIniValue(ini, secName,
			"SelectedCursorColor", "SelectedColorUnderCursor", "FileNameSelectedUnderCursor")
		rules = append(rules, ruleSection{Section: secName, Rule: rule})
	}
	return rules
}

// firstIniValue returns the value of the first of the given keys that the
// section actually sets, so one setting can be written under any of its
// accepted names. Keys are tried in order, the earlier name winning when a
// section spells the same colour twice.
func firstIniValue(ini *ini.File, section string, keys ...string) string {
	for _, key := range keys {
		if val := ini.GetString(section, key, ""); val != "" {
			return val
		}
	}
	return ""
}

func parseAttrFlags(s string) AttrFlags {
	var flags AttrFlags
	parts := strings.Split(s, ",")
	for _, p := range parts {
		switch strings.TrimSpace(p) {
		case "directory", "dir", "d":
			flags |= AttrDirectory
		case "hidden", "h":
			flags |= AttrHidden
		case "executable", "exec", "e":
			flags |= AttrExecutable
		case "readonly", "ro":
			flags |= AttrReadOnly
		case "system", "sys":
			flags |= AttrSystem
		case "archive", "arc":
			flags |= AttrArchive
		case "symlink", "link", "sym", "l":
			flags |= AttrSymlink
		case "junction", "junc", "j":
			flags |= AttrJunction
		}
	}
	return flags
}

// filepathMatchFn is the filepath.Match seam highlight_cache_test.go
// substitutes with a counting wrapper, the same pattern
// internal/panel/menu_cache_test.go already uses for BuildMenuBarItems.
// Production code always leaves it as filepath.Match.
var filepathMatchFn = filepath.Match

func (r *HighlightRule) Match(item *vfs.VFSItem) bool {
	// Определение платформозависимых флагов "на лету"
	isReadOnly := false
	isSystem := false
	isArchive := false
	if runtime.GOOS == "windows" {
		isReadOnly = item.WinAttrs&1 != 0 // FILE_ATTRIBUTE_READONLY
		isSystem = item.WinAttrs&4 != 0   // FILE_ATTRIBUTE_SYSTEM
		isArchive = item.WinAttrs&32 != 0 // FILE_ATTRIBUTE_ARCHIVE
	} else {
		isReadOnly = item.UnixMode&0222 == 0 // Нет прав на запись
	}

	matchAttr := func(flag AttrFlags, set bool) bool {
		switch flag {
		case AttrDirectory:
			return item.IsDir == set
		case AttrHidden:
			return item.IsHidden == set
		case AttrExecutable:
			// On Unix every directory carries the x bit (it means "may be
			// entered", not "may be run"), so a bare IsExecutable check would
			// light up all folders (#419). The rule attribute means
			// "executable program", which a directory never is.
			return (item.IsExecutable && !item.IsDir) == set
		case AttrReadOnly:
			return isReadOnly == set
		case AttrSystem:
			return isSystem == set
		case AttrArchive:
			return isArchive == set
		case AttrSymlink:
			return item.IsSymlink == set
		case AttrJunction:
			// Only a Windows directory junction or volume mount point;
			// Symlink keeps matching every link, a junction included.
			return (vfs.LinkKindOf(item) == vfs.LinkJunction) == set
		}
		return true
	}

	// Проверка AttrSet (должны присутствовать)
	for _, f := range []AttrFlags{AttrDirectory, AttrHidden, AttrExecutable, AttrReadOnly, AttrSystem, AttrArchive, AttrSymlink, AttrJunction} {
		if r.AttrSet&f != 0 && !matchAttr(f, true) {
			return false
		}
	}

	// Проверка AttrClear (должны отсутствовать)
	for _, f := range []AttrFlags{AttrDirectory, AttrHidden, AttrExecutable, AttrReadOnly, AttrSystem, AttrArchive, AttrSymlink, AttrJunction} {
		if (r.AttrClear|r.implicitClear)&f != 0 && !matchAttr(f, false) {
			return false
		}
	}

	// Фильтрация по размеру
	if r.SizeAbove > 0 && item.Size < r.SizeAbove {
		return false
	}
	if r.SizeBelow > 0 && item.Size > r.SizeBelow {
		return false
	}

	// Фильтрация по датам
	if !r.DateAfter.IsZero() || r.DateAfterDur > 0 || !r.DateBefore.IsZero() || r.DateBeforeDur > 0 {
		var t time.Time
		switch r.DateType {
		case DateCreated:
			t = item.CTime
		case DateAccessed:
			t = item.ATime
		default:
			t = item.MTime
		}

		if r.DateRelative {
			if r.DateAfterDur > 0 && t.Before(time.Now().Add(-r.DateAfterDur)) {
				return false
			}
			if r.DateBeforeDur > 0 && t.After(time.Now().Add(-r.DateBeforeDur)) {
				return false
			}
		} else {
			if !r.DateAfter.IsZero() && t.Before(r.DateAfter) {
				return false
			}
			if !r.DateBefore.IsZero() && t.After(r.DateBefore) {
				return false
			}
		}
	}

	// Проверка по маске имени файла
	if len(r.Masks) == 0 {
		return true
	}
	name := item.Name
	if r.IgnoreCase {
		name = strings.ToLower(name)
	}
	for _, mask := range r.Masks {
		m := mask
		if r.IgnoreCase {
			m = strings.ToLower(m)
		}
		matched, err := filepathMatchFn(m, name)
		if err == nil && matched {
			return true
		}
	}
	return false
}

// colorFor is the colour expression a rule keeps for one of the four states.
// Each of the four states answers only to its own key, as in far2l, where
// every state starts from its own panel colour (hilight.cpp, FarColor[]). A
// selected file under the cursor that fell back to SelectedColor was painted
// exactly like the selection around it once that colour had a background, and
// the cursor disappeared (#1150).
func (r *HighlightRule) colorFor(isSelected, isCursor bool) string {
	switch {
	case isCursor && isSelected:
		return r.SelectedCursorStr
	case isCursor:
		return r.CursorStr
	case isSelected:
		return r.SelectedStr
	}
	return r.NormalStr
}

// ruleAt resolves an index from matchedRules into either list.
func (fh *FileHighlighter) ruleAt(idx int) *HighlightRule {
	if idx >= len(fh.Rules) {
		return &fh.overrides[idx-len(fh.Rules)]
	}
	return &fh.Rules[idx]
}

func (fh *FileHighlighter) GetColor(item *vfs.VFSItem, defaultAttr uint64, isSelected, isCursor bool) uint64 {
	if item.Name == ".." {
		return defaultAttr
	}
	attr := defaultAttr
	matchedAny := false

	// matchedRulesCached does the part of this that used to run
	// filepath.Match against every rule on every call — the traversal it
	// returns already stops exactly where the loop below used to return
	// (at the first non-cascading match, or at the end of Rules), because
	// that stopping point never depended on isSelected/isCursor/defaultAttr
	// to begin with, only on rule.Match and rule.ContinueProcessing (#884).
	for _, idx := range fh.matchedRulesCached(item) {
		rule := fh.ruleAt(idx)
		colorExpr := rule.colorFor(isSelected, isCursor)
		if colorExpr != "" {
			attr = ParseFarColor(colorExpr, attr)
			matchedAny = true
		}
	}

	if matchedAny {
		if config.App.EnforceColorCorrection {
			fg, bg := GetColorRGBBoth(attr)
			nfg := CorrectContrast(fg, bg)
			if nfg != fg {
				attr = vtui.SetRGBFore(attr, nfg)
			}
		}
		return attr
	}
	return defaultAttr
}

// GetMarker возвращает символ пометки для файла от первого совпавшего правила.
func (fh *FileHighlighter) GetMarker(item *vfs.VFSItem) string {
	if item.Name == ".." {
		return ""
	}
	// See GetColor: matchedRulesCached's trace already stops exactly where
	// this loop used to (the first non-cascading match, or the end of
	// Rules), so replaying it here needs no ContinueProcessing check of its
	// own.
	matched := fh.matchedRulesCached(item)
	// A UseDefaults override is applied last, so its mark wins; the one applied
	// last is the one that decides.
	for i := len(matched) - 1; i >= 0 && matched[i] >= len(fh.Rules); i-- {
		if mark := fh.ruleAt(matched[i]).Mark; mark != "" {
			return mark
		}
	}
	for _, idx := range matched {
		if idx >= len(fh.Rules) {
			break
		}
		if fh.Rules[idx].Mark != "" {
			return fh.Rules[idx].Mark
		}
	}
	return ""
}
