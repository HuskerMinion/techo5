//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// The Commodore 64 skins' home screen draws on every panel, alone, with timers and with the next alarm,
// its characters a whole number of pixels so they stay sharp; with SHOW_PREVIEW set, each is written
// there to look at.
func TestC64HomeDraws(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 10, 25, 10, 42, 0, 0, time.Local)
	sky := home.Weather{Condition: "sunny", Temp: "54°"}
	running := []timer.Countdown{{Name: "Pasta", Left: 4*time.Minute + 32*time.Second, Total: 10 * time.Minute, Active: true}}
	scenes := map[string]scene{
		"":       {now: at, phase: "idle", weather: sky},
		"-timer": {now: at, phase: "idle", weather: sky, timers: running},
		"-alarm": {now: at, phase: "idle", weather: sky, timers: running, alarms: alarm.View{Next: &alarm.Upcoming{At: at.Add(16 * time.Hour)}}},
		"-blink": {now: at.Add(c64Blink), phase: "idle", weather: sky},
		"-call":  {now: at.Add(c64Blink), phase: "idle", weather: sky, callButton: true},
	}
	dir := os.Getenv("SHOW_PREVIEW")
	t.Cleanup(func() { skinPreview = "" })
	for _, sk := range []string{skinC64, skinBASIC} {
		skinPreview = sk
		for suffix, s := range scenes {
			for _, panel := range []struct {
				name       string
				wide, high int
			}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}, {"-panel-portrait", 800, 1280}} {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				r := newRenderer(img)
				r.draw(s)
				if !r.c64Drawn {
					t.Fatalf("%s%s%s: not the computer's screen", sk, suffix, panel.name)
				}
				b := r.s(20)
				x0, y0, cell, cols, rows := c64Grid(panel.wide, panel.high, b)
				if cell%8 != 0 || x0 < b || y0 < b || cols < c64Cols || rows < c64Rows || x0-b >= cell || y0-b >= cell {
					t.Errorf("%s%s: grid %d,%d cell %d, %d by %d", sk, panel.name, x0, y0, cell, cols, rows)
				}
				if dir == "" {
					continue
				}
				f, err := os.Create(filepath.Join(dir, "c64-"+sk+suffix+panel.name+".png"))
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

// TECHO5 BASIC's banner says the version and the memory, and both lines fit the screen's 40 columns.
func TestBASICBanner(t *testing.T) {
	top, second := c64Banner(skinBASIC)
	if !strings.Contains(top, "TECHO5 BASIC") || !strings.Contains(second, "BYTES FREE") {
		t.Errorf("banner %q / %q", top, second)
	}
	for _, l := range []string{top, second} {
		if len(l) > c64Cols {
			t.Errorf("%q is wider than the screen", l)
		}
	}
	if top, _ := c64Banner(skinC64); top != "**** COMMODORE 64 BASIC V2 ****" {
		t.Errorf("Commodore 64 banner %q", top)
	}
}
