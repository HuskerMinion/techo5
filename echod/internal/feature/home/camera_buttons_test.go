package home

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// home_camera_buttons' list: commas or new lines between buttons, an entity after the action or none,
// and a bad item refusing the whole set by what is wrong with it.
func TestParseCameraButtons(t *testing.T) {
	got, err := ParseCameraButtons(" Open gate = script.open_gate ,\nUnlock=lock.unlock:lock.front_door\r\n\n")
	want := []config.CameraButton{{Label: "Open gate", Service: "script.open_gate"},
		{Label: "Unlock", Service: "lock.unlock", Entity: "lock.front_door"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v (%v), want %+v", got, err, want)
	}
	if got, err := ParseCameraButtons("  "); err != nil || len(got) != 0 {
		t.Errorf("an empty list gave %+v (%v), want none", got, err)
	}
	if got, err := ParseCameraButtons("Öffnen=script.tor_2"); err != nil || len(got) != 1 {
		t.Errorf("a label in another language gave %+v (%v)", got, err)
	}
	for in, says := range map[string]string{
		"Open gate":                            "Label=domain.service",
		"=script.open_gate":                    "Label=domain.service",
		"Open gate=":                           "Label=domain.service",
		"Open gate=script":                     "domain.service",
		"Open gate=Script.Open_Gate":           "domain.service",
		"Open gate=script.open gate":           "domain.service",
		"Open gate=script.open-gate":           "domain.service",
		"Open gate=script.open.gate":           "domain.service",
		"Unlock=lock.unlock:front_door":        "domain.object",
		"Unlock=lock.unlock:lock.Front_Door":   "domain.object",
		"A label far too long for the bar=a.b": "24 characters",
		"Tab\there=script.x":                   "one line",
		"Ok=script.a, Bad=script":              "domain.service",
		"A=a.a, B=b.b, C=c.c, D=d.d, E=e.e":    "room for 4",
	} {
		got, err := ParseCameraButtons(in)
		if err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%q gave %+v (%v), want refused with %q", in, got, err, says)
		}
	}
	// The error names a button by its place, never by its text: a refused change is logged.
	if _, err := ParseCameraButtons("Ok=script.a, Front gate secret=script"); err == nil || !strings.Contains(err.Error(), "button 2") || strings.Contains(err.Error(), "secret") {
		t.Errorf("a refused button gave %v, want it named by place and its label left out", err)
	}
}

// What FormatCameraButtons writes, ParseCameraButtons reads back the same.
func TestFormatCameraButtonsReadsBack(t *testing.T) {
	in := []config.CameraButton{{Label: "Open gate", Service: "script.open_gate"},
		{Label: "Unlock", Service: "lock.unlock", Entity: "lock.front_door"}}
	got, err := ParseCameraButtons(FormatCameraButtons(in))
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Errorf("read back %+v (%v), want %+v", got, err, in)
	}
}

// The action's camera is one word, and an empty list takes that camera's buttons off.
func TestSetCameraButtons(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	gate := []config.CameraButton{{Label: "Open gate", Service: "script.open_gate"}}
	if err := SetCameraButtons(" go2rtc:gate ", gate); err != nil {
		t.Fatal(err)
	}
	if got := CameraButtons("go2rtc:gate"); !reflect.DeepEqual(got, gate) {
		t.Errorf("buttons %+v, want the gate's", got)
	}
	for _, bad := range []string{"", "  ", "front door"} {
		if err := SetCameraButtons(bad, gate); err == nil {
			t.Errorf("camera %q was taken", bad)
		}
	}
	if err := SetCameraButtons("go2rtc:gate", nil); err != nil {
		t.Fatal(err)
	}
	if got := CameraButtons("go2rtc:gate"); len(got) != 0 {
		t.Errorf("buttons %+v after an empty list, want none", got)
	}
}
