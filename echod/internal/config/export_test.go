package config

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// secretish is a field name that sounds like it could hold a secret.
var secretish = regexp.MustCompile(`(?i)key|token|pass|pin|word|secret|cred`)

// notSecret is every field whose name sounds like a secret and is not one, by its path. A new field that
// sounds like one has to be put either here or in ClearSecrets and KeepSecrets, which is the point.
var notSecret = []string{
	"Alarms.Snoozed.Key", // which alarm a snooze is of
	"Alarms.Held.Key",    // which alarm the wake light waits for
}

// each calls f with the path of every string in v, field, slice element or map value, creating what it
// has to on the way so there is one: an element in an empty slice, a value behind a nil pointer, an entry
// in an empty map. f gets the value to set and the name of the field it is in.
func each(v reflect.Value, path, name string, f func(path, name string, set func(string))) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		each(v.Elem(), path, name, f)
	case reflect.Slice:
		if v.Len() == 0 {
			v.Set(reflect.Append(v, reflect.New(v.Type().Elem()).Elem()))
		}
		for i := range v.Len() {
			each(v.Index(i), path, name, f)
		}
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return
		}
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		if v.Len() == 0 {
			v.SetMapIndex(reflect.ValueOf("k").Convert(v.Type().Key()), reflect.New(v.Type().Elem()).Elem())
		}
		for _, k := range v.MapKeys() {
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(v.MapIndex(k))
			each(e, path, name, f)
			v.SetMapIndex(k, e)
		}
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			return
		}
		for i := range v.NumField() {
			field := v.Type().Field(i)
			if !field.IsExported() || field.Tag.Get("json") == "-" {
				continue
			}
			each(v.Field(i), strings.TrimPrefix(path+"."+field.Name, "."), field.Name, f)
		}
	case reflect.String:
		f(path, name, func(s string) { v.SetString(s) })
	}
}

// Every field that sounds like a secret is either cleared from a settings file or named as not one, so
// a key added later cannot leave the device in one because nobody remembered this file.
func TestNoSecretLeavesInAFile(t *testing.T) {
	c := Defaults()
	var paths []string
	each(reflect.ValueOf(&c).Elem(), "", "", func(path, name string, set func(string)) {
		if secretish.MatchString(name) {
			paths = append(paths, path)
			set("s3cret-" + strings.ReplaceAll(path, ".", "-"))
		}
	})
	if len(paths) < 8 {
		t.Fatalf("found only %d secret-sounding fields, the walk is broken: %v", len(paths), paths)
	}
	file, err := Export(c, "test", "v0")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		marker := "s3cret-" + strings.ReplaceAll(p, ".", "-")
		if strings.Contains(string(file), marker) && !slices.Contains(notSecret, p) {
			t.Errorf("%s left the device in a settings file: clear it in ClearSecrets (and keep it in KeepSecrets), or name it in notSecret", p)
		}
	}
}

// No address with a user and password in it leaves the device, whichever setting it is in.
func TestNoPasswordInAnAddressLeaves(t *testing.T) {
	c := Defaults()
	n := 0
	each(reflect.ValueOf(&c).Elem(), "", "", func(_, _ string, set func(string)) {
		n++
		set("rtsp://admin:hunter2@192.0.2.9:554/stream")
	})
	file, err := Export(c, "test", "v0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), "hunter2") {
		t.Errorf("a password in an address left the device (%d strings seeded)", n)
	}
}

func TestCalendarAndCameraAddressesStay(t *testing.T) {
	c := Defaults()
	c.Calendar.Links = []CalendarLink{{ID: "a", Name: "Home", URL: "https://p01-caldav.example/private/abc"}}
	c.TalkBack.Cameras = map[string]string{"camera.porch": "rtsp://192.0.2.9/porch"}
	file, err := Export(c, "test", "v0")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"private/abc", "rtsp://192.0.2.9/porch"} {
		if strings.Contains(string(file), s) {
			t.Errorf("%s left the device", s)
		}
	}
}

func TestARestoreKeepsTheDevicesOwn(t *testing.T) {
	from := Defaults()
	from.Screen.Clock24 = true
	from.Speaker.Volume = 3
	from.Alarms.List = []Alarm{{ID: "a1", Hour: 7, Label: "Up"}}
	from.Security.SSH = true
	from.Diag.RemoteADB, from.Diag.InsecureTLS = true, true
	from.Streaming.AirPlay = true
	from.Video.On = true
	from.Video.Allowed = []AllowedAddr{{Addr: "192.0.2.66"}}

	here := Defaults()
	here.Device = Device{Name: "Kitchen"}
	here.Brain.LLM, here.Brain.Key = "http://192.0.2.5:8080/v1", "mine"
	here.Home.HouseWord = "lantern"
	here.Security.LockPIN = "1234"
	here.Security.Camera = true
	here.Bluetooth.Audio = "speaker"
	here.Timers.Local = []LocalTimer{{ID: "local:x", Total: time.Minute}}
	from.Brain.LLM = here.Brain.LLM

	file, err := Export(from, "Echo Show 5 2nd gen (cronos)", "v0")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Restored(here, file, "Echo Show 5 2nd gen (cronos)")
	if err != nil {
		t.Fatal(err)
	}
	got := r.Config
	if !got.Screen.Clock24 || got.Speaker.Volume != 3 || len(got.Alarms.List) != 1 {
		t.Errorf("the file's settings did not come across: clock24 %v volume %d alarms %d",
			got.Screen.Clock24, got.Speaker.Volume, len(got.Alarms.List))
	}
	if got.Brain.Key != "mine" || got.Home.HouseWord != "lantern" || got.Security.LockPIN != "1234" {
		t.Errorf("this device's secrets were not kept: %q %q %q", got.Brain.Key, got.Home.HouseWord, got.Security.LockPIN)
	}
	if got.Security.SSH || !got.Security.Camera {
		t.Errorf("the privacy switches came from the file: ssh %v camera %v", got.Security.SSH, got.Security.Camera)
	}
	if got.Diag.RemoteADB || got.Diag.InsecureTLS || got.Streaming.AirPlay || got.Video.On || len(got.Video.Allowed) != 0 {
		t.Errorf("a door was opened by the file: %+v %+v %+v", got.Diag, got.Streaming, got.Video)
	}
	if got.Bluetooth.Audio != "speaker" || len(got.Timers.Local) != 1 || got.Device.Name != "Kitchen" {
		t.Errorf("this device's own were not kept: %+v %+v %+v", got.Bluetooth, got.Timers.Local, got.Device)
	}
	if len(r.Reenter) != 0 || r.FromModel != "" {
		t.Errorf("reenter %v, from %q; want nothing", r.Reenter, r.FromModel)
	}
	for _, want := range []string{"alarms", "screen", "speaker"} {
		if !slices.Contains(r.Changed, want) {
			t.Errorf("changed %v, want %s in it", r.Changed, want)
		}
	}
	for _, not := range []string{"security", "bluetooth", "timers", "diag", "streaming"} {
		if slices.Contains(r.Changed, not) {
			t.Errorf("changed %v names %s, which a restore keeps", r.Changed, not)
		}
	}
}

// A file that points a setting at another server does not get this device's secret for it.
func TestASecretIsNotSentToTheFilesServer(t *testing.T) {
	here := Defaults()
	here.Brain.LLM, here.Brain.Key = "http://192.0.2.5:8080/v1", "mine"
	here.Home.Immich.Server, here.Home.Immich.Key = "http://192.0.2.20:2283", "immich-key"
	here.Dashboard.Server, here.Dashboard.Key = "http://192.0.2.30:8099", "dash-key"

	theirs := Defaults()
	theirs.Brain.LLM = "http://198.51.100.1/v1"
	theirs.Home.Immich.Server = here.Home.Immich.Server // same server: the key stays
	theirs.Dashboard.Server = "http://198.51.100.2"

	file, _ := Export(theirs, "m", "v0")
	r, err := Restored(here, file, "m")
	if err != nil {
		t.Fatal(err)
	}
	if r.Config.Brain.Key != "" || r.Config.Dashboard.Key != "" {
		t.Errorf("a key followed the file to its server: %q %q", r.Config.Brain.Key, r.Config.Dashboard.Key)
	}
	if r.Config.Home.Immich.Key != "immich-key" {
		t.Errorf("the key for the same server was not kept: %q", r.Config.Home.Immich.Key)
	}
	if len(r.Reenter) != 2 {
		t.Errorf("reenter %v, want the assistant's and the dashboard's", r.Reenter)
	}

	// A crafted file with a secret of its own does not get it in either.
	crafted := []byte(`{"format":"techo5-settings","version":1,"settings":{"brain":{"llm":"http://198.51.100.1/v1","key":"theirs"},"home":{"house_word":"theirs"}}}`)
	r, err = Restored(here, crafted, "m")
	if err != nil {
		t.Fatal(err)
	}
	if r.Config.Brain.Key == "theirs" || r.Config.Home.HouseWord == "theirs" {
		t.Errorf("a file put its own secrets in: %q %q", r.Config.Brain.Key, r.Config.Home.HouseWord)
	}
}

// Applied, a file goes onto the settings as they are then: what changed since it was looked at stays.
func TestARestoreKeepsWhatChangedSinceItWasLookedAt(t *testing.T) {
	Use(filepath.Join(t.TempDir(), "state.json"))
	src := Defaults()
	src.Screen.Clock24 = true
	file, _ := Export(src, "m", "v0")

	if _, err := Restored(Get(), file, "m"); err != nil {
		t.Fatal(err)
	}
	// Meanwhile, on the device:
	if err := Set().Security().LockPIN("4321"); err != nil {
		t.Fatal(err)
	}
	if _, err := Set().Restore(file, "m"); err != nil {
		t.Fatal(err)
	}
	if got := Get(); got.Security.LockPIN != "4321" || !got.Screen.Clock24 {
		t.Errorf("after the restore: pin %q clock24 %v", got.Security.LockPIN, got.Screen.Clock24)
	}
}

// A file from another model says so, and a Spot does not rescale a level that is already on its scale.
func TestAFileFromAnotherModel(t *testing.T) {
	file, _ := Export(Defaults(), "Echo Dot 2nd gen (biscuit)", "v0")
	r, err := Restored(Defaults(), file, "Echo Spot (rook)")
	if err != nil {
		t.Fatal(err)
	}
	if r.FromModel != "Echo Dot 2nd gen (biscuit)" {
		t.Errorf("from %q", r.FromModel)
	}
	if !r.Config.Screen.NightLightRescaled {
		t.Error("a file from another model would be rescaled again")
	}
}

func TestOnlySettingsFilesAreRead(t *testing.T) {
	for name, b := range map[string]string{
		"not JSON":       "hello",
		"another format": `{"format":"something","version":1,"settings":{}}`,
		"no version":     `{"format":"techo5-settings","settings":{}}`,
		"bad settings":   `{"format":"techo5-settings","version":1,"settings":{"speaker":"loud"}}`,
	} {
		if _, err := Restored(Defaults(), []byte(b), "m"); err == nil {
			t.Errorf("%s: read without complaint", name)
		}
	}
	newer, _ := json.Marshal(File{Format: FileFormat, Version: FileVersion + 1, Settings: json.RawMessage(`{}`)})
	if _, err := Restored(Defaults(), newer, "m"); err != errTooNew {
		t.Errorf("a newer file: %v", err)
	}
}

// What a file leaves out stays at the defaults, as a device's own file that predates a setting does.
func TestAnOlderFileLeavesNewSettingsAtTheirDefaults(t *testing.T) {
	old := []byte(`{"format":"techo5-settings","version":1,"settings":{"screen":{"clock_24":true}}}`)
	r, err := Restored(Defaults(), old, "m")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Config.Screen.Clock24 {
		t.Error("the file's one setting did not come across")
	}
	if r.Config.Speaker.Volume != Defaults().Speaker.Volume {
		t.Errorf("a setting the file left out is %d, want the default %d", r.Config.Speaker.Volume, Defaults().Speaker.Volume)
	}
}

// A restore does not set the microphones or the camera to take in the room.
func TestARestoreDoesNotStartListening(t *testing.T) {
	from := Defaults()
	from.Home.DropIn = true
	from.Wake.OnHA.Stream, from.Wake.OnHA.Confirm = true, true
	from.Wake.KeepClips = true
	from.Presence.On, from.Presence.Gestures = true, true
	file, _ := Export(from, "m", "v0")
	r, err := Restored(Defaults(), file, "m")
	if err != nil {
		t.Fatal(err)
	}
	c := r.Config
	if c.Home.DropIn || c.Wake.OnHA.Stream || c.Wake.OnHA.Confirm || c.Wake.KeepClips || c.Presence.On || c.Presence.Gestures {
		t.Errorf("a restore turned listening or watching on: drop-in %v stream %v confirm %v clips %v presence %v gestures %v",
			c.Home.DropIn, c.Wake.OnHA.Stream, c.Wake.OnHA.Confirm, c.Wake.KeepClips, c.Presence.On, c.Presence.Gestures)
	}
}

// Every server a restore changes is shown, keyless ones too.
func TestServerChangesAreShown(t *testing.T) {
	here := Defaults()
	here.Brain.LLM = "http://192.0.2.5:8080/v1"
	theirs := Defaults()
	theirs.Brain.LLM = "http://198.51.100.1/v1"
	theirs.Brain.STT = "198.51.100.1:10300"
	file, _ := Export(theirs, "m", "v0")
	r, err := Restored(here, file, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Servers) != 2 {
		t.Fatalf("servers %+v, want the chat model and speech to text", r.Servers)
	}
	for _, sc := range r.Servers {
		if sc.What == "The assistant's chat model" && (sc.Old != here.Brain.LLM || sc.New != theirs.Brain.LLM) {
			t.Errorf("chat model shown as %+v", sc)
		}
	}
}

// Calendar links and paired computers only the file's device has are named for entering again.
func TestWhatOnlyTheOtherDeviceHasIsNamed(t *testing.T) {
	theirs := Defaults()
	theirs.Calendar.Links = []CalendarLink{{ID: "c1", Name: "School", URL: "https://example.com/private"}}
	theirs.Deck.Computers = []DeckComputer{{Name: "Desk PC", Addr: "192.0.2.40", Key: "k"}}
	file, _ := Export(theirs, "m", "v0")
	r, err := Restored(Defaults(), file, "m")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"calendar link School", "paired computer Desk PC"} {
		if !slices.Contains(r.Reenter, want) {
			t.Errorf("reenter %v, want %q", r.Reenter, want)
		}
	}
}

func TestCredentialsComeOutOfAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"rtsp://admin:hunter2@192.0.2.9:554/s":  "rtsp://192.0.2.9:554/s",
		"https://tok3n@example.com/api":         "https://example.com/api",
		"rtsp://admin:pa/ss@w0rd@192.0.2.9/s":   "rtsp://192.0.2.9/s",
		"http://192.0.2.1/feed?api_key=abc&q=1": "http://192.0.2.1/feed?q=1",
		"http://192.0.2.1/feed?q=1&b=2&token=x": "http://192.0.2.1/feed?q=1&b=2",
		"http://192.0.2.1/feed":                 "http://192.0.2.1/feed",
		"no address here":                       "no address here",
		"see https://u:p@example.com/x for it":  "see https://example.com/x for it",
		// Every address in a value, not just the first.
		"rtsp://u:p@cam/s rtsp://u2:p2@cam2/s": "rtsp://cam/s rtsp://cam2/s",
		"foo://bar then https://u:p@host/x":    "foo://bar then https://host/x",
		"one\nrtsp://u:p@cam/s":                "one\nrtsp://cam/s",
		// An @ after the authority is a path or a search, not a login.
		"https://host/@user":                             "https://host/@user",
		"https://www.google.com/maps/@40.1,-96.7,15z":    "https://www.google.com/maps/@40.1,-96.7,15z",
		"http://example.com/?q=a@b.c":                    "http://example.com/?q=a@b.c",
		"https://host:8443/@user":                        "https://host:8443/@user",
		"http://192.0.2.9/cgi?cmd=snap&usr=admin&pwd=s3": "http://192.0.2.9/cgi?cmd=snap",
		"https://example.com/cb#access_token=abc&x=1":    "https://example.com/cb#x=1",
		"https://example.com/page#section":               "https://example.com/page#section",
		// A ?, # or / in a password, written as it is.
		"rtsp://u:p?ss@cam/s":                   "rtsp://cam/s",
		"rtsp://u:p#ss@cam/s":                   "rtsp://cam/s",
		"rtsp://admin:pa?ss@192.0.2.5:554/h264": "rtsp://192.0.2.5:554/h264",
		// An IPv6 host is not a login.
		"http://[::1]:8123/@user":     "http://[::1]:8123/@user",
		"http://host:/p?q=a@b":        "http://host:/p?q=a@b",
		"http://host:/p@x":            "http://host:/p@x",
		"http://[fe80::1]/maps/@40.1": "http://[fe80::1]/maps/@40.1",
		// A query with nothing to drop is left as written; one with something keeps the rest as written.
		"http://h/x?a=1;b=2&&c":      "http://h/x?a=1;b=2&&c",
		"http://h/x?":                "http://h/x?",
		"http://h/x?a=1;token=t;b=2": "http://h/x?a=1;b=2",
		// The names a key goes by.
		"http://h/x?accessToken=a&X-Api-Key=b&q=1":        "http://h/x?q=1",
		"http://h/x?api-key=a&clientSecret=b&authToken=c": "http://h/x",
		"http://%zz/x?token=abc":                          "http://%zz/x",
	} {
		if got := scrubAddress(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// Sendspin, the Bluetooth proxy and a choice of no wake word are the device's own.
func TestARestoreKeepsWhatIsOnByDefaultOff(t *testing.T) {
	here := Defaults()
	here.Sendspin.Enabled = false
	here.Bluetooth.Proxy = false
	here.Wake.NoneChosen, here.Wake.Words = true, nil
	from := Defaults()
	from.Sendspin.Enabled, from.Bluetooth.Proxy = true, true
	from.Wake.Words = []WakeWord{{ID: "okay_nabu"}}
	file, _ := Export(from, "m", "v0")
	r, err := Restored(here, file, "m")
	if err != nil {
		t.Fatal(err)
	}
	c := r.Config
	if c.Sendspin.Enabled || c.Bluetooth.Proxy || !c.Wake.NoneChosen || len(c.Wake.Words) != 0 {
		t.Errorf("sendspin %v proxy %v none chosen %v words %v", c.Sendspin.Enabled, c.Bluetooth.Proxy, c.Wake.NoneChosen, c.Wake.Words)
	}
}

// Secure Connections is this device's own both ways (#183): a file never carries it, a restore keeps
// this device's whichever way the file's device had it, and clearing what is a device's own leaves it on.
func TestSecureConnectionsAreTheDevicesOwn(t *testing.T) {
	from := Defaults()
	from.Bluetooth.SecureConnectionsOff = true
	file, err := Export(from, "m", "v0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), "secure_connections") {
		t.Errorf("the file carries secure connections: %s", file)
	}
	for _, off := range []bool{false, true} {
		here := Defaults()
		here.Bluetooth.SecureConnectionsOff = off
		r, err := Restored(here, file, "m")
		if err != nil {
			t.Fatal(err)
		}
		if r.Config.Bluetooth.SecureConnectionsOff != off || slices.Contains(r.Changed, "bluetooth") {
			t.Errorf("here off=%v: restored off=%v, changed %v", off, r.Config.Bluetooth.SecureConnectionsOff, r.Changed)
		}
	}
	cleared := from
	ClearOwn(&cleared)
	if cleared.Bluetooth.SecureConnectionsOff {
		t.Error("cleared of what is the device's own, secure connections stay off")
	}
}

// A long value with many addresses in it is cleaned in one pass, not one per address.
func TestManyAddressesAreQuick(t *testing.T) {
	s := strings.Repeat("a://", 1<<18)
	start := time.Now()
	scrubAddress(s)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("a megabyte of addresses took %v", d)
	}
}

// The release's new uses of the microphones and the camera, and the remote API, are the device's own,
// with their secrets; go2rtc's login stays only with its own server.
func TestARestoreKeepsTheNewSwitchesAndLogins(t *testing.T) {
	from := Defaults()
	from.Microphone.Noise, from.Microphone.Claps = true, true
	from.Screen.ClockHold = true
	from.Update.Channel, from.Update.AutoInstall = "dev", true
	from.CamStream = CamStream{On: true, User: "them", Pass: "their-password"}
	from.Remote = Remote{On: true, Secret: "theirs"}
	from.Home.Go2RTC.Base = "http://192.0.2.9:1984"

	here := Defaults()
	here.Nearest.Off = true // turned off here; the file's default (on) must not turn it back on
	here.CamStream = CamStream{User: "camera", Pass: "my-password"}
	here.Remote = Remote{Secret: "mine"}
	here.Home.Go2RTC.Base = "http://admin:secret@192.0.2.9:1984"

	file, err := Export(from, "", "v0")
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"their-password", "theirs"} {
		if strings.Contains(string(file), leak) {
			t.Errorf("the file carries %q", leak)
		}
	}
	got, err := Restored(here, file, "")
	if err != nil {
		t.Fatal(err)
	}
	c := got.Config
	if c.Microphone.Noise || c.Microphone.Claps || c.CamStream.On || c.Remote.On || c.Nearest.Enabled() || c.Screen.ClockHold {
		t.Errorf("a restore turned on what the device had off: %+v %+v %+v %+v", c.Microphone, c.CamStream, c.Remote, c.Nearest)
	}
	if c.Update.Channel != here.Update.Channel || c.Update.AutoInstall {
		t.Errorf("a restore changed how the device updates: %+v", c.Update)
	}
	if c.CamStream.Pass != "my-password" || c.Remote.Secret != "mine" {
		t.Errorf("the device's own logins were not kept: %+v %+v", c.CamStream, c.Remote)
	}
	if c.Home.Go2RTC.Base != "http://admin:secret@192.0.2.9:1984" {
		t.Errorf("go2rtc %q: the login for the same server was not kept", c.Home.Go2RTC.Base)
	}

	other := from
	other.Home.Go2RTC.Base = "http://192.0.2.77:1984"
	file, err = Export(other, "", "v0")
	if err != nil {
		t.Fatal(err)
	}
	got, err = Restored(here, file, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Home.Go2RTC.Base != "http://192.0.2.77:1984" || !slices.Contains(got.Reenter, "go2rtc's login") {
		t.Errorf("go2rtc %q, reenter %v: another server must not get this device's login", got.Config.Home.Go2RTC.Base, got.Reenter)
	}
}
