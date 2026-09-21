//go:build spot

package camera

import (
	"image/png"
	"net/http"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/display"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
)

// registerScreen adds /screen.png: what the round panel shows, as last drawn. Off unless the Screen
// web access switch is on. ?sheet= puts the settings screen up first ("settings" for its categories,
// a category's name, or "off"), ?list= a row's list of choices on it, and ?demo=1 placeholders for the
// owner's details, for screenshots that will be published.
func (f *Feature) registerScreen() {
	web.Handle("/screen.png", "Screen", screenOpen, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("demo") != "" {
			display.Get().Demo(20 * time.Second)
		}
		if name := q.Get("sheet"); name != "" && display.Get().OpenSheet(name) {
			time.Sleep(500 * time.Millisecond)
		}
		if list := q.Get("list"); list != "" && display.Get().OpenList(list) {
			time.Sleep(700 * time.Millisecond)
		}
		img := display.Get().Screenshot()
		if img == nil {
			http.Error(w, "the screen is not open", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		png.Encode(w, img)
	})
}

func screenOpen() bool { return config.Get().Security.Screen }
