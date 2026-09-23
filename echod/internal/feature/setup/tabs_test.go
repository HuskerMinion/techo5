package setup

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// in is a browser that has been let in, and the token its forms carry.
func in(t *testing.T) (*Feature, *http.Cookie) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	return f, c
}

// post sends a form as the page does, and returns where it was sent back to.
func post(t *testing.T, f *Feature, c *http.Cookie, v url.Values) *url.URL {
	t.Helper()
	v.Set("token", c.Value)
	r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	f.serve(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("saving %v: %d %s", v, w.Code, w.Body.String())
	}
	to, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return to
}

func TestEveryTabDrawsAndMarksItself(t *testing.T) {
	f, c := in(t)
	for _, tb := range tabs {
		body := get(f, "/setup?tab="+tb.id, c).Body.String()
		if !strings.Contains(body, `href="/setup?tab=`+tb.id+`" class="on"`) {
			t.Errorf("tab %s is not marked as the one shown", tb.id)
		}
	}
	if body := get(f, "/setup?tab=nonsense", c).Body.String(); !strings.Contains(body, `tab=alarms" class="on"`) {
		t.Error("a tab that is not one did not fall back to Alarms & Timers")
	}
	if body := get(f, "/setup?tab=general", c).Body.String(); !strings.Contains(body, "Time zone") ||
		!strings.Contains(body, "Download diagnostics") {
		t.Error("General lost the time zone or the diagnostics")
	}
}

// A save comes back to the tab it was made on, or every save would throw the page back to the first.
func TestASaveComesBackToItsTab(t *testing.T) {
	f, c := in(t)
	to := post(t, f, c, url.Values{"what": {"house"}, "tab": {"sound"}, "word": {"costilla"}})
	if to.Query().Get("tab") != "sound" || to.Query().Get("saved") != "1" {
		t.Errorf("sent back to %s", to)
	}
	to = post(t, f, c, url.Values{"what": {"alarm_add"}, "tab": {"alarms"}, "kind": {"alarm"}, "at": {""}})
	if to.Query().Get("tab") != "alarms" || to.Query().Get("problem") == "" {
		t.Errorf("a refused save was sent back to %s", to)
	}
	// What went wrong arrives as words and is shown escaped.
	body := get(f, to.String(), c).Body.String()
	if !strings.Contains(body, `banner bad">that needs a time`) {
		t.Errorf("the problem is not on the page: %s", first(body))
	}
}

func TestAlarmsRemindersAndTimersFromThePage(t *testing.T) {
	f, c := in(t)
	add := func(v url.Values) {
		t.Helper()
		v.Set("what", "alarm_add")
		v.Set("tab", "alarms")
		if p := post(t, f, c, v).Query().Get("problem"); p != "" {
			t.Fatalf("adding %v: %s", v, p)
		}
	}
	add(url.Values{"kind": {"alarm"}, "at": {"06:45"}, "label": {"Wake up"}, "day": {"1", "2", "3", "4", "5"}, "alarm_sunrise": {"10"}})
	add(url.Values{"kind": {"reminder"}, "in": {"20 minutes"}, "label": {"Pasta"}, "ring_on": {"Kitchen, Office"}})
	add(url.Values{"kind": {"timer"}, "in": {"10 minutes"}, "label": {"Eggs"}})

	list := config.Get().Alarms.List
	if len(list) != 2 {
		t.Fatalf("%d alarms saved, want an alarm and a reminder", len(list))
	}
	var wake, pasta config.Alarm
	for _, a := range list {
		if a.Remind {
			pasta = a
		} else {
			wake = a
		}
	}
	if wake.Hour != 6 || wake.Minute != 45 || wake.Days != config.DaysWeekdays || wake.Sunrise != 10 || wake.Label != "Wake up" {
		t.Errorf("the alarm saved as %+v", wake)
	}
	if pasta.Label != "Pasta" || len(pasta.RingOn) != 2 || pasta.RingOn[1] != "Office" {
		t.Errorf("the reminder saved as %+v", pasta)
	}
	var eggs string
	for _, tm := range timer.Get().List(time.Now()) {
		if tm.Name == "Eggs" {
			eggs = tm.ID
		}
	}
	if eggs == "" {
		t.Fatal("the timer was not started")
	}

	body := get(f, "/setup?tab=alarms", c).Body.String()
	for _, want := range []string{"Wake up", "Pasta", "Eggs", "here and Kitchen, Office", "light 10 min"} {
		if !strings.Contains(body, want) {
			t.Errorf("the tab does not show %q", want)
		}
	}

	edit := url.Values{"what": {"alarm_edit"}, "tab": {"alarms"}, "id": {wake.ID}, "act": {"save"},
		"at": {"07:00"}, "label": {"Up"}, "alarm_sunrise": {"-1"}}
	if p := post(t, f, c, edit).Query().Get("problem"); p != "" {
		t.Fatal(p)
	}
	for _, a := range config.Get().Alarms.List {
		if a.ID == wake.ID && (a.Hour != 7 || a.Label != "Up" || a.Days != config.DaysOnce || a.Sunrise != config.SunriseOff) {
			t.Errorf("the edit saved as %+v", a)
		}
	}
	post(t, f, c, url.Values{"what": {"alarm_edit"}, "tab": {"alarms"}, "id": {wake.ID}, "act": {"toggle"}})
	for _, a := range config.Get().Alarms.List {
		if a.ID == wake.ID && a.On {
			t.Error("turning it off left it on")
		}
	}
	post(t, f, c, url.Values{"what": {"alarm_edit"}, "tab": {"alarms"}, "id": {pasta.ID}, "act": {"delete"}})
	post(t, f, c, url.Values{"what": {"timer_cancel"}, "tab": {"alarms"}, "id": {eggs}})
	if n := len(config.Get().Alarms.List); n != 1 {
		t.Errorf("%d alarms after deleting the reminder", n)
	}
	for _, tm := range timer.Get().List(time.Now()) {
		if tm.ID == eggs {
			t.Error("the timer is still running after it was canceled")
		}
	}

	// A reminder has to say something, from the page as from Home Assistant.
	v := url.Values{"what": {"alarm_add"}, "tab": {"alarms"}, "kind": {"reminder"}, "at": {"14:30"}}
	if post(t, f, c, v).Query().Get("problem") == "" {
		t.Error("a reminder with no words was accepted")
	}
}
