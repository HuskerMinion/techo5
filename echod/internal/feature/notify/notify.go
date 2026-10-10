// Package notify puts a notification from Home Assistant on this device: words in a card in the middle
// of the screen, or words over a picture on the camera page. It is the device's own, like every other
// action: an automation reaches several devices by calling each one's.
//
// One is up at a time, and a new one replaces it. It chimes as it comes up, except at night and in
// quiet hours, and it tells Home Assistant when it is shown and how it went (esphome.techo5_notification),
// so an automation can follow up on one nobody saw.
package notify

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	component.Register(component.Device, Get(), component.Order(84))
}

// Event is the name of the Home Assistant event a notification fires.
const Event = "esphome.techo5_notification"

const (
	// untilTapped is how long a notification asked for with no time stays: a year, which a tap ends
	// long before. It is the camera page's own "until tapped", so the page shows no countdown for it.
	untilTapped = 365 * 24 * time.Hour

	longest = 24 * time.Hour // a time that was asked for is held to a day

	// maxTitle and maxMessage are as much as a card holds, in bytes: a heading and three lines.
	maxTitle   = 40
	maxMessage = 240

	tickEvery = time.Second
)

// Kind is what a notification shows: words in a card, or words over a picture.
type Kind string

const (
	Text    Kind = "text"
	Picture Kind = "picture"
)

// Notification is the one up now.
type Notification struct {
	ID      string
	Kind    Kind
	Title   string // "" for none
	Message string
	Icon    string // an mdi icon name, "" for none
	Until   time.Time
}

// pictures is the camera page, as a picture notification uses it (home.Feature).
type pictures interface {
	ShowPicture(src string, c home.Caption, d time.Duration, sound string) error
	PictureUp(id string) bool
	HideCamera()
}

type Feature struct {
	// Changed fires when a notification comes up or goes, so the screen redraws.
	Changed hook.Hook[struct{}]

	mu  sync.Mutex
	up  *Notification
	seq int

	// order keeps one notification's showing and putting from interleaving with another's. It is held
	// across the call to the camera page, which mu never is.
	order sync.Mutex

	actions []*esphome.Action

	// The world, replaced in tests: the clock, the camera page (nil with no screen), the chime, the
	// voice (Home Assistant's, through this device's assist satellite, as a reminder's), whether to keep
	// quiet, and Home Assistant's event bus.
	now    func() time.Time
	pics   pictures
	chime  func()
	say    func(words string)
	hushed func() bool
	fire   func(component.Event)
}

var (
	once   sync.Once
	shared *Feature

	nightMu sync.Mutex
	nightFn = func(time.Time) bool { return false }
)

func Get() *Feature {
	once.Do(func() {
		var pics pictures
		if hasScreen {
			pics = home.Get()
		}
		shared = newFeature(pics)
	})
	return shared
}

// SetNight hands over the screen's idea of night, which is the display's to decide (night hours, Home
// Assistant's night, the switch over either). A Dot has no night and keeps the default.
func SetNight(f func(time.Time) bool) {
	nightMu.Lock()
	nightFn = f
	nightMu.Unlock()
}

// hushedNow is whether a notification comes up without its chime: at night, and in quiet hours.
func hushedNow() bool {
	nightMu.Lock()
	night := nightFn
	nightMu.Unlock()
	return config.Quiet() || night(time.Now())
}

func newFeature(pics pictures) *Feature {
	f := &Feature{
		now:    time.Now,
		pics:   pics,
		chime:  playChime,
		say:    remind.Say,
		hushed: hushedNow,
		fire:   func(e component.Event) { component.Fire.Emit(e) },
	}
	// Home Assistant registers an action's arguments as a closed set, all required, so these lists are
	// fixed for good: an optional argument is passed as "" or 0, and one added later would break every
	// automation already calling the action.
	f.actions = []*esphome.Action{
		{
			Name: "notify",
			Args: []esphome.Arg{
				{Name: "message", Type: esphome.ArgString},
				{Name: "title", Type: esphome.ArgString},
				{Name: "icon", Type: esphome.ArgString},
				{Name: "chime", Type: esphome.ArgString},
				{Name: "speak", Type: esphome.ArgString},
				{Name: "seconds", Type: esphome.ArgInt},
			},
			Run: func(c esphome.Call) (any, error) {
				return nil, f.Notify(c.String("message"), c.String("title"), c.String("icon"), c.String("chime"), c.String("speak"), c.Int("seconds"))
			},
		},
		{
			Name: "notify_picture",
			Args: []esphome.Arg{
				{Name: "message", Type: esphome.ArgString},
				{Name: "title", Type: esphome.ArgString},
				{Name: "picture", Type: esphome.ArgString},
				{Name: "chime", Type: esphome.ArgString},
				{Name: "speak", Type: esphome.ArgString},
				{Name: "sound", Type: esphome.ArgString},
				{Name: "seconds", Type: esphome.ArgInt},
			},
			Run: func(c esphome.Call) (any, error) {
				return nil, f.NotifyPicture(c.String("message"), c.String("title"), c.String("picture"), c.String("chime"), c.String("speak"), c.String("sound"), c.Int("seconds"))
			},
		},
	}
	return f
}

func (f *Feature) Name() string { return "notifications" }

func (f *Feature) Actions() []*esphome.Action { return f.actions }

// Notify puts words up in a card. chime "off" puts it up without its chime, and speak "on" has the
// words said aloud as well.
func (f *Feature) Notify(message, title, icon, chime, speak string, seconds int) error {
	message = clean(message, maxMessage)
	if message == "" {
		return errors.New("notify: the message is empty")
	}
	// Without this, a text notification could be put between a picture's view going up and the picture
	// being put, and then the picture's view would take the page down for the wrong one.
	f.order.Lock()
	defer f.order.Unlock()
	f.put(Notification{
		ID: f.nextID(), Kind: Text, Title: clean(title, maxTitle), Message: message,
		Icon: strings.TrimSpace(icon), Until: f.now().Add(lasting(seconds)),
	}, chimeOff(chime), speakOn(speak))
	return nil
}

// NotifyPicture puts words up over a picture on the camera page, or the picture alone when there are
// none: a doorbell's camera says enough by itself. The picture is checked on every device, so a Dot
// refuses what a Show would. chime "off" puts it up without its chime, speak "on" has its words said
// aloud as well, and sound is a camera's own audio as home_show_camera_sound takes it ("on", "off", or
// anything else for the device's Camera sound setting).
func (f *Feature) NotifyPicture(message, title, picture, chime, speak, sound string, seconds int) error {
	message, picture = clean(message, maxMessage), strings.TrimSpace(picture)
	if err := home.CheckPicture(picture); err != nil {
		return err
	}
	d := lasting(seconds)
	// Two at once could otherwise show A, show B, put B, put A, leaving B's view up under A's
	// notification, so the whole sequence goes one at a time.
	f.order.Lock()
	defer f.order.Unlock()
	n := Notification{ID: f.nextID(), Kind: Picture, Title: clean(title, maxTitle), Message: message, Until: f.now().Add(d)}
	// The view first, then the notification: the ticker counts a picture as over when its view is, and
	// must never look between the two.
	if f.pics != nil {
		if err := f.pics.ShowPicture(picture, home.Caption{ID: n.ID, Title: n.Title, Message: n.Message}, d, sound); err != nil {
			return err
		}
	}
	f.put(n, chimeOff(chime), speakOn(speak))
	return nil
}

// chimeOff is whether an action asked for no chime: "off", as home_show_camera_sound takes its sound.
// Anything else, an empty string included, leaves the chime to the hour.
func chimeOff(chime string) bool { return strings.EqualFold(strings.TrimSpace(chime), "off") }

// speakOn is whether an action asked for its words said aloud: "on". Anything else, an empty string
// included, keeps them to the screen, which is what a notification was before it could speak.
func speakOn(speak string) bool { return strings.EqualFold(strings.TrimSpace(speak), "on") }

// spoken is what a notification says aloud: its title and its message as one sentence would carry
// them, or the one of them there is.
func spoken(n Notification) string {
	switch {
	case n.Title == "":
		return n.Message
	case n.Message == "":
		return n.Title
	case strings.ContainsAny(n.Title[len(n.Title)-1:], ".!?"):
		return n.Title + " " + n.Message
	}
	return n.Title + ". " + n.Message
}

func (f *Feature) nextID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	return strconv.Itoa(f.seq)
}

// put makes n the one up, ending whatever was. silent leaves its chime out whatever the hour, and aloud
// has its words said after the chime.
func (f *Feature) put(n Notification, silent, aloud bool) {
	f.mu.Lock()
	old := f.up
	f.up = &n
	f.mu.Unlock()
	if old != nil {
		// Words replacing a picture take the picture's view down; a picture replacing a picture has
		// already replaced the view.
		if old.Kind == Picture && n.Kind == Text && f.pics != nil && f.pics.PictureUp(old.ID) {
			f.pics.HideCamera()
		}
		f.report(*old, "expired")
	}
	f.report(n, "shown")
	// Night and quiet hours keep a notification to the screen: no chime and no words, since it is not
	// an alarm and nobody set it for now.
	hushed := f.hushed()
	chime, words := !silent && !hushed, ""
	if aloud && !hushed {
		words = spoken(n)
	}
	slog.Info("notification", "kind", n.Kind, "title", n.Title, "until", n.Until.Format(time.Kitchen),
		"chime", chime, "spoken", words != "", "hushed", hushed)
	if chime || words != "" {
		// One after the other in one goroutine: the chime has the speaker until it is done, and words
		// over it would be lost under it.
		safe.Go("notification", func() {
			if chime {
				f.chime()
			}
			if words != "" {
				f.say(words)
			}
		})
	}
	f.Changed.Emit(struct{}{})
}

// Card is the notification drawn as a card: one of words, up now.
func (f *Feature) Card() (Notification, bool) {
	n, up := f.Showing()
	return n, up && n.Kind == Text
}

// Showing is the notification up now, of either kind.
func (f *Feature) Showing() (Notification, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.up == nil || !f.now().Before(f.up.Until) {
		return Notification{}, false
	}
	return *f.up, true
}

// Dismiss takes the notification down because somebody tapped it, and says whether there was one.
func (f *Feature) Dismiss() bool {
	f.mu.Lock()
	n := f.up
	f.up = nil
	f.mu.Unlock()
	if n == nil {
		return false
	}
	if n.Kind == Picture && f.pics != nil && f.pics.PictureUp(n.ID) {
		f.pics.HideCamera()
	}
	f.report(*n, "dismissed")
	f.Changed.Emit(struct{}{})
	return true
}

// Run ends notifications whose time is up, here rather than from the screen's frames, so Home
// Assistant hears it with the screen asleep too.
func (f *Feature) Run(ctx context.Context) error {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			f.tick(f.now())
		}
	}
}

// tick ends the notification up if it is over: words when their time is, a picture when its view is
// (which has its own time, counted from the picture arriving, and can be taken down other ways).
func (f *Feature) tick(now time.Time) {
	f.mu.Lock()
	n := f.up
	if n == nil {
		f.mu.Unlock()
		return
	}
	over := !now.Before(n.Until)
	if n.Kind == Picture && f.pics != nil {
		over = !f.pics.PictureUp(n.ID)
	}
	if !over {
		f.mu.Unlock()
		return
	}
	f.up = nil
	f.mu.Unlock()
	f.report(*n, "expired")
	f.Changed.Emit(struct{}{})
}

func (f *Feature) report(n Notification, what string) {
	f.fire(component.Event{Name: Event, Data: map[string]string{
		"event": what, "title": n.Title, "message": n.Message, "kind": string(n.Kind),
		"device": config.Get().Device.Name,
	}})
}

// lasting is how long a notification stays: its own seconds, a day at most, or until somebody taps it
// when it was given none. A notification nobody gave a time to is one somebody is meant to see.
func lasting(seconds int) time.Duration {
	if seconds <= 0 {
		return untilTapped
	}
	if seconds > int(longest/time.Second) {
		return longest
	}
	return time.Duration(seconds) * time.Second
}

// clean is words as a card shows them: one line, no control characters, at most n bytes and never cut
// through a character. They are drawn and never read as anything.
func clean(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		s = strings.TrimSpace(s[:n])
	}
	return s
}
