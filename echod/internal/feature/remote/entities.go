package remote

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	esphome "github.com/ygelfand/go-esphome-device"
)

// What the API may change. Home Assistant may change everything, over a link it encrypts and a device it
// was let into; the API is a key in a script, and may change the everyday settings and nothing that is
// about privacy or security. Those are read-only here, by name (readOnly), and so is every diagnostic
// entity, whatever it is. A test walks every entity the device has and fails on a new one whose name says
// privacy or security and that is not named here, so one added later is not writable by default.

// readOnly is every entity the API may read and not change.
var readOnly = []string{
	// Doors on the network, and this API itself.
	"remote_api", "ssh", "setup_page", "camera_web_access", "screen_web_access", "remote_adb", "insecure_tls",
	"airplay", "airplay_2", "spotify_connect", "dlna", "dlna_video", "video", "sendspin", "bluetooth_proxy",
	// Listening, watching and keeping.
	"mic_mute", "intercom_drop_in", "talk_back", "camera_live", "keep_wake_clips", "keep_recordings_1",
	"keep_recordings_2", "presence", "presence_detection", "gestures", "wake_word_on_home_assistant",
	"confirm_wake_with_home_assistant",
	// When the microphones open: a press that starts listening, and how readily a word or a sound does.
	"wake_assistant_1", "wake_assistant_2", "wake_threshold_1", "wake_threshold_2", "confirm_threshold",
	"max_listen_1", "max_listen_2",
	// New uses of the microphones and the camera: measuring the room, listening for claps, a finger held
	// on the clock, serving the camera, and which device answers a wake word.
	"ambient_noise_sensor", "clap_gesture", "clock_hold_gesture", "camera_streaming", "camera_login_from_ha",
	"nearest_device_answers", "person_detection",
	// Taking settings from the other devices.
	"settings_sharing",
	// Calls, and what may end one or listen during one.
	"phone", "phone_answer", "phone_hangup", "phone_peer", "wake_word_during_calls", "assistant_replies_into_calls",
	"hear_call_under_assistant", "hangup_word", "hangup_word_threshold", "call_contacts_by_voice",
	// And whether a call is encrypted at all.
	"phone_plain_sip",
	// Who can get in, and what it is paired with.
	"settings_lock", "bluetooth_pairing", "bluetooth_phone_pairing", "bluetooth_forget", "bluetooth_forget_phones",
	"bluetooth_secure_connections",
	// What it runs.
	"check_for_updates", "update_automatically", "update_channel", "restart", "purge_cache",
	// The tailnet, and setting up from afar.
	"tailscale", "setup_remote", "setup_let_in",
}

// Writable is whether the API may change e. Exported for the test that walks every entity the device has.
func Writable(e esphome.Entity) bool {
	b := base(e)
	if b == nil || b.Category == esphome.CategoryDiagnostic || slices.Contains(readOnly, b.ObjectID) {
		return false
	}
	switch e.(type) {
	case *esphome.Switch, *esphome.Number, *esphome.Select, *esphome.Text, *esphome.Button:
		return true
	}
	return false
}

// base is an entity's shared fields, for the kinds the API knows.
func base(e esphome.Entity) *esphome.Base {
	switch v := e.(type) {
	case *esphome.Switch:
		return &v.Base
	case *esphome.Number:
		return &v.Base
	case *esphome.Select:
		return &v.Base
	case *esphome.Text:
		return &v.Base
	case *esphome.Button:
		return &v.Base
	case *esphome.Sensor:
		return &v.Base
	case *esphome.BinarySensor:
		return &v.Base
	case *esphome.TextSensor:
		return &v.Base
	}
	return nil
}

// entity is one entity as the API shows it.
type entity struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	State    any      `json:"state,omitempty"`
	Writable bool     `json:"writable"`
	Options  []string `json:"options,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Step     *float64 `json:"step,omitempty"`
	Unit     string   `json:"unit,omitempty"`
}

// unlisted is the entities the API leaves out altogether: what was last said in the room, and the
// answer to it. They are words people spoke, and the API travels the network unencrypted.
var unlisted = []string{"last_heard", "last_reply"}

// list is every entity the API knows the kind of.
func (f *Remote) list() []entity {
	var out []entity
	for _, e := range f.entities() {
		b := base(e)
		if b == nil || slices.Contains(unlisted, b.ObjectID) {
			continue
		}
		x := entity{ID: b.ObjectID, Name: b.Name, Writable: Writable(e)}
		switch v := e.(type) {
		case *esphome.Switch:
			x.Kind, x.State = "switch", v.Get()
		case *esphome.Number:
			x.Kind, x.State, x.Unit = "number", v.Get(), v.Unit
			x.Min, x.Max, x.Step = &v.Min, &v.Max, &v.Step
		case *esphome.Select:
			x.Kind, x.State, x.Options = "select", v.Get(), v.Options
		case *esphome.Text:
			x.Kind, x.State = "text", v.Get()
		case *esphome.Button:
			x.Kind = "button"
		case *esphome.Sensor:
			x.Kind, x.State, x.Unit = "sensor", v.Get(), v.Unit
		case *esphome.BinarySensor:
			x.Kind, x.State = "binary_sensor", v.Get()
		case *esphome.TextSensor:
			x.Kind, x.State = "text_sensor", v.Get()
		}
		out = append(out, x)
	}
	slices.SortFunc(out, func(a, b entity) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
	return out
}

// set changes one entity, as Home Assistant would: through what the device does when it is told to.
// The body is {"state": value}; a button takes an empty body.
func (f *Remote) set(w http.ResponseWriter, id string, body []byte) {
	var e esphome.Entity
	for _, x := range f.entities() {
		if b := base(x); b != nil && b.ObjectID == id && !slices.Contains(unlisted, id) {
			e = x
			break
		}
	}
	if e == nil {
		http.Error(w, "no entity "+strconv.Quote(id), http.StatusNotFound)
		return
	}
	if !Writable(e) {
		http.Error(w, id+" is read-only through the API", http.StatusForbidden)
		return
	}

	var req struct {
		State json.RawMessage `json:"state"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "that is not JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	bad := func(want string) { http.Error(w, id+" takes "+want, http.StatusBadRequest) }

	switch v := e.(type) {
	case *esphome.Button:
		if v.OnPress != nil {
			v.OnPress()
		}
	case *esphome.Switch:
		var on bool
		if json.Unmarshal(req.State, &on) != nil {
			bad(`{"state": true} or false`)
			return
		}
		if v.OnCommand != nil {
			v.OnCommand(on)
		} else {
			v.Set(on)
		}
	case *esphome.Number:
		var n float32
		if json.Unmarshal(req.State, &n) != nil {
			bad(`{"state": a number}`)
			return
		}
		if float64(n) < v.Min || float64(n) > v.Max {
			http.Error(w, id+" takes a number from "+fmtNum(v.Min)+" to "+fmtNum(v.Max), http.StatusBadRequest)
			return
		}
		if v.OnCommand != nil {
			v.OnCommand(n)
		} else {
			v.Set(n)
		}
	case *esphome.Select:
		var s string
		if json.Unmarshal(req.State, &s) != nil || !slices.Contains(v.Options, s) {
			bad("one of its options")
			return
		}
		if v.OnCommand != nil {
			v.OnCommand(s)
		} else {
			v.Set(s)
		}
	case *esphome.Text:
		var s string
		if json.Unmarshal(req.State, &s) != nil {
			bad(`{"state": "some text"}`)
			return
		}
		if v.OnCommand != nil {
			v.OnCommand(s)
		} else {
			v.Set(s)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
