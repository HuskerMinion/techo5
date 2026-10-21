package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Settings as a file: saved from one device, given back to it after a reinstall, or given to another
// device to start it off the same way.
//
// Two things never travel in the file, and both lists are functions of their own so anything else that
// has to know (a reset, say) uses the same ones:
//
//   - Secrets (ClearSecrets, KeepSecrets): keys, tokens, passwords, the settings lock's PIN, the house
//     word, calendars' private addresses, cameras' stream addresses, and any address with a user and
//     password in it. Restored, a device keeps its own, but only for the server it was for: a file that
//     points a setting at another server leaves its secret to be entered again, so a file cannot hand
//     this device's key to somebody else's server.
//   - What belongs to this device alone (KeepOwn): its privacy switches and what opens its doors on the
//     network (SSH, the camera and screen pages, remote debugging, certificate checks, AirPlay, Spotify
//     Connect, DLNA, video and the addresses allowed to send it), and what has its microphones or camera
//     take in the room (drop-in calls, sending the room to Home Assistant's wake word engine, keeping
//     wake word clips, presence and gestures), which are changed on purpose, one device at a time; the speaker it is paired with; the amplifier's gain, which is the hardware's; and what it
//     is in the middle of (snoozes, timers, a held wake light, calendar pop-ups shown, the calendars hidden
//     on its calendar page, the lock's count of wrong tries, the version it last ran).
//
// Everything else is the settings, and a restore replaces them. Settings this build has no field for,
// kept from the file on disk for a newer build (unknown.go), are left as they were.
//
// The file is still personal: it holds where the device is (the weather's place), addresses on the home
// network and user names. It is for keeping, not for posting.

// FileFormat says what a settings file is, so a file of anything else is refused rather than read.
const FileFormat = "techo5-settings"

// FileVersion is the file's own layout, apart from the settings' (which old files leave out and new
// ones add to, as the device's own file does). A file of a later layout is refused.
const FileVersion = 1

// File is a settings file.
type File struct {
	Format   string          `json:"format"`
	Version  int             `json:"version"`
	Model    string          `json:"model,omitempty"`    // the device it came from
	Software string          `json:"software,omitempty"` // and what it ran
	Settings json.RawMessage `json:"settings"`
}

// Export is c as a settings file, without its secrets or what is this device's own. model is the device's
// model, which a restore on another model needs to know.
func Export(c Config, model, software string) ([]byte, error) {
	out := deepCopy(c)
	ClearSecrets(&out)
	ClearOwn(&out)
	settings, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(File{Format: FileFormat, Version: FileVersion, Model: model, Software: software, Settings: settings}, "", "  ")
}

var (
	errNotSettings = errors.New("that is not a TECHO5 settings file")
	errTooNew      = errors.New("that settings file is from a newer version of TECHO5; update this device first")
)

// Restoration is what a settings file makes of a device's settings.
type Restoration struct {
	Config Config

	// Changed is which parts of the settings it changes, by their names in the file.
	Changed []string

	// Reenter is what will need entering again: secrets left empty because the file points their setting
	// somewhere else, and calendar links and paired computers the file names that this device does not
	// have (their addresses and keys never travel).
	Reenter []string

	// Servers is every server address the file changes, which is where voice, words or photos would go.
	Servers []ServerChange

	// FromModel is the model the file came from, when that is not this one; empty otherwise.
	FromModel string
}

// ServerChange is one server address a restore changes.
type ServerChange struct{ What, Old, New string }

// servers is every setting that says where something of the device's goes, to show the ones a restore
// changes.
func servers(c Config) []ServerChange {
	return []ServerChange{
		{What: "The assistant (direct or Home Assistant)", New: string(c.Brain.Mode)},
		{What: "The assistant's speech to text", New: c.Brain.STT},
		{What: "The assistant's text to speech", New: c.Brain.TTS},
		{What: "The assistant's chat model", New: c.Brain.LLM},
		{What: "The assistant's web search", New: c.Brain.Search},
		{What: "The assistant's page reader", New: c.Brain.Reader},
		{What: "The dashboard server", New: c.Dashboard.Server},
		{What: "Music Assistant", New: c.MusicAssistant.URL},
		{What: "Immich", New: c.Home.Immich.Server},
		{What: "The doorbell recorder", New: c.Home.Reolink.Base},
		{What: "OBS", New: c.Deck.OBS.Addr},
		{What: "go2rtc", New: withoutLogin(c.Home.Go2RTC.Base)},
		{What: "Realtime voice (the provider and its relay)", New: realtimeServer(c.Realtime)},
	}
}

// realtimeServer is where realtime voice goes: its provider, and the relay in its place if there is one.
func realtimeServer(r Realtime) string {
	if r.Provider == "" {
		return ""
	}
	if r.Endpoint != "" {
		return r.Provider + " via " + withoutLogin(r.Endpoint)
	}
	return r.Provider
}

// withoutLogin is an address with any user and password taken out of it, for comparing and showing.
func withoutLogin(addr string) string {
	u, err := url.Parse(addr)
	if err != nil || u.User == nil {
		return addr
	}
	u.User = nil
	return u.String()
}

// serverChanges is the server addresses that differ between a and b.
func serverChanges(a, b Config) []ServerChange {
	var out []ServerChange
	was, now := servers(a), servers(b)
	for i := range now {
		if was[i].New != now[i].New {
			out = append(out, ServerChange{What: now[i].What, Old: was[i].New, New: now[i].New})
		}
	}
	return out
}

// ServerChanges is the server addresses that differ between a and b, for anything else that changes
// settings and must not move where the device's voice, words or photos go (feature/share).
func ServerChanges(a, b Config) []ServerChange { return serverChanges(a, b) }

// Restored is what a settings file makes of cur, on a device of model: the file's settings, with cur's
// secrets (for the same servers) and what is cur's own kept.
func Restored(cur Config, file []byte, model string) (Restoration, error) {
	var f File
	if err := json.Unmarshal(file, &f); err != nil || f.Format != FileFormat || f.Version < 1 {
		return Restoration{}, errNotSettings
	}
	if f.Version > FileVersion {
		return Restoration{}, errTooNew
	}
	c := Defaults()
	if err := json.Unmarshal(f.Settings, &c); err != nil {
		return Restoration{}, fmt.Errorf("the settings in that file could not be read: %w", err)
	}
	other := f.Model != "" && f.Model != model
	if other {
		// A level saved by a device whose night light was never on the old scale is already on the new
		// one: the Spot's rescale must not run on it a second time.
		c.Screen.NightLightRescaled = true
	}
	c.moveSounds()
	c.rescaleNightLight()

	// A file from before realtime voice says nothing about it: the device's own is kept, the slots chosen
	// for it with it, rather than taken for one that turns it off.
	var sections map[string]json.RawMessage
	if json.Unmarshal(f.Settings, &sections) == nil {
		if _, ok := sections["realtime"]; !ok {
			c.Realtime = cur.Realtime
			for i := range c.Wake.Words {
				c.Wake.Words[i].Realtime = i < len(cur.Wake.Words) && cur.Wake.Words[i].Realtime
			}
		}
	}

	// What the file names that only the device that made it can fill in: its calendar links and paired
	// computers have their addresses and keys taken out, and the device's own are kept in their place.
	var missing []string
	for _, l := range c.Calendar.Links {
		if !slices.ContainsFunc(cur.Calendar.Links, func(m CalendarLink) bool { return m.ID == l.ID }) {
			missing = append(missing, "calendar link "+l.Name)
		}
	}
	for _, p := range c.Deck.Computers {
		if !slices.ContainsFunc(cur.Deck.Computers, func(m DeckComputer) bool { return m.Name == p.Name }) {
			missing = append(missing, "paired computer "+p.Name)
		}
	}

	r := Restoration{Reenter: append(KeepSecrets(&c, cur), missing...)}
	KeepOwn(&c, cur)
	// A slot sends its voice only where it was chosen to: a file that moves realtime voice to another
	// provider or relay leaves every slot off it, for somebody to choose again here.
	if c.Realtime.Provider != cur.Realtime.Provider || c.Realtime.Endpoint != cur.Realtime.Endpoint {
		for i := range c.Wake.Words {
			c.Wake.Words[i].Realtime = false
		}
	}
	c.Device = cur.Device
	r.Config, r.Changed, r.Servers = c, Changed(cur, c), serverChanges(cur, c)
	if other {
		r.FromModel = f.Model
	}
	return r, nil
}

// Restore applies a settings file to the settings as they are when it is applied, not as they were when
// it was looked at: a secret entered, a switch turned off or a timer set in between stays. Components
// read the settings as they start, so the daemon is restarted after it.
func (w Writer) Restore(file []byte, model string) (Restoration, error) {
	var r Restoration
	var err error
	if werr := w.st.Update(func(old *Config) {
		if r, err = Restored(deepCopy(*old), file, model); err == nil {
			*old = r.Config
		}
	}); werr != nil {
		return r, werr
	}
	return r, err
}

// Changed is which top-level parts of the settings differ between a and b, by their names in the file.
func Changed(a, b Config) []string {
	var out []string
	ta := reflect.TypeOf(a)
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	for i := range ta.NumField() {
		name := jsonName(ta.Field(i))
		if name == "" {
			continue
		}
		ja, _ := json.Marshal(va.Field(i).Interface())
		jb, _ := json.Marshal(vb.Field(i).Interface())
		if string(ja) != string(jb) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return ""
	}
	for i, ch := range tag {
		if ch == ',' {
			return tag[:i]
		}
	}
	if tag == "" {
		return f.Name
	}
	return tag
}

// ClearSecrets empties every secret in c.
func ClearSecrets(c *Config) {
	c.Brain.Key = ""
	c.Dashboard.Key = ""
	c.MusicAssistant.Token = ""
	c.Home.HouseWord = ""
	c.Home.Immich.Key = ""
	c.Home.Reolink.Pass = ""
	c.TalkBack = TalkBack{}
	c.Security.LockPIN = ""
	c.Deck.OBS.Password = ""
	c.Remote.Secret = ""
	c.CamStream.Pass = ""
	c.Realtime.Key = ""
	c.Realtime.MCPToken = ""
	for i := range c.Deck.Computers {
		c.Deck.Computers[i].Key = ""
	}
	for i := range c.Calendar.Links {
		c.Calendar.Links[i].URL = ""
	}
	scrubUserinfo(reflect.ValueOf(c).Elem())
}

// KeepSecrets puts from's secrets into c, in place of whatever c has, and answers the ones it could not
// keep. A secret is kept only for the server it was for: where c points the setting somewhere else, the
// secret is left empty for somebody to enter, rather than sent to a server the file chose.
func KeepSecrets(c *Config, from Config) (reenter []string) {
	keep := func(secret *string, mine string, same bool, what string) {
		switch {
		case same:
			*secret = mine
		case mine != "":
			*secret = ""
			reenter = append(reenter, what)
		default:
			*secret = ""
		}
	}
	keep(&c.Brain.Key, from.Brain.Key, c.Brain.LLM == from.Brain.LLM, "the assistant's key")
	keep(&c.Dashboard.Key, from.Dashboard.Key, c.Dashboard.Server == from.Dashboard.Server, "the dashboard server's key")
	keep(&c.MusicAssistant.Token, from.MusicAssistant.Token, c.MusicAssistant.URL == from.MusicAssistant.URL, "Music Assistant's token")
	keep(&c.Home.Immich.Key, from.Home.Immich.Key, c.Home.Immich.Server == from.Home.Immich.Server, "Immich's key")
	keep(&c.Home.Reolink.Pass, from.Home.Reolink.Pass,
		c.Home.Reolink.Base == from.Home.Reolink.Base && c.Home.Reolink.User == from.Home.Reolink.User &&
			c.Home.Reolink.Fingerprint == from.Home.Reolink.Fingerprint, "the doorbell recorder's password")
	keep(&c.Deck.OBS.Password, from.Deck.OBS.Password, c.Deck.OBS.Addr == from.Deck.OBS.Addr, "OBS's password")
	keep(&c.Realtime.Key, from.Realtime.Key,
		c.Realtime.Provider == from.Realtime.Provider && c.Realtime.Endpoint == from.Realtime.Endpoint, "realtime voice's key")
	// The token is for this device's Home Assistant, which a settings file does not name: kept whole.
	c.Realtime.MCPToken = from.Realtime.MCPToken
	// go2rtc's login is in its address: the device's is kept only for the same server (scheme, host and
	// port), and a file naming another server gets that server with no login.
	if mine := from.Home.Go2RTC.Base; mine != "" && withoutLogin(mine) == withoutLogin(c.Home.Go2RTC.Base) {
		c.Home.Go2RTC.Base = mine
	} else if mine != withoutLogin(mine) {
		reenter = append(reenter, "go2rtc's login")
	}

	// These have no server of their own to follow: the device's own, kept whole.
	c.Home.HouseWord = from.Home.HouseWord
	c.Security.LockPIN = from.Security.LockPIN
	c.Remote.Secret = from.Remote.Secret
	c.CamStream.Pass = from.CamStream.Pass
	c.TalkBack = from.TalkBack
	c.TalkBack.Cameras = maps.Clone(from.TalkBack.Cameras)
	c.TalkBack.Pictures = maps.Clone(from.TalkBack.Pictures)
	// A paired computer's key is the pairing itself, and a calendar link's address is the calendar: the
	// ones this device has stay what they are, whatever the file lists.
	c.Deck.Computers = slices.Clone(from.Deck.Computers)
	c.Calendar.Links = slices.Clone(from.Calendar.Links)
	return reenter
}

// ClearOwn empties what is this device's alone.
func ClearOwn(c *Config) { KeepOwn(c, Config{}) }

// KeepOwn puts from's own things into c, in place of whatever c has.
func KeepOwn(c *Config, from Config) {
	lockPIN := c.Security.LockPIN // a secret, kept or cleared by its own list
	c.Security = from.Security
	c.Security.LockPIN = lockPIN

	c.Diag = from.Diag

	// What has the microphones or the camera take in the room: listening in, sending it to Home
	// Assistant, keeping clips of it, watching for people.
	c.Home.DropIn = from.Home.DropIn
	// What may listen during a call or end one by voice: the wake word let through, its answer sent up
	// the call, the far end played under it, and the hang-up word.
	c.Home.CallWake, c.Home.CallReplies, c.Home.CallUnderlay = from.Home.CallWake, from.Home.CallReplies, from.Home.CallUnderlay
	c.Wake.Hangup = from.Wake.Hangup
	c.Wake.OnHA.Stream, c.Wake.OnHA.Confirm = from.Wake.OnHA.Stream, from.Wake.OnHA.Confirm
	c.Wake.KeepClips = from.Wake.KeepClips
	c.Presence.On, c.Presence.Gestures = from.Presence.On, from.Presence.Gestures
	c.Presence.Person = from.Presence.Person
	c.Streaming = from.Streaming
	c.Video.On, c.Video.DLNA = from.Video.On, from.Video.DLNA
	c.Video.Allowed = slices.Clone(from.Video.Allowed)
	c.Home.CameraLive = from.Home.CameraLive
	// Keeping the audio of a slot's turns on disk is this device's: the file's words come in, kept
	// to as many turns as the device kept for that slot before, none for a slot it did not have.
	for i := range c.Wake.Words {
		c.Wake.Words[i].Recordings = 0
		if i < len(from.Wake.Words) {
			c.Wake.Words[i].Recordings = from.Wake.Words[i].Recordings
		}
		// Sending a slot's voice to a company is chosen on the device, never by a file.
		c.Wake.Words[i].Realtime = i < len(from.Wake.Words) && from.Wake.Words[i].Realtime
	}
	// What realtime voice has used today is the device's count.
	c.Realtime.UsedDay, c.Realtime.UsedSeconds = from.Realtime.UsedDay, from.Realtime.UsedSeconds

	// Sendspin keeps a port open and says so on the network; the Bluetooth proxy hears nearby phones for
	// Home Assistant. Neither is a restore's to turn on: Sendspin is on by default, so a file from before
	// it was turned off would turn it back on.
	c.Sendspin = from.Sendspin
	c.Bluetooth = from.Bluetooth

	// Measuring the room, listening for claps and serving the camera: new uses of the microphones and
	// the camera, each this device's to turn on. The camera's login goes with it (its password is also
	// a secret, kept by KeepSecrets). The remote API's switch too: a restore never opens a door.
	c.Microphone.Noise = from.Microphone.Noise
	c.Microphone.Claps = from.Microphone.Claps
	c.CamStream = from.CamStream
	c.Remote.On = from.Remote.On
	// Answering for the others when this one heard best keeps measuring the room and talks to them on the
	// network; a finger held on the clock goes to Home Assistant. Both this device's to turn on.
	c.Nearest = from.Nearest
	c.Screen.ClockHold = from.Screen.ClockHold
	// Sharing settings with the other devices, and which of them, is this device's to set up: a file
	// from another device names that device's links, not this one's.
	c.Share = from.Share.clone()
	// Being a screen for the Tessera add-on puts a device of its own in Home Assistant and lets the add-on
	// send it layouts, and a change to it restarts the daemon: this device's to turn on, never a file's.
	c.Tessera = from.Tessera

	// "No wake word" chosen on purpose is a device that is not to listen: a file with words in it does
	// not start it listening.
	if from.Wake.NoneChosen {
		c.Wake.NoneChosen = true
		c.Wake.Words = slices.Clone(from.Wake.Words)
	}
	c.Speaker.GainDB = from.Speaker.GainDB
	c.Microphone.Muted = from.Microphone.Muted

	c.Alarms.Snoozed = slices.Clone(from.Alarms.Snoozed)
	c.Alarms.Held = nil
	if from.Alarms.Held != nil {
		held := *from.Alarms.Held
		c.Alarms.Held = &held
	}
	c.Alarms.HeldDark = from.Alarms.HeldDark
	c.Timers.Local = slices.Clone(from.Timers.Local)
	c.Calendar.PopupShown = slices.Clone(from.Calendar.PopupShown)
	// The calendars hidden with the calendar page's chips are this screen's: a file from a screen in
	// another room does not hide any here, and a restore keeps what was hidden.
	c.Calendar.Hidden = slices.Clone(from.Calendar.Hidden)
	c.Update.LastVersion = from.Update.LastVersion
	// Which builds it takes, and whether it installs them by itself, are this device's: a file is not
	// to move it to another channel or have it install on its own.
	c.Update.Channel = from.Update.Channel
	c.Update.AutoInstall = from.Update.AutoInstall
}

// scheme finds an address in a setting.
var scheme = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://`)

// secretParams are query and fragment parameters that carry a credential in an address, lowercased and
// with _ and - taken out (paramName), so access_token, accessToken and access-token are one.
var secretParams = []string{
	"key", "apikey", "xapikey", "token", "accesstoken", "auth", "authtoken", "clientsecret",
	"password", "pass", "passwd", "pwd", "usr", "secret", "signature", "sig",
}

// paramName is a parameter's name as secretParams has it.
func paramName(name string) string {
	if n, err := url.QueryUnescape(name); err == nil {
		name = n
	}
	return strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(name))
}

// scrubUserinfo takes any credential out of every address in v, wherever it is: a camera, a stream or
// a server given as rtsp://user:pass@host or https://token@host keeps the host and loses the rest, and a
// key in its query is dropped.
func scrubUserinfo(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			scrubUserinfo(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				scrubUserinfo(v.Field(i))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			scrubUserinfo(v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(v.MapIndex(k))
			scrubUserinfo(e)
			v.SetMapIndex(k, e)
		}
	case reflect.String:
		if v.CanSet() {
			if s := scrubAddress(v.String()); s != v.String() {
				v.SetString(s)
			}
		}
	}
}

// scrubAddress is s with the credentials taken out of every address in it. Each address ends at the
// first whitespace or where the next one starts, and the value is built once, front to back, however
// many addresses it holds.
func scrubAddress(s string) string {
	found := scheme.FindAllStringIndex(s, -1)
	if found == nil {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prev := 0
	for i, f := range found {
		next := len(s)
		if i+1 < len(found) {
			next = found[i+1][0]
		}
		end := next
		if sp := strings.IndexFunc(s[f[1]:next], unicode.IsSpace); sp >= 0 {
			end = f[1] + sp
		}
		b.WriteString(s[prev:f[0]])
		b.WriteString(scrubOne(s[f[0]:end]))
		prev = end
	}
	b.WriteString(s[prev:])
	return b.String()
}

// scrubOne is one address with its user and password, and any credential in its query or fragment,
// taken out.
//
// A user and password are only in the authority, the part before the first /, ? or #: an @ further on is
// a path or a search (a map's /@40.1,-96.7 or ?q=a@b.c) and is left alone. The one exception is a
// password with a /, ? or # in it, written as it is: an authority that reads user:something (not
// host:port, and not an IPv6 address in brackets) with an @ later on is taken to be that, up to the last @.
func scrubOne(addr string) string {
	head := strings.Index(addr, "://") + 3
	rest := addr[head:]
	authEnd := len(rest)
	if k := strings.IndexAny(rest, "/?#"); k >= 0 {
		authEnd = k
	}
	authority := rest[:authEnd]
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		rest = rest[at+1:]
	} else if colon := strings.Index(authority, ":"); colon >= 0 && authority[colon+1:] != "" &&
		!strings.HasPrefix(authority, "[") && !allDigits(authority[colon+1:]) {
		if at := strings.LastIndex(rest, "@"); at >= authEnd {
			rest = rest[at+1:]
		}
	}

	// The query and the fragment, pair by pair as written, so what is kept keeps its order and form.
	frag, hasFrag := "", false
	if k := strings.Index(rest, "#"); k >= 0 {
		rest, frag, hasFrag = rest[:k], rest[k+1:], true
	}
	query, hasQuery := "", false
	if k := strings.Index(rest, "?"); k >= 0 {
		rest, query, hasQuery = rest[:k], rest[k+1:], true
	}
	out := addr[:head] + rest
	if hasQuery {
		if q, dropped := withoutSecretParams(query); !dropped || q != "" {
			out += "?" + q
		}
	}
	if hasFrag {
		f, dropped := frag, false
		if strings.Contains(frag, "=") {
			f, dropped = withoutSecretParams(frag)
		}
		if !dropped || f != "" {
			out += "#" + f
		}
	}
	return out
}

// withoutSecretParams is a query or fragment without the pairs that carry a credential, and whether any
// went. With none gone it is the text as it was; otherwise each pair kept keeps the separator it had.
func withoutSecretParams(q string) (string, bool) {
	var b strings.Builder
	dropped, start := false, 0
	for start <= len(q) {
		end := len(q)
		if k := strings.IndexAny(q[start:], "&;"); k >= 0 {
			end = start + k
		}
		pair := q[start:end]
		name, _, _ := strings.Cut(pair, "=")
		if pair != "" && slices.Contains(secretParams, paramName(name)) {
			dropped = true
		} else {
			if b.Len() > 0 && start > 0 {
				b.WriteByte(q[start-1])
			}
			b.WriteString(pair)
		}
		start = end + 1
	}
	if !dropped {
		return q, false
	}
	return strings.Trim(b.String(), "&;"), true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
