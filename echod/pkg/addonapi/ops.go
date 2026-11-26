package addonapi

import "time"

// Each op's arguments and result, and each event's data. A field left out of a request is its zero
// value; every field an op does not name is refused (Unmarshal).

// Limits of the ops.
const (
	// MaxSpeak is the most characters one speak may say, and MaxSpeakQueue how many of an add-on's
	// may wait to be said.
	MaxSpeak      = 400
	MaxSpeakQueue = 3
	// MaxEventData is the most an event.fire's data may come to, as JSON, and EventsPerSecond how
	// many an add-on may fire.
	MaxEventData    = 1024
	EventsPerSecond = 2
	// SoundsPerSecond is how many sounds an add-on may play.
	SoundsPerSecond = 1
	// MaxWAV and MaxWAVLength bound a sound from the add-on's own files: mono, 16-bit.
	MaxWAV       = 512 << 10
	MaxWAVLength = 5 * time.Second
	// MaxTimers is how many timers one add-on may have at once, and MaxTimer the longest one.
	MaxTimers = 8
	MaxTimer  = 24 * time.Hour
	// MaxAwake is the longest a keep-awake hold lasts before it is renewed, and MaxAwakeHolds how many
	// one add-on may hold.
	MaxAwake      = 30 * time.Minute
	MaxAwakeHolds = 4
	// MaxAnswer is the most a command's answer may say, which Home Assistant can speak; MaxTestAnswer
	// a settings test's.
	MaxAnswer     = 1024
	MaxTestAnswer = 200
	// EntitySetsPerSecond is how often one entity's state may be set.
	EntitySetsPerSecond = 5
	// MaxURL and MaxTitle bound a media.play.
	MaxURL   = 2048
	MaxTitle = 100
)

// Sounds are the fixed set sound.play plays by name.
var Sounds = []string{"chime", "done", "error", "tick"}

// CommandReply answers a command event that asked for one (Call not 0).
type CommandReply struct {
	Call uint64 `json:"call"`
	OK   bool   `json:"ok"`
	// Text is what the command says back, which Home Assistant may speak; or, with OK false, why it
	// did not work.
	Text string `json:"text,omitempty"`
}

// Fire is event.fire: esphome.techo5_addon on Home Assistant's bus, with the add-on's id, name and
// data.
type Fire struct {
	Name string            `json:"name"`
	Data map[string]string `json:"data,omitempty"`
}

// Speak is speak: words for Home Assistant to say on this device.
type Speak struct {
	Text string `json:"text"`
}

// Queued is speak's answer: how many of the add-on's words wait to be said, these included.
type Queued struct {
	Queued int `json:"queued"`
}

// Dropped is speak.cancel's answer.
type Dropped struct {
	Dropped int `json:"dropped"`
}

// Sound is sound.play: one of Sounds by Name, or a WAV among the add-on's extra files by File.
type Sound struct {
	Name string `json:"name,omitempty"`
	File string `json:"file,omitempty"`
}

// TimerStart is timer.start.
type TimerStart struct {
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
}

// TimerRef names one of the add-on's timers: timer.cancel's arguments, timer.start's answer.
type TimerRef struct {
	ID string `json:"id"`
}

// Timer is one of the add-on's timers, as timer.list lists it.
type Timer struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Left    float64 `json:"left"`  // seconds
	Total   float64 `json:"total"` // seconds
	Running bool    `json:"running"`
}

// Timers is timer.list's answer.
type Timers struct {
	Timers []Timer `json:"timers"`
}

// AwakeHold is awake.hold's arguments; with Handle, awake.renew's.
type AwakeHold struct {
	Handle  string  `json:"handle,omitempty"`
	Seconds float64 `json:"seconds"`
}

// Awake is awake.hold's and awake.renew's answer: the handle, and when it runs out unless renewed.
type Awake struct {
	Handle string    `json:"handle"`
	Until  time.Time `json:"until"`
}

// AwakeRelease is awake.release.
type AwakeRelease struct {
	Handle string `json:"handle"`
}

// Values is an add-on's settings, by key: settings.get's answer and the settings event's data.
type Values struct {
	Values map[string]any `json:"values"`
}

// SettingsReply answers a settings.test event.
type SettingsReply struct {
	Call uint64 `json:"call"`
	OK   bool   `json:"ok"`
	// Text is why not, with OK false; shown on the card, at most MaxTestAnswer characters.
	Text string `json:"text,omitempty"`
}

// EntitySet is entity.set: one of the add-on's entities, by its id in addon.json, and its state.
type EntitySet struct {
	ID    string `json:"id"`
	State any    `json:"state"`
}

// Media kinds.
const (
	MediaVideo = "video"
	MediaAudio = "audio"
)

// MediaPlay is media.play: a video full screen, or audio on the speaker, from an http(s) address.
type MediaPlay struct {
	URL   string `json:"url"`
	Kind  string `json:"kind"`
	Title string `json:"title,omitempty"`
}

// None is the arguments of an op that takes none, and the result of one that says nothing.
type None struct{}

// CommandEvent is a command called from Home Assistant: by the addon_command action, which waits for
// a CommandReply when Call is not 0, or by the command's button.
type CommandEvent struct {
	Call    uint64         `json:"call,omitempty"`
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
	// From is "action" or "button".
	From string `json:"from"`
}

// Timer states a timer event carries.
const (
	TimerRinging  = "ringing"
	TimerStopped  = "stopped"
	TimerCanceled = "canceled"
)

// TimerEvent is one of the add-on's timers ringing, its ring stopped, or it canceled on the device.
type TimerEvent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// SettingsTest asks whether these values, the form's, work; answered with a SettingsReply.
type SettingsTest struct {
	Call   uint64         `json:"call"`
	Values map[string]any `json:"values"`
}

// EntityCommand is Home Assistant setting one of the add-on's entities: a switch turned, an option
// picked, a number set (State), or a button pressed (Press).
type EntityCommand struct {
	ID    string `json:"id"`
	State any    `json:"state,omitempty"`
	Press bool   `json:"press,omitempty"`
}

// MediaEvent is what became of the add-on's media: "ended" by itself, "stopped" by someone or
// something else, or "failed".
type MediaEvent struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}
