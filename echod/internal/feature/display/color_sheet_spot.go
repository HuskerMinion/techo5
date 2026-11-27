//go:build spot

package display

import (
	"image"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The Spot has no color sheet: a finger held still and lifted on its dashboard is the ring menu.

func (d *Display) colorOpen() bool { return false }

// tileAction does what a tile's card says: a card that navigates to dashboard.AlarmsPath opens the
// alarms, as on the Show. One that navigates to dashboard.WeatherPath or dashboard.RadarPath opens the
// weather face on the forecast or the rain map (#772), as the weather in the ring menu does, unless
// this device is set to no weather; at night it is shown in the night clock's place at the night
// light's level, as the ring menu is. The Spot has no music library: a card that opens it on the Show is nowhere
// to go here (dashboard.Feature.Tap).
func (d *Display) tileAction(t *dashTile) {
	if t.action.View == dashboard.AlarmsPath {
		d.OpenSheet("alarms")
		return
	}
	if v := t.action.View; v == dashboard.WeatherPath || v == dashboard.RadarPath {
		if config.Get().Home.WeatherEntity() != "" {
			slog.Info("dashboard weather tile", "radar", v == dashboard.RadarPath)
			d.locked(func() {
				d.openMenu(modeWeather, "")
				d.radar = v == dashboard.RadarPath
				d.weatherUntil = time.Now().Add(weatherIdle)
			})
			d.wake()
		}
		return
	}
	dashboard.Get().Tap(*t.action)
}

func (d *Display) colorTap(int, int) bool { return false }

func (d *Display) onColorSheet(int, int) bool { return false }

func (d *Display) sliderAt(int, int) (sheetSlider, bool) { return sheetSlider{}, false }

func (d *Display) slideSheet(sheetSlider, int, bool) {}

func (d *Display) sliderBegins(sheetSlider) {}

func (s sheetSlider) slides(bool) bool { return true }

func (d *Display) pageSwipeOnSheet(image.Point, int, int) bool { return false }

func (d *Display) samePartOnSheet(image.Point, image.Point) bool { return true }

func (d *Display) libDragStart(int, int) {}

func (d *Display) libDragMove(int, int) bool { return false }

func (d *Display) libraryHold(int, int) bool { return false }
