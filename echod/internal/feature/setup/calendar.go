package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// calendarLinksSection is the calendars read from their iCal address (config.Calendar.Links). The
// addresses are secrets, so what is kept is listed by name only.
func calendarLinksSection(w http.ResponseWriter, token string) {
	links := config.Get().Calendar.Links
	fmt.Fprint(w, `<fieldset><legend>Calendars from a link</legend>`)
	if len(links) == 0 {
		fmt.Fprint(w, `<p style="margin:0">None yet.</p>`)
	}
	for _, l := range links {
		fmt.Fprintf(w, `<form method="post" action="/setup/save" style="display:flex;gap:.5rem;align-items:center;margin:.3rem 0">`)
		hidden(w, token, "calendar-remove", "weather")
		fmt.Fprintf(w, `<input type="hidden" name="id" value="%s"><span style="flex:1">%s</span>
		 <button type="submit">Remove</button></form>`, html.EscapeString(l.ID), html.EscapeString(l.Name))
	}
	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "calendar", "weather")
	fmt.Fprint(w, `<label for="calname">Name</label>
	 <input id="calname" name="name" maxlength="40" placeholder="Family" autocomplete="off">
	 <label for="calurl">The calendar's iCal address</label>
	 <input id="calurl" name="url" placeholder="https://calendar.google.com/calendar/ical/.../basic.ics" autocomplete="off">
	 <p class="note">Google Calendar: Settings, the calendar, <strong>Secret address in iCal format</strong>.
	  iCloud: share the calendar as a <strong>Public Calendar</strong> and copy the link. Outlook: Settings,
	  Calendar, Shared calendars, <strong>Publish a calendar</strong>, the ICS link. Anyone with the address
	  can read the calendar, so this page never shows it again. The device reads it every 15 minutes.</p>
	 <p><button type="submit">Add</button></p></form></fieldset>`)
}

func saveCalendarLink(r *http.Request) string {
	name := strings.TrimSpace(r.PostFormValue("name"))
	addr := strings.TrimSpace(r.PostFormValue("url"))
	if name == "" {
		name = "Calendar"
	}
	if strings.ContainsAny(name+addr, "\r\n") {
		return "the name and the address are one line each"
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "webcal") {
		return "that is not a calendar address: it starts with https:// or webcal://"
	}
	n, err := home.CheckCalendarLink(addr)
	if err != nil {
		return "could not read that calendar: " + err.Error()
	}
	if err := home.Get().AddCalendarLink(name, addr); err != nil {
		return "could not keep it: " + err.Error()
	}
	slog.Info("setup page: a calendar link was added", "name", name, "host", u.Host, "events_this_month", n)
	return ""
}

func removeCalendarLink(r *http.Request) string {
	if err := home.Get().RemoveCalendarLink(strings.TrimSpace(r.PostFormValue("id"))); err != nil {
		return "could not remove it: " + err.Error()
	}
	return ""
}

// calendarLookSection is how the calendar page looks: the screen's switches, each a choice here. A
// switch whose default goes by the screen's size shows that default until somebody changes it, and
// saving the form keeps only what was changed, so the default is not kept as if it had been chosen.
func calendarLookSection(w http.ResponseWriter, token string) {
	s := screen.Load()
	if s == nil || len(s.Calendar) == 0 {
		return
	}
	fmt.Fprint(w, `<fieldset><legend>The calendar's look</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "calendar-look", "weather")
	for _, c := range s.Calendar {
		fmt.Fprintf(w, `<label for="look-%s">%s</label><select id="look-%s" name="look-%s">`,
			c.Key, html.EscapeString(c.Label), c.Key, c.Key)
		now := c.Now()
		for i, o := range c.Options {
			fmt.Fprintf(w, `<option value="%d"%s>%s</option>`, i, selected(i == now), html.EscapeString(o))
		}
		fmt.Fprintf(w, `</select><p class="note">%s</p>`, html.EscapeString(c.Note))
	}
	fmt.Fprint(w, `<p class="note">Also on the screen, under Settings, Display, Calendar, and in Home Assistant.
	 Which calendars show is chosen on the calendar page itself, with the chips over the month.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`)
}

func saveCalendarLook(r *http.Request) string {
	s := screen.Load()
	if s == nil || len(s.Calendar) == 0 {
		return "this device has no calendar page"
	}
	pick := make([]int, len(s.Calendar))
	for i, c := range s.Calendar {
		v := r.PostFormValue("look-" + c.Key)
		if v == "" {
			pick[i] = -1 // not on the form sent: left as it is
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n >= len(c.Options) {
			return "that is not a choice " + strings.ToLower(c.Label) + " has"
		}
		pick[i] = n
	}
	changed := 0
	for i, c := range s.Calendar {
		if pick[i] >= 0 && pick[i] != c.Now() {
			c.Choose(pick[i])
			changed++
		}
	}
	slog.Info("setup page: calendar look set", "changed", changed)
	return ""
}
