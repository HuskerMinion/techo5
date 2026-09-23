// Package remind is what a reminder does when it comes due: a chime, the label said aloud once, and
// the label left on the screen until somebody deals with it — here, and on whichever other devices
// it was set to go off on.
//
// A reminder is not a ring. It is said once rather than repeated, so it holds nothing open and has
// nothing to snooze; what it keeps is the card, and stopping it anywhere stops it everywhere it went,
// because whoever heard it has dealt with it.
//
// **Saying it.** A device cannot turn words into speech itself. It asks Home Assistant to announce
// the label on this device, in the voice the house's assistant already uses. Without Home Assistant,
// or with the device not allowed to ask, the chime and the screen are all there is.
package remind

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// tone is what a reminder arrives with: three notes rising, apart from the alarm's and an
// announcement's two.
var tone = []speaker.Note{{Freq: 523, Ms: 110}, {Freq: 659, Ms: 110}, {Freq: 784, Ms: 200}}

// Reminder is one on the screen: what it says, where it came from, and when it went off.
type Reminder struct {
	ID    string
	Label string
	From  string // the device it was set on; this device's own name for one of its own
	At    time.Time
}

// dest is where else a reminder goes: every device, some by name, or (the zero value) nowhere.
type dest struct {
	all   bool
	names []string
}

func (d dest) any() bool { return d.all || len(d.names) > 0 }

// to is the list SendTo takes, which reads nil as every device.
func (d dest) to() []string {
	if d.all {
		return nil
	}
	return d.names
}

type Feature struct {
	mu      sync.Mutex
	showing *Reminder
	// sentTo is where else a reminder of this device's own went, so stopping it here stops it there.
	sentTo dest

	// Changed fires when a reminder comes up or goes away.
	Changed hook.Hook[struct{}]

	// keep is whether a reminder stays up once said (keeps, for this build).
	keep bool

	// chime, say and send are the sound, the speech and the other devices; replaced in tests.
	chime func()
	say   func(label string)
	send  func(names []string, m announce.Message)
}

// playTone is one round of the reminder's chime at the ring's own volume, which is what somebody set
// for being fetched by something that goes off at a time.
func playTone() { ring.Sample(tone) }

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		shared = &Feature{keep: keeps, chime: playTone, say: sayThroughHA, send: announce.Get().SendTo}
		a := announce.Get()
		a.Reminded.Listen(func(m announce.Message) {
			shared.show(Reminder{ID: m.ID, Label: m.Text, From: m.From, At: time.Now()}, dest{})
		})
		a.ReminderStopped.Listen(func(m announce.Message) { shared.dismiss(m.ID) })
	})
	return shared
}

func init() {
	// The Stop ringing button, and anything else that means "stop", takes a reminder down too.
	ring.Silences(func() bool { return Get().Stop() })
}

// Fire is one of this device's own reminders coming due: it goes off here and on the devices in
// ringOn (config.RingEverywhere for all of them).
func (f *Feature) Fire(label string, ringOn []string) {
	r := Reminder{ID: newID(), Label: label, From: config.Get().Device.Name, At: time.Now()}
	d := targets(ringOn, r.From)
	f.show(r, d)
	if d.any() {
		safe.Go("reminder", func() {
			f.send(d.to(), announce.Message{Kind: announce.KindReminder, ID: r.ID, Text: r.Label})
		})
	}
}

// show puts a reminder up, chimes, and has it said. One that arrives while another is showing takes
// its place: the newer one is what somebody needs to hear now.
func (f *Feature) show(r Reminder, sentTo dest) {
	slog.Info("reminder", "id", r.ID, "label", r.Label, "from", r.From)
	if f.keep {
		f.mu.Lock()
		f.showing, f.sentTo = &r, sentTo
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
	}

	// Quiet hours do not hold it back: it was set for this time on purpose, as an alarm is.
	f.chime()
	safe.Go("reminder", func() { f.say(r.Label) })
}

// Showing is the reminder on the screen, if there is one.
func (f *Feature) Showing() (Reminder, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.showing == nil {
		return Reminder{}, false
	}
	return *f.showing, true
}

// Stop takes the reminder down here and on every device it went to, and reports whether there was
// one. For a reminder that came from another device, the others it went to are told as well: every
// device is told, since this one does not know which the sender picked, and a device not showing
// that reminder ignores it.
func (f *Feature) Stop() bool {
	f.mu.Lock()
	r, sentTo := f.showing, f.sentTo
	f.showing, f.sentTo = nil, dest{}
	f.mu.Unlock()
	if r == nil {
		return false
	}
	slog.Info("reminder stopped", "id", r.ID)
	f.Changed.Emit(struct{}{})

	if r.From != config.Get().Device.Name {
		sentTo = dest{all: true}
	}
	if !sentTo.any() {
		return true // it went nowhere else
	}
	stopped := announce.Message{Kind: announce.KindReminderStopped, ID: r.ID}
	safe.Go("reminder", func() { f.send(sentTo.to(), stopped) })
	return true
}

// dismiss takes a reminder down because it was stopped on another device. Nothing is sent on: that
// device has told everyone who needs telling.
func (f *Feature) dismiss(id string) {
	f.mu.Lock()
	match := f.showing != nil && f.showing.ID == id
	if match {
		f.showing, f.sentTo = nil, dest{}
	}
	f.mu.Unlock()
	if match {
		slog.Info("reminder stopped on another device", "id", id)
		f.Changed.Emit(struct{}{})
	}
}

// targets reads a reminder's RingOn: everywhere, or the devices named other than this one, self,
// which goes off anyway.
func targets(ringOn []string, self string) dest {
	var d dest
	for _, n := range ringOn {
		n = strings.TrimSpace(n)
		switch {
		case n == "", strings.EqualFold(n, self):
		case strings.EqualFold(n, config.RingEverywhere):
			return dest{all: true}
		default:
			d.names = append(d.names, n)
		}
	}
	return d
}

// newID is unique enough across a house: this device's name and the moment it went off.
func newID() string {
	return layout.EntitySlug(config.Get().Device.Name) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// Say asks Home Assistant to say words on this device, for anything else that goes off with a label:
// an alarm saying what it is for.
func Say(words string) { sayThroughHA(words) }

// sayThroughHA asks Home Assistant to say the label on this device. Its own chime is skipped, since
// the reminder has just played one.
func sayThroughHA(label string) {
	component.CallService.Emit(component.Call{
		Service: "assist_satellite.announce",
		Data:    map[string]string{"entity_id": satellite(), "message": label, "preannounce": "false"},
	})
}

// satellite is this device's assist satellite in Home Assistant: the one named for it, found the way
// the radio finds its media player, since an entity id does not follow a renamed device. The id it
// would have is the answer when Home Assistant cannot be asked.
func satellite() string {
	name := config.Get().Device.Name
	guess := "assist_satellite." + layout.EntitySlug(name) + "_assist_satellite"
	list, err := hass.Get().Entities("assist_satellite")
	if err != nil {
		return guess
	}
	for _, e := range list {
		if strings.EqualFold(e.Name, name+" Assist satellite") {
			return e.ID
		}
	}
	return guess
}
