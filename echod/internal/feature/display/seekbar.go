//go:build !dot

package display

import (
	"image"
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dlna"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// Seeking on the screen: the bar under a video's controls and on Now Playing. A tap on the bar moves to
// the place under the finger; a finger that slides along it shows the place it is over, and moves
// there when it lifts. A bar is tappable only when somebody can act on it (seekRoute): a bar that does
// nothing when it is tapped is not drawn as one, and on Now Playing it only shows where the song is.
//
// The touchscreen reports a moving finger as swipes, which the pages use for the volume and to stop a
// video. A finger that comes down on a bar is followed instead (touch.SetFollow, the dashboard's way
// of dragging), from the moment it lands until it lifts, so the rest of the page keeps its swipes. One
// that then sets off up or down rather than along the bar is a swipe after all, and is handed back to
// the touchscreen as one (touch.Unfollow): a swipe for the volume, or to stop a video, may start on
// the bar as well as anywhere else.

// seekRoute is how a place chosen on a bar reaches what plays.
type seekRoute int

const (
	// seekNone is nobody to act on it: the bar only shows where it is.
	seekNone seekRoute = iota
	// seekVideo is the video player moving the video itself (video.Seek): one from Home Assistant.
	seekVideo
	// seekDLNA is the DLNA renderer's song or video (dlna.SeekFromDevice): moved by the renderer when
	// it can, asked of its controller when it cannot.
	seekDLNA
	// seekMusic is Music Assistant's queue for this device (musicSeek, the Show's Now Playing).
	seekMusic
)

func (r seekRoute) String() string {
	switch r {
	case seekVideo:
		return "video"
	case seekDLNA:
		return "dlna"
	case seekMusic:
		return "music assistant"
	}
	return "none"
}

// seekHeld is how long the place just sent is shown on the bar in place of the position the source
// says, unless the source has caught up first: a seek takes a moment to land, and a bar that jumped
// back to where the song was meanwhile looks as if the tap did nothing.
const seekHeld = 3 * time.Second

// seekCaught is how close the source's position has to come to the place sent for it to have landed.
const seekCaught = 2 * time.Second

// seekView is a bar's part of a scene: how a place chosen on it is reached, the video it is for, how
// long what it moves runs, and the place to show in place of the position (shown) while a finger is
// choosing one or one has just been sent.
type seekView struct {
	route seekRoute
	id    uint64
	dur   time.Duration // the whole song's or film's, start included
	// start is where in the film the stream playing begins (dlna.VideoStart): positions the player
	// gives are the stream's, so the film's own is start further on.
	start time.Duration
	shown bool
	to    time.Duration
}

// tappable is whether the bar takes a finger: it has a length to place a tap in and a way to send it.
func (v seekView) tappable() bool { return v.route != seekNone && v.dur > 0 }

// at is the position to draw: the place shown, or pos as the source says it.
func (v seekView) at(pos time.Duration) time.Duration {
	if v.shown {
		return v.to
	}
	return v.start + pos
}

// seekZone is a bar as drawn, for a finger: its track, what a finger on it hits (reach), and the seek
// it makes.
type seekZone struct {
	view  seekView
	bar   image.Rectangle
	reach image.Rectangle
}

// on is whether p is on the bar, for a bar that takes a finger.
func (z seekZone) on(p image.Point) bool { return z.view.tappable() && z.bar.Dx() > 0 && p.In(z.reach) }

// at is the place in what the bar moves that a finger at x is over. Past either end of the track is
// that end, so a finger that slides off the bar's end is at the start or at the end.
func (z seekZone) at(x int) time.Duration {
	if z.bar.Dx() <= 0 || z.view.dur <= 0 {
		return 0
	}
	f := float64(x-z.bar.Min.X) / float64(z.bar.Dx())
	return time.Duration(min(max(f, 0), 1) * float64(z.view.dur))
}

// seekFill is how much of a track w wide is filled for pos into dur.
func seekFill(w int, pos, dur time.Duration) int {
	if w <= 0 || dur <= 0 {
		return 0
	}
	return int(float64(w) * float64(min(max(pos, 0), dur)) / float64(dur))
}

// seekBars is the bar the frame being drawn has (next, which only the frame loop touches) and the one
// the panel shows, which a finger finds. The frame's is published once the frame is done, as the call
// bar's buttons are, so a finger landing while a frame draws finds the bar on the panel.
type seekBars struct {
	mu    sync.Mutex
	shown seekZone
	next  seekZone
}

// begin is a frame starting: it has no bar until a page draws one.
func (b *seekBars) begin() { b.next = seekZone{} }

// drew is the frame having drawn z.
func (b *seekBars) drew(z seekZone) { b.next = z }

// publish is the frame done: its bar is the one on the panel.
func (b *seekBars) publish() {
	b.mu.Lock()
	b.shown = b.next
	b.mu.Unlock()
}

// zone is the bar on the panel, if any.
func (b *seekBars) zone() seekZone {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.shown
}

// seekFinger is a finger on a bar: the bar it came down on, whether follow mode was turned on for it,
// whether it is sliding along it and to where, and the place last sent, shown until heldUntil.
type seekFinger struct {
	zone   seekZone
	from   image.Point // where it came down
	landed bool
	follow bool
	moving bool
	// along is the finger having set off along the bar: decided at its first Drag, and from then on
	// it slides the bar wherever it wanders.
	along bool
	to    time.Duration

	heldRoute seekRoute
	heldID    uint64
	held      time.Duration
	heldUntil time.Time
}

// The ways a seek goes; tests put their own in.
var (
	seekVideoTo  = video.Seek
	seekDLNATo   = func(id uint64, to time.Duration) bool { return dlna.Get().SeekFromDevice(id, to) }
	videoCanSeek = video.Seekable
	dlnaVideoWay = func(id uint64) (dlna.SeekWay, time.Duration) { return dlna.Get().VideoSeekWay(id) }
	dlnaSongWay  = func() (dlna.SeekWay, time.Duration) { return dlna.Get().SeekWay() }
	// dlnaVideoStart is where in the film the renderer's video begins (dlna.VideoStart).
	dlnaVideoStart = func(id uint64) time.Duration { return dlna.Get().VideoStart(id) }
)

// videoSeekRoute is how the video the player has is moved from its bar, and how long it runs. A DLNA
// video is the renderer's to move, or its controller's (dlna.VideoSeekWay); one from Home Assistant is
// the player's, when it says how long it runs and can be moved through. A video that cannot be moved
// by anybody has no bar.
func videoSeekRoute(st video.State) seekView {
	if !st.Active() || st.Phase == video.Asking {
		return seekView{}
	}
	if st.Origin == video.FromDLNA {
		if way, dur := dlnaVideoWay(st.ID); way != dlna.SeekNone && dur > 0 {
			start := dlnaVideoStart(st.ID)
			return seekView{route: seekDLNA, id: st.ID, dur: start + dur, start: start}
		}
		return seekView{}
	}
	if ok, known := videoCanSeek(st.ID); ok && known && st.Dur > 0 {
		return seekView{route: seekVideo, id: st.ID, dur: st.Dur}
	}
	return seekView{}
}

// seekRun sends a place chosen on a bar the way the bar's seek goes. The ways that ask over the
// network are not waited for: this runs on the touchscreen's goroutine.
func seekRun(v seekView, to time.Duration) {
	// A place at the very end would only stop the song or film there (or skip Music Assistant on to the
	// next track): the end is a second short of it.
	if v.dur > 2*time.Second {
		to = min(to, v.dur-time.Second)
	}
	slog.Info("screen: seek", "route", v.route, "to", to.Round(time.Second))
	switch v.route {
	case seekVideo:
		go func() {
			if err := seekVideoTo(v.id, to); err != nil {
				slog.Info("screen: the video was not moved", "err", err)
			}
		}()
	case seekDLNA:
		go func() {
			if !seekDLNATo(v.id, to) {
				slog.Info("screen: a seek on a DLNA stream with nobody to make it")
			}
		}()
	case seekMusic:
		musicSeek(to)
	}
}

// seekShowLocked is the place a bar for v shows in place of pos, the position its source says: the
// place under a finger sliding along it, or the place just sent until the source has caught up with it
// or seekHeld has passed. Called with d.mu held.
func (d *Display) seekShowLocked(v seekView, pos time.Duration, now time.Time) seekView {
	pos += v.start // the source's position is the stream's; the place held is the film's
	f := &d.seekF
	switch {
	case v.route == seekNone:
	case f.moving && f.zone.view.route == v.route && f.zone.view.id == v.id:
		v.shown, v.to = true, f.to
	case f.heldRoute == v.route && f.heldID == v.id && now.Before(f.heldUntil):
		if gap := pos - f.held; gap > -seekCaught && gap < seekCaught {
			f.heldUntil = time.Time{}
			break
		}
		v.shown, v.to = true, f.held
	}
	return v
}

// seekSentLocked notes a place sent, to show it until it lands. Called with d.mu held.
func (d *Display) seekSentLocked(v seekView, to time.Duration) {
	d.seekF.heldRoute, d.seekF.heldID, d.seekF.held, d.seekF.heldUntil = v.route, v.id, to, time.Now().Add(seekHeld)
}

// seekLanded is a finger coming down (the touchscreen's Landed). On a bar that takes a finger, the
// touchscreen follows it from now until it lifts, so that it slides the bar rather than swiping; follow
// mode is turned on here only if nothing else has it on, and off again only then (seekLifted).
func (d *Display) seekLanded(at image.Point) {
	if d.r == nil {
		return
	}
	z := d.r.seekBars.zone()
	d.mu.Lock()
	shown := d.pageShown
	f := &d.seekF
	f.zone, f.landed, f.moving, f.along = seekZone{}, false, false, false
	// A finger that comes down where a pull starts (the drawer's edge) is the pull's: a tap there is
	// still the bar's (seekGesture), but a finger moving from there is not followed.
	if !z.on(at) || !d.on || !d.seekTakesLocked() || d.seekPullLocked(at) {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()
	// A card over the page is not the bar under it (pressRefused): its own taps come first.
	if d.pressRefused(at, shown) {
		return
	}
	d.mu.Lock()
	f.zone, f.landed, f.from = z, true, at
	f.follow = !d.followWantedLocked()
	turn := f.follow
	d.mu.Unlock()
	if turn {
		touch.Get().SetFollow(true)
	}
}

// seekLifted is the finger lifting (the touchscreen's Down, false), after its last gesture: follow mode
// goes back off if seekLanded turned it on and nothing else has wanted it meanwhile.
func (d *Display) seekLifted() {
	d.mu.Lock()
	f := &d.seekF
	turn := f.follow && !d.followWantedLocked()
	f.landed, f.follow, f.moving, f.along = false, false, false, false
	d.mu.Unlock()
	if turn {
		touch.Get().SetFollow(false)
	}
}

// across is whether a finger that has moved by dx, dy since it came down set off up or down rather
// than along a bar: clearly so (twice as far up or down as along), as a finger that slides along the
// bar often wobbles a little first.
func across(dx, dy int) bool { return abs(dy) > 2*abs(dx) }

// seekGesture is a finger on the bar as last drawn: a tap moves to the place under it; a finger that
// came down on the bar and slides along it (Hold, Drag) shows the place it is over, and moves there as
// it lifts (Release). It says whether the finger was the bar's, which the page then leaves alone.
func (d *Display) seekGesture(g touch.Gesture) bool {
	if d.r == nil {
		return false
	}
	at := image.Pt(g.X, g.Y)
	switch g.Kind {
	case touch.Tap:
		z := d.r.seekBars.zone()
		if !z.on(at) {
			return false
		}
		to := z.at(g.X)
		d.mu.Lock()
		d.seekSentLocked(z.view, to)
		d.mu.Unlock()
		seekRun(z.view, to)
		d.wake()
		return true
	case touch.Hold:
		d.mu.Lock()
		f := &d.seekF
		took := f.landed && f.zone.on(at)
		if took {
			f.moving, f.to = true, f.zone.at(g.X)
		}
		d.mu.Unlock()
		if took {
			d.wake()
		}
		return took
	case touch.Drag:
		d.mu.Lock()
		f := &d.seekF
		took := f.moving
		handBack, turn := false, false
		switch {
		case took && !f.along && across(g.X-f.from.X, g.Y-f.from.Y):
			// Up or down from the start: a swipe, not a seek. The bar lets it go, follow mode goes
			// off if the bar turned it on, and the touchscreen reports the finger as the swipe it is
			// from its next move, to the page under it. The place it showed is shown no longer.
			f.moving, f.landed = false, false
			handBack, turn = f.follow, f.follow && !d.followWantedLocked()
			f.follow = false
		case took:
			f.along = true
			f.to = f.zone.at(g.X)
		}
		d.mu.Unlock()
		if turn {
			touch.Get().SetFollow(false)
		}
		if handBack {
			touch.Get().Unfollow()
		}
		if took {
			d.wake()
		}
		return took
	case touch.Release:
		d.mu.Lock()
		f := &d.seekF
		took := f.moving
		z, to := f.zone, f.zone.at(g.X)
		if took {
			f.moving = false
			d.seekSentLocked(z.view, to)
		}
		d.mu.Unlock()
		if took {
			seekRun(z.view, to)
			d.wake()
		}
		return took
	}
	return false
}
