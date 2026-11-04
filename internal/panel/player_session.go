package panel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/media"
	"github.com/unxed/vtui"
)

// playerSession is what plays: the audio output, the playlist, the track
// loaded, and the file panel's recordings when they are what plays. There is
// one for the process, and a player panel shows it. Closing the panel leaves
// the music playing in the background, and the next player opened shows it
// as it is (#2185). The session ends when no player shows it and nothing is
// loaded.
type playerSession struct {
	Engine  PlayerEngine
	root    *playlistItem // invisible root; children are the top level
	current *playlistItem // the track loaded in the engine, if any
	marquee int
	unmuted float64 // the volume M brings back

	// queue is the file panel's audio files when the current track came
	// from Enter in the panel rather than the playlist; queuePos is the
	// current one. Nil while the playlist drives playback.
	queue    []string
	queuePos int

	ffmpegWarned bool   // the install message is shown once per session
	status       string // one-line error/notice shown instead of the title

	views int // the player panels that show the session
	stop  chan struct{}
}

// activePlayerSession is the session the player panels show, or nil. Only the
// UI thread touches it.
var activePlayerSession *playerSession

func newPlayerSession(engine PlayerEngine) *playerSession {
	return &playerSession{
		Engine: engine,
		root:   &playlistItem{Folder: true, Expanded: true},
		stop:   make(chan struct{}),
	}
}

// openPlayerSession is the session for a player panel that opens: the one
// that plays already, or a new one with the saved playlist.
func openPlayerSession() *playerSession {
	if activePlayerSession == nil {
		s := newPlayerSession(NewPlayerEngine())
		s.loadPlaylist()
		go s.tick()
		activePlayerSession = s
	}
	activePlayerSession.views++
	return activePlayerSession
}

// leave is a player panel that closes. The session goes on while something is
// loaded: that is the music playing in the background.
func (s *playerSession) leave() {
	if s.views > 0 {
		s.views--
	}
	s.endIfIdle()
}

func (s *playerSession) endIfIdle() {
	if s.views == 0 && !s.Engine.IsLoaded() {
		s.end()
	}
}

func (s *playerSession) end() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	s.Engine.Close()
	if activePlayerSession == s {
		activePlayerSession = nil
	}
}

// MusicLoaded is whether the player has a track loaded, playing or paused,
// in a player panel or in the background.
func MusicLoaded() bool {
	s := activePlayerSession
	return s != nil && s.Engine.IsLoaded()
}

// MusicCommand does cmd to what the player plays, whether a player is open
// or not, and says whether it was a command that needs no player: play or
// pause, the next and the previous track, stop (the Music.* actions).
func MusicCommand(cmd PlayerCommand) bool {
	s := activePlayerSession
	if s == nil {
		return false
	}
	switch cmd {
	case PlayerPlayPause:
		s.playPause()
	case PlayerNext:
		s.playRelative(+1)
	case PlayerPrevious:
		s.playRelative(-1)
	case PlayerStop:
		s.stopPlaying()
	default:
		return false
	}
	return true
}

// EndPlayerSession stops whatever the player plays, in a panel or in the
// background, and forgets it.
func EndPlayerSession() {
	if s := activePlayerSession; s != nil {
		s.end()
	}
}

// tick repaints while something is playing so the clock, the marquee and
// the spectrum move, and advances to the next track when one ends. It
// never touches UI state itself; everything happens inside PostTask.
func (s *playerSession) tick() {
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			if !s.tickDue() {
				continue
			}
			vtui.FrameManager.PostTask(s.step)
		}
	}
}

// tickDue is whether a tick has anything to do: a track plays, or one has
// just ended. A paused track repainted the whole window six times a second
// although nothing on it moved.
func (s *playerSession) tickDue() bool {
	return s.Engine.IsPlaying() || s.Engine.Finished()
}

// step is a tick on the UI thread. It moves on from a track that ended, and
// repaints a player that is in view; nothing else on the screen moves with
// the music, so the music in the background or in an editor's screen
// repaints nothing.
func (s *playerSession) step() {
	if s.Engine.Finished() {
		if !s.playRelative(+1) {
			s.stopPlaying()
		}
		vtui.FrameManager.Redraw()
		return
	}
	if !s.Engine.IsPlaying() || !playerInView() {
		return
	}
	s.marquee++
	vtui.FrameManager.Redraw()
}

// ---- playlist file ----------------------------------------------------

func playlistFile() string {
	return filepath.Join(config.GetF4ConfigDir(), "playlist.json")
}

func (s *playerSession) loadPlaylist() {
	data, err := os.ReadFile(playlistFile())
	if err != nil {
		return
	}
	_ = unmarshalPlaylist(data, s.root)
}

func (s *playerSession) savePlaylist() {
	data, err := marshalPlaylist(s.root)
	if err != nil {
		return
	}
	_ = os.WriteFile(playlistFile(), data, 0o600)
}

// ---- playback ---------------------------------------------------------

func (s *playerSession) playItem(it *playlistItem) bool {
	if it == nil || it.Folder {
		return false
	}
	// A playlist track takes over from the file panel queue.
	if it.parent != nil {
		s.queue = nil
	}
	s.status = ""
	if err := s.Engine.Load(it.Path); err != nil {
		s.status = err.Error()
		s.current = nil
		if errors.Is(err, media.ErrNeedFFmpeg) {
			s.status = fmt.Sprintf(i18n.Msg("Player.NeedFFmpeg"), filepath.Ext(it.Path))
			if !s.ffmpegWarned {
				s.ffmpegWarned = true
				vtui.ShowMessage(i18n.Msg("Player.Title"), media.ToolFFmpeg.MissingMessage(), []string{i18n.Msg("vtui.Ok")})
			}
		}
		return false
	}
	s.current = it
	s.marquee = 0
	s.Engine.Play()
	return true
}

// stopPlaying unloads the track, and ends a session no player shows any more.
func (s *playerSession) stopPlaying() {
	s.Engine.Stop()
	s.current = nil
	s.queue = nil
	s.endIfIdle()
}

// PlayFile plays one file that is not in the playlist — the one under the
// cursor of the file panel — and remembers its neighbours so that playback
// can go on to the next recording, and |< >| move within the folder. The
// list is the panel's audio files in panel order; pos is the one to play.
func (s *playerSession) PlayFile(files []string, pos int) bool {
	if pos < 0 || pos >= len(files) {
		return false
	}
	it := &playlistItem{Name: trackDisplayName(files[pos]), Path: files[pos]}
	if !s.playItem(it) {
		return false
	}
	s.queue = append([]string(nil), files...)
	s.queuePos = pos
	return true
}

// AutoPlayFile follows the file-panel cursor without restarting a track when
// an arrow key merely hits the edge of the list. It follows the cursor only
// while the player plays the panel's files, or nothing: a track of the
// playlist stays, or Tab to the player and back and the next arrow in the
// file panel swapped it for the file under the cursor (#2185).
func (s *playerSession) AutoPlayFile(files []string, pos int) bool {
	if s.Engine == nil || pos < 0 || pos >= len(files) {
		return false
	}
	if s.Engine.IsLoaded() && (s.queue == nil || s.current != nil && s.current.Path == files[pos]) {
		return false
	}
	return s.PlayFile(files, pos)
}

// StopIfPlaying stops playback when the current track is one of paths.
// The delete action calls it before removing files: an open file cannot be
// deleted on Windows, and a player that carries on reading a file that is
// gone is not what anybody expects on the others. The queue forgets the
// files too so the player does not step onto them later.
func (s *playerSession) StopIfPlaying(paths []string) {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[filepath.Clean(p)] = true
	}
	if s.current != nil && set[filepath.Clean(s.current.Path)] {
		s.Engine.Stop()
		s.current = nil
	}
	if s.queue == nil {
		return
	}
	kept := s.queue[:0]
	pos := s.queuePos
	for i, p := range s.queue {
		if set[filepath.Clean(p)] {
			if i < s.queuePos {
				pos--
			}
			continue
		}
		kept = append(kept, p)
	}
	s.queue = kept
	s.queuePos = max(-1, min(pos, len(kept)-1))
}

// playQueueRelative steps through the file panel's files. Files that have
// gone since the list was taken (deleted from the panel meanwhile) are
// skipped.
func (s *playerSession) playQueueRelative(delta int) bool {
	for i := s.queuePos + delta; i >= 0 && i < len(s.queue); i += delta {
		if _, err := os.Stat(s.queue[i]); err != nil {
			continue
		}
		if s.PlayFile(s.queue, i) {
			return true
		}
	}
	return false
}

// playRelative plays the track delta positions away from the current one
// in play order. With nothing current it starts from the ends.
func (s *playerSession) playRelative(delta int) bool {
	if s.queue != nil {
		return s.playQueueRelative(delta)
	}
	list := s.root.tracks(nil)
	if len(list) == 0 {
		return false
	}
	idx := -1
	for i, t := range list {
		if t == s.current {
			idx = i
			break
		}
	}
	if idx < 0 {
		if delta > 0 {
			idx = -1
		} else {
			idx = len(list)
		}
	}
	idx += delta
	if idx < 0 || idx >= len(list) {
		return false
	}
	return s.playItem(list[idx])
}

// playPause pauses or goes on playing, or with nothing loaded starts the
// playlist, or the file panel's recordings it played last.
func (s *playerSession) playPause() {
	if s.Engine.IsLoaded() {
		s.Engine.TogglePause()
		return
	}
	s.playRelative(+1)
}

func (s *playerSession) adjustVolume(delta float64) {
	s.Engine.SetVolume(s.Engine.Volume() + delta)
}

// SeekBy moves the current track by delta. It is also used by the file-panel
// shortcuts, where the player is open but does not have keyboard focus.
func (s *playerSession) SeekBy(delta time.Duration) bool {
	if s.Engine == nil || !s.Engine.IsLoaded() {
		return false
	}
	s.Engine.Seek(delta)
	return true
}

// JumpToTenth moves the current track to tenth tenths of its length, as the
// digits do on YouTube: 0 to the start, 5 to the middle.
func (s *playerSession) JumpToTenth(tenth int) bool {
	if s.Engine == nil || !s.Engine.IsLoaded() || s.Engine.Duration() <= 0 || tenth < 0 || tenth > 9 {
		return false
	}
	target := s.Engine.Duration() * time.Duration(tenth) / 10
	return s.SeekBy(target - s.Engine.Position())
}

// toggleMute turns the sound off, or back on to the volume it had.
func (s *playerSession) toggleMute() {
	if v := s.Engine.Volume(); v > 0 {
		s.unmuted = v
		s.Engine.SetVolume(0)
		return
	}
	v := s.unmuted
	if v <= 0 {
		v = 0.8
	}
	s.Engine.SetVolume(v)
}
