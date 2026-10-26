//go:build !dot && !spot

package display

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // camera snapshots
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/picscale"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// A camera on the Tessera page: a tile the layout shows the camera's picture on (display live) shows its
// latest snapshot, as Tessera's own screens do, fetched from Home Assistant with the device's token every
// few seconds (the layout's refresh, if it gives one) while the tile is drawn, and scaled once to the tile
// (filling it, or the whole picture with bars: fit contain), so a frame only copies it. A camera no frame
// has drawn for a while is let go. A tap on the tile puts the camera up on its own (tessTap).
//
// A camera the camera page plays live over RTSP (home.LiveRTSP: Live camera video on, and go2rtc's
// stream or the setup page's picture address for it) plays live in its tile too, decoded straight to
// the tile's size and shape at a few frames a second (video.OpenCameraRTSPFilling). Snapshots fill in
// until its first frame, and for a while after a stream that failed.

const (
	tessCamEvery = 3 * time.Second  // between one snapshot of a camera and the next, unless the layout says
	tessCamIdle  = 10 * time.Second // undrawn this long, and its snapshots stop
	tessCamRetry = 15 * time.Second // after a snapshot that would not come

	tessLiveStart = 20 * time.Second // the most a live picture's first frame may take
	tessLiveStall = 10 * time.Second // the most a live picture may go without a frame once started
	tessLiveRetry = time.Minute      // after a live picture that failed, snapshots this long
)

// tessLiveSlots is how many tiles may play live at once: each is a decoder of its own, and a page of live
// cameras would take a Show 5's every core. The rest show snapshots, and try again after tessLiveRetry.
var tessLiveSlots = make(chan struct{}, 2)

var errLiveBusy = errors.New("as many cameras play live as may")

// tessCams are the cameras the Tessera page draws, by entity and fit (tessCamKey).
var tessCams = &tessCamSet{cams: map[string]*tessCam{}}

type tessCamSet struct {
	mu   sync.Mutex
	cams map[string]*tessCam
	wake func() // a new snapshot to draw
}

type tessCam struct {
	entity string
	whole  bool          // the whole picture, with bars, rather than the tile filled
	every  time.Duration // between snapshots
	size   image.Point   // the tile's, as last drawn
	shot   *image.RGBA   // the latest snapshot, at size; nil until the first
	drawn  time.Time     // when a frame last drew it
}

// fetchTessCam is a camera's snapshot from Home Assistant, about width wide; a variable for the tests.
var fetchTessCam = func(ctx context.Context, entity string, width int) ([]byte, error) {
	return hass.Get().FetchURLContext(ctx, "/api/camera_proxy/"+entity+"?width="+strconv.Itoa(width))
}

// tessLive is a camera's live picture being decoded, a frame at a time (video.Camera).
type tessLive interface {
	Next(wait time.Duration) (*image.RGBA, error)
	Close()
}

// tessLiveAddr is where a camera's live picture is read from, and openTessLive starts one filling w×h;
// variables for the tests.
var (
	tessLiveAddr = home.LiveRTSP
	openTessLive = func(ctx context.Context, addr, user, pass string, w, h int, whole bool) (tessLive, error) {
		if whole {
			return video.OpenCameraRTSPFitting(ctx, addr, user, pass, w, h)
		}
		return video.OpenCameraRTSPFilling(ctx, addr, user, pass, w, h)
	}
)

// tessCamKey is a camera's place in tessCams: a camera on two tiles, one filled and one whole, is two.
func tessCamKey(entity string, whole bool) string {
	if whole {
		return entity + " whole"
	}
	return entity
}

// frame is entity's latest picture for a tile size wide and high, the whole of it or filling the tile,
// nil until there is one; the first ask starts its pictures coming, a snapshot each every (the layout's
// refresh; zero for tessCamEvery) when it has no live picture.
func (s *tessCamSet) frame(entity string, size image.Point, whole bool, every time.Duration) *image.RGBA {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tessCamKey(entity, whole)
	c, ok := s.cams[key]
	if !ok {
		c = &tessCam{entity: entity, whole: whole}
		s.cams[key] = c
		safe.Go("tessera camera", func() { s.keep(key) })
	}
	c.drawn = time.Now()
	c.every = every
	if c.size != size {
		c.size = size // the next snapshot comes at the new size; this one is drawn as it is till then
	}
	if c.shot == nil || c.shot.Rect.Size() != size {
		return nil
	}
	return c.shot
}

// keep fetches a camera's snapshots while its tile is drawn, or plays its live picture where it has one.
func (s *tessCamSet) keep(key string) {
	defer func() {
		s.mu.Lock()
		delete(s.cams, key)
		s.mu.Unlock()
	}()
	var liveFailed time.Time
	for {
		s.mu.Lock()
		c := s.cams[key]
		entity, whole := c.entity, c.whole
		size, idle, shown := c.size, time.Since(c.drawn) > tessCamIdle, c.shot != nil
		every := cmp.Or(c.every, tessCamEvery)
		s.mu.Unlock()
		if idle {
			return
		}
		// The first snapshot before a live picture: it is there in a second, the stream's first frame in
		// several.
		if addr, user, pass, ok := tessLiveAddr(entity); ok && shown && size.X > 0 && size.Y > 0 &&
			time.Since(liveFailed) > tessLiveRetry {
			if err := s.live(key, addr, user, pass, size, whole); errors.Is(err, errLiveBusy) {
				slog.Debug("tessera camera: snapshots while other tiles play live", "entity", entity)
				liveFailed = time.Now()
			} else if err != nil {
				slog.Info("tessera camera: live picture stopped, snapshots for now", "entity", entity, "err", err)
				liveFailed = time.Now()
			}
			continue
		}
		wait := every
		if shot, err := s.snapshot(entity, size, whole); err != nil {
			slog.Debug("tessera camera: no snapshot", "entity", entity, "err", err)
			wait = tessCamRetry
		} else {
			s.mu.Lock()
			c.shot = shot
			wake := s.wake
			s.mu.Unlock()
			if wake != nil {
				wake()
			}
		}
		time.Sleep(wait)
	}
}

// live plays a camera's live picture from addr into its tile, while the tile is drawn at size. It ends nil
// when the tile went undrawn or changed size, else with why the picture stopped.
func (s *tessCamSet) live(key, addr, user, pass string, size image.Point, whole bool) error {
	select {
	case tessLiveSlots <- struct{}{}:
		defer func() { <-tessLiveSlots }()
	default:
		return errLiveBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The decoder's sizes are even: a tile an odd pixel wider or higher is decoded that pixel over, and
	// its last column or row left off.
	cam, err := openTessLive(ctx, addr, user, pass, (size.X+1)&^1, (size.Y+1)&^1, whole)
	if err != nil {
		return err
	}
	defer cam.Close()
	for wait := tessLiveStart; ; wait = tessLiveStall {
		f, err := cam.Next(wait)
		if err != nil {
			return err
		}
		// A picture of its own each frame, not the decoder's, which the next frame is read into while this
		// one may still be being drawn.
		shot := image.NewRGBA(image.Rectangle{Max: size})
		for y := range size.Y {
			copy(shot.Pix[y*shot.Stride:y*shot.Stride+size.X*4], f.Pix[y*f.Stride:])
		}
		s.mu.Lock()
		c := s.cams[key]
		done := c.size != size || time.Since(c.drawn) > tessCamIdle
		if !done {
			c.shot = shot
		}
		wake := s.wake
		s.mu.Unlock()
		if done {
			return nil
		}
		if wake != nil {
			wake()
		}
	}
}

// snapshot is entity's picture now, cut to the tile's shape and scaled to it, or the whole of it scaled
// into the tile with black bars.
func (s *tessCamSet) snapshot(entity string, size image.Point, whole bool) (*image.RGBA, error) {
	if size.X <= 0 || size.Y <= 0 {
		return nil, errNoTile
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := fetchTessCam(ctx, entity, size.X)
	if err != nil {
		return nil, err
	}
	// One at a time, as the camera grid's: a camera's picture can be 4K, tens of megabytes decoded.
	tessDecoding <- struct{}{}
	defer func() { <-tessDecoding }()
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	dst := image.NewRGBA(image.Rectangle{Max: size})
	if whole {
		draw.Draw(dst, dst.Rect, image.NewUniform(color.RGBA{0, 0, 0, 255}), image.Point{}, draw.Src)
		picscale.CatmullRom.Scale(dst, contain(src.Bounds(), dst.Rect), src, src.Bounds(), xdraw.Src)
		return dst, nil
	}
	picscale.CatmullRom.Scale(dst, dst.Rect, src, cover(src.Bounds(), dst.Rect), xdraw.Src)
	return dst, nil
}

var errNoTile = errors.New("the tile has no size yet")

// contain is the part of dst that src, scaled to fit inside it with its shape kept, fills, in its middle.
func contain(src, dst image.Rectangle) image.Rectangle {
	w, h := dst.Dx(), src.Dy()*dst.Dx()/max(src.Dx(), 1)
	if h > dst.Dy() {
		w, h = src.Dx()*dst.Dy()/max(src.Dy(), 1), dst.Dy()
	}
	x, y := dst.Min.X+(dst.Dx()-w)/2, dst.Min.Y+(dst.Dy()-h)/2
	return image.Rect(x, y, x+w, y+h)
}

// roundPicture rounds the corners of a picture that fills b, as the tiles beside it are rounded: what lies
// outside the curve goes back to the page's ground.
func (r *paint) roundPicture(b image.Rectangle, rad float64, ground color.RGBA) {
	x0, y0, x1, y1 := rectF(b)
	corner := min(int(math.Ceil(rad))+1, b.Dx()/2, b.Dy()/2)
	for _, ys := range [2][2]int{{b.Min.Y, b.Min.Y + corner}, {b.Max.Y - corner, b.Max.Y}} {
		for y := ys[0]; y < ys[1]; y++ {
			for _, xs := range [2][2]int{{b.Min.X, b.Min.X + corner}, {b.Max.X - corner, b.Max.X}} {
				for x := xs[0]; x < xs[1]; x++ {
					if out := 1 - clamp01(0.5-rrDist(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1, rad)); out > 0 {
						r.blendAt(x, y, ground, out)
					}
				}
			}
		}
	}
}

// How a camera's picture is darkened under its name, as Tessera's app prepares it for its own screens
// (tile_art.py): from clear to fadeDepth of black over the bottom fadeShare of the tile.
const (
	fadeShare = 0.42
	fadeDepth = 150
)

// fadeFoot darkens the bottom of the picture in b for the name written on it.
func fadeFoot(dst *image.RGBA, b image.Rectangle) {
	h := max(1, int(math.Round(float64(b.Dy())*fadeShare)))
	for i := range h {
		a := uint8((i + 1) * fadeDepth / h)
		y := b.Max.Y - h + i
		washOver(dst, image.Rect(b.Min.X, y, b.Max.X, y+1), color.RGBA{0, 0, 0, a})
	}
}

// tessDecoding lets one Tessera snapshot at a time be decoded and scaled.
var tessDecoding = make(chan struct{}, 1)
