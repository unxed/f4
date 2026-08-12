package main

import (
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"unicode"

	"github.com/unxed/vtui"
)

var (
	titleOnce     sync.Once
	cachedHost    string
	cachedUser    string
	cachedAdmin   string
	cachedVersion string
	cachedPlat    string
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
		tStr := strings.TrimSpace(string(timeOut))
		if len(tStr) >= 16 {
			timeStr = strings.Replace(tStr[:16], "T", " ", 1)
		}
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
				if len(timeStr) >= 16 {
					timeStr = strings.Replace(timeStr[:16], "T", " ", 1)
				}
			}
		}
	}
	if rev == "" {
		rev, dirty, timeStr = getGitFallback()
	}
	return rev, dirty, timeStr
}

func getShortVersionInfo() string {
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
		sb.WriteString(" [" + timeStr + "]")
	}
	return sb.String()
}

func UpdateWindowTitle(scr *vtui.ScreenBuf) {
	titleOnce.Do(initTitleCache)

	if vtui.FrameManager == nil {
		return
	}

	state := "Panels"
	if len(vtui.FrameManager.Screens) > 0 {
		state = stableWorkspaceTitle(vtui.FrameManager.Screens[vtui.FrameManager.ActiveIdx])
	}

	template := AppConfig.ConsoleTitleTemplate
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
	vtui.SetWindowTitle(title)

	// Macro recording indicator — drawn after MenuBar so it's always on top
	if MacroMgr != nil && MacroMgr.Recording {
		scr.Write(0, 0, vtui.StringToCharInfo(" R ", vtui.SetRGBBoth(0, 0xFFFFFF, 0xFF0000)))
	}
}

func stableWorkspaceTitle(screen *vtui.AppScreen) string {
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
