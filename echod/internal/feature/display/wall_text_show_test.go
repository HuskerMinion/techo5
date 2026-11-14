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

// With SHOW_PREVIEW set, the calendar's pages on the Skylight at the sizes a wall panel is read at.
func TestWallTextPreviews(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW is not set")
	}
	previewFont(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Calendar().Sources(lookOrder)
	t.Cleanup(func() { config.Use(filepath.Join(t.TempDir(), "state.json")) }) // the size chosen here is not the next test's
	for _, size := range []int{125} {
		_ = config.Set().Screen().TextSize(size)
		scenes := calendarScenes()
		scenes["calendar-month-many"] = reviewScenes()["calendar-month-many"]
		scenes["clock-next-up"] = scene{now: lookNow, phase: "idle", weather: hassSunny(), nextUp: lookCard()}
		for name, s := range scenes {
			img := image.NewRGBA(image.Rect(0, 0, skylightWide, skylightHigh))
			newRenderer(img).draw(s)
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("wall-%d-%s-skylight.png", size, name)))
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
