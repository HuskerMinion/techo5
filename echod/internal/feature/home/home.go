// Package home is the house on the screen: the weather on the clock, and a radio page that lists
// the stations Home Assistant knows and plays one through Home Assistant's own script — the same
// path the old dashboard's chips used, so favorites, search and the station finder stay where
// they are. Nothing here is baked in: Home Assistant tells the device which entities to follow and
// which script to call, through two actions (esphome.<device>_home_weather and _home_radio).
package home

import (
	"context"
	"image"
	"log/slog"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

func init() {
	component.Register(component.Device, Get(), component.Order(36))
}

// Weather is what the clock shows.
type Weather struct {
	Condition string // Home Assistant's state: "partlycloudy", "rain"…
	Temp      string // "75°" already formatted, empty when unknown
}

// Radio is what the radio page shows.
type Radio struct {
	Configured bool   // there is a list to show: favorites wired, or a token for Radio Browser
	Source     string // the list shown, config.RadioFavorites, RadioLocal or RadioPopular
	Sources    int    // how many lists there are to step through
	Loading    bool   // the list is being fetched
	Problem    string // why the list is empty, when fetching it failed
	Stations   []string
	Now        string // the station Home Assistant says is playing, empty for none
	Playing    bool   // the device's own player is running
	Chosen     string // the station tapped last, until Now catches up

	// What the station is playing, when its service says: the song, and a picture for the
	// background — the cover when there is one, else the station's logo (Logo true).
	Title, Artist, Album string
	Art                  *image.RGBA
	Thumb                *image.RGBA // the same picture as a square, fitted or filled the same way
	Logo                 bool
	Music                bool // a music station, for the default picture when there is none
}

type Feature struct {
	// Changed fires when anything shown changes; listeners must not block.
	Changed hook.Hook[struct{}]

	mu     sync.Mutex
	chosen string

	// listed is the Radio Browser station tapped last, at listedAt, to name the stream that follows.
	listed   string
	listedAt time.Time

	// weatherSel picks the weather entity; weathers is Home Assistant's list of them, fetched at
	// weathersAt.
	weatherSel *esphome.Select
	weathers   []hass.Entity
	weathersAt time.Time

	// haCameras is every camera Home Assistant has, fetched at haCamerasAt, for a device given no list
	// of its own; haCamerasBusy is a fetch under way.
	haCameras     []config.Camera
	haCamerasAt   time.Time
	haCamerasBusy bool

	// The radio's lists from Home Assistant's Radio Browser; see local.go.
	lists radioLists

	// radar is the rain map; see radar.go.
	radar   radarState
	url     string // the stream playing, from the media player
	urlName string // its station name once found

	// asked is the last station this device asked to play and askedAt when, which is what a dropped
	// stream is put back on with.
	asked   string
	askedAt time.Time

	// resumed counts how many times a dropped stream has been put back on lately, and resumedAt when
	// the last of those was, so a station that will not stay up is eventually left off (resume.go).
	resumed   int
	resumedAt time.Time
	forecast  []hass.Day
	fetched   time.Time
	poke      chan struct{}

	// meta is what the playing station is playing, for the now-playing screen; metaPoke asks for
	// a refresh when the station changes.
	meta     meta
	metaPoke chan struct{}

	// cam is the camera view in progress; see camera.go.
	cam CameraView

	// slideshowSel picks the display mode, slideshowOverlaySel the screensaver's clock/date size,
	// slideshowIdleNum the screensaver's idle wait; slideshow is the fetch state. See slideshow.go.
	slideshowSel        *esphome.Select
	slideshowOverlaySel *esphome.Select
	slideshowIdleNum    *esphome.Number

	// slideshowEveryNum is how long one photo stays up; slideshowFolderTxt names the folder the
	// photos come from, which is picked on the screen or with the home_slideshow action.
	slideshowEveryNum  *esphome.Number
	slideshowFolderTxt *esphome.TextSensor

	// slideshowShuffleSw and slideshowSubfoldersSw are how the photos are picked from the source.
	slideshowShuffleSw    *esphome.Switch
	slideshowSubfoldersSw *esphome.Switch
	slideshow             slideshowState
}

// forecastEvery is how often the forecast is refreshed while there is a weather entity.
const forecastEvery = 30 * time.Minute

// Run keeps the forecast current. Nothing to do without a token or a weather entity.
func (f *Feature) Run(ctx context.Context) error {
	go f.metaLoop(ctx)
	if hasScreen {
		go f.slideshowLoop(ctx)
	}
	for {
		f.refreshSources()
		f.refreshForecast()
		select {
		case <-ctx.Done():
			return nil
		case <-f.poke:
		case <-time.After(forecastEvery):
		}
	}
}

func (f *Feature) refreshForecast() {
	entity := config.Get().Home.WeatherEntity()
	if entity == "" || !hass.Get().Ready() {
		return
	}
	days, err := hass.Get().Forecast(entity)
	if err != nil {
		slog.Warn("home: forecast", "err", err)
		return
	}
	f.mu.Lock()
	f.forecast, f.fetched = days, time.Now()
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// Forecast is the daily forecast as last fetched, today first.
func (f *Feature) Forecast() []hass.Day {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]hass.Day(nil), f.forecast...)
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		shared = &Feature{poke: make(chan struct{}, 1), metaPoke: make(chan struct{}, 1)}
		shared.buildWeatherSelect()
		shared.buildSlideshowSelect()
		hastate.Get().Changed.Listen(func(hastate.Update) { shared.Changed.Emit(struct{}{}) })
		media.Get().OnPlay.Listen(shared.played)
		media.Get().OnEnd.Listen(shared.ended)
	})
	return shared
}

// played is a new stream starting: whatever was tapped is no longer the answer to "what is
// this", so its name is looked up from the URL.
func (f *Feature) played(url string) {
	f.mu.Lock()
	f.url, f.urlName, f.chosen = url, "", ""
	if f.listed != "" && time.Since(f.listedAt) < listedFor {
		f.urlName, f.listed = f.listed, ""
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
		f.pokeMeta()
		return
	}
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
	go func() {
		f.nameStream(url)
		f.pokeMeta()
	}()
}

// nameStream finds a station name for a stream URL in the lists Home Assistant keeps —
// favorites and the last search — and falls back to the stream's host.
func (f *Feature) nameStream(url string) {
	name := ""
	if hass.Get().Ready() {
		for _, entity := range []string{"sensor.radio_favorites", "sensor.radio_search_results"} {
			st, err := hass.Get().State(entity)
			if err != nil {
				continue
			}
			for _, key := range []string{"favorites", "results"} {
				items, _ := st.Attributes[key].([]any)
				for _, it := range items {
					m, _ := it.(map[string]any)
					if u, _ := m["url"].(string); u == url {
						if n, _ := m["name"].(string); n != "" {
							name = n
						}
					}
				}
			}
			if name != "" {
				break
			}
		}
	}
	// No host fallback: Home Assistant proxies streams through itself, so the host would be its
	// own address, which is not a station. Unknown stays unknown and the page falls back to the
	// "last station" text or what was tapped.
	f.mu.Lock()
	if f.url == url {
		f.urlName = name
	}
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

func (f *Feature) wake() {
	select {
	case f.poke <- struct{}{}:
	default:
	}
}

func (f *Feature) Name() string { return "home" }

// Restore registers what to follow from the saved configuration, before Home Assistant connects.
func (f *Feature) Restore(c config.Config) {
	f.weatherSel.Options = weatherOptions(c.Home)
	f.weatherSel.Set(chosenOption(c.Home))
	if hasScreen {
		f.slideshowSel.Set(slideshowLabelFor(c.Home.Slideshow.Mode))
		f.slideshowOverlaySel.Set(slideshowOverlayLabelFor(c.Home.Slideshow.Overlay))
		idle := c.Home.Slideshow.IdleMinutes
		if idle <= 0 {
			idle = int(slideshowIdleDefault / time.Minute)
		}
		f.slideshowIdleNum.Set(float32(idle))
		f.slideshowEveryNum.Set(float32(slideshowInterval(c.Home.Slideshow) / time.Second))
		f.slideshowFolderTxt.Set(slideshowFolderName(c.Home.Slideshow.Source))
		f.slideshowShuffleSw.Set(!c.Home.Slideshow.InOrder)
		f.slideshowSubfoldersSw.Set(!c.Home.Slideshow.TopOnly)
	}
	f.want(c.Home)
}

func (f *Feature) want(h config.Home) {
	var keys []hastate.Key
	if w := h.WeatherEntity(); w != "" {
		keys = append(keys, hastate.Key{Entity: w}, hastate.Key{Entity: w, Attribute: "temperature"},
			hastate.Key{Entity: w, Attribute: "temperature_unit"}, hastate.Key{Entity: w, Attribute: "friendly_name"})
	}
	// Home's location, for the rain map.
	keys = append(keys, hastate.Key{Entity: "zone.home", Attribute: "latitude"}, hastate.Key{Entity: "zone.home", Attribute: "longitude"})
	for _, s := range h.Radio.Stations {
		keys = append(keys, hastate.Key{Entity: s, Attribute: "options"})
	}
	if h.Radio.Now != "" {
		keys = append(keys, hastate.Key{Entity: h.Radio.Now})
	}
	hastate.Get().Follow("home", keys...)
}

// Actions are how Home Assistant configures this: which weather entity to show, and how the
// radio page is wired. Both persist and take effect at the next connection.
func (f *Feature) Actions() []*esphome.Action {
	actions := append(f.cameraActions(), f.accessAction())
	if hasScreen {
		actions = append(actions, f.slideshowAction())
	}
	return append(actions, []*esphome.Action{
		{
			Name: "home_weather",
			Args: []esphome.Arg{{Name: "entity", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				// An entity; "default" for Home Assistant's own forecast, "none" for no weather.
				switch entity := strings.TrimSpace(c.String("entity")); strings.ToLower(entity) {
				case "", "default":
					f.ChooseWeather(config.DefaultWeather)
				case config.WeatherOff:
					f.ChooseWeather("")
				default:
					f.ChooseWeather(entity)
				}
				return nil, nil
			},
		},
		{
			Name: "home_radio",
			Args: []esphome.Arg{
				{Name: "stations", Type: esphome.ArgString}, // input_select entities, comma separated
				{Name: "now", Type: esphome.ArgString},      // entity whose state names the playing station
				{Name: "service", Type: esphome.ArgString},  // script that plays a station
				{Name: "field", Type: esphome.ArgString},    // its station argument (default "station")
				{Name: "speaker_field", Type: esphome.ArgString},
				{Name: "speaker", Type: esphome.ArgString}, // this device's media_player entity
			},
			Run: func(c esphome.Call) (any, error) {
				r := config.Radio{
					Now:          strings.TrimSpace(c.String("now")),
					Service:      strings.TrimSpace(c.String("service")),
					Field:        strings.TrimSpace(c.String("field")),
					SpeakerField: strings.TrimSpace(c.String("speaker_field")),
					Speaker:      strings.TrimSpace(c.String("speaker")),
				}
				for _, s := range strings.Split(c.String("stations"), ",") {
					if s = strings.TrimSpace(s); s != "" {
						r.Stations = append(r.Stations, s)
					}
				}
				if r.Field == "" {
					r.Field = "station"
				}
				if r.SpeakerField == "" {
					r.SpeakerField = "speaker"
				}
				if err := config.Set().Home().Radio(r); err != nil {
					return nil, err
				}
				slog.Info("home: radio wired", "stations", r.Stations, "service", r.Service, "now", r.Now)
				f.rewire()
				return nil, nil
			},
		},
	}...)
}

// rewire re-registers what to follow and asks for a reconnect, since Home Assistant only asks
// what the device wants once per connection.
func (f *Feature) rewire() {
	f.want(config.Get().Home)
	component.Reconnect.Emit(struct{}{})
	f.wake()
	f.Changed.Emit(struct{}{})
}

// accessAction is the third action: the URL and a long-lived token for Home Assistant's REST
// API, for the forecast and, later, pictures and cameras.
func (f *Feature) accessAction() *esphome.Action {
	return &esphome.Action{
		Name: "home_assistant",
		Args: []esphome.Arg{{Name: "url", Type: esphome.ArgString}, {Name: "token", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			if err := hass.Get().Set(c.String("url"), c.String("token")); err != nil {
				return nil, err
			}
			slog.Info("home: home assistant access stored")
			f.wake()
			return nil, nil
		},
	}
}

// Weather is the current reading for the clock.
func (f *Feature) Weather() Weather {
	entity := config.Get().Home.WeatherEntity()
	if entity == "" {
		return Weather{}
	}
	t := hastate.Get()
	w := Weather{Condition: t.State(entity)}
	if w.Condition == "unknown" || w.Condition == "unavailable" {
		w.Condition = ""
	}
	if temp, ok := t.Value(entity, "temperature"); ok && temp != "" && temp != "None" {
		if i := strings.IndexByte(temp, '.'); i > 0 {
			temp = temp[:i]
		}
		w.Temp = temp + "°"
	}
	return w
}

// Radio is the page's content.
func (f *Feature) Radio() Radio {
	h := config.Get().Home.Radio
	sources := RadioSources()
	r := Radio{Configured: len(sources) > 0, Source: radioSource(), Sources: len(sources)}
	if !r.Configured {
		// No list to show, but the page is shown for a carried stream all the same, and it has to name
		// what is playing: the same last word the configured path ends with.
		return carried(r)
	}
	t := hastate.Get()
	if r.Source == config.RadioFavorites {
		r.Stations = favoriteNames(h)
	}
	// Favorites that turn out to hold no stations (lists not in Home Assistant yet) fall back to
	// the stations near home, when there are any to fall back to.
	if r.Source == config.RadioFavorites && len(r.Stations) == 0 && r.Sources > 1 {
		r.Source = config.RadioLocal
	}
	if r.Source == config.RadioOwn {
		for _, st := range OwnStations() {
			r.Stations = append(r.Stations, st.Name)
		}
	}
	if r.Source != config.RadioFavorites && r.Source != config.RadioOwn {
		f.fetchList(r.Source)
		f.mu.Lock()
		l := f.list(r.Source)
		for _, st := range l.stations {
			r.Stations = append(r.Stations, st.Name)
		}
		r.Loading, r.Problem = l.busy && len(l.stations) == 0, l.err
		f.mu.Unlock()
	}
	r.Playing, _ = media.Get().Playing()
	f.mu.Lock()
	r.Chosen = f.chosen
	r.Now = f.urlName
	r.Title, r.Artist, r.Album = f.meta.now.Title, f.meta.now.Artist, f.meta.now.Album
	r.Art, r.Thumb, r.Logo, r.Music = f.meta.art, f.meta.thumb, f.meta.artLogo, f.meta.st.Music
	f.mu.Unlock()
	// The stream's own name wins; Home Assistant's "last station" text is the fallback.
	if r.Now == "" && h.Now != "" {
		r.Now = t.State(h.Now)
		if r.Now == "unknown" || r.Now == "unavailable" {
			r.Now = ""
		}
	}

	return carried(r)
}

// carried puts what a remote is playing on the page, over whatever this device chose. It is the last
// word wherever the page is built: everything before it says what this device picked, and that is what
// the room is playing only when nothing else is.
//
// Receiving() is not the name of it. That names a phone over Bluetooth, which is a stream of this
// player's own rather than a remote's; the only thing that takes the speaker this way is Sendspin, and
// what runs the server it talks to here is Music Assistant, so that is what the page calls it.
func carried(r Radio) Radio {
	if !media.Get().ExternalPlaying() {
		return r
	}
	trackTitle, trackArtist, trackAlbum := media.Get().Track()
	r.Playing, r.Now = true, "Music Assistant"
	r.Title, r.Artist, r.Album = trackTitle, trackArtist, trackAlbum
	// The page draws a stand-in of its own for a track with no picture, so the last station's cover
	// and logo must not be left behind somebody else's.
	r.Art, r.Thumb, r.Logo = nil, nil, false
	r.Music = true
	return r
}

// favoriteNames is the stations in Home Assistant's lists, in order, each once.
func favoriteNames(h config.Radio) []string {
	t := hastate.Get()
	var out []string
	seen := map[string]bool{}
	for _, entity := range h.Stations {
		v, ok := t.Value(entity, "options")
		if !ok {
			continue
		}
		for _, name := range hastate.Options(v) {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] || strings.HasPrefix(strings.ToLower(name), "select a") {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Play plays a station of the list shown on this device: a favorite through Home Assistant's
// script, a Radio Browser station through the device's own player entity.
func (f *Feature) Play(station string) {
	// What was asked for, in the words the lists use, so a stream that drops can be asked for again:
	// the name worked out from a URL afterwards is often the stream's host, which no list has.
	f.mu.Lock()
	f.asked, f.askedAt = station, time.Now()
	f.mu.Unlock()

	source := radioSource()
	// Favorites with nothing in them show the stations near home instead (Radio).
	if source == config.RadioFavorites && len(favoriteNames(config.Get().Home.Radio)) == 0 {
		source = config.RadioLocal
	}
	if source == config.RadioOwn {
		if f.playOwn(station) {
			f.Changed.Emit(struct{}{})
			f.pokeMeta()
		}
		return
	}
	if source != config.RadioFavorites {
		if f.playListed(source, station) {
			f.Changed.Emit(struct{}{})
			f.pokeMeta()
		}
		return
	}
	h := config.Get().Home.Radio
	if !h.Configured() {
		return
	}
	f.mu.Lock()
	f.chosen = station
	f.mu.Unlock()
	callFavorite(h, station)
	f.Changed.Emit(struct{}{})
	f.pokeMeta()
}

// askFor is the station name to hand the finder: a list label like "101.1 WXYZ on iHeartRadio"
// was written for Alexa, which took the whole phrase; a search wants just "101.1 WXYZ".
func askFor(label string) string {
	l := strings.TrimSpace(label)
	for _, tail := range []string{" on iHeartRadio", " on iHeart", " on TuneIn", " on Tune In"} {
		if strings.HasSuffix(strings.ToLower(l), strings.ToLower(tail)) {
			return strings.TrimSpace(l[:len(l)-len(tail)])
		}
	}
	return l
}

// Stop ends whatever the player is doing.
func (f *Feature) Stop() {
	media.Get().Pause()
	f.mu.Lock()
	f.chosen = ""
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
	f.pokeMeta()
}
