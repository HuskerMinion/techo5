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
// shorter than any view anybody asks for, and the check is a comparison under a lock.
const cameraSoundPoll = time.Second

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

// startCameraSound asks Home Assistant to play this camera's audio on this device.
//
// Nothing is waited for: the request is answered by a stream that arrives later as an ordinary media
// URL, which cameraSoundPlaying recognizes. A request that is refused, or a camera Home Assistant
// cannot stream, therefore leaves the view silent and nothing to stop.
//
// The URL is asked to be played *over* whatever is playing rather than instead of it: the music keeps
// going, ducked, and comes back up when the view ends, so hearing the door does not end what the room
// was listening to and does not take this device out of a Music Assistant group. That ask is made
// before the call, because the URL can arrive before the call returns — the service is answered only
// once the stream has been set up and sent — and a URL that arrives first is played as a track.
func (f *Feature) startCameraSound(entity string) {
	if entity == LocalCamera {
		// The device's own camera has no audio to play, and it is not a Home Assistant camera to ask
		// about.
		return
	}
	f.mu.Lock()
	f.camSound, f.camSoundURL = entity, ""
	f.mu.Unlock()

	// The device's own media player is what it is played on, by the device's own action on itself,
	// rather than by a script on the other side having to know which camera this is.
	media.Get().OverNext()
	err := hass.Get().Call("camera", "play_stream", map[string]any{
		"entity_id":    entity,
		"media_player": speakerEntity(),
		"format":       "hls",
	})
	if err != nil {
		slog.Warn("camera sound", "entity", entity, "err", err)
		// Nothing is coming, so the ask goes with the request: left standing it would take the next
		// track of somebody's music for a camera's sound.
		media.Get().ForgetOverNext()
		f.mu.Lock()
		f.camSound = ""
		f.mu.Unlock()
		return
	}
	slog.Info("camera sound on", "entity", entity)
}

// cameraSoundPlaying notes the URL of the stream that answered a camera sound request. The player
// holds one stream at a time, so a play of anything else afterwards — somebody's music — leaves this
// behind, and cameraSoundEnds then finds the sound is no longer this view's to stop.
func (f *Feature) cameraSoundPlaying(url string) {
	f.mu.Lock()
	if f.camSound != "" && f.camSoundURL == "" {
		f.camSoundURL = url
	}
	f.mu.Unlock()
}

// CameraSoundPlaying is whether the view on the screen has a sound playing for it, which is what the
// screen asks before drawing the control that silences it.
func (f *Feature) CameraSoundPlaying() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.camSound != "" && f.camSoundURL != ""
}

// SilenceCameraSound stops this view's sound without taking the view down, which is what the screen's
// control asks for and what somebody at a doorbell wants: the picture stays and the sound stops.
func (f *Feature) SilenceCameraSound() {
	if f.takeCameraSound() {
		media.Get().StopOver()
		slog.Info("camera sound silenced from the screen")
	}
}

// takeCameraSound gives up this view's claim on the sound and says whether what is playing is still
// its own, and so whether there is anything to stop. The claim goes either way: the view gives it up
// as well as stopping the sound, so nothing has to remember that it was silenced — the watcher finds
// nothing of this view's to stop when the view ends, and the next view is a fresh one that follows the
// setting again.
//
// A track that something else started in the meantime is not this view's to stop, which is the rule
// the end of a view follows as well.
func (f *Feature) takeCameraSound() (ours bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ours = f.camSound != "" && f.camSoundURL != "" && f.camSoundURL == f.url
	f.camSound, f.camSoundURL = "", ""
	return ours
}

// cameraSoundEnds is what a view coming down means for its sound, read under the lock: whether the
// view is still up, and whether what is playing is still the track this view started. The two are
// answered together and before anything is cleared, which is the whole reason they are not asked
// separately.
func (f *Feature) cameraSoundEnds(entity string) (up, ours bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mine := f.camSound == entity
	up = f.cam.Entity == entity && time.Now().Before(f.cam.Until)
	ours = mine && f.camSoundURL != "" && f.camSoundURL == f.url
	if !up && mine {
		f.camSound, f.camSoundURL = "", ""
	}
	return up, ours
}

// watchCameraSound takes the camera's audio off the speaker when the view it belongs to ends: its
// time runs out, somebody taps it away, or another camera replaces it. A view that closed with the
// yard still playing would be a device nobody can silence from the screen, which is the one thing
// this feature must not do.
//
// A track that something else took over in the meantime is left alone: it is not this view's any
// more, and stopping it would be stopping somebody's music because a camera they looked at once
// closed.
func (f *Feature) watchCameraSound(entity string) {
	for {
		time.Sleep(cameraSoundPoll)
		up, ours := f.cameraSoundEnds(entity)
		if up {
			continue
		}
		if ours {
			media.Get().StopOver()
			slog.Info("camera sound off", "entity", entity)
		}
		return
	}
}
