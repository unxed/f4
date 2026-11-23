package panel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/internal/media"
	"github.com/unxed/vtui"
)

// tickEngine is a PlayerEngine that plays, is paused or is at the end of its
// track as the test says.
type tickEngine struct {
	path                      string
	loaded, playing, finished bool
}

func (e *tickEngine) Load(path string) error {
	*e = tickEngine{path: path, loaded: true}
	return nil
}
func (e *tickEngine) Close()                       { e.Stop() }
func (e *tickEngine) Play()                        { e.playing = e.loaded }
func (e *tickEngine) TogglePause() bool            { e.playing = !e.playing && e.loaded; return e.playing }
func (e *tickEngine) Stop()                        { *e = tickEngine{} }
func (e *tickEngine) Seek(time.Duration) bool      { return e.loaded }
func (e *tickEngine) IsPlaying() bool              { return e.playing }
func (e *tickEngine) IsLoaded() bool               { return e.loaded }
func (e *tickEngine) Finished() bool               { return e.finished }
func (e *tickEngine) Volume() float64              { return 0.8 }
func (e *tickEngine) SetVolume(float64)            {}
func (e *tickEngine) Position() time.Duration      { return 0 }
func (e *tickEngine) Duration() time.Duration      { return time.Minute }
func (e *tickEngine) Info() media.AudioTrackInfo   { return media.AudioTrackInfo{} }
func (e *tickEngine) Spectrum(bands int) []float64 { return make([]float64, bands) }

// tickFixture is a session playing a.mp3 of a two-track playlist, on panels
// that show a player of it when shown is true.
func tickFixture(t *testing.T, shown bool) (*playerSession, *tickEngine, *PanelsFrame) {
	t.Helper()
	pf, _ := livePanels(t)
	eng := &tickEngine{}
	s := newPlayerSession(eng)
	t.Cleanup(s.end)
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.mp3")
	bPath := filepath.Join(dir, "b.mp3")
	for _, path := range []string{aPath, bPath} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.root.insertChild(-1, &playlistItem{Name: "a", Path: aPath})
	s.root.insertChild(-1, &playlistItem{Name: "b", Path: bPath})
	if !s.playRelative(+1) || eng.path != aPath || !eng.playing {
		t.Fatalf("the first track did not start: %+v", *eng)
	}
	if shown {
		pf.AltPanels[1-pf.ActiveIdx] = &PlayerPanel{playerSession: s}
	}
	vtui.FrameManager.RedrawChan = make(chan struct{}, 1)
	return s, eng, pf
}

func redrawRequested() bool {
	select {
	case <-vtui.FrameManager.RedrawChan:
		return true
	default:
		return false
	}
}

func TestMusicCommandRoutesTransportControlsToActiveSession(t *testing.T) {
	s, eng, _ := tickFixture(t, false)
	previous := activePlayerSession
	activePlayerSession = s
	t.Cleanup(func() { activePlayerSession = previous })

	if !MusicCommand(PlayerPlayPause) || eng.playing {
		t.Fatalf("play/pause command: playing=%v", eng.playing)
	}
	if !MusicCommand(PlayerNext) || eng.path != s.root.Children[1].Path || !eng.playing {
		t.Fatalf("next command: engine=%+v", *eng)
	}
	if !MusicCommand(PlayerPrevious) || eng.path != s.root.Children[0].Path || !eng.playing {
		t.Fatalf("previous command: engine=%+v", *eng)
	}
	if !MusicCommand(PlayerStop) || eng.IsLoaded() {
		t.Fatalf("stop command: engine=%+v", *eng)
	}
	if MusicCommand(PlayerVolumeUp) {
		t.Fatal("unsupported system command was accepted")
	}
}

// A paused track repainted the whole window six times a second, a couple of
// per cent of a core for a screen on which nothing moved.
func TestPlayerTickIdlesWhilePaused(t *testing.T) {
	s, eng, _ := tickFixture(t, true)
	if !s.tickDue() {
		t.Fatal("a playing track must tick: its clock and spectrum move")
	}
	eng.TogglePause()
	if s.tickDue() {
		t.Fatal("a paused track ticks")
	}
}

func TestPlayerTickRepaintsAPlayerInView(t *testing.T) {
	s, _, _ := tickFixture(t, true)
	marquee := s.marquee
	s.step()
	if redrawn := redrawRequested(); !redrawn || s.marquee != marquee+1 {
		t.Fatalf("a tick of a player in view: redraw=%v marquee %d→%d; want a redraw and the marquee moved", redrawn, marquee, s.marquee)
	}

	// A dialog over the panels leaves the player in view.
	vtui.FrameManager.Push(vtui.NewDialog(10, 5, 40, 10, "Dialog"))
	s.step()
	if !redrawRequested() {
		t.Error("a player under a dialog stopped moving")
	}
}

// Nothing on the screen moves with music in the background, so it repaints
// nothing.
func TestPlayerTickRepaintsNothingForMusicInTheBackground(t *testing.T) {
	s, _, _ := tickFixture(t, false)
	s.step()
	if redrawRequested() {
		t.Error("music in the background repainted the window")
	}
}

// A track that ended is followed by the next one, in view or not, and the
// last one stops the music; both show on the screen.
func TestPlayerTickMovesOnFromATrackThatEnded(t *testing.T) {
	s, eng, _ := tickFixture(t, false)
	eng.playing, eng.finished = false, true
	if !s.tickDue() {
		t.Fatal("a track that ended does not tick, so the next one never starts")
	}
	s.step()
	if redrawn := redrawRequested(); filepath.Base(eng.path) != "b.mp3" || !eng.playing || !redrawn {
		t.Fatalf("after a's end: engine %+v, redraw=%v; want b playing and a redraw", *eng, redrawn)
	}
	eng.playing, eng.finished = false, true
	s.step()
	if eng.loaded || !redrawRequested() {
		t.Fatalf("after the last track's end: engine %+v; want it stopped and a redraw", *eng)
	}
}

func TestPlayerNextSkipsPlaylistFilesThatWereRemoved(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "current.mp3")
	nextPath := filepath.Join(dir, "next.mp3")
	if err := os.WriteFile(currentPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nextPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	s := newPlayerSession(&tickEngine{})
	t.Cleanup(s.end)
	current := &playlistItem{Name: "current", Path: currentPath}
	s.root.insertChild(-1, current)
	s.root.insertChild(-1, &playlistItem{Name: "removed", Path: filepath.Join(dir, "removed.mp3")})
	next := &playlistItem{Name: "next", Path: nextPath}
	s.root.insertChild(-1, next)
	if !s.playItem(current) {
		t.Fatal("could not start the current track")
	}

	if !s.playRelative(+1) || s.current != next || s.Engine.(*tickEngine).path != nextPath {
		t.Fatalf("Next did not skip the removed file: current=%v engine=%+v", s.current, *s.Engine.(*tickEngine))
	}
}
