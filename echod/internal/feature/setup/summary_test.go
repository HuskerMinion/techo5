package setup

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// cardSummary is a card's title and the line beside it.
var cardSummary = regexp.MustCompile(`<details class="card" id="sec-([^"]+)"[^>]*><summary><span class="ttl">[^<]*</span><span class="sum">([^<]*)</span></summary>`)

// summaries are the lines on every card of every tab this build has, by section.
func summaries(t *testing.T) map[string]string {
	t.Helper()
	f := build()
	f.Open()
	c := ask(t, f)
	f.press()
	out := map[string]string{}
	for _, tb := range shownTabs() {
		page := get(f, "/setup?tab="+tb.id, c).Body.String()
		_, all := openCards(page)
		found := cardSummary.FindAllStringSubmatch(page, -1)
		if len(found) != len(all) {
			t.Errorf("%s: %d cards, %d with a summary line", tb.id, len(all), len(found))
		}
		for _, m := range found {
			out[m[1]] = html.UnescapeString(m[2])
		}
	}
	return out
}

// noSummary are the cards with nothing worth saying while shut: Status's and the alarms' lists, which
// are always open and show what they hold, and the cards that are only a note, a download or a button.
var noSummary = map[string]bool{
	"now": true, "switchedon": true, "diagnostics": true, "alarm_edit": true, "haalarms": true,
	"thispage": true, "settingsfile": true, "giftreset": true, "calendar-look": true,
}

// Every card says where it stands while shut, from what it is set to, and none says a secret, every
// secret the page keeps being set.
func TestEveryCardSaysWhereItStands(t *testing.T) {
	saveFakeSecrets(t)
	const houseWord = "walnut-fake-house-word-long"
	calendarURL := "https://calendar.example/ical/walnut-fake-calendar-secret/basic.ics"
	for what, err := range map[string]error{
		"house word": config.Set().Home().HouseWord(houseWord),
		"brain":      config.Set().Brain().Set(config.Brain{Search: "http://192.168.1.20:8888"}),
		"calendar":   config.Set().Calendar().Links([]config.CalendarLink{{ID: "walnut", Name: "Family", URL: calendarURL}}),
		"baby key":   config.Set().Security().BabyKey("walnut-fake-baby-key"),
		"baby":       config.Set().Security().Baby(true),
		"screen key": config.Set().Security().ScreenKey("walnut-fake-screen-key"),
		"screen":     config.Set().Security().Screen(true),
	} {
		if err != nil {
			t.Fatalf("saving the %s: %v", what, err)
		}
	}
	got := summaries(t)

	for name, want := range map[string]string{
		"house":    "On · house word set",
		"brain":    "Home Assistant · web search on",
		"music":    "Set up",
		"remote":   "Off",
		"name":     deviceName(),
		"calendar": "Family",
	} {
		if got[name] != want {
			t.Errorf("%s says %q, not %q", name, got[name], want)
		}
	}
	// The sections only some devices draw, where this one does.
	for name, want := range map[string]string{
		"dashboard": "Set up",
		"immich":    "Set up · not in the slideshow",
		"reolink":   "0 cameras",
		"go2rtc":    "0 streams",
		"lock":      "Off",
	} {
		if s, ok := got[name]; ok && s != want {
			t.Errorf("%s says %q, not %q", name, s, want)
		}
	}
	if s, ok := got["baby"]; ok && !strings.HasPrefix(s, "On · 0 listening") {
		t.Errorf("baby says %q", s)
	}
	if s, ok := got["screenpage"]; ok && s != "On" {
		t.Errorf("screenpage says %q", s)
	}
	for _, name := range []string{"speaker", "house", "timezone", "name", "updates", "remote"} {
		if _, ok := got[name]; !ok {
			t.Errorf("no %s card on any tab", name)
		}
	}

	for name, s := range got {
		if noSummary[name] != (s == "") {
			t.Errorf("%s's line is %q", name, s)
		}
		if len([]rune(s)) > 80 {
			t.Errorf("%s's line is long: %q", name, s)
		}
		if strings.HasSuffix(s, ".") {
			t.Errorf("%s's line ends in a period: %q", name, s)
		}
	}

	secrets := []string{houseWord, calendarURL, "walnut-fake-calendar-secret", "walnut-fake-baby-key", "walnut-fake-screen-key"}
	for _, v := range fakeSecrets {
		secrets = append(secrets, v)
	}
	for name, s := range got {
		for _, secret := range secrets {
			if strings.Contains(s, secret) {
				t.Errorf("%s's line says a secret: %q", name, s)
			}
		}
	}
}

// The lines themselves, from what each section reads.
func TestSummaryLines(t *testing.T) {
	join := func(s []string) string { return strings.Join(s, " · ") }
	for _, c := range []struct{ got, want string }{
		{join(listeningSummary(0, 0)), "Only after a question"},
		{join(listeningSummary(8, 0)), "On · 8 s"},
		{join(listeningSummary(8, 1)), "On · 8 s · 1 time in a row"},
		{join(speakerSummary(10, "Off", false)), fmt.Sprintf("Volume 10 of %d", media.VolumeSteps)},
		{join(speakerSummary(6, "10 PM – 7 AM", true)), fmt.Sprintf("Volume 6 of %d · quiet hours 10 PM – 7 AM · do not disturb", media.VolumeSteps)},
		{join(wifiSummary(true, "HomeNet", 2)), "Connected to HomeNet · 2 remembered"},
		{join(wifiSummary(false, "", 0)), "Not connected"},
		{join(updatesSummary("v1.6.44", "", false, true)), "v1.6.44 · automatic at night"},
		{join(updatesSummary("v1.6.44", "v1.6.45", false, false)), "v1.6.44 · v1.6.45 ready"},
		{join(updatesSummary("v1.6.44", "v1.6.45", true, true)), "v1.6.44 · installing v1.6.45 · automatic at night"},
		{join(babySummary(false, 0, "")), "Off"},
		{join(babySummary(true, 1, "Dimmest")), "On · 1 listening · light Dimmest"},
		{join(houseSummary("")), "Off"},
		{join(stationsSummary(0, "", false)), "None added"},
		{join(stationsSummary(3, "Jazz FM", true)), "3 stations · playing Jazz FM"},
		{join(streamingSummary(config.Streaming{}, true)), "Off"},
		{join(streamingSummary(config.Streaming{AirPlay: true, Spotify: true, DLNA: true}, true)), "AirPlay, Spotify Connect, DLNA"},
		{join(streamingSummary(config.Streaming{AirPlay: true, DLNA: true}, false)), "DLNA"},
		{join(camStreamSummary(config.CamStream{On: true})), "On · waiting for a password"},
		{join(camStreamSummary(config.CamStream{On: true, Pass: "walnut-fake-stream-pass"})), "On"},
		{join(remoteSummary(true, false)), "On · no key yet"},
		{join(wakeClipsSummary(false, config.WakeSend{})), "Off"},
		{join(wakeClipsSummary(true, config.WakeSend{Target: config.SendWakesmith, URL: "http://192.168.1.20:8765", Token: "walnut-fake-clip-token"})), "Keeping clips · sent to wakesmith"},
		{join(timeServerSummary("")), "The internet's only"},
		{join(timezoneSummary("America/Chicago", false)), "Follow Home Assistant · America/Chicago"},
		{join(timezoneSummary("Europe/London", true)), "Europe/London"},

		{join(go2rtcSummary(config.Go2RTC{Base: "http://viewer:walnut-fake-rtc-pass@192.168.1.20:1984"})), "0 streams"},
		{join(reolinkSummary(config.Reolink{Base: "http://192.168.1.30", Cameras: []config.Camera{{Name: "Door"}}})), "1 camera · no password"},
		{join(adoptSummary(time.Time{}, true)), "Connected"},
		{join(haAccessSummary(true, false, true, "http://homeassistant.local:8123")), "Signed in · homeassistant.local:8123"},
		{join(haAccessSummary(false, false, false, "")), "No access yet"},
		{join(shareSummary(config.Share{On: true, Links: map[string][]string{"volume": {"Kitchen"}}}, false)), "On · 1 setting linked · no house word"},
		{join(photosSummary(0)), "None kept"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
