package home

import (
	"context"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A pixel in each source's own color reads back as that color's dBZ; a pale edge a tile server
// blended into the ground, and a color from neither table, read as no rain.
func TestReflectivityReadsBothSourcesColors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table []paletteColor
	}{{"RainViewer", rainViewerColors}, {"NWS", iemColors}} {
		img := image.NewRGBA(image.Rect(0, 0, 4, 1))
		want := tc.table[len(tc.table)/2]
		set := func(x int, r, g, b, a uint8) { copy(img.Pix[x*4:], []uint8{r, g, b, a}) }
		set(0, want.r, want.g, want.b, 255)
		set(1, want.r, want.g, want.b, 120) // blended into the ground
		set(2, 255, 0, 255, 255)            // nobody's rain
		// the other pixel stays transparent
		got := reflectivity(img, tc.table)
		// A table may give one color to several dBZ (RainViewer's top end is one color from 75 up):
		// any of them is right.
		same := false
		for _, c := range tc.table {
			same = same || c.dBZ == got[0] && c.r == want.r && c.g == want.g && c.b == want.b
		}
		if !same {
			t.Errorf("%s: its own color read as %v dBZ, want %v or another of the same color", tc.name, got[0], want.dBZ)
		}
		for i := 1; i < 4; i++ {
			if tc.name == "NWS" && i == 2 {
				continue // the NWS palette ends in magenta and white for hail, so this one is rain there
			}
			if got[i] != noRain {
				t.Errorf("%s: pixel %d read as %v, want no rain", tc.name, i, got[i])
			}
		}
	}
}

// The ramp is clear below light rain and ever more solid as the rain gets heavier.
func TestTheRampGetsSolider(t *testing.T) {
	if a := rampAt(5).a; a != 0 {
		t.Errorf("5 dBZ painted at alpha %v, want clear", a)
	}
	last := float32(-1)
	for v := float32(12); v <= 70; v += 2 {
		a := rampAt(v).a
		if a < last {
			t.Errorf("alpha fell from %v to %v at %v dBZ", last, a, v)
		}
		last = a
	}
}

// Painting leaves the ground alone where there is no rain and colors it where there is.
func TestPaintRainOnlyWhereItRains(t *testing.T) {
	dst := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for i := range dst.Pix {
		dst.Pix[i] = 50
	}
	field := make([]float32, 40*20)
	for i := range field {
		field[i] = noRain
	}
	for y := 5; y < 15; y++ {
		for x := 25; x < 35; x++ {
			field[y*40+x] = 45
		}
	}
	paintRain(dst, field, 40, 20)
	if p := dst.Pix[(10*40+3)*4:]; p[0] != 50 || p[1] != 50 {
		t.Errorf("dry ground changed to %v", p[:3])
	}
	if p := dst.Pix[(10*40+30)*4:]; p[0] < 150 {
		t.Errorf("45 dBZ painted as %v, want orange", p[:3])
	}
}

func TestTheSourceFollowsTheSettingAndHome(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	omaha, london := [2]float64{41.26, -95.94}, [2]float64{51.5, -0.12}
	for _, tc := range []struct {
		setting string
		at      [2]float64
		want    string
	}{
		{"", omaha, "NWS"}, {"", london, "RainViewer"},
		{config.RadarRainViewer, omaha, "RainViewer"}, {config.RadarNWS, london, "NWS"},
	} {
		if err := config.Set().Home().RadarSource(tc.setting); err != nil {
			t.Fatal(err)
		}
		if got := radarSourceFor(tc.at[0], tc.at[1]).name; got != tc.want {
			t.Errorf("setting %q at %v: %s, want %s", tc.setting, tc.at, got, tc.want)
		}
	}
	for lat, lon := range map[float64]float64{61.2: -149.9, 21.3: -157.9, 18.4: -66.1} { // Anchorage, Honolulu, San Juan
		if inLower48(lat, lon) {
			t.Errorf("%v, %v counted as the lower 48, which the NWS composite covers alone", lat, lon)
		}
	}
}

// The NWS frames are the newest composite and the ones ten, twenty... minutes before it, oldest first,
// each from its own layer and kept under its own time.
func TestTheNWSFramesStepBackFromTheNewest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"meta": {"product": "N0Q", "valid": "2026-09-26T12:15:00Z"}}`)
	}))
	defer srv.Close()
	defer func(n string) { iemNow = n }(iemNow)
	iemNow = srv.URL
	frames, err := nws.frames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != radarFrames {
		t.Fatalf("%d frames, want %d", len(frames), radarFrames)
	}
	newest := frames[len(frames)-1]
	if !newest.at.Equal(time.Date(2026, 9, 26, 12, 15, 0, 0, time.UTC)) || frames[0].at.After(newest.at) {
		t.Errorf("frames run %v .. %v, want oldest first ending 12:15", frames[0].at, newest.at)
	}
	if got := newest.tile(8, 58, 96); got != fmt.Sprintf(iemTiles, "nexrad-n0q-900913", 8, 58, 96) {
		t.Errorf("the newest frame's tile is %s", got)
	}
	if got := frames[0].tile(8, 1, 2); got != fmt.Sprintf(iemTiles, fmt.Sprintf("nexrad-n0q-900913-m%02dm", (radarFrames-1)*10), 8, 1, 2) {
		t.Errorf("the oldest frame's tile is %s", got)
	}
	if frames[0].key == newest.key {
		t.Error("two frames kept under one key")
	}
}

func TestCloudsOnlyWhereASatelliteSees(t *testing.T) {
	for lon, want := range map[float64]string{-95.9: "GOES-East", -74: "GOES-East", -122.3: "GOES-West", -157.9: "GOES-West", 10: "", 139.7: ""} {
		got := cloudLayer(lon)
		if (want == "") != (got == "") || want != "" && got[:len(want)] != want {
			t.Errorf("clouds at %v°: %q, want %s", lon, got, want)
		}
	}
}
