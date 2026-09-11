package i18n

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/abadojack/whatlanggo"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/internal/testutil"
)

func hasHotkey(s string) bool {
	s = strings.ReplaceAll(s, "&&", "")
	idx := strings.Index(s, "&")
	if idx == -1 || idx == len(s)-1 {
		return false
	}
	nextChar := []rune(s[idx+1:])[0]
	return unicode.IsLetter(nextChar) || unicode.IsDigit(nextChar)
}

const langCoverageBaselinePath = "lang/coverage_baseline.txt"

func loadLangCoverageBaseline(data []byte) (map[string]int, error) {
	baseline := make(map[string]int)
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("line %d: expected language=count", lineNumber+1)
		}
		code := strings.TrimSpace(parts[0])
		if code == "" {
			return nil, fmt.Errorf("line %d: language code is empty", lineNumber+1)
		}
		if _, exists := baseline[code]; exists {
			return nil, fmt.Errorf("line %d: duplicate language %q", lineNumber+1, code)
		}
		count, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || count < 0 {
			return nil, fmt.Errorf("line %d: invalid coverage count for %q", lineNumber+1, code)
		}
		baseline[code] = count
	}
	return baseline, nil
}

func writeLangCoverageBaseline(path string, coverage map[string]int) error {
	codes := make([]string, 0, len(coverage))
	for code := range coverage {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	var builder strings.Builder
	for _, code := range codes {
		if _, err := fmt.Fprintf(&builder, "%s=%d\n", code, coverage[code]); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(builder.String()), 0600)
}

func TestLangConsistency(t *testing.T) {
	testutil.SkipIfNoRelevantChanges(t, "lang_consistency",
		"lang/*.lng",
		"lang/*.txt",
		"lang_consistency_test.go",
	)
	enData, err := os.ReadFile(filepath.Join("lang", "en.lng"))
	if err != nil {
		t.Fatalf("Failed to read en.lng: %v", err)
	}

	enIni := ini.Parse(bytes.NewReader(enData))
	enStrings := LoadLangMapFromINI(enIni)

	var enKeys []string
	enRawStrings := make(map[string]string)
	lines := strings.Split(string(enData), "\n")
	inStringsSection := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[Strings]") {
			inStringsSection = true
			continue
		} else if strings.HasPrefix(line, "[") {
			inStringsSection = false
			continue
		}
		if !inStringsSection || line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx > 0 {
			key := strings.TrimSpace(line[:idx])
			enRawStrings[key] = line[idx+1:]
			if _, ok := enStrings[key]; ok {
				found := false
				for _, k := range enKeys {
					if k == key {
						found = true
						break
					}
				}
				if !found {
					enKeys = append(enKeys, key)
				}
			}
		}
	}

	files, err := filepath.Glob(filepath.Join("lang", "*.lng"))
	if err != nil {
		t.Fatalf("Failed to glob lang/*.lng: %v", err)
	}
	// Glob reports no error for zero matches, so a wrong directory would leave
	// this test passing over an empty set.
	if len(files) == 0 {
		t.Fatal("no .lng files under lang/")
	}

	placeholderRe := regexp.MustCompile(`%[sdvq]`)
	baselineData, err := os.ReadFile(langCoverageBaselinePath)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", langCoverageBaselinePath, err)
	}
	baseline, err := loadLangCoverageBaseline(baselineData)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", langCoverageBaselinePath, err)
	}
	updateBaseline := os.Getenv("F4_UPDATE_COVERAGE_BASELINE") == "1"
	coverage := make(map[string]int)
	seenCodes := make(map[string]bool)

	for _, File := range files {
		data, err := os.ReadFile(File)
		if err != nil {
			t.Errorf("Failed to read %s: %v", File, err)
			continue
		}

		ini := ini.Parse(bytes.NewReader(data))
		stringsMap := LoadLangMapFromINI(ini)

		code := ini.GetString("Language", "Code", "")
		expectedCode := strings.TrimSuffix(filepath.Base(File), ".lng")
		if code != expectedCode {
			t.Errorf("%s: [Language] Code is '%s', expected '%s'", File, code, expectedCode)
		}
		if ini.GetString("Language", "Name", "") == "" {
			t.Errorf("%s: [Language] Name is missing", File)
		}
		seenCodes[code] = true

		missingKeys := make([]string, 0)
		for _, key := range enKeys {
			if _, ok := stringsMap[key]; ok {
				coverage[code]++
			} else {
				missingKeys = append(missingKeys, key)
			}
		}
		if !updateBaseline {
			if expected, ok := baseline[code]; !ok {
				t.Errorf("%s has no coverage baseline; add %s=<covered key count>", File, code)
			} else if coverage[code] < expected {
				t.Errorf("%s covers %d/%d English keys, baseline requires at least %d", code, coverage[code], len(enKeys), expected)
			}
		}
		if code != "en" {
			t.Logf("%s coverage: %d/%d (%.1f%%); missing keys (%d): %s", code, coverage[code], len(enKeys), 100*float64(coverage[code])/float64(len(enKeys)), len(missingKeys), strings.Join(missingKeys, ", "))
		}

		if filepath.Base(File) == "en.lng" {
			continue
		}

		seenKeys := make(map[string]bool)
		targetRawStrings := make(map[string]string)
		flines := strings.Split(string(data), "\n")
		targetInStringsSection := false
		for _, line := range flines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[Strings]") {
				targetInStringsSection = true
				continue
			} else if strings.HasPrefix(line, "[") {
				targetInStringsSection = false
				continue
			}
			if !targetInStringsSection || line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
				continue
			}
			idx := strings.Index(line, "=")
			if idx > 0 {
				key := strings.TrimSpace(line[:idx])
				targetRawStrings[key] = line[idx+1:]
				if seenKeys[key] {
					t.Errorf("%s: Duplicate key found: %s", File, key)
				}
				seenKeys[key] = true

				if _, ok := enStrings[key]; !ok {
					t.Errorf("%s: Key '%s' does not exist in en.lng", File, key)
				}
			} else {
				t.Errorf("%s: Invalid line without '=' in [Strings]: %s", File, line)
			}
		}

		mergedLineRe := regexp.MustCompile(`[a-zA-Z0-9_.-]+=`)
		allowedLangs := map[string][]whatlanggo.Lang{
			"ar": {whatlanggo.Arb, whatlanggo.Eng},
			"bn": {whatlanggo.Ben, whatlanggo.Eng},
			"be": {whatlanggo.Bel, whatlanggo.Rus, whatlanggo.Ukr, whatlanggo.Eng},
			"cs": {whatlanggo.Ces, whatlanggo.Pol, whatlanggo.Hrv, whatlanggo.Srp, whatlanggo.Slv, whatlanggo.Eng, whatlanggo.Deu, whatlanggo.Fra, whatlanggo.Ita, whatlanggo.Spa, whatlanggo.Por, whatlanggo.Hat, whatlanggo.Nld},
			"de": {whatlanggo.Deu, whatlanggo.Eng, whatlanggo.Nld, whatlanggo.Epo, whatlanggo.Fra, whatlanggo.Ita, whatlanggo.Spa},
			"pl": {whatlanggo.Pol, whatlanggo.Eng, whatlanggo.Ces, whatlanggo.Hrv, whatlanggo.Srp, whatlanggo.Slv, whatlanggo.Epo, whatlanggo.Deu, whatlanggo.Fra, whatlanggo.Ita, whatlanggo.Spa},
			"uk": {whatlanggo.Ukr, whatlanggo.Rus, whatlanggo.Bel, whatlanggo.Bul, whatlanggo.Srp, whatlanggo.Eng},
			"ru": {whatlanggo.Rus, whatlanggo.Ukr, whatlanggo.Bel, whatlanggo.Bul, whatlanggo.Srp, whatlanggo.Tuk, whatlanggo.Eng},
			"zh": {whatlanggo.Cmn, whatlanggo.Jpn, whatlanggo.Eng},
			"ja": {whatlanggo.Jpn, whatlanggo.Cmn, whatlanggo.Eng},
			"ko": {whatlanggo.Kor, whatlanggo.Eng},
			"ka": {whatlanggo.Kat, whatlanggo.Eng},
			"hu": {whatlanggo.Hun, whatlanggo.Eng},
			"fi": {whatlanggo.Fin, whatlanggo.Eng},
			"hy": {whatlanggo.Eng},
			"lt": {whatlanggo.Lit, whatlanggo.Eng},
			// Short Latvian UI explanations are sometimes classified as the
			// closely related Lithuanian, even with exclusively Latvian words.
			"lv": {whatlanggo.Lav, whatlanggo.Lit, whatlanggo.Eng},
			"et": {whatlanggo.Est, whatlanggo.Eng},
			"es": {whatlanggo.Spa, whatlanggo.Eng},
			"he": {whatlanggo.Heb, whatlanggo.Eng},
			"hi": {whatlanggo.Hin, whatlanggo.Eng},
			"tr": {whatlanggo.Tur, whatlanggo.Eng},
		}

		for _, key := range enKeys {
			enVal := enStrings[key]
			val, ok := stringsMap[key]
			if !ok {
				// Missing keys are perfectly fine and explicitly allowed for contributors.
				// The runtime localization engine will elegantly fallback to the English base
				// (or the user's secondary language).
				t.Logf("Tech Debt -> %s: Missing key '%s' (will fallback at runtime)", File, key)
				continue
			}

			enNewlines := strings.Count(enVal, "\n")
			valNewlines := strings.Count(val, "\n")
			if enNewlines != valNewlines {
				t.Logf("Tech Debt -> %s: Key '%s' has %d newlines, expected %d (outdated translation)", File, key, valNewlines, enNewlines)
				continue
			}

			enPlaces := placeholderRe.FindAllString(enVal, -1)
			valPlaces := placeholderRe.FindAllString(val, -1)
			if len(enPlaces) != len(valPlaces) {
				t.Logf("Tech Debt -> %s: Key '%s' has %v placeholders, expected %v (outdated translation)", File, key, valPlaces, enPlaces)
				continue
			} else {
				mismatch := false
				for i := range enPlaces {
					if enPlaces[i] != valPlaces[i] {
						t.Logf("Tech Debt -> %s: Key '%s' placeholder mismatch at %d: %s vs %s (outdated translation)", File, key, i, valPlaces[i], enPlaces[i])
						mismatch = true
						break
					}
				}
				if mismatch {
					continue
				}
			}

			// 1. Anti-merge
			for _, match := range mergedLineRe.FindAllString(val, -1) {
				s := match[:len(match)-1]
				for i := 0; i < len(s); i++ {
					if _, ok := enStrings[s[i:]]; ok {
						t.Errorf("%s: Key '%s' contains a merged line pattern for key '%s'", File, key, s[i:])
						break
					}
				}
			}

			// 4. Whitespace drift
			enRawVal := enRawStrings[key]
			valRawVal := targetRawStrings[key]
			if strings.HasPrefix(enRawVal, " ") != strings.HasPrefix(valRawVal, " ") {
				t.Logf("Tech Debt -> %s: Key '%s' leading space mismatch (outdated translation)", File, key)
				continue
			}
			if strings.HasSuffix(enRawVal, " ") != strings.HasSuffix(valRawVal, " ") {
				t.Logf("Tech Debt -> %s: Key '%s' trailing space mismatch (outdated translation)", File, key)
				continue
			}

			// 5. Ampersand (Hotkey) sanity check
			enHasAmp := hasHotkey(enVal)
			valHasAmp := hasHotkey(val)

			if !enHasAmp && valHasAmp {
				t.Logf("Tech Debt -> %s: Key '%s' has unexpected hotkey '&' (outdated translation)", File, key)
				continue
			}

			if valHasAmp {
				valNoDbl := strings.ReplaceAll(val, "&&", "")
				ampCount := strings.Count(valNoDbl, "&")
				if ampCount > 1 {
					t.Errorf("%s: Key '%s' has multiple single '&'", File, key)
				}
				idx := strings.Index(valNoDbl, "&")
				if idx == len(valNoDbl)-1 {
					t.Errorf("%s: Key '%s' has '&' at the end of the string", File, key)
				} else {
					nextChar := []rune(valNoDbl[idx+1:])[0]
					if !unicode.IsLetter(nextChar) && !unicode.IsDigit(nextChar) {
						t.Errorf("%s: Key '%s' has invalid char after '&': %c", File, key, nextChar)
					}
					if code == "zh" || code == "ja" || code == "ko" {
						if nextChar < 'A' || (nextChar > 'Z' && nextChar < 'a') || nextChar > 'z' {
							t.Errorf("%s: Key '%s' in CJK must use Latin letter for hotkey, got: %c", File, key, nextChar)
						}
					}
				}
			}

			// 2. N-gram language detection
			cleanVal := placeholderRe.ReplaceAllString(val, "")
			cleanVal = strings.ReplaceAll(cleanVal, "&", "")
			// whatlanggo has no Armenian model. For Armenian text containing
			// Latin product names it confidently classifies only those names.
			// The alphabet and homoglyph canaries still validate Armenian.
			if code != "hy" && utf8.RuneCountInString(cleanVal) > 50 {
				info := whatlanggo.Detect(cleanVal)
				if info.IsReliable() && info.Confidence > 0.90 {
					allowed := false
					for _, l := range allowedLangs[code] {
						if info.Lang == l {
							allowed = true
							break
						}
					}
					if !allowed && info.Lang != whatlanggo.Eng {
						t.Errorf("%s: Key '%s' detected as %s with high confidence (%.2f)", File, key, info.Lang.String(), info.Confidence)
					}
				}
			}
		}
	}

	if updateBaseline {
		if err := writeLangCoverageBaseline(langCoverageBaselinePath, coverage); err != nil {
			t.Fatalf("cannot update %s: %v", langCoverageBaselinePath, err)
		}
		t.Logf("%s regenerated for %d languages; review and commit it", langCoverageBaselinePath, len(coverage))
		return
	}

	for code := range baseline {
		if !seenCodes[code] {
			t.Errorf("coverage baseline requires %s.lng but the language file is missing", code)
		}
	}
}

func TestLangCoverageBaselineParsing(t *testing.T) {
	baseline, err := loadLangCoverageBaseline([]byte("# comment\nru = 1343\nen=1355\n"))
	if err != nil {
		t.Fatal(err)
	}
	if baseline["ru"] != 1343 || baseline["en"] != 1355 {
		t.Fatalf("baseline = %#v", baseline)
	}

	for _, input := range []string{
		"ru=1\nru=2\n",
		"ru=-1\n",
		"ru\n",
	} {
		if _, err := loadLangCoverageBaseline([]byte(input)); err == nil {
			t.Errorf("loadLangCoverageBaseline(%q) unexpectedly succeeded", input)
		}
	}
}

func TestWriteLangCoverageBaselineSortsCodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coverage_baseline.txt")
	if err := writeLangCoverageBaseline(path, map[string]int{"zh": 643, "en": 1355, "ar": 668}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "ar=668\nen=1355\nzh=643\n"; got != want {
		t.Fatalf("baseline = %q, want %q", got, want)
	}
}

func TestAntiMergeLogic(t *testing.T) {
	enStrings := map[string]string{
		"LanguageSettings.Title": "Language Settings",
		"FileOp.Resume":          "Resume",
		"Some.OtherKey":          "Value",
	}

	mergedLineRe := regexp.MustCompile(`[a-zA-Z0-9_.-]+=`)

	checkMerged := func(val string) (string, bool) {
		for _, match := range mergedLineRe.FindAllString(val, -1) {
			s := match[:len(match)-1]
			for i := 0; i < len(s); i++ {
				if _, ok := enStrings[s[i:]]; ok {
					return s[i:], true
				}
			}
		}
		return "", false
	}

	tests := []struct {
		input     string
		wantKey   string
		wantFound bool
	}{
		{"Аднав&іцьLanguageSettings.Title= Налады мовы", "LanguageSettings.Title", true},
		{"ResumeLanguageSettings.Title= Налады мовы", "LanguageSettings.Title", true},
		{"This is a normal translation.", "", false},
		{"Some value = 42", "", false},
		{"NotAKey.Title= some text", "", false},
		{"FirstSome.OtherKey=SecondLanguageSettings.Title=foo", "Some.OtherKey", true},
	}

	for _, tt := range tests {
		gotKey, gotFound := checkMerged(tt.input)
		if gotFound != tt.wantFound {
			t.Errorf("checkMerged(%q) found = %v, want %v", tt.input, gotFound, tt.wantFound)
		}
		if gotFound && gotKey != tt.wantKey {
			t.Errorf("checkMerged(%q) found key = %q, want %q", tt.input, gotKey, tt.wantKey)
		}
	}
}
