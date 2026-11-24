package dlna

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Seek. A controller's slider sends a seek for every place the finger passes, a few a second or more:
// each is answered at once, and the position reads as the last one asked for, but only the last of a
// burst is carried out, seekSettle after the first, so the server is not asked for the song (or the
// decoder started) once for every one of them.
//
// A video is the video player's to move (video.Seek: its decoder started again there). A song is
// fetched again from the place (seekmap.go) and replaces itself on the speaker, as a song set does,
// so Now Playing, a turn's ducking and the volume carry on as they were; while it is being fetched the
// transport reads TRANSITIONING. A seek while paused waits for Play, so nothing of the song is heard
// from the old place in between, and the position reads as the new one meanwhile.

// seekSettle is how long a burst of seeks is given to settle before its last is carried out. A var so
// a test can hold the burst open and carry it out itself, rather than race the timer on a loaded run.
var seekSettle = 300 * time.Millisecond

// The video player, as a seek uses it; tests put their own in.
var (
	seekVideo     = video.Seek
	videoSeekable = video.Seekable
	currentVideo  = video.Current
)

// seekReq is a start that is a seek: from to in the song m maps, with the cancel of its fetch, which
// becomes the song's once it plays.
type seekReq struct {
	m      *seekMap
	to     time.Duration
	cancel context.CancelFunc
}

// seek takes a controller's Seek.
func (r *renderer) seek(unit, target string) error {
	switch strings.ToUpper(strings.TrimSpace(unit)) {
	case "REL_TIME", "ABS_TIME":
		// One track at a time: the time in the track is the time in the whole.
	default:
		return errNotSupported
	}
	to, ok := parseSeekTarget(target)
	if !ok {
		return errBadTarget
	}
	return r.seekPlace(to)
}

// seekPlace moves the current song or video to to: a controller's Seek, or a seek on the device's own
// screen (SeekFromDevice), which go the same way so that a burst from either settles the same.
func (r *renderer) seekPlace(to time.Duration) error {
	st := r.sync()
	ok, known, dur := r.seekable()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur.uri == "" || (st != stPlaying && st != stPaused && st != stLoading) {
		return errNoContents
	}
	if known && !ok {
		return errNotSupported
	}
	if dur > 0 && to > dur {
		return errBadTarget
	}
	r.seekTo, r.seeking = to, true
	if r.seekTimer == nil {
		r.seekArm++
		arm := r.seekArm
		r.seekTimer = time.AfterFunc(seekSettle, func() { r.settled(arm) })
	}
	return nil
}

// settled is the settle timer armed as arm (r.seekArm) going off. A timer that went off just as the
// seek it was for was forgotten (clearSeekLocked) is no longer the one armed: by the time it has the
// lock another seek may have armed its own, and it must neither carry that one out early nor forget
// its timer, which would leave it to go off later for nothing and a third armed beside it.
func (r *renderer) settled(arm int) {
	r.mu.Lock()
	if arm != r.seekArm {
		r.mu.Unlock()
		return
	}
	r.carryOutSeek()
}

// seekable is whether the current song can be moved through, whether that is known yet (not while a
// video is still being found out, nor a song still starting or being mapped), and its length.
func (r *renderer) seekable() (ok, known bool, dur time.Duration) {
	r.mu.Lock()
	isVideo, id, dur := r.cur.video, r.videoID, r.cur.dur
	r.mu.Unlock()
	if isVideo {
		if id == 0 {
			return false, false, dur
		}
		ok, known = videoSeekable(id)
		if vs := currentVideo(); dur == 0 && vs.ID == id {
			dur = vs.Dur
		}
		return ok, known, dur
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.smap != nil && r.smap.uri == r.cur.uri:
		return true, true, r.smap.dur
	case r.mapping == r.cur.uri || r.state == stLoading:
		return false, false, dur
	}
	return false, true, dur
}

// seekNow carries out the last seek asked for, once the burst has settled.
func (r *renderer) seekNow() {
	r.mu.Lock()
	r.carryOutSeek()
}

// carryOutSeek is seekNow with r.mu held, which it lets go.
func (r *renderer) carryOutSeek() {
	// Carried out now: a settle timer armed in the moment before (a seekPlace between a caller's
	// check and this) is no longer the one armed, so it finds nothing of its own when it goes off.
	if r.seekTimer != nil {
		r.seekTimer.Stop()
	}
	r.seekArm++
	r.seekTimer = nil
	if !r.seeking {
		r.mu.Unlock()
		return
	}
	to := r.seekTo
	if r.cur.video {
		id := r.videoID
		r.seeking = false
		r.mu.Unlock()
		if id != 0 {
			if err := seekVideo(id, to); err != nil {
				slog.Info("dlna: the video was not moved", "to", to.Round(time.Second), "err", err)
			}
		}
		r.f.e.changed()
		return
	}
	switch {
	case r.smap == nil || r.smap.uri != r.cur.uri:
		// Still starting, or still being mapped: carried out once it is (mapped). Otherwise it has
		// gone, and the seek with it.
		if r.mapping != r.cur.uri && r.state != stLoading {
			r.seeking = false
		}
	case r.state == stLoading && r.seekFetch == nil:
		// The song is still starting (a Play, not a seek, is fetching it): carried out once it has
		// (start). Fetched again now, the start under way would be thrown away for a seek that then
		// finds nothing of the renderer's on the speaker, and stops.
	case r.state == stPaused:
		r.seekOnPlay = true
	case r.state == stPlaying || r.state == stLoading:
		r.startSeekLocked(to)
	default:
		r.seeking = false
	}
	r.mu.Unlock()
	r.f.e.changed()
}

// startSeekLocked fetches the song again from to. The song plays on from where it was until the
// fetch has the new place, and then gives way to it. Wants r.mu.
func (r *renderer) startSeekLocked(to time.Duration) {
	r.gen++
	gen, s, m := r.gen, r.cur, r.smap
	r.state, r.seekOnPlay = stLoading, false
	if r.seekFetch != nil {
		r.seekFetch() // an earlier seek still fetching: overtaken
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.seekFetch = cancel
	slog.Info("dlna: moving", "title", s.title, "to", to.Round(time.Second))
	safe.Go("dlna seek", func() { r.start(ctx, gen, s, &seekReq{m: m, to: to, cancel: cancel}) })
}

// handedOver stands in for a seek's fetch once its body has gone to the speaker: a seek that overtakes
// it then must not end the fetch, which would end the song's sound with it, but the transport is still
// moving (TRANSITIONING) until the seek has landed.
func handedOver() {}

// clearMapLocked forgets how to move through the song, and stops finding it out: another song set, or
// the switch going off. Only the current song's map is kept, and a song set again is mapped again, as
// the file at its address may have changed. Wants r.mu.
func (r *renderer) clearMapLocked() {
	r.smap = nil
	r.stopMappingLocked()
}

// stopMappingLocked ends a mapping under way, which then changes nothing: the song stopped, failed or
// another set. A song mapped already keeps its map. Wants r.mu.
func (r *renderer) stopMappingLocked() {
	r.mapping = ""
	r.mapGen++
	if r.mapCancel != nil {
		r.mapCancel()
		r.mapCancel = nil
	}
}

// clearSeekLocked forgets a seek not yet carried out: the song stopped or another set. Wants r.mu.
func (r *renderer) clearSeekLocked() {
	r.seeking, r.seekOnPlay = false, false
	r.seekArm++ // a timer going off now, too late to be stopped, is not the one armed (settled)
	if r.seekTimer != nil {
		r.seekTimer.Stop()
		r.seekTimer = nil
	}
	if r.seekFetch != nil {
		r.seekFetch()
		r.seekFetch = nil
	}
	r.stopMappingLocked()
}

// mapSong finds out how to move through the song s, length bytes long, once it has started: from now
// on it can be moved through, and a seek asked for while it was being found out is carried out. ctx is
// the mapping's own, ended when the song is stopped or another set (clearMapLocked); gen is which
// mapping it is (r.mapGen).
func (r *renderer) mapSong(ctx context.Context, cancel context.CancelFunc, gen int, s song, length int64) {
	defer cancel()
	m, err := mapSong(ctx, fetchClient, s.uri, length, s.dur)
	r.mu.Lock()
	if r.mapGen != gen {
		// Ended meanwhile (stopped, another song set): what was found out is not the current song's,
		// and another mapping may be under way for it.
		r.mu.Unlock()
		return
	}
	r.mapping, r.mapCancel = "", nil
	if r.cur.uri != s.uri {
		r.mu.Unlock()
		return
	}
	if err != nil {
		slog.Info("dlna: the song cannot be moved through", "title", s.title, "err", quietErr(err))
		if r.seeking && r.seekTimer == nil {
			r.seeking = false
		}
		r.mu.Unlock()
		r.f.e.changed()
		return
	}
	r.smap = m
	if r.cur.dur == 0 {
		// A description that gave no length: the file's own, so a controller has a slider to move.
		r.cur.dur = m.dur
	}
	pending := r.seeking && r.seekTimer == nil
	r.mu.Unlock()
	slog.Info("dlna: the song can be moved through", "title", s.title, "format", m.kind, "length", m.dur.Round(time.Second))
	if pending {
		r.seekNow()
	}
	r.f.e.changed()
}

// actions is CurrentTransportActions: what a controller may do now.
func (r *renderer) actions() string {
	st := r.sync()
	ok, known, _ := r.seekable()
	seek := ""
	if ok && known {
		seek = ",Seek"
	}
	switch st {
	case stPlaying:
		return "Pause,Stop" + seek
	case stPaused:
		return "Play,Stop" + seek
	case stLoading:
		return "Play" + seek
	}
	return "Play"
}
