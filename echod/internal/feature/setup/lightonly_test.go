package setup

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// An alarm added or edited on the page can be light only (#164), and the box shows what was saved.
func TestAnAlarmCanBeLightOnlyFromThePage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	add := url.Values{"kind": {"alarm"}, "at": {"06:45"}, "label": {"Morning"}, "alarm_sunrise": {"0"}, "light_only": {"yes"}}
	if p := addFromPage(form(add)); p != "" {
		t.Fatal(p)
	}
	list := config.Get().Alarms.List
	if len(list) != 1 || !list[0].LightOnly {
		t.Fatalf("added %+v", list)
	}
	w := httptest.NewRecorder()
	alarmDrawer(w, "tok", list[0], nil)
	if !strings.Contains(w.Body.String(), `name="light_only" value="yes" style="width:auto" checked`) ||
		!strings.Contains(w.Body.String(), "light only") {
		t.Error("the alarm's drawer does not show it as light only")
	}

	edit := url.Values{"id": {list[0].ID}, "act": {"save"}, "at": {"06:45"}, "label": {"Morning"}, "alarm_sunrise": {"0"}}
	if p := editFromPage(form(edit)); p != "" {
		t.Fatal(p)
	}
	if config.Get().Alarms.List[0].LightOnly {
		t.Error("unticking the box left the alarm light only")
	}
}

// Light only is an alarm's: the add form hides its box while a reminder or a timer is chosen, and a
// reminder saved from the page is never light only, whatever the form sent.
func TestAReminderIsNeverLightOnly(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	w := httptest.NewRecorder()
	addDrawer(w, "tok", nil)
	body := w.Body.String()
	box := strings.Index(body, `name="light_only"`)
	wrap := strings.LastIndex(body[:max(box, 0)], `<div class="alarmonly">`)
	if box < 0 || wrap < 0 || strings.Contains(body[wrap:box], "</div>") {
		t.Error("the add form's Light only box is not inside the part hidden for reminders")
	}
	page := httptest.NewRecorder()
	head(page)
	if !strings.Contains(page.Body.String(), ".alarmonly{display:none}") {
		t.Error("nothing hides the alarm-only part")
	}

	add := url.Values{"kind": {"reminder"}, "at": {"06:45"}, "label": {"Bins"}, "light_only": {"yes"}}
	if p := addFromPage(form(add)); p != "" {
		t.Fatal(p)
	}
	list := config.Get().Alarms.List
	if len(list) != 1 || !list[0].Remind || list[0].LightOnly {
		t.Fatalf("added %+v", list)
	}

	al := list[0]
	al.LightOnly = true
	if err := config.Set().Alarms().Put(al); err != nil {
		t.Fatal(err)
	}
	edit := url.Values{"id": {al.ID}, "act": {"save"}, "at": {"06:45"}, "label": {"Bins"}, "light_only": {"yes"}}
	if p := editFromPage(form(edit)); p != "" {
		t.Fatal(p)
	}
	if config.Get().Alarms.List[0].LightOnly {
		t.Error("a reminder saved from the page stayed light only")
	}
}
