package app

import (
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"unicode"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/update"
	"github.com/unxed/vtui"
)

var (
	titleOnce     sync.Once
	cachedHost    string
	cachedUser    string
	cachedAdmin   string
	cachedVersion string
	cachedPlat    string
	// buildVersion is set by the release workflow for tagged artifacts. Go's
	// embedded VCS metadata contains the commit but not the release tag, so
	// the updater would otherwise mistake a beta build for a development build.
	buildVersion string
)

func initTitleCache() {
	h, _ := os.Hostname()
	cachedHost = h

	u, err := user.Current()
	if err == nil && u != nil {
		cachedUser = u.Username
		if idx := strings.LastIndex(cachedUser, "\\"); idx != -1 {
			cachedUser = cachedUser[idx+1:]
		}
	} else {
		cachedUser = "user"
	}

	cachedAdmin = getAdminString()
	cachedVersion = getShortVersionInfo()
	cachedPlat = runtime.GOARCH
}

func isReleaseVersion(v string) bool {
	if !strings.HasPrefix(v, "v") {
		return false
	}
	s := v[1:]
	for _, r := range s {
		if !unicode.IsDigit(r) && r != '.' {
			return false
		}
	}
	return true
}

func getGitTag() string {
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func getGitFallback() (rev string, dirty string, timeStr string) {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", "", ""
	}
	rev = strings.TrimSpace(string(out))
	if rev == "" {
		return "", "", ""
	}
	statusOut, err := exec.Command("git", "status", "--porcelain").Output()
	if err == nil && len(strings.TrimSpace(string(statusOut))) > 0 {
		dirty = "-dirty"
	}
	timeOut, err := exec.Command("git", "log", "-1", "--format=%cI").Output()
	if err == nil {
		timeStr = strings.TrimSpace(string(timeOut))
	}
	return rev, dirty, timeStr
}

func getVCSInfo() (rev string, dirty string, timeStr string) {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
				if len(rev) > 7 {
					rev = rev[:7]
				}
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "-dirty"
				}
			case "vcs.time":
				timeStr = s.Value
			}
		}
	}
	if rev == "" {
		rev, dirty, timeStr = getGitFallback()
	}
	return rev, dirty, timeStr
}

func getShortVersionInfo() string {
	if buildVersion != "" {
		return buildVersion
	}

	baseVer := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		baseVer = info.Main.Version
	}
	if baseVer == "" || baseVer == "(devel)" {
		baseVer = getGitTag()
	}
	if baseVer == "" {
		baseVer = "(devel)"
	}

	rev, dirty, _ := getVCSInfo()
	if isReleaseVersion(baseVer) {
		return baseVer + dirty
	}
	if rev != "" {
		return rev + dirty
	}
	return baseVer
}

func getLongVersionInfo() string {
	if buildVersion != "" {
		_, _, timeStr := getVCSInfo()
		if timeStr != "" {
			return buildVersion + " [" + update.FormatBuildTime(timeStr) + "]"
		}
		return buildVersion
	}

	baseVer := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		baseVer = info.Main.Version
	}
	if baseVer == "" || baseVer == "(devel)" {
		baseVer = getGitTag()
	}
	if baseVer == "" {
		baseVer = "(devel)"
	}

	rev, dirty, timeStr := getVCSInfo()
	var sb strings.Builder
	if isReleaseVersion(baseVer) {
		sb.WriteString(baseVer + dirty)
	} else if rev != "" {
		sb.WriteString(rev + dirty)
	} else {
		sb.WriteString(baseVer)
	}
	if timeStr != "" {
		sb.WriteString(" [" + update.FormatBuildTime(timeStr) + "]")
	}
	return sb.String()
}

func UpdateWindowTitle(scr *vtui.ScreenBuf) {
	titleOnce.Do(initTitleCache)

	if vtui.FrameManager == nil {
		return
	}

	vtui.SetWindowTitle(currentWindowTitle())
	drawWorktreeIdentity(scr)

	drawMacroRecordingIndicator(scr)
}

// drawMacroRecordingIndicator marks the screen while a macro is being recorded
// (Ctrl+.), as far does. It is drawn after the menu bar, on the first row the
// workspace tab strip leaves to the application: vtui paints that strip after
// OnRender and wiped a mark on row 0 whenever the strip was shown (DkmS1953,
// f4#1870).
func drawMacroRecordingIndicator(scr *vtui.ScreenBuf) {
	if macro.MacroMgr == nil || !macro.MacroMgr.Recording || vtui.FrameManager == nil {
		return
	}
	scr.Write(0, vtui.FrameManager.WorkspaceTopInset(), vtui.StringToCharInfo(" R ", vtui.SetRGBBoth(0, 0xFFFFFF, 0xFF0000)))
}

// currentWindowTitle returns the exact title f4 exposes to the host terminal
// or GUI window. Keeping this separate from UpdateWindowTitle lets actions
// and macros report the same value the user sees in the window chrome.
func currentWindowTitle() string {
	titleOnce.Do(initTitleCache)

	state := "Panels"
	if vtui.FrameManager != nil && len(vtui.FrameManager.Screens) > 0 {
		active := vtui.FrameManager.ActiveIdx
		if active < 0 || active >= len(vtui.FrameManager.Screens) {
			active = 0
		}
		state = stableWorkspaceTitle(vtui.FrameManager.Screens[active])
	}

	template := config.App.ConsoleTitleTemplate
	if template == "" {
		template = "f4 - %State"
	}

	r := strings.NewReplacer(
		"%State", state,
		"%Ver", cachedVersion,
		"%Platform", cachedPlat,
		"%Backend", getBackendName(),
		"%Host", cachedHost,
		"%User", cachedUser,
		"%Admin", cachedAdmin,
	)

	title := r.Replace(template)
	title = strings.ReplaceAll(title, "  ", " ") // Убираем двойные пробелы, если %Admin пустой
	return title
}

var copyWindowTitleToClipboard = func(title string) {
	terminal.SetClipboardAsync(title)
}

func actionCopyWindowTitle() bool {
	identity := currentFrameIdentity()
	if identity == "" {
		// Keep a useful value during startup/shutdown, when the frame stack may
		// briefly be empty. Normal UI operation always has a top frame.
		identity = currentWindowTitle()
	}
	copyWindowTitleToClipboard(identity)
	return true
}

// currentFrameIdentity returns the help topic identity of the frame currently
// receiving input. Unlike currentWindowTitle, it intentionally includes modal
// dialogs and menus: terminal.App.CopyWindowTitle is a debugging action for the UI
// context the user is working in, not for the host terminal/workspace title.
// zoin-bot: prefer the stable help ID because it can be fed directly into the
// help translator; the visible title remains a compatibility fallback for
// frames that do not declare one.
func currentFrameIdentity() string {
	if vtui.FrameManager == nil {
		return ""
	}
	if frame := vtui.FrameManager.GetTopFrame(); frame != nil {
		if help := strings.TrimSpace(frame.GetHelp()); help != "" {
			return help
		}
		return strings.TrimSpace(frame.GetTitle())
	}
	return ""
}

func stableWorkspaceTitle(screen *vtui.AppScreen) string {
	if screen == nil {
		return "Panels"
	}
	// Keep compatibility while the corresponding VTUI API is being reviewed.
	// Once available, the structural assertion starts using it automatically.
	if provider, ok := any(screen).(interface{ GetWorkspaceTitle() string }); ok {
		return provider.GetWorkspaceTitle()
	}
	for i := len(screen.Frames) - 1; i >= 0; i-- {
		if screen.Frames[i].IsModal() {
			continue
		}
		if title := strings.TrimSpace(screen.Frames[i].GetTitle()); title != "" {
			return title
		}
	}
	return screen.GetTitle()
}

func getBackendName() string {
	if vtui.FrameManager == nil {
		return "Console"
	}
	return vtui.FrameManager.GetBackendName()
}
