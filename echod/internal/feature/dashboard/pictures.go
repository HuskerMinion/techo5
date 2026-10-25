//go:build !dot

package dashboard

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	_ "image/jpeg" // camera snapshots
	_ "image/png"  // pictures from /local
	"log/slog"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/picscale"
)

// Pictures on a drawn dashboard: a camera's latest snapshot, or a picture card's image. A camera is
// fetched again every few seconds while the page is up; an image, now and then.

const (
	cameraEvery  = 5 * time.Second
	pictureEvery = 10 * time.Minute
	// pictureRetry is how soon an image that would not load is tried again, twice as long each time it
	// fails again, up to pictureEvery: a Home Assistant restarting is back well before ten minutes.
	pictureRetry = 30 * time.Second
)

// wanted is one picture a dashboard shows: a camera entity, or an image's address.
type wanted struct {
	camera string
	url    string
	// wide is a picture kept as wide as the screen rather than a card: a picture-elements card's,
	// which may be the whole page.
	wide bool
}

// key is the picture as it is kept: the same image kept at a card's width and at the screen's is
// two pictures, so each is drawn from one of its own size.
func (w wanted) key() string {
	k := w.url
	if w.camera != "" {
		k = w.camera
	}
	if w.wide {
		k += " wide"
	}
	return k
}

// shot is a picture as it stands once fetched: decoded, or why it is not.
type shot struct {
	img image.Image
	// size is its own, read from its header: known for a picture too large to decode too, whose
	// elements are still placed on a frame of its shape.
	size     image.Point
	tooLarge bool
	failed   bool // it could not be fetched or read
}

// fetchPicture gets one picture, decoded.
func fetchPicture(ctx context.Context, w wanted) (shot, error) {
	var b []byte
	var err error
	if w.camera != "" {
		// Asked for at the size it is kept: a camera whose integration scales its snapshot then sends
		// a small one. Many ignore the width and send their own size, which pictureMost then bounds.
		b, err = hass.Get().Fetch("/api/camera_proxy/" + w.camera + "?width=" + strconv.Itoa(w.width()))
	} else {
		u := w.url
		if strings.HasPrefix(u, mediaSource) {
			// An image picked in the editor's media browser: Home Assistant says where it is, signed
			// for a short while, so it is asked again each time.
			rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			var r hass.Resolved
			r, err = hass.Get().ResolveMedia(rctx, u)
			cancel()
			if err != nil {
				return shot{failed: true}, err
			}
			u = r.URL
		}
		if strings.HasPrefix(u, "/") {
			b, err = hass.Get().Fetch(u)
		} else {
			b, err = hass.Get().FetchURL(u)
		}
	}
	if err != nil {
		return shot{failed: true}, err
	}
	return decodePicture(ctx, b, w)
}

// decodePicture is a fetched picture decoded and shrunk to as wide as it is kept, unless it would take
// more memory decoded than it may (most), or the session it is for has ended.
func decodePicture(ctx context.Context, b []byte, w wanted) (shot, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return shot{failed: true}, err
	}
	size := image.Pt(cfg.Width, cfg.Height)
	// In int64: on the 32-bit devices a picture's header can claim a size whose pixels overflow an int.
	need := int64(cfg.Width) * int64(cfg.Height) * bytesPerPixel(cfg.ColorModel)
	if cfg.Width <= 0 || cfg.Height <= 0 || need > w.most() {
		return shot{size: size, tooLarge: true}, errTooLarge
	}
	if err := ctx.Err(); err != nil {
		return shot{failed: true}, err // nobody is waiting for it: not worth the memory
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return shot{failed: true}, err
	}
	small := shrink(img, w.width())
	if w.wide && need > pictureMost {
		// The full-size picture is garbage now: handed back to the system at once, as the next may be as
		// large, rather than left for the collector while the device runs short.
		debug.FreeOSMemory()
	}
	return shot{img: small, size: size}, nil
}

// bytesPerPixel is how much memory a picture of model takes decoded, for each pixel: Go's decoders
// keep a 16-bit PNG at 8 bytes a pixel, a JPEG at up to 3 (YCbCr), a paletted or gray one at 1.
func bytesPerPixel(m color.Model) int64 {
	switch m {
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	case color.CMYKModel:
		return 4
	case color.YCbCrModel:
		return 3
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.GrayModel, color.AlphaModel:
		return 1
	}
	if _, ok := m.(color.Palette); ok {
		return 1
	}
	return 4
}

// mediaSource begins an image picked in Home Assistant's media browser, which it resolves.
const mediaSource = "media-source://"

// most is the most memory the picture may take decoded to be decoded at all, in bytes.
func (w wanted) most() int64 {
	if w.wide {
		return wideMost
	}
	return pictureMost
}

// width is as wide as the picture is kept.
func (w wanted) width() int {
	if w.wide {
		return screenWidth
	}
	return pictureWidth
}

// errTooLarge is a picture with more pixels than it may have (most), which is said on its card rather
// than left loading.
var errTooLarge = errors.New("dashboard: a picture too large to show")

const (
	// pictureMost is the most memory a picture may take decoded at all: 8 MB, a 1080p picture at 4
	// bytes a pixel, which the device can spare for a moment. A camera's comes already scaled
	// (fetchPicture).
	pictureMost = 8 << 20
	// wideMost is the most for a picture kept as wide as the screen, which may be a whole page drawn
	// as one picture, exported at a print's size: 64 MB decoded, 4096 by 4096 at 4 bytes a pixel and
	// less for a 16-bit PNG, at 8, and shrunk to the screen's width at once. Go's decoders can't scale
	// as they decode, so this is the whole picture for that moment, one picture at a time, on devices
	// with 1 GB.
	wideMost = 64 << 20
	// pictureWidth is as wide as a picture is kept: wider than a card on any of the screens, so it
	// is drawn by scaling down a little rather than decoded and scaled from full size every frame.
	pictureWidth = 640
)

// shrink is a picture at most most wide.
func shrink(img image.Image, most int) image.Image {
	b := img.Bounds()
	if b.Dx() <= most {
		return img
	}
	h := b.Dy() * most / b.Dx()
	out := image.NewRGBA(image.Rect(0, 0, most, max(h, 1)))
	picscale.CatmullRom.Scale(out, out.Rect, img, b, xdraw.Src)
	return out
}

// retryAfter is how long until w is fetched again: a camera every few seconds, an image every
// pictureEvery, and an image that failed sooner, pictureRetry and twice as long each time after, up to
// pictureEvery. retry keeps each image's wait since it last failed.
func retryAfter(w wanted, failed bool, retry map[string]time.Duration) time.Duration {
	if w.camera != "" {
		return cameraEvery
	}
	if !failed {
		delete(retry, w.key())
		return pictureEvery
	}
	wait := pictureRetry
	if last, ok := retry[w.key()]; ok {
		wait = min(2*last, pictureEvery)
	}
	retry[w.key()] = wait
	return wait
}

// keepPictures fetches the pictures until ctx ends, handing each one to got as it arrives, and a
// picture too large to show, or one that would not load, to got as that.
func keepPictures(ctx context.Context, list []wanted, got func(key string, s shot)) {
	if len(list) == 0 {
		return
	}
	next := map[string]time.Time{}
	retry := map[string]time.Duration{} // an image's wait after it last failed (retryAfter)
	for {
		for _, w := range list {
			if time.Now().Before(next[w.key()]) {
				continue
			}
			sh, err := fetchPicture(ctx, w)
			if ctx.Err() != nil {
				return
			}
			if err != nil && !errors.Is(err, errTooLarge) {
				slog.Debug("dashboard picture", "which", w.key(), "err", err)
			}
			next[w.key()] = time.Now().Add(retryAfter(w, sh.failed, retry))
			got(w.key(), sh)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// recent is the last few pictures the drawn page showed, kept between its views: a view opened
// again, a room and its pop-up in turn, draws its pictures at once rather than "Loading the
// picture…", and fetches them again behind that as ever. Few, as a picture as wide as the screen
// is 4 MB; and let go with the page, and when another dashboard is chosen (empty).
type recent struct {
	mu    sync.Mutex
	shots map[string]shot
	order []string // their keys, oldest first
	gen   int      // how many times it has been emptied: a picture for a page from before is not kept
}

const (
	recentMost  = 6
	recentBytes = 24 << 20
)

// put keeps sh as key's picture, the newest, for a page that began at gen. A picture that would not
// load is not kept: the next view asks for it afresh.
func (r *recent) put(gen int, key string, sh shot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen != r.gen || sh.failed {
		return
	}
	r.drop(key)
	if shotBytes(sh) > recentBytes {
		return
	}
	if r.shots == nil {
		r.shots = map[string]shot{}
	}
	r.shots[key] = sh
	r.order = append(r.order, key)
	for len(r.order) > recentMost || r.bytes() > recentBytes {
		r.drop(r.order[0])
	}
}

// get is key's picture, if it is kept, which makes it the newest.
func (r *recent) get(key string) (shot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sh, ok := r.shots[key]
	if ok {
		r.drop(key)
		r.shots[key] = sh
		r.order = append(r.order, key)
	}
	return sh, ok
}

// generation is the page's to put pictures with.
func (r *recent) generation() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen
}

// empty lets every picture go, and any still on its way for a page from before.
func (r *recent) empty() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shots, r.order = nil, nil
	r.gen++
}

// drop lets key's picture go. With r.mu held.
func (r *recent) drop(key string) {
	if _, ok := r.shots[key]; !ok {
		return
	}
	delete(r.shots, key)
	for i, k := range r.order {
		if k == key {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// bytes is about how much memory the pictures kept take, at 4 bytes a pixel. With r.mu held.
func (r *recent) bytes() int64 {
	var n int64
	for _, sh := range r.shots {
		n += shotBytes(sh)
	}
	return n
}

// shotBytes is about how much memory a picture takes, at 4 bytes a pixel: most are kept so (shrink).
func shotBytes(sh shot) int64 {
	if sh.img == nil {
		return 0
	}
	b := sh.img.Bounds()
	px := int64(b.Dx()) * int64(b.Dy())
	// What the decoded image really holds: a 16-bit PNG no wider than the screen is kept as decoded,
	// at 8 bytes a pixel, and counting it as 4 would let the cache hold twice its bound.
	switch sh.img.(type) {
	case *image.RGBA64, *image.NRGBA64:
		return px * 8
	case *image.Gray16:
		return px * 2
	case *image.Gray, *image.Alpha, *image.Paletted:
		return px
	}
	return px * 4 // RGBA and NRGBA; YCbCr holds less, so it is overcounted, on the safe side
}
