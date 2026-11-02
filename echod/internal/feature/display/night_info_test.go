//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// nightInfoFrame is a night clock frame with something to carry: glance chips, or else a Next up card.
func nightInfoFrame(at time.Time, style string, info, chips bool) scene {
	s := scene{now: at, phase: "idle", redClock: true, redStyle: style, nightDim: true, nightInfo: info}
	if chips {
		s.glance = []home.Chip{{Icon: "mdi:door", Text: "Back door open"}, {Icon: "mdi:thermometer", Text: "71 degrees"}}
		return s
	}
	s.nextUp = &nextUpCard{
		ev:    hass.Event{Summary: "Check in with the night nurse", Start: at.Add(40 * time.Minute), End: at.Add(100 * time.Minute)},
		then:  &hass.Event{Summary: "Morning meds", Start: at.Add(4 * time.Hour), End: at.Add(5 * time.Hour)},
		color: color.RGBA{0x4a, 0xa8, 0xf2, 0xff},
	}
	return s
}

func drawNight(w, h int, s scene) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	newRenderer(img).draw(s)
	return img
}

// brighterThanNight is the first pixel the night's red cannot make (a channel past the night red's, or
// any blue), if there is one. The flip clock's own off-white is the one other ink allowed.
func brighterThanNight(img *image.RGBA, flip bool) (image.Point, color.RGBA, bool) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if flip && luma(c.R, c.G, c.B) <= luma(flipInk.R, flipInk.G, flipInk.B)+1 {
				continue
			}
			if c.R > nightRed.R || c.G > nightRed.G || c.B > 0 {
				return image.Pt(x, y), c, true
			}
		}
	}
	return image.Point{}, color.RGBA{}, false
}

// litBelow counts the pixels that are not black from row y down.
func litBelow(img *image.RGBA, y int) int {
	n := 0
	b := img.Bounds()
	for ; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if c := img.RGBAAt(x, y); c.R|c.G|c.B != 0 {
				n++
			}
		}
	}
	return n
}

// With the option off a night clock is exactly the clock it was, whatever is waiting to be shown; with
// it on and nothing to show it is the same again.
func TestNightInfoOffIsTheClockAsItWas(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	at := time.Date(2026, 11, 2, 3, 47, 0, 0, time.Local)
	for _, panel := range showPanels() {
		for _, style := range []string{nightStylePlain, nightStyleLED, nightStyleFlip} {
			alone := drawNight(panel.wide, panel.high, scene{now: at, phase: "idle", redClock: true, redStyle: style, nightDim: true})
			for _, chips := range []bool{false, true} {
				if got := drawNight(panel.wide, panel.high, nightInfoFrame(at, style, false, chips)); !samePixels(got, alone) {
					t.Errorf("%s %q (chips %v): the night clock changed with the option off", panel.name, style, chips)
				}
			}
			empty := scene{now: at, phase: "idle", redClock: true, redStyle: style, nightDim: true, nightInfo: true}
			if got := drawNight(panel.wide, panel.high, empty); !samePixels(got, alone) {
				t.Errorf("%s %q: the option on with nothing to show changed the clock", panel.name, style)
			}
		}
	}
}

// On, the Next up card and the chips are drawn along the foot in the night's colors on every night clock
// style and panel, and nothing is brighter than the night red (or the flip clock's own ink).
func TestNightInfoDrawsTheStripInTheNightPalette(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	at := time.Date(2026, 11, 2, 3, 47, 0, 0, time.Local)
	for _, panel := range showPanels() {
		for _, style := range []string{nightStylePlain, nightStyleLED, nightStyleFlip} {
			alone := drawNight(panel.wide, panel.high, scene{now: at, phase: "idle", redClock: true, redStyle: style, nightDim: true})
			for _, chips := range []bool{false, true} {
				name := panel.name + " " + style + " card"
				if chips {
					name = panel.name + " " + style + " chips"
				}
				got := drawNight(panel.wide, panel.high, nightInfoFrame(at, style, true, chips))
				if samePixels(got, alone) {
					t.Fatalf("%s: nothing was drawn", name)
				}
				if p, c, bad := brighterThanNight(got, style == nightStyleFlip); bad {
					t.Errorf("%s: a pixel the night cannot make at %v: %v", name, p, c)
				}
				from := panel.high * 78 / 100
				// Along the foot, lit where the plain clock is black: the strip, not the clock.
				fresh := 0
				for y := from; y < panel.high; y++ {
					for x := 0; x < panel.wide; x++ {
						if c := alone.RGBAAt(x, y); c.R|c.G|c.B == 0 {
							if g := got.RGBAAt(x, y); g.R|g.G|g.B != 0 {
								fresh++
							}
						}
					}
				}
				if fresh < 600 {
					t.Errorf("%s: only %d new lit pixels along the foot", name, fresh)
				}
				// The time is still there, and clear of the foot.
				top := got.SubImage(image.Rect(0, 0, panel.wide, from)).(*image.RGBA)
				if litBelow(top, 0) < 2000 {
					t.Errorf("%s: the time is gone", name)
				}
			}
		}
	}
}

// A notification at night is drawn at once on the night clock, in the night's palette, and tapped away as
// usual; nothing lights the panel for it (the backlight stays at the night light's level).
func TestNightInfoShowsANotificationAtOnce(t *testing.T) {
	d := noticeDisplay(t, showWide, showHigh)
	atNight(t)
	if err := config.Set().Screen().AtNight(true, true); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Screen().NightInfo(true); err != nil {
		t.Fatal(err)
	}
	if !nightInfoOn() {
		t.Fatal("the option is not on")
	}
	d.nightGlow = true
	at := time.Now()
	frame := func() (scene, *image.RGBA) {
		s := scene{now: at, phase: "idle", redClock: true, nightInfo: true, nightDim: true}
		s.notification, s.showNotification = notify.Get().Card()
		d.gateNotice(&s, nightNow(at))
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		newRenderer(img).draw(s)
		return s, img
	}
	_, clock := frame()
	if err := notify.Get().Notify("The server is down", "Server", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	s, shown := frame()
	if !s.showNotification || !s.redClock {
		t.Fatalf("a notification at night: showNotification=%v redClock=%v, want the card on the night clock", s.showNotification, s.redClock)
	}
	if samePixels(shown, clock) {
		t.Fatal("the notification is not drawn")
	}
	// Under a notification the Next up card and the chips are not drawn, so the card never cuts across them.
	with := nightInfoFrame(at, nightStylePlain, true, false)
	with.showNotification, with.notification = true, s.notification
	without := with
	without.nextUp = nil
	if !samePixels(drawNight(showWide, showHigh, with), drawNight(showWide, showHigh, without)) {
		t.Error("the Next up card is drawn under the notification")
	}
	if p, c, bad := brighterThanNight(shown, false); bad {
		t.Errorf("the notification lights a pixel the night cannot make at %v: %v", p, c)
	}
	if d.noticeHeld() {
		t.Error("the notification is waiting for a touch")
	}
	d.mu.Lock()
	glowing, lit := d.nightGlow, d.on
	d.mu.Unlock()
	if !glowing || !lit {
		t.Errorf("the night light was lifted or put out: glow=%v on=%v", glowing, lit)
	}
	// noticeLights is the one thing that could light a panel for it, and it does not at night.
	if d.noticeLights(false, true) {
		t.Error("the notification lit a dark panel at night")
	}

	// Tapped away on the night clock like any other, and the clock is alone again.
	listenForNotice()
	tapAt(d, image.Pt(showWide/2, showHigh/2))
	dismissedByTap(t, "a tap on the card on the night clock")
	if _, after := frame(); !samePixels(after, clock) {
		t.Error("the night clock did not come back after the card was tapped away")
	}

	// Off, it is held for a touch as before.
	if err := notify.Get().Notify("Held", "", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	s = scene{now: at, phase: "idle", redClock: true, nightInfo: false}
	s.notification, s.showNotification = notify.Get().Card()
	d.gateNotice(&s, true)
	if s.showNotification {
		t.Error("with the option off the notification was not held")
	}
}

// The setting does nothing where there is no night clock to put it on.
func TestNightInfoNeedsTheNightClock(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().NightInfo(true); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Screen().AtNight(true, false); err != nil {
		t.Fatal(err)
	}
	if nightInfoOn() {
		t.Error("on with a night light that is not the clock")
	}
	if err := config.Set().Screen().AtNight(true, true); err != nil {
		t.Fatal(err)
	}
	if !nightInfoOn() {
		t.Error("off with the night clock")
	}
}

// With SHOW_PREVIEW set, the night clock with the option on is written there: the Next up card, the
// chips and a notification, in each night style, on the Show 5 (960x480) and the Show 8 (1280x800).
func TestNightInfoPreview(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW is not set")
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	at := time.Date(2026, 11, 2, 3, 47, 0, 0, time.Local)
	for _, panel := range showPanels()[:2] {
		for style, label := range map[string]string{nightStylePlain: "red", nightStyleLED: "led", nightStyleFlip: "flip"} {
			note := nightInfoFrame(at, style, true, false)
			note.showNotification = true
			note.notification = notify.Notification{Kind: notify.Text, Title: "Server", Icon: "mdi:server-network-off",
				Message: "The home server is not answering"}
			for file, s := range map[string]scene{
				"card":         nightInfoFrame(at, style, true, false),
				"chips":        nightInfoFrame(at, style, true, true),
				"notification": note,
			} {
				img := drawNight(panel.wide, panel.high, s)
				f, err := os.Create(filepath.Join(dir, "night-info-"+label+"-"+file+"-"+panel.name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, img); err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
		}
	}
}
