package home

import (
	"bytes"
	"context"
	"image"
	"image/draw"
	"log/slog"
	"math/rand/v2"
	"path"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The idle photo slideshow. Background mode shows photos from a Home Assistant media source
// behind the ordinary idle page — the clock, date and weather stay exactly as they are, in front.
// Screensaver mode takes the whole screen after an idle wait, with its own, simpler clock/date
// overlay (off, small, or normal size) — no weather, no timers; those belong to the ordinary idle
// page, not the photo-frame look. The device only ever asks Home Assistant for the next photo: it
// neither knows nor cares which backend (Immich, a network share, anything else Home Assistant can
// browse) a source came from. See docs/slideshow-plan.md; Show and Spot only (see screen.go).

// slideshowEvery is how long one photo stays up before the next is fetched.
const slideshowEvery = time.Minute

// slideshowFade is how long a new photo takes to cross-fade in over the last one; SlideshowFrame is
// how often the display should redraw while it does, for a smooth blend rather than a jump cut.
const (
	slideshowFade  = 900 * time.Millisecond
	SlideshowFrame = 80 * time.Millisecond
)

// slideshowMaxFolders and slideshowMaxPhotos bound how much of a library one look gathers, so
// picking the top of a very large one still starts in reasonable time and memory.
const (
	slideshowMaxFolders = 1000
	slideshowMaxPhotos  = 20000
)

// slideshowRetries is how many children are tried, in one advance, before giving up for this
// round: a library will have the odd broken or unreachable file, and that shouldn't stall the
// whole slideshow.
const slideshowRetries = 5

// browseEvery is how long a browsed child list is trusted before Home Assistant is asked again.
const browseEvery = time.Hour

// slideshowGiveUp is how many looks in a row may come back with nothing before the screen says so
// rather than the device going on trying quietly, and slideshowWaitAfter is how long it then leaves
// between looks: a folder that was renamed or unshared is not going to be there thirty seconds
// later, and a large library is expensive to walk.
const (
	slideshowGiveUp    = 3
	slideshowWaitAfter = 15 * time.Minute
)

// The lines the screen shows while the slideshow has no photo. Short: they sit in the footer.
const (
	slideshowUnreadable = "photos: folder unreadable"
	slideshowEmpty      = "photos: folder is empty"
)

// slideshowIdleDefault is how long Screensaver mode waits for when IdleMinutes is unset.
const slideshowIdleDefault = 5 * time.Minute

// The mode select's options, as shown; slideshowModeFor and slideshowLabelFor translate to and
// from config.Slideshow's stored value.
const (
	slideshowOff              = "Off"
	slideshowBackgroundLabel  = "Background"
	slideshowScreensaverLabel = "Screensaver"
)

// The overlay select's options.
const (
	slideshowOverlayOffLabel    = "Off"
	slideshowOverlaySmallLabel  = "Small"
	slideshowOverlayNormalLabel = "Normal"
)

type slideshowState struct {
	children []hass.Media // the source's playable children, last browsed
	browsed  time.Time
	idx      int

	image *image.RGBA
	prev  *image.RGBA // what was showing before image, faded out while at is within slideshowFade
	at    time.Time   // when image took over, for both slideshowEvery and the fade

	// trouble is why there is no photo, for the screen to show; empty while all is well. fails
	// counts the looks that came back with nothing in a row, and waitUntil holds the next look off
	// once there have been slideshowGiveUp of them.
	trouble   string
	fails     int
	waitUntil time.Time
}

func (f *Feature) buildSlideshowSelect() {
	f.slideshowSel = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "slideshow_mode",
			Name:     "Slideshow",
			Icon:     "mdi:image-multiple",
			Category: esphome.CategoryConfig,
		},
		Options:   []string{slideshowOff, slideshowBackgroundLabel, slideshowScreensaverLabel},
		OnCommand: func(v string) { f.ChooseSlideshowMode(slideshowModeFor(v)) },
	}
	f.slideshowOverlaySel = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "slideshow_screensaver_overlay",
			Name:     "Screensaver clock",
			Icon:     "mdi:clock-outline",
			Category: esphome.CategoryConfig,
		},
		Options:   []string{slideshowOverlayOffLabel, slideshowOverlaySmallLabel, slideshowOverlayNormalLabel},
		OnCommand: func(v string) { f.ChooseSlideshowOverlay(slideshowOverlayFor(v)) },
	}
	f.slideshowIdleNum = &esphome.Number{
		Base: esphome.Base{
			ObjectID: "slideshow_screensaver_idle",
			Name:     "Screensaver idle wait",
			Icon:     "mdi:timer-sand",
			Category: esphome.CategoryConfig,
		},
		Min: 1, Max: 60, Step: 1, Unit: "min",
		Mode: esphome.NumberBox,
	}
	f.slideshowShuffleSw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "slideshow_shuffle",
			Name:     "Slideshow shuffle",
			Icon:     "mdi:shuffle-variant",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) { f.SetSlideshowShuffle(on) },
	}
	f.slideshowSubfoldersSw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "slideshow_subfolders",
			Name:     "Slideshow includes subfolders",
			Icon:     "mdi:folder-multiple-image",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) { f.SetSlideshowSubfolders(on) },
	}
	f.slideshowIdleNum.OnCommand = func(v float32) {
		f.slideshowIdleNum.Set(v)
		s := config.Get().Home.Slideshow
		s.IdleMinutes = int(v)
		if err := config.Set().Home().Slideshow(s); err != nil {
			slog.Warn("home: saving the slideshow idle wait failed", "err", err)
		}
	}
}

func slideshowModeFor(v string) string {
	switch v {
	case slideshowBackgroundLabel:
		return config.SlideshowBackground
	case slideshowScreensaverLabel:
		return config.SlideshowScreensaver
	}
	return ""
}

func slideshowLabelFor(mode string) string {
	switch mode {
	case config.SlideshowBackground:
		return slideshowBackgroundLabel
	case config.SlideshowScreensaver:
		return slideshowScreensaverLabel
	}
	return slideshowOff
}

func slideshowOverlayFor(v string) string {
	switch v {
	case slideshowOverlayOffLabel:
		return config.SlideshowOverlayOff
	case slideshowOverlaySmallLabel:
		return config.SlideshowOverlaySmall
	}
	return ""
}

func slideshowOverlayLabelFor(overlay string) string {
	switch overlay {
	case config.SlideshowOverlayOff:
		return slideshowOverlayOffLabel
	case config.SlideshowOverlaySmall:
		return slideshowOverlaySmallLabel
	}
	return slideshowOverlayNormalLabel
}

// ChooseSlideshowMode sets the display mode.
func (f *Feature) ChooseSlideshowMode(mode string) {
	s := config.Get().Home.Slideshow
	if s.Mode == mode {
		return
	}
	s.Mode = mode
	if err := config.Set().Home().Slideshow(s); err != nil {
		slog.Warn("home: saving the slideshow mode failed", "err", err)
		return
	}
	slog.Info("home: slideshow mode", "mode", slideshowLabelFor(mode))
	f.slideshowSel.Set(slideshowLabelFor(mode))
	f.Changed.Emit(struct{}{})
}

// ChooseSlideshowOverlay sets the screensaver's clock/date overlay size.
func (f *Feature) ChooseSlideshowOverlay(overlay string) {
	s := config.Get().Home.Slideshow
	if s.Overlay == overlay {
		return
	}
	s.Overlay = overlay
	if err := config.Set().Home().Slideshow(s); err != nil {
		slog.Warn("home: saving the slideshow overlay failed", "err", err)
		return
	}
	slog.Info("home: slideshow overlay", "overlay", slideshowOverlayLabelFor(overlay))
	f.slideshowOverlaySel.Set(slideshowOverlayLabelFor(overlay))
	f.Changed.Emit(struct{}{})
}

// SetSlideshowSource sets which media source to show photos from, from the home_slideshow action or
// the screen's folder list.
func (f *Feature) SetSlideshowSource(source string) {
	f.changeSlideshow(func(s *config.Slideshow) { s.Source = source })
	slog.Info("home: slideshow source", "source", source)
}

// SetSlideshowShuffle shows the photos shuffled, or in the source's own order.
func (f *Feature) SetSlideshowShuffle(on bool) {
	f.changeSlideshow(func(s *config.Slideshow) { s.InOrder = !on })
	f.slideshowShuffleSw.Set(on)
}

// SetSlideshowSubfolders includes the photos in the source's subfolders, or only its own.
func (f *Feature) SetSlideshowSubfolders(on bool) {
	f.changeSlideshow(func(s *config.Slideshow) { s.TopOnly = !on })
	f.slideshowSubfoldersSw.Set(on)
}

// changeSlideshow saves a change to what the slideshow shows and has the photos gathered again.
func (f *Feature) changeSlideshow(change func(*config.Slideshow)) {
	cur := config.Get().Home.Slideshow
	next := cur
	change(&next)
	if next == cur {
		return
	}
	if err := config.Set().Home().Slideshow(next); err != nil {
		slog.Warn("home: saving the slideshow failed", "err", err)
		return
	}
	f.mu.Lock()
	if next.Source == "" {
		f.slideshow = slideshowState{} // nothing to show: the last photo goes too
	} else {
		shown := f.slideshow.image
		f.slideshow = slideshowState{image: shown, prev: shown, at: time.Now().Add(-slideshowEvery)} // due now
	}
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// SlideshowSettings is what the slideshow shows from: the source, and whether it shuffles and
// includes subfolders.
func (f *Feature) SlideshowSettings() (source string, shuffle, subfolders bool) {
	s := config.Get().Home.Slideshow
	return s.Source, !s.InOrder, !s.TopOnly
}

// BrowseFolder lists one media source folder, for the screen's folder list.
func (f *Feature) BrowseFolder(ctx context.Context, id string) (hass.Media, error) {
	return hass.Get().Browse(ctx, id)
}

// slideshowLoop advances the slideshow while Background mode is on. Run from Feature.Run.
func (f *Feature) slideshowLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		f.advanceSlideshow()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// advanceSlideshow fetches the next photo when the current one has been up long enough, or there
// isn't one yet. Nothing to do with the slideshow off, without a source, or before Home Assistant
// access is set.
func (f *Feature) advanceSlideshow() {
	h := config.Get().Home.Slideshow
	if (h.Mode != config.SlideshowBackground && h.Mode != config.SlideshowScreensaver) || h.Source == "" || !hass.Get().Ready() {
		return
	}
	f.mu.Lock()
	due := f.slideshow.image == nil || time.Since(f.slideshow.at) >= slideshowEvery
	f.mu.Unlock()
	if !due {
		return
	}
	for try := 0; try < slideshowRetries; try++ {
		id, ok := f.nextSlideshowChild(h)
		if !ok {
			return // nothing to show, or the source failed to browse
		}
		img, err := fetchSlideshowImage(id)
		if err != nil {
			slog.Warn("home: slideshow photo", "id", id, "err", err)
			continue
		}
		f.mu.Lock()
		f.slideshow.prev, f.slideshow.image, f.slideshow.at = f.slideshow.image, img, time.Now()
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
		return
	}
	slog.Warn("home: slideshow", "err", "no photo fetched after retries")
}

// nextSlideshowChild is the next photo's id to try, gathering the source again when the list is
// stale or empty. A shuffled list is shuffled afresh each time round.
func (f *Feature) nextSlideshowChild(h config.Slideshow) (string, bool) {
	f.mu.Lock()
	stale := time.Since(f.slideshow.browsed) > browseEvery || len(f.slideshow.children) == 0
	waiting := stale && time.Now().Before(f.slideshow.waitUntil)
	f.mu.Unlock()
	if waiting {
		return "", false // given up for now; the screen is saying why
	}
	if stale {
		photos, err := gatherSlideshow(h)
		if err != nil {
			slog.Warn("home: browsing the slideshow source", "source", h.Source, "err", err)
			f.slideshowNothing(slideshowUnreadable)
			return "", false
		}
		if len(photos) == 0 {
			slog.Warn("home: slideshow photos", "source", h.Source, "count", 0, "subfolders", !h.TopOnly)
			f.slideshowNothing(slideshowEmpty)
			return "", false
		}
		if !h.InOrder {
			rand.Shuffle(len(photos), func(i, j int) { photos[i], photos[j] = photos[j], photos[i] })
		}
		slog.Info("home: slideshow photos", "count", len(photos), "subfolders", !h.TopOnly, "shuffled", !h.InOrder)
		f.mu.Lock()
		f.slideshow.children, f.slideshow.browsed, f.slideshow.idx = photos, time.Now(), 0
		f.slideshow.trouble, f.slideshow.fails, f.slideshow.waitUntil = "", 0, time.Time{}
		f.mu.Unlock()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.slideshow.children)
	if n == 0 {
		return "", false
	}
	if f.slideshow.idx >= n {
		f.slideshow.idx = 0
		if !h.InOrder {
			c := f.slideshow.children
			rand.Shuffle(n, func(i, j int) { c[i], c[j] = c[j], c[i] })
		}
	}
	id := f.slideshow.children[f.slideshow.idx].ID
	f.slideshow.idx++
	return id, true
}

// slideshowNothing records a look that came back with nothing. After slideshowGiveUp of them in a
// row the device stops looking for a while and the screen says why, so a folder that was renamed,
// moved or unshared is visible instead of being retried quietly for ever.
func (f *Feature) slideshowNothing(why string) {
	f.mu.Lock()
	f.slideshow.fails++
	give := f.slideshow.fails >= slideshowGiveUp
	if give {
		f.slideshow.trouble, f.slideshow.waitUntil = why, time.Now().Add(slideshowWaitAfter)
	}
	f.mu.Unlock()
	if give {
		slog.Warn("home: slideshow", "err", why, "waiting", slideshowWaitAfter)
		f.Changed.Emit(struct{}{})
	}
}

// SlideshowTrouble is the line the screen shows when the slideshow has given up looking: why there
// is no photo. Empty while the slideshow is off, working, or still trying.
func (f *Feature) SlideshowTrouble() string {
	mode := config.Get().Home.Slideshow.Mode
	if mode != config.SlideshowBackground && mode != config.SlideshowScreensaver {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.slideshow.trouble
}

// gatherSlideshow lists the photos a slideshow shows: the source's own and, unless TopOnly, those of
// every folder under it, over one connection and within slideshowMaxFolders and slideshowMaxPhotos.
func gatherSlideshow(h config.Slideshow) ([]hass.Media, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	folders := slideshowMaxFolders
	if h.TopOnly {
		folders = 1
	}
	var photos []hass.Media
	err := hass.Get().BrowseTree(ctx, h.Source, folders, func(m hass.Media) bool {
		for _, c := range m.Children {
			if c.CanPlay && isPhoto(c) {
				photos = append(photos, c)
			}
		}
		return len(photos) < slideshowMaxPhotos
	})
	return photos, err
}

// isPhoto is whether a playable entry is a picture rather than a video or a sound, going by the
// type Home Assistant gives it, or failing that its name.
func isPhoto(m hass.Media) bool {
	k := strings.ToLower(m.Kind)
	if strings.HasPrefix(k, "image") {
		return true
	}
	if k != "" && k != "application/octet-stream" {
		return false
	}
	switch strings.ToLower(path.Ext(m.ID)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}

// fetchSlideshowImage resolves, fetches and crops one photo to the panel, full-bleed.
func fetchSlideshowImage(id string) (*image.RGBA, error) {
	r, err := hass.Get().ResolveMedia(context.Background(), id)
	if err != nil {
		return nil, err
	}
	b, err := hass.Get().FetchURL(r.URL)
	if err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	return cropToFill(upright(src, exifOrientation(b)), slideshowW, slideshowH), nil
}

// cropToFill scales src to cover w×h exactly, cropping whichever side runs long — the same rule
// the now-playing background already uses for cover art (meta.go's fetchArt).
func cropToFill(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 {
		return dst
	}
	tw, th := w, sh*w/sw
	if th < h {
		tw, th = sw*h/sh, h
	}
	target := image.Rect((w-tw)/2, (h-th)/2, (w-tw)/2+tw, (h-th)/2+th)
	xdraw.ApproxBiLinear.Scale(dst, target, src, sb, draw.Src, nil)
	return dst
}

// SlideshowMode is the configured display mode: config.SlideshowBackground,
// config.SlideshowScreensaver, or empty for off.
func (f *Feature) SlideshowMode() string { return config.Get().Home.Slideshow.Mode }

// SlideshowOverlay is the screensaver's clock/date overlay: config.SlideshowOverlayOff,
// config.SlideshowOverlaySmall, or empty for the normal, full-size clock.
func (f *Feature) SlideshowOverlay() string { return config.Get().Home.Slideshow.Overlay }

// SlideshowIdleTimeout is how long Screensaver mode waits for.
func (f *Feature) SlideshowIdleTimeout() time.Duration {
	if m := config.Get().Home.Slideshow.IdleMinutes; m > 0 {
		return time.Duration(m) * time.Minute
	}
	return slideshowIdleDefault
}

// SlideshowBackground is the current photo for Background mode, nil off that mode.
func (f *Feature) SlideshowBackground() *image.RGBA {
	if config.Get().Home.Slideshow.Mode != config.SlideshowBackground {
		return nil
	}
	return f.currentSlideshowPhoto()
}

// SlideshowScreensaverPhoto is the current photo for Screensaver mode, nil off that mode.
func (f *Feature) SlideshowScreensaverPhoto() *image.RGBA {
	if config.Get().Home.Slideshow.Mode != config.SlideshowScreensaver {
		return nil
	}
	return f.currentSlideshowPhoto()
}

// currentSlideshowPhoto is the photo either mode shows, cross-fading from the last one for
// slideshowFade after a change; nil when nothing has been fetched yet.
func (f *Feature) currentSlideshowPhoto() *image.RGBA {
	f.mu.Lock()
	img, prev, at := f.slideshow.image, f.slideshow.prev, f.slideshow.at
	f.mu.Unlock()
	if img == nil {
		return nil
	}
	elapsed := time.Since(at)
	if prev == nil || elapsed >= slideshowFade {
		return img
	}
	return crossfade(prev, img, float64(elapsed)/float64(slideshowFade))
}

// SlideshowTransitioning is whether a fade is under way, so the display redraws faster than the
// usual once-a-second idle pace while it plays.
func (f *Feature) SlideshowTransitioning() bool {
	mode := config.Get().Home.Slideshow.Mode
	if mode != config.SlideshowBackground && mode != config.SlideshowScreensaver {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.slideshow.prev != nil && time.Since(f.slideshow.at) < slideshowFade
}

// crossfade blends a into b, t running 0 (all a) to 1 (all b). Both are always artW×artH and fully
// opaque (cropToFill's draw.Src), so a plain per-byte lerp needs no bounds or alpha handling.
func crossfade(a, b *image.RGBA, t float64) *image.RGBA {
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	out := image.NewRGBA(a.Rect)
	for i := range out.Pix {
		out.Pix[i] = byte(float64(a.Pix[i])*(1-t) + float64(b.Pix[i])*t)
	}
	return out
}

// slideshowAction wires the media source, from Home Assistant, the same way the cameras list is
// wired by home_cameras.
func (f *Feature) slideshowAction() *esphome.Action {
	return &esphome.Action{
		Name: "home_slideshow",
		Args: []esphome.Arg{{Name: "source", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			f.SetSlideshowSource(c.String("source"))
			return nil, nil
		},
	}
}
