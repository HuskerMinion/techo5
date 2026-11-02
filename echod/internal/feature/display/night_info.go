//go:build !dot

package display

import (
	"image"
	"image/color"
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Night clock shows Next up and notifications (#397): off by default, the night clock is the time alone
// and a notification waits for a touch. On, the night clock also carries what is worth waking to read -
// on a Show the Next up card and the glance chips along the foot, and a notification at once, until
// tapped away - drawn in the night's own palette (nightTone) at the night light's brightness: the
// backlight is never raised for any of it. Where the night is no red clock (Screen at night is off or
// a night light without the clock) there is no night clock to put it on, and the setting does nothing.
// The Spot has no Next up card or chips, so there it is the notification alone (render_night_spot.go).

// nightInfoStyle is the clock style the Next up card is worked out for at night: the day's own may be
// the Dashboard or the Agenda, which list the events instead of carrying a card.
const nightInfoStyle = ""

// nightInfoOn is whether the night clock carries Next up, the chips and notifications.
func nightInfoOn() bool { return config.Get().Screen.NightInfo && nightClockOn() }

// nightInfoSwitch is the Home Assistant switch for the setting.
func nightInfoSwitch(d *Display) *esphome.Switch {
	s := &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "screen_night_info",
			Name:     "Night clock shows Next up and notifications",
			Icon:     "mdi:bell-ring-outline",
			Category: esphome.CategoryConfig,
		},
	}
	s.OnCommand = d.setNightInfo
	return s
}

// setNightInfo saves the setting, tells Home Assistant and shows it at once.
func (d *Display) setNightInfo(on bool) {
	if err := config.Set().Screen().NightInfo(on); err != nil {
		slog.Error("saving a setting failed", "setting", "screen_night_info", "err", err)
		on = config.Get().Screen.NightInfo
	} else {
		slog.Info("setting changed", "setting", "screen_night_info", "using", on)
	}
	if d.nightInfoSw != nil {
		d.nightInfoSw.Set(on)
	}
	d.wake()
}

// nightTone is a color as the night shows it: its brightness taken to the night red's dim scale, so
// white is the night clock's own red and nothing is brighter than it. Black stays black.
func nightTone(c color.RGBA) color.RGBA {
	l := (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
	return color.RGBA{uint8(l * int(nightRed.R) / 255), uint8(l * int(nightRed.G) / 255), 0, 255}
}

// nightLayerFor is a black layer over b, kept in *buf between frames: allocated when the box changes,
// otherwise cleared for reuse, so a night redraw does not allocate.
func nightLayerFor(buf **image.RGBA, b image.Rectangle) *image.RGBA {
	if *buf == nil || (*buf).Rect != b {
		*buf = image.NewRGBA(b)
	}
	l := *buf
	for i := 0; i < len(l.Pix); i += 4 {
		l.Pix[i], l.Pix[i+1], l.Pix[i+2], l.Pix[i+3] = 0, 0, 0, 255
	}
	return l
}

// nightToneOnto copies the pixels of src that are not black into dst in the night's tone, in b.
func nightToneOnto(dst, src *image.RGBA, b image.Rectangle) {
	b = b.Intersect(dst.Rect).Intersect(src.Rect)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if c := src.RGBAAt(x, y); c.R|c.G|c.B != 0 {
				dst.SetRGBA(x, y, nightTone(c))
			}
		}
	}
}
