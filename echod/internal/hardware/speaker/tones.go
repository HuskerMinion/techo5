package speaker

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The sounds the device makes about itself, as opposed to anything it was asked to play.
//
// Direction carries the meaning — rising for on, falling for off — and a hold is two notes where a
// press is one, so they are told apart without looking.
const toneLevel = 0.3

var (
	ToneVolume = []Note{{Freq: 880, Ms: 80}}

	ToneMute     = []Note{{Freq: 880, Ms: 70}, {Freq: 587, Ms: 110}}
	ToneUnmute   = []Note{{Freq: 587, Ms: 70}, {Freq: 880, Ms: 110}}
	ToneMuteHold = []Note{{Freq: 587, Ms: 70}, {Freq: 440, Ms: 130}}

	// ToneTrouble falls twice and ends low, which no acknowledgement does. A request that cannot be
	// served has to sound different from one that was, or a failure is indistinguishable from the
	// device having ignored the person entirely.
	ToneTrouble = []Note{{Freq: 622, Ms: 90}, {Freq: 466, Ms: 90}, {Freq: 349, Ms: 180}}

	// ToneCancel is one short falling pair: the request was dropped, which is neither a failure nor
	// an answer.
	ToneCancel = []Note{{Freq: 698, Ms: 60}, {Freq: 466, Ms: 90}}

	// TonePairing and TonePairingOff are Bluetooth pairing mode asked for from a button: three notes,
	// so they are not mistaken for mute's two, rising for on and falling for off.
	TonePairing    = []Note{{Freq: 523, Ms: 70}, {Freq: 659, Ms: 70}, {Freq: 988, Ms: 140}}
	TonePairingOff = []Note{{Freq: 988, Ms: 70}, {Freq: 659, Ms: 70}, {Freq: 523, Ms: 140}}

	// TonePaired is pairing mode having worked: the pairing chime's top note twice, ending higher.
	TonePaired = []Note{{Freq: 988, Ms: 90}, {Ms: 60}, {Freq: 1319, Ms: 200}}

	// ToneTimer is a timer that has finished. Three of the same note, because it repeats until
	// somebody stops it and a melody wears out faster than a beep does.
	ToneTimer = []Note{
		{Freq: 880, Ms: 160}, {Ms: 130},
		{Freq: 880, Ms: 160}, {Ms: 130},
		{Freq: 880, Ms: 160},
	}
)

// alarmSounds are what an alarm can ring with, in the order they are offered; the first is the
// default. Each is one round of the pattern, repeated while the alarm rings; a round longer than the
// time between rounds is let finish first (ring/bell.go). Home Assistant is the timer sound of the
// Home Assistant satellites, and Beeps the one TECHO5 timers always used.
var alarmSounds = []struct {
	name  string
	notes []Note
}{
	{"Home Assistant", []Note{ClipTimer.Note()}},
	{"Beeps", ToneTimer},
	{"Chimes", []Note{{Freq: 523, Ms: 90}, {Freq: 659, Ms: 90}, {Freq: 784, Ms: 90}, {Freq: 1047, Ms: 320}}},
	{"Bells", []Note{{Freq: 1319, Ms: 320}, {Ms: 80}, {Freq: 1047, Ms: 520}}},
	{"Gentle", []Note{{Freq: 587, Ms: 240}, {Ms: 140}, {Freq: 740, Ms: 360}}},
	{"Pulse", []Note{
		{Freq: 988, Ms: 70}, {Ms: 50}, {Freq: 988, Ms: 70}, {Ms: 50},
		{Freq: 988, Ms: 70}, {Ms: 50}, {Freq: 988, Ms: 70},
	}},
}

// AlarmSounds lists the alarm sounds' names in the order they are offered.
func AlarmSounds() []string {
	names := make([]string, len(alarmSounds))
	for i, s := range alarmSounds {
		names[i] = s.name
	}
	return names
}

// AlarmSound is one round of the named alarm sound; an unknown name is the first.
func AlarmSound(name string) []Note {
	for _, s := range alarmSounds {
		if s.name == name {
			return s.notes
		}
	}
	return alarmSounds[0].notes
}

// wakeTones is what a detection can sound like. They are told apart by shape rather than pitch, so
// two wake words set to different ones are distinguishable without knowing which is which.
var wakeTones = map[config.Tone][]Note{
	config.ToneHA:    {ClipWake.Note()},
	config.ToneNone:  nil,
	config.ToneChirp: {{Freq: 784, Ms: 60}, {Freq: 1175, Ms: 90}},
	config.ToneDing:  {{Freq: 1319, Ms: 200}},
	config.ToneRise:  {{Freq: 659, Ms: 55}, {Freq: 880, Ms: 55}, {Freq: 1319, Ms: 110}},
}

// WakeTone is what a slot set to t sounds like.
func WakeTone(t config.Tone) []Note { return wakeTones[t] }

// Length is how long notes take to sound, rests included.
func Length(notes []Note) time.Duration {
	var ms int
	for _, n := range notes {
		ms += n.Ms
	}
	return time.Duration(ms) * time.Millisecond
}

// Audible is how long notes are loud enough to matter to the microphones: their length, except that a
// recording counts only until its fade (Clip.LoudMs). A turn holds its microphone back for this long
// while the wake sound plays, and Home Assistant's lasts nearly a second, most of it a quiet tail the
// echo canceller takes care of; holding for all of it lost the first words of every request.
//
// With no echo canceller running, nothing takes the fade out, and a speaker a few centimetres from the
// microphones is loud even 20 dB down: uncancelled counts until the fade is 30 dB down instead.
func Audible(notes []Note, uncancelled bool) time.Duration {
	var ms int
	for _, n := range notes {
		switch {
		case n.Clip != nil && uncancelled:
			ms += n.Clip.QuietMs()
		case n.Clip != nil:
			ms += n.Clip.LoudMs()
		default:
			ms += n.Ms
		}
	}
	return time.Duration(ms) * time.Millisecond
}

// WakeTones lists them in the order they are offered.
func WakeTones() []config.Tone {
	return []config.Tone{config.ToneHA, config.ToneNone, config.ToneChirp, config.ToneDing, config.ToneRise}
}

// TimerSound is one round of a finished timer: Home Assistant's, or the classic beeps.
func TimerSound() []Note {
	if config.Get().Speaker.ClassicSounds {
		return ToneTimer
	}
	return []Note{ClipTimer.Note()}
}

// MuteSound is what muting (on) or unmuting the microphones sounds like: Home Assistant's, or the
// classic pair of notes.
func MuteSound(on bool) []Note {
	classic := config.Get().Speaker.ClassicSounds
	switch {
	case classic && on:
		return ToneMute
	case classic:
		return ToneUnmute
	case on:
		return []Note{ClipMuteOn.Note()}
	}
	return []Note{ClipMuteOff.Note()}
}

// Chime plays a tone alongside whatever is playing rather than instead of it: pressing volume during
// a reply should beep and leave the reply alone.
func (d *Driver) Chime(notes []Note) {
	if d == nil || len(notes) == 0 {
		return
	}
	d.Interject(func(p *Player) { p.Chime(toneLevel, notes...) })
}
