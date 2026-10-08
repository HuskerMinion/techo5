//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

var artConds = map[artCond]string{artClear: "sunny", artPartly: "partlycloudy", artCloudy: "cloudy", artRain: "rainy", artStorm: "lightning-rainy", artSnow: "snowy", artFog: "fog"}

// Every weather at every part of the day draws, on both panels, alone and behind the clock; with
// SHOW_PREVIEW set, each is written there to look at.
func TestWeatherArtDraws(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	for c, cond := range artConds {
		for when := artDawn; when <= artNight; when++ {
			for _, p := range [][2]int{{showWide, showHigh}, {show8Wide, show8High}} {
				land := paintLandscape(artKey{c, when, p[0], p[1], 42})
				frame := image.NewRGBA(land.base.Rect)
				composeArt(frame, land, artBodies{moon: moonAt(0, 0, false, at)}, at.Unix())
				img := image.NewRGBA(image.Rect(0, 0, p[0], p[1]))
				s := scene{now: at, phase: "idle", weather: home.Weather{Condition: cond, Temp: "72°"}, slideshow: frame, artFx: fxFor(cond)}
				newRenderer(img).draw(s)
				if dir == "" || p[0] != showWide {
					continue
				}
				for name, pic := range map[string]*image.RGBA{"art": frame, "art-clock": img} {
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-%s-%d.png", name, cond, when)))
					if err != nil {
						t.Fatal(err)
					}
					png.Encode(f, pic)
					f.Close()
				}
			}
		}
	}
}

// The landscape is drawn once per weather and part of the day; this is what that costs.
func BenchmarkWeatherArtLandscape(b *testing.B) {
	for b.Loop() {
		paintLandscape(artKey{artRain, artDay, showWide, showHigh, 42})
	}
}

// One frame of the weather art with rain falling and the clock over it, drawn twelve times a second
// while it rains; BenchmarkWeatherPageFrame is the forecast page with the same rain, which the Shows
// already draw at that pace, to measure it against.
func BenchmarkWeatherArtFrame(b *testing.B) {
	config.Use(filepath.Join(b.TempDir(), "state.json"))
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	rise, set := at.Add(-7*time.Hour), at.Add(5*time.Hour)
	k := artKey{artRain, artDay, showWide, showHigh, artSeed()}
	arts.mu.Lock()
	arts.land = paintLandscape(k)
	arts.mu.Unlock()
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	r := newRenderer(img)
	i := 0
	for b.Loop() {
		now := at.Add(time.Duration(i) * fxFrame)
		frame := artFrame(now, "rainy", rise, set, true, artMoon{}, showWide, showHigh)
		r.draw(scene{now: now, phase: "idle", weather: home.Weather{Condition: "rainy", Temp: "54°"}, slideshow: frame, artFx: fxRain})
		i++
	}
}

func BenchmarkWeatherPageFrame(b *testing.B) {
	config.Use(filepath.Join(b.TempDir(), "state.json"))
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	r := newRenderer(img)
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	i := 0
	for b.Loop() {
		now := at.Add(time.Duration(i) * fxFrame)
		r.draw(scene{now: now, phase: "idle", weather: home.Weather{Condition: "rainy", Temp: "54°"}, showWeather: true, sky: fxRain})
		i++
	}
}

// Snow draws its caps against a ragged line; on the Show 8's panel that used to cost eight times rain.
func BenchmarkWeatherArtSnowShow8(b *testing.B) {
	for b.Loop() {
		paintLandscape(artKey{artSnow, artDay, show8Wide, show8High, 42})
	}
}

// The moon over the landscape behind the clock, each phase low and high, through clouds and fog and by
// day, on both Shows; with SHOW_PREVIEW set, each is written there to look at.
func TestWeatherArtMoonPreviews(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 10, 13, 21, 14, 0, 0, time.Local)
	for _, p := range []struct {
		name string
		w, h int
	}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}} {
		writeMoonPreviews(t, dir, p.name, p.w, p.h, func(art *image.RGBA, cond string) *image.RGBA {
			img := image.NewRGBA(image.Rect(0, 0, p.w, p.h))
			newRenderer(img).draw(scene{now: at, phase: "idle", weather: home.Weather{Condition: cond, Temp: "48°"}, slideshow: art, artFx: fxFor(cond)})
			return img
		})
	}
}
