package home

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The rain map: RainViewer's radar over an OpenStreetMap map, darkened here to sit with the rest of
// the screen, centred on Home Assistant's home zone. Both are free to use with credit, which the page
// shows; OpenStreetMap's tile policy also asks for an identifying User-Agent and light use, so the map
// is fetched once for a location, two tiles at a time, and kept. RainViewer's free tiles stop at zoom 7,
// so the radar is drawn from zoom 7 at twice the size over a zoom 8 map: about 450 km across. The
// last frames play as a loop, so the rain's direction shows.

const (
	radarW, radarH = 960, 480

	mapZoom   = 8
	radarZoom = 7
	tileSize  = 256

	// radarFrames is how many of the past frames loop (ten minutes apart).
	radarFrames = 6
	// radarEvery is how old the frames may get while the page is up.
	radarEvery = 5 * time.Minute

	radarIndex = "https://api.rainviewer.com/public/weather-maps.json"
	mapTiles   = "https://tile.openstreetmap.org/%d/%d/%d.png"
	userAgent  = "TECHO5 (https://github.com/HuskerMinion/techo5)"
)

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
}

type radarState struct {
	mu       sync.Mutex
	view     RadarView
	fetched  time.Time
	busy     bool
	lat, lon float64
	base     *image.RGBA // the darkened map, for lat/lon
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
	err := f.buildRadar()
	r.mu.Lock()
	r.busy, r.fetched = false, time.Now()
	if err != nil {
		slog.Warn("radar: fetch", "err", err)
		r.view.Problem = err.Error()
		if len(r.view.Frames) == 0 {
			// Try again sooner than a good fetch would.
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cx, cy := worldPixel(lat, lon, mapZoom)
	x0, y0 := int(cx)-radarW/2, int(cy)-radarH/2

	r := &f.radar
	r.mu.Lock()
	base := r.base
	if r.lat != lat || r.lon != lon {
		base = nil
	}
	r.mu.Unlock()
	if base == nil {
		if base, err = mosaic(ctx, radarW, radarH, x0, y0, mapZoom, 2, func(x, y int) string {
			return fmt.Sprintf(mapTiles, mapZoom, x, y)
		}); err != nil {
			return fmt.Errorf("map: %w", err)
		}
		darken(base)
		r.mu.Lock()
		r.base, r.lat, r.lon = base, lat, lon
		r.view.Home = image.Pt(radarW/2, radarH/2)
		r.mu.Unlock()
	}

	var index struct {
		Host  string `json:"host"`
		Radar struct {
			Past []struct {
				Time int64  `json:"time"`
				Path string `json:"path"`
			} `json:"past"`
		} `json:"radar"`
	}
	b, err := get(ctx, radarIndex)
	if err != nil {
		return fmt.Errorf("radar index: %w", err)
	}
	if err := json.Unmarshal(b, &index); err != nil {
		return fmt.Errorf("radar index: %w", err)
	}
	past := index.Radar.Past
	if len(past) == 0 || index.Host == "" {
		return errors.New("radar index is empty")
	}
	if len(past) > radarFrames {
		past = past[len(past)-radarFrames:]
	}

	frames := make([]RadarFrame, 0, len(past))
	for _, p := range past {
		// The radar at half the map's zoom, twice the size.
		rain, err := mosaic(ctx, radarW/2, radarH/2, x0/2, y0/2, radarZoom, 4, func(x, y int) string {
			return fmt.Sprintf("%s%s/%d/%d/%d/%d/2/1_1.png", index.Host, p.Path, tileSize, radarZoom, x, y)
		})
		if err != nil {
			return fmt.Errorf("radar: %w", err)
		}
		img := image.NewRGBA(image.Rect(0, 0, radarW, radarH))
		draw.Draw(img, img.Bounds(), base, image.Point{}, draw.Src)
		xdraw.ApproxBiLinear.Scale(img, img.Bounds(), rain, rain.Bounds(), draw.Over,
			&xdraw.Options{SrcMask: image.NewUniform(color.Alpha{A: 210})})
		frames = append(frames, RadarFrame{Image: img, At: time.Unix(p.Time, 0)})
	}
	r.mu.Lock()
	r.view.Frames = frames
	r.mu.Unlock()
	slog.Info("radar: frames fetched", "frames", len(frames), "latest", frames[len(frames)-1].At.Format(time.Kitchen))
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

// darken turns a light map dark: brightness inverted, so land goes near black and labels light, then
// tinted towards the screen's warm ground.
func darken(img *image.RGBA) {
	p := img.Pix
	for i := 0; i+3 < len(p); i += 4 {
		lum := (299*int(p[i]) + 587*int(p[i+1]) + 114*int(p[i+2])) / 1000
		v := 255 - lum
		p[i] = uint8(min(255, 22+v*62/100))
		p[i+1] = uint8(min(255, 18+v*57/100))
		p[i+2] = uint8(min(255, 16+v*52/100))
		p[i+3] = 255
	}
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := tileClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
