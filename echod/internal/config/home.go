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

	Radio Radio `json:"radio"`

	// RadioSource is the list the radio page shows: RadioFavorites (the stations wired with
	// home_radio), RadioLocal or RadioPopular (Home Assistant's Radio Browser). Empty picks
	// favorites when they are wired, local stations otherwise.
	RadioSource string `json:"radio_source,omitempty"`

	// Cameras are camera.* entities and the names to say for them, in the order the list shows.
	Cameras []Camera `json:"cameras,omitempty"`

	// Slideshow is the idle photo slideshow's source and display mode.
	Slideshow Slideshow `json:"slideshow"`

	// HouseWord is what the devices in one house share so that they will take announcements from
	// each other and from nothing else. Empty means this device takes none: a device that makes a
	// noise in a bedroom should do nothing until somebody has said it may.
	HouseWord string `json:"house_word,omitempty"`

	// DropIn lets an intercom call from another device in the house connect by itself after a chime,
	// with nobody answering. Off unless somebody turns it on: it is a way to listen in on a room.
	DropIn bool `json:"drop_in,omitempty"`

	// DoNotDisturb turns intercom calls away: the caller is told, and nothing rings here.
	DoNotDisturb bool `json:"do_not_disturb,omitempty"`

	// RadarSource is where the rain map's radar comes from: RadarNWS (the U.S. National Weather
	// Service's national composite, lower 48 only), RadarRainViewer (worldwide), or empty for
	// automatic, which is the NWS when home is in the lower 48 and RainViewer anywhere else.
	RadarSource string `json:"radar_source,omitempty"`

	// AlertsOff turns the National Weather Service's alerts off (they are on for a device with a
	// screen in the U.S.): no badge, no pills, no fetching.
	AlertsOff bool `json:"alerts_off,omitempty"`
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
}

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

func (w HomeWriter) Weather(entity string) error {
	return w.st.Update(func(c *Config) { c.Home.Weather = entity })
}

func (w HomeWriter) Radio(r Radio) error {
	return w.st.Update(func(c *Config) { c.Home.Radio = r })
}

func (w HomeWriter) HouseWord(v string) error {
	return w.st.Update(func(c *Config) { c.Home.HouseWord = v })
}

func (w HomeWriter) DropIn(v bool) error {
	return w.st.Update(func(c *Config) { c.Home.DropIn = v })
}

func (w HomeWriter) DoNotDisturb(v bool) error {
	return w.st.Update(func(c *Config) { c.Home.DoNotDisturb = v })
}

func (w HomeWriter) WeatherSources(ids []string) error {
	return w.st.Update(func(c *Config) { c.Home.WeatherSources = ids })
}

func (w HomeWriter) AlertsOff(v bool) error {
	return w.st.Update(func(c *Config) { c.Home.AlertsOff = v })
}

func (w HomeWriter) RadarSource(source string) error {
	return w.st.Update(func(c *Config) { c.Home.RadarSource = source })
}

func (w HomeWriter) RadioSource(source string) error {
	return w.st.Update(func(c *Config) { c.Home.RadioSource = source })
}

func (w HomeWriter) Cameras(cams []Camera) error {
	return w.st.Update(func(c *Config) { c.Home.Cameras = cams })
}

func (w HomeWriter) Slideshow(s Slideshow) error {
	return w.st.Update(func(c *Config) { c.Home.Slideshow = s })
}
