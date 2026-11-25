//go:build !dot

package display

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// The Alarms card offers Stop for a timer set here, and shows a silent one (timer_start_silent) without
// it, as Home Assistant's are shown: the automation whose wait it is ends it (#723).
func TestTimerRowsShowASilentTimerWithoutStop(t *testing.T) {
	sv := sheetView{timers: []timer.Countdown{
		{ID: "local:pasta", Name: "Pasta", Left: time.Minute, Active: true, Local: true},
		{ID: "local:join", Name: "Pairing", Left: time.Minute, Active: true, Local: true, Silent: true},
	}}
	rows := timerRows(sv)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want two timers and New timer", len(rows))
	}
	if r := rows[0]; r.kind != ctlButton || r.button != tr("stop") || r.sub != tr("ui_set_here")+" · Pasta" {
		t.Errorf("the timer set here is %+v, want it with Stop", r)
	}
	if r := rows[1]; r.kind != ctlValue || r.button != "" || r.sub != tr("ui_from_home_assistant")+" · Pairing" {
		t.Errorf("the silent timer is %+v, want it shown as Home Assistant's, without Stop", r)
	}
}
