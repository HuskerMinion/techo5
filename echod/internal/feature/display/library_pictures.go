//go:build !dot && !spot

package display

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/png" // Music Assistant's thumbnails
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // a service's own covers, as the image proxy passes them on

	"github.com/HuskerMinion/techo5/echod/internal/lib/fastjpeg"
	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/picscale"
)

// The music library's pictures: the covers of albums and playlists and the pictures of artists, from
// Music Assistant's image proxy. Each is fetched once, in the background, scaled to the square it is
// drawn in and kept so; a frame draws what is kept and a placeholder for the rest, and never waits for
// one. A few hundred are kept, the ones drawn last, which is a few pages of each tab.

const (
	// libPicsMost is how many pictures are kept, and libPicsBytes how much memory they may take: a
	// cover in a grid on the Show 8 is about 230 pixels square, 210 KB.
	libPicsMost  = 300
	libPicsBytes = 32 << 20
	// libPicsAtOnce is how many pictures are fetched at once, and libPicsWaiting how many may wait
	// for their turn: a finger swiping through pages asks for every page's pictures, and the ones of
	// the pages it went past are asked for again if it comes back.
	libPicsAtOnce  = 3
	libPicsWaiting = 48
	// libPicRetry is how long a picture that would not load is left before it is asked for again.
	libPicRetry = 2 * time.Minute
	// libPicMost is the most memory a picture may take decoded: the proxy sends the original when it
	// will not scale it, and an original cover can be 3000 pixels square.
	libPicMost = 40 << 20
)

// libPictures is the kept pictures, the newest at the front, and the ones on their way.
type libPictures struct {
	mu      sync.Mutex
	kept    map[string]*list.Element // of *libPic
	order   list.List
	bytes   int64
	asked   map[string]bool      // on their way
	failed  map[string]time.Time // would not load, at
	slots   chan struct{}
	changed func() // a picture arrived: the frame is drawn again
}

type libPic struct {
	key string
	img *image.RGBA
}

// libPics are the library's pictures, for every frame: one screen, one set.
var libPics = &libPictures{}

// libFetch gets a picture's bytes and the status they came with; a variable so that a test can serve
// pictures without a server.
var libFetch = func(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := libPicClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return b, resp.StatusCode, err
}

// libPicClient fetches covers, through the network guard as the device's other picture fetches are:
// a picture's address comes from Music Assistant or a music service, and must not reach the device
// itself or its other addresses.
var libPicClient = &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
	DialContext:         netguard.Dialer(10 * time.Second).DialContext,
	TLSHandshakeTimeout: 10 * time.Second,
	MaxIdleConnsPerHost: 3,
	IdleConnTimeout:     30 * time.Second,
}}

// picture is u's picture side pixels square, round for an artist, if it is kept; otherwise nil, and it
// is fetched. It is kept at the size and in the shape it is drawn, so a frame only copies it.
func (p *libPictures) picture(u string, side int, round bool) *image.RGBA {
	if u == "" || side <= 0 {
		return nil
	}
	key := libPicKey(u, side, round)
	p.mu.Lock()
	defer p.mu.Unlock()
	if el, ok := p.kept[key]; ok {
		p.order.MoveToFront(el)
		return el.Value.(*libPic).img
	}
	if p.asked[key] || len(p.asked) >= libPicsWaiting {
		return nil
	}
	if at, ok := p.failed[key]; ok && time.Since(at) < libPicRetry {
		return nil
	}
	if p.asked == nil {
		p.asked, p.failed, p.kept = map[string]bool{}, map[string]time.Time{}, map[string]*list.Element{}
		p.slots = make(chan struct{}, libPicsAtOnce)
	}
	p.asked[key] = true
	go p.fetch(key, u, side, round)
	return nil
}

// fetch gets one picture, scales it and keeps it.
func (p *libPictures) fetch(key, u string, side int, round bool) {
	p.slots <- struct{}{}
	img, err := loadLibPicture(u, side, round)
	<-p.slots
	p.mu.Lock()
	delete(p.asked, key)
	if err != nil {
		p.failed[key] = time.Now()
		p.mu.Unlock()
		slog.Debug("music library picture", "host", picHost(u), "err", err)
		return
	}
	delete(p.failed, key)
	if _, ok := p.kept[key]; !ok {
		p.kept[key] = p.order.PushFront(&libPic{key: key, img: img})
		p.bytes += int64(len(img.Pix))
	}
	for p.order.Len() > libPicsMost || (p.bytes > libPicsBytes && p.order.Len() > 1) {
		old := p.order.Back()
		pic := old.Value.(*libPic)
		p.order.Remove(old)
		delete(p.kept, pic.key)
		p.bytes -= int64(len(pic.img.Pix))
	}
	changed := p.changed
	p.mu.Unlock()
	if changed != nil {
		changed()
	}
}

// keep puts a picture in as if it had been fetched, for a test's preview.
func (p *libPictures) keep(u string, side int, round bool, img *image.RGBA) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.kept == nil {
		p.asked, p.failed, p.kept = map[string]bool{}, map[string]time.Time{}, map[string]*list.Element{}
		p.slots = make(chan struct{}, libPicsAtOnce)
	}
	key := libPicKey(u, side, round)
	p.kept[key] = p.order.PushFront(&libPic{key: key, img: img})
	p.bytes += int64(len(img.Pix))
}

// loadLibPicture fetches u and makes it side pixels square. The proxy answers a size it does not make
// with 400, so that is asked once more without one, for the original.
func loadLibPicture(u string, side int, round bool) (*image.RGBA, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b, status, err := libFetch(ctx, u)
	if err == nil && status == http.StatusBadRequest {
		if plain, ok := withoutSize(u); ok {
			b, status, err = libFetch(ctx, plain)
		}
	}
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("the picture's server answered %d", status)
	}
	return squarePicture(b, side, round)
}

// withoutSize is u without its size query, and whether it had one.
func withoutSize(u string) (string, bool) {
	p, err := url.Parse(u)
	if err != nil {
		return "", false
	}
	q := p.Query()
	if !q.Has("size") {
		return "", false
	}
	q.Del("size")
	p.RawQuery = q.Encode()
	return p.String(), true
}

var errLibPicLarge = errors.New("a picture too large to show")

// libPicKey is a picture as it is kept: the same cover at two sizes, or square and round, is two.
func libPicKey(u string, side int, round bool) string {
	if round {
		return fmt.Sprintf("%d round %s", side, u)
	}
	return fmt.Sprintf("%d %s", side, u)
}

// squarePicture decodes a picture and scales its middle square to side pixels square, with rounded
// corners that the card shows through, or as a circle when round.
func squarePicture(b []byte, side int, round bool) (*image.RGBA, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height)*4 > libPicMost {
		return nil, errLibPicLarge
	}
	var img image.Image
	if format == "jpeg" {
		img, err = fastjpeg.DecodeAtLeast(b, side, side)
	} else {
		img, _, err = image.Decode(bytes.NewReader(b))
	}
	if err != nil {
		return nil, err
	}
	sb := img.Bounds()
	n := min(sb.Dx(), sb.Dy())
	src := image.Rect(0, 0, n, n).Add(sb.Min).Add(image.Pt((sb.Dx()-n)/2, (sb.Dy()-n)/2))
	out := image.NewRGBA(image.Rect(0, 0, side, side))
	picscale.CatmullRom.Scale(out, out.Rect, img, src, xdraw.Src)
	rad := max(side/14, 2)
	if round {
		rad = side / 2
	}
	roundCorners(out, rad)
	return out, nil
}

// roundCorners makes img's corners outside a radius of rad clear, their edge softened over a pixel so
// that a circle is round rather than stepped. The picture is premultiplied, so clearing is scaling.
func roundCorners(img *image.RGBA, rad int) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	rr := float64(rad)
	for dy := 0; dy < rad; dy++ {
		for dx := 0; dx < rad; dx++ {
			fx, fy := rr-float64(dx)-0.5, rr-float64(dy)-0.5
			cover := min(max(rr-math.Sqrt(fx*fx+fy*fy)+0.5, 0), 1)
			if cover >= 1 {
				continue
			}
			for _, pt := range []image.Point{{dx, dy}, {w - 1 - dx, dy}, {dx, h - 1 - dy}, {w - 1 - dx, h - 1 - dy}} {
				i := img.PixOffset(pt.X, pt.Y)
				for k := i; k < i+4; k++ {
					img.Pix[k] = uint8(float64(img.Pix[k])*cover + 0.5)
				}
			}
		}
	}
}

// drawLibPicture draws a kept picture at b, over what is there.
func (r *paint) drawLibPicture(img *image.RGBA, b image.Rectangle) {
	draw.Draw(r.dst, b, img, image.Point{}, draw.Over)
}

// setChanged is what is called when a picture arrives: the display drawing a frame again.
func (p *libPictures) setChanged(f func()) {
	p.mu.Lock()
	p.changed = f
	p.mu.Unlock()
}

// picHost is the host of a picture's address, for the log: a service's address can carry a signed
// query that is nobody's business.
func picHost(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Host
	}
	return ""
}
