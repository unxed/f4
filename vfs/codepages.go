package vfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	stdunicode "unicode"
	"unicode/utf8"

	"github.com/abadojack/whatlanggo"
	"github.com/unxed/localecp"
	"github.com/unxed/vtui"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"
)

const (
	CodepageAutoDetect = -1

	legacySystemANSI = 11111
	legacySystemOEM  = 22222
)

// systemANSI and systemOEM are the ids behind the "ANSI" and "OEM" entries,
// systemANSIEncoding and systemOEMEncoding the encodings they stand for.
// The detected* copies keep what the platform deduced at startup, so that
// clearing the configuration override restores it without asking again.
var (
	systemANSI, systemOEM                     int
	systemANSIEncoding, systemOEMEncoding     encoding.Encoding
	detectedANSI, detectedOEM                 int
	detectedANSIEncoding, detectedOEMEncoding encoding.Encoding
)

// otherCodepages is every codepage except the two system aliases, in menu
// order: the built-in table followed by whatever the platform contributes.
// SetSystemCodepages rebuilds AvailableCodepages from it, so pinning ANSI or
// OEM does not repeat the platform scan -- on Unix that scan is an
// `iconv --list` subprocess.
var otherCodepages []Codepage

type codepageGroup uint8

const (
	codepageSystem codepageGroup = iota
	codepageUnicode
	codepageOther
	codepageIconv
)

type Codepage struct {
	ID    int
	Name  string
	Enc   encoding.Encoding
	group codepageGroup
}

var AvailableCodepages []Codepage

const UTF8BOMSize = 3

func init() {
	ascii, _ := htmlindex.Get("us-ascii")
	systemANSI, systemOEM = systemCodepageIDs()
	systemANSIEncoding, systemOEMEncoding = localecp.ANSIEncoding, localecp.OEMEncoding
	detectedANSI, detectedOEM = systemANSI, systemOEM
	detectedANSIEncoding, detectedOEMEncoding = systemANSIEncoding, systemOEMEncoding
	otherCodepages = []Codepage{
		{ID: 65001, Name: "UTF-8", Enc: unicode.UTF8, group: codepageUnicode},
		{ID: 1200, Name: "UTF-16 (Little endian)", Enc: unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), group: codepageUnicode},
		{ID: 1201, Name: "UTF-16 (Big endian)", Enc: unicode.UTF16(unicode.BigEndian, unicode.UseBOM), group: codepageUnicode},
		{ID: 12000, Name: "UTF-32 (Little endian)", Enc: utf32.UTF32(utf32.LittleEndian, utf32.UseBOM), group: codepageUnicode},
		{ID: 12001, Name: "UTF-32 (Big endian)", Enc: utf32.UTF32(utf32.BigEndian, utf32.UseBOM), group: codepageUnicode},
		{ID: 20127, Name: "US-ASCII", Enc: ascii, group: codepageOther},
		{ID: 37, Name: "IBM EBCDIC US-Canada", Enc: charmap.CodePage037, group: codepageOther},
		{ID: 437, Name: "CP437 (US OEM)", Enc: charmap.CodePage437, group: codepageOther},
		{ID: 850, Name: "CP850 (Western OEM)", Enc: charmap.CodePage850, group: codepageOther},
		{ID: 852, Name: "CP852 (Slavic OEM)", Enc: charmap.CodePage852, group: codepageOther},
		{ID: 855, Name: "CP855 (Cyrillic OEM)", Enc: charmap.CodePage855, group: codepageOther},
		{ID: 858, Name: "CP858 (Western OEM)", Enc: charmap.CodePage858, group: codepageOther},
		{ID: 860, Name: "CP860 (Portuguese OEM)", Enc: charmap.CodePage860, group: codepageOther},
		{ID: 862, Name: "CP862 (Hebrew OEM)", Enc: charmap.CodePage862, group: codepageOther},
		{ID: 863, Name: "CP863 (Canadian French OEM)", Enc: charmap.CodePage863, group: codepageOther},
		{ID: 865, Name: "CP865 (Nordic OEM)", Enc: charmap.CodePage865, group: codepageOther},
		{ID: 866, Name: "CP866 (Cyrillic OEM)", Enc: charmap.CodePage866, group: codepageOther},
		{ID: 1047, Name: "IBM-1047 EBCDIC", Enc: charmap.CodePage1047, group: codepageOther},
		{ID: 1140, Name: "IBM-1140 EBCDIC", Enc: charmap.CodePage1140, group: codepageOther},
		{ID: 874, Name: "Windows-874 (Thai)", Enc: charmap.Windows874, group: codepageOther},
		{ID: 1250, Name: "Windows-1250 (Central European)", Enc: charmap.Windows1250, group: codepageOther},
		{ID: 1251, Name: "Windows-1251 (Cyrillic)", Enc: charmap.Windows1251, group: codepageOther},
		{ID: 1252, Name: "Windows-1252 (Western)", Enc: charmap.Windows1252, group: codepageOther},
		{ID: 1253, Name: "Windows-1253 (Greek)", Enc: charmap.Windows1253, group: codepageOther},
		{ID: 1254, Name: "Windows-1254 (Turkish)", Enc: charmap.Windows1254, group: codepageOther},
		{ID: 1255, Name: "Windows-1255 (Hebrew)", Enc: charmap.Windows1255, group: codepageOther},
		{ID: 1256, Name: "Windows-1256 (Arabic)", Enc: charmap.Windows1256, group: codepageOther},
		{ID: 1257, Name: "Windows-1257 (Baltic)", Enc: charmap.Windows1257, group: codepageOther},
		{ID: 1258, Name: "Windows-1258 (Vietnamese)", Enc: charmap.Windows1258, group: codepageOther},
		{ID: 20866, Name: "KOI8-R (Cyrillic)", Enc: charmap.KOI8R, group: codepageOther},
		{ID: 21866, Name: "KOI8-U (Cyrillic/Ukrainian)", Enc: charmap.KOI8U, group: codepageOther},
		{ID: 28591, Name: "ISO-8859-1 (Western)", Enc: charmap.ISO8859_1, group: codepageOther},
		{ID: 28592, Name: "ISO-8859-2 (Central European)", Enc: charmap.ISO8859_2, group: codepageOther},
		{ID: 28593, Name: "ISO-8859-3 (Southern European)", Enc: charmap.ISO8859_3, group: codepageOther},
		{ID: 28594, Name: "ISO-8859-4 (Northern European)", Enc: charmap.ISO8859_4, group: codepageOther},
		{ID: 28595, Name: "ISO-8859-5 (Cyrillic)", Enc: charmap.ISO8859_5, group: codepageOther},
		{ID: 28596, Name: "ISO-8859-6 (Arabic)", Enc: charmap.ISO8859_6, group: codepageOther},
		{ID: 28597, Name: "ISO-8859-7 (Greek)", Enc: charmap.ISO8859_7, group: codepageOther},
		{ID: 28598, Name: "ISO-8859-8 (Hebrew)", Enc: charmap.ISO8859_8, group: codepageOther},
		{ID: 28599, Name: "ISO-8859-9 (Turkish)", Enc: charmap.ISO8859_9, group: codepageOther},
		{ID: 28600, Name: "ISO-8859-10 (Nordic)", Enc: charmap.ISO8859_10, group: codepageOther},
		{ID: 28603, Name: "ISO-8859-13 (Baltic)", Enc: charmap.ISO8859_13, group: codepageOther},
		{ID: 28604, Name: "ISO-8859-14 (Celtic)", Enc: charmap.ISO8859_14, group: codepageOther},
		{ID: 28605, Name: "ISO-8859-15 (Western)", Enc: charmap.ISO8859_15, group: codepageOther},
		{ID: 28606, Name: "ISO-8859-16 (Central European)", Enc: charmap.ISO8859_16, group: codepageOther},
		{ID: 932, Name: "Shift JIS (Japanese)", Enc: japanese.ShiftJIS, group: codepageOther},
		{ID: 50220, Name: "ISO-2022-JP (Japanese)", Enc: japanese.ISO2022JP, group: codepageOther},
		{ID: 51932, Name: "EUC-JP (Japanese)", Enc: japanese.EUCJP, group: codepageOther},
		{ID: 51949, Name: "EUC-KR (Korean)", Enc: korean.EUCKR, group: codepageOther},
		{ID: 936, Name: "GBK (Simplified Chinese)", Enc: simplifiedchinese.GBK, group: codepageOther},
		{ID: 52936, Name: "HZ-GB-2312 (Simplified Chinese)", Enc: simplifiedchinese.HZGB2312, group: codepageOther},
		{ID: 54936, Name: "GB18030 (Simplified Chinese)", Enc: simplifiedchinese.GB18030, group: codepageOther},
		{ID: 950, Name: "Big5 (Traditional Chinese)", Enc: traditionalchinese.Big5, group: codepageOther},
	}
	// platformCodepages asks FindCodepage which ids are already covered, so
	// the list it consults has to be in place before it runs.
	AvailableCodepages = append(systemCodepageEntries(), otherCodepages...)
	otherCodepages = append(otherCodepages, platformCodepages()...)
	rebuildAvailableCodepages()
}

// systemCodepageEntries are the two aliases at the head of the codepage list,
// built from whatever ANSI and OEM currently mean.
func systemCodepageEntries() []Codepage {
	ansiName, oemName := systemCodepageNames()
	return []Codepage{
		{ID: systemANSI, Name: ansiName, Enc: systemANSIEncoding, group: codepageSystem},
		{ID: systemOEM, Name: oemName, Enc: systemOEMEncoding, group: codepageSystem},
	}
}

func rebuildAvailableCodepages() {
	list := make([]Codepage, 0, len(otherCodepages)+2)
	list = append(list, systemCodepageEntries()...)
	list = append(list, otherCodepages...)
	AvailableCodepages = uniqueCodepages(list)
}

// SetSystemCodepages pins what ANSI and OEM mean, the way far2l's
// ~/.config/far2l/cp file does.
//
// Neither is a system property on Unix: localecp guesses both from
// LC_ALL/LC_CTYPE/LANG, and the guess is wrong whenever the locale says
// nothing useful about the legacy encodings the user actually meets --
// LANG=C, an en_US.UTF-8 desktop used to read CP866 archives, a locale that
// is simply not in the table. There is nothing better to infer from, so the
// user has to be able to state it outright (#368).
//
// A zero or negative id means "whatever was detected", so removing the
// setting restores the deduced codepage. An id this build does not know is
// reported and leaves that half detected, because one bad line in a
// configuration file must not cost the user both codepages. Call it during
// startup, before anything is decoded: it rewrites process-wide state and
// does not synchronize with readers.
func SetSystemCodepages(ansiID, oemID int) error {
	newANSI, ansiEnc, ansiErr := resolveSystemCodepage(ansiID, detectedANSI, detectedANSIEncoding)
	newOEM, oemEnc, oemErr := resolveSystemCodepage(oemID, detectedOEM, detectedOEMEncoding)
	systemANSI, systemANSIEncoding = newANSI, ansiEnc
	systemOEM, systemOEMEncoding = newOEM, oemEnc
	rebuildAvailableCodepages()
	return errors.Join(ansiErr, oemErr)
}

// resolveSystemCodepage turns a configured id into the id and encoding an
// alias should use, falling back to the detected pair.
func resolveSystemCodepage(id, detected int, detectedEnc encoding.Encoding) (int, encoding.Encoding, error) {
	if id <= 0 {
		return detected, detectedEnc, nil
	}
	id = normalizeCodepageID(id)
	cp, ok := FindCodepage(id)
	if !ok || cp.Enc == nil {
		return detected, detectedEnc, fmt.Errorf("unknown codepage %d", id)
	}
	return id, cp.Enc, nil
}

// SystemANSICodepage and SystemOEMCodepage are the real system codepage IDs
// used by Far for its ANSI and OEM entries. The associated encodings are the
// ones localecp deduced, unless SetSystemCodepages replaced them.
func SystemANSICodepage() int { return systemANSI }
func SystemOEMCodepage() int  { return systemOEM }

// uniqueCodepages keeps one entry per id. A system alias ("System ANSI",
// "System OEM") wins over a later entry with the same id -- except when that
// id is a Unicode codepage. Then the alias is dropped and the Unicode entry
// stays: on a machine whose system codepage is UTF-8 the alias would only
// say "System ANSI (65001)" and push the real UTF-8 entry out of the list,
// which is what #875 saw. Nothing is lost -- on such a machine ANSI *is*
// UTF-8 -- and DisplayCodepageName keeps calling 65001 "UTF-8".
func uniqueCodepages(codepages []Codepage) []Codepage {
	unicode := make(map[int]struct{})
	for _, cp := range codepages {
		if cp.group == codepageUnicode {
			unicode[cp.ID] = struct{}{}
		}
	}
	seen := make(map[int]struct{}, len(codepages))
	result := make([]Codepage, 0, len(codepages))
	for _, cp := range codepages {
		if cp.group == codepageSystem {
			if _, isUnicode := unicode[cp.ID]; isUnicode {
				continue
			}
			seen[cp.ID] = struct{}{}
			result = append(result, cp)
			continue
		}
		if _, ok := seen[cp.ID]; ok {
			continue
		}
		seen[cp.ID] = struct{}{}
		result = append(result, cp)
	}
	return result
}

func DisplayCodepageName(id int) string {
	id = normalizeCodepageID(id)
	// Unicode first: on a UTF-8 system 65001 is also the "ANSI" id, and the
	// user who picked UTF-8 must read "UTF-8" in the status line, not "ANSI".
	if id == 65001 {
		return "UTF-8"
	}
	if id == systemANSI {
		return "ANSI"
	}
	if id == systemOEM {
		return "OEM"
	}
	if cp, ok := FindCodepage(id); ok {
		return cp.Name
	}
	return fmt.Sprintf("%d", id)
}

func FindCodepage(id int) (Codepage, bool) {
	id = normalizeCodepageID(id)
	for _, cp := range AvailableCodepages {
		if cp.ID == id {
			return cp, true
		}
	}
	return Codepage{}, false
}

func normalizeCodepageID(id int) int {
	switch id {
	case legacySystemANSI:
		return systemANSI
	case legacySystemOEM:
		return systemOEM
	default:
		return id
	}
}

// NormalizeCodepageID maps the pre-875 system aliases to Far's real ACP/OEMCP
// codepage IDs. It lets old configuration values keep working while all newly
// written values use the real system IDs.
func NormalizeCodepageID(id int) int {
	return normalizeCodepageID(id)
}

// CodepageMenuLabel is shared by the settings and viewer/editor menus. System
// aliases and iconv-only names have no meaningful numeric codepage to show;
// native and built-in codepages retain their standard numeric ID.
func CodepageMenuLabel(cp Codepage) string {
	if cp.group == codepageSystem || cp.ID < 0 {
		return cp.Name
	}
	return fmt.Sprintf("%5d  %s", cp.ID, cp.Name)
}

func DecodeBytes(data []byte, cpID int) ([]byte, error) {
	cpID = normalizeCodepageID(cpID)
	if cpID == 65001 {
		return data, nil
	}

	cp, ok := FindCodepage(cpID)
	if !ok || cp.Enc == nil {
		return data, fmt.Errorf("unsupported codepage: %d", cpID)
	}
	decoder := cp.Enc.NewDecoder()

	if decoder == nil {
		return data, fmt.Errorf("decoder is nil for codepage: %d", cpID)
	}

	return decoder.Bytes(data)
}

func EncodeBytes(data []byte, cpID int) ([]byte, error) {
	cpID = normalizeCodepageID(cpID)
	if cpID == 65001 {
		return data, nil
	}

	cp, ok := FindCodepage(cpID)
	if !ok || cp.Enc == nil {
		return data, fmt.Errorf("unsupported codepage: %d", cpID)
	}
	encoder := cp.Enc.NewEncoder()

	if encoder == nil {
		return data, fmt.Errorf("encoder is nil for codepage: %d", cpID)
	}

	return encoder.Bytes(data)
}

func DetectBOM(data []byte) (int, bool) {
	if HasUTF8BOM(data) {
		return 65001, true
	}
	if len(data) >= 2 {
		if data[0] == 0xFF && data[1] == 0xFE {
			return 1200, true
		}
		if data[0] == 0xFE && data[1] == 0xFF {
			return 1201, true
		}
	}
	return 65001, false
}

// HasUTF8BOM reports whether data starts with the UTF-8 byte-order mark.
// Detection and removal are separate because the marker is useful for
// choosing the codepage, while it is not part of the text shown to a user.
func HasUTF8BOM(data []byte) bool {
	return len(data) >= UTF8BOMSize &&
		data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF
}

// StripUTF8BOM returns data without a leading UTF-8 byte-order mark. It keeps
// the original slice when there is no marker, so callers can use it on hot
// paths without an allocation.
func StripUTF8BOM(data []byte) []byte {
	if !HasUTF8BOM(data) {
		return data
	}
	return data[UTF8BOMSize:]
}

func DetectEncoding(data []byte, autodetect bool, defaultCP int) int {
	if cp, ok := DetectBOM(data); ok {
		return cp
	}
	if autodetect {
		// Before the UTF-8 check, not after it: UTF-16 Cyrillic is made of
		// bytes below 0x80 (U+0447 is 0x47 0x04) and therefore passes
		// utf8.Valid, which used to leave such a file displayed as UTF-8
		// control characters or classified as binary outright.
		if cp, ok := detectUTF16WithoutBOM(data); ok {
			return cp
		}
		if utf8.Valid(data) {
			return 65001
		}
		if cp, ok := detectLegacyCodepage(data); ok {
			return cp
		}
		return defaultCP
	}
	return defaultCP
}

// utf16DetectMinBytes is the shortest sample worth guessing from. A couple of
// characters can be zero-padded by coincidence; a couple of dozen cannot.
const utf16DetectMinBytes = 16

// detectUTF16WithoutBOM reports UTF-16 and its endianness for text that
// carries no byte-order mark, which is what Far does and what f4 needs for
// files written by a tool that omits the marker.
//
// Every character below U+0100 leaves one byte of its pair at zero, always on
// the same side, and the pattern survives text that is mostly Cyrillic or
// Greek because spaces, digits and punctuation stay ASCII. A single-byte
// encoding never produces it, and neither does UTF-8, whose only NUL is a NUL
// character.
func detectUTF16WithoutBOM(data []byte) (int, bool) {
	if len(data) < utf16DetectMinBytes {
		return 0, false
	}

	pairs := len(data) / 2
	evenZeros, oddZeros := 0, 0
	for i := 0; i < pairs*2; i += 2 {
		if data[i] == 0 && data[i+1] == 0 {
			// A NUL character. Text does not contain one, and a run of
			// them is padding in a binary that would otherwise satisfy
			// the balance test below.
			return 0, false
		}
		if data[i] == 0 {
			evenZeros++
		}
		if data[i+1] == 0 {
			oddZeros++
		}
	}

	// An eighth of the characters being ASCII is a low bar for prose in any
	// alphabetic script -- spaces alone usually clear it, and this file's
	// dense Russian still runs at a fifth -- while remaining far above the
	// zero NUL bytes an ordinary 8-bit or UTF-8 file has. The zeros must also
	// sit overwhelmingly on one side: a file that scatters them across both
	// is not a stream of 16-bit characters, whatever else it is.
	//
	// Text with no ASCII at all, such as UTF-16 Japanese, stays below the bar
	// and is left to the caller's configured default. Guessing it would mean
	// accepting any byte pattern as text.
	const minZeroShare = 8
	const sidedness = 8
	switch {
	case oddZeros*minZeroShare >= pairs && oddZeros > evenZeros*sidedness:
		return 1200, true
	case evenZeros*minZeroShare >= pairs && evenZeros > oddZeros*sidedness:
		return 1201, true
	}
	return 0, false
}

// detectLegacyCodepage looks at every single-byte codepage f4 can decode. A
// byte stream does not carry its codepage, so this remains deliberately
// conservative: text quality is the primary signal and language detection is
// used only to break a close tie. An uncertain result falls back to the
// user's configured default rather than turning arbitrary binary data into
// text.
func detectLegacyCodepage(data []byte) (int, bool) {
	if len(data) < 8 {
		return 0, false
	}

	// Keep the system aliases first. When an alias and its explicit codepage
	// decode to the same text, the alias is the useful result on that system
	// (and preserves the existing ANSI/OEM behaviour). Explicit codepages are
	// still tried when the system locale is unrelated to the file.
	ids := []int{systemANSI, systemOEM, 1251, 866, 20866, 1252, 437, 850, 852}
	type candidate struct {
		id         int
		text       string
		score      int
		confidence float64
	}
	candidates := make([]candidate, 0, len(ids))
	seenText := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		decoded, err := DecodeBytes(data, id)
		if err != nil {
			continue
		}
		text := string(decoded)
		if _, seen := seenText[text]; seen {
			continue
		}
		seenText[text] = struct{}{}
		candidates = append(candidates, candidate{
			id:    id,
			text:  text,
			score: legacyTextScore(decoded),
		})
	}
	if len(candidates) == 0 {
		return 0, false
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})
	top := candidates[0]
	if top.score <= 0 {
		return 0, false
	}
	if len(candidates) == 1 || top.score-candidates[1].score >= 8 {
		return top.id, true
	}

	// The common Cyrillic case is a genuine tie: CP1251 and KOI8-R can both
	// produce equally readable Unicode text. whatlanggo is intentionally not
	// allowed to overrule a clear text-quality win; it only ranks candidates
	// whose score is within the same conservative ambiguity window.
	for i := range candidates {
		if top.score-candidates[i].score >= 8 {
			break
		}
		candidates[i].confidence = legacyLanguageConfidence(candidates[i].text)
	}
	// candidates[0] was copied into top before the tie-breaker scores were
	// calculated; refresh the copy before comparing it with the other entries.
	top = candidates[0]
	best := top
	for _, c := range candidates[1:] {
		if top.score-c.score >= 8 {
			break
		}
		if c.confidence > best.confidence {
			best = c
		}
	}
	if best.id != top.id && best.confidence-top.confidence >= 0.05 {
		return best.id, true
	}
	if best.id == top.id && best.confidence >= 0.05 {
		return best.id, true
	}
	// When the system alias itself is one of the equally readable candidates,
	// prefer it over an explicit duplicate. This keeps ANSI/OEM detection
	// stable for the user's locale while still allowing an explicit codepage
	// to win whenever it is materially more plausible.
	if top.id == systemANSI || top.id == systemOEM {
		return top.id, true
	}
	return 0, false
}

// legacyLanguageConfidence averages detection over words instead of asking
// whatlanggo to classify one long fragment. A wrong codepage can accidentally
// form a convincing sequence across an entire fragment: Russian CP1251 text
// read as Windows-1252 is reported as German with confidence 1.0, the same
// number the correct decoding earns, so scoring the fragment as a whole made
// the system ANSI codepage win every Cyrillic tie. Word-level scores separate
// the two -- the mojibake words score near zero -- and, because every
// candidate is now measured the same way, the numbers can be compared at all.
// Punctuation and numeric-only fragments are ignored.
func legacyLanguageConfidence(text string) float64 {
	text = legacyLanguageSample(text)

	var total float64
	words := 0
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return stdunicode.IsSpace(r) || stdunicode.IsPunct(r)
	}) {
		if words >= 32 {
			break
		}
		if utf8.RuneCountInString(word) < 3 {
			continue
		}
		hasLetter := false
		for _, r := range word {
			if stdunicode.IsLetter(r) {
				hasLetter = true
				break
			}
		}
		if !hasLetter {
			continue
		}
		total += whatlanggo.Detect(word).Confidence
		words++
	}
	if words == 0 {
		return 0
	}
	confidence := total / float64(words)
	// The supported Cyrillic codepages are primarily used for Russian text,
	// and CP1251/KOI8-R can otherwise decode the same bytes into equally
	// readable-looking Slavic text. Prefer a candidate with common Russian
	// n-grams, but keep the bonus small enough that it cannot overturn a clear
	// text-quality result.
	if whatlanggo.Detect(text).Lang == whatlanggo.Rus {
		confidence += 0.15
	}
	confidence += legacyRussianNgramConfidence(text)
	return confidence
}

// This compact set of frequent Russian trigrams is used only as a
// tie-breaker between equally plausible Cyrillic decodings. It is deliberately
// not a complete language model: the text-quality score remains authoritative,
// and short or non-Russian text can still fall back to the configured default.
var russianLanguageMarkers = []string{
	"при", "рав", "ств", "ени", "ове", "ани", "сво", "лов", "чел", "ого",
	"ния", "ест", "аво", "ние", "льн", "ова", "ать", "или", "его",
	"аци", "лен", "енн", "тво", "сто", "аль", "про", "сти", "пол", "раз",
	"нос", "она", "тел", "ред", "ель", "общ", "под", "ное", "еск", "ели",
	"ече", "для", "ово", "льс", "ции", "ной", "ами", "кон", "сть", "пос",
	"тра", "так", "нал", "дру", "тер", "изн", "соц",
}

func legacyRussianNgramConfidence(text string) float64 {
	text = strings.ToLower(text)
	runeCount := utf8.RuneCountInString(text)
	if runeCount < 3 {
		return 0
	}

	matches := 0
	for _, marker := range russianLanguageMarkers {
		matches += strings.Count(text, marker)
	}
	return float64(matches) / float64(runeCount-2)
}

// A 16 KiB header is enough for text scoring but is unnecessarily expensive
// for a language model. Keep the language tie-breaker bounded and deterministic
// so opening a large file does not spend noticeable time classifying every
// word in the whole header.
func legacyLanguageSample(text string) string {
	const maxRunes = 4096
	runes := []rune(text)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return text
}

// A letter's script, as a bit so that one word's letters can be OR-ed
// together. Only the scripts the supported single-byte codepages can produce
// are named; everything else shares one bucket, which keeps the mixed-word
// test below conservative.
const (
	scriptLatin = 1 << iota
	scriptGreek
	scriptCyrillic
	scriptOther
)

func letterScript(r rune) int {
	if !stdunicode.IsLetter(r) {
		return 0
	}
	switch {
	case r < 0x0250 || (r >= 0x1E00 && r <= 0x1EFF):
		return scriptLatin
	case r >= 0x0370 && r <= 0x03FF:
		return scriptGreek
	case r >= 0x0400 && r <= 0x052F:
		return scriptCyrillic
	}
	return scriptOther
}

// mixedScriptPenalty charges a word whose letters come from more than one
// script. Reading text with the wrong single-byte codepage is what produces
// those: CP1252 text read as CP866 turns "Café" into "УCafщ", and no language
// spells a word half in Latin and half in Cyrillic. Whole-word Cyrillic or
// whole-word Latin costs nothing, so a correctly decoded file is unaffected
// however many scripts it mentions.
func mixedScriptPenalty(scripts int) int {
	if scripts != 0 && scripts&(scripts-1) != 0 {
		return -16
	}
	return 0
}

func legacyTextScore(data []byte) int {
	score := 0
	wordScripts := 0
	afterLower := false
	for _, r := range string(data) {
		if script := letterScript(r); script != 0 {
			wordScripts |= script
		} else {
			score += mixedScriptPenalty(wordScripts)
			wordScripts = 0
		}
		// A capital in the middle of a lowercase word is the other classic
		// mojibake signature, and the one that separates candidates no
		// script test can tell apart: CP1252 "Café" read as CP850 becomes
		// "CafÚ". Text that legitimately spells a word that way -- camelCase
		// in source, say -- is ASCII, so every candidate is charged for it
		// equally and the comparison is unaffected.
		if stdunicode.IsUpper(r) && afterLower {
			score -= 8
		}
		afterLower = stdunicode.IsLower(r)
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			score++
		case stdunicode.IsLetter(r):
			score += 4
		case stdunicode.IsDigit(r):
			score += 2
		case stdunicode.IsSpace(r) || stdunicode.IsPunct(r):
			score++
		case stdunicode.IsControl(r):
			score -= 8
		case stdunicode.IsGraphic(r):
			score++
		default:
			score -= 4
		}

		// Box-drawing characters, non-breaking spaces, and replacement
		// characters are common signs that bytes were decoded with the
		// wrong legacy codepage, even though they are technically graphic.
		if (r >= 0x2500 && r <= 0x259F) || r == '\u00A0' || r == '\uFFFD' {
			score -= 8
		}
		// CP437/CP850 decode several common ANSI punctuation and accented
		// letters as Greek characters. They are valid Unicode letters, but
		// are unlikely in ordinary text and are a useful wrong-codepage hint.
		if stdunicode.In(r, stdunicode.Greek) {
			score -= 8
		}
	}
	// The last word ends at EOF, with no separator to close it.
	score += mixedScriptPenalty(wordScripts)
	return score
}

func GetCodepageDecoderEncoder(cp string) (*encoding.Decoder, *encoding.Encoder) {
	if cp == "" || cp == "65001" {
		return nil, nil
	}
	id, err := strconv.Atoi(cp)
	if err != nil {
		return nil, nil
	}
	id = normalizeCodepageID(id)
	if cpObj, ok := FindCodepage(id); ok && cpObj.Enc != nil {
		return cpObj.Enc.NewDecoder(), cpObj.Enc.NewEncoder()
	}
	return nil, nil
}

func GetSystemOEMEncoding() encoding.Encoding {
	return systemOEMEncoding
}

func GetSystemANSIEncoding() encoding.Encoding {
	return systemANSIEncoding
}

type MemoryReadAtCloser struct {
	Data []byte
}

func (m *MemoryReadAtCloser) Size() int64 { return int64(len(m.Data)) }
func (m *MemoryReadAtCloser) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if off >= int64(len(m.Data)) {
		return 0, io.EOF
	}
	n := copy(p, m.Data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (m *MemoryReadAtCloser) Read(ctx context.Context, p []byte) (int, error) {
	return 0, io.EOF
}
func (m *MemoryReadAtCloser) Close() error { return nil }

func GetNextFastSwitchCodepage(current int) int {
	legacy := current == legacySystemANSI || current == legacySystemOEM
	current = normalizeCodepageID(current)
	FastSwitchCodepages := make([]int, 0, 3)
	for _, id := range []int{65001, systemANSI, systemOEM} {
		if !slicesContains(FastSwitchCodepages, id) {
			FastSwitchCodepages = append(FastSwitchCodepages, id)
		}
	}
	for i, id := range FastSwitchCodepages {
		if id == current {
			nextIdx := (i + 1) % len(FastSwitchCodepages)
			if legacy {
				switch FastSwitchCodepages[nextIdx] {
				case systemANSI:
					return legacySystemANSI
				case systemOEM:
					return legacySystemOEM
				}
			}
			return FastSwitchCodepages[nextIdx]
		}
	}
	return 65001
}

func slicesContains(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func BuildCodepageMenuItems(currentCpID int, autoDetect bool) ([]vtui.MenuItem, int) {
	currentCpID = normalizeCodepageID(currentCpID)
	var items []vtui.MenuItem
	currIdx := 0

	addHeader := func(title string) {
		items = append(items, vtui.MenuItem{Text: title, Separator: true})
	}

	addCP := func(cp Codepage) {
		text := CodepageMenuLabel(cp)

		// The file's codepage is always marked, whether it was detected or
		// chosen: it is a fact about the file on screen. autoDetect only
		// says whether "Auto-detect" is also ticked and where the cursor
		// starts.
		if cp.ID == currentCpID {
			text = "√ " + text
			if !autoDetect {
				currIdx = len(items)
			}
		} else {
			text = "  " + text
		}
		items = append(items, vtui.MenuItem{
			Text:     text,
			UserData: cp.ID,
		})
	}

	autoText := "  Auto-detect "
	if autoDetect {
		autoText = "√ Auto-detect "
		currIdx = 0
	}
	items = append(items, vtui.MenuItem{
		Text:     autoText,
		UserData: CodepageAutoDetect,
	})

	// A header only above a group that has members: on a UTF-8 system the
	// System group can be empty (its aliases fold into UTF-8, see
	// uniqueCodepages), and a bare " System " line above nothing is noise.
	addGroup := func(title string, in func(Codepage) bool) {
		first := true
		for _, cp := range AvailableCodepages {
			if !in(cp) {
				continue
			}
			if first {
				addHeader(title)
				first = false
			}
			addCP(cp)
		}
	}
	addGroup(" System ", func(cp Codepage) bool { return cp.group == codepageSystem })
	addGroup(" Unicode ", func(cp Codepage) bool { return cp.group == codepageUnicode })
	addGroup(" Other ", func(cp Codepage) bool { return cp.group == codepageOther || cp.group == codepageIconv })

	return items, currIdx
}
