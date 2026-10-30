package home

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Following other players: Now Playing can show what Home Assistant's other media players are playing,
// a Sonos in the same room or the kitchen's speaker, with its cover, and its play, pause, back and
// forward buttons reach that player. They are ticked on the settings sheet, or one is chosen from the
// "Now Playing follows" select, and none is followed until then. With several, the page shows the one
// that started playing last, and when it stops, another still playing. Anything this device plays itself
// comes first: a followed player is only on the page while this device has nothing of its own on it.
//
// The players are followed over Home Assistant's websocket, which needs the address and token from the
// home_assistant action; the same token fetches their covers and sends the buttons.

// followNone is the option for following nothing, as the select and the screen say it.
const followNone = "None"

// followed are the followed players, in the order they were chosen, and what ends the current follow.
var followed struct {
	mu      sync.Mutex
	players []*player
	cancel  context.CancelFunc
}

// player is a followed player as last heard: its state, the parts of its attributes the page shows,
// and the cover for the picture it last named. Under followed.mu.
type player struct {
	entity  string
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

	// dismissed is the track the page was put away on with Done: a followed player is somebody
	// else's music, so Done does not stop it, it only puts it away until the next track.
	dismissed string

	// played is whether the player has been heard playing since this follow began, playedAt when it
	// last started, and pausedAt when it last went from playing to paused. Some players never stop: a
	// Sonos that is stopped stays paused on its last track for good. So a pause lets the page go after
	// followPausedFor, and a player first heard paused (a restart in the middle of one) is not shown
	// until it plays. pauseEnd redraws the page when the pause runs out.
	played   bool
	playedAt time.Time
	pausedAt time.Time
	pauseEnd *time.Timer

	// idlePause is whether the pause is a Music Assistant player's idle taken as one (heard).
	idlePause bool
}

// followedPlayer is the followed player for entity, or nil. Under followed.mu.
func followedPlayer(entity string) *player {
	for _, p := range followed.players {
		if p.entity == entity {
			return p
		}
	}
	return nil
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
	f.followOpts = component.NewOptions(f.followSel)
}

// optionsMax and optionBytes cap a select whose options come from Home Assistant's lists. All of a
// select's options go to Home Assistant in one message, and the ESPHome library cuts the length of a
// message over 64 KiB short without a word: Home Assistant then cannot decrypt the connection, drops
// it, and connects again at once, over and over, every entity of the device unknown (#136). No one
// picks from more than this many anyway.
const (
	optionsMax  = 100
	optionBytes = 8 << 10
)

// roomFor is whether opts has room for one more option, o.
func roomFor(opts []string, o string) bool {
	if len(opts) >= optionsMax {
		return false
	}
	n := len(o)
	for _, x := range opts {
		n += len(x)
	}
	return n <= optionBytes
}

// followOptions is what the select offers: none, the ones chosen, and every media player Home
// Assistant listed when it was last asked, this device's own left out.
func followOptions(h config.Home) []string {
	opts := []string{followNone}
	add := func(id string) {
		if id != "" && roomFor(opts, id) && !slices.Contains(opts, id) {
			opts = append(opts, id)
		}
	}
	for _, id := range h.Followed() {
		add(id) // first, so the cap never leaves them out
	}
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

// followOption is what the select shows: the first player chosen, as a select holds only one, or none.
func followOption(h config.Home) string {
	if ids := h.Followed(); len(ids) > 0 {
		return ids[0]
	}
	return followNone
}

// ChooseFollow follows one media player and no other, "" for none: the select's choice.
func (f *Feature) ChooseFollow(entity string) {
	var ids []string
	if entity != "" {
		ids = []string{entity}
	}
	f.setFollowed(ids)
}

// ToggleFollow starts or stops following one media player, keeping the others: a tick on the screen.
func (f *Feature) ToggleFollow(entity string) {
	if entity == "" {
		return
	}
	ids := config.Get().Home.Followed()
	if i := slices.Index(ids, entity); i >= 0 {
		ids = slices.Delete(ids, i, i+1)
	} else {
		ids = append(ids, entity)
	}
	f.setFollowed(ids)
}

func (f *Feature) setFollowed(ids []string) {
	if err := config.Set().Home().FollowPlayers(ids); err != nil {
		slog.Warn("home: saving the followed players failed", "err", err)
		return
	}
	slog.Info("home: now playing follows", "entities", ids)
	f.followOpts.Set(followOptions(config.Get().Home))
	f.followSel.Set(followOption(config.Get().Home))
	f.restartFollow()
	f.Changed.Emit(struct{}{})
}

// FollowChoices is what the screen's list offers, as the select does: each player's entity ("" for
// none), its name, and whether it is in force.
func (f *Feature) FollowChoices() (entities, names []string, on []bool) {
	chosen := config.Get().Home.Followed()
	f.mu.Lock()
	known := append([]hass.Entity(nil), f.players...)
	f.mu.Unlock()
	for _, o := range f.followOpts.Get() {
		e := followEntity(o)
		entities = append(entities, e)
		names = append(names, playerName(e, known))
		on = append(on, (e == "" && len(chosen) == 0) || (e != "" && slices.Contains(chosen, e)))
	}
	return entities, names, on
}

// FollowSource is the followed players as the sheet says them: none, the one, or how many.
func (f *Feature) FollowSource() string {
	ids := config.Get().Home.Followed()
	if len(ids) > 1 {
		return fmt.Sprintf("%d players", len(ids))
	}
	if len(ids) == 0 {
		return followNone
	}
	f.mu.Lock()
	known := append([]hass.Entity(nil), f.players...)
	f.mu.Unlock()
	return playerName(ids[0], known)
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
	if !f.followOpts.Set(followOptions(config.Get().Home)) {
		return
	}
	// New options reach Home Assistant at the next connection.
	f.rewire()
}

// restartFollow follows the players chosen now, if any. A player still chosen keeps what was heard of it
// (its pause, its Done, when it started), so ticking another does not move the page; the follow is
// started again only when the players are not the same ones.
func (f *Feature) restartFollow() {
	ids := config.Get().Home.Followed()
	followed.mu.Lock()
	same := followed.cancel != nil && len(ids) == len(followed.players)
	var kept []*player
	for _, id := range ids {
		p := followedPlayer(id)
		if p == nil {
			p, same = &player{entity: id}, false
		}
		kept = append(kept, p)
	}
	for _, p := range followed.players {
		if !slices.Contains(kept, p) && p.pauseEnd != nil {
			p.pauseEnd.Stop()
		}
	}
	followed.players = kept
	if same {
		followed.mu.Unlock()
		return
	}
	if followed.cancel != nil {
		followed.cancel()
		followed.cancel = nil
	}
	if len(ids) == 0 {
		followed.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	followed.cancel = cancel
	followed.mu.Unlock()
	safe.Go("follow players", func() { f.followLoop(ctx, ids) })
}

// followLoop keeps the websocket follow up until ctx ends, connecting again after a drop.
func (f *Feature) followLoop(ctx context.Context, ids []string) {
	wait := 2 * time.Second
	for ctx.Err() == nil {
		if hass.Get().Ready() {
			started := time.Now()
			if err := f.followOnce(ctx, ids); err != nil && ctx.Err() == nil {
				slog.Debug("home: following players", "entities", ids, "err", err)
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

func (f *Feature) followOnce(ctx context.Context, ids []string) error {
	live, err := hass.Get().OpenLive(ctx)
	if err != nil {
		return err
	}
	defer live.Close()
	err = live.FollowEntities(ctx, ids, func(e hass.LiveEntity) {
		f.heard(e.ID, e)
	}, func(id string) {
		f.heard(id, hass.LiveEntity{ID: id, State: "unavailable"})
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

// heard takes a new state of a followed player. It runs on the websocket's reader, so the cover is
// fetched elsewhere.
func (f *Feature) heard(entity string, e hass.LiveEntity) {
	str := func(k string) string {
		s, _ := e.Attrs[k].(string)
		return strings.TrimSpace(s)
	}
	followed.mu.Lock()
	p := followedPlayer(entity)
	if p == nil {
		followed.mu.Unlock()
		return // a late state from the last follow
	}
	state := e.State
	// Music Assistant pauses some players (a Sonos) by stopping them where they are: the player goes
	// idle with its track still named, and a play picks it up there (#345). An idle straight from
	// playing is taken as that pause; one after a pause is the music over (the queue emptied, #150), and
	// so is one with the song played to its end: the last of the queue finished.
	idlePause := state == "idle" && str("app_id") == "music_assistant" && str("media_title") != "" &&
		(p.state == "playing" || p.idlePause) && !playedToTheEnd(e)
	if idlePause {
		state = "paused"
	}
	p.idlePause = idlePause
	switch state {
	case "playing":
		if p.state != "playing" {
			p.playedAt = time.Now()
		}
		p.played, p.pausedAt = true, time.Time{}
		if p.pauseEnd != nil {
			p.pauseEnd.Stop()
			p.pauseEnd = nil
		}
	case "paused":
		// The pause is timed from when it began: a reconnect that hears it paused again does not
		// start it over.
		if p.played && p.pausedAt.IsZero() {
			p.pausedAt = time.Now()
			p.pauseEnd = time.AfterFunc(followPausedFor+time.Second, func() { f.Changed.Emit(struct{}{}) })
		}
	}
	p.state = state
	p.name = str("friendly_name")
	p.title, p.artist, p.album = str("media_title"), str("media_artist"), str("media_album_name")
	p.pos, p.posSet = followedPosition(p.title, e)
	pic := str("entity_picture")
	if pic != p.picture {
		p.coverWait, p.coverAt = 0, time.Time{} // a new cover is tried at once
	}
	// A cover not made yet is tried again on a later state, waiting longer each time it fails.
	fetch := pic != "" && pic != p.artFor && pic != p.fetching && time.Now().After(p.coverAt)
	p.picture = pic
	if pic == "" {
		p.artFor, p.art, p.thumb = "", nil, nil
	}
	if fetch {
		p.fetching = pic
	}
	followed.mu.Unlock()
	if fetch {
		safe.Go("followed cover", func() { fetchFollowedArt(entity, pic) })
	}
	f.Changed.Emit(struct{}{})
}

// playedToTheEnd is whether a player's song is at its end, by media_position and media_duration: within
// a few seconds of it, for a position taken a moment before the last of it played.
func playedToTheEnd(e hass.LiveEntity) bool {
	pos, ok := e.Attrs["media_position"].(float64)
	dur, _ := e.Attrs["media_duration"].(float64)
	return ok && dur > 0 && pos >= dur-3
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
	p := followedPlayer(entity)
	if p == nil {
		followed.mu.Unlock()
		return // no longer followed
	}
	if p.fetching == pic {
		p.fetching = ""
	}
	if p.picture != pic {
		// Another picture since: this one's result, and its failure, are not the new one's.
		followed.mu.Unlock()
		return
	}
	if err != nil {
		slog.Debug("home: the followed player's cover", "err", err)
		p.coverWait = min(max(p.coverWait*2, 10*time.Second), 5*time.Minute)
		p.coverAt = time.Now().Add(p.coverWait)
		followed.mu.Unlock()
		return
	}
	p.artFor, p.art, p.thumb = pic, art, thumb
	followed.mu.Unlock()
	Get().Changed.Emit(struct{}{})
}

// key names the track on the player, for Done.
func (p *player) key() string {
	return p.entity + "\x00" + p.title + "\x00" + p.artist
}

// showable is whether p has something for the page: it is playing on a track, or paused for less than
// followPausedFor after playing, and the track has not been put away with Done. Under followed.mu.
func (p *player) showable() bool {
	if p.state != "playing" && p.state != "paused" {
		return false
	}
	if p.title == "" && p.artist == "" {
		return false // a player that is on with nothing named is nothing to show
	}
	if p.state == "paused" && (!p.played || time.Since(p.pausedAt) >= followPausedFor) {
		return false
	}
	return p.dismissed != p.key()
}

// shownLocked is the followed player for the page, or nil: of those with something to show, the one
// playing that started last, or when none is playing, the one paused last; the first chosen on a tie.
// Under followed.mu.
func shownLocked() *player {
	var best *player
	for _, p := range followed.players {
		if !p.showable() {
			continue
		}
		switch {
		case best == nil:
			best = p
		case p.state == "playing" && best.state != "playing":
			best = p
		case p.state == "playing" && best.state == "playing" && p.playedAt.After(best.playedAt):
			best = p
		case p.state == "paused" && best.state == "paused" && p.pausedAt.After(best.pausedAt):
			best = p
		}
	}
	return best
}

// shownFollowed is the entity of the followed player on the page, or "".
func shownFollowed() string {
	followed.mu.Lock()
	defer followed.mu.Unlock()
	if p := shownLocked(); p != nil {
		return p.entity
	}
	return ""
}

// Following reports whether the page is showing a followed player: one has something to show
// (showable) and nothing of this device's own is on the page.
func Following() bool {
	if ownMusic() {
		return false
	}
	followed.mu.Lock()
	defer followed.mu.Unlock()
	return shownLocked() != nil
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

// withFollowed puts a followed player on the page when nothing of this device's is.
func withFollowed(r Radio) Radio {
	if ownMusic() {
		return r
	}
	followed.mu.Lock()
	defer followed.mu.Unlock()
	p := shownLocked()
	if p == nil {
		return r
	}
	r.Followed = true
	r.Chosen = ""
	r.Playing, r.Paused = p.state == "playing", p.state == "paused"
	r.Now = p.name
	if r.Now == "" {
		r.Now = p.entity
	}
	r.Title, r.Artist, r.Album = p.title, p.artist, p.album
	r.Art, r.Thumb = p.art, p.thumb
	r.Logo, r.Music = false, true
	return r
}

// followedPos is where the song on the page's followed player is, when the player says.
func followedPos() (media.Position, bool) {
	followed.mu.Lock()
	defer followed.mu.Unlock()
	if p := shownLocked(); p != nil && p.posSet {
		return p.pos, true
	}
	return media.Position{}, false
}

// FollowTransport sends a button on the page to the followed player on it.
func (f *Feature) FollowTransport(t media.Transport) {
	followed.mu.Lock()
	var entity string
	var playing bool
	if p := shownLocked(); p != nil {
		entity, playing = p.entity, p.state == "playing"
	}
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

// DismissFollowed puts the followed player on the page away until its next track, without touching the
// player: it is somebody else's music. Another followed player with something to show takes the page.
func (f *Feature) DismissFollowed() {
	followed.mu.Lock()
	if p := shownLocked(); p != nil {
		p.dismissed = p.key()
	}
	followed.mu.Unlock()
	f.Changed.Emit(struct{}{})
}
