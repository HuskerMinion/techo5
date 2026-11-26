//go:build !dot && !spot

package display

import (
	"context"
	"image"
	"image/color"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dlna"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// The Show's bars to seek on (seekbar.go): the video's, and Now Playing's in either style.

// seekReach is how far above and below a bar's track a finger still lands on it, in the Show 5's
// pixels: the track is a few pixels thick, a finger is not.
const seekReach = 16

// seekThick is how thick a bar's track is drawn, in the Show 5's pixels.
const seekThick = 6

// followWantedLocked is whether something besides a bar wants the touchscreen to follow every finger:
// the dashboard and a custom screen that asks (custom_screens.go). Called with d.mu held.
func (d *Display) followWantedLocked() bool { return d.dashFollow || d.customs.follow }

// seekTakesLocked is whether a finger on a bar is the bar's: not on a night light, where every touch
// is the night light's (gesture), unless the video page is up, whose controls work at night as by day.
// Called with d.mu held.
func (d *Display) seekTakesLocked() bool { return !d.nightGlow || d.videoOnScreen }

// receivingNow names the received track playing, for a test to say.
var receivingNow = func() string { return media.Get().Receiving() }

// musicSeekRoute is how a place chosen on Now Playing's bar reaches what plays, for the song rd at pos.
// A DLNA song is the renderer's to move or its controller's (dlna.SeekWay); a track Music Assistant
// plays on this device's queue (seekable) is the queue's. That includes a song Music Assistant sends
// over DLNA that the renderer cannot move through (a stream with no length in bytes): the queue can,
// as the back and forward buttons beside the bar already do. Anything else (Spotify Connect, AirPlay,
// a station, another room's player) has a bar that only shows where it is.
func musicSeekRoute(rd home.Radio, paused bool, pos media.Position) seekView {
	if pos.Dur <= 0 || rd.Followed {
		return seekView{}
	}
	if receivingNow() == home.DLNAName {
		if way, _ := dlnaSongWay(); way != dlna.SeekNone {
			return seekView{route: seekDLNA, dur: pos.Dur}
		}
	}
	if seekable(rd, paused) {
		return seekView{route: seekMusic, dur: pos.Dur}
	}
	return seekView{}
}

// musicSeek moves what Music Assistant plays on this device's queue to to, as spokenSkipBy moves it by
// seconds. A token that may not use the queue's controls falls back to Home Assistant's seek on the
// player, which takes the same place; any other failure is left alone.
func musicSeek(to time.Duration) {
	c, set := libClient()
	target, queue := spokenWhere()
	if !set || queue == "" {
		return
	}
	at := int(max(to, 0) / time.Second)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := c.Seek(ctx, queue, at)
		if err == nil {
			return
		}
		if !musicassistant.IsForbidden(err) {
			slog.Warn("now playing: seek", "err", err)
			return
		}
		slog.Warn("now playing: seek through Home Assistant instead", "err", err)
		libTap(dashboard.Action{Entity: target, Service: "media_player.media_seek", Data: map[string]any{"seek_position": float64(at)}})
	}()
}

// seekBar draws a bar's track from x0 to x1 centered on y, filled to pos of dur in fill over track, and
// says where it is for a finger, if v takes one.
func (r *renderer) seekBar(v seekView, x0, x1, y int, pos, dur time.Duration, track, fill color.RGBA) {
	thick := r.s(seekThick)
	bar := image.Rect(x0, y-thick/2, x1, y-thick/2+thick)
	r.roundRect(bar, thick/2, track)
	done := bar
	done.Max.X = bar.Min.X + seekFill(bar.Dx(), v.at(pos), dur)
	if !done.Empty() {
		r.roundRect(done, min(done.Dx(), done.Dy())/2, fill)
	}
	if !v.tappable() {
		return
	}
	// A knob where playback is, so a finger can see where it starts from (a fill alone can be too
	// short to see, early in a long film), and a larger one where a finger is choosing.
	knob := r.s(8)
	if v.shown {
		knob = r.s(11)
	}
	r.disc(done.Max.X, y, knob, fill)
	reach := r.s(seekReach)
	r.seekBars.drew(seekZone{view: v, bar: bar, reach: image.Rect(bar.Min.X-r.s(8), y-reach, bar.Max.X+r.s(8), y+reach)})
}

// seekPullLocked is whether a finger coming down at at is where a pull starts, rather than on the bar:
// the left and right edges, where a swipe in brings up the dashboard or the drawer (drawerEdge). Not
// on the video page, which has no drawer and takes every finger. Called with d.mu held.
func (d *Display) seekPullLocked(at image.Point) bool {
	if d.videoOnScreen || d.r == nil {
		return false
	}
	edge := d.r.drawerEdge()
	return at.X < edge || at.X >= d.r.w-edge
}

// keep is a frame that keeps the page the last one drew (Rainbow's kept page, on the Show only): its
// bar is still the one on the panel.
func (b *seekBars) keep() { b.next = b.zone() }
