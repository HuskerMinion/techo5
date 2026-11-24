package setup

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// The setup page offers Cancel timer for a timer set here, not for a silent one (timer_start_silent),
// which the automation whose wait it is ends itself (#723).
func TestTimerDrawerLeavesASilentTimerToItsAutomation(t *testing.T) {
	w := httptest.NewRecorder()
	timerDrawer(w, "tok", timer.Countdown{ID: "local:pasta", Name: "Pasta", Left: time.Minute, Active: true, Local: true})
	if b := w.Body.String(); !strings.Contains(b, "Cancel timer") || !strings.Contains(b, "this device") {
		t.Errorf("a timer set here has no Cancel timer: %s", b)
	}

	w = httptest.NewRecorder()
	timerDrawer(w, "tok", timer.Countdown{ID: "local:join", Name: "Pairing", Left: time.Minute, Active: true, Local: true, Silent: true})
	if b := w.Body.String(); strings.Contains(b, "Cancel timer") || !strings.Contains(b, "automation") {
		t.Errorf("a silent timer is offered to be canceled: %s", b)
	}
}
