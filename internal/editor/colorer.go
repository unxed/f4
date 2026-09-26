package editor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	colorer "github.com/unxed/colorer4go"
	colorerdata "github.com/unxed/f4/internal/colorer"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/vtui"
)

const (
	// maxCachedAttrLines and attrCacheKeepWindow bound how many lines of
	// already-computed colours storeAttrs keeps around. Each entry is a
	// []uint64 the width of its line, and nothing ever evicted it before
	// this: scrolling through a large file accumulated colours for every
	// line ever drawn, hundreds of megabytes on the reference file. See
	// HIGHLIGHT.md, item 3.
	//
	// The window only needs to be larger than a screen by a comfortable
	// margin — a line evicted and then scrolled back to is just a cache
	// miss, and a miss above the parse position costs a re-anchor, which is
	// the designed fallback, not a bug.
	maxCachedAttrLines  = 20000
	attrCacheKeepWindow = 5000

	// How far the session may be fed forward to reach a line. Beyond this
	// the anchor is thrown away and rebuilt, which costs a fixed
	// hlColorerContext lines instead of the whole distance.
	hlColorerForward = 2000
	// How many lines above a viewport are parsed for context when the
	// session is re-anchored. Everything a construct opened further up
	// would have told us is lost; this is the price of a parser whose
	// state cannot be snapshotted. See HIGHLIGHT.md, phase 5.
	hlColorerContext = 300

	// How many lines behind the parse position stay in the wasm session
	// once forgetBehind starts trimming it. Kept equal to hlColorerContext:
	// that is already the margin the rest of this file trusts for a replay,
	// so forgetting does not introduce a second, untested number.
	hlColorerKeepBehind = hlColorerContext

	// How often forgetBehind actually calls into wasm, in lines fed. Calling
	// it on every ParseLine would double the wasm calls a forward scroll
	// makes; batching keeps the cost negligible while still bounding memory
	// well under file size. See HIGHLIGHT.md, item 9 and item 4's log.
	hlColorerForgetEvery = 1000

	// How many uncoloured lines one worker job may take on, counting the
	// line that missed the cache. One job per line meant one full UI round
	// trip — worker, PostTask, whole-screen redraw — per line, and the
	// viewport visibly filled with colour line by line. A batch colours a
	// screen in a single round trip; the cap only has to exceed any
	// realistic terminal height.
	hlColorerBatchLines = 200

	// How much line text one batch snapshot may copy on the UI render
	// thread. Lines are cut at 64 KB each, so a line count alone bounds the
	// snapshot at ~12.8 MB — fine as a parse budget, not as a synchronous
	// copy inside a frame. Ordinary code is a few dozen bytes per line and
	// never notices this; only files with enormous lines trade batch depth
	// for a bounded frame.
	hlColorerBatchBytes = 256 * 1024
)

// The style bits an hrd assign carries, as StyledRegion defines them.
const (
	colorerStyleBold      = 1
	colorerStyleItalic    = 2
	colorerStyleUnderline = 4
	colorerStyleStrikeout = 8
)

func applyColorerStyle(base uint64, style *colorer.RegionDefine) uint64 {
	attr := base
	if style.IsForeSet {
		attr = vtui.SetRGBFore(attr, style.Fore)
	}
	if style.IsBackSet {
		attr = vtui.SetRGBBack(attr, style.Back)
	}
	if style.Style&colorerStyleBold != 0 {
		attr |= vtui.ForegroundIntensity
	}
	if style.Style&colorerStyleUnderline != 0 {
		attr |= vtui.CommonLvbUnderscore
	}
	if style.Style&colorerStyleStrikeout != 0 {
		attr |= vtui.CommonLvbStrikeout
	}
	return attr
}

// colorerLineRuneCount reports how many indexing units Colorer uses for a
// line, which is also how many attributes the editor needs for it.
//
// Colorer keeps a line in its legacy UnicodeString, one element per code
// point. Its UTF-8 decoder in strings/legacy/CString.cpp writes a whole
// decoded code point into a single wchar_t, 32 bits wide under wasi-sdk, and
// never builds a surrogate pair; strings/legacy/Character.h states that the
// library has no surrogate support at all. Region offsets are therefore rune
// indices. They are not UTF-16 unit indices, which is what this file used to
// assume, and the difference showed as colours sliding one position left
// after every astral character on the line and staying there.
//
// Malformed UTF-8 is the one place the two counts can still drift: Go yields
// one replacement rune per bad byte, while Colorer's decoder swallows the
// continuation bytes of a truncated sequence. See REVIEW.md.
func colorerLineRuneCount(line string) int {
	return utf8.RuneCountInString(line)
}

// colorerRegionRunes fits a region Colorer reported onto the attribute slice
// of a line holding lineRunes runes. The offsets pass through unchanged,
// because they are already rune indices; only the clamping is work. A
// negative end means the region runs to the end of the line, which the caller
// also paints the rest of the row with, so it is reported through toEOL.
func colorerRegionRunes(start, end, lineRunes int) (int, int, bool) {
	toEOL := end < 0
	if toEOL || end > lineRunes {
		end = lineRunes
	}
	if start < 0 {
		start = 0
	}
	if start > lineRunes {
		start = lineRunes
	}
	if end < start {
		end = start
	}
	return start, end, toEOL
}

var (
	colorerPoolMu     sync.Mutex
	colorerIdle       *colorer.Session
	colorerIdleSource ColorerSource
)

func ensureRadiolaSchema(configsDir string) {
	catalogPath := filepath.Join(configsDir, "base", "catalog.xml")
	if _, err := os.Stat(catalogPath); os.IsNotExist(err) {
		return
	}

	hrdDir := filepath.Join(configsDir, "base", "hrd", "rgb")
	hrdPath := filepath.Join(hrdDir, "radiola.hrd")

	_ = os.MkdirAll(hrdDir, 0700)
	_ = os.WriteFile(hrdPath, []byte(colorerdata.RadiolaHRD), 0600)
	_ = os.Chmod(hrdPath, 0600)

	catalogRGBPath := filepath.Join(configsDir, "base", "hrd", "catalog-rgb.xml")
	data, err := os.ReadFile(catalogRGBPath)
	if err == nil {
		content := string(data)
		if !strings.Contains(content, "name=\"Radiola\"") {
			entry := "\n        <hrd class=\"rgb\" name=\"Radiola\" description=\"Radiola\">\n            <location link=\"&hrd;/rgb/radiola.hrd\"/>\n        </hrd>\n"
			// #nosec G703 -- configsDir is the user-selected Colorer catalog root and every appended component is fixed here.
			_ = os.WriteFile(catalogRGBPath, []byte(content+entry), 0600)
			_ = os.Chmod(catalogRGBPath, 0600)
		}
	}
}

// colorerDiagnosticsLevel says how much of what Colorer reports about its
// configuration goes to debug.log. COLORER_VERBOSE takes the values far2l's
// FarColorer reads from the same variable (CerrLogger.cpp): off, error,
// warning or warn, info, debug, trace. Unlike far2l, unset means warning, not
// off: the stock catalog says nothing at that level, and a broken one says
// exactly what is broken, which is the one thing debug.log is for.
func colorerDiagnosticsLevel() (colorer.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COLORER_VERBOSE"))) {
	case "off":
		return 0, false
	case "error":
		return colorer.LevelError, true
	case "info":
		return colorer.LevelInfo, true
	case "debug":
		return colorer.LevelDebug, true
	case "trace":
		return colorer.LevelTrace, true
	default:
		return colorer.LevelWarn, true
	}
}

// colorerSessionOptions are the options every Colorer session is created
// with: Colorer's own reports — an HRC file that does not parse, with file and
// line; a regexp that does not compile; a missing file — delivered to
// debug.log. Without them a broken configuration shows up only as missing
// colours, or as a session error naming nothing but a throw site.
func colorerSessionOptions() []colorer.Option {
	level, enabled := colorerDiagnosticsLevel()
	if !enabled {
		return nil
	}
	return []colorer.Option{colorer.WithDiagnostics(level, logColorerDiagnostic)}
}

func logColorerDiagnostic(d colorer.Diagnostic) {
	vtui.DebugLog("COLORER: %s", d)
}

// ColorerSource is everything a Colorer session is built from: the
// configuration directory and the user's own schemes and colour styles.
// Sessions built from equal sources are interchangeable, and that is what the
// pool and the caches compare — a changed user path is never answered by a
// session loaded without it.
type ColorerSource struct {
	ConfigsDir string
	// UserHRC and UserHRD are FarColorer's user file of schemes and user file
	// of color styles: a file or a folder each, empty for none.
	UserHRC string
	UserHRD string
	// UserHRCSettings is FarColorer's user HRC settings file, empty for none.
	UserHRCSettings string
}

var colorerPercentVar = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)%`)

// expandColorerUserPath resolves what a user writes in a path setting: a
// leading ~ for the home folder, $NAME and ${NAME}, and %NAME% on Windows. The
// library is handed a plain path, and "stat ~/.config/...: no such file" was
// the answer to a path that was perfectly good in a shell (#277). Names that are
// not set are left as written.
func expandColorerUserPath(path string) string {
	if runtime.GOOS == "windows" {
		path = colorerPercentVar.ReplaceAllStringFunc(path, func(m string) string {
			if value, ok := os.LookupEnv(m[1 : len(m)-1]); ok {
				return value
			}
			return m
		})
	}
	path = os.Expand(path, func(name string) string {
		if value, ok := os.LookupEnv(name); ok {
			return value
		}
		return "$" + name
	})
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := hostmode.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// colorerLocationTagRe matches a <location> element in an hrd-sets XML file,
// whichever attributes it carries and whether or not it self-closes.
var colorerLocationTagRe = regexp.MustCompile(`<location(?:\s[^>]*)?/?>`)

// colorerLinkAttrRe matches that element's link attribute and captures its
// value, single or double quoted.
var colorerLinkAttrRe = regexp.MustCompile(`\blink\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// colorerUserHRDCacheDirName is where materializeUserHRDPath copies the
// files a user's hrd-sets <location link> points to. It lives inside the
// Colorer configuration directory's own "base" folder — the one directory
// Colorer resolves every <location link> against, no matter which file
// contains the element (see materializeUserHRDPath) — under a name no
// catalog uses.
const colorerUserHRDCacheDirName = ".f4-user-hrd-cache"

// materializeUserHRDPath works around a Colorer limitation reported in
// f4#277 by montoner0: a <location link> in an hrd-sets file — the format
// EditorColorerUserHrd loads when it names a single XML file rather than a
// folder of standalone .hrd files — resolves against catalog.xml's own
// directory, never against the file that contains the link (traced to
// ParserFactory::Impl::fillMapper in colorer4go's vendored Colorer-library,
// which always resolves hrd_location entries against base_catalog_path).
// HRC schemes have no such problem: HrcLibraryImpl resolves a scheme's
// <location link> against the file that contains it, via
// XmlInputSource::createRelative, which is exactly what this function gives
// hrd-sets files by another route.
//
// A user file that lives anywhere else on disk and links to a sibling .hrd
// file by a plain relative path could therefore never find it. This copies
// every such link's target into configsDir/base/.f4-user-hrd-cache — inside
// the directory Colorer does resolve links against — and hands Colorer a
// rewritten copy of the file whose links point there instead. What is left
// untouched, on purpose:
//
//   - a folder of standalone .hrd files: each names itself in its own root
//     element and carries no <location> indirection to fix;
//   - a link that is empty, absolute, a URL, or uses an XML entity such as
//     &hrd; — only catalog.xml's own DOCTYPE defines those, and a link
//     written that way already means "resolve me against the catalog",
//     which keeps working exactly as before.
//
// Anything this function cannot read, parse or copy falls back to the
// original path, which is always a valid, if limited, answer — today's
// behaviour.
func materializeUserHRDPath(configsDir, userHRDPath string) string {
	info, err := os.Stat(userHRDPath)
	if err != nil || info.IsDir() {
		return userHRDPath
	}
	data, err := os.ReadFile(userHRDPath)
	if err != nil {
		return userHRDPath
	}
	origDir := filepath.Dir(userHRDPath)
	cacheDir := filepath.Join(configsDir, "base", colorerUserHRDCacheDirName)
	newContent, changed := rewriteUserHRDLocationLinks(data, origDir, cacheDir)
	if !changed {
		return userHRDPath
	}
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return userHRDPath
	}
	topPath := filepath.Join(cacheDir, "top-"+filepath.Base(userHRDPath))
	// #nosec G703 -- cacheDir is fixed (configsDir/base/.f4-user-hrd-cache) and
	// filepath.Base strips any ".."/separator the setting could carry, so this
	// cannot write outside cacheDir.
	if err := os.WriteFile(topPath, newContent, 0600); err != nil {
		return userHRDPath
	}
	return topPath
}

// rewriteUserHRDLocationLinks rewrites every plain relative <location link>
// in content — an hrd-sets file whose own directory is origDir — to point
// into cacheDir instead, copying each link's target there under an
// ASCII-safe generated name (Colorer's legacy strings read a file name as
// CP1251, same limit as EditorColorerUserHrd itself). A link this function
// does not rewrite, and every other byte of content, is returned unchanged;
// changed is false when nothing needed rewriting, in which case the caller
// keeps the original file.
func rewriteUserHRDLocationLinks(content []byte, origDir, cacheDir string) ([]byte, bool) {
	changed := false
	n := 0
	out := colorerLocationTagRe.ReplaceAllFunc(content, func(tag []byte) []byte {
		loc := colorerLinkAttrRe.FindSubmatchIndex(tag)
		if loc == nil {
			return tag
		}
		start, end := loc[2], loc[3]
		if start < 0 {
			start, end = loc[4], loc[5]
		}
		link := string(tag[start:end])
		if link == "" || strings.ContainsRune(link, '&') || filepath.IsAbs(link) || strings.Contains(link, "://") {
			return tag
		}
		srcPath := filepath.Join(origDir, filepath.FromSlash(link))
		if srcInfo, statErr := os.Stat(srcPath); statErr != nil || srcInfo.IsDir() {
			return tag
		}
		data, readErr := os.ReadFile(srcPath)
		if readErr != nil {
			return tag
		}
		n++
		cacheName := fmt.Sprintf("link%d.hrd", n)
		if mkErr := os.MkdirAll(cacheDir, 0700); mkErr != nil {
			return tag
		}
		// #nosec G703 -- cacheName is generated here from n and a fixed
		// extension, not from link, so it cannot carry a traversal segment.
		if writeErr := os.WriteFile(filepath.Join(cacheDir, cacheName), data, 0600); writeErr != nil {
			return tag
		}
		changed = true
		newTag := make([]byte, 0, len(tag)+len(colorerUserHRDCacheDirName)+len(cacheName))
		newTag = append(newTag, tag[:start]...)
		newTag = append(newTag, colorerUserHRDCacheDirName+"/"+cacheName...)
		newTag = append(newTag, tag[end:]...)
		return newTag
	})
	return out, changed
}

// CurrentColorerSource is the source the applied configuration names.
func CurrentColorerSource() ColorerSource {
	return ColorerSource{
		ConfigsDir:      ColorerConfigsDir(),
		UserHRC:         strings.TrimSpace(config.App.EditorColorerUserHrc),
		UserHRD:         strings.TrimSpace(config.App.EditorColorerUserHrd),
		UserHRCSettings: strings.TrimSpace(config.App.EditorColorerHrcSettings),
	}
}

// sessionOptions are the options a session from this source is created with.
func (src ColorerSource) sessionOptions() []colorer.Option {
	return append(colorerSessionOptions(), src.userOptions()...)
}

// userOptions load far2l's HRC settings, when the configuration has them, and
// the user's own colour styles and schemes.
func (src ColorerSource) userOptions() []colorer.Option {
	var opts []colorer.Option
	if settings := colorerHRCSettingsPath(src.ConfigsDir); fileExists(settings) {
		opts = append(opts, colorer.WithHRCSettings(settings))
	}
	if src.UserHRD != "" {
		hrdPath := expandColorerUserPath(src.UserHRD)
		opts = append(opts, colorer.WithUserHRD(materializeUserHRDPath(src.ConfigsDir, hrdPath)))
	}
	if src.UserHRC != "" {
		opts = append(opts, colorer.WithUserHRC(expandColorerUserPath(src.UserHRC)))
	}
	if src.UserHRCSettings != "" {
		opts = append(opts, colorer.WithUserHRCSettings(expandColorerUserPath(src.UserHRCSettings)))
	}
	return opts
}

func acquireColorerSession(src ColorerSource) (*colorer.Session, error) {
	if err := colorerRuntimeCheck(); err != nil {
		return nil, err
	}
	ensureRadiolaSchema(src.ConfigsDir)

	colorerPoolMu.Lock()
	if colorerIdle != nil && colorerIdleSource == src {
		session := colorerIdle
		colorerIdle = nil
		colorerPoolMu.Unlock()
		session.Reset()
		vtui.DebugLog("COLORER: Reusing a pooled session")
		applyColorerProfile(session)
		return session, nil
	}
	colorerPoolMu.Unlock()

	catalogPath := "/base/catalog.xml"
	vtui.DebugLog("COLORER: Initializing session with catalog %q, configs %q, user schemes %q, user styles %q", catalogPath, src.ConfigsDir, src.UserHRC, src.UserHRD)
	session, err := colorer.NewSession(context.Background(), catalogPath, src.ConfigsDir, src.sessionOptions()...)
	if err == nil {
		applyColorerProfile(session)
	}
	return session, err
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// A pooled Session owns the context it was created with. Colorer parsing gets
// a private context instead, so Esc can interrupt an in-flight ParseLine.
func acquireCancelableColorerSession(ctx context.Context, src ColorerSource) (*colorer.Session, error) {
	if err := colorerRuntimeCheck(); err != nil {
		return nil, err
	}
	ensureRadiolaSchema(src.ConfigsDir)

	catalogPath := "/base/catalog.xml"
	vtui.DebugLog("COLORER: Initializing cancellable session with catalog %q, configs %q, user schemes %q, user styles %q", catalogPath, src.ConfigsDir, src.UserHRC, src.UserHRD)
	session, err := colorer.NewSession(ctx, catalogPath, src.ConfigsDir, src.sessionOptions()...)
	if err == nil {
		applyColorerProfile(session)
	}
	return session, err
}

func releaseColorerSession(session *colorer.Session, src ColorerSource) {
	if session == nil {
		return
	}
	// A call that failed leaves the session refusing every later one; pooling
	// it would hand that failure to whoever acquires it next, attributed to
	// whatever they were trying to do.
	if err := session.Err(); err != nil {
		vtui.DebugLog("COLORER: Closing a failed session instead of pooling it: %v", err)
		go session.Close()
		return
	}
	colorerPoolMu.Lock()
	if colorerIdle == nil {
		colorerIdle = session
		colorerIdleSource = src
		colorerPoolMu.Unlock()
		return
	}
	colorerPoolMu.Unlock()
	go session.Close()
}

func ResetColorerSessions() {
	colorerPoolMu.Lock()
	session := colorerIdle
	colorerIdle = nil
	colorerIdleSource = ColorerSource{}
	colorerPoolMu.Unlock()
	if session != nil {
		session.Close()
	}
}

func ColorerConfigsDir() string {
	if custom := strings.TrimSpace(config.App.EditorColorerCatalog); custom != "" {
		return custom
	}
	return DefaultColorerConfigsDir()
}

// DefaultColorerConfigsDir is where the configuration lives when none is set.
func DefaultColorerConfigsDir() string {
	return filepath.Join(config.GetF4ConfigDir(), "colorer", "configs")
}

func ResetColorerRegions() {
	// No-op now that region graph scanning is offloaded to C++
}

type ColorerScheme struct {
	Name        string
	Description string
}

func ListColorerSchemes() []ColorerScheme {
	return ListColorerSchemesFor(CurrentColorerSource())
}

// ListColorerSchemesFor lists the RGB colour styles a session from src offers,
// the user's own included. Colorer itself reads the catalog: it pulls its
// style lists in through XML entities, which only a full XML reader follows.
// It instantiates Colorer on a cache miss, so a caller on the UI thread should
// not be the first to ask for a source.
func ListColorerSchemesFor(src ColorerSource) []ColorerScheme {
	// Fast path: return cached list if already loaded.
	schemesCacheMu.RLock()
	if cachedSchemes != nil && cachedSchemesSource == src {
		defer schemesCacheMu.RUnlock()
		return cachedSchemes
	}
	schemesCacheMu.RUnlock()

	// Slow path: load schemes from disk.
	session, err := acquireColorerSession(src)
	if err != nil {
		vtui.DebugLog("COLORER: Cannot list colour styles, session failed: %v", err)
		return nil
	}
	defer releaseColorerSession(session, src)

	instances, err := session.EnumHRDInstances("rgb")
	if err != nil {
		vtui.DebugLog("COLORER: Cannot list colour styles: %v", err)
		return nil
	}
	var schemes []ColorerScheme
	for _, inst := range instances {
		schemes = append(schemes, ColorerScheme{
			Name:        inst.Name,
			Description: inst.Description,
		})
	}

	// Store in cache.
	schemesCacheMu.Lock()
	cachedSchemes = schemes
	cachedSchemesSource = src
	schemesCacheMu.Unlock()

	return schemes
}

// ResetColorerSchemesCache clears the cached scheme list.
// Useful for tests that need to simulate a fresh environment.
func ResetColorerSchemesCache() {
	schemesCacheMu.Lock()
	cachedSchemes = nil
	schemesCacheMu.Unlock()
}

func ColorerSchemeLabel(scheme ColorerScheme) string {
	if label := strings.TrimSpace(scheme.Description); label != "" {
		return label
	}
	return scheme.Name
}

var (
	schemeMu         sync.Mutex
	schemeName       string
	schemeGeneration uint64

	// cachedSchemes holds the list of Colorer schemes loaded from disk for
	// cachedSchemesSource. It is populated lazily by ListColorerSchemesFor.
	cachedSchemes       []ColorerScheme
	cachedSchemesSource ColorerSource
	schemesCacheMu      sync.RWMutex
)

func SetColorerScheme(name string) {
	schemeMu.Lock()
	unchanged := name == schemeName
	schemeMu.Unlock()
	if unchanged {
		return
	}

	schemeMu.Lock()
	schemeName = name
	schemeGeneration++
	schemeMu.Unlock()

	vtui.DebugLog("COLORER: Color style %q activated", name)
}

func ResetColorerScheme() {
	schemeMu.Lock()
	schemeName = ""
	schemeGeneration++
	schemeMu.Unlock()
}

func ColorerSchemeGeneration() uint64 {
	schemeMu.Lock()
	defer schemeMu.Unlock()
	return schemeGeneration
}

const colorerBackgroundRegion = "def:Text"

var colorerBackgroundCache struct {
	sync.Mutex
	generation uint64
	source     ColorerSource
	ready      bool
	loading    bool
	define     *colorer.RegionDefine
}

// We need a helper to get region definition globally from the active scheme.
func ColorerGetRegionDefine(region string) *colorer.RegionDefine {
	src := CurrentColorerSource()
	schemeMu.Lock()
	activeScheme := schemeName
	schemeMu.Unlock()
	return colorerGetRegionDefineFor(region, src, activeScheme)
}

func colorerGetRegionDefineFor(region string, src ColorerSource, activeScheme string) *colorer.RegionDefine {
	session, err := acquireColorerSession(src)
	if err != nil {
		vtui.DebugLog("COLORER: Cannot read region %q, session failed: %v", region, err)
		return nil
	}
	defer releaseColorerSession(session, src)

	if activeScheme == "" {
		activeScheme = "default"
	}
	if err := session.SetHRD("rgb", activeScheme); err != nil {
		vtui.DebugLog("COLORER: Cannot read region %q, colour style %q failed: %v", region, activeScheme, err)
		return nil
	}

	// A region the style does not define is an ordinary answer, not a fault;
	// only a failed session is worth a line in the log.
	rd, err := session.GetRegionDefine(region)
	if err != nil && session.Err() != nil {
		vtui.DebugLog("COLORER: Cannot read region %q: %v", region, err)
	}
	return rd
}

func ColorerEditorBaseAttr(base uint64) uint64 {
	if !config.App.EditorColorerBackground {
		return base
	}
	if !strings.EqualFold(config.App.EditorHighlighter, "Colorer") {
		return base
	}

	gen := ColorerSchemeGeneration()
	src := CurrentColorerSource()
	colorerBackgroundCache.Lock()
	if colorerBackgroundCache.ready && colorerBackgroundCache.generation == gen && colorerBackgroundCache.source == src {
		rd := colorerBackgroundCache.define
		colorerBackgroundCache.Unlock()
		return applyColorerBackground(base, rd)
	}
	if !colorerBackgroundCache.loading {
		colorerBackgroundCache.loading = true
		frames := vtui.FrameManager
		requestedGeneration := gen
		requestedSource := src
		schemeMu.Lock()
		requestedScheme := schemeName
		schemeMu.Unlock()
		colorerSetups.start()
		go func() {
			defer colorerSetups.done()
			rd := colorerGetRegionDefineFor(colorerBackgroundRegion, requestedSource, requestedScheme)
			colorerBackgroundCache.Lock()
			colorerBackgroundCache.define = rd
			colorerBackgroundCache.generation = requestedGeneration
			colorerBackgroundCache.source = requestedSource
			colorerBackgroundCache.ready = true
			colorerBackgroundCache.loading = false
			colorerBackgroundCache.Unlock()
			if frames != nil {
				frames.Redraw()
			}
		}()
	}
	colorerBackgroundCache.Unlock()
	return base
}

func applyColorerBackground(base uint64, rd *colorer.RegionDefine) uint64 {
	if rd == nil {
		return base
	}

	attr := base
	if rd.IsForeSet {
		attr = vtui.SetRGBFore(attr, rd.Fore)
	}
	if rd.IsBackSet {
		attr = vtui.SetRGBBack(attr, rd.Back)
	}
	return attr
}

func newColorerHighlighter(ev *EditorView, filename, firstLine string, fallback vtui.Highlighter) *ColorerHighlighter {
	SetColorerScheme(config.App.EditorColorerScheme)

	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	ch := &ColorerHighlighter{
		fallback:      fallback,
		filename:      filename,
		firstLine:     firstLine,
		starting:      true,
		colorerSrc:    CurrentColorerSource(),
		owner:         ev,
		sessionCtx:    sessionCtx,
		sessionCancel: sessionCancel,
	}
	if ev != nil {
		ch.SetLineSource(ev.lineTextForHighlight)
	}

	// Read on the goroutine that starts this work, not inside it: the
	// work outlives the call, and reading the global from it races
	// anything that reassigns vtui.FrameManager meanwhile.
	frames := vtui.FrameManager
	ch.postTask = frames.PostTask
	ch.redraw = frames.Redraw
	colorerSetups.start()
	go func() {
		defer colorerSetups.done()
		session, err := acquireCancelableColorerSession(sessionCtx, ch.colorerSrc)
		if err != nil {
			vtui.DebugLog("COLORER: Failed to init session: %v", err)
			if !ch.closed {
				ch.useFallback(ev)
			}
			return
		}

		schemeMu.Lock()
		activeScheme := schemeName
		schemeMu.Unlock()

		if activeScheme == "" {
			activeScheme = "default"
		}
		if hErr := session.SetHRD("rgb", activeScheme); hErr != nil {
			vtui.DebugLog("COLORER: Colour style %q failed for %q, using the fallback highlighter: %v", activeScheme, filename, hErr)
			session.Close()
			if !ch.closed {
				ch.useFallback(ev)
			}
			return
		}

		selected, sErr := session.SelectType(filename, firstLine)
		detected, _ := session.FileType()
		settings := readColorerTypeSettings(session)
		vtui.DebugLog("COLORER: SelectType(%q, len=%d) -> selected=%v, err=%v", filename, len(firstLine), selected, sErr)
		if sErr != nil || !selected {
			session.Close()
			if !ch.closed {
				ch.useFallback(ev)
			}
			return
		}

		frames.PostTask(func() {
			if ch.closed || ch.disabled {
				session.Close()
				return
			}
			if ev != nil && ev.Highlighter != vtui.Highlighter(ch) {
				session.Close()
				return
			}
			if closer, ok := ch.fallback.(io.Closer); ok {
				closer.Close()
			}
			ch.fallback = nil
			ch.session = session
			ch.detectedType = detected
			ch.typeSettings = settings
			ch.workerTypeSettings = settings
			ch.starting = false
			ch.attrCache = nil
			ch.parsedIdx = 0
			ch.startWorker(session)
			if ev != nil {
				ev.finishColorerWork(ch.startupWorkID)
				ev.invalidateStates(0)
			}
			frames.Redraw()
		})
	}()

	return ch
}

func (ch *ColorerHighlighter) useFallback(ev *EditorView) {
	vtui.FrameManager.PostTask(func() {
		if !ch.starting || ch.closed || ch.disabled {
			return
		}
		ch.starting = false
		if ev != nil {
			// With no session of its own this object has nothing left to
			// add, and while it sits in ev.highlighter the editor treats
			// the engine behind it as if it were Colorer: no state chain,
			// no walker. Hand the engine over instead.
			if fb := ch.fallback; fb != nil && ev.Highlighter == vtui.Highlighter(ch) {
				ch.fallback = nil
				ev.Highlighter = fb
				ev.colorerFellBack = true
			}
			ev.finishColorerWork(ch.startupWorkID)
			ev.invalidateStates(0)
		}
		vtui.FrameManager.Redraw()
	})
}

// colorerStaleLine is one line's colours kept from before an edit.
type colorerStaleLine struct {
	attrs []uint64
	bg    uint64
}

type ColorerHighlighter struct {
	session    *colorer.Session
	fallback   vtui.Highlighter
	attrCache  map[int][]uint64
	bgCache    map[int]uint64
	pairCache  map[int][]colorer.Pair // kept and evicted with attrCache
	pairSearch *colorerPairSearch     // a whole-file pair search in progress
	parsedIdx  int
	filename   string
	firstLine  string
	colorerSrc ColorerSource
	closed     bool
	starting   bool
	owner      *EditorView
	postTask   func(func())
	redraw     func()

	// outlineCache holds each parsed line's outline entries, kept and
	// evicted with attrCache; outlineBuild is a whole-file outline in
	// progress.
	outlineCache map[int][]colorerOutlineEntry
	outlineBuild *colorerOutlineBuild
	// regionCache holds each parsed line's regions, kept and evicted with
	// attrCache, for select region.
	regionCache map[int][]colorerRegionSpan

	// stale holds the colours an edit dropped. Fresh colours come from the
	// worker a moment later; until then the old ones are drawn instead of
	// plain text, so a keystroke does not blink every line below it (#1230).
	// lineCount is the document's line count when the colours were last
	// current, to tell how far an edit moved the lines below it.
	stale     map[int]colorerStaleLine
	lineCount int

	// The type the user picked from the list of types, "" to choose by file
	// name; and the type the file name chose. Both UI-owned. workerFileType
	// is the type the worker last gave its session, and only the worker
	// touches it.
	fileTypeOverride string
	detectedType     string
	workerFileType   string

	// The file type's parameters FarEditor::reloadTypeSettings applies:
	// typeSettings for the UI, workerTypeSettings for the worker, which
	// reads them whenever it gives its session a type and hands changes to
	// the UI.
	typeSettings       colorerTypeSettings
	workerTypeSettings colorerTypeSettings

	// The session and its worker share this context. The worker is the only
	// goroutine allowed to call ParseLine/Reset/SelectType on the live session;
	// the UI only queues immutable line snapshots and consumes results.
	sessionCtx     context.Context
	sessionCancel  context.CancelFunc
	workerJobs     chan colorerJob
	workerDone     chan struct{}
	pending        bool
	disabled       bool
	forceReset     bool
	workGeneration uint64
	startupWorkID  uint64

	// lineAt hands over the text of any logical line, so re-anchoring can
	// read the context it needs straight from the document instead of the
	// highlighter keeping a second copy of the file.
	lineAt func(idx int) (string, bool)
}

// SetLineSource gives the highlighter a way to read the document.
func (ch *ColorerHighlighter) SetLineSource(fn func(idx int) (string, bool)) {
	ch.lineAt = fn
}

// Highlight is the vtui.Highlighter entry point. It is still needed while
// the session is loading or has been given up on; the editor draws Colorer
// with a live session through HighlightLine instead, so this delegates
// nothing in that case. See HIGHLIGHT.md, phase 5c and item 2.
func (ch *ColorerHighlighter) Highlight(line string, prevState any, baseAttr uint64) ([]uint64, any) {
	if ch.session == nil {
		if ch.starting {
			return nil, nil
		}
		if ch.fallback != nil {
			return ch.fallback.Highlight(line, prevState, baseAttr)
		}
		return nil, nil
	}
	return nil, nil
}

func (ch *ColorerHighlighter) attrsForSyntax(line string, regions []colorer.Region, baseAttr uint64, syntax bool) ([]uint64, uint64) {
	return colorerAttrs(line, regions, baseAttr, syntax, ch.workerTypeSettings.plainEOL)
}

// colorerAttrs turns a parsed line's regions into the attribute of each rune,
// over baseAttr, and the colour for the rest of the row. plainEOL is
// fullback=no: a region running to the end of the line colours only the text.
func colorerAttrs(line string, regions []colorer.Region, baseAttr uint64, syntax, plainEOL bool) ([]uint64, uint64) {
	lineRunes := colorerLineRuneCount(line)
	attrs := make([]uint64, lineRunes)
	for i := range attrs {
		attrs[i] = baseAttr
	}

	eolBg := baseAttr
	for _, reg := range regions {
		if !syntax {
			continue
		}

		start, end, toEOL := colorerRegionRunes(reg.Start, reg.End, lineRunes)
		rd := colorer.RegionDefine{
			Fore:      reg.Fore,
			Back:      reg.Back,
			Style:     reg.Style,
			IsForeSet: reg.IsForeSet,
			IsBackSet: reg.IsBackSet,
		}

		// fullback=no keeps the colour of a region running to the end of
		// the line on its text, off the rest of the row.
		if toEOL && !plainEOL {
			eolBg = applyColorerStyle(eolBg, &rd)
		}
		for i := start; i < end; i++ {
			attrs[i] = applyColorerStyle(attrs[i], &rd)
		}
	}
	return attrs, eolBg
}

// HighlightLine colours one line addressed by its real number in the document.
//
// This is the path the editor draws through. It replaces the state chain that
// a Colorer session cannot provide: instead of carrying a state from line to
// line, the session is parked next to the viewport and fed forward from there.
func (ch *ColorerHighlighter) HighlightLine(idx int, line string, baseAttr uint64) []uint64 {
	if ch.session == nil || idx < 0 || ch.closed || ch.disabled {
		return nil
	}

	if attrs, ok := ch.attrCache[idx]; ok {
		return attrs
	}

	// ParseLine can execute arbitrary Colorer grammar code and is therefore
	// never allowed to run from DisplayObject's UI render path. queueLine makes
	// a bounded snapshot of the required context and the worker does all WASM
	// calls asynchronously.
	ch.queueLine(idx, line, baseAttr)
	return ch.stale[idx].attrs
}

// colorerContextPlan decides how the session gets to idx: fed forward from
// where it stands, or reset and restarted from a fixed run of context lines.
//
// Feeding forward is what sequential scrolling gets, and it keeps every
// construct opened above intact. A jump, or any move backwards — the session
// cannot be rewound — costs the same whether it is over a hundred lines or
// half a million, which is the whole point of the anchor.
func colorerContextPlan(parsedIdx, idx int) (start int, reset bool) {
	if parsedIdx <= idx && idx-parsedIdx <= hlColorerForward {
		return parsedIdx, false
	}
	start = idx - hlColorerContext
	if start < 0 {
		start = 0
	}
	return start, true
}

// colorerForgetPlan decides whether forgetBehind has enough new lines behind
// the parse position to be worth a wasm call, and where the cut should land.
// Pure, like colorerContextPlan, so the batching threshold is tested without
// a session.
func colorerForgetPlan(parsedIdx, forgottenUpTo int) (keepFrom int, do bool) {
	if parsedIdx-forgottenUpTo < hlColorerForgetEvery {
		return 0, false
	}
	keepFrom = parsedIdx - hlColorerKeepBehind
	if keepFrom <= forgottenUpTo {
		return 0, false
	}
	return keepFrom, true
}

// DropFrom forgets everything the highlighter knows from line idx on. An edit
// invalidates the colours below it, and it invalidates the session itself
// whenever the session has already parsed past that line: its cache cannot be
// unwound, only thrown away. Nothing is kept to draw in the meantime; that is
// for DropAfterEdit, where the old colours are still a good guess.
func (ch *ColorerHighlighter) DropFrom(idx int) {
	if idx < 0 {
		idx = 0
	}
	ch.dropCacheFrom(idx)
	for key := range ch.stale {
		if key >= idx {
			delete(ch.stale, key)
		}
	}
	ch.abandonWork()
}

// DropAfterEdit is DropFrom for a text edit at line idx that leaves the
// document with lineCount lines. The colours it drops are kept, moved along
// with the lines they belonged to, and drawn until the worker has the new ones:
// without that every keystroke would show the lines below it as plain text for
// a moment (#1230).
func (ch *ColorerHighlighter) DropAfterEdit(idx, lineCount int) {
	if idx < 0 {
		idx = 0
	}
	delta := 0
	if ch.lineCount > 0 {
		delta = lineCount - ch.lineCount
	}
	ch.lineCount = lineCount
	ch.keepStale(idx, delta)
	ch.dropCacheFrom(idx)
	ch.abandonWork()
}

// DropAfterReplace is DropAfterEdit for Undo and Redo, which put back an
// earlier text without saying what changed. The colours of every line are kept
// to draw until the worker has fresh ones (else the whole screen blinks plain,
// #1230); the lines from idx on, where the change begins, move by the change in
// the line count, and those above it stay where they are. Everything is
// recomputed from the top: none of it is trusted as current.
func (ch *ColorerHighlighter) DropAfterReplace(idx, lineCount int) {
	if idx < 0 {
		idx = 0
	}
	delta := 0
	if ch.lineCount > 0 {
		delta = lineCount - ch.lineCount
	}
	ch.lineCount = lineCount

	old := make(map[int]colorerStaleLine, len(ch.stale)+len(ch.attrCache))
	for key, line := range ch.stale {
		old[key] = line
	}
	for key, attrs := range ch.attrCache {
		old[key] = colorerStaleLine{attrs: attrs, bg: ch.bgCache[key]}
	}
	ch.stale = make(map[int]colorerStaleLine, len(old))
	// The lines that moved go in first, so that one that lands on a line above
	// the change does not displace what has always been there.
	for key, line := range old {
		if key >= idx {
			if to := key + delta; to >= idx {
				ch.stale[to] = line
			}
		}
	}
	for key, line := range old {
		if key < idx {
			ch.stale[key] = line
		}
	}
	ch.dropCacheFrom(0)
	ch.abandonWork()
}

// noteLineCount records the line count the cached colours belong to. The
// editor calls it every frame, so an edit sees how many lines it added or
// removed.
func (ch *ColorerHighlighter) noteLineCount(n int) {
	ch.lineCount = n
}

// keepStale moves the colours of lines idx and below, fresh or already stale,
// into the stale set, delta lines further down (up, if negative). The edited
// line keeps its old colours in place as well: the text of it changed, but
// most of it is where it was.
func (ch *ColorerHighlighter) keepStale(idx, delta int) {
	moved := make(map[int]colorerStaleLine)
	for key, line := range ch.stale {
		if key >= idx {
			moved[key] = line
			delete(ch.stale, key)
		}
	}
	for key, attrs := range ch.attrCache {
		if key >= idx {
			moved[key] = colorerStaleLine{attrs: attrs, bg: ch.bgCache[key]}
		}
	}
	if len(moved) == 0 {
		return
	}
	if ch.stale == nil {
		ch.stale = make(map[int]colorerStaleLine, len(moved))
	}
	for key, line := range moved {
		if to := key + delta; to >= idx {
			ch.stale[to] = line
		}
	}
	if line, ok := moved[idx]; ok {
		if _, taken := ch.stale[idx]; !taken {
			ch.stale[idx] = line
		}
	}
}

// abandonWork throws away what the worker was doing: the results of a job in
// flight describe text that is gone, and the session is re-anchored next frame.
func (ch *ColorerHighlighter) abandonWork() {
	// An edit moves the text a pair search walks; its tokens are stale, and
	// so is an outline collected so far.
	ch.pairSearch = nil
	ch.outlineBuild = nil
	// The worker may currently be inside ParseLine. Do not touch its session
	// from the UI; invalidate that result and let the next frame enqueue a
	// fresh anchored snapshot.
	ch.forceReset = true
	ch.workGeneration++
	ch.parsedIdx = 0
}

func (ch *ColorerHighlighter) GetLineBackground(idx int, defaultAttr uint64) uint64 {
	if bg, ok := ch.bgCache[idx]; ok {
		return bg
	}
	if line, ok := ch.stale[idx]; ok {
		return line.bg
	}
	return defaultAttr
}

func (ch *ColorerHighlighter) storeAttrs(idx int, attrs []uint64, bg uint64, pairs []colorer.Pair) {
	if ch.attrCache == nil {
		ch.attrCache = make(map[int][]uint64)
	}
	if ch.bgCache == nil {
		ch.bgCache = make(map[int]uint64)
	}
	if len(ch.attrCache) >= maxCachedAttrLines {
		for key := range ch.attrCache {
			// Do not evict lines near top of document (0..2000) so Ctrl+Home always retains instant cached colors
			if key > 2000 && (key < idx-attrCacheKeepWindow || key > idx+attrCacheKeepWindow) {
				delete(ch.attrCache, key)
				delete(ch.bgCache, key)
				delete(ch.pairCache, key)
				delete(ch.outlineCache, key)
				delete(ch.regionCache, key)
			}
		}
		if len(ch.attrCache) >= maxCachedAttrLines {
			ch.attrCache = make(map[int][]uint64)
			ch.bgCache = make(map[int]uint64)
			ch.pairCache = nil
			ch.outlineCache = nil
			ch.regionCache = nil
		}
	}
	ch.attrCache[idx] = attrs
	ch.bgCache[idx] = bg
	delete(ch.stale, idx)
	delete(ch.outlineCache, idx)
	delete(ch.regionCache, idx)
	if len(pairs) > 0 {
		if ch.pairCache == nil {
			ch.pairCache = make(map[int][]colorer.Pair)
		}
		ch.pairCache[idx] = pairs
	} else {
		delete(ch.pairCache, idx)
	}
}

func (ch *ColorerHighlighter) dropCacheFrom(idx int) {
	for key := range ch.attrCache {
		if key >= idx {
			delete(ch.attrCache, key)
		}
	}
	for key := range ch.bgCache {
		if key >= idx {
			delete(ch.bgCache, key)
		}
	}
	for key := range ch.pairCache {
		if key >= idx {
			delete(ch.pairCache, key)
		}
	}
	for key := range ch.outlineCache {
		if key >= idx {
			delete(ch.outlineCache, key)
		}
	}
	for key := range ch.regionCache {
		if key >= idx {
			delete(ch.regionCache, key)
		}
	}
}

func (ch *ColorerHighlighter) Close() error {
	ch.closed = true
	ch.disabled = true
	if ch.sessionCancel != nil {
		ch.sessionCancel()
	}
	ch.stopWorker()
	ch.attrCache = nil
	ch.pairCache = nil
	ch.pairSearch = nil
	ch.outlineCache = nil
	ch.outlineBuild = nil
	ch.regionCache = nil
	ch.stale = nil
	ch.parsedIdx = 0
	if closer, ok := ch.fallback.(io.Closer); ok {
		closer.Close()
	}
	ch.fallback = nil
	// Worker-owned sessions are closed by runWorker. A highlighter created by
	// tests or a caller without a worker still uses the old pooled lifecycle.
	if ch.session != nil && ch.workerDone == nil {
		releaseColorerSession(ch.session, ch.colorerSrc)
	}
	ch.session = nil
	return nil
}

// storeOutline keeps a parsed line's outline entries; call it after storeAttrs,
// which evicts them with the line's colours.
func (ch *ColorerHighlighter) storeOutline(idx int, entries []colorerOutlineEntry) {
	if len(entries) == 0 {
		return
	}
	if ch.outlineCache == nil {
		ch.outlineCache = make(map[int][]colorerOutlineEntry)
	}
	ch.outlineCache[idx] = entries
}

// storeRegions keeps a parsed line's regions; call it after storeAttrs, which
// evicts them with the line's colours.
func (ch *ColorerHighlighter) storeRegions(idx int, spans []colorerRegionSpan) {
	if len(spans) == 0 {
		return
	}
	if ch.regionCache == nil {
		ch.regionCache = make(map[int][]colorerRegionSpan)
	}
	ch.regionCache[idx] = spans
}
