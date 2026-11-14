//go:build spot

package display

import (
	"image"
	"image/color"
	"testing"
	"time"
)

// inkRows are the rows of img that have a pixel of c in the middle of the face, where the words are
// and the rim is not.
func inkRows(img *image.RGBA, c color.RGBA) map[int]bool {
	rows := map[int]bool{}
	for y := 60; y < side-60; y++ {
		for x := 130; x < side-130; x++ {
			if img.RGBAAt(x, y) == c {
				rows[y] = true
				break
			}
		}
	}
	return rows
}

// A lost Home Assistant is said over the time in the timer's amber, never in the mute's red, and the
// rim is left as it was: the red rim and the red word mean the microphone and nothing else.
func TestTheSpotSaysWhatIsLost(t *testing.T) {
	at := time.Date(2026, 10, 4, 14, 0, 0, 0, time.Local)
	face := func(s roundScene) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		newRoundRenderer(img).draw(s)
		return img
	}
	plain := face(roundScene{now: at, phase: "idle"})
	lost := face(roundScene{now: at, phase: "idle", linkDown: linkNoHome})
	if len(inkRows(plain, colTimer)) != 0 {
		t.Fatal("the plain clock face already has the amber on it")
	}
	if len(inkRows(lost, colTimer)) == 0 {
		t.Error("Home Assistant lost: nothing on the clock face says so")
	}
	if len(inkRows(lost, colMuted)) != 0 {
		t.Error("Home Assistant lost is said in the mute's red")
	}
	rim := (rimIn + rimOut) / 2
	if got, want := lost.RGBAAt(center, center-rim), plain.RGBAAt(center, center-rim); got != want {
		t.Errorf("Home Assistant lost changed the rim to %v, from %v", got, want)
	}
}

// Muted as well, both are said: the mute where it always is and in its red, the loss on a line over it.
func TestTheSpotSaysMuteAndWhatIsLostTogether(t *testing.T) {
	at := time.Date(2026, 10, 4, 14, 0, 0, 0, time.Local)
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	newRoundRenderer(img).draw(roundScene{now: at, phase: "idle", muted: true, linkDown: linkNoWifi})
	mute, lost := inkRows(img, colMuted), inkRows(img, colTimer)
	if len(mute) == 0 || len(lost) == 0 {
		t.Fatalf("muted with no Wi-Fi: %d rows of the mute's word, %d of the loss's", len(mute), len(lost))
	}
	for y := range lost {
		if mute[y] {
			t.Fatalf("the two lines share row %d", y)
		}
	}
	if got := img.RGBAAt(center, center-(rimIn+rimOut)/2); got != colMuted {
		t.Errorf("muted with no Wi-Fi: the rim is %v, want the mute's %v", got, colMuted)
	}
}

// The night clock lights nothing it does not have to: a lost Home Assistant is not said on it.
func TestTheNightClockDoesNotSayWhatIsLost(t *testing.T) {
	at := time.Date(2026, 10, 4, 23, 0, 0, 0, time.Local)
	face := func(down string) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		newRoundRenderer(img).draw(roundScene{now: at, phase: "idle", redClock: true, linkDown: down})
		return img
	}
	plain, lost := face(linkUp), face(linkNoHome)
	for i := range plain.Pix {
		if plain.Pix[i] != lost.Pix[i] {
			t.Fatal("the night clock changed with Home Assistant lost")
		}
	}
}
