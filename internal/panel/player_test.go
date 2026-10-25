package panel

import (
	"bytes"
	"errors"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/media"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func newTestPlayerPanel() *PlayerPanel {
	return &PlayerPanel{
		playerSession: newPlayerSession(media.NewAudioEngine()),
		cursor:        playerCursorButtons,
	}
}

func TestPlaylistTreeOperations(t *testing.T) {
	pp := newTestPlayerPanel()
	f := &playlistItem{Name: "F", Folder: true, Expanded: true}
	a := &playlistItem{Name: "a", Path: "/a.mp3"}
	b := &playlistItem{Name: "b", Path: "/b.mp3"}
	pp.root.insertChild(-1, f)
	pp.root.insertChild(-1, a)
	pp.root.insertChild(-1, b)

	// Play order is depth first and independent of expansion.
	f.insertChild(-1, &playlistItem{Name: "c", Path: "/c.mp3"})
	f.Expanded = false
	got := pp.root.tracks(nil)
	if len(got) != 3 || got[0].Name != "c" || got[2].Name != "b" {
		t.Fatalf("tracks order = %v", names(got))
	}

	// Rows respect expansion.
	pp.rebuildRows()
	if len(pp.rows) != 3 {
		t.Fatalf("collapsed rows = %d, want 3", len(pp.rows))
	}
	f.Expanded = true
	pp.rebuildRows()
	if len(pp.rows) != 4 || pp.rows[1].depth != 1 {
		t.Fatalf("expanded rows = %d depth1=%d", len(pp.rows), pp.rows[1].depth)
	}

	// Ctrl+Down swaps siblings.
	pp.moveItem(a, +1)
	if pp.root.Children[2] != a || pp.root.Children[1] != b {
		t.Fatalf("moveItem order = %v", names(pp.root.Children))
	}
	// Ctrl+Right drops into the folder above; Ctrl+Left lifts it back out
	// right after that folder.
	pp.moveInto(b)
	if b.parent != f || len(f.Children) != 2 {
		t.Fatalf("moveInto: parent=%v children=%d", b.parent, len(f.Children))
	}
	pp.moveOut(b)
	if b.parent != pp.root || pp.root.Children[1] != b {
		t.Fatalf("moveOut order = %v", names(pp.root.Children))
	}
	// Cursor follows the moved item.
	if pp.cursor < 0 || pp.rows[pp.cursor].item != b {
		t.Fatalf("cursor did not follow item: %d", pp.cursor)
	}
}

func names(items []*playlistItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

func TestPlaylistPersistenceRoundTrip(t *testing.T) {
	pp := newTestPlayerPanel()
	f := &playlistItem{Name: "F", Folder: true, Expanded: true}
	f.insertChild(-1, &playlistItem{Name: "x", Path: "/x.mp3"})
	pp.root.insertChild(-1, f)
	saved, err := marshalPlaylist(pp.root)
	if err != nil {
		t.Fatal(err)
	}
	pp2 := newTestPlayerPanel()
	if err := unmarshalPlaylist(saved, pp2.root); err != nil {
		t.Fatal(err)
	}
	if len(pp2.root.Children) != 1 || len(pp2.root.Children[0].Children) != 1 {
		t.Fatalf("round trip lost items")
	}
	if pp2.root.Children[0].Children[0].parent != pp2.root.Children[0] {
		t.Fatalf("parent links not restored")
	}
}

func TestAddPathsWalksDirectoriesAndSkipsOthers(t *testing.T) {
	dir := t.TempDir()
	must := func(p string) {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "album", "cd2"), 0o755); err != nil {
		t.Fatal(err)
	}
	must(filepath.Join(dir, "album", "1.mp3"))
	must(filepath.Join(dir, "album", "cover.jpg"))
	must(filepath.Join(dir, "album", "cd2", "2.MP3"))
	must(filepath.Join(dir, "loose.mp3"))
	must(filepath.Join(dir, "notes.txt"))

	pp := newTestPlayerPanel()
	// Playlist saving writes to the config dir; point it at the temp dir.
	// Initialize the once-only detector before saving and restoring the test
	// override, otherwise a shuffled run can leave an empty cached path behind.
	_ = config.GetF4ConfigDir()
	oldCfg := config.CachedF4ConfigDir
	config.CachedF4ConfigDir = dir
	config.ConfigDirOnce.Do(func() {})
	defer func() { config.CachedF4ConfigDir = oldCfg }()

	n := pp.AddPaths([]string{filepath.Join(dir, "album"), filepath.Join(dir, "loose.mp3"), filepath.Join(dir, "notes.txt")})
	if n != 3 {
		t.Fatalf("added %d, want 3", n)
	}
	if len(pp.root.Children) != 2 || !pp.root.Children[0].Folder {
		t.Fatalf("top level = %v", names(pp.root.Children))
	}
	album := pp.root.Children[0]
	if len(album.Children) != 2 || !album.Children[1].Folder || album.Children[1].Name != "cd2" {
		t.Fatalf("album children = %v", names(album.Children))
	}
}

func TestLinearResamplerKeepsDCAndLength(t *testing.T) {
	// 100 frames of a constant stereo value at 48 kHz → ~92 frames at 44.1.
	const frames = 100
	src := make([]byte, frames*media.AudioBytesPerFrame)
	for i := 0; i < frames; i++ {
		src[i*4], src[i*4+1] = 0x00, 0x10   // 4096
		src[i*4+2], src[i*4+3] = 0x00, 0xF0 // -4096
	}
	r := media.NewLinearResampler(bytes.NewReader(src), 48000, 44100)
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	got := len(out) / media.AudioBytesPerFrame
	if got < 90 || got > 93 {
		t.Fatalf("output frames = %d, want ≈92", got)
	}
	for i := 0; i < got; i++ {
		l := int16(uint16(out[i*4]) | uint16(out[i*4+1])<<8)
		rr := int16(uint16(out[i*4+2]) | uint16(out[i*4+3])<<8)
		if l != 4096 || rr != -4096 {
			t.Fatalf("frame %d = %d/%d", i, l, rr)
		}
	}
}

func TestLinearResamplerPropagatesSourceErrors(t *testing.T) {
	wantErr := errors.New("decoder failure")
	r := media.NewLinearResampler(&failingPCMReader{err: wantErr}, 48000, 44100)
	buf := make([]byte, 2*media.AudioBytesPerFrame)
	n, err := r.Read(buf)
	if n != media.AudioBytesPerFrame || !errors.Is(err, wantErr) {
		t.Fatalf("Read() = (%d, %v), want (%d, %v)", n, err, media.AudioBytesPerFrame, wantErr)
	}
}

type failingPCMReader struct {
	err  error
	done bool
}

func (r *failingPCMReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	copy(p, []byte{0, 0x10, 0, 0x10})
	return media.AudioBytesPerFrame, nil
}

func TestPCMTapSpectrumSilenceIsZero(t *testing.T) {
	tap := media.NewPCMTap(bytes.NewReader(make([]byte, 4096)), 44100)
	if _, err := io.ReadAll(tap); err != nil {
		t.Fatal(err)
	}
	if !tap.Eof() || tap.BytesRead() != 4096 {
		t.Fatalf("eof=%v read=%d", tap.Eof(), tap.BytesRead())
	}
	for i, v := range tap.Spectrum(8) {
		if v != 0 {
			t.Fatalf("band %d = %v on silence", i, v)
		}
	}
}

func TestMarqueeSliceWraps(t *testing.T) {
	if got := marqueeSlice("abcdef", 4, 4); got != "efab" {
		t.Fatalf("got %q", got)
	}
	if got := marqueeSlice("", 4, 4); got != "" {
		t.Fatalf("empty: %q", got)
	}
}

func TestMP3FirstFrameIsMonoSkipsID3(t *testing.T) {
	dir := t.TempDir()
	// ID3v2 header claiming a 5-byte tag, then junk, then a MPEG1 Layer III
	// header with channel mode 11 (mono).
	mono := append([]byte("ID3\x03\x00\x00\x00\x00\x00\x05"), 0xFF, 0xFF, 0xFF, 0xFF, 0xFF)
	mono = append(mono, 0x00, 0xFF, 0xFB, 0x90, 0xC0)
	stereo := append([]byte{}, mono...)
	stereo[len(stereo)-1] = 0x00
	mp := filepath.Join(dir, "m.mp3")
	sp := filepath.Join(dir, "s.mp3")
	if err := os.WriteFile(mp, mono, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sp, stereo, 0o600); err != nil {
		t.Fatal(err)
	}
	if !media.Mp3FirstFrameIsMono(mp) {
		t.Fatalf("mono header not detected")
	}
	if media.Mp3FirstFrameIsMono(sp) {
		t.Fatalf("stereo reported as mono")
	}
}
