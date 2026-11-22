package notify

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Notifications with buttons, and critical ones, modelled on what Home Assistant's phone app does with
// actionable and critical notifications.
//
// A button tap is told to Home Assistant as the notification's "action" event, carrying the button's
// id, and is how the notification ends. A critical notification rings on the bell, through the night
// and quiet hours and at a volume of its own, until it is answered with a button, cleared, run out,
// or quieted (a tap on the card, the stop word, Stop in Home Assistant); quieted, its card stays until
// a button answers it. While one is up, nothing else takes its place but another critical one: a
// notification that comes meanwhile waits, the latest of them only, and shows when it goes.

const (
	// maxButtons is as many buttons as a card holds; a Spot's face draws the first two.
	maxButtons = 3
	// maxButtonLabel is as long as a button's label is, in bytes: one short line.
	maxButtonLabel = 24
	// maxButtonID is as long as a button's id is, in bytes.
	maxButtonID = 64
	// OKID is the id of the button a critical notification is given when it is sent none.
	OKID = "ok"
)

// criticalTone is a critical notification's ring: three short high notes, apart from the alarm's and
// a timer's, so it is not taken for either.
var criticalTone = []speaker.Note{
	{Freq: 1319, Ms: 130}, {Freq: 0, Ms: 70}, {Freq: 1319, Ms: 130}, {Freq: 0, Ms: 70}, {Freq: 1568, Ms: 220},
}

// Button is one a notification carries: what it says, and what Home Assistant hears it called when
// it is tapped. A Label of "" is drawn as "OK" in the screen's language.
type Button struct {
	Label string
	ID    string
}

// parseButtons reads buttons as notify_buttons and notify_critical take them: split by commas or new
// lines, each "Label=id", or "Label" alone for a button whose id is its label. Empty entries are
// skipped. A list with a bad button is refused whole.
func parseButtons(s string) ([]Button, error) {
	var out []Button
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		label, id, found := strings.Cut(part, "=")
		label, id = strings.TrimSpace(label), strings.TrimSpace(id)
		if !found {
			id = label
		}
		if label == "" {
			return nil, fmt.Errorf("notify: the button %q has no label", part)
		}
		if id == "" {
			return nil, fmt.Errorf("notify: the button %q has no id", part)
		}
		if l := clean(label, maxButtonLabel); l != label {
			return nil, fmt.Errorf("notify: the button label %q is longer than %d bytes or has control characters", label, maxButtonLabel)
		}
		if len(id) > maxButtonID {
			return nil, fmt.Errorf("notify: the button id %q is longer than %d bytes", id, maxButtonID)
		}
		out = append(out, Button{Label: label, ID: id})
	}
	if len(out) > maxButtons {
		return nil, fmt.Errorf("notify: %d buttons, at most %d fit", len(out), maxButtons)
	}
	return out, nil
}

// volumeStep is a critical notification's volume, 0 to 100 percent, in the speaker's steps: at least one
// step for any volume above 0, and 0 (the ring volume set on the device) for 0 or less.
func volumeStep(percent int) int {
	if percent <= 0 {
		return 0
	}
	return (min(percent, 100)*config.VolumeSteps + 99) / 100
}

// riseFor is a critical notification's rise, seconds and at most the whole ring: none for 0 or less.
func riseFor(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return min(time.Duration(seconds)*time.Second, ring.RingFor)
}

// lightPercent is a critical notification's screen brightness, 1 to 100 percent: full for 0 or less.
func lightPercent(percent int) int {
	if percent <= 0 {
		return 100
	}
	return min(percent, 100)
}

// buttonActions are the two actions this file adds. Their arguments are fixed for good, as notify's are.
func (f *Feature) buttonActions() []*esphome.Action {
	return []*esphome.Action{
		{
			Name: "notify_buttons",
			Args: []esphome.Arg{
				{Name: "message", Type: esphome.ArgString},
				{Name: "title", Type: esphome.ArgString},
				{Name: "icon", Type: esphome.ArgString},
				{Name: "chime", Type: esphome.ArgString},
				{Name: "speak", Type: esphome.ArgString},
				{Name: "tag", Type: esphome.ArgString},
				{Name: "seconds", Type: esphome.ArgInt},
				{Name: "size", Type: esphome.ArgString},
				{Name: "buttons", Type: esphome.ArgString},
				{Name: "volume", Type: esphome.ArgInt},
			},
			Run: func(c esphome.Call) (any, error) {
				return nil, f.NotifyButtons(c.String("message"), c.String("title"), c.String("icon"), c.String("chime"), c.String("speak"), c.String("tag"), c.Int("seconds"), c.String("size"), c.String("buttons"), c.Int("volume"))
			},
		},
		{
			Name: "notify_critical",
			Args: []esphome.Arg{
				{Name: "message", Type: esphome.ArgString},
				{Name: "title", Type: esphome.ArgString},
				{Name: "icon", Type: esphome.ArgString},
				{Name: "tag", Type: esphome.ArgString},
				{Name: "seconds", Type: esphome.ArgInt},
				{Name: "size", Type: esphome.ArgString},
				{Name: "buttons", Type: esphome.ArgString},
				{Name: "volume", Type: esphome.ArgInt},
				{Name: "volume_rise", Type: esphome.ArgInt},
				{Name: "brightness", Type: esphome.ArgInt},
				{Name: "brightness_rise", Type: esphome.ArgInt},
			},
			Run: func(c esphome.Call) (any, error) {
				return nil, f.NotifyCritical(c.String("message"), c.String("title"), c.String("icon"), c.String("tag"), c.Int("seconds"), c.String("size"), c.String("buttons"),
					c.Int("volume"), c.Int("volume_rise"), c.Int("brightness"), c.Int("brightness_rise"))
			},
		},
	}
}

// NotifyButtons is NotifySized with buttons (parseButtons) and a chime volume of its own, volume percent
// (0 for the chime's usual level, not silent; night, quiet hours and chime off still keep it quiet). A
// tap on a button ends it with an "action" event naming the button; a tap anywhere else on the card puts
// it away, as on any card. With no buttons it is NotifySized with a volume. A Dot has nothing to tap,
// and shows it as notify does.
func (f *Feature) NotifyButtons(message, title, icon, chime, speak, tag string, seconds int, size, buttons string, volume int) error {
	bs, err := parseButtons(buttons)
	if err != nil {
		return err
	}
	message = clean(message, maxMessage)
	if message == "" {
		return errors.New("notify: the message is empty")
	}
	f.order.Lock()
	defer f.order.Unlock()
	f.put(Notification{
		ID: f.nextID(), Kind: Text, Title: clean(title, maxTitle), Message: message,
		Icon: strings.TrimSpace(icon), Tag: strings.TrimSpace(tag), Until: f.now().Add(lasting(seconds, false)),
		Size: ParseSize(size), Buttons: bs,
	}, how{silent: !home.ParseOnOff(chime, true), aloud: home.ParseOnOff(speak, false), hushed: f.hushed(), untilDone: seconds <= 0,
		step: volumeStep(volume)})
	return nil
}

// NotifyCritical puts up a critical notification: a card with buttons ("OK" when none are given) that
// rings on the bell, at volume percent (0 for the ring volume set on the device), through the night and
// quiet hours, and lights the screen at brightness percent (0 for full) while it rings. With
// volume_rise seconds the ring starts at the speaker's volume now and climbs to its own over them, and
// with brightness_rise the screen climbs from its brightness now the same way; 0 is at once for both.
// The ring lasts as long as an alarm's at most; the card stays for seconds (until a button answers it,
// 12 hours at most, with none). On a Dot, which has no buttons, the stop word or the action button ends
// it.
func (f *Feature) NotifyCritical(message, title, icon, tag string, seconds int, size, buttons string, volume, volumeRise, brightness, brightnessRise int) error {
	bs, err := parseButtons(buttons)
	if err != nil {
		return err
	}
	if len(bs) == 0 {
		bs = []Button{{ID: OKID}}
	}
	message = clean(message, maxMessage)
	if message == "" {
		return errors.New("notify: the message is empty")
	}
	f.order.Lock()
	defer f.order.Unlock()
	f.put(Notification{
		ID: f.nextID(), Kind: Text, Title: clean(title, maxTitle), Message: message,
		Icon: strings.TrimSpace(icon), Tag: strings.TrimSpace(tag), Until: f.now().Add(lasting(seconds, false)),
		Size: ParseSize(size), Buttons: bs, Critical: true, step: volumeStep(volume), Brightness: lightPercent(brightness),
		BrightnessRise: riseFor(brightnessRise), volumeRise: riseFor(volumeRise),
	}, how{silent: true})
	return nil
}

// Press is a tap on button i of notification id: Home Assistant hears the button's id, and the
// notification ends. It says whether there was such a button to press.
func (f *Feature) Press(id string, i int) bool {
	f.order.Lock()
	defer f.order.Unlock()
	f.mu.Lock()
	n := f.up
	if n == nil || id == "" || n.ID != id || i < 0 || i >= len(n.Buttons) {
		f.mu.Unlock()
		return false
	}
	f.up = nil
	f.reportAction(*n, "action", n.Buttons[i].ID)
	f.leftUp(*n)
	f.mu.Unlock()
	slog.Info("notification answered", "id", n.ID, "button", i)
	f.Changed.Emit(struct{}{})
	return true
}

// Tap is a finger on notification id's card, off its buttons. A critical one is quieted and stays up,
// since only a button answers it; any other is put away, as DismissID. It says whether it did either.
func (f *Feature) Tap(id string) bool {
	f.mu.Lock()
	n := f.up
	critical := n != nil && n.ID == id && n.Critical
	f.mu.Unlock()
	if critical {
		return f.quiet(id)
	}
	return f.DismissID(id)
}

// CriticalUp is whether a critical notification is up: the screen lets nothing hold it back.
func (f *Feature) CriticalUp() bool {
	n, up := f.Showing()
	return up && n.Critical
}

// holdsBack is whether old, the notification up, keeps a new one n from taking its place: a critical one
// does while it rings. Quieted, by a button, the stop word or Stop, or rung out, it is a card like any
// other, so a picture or words that come later replace it rather than being refused or kept waiting for
// up to the card's twelve hours. Held with f.mu.
func (f *Feature) holdsBack(old *Notification, n Notification) bool {
	return old != nil && old.Critical && !n.Critical && f.now().Before(old.Until) &&
		f.ringing != nil && f.ringing.id == old.ID
}

// criticalRinging is whether a critical notification is up and still ringing.
func (f *Feature) criticalRinging() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.up != nil && f.up.Critical && f.ringing != nil && f.ringing.id == f.up.ID
}

// criticalRing is the bell a critical notification rings.
type criticalRing struct {
	id      string
	stop    func()
	started time.Time
}

// startRing rings for critical notification n, unless it has gone by the time the bell is up.
func (f *Feature) startRing(n Notification) {
	started := time.Now()
	stop := f.ring(n.Title, n.step, n.volumeRise, func() { f.rang(n.ID, started) })
	f.mu.Lock()
	if f.up == nil || f.up.ID != n.ID {
		f.mu.Unlock()
		stop()
		return
	}
	if old := f.ringing; old != nil {
		old.stop()
	}
	f.ringing = &criticalRing{id: n.ID, stop: stop, started: started}
	f.mu.Unlock()
}

// rang is the bell of critical notification id ending by itself: it rang out, or a button on the device
// quieted it and the snooze offer ran out. The card stays on a screen; a Dot, with nothing else to end
// it, ends it: expired if it rang out, dismissed if somebody stopped it.
func (f *Feature) rang(id string, started time.Time) {
	f.mu.Lock()
	r := f.ringing
	if r == nil || r.id != id {
		f.mu.Unlock()
		return // stopped from here, which has dealt with the rest
	}
	f.ringing = nil
	f.mu.Unlock()
	if f.pics != nil {
		// Rung out, the card stays, and what waited behind the bell comes up over it.
		safe.Go("notify unpark", f.unpark)
	}
	if f.pics == nil {
		why := "dismissed"
		if time.Since(started) >= ring.RingFor-2*time.Second {
			why = "expired"
		}
		f.end(why, func(n *Notification) bool { return n.ID == id })
	}
}

// silence is the stop word, Stop in Home Assistant, or the action button's second press, reaching the
// critical notification's bell (ring.Silences): the bell stops. On a screen the card stays for a
// button; a Dot ends it as dismissed.
func (f *Feature) silence() bool {
	f.mu.Lock()
	r := f.ringing
	f.mu.Unlock()
	if r == nil {
		return false
	}
	return f.quiet(r.id)
}

// quiet stops critical notification id's bell, if it rings, and on a Dot ends it as dismissed.
func (f *Feature) quiet(id string) bool {
	f.mu.Lock()
	r := f.ringing
	if r == nil || r.id != id {
		f.mu.Unlock()
		return false
	}
	f.ringing = nil
	f.mu.Unlock()
	r.stop()
	slog.Info("critical notification quieted", "id", id)
	if f.pics != nil {
		// Quieted, the card stays, and what waited behind the bell comes up over it, as anything sent
		// from now on would (holdsBack).
		safe.Go("notify unpark", f.unpark)
	}
	if f.pics == nil {
		f.end("dismissed", func(n *Notification) bool { return n.ID == id })
	}
	f.Changed.Emit(struct{}{})
	return true
}

// CriticalLight is how the screen is to be lit for the critical notification ringing now: its
// brightness in percent, how long it takes to get there, and since when it rings. The screen takes the
// light over while it rings, and gives it back once it is quieted or answered. ok is false when none
// rings.
type CriticalLight struct {
	Percent int
	Rise    time.Duration
	Since   time.Time
}

func (f *Feature) CriticalLight() (CriticalLight, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ringing == nil || f.up == nil || f.up.ID != f.ringing.id {
		return CriticalLight{}, false
	}
	return CriticalLight{Percent: f.up.Brightness, Rise: f.up.BrightnessRise, Since: f.ringing.started}, true
}

// Ringing is whether critical notification id's bell rings: the card says so.
func (f *Feature) Ringing(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ringing != nil && f.ringing.id == id
}

// parked is a notification that came while a critical one was up, and waits for it to go.
type parked struct {
	n Notification
	h how
}

// park keeps n back while critical notification up is up, in place of whatever was waiting. Called with
// f.mu held.
func (f *Feature) park(n Notification, h how) {
	if f.waiting != nil {
		f.report(f.waiting.n, "replaced")
	}
	f.waiting = &parked{n: n, h: h}
	slog.Info("notification waits for the critical one up", "id", n.ID)
}

// leftUp is what follows n going, after f.up was cleared: its bell stops, and what waited for it comes
// up, once n's caller has let go (unpark). Called with f.mu held.
func (f *Feature) leftUp(n Notification) {
	if r := f.ringing; r != nil && r.id == n.ID {
		f.ringing = nil
		r.stop()
	}
	if n.Critical && f.waiting != nil {
		safe.Go("notification waiting", f.unpark)
	}
}

// unpark puts the notification that waited up, if nothing critical has come since and its time is not
// over. It comes up without its chime and its words, which were for when it came.
func (f *Feature) unpark() {
	f.order.Lock()
	defer f.order.Unlock()
	f.mu.Lock()
	w := f.waiting
	if w == nil {
		f.mu.Unlock()
		return
	}
	now := f.now()
	if f.holdsBack(f.up, w.n) {
		f.mu.Unlock()
		return // a critical one still rings: it waits on
	}
	f.waiting = nil
	if !now.Before(w.n.Until) {
		f.report(w.n, "expired")
		f.mu.Unlock()
		return
	}
	f.mu.Unlock()
	h := w.h
	h.silent, h.aloud = true, false
	f.put(w.n, h)
}

// clearWaiting drops the notification that waits if it carries tag ("" for any), as cleared. Called
// with f.mu held.
func (f *Feature) clearWaiting(tag string) bool {
	w := f.waiting
	if w == nil || w.n.Own || (tag != "" && w.n.Tag != tag) {
		return false
	}
	f.waiting = nil
	f.report(w.n, "cleared")
	return true
}
