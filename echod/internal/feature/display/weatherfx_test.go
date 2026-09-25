//go:build !dot

package display

import (
	"image"
	"testing"
	"time"
)

// Only a falling or blowing sky moves, and the setting turns every one of them still.
func TestSkiesForConditions(t *testing.T) {
	defer weatherAnimation.Store(weatherAnimation.Load())
	weatherAnimation.Store(true)
	for cond, want := range map[string]skyFx{"rainy": fxRain, "pouring": fxPour, "snowy": fxSnow, "snowy-rainy": fxSleet,
		"hail": fxHail, "lightning-rainy": fxStorm, "fog": fxFog, "windy": fxWind, "sunny": fxNone, "cloudy": fxNone, "": fxNone} {
		if got := skyNow(cond); got != want {
			t.Errorf("%q: sky %d, want %d", cond, got, want)
		}
	}
	weatherAnimation.Store(false)
	if skyNow("pouring") != fxNone {
		t.Error("the setting off still moves the sky")
	}
}

// Rain moves between frames, and a still sky draws nothing at all.
func TestTheSkyMoves(t *testing.T) {
	at := time.Date(2026, 9, 25, 7, 0, 0, 0, time.UTC)
	frame := func(fx skyFx, when time.Time) *image.RGBA {
		p := &paint{dst: image.NewRGBA(image.Rect(0, 0, 960, 480)), w: 960, h: 480}
		p.sky(fx, when, p.dst.Rect, image.Rect(400, 60, 470, 400))
		return p.dst
	}
	a, b := frame(fxRain, at), frame(fxRain, at.Add(fxFrame))
	if string(a.Pix) == string(b.Pix) {
		t.Error("the rain did not move between frames")
	}
	if blank := frame(fxNone, at); string(blank.Pix) != string(image.NewRGBA(blank.Rect).Pix) {
		t.Error("a still sky drew something")
	}
}

// What a frame of each sky costs to draw, on the largest panel.
func BenchmarkSky(b *testing.B) {
	p := &paint{dst: image.NewRGBA(image.Rect(0, 0, 1280, 800)), w: 1280, h: 800, sNum: 4, sDen: 3}
	for _, fx := range []skyFx{fxRain, fxPour, fxSnow, fxStorm, fxFog} {
		b.Run([]string{"", "rain", "pour", "snow", "sleet", "hail", "storm", "fog", "wind"}[fx], func(b *testing.B) {
			at := time.Now()
			for i := 0; b.Loop(); i++ {
				p.sky(fx, at.Add(time.Duration(i)*fxFrame), p.dst.Rect, image.Rect(560, 90, 630, 530))
			}
		})
	}
}
