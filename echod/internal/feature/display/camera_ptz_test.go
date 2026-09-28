//go:build !dot && !spot

package display

import "testing"

// An arrow on an ONVIF camera is onvif.ptz's continuous move for a moment, the way that arrow points;
// on a script it is the script, told which way.
func TestPTZCall(t *testing.T) {
	d, s, data := ptzCall("camera.porch", 2)
	if d != "onvif" || s != "ptz" || data["move_mode"] != "ContinuousMove" || data["pan"] != "LEFT" || data["entity_id"] != "camera.porch" {
		t.Errorf("left on an ONVIF camera: %s.%s %v", d, s, data)
	}
	if _, tilt := data["tilt"]; tilt {
		t.Errorf("left on an ONVIF camera also tilts it: %v", data)
	}
	d, s, data = ptzCall("script.porch_ptz", 0)
	v, _ := data["variables"].(map[string]any)
	if d != "script" || s != "turn_on" || data["entity_id"] != "script.porch_ptz" || v["direction"] != "up" {
		t.Errorf("up on a script: %s.%s %v", d, s, data)
	}
}
