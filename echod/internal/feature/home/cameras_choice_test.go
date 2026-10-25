package home

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// home_cameras' argument: a list, "none" in any case for no Home Assistant camera, empty for all.
func TestParseCameras(t *testing.T) {
	cases := []struct {
		in    string
		none  bool
		names string
	}{
		{"", false, ""},
		{"  ", false, ""},
		{"none", true, ""},
		{" None ", true, ""},
		{"NONE", true, ""},
		{"camera.front_door=Front door,camera.deck=Deck", false, "camera.front_door=Front door|camera.deck=Deck|"},
		{" camera.garage , ,camera.yard = Yard ", false, "camera.garage=garage|camera.yard=Yard|"},
		{"camera.none", false, "camera.none=none|"},
	}
	for _, c := range cases {
		cams, none := ParseCameras(c.in)
		var got string
		for _, cam := range cams {
			got += cam.Entity + "=" + cam.Name + "|"
		}
		if none != c.none || got != c.names {
			t.Errorf("ParseCameras(%q) = %q none %v, want %q none %v", c.in, got, none, c.names, c.none)
		}
	}
}

// Cameras follows the choice: All is Home Assistant's list, None is none of it, a list is the list.
func TestCamerasFollowTheChoice(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := listHACameras
	t.Cleanup(func() { listHACameras = was })
	listHACameras = func(*Feature) []config.Camera {
		return []config.Camera{{Entity: "camera.bedroom", Name: "Bedroom"}, {Entity: "camera.porch", Name: "Porch"}}
	}
	f := newFeature(t)
	ha := func() (out []string) {
		for _, c := range f.Cameras() {
			if c.Entity != LocalCamera {
				out = append(out, c.Entity)
			}
		}
		return out
	}
	if got := ha(); len(got) != 2 {
		t.Errorf("never set: %v, want all of Home Assistant's", got)
	}
	cams, none := ParseCameras("None")
	if err := f.setCameras(cams, none); err != nil {
		t.Fatal(err)
	}
	if got := ha(); len(got) != 0 {
		t.Errorf("none: %v", got)
	}
	cams, none = ParseCameras("camera.porch=Porch")
	if err := f.setCameras(cams, none); err != nil {
		t.Fatal(err)
	}
	if got := ha(); len(got) != 1 || got[0] != "camera.porch" {
		t.Errorf("a list: %v", got)
	}
	cams, none = ParseCameras("")
	if err := f.setCameras(cams, none); err != nil {
		t.Fatal(err)
	}
	if got := ha(); len(got) != 2 {
		t.Errorf("empty again: %v, want all", got)
	}
}
