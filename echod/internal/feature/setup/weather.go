package setup

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timezone"
	"github.com/HuskerMinion/techo5/echod/internal/lib/openmeteo"
)

// placeSection is where the device is, for its own weather, the weather alerts and the rain map
// (config.Home.Place), and how temperatures are shown. Home Assistant's own location and weather win
// where there is a Home Assistant; this is what a device without one goes by.
func placeSection(w http.ResponseWriter, token string) {
	h := config.Get().Home
	fieldsetOpen(w, "place", "Where this device is")
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "place")
	if h.Place.Set() {
		fmt.Fprintf(w, `<p style="margin:0">Now: <strong>%s</strong> (%.3f, %.3f)</p>`,
			html.EscapeString(h.Place.Name), h.Place.Lat, h.Place.Lon)
	} else {
		fmt.Fprint(w, `<p style="margin:0">Not set. With Home Assistant, its home location is used.</p>`)
	}
	units := func(u string) string { return selected(h.Units == u) }
	fmt.Fprintf(w, `<label for="where">ZIP code or town</label>
	 <input id="where" name="where" value="" placeholder="99501, or Anchorage, Alaska" autocomplete="off">
	 <p><label><input type="checkbox" name="zone" value="yes" checked style="width:auto"> Set the time zone
	  from it too</label></p>
	 <p class="note">A new time zone restarts the device, which is back in about a minute, and this page
	  with it.</p>
	 <label for="units">Temperatures in</label>
	 <select id="units" name="units">
	  <option value=""%s>Fahrenheit in the U.S., Celsius elsewhere</option>
	  <option value="F"%s>Fahrenheit</option>
	  <option value="C"%s>Celsius</option>
	 </select>
	 <p class="note">Used for the weather on the clock, the forecast page, weather alerts and the rain map
	  when there is no Home Assistant to say where home is. The weather comes from Open-Meteo, free and
	  without an account. Leave the box empty to change only the units.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		units(""), units(config.UnitsF), units(config.UnitsC))
}

// savePlace looks the place up and keeps the first match. A bare five-digit number is a U.S. ZIP
// code: without saying so, the lookup finds the same number in other countries first.
func savePlace(r *http.Request) string {
	units := strings.TrimSpace(r.PostFormValue("units"))
	if units != "" && units != config.UnitsF && units != config.UnitsC {
		return "that is not a unit this device knows"
	}
	if err := config.Set().Home().Units(units); err != nil {
		return "could not save the units: " + err.Error()
	}
	where := strings.TrimSpace(r.PostFormValue("where"))
	if where == "" {
		home.Get().Poke()
		return ""
	}
	country := ""
	if len(where) == 5 && strings.IndexFunc(where, func(r rune) bool { return !unicode.IsDigit(r) }) < 0 {
		country = "US"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	found, err := openmeteo.Find(ctx, where, country)
	if err != nil {
		return "could not look that up: " + err.Error()
	}
	if len(found) == 0 {
		return "no place called " + where + " was found; try a town and state"
	}
	p := found[0]
	if err := config.Set().Home().Place(config.Place{Name: p.Name, Lat: p.Lat, Lon: p.Lon, Country: p.Country}); err != nil {
		return "could not save the place: " + err.Error()
	}
	slog.Info("setup page: the device's place was set", "place", p.Name, "country", p.Country)
	// The same zone chosen again changes nothing, and would cost a restart.
	same := timezone.Get().SetHere() && timezone.Get().Current() == p.Zone
	if r.PostFormValue("zone") == "yes" && p.Zone != "" && !same {
		if err := timezone.Get().Choose(p.Zone); err != nil {
			return "the place was saved, but not its time zone (" + p.Zone + "): " + err.Error()
		}
	}
	home.Get().Poke()
	return ""
}
