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
	// EntitySetsPerSecond is how often one entity's state may be set, and StatesPerSecond how often
	// any of an add-on's may, all together. A state an entity already has is not counted.
	EntitySetsPerSecond = 5
	StatesPerSecond     = 5
	// TimerOpBurst and TimerOpEvery bound timer.start and timer.cancel together: that many at once,
	// then one every TimerOpEvery.
	TimerOpBurst = 8
	TimerOpEvery = 10 * time.Second
	// MediaPlayBurst and MediaPlayEvery bound media.play: that many at once, then one every
	// MediaPlayEvery.
	MediaPlayBurst = 3
	MediaPlayEvery = 30 * time.Second
	// MaxURL and MaxTitle bound a media.play.
	MaxURL   = 2048
	MaxTitle = 100
	// MediaPositionEvery is the most often a media.state event says only that the position moved:
	// a change of state is said at once (within MediaCoalesce), a playing track's position at most this
	// often.
	MediaPositionEvery = 10 * time.Second
	// MediaCoalesce is how long changes to an add-on's media are gathered before the latest is said, so
	// a burst of them (loading, playing, paused) costs one event: at most two a second, besides the one
	// end each media.play has.
	MediaCoalesce = 500 * time.Millisecond
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

// MediaStarted is media.play's answer: the id the media.state events of what it started carry.
type MediaStarted struct {
	Media string `json:"media"`
}

// Media states a media.state event and media.status say.
const (
	// MediaIdle is media.status for an add-on that has started nothing since the daemon started.
	MediaIdle = "idle"
	// MediaBuffering is media started, or moved through, whose picture or sound has not arrived yet.
	MediaBuffering = "buffering"
	MediaPlaying   = "playing"
	MediaPaused    = "paused"
	// MediaStopped is media that is over, for the Reason given. Nothing follows it for that media.
	MediaStopped = "stopped"
)

// Reasons a media.state that says MediaStopped gives.
const (
	// MediaEnded is media that played to its end.
	MediaEnded = "ended"
	// MediaStoppedOnDevice is media stopped on the device: its Stop button, the stop word, Home
	// Assistant, the sleep timer, the Video switch turned off.
	MediaStoppedOnDevice = "device"
	// MediaReplaced is media something else was played in place of: the add-on's own next media.play,
	// or anything else, which the add-on is not told about.
	MediaReplaced = "replaced"
	// MediaError is media that could not be fetched or played.
	MediaError = "error"
	// MediaStoppedByAddon is media the add-on stopped itself (media.stop).
	MediaStoppedByAddon = "addon"
)

// MediaState is what media an add-on started is doing: a media.state event's data, and media.status's
// answer. It is only ever about media the add-on started itself.
type MediaState struct {
	// Media is media.play's id for it; empty with State idle.
	Media string `json:"media,omitempty"`
	// Kind is MediaVideo or MediaAudio.
	Kind  string `json:"kind,omitempty"`
	State string `json:"state"`
	// Reason is why, with State stopped.
	Reason string `json:"reason,omitempty"`
	// PositionMs is how far into it the device is, in milliseconds; DurationMs how long it is. Each is
	// left out when the device cannot tell: audio says no length, and a live stream none either.
	PositionMs *int64 `json:"position_ms,omitempty"`
	DurationMs *int64 `json:"duration_ms,omitempty"`
}

// Position is PositionMs as a duration, and whether it is known.
func (m MediaState) Position() (time.Duration, bool) { return msOf(m.PositionMs) }

// Duration is DurationMs as a duration, and whether it is known.
func (m MediaState) Duration() (time.Duration, bool) { return msOf(m.DurationMs) }

func msOf(p *int64) (time.Duration, bool) {
	if p == nil {
		return 0, false
	}
	return time.Duration(*p) * time.Millisecond, true
}

// Ms is d as a MediaState's milliseconds.
func Ms(d time.Duration) *int64 {
	v := d.Milliseconds()
	return &v
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
// Quiet is a timer that ran out in quiet hours: it made no sound, and no "stopped" follows.
type TimerEvent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Quiet bool   `json:"quiet,omitempty"`
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

// Stage 2: the screen. Scenes and their blocks are in scene.go.

// SceneSet is scene.set: the whole scene for the add-on's page or its card (OnPage, OnCard).
type SceneSet struct {
	On    string `json:"on"`
	Scene Block  `json:"scene"`
}

// ScenePatch is scene.patch: the block of this id in the page's or the card's scene replaced by Block,
// which keeps the id, so a timer's chip or a ticked item does not send the whole page again.
type ScenePatch struct {
	On    string `json:"on"`
	ID    string `json:"id"`
	Block Block  `json:"block"`
}

// TouchEvent is a tap on one of the add-on's buttons or chips, by its id, on its page or its card.
type TouchEvent struct {
	ID string `json:"id"`
	On string `json:"on"`
}

// SwipeEvent is a swipe left or right across the add-on's page (SwipeLeft, SwipeRight). A swipe on the
// card is the daemon's: it puts the card away.
type SwipeEvent struct {
	Direction string `json:"direction"`
}

// ScreenStateEvent is the add-on's page or card coming into view or going out of it (Shown), so an
// add-on knows when what it shows is seen. Dismissed is a card the owner swiped away: it is gone, and
// comes back only with the next scene.set for the card.
type ScreenStateEvent struct {
	On        string `json:"on"`
	Shown     bool   `json:"shown"`
	Dismissed bool   `json:"dismissed,omitempty"`
}
