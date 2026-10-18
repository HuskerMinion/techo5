package config

import "slices"

// Home is what the device shows and reaches for in Home Assistant beyond its own entities: a
// weather entity for the clock screen, and the radio — the selects whose options are the
// stations, the text that names what is playing, and the script that plays one. All of it is
// set from Home Assistant through the device's actions, so nothing here is baked in.
type Home struct {
	// Weather is a weather.* entity shown on the idle screen. Empty is DefaultWeather, the forecast
	// every Home Assistant sets up on its own, or, when Home Assistant listed weather entities and
	// that is not among them, the first it listed; WeatherOff shows none.
	Weather string `json:"weather,omitempty"`

	// WeatherSources are the weather entities Home Assistant listed last, offered as choices.
	WeatherSources []string `json:"weather_sources,omitempty"`

	// FollowPlayer is another media_player Now Playing shows while this device plays nothing of its
	// own; empty follows none. PlayerSources are the media players Home Assistant listed last.
	FollowPlayer  string   `json:"follow_player,omitempty"`
	PlayerSources []string `json:"player_sources,omitempty"`

	// Lyrics shows the words of the song on Now Playing, looked up at LRCLIB by title and artist.
	Lyrics bool `json:"lyrics,omitempty"`

	// Location is a zone.* entity the rain map and weather alerts are centered on, for a device that
	// is somewhere other than home (a family device in another house). Empty is Home Assistant's home.
	Location string `json:"location,omitempty"`

	// Place is where the device is, kept on the device: for weather, alerts and the rain map when
	// there is no Home Assistant to say where home is (Home Assistant's own location wins when there
	// is). Set on the setup page from a ZIP code or a town.
	Place Place `json:"place"`

	// Units is how temperatures are shown when the device fetches its own weather: UnitsF, UnitsC, or
	// empty for Fahrenheit in the U.S. and Celsius elsewhere.
	Units string `json:"units,omitempty"`

	Radio Radio `json:"radio"`

	// RadioSource is the list the radio page shows: RadioFavorites (the stations wired with
	// home_radio), RadioLocal or RadioPopular (Home Assistant's Radio Browser). Empty picks
	// favorites when they are wired, local stations otherwise.
	RadioSource string `json:"radio_source,omitempty"`

	// Cameras are camera.* entities and the names to say for them, in the order the list shows.
	Cameras []Camera `json:"cameras,omitempty"`

	// Reolink is a Reolink NVR, Home Hub or camera read directly, for a device with no Home Assistant
	// to proxy its cameras (feature/home/reolink.go). Its cameras join the list.
	Reolink Reolink `json:"reolink"`

	// Go2RTC is a go2rtc server whose streams are cameras on the list (feature/home/go2rtc.go).
	Go2RTC Go2RTC `json:"go2rtc,omitempty"`

	// Glance are Home Assistant entities shown as chips along the foot of the clock page, each only
	// while it has something to say (feature/home/glance.go), in the order given.
	Glance []string `json:"glance,omitempty"`

	// Slideshow is the idle photo slideshow's source and display mode.
	Slideshow Slideshow `json:"slideshow"`

	// Immich is an Immich server the slideshow reads photos from directly, with no Home Assistant in
	// between: its address and an API key, which is a secret and never shown again once saved.
	Immich Immich `json:"immich,omitempty"`

	// HouseWord is what the devices in one house share so that they will take announcements from
	// each other and from nothing else. Empty means this device takes none: a device that makes a
	// noise in a bedroom should do nothing until somebody has said it may.
	HouseWord string `json:"house_word,omitempty"`

	// DropIn lets an intercom call from another device in the house connect by itself after a chime,
	// with nobody answering. Off unless somebody turns it on: it is a way to listen in on a room.
	DropIn bool `json:"drop_in,omitempty"`

	// RingSound is how a call rings here, one of the phone's ring sounds; empty is the first of them.
	RingSound string `json:"ring_sound,omitempty"`

	// DoNotDisturb turns intercom calls away: the caller is told, and nothing rings here.
	DoNotDisturb bool `json:"do_not_disturb,omitempty"`

	// CallWake lets the wake word open a turn while a call is up, which is how a call is hung up by
	// voice: Home Assistant hears "hang up" and calls phone_hangup. Off unless chosen.
	CallWake bool `json:"call_wake,omitempty"`

	// CallReplies sends the assistant's reply to a turn during a call into the call as well, so the
	// far end hears it. Off, only the room does. An announcement goes into the call either way.
	CallReplies bool `json:"call_replies,omitempty"`

	// CallUnderlay keeps the far end playing, quieter, under the chime and the reply of a turn during
	// a call. Off, they are not heard until the speaker is the call's again.
	CallUnderlay bool `json:"call_underlay,omitempty"`

	// RadarSource is where the rain map's radar comes from: RadarNWS (the U.S. National Weather
	// Service's national composite, lower 48 only), RadarRainViewer (worldwide), or empty for
	// automatic, which is the NWS when home is in the lower 48 and RainViewer anywhere else.
	RadarSource string `json:"radar_source,omitempty"`

	// AlertsOff turns the National Weather Service's alerts off (they are on for a device with a
	// screen in the U.S.): no badge, no pills, no fetching.
	AlertsOff bool `json:"alerts_off,omitempty"`

	// CameraSound plays a camera's own audio on this device while its view is up: the yard or the
	// street, in the room. Off unless somebody asks for it — a device that starts making the
	// outside's noise unasked is a device people turn off — and the home_show_camera_sound action
	// can ask for it, or refuse it, for one view whatever this says.
	CameraSound bool `json:"camera_sound,omitempty"`

	// CameraLive shows a camera Home Assistant can stream as live video, decoded on the device, with its
	// sound from the same stream (feature/home, camera_stream.go). Off: snapshots and camera.play_stream,
	// as before it existed.
	CameraLive bool `json:"camera_live,omitempty"`
}

// Slideshow is how the idle screen's photo slideshow is wired: a Home Assistant media source to
// step through, and how it shows on screen. Empty Mode is off.
type Slideshow struct {
	// Source is a media source id, like media-source://immich/album-id or a local media source's
	// folder — whatever Home Assistant's browse API accepts. Its photos, and those in the folders
	// under it unless TopOnly, are shown in a shuffled order unless InOrder.
	Source string `json:"source,omitempty"`

	// TopOnly leaves out the photos in the source's subfolders; InOrder shows them in the source's
	// own order instead of shuffled. Both off is the default: a library of year and event folders
	// picked at its top shows all of it, mixed.
	TopOnly bool `json:"top_only,omitempty"`
	InOrder bool `json:"in_order,omitempty"`

	// Mode is SlideshowBackground (behind the ordinary idle page, always on), SlideshowScreensaver
	// (full screen, after IdleMinutes idle), or empty for off.
	Mode string `json:"mode,omitempty"`

	// Overlay is the clock/date shown over a Screensaver photo: SlideshowOverlayOff,
	// SlideshowOverlaySmall, or empty for the normal, full-size clock. Unused in Background mode,
	// which always shows the ordinary idle page's own clock.
	Overlay string `json:"overlay,omitempty"`

	// IdleMinutes is how long Screensaver mode waits for, zero for the default
	// (SlideshowIdleDefault). Unused in Background mode.
	IdleMinutes int `json:"idle_minutes,omitempty"`

	// EverySeconds is how long one photo stays up before the next, zero for the default
	// (slideshowEvery, a minute).
	EverySeconds int `json:"every_seconds,omitempty"`

	// WholePhoto shows each photo whole, centered, with a blurred and darkened copy of it filling the
	// sides, rather than cropped to fill the screen (the default), which cuts off much of a tall photo.
	WholePhoto bool `json:"whole_photo,omitempty"`

	// Art shows weather art in place of the photos: a landscape the device draws for the weather and
	// the time of day (display/weather_art.go). Source is kept, for turning it off again.
	Art bool `json:"weather_art,omitempty"`

	// Pairs shows two tall photos side by side, each filling half the screen, rather than one with
	// blurred sides. The Show only: a round screen has no halves.
	Pairs bool `json:"pairs,omitempty"`

	// Caption shows when and where an Immich photo was taken, over it in Screensaver mode.
	Caption bool `json:"caption,omitempty"`

	// ImmichFavorites, ImmichFrom, ImmichTo and ImmichPerson narrow an Immich source: to its favorites;
	// to photos taken on or after ImmichFrom and on or before ImmichTo (dates, 2006-01-02, either may be
	// empty); to photos of the person Immich knows by the name ImmichPerson.
	ImmichFavorites bool   `json:"immich_favorites,omitempty"`
	ImmichFrom      string `json:"immich_from,omitempty"`
	ImmichTo        string `json:"immich_to,omitempty"`
	ImmichPerson    string `json:"immich_person,omitempty"`
}

// PhotoDay is the form ImmichFrom and ImmichTo are kept in.
const PhotoDay = "2006-01-02"

// Camera is one camera on the screen's list.
type Camera struct {
	Entity string `json:"entity"`
	Name   string `json:"name"`
}

// Radio is how the screen's radio page is wired to the house's own radio setup.
type Radio struct {
	// Stations are input_select entities whose options are station names, listed in order.
	Stations []string `json:"stations,omitempty"`

	// Now is an entity whose state names the station playing, shown while the player runs.
	Now string `json:"now,omitempty"`

	// Own are stations kept on the device: a name and the address of the stream, played by the
	// device itself. Nothing about them needs Home Assistant, which is the point of them.
	Own []Station `json:"own,omitempty"`

	// Service is the script that plays a station, called with Field = station name and
	// SpeakerField = Speaker (this device's media player entity in Home Assistant).
	Service      string `json:"service,omitempty"`
	Field        string `json:"field,omitempty"`
	SpeakerField string `json:"speaker_field,omitempty"`
	Speaker      string `json:"speaker,omitempty"`
}

// Station is one of the device's own radio stations.
type Station struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// MaxOwnStations is as many as the device keeps: a short list somebody typed, not a library.
const MaxOwnStations = 20

// DefaultWeather is Home Assistant's own forecast (Met.no), which a new installation sets up for its
// home location.
const DefaultWeather = "weather.forecast_home"

// WeatherOff is the choice of no weather at all.
const WeatherOff = "none"

// WeatherEntity is the weather entity to show, empty for none.
// HomeZone is the zone the rain map and weather alerts are centered on: Location, or zone.home.
func (h Home) HomeZone() string {
	if h.Location != "" {
		return h.Location
	}
	return HomeZoneDefault
}

// HomeZoneDefault is Home Assistant's own home.
const HomeZoneDefault = "zone.home"

func (h Home) WeatherEntity() string {
	switch h.Weather {
	case "":
		// An installation whose own forecast was removed or renamed still shows a forecast.
		if len(h.WeatherSources) > 0 && !slices.Contains(h.WeatherSources, DefaultWeather) {
			return h.WeatherSources[0]
		}
		return DefaultWeather
	case WeatherOff:
		return ""
	}
	return h.Weather
}

// The radio page's lists.
const (
	RadioFavorites = "favorites"
	RadioLocal     = "local"
	RadioPopular   = "popular"

	// RadioOwn are the stations kept on the device itself, played straight from their stream
	// address. They are the only ones a device without Home Assistant can play.
	RadioOwn = "own"
)

// Where the rain map's radar comes from; empty is automatic.
const (
	RadarNWS        = "nws"
	RadarRainViewer = "rainviewer"
)

// The slideshow's display modes.
const (
	SlideshowBackground  = "background"  // behind the ordinary idle page, always on
	SlideshowScreensaver = "screensaver" // full screen, after idle
)

// The screensaver's clock/date overlay. Empty is the normal, full-size clock.
const (
	SlideshowOverlayOff   = "off"
	SlideshowOverlaySmall = "small"
)

func defaultHome() Home {
	return Home{Radio: Radio{Field: "station", SpeakerField: "speaker"}}
}

// Configured reports whether the radio page has anything to work with.
func (r Radio) Configured() bool { return len(r.Stations) > 0 && r.Service != "" }

type HomeWriter struct{ st *Store }

func (w HomeWriter) Place(p Place) error {
	return w.st.Update(func(c *Config) { c.Home.Place = p })
}

func (w HomeWriter) Units(u string) error {
	return w.st.Update(func(c *Config) { c.Home.Units = u })
}

func (w HomeWriter) Location(zone string) error {
	return w.st.Update(func(c *Config) { c.Home.Location = zone })
}

func (w HomeWriter) Weather(entity string) error {
	return w.st.Update(func(c *Config) { c.Home.Weather = entity })
}

// Radio wires the radio page. The device's own stations are kept whatever r says: they are typed on
// the setup page and written only with OwnStations, so wiring the favorites (home_radio) cannot
// take them away.
func (w HomeWriter) Radio(r Radio) error {
	return w.st.Update(func(c *Config) {
		r.Own = c.Home.Radio.Own
		c.Home.Radio = r
	})
}

// OwnStations replaces the device's own stations and nothing else of the radio's wiring.
func (w HomeWriter) OwnStations(list []Station) error {
	return w.st.Update(func(c *Config) { c.Home.Radio.Own = list })
}

func (w HomeWriter) HouseWord(v string) error {
	return w.st.Update(func(c *Config) { c.Home.HouseWord = v })
}

func (w HomeWriter) DropIn(v bool) error {
	return w.st.Update(func(c *Config) { c.Home.DropIn = v })
}

func (w HomeWriter) RingSound(v string) error {
	return w.st.Update(func(c *Config) { c.Home.RingSound = v })
}

func (w HomeWriter) CallWake(v bool) error {
	return w.st.Update(func(c *Config) { c.Home.CallWake = v })
}

func (w HomeWriter) CallReplies(v bool) error {
	return w.st.Update(func(c *Config) { c.Home.CallReplies = v })
}

func (w HomeWriter) CallUnderlay(v bool) error {
	return w.st.Update(func(c *Config) { c.Home.CallUnderlay = v })
}

func (w HomeWriter) DoNotDisturb(v bool) error {
	return w.st.Update(func(c *Config) { c.Home.DoNotDisturb = v })
}

func (w HomeWriter) Lyrics(on bool) error {
	return w.st.Update(func(c *Config) { c.Home.Lyrics = on })
}

func (w HomeWriter) FollowPlayer(entity string) error {
	return w.st.Update(func(c *Config) { c.Home.FollowPlayer = entity })
}

func (w HomeWriter) PlayerSources(ids []string) error {
	return w.st.Update(func(c *Config) { c.Home.PlayerSources = ids })
}

func (w HomeWriter) WeatherSources(ids []string) error {
	return w.st.Update(func(c *Config) { c.Home.WeatherSources = ids })
}

func (w HomeWriter) AlertsOff(v bool) error {
	return w.st.Update(func(c *Config) { c.Home.AlertsOff = v })
}

func (w HomeWriter) CameraSound(v bool) error {
	return w.st.Update(func(c *Config) { c.Home.CameraSound = v })
}

func (w HomeWriter) CameraLive(v bool) error {
	return w.st.Update(func(c *Config) { c.Home.CameraLive = v })
}

func (w HomeWriter) RadarSource(source string) error {
	return w.st.Update(func(c *Config) { c.Home.RadarSource = source })
}

func (w HomeWriter) RadioSource(source string) error {
	return w.st.Update(func(c *Config) { c.Home.RadioSource = source })
}

func (w HomeWriter) Glance(entities []string) error {
	return w.st.Update(func(c *Config) { c.Home.Glance = entities })
}

func (w HomeWriter) Cameras(cams []Camera) error {
	return w.st.Update(func(c *Config) { c.Home.Cameras = cams })
}

func (w HomeWriter) Slideshow(s Slideshow) error {
	return w.st.Update(func(c *Config) { c.Home.Slideshow = s })
}

func (w HomeWriter) Immich(v Immich) error {
	return w.st.Update(func(c *Config) { c.Home.Immich = v })
}

// Immich is an Immich server (a photo library people run themselves) the slideshow can show photos
// from. Server is its address, http or https, with any path it is served under; Key is an API key made
// in Immich's account settings. Album is the name of the album last chosen as the slideshow's source,
// for naming it without asking the server.
type Immich struct {
	Server string `json:"server,omitempty"`
	Key    string `json:"key,omitempty"`
	Album  string `json:"album,omitempty"`
	// AlbumID is the album Album names: a name is only shown for the album it is the name of.
	AlbumID string `json:"album_id,omitempty"`
}

// Set is whether there is a server to read from.
func (i Immich) Set() bool { return i.Server != "" && i.Key != "" }

// Place is somewhere on the map, as a person names it.
type Place struct {
	Name    string  `json:"name,omitempty"` // "Anchorage, Alaska"
	Lat     float64 `json:"lat,omitempty"`
	Lon     float64 `json:"lon,omitempty"`
	Country string  `json:"country,omitempty"` // ISO code, "US"
}

// Set is whether there is a place.
func (p Place) Set() bool { return p.Lat != 0 || p.Lon != 0 }

const (
	UnitsF = "F"
	UnitsC = "C"
)

// Fahrenheit is whether temperatures are shown in Fahrenheit.
func (h Home) Fahrenheit() bool {
	switch h.Units {
	case UnitsF:
		return true
	case UnitsC:
		return false
	}
	switch h.Place.Country {
	case "", "US", "PR", "VI", "GU", "AS", "MP", "LR", "BS", "BZ", "KY", "PW", "FM", "MH":
		return true
	}
	return false
}

// Reolink is one recorder: where it is and how it answers (Base, with https or http), the account the
// device logs in with, the fingerprint of its certificate as it was when set up, and its cameras by
// name as it listed them. The password is a secret, never shown again once saved.
type Reolink struct {
	Base        string   `json:"base,omitempty"`
	User        string   `json:"user,omitempty"`
	Pass        string   `json:"pass,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	Cameras     []Camera `json:"cameras,omitempty"`
}

// Go2RTC is a go2rtc server: its address (with its login, if it has one) and its streams as cameras, as
// it listed them when it was set up.
type Go2RTC struct {
	Base    string   `json:"base,omitempty"`
	Cameras []Camera `json:"cameras,omitempty"`
}

func (w HomeWriter) Go2RTC(g Go2RTC) error {
	return w.st.Update(func(c *Config) { c.Home.Go2RTC = g })
}

func (w HomeWriter) Reolink(r Reolink) error {
	return w.st.Update(func(c *Config) { c.Home.Reolink = r })
}
