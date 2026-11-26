package serve

import (
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// media.state: what the media an add-on started is doing, sent to the add-on as it changes. The device
// says only that something may have changed (MediaChanged), from hooks that fire for much that changes
// nothing, and when one of the add-on's media ends (MediaEnded); what is said is worked out here, so
// the fake daemon's add-on is told exactly what a device's would be.
//
// It is bounded and cheap: the changes of one add-on's media are gathered for addonapi.MediaCoalesce
// and the latest said, a playing track's position is said at most every addonapi.MediaPositionEvery,
// and each media's end once. Nothing is logged or written for any of it.

// mediaCoalesce and positionEvery are addonapi's, as variables for tests.
var (
	mediaCoalesce = addonapi.MediaCoalesce
	positionEvery = addonapi.MediaPositionEvery
)

// keepSent is how many of an add-on's media the server remembers what it last said about: the one
// playing, and the few before it whose end may still be on its way.
const keepSent = 4

// mediaWatch is one add-on's media.state, under its own mu: held while an event is decided and
// queued, so two of them cannot reach the add-on in the other order from the one they were decided in.
type mediaWatch struct {
	mu     sync.Mutex
	closed bool
	timer  *time.Timer // the next look, nil with none due
	due    time.Time
	sent   []addonapi.MediaState // what was last said of each media, oldest first
	posAt  time.Time             // when a position was last said
}

// MediaChanged is the device saying the add-on's media may have changed: it is looked at within
// mediaCoalesce, together with whatever else changes by then.
func (s *Server) MediaChanged(id string) {
	if a := s.addon(id); a != nil {
		s.mediaSoon(a, mediaCoalesce)
	}
}

// MediaEnded is the device saying one of the add-on's media is over, st saying how. It is said at once,
// before anything started after it: an add-on whose next media.play replaced its own last media hears
// of that end before the next one plays.
func (s *Server) MediaEnded(id string, st addonapi.MediaState) {
	if a := s.addon(id); a != nil {
		s.mediaReport(a, st)
	}
}

func (s *Server) addon(id string) *addon {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addons[id]
}

// mediaSoon has the add-on's media looked at within d, unless a look is due sooner already: a burst of
// changes costs one look, and so one event.
func (s *Server) mediaSoon(a *addon, d time.Duration) {
	w := &a.media
	w.mu.Lock()
	defer w.mu.Unlock()
	due := time.Now().Add(d)
	if w.closed || (w.timer != nil && !w.due.After(due)) {
		return
	}
	if w.timer != nil {
		// One already running finds nothing new to say: the look is idempotent.
		w.timer.Stop()
	}
	w.due = due
	w.timer = time.AfterFunc(d, func() { s.mediaLook(a) })
}

// mediaLook asks the device what the add-on's media is doing, and says it if that is news.
func (s *Server) mediaLook(a *addon) {
	w := &a.media
	w.mu.Lock()
	w.timer = nil
	closed := w.closed
	w.mu.Unlock()
	if closed {
		return
	}
	s.mediaReport(a, s.Device.MediaStatus(a.grant.ID))
}

// mediaReport says st to the add-on when it is news: a media not said before, a change of its state, its
// reason or its length, or a playing media's position once positionEvery has passed. A media's end is
// said once and nothing of it after. While one plays, another look is due for its position.
func (s *Server) mediaReport(a *addon, st addonapi.MediaState) {
	if st.Media == "" {
		return // idle: the add-on started nothing to be told of
	}
	w := &a.media
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	now := s.Now()
	i := w.find(st.Media)
	send := i < 0 || changed(w.sent[i], st)
	if i >= 0 && w.sent[i].State == addonapi.MediaStopped {
		send = false
	}
	playing := st.State == addonapi.MediaPlaying
	if !send && playing && st.PositionMs != nil && now.Sub(w.posAt) >= positionEvery {
		send = true
	}
	if send {
		w.remember(i, st)
		if st.PositionMs != nil {
			w.posAt = now
		}
		s.Emit(a.grant.ID, addonapi.EventMediaState, st)
	}
	if playing {
		next := max(positionEvery-now.Sub(w.posAt), mediaCoalesce)
		due := time.Now().Add(next)
		if w.timer == nil || w.due.After(due) {
			if w.timer != nil {
				w.timer.Stop()
			}
			w.due = due
			w.timer = time.AfterFunc(next, func() { s.mediaLook(a) })
		}
	}
}

// changed is whether st says something about a media that was not said in last: its position alone is
// not, as it moves all the time.
func changed(last, st addonapi.MediaState) bool {
	return last.State != st.State || last.Reason != st.Reason || last.Kind != st.Kind || !sameMs(last.DurationMs, st.DurationMs)
}

func sameMs(a, b *int64) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

func (w *mediaWatch) find(media string) int {
	for i, st := range w.sent {
		if st.Media == media {
			return i
		}
	}
	return -1
}

// remember keeps st as what was last said of its media (at i, or new), forgetting the oldest past
// keepSent.
func (w *mediaWatch) remember(i int, st addonapi.MediaState) {
	if i >= 0 {
		w.sent = append(w.sent[:i], w.sent[i+1:]...)
	}
	w.sent = append(w.sent, st)
	if len(w.sent) > keepSent {
		w.sent = w.sent[len(w.sent)-keepSent:]
	}
}

// mediaClose stops looking at a closed add-on's media.
func (s *Server) mediaClose(a *addon) {
	w := &a.media
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}
