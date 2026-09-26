package home

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The rain map, centered on Home Assistant's home zone: radar from the NWS in the lower 48 or
// RainViewer anywhere (radar_source.go), drawn in one look over NASA's Blue Marble with the clouds from
// a weather satellite (radar_look.go). All of it is free to use with credit, which the page shows; the
// map is fetched once for a location, two tiles at a time, and kept. RainViewer's free tiles stop at
// zoom 7, so its radar is drawn from zoom 7 at twice the size over the zoom 8 map; the NWS's comes at
// zoom 8. The last frames play as a loop, so the rain's direction shows.
//
// Speed, since a page nobody can see yet is a page nobody waits for: the fetch starts when the weather
// page opens, where the Radar button is; the newest frame is shown as soon as it alone is in, and the
// older ones for the loop arrive behind it, fetched side by side; a frame already fetched is kept, so
// a refresh fetches only what is new; and the map, which only changes when home does, is kept on disk
// across restarts. Measured on a PC, RainViewer answers in about 0.6 s whatever is asked, so the time
// was all in asking one frame after another.
//
// The picture is built at the panel's own size (radarW and radarH, in the panel_*.go files), because
// the page draws it at one to one: an image smaller than the screen leaves the rest of the screen
// empty, which is what a Show 8 got while this was fixed at the Show 5's 960x480. A wider panel
// therefore sees more country rather than a stretched map — about 450 km across on a Show 5 and
// about 600 on a Show 8.

const (
	mapZoom   = 8
	radarZoom = 7
	tileSize  = 256

	// radarFrames is how many of the past frames loop (ten minutes apart), and framesAtOnce how many of
	// them are fetched side by side, each four tiles at a time.
	radarFrames  = 6
	framesAtOnce = 2
	// radarEvery is how old the frames may get while the page is up.
	radarEvery = 5 * time.Minute

	userAgent = "TECHO5 (https://github.com/HuskerMinion/techo5)"
)

// Where RainViewer's frames are listed; a test points it at its own server.
var radarIndex = "https://api.rainviewer.com/public/weather-maps.json"

// RadarFrame is one picture of the loop and when the rain was measured.
type RadarFrame struct {
	Image *image.RGBA
	At    time.Time
}

// RadarView is what the radar page draws.
type RadarView struct {
	Frames  []RadarFrame
	Loading bool
	Problem string
	Home    image.Point // where home is on the picture
	Credit  string      // where the radar, the clouds and the map came from, for the page to show
	Short   string      // the same in a few words, for the Spot's round face
	Note    string      // why the source shown is not the one chosen, or ""
	Places  []RadarPlace
	Origin  image.Point // the picture's top left, in world pixels at the map's zoom
}

// Pixel is where a longitude and latitude fall on the picture, the world wrapped around at the
// dateline so a place just across it lands beside home rather than a world's width away.
func (v RadarView) Pixel(lon, lat float64) image.Point {
	x, y := worldPixel(lat, lon, mapZoom)
	return image.Pt(wrapX(x, v.Origin.X, radarW), int(math.Round(y))-v.Origin.Y)
}

// wrapX is world x at the map's zoom as a column of a picture w wide whose left edge is x0: the copy of
// it nearest the picture's middle, the world being a loop.
func wrapX(x float64, x0, w int) int {
	world := float64(tileSize) * math.Pow(2, mapZoom)
	d := x - float64(x0) - float64(w)/2
	d -= world * math.Round(d/world)
	return int(math.Round(d + float64(w)/2))
}

// RadarPlace is a town on the picture, for the page to name where there is room: largest first.
type RadarPlace struct {
	Name string
	At   image.Point
	Pop  int
}

type radarState struct {
	mu       sync.Mutex
	view     RadarView
	fetched  time.Time
	busy     bool
	lat, lon float64
	base     *image.RGBA // the tinted map, for lat/lon
	source   string      // the source the frames in made came from
	gen      int         // bumped when the source setting changes; a fetch begun before it is stale

	// made are the frames already drawn, by their source's key for them, for the frames still current.
	made map[string]RadarFrame
}

// mapDir is where the tinted map is kept between restarts (a test moves it), and mapFile its name,
// for what it shows.
var mapDir = layout.StateDir

func mapFile(lat, lon float64) string {
	// v2 is Blue Marble with tintBase's look: a change to either has to be a new name, or the old look
	// comes back from disk.
	return filepath.Join(mapDir, fmt.Sprintf("radar-map-v2-%.4f-%.4f-%dx%d.png", lat, lon, radarW, radarH))
}

// savedMap is the map kept on disk for lat/lon, or nil.
func savedMap(lat, lon float64) *image.RGBA {
	f, err := os.Open(mapFile(lat, lon))
	if err != nil {
		return nil
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil || img.Bounds().Dx() != radarW || img.Bounds().Dy() != radarH {
		return nil
	}
	out := image.NewRGBA(image.Rect(0, 0, radarW, radarH))
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

// saveMap keeps the map on disk, in place of any kept for somewhere else.
func saveMap(lat, lon float64, img *image.RGBA) {
	old, _ := filepath.Glob(filepath.Join(mapDir, "radar-map-*")) // a .tmp a crash left behind too
	for _, f := range old {
		_ = os.Remove(f)
	}
	path := mapFile(lat, lon)
	f, err := os.Create(path + ".tmp")
	if err != nil {
		slog.Debug("radar: keeping the map", "err", err)
		return
	}
	err = png.Encode(f, img)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		_ = os.Remove(path + ".tmp")
		slog.Debug("radar: keeping the map", "err", err)
	}
}

var tileClient = &http.Client{Timeout: 15 * time.Second}

// Radar is the rain map as last fetched, and starts a fetch when it is due.
func (f *Feature) Radar() RadarView {
	r := &f.radar
	r.mu.Lock()
	due := !r.busy && (r.fetched.IsZero() || time.Since(r.fetched) > radarEvery)
	if due {
		r.busy = true
	}
	v := r.view
	v.Loading = r.busy && len(v.Frames) == 0
	r.mu.Unlock()
	if due {
		go f.fetchRadar()
	}
	return v
}

func (f *Feature) fetchRadar() {
	r := &f.radar
	r.mu.Lock()
	gen := r.gen
	r.mu.Unlock()
	err := f.buildRadar()
	r.mu.Lock()
	r.busy, r.fetched = false, time.Now()
	if r.gen != gen {
		r.fetched = time.Time{} // the source changed while this fetch ran: fetch again at once
	}
	if err != nil {
		slog.Warn("radar: fetch", "err", err)
		r.view.Problem = err.Error()
		if len(r.view.Frames) < radarFrames {
			// Try again sooner than a good fetch would: nothing shown, or only the newest frame still.
			r.fetched = time.Now().Add(-radarEvery + 30*time.Second)
		}
	} else {
		r.view.Problem = ""
	}
	r.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// home is Home Assistant's home location: the zone's attributes, or its configuration.
func homeLocation() (lat, lon float64, err error) {
	t := hastate.Get()
	la, ok1 := t.Value("zone.home", "latitude")
	lo, ok2 := t.Value("zone.home", "longitude")
	if ok1 && ok2 {
		lat, e1 := strconv.ParseFloat(la, 64)
		lon, e2 := strconv.ParseFloat(lo, 64)
		if e1 == nil && e2 == nil && (lat != 0 || lon != 0) {
			return lat, lon, nil
		}
	}
	if hass.Get().Ready() {
		if c, err := hass.Get().Config(); err == nil && (c.Latitude != 0 || c.Longitude != 0) {
			return c.Latitude, c.Longitude, nil
		}
	}
	return 0, 0, errors.New("home's location is not known yet")
}

// worldPixel is a place on the Web Mercator world at a zoom, in pixels.
func worldPixel(lat, lon float64, zoom int) (x, y float64) {
	n := float64(tileSize) * math.Pow(2, float64(zoom))
	x = (lon + 180) / 360 * n
	phi := lat * math.Pi / 180
	y = (1 - math.Log(math.Tan(phi)+1/math.Cos(phi))/math.Pi) / 2 * n
	return x, y
}

func (f *Feature) buildRadar() error {
	lat, lon, err := homeLocation()
	if err != nil {
		return err
	}
	return f.buildRadarAt(lat, lon)
}

// buildRadarAt is buildRadar for a home at lat, lon.
func (f *Feature) buildRadarAt(lat, lon float64) error {
	var err error
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	began := time.Now()
	var newestAfter time.Duration // how long until the newest frame was there to show

	cx, cy := worldPixel(lat, lon, mapZoom)
	x0, y0 := int(cx)-radarW/2, int(cy)-radarH/2

	src, note := radarSourceFor(lat, lon)
	r := &f.radar
	r.mu.Lock()
	base := r.base
	if r.lat != lat || r.lon != lon {
		base, r.made = nil, nil
	}
	if r.source != src.name {
		// A different source: its frames replace the old ones rather than looping with them.
		r.made, r.view.Frames = nil, nil
	}
	r.mu.Unlock()
	if base == nil {
		base = savedMap(lat, lon)
	}
	if base == nil {
		if base, err = mosaic(ctx, radarW, radarH, x0, y0, mapZoom, 2, func(x, y int) string {
			return fmt.Sprintf(baseTiles, mapZoom, y, x)
		}); err != nil {
			return fmt.Errorf("map: %w", err)
		}
		tintBase(base)
		saveMap(lat, lon, base)
	}
	r.mu.Lock()
	r.base, r.lat, r.lon, r.source = base, lat, lon, src.name
	r.view.Home = image.Pt(radarW/2, radarH/2)
	r.view.Credit, r.view.Short, r.view.Note = radarCredit(src, lon), src.name+" · NASA", note
	r.view.Places = placesIn(x0, y0, radarW, radarH)
	r.view.Origin = image.Pt(x0, y0)
	r.mu.Unlock()

	// The clouds change slowly next to the rain, so one picture of them sits under every frame. They
	// are fetched while the frames are listed, and given three seconds: the map alone is under the rain
	// if the satellite is slow or cannot be reached, rather than the rain waiting for it.
	grounded := make(chan *image.RGBA, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		g := image.NewRGBA(base.Bounds())
		draw.Draw(g, g.Bounds(), base, image.Point{}, draw.Src)
		if err := clouds(cctx, g, lon, x0, y0); err != nil {
			slog.Debug("radar: clouds", "err", err)
			draw.Draw(g, g.Bounds(), base, image.Point{}, draw.Src)
		}
		grounded <- g
	}()

	past, err := src.frames(ctx)
	ground := <-grounded
	if err != nil {
		return err
	}

	// One frame: the source's radar at its zoom, turned back into dBZ and painted over the ground.
	var fctx context.Context = ctx // the fetches' own, once they start side by side
	scale := 1 << (mapZoom - src.zoom)
	frame := func(p sourceFrame) (RadarFrame, error) {
		rain, err := mosaic(fctx, radarW/scale, radarH/scale, floorDiv(x0, scale), floorDiv(y0, scale), src.zoom, 4, func(x, y int) string {
			return p.tile(src.zoom, x, y)
		})
		if err != nil {
			return RadarFrame{}, fmt.Errorf("radar: %w", err)
		}
		img := image.NewRGBA(image.Rect(0, 0, radarW, radarH))
		draw.Draw(img, img.Bounds(), ground, image.Point{}, draw.Src)
		paintRain(img, reflectivity(rain, src.colors), rain.Bounds().Dx(), rain.Bounds().Dy())
		return RadarFrame{Image: img, At: p.at}, nil
	}

	r.mu.Lock()
	made := make(map[string]RadarFrame, len(past))
	for _, p := range past {
		if fr, ok := r.made[p.key]; ok {
			made[p.key] = fr
		}
	}
	r.mu.Unlock()
	fetched := 0

	// The newest first, and on screen by itself if there is nothing there yet: it is the one anybody
	// opening the page is looking for.
	newest := past[len(past)-1]
	if _, ok := made[newest.key]; !ok {
		fr, err := frame(newest)
		if err != nil {
			return err
		}
		made[newest.key] = fr
		fetched++
		r.mu.Lock()
		if len(r.view.Frames) == 0 {
			r.view.Frames = []RadarFrame{fr}
		}
		r.mu.Unlock()
		newestAfter = time.Since(began)
		f.Changed.Emit(struct{}{})
	}

	// The rest side by side, framesAtOnce at a time. What is missing is listed before anything starts,
	// so no fetch writes the map while the loop is still reading it; the first failure stops the others,
	// and what did arrive is kept for the retry either way.
	var todo []int
	for i, p := range past[:len(past)-1] {
		if _, ok := made[p.key]; !ok {
			todo = append(todo, i)
		}
	}
	var stop context.CancelFunc
	fctx, stop = context.WithCancel(ctx)
	defer stop()
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
		sem   = make(chan struct{}, framesAtOnce)
	)
	for _, i := range todo {
		p := past[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if fctx.Err() != nil {
				return
			}
			fr, err := frame(p)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if first == nil {
					first = err
					stop()
				}
				return
			}
			made[p.key] = fr
			fetched++
		}()
	}
	wg.Wait()
	if first != nil {
		r.mu.Lock()
		r.made = made
		r.mu.Unlock()
		return first
	}

	frames := make([]RadarFrame, 0, len(past))
	for _, p := range past {
		frames = append(frames, made[p.key])
	}
	r.mu.Lock()
	r.view.Frames, r.made = frames, made
	r.mu.Unlock()
	slog.Info("radar: frames ready", "source", src.name, "frames", len(frames), "fetched", fetched,
		"latest", frames[len(frames)-1].At.Local().Format(time.Kitchen),
		"newest_after", newestAfter.Round(10*time.Millisecond), "took", time.Since(began).Round(10*time.Millisecond))
	return nil
}

// mosaic is a w by h picture of tiles at zoom, its top left at world pixel (x0, y0).
func mosaic(ctx context.Context, w, h, x0, y0, zoom, parallel int, url func(x, y int) string) (*image.RGBA, error) {
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	n := 1 << zoom
	type tile struct{ tx, ty int }
	var tiles []tile
	for ty := floorDiv(y0, tileSize); ty*tileSize < y0+h; ty++ {
		if ty < 0 || ty >= n {
			continue
		}
		for tx := floorDiv(x0, tileSize); tx*tileSize < x0+w; tx++ {
			tiles = append(tiles, tile{tx, ty})
		}
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
		sem   = make(chan struct{}, parallel)
	)
	for _, t := range tiles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			x := ((t.tx % n) + n) % n
			b, err := get(ctx, url(x, t.ty))
			var img image.Image
			if err == nil {
				img, err = decodeWithin(b, maxArtPixels, "map tile")
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if first == nil {
					first = err
				}
				return
			}
			at := image.Pt(t.tx*tileSize-x0, t.ty*tileSize-y0)
			draw.Draw(out, image.Rectangle{Min: at, Max: at.Add(image.Pt(tileSize, tileSize))}, img, img.Bounds().Min, draw.Src)
		}()
	}
	wg.Wait()
	return out, first
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func get(ctx context.Context, url string) ([]byte, error) { return getLimit(ctx, url, "", 4<<20) }

// errNotFound is a 404: the NWS's answer for a place it has no forecast point for.
var errNotFound = errors.New("not found")

// getLimit is get asking for a type (the NWS's API answers in the one it is asked for) and reading up
// to limit bytes; an answer longer than that is an error rather than a cut-off one.
func getLimit(ctx context.Context, url, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := tileClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", url, errNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: longer than %d bytes", url, limit)
	}
	return b, err
}
