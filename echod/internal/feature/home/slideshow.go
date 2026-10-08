package home

import (
	"context"
	"image"
	"image/draw"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/url"
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

// slideshowEvery is how long one photo stays up before the next is fetched when nothing is set, and
// slideshowEveryMin/Max bound what may be.
const (
	slideshowEvery    = time.Minute
	slideshowEveryMin = 5 * time.Second
	slideshowEveryMax = time.Hour
)

// slideshowInterval is how long a photo stays up, as configured.
func slideshowInterval(h config.Slideshow) time.Duration {
	if h.EverySeconds <= 0 {
		return slideshowEvery
	}
	return min(max(time.Duration(h.EverySeconds)*time.Second, slideshowEveryMin), slideshowEveryMax)
}

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
	shown string      // image's id, to draw it again when Show whole photo is turned on or off

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
	f.slideshowFolderTxt = &esphome.TextSensor{
		Base: esphome.Base{
			ObjectID: "slideshow_folder",
			Name:     "Slideshow folder",
			Icon:     "mdi:folder-image",
			Category: esphome.CategoryDiagnostic,
		},
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
	f.slideshowArtSw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "slideshow_weather_art",
			Name:     "Weather art",
			Icon:     "mdi:image-filter-hdr",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) { f.SetSlideshowArt(on) },
	}
	f.slideshowWholeSw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "slideshow_whole_photo",
			Name:     "Slideshow whole photo",
			Icon:     "mdi:fit-to-screen-outline",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) { f.SetSlideshowWholePhoto(on) },
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
	f.slideshowEveryNum = &esphome.Number{
		Base: esphome.Base{
			ObjectID: "slideshow_interval",
			Name:     "Slideshow time per photo",
			Icon:     "mdi:timer-outline",
			Category: esphome.CategoryConfig,
		},
		Min: float64(slideshowEveryMin / time.Second), Max: float64(slideshowEveryMax / time.Second), Step: 5, Unit: "s",
		Mode: esphome.NumberBox,
	}
	f.slideshowEveryNum.OnCommand = func(v float32) {
		f.slideshowEveryNum.Set(v)
		s := config.Get().Home.Slideshow
		s.EverySeconds = int(v)
		if err := config.Set().Home().Slideshow(s); err != nil {
			slog.Warn("home: saving the slideshow interval failed", "err", err)
		}
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

// SetSlideshowArt shows weather art in place of the photos, or the photos again. Turned on with the
// slideshow off, it comes on as the background, since weather art that is never shown is no answer.
func (f *Feature) SetSlideshowArt(on bool) {
	f.changeSlideshow(func(s *config.Slideshow) {
		s.Art = on
		if on && s.Mode != config.SlideshowBackground && s.Mode != config.SlideshowScreensaver {
			s.Mode = config.SlideshowBackground
		}
	})
	f.slideshowArtSw.Set(on)
	f.slideshowSel.Set(slideshowLabelFor(config.Get().Home.Slideshow.Mode)) // it may have come on with it
}

// SlideshowArt is whether the slideshow shows weather art rather than photos.
func (f *Feature) SlideshowArt() bool { return config.Get().Home.Slideshow.Art }

// SetSlideshowSubfolders includes the photos in the source's subfolders, or only its own.
func (f *Feature) SetSlideshowSubfolders(on bool) {
	f.changeSlideshow(func(s *config.Slideshow) { s.TopOnly = !on })
	f.slideshowSubfoldersSw.Set(on)
}

// SetSlideshowWholePhoto shows each photo whole, with blurred sides, or cropped to fill the screen.
// The photo up now is drawn again the new way at once; which photos are shown, and their order, stay.
func (f *Feature) SetSlideshowWholePhoto(on bool) {
	s := config.Get().Home.Slideshow
	f.slideshowWholeSw.Set(on)
	if s.WholePhoto == on {
		return
	}
	s.WholePhoto = on
	if err := config.Set().Home().Slideshow(s); err != nil {
		slog.Warn("home: saving the slideshow whole photo setting failed", "err", err)
		return
	}
	f.mu.Lock()
	id := f.slideshow.shown
	f.mu.Unlock()
	if id == "" || f.SlideshowMode() == "" || s.Art {
		return // nothing up to draw again; the next photo is fetched the new way
	}
	go func() {
		img, err := fetchSlideshowImage(id, on)
		if err != nil {
			slog.Warn("home: slideshow photo", "id", id, "err", err)
			return
		}
		f.mu.Lock()
		if f.slideshow.shown != id || config.Get().Home.Slideshow.WholePhoto != on {
			f.mu.Unlock()
			return // moved on, or changed back, while this one was fetched
		}
		f.slideshow.prev, f.slideshow.image = f.slideshow.image, img
		f.slideshow.at = time.Now() // the time per photo starts again too: it is a new look at it
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
	}()
}

// SlideshowWholePhoto is whether photos are shown whole rather than cropped to fill the screen.
func (f *Feature) SlideshowWholePhoto() bool { return config.Get().Home.Slideshow.WholePhoto }

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
		img, id := f.slideshow.image, f.slideshow.shown
		f.slideshow = slideshowState{image: img, prev: img, shown: id, at: time.Now().Add(-slideshowEveryMax)} // due now
	}
	f.mu.Unlock()
	f.slideshowFolderTxt.Set(slideshowFolderName(next.Source))
	f.Changed.Emit(struct{}{})
}

// SlideshowSettings is what the slideshow shows from: the source, and whether it shuffles and
// includes subfolders.
func (f *Feature) SlideshowSettings() (source string, shuffle, subfolders bool) {
	s := config.Get().Home.Slideshow
	return s.Source, !s.InOrder, !s.TopOnly
}

// SlideshowEvery is how long a photo stays up, and SetSlideshowEvery changes it; both are in play on
// the screen's Time per photo row as well as Home Assistant's number.
func (f *Feature) SlideshowEvery() time.Duration {
	return slideshowInterval(config.Get().Home.Slideshow)
}

func (f *Feature) SetSlideshowEvery(d time.Duration) {
	d = min(max(d, slideshowEveryMin), slideshowEveryMax)
	f.changeSlideshow(func(s *config.Slideshow) { s.EverySeconds = int(d / time.Second) })
	f.slideshowEveryNum.Set(float32(d / time.Second))
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
	if (h.Mode != config.SlideshowBackground && h.Mode != config.SlideshowScreensaver) || h.Source == "" || h.Art ||
		(h.Source != LocalPhotos && !hass.Get().Ready()) {
		return
	}
	f.mu.Lock()
	due := f.slideshow.image == nil || time.Since(f.slideshow.at) >= slideshowInterval(h)
	f.mu.Unlock()
	if !due {
		return
	}
	for try := 0; try < slideshowRetries; try++ {
		id, ok := f.nextSlideshowChild(h)
		if !ok {
			return // nothing to show, or the source failed to browse
		}
		// Show whole photo turned on or off while the photo was fetched: fetched again the new way,
		// or it would stay up the old way for its whole time.
		whole := h.WholePhoto
		for redo := 0; ; redo++ {
			img, err := fetchSlideshowImage(id, whole)
			if err != nil {
				slog.Warn("home: slideshow photo", "id", id, "err", err)
				break
			}
			f.mu.Lock()
			if now := config.Get().Home.Slideshow.WholePhoto; now != whole && redo < 2 {
				f.mu.Unlock()
				whole = now
				continue
			}
			f.slideshow.prev, f.slideshow.image, f.slideshow.at = f.slideshow.image, img, time.Now()
			f.slideshow.shown = id
			f.mu.Unlock()
			f.Changed.Emit(struct{}{})
			return
		}
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
// moved or unshared is visible instead of being retried quietly forever.
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
	if mode != config.SlideshowBackground && mode != config.SlideshowScreensaver || config.Get().Home.Slideshow.Art {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.slideshow.trouble
}

// slideshowFolderName is the chosen source as Home Assistant should show it: the last folder of the
// media source id, which is what the screen's Photo folder row names too.
func slideshowFolderName(id string) string {
	if id == "" {
		return "None chosen"
	}
	if id == LocalPhotos {
		return "On this device"
	}
	rest := strings.TrimRight(strings.TrimPrefix(id, "media-source://"), "/")
	name := rest[strings.LastIndex(rest, "/")+1:]
	if u, err := url.PathUnescape(name); err == nil {
		name = u
	}
	if name == "" || name == "media_source" {
		return "All photos"
	}
	return name
}

// gatherSlideshow lists the photos a slideshow shows: the source's own and, unless TopOnly, those of
// every folder under it, over one connection and within slideshowMaxFolders and slideshowMaxPhotos.
func gatherSlideshow(h config.Slideshow) ([]hass.Media, error) {
	if h.Source == LocalPhotos {
		return localPhotos(), nil
	}
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

// isPhoto is whether a playable entry is a picture the device can show, going by the type Home
// Assistant gives it, or failing that its name. Only JPEG and PNG: those are the decoders built in,
// and a GIF or a WebP would only be fetched to be skipped.
func isPhoto(m hass.Media) bool {
	k := strings.ToLower(m.Kind)
	switch k {
	case "image/jpeg", "image/jpg", "image/png":
		return true
	}
	if strings.HasPrefix(k, "image") {
		return false
	}
	if k != "" && k != "application/octet-stream" {
		return false
	}
	switch strings.ToLower(path.Ext(m.ID)) {
	case ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}

// fetchSlideshowImage resolves, fetches and fits one photo to the panel: cropped to fill it, or whole
// with blurred sides.
func fetchSlideshowImage(id string, whole bool) (*image.RGBA, error) {
	var b []byte
	var err error
	if strings.HasPrefix(id, localPhotoPrefix) {
		b, err = localPhoto(id)
	} else {
		var r hass.Resolved
		r, err = hass.Get().ResolveMedia(context.Background(), id)
		if err == nil {
			b, err = hass.Get().FetchURL(r.URL)
		}
	}
	if err != nil {
		return nil, err
	}
	src, err := decodeWithin(b, maxPhotoPixels, "slideshow photo "+id)
	if err != nil {
		return nil, err
	}
	src = upright(src, exifOrientation(b))
	if whole {
		return fitWhole(src, slideshowW, slideshowH), nil
	}
	return cropToFill(src, slideshowW, slideshowH), nil
}

// wholeShade is how much of the blurred sides' brightness is kept, out of 256: dark enough that the
// photo itself stands out, light enough that the sides still read as part of it.
const wholeShade = 140

// fitWhole scales src to fit inside w×h, all of it showing, centered. What it leaves uncovered is the
// same photo cropped to fill, blurred and darkened, the way a photo frame shows a tall picture on a
// wide screen; no black bars. A photo already the screen's shape is just that photo. On a round
// screen (the Spot's) the photo fits inside the circle, its corners on it, or they would be cut off.
func fitWhole(src image.Image, w, h int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 || w <= 0 || h <= 0 {
		return cropToFill(src, w, h)
	}
	scale := min(float64(w)/float64(sw), float64(h)/float64(sh))
	if roundPanel {
		scale = min(scale, float64(min(w, h))/math.Hypot(float64(sw), float64(sh)))
	}
	tw, th := int(float64(sw)*scale), int(float64(sh)*scale)
	if !roundPanel && tw >= w-2 && th >= h-2 {
		return cropToFill(src, w, h) // within a pixel or two: nothing worth a border
	}
	dst := blurredCover(src, w, h, wholeShade)
	target := image.Rect((w-tw)/2, (h-th)/2, (w-tw)/2+tw, (h-th)/2+th)
	xdraw.ApproxBiLinear.Scale(dst, target, src, sb, draw.Over, nil)
	return dst
}

// blurBlock is the size, in pixels, of the squares blurredCover averages a picture down to before it
// is stretched back up: large enough that no detail survives, so the sides read as color and light.
// blurSamples is how many pixels a side each square averages.
const (
	blurBlock   = 24
	blurSamples = 4
)

// blurredCover is src cropped to cover w×h, blurred, and darkened to shade/256, always opaque. It is
// worked out at a small size and only then stretched to w×h, so a photo costs no full-size copy more
// than the one returned.
func blurredCover(src image.Image, w, h, shade int) *image.RGBA {
	sw, sh := (w+blurBlock-1)/blurBlock, (h+blurBlock-1)/blurBlock
	mid := cropToFill(src, sw*blurSamples, sh*blurSamples)
	small := image.NewRGBA(image.Rect(0, 0, sw, sh))
	const n = blurSamples * blurSamples
	for by := 0; by < sh; by++ {
		for bx := 0; bx < sw; bx++ {
			var r, g, bl int
			for y := by * blurSamples; y < (by+1)*blurSamples; y++ {
				i := mid.PixOffset(bx*blurSamples, y)
				for x := 0; x < blurSamples; x++ {
					r, g, bl = r+int(mid.Pix[i]), g+int(mid.Pix[i+1]), bl+int(mid.Pix[i+2])
					i += 4
				}
			}
			o := small.PixOffset(bx, by)
			small.Pix[o] = byte(r / n * shade / 256)
			small.Pix[o+1] = byte(g / n * shade / 256)
			small.Pix[o+2] = byte(bl / n * shade / 256)
			small.Pix[o+3] = 255
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.BiLinear.Scale(out, out.Rect, small, small.Rect, draw.Src, nil)
	return out
}

// cropToFill scales src to cover w×h exactly, cropping whichever side runs long — the same rule
// the now-playing background already uses for cover art (meta.go's fetchArt). What a PNG leaves
// see-through is black: the result is always opaque, which crossfade counts on.
func cropToFill(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Rect, image.Black, image.Point{}, draw.Src)
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
	xdraw.ApproxBiLinear.Scale(dst, target, src, sb, draw.Over, nil)
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
	if config.Get().Home.Slideshow.Mode != config.SlideshowBackground || config.Get().Home.Slideshow.Art {
		return nil
	}
	return f.currentSlideshowPhoto()
}

// SlideshowScreensaverPhoto is the current photo for Screensaver mode, nil off that mode.
func (f *Feature) SlideshowScreensaverPhoto() *image.RGBA {
	if config.Get().Home.Slideshow.Mode != config.SlideshowScreensaver || config.Get().Home.Slideshow.Art {
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
// opaque (cropToFill's and fitWhole's draw.Src), so a plain per-byte lerp needs no bounds or alpha handling.
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
