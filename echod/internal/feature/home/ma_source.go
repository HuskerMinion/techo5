package home

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// Where a Music Assistant track comes from: Spotify, Plex, a radio station, the music on its own disks.
// Over Sendspin a track arrives with its title, artist and album and nothing else, so the page and the
// now_playing_source sensor called all of it "Music Assistant". Home Assistant has the rest: Music
// Assistant's entity for this device names the track by its URI in media_content_id, and the URI starts
// with the provider it came from (spotify--<instance>://track/..., plex--<instance>://track/...,
// radiobrowser://radio/..., library://track/...). The media_title action did not help: Music Assistant
// plays first and the name it gave is gone by then.
//
// The entity is asked once per track, in the background, with the Home Assistant token the device was
// given (as resume and the emptied queue ask it, resume_ma.go and emptied_ma.go). Without the token, or
// for a provider not known here, the name stays Music Assistant.

// MusicAssistantName is what a Music Assistant track is called when where it comes from is not known.
const MusicAssistantName = "Music Assistant"

// The services a provider can be named as, besides its brand. The screen says them in its own language
// (maScreenName); the sensor says them as they are, as it says Radio and Playback.
const (
	maServiceRadio   = "Radio"
	maServiceLibrary = "Library"
)

// maProviders is the service each Music Assistant provider is, by its domain: the part of the URI
// before "--<instance>://", or before "://" for the ones that have no instances.
var maProviders = map[string]string{
	"spotify":          "Spotify",
	"plex":             "Plex",
	"ytmusic":          "YouTube Music",
	"tidal":            "Tidal",
	"qobuz":            "Qobuz",
	"deezer":           "Deezer",
	"apple_music":      "Apple Music",
	"soundcloud":       "SoundCloud",
	"jellyfin":         "Jellyfin",
	"radiobrowser":     maServiceRadio,
	"tunein":           maServiceRadio,
	"library":          maServiceLibrary,
	"filesystem_local": maServiceLibrary,
	"filesystem_smb":   maServiceLibrary,
}

// maServiceOf is the service the URI Music Assistant plays (media_content_id) comes from, empty when it
// is not one known here: a plain URL, a provider added since, or nothing at all.
func maServiceOf(contentID string) string {
	scheme, _, ok := strings.Cut(strings.TrimSpace(contentID), "://")
	if !ok || scheme == "" {
		return ""
	}
	domain, _, _ := strings.Cut(strings.ToLower(scheme), "--")
	return maProviders[domain]
}

// maSourceName is s as the sensor says it: the service when it is known, Music Assistant otherwise.
func maSourceName(s string) string {
	if s == "" {
		return MusicAssistantName
	}
	return s
}

// maScreenName is s as the page says it: a brand as it is, Radio and Library in the screen's language,
// and Music Assistant when the service is not known.
func maScreenName(s string) string {
	lang := config.Get().Screen.Language
	switch s {
	case "":
		return MusicAssistantName
	case maServiceRadio:
		return locale.Text(lang, "drawer_radio")
	case maServiceLibrary:
		return locale.Text(lang, "media_library")
	}
	return s
}

// maSource is the service of the track Music Assistant plays here, as last found out.
//
// It is kept by the track's title, since that is all a track is over Sendspin: a title asked about once
// stays answered, so a held track (the same title, paused) and the same song again cost nothing. While a
// new title is asked about the last answer stands, so a page going from one Spotify song to the next
// does not read "Music Assistant" for the moment in between; a change of service shows when the answer
// comes, a moment later.
var maSource struct {
	mu     sync.Mutex
	title  string // the track asked about last
	known  bool   // whether the answer for title has come
	name   string // the answer for title once known: a service, or empty for none known
	last   string // the last answer that came, for any title
	asking bool   // a lookup is running
}

// maSourceFor is the service the Music Assistant track named title comes from, empty when it is not
// known (yet). A title not asked about yet is asked about in the background, when the device has Home
// Assistant's address and token; the answer pokes the sensors and the page (maSourceFound).
func maSourceFor(title string) string {
	maSource.mu.Lock()
	defer maSource.mu.Unlock()
	if title == "" {
		// Music Assistant clears the name before it lets go of a paused track: nothing new to ask.
		return maSource.last
	}
	if title == maSource.title {
		if maSource.known {
			return maSource.name
		}
		return maSource.last
	}
	if !maCanAsk() {
		return ""
	}
	maSource.title, maSource.known = title, false
	if !maSource.asking {
		maSource.asking = true
		go maLookup()
	}
	return maSource.last
}

// maCanAsk is whether Home Assistant can be asked; tests put their own in.
var maCanAsk = func() bool { return hass.Get().Ready() }

// maSourceFound is told when an answer has come; it has the sensors and the page look again.
var maSourceFound = func() {
	f := Get()
	f.pokeNowPlaying()
	f.Changed.Emit(struct{}{})
}

const (
	// maLookupTries is how many times Home Assistant is asked before its answer is taken whatever it
	// names, and maLookupEvery the wait between them. Music Assistant tells this device of a new track
	// over Sendspin and Home Assistant over its own connection, and either can be first: an entity still
	// naming the last song has not caught up yet. A title that never matches (a station's song, which
	// Home Assistant may name differently) is taken after the last try, as the provider is the same.
	maLookupTries = 5
	maLookupEvery = time.Second
)

// maLookupWait is the wait between tries; tests make it short.
var maLookupWait = func() { time.Sleep(maLookupEvery) }

// maLookup finds out the service of maSource.title, and of whatever title is asked about while it runs,
// until it has caught up with the last.
func maLookup() {
	for {
		maSource.mu.Lock()
		title := maSource.title
		maSource.mu.Unlock()

		name := maLookupTitle(title)

		maSource.mu.Lock()
		if maSource.title != title {
			// Another track came meanwhile: this answer is for nobody.
			maSource.mu.Unlock()
			continue
		}
		maSource.known, maSource.name, maSource.last, maSource.asking = true, name, name, false
		maSource.mu.Unlock()
		slog.Info("music assistant: track source", "source", maSourceName(name))
		maSourceFound()
		return
	}
}

// maLookupTitle asks Home Assistant what Music Assistant's entity for this device plays, until it names
// title or the tries run out, and says the service of what it last named.
func maLookupTitle(title string) string {
	service := ""
	for try := 0; try < maLookupTries; try++ {
		if try > 0 {
			maLookupWait()
		}
		content, named, ok := maContent()
		if !ok {
			return service
		}
		service = maServiceOf(content)
		if sameTitle(named, title) {
			return service
		}
	}
	return service
}

// sameTitle is whether Home Assistant's media_title is the title Sendspin gave, as far as the two can
// differ: Sendspin's is cleaned for the screen (cleanTitle in feature/sendspin) and either may carry
// spaces at its ends.
func sameTitle(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// maEntity is Music Assistant's entity for this device as last found, and when, so a track does not cost
// a read of every state in Home Assistant (musicAssistantPlayer); it is found again after maEntityEvery,
// or when asking it fails.
var maEntity struct {
	mu sync.Mutex
	id string
	at time.Time
}

const maEntityEvery = 10 * time.Minute

// maContent is what Music Assistant's entity for this device plays: its media_content_id and
// media_title. ok is false when Home Assistant could not be asked or has no such entity. Tests put their
// own in.
var maContent = func() (contentID, title string, ok bool) {
	maEntity.mu.Lock()
	id := maEntity.id
	if id == "" || time.Since(maEntity.at) > maEntityEvery {
		maEntity.mu.Unlock()
		found, err := musicAssistantPlayer()
		if err != nil || found == "" {
			return "", "", false
		}
		maEntity.mu.Lock()
		maEntity.id, maEntity.at, id = found, time.Now(), found
	}
	maEntity.mu.Unlock()

	st, err := hass.Get().State(id)
	if err != nil {
		maEntity.mu.Lock()
		maEntity.id = "" // renamed or gone: found again next time
		maEntity.mu.Unlock()
		return "", "", false
	}
	contentID, _ = st.Attributes["media_content_id"].(string)
	title, _ = st.Attributes["media_title"].(string)
	return contentID, title, true
}
