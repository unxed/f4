package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

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
)

type DateType int

const (
	DateModified DateType = iota
	DateCreated
	DateAccessed
)

type HighlightRule struct {
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
}

type FileHighlighter struct {
	UserRules  []HighlightRule
	ThemeRules []HighlightRule
	Rules      []HighlightRule
}

var GlobalFileHighlighter *FileHighlighter

func init() {
	GlobalFileHighlighter = &FileHighlighter{}
}

func (fh *FileHighlighter) LoadFromIni(ini *IniFile) {
	fh.LoadUserRules(ini)
}

func (fh *FileHighlighter) LoadUserRules(ini *IniFile) {
	fh.UserRules = parseHighlightRules(ini)
	fh.CombineRules()
}

func (fh *FileHighlighter) LoadThemeRules(ini *IniFile) {
	fh.ThemeRules = parseHighlightRules(ini)
	fh.CombineRules()
}

func (fh *FileHighlighter) CombineRules() {
	fh.Rules = nil
	if AppConfig.HighlightPriority == 1 { // Theme wins
		fh.Rules = append(fh.Rules, fh.ThemeRules...)
		fh.Rules = append(fh.Rules, fh.UserRules...)
	} else { // User wins
		fh.Rules = append(fh.Rules, fh.UserRules...)
		fh.Rules = append(fh.Rules, fh.ThemeRules...)
	}
}

// ruleSection pairs a parsed rule with the ini section it came from, so a
// caller that needs section-local keys (sort groups read Name and Group) can
// still reach them after the shared matcher fields have been parsed.
type ruleSection struct {
	Section string
	Rule    HighlightRule
}

func parseHighlightRules(ini *IniFile) []HighlightRule {
	sections := parseRuleSections(ini, "highlight_")
	rules := make([]HighlightRule, 0, len(sections))
	for _, section := range sections {
		rules = append(rules, section.Rule)
	}
	return rules
}

// parseRuleSections reads every "<prefix>N" section into a HighlightRule,
// ordered by the numeric suffix. Highlighting and sort groups share this
// parser so both accept the same mask, attribute, size and date keys; the
// colour keys are simply left empty for rules that do not use them.
func parseRuleSections(ini *IniFile, prefix string) []ruleSection {
	var rules []ruleSection
	var sections []string
	for secName := range ini.data {
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

		rule.Mark = ini.GetString(secName, "Mark", "")
		if rule.Mark == "" {
			rule.Mark = ini.GetString(secName, "MarkChar", "")
		}

		rule.NormalStr = ini.GetString(secName, "NormalColor", "")
		rule.SelectedStr = ini.GetString(secName, "SelectedColor", "")
		rule.CursorStr = ini.GetString(secName, "CursorColor", "")
		rule.SelectedCursorStr = ini.GetString(secName, "SelectedCursorColor", "")
		if rule.CursorStr == "" {
			rule.CursorStr = ini.GetString(secName, "NormalColorUnderCursor", "")
		}
		if rule.SelectedCursorStr == "" {
			rule.SelectedCursorStr = ini.GetString(secName, "SelectedColorUnderCursor", "")
		}
		rules = append(rules, ruleSection{Section: secName, Rule: rule})
	}
	return rules
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
		}
	}
	return flags
}

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
		}
		return true
	}

	// Проверка AttrSet (должны присутствовать)
	for _, f := range []AttrFlags{AttrDirectory, AttrHidden, AttrExecutable, AttrReadOnly, AttrSystem, AttrArchive, AttrSymlink} {
		if r.AttrSet&f != 0 && !matchAttr(f, true) {
			return false
		}
	}

	// Проверка AttrClear (должны отсутствовать)
	for _, f := range []AttrFlags{AttrDirectory, AttrHidden, AttrExecutable, AttrReadOnly, AttrSystem, AttrArchive, AttrSymlink} {
		if r.AttrClear&f != 0 && !matchAttr(f, false) {
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
		matched, err := filepath.Match(m, name)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func (fh *FileHighlighter) GetColor(item *vfs.VFSItem, defaultAttr uint64, isSelected, isCursor bool) uint64 {
	if item.Name == ".." {
		return defaultAttr
	}
	attr := defaultAttr
	matchedAny := false

	for _, rule := range fh.Rules {
		if rule.Match(item) {
			colorExpr := ""
			if isCursor {
				if isSelected {
					if rule.SelectedCursorStr != "" {
						colorExpr = rule.SelectedCursorStr
					} else if rule.SelectedStr != "" {
						colorExpr = rule.SelectedStr
					}
				} else {
					if rule.CursorStr != "" {
						colorExpr = rule.CursorStr
					}
				}
			} else if isSelected {
				if rule.SelectedStr != "" {
					colorExpr = rule.SelectedStr
				}
			} else {
				if rule.NormalStr != "" {
					colorExpr = rule.NormalStr
				}
			}

			if colorExpr != "" {
				attr = ParseFarColor(colorExpr, attr)
				matchedAny = true
			}

			// Если каскадная обработка выключена, сразу возвращаем результат
			if !rule.ContinueProcessing {
				if matchedAny {
					if AppConfig.EnforceColorCorrection {
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
		}
	}

	if matchedAny {
		if AppConfig.EnforceColorCorrection {
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
	for _, rule := range fh.Rules {
		if rule.Match(item) {
			if rule.Mark != "" {
				return rule.Mark
			}
			if !rule.ContinueProcessing {
				break
			}
		}
	}
	return ""
}
