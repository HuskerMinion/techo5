package home

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Where the radar comes from. RainViewer covers the world; in the lower 48 the U.S. National Weather
// Service's own composite is sharper (zoom 8 where RainViewer's free tiles stop at 7) and public
// domain. The NWS picture is fetched through the Iowa Environmental Mesonet at Iowa State, which cuts
// the national composite into map tiles every five minutes and keeps the last hour of them.

// radarSource is one place radar frames come from.
type radarSource struct {
	name   string // for the page's credit and the log
	zoom   int    // the tiles' zoom: mapZoom, or one less and drawn twice the size
	colors []paletteColor
	// frames lists the frames to show, oldest first, at most radarFrames of them.
	frames func(ctx context.Context) ([]sourceFrame, error)
}

// sourceFrame is one frame of a source: when it was measured, what it is kept under, and its tiles.
type sourceFrame struct {
	at   time.Time
	key  string
	tile func(zoom, x, y int) string
}

// Where the sources are; a test points them at its own server.
var (
	iemTiles = "https://mesonet.agron.iastate.edu/cache/tile.py/1.0.0/%s/%d/%d/%d.png"
	iemNow   = "https://mesonet.agron.iastate.edu/data/gis/images/4326/USCOMP/n0q_0.json"
)

// inLower48 reports whether a place is inside the NWS composite's coverage, the contiguous U.S.
func inLower48(lat, lon float64) bool {
	return lat >= 24.3 && lat <= 49.6 && lon >= -125.1 && lon <= -66.8
}

// radarSourceFor is the source the setting and home's place pick.
func radarSourceFor(lat, lon float64) radarSource {
	switch config.Get().Home.RadarSource {
	case config.RadarRainViewer:
		return rainViewer
	case config.RadarNWS:
		return nws
	}
	if inLower48(lat, lon) {
		return nws
	}
	return rainViewer
}

var rainViewer = radarSource{
	name:   "RainViewer",
	zoom:   mapZoom - 1,
	colors: rainViewerColors,
	frames: func(ctx context.Context) ([]sourceFrame, error) {
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
			return nil, fmt.Errorf("radar index: %w", err)
		}
		if err := json.Unmarshal(b, &index); err != nil {
			return nil, fmt.Errorf("radar index: %w", err)
		}
		past := index.Radar.Past
		if len(past) == 0 || index.Host == "" {
			return nil, errors.New("radar index is empty")
		}
		if len(past) > radarFrames {
			past = past[len(past)-radarFrames:]
		}
		out := make([]sourceFrame, len(past))
		for i, p := range past {
			host, path := index.Host, p.Path
			out[i] = sourceFrame{at: time.Unix(p.Time, 0), key: "rv" + path, tile: func(z, x, y int) string {
				// Scheme 2 (Universal Blue) is what the free tier sends whatever is asked; the table
				// that turns it back into dBZ is for that scheme.
				return fmt.Sprintf("%s%s/%d/%d/%d/%d/2/1_1.png", host, path, tileSize, z, x, y)
			}}
		}
		return out, nil
	},
}

var nws = radarSource{
	name:   "NWS",
	zoom:   mapZoom,
	colors: iemColors,
	frames: func(ctx context.Context) ([]sourceFrame, error) {
		// The newest composite's time; the older ones are its layer's -mNNm, NN minutes before it.
		var now struct {
			Meta struct {
				Valid time.Time `json:"valid"`
			} `json:"meta"`
		}
		b, err := get(ctx, iemNow)
		if err != nil {
			return nil, fmt.Errorf("radar time: %w", err)
		}
		if err := json.Unmarshal(b, &now); err != nil || now.Meta.Valid.IsZero() {
			return nil, fmt.Errorf("radar time: %v", err)
		}
		// Ten minutes apart, like RainViewer's, so the loop moves at the same pace.
		out := make([]sourceFrame, 0, radarFrames)
		for i := radarFrames - 1; i >= 0; i-- {
			back := i * 10
			layer := "nexrad-n0q-900913"
			if back > 0 {
				layer += fmt.Sprintf("-m%02dm", back)
			}
			at := now.Meta.Valid.Add(-time.Duration(back) * time.Minute)
			out = append(out, sourceFrame{at: at, key: "nws" + at.UTC().Format("20060102T1504"), tile: func(z, x, y int) string {
				return fmt.Sprintf(iemTiles, layer, z, x, y)
			}})
		}
		return out, nil
	},
}

// radarCredit is the page's credit line for a source, with the map and, where they are, the clouds.
func radarCredit(src radarSource, lon float64) string {
	s := "Radar " + src.name
	if cloudLayer(lon) != "" {
		s += "  ·  Clouds NOAA GOES"
	}
	return s + "  ·  Map NASA  ·  Places GeoNames"
}
