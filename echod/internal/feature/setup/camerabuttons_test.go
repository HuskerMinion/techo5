package setup

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The box: a camera a line, an entity with a colon of its own, an empty line for a camera taking its
// buttons off, and a bad line refused by what is wrong with it.
func TestParseCameraButtonsText(t *testing.T) {
	got, problem := parseCameraButtonsText("camera.gate: Open gate=script.open_gate\r\n\n" +
		"go2rtc:porch: Unlock=lock.unlock:lock.front_door, Light=light.toggle:light.porch\nlocal:")
	want := map[string][]config.CameraButton{
		"camera.gate": {{Label: "Open gate", Service: "script.open_gate"}},
		"go2rtc:porch": {{Label: "Unlock", Service: "lock.unlock", Entity: "lock.front_door"},
			{Label: "Light", Service: "light.toggle", Entity: "light.porch"}},
		"local": nil,
	}
	if problem != "" || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v (%q), want %+v", got, problem, want)
	}
	for in, says := range map[string]string{
		"camera.gate Open gate=script.open_gate":         "colon",
		"camera.gate:Open gate=script.open_gate":         "colon",
		": Open gate=script.open_gate":                   "colon",
		"camera.gate: Open gate=script\n":                "camera.gate: ",
		"camera.gate: A=a.a\ncamera.gate: B=b.b":         "two lines",
		"camera.gate: A=a.a, B=b.b, C=c.c, D=d.d, E=e.e": "room for 4",
	} {
		if _, problem := parseCameraButtonsText(in); !strings.Contains(problem, says) {
			t.Errorf("%q gave %q, want refused with %q", in, problem, says)
		}
	}
}

// Saved and shown again: the box shows what was saved, escaped on the page, a refused save keeps what
// was there, and an emptied box takes every button off.
func TestCameraButtonsSaveAndShow(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	post := func(text, was string) string {
		r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(url.Values{"buttons": {text}, "was_buttons": {was}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return saves["camerabuttons"](r)
	}
	if problem := post("camera.gate: <Gate>=script.open_gate", ""); problem != "" {
		t.Fatal(problem)
	}
	want := []config.CameraButton{{Label: "<Gate>", Service: "script.open_gate"}}
	if got := config.Get().Home.CameraButtons["camera.gate"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("saved %+v, want %+v", got, want)
	}
	text := cameraButtonsText(nil, config.Get().Home.CameraButtons)
	if text != "camera.gate: <Gate>=script.open_gate" {
		t.Errorf("the box shows %q", text)
	}
	if problem := post("camera.gate: Bad=script", text); problem == "" {
		t.Error("a bad button was saved")
	}
	if got := config.Get().Home.CameraButtons["camera.gate"]; !reflect.DeepEqual(got, want) {
		t.Errorf("a refused save left %+v, want %+v", got, want)
	}
	if problem := post("camera.gate:  <Gate>=script.open_gate\r\n", text); problem != "" {
		t.Errorf("the box saved back as shown gave %q", problem)
	}
	if problem := post("", text); problem != "" || len(config.Get().Home.CameraButtons) != 0 {
		t.Errorf("an emptied box left %+v (%q), want no buttons", config.Get().Home.CameraButtons, problem)
	}
}
