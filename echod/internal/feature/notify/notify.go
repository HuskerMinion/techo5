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
	// untilTappedCap is how long words or a still picture asked for with no time stay if nobody taps
	// them: long enough for a night's sleep and then some, short enough that one forgotten is not up
	// for ever.
	untilTappedCap = 12 * time.Hour

	// liveWith is how long a live camera asked for with no time stays, as home_show_camera takes 0: a
	// live view polls Home Assistant the whole time it is up.
	liveWith = 30 * time.Second

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
	Tag     string // what the caller called it, carried back on the event; "" for none
	Until   time.Time
}

// pictures is the camera page, as a picture notification uses it (home.Feature).
type pictures interface {
	ShowPicture(src string, c home.Caption, d time.Duration, sound string) error
	PictureUp(id string) bool
	HidePicture(id string) bool
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
	nightFn = nightByHours
)

// nightByHours is night by the night hours in the settings alone. It stands in until the display says
// what night is, so a notification right after boot is not chimed at night.
func nightByHours(t time.Time) bool { return config.InWindow(config.Get().Screen.Night, t) }

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
// Assistant's night, the switch over either). A Dot has no display and keeps the night hours.
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
		chime:  Chime,
		say:    remind.Say,
		hushed: hushedNow,
		// Events go out through an outbox (outbox.go), so nothing here waits on Home Assistant.
		fire: newOutbox(func(e component.Event) { component.Fire.Emit(e) }, outboxSize).post,
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
				{Name: "tag", Type: esphome.ArgString},
				{Name: "seconds", Type: esphome.ArgInt},
			},
			Run: func(c esphome.Call) (any, error) {
				return nil, f.Notify(c.String("message"), c.String("title"), c.String("icon"), c.String("chime"), c.String("speak"), c.String("tag"), c.Int("seconds"))
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
				{Name: "tag", Type: esphome.ArgString},
				{Name: "seconds", Type: esphome.ArgInt},
			},
			Run: func(c esphome.Call) (any, error) {
				return nil, f.NotifyPicture(c.String("message"), c.String("title"), c.String("picture"), c.String("chime"), c.String("speak"), c.String("sound"), c.String("tag"), c.Int("seconds"))
			},
		},
	}
	return f
}

func (f *Feature) Name() string { return "notifications" }

func (f *Feature) Actions() []*esphome.Action { return f.actions }

// Notify puts words up in a card. chime and speak read on and off as sound does (chime is on unless
// off, speak is off unless on), and tag is carried back on the notification's events.
func (f *Feature) Notify(message, title, icon, chime, speak, tag string, seconds int) error {
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
		Icon: strings.TrimSpace(icon), Tag: strings.TrimSpace(tag), Until: f.now().Add(lasting(seconds, false)),
	}, how{silent: !home.ParseOnOff(chime, true), aloud: home.ParseOnOff(speak, false), hushed: f.hushed(), untilDone: seconds <= 0})
	return nil
}

// NotifyPicture puts words up over a picture on the camera page, or the picture alone when there are
// none: a doorbell's camera says enough by itself. The picture is checked on every device, so a Dot
// refuses what a Show would. chime and speak are as Notify takes them, and sound is a camera's own audio
// as home_show_camera_sound takes it (on or off, or anything else for the device's Camera sound
// setting); it stays off at night and in quiet hours.
func (f *Feature) NotifyPicture(message, title, picture, chime, speak, sound, tag string, seconds int) error {
	message, picture = clean(message, maxMessage), strings.TrimSpace(picture)
	if err := home.CheckPicture(picture); err != nil {
		return err
	}
	d := lasting(seconds, home.PictureIsLive(picture))
	// Two at once could otherwise show A, show B, put B, put A, leaving B's view up under A's
	// notification, so the whole sequence goes one at a time.
	f.order.Lock()
	defer f.order.Unlock()
	h := how{silent: !home.ParseOnOff(chime, true), aloud: home.ParseOnOff(speak, false), hushed: f.hushed(), untilDone: seconds <= 0}
	if h.hushed {
		sound = "off"
	}
	n := Notification{ID: f.nextID(), Kind: Picture, Title: clean(title, maxTitle), Message: message, Tag: strings.TrimSpace(tag), Until: f.now().Add(d)}
	// The view first, then the notification: the ticker counts a picture as over when its view is, and
	// must never look between the two.
	if f.pics != nil {
		if err := f.pics.ShowPicture(picture, home.Caption{ID: n.ID, Title: n.Title, Message: n.Message, UntilTapped: seconds <= 0 && !home.PictureIsLive(picture)}, d, sound); err != nil {
			return err
		}
	}
	f.put(n, h)
	return nil
}

// how is what a notification is put up with.
type how struct {
	silent bool // the caller asked for no chime
	aloud  bool // the caller asked for the words said
	hushed bool // night or quiet hours: no chime, no words, no camera sound
	// untilDone ends a notification on a device with no screen once its chime and words are done, when
	// the caller gave it no time: there is nothing to tap, so nothing else would end it.
	untilDone bool
}

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

// put makes n the one up, ending whatever was as replaced.
func (f *Feature) put(n Notification, h how) {
	// The events are posted with the change, under f.mu, so a tap that ends n straight away is
	// reported after n was shown. Posting never waits (outbox.go).
	f.mu.Lock()
	old := f.up
	f.up = &n
	if old != nil {
		f.report(*old, "replaced")
	}
	f.report(n, "shown")
	f.mu.Unlock()
	// Words replacing a picture take the picture's view down, if it is still that picture's; a picture
	// replacing a picture has already replaced the view.
	if old != nil && old.Kind == Picture && n.Kind == Text && f.pics != nil {
		f.pics.HidePicture(old.ID)
	}
	// Night and quiet hours keep a notification to the screen: no chime and no words, since it is not
	// an alarm and nobody set it for now.
	chime, words := !h.silent && !h.hushed, ""
	if h.aloud && !h.hushed {
		words = spoken(n)
	}
	slog.Info("notification", "kind", n.Kind, "title", n.Title, "until", n.Until.Format(time.Kitchen),
		"chime", chime, "spoken", words != "", "hushed", h.hushed)
	ends := h.untilDone && f.pics == nil
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
			if ends {
				f.finish(n.ID)
			}
		})
	} else if ends {
		f.finish(n.ID)
	}
	f.Changed.Emit(struct{}{})
}

// finish ends notification id because there was nothing more to do for it, and does nothing if it is
// not the one up.
func (f *Feature) finish(id string) {
	f.mu.Lock()
	n := f.up
	if n == nil || n.ID != id {
		f.mu.Unlock()
		return
	}
	f.up = nil
	f.report(*n, "expired")
	f.mu.Unlock()
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
	n := f.up
	if n == nil {
		return Notification{}, false
	}
	// A picture is up while its view is, as tick counts it: the view has its own time, counted from the
	// picture arriving.
	if n.Kind == Picture && f.pics != nil {
		return *n, f.pics.PictureUp(n.ID)
	}
	if !f.now().Before(n.Until) {
		return Notification{}, false
	}
	return *n, true
}

// Dismiss takes the notification down because somebody tapped it, and says whether there was one.
func (f *Feature) Dismiss() bool { return f.dismiss("") }

// DismissID takes the notification down only if it is the one with this id, and says whether it did: a
// tap on a picture's view that has gone must not end the notification that came after it.
func (f *Feature) DismissID(id string) bool { return id != "" && f.dismiss(id) }

func (f *Feature) dismiss(id string) bool {
	f.mu.Lock()
	n := f.up
	if n == nil || (id != "" && n.ID != id) {
		f.mu.Unlock()
		return false
	}
	f.up = nil
	f.report(*n, "dismissed")
	f.mu.Unlock()
	if n.Kind == Picture && f.pics != nil {
		f.pics.HidePicture(n.ID)
	}
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
	// Not between a picture's view going up and its notification being put: the old picture's view is
	// gone by then, and ending it here would report it expired when it is being replaced.
	f.order.Lock()
	defer f.order.Unlock()
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
	f.report(*n, "expired")
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// report posts an event about n. Callers hold f.mu, so events are posted in the order the changes
// happen; fire never waits (the outbox), so holding the lock for it costs nothing.
func (f *Feature) report(n Notification, what string) {
	f.fire(component.Event{Name: Event, Data: map[string]string{
		"event": what, "id": n.ID, "tag": n.Tag, "title": n.Title, "message": n.Message, "kind": string(n.Kind),
		"device": config.Get().Device.Name,
	}})
}

// lasting is how long a notification stays: its own seconds, a day at most. With none it is until
// somebody taps it, which is twelve hours at most, or for a live camera 30 seconds. A notification
// nobody gave a time to is one somebody is meant to see.
func lasting(seconds int, live bool) time.Duration {
	switch {
	case seconds <= 0 && live:
		return liveWith
	case seconds <= 0:
		return untilTappedCap
	case seconds > int(longest/time.Second):
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
