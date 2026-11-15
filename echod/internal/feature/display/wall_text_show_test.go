//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The Coming up list's rows grow with the words, so words drawn larger keep their room.
func TestAgendaRowsGrowWithTheText(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Calendar().Sources(lookOrder)
	t.Cleanup(func() { config.Use(filepath.Join(t.TempDir(), "state.json")) }) // the size chosen here is not the next test's
	rowsAt := func(size int) int {
		_ = config.Set().Screen().TextSize(size)
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, skylightWide, skylightHigh)))
		v := lookView()
		v.agenda = true
		r.draw(scene{now: lookNow, phase: "idle", showCalendar: true, cal: v})
		return r.calendarAgendaStep()
	}
	usual, large := rowsAt(100), rowsAt(125)
	if large >= usual {
		t.Errorf("a page holds %d rows at 125%% and %d at 100%%: the rows did not grow", large, usual)
	}
}

// With SHOW_PREVIEW set, the main screens at the large text sizes, on every panel: the calendar's
// pages, the clock with and without its next-up card, the weather, now playing and the Display
// settings, as wall-<size>-<screen>-<panel>.png.
func TestWallTextPreviews(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW is not set")
	}
	previewFont(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Calendar().Sources(lookOrder)
	t.Cleanup(func() { config.Use(filepath.Join(t.TempDir(), "state.json")) }) // the size chosen here is not the next test's
	at := lookNow
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "rainy", "lightning-rainy", "sunny", "snowy", "cloudy"} {
		week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: c, High: float64(78 - 3*i), Low: float64(55 - 2*i), Rain: 10 * i})
	}
	long := lookCard()
	long.ev.Summary = "Pumpkin patch and hayride with the neighbors"
	for _, size := range []int{125, 150} {
		_ = config.Set().Screen().TextSize(size)
		scenes := calendarScenes()
		scenes["calendar-month-many"] = reviewScenes()["calendar-month-many"]
		scenes["clock"] = scene{now: at, phase: "idle", weather: hassSunny()}
		scenes["clock-next-up-long"] = scene{now: at, phase: "idle", weather: hassSunny(), nextUp: long}
		scenes["weather"] = scene{now: at, phase: "idle", weather: home.Weather{Condition: "lightning-rainy", Temp: "68°"}, forecast: week, showWeather: true}
		scenes["nowplaying"] = scene{now: at, phase: "idle", nowPlaying: true, playing: true, weather: hassSunny(),
			radio: home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy", Artist: "Eagles"}}
		scenes["settings-display"] = scene{now: at, phase: "idle", showSheet: true, sheet: settings{cat: catDisplay, brightness: 70}}
		for _, panel := range showPanels() {
			for name, s := range scenes {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				newRenderer(img).draw(s)
				f, err := os.Create(filepath.Join(dir, fmt.Sprintf("wall-%d-%s-%s.png", size, name, panel.name)))
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
