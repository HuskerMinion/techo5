//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The camera view's strips: solid, a shade only over the name's strip; at Fade, a deeper shade at the
// very edge of the screen that thins into the picture, under the name and at the foot alike. With
// SHOW_PREVIEW set, each is written there to look at.
func TestTheCameraStrips(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 10, 25, 11, 18, 0, 0, time.Local)
	white := color.RGBA{0xf0, 0xf0, 0xf0, 0xff}
	frame := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
	draw.Draw(frame, frame.Rect, image.NewUniform(white), image.Point{}, draw.Src)
	view := home.CameraView{Entity: "camera.front_door", Name: "Front door", Frame: frame, Until: at.Add(time.Minute)}
	paint := func() *renderer {
		img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
		r := newRenderer(img)
		r.draw(scene{now: at, phase: "idle", showCamera: true, camera: view})
		return r
	}
	luma := func(r *renderer, y int) int {
		c := r.dst.RGBAAt(r.w/2, y)
		return int(c.R) + int(c.G) + int(c.B)
	}
	save := func(r *renderer, name string) {
		if dir := os.Getenv("SHOW_PREVIEW"); dir != "" {
			if f, err := os.Create(filepath.Join(dir, name+".png")); err == nil {
				_ = png.Encode(f, r.dst)
				f.Close()
			}
		}
	}

	solid := paint()
	save(solid, "camera-strips-solid")
	if l := luma(solid, solid.s(70)); l < 3*0xe8 {
		t.Errorf("solid: the picture under the name's strip is shaded (%d)", l)
	}
	if err := config.Set().Screen().CameraBands("fade"); err != nil {
		t.Fatal(err)
	}
	if !cameraBandsFade() {
		t.Fatal("Fade not read back")
	}
	fade := paint()
	save(fade, "camera-strips-fade")
	top, mid, clear := luma(fade, 1), luma(fade, fade.s(70)), luma(fade, fade.h/2)
	if !(top < mid && mid < clear) {
		t.Errorf("fade: the top strip is %d at the edge, %d lower, %d in the middle: not fading in", top, mid, clear)
	}
	foot, above := luma(fade, fade.h-2), luma(fade, fade.h-fade.s(80))
	if !(foot < above && above < clear) {
		t.Errorf("fade: the foot is %d at the edge, %d higher, %d in the middle: not fading in", foot, above, clear)
	}
}

// Camera picture and Camera strips are rows of the screen's settings, beside Camera time, and settings
// in Home Assistant.
func TestTheCameraLookSettings(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if s := cameraFitSelect(); s.ObjectID != "screen_camera_picture" || len(s.Options) != 3 {
		t.Errorf("Camera picture select %q with %d choices", s.ObjectID, len(s.Options))
	}
	setCameraFit(1)
	if config.Get().Screen.CameraFit != "fill" || home.CameraFit() != "fill" {
		t.Errorf("Fill the screen kept as %q", config.Get().Screen.CameraFit)
	}
	setCameraFit(0)
	if config.Get().Screen.CameraFit != "" {
		t.Errorf("Whole picture kept as %q", config.Get().Screen.CameraFit)
	}
	if v, ok := pickerFor("camfit", sheetView{}); !ok || v.title != "Camera picture" {
		t.Errorf("no picker for Camera picture: %+v", v)
	}
}

// Camera strips shade darkens the strips from Light to Strong, solid and fading alike, Medium being the
// look the camera view always had; and it is a row of the settings beside the others.
func TestTheCameraStripsShade(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if cameraStripShade(false) != shade {
		t.Errorf("Medium solid is %v, not the page's shade %v", cameraStripShade(false), shade)
	}
	var last [2]uint8
	for i := range cameraShades {
		setCameraShade(i)
		for k, fade := range []bool{false, true} {
			a := cameraStripShade(fade).A
			if i > 0 && a <= last[k] {
				t.Errorf("%s (fade %v) is no darker than the one before: %d", cameraShades[i].label, fade, a)
			}
			last[k] = a
		}
	}
	if v, ok := pickerFor("camshade", sheetView{}); !ok || len(v.opts) != 3 {
		t.Errorf("no picker for Camera strips shade: %+v", v)
	}
}

// With fading strips the name and the hint are in one face, each as far from its edge of the screen: the
// first row of the name's ink as far from the top as the last row of the hint's from the foot, give or
// take what the letters themselves add.
func TestTheCameraWordsSitAlike(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().CameraBands("fade"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 25, 11, 43, 0, 0, time.Local)
	frame := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)) // black: only the words are bright
	view := home.CameraView{Entity: "camera.front_door", Name: "Mirilla", Frame: frame, Until: at.Add(time.Hour)}
	img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
	r := newRenderer(img)
	r.draw(scene{now: at, phase: "idle", showCamera: true, camera: view})
	if dir := os.Getenv("SHOW_PREVIEW"); dir != "" {
		if f, err := os.Create(filepath.Join(dir, "camera-words-fade.png")); err == nil {
			_ = png.Encode(f, r.dst)
			f.Close()
		}
	}
	inked := func(y int) bool {
		for x := r.margin; x < r.w/3; x++ {
			c := r.dst.RGBAAt(x, y)
			if int(c.R)+int(c.G)+int(c.B) > 300 {
				return true
			}
		}
		return false
	}
	first, last := -1, -1
	for y := 0; y < r.h/3; y++ {
		if inked(y) {
			first = y
			break
		}
	}
	for y := r.h - 1; y > 2*r.h/3; y-- {
		if inked(y) {
			last = y
			break
		}
	}
	if first < 0 || last < 0 {
		t.Fatalf("no words found: name from %d, hint to %d", first, last)
	}
	// "Mirilla" has no descender and "tap to close" a p: the hint's ink runs a descender lower.
	top, foot := first, r.h-1-last
	if d := top - foot; d < -r.s(14) || d > r.s(14) {
		t.Errorf("the name's ink starts %dpx from the top, the hint's ends %dpx from the foot", top, foot)
	}
	// The sides as far in as the top: the name's first column of ink, and the clock's last.
	col := func(x0, x1, step int) int {
		for x := x0; x != x1; x += step {
			for y := 0; y < r.h/8; y++ {
				if c := r.dst.RGBAAt(x, y); int(c.R)+int(c.G)+int(c.B) > 300 {
					return x
				}
			}
		}
		return -1
	}
	left, right := col(0, r.w/4, 1), r.w-1-col(r.w-1, 3*r.w/4, -1)
	for _, side := range []int{left, right} {
		if d := side - top; d < -r.s(8) || d > r.s(8) {
			t.Errorf("the words are %dpx from the left and %dpx from the right, %dpx from the top", left, right, top)
			break
		}
	}
}
