//go:build spot

package display

import (
	"image"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The video face on the Spot (render_video_spot.go), the Show's video page (video.go) fitted to the
// round panel: the picture inside the circle, a tap for the controls along the foot of it, a swipe down
// or Stop to end it. What covers it is what takes the Spot's face: a call, a ring, the PIN pad, a
// browser's or a DLNA video's question, the pairing, a camera, the settings and the dial's menu, an
// announcement or a reminder, a voice turn. It pauses under them and goes on afterward.

// videoCoveredSpot is whether something in the scene goes over the video.
func videoCoveredSpot(s roundScene) bool {
	turn := s.phase == "listening" || s.phase == "thinking" || s.phase == "replying"
	return s.call.Phase != phone.Idle || s.ringing.any() || s.pin.open || s.setupAsking || s.showVideoAsk || s.btPairing ||
		s.showCamera || s.sheetOpen || s.menuOpen || s.announceRecording || s.showReminder || s.showAnnouncement || turn
}

// videoSceneSpot fills in the video's part of the scene, and tells the player whether it is covered.
func (d *Display) videoSceneSpot(s *roundScene, now time.Time) {
	st := video.Get().State()
	s.video = st
	if id, from, title, ok := video.Get().Asking(); ok {
		s.showVideoAsk, s.videoAsk = true, videoAsk{id: id, from: from, title: title}
	}
	drawnAsk := uint64(0)
	if s.showVideoAsk && s.call.Phase == phone.Idle && !s.ringing.any() && !s.pin.open && !s.setupAsking {
		drawnAsk = s.videoAsk.id
	}
	d.vask.drawn(drawnAsk, now)
	d.mu.Lock()
	failedRecently := st.Failed != 0 && st.Failed == d.videoTried && now.Sub(st.ErrAt) < videoFailShown
	up := st.Active() && st.Phase != video.Asking
	covered := videoCoveredSpot(*s)
	s.showVideo = (up || failedRecently) && !covered
	if up {
		d.videoTried = st.ID
	}
	s.videoControls = now.Before(d.videoUntil) || st.Phase == video.Paused || st.Phase == video.Loading || !st.Frames || s.showVolume
	s.videoLive = s.showVideo && up && st.Frames
	d.videoOnScreen = s.showVideo
	d.mu.Unlock()
	video.Get().Covered(up && covered)
}

// videoNextSpot is how long the frame loop may wait before it looks again while the video is up.
func (d *Display) videoNextSpot(now time.Time) time.Duration {
	d.mu.Lock()
	until := d.videoUntil
	d.mu.Unlock()
	wait := videoRecheck
	if left := until.Sub(now); left > 0 && left < wait {
		wait = left + 10*time.Millisecond
	}
	return wait
}

func (d *Display) videoUpSpot() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.videoOnScreen
}

// videoGestureSpot is a finger on the video face.
func (d *Display) videoGestureSpot(g touch.Gesture) {
	defer d.wake()
	if !video.Get().State().Active() {
		d.mu.Lock()
		d.videoTried = 0 // the face saying a video could not be played: any touch puts it away
		d.mu.Unlock()
		return
	}
	show := func() {
		d.mu.Lock()
		d.videoUntil = time.Now().Add(videoControls)
		d.mu.Unlock()
	}
	switch g.Kind {
	case touch.SwipeDown:
		slog.Info("screen: video stopped by a swipe")
		go video.Get().Stop()
	case touch.SwipeUp:
		show()
	case touch.Tap:
		d.mu.Lock()
		shown := time.Now().Before(d.videoUntil)
		d.mu.Unlock()
		st := video.Get().State()
		if !(shown || st.Phase == video.Paused || st.Phase == video.Loading || !st.Frames) {
			show()
			return
		}
		switch d.r.videoTappedSpot(image.Pt(g.X, g.Y)) {
		case videoTapPlay:
			go video.Get().Toggle()
			show()
		case videoTapStop:
			slog.Info("screen: video stopped")
			go video.Get().Stop()
		case videoTapQuieter:
			go media.Get().Adjust(-1)
			show()
		case videoTapLouder:
			go media.Get().Adjust(+1)
			show()
		default:
			d.mu.Lock()
			d.videoUntil = time.Time{}
			d.mu.Unlock()
		}
	}
}
