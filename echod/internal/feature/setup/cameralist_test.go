package setup

import (
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The order box: names in any case, the same name taken in list order, an unknown name refused.
func TestCameraOrder(t *testing.T) {
	cams := []config.Camera{{Entity: "local", Name: "This Show"}, {Entity: "go2rtc:a", Name: "Yard"},
		{Entity: "go2rtc:b", Name: "Yard"}, {Entity: "camera.porch", Name: "Porch"}}
	got, problem := cameraOrder(cams, "porch\r\n\r\n yard \nYARD\n")
	if problem != "" || strings.Join(got, ",") != "camera.porch,go2rtc:a,go2rtc:b" {
		t.Errorf("order %v (%q), want the porch then both yards", got, problem)
	}
	if got, problem := cameraOrder(cams, " \n"); problem != "" || len(got) != 0 {
		t.Errorf("an empty box gave %v (%q), want the usual order", got, problem)
	}
	if _, problem := cameraOrder(cams, "Porch\nGarage"); !strings.Contains(problem, "Garage") {
		t.Errorf("an unknown name gave %q, want it refused by name", problem)
	}
}
