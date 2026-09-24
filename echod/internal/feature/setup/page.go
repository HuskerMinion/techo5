package setup

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/diag"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timezone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The page itself: one document served from the binary, no framework and nothing fetched from the
// internet, so it works on a device with no route out. Forms post and the page reloads; the only
// script polls while a browser waits to be let in, and there is a button to do the same by hand.

// maxBody is the most a request may carry. Everything here is a few short fields.
const maxBody = 16 << 10

// cookieName is the session a press hands out.
const cookieName = "techo5_setup"

// Run closes the page when it has been left alone, and keeps Home Assistant's switch honest.
func (f *Feature) Run(ctx context.Context) error {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		if !f.On() {
			f.mu.Lock()
			stale := !f.openedAt.IsZero()
			f.mu.Unlock()
			if stale {
				f.Close() // it ran out on its own; say so and forget the sessions
			}
		}
	}
}

func (f *Feature) serve(w http.ResponseWriter, r *http.Request) {
	// Only a browser that has been let in keeps the page open. Anything else on the network can ask
	// for this path as often as it likes, and the idle time is there to close the page when the
	// person who opened it has walked away — a scanner or a stale tab must not hold it open for them.
	if _, in := f.session(r); in {
		f.used()
	}
	switch strings.TrimSuffix(r.URL.Path, "/") {
	case "/setup":
		f.index(w, r)
	case "/setup/wait":
		f.wait(w, r)
	case "/setup/state":
		f.state(w, r)
	case "/setup/save":
		f.save(w, r)
	case "/setup/diagnostics.txt":
		f.diagnostics(w, r)
	default:
		http.NotFound(w, r)
	}
}

// diagnostics hands over everything worth having when something is wrong, with the addresses, names,
// keys and serial numbers already replaced — so that somebody can send it to an issue without
// reading it line by line first. Behind the press, like everything else here.
func (f *Feature) diagnostics(w http.ResponseWriter, r *http.Request) {
	if _, in := f.session(r); !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	slog.Info("setup page: diagnostics collected")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="techo5-diagnostics.txt"`)
	fmt.Fprint(w, diag.Bundle())
}

// letInRequest is what the rest of the web port asks about a request: whether the browser making it
// has been let in by a press on the device. Handed to web.Guard as this feature is built, so that a
// page in another feature can put its own device-changing options behind the same press.
func (f *Feature) letInRequest(r *http.Request) bool {
	_, in := f.session(r)
	return in
}

// session is the cookie this request carries, and whether it has been let in.
func (f *Feature) session(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	return c.Value, f.letIn(c.Value)
}

func (f *Feature) index(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if !in {
		f.lockedPage(w)
		return
	}
	q := r.URL.Query()
	f.settingsPage(w, token, tabOf(q.Get("tab")), q.Get("saved"), q.Get("renamed"), q.Get("problem"), q.Get("scan") != "")
}

// wait starts this browser waiting for a press and gives it the cookie the press will let in.
func (f *Feature) wait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "post to ask", http.StatusMethodNotAllowed)
		return
	}
	token, ok := f.await()
	if !ok {
		if f.ShutOut() {
			http.Error(w, "too many tries without a press on the device; try again in a few minutes",
				http.StatusTooManyRequests)
			return
		}
		http.Error(w, "another browser is already waiting for a press; try again in a minute", http.StatusConflict)
		return
	}
	// The cookie covers the whole port, not just this page: the screenshot page asks the same
	// question about its device-changing options, and a cookie scoped to /setup would never be sent
	// there. Nothing else is served on this port, it cannot be read by script, and Strict keeps it
	// off requests another site started.
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(life / time.Second),
	})
	f.Changed.Emit(struct{}{}) // the device says a browser is asking
	slog.Info("setup page: a browser is asking to be let in")
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

// state is what the waiting page polls: whether the press has happened yet.
func (f *Feature) state(w http.ResponseWriter, r *http.Request) {
	_, in := f.session(r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `{"in":%t,"waiting":%t}`+"\n", in, f.Waiting())
}

// save takes the forms. Every write names the setting it changes; nothing here touches keys, runs a
// command or writes anything the page does not list.
func (f *Feature) save(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post to save", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "that form was too big or malformed", http.StatusBadRequest)
		return
	}
	// The form carries the session it came from, so a page on another site cannot post here with a
	// browser's cookie.
	if r.PostFormValue("token") != token {
		slog.Warn("setup page: a save arrived without the page's own token")
		http.Error(w, "that form did not come from this page", http.StatusForbidden)
		return
	}

	var problem string
	var renamed bool
	switch what := r.PostFormValue("what"); what {
	case "wifi":
		ssid := strings.TrimSpace(r.PostFormValue("ssid"))
		if other := strings.TrimSpace(r.PostFormValue("other")); other != "" {
			ssid = other
		}
		problem = joinWifi(r.Context(), ssid, r.PostFormValue("passphrase"))
	case "forget":
		ssid := strings.TrimSpace(r.PostFormValue("ssid"))
		if err := wifi.Forget(r.Context(), ssid); err != nil {
			problem = "could not forget " + ssid + ": " + err.Error()
		} else {
			slog.Info("setup page: a network was forgotten", "ssid", ssid)
		}
	case "name":
		problem = rename(r.PostFormValue("name"), r.PostFormValue("understood") == "yes")
		if problem == "" {
			renamed = true
		}
	case "stations":
		// Play and Stop are buttons in the same form as Save, so that a row can be tried with what is
		// typed in it rather than only with what was last kept. Neither of them saves anything.
		switch {
		case r.PostFormValue("stop") != "":
			media.Get().Stop()
		case r.PostFormValue("play") != "":
			problem = playRow(r)
		default:
			problem = saveStations(r)
		}
	case "house":
		problem = saveHouse(r.PostFormValue("word"))
	case "dashboard":
		problem = saveDashboard(r)
	case "timezone":
		zone := strings.TrimSpace(r.PostFormValue("zone"))
		switch {
		case zone == "":
			if err := timezone.Get().Follow(); err != nil {
				problem = err.Error()
			}
		default:
			if err := timezone.Get().Choose(zone); err != nil {
				problem = "that is not a time zone this device knows: " + err.Error()
			}
		}
	default:
		if !alarmSave(r, what, &problem) {
			problem = "nothing to save"
		}
	}

	tab := r.PostFormValue("tab")
	to := back(tab, "saved", "1")
	if renamed {
		to = back(tab, "renamed", "1")
	}
	if problem != "" {
		slog.Warn("setup page: a change was refused", "problem", problem)
		to = back(tab, "problem", problem)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (f *Feature) lockedPage(w http.ResponseWriter) {
	waiting := f.Waiting()
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1><p class="sub">Setup</p>`, html.EscapeString(deviceName()))
	defer fmt.Fprint(w, `</div>`)
	if !waiting {
		fmt.Fprint(w, `<form method="post" action="/setup/wait"><p>To change anything here, press the
		 action button on the device. That is what proves you are standing in front of it.</p>
		 <p><button type="submit">Ask to be let in</button></p></form>`)
	} else {
		fmt.Fprint(w, `<p><strong>Press the action button on the device now.</strong></p>
		 <p class="note">Waiting for the press. The device is showing that a browser is asking.</p>
		 <p><a href="/setup">Check again</a></p>
		 <script>setInterval(async()=>{try{const r=await fetch('/setup/state');const s=await r.json();
		 if(s.in)location.href='/setup';}catch(e){}},2000)</script>`)
	}
	fmt.Fprint(w, `<p class="note">This page is on your own network, without encryption, and closes
	 itself when it is left alone.</p>`)
}

func (f *Feature) settingsPage(w http.ResponseWriter, token, tab, saved, renamed, problem string, scan bool) {
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1><p class="sub">Setup</p><div class="layout">`, html.EscapeString(deviceName()))
	nav(w, tab)
	fmt.Fprint(w, `<section>`)
	for _, t := range tabs {
		if t.id == tab {
			fmt.Fprintf(w, `<h2>%s</h2>`, html.EscapeString(t.title))
		}
	}
	if saved != "" {
		fmt.Fprint(w, `<div class="banner ok">Saved.</div>`)
	}
	if renamed != "" {
		fmt.Fprint(w, `<div class="banner ok">Renamed. The device is restarting and will be back in a minute or
		 so. Home Assistant keeps it as the same device, under its old entity ids.</div>`)
	}
	if problem != "" {
		fmt.Fprintf(w, `<div class="banner bad">%s</div>`, html.EscapeString(problem))
	}

	switch tab {
	case "sound":
		houseSection(w, token)
		stationsSection(w, token)
	case "alarms":
		alarmsSection(w, token)
	case "connections":
		if wifi.Available() {
			f.wifiSection(w, token, scan)
		} else {
			fmt.Fprint(w, `<fieldset><legend>Wi-Fi</legend><p class="note" style="margin:0">This device's network
			 is not one this page can change.</p></fieldset>`)
		}
		dashboardSection(w, token)
	case "privacy":
		privacySection(w)
	case "general":
		timezoneSection(w, token)
		nameSection(w, token)
		diagnosticsSection(w)
	}
	fmt.Fprint(w, `</section></div></div>`)
}

// timezoneSection is which zone the clock shows.
func timezoneSection(w http.ResponseWriter, token string) {
	cur := timezone.Get().Current()
	fmt.Fprint(w, `<form method="post" action="/setup/save"><fieldset><legend>Time zone</legend>`)
	hidden(w, token, "timezone", "general")
	fmt.Fprint(w, `<label for="zone">Zone</label><select id="zone" name="zone">`)
	fmt.Fprintf(w, `<option value=""%s>Follow Home Assistant</option>`, selected(!timezone.Get().SetHere()))
	for _, region := range timezone.Regions() {
		fmt.Fprintf(w, `<optgroup label="%s">`, html.EscapeString(region))
		for _, z := range timezone.Zones(region) {
			full := region + "/" + z
			fmt.Fprintf(w, `<option value="%s"%s>%s</option>`,
				html.EscapeString(full), selected(full == cur && timezone.Get().SetHere()), html.EscapeString(full))
		}
		fmt.Fprint(w, `</optgroup>`)
	}
	fmt.Fprint(w, `</select><p class="note">The device's clock keeps time on its own; this is only
	 which zone it shows.</p><p><button type="submit">Save</button></p></fieldset></form>`)
}

// privacySection says what the device has open, and what this page will never do. It shows and does
// not change: a web page that could open SSH would be a bigger hole than the convenience is worth, so
// these stay on the device's own screen and in Home Assistant.
func privacySection(w http.ResponseWriter) {
	s := config.Get().Security
	onOff := func(on bool) string {
		if on {
			return "<strong>on</strong>"
		}
		return "off"
	}
	fmt.Fprintf(w, `<fieldset><legend>What is switched on</legend>
	 <p style="margin:0">SSH: %s · Camera on the network: %s · Screen on the network: %s</p>
	 <p class="note">Shown here, not changed here. Change them on the device's own screen, or in Home
	  Assistant.</p></fieldset>`, onOff(s.SSH), onOff(s.Camera), onOff(s.Screen))
	fmt.Fprint(w, `<fieldset><legend>This page</legend>
	 <p class="note" style="margin:0">It opens only after a press on the device, and closes itself when it
	  is left alone. It is on your own network, without encryption. It never touches SSH keys, the Home
	  Assistant key, or the software the device runs.</p></fieldset>`)
}

// wifiSection is the networks: what the device is on, what it remembers, and how to add another.
// Adding one does not drop the network it is on, so a device can be given the network it is going to
// while it is still on the one it is at.
func (f *Feature) wifiSection(w http.ResponseWriter, token string, scan bool) {
	if !wifi.Available() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	st := wifi.Current(ctx)

	fmt.Fprint(w, `<fieldset><legend>Wi-Fi</legend>`)
	switch {
	case st.Connected:
		fmt.Fprintf(w, `<p>On <strong>%s</strong>, at %s.</p>`, html.EscapeString(st.SSID), html.EscapeString(st.Address))
	default:
		fmt.Fprintf(w, `<p class="bad">Not on a network (%s).</p>`, html.EscapeString(st.State))
	}

	if saved := wifi.Saved(); len(saved) > 0 {
		fmt.Fprint(w, `<p class="note">Remembered, in the order it tries them:</p><ul>`)
		for _, ssid := range saved {
			fmt.Fprintf(w, `<li>%s <form method="post" action="/setup/save" style="display:inline">`, html.EscapeString(ssid))
			hidden(w, token, "forget", "connections")
			fmt.Fprintf(w, `<input type="hidden" name="ssid" value="%s">
			 <button type="submit" class="quiet">Forget</button></form></li>`, html.EscapeString(ssid))
		}
		fmt.Fprint(w, `</ul>`)
	}

	fmt.Fprint(w, `<form method="post" action="/setup/save">`)
	hidden(w, token, "wifi", "connections")
	fmt.Fprint(w, `<label for="ssid">Network</label><select id="ssid" name="ssid">`)
	if scan {
		nets, err := wifi.Scan(ctx)
		if err != nil {
			fmt.Fprint(w, `<option value="">(the scan failed)</option>`)
		}
		for _, n := range nets {
			fmt.Fprintf(w, `<option value="%s">%s%s</option>`,
				html.EscapeString(n.SSID), html.EscapeString(n.SSID), lock(n.Secured))
		}
	} else {
		fmt.Fprint(w, `<option value="">(not scanned yet)</option>`)
	}
	fmt.Fprint(w, `</select>`)
	if !scan {
		fmt.Fprint(w, `<p><a href="/setup?tab=connections&amp;scan=1">Scan for networks</a> — it takes a few seconds.</p>`)
	}
	fmt.Fprint(w, `<label for="other">…or a name it did not find</label>
	 <input id="other" name="other" autocomplete="off" placeholder="Network name">
	 <label for="passphrase">Passphrase</label>
	 <input id="passphrase" name="passphrase" type="password" autocomplete="new-password">
	 <p class="note">The networks it already remembers are kept. If the one you add is somewhere else,
	  nothing changes here until the device is taken there. If it is a network in range, the device
	  moves to it — and this page goes with it, so you will have to find it again at its new address.</p>
	 <p><button type="submit">Add network</button></p></form></fieldset>`)
}

// diagnosticsSection offers the bundle. It is first because somebody who came here to ask for help
// should not have to read past the radio stations to find it.
func diagnosticsSection(w http.ResponseWriter) {
	fmt.Fprint(w, `<fieldset><legend>Something wrong?</legend>
	 <p><a href="/setup/diagnostics.txt">Download diagnostics</a> — what this device knows about
	  itself, the last of its log, and what it is set to.</p>
	 <p class="note">Addresses, network names, keys and serial numbers are replaced before you get it,
	  so it can go straight into an issue. Worth a look before you send it all the same.</p>
	 </fieldset>`)
}

// stationsSection is the radio stations kept on the device: a name and the address of the stream,
// which is the thing nobody wants to type on the device's own screen and the reason this page earns
// its place. They play without Home Assistant, which is the only radio a device on its own has.
func stationsSection(w http.ResponseWriter, token string) {
	list := home.OwnStations()
	fmt.Fprint(w, `<fieldset><legend>Radio stations on this device</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "stations", "sound")
	// One more row than there are stations, so there is always somewhere to add one; clearing a
	// name takes that station out.
	for i := 0; i <= len(list) && i < config.MaxOwnStations; i++ {
		var st config.Station
		if i < len(list) {
			st = list[i]
		}
		fmt.Fprintf(w, `<label for="n%d">Name</label>
		 <input id="n%d" name="name" value="%s" maxlength="40" autocomplete="off" placeholder="Station name">
		 <label for="u%d">Stream address</label>
		 <input id="u%d" name="url" value="%s" autocomplete="off" placeholder="https://…">
		 <p><button type="submit" name="play" value="%d" class="quiet">Play this one</button></p>`,
			i, i, html.EscapeString(st.Name), i, i, html.EscapeString(st.URL), i)
	}

	// What the device is doing, because a page with play buttons and no answer is a remote control
	// with no display — and on a Dot it is the only place this can be seen at all.
	if station, playing := home.Get().NowPlaying(); playing {
		if station == "" {
			station = "something"
		}
		fmt.Fprintf(w, `<p class="playing">Playing %s
		 <button type="submit" name="stop" value="1" class="quiet">Stop</button></p>`,
			html.EscapeString(station))
	}

	fmt.Fprint(w, `<p class="note">Play uses what is typed in the row, saved or not, so an address can
	 be tried before it is kept. Clear a name to take a station out. They show on the device under
	 Radio, as "On this device".</p>
	 <p><button type="submit">Save stations</button></p></form></fieldset>`)
}

// saveStations takes the rows the form posted, in the order they were in. Names and addresses come
// back as two lists of the same length, one row each.
// playRow plays the row whose Play button was pressed, with the name and address as they stand in
// the form. A row with nothing in it is a press on the spare row at the bottom, which is somebody
// asking for nothing.
func playRow(r *http.Request) string {
	i, err := strconv.Atoi(r.PostFormValue("play"))
	names, urls := r.PostForm["name"], r.PostForm["url"]
	if err != nil || i < 0 || i >= len(names) || i >= len(urls) {
		return "that row is not one of these"
	}
	name, u := strings.TrimSpace(names[i]), strings.TrimSpace(urls[i])
	switch {
	case name == "" && u == "":
		return "fill the row in first"
	case u == "":
		return name + " has no stream address"
	}
	if name == "" {
		name = "that stream"
	}
	if !home.Get().PlayStream(name, u) {
		return name + "'s address has to start with http:// or https://"
	}
	return ""
}

func saveStations(r *http.Request) string {
	names, urls := r.PostForm["name"], r.PostForm["url"]
	if len(names) != len(urls) {
		return "that form did not arrive whole"
	}
	var list []config.Station
	for i := range names {
		name, u := strings.TrimSpace(names[i]), strings.TrimSpace(urls[i])
		switch {
		case name == "" && u == "":
			continue // an empty row is one that was never filled in, or one being taken out
		case name == "":
			return "a station needs a name as well as an address"
		case u == "":
			return name + " has no stream address"
		case !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://"):
			return name + "'s address has to start with http:// or https://"
		}
		list = append(list, config.Station{Name: name, URL: u})
	}
	if err := home.SetOwnStations(list); err != nil {
		return "could not save them: " + err.Error()
	}
	return ""
}

// nameSection renames the device, behind what it does and the box that has to be ticked.
//
// What it does was worth checking rather than assuming: Home Assistant keys its entities on the
// device's MAC address (unique ids read "a1:b2:c3:d4:e5:f6/0/media_player/Speaker"), so a renamed
// device is the same device to it and keeps the entity ids it was given. Automations go on working.
// What changes is what Home Assistant shows, and the entity ids then no longer look like the name —
// which is confusing enough to warn about, and reason to rename in Home Assistant as well.
func nameSection(w http.ResponseWriter, token string) {
	name := deviceName()
	fmt.Fprint(w, `<fieldset><legend>Name</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "name", "general")
	fmt.Fprintf(w, `<label for="name">This device is called</label>
	 <input id="name" name="name" value="%s" maxlength="31" autocomplete="off">
	 <p class="bad"><strong>Home Assistant keeps the entity ids it already gave this device.</strong>
	  It knows the device by its address, not its name, so <code>%s</code> stays as it is and your
	  automations keep working — but it will no longer look like the new name, and Home Assistant will
	  go on showing the old one in places until you rename the device there too.</p>
	 <p class="note">On a device that does not use Home Assistant, none of that applies: the name is
	  only what the screen says. Naming a device before you hand it to somebody is what this is for.</p>
	 <p><label><input type="checkbox" name="understood" value="yes" style="width:auto">
	  I understand the entity ids in Home Assistant do not change with it</label></p>
	 <p class="note">The device restarts to announce the new name, and this page goes with it.</p>
	 <p><button type="submit">Rename and restart</button></p></form></fieldset>`,
		html.EscapeString(name), html.EscapeString("media_player."+layout.EntitySlug(name)+"_speaker"))
}

// houseSection is the word the devices in one house share.
//
// It is the whole of the security on announcements: a device takes one from anything on the network
// that knows the word, and ignores everything else. That is the right size for the thing — an
// announcement is a voice in a room, not a door — but it does mean the word is worth typing rather
// than leaving as something guessable, and it means the same word has to go on every device here.
func houseSection(w http.ResponseWriter, token string) {
	word := config.Get().Home.HouseWord
	fmt.Fprint(w, `<fieldset><legend>Announcements</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "house", "sound")
	fmt.Fprintf(w, `<label for="word">House word</label>
	 <input id="word" name="word" value="%s" maxlength="63" autocomplete="off">
	 <p class="note">Type the <strong>same word on every device in this house</strong>. They then find
	  each other on the network, and speaking to one plays it on the others. Anything that does not
	  have the word is ignored.</p>
	 <p class="note">Leave it empty to turn announcements off here: the device stops advertising itself
	  and stops taking them. Reminders set to go off on other devices use it too.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		html.EscapeString(word))
}

// saveHouse keeps the word, and then tells the web feature to look again: the port announcements
// arrive on is only listening while the word is set, and without the nudge it opens on that
// feature's own next look, up to a minute later. The first announcement after setup is exactly the
// one somebody is standing there waiting for.
func saveHouse(v string) string {
	word := strings.TrimSpace(v)
	if strings.ContainsAny(word, "\r\n") {
		return "a house word is one word on one line"
	}
	if err := config.Set().Home().HouseWord(word); err != nil {
		return "could not save it: " + err.Error()
	}
	slog.Info("setup page: the house word was set", "set", word != "")
	web.Wake()
	return ""
}

func lock(secured bool) string {
	if secured {
		return " 🔒"
	}
	return ""
}

// joinWifi adds a network and reports what went wrong, if anything. The library puts the old
// configuration back when the new network never comes up, so a wrong passphrase does not strand it.
func joinWifi(ctx context.Context, ssid, passphrase string) string {
	if ssid == "" {
		return "no network was named"
	}
	wifi.SettingUp(true)
	defer wifi.SettingUp(false)
	ctx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	if err := wifi.Join(ctx, ssid, passphrase); err != nil {
		return err.Error()
	}
	slog.Info("setup page: a network was added", "ssid", ssid)
	return ""
}

func selected(on bool) string {
	if on {
		return " selected"
	}
	return ""
}

func deviceName() string {
	if n := config.Get().Device.Name; n != "" {
		return n
	}
	return layout.DefaultName
}
