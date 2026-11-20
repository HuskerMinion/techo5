package setup

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/share"
)

// fakeDevices is fakeAlsoOn with what the devices have, and this device's own values.
func fakeDevices(t *testing.T, here map[string]string, by map[string]map[string]string) (copied *[][2][]string) {
	t.Helper()
	var peers []string
	for d := range by {
		peers = append(peers, d)
	}
	slices.Sort(peers)
	copied, _ = fakeAlsoOn(t, peers, func(d, id string) string {
		if by[d][id] == here[id] {
			return share.Same
		}
		return share.Applied
	})
	oldHere, oldPage := alsoOnHere, devicePage
	t.Cleanup(func() { alsoOnHere, devicePage = oldHere, oldPage })
	alsoOnHere = func([]string) map[string]string { return here }
	devicePage = func(d string) string { return "http://" + strings.ToLower(d) + ".example/setup?tab=system" }
	alsoOnValues = func(context.Context, []string, []string) map[string]map[string]string { return by }
	return copied
}

func TestDevicesRows(t *testing.T) {
	var a, b string
	for _, sec := range share.Sections() {
		for _, st := range sec.Settings {
			switch {
			case a == "":
				a = st.ID
			case b == "":
				b = st.ID
			}
		}
	}
	if b == "" {
		t.Skip("this build has fewer than two shared settings")
	}
	here := map[string]string{a: "1", b: "on"}
	by := map[string]map[string]string{"Den": {a: "1", b: "off"}, "Office": {a: "1"}}
	rows, same := devicesRows(here, by, []string{"Den", "Office"})
	if len(rows) != 1 || rows[0].id != b || rows[0].here != "on" {
		t.Errorf("rows %v", rows)
	}
	if same != 1 {
		t.Errorf("same %d, want 1", same)
	}
	if rows, same := devicesRows(here, by, nil); len(rows) != 0 || same != 0 {
		t.Errorf("no device answered, still %v and %d the same", rows, same)
	}
}

func TestDevicesCard(t *testing.T) {
	f, c := in(t)
	fakeDevices(t, map[string]string{"alarm_snooze_length": "9", "alarm_sound": "Chimes"}, map[string]map[string]string{
		"Kitchen": {"alarm_snooze_length": "12", "alarm_sound": "Chimes"},
		"Office":  {"alarm_snooze_length": "9"},
		"Garage":  nil,
	})
	if body := get(f, "/setup?tab=system", c).Body.String(); strings.Contains(body, `id="sec-devices"`) {
		t.Fatal("the card is drawn with sharing off")
	}
	_ = config.Set().Share().On(true)
	body := get(f, "/setup?tab=system", c).Body.String()
	i := strings.Index(body, `id="sec-devices"`)
	if i < 0 {
		t.Fatal("no Your devices card with sharing on")
	}
	card := body[i:]
	card = card[:strings.Index(card, "</details>")]
	for _, want := range []string{
		`<td class="diff">12</td>`,
		`name="id" value="alarm_snooze_length"`,
		`<strong>Garage</strong> has sharing off. <a href="http://garage.example/setup?tab=system">`,
		`<th>Kitchen</th>`, `<th>Office</th>`,
		`1 setting differs`,
	} {
		if !strings.Contains(card, want) {
			t.Errorf("the card has no %q", want)
		}
	}
	if strings.Contains(card, `value="alarm_sound"`) {
		t.Error("a setting the same everywhere has a row")
	}
	if strings.Contains(card, `<th>Garage</th>`) {
		t.Error("a device with sharing off has a column")
	}
}

func TestDevicesMakeAllLikeThis(t *testing.T) {
	f, c := in(t)
	_ = config.Set().Share().On(true)
	copied := fakeDevices(t, map[string]string{"alarm_snooze_length": "9"}, map[string]map[string]string{
		"Kitchen": {"alarm_snooze_length": "12"},
		"Office":  {"alarm_snooze_length": "9"},
	})
	w := postTo(f, "/setup/devices", c, url.Values{"token": {c.Value}, "id": {"alarm_snooze_length"}})
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "tab=system") || !strings.Contains(loc, "saved") {
		t.Errorf("sent back to %q", loc)
	}
	if len(*copied) != 1 || !slices.Equal((*copied)[0][0], []string{"alarm_snooze_length"}) ||
		!slices.Equal((*copied)[0][1], []string{"Kitchen", "Office"}) {
		t.Fatalf("copied %v", *copied)
	}
	said := alsoOnSaid.get(c.Value, "devices")
	if said == nil {
		t.Fatal("nothing said on the card")
	}
	all := strings.Join(said.lines, "\n")
	for _, want := range []string{"Kitchen: took it", "Office: already had these"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}

	w = postTo(f, "/setup/devices", c, url.Values{"token": {c.Value}, "id": {"wifi_password"}})
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "problem") || len(*copied) != 1 {
		t.Errorf("a setting off the list: sent back to %q, copied %d times", loc, len(*copied))
	}
	w = postTo(f, "/setup/devices", c, url.Values{"id": {"alarm_snooze_length"}})
	if w.Code != 403 || len(*copied) != 1 {
		t.Errorf("a post with no token: %d, copied %d times", w.Code, len(*copied))
	}
}

// A find draws every tab, the System tab's Your devices and the Also on rows too, without asking the
// devices: it shows what was kept, or says to open the tab.
func TestAFindDoesNotAskTheDevices(t *testing.T) {
	f, c := in(t)
	_ = config.Set().Share().On(true)
	fakeDevices(t, map[string]string{"alarm_snooze_length": "9"}, map[string]map[string]string{"Kitchen": {"alarm_snooze_length": "12"}})
	asked := 0
	alsoOnValues = func(context.Context, []string, []string) map[string]map[string]string {
		asked++
		return map[string]map[string]string{"Kitchen": {"alarm_snooze_length": "12"}}
	}
	page := get(f, "/setup?find="+url.QueryEscape("your devices"), c).Body.String()
	if asked != 0 {
		t.Errorf("a find asked the devices %d times", asked)
	}
	if !strings.Contains(page, "open the System tab to see them") {
		t.Error("a find with nothing kept does not say where to look")
	}
	get(f, "/setup?tab=system", c)
	if asked != 1 {
		t.Fatalf("the System tab asked %d times", asked)
	}
	if page := get(f, "/setup?find="+url.QueryEscape("your devices"), c).Body.String(); !strings.Contains(page, `<td class="diff">12</td>`) || asked != 1 {
		t.Errorf("a find after the tab: asked %d times, kept values shown: %v", asked, strings.Contains(page, `class="diff"`))
	}
}

// Find does not find a card by its Also on row: a device's name is no setting of the card.
func TestAFindSkipsTheAlsoOnRow(t *testing.T) {
	if got := cardText([]byte(`<details class="card"><summary>x</summary><label>Alarm sound</label><fieldset class="also"><legend>Also on</legend>Kitchen same as here</fieldset></details>`)); strings.Contains(got, "kitchen") || !strings.Contains(got, "alarm sound") {
		t.Errorf("card text %q", got)
	}
}
