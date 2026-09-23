package home

import (
	"bytes"
	"context"
	"image"
	"image/draw"
	_ "image/jpeg" // album art
	_ "image/png"  // station logos
	"log/slog"
	"net/http"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/radiometa"
)

// What the station is playing, for the now-playing screen: the song, and a picture for the
// background — the album art when there is a song with one, the station's logo otherwise.
// Polled from the station's service while the radio runs; the screen draws whatever is here.

const (
	metaEvery = 15 * time.Second

	// thumbSide is the square a picture is also made into, for a screen that shows it as a picture
	// rather than behind the words (the Spot's round one).
	//
	// artW and artH — the size the background picture is made to fit — live in the panel_*.go files,
	// because they are the panel and the panel is not the same on every device. The now-playing page
	// draws the picture at one to one, so a picture built at the Show 5's 960x480 covered only the
	// top left of a Show 8 and left the rest bare.
	thumbSide = 240
)

// meta is the state the poller keeps.
type meta struct {
	station string // the list name being followed
	st      radiometa.Station
	now     radiometa.Now
	art     *image.RGBA
	thumb   *image.RGBA
	artURL  string
	artLogo bool // the picture is the station's logo, not a cover
}

var artClient = &http.Client{Timeout: 10 * time.Second}

// playingStation is the station name to follow, or empty when the radio is not running.
func (f *Feature) playingStation() string {
	if playing, paused := media.Get().Playing(); !playing && !paused {
		return ""
	}
	f.mu.Lock()
	urlName, chosen := f.urlName, f.chosen
	f.mu.Unlock()
	if urlName != "" {
		return urlName
	}
	// Home Assistant's "last station" text: the stream is its proxy, so the name is not in the
	// URL, and the tapped name is cleared once the stream starts.
	if entity := config.Get().Home.Radio.Now; entity != "" {
		if v := hastate.Get().State(entity); v != "" && v != "unknown" && v != "unavailable" {
			return v
		}
	}
	return chosen
}

// metaLoop follows the playing station.
func (f *Feature) metaLoop(ctx context.Context) {
	slog.Info("radio: following what plays")
	tick := time.NewTicker(metaEvery)
	defer tick.Stop()
	for {
		f.refreshMeta(ctx)
		select {
		case <-ctx.Done():
			return
		case <-f.metaPoke:
		case <-tick.C:
		}
	}
}

// pokeMeta asks for a refresh now: the station changed.
func (f *Feature) pokeMeta() {
	select {
	case f.metaPoke <- struct{}{}:
	default:
	}
}

func (f *Feature) refreshMeta(ctx context.Context) {
	station := f.playingStation()
	f.mu.Lock()
	cur := f.meta
	f.mu.Unlock()
	slog.Debug("radio: refresh", "station", station, "following", cur.station)
	if station == "" {
		if cur.station != "" {
			f.mu.Lock()
			f.meta = meta{}
			f.mu.Unlock()
			f.Changed.Emit(struct{}{})
		}
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	next := cur
	if cur.station != station {
		next = meta{station: station}
		st, err := radiometa.Resolve(ctx, station)
		if err != nil {
			slog.Info("radio: station not found on its service", "station", station, "err", err)
			f.mu.Lock()
			f.meta = next
			f.mu.Unlock()
			f.Changed.Emit(struct{}{})
			return
		}
		next.st = st
	}
	if next.st.ID == "" {
		return
	}
	now, err := radiometa.Playing(ctx, next.st)
	if err != nil {
		slog.Debug("radio: now playing", "station", station, "err", err)
	} else {
		next.now = now
	}
	// The picture: cover if the song has one, else the logo; fetched only when the URL changes.
	want, logo := next.now.Art, false
	if want == "" {
		want, logo = next.st.Logo, true
	}
	if want != next.artURL {
		img, thumb, err := fetchArt(ctx, want, logo)
		if err != nil {
			slog.Debug("radio: art", "url", want, "err", err)
			img, thumb = nil, nil
		}
		next.art, next.thumb, next.artURL, next.artLogo = img, thumb, want, logo
	}
	f.mu.Lock()
	changed := next.now != cur.now || next.artURL != cur.artURL || next.station != cur.station
	f.meta = next
	f.mu.Unlock()
	if changed {
		slog.Info("radio: now", "station", station, "title", next.now.Title, "artist", next.now.Artist, "art", next.artURL != "")
		f.Changed.Emit(struct{}{})
	}
}

// fetchArt downloads a picture and lays it out for the panel: a cover is scaled to fill the
// panel and cropped; a logo is scaled to fit and centered, since a cropped logo is no logo. The square
// thumbnail follows the same rule.
func fetchArt(ctx context.Context, u string, logo bool) (*image.RGBA, *image.RGBA, error) {
	if u == "" {
		return nil, nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	res, err := artClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(http.MaxBytesReader(nil, res.Body, 4<<20)); err != nil {
		return nil, nil, err
	}
	src, err := decodeWithin(buf.Bytes(), maxArtPixels, "cover art from "+u)
	if err != nil {
		return nil, nil, err
	}
	dst := image.NewRGBA(image.Rect(0, 0, artW, artH))
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 {
		return nil, nil, nil
	}
	var target image.Rectangle
	if logo {
		// Fit on the right half, clear of the text, with room around it. These were 500, artW-40 and
		// artH-140 when the panel was always 960x480; they are the same fractions of whatever panel
		// this is, so the logo stays clear of the words on a wider screen too.
		left, right := artW*500/960, artW-artW*40/960
		h := artH * 340 / 480
		w := sw * h / sh
		if w > right-left {
			w = right - left
			h = sh * w / sw
		}
		x := left + (right-left-w)/2
		target = image.Rect(x, (artH-h)/2, x+w, (artH+h)/2)
	} else {
		// Fill, cropping whichever way is longer.
		w, h := artW, sh*artW/sw
		if h < artH {
			w, h = sw*artH/sh, artH
		}
		target = image.Rect((artW-w)/2, (artH-h)/2, (artW-w)/2+w, (artH-h)/2+h)
	}
	xdraw.ApproxBiLinear.Scale(dst, target, src, sb, draw.Src, nil)

	thumb := image.NewRGBA(image.Rect(0, 0, thumbSide, thumbSide))
	w, h := thumbSide, thumbSide
	if (sw > sh) == logo {
		h = sh * thumbSide / sw // fit the longer side (a logo), or fill with the shorter (a cover)
	} else {
		w = sw * thumbSide / sh
	}
	xdraw.ApproxBiLinear.Scale(thumb, image.Rect((thumbSide-w)/2, (thumbSide-h)/2, (thumbSide-w)/2+w, (thumbSide-h)/2+h), src, sb, draw.Src, nil)
	return dst, thumb, nil
}
