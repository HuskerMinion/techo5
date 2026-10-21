package config

// Wake is the wake word configuration, indexed by Home Assistant's wake word slot.
type Wake struct {
	Words []WakeWord `json:"words"`

	// NoneChosen is "No wake word" chosen on purpose. Without it an empty selection reads as a device
	// that was never set up, which starts with the default word so it is not deaf out of the box, and
	// the choice came back undone at every start.
	NoneChosen bool `json:"none_chosen,omitempty"`

	// Stop is the device's own word for interrupting what it is saying. It is not one of the slots
	// above: Home Assistant does not choose it, and it opens no pipeline.
	Stop Stop `json:"stop"`

	// Hangup is a word that hangs up a call, heard on the device. Like Stop it is not one of the slots:
	// it opens no pipeline, and it is listened for only while a call rings or is up.
	Hangup Hangup `json:"hangup,omitempty"`

	// OnHA is Home Assistant's own wake word engine, used beside the device's or in its place.
	OnHA OnHA `json:"on_ha,omitempty"`

	// KeepClips keeps short clips of the wake word on the device, for training a model on (feature/
	// detect, clips.go). Off unless chosen, and turning it off deletes what was kept.
	KeepClips bool `json:"keep_clips,omitempty"`
}

// OnHA is what Home Assistant's wake word engine is asked to do. Both are off unless chosen: either one
// sends the room's audio to Home Assistant, and the device's own engine is what the rest assumes.
type OnHA struct {
	// Stream sends the microphones to Home Assistant all the time the device is idle, and its pipeline's
	// wake word engine listens for the first wake word in place of the device's.
	Stream bool `json:"stream,omitempty"`

	// Confirm asks Home Assistant about a wake the device was not sure of: one that scored at least
	// ConfirmAt but under the wake word threshold opens only if Home Assistant's engine hears it too.
	Confirm bool `json:"confirm,omitempty"`

	// ConfirmThreshold is the lowest score that is worth asking about. Zero is the default.
	ConfirmThreshold float64 `json:"confirm_threshold,omitempty"`
}

// DefaultConfirmThreshold is where asking starts: under the default wake word threshold by enough to
// catch a word said with an accent, and over the noise that would have Home Assistant asked all day.
const DefaultConfirmThreshold = 0.7

// ConfirmAt is the lowest score asked about.
func (o OnHA) ConfirmAt() float64 {
	if o.ConfirmThreshold <= 0 {
		return DefaultConfirmThreshold
	}
	return o.ConfirmThreshold
}

// Stop is the interrupting word.
type Stop struct {
	// Threshold is the score it has to reach, on its own scale. At StopOff the word is not listened for
	// at all: the model is left unloaded, so switching it off costs nothing rather than costing a
	// comparison. There is no separate switch because this is one.
	Threshold float64 `json:"threshold"`
}

const (
	// StopOff is a threshold no score can reach, which is how the word is turned off.
	StopOff = 1.0

	// Above the 0.5 the model is calibrated for, below where spoken attempts land.
	DefaultStopThreshold = 0.7
)

func defaultStop() Stop {
	return Stop{Threshold: DefaultStopThreshold}
}

// Listening reports whether the stop word is being listened for.
func (s Stop) Listening() bool { return s.Threshold < StopOff }

// Hangup is the hang-up word: which word, and the score it has to reach. An empty ID is none, which is
// the default, so hanging up by voice is something chosen; HangupStop is the stop word, and anything
// else is a model from the same library as the wake words.
type Hangup struct {
	ID string `json:"id,omitempty"`

	// Threshold is the score it has to reach. Zero is DefaultThreshold, a wake word's.
	Threshold float64 `json:"threshold,omitempty"`
}

// HangupStop is the stop word doing the hang-up word's work: the stop word that is listened for anyway,
// so nothing more is loaded for a call, and a ring during the call is still stopped first.
const HangupStop = "stop_word"

// On reports whether a hang-up word is chosen at all.
func (h Hangup) On() bool { return h.ID != "" }

// StopWord reports whether the stop word is the hang-up word.
func (h Hangup) StopWord() bool { return h.ID == HangupStop }

// ModelID is the model the hang-up word takes from the wake word library, empty for none or the stop
// word: what has to be kept on the device for it.
func (h Hangup) ModelID() string {
	if !h.On() || h.StopWord() {
		return ""
	}
	return h.ID
}

// ThresholdAt is the score the hang-up word has to reach.
func (h Hangup) ThresholdAt() float64 {
	if h.Threshold <= 0 {
		return DefaultThreshold
	}
	return h.Threshold
}

// WakeWord is one slot: which wake word listens there and how it behaves when it fires. An empty ID
// is the slot switched off, which is also how detection is turned off altogether.
type WakeWord struct {
	ID string `json:"id"`

	// Threshold is the score a detection has to reach. Per word, because models disagree on scale.
	Threshold float64 `json:"threshold"`

	Tone   Tone   `json:"tone"`
	Effect string `json:"effect"`

	// ThinkingEffect and ReplyingEffect override what those phases show. Empty leaves them to Effect.
	ThinkingEffect string `json:"thinking_effect"`
	ReplyingEffect string `json:"replying_effect"`

	// Delivery is how the reply from this slot's pipeline reaches the device.
	Delivery Delivery `json:"delivery"`

	// FollowUp is seconds to listen after a reply, zero to only do it when Home Assistant asks.
	FollowUp int `json:"follow_up"`

	// FollowUps is how many follow-ups in a row a wake word opens, zero for no limit. It counts only the
	// listening after every reply that FollowUp turns on: a question the assistant asks is always
	// listened for.
	FollowUps int `json:"follow_ups,omitempty"`

	// FollowUpTone is what a turn opened without a wake word sounds like. Empty is the wake word's own
	// tone, which is what a follow-up has always done; None makes the follow-up silent, and any other
	// tone gives it a sound of its own so the two are told apart by ear.
	//
	// Empty is the default, so a slot saved before this existed needs no key for it and no second field
	// recording whether somebody chose: an override nobody touched is stored as nothing at all.
	FollowUpTone Tone `json:"follow_up_tone,omitempty"`

	// Buffer is milliseconds of a streamed reply to collect before playing any of it.
	Buffer int `json:"buffer"`

	// Seconds before giving up. Listening holds the microphone open and Home Assistant normally ends
	// it, so that one is a backstop; thinking holds only the ring, and a model can take a minute.
	MaxListen int `json:"max_listen"`
	MaxThink  int `json:"max_think"`

	// Recordings is how many of this slot's turns to keep the audio of on disk. Zero keeps none.
	Recordings int `json:"recordings"`

	// Realtime is this slot answered by realtime voice (config.Realtime) in place of its assistant.
	Realtime bool `json:"realtime,omitempty"`
}

const (
	DefaultThreshold = 0.85
	DefaultEffect    = "Pulse"
	DefaultTone      = ToneHA
	DefaultDelivery  = DeliveryWhole

	DefaultMaxListen = 15
	DefaultMaxThink  = 90

	// Zero is no follow-up unless Home Assistant asks for one.
	DefaultFollowUp = 0

	// Home Assistant paces itself to stay 384 ms ahead, so holding that much consumes the whole lead:
	// measured, 384 gave 8 seams in a 13 second reply and 650 gave one.
	DefaultBuffer = 650
)

// DefaultWakeWord is a slot nobody has set: switched off, and everything else ready for when it is.
func DefaultWakeWord() WakeWord {
	return WakeWord{
		Threshold: DefaultThreshold,
		Tone:      DefaultTone,
		Effect:    DefaultEffect,
		Delivery:  DefaultDelivery,
		FollowUp:  DefaultFollowUp,
		Buffer:    DefaultBuffer,
		MaxListen: DefaultMaxListen,
		MaxThink:  DefaultMaxThink,
	}
}

// DefaultWakeID is the word a new device answers to; the model ships in the image.
const DefaultWakeID = "alexa"

// defaultWords is the one slot a new device comes with.
func defaultWords() []WakeWord {
	w := DefaultWakeWord()
	w.ID = DefaultWakeID
	return []WakeWord{w}
}

// Slot is one wake word slot, or an unset one with the defaults in it.
func (w Wake) Slot(n int) WakeWord {
	if n < 0 || n >= len(w.Words) {
		return DefaultWakeWord()
	}
	return w.Words[n]
}

// Slots is the first n slots, one entry each whether or not any has been set.
func (w Wake) Slots(n int) []WakeWord {
	out := make([]WakeWord, n)
	for i := range out {
		out[i] = w.Slot(i)
	}
	return out
}

// OnHAWriter is Home Assistant's wake word engine.
type OnHAWriter struct{ st *Store }

func (w OnHAWriter) Stream(v bool) error {
	return w.st.Update(func(c *Config) { c.Wake.OnHA.Stream = v })
}

func (w OnHAWriter) Confirm(v bool) error {
	return w.st.Update(func(c *Config) { c.Wake.OnHA.Confirm = v })
}

func (w OnHAWriter) ConfirmThreshold(v float64) error {
	return w.st.Update(func(c *Config) { c.Wake.OnHA.ConfirmThreshold = v })
}

// StopWriter is the stop word, which belongs to no slot.
type StopWriter struct{ st *Store }

func (w StopWriter) Threshold(v float64) error {
	return w.st.Update(func(c *Config) { c.Wake.Stop.Threshold = v })
}

// HangupWriter is the hang-up word, which belongs to no slot.
type HangupWriter struct{ st *Store }

func (w HangupWriter) ID(v string) error {
	return w.st.Update(func(c *Config) { c.Wake.Hangup.ID = v })
}

func (w HangupWriter) Threshold(v float64) error {
	return w.st.Update(func(c *Config) { c.Wake.Hangup.Threshold = v })
}

type WakeWriter struct {
	st   *Store
	slot int
}

func (w WakeWriter) ID(v string) error {
	return w.word(func(word *WakeWord) { word.ID = v })
}

func (w WakeWriter) Threshold(v float64) error {
	return w.word(func(word *WakeWord) { word.Threshold = v })
}

func (w WakeWriter) Tone(v Tone) error {
	return w.word(func(word *WakeWord) { word.Tone = v })
}

func (w WakeWriter) Effect(v string) error {
	return w.word(func(word *WakeWord) { word.Effect = v })
}

func (w WakeWriter) ThinkingEffect(v string) error {
	return w.word(func(word *WakeWord) { word.ThinkingEffect = v })
}

func (w WakeWriter) ReplyingEffect(v string) error {
	return w.word(func(word *WakeWord) { word.ReplyingEffect = v })
}

func (w WakeWriter) Delivery(v Delivery) error {
	return w.word(func(word *WakeWord) { word.Delivery = v })
}

func (w WakeWriter) FollowUp(seconds int) error {
	return w.word(func(word *WakeWord) { word.FollowUp = seconds })
}

func (w WakeWriter) FollowUps(n int) error {
	return w.word(func(word *WakeWord) { word.FollowUps = n })
}

func (w WakeWriter) FollowUpTone(v Tone) error {
	return w.word(func(word *WakeWord) { word.FollowUpTone = v })
}

func (w WakeWriter) Buffer(ms int) error {
	return w.word(func(word *WakeWord) { word.Buffer = ms })
}

func (w WakeWriter) MaxListen(seconds int) error {
	return w.word(func(word *WakeWord) { word.MaxListen = seconds })
}

func (w WakeWriter) MaxThink(seconds int) error {
	return w.word(func(word *WakeWord) { word.MaxThink = seconds })
}

func (w WakeWriter) Recordings(count int) error {
	return w.word(func(word *WakeWord) { word.Recordings = count })
}

// word grows the list to reach the slot, so slot 1 can be set on a device where slot 0 never was.
// The slots invented along the way get the defaults rather than zeros.
func (w WakeWriter) word(f func(*WakeWord)) error {
	if w.slot < 0 {
		return errSlot(w.slot)
	}
	return w.st.Update(func(c *Config) {
		for len(c.Wake.Words) <= w.slot {
			c.Wake.Words = append(c.Wake.Words, DefaultWakeWord())
		}
		f(&c.Wake.Words[w.slot])
	})
}

// Delivery is how a spoken reply reaches the device. It is per slot because a local pipeline and a
// cloud one differ in how long the audio takes to start, so the trade between starting sooner and
// not gapping is not the same for both.
type Delivery string

const (
	// DeliveryWhole fetches the reply from the url Home Assistant serves it at. It cannot gap and it
	// says when the audio has ended, which the stream does not.
	DeliveryWhole Delivery = "whole"

	// DeliveryStream takes the reply over the API as it is generated. It starts sooner and it can gap:
	// the chunks arrive at about the rate they play, so any hiccup splices silence into a word.
	DeliveryStream Delivery = "stream"
)

// Label is how the setting is shown.
func (d Delivery) Label() string {
	switch d {
	case DeliveryWhole:
		return "Whole file"
	case DeliveryStream:
		return "Streamed"
	}
	return string(d)
}

// Tone is the sound a wake word makes when it fires.
type Tone string

const (
	// ToneHA is the Home Assistant satellites' own wake sound, the default (hardware/speaker/clips.go).
	ToneHA Tone = "home_assistant"

	// ToneNone is silence: the ring is feedback enough for some people.
	ToneNone Tone = "none"

	// ToneChirp is two quick rising notes.
	ToneChirp Tone = "chirp"

	// ToneDing is one longer note, for somewhere noisy.
	ToneDing Tone = "ding"

	// ToneRise is three ascending notes, the most conspicuous of them.
	ToneRise Tone = "rise"
)

// Label is how the setting is shown.
func (t Tone) Label() string {
	switch t {
	case ToneHA:
		return "Home Assistant"
	case ToneNone:
		return "None"
	case ToneChirp:
		return "Chirp"
	case ToneDing:
		return "Ding"
	case ToneRise:
		return "Rise"
	}
	return string(t)
}
