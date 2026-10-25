//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// The doorbell pressed during a call: the call's page says somebody is at the door, under its title,
// and below the note of an alarm ringing behind the call when there is one. With SHOW_PREVIEW set,
// each is written there to look at.
func TestAtTheDoorOnTheCallPage(t *testing.T) {
	at := time.Date(2026, 10, 10, 14, 7, 0, 0, time.Local)
	dir := os.Getenv("SHOW_PREVIEW")
	call := phone.State{Configured: true, Registered: true, Phase: phone.Talking, Peer: "Kitchen", Intercom: true, Since: at.Add(-75 * time.Second)}
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}} {
		draw := func(s scene) *image.RGBA {
			s.now, s.phase, s.call = at, "idle", call
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			newRenderer(img).draw(s)
			return img
		}
		plain := draw(scene{})
		for name, s := range map[string]scene{
			"call-at-door":         {atDoor: true},
			"call-at-door-ringing": {atDoor: true, ring: ringState{timer: "Pasta"}},
			"call-ringing":         {ring: ringState{timer: "Pasta"}},
		} {
			img := draw(s)
			if same(img.Pix, plain.Pix) {
				t.Errorf("%s%s: the page looks the same as a plain call", name, panel.name)
			}
			if dir == "" {
				continue
			}
			f, err := os.Create(filepath.Join(dir, name+panel.name+".png"))
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
