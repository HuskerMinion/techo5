package setup

import (
	"context"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/share"
)

// Every setting a card offers to send is on feature/share's list, and every card named is one the page
// has: a name mistyped would offer nothing, or send nothing, without a word.
func TestAlsoOnCardsAreSharedSettings(t *testing.T) {
	for what, ids := range alsoOnCards {
		if sectionNamed(what) == "" {
			t.Errorf("%s is not a form of the page", what)
		}
		for _, id := range ids {
			if !share.Shareable(id) {
				t.Errorf("%s offers %s, which is not on the list", what, id)
			}
		}
	}
}

// fakeAlsoOn stands in for the other devices: peers on the network, and what a copy and a link to them
// answer, keeping what was asked.
func fakeAlsoOn(t *testing.T, peers []string, answer func(d, id string) string) (copied *[][2][]string, linked *map[string][]string) {
	t.Helper()
	oldDevices, oldCopy, oldLink := alsoOnDevices, alsoOnCopy, alsoOnLink
	t.Cleanup(func() { alsoOnDevices, alsoOnCopy, alsoOnLink = oldDevices, oldCopy, oldLink })
	var calls [][2][]string
	links := map[string][]string{}
	alsoOnDevices = func() []string { return peers }
	alsoOnCopy = func(_ context.Context, ids, devices []string) map[string]map[string]string {
		calls = append(calls, [2][]string{ids, devices})
		out := map[string]map[string]string{}
		for _, d := range devices {
			out[d] = map[string]string{}
			for _, id := range ids {
				out[d][id] = answer(d, id)
			}
		}
		return out
	}
	alsoOnLink = func(_ context.Context, id string, devices []string) (map[string]string, error) {
		links[id] = devices
		out := map[string]string{}
		for _, d := range devices {
			out[d] = share.Asked
		}
		return out, nil
	}
	return &calls, &links
}

func TestAlsoOnRowNeedsSharingOn(t *testing.T) {
	f, c := in(t)
	fakeAlsoOn(t, []string{"Kitchen", "Office"}, func(string, string) string { return share.Applied })
	if body := get(f, "/setup?tab=alarms", c).Body.String(); strings.Contains(body, `name="also_on"`) {
		t.Fatal("the row is offered with sharing off")
	}
	_ = config.Set().Share().On(true)
	body := get(f, "/setup?tab=alarms", c).Body.String()
	for _, want := range []string{`name="also_on" value="Kitchen"`, `name="also_on" value="Office"`, `name="also_link" value="yes"`, "Alarm sound"} {
		if !strings.Contains(body, want) {
			t.Errorf("the alarms tab has no %q", want)
		}
	}
	if i := strings.Index(body, "A save here also sends"); i < 0 {
		t.Error("the row does not say what it sends")
	} else if names := body[i : i+strings.Index(body[i:], "</p>")]; strings.Contains(names, "Ring volume") {
		t.Errorf("the row offers the ring volume, which is never shared: %s", names)
	}
	fakeAlsoOn(t, nil, func(string, string) string { return share.Applied })
	if body := get(f, "/setup?tab=alarms", c).Body.String(); strings.Contains(body, `name="also_on"`) {
		t.Error("the row is offered with no other device")
	}
}

func TestAlsoOnSendsTheCardsSettings(t *testing.T) {
	_, c := in(t)
	_ = config.Set().Share().On(true)
	copied, linked := fakeAlsoOn(t, []string{"Kitchen", "Office", "Garage"}, func(d, id string) string {
		switch {
		case d == "Office" && id == "alarm_sound":
			return share.Kept
		case d == "Garage":
			return share.NotTaking
		}
		return share.Applied
	})
	form := url.Values{"what": {"alarm_prefs"}, "also_on": {"Kitchen", "Office", "Garage", "kitchen", ""}, "also_link": {"yes"}}
	r := httptest.NewRequest("POST", "/setup/save", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	alsoOnSave(r, c.Value, "alarm_prefs")

	if len(*copied) != 1 {
		t.Fatalf("copied %d times", len(*copied))
	}
	ids, devices := (*copied)[0][0], (*copied)[0][1]
	if !slices.Equal(ids, []string{"alarm_sound", "alarm_snooze_length"}) {
		t.Errorf("sent %v", ids)
	}
	if !slices.Equal(devices, []string{"Kitchen", "Office", "Garage"}) {
		t.Errorf("sent to %v, a name twice or an empty one", devices)
	}
	if got := (*linked)["alarm_sound"]; !slices.Equal(got, []string{"Kitchen", "Office", "Garage"}) {
		t.Errorf("linked with %v", got)
	}

	said := alsoOnSaid.get(c.Value, "alarm_prefs")
	if said == nil {
		t.Fatal("nothing said on the card")
	}
	all := strings.Join(said.lines, "\n")
	for _, want := range []string{"Kitchen: took it; asked to keep them in step", "Office: kept its own alarm sound", "Garage: sharing is off there"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
	if alsoOnSaid.get(c.Value, "speaker") != nil || alsoOnSaid.get("another", "alarm_prefs") != nil {
		t.Error("said on another card or to another session")
	}

	// Saved here only: what the last one sent is no longer said.
	r = httptest.NewRequest("POST", "/setup/save", strings.NewReader("what=alarm_prefs"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	alsoOnSave(r, c.Value, "alarm_prefs")
	if alsoOnSaid.get(c.Value, "alarm_prefs") != nil {
		t.Error("an earlier save's devices are still said after a save here only")
	}
	if len(*copied) != 1 {
		t.Error("a save with no device ticked sent something")
	}
}

func TestAlsoOnSendsNothingWithSharingOff(t *testing.T) {
	_, c := in(t)
	copied, _ := fakeAlsoOn(t, []string{"Kitchen"}, func(string, string) string { return share.Applied })
	r := httptest.NewRequest("POST", "/setup/save", strings.NewReader("what=alarm_prefs&also_on=Kitchen"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	alsoOnSave(r, c.Value, "alarm_prefs")
	if len(*copied) != 0 {
		t.Error("sent with sharing off here")
	}
}

func TestAlsoOnLine(t *testing.T) {
	for _, tc := range []struct {
		copied map[string]string
		want   string
	}{
		{nil, "Den: could not be reached"},
		{map[string]string{"bass": share.Unreachable}, "Den: could not be reached"},
		{map[string]string{"bass": share.Same, "treble": share.Applied}, "Den: took it"},
		{map[string]string{"bass": share.Applied, "treble": share.NotHere}, "Den: took it; 1 setting it does not have"},
		{map[string]string{"bass": share.Kept, "treble": share.Applied}, "Den: kept its own bass (Keep mine is on there); took the rest"},
	} {
		if got := alsoOnLine("Den", tc.copied, nil); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.copied, got, tc.want)
		}
	}
}
