// Package setup is the device's own setup page: the few settings that are miserable to type on a
// five-inch screen, and the only way to configure a device that has no screen at all.
//
// It is off until somebody asks for it — from the device's settings screen, from Home Assistant, or
// on a Dot by holding the action button — and it turns itself off again seven minutes after the last
// request, or an hour after it was opened, whichever comes first. While it is off the path is not
// served and, with nothing else switched on, the port is not listening.
//
// Getting in is a press on the device rather than a password: a browser that asks is told to press
// the action button, the device says so too, and the press authorizes that one browser. It proves
// somebody is standing at the device, it cannot be read off a photograph, and it works on a Dot,
// which has no screen to show a code on. See docs/setup-page-design.md.
package setup

import (
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

func init() {
	component.Register(component.Network, Get(), component.Order(72))
}

const (
	// idle closes the page when nothing has been asked of it for this long; life is the longest it
	// stays open however busy it is.
	idle = 7 * time.Minute
	life = time.Hour

	// pressWait is how long a browser waiting to be let in has to be answered by a press on the
	// device, and holdOpen is how long the action button must be held to open the page on a device
	// with no screen to ask from.
	pressWait = 60 * time.Second
	holdOpen  = buttons.LongPress

	// pressGrace is how long after the page has taken a press it still says the press was its own.
	// The button's listeners run in no fixed order, so whoever asks whether a press belonged to the
	// page may well be asking after the page has already answered it and stopped waiting. Every
	// listener sees the same press in the same instant, so this only has to outlast that instant, and
	// has to be far short of anybody's next press.
	pressGrace = 50 * time.Millisecond

	// sessions is how many browsers may be let in at once. A setup page is used by one person at a
	// device; this is a bound, not a feature.
	sessions = 4

	// tries is how many times a browser may ask and not be answered by a press before asking is
	// refused for coolOff. Somebody at the device presses within the minute; anything else is either
	// a mistake or a machine, and both can wait.
	tries   = 5
	coolOff = 10 * time.Minute
)

type Feature struct {
	sw *esphome.Switch

	mu sync.Mutex
	// openUntil is when the page closes for want of use, and openedAt when it was switched on, for
	// the hour it may stay open at the very most.
	openUntil time.Time
	openedAt  time.Time

	// heldOpen is whether the press being made right now is what opened the page, so that a press that
	// goes on to mean something else can take it back down (button).
	heldOpen bool

	// waiting is the browser asking to be let in: its nonce, and when it gives up. Only one at a
	// time, so a press can never let in a browser the presser did not mean.
	waiting    string
	waitingEnd time.Time

	// took is when a press was last taken by the page, so that the rest of the button's listeners can
	// be told the press was not theirs even though the waiting is already over by the time they ask.
	took time.Time

	// live are the sessions let in, by their cookie, each with when it was last used.
	live map[string]time.Time

	// unanswered counts the askings that no press answered, and shutUntil is when asking is allowed
	// again once there have been too many.
	unanswered int
	shutUntil  time.Time

	// Changed fires when the page opens or closes, or when a browser starts or stops waiting, so the
	// screen can say what is going on.
	Changed hook.Hook[struct{}]
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		shared = build()
		buttons.Get().Events.Listen(shared.button)
		// The press that lets a browser in is not also a question for the assistant. A screen consumes
		// it in the "let it in?" card it is a tap on; a device with no screen has only the button, so the
		// conversation has to be told that this press is already spoken for.
		voice.OwnsPress(shared.TookPress)
		web.Handle("/setup", "Setup", shared.On, shared.serve)
		web.Handle("/setup/", "", shared.On, shared.serve)
		// The press is the whole port's authorization, not only this page's: a screenshot is one
		// thing to let the network have and driving the screen from it is another.
		web.Guard(shared.letInRequest)
	})
	return shared
}

// build is the feature on its own, without the hardware and the web port wired to it, so a test can
// have one.
func build() *Feature {
	f := &Feature{live: map[string]time.Time{}}
	f.sw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "setup_page",
			Name:     "Setup page",
			Icon:     "mdi:tools",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) {
			if on {
				f.Open()
				return
			}
			f.Close()
		},
	}
	return f
}

func (f *Feature) Name() string { return "setup page" }

func (f *Feature) Entities() []esphome.Entity { return []esphome.Entity{f.sw} }

// On is whether the page is being served at all.
func (f *Feature) On() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.on(time.Now())
}

func (f *Feature) on(now time.Time) bool {
	return now.Before(f.openUntil) && now.Before(f.openedAt.Add(life))
}

// Open puts the page up. Asking again while it is up puts the idle time back to the start, so a
// switch flicked twice does not shorten it.
func (f *Feature) Open() {
	now := time.Now()
	f.mu.Lock()
	if !f.on(now) {
		f.openedAt = now
		slog.Info("setup page open", "idle", idle, "at most", life)
	}
	f.openUntil = now.Add(idle)
	f.mu.Unlock()

	f.sw.Set(true)
	web.Wake()
	f.Changed.Emit(struct{}{})
}

// Close takes it down and forgets every session with it.
func (f *Feature) Close() {
	f.mu.Lock()
	was := f.on(time.Now())
	f.openUntil, f.openedAt = time.Time{}, time.Time{}
	f.waiting, f.waitingEnd = "", time.Time{}
	f.heldOpen = false
	clear(f.live)
	f.mu.Unlock()

	if was {
		slog.Info("setup page closed")
	}
	f.sw.Set(false)
	web.Wake()
	f.Changed.Emit(struct{}{})
}

// used puts the idle time back to the start: typing on the page keeps it open.
func (f *Feature) used() {
	now := time.Now()
	f.mu.Lock()
	if f.on(now) {
		f.openUntil = now.Add(idle)
		// Somebody is on the page, so the press that opened it meant this after all.
		f.heldOpen = false
	}
	f.mu.Unlock()
}

// Waiting is whether a browser is asking to be let in, for the screen to say so and for the light
// ring to pulse on a device with no screen.
func (f *Feature) Waiting() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.waiting != "" && time.Now().Before(f.waitingEnd)
}

// TookPress reports whether the press being handled now belongs to the setup page rather than to
// whatever the action button usually does. Everything else listening to that button asks this before
// acting: a press made to let a browser in is not also a question for the assistant, and on a device
// with no screen there is nothing else to tell the button so.
//
// It is true while a browser is waiting and for pressGrace after the press was taken, because the
// listeners run in no fixed order and this may be asked either side of the page answering.
func (f *Feature) TookPress() bool {
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.waiting != "" && now.Before(f.waitingEnd) {
		return true
	}
	return !f.took.IsZero() && now.Sub(f.took) < pressGrace
}

// button is the action button: a hold opens the page on a device that has no other way to ask, and a
// tap answers a browser that is waiting to be let in.
//
// A longer hold on the Dot is Bluetooth pairing, and every one of those passes through this hold on
// its way there. Opening the page for somebody who was reaching for pairing puts a device on the
// network that nobody asked to put there, so the page is taken down again when the same press turns
// out to have been the longer one. Only a press that opened it is undone: a page already up, or one
// opened any other way, is left alone.
func (f *Feature) button(e buttons.Event) {
	if e.Name != buttons.Action {
		return
	}
	switch e.Kind {
	case buttons.Hold:
		if !f.On() {
			slog.Info("setup page opened by holding the action button")
			f.Open()
			f.mu.Lock()
			f.heldOpen = true
			f.mu.Unlock()
		}
	case buttons.LongHold:
		f.mu.Lock()
		held := f.heldOpen
		f.heldOpen = false
		f.mu.Unlock()
		if held {
			slog.Info("setup page closed again: the press went on to be something else")
			f.Close()
		}
	case buttons.Tap:
		f.press()
	}
}

// press lets in the browser that is waiting, if one is. It answers whoever was already asking rather
// than the next to ask: a press cannot be saved up.
func (f *Feature) press() bool {
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.on(now) || f.waiting == "" || now.After(f.waitingEnd) {
		return false
	}
	if len(f.live) >= sessions {
		// Forget the oldest rather than refuse: the newest press is the one somebody just made.
		oldest, at := "", time.Time{}
		for k, t := range f.live {
			if at.IsZero() || t.Before(at) {
				oldest, at = k, t
			}
		}
		delete(f.live, oldest)
	}
	f.live[f.waiting] = now
	f.waiting, f.waitingEnd = "", time.Time{}
	f.took = now
	f.unanswered = 0 // a press clears the run
	slog.Info("setup page: a browser was let in by a press on the device")
	return true
}

// Answer is the press on a device whose press is a tap: yes lets the browser in, no sends it away
// at once rather than leaving it to time out. Refusing counts like any unanswered asking, so a
// browser that keeps asking and keeps being refused runs into the same cool-off.
func (f *Feature) Answer(allow bool) bool {
	if allow {
		return f.press()
	}
	f.mu.Lock()
	had := f.waiting != ""
	f.waiting, f.waitingEnd = "", time.Time{}
	f.mu.Unlock()
	if had {
		slog.Info("setup page: a browser was refused at the device")
		f.Changed.Emit(struct{}{})
	}
	return had
}

// await starts a browser waiting to be let in and hands back the cookie it will hold if the press
// comes. Only one browser waits at a time: a second is refused, so that whoever presses knows which
// browser they are letting in. Asking over and over without a press stops being allowed for a while.
func (f *Feature) await() (token string, ok bool) {
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.on(now) || now.Before(f.shutUntil) {
		return "", false
	}
	if f.waiting != "" && now.Before(f.waitingEnd) {
		return "", false
	}
	// Every asking counts until a press clears the run, so a browser cannot sit there asking.
	f.unanswered++
	if f.unanswered >= tries {
		f.shutUntil = now.Add(coolOff)
		f.waiting, f.waitingEnd = "", time.Time{}
		slog.Warn("setup page: too many askings with no press; not asking again for a while",
			"tries", f.unanswered, "for", coolOff)
		return "", false
	}
	f.waiting, f.waitingEnd = newToken(), now.Add(pressWait)
	return f.waiting, true
}

// ShutOut is whether asking is refused for the moment, for the page to say why.
func (f *Feature) ShutOut() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Now().Before(f.shutUntil)
}

// letIn is whether this cookie is a session that has been let in, and keeps it alive.
func (f *Feature) letIn(token string) bool {
	if token == "" {
		return false
	}
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.on(now) {
		return false
	}
	if _, ok := f.live[token]; !ok {
		return false
	}
	f.live[token] = now
	return true
}

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
