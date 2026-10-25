package home

import (
	"fmt"
	"image"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// screenSize is the screen's own size once a panel's screen has said it (SetPanelSize), nil until then
// and on every other device, where the sizes in the panel_*.go files are the screen. A TECHO5 Panel's
// canvas is whatever the app asked for, which is only known once the screen has opened, so a picture
// is sized by what is known when it is fetched rather than when the daemon started.
var screenSize atomic.Pointer[image.Point]

// sizedToScreen is w by h, or the screen's own size once it is known.
func sizedToScreen(w, h int) (int, int) {
	if p := screenSize.Load(); p != nil {
		return p.X, p.Y
	}
	return w, h
}

// slideshowSize is what a slideshow photo is fitted to.
func slideshowSize() (w, h int) { return sizedToScreen(slideshowW, slideshowH) }

// artSize is what a now-playing background picture is fitted to.
func artSize() (w, h int) { return sizedToScreen(artW, artH) }

// radarSize is what the rain map is composited at.
func radarSize() (w, h int) { return sizedToScreen(radarW, radarH) }

// cameraFrameSize is what a camera frame is scaled to fit.
func cameraFrameSize() (w, h int) { return sizedToScreen(cameraFrameW, cameraFrameH) }

// screenSized is the screen saying it is w by h. What is up already was made at the size assumed
// before, so the photo up is fetched again at this one and the rain map is made again when next
// looked at; a picture that stays (a now-playing cover) is scaled to the screen where it is drawn until
// the next one comes.
func (f *Feature) screenSized(w, h int) {
	ow, oh := slideshowSize()
	p := image.Pt(w, h)
	screenSize.Store(&p)
	if ow == w && oh == h {
		return
	}
	slog.Info("home: pictures sized to the screen", "size", fmt.Sprintf("%dx%d", w, h), "was", fmt.Sprintf("%dx%d", ow, oh))

	r := &f.radar
	r.mu.Lock()
	r.base, r.made, r.view.Frames = nil, nil, nil
	r.fetched = time.Time{}
	r.gen++ // a fetch under way is at the old size: fetch again once it is done
	r.mu.Unlock()

	f.mu.Lock()
	ids := f.slideshow.shown
	f.mu.Unlock()
	if s := config.Get().Home.Slideshow; len(ids) > 0 && f.SlideshowMode() != "" && !s.Art {
		go f.refetchShown(ids, s.WholePhoto)
	}
	f.Changed.Emit(struct{}{})
}
