// Package home is the house on the screen: the weather on the clock, and a radio page that lists
// the stations Home Assistant knows and plays one through Home Assistant's own script — the same
// path the old dashboard's chips used, so favorites, search and the station finder stay where
// they are. Nothing here is baked in: Home Assistant tells the device which entities to follow and
// which script to call, through two actions (esphome.<device>_home_weather and _home_radio).
package home

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	neturl "net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/openmeteo"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
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
	Playing    bool   // something is playing: the device's own stream, or one it carries
	Paused     bool   // something is paused and still on the page: a held remote track counts
	Chosen     string // the station tapped last, until Now catches up
	Grouped    bool   // this room is playing along with a group, so a stop here stops the group

	// What the station is playing, when its service says: the song, and a picture for the
	// background — the cover when there is one, else the station's logo (Logo true).
	Title, Artist, Album string
	Art                  *image.RGBA
	Thumb                *image.RGBA // the same picture as a square, fitted or filled the same way
	Logo                 bool
	Music                bool // a music station, for the default picture when there is none

	// Followed is another of Home Assistant's players on the page (follow.go): its buttons go to it,
	// and Done puts it away rather than stopping it.
	Followed bool
}

type Feature struct {
	// Changed fires when anything shown changes; listeners must not block.
	Changed hook.Hook[struct{}]
	// Hint fires with a note for the screen (hint.go); listeners must not block.
	Hint hook.Hook[Hint]

	mu     sync.Mutex
	chosen string

	loops sync.WaitGroup // the goroutines a camera or picture view started (spawn); a test waits on it

	// others are the entities followed for something besides the glance strip, and chips the glance
	// strip's chips as last sent on Changed (glance.go, redraw).
	others map[string]bool
	chips  []Chip

	// grouped is whether Music Assistant has this room playing along with any other, as last asked, and
	// groupedAt when it was asked. See PokeGroup: the row that says what a Stop will do is drawn every
	// frame, and the answer is a request to Home Assistant.
	grouped   bool
	groupedAt time.Time

	// listed is the Radio Browser station tapped last, at listedAt, to name the stream that follows.
	listed   string
	listedAt time.Time

	// weatherSel picks the weather entity; weathers is Home Assistant's list of them, fetched at
	// weathersAt.
	weatherSel  *esphome.Select
	weatherOpts *component.Options // weatherSel's options
	radarSel    *esphome.Select    // where the rain map's radar comes from (radar_setting.go)

	// alerts are the NWS's weather alerts at home and nearby (alerts.go); alertsSw turns them on and off.
	alerts     alertState
	alertsSw   *esphome.Switch
	weathers   []hass.Entity
	weathersAt time.Time

	// followSel picks another media player for Now Playing to follow; players is Home Assistant's list of
	// them, fetched at playersAt (follow.go).
	followSel  *esphome.Select
	followOpts *component.Options // followSel's options
	players    []hass.Entity
	playersAt  time.Time

	lyricsSw *esphome.Switch // the words on Now Playing (lyrics.go)

	// haCameras is every camera Home Assistant has, fetched at haCamerasAt, for a device given no list
	// of its own; haCamerasBusy is a fetch under way.
	haCameras     []config.Camera
	haCamerasAt   time.Time
	haCamerasBusy bool

	// The radio's lists from Home Assistant's Radio Browser; see local.go.
	lists radioLists

	// radar is the rain map; see radar.go.
	radar   radarState
	url     string    // the stream playing, from the media player
	urlAt   time.Time // when it started
	urlName string    // its station name once found

	// asked is the last station this device asked to play and askedAt when, which is what a dropped
	// stream is put back on with.
	asked   string
	askedAt time.Time

	// claiming is a station asked for whose stream has not started yet, and stationURL the url that
	// started for the last one: what plays from it is live radio (liveURL).
	// claimName is the station claimed for, claimAt when the claim was made and claimFor how long it
	// holds, and claimedAt when a stream was last taken for one. ownURL is the last stream this device
	// played itself for a station (playingOwn).
	claiming   bool
	stationURL string
	claimName  string
	claimAt    time.Time
	claimFor   time.Duration
	claimedAt  time.Time
	ownURL     string
	// claimByScript is a claim made because the station script turned on (stationStarted), and
	// byScript a stream taken for a station that way: the name the text gives may still be the station
	// before, so a name arriving just after replaces it, and the stream is put back by its url.
	claimByScript, byScript bool
	// stationGen counts the stations asked for and forgotten, so a put-back waiting its seconds can
	// tell a Stop or another station came in between (putBack).
	stationGen uint64

	// startSeq counts the stations asked of Home Assistant through the radio script, heardAt is when it
	// last gave a sign of starting one, and hinted is a note up that said it did not (hint.go).
	startSeq uint64
	heardAt  time.Time
	hinted   bool
	// external stands in for whether Music Assistant plays on the device, in a test (externalPlaying).
	external func() bool

	// resumed counts how many times a dropped stream has been put back on lately, and resumedAt when
	// the last of those was, so a station that will not stay up is eventually left off (resume.go).
	resumed   int
	resumedAt time.Time
	forecast  []hass.Day
	fetched   time.Time
	// own is the weather now as the device fetched it itself (own_weather.go), for a device with no
	// Home Assistant; ownAt is when.
	own   openmeteo.Now
	ownAt time.Time

	poke chan struct{}

	// meta is what the playing station is playing, for the now-playing screen; metaPoke asks for
	// a refresh when the station changes.
	meta     meta
	metaPoke chan struct{}

	// cam is the camera view in progress; see camera.go.
	cam CameraView
	// viewSeq numbers the views put up (CameraView.View).
	viewSeq uint64
	// stillCancel gives up the fetch of the still on the view (picture.go), when there is one.
	stillCancel context.CancelFunc
	// waiting is a still that waits for the camera view up to end (picture.go), nil for none.
	waiting *waitingStill
	// endedID and endedWhy are the last notification's view that went other than by running out, and
	// how (PictureEnded).
	endedID, endedWhy string
	// camGen numbers the camera views whose frames are fetched (CameraView.gen), and camCancel ends what
	// the one up fetches with.
	camGen    uint64
	camCancel context.CancelFunc

	// camSound is the camera whose sound this view has, camOver the media player's token for the request
	// it was asked with, and camMuted whether that sound was silenced from the screen. camSound stays for
	// as long as the view does, playing or not: the view still has a sound, which is what the control on
	// the screen is drawn from, and muting it is not the end of it. cameraSoundSw is the setting's
	// switch. See camera_sound.go.
	// radioStationTxt, radioArtistTxt and radioTitleTxt are what the radio is playing, for Home
	// Assistant (radio_sensors.go).
	radioStationTxt, radioArtistTxt, radioTitleTxt *esphome.TextSensor

	// nowSourceTxt, nowTitleTxt, nowArtistTxt, nowAlbumTxt and nowStateTxt are what is played to the
	// device, for Home Assistant; nowPoke asks for them to be looked at again (nowplaying_sensors.go).
	nowSourceTxt, nowTitleTxt, nowArtistTxt, nowAlbumTxt, nowStateTxt *esphome.TextSensor
	nowPoke                                                           chan struct{}

	camSound      string
	camOver       media.OverToken
	camMuted      bool
	cameraSoundSw *esphome.Switch
	cameraLiveSw  *esphome.Switch

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
	slideshowShuffleSw *esphome.Switch
	// slideshowArtSw is weather art in place of the photos (Weather art).
	slideshowArtSw        *esphome.Switch
	slideshowSubfoldersSw *esphome.Switch
	// slideshowWholeSw shows photos whole, with blurred sides, instead of cropped to fill.
	slideshowWholeSw *esphome.Switch
	// slideshowPairsSw puts two tall photos side by side; slideshowCaptionSw writes when and where an
	// Immich photo was taken over it.
	slideshowPairsSw   *esphome.Switch
	slideshowCaptionSw *esphome.Switch
	// immichFavoritesSw, immichPersonTxt, immichFromTxt and immichToTxt narrow an Immich source.
	immichFavoritesSw *esphome.Switch
	immichPersonTxt   *esphome.Text
	immichFromTxt     *esphome.Text
	immichToTxt       *esphome.Text

	slideshow slideshowState
	// slideshowGen counts the slideshow's changes of source (changeSlideshow, ResetImmich), so a list
	// gathered for the source before is not kept for the one after.
	slideshowGen uint64
}

// forecastEvery is how often the forecast is refreshed while there is a weather entity.
const forecastEvery = 30 * time.Minute

// Run keeps the forecast current. Nothing to do without a token or a weather entity.
func (f *Feature) Run(ctx context.Context) error {
	// A station this device asked for is live radio: a drop is fetched again (media resume.go), where
	// a song would start over.
	media.SetLive(f.liveURL)
	go f.metaLoop(ctx)
	go f.nowPlayingLoop(ctx)
	if hasScreen {
		go f.slideshowLoop(ctx)
	}
	for {
		f.refreshSources()
		f.refreshPlayers()
		f.refreshForecast()
		select {
		case <-ctx.Done():
			f.stopWaiting()
			return nil
		case <-f.poke:
		case <-time.After(forecastEvery):
		}
	}
}

func (f *Feature) refreshForecast() {
	entity := config.Get().Home.WeatherEntity()
	if entity == "" {
		return
	}
	if !hass.Get().Ready() {
		f.refreshOwnWeather()
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
		shared = &Feature{poke: make(chan struct{}, 1), metaPoke: make(chan struct{}, 1), nowPoke: make(chan struct{}, 1)}
		shared.buildWeatherSelect()
		shared.buildFollowSelect()
		shared.buildLyricsSwitch()
		shared.buildRadarSelect()
		shared.buildAlertsSwitch()
		shared.buildCameraSoundSwitch()
		shared.buildCameraLiveSwitch()
		shared.buildSlideshowSelect()
		shared.buildRadioSensors()
		shared.buildNowPlayingSensors()
		hastate.Get().Changed.Listen(shared.stateChanged)
		hastate.Get().Changed.Listen(shared.stationStarted)
		media.Get().OnPlay.Listen(shared.played)
		// Stopped anywhere (the voice assistant, the setup page, the action button, Home Assistant):
		// the station is over, and a put-back on its way is not made.
		media.Get().OnStop.Listen(func(struct{}) {
			shared.mu.Lock()
			shared.stopped()
			shared.mu.Unlock()
		})
		media.Get().OnEnd.Listen(shared.ended)
		media.Get().OnNowPlaying.Listen(func(struct{}) { shared.pokeNowPlaying() })
		media.Get().OnResumeRemote.Listen(func(struct{}) { safe.Go("resume music assistant", resumeMusicAssistant) })
		media.Get().OnTakeOver.Listen(func(struct{}) { safe.Go("leave the group", leaveGroup) })
	})
	return shared
}

// played is a new stream starting: whatever was tapped is no longer the answer to "what is
// this", so its name is looked up from the URL.
func (f *Feature) played(url string) {
	f.streamArrived()
	f.mu.Lock()
	f.url, f.urlName, f.chosen = url, "", ""
	f.urlAt = time.Now()
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
	// A file of Home Assistant's own - rain to sleep to - is not a station, and without a name of
	// its own the page would fall back to the house's "last station" text: the rain named after
	// whatever some other speaker last played.
	if name == "" {
		name = fileName(url)
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

// fileName is a name for a sound file Home Assistant serves from its own folders ("/local/sleep/
// rain_deep.mp3" is "Rain deep"), or "" for anything else. Only those folders: a station's stream can
// end in ".mp3" too, and its file name ("live", "stream") would be a worse name than the station's.
func fileName(url string) string {
	path, _, _ := strings.Cut(url, "?")
	if p, err := neturl.PathUnescape(path); err == nil {
		path = p
	}
	if !strings.Contains(path, "/local/") && !strings.Contains(path, "/media/local/") {
		return ""
	}
	base := path[strings.LastIndex(path, "/")+1:]
	dot := strings.LastIndex(base, ".")
	if dot <= 0 {
		return ""
	}
	switch strings.ToLower(base[dot+1:]) {
	case "mp3", "ogg", "oga", "opus", "flac", "wav", "m4a", "aac":
	default:
		return ""
	}
	name := strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ").Replace(base[:dot]))
	if name == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToUpper(first)) + name[size:]
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
	if id, ok := strings.CutPrefix(c.Home.Slideshow.Source, ImmichAlbumPrefix); ok && c.Home.Immich.Set() && c.Home.Immich.AlbumID != id {
		// Kept before the album's id was kept with its name: asked for once the network is up.
		naming.Add(1)
		safe.Go("immich album name at start", func() { defer naming.Done(); f.nameImmichAlbumSoon(id) })
	}
	f.weatherOpts.Restore(weatherOptions(c.Home))
	f.weatherSel.Set(chosenOption(c.Home))
	f.followOpts.Restore(followOptions(c.Home))
	f.followSel.Set(followOption(c.Home))
	f.lyricsSw.Set(c.Home.Lyrics)
	if hasScreen {
		f.restartFollow()
	}
	f.radarSel.Set(radarChoices[RadarSourceIndex()].label)
	f.alertsSw.Set(!c.Home.AlertsOff)
	f.showRadio(meta{}) // nothing plays at a start; the poller fills them in
	// Nor is anything played to it. All of them are written, though the four names hold "" already, so
	// none of them starts out unknown in Home Assistant.
	f.showNowPlaying(nil, nothingPlaying)
	f.cameraSoundSw.Set(c.Home.CameraSound)
	f.cameraLiveSw.Set(c.Home.CameraLive)
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
		f.slideshowArtSw.Set(c.Home.Slideshow.Art)
		f.slideshowSubfoldersSw.Set(!c.Home.Slideshow.TopOnly)
		f.slideshowWholeSw.Set(c.Home.Slideshow.WholePhoto)
		f.slideshowPairsSw.Set(c.Home.Slideshow.Pairs && PairsHere())
		f.slideshowCaptionSw.Set(c.Home.Slideshow.Caption)
		f.immichFavoritesSw.Set(c.Home.Slideshow.ImmichFavorites)
		f.immichPersonTxt.Set(c.Home.Slideshow.ImmichPerson)
		f.immichFromTxt.Set(c.Home.Slideshow.ImmichFrom)
		f.immichToTxt.Set(c.Home.Slideshow.ImmichTo)
	}
	f.want(c.Home)
}

func (f *Feature) want(h config.Home) {
	var keys []hastate.Key
	if w := h.WeatherEntity(); w != "" {
		keys = append(keys, hastate.Key{Entity: w}, hastate.Key{Entity: w, Attribute: "temperature"},
			hastate.Key{Entity: w, Attribute: "temperature_unit"}, hastate.Key{Entity: w, Attribute: "friendly_name"})
	}
	// Where the rain map and weather alerts are centered: home, or the zone this device was given.
	zone := h.HomeZone()
	keys = append(keys, hastate.Key{Entity: zone, Attribute: "latitude"}, hastate.Key{Entity: zone, Attribute: "longitude"})
	for _, s := range h.Radio.Stations {
		keys = append(keys, hastate.Key{Entity: s, Attribute: "options"})
	}
	if h.Radio.Now != "" {
		keys = append(keys, hastate.Key{Entity: h.Radio.Now})
	}
	// The script that plays a station: it turns on whenever a station is started with it, from here
	// or anywhere in the house (stationStarted), the same station again too, which changes no text.
	if s := h.Radio.Service; strings.HasPrefix(s, "script.") {
		keys = append(keys, hastate.Key{Entity: s})
	}
	others := make(map[string]bool, len(keys))
	for _, k := range keys {
		others[k.Entity] = true
	}
	f.mu.Lock()
	f.others = others
	f.mu.Unlock()
	if hasGlance {
		keys = append(keys, glanceKeys(h)...)
	}
	hastate.Get().Follow("home", keys...)
}

// locationAction centers the rain map and weather alerts on a zone rather than home, for a device in
// another house: "zone.cabin", or empty (or "home") for home again.
func (f *Feature) locationAction() *esphome.Action {
	return &esphome.Action{
		Name: "home_location",
		Args: []esphome.Arg{{Name: "zone", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			zone := strings.ToLower(strings.TrimSpace(c.String("zone")))
			switch zone {
			case "", "home", config.HomeZoneDefault:
				zone = ""
			default:
				if !strings.HasPrefix(zone, "zone.") || len(zone) == len("zone.") {
					return nil, fmt.Errorf("home_location: %q is not a zone entity, like zone.cabin", zone)
				}
			}
			if err := config.Set().Home().Location(zone); err != nil {
				return nil, err
			}
			slog.Info("home: location", "zone", config.Get().Home.HomeZone())
			f.rewire()
			return nil, nil
		},
	}
}

// Actions are how Home Assistant configures this: which weather entity to show, and how the
// radio page is wired. Both persist and take effect at the next connection.
func (f *Feature) Actions() []*esphome.Action {
	actions := append(append(f.cameraActions(), f.gridActions()...), f.accessAction(), f.go2rtcAction())
	if hasScreen {
		actions = append(actions, f.slideshowAction(), f.calendarAction(), f.locationAction(), f.cameraButtonsAction())
	}
	if hasGlance {
		actions = append(actions, f.glanceAction())
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
			was := hass.Get().Address()
			if err := hass.Get().Set(c.String("url"), c.String("token")); err != nil {
				return nil, err
			}
			// Realtime voice's token is for the Home Assistant it was given for: another address does
			// not get it. The first address, at setup, does: a token typed in before it is for it, and
			// a device that left its last Home Assistant cleared the token then (api's leave).
			if now := hass.Get().Address(); was != "" && now != was && config.Get().Realtime.MCPToken != "" {
				_ = config.Set().Realtime().SetMCPToken("")
				slog.Warn("home: Home Assistant's address changed; realtime voice's token for it was cleared")
			}
			slog.Info("home: home assistant access stored")
			f.wake()
			return nil, nil
		},
	}
}

// Poke has the forecast fetched again now, as after the place it is for has changed.
func (f *Feature) Poke() { f.wake() }

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
	// Nothing from Home Assistant: the device's own, when it has fetched some.
	if w.Condition == "" {
		w = f.ownWeather()
	} else if temp, ok := t.Value(entity, "temperature"); ok && temp != "" && temp != "None" {
		if i := strings.IndexByte(temp, '.'); i > 0 {
			temp = temp[:i]
		}
		w.Temp = temp + "°"
	}
	w.Condition = f.SkyAt(w.Condition, time.Now())
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
		return withFollowed(carried(r))
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
	// Both from the music's own state, which is what the page is drawn from: a remote's track paused from
	// here is held with play offered rather than playing, and the list needs to know that as the page does.
	r.Playing, r.Paused = media.Get().ScreenState()
	// Only while there is something of Music Assistant's being carried: the row warns that a stop reaches
	// the group, and a Stop only reaches Music Assistant's player while this device is carrying its
	// stream. A grouped Music Assistant player with a station of this device's playing is not reached.
	r.Grouped = f.Grouped() && media.Get().Carried()
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

	return withFollowed(carried(r))
}

// carried puts what a remote is playing on the page, over whatever this device chose. It is the last
// word wherever the page is built: everything before it says what this device picked, and that is what
// the room is playing only when nothing else is.
//
// Receiving() is not the name of it. That names a phone over Bluetooth, which is a stream of this
// player's own rather than a remote's; the only thing that takes the speaker this way is Sendspin, and
// what runs the server it talks to here is Music Assistant, so that is what the page calls it.
//
// Whose song it is comes from whosePlaying, the order the now-playing sensors go by too.
func carried(r Radio) Radio {
	s := snapshotOf(media.Get())
	switch whosePlaying(s, false) {
	case playingOwn:
		if !bluetoothFrom(s.recvFrom) {
			break
		}
		// A phone playing over Bluetooth is a stream of this player's own, but not the radio's: the page
		// names the phone, not the station last chosen here or Home Assistant's "last station" text,
		// which went on naming a station while a phone played (and after the strip was closed and the
		// phone played again). The song only if the device was told one; nothing reads one from a phone
		// yet.
		r.Chosen = ""
		r.Playing, r.Paused = s.ownPlaying, s.ownPaused
		r.Now, r.Title, r.Artist, r.Album = s.recvFrom, s.recvTitle, s.recvArtist, s.recvAlbum
		r.Art, r.Thumb = nil, nil
		r.Logo, r.Music = false, true
	case playingReceived:
		// A receiver this device runs (AirPlay, Spotify Connect: feature/streaming) is playing to it: the
		// page names the app, and the song and its cover where it gave them.
		r.Chosen = ""
		r.Playing, r.Paused = s.ownPlaying, s.ownPaused
		r.Now, r.Title, r.Artist, r.Album = s.recvFrom, s.recvTitle, s.recvArtist, s.recvAlbum
		r.Art, r.Thumb = receivedArt(s.recvFrom)
		r.Logo, r.Music = false, true
	case playingHeld:
		// A remote's track paused from here is still the page's, with play on it, after the remote
		// has let it go.
		r.Playing, r.Now = false, "Music Assistant"
		r.Title, r.Artist, r.Album = s.heldTitle, s.heldArtist, s.heldAlbum
		r.Art, r.Thumb = remoteArt()
		r.Logo = false
		r.Music = true
	case playingCarried:
		// Nothing of this device's is starting while a remote is what the room hears. A station tapped
		// just before Music Assistant took the speaker never plays - the remote ends it - and the list
		// went on reading "Starting…" for it for as long as the remote carried on.
		r.Chosen = ""
		r.Playing, r.Now = true, "Music Assistant"
		r.Title, r.Artist, r.Album = s.extTitle, s.extArtist, s.extAlbum
		// Music Assistant's own picture for the track when it sent one. Without one the page draws a
		// stand-in of its own, so the last station's cover and logo must not be left behind somebody
		// else's.
		r.Art, r.Thumb = remoteArt()
		r.Logo = false
		r.Music = true
	}
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
func (f *Feature) Play(station string) { f.play(station, nil) }

// play is Play; with gen, only while the station's generation still is *gen (a put-back, which a Stop
// or another station since cancels: putBack).
func (f *Feature) play(station string, gen *uint64) {
	// What was asked for, in the words the lists use, so a stream that drops can be asked for again:
	// the name worked out from a URL afterwards is often the stream's host, which no list has.
	f.mu.Lock()
	if gen != nil && f.stationGen != *gen {
		f.mu.Unlock()
		return
	}
	f.askStation(station)
	g := f.stationGen
	f.mu.Unlock()

	source := radioSource()
	// Favorites with nothing in them show the stations near home instead (Radio).
	if source == config.RadioFavorites && len(favoriteNames(config.Get().Home.Radio)) == 0 {
		source = config.RadioLocal
	}
	if source == config.RadioOwn {
		if f.playOwn(station, &g) {
			f.Changed.Emit(struct{}{})
			f.pokeMeta()
		} else {
			slog.Info("radio: the station is not one kept on the device; not played", "station", station)
			f.unask(station)
		}
		return
	}
	if source != config.RadioFavorites {
		if f.playListed(source, station) {
			f.Changed.Emit(struct{}{})
			f.pokeMeta()
		} else {
			slog.Info("radio: the station is not on the list the radio page shows; not played", "station", station, "list", source)
			f.unask(station)
		}
		return
	}
	h := config.Get().Home.Radio
	if !h.Configured() {
		f.unask(station)
		return
	}
	f.mu.Lock()
	f.chosen = station
	f.mu.Unlock()
	// Watched only when somebody asked for it (hint.go): a put-back, after a stream dropped, is the
	// device's own doing, and a note about it would be about nothing anybody did.
	f.callFavorite(h, station, gen == nil)
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

// Stop ends whatever is playing, whoever is playing it.
//
// Every caller is a person asking - the Stop row on either panel, the Done button, "go home" by voice -
// so a stream this device did not start is asked to stop, and any stream of this player's own is ended
// rather than paused: the row means the music, so the station goes and the page goes back to the clock
// with it. The rule is who is asking, not what the stream is. Home Assistant's own stop is a different
// path and stays the pause (see media's command), because that arrives from an automation with nobody
// necessarily in the room, and a track it paused is still the page's with play on it. A server that will
// take only a pause gets a pause, because that is the strongest thing it offers, which is what the row
// did before anything was carried and what it should go on doing.
func (f *Feature) Stop() {
	if Following() {
		// The page is another room's player: a stop here puts it away, and leaves that music alone.
		f.DismissFollowed()
		return
	}
	// The same request a transport button makes, so that what a stop reaches is media's to decide: a
	// stream this device did not start is asked to stop, a track it is holding for somebody else is let
	// go, and its own stream is ended. Working any of it out here as well is how the hold came to be
	// dropped after the ask that had just made it.
	media.Get().Transport(media.TransportStop)
	// This player's own stream goes with it, whoever is being heard: a station can be sitting under a
	// carried stream while the two hand the speaker over, and a person asking for silence means all of
	// it. With nothing of its own playing this does nothing.
	media.Get().Stop()
	f.mu.Lock()
	f.chosen = ""
	f.forgetStation()
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
	f.pokeMeta()
}

// The names the receivers play their tracks under (feature/streaming), which the page shows as is.
const (
	AirPlayName = "AirPlay"
	SpotifyName = "Spotify"
	DLNAName    = "DLNA"
)

// Receiver is whether a received track's name is one of the receivers'.
func Receiver(from string) bool {
	return from == AirPlayName || from == SpotifyName || from == DLNAName
}
