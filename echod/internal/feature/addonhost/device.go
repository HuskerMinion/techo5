package addonhost

import (
	"context"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/internal/feature/api"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/keepawake"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// device is what the API's requests do on this device (serve.Device).
type device struct {
	host *Host

	// play is the players an add-on's media plays on: the device's own (devicePlayers) unless a test
	// gives its own.
	play   players
	listen sync.Once // the players' hooks, listened to from an add-on's first media.play

	mu    sync.Mutex
	seq   uint64             // media ids, which media.state carries
	video map[string]*played // the video each add-on started last, by add-on
	audio map[string]*played // the track each add-on started last, by add-on
}

// AddonEvent is the event an add-on's event.fire puts on Home Assistant's bus.
const AddonEvent = "esphome.techo5_addon"

func (d *device) Info() addonapi.Device {
	screenMu.Lock()
	w, h := screenW, screenH
	screenMu.Unlock()
	return addonapi.Device{Family: addons.Family(), Board: layout.Board, Width: w, Height: h, Language: language(),
		Version: layout.Version}
}

func (d *device) Settings(id string) map[string]any {
	_, values, ok := d.host.Settings(id)
	if !ok {
		return map[string]any{}
	}
	return values
}

func (d *device) Connected() bool { return api.Get().Connected() }

func (d *device) Quiet() bool { return config.Quiet() }

// How long words asked of Home Assistant may take: to begin sounding here, and in all.
var (
	sayStarts = 10 * time.Second
	sayLasts  = time.Minute
	sayPoll   = 100 * time.Millisecond
)

// Say has Home Assistant announce the words on this device, as a reminder's are said (remind.Say): in
// the household's voice, ducking the music, behind a turn, a call or a ring, and stopped by the stop
// word. It returns once they have been heard, so the add-on's next words wait their turn.
func (d *device) Say(ctx context.Context, id, text string) error {
	heard := true
	remind.SayOr(text, func() { heard = false })
	if !heard {
		return addonapi.Errorf(addonapi.CodeUnavailable, "Home Assistant is not connected, and it is what speaks")
	}
	p := media.Get()
	started := time.Now()
	began := false
	for {
		speaking := p.Speaking()
		switch {
		case speaking:
			began = true
		case began:
			return nil // said
		case time.Since(started) > sayStarts:
			return nil // not heard here (Home Assistant without TTS, say): the next ones are not held up for it
		}
		if time.Since(started) > sayLasts {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sayPoll):
		}
	}
}

// sounds are the fixed set, as notes: short and apart from the device's own cues.
var sounds = map[string][]speaker.Note{
	"chime": {{Freq: 784, Ms: 110}, {Freq: 1047, Ms: 220}},
	"done":  {{Freq: 523, Ms: 90}, {Freq: 659, Ms: 90}, {Freq: 784, Ms: 180}},
	"error": {{Freq: 392, Ms: 160}, {Freq: 311, Ms: 260}},
	"tick":  {{Freq: 1500, Ms: 25}},
}

// PlaySound plays a sound over whatever is playing, as the device's own chimes do.
func (d *device) PlaySound(id, name string, w *addonapi.WAV) error {
	if w != nil {
		// Never louder than the device's own cues: held to half of full scale (-6 dBFS) here, and
		// Synth brings it to the chimes' level, which the device's volume then sets as it does theirs.
		speaker.Sound().Chime([]speaker.Note{speaker.Synth(capPeak(resample(w.Samples, w.Rate, speaker.Rate), maxPeak))})
		return nil
	}
	notes, ok := sounds[name]
	if !ok {
		return addonapi.Errorf(addonapi.CodeBadRequest, "there is no sound %q", name)
	}
	speaker.Sound().Chime(notes)
	return nil
}

// maxPeak is the loudest an add-on's own sound may be, as a share of full scale: -6 dBFS.
const maxPeak = 0.5

// capPeak scales samples down so none is past most of full scale; quieter ones are left as they are.
func capPeak(in []int16, most float64) []int16 {
	peak := 0
	for _, s := range in {
		peak = max(peak, abs(int(s)))
	}
	limit := most * 32767
	if float64(peak) <= limit {
		return in
	}
	g := limit / float64(peak)
	out := make([]int16, len(in))
	for i, s := range in {
		out[i] = int16(float64(s) * g)
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// resample takes mono samples from one rate to another, by straight lines between them: enough for a
// cue of a few seconds.
func resample(in []int16, from, to int) []int16 {
	if from == to || from <= 0 || len(in) == 0 {
		return in
	}
	n := int(int64(len(in)) * int64(to) / int64(from))
	out := make([]int16, n)
	for i := range out {
		pos := float64(i) * float64(from) / float64(to)
		j := int(pos)
		if j+1 >= len(in) {
			out[i] = in[len(in)-1]
			continue
		}
		frac := pos - float64(j)
		out[i] = int16(float64(in[j])*(1-frac) + float64(in[j+1])*frac)
	}
	return out
}

func (d *device) StartTimer(id, name string, dur time.Duration) (string, error) {
	t := timer.Get().StartOwned(id, d.label(id), name, dur)
	if t == "" {
		return "", addonapi.Errorf(addonapi.CodeBadRequest, "the timer did not start")
	}
	return t, nil
}

// label is the add-on's name, as its timers are shown under.
func (d *device) label(id string) string {
	if n, ok := d.host.names()[id]; ok && n != "" {
		return n
	}
	return id
}

func (d *device) CancelTimer(id, t string) bool { return timer.Get().CancelOwned(id, t) }

func (d *device) Timers(id string) []addonapi.Timer {
	var out []addonapi.Timer
	for _, c := range timer.Get().OwnedBy(id, time.Now()) {
		out = append(out, addonapi.Timer{ID: c.ID, Name: c.OwnName, Left: c.Left.Seconds(), Total: c.Total.Seconds(), Running: c.Active})
	}
	return out
}

func (d *device) SetAwake(held bool) { keepawake.Set(held) }

// Fire puts the add-on's event on Home Assistant's bus, with its id and the event's name beside its
// data, which may not use those two keys.
func (d *device) Fire(id, name string, data map[string]string) error {
	ev := map[string]string{"addon": id, "name": name}
	for k, v := range data {
		if k == "addon" || k == "name" {
			return addonapi.Errorf(addonapi.CodeBadRequest, "an event's data may not use the keys addon and name, which say whose event it is")
		}
		ev[k] = v
	}
	component.Fire.Emit(component.Event{Name: AddonEvent, Data: ev})
	return nil
}

func (d *device) SetEntity(id string, e addonapi.Entity, state any) error {
	ents := d.host.entitiesOf(id)
	if ents == nil {
		return addonapi.Errorf(addonapi.CodeUnavailable, "the add-on's entities are not in Home Assistant yet")
	}
	return ents.set(e.ID, state)
}
