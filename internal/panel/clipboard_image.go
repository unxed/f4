package panel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/media"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// ValidateClipboardImageName rejects paths and nonportable filename characters.
func ValidateClipboardImageName(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || strings.ContainsAny(name, `/\:<>"|?*`) {
		return fmt.Errorf("invalid clipboard image filename %q", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("control characters are not allowed in filenames")
		}
	}
	base, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return fmt.Errorf("reserved clipboard image filename %q", name)
	}
	return nil
}

type clipboardImageName struct {
	before, after string
	width         int
	foldCase      bool
}

func (n clipboardImageName) next(ctx context.Context, fs vfs.VFS, dir string) (*big.Int, error) {
	var entries []vfs.VFSItem
	if err := fs.ReadDir(ctx, dir, func(items []vfs.VFSItem) { entries = append(entries, items...) }); err != nil {
		return nil, err
	}
	// Query one alternate spelling absent from the listing. This observes the
	// destination directory's actual semantics, including case-sensitive NTFS
	// directories and case-insensitive APFS, without relying on the host OS.
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		names[entry.Name] = true
	}
	foldCase := n.foldCase
	for _, entry := range entries {
		alternate := []byte(entry.Name)
		changed := false
		for i, b := range alternate {
			if b >= 'a' && b <= 'z' {
				alternate[i] = b - 'a' + 'A'
				changed = true
				break
			}
			if b >= 'A' && b <= 'Z' {
				alternate[i] = b - 'A' + 'a'
				changed = true
				break
			}
		}
		if !changed || names[string(alternate)] {
			continue
		}
		_, err := fs.Stat(ctx, fs.Join(dir, string(alternate)))
		if err == nil {
			foldCase = true
			break
		}
		if errors.Is(err, os.ErrNotExist) {
			foldCase = false
			break
		}
		return nil, err
	}
	maximum := new(big.Int)
	before, after := n.before, n.after
	if foldCase {
		before, after = strings.ToLower(before), strings.ToLower(after)
	}
	for _, item := range entries {
		name := item.Name
		if foldCase {
			name = strings.ToLower(name)
		}
		if !strings.HasPrefix(name, before) || !strings.HasSuffix(name, after) || len(name) <= len(before)+len(after) {
			continue
		}
		digits := name[len(before) : len(name)-len(after)]
		if strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			continue
		}
		value, ok := new(big.Int).SetString(digits, 10)
		if ok && value.Cmp(maximum) > 0 {
			maximum.Set(value)
		}
	}
	return maximum.Add(maximum, big.NewInt(1)), nil
}

func (n clipboardImageName) filename(sequence *big.Int) string {
	digits := sequence.String()
	return n.before + strings.Repeat("0", max(0, n.width-len(digits))) + digits + n.after
}

// saveClipboardImage stages complete encoded bytes before atomic publication.
func saveClipboardImage(ctx context.Context, fs vfs.VFS, dir string, img image.Image, cfg config.F4Config, naming clipboardImageName) (name string, result error) {
	caps := fs.GetCapabilities()
	if !caps.HasWrite {
		return "", fmt.Errorf("%s", i18n.Msg("ClipboardImage.ReadOnly"))
	}
	if !caps.HasAtomicNoReplaceRename {
		return "", fmt.Errorf("%s", i18n.Msg("ClipboardImage.Unsupported"))
	}
	var encoded bytes.Buffer
	if err := media.EncodeClipboardImage(&encoded, img, cfg.ClipboardImageFormat, cfg.ClipboardImagePNGCompression, cfg.ClipboardImageJPEGQuality); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sequence, err := naming.next(ctx, fs, dir)
	if err != nil {
		return "", err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	stage := fs.Join(dir, ".f4-clipboard-"+hex.EncodeToString(nonce[:])+".tmp")
	if _, err := fs.Stat(ctx, stage); err == nil {
		return "", vfs.ErrDestinationExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	writeCtx := vfs.WithDestinationOverwrite(ctx, false)
	writer, err := fs.Create(writeCtx, stage)
	if err != nil {
		return "", err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if stage != "" {
			if err := fs.Remove(cleanupCtx, stage); err != nil && !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, err)
			}
		}
	}()
	if _, err := encoded.WriteTo(writer); err != nil {
		if abort, ok := writer.(vfs.AbortableWriter); ok {
			return "", errors.Join(err, abort.Abort(), writer.Close())
		}
		return "", errors.Join(err, writer.Close())
	}
	if err := ctx.Err(); err != nil {
		if abort, ok := writer.(vfs.AbortableWriter); ok {
			return "", errors.Join(err, abort.Abort(), writer.Close())
		}
		return "", errors.Join(err, writer.Close())
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name = naming.filename(sequence)
		if err := ValidateClipboardImageName(name); err != nil {
			return "", err
		}
		err = fs.Rename(writeCtx, stage, fs.Join(dir, name))
		if !errors.Is(err, vfs.ErrDestinationExists) && !errors.Is(err, os.ErrExist) {
			if err != nil {
				return "", err
			}
			// Publication consumed the staging path. Do not ask a provider to
			// remove a missing object (many remote providers report an error).
			stage = ""
			return name, nil
		}
		// Other writers may have reserved several larger names while we encoded.
		// Rescan the complete directory rather than merely filling the next gap.
		next, err := naming.next(ctx, fs, dir)
		if err != nil {
			return "", err
		}
		sequence.Add(sequence, big.NewInt(1))
		if next.Cmp(sequence) > 0 {
			sequence = next
		}
	}
}

var readPanelClipboard = terminal.ReadClipboardContents

// ActionPasteClipboard owns panel paste, including the existing text fallback.
func ActionPasteClipboard(pf *PanelsFrame) bool {
	if pf == nil || !pf.ShowPanels || vtui.FrameManager == nil {
		return false
	}
	active := pf.GetActivePanel()
	if active == nil || active.Vfs == nil {
		return false
	}
	capture := captureApplyCommandPanel(active, nil)
	capture.Vfs = capture.PanelVFS.Clone()
	var released sync.Once
	release := func() {
		released.Do(func() {
			if capture.Vfs != nil && !fileops.SameVFSInstance(capture.Vfs, capture.PanelVFS) {
				_ = capture.Vfs.Close()
			}
		})
	}
	passive := captureApplyCommandPanel(pf.GetInactivePanel(), nil)
	cfg := config.App
	ctx := cmdline.ApplyCommandContext{Active: capture.Snapshot, Passive: passive.Snapshot}
	if pf.ActiveIdx == 1 {
		ctx.ActiveSide = cmdline.ApplyCommandRightSide
	}
	vtui.DebugLog("ClipboardImage: reading clipboard for %q", capture.Dir)
	read := readPanelClipboard
	done := terminal.TrackClipboardRead()
	vtui.RunAsync(func(task *vtui.TaskContext) {
		defer done()
		defer task.Cancel()
		contents, err := read(task.Context)
		task.RunOnUI(func() {
			if pf.Closed {
				release()
				return
			}
			if pf.pasteFileClipboardContents(contents, err) {
				release()
				return
			}
			if err != nil {
				release()
				showClipboardImageError(pf, err)
				return
			}
			pasteText := func() { release(); pf.CmdLine.PasteText(contents.Text) }
			if contents.Image == nil {
				pasteText()
				return
			}
			save := func() { prepareClipboardImagePaste(pf, capture, ctx, cfg, contents.Image, release) }
			if contents.Text != "" {
				showClipboardPasteChoice(pf, contents.Text, save, pasteText, release)
			} else {
				save()
			}
		})
	})
	return true
}

func showClipboardImageError(anchor vtui.Frame, err error) {
	vtui.DebugLog("ClipboardImage: %v", err)
	vtui.ShowMessageOn(anchor, i18n.Msg("ClipboardImage.Title"), fmt.Sprintf(i18n.Msg("ClipboardImage.FailureFmt"), err), []string{i18n.Msg("vtui.Ok")})
}

func prepareClipboardImagePaste(pf *PanelsFrame, capture ApplyPanelCapture, ctx cmdline.ApplyCommandContext, cfg config.F4Config, img image.Image, release func()) {
	compiled, err := cmdline.CompileFilenameTemplate(cfg.ClipboardImageTemplate)
	if err != nil {
		release()
		showClipboardImageError(pf, err)
		return
	}
	if len(cfg.ClipboardImageDigitFormat) < 1 || len(cfg.ClipboardImageDigitFormat) > 20 || strings.Trim(cfg.ClipboardImageDigitFormat, "0") != "" {
		release()
		showClipboardImageError(pf, fmt.Errorf("%s", i18n.Msg("ClipboardImage.InvalidDigits")))
		return
	}
	prompts, err := compiled.ResolvePromptsWithPrefix(ctx, cfg.ClipboardImagePrefix)
	if err != nil {
		release()
		showClipboardImageError(pf, err)
		return
	}
	accept := func(values cmdline.ApplyCommandPromptValues) {
		before, after, err := compiled.ExpandParts(ctx, values, cfg.ClipboardImagePrefix)
		if err != nil {
			release()
			showClipboardImageError(pf, err)
			return
		}
		ext := ".png"
		if cfg.ClipboardImageFormat == "jpeg" {
			ext = ".jpg"
		}
		naming := clipboardImageName{before: before, after: after + ext, width: len(cfg.ClipboardImageDigitFormat), foldCase: ctx.Active.PathStyle == cmdline.ApplyCommandPathStyleWindows}
		if err := ValidateClipboardImageName(naming.filename(big.NewInt(1))); err != nil {
			release()
			showClipboardImageError(pf, err)
			return
		}
		fs := capture.Vfs
		if fs == nil || fileops.SameVFSInstance(fs, capture.PanelVFS) {
			release()
			showClipboardImageError(pf, fmt.Errorf("%s", i18n.Msg("ClipboardImage.Unsupported")))
			return
		}
		var name string
		pf.RunProgressTask(i18n.Msg("ClipboardImage.Title"), i18n.Msg("ClipboardImage.Saving"), false, func(worker context.Context, update func(string, int)) error {
			defer release()
			var err error
			name, err = saveClipboardImage(worker, fs, capture.Dir, img, cfg, naming)
			return err
		}, func(err error) {
			if name != "" {
				finishClipboardImagePaste(pf, capture, name)
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				release()
				showClipboardImageError(pf, err)
			}
		})
	}
	if len(prompts) == 0 {
		accept(cmdline.ApplyCommandPromptValues{})
	} else {
		showApplyCommandPrompts(pf, prompts, accept, release)
	}
}

func finishClipboardImagePaste(pf *PanelsFrame, capture ApplyPanelCapture, name string) {
	if pf.Closed {
		return
	}
	if fileops.SameVFSInstance(capture.Panel.Vfs, capture.PanelVFS) && capture.Panel.Vfs.GetPath() == capture.Dir {
		capture.Panel.ExitFastFind()
		capture.Panel.clipboardImageRevealName = name
		capture.Panel.clipboardImageRevealPath = capture.Dir
		capture.Panel.clipboardImageRevealVFS = capture.PanelVFS
		// Some callers mark entries directly. Carry those marks into the
		// persistent map the directory loader uses for replacement entries.
		if capture.Panel.SelectedItems == nil {
			capture.Panel.SelectedItems = make(map[string]bool)
		}
		for _, entry := range capture.Panel.Entries {
			if entry.Name != ".." {
				if entry.Selected {
					capture.Panel.SelectedItems[entry.Name] = true
				} else {
					delete(capture.Panel.SelectedItems, entry.Name)
				}
			}
		}
		capture.Panel.PendingSelection = name
		capture.Panel.ReadDirectory()
	}
	toast.Show(fmt.Sprintf(i18n.Msg("ClipboardImage.SavedFmt"), name), 3*time.Second)
}

// A saved image must be focusable even when hidden-file display is disabled.
// Reveal only that entry and only while this panel shows the saved directory.
func (fp *FileSystemPanel) clipboardImageRevealFor(fs vfs.VFS, dir string) string {
	if fp.clipboardImageRevealPath == dir && fileops.SameVFSInstance(fp.clipboardImageRevealVFS, fs) {
		return fp.clipboardImageRevealName
	}
	return ""
}

func showClipboardPasteChoice(pf *PanelsFrame, text string, save, paste func(), canceled ...func()) {
	dlg := vtui.NewCenteredDialog(72, 18, i18n.Msg("ClipboardImage.Title"))
	dlg.ShowClose = true
	dlg.SetHelp("ClipboardImages")
	chosen := false
	cancel := func() {
		if len(canceled) > 0 && canceled[0] != nil {
			canceled[0]()
		}
	}
	dlg.OnResult = func(int) {
		if !chosen {
			cancel()
		}
	}
	explanation := vtui.NewText(dlg.X1+2, dlg.Y1+2, strings.Join(vtui.WrapText(i18n.Msg("ClipboardImage.Both"), 68), "\n"), 0)
	explanation.SetPosition(dlg.X1+2, dlg.Y1+2, dlg.X2-2, dlg.Y1+3)
	dlg.AddItem(explanation)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, vtui.WrapText(line, 66)...)
	}
	preview := vtui.NewListBox(dlg.X1+2, dlg.Y1+4, 68, 9, wrapped)
	dlg.AddItem(preview)
	buttons := vtui.NewHBoxLayout(dlg.X1+2, dlg.Y2-2, 68, 1)
	for i, spec := range []struct {
		label string
		fn    func()
	}{{"ClipboardImage.SaveImage", save}, {"ClipboardImage.PasteText", paste}, {"vtui.Cancel", cancel}} {
		button := vtui.NewButton(0, 0, i18n.Msg(spec.label))
		button.IsDefault = i == 0
		button.OnClick = func() { chosen = true; dlg.Close(); spec.fn() }
		dlg.AddItem(button)
		buttons.Add(button, vtui.Margins{Right: 2}, vtui.AlignCenter)
	}
	buttons.Apply()
	vtui.FrameManager.PushToFrameScreen(pf, dlg)
}
