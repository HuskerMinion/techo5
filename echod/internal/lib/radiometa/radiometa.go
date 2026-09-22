// Package radiometa finds what a radio station is playing, and what it looks like, from the
// services the stations come from: iHeartRadio and TuneIn. Station names on the screen's list
// carry the service ("101.1 WXYZ on iHeartRadio", "1450 WKRP on TuneIn"); a bare name is tried on
// both. Nothing here is a documented API: these are the endpoints the services' own apps use,
// read with the same shapes they return today, and every failure is just "no information".
package radiometa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Station is a resolved station: where its live data comes from, and its logo.
type Station struct {
	Name    string // as the list has it
	Service string // "iHeartRadio" or "TuneIn"
	ID      string // the service's own id
	Logo    string // URL, may be empty
	Music   bool   // a music station rather than talk, as far as the service says
}

// Now is what a station is playing.
type Now struct {
	Title, Artist, Album string
	Art                  string // URL of the cover, may be empty
}

var client = &http.Client{Timeout: 8 * time.Second}

// errNothing is a service saying there is nothing on: talk, adverts, between songs.
var errNothing = errors.New("radiometa: nothing on")

var (
	mu       sync.Mutex
	resolved = map[string]Station{} // by list name; a failed lookup is not cached
)

// service splits "name on Service" into the two.
func service(name string) (query, svc string) {
	for _, s := range []string{"iHeartRadio", "TuneIn"} {
		if i := strings.LastIndex(strings.ToLower(name), " on "+strings.ToLower(s)); i > 0 {
			return strings.TrimSpace(name[:i]), s
		}
	}
	return strings.TrimSpace(name), ""
}

// Resolve finds the station behind a list name.
func Resolve(ctx context.Context, name string) (Station, error) {
	mu.Lock()
	st, ok := resolved[name]
	mu.Unlock()
	if ok {
		return st, nil
	}
	query, svc := service(name)
	var err error
	switch svc {
	case "iHeartRadio":
		st, err = iheartFind(ctx, query)
	case "TuneIn":
		st, err = tuneinFind(ctx, query)
	default:
		if st, err = iheartFind(ctx, query); err != nil {
			st, err = tuneinFind(ctx, query)
		}
	}
	if err != nil {
		return Station{}, err
	}
	st.Name = name
	mu.Lock()
	resolved[name] = st
	mu.Unlock()
	return st, nil
}

// Playing is what the station has on now. A talk station, or a music one between songs, gives
// an empty Now and no error.
func Playing(ctx context.Context, st Station) (Now, error) {
	switch st.Service {
	case "iHeartRadio":
		return iheartNow(ctx, st.ID)
	case "TuneIn":
		return tuneinNow(ctx, st.ID)
	}
	return Now{}, errors.New("radiometa: unknown service")
}

func get(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "TECHO5/1.0")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNoContent {
		return errNothing
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("radiometa: %s: %s", u, res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, maxReply)).Decode(into)
}

// maxReply is how much of a service's answer is decoded. A search result or a now-playing record is a
// few kilobytes; a megabyte is far past anything these have ever returned. Neither service is ours and
// neither promises anything, so the bound is what keeps a station's metadata host — or whatever has
// taken its name today — from feeding a device with half a gigabyte until it dies. A reply cut off
// here fails to parse, which is the same "no information" every other failure here comes to.
const maxReply = 1 << 20

var word = regexp.MustCompile(`[A-Za-z0-9.]+`)

// score counts how many words of the query a candidate carries, call letters weighing most:
// "98.7 WZZZ" must pick WZZZ over another 97.3.
func score(query, candidate string) int {
	c := strings.ToLower(candidate)
	n := 0
	for _, w := range word.FindAllString(strings.ToLower(query), -1) {
		if !strings.Contains(c, w) {
			continue
		}
		if len(w) >= 3 && strings.Trim(w, "0123456789.") == w {
			n += 3 // letters: call sign or a name
		} else {
			n++
		}
	}
	return n
}

// ---- iHeartRadio ----

func iheartFind(ctx context.Context, query string) (Station, error) {
	var out struct {
		Results struct {
			Stations []struct {
				ID          json.Number `json:"id"`
				Name        string      `json:"name"`
				CallLetters string      `json:"callLetters"`
				Description string      `json:"description"`
				ImageURL    string      `json:"imageUrl"`
			} `json:"stations"`
		} `json:"results"`
	}
	u := "https://api.iheart.com/api/v3/search/all?countryCode=US&stations=true&albums=false&artists=false&tracks=false&bundles=false&playlists=false&podcasts=false&limit=8&keywords=" + url.QueryEscape(query)
	if err := get(ctx, u, &out); err != nil {
		return Station{}, err
	}
	best, bestScore := -1, 0
	for i, s := range out.Results.Stations {
		if sc := score(query, s.Name+" "+s.CallLetters); sc > bestScore {
			best, bestScore = i, sc
		}
	}
	if best < 0 {
		return Station{}, fmt.Errorf("radiometa: iHeartRadio knows no %q", query)
	}
	s := out.Results.Stations[best]
	d := strings.ToLower(s.Description + " " + s.Name)
	music := !(strings.Contains(d, "news") || strings.Contains(d, "talk") || strings.Contains(d, "sports"))
	return Station{Service: "iHeartRadio", ID: s.ID.String(), Logo: s.ImageURL, Music: music}, nil
}

type iheartTrack struct {
	Code          int     `json:"code"`
	Title         string  `json:"title"`
	Artist        string  `json:"artist"`
	Album         string  `json:"album"`
	ImagePath     string  `json:"imagePath"`
	TrackDuration float64 `json:"trackDuration"`
	StartTime     float64 `json:"startTime"` // unix seconds
}

func (t iheartTrack) now() Now {
	return Now{Title: t.Title, Artist: t.Artist, Album: t.Album, Art: t.ImagePath}
}

func iheartNow(ctx context.Context, id string) (Now, error) {
	var out iheartTrack
	u := "https://us.api.iheart.com/api/v3/live-meta/stream/" + id + "/currentTrackMeta?defaultMetadata=true"
	err := get(ctx, u, &out)
	switch {
	case err == nil && out.Code == 0 && out.Title != "":
		return out.now(), nil
	case err != nil && !errors.Is(err, errNothing) && !strings.Contains(err.Error(), "410"):
		return Now{}, err
	}
	// "Nothing on" from the live endpoint also happens mid-song for a while; the history knows
	// what started last, and if that song is still within its own length it is the one playing.
	var hist struct {
		Data []iheartTrack `json:"data"`
	}
	if err := get(ctx, "https://us.api.iheart.com/api/v3/live-meta/stream/"+id+"/trackHistory?limit=1", &hist); err != nil || len(hist.Data) == 0 {
		return Now{}, nil
	}
	t := hist.Data[0]
	age := float64(time.Now().Unix()) - t.StartTime
	if t.Title == "" || age < 0 || age > t.TrackDuration+90 {
		return Now{}, nil
	}
	return t.now(), nil
}

// ---- TuneIn ----

type tuneinOutline struct {
	Element      string `json:"element"`
	Type         string `json:"type"`
	Item         string `json:"item"`
	Text         string `json:"text"`
	GuideID      string `json:"guide_id"`
	Image        string `json:"image"`
	CurrentTrack string `json:"current_track"`
	Subtext      string `json:"subtext"`
}

func tuneinFind(ctx context.Context, query string) (Station, error) {
	var out struct {
		Body []tuneinOutline `json:"body"`
	}
	u := "https://opml.radiotime.com/Search.ashx?render=json&query=" + url.QueryEscape(query)
	if err := get(ctx, u, &out); err != nil {
		return Station{}, err
	}
	best, bestScore := -1, 0
	for i, o := range out.Body {
		if o.Item != "station" {
			continue
		}
		if sc := score(query, o.Text); sc > bestScore {
			best, bestScore = i, sc
		}
	}
	if best < 0 {
		return Station{}, fmt.Errorf("radiometa: TuneIn knows no %q", query)
	}
	o := out.Body[best]
	// The larger logo is the same path with a different size letter.
	logo := strings.Replace(o.Image, "q.png", "g.png", 1)
	logo = strings.Replace(logo, "logoq.", "logog.", 1)
	var desc struct {
		Body []struct {
			IsMusic bool   `json:"is_music"`
			Logo    string `json:"logo"`
		} `json:"body"`
	}
	music := false
	if err := get(ctx, "https://opml.radiotime.com/Describe.ashx?render=json&id="+o.GuideID, &desc); err == nil && len(desc.Body) > 0 {
		music = desc.Body[0].IsMusic
		if logo == "" {
			logo = desc.Body[0].Logo
		}
	}
	return Station{Service: "TuneIn", ID: o.GuideID, Logo: logo, Music: music}, nil
}

func tuneinNow(ctx context.Context, id string) (Now, error) {
	var out struct {
		Body []struct {
			CurrentSong     string `json:"current_song"`
			CurrentArtist   string `json:"current_artist"`
			CurrentAlbum    string `json:"current_album"`
			CurrentAlbumArt string `json:"current_album_art"`
		} `json:"body"`
	}
	if err := get(ctx, "https://opml.radiotime.com/Describe.ashx?render=json&id="+id, &out); err != nil {
		return Now{}, err
	}
	if len(out.Body) == 0 || out.Body[0].CurrentSong == "" {
		return Now{}, nil
	}
	b := out.Body[0]
	return Now{Title: b.CurrentSong, Artist: b.CurrentArtist, Album: b.CurrentAlbum, Art: b.CurrentAlbumArt}, nil
}
