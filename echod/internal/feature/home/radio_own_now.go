package home

import (
	"context"
	"image"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/radiometa"
)

// A station of one's own is on neither iHeart nor TuneIn, so nothing there can say what it plays. The
// radio's "now" entity can: a template sensor whose state names the station and whose attributes carry
// the song, the way a media player's do. Fed from the station's own now-playing address, or its ICY
// titles, it keeps the song and the cover up to date for as long as the station plays (#364).

// nowAttributes are the attributes of the "now" entity read for the song, named as a media player's.
var nowAttributes = []string{"media_title", "media_artist", "media_album_name", "entity_picture"}

// entityNow is the song the "now" entity carries for station. ok is false when the entity names some
// other station, so attributes left from the station before are not taken for this one, or when it
// carries no song at all.
func entityNow(station string) (n radiometa.Now, ok bool) {
	entity := config.Get().Home.Radio.Now
	if entity == "" || station == "" {
		return n, false
	}
	t := hastate.Get()
	if !strings.EqualFold(strings.TrimSpace(t.State(entity)), strings.TrimSpace(station)) {
		return n, false
	}
	attr := func(name string) string {
		v, _ := t.Value(entity, name)
		v = strings.TrimSpace(v)
		if v == "None" || v == "unknown" || v == "unavailable" {
			return "" // what Home Assistant sends for an attribute set to nothing
		}
		return v
	}
	n = radiometa.Now{Title: attr("media_title"), Artist: attr("media_artist"), Album: attr("media_album_name"),
		Art: attr("entity_picture")}
	return n, n != radiometa.Now{}
}

// entityNames is whether the "now" entity names station as the one playing.
func entityNames(station string) bool {
	entity := config.Get().Home.Radio.Now
	return entity != "" && station != "" &&
		strings.EqualFold(strings.TrimSpace(hastate.Get().State(entity)), strings.TrimSpace(station))
}

// fetchEntityArt fetches the "now" entity's picture: a path on Home Assistant (with the token, as a
// media player's entity_picture is) or an address anywhere. A cover, so it fills the panel.
func fetchEntityArt(ctx context.Context, u string) (*image.RGBA, *image.RGBA, error) {
	if u == "" {
		return nil, nil, nil
	}
	b, err := hass.Get().FetchURLContext(ctx, u)
	if err != nil {
		return nil, nil, err
	}
	return layoutArt(b, false, "the radio's now entity's picture")
}

// nowChanged is a value arriving for the "now" entity: a new song is looked at at once, not at the
// next poll.
func (f *Feature) nowChanged(u hastate.Update) {
	if u.Entity != "" && u.Entity == config.Get().Home.Radio.Now {
		f.pokeMeta()
	}
}
