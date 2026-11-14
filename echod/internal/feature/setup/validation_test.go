package setup

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// What the device refuses when a form arrives, the browser says first where it can: a box that has to
// be filled in is marked so, and an address is asked for as one. The device still refuses it, for a
// browser that does not ask or a form sent by something else.
func TestTheBrowserAsksForWhatTheDeviceWouldRefuse(t *testing.T) {
	f, c := in(t)
	var page strings.Builder
	for _, tb := range shownTabs() {
		page.WriteString(get(f, "/setup?tab="+tb.id, c).Body.String())
	}
	body := page.String()

	// The input of each id, where this device has it, carries each of these.
	for id, attrs := range map[string][]string{
		"calurl":    {`type="url"`, `required`},
		"tskey":     {`required`},
		"tscontrol": {`type="url"`},
		"nbkey":     {`required`},
		"nbmgmt":    {`type="url"`},
		"maurl":     {`type="url"`},
		"u0":        {`type="url"`},
		"search":    {`type="url"`},
		"reader":    {`type="url"`},
	} {
		tag := regexp.MustCompile(`<input id="` + id + `"[^>]*>`).FindString(body)
		if tag == "" {
			if id == "calurl" || id == "u0" {
				t.Errorf("the page has no input %q", id)
			}
			continue // not on this device
		}
		for _, a := range attrs {
			if !strings.Contains(tag, a) {
				t.Errorf("%s is not %s", tag, a)
			}
		}
	}
	// A box the page can leave out (TestDirectFieldsAreLeftOutForHomeAssistant and the rest) is never one
	// the browser checks: it would stop the form for a box nobody can see, and not say why.
	for _, hides := range []string{"directonly", "spotifyonly", "saveronly", "worldonly", "fixedonly", "theseonly"} {
		at := strings.Index(body, `<div class="`+hides+`">`)
		if at < 0 {
			continue
		}
		part := body[at:]
		part = part[:strings.Index(part, `</div>`)]
		if strings.Contains(part, " required") || strings.Contains(part, `type="url"`) || strings.Contains(part, "pattern=") {
			t.Errorf("a box under .%s is checked by the browser while it may be left out of the page", hides)
		}
	}

	for _, v := range []url.Values{
		{"what": {"tailscale"}, "key": {" "}},
		{"what": {"netbird"}, "key": {""}},
		{"what": {"calendar"}, "name": {"Family"}, "url": {""}},
		{"what": {"alarm_add"}, "kind": {"alarm"}, "at": {""}},
	} {
		if post(t, f, c, v).Query().Get("problem") == "" {
			t.Errorf("%v was accepted", v)
		}
	}
}

// An alarm that is there has to keep a time, and only Save is held back for it: turning it off or
// deleting it goes through whatever the box holds.
func TestAnAlarmsTimeIsNeededOnlyToSaveIt(t *testing.T) {
	f, c := in(t)
	if p := post(t, f, c, url.Values{"what": {"alarm_add"}, "kind": {"alarm"}, "at": {"06:45"}}).Query().Get("problem"); p != "" {
		t.Fatal(p)
	}
	body := get(f, "/setup?tab=alarms", c).Body.String()
	if !regexp.MustCompile(`<input id="te[^"]*" name="at" type="time" required`).MatchString(body) {
		t.Error("an alarm's own time is not marked as needed")
	}
	for _, act := range []string{"toggle", "delete"} {
		if !regexp.MustCompile(`name="act" value="` + act + `"[^>]* formnovalidate>`).MatchString(body) {
			t.Errorf("%s waits for the time to be filled in", act)
		}
	}
	// The drawer that adds one also sets timers, which have no time of day: its box is not marked.
	if regexp.MustCompile(`<input id="at" name="at" type="time"[^>]*required`).MatchString(body) {
		t.Error("the add drawer's time is needed, which a timer does not have")
	}
}

// A refusal names the box it is about.
func TestARefusalNamesItsBox(t *testing.T) {
	f, c := in(t)
	prefs := func(snooze, vol, sunrise string) string {
		return post(t, f, c, url.Values{"what": {"alarm_prefs"}, "snooze": {snooze}, "ringvol": {vol}, "sunrise": {sunrise}}).Query().Get("problem")
	}
	for said, got := range map[string]string{
		"snooze length": prefs("soon", "5", "0"),
		"ring volume":   prefs("9", "loud", "0"),
		"wake light":    prefs("9", "5", ""),
		"At box":        post(t, f, c, url.Values{"what": {"alarm_add"}, "kind": {"alarm"}, "at": {""}}).Query().Get("problem"),
		"like 06:45":    post(t, f, c, url.Values{"what": {"alarm_add"}, "kind": {"alarm"}, "at": {"quarter to seven"}}).Query().Get("problem"),
		"iCal address":  post(t, f, c, url.Values{"what": {"calendar"}, "url": {"calendar.example.org"}}).Query().Get("problem"),
		"no such form":  post(t, f, c, url.Values{"what": {"nothing-here"}}).Query().Get("problem"),
	} {
		if !strings.Contains(got, said) {
			t.Errorf("the refusal %q does not say %q", got, said)
		}
	}
}
