//go:build !panel

package setup

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addonhost"
	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// Every capability of the add-on API has its line on the consent screen, word for word: changing one
// is a decision, and this test is where that shows.
func TestEveryCapabilityHasItsConsentLine(t *testing.T) {
	want := map[string]string{
		"commands":  "Adds commands Home Assistant and voice can call: next, start cooking.",
		"events":    "Sends events of its own to Home Assistant.",
		"speak":     "Says words on this device.",
		"sound":     "Plays short sounds on this device.",
		"timers":    "Sets timers that ring on this device (silent in quiet hours).",
		"keepawake": "Keeps the screen on while you use it.",
		"settings":  "Has settings of its own on this page.",
		"entities":  "Adds entities to Home Assistant: sensor.step.",
		"media":     "Plays video and audio from the network on this device.",
	}
	l := plexListing(true, "", false)
	l.Version.API, l.Version.Capabilities = 1, addonapi.Capabilities
	l.Version.Commands, l.Version.Entities = []string{"next", "start_cooking"}, []string{"sensor.step"}
	for _, c := range addonapi.Capabilities {
		if got := capabilityLine(c, l.Version); got != want[c] {
			t.Errorf("%s says %q, not %q", c, got, want[c])
		}
	}
	if len(want) != len(addonapi.Capabilities) {
		t.Errorf("%d lines checked for %d capabilities", len(want), len(addonapi.Capabilities))
	}
	// They are on the card above the box, with the rest of what it asks.
	w := httptest.NewRecorder()
	addonsAvailable(w, "tok", []addons.Listing{l})
	body := strings.Join(strings.Fields(w.Body.String()), " ")
	for _, line := range want {
		i := strings.Index(body, escaped(line))
		if i < 0 || i > strings.Index(body, "Add-ons run on this device") {
			t.Errorf("the line %q is not above the warning:\n%s", line, body)
		}
	}
	// A listing without the API says nothing of it.
	w = httptest.NewRecorder()
	addonsAvailable(w, "tok", []addons.Listing{plexListing(true, "", false)})
	if strings.Contains(w.Body.String(), "Home Assistant and voice") {
		t.Error("a listing without the API shows a capability")
	}
}

func escaped(s string) string { return strings.ReplaceAll(s, "'", "&#39;") }

// An add-on's secret setting is never kept in a refused form, whatever it is called.
func TestAnAddonsSecretIsLeftOutOfTheStash(t *testing.T) {
	var s rejectedForms
	s.keep("tok", "addon-settings", url.Values{addonhost.SecretPrefix + "token": {"s3cret"}, addonhost.FieldPrefix + "address": {"http://x"}, "id": {"meals"}})
	got := s.byID["tok"].fields
	if got.Get(addonhost.SecretPrefix+"token") != "" || got.Get(addonhost.FieldPrefix+"address") != "http://x" {
		t.Errorf("kept %v", got)
	}
}

// A secret is never put back in the page: its field is empty, and says whether one is set.
func TestAnAddonsSettingsFormNeverShowsASecret(t *testing.T) {
	schema := &addonapi.Settings{Test: true, Fields: []addonapi.Setting{
		{Key: "address", Label: "Mealie address", Type: addonapi.TypeString, Max: 200},
		{Key: "token", Label: "API token", Type: addonapi.TypeSecret},
		{Key: "loud", Label: "Read aloud", Type: addonapi.TypeBool},
		{Key: "size", Label: "Servings", Type: addonapi.TypeNumber, Min: 1, Max: 12, Step: 1},
		{Key: "unit", Label: "Units", Type: addonapi.TypeSelect, Options: []string{"metric", "US"}},
	}}
	w := httptest.NewRecorder()
	addonSettingsForm(w, "tok", "meals", schema, map[string]any{"address": "http://mealie.local", "token": "s3cret",
		"loud": true, "size": 4.0, "unit": "US"})
	body := w.Body.String()
	if strings.Contains(body, "s3cret") {
		t.Fatalf("the secret is in the page:\n%s", body)
	}
	for _, want := range []string{`value="addon-settings"`, `name="set_address" value="http://mealie.local" maxlength="200"`,
		`type="password" name="secret_token" value=""`, "set; leave empty to keep it", `name="clear_token"`,
		`name="set_loud" value="yes" checked`, `name="set_size" value="4" min="1" max="12" step="1"`,
		`<option value="US" selected>`, `name="test" value="yes"`} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q:\n%s", want, body)
		}
	}
}

// An update asking for a new capability says it as its consent line does.
func TestAnUpdateSaysANewCapabilityAsItsLine(t *testing.T) {
	got := moreWords([]string{"the speak capability", "the commands capability", "the command back", "port 4000/tcp"})
	want := []string{"Says words on this device", "Adds commands Home Assistant and voice can call", "the command back", "port 4000/tcp"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("said %q", got)
	}
}
