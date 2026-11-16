//go:build linux

package btaudio

import (
	"log/slog"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluealsa"
)

// A speaker or earbuds the device plays to has a volume of its own, which the device can set over AVRCP
// (absolute volume). bluez-alsa's default for a stream it plays out is to scale the audio itself and
// never set it, so the speaker stayed at whatever level it was at: low after it was switched off and on,
// quieter at the device's top step than with Fire OS, and its own buttons did nothing (#550).
//
// So the device's volume is the speaker's, as with a stock Echo or a phone. While a speaker that takes
// absolute volume is the output, bluez-alsa leaves the audio unscaled, the speaker is handed the level
// for the device's volume step (levelFor: the step's share of the range, as a phone hands over its
// slider), and the device scales nothing either (speaker.SetSinkLevel), so the volume is applied once.
// The volume changing here, by the buttons, Home Assistant, a voice request or night volume, moves the
// speaker's; the speaker's own buttons moving it set the device's volume. A speaker that does not take
// absolute volume, or a bluez-alsa that will not leave the audio unscaled, keeps the volume in software
// here, as before.

// ampAgain is how long after the stream opens the speaker is handed its level again: a speaker's AVRCP
// connection, which carries the level, can come up a few seconds after its audio, and a speaker can put
// its own level back as it does.
const ampAgain = 4 * time.Second

// absEvery is how often a look asks bluetoothd whether the speaker takes absolute volume: its AVRCP
// connection can come up, or go, while the audio stays.
const absEvery = 5 * time.Second

// sinkLevel hands the sink s its own volume (set) or, with nil, takes it back. A test stands in for it.
var sinkLevel = func(s speaker.Sink, set func(step int)) { speaker.Get().SetSinkLevel(s, set) }

// canAbs asks bluetoothd whether the speaker at device takes absolute volume now. A question that
// gets no answer (the bus busy, bluetoothd restarting) answers what is so already, so a moment's error
// does not take the volume back into software and hand it over again. It is called by speakerLook with
// f.spk.mu held, which is what makes reading f.spk.abs here safe.
func (f *Feature) canAbs(device dbus.ObjectPath) func() bool {
	return func() bool {
		f.mu.Lock()
		a := f.adapter
		f.mu.Unlock()
		if a == nil {
			return false
		}
		on, err := a.AbsoluteVolume(device)
		if err != nil {
			return f.spk.abs
		}
		return on
	}
}

// speakerVol is the volume of the stream played out to a speaker. mu keeps it, and puts one level on
// the speaker at a time.
type speakerVol struct {
	mu     sync.Mutex
	stream *bluealsa.Stream // the stream it is for, nil for none
	// abs is whether the speaker's own volume is the device's now, since absAt.
	abs   bool
	absAt time.Time
	// told is the step the speaker was last handed, -1 for none since abs went on. level is the
	// stream's level as last seen or set, known whether there is one, and ours until when a level read
	// may still be the one handed to the speaker, on its way back or rounded to the speaker's own steps.
	told  int
	level int
	known bool
	ours  time.Time
	// checked is when bluetoothd was last asked about absolute volume, again whether the next look asks
	// and hands the level again (ampAgain), and said whether the volume staying in software was logged.
	checked time.Time
	again   bool
	said    bool
	// refused is bluez-alsa having refused to leave this stream's audio unscaled: not asked again
	// every absEvery for as long as the stream lasts.
	refused bool
}

// reset makes v the state for a new stream s, nil for none. Called with v.mu held.
func (v *speakerVol) reset(s *bluealsa.Stream) {
	v.stream, v.abs, v.absAt = s, false, time.Time{}
	v.told, v.level, v.known, v.ours = -1, 0, false, time.Time{}
	v.checked, v.again, v.said, v.refused = time.Time{}, false, false, false
}

// speakerLook is a look at the stream s plays into, p as bluez-alsa has it now: the speaker's own volume
// is handed to it or taken back as absolute volume comes and goes (canAbs, asked of bluetoothd at most
// every absEvery, or now when force), and a level other than the last seen or set is the speaker's own
// buttons, which set the device's volume.
func (f *Feature) speakerLook(al pcmSetter, p bluealsa.PCM, s *bluealsa.Stream, canAbs func() bool, force bool) {
	v := &f.spk
	v.mu.Lock()
	if v.stream != s {
		v.reset(s)
		force = true
	}
	force = force || v.again
	v.again = false
	now := time.Now()
	// bluez-alsa back to scaling the audio, once it has had time to say it took the change: restarted,
	// or it lost the stream's setting. Put right.
	lost := v.abs && p.SoftVolume && now.Sub(v.absAt) >= modeSettle
	if force || lost || now.Sub(v.checked) >= absEvery {
		v.checked = now
		can := canAbs()
		switch {
		case can && !v.refused && (!v.abs || force || lost):
			f.absOn(al, s)
			v.mu.Unlock()
			return
		case !can && v.abs:
			f.absOff(al, s)
			v.mu.Unlock()
			return
		case !can && !v.said:
			v.said = true
			slog.Info("bluetooth: the speaker does not take absolute volume (yet); the volume stays in software")
		}
	}
	if !v.abs {
		v.mu.Unlock()
		return
	}
	level := p.Level()
	last, known, ours := v.level, v.known, now.Before(v.ours)
	v.level, v.known = level, true
	told := v.told
	v.mu.Unlock()
	if !speakerSets(level, last, known, ours, told, volumeNow()) {
		return
	}
	speakerTo(level)
	// Night volume held the raise back, or the level was within the step already: the speaker goes to
	// where the device ended up, since what it plays is no longer scaled here.
	if at := volumeNow(); at != stepFor(level) {
		f.handSpeaker(al, s, at)
	}
}

// speakerSets is whether a speaker's level, read now, is its own buttons moved and sets the device's
// volume (step now): a level other than the last seen or set, not while the one handed to it may still
// be on its way back (ours), and only while the speaker was last handed the device's volume step
// itself. Raised for a ring or a reply above the music, or at 0 for a mute, the speaker's level is the
// device's doing. Ignoring what comes back for a moment after a level is handed over is what keeps a
// change from going round: from here to the speaker, back as a press, and here again.
func speakerSets(level, last int, known, ours bool, told, step int) bool {
	return known && level != last && !ours && told == step
}

// speakerTo sets the device's volume to the step for a speaker's level. A variable for the tests.
var speakerTo = func(level int) { volumeFrom(level, "bluetooth: a speaker's own buttons set the volume") }

// absOn hands the speaker its own volume: bluez-alsa leaves the audio unscaled, and the speaker is put
// at the device's level from here on (handSpeaker). A bluez-alsa that will not leave the audio
// unscaled keeps the volume in software. Called with f.spk.mu held.
func (f *Feature) absOn(al pcmSetter, s *bluealsa.Stream) {
	v := &f.spk
	was := v.abs
	if err := al.SetSoftVolume(s.PCM, false); err != nil {
		v.refused = true
		if !v.said {
			v.said = true
			slog.Info("bluetooth: bluez-alsa keeps scaling the speaker's audio; the volume stays in software", "err", err)
		}
		if was {
			f.absOff(al, s)
		}
		return
	}
	v.abs, v.absAt, v.told = true, time.Now(), -1
	if !was {
		slog.Info("bluetooth: the device's volume is the speaker's own (absolute volume)")
	}
	// The speaker is told the step on a goroutine of the speaker package's, which waits for mu.
	sinkLevel(s, func(step int) { f.handSpeaker(al, s, step) })
}

// absOff takes the speaker's volume back into software: the device scales the audio again, and
// bluez-alsa leaves it at full level, as before. Called with f.spk.mu held.
func (f *Feature) absOff(al pcmSetter, s *bluealsa.Stream) {
	v := &f.spk
	v.abs, v.told, v.known = false, -1, false
	sinkLevel(s, nil)
	if err := al.SetSoftVolume(s.PCM, true); err != nil {
		slog.Debug("bluetooth: scaling a speaker's audio again", "err", err)
	}
	if err := al.SetLevel(s.PCM, bluealsa.VolumeMax); err != nil {
		slog.Debug("bluetooth: a speaker's audio back to full level", "err", err)
	}
	slog.Info("bluetooth: the speaker no longer takes absolute volume; the volume is in software again")
}

// handSpeaker puts the speaker of s at the level for step: the speaker's half of SetSinkLevel. Not
// when it is at that step already, from its own buttons, so a press is not rounded to the device's
// step; always the first time after abs went on, which is what puts back a level the speaker lost
// switching off and on.
func (f *Feature) handSpeaker(al pcmSetter, s *bluealsa.Stream, step int) {
	v := &f.spk
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stream != s || !v.abs {
		return
	}
	first := v.told < 0
	v.told = step
	if !first && v.known && stepFor(v.level) == step {
		return
	}
	level := levelFor(step)
	v.level, v.known = level, true
	v.ours = time.Now().Add(echoFor)
	if err := al.SetLevel(s.PCM, level); err != nil {
		slog.Warn("bluetooth: setting the speaker's volume", "err", err)
	}
}

// speakerAgain has the look ampAgain after the stream s opened hand the speaker its level again.
func (f *Feature) speakerAgain(s *bluealsa.Stream) {
	time.AfterFunc(ampAgain, func() {
		f.spk.mu.Lock()
		if f.spk.stream == s {
			f.spk.again = true
		}
		f.spk.mu.Unlock()
		f.wake()
	})
}

// speakerGone forgets the stream played out: it was closed, and nothing is handed to it any more.
func (f *Feature) speakerGone() {
	f.spk.mu.Lock()
	f.spk.reset(nil)
	f.spk.mu.Unlock()
}

// volumeFrom sets the device's volume to the step for a remote's level, logging why. A raise stops
// where night volume says (media.CapRaise).
func volumeFrom(level int, why string) {
	m := media.Get()
	from, to := m.Volume(), stepFor(level)
	if to > from {
		to = m.CapRaise(to)
	}
	if to != from {
		slog.Info(why, "level", level, "step", to)
		m.Set(to)
	}
}
