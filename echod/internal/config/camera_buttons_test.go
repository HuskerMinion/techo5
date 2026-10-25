package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A camera's buttons are kept by its entity, set and taken off one camera at a time or all at once, and
// a device that never had any writes nothing for them.
func TestCameraButtonsAreKept(t *testing.T) {
	Use(filepath.Join(t.TempDir(), "state.json"))
	gate := []CameraButton{{Label: "Open gate", Service: "script.open_gate"}}
	door := []CameraButton{{Label: "Unlock", Service: "lock.unlock", Entity: "lock.front_door"}}
	if err := Set().Home().CameraButtons("camera.gate", gate); err != nil {
		t.Fatal(err)
	}
	if err := Set().Home().CameraButtons("go2rtc:porch", door); err != nil {
		t.Fatal(err)
	}
	got := Get().Home.CameraButtons
	if !reflect.DeepEqual(got["camera.gate"], gate) || !reflect.DeepEqual(got["go2rtc:porch"], door) {
		t.Fatalf("buttons %+v, want the gate's and the porch's", got)
	}
	if err := Set().Home().CameraButtons("camera.gate", nil); err != nil {
		t.Fatal(err)
	}
	if _, has := Get().Home.CameraButtons["camera.gate"]; has || len(Get().Home.CameraButtons) != 1 {
		t.Errorf("buttons %+v after taking the gate's off, want the porch's alone", Get().Home.CameraButtons)
	}
	if err := Set().Home().AllCameraButtons(map[string][]CameraButton{"local": gate, "camera.deck": nil}); err != nil {
		t.Fatal(err)
	}
	if got := Get().Home.CameraButtons; len(got) != 1 || !reflect.DeepEqual(got["local"], gate) {
		t.Errorf("buttons %+v after setting them all, want the device's own camera's alone", got)
	}
	if err := Set().Home().AllCameraButtons(nil); err != nil {
		t.Fatal(err)
	}
	if Get().Home.CameraButtons != nil {
		t.Errorf("buttons %+v after taking them all off, want none", Get().Home.CameraButtons)
	}
	file, err := Export(Get(), "test", "v0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), "camera_buttons") {
		t.Error("a device with no camera buttons wrote them in its settings file")
	}
}

// Camera buttons can open gates and unlock doors, so a settings file never brings them in: the device
// keeps its own, whatever the file says.
func TestCameraButtonsStayTheDevicesOwn(t *testing.T) {
	from := Defaults()
	from.Home.CameraButtons = map[string][]CameraButton{
		"camera.gate": {{Label: "Open gate", Service: "script.open_gate"}},
		"reolink:0":   {{Label: "Unlock", Service: "lock.unlock", Entity: "lock.front_door"}},
	}
	file, err := Export(from, "test", "v0")
	if err != nil {
		t.Fatal(err)
	}
	own := Defaults()
	own.Home.CameraButtons = map[string][]CameraButton{"go2rtc:porch": {{Label: "Chime", Service: "script.chime"}}}
	r, err := Restored(own, file, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Config.Home.CameraButtons, own.Home.CameraButtons) {
		t.Errorf("restored %+v, want the device's own %+v", r.Config.Home.CameraButtons, own.Home.CameraButtons)
	}
}
