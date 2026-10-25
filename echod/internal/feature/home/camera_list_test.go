package home

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func entities(cams []config.Camera) string {
	var out []string
	for _, c := range cams {
		out = append(out, c.Entity)
	}
	return strings.Join(out, ",")
}

// The order chosen comes first, as chosen; the rest follow as they were; a camera gone is passed over.
func TestOrdered(t *testing.T) {
	cams := []config.Camera{{Entity: LocalCamera}, {Entity: "camera.porch"}, {Entity: "go2rtc:door"}, {Entity: "go2rtc:yard"}}
	for _, c := range []struct {
		order []string
		want  string
	}{
		{nil, "local,camera.porch,go2rtc:door,go2rtc:yard"},
		{[]string{"go2rtc:door", "go2rtc:yard"}, "go2rtc:door,go2rtc:yard,local,camera.porch"},
		{[]string{"go2rtc:yard", "camera.gone", "local"}, "go2rtc:yard,local,camera.porch,go2rtc:door"},
	} {
		if got := entities(ordered(cams, c.order)); got != c.want {
			t.Errorf("ordered(%v) = %s, want %s", c.order, got, c.want)
		}
	}
}

// The list follows the order saved, with Home Assistant's cameras, go2rtc's and the rest.
func TestCamerasFollowTheOrder(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := listHACameras
	t.Cleanup(func() { listHACameras = was })
	listHACameras = func(*Feature) []config.Camera { return []config.Camera{{Entity: "camera.porch", Name: "Porch"}} }
	if err := config.Set().Home().Go2RTC(config.Go2RTC{Base: "http://go2rtc.invalid:1984", Cameras: []config.Camera{
		{Entity: "go2rtc:door", Name: "door"}, {Entity: "go2rtc:yard", Name: "yard"}}}); err != nil {
		t.Fatal(err)
	}
	f := newFeature(t)
	if err := SetCameraList(false, []string{"go2rtc:yard", "go2rtc:door"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range f.Cameras() {
		if c.Entity != LocalCamera {
			got = append(got, c.Entity)
		}
	}
	if strings.Join(got, ",") != "go2rtc:yard,go2rtc:door,camera.porch" {
		t.Errorf("list %v, want the yard, the door, then the porch", got)
	}
}

// A go2rtc stream's own name is shown and said; empty, the stream's name in go2rtc comes back.
func TestGo2RTCNames(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Go2RTC(config.Go2RTC{Base: "http://go2rtc.invalid:1984", Cameras: []config.Camera{
		{Entity: "go2rtc:front_sub", Name: "front_sub"}, {Entity: "go2rtc:yard", Name: "Yard"}}}); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Home().Go2RTCNames(map[string]string{"go2rtc:front_sub": " Doorbell ", "go2rtc:yard": ""}); err != nil {
		t.Fatal(err)
	}
	cams := config.Get().Home.Go2RTC.Cameras
	if cams[0].Name != "Doorbell" || cams[1].Name != "yard" {
		t.Errorf("names %q, %q; want Doorbell, yard", cams[0].Name, cams[1].Name)
	}
	if cams[0].Entity != "go2rtc:front_sub" {
		t.Errorf("the stream became %q; a name changes nothing in go2rtc", cams[0].Entity)
	}
}
