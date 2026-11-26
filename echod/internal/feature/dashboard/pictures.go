//go:build !dot

package dashboard

import (
	"bytes"
	"context"
	"errors"
	"hash/fnv"
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

	"github.com/HuskerMinion/techo5/echod/internal/lib/fastjpeg"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/picscale"
)

// Pictures on a drawn dashboard: a camera's latest snapshot, or a picture card's image. A camera is
// fetched again every few seconds while the page is up; an image, as its server's caching headers
// say (keepFor), and now and then when they say nothing.

const (
	cameraEvery  = 5 * time.Second
	pictureEvery = 10 * time.Minute
	// pictureRetry is how soon an image that would not load is tried again, twice as long each time it
	// fails again, up to pictureEvery: a Home Assistant restarting is back well before ten minutes.
	pictureRetry = 30 * time.Second
)

// pictureFloor is the soonest an image is fetched again, however often the page is redrawn and
// whatever its server says: an image at a fixed address that changes (a camera's snapshot, a picker's
// thumbnails, a player's cover) sent no-store is fetched on the page's next redraw, and a page redrawn
// a few times a second must not ask a server that often. A variable for the tests, as pictureTick.
var pictureFloor = 2 * time.Second

// pictureTick is how often the pictures are looked over to see which is due.
var pictureTick = time.Second

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
	// noStore is its server having said no-store: drawn on the page it was fetched for, and not kept
	// for the next one (recent), which fetches it afresh.
	noStore bool
}

// keeping is what an image's server said about keeping it when it was last fetched, which says when it
// is fetched again (keepFor) and how (fetchPicture).
type keeping struct {
	// url is the address fetched, which etag and lastModified are about: an image picked in the media
	// browser is resolved to a new signed address each time, and another address's validators are not
	// asked about.
	url                string
	etag, lastModified string
	noStore            bool
	// revalidate is no-cache, or a max-age of 0: kept, and asked about again on the page's next
	// redraw, with the validators when there are any.
	revalidate bool
	maxAge     time.Duration // a max-age above 0; 0 when the server gave none
	// sum is the bytes' hash: the same bytes sent again (a server that sends no validators) are not
	// decoded and drawn again.
	sum uint64
}

// keepingFrom is what f's headers say about keeping the image fetched from url.
func keepingFrom(url string, f hass.Fetched) keeping {
	k := keeping{url: url, etag: f.ETag, lastModified: f.LastModified}
	k.cacheControl(f.CacheControl)
	return k
}

// cacheControl reads a Cache-Control header into k: no-store, no-cache and max-age, the directives a
// private cache of one picture acts on. Anything else (private, public, must-revalidate, s-maxage, a
// directive of an extension) changes nothing here.
func (k *keeping) cacheControl(h string) {
	k.noStore, k.revalidate, k.maxAge = false, false, 0
	for _, d := range strings.Split(h, ",") {
		name, value, _ := strings.Cut(strings.TrimSpace(d), "=")
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "no-store":
			k.noStore = true
		case "no-cache":
			// With a field list too (no-cache="Set-Cookie"): read as the whole reply, the safe side.
			k.revalidate = true
		case "max-age":
			n, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(value), `"`), 10, 64)
			switch {
			case err != nil || n < 0:
				// Not a number: nothing said.
			case n == 0:
				k.revalidate = true
			default:
				k.maxAge = time.Duration(min(n, int64(pictureEvery/time.Second))) * time.Second
			}
		}
	}
}

// keepFor is how long an image fetched with k is kept before it is fetched again at the latest, and
// whether it is fetched again sooner, on the page's next redraw (no-store and no-cache), not before
// pictureFloor. A max-age is kept for, between pictureFloor and pictureEvery; an image whose server
// said nothing, pictureEvery, as ever.
func keepFor(k keeping) (time.Duration, bool) {
	switch {
	case k.noStore || k.revalidate:
		return pictureEvery, true
	case k.maxAge > 0:
		return min(max(k.maxAge, pictureFloor), pictureEvery), false
	}
	return pictureEvery, false
}

// fetchPicture gets one picture, decoded, with what its server said about keeping it. was is what it
// said the last time the picture was fetched and loaded (empty the first time): asked with its
// validators, a server that answers 304, or bytes the same as before, leave the picture as it is drawn
// (same), not decoded again.
func fetchPicture(ctx context.Context, w wanted, was keeping) (sh shot, k keeping, same bool, err error) {
	if w.camera != "" {
		// Asked for at the size it is kept: a camera whose integration scales its snapshot then sends
		// a small one. Many ignore the width and send their own size, which pictureMost then bounds.
		b, err := hass.Get().Fetch("/api/camera_proxy/" + w.camera + "?width=" + strconv.Itoa(w.width()))
		if err != nil {
			return shot{failed: true}, keeping{}, false, err
		}
		sh, err := decodePicture(ctx, b, w)
		return sh, keeping{}, false, err
	}
	u := w.url
	if strings.HasPrefix(u, mediaSource) {
		// An image picked in the editor's media browser: Home Assistant says where it is, signed
		// for a short while, so it is asked again each time.
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		r, err := hass.Get().ResolveMedia(rctx, u)
		cancel()
		if err != nil {
			return shot{failed: true}, keeping{}, false, err
		}
		u = r.URL
	}
	var etag, modified string
	if was.url == u {
		etag, modified = was.etag, was.lastModified
	}
	f, err := hass.Get().FetchFresh(ctx, u, etag, modified)
	if err != nil {
		return shot{failed: true}, keeping{}, false, err
	}
	if f.NotModified {
		// A 304 may say anew how long to keep it, and the validators to ask with next; what it leaves
		// out stands as the full reply said it.
		k = was
		if f.CacheControl != "" {
			k.cacheControl(f.CacheControl)
		}
		if f.ETag != "" {
			k.etag = f.ETag
		}
		if f.LastModified != "" {
			k.lastModified = f.LastModified
		}
		return shot{noStore: k.noStore}, k, true, nil
	}
	k = keepingFrom(u, f)
	h := fnv.New64a()
	_, _ = h.Write(f.Body)
	k.sum = h.Sum64()
	if was.sum != 0 && was.sum == k.sum {
		return shot{noStore: k.noStore}, k, true, nil
	}
	sh, err = decodePicture(ctx, f.Body, w)
	sh.noStore = k.noStore
	return sh, k, false, err
}

// decodePicture is a fetched picture decoded and shrunk to as wide as it is kept, unless it would take
// more memory decoded than it may (most), or the session it is for has ended.
func decodePicture(ctx context.Context, b []byte, w wanted) (shot, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
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
	var img image.Image
	if format == "jpeg" {
		// No narrower than it is kept, which is all shrink needs (lib/fastjpeg).
		img, err = fastjpeg.DecodeAtLeast(b, w.width(), 1)
	} else {
		img, _, err = image.Decode(bytes.NewReader(b))
	}
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

// plan is when one picture is fetched again.
type plan struct {
	at   time.Time // when it was last fetched
	next time.Time // when it is fetched again at the latest
	// onRedraw is its being fetched again on the page's next redraw after at, pictureFloor after at
	// at the soonest: an image its server said not to keep, or to ask about each time.
	onRedraw bool
	keep     keeping // what its server said, for the next fetch; empty after one that failed
}

// due is whether the picture is to be fetched now, the page having last been redrawn at redrawn.
func (p *plan) due(now, redrawn time.Time) bool {
	if !now.Before(p.next) {
		return true
	}
	return p.onRedraw && redrawn.After(p.at) && now.Sub(p.at) >= pictureFloor
}

// keepPictures fetches the pictures until ctx ends, handing each one to got as it arrives, and a
// picture too large to show, or one that would not load, to got as that. A picture fetched again and
// found the same is not handed over again: nothing on the page changed. redraws says the page was
// redrawn, which a picture its server said not to keep is fetched again on.
func keepPictures(ctx context.Context, list []wanted, redraws <-chan struct{}, got func(key string, s shot)) {
	if len(list) == 0 {
		return
	}
	plans := map[string]*plan{}
	retry := map[string]time.Duration{} // an image's wait after it last failed (retryAfter)
	var redrawn time.Time
	for {
		for _, w := range list {
			p := plans[w.key()]
			if p != nil && !p.due(time.Now(), redrawn) {
				continue
			}
			var was keeping
			if p != nil {
				was = p.keep
			}
			sh, k, same, err := fetchPicture(ctx, w, was)
			if ctx.Err() != nil {
				return
			}
			if err != nil && !errors.Is(err, errTooLarge) {
				slog.Debug("dashboard picture", "which", w.key(), "err", err)
			}
			np := &plan{at: time.Now()}
			wait := retryAfter(w, sh.failed, retry)
			if !sh.failed {
				np.keep = k
				if w.camera == "" {
					wait, np.onRedraw = keepFor(k)
				}
			}
			np.next = np.at.Add(wait)
			plans[w.key()] = np
			if !same {
				got(w.key(), sh)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-redraws:
			// Looked over at once: a picture due on this redraw whose floor has passed is fetched now.
			redrawn = time.Now()
		case <-time.After(pictureTick):
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
// load, or one its server said not to store, is not kept: the next view asks for it afresh.
func (r *recent) put(gen int, key string, sh shot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen != r.gen || sh.failed || sh.noStore {
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
