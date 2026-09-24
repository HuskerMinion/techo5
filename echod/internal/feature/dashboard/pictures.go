//go:build !dot

package dashboard

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg" // camera snapshots
	_ "image/png"  // pictures from /local
	"log/slog"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Pictures on a drawn dashboard: a camera's latest snapshot, or a picture card's image. A camera is
// fetched again every few seconds while the page is up; an image, now and then.

const (
	cameraEvery  = 5 * time.Second
	pictureEvery = 10 * time.Minute
)

// wanted is one picture a dashboard shows: a camera entity, or an image's address.
type wanted struct {
	camera string
	url    string
}

func (w wanted) key() string {
	if w.camera != "" {
		return w.camera
	}
	return w.url
}

// fetchPicture gets one picture, decoded.
func fetchPicture(w wanted) (image.Image, error) {
	var b []byte
	var err error
	if w.camera != "" {
		b, err = hass.Get().Fetch("/api/camera_proxy/" + w.camera)
	} else {
		u := w.url
		if strings.HasPrefix(u, "/") {
			b, err = hass.Get().Fetch(u)
		} else {
			b, err = hass.Get().FetchURL(u)
		}
	}
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > pictureMost {
		return nil, errors.New("dashboard: a picture too large to show")
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	return shrink(img), nil
}

const (
	// pictureMost is the most pixels a picture may have to be decoded at all: a 4K camera's.
	pictureMost = 3840 * 2160
	// pictureWidth is as wide as a picture is kept: wider than a card on any of the screens, so it
	// is drawn by scaling down a little rather than decoded and scaled from full size every frame.
	pictureWidth = 640
)

// shrink is a picture at most pictureWidth wide.
func shrink(img image.Image) image.Image {
	b := img.Bounds()
	if b.Dx() <= pictureWidth {
		return img
	}
	h := b.Dy() * pictureWidth / b.Dx()
	out := image.NewRGBA(image.Rect(0, 0, pictureWidth, max(h, 1)))
	xdraw.ApproxBiLinear.Scale(out, out.Rect, img, b, xdraw.Src, nil)
	return out
}

// keepPictures fetches the pictures until ctx ends, handing each one to got as it arrives.
func keepPictures(ctx context.Context, list []wanted, got func(key string, img image.Image)) {
	if len(list) == 0 {
		return
	}
	next := map[string]time.Time{}
	for {
		for _, w := range list {
			if time.Now().Before(next[w.key()]) {
				continue
			}
			every := pictureEvery
			if w.camera != "" {
				every = cameraEvery
			}
			next[w.key()] = time.Now().Add(every)
			img, err := fetchPicture(w)
			if err != nil {
				slog.Debug("dashboard picture", "which", w.key(), "err", err)
				continue
			}
			got(w.key(), img)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
