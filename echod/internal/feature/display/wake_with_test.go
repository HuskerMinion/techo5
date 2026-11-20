//go:build !dot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The alarm editor's Wake with row (#651) is an alarm's that rings, not a light-only or a silent one's;
// its list keeps the music the alarm wakes with, and the alarm sound first, and saving keeps the choice.
func TestTheAlarmEditorSetsWakeWith(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	has := func(al config.Alarm) bool {
		for _, r := range alarmEditorCard(sheetView{}, alarmDraft{alarm: al}).rows {
			if r.id == "e.wake" {
				return true
			}
		}
		return false
	}
	if !has(config.Alarm{}) || has(config.Alarm{LightOnly: true}) || has(config.Alarm{Silent: true}) || has(config.Alarm{Remind: true}) {
		t.Error("Wake with is offered to the wrong alarms")
	}

	radio := config.WakeWith{Kind: config.WakeRadio, Name: "WXYZ", URI: "http://radio.invalid/wxyz.mp3"}
	d := &Display{poke: make(chan struct{}, 1)}
	d.EditNewAlarm()
	d.editDraft(func(dr *alarmDraft) { dr.alarm.Wake = radio })
	p, ok := pickerFor("e.wake", sheetView{draft: d.draft})
	if !ok || len(p.opts) < 2 || p.opts[0] != "Alarm sound" || p.opts[p.cur] != "Radio: WXYZ" {
		t.Fatalf("the list is %q at %d", p.opts, p.cur)
	}
	if v := wakeValue(radio); v != "WXYZ" {
		t.Errorf("the row says %q", v)
	}
	d.actionTap("save")
	list := config.Get().Alarms.List
	if len(list) != 1 || list[0].Wake != radio {
		t.Fatalf("saved %+v", list)
	}

	d.mu.Lock()
	d.draft = &alarmDraft{alarm: list[0]}
	d.mu.Unlock()
	d.choose("e.wake", 0)
	if d.draft.alarm.Wake.Music() {
		t.Errorf("choosing the alarm sound left %+v", d.draft.alarm.Wake)
	}
}
