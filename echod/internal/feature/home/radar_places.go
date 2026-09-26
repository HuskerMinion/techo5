//go:build !dot

package home

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"image"
	"strconv"
	"strings"
	"sync"
)

// Town names for the rain map. They come from GeoNames (geonames.org, CC BY 4.0): places of 15,000
// people or more everywhere, and of 5,000 or more in the U.S. and Canada, where the country between
// cities is emptier and a map of it would otherwise say nothing. They are kept in the image as name,
// latitude and longitude in thousandths of a degree, and population, largest first; names in a script
// the screen's font has no letters for are in their Latin spelling. Only a device with a screen
// carries them.

//go:embed places.tsv.gz
var placesGz []byte

type place struct {
	name     string
	lat, lon float64
	pop      int
}

var (
	placesOnce sync.Once
	places     []place
)

func loadPlaces() []place {
	placesOnce.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(placesGz))
		if err != nil {
			return
		}
		sc := bufio.NewScanner(zr)
		for sc.Scan() {
			f := strings.Split(sc.Text(), "\t")
			if len(f) != 4 {
				continue
			}
			la, e1 := strconv.Atoi(f[1])
			lo, e2 := strconv.Atoi(f[2])
			pop, e3 := strconv.Atoi(f[3])
			if e1 != nil || e2 != nil || e3 != nil {
				continue
			}
			places = append(places, place{f[0], float64(la) / 1000, float64(lo) / 1000, pop})
		}
	})
	return places
}

// maxPlaces is how many towns in view are offered; the page draws those that fit, largest first.
const maxPlaces = 80

// placesIn are the towns on a w by h picture whose top left is world pixel x0, y0 at the map's zoom,
// largest first, each where it falls on the picture.
func placesIn(x0, y0, w, h int) []RadarPlace {
	var out []RadarPlace
	for _, p := range loadPlaces() {
		x, y := worldPixel(p.lat, p.lon, mapZoom)
		at := image.Pt(wrapX(x, x0, w), int(y)-y0)
		if at.X < 0 || at.Y < 0 || at.X >= w || at.Y >= h {
			continue
		}
		out = append(out, RadarPlace{Name: p.name, At: at, Pop: p.pop})
		if len(out) == maxPlaces {
			break
		}
	}
	return out
}
