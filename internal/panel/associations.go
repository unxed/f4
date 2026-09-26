package panel

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/filemask"
	"github.com/unxed/f4/vfs/hostmode"
)

// AssocKind numbers the six command slots per far2l's filetype.hpp
// (FILETYPE_EXEC, FILETYPE_ALTEXEC, …). Keeping the same order lets a
// far2l associations.ini be dropped into f4 (and vice versa) without
// remapping the State bitmask.
type AssocKind int

const (
	AssocExecute AssocKind = 0 // Enter
	AssocAltExec AssocKind = 1 // Ctrl+PgDn (reserved; not wired yet)
	AssocView    AssocKind = 2 // F3
	AssocAltView AssocKind = 3 // Alt+F3 (reserved; not wired yet)
	AssocEdit    AssocKind = 4 // F4
	AssocAltEdit AssocKind = 5 // Alt+F4 (reserved; not wired yet)
)

const AssocKindCount = 6

// AssocKeyName maps AssocKind to the INI key far2l uses for the slot.
var AssocKeyName = [AssocKindCount]string{
	"Execute",
	"AltExec",
	"View",
	"AltView",
	"Edit",
	"AltEdit",
}

// FileAssoc mirrors far2l's FileTypeStrings so associations.ini is
// interchangeable between the two applications. Enabled[N] carries the
// far2l State bit for slot N; a disabled slot stays in the file but is
// skipped during dispatch.
type FileAssoc struct {
	Mask        string
	Description string
	Commands    [AssocKindCount]string
	Enabled     [AssocKindCount]bool
}

// associationsRoot is the INI section prefix used by far2l. Do not
// rename — files must round-trip byte-identical with far2l.
const associationsRoot = "Associations"

// associationsFilePathFn is the resolver used by AssociationsFilePath.
// Tests overwrite it to redirect load/save to a temp file so they can
// exercise the full dispatch path without touching the user's config.
var AssociationsFilePathFn = defaultAssociationsFilePath

// AssociationsFilePath returns the user-config location. Follows the
// far2l naming so the same file can live under either app's settings.
func AssociationsFilePath() string { return AssociationsFilePathFn() }

func defaultAssociationsFilePath() string {
	// In portable mode (UseSystemProfiles=0) write under <exeDir>/Profile;
	// otherwise use the system %AppData%/f4/settings path as before. Read
	// config.UserConfigDir live in non-portable mode so tests overriding the seam
	// keep working.
	if config.IsPortableProfile() {
		return filepath.Join(config.GetF4ConfigDir(), "settings", "associations.ini")
	}
	configDir, _ := config.UserConfigDir()
	return filepath.Join(configDir, "f4", "settings", "associations.ini")
}

// LoadAssociations parses an associations.ini. A missing file is not
// an error — an empty slice is returned. Sections outside the
// Associations/ subtree are ignored so the loader stays safe when
// pointed at a mixed-content INI.
func LoadAssociations(path string) ([]FileAssoc, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []FileAssoc{}, nil
		}
		return nil, err
	}
	defer f.Close()

	sections := map[string]map[string]string{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var cur map[string]string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := trimmed[1 : len(trimmed)-1]
			if name == associationsRoot || strings.HasPrefix(name, associationsRoot+"/") {
				if _, ok := sections[name]; !ok {
					sections[name] = map[string]string{}
				}
				cur = sections[name]
			} else {
				cur = nil
			}
			continue
		}
		if cur == nil {
			continue
		}
		if eq := strings.IndexByte(line, '='); eq != -1 {
			key := strings.TrimSpace(line[:eq])
			val := strings.TrimSpace(line[eq+1:])
			if key != "" {
				cur[key] = val
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	list := []FileAssoc{}
	for i := 0; ; i++ {
		sec, ok := sections[fmt.Sprintf("%s/Type%d", associationsRoot, i)]
		if !ok {
			break
		}
		if sec["Mask"] == "" {
			// Empty mask terminates in far2l too.
			break
		}
		a := FileAssoc{
			Mask:        sec["Mask"],
			Description: sec["Description"],
		}
		state := parseAssocState(sec["State"])
		for k := 0; k < AssocKindCount; k++ {
			a.Commands[k] = sec[AssocKeyName[k]]
			a.Enabled[k] = state&(1<<uint(k)) != 0
		}
		list = append(list, a)
	}
	return list, nil
}

// SaveAssociations writes the list atomically (tmp+rename). Parent
// directories are created as needed. Keys are written in the same
// order far2l uses so file-level diffs stay small.
func SaveAssociations(path string, list []FileAssoc) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}

	var buf strings.Builder
	for i, a := range list {
		if i > 0 {
			buf.WriteByte('\n')
		}
		fmt.Fprintf(&buf, "[%s/Type%d]\n", associationsRoot, i)
		buf.WriteString("Mask=")
		buf.WriteString(a.Mask)
		buf.WriteByte('\n')
		buf.WriteString("Description=")
		buf.WriteString(a.Description)
		buf.WriteByte('\n')
		for k := 0; k < AssocKindCount; k++ {
			buf.WriteString(AssocKeyName[k])
			buf.WriteByte('=')
			buf.WriteString(a.Commands[k])
			buf.WriteByte('\n')
		}
		fmt.Fprintf(&buf, "State=%d\n", encodeAssocState(a.Enabled))
	}

	return config.WriteUserFileAtomically(path, []byte(buf.String()), 0o600)
}

func parseAssocState(v string) uint32 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}

func encodeAssocState(enabled [AssocKindCount]bool) uint32 {
	var m uint32
	for i, on := range enabled {
		if on {
			m |= 1 << uint(i)
		}
	}
	return m
}

// MatchMask reports whether name satisfies the given far2l-style mask.
// The matcher itself lives in internal/filemask, where code that is not a
// panel can reach it; this is the name the panel and its callers know it by.
func MatchMask(name, mask string, ignoreCase bool) bool {
	return filemask.Match(name, mask, ignoreCase)
}

// MatchingAssociations returns the associations that fire for name in
// the given slot. Result order preserves list order — that is what the
// picker will show. On Windows the mask match is case-insensitive by
// default; elsewhere it honours the case of the underlying filesystem
// (best-effort: we default to case-sensitive on non-Windows too, as
// far2l does).
//
// Under Wine's posix personality (WINE.md §18.2, "регистр в сравнениях")
// "elsewhere" includes this build too: the mask is being matched against
// names hostfs read straight off a real POSIX filesystem through
// libwinescape, not through Win32, so the same case-sensitive default
// applies as on the Linux build.
func MatchingAssociations(list []FileAssoc, name string, kind AssocKind) []FileAssoc {
	if name == "" || int(kind) < 0 || int(kind) >= AssocKindCount {
		return nil
	}
	ignoreCase := runtime.GOOS == "windows" && !hostmode.Posix()
	var out []FileAssoc
	for _, a := range list {
		if !a.Enabled[kind] || a.Commands[kind] == "" {
			continue
		}
		if MatchMask(name, a.Mask, ignoreCase) {
			out = append(out, a)
		}
	}
	return out
}
