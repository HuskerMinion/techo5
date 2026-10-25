package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// Which of Home Assistant's cameras the device lists, kept across a restart: never set is All, None is
// an empty list chosen on purpose, and a list is that list. Clearing the list goes back to All.
func TestCameraChoiceIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	reload := func() Home {
		t.Helper()
		st, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return st.Get().Home
	}
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if h := st.Get().Home; !h.AllHACameras() || h.NoHACameras() {
		t.Errorf("never set: all %v none %v, want all", h.AllHACameras(), h.NoHACameras())
	}

	if err := st.Set().Home().NoCameras(); err != nil {
		t.Fatal(err)
	}
	if h := reload(); !h.NoHACameras() || h.AllHACameras() || len(h.Cameras) != 0 {
		t.Errorf("none, reloaded: %+v", h)
	}

	list := []Camera{{Entity: "camera.porch", Name: "Porch"}}
	if err := st.Set().Home().Cameras(list); err != nil {
		t.Fatal(err)
	}
	if h := reload(); h.AllHACameras() || h.NoHACameras() || len(h.Cameras) != 1 || h.Cameras[0] != list[0] {
		t.Errorf("a list, reloaded: %+v", h)
	}

	if err := st.Set().Home().Cameras(nil); err != nil {
		t.Fatal(err)
	}
	if h := reload(); !h.AllHACameras() || h.CamerasChosen {
		t.Errorf("cleared, reloaded: %+v, want all", h)
	}
}

// A list saved by a build from before the choice has no cameras_chosen in it, and stays that list; a
// device never set writes nothing new to its file.
func TestAnOlderCameraListStaysAList(t *testing.T) {
	var h Home
	if err := json.Unmarshal([]byte(`{"cameras":[{"entity":"camera.porch","name":"Porch"}]}`), &h); err != nil {
		t.Fatal(err)
	}
	if h.AllHACameras() || h.NoHACameras() || len(h.Cameras) != 1 {
		t.Errorf("an older list reads as all %v none %v", h.AllHACameras(), h.NoHACameras())
	}
	b, _ := json.Marshal(Home{})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["cameras_chosen"]; ok {
		t.Error("a device never set writes cameras_chosen")
	}
}

// A settings file carries the choice, and one that says All does not undo None or a list chosen on
// the device it is restored on.
func TestARestoreDoesNotWidenTheCameras(t *testing.T) {
	none := Defaults()
	none.Home.CamerasChosen = true
	list := Defaults()
	list.Home.Cameras = []Camera{{Entity: "camera.porch", Name: "Porch"}}
	list.Home.CamerasChosen = true

	restore := func(here, from Config) Home {
		t.Helper()
		file, err := Export(from, "m", "v0")
		if err != nil {
			t.Fatal(err)
		}
		r, err := Restored(here, file, "m")
		if err != nil {
			t.Fatal(err)
		}
		return r.Config.Home
	}
	if h := restore(none, Defaults()); !h.NoHACameras() {
		t.Errorf("a file saying All undid None: %+v", h)
	}
	if h := restore(list, Defaults()); len(h.Cameras) != 1 || !h.CamerasChosen {
		t.Errorf("a file saying All undid a list: %+v", h)
	}
	if h := restore(Defaults(), none); !h.NoHACameras() {
		t.Errorf("a file saying None did not carry it: %+v", h)
	}
	if h := restore(none, list); len(h.Cameras) != 1 || h.Cameras[0].Entity != "camera.porch" {
		t.Errorf("a file with a list did not replace None: %+v", h)
	}
}
