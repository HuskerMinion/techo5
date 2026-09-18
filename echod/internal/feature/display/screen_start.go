//go:build !dot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/screen"
)

// settleScreen puts a panel just opened in the state restored for it. A screen that was off when the
// device restarted showed Amazon's logo, lit, until somebody turned it on (2026-09-18): the bootloader
// leaves the backlight lit while the driver reads it as 0, so Restore's 0 changed nothing, and nothing
// is drawn while the screen is off. So a screen restored as off is blanked, and its backlight is
// stepped up and back down, which the driver does act on.
func (d *Display) settleScreen(dev *screen.Device) {
	d.mu.Lock()
	on := d.on
	d.mu.Unlock()
	if !on {
		c := dev.Canvas()
		draw.Draw(c, c.Rect, image.NewUniform(color.RGBA{0, 0, 0, 255}), image.Point{}, draw.Src)
		if err := dev.Present(); err != nil {
			slog.Warn("blanking the screen failed", "err", err)
		}
		if err := screen.SetBacklight(1); err != nil {
			slog.Warn("setting the backlight failed", "err", err)
		}
	}
	d.relight(true)
}
