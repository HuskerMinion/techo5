package home

import (
	"log/slog"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A camera's own sound, on this device's speaker, for as long as its view is up.
//
// The picture is fetched from Home Assistant one snapshot at a time (camera.go). The sound cannot be:
// it is a stream of AAC inside the camera's own video, and the device decodes nothing for the
// speaker but the WAV Home Assistant converts for it. So the sound is asked for the other way round —
// Home Assistant is told to play the camera's stream *to this device's media player*
// (camera.play_stream), and the stream it sends back is the ordinary one every other track arrives
// on: converted with ffmpeg on the way, streamed, and played by the media player like a radio
// station. Nothing about the camera's address, codecs or credentials is known here, which is the
// whole reason for doing it this way: every camera Home Assistant can stream is one this can hear.

// cameraSoundPoll is how often a view's sound is checked against the view still being up. A second is
// shorter than any view anybody asks for, and the check is a comparison under a lock. A variable only so
// that a test of what the watching does does not have to wait a second to see it.
var cameraSoundPoll = time.Second

// CameraSound is whether a camera opened from the screen or by voice brings its audio with it.
func CameraSound() bool { return config.Get().Home.CameraSound }

// buildCameraSoundSwitch is the setting as Home Assistant's switch.
func (f *Feature) buildCameraSoundSwitch() {
	f.cameraSoundSw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "camera_sound",
			Name:     "Camera sound",
			Icon:     "mdi:volume-high",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) { f.SetCameraSound(on) },
	}
}

// SetCameraSound saves the choice and shows it in Home Assistant. A view that is already up is left
// alone: it was asked for under the old answer, and taking its sound away halfway through would be
// stranger than letting the next one start quiet.
func (f *Feature) SetCameraSound(on bool) {
	if err := config.Set().Home().CameraSound(on); err != nil {
		slog.Error("saving the camera sound switch failed", "err", err)
		return
	}
	f.cameraSoundSw.Set(on)
	slog.Info("setting changed", "setting", "camera_sound", "using", on)
}

// soundAsked reads the home_show_camera_sound action's sound argument: "on" or "off" for this one
// view, anything else — an empty string included — for what the device's own setting says.
func soundAsked(arg string, setting bool) bool {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "on", "true", "yes", "1":
		return true
	case "off", "false", "no", "0":
		return false
	}
	return setting
}

// overPlayer is the media player's side of a sound played over the music. It is a field of functions so
// that what this feature decides about a view's sound can be tested without a speaker: which request
// belongs to which view, what a tap does to it, and when one is asked for again, are all decisions about
// tokens rather than about audio.
type overPlayer struct {
	Ask     func() media.OverToken
	State   func(media.OverToken) media.OverState
	Mute    func(media.OverToken, bool)
	Stop    func(media.OverToken)
	Drop    func(media.OverToken)
	Settled func(media.OverToken)
}

var over = overPlayer{
	Ask:     func() media.OverToken { return media.Get().OverNext() },
	State:   func(t media.OverToken) media.OverState { return media.Get().OverState(t) },
	Mute:    func(t media.OverToken, on bool) { media.Get().MuteOver(t, on) },
	Stop:    func(t media.OverToken) { media.Get().StopOver(t) },
	Drop:    func(t media.OverToken) { media.Get().ForgetOverNext(t) },
	Settled: func(t media.OverToken) { media.Get().OverSettled(t) },
}

// soundEnv is what a view's sound needs from the rest of the program, taken once when the work starts.
//
// A goroutine is given its own copy rather than reading these variables itself: a watcher outlives the view
// it was started for by up to a poll, and one still reading the package's player after a later view — or a
// test — replaced it would be answering a token that player never handed out. That is a data race as well
// as nonsense, and the release checks run with -race.
type soundEnv struct {
	player overPlayer
	call   func(entity, player string) error
	poll   time.Duration
}

// thisDevice is the running device's side of all this.
func thisDevice() soundEnv {
	return soundEnv{player: over, call: playStream, poll: cameraSoundPoll}
}

// startCameraSound asks Home Assistant to play this camera's audio on this device and watches it for as
// long as the view lasts.
//
// The ask is made before the call, because the url the call produces can arrive before the call returns
// — the service is answered only once the stream has been set up and sent — and a url that arrives with
// no ask of its own would be played as a track, over the music it was meant to be heard over.
func (f *Feature) startCameraSound(entity string, env soundEnv) {
	if entity == LocalCamera {
		// The device's own camera has no audio to play, and it is not a Home Assistant camera to ask
		// about.
		return
	}
	token := env.player.Ask()
	f.mu.Lock()
	f.camSound, f.camOver = entity, token
	f.mu.Unlock()

	go f.watchCameraSound(entity, token, env)
	go f.askCameraSound(entity, token, env)
}

// playStream is the call that asks Home Assistant for a camera's stream on this device. It is a variable
// so that what this feature decides about a request and its answer can be tested without a Home
// Assistant: the request is answered by a stream arriving later, which the call itself says nothing about.
var playStream = func(entity, player string) error {
	// The device's own media player is what it is played on, by the device's own ask on itself, rather
	// than by a script on the other side having to know which camera this is.
	return hass.Get().Call("camera", "play_stream", map[string]any{
		"entity_id":    entity,
		"media_player": player,
		"format":       "hls",
	})
}

// askCameraSound makes the call a request stands for. Nothing is waited for: the request is answered by a
// stream that arrives later as an ordinary media url, which the player plays under this token.
func (f *Feature) askCameraSound(entity string, token media.OverToken, env soundEnv) {
	err := env.call(entity, speakerEntity())

	// Nothing more is coming from this call, whichever way it went: a url still on its way has only just
	// been sent, so a request that was given up on can stop waiting for it shortly. Until then the ask
	// stands, because a slow call is answered slowly — fifteen seconds is one Home Assistant has been seen
	// to take — and an ask forgotten before that lets its url through as a track.
	env.player.Settled(token)

	if err == nil {
		slog.Info("camera sound on", "entity", entity)
		return
	}

	// A stream can be on its way even when the call that started it fails: the service is answered only
	// once the stream has been sent, so a slow one times out with the sound already playing. A sound that
	// has arrived is left playing, and left stoppable; a request that nothing has answered is given up on.
	slog.Warn("camera sound", "entity", entity, "err", err)
	if env.player.State(token) != media.OverPlaying {
		env.player.Drop(token)
	}
}

// CameraSoundOn is whether the view on the screen has a sound of its own — it was asked for, whether or
// not it is playing now. It is what the screen draws the control from, and it is what makes the control
// a toggle rather than a one-way door: a sound somebody silenced has to be brought back from somewhere,
// and closing the view to open it again is not an answer at a doorbell.
func (f *Feature) CameraSoundOn() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.camSound != ""
}

// CameraSoundLive is whether that sound is playing or on its way, which is what the control says: Mute
// while it is, and Unmute when it is not — silenced from the screen, taken by a reply or an
// announcement, never answered, or connected and silenced from the screen.
//
// It is asked of the media player rather than remembered here, because a sound can end without this
// feature being told: a reply or an announcement claims the speaker, and what was playing over the music
// goes with it.
func (f *Feature) CameraSoundLive() bool {
	f.mu.Lock()
	token := f.camOver
	f.mu.Unlock()
	return over.State(token).Live()
}

// ToggleCameraSound silences this view's sound, or brings it back. The picture stays either way, which is
// the whole use of it at a doorbell.
//
// Muting a sound that is still on its way mutes it rather than giving the request up: the stream arrives
// and is read and thrown away, so the sound is back the moment it is asked for rather than a few seconds
// later. What is playing and what to do about it are read and changed under the one lock, so two taps
// cannot both start a request — the second finds the first's sound and silences or brings back that.
func (f *Feature) ToggleCameraSound() {
	env := thisDevice()

	f.mu.Lock()
	entity, token := f.camSound, f.camOver
	if entity == "" {
		f.mu.Unlock()
		return // this view has no sound of its own to silence, and none to ask for
	}

	switch env.player.State(token) {
	case media.OverComing, media.OverPlaying:
		f.camMuted = true
		f.mu.Unlock()
		env.player.Mute(token, true)
		slog.Info("camera sound silenced from the screen", "entity", entity)
		return
	case media.OverMuted:
		f.camMuted = false
		f.mu.Unlock()
		env.player.Mute(token, false)
		slog.Info("camera sound heard again", "entity", entity)
		return
	}

	// Silenced because something took the speaker, or because nothing ever answered: a tap asks for the
	// sound again, and it is wanted, so the request is not a muted one.
	f.camMuted = false
	token = env.player.Ask()
	f.camOver = token
	go f.watchCameraSound(entity, token, env)
	f.mu.Unlock()

	slog.Info("camera sound asked for again", "entity", entity)
	go f.askCameraSound(entity, token, env)
}

// askAgain takes a fresh request for the sound of a view that still wants it, which is how a sound taken
// by a reply or an announcement comes back. It reports the token to watch next, or nothing if the view
// gave its sound up in the meantime.
func (f *Feature) askAgain(entity string, watched media.OverToken, env soundEnv) media.OverToken {
	// The check and the new request are one step, under the one lock. A tap can land between a watcher
	// finding its sound taken and asking for it again, and asking anyway would put two requests out for one
	// control — the second url with no ask of its own, played as a track.
	f.mu.Lock()
	if f.camSound != entity || f.camOver != watched {
		f.mu.Unlock()
		return 0
	}
	token := env.player.Ask()
	f.camOver = token
	muted := f.camMuted
	f.mu.Unlock()

	// A sound silenced from the screen is asked for again silenced, and kept connected: it is asked for so
	// that it is there to bring back, not so that it starts talking after an announcement.
	env.player.Mute(token, muted)

	slog.Info("camera sound asked for again after it was taken", "entity", entity, "silenced", muted)
	go f.askCameraSound(entity, token, env)
	return token
}

// cameraViewUp is whether this camera is the view on the screen now.
func (f *Feature) cameraViewUp(entity string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cam.Entity == entity && time.Now().Before(f.cam.Until)
}

// cameraSoundMine is whether the view's sound is still the one this request was made for: the view has
// not given its sound up, and no newer request has taken its place.
func (f *Feature) cameraSoundMine(entity string, token media.OverToken) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.camSound == entity && f.camOver == token
}

// cameraSoundOff is the view being over: its sound goes with it, and the view forgets it had one, so the
// next view starts clean. A sound that is already over is nothing to stop — the watcher calls this on
// the way out of every view with a sound, whether or not there was ever one.
func (f *Feature) cameraSoundOff(entity string, token media.OverToken, env soundEnv) {
	env.player.Stop(token)
	f.mu.Lock()
	if f.camSound == entity && f.camOver == token {
		f.camSound, f.camOver = "", 0
	}
	f.mu.Unlock()
}

// watchCameraSound takes the view's sound off the speaker when the view ends: its time runs out,
// somebody taps it away, or another camera replaces it. A view that closed with the yard still playing
// would be a device nobody can silence from the screen, which is the one thing this feature must not do.
//
// It watches one request, not one view: a sound taken from this view by a reply or an announcement is
// asked for again, and this goes on watching the new one.
func (f *Feature) watchCameraSound(entity string, token media.OverToken, env soundEnv) {
	for {
		time.Sleep(env.poll)

		if !f.cameraViewUp(entity) {
			// The view is over, whoever has the screen now: this sound goes with it.
			f.cameraSoundOff(entity, token, env)
			return
		}
		if !f.cameraSoundMine(entity, token) {
			return // the view is up, but this request has been given up on or replaced
		}
		if env.player.State(token) == media.OverTaken {
			// A reply or an announcement claimed the speaker, so the sound went with it. The view still
			// wants one, so it is asked for again, and that request waits its turn behind what is being
			// said rather than cutting it off.
			if token = f.askAgain(entity, token, env); token == 0 {
				return
			}
		}
	}
}
