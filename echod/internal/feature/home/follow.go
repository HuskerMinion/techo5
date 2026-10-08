package home

import (
	"context"
	"image"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Following another player: Now Playing can show what one of Home Assistant's other media players is
// playing, a Sonos in the same room or the kitchen's speaker, with its cover, and its play, pause, back
// and forward buttons reach that player. It is chosen from the "Now Playing follows" select (or the
// settings sheet) and is off until then. Anything this device plays itself comes first: the followed
// player is only on the page while this device has nothing of its own on it.
//
// The player is followed over Home Assistant's websocket, which needs the address and token from the
// home_assistant action; the same token fetches its cover and sends the buttons.

// followNone is the option for following nothing, as the select and the screen say it.
const followNone = "None"

// followed is the followed player as last heard: its state, the parts of its attributes the page
// shows, and the cover for the picture it last named.
var followed struct {
	mu      sync.Mutex
	entity  string // the player these are for; anything else is stale
	state   string // "playing", "paused", "idle", "off", ...
	name    string // friendly_name
	title   string
	artist  string
	album   string
	picture string // entity_picture, as Home Assistant gives it
	artFor  string // the picture art and thumb were made from

	fetching  string        // the picture whose cover is being fetched now, or ""
	coverAt   time.Time     // not before this is the cover tried again, after a failure
	coverWait time.Duration // the wait after the last failure, doubling to five minutes
	art       *image.RGBA
	thumb     *image.RGBA

	// pos is where the song is, from media_position and the time Home Assistant took it, when the
	// player says (posSet); for the lyrics.
	pos    media.Position
	posSet bool

	// dismissed is the track the page was put away on with Done: the followed player is somebody
	// else's music, so Done does not stop it, it only puts it away until the next track.
	dismissed string

	// played is whether the player has been heard playing since this follow began, and pausedAt
	// when it last went from playing to paused. Some players never stop: a Sonos that is stopped
	// stays paused on its last track for good. So a pause lets the page go after followPausedFor,
	// and a player first heard paused (a restart in the middle of one) is not shown until it
	// plays. pauseEnd redraws the page when the pause runs out.
	played   bool
	pausedAt time.Time
	pauseEnd *time.Timer

	cancel context.CancelFunc // ends the current follow
}

// followPausedFor is how long a paused followed player stays on the page.
var followPausedFor = 10 * time.Minute

func (f *Feature) buildFollowSelect() {
	f.followSel = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "now_playing_follows",
			Name:     "Now Playing follows",
			Icon:     "mdi:speaker-multiple",
			Category: esphome.CategoryConfig,
		},
		Options:   followOptions(config.Get().Home),
		OnCommand: func(v string) { f.ChooseFollow(followEntity(v)) },
	}
}

// followOptions is what the select offers: none, the one chosen, and every media player Home
// Assistant listed when it was last asked, this device's own left out.
func followOptions(h config.Home) []string {
	opts := []string{followNone}
	add := func(id string) {
		if id != "" && !slices.Contains(opts, id) {
			opts = append(opts, id)
		}
	}
	add(h.FollowPlayer)
	for _, id := range h.PlayerSources {
		add(id)
	}
	return opts
}

// followEntity is the entity an option names, "" for none.
func followEntity(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "media_player.") || !entityID.MatchString(v) {
		return ""
	}
	return v
}

// entityID is what an entity id looks like; anything else is not used, since an id goes into a template.
var entityID = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9_]+$`)

func followOption(h config.Home) string {
	if h.FollowPlayer != "" {
		return h.FollowPlayer
	}
	return followNone
}

// ChooseFollow follows a media player, "" for none.
func (f *Feature) ChooseFollow(entity string) {
	if err := config.Set().Home().FollowPlayer(entity); err != nil {
		slog.Warn("home: saving the followed player failed", "err", err)
		return
	}
	slog.Info("home: now playing follows", "entity", entity)
	f.mu.Lock()
	f.followSel.Options = followOptions(config.Get().Home)
	f.mu.Unlock()
	f.followSel.Set(followOption(config.Get().Home))
	f.restartFollow()
	f.Changed.Emit(struct{}{})
}

// NextFollow moves to the next option, for the settings sheet.
func (f *Feature) NextFollow() {
	f.mu.Lock()
	opts := f.followSel.Options
	f.mu.Unlock()
	i := slices.Index(opts, followOption(config.Get().Home))
	f.ChooseFollow(followEntity(opts[(i+1)%len(opts)]))
}

// FollowChoices is what the screen's list offers, as the select does: each player's entity ("" for
// none) and its name, with the index of the one in force, or -1.
func (f *Feature) FollowChoices() (entities, names []string, cur int) {
	cur = -1
	chosen := followOption(config.Get().Home)
	f.mu.Lock()
	known := append([]hass.Entity(nil), f.players...)
	opts := f.followSel.Options
	f.mu.Unlock()
	for i, o := range opts {
		e := followEntity(o)
		entities = append(entities, e)
		names = append(names, playerName(e, known))
		if o == chosen {
			cur = i
		}
	}
	return entities, names, cur
}

// FollowSource is the followed player as the sheet says it.
func (f *Feature) FollowSource() string {
	f.mu.Lock()
	known := append([]hass.Entity(nil), f.players...)
	f.mu.Unlock()
	return playerName(config.Get().Home.FollowPlayer, known)
}

func playerName(entity string, known []hass.Entity) string {
	if entity == "" {
		return followNone
	}
	for _, e := range known {
		if e.ID == entity && e.Name != "" {
			return e.Name
		}
	}
	return entity
}

// refreshPlayers asks Home Assistant which media players it has, and offers them, on the weather list's
// schedule.
func (f *Feature) refreshPlayers() {
	if !hasScreen {
		return // following is for a page to show it on
	}
	f.mu.Lock()
	due := time.Since(f.playersAt) > sourcesEvery
	f.mu.Unlock()
	if !due || !hass.Get().Ready() {
		return
	}
	list, err := hass.Get().Entities("media_player")
	if err != nil {
		slog.Warn("home: listing media players", "err", err)
		return
	}
	own := speakerEntity()
	list = slices.DeleteFunc(list, func(e hass.Entity) bool { return e.ID == own })
	f.mu.Lock()
	f.players, f.playersAt = list, time.Now()
	f.mu.Unlock()
	ids := make([]string, 0, len(list))
	for _, e := range list {
		ids = append(ids, e.ID)
	}
	if !slices.Equal(ids, config.Get().Home.PlayerSources) {
		if err := config.Set().Home().PlayerSources(ids); err != nil {
			slog.Warn("home: saving the media players failed", "err", err)
		}
	}
	opts := followOptions(config.Get().Home)
	f.mu.Lock()
	same := slices.Equal(opts, f.followSel.Options)
	if !same {
		f.followSel.Options = opts // a new slice: one being read elsewhere is left as it was
	}
	f.mu.Unlock()
	if same {
		return
	}
	// New options reach Home Assistant at the next connection.
	f.rewire()
}

// restartFollow ends the follow in progress and starts one for the player chosen now, if any.
func (f *Feature) restartFollow() {
	entity := config.Get().Home.FollowPlayer
	followed.mu.Lock()
	if followed.cancel != nil {
		followed.cancel()
		followed.cancel = nil
	}
	followed.entity, followed.state, followed.title, followed.artist, followed.album = entity, "", "", "", ""
	followed.name, followed.picture, followed.artFor, followed.art, followed.thumb = "", "", "", nil, nil
	followed.dismissed, followed.coverAt, followed.coverWait, followed.fetching = "", time.Time{}, 0, ""
	followed.pos, followed.posSet = media.Position{}, false
	followed.played, followed.pausedAt = false, time.Time{}
	if followed.pauseEnd != nil {
		followed.pauseEnd.Stop()
		followed.pauseEnd = nil
	}
	if entity == "" {
		followed.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	followed.cancel = cancel
	followed.mu.Unlock()
	safe.Go("follow a player", func() { f.followLoop(ctx, entity) })
}

// followLoop keeps the websocket follow up until ctx ends, connecting again after a drop.
func (f *Feature) followLoop(ctx context.Context, entity string) {
	wait := 2 * time.Second
	for ctx.Err() == nil {
		if hass.Get().Ready() {
			started := time.Now()
			if err := f.followOnce(ctx, entity); err != nil && ctx.Err() == nil {
				slog.Debug("home: following a player", "entity", entity, "err", err)
			}
			if time.Since(started) > time.Minute {
				wait = 2 * time.Second
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, time.Minute)
	}
}

func (f *Feature) followOnce(ctx context.Context, entity string) error {
	live, err := hass.Get().OpenLive(ctx)
	if err != nil {
		return err
	}
	defer live.Close()
	err = live.FollowEntities(ctx, []string{entity}, func(e hass.LiveEntity) {
		f.heard(entity, e)
	}, func(string) {
		f.heard(entity, hass.LiveEntity{ID: entity, State: "unavailable"})
	})
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-live.Done():
	}
	return nil
}

// heard takes a new state of the followed player. It runs on the websocket's reader, so the cover is
// fetched elsewhere.
func (f *Feature) heard(entity string, e hass.LiveEntity) {
	str := func(k string) string {
		s, _ := e.Attrs[k].(string)
		return strings.TrimSpace(s)
	}
	followed.mu.Lock()
	if followed.entity != entity {
		followed.mu.Unlock()
		return
	}
	followed.state = e.State
	switch e.State {
	case "playing":
		followed.played, followed.pausedAt = true, time.Time{}
		if followed.pauseEnd != nil {
			followed.pauseEnd.Stop()
			followed.pauseEnd = nil
		}
	case "paused":
		// The pause is timed from when it began: a reconnect that hears it paused again does not
		// start it over.
		if followed.played && followed.pausedAt.IsZero() {
			followed.pausedAt = time.Now()
			followed.pauseEnd = time.AfterFunc(followPausedFor+time.Second, func() { f.Changed.Emit(struct{}{}) })
		}
	}
	followed.name = str("friendly_name")
	followed.title, followed.artist, followed.album = str("media_title"), str("media_artist"), str("media_album_name")
	followed.pos, followed.posSet = followedPosition(followed.title, e)
	pic := str("entity_picture")
	if pic != followed.picture {
		followed.coverWait, followed.coverAt = 0, time.Time{} // a new cover is tried at once
	}
	// A cover not made yet is tried again on a later state, waiting longer each time it fails.
	fetch := pic != "" && pic != followed.artFor && pic != followed.fetching && time.Now().After(followed.coverAt)
	followed.picture = pic
	if pic == "" {
		followed.artFor, followed.art, followed.thumb = "", nil, nil
	}
	if fetch {
		followed.fetching = pic
	}
	followed.mu.Unlock()
	if fetch {
		safe.Go("followed cover", func() { fetchFollowedArt(entity, pic) })
	}
	f.Changed.Emit(struct{}{})
}

// followedPosition reads where the song is from a player's attributes: media_position seconds into it
// at media_position_updated_at, media_duration long, moving while the player is playing.
func followedPosition(title string, e hass.LiveEntity) (media.Position, bool) {
	pos, ok := e.Attrs["media_position"].(float64)
	if !ok {
		return media.Position{}, false
	}
	at := time.Now()
	if s, _ := e.Attrs["media_position_updated_at"].(string); s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil && !t.After(at) {
			at = t
		}
	}
	dur, _ := e.Attrs["media_duration"].(float64)
	rate := 0.0
	if e.State == "playing" {
		rate = 1
	}
	return media.Position{
		Title: title,
		At:    at,
		Pos:   time.Duration(pos * float64(time.Second)),
		Dur:   time.Duration(dur * float64(time.Second)),
		Rate:  rate,
	}, true
}

// fetchFollowedArt makes the page's picture from the cover Home Assistant named, unless another has
// been named since.
func fetchFollowedArt(entity, pic string) {
	var art, thumb *image.RGBA
	b, err := hass.Get().FetchURL(pic)
	if err == nil {
		art, thumb, err = layoutArt(b, false, "the followed player's cover")
	}
	followed.mu.Lock()
	if followed.fetching == pic {
		followed.fetching = ""
	}
	if followed.entity != entity || followed.picture != pic {
		// Another picture since: this one's result, and its failure, are not the new one's.
		followed.mu.Unlock()
		return
	}
	if err != nil {
		slog.Debug("home: the followed player's cover", "err", err)
		followed.coverWait = min(max(followed.coverWait*2, 10*time.Second), 5*time.Minute)
		followed.coverAt = time.Now().Add(followed.coverWait)
		followed.mu.Unlock()
		return
	}
	if followed.entity != entity || followed.picture != pic {
		followed.mu.Unlock()
		return
	}
	followed.artFor, followed.art, followed.thumb = pic, art, thumb
	followed.mu.Unlock()
	Get().Changed.Emit(struct{}{})
}

// followedKey names the track on the followed player, for Done.
func followedKey() string {
	return followed.entity + "\x00" + followed.title + "\x00" + followed.artist
}

// Following reports whether the page is showing the followed player: it is playing on a track, or
// paused for less than followPausedFor after playing, nothing of this device's own is on the page, and
// the track has not been put away with Done.
func Following() bool {
	if ownMusic() {
		return false
	}
	followed.mu.Lock()
	defer followed.mu.Unlock()
	return followingLocked()
}

func followingLocked() bool {
	if followed.entity == "" || (followed.state != "playing" && followed.state != "paused") {
		return false
	}
	if followed.title == "" && followed.artist == "" {
		return false // a player that is on with nothing named is nothing to show
	}
	if followed.state == "paused" && (!followed.played || time.Since(followed.pausedAt) >= followPausedFor) {
		return false
	}
	return followed.dismissed != followedKey()
}

// ownMusic is whether this device has music of its own for the page: its own stream, a receiver's, or a
// remote's, carried or held.
func ownMusic() bool {
	if playing, paused := media.Get().ScreenState(); playing || paused {
		return true
	}
	if from, _, _, _ := media.Get().ReceivedTrack(); Receiver(from) {
		return true
	}
	if _, _, _, ok := media.Get().Held(); ok {
		return true
	}
	return media.Get().Carried()
}

// withFollowed puts the followed player on the page when nothing of this device's is.
func withFollowed(r Radio) Radio {
	if ownMusic() {
		return r
	}
	followed.mu.Lock()
	defer followed.mu.Unlock()
	if !followingLocked() {
		return r
	}
	r.Followed = true
	r.Chosen = ""
	r.Playing, r.Paused = followed.state == "playing", followed.state == "paused"
	r.Now = followed.name
	if r.Now == "" {
		r.Now = followed.entity
	}
	r.Title, r.Artist, r.Album = followed.title, followed.artist, followed.album
	r.Art, r.Thumb = followed.art, followed.thumb
	r.Logo, r.Music = false, true
	return r
}

// FollowTransport sends a button on the page to the followed player.
func (f *Feature) FollowTransport(t media.Transport) {
	followed.mu.Lock()
	entity, playing := followed.entity, followed.state == "playing"
	followed.mu.Unlock()
	if entity == "" {
		return
	}
	var service string
	switch t {
	case media.TransportPlay:
		service = "media_play"
	case media.TransportPause:
		service = "media_pause"
	case media.TransportToggle:
		service = "media_play"
		if playing {
			service = "media_pause"
		}
	case media.TransportNext:
		service = "media_next_track"
	case media.TransportPrevious:
		service = "media_previous_track"
	case media.TransportStop:
		service = "media_stop"
	default:
		return
	}
	if err := hass.Get().Call("media_player", service, map[string]any{"entity_id": entity}); err != nil {
		slog.Warn("home: a button for the followed player", "entity", entity, "service", service, "err", err)
	}
}

// DismissFollowed puts the followed player's page away until its next track, without touching the
// player: it is somebody else's music.
func (f *Feature) DismissFollowed() {
	followed.mu.Lock()
	followed.dismissed = followedKey()
	followed.mu.Unlock()
	f.Changed.Emit(struct{}{})
}
