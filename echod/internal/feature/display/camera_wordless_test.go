//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// white is a picture of nothing but white, the size of the panel, so anything drawn over it shows.
func white(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	return img
}

// A picture sent with no words is the picture alone: no name and clock strip at the top and no caption
// bar, only the strip at the foot that says how to close it. One with words keeps both.
func TestAWordlessPictureIsThePictureAlone(t *testing.T) {
	at := time.Date(2026, 10, 9, 14, 7, 0, 0, time.Local)
	draw := func(c *home.Caption) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		newRenderer(img).draw(scene{now: at, phase: "idle", showCamera: true, camera: home.CameraView{
			Entity: "camera.front_door", Name: "Front door", Until: at.Add(30 * time.Second),
			Frame: white(showWide, showHigh), Caption: c}})
		return img
	}
	isWhite := func(img *image.RGBA, x, y int) bool { return img.RGBAAt(x, y) == color.RGBA{255, 255, 255, 255} }

	bare := draw(&home.Caption{ID: "1"})
	if !isWhite(bare, showWide/2, 20) {
		t.Error("the top strip is drawn over a picture sent with no words")
	}
	if !isWhite(bare, showWide/2, showHigh-36-20) {
		t.Error("a caption bar is drawn over a picture sent with no words")
	}
	if isWhite(bare, showWide/2, showHigh-10) {
		t.Error("the strip with tap to close is missing")
	}

	worded := draw(&home.Caption{ID: "1", Title: "Doorbell", Message: "Someone is here"})
	if isWhite(worded, showWide/2, 20) || isWhite(worded, showWide/2, showHigh-36-20) {
		t.Error("a picture with words lost its top strip or its caption bar")
	}
}
