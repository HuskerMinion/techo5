package home

import (
	"context"
	"errors"
	"image"
	"image/draw"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/picscale"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The idle photo slideshow. Background mode shows photos from a Home Assistant media source
// behind the ordinary idle page — the clock, date and weather stay exactly as they are, in front.
// Screensaver mode takes the whole screen after an idle wait, with its own, simpler clock/date
// overlay (off, small, or normal size) — no weather, no timers; those belong to the ordinary idle
// page, not the photo-frame look. Photos come from Home Assistant's media library, which does not say
// which backend (Immich, a network share, anything else it can browse) a source came from; from the
// device's own photos (photos.go); or straight from an Immich server with no Home Assistant at all
// (immich.go). See docs/slideshow-plan.md; Show and Spot only (see screen.go).

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
	shown []string    // image's ids, two for a pair, to draw it again when Show whole photo is turned on or off

	// ahead is photos to show before the list moves on: ones looked at for a pair's second half and
	// found wide, shown in their own turn rather than skipped.
	ahead []string
	// captions are when and where the photos up were taken, for the screen to write over them.
	captions []PhotoCaption

	// trouble is why there is no photo, for the screen to show; empty while all is well. fails
	// counts the looks that came back with nothing in a row, and waitUntil holds the next look off
	// once there have been slideshowGiveUp of them.
	trouble   string
	fails     int
	waitUntil time.Time

	// rush is how far a re-read of the source (rereadSlideshow) has cut the photo's time short:
	// rushAsked from the re-read until the look it made due shows a photo, rushShown while that photo is
	// up, and rushNone once a photo takes over at its own time.
	rush int
}

// The states of slideshowState.rush.
const (
	rushNone = iota
	rushAsked
	rushShown
)

// PhotoCaption is when and where a photo on the screen was taken (Slideshow.Caption), for the screen to
// write over it: a photo alone, or one half of a pair. Either of When and Place may be missing.
type PhotoCaption struct {
	When  time.Time
	Place string
	// Right is the right-hand photo of a pair; Pair is whether there is a pair at all.
	Right, Pair bool
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
	f.slideshowPairsSw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "slideshow_pairs",
			Name:     "Slideshow photo pairs",
			Icon:     "mdi:view-split-vertical",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) { f.SetSlideshowPairs(on) },
	}
	f.slideshowCaptionSw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "slideshow_caption",
			Name:     "Slideshow date and place",
			Icon:     "mdi:map-marker-outline",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) { f.SetSlideshowCaption(on) },
	}
	f.immichFavoritesSw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "immich_favorites",
			Name:     "Immich favorites only",
			Icon:     "mdi:heart-outline",
			Category: esphome.CategoryConfig,
		},
		OnCommand: func(on bool) { f.SetImmichFavorites(on) },
	}
	f.immichPersonTxt = &esphome.Text{
		Base: esphome.Base{
			ObjectID: "immich_person",
			Name:     "Immich person",
			Icon:     "mdi:account-outline",
			Category: esphome.CategoryConfig,
		},
		MaxLength: immichNameMax,
		OnCommand: func(v string) { f.SetImmichPerson(v) },
	}
	day := func(id, name string, set func(string) error) *esphome.Text {
		t := &esphome.Text{
			Base:      esphome.Base{ObjectID: id, Name: name, Icon: "mdi:calendar-range", Category: esphome.CategoryConfig},
			MaxLength: uint32(len(config.PhotoDay)),
			Pattern:   `^(\d{4}-\d{2}-\d{2})?$`,
		}
		t.OnCommand = func(v string) {
			if err := set(v); err != nil {
				slog.Warn("home: an Immich day not kept", "err", err)
			}
		}
		return t
	}
	f.immichFromTxt = day("immich_from", "Immich first day", func(v string) error { return f.SetImmichDays(v, config.Get().Home.Slideshow.ImmichTo) })
	f.immichToTxt = day("immich_to", "Immich last day", func(v string) error { return f.SetImmichDays(config.Get().Home.Slideshow.ImmichFrom, v) })
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
// the screen's folder list. The source already showing is read again (rereadSlideshow) rather than
// left as it is, which is what a folder whose photos have changed wants (#180).
func (f *Feature) SetSlideshowSource(source string) {
	if source != "" && source == config.Get().Home.Slideshow.Source {
		f.rereadSlideshow()
		slog.Info("home: slideshow source read again", "source", source)
		return
	}
	if id, ok := strings.CutPrefix(source, ImmichAlbumPrefix); ok {
		// The album's name is kept with its id, to name it without asking the server. Not known yet (the
		// list not fetched since a restart, or the album set from Home Assistant), the name of the album
		// before is not kept for this one: it is asked for, and the album is "Immich album" meanwhile.
		name, known := immichNames.name(id)
		i := config.Get().Home.Immich
		i.Album, i.AlbumID = name, id
		if err := config.Set().Home().Immich(i); err != nil {
			slog.Warn("home: saving the Immich album's name failed", "err", err)
		}
		if !known && i.Set() {
			safe.Go("immich album name", func() { f.nameImmichAlbum(id) })
		}
	}
	f.changeSlideshow(func(s *config.Slideshow) { s.Source = source })
	slog.Info("home: slideshow source", "source", source)
}

// rereadSlideshow gathers the source's photos again on the slideshow's next look, and makes that look
// due now, for a folder something outside has just filled with new photos (#180). Until then the device
// trusts the list it has for browseEvery, so new photos took up to an hour to show, and setting the same
// source again changed nothing: the way round was to clear the source and set it again, which put the
// screen's photo out in between. It is forgetSlideshowList (photos.go, for a photo sent to the device)
// with the next look brought forward rather than left to the photo's time.
//
// Every kind of source the slideshow shows from goes the same way, since gathering is what reads each
// one: a Home Assistant media source folder (and its subfolders), Immich, and the device's own photos.
// The photo up stays up while the new list is gathered and its first photo fetched, and the new one
// takes over from it as any next photo does, so the screen is never dark between them. A gathering
// already under way is of the list before: the generation moves on, so what it finds is not kept.
//
// The photo's time is cut short once for a run of re-reads: a script that copies photos in one at a time
// and asks after each would otherwise change the photo every half minute. A re-read asked while the
// look it made due is still to come, or while the photo that look put up is up, still reads the list
// again, for that photo's next look, and leaves its time as it is. A slideshow in order keeps its place
// in the list read again (keepGathered), and the photos ahead still in it.
func (f *Feature) rereadSlideshow() {
	f.mu.Lock()
	f.slideshowGen++
	f.slideshow.browsed = time.Time{}
	// A fresh start, as a new source gets: a folder that was empty or unreadable may not be any more.
	f.slideshow.trouble, f.slideshow.fails, f.slideshow.waitUntil = "", 0, time.Time{}
	if f.slideshow.image != nil && f.slideshow.rush == rushNone {
		f.slideshow.at = time.Now().Add(-slideshowEveryMax) // due now
		f.slideshow.rush = rushAsked
	}
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// nameImmichAlbum asks Immich for its albums, to name the album id chosen without its name known.
func (f *Feature) nameImmichAlbum(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	srv := config.Get().Home.Immich
	if _, err := ImmichAlbums(ctx, srv); err != nil {
		return
	}
	name, ok := immichNames.name(id)
	i := config.Get().Home.Immich
	source, _, _ := f.SlideshowSettings()
	if !ok || source != ImmichAlbumPrefix+id || i.Server != srv.Server {
		return // since chosen otherwise, or another server
	}
	i.Album, i.AlbumID = name, id
	if err := config.Set().Home().Immich(i); err != nil {
		slog.Warn("home: saving the Immich album's name failed", "err", err)
	}
	f.Changed.Emit(struct{}{})
}

// nameAgainAfter is how much longer each try at naming an album at start waits than the last; a test
// shortens it. naming is those tries under way, for a test to wait on.
var (
	nameAgainAfter = time.Minute
	naming         sync.WaitGroup
)

// nameImmichAlbumSoon is nameImmichAlbum at start, tried a few times while the network comes up, and no
// more once it is named.
func (f *Feature) nameImmichAlbumSoon(id string) {
	named := func() bool {
		i := config.Get().Home.Immich
		return i.AlbumID == id && i.Album != ""
	}
	for try := 0; try < 5; try++ {
		time.Sleep(time.Duration(try) * nameAgainAfter)
		if named() {
			return
		}
		if source, _, _ := f.SlideshowSettings(); source != ImmichAlbumPrefix+id {
			return // since chosen otherwise
		}
		f.nameImmichAlbum(id)
		if named() {
			return
		}
	}
}

// keepGathered keeps photos gathered for the slideshow, unless its source has changed since the
// gathering began (gen): a gathering takes as long as five minutes.
//
// A slideshow in order (inOrder) carries on from the photo up when that photo is in the list gathered:
// the next is the one after it there, and the photos ahead (a pair's wide ones) that are still in it
// stay ahead. Read again for a folder that has had photos added (#180), or after browseEvery, it went
// back to the first photo every time. A photo up that is not in the new list, or a shuffled slideshow,
// starts the list from its beginning.
func (f *Feature) keepGathered(gen uint64, source string, photos []hass.Media, inOrder bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	// The source as well as the generation: the source is read before the generation is, and a change
	// in between would have the old source's photos kept under the new generation.
	if f.slideshowGen != gen || config.Get().Home.Slideshow.Source != source {
		return false
	}
	idx, ahead := 0, []string(nil)
	if n := len(f.slideshow.shown); inOrder && n > 0 {
		in := func(id string) int { return slices.IndexFunc(photos, func(m hass.Media) bool { return m.ID == id }) }
		if i := in(f.slideshow.shown[n-1]); i >= 0 {
			idx = i + 1
			for _, id := range f.slideshow.ahead {
				if in(id) >= 0 {
					ahead = append(ahead, id)
				}
			}
		}
	}
	f.slideshow.children, f.slideshow.browsed, f.slideshow.idx, f.slideshow.ahead = photos, time.Now(), idx, ahead
	f.slideshow.trouble, f.slideshow.fails, f.slideshow.waitUntil = "", 0, time.Time{}
	return true
}

// ResetImmich is the Immich server changed: photos listed from the one before are not this one's, so
// the list is gathered again, and an album of the one before is not this one's either, so the slideshow
// shows the whole library instead.
func (f *Feature) ResetImmich() {
	f.mu.Lock()
	f.slideshowGen++
	f.slideshow.children, f.slideshow.browsed, f.slideshow.idx, f.slideshow.ahead = nil, time.Time{}, 0, nil
	f.slideshow.trouble, f.slideshow.fails, f.slideshow.waitUntil = "", 0, time.Time{}
	f.mu.Unlock()
	if source, _, _ := f.SlideshowSettings(); strings.HasPrefix(source, ImmichAlbumPrefix) {
		f.SetSlideshowSource(ImmichAll)
	}
	f.Changed.Emit(struct{}{})
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
	ids := f.slideshow.shown
	f.mu.Unlock()
	if len(ids) == 0 || f.SlideshowMode() == "" || s.Art {
		return // nothing up to draw again; the next photo is fetched the new way
	}
	go f.refetchShown(ids, on)
}

// refetchShown fetches and fits the photos up again, whole or not, and puts them up in place of what
// is there unless the slideshow has moved on or the setting changed back meanwhile.
func (f *Feature) refetchShown(ids []string, whole bool) {
	img, err := fetchShown(ids, whole)
	if err != nil {
		slog.Warn("home: slideshow photo", "id", ids[0], "err", err)
		return
	}
	f.mu.Lock()
	if !slices.Equal(f.slideshow.shown, ids) || config.Get().Home.Slideshow.WholePhoto != whole ||
		img.Rect.Size() != image.Pt(slideshowSize()) {
		f.mu.Unlock()
		return // moved on, changed back, or the screen's size changed, while this one was fetched
	}
	f.slideshow.prev, f.slideshow.image = f.slideshow.image, img
	f.slideshow.at = time.Now() // the time per photo starts again too: it is a new look at it
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// SlideshowWholePhoto is whether photos are shown whole rather than cropped to fill the screen.
func (f *Feature) SlideshowWholePhoto() bool { return config.Get().Home.Slideshow.WholePhoto }

// PairsHere is whether this screen can show two photos side by side: not the Spot's round one.
func PairsHere() bool { return !roundPanel }

// SetSlideshowPairs shows two tall photos side by side, or each alone. The next photo is put up the
// new way.
func (f *Feature) SetSlideshowPairs(on bool) {
	f.changeSlideshow(func(s *config.Slideshow) { s.Pairs = on && PairsHere() })
	f.slideshowPairsSw.Set(config.Get().Home.Slideshow.Pairs)
}

// SlideshowPairs is whether tall photos are shown two at a time.
func (f *Feature) SlideshowPairs() bool { return config.Get().Home.Slideshow.Pairs && PairsHere() }

// SetSlideshowCaption writes when and where each Immich photo was taken over it, or not.
func (f *Feature) SetSlideshowCaption(on bool) {
	f.changeSlideshow(func(s *config.Slideshow) { s.Caption = on })
	f.slideshowCaptionSw.Set(on)
}

// SlideshowCaption is whether Immich photos say when and where they were taken.
func (f *Feature) SlideshowCaption() bool { return config.Get().Home.Slideshow.Caption }

// SetImmichFavorites shows only Immich's favorites, or every photo the source has.
func (f *Feature) SetImmichFavorites(on bool) {
	f.changeSlideshow(func(s *config.Slideshow) { s.ImmichFavorites = on })
	f.immichFavoritesSw.Set(on)
}

// ImmichFavorites is whether only Immich's favorites are shown.
func (f *Feature) ImmichFavorites() bool { return config.Get().Home.Slideshow.ImmichFavorites }

// immichNameMax bounds a person's name as the filter takes it.
const immichNameMax = 100

// SetImmichPerson shows only the photos of the person Immich knows by that name, or everybody's for
// an empty one.
func (f *Feature) SetImmichPerson(name string) {
	name = strings.TrimSpace(name)
	if len(name) > immichNameMax {
		name = name[:immichNameMax]
	}
	f.changeSlideshow(func(s *config.Slideshow) { s.ImmichPerson = name })
	f.immichPersonTxt.Set(name)
}

// SetImmichDays shows only the photos taken from the day from through the day to (config.PhotoDay);
// either empty leaves that end open. A day that is not one, or a last day before the first, is
// refused, and what was set stays.
func (f *Feature) SetImmichDays(from, to string) error {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	cur := config.Get().Home.Slideshow
	refuse := func(why string) error {
		f.immichFromTxt.Set(cur.ImmichFrom)
		f.immichToTxt.Set(cur.ImmichTo)
		return errors.New(why)
	}
	var a, b time.Time
	var err error
	if from != "" {
		if a, err = time.Parse(config.PhotoDay, from); err != nil {
			return refuse("the first day is not a date like 2024-06-30")
		}
	}
	if to != "" {
		if b, err = time.Parse(config.PhotoDay, to); err != nil {
			return refuse("the last day is not a date like 2024-06-30")
		}
	}
	if from != "" && to != "" && b.Before(a) {
		return refuse("the last day is before the first")
	}
	f.changeSlideshow(func(s *config.Slideshow) { s.ImmichFrom, s.ImmichTo = from, to })
	f.immichFromTxt.Set(from)
	f.immichToTxt.Set(to)
	return nil
}

// ImmichFilters is what narrows an Immich source: favorites only, the first and last day, a person.
func (f *Feature) ImmichFilters() (favorites bool, from, to, person string) {
	s := config.Get().Home.Slideshow
	return s.ImmichFavorites, s.ImmichFrom, s.ImmichTo, s.ImmichPerson
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
	f.slideshowGen++
	if next.Source == "" {
		f.slideshow = slideshowState{} // nothing to show: the last photo goes too
	} else {
		img, ids, caps := f.slideshow.image, f.slideshow.shown, f.slideshow.captions
		f.slideshow = slideshowState{image: img, prev: img, shown: ids, captions: caps, at: time.Now().Add(-slideshowEveryMax)} // due now
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

// mediaRoot is the top of Home Assistant's media library, where the screen's folder list starts.
const mediaRoot = "media-source://media_source"

// BrowseFolder lists one media source folder, for the screen's folder list. With an Immich server set
// up, the top of the list has an Immich folder of its own, holding its albums; with no Home Assistant
// to ask, it is all the list has.
func (f *Feature) BrowseFolder(ctx context.Context, id string) (hass.Media, error) {
	if IsImmich(id) {
		return browseImmich(ctx, id)
	}
	immich := id == mediaRoot && config.Get().Home.Immich.Set()
	if immich && !hass.Get().Ready() {
		return hass.Media{ID: mediaRoot, Title: "Photos", CanExpand: true,
			Children: []hass.Media{{ID: ImmichAll, Title: "Immich", CanExpand: true}}}, nil
	}
	m, err := hass.Get().Browse(ctx, id)
	if err == nil && immich {
		m.Children = append([]hass.Media{{ID: ImmichAll, Title: "Immich", CanExpand: true}}, m.Children...)
	}
	return m, err
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
		(h.Source != LocalPhotos && !IsImmich(h.Source) && !hass.Get().Ready()) {
		return
	}
	f.mu.Lock()
	due := f.slideshow.image == nil || time.Since(f.slideshow.at) >= slideshowInterval(h)
	f.mu.Unlock()
	if !due {
		return
	}
	var last error
	for try := 0; try < slideshowRetries; try++ {
		id, ok := f.nextSlideshowChild(h)
		if !ok {
			return // nothing to show, or the source failed to browse
		}
		src, err := fetchSlideshowSource(id)
		if err != nil {
			slog.Warn("home: slideshow photo", "id", id, "err", err)
			last = err
			continue
		}
		ids, srcs := []string{id}, []image.Image{src}
		if h.Pairs && PairsHere() && tall(src) {
			if id2, src2, ok := f.partner(h); ok {
				ids, srcs = append(ids, id2), append(srcs, src2)
			}
		}
		caps := slideshowCaptions(h, ids)
		// Show whole photo turned on or off while the photo was fetched: drawn again the new way, or it
		// would stay up the old way for its whole time.
		whole := h.WholePhoto
		for redo := 0; ; redo++ {
			img := fitShown(srcs, whole)
			f.mu.Lock()
			if now := config.Get().Home.Slideshow.WholePhoto; now != whole && redo < 2 {
				f.mu.Unlock()
				whole = now
				continue
			}
			f.slideshow.prev, f.slideshow.image, f.slideshow.at = f.slideshow.image, img, time.Now()
			f.slideshow.shown, f.slideshow.captions = ids, caps
			if f.slideshow.rush == rushAsked {
				f.slideshow.rush = rushShown // the photo a re-read brought forward gets its whole time
			} else {
				f.slideshow.rush = rushNone
			}
			f.slideshow.fails, f.slideshow.trouble = 0, ""
			f.mu.Unlock()
			f.Changed.Emit(struct{}{})
			return
		}
	}
	slog.Warn("home: slideshow", "err", "no photo fetched after retries")
	if fetchCounts(last) {
		// None of a look's photos could be fetched: the source is down or refusing, which the screen says
		// after a few such looks, as for one that cannot be listed. The list is gathered again then.
		f.mu.Lock()
		f.slideshow.browsed = time.Time{}
		f.mu.Unlock()
		f.slideshowNothing(slideshowUnreadable)
	}
}

// pairLooks is how many photos are looked at for a tall photo's partner before it is shown alone.
const pairLooks = 3

// partner is a second tall photo to show beside one: the next in the list that is. Wide ones looked
// at on the way are kept to show next (ahead), so pairing skips none.
func (f *Feature) partner(h config.Slideshow) (string, image.Image, bool) {
	var wide []string
	defer func() {
		if len(wide) > 0 {
			f.mu.Lock()
			f.slideshow.ahead = append(wide, f.slideshow.ahead...)
			f.mu.Unlock()
		}
	}()
	for look := 0; look < pairLooks; look++ {
		id, ok := f.nextSlideshowChild(h)
		if !ok {
			return "", nil, false
		}
		src, err := fetchSlideshowSource(id)
		if err != nil {
			slog.Warn("home: slideshow photo", "id", id, "err", err)
			continue // a broken one is let go, as it would be in its own turn
		}
		if tall(src) {
			return id, src, true
		}
		wide = append(wide, id)
	}
	return "", nil, false
}

// tall is whether a photo is taller than it is wide: one of a pair.
func tall(img image.Image) bool {
	b := img.Bounds()
	return b.Dy() > b.Dx()
}

// slideshowCaptions asks Immich when and where each photo up was taken, when captions are on and there
// is a screensaver to show them. A photo it cannot say for has none; the other of a pair still does.
func slideshowCaptions(h config.Slideshow, ids []string) []PhotoCaption {
	if !h.Caption || h.Mode != config.SlideshowScreensaver {
		return nil
	}
	var out []PhotoCaption
	for i, id := range ids {
		if !strings.HasPrefix(id, immichAssetPrefix) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		t, err := immichAbout(ctx, config.Get().Home.Immich, id)
		cancel()
		if err != nil {
			slog.Debug("home: when and where a photo was taken", "err", err)
			continue
		}
		if t.When.IsZero() && t.Place == "" {
			continue
		}
		out = append(out, PhotoCaption{When: t.When, Place: t.Place, Right: i == 1, Pair: len(ids) == 2})
	}
	return out
}

// SlideshowCaptions is when and where the photos up were taken, for Screensaver mode to write over
// them; none in any other mode, or with captions off.
func (f *Feature) SlideshowCaptions() []PhotoCaption {
	h := config.Get().Home.Slideshow
	if h.Mode != config.SlideshowScreensaver || h.Art || !h.Caption {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.slideshow.captions)
}

// fetchCounts is whether a look whose photos all failed, the last with err, says the source is in
// trouble: not when Immich has merely no preview of them yet, which a library just added is short of
// for hours while it makes them.
func fetchCounts(err error) bool { return err != nil && !errors.Is(err, errImmichGone) }

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
		f.mu.Lock()
		gen := f.slideshowGen
		f.mu.Unlock()
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
		if !f.keepGathered(gen, h.Source, photos, h.InOrder) {
			return "", false // the source changed while it was gathered: the next look gathers the new one
		}
		slog.Info("home: slideshow photos", "count", len(photos), "subfolders", !h.TopOnly, "shuffled", !h.InOrder)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.slideshow.ahead) > 0 {
		id := f.slideshow.ahead[0]
		f.slideshow.ahead = f.slideshow.ahead[1:]
		return id, true
	}
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
	if IsImmich(id) {
		return ImmichSourceName(id)
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
	if IsImmich(h.Source) {
		srv := config.Get().Home.Immich
		if !srv.Set() {
			return nil, errors.New("immich: not set up")
		}
		return immichPhotos(ctx, srv, h.Source, immichFilterOf(h), slideshowMaxPhotos)
	}
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

// fetchShown fetches and fits the photos ids again: one alone, or a pair side by side.
func fetchShown(ids []string, whole bool) (*image.RGBA, error) {
	var srcs []image.Image
	for _, id := range ids {
		src, err := fetchSlideshowSource(id)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, src)
	}
	return fitShown(srcs, whole), nil
}

// pairGap is the dark line between the two photos of a pair, in pixels.
const pairGap = 6

// fitShown fits one photo to the panel, cropped to fill it or whole with blurred sides; or two side
// by side, each the same way in its half.
func fitShown(srcs []image.Image, whole bool) *image.RGBA {
	fit := cropToFill
	if whole {
		fit = fitWhole
	}
	sw, sh := slideshowSize()
	if len(srcs) < 2 {
		return fit(srcs[0], sw, sh)
	}
	out := image.NewRGBA(image.Rect(0, 0, sw, sh))
	draw.Draw(out, out.Rect, image.Black, image.Point{}, draw.Src)
	half := (sw - pairGap) / 2
	draw.Draw(out, image.Rect(0, 0, half, sh), fit(srcs[0], half, sh), image.Point{}, draw.Src)
	draw.Draw(out, image.Rect(sw-half, 0, sw, sh), fit(srcs[1], half, sh), image.Point{}, draw.Src)
	return out
}

// fetchSlideshowImage resolves, fetches and fits one photo to the panel: cropped to fill it, or whole
// with blurred sides.
func fetchSlideshowImage(id string, whole bool) (*image.RGBA, error) {
	src, err := fetchSlideshowSource(id)
	if err != nil {
		return nil, err
	}
	return fitShown([]image.Image{src}, whole), nil
}

// fetchSlideshowSource resolves, fetches and decodes one photo, upright.
func fetchSlideshowSource(id string) (image.Image, error) {
	var b []byte
	var err error
	switch {
	case strings.HasPrefix(id, localPhotoPrefix):
		b, err = localPhoto(id)
	case strings.HasPrefix(id, immichAssetPrefix):
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		b, err = immichPhoto(ctx, config.Get().Home.Immich, id)
		cancel()
	default:
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
	return upright(src, exifOrientation(b)), nil
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
	picscale.CatmullRom.Scale(dst, target, src, sb, draw.Over)
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
	picscale.BiLinear.Scale(out, out.Rect, small, small.Rect, draw.Src)
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
	picscale.CatmullRom.Scale(dst, target, src, sb, draw.Over)
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

// crossfade blends a into b, t running 0 (all a) to 1 (all b). Both are the slideshow's size and fully
// opaque (cropToFill's and fitWhole's draw.Src), so a plain per-byte lerp needs no alpha handling. A
// panel whose screen said its size between the two has them different sizes: that change is a cut.
func crossfade(a, b *image.RGBA, t float64) *image.RGBA {
	if a.Rect != b.Rect {
		return b
	}
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
