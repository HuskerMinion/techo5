//go:build !dot && !spot

package display

import (
	"context"
	"image"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/plexcast"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The video page (render_video.go): a video from feature/video, full screen over the clock, the
// dashboards and the deck. A call, a ring, the PIN pad, a browser asking for the setup page, the
// pairing and Wi-Fi pages, a camera, the settings and a voice turn go over it, and it pauses under
// them (video.Covered). A tap brings up its controls for a few seconds; a swipe down, or Stop, ends it.
//
// The picture never goes through the canvas. Once frames come, the frame loop draws only the
// controls there, and paintVideo hands each frame to the panel as the video's clock reaches it
// (screen.PresentFrame), blending the controls over it while they are up.

// videoCovered is whether something in the scene goes over the video, so it is not the page.
func videoCovered(s scene) bool {
	// A cue's light, and its captions while they stay: the video stays paused until they are gone.
	turn := s.phase == "listening" || s.phase == "thinking" || s.phase == "replying" || cueLit(s.cue) ||
		(s.cue != "" && (cueWords(s.cue, s.heard, s.reply) != "" || s.failed != ""))
	return s.call.Phase != phone.Idle || s.ring.any() || s.pin.open || s.setupAsking || s.showVideoAsk || s.bt.Pairing ||
		s.showWifi || s.showCamera || s.showSheet || turn
}

// videoScene fills in the video's part of the scene, and tells the player whether it is covered.
func (d *Display) videoScene(s *scene, now time.Time) {
	st := video.Get().State()
	s.video = st
	if id, from, title, ok := video.Get().Asking(); ok {
		s.showVideoAsk, s.videoAsk = true, videoAsk{id: id, from: from, title: title}
	}
	// What draw() puts over the question (render.go) leaves it undrawn, and so unanswerable.
	drawnAsk := uint64(0)
	if s.showVideoAsk && !s.callPageUp() && !s.ring.any() && !s.pin.open && !s.setupAsking {
		drawnAsk = s.videoAsk.id
	}
	d.vask.drawn(drawnAsk, now)
	d.mu.Lock()
	tried := d.videoTried
	d.mu.Unlock()
	failedRecently := st.Failed != 0 && st.Failed == tried && now.Sub(st.ErrAt) < videoFailShown
	up := st.Active() && st.Phase != video.Asking
	s.showVideo = (up || failedRecently) && !videoCovered(*s)
	video.Get().Covered(up && videoCovered(*s))
	d.mu.Lock()
	if up {
		d.videoTried = st.ID
	}
	// A film from the account can have its captions turned on or off from here; anything else has no
	// server burning them into the picture.
	s.videoSubs, s.videoSubsCan = false, false
	if st.Active() && st.Origin == video.FromPlex {
		s.videoSubs, s.videoSubsCan = plexcast.Subtitles()
	}
	controls := now.Before(d.videoUntil) || st.Phase == video.Paused || st.Phase == video.Loading || !st.Frames
	s.videoControls = controls
	s.videoLive = s.showVideo && up && st.Frames
	d.videoOnScreen = s.showVideo
	d.videoLive = s.videoLive
	d.mu.Unlock()
}

// videoNext is how long the frame loop may wait before it looks again while the video page is up.
func (d *Display) videoNext(now time.Time) time.Duration {
	d.mu.Lock()
	until := d.videoUntil
	d.mu.Unlock()
	wait := videoRecheck
	if left := until.Sub(now); left > 0 && left < wait {
		wait = left + 10*time.Millisecond // the controls go when they are due to
	}
	// The spinner turns while the picture is on its way, so the canvas is redrawn faster than the
	// half-second a playing film needs: a ring that moves once a second is not a spinner.
	if st := video.Get().State(); st.Active() && !st.Frames {
		wait = min(wait, 80*time.Millisecond)
	}
	return wait
}

// videoUp is whether the last frame drew the video page: then every finger is its.
func (d *Display) videoUp() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.videoOnScreen
}

// videoGesture is a finger on the video page.
func (d *Display) videoGesture(g touch.Gesture) {
	defer d.wake()
	if !video.Get().State().Active() {
		// The page saying a video could not be played: any touch puts it away.
		d.mu.Lock()
		d.videoTried = 0
		d.mu.Unlock()
		return
	}
	switch g.Kind {
	case touch.SwipeDown:
		slog.Info("screen: video stopped by a swipe")
		go video.Get().Stop()
	case touch.SwipeUp:
		d.showVideoControls()
	case touch.SwipeLeft, touch.SwipeRight:
		// A swipe across the picture is the transport: forward on the left, back on the right, as
		// turning a page. Fifteen seconds a swipe, the step Plex's own players use for their buttons.
		d.showVideoControls()
		step := 15 * time.Second
		if g.Kind == touch.SwipeRight {
			step = -step
		}
		go seekVideo(step)
	case touch.Tap:
		d.mu.Lock()
		shown := time.Now().Before(d.videoUntil)
		d.mu.Unlock()
		st := video.Get().State()
		controls := shown || st.Phase == video.Paused || st.Phase == video.Loading || !st.Frames
		if !controls {
			d.showVideoControls()
			return
		}
		if frac, ok := d.r.videoScrubAt(image.Pt(g.X, g.Y)); ok {
			st := video.Get().State()
			if st.Dur > 0 {
				to := time.Duration(float64(st.Dur) * frac)
				go seekVideo(to - st.Pos)
			}
			d.showVideoControls()
			return
		}
		switch d.r.videoTapped(image.Pt(g.X, g.Y)) {
		case videoTapPlay:
			go video.Get().Toggle()
			d.showVideoControls()
		case videoTapStop:
			slog.Info("screen: video stopped")
			go video.Get().Stop()
		case videoTapSubtitles:
			go toggleSubtitles()
			d.showVideoControls()
		case videoTapQuieter:
			go media.Get().Adjust(-1)
			d.showVideoControls()
		case videoTapLouder:
			go media.Get().Adjust(+1)
			d.showVideoControls()
		default:
			// Off the controls: they go, and the picture is the whole screen again.
			d.mu.Lock()
			d.videoUntil = time.Time{}
			d.mu.Unlock()
		}
	}
}

// seekVideo moves the picture by delta: the account's film through feature/plexcast, which is what the
// phone's transport and the server's Now Playing page read, and any other video through the player
// itself, which has nobody to tell.
func seekVideo(delta time.Duration) {
	st := video.Get().State()
	to := st.Pos + delta
	if to < 0 {
		to = 0
	}
	if st.Dur > 0 && to > st.Dur {
		to = st.Dur
	}
	if st.Origin == video.FromPlex && plexcast.SeekTo(to) {
		return
	}
	video.Get().Seek(to)
}

// toggleSubtitles turns the account's captions the other way, if this film has any to turn.
func toggleSubtitles() {
	on, can := plexcast.Subtitles()
	if !can {
		return
	}
	slog.Info("screen: subtitles", "on", !on)
	if err := plexcast.SetSubtitles(!on); err != nil {
		slog.Warn("screen: the subtitles could not be changed", "err", err)
	}
}

func (d *Display) showVideoControls() {
	d.mu.Lock()
	d.videoUntil = time.Now().Add(videoControls)
	d.mu.Unlock()
}

// videoAskGesture is a finger on the question about a DLNA video: only the two answers answer it, and
// only the question drawn, once it has settled (askLatch).
func (d *Display) videoAskGesture(g touch.Gesture) {
	if g.Kind != touch.Tap || d.r == nil {
		return
	}
	id, ok := d.vask.answerable(time.Now())
	if !ok {
		return
	}
	if allow, answered := d.r.askTap(g.X, g.Y); answered {
		go video.Get().Answer(id, allow)
	}
}

// paintVideo shows the video's frames as they fall due (videoPainter.paint).
func (d *Display) paintVideo(ctx context.Context, wait time.Duration, over image.Rectangle) {
	d.vp.paint(ctx, d.dev, d.poke, wait, over)
}

// answerVideoShots hands whoever asked for a screenshot the video page as it is on the panel.
func (d *Display) answerVideoShots() {
	for {
		select {
		case ch := <-d.shots:
			ch <- d.vp.shot(d.dev, d.r.dst)
		default:
			return
		}
	}
}

// dropVideoFrame gives back the frame kept for the panel once the video page is gone.
func (d *Display) dropVideoFrame() { d.vp.drop() }
