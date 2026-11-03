package home

import (
	"bytes"
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

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
)

// Photos straight from an Immich server (config.Home.Immich), for a slideshow with no Home Assistant
// in between (#134), as the weather can come from Open-Meteo and calendars from an iCal link. Immich is
// a source like the device's own photos: its ids are ImmichAll, or ImmichAlbumPrefix and an album's
// id, and each photo's is immichAssetPrefix and the photo's id; the slideshow fetches, fits and shows
// them as it does Home Assistant's.
//
// The API key opens the owner's whole library, so it goes only in the request header: never in a URL,
// a log line, an error or the setup page. Errors name the server's host at most. Requests go through
// netguard, as everything fetched for an address somebody typed does, with no proxy from the
// environment; they follow no redirect, which would carry the key's header to wherever it pointed; and
// every reply is read up to a cap.

const (
	// ImmichAll is the whole library as a slideshow source.
	ImmichAll = "immich:all"
	// ImmichAlbumPrefix begins an album's source id; the album's id follows.
	ImmichAlbumPrefix = "immich:album/"
	// immichAssetPrefix begins a photo's id in the slideshow's list.
	immichAssetPrefix = "immich:asset/"
)

const (
	// immichListMax caps a reply that lists albums or a page of photos: a page of a thousand photos is
	// a few megabytes of JSON.
	immichListMax = 8 << 20
	// immichPhotoMax caps one photo. Immich's preview is a JPEG of about 1440 pixels on its long side,
	// a few hundred kilobytes.
	immichPhotoMax = 16 << 20
	// immichPage is how many photos one search asks for, Immich's most.
	immichPage = 1000
)

// immichID is an id as Immich gives them: a UUID. Anything else is refused before it goes into a path.
var immichID = regexp.MustCompile(`^[0-9a-fA-F-]{1,64}$`)

// immichClient is the client every request to Immich goes through; a variable for the tests, whose
// fake server is on the loopback netguard refuses.
var immichClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		Proxy:                  nil,
		DialContext:            netguard.Dialer(10 * time.Second).DialContext,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  20 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		IdleConnTimeout:        30 * time.Second,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// ErrImmichKey is Immich refusing the API key.
var ErrImmichKey = errors.New("immich: the server refused the API key")

// ImmichAlbum is one album on the server.
type ImmichAlbum struct {
	ID     string `json:"id"`
	Name   string `json:"albumName"`
	Photos int    `json:"assetCount"`
}

// immichAsset is one photo or video, as a search lists it.
type immichAsset struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// CheckImmichServer says whether an address is one to keep: http or https, a host, no login, query or
// fragment in it. It returns the address as it is kept.
func CheckImmichServer(addr string) (string, error) {
	addr = strings.TrimRight(strings.TrimSpace(addr), "/")
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("the address starts with http:// or https://, like http://192.168.1.20:2283")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(addr, " \r\n") {
		return "", errors.New("just the server's address, with no login or anything after a ? or #")
	}
	return addr, nil
}

// immichDo makes one request to Immich and reads its reply, up to max bytes. path is the API's own (it
// starts with /api/); body, when not nil, is sent as JSON.
func immichDo(ctx context.Context, srv config.Immich, method, path string, body any, max int64) ([]byte, error) {
	base, err := url.Parse(srv.Server)
	if err != nil || base.Host == "" {
		return nil, errors.New("immich: no server address")
	}
	var in io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		in = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(srv.Server, "/")+path, in)
	if err != nil {
		return nil, fmt.Errorf("immich at %s: the request could not be made", base.Host)
	}
	req.Header.Set("x-api-key", srv.Key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := immichClient.Do(req)
	if err != nil {
		// Not err itself: a url.Error carries the whole address, and the host is enough to say.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || isTimeout(err) {
			return nil, fmt.Errorf("immich at %s did not answer in time", base.Host)
		}
		return nil, fmt.Errorf("immich at %s could not be reached", base.Host)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrImmichKey
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("immich at %s: %w", base.Host, errImmichGone)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("immich at %s answered %d", base.Host, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("immich at %s: the reply did not arrive whole", base.Host)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("immich at %s: the reply was larger than %d bytes", base.Host, max)
	}
	return b, nil
}

// isTimeout is whether a request failed for taking too long.
func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

// ImmichAlbums lists the server's albums, to choose one from. It is also how a server and key are
// checked before they are kept.
func ImmichAlbums(ctx context.Context, srv config.Immich) ([]ImmichAlbum, error) {
	b, err := immichDo(ctx, srv, http.MethodGet, "/api/albums", nil, immichListMax)
	if err != nil {
		return nil, err
	}
	var all []ImmichAlbum
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, errors.New("immich: the album list was not what Immich sends")
	}
	var out []ImmichAlbum
	for _, a := range all {
		if immichID.MatchString(a.ID) {
			out = append(out, a)
		}
	}
	immichNames.remember(out)
	return out, nil
}

// immichFilter narrows what a source shows: the slideshow's Immich options (config.Slideshow).
type immichFilter struct {
	favorites bool
	from, to  string // days, config.PhotoDay; either may be empty
	person    string // a name, as Immich knows the person
}

// immichFilterOf is the filter the slideshow's settings ask for.
func immichFilterOf(h config.Slideshow) immichFilter {
	return immichFilter{favorites: h.ImmichFavorites, from: h.ImmichFrom, to: h.ImmichTo, person: strings.TrimSpace(h.ImmichPerson)}
}

// ask adds the filter to a search. The days are whole days on this device's clock, from the first
// moment of from to the last of to: Immich matches them to the moment each photo was taken.
func (fl immichFilter) ask(ctx context.Context, srv config.Immich, query map[string]any) error {
	if fl.favorites {
		query["isFavorite"] = true
	}
	if fl.from != "" {
		d, err := time.ParseInLocation(config.PhotoDay, fl.from, time.Local)
		if err != nil {
			return errors.New("immich: the first day to show is not a date")
		}
		query["takenAfter"] = d.UTC().Format(immichTime)
	}
	if fl.to != "" {
		d, err := time.ParseInLocation(config.PhotoDay, fl.to, time.Local)
		if err != nil {
			return errors.New("immich: the last day to show is not a date")
		}
		query["takenBefore"] = d.AddDate(0, 0, 1).Add(-time.Millisecond).UTC().Format(immichTime)
	}
	if fl.person != "" {
		id, err := immichPersonID(ctx, srv, fl.person)
		if err != nil {
			return err
		}
		query["personIds"] = []string{id}
	}
	return nil
}

// immichTime is a moment as Immich's search takes one.
const immichTime = "2006-01-02T15:04:05.000Z"

// immichPeoplePages bounds how many pages of people one look asks for.
const immichPeoplePages = 10

// immichPersonID is the id of the person Immich knows by name, whatever its case. Not known is an error
// that says so: a slideshow of nobody would only look empty.
func immichPersonID(ctx context.Context, srv config.Immich, name string) (string, error) {
	for page := 1; page <= immichPeoplePages; page++ {
		b, err := immichDo(ctx, srv, http.MethodGet, fmt.Sprintf("/api/people?size=%d&page=%d", immichPage, page), nil, immichListMax)
		if err != nil {
			return "", err
		}
		var r struct {
			People []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"people"`
			HasNextPage bool `json:"hasNextPage"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return "", errors.New("immich: the list of people was not what Immich sends")
		}
		for _, p := range r.People {
			if strings.EqualFold(strings.TrimSpace(p.Name), name) && immichID.MatchString(p.ID) {
				return p.ID, nil
			}
		}
		if !r.HasNextPage || len(r.People) == 0 {
			break
		}
	}
	return "", fmt.Errorf("immich: nobody called %q among the people it knows", name)
}

// immichPhotos lists the photos of a source, ImmichAll or an album, as narrowed by fl, page by page up
// to max. Videos are left out; every photo is shown as Immich's JPEG preview, whatever it was taken as.
func immichPhotos(ctx context.Context, srv config.Immich, source string, fl immichFilter, max int) ([]hass.Media, error) {
	// Timeline only, so archived photos stay out: Immich's search asks for no visibility of its own,
	// and with none it answers with the archive as well as the timeline (what it leaves out is only
	// what is locked).
	query := map[string]any{"type": "IMAGE", "size": immichPage, "visibility": "timeline"}
	if id, ok := strings.CutPrefix(source, ImmichAlbumPrefix); ok {
		if !immichID.MatchString(id) {
			return nil, errors.New("immich: that is not an album")
		}
		query["albumIds"] = []string{id}
	} else if source != ImmichAll {
		return nil, errors.New("immich: that is not an Immich source")
	}
	if err := fl.ask(ctx, srv, query); err != nil {
		return nil, err
	}
	var out []hass.Media
	// A page that adds nothing ends it, and so do pages enough for max and a few over: a server that
	// kept saying there was another page of nothing would otherwise be asked until the gathering's time
	// ran out.
	pages := max/immichPage + 5
	for page := 1; len(out) < max && page <= pages; page++ {
		query["page"] = page
		b, err := immichDo(ctx, srv, http.MethodPost, "/api/search/metadata", query, immichListMax)
		if errors.Is(err, errImmichGone) {
			return nil, fmt.Errorf("%w (an album since deleted?)", err)
		}
		if err != nil {
			return nil, err
		}
		var r struct {
			Assets struct {
				Items    []immichAsset `json:"items"`
				NextPage *string       `json:"nextPage"`
			} `json:"assets"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, errors.New("immich: the photo list was not what Immich sends")
		}
		before := len(out)
		for _, a := range r.Assets.Items {
			if a.Type == "IMAGE" && immichID.MatchString(a.ID) && len(out) < max {
				out = append(out, hass.Media{ID: immichAssetPrefix + a.ID, Kind: "image/jpeg", CanPlay: true})
			}
		}
		if r.Assets.NextPage == nil || *r.Assets.NextPage == "" || len(r.Assets.Items) == 0 || len(out) == before {
			break
		}
	}
	return out, nil
}

// immichTaken is when and where a photo was taken, as Immich has it: when as the clock there said it,
// and the place as a person names it, "Springfield, Ohio". Either may be missing.
type immichTaken struct {
	When  time.Time
	Place string
}

// immichAbout asks Immich when and where one photo was taken.
func immichAbout(ctx context.Context, srv config.Immich, id string) (immichTaken, error) {
	asset := strings.TrimPrefix(id, immichAssetPrefix)
	if !immichID.MatchString(asset) {
		return immichTaken{}, errors.New("immich: that is not a photo")
	}
	b, err := immichDo(ctx, srv, http.MethodGet, "/api/assets/"+asset, nil, immichListMax)
	if err != nil {
		return immichTaken{}, err
	}
	var r struct {
		LocalDateTime string `json:"localDateTime"`
		Exif          struct {
			Taken   string `json:"dateTimeOriginal"`
			City    string `json:"city"`
			State   string `json:"state"`
			Country string `json:"country"`
		} `json:"exifInfo"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return immichTaken{}, errors.New("immich: the photo's details were not what Immich sends")
	}
	var t immichTaken
	// localDateTime is the clock where it was taken, written as if it were UTC; its fields are the ones
	// to show. dateTimeOriginal is the moment, in UTC, when there is no localDateTime.
	for _, s := range []string{r.LocalDateTime, r.Exif.Taken} {
		if w, err := time.Parse(time.RFC3339, s); err == nil {
			t.When = w.UTC()
			break
		}
	}
	t.Place = placeName(r.Exif.City, r.Exif.State, r.Exif.Country)
	return t, nil
}

// placeName is a place as a caption names it: the city and its state, or the city and its country
// where there is no state; whichever of them there is, alone.
func placeName(city, state, country string) string {
	var parts []string
	for _, p := range []string{city, state} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if c := strings.TrimSpace(country); c != "" && len(parts) < 2 {
		parts = append(parts, c)
	}
	return strings.Join(parts, ", ")
}

// errImmichGone is Immich answering that what was asked for is not there.
var errImmichGone = errors.New("not found")

// immichPhoto fetches one photo by its slideshow id: Immich's preview of it.
func immichPhoto(ctx context.Context, srv config.Immich, id string) ([]byte, error) {
	asset := strings.TrimPrefix(id, immichAssetPrefix)
	if !immichID.MatchString(asset) {
		return nil, errors.New("immich: that is not a photo")
	}
	b, err := immichDo(ctx, srv, http.MethodGet, "/api/assets/"+asset+"/thumbnail?size=preview", nil, immichPhotoMax)
	if errors.Is(err, errImmichGone) {
		// Immich makes a preview of each photo after it finds it, which for a library just added takes
		// hours: until then there is none to show, and the slideshow goes on to the next photo.
		return nil, fmt.Errorf("%w (no preview yet, or the photo was removed)", err)
	}
	return b, err
}

// immichNames keeps the albums' names as last listed, to name the one chosen.
var immichNames = &albumNames{}

type albumNames struct {
	mu    sync.Mutex
	names map[string]string
}

func (n *albumNames) remember(albums []ImmichAlbum) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.names = map[string]string{}
	for _, a := range albums {
		n.names[a.ID] = a.Name
	}
}

func (n *albumNames) name(id string) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, ok := n.names[id]
	return s, ok
}

// IsImmich is whether a slideshow source or photo id is Immich's.
func IsImmich(id string) bool { return strings.HasPrefix(id, "immich:") }

// ImmichSourceName names an Immich source for the screen and Home Assistant: the album's name, or
// Immich for the whole library.
func ImmichSourceName(source string) string {
	id, ok := strings.CutPrefix(source, ImmichAlbumPrefix)
	if !ok {
		return "Immich"
	}
	if name, ok := immichNames.name(id); ok && name != "" {
		return name
	}
	if i := config.Get().Home.Immich; i.Album != "" && i.AlbumID == id {
		return i.Album
	}
	return "Immich album"
}

// ImmichSourceOK is whether source is an Immich source the device can ask for: the whole library, or
// an album by an id of the form Immich gives.
func ImmichSourceOK(source string) bool {
	if source == ImmichAll {
		return true
	}
	id, ok := strings.CutPrefix(source, ImmichAlbumPrefix)
	return ok && immichID.MatchString(id)
}

// browseImmich is the Immich folder of the screen's photo folder list (ImmichAll, the whole library,
// when it is used): the albums, each a folder to open and use. An album lists nothing under it when
// opened; its photos are counted in its name.
func browseImmich(ctx context.Context, id string) (hass.Media, error) {
	srv := config.Get().Home.Immich
	if !srv.Set() {
		return hass.Media{}, errors.New("immich: not set up")
	}
	if id != ImmichAll {
		return hass.Media{ID: id, Title: ImmichSourceName(id)}, nil
	}
	albums, err := ImmichAlbums(ctx, srv)
	if err != nil {
		return hass.Media{}, err
	}
	m := hass.Media{ID: ImmichAll, Title: "Immich", CanExpand: true}
	for _, a := range albums {
		m.Children = append(m.Children, hass.Media{ID: ImmichAlbumPrefix + a.ID,
			Title: fmt.Sprintf("%s (%d)", a.Name, a.Photos), CanExpand: true})
	}
	return m, nil
}
