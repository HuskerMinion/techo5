package home

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

const (
	tallA  = "bbbbbbbb-0000-0000-0000-00000000000a"
	tallC  = "bbbbbbbb-0000-0000-0000-00000000000c"
	wide1  = "bbbbbbbb-0000-0000-0000-000000000001"
	wide2  = "bbbbbbbb-0000-0000-0000-000000000002"
	wide3  = "bbbbbbbb-0000-0000-0000-000000000003"
	person = "cccccccc-0000-0000-0000-000000000001"
)

// solidJPEG is a w×h photo of one color.
func solidJPEG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// moreImmich is an Immich with people, photos' details, and tall and wide photos of their own colors.
type moreImmich struct {
	mu       sync.Mutex
	searches []map[string]any
	fetched  []string
	// found is the photos a search answers, in order; none is tallA alone.
	found []string
}

var (
	red  = color.RGBA{220, 30, 30, 255}
	blue = color.RGBA{30, 30, 220, 255}
)

func (m *moreImmich) serve(t *testing.T) *httptest.Server {
	t.Helper()
	photos := map[string][]byte{
		tallA: solidJPEG(t, 300, 400, red),
		tallC: solidJPEG(t, 300, 400, blue),
		wide1: solidJPEG(t, 400, 300, color.RGBA{30, 200, 30, 255}),
		wide2: solidJPEG(t, 400, 300, color.RGBA{30, 200, 30, 255}),
		wide3: solidJPEG(t, 400, 300, color.RGBA{30, 200, 30, 255}),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != testKey {
			http.Error(w, `{"message":"Invalid API key"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/people":
			fmt.Fprintf(w, `{"total":2,"hidden":0,"hasNextPage":false,"people":[{"id":"dddddddd-0000-0000-0000-000000000009","name":"Someone Else"},{"id":%q,"name":"Pat Example"}]}`, person)
		case r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata":
			var q map[string]any
			_ = json.NewDecoder(r.Body).Decode(&q)
			m.mu.Lock()
			m.searches = append(m.searches, q)
			found := slices.Clone(m.found)
			m.mu.Unlock()
			if len(found) == 0 {
				found = []string{tallA}
			}
			var items []string
			for _, id := range found {
				items = append(items, fmt.Sprintf(`{"id":%q,"type":"IMAGE"}`, id))
			}
			fmt.Fprintf(w, `{"assets":{"total":%d,"count":%d,"items":[%s],"nextPage":null}}`, len(items), len(items), strings.Join(items, ","))
		case r.Method == http.MethodGet && r.URL.Query().Get("size") == "preview":
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/assets/"), "/thumbnail")
			m.mu.Lock()
			m.fetched = append(m.fetched, id)
			m.mu.Unlock()
			if b, ok := photos[id]; ok {
				_, _ = w.Write(b)
				return
			}
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/"+tallA:
			fmt.Fprint(w, `{"id":"x","localDateTime":"2019-06-15T14:30:00.000Z","exifInfo":{"dateTimeOriginal":"2019-06-15T19:30:00.000Z","city":"Springfield","state":"Ohio","country":"United States"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/"+tallC:
			fmt.Fprint(w, `{"id":"x","exifInfo":{"dateTimeOriginal":"2020-01-02T08:00:00.000Z","city":null,"state":null,"country":"Canada"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Favorites, the days and a person go into Immich's search as it takes them: whole days on this
// device's clock, and the person by the id Immich has for the name, whatever its case.
func TestImmichFilterAsks(t *testing.T) {
	letLoopback(t)
	m := &moreImmich{}
	srv := m.serve(t)
	s := config.Immich{Server: srv.URL, Key: testKey}
	fl := immichFilter{favorites: true, from: "2019-06-01", to: "2019-06-30", person: "pat example"}
	if _, err := immichPhotos(context.Background(), s, ImmichAll, fl, 10); err != nil {
		t.Fatal(err)
	}
	if len(m.searches) != 1 {
		t.Fatalf("%d searches", len(m.searches))
	}
	q := m.searches[0]
	from, _ := time.ParseInLocation(config.PhotoDay, "2019-06-01", time.Local)
	to, _ := time.ParseInLocation(config.PhotoDay, "2019-07-01", time.Local)
	if q["isFavorite"] != true {
		t.Errorf("isFavorite = %v", q["isFavorite"])
	}
	if q["takenAfter"] != from.UTC().Format(immichTime) || q["takenBefore"] != to.Add(-time.Millisecond).UTC().Format(immichTime) {
		t.Errorf("days asked as %v to %v", q["takenAfter"], q["takenBefore"])
	}
	if ids, _ := q["personIds"].([]any); len(ids) != 1 || ids[0] != person {
		t.Errorf("personIds = %v", q["personIds"])
	}
	if q["type"] != "IMAGE" {
		t.Errorf("type = %v", q["type"])
	}
}

// No filter asks for nothing more than before.
func TestImmichNoFilter(t *testing.T) {
	letLoopback(t)
	m := &moreImmich{}
	srv := m.serve(t)
	if _, err := immichPhotos(context.Background(), config.Immich{Server: srv.URL, Key: testKey}, ImmichAll, immichFilter{}, 10); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"isFavorite", "takenAfter", "takenBefore", "personIds"} {
		if _, ok := m.searches[0][k]; ok {
			t.Errorf("%s asked with no filter", k)
		}
	}
}

// A person Immich does not know is said so, and nothing is searched for; a day that is not one too.
func TestImmichFilterRefuses(t *testing.T) {
	letLoopback(t)
	m := &moreImmich{}
	srv := m.serve(t)
	s := config.Immich{Server: srv.URL, Key: testKey}
	_, err := immichPhotos(context.Background(), s, ImmichAll, immichFilter{person: "Nobody Known"}, 10)
	if err == nil || !strings.Contains(err.Error(), "Nobody Known") {
		t.Errorf("err = %v", err)
	}
	if _, err := immichPhotos(context.Background(), s, ImmichAll, immichFilter{from: "June"}, 10); err == nil {
		t.Error("a first day that is not a date was taken")
	}
	if len(m.searches) != 0 {
		t.Errorf("%d searches made for a filter that could not be", len(m.searches))
	}
}

// When and where: the clock where it was taken, and the place as a caption names it.
func TestImmichAbout(t *testing.T) {
	letLoopback(t)
	m := &moreImmich{}
	srv := m.serve(t)
	s := config.Immich{Server: srv.URL, Key: testKey}
	a, err := immichAbout(context.Background(), s, immichAssetPrefix+tallA)
	if err != nil {
		t.Fatal(err)
	}
	if a.When != time.Date(2019, 6, 15, 14, 30, 0, 0, time.UTC) || a.Place != "Springfield, Ohio" {
		t.Errorf("about = %+v", a)
	}
	c, err := immichAbout(context.Background(), s, immichAssetPrefix+tallC)
	if err != nil {
		t.Fatal(err)
	}
	if c.When != time.Date(2020, 1, 2, 8, 0, 0, 0, time.UTC) || c.Place != "Canada" {
		t.Errorf("about = %+v", c)
	}
	if _, err := immichAbout(context.Background(), s, immichAssetPrefix+"../x"); err == nil {
		t.Error("an id that is not one was asked about")
	}
}

func TestPlaceName(t *testing.T) {
	for _, c := range []struct{ city, state, country, want string }{
		{"Springfield", "Ohio", "United States", "Springfield, Ohio"},
		{"Lyon", "", "France", "Lyon, France"},
		{"", "Bavaria", "Germany", "Bavaria, Germany"},
		{"", "", "Canada", "Canada"},
		{" ", "", "", ""},
	} {
		if got := placeName(c.city, c.state, c.country); got != c.want {
			t.Errorf("placeName(%q, %q, %q) = %q, want %q", c.city, c.state, c.country, got, c.want)
		}
	}
}

// slideshowFeature is a Feature showing an Immich list of photos ids, as Background mode.
func slideshowFeature(t *testing.T, m *moreImmich, change func(*config.Slideshow), ids ...string) *Feature {
	t.Helper()
	letLoopback(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	srv := m.serve(t)
	if err := config.Set().Home().Immich(config.Immich{Server: srv.URL, Key: testKey}); err != nil {
		t.Fatal(err)
	}
	s := config.Slideshow{Source: ImmichAll, Mode: config.SlideshowBackground, InOrder: true}
	change(&s)
	if err := config.Set().Home().Slideshow(s); err != nil {
		t.Fatal(err)
	}
	f := &Feature{}
	f.buildSlideshowSelect()
	for _, id := range ids {
		f.slideshow.children = append(f.slideshow.children, mediaOf(id))
	}
	f.slideshow.browsed = time.Now()
	return f
}

// mediaOf is an Immich photo as the slideshow's list holds it.
func mediaOf(id string) hass.Media {
	return hass.Media{ID: immichAssetPrefix + id, Kind: "image/jpeg", CanPlay: true}
}

func shownNow(f *Feature) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, id := range f.slideshow.shown {
		out = append(out, strings.TrimPrefix(id, immichAssetPrefix))
	}
	return out
}

// due puts the photo up now over its time, so the next advance fetches the next.
func due(f *Feature) {
	f.mu.Lock()
	f.slideshow.at = time.Now().Add(-slideshowEveryMax)
	f.mu.Unlock()
}

// A tall photo is paired with the next tall one; a wide one looked at on the way is shown next, not
// skipped.
func TestPhotoPairs(t *testing.T) {
	if !PairsHere() {
		t.Skip("a round screen shows no pairs")
	}
	m := &moreImmich{}
	f := slideshowFeature(t, m, func(s *config.Slideshow) { s.Pairs = true }, tallA, wide1, tallC, wide2)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{tallA, tallC}) {
		t.Fatalf("shown %v, want the two tall photos", got)
	}
	img := f.currentSlideshowPhoto()
	left, right := img.RGBAAt(slideshowW/4, slideshowH/2), img.RGBAAt(slideshowW*3/4, slideshowH/2)
	if left.R < 180 || left.B > 80 || right.B < 180 || right.R > 80 {
		t.Errorf("left %v, right %v: want red then blue", left, right)
	}
	if gap := img.RGBAAt(slideshowW/2, slideshowH/2); gap.R > 20 || gap.G > 20 || gap.B > 20 {
		t.Errorf("between the two %v, want dark", gap)
	}
	due(f)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{wide1}) {
		t.Errorf("then shown %v, want the wide one passed over", got)
	}
	due(f)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{wide2}) {
		t.Errorf("then shown %v, want the list going on", got)
	}
}

// With no second tall photo near, one is shown alone, and the wide ones looked at come next.
func TestPairAloneWhenNoneNear(t *testing.T) {
	if !PairsHere() {
		t.Skip("a round screen shows no pairs")
	}
	m := &moreImmich{}
	f := slideshowFeature(t, m, func(s *config.Slideshow) { s.Pairs = true }, tallA, wide1, wide2, wide3, tallC)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{tallA}) {
		t.Fatalf("shown %v, want the tall one alone", got)
	}
	for _, want := range []string{wide1, wide2, wide3} {
		due(f)
		f.advanceSlideshow()
		if got := shownNow(f); !slices.Equal(got, []string{want}) {
			t.Fatalf("shown %v, want %s next", got, want)
		}
	}
}

// Pairs off, a tall photo is shown alone and nothing more is fetched for it.
func TestNoPairsFetchesOne(t *testing.T) {
	m := &moreImmich{}
	f := slideshowFeature(t, m, func(*config.Slideshow) {}, tallA, tallC)
	f.advanceSlideshow()
	if got := shownNow(f); !slices.Equal(got, []string{tallA}) {
		t.Fatalf("shown %v", got)
	}
	if len(m.fetched) != 1 {
		t.Errorf("fetched %v, want one photo", m.fetched)
	}
}

// Captions are asked for each photo up, the right one of a pair marked, in Screensaver mode, which
// alone shows them.
func TestPhotoCaptions(t *testing.T) {
	m := &moreImmich{}
	f := slideshowFeature(t, m, func(s *config.Slideshow) {
		s.Caption, s.Pairs, s.Mode = true, true, config.SlideshowScreensaver
	}, tallA, tallC)
	f.advanceSlideshow()
	f.mu.Lock()
	caps := slices.Clone(f.slideshow.captions)
	f.mu.Unlock()
	got := f.SlideshowCaptions()
	if !slices.Equal(got, caps) || len(got) == 0 {
		t.Fatalf("captions %v, kept %v", got, caps)
	}
	if got[0].Place != "Springfield, Ohio" || got[0].Right {
		t.Errorf("first caption %+v", got[0])
	}
	if PairsHere() {
		if len(got) != 2 || !got[1].Right || !got[1].Pair || got[1].Place != "Canada" {
			t.Errorf("captions %+v, want both of the pair", got)
		}
	}
	f.ChooseSlideshowMode(config.SlideshowBackground)
	if got := f.SlideshowCaptions(); got != nil {
		t.Errorf("captions in Background mode: %v", got)
	}
	f.ChooseSlideshowMode(config.SlideshowScreensaver)
	f.SetSlideshowCaption(false)
	if got := f.SlideshowCaptions(); got != nil {
		t.Errorf("captions with them off: %v", got)
	}
	// Not asked for at all where they would not be shown.
	if got := slideshowCaptions(config.Slideshow{Caption: true, Mode: config.SlideshowBackground}, []string{immichAssetPrefix + tallA}); got != nil {
		t.Errorf("captions asked for Background mode: %v", got)
	}
}

// The days are kept only as days, and only in order; what was set stays when they are refused.
func TestSetImmichDays(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := &Feature{}
	f.buildSlideshowSelect()
	if err := f.SetImmichDays("2019-06-01", "2019-06-30"); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"June", ""}, {"", "2019-13-40"}, {"2019-07-01", "2019-06-01"}} {
		if err := f.SetImmichDays(c[0], c[1]); err == nil {
			t.Errorf("%q to %q was kept", c[0], c[1])
		}
	}
	if _, from, to, _ := f.ImmichFilters(); from != "2019-06-01" || to != "2019-06-30" {
		t.Errorf("days now %q to %q", from, to)
	}
	if f.immichFromTxt.Get() != "2019-06-01" || f.immichToTxt.Get() != "2019-06-30" {
		t.Errorf("Home Assistant shows %q to %q", f.immichFromTxt.Get(), f.immichToTxt.Get())
	}
	if err := f.SetImmichDays("", ""); err != nil {
		t.Fatal(err)
	}
	if _, from, to, _ := f.ImmichFilters(); from != "" || to != "" {
		t.Errorf("days not cleared: %q to %q", from, to)
	}
	f.SetImmichPerson("  " + strings.Repeat("x", 200) + "  ")
	if _, _, _, p := f.ImmichFilters(); len(p) != immichNameMax {
		t.Errorf("a person's name kept at %d characters", len(p))
	}
}

// Pairs are refused where there are no halves to put them in.
func TestPairsOnlyWhereThereAreHalves(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := &Feature{}
	f.buildSlideshowSelect()
	f.SetSlideshowPairs(true)
	if f.SlideshowPairs() != PairsHere() {
		t.Errorf("pairs %v on a screen where they are %v", f.SlideshowPairs(), PairsHere())
	}
}
