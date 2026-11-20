package setup

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// An alarm added or edited on the page can wake with music (#651); its list offers the music there is
// and shows what was saved, and the alarm sound is chosen by sending nothing.
func TestAnAlarmCanWakeWithMusicFromThePage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	radio := config.WakeWith{Kind: config.WakeRadio, Name: "WXYZ & Co", URI: "http://radio.invalid/wxyz.mp3"}
	add := url.Values{"kind": {"alarm"}, "at": {"06:45"}, "alarm_sunrise": {"0"}, "wake": {wakeValue(radio)}}
	if p := addFromPage(form(add)); p != "" {
		t.Fatal(p)
	}
	list := config.Get().Alarms.List
	if len(list) != 1 || list[0].Wake != radio {
		t.Fatalf("added %+v", list)
	}
	w := httptest.NewRecorder()
	alarmDrawer(w, "tok", list[0], nil)
	body := w.Body.String()
	if !strings.Contains(body, "Radio: WXYZ &amp; Co</option>") || !strings.Contains(body, "wakes with WXYZ &amp; Co") ||
		!strings.Contains(body, `selected>Radio: WXYZ`) {
		t.Errorf("the drawer does not show the music it wakes with:\n%s", body)
	}

	edit := url.Values{"id": {list[0].ID}, "act": {"save"}, "at": {"06:45"}, "alarm_sunrise": {"0"}, "wake": {""}}
	if p := editFromPage(form(edit)); p != "" {
		t.Fatal(p)
	}
	if got := config.Get().Alarms.List[0].Wake; got.Music() {
		t.Errorf("choosing the alarm sound left %+v", got)
	}
}
