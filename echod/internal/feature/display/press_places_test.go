//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The press on the places beyond the buttons: the clock's places to tap, the calendar's days and
// events, the parts of the sheets over the dashboard and the music library. Each case is a page, where
// on it a tap acts (the press shows there and nowhere else), and where on it a tap does nothing (the
// page is drawn there as it is without a finger).

// placeCase is a page with a place a tap acts on (on, empty for none) and a point where one does
// nothing (off, ok false for none), both asked of the renderer that has just drawn the page plain.
type placeCase struct {
	name  string
	scene scene
	on    func(r *renderer) image.Rectangle
	off   func(r *renderer) (image.Point, bool)
	// calendars is whether this device shows a calendar, which the date and the next events need to
	// open anything.
	calendars bool
}

func mid(b image.Rectangle) image.Point { return b.Min.Add(b.Max).Div(2) }

// partOf is the first of zones of kind, as a sheet drew them.
func partOf(zones []mediaZone, kind int) image.Rectangle {
	for _, z := range zones {
		if z.kind == kind {
			return z.r
		}
	}
	return image.Rectangle{}
}

func placeCases(t *testing.T) []placeCase {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	song := home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy", Artist: "Eagles"}
	clock := calendarScenes()["clock-next-up"]
	month := calendarScenes()["calendar-month"]
	detail := month
	ev := lookEvents()[0]
	detail.cal.detail = &ev
	weatherAt := func(r *renderer) image.Rectangle {
		r.weatherMu.Lock()
		defer r.weatherMu.Unlock()
		return r.weatherAt
	}
	dateAt := func(r *renderer) image.Rectangle {
		r.weatherMu.Lock()
		defer r.weatherMu.Unlock()
		return r.dateAt
	}
	// calAt is the first of the calendar's places of kind that is the one a tap at its middle finds.
	calAt := func(kind calHitKind) func(r *renderer) image.Rectangle {
		return func(r *renderer) image.Rectangle {
			r.calMu.Lock()
			hits := append([]calHit(nil), r.calHits...)
			r.calMu.Unlock()
			for _, h := range hits {
				if got, ok := r.calendarHit(mid(h.r)); ok && h.kind == kind && got.kind == kind && got.r == h.r {
					return h.r
				}
			}
			return image.Rectangle{}
		}
	}
	// theViewShown is the Month of the switch over the month: the view shown, which a tap does nothing
	// on. It is the switch's last, just right of the views that are buttons.
	theViewShown := func(r *renderer) (image.Point, bool) {
		r.calMu.Lock()
		defer r.calMu.Unlock()
		right, y := 0, 0
		for _, h := range r.calHits {
			if h.kind == calViewDay || h.kind == calViewWeek {
				right, y = max(right, h.r.Max.X), mid(h.r).Y
			}
		}
		return image.Pt(right+r.s(10), y), right > 0
	}
	// aDayBesideTheWindow is a day of the month outside the event's window: a tap there closes it.
	aDayBesideTheWindow := func(r *renderer) (image.Point, bool) {
		r.calMu.Lock()
		hits := append([]calHit(nil), r.calHits...)
		r.calMu.Unlock()
		for _, h := range hits {
			if got, _ := r.calendarHit(mid(h.r)); h.kind == calDay && got.kind == calOutside {
				return mid(h.r), true
			}
		}
		return image.Point{}, false
	}

	drawn := func(s scene) scene {
		s.now, s.phase, s.showDash, s.dashMode = at, "idle", true, config.DashboardDrawn
		if s.drawn.Sections == nil {
			s.drawn = fourControls()
		}
		return s
	}
	mediaParts := func(r *renderer) []mediaZone {
		r.zmu.Lock()
		defer r.zmu.Unlock()
		return append([]mediaZone(nil), r.mediaZones...)
	}
	sheet := mediaSheet{entity: "media_player.kitchen", volume: -1,
		lists: dashboard.MediaLists{MusicAssistant: true, Target: "media_player.ma_kitchen", Group: "media_player.ma_kitchen"}}
	media := mediaView{sheet: sheet, now: dashboard.MediaNow{Entity: sheet.entity, Name: "Kitchen", State: "playing", Volume: 0.4}}
	light := dashboard.LightColor{Entity: "light.desk", Name: "Desk", Kelvin: true, MinK: 2200, MaxK: 6500, NowK: 3000}
	climate := climateView{now: climateStates["climate.hall"]}
	fan := fanView{now: fanStates["fan.ceiling"]}
	lib := libraryView{sheet: libSheetWith(libAlbums, libEntries("album", 2), "media_player.ma_kitchen")}
	climateParts := func(r *renderer) []mediaZone {
		r.zmu.Lock()
		defer r.zmu.Unlock()
		return append([]mediaZone(nil), r.climateZones...)
	}
	fanParts := func(r *renderer) []mediaZone {
		r.zmu.Lock()
		defer r.zmu.Unlock()
		return append([]mediaZone(nil), r.fanZones...)
	}
	libParts := func(r *renderer) []mediaZone {
		r.zmu.Lock()
		defer r.zmu.Unlock()
		return append([]mediaZone(nil), r.libZones...)
	}
	fakeClimate(t)
	fakeFan(t)

	return []placeCase{
		{name: "clock-weather", scene: clock, on: weatherAt},
		{name: "clock-next-up", scene: clock, calendars: true, on: func(r *renderer) image.Rectangle {
			r.weatherMu.Lock()
			defer r.weatherMu.Unlock()
			return r.nextCardAt
		}},
		{name: "clock-date", scene: clock, calendars: true, on: dateAt},
		// With no calendar shown here, the date and the card open nothing.
		{name: "clock-date-no-calendar", scene: clock, off: func(r *renderer) (image.Point, bool) {
			b := dateAt(r)
			return mid(b), !b.Empty()
		}},
		{name: "clock-call", scene: scene{now: at, phase: "idle", callButton: true}, on: func(r *renderer) image.Rectangle { return r.callButtonRect() }},
		{name: "clock-music-word", scene: scene{now: at, phase: "idle", playing: true, musicAway: true, radio: song},
			on: func(r *renderer) image.Rectangle { return r.playingButton() }},
		// The word with the music's page not put away is no way back to it.
		{name: "clock-music-word-not-away", scene: scene{now: at, phase: "idle", playing: true, radio: song},
			off: func(r *renderer) (image.Point, bool) { return mid(r.playingButton()), true }},
		{name: "calendar-day", scene: month, on: calAt(calDay)},
		{name: "calendar-event", scene: month, on: calAt(calEvent)},
		{name: "calendar-view-shown", scene: month, off: theViewShown},
		{name: "calendar-day-under-window", scene: detail, on: calAt(calClose), off: aDayBesideTheWindow},
		{name: "media-play", scene: drawn(scene{dashMedia: &media}),
			on: func(r *renderer) image.Rectangle { return partOf(mediaParts(r), mediaPartPlay) },
			off: func(r *renderer) (image.Point, bool) {
				b := partOf(mediaParts(r), mediaPartVolume)
				return mid(b), !b.Empty()
			}},
		{name: "media-library", scene: drawn(scene{dashMedia: &media}),
			on: func(r *renderer) image.Rectangle { return partOf(mediaParts(r), mediaPartLibrary) }},
		{name: "color-done", scene: drawn(scene{dashColor: &light}),
			on: func(r *renderer) image.Rectangle {
				r.zmu.Lock()
				defer r.zmu.Unlock()
				for _, z := range r.colorZones {
					if z.kind == colorPartDone {
						return z.r
					}
				}
				return image.Rectangle{}
			},
			off: func(r *renderer) (image.Point, bool) {
				r.zmu.Lock()
				defer r.zmu.Unlock()
				return r.colorCard.Min.Add(image.Pt(r.s(6), r.s(6))), !r.colorCard.Empty()
			}},
		{name: "climate-up", scene: drawn(scene{drawn: thermostats(), dashClimate: &climate}),
			on: func(r *renderer) image.Rectangle { return partOf(climateParts(r), climatePartUp) },
			off: func(r *renderer) (image.Point, bool) {
				r.zmu.Lock()
				defer r.zmu.Unlock()
				return r.climateCard.Min.Add(image.Pt(r.s(6), r.s(6))), !r.climateCard.Empty()
			}},
		{name: "climate-mode", scene: drawn(scene{drawn: thermostats(), dashClimate: &climate}),
			on: func(r *renderer) image.Rectangle { return partOf(climateParts(r), climatePartMode) }},
		{name: "fan-power", scene: drawn(scene{drawn: fans(), dashFan: &fan}),
			on: func(r *renderer) image.Rectangle { return partOf(fanParts(r), fanPartPower) },
			off: func(r *renderer) (image.Point, bool) {
				b := partOf(fanParts(r), fanPartSpeed)
				return mid(b), !b.Empty()
			}},
		{name: "fan-preset", scene: drawn(scene{drawn: fans(), dashFan: &fan}),
			on: func(r *renderer) image.Rectangle { return partOf(fanParts(r), fanPartPreset) }},
		{name: "library-item", scene: drawn(scene{dashLibrary: &lib}),
			on: func(r *renderer) image.Rectangle { return partOf(libParts(r), libPartItem) },
			off: func(r *renderer) (image.Point, bool) {
				// Below the two covers, where the page only scrolls.
				b := partOf(libParts(r), libPartContent)
				p := image.Pt(mid(b).X, b.Max.Y-r.s(20))
				z, _, _ := r.libraryAt(p.X, p.Y)
				return p, !b.Empty() && z.kind == libPartContent
			}},
		{name: "library-tab", scene: drawn(scene{dashLibrary: &lib}),
			on: func(r *renderer) image.Rectangle { return partOf(libParts(r), libPartTab) }},
	}
}

// A finger on a place a tap acts on shows on that place and nowhere else, and is gone from the frame
// after; a finger where a tap does nothing changes nothing. With SHOW_PREVIEW set, each pressed page is
// written there to look at.
func TestAPressShowsWhereItsTapActs(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	// The week view on, for the Day, Week and Month switch over the month.
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Calendar().Week(true); err != nil {
		t.Fatal(err)
	}
	for _, c := range placeCases(t) {
		want := []string(nil)
		if c.calendars {
			want = lookOrder
		}
		if err := config.Set().Calendar().Sources(want); err != nil {
			t.Fatal(err)
		}
		plain := testRenderer()
		plain.draw(c.scene)
		press := func(at image.Point) *renderer {
			r := testRenderer()
			s := c.scene
			s.press = pressView{on: true, at: at}
			r.draw(s)
			return r
		}
		if c.on != nil {
			b := c.on(plain)
			if b.Empty() {
				t.Errorf("%s: nothing to press", c.name)
			} else {
				pressed := press(mid(b))
				if box, differs := diffBox(pressed, plain); !differs {
					t.Errorf("%s: a finger on %v looks as the page does without one", c.name, b)
				} else if !box.In(b.Inset(-plain.s(8))) {
					t.Errorf("%s: the press shows over %v, outside %v", c.name, box, b)
				}
				if dir != "" {
					writePressed(t, filepath.Join(dir, "pressed-"+c.name+".png"), pressed)
				}
				pressed.draw(c.scene)
				if box, differs := diffBox(pressed, plain); differs {
					t.Errorf("%s: after the press the page still differs over %v", c.name, box)
				}
			}
		}
		if c.off != nil {
			p, ok := c.off(plain)
			if !ok {
				t.Errorf("%s: no place to try", c.name)
				continue
			}
			if box, differs := diffBox(press(p), plain); differs {
				t.Errorf("%s: a finger at %v, where a tap does nothing, changed the page over %v", c.name, p, box)
			}
		}
	}
}

func writePressed(t *testing.T, path string, r *renderer) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, r.dst); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// A press made on one view of the calendar, on a sheet over the dashboard or on a page of the library
// is not shown on the one a tap there brings up.
func TestAPressIsNotCarriedToTheViewItOpened(t *testing.T) {
	now := time.Now()
	month := calendarScenes()["calendar-month"]
	day := calendarScenes()["calendar-day"]
	media := scene{now: now, phase: "idle", showDash: true, dashMedia: &mediaView{}}
	lib := libraryView{sheet: libSheetWith(libAlbums, nil, "")}
	library := scene{now: now, phase: "idle", showDash: true, dashLibrary: &lib}
	other := libraryView{sheet: libSheetWith(libArtists, nil, "")}
	tab := scene{now: now, phase: "idle", showDash: true, dashLibrary: &other}
	for name, pair := range map[string][2]scene{
		"a day of the month": {month, day},
		"Library on a sheet": {media, library},
		"a tab of it":        {library, tab},
	} {
		d := pressDisplay(pair[0])
		d.fingerLanded(touch.Point{X: 480, Y: 300})
		s := pair[0]
		if d.pressScene(&s, now); !s.press.on {
			t.Fatalf("%s: no press to begin with", name)
		}
		if pressShown(d, pair[1], now) || d.press.set {
			t.Errorf("%s: what it opened came up with the press still shown", name)
		}
	}
}
