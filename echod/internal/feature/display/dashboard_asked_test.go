//go:build !dot && !spot

package display

import (
	"testing"
	"time"
)

// A dashboard Home Assistant put up stays past the time one opened by a finger is forgotten, until
// Home Assistant takes it down.
func TestDashboardShownByHomeAssistantStays(t *testing.T) {
	d := &Display{}
	d.dashboardAsked(true)
	if !d.dash || !d.dashHeld {
		t.Fatalf("dashboard_show: dash %v held %v, want both", d.dash, d.dashHeld)
	}
	d.dashTouched = time.Now().Add(-20 * time.Minute)
	d.dashScene(&scene{phase: "idle"}, false)
	if !d.dash {
		t.Fatal("a dashboard put up by Home Assistant was forgotten after the return time")
	}

	d.dashShowing = true
	d.dashboardAsked(false)
	if d.dash {
		t.Fatal("dashboard_hide left the dashboard up")
	}
}

// One opened by a finger is still forgotten.
func TestDashboardOpenedByHandIsForgotten(t *testing.T) {
	d := &Display{}
	d.dash, d.dashTouched = true, time.Now().Add(-20*time.Minute)
	d.dashScene(&scene{phase: "idle"}, false)
	if d.dash {
		t.Fatal("a dashboard opened by hand stayed past the return time")
	}
}
