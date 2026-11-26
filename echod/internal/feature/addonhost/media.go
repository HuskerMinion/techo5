package addonhost

import (
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// An add-on's media: played on the device's own video player and media player, acted on only while it
// is still what plays, and followed for the add-on (media.state) only while it is the add-on's own.
// Whatever else plays, and what replaced the add-on's, is never said: an add-on is told that its
// media was replaced, not by what.

// players is what an add-on's media plays on: the device's (devicePlayers), or a test's.
type players interface {
	playVideo(video.Request) (uint64, error)
	videoNow() video.State
	pauseVideo()
	resumeVideo()
	stopVideo(id uint64)

	playAudio(url string, t *media.Title)
	// audioNamed is what the track started with t is doing while it is loaded (media.Stream.Named).
	audioNamed(t *media.Title) (paused, heard bool, at time.Duration, ok bool)
	pauseAudio()
	resumeAudio()
	stopAudio()

	// listen wires the players' hooks: a named track's end, the room's music changing, the video
	// changing. Each runs on whatever changed it, and must not block.
	listen(audioEnded func(media.NamedEnd), audioChanged, videoChanged func())
}

// devicePlayers is the device's own players.
type devicePlayers struct{}

func (devicePlayers) playVideo(r video.Request) (uint64, error) { return video.Play(r) }
func (devicePlayers) videoNow() video.State                     { return video.Current() }
func (devicePlayers) pauseVideo()                               { video.Pause() }
func (devicePlayers) resumeVideo()                              { video.Resume() }
func (devicePlayers) stopVideo(id uint64)                       { video.StopID(id) }
func (devicePlayers) playAudio(url string, t *media.Title)      { media.Get().PlayNamed(url, t) }
func (devicePlayers) audioNamed(t *media.Title) (bool, bool, time.Duration, bool) {
	return media.Get().Named(t)
}
func (devicePlayers) pauseAudio()  { media.Get().Pause() }
func (devicePlayers) resumeAudio() { media.Get().Resume() }
func (devicePlayers) stopAudio()   { media.Get().Stop() }
func (devicePlayers) listen(ended func(media.NamedEnd), audio, vid func()) {
	p := media.Get()
	p.OnNamedEnd.Listen(ended)
	p.OnNowPlaying.Listen(func(struct{}) { audio() })
	video.Listen(vid)
}

// played is one media.play of an add-on's.
type played struct {
	media string // its id, as media.state says it
	kind  string
	vid   uint64       // a video's id in the video player
	title *media.Title // an audio track's, the same pointer the media player holds while it is loaded

	// last is what it was doing when last looked at, whose position its end says; final is how it
	// ended, once it has, set once. Both under the device's mu.
	last  *addonapi.MediaState
	final *addonapi.MediaState
}

func (d *device) players() players {
	if d.play == nil {
		d.play = devicePlayers{}
	}
	return d.play
}

// Media plays the add-on's video or audio, and pauses, resumes or stops only what it started: what
// somebody else put on since is not the add-on's to touch. A play answers the id media.state gives it.
//
// The players are called without the device's mu held: their hooks, which take it, run on the
// goroutine that played, paused or stopped.
func (d *device) Media(id, op string, p addonapi.MediaPlay) (string, error) {
	pl := d.players()
	if op == addonapi.OpMediaPlay {
		return d.start(id, p)
	}
	d.mu.Lock()
	vid, aud := d.video[id], d.audio[id]
	vidLive, audLive := vid != nil && vid.final == nil, aud != nil && aud.final == nil
	d.mu.Unlock()
	if vidLive {
		if st := pl.videoNow(); st.Active() && st.ID == vid.vid {
			switch op {
			case addonapi.OpMediaPause:
				pl.pauseVideo()
			case addonapi.OpMediaResume:
				pl.resumeVideo()
			case addonapi.OpMediaStop:
				d.end(id, vid, addonapi.MediaStoppedByAddon)
				pl.stopVideo(vid.vid)
			}
			return "", nil
		}
	}
	if audLive {
		if _, _, _, ok := pl.audioNamed(aud.title); ok {
			switch op {
			case addonapi.OpMediaPause:
				pl.pauseAudio()
			case addonapi.OpMediaResume:
				pl.resumeAudio()
			case addonapi.OpMediaStop:
				// Its end is the add-on's, said before the player tells of it as a stop on the device.
				d.end(id, aud, addonapi.MediaStoppedByAddon)
				pl.stopAudio()
			}
			return "", nil
		}
	}
	return "", addonapi.Errorf(addonapi.CodeBadRequest, "nothing the add-on started is playing")
}

// start plays a media.play.
func (d *device) start(id string, p addonapi.MediaPlay) (string, error) {
	pl := d.players()
	name := id
	if n, ok := d.host.names()[id]; ok {
		name = n
	}
	rec := &played{kind: p.Kind}
	switch p.Kind {
	case addonapi.MediaVideo:
		// Listened to before it plays, so the video's first change is heard.
		d.listen.Do(func() { pl.listen(d.audioEnded, d.audioChanged, d.videoChanged) })
		vid, err := pl.playVideo(video.Request{URL: p.URL, Title: p.Title, Origin: video.FromAddon})
		if err != nil {
			return "", asAPIError(addonapi.CodeUnavailable, err)
		}
		rec.vid = vid
	default:
		// The address is held to what a video's is (no control characters, http or https, a host),
		// and one that names the device or loopback by number is refused here; a name is refused as it
		// is dialed (media.PlayNamed, guarded).
		u, err := video.CheckURL(p.URL)
		if err != nil {
			return "", addonapi.Errorf(addonapi.CodeBadRequest, "%v", err)
		}
		if ip := net.ParseIP(strings.Trim(u.Hostname(), "[]")); ip != nil && netguard.Refused(ip) {
			return "", addonapi.Errorf(addonapi.CodeBadRequest, "media is not played from the device itself")
		}
		rec.title = &media.Title{Title: p.Title, Source: name}
	}
	// From the first media.play that is played, not before: a device whose add-ons play nothing has the
	// players' hooks as they were.
	d.listen.Do(func() { pl.listen(d.audioEnded, d.audioChanged, d.videoChanged) })
	d.mu.Lock()
	if d.video == nil {
		d.video, d.audio = map[string]*played{}, map[string]*played{}
	}
	d.seq++
	rec.media = strconv.FormatUint(d.seq, 10)
	slots := d.audio
	if p.Kind == addonapi.MediaVideo {
		slots = d.video
	}
	prev := slots[id]
	// Recorded before an audio track plays, so its end, however soon it comes, finds it. A video's is
	// known only once it has started; its end, should it come first, is found by the next look
	// (MediaStatus), which the server makes once media.play has been answered.
	slots[id] = rec
	d.mu.Unlock()
	// The add-on's last media of this kind is replaced by this one, said before anything is said of
	// the new one. The player's own word for it finds it no longer the add-on's latest, and says nothing.
	if prev != nil {
		d.end(id, prev, addonapi.MediaReplaced)
	}
	if rec.title != nil {
		pl.playAudio(p.URL, rec.title)
	}
	return rec.media, nil
}

// end marks one of the add-on's media over for reason, unless it is already, and tells the add-on.
func (d *device) end(id string, rec *played, reason string) {
	d.mu.Lock()
	if rec.final != nil {
		d.mu.Unlock()
		return
	}
	rec.final = &addonapi.MediaState{Media: rec.media, Kind: rec.kind, State: addonapi.MediaStopped, Reason: reason}
	if rec.last != nil {
		rec.final.PositionMs = rec.last.PositionMs
	}
	st := *rec.final
	d.mu.Unlock()
	d.host.srv.MediaEnded(id, st)
}

// MediaStatus is what the add-on's latest media is doing: the latest started of its video and its
// audio that still plays, else how the latest ended, else idle.
func (d *device) MediaStatus(id string) addonapi.MediaState {
	d.mu.Lock()
	var latest *played
	for _, rec := range []*played{d.video[id], d.audio[id]} {
		if rec == nil {
			continue
		}
		if latest == nil || (live(rec) && !live(latest)) || (live(rec) == live(latest) && newer(rec, latest)) {
			latest = rec
		}
	}
	d.mu.Unlock()
	if latest == nil {
		return addonapi.MediaState{State: addonapi.MediaIdle}
	}
	return d.stateOf(id, latest)
}

// live is whether rec is not over yet. Wants the device's mu.
func live(rec *played) bool { return rec.final == nil }

func newer(a, b *played) bool {
	x, _ := strconv.ParseUint(a.media, 10, 64)
	y, _ := strconv.ParseUint(b.media, 10, 64)
	return x > y
}

// stateOf is what rec is doing now, from the player it plays on. One found over is marked so, and the
// add-on told (end).
func (d *device) stateOf(id string, rec *played) addonapi.MediaState {
	d.mu.Lock()
	final := rec.final
	d.mu.Unlock()
	if final != nil {
		return *final
	}
	pl := d.players()
	st := addonapi.MediaState{Media: rec.media, Kind: rec.kind}
	reason := ""
	if rec.kind == addonapi.MediaVideo {
		v := pl.videoNow()
		switch {
		case v.Active() && v.ID == rec.vid:
			st.State = addonapi.MediaPlaying
			switch v.Phase {
			case video.Paused:
				st.State = addonapi.MediaPaused
			case video.Loading, video.Asking:
				st.State = addonapi.MediaBuffering
			}
			st.PositionMs = addonapi.Ms(v.Pos)
			if v.Dur > 0 {
				st.DurationMs = addonapi.Ms(v.Dur)
			}
		case v.Failed == rec.vid:
			reason = addonapi.MediaError
		case v.Ended == rec.vid:
			reason = addonapi.MediaEnded
		case v.Active():
			reason = addonapi.MediaReplaced
		default:
			reason = addonapi.MediaStoppedOnDevice
		}
	} else {
		paused, heard, at, ok := pl.audioNamed(rec.title)
		if !ok {
			// Not loaded, and not marked over: its end is on its way from the media player (audioEnded),
			// which says how, or it is not loaded yet. Until then it is what it was, so nothing is said
			// of it that the end would have to take back.
			d.mu.Lock()
			defer d.mu.Unlock()
			switch {
			case rec.final != nil:
				return *rec.final
			case rec.last != nil:
				return *rec.last
			}
			st.State = addonapi.MediaBuffering
			return st
		}
		switch {
		case paused:
			st.State = addonapi.MediaPaused
		case !heard:
			st.State = addonapi.MediaBuffering
		default:
			st.State = addonapi.MediaPlaying
		}
		st.PositionMs = addonapi.Ms(at)
	}
	if reason != "" {
		d.end(id, rec, reason)
		d.mu.Lock()
		defer d.mu.Unlock()
		return *rec.final
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if rec.final != nil {
		return *rec.final // it ended as it was looked at
	}
	rec.last = &st
	return st
}

// audioEnded is the media player telling of a named track's end: one of an add-on's latest is marked
// over, for the reason it gives.
func (d *device) audioEnded(e media.NamedEnd) {
	d.mu.Lock()
	var id string
	var rec *played
	for a, r := range d.audio {
		if r.title == e.Title && r.final == nil {
			id, rec = a, r
			break
		}
	}
	d.mu.Unlock()
	if rec == nil {
		return // not an add-on's, or one whose end was said already
	}
	reason := addonapi.MediaEnded
	switch e.How {
	case media.EndFailed:
		reason = addonapi.MediaError
	case media.EndStopped:
		reason = addonapi.MediaStoppedOnDevice
	case media.EndReplaced:
		reason = addonapi.MediaReplaced
	}
	d.end(id, rec, reason)
}

// audioChanged is the room's music maybe changing: each add-on with a track still its own has it looked
// at soon. The hook fires for much that changes nothing, so this only asks the server to look, which
// gathers them.
func (d *device) audioChanged() {
	for _, id := range d.liveOf(d.audioSlots) {
		d.host.srv.MediaChanged(id)
	}
}

// videoChanged is the video player changing: an add-on's video that is over is marked so at once, and
// one still up looked at soon.
func (d *device) videoChanged() {
	for _, id := range d.liveOf(d.videoSlots) {
		d.mu.Lock()
		rec := d.video[id]
		d.mu.Unlock()
		if rec == nil {
			continue
		}
		if st := d.stateOf(id, rec); st.State != addonapi.MediaStopped {
			d.host.srv.MediaChanged(id)
		}
	}
}

func (d *device) audioSlots() map[string]*played { return d.audio }
func (d *device) videoSlots() map[string]*played { return d.video }

// liveOf is the add-ons whose media of one kind is still theirs.
func (d *device) liveOf(slots func() map[string]*played) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ids []string
	for id, rec := range slots() {
		if rec.final == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// forget drops what a removed add-on was playing, and stops it.
func (d *device) forget(id string) {
	_, _ = d.Media(id, addonapi.OpMediaStop, addonapi.MediaPlay{})
	d.mu.Lock()
	delete(d.video, id)
	delete(d.audio, id)
	d.mu.Unlock()
}
