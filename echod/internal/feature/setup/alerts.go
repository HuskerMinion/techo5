package setup

import (
	"fmt"
	"html"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// alertsSection is, for MeteoAlarm's weather alerts (home/alerts_source.go), which regions are home's,
// how far around it a region still counts and how often the feed is read. Where the alerts come from
// is not a setting: Home Assistant's country picks it. The same settings are Home Assistant's "Alert
// regions", "Alerts nearby" and "MeteoAlarm refresh".
func alertsSection(w http.ResponseWriter, token string) {
	h := config.Get().Home
	sectionOpen(w, "alerts", "Weather alerts", alertsSummary(h)...)
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "alerts")
	fmt.Fprint(w, `<p class="note" style="margin-top:0">Alerts come from the U.S. National Weather Service
	 in the U.S. and from MeteoAlarm, the official warnings of Europe's national weather services, in a
	 country it covers, by Home Assistant's country. The Weather alerts switch on the device's settings
	 turns them off.</p>`)
	fmt.Fprintf(w, `<label for="alertregions">MeteoAlarm regions</label>
	 <input id="alertregions" name="regions" value="%s" placeholder="Found from where home is" autocomplete="off">
	 <p class="note">Leave empty to use the regions home lies in. Or list them by their EMMA_ID, separated
	  by commas (ES133, ES842): on meteoalarm.org, a region's code is in its link. Countries that send
	  outlines instead of regions (Switzerland, Sweden, the U.K....) are matched by home's location
	  either way.</p>`, html.EscapeString(strings.Join(h.AlertRegions, ", ")))
	if now := home.AlertRegionsNow(); len(now) > 0 {
		fmt.Fprintf(w, `<p class="note">In use now: %s</p>`, html.EscapeString(strings.Join(now, ", ")))
	}
	fmt.Fprint(w, `<label for="alertsnear">Also regions within</label><select id="alertsnear" name="near">`)
	for _, km := range home.AlertsNearChoices {
		label := "None: only home's own"
		if km > 0 {
			label = strconv.Itoa(km) + " km of home"
		}
		fmt.Fprintf(w, `<option value="%d"%s>%s</option>`, km, selected(km == home.AlertsNearKm()), label)
	}
	fmt.Fprint(w, `</select><label for="alertsevery">Read MeteoAlarm every</label><select id="alertsevery" name="every">`)
	every := int(home.MeteoAlarmEvery() / time.Minute)
	for _, m := range home.AlertsEveryChoices {
		fmt.Fprintf(w, `<option value="%d"%s>%d minutes</option>`, m, selected(m == every), m)
	}
	fmt.Fprint(w, `</select>
	 <p class="note">A country's MeteoAlarm feed can be a few megabytes, read again in full each time.
	  It is read only while the screen is on. MeteoAlarm's
	  <a href="https://meteoalarm.org/en/live/page/terms-and-conditions" target="_blank" rel="noopener">terms</a>
	  ask that warnings be shown no more than ten minutes after meteoalarm.org has them: 10 minutes
	  keeps to that, a longer wait does not.</p>
	 <p class="note">Warnings from EUMETNET – MeteoAlarm and the national weather services that issue
	  them, <a href="https://www.meteoalarm.org" target="_blank" rel="noopener">www.meteoalarm.org</a>.
	  Time delays between this device and the www.meteoalarm.org website are possible. For the most
	  up-to-date awareness information as published by the participating National Meteorological and
	  Hydrological Services, please refer to www.meteoalarm.org.</p>
	 <p><button type="submit">Save</button></p></form>`)
	sectionClose(w)
}

// alertsSummary is the line on the shut card: off, the regions when set by hand, or that they are
// found from where home is.
func alertsSummary(h config.Home) []string {
	switch {
	case h.AlertsOff:
		return []string{"Off"}
	case len(h.AlertRegions) > 0:
		return []string{strings.Join(h.AlertRegions, ", ")}
	}
	return []string{"Regions from where home is"}
}

// saveAlerts keeps the alert settings; nothing is kept when one of them is not one this device knows.
func saveAlerts(r *http.Request) string {
	near, err := strconv.Atoi(r.PostFormValue("near"))
	if err != nil || !slices.Contains(home.AlertsNearChoices, near) {
		return "that is not a distance this device knows"
	}
	every, err := strconv.Atoi(r.PostFormValue("every"))
	if err != nil || !slices.Contains(home.AlertsEveryChoices, every) {
		return "that is not a refresh this device knows"
	}
	regions := r.PostFormValue("regions")
	if _, bad := home.ParseAlertRegions(regions); bad != "" {
		return bad + " is not a MeteoAlarm region code (two letters and three digits, like ES842)"
	}
	f := home.Get()
	f.SetAlertsEvery(every)
	f.SetAlertsNearKm(near)
	f.SetAlertRegions(regions)
	return ""
}
