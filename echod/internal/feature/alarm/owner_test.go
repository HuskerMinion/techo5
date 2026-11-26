package alarm

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// alarms_list says which add-on set a timer, and its label says so too.
func TestAListedTimerSaysItsAddon(t *testing.T) {
	l := listing(time.Now(), View{}, []timer.Countdown{{ID: "local:a", Name: "Meals: rice", Left: time.Minute, Total: time.Minute,
		Active: true, Local: true, Owner: "meals", OwnName: "rice"}}, nil)
	if len(l.Timers) != 1 || l.Timers[0].Owner != "meals" || l.Timers[0].Label != "Meals: rice" {
		t.Errorf("listed %+v", l.Timers)
	}
}
