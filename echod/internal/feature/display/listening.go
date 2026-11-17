//go:build !dot && !spot

package display

import (
	"context"
	"image"
	"image/color"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// Listening to an audiobook or a podcast episode on this device: Now Playing and the music strip have
// back and forward 30 seconds in place of the previous and next track, which would go to another book
// or episode, and Now Playing has the playback speed. Whether what plays is one is asked of Music
// Assistant, on this device's player's queue, when the track changes and now and then while it plays.

// spokenEvery is how often what plays is asked about again while the track stays the same: a speed set
// elsewhere shows within this.
const spokenEvery = 30 * time.Second

// spokenSkip is how far back and forward go, in seconds.
const spokenSkip = 30

// spokenSpeeds are the playback speeds the speed button steps through.
var spokenSpeeds = []float64{0.75, 1, 1.25, 1.5, 2}

// spokenState is what Music Assistant last said this device's queue plays, for the track key.
type spokenState struct {
	key string
	at  time.Time
	// answered is the track key the answer in now was for, and named whether the page's title named
	// its item then.
	answered      string
	named         bool
	asking        bool
	target, queue string
	now           musicassistant.QueueNow
}

var spoken struct {
	mu sync.Mutex
	s  spokenState
}

// spokenFind is this device's Music Assistant player and its queue; empty without one. It asks Home
// Assistant; a variable for the tests.
var spokenFind = func() (target, queue string) {
	t := libFindPlayer("")
	if t == "" {
		return "", ""
	}
	return t, libQueueOf(t)
}

// spokenWord is whether an audiobook or a podcast episode plays on this device, as Music Assistant last
// said, and how fast. It does not wait for an answer: one is asked for when the track (rd) has changed
// or the last is old, and the screen is woken when it comes. A player followed from another room, and
// a device without Music Assistant, never have one.
//
// What Music Assistant says must be what the page shows: its queue playing while the page says playing
// (paused while paused), and its item the one named. A book left paused on the queue while the device
// plays a station of its own is not the station. Until the answer for a new title comes, the last one
// stands, as far as the title goes: a book's title can change at a chapter's start, and a tap on back
// meanwhile must not jump to another book.
func (d *Display) spokenWord(rd home.Radio, paused bool, now time.Time) (bool, float64) {
	c, set := libClient()
	if !set || rd.Followed || rd.Title == "" {
		return false, 1
	}
	key := rd.Title + "\x00" + rd.Now
	spoken.mu.Lock()
	defer spoken.mu.Unlock()
	s := &spoken.s
	if s.key != key {
		s.key, s.at = key, time.Time{} // asked again at once; the last answer stands until then
	}
	if !s.asking && now.Sub(s.at) >= spokenEvery {
		s.asking = true
		go func() {
			target, queue := spokenFind()
			var n musicassistant.QueueNow
			if queue != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				var err error
				n, err = c.Now(ctx, queue)
				cancel()
				if err != nil {
					slog.Warn("now playing: what the music library plays", "err", err)
				}
			}
			spoken.mu.Lock()
			if spoken.s.key == key {
				// An answer for a track gone since is not this one's: the next frame asks again.
				spoken.s.target, spoken.s.queue, spoken.s.now, spoken.s.at = target, queue, n, time.Now()
				spoken.s.answered = key
			}
			spoken.s.asking = false
			spoken.mu.Unlock()
			d.wake()
		}()
	}
	q := s.now
	if s.answered == key {
		s.named = spokenNames(q, rd.Title)
	}
	want := "playing"
	if paused {
		want = "paused"
	}
	on := q.SpokenWord() && q.Active && q.State == want && s.named
	return on, max(q.Speed, 0.5)
}

// spokenNames is whether title, what the page shows playing, names the queue's item: its name or the
// queue's name for it, either holding the other (a chapter's title can carry the book's, or the other
// way round), ignoring case.
func spokenNames(q musicassistant.QueueNow, title string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	if t == "" {
		return false
	}
	for _, n := range []string{q.Name, q.ItemName} {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" && (strings.Contains(n, t) || strings.Contains(t, n)) {
			return true
		}
	}
	return false
}

// spokenWhere is the player and queue the last answer was for.
func spokenWhere() (target, queue string) {
	spoken.mu.Lock()
	defer spoken.mu.Unlock()
	return spoken.s.target, spoken.s.queue
}

// spokenSkipBy moves the book or episode playing on by seconds, or back for a negative number, through
// Music Assistant's queue. Only when the token may not (the queue's controls need the queues' control
// scope) is Home Assistant asked to seek from where the player says it is: any other failure (a skip
// past the end, a queue no longer playing, an answer that came too late for a skip that was made) is
// left alone, as a seek then could go past the end, move something else, or skip twice.
func spokenSkipBy(seconds int) {
	c, set := libClient()
	target, queue := spokenWhere()
	if !set || queue == "" {
		return
	}
	slog.Info("now playing: skip", "seconds", seconds)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := c.Skip(ctx, queue, seconds)
		if err == nil {
			return
		}
		if !musicassistant.IsForbidden(err) {
			slog.Warn("now playing: skip", "err", err)
			return
		}
		slog.Warn("now playing: skip through Home Assistant instead", "err", err)
		st, serr := libPlayerState(target)
		if serr != nil {
			return
		}
		if pos, ok := libPositionOf(st.Attributes, st.State, time.Now()); ok {
			libTap(dashboard.Action{Entity: target, Service: "media_player.media_seek", Data: map[string]any{"seek_position": float64(max(int(pos)+seconds, 0))}})
		}
	}()
}

// spokenNextSpeed is the speed after now among spokenSpeeds, back to the first after the last.
func spokenNextSpeed(now float64) float64 {
	for _, s := range spokenSpeeds {
		if s > now+0.01 {
			return s
		}
	}
	return spokenSpeeds[0]
}

// spokenSpeedUp sets the next playback speed for the book or episode playing. Music Assistant alone
// has one (Home Assistant has none to fall back on), so a token that may not set it leaves it as it was.
func (d *Display) spokenSpeedUp() {
	c, set := libClient()
	spoken.mu.Lock()
	queue, key, speed := spoken.s.queue, spoken.s.key, spoken.s.now.Speed
	spoken.mu.Unlock()
	if !set || queue == "" {
		return
	}
	next := spokenNextSpeed(speed)
	slog.Info("now playing: playback speed", "speed", next)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.SetSpeed(ctx, queue, next); err != nil {
			slog.Warn("now playing: playback speed", "forbidden", musicassistant.IsForbidden(err), "err", err)
			return
		}
		spoken.mu.Lock()
		if spoken.s.key == key {
			spoken.s.now.Speed = next
		}
		spoken.mu.Unlock()
		d.wake()
	}()
}

// speedWord is a playback speed as its button shows it: 1×, 1.25×.
func speedWord(speed float64) string {
	return strconv.FormatFloat(speed, 'f', -1, 64) + "×"
}

// speedButton is Now Playing's speed button for word, level with Done: at the right end of the foot on
// the modern page, whose Library button is beside the star; on the classic page, whose Library button
// has the right end, to that button's left, or above it when the three buttons leave no room there.
func (r *renderer) speedButton(word, libraryWord string, modern bool) image.Rectangle {
	done := r.doneButton()
	face := r.faces().button
	// As wide as the widest speed, whichever one is on it: sized to its own word, 0.75× and 1.25× made
	// it wide enough to move above Library and back as the speed went round (techo5#613).
	w := r.s(84)
	for _, sp := range spokenSpeeds {
		w = max(w, r.width(face, speedWord(sp))+2*r.s(22))
	}
	w = max(w, r.width(face, word)+2*r.s(22))
	if modern {
		return image.Rect(r.w-r.margin-w, done.Min.Y, r.w-r.margin, done.Max.Y)
	}
	right := r.w - r.margin
	if libraryWord != "" {
		right = r.libraryButton(libraryWord, false).Min.X - r.s(12)
	}
	b := image.Rect(right-w, done.Min.Y, right, done.Max.Y)
	if _, _, next := r.transportButtons(); b.Min.X < next.Max.X+r.s(12) {
		lib := r.libraryButton(libraryWord, false)
		b = image.Rect(lib.Max.X-w, done.Min.Y-done.Dy()-r.s(12), lib.Max.X, done.Min.Y-r.s(12))
	}
	return b
}

// drawSpeed draws the speed button, if the page has one.
func (r *renderer) drawSpeed(s scene) {
	if !s.spoken {
		return
	}
	word := speedWord(s.speed)
	b := r.speedButton(word, s.libraryWord, s.modernMusic)
	col := r.buttonFace(b, float64(b.Dy())/2, btnSecondary)
	face := r.faces().button
	r.text(face, word, b.Min.X+(b.Dx()-r.width(face, word))/2, b.Min.Y+b.Dy()/2+r.s(8), col)
}

// seekBack and seekForward are how far the buttons on a song or a podcast with no spoken-word skip go, in
// seconds: back a little, to hear a missed line again, and forward more, to get past a stretch. These are
// the usual pair of podcast apps.
const (
	seekBack    = 15
	seekForward = 30
)

// seekable is whether the track on the page can be moved through from here: it is on this device's own
// Music Assistant queue (Music Assistant answered for it, as spokenWord asks) and doing what the page
// says, and it is not a radio stream, which has no length to move in. The queue is what the skip is sent
// to (spokenSkipBy).
func seekable(rd home.Radio, paused bool) bool {
	if rd.Followed || rd.Title == "" {
		return false
	}
	key := rd.Title + "\x00" + rd.Now
	spoken.mu.Lock()
	defer spoken.mu.Unlock()
	s := spoken.s
	if s.key != key || s.answered != key || s.queue == "" {
		return false
	}
	want := "playing"
	if paused {
		want = "paused"
	}
	q := s.now
	return q.Active && q.State == want && spokenNames(q, rd.Title) && q.MediaType != "" && q.MediaType != "radio"
}

// seekButtons are the back and forward buttons for a song: under the three on the modern page, which has
// the room below them, and above the Library button at the right of the classic page's foot, where the
// speed button goes for spoken word (the two are never up together).
func (r *renderer) seekButtons(modern bool) (back, forward image.Rectangle) {
	w, h, gap := r.s(96), r.s(46), r.s(14)
	if modern {
		_, details := r.modernMusicLayout()
		b, _, _ := r.musicButtons(true)
		x := details.Min.X + (details.Dx()-2*w-gap)/2
		y := b.Max.Y + r.s(10)
		return image.Rect(x, y, x+w, y+h), image.Rect(x+w+gap, y, x+2*w+gap, y+h)
	}
	y := r.doneButton().Min.Y - r.s(12) - h
	forward = image.Rect(r.w-r.margin-w, y, r.w-r.margin, y+h)
	return forward.Add(image.Pt(-(w + gap), 0)), forward
}

// drawSeek draws the back and forward buttons, if the page has them.
func (r *renderer) drawSeek(s scene) {
	if !s.seek {
		return
	}
	back, forward := r.seekButtons(s.modernMusic)
	for _, b := range []struct {
		r    image.Rectangle
		icon string
	}{{back, "rewind-15"}, {forward, "fast-forward-30"}} {
		if s.modernMusic {
			// The modern page's own look: the dark fill of its previous and next buttons.
			surface, ink := color.RGBA{32, 42, 54, 255}, color.RGBA{242, 246, 250, 255}
			r.roundRect(b.r, b.r.Dy()/2, r.pressFill(b.r, surface, ink))
			r.iconIn(b.r, b.icon, 28, ink)
			continue
		}
		col := r.buttonFace(b.r, float64(b.r.Dy())/2, btnSecondary)
		r.iconIn(b.r, b.icon, 28, col)
	}
}

// skipIcons are the back and forward buttons' icons: 30 seconds for spoken word, the track otherwise.
func skipIcons(spokenWord bool) (back, next string) {
	if spokenWord {
		return "rewind-30", "fast-forward-30"
	}
	return "skip-previous", "skip-next"
}
