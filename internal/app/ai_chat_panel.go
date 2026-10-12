package app

import (
	"context"
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/internal/vtvibe"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// AIChatPanel is f4's AI assistant panel: the chat history/input widget
// itself is vtui.ChatWindow (extracted to vtui in f4 #615 so a future
// Telegram-client vtui port can reuse it); everything below is AI-specific
// glue between that generic control and a vtvibe.Session — provider config,
// the attached-files/apply-patch status strip, the ai:// link scheme, and
// the RCtrl+C/RCtrl+P hotkeys.
type AIChatPanel struct {
	*vtui.ChatWindow
	src *panel.FileSystemPanel
}

// aiChatStatusBar renders the strip above the input box: attached context
// files win over an available patch, because once the user is collecting
// context that is what they are working on (the patch is still one keypress
// away in ai://out). This is entirely AI-specific bookkeeping, so it lives
// here rather than in vtui.ChatWindow.
type aiChatStatusBar struct {
	cp *AIChatPanel
}

const (
	aiBarNone = iota
	aiBarFiles
	aiBarPatch
)

// barKind decides what the strip shows right now.
func (cp *AIChatPanel) barKind() int {
	session := cp.getSession()
	if session.HasNewContextFiles() {
		return aiBarFiles
	}
	if session.LastPatch() != nil {
		return aiBarPatch
	}
	// Fallback when there are some context files but no new ones
	ctxFiles := session.ContextFiles()
	userFilesCount := 0
	for _, f := range ctxFiles {
		if f != "ap.md" {
			userFilesCount++
		}
	}
	if userFilesCount > 0 && session.LastPatch() == nil {
		return aiBarFiles
	}
	return aiBarNone
}

func (b *aiChatStatusBar) Label(maxW int) string {
	session := b.cp.getSession()
	switch b.cp.barKind() {
	case aiBarFiles:
		return formatAttachedFilesLabel(session.ContextFiles(), maxW)
	case aiBarPatch:
		return formatApplyPatchLabel(session.LastPatch(), maxW)
	}
	return ""
}

// Activate is what Enter on the strip does.
func (b *aiChatStatusBar) Activate() {
	pf := panel.FindPanelsFrameAnyScreen()
	if pf == nil {
		return
	}
	switch b.cp.barKind() {
	case aiBarFiles:
		AiSetViewModePanel(pf, pf.ActiveIdx, "ai://ctx", false)
	case aiBarPatch:
		aiApplyPatch(pf)
	}
}

func NewAIChatPanel(src *panel.FileSystemPanel) *AIChatPanel {
	x1, y1, x2, y2 := src.GetPosition()
	cp := &AIChatPanel{
		ChatWindow: vtui.NewChatWindow(x1, y1, x2, y2, i18n.Msg("AI.ChatTitle")),
		src:        src,
	}
	cp.Frame.ColorBoxIdx = theme.ColPanelBox
	cp.Frame.ColorBackgroundIdx = theme.ColPanelText
	cp.ColorTitleIdx = theme.ColPanelTitle
	cp.ColorTitleFocusedIdx = theme.ColPanelSelectedTitle
	cp.Frame.ColorTitleIdx = theme.ColPanelTitle
	cp.Input.ColorTextIdx = theme.ColPanelText

	cp.ColorTextIdx = theme.ColPanelText
	cp.ColorHeaderIdx = theme.ColPanelTitle
	cp.ColorLinkIdx = vtui.ColMenuHighlight
	cp.ColorLinkFocusIdx = theme.ColPanelCursor
	cp.ColorStatusBarIdx = theme.ColPanelHighlightText
	cp.ColorHighlightBgRefIdx = theme.ColEditorText

	cp.SelfLabel = i18n.Msg("AI.ChatYou")
	cp.PeerLabel = i18n.Msg("AI.ChatModel")
	cp.BusyLabel = i18n.Msg("AI.ChatTyping")
	cp.HighlightLang = "chat.md"
	cp.URLSchemes = []string{"ai://"}

	cp.StatusBar = &aiChatStatusBar{cp: cp}
	cp.OnSend = func(text string) {
		aiSend(panel.FindPanelsFrameAnyScreen(), text)
	}
	cp.OnActivateLink = cp.navigateToTarget
	cp.OnLinkAltKey = func(target string, _ *vtinput.InputEvent) bool {
		cp.copyLinkTarget(target)
		return true
	}
	cp.OnStatusBarKey = func(e *vtinput.InputEvent) bool {
		if e.VirtualKeyCode == vtinput.VK_F3 && cp.barKind() == aiBarPatch {
			// Read the patch before trusting it.
			cp.navigateToTarget("ai://out/afix.ap")
			return true
		}
		return false
	}
	cp.ExtraLink = extraChatOutputLink

	cp.SetPosition(x1, y1, x2, y2)
	return cp
}

// extraChatOutputLink turns a ```lang:filename fenced code header into a
// jump link to that output file (the ap patch protocol's convention for
// naming the file a code block belongs to).
func extraChatOutputLink(line string) (label, target string, ok bool) {
	pStr := strings.TrimSpace(line)
	if !strings.HasPrefix(pStr, "```") {
		return "", "", false
	}
	colon := strings.Index(pStr, ":")
	if colon == -1 {
		return "", "", false
	}
	filename := strings.TrimSpace(pStr[colon+1:])
	filename = strings.TrimPrefix(filename, "ai://out/")
	filename = strings.TrimPrefix(filename, "ai://")
	filename = strings.TrimPrefix(filename, "/out/")
	filename = strings.TrimPrefix(filename, "out/")
	if filename == "" {
		return "", "", false
	}
	t := "ai://out/" + filename
	return t, t, true
}

func (cp *AIChatPanel) Kind() string                   { return "ai_chat" }
func (cp *AIChatPanel) Source() *panel.FileSystemPanel { return cp.src }

func (cp *AIChatPanel) GetSelectedName() string {
	if cp.src == nil {
		return ""
	}
	return cp.src.GetSelectedName()
}

func (cp *AIChatPanel) navigateToTarget(target string) {
	pf := panel.FindPanelsFrameAnyScreen()
	if pf == nil {
		return
	}
	if strings.HasPrefix(target, "ai://") {
		if strings.HasPrefix(target, "ai://out/") || strings.HasPrefix(target, "ai://ctx/") {
			actionOpenViewer(pf, cp.src.Vfs, target)
		} else {
			AiSetViewModePanel(pf, pf.ActiveIdx, target, false)
		}
	}
}

// ProcessKey adds the AI-specific hotkeys (copy last response, apply patch,
// undo the last applied patch)
// on top of vtui.ChatWindow's generic scrolling/link/input handling.
func (cp *AIChatPanel) ProcessKey(e *vtinput.InputEvent) bool {
	if !e.KeyDown || !cp.IsFocused() {
		return false
	}

	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	rctrl := (e.ControlKeyState & vtinput.RightCtrlPressed) != 0
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0

	if e.VirtualKeyCode == vtinput.VK_C && rctrl && !alt && !shift {
		session := cp.getSession()
		turns := session.Turns()
		for i := len(turns) - 1; i >= 0; i-- {
			if turns[i].Role != "user" && turns[i].Text != "RCtrl+A to hide" {
				terminal.SetClipboardAsync(turns[i].Text)
				toast.Show("Copied last response to clipboard", 2*time.Second)
				break
			}
		}
		return true
	}

	if e.VirtualKeyCode == vtinput.VK_P && rctrl && !alt && !shift {
		aiApplyPatch(panel.FindPanelsFrameAnyScreen())
		return true
	}

	// Ctrl+Z (either Ctrl) undoes the last applied patch (docs/VTVIBE.md
	// §7.4); the input line has no undo of its own to take it from.
	if e.VirtualKeyCode == vtinput.VK_Z && ctrl && !alt && !shift {
		aiUndoPatch(panel.FindPanelsFrameAnyScreen())
		return true
	}

	return cp.ChatWindow.ProcessKey(e)
}

// Show refreshes the turns/busy state from the current session and lets
// vtui.ChatWindow render them.
func (cp *AIChatPanel) Show(scr *vtui.ScreenBuf) {
	session := cp.getSession()
	turns := session.Turns()
	chatTurns := make([]vtui.ChatTurn, len(turns))
	for i, t := range turns {
		role := vtui.ChatRolePeer
		if t.Role == "user" {
			role = vtui.ChatRoleSelf
		}
		chatTurns[i] = vtui.ChatTurn{Role: role, Text: t.Text, Time: t.Time}
	}
	// The answer being streamed in shows as it grows (f4#1842, stage H2).
	if pending := session.Pending(); pending != "" {
		chatTurns = append(chatTurns, vtui.ChatTurn{Role: vtui.ChatRolePeer, Text: pending, Time: time.Now()})
	}
	cp.Turns = chatTurns
	cp.Busy = session.Busy()
	// The model may name the dialog (f4#1842); the name joins the title.
	title := i18n.Msg("AI.ChatTitle")
	if name := session.Title(); name != "" {
		title += ": " + name
	}
	// The working mode is always in sight (f4#1842, stage H6).
	if aiNonstop(session) {
		title += " · " + i18n.Msg("AI.ModeNonstop")
	}
	// What the dialog has spent is always in sight (f4#1842, stage H9).
	if total := vtvibe.TotalSpent(session.Spent()); total.In > 0 || total.Out > 0 {
		title += " · " + fmt.Sprintf(i18n.Msg("AI.TitleTokens"), vtvibe.FormatTokens(total.In), vtvibe.FormatTokens(total.Out))
	}
	if cp.Frame != nil && cp.Frame.GetTitle() != title {
		cp.Frame.SetTitle(title)
	}

	cp.ChatWindow.Show(scr)
}

// formatApplyPatchLabel draws the button this whole feature exists for: the
// model answered with an ap patch, one keypress applies it.
func formatApplyPatchLabel(p *vtvibe.Patch, maxW int) string {
	if p == nil {
		return ""
	}
	head := " ⚡ " + i18n.Msg("AI.ApplyPatchBar")
	label := formatBarLabel(head+" (RCtrl+P): ", p.Files, maxW)
	if label == "" {
		label = formatBarLabel(head+" ", nil, maxW)
	}
	if label == "" {
		label = formatBarLabel(" ⚡ ", nil, maxW)
	}
	return label
}

func formatAttachedFilesLabel(files []string, maxW int) string {
	if len(files) == 0 {
		return ""
	}
	return formatBarLabel(" 📎 ", files, maxW)
}

// formatBarLabel renders "<prefix>a.go, b.go " into maxW cells, dropping names
// and then the list itself rather than overflowing the frame.
func formatBarLabel(prefix string, files []string, maxW int) string {
	if maxW <= 5 {
		return ""
	}
	prefixW := runewidth.StringWidth(prefix)
	if prefixW >= maxW {
		return ""
	}

	avail := maxW - prefixW
	var parts []string
	currW := 0

	for i, f := range files {
		item := f
		if i > 0 {
			item = ", " + f
		}
		w := runewidth.StringWidth(item)
		if currW+w <= avail {
			parts = append(parts, item)
			currW += w
		} else {
			dots := "..."
			dotsW := 3
			for currW+dotsW > avail && len(parts) > 0 {
				last := parts[len(parts)-1]
				currW -= runewidth.StringWidth(last)
				parts = parts[:len(parts)-1]
			}
			if currW+dotsW <= avail {
				parts = append(parts, dots)
			}
			break
		}
	}
	if len(parts) == 0 {
		if len(files) == 0 && prefixW+1 <= maxW {
			return prefix
		}
		return ""
	}
	return prefix + strings.Join(parts, "") + " "
}

func cellCutChat(s string, width int) int {
	if width <= 0 || s == "" {
		return len(s)
	}
	used := 0
	for i := 0; i < len(s); {
		r, sz := utf8.DecodeRuneInString(s[i:])
		w := runewidth.RuneWidth(r)
		if used+w > width {
			return i
		}
		used += w
		i += sz
	}
	return len(s)
}

func (cp *AIChatPanel) getSession() *vtvibe.Session {
	if cp.src != nil && cp.src.Vfs != nil {
		if w, ok := cp.src.Vfs.(*aiVFSWrapper); ok {
			return w.Session()
		} else if a, ok := cp.src.Vfs.(*vtvibe.AIVFS); ok {
			return a.Session()
		}
	}
	return aiSession()
}

func (cp *AIChatPanel) copyLinkTarget(target string) {
	pf := panel.FindPanelsFrameAnyScreen()
	if pf == nil || cp.src == nil {
		return
	}

	var dstFSP *panel.FileSystemPanel
	for _, p := range pf.Panels {
		if fsp, ok := p.(*panel.FileSystemPanel); ok && fsp != cp.src {
			dstFSP = fsp
			break
		}
	}
	if dstFSP == nil || dstFSP.Vfs == nil {
		return
	}

	cleanTarget := strings.TrimPrefix(target, "ai://")
	if !strings.HasPrefix(cleanTarget, "/") {
		cleanTarget = "/" + cleanTarget
	}

	fileName := cp.src.Vfs.Base(cleanTarget)
	dstDir := dstFSP.Vfs.GetPath()
	dstPath := dstFSP.Vfs.Join(dstDir, fileName)

	pf.RunProgressTask(" Copy ", "Copying "+fileName+"...", false,
		func(ctx context.Context, update func(msg string, percent int)) error {
			srcFile, err := cp.src.Vfs.Open(ctx, cleanTarget)
			if err != nil {
				return err
			}
			defer srcFile.Close()

			dstFile, err := dstFSP.Vfs.Create(ctx, dstPath)
			if err != nil {
				return err
			}
			defer dstFile.Close()

			buf := make([]byte, 32768)
			total := srcFile.Size()
			var copied int64
			for {
				n, readErr := srcFile.Read(ctx, buf)
				if n > 0 {
					if _, writeErr := dstFile.Write(buf[:n]); writeErr != nil {
						return writeErr
					}
					copied += int64(n)
					if total > 0 {
						update("", int(copied*100/total))
					}
				}
				if readErr != nil {
					if readErr == io.EOF {
						break
					}
					return readErr
				}
			}
			return nil
		},
		func(err error) {
			if err != nil {
				vtui.ShowMessage(" Error ", "Copy failed:\n"+err.Error(), []string{"&Ok"})
			} else {
				dstFSP.ReadDirectory()
				toast.Show("Copied "+fileName+" to "+dstDir, 2*time.Second)
			}
		})
}
